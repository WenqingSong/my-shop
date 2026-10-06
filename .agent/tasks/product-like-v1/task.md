# Task: 商品点赞（Product Like）V1

## Goal

交付用户侧「商品点赞」核心闭环：登录用户可对商品（SPU）点赞、取消点赞，并获知自己对某商品是否已点赞；点赞归属当前登录用户，重复点赞不产生重复条目；点赞的对外可见性（公开点赞数 和/或 本人点赞状态）与数据模型由 Analyst 固化、Owner 确认。点赞是区别于「收藏」的独立用户交互，不改造收藏/评价/商品既有行为。

## Scope

- 点赞数据模型（per-user 点赞表或等价结构，具体 schema 由 Analyst 固化），经既有 golang-migrate 机制新增迁移文件，并同步更新 `internal/migrations/migrations_test.go`（`latestMigrationVersion`/`businessTables`/`expectedSchema`）。
- 点赞写操作（登录用户，作用于 `Principal.UserID`）：点赞、取消点赞（toggle 语义由 Analyst/Owner 固化）。
- 点赞查询：当前用户对指定商品是否已点赞的查询；点赞的对外可见性（公开点赞数 和/或 本人点赞状态）按 Owner 确认语义实现。
- 重复点赞防护：同一用户对同一商品至多一条点赞，DB 唯一约束兜底并发。
- 用户隔离：所有读写在 `Principal.UserID` 维度过滤，用户只能操作自己的点赞。
- 商品存在性/可点赞校验：点赞目标商品需满足 Owner 确认的校验条件（存在、且在售与否，由 Analyst/Owner 固化）。
- 错误码：新增点赞错误码域（语义：商品点赞），具体编号由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。
- 长期设计：新增 `docs/design/like.md`（Design Impact = NEW），沉淀点赞数据模型、可见性/计数语义、用户隔离边界与错误码域。
- 必要测试：点赞（正常/重复/非法商品）、取消点赞、是否已点赞、用户隔离、非法输入、并发重复点赞只落一条、可见性/计数正确性。

## Out of Scope

- 评价（review）点赞：`review.md` 已 Deferred「点赞/回复/追评/举报」，本任务只做「商品点赞」，不做评价点赞。
- 收藏（favorite）：点赞与收藏是两个独立概念，本任务不修改收藏模块行为、不复用 `favorites` 表。
- SKU 粒度点赞（本任务按商品 SPU 粒度，与收藏/评价/浏览量一致；如需 SKU 粒度另立任务）。
- 点赞分组、标签、备注、排序/推荐算法、点赞排行榜。
- 匿名/游客点赞（如「未登录也可点赞并计数」）；本任务默认点赞需登录（per-user 状态），是否支持匿名计数由 Owner 确认后再定。
- 后台管理点赞（管理员查看/清理用户点赞），不新增 `like:*` 权限 code、不涉及 `AdminAuth`/`RequirePermission`、不改 `seed.go`。
- 商品删除/下架后点赞的自动清理策略（保留为软引用并由查询标识不可用，具体语义由 Analyst/Owner 固化）。
- 前端页面改造（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。
- 修改既有商品/用户/收藏/评价/IAM 模块行为（除点赞引用所需的最小只读查询）。

## Milestone

Milestone: 商品点赞（Product Like）V1 核心闭环

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/like.md

注：本判定基于「点赞为 per-user 新实体（独立点赞表）+ 新公开协议（点赞 API + 错误码域）」的最小实现。若 Analyst 分析结论为「点赞仅 = 商品计数字段（无 per-user 状态）」，则校正为 UPDATE（`docs/design/product.md`）；校正改变 Scope 时回 Owner/Task Builder。

## Acceptance Criteria

- [ ] AC-001（点赞）：给定登录用户与一个符合点赞条件的商品，当用户点赞时，系统正确记录该用户对该商品的点赞，点赞状态/计数发生正确变化。
- [ ] AC-002（取消点赞）：给定已点赞的登录用户，当用户取消点赞时，系统正确移除点赞记录，点赞状态/计数正确回退。
- [ ] AC-003（重复点赞）：同一用户对同一商品重复点赞时行为符合 Owner 确认语义（幂等成功或拒绝），且不产生重复条目（DB 唯一约束兜底并发）。
- [ ] AC-004（点赞可见性）：点赞状态/计数按 Owner 确认的语义对外可见（公开点赞数 和/或 本人点赞状态），结果正确反映当前点赞事实。
- [ ] AC-005（是否已点赞）：登录用户可查询自己对指定商品是否已点赞，结果正确反映当前点赞状态。
- [ ] AC-006（用户隔离）：用户 A 无法查看/操作用户 B 的点赞；跨用户操作返回稳定拒绝（404 防枚举）且无写入。
- [ ] AC-007（商品校验）：对不存在或不满足可点赞条件的商品点赞被拒绝，且不产生任何写入。
- [ ] AC-008（必须登录）：点赞接口均需有效用户凭证，无 token/非法 token 返回 401 且不返回任何点赞数据。
- [ ] AC-009（数据模型与迁移）：点赞表/字段经迁移正确建立，`migrations_test.go` 的版本与表/字段快照同步更新，迁移可重复/幂等。
- [ ] AC-010（长期设计）：新增 `docs/design/like.md`，沉淀点赞数据模型、可见性/计数语义、用户隔离边界、错误码域。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。点赞模块应沿用此结构。
- 身份域：前台用户经 `middleware.Auth` 注入 `Principal{UserID, Sid}`；前台受保护路由挂 `internal/cmd/routes_frontend.go` 的 `Auth` 分组（当前已含 `/favorites`、`/reviews` 等用户侧接口）。点赞为纯用户域，不涉及 `AdminAuth`/`RequirePermission`。
- 商品：`products`（`status` TINYINT 0=draft/1=on_shelf/2=off_shelf）；前台公开详情 `GET /products/:id` 仅返回 `on_shelf`。点赞目标为商品 SPU（`products.id`）。
- 类似模块参考：`favorite`（`favorites`，用户侧、`user_id` 维度隔离、软引用无 FK、`uk_user_product` 唯一约束兜底重复）、`review`（`reviews`）、`cart`（`cart_items`）。点赞在「per-user + 唯一约束 + 软引用无 FK」上与收藏同构，可复用其实现模式；但点赞的「公开计数」语义（如确认）与收藏不同——收藏已明确「收藏数量统计公开展示 Out of Scope」。
- 「点赞」与「收藏」为两个独立概念：`product-favorite-v1` 已完成收藏；`review.md` Deferred 中的「点赞」指「评价点赞」，与本任务的「商品点赞」不同。
- 错误码集中在 `internal/codes/codes.go`，已分配至域序 12（flash-sale，`RESERVED`）。点赞需新增一个错误码域（域序 13 = 12000 之后），具体域号由 Analyst 读 `.agent/registry/error-codes.md` 派生。
- 迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，当前最新为 `20261001000012_product_view_count`（`RESERVED`）。点赞需新增 1 个 migration，具体 version 由 Analyst 读 `.agent/registry/migrations.md` 派生；需同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion`/`businessTables`/`expectedSchema`。
- 事实来源为单一 MySQL；Redis 仅会话；无 MQ。点赞为同步写、无异步。
- 后端当前无任何点赞表/点赞模块（代码中 `Like` 命中均为 SQL `LIKE` 关键字搜索，非点赞功能）；`frotend_web`/`frotend_manage` 为模板工程，未接入本后端。

Assumption（合理但未经 Owner 确认，交 Analyst 核实并向 Owner 确认）：

- 「点赞」是区别于「收藏」的独立用户交互；点赞粒度与收藏一致为商品 SPU（`products.id`）。
- 点赞需登录（per-user 状态），匿名/游客点赞为 Out of Scope。
- 点赞为纯用户域，无管理员参与、不新增权限 code、不改 `seed.go`。
- 点赞需校验目标商品存在；是否要求 `on_shelf`（即是否允许点赞已下架/草稿商品）待定。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 点赞可见性：点赞数是否公开（商品详情/列表展示「X 人点赞」）还是仅本人可见「是否点赞」；这是点赞与收藏最核心的语义差异点。
- 数据模型：per-user 点赞表（`product_likes`）+ 实时 `COUNT` 聚合 vs 冗余计数列（`products.like_count` 原子自增）vs 两者结合；各自的并发正确性与写放大代价。
- 点赞粒度：商品（SPU）还是 SKU。
- 可点赞校验：是否仅允许点赞 `on_shelf` 商品；商品被下架/删除后已有点赞如何处理（软引用保留并标识不可用，还是自动清理）。
- 重复点赞/取消语义：幂等成功（no-op）还是拒绝；取消不存在的点赞是 404 还是幂等成功。
- 是否支持匿名/游客点赞（未登录计数）。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试必须走真实 `RegisterFrontendRoutes` + `middleware.Auth`，用真实用户账号断言。

- AC-001 → 需 MySQL + Redis：登录用户点赞一个 on_shelf 商品，断言点赞落库且归属该用户、状态/计数正确变化。
- AC-002 → 需 MySQL + Redis：取消点赞后断言点赞记录移除、状态/计数正确回退。
- AC-003 → 需 MySQL（+ `-race`）：同一商品并发/重复点赞，断言至多一条（唯一约束兜底），且行为符合 Owner 确认语义。
- AC-004 → 需 MySQL：按 Owner 确认的可见性位置断言点赞数/点赞状态正确返回。
- AC-005 → 需 MySQL + Redis：点赞后查「是否已点赞」返回 true，取消后返回 false。
- AC-006 → 需 MySQL + Redis：用户 B 查看/操作用户 A 的点赞，断言稳定拒绝（404 防枚举）且无写入。
- AC-007 → 需 MySQL：点赞不存在/不满足可点赞条件的商品，断言稳定拒绝且无写入。
- AC-008 → 需 MySQL + Redis：无 token/非法 token 访问点赞接口，断言 401 且无数据返回。
- AC-009 → 需 MySQL：执行迁移断言点赞表/字段结构正确、`migrations_test.go` 通过、迁移可重复执行。
- AC-010 → 文档审查：`docs/design/like.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及新数据模型（点赞表与用户/商品的实体关系）、新公开协议（点赞 API + 错误码域）、点赞可见性/计数语义（公开计数 vs 本人状态，denormalized counter vs 实时 COUNT 聚合）带来的并发与写放大权衡、重复点赞的并发去重（唯一约束），以及「点赞 vs 收藏」的语义边界与可点赞校验条件等关键业务规则；多个现实方案会产生不同数据模型与用户可见行为，需 Analyst 固化 Contract 后由 Owner 确认。

## Analyst Questions

1. 点赞可见性语义：点赞数是否公开（商品详情/列表展示「X 人点赞」）还是仅本人可见；是否新增独立公开统计接口。
2. 数据模型与一致性：per-user 点赞表（`product_likes`）schema；`user_id`/`product_id` 的引用方式（软引用 vs FK）；联合唯一约束列组合；公开计数用实时 `COUNT` 聚合 vs 冗余计数列（`products.like_count` 原子自增）vs 两者结合，及其并发正确性与写放大代价。
3. 可点赞校验路径：点赞时如何校验商品（存在、`on_shelf` 与否）；商品被下架/删除后已有点赞的语义（软引用保留并标识不可用 vs 自动清理）。
4. 重复点赞与取消的并发语义：`uk_user_product` 唯一约束兜底；重复点赞返回幂等成功还是拒绝；取消不存在点赞的返回语义。
5. 「是否已点赞」的提供方式：独立鉴权查询接口（如 `GET /likes/check`）还是改造商品详情为 auth 感知；点赞列表是否需要分页与排序字段。
6. 错误语义：点赞/取消/是否已点赞的稳定错误码与 HTTP 状态；越权/不存在/重复/非法输入的语义。
7. 全局资源：新增点赞错误码域（语义：商品点赞）、新增 1 个 migration（点赞表）；具体域号/version 由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。

## Review Baseline

- Base commit：`727cf978976d63010ed8bb67a991109e251bc57f`（分支 `feature/like`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/product-like-v1/`、`api/like*/`（或等价点赞 API 包）、`internal/controller/like*/`、`internal/logic/like*/`、`internal/service` 的 `ILike` 接口、`internal/codes` 点赞域扩展、migration 文件（点赞表）、`internal/cmd/routes_frontend.go` 路由扩展及对应测试；`docs/design/like.md` 由 Analyst 写入。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）
