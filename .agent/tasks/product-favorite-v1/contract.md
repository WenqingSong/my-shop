# Technical Contract

## Decision Status

APPROVED

（2026-10-05 Owner 确认全部 5 项决定：重复收藏幂等成功、取消收藏按 `product_id` 幂等、仅 `on_shelf` 可收藏、商品下架/删除后已有收藏保留并标识不可用、独立 `GET /favorites/check` 不改商品详情。全部与 Analyst 推荐一致。Contract 与 Task 兼容，转 APPROVED。）

## Problem

交付用户侧「商品收藏」V1 核心闭环：登录用户可收藏商品（SPU）、取消收藏、查看自己的收藏列表、获知自己对某商品是否已收藏；收藏仅归属当前登录用户，重复收藏不产生重复条目，任何越权操作均不产生数据库写入。

任务边界：收藏为单一 MySQL 事实来源（无 MQ、无异步、无缓存、无幂等键）；收藏粒度为商品 SPU（`products.id`，SKU 粒度明确 Out of Scope）；收藏为纯用户域（不涉及 `AdminAuth`/`RequirePermission`、不新增权限 code、不改 `seed.go`）；不改动商品/用户/IAM 模块既有行为（除收藏引用所需的最小只读查询 `IProduct.GetByID`）。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta`）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问 `g.DB().Model()`，无 dao/model 层。证据：`internal/logic/cart/cart.go`、`internal/service/cart.go`。
- VERIFIED：用户身份经 `internal/middleware/auth.go` 的 `Auth` 解析 `type=user` token 并注入 `Principal{UserID, Sid}`；controller 经 `middleware.PrincipalFromContext(ctx)` 读取。前台受保护路由挂载于 `internal/cmd/routes_frontend.go` 的 `user.Middleware(middleware.Auth)` 分组（`internal/controller/cart/cart.go` 的 `currentUserID` 示范）。
- VERIFIED：`products` 表 `status` TINYINT（0=draft/1=on_shelf/2=off_shelf），前台公开详情 `GET /products/:id` 仅返回 `on_shelf`（`api/product/v1/product.go`）。**`IProduct.GetByID(ctx, id)` 已存在**：返回全字段、含任意 `status`（字符串枚举 `"draft"/"on_shelf"/"off_shelf"`）、不存在返回 nil（`internal/service/product.go:32`、`internal/logic/product/product.go:302`），收藏可直接复用它校验商品存在性与在售状态，**无需新增跨模块只读方法**。
- VERIFIED：`products` 无物理删除接口（`IProduct` 仅有 Create/Update/OnShelf/OffShelf，无 Delete；`internal/logic/product` 仅 `product_images` 有 Delete）。商品生命周期为状态迁移，非物理删除；`product_deleted` 是未来防御性语义。
- VERIFIED：类似模块参考——`cart`（`cart_items`，`user_id` 维度隔离、`uk_user_sku` 唯一约束兜底并发、软引用无 FK、LEFT JOIN 容忍悬空引用）；`review`（`reviews`，`uk_order_item` 唯一约束、软引用无 FK、`isDuplicateKeyError` 识别 1062）。收藏在数据模型与隔离语义上与其同构，可复用其实现模式。
- VERIFIED：错误码集中 `internal/codes/codes.go`，已分配至域序 10（product-review-v1，10000-10999，RESERVED）。下一空闲域序 = 11，区间 11000-11999。
- VERIFIED：迁移 golang-migrate v4，`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，当前最新 `20261001000009_reviews`（RESERVED）。下一 version = 20261001000010。`internal/migrations/migrations_test.go` 硬编码 `latestMigrationVersion` 与 `businessTables`（并按 FK 依赖倒序 drop），新增 `favorites` 必须同步更新。
- VERIFIED：事实来源为单一 MySQL；Redis 仅会话；无 MQ。收藏为同步写、无异步。
- VERIFIED：全项目无任何 `favorite`/`favorites` 代码、表、迁移或错误码（`grep -ri favorite` 无命中），为全新模块。
- UNKNOWN：无阻塞性 UNKNOWN。以下为待 Owner 确认的设计选择（非事实缺口）：重复收藏语义、取消收藏语义与锚点、可收藏校验是否限定 `on_shelf`、下架后已有收藏的处理、是否已收藏接口形态。

## Recommendation

RECOMMENDATION：单表 `favorites`（`user_id` + `product_id` 联合唯一，软引用无 FK，不存商品快照、查询时实时联查 `products`）；添加收藏幂等成功、取消收藏按 `product_id` 幂等成功、重复收藏由 `uk_user_product` 唯一约束兜底并发；可收藏校验限定商品 `on_shelf`；商品下架/删除后已有收藏保留为软引用、查询时标识不可用（不自动清理）；「是否已收藏」以独立鉴权查询接口承载（不改商品详情）。

关键取舍：**「取消收藏按 `product_id` 幂等」而非「按 `favorite_id` + 404 防枚举」**——收藏是「用户-商品」二元关系，前端商品详情页只知道 `product_id`、以「收藏/取消」toggle 交互，按 `product_id` 操作天然只作用于本人、天然幂等，无需泄露 favorite_id 给前端；代价是「取消一个未收藏的商品」返回成功（no-op）而非 404，这是收藏区别于购物车（`DELETE /cart/items/:id` 按条目 id 且 404）的业务语义差异。

分项推荐：

1. **数据模型**：`favorites` 表 `id`、`user_id`、`product_id`、`created_at`（**无 `updated_at`**，收藏无更新操作）；`UNIQUE uk_user_product (user_id, product_id)` 兜底「同一用户同一商品至多一条」，兼作按用户查询索引（`user_id` 前导列）。**不建 FK**（软引用，商品删除后条目保留为悬空引用）。**不存商品快照**（名称/主图/价格实时联查 `products`），与 cart/review 一致。
2. **添加收藏（幂等）**：`POST /favorites`（body `product_id`），校验 `product_id > 0` → `IProduct.GetByID` 判存在（nil → 4001 404）→ 判 `status == on_shelf`（非 on_shelf → 11001 409）→ 单条 INSERT；命中 `uk_user_product`（1062）视为幂等成功（no-op，不报错）。
3. **取消收藏（幂等，按 product_id）**：`DELETE /favorites/:product_id`，`WHERE product_id=? AND user_id=?` 物理删除；`RowsAffected=0`（未收藏/不存在）视为幂等成功（no-op），不报错、不区分越权（按 product_id 天然只作用于本人）。
4. **收藏列表（分页）**：`GET /favorites?page=&size=`，`WHERE user_id=?` 过滤，按 `created_at DESC`（或 `id DESC`）排序，分页默认 page=1/size=20/max=100；返回项含收藏时间与实时联查的商品信息，商品下架/删除时标识不可用。
5. **是否已收藏（独立鉴权查询）**：`GET /favorites/check?product_id=` 返回 `{favorited: bool}`；不改造商品详情（Out of Scope 明确不改商品模块行为，且商品详情为公开接口，改造成 auth 感知会引入可选认证复杂度）。
6. **下架/删除后已有收藏**：软引用保留 + 查询时标识不可用（`available=false`，`unavailable_reason="off_shelf"`/`"product_deleted"`），不自动清理。
7. **错误码域（11000-11999）**：仅新增 `11001 FAVORITE_PRODUCT_UNAVAILABLE`（商品不在售/不可收藏 → 409）；商品不存在复用 `4001`（404），参数非法复用 `1001`（400），未认证复用 `1002`（401）。
8. **收藏数量上限**：V1 不设上限（Task 未要求，避免不必要复杂度），留待后续任务。

## Selected Design

Owner 已确认（2026-10-05）全部设计问题，均与 Recommendation 一致：

1. **重复收藏语义（Q1）**：幂等成功。同一 `(user_id, product_id)` 重复添加返回成功（no-op），由 `uk_user_product` 唯一约束兜底并发，不产生第二条记录。
2. **取消收藏（Q2）**：按 `product_id` 操作、幂等成功；未收藏/不存在时 no-op（不报错、不区分越权）。前端以商品维度 toggle，天然只作用于本人。
3. **可收藏校验（Q3）**：仅 `on_shelf` 商品允许新增收藏；不存在返回 4001（404）、非在售（draft/off_shelf）返回 11001（409）。
4. **下架/删除后已有收藏（Q4）**：软引用保留、不自动清理；列表 `LEFT JOIN` 实时标识 `available=false`、`unavailable_reason="off_shelf"`/`"product_deleted"`。
5. **是否已收藏接口（Q5）**：V1 使用独立 `GET /favorites/check`（返回 `{favorited: bool}`），不修改 Product Detail。

其余（数据模型、路由、错误码域 11000-11999、软引用无 FK、不存商品快照、列表分页、无收藏数量上限等）按 Recommendation 落实，不再重新展开。

## Interfaces and Data

### 数据模型 `favorites`（golang-migrate 新增单文件迁移）

迁移文件：`internal/migrations/sql/20261001000010_favorites.up.sql`（version 紧随 reviews `...009`，见 Global Resource Reservation）。DDL 不使用 `IF NOT EXISTS`。

```sql
CREATE TABLE favorites (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    BIGINT UNSIGNED NOT NULL,
  product_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_product (user_id, product_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

- `user_id`/`product_id` 均为软引用（无 FK），取自 `Principal.UserID` 与请求 `product_id`（经 `IProduct.GetByID` 校验存在与在售）。
- **无 `updated_at`**：收藏无更新操作（添加/取消二元，无字段可变）。
- `uk_user_product (user_id, product_id)` 唯一约束兜底「同一用户同一商品至多一条」，并作为按用户查询索引（`user_id` 前导列）。
- 同步更新 `internal/migrations/migrations_test.go`：`latestMigrationVersion` → `20261001000010`；`businessTables` 最前方插入 `"favorites"`（无 FK 依赖，置于最前 drop）；并同步补入 `expectedSchema` 以纳入结构等价校验。

### API 契约（`api/favorite/v1`，前台 `Auth` 分组）

| 方法/路径 | 说明 |
| --- | --- |
| `POST /favorites` | 添加收藏，body `product_id`（必填、>0）；幂等 |
| `DELETE /favorites/:product_id` | 取消收藏（按 product_id，幂等） |
| `GET /favorites` | 收藏列表，query `page`/`size`（分页） |
| `GET /favorites/check` | 是否已收藏，query `product_id`，返回 `{favorited: bool}` |

列表项 `FavoriteItem` 字段（实时联查 `products`，LEFT JOIN 容忍悬空）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | int64 | 收藏条目 id |
| `product_id` | int64 | 商品 id |
| `product_name` | string | 商品名（实时联查；商品删除时为空） |
| `product_main_image` | string | 商品主图（实时联查） |
| `product_price` | *int64 | 商品价格整数分（实时联查；商品删除时为 null） |
| `available` | bool | 商品当前是否 `on_shelf` |
| `unavailable_reason` | string | `""`（可用）｜`"off_shelf"`（下架）｜`"product_deleted"`（商品不存在） |
| `created_at` | time | 收藏时间 |

### 代码接口

- 新增 `api/favorite/v1`、`internal/controller/favorite`、`internal/service/favorite.go`（`IFavorite` + `RegisterFavorite`）、`internal/logic/favorite`（`init()` 注册）；`internal/logic/logic.go` 注册 favorite 包。
- `IFavorite` 接口：`Add(ctx, userID, req) (*v1.AddRes, error)`；`Remove(ctx, userID, productID) error`；`List(ctx, userID, req) (*v1.ListRes, error)`；`Check(ctx, userID, productID) (*v1.CheckRes, error)`。
- Controller 经 `middleware.PrincipalFromContext(ctx)` 取 `UserID` 传入 service；所有操作以 `UserID` 为唯一身份来源。
- **无需新增跨模块只读方法**：复用 `IProduct.GetByID`（已返回任意 status、nil when not found）校验商品存在性与 `on_shelf`。

### 错误码新增（`internal/codes/codes.go`）

| code | 语义 | HTTP |
| --- | --- | --- |
| 11001 | FAVORITE_PRODUCT_UNAVAILABLE（商品不在售/不可收藏） | 409 |

复用：`4001`（商品不存在 → 404）、`1001`（参数非法 → 400）、`1002`（未认证 → 401）。

## Business Invariants

- INV-001（归属与隔离）：每条收藏只归属 `Principal.UserID`；所有读写在 `user_id` 维度过滤，用户只能查看/取消自己的收藏，任何跨用户操作不产生写入。
- INV-002（用户+商品唯一 + 幂等）：同一用户对同一商品至多一条收藏，由 `uk_user_product` 唯一约束兜底；重复收藏并发下不产生第二条记录（幂等成功）。
- INV-003（可收藏校验）：收藏仅当商品存在且 `status=on_shelf` 时成功；不存在（4001）或非在售（11001）拒绝且无写入。
- INV-004（取消幂等）：取消收藏按 `product_id` 作用于本人；未收藏/不存在也幂等成功（no-op），不报错。
- INV-005（软引用容忍商品删除）：`product_id` 无 FK；商品下架/删除后收藏保留，列表 `LEFT JOIN` 标识 `off_shelf`/`product_deleted`，不自动清理、不阻止商品状态迁移。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL。`favorites`（收藏事实）；`products`（商品存在性/状态/名称/主图/价格，只读引用）。Redis 仅会话；无 MQ、无异步、无跨系统事务。
- 添加成功 = 收藏持久化（新增或幂等 no-op）；取消成功 = 收藏删除（或幂等 no-op）；列表成功 = 返回时点本人收藏及实时联查的商品信息；check 成功 = 返回时点收藏状态 bool。
- 原子性与并发：`favorites` 单表写，无跨表事务。添加用单条 INSERT，命中 `uk_user_product`（1062）视为幂等成功；取消用 `DELETE WHERE product_id=? AND user_id=?` 核对 `RowsAffected`（=0 视为幂等成功）。**无需「先 SELECT 再 INSERT」，由唯一约束直接兜底并发重复**（与 review 的 1062 兜底同机制，但语义为幂等成功而非拒绝）。
- 可收藏校验与写入之间存在 TOCTOU 窗口（校验通过后商品被下架）：允许——列表/check 在下一次查询即反映最新状态，本任务不做收藏时刻的强一致锁定（与 cart 加购同立场）。
- 校验失败（未认证 401、参数非法 400、商品不存在 4001、商品非在售 11001）：写入前拒绝且无写入。
- 非幂等重试说明：添加/取消均为幂等语义，客户端重试无副作用（区别于 cart 的数量累加语义）。
- DB 技术错误统一 `CodeInternalError`（500），不泄漏底层细节。

## Allowed / Forbidden Changes

允许：
- 新增迁移 `20261001000010_favorites.up.sql`；同步更新 `migrations_test.go` 的 `latestMigrationVersion`、`businessTables` 与 `expectedSchema`。
- 新增 `api/favorite/v1`、`internal/controller/favorite`、`internal/service/favorite.go`（`IFavorite`）、`internal/logic/favorite`、错误码 `11001`、`routes_frontend.go` 收藏路由。
- 收藏校验复用 `IProduct.GetByID`（只读引用，不改其语义）。
- 新增对应测试（见 Verification Requirements）。

禁止：
- 不改动已合入的 baseline/products/skus/... 迁移及迁移机制本身；不回退到 `boot.go` 建表。
- 不改动 `products`/`users` 表结构；不改动商品/用户/IAM 模块既有接口语义与公开行为（含商品 `DetailRes` 结构）。
- 不为 `favorites.user_id`/`product_id` 建 FK（软引用）；不新增 `favorite:*` 权限 code、不改 `seed.go`、不涉及 `AdminAuth`/`RequirePermission`。
- 不引入 SKU 粒度收藏、收藏分组/标签/备注、收藏数量统计展示、收藏数量上限、自动清理收藏、后台管理收藏。
- 不 fail-open 放行未认证请求；不在 SQL 中拼接前端输入；不信任客户端提交的 `user_id`（一律以 `Principal.UserID` 为准）。

## Verification Requirements

- INV-001 → MySQL + Redis：用户 B 查列表/取消用户 A 的收藏，断言只含本人收藏、跨用户无写入；两个真实用户注册登录验证（AC-006）。
- INV-002 → MySQL + `-race`：同一用户并发/重复收藏同一商品，断言至多一条记录且均幂等成功（AC-002）。
- INV-003 → MySQL：收藏不存在商品（4001）、off_shelf/draft 商品（11001），断言拒绝且无写入（AC-007）。
- INV-004 → MySQL：取消一个未收藏的商品，断言幂等成功且无错误；取消已收藏商品后库中记录删除（AC-003）。
- 列表 → MySQL + Redis：收藏后查列表，断言分页、收藏时间正确、只含本人收藏、商品信息实时联查正确（AC-004）。
- 是否已收藏 → MySQL + Redis：收藏后 `check` 返回 true，取消后返回 false（AC-005）。
- 未认证 → MySQL + Redis：无 token/非法 token 访问收藏接口，断言 401 且无数据返回（AC-008）。
- 迁移结构 → MySQL：`latestMigrationVersion`/`businessTables`/`expectedSchema` 更新后 `go test ./internal/migrations/` 通过，迁移幂等（AC-009）。
- 长期设计 → 文档审查：`docs/design/favorite.md` 与 APPROVED Contract、最终实现一致（AC-010）。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；MySQL/Redis 集成验证需容器就绪（`docker compose up -d`）。

## Global Resource Reservation

| 类别 | 派生值 | 依据 | 状态 |
| --- | --- | --- | --- |
| 错误码域 | `11000-11999`（域序 11） | `max(已记录域序)=10`（product-review-v1）`+1`=11 | RESERVED（待独立 Registry 变更进入 `develop`） |
| migration version | `20261001000010` | `max(已记录 version)=20261001000009`（reviews）`+1` | RESERVED（待独立 Registry 变更进入 `develop`） |

待写入 Registry 的 RESERVED 行（由独立 Registry 变更进入 `develop`，非本 Feature Branch 私留）：

- `error-codes.md`：`| 11000-11999 | product-favorite-v1 | RESERVED | product-favorite-v1 收藏域（域序 11 = max(10)+1） |`
- `migrations.md`：`| 20261001000010 | favorites | product-favorite-v1 | RESERVED | favorites 收藏表 |`

## Open Risks

- 软引用悬空：若未来商品支持物理删除，`favorites` 保留悬空 `product_id`，查询 `LEFT JOIN` 必须容忍 NULL（标识 `product_deleted`），不得因联查失败报 500。
- 收藏数量上限未设：V1 不设上限，极端用户可无限收藏；量大时列表分页已覆盖，是否加上限由后续任务决定。
- `check` 接口为按次鉴权查询，商品详情页需每次发请求获取收藏态；若未来要求详情页内联收藏态，需改商品详情为 auth 感知（当前 Out of Scope）。

## Owner Decision Record

Owner 于 2026-10-05 批准（ACCEPT），适用范围为 product-favorite-v1 全部 Goal/AC：

1. **重复收藏**：幂等成功（no-op），`uk_user_product` 唯一约束兜底并发，不产生第二条记录。
2. **取消收藏**：按 `product_id` 操作、幂等成功；未收藏时 no-op（不报错）。
3. **可收藏校验**：仅 `on_shelf` 商品允许新增收藏；不存在 4001（404）、非在售 11001（409）。
4. **下架/删除后已有收藏**：软引用保留、不自动清理；列表标识 `unavailable`。
5. **是否已收藏**：V1 使用独立 `GET /favorites/check`，不修改 Product Detail。

批准当前 Contract 推荐方案；进入实现前按 Workflow 先完成 Registry Reservation（错误码域 `11000-11999`、migration `20261001000010`）。Design Impact = `NEW`，目标长期设计 `docs/design/favorite.md`（已随 APPROVED 产出）。
