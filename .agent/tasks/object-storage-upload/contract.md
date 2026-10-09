# Technical Contract

## Decision Status
APPROVED

## Problem

交付后端「文件上传 / 对象存储（七牛云）V1」能力：客户端经后端接口取得七牛云上传凭证后，可将文件直传至七牛并获得可公开访问的 URL。上传受身份/权限、文件类型与大小约束，错误可稳定区分，凭据经配置安全注入。

本任务经历三轮 Contract Revision：
- 第一轮（D1-D4，2026-10-07）：上传主体、不落库、官方 SDK、Secret 配置模型。
- 第二轮（D5-D6，2026-10-08）：启动 fail-fast 反转 D4 懒校验、最小权限只读验证、E2E 载体与 `.env` 生命周期。
- 第三轮（D7-D9，2026-10-09）：本地开发环境两阶段初始化（`make init`/`make up`）+ 独立真实 E2E 入口（`make test-storage`），本次 Owner 已确认。

本 Contract 保留既有决定（D1-D6），并将第三轮新增的「两阶段初始化 + 真实 HTTP E2E」落成一致约束，已由 Owner 确认（2026-10-09）。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2，模块 `cnb.cool/go-cloud-devops/my-shop`，分层 `api/<module>/v1` → `internal/controller` → `internal/service` → `internal/logic`，数据访问 `g.DB().Model()`，无 `dao`/`model` 层。
- VERIFIED：认证与登录链路现成——前台 `POST /register`（公开）、`POST /login`（公开，返回 `access_token`）；后台 `POST /admin/login`（公开）。上传双端点已注册：`GET /qiniu/upload/token`（`Auth`，前台登录用户）与 `GET /admin/qiniu/upload/token`（`AdminAuth`，不叠加独立权限 code）。
- VERIFIED（当前实现，D5 已落地，commit `f52d044`）：`internal/logic/upload/upload.go` 的 `ValidateConfig` = `validateStructural`（region/ttl/大小/白名单）→ `validateRequired`（AK/SK/bucket/domain 存在性）→ `checkBucketAvailable`（`BucketManager.GetBucketInfo(bucket)`，指定 bucket 最小权限只读，非 `Buckets()`，注入带 `Timeout=10s` 的 `http.Client` 约束超时）。`internal/cmd/cmd.go` 的 `serve()` 在 `boot.Bootstrap(ctx)` 之后调用 `service.Upload().ValidateConfig(ctx)`，任一失败返回错误 → 进程非零退出。`IssueToken` 内保留 `validateCredentials` 作为运行期 17002 防御性守卫。
- VERIFIED（可测试装配）：`sUpload.checkBucket` 为可注入薄函数（`New()` 默认 `checkBucketAvailable`）；`internal/cmd` 集成测试（`setupIsolationServer`/`isoFrontendLogin`/`isoAdminLogin` 等，见 `identity_isolation_test.go`）经 `boot.Bootstrap(ctx)` 直接装配 + 挂载真实路由、**不经过 `serve()`**，故七牛启动 fail-fast 不影响既有集成测试（仅需 MySQL/Redis、无需七牛凭据）。该测试 harness 已可复用做真实 HTTP E2E（登录 + 签发走真实 HTTP 路由）。
- VERIFIED（脚本与构建现状）：当前 `Makefile` 目标为 `help/bootstrap/up/down/restart/status/logs/health/test/clean`，**无 `init`、无 `test-storage`**；`bootstrap.sh` 现仅调用 `up.sh`；`scripts/up.sh` 流程为 `docker_compose up -d mysql redis` → `wait_for_deps` → `build_app` → `migrate_app` → `start_app`，**无七牛预检**。`scripts/lib.sh` 已有可复用能力：`_load_env_file`（加载根目录 `.env`，已导出环境变量优先）、`docker_compose`、`container_healthy`、`wait_for_deps`、`build_app`/`migrate_app`/`start_app`/`stop_app`、`app_health_url`。`internal/cmd/cmd.go` 已有 gcmd 命令结构（`Main`/`serve`/`migrate <up|force|version>`），可扩展新子命令。
- VERIFIED（七牛 SDK 能力，`qiniu/go-sdk/v7`）：`BucketManager.GetBucketInfo(bucket)`（指定 bucket 只读）、`storage.FormUploader`（直传，携带 upload token）、`BucketManager.Delete(bucket, key)`（删除测试对象）。
- VERIFIED（Secret 生命周期，已落地）：`.env.example` 已提交（七牛项已取消注释，仅变量名与安全占位、无真实值）；`.gitignore` 忽略 `.env`/`.env.local`/`.env.*.local` 且不误伤 `.env.example`；`manifest/config/config.yaml` 的 `qiniu.access_key`/`secret_key`/`bucket`/`domain` 均为空串；加载链路沿用 `g.Cfg().GetEffective` 环境变量覆盖（`QINIU_*`），无 dotenv。当前 working tree clean，无 `.env` 文件。
- VERIFIED（全局资源）：错误码域 `17000-17999` 已在 `origin/develop` Registry（`error-codes.md`）以 owner=`object-storage-upload` RESERVED（域序 17）；V1 不落库、无 migration，故本次修订**不新增**全局资源、无需新 Registry commit。
- UNKNOWN：真实七牛凭据当前不可得，AC-002/AC-010/AC-013/AC-014 需 Owner 在运行环境准备真实 `QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY`/`QINIU_BUCKET`/`QINIU_DOMAIN` 后才能验证，否则 NOT_VERIFIED。

## Recommendation

RECOMMENDATION：**在 D1-D6 基础上，本地开发环境收敛为「`make init`（第一阶段，不要求凭据、幂等）+ `make up`（第二阶段，加载 `.env` + 依赖校验 + 七牛真实 bucket 预检 + 启动）+ `make test-storage`（独立真实 HTTP E2E）」三入口；七牛预检载体为新增 Go 子命令 `my-shop qiniu check` 复用 `ValidateConfig`；E2E 用 build-tagged Go 测试走真实 HTTP 链路（登录 → 签发接口 → 上传 → 验证 → 清理），缺凭据或链路失败非零退出。**

具体要点：

1. **Q1（`make up` 七牛预检载体）→ Go 子命令 `my-shop qiniu check`**：复用 `service.Upload().ValidateConfig(ctx)`（结构 + 存在性 + `GetBucketInfo`）作为唯一校验事实源，不复制到 shell。`make up` 流程：`source lib.sh` → `docker_compose up -d mysql redis` → `wait_for_deps` → `build_app` → `my-shop qiniu check`（失败非零退出、打印不含凭据的明确原因、不启动后端）→ `migrate_app` → `start_app`。`serve()` 保留自身 `ValidateConfig`（AC-010 独立 fail-fast）。代价：`make up` 路径下预检 + serve 启动各触发一次 `GetBucketInfo`（多一次廉价只读调用），换取两入口独立不耦合。

2. **Q2（`make test-storage` 载体）→ build-tagged Go 测试（`//go:build storage_e2e`），走真实 HTTP 链路**（D8）：测试放在 `internal/cmd`，复用现有真实 HTTP 测试 harness（`setupIsolationServer` 装配真实路由 + 真实 MySQL/Redis），**不直接调用 `service.Upload().IssueToken`**。链路：`POST /register`（注册一次性用户）→ `POST /login`（取 `access_token`）→ `GET /qiniu/upload/token`（带 `Authorization: Bearer`）→ 用返回 token 经 SDK `FormUploader` 直传真实 1×1 PNG（内嵌真实 PNG 字节，非伪造 Content-Type）→ HTTP GET `final_url` 断言 200 且 `Content-Type=image/png` → 用返回 key 经 SDK `BucketManager.Delete` 删除测试对象。build tag 使该测试默认不进入 `go test ./...`（D9：普通 `go test ./...` 仍不要求真实凭据）。

3. **Q3（缺凭据与失败语义）→ 非零退出（D9）**：`make test-storage` = `scripts/test-storage.sh`：`source lib.sh` → 校验四个 `QINIU_*` 非空（缺任一 → 打印明确错误 + 退出 1）→ 确保 MySQL/Redis 就绪 → `go test -tags storage_e2e -run TestStorageE2E -v ./internal/cmd/...`（测试内凭据缺失或任一环节失败 → `t.Fatal` → 非零退出）。NOT_VERIFIED 以「非零退出 + 明确说明前置动作」表达，不以退出 0 冒充通过。

4. **`make init`（第一阶段，新增 `scripts/init.sh`）**：检查 docker/docker compose → `docker_compose up -d mysql redis` + `wait_for_deps` → 检查根目录 `.env`（不存在则 `cp .env.example .env`，已存在保留绝不覆盖）→ 输出清晰提示编辑 `.env` 填七牛 AK/SK/Bucket/Domain 并执行 `make up`。不要求七牛凭据、不启动 Go 后端、不在终端交互输入 Secret；幂等。

关键取舍：把「七牛可用性验证」扩展为「`make up` 预检 + `serve` 启动双重 fail-fast」，换来本地启动前清晰即时的配置/凭据错误反馈；E2E 走真实 HTTP 登录+签发链路（而非直接调 `IssueToken`），换来对鉴权与签发接口的真实覆盖，代价是 E2E 依赖 MySQL/Redis 与一次真实上传/删除（需真实凭据，缺凭据非零退出）。

## Selected Design

**已确认（D1-D9，Owner 已批准）：**

- D1（上传主体）：后台管理员（`AdminAuth`）+ 前台登录用户（`Auth`）两个端点，共用同一签发核心；不新增独立权限 code。
- D2（落库）：V1 不落库，仅无状态签发上传凭证，不新增 migration。
- D3（七牛依赖）：使用官方 `qiniu/go-sdk`，不自行实现上传 Token 签名协议。
- D4（Secret 配置模型）：`config.yaml` 存非敏感默认值 + 环境变量注入真实凭据；`.env.example` 可提交、`.env` 本地真实值 gitignore 不提交；不引入 dotenv；`access_key`/`secret_key` 仅环境变量，`bucket`/`domain` 非敏感可写 config.yaml 或 env 覆盖。
- D5（启动依赖 fail-fast）：`serve` 启动对 `access_key`/`secret_key`/`bucket`/`domain`/`region` 全部 fail-fast，并对指定 bucket 执行 `GetBucketInfo` 真实只读可用性检查，失败即进程非零退出；`17002` 仅运行期守卫。
- D6（E2E 载体与 Secret 生命周期）：真实后端签发链路（读环境变量 → 签发 token → 直传真实最小 PNG → `final_url` 200 + image/png → 用返回 key 删除）；`.env` 非 Git artifact、跨环境不传播，缺凭据 NOT_VERIFIED。
- D7（七牛预检载体，2026-10-09）：新增 Go 子命令 `my-shop qiniu check`，复用 `ValidateConfig`；`make up` 在 `build_app` 之后、`start_app` 之前调用其作为预检；`serve` 保留独立 fail-fast。
- D8（E2E 必须走真实 HTTP 链路，2026-10-09）：`make test-storage` 必须验证真实 HTTP 上传链路——登录鉴权 → 请求签发接口 → 上传极小合法 PNG → 验证 `final_url` HTTP 200 与 `Content-Type=image/png` → 清理测试对象；**不得仅调用 `IssueToken` 后宣称完成 E2E**。
- D9（缺凭据/失败非零退出，2026-10-09）：显式执行 `make test-storage` 时，缺少真实凭据或无法完成验证必须非零退出；普通 `go test ./...` 仍不要求真实凭据（build tag 隔离）。

其余设计（直传模型、key 预生成、类型/大小白名单、错误码域 17000-17999、配置模型、双端点 API）按本 Contract 执行。

## Interfaces and Data

### API 契约（`api/upload/v1`，已实现，不变）

- `GET /admin/qiniu/upload/token`（`AdminAuth`）、`GET /qiniu/upload/token`（`Auth`）。
- 请求（query，均可选）：`filename`（含扩展名）、`content_type`（声明 MIME）。
- 响应 `{code:0,message:"OK",data:{token, key, upload_url, domain, final_url, expires_at}}`，字段语义不变。

### 配置模型（`manifest/config/config.yaml` 的 `qiniu` 段，不变）

| 字段 | 环境变量 | 类型/默认 | 说明 |
| --- | --- | --- | --- |
| `access_key` | `QINIU_ACCESS_KEY` | string，config.yaml 空值 | 凭据标识，非机密，仅环境变量注入 |
| `secret_key` | `QINIU_SECRET_KEY` | string，config.yaml 空值 | 机密，仅环境变量、不进日志/响应/仓库 |
| `bucket` | `QINIU_BUCKET` | string，无默认 | 非敏感，可写 config.yaml 或环境变量覆盖 |
| `domain` | `QINIU_DOMAIN` | string，无默认 | 非敏感，可写 config.yaml 或环境变量覆盖 |
| `region` | `QINIU_REGION` | string，默认 `z2` | 七牛区域 |
| `token_ttl` | `QINIU_TOKEN_TTL` | int，默认 3600 | 必须 > 0 |
| `max_file_size` | `QINIU_MAX_FILE_SIZE` | int，默认 10485760 | 必须 > 0 |
| `allowed_extensions` | `QINIU_ALLOWED_EXTENSIONS` | []string | 扩展名预校验白名单 |
| `allowed_mime_types` | `QINIU_ALLOWED_MIME_TYPES` | []string | 固化进 token `mimeLimit` |

### 新增命令与脚本入口（D7/D8/D9）

- `my-shop qiniu check`：复用 `service.Upload().ValidateConfig`，失败非零退出、错误不含凭据；供 `make up` 预检调用。
- `make init` → `scripts/init.sh`（新增）；`make up` → `scripts/up.sh`（修改：插入 `qiniu check` 预检）；`make test-storage` → `scripts/test-storage.sh`（新增）。
- E2E 测试：`internal/cmd`（`//go:build storage_e2e`），复用真实 HTTP 测试 harness，`make test-storage` 用 `go test -tags storage_e2e -run TestStorageE2E` 驱动。

### Secret 注入模型（项目级统一，D4 + D6）

- `.env.example`：变量名 + 安全占位，可提交；`.env`：本地真实值，gitignore 忽略、绝不提交。
- 本地：`make init` 自动从 `.env.example` 复制生成 `.env`（不覆盖已有）→ Owner 编辑填真实值 → `make up`/`scripts/*.sh` 经 `scripts/lib.sh` 加载；CI/生产：平台 Secret / 环境变量注入。不引入 dotenv。
- 真实 AK/SK 只存在于运行环境（本地 `.env` / 平台 Secret），不进入任何受跟踪提交物；`.env` 不随 Git/分支/worktree/容器传播。

### 全局资源预留（不变）

- `error_code_domain`：区间 **`17000-17999`**，owner=`object-storage-upload`（`origin/develop` 已 RESERVED）。域内编号：`17001` UPLOAD_INVALID_INPUT（400）、`17002` UPLOAD_CONFIG_INVALID（运行期守卫，500）、`17003` UPLOAD_TOKEN_FAILED（500）。
- `migration_version`：不预留（V1 不落库）。本次修订不新增错误码、不新增 migration。

## Business Invariants

- INV-001（授权边界）：未认证 401、token type 不符 403，不签发、不产生上传与 DB 写入。
- INV-002（文件边界）：声明扩展名/MIME 不在白名单、或大小超上限 → 400（17001），不签发；最终文件级强制由 token 的 `mimeLimit`/`fsizeLimit` 由七牛服务端执行。
- INV-003（凭据安全）：`secret_key` 不落默认值、不进日志/响应/仓库，缺失/非法返回稳定 17002；启动 fail-fast 与运行期错误均不泄漏凭据值、内部路径或堆栈。
- INV-004（key 唯一可控）：key 由后端预生成（`upload/{yyyyMMdd}/{随机}.{ext}`）并写入 token scope，客户端不可任意指定。
- INV-005（Secret 不落仓库/跨环境不传播）：真实 AK/SK 只存在于运行环境，不进入任何受跟踪提交物；`.env` 为未跟踪本地文件，不随 Git/分支/worktree/容器传播；构建与单测不以「`.env` 存在」为通过前提。
- INV-006（启动 fail-fast）：正常 `serve` 启动时，七牛 required 配置缺失/非法，或对指定 bucket 的 `GetBucketInfo` 真实只读可用性检查失败 → 进程非零退出；`17002` 仅运行期守卫。
- INV-007（两阶段初始化幂等与不破坏）：`make init` 可重复执行；不覆盖已有 `.env`、不破坏已运行的容器与已有数据；本阶段不要求七牛凭据、不启动 Go 后端、不在终端交互输入 Secret。
- INV-008（make up 启动前置校验）：`make up` 在启动后端前完成依赖（MySQL/Redis）+ 七牛配置 + 真实 bucket 可用性校验，任一失败非零退出、不启动后端、错误不含凭据。
- INV-009（test-storage 真实 HTTP 链路与清理）：`make test-storage` 必须走真实 HTTP 链路（登录鉴权 → 请求签发接口 → 上传真实最小 PNG → 验证 `final_url` 200 + image/png → 删除测试对象），不直接调用 `IssueToken`；上传的测试对象必须删除不留残留；缺真实凭据或任一环节失败/无法完成验证 → 非零退出，不用 Mock/假凭据冒充通过。

## Failure and Consistency Semantics

- 事实来源：单一配置源（`manifest/config/config.yaml` 非敏感结构 + 环境变量凭据叠加），无 DB 写入（上传不落库）、无 Redis 参与（仅会话）、无 MQ。直传发生在七牛侧，后端不感知上传是否最终成功。
- **启动失败语义（D5）**：`serve()` 在 `boot.Bootstrap()` 之后执行七牛启动校验，任一失败返回错误 → 进程非零退出；错误指认缺失/非法字段名，不泄漏凭据值；真实可用性检查有超时上限，七牛不可达/无权限/bucket 不存在均视为启动失败。
- **`make up` 预检失败语义（D7）**：`qiniu check` 预检失败 → `make up` 非零退出、不启动后端、打印不含凭据的明确原因；与 `serve` 自身 fail-fast 双保险（`make up` 路径下最多两次 `GetBucketInfo`）。
- **`make test-storage` 语义（D8/D9）**：缺真实凭据 → 非零退出 + 明确说明前置动作（NOT_VERIFIED 以非零退出表达，不以退出 0 冒充通过）；有凭据但登录/签发/直传/`final_url` 校验/删除任一环节失败 → 非零退出；删除步骤尽力执行，目标是清理本次测试对象。
- 签发成功 = 返回一份受 scope 约束、带有效期（ttl）的 upload token 与对应 key/URL；不代表文件已上传。
- 运行期失败语义：未认证 401、type 不符 403、非法扩展名/类型/大小 400（17001）、配置缺失/非法 500（17002，防御性守卫）、签发内部错误 500（17003）；均不产生上传与写入，不泄漏凭据。
- 无跨系统事务、无并发写入、无幂等键、无重试语义（每次签发独立生成新 token + 新 key）。

## Allowed / Forbidden Changes

- 允许：在 `internal/cmd` 新增 `qiniu check` 子命令（复用 `service.Upload().ValidateConfig`）；修改 `scripts/up.sh` 插入 `qiniu check` 预检；新增 `scripts/init.sh`、`scripts/test-storage.sh`；`Makefile` 新增 `init`/`test-storage` 目标；新增 build-tagged E2E 测试（`//go:build storage_e2e`，`internal/cmd`，走真实 HTTP 链路）；同步更新 `docs/design/storage.md` 与 `README.md` 说明两阶段初始化、`make test-storage` 与 Secret 边界；`manifest/config/config.yaml` 的 `qiniu` 段注释同步为「启动 required」。
- 禁止：修改既有模块（商品/分类/文章/轮播图/IAM 等）行为；改造前端模板工程 `frotend_web`/`frotend_manage`；新增 DB 表或 migration；硬编码任何七牛凭据或生产地址；提交 `.env` 或含真实 AK/SK 的文件；引入 dotenv；在启动可用性检查中使用 `BucketManager.Buckets()`（全账户列举）；改变既有错误码取值、HTTP 状态映射、message 或统一响应 `{code,message,data}` 格式；让 `make init`/`make up` 在终端交互输入真实 Secret；让 `go build`/`go test ./...` 依赖 `.env` 或真实凭据存在；**在 `make test-storage` 中仅调用 `IssueToken` 冒充完成 E2E（必须走真实 HTTP 登录 + 签发接口）**；缺凭据时以退出 0 冒充通过。

## Verification Requirements

- INV-006 → 启动验证：无/非法七牛配置时 `serve` 非零退出、错误不含凭据；配置有效但真实依赖验证失败同样启动失败；使用 `GetBucketInfo`（非 `Buckets()`）。
- INV-007 → AC-011/AC-012：全新工作区运行 `make init` 断言 Docker 检查通过、容器启动、`.env` 由 `.env.example` 复制生成、输出编辑提示、不启动后端、无交互输入；连续两次 `make init` 断言第二次不覆盖 `.env`、容器与数据不破坏。
- INV-008 → AC-013：缺任一七牛配置时 `make up` 非零退出并明确提示；配全但 bucket 不可用时同样非零退出；全部通过才启动后端。
- INV-009 → AC-014：`make test-storage` 走真实 HTTP 链路（注册/登录 → 签发接口 → 直传 → 验证 → 删除）并清理测试对象；缺凭据或任一环节失败 → 非零退出。
- INV-001/002/003/004/005 → 沿用既有验证（鉴权边界、文件边界、凭据安全、key 唯一、Secret 不落仓库）。
- AC-001 → 已认证客户端签发接口返回 token/key/upload_url/domain/final_url/expires_at 齐全，token scope 与配置一致。
- AC-002 → 需真实七牛凭据：真实后端签发链路（读环境变量 → 签发 → 直传真实 1×1 PNG → `final_url` 200 + image/png → 删除）；无凭据 NOT_VERIFIED。
- AC-006 → `go build ./...`、`go test ./...` 不依赖 `.env` 存在即可通过；`go.mod` 无 dotenv；E2E 测试 build-tagged、默认不跑。
- AC-007/008 → Registry（`origin/develop`）↔ Contract ↔ 实现三边一致（17000-17999）；`docs/design/storage.md` 与 APPROVED Contract、最终实现一致（含 Secret 边界、启动 fail-fast、两阶段初始化与 `make test-storage` 真实 HTTP 链路）。
- 通用：`gofmt -l`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Open Risks

- 直传固有边界：后端无法在签发时强制校验真实文件内容；`mimeLimit` 依赖七牛对客户端上报 Content-Type 的执行，客户端可伪造声明。
- 启动真实可用性检查引入对七牛的运行时网络依赖：七牛不可达会导致服务无法启动（D2/D5 接受的代价）；`GetBucketInfo` 内部用 `context.Background()`、超时经注入带 `Timeout` 的 `http.Client` 约束。
- `make up` 预检 + `serve` 启动双重 fail-fast 在 `make up` 路径下触发两次 `GetBucketInfo`（一次性启动成本，已接受）。
- E2E 复用 `internal/cmd` 测试 harness（`setupIsolationServer` 会清空 `users`/`admins` 表并重建超管、`FlushDB`），在 dev 环境有破坏性副作用；与既有集成测试行为一致，但 `make test-storage` 运行会重置身份表，属已知边界，交付前需在文档中提示。
- E2E 走真实上传/删除，每次 `make test-storage` 会对七牛产生一次真实对象上传与删除（依赖真实凭据、产生网络流量），删除失败可能残留测试对象（INV-009 要求尽力清理）。
- 真实七牛凭据缺失：AC-002/AC-010/AC-013/AC-014 无法自验，需 Owner 注入真实凭据后由 Deliverer 验证，否则 NOT_VERIFIED（以非零退出表达）。

## Owner Decision Record

- 2026-10-07：Owner 确认 D1（上传主体）、D2（V1 不落库）、D3（官方 SDK）。
- 2026-10-07（Contract Revision D4）：Owner 确认项目级 Secret 配置方案（`.env.example` 可提交、`.env` 本地 gitignore、不引入 dotenv、`bucket`/`domain` 非敏感可写 config.yaml、`AK/SK` 仅环境变量）。
- 2026-10-08（Contract Revision D5/D6）：Owner 确认 D5（启动 fail-fast 反转 D4 懒校验 + `GetBucketInfo` 最小权限只读）、D6（真实后端签发 E2E 载体 + `.env` 跨环境不传播）。
- 2026-10-09（Contract Revision D7/D8/D9，Owner 已确认）：
  - D7：接受新增 `my-shop qiniu check` 子命令复用 `ValidateConfig`，`serve` 保留独立 fail-fast。
  - D8：`make test-storage` 必须验证真实 HTTP 上传链路（登录鉴权 → 请求签发接口 → 上传极小合法 PNG → 验证 `final_url` HTTP 200 与 Content-Type → 清理测试对象），不得仅调用 `IssueToken` 宣称完成 E2E。
  - D9：显式执行 `make test-storage` 时缺少真实凭据或无法完成验证必须非零退出；普通 `go test ./...` 仍不要求真实凭据。
  - 保留 `make init` 的幂等与 Secret 安全要求。
