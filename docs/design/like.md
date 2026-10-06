# 商品点赞设计（Like）

本文面向项目接手者，说明「商品点赞」（用户侧）的架构、数据模型、可点赞校验、可见性/计数语义、用户隔离边界与错误码域。事实来源为 `product-like-v1` 最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

商品点赞回答「当前登录用户对哪些商品表示赞同」，承载用户侧点赞主数据（`product_likes`），实现：点赞、取消点赞、查询自己对某商品是否已点赞、公开查询某商品的点赞数。点赞只归属当前登录用户；**点赞粒度为商品 SPU**（`products.id`，SKU 粒度点赞另立任务）。

边界：点赞是区别于「收藏」的独立用户交互，二者的核心差异是**点赞有公开计数**（商品「X 人点赞」），收藏明确「收藏数量统计公开展示 Out of Scope」。点赞为临时用户意图，**不作为商品生命周期的强引用**（`product_id` 采用软引用，不建 FK）；商品下架/删除后点赞保留为软引用，不自动清理。点赞为纯用户域，**不涉及管理员、不新增权限 code、不改 `seed.go`、不涉及 `AdminAuth`/`RequirePermission`**。事实来源为单一 MySQL（`product_likes`）；Redis 仅会话；无 MQ、无异步、无缓存、无幂等键。

## 2. 数据模型

### 2.1 `product_likes`（点赞主数据）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用 `users.id`（无 FK），取自 `Principal.UserID` |
| `product_id` | BIGINT UNSIGNED | 非空，软引用 `products.id`（无 FK），点赞时校验存在且在售 |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP`，即点赞时间 |

唯一约束：`uk_user_product (user_id, product_id)`（同一用户同一商品至多一条，兼作按用户查询索引）。

索引：`idx_product_id (product_id)`（支撑公开点赞数的实时 COUNT 聚合）。

**无 `updated_at`**：点赞为二元关系（点赞/取消），无字段可变。

**软引用**：`user_id`/`product_id` 均不建 FK。点赞时应用层校验商品存在且 `on_shelf`；商品下架/删除后点赞保留为悬空引用，不自动清理。

**不存商品快照**：名称/主图/价格均不落库，点赞只记录二元关系本身；公开点赞数由实时 COUNT 聚合，不落冗余计数列。

## 3. 生命周期与可见性语义

点赞无状态机（无持久化状态字段）。生命周期为「点赞 → 取消」，均按 `product_id` 维度幂等：

- **点赞**：仅当商品存在且 `status=on_shelf` 才成功；重复点赞同一 `(user_id, product_id)` 幂等成功（no-op），由 `uk_user_product` 唯一约束兜底并发。
- **取消**：按 `product_id` 作用于本人，`WHERE product_id AND user_id` 物理删除；未点赞/不存在也幂等成功（no-op）。
- **本人状态（是否已点赞）**：独立鉴权接口 `GET /likes/check?product_id=` 返回 `{liked: bool}`，不改造商品详情（`GET /products/:id` 保持公开、无 token）。
- **公开点赞数**：公开只读接口 `GET /likes/count?product_id=`（无需 token）返回 `{count}`；计数 = `product_likes` 按 `product_id` 实时 `COUNT(*)`，恒准确、无冗余列、无双重写。

## 4. 业务不变量

- INV-001（归属与隔离）：每条点赞只归属 `Principal.UserID`；所有读写在 `user_id` 维度过滤，用户只能操作自己的点赞，任何跨用户操作不产生写入。
- INV-002（用户+商品唯一 + 幂等）：同一用户对同一商品至多一条点赞，由 `uk_user_product` 唯一约束兜底并发；重复点赞不产生第二条记录（幂等成功）。
- INV-003（可点赞校验）：点赞仅当商品存在且 `on_shelf` 时成功；不存在（4001）或非在售（13001）拒绝且无写入。
- INV-004（取消幂等）：取消点赞按 `product_id` 作用于本人；未点赞/不存在也幂等成功（no-op），不报错、不区分越权。
- INV-005（软引用容忍商品删除）：`product_id` 无 FK；商品下架/删除后点赞保留，不自动清理、不阻止商品状态迁移。
- INV-006（公开计数准确）：公开点赞数 = `product_likes` 实时 COUNT，与点赞/取消事实同步一致，无冗余列、无双重写。

## 5. 一致性模型与失败语义

- 事实来源：单一 MySQL。`product_likes`（点赞事实）；`products`（存在性/状态，只读引用）。Redis 仅会话；无 MQ、无异步、无跨系统事务。
- 点赞成功 = 点赞持久化（新增或幂等 no-op）；取消成功 = 点赞删除（或幂等 no-op）；check 成功 = 返回时点状态 bool；count 成功 = 返回时点计数。
- 原子性与并发：单表写，无跨表事务。点赞单条 INSERT，命中 `uk_user_product`（1062）视为幂等成功；取消 `DELETE WHERE product_id=? AND user_id=?` 核对 `RowsAffected`（=0 视为幂等成功）。无需「先 SELECT 再 INSERT」，唯一约束直接兜底并发重复。
- 可点赞校验与写入之间存在 TOCTOU 窗口（校验通过后商品被下架）允许：check/count 下一次查询即反映最新状态，与收藏同立场。
- 失败语义：认证失败 401、参数非法 400、商品不存在 404（4001）、商品非在售 409（13001），均无写入；DB 技术错误统一 1000（500），不泄漏底层细节。

## 6. 安全与权限边界

- 前台受保护路由挂载 `internal/cmd/routes_frontend.go` 的 `Auth` 分组（仅登录用户可访问），公开计数接口除外：

| 方法/路径 | 说明 |
| --- | --- |
| `POST /likes` | 点赞（body `product_id`，幂等） |
| `DELETE /likes/:product_id` | 取消点赞（按 product_id，幂等） |
| `GET /likes/check` | 是否已点赞（query `product_id`，鉴权，返回 `{liked: bool}`） |
| `GET /likes/count` | 公开点赞数（query `product_id`，无 token，返回 `{count}`） |

- 身份信任：不信任请求自带身份；`Principal.UserID` 是唯一身份来源，所有写操作与本人状态查询经 `user_id` 过滤。
- 越权语义：取消点赞按 `product_id` 天然只作用于本人（不按 like_id 操作，故不存在「取消他人点赞」的越权面）；check 仅返回本人状态，不泄露他人点赞。
- 无管理员权限体系参与：点赞仅用户域，不涉及 `AdminAuth`/`RequirePermission`、不新增权限 code、不改 `seed.go`。
- 「是否已点赞」以独立鉴权接口承载，**不改造商品详情**；「公开点赞数」以点赞模块自有公开只读接口承载，**不改商品模块**。

## 7. 错误码域（13000-13999）

| code | 语义 | HTTP |
| --- | --- | --- |
| 13001 | LIKE_PRODUCT_UNAVAILABLE（商品不在售/不可点赞） | 409 |

复用：`4001`（商品不存在 → 404）、`1001`（参数非法 → 400）、`1002`（未认证 → 401）。

## 8. 跨模块关系

- `product_likes.product_id` → `products.id`（软引用，无 FK）：点赞时经 `IProduct.GetByID` 校验存在与 `on_shelf`（复用既有只读方法，不新增跨模块方法）；商品下架/删除后点赞悬空。
- `product_likes.user_id` → `users.id`（软引用，无 FK）：取自 `Principal.UserID`。
- 建表经 golang-migrate 迁移（`20261001000013_product_likes.up.sql`），见 `migration.md`。
- 错误码域经全局资源预留（域序 13），权威分配状态见 `.agent/registry/error-codes.md`。
- 与收藏（`favorite.md`）的边界：点赞与收藏为独立交互、独立表、独立错误码域；核心语义差异 = 点赞有公开计数、收藏无公开计数。

## 9. Deferred / 已知留白

- **SKU 粒度点赞**：本任务为商品 SPU 粒度；SKU 粒度另立任务。
- **点赞分组/标签/备注/排序/推荐/排行榜**：Out of Scope。
- **匿名/游客点赞**：V1 点赞需登录（per-user 状态）；匿名计数另立任务。
- **后台管理点赞**（管理员查看/清理用户点赞）：Out of Scope，不新增 `like:*` 权限 code。
- **商品删除后点赞自动清理**：不自动清理，点赞保留为软引用（当前 `products` 无物理删除接口，为未来防御性语义）。
- **点赞数性能**：公开点赞数用实时 COUNT，商品量极大时每次聚合有扫描代价，已用 `idx_product_id` 缓解；后续可升级为冗余计数列（属 Contract Revision）。
- **详情页内联点赞态**：V1 用独立 `GET /likes/check`；若未来要求商品详情内联点赞态/点赞数，需改商品详情为 auth 感知或内联计数（属 Contract Revision）。
