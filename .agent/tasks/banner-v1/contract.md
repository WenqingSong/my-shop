# Technical Contract

## Decision Status
WAITING_FOR_OWNER_DECISION

## Problem

交付后端「轮播图（Banner）」V1 核心闭环：前台公开接口 `GET /banners` 返回启用中的轮播图列表（标题、图片地址、可选跳转目标，按展示顺序排列）；3 张占位图片以静态资源形式进仓库并由后端直接对外可访问；后台管理员可创建、更新、排序、启用/禁用、删除轮播图。本次不实现文件上传与对象存储，图片经仓库内静态资源引用。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2（Go 1.23+，`github.com/gogf/gf/v2 v2.10.3`），分层 `api/<module>/v1`（`g.Meta` path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。证据：`internal/logic/categories/categories.go`、`internal/service/category.go`。
- VERIFIED：身份域分离。前台 `middleware.Auth` 注入 `Principal{UserID,Sid}`；后台 `AdminAuth`（验签→401、`type≠admin`→403、会话失效→401、`admins` 禁用/不存在→401）+ `RequirePermission(code)`（`IsSuper` 放行、命中放行、未命中 403、DB 查询失败 fail-closed 500）。证据：`internal/middleware/auth.go`。
- VERIFIED：前台公开路由挂 `internal/cmd/routes_frontend.go`，后台写路由挂 `internal/cmd/routes_admin.go` 的 `require(code)` 分组；后台读接口（商品/库存查询）普遍仅 `AdminAuth`、无读权限。证据：`routes_admin.go` 第 84-101 行。
- VERIFIED：项目当前**没有任何静态文件服务**。全仓库 `AddStaticPath`/`AddStaticServer`/`SetServerRoot` 均无业务代码使用；`manifest/config/config.yaml` 无静态资源配置。唯一 `go:embed` 先例是 `internal/migrations` 内嵌 `sql/*.sql`。证据：`internal/migrations/migrations.go` 第 45-46 行。
- VERIFIED：错误码集中在 `internal/codes/codes.go`（单一 `const` 块 + `codeTable`）。`.agent/registry/error-codes.md` 已分配至域序 12（`12000-12999` flash-sale-v1，RESERVED）。下一空闲域序 = 13，区间 `13000-13999`。
- VERIFIED：迁移 golang-migrate v4，`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，DDL 无 `IF NOT EXISTS`、仅 `.up.sql`。`.agent/registry/migrations.md` 最新 `20261001000012`（product-view-count-v1，RESERVED）。下一 version = `20261001000013`。`migrations_test.go` 的 `latestMigrationVersion` 当前 `20261001000012`、`businessTables` 22 张、`expectedSchema` 需同步。
- VERIFIED：RBAC seed `internal/boot/seed.go` 的 `seedPermissionList` 现含 27 个权限（`category/product/sku/inventory/admin/role/permission/order/flash_sale/review`）；`banner:*` 未登记。权限 code 属 B 类 namespace，不进 `.agent/registry/*`。
- VERIFIED：事实来源为单一 MySQL；Redis 仅会话；无 MQ。轮播图为同步读写、无异步。
- VERIFIED：后端当前无任何轮播图表/模块；`frotend_web`/`frotend_manage` 为 vue-element-admin 模板工程，未接入本后端（前端渲染/轮播动画/点击跳转不在本次范围）。

## Recommendation

RECOMMENDATION：采用「单一位首页轮播 + 数据库驱动 + 自由字符串跳转 + go:embed 内嵌静态图」的最小闭环设计。

- 数据模型：新增 `banners` 表（软引用、无 FK），字段 `id/title/image/link_url/sort/status/created_at/updated_at`；`status` 二态 `1=启用 / 0=禁用`；`sort` 为展示顺序（升序，同值按 `id` 升序）；`link_url` 可空。索引 `idx_status_sort(status, sort)` 支撑公开列表查询。
- 公开查询（无 token）：`GET /banners`，仅返回 `status=1` 项，按 `sort` 升序、同值按 `id` 升序；每项含 `id/title/image/link_url/sort`。
- 后台查询（`AdminAuth`，无读权限，与商品/库存查询一致）：`GET /admin/banners`（全部状态列表）、`GET /admin/banners/:id`（详情）。
- 后台写（`AdminAuth` + `RequirePermission`）：`POST /admin/banners`（`banner:create`）、`PUT /admin/banners/:id`（`banner:update`）、`DELETE /admin/banners/:id`（`banner:delete`）。
- 图片静态资源：3 张占位图置于 `internal/static/banners/`，经 `//go:embed` 内嵌进二进制，后端在 `/static/banners/` 下直接提供（正确 `Content-Type` 图片类型）；`banners.image` 存相对路径（如 `/static/banners/1.png`）。

关键取舍：

- 跳转目标「自由字符串」vs「结构化类型 + 外键引用」：选自由字符串（单个可空 `link_url VARCHAR(512)`）。轮播图跳转最终是前端导航到一个 URL/路径，商品页/分类页/外部 URL 的区分由前端 URL 约定承担，后端无需为此建立跨模块引用、FK 或悬空引用处理；代价是无 DB 级引用完整性校验，符合 V1「最小闭环、不为未来假设提前抽象」取向。
- 静态图片「go:embed 内嵌」vs「仓库静态目录 + 运行路径」：选 go:embed 内嵌。与项目既有 migration 的 `go:embed` 模式一致，单二进制部署不依赖工作目录/外置文件，且严格满足 AC-002「不依赖外部 URL 或本机临时文件」；代价是图片变更需重新编译（V1 仅 3 张占位图，可接受）。
- 字段集合「最小字段」vs「时间窗/多分组/副标题」：选最小字段。生效时间窗、多位置/多分组、副标题均在 Out of Scope 默认排除（除非 Owner 明确需要），V1 只做单一「首页轮播」场景。

## Selected Design

等待 Owner 确认。

## Interfaces and Data

### 数据模型 `banners`

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `title` | VARCHAR(64) | 非空，trim 后非空、≤64 字符 |
| `image` | VARCHAR(255) | 非空，相对路径（`/static/banners/<file>`），仓库内静态资源 |
| `link_url` | VARCHAR(512) | 可空，无跳转时为 NULL |
| `sort` | INT | 非空默认 0，展示顺序（升序） |
| `status` | TINYINT | 非空默认 1（`1=启用`、`0=禁用`） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`idx_status_sort(status, sort)`（公开列表 `WHERE status=1 ORDER BY sort`）。无唯一约束（sort 允许重复，同值按 `id` 升序）。

迁移：`20261001000013_banners.up.sql`，经 golang-migrate；同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion = 20261001000013`、`businessTables`（新增 `banners`，无外键依赖、置前）与 `expectedSchema`（新增 `banners` 结构快照）。

### API 契约

前台公开（无 token）：

- `GET /banners` → `{items:[{id, title, image, link_url, sort}]}`；仅 `status=1`，`ORDER BY sort ASC, id ASC`。

后台查询（`AdminAuth`，无读权限）：

- `GET /admin/banners` → `{items:[{id, title, image, link_url, sort, status, created_at, updated_at}]}`；全部状态，`ORDER BY sort ASC, id ASC`。
- `GET /admin/banners/:id` → 单个轮播图完整字段（全部状态）。

后台写（`AdminAuth` + `RequirePermission`）：

- `POST /admin/banners`：入参 `title`（必填）、`image`（必填）、`link_url`（可选）、`sort`（可选，默认 0）、`status`（可选，默认 1）。
- `PUT /admin/banners/:id`：入参 `title`/`image`/`link_url`/`sort`/`status` 均为可选（指针，仅更新提交字段）。
- `DELETE /admin/banners/:id`：物理删除。

### 错误码域（13000-13999，域序 13）

| code | 语义 | HTTP |
| --- | --- | --- |
| 13001 | BANNER_NOT_FOUND（轮播图不存在） | 404 |
| 13002 | BANNER_INVALID_INPUT（标题为空/超长、图片为空/超长、跳转超长、状态非法） | 400 |

复用：`1001`（参数格式错误兜底）、`1002`（401）、`1003`（403）。

### 权限 code（B 类 namespace，不进 Registry）

- 新增 `banner:create`、`banner:update`、`banner:delete`，在 `internal/boot/seed.go` 的 `seedPermissionList` 登记。

## Business Invariants

- INV-001（公开可见性与排序）：公开 `GET /banners` 仅返回 `status=1` 项，按 `sort` 升序、同值按 `id` 升序；禁用项即时不出现在公开列表。
- INV-002（权限边界）：后台写操作需 `AdminAuth` + 对应权限 code（`IsSuper` 放行）；未认证 401、无权限 403 且无任何 DB 写入。
- INV-003（数据完整性）：`title` 非空且 ≤64、`image` 非空且 ≤255、`link_url` 可空但 ≤512、`status ∈ {0,1}`；非法输入 400 且无写入。
- INV-004（删除语义）：删除成功后该轮播图不再出现在公开列表与后台查询中。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL `banners`；Redis 仅会话，不参与轮播图；无 MQ、无异步。
- 创建成功 = 单条 `INSERT banners`；更新/删除 = 条件更新/删除并核对 `RowsAffected`（`RowsAffected=0` → 13001 404）。
- 失败语义：未认证 401、无权限 403、不存在 404（13001）、非法输入 400（13002），均无写入；DB 技术错误统一 `1000`（500），不泄漏底层细节。
- 无跨表/跨系统事务、无幂等键、无并发窗口（CRUD 为单表原子操作）。

## Allowed / Forbidden Changes

- 允许：新增 `api/banner/v1`、`internal/controller/banner`、`internal/service` 的 `IBanner` 接口与 `internal/logic/banner`；`internal/codes/codes.go` 新增 13000–13999 两个码；`internal/migrations/sql/20261001000013_banners.up.sql`；`migrations_test.go` 版本/表清单/结构快照更新；`internal/boot/seed.go` 新增 3 个 `banner:*` 权限；`internal/cmd/routes_frontend.go`、`routes_admin.go` 挂载轮播图路由；`internal/static/banners/` 占位图 + go:embed 静态服务。
- 允许：为静态图片服务新增最小内嵌资源目录与静态路由，不改动既有公开接口与错误语义。
- 禁止：修改商品/分类/IAM/订单等既有模块行为与公开接口；禁止引入 Redis 缓存、MQ、异步任务、文件上传、对象存储、CDN、图片处理；禁止引入生效时间窗/多分组/副标题等未批准字段；禁止在 Feature Branch 内私留 `RESERVED` 条目。

## Verification Requirements

- INV-001（公开可见性与排序）→ 集成测试（MySQL）：预置启用/禁用轮播图，无 token 请求 `GET /banners`，断言仅含启用项、按 `sort` 升序、字段完整（AC-001、AC-006）。
- INV-002（权限边界）→ 集成测试（MySQL + Redis）：管理员带权限创建落库；无权限/未认证断言 401/403 且无写入（AC-003）。
- INV-003（数据完整性）→ 集成测试（MySQL）：非法标题/图片/状态断言 400 且无写入。
- INV-004（删除语义）→ 集成测试（MySQL）：更新各字段后断言公开列表与后台详情反映新值（AC-004）；删除后断言公开与后台均不再出现（AC-005）。
- 图片可访问（AC-002）→ 集成测试（启动服务）：直接请求返回的图片地址断言 HTTP 200 且 `Content-Type` 为图片类型；图片文件存在于仓库。
- 迁移（AC-007）→ 集成测试（MySQL）：`migrate up` 断言 `banners` 表结构正确、`migrations_test.go` 通过、迁移幂等。
- 长期设计（AC-008）→ 文档审查：`docs/design/banner.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Global Resource Reservation

| 类别 | 派生值 | 依据 | 状态 |
| --- | --- | --- | --- |
| 错误码域 | `13000-13999`（域序 13） | `max(已记录域序)=12`（flash-sale-v1）`+1`=13 | 待 Owner 确认后 RESERVED |
| migration version | `20261001000013` | `max(已记录 version)=20261001000012`（product-view-count-v1）`+1` | 待 Owner 确认后 RESERVED |

Contract APPROVED 后，将形成 Registry-only commit 写入 `.agent/registry/error-codes.md` 与 `.agent/registry/migrations.md` 的 `RESERVED` 行（不进入 Feature Branch）：

- `error-codes.md`：`| 13000-13999 | banner-v1 | RESERVED | banner-v1 轮播图域（域序 13 = max(12)+1） |`
- `migrations.md`：`| 20261001000013 | banners | banner-v1 | RESERVED | banners 轮播图表 |`

## Open Risks

- 3 张占位图需实现方从公开来源获取并提交进仓库：需注意来源可用性与无敏感/侵权内容；图片不可得时以本地生成的极简占位图替代。
- `image` 字段 V1 不做「路径必须命中内嵌资源集」的硬校验：管理员可填任意路径字符串（含不存在的路径），悬空路径返回 404/图片不可访问；V1 接受此留白，不引入上传与资源清单校验。
- 公开列表返回相对路径 `/static/banners/...`：消费者需拼接服务源站；V1 不做绝对 URL（后端无法可靠得知对外域名）。

## Owner Decision Record

等待 Owner 确认。
