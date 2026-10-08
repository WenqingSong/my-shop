# Technical Contract

## Decision Status
APPROVED

## Problem

交付后端「文件上传 / 对象存储（七牛云）V1」能力：客户端经后端接口取得七牛云上传凭证后，可将文件直传至七牛并获得可公开访问的 URL。上传受身份/权限、文件类型与大小约束，错误可稳定区分，凭据经配置安全注入。

本任务已有 APPROVED Contract（D1/D2/D3/D4），Coder 已实现（C1=`6890d9d`）并经 Cleaner 审查（`CHANGES_REQUIRED`）。本次为 **Contract Revision（第二轮）**，由 `owner_update`（2026-10-08）触发，包含两处需重新收敛的设计变化：

1. **Secret 生命周期明确化**：`.env` 为未跟踪本地运行文件、非 Git artifact，真实 `QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY` 仅存 Owner 本地 `.env`，跨 Agent / Session / worktree / 容器不传播；缺凭据只能 NOT_VERIFIED（在 D4 基础上的明确化）。
2. **启动依赖语义反转（D2，2026-10-08）**：正常 `serve` 启动时七牛云为 **required dependency**——`access_key`/`secret_key`/`bucket`/`domain`/`region` 等配置缺失/非法，或对**指定 bucket** 的真实只读可用性检查失败，进程启动失败（fail-fast）；`17002` 仅作为运行期防御性守卫。**这反转了 D4（2026-10-07）「bucket/domain/AK/SK 签发时懒校验（17002）、启动仅校验 region/ttl/大小/白名单」的决定。**

本 Contract 保留既有 Owner 决定（D1/D2/D3/D4 中仍有效的部分），重新建立一致约束，已由 Owner 确认（2026-10-08）。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2.10.3，模块 `cnb.cool/go-cloud-devops/my-shop`，分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问 `g.DB().Model()`，无 `dao`/`model` 层。
- VERIFIED：认证中间件现成——前台 `middleware.Auth` 注入 `Principal{UserID,Sid}`；后台 `middleware.AdminAuth` + `RequirePermission(code)`（`IsSuper` 放行、fail-closed）。前台公开路由挂 `routes_frontend.go`，后台写路由挂 `routes_admin.go` 的 `require(code)` 分组；上传双端点已注册：`GET /qiniu/upload/token`（`Auth`）与 `GET /admin/qiniu/upload/token`（`AdminAuth`，不叠加独立权限 code）。
- VERIFIED：上传实现已落地（C1=`6890d9d`）：`api/upload/v1`、`internal/controller/upload`、`internal/logic/upload`（`ValidateConfig`/`IssueToken`）、`internal/service/upload.go`；错误码 17001/17002/17003 已在 `internal/codes/codes.go` 落地。
- VERIFIED（当前校验时机，与 D4 一致、但 **将被 D2 反转**）：`internal/logic/upload/upload.go` 中 `ValidateConfig(ctx)` 仅调 `validateStructural()`（region/ttl/大小/白名单，启动 fail-fast）；`bucket`/`domain`/`AK`/`SK` 的缺失校验在 `IssueToken` 内的 `validateCredentials()`（签发时返回 17002）。`internal/cmd/cmd.go` 的 `serve()` 在 `boot.Bootstrap(ctx)` 之后调 `service.Upload().ValidateConfig(ctx)`。这一当前行为即 Cleaner Finding `CLEAN-001` 所指的「bucket/domain 懒校验」，现按新 D2 改为「启动 fail-fast」。
- VERIFIED（测试装配）：`internal/cmd` 的集成测试（`identity_isolation_test.go` 的 `setupIsolationServer`、`routes_test.go` 的 `collectRoutes`）直接调 `boot.Bootstrap(ctx)` + `migrations.Up(ctx)` 并挂载路由，**不经过 `serve()`**。因此把启动 fail-fast 放在 `serve()` 入口（而非 `boot.Bootstrap()`）不会影响既有集成测试——它们在仅有 MySQL/Redis、无七牛凭据时仍可运行。
- VERIFIED（七牛 SDK 能力，`qiniu/go-sdk/v7 v7.29.0`）：
  - `BucketManager.Buckets(shared)` → 列出**整个账户全部 bucket**（D2 明确禁止使用）。
  - `BucketManager.GetBucketInfo(bucketName)` → `POST /v2/bucketInfo?bucket=<name>`，**指定 bucket** 的元信息只读查询（返回 Zone/Region/Host/Private 等），bucket 不存在或凭据无权限时报错。这是满足「指定 bucket 最小权限只读验证」的候选。
  - `BucketManager.ListFiles(bucket, prefix, "", "", 1)` → `POST /list`（RSF），指定 bucket 的对象列表只读，需 `rsf://bucket` 列表权限。
  - `BucketManager.Stat(bucket, key)` → 依赖已知 key，不适用于「bucket 是否存在」检查。
- VERIFIED（Secret 生命周期，`0116621` 落地）：`.env.example` 已提交（仅变量名与安全占位，含 `QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY`/`QINIU_BUCKET`/`QINIU_DOMAIN`/`QINIU_REGION`/`QINIU_TOKEN_TTL`/`QINIU_MAX_FILE_SIZE`）；`.gitignore` 已忽略 `.env`/`.env.local`/`.env.*.local` 且不误伤 `.env.example`（`git check-ignore .env` 命中）；`manifest/config/config.yaml` 的 `qiniu.access_key`/`secret_key`/`bucket`/`domain` 均为空串；`scripts/lib.sh` 的 `_load_env_file()` 加载根目录 `.env`（已导出环境变量优先，无 dotenv）；README 已说明 `.env` 边界。当前 working tree 仅 `contract.md` 未提交修改，无 `.env` 文件。
- VERIFIED（全局资源）：错误码域 `17000-17999` 已在 `origin/develop` Registry 以 owner=`object-storage-upload` RESERVED（域序 17）；V1 不落库、无 migration。`state.yaml` 记录 `resources.reservations.error_code_domain=[17000-17999]`，`contract.target=0116621`。
- UNKNOWN：真实七牛凭据当前不可得，AC-002（4 段真实端到端）与 AC-010（启动真实可用性检查失败路径）需 Owner 在运行环境准备真实 `QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY`/`QINIU_BUCKET`/`QINIU_DOMAIN` 后才能由 Deliverer 验证，否则 NOT_VERIFIED。

## Recommendation

RECOMMENDATION：**在 D1/D2/D3/D4（Secret 部分）基础上，将七牛配置校验统一收敛为「启动 fail-fast + 运行期防御性守卫」两层，启动真实可用性检查用 SDK 的 `BucketManager.GetBucketInfo(bucket)`（指定 bucket 最小权限只读），并把 fail-fast 作用点限定在 `serve()` 入口、不进入 `boot.Bootstrap()`。**

具体要点：

1. **启动校验（fail-fast，仅 `serve()` 入口触发）**：`service.Upload().ValidateConfig(ctx)`（或等价启动就绪方法）依次执行：
   - 结构校验：`region` 合法、`token_ttl>0`、`max_file_size>0`、扩展名/MIME 白名单非空（沿用现有 `validateStructural`）；
   - 凭据/桶/域名存在性校验：`access_key`/`secret_key`/`bucket`/`domain` 非空（从现有 `validateCredentials` 提升到启动）；
   - 真实可用性检查：`BucketManager.GetBucketInfo(bucket)`，对**指定 bucket** 做最小权限只读验证，带超时上限；任何一步失败返回错误 → `serve()` 返回错误 → 进程非零退出。
   - 错误信息只指认缺失/非法的配置字段名（如 `qiniu.access_key 未配置`），**绝不包含凭据值、内部路径或堆栈**。

2. **运行期守卫（17002）**：`IssueToken` 仍保留 `validateCredentials()`（AK/SK/bucket/domain 非空）作为运行期防御性守卫，返回稳定 `17002`，保护「异常/绕过启动检查」的调用路径；不承担启动错误表达（D2）。

3. **最小权限只读验证方式（Q1 推荐）**：选用 `BucketManager.GetBucketInfo(bucket)`。它只针对**指定 bucket**（`POST /v2/bucketInfo?bucket=<name>`），不触发 `Buckets()` 的全账户 bucket 列举，符合 D2 约束；验证「bucket 存在 + AK/SK 可访问」。备选 `ListFiles(bucket, "", "", "", 1)`（需 `rsf://` 列表权限）仅在 Owner 希望把所需权限进一步收窄到「对象列表读」时使用。**推荐 `GetBucketInfo` 为默认**。

4. **fail-fast 作用点与测试边界（Q2）**：启动校验只挂在 `serve()`（`boot.Bootstrap` 之后、`s.Run()` 之前），**不进入 `boot.Bootstrap()`**。因 `internal/cmd` 集成测试经 `boot.Bootstrap()` 直接装配（不经过 `serve()`），故 `go build ./...` / `go test ./...` 不依赖七牛凭据即可通过；仅「结构 + 凭据存在性」fail-fast 可无网络直接单测，真实可用性检查的失败路径由 Deliverer 在真实凭据下验证（缺凭据 NOT_VERIFIED）。为使 fail-fast 接线可测，真实可用性检查做成一个可注入的薄函数（单测注入假实现验证「检查失败 → 返回错误」的接线，真实网络验证留 E2E）。

关键取舍：**把七牛从「可选的签发依赖」提升为「服务启动的 required dependency」，换来配置/凭据错误在启动时立即暴露、不延迟到请求时才发现（生产更早 fail、更可运维），代价是无七牛凭据的本地/CI 环境无法再通过 `serve` 启动服务**（构建与单测不受影响；集成测试因绕过 `serve()` 也不受影响）。这是 Owner D2 已明确接受的取舍，本 Contract 只将其落成一致约束。

## Selected Design

Owner 已确认（D1/D2/D3/D4 2026-10-07；D5/D6 2026-10-08）：

- D1（上传主体）：后台管理员（`AdminAuth`）+ 前台登录用户（`Auth`）两个端点，共用同一签发核心；签发凭证为低敏感操作，不新增独立权限 code。
- D2（落库）：V1 不落库，仅无状态签发上传凭证，不新增 migration。
- D3（七牛依赖）：使用官方 `qiniu/go-sdk`，不自行实现上传 Token 签名协议。
- D4（Secret 配置模型，Secret 部分维持有效）：`config.yaml` 存非敏感默认值 + 环境变量注入真实凭据；`.env.example` 可提交、`.env` 本地真实值 gitignore 不提交；不引入 dotenv，沿用 `g.Cfg().GetEffective` 环境变量覆盖；`access_key`/`secret_key` 仅环境变量（config.yaml 空值），`bucket`/`domain` 非敏感可写 config.yaml 或 env 覆盖。**D4 中「bucket/domain/AK/SK 签发时懒校验（17002）」一条被本次 D5 反转，其余维持。**
- D5（启动依赖 fail-fast，2026-10-08，Owner 已确认）：反转 D4 的懒校验——`serve` 启动对 `access_key`/`secret_key`/`bucket`/`domain`/`region` 全部 fail-fast，并对指定 bucket 执行 `GetBucketInfo` 真实只读可用性检查，失败即进程非零退出；`17002` 仅运行期守卫。
- D6（E2E 载体与 Secret 生命周期，2026-10-08，Owner 已确认）：真实后端签发链路（读环境变量 → 签发 token → 直传真实最小 PNG（1×1，几十字节）→ `final_url` HTTP 200 且 `Content-Type=image/png` → 用返回的精确 key 经 SDK 删除测试对象）；不修改生产 key 规则、不要求 `e2e/` 专用前缀；`.env` 为非 Git artifact、跨 Agent/worktree/容器不传播，缺凭据 NOT_VERIFIED。

其余设计（直传模型、key 预生成、类型/大小白名单、错误码域 17000-17999、配置模型）按本 Contract 执行。

## Interfaces and Data

### API 契约（`api/upload/v1`，已实现，不变）

- `GET /admin/qiniu/upload/token`（`AdminAuth`）、`GET /qiniu/upload/token`（`Auth`）。
- 请求（query，均可选）：`filename`（含扩展名）、`content_type`（声明 MIME）。
- 响应 `{code:0,message:"OK",data:{token, key, upload_url, domain, final_url, expires_at}}`，字段语义不变（`expires_at` = 签发时间 + ttl）。

### 配置模型（`manifest/config/config.yaml` 的 `qiniu` 段，不变）

| 字段 | 环境变量 | 类型/默认 | 说明 |
| --- | --- | --- | --- |
| `access_key` | `QINIU_ACCESS_KEY` | string，config.yaml 空值 | 凭据标识，非机密，仅环境变量注入（与 secret_key 成对） |
| `secret_key` | `QINIU_SECRET_KEY` | string，config.yaml 空值 | 机密，仅环境变量、不进日志/响应/仓库 |
| `bucket` | `QINIU_BUCKET` | string，无默认 | 非敏感，可写 config.yaml 或环境变量覆盖 |
| `domain` | `QINIU_DOMAIN` | string，无默认 | 非敏感，可写 config.yaml 或环境变量覆盖 |
| `region` | `QINIU_REGION` | string，默认 `z2` | 七牛区域，映射 upload host |
| `token_ttl` | `QINIU_TOKEN_TTL` | int，默认 3600 | 必须 > 0 |
| `max_file_size` | `QINIU_MAX_FILE_SIZE` | int，默认 10485760 | 必须 > 0 |
| `allowed_extensions` | `QINIU_ALLOWED_EXTENSIONS` | []string | 扩展名预校验白名单 |
| `allowed_mime_types` | `QINIU_ALLOWED_MIME_TYPES` | []string | 固化进 token `mimeLimit` |

校验语义（D5 修订后）：启动 fail-fast 校验以上全部（结构 + 存在性 + `GetBucketInfo` 真实可用性）；`access_key`/`secret_key`/`bucket`/`domain` 缺失/非法在启动即失败，不再等到签发时。

### Secret 注入模型（项目级统一，D4 + D6）

- `.env.example`：变量名 + 安全占位，可提交；`.env`：本地真实值，gitignore 忽略、绝不提交。
- 本地：Owner `cp .env.example .env` 并填真实 `QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY`，经 `scripts/lib.sh` 加载；CI/生产：平台 Secret / 环境变量注入。不引入 dotenv。
- `.env` 为未跟踪本地文件、非 Git artifact，不随 Git/分支/worktree/容器传播；每个运行环境（含 Deliverer 临时 worktree/容器）须自行准备，缺凭据只能 NOT_VERIFIED。
- 真实 AK/SK 只存在于 Owner 本地 `.env`，不进入任何受跟踪提交物（config.yaml/源码/测试/日志/文档/脚本）。

### 全局资源预留（不变）

- `error_code_domain`：区间 **`17000-17999`**，owner = `object-storage-upload`（`origin/develop` 已 RESERVED）。域内编号：
  - `17001` UPLOAD_INVALID_INPUT（扩展名/MIME/参数非法）→ 400
  - `17002` UPLOAD_CONFIG_INVALID（运行期七牛配置缺失/非法防御性守卫）→ 500
  - `17003` UPLOAD_TOKEN_FAILED（签发凭证失败）→ 500
- `migration_version`：不预留（V1 不落库）。

## Business Invariants

- INV-001（授权边界）：未认证请求访问任一签发接口返回 401，token type 不符返回 403，均不签发凭证、不产生上传与 DB 写入。
- INV-002（文件边界）：签发请求声明的扩展名/MIME 不在白名单、或大小超上限，返回 400（17001），不签发；最终文件级强制由 token 的 `mimeLimit`/`fsizeLimit` 由七牛服务端执行。
- INV-003（凭据安全）：`access_key`/`secret_key` 仅经环境变量注入、config.yaml 保持空值、不提交仓库、不进日志/响应；启动 fail-fast 或运行期 17002 的任何错误信息均不泄漏凭据值、内部路径或堆栈。
- INV-004（key 唯一可控）：上传 key 由后端预生成（`upload/{yyyyMMdd}/{随机}.{ext}`）并写入 token scope，客户端不能任意指定。
- INV-005（Secret 不落仓库/跨环境不传播）：真实 AK/SK 只存在于运行环境（本地 `.env` / 平台 Secret），不进入任何受跟踪提交物；`.env` 为未跟踪本地文件，不随 Git/分支/worktree/容器传播；构建与单测不以「`.env` 存在」为通过前提。
- INV-006（启动 fail-fast）：正常 `serve` 启动时，七牛 required 配置（`access_key`/`secret_key`/`bucket`/`domain`/`region`）缺失/非法，或对指定 bucket 的 `GetBucketInfo` 真实只读可用性检查失败 → 进程非零退出（fail-fast，不静默降级）；`17002` 仅在运行期签发路径作为防御性守卫，不承担启动错误表达。

## Failure and Consistency Semantics

- 事实来源：单一配置源（`manifest/config/config.yaml` 非敏感结构 + 环境变量凭据叠加），无 DB 写入、无 Redis 参与、无 MQ。直传发生在七牛侧，后端不感知上传是否最终成功。
- **启动失败语义（D5）**：`serve()` 在 `boot.Bootstrap()` 之后执行七牛启动校验，任一失败返回错误 → 进程非零退出；错误指认缺失/非法字段名，不泄漏凭据值。真实可用性检查有超时上限，七牛不可达/无权限/bucket 不存在均视为启动失败。
- 签发成功 = 返回一份受 scope 约束、带有效期（ttl）的 upload token 与对应 key/URL；不代表文件已上传。
- 运行期失败语义：未认证 401、type 不符 403、非法扩展名/类型/大小 400（17001）、运行期配置缺失/非法 500（17002，防御性守卫）、签名/签发内部错误 500（17003）；均不产生上传与写入，不泄漏凭据。
- 无跨系统事务、无并发写入、无幂等键、无重试语义（每次签发独立、幂等生成新 token + 新 key）。

## Allowed / Forbidden Changes

- 允许：调整 `internal/logic/upload` 的校验（把 `bucket`/`domain`/`AK`/`SK` 存在性 + `GetBucketInfo` 真实可用性并入启动校验，`IssueToken` 保留运行期 17002 守卫）；在 `internal/cmd` 的 `serve()` 接线启动校验（不进入 `boot.Bootstrap()`）；`internal/service` 的 `IUpload` 接口同步（如新增启动就绪方法或调整 `ValidateConfig` 语义）；更新 `internal/cmd`/`internal/logic/upload` 对应测试与 `docs/design/storage.md`；`manifest/config/config.yaml` 的 `qiniu` 段注释同步为「启动 required」。
- 禁止：修改既有模块（商品/分类/文章/轮播图/IAM 等）行为；改造前端模板工程 `frotend_web`/`frotend_manage`；新增 DB 表或 migration；硬编码任何七牛凭据或生产地址；提交 `.env` 或含真实 AK/SK 的文件；引入 dotenv；改变既有错误码取值、HTTP 状态映射、message 或统一响应 `{code,message,data}` 格式；在启动可用性检查中使用 `BucketManager.Buckets()`（全账户列举）。

## Verification Requirements

- INV-006 → 启动验证：无/非法七牛配置时 `serve` 以非零退出码失败，错误不含凭据；提供有效配置但真实依赖验证失败（bucket 不存在/无权限/网络不可达）时同样启动失败；启动检查使用 `GetBucketInfo`（指定 bucket，非 `Buckets()`）。
- INV-001 → 启动服务：无 token 请求 → 401；user token 访问 `/admin/qiniu/upload/token` 或 admin token 访问 `/qiniu/upload/token` → 403；断言不签发、无 DB 写入。
- INV-002 → 启动服务：`filename=evil.exe` / 非法 `content_type` → 400（17001），不签发；断言 token scope 含 `mimeLimit`/`fsizeLimit` 与配置一致。
- INV-003/005 → 静态核验 + 运行：`git diff`/`git ls-files` 范围内无真实 AK/SK；启动 fail-fast 与运行期 17002 均不泄漏凭据；`git check-ignore -v .env` 命中、`.env.example` 未命中。
- INV-004 → 启动服务：连续两次签发返回不同 key；key 符合 `upload/{yyyyMMdd}/{随机}.{ext}` 且写入 token scope。
- AC-001 → 启动服务：已认证客户端请求签发接口，断言 token/key/upload_url/domain/final_url/expires_at 齐全，token 过期时间 = 签发时间 + ttl。
- AC-002 → 需真实七牛凭据：4 段链路（读环境变量 → 签发 token → 直传真实最小 PNG（1×1）→ `final_url` HTTP 200 且 `Content-Type=image/png` → 用返回 key 经 SDK 删除测试对象）；无凭据 NOT_VERIFIED。
- AC-006 → `go build ./...`、`go test ./...` 不依赖 `.env` 存在即可通过；`go.mod` 无 dotenv。
- AC-007/008 → Registry ↔ Contract ↔ 实现三边一致（17000-17999）；`docs/design/storage.md` 与 APPROVED Contract、最终实现一致（含 Secret 边界与启动 fail-fast 语义）。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Open Risks

- 直传固有边界：后端无法在签发时强制校验真实文件内容；`mimeLimit` 依赖七牛对客户端上报 Content-Type 的执行，客户端可伪造声明。
- 启动真实可用性检查引入对七牛的运行时网络依赖：七牛不可达会导致服务无法启动（D2 接受的代价）；`GetBucketInfo` 内部用 `context.Background()`、不接受外部 ctx，超时需经 SDK client 配置或外层 goroutine 约束。
- `GetBucketInfo` 所需权限为「指定 bucket 的 info 读权限」；若 Owner 希望收窄到「对象列表读」，改用 `ListFiles`（`rsf://`）会引入列表权限，二者均为 bucket 级、均满足「不列举全账户」的约束。
- `region` 与 bucket 实际所在 zone 的映射仅在签发 `upload_url` 时使用，本 V1 不做「配置 region 与 bucket 真实 zone 交叉校验」，不匹配可能导致直传失败（属后续可选改进）。
- 真实七牛凭据缺失：AC-002/AC-010 的失败路径无法自验，需 Owner 注入真实凭据后由 Deliverer 验证，否则 NOT_VERIFIED。
- 引入官方 `qiniu/go-sdk` 增加第三方依赖（D3 明确接受的代价）。

## Owner Decision Record

- 2026-10-07：Owner 确认 D1（上传主体 = 后台管理员 + 前台登录用户）、D2（V1 不落库、仅无状态签发）、D3（改用官方 `qiniu/go-sdk`，不自行实现上传 Token 签名协议）。其余设计按 Contract 执行。
- 2026-10-07（Contract Revision D4）：Owner 确认项目级 Secret 配置方案——新增 `.env.example`、本地真实凭据放 `.env`（gitignore，经 `scripts/lib.sh`/Docker Compose 加载，不引入 dotenv）、CI/生产经平台 Secret/环境变量注入；`qiniu.access_key`/`qiniu.secret_key` 保持 config.yaml 空值；`bucket`/`domain` 非敏感可写 config.yaml 并允许环境变量覆盖。**同时确认 `bucket`/`domain` 与 `AK/SK` 归入「签发时校验（17002）」，启动仅 fail-fast 校验 region/ttl/大小/白名单。**
- 2026-10-08（Contract Revision，Owner 已确认 D5/D6）：保留 D1-D3 与 D4 的 Secret 部分；**D5 反转 D4 的懒校验**——serve 启动对全部七牛 required 配置 + 指定 bucket 的最小权限只读可用性检查 fail-fast，`17002` 仅运行期守卫；D6 固化 E2E 载体（真实后端签发 + 真实最小 PNG（1×1，几十字节）+ 用返回精确 key 经 SDK 删除测试对象，不修改生产 key 规则、不要求 `e2e/` 前缀）与 `.env` 跨环境不传播语义。最小权限只读检查经调查采用 `BucketManager.GetBucketInfo(bucket)`（`POST /v2/bucketInfo?bucket=`，仅指定 bucket、非全账户列举），满足 Owner「不锁死 `Buckets()`」约束。
