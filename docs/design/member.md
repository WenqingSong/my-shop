# 积分与会员权益设计（Points & Membership）

本文说明积分账户/积分流水的数据模型与不变量，并划清与后续会员等级的边界。**当前只有积分数据库结构**：
没有签到、消费返积分、积分抵扣、积分商城或会员等级/折扣的业务代码。

## 1. 职责与边界

- 积分回答「用户当前有多少积分、每次增减从哪来、是否重复记账」。
- 事实来源：单一 MySQL。`user_points_accounts`（余额）、`user_points_logs`（流水，append-only）。
- 不负责：订单金额与优惠计算（见 `pricing.md`）、钱包（见 `wallet.md`）、会员等级/折扣（未建模，Deferred）。
- 与钱包隔离：积分与余额是两套账户、两套流水，不互相换算；积分不是货币。

## 2. 数据模型

### 2.1 `user_points_accounts`（积分账户）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用 `users.id`（无 FK） |
| `points` | BIGINT UNSIGNED | 非空默认 0，当前积分（整数，非负） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_user_id(user_id)` UNIQUE——一个用户至多一个积分账户。

### 2.2 `user_points_logs`（积分流水，append-only）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK） |
| `change_amount` | BIGINT | 非空，带符号变动值：正=增加、负=扣减 |
| `points_before` | BIGINT UNSIGNED | 非空，变动前积分 |
| `points_after` | BIGINT UNSIGNED | 非空，变动后积分（必须 ≥ 0） |
| `change_type` | VARCHAR(32) | 非空，语义类型：`sign_in`/`order_reward`/`order_deduct`/`refund_return`/`admin_adjust` 等 |
| `biz_type` | VARCHAR(32) | 非空，关联业务类型：`order`/`refund`/`sign`/`manual` 等 |
| `biz_id` | BIGINT UNSIGNED | 可空，关联业务 id |
| `idempotency_key` | VARCHAR(64) | 非空，幂等标识，`uk_idempotency` 全局唯一 |
| `remark` | VARCHAR(255) | 非空默认 '' |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_idempotency(idempotency_key)`、`idx_user_time(user_id, created_at)`、`idx_biz(biz_type, biz_id)`。

## 3. 业务不变量

- INV-M01（一人一账户）：`user_points_accounts.user_id` 唯一；不存在两个积分余额。
- INV-M02（积分非负）：`points` 为 `BIGINT UNSIGNED`，扣减越界时 MySQL 报错回滚；业务侧还必须使用条件更新
  `UPDATE user_points_accounts SET points = points - ? WHERE user_id = ? AND points >= ?` + `RowsAffected`，避免非严格模式下截断为 0。
- INV-M03（逐笔可追溯）：每次变动写一条流水，`points_after = points_before + change_amount`，且两者相等关系必须由写入方保证
  （DB 只保证 `points_after` 非负）；流水只追加，无 UPDATE/DELETE 业务路径。
- INV-M04（事件幂等）：`idempotency_key` 全局唯一；同一业务事件使用确定性键（如 `order_reward:<order_id>`、
  `refund_return:<refund_id>`、`sign_in:<user_id>:<date>`），重复写入 1062 即视为已处理。
- INV-M05（同事务）：积分余额变更与流水写入必须在同一 MySQL 事务内提交，任一步失败整体回滚。

## 4. 与订单、退款和钱包的边界

**当前**：订单表没有积分抵扣列，下单/支付/退款都不读写积分；`pricing.md` 把积分抵扣列为 Deferred。

**目标（B1-04 及以后，需先更新 `pricing.md` 与 `order.md`）**：

- 下单抵扣：在订单事务内按服务端规则计算 `points_discount`，条件扣减积分 + 写流水（`change_type='order_deduct'`、
  `biz_type='order'`、`biz_id=order_id`），并在订单上保存抵扣金额/使用积分的快照列；
- 消费返积分：支付成功后按配置比例发放（`order_reward`），必须幂等，不得重复发放；
- 取消/退款返还：按原扣减明细精确返还（`refund_return`），不得重复返还或超额；
- 积分与钱包互不换算、互不抵扣；积分不能提现。

## 5. 与会员等级的边界

- 当前**没有**会员等级表、等级折扣、升级规则、会员价；`pricing.md` 的会员优惠为 Deferred。
- 若后续引入会员等级，应单独新增等级定义/用户等级表，并明确：等级折扣只作用于商品金额、写入订单独立快照列、
  与券/积分的叠加顺序经 Owner 决策后写入 `pricing.md`。不得把等级折扣塞进 `discount_amount` 的券语义。
- 积分可作为等级成长值来源，但成长值与可用积分是两种语义，需分别建模，不可复用同一余额列。

## 6. Deferred / 已知留白

- 签到、积分商城、积分兑换券、积分过期、积分冻结。
- 会员等级/成长值/折扣与会员价。
- 积分抵扣比例、单笔上限、叠加规则与舍入（见 `pricing.md`）。
- 积分业务错误码（B1-04 定义，本任务不新增错误码）。
