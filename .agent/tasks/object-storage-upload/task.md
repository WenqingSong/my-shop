# Task: 文件上传与对象存储（Qiniu）V1

## Goal

交付后端「文件上传 / 对象存储」能力：客户端经后端接口取得上传凭证后，可将文件直传至七牛云对象存储并获得可公开访问的文件地址；上传受身份/权限、文件类型与大小约束，错误可稳定区分；七牛云凭据经环境变量安全注入，**真实 AK/SK 只存在于 Owner 本地 `.env`，不进入任何仓库提交物**；本地开发环境经 `make init` / `make up` 两阶段初始化，不再要求开发者自行摸索 `.env`；最终在真实凭据下由 Deliverer 经 `make test-storage` 完成「签发 token → 真实上传 → final URL 可访问 → 删除测试对象」端到端验收。

## Scope

- 七牛云对象存储接入：bucket、对外域名/region、凭证有效期等**非敏感配置**可写 `manifest/config/config.yaml` 或经环境变量覆盖；`access_key`/`secret_key` 为**机密**，仅经环境变量注入。
- **本地 Secret 生命周期（本次修订核心）**：
  - 仓库提交 `.env.example`（仅变量名与安全占位，无任何真实 Secret）；
  - `.env` 为 Owner 本地运行文件，必须被 `.gitignore` 忽略、不进入 Git、不作为 Git artifact 创建或提交；
  - 本地真实七牛凭据 `QINIU_ACCESS_KEY` / `QINIU_SECRET_KEY` 写入 `.env`；
  - 禁止把真实 AK/SK 写入 `config.yaml`、源码、测试、日志、文档或任何提交文件；
  - 加载链路继续使用现有 `scripts/lib.sh`（本地导出）／Docker Compose（变量替换）／GoFrame 环境变量覆盖（`g.Cfg().GetEffective`），**不引入 dotenv**；
  - `.env` 属未跟踪文件，**不在不同 Agent / Session / worktree / 容器之间传播**，每个运行环境须自行准备。
- 上传凭证签发接口与上传边界：允许的文件类型（扩展名/内容类型）与大小上限、上传主体（后台管理员 / 前台登录用户）的权限与访问控制。
- 错误语义：文件上传/对象存储错误码域（语义：upload/storage），具体域号由 Analyst 读 `.agent/registry/*` 派生并写入 Contract（当前实现使用 17000-17999 域内 17001/17002/17003）。
- 启动依赖语义（Owner D2/D5）：正常 `serve` 启动时七牛云为 required dependency——`access_key`/`secret_key`/`bucket`/`domain`/`region` 等配置缺失/非法，或对指定 bucket 的真实可用性检查（`GetBucketInfo` 最小权限只读）失败，进程启动失败（fail-fast）；`17002` 仅作为运行期防御性守卫（保护异常/绕过启动检查的调用路径），不承担启动错误表达。
- **本地开发环境两阶段初始化（Owner 决策 2026-10-09）**：
  - `make init`（第一阶段）：检查 Docker 等本地依赖 → 初始化并启动 MySQL/Redis 等容器 → 检查根目录 `.env`（不存在则从 `.env.example` 复制创建，已存在则保留、绝不覆盖）→ 输出清晰提示要求开发者编辑 `.env` 填 AK/SK/Bucket/Domain → 提示填完执行 `make up`；本阶段**不要求七牛凭据、不启动 Go 后端**。
  - `make up`（第二阶段）：加载现有 `.env` → 校验 MySQL/Redis 等依赖 → 校验七牛 AK/SK/Bucket/Domain → 执行真实七牛 Bucket 可用性检查 → 任一失败终止启动并明确提示原因 → 全部通过后启动 Go 后端。
  - 初始化操作必须**可重复执行**，不破坏已有环境（不覆盖已有 `.env`、不破坏已有容器与数据）。
- **独立真实 E2E 入口（Owner 决策 2026-10-09）**：`make test-storage` 提供「后端签发 Token → 上传极小合法 PNG → 校验 URL 可访问 → 删除本次测试对象」的端到端验证入口。
- 长期设计：`docs/design/storage.md`（Design Impact = NEW）沉淀上传模型、配置模型、Secret 边界与错误语义。
- 必要测试：凭证签发正常/拒绝路径、类型与大小边界、凭据缺失/非法语义、错误码；并确保测试不硬编码或打印真实凭据。

## Out of Scope

- CDN、图片处理（裁剪/压缩/水印/格式转换/缩略图）。
- 其他对象存储（MinIO/OSS/S3）接入。
- 前端模板工程（`frotend_web`/`frotend_manage`）的真实改造：仅交付后端能力与明确 API 契约，不实现前端页面上传。
- 与具体业务实体（商品图、文章图、轮播图等）的强关联落库改造。
- 上传进度、分片上传/断点续传/大文件直传优化。
- 修改既有模块（商品/文章/轮播图/IAM 等）行为，除上传能力所需的最小接入外。
- **引入 dotenv 或任何新的 Secret 加载依赖**（`github.com/joho/godotenv` 等）。
- **为 CI/容器自动注入真实 AK/SK**：真实凭据来源（本地 `.env` / 平台 Secret）属运行环境配置，不由本任务代码提供默认值。
- **提交 `.env` 或任何含真实凭据的文件**；不包括凭据轮换/撤销设施（超出 V1）。
- **交互式 Secret 输入**：初始化/启动流程只提示编辑 `.env`，不接受在终端交互输入真实 Secret。

## Milestone

Milestone: 文件上传与对象存储（Qiniu）V1 完整交付验收（凭证签发 + 鉴权与输入边界 + Secret 可信注入 + 启动 fail-fast/真实可用性检查 + `make init`/`make up` 两阶段初始化 + `make test-storage` 真实最小 PNG 端到端直传与清理）

## Secret 与环境边界（三类归属）

### A. Repository Artifact（必须提交，可由 Review/CI 机械核验）

| 产物 | 要求 |
| --- | --- |
| `.env.example` | 提交；仅变量名、注释与安全占位；不含任何真实值 |
| `.gitignore` | 必须忽略 `.env`（同时不得误伤 `.env.example`） |
| `manifest/config/config.yaml`（`qiniu` 段） | `access_key`/`secret_key` 保持空值；`bucket`/`domain`/`region`/`token_ttl`/`max_file_size`/白名单为非敏感默认值 |
| `Makefile` + `scripts/*.sh`（`init.sh`/`up.sh`/`test-storage.sh` 等） | 复用 `scripts/lib.sh` + Docker Compose；不引入 dotenv；不交互输入 Secret |
| `docs/design/storage.md`、`README.md` | 说明 Secret 注入模型、非敏感/机密边界与两阶段初始化流程 |
| 加载链路 | 沿用 `scripts/lib.sh` + Docker Compose + GoFrame 环境变量覆盖，**不引入 dotenv** |
| 源码/测试/脚本 | 不硬编码、不落盘、不打印真实 AK/SK |

### B. Owner Runtime Setup（不提交，不属于仓库交付物）

- 开发者首次执行 `make init`：脚本自动从 `.env.example` 复制生成 `.env`（若不存在）并提示编辑，填入真实 `QINIU_ACCESS_KEY`、`QINIU_SECRET_KEY`（必要时 `QINIU_BUCKET`/`QINIU_DOMAIN`）；`.env` 已存在则保留不覆盖。
- 运行方式：本地经 `make up` / `scripts/*.sh`（`scripts/lib.sh` 自动加载根目录 `.env`）或 Docker Compose 变量替换注入。
- CI/生产经平台 Secret / 环境变量注入，不依赖仓库内文件。
- **跨 Agent / Session / worktree 边界**：`.env` 是未跟踪本地文件，不随 Git 提交、分支切换、worktree 创建或容器构建传播；每个运行环境（含 Deliverer 的临时 worktree/容器）必须自行准备，任何角色不得以「`.env` 不存在」为由绕过验证，只能如实标记 `NOT_VERIFIED` 并说明所需 Owner 前置动作。

### C. E2E Acceptance（Deliverer 里程碑，需 B 已就绪，入口 `make test-storage`）

完整链路必须可观察通过，使用**真实最小合法图片**（1×1 PNG，几十字节级别），不以伪造内容类型冒充：

1. 后端读取真实环境变量并签发 upload token（Owner D1：方案 a，真实后端签发链路）；
2. 用该 token 直传真实最小 PNG 到七牛；
3. `final_url` HTTP 200 且 `Content-Type=image/png`；
4. 使用返回的精确 key，经七牛 SDK 删除本次测试对象（清理，不留残留）。

不修改生产 key 规则、不要求 `e2e/` 专用前缀。

## Acceptance Criteria

- [ ] AC-001（凭证签发）：已认证且被授权的客户端调用上传凭证接口，返回可用的七牛云上传凭证（含 key/upload_url/domain/final_url/expires_at 等最小必要信息），token scope 与配置一致。
- [ ] AC-002（真实端到端）：在 Owner 已准备真实凭据的运行环境中，走**真实后端签发链路**完成「读取真实环境变量 → 签发 upload token → 直传真实最小 PNG（1×1，几十字节）→ `final_url` HTTP 200 且 `Content-Type=image/png` → 用返回 key 经七牛 SDK 删除测试对象」；不使用伪造内容类型（如 1 byte 伪装 image/png）、不修改生产 key 规则、不要求 `e2e/` 专用前缀。**无真实凭据时如实标记 NOT_VERIFIED，不得用 Mock 或假凭据冒充通过。**
- [ ] AC-003（鉴权拒绝）：未认证 / 无权限的请求访问凭证签发接口返回稳定 401/403，不签发凭证、不产生上传。
- [ ] AC-004（类型/大小边界）：超出允许的扩展名/内容类型或大小上限的请求被拒绝，返回稳定错误码 400，不签发凭证。
- [ ] AC-005（配置与凭据 Secret 边界）：真实 AK/SK 不出现在任何受跟踪文件中；`config.yaml` 的 `qiniu.access_key`/`qiniu.secret_key` 为空；源码、测试、文档、日志与响应均不含真实凭据；任何失败路径（启动 fail-fast 或运行期 17002）均不泄漏凭据细节、内部路径或堆栈。
- [ ] AC-006（本地 Secret 加载链路）：仓库提供 `.env.example` 且 `.env` 被 `.gitignore` 忽略（`git check-ignore .env` 通过）；七牛配置经 `scripts/lib.sh`/Docker Compose/GoFrame 环境变量覆盖生效，**未引入 dotenv 类依赖**；`go build ./...` 与 `go test ./...` 不依赖 `.env` 存在即可通过（构建与单测不要求真实凭据；服务运行启动语义见 AC-010）。
- [ ] AC-007（错误码域）：文件上传/对象存储错误码域经 `.agent/registry/*` 分配并在 `internal/codes` 落地；V1 不落库，不新增 migration。
- [ ] AC-008（长期设计）：`docs/design/storage.md` 沉淀上传模型、配置模型、Secret（AK/SK）与非敏感配置（bucket/domain/region）边界、加载链路与错误语义，并与 APPROVED Contract、最终实现一致。
- [ ] AC-009（环境传播边界）：task.md/README/Design 明确 `.env` 属 Owner 本地 runtime setup、非 Git artifact，跨 Agent/worktree 不自动传播；构建与单元测试不得把「`.env` 存在」作为通过前提（真实服务启动检查除外，见 AC-010）。
- [ ] AC-010（启动依赖与真实可用性检查）：正常 `serve` 启动时，七牛 required dependency 配置（`access_key`/`secret_key`/`bucket`/`domain`/`region` 等）缺失或非法 → 进程启动失败（fail-fast，不静默降级、不等到签发时才发现）；启动时对**指定 bucket** 执行最小权限只读验证（`GetBucketInfo`，非 `BucketManager.Buckets()`），真实依赖验证失败 → 进程启动失败；`17002` 仅作为运行期防御性守卫（保护异常/绕过启动检查的调用路径），不承担启动错误表达。
- [ ] AC-011（make init 两阶段第一阶段）：`make init` 检查 Docker 等本地依赖、初始化并启动 MySQL/Redis 容器；检查根目录 `.env`——不存在时从 `.env.example` 复制创建，已存在时保留绝不覆盖；输出清晰提示要求开发者编辑 `.env` 填七牛 AK/SK/Bucket/Domain 并执行 `make up`；本阶段不要求七牛凭据、不启动 Go 后端、不在终端交互输入真实 Secret。
- [ ] AC-012（make init 幂等）：`make init` 可重复执行；对已存在的 `.env` 不覆盖、对已运行的容器与已有数据不破坏，不产生破坏性副作用。
- [ ] AC-013（make up 两阶段第二阶段）：`make up` 加载现有 `.env` → 校验 MySQL/Redis 等依赖 → 校验七牛 AK/SK/Bucket/Domain → 执行真实七牛 Bucket 可用性检查；任一必要依赖检查失败则**终止启动**、以非零退出码退出并明确提示原因（不泄漏凭据）；全部通过后才启动 Go 后端。
- [ ] AC-014（make test-storage 独立 E2E）：`make test-storage` 提供独立入口完成「后端签发 Token → 上传极小合法 PNG → `final_url` HTTP 200 且 `Content-Type=image/png` → 用返回 key 删除本次测试对象」；复用真实凭据、不修改生产 key 规则、不要求 `e2e/` 前缀；缺真实凭据时如实 `NOT_VERIFIED`。

## Relevant Context

已核实事实（2026-10-09 owner_update 时核实）：

- 技术栈 GoFrame v2，模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1` → `internal/controller` → `internal/service` → `internal/logic`，数据访问 `g.DB().Model()`，无 `dao`/`model` 层。
- 上传能力已实现：`api/upload/v1`、`internal/controller/upload`、`internal/logic/upload`、`internal/service/upload.go`、双端点 `GET /admin/qiniu/upload/token`（`AdminAuth`）与 `GET /qiniu/upload/token`（`Auth`）；错误码 17001/17002/17003 已落地。
- D5 已决定并实现（commit `f52d044`）：`internal/logic/upload/upload.go` 的 `ValidateConfig` = `validateStructural`（region/ttl/大小/白名单）→ `validateRequired`（AK/SK/bucket/domain 存在性）→ `checkBucketAvailable`（`BucketManager.GetBucketInfo(bucket)` 指定 bucket 最小权限只读，**非 `Buckets()`**，注入带 Timeout 的 http.Client 约束超时）；`serve` 启动时调用，任一失败非零退出。
- 错误码域 `17000-17999` 已在 `origin/develop` Registry 以 owner=`object-storage-upload` 预留；V1 不落库、无 migration。
- `.env.example` 已提交，七牛项已取消注释（`QINIU_ACCESS_KEY=`/`QINIU_SECRET_KEY=`/`QINIU_BUCKET=`/`QINIU_DOMAIN=` 可直填），仅变量名与安全占位、无真实值。
- `.gitignore` 已忽略 `.env`、`.env.local`、`.env.*.local`，且不影响 `.env.example`。
- `scripts/lib.sh` 已有可复用能力：`_load_env_file`（根目录 `.env` 存在则加载，已导出环境变量优先）、`docker_compose`、`container_healthy`、`wait_for_deps`、`build_app`/`migrate_app`/`start_app`/`stop_app`、`app_health_url` 等。
- 当前 `Makefile` 目标：`help/bootstrap/up/down/restart/status/logs/health/test/clean`，**无 `init`、无 `test-storage`**；`bootstrap.sh` 现仅调用 `up.sh`。
- 当前 `scripts/up.sh` 流程：`docker_compose up -d mysql redis` → `wait_for_deps` → `build_app` → `migrate_app` → `start_app`；**无七牛配置校验、无真实 bucket 可用性检查**（后者现仅存在于 Go `serve` 的 `ValidateConfig`）。
- `manifest/config/config.yaml` 的 `qiniu` 段当前 `access_key`/`secret_key`/`bucket`/`domain` 均为空串（region `z2`、ttl 3600、max_file_size 10485760、图片白名单）；该文件的 commit 历史中从未出现非空真实凭据。
- 当前 `state.yaml`：`contract.status=APPROVED`（target `8abdec2f`）、`review.status=PENDING`（target `7c881f6`）、`delivery.status=NOT_RUN`。

Owner 决策（作为 Analyst 固化 Contract 的约束）：

- D1（E2E 执行方式，2026-10-08）：选择方案 a——必须走真实后端签发链路；E2E 不使用「1 byte 内容伪装 image/png」，改用真实最小合法图片（1×1 PNG，几十字节级）；链路为「后端签发 token → 上传真实最小 PNG → `final_url` HTTP 200 → `Content-Type=image/png` → 用返回的精确 key 经七牛 SDK 删除本次测试对象」；不修改生产 key 规则、不要求 `e2e/` 专用前缀。
- D2（启动依赖与 17002 语义，2026-10-08）：接受 `17002` 作为运行期防御性守卫；正常 `serve` 启动阶段若七牛配置缺失或真实依赖验证失败，进程直接启动失败；`17002` 不承担启动错误表达，只保护运行期异常/绕过启动检查的调用路径；启动真实可用性检查不得硬锁 `BucketManager.Buckets()`。
- D5（最小权限只读验证，已实现）：启动真实可用性检查采用针对「指定 bucket」的 `GetBucketInfo`（最小权限只读），不使用「列出整个账户所有 bucket」的 `Buckets()`；已落地于 `ValidateConfig`。
- 两阶段初始化（2026-10-09）：`make init`（第一阶段，不要求七牛凭据、不启动后端）+ `make up`（第二阶段，加载 `.env` → 校验依赖与七牛配置 → 真实 bucket 可用性检查 → 全部通过才启动后端）+ `make test-storage`（独立真实 E2E 入口）；不交互输入 Secret、复用现有 Makefile/`scripts/lib.sh`/Docker Compose、不引入 dotenv、初始化幂等、保留现有上传功能与已确认业务需求。

Assumption（合理但未经 Owner 确认，交 Analyst 核实并向 Owner 确认）：

- Owner 本地真实凭据经 `/root/projects/my-shop/.env` 注入即可满足 E2E；若 Deliverer 使用临时 worktree/容器，Owner 需在该运行环境重复准备 `.env` 或等价环境变量。
- `bucket`/`domain` 为非敏感配置，允许写入 `config.yaml`；但 serve/`make up` 启动时它们属 required dependency，缺失/非法 → fail-fast（17002 仅运行期守卫）。

OPEN QUESTION（不阻塞任务定义，交 Analyst 分析、Owner 确认）：

- `make up` 预检中「真实七牛 Bucket 可用性检查」的实现载体：是新增 Go 子命令（如 `my-shop qiniu check`，复用 `ValidateConfig`/`checkBucketAvailable`）由 shell 调用，还是仅做 shell 层配置存在性检查、真实检查留在 `serve` 内部；两者是否会重复触发网络请求。
- `make test-storage` 的实现载体：shell + curl 驱动（签发→直传→删除）还是 Go 工具/Go test；删除测试对象依赖七牛 SDK，需明确其复用方式（Go 子命令 vs curl 签名）。
- serve fail-fast 对测试装配的影响：正常 serve 现在要求真实七牛凭据才能启动，既有 `internal/cmd` 集成测试如何在不要求真实凭据的前提下保持可运行（fail-fast 作用点与测试注入方式）。

## Verification

环境：可连接的 MySQL 8.0 与 Redis 7；AC-002/AC-014 需 Owner 已通过 `make init`/编辑 `.env` 填入真实 `QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY`。

- AC-001 → 启动服务：已认证客户端请求签发接口，断言 token/key/upload_url/domain/final_url/expires_at 齐全、token 内嵌 put policy 与配置一致。
- AC-002 → 真实凭据（B 就绪）：走真实后端签发链路，用返回 token 实际上传真实最小 PNG（1×1，几十字节）到七牛，断言 `final_url` HTTP 200、`Content-Type=image/png`、expire 正确，并用返回 key 经七牛 SDK 删除测试对象；缺凭据 → NOT_VERIFIED 并说明所需 Owner 前置动作。
- AC-003 → 需 MySQL：未认证 401、跨身份域 403，均不签发、无写入。
- AC-004 → 启动服务：非法扩展名/非法 MIME/超大小声明 → 400（17001），不签发。
- AC-005 → 静态核验 + 运行时：Git diff/`git ls-files` 范围内无真实 AK/SK；凭据缺失/非法在启动阶段 fail-fast、运行期 17002 均不泄漏；响应与日志不含凭据。
- AC-006 → 静态核验 + 构建：`git check-ignore -v .env` 命中、`.env.example` 未被忽略；`go.mod` 无 dotenv 依赖；缺少 `.env` 时 `go build ./...`、`go test ./...` 仍通过（构建/单测不要求真实凭据）。
- AC-007 → 需 MySQL：Registry ↔ Contract ↔ 实现三边一致（17000-17999）；无新增 migration。
- AC-008 → 文档审查：`docs/design/storage.md` 与 APPROVED Contract、最终实现一致，含 Secret 边界说明。
- AC-009 → 文档与脚本审查：task/README/Design 明示 `.env` 为本地 runtime setup；构建与单测在缺 `.env` 时不失败、不制造 PASS。
- AC-010 → 启动验证：无/非法七牛配置时 `serve` 启动进程以非零退出码失败并给出不含凭据的明确错误；提供有效配置但真实依赖验证失败（如 bucket 不存在/无权限）时同样启动失败；启动检查使用指定 bucket 的 `GetBucketInfo`（非 `Buckets()`）。
- AC-011 → 环境验证：在无 `.env` 的全新工作区运行 `make init`，断言 Docker 检查通过、MySQL/Redis 容器启动、`.env` 由 `.env.example` 复制生成、输出编辑提示；进程未启动 Go 后端、无交互 Secret 输入提示。
- AC-012 → 幂等验证：连续两次运行 `make init`，断言第二次不覆盖已有 `.env`（内容不变）、容器与数据不破坏、退出正常。
- AC-013 → 环境验证：缺七牛配置（AK/SK/Bucket/Domain 任一为空）时 `make up` 非零退出并明确提示；配全但 bucket 不可用（如错误 bucket/无权限）时同样非零退出；全部通过时才启动后端。
- AC-014 → 真实凭据：`make test-storage` 走完整链路并清理测试对象；缺凭据时 `NOT_VERIFIED` 并说明前置动作。
- 通用命令：`gofmt -l`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Complexity

COMPLEX

原因：新增对象存储/文件上传这一此前不存在的模块能力与长期公开协议（凭证签发 API + 新错误码域），引入第三方七牛云接入，并涉及 Secret 注入与安全边界（config / 环境变量 / 日志 / 跨环境传播）、serve 启动 fail-fast 的真实可用性检查（D5 已实现），以及新的本地开发两阶段初始化与 `make test-storage` 脚本工程；其中「make up 预检与 test-storage 的实现载体」仍未确定、serve fail-fast 对测试装配的影响需设计，需 Analyst 建立一致 Contract 并由 Owner 确认。

## Analyst Questions

1. `make up` 预检中「真实七牛 Bucket 可用性检查」的实现载体：新增 Go 子命令（复用 `ValidateConfig`/`checkBucketAvailable`）由 shell 调用，还是 shell 层仅做配置存在性检查、真实检查留 `serve` 内部；避免与 `serve` 启动重复触发网络请求，并给出推荐。
2. `make test-storage` 的实现载体：shell + curl（签发→直传→删除）还是 Go 工具/Go test；「删除测试对象」依赖七牛 SDK，明确复用方式（Go 子命令 vs 手动签名）。
3. serve fail-fast 的装配与测试边界：启动真实可用性检查的作用点（仅在 serve 入口触发，测试/构建不触发）；无真实凭据时 `go build`/`go test` 与既有 `internal/cmd` 集成测试如何保持可运行。
4. Secret 边界与加载模型最终确认：Repository Artifact（`.env.example`、`.gitignore`、`config.yaml` 空值、Makefile/scripts、README/Design）与 Owner Runtime Setup（`.env`）清单，以及「提交物不含真实 AK/SK」的机械证明方式。
5. 上传主体与权限边界：双端点（`AdminAuth` + `Auth`）与 401/403 边界是否随本次修订变动（当前维持不变）。
6. 残留 re-verification 范围：CLEAN-001（bucket/domain 校验漂移）已由 D5 的 `validateRequired` 解决，待 Cleaner 复审关闭；CLEAN-002 关闭与否由 Cleaner 判定；`state.review.target`（当前 `7c881f6`）是否需因新增脚本工程前移。
7. 全局资源：错误码域 17000-17999（已预留）与（如需）migration 的三边一致核验。

## Review Baseline

- Base commit（原任务起点）：`7de6e74904e2cd95cfc931bb8cd4628c4e23a859`（分支 `feat/object-storage-upload`，起始 working tree clean）。
- 本次 owner_update（两阶段初始化决策回合）时的分支 head：`7ae0977`（local == `origin/feat/object-storage-upload`，working tree clean）。已含 D5 实现（`f52d044`）、Review Request 更新（`7c881f6`）等。
- 已存在的本任务修改（非本次新增）：`.agent/tasks/object-storage-upload/*`、`docs/design/storage.md`、`api/upload/v1/upload.go`、`internal/controller/upload/upload.go`、`internal/logic/upload/{upload.go,upload_test.go}`、`internal/service/upload.go`、`internal/codes/codes.go`、`internal/cmd/{cmd.go,routes_admin.go,routes_frontend.go,routes_test.go,upload_test.go}`、`internal/logic/logic.go`、`.env.example`、`.gitignore`、`README.md`、`manifest/config/config.yaml`、`go.mod`/`go.sum`。
- 重叠修改的区分方式：本次 Task Definition 修订只写 `.agent/tasks/object-storage-upload/task.md`；`contract.md` 为 Analyst 所有物，TaskBuilder 不提交、不改写。新增的 `Makefile` `init`/`test-storage` 目标与 `scripts/init.sh`/`scripts/test-storage.sh`（或等价脚本）属本 Task 新增产物，后续由 Coder 实现并纳入 Cleaner 审查范围；审查范围起点以 `state.yaml` 的 `review.target`（当前 `7c881f6`）为准。

## 本次 Requirement Revision（owner_update 2026-10-09）

与上一版要求的主要差异：

| 维度 | 上一版 | 新版 |
| --- | --- | --- |
| 本地初始化 | 无 `init`，依赖开发者自行 `cp .env.example .env` | `make init` 两阶段第一阶段：自动建 `.env`（不覆盖）+ 启动容器 + 提示编辑 |
| 启动前预检 | `serve` 内部 fail-fast（D2/D5） | 增加 `make up` 第二阶段预检：`.env` + 依赖 + 七牛配置 + 真实 bucket 可用性检查，通过后才启动后端 |
| E2E 入口 | AC-002 手动/交付验证 | 增加 `make test-storage` 独立真实 E2E 入口（AC-014） |
| 最小权限只读验证 | OPEN QUESTION | 已由 D5 决定并实现：`GetBucketInfo`（指定 bucket），非 `Buckets()` |
| 交互 Secret | 未明确 | 明确禁止终端交互输入 Secret，只提示编辑 `.env` |

需重新验证的既有结论：

- Contract（`target=8abdec2f`，APPROVED）：新增两阶段初始化、`make up` 预检与 `make test-storage` 后，**由 Analyst 重新修订 Contract 并经 Owner 确认**，TaskBuilder 不自行批准变更。
- Finding `CLEAN-001`（bucket/domain 校验漂移，OPEN）：已由 D5 的 `validateRequired` 在启动 fail-fast 解决，待 Cleaner 复审关闭。
- Finding `CLEAN-002`（工作区写入真实凭据，OPEN）：脏状态已清理，关闭与否由 Cleaner 复审判定。
- 实现（含 D5）与文档 `docs/design/storage.md`：在新两阶段初始化与 Secret 边界表述下需重新核对一致性；`docs/design/storage.md` 需补充两阶段初始化与 `make test-storage` 说明。

## Initial Route

交 Analyst（COMPLEX，且 Owner 两阶段初始化决策影响已 APPROVED Contract，需 Analyst 重新修订 Contract——含 `make up` 预检与 `make test-storage` 的实现载体方案——后由 Owner 确认）
