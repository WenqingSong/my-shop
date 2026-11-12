# 积分与会员等级设计（Points & Membership）

本文说明积分账户/积分流水、会员等级配置、用户会员信息与成长值流水的数据模型、不变量，以及 B1-04-08 实现的
会员等级判定、成长值账本与会员折扣计价。

- 积分账本（`user_points_accounts`/`user_points_logs`）由 B1-03-02 建表，**本任务不改动、不读写**（积分抵扣属 B1-04-05）。
- 会员等级、成长值与会员折扣由 B1-04-08 实现（`internal/logic/member`），并接入订单计价与退款链路。

## 1. 职责与边界

- 积分回答「用户当前有多少可用积分、每次增减从哪来、是否重复记账」（B1-04-05 实现）。
- 会员等级回答「平台的等级配置是什么、用户当前属于哪一级、累计成长值是多少、何时升级」。
- 会员折扣回答「某个等级对应几折、下单时能抵多少商品金额、快照写在哪一列」。
- 事实来源：单一 MySQL。
  - 积分：`user_points_accounts`（余额）、`user_points_logs`（流水，append-only）。
  - 会员：`member_levels`（等级配置）、`user_memberships`（用户当前等级与累计成长值）、`member_growth_logs`（成长值流水，append-only）。
  - 会员折扣快照：`orders.member_discount_amount`（B1-04-08 新增，见 `pricing.md` §5）。
- 不负责：优惠券（`coupon.md`）、钱包（`wallet.md`）、分销佣金（`distribution.md`）、运费（`freight.md`）、积分抵扣（Deferred，无实现）。
- 与钱包隔离：积分、成长值、钱包余额是三套账户，不互相换算；积分与成长值都不是货币，不可提现、不可兑换商品。

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

`growth_threshold` 唯一用于消除「两个等级同一门槛」的升级歧义。**「按 `level_no` 升序时 `growth_threshold` 严格递增」由管理端
在写事务内校验**（violate → 24002 并回滚），见 §5.1。`discount_rate_bp` 的合法区间 `1..10000` 由应用层校验，
DB 用 `INT UNSIGNED` 保证非负，不引入 CHECK（与项目既有迁移约定一致）。

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

**惰性建档**：用户没有会员行时视为「未入会，成长值 0」。读取路径（`GET /my/membership`、下单算折扣）**只解析等级、
不创建档案**；档案只在第一次成长值发放时由支付事务创建（`INSERT ... ON DUPLICATE KEY UPDATE id=id`）。

### 2.5 `member_growth_logs`（成长值流水，append-only）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK） |
| `change_amount` | BIGINT | 非空，带符号变动值：正=增加、负=扣减 |
| `growth_before` | BIGINT UNSIGNED | 非空，变动前成长值 |
| `growth_after` | BIGINT UNSIGNED | 非空，变动后成长值（必须 ≥ 0） |
| `change_type` | VARCHAR(32) | 非空，语义类型：当前仅实现 `order_reward`/`refund_deduct`（`admin_adjust` 见 §2.5 未实现说明） |
| `biz_type` | VARCHAR(32) | 非空，关联业务类型：当前仅实现 `order`/`refund` |
| `biz_id` | BIGINT UNSIGNED | 可空，关联业务 id |
| `idempotency_key` | VARCHAR(64) | 非空，幂等标识，`uk_idempotency` 全局唯一 |
| `remark` | VARCHAR(255) | 非空默认 '' |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_idempotency(idempotency_key)`、`idx_user_time(user_id, created_at)`、`idx_biz(biz_type, biz_id)`。
结构与 `user_points_logs` 同构，但**独立成表**：成长值与可用积分不是同一账本。

**已实现的流水类型**（B1-04-08）：

| `change_type` | `biz_type` | `biz_id` | 幂等键 | 触发 |
| --- | --- | --- | --- | --- |
| `order_reward` | `order` | 订单 id | `growth:order_reward:<order_id>` | 订单支付成功（`10→20`） |
| `refund_deduct` | `refund` | 退款单 id | `growth:refund_deduct:<refund_id>` | 退款成功（`refund_requests.status=60`） |

`admin_adjust` 未实现（无人工调账入口，Deferred）。

### 2.6 `orders.member_discount_amount`（会员折扣快照，B1-04-08 新增）

`INT UNSIGNED NOT NULL DEFAULT 0`，默认 0、迁移不回填。语义与金额守恒见 `pricing.md` §5 与 `order.md` §2.3。

## 3. 成长值口径与来源

- **口径**：成长值 = 已支付订单的**实付金额（整数分）**，即 1 分消费 = 1 成长值。
  因此 `user_memberships.growth_value` 等于「累计净消费金额（分）」；`member_levels.growth_threshold` 同样是整数分口径
  （例如门槛 `100000` = 累计消费 1000 元）。
- **来源**：仅订单支付成功（`orders.status=10→20`）发放，金额取 `orders.total_amount`（已扣除券与会员折扣后的实付）。
- **金额为 0 的订单**不发放成长值、不写 0 元流水。
- **扣减**：退款成功时按「该订单当初发放的成长值」原额扣减，见 §6.2。
- **不换算**：不建立「积分 → 成长值」或「成长值 → 积分」的自动换算，也不把积分余额复制到 `growth_value`。

## 4. 等级判定与升级规则

```text
member_levels(level_no, growth_threshold, discount_rate_bp, status)
        │ 1:N（按 growth_threshold 从小到大）
user_memberships(user_id, level_id, growth_value, upgraded_at)
        │ 1:N
member_growth_logs(user_id, change_amount, growth_before, growth_after, idempotency_key)
```

判定函数（`internal/logic/member` 的 `findLevelForGrowth`）：

```text
levelForGrowth(g) = { L | L.status = 1 且 L.growth_threshold ≤ g } 中 growth_threshold 最大者
                    （不存在时返回空 → 无适用等级）
```

规则：

1. **无适用等级**（平台尚未配置任何启用等级，或最低门槛 > 当前成长值）：成长值发放按 **no-op** 处理
   （记 warn 日志、不建档、不写流水、不阻塞支付），折扣按 `10000`（原价）。
2. **首次建档**：第一次发放成长值时，以 `levelForGrowth(0)` 得到的等级初始化 `user_memberships.level_id`。
3. **升级**：写入成长值后按新的 `growth_value` 重新解析等级；仅当解析出的等级与当前 `level_id` 不同才更新 `level_id`
   并写 `upgraded_at = NOW()`（同一事务）。
4. **只升不降**：退款扣减成长值后**不回退** `level_id`；降级、成长值过期、等级有效期均未建模（Deferred）。
5. **等级停用**：`status=0` 的等级不再参与新的等级判定，但已被用户档案引用的等级继续按其 `discount_rate_bp` 生效
   （快照语义，避免停用配置静默改变已入会用户权益）。
6. **未建档用户的折扣**：按 `levelForGrowth(0)` 解析，即门槛为 0 的启用等级；仍无则不打折。
   API 中 `level_id`/`level_no` 仍返回 `null`，用于区分「未建档」与「已建档于最低等级」。

## 5. 会员折扣与计价关系

### 5.1 等级配置维护约束（管理端）

写操作（创建/更新）在同一事务内先 `SELECT ... FOR UPDATE` 锁全部等级行，再校验：

| 校验 | 失败错误码 |
| --- | --- |
| `level_no` 为正整数；`name` trim 后 1~64 字符；`growth_threshold ≥ 0`；`discount_rate_bp ∈ [1,10000]`；`status ∈ {0,1}` | 24002 |
| 「按 `level_no` 升序时 `growth_threshold` 严格递增」 | 24002 |
| `level_no` / `growth_threshold` 唯一（DB 唯一键，1062） | 24004 |
| 删除被 `user_memberships.level_id` 引用的等级（FK RESTRICT，1451） | 24003 |

锁定全部等级行是为了让「唯一性 + 单调性」两项校验在并发写入下仍然成立（两个各自合法的写入合并后可能非法）。

### 5.2 折扣公式与边界

```text
raw_member_discount  = floor(original_amount × (10000 - discount_rate_bp) / 10000)
member_discount      = min(raw_member_discount, original_amount - coupon_discount)
member_discount      = pricing.Quote({OriginalAmount, CouponDiscount, MemberDiscount, PointsDiscount:0, ShippingFee}).MemberDiscount
total_amount         = original_amount - coupon_discount - member_discount + shipping_fee
```

折扣金额作为 `MemberDiscount` 槽位交给 `pricing.Quote`（唯一金额公式入口），订单落库取该入口的输出；
运费在优惠之后相加，不参与折扣（见 `pricing.md` §8）。

- 折扣作用于**商品金额**（Σ `order_items.price × quantity`），不折扣运费。
- 结果向下取整到分（整数分），无浮点、无小数。
- 第二条夹紧保证「各项优惠累计 ≤ 商品金额」，`total_amount ≥ 0` 恒成立；
  券已把商品金额抵满时会员折扣为 0（**会员折扣不突破订单金额边界**）。
- **叠加顺序（本任务冻结）**：先算券（券的资格与金额判定基于 `original_amount`、与历史语义一致），
  再算会员折扣（按 `original_amount` 计算后夹紧到券后剩余）。理由：券的资格与金额是既有已交付语义，不得因引入会员折扣而改变；
  会员折扣作为「第二项优惠」只需保证累计不超商品金额。
- **只影响一次算价**：折扣在下单时计算一次并写入 `orders.member_discount_amount` 快照；
  之后等级变更、配置变更、订单金额查询都不重算（与 `pricing.md` INV-P03 一致）。

### 5.3 与 `orders.discount_amount` 的关系

`orders.discount_amount` 的语义**仍然是「优惠券优惠」**，不得写入会员折扣。会员折扣使用独立列
`orders.member_discount_amount`，两者的合计不得超过 `original_amount`。

## 6. 事务边界与幂等

### 6.1 支付成功（订单 `10→20`）

单事务内依次完成（`internal/logic/order` 的 `Pay`）：

1. 订单条件更新 `status=10 → 20`（`RowsAffected` 闸门，重复支付 0 行 → 9002）；
2. 若用券，核销券（`locked→used`）；
3. **`IMember.AccrueForOrderInTx`**：读订单快照 → 解析等级 → `INSERT ... ON DUPLICATE KEY` 建档 →
   `SELECT ... FOR UPDATE` 锁档案行 → 写成长值流水 → 更新 `growth_value`（+ 必要时 `level_id`/`upgraded_at`）；
4. **`IDistribution.AccrueForOrderInTx`**：按订单实收金额计提一级佣金（见 `distribution.md`）。

任一步失败整体回滚：不出现「已支付但成长值未加」或「成长值已加但等级未升」。

并发：建档用 `ON DUPLICATE KEY UPDATE id=id`，随后 `FOR UPDATE` 锁行，使**同一用户**的并发成长值变更串行化，
`growth_before/growth_after` 不会交错（见 §9.1 的并发测试）。

### 6.2 退款成功（`refund_requests.status=60`）

在退款成功的同一事务内（`internal/logic/refund` 的 `Execute`）：

1. 订单 `20→70` 条件更新 + 恢复库存（既有）；
2. 退款状态推进到 `60`（既有）；
3. **`IDistribution.ReverseForRefundInTx`**：佣金条件更新 `status IN (10,20) → 30`（见 `distribution.md`）；
4. **`IMember.DeductForRefundInTx`**：按该订单的 `growth:order_reward:<order_id>` 流水原额扣减成长值；
5. 写退款审计（既有）。

`DeductForRefundInTx` 的细节：

- 订单从未发放过成长值（例如支付时平台未配置等级）→ no-op，不伪造流水；
- 扣减额 = `min(原发放额, 当前 growth_value)`，不出现负成长值（`BIGINT UNSIGNED` + 条件更新双保险）；
- 幂等键 `growth:refund_deduct:<refund_id>`，重复调用命中 1062 → no-op；
- 不回退 `level_id`（只升不降）。

### 6.3 幂等键设计

| 事件 | 幂等键 | 兜底约束 |
| --- | --- | --- |
| 订单支付发放成长值 | `growth:order_reward:<order_id>` | `member_growth_logs.uk_idempotency` |
| 退款扣减成长值 | `growth:refund_deduct:<refund_id>` | `member_growth_logs.uk_idempotency` |

重复业务事件（重复支付回调、退款重试）不会重复增减成长值：命中 1062 即视为已处理，返回成功且不改任何数值。

## 7. 业务不变量

### 7.1 积分

- INV-M01（一人一账户）：`user_points_accounts.user_id` 唯一；不存在两个积分余额。
- INV-M02（积分非负）：`points` 为 `BIGINT UNSIGNED`；业务侧还必须使用条件更新 `WHERE points >= ?` + `RowsAffected`。
- INV-M03（逐笔可追溯）：每次变动写一条流水，`points_after = points_before + change_amount`；流水只追加。
- INV-M04（事件幂等）：`idempotency_key` 全局唯一，重复写入 1062 即视为已处理。
- INV-M05（同事务）：积分余额变更与流水写入必须在同一 MySQL 事务内提交。

### 7.2 会员

- INV-M06（等级序号唯一）：`member_levels.level_no` 唯一。
- INV-M07（成长门槛唯一）：`member_levels.growth_threshold` 唯一。
- INV-M08（一人一条会员记录）：`user_memberships.user_id` 唯一。
- INV-M09（等级引用有效）：`user_memberships.level_id` 必须存在（FK RESTRICT），被引用的等级不可删除。
- INV-M10（成长值非负 + 可追溯）：`growth_value` 与 `growth_after` 为 `BIGINT UNSIGNED`；每次变动写
  `member_growth_logs`，满足 `growth_after = growth_before + change_amount`；余额更新使用条件更新 `WHERE growth_value = <before>`。
- INV-M11（成长值事件幂等）：`member_growth_logs.uk_idempotency` 全局唯一；同一订单/退款事件使用确定性键。
- INV-M12（等级变更与流水同事务）：`level_id`/`growth_value`/`upgraded_at` 与成长值流水同一事务提交。
- INV-M13（等级单调）：`level_no` 升序时 `growth_threshold` 严格递增（管理端写事务内校验）。
- INV-M14（只升不降）：成长值只随支付增加、随退款减少；`level_id` 不会因退款/降级而回退。
- INV-M15（折扣不越界）：`member_discount_amount ≤ original_amount - discount_amount`，且 `total_amount ≥ 0`。
- INV-M16（折扣只算一次）：会员折扣在下单时算一次并快照；后续配置/等级变化不回写历史订单。

## 8. 与订单、退款、分销和钱包的边界

- 订单：下单时读等级折扣率 → 计算并快照 `member_discount_amount`；支付成功发放成长值。会员折扣不改变订单状态机。
- 退款：退款成功扣减成长值；退款金额仍为 `orders.total_amount`（服务端派生，V1 整单全额），
  **不因会员折扣单独计算退款金额**。
- 分销：佣金基数使用「商品实收金额」（已扣除券与会员折扣），见 `distribution.md` §3.2；成长值与佣金是两套独立账本。
- 钱包：成长值不是货币，不入钱包、不可提现、不可抵扣余额；本模块不调用任何钱包接口。
- 积分：与积分账本完全隔离（不同表、不同幂等键前缀、不同流言语义）。

## 9. API 与权限

前台（`Auth`，作用于 `Principal.UserID`）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/members/levels` | 启用中的等级配置列表（按 `level_no` 升序） |
| GET | `/my/membership` | 本人当前等级、成长值、折扣率、下一等级进度 |
| GET | `/my/membership/growth-logs` | 本人成长值流水（分页，clamp 风格：`page<1` 取 1、`size>100` 截断） |

后台（`AdminAuth` + `RequirePermission`）：

| 方法 | 路径 | 权限码 | 说明 |
| --- | --- | --- | --- |
| GET | `/admin/member-levels` | `member:list` | 全部等级配置（含停用） |
| POST | `/admin/member-levels` | `member:create` | 创建等级 |
| PUT | `/admin/member-levels/:id` | `member:update` | 全量更新等级 |
| DELETE | `/admin/member-levels/:id` | `member:delete` | 删除等级（被引用 → 24003） |
| GET | `/admin/members/:user_id` | `member:view` | 用户会员信息（含 `has_record`） |

身份信任：用户侧身份只来自 `Principal.UserID`，不接受客户端传 `user_id`；后台来自 `AdminPrincipal` 并经权限码校验。

## 10. 错误码域

域 `20000-20999`（会员等级/成长值）：

| code | 语义 | HTTP |
| --- | --- | --- |
| 24001 | MEMBER_LEVEL_NOT_FOUND（等级配置不存在） | 404 |
| 24002 | MEMBER_LEVEL_INVALID（名称/门槛/折扣率/状态非法，或破坏单调性） | 400 |
| 24003 | MEMBER_LEVEL_IN_USE（等级被用户会员记录引用，不可删除） | 409 |
| 24004 | MEMBER_LEVEL_DUPLICATE（等级序号或成长值门槛已存在） | 409 |
| 24005 | MEMBER_GROWTH_INSUFFICIENT（成长值扣减超过余额，防御性拒绝） | 409 |

复用：`1001`（400）、`1002`（401）、`1003`（403）。

## 11. Migration

| 版本 | 文件 | 内容 |
| --- | --- | --- |
| `20261110000008` | `points` | `user_points_accounts` + `user_points_logs`（积分账户 + 流水，B1-03-02） |
| `20261110000013` | `member` | `member_levels` + `user_memberships` + `member_growth_logs`（B1-03-03） |
| `20261110000015` | `order_member_discount` | `orders` 增 `member_discount_amount`（B1-04-08，默认 0、无回填；含 down） |

前三者与 `20261110000013` 互不依赖：`20261110000013` 不修改、不复制积分账本；down 只删除本组三表。
`20261110000015` 是单列 additive 变更，down 只删除该列，不改动既有金额列与历史数据
（真实结构与约束由 `internal/migrations/marketing_schema_migration_test.go` 与
`internal/migrations/order_member_discount_migration_test.go` 在真实 MySQL 上验证）。

## 12. Deferred / 已知留白

- 积分业务（签到、消费返积分、积分抵扣、积分商城、积分过期）：属 B1-04-05，本模块不读写积分账本。
- 会员等级初始化数据（seed）：本任务不 seed 等级；平台未配置等级时成长值发放为 no-op，折扣为原价。
- 降级、等级有效期、成长值过期、成长值人工调账（`admin_adjust`）。
- 会员价（`products.vip_price` 类字段未建模）、会员专享商品/权益包。
- 成长值发放比例配置（当前固定 1 分消费 = 1 成长值）。
- 会员折扣与后续项（运费、积分抵扣）的叠加顺序；B1-04-03/05 引入时须更新 `pricing.md` 并补金额守恒测试。
