# 钱包与充值设计（Wallet）

本文说明用户钱包账户、资金流水、Mock 充值订单，以及余额支付/退款入账的资金一致性设计。

**当前状态（B1-04-04 之后）**：

- **已实现**：余额查询、资金流水分页查询、Mock 充值、账户按需初始化，以及供订单/退款模块复用的
  **事务内**扣款/入账能力（`IWallet.DebitForOrderInTx` / `IWallet.CreditForRefundInTx`）。
- **尚未接入**：订单支付入口 `order.Pay` 与退款执行入口 `refund.Execute` 仍只做状态推进，
  未调用钱包能力。原因是本任务基线（`origin/feat/p2-backend` @ `af512f9`）尚未合入 B1-04-03
  的订单/计价契约改动，按任务约束不抢改这两个文件；接入顺序与失败语义见 §7。

## 1. 职责与边界

- 钱包回答「用户有多少可用余额、每一分钱从哪来/到哪去、充值单是否已入账」。
- 事实来源：单一 MySQL。`wallet_accounts`（余额）、`wallet_transactions`（流水，append-only）、`recharge_orders`（充值单）。
- 不负责：订单状态机（见 `order.md`）、退款审批（见 `refund.md`）、积分（见 `member.md`）、真实支付渠道。
- 不接入微信/支付宝；`recharge_orders.pay_type` 仅承载 Mock 标记（`mock`），空串表示尚未支付。

## 2. 数据模型

Schema 由 B1-03-02 的迁移建立，本任务**未新增迁移、未改字段**：

- `wallet_accounts`：`20261110000006_wallet.up.sql`
- `wallet_transactions`：`20261110000006_wallet.up.sql`
- `recharge_orders`：`20261110000007_recharge_orders.up.sql`

### 2.1 `wallet_accounts`（钱包账户）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增；流水以它关联账户 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用 `users.id`（无 FK） |
| `balance` | INT UNSIGNED | 非空默认 0，可用余额（整数分） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP`，`updated_at` 随余额变更刷新 |

索引：`uk_user_id(user_id)` UNIQUE——一个用户至多一个钱包账户。

### 2.2 `wallet_transactions`（资金流水，append-only）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `account_id` | BIGINT UNSIGNED | 非空，FK → `wallet_accounts.id` `ON DELETE RESTRICT` |
| `user_id` | BIGINT UNSIGNED | 非空，冗余归属锚点（按用户查询/对账，无 FK） |
| `tx_type` | VARCHAR(32) | `recharge`/`order_pay`/`order_refund`（本任务实际写入的三类） |
| `direction` | TINYINT | `1=入账`、`2=出账`；金额恒为正，方向单独表达 |
| `amount` | INT UNSIGNED | 本次变动金额（整数分，> 0） |
| `balance_before` | INT UNSIGNED | 变动前余额 |
| `balance_after` | INT UNSIGNED | 变动后余额 |
| `biz_type` | VARCHAR(32) | `recharge`/`order`/`refund` |
| `biz_id` | BIGINT UNSIGNED | 关联业务 id（充值单/订单/退款申请），NULL=无具体业务对象 |
| `idempotency_key` | VARCHAR(64) | 非空，`uk_idempotency` 全局唯一 |
| `remark` | VARCHAR(255) | 非空默认 '' |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_idempotency(idempotency_key)`、`idx_account_id(account_id, id)`、`idx_user_id(user_id, id)`、`idx_biz(biz_type, biz_id)`；
FK `fk_wallet_transactions_account`（`ON DELETE RESTRICT`）。

### 2.3 `recharge_orders`（Mock 充值订单）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `order_no` | VARCHAR(32) | 非空，`uk_order_no` 全局唯一；本任务用 `(user_id, idempotency_key)` 确定性派生 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK） |
| `amount` | INT UNSIGNED | 充值金额（整数分，> 0） |
| `status` | TINYINT | 10=待支付、20=支付成功、30=已取消、40=支付失败 |
| `pay_type` | VARCHAR(16) | 成功时为 `mock`，未支付为空串 |
| `paid_at` | DATETIME | 仅 status=20 写入 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_order_no(order_no)`、`idx_user_status(user_id, status)`。

充值单与钱包账户**分离**：充值单描述「用户发起的一笔充值」，入账结果记录在 `wallet_transactions`，
两者通过 `biz_type='recharge'` + `biz_id=recharge_orders.id` 关联（无 FK）。

## 3. 业务不变量

| 编号 | 不变量 | 落地方式 |
| --- | --- | --- |
| INV-W01 | 一人一账户 | `wallet_accounts.user_id` 唯一；初始化用 `INSERT ... ON DUPLICATE KEY UPDATE`，并发由唯一键兜底 |
| INV-W02 | 余额非负 | 扣减用条件更新 `balance = balance - ? WHERE id = ? AND balance >= ?`，`RowsAffected=0` 即余额不足（22002） |
| INV-W03 | 流水可追溯 | 每次余额变更写一条流水，记录 `direction`/`amount`/`balance_before`/`balance_after`；只追加 |
| INV-W04 | 幂等 | `idempotency_key` 全局唯一，取值确定性：`recharge:<recharge_order_id>`、`order_pay:<order_id>`、`refund:<refund_id>` |
| INV-W05 | 同事务 | 余额变更与流水写入必须在同一 MySQL 事务内；不得「先改余额后补流水」 |
| INV-W06 | 账户必须存在 | 流水 `account_id` 有 FK；账户不存在时写入失败（1452），不静默跳过资金操作 |
| INV-W07 | 充值不重复入账 | 确定性充值单号 + `recharge:<id>` 幂等键 + 同事务状态 `10→20` |
| INV-W08 | 同用户资金操作串行 | 所有资金操作先对 `wallet_accounts` 行加锁（`SELECT ... FOR UPDATE`），并发请求在该行排队 |
| INV-W09 | 余额不溢出 | 入账前校验 `balance <= INT UNSIGNED 上限 - amount`，条件更新兜底（22010） |
| INV-W10 | 账实可核对 | 期初余额 + Σ入账 - Σ出账 = 当前余额；`balance_before`/`balance_after` 逐笔闭合 |

## 4. API 契约

三个用户端接口，全部挂在 `middleware.Auth` 之后，身份取 `Principal.UserID`；**不接受任何客户端传入的 `user_id`/`account_id`**。

| 方法 | 路径 | 说明 | 成功响应 |
| --- | --- | --- | --- |
| GET | `/wallet` | 查询本人余额（账户不存在时初始化余额 0，不写流水） | `{balance, updated_at}` |
| GET | `/wallet/transactions` | 本人资金流水分页（`page`/`size`/`tx_type`），按 `id` 倒序 | `{items[], total, page, size}` |
| POST | `/wallet/recharges` | Mock 充值（`amount`、`idempotency_key`） | 充值单 `{id, order_no, amount, status, pay_type, paid_at, created_at}` |

约束：

- 分页 `page < 1`、`size < 1 || size > 100`、`tx_type` 不在 `recharge/order_pay/order_refund` 白名单 → `1001`（400）。
- 流水出参包含 `tx_type`、`direction`（`credit`/`debit`）、`amount`、`balance_before`、`balance_after`、`biz_type`、`biz_id`、`remark`、`created_at`；不暴露 `account_id`、`user_id`、`idempotency_key`。
- 只读查询不会返回他人数据（`WHERE user_id = ?`），也不因「没有账户」返回 404。

## 5. Mock 充值流程

```text
校验开关（关闭 → 22009）
    → 校验金额（<=0 或 > 1,000,000 分 → 22003）
    → 校验幂等键（空或 > 64 字符 → 1001）
    → 派生充值单号 RC + sha256(user_id:key)[:30]
    → 单事务：
        1. 锁账户行（不存在则初始化为 0）
        2. 按单号锁读充值单：已存在 → 金额/状态核对 → 幂等返回或 22008/22005
        3. INSERT recharge_orders(status=10)
        4. 条件 UPDATE 余额（含上限保护，失败 → 22010）
        5. INSERT wallet_transactions（幂等键 recharge:<id>）
        6. UPDATE recharge_orders SET status=20, pay_type='mock', paid_at=NOW() WHERE status=10（0 行 → 22005）
    → 提交
```

要点：

- 充值单、余额、流水**同一事务**；任一步失败整体回滚（例如余额溢出时充值单不会残留）。
- 单笔上限 `1,000,000` 分（¥10,000）：既收敛「凭空造钱」的风险面，也远离 `INT UNSIGNED` 上限。
- 同一幂等键重复提交：命中确定性单号 → 金额一致且已成功时返回既有充值单（不重复入账）；
  金额不同 → `22008`；状态非成功 → `22005`。

## 6. 余额支付与退款入账（事务内能力）

钱包不掌握订单/退款状态机，只提供**必须由调用方在同一事务内调用**的资金原语：

```go
// 订单余额支付：幂等键 order_pay:<order_id>，tx_type=order_pay、biz_type=order、biz_id=order_id
DebitForOrderInTx(ctx, tx, service.WalletDebitInput{UserID, OrderID, Amount}) (*WalletFundResult, error)

// 退款退回余额：幂等键 refund:<refund_id>，tx_type=order_refund、biz_type=refund、biz_id=refund_id
CreditForRefundInTx(ctx, tx, service.WalletCreditInput{UserID, RefundID, OrderID, Amount}) (*WalletFundResult, error)
```

返回 `WalletFundResult.AlreadyApplied`：幂等键已存在且业务要素（用户/方向/类型/金额）一致时为 `true`，
表示**本次未再次变动资金**，调用方应据此把业务当作「已处理」而不是再推进一次。

失败语义：

| 场景 | 结果 |
| --- | --- |
| 余额不足 | `22002`，事务回滚，余额/流水/订单状态都不变 |
| 同一订单以不同金额再次扣款 | `22006`，事务回滚 |
| 同一退款以不同金额再次入账 / 已有异向流水 | `22007`，事务回滚 |
| 退款入账将溢出 | `22010`，事务回滚 |
| 账户不可用 | `22001`，事务回滚 |

调用方必须把「订单状态推进」「退款状态推进」「库存恢复」与上述调用放在**同一个** `g.DB().Transaction` 内，
不得在钱包方法内自开事务（当前实现严格遵守）。

## 7. 接入顺序（订单/退款模块）

订单支付（接入 `order.Pay` 的余额渠道时）：

```text
锁订单行 → 校验归属/状态 → 读取 total_amount
  → 同事务：DebitForOrderInTx → orders 10→20（条件更新 + paid_at + pay_type='balance'）
  → 提交 → 回读订单
```

退款执行（接入 `refund.Execute` 成功分支时）：

```text
锁订单行 → 订单 20→70 + 恢复库存（CompleteRefundInTx）
  → 同事务：CreditForRefundInTx → refund_requests → 60
  → 提交
```

**只有原支付渠道为余额（`orders.pay_type='balance'`）才允许调用 `CreditForRefundInTx`**；
其它渠道不得增加钱包余额。

## 8. 安全边界

- Mock 充值默认关闭：`wallet.mock_recharge.enabled`（`WALLET_MOCK_RECHARGE_ENABLED`），代码/YAML 默认 `false`。
- 部署严格模式（`DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true`）下若开启该开关，进程**启动失败**（`boot.ValidateConfig` → `config.ValidateMockRechargeDisabled`），
  避免生产环境出现「任意登录用户凭空增加余额」的公开能力。
- 金额只从服务端派生（充值金额来自请求但受上限约束；订单金额来自订单行，退款金额来自退款单），不接受客户端传入「入账金额」。
- 日志只记录 `user_id`/`recharge_id`/`amount`/`balance`，不输出幂等键以外的敏感信息；钱包表不含 PII。

## 9. 错误码（域 22000-21999）

| code | 语义 | HTTP | 使用点 |
| --- | --- | --- | --- |
| 22001 | 钱包账户不存在或初始化失败 | 500 | 账户初始化/锁读失败、幂等回放时流水缺失 |
| 22002 | 余额不足 | 409 | `DebitForOrderInTx` 条件更新 0 行 |
| 22003 | 充值金额非法 | 400 | 充值金额 <=0 或超上限 |
| 22004 | 充值订单不存在 | 404 | 回读充值单失败/归属不符（防枚举） |
| 22005 | 充值订单状态不允许操作 | 409 | 状态非 20 的幂等回放、`10→20` 条件更新 0 行 |
| 22006 | 重复支付 | 409 | 同一订单的 `order_pay:<id>` 已存在但金额/要素不符 |
| 22007 | 退款入账失败 | 409 | 同一退款的 `refund:<id>` 已存在但金额/要素不符 |
| 22008 | 资金流水幂等冲突 | 409 | 充值幂等键冲突、流水唯一键兜底 |
| 22009 | Mock 充值未启用 | 403 | 安全开关关闭 |
| 22010 | 金额超出可表示范围 | 409 | 余额入账将超过 `INT UNSIGNED` 上限 |

## 10. Deferred / 已知留白

- 订单支付与退款执行入口的实际接入（阻塞于 B1-04-03 未合入，见 §7 与交付报告）。
- 充值单查询接口（列表/详情）、充值取消与过期。
- 充值方案/套餐（`recharge_plans`）：未获 Owner 决策，不建表、不建迁移。
- 冻结余额/在途资金、多币种、流水冲正（红冲）、提现与对账任务。
- 钱包流水的后台管理与人工调账接口。
