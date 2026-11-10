# 一级分销设计（Distribution）

本文说明一级推荐关系与订单佣金的数据模型、状态机、计佣与退款冲回机制、事务边界与不变量。
B1-04-08 实现了绑定、计佣、结算、冲回与查询；本项目**只做一级分销**，不做多级分销、佣金账户与提现。

## 1. 职责与边界

- 推荐关系回答「用户是谁推荐来的、一级推荐人是谁、关系何时绑定、能否改绑」。
- 佣金记录回答「哪个订单、买家是谁、受益推荐人是谁、按什么基数/比例计了多少佣金、当前什么状态、退款后冲回多少」。
- 事实来源：单一 MySQL。`user_referrals`（关系）、`distribution_commissions`（佣金）。
- 不负责：订单金额与支付（`order.md`）、退款状态机（`refund.md`）、钱包余额与流水（`wallet.md`）、佣金提现。
- **不建佣金账户**：佣金记录是**应收/已结算事实**，不是可消费余额；本模块不写钱包、不调用任何钱包接口、无提现入口。
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

### 2.2 绑定语义（已实现）

入口：`POST /referrals/bind`（登录用户，body 只有 `referrer_user_id`；被推荐人始终取 `Principal.UserID`，
**不接受客户端指定被推荐人**）。

| 场景 | 结果 |
| --- | --- |
| `referrer_user_id ≤ 0` | 1001（400） |
| 自己推荐自己 | 21003（400） |
| 推荐人不存在或 `users.status≠1` | 21005（400） |
| 已绑定且推荐人相同 | 幂等成功，返回既有关系（不新增行） |
| 已绑定且推荐人不同 | 21002（409），关系不变 |
| 绑定后形成环（含 2 环与更长链） | 21004（409），事务回滚，不留下关系行 |
| 其余 | 成功，返回关系 + 推荐人用户名 |

实现要点（`internal/logic/distribution` 的 `Bind`）：

1. 事务内按 `id ASC` 锁定「本人 + 推荐人」两行 `users`（`FOR UPDATE`），消除涉及同一用户的并发绑定窗口，
   并在锁内校验推荐人存在且启用；
2. 锁内读取本人 `user_referrals`（`FOR UPDATE`）判重：同推荐人幂等返回，不同推荐人拒绝；
3. 插入关系；并发重复由 `uk_user_id`（1062）兜底，命中后重新读取并做同样的幂等/拒绝判定；
4. **插入后做环检测**：从本人沿 `referrer_user_id` 链向上遍历（每一行用 `FOR UPDATE` 读最新已提交关系），
   若回到本人即成环 → 21004 回滚。遍历深度上限 200，超限按成环处理（保守拒绝而不是放行未知结构）。

**并发边界（已知）**：`users` 行锁 + 插入后环检测能消除同一对用户、以及任意两条共享顶点的并发绑定成环。
理论上多条互不共享顶点的边同时提交仍可能形成长环；V1 绑定入口单一、关系不可改绑，未引入全局串行锁。
若后续出现批量导入/换绑入口，需在此处补充全局串行化（见 §8）。

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
- `uk_refund_id` 保证同一退款单至多冲回一条佣金；MySQL 唯一索引允许多个 NULL（未冲回记录）。
- `base_amount`/`rate_bp`/`commission_amount` 是计算快照；后续改配置、改订单金额不得回写历史佣金。
- `buyer_user_id`/`beneficiary_user_id` 是身份快照；用户改名/删除不影响历史记录。
- `order_id` 物理 FK RESTRICT：佣金是资金事实，订单在佣金存在时不可删除。

### 3.2 计佣公式与快照（已实现）

计佣发生在**订单支付成功事务内**（`internal/logic/order` 的 `Pay` → `IDistribution.AccrueForOrderInTx`）：

```text
base_amount       = max(0, orders.original_amount - orders.discount_amount - orders.member_discount_amount)
                    // 商品实收金额：已扣除券与会员折扣，不含运费
rate_bp           = 500（V1 固定 5%）
commission_amount = floor(base_amount × rate_bp / 10000)   // 向下取整到分
```

- **基数口径已冻结**：商品实收金额（不含运费）。它与 `pricing.md` 的金额守恒一致，
  且在未来引入运费后仍然稳定（运费不参与计佣）。
- **比例口径**：V1 固定为代码常量 `commissionRateBp = 500`，写入 `rate_bp` 快照后不再随常量变化。
  比例的配置来源（系统配置/活动配置/商品配置）仍为 Deferred，不引入配置表。
- 写入条件与 no-op 情形：订单未支付 / 买家无推荐人 / 受益人与买家相同（防御） / `base_amount ≤ 0` /
  `commission_amount = 0` 时**不写记录**（避免 0 元佣金占用 `uk_order_id`）。
- 重复支付回调、重复请求由 `uk_order_id` 兜底：命中 1062 视为已计提，返回成功且无副作用。

### 3.3 佣金状态机

| from | to | 触发 | 说明 |
| --- | --- | --- | --- |
| 10 待结算 | 20 已结算 | 后台人工结算（`POST /admin/commissions/:id/settle`，`distribution:settle`） | `settled_at=NOW()` |
| 10 待结算 | 30 已冲回 | 退款成功 | `reversed_amount`、`reversed_at`、`refund_id`、`reversal_reason` |
| 20 已结算 | 30 已冲回 | 退款成功 | 同上；已结算佣金也必须可冲回 |

终态：30。`30→20/10` 不允许。

- 结算接口对已结算（20）**幂等成功**（返回当前记录，不重复写 `settled_at`）；对已冲回（30）返回 21007 且不改数据。
- 自动结算（订单完成/售后期结束触发）未实现，`10→20` 目前只由后台人工结算触发（Deferred）。

## 4. 退款冲回机制

退款成功（`refund_requests.status=60`，订单 `paid→refunded`）后，在**退款成功的同一事务内**冲回佣金
（`internal/logic/refund` 的 `Execute` → `IDistribution.ReverseForRefundInTx`）：

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

- 只冲回一次：`WHERE status IN (10,20)` 条件更新 + `RowsAffected`；重复执行 0 行即 no-op。
- 冲回金额与佣金同额：`reversed_amount` 直接取同一行的 `commission_amount`（`gdb.Raw`），
  不读后写，因此不存在「冲回超过原佣金」的路径。
- 同一退款单只冲回一条：`uk_refund_id` 兜底（1062 视为已冲回）。
- 没有佣金记录（订单无推荐人/未计佣）时，冲回是 no-op，不伪造记录。
- 冲回不回写 `orders`/`refund_requests`/钱包；佣金状态是独立事实。
- 订单取消（未支付）不产生佣金；若未来对已支付订单取消，`reversal_reason='cancel'`，同样走上述条件更新。

## 5. 业务不变量

- INV-D01（一人一个推荐人）：`user_referrals.user_id` 唯一；不存在两条推荐关系。
- INV-D02（禁止自荐）：`user_id <> referrer_user_id`（应用校验，21003；DB 不引入 CHECK）。
- INV-D03（推荐人有效）：绑定事务内校验推荐人存在且 `users.status=1`（21005）；已绑定用户不可改绑（21002）。
- INV-D04（只做一级）：`referrer_user_id` 是最终推荐人；不存在多级链/层级路径字段。
- INV-D05（一单一佣金）：`distribution_commissions.uk_order_id` 保证订单至多一条佣金，重复计提 1062。
- INV-D06（金额快照）：`base_amount`/`rate_bp`/`commission_amount` 写入后不随配置或订单变化回写。
- INV-D07（佣金非负且不超基数）：金额列为 `INT UNSIGNED`；`commission_amount = floor(base×rate/10000) ≤ base` 由公式保证。
- INV-D08（状态机）：仅 `10→20`、`10→30`、`20→30` 合法；`30` 终态；非法迁移 21007 且不改数据。
- INV-D09（冲回一次）：`status IN (10,20)` 条件更新 + `RowsAffected`；`uk_refund_id` 兜底同一退款单重复冲回。
- INV-D10（订单保护）：有佣金记录的订单不可删除（FK RESTRICT）。
- INV-D11（禁止环）：绑定后沿推荐人链遍历，回到本人即 21004 并回滚。
- INV-D12（佣金独立于钱包）：佣金不写 `wallet_accounts`/`wallet_transactions`，不产生可消费余额。

## 6. 跨模块关系

- `user_referrals.user_id`/`referrer_user_id` → `users.id`（软引用，无 FK）。
- `distribution_commissions.order_id` → `orders.id`（FK RESTRICT）；
  `refund_id` → `refund_requests.id`（软引用，无 FK）；
  `buyer_user_id`/`beneficiary_user_id` → `users.id`（软引用，无 FK）。
- 计佣入口：订单支付成功事务内（`order.Pay`），先读 `user_referrals`（买家的推荐人），
  再按订单金额快照计算并插入佣金；`uk_order_id` 兜底重复回调。
- 冲回入口：退款成功事务内（`refund.Execute`），按 `order_id` 条件更新；与订单状态推进、库存恢复、
  会员成长值扣减同事务。
- 会员：佣金基数依赖 `orders.member_discount_amount` 快照（会员折扣会降低计佣基数），
  但成长值与佣金是两套独立账本，互不换算。
- 钱包：佣金不直接入钱包；如后续需要「佣金余额」，必须新建佣金账户/流水或明确复用钱包的语义，
  并先更新 `wallet.md` 与本文，不得复用 `wallet_accounts.balance` 而不加区分。
- 建表经 golang-migrate `20261110000014_distribution.up.sql`（含 down），详见 `migration.md`。

## 7. API 与权限

前台（`Auth`，作用于 `Principal.UserID`）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/referrals/bind` | 绑定推荐人（一次性、不可改绑） |
| GET | `/my/referral` | 我的推荐关系 + 直接下级数量 |
| GET | `/my/commissions` | 我作为受益人的佣金明细（分页 clamp + 状态过滤） |
| GET | `/my/commissions/summary` | 我的佣金按状态汇总 |

后台（`AdminAuth` + `RequirePermission`）：

| 方法 | 路径 | 权限码 | 说明 |
| --- | --- | --- | --- |
| GET | `/admin/referrals` | `distribution:list` | 推荐关系列表（被推荐人/推荐人精确过滤，严格分页） |
| GET | `/admin/commissions` | `distribution:list` | 佣金列表（订单/买家/受益人/状态/时间过滤 + 白名单排序，严格分页） |
| GET | `/admin/commissions/:id` | `distribution:view` | 佣金详情 |
| POST | `/admin/commissions/:id/settle` | `distribution:settle` | 结算佣金（10→20） |

严格分页：`page<1`、`size<1`、`size>100` 一律 400（拒绝而非钳制）；`sort` 白名单仅 `id`/`created_at`，
`order` 仅 `asc`/`desc`；时间范围为 RFC3339（含时区偏移），左闭右开，非法或 `start ≥ end` 返回 400。

## 8. 错误码域

域 `21000-21999`（一级分销）：

| code | 语义 | HTTP |
| --- | --- | --- |
| 21001 | REFERRAL_NOT_FOUND（推荐关系不存在或不可见） | 404 |
| 21002 | REFERRAL_ALREADY_BOUND（已绑定其他推荐人，不可改绑） | 409 |
| 21003 | REFERRAL_SELF_BIND（不能绑定自己为推荐人） | 400 |
| 21004 | REFERRAL_CYCLE（绑定会形成推荐关系环） | 409 |
| 21005 | REFERRAL_INVALID_REFERRER（推荐人不存在或已禁用） | 400 |
| 21006 | COMMISSION_NOT_FOUND（佣金记录不存在） | 404 |
| 21007 | COMMISSION_INVALID_STATUS（已冲回不可结算等非法状态迁移） | 409 |

复用：`1001`（400）、`1002`（401）、`1003`（403）。

## 9. Deferred / 已知留白

- 多级分销、团队关系、层级佣金、平级返佣。
- 佣金账户、提现、结算批次、结算周期、税/手续费、发票。
- 佣金比例的配置来源（系统配置/活动配置/商品配置）；自动结算触发条件（订单完成/售后期结束）。
- 部分退款与部分冲回（V1 整单全额退款，`reversed_amount = commission_amount`）。
- 退款失败/关闭时是否恢复佣金（V1 只有退款成功才冲回；失败/关闭不冲回）。
- 已结算佣金已入钱包/已提现后的追偿与负向流水（V1 只记录冲回状态，不处理资金追回）。
- 邀请码/邀请链接、防刷与风控：当前绑定入口只接受 `referrer_user_id`，无邀请码校验、无频率限制、
  无「注册后才可绑定」的时间窗约束。
- 绑定链的全局串行化（见 §2.2 并发边界）。
