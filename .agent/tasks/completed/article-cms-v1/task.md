# Task: 文章功能（用户侧 CMS）V1

## Goal

交付商城内的「用户文章」核心闭环：登录用户可发布、修改、删除自己的文章，任何人可公开浏览文章列表与详情，登录用户可对文章点赞/取消点赞、收藏/取消收藏，并获知自己对文章是否已点赞/收藏。作者身份一律来自登录上下文 `Principal.UserID`（不可伪造）；非作者不能修改、删除他人文章；「我的文章」只返回当前登录用户自己的文章；公开读、写需登录。点赞/收藏复用既有商品点赞/收藏的机制（幂等 + 唯一约束 + 公开计数 + 独立鉴权状态查询），不得破坏既有商品点赞/收藏行为。

## Scope

- 文章数据模型（`articles` 表或等价结构，具体 schema 由 Analyst 固化），经既有 golang-migrate 机制新增迁移文件，并同步更新 `internal/migrations/migrations_test.go`（`latestMigrationVersion` / `businessTables` / `expectedSchema`）。
- 文章写操作（登录用户，作用于 `Principal.UserID`）：发布、修改、删除自己的文章；非作者修改/删除返回稳定拒绝（404 防枚举）且无写入。
- 文章查询：公开列表（分页，无需登录）、公开详情（无需登录）、我的文章列表（仅当前登录用户本人）。
- 文章点赞/取消点赞（登录用户，幂等），公开点赞数，本人是否已点赞查询；不破坏既有商品点赞/收藏。
- 文章收藏/取消收藏（登录用户，幂等），本人收藏列表/是否已收藏查询；不破坏既有商品收藏。
- 删除文章时对点赞/收藏等关联数据的正确清理（不残留孤儿数据）。
- 文章详情返回点赞/收藏状态：按 Owner 确认语义实现（复用现有独立鉴权查询接口风格，或详情内联，由 Analyst/Owner 固化）。
- 错误码：新增文章错误码域（语义：文章），具体编号由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。
- 长期设计：新增 `docs/design/article.md`（Design Impact = NEW），沉淀文章数据模型、复用机制、越权语义、关联清理语义与错误码域。
- 必要测试：发布/修改/删除（含非作者越权）、公开列表/详情、我的文章、点赞/取消（含重复、隔离）、收藏/取消（含重复、隔离）、删除文章清理关联数据、非法输入、并发重复只落一条、迁移与设计一致性。

## Out of Scope

- 评论（文章评论）。
- 关注/粉丝体系。
- 标签、分类体系、复杂内容分类。
- 推荐、热榜、排序算法。
- 全文搜索。
- 审核工作流、草稿/定时发布。
- 独立后台 CMS（后台管理文章、下架等）；不新增 `article:*` 权限 code、不涉及 `AdminAuth`/`RequirePermission`、不改 `seed.go`。
- 修改既有商品点赞/收藏模块行为（`product_likes`、`favorites` 及其 API/错误语义保持不变）。
- 前端页面改造（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。

## Milestone

Milestone: 文章功能（用户侧 CMS）V1 核心闭环

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/article.md

## Acceptance Criteria

- [ ] AC-001（发布文章）：给定登录用户，当发布文章时，系统成功创建文章且作者身份取自 `Principal.UserID`，不信任客户端提交的任何身份字段。
- [ ] AC-002（修改文章）：文章作者可修改自己的文章；非作者修改他人文章返回稳定拒绝（404 防枚举）且不产生写入。
- [ ] AC-003（删除文章）：文章作者可删除自己的文章，且关联的点赞/收藏数据被正确清理；非作者删除他人文章返回稳定拒绝（404 防枚举）且不产生写入。
- [ ] AC-004（公开列表）：无需登录即可分页获取公开文章列表，排序/分页结果正确。
- [ ] AC-005（公开详情）：无需登录即可查看单篇文章详情，内容正确。
- [ ] AC-006（我的文章）：登录用户可查询自己的文章列表，仅返回当前登录用户本人的文章，不泄露他人文章。
- [ ] AC-007（点赞/取消点赞）：登录用户可对文章点赞/取消点赞，重复点赞幂等且不产生重复条目（DB 唯一约束兜底并发）；公开点赞数正确反映事实；不破坏既有商品点赞。
- [ ] AC-008（收藏/取消收藏）：登录用户可对文章收藏/取消收藏，重复收藏幂等且不产生重复条目；可查询本人是否已收藏/本人收藏列表；不破坏既有商品收藏。
- [ ] AC-009（详情点赞/收藏状态）：文章详情返回点赞/收藏状态的方式符合 Owner 确认语义（独立鉴权查询接口或详情内联），结果正确。
- [ ] AC-010（关联数据清理）：删除文章后，该文章的点赞/收藏关联数据不再残留，公开计数/列表正确回落。
- [ ] AC-011（鉴权边界）：所有写操作需有效用户凭证（无/非法 token 返回 401 且无写入）；公开读不要求登录；既有商品点赞/收藏行为不受本任务影响。
- [ ] AC-012（数据模型与迁移）：文章相关表经迁移正确建立，`migrations_test.go` 的版本与表/字段快照同步更新，迁移可重复/幂等。
- [ ] AC-013（长期设计）：新增 `docs/design/article.md`，沉淀文章数据模型、复用机制、越权语义、关联清理语义与错误码域，与 APPROVED Contract 及最终实现一致。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无独立 `dao`/`model` 层。文章模块应沿用此结构。
- 身份域：前台用户经 `middleware.Auth` 注入 `Principal{UserID, Sid}`；受保护路由挂 `internal/cmd/routes_frontend.go` 的 `Auth` 分组。文章写操作、点赞/收藏、我的文章均为纯用户域，身份唯一来源是 `Principal.UserID`。
- 既有点赞/收藏是**商品专用**：`product_likes`（`user_id` + `product_id`，`uk_user_product` 唯一约束，软引用无 FK）、`favorites`（`user_id` + `product_id`，`uk_user_product`）。二者字段为 `product_id`，**不是通用 `target_type`/`target_id` 结构**，不能直接对文章复用同一张表（除非改造表结构，可能破坏商品点赞/收藏）。`docs/design/like.md` / `favorite.md` 明确「点赞与收藏为独立交互、独立表、独立错误码域」。
- 作者越权控制既有模式：`internal/logic/review/review.go` 的修改/删除用条件更新/删除 `WHERE id AND user_id` + 核对 `RowsAffected`，=0 时返回 404 防枚举（`CodeReviewNotFound`）。文章「非作者不能修改/删除」应沿用此模式。
- 既有「是否已点赞/收藏」用独立鉴权接口 `GET /likes/check`、`GET /favorites/check`，**不改造商品详情**（`GET /products/:id` 保持公开无 token）。「公开点赞数」用独立公开接口 `GET /likes/count`（无需 token）。
- `users` 表仅 `id`/`username`/`password_hash`/时间戳，无昵称/头像字段；若文章需展示作者信息，仅可用 `username`（软引用 `users.id`）。
- 错误码集中在 `internal/codes/codes.go`，按 1000 对齐的域分配；Registry（`.agent/registry/error-codes.md`）当前已分配至域序 14（banner，`RESERVED`）。文章需新增一个错误码域。
- 迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`；Registry（`.agent/registry/migrations.md`）当前最新为 `20261001000015`（flash-sale-v3，`RESERVED`）。文章需新增 migration（文章表 + 文章点赞表 + 文章收藏表，具体表拆分由 Analyst 固化），并同步更新 `internal/migrations/migrations_test.go`。
- 后端当前无任何文章/CMS 模块（代码中 `post` 命中均为 HTTP `method:"post"`）；`frotend_web`/`frotend_manage` 为模板工程，未接入本后端。
- 事实来源为单一 MySQL；Redis 仅会话；无 MQ。文章为同步写、无异步。

Assumption（合理但未经 Owner 确认，交 Analyst 核实并向 Owner 确认）：

- 「复用现有点赞/收藏机制」= 复用既有实现模式（幂等 + 唯一约束 + 公开计数 + 独立鉴权状态查询），而非复用 `product_likes`/`favorites` 同一张表；文章应新建独立的点赞/收藏表，以保证「不破坏商品点赞/收藏」。
- 文章为纯用户域，无管理员参与、不新增权限 code、不改 `seed.go`、不涉及 `AdminAuth`/`RequirePermission`（与商品点赞/收藏/评价的用户侧一致）。
- 文章作者信息如需展示，使用 `users.username`（当前无昵称/头像）。
- 文章删除为物理删除 + 关联点赞/收藏物理清理（对应 Owner「删除文章时处理点赞/收藏等关联数据」）；是否软删由 Analyst 与 Owner 确认。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 文章详情返回点赞/收藏状态的承载方式：Owner 表述为「如现有项目风格支持」；现有风格 = 独立 `GET /likes/check`、`GET /favorites/check` 接口、详情保持公开无 token。若要求详情内联点赞/收藏状态，需详情变为 auth 感知（与「公开读」冲突），属协议选择，交 Analyst 定案。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试必须走真实 `RegisterFrontendRoutes` + `middleware.Auth`，用真实用户账号断言。

- AC-001 → 需 MySQL + Redis：登录用户发布文章，断言落库且 `author_id` 取自 `Principal.UserID`；伪造 `author_id` 字段被忽略。
- AC-002 → 需 MySQL + Redis：作者修改成功；另一用户修改返回 404 且文章内容不变。
- AC-003 → 需 MySQL + Redis：作者删除成功且关联点赞/收藏被清理；非作者删除返回 404 且无任何删除。
- AC-004 → 需 MySQL：无需 token 分页拉取列表，断言排序/分页正确。
- AC-005 → 需 MySQL：无需 token 查看详情，断言内容正确。
- AC-006 → 需 MySQL + Redis：用户 A 查「我的文章」仅含 A 的文章，不含 B 的文章。
- AC-007 → 需 MySQL（+ `-race`）：点赞/取消/重复点赞幂等，公开点赞数正确，唯一约束兜底并发；商品点赞行为不受影响。
- AC-008 → 需 MySQL（+ `-race`）：收藏/取消/重复收藏幂等，本人收藏列表/是否已收藏正确；商品收藏行为不受影响。
- AC-009 → 需 MySQL + Redis：按 Owner 确认的承载方式断言详情点赞/收藏状态正确。
- AC-010 → 需 MySQL：删除文章后断言 article_likes/article_favorites 无残留、公开计数回落。
- AC-011 → 需 MySQL + Redis：无/非法 token 访问写接口断言 401 且无写入；公开读接口无需 token 可访问。
- AC-012 → 需 MySQL：执行迁移断言表/字段结构正确、`migrations_test.go` 通过、迁移可重复执行。
- AC-013 → 文档审查：`docs/design/article.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及新数据模型（文章表与作者实体关系、文章点赞/收藏表）、新公开协议（文章 API + 错误码域）、跨模块一致性与事务边界（删除文章需清理点赞/收藏关联数据）、以及「复用既有点赞/收藏机制 vs 改造既有商品点赞/收藏表」这一会影响既有模块的架构选择；同时「详情返回点赞/收藏状态」的承载方式（独立鉴权接口 vs 详情内联）与「软删 vs 硬删」会产生不同用户可见行为与数据模型。多个现实方案会产生不同业务与可靠性结果，需 Analyst 固化 Contract 后由 Owner 确认。

## Analyst Questions

1. 点赞/收藏复用方式：新建文章独立表 `article_likes`/`article_favorites`（沿用既有幂等 + 唯一约束 + 公开计数 + 独立鉴权查询模式，不碰 `product_likes`/`favorites`）vs 泛化既有商品表为 `target_type`/`target_id`（需迁移存量、改写既有逻辑、存在破坏商品点赞/收藏的风险）；约束为「不破坏商品点赞/收藏」。
2. 文章数据模型：`articles` 表 schema（字段、类型、索引）；`author_id` 引用 `users.id` 的方式（软引用 vs FK）；时间戳字段；软删 vs 硬删。
3. 删除文章的关联清理：删除文章时如何原子清理文章点赞/收藏（事务边界 `articles` + `article_likes` + `article_favorites`）；硬删 + 事务清理 vs 软删 + 隐藏，以及失败时的处理。
4. 详情点赞/收藏状态的承载方式：独立鉴权查询接口（如 `GET /articles/:id/liked`、`GET /articles/:id/favorited`，详情保持公开无 token）vs 详情内联（需详情 auth 感知，与「公开读」冲突）。
5. 公开列表/详情字段：是否含作者信息（`users.username`）、点赞数/收藏数是否公开、分页与排序规则（如按创建时间倒序）。
6. 输入校验：标题/内容的长度上限、必填与 trim 规则。
7. 错误语义：文章不存在/非作者越权（404 防枚举）、参数非法（400）、未登录（401）、重复操作幂等等稳定错误码与 HTTP 状态。
8. 全局资源：新增文章错误码域（语义：文章）、新增 migration（文章表 + 文章点赞表 + 文章收藏表，表数量由 Analyst 固化）；具体域号/version 由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。

## Review Baseline

- Base commit：`91a67d5b89abc5d250a49a2074881b9ba3f621e5`（分支 `feature/cms`，local == `origin/feature/cms`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/article-cms-v1/`、`api/article*/`（或等价文章 API 包，含文章/文章点赞/文章收藏契约）、`internal/controller/article*/`、`internal/logic/article*/`、`internal/service` 的文章相关接口、`internal/codes` 文章域扩展、migration 文件（文章相关表）、`internal/cmd/routes_frontend.go` 路由扩展及对应测试；`docs/design/article.md` 由 Analyst 写入。本任务不修改 `product_likes`/`favorites` 及其既有 API/错误语义。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）
