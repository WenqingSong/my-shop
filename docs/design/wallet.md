# 钱包与充值设计（Wallet）

本文说明用户钱包账户、资金流水与 Mock 充值订单的数据模型、不变量与后续接入边界。**当前只有数据库结构**：
没有充值/余额支付 API，订单支付与退款也不读写钱包；相关业务在 B1-04 实现。

## 1. 职责与边界

- 钱包回答「用户有多少可用余额、每一分钱从哪来/到哪去、充值单是否已入账」。
- 事实来源：单一 MySQL。`wallet_accounts`（余额）、`wallet_transactions`（流水，append-only）、`recharge_orders`（充值单）。
- 不负责：订单状态机（见 `order.md`）、退款审批（见 `refund.md`）、积分（见 `member.md`）、真实支付渠道。
- 不接入微信/支付宝；支付方式字段仅承载 Mock 与未来内部渠道标识。

## 2. 数据模型

### 2.1 `wallet_accounts`（钱包账户）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增；流水以它关联账户 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用 `users.id`（无 FK，避免删除用户级联丢失资金记录） |
| `balance` | INT UNSIGNED | 非空默认 0，可用余额（整数分） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_user_id(user_id)` UNIQUE——一个用户至多一个钱包账户。

### 2.2 `wallet_transactions`（资金流水，append-only）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `account_id` | BIGINT UNSIGNED | 非空，FK → `wallet_accounts.id` `ON DELETE RESTRICT` |
| `user_id` | BIGINT UNSIGNED | 非空，冗余归属锚点（按用户对账/查询，无 FK） |
| `tx_type` | VARCHAR(32) | 非空，交易类型：`recharge`/`order_pay`/`order_refund`/`manual_adjust` 等 |
| `direction` | TINYINT | 非空，`1=入账`、`2=出账`；金额恒为正，方向单独表达 |
| `amount` | INT UNSIGNED | 非空，本次变动金额（整数分，> 0） |
| `balance_before` | INT UNSIGNED | 非空，变动前余额 |
| `balance_after` | INT UNSIGNED | 非空，变动后余额 |
| `biz_type` | VARCHAR(32) | 非空，关联业务类型：`recharge`/`order`/`refund`/`manual` 等 |
| `biz_id` | BIGINT UNSIGNED | 可空，关联业务 id；NULL=无具体业务对象（如手工调整） |
| `idempotency_key` | VARCHAR(64) | 非空，幂等标识，`uk_idempotency` 全局唯一 |
| `remark` | VARCHAR(255) | 非空默认 '' |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_idempotency(idempotency_key)`、`idx_account_id(account_id, id)`、`idx_user_id(user_id, id)`、`idx_biz(biz_type, biz_id)`；
FK `fk_wallet_transactions_account`（`ON DELETE RESTRICT`）。

### 2.3 `recharge_orders`（Mock 充值订单）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `order_no` | VARCHAR(32) | 非空，`uk_order_no` 全局唯一充值单号 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK） |
| `amount` | INT UNSIGNED | 非空，充值金额（整数分，> 0） |
| `status` | TINYINT | 非空默认 10：10=待支付、20=支付成功、30=已取消、40=支付失败 |
| `pay_type` | VARCHAR(16) | 非空默认 ''，支付方式（当前 Mock，如 `mock`） |
| `paid_at` | DATETIME | 可空，支付完成时间（仅 status=20 写入） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_order_no(order_no)`、`idx_user_status(user_id, status)`。

充值订单与钱包账户**分离**：充值单只描述「用户发起的一笔充值」，入账结果记录在 `wallet_transactions`，
两者通过 `user_id` 与流水的 `biz_type='recharge'` + `biz_id=recharge_orders.id` 关联（无 FK）。

## 3. 业务不变量

- INV-W01（一人一账户）：`wallet_accounts.user_id` 唯一；不存在「同一用户两个余额」。
- INV-W02（余额非负）：`balance` 为 `INT UNSIGNED`，扣减为负时 MySQL 报错回滚；业务侧还必须使用条件更新
  `UPDATE wallet_accounts SET balance = balance - ? WHERE id = ? AND balance >= ?` 并以 `RowsAffected` 判定，
  不依赖 `sql_mode` 是否严格（非严格模式下无符号下溢会被截断为 0 并产生 warning，条件更新可避免这种静默截断）。
- INV-W03（流水可追溯）：每次余额变更必须写一条流水，记录 `direction`/`amount`/`balance_before`/`balance_after`；
  流水只追加，无 UPDATE/DELETE 业务路径，不允许事后改写金额或余额。
- INV-W04（幂等）：`wallet_transactions.idempotency_key` 全局唯一；同一业务请求必须使用确定性键
  （如 `recharge:<id>`、`order_pay:<order_id>`、`refund:<refund_id>`），重复写入命中 1062 即视为已处理。
- INV-W05（同事务）：余额变更与流水写入必须在同一个 MySQL 事务内提交，禁止「先改余额后补流水」或跨系统事务。
- INV-W06（账户必须存在）：流水 `account_id` 有外键，账户不存在时写入失败（1452），不得静默跳过资金操作；
  账户删除受 `ON DELETE RESTRICT` 阻断，必须先处理流水。
- INV-W07（充值不重复入账）：同一充值单只允许一条成功入账流水，由确定性幂等键 `recharge:<recharge_order_id>`
  + 唯一索引保证；充值状态 `10→20` 与入账同事务。

## 4. 与订单支付、退款的关系

**当前（B1-03-02 之后、B1-04 之前）**：订单 Mock 支付只推进 `orders.status/paid_at/pay_type/transaction_no`，
不读写钱包；退款成功只推进订单与恢复库存，不产生资金流水。

**目标（B1-04 实现，需先更新本文）**：

- 余额支付：在同一事务内「订单 `10→20` + 钱包条件扣减 + 写 `wallet_transactions`（`tx_type='order_pay'`、
  `biz_type='order'`、`biz_id=order_id`、幂等键 `order_pay:<order_id>`）」；
- 退款入账：退款成功事务内「订单 `20→70` + 库存恢复 + 退款状态 + 钱包入账 + 流水（`tx_type='order_refund'`、
  `biz_type='refund'`、`biz_id=refund_id`、幂等键 `refund:<refund_id>`）」；
- 余额不足返回明确业务错误（不得部分扣减）；重复请求由唯一幂等键与订单状态机双重拦截。
- 钱包与订单**无外键**，通过业务 id 软关联；两者在同一 MySQL，可同事务，无跨系统一致性问题。

## 5. 失败与并发语义

- 并发扣减：条件 UPDATE 由 InnoDB 行锁串行化，仅余额足够者成功；失败方 `RowsAffected=0` → 余额不足。
- 并发入账：唯一幂等键保证同一业务事件只入账一次；后到者 1062，需回读既有流水并返回幂等成功。
- 部分失败：余额更新与流水写入同事务，任一步失败整体回滚，不存在「余额变了没有流水」或反之。
- 账户缺失：流水插入命中 1452 → 明确错误（账户不存在），不得静默忽略或自动补零余额。

## 6. 安全边界

- 无 HTTP 接口；后续接口必须从 `Principal.UserID` 取归属，不接受请求体 `user_id`/`account_id`。
- 金额只从服务端充值单/订单派生，不接受客户端传入入账金额。
- 日志与错误不得输出完整幂等键以外的敏感信息；钱包表不含 PII。

## 7. Deferred / 已知留白

- 充值 API、Mock 支付回调、余额支付、退款入账、余额对账与提现。
- 冻结余额/在途资金、多币种、钱包流水冲正（红冲）语义。
- 充值优惠（赠送金额）、充值方案、过期与退款。
- 钱包业务错误码（B1-04 定义，本任务不新增错误码）。
