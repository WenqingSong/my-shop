# Technical Contract

## Decision Status
APPROVED

## Problem

交付用户侧「商品点赞」核心闭环：登录用户可对商品（SPU）点赞/取消点赞、查询自己对指定商品是否已点赞；重复点赞不产生重复条目；点赞归属当前登录用户。点赞是区别于「收藏」的独立用户交互，不改造收藏/评价/商品既有行为。

阻止直接实现的关键设计选择只有两点：**（1）点赞的对外可见性语义**——公开点赞数、本人状态、还是两者；（2）据此确定**数据模型**——per-user 点赞表 + 实时 COUNT 聚合 vs 冗余计数列。其余（可点赞校验、重复/取消语义、是否已点赞提供方式、错误码）均可镜像已验证的收藏模块模式，无需额外权衡。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问 `g.DB().Model()`，无 dao/model 层（`agents.md`、`internal/` 目录结构）。
- VERIFIED：前台用户经 `middleware.Auth` 注入 `Principal{UserID, Sid}`；受保护路由挂 `internal/cmd/routes_frontend.go` 的 `Auth` 分组（现含 `/favorites`、`/reviews` 等）。点赞为纯用户域，不涉及 `AdminAuth`/`RequirePermission`。
- VERIFIED：商品 `products.status` TINYINT（0=draft/1=on_shelf/2=off_shelf）；`IProduct.GetByID` 返回任意状态商品、`productv1.StatusOnShelf = "on_shelf"` 可复用（`internal/service/product.go`、`api/product/v1/product.go`）。
- VERIFIED：收藏模块 `favorites` 是点赞的同构参考：`uk_user_product (user_id, product_id)` 唯一约束、软引用无 FK、添加校验「存在且 on_shelf」、重复收藏 1062 幂等成功、取消 `RowsAffected=0` 幂等成功、独立鉴权 `GET /favorites/check`、公开计数明确 Out of Scope（`internal/logic/favorite/favorite.go`、`docs/design/favorite.md`）。
- VERIFIED：错误码集中在 `internal/codes/codes.go`，已分配至域序 12（flash-sale，RESERVED）。点赞需新增域序 13 = `13000-13999`。
- VERIFIED：迁移 golang-migrate v4，当前最新 `20261001000012_product_view_count`（RESERVED）。点赞新增 version `20261001000013`；需同步 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion`/`businessTables`/`expectedSchema`。
- VERIFIED：事实来源单一 MySQL；Redis 仅会话；无 MQ。点赞为同步写、无异步。
- UNKNOWN：商品当前无物理删除接口（`docs/design/product.md` §9），「商品删除后点赞」为未来防御性语义，与收藏一致按软引用处理，无额外证据缺口。

## Recommendation

RECOMMENDATION（单一推荐方案）：

1. **数据模型**：新增 `product_likes` 表（per-user 点赞主数据），`uk_user_product (user_id, product_id)` 唯一约束兜底并发去重，`user_id`/`product_id` 软引用不建 FK，`created_at` 点赞时间；若实现公开计数，另加 `idx_product_id (product_id)` 支撑按商品聚合。完全镜像 `favorites` 成熟模式。
2. **写语义**：点赞 = 幂等成功（单条 INSERT，命中 1062 视为 no-op）；取消 = 幂等成功（`DELETE WHERE user_id AND product_id`，`RowsAffected=0` 也成功）。与收藏一致。
3. **可点赞校验**：商品存在且 `on_shelf` 才可点赞；不存在 → 404（复用 `4001`）、非在售 → 409（新增 `13001`）。商品下架/删除后已有点赞保留为软引用、不自动清理。
4. **本人状态**：独立鉴权 `GET /likes/check` 返回 `{liked: bool}`，不改造商品详情（保持 `GET /products/:id` 公开无 token）。
5. **公开点赞数（核心语义差异点）**：推荐实现，采用**实时 COUNT 聚合**（`SELECT COUNT(*) FROM product_likes WHERE product_id=?`），由点赞模块自有**公开只读接口** `GET /likes/count?product_id=`（无需 token）返回 `{count}`；不引入 `products.like_count` 冗余列、不改商品模块。

关键取舍（仅公开计数一处需要权衡）：
- 实时 COUNT 聚合（推荐）：计数恒准确、无冗余、无双重写；代价是每次读取需聚合查询，以 `idx_product_id` 索引支撑。
- 冗余计数列 `products.like_count` 原子自增：读更快，但引入 `product_likes` 与 `like_count` 双写一致性，以及取消/重复/下架/删除场景的计数回退复杂度，V1 不推荐。
- 仅本人状态（无公开计数）：点赞与收藏几乎同构，失去「点赞区别于收藏」的核心语义，除非 Owner 明确要求最小化，否则不推荐。

## Selected Design

- 数据模型：新增 `product_likes` 表，`uk_user_product (user_id, product_id)` 唯一约束兜底并发去重，`user_id`/`product_id` 软引用不建 FK，`created_at` 点赞时间；加 `idx_product_id (product_id)` 支撑公开计数聚合。镜像 `favorites` 模式。
- 写语义：点赞幂等成功（单条 INSERT，命中 1062 视为 no-op）；取消幂等成功（`DELETE WHERE user_id AND product_id`，`RowsAffected=0` 也成功）。
- 可点赞校验：仅商品存在且 `on_shelf` 才可点赞；不存在 → 404（4001）、非在售 → 409（13001）；下架/删除后历史点赞保留为软引用、不自动清理。
- 本人状态：独立鉴权 `GET /likes/check?product_id=` 返回 `{liked: bool}`，不改造商品详情。
- 公开点赞数：实时 COUNT 聚合（`SELECT COUNT(*) FROM product_likes WHERE product_id=?`），公开只读接口 `GET /likes/count?product_id=`（无需 token）返回 `{count}`，不引入 `products.like_count` 冗余列、不改商品模块。
- 错误码域 `13000-13999`（`13001` LIKE_PRODUCT_UNAVAILABLE → 409）；migration `20261001000013_product_likes`。

## Global Resources

- 错误码域：`13000-13999`（域序 13 = max(12)+1，语义：商品点赞）。
  - `13001` LIKE_PRODUCT_UNAVAILABLE（商品不在售/不可点赞）→ HTTP 409。
- Migration：`20261001000013_product_likes`（next = max(已记录 20261001000012)+1）。

上述派生自 `.agent/registry/error-codes.md` 与 `.agent/registry/migrations.md`，Contract APPROVED 后由 Analyst 以 Registry-only commit 落 `RESERVED`。

## Interfaces and Data

- 表 `product_likes`：`id`（BIGINT UNSIGNED 主键自增）、`user_id`（BIGINT UNSIGNED 非空，软引用 `users.id`）、`product_id`（BIGINT UNSIGNED 非空，软引用 `products.id`）、`created_at`（DATETIME 默认 CURRENT_TIMESTAMP）；`UNIQUE KEY uk_user_product (user_id, product_id)`；若公开计数则加 `KEY idx_product_id (product_id)`。无 `updated_at`（二元关系，无字段可变）。
- API（前台 Auth 分组 + 公开计数接口）：
  - `POST /likes`（body `product_id`，点赞，幂等）→ `{liked: bool}`（或等价条目信息）。
  - `DELETE /likes/:product_id`（取消点赞，幂等）→ 空响应。
  - `GET /likes/check?product_id=`（鉴权，是否已点赞）→ `{liked: bool}`。
  - `GET /likes/count?product_id=`（公开，无 token，点赞数）→ `{count: int}`（仅当 Owner 选择公开计数）。
- 复用既有只读方法：`IProduct.GetByID`（校验商品存在与 on_shelf），不新增跨模块方法。
- 错误码：新增 `13001`；复用 `4001`（商品不存在 → 404）、`1001`（参数非法 → 400）、`1002`（未认证 → 401）、`1000`（内部错误 → 500）。

## Business Invariants

- INV-001（归属与隔离）：每条点赞只归属 `Principal.UserID`；所有读写在 `user_id` 维度过滤，用户只能操作自己的点赞，任何跨用户操作不产生写入。
- INV-002（用户+商品唯一 + 幂等）：同一用户对同一商品至多一条点赞，由 `uk_user_product` 唯一约束兜底并发；重复点赞不产生第二条记录（幂等成功）。
- INV-003（可点赞校验）：点赞仅当商品存在且 `on_shelf` 时成功；不存在（4001）或非在售（13001）拒绝且无写入。
- INV-004（取消幂等）：取消点赞按 `product_id` 作用于本人；未点赞/不存在也幂等成功（no-op），不报错、不区分越权。
- INV-005（软引用容忍商品删除）：`product_id` 无 FK；商品下架/删除后点赞保留，不自动清理、不阻止商品状态迁移。
- INV-006（公开计数准确，仅当实现公开计数）：公开点赞数 = `product_likes` 实时 COUNT，与点赞/取消事实同步一致，无冗余列、无双重写。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL。`product_likes`（点赞事实）；`products`（存在性/状态，只读引用）。Redis 仅会话；无 MQ、无异步、无跨系统事务。
- 点赞成功 = 点赞持久化（新增或幂等 no-op）；取消成功 = 点赞删除（或幂等 no-op）；check 成功 = 返回时点状态 bool；count 成功 = 返回时点计数。
- 原子性与并发：单表写，无跨表事务。点赞单条 INSERT，命中 `uk_user_product`（1062）视为幂等成功；取消 `DELETE WHERE product_id=? AND user_id=?` 核对 `RowsAffected`（=0 视为幂等成功）。无需「先 SELECT 再 INSERT」，唯一约束直接兜底并发重复。
- 可点赞校验与写入之间的 TOCTOU 窗口（校验通过后商品被下架）允许：check/count 下一次查询即反映最新状态，与收藏同立场。
- 失败语义：认证失败 401、参数非法 400、商品不存在 404（4001）、商品非在售 409（13001），均无写入；DB 技术错误统一 1000（500），不泄漏底层细节。

## Allowed / Forbidden Changes

- 允许：新增 `api/like/v1`、`internal/controller/like`、`internal/logic/like`、`internal/service/like.go`（`ILike` 接口 + `Register`）；`internal/codes/codes.go` 新增 `13001`；新增迁移 `20261001000013_product_likes.up.sql` 并同步 `migrations_test.go`；`internal/cmd/routes_frontend.go` 路由扩展；点赞域测试；`docs/design/like.md`。
- 禁止：改造商品详情/列表为 auth 感知或内联点赞数（除非 Owner 明确改选）；修改收藏/评价/商品既有行为；新增 `like:*` 权限 code 或涉及 `AdminAuth`/`RequirePermission`/`seed.go`；引入 `products.like_count` 冗余计数列（除非 Owner 改选）；SKU 粒度点赞；匿名/游客点赞；点赞列表/分组/排序推荐。

## Verification Requirements

- INV-002 → 同一用户并发/重复点赞同一商品，`-race` 下断言 `product_likes` 至多一条（唯一约束兜底）。
- INV-003 → 点赞不存在商品返回 404（4001）无写入；点赞 draft/off_shelf 商品返回 409（13001）无写入。
- INV-004 → 取消未点赞的商品幂等成功；取消他人点赞面不存在（按 product_id 天然只作用本人）。
- INV-001 → 用户 B 查询/操作用户 A 的点赞，断言稳定拒绝（404 防枚举）且无写入。
- INV-006（若公开计数）→ 点赞/取消后 `GET /likes/count` 计数正确变化；并发点赞计数仍准确。
- AC-008 → 无 token/非法 token 访问点赞接口返回 401 且无数据。
- AC-009 → 迁移建立 `product_likes` 结构正确、`migrations_test.go` 通过、迁移可重复执行。
- AC-010 → `docs/design/like.md` 与 APPROVED Contract、最终实现一致。
- 环境：MySQL 8.0 + Redis 7（`docker compose up -d`）；集成测试走真实 `RegisterFrontendRoutes` + `middleware.Auth`。

## Open Risks

- 公开点赞数若用实时 COUNT，商品量极大时每次聚合有扫描代价；已用 `idx_product_id` 缓解，V1 规模下可控，后续可升级为冗余计数列（属 Contract Revision）。
- 点赞与收藏接口/语义高度同构，需在 `docs/design/like.md` 明确二者边界（核心差异 = 公开计数）。

## Owner Decision Record

- D-001（公开点赞数）：实现公开点赞数，采用实时 COUNT 聚合（公开只读接口 `GET /likes/count`），不引入 `products.like_count` 冗余计数列。
- D-002（可点赞校验）：仅 `on_shelf` 商品可新增点赞；商品下架/删除后历史点赞保留为软引用、不自动清理。
- D-003（重复/取消语义）：点赞与取消均幂等成功（no-op）。

适用范围：`product-like-v1` 全量（数据模型、写语义、可点赞校验、本人状态、公开计数、错误码域、migration）。
