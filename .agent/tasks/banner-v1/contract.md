# Technical Contract

## Decision Status
WAITING_FOR_OWNER_DECISION

> 本文处于**最小 Contract Revision（REV-001 / CLEAN-001）待确认**：修正 `Update` 存在性判断语义，与已实现的三态语义对齐（详见下文 `Contract Revision`）。待 Owner ACCEPT/REJECT 后恢复 APPROVED。

## Problem

交付后端「轮播图（Banner）」V1 核心闭环：前台公开接口 `GET /banners` 返回启用中的轮播图列表（标题、图片地址、可选跳转目标，按展示顺序排列）；3 张占位图片经运行时本地文件存储（LocalStorage）由后端直接对外可访问；后台管理员可创建、更新、排序、启用/禁用、删除轮播图。本次不实现文件上传（无 HTTP 上传接口）与对象存储（MinIO/OSS/S3 为后续替换项，不在 V1）；图片经独立 Storage 边界引用。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2（Go 1.23+，`github.com/gogf/gf/v2 v2.10.3`），分层 `api/<module>/v1`（`g.Meta` path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。证据：`internal/logic/categories/categories.go`、`internal/service/category.go`。
- VERIFIED：身份域分离。前台 `middleware.Auth` 注入 `Principal{UserID,Sid}`；后台 `AdminAuth`（验签→401、`type≠admin`→403、会话失效→401、`admins` 禁用/不存在→401）+ `RequirePermission(code)`（`IsSuper` 放行、命中放行、未命中 403、DB 查询失败 fail-closed 500）。证据：`internal/middleware/auth.go`。
- VERIFIED：前台公开路由挂 `internal/cmd/routes_frontend.go`，后台写路由挂 `internal/cmd/routes_admin.go` 的 `require(code)` 分组；后台读接口（商品/库存查询）普遍仅 `AdminAuth`、无读权限。证据：`routes_admin.go` 第 84-101 行。
- VERIFIED：项目当前**没有任何静态文件服务**。全仓库 `AddStaticPath`/`AddStaticServer`/`SetServerRoot` 均无业务代码使用；`manifest/config/config.yaml` 无静态资源配置。唯一 `go:embed` 先例是 `internal/migrations` 内嵌 `sql/*.sql`。证据：`internal/migrations/migrations.go` 第 45-46 行。轮播图图片的本地文件静态服务为本次新增能力。
- VERIFIED：错误码集中在 `internal/codes/codes.go`（单一 `const` 块 + `codeTable`）。`.agent/registry/error-codes.md` 已分配至域序 13（`13000-13999` product-like-v1，RESERVED）。下一空闲域序 = 14，区间 `14000-14999`。
- VERIFIED：迁移 golang-migrate v4，`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，DDL 无 `IF NOT EXISTS`、仅 `.up.sql`。`.agent/registry/migrations.md` 最新 `20261001000013`（product-like-v1，RESERVED）。下一 version = `20261001000014`。`migrations_test.go` 的 `latestMigrationVersion` 当前 `20261001000012`（本分支基线）、`businessTables` 22 张、`expectedSchema` 需同步。
- VERIFIED：RBAC seed `internal/boot/seed.go` 的 `seedPermissionList` 现含 30 个权限（`category/product/sku/inventory/admin/role/permission/order/flash_sale/review`）；`banner:*` 未登记。权限 code 属 B 类 namespace，不进 `.agent/registry/*`。
- VERIFIED：事实来源为单一 MySQL；Redis 仅会话；无 MQ。轮播图为同步读写、无异步。
- VERIFIED：后端当前无任何轮播图表/模块；`frotend_web`/`frotend_manage` 为 vue-element-admin 模板工程，未接入本后端（前端渲染/轮播动画/点击跳转不在本次范围）。

## Recommendation

RECOMMENDATION：采用「单一位首页轮播 + 数据库驱动 + 自由字符串跳转 + 运行时本地文件存储（LocalStorage）+ 独立 Storage 边界、无 HTTP 上传」的最小闭环设计。

- 数据模型：新增 `banners` 表（软引用、无 FK），字段 `id/title/image_url/link_url/sort/status/created_at/updated_at`；`status` 二态 `1=启用 / 0=禁用`；`sort` 为展示顺序（升序，同值按 `id` 升序）；`link_url` 可空。索引 `idx_status_sort(status, sort)` 支撑公开列表查询。
- 公开查询（无 token）：`GET /banners`，仅返回 `status=1` 项，按 `sort` 升序、同值按 `id` 升序；每项含 `id/title/image_url/link_url/sort`。
- 后台查询（`AdminAuth`，无读权限，与商品/库存查询一致）：`GET /admin/banners`（全部状态列表）、`GET /admin/banners/:id`（详情）。
- 后台写（`AdminAuth` + `RequirePermission`）：`POST /admin/banners`（`banner:create`）、`PUT /admin/banners/:id`（`banner:update`）、`DELETE /admin/banners/:id`（`banner:delete`）。
- 图片存储：经独立 Storage 边界，V1 实现为 LocalStorage（本地目录 + 后端静态路由对外服务）；`banners.image_url` 存可访问地址（V1 为 `/storage/banners/<file>` 相对路径）。3 张占位图由启动 seed/初始化写入本地存储目录，**无 HTTP 上传接口**。

关键取舍：

- 跳转目标「自由字符串」vs「结构化类型 + 外键引用」：选自由字符串（单个可空 `link_url VARCHAR(512)`）。轮播图跳转最终是前端导航到一个 URL/路径，商品页/分类页/外部 URL 的区分由前端 URL 约定承担，后端无需为此建立跨模块引用、FK 或悬空引用处理；代价是无 DB 级引用完整性校验，符合 V1「最小闭环、不为未来假设提前抽象」取向。
- 图片「运行时本地文件存储 + Storage 边界」vs「go:embed 内嵌」vs「仓库静态目录」：按 Owner 决定选前者。定义独立 Storage 边界（V1 = LocalStorage），图片不内嵌进二进制、不作为静态资源进仓库，便于后续替换 MinIO/OSS/S3；代价是部署需在本地存储目录就绪占位图（无 go:embed 的自包含性），且 V1 无上传能力。
- 字段集合「最小字段」vs「时间窗/多分组/副标题」：选最小字段。生效时间窗、多位置/多分组、副标题均在 Out of Scope 默认排除，V1 只做单一「首页轮播」场景。

## Selected Design

已确认（Owner 决定）：

1. **跳转目标**：自由字符串 `link_url`（可空，无跳转时为 NULL）。
2. **图片存储**：运行时本地文件存储，不采用 go:embed。DB 存可访问 `image_url`；文件经独立 Storage 边界，V1 实现为 LocalStorage，后续可替换 MinIO/OSS/S3；**V1 无 HTTP 上传接口**，3 张占位图由部署/初始化阶段放入本地存储目录。
3. **字段集合**：最小字段 + 单一首页轮播（排除时间窗/多分组/副标题）。
4. **后台权限**：`banner:create/update/delete` 三个写权限；后台读仅 `AdminAuth`、无读权限。

## Interfaces and Data

### 数据模型 `banners`

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `title` | VARCHAR(64) | 非空，trim 后非空、≤64 字符 |
| `image_url` | VARCHAR(255) | 非空，可访问地址；V1 为后端静态路由相对路径（`/storage/banners/<file>`） |
| `link_url` | VARCHAR(512) | 可空，无跳转时为 NULL |
| `sort` | INT | 非空默认 0，展示顺序（升序） |
| `status` | TINYINT | 非空默认 1（`1=启用`、`0=禁用`） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`idx_status_sort(status, sort)`（公开列表 `WHERE status=1 ORDER BY sort`）。无唯一约束（sort 允许重复，同值按 `id` 升序）。

迁移：`20261001000014_banners.up.sql`，经 golang-migrate；同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion = 20261001000014`、`businessTables`（新增 `banners`，无外键依赖、置前）与 `expectedSchema`（新增 `banners` 结构快照）。

### Storage 边界（新增，V1 最小实现）

- 定义独立 Storage 边界（如 `internal/storage`，`Storage` 接口），V1 唯一实现为 `LocalStorage`（本地目录，根目录可配置）。
- 静态服务：后端用 GoFrame 静态文件服务将 LocalStorage 的 banner 目录映射为 `/storage/banners/...` 对外提供（正确图片 `Content-Type`）。
- 3 张占位图由启动 seed/初始化写入 LocalStorage banner 目录；**V1 无 HTTP 上传接口**。
- `banners.image_url` 存 Storage 边界生成/约定的可访问相对路径；banner 业务逻辑不感知具体存储介质，后续换 MinIO/OSS/S3 时仅替换 Storage 实现、`image_url` 改为外部 URL，banner 逻辑不变。

### API 契约

前台公开（无 token）：

- `GET /banners` → `{items:[{id, title, image_url, link_url, sort}]}`；仅 `status=1`，`ORDER BY sort ASC, id ASC`。

后台查询（`AdminAuth`，无读权限）：

- `GET /admin/banners` → `{items:[{id, title, image_url, link_url, sort, status, created_at, updated_at}]}`；全部状态，`ORDER BY sort ASC, id ASC`。
- `GET /admin/banners/:id` → 单个轮播图完整字段（全部状态）。

后台写（`AdminAuth` + `RequirePermission`）：

- `POST /admin/banners`：入参 `title`（必填）、`image_url`（必填）、`link_url`（可选）、`sort`（可选，默认 0）、`status`（可选，默认 1）。
- `PUT /admin/banners/:id`：入参 `title`/`image_url`/`link_url`/`sort`/`status` 均为可选（指针，仅更新提交字段）。
- `DELETE /admin/banners/:id`：物理删除（仅删 DB 记录，不删本地图片文件）。

### 错误码域（14000-14999，域序 14）

| code | 语义 | HTTP |
| --- | --- | --- |
| 14001 | BANNER_NOT_FOUND（轮播图不存在） | 404 |
| 14002 | BANNER_INVALID_INPUT（标题为空/超长、图片为空/超长、跳转超长、状态非法） | 400 |

复用：`1001`（参数格式错误兜底）、`1002`（401）、`1003`（403）。

### 权限 code（B 类 namespace，不进 Registry）

- 新增 `banner:create`、`banner:update`、`banner:delete`，在 `internal/boot/seed.go` 的 `seedPermissionList` 登记。

## Business Invariants

- INV-001（公开可见性与排序）：公开 `GET /banners` 仅返回 `status=1` 项，按 `sort` 升序、同值按 `id` 升序；禁用项即时不出现在公开列表。
- INV-002（权限边界）：后台写操作需 `AdminAuth` + 对应权限 code（`IsSuper` 放行）；未认证 401、无权限 403 且无任何 DB 写入。
- INV-003（数据完整性）：`title` 非空且 ≤64、`image_url` 非空且 ≤255、`link_url` 可空但 ≤512、`status ∈ {0,1}`；非法输入 400 且无写入。
- INV-004（删除语义）：删除成功后该轮播图不再出现在公开列表与后台查询中（仅删 DB 记录，不删本地图片文件）。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL `banners`；图片文件位于本地文件系统（LocalStorage），不参与 DB 事务。Redis 仅会话，不参与轮播图；无 MQ、无异步。
- 创建成功 = 单条 `INSERT banners`。
- 更新 = 先按 `id` 查存在性（不存在 → 14001 404、无写入），再执行 `UPDATE`；**不依据 `RowsAffected` 判断存在性**——目标存在但提交值无变化时 `RowsAffected=0` 仍为幂等成功；更新后再次查询兜底并发删除（已消失 → 14001 404）。未提交任何字段视为幂等成功。
- 删除 = 条件删除并核对 `RowsAffected`（`RowsAffected=0` → 14001 404、无写入）。
- 失败语义：未认证 401、无权限 403、不存在 404（14001）、非法输入 400（14002），均无写入；DB 技术错误统一 `1000`（500），不泄漏底层细节。
- `image_url` 为软引用：V1 不做「路径必须命中本地存储文件」的硬校验（无上传，图片由 seed/部署管理），悬空路径表现为图片 404/不可访问，不影响 DB 记录一致性。
- 无跨表/跨系统事务、无幂等键、无并发窗口（CRUD 为单表原子操作）；删除 banner 不触达文件系统。

## Allowed / Forbidden Changes

- 允许：新增 `api/banner/v1`、`internal/controller/banner`、`internal/service` 的 `IBanner` 接口与 `internal/logic/banner`；`internal/codes/codes.go` 新增 14000–14999 两个码；`internal/migrations/sql/20261001000014_banners.up.sql`；`migrations_test.go` 版本/表清单/结构快照更新；`internal/boot/seed.go` 新增 3 个 `banner:*` 权限；`internal/cmd/routes_frontend.go`、`routes_admin.go` 挂载轮播图路由；新增 Storage 边界（`internal/storage`，LocalStorage 实现 + 本地目录静态路由 + 占位图 seed）。
- 允许：为本地文件静态服务新增最小静态路由与 Storage 抽象，不改动既有公开接口与错误语义。
- 禁止：修改商品/分类/IAM/订单等既有模块行为与公开接口；禁止引入 Redis 缓存、MQ、异步任务、HTTP 文件上传接口、对象存储（MinIO/OSS/S3）、CDN、图片处理；禁止引入生效时间窗/多分组/副标题等未批准字段；禁止在 Feature Branch 内私留 `RESERVED` 条目。

## Verification Requirements

- INV-001（公开可见性与排序）→ 集成测试（MySQL）：预置启用/禁用轮播图，无 token 请求 `GET /banners`，断言仅含启用项、按 `sort` 升序、字段完整（AC-001、AC-006）。
- INV-002（权限边界）→ 集成测试（MySQL + Redis）：管理员带权限创建落库；无权限/未认证断言 401/403 且无写入（AC-003）。
- INV-003（数据完整性）→ 集成测试（MySQL）：非法标题/图片/状态断言 400 且无写入。
- INV-004（删除语义）→ 集成测试（MySQL）：更新各字段后断言公开列表与后台详情反映新值（AC-004）；删除后断言公开与后台均不再出现（AC-005）。
- 图片可访问（AC-002）→ 集成测试（启动服务）：本地存储目录内 3 张占位图经 `image_url` 直接请求断言 HTTP 200 且 `Content-Type` 为图片类型。
- 迁移（AC-007）→ 集成测试（MySQL）：`migrate up` 断言 `banners` 表结构正确、`migrations_test.go` 通过、迁移幂等。
- 长期设计（AC-008）→ 文档审查：`docs/design/banner.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Global Resource Reservation

| 类别 | 派生值 | 依据 | 状态 |
| --- | --- | --- | --- |
| 错误码域 | `14000-14999`（域序 14） | `max(已记录域序)=13`（product-like-v1）`+1`=14 | 待 Registry 落库 RESERVED |
| migration version | `20261001000014` | `max(已记录 version)=20261001000013`（product-like-v1）`+1` | 待 Registry 落库 RESERVED |

Contract APPROVED 后，将形成 Registry-only commit 写入 `.agent/registry/error-codes.md` 与 `.agent/registry/migrations.md` 的 `RESERVED` 行（不进入 Feature Branch）：

- `error-codes.md`：`| 14000-14999 | banner-v1 | RESERVED | banner-v1 轮播图域（域序 14 = max(13)+1） |`
- `migrations.md`：`| 20261001000014 | banners | banner-v1 | RESERVED | banners 轮播图表 |`

## Open Risks

- 3 张占位图不进仓库、不 go:embed：需由启动 seed/初始化在 LocalStorage banner 目录写入（本地生成极简占位图或部署脚本拷贝），部署时需保证本地存储目录可写且就绪；图片不可得时以程序生成的极简占位图替代。
- `image_url` 字段 V1 不做「路径必须命中本地存储文件」的硬校验：管理员可填任意路径字符串（含不存在的路径），悬空路径返回 404/图片不可访问；V1 接受此留白。
- 公开列表返回相对路径 `/storage/banners/...`：消费者需拼接服务源站；V1 不做绝对 URL（后端无法可靠得知对外域名）。
- Storage 边界为未来 MinIO/OSS/S3 替换而设，V1 只实现 LocalStorage；需避免 banner 逻辑泄漏 LocalStorage 细节，否则后续替换代价高。

## Owner Decision Record

| # | 问题 | 决定 | 与 Task 兼容性 |
| --- | --- | --- | --- |
| Q1 | 跳转目标语义 | 自由字符串 `link_url`（可空） | 兼容 |
| Q2 | 图片存储/服务方式 | 运行时本地文件存储（LocalStorage）+ 独立 Storage 边界，DB 存 `image_url`，V1 无 HTTP 上传；不采用 go:embed；MinIO/OSS/S3 为后续替换 | **改变原 Task**（见下） |
| Q3 | 字段集合 | 最小字段 + 单一首页轮播 | 兼容 |
| Q4 | 后台权限粒度 | `banner:create/update/delete`；后台读仅 `AdminAuth` | 兼容 |

Q2 改变原 `task.md`，已同步以下 4 处（`task.md` 现已与本文一致）：

1. Scope「图片静态资源」段：由「图片以静态资源形式放进仓库」改为「运行时本地文件存储（LocalStorage）+ 独立 Storage 边界，V1 无 HTTP 上传」。
2. Out of Scope：保留「文件上传」排除（V1 无 HTTP 上传接口）；「对象存储（OSS/S3）」仍排除，但注明 MinIO/OSS/S3 为 Storage 边界后续替换项、不在 V1。
3. AC-002：由「仓库内 3 张轮播图图片」改为「本地存储目录 3 张占位图，经 `image_url` 可访问（HTTP 200、内容类型为图片）」。
4. 字段名 `image` → `image_url`（Goal/Scope/AC 中涉及处）。

## Contract Revision

### REV-001（CLEAN-001）：修正 Update 存在性判断语义

- 分类：CONTRACT_REVISION（文档/Contract 对正确实现的语义校正，非新功能需求）。
- 原因：MySQL `UPDATE` 的 `RowsAffected=0` 既可能表示「记录不存在」，也可能表示「记录存在但提交值与原值完全相同」。原 Contract 将「更新/删除 `RowsAffected=0 → 14001 404`」统一用于 Update，导致「无变化的幂等更新」被误判为 404。
- 修订内容：仅修正 `Update` 语义为三态——不存在 → 14001/404；存在且字段变化 → 成功；存在但值未变（含未提交任何字段）→ 幂等成功；不再以 `RowsAffected=0` 等价不存在。`Delete` 语义不变（`RowsAffected=0 → 404` 对 DELETE 仍正确）。
- 与实现一致：`internal/logic/banner/banner.go` `Update` 已采用「更新前 `findOne` 判存在 → `UPDATE`（不依赖 `RowsAffected`）→ 更新后 `findOne` 兜底并发删除」；回归测试 `TestBannerUpdateRegression` 覆盖「不存在→404 / 有变化→成功 / 相同值幂等→成功 / 无权限→403 无副作用」。
- 范围：仅 `Failure and Consistency Semantics` 一处 + `docs/design/banner.md` §5 同步；不改 API、错误码、数据库结构、权限、业务代码或测试。
- 状态：WAITING_FOR_OWNER_DECISION（待 Owner ACCEPT/REJECT）。
