# 优惠券设计（Coupon）

本文面向项目接手者，说明优惠券的架构、数据模型、状态机、领取/用券的一致性模型与权限边界。
事实来源为 `coupon-v1` APPROVED Contract、现有实现，以及 B1-03-03 新增的批次/适用范围/限领 Schema。
**当前业务代码仍只实现「平台通用固定金额满减券」**：批次、适用范围、折扣率与每人限领目前只有数据库结构，
写入与判定逻辑属 B1-04。

## 1. 职责与边界

优惠券回答「管理员发了什么券、从哪个批次发、适用哪些商品/分类、用户领了哪些券、下单时如何抵扣」：
管理员创建/查询模板、停止新领取；登录用户领取（受模板总量、批次总量、每人限领、时间窗约束，并发不重复/不超发）；
普通订单可选一张自己的券，服务端重读 SKU 价格计算原始金额、校验券资格后计算优惠与应付金额并写入快照；
订单创建与优惠券预占同事务；Mock 支付核销、取消/超时释放、退款不返券。

边界：

- 固定金额满减是当前唯一实现；百分比折扣只有 `discount_type`/`discount_rate_bp` 结构，未实现计价。
- 适用范围支持「商品」与「分类」，不支持品牌、SKU 级、排除清单、多券叠加、优惠分摊、秒杀订单用券。
- 每人限领由模板级 `per_user_limit` 表达，默认 1；领取次数用 `claim_no` 承载，不支持「同一用户同一模板无限张」之外的复杂规则。
- **不引入** Redis 优惠券缓存与分布式锁；**不实现**前端。事实来源为单一 MySQL；Redis 仅会话；无 MQ、无跨系统事务。

## 2. 数据模型

### 2.1 `coupon_templates`（优惠券模板）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `name` | VARCHAR(64) | 非空，模板名 |
| `threshold_amount` | INT UNSIGNED | 非空，满减门槛（整数分，≥0，0=无门槛） |
| `discount_amount` | INT UNSIGNED | 非空，固定减免金额（整数分）；`discount_type=2` 时应为 0 |
| `discount_type` | TINYINT | 非空默认 1；`1=固定金额满减`、`2=折扣率`（结构预留） |
| `discount_rate_bp` | INT UNSIGNED | 可空；折扣率（万分比，9000=9 折），仅 `discount_type=2` 使用 |
| `total_quantity` | INT UNSIGNED | 非空，模板级全局发放总量（>0） |
| `per_user_limit` | INT UNSIGNED | 非空默认 1，每人限领数量；`0=不限` |
| `issued_quantity` | INT UNSIGNED | 非空默认 0，已发放数量（模板级全局计数） |
| `claim_start_time` | DATETIME | 非空，领取开始 |
| `claim_end_time` | DATETIME | 非空，领取结束 |
| `use_start_time` | DATETIME | 非空，使用开始 |
| `use_end_time` | DATETIME | 非空，使用结束 |
| `status` | TINYINT | 非空默认 1，`1=正常可领取`、`0=已停止新领取` |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`。

「未开始/已结束领取」由 `claim_start_time`/`claim_end_time` 与 MySQL `NOW()` 比较派生，**不设冗余终态**；
「停止新领取」用 `status` 显式表示（管理员动作，仅阻断新领取、不影响已领取券）。使用有效期由
`use_start_time`/`use_end_time` 派生。

`discount_type`/`discount_rate_bp` 的一致性（type=1 必须填 `discount_amount` 且 rate 为空；type=2 必须填
`1..10000` 的 rate 且 `discount_amount=0`）由应用层校验，不引入 CHECK（与项目既有迁移约定一致）。

### 2.2 `coupon_batches`（发行批次）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `template_id` | BIGINT UNSIGNED | 非空，FK → `coupon_templates.id` ON DELETE RESTRICT |
| `total_quantity` | INT UNSIGNED | 非空，本批次发放总量 |
| `issued_quantity` | INT UNSIGNED | 非空默认 0，本批次已发放量 |
| `claim_start_time` | DATETIME | 非空，本批次领取开始 |
| `claim_end_time` | DATETIME | 非空，本批次领取结束 |
| `status` | TINYINT | 非空默认 1，`1=启用`、`0=停止` |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`idx_template_status(template_id, status)`。

批次是模板下的**可选发放通道**，不是第二个模板：

- 模板仍是券权益的唯一事实来源（门槛、减免/折扣、使用有效期、停止领取）；
- 批次只增加「本批配额 + 本批领取时间窗 + 本批启停」；
- 从批次领取时，**模板闸门与批次闸门同时生效**：模板 `issued_quantity` 照常 +1（既有 INV-C02 不变），
  批次 `issued_quantity` 也 +1；任一闸门不满足即拒绝；
- 模板没有批次时，行为与 B1-03-02 完全一致（只看模板）；
- 模板被批次引用时不可删除（RESTRICT），历史批次与领取记录不会被静默清理。

### 2.3 `coupon_template_scopes`（适用范围）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `template_id` | BIGINT UNSIGNED | 非空，FK → `coupon_templates.id` ON DELETE CASCADE |
| `product_id` | BIGINT UNSIGNED | 可空，FK → `products.id` ON DELETE CASCADE |
| `category_id` | BIGINT UNSIGNED | 可空，FK → `categories.id` ON DELETE CASCADE |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_template_product(template_id, product_id)`、`uk_template_category(template_id, category_id)`、
`idx_product_id(product_id)`、`idx_category_id(category_id)`。

语义：

- **无范围行 = 平台通用券**（与既有券完全一致）；
- 有范围行 = 只能用于所列商品/分类；同一模板同一商品/分类至多一条（唯一键兜底）；
- `product_id` 与 `category_id` 恰有一个非空（应用层保证；两列都空或都非空均拒绝）；
- 商品/分类被删除时范围行级联删除（范围是配置，不是历史事实）；已领取的用户券不受影响。

### 2.4 `user_coupons`（用户券实例）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），指向 `users.id` |
| `template_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），指向 `coupon_templates.id` |
| `batch_id` | BIGINT UNSIGNED | 可空，软引用（无 FK），指向 `coupon_batches.id`；NULL=历史券/不经批次 |
| `claim_no` | INT UNSIGNED | 非空默认 1，该用户在该模板下的第几次领取（从 1 开始） |
| `coupon_title` | VARCHAR(64) | 可空，领取时的模板名称快照；NULL=历史券未快照 |
| `coupon_price` | INT UNSIGNED | 可空，领取时的减免金额快照（整数分）；NULL=历史券未快照 |
| `use_min_price` | INT UNSIGNED | 可空，领取时的使用门槛快照（整数分）；NULL=历史券未快照 |
| `end_time` | DATETIME | 可空，领取时确定的券到期时间快照；NULL=历史券未快照 |
| `status` | TINYINT | 非空，`10=可用`、`20=已预占`、`30=已使用`（见状态机） |
| `order_id` | BIGINT UNSIGNED | 可空，当前预占/核销该券的订单 id（释放时清空） |
| `received_at` | DATETIME | 领取时间 |
| `locked_at` | DATETIME | 可空，预占时间 |
| `used_at` | DATETIME | 可空，核销时间 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`uk_user_template_claim(user_id, template_id, claim_no)` UNIQUE、
`idx_user_id(user_id)`、`idx_order_id(order_id)`、`idx_batch_id(batch_id)`。

**唯一键替换（B1-03-03）**：`uk_user_template(user_id, template_id)` 被替换为
`uk_user_template_claim(user_id, template_id, claim_no)`。原因：每人限领 >1 时，旧唯一键会机械阻止第二张券。
兼容性由 `claim_no DEFAULT 1` 保证：既有领取路径不写 `claim_no` 时仍为 1，同一用户同一模板第二次领取仍返回 1062，
INV-C01 的默认语义与既有测试完全不变。历史行不回填 `batch_id`（保持 NULL）、`claim_no` 取默认 1。

## 3. 状态机与合法迁移

`user_coupons.status` 三态，**不引入「已释放」终态**（释放 = 回到可用）：

| from | to | 触发者 | 备注 |
| --- | --- | --- | --- |
| 可用(10) | 已预占(20) | 下单 | 同订单创建事务内条件更新 |
| 已预占(20) | 已使用(30) | Mock 支付成功 | 条件更新 + RowsAffected，终态 |
| 已预占(20) | 可用(10) | 取消/超时取消 | 条件更新 + RowsAffected，清空 `order_id`/`locked_at` |

终态：已使用(30)。非法迁移拒绝且无副作用。释放 = `locked → available`，券在有效期内可再次用于新订单，不是丢券终态。
`claim_no`/`batch_id` 只描述领取来源与第几张，不参与状态机；核销/释放不改变它们。

## 4. 业务不变量

- INV-C01（领取唯一，默认每人一张）：同一 `(user_id, template_id, claim_no)` 至多一张券（`uk_user_template_claim` + 1062 分流）。
  默认 `claim_no=1` 时即「同一用户同一模板至多一张」；`per_user_limit=N` 时合法 `claim_no` 为 `1..N`。
- INV-C02（模板总量不超发）：`coupon_templates.issued_quantity ≤ total_quantity`（条件更新
  `WHERE status=1 AND issued_quantity < total_quantity AND ...` + RowsAffected）。批次领取同样先过此闸门，模板计数照常 +1。
- INV-C03（券状态机）：仅 `10→20→30` 与 `20→10` 合法；`30` 终态。
- INV-C04（预占唯一）：一张券同一时刻至多被一个订单占用（`WHERE status=available` 条件更新 + RowsAffected）。
- INV-C05（订单-券同事务）：订单创建与预占同一 MySQL 事务，任一步失败整体回滚，不残留「已预占」。
- INV-C06（服务端金额与快照）：`original = Σ skus.price×quantity`；固定金额券
  `discount = min(模板.discount_amount, original)`（≤ original）；`total = original - discount`；
  不信任客户端金额；下单后改价/改模板不改变快照。
- INV-C08（核销/释放原子）：支付核销 `locked→used`、取消/超时 `locked→available` 均条件更新 + RowsAffected 只生效一次；退款不返券。
- INV-C09（归属隔离）：券仅本人可见可用；越权/不存在统一 19003（404），无写入。
- INV-C10（幂等含 coupon_id）：同 `(user_id, idempotency_key)` 同 `request_hash`（含 coupon_id）→ 返回既有订单；同 key 不同内容 → 9006。
- INV-C11（权益快照）：新领取的券必须写入 `coupon_title`/`coupon_price`/`use_min_price`/`end_time` 快照，写入后不随模板修改而漂移；
  历史券四列为 NULL（不伪造、不回填），读取方回退模板当前值。核销/释放只改 `status` 与时间列，不改快照。
- INV-C12（批次配额不超发）：`coupon_batches.issued_quantity ≤ total_quantity`，批次领取用条件更新
  `WHERE status=1 AND issued_quantity < total_quantity AND claim_start_time <= NOW() AND claim_end_time > NOW()` + RowsAffected；
  与模板闸门在同一事务内先后执行，任一失败整体回滚。
- INV-C13（模板/批次双层时间窗）：从批次领取必须同时满足模板与批次的领取时间窗及启停状态；
  批次时间窗不延长模板窗口。
- INV-C14（每人限领）：`claim_no` 在 `(user_id, template_id)` 内从 1 连续递增，且不得超过
  `coupon_templates.per_user_limit`（0=不限）；并发领取由 `uk_user_template_claim` 兜底，撞键视为已领取/请重试。
- INV-C15（范围匹配）：模板存在范围行时，订单项必须全部落在范围内才可使用该券；无范围行时平台通用。
  范围只影响「能否用」，不改变面额与门槛；退款不返券。

## 5. 一致性模型与失败语义

- 事实来源：单一 MySQL。`coupon_templates`（模板事实）、`coupon_batches`（批次配额）、
  `coupon_template_scopes`（范围配置）、`user_coupons`（券实例事实）、`orders`（订单事实）、
  `inventories`（库存事实）。Redis 仅会话；无 MQ、无跨系统事务。
- 领取（单事务）：读模板校验资格 → 模板原子闸门（INV-C02）→ 如指定批次，批次原子闸门（INV-C12）→
  计算 `claim_no = 该用户该模板已领数量 + 1` 并校验 `per_user_limit` → 插入 `user_coupons`
  （四列权益快照 + `batch_id` + `claim_no`；1062 → 已领取/并发冲突，回滚使两个 `issued_quantity` 都不落库）。
  并发正确性由条件更新 + 唯一约束兜底，无需 `FOR UPDATE`。
- 下单用券（单事务）：校验归属/状态/有效期/门槛/范围 → 订单 + 订单项 + 扣库存 + 预占同事务提交；
  预占失败（券并发被占/不可用）→ 整体回滚，券不残留 `locked`、库存不扣。
- 支付/取消：`Pay` 事务「订单 10→20 + 券核销」；`cancelInTx`（取消与超时共用）「订单 10→60 + 恢复库存 + 券释放」。
  核销/释放 RowsAffected=0 属不可达防御分支（下单已原子预占）。
- 退款：不触碰 `user_coupons`（退款不返券，券保持 `used`）。
- 超时取消：沿用现有 scanner + 懒取消，`cancelInTx` 内统一释放券；多实例并发靠条件状态更新串行化。
- **当前实现状态**：`Claim` 仍只走模板闸门、不写 `batch_id`/`claim_no`/四列快照；
  批次与范围的读写、快照写入、`per_user_limit>1` 的领取均属 B1-04。历史行为在 B1-04 之前保持不变。

## 6. 安全与权限边界

- 前台受保护路由（`middleware.Auth`）：`POST /coupons/:template_id/claim`（领取）、`GET /my/coupons`（我的券）。
- 后台写路由（`AdminAuth` + `RequirePermission`）：`POST /admin/coupons`（`coupon:create`）、`GET /admin/coupons`（`coupon:list`）、
  `POST /admin/coupons/:id/stop`（`coupon:stop`）。
- 身份信任：`Principal.UserID` 是用户侧唯一身份来源，券的 `user_id` 取自该字段；不信任请求自带身份。
- 权限 code（B 类，seed 于 `internal/boot/seed.go`）：`coupon:create`、`coupon:list`、`coupon:stop`。
- 批次/范围管理尚未新增路由与权限码；B1-04 实现时必须按 RBAC 流程登记，不在本次 Schema 任务中预分配。

## 7. 错误码域

域 **19000-19999**（语义：优惠券），本次 Schema **不新增错误码**：

| code | 语义 | HTTP |
| --- | --- | --- |
| 19001 | COUPON_TEMPLATE_NOT_FOUND | 404 |
| 19002 | COUPON_TEMPLATE_INVALID_ARGUMENT | 400 |
| 19003 | COUPON_NOT_FOUND（不存在或不属于当前用户） | 404 |
| 19004 | COUPON_ALREADY_CLAIMED | 409 |
| 19005 | COUPON_CLAIM_UNAVAILABLE（未开始/已结束/已停止） | 409 |
| 19006 | COUPON_CLAIM_SOLD_OUT（发放总量已满） | 409 |
| 19007 | COUPON_UNAVAILABLE（已使用/被占用/不在使用期/未达门槛） | 409 |

批次售罄、批次停用、超出每人限领、范围不匹配的对外错误码属 B1-04 契约；在此之前复用/扩展上述码需先更新
`error-codes.md` 与 Registry。

## 8. 跨模块关系

- `user_coupons.user_id` → `users.id`（软引用，无 FK）；`user_coupons.template_id` → `coupon_templates.id`（软引用，无 FK）；
  `user_coupons.batch_id` → `coupon_batches.id`（软引用，无 FK：批次删除不得连带删除历史券）。
- `user_coupons.order_id` → `orders.id`（软引用，无 FK，释放时清空）；`orders.user_coupon_id` → `user_coupons.id`
  （软引用，下单时快照写入、不再随释放改变）。
- `coupon_batches.template_id` → `coupon_templates.id`（FK RESTRICT）；`coupon_template_scopes.template_id` →
  `coupon_templates.id`（FK CASCADE）、`product_id` → `products.id`（FK CASCADE）、`category_id` → `categories.id`（FK CASCADE）。
- 订单：`IOrder.Create` 接入 `ICoupon.LockInTx`（预占）；`Pay` 接入 `MarkUsedInTx`（核销）；`cancelInTx` 接入 `ReleaseInTx`（释放）；
  金额三列快照见 `order.md`。范围校验若需要订单项，必须在订单事务内、预占之前完成。
- 库存：下单经 `IInventory.DeductInTx`、取消/退款经 `IncreaseInTx`，与券联动在同一外层事务内。
- 建表经 golang-migrate 迁移：
  - `20261001000021_coupon.up.sql`（模板 + 用户券两表）；
  - `20261001000022_order_coupon_amounts.up.sql`（orders 增三列 + 回填）；
  - `20261110000009_coupon_snapshot.up.sql`（用户券四个可空快照列，无回填）；
  - `20261110000010_coupon_batch.up.sql`（模板三个新列 + 批次 + 范围 + 用户券 `batch_id`/`claim_no` 与唯一键替换）。
  详见 `migration.md`。
- 快照列当前只有结构：`Claim` 仍不写快照，`ListMine` 仍联表读模板；写入快照属 B1-04，读取方在此期间必须容忍 NULL。

## 9. Deferred / 已知留白

- 百分比折扣的实际计价与舍入（结构已建）。
- 指定商品/品类/品牌券之外的排除清单、SKU 级范围、多券叠加、优惠分摊到多订单项、秒杀订单用券。
- 结算/计价规则引擎、积分抵扣、会员折扣、自动发券、转赠、营销推荐。
- 领取时写入权益快照与批次/限领逻辑（列/表已建，`Claim` 尚未读写）。
- 批次/范围的管理端 API、权限码与错误码。
- Redis 优惠券缓存、分布式锁（V1 锁无关，依赖 MySQL 条件更新 + 唯一约束）。
- 前台领券/用券 UI（前端模板工程未接入后端）。
