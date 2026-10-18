# Task: 轮播图（Banner）V1

## Goal

交付后端「轮播图」核心能力：前端可通过公开接口获取当前启用中的轮播图列表（含标题、图片地址、可选跳转目标，按展示顺序排列）；3 张轮播图占位图经运行时本地文件存储（LocalStorage）由后端直接对外可访问；后台管理员可在后台对轮播图进行创建、更新、排序、启用/禁用与删除管理。本次不实现文件上传（无 HTTP 上传接口）与对象存储，图片经独立 Storage 边界引用。

## Scope

- 轮播图数据模型（`banners` 表），经既有 golang-migrate 机制新增迁移文件（schema 由 Analyst 固化），并同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion`、`businessTables` 与 `expectedSchema`。
- 公开查询接口 `GET /banners`：无需登录，返回启用中的轮播图列表，按展示顺序（`sort`）排列。
- 后台管理接口（AdminAuth + RequirePermission，具体权限码由 Analyst 固化并在 `internal/boot/seed.go` 登记）：
  - 创建轮播图（标题、图片引用、可选跳转目标、排序、状态）；
  - 更新轮播图（改标题/图片/跳转/排序/状态）；
  - 删除轮播图。
- 图片存储：3 张占位图经运行时本地文件存储（LocalStorage）+ 独立 Storage 边界对外可访问；`banners.image_url` 存可访问地址；无 HTTP 上传接口、不接对象存储。
- 错误码：新增轮播图错误码域（语义：轮播图 Banner），具体编号由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。
- 长期设计：新增 `docs/design/banner.md`（Design Impact = NEW），沉淀轮播图数据模型、图片引用与静态服务方式、排序/状态语义、权限边界与错误码域。
- 必要测试：公开列表（启用过滤/排序/边界）、后台创建/更新/删除/权限拒绝、状态启用禁用的可见性，以及迁移幂等。

## Out of Scope

- 文件上传（V1 无 HTTP 上传接口）、对象存储（OSS/S3 等，MinIO/OSS/S3 为 Storage 边界后续替换项、不在 V1）、CDN、图片处理（裁剪/压缩/水印）。
- 前端页面改造（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）；轮播图的前端渲染、轮播动画、点击跳转行为均不在本次范围。
- 多位置/多分组轮播（如首页位、活动位），除非 Analyst/Owner 明确需要（见 Analyst Questions）。
- 定时上下架 / 生效时间窗，除非 Analyst/Owner 明确需要（见 Analyst Questions）。
- 轮播图点击统计、曝光统计、A/B 测试。
- 修改既有商品/分类/IAM 等模块行为（除轮播图跳转目标引用所需的最小只读查询）。

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/banner.md

## Acceptance Criteria

- [ ] AC-001（公开列表）：无 token 访问 `GET /banners` 成功返回启用中的轮播图列表，仅含启用项，且按展示顺序（`sort`）排列；每项含标题、图片地址（该地址可直接访问）、可选跳转目标。
- [ ] AC-002（图片可访问）：本地存储目录内 3 张占位图均可经 `image_url` 直接访问（HTTP 200，且内容类型为图片）。
- [ ] AC-003（后台创建与权限）：管理员携带相应权限可创建轮播图并落库；无权限或未认证访问返回稳定拒绝（401/403）且不产生写入。
- [ ] AC-004（后台更新与排序）：管理员可更新轮播图标题、图片、跳转目标、排序与状态，更新后公开列表与详情反映新值。
- [ ] AC-005（后台删除）：管理员可删除轮播图，删除后不再出现在公开列表与后台可见数据中。
- [ ] AC-006（状态过滤）：禁用（下线）的轮播图不出现在公开 `GET /banners` 列表中；后台查询可看到全部状态。
- [ ] AC-007（数据模型与迁移）：`banners` 表经迁移正确建表，`migrations_test.go` 的版本号与表清单同步更新，迁移可重复/幂等执行。
- [ ] AC-008（长期设计）：新增 `docs/design/banner.md`，沉淀轮播图数据模型、图片引用与静态服务方式、排序/状态语义、权限边界与错误码域，并与 APPROVED Contract、最终实现一致。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。轮播图模块应沿用此结构。
- 身份域：前台用户经 `middleware.Auth` 注入 `Principal{UserID, Sid}`；后台管理员经 `AdminAuth` + `RequirePermission(code)`（`IsSuper` 放行、fail-closed）。前台公开路由挂 `routes_frontend.go`，后台写路由挂 `routes_admin.go` 的 `require(code)` 分组。
- 静态资源现状：后端当前**没有任何静态文件服务**（`AddStaticPath`/`AddStaticServer`/`SetServerRoot`/`go:embed` 资源目录均不存在于业务代码），`manifest/config/config.yaml` 亦无静态资源相关配置。轮播图图片的本地文件静态服务为本次新增能力（V1 实现为 LocalStorage，见 Contract）。
- 错误码集中在 `internal/codes/codes.go`；`.agent/registry/error-codes.md` 已分配至 `12000-12999`（flash-sale-v1，RESERVED）。轮播图为下一空闲错误码域（具体域号由 Analyst 派生）。
- 迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，`.agent/registry/migrations.md` 最新为 `20261001000012`（product-view-count-v1，RESERVED）。轮播图表需新增 1 个 migration（具体 version 由 Analyst 派生），并同步更新 `migrations_test.go`（`latestMigrationVersion` 当前为 `20261001000012`、`businessTables` 当前 22 张）。
- RBAC seed：`internal/boot/seed.go` 的 `seedPermissionList` 现含 27 个权限；轮播图管理写权限需新增并登记（权限 code 属 namespace 类资源，不进 `.agent/registry/*`）。
- 事实来源为单一 MySQL；Redis 仅会话；无 MQ。轮播图为同步读写，无异步。
- 后端当前无任何轮播图表/模块；`frotend_web`/`frotend_manage` 为 vue-element-admin 模板工程，未接入本后端。

Assumption（合理但未经 Owner 确认，交 Analyst 核实并向 Owner 确认）：

- 轮播图为数据库驱动（`banners` 表 + 后台管理），而非硬编码/配置文件清单；「先实现轮播图功能」指具备可运营管理的最小闭环。
- 后台管理在本次范围，图片经运行时本地文件存储（LocalStorage）+ 独立 Storage 边界引用，无 HTTP 上传、不接对象存储。
- 3 张图片为占位/示例用途，由启动 seed/初始化写入本地存储目录（图片不可得时以程序生成的极简占位图替代）。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 跳转目标语义：轮播图是否携带跳转（商品/分类/外部 URL/无跳转），跳转目标是外键引用还是自由字符串。
- 静态服务方式：已由 Owner 决定为运行时本地文件存储（LocalStorage）+ 独立 Storage 边界（见 Contract），不再开放。
- 字段集合：是否需要生效时间窗、多位置/多分组（position/type）、副标题等；是否仅单一「首页轮播」场景即可。
- 后台权限粒度：`banner:create/update/delete` 是否足够，是否需区分读权限（现有后台读接口普遍仅 AdminAuth，无读权限）。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试必须走真实 `RegisterFrontendRoutes`（公开列表）与 `RegisterAdminRoutes` + `AdminAuth/RequirePermission`（后台管理），用真实管理员账号断言。

- AC-001 → 需 MySQL：预置启用/禁用轮播图后，无 token 请求 `GET /banners`，断言仅含启用项、按 `sort` 排列、字段完整。
- AC-002 → 需启动服务：直接请求返回的图片地址，断言 HTTP 200 且 `Content-Type` 为图片类型，占位图存在于本地存储目录。
- AC-003 → 需 MySQL：管理员带权限创建轮播图断言落库；无权限/未认证断言 401/403 且无写入。
- AC-004 → 需 MySQL：更新各字段后断言公开列表与后台详情反映新值。
- AC-005 → 需 MySQL：删除后断言公开列表与后台均不再出现。
- AC-006 → 需 MySQL：禁用项不出现在公开列表，后台可见全部状态。
- AC-007 → 需 MySQL：执行迁移断言 `banners` 表结构正确、`migrations_test.go` 通过、迁移可重复执行。
- AC-008 → 文档审查：`docs/design/banner.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及新数据模型（`banners` 表）、新公开协议（公开列表 API + 后台管理 API + 错误码域）、以及一个此前不存在的新能力（后端静态图片服务）；跳转目标语义、静态服务方式、字段集合与后台权限粒度均存在多个会产生不同业务结果与运维形态的现实方案，需 Analyst 固化 Contract 后由 Owner 确认。

## Analyst Questions

1. 轮播图数据模型：`banners` schema；字段集合（标题、图片、跳转目标、排序、状态等）；跳转目标建模（外键引用 vs 自由字符串、是否可空）。
2. 跳转目标语义：是否携带跳转；跳转类型（商品/分类/外部 URL/无）；校验与可空规则。
3. 静态图片服务方式：仓库静态目录 + GoFrame 静态路由 vs `go:embed` 内嵌资源 vs 外部 URL；图片地址在 `banners` 中如何存储（相对路径/完整路径/绝对 URL）。
4. 公开列表语义：仅返回启用项、排序字段与方向；是否需位置/分组（多轮播位）；是否需要生效时间窗。
5. 后台权限粒度：`banner:create/update/delete` 等权限 code 及是否需读权限；权限 seed 登记。
6. 错误语义：公开列表、创建/更新/删除、越权/不存在/非法输入的稳定错误码与 HTTP 状态。
7. 全局资源：新增轮播图错误码域（语义：轮播图 Banner）、新增 1 个 migration（`banners` 表）；具体域号/version 由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。

## Review Baseline

- Base commit：`727cf978976d63010ed8bb67a991109e251bc57f`（分支 `feature/banner`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/banner-v1/`、`api/banner*/`（或等价轮播图 API 包）、`internal/controller/banner*/`、`internal/logic/banner*/`、`internal/service` 的 `IBanner` 接口、`internal/codes` 轮播图域扩展、migration 文件（`banners`）、Storage 边界（`internal/storage`，本地目录静态路由）、`internal/boot/seed.go` 的轮播图权限 seed、`internal/cmd` 路由扩展及对应测试；`docs/design/banner.md` 由 Analyst 写入。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）
