# 轮播图设计（Banner）

本文面向项目接手者，说明「轮播图」核心闭环的架构、数据模型、图片存储边界、排序/状态语义、权限边界与错误码域。事实来源为 `banner-v1` 的 APPROVED Contract 与最终实现。

## 1. 职责与边界

轮播图回答「运营在首页展示什么」，承载轮播图主数据（`banners`），实现：前台公开列表（启用项、按展示顺序排列）、后台创建/更新/排序/启用禁用/删除。图片经独立 Storage 边界以运行时本地文件存储（LocalStorage）对外服务。

边界：V1 无 HTTP 文件上传接口、不接对象存储（MinIO/OSS/S3 为 Storage 边界后续替换项）；单一「首页轮播」场景，不做多位置/多分组（position/type）、不做生效时间窗、不做副标题；跳转目标为自由字符串 `link_url`（无 DB 级引用完整性）；不做点击/曝光统计、A/B 测试；不修改既有商品/分类/IAM 等模块行为。事实来源为单一 MySQL；Redis 仅会话；无 MQ；轮播图为同步读写、无异步。

## 2. 数据模型

### 2.1 `banners`（轮播图主数据）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `title` | VARCHAR(64) | 非空，trim 后非空、≤64 字符 |
| `image_url` | VARCHAR(255) | 非空，可访问地址；V1 为后端静态路由相对路径（`/storage/banners/<file>`） |
| `link_url` | VARCHAR(512) | 可空，无跳转时为 NULL |
| `sort` | INT | 非空默认 0，展示顺序（升序） |
| `status` | TINYINT | 非空默认 1（`1=启用`、`0=禁用`） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`idx_status_sort(status, sort)`（公开列表 `WHERE status=1 ORDER BY sort`）。无唯一约束（`sort` 允许重复，同值按 `id` 升序）。

### 2.2 状态映射

| DB `status` | 语义 |
| --- | --- |
| `1` | 启用（公开，出现在公开列表） |
| `0` | 禁用（下线，不出现在公开列表，后台可见） |

### 2.3 排序语义

公开列表 `ORDER BY sort ASC, id ASC`：`sort` 升序为主序，`sort` 相同按 `id` 升序兜底，保证稳定确定性排序。`sort` 允许重复，无需唯一约束。

## 3. Storage 边界（图片存储与静态服务）

- 定义独立 Storage 边界（`internal/storage`，`Storage` 接口），V1 唯一实现为 `LocalStorage`（本地目录，根目录可配置）。
- 静态服务：后端用 GoFrame 静态文件服务将 LocalStorage 的 banner 目录映射为 `/storage/banners/...` 对外提供（正确图片 `Content-Type`）。
- 3 张占位图由启动 seed/初始化写入 LocalStorage banner 目录；**V1 无 HTTP 上传接口**。
- `banners.image_url` 存 Storage 边界生成/约定的可访问相对路径；banner 业务逻辑不感知具体存储介质，后续换 MinIO/OSS/S3 时仅替换 Storage 实现、`image_url` 改为外部 URL，banner 逻辑不变。
- `image_url` 为软引用：V1 不做「路径必须命中本地存储文件」的硬校验，悬空路径表现为图片 404/不可访问，不影响 `banners` 记录一致性。

## 4. 业务不变量

- INV-001（公开可见性与排序）：公开 `GET /banners` 仅返回 `status=1` 项，按 `sort` 升序、同值按 `id` 升序；禁用项即时不出现在公开列表。
- INV-002（权限边界）：后台写操作需 `AdminAuth` + 对应权限 code（`IsSuper` 放行）；未认证 401、无权限 403 且无任何 DB 写入。
- INV-003（数据完整性）：`title` 非空且 ≤64、`image_url` 非空且 ≤255、`link_url` 可空但 ≤512、`status ∈ {0,1}`；非法输入 400 且无写入。
- INV-004（删除语义）：删除成功后该轮播图不再出现在公开列表与后台查询中（仅删 DB 记录，不删本地图片文件）。

## 5. 一致性模型与失败语义

- 事实来源：单一 MySQL `banners`；图片文件位于本地文件系统（LocalStorage），不参与 DB 事务。Redis 仅会话，不参与轮播图；无 MQ、无异步。
- 创建成功 = 单条 `INSERT banners`。
- 更新 = 先按 `id` 查存在性（不存在 → 14001 404、无写入），再执行 `UPDATE`；不依据 `RowsAffected` 判断存在性（目标存在但提交值无变化时为幂等成功）；更新后再次查询兜底并发删除（已消失 → 14001 404）。
- 删除 = 条件删除并核对 `RowsAffected`（`RowsAffected=0` → 14001 404、无写入）。
- 失败语义：未认证 401、无权限 403、不存在 404（14001）、非法输入 400（14002），均无写入；DB 技术错误 → 1000（500），不泄漏底层细节。
- 无跨表/跨系统事务、无幂等键、无并发窗口（CRUD 为单表原子操作）；删除 banner 不触达文件系统。

## 6. 安全与权限边界

- 前台公开接口（无 token）：`GET /banners`（仅启用项）。
- 后台查询（`AdminAuth`，无读权限，与商品/库存查询一致）：`GET /admin/banners`（全部状态）、`GET /admin/banners/:id`（详情）。
- 后台写（`AdminAuth` + `RequirePermission`）：`POST /admin/banners`（`banner:create`）、`PUT /admin/banners/:id`（`banner:update`）、`DELETE /admin/banners/:id`（`banner:delete`）。
- 权限 code：`banner:create`、`banner:update`、`banner:delete`（seed 登记 `internal/boot/seed.go`）。
- 身份信任：后台写仅管理员（含超管 `IsSuper` 放行），经 `RequirePermission`；前台公开接口无需身份。

## 7. 错误码域（14000-14999）

| code | 语义 | HTTP |
| --- | --- | --- |
| 14001 | BANNER_NOT_FOUND（轮播图不存在） | 404 |
| 14002 | BANNER_INVALID_INPUT（标题为空/超长、图片为空/超长、跳转超长、状态非法） | 400 |

复用：`1002`（401）、`1003`（403）、`1001`（参数格式错误兜底）、`1000`（500）。

## 8. 跨模块关系

- `banners` 为独立表，软引用无 FK；跳转目标 `link_url` 为自由字符串，不引用商品/分类等既有模块。
- 建表经 golang-migrate 迁移（`20261001000014_banners.up.sql`），见 `migration.md`。
- 权限 code `banner:create/update/delete` 经 `internal/boot/seed.go` 登记，授权模型见 `rbac.md`。
- 错误码域 `14000-14999` 的编码模型与分配规则见 `error-codes.md`，分配状态以 `.agent/registry/error-codes.md` 为权威。
- Storage 边界为跨模块可复用基础设施（`internal/storage`），后续 MinIO/OSS/S3 替换仅改 Storage 实现，不影响 banner 逻辑。

## 9. Deferred / 已知留白

- HTTP 文件上传接口（V1 无上传，图片由 seed/部署写入本地存储目录）。
- 对象存储（MinIO/OSS/S3）与 CDN、图片处理（裁剪/压缩/水印）。
- 多位置/多分组轮播（position/type）、生效时间窗、副标题。
- 轮播图点击统计、曝光统计、A/B 测试。
- `image_url` 无「路径命中本地存储文件」硬校验，悬空路径返回图片 404；如需校验或绝对 URL，属后续 Contract 修订。
