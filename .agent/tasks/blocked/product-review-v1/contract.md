# Technical Contract

## Decision Status
APPROVED

## Problem

交付「商品评价」V1 核心闭环：买家对已完成订单中的订单项提交星级 + 文字评价；评价由服务端绑定订单项与商品、每个已购项最多评价一次；商品维度公开评价列表 + 汇总（平均分 + 条数）随评价增删/下架正确变化；买家仅能操作自己的评价，管理员可下架违规评价；所有越权与重复评价均不产生数据库写入。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2（Go 1.23+），分层 `api/<module>/v1`（`g.Meta` path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。证据：`internal/logic/order/order.go`、`internal/service/order.go`。
- VERIFIED：身份域：前台 `middleware.Auth` 注入 `Principal{UserID,Sid}`（`internal/middleware/principal.go`、`auth.go`）；后台 `AdminAuth` + `RequirePermission(code)`（`IsSuper` 放行、fail-closed）。前台受保护路由挂 `routes_frontend.go` 的 `Auth` 分组，后台写路由挂 `routes_admin.go` 的 `require(code)` 分组。
- VERIFIED：订单状态机 `orders.status` TINYINT：10 待支付/20 已支付/30 已发货/40 已收货/50 已完成/60 已取消/70 已退款。**关键事实：`Receive` 在同一事务内原子推进 30→40→50（`internal/logic/order/order.go` `Receive`/`cancelInTx` 同款事务模式），因此状态 40（已收货）从不在数据库中持久化**——可评价的持久状态只可能是 50（已完成）。
- VERIFIED：`order_items` 是评价资格与「每个已购项最多一次」的自然锚点，字段含 `id`/`order_id`/`sku_id`/`product_id`/`sku_name`/`product_name`/`product_main_image`/`price`/`quantity`；`sku_id`/`product_id` 为软引用（无 FK）且为下单快照，SKU/商品被删后订单项仍可读。
- VERIFIED：错误码集中在 `internal/codes/codes.go`，已用至域序 9（order 9000-9999）；Registry 无评价域。下一空闲域序 = 10，区间 10000-10999。
- VERIFIED：迁移 golang-migrate v4，`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，当前最新 `20261001000008_refresh_tokens.up.sql`。下一 version = 20261001000009。`migrations_test.go` 的 `latestMigrationVersion` 与 `businessTables` 需同步。
- VERIFIED：RBAC seed `internal/boot/seed.go` 的 `seedPermissionList` 现含 `category/product/sku/inventory/admin/role/permission/order:ship/order:refund`；`review:*` 未登记。权限 code 属 B 类 namespace，不纳入 Registry。
- VERIFIED：事实来源为单一 MySQL；Redis 仅会话；无 MQ。评价为同步写、无异步。
- VERIFIED：前台公开商品详情 `GET /products/:id`（`api/product/v1/product.go` `DetailRes` = SPU + SKUs），未含评价。Out of Scope 明确禁止修改商品/订单模块行为（除最小只读引用），故评价列表与汇总以独立公开接口承载，不嵌入商品详情响应。

## Recommendation

RECOMMENDATION：采用「先发后审 + 软删除 + 永久唯一约束 + 实时聚合汇总」的最小闭环设计。

- 数据模型：新增 `reviews` 表（软引用、无 FK，快照自足），`uk_user_order_item(user_id, order_item_id)` 唯一约束兜底「每个已购项最多一次」；`status` 三态 `1=published / 2=taken_down / 3=deleted`。
- 提交（前台 Auth）：`POST /reviews`，`order_item_id + rating + content` 入参；服务端从 `Principal.UserID` + `order_items`（再向上校验 `orders.status`）推导 `user_id/product_id/sku_id/order_item_id`，不信任客户端归属信息。
- 公开查询（无需 token）：`GET /products/:id/reviews`，返回已发布评价分页列表 + 汇总（`avg_rating` 1 位小数 + `count`），实时聚合，仅 `status=published`。
- 归属隔离（前台 Auth）：`GET /my/reviews`（查看本人全部状态）、`PUT /reviews/:id`（改本人 published）、`DELETE /reviews/:id`（本人软删 → deleted）。
- 管理员（AdminAuth + `RequirePermission("review:take_down")`）：`POST /admin/reviews/:id/take-down`（published → taken_down，软下架）。

关键取舍：

- 汇总「实时聚合」vs「冗余字段 + 事务更新」：选实时聚合。评价量级小、单 MySQL、无缓存层，实时 `COUNT/AVG` 天然一致、无写放大与冗余字段漂移风险；代价是读路径多一次聚合查询（`idx_product_status(product_id, status)` 支撑），当前阶段可接受。
- 删除「软删除 + 永久唯一约束」vs「硬删除 + 可重新评价」：选软删除。严格符合 Goal「每个已购项最多评价一次」，避免「删了再评」造成评价数膨胀与汇总语义复杂化；代价是买家删除后不可重新评价（唯一槽位保留），这符合 V1 的刻意简化取向。
- 可评价状态「已完成(50)」唯一（40 是瞬时态，见证据）；不引入评价有效期窗口，V1 不增加配置与过期语义。

## Selected Design

Owner 已批准推荐方案，确认如下：

- 可评价状态：`orders.status=50(completed)` 为唯一可评价状态；不调整现有 Order 状态机（40 为瞬时态，不持久化）。
- 汇总一致性：实时聚合 `COUNT/AVG`，V1 不引入商品评分冗余字段。
- 审核机制：先发后审，评价创建后默认公开（`published`），管理员可下架；V1 不引入审核队列与富媒体评价。
- 删除语义：软删除 + 永久唯一。评价删除后该 `order_item` 的评价资格视为已消费，不允许重新评价。
- 评价有效期：V1 不设置有效期（收货后不限天数）。
- 唯一约束：`order_item_id` 本身全局唯一（`order_items.id` 主键），故唯一约束直接采用 `UNIQUE(order_item_id)`（不再含 `user_id`），业务语义不变（每个订单项最多一条评价）；另建 `idx_user_id` 支撑本人评价列表。

公开列表与评分汇总只统计 `published` 且未删除的评价；「每个订单项最多一次评价」为永久业务不变量。

## Interfaces and Data

### 数据模型 `reviews`

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用 `users.id`（无 FK），取自 `Principal.UserID` |
| `order_item_id` | BIGINT UNSIGNED | 非空，软引用 `order_items.id`（无 FK），购买资格锚点 |
| `product_id` | BIGINT UNSIGNED | 非空，软引用 `products.id`（无 FK），提交时从 order_item 快照冗余 |
| `sku_id` | BIGINT UNSIGNED | 非空，软引用 `skus.id`（无 FK），提交时从 order_item 快照冗余 |
| `rating` | TINYINT UNSIGNED | 非空，1–5 星，应用层校验 |
| `content` | VARCHAR(500) | 非空，trim 后非空、≤500 字符，应用层校验 |
| `image_urls` | JSON NULL | 可空，预留图片 URL 字段（本次不实现上传，恒为 NULL，不进 API） |
| `status` | TINYINT | 非空默认 1：`1=published`、`2=taken_down`、`3=deleted` |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`uk_order_item(order_item_id)` 唯一（每个订单项最多一条评价）、`idx_user_id(user_id)`（本人评价列表）、`idx_product_status(product_id, status)`（公开列表 + 汇总聚合）。

迁移：`20261001000009_reviews.up.sql`，经 golang-migrate；同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion = 20261001000009` 与 `businessTables`（新增 `reviews`，置于无外键依赖组、置前），推荐同步补入 `expectedSchema` 以纳入结构等价校验。

### API 契约

前台公开（无 token）：

- `GET /products/:id/reviews` → `{items:[{id, product_id, sku_id, rating, content, user_id, created_at}], page, size, total, avg_rating, count}`；仅 `status=published`，默认排序 `id DESC`（最新在前）。

前台用户（`Auth`，作用于 `Principal.UserID`）：

- `POST /reviews`：入参 `order_item_id`（必填）、`rating`（1–5 必填）、`content`（必填、trim 后非空且 ≤500）；服务端推导归属，忽略/拒绝客户端提交的 `user_id/product_id/sku_id`。
- `GET /my/reviews` → 本人全部评价（分页，含全部 status）。
- `PUT /reviews/:id`：入参 `rating`、`content`；仅本人且 `status=published` 可改。
- `DELETE /reviews/:id`：仅本人，`published → deleted`。

后台管理员（`AdminAuth` + `RequirePermission("review:take_down")`）：

- `POST /admin/reviews/:id/take-down`：`published → taken_down`。

### 错误码域（10000-10999，域序 10）

| code | 语义 | HTTP |
| --- | --- | --- |
| 10001 | REVIEW_NOT_FOUND（评价不存在、非本人、或不可见，防枚举） | 404 |
| 10002 | REVIEW_NOT_ELIGIBLE（未购买该商品或订单未达可评价状态） | 409 |
| 10003 | REVIEW_ALREADY_EXISTS（同一订单项已评价） | 409 |
| 10004 | REVIEW_INVALID_INPUT（星级越界、内容为空或超长） | 400 |

复用：`1002`（401，未登录/会话失效）、`1003`（403，无权限）、`1001`（参数格式错误兜底）。

### 权限 code

- 新增 `review:take_down`（下架违规评价），在 `internal/boot/seed.go` 的 `seedPermissionList` 登记。属 B 类 namespace，不纳入 Registry。

## Business Invariants

- INV-001（服务端归属绑定）：`reviews.user_id/product_id/sku_id/order_item_id` 全部由服务端从 `Principal.UserID` 与 `orders + order_items` 推导；客户端提交的任何归属字段被忽略/拒绝。
- INV-002（每个已购项最多一次）：同一 `order_item_id` 至多一条评价（`order_item_id` 全局唯一），由 `uk_order_item` 唯一约束兜底；重复提交返回 10003 且无第二条写入。`reviews.user_id` 与订单归属一致（服务端推导），不参与唯一键。
- INV-003（购买资格）：仅当 `order_item` 属于当前用户且其所属订单 `status=50(completed)` 时可提交；否则返回 10002 且无写入。
- INV-004（归属隔离）：买家仅能查看/修改/删除自己的评价；操作他人或不存在统一返回 10001（404，防枚举）且无写入。
- INV-005（公开可见性）：公开列表与汇总仅含 `status=published`；`taken_down`/`deleted` 即时不再出现在公开列表与汇总。
- INV-006（汇总一致性）：`avg_rating` 与 `count` 实时从 `status=published` 的评价聚合（无冗余字段），与增删/下架天然一致。
- INV-007（权限边界）：管理员下架仅限持 `review:take_down`（含超管 `IsSuper` 放行）；无权限返回 403 且无 DB 副作用。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL；`reviews`（评价事实）、`orders`/`order_items`（购买资格只读引用）。Redis 仅会话，不参与评价；无 MQ、无异步。
- 提交成功 = 单条 `INSERT reviews` 成功（`status=published`）；资格校验为只读的 `orders + order_items` 查询，写入仅 `reviews` 单表，无跨表事务。
- 重复提交 = 命中 `uk_order_item`（MySQL 1062）→ 10003，无第二条写入（并发兜底）。
- 修改成功 = 条件更新 `WHERE id=? AND user_id=? AND status=1` 核对 `RowsAffected`；`RowsAffected=0` → 10001。
- 删除成功 = 条件更新 `status: published→deleted` 核对 `RowsAffected`；重复删除 `RowsAffected=0` → 10001。
- 下架成功 = 条件更新 `status: published→taken_down` 核对 `RowsAffected`；重复下架/已删除 `RowsAffected=0` → 10001。
- 汇总 = 每请求实时 `COUNT(*)` / `AVG(rating)`（`WHERE product_id=? AND status=1`），无一致性与并发窗口。
- 失败语义：认证失败 401、无权限 403、越权/不存在 404（10001，防枚举）、资格不符/重复 409、非法输入 400，均无写入；DB 技术错误 → 1000（500），不泄漏底层细节。

## Allowed / Forbidden Changes

- 允许：新增 `api/review/v1`、`internal/controller/review`、`internal/service` 的 `IReview` 接口与 `internal/logic/review`；`internal/codes/codes.go` 新增 10000–10999 四个码；`internal/migrations/sql/20261001000009_reviews.up.sql`；`migrations_test.go` 的版本/表清单/结构快照更新；`internal/boot/seed.go` 新增 `review:take_down`；`internal/cmd/routes_frontend.go`、`routes_admin.go` 挂载评价路由。
- 允许：为校验购买资格对 `orders`/`order_items` 做最小只读查询（可在 review logic 内直接 `g.DB().Model()` 查询，或复用/新增只读 service 方法）。
- 禁止：修改 `orders`/`order_items`/`products`/`skus`/库存/身份模块的既有行为与公开接口（含商品 `DetailRes` 结构）；禁止引入 Redis 缓存、MQ、异步任务；禁止引入冗余汇总字段；禁止在 Feature Branch 内私留 `RESERVED` 条目。

## Verification Requirements

- INV-001（服务端归属绑定）→ 集成测试（MySQL + Redis）：提交含伪造 `product_id`/`user_id` 的评价，断言服务端以 `Principal` 与订单项为准，伪造值被忽略/拒绝（AC-003）。
- INV-002（每个已购项最多一次）→ 集成测试（MySQL + `-race`）：同一订单项并发/重复提交，断言至多一条评价（唯一约束兜底）（AC-004）。
- INV-003（购买资格）→ 集成测试（MySQL + Redis）：未购买该商品的用户、或订单未达 `completed` 状态提交，断言稳定拒绝且无评价记录（AC-002）；登录买家对已完成订单项提交成功且归属该买家（AC-001）。
- INV-004（归属隔离）→ 集成测试（MySQL + Redis）：用户 B 修改/删除用户 A 的评价，断言稳定拒绝（404，防枚举）且无写入（AC-006）。
- INV-005/INV-006（公开可见性与汇总）→ 集成测试（MySQL）：提交/删除/下架评价后查公开列表，断言列表与 `avg_rating`、`count` 正确变化（AC-005、AC-007）。
- INV-007（权限）→ 集成测试（MySQL + Redis）：无权限管理员及普通用户 token 下架评价，断言 403 且无写入；超管/持 `review:take_down` 成功后评价从公开列表消失（AC-007）。
- 非法输入 → 集成测试（MySQL）：星级越界/内容为空或超长，断言拒绝且无写入（AC-008）。
- 迁移 → 集成测试（MySQL）：执行 `migrate up` 断言 `reviews` 表结构正确、`migrations_test.go` 通过、迁移幂等（AC-009）。
- 长期设计 → 文档审查：`docs/design/review.md` 与 APPROVED Contract、最终实现一致（AC-010）。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Global Resource Reservation

| 类别 | 派生值 | 依据 | 状态 |
| --- | --- | --- | --- |
| 错误码域 | `10000-10999`（域序 10） | `max(已记录域序)=9`（order）`+1`=10 | RESERVED（已写入 Registry，随 reservation commit 生效） |
| migration version | `20261001000009` | `max(已记录 version)=20261001000008`（refresh_tokens）`+1` | RESERVED（已写入 Registry，随 reservation commit 生效） |

待写入 Registry 的 RESERVED 行（由独立 Registry 变更进入 `develop`，非本 Feature Branch）：

- `error-codes.md`：`| 10000-10999 | review | RESERVED | product-review-v1 评价域（域序 10 = max(9)+1） |`
- `migrations.md`：`| 20261001000009 | reviews | product-review-v1 | RESERVED | reviews 评价表 |`

## Open Risks

- 公开评价不 join `users` 昵称，`user_id` 直接暴露：如需展示买家昵称需后续扩展（涉及用户信息查询，超出 V1 Scope）。
- `image_urls` 预留列本次不实现上传，恒为 NULL：上传能力另议。

## Owner Decision Record

Owner 于 2026-10-05 批准（ACCEPT），适用范围为 product-review-v1 全部 Goal/AC：

- Q1：批准 `completed(50)` 为唯一可评价订单状态，不调整现有 Order 状态机。
- Q2：批准实时聚合 `COUNT/AVG`，V1 不引入商品评分冗余字段。
- Q3：批准先发后审；评价创建后默认公开，管理员可下架；V1 不引入审核队列与富媒体评价。
- Q4：批准软删除 + 永久唯一。评价删除后该 `order_item` 的评价资格视为已消费，不允许重新评价。
- Q5：批准 V1 不设置评价有效期。
- 补充约束：公开列表与评分汇总只统计 `published` 且未删除的评价；「每个订单项最多一次评价」为永久业务不变量；`order_item_id` 全局唯一，唯一约束采用 `UNIQUE(order_item_id)`（不含 `user_id`），业务语义不变。

批准当前 Contract 推荐方案；进入实现前按 Workflow 先完成 Registry Reservation（错误码域 `10000-10999`、migration `20261001000009`）。
