# 积分与会员等级设计（Points & Membership）

本文说明积分账户/积分流水、会员等级配置、用户会员信息与成长值流水的数据模型与不变量。
**当前只有数据库结构**：没有签到、消费返积分、积分抵扣、积分商城、等级计算、升级、会员折扣的业务代码。
B1-03-03 只补 Schema，不提前实现 `pricing.md` 中的会员优惠与积分抵扣。

## 1. 职责与边界

- 积分回答「用户当前有多少可用积分、每次增减从哪来、是否重复记账」。
- 会员等级回答「平台的等级配置是什么、用户当前属于哪一级、累计成长值是多少、何时升级」。
- 会员折扣回答「某个等级对应几折」，但**折扣如何参与订单计价尚未实现**（Deferred，见 `pricing.md`）。
- 事实来源：单一 MySQL。
  - 积分：`user_points_accounts`（余额）、`user_points_logs`（流水，append-only）。
  - 会员：`member_levels`（等级配置）、`user_memberships`（用户当前等级与累计成长值）、`member_growth_logs`（成长值流水，append-only）。
- 不负责：订单金额与优惠计算（`pricing.md`）、钱包（`wallet.md`）、优惠券（`coupon.md`）、分销佣金（`distribution.md`）。
- 与钱包隔离：积分、成长值、钱包余额是三套账户，不互相换算；积分与成长值都不是货币。

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

### 2.3 `member_levels`（会员等级配置）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `level_no` | INT UNSIGNED | 非空，等级序号（1、2、3…），`uk_level_no` 唯一 |
| `name` | VARCHAR(64) | 非空，等级名称 |
| `growth_threshold` | BIGINT UNSIGNED | 非空默认 0，达到该累计成长值可升级，`uk_growth_threshold` 唯一 |
| `discount_rate_bp` | INT UNSIGNED | 非空默认 10000，会员折扣率（万分比：10000=原价、9000=9 折、8800=8.8 折） |
| `status` | TINYINT | 非空默认 1，`1=启用`、`0=停用` |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

`growth_threshold` 唯一用于消除「两个等级同一门槛」的升级歧义；仅唯一不保证门槛随 `level_no` 单调递增，
单调性由管理端校验（B1-04）。`discount_rate_bp` 的合法区间 `1..10000` 由应用层校验，DB 用 `INT UNSIGNED`
保证非负，不引入 CHECK（与项目既有迁移约定一致）。

### 2.4 `user_memberships`（用户会员信息）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用 `users.id`（无 FK），`uk_user_id` 唯一 |
| `level_id` | BIGINT UNSIGNED | 非空，FK → `member_levels.id` ON DELETE RESTRICT |
| `growth_value` | BIGINT UNSIGNED | 非空默认 0，当前累计成长值（非负） |
| `upgraded_at` | DATETIME | 可空，最近一次等级变更时间；NULL=尚未升级过 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_user_id(user_id)` UNIQUE、`idx_level_id(level_id)`。
用户没有会员行时视为「未入会/默认等级」，由应用决定是否按最低等级处理；数据库不自动建行、不 seed 等级。

### 2.5 `member_growth_logs`（成长值流水，append-only）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK） |
| `change_amount` | BIGINT | 非空，带符号变动值：正=增加、负=扣减 |
| `growth_before` | BIGINT UNSIGNED | 非空，变动前成长值 |
| `growth_after` | BIGINT UNSIGNED | 非空，变动后成长值（必须 ≥ 0） |
| `change_type` | VARCHAR(32) | 非空，语义类型：`order_reward`/`refund_deduct`/`admin_adjust` 等 |
| `biz_type` | VARCHAR(32) | 非空，关联业务类型：`order`/`refund`/`manual` 等 |
| `biz_id` | BIGINT UNSIGNED | 可空，关联业务 id |
| `idempotency_key` | VARCHAR(64) | 非空，幂等标识，`uk_idempotency` 全局唯一 |
| `remark` | VARCHAR(255) | 非空默认 '' |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_idempotency(idempotency_key)`、`idx_user_time(user_id, created_at)`、`idx_biz(biz_type, biz_id)`。
结构与 `user_points_logs` 同构，但**独立成表**：成长值与可用积分不是同一账本。

## 3. 业务不变量

### 3.1 积分

- INV-M01（一人一账户）：`user_points_accounts.user_id` 唯一；不存在两个积分余额。
- INV-M02（积分非负）：`points` 为 `BIGINT UNSIGNED`，扣减越界时 MySQL 报错回滚；业务侧还必须使用条件更新
  `UPDATE user_points_accounts SET points = points - ? WHERE user_id = ? AND points >= ?` + `RowsAffected`，避免非严格模式下截断为 0。
- INV-M03（逐笔可追溯）：每次变动写一条流水，`points_after = points_before + change_amount`，且两者相等关系必须由写入方保证
  （DB 只保证 `points_after` 非负）；流水只追加，无 UPDATE/DELETE 业务路径。
- INV-M04（事件幂等）：`idempotency_key` 全局唯一；同一业务事件使用确定性键（如 `order_reward:<order_id>`、
  `refund_return:<refund_id>`、`sign_in:<user_id>:<date>`），重复写入 1062 即视为已处理。
- INV-M05（同事务）：积分余额变更与流水写入必须在同一 MySQL 事务内提交，任一步失败整体回滚。

### 3.2 会员

- INV-M06（等级序号唯一）：`member_levels.level_no` 唯一，一个序号只对应一个等级配置。
- INV-M07（成长门槛唯一）：`member_levels.growth_threshold` 唯一，同一门槛不允许出现两个等级。
- INV-M08（一人一条会员记录）：`user_memberships.user_id` 唯一；不存在两条当前等级记录。
- INV-M09（等级引用有效）：`user_memberships.level_id` 必须存在（FK RESTRICT），被用户引用的等级不可删除。
- INV-M10（成长值非负 + 可追溯）：`growth_value` 与 `growth_after` 为 `BIGINT UNSIGNED`；每次变动写
  `member_growth_logs`，满足 `growth_after = growth_before + change_amount`；余额变更使用条件更新
  `WHERE growth_value >= ?` 防御非严格模式截断。
- INV-M11（成长值事件幂等）：`member_growth_logs.uk_idempotency` 全局唯一；同一订单/退款事件使用确定性键，
  重复写入 1062 即视为已处理。
- INV-M12（等级变更与流水同事务）：`user_memberships.level_id/growth_value/upgraded_at` 的变更与成长值流水
  必须同一事务提交；不允许「成长值已加、等级未升」或反之以致账实不符。

## 4. 积分与成长值为什么必须分开

| 维度 | 积分 | 成长值 |
| --- | --- | --- |
| 业务问题 | 用户「能用多少」 | 用户「累计到哪一级」 |
| 是否可消费 | 可抵扣/兑换（Deferred） | 不消费，只用于等级判定 |
| 是否可减少 | 可扣减、可返还 | 退款/撤销时可扣减，但不因消费而减少 |
| 账本 | `user_points_accounts` + `user_points_logs` | `user_memberships.growth_value` + `member_growth_logs` |
| 幂等键示例 | `order_reward:<order_id>` | `growth:order_reward:<order_id>` |

**当前决策**：不建立「积分 → 成长值」的自动换算，也不把积分余额复制到 `growth_value`。
如果 B1-04 要让消费同时产生积分与成长值，必须在同一订单事件中分别写两套流水、各自幂等，
并在 `pricing.md`/本文记录换算规则后再实现；不得未经论证合并字段。

## 5. 等级升级所需的数据关系

```text
member_levels(level_no, growth_threshold, discount_rate_bp, status)
        │ 1:N（按 growth_threshold 从小到大）
user_memberships(user_id, level_id, growth_value, upgraded_at)
        │ 1:N
member_growth_logs(user_id, change_amount, growth_before, growth_after, idempotency_key)
```

升级判定（B1-04 实现，本任务不写代码）：

1. 目标等级必须 `status=1`，且 `user_memberships.growth_value >= member_levels.growth_threshold`；
2. 在写成长值流水的事务内，按目标等级更新 `user_memberships.level_id`、`upgraded_at`；
3. 升级只升不降（降级、成长值过期、等级有效期未建模，Deferred）；
4. 等级折扣 `discount_rate_bp` 只作用于商品金额，订单必须新增独立折扣快照列，**不得**改写
   `orders.discount_amount` 的优惠券语义（见 `pricing.md`）。

## 6. 与订单、退款和钱包的边界

**当前**：订单表没有积分抵扣、成长值、会员折扣列；下单/支付/退款都不读写积分、成长值与会员等级；
`pricing.md` 把会员优惠与积分抵扣列为 Deferred。

**目标（B1-04 及以后，需先更新 `pricing.md` 与 `order.md`）**：

- 下单抵扣：在订单事务内按服务端规则计算 `points_discount`，条件扣减积分 + 写流水（`change_type='order_deduct'`、
  `biz_type='order'`、`biz_id=order_id`），并在订单上保存抵扣金额/使用积分的快照列；
- 消费返积分/成长值：支付成功后按配置规则发放（`order_reward`），两套账本分别幂等，不得重复发放；
- 取消/退款返还：按原扣减明细精确返还（`refund_return`/`refund_deduct`），不得重复返还或超额；
- 会员折扣：按 `member_levels.discount_rate_bp` 计算，结果写订单独立快照列；与券/积分的叠加顺序由 Owner 决策后写入 `pricing.md`；
- 积分与钱包互不换算、互不抵扣；积分不能提现；成长值不能提现、不能兑换商品。

## 7. Migration

| 版本 | 文件 | 内容 |
| --- | --- | --- |
| `20261110000008` | `points` | `user_points_accounts` + `user_points_logs`（积分账户 + 流水，B1-03-02） |
| `20261110000013` | `member` | `member_levels` + `user_memberships` + `member_growth_logs`（等级配置 + 用户会员 + 成长值流水） |

两组迁移互不依赖：`20261110000013` 不修改、不复制积分账本；down 只删除本组三表，积分账本保留。
真实结构与约束由 `internal/migrations/marketing_schema_migration_test.go` 在真实 MySQL 上验证。

## 8. Deferred / 已知留白

- 签到、积分商城、积分兑换券、积分过期、积分冻结。
- 会员等级初始化数据、等级任务、等级有效期、降级、成长值过期。
- 会员折扣实际计价、会员价（`products.vip_price` 类字段未建模）。
- 积分抵扣比例、单笔上限、叠加规则与舍入（见 `pricing.md`）。
- 积分/会员/成长值业务错误码（B1-04 定义，本任务不新增错误码）。
