# Task: 商品收藏（Product Favorite）V1

## Goal

交付用户侧「商品收藏」核心闭环：登录用户可收藏商品（SPU）、取消收藏、查看自己的收藏列表，并能获知自己对某商品是否已收藏；收藏仅归属当前登录用户，重复收藏不产生重复条目，任何越权操作均不产生数据库写入。

## Scope

- 收藏数据模型（`favorites` 表），经既有 golang-migrate 机制新增迁移文件（schema 由 Analyst 固化），并同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion` 与 `businessTables`。
- 收藏写操作（登录用户，作用于 `Principal.UserID`）：添加收藏、取消收藏。
- 收藏查询：当前用户的收藏列表（分页）；当前用户对指定商品是否已收藏的查询（供商品详情展示）。
- 重复收藏防护：同一用户对同一商品至多一条收藏，DB 唯一约束兜底并发。
- 用户隔离：所有读写在 `Principal.UserID` 维度过滤，用户只能操作自己的收藏。
- 商品存在性/可收藏校验：收藏的目标商品需满足 Owner 确认的校验条件（存在、且在售与否，具体由 Analyst/Owner 固化）。
- 错误码：新增收藏错误码域（语义：商品收藏），具体编号由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。
- 长期设计：新增 `docs/design/favorite.md`（Design Impact = NEW），沉淀收藏数据模型、商品校验语义、用户隔离边界与错误码域。
- 必要测试：添加收藏（正常/重复/非法商品）、取消收藏、列表、是否已收藏、用户隔离、非法输入、并发重复收藏只落一条。

## Out of Scope

- SKU 粒度收藏（本任务按商品 SPU 粒度；如需 SKU 粒度另立任务）。
- 收藏分组、标签、备注、排序/推荐算法。
- 收藏数量统计（如「商品被收藏次数」）的公开展示。
- 后台管理收藏（管理员查看/清理用户收藏），不新增 `favorite:*` 权限 code、不涉及 `AdminAuth`/`RequirePermission`。
- 商品删除/下架后收藏的自动清理策略（保留为软引用并由查询标识不可用，具体语义由 Analyst/Owner 固化）。
- 前端页面改造（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。
- 修改既有商品/用户/IAM 模块行为（除收藏引用所需的最小只读查询）。

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/favorite.md

## Acceptance Criteria

- [ ] AC-001（添加收藏·登录）：登录用户可收藏一个符合可收藏条件的商品，成功后该收藏归属该用户且在其收藏列表中可见。
- [ ] AC-002（重复收藏）：同一用户对同一商品重复收藏时的行为符合 Owner 确认的语义（幂等成功或拒绝），且不产生重复条目（DB 唯一约束兜底并发）。
- [ ] AC-003（取消收藏）：登录用户可取消收藏，成功后该商品从该用户收藏列表中移除。
- [ ] AC-004（收藏列表）：登录用户可查看自己的收藏列表（分页），返回项含收藏的商品信息与收藏时间。
- [ ] AC-005（是否已收藏）：登录用户可查询自己对指定商品是否已收藏，结果正确反映当前收藏状态（供商品详情展示）。
- [ ] AC-006（用户隔离）：用户 A 无法查看/取消用户 B 的收藏；跨用户操作返回稳定拒绝（404 防枚举）且无写入。
- [ ] AC-007（商品校验）：收藏不存在或不满足可收藏条件的商品被拒绝，且不产生任何写入。
- [ ] AC-008（必须登录）：所有收藏接口均需有效用户凭证，无 token/非法 token 返回 401 且不返回任何收藏数据。
- [ ] AC-009（数据模型与迁移）：收藏表经迁移正确建表，`migrations_test.go` 的版本与表清单同步更新，迁移可重复/幂等。
- [ ] AC-010（长期设计）：新增 `docs/design/favorite.md`，沉淀收藏数据模型、商品校验语义、用户隔离边界、错误码域。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。收藏模块应沿用此结构。
- 身份域：前台用户经 `middleware.Auth` 注入 `Principal{UserID, Sid}`；前台受保护路由挂 `internal/cmd/routes_frontend.go` 的 `Auth` 分组。收藏为纯用户域，不涉及 `AdminAuth`/`RequirePermission`。
- 商品：`products`（`status` TINYINT 0=draft/1=on_shelf/2=off_shelf）；前台公开详情 `GET /products/:id` 仅返回 `on_shelf`。收藏目标为商品 SPU（`products.id`）。
- 类似模块参考：`cart`（`cart_items`，用户侧、`user_id` 维度隔离、软引用无 FK）、`review`（`reviews`，用户侧、唯一约束兜底重复、软引用无 FK）。收藏在数据模型与隔离语义上与其同构，可复用其实现模式。
- 错误码集中在 `internal/codes/codes.go`，已分配至商品评价域（域序 10，`RESERVED`）。收藏需新增一个错误码域，具体域号由 Analyst 读 `.agent/registry/error-codes.md` 派生。
- 迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，当前最新为 `20261001000009_reviews`（`RESERVED`）。收藏表需新增 1 个 migration，具体 version 由 Analyst 读 `.agent/registry/migrations.md` 派生；需同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion` 与 `businessTables`。
- 事实来源为单一 MySQL；Redis 仅会话；无 MQ。收藏为同步写、无异步。
- 后端当前无任何收藏表/收藏模块；`frotend_web`/`frotend_manage` 为模板工程，未接入本后端。

Assumption（合理但未经 Owner 确认，交 Analyst 核实并向 Owner 确认）：

- 收藏粒度为商品 SPU（`products.id`），非 SKU；「收藏」即对整件商品的收藏。
- 收藏为纯用户域，无管理员参与、不新增权限 code、不改 `seed.go`。
- 收藏需校验目标商品存在；是否要求 `on_shelf`（即是否允许收藏已下架/草稿商品）待定。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 收藏粒度：商品（SPU）还是 SKU。
- 可收藏校验：是否仅允许收藏 `on_shelf` 商品；商品被下架/删除后已有收藏如何处理（软引用保留并标识不可用，还是自动清理）。
- 重复收藏语义：幂等成功（no-op）还是拒绝；取消不存在的收藏是 404 还是幂等成功。
- 「是否已收藏」的提供方式：独立鉴权查询接口（如 `GET /favorites/check`）还是改造商品详情为 auth 感知；收藏列表是否需要分页与排序字段。
- 收藏数量上限（如有）。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试必须走真实 `RegisterFrontendRoutes` + `middleware.Auth`，用真实用户账号断言。

- AC-001 → 需 MySQL + Redis：登录用户收藏一个 on_shelf 商品，断言收藏落库且归属该用户、列表中可见。
- AC-002 → 需 MySQL（+ `-race`）：同一商品并发/重复收藏，断言至多一条（唯一约束兜底），且行为符合 Owner 确认的语义。
- AC-003 → 需 MySQL + Redis：取消收藏后断言该商品从该用户收藏列表移除、库中记录删除。
- AC-004 → 需 MySQL + Redis：登录用户查收藏列表，断言分页与收藏时间正确、只含本人收藏。
- AC-005 → 需 MySQL + Redis：收藏后查「是否已收藏」返回 true，取消后返回 false。
- AC-006 → 需 MySQL + Redis：用户 B 查看/取消用户 A 的收藏，断言稳定拒绝（404 防枚举）且无写入。
- AC-007 → 需 MySQL：收藏不存在/不满足可收藏条件的商品，断言稳定拒绝且无写入。
- AC-008 → 需 MySQL + Redis：无 token/非法 token 访问收藏接口，断言 401 且无数据返回。
- AC-009 → 需 MySQL：执行迁移断言收藏表结构正确、`migrations_test.go` 通过、迁移可重复执行。
- AC-010 → 文档审查：`docs/design/favorite.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及新数据模型（`favorites` 与用户/商品的实体关系）、新公开协议（收藏 API + 错误码域）、重复收藏的并发去重（唯一约束）、商品删除/下架后收藏的一致性语义，以及可收藏校验条件（存在性/在售）等关键业务规则；多个现实方案会产生不同数据模型与用户可见行为，需 Analyst 固化 Contract 后由 Owner 确认。

## Analyst Questions

1. 收藏数据模型：`favorites` schema；`user_id`/`product_id` 的引用方式（软引用 vs FK）；联合唯一约束列组合；是否存商品冗余快照字段（名称/图片），还是查询时实时联查 `products`。
2. 可收藏校验路径：收藏时如何校验商品（存在、`on_shelf` 与否）；商品被下架/删除后已有收藏的语义（软引用保留并标识不可用 vs 自动清理）。
3. 重复收藏与取消的并发语义：`uk_user_product` 唯一约束兜底；重复收藏返回幂等成功还是拒绝；取消不存在收藏的返回语义。
4. 「是否已收藏」的提供方式：独立鉴权查询接口 vs 商品详情 auth 感知；收藏列表分页/排序字段。
5. 错误语义：收藏/取消/列表/是否已收藏的稳定错误码与 HTTP 状态；越权/不存在/重复/非法输入的语义。
6. 全局资源：新增收藏错误码域（语义：商品收藏）、新增 1 个 migration（`favorites` 表）；具体域号/version 由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。

## Review Baseline

- Base commit：`fc585493bb60b43701815e45ab8fa4f15c2dc96c`（分支 `feature/product-favorite`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/product-favorite-v1/`、`api/favorite*/`（或等价收藏 API 包）、`internal/controller/favorite*/`、`internal/logic/favorite*/`、`internal/service` 的 `IFavorite` 接口、`internal/codes` 收藏域扩展、migration 文件（`favorites`）、`internal/cmd/routes_frontend.go` 路由扩展及对应测试；`docs/design/favorite.md` 由 Analyst 写入。当前工作区干净，无既有未提交修改。

## Initial Route

READY_FOR_ANALYST
