# 优惠券设计（Coupon）

本文面向项目接手者，说明「平台通用固定金额满减券」的架构、数据模型、状态机、领取/用券的一致性模型与权限边界。事实来源为 `coupon-v1` APPROVED Contract 与最终实现。

## 1. 职责与边界

优惠券回答「管理员发了什么券、用户领了哪些券、下单时如何抵扣」：管理员创建/查询优惠券模板并停止新领取；登录用户领取（每用户每模板至多一张、发放总量不超发、并发不重复/不超发）；普通订单可选一张自己的券，服务端重读 SKU 价格计算原始金额、校验券资格后计算优惠与应付金额并写入快照；订单创建与优惠券预占同事务；Mock 支付核销、取消/超时释放、退款不返券。

边界（V1 即最终版本）：仅支持平台通用固定金额满减券（整数分）；**不支持**百分比折扣、指定商品/品类/品牌券、多券叠加、优惠分摊到多订单项、秒杀订单用券、结算/计价规则引擎、积分抵扣、会员折扣、自动发券、转赠、营销推荐；**不引入** Redis 优惠券缓存与分布式锁；**不实现**前端。事实来源为单一 MySQL；Redis 仅会话；无 MQ、无跨系统事务。

## 2. 数据模型

### 2.1 `coupon_templates`（优惠券模板）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `name` | VARCHAR(64) | 非空，模板名 |
| `threshold_amount` | INT UNSIGNED | 非空，满减门槛（整数分，≥0，0=无门槛） |
| `discount_amount` | INT UNSIGNED | 非空，减免金额（整数分，>0） |
| `total_quantity` | INT UNSIGNED | 非空，发放总量（>0） |
| `issued_quantity` | INT UNSIGNED | 非空默认 0，已发放数量 |
| `claim_start_time` | DATETIME | 非空，领取开始 |
| `claim_end_time` | DATETIME | 非空，领取结束 |
| `use_start_time` | DATETIME | 非空，使用开始 |
| `use_end_time` | DATETIME | 非空，使用结束 |
| `status` | TINYINT | 非空默认 1，`1=正常可领取`、`0=已停止新领取` |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`。

「未开始/已结束领取」由 `claim_start_time`/`claim_end_time` 与 MySQL `NOW()` 比较派生，**不设冗余终态**（沿用秒杀「已结束由 `end_time` 派生」惯例）；「停止新领取」用 `status` 显式表示（管理员动作，仅阻断新领取、不影响已领取券）。使用有效期由 `use_start_time`/`use_end_time` 派生。

### 2.2 `user_coupons`（用户券实例）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），指向 `users.id` |
| `template_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），指向 `coupon_templates.id` |
| `status` | TINYINT | 非空，`10=可用`、`20=已预占`、`30=已使用`（见状态机） |
| `order_id` | BIGINT UNSIGNED | 可空，当前预占/核销该券的订单 id（释放时清空） |
| `received_at` | DATETIME | 领取时间 |
| `locked_at` | DATETIME | 可空，预占时间 |
| `used_at` | DATETIME | 可空，核销时间 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`uk_user_template(user_id, template_id)` UNIQUE、`idx_user_id(user_id)`、`idx_order_id(order_id)`。

## 3. 状态机与合法迁移

`user_coupons.status` 三态，**不引入「已释放」终态**（释放 = 回到可用）：

| from | to | 触发者 | 备注 |
| --- | --- | --- | --- |
| 可用(10) | 已预占(20) | 下单 | 同订单创建事务内条件更新 |
| 已预占(20) | 已使用(30) | Mock 支付成功 | 条件更新 + RowsAffected，终态 |
| 已预占(20) | 可用(10) | 取消/超时取消 | 条件更新 + RowsAffected，清空 `order_id`/`locked_at` |

终态：已使用(30)。非法迁移拒绝且无副作用。释放 = `locked → available`，券在有效期内可再次用于新订单，不是丢券终态。

「已预占」用独立 `status` 列（非由订单状态派生），与订单生命周期在**同一事务内联动**但存储解耦：预占/核销/释放均带 `user_id`/`status` 归属校验，与对应订单状态变更同事务，重复请求不重复释放/核销。

## 4. 业务不变量

- INV-C01（领取唯一）：同一 `(user_id, template_id)` 至多一张券（`uk_user_template` + 1062 分流）。
- INV-C02（总量不超发）：`issued_quantity ≤ total_quantity`（条件更新 `WHERE issued_quantity < total_quantity` + RowsAffected）。
- INV-C03（券状态机）：仅 `10→20→30` 与 `20→10` 合法；`30` 终态。
- INV-C04（预占唯一）：一张券同一时刻至多被一个订单占用（`WHERE status=available` 条件更新 + RowsAffected）。
- INV-C05（订单-券同事务）：订单创建与预占同一 MySQL 事务，任一步失败整体回滚，不残留「已预占」。
- INV-C06（服务端金额与快照）：`original = Σ skus.price×quantity`；`discount = min(模板.discount_amount, original)`（≤ original）；`total = original - discount`；不信任客户端金额；下单后改价/改模板不改变快照。
- INV-C08（核销/释放原子）：支付核销 `locked→used`、取消/超时 `locked→available` 均条件更新 + RowsAffected 只生效一次；退款不返券。
- INV-C09（归属隔离）：券仅本人可见可用；越权/不存在统一 19003（404），无写入。
- INV-C10（幂等含 coupon_id）：同 `(user_id, idempotency_key)` 同 `request_hash`（含 coupon_id）→ 返回既有订单；同 key 不同内容 → 9006。

## 5. 一致性模型与失败语义

- 事实来源：单一 MySQL。`coupon_templates`（模板事实）、`user_coupons`（券实例事实）、`orders`（订单事实）、`inventories`（库存事实）。Redis 仅会话；无 MQ、无跨系统事务。
- 领取（单事务，锁无关）：读模板校验资格 → 原子闸门 `UPDATE coupon_templates SET issued_quantity = issued_quantity + 1 WHERE id=? AND status=1 AND issued_quantity < total_quantity AND claim_start_time <= NOW() AND claim_end_time > NOW()`（RowsAffected=0 → 19006）→ 插入 `user_coupons`（1062 → 19004，回滚使 `issued_quantity` 不落库）。并发正确性由条件更新 + 唯一约束兜底，无需 `FOR UPDATE`。
- 下单用券（单事务）：订单 + 订单项 + 扣库存 + 预占同事务提交；预占失败（券并发被占/不可用）→ 整体回滚，券不残留 `locked`、库存不扣。
- 支付/取消：`Pay` 改为事务「订单 10→20 + 券核销」；`cancelInTx`（取消与超时共用）「订单 10→60 + 恢复库存 + 券释放」。核销/释放 RowsAffected=0 属不可达防御分支（下单已原子预占）。
- 退款：不触碰 `user_coupons`（退款不返券，券保持 `used`）。
- 超时取消：沿用现有 scanner + 懒取消，`cancelInTx` 内统一释放券；多实例并发靠条件状态更新串行化。

## 6. 安全与权限边界

- 前台受保护路由（`middleware.Auth`）：`POST /coupons/:template_id/claim`（领取）、`GET /my/coupons`（我的券）。
- 后台写路由（`AdminAuth` + `RequirePermission`）：`POST /admin/coupons`（`coupon:create`）、`GET /admin/coupons`（`coupon:list`）、`POST /admin/coupons/:id/stop`（`coupon:stop`）。
- 身份信任：`Principal.UserID` 是用户侧唯一身份来源，券的 `user_id` 取自该字段；不信任请求自带身份。
- 权限 code（B 类，seed 于 `internal/boot/seed.go`）：`coupon:create`、`coupon:list`、`coupon:stop`。

## 7. 错误码域

域 **19000-19999**（语义：优惠券）：

| code | 语义 | HTTP |
| --- | --- | --- |
| 19001 | COUPON_TEMPLATE_NOT_FOUND | 404 |
| 19002 | COUPON_TEMPLATE_INVALID_ARGUMENT | 400 |
| 19003 | COUPON_NOT_FOUND（不存在或不属于当前用户） | 404 |
| 19004 | COUPON_ALREADY_CLAIMED | 409 |
| 19005 | COUPON_CLAIM_UNAVAILABLE（未开始/已结束/已停止） | 409 |
| 19006 | COUPON_CLAIM_SOLD_OUT（发放总量已满） | 409 |
| 19007 | COUPON_UNAVAILABLE（已使用/被占用/不在使用期/未达门槛） | 409 |

## 8. 跨模块关系

- `user_coupons.user_id` → `users.id`（软引用，无 FK）；`user_coupons.template_id` → `coupon_templates.id`（软引用，无 FK）。
- `user_coupons.order_id` → `orders.id`（软引用，无 FK，释放时清空）；`orders.user_coupon_id` → `user_coupons.id`（软引用，下单时快照写入、不再随释放改变）。
- 订单：`IOrder.Create` 接入 `ICoupon.LockInTx`（预占）；`Pay` 接入 `MarkUsedInTx`（核销）；`cancelInTx` 接入 `ReleaseInTx`（释放）；金额三列快照见 `order.md`。
- 库存：下单经 `IInventory.DeductInTx`、取消/退款经 `IncreaseInTx`，与券联动在同一外层事务内。
- 建表经 golang-migrate 迁移 `20261001000021_coupon.up.sql`（两表）、`20261001000022_order_coupon_amounts.up.sql`（orders 增三列 + 回填），见 `migration.md`。

## 9. Deferred / 已知留白

- 百分比折扣、指定商品/品类/品牌券、多券叠加、优惠分摊到多订单项、秒杀订单用券（V1 明确排除）。
- 结算/计价规则引擎、积分抵扣、会员折扣、自动发券、转赠、营销推荐。
- Redis 优惠券缓存、分布式锁（V1 锁无关，依赖 MySQL 条件更新 + 唯一约束）。
- 前台领券/用券 UI（前端模板工程未接入后端）。
