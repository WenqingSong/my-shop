# Task: 文件上传与对象存储（Qiniu）V1

## Goal

交付后端「文件上传 / 对象存储」能力：客户端经后端接口取得上传凭证后，可将文件直传至七牛云对象存储并获得可公开访问的文件地址；上传受身份/权限、文件类型与大小约束，错误可稳定区分；七牛云凭据经环境变量安全注入，**真实 AK/SK 只存在于 Owner 本地 `.env`，不进入任何仓库提交物**；最终在真实凭据下由 Deliverer 完成「环境变量 → 签发 token → 真实上传 → final URL 可访问」端到端验收。

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

## Milestone

Milestone: 文件上传与对象存储（Qiniu）V1 完整交付验收（凭证签发 + 鉴权与输入边界 + Secret 可信注入 + 真实七牛凭据下端到端「签发 token → 直传 → final URL 可访问」）

## Secret 与环境边界（三类归属）

### A. Repository Artifact（必须提交，可由 Review/CI 机械核验）

| 产物 | 要求 |
| --- | --- |
| `.env.example` | 提交；仅变量名、注释与安全占位；不含任何真实值 |
| `.gitignore` | 必须忽略 `.env`（同时不得误伤 `.env.example`） |
| `manifest/config/config.yaml`（`qiniu` 段） | `access_key`/`secret_key` 保持空值；`bucket`/`domain`/`region`/`token_ttl`/`max_file_size`/白名单为非敏感默认值 |
| `docs/design/storage.md`、`README.md` | 说明 Secret 注入模型与非敏感/机密边界 |
| 加载链路 | 沿用 `scripts/lib.sh` + Docker Compose + GoFrame 环境变量覆盖，**不引入 dotenv** |
| 源码/测试/脚本 | 不硬编码、不落盘、不打印真实 AK/SK |

### B. Owner Runtime Setup（不提交，不属于仓库交付物）

- Owner 在真实 E2E 前执行：`cp .env.example .env`，并填入真实 `QINIU_ACCESS_KEY`、`QINIU_SECRET_KEY`（必要时 `QINIU_BUCKET`/`QINIU_DOMAIN`）。
- 运行方式：本地经 `make up` / `scripts/*.sh`（`scripts/lib.sh` 自动加载根目录 `.env`）或 Docker Compose 变量替换注入。
- CI/生产经平台 Secret / 环境变量注入，不依赖仓库内文件。
- **跨 Agent / Session / worktree 边界**：`.env` 是未跟踪本地文件，不随 Git 提交、分支切换、worktree 创建或容器构建传播；每个运行环境（含 Deliverer 的临时 worktree/容器）必须自行准备，任何角色不得以「`.env` 不存在」为由绕过验证，只能如实标记 `NOT_VERIFIED` 并说明所需 Owner 前置动作。

### C. E2E Acceptance（Deliverer 里程碑，需 B 已就绪）

完整链路必须可观察通过：后端读取真实环境变量 → 签发 upload token → 客户端/测试用该 token 上传真实文件到七牛 → `final_url` 可 HTTP 200 访问且内容类型与上传文件一致。

## Acceptance Criteria

- [ ] AC-001（凭证签发）：已认证且被授权的客户端调用上传凭证接口，返回可用的七牛云上传凭证（含 key/upload_url/domain/final_url/expires_at 等最小必要信息），token scope 与配置一致。
- [ ] AC-002（真实端到端）：在 Owner 已准备真实凭据的运行环境中，执行完整链路「后端读取真实环境变量 → 签发 upload token → 上传真实文件到七牛 → `final_url` 可访问」；`final_url` 返回 HTTP 200、Content-Type 与上传文件一致，且签名/过期时间正确。**无真实凭据时如实标记 NOT_VERIFIED，不得用 Mock 或假凭据冒充通过。**
- [ ] AC-003（鉴权拒绝）：未认证 / 无权限的请求访问凭证签发接口返回稳定 401/403，不签发凭证、不产生上传。
- [ ] AC-004（类型/大小边界）：超出允许的扩展名/内容类型或大小上限的请求被拒绝，返回稳定错误码 400，不签发凭证。
- [ ] AC-005（配置与凭据 Secret 边界）：真实 AK/SK 不出现在任何受跟踪文件中；`config.yaml` 的 `qiniu.access_key`/`qiniu.secret_key` 为空；源码、测试、文档、日志与响应均不含真实凭据；凭据缺失或非法时按安全语义失败（明确错误码，不泄漏凭据细节、内部路径或堆栈）。
- [ ] AC-006（本地 Secret 加载链路）：仓库提供 `.env.example` 且 `.env` 被 `.gitignore` 忽略（`git check-ignore .env` 通过）；七牛配置经 `scripts/lib.sh`/Docker Compose/GoFrame 环境变量覆盖生效，**未引入 dotenv 类依赖**；缺少 `.env` 时服务仍可构建与启动，签发接口按配置缺失语义失败（不崩溃、不假定文件存在）。
- [ ] AC-007（错误码域）：文件上传/对象存储错误码域经 `.agent/registry/*` 分配并在 `internal/codes` 落地；V1 不落库，不新增 migration。
- [ ] AC-008（长期设计）：`docs/design/storage.md` 沉淀上传模型、配置模型、Secret（AK/SK）与非敏感配置（bucket/domain/region）边界、加载链路与错误语义，并与 APPROVED Contract、最终实现一致。
- [ ] AC-009（环境传播边界）：task.md/README/Design 明确 `.env` 属 Owner 本地 runtime setup、非 Git artifact，跨 Agent/worktree 不自动传播；构建、测试与交付脚本不得把「`.env` 存在」作为通过前提。

## Relevant Context

已核实事实（2026-10-08 owner_update 时核实）：

- 技术栈 GoFrame v2，模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1` → `internal/controller` → `internal/service` → `internal/logic`，数据访问 `g.DB().Model()`，无 `dao`/`model` 层。
- 上传能力已实现（feature commit `6890d9d`）：`api/upload/v1`、`internal/controller/upload`、`internal/logic/upload`（`ValidateConfig`/`IssueToken`）、`internal/service/upload.go`、双端点 `GET /admin/qiniu/upload/token`（`AdminAuth`）与 `GET /qiniu/upload/token`（`Auth`）；错误码 17001/17002/17003 已落地。
- 错误码域 `17000-17999` 已在 `origin/develop` Registry 以 owner=`object-storage-upload` 预留；V1 不落库、无 migration。
- `.env.example` 已存在并提交（commit `0116621`），内容仅注释与安全占位，无任何真实值；已含 `QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY`/`QINIU_BUCKET`/`QINIU_DOMAIN`/`QINIU_REGION`/`QINIU_TOKEN_TTL`/`QINIU_MAX_FILE_SIZE` 说明。
- `.gitignore` 已忽略 `.env`、`.env.local`、`.env.*.local`，且不影响 `.env.example`。
- `scripts/lib.sh` 已有 `_load_env_file()`：根目录 `.env` 存在则加载，**已导出的环境变量优先级更高**，随后由 `scripts/*.sh` / Makefile 启动应用继承；Go 侧无 dotenv 依赖，靠 `g.Cfg().GetEffective` 环境变量覆盖。
- Docker Compose 仅定义 MySQL/Redis（应用为宿主机二进制），Compose 本身的 `${VAR:-default}` 从 shell/docker `.env` 取变量替换。
- `manifest/config/config.yaml` 的 `qiniu` 段当前 `access_key`/`secret_key`/`bucket`/`domain` 均为空串（regin `z2`、ttl 3600、max_file_size 10485760、图片白名单）；该文件的 commit 历史中从未出现非空真实凭据。
- 既有 Review 结论：Cleaner 对 C1=`6890d9d` 的结论为 `CHANGES_REQUIRED`，`CLEAN-001`（bucket/domain 启动 fail-fast vs 签发时懒校验，与 Contract/Design 漂移，P2）仍为 OPEN；`CLEAN-002`（工作区 `config.yaml` 被写入真实凭据，P1）所指脏状态已在 `0116621` 清理，当前 working tree clean。

Assumption（合理但未经 Owner 确认，交 Analyst 核实并向 Owner 确认）：

- Owner 本地真实凭据经 `/root/projects/my-shop/.env` 注入即可满足 E2E；若 Deliverer 使用临时 worktree/容器，Owner 需在该运行环境重复准备 `.env` 或等价环境变量。
- `bucket`/`domain` 为非敏感配置，允许写入 `config.yaml`；真实 E2E 若未提供，仍按签发时校验语义失败（17002）。

OPEN QUESTION（不阻塞任务定义，交 Analyst 分析、Owner 确认）：

- Contract Revision D4 内部对 `bucket`/`domain` 校验时机的表述与 Recommendation §5 不一致（一处说启动 fail-fast，一处说与 AK/SK 一并签发时校验），需 Analyst 统一并以实现或 Contract Revision 收敛。
- 真实 E2E 的执行载体：是否需要一个可提交的 E2E 脚本/测试（读取环境变量，缺凭据时 skip 并标记 NOT_VERIFIED），还是纯 Deliverer 手工验证。若需要，该脚本属 Repository Artifact，但不得含任何真实值。

## Verification

环境：可连接的 MySQL 8.0 与 Redis 7（`make up`）；AC-002 需 Owner 已执行 `cp .env.example .env` 并填入真实 `QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY`。

- AC-001 → 启动服务：已认证客户端请求签发接口，断言 token/key/upload_url/domain/final_url/expires_at 齐全、token 内嵌 put policy 与配置一致。
- AC-002 → 真实凭据（B 就绪）：用返回 token 实际上传一个小文件到七牛，断言 `final_url` HTTP 200、Content-Type 一致、expire 正确；缺凭据 → NOT_VERIFIED 并说明所需 Owner 前置动作。
- AC-003 → 需 MySQL：未认证 401、跨身份域 403，均不签发、无写入。
- AC-004 → 启动服务：非法扩展名/非法 MIME/超大小声明 → 400（17001），不签发。
- AC-005 → 静态核验 + 运行时：Git diff/`git ls-files` 范围内无真实 AK/SK；凭据缺失/非法 → 17002；响应与日志不含凭据。
- AC-006 → 静态核验 + 运行：`git check-ignore -v .env` 命中、`.env.example` 未被忽略；`go.mod` 无 dotenv 依赖；删除/缺少 `.env` 时 `go build`、`go test` 仍通过，服务可启动且签发返回 17002。
- AC-007 → 需 MySQL：Registry ↔ Contract ↔ 实现三边一致（17000-17999）；无新增 migration。
- AC-008 → 文档审查：`docs/design/storage.md` 与 APPROVED Contract、最终实现一致，含 Secret 边界说明。
- AC-009 → 文档与脚本审查：task/README/Design 明示 `.env` 为本地 runtime setup；脚本与测试在缺 `.env` 时按 NOT_VERIFIED 语义失败或跳过，不制造 PASS。
- 通用命令：`gofmt -l`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Complexity

COMPLEX

原因：新增对象存储/文件上传这一此前不存在的模块能力与长期公开协议（凭证签发 API + 新错误码域），引入第三方七牛云接入，并涉及 Secret 注入与安全边界（config / 环境变量 / 日志 / 跨环境传播）；同时仍存在会产生不同业务、安全与运维结果的关键方案分歧（bucket/domain 校验时机对应既有 CLEAN-001；E2E 执行载体），需在 Analyst 重新建立一致的 Contract 并由 Owner 确认。

## Analyst Questions

1. Secret 边界与加载模型最终确认：Repository Artifact（`.env.example`、`.gitignore`、`config.yaml` 空值、README/Design）与 Owner Runtime Setup（`.env`）的完整清单与验收方式；如何机械证明「任何提交物不含真实 AK/SK」。
2. `bucket`/`domain`/`region` 与 AK/SK 的校验时机：统一为「启动 fail-fast」还是「签发时 17002」，消除 Contract Revision D4 与 Recommendation §5 的冲突，并据此收敛实现、Design 与既有 Finding CLEAN-001。
3. 真实 E2E 的载体与证据形式：是否需要可提交的 E2E 脚本/测试（读环境变量、缺凭据时 NOT_VERIFIED），以及 Deliverer 在临时 worktree/容器中如何取得等价环境变量。
4. 上传主体与权限边界是否维持双端点（`AdminAuth` + `Auth`）、不新增权限 code；401/403 边界是否需随本次修订重新确认。
5. 残留：既有 Finding（CLEAN-001）与 APPROVED Contract（target `0116621`）在新要求下的重新验证范围：`state.review.target`（C1=`6890d9d`）是否需要前移，是否为本次要求变更单独形成 Review Request。
6. 全局资源：错误码域（语义：upload/storage）与（如需）migration 的实际分配以已 APPROVED Contract 与 `.agent/registry/*` 为准；若本次修订不改变已预留资源则无需重新分配，变更后需重新核验三边一致。

## Review Baseline

- Base commit（原任务起点）：`7de6e74904e2cd95cfc931bb8cd4628c4e23a859`（分支 `feat/object-storage-upload`，起始 working tree clean）。
- 本次 owner_update 时的分支 head：`31387dfa0ec1de3f3088ecc20a75c3716cef8665`（Cleaner findings commit；local == `origin/feat/object-storage-upload`，working tree clean）。
- 已存在的本任务修改（非本次新增）：`.agent/tasks/object-storage-upload/*`（task/contract/state/findings）、`docs/design/storage.md`、`api/upload/v1/upload.go`、`internal/controller/upload/upload.go`、`internal/logic/upload/{upload.go,upload_test.go}`、`internal/service/upload.go`、`internal/codes/codes.go`、`internal/cmd/{cmd.go,routes_admin.go,routes_frontend.go,routes_test.go,upload_test.go}`、`internal/logic/logic.go`、`.env.example`、`.gitignore`、`README.md`、`manifest/config/config.yaml`、`go.mod`/`go.sum`。
- 重叠修改的区分方式：本次 Task Definition 修订只写 `.agent/tasks/object-storage-upload/task.md`；其余 artifact 与代码由对应角色在处理本 Task 新要求时按各自出口 Gate 流程更新。审查范围起点以 `state.yaml` 的 `review.target`（当前 C1=`6890d9d`）为准；涉及 `0116621`（Secret 配置落地）之后新增的变更需重新纳入 Cleaner 审查范围。

## 本次 Requirement Revision（owner_update 2026-10-08）

与旧版要求的主要差异：

| 维度 | 旧要求 | 新要求 |
| --- | --- | --- |
| Secret 载体 | 「secret 走环境变量，不提交、不进日志/响应」 | 明确 `.env` 为本地运行文件（gitignore、不提交、非 Git artifact），`.env.example` 为唯一提交模板 |
| Owner 前置动作 | 未规定 | 明确 Owner 在真实 E2E 前 `cp .env.example .env` 并填真实 AK/SK |
| 加载链路 | 未规定 | 必须沿用 `scripts/lib.sh` / Docker Compose / GoFrame 环境变量覆盖，禁止引入 dotenv |
| Secret 边界 | 笼统「凭据敏感」 | 明确 `QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY` 为机密；`bucket`/`domain`/`region` 为非敏感配置 |
| E2E 验收 | 「AC-002 需真实凭据，否则 NOT_VERIFIED」 | 明确完整链路四段（读环境变量 → 签发 token → 真实上传 → final URL 可访问）作为 Deliverer 里程碑 |
| 跨环境传播 | 未规定 | 明确未跟踪 `.env` 不在 Agent/worktree/容器间传播，缺凭据只能 NOT_VERIFIED |

需重新验证的既有结论：

- Contract（`target=0116621`，APPROVED）：Secret 注入模型、`bucket`/`domain` 校验时机需与新版要求一致后，**由 Analyst 重新建立 Contract 并经 Owner 确认**，TaskBuilder 不自行批准变更。
- Finding `CLEAN-001`（bucket/domain 校验漂移，OPEN）：随 Contract 修订收敛，需重新复审。
- Finding `CLEAN-002`（工作区写入真实凭据，OPEN）：脏状态已清理，关闭与否由 Cleaner 复审判定。
- 实现 C1（`6890d9d`）与文档 `docs/design/storage.md`：在新 Secret 边界表述下需重新核对一致性。

## Initial Route

交 Analyst（COMPLEX，且 Owner 变化影响已 APPROVED Contract，需 Analyst 重新建立一致的 Contract 后由 Owner 确认）
