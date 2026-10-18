# Technical Contract

## Decision Status
APPROVED

## Problem

交付「平台通用固定金额满减券」最小完整闭环（后端）：管理员创建优惠券模板并管理发放；登录用户领取（每用户每模板至多一张、总量不超发、并发不重复/不超发）；普通订单可选一张自己的券，服务端重读 SKU 价格计算原始金额、校验券资格后计算优惠与应付金额并写入快照；订单创建与优惠券预占同事务；Mock 支付核销、取消/超时释放、退款不返券；无券下单行为与金额语义不变。

## Verified Current Behavior

- VERIFIED：分层为 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册）；数据访问 `g.DB().Model()`，无 `dao`/`model` 层（`internal/logic/order/order.go`、`internal/service/*.go`）。
- VERIFIED：当前后端无任何优惠券代码/表；`coupon` 仅出现在未接入后端的 `frotend_web`/`frotend_manage` 模板工程。
- VERIFIED：订单创建为单事务「写 orders + 写 order_items + 扣库存（`IInventory.DeductInTx`）+（购物车）删已购条目」（`order.go` `insertOrder`）。`orders.total_amount = Σ order_items.price×quantity`，当前无 `original_amount` / `discount_amount` 字段。
- VERIFIED：订单幂等 `uk_user_idempotency(user_id, idempotency_key)` + `request_hash` 判同 key 内容一致；`request_hash = sha256(source|address_id|sku_id:qty...)`，**当前不含 coupon_id**（`order.go` `requestHash`）。
- VERIFIED：订单状态机 10→20→30→40→50，旁路 10→60、20→70；取消/退款用「条件状态更新 + RowsAffected」恢复库存（`cancelInTx`/`Refund`）；超时取消 = scanner ticker + 查询/支付入口懒取消（`cancelExpired`）。
- VERIFIED：`Pay` 当前是**无事务的单条件 UPDATE**（10→20，RowsAffected 闸门），不涉及其它表；`Cancel`/`Refund` 在事务内（`g.DB().Transaction`）。
- VERIFIED：库存 `DeductInTx`/`IncreaseInTx` 复用「条件更新 + RowsAffected + 流水」；`INSERT ... ON DUPLICATE KEY UPDATE` 做并发安全 upsert（`internal/logic/inventory/inventory.go`）。
- VERIFIED：后台鉴权 = `AdminAuth` + `RequirePermission(code)`（`IsSuper` 放行、fail-closed）；权限 code 为 B 类 namespace，seed 于 `internal/boot/seed.go` `seedPermissionList`；前台经 `middleware.Auth` 注入 `Principal.UserID`（`routes_admin.go`/`routes_frontend.go`/`middleware/principal.go`）。
- VERIFIED：金额统一整数分（`INT UNSIGNED`）；Dashboard `today_order_amount = SUM(orders.total_amount)` 且 `status∈{20,30,40,50}` 且 `created_at∈今日`（`docs/design/dashboard.md` §2.3）。
- VERIFIED：错误码域 Registry `origin/develop` 最大域序 18（`18000-18999 order-refund-v1 RESERVED`）→ 下一空闲域序 **19 = 19000-19999**。
- VERIFIED：迁移 Registry `origin/develop` 最大 version `20261001000020`（`refund | order-refund-v1 RESERVED`）→ 下一空闲 version **20261001000021**。
- VERIFIED：`internal/migrations/migrations_test.go` 有 `latestMigrationVersion`、`businessTables`、`expectedSchema`（列/索引精确结构快照），新增表/列必须同步更新（task.md 已声明，schema 由 Analyst 固化）。
- VERIFIED：事实来源单一 MySQL；Redis 仅会话；无 MQ、无跨系统事务。

## Recommendation

RECOMMENDATION：采用「三态优惠券状态机 + 订单金额三列快照 + 幂等并入 request_hash + 锁无关领取原子闸门」，具体如下。

### 金额语义（D1）

- 新增 `orders.original_amount INT UNSIGNED NOT NULL DEFAULT 0`（优惠前金额快照）、`orders.discount_amount INT UNSIGNED NOT NULL DEFAULT 0`（优惠金额快照）。
- **保留 `orders.total_amount` 的「成交/应付金额」语义**：`total_amount = original_amount - discount_amount`。
- 无券下单：`original_amount = total_amount`、`discount_amount = 0`，`total_amount` 与现状完全一致 → 满足 AC-010。
- 历史订单：迁移回填 `original_amount = total_amount`、`discount_amount = 0`，`total_amount` 不变 → 满足 AC-011。
- Dashboard 影响：`today_order_amount = SUM(total_amount)` 对用券订单变小（实付口径）、无券订单不变。这是口径的自然收敛（文案已标注「已支付口径」，非「实收销售额」），不得把 Mock 支付金额视为真实资金到账；需同步更新 `docs/design/order.md` §2.3 与 `docs/design/dashboard.md` §2.3。

关键取舍：相比「保持 `total_amount` 为原始金额、另增 `payable_amount` 列」的替代方案，本方案让 `total_amount` 名副其实（就是应付/实付），不引入 `total_amount ≡ original_amount` 的冗余列，Dashboard 无需额外改字段；代价是 Dashboard 金额口径对用券订单的绝对数值变小，需在 Design 中明确。

### 优惠券数据模型与状态机（D2）

`coupon_templates`（模板，软引用、无外键）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| id | BIGINT UNSIGNED AUTO_INCREMENT PK | |
| name | VARCHAR(64) NOT NULL | 模板名 |
| threshold_amount | INT UNSIGNED NOT NULL | 满减门槛（整数分，≥0，0=无门槛） |
| discount_amount | INT UNSIGNED NOT NULL | 减免金额（整数分，>0） |
| total_quantity | INT UNSIGNED NOT NULL | 发放总量（>0） |
| issued_quantity | INT UNSIGNED NOT NULL DEFAULT 0 | 已发放数量 |
| claim_start_time | DATETIME NOT NULL | 领取开始 |
| claim_end_time | DATETIME NOT NULL | 领取结束 |
| use_start_time | DATETIME NOT NULL | 使用开始 |
| use_end_time | DATETIME NOT NULL | 使用结束 |
| status | TINYINT NOT NULL DEFAULT 1 | 1=正常可领取、0=已停止新领取 |
| created_at / updated_at | DATETIME | |

索引：`PRIMARY(id)`。**「未开始/已结束领取」由 `claim_start_time`/`claim_end_time` 与 MySQL `NOW()` 派生，不设冗余终态**（沿用 flash_sale「已结束由 end_time 派生」惯例）；「停止新领取」用 `status` 显式表示（管理员动作，不影响已领取券）。使用有效期由 `use_start_time`/`use_end_time` 派生。

`user_coupons`（用户券实例）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| id | BIGINT UNSIGNED AUTO_INCREMENT PK | |
| user_id | BIGINT UNSIGNED NOT NULL | 软引用 users.id |
| template_id | BIGINT UNSIGNED NOT NULL | 软引用 coupon_templates.id |
| status | TINYINT NOT NULL | 10=可用、20=已预占、30=已使用 |
| order_id | BIGINT UNSIGNED NULL | 当前预占/核销该券的订单 id（释放时清空） |
| received_at | DATETIME | 领取时间 |
| locked_at | DATETIME NULL | 预占时间 |
| used_at | DATETIME NULL | 核销时间 |
| created_at / updated_at | DATETIME | |

索引：`PRIMARY(id)`、`uk_user_template(user_id, template_id)` UNIQUE、`idx_user_id(user_id)`、`idx_order_id(order_id)`。

**状态机（三态，不引入「已释放」终态）**：`available(10) → locked(20) → used(30)`；`locked(20) → available(10)`（取消/超时释放）。`used` 为终态。合法迁移：

| from | to | 触发 |
| --- | --- | --- |
| available(10) | locked(20) | 下单预占（同订单事务内条件更新） |
| locked(20) | used(30) | 支付成功核销（条件更新 + RowsAffected） |
| locked(20) | available(10) | 取消/超时释放（条件更新 + RowsAffected） |

「已预占」用独立 `status` 列（非由订单状态派生），与订单生命周期在**同一事务内联动**，但状态本身解耦存储。释放 = 回到 `available`（券在有效期内可再次用于新订单），不是丢券终态。

### 订单-券事务与联动

- 新增 `ICoupon` service，暴露 tx 感知方法：`Claim`、`ListMine`、`LockInTx(ctx, tx, userID, couponID, orderID)`、`MarkUsedInTx(ctx, tx, couponID)`、`ReleaseInTx(ctx, tx, orderID)`（复用 `IInventory.DeductInTx` 同款「不自开事务」模式）。
- 订单 `insertOrder` 事务内新增一步：**预占** `UPDATE user_coupons SET status=locked, order_id=?, locked_at=NOW() WHERE id=? AND user_id=? AND status=available`，RowsAffected=0 → 19007（券不可用）并整体回滚。这是 AC-013「一张券不被两个订单同时占用」的原子闸门。
- `Pay` 由无事务改为事务：`10→20`（条件更新）+ 若用券则 `UPDATE user_coupons SET status=used, used_at=NOW() WHERE id=? AND status=locked`（条件更新，RowsAffected 兜底；重复支付不重复核销）。
- `cancelInTx`（取消与超时共用）新增一步：`UPDATE user_coupons SET status=available, order_id=NULL, locked_at=NULL WHERE order_id=? AND status=locked`。
- `Refund` 不触碰 `user_coupons`（退款不返券，券保持 `used`），无需改库存之外逻辑。
- 预占/核销/释放均需可靠的订单归属校验（`user_id` + `status` 条件），并与对应订单状态变更同事务，重复请求不得重复释放/核销。

### 幂等纳入 coupon_id（D3）

- **并入 `request_hash`**：`request_hash = sha256(source|address_id|coupon_id|sku_id:qty...)`，`coupon_id` 缺省为 0（无券）。
- 保持 `uk_user_idempotency(user_id, idempotency_key)` 不变，**不新增字段/索引**；保持 9006 语义。同 key 同内容（含 coupon）→ 返回既有订单（幂等成功，不重复建单/占券）；同 key 不同内容 → 9006。
- 关键取舍：相比「把 coupon_id 加入唯一键」的替代方案，本方案避免 NULL coupon 下唯一键退化的缺陷（MySQL 唯一索引允许多个 NULL，会令 `(user_id, key)` 在有券/无券间可重复建单）。

### 领取防超发/防重复

领取单事务（锁无关，无 FOR UPDATE）：

1. 读模板：不存在 → 19001；`status=0` → 19005；`claim_start_time > NOW()` 或 `claim_end_time <= NOW()` → 19005（用 MySQL `NOW()`，避免 Go↔MySQL 时区漂移）。
2. 原子闸门：`UPDATE coupon_templates SET issued_quantity = issued_quantity + 1 WHERE id=? AND status=1 AND issued_quantity < total_quantity AND claim_start_time <= NOW() AND claim_end_time > NOW()`；RowsAffected=0 → 19006（发放总量已满，兜底并发超发）。
3. 写入 `user_coupons`（status=available, received_at=NOW()）；命中 `uk_user_template` 1062 → 19004（已领取），事务回滚使 `issued_quantity` 不落库。

并发正确性：总量由条件更新原子保证（AC-006）；单用户单模板由唯一约束兜底（AC-003）。

### 权限与错误码

权限 code（B 类，seed 登记，不占 Registry）：`coupon:create`（创建模板）、`coupon:list`（查询模板）、`coupon:stop`（停止新领取）。

路由：
- 前台（`middleware.Auth`）：`POST /coupons/:template_id/claim`（领取）、`GET /my/coupons`（我的券）。
- 后台（`AdminAuth` + `RequirePermission`）：`POST /admin/coupons`（`coupon:create`）、`GET /admin/coupons`（`coupon:list`）、`POST /admin/coupons/:id/stop`（`coupon:stop`）。

新增错误码域 **19000-19999**（语义：优惠券）：

| code | 语义 | HTTP |
| --- | --- | --- |
| 19001 | COUPON_TEMPLATE_NOT_FOUND（模板不存在） | 404 |
| 19002 | COUPON_TEMPLATE_INVALID_ARGUMENT（满减门槛/减免金额/时间/总量非法） | 400 |
| 19003 | COUPON_NOT_FOUND（券不存在或不属于当前用户，防枚举） | 404 |
| 19004 | COUPON_ALREADY_CLAIMED（已领取该模板） | 409 |
| 19005 | COUPON_CLAIM_UNAVAILABLE（未开始/已结束/已停止，不可领取） | 409 |
| 19006 | COUPON_CLAIM_SOLD_OUT（发放总量已满） | 409 |
| 19007 | COUPON_UNAVAILABLE（下单用券：已使用/被占用/不在使用期/未达门槛） | 409 |

复用：`1000`（500）、`1001`（400）、`1002`（401）、`1003`（403）、`5001`（SKU 不存在）、`6001`（库存不足）、`9006`（幂等冲突）。

## Selected Design

经 Owner 批准，最终设计为：

- **金额语义（D1 ACCEPT）**：新增 `original_amount`/`discount_amount`，`total_amount = original_amount - discount_amount`；无券兼容、历史回填；Dashboard 口径同步核实，Mock 支付金额不视为真实到账。
- **状态机（D2 ACCEPT）**：`available(10) → locked(20) → used(30)`，取消 `locked → available`；不新增「已释放」终态；预占/核销/释放带可靠订单归属校验并与订单状态变更同事务，重复请求不重复释放/核销。
- **幂等（D3 ACCEPT）**：coupon_id 并入 `request_hash`，不新增幂等字段，保持唯一约束与 9006 语义。
- **资源（D4 有条件批准）**：核验 `origin/develop` Registry 发现 `18000-18999` 与 `20261001000020` 已被 `order-refund-v1` RESERVED，改选 **错误码域 19000-19999**、**迁移 20261001000021 + 20261001000022**（无冲突）。
- **范围锁定**：Coupon V1 即最终版本，不增加百分比折扣、叠券、类目券、营销规则引擎；保持现有订单状态机、库存补偿与 IAM 行为兼容。

## Interfaces and Data

- 新表：`coupon_templates`、`user_coupons`（schema 见上；均软引用、无外键，沿用 `users`/`categories` 惯例）。
- 改表：`orders` 增 `original_amount`、`discount_amount`、`user_coupon_id BIGINT UNSIGNED NULL`（快照引用，下单时写入、不再随释放改变）。
- 新 service：`internal/service/coupon.go` 定义 `ICoupon`（`Claim`/`ListMine`/`LockInTx`/`MarkUsedInTx`/`ReleaseInTx`）+ `RegisterCoupon`；`internal/logic/coupon/` 实现。
- 订单改动：`api/order/v1` 的 `CreateReq` 增可选 `coupon_id`；`Order` 响应增 `original_amount`/`discount_amount`/`user_coupon_id`；`internal/logic/order/order.go` 的 `Create`/`Pay`/`cancelInTx` 接入 `ICoupon` 与金额计算。
- 迁移：`20261001000021_coupon.up.sql`（`coupon_templates` + `user_coupons`）、`20261001000022_order_coupon_amounts.up.sql`（`orders` 增三列 + 回填 `original_amount = total_amount`）。
- `internal/migrations/migrations_test.go`：`latestMigrationVersion` 更新为 `20261001000022`；`businessTables` 增 `coupon_templates`、`user_coupons`；`expectedSchema` 增两表结构与 `orders` 三列。
- `internal/boot/seed.go` `seedPermissionList` 增 `coupon:create`/`coupon:list`/`coupon:stop`。
- `internal/codes/codes.go` 增 19001-19007 及码表映射。

## Business Invariants

- INV-C01（领取唯一）：同一 `(user_id, template_id)` 至多一张券，由 `uk_user_template` 唯一约束 + 1062 分流兜底（AC-003/AC-006）。
- INV-C02（总量不超发）：`issued_quantity ≤ total_quantity` 恒成立，由条件更新 `WHERE issued_quantity < total_quantity` + RowsAffected 保证（AC-005/AC-006）。
- INV-C03（券状态机）：仅 `10→20→30` 与 `20→10` 合法；`30` 终态；非法迁移拒绝且无副作用（AC-008/AC-015/AC-016/AC-018）。
- INV-C04（预占唯一）：一张券同一时刻至多被一个订单占用，由 `WHERE status=available` 条件更新 + RowsAffected 原子闸门保证（AC-013）。
- INV-C05（订单-券同事务）：订单创建与预占同一 MySQL 事务，任一步失败整体回滚，不残留「已预占」（AC-012）。
- INV-C06（服务端金额与快照）：`original = Σ skus.price×quantity`（重读）；`discount = min(模板.discount_amount, original)`（≤ original）；`total = original - discount`；不信任客户端金额；下单后改价/改模板不改变快照（AC-008/AC-009/AC-011）。
- INV-C07（无券兼容）：无券时 `discount=0`、`original=total`，`total_amount` 语义与现状一致（AC-010）。
- INV-C08（核销/释放原子）：支付核销 `locked→used`、取消/超时 `locked→available` 均为条件更新 + RowsAffected，只生效一次；退款不返券（AC-015/AC-016/AC-017/AC-018）。
- INV-C09（归属隔离）：券仅本人可见可用（`user_id` 取自 `Principal.UserID`）；越权/不存在统一 19003（404），无写入（AC-002/AC-007）。
- INV-C10（幂等含 coupon_id）：同 `(user_id, idempotency_key)` 同 `request_hash`（含 coupon_id）→ 返回既有订单；同 key 不同内容 → 9006（AC-014）。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL。`coupon_templates`（模板事实）、`user_coupons`（券实例事实）、`orders`（订单事实）、`inventories`（库存事实）。Redis 仅会话；无 MQ、无跨系统事务。
- 成功语义：领取成功 = 模板 `issued_quantity` 原子 +1 且插入券实例同事务提交；下单用券成功 = 订单 + 订单项 + 扣库存 + 预占同事务提交；支付成功 = 订单 10→20 + 券核销同事务；取消成功 = 订单 10→60 + 恢复库存 + 券释放同事务。
- 失败/回滚：预占失败（券并发被占/不可用）→ 整体回滚，券不残留 `locked`、库存不扣；核销/释放均为条件更新，RowsAffected=0 属不可达防御分支（下单已原子预占）。
- 幂等/重复：领取重复 → 唯一约束 1062 → 19004；支付/取消重复 → 状态机 + 条件更新不重复核销/释放；退款不触碰券。
- 超时取消：沿用现有 scanner + 懒取消，`cancelInTx` 内统一释放券；多实例并发靠条件状态更新串行化。

## Allowed / Forbidden Changes

- 允许：新增 `coupon_templates`/`user_coupons` 表；`orders` 增 `original_amount`/`discount_amount`/`user_coupon_id` 三列；新增 `ICoupon` service 与优惠券 API；`Pay` 改为事务包裹；`Create` 计算金额并接入预占；`cancelInTx` 接入释放；扩展 `request_hash` 含 `coupon_id`；seed `coupon:*` 权限；新增 19000-19999 错误码域；更新 `migrations_test.go` 与 `docs/design/*`。
- 禁止：修改既有订单状态机、IAM/RBAC 语义、购物车/库存/地址/SKU/商品/分类行为（除下单用券最小只读引用与必要的库存/券事务协同）；改变既有错误码取值/HTTP 映射；引入 Redis 优惠券缓存、分布式锁、MQ；实现前端；实现百分比折扣/叠券/类目券/营销规则引擎；对无券订单改变任何现有金额/幂等/库存行为。

## Verification Requirements

- INV-C01 → 并发领取同一模板，`uk_user_template` 兜底；断言单用户至多一张（真实 MySQL + `-race`）。
- INV-C02 → 并发领取，断言 `issued_quantity ≤ total_quantity`（真实 MySQL + `-race`）。
- INV-C03/C08 → 支付/取消/超时/退款与券状态联动，断言合法迁移、重复不重复核销/释放、退款不返券（真实 MySQL）。
- INV-C04/C05 → 并发下单同一张券断言至多一单占用；下单失败（库存不足）断言券不残留 `locked`（真实 MySQL + `-race`）。
- INV-C06/C07/C10 → 门槛/有效期/归属校验、服务端金额计算、无券兼容、下单后改价/改模板快照不变、同幂等键不同券 → 9006（真实 MySQL）。
- 回归：`go test -p 1 ./...`（含 order/dashboard/iam/rbac）；并发 AC 用真实 MySQL 事务与 `-race`，不允许仅 Mock。

## Open Risks

- Dashboard `today_order_amount` 语义变为「优惠后应付」，绝对数值对用券订单变小；Owner 已确认可接受并同步更新 `docs/design/dashboard.md`。
- 「停止新领取」只阻断新领取、不影响已领取券使用，语义已固化于 `status` 字段；停止后已领取券仍可正常下单使用。
- 金额三列快照对 `migrations_test.go` 的 `expectedSchema` 严格等价测试有结构性影响，Coder 需同步更新，否则等价性测试失败（属预期、非风险）。
- 锁无关领取依赖「先原子闸门 +1 再插入、插入失败回滚」的顺序，Coder 必须保持该顺序；否则并发超发/重复计数会漂移。

## Owner Decision Record

- D1 金额语义：ACCEPT（`original_amount`/`discount_amount`/`total_amount`，`total = original - discount`；无券兼容、历史回填；Dashboard 不得把 Mock 支付金额视为真实到账）。
- D2 状态机：ACCEPT（`available→locked→used`，取消 `locked→available`；不设「已释放」终态；预占/核销/释放带归属校验并与订单状态同事务，重复不重复释放/核销）。
- D3 幂等：ACCEPT（coupon_id 并入 `request_hash`，不新增字段，保持唯一约束与 9006 语义）。
- D4 全局资源：有条件批准——核验 `origin/develop` Registry 发现 `18000-18999` 与 `20261001000020` 已被 `order-refund-v1` RESERVED，改选 **19000-19999**（错误码域）+ **20261001000021 / 20261001000022**（迁移），无冲突，待 Registry-only commit 落实 RESERVED。
- 其他：Coupon V1 即最终版本（不加百分比折扣/叠券/类目券/规则引擎）；保持订单状态机、库存补偿与 IAM 兼容；feature 分支由 Owner 准备后再提交。

## 全局资源清单（APPROVED，待 Registry-only commit 落 RESERVED）

| kind | 候选值 | 语义 | 状态 |
| --- | --- | --- | --- |
| error_code_domain | 19000-19999（域序 19 = max(18)+1） | 优惠券错误码域 | 待落 RESERVED |
| migration_version | 20261001000021（coupon） | coupon_templates + user_coupons | 待落 RESERVED |
| migration_version | 20261001000022（order_coupon_amounts） | orders 增三列 + 回填 | 待落 RESERVED |
