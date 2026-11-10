# 一级分销设计（Distribution）

本文说明一级推荐关系与订单佣金记录的长期数据模型、状态、退款冲回机制与不变量。
**B1-03-03 只落数据库结构**：没有绑定推荐人、计佣、结算、提现或退款冲回的业务代码；
本项目暂定**只做一级分销**，不实现多级分销、佣金账户、提现与复杂结算。

## 1. 职责与边界

- 推荐关系回答「用户是谁推荐来的、一级推荐人是谁、关系何时绑定、能否改绑」。
- 佣金记录回答「哪个订单、买家是谁、受益推荐人是谁、按什么基数/比例计了多少佣金、当前什么状态、退款后冲回多少」。
- 事实来源：单一 MySQL。`user_referrals`（关系）、`distribution_commissions`（佣金）。
- 不负责：订单金额与支付（`order.md`）、退款状态机（`refund.md`）、钱包余额与流水（`wallet.md`）、佣金提现。
- 不建佣金账户：佣金记录是**应收/已结算事实**，不是可消费余额；如需提现，必须另建账户与流水模型并由 Owner 决策。
- 不引入 MQ、Redis 缓存、分布式事务；计佣与冲回都在调用方 MySQL 事务内完成。

## 2. 推荐关系模型

### 2.1 `user_referrals`

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，被推荐人（下级），软引用 `users.id`；`uk_user_id` 唯一 |
| `referrer_user_id` | BIGINT UNSIGNED | 非空，一级推荐人，软引用 `users.id` |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP`，即绑定时间 |

索引：`PRIMARY(id)`、`uk_user_id(user_id)` UNIQUE、`idx_referrer_user(referrer_user_id, created_at)`。

- 一人一行、关系一次性绑定、不可改绑：`user_id` 唯一是机械保证；没有 UPDATE 业务路径。
- 只存一级：`referrer_user_id` 直接指向最终推荐人；不存 `parent_id` 链、不存层级路径。
- `created_at` 即绑定时间，不再冗余 `bound_at`。
- 用户软引用（无 FK）：沿用项目约定，用户删除不影响历史关系；推荐人是否存在由绑定事务校验。

### 2.2 绑定规则（应用层，B1-04 实现）

1. 不能自荐：`user_id <> referrer_user_id`；
2. 推荐人必须存在且状态正常；被推荐人必须尚未绑定（`uk_user_id` 兜底并发重复绑定，1062 视为已绑定）；
3. 绑定发生在注册/首次登录/邀请链接等入口，具体入口由 B1-04 契约决定；
4. 绑定成功后不因订单、退款、用户改名而改变；不允许「换绑到另一个推荐人」。

数据库只保证「一个用户至多一个推荐人」；自荐与推荐人有效性属于应用不变量（INV-D02/03）。

## 3. 佣金模型

### 3.1 `distribution_commissions`

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `order_id` | BIGINT UNSIGNED | 非空，FK → `orders.id` ON DELETE RESTRICT；`uk_order_id` 唯一 |
| `buyer_user_id` | BIGINT UNSIGNED | 非空，买家 user id 快照（软引用） |
| `beneficiary_user_id` | BIGINT UNSIGNED | 非空，受益推荐人 user id 快照（软引用） |
| `base_amount` | INT UNSIGNED | 非空，计佣基数快照（整数分） |
| `rate_bp` | INT UNSIGNED | 非空，佣金比例快照（万分比，500=5%） |
| `commission_amount` | INT UNSIGNED | 非空，佣金金额快照（整数分） |
| `status` | TINYINT | 非空默认 10，`10=待结算`、`20=已结算`、`30=已冲回` |
| `settled_at` | DATETIME | 可空，结算时间 |
| `reversed_amount` | INT UNSIGNED | 非空默认 0，已冲回金额（整单退款时等于 `commission_amount`） |
| `reversed_at` | DATETIME | 可空，冲回时间 |
| `refund_id` | BIGINT UNSIGNED | 可空，触发冲回的退款单 id（软引用 `refund_requests.id`）；`uk_refund_id` 唯一 |
| `reversal_reason` | VARCHAR(32) | 非空默认 ''，冲回原因：`refund`/`cancel` 等 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`uk_order_id(order_id)` UNIQUE、`uk_refund_id(refund_id)` UNIQUE、
`idx_beneficiary_status(beneficiary_user_id, status, created_at)`、`idx_status_created(status, created_at)`。

- `uk_order_id` 是「防止重复计提」的最终幂等约束：一个订单至多一条佣金记录，重复计提返回 1062。
  不额外引入 `idempotency_key`，因为它与订单唯一键表达同一规则。
- `uk_refund_id` 保证同一退款单至多冲回一条佣金；MySQL 唯一索引允许多个 NULL（未冲回记录）。
- `base_amount`/`rate_bp`/`commission_amount` 是计算快照；后续改配置、改订单金额不得回写历史佣金。
- `buyer_user_id`/`beneficiary_user_id` 是身份快照；用户改名/删除不影响历史记录。
- `order_id` 物理 FK RESTRICT：佣金是资金事实，订单在佣金存在时不可删除；退款记录用软引用，
  避免退款表删除级联影响佣金历史。

### 3.2 计佣公式与快照

计佣发生在订单支付成功后的业务事务内（B1-04 实现）。本文只固化公式与快照字段：

```text
base_amount       = 服务端确定的计佣基数（整数分）
rate_bp           = 计佣比例（万分比，500 = 5%）
commission_amount = floor(base_amount × rate_bp / 10000)   // 向下取整到分
```

- 基数、比例、舍入规则必须由 B1-04 在 `pricing.md`/本文明确后再实现；当前**不假设**基数是商品金额还是实付金额。
- `commission_amount` 必须 ≤ `base_amount`；比例上限 10000（100%）由应用校验，DB 只保证非负。
- 同一订单只计一次；重复支付回调/重复请求由 `uk_order_id` 兜底。
- 佣金不直接写入钱包：状态推进到「已结算」后是否入钱包、何时入钱包，属 B1-04/后续任务，
  且必须先定义佣金账户或复用钱包的语义，本文不预设。

### 3.3 佣金状态机

| from | to | 触发 | 说明 |
| --- | --- | --- | --- |
| 10 待结算 | 20 已结算 | 结算任务/人工确认 | `settled_at=NOW()` |
| 10 待结算 | 30 已冲回 | 退款成功/订单取消 | `reversed_amount`、`reversed_at`、`refund_id`、`reversal_reason` |
| 20 已结算 | 30 已冲回 | 退款成功 | 同上；已结算佣金也必须可冲回 |

终态：30。`20→30` 允许，`30→20/10` 不允许；非法迁移拒绝且无副作用。
本任务不实现结算任务；`10→20` 的触发条件（订单完成/售后期结束）由 B1-04 契约决定。

## 4. 退款冲回机制

退款成功（`refund_requests.status=60`，订单 `paid→refunded`）后，必须在退款成功事务内冲回佣金：

```sql
UPDATE distribution_commissions
   SET status = 30,
       reversed_amount = commission_amount,
       reversed_at = NOW(),
       refund_id = ?,
       reversal_reason = 'refund'
 WHERE order_id = ?
   AND status IN (10, 20);
```

不变量：

- 只冲回一次：`WHERE status IN (10,20)` 条件更新 + `RowsAffected`；重复执行 0 行。
- 同一退款单只冲回一条：`uk_refund_id` 兜底（1062 视为已冲回）。
- 冲回金额可审计：`reversed_amount` 与 `commission_amount` 同时保留；未来部分退款可只写部分金额，
  但 V1 退款是整单全额，`reversed_amount = commission_amount`。
- 没有佣金记录（订单无推荐人/未计佣）时，冲回是 no-op，不伪造记录。
- 冲回不回写 `orders`/`refund_requests`/钱包；佣金状态是独立事实。
- 订单取消（未支付）不产生佣金；若未来对已支付订单取消，`reversal_reason='cancel'`，同样走上述条件更新。

退款流程本身见 `refund.md`：V1 仅整单全额退款，退款成功推进订单状态并恢复库存；
佣金冲回是该事务的附加步骤，不改变退款状态机。

## 5. 业务不变量

- INV-D01（一人一个推荐人）：`user_referrals.user_id` 唯一；不存在两条推荐关系。
- INV-D02（禁止自荐）：`user_id <> referrer_user_id`（应用校验；DB 不引入 CHECK）。
- INV-D03（推荐人有效）：绑定事务内校验推荐人存在且状态正常；已绑定用户不可改绑。
- INV-D04（只做一级）：`referrer_user_id` 是最终推荐人；不存在多级链/层级路径字段。
- INV-D05（一单一佣金）：`distribution_commissions.uk_order_id` 保证订单至多一条佣金，重复计提 1062。
- INV-D06（金额快照）：`base_amount`/`rate_bp`/`commission_amount` 写入后不随配置或订单变化回写。
- INV-D07（佣金非负）：金额列为 `INT UNSIGNED`；`commission_amount ≤ base_amount` 由应用保证。
- INV-D08（状态机）：仅 `10→20`、`10→30`、`20→30` 合法；`30` 终态。
- INV-D09（冲回一次）：`status IN (10,20)` 条件更新 + `RowsAffected`；`uk_refund_id` 兜底同一退款单重复冲回。
- INV-D10（订单保护）：有佣金记录的订单不可删除（FK RESTRICT）。

## 6. 跨模块关系

- `user_referrals.user_id`/`referrer_user_id` → `users.id`（软引用，无 FK）。
- `distribution_commissions.order_id` → `orders.id`（FK RESTRICT）；
  `refund_id` → `refund_requests.id`（软引用，无 FK）；
  `buyer_user_id`/`beneficiary_user_id` → `users.id`（软引用，无 FK）。
- 计佣入口：订单支付成功事务内（B1-04），先读 `user_referrals`（买家的一级推荐人），
  再按当时配置计算并插入佣金；`uk_order_id` 兜底重复回调。
- 冲回入口：退款成功事务内（B1-04），按 `order_id` 条件更新；与订单状态推进、库存恢复同事务。
- 钱包：佣金不直接入钱包；如后续需要「佣金余额」，必须新建佣金账户/流水或明确复用钱包的语义，
  并先更新 `wallet.md` 与本文，不得复用 `wallet_accounts.balance` 而不加区分。
- 建表经 golang-migrate `20261110000014_distribution.up.sql`（含 down），详见 `migration.md`。

## 7. Deferred / 已知留白

- 多级分销、团队关系、层级佣金、平级返佣。
- 佣金账户、提现、结算批次、结算周期、税/手续费、发票。
- 计佣基数与比例的配置来源（系统配置/活动配置/商品配置）、部分退款与部分冲回规则。
- 退款失败/关闭时是否恢复佣金（V1 只有退款成功才冲回；失败/关闭不冲回）。
- 已结算佣金已入钱包/已提现后的追偿与负向流水（V1 只记录冲回状态，不处理资金追回）。
- 推荐关系绑定入口、邀请码/链接、防刷与风控。
- 分销业务错误码、权限码、管理端 API、前端页面与 OpenAPI。
