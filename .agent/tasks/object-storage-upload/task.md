# Task: 文件上传与对象存储（Qiniu）V1

## Goal

交付后端「文件上传 / 对象存储」能力：客户端经后端接口取得上传凭证后，可将文件上传至七牛云（Qiniu）对象存储并获得可公开访问的文件地址；上传受身份/权限、文件类型与大小约束，错误可稳定区分；七牛云凭据经配置安全注入、不硬编码、不提交仓库。

## Scope

- 七牛云对象存储接入：凭据（AccessKey/SecretKey 或等价的 token）、bucket、对外域名/region、凭证有效期等经配置注入；凭据按敏感信息处理（secret 走环境变量，不提交、不进日志/响应）。
- 配置字段：按 Owner 指示，Coder 先落地配置字段结构（`manifest/config/config.yaml` 增加七牛云相关段，含安全默认值与必填说明），Owner 注入七牛云 token 后再继续实现；关键配置缺失/非法不得静默忽略。
- 上传凭证签发接口（供客户端直传）与/或后端上传接口（具体上传模型由 Analyst 固化，见 Analyst Questions）。
- 上传边界：允许的文件类型（扩展名/内容类型）与大小上限、单文件或多文件、上传主体（后台管理员 / 前台登录用户 / 匿名）的权限与访问控制。
- 错误语义：新增文件上传/对象存储错误码域（语义：upload/storage），具体域号由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。
- 长期设计：新增 `docs/design/storage.md`（Design Impact = NEW），沉淀上传模型、配置模型、安全边界与错误语义。
- 必要测试：凭证签发正常/拒绝路径、类型与大小边界、凭据缺失/非法语义、错误码与（如有）迁移幂等。

## Out of Scope

- CDN、图片处理（裁剪/压缩/水印/格式转换/缩略图）。
- 其他对象存储（MinIO/OSS/S3）接入（除非设计决定同时支持）。
- 前端模板工程（`frotend_web`/`frotend_manage`）的真实改造：它们是与本后端未连通的模板工程，本次仅交付后端能力与明确 API 契约，不实现前端页面上传。
- 与具体业务实体（商品图、文章图、轮播图等）的强关联落库改造，除非 Analyst/Owner 明确需要。
- 上传进度、分片上传/断点续传/大文件直传优化（除非 Analyst/Owner 明确需要）。
- 修改既有模块（商品/文章/轮播图/IAM 等）行为，除上传能力所需的最小接入外。

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/storage.md

## Acceptance Criteria

- [ ] AC-001（凭证签发）：已认证且被授权的客户端调用上传凭证接口，返回可用的七牛云上传凭证（含上传所需 bucket/域名等最小必要信息），可用于向七牛云上传文件。
- [ ] AC-002（上传与可访问）：使用返回凭证向七牛云上传文件成功后，可取得一个可直接访问的文件 URL（HTTP 200，内容类型与上传文件一致）。—— 需要真实七牛云凭据，否则如实标记 NOT_VERIFIED。
- [ ] AC-003（鉴权拒绝）：未认证 / 无权限的请求访问凭证签发接口返回稳定 401/403，不签发凭证、不产生上传。
- [ ] AC-004（类型/大小边界）：超出允许的扩展名/内容类型或大小上限的请求被拒绝，返回稳定错误码（400），不签发凭证。
- [ ] AC-005（配置与凭据安全）：七牛云凭据缺失或非法时，服务按安全语义失败（启动 fail-fast 或签发时明确错误码），不在日志/响应/代码中泄漏凭据，不硬编码默认凭据。
- [ ] AC-006（错误码域与迁移）：新增文件上传/对象存储错误码域经 `.agent/registry/*` 分配并在 `internal/codes` 落地；若架构决定落库文件记录，新增 migration 且幂等可重复执行。
- [ ] AC-007（长期设计）：新增 `docs/design/storage.md`，沉淀上传模型、配置模型、安全边界与错误语义，并与 APPROVED Contract、最终实现一致。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2，模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。
- 身份域：前台用户经 `middleware.Auth` 注入 `Principal{UserID, Sid}`；后台管理员经 `AdminAuth` + `RequirePermission(code)`（`IsSuper` 放行、fail-closed）。前台公开路由挂 `routes_frontend.go`，后台写路由挂 `routes_admin.go` 的 `require(code)` 分组。
- 现状存储边界：`internal/storage/storage.go` 仅定义 `Storage` 接口 + `LocalStorage`（本地目录），且仅实现轮播图占位图 `PrepareBannerPlaceholders`，**无 HTTP 上传接口、无对象存储接入**；`internal/cmd/cmd.go` 将 LocalStorage 的 banner 目录映射为静态路由 `/storage/banners`。
- 前端模板工程 `frotend_web` 与 `frotend_manage` 的 `src/api/qiniu.js` 均已引用 `GET /qiniu/upload/token`（注释标注「假地址 自行替换」），暗示前端期望后端签发七牛云上传 token（直传模型）。
- 配置机制：`manifest/config/config.yaml` + `internal/boot/boot.go` 经 `g.Cfg().GetEffective(ctx, key, def)` 读取、支持环境变量覆盖；现有 `storage.local.root` 段，无任何对象存储配置。
- 错误码集中在 `internal/codes/codes.go`（域区间 + `codeTable` 绑定 HTTP 状态与安全 message）；`.agent/registry/error-codes.md` 最新已分配至 `16000-16999`（article-cms-v1，RESERVED），下一空闲域由 Analyst 派生。
- 迁移机制 golang-migrate v4（`internal/migrations/sql/{14位时间戳}_{title}.up.sql`）；`.agent/registry/migrations.md` 最新为 `20261001000017`（articles，RESERVED）。是否新增 migration 取决于是否落库文件记录（见 Analyst Questions）。
- RBAC seed：`internal/boot/seed.go` 的 `seedPermissionList` 幂等写入权限 code（code 属 namespace 类资源，不进 `.agent/registry/*`）。
- 事实来源为单一 MySQL；Redis 仅会话；无 MQ。

Assumption（合理但未经 Owner 确认，交 Analyst 核实并向 Owner 确认）：

- 上传模型倾向「后端签发七牛云上传 token、客户端直传」，最贴合现有前端 `qiniu.js`；但仍需 Analyst 固化并经 Owner 确认。
- 范围仅后端能力 + 明确 API 契约，不改前端模板工程。
- 七牛云凭据经配置注入，secret 走环境变量，不提交仓库、不写死默认值。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 上传模型：后端签发 token 客户端直传 vs 后端代理上传 vs 后端直传 multipart（三者安全/可靠/运维形态不同）。
- 上传主体与权限：仅后台管理员，还是前台登录用户，或开放匿名；是否需要独立权限 code。
- 是否落库文件记录（`files`/`uploads` 表）跟踪上传结果，还是仅无状态签发凭证。
- 文件类型/大小白名单与上限、单文件 vs 多文件。
- 是否引入七牛云官方 Go SDK 依赖（go.mod 成本与维护），还是自实现凭证签名。

## Verification

环境：可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；涉及真实七牛云上传的 AC 需真实凭据，缺失时标记 NOT_VERIFIED 并说明影响。

- AC-001 → 需启动服务：已认证客户端请求凭证签发接口，断言返回可用凭证及 bucket/域名等最小必要信息；签名/过期时间正确。
- AC-002 → 需真实七牛云凭据：用返回凭证实际上传一个小文件，断言取得可公开访问 URL、HTTP 200、内容类型正确；无凭据时 NOT_VERIFIED。
- AC-003 → 需 MySQL：未认证/无权限请求断言 401/403、不签发凭证、无写入。
- AC-004 → 需启动服务：非法扩展名/类型或超大小请求断言稳定 400、不签发凭证。
- AC-005 → 需启动服务：缺凭据/非法凭据时断言按安全语义失败（fail-fast 或明确错误码），响应/日志不含凭据。
- AC-006 → 需 MySQL（若落库）：错误码域经 registry 分配、`internal/codes` 落地；迁移幂等可重复执行、`migrations_test.go` 同步更新。
- AC-007 → 文档审查：`docs/design/storage.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Complexity

COMPLEX

原因：新增对象存储/文件上传这一此前不存在的模块能力与长期公开协议（凭证签发/上传 API + 新错误码域），引入第三方七牛云接入，且存在多个会产生不同业务、安全与运维结果的关键方案（直传 vs 代理 vs 后端直传；是否落库；上传主体与权限粒度），需 Analyst 固化 Contract 后由 Owner 确认。

## Analyst Questions

1. 上传模型：后端签发七牛云上传 token 客户端直传，还是后端代理上传，或后端直传 multipart；各方案的安全边界、失败语义与运维成本对比与推荐。
2. 七牛云凭据与配置模型：AccessKey/SecretKey 或等价 token、bucket、对外域名/region、凭证有效期（TTL）、单文件大小上限等字段；secret 的注入与保护方式（环境变量/配置优先级）；缺失/非法配置的启动与运行时语义。
3. 上传主体与权限：后台管理员 / 前台登录用户 / 匿名；是否需要独立权限 code 与 seed 登记；未认证 401、无权限 403 的具体边界。
4. 文件边界：允许的内容类型/扩展名白名单、大小上限、单文件 vs 多文件；非法输入的稳定错误码与 HTTP 状态。
5. 是否落库文件记录（`files`/`uploads` 表）：字段集合、与上传结果/回调的关联、是否需要 migration；还是仅无状态签发凭证。
6. 第三方依赖：是否引入七牛云官方 Go SDK，还是仅实现凭证签名（评估 go.mod 依赖成本）。
7. 全局资源：新增 1 个错误码域（语义：upload/storage），具体域号由 Analyst 读 `.agent/registry/*` 派生并写入 Contract；若落库则新增 1 个 migration（`files`/`uploads` 表），具体 version 由 Analyst 派生。

## Review Baseline

- Base commit：`7de6e74904e2cd95cfc931bb8cd4628c4e23a859`（分支 `feat/object-storage-upload`，local == `origin/feat/object-storage-upload`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/object-storage-upload/`、`api/upload*`（或等价上传 API 包）、`internal/controller/upload*`、`internal/logic/upload*`、`internal/service` 的 `IUpload` 接口、`internal/codes` 上传/存储域扩展、`manifest/config/config.yaml` 七牛云配置段、可能的 migration 文件与 `internal/storage` 对象存储实现、`internal/boot`/`internal/cmd` 路由与启动接线及对应测试；`docs/design/storage.md` 由 Analyst 写入。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）
