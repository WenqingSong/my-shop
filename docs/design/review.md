# 商品评价设计（Review）

本文面向项目接手者，说明「商品评价」核心闭环的架构、数据模型、购买资格校验、汇总一致性、状态与权限边界。事实来源为 `product-review-v1` 的 APPROVED Contract 与最终实现。

## 1. 职责与边界

评价回答「买家对已购商品怎么看」，承载评价主数据（`reviews`），实现：提交评价（星级 + 文字）、购买资格校验（订单项归属 + 订单状态）、每个已购项最多评价一次、商品维度公开评价列表与汇总（平均分 + 条数）、买家归属隔离、管理员下架违规评价。

边界：仅文本评价（无图片上传，仅预留 `image_urls` 字段）；不做点赞/回复/追评/举报；不做排序/推荐/置顶算法（仅最新/默认排序）；不做优惠券/积分/会员激励；不修改既有订单/商品/SKU/库存/身份模块行为（除购买资格校验所需的最小只读引用）。事实来源为单一 MySQL；无 MQ；Redis 仅会话。评价为同步写、无异步。

## 2. 数据模型

### 2.1 `reviews`（评价主数据）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用 `users.id`（无 FK），取自 `Principal.UserID` |
| `order_item_id` | BIGINT UNSIGNED | 非空，软引用 `order_items.id`（无 FK），购买资格锚点 |
| `product_id` | BIGINT UNSIGNED | 非空，软引用 `products.id`（无 FK），提交时从 order_item 快照冗余 |
| `sku_id` | BIGINT UNSIGNED | 非空，软引用 `skus.id`（无 FK），提交时从 order_item 快照冗余 |
| `rating` | TINYINT UNSIGNED | 非空，1–5 星，应用层校验 |
| `content` | VARCHAR(500) | 非空，trim 后非空、≤500 字符，应用层校验 |
| `image_urls` | JSON NULL | 可空，预留图片 URL 字段（本次不实现上传，恒为 NULL） |
| `status` | TINYINT | 非空默认 1：`1=published`、`2=taken_down`、`3=deleted` |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`uk_order_item(order_item_id)` 唯一、`idx_user_id(user_id)`、`idx_product_status(product_id, status)`。

**快照自足**：`product_id`/`sku_id` 为提交时从 `order_items` 快照冗余的软引用，SKU/商品被删除后评价仍可读（与订单项软引用约定一致）。`user_id` 为冗余归属字段，唯一约束落在 `order_item_id`（本身全局唯一），不参与唯一键。

### 2.2 状态映射

| DB `status` | 语义 |
| --- | --- |
| `1` | `published`（公开，参与汇总） |
| `2` | `taken_down`（管理员下架，不公开、不参与汇总） |
| `3` | `deleted`（买家删除，不公开、不参与汇总） |

## 3. 购买资格与状态

- 可评价状态唯一为 `orders.status=50(completed)`；`40(received)` 为瞬时态（`Receive` 同一事务原子推进 30→40→50），不持久化。
- 提交资格：`order_item` 属于当前用户（`orders.user_id == Principal.UserID`，经 `order_items.order_id` 上查）且订单 `status=50`。
- 评价生命周期：`published → taken_down`（管理员下架）、`published → deleted`（买家删除）、`published → published`（买家修改星级/内容，不改状态）。`taken_down`/`deleted` 为终态，不可逆。
- 「每个订单项最多一次评价」为永久业务不变量：`uk_order_item(order_item_id)` 唯一约束兜底，删除后唯一槽位仍保留、不允许重新评价。

## 4. 业务不变量

- INV-001（服务端归属绑定）：`reviews.user_id/product_id/sku_id/order_item_id` 全部由服务端从 `Principal.UserID` 与 `orders + order_items` 推导；客户端提交的归属字段被忽略/拒绝。
- INV-002（每个已购项最多一次）：同一 `order_item_id` 至多一条评价（全局唯一），由 `uk_order_item` 兜底；重复提交 10003 且无第二条写入。
- INV-003（购买资格）：仅当订单项属于当前用户且其订单 `status=50` 时可提交；否则 10002 且无写入。
- INV-004（归属隔离）：买家仅能查看/修改/删除自己的评价；操作他人或不存在统一 10001（404，防枚举）且无写入。
- INV-005（公开可见性）：公开列表与汇总仅含 `status=published`；`taken_down`/`deleted` 即时不再公开。
- INV-006（汇总一致性）：`avg_rating` 与 `count` 实时从 `status=published` 聚合（无冗余字段），随增删/下架天然一致。
- INV-007（权限边界）：管理员下架仅限持 `review:take_down`（含超管 `IsSuper` 放行）；无权限 403 且无 DB 副作用。

## 5. 一致性模型与失败语义

- 事实来源：单一 MySQL。`reviews`（评价事实）、`orders`/`order_items`（购买资格只读引用）。Redis 仅会话，不参与评价；无 MQ、无异步。
- 提交成功 = 单条 `INSERT reviews`（`status=published`）；资格校验为只读查询，写入仅 `reviews` 单表，无跨表事务。
- 修改/删除/下架 = 条件更新 + 核对 `RowsAffected`（修改 `WHERE id AND user_id AND status=1`；删除 `published→deleted`；下架 `published→taken_down`），`RowsAffected=0` → 10001。
- 重复提交 = 命中 `uk_order_item`（1062）→ 10003，并发兜底。
- 汇总 = 每请求实时 `COUNT(*)`/`AVG(rating)`（`WHERE product_id AND status=1`），无一致性与并发窗口。
- 失败语义：认证 401、无权限 403、越权/不存在 404（10001，防枚举）、资格不符/重复 409、非法输入 400，均无写入；DB 技术错误 → 1000（500）。

## 6. 安全与权限边界

- 前台公开接口（无 token）：`GET /products/:id/reviews`（已发布评价分页 + 汇总）。
- 前台用户接口（`Auth`，作用于 `Principal.UserID`）：`POST /reviews`、`GET /my/reviews`、`PUT /reviews/:id`、`DELETE /reviews/:id`。
- 后台写接口（`AdminAuth` + `RequirePermission`）：`POST /admin/reviews/:id/take-down` → `review:take_down`。
- 身份信任：`Principal.UserID` 是用户侧唯一身份来源；下架仅管理员（含超管），经 `RequirePermission`。
- 越权语义：买家越权与不存在统一 10001（404，防枚举）；无权限管理员 403；均无 DB 副作用。
- 权限 code：`review:take_down`（seed 登记 `internal/boot/seed.go`）。

## 7. 错误码域（10000-10999）

| code | 语义 | HTTP |
| --- | --- | --- |
| 10001 | REVIEW_NOT_FOUND（评价不存在、非本人、或不可见） | 404 |
| 10002 | REVIEW_NOT_ELIGIBLE（未购买该商品或订单未达可评价状态） | 409 |
| 10003 | REVIEW_ALREADY_EXISTS（同一订单项已评价） | 409 |
| 10004 | REVIEW_INVALID_INPUT（星级越界、内容为空或超长） | 400 |

复用：`1002`（401）、`1003`（403）、`1001`（参数格式错误兜底）。

## 8. 跨模块关系

- `reviews.order_item_id` → `order_items.id`（软引用，无 FK）；`product_id`/`sku_id` 为提交时从 order_item 快照冗余（软引用，无 FK）。
- 购买资格：提交时读取 `orders`（状态、`user_id`）与 `order_items`（归属、`product_id`/`sku_id`），最小只读引用，不修改订单模块行为。
- 公开列表与汇总为独立接口（`/products/:id/reviews`），不嵌入商品详情响应（避免修改 `products` 模块 `DetailRes`）。
- 建表经 golang-migrate 迁移（`20261001000009_reviews.up.sql`），见 `migration.md`。
- 权限 code `review:take_down` 经 `internal/boot/seed.go` 登记，见 `rbac.md`。

## 9. Deferred / 已知留白

- 评价图片/视频等富媒体上传（仅预留 `image_urls` 字段，恒 NULL，上传能力另议）。
- 点赞、回复、追评、举报流程。
- 排序/推荐/置顶算法（仅最新/默认排序）。
- 公开评价不 join `users` 昵称（`user_id` 直接暴露）；如需展示昵称需后续扩展。
- 评价删除后不可重新评价（永久唯一槽位）；若需「删除后可重新评价」须改硬删除或引入可复用槽位，属 Contract 修订。
