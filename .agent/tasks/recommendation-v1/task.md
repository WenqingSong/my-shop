# Task: 推荐位（Recommendation）V1

## Goal

交付后端「推荐位」核心能力：管理员可在后台创建、查看、修改、删除/禁用推荐位，并为推荐位配置推荐商品（添加、移除、调整排序，禁止同一商品重复加入同一推荐位）；前台通过公开接口 `GET /recommendations/:code` 按稳定顺序只获取「启用推荐位中的可售商品」，商品后续下架无需物理删除推荐关系、仅在前台查询时被过滤。后台管理走既有 `AdminAuth + RequirePermission` 双层保护（未认证 401、无权限 403、拒绝零写入）。

## Scope

- 推荐位与推荐商品数据模型（`recommend_positions` + `recommend_items` 两表），经既有 golang-migrate 机制新增迁移（schema 由 Analyst 固化），并同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion`、`businessTables` 与 `expectedSchema`。
- 推荐位后台管理接口（AdminAuth + RequirePermission，具体权限码由 Analyst 固化并在 `internal/boot/seed.go` 登记）：创建、查看、修改、删除/禁用推荐位。
- 推荐商品管理接口（AdminAuth + RequirePermission）：向推荐位添加商品、移除商品、调整排序；同一商品在同一推荐位内唯一（DB 唯一约束兜底 + 稳定错误码）。
- 添加商品时的有效性校验：校验商品存在，拒绝不存在商品（是否「仅允许 on_shelf 商品加入」见 Analyst Questions，由 Owner 确认）。
- 前台公开接口 `GET /recommendations/:code`：无 token，仅返回启用推荐位中的可售商品，按 `sort, id` 稳定排序。
- 下架/删除语义：商品下架后不物理删除推荐关系，前台查询过滤不可售商品。
- 错误码：新增推荐位错误码域（语义：推荐位 Recommend），具体编号由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。
- 长期设计：新增 `docs/design/recommendation.md`（Design Impact = NEW），沉淀推荐位/推荐商品数据模型、唯一性与排序语义、前台过滤规则、权限边界与错误码域。
- 必要测试：推荐位 CRUD、推荐商品增删排序、重复添加拒绝、商品不存在/下架、前台只返回启用位中的可售商品且稳定排序、权限隔离（401/403 拒绝零写入）、迁移幂等。

## Out of Scope

- 推荐算法、千人千面、个性化推荐、机器学习/排序模型、商品推荐策略（点击/销量/热度等自动推荐）。
- 推荐位的前端可视化编排、拖拽排序、预览等页面能力（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。
- 定时上下架推荐位/推荐商品的生效时间窗，除非 Analyst/Owner 明确需要（见 Analyst Questions）。
- 推荐位曝光/点击统计、效果分析、A/B 测试。
- 商品搜索、商品筛选、SKU 维度推荐、跨推荐位聚合推荐等超出「按 code 查询单个推荐位」的能力。
- 修改既有商品/分类/IAM/RBAC 等模块行为（除推荐商品有效性校验所需的最小只读查询）。

## Milestone

Milestone: 推荐位（Recommendation）V1 核心闭环（推荐位后台管理 + 推荐商品管理 + 前台推荐位查询）

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/recommendation.md

## Acceptance Criteria

- [ ] AC-001（数据模型与迁移）：`recommend_positions` 与 `recommend_items` 两表经迁移正确建表；同一推荐位下同一商品具有唯一约束，存在支撑前台查询的索引；`migrations_test.go` 的版本号与表清单同步更新；迁移可重复/幂等执行。
- [ ] AC-002（推荐位后台 CRUD）：管理员携带相应权限可创建推荐位并落库，可查看、修改、删除/禁用推荐位；未认证/无权限访问返回稳定拒绝（401/403）且不产生写入。
- [ ] AC-003（推荐商品管理）：管理员可向指定推荐位添加商品、移除商品、调整排序；操作结果可被后续查询与前台接口观察到。
- [ ] AC-004（重复添加拒绝）：同一商品重复加入同一推荐位被拒绝（稳定错误码），且不产生重复关系记录；即使并发提交也被唯一约束兜底。
- [ ] AC-005（商品有效性校验）：添加推荐商品时校验商品存在，商品不存在返回稳定错误且不产生写入（是否「仅允许 on_shelf 商品加入」以 Owner 确认为准，见 Analyst Questions）。
- [ ] AC-006（前台推荐位查询）：无 token 访问 `GET /recommendations/:code` 成功，仅返回启用推荐位中的可售商品，按 `sort` 升序、同值按 `id` 升序稳定排序；禁用推荐位不返回有效商品。
- [ ] AC-007（下架/删除语义）：推荐商品对应的商品下架后，推荐关系仍保留（不物理删除）；前台查询过滤掉不可售商品，后台查询仍能看到该关系。
- [ ] AC-008（权限与隔离）：新增 `recommend:*` RBAC 权限（具体 code 由 Analyst 固化并登记 seed）；未认证访问管理接口返回 401、无权限返回 403，且均不产生写入。
- [ ] AC-009（长期设计）：新增 `docs/design/recommendation.md`，沉淀推荐位/推荐商品数据模型、唯一性与排序语义、前台过滤规则、权限边界与错误码域，并与 APPROVED Contract、最终实现一致。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。推荐位模块应沿用此结构。
- 身份域：前台用户经 `middleware.Auth` 注入 `Principal{UserID, Sid}`；后台管理员经 `AdminAuth` 注入 `AdminPrincipal{AdminID, Sid, IsSuper}`，再由 `RequirePermission(code)` 授权（`IsSuper` 直接放行、权限查询 fail-closed、未命中 403）。后台写路由挂 `internal/cmd/routes_admin.go` 的 `require(code)` 分组，前台公开路由挂 `routes_frontend.go`。
- **后台 RBAC 已完整存在**（`internal/middleware/auth.go`、`internal/boot/seed.go`、`docs/design/rbac.md`）：含 `admins`/`admin_roles`/`role_permissions`/`permissions` 表，权限 code 在 `internal/boot/seed.go` 的 `seedPermissionList` 幂等登记（当前含 `banner:create/update/delete` 等）。因此本任务只需新增推荐位权限 code 并登记，无需新建 RBAC 体系。**注意**：`AGENTS.md` §11「权限/RBAC 仅有登录认证、没有管理员或角色体系」为过期描述，与当前代码不一致，以代码为准。
- **商品表已存在**：`products.status` 为 `0=draft / 1=on_shelf / 2=off_shelf`（见 `internal/migrations/sql/20261001000002_products.up.sql`），并有 `product:on_shelf`/`product:off_shelf` 写接口与 `CodeProductInvalidStatusTransition` 等状态语义。因此「校验商品存在」「on_shelf 过滤」均可行。**注意**：`AGENTS.md` §11「当前没有商品表」为过期描述，以代码为准。
- 最接近的既有模块是轮播图 `banner-v1`（单表 `banners`，`status`(1 启用/0 禁用) + `sort`，公开列表仅启用项按 `sort,id` 排序，后台 CRUD 走 `RequirePermission("banner:*")`），推荐位可参考其 API/Controller/Logic/路由/权限 seed 结构，但为「两表 + 商品关联」模型。
- 错误码集中在 `internal/codes/codes.go`；`.agent/registry/error-codes.md` 已分配至 `14000-14999`（banner-v1，RESERVED）。推荐位为下一空闲错误码域（具体域号由 Analyst 派生）。
- 迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`；`.agent/registry/migrations.md` 最新为 `20261001000014`（banners，RESERVED）。推荐位需新增 1 个 migration（含两张表，具体 version 由 Analyst 派生），并同步更新 `migrations_test.go`。
- 事实来源为单一 MySQL；Redis 仅会话；无 MQ。推荐位为同步读写，无异步、无跨系统一致性。
- 后端当前无任何推荐位相关表/模块/接口；`frotend_web`/`frotend_manage` 为 vue-element-admin 模板工程，未接入本后端。

Assumption（合理但未经 Owner 确认，交 Analyst 核实并向 Owner 确认）：

- 「推荐位」为数据库驱动（`recommend_positions` + `recommend_items`），后台可运营管理；推荐位有稳定业务标识 `code` 供前台 `GET /recommendations/:code` 定位（`code` 唯一、不可随意变更）。
- 「删除/禁用推荐位」两者均属本次范围（禁用为软下线、删除为硬删除），具体语义与是否级联删除推荐商品关系由 Analyst 固化。
- 前台查询返回单个推荐位的推荐商品列表（不涉及商品搜索/分页上限，除非 Owner 明确需要）。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 商品有效性边界：T05 建议「仅允许 on_shelf 商品加入」，T07 要求「商品以后下架不物理删除关系、前台过滤」——两者并存意味着「加入时需 on_shelf，加入后下架则保留关系并前台过滤」。是否严格限制「仅 on_shelf 可加入」，还是允许任意存在商品加入、前台只展示可售，需 Owner 决定。
- 推荐位删除语义：物理删除 vs 禁用（status 软下线）；删除推荐位时推荐商品关系如何处置（级联删除 vs 保留孤儿）。
- 前台「有效商品」定义：是否仅 `products.status=on_shelf`，是否还需校验分类启用、SKU 可售等。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试必须走真实 `RegisterFrontendRoutes`（前台查询）与 `RegisterAdminRoutes` + `AdminAuth/RequirePermission`（后台管理），用真实管理员账号断言。

- AC-001 → 需 MySQL：执行迁移断言 `recommend_positions`/`recommend_items` 表结构正确、唯一约束与索引生效、`migrations_test.go` 通过、迁移可重复执行。
- AC-002 → 需 MySQL：管理员带权限创建/查看/修改/删除或禁用推荐位断言落库与生效；未认证/无权限断言 401/403 且无写入。
- AC-003 → 需 MySQL：添加/移除/调整排序后断言后台与前台查询反映新值。
- AC-004 → 需 MySQL：重复添加被拒（稳定错误码）且关系不重复；并发提交由唯一约束兜底。
- AC-005 → 需 MySQL：添加不存在商品断言拒绝且无写入；on_shelf 限制按 Owner 确认结果断言。
- AC-006 → 需 MySQL：预置启用/禁用推荐位与可售/不可售商品后，无 token 请求 `GET /recommendations/:code`，断言仅含启用位中的可售商品、按 `sort,id` 稳定排序。
- AC-007 → 需 MySQL：商品下架后断言推荐关系仍保留、前台不再返回、后台仍可见。
- AC-008 → 需 MySQL：`recommend:*` 权限已 seed；未认证 401、无权限 403、均不产生写入。
- AC-009 → 文档审查：`docs/design/recommendation.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及新数据模型（`recommend_positions` + `recommend_items` 两表及实体关系、唯一约束）、新公开协议（前台查询 API + 后台管理 API + 新错误码域）、以及商品有效性/下架过滤的跨模块语义。推荐位标识建模、删除 vs 禁用语义、商品可售性边界（加入时校验 vs 前台过滤）、权限粒度等均存在多个会产生不同业务结果与运维形态的现实方案，需 Analyst 固化 Contract 后由 Owner 确认。

## Analyst Questions

1. 数据模型：`recommend_positions`/`recommend_items` schema；推荐位用稳定 `code` 还是仅自增 `id` 定位；`code` 唯一性、格式与是否可改；`recommend_items` 的唯一约束（同一推荐位 + 同一商品）。
2. 推荐位删除语义：物理删除 vs 禁用（status 软下线），两者是否都支持；删除推荐位时推荐商品关系的处置（级联删除/保留）。
3. 商品有效性边界：加入推荐位时是否「仅允许 on_shelf 商品」，还是允许任意存在商品、前台只展示可售；下架商品保留关系但前台过滤的实现方式。
4. 前台查询语义：`GET /recommendations/:code` 返回结构（推荐位元信息 + 商品列表）；「有效商品」定义（`products.status=on_shelf` 是否还需校验分类/SKU）；排序字段与稳定排序（`sort,id`）；是否需要分页/数量上限。
5. 权限粒度：`recommend:create/update/delete/manage` 具体权限 code 集合；推荐位 CRUD 与推荐商品管理是否拆分权限；后台读接口是否沿用「仅 AdminAuth、无读权限」的现状。
6. 错误语义：推荐位不存在、code 非法、重复添加、商品不存在/不可售、越权/未认证的稳定错误码与 HTTP 状态。
7. 全局资源：新增 1 个错误码域（语义：推荐位 Recommend）、新增 1 个 migration（`recommend_positions` + `recommend_items` 两表）；具体域号/version 由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。

## Review Baseline

- Base commit：`d8438b4938a9b6b2e2f82fb87d9c6c9a60b6662b`（分支 `feat/recommendation-slot`，HEAD 与 `origin/feat/recommendation-slot` 一致）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/recommendation-v1/`、`api/recommendation*/`（或等价推荐位 API 包）、`internal/controller/recommendation*/`、`internal/logic/recommendation*/`、`internal/service` 的 `IRecommendation` 接口、`internal/codes` 推荐位域扩展、migration 文件（`recommend_positions` + `recommend_items`）、`internal/boot/seed.go` 的推荐位权限 seed、`internal/cmd` 路由扩展及对应测试；`docs/design/recommendation.md` 由 Analyst 写入。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）
