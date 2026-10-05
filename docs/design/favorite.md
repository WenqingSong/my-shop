# 商品收藏设计（Favorite）

本文面向项目接手者，说明「商品收藏」（用户侧）的架构、数据模型、可收藏校验、可用性语义、用户隔离边界与错误码域。事实来源为 `product-favorite-v1` 最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

商品收藏回答「当前登录用户对哪些商品感兴趣」，承载用户侧收藏主数据（`favorites`），实现：添加收藏、取消收藏、查看自己的收藏列表、查询自己对某商品是否已收藏。收藏只归属当前登录用户；**收藏粒度为商品 SPU**（`products.id`，SKU 粒度收藏另立任务）。

边界：收藏为临时用户意图，**不作为商品生命周期的强引用**（`product_id` 采用软引用，不建 FK）；商品下架/删除后收藏保留为软引用并由查询标识不可用，不自动清理。收藏为纯用户域，**不涉及管理员、不新增权限 code、不改 `seed.go`、不涉及 `AdminAuth`/`RequirePermission`**。事实来源为单一 MySQL（`favorites`）；Redis 仅会话；无 MQ、无异步、无缓存、无幂等键。

## 2. 数据模型

### 2.1 `favorites`（收藏主数据）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用 `users.id`（无 FK），取自 `Principal.UserID` |
| `product_id` | BIGINT UNSIGNED | 非空，软引用 `products.id`（无 FK），添加时校验存在且在售 |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP`，即收藏时间 |

唯一约束：`uk_user_product (user_id, product_id)`（同一用户同一商品至多一条，兼作按用户查询索引）。

**无 `updated_at`**：收藏为二元关系（收藏/取消），无字段可变。

**软引用**：`user_id`/`product_id` 均不建 FK。添加时应用层校验商品存在且 `on_shelf`；商品下架/删除后收藏保留为悬空引用，由查询识别并标识不可用。

**不存商品快照**：名称/主图/价格均不落库，查询时实时联查 `products`（与 `cart`/`review` 一致），商品信息变化即时可见。

## 3. 生命周期与可用性语义

收藏无状态机（无持久化状态字段）。生命周期为「添加 → 取消」，均按 `product_id` 维度幂等：

- **添加**：仅当商品存在且 `status=on_shelf` 才成功；重复添加同一 `(user_id, product_id)` 幂等成功（no-op），由 `uk_user_product` 唯一约束兜底并发。
- **取消**：按 `product_id` 作用于本人，`WHERE product_id AND user_id` 物理删除；未收藏/不存在也幂等成功（no-op）。
- **可用性标识**：不自动清理、不自动调整；商品下架/删除后收藏保留，列表查询实时联查 `products`，动态计算 `available`/`unavailable_reason`（`off_shelf`/`product_deleted`）。

## 4. 业务不变量

- INV-001（归属与隔离）：每条收藏只归属 `Principal.UserID`；所有读写在 `user_id` 维度过滤，用户只能查看/取消自己的收藏，任何跨用户操作不产生写入。
- INV-002（用户+商品唯一 + 幂等）：同一用户对同一商品至多一条收藏，由 `uk_user_product` 唯一约束兜底；重复收藏并发下不产生第二条记录（幂等成功）。
- INV-003（可收藏校验）：收藏仅当商品存在且 `on_shelf` 时成功；不存在（4001）或非在售（11001）拒绝且无写入。
- INV-004（取消幂等）：取消收藏按 `product_id` 作用于本人；未收藏/不存在也幂等成功（no-op），不报错、不区分越权。
- INV-005（软引用容忍商品删除）：`product_id` 无 FK；商品下架/删除后收藏保留，列表 `LEFT JOIN` 标识 `off_shelf`/`product_deleted`，不自动清理、不阻止商品状态迁移。

## 5. 一致性模型与失败语义

- 事实来源：单一 MySQL。`favorites`（收藏事实）；`products`（存在性/状态/名称/主图/价格，只读引用）。Redis 仅会话；无 MQ、无异步、无跨系统事务。
- 添加成功 = 收藏持久化（新增或幂等 no-op）；取消成功 = 收藏删除（或幂等 no-op）；列表成功 = 返回时点本人收藏及实时联查的商品信息；check 成功 = 返回时点收藏状态 bool。
- 原子性与并发：`favorites` 单表写，无跨表事务。添加用单条 INSERT，命中 `uk_user_product`（1062）视为幂等成功；取消用 `DELETE WHERE product_id=? AND user_id=?` 核对 `RowsAffected`（=0 视为幂等成功）。无需「先 SELECT 再 INSERT」，唯一约束直接兜底并发重复。
- 可收藏校验与写入之间存在 TOCTOU 窗口（校验通过后商品被下架）：允许——列表/check 在下一次查询即反映最新状态，不做收藏时刻的强一致锁定（与购物车加购同立场）。
- 失败语义：认证失败 401、参数非法 400、商品不存在 404（4001）、商品非在售 409（11001），均无写入；DB 技术错误统一 1000（500），不泄漏底层细节。

## 6. 安全与权限边界

- 前台受保护路由挂载 `internal/cmd/routes_frontend.go` 的 `Auth` 分组（仅登录用户可访问）：

| 方法/路径 | 说明 |
| --- | --- |
| `POST /favorites` | 添加收藏（body `product_id`，幂等） |
| `DELETE /favorites/:product_id` | 取消收藏（按 product_id，幂等） |
| `GET /favorites` | 收藏列表（分页） |
| `GET /favorites/check` | 是否已收藏（query `product_id`，返回 `{favorited: bool}`） |

- 身份信任：不信任请求自带身份；`Principal.UserID` 是唯一身份来源，所有操作经 `user_id` 过滤。
- 越权语义：取消收藏按 `product_id` 天然只作用于本人（不按 favorite_id 操作，故不存在「取消他人收藏」的越权面）；列表/check 仅返回本人数据，不泄露他人收藏。
- 无管理员权限体系参与：收藏仅用户域，不涉及 `AdminAuth`/`RequirePermission`、不新增权限 code、不改 `seed.go`。
- 「是否已收藏」以独立鉴权接口承载，**不改造商品详情**（`GET /products/:id` 保持公开、无 token）。

## 7. 错误码域（11000-11999）

| code | 语义 | HTTP |
| --- | --- | --- |
| 11001 | FAVORITE_PRODUCT_UNAVAILABLE（商品不在售/不可收藏） | 409 |

复用：`4001`（商品不存在 → 404）、`1001`（参数非法 → 400）、`1002`（未认证 → 401）。

## 8. 跨模块关系

- `favorites.product_id` → `products.id`（软引用，无 FK）：添加时经 `IProduct.GetByID` 校验存在与 `on_shelf`（复用既有只读方法，不新增跨模块方法）；商品下架/删除后收藏悬空，由查询标识不可用。
- `favorites.user_id` → `users.id`（软引用，无 FK）：取自 `Principal.UserID`。
- 建表经 golang-migrate 迁移（`20261001000010_favorites.up.sql`），见 `migration.md`。
- 错误码域经全局资源预留（域序 11），权威分配状态见 `.agent/registry/error-codes.md`。

## 9. Deferred / 已知留白

- **SKU 粒度收藏**：本任务为商品 SPU 粒度；SKU 粒度另立任务。
- **收藏分组/标签/备注/排序推荐**：Out of Scope。
- **收藏数量统计公开展示**（如「商品被收藏次数」）：Out of Scope，不实现计数器。
- **收藏数量上限**：V1 不设上限；极端用户可无限收藏，列表分页已覆盖，是否加上限由后续任务决定。
- **后台管理收藏**（管理员查看/清理用户收藏）：Out of Scope，不新增 `favorite:*` 权限 code。
- **商品删除后收藏自动清理**：不自动清理，收藏保留为软引用并标识 `product_deleted`（当前 `products` 无物理删除接口，为未来防御性语义）。
- **详情页内联收藏态**：V1 用独立 `GET /favorites/check`；若未来要求商品详情内联收藏态，需改商品详情为 auth 感知（属 Contract 修订）。
