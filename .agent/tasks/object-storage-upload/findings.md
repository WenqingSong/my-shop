# Cleaner Findings

## Review Target

- 任务：`object-storage-upload`（文件上传与对象存储 Qiniu V1）
- 任务基线（base commit）：`7de6e74904e2cd95cfc931bb8cd4628c4e23a859`（`feat/object-storage-upload`，任务开始时 working tree clean）
- 审查对象（implementation Evidence Commit C1）：`11790c5ece155dac76dd54ed4245baa64d0650d4`（`test(upload): E2E 直传成功后注册清理...`，含上一实现提交 `3a9fc5a` 的 `qiniu check` 子命令 + 两阶段初始化 + 真实存储 E2E）
- Coder metadata commit（C2，非审查对象）：`bfb5b53`（`chore(workflow): 更新 Review Request...`），当前 HEAD，local == `origin/feat/object-storage-upload`
- 审查范围：`7de6e74..11790c5` 的完整变更（生产代码 + 测试 + `docs/design/storage.md` + 脚本 + `Makefile` + `manifest/config/config.yaml` qiniu 段）
- 新增文件：`api/upload/v1/upload.go`、`internal/controller/upload/upload.go`、`internal/logic/upload/{upload.go,upload_test.go}`、`internal/service/upload.go`、`internal/cmd/{upload_test.go,storage_e2e_test.go}`、`scripts/{init.sh,test-storage.sh}`、`docs/design/storage.md`、`.env.example`
- 修改文件：`internal/codes/codes.go`、`internal/cmd/{cmd.go,routes_admin.go,routes_frontend.go,routes_test.go}`、`internal/logic/logic.go`、`manifest/config/config.yaml`、`Makefile`、`scripts/up.sh`、`README.md`、`.gitignore`、`go.mod`/`go.sum`
- 全局资源：错误码域 `17000-17999`（`origin/develop` Registry 已 RESERVED，owner=object-storage-upload）；无 migration（V1 不落库）
- 当前工作区状态：`HEAD=bfb5b53`，local == origin；未跟踪 `storage/`（轮播图运行时占位图，属 banner 特性运行时产物，与本次任务无关）；`.env` 为 gitignore 忽略的本地真实凭据文件，未提交

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（凭证签发） | PASS | `TestUploadTokenHappyPath` 通过：token/key/upload_url/domain/final_url/expires_at 齐全；解码 token 内嵌 put policy，scope=bucket:key、deadline≈expires_at、mimeLimit/fsizeLimit 与配置一致；后台端点同样可用；连续签发 key 不同（INV-004） |
| AC-002（真实端到端） | NOT_VERIFIED | Deliverer 里程碑（真实上传 + 真实凭据）；Cleaner 不执行 Deliverer 里程碑验收。真实凭据已就绪（`qiniu check` 通过），E2E 测试 `go vet -tags storage_e2e` 编译通过、链路逻辑审查无缺陷，待 Deliverer 经 `make test-storage` 执行 |
| AC-003（鉴权拒绝） | PASS | `TestUploadTokenAuthBoundary` 通过：未认证 401/1002；user token 打后台端点、admin token 打前台端点均 403/1003，不签发 |
| AC-004（类型/大小边界） | PASS | `TestUploadTokenInvalidInput` 通过：非法扩展名/非法 MIME/无扩展名 → 400/17001，不签发。大小上限无请求声明参数，经 token `fsizeLimit`（10485760）由七牛服务端执行（HappyPath 已断言），符合 Contract「固化进 token scope」语义 |
| AC-005（配置与凭据安全） | PASS | `config.yaml` qiniu 段 AK/SK/bucket/domain 空默认值，`git diff config.yaml` 无改动；`git grep` 受跟踪文件 QINIU_ 均为变量名/占位、无真实值；错误信息只指认字段名/bucket，不泄漏凭据 |
| AC-006（Secret 加载链路） | PASS | `git check-ignore .env` 命中（.gitignore:16），`.env.example` 未被忽略；`go.mod` 无 dotenv；`go build ./...`/`go test ./...` 在无 .env 环境下通过 |
| AC-007（错误码域） | PASS | 三边一致：`origin/develop` Registry=17000-17999 RESERVED（owner=object-storage-upload）；Contract=17000-17999（17001/17002/17003）；实现 `codes.go`=17001/17002/17003。无新增 migration |
| AC-008（长期设计） | PASS | `docs/design/storage.md` 与 APPROVED Contract、最终实现一致（上传模型、配置模型、Secret 边界、启动 fail-fast、两阶段初始化、`make test-storage` 真实 HTTP 链路） |
| AC-009（环境传播边界） | PASS | task.md/README/Design 均明示 `.env` 为 Owner 本地 runtime setup、非 Git artifact、跨环境不传播；构建与单测不以 `.env` 存在为通过前提 |
| AC-010（启动依赖与真实可用性检查） | PASS | `./bin/my-shop serve`（无 Qiniu 凭据、其余依赖就绪）exit 1 且 "qiniu.access_key 未配置"（fail-fast、不泄漏）；`ValidateConfig` 用指定 bucket 的 `GetBucketInfo`（非 `Buckets()`）；`qiniu check` 配全但 bucket 不存在 → exit 1 "no such entry" |
| AC-011（make init 第一阶段） | PASS | `make init` 实测：Docker/Compose 检查通过、容器启动、检测到已有 .env 保留不覆盖、输出编辑提示、不启动后端、无交互 Secret 输入 |
| AC-012（make init 幂等） | PASS | `make init` 前后 .env md5 不变（df23f00a...）；容器已运行时幂等不破坏 |
| AC-013（make up 第二阶段） | PASS | `qiniu check` 预检机制三态实测：无凭据 exit 1；真实凭据 exit 0；bucket 不存在 exit 1 "no such entry"；`up.sh` 顺序为 build → qiniu check（失败非零退出、不启动）→ migrate → start，`set -euo pipefail` 保证失败终止 |
| AC-014（make test-storage 独立 E2E） | NOT_VERIFIED | Deliverer 里程碑；同 AC-002。`test-storage.sh` 校验四 QINIU_* 非空、缺任一 exit 1；E2E 测试编译通过、逻辑审查无缺陷，待 Deliverer 执行 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `gofmt -l`（新增/修改 Go 文件） | PASS | 无输出 |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `go vet -tags storage_e2e ./internal/cmd/...` | PASS | exit 0（E2E 测试编译通过） |
| `go test -p 1 ./...` | PASS | 全量通过（无 .env 环境），含 `internal/cmd`（50s）、`internal/migrations`（25s） |
| `make test` 环境（source .env）下 `TestUploadTokenMissingConfig` | FAIL | 见 CLEAN-003 |
| `bash -n scripts/{init,up,test-storage,lib}.sh` | PASS | 语法通过 |
| `./bin/my-shop qiniu check`（无凭据） | PASS | exit 1，"qiniu.access_key 未配置"（不泄漏凭据） |
| `./bin/my-shop qiniu check`（真实凭据） | PASS | exit 0，"七牛云配置与 bucket 可用性校验通过" |
| `./bin/my-shop qiniu check`（bucket 不存在） | PASS | exit 1，"no such entry"（不泄漏凭据） |
| `./bin/my-shop serve`（无 Qiniu 凭据，其余依赖就绪） | PASS | exit 1，"qiniu.access_key 未配置"（AC-010 fail-fast） |
| `make init`（.env 已存在） | PASS | exit 0，.env md5 不变（幂等），不启动后端，无交互输入 |
| `git check-ignore .env` / `.env.example` | PASS | .env 命中 ignore；.env.example 未被忽略 |
| `go.mod` 无 dotenv | PASS | 仅新增 `github.com/qiniu/go-sdk/v7` |
| Registry ↔ Contract ↔ 实现（错误码域） | PASS | develop Registry=17000-17999 RESERVED；Contract 一致；codes.go=17001/17002/17003。本地 feature 分支 registry 滞后于 develop 属预期（Reservation 仅落 develop），权威以 `origin/develop` 为准 |
| 受跟踪文件无真实凭据 | PASS | `git grep` QINIU_ 均为变量名/占位；config.yaml AK/SK 空；`git diff config.yaml` 无改动 |

## Findings

### CLEAN-001（历史，已关闭）：bucket/domain 配置校验语义与 Contract/Design 漂移

- Severity：P2
- Status：CLOSED
- 说明：D5 的 `validateRequired`（`internal/logic/upload/upload.go` L180-192）已在启动 fail-fast 校验 bucket/domain；`ValidateConfig` = `validateStructural` → `validateRequired` → `checkBucketAvailable`。已与 Contract/Design 一致，`TestValidateRequired` 覆盖缺 bucket/domain 拒绝。

### CLEAN-002（历史，已关闭）：工作区 `manifest/config/config.yaml` 硬编码真实凭据

- Severity：P1
- Status：CLOSED
- 说明：当前 `manifest/config/config.yaml` qiniu 段为空默认值，`git diff manifest/config/config.yaml` 无改动，无真实凭据残留。

### CLEAN-003：`TestUploadTokenMissingConfig` 在 `make test`（加载 `.env`）环境下失败

- Severity：P2
- Status：OPEN
- Location：`internal/cmd/upload_test.go` `TestUploadTokenMissingConfig`（L216-229）
- AC / Invariant：AC-006 / INV-005（构建与单测不依赖 `.env` 存在即可通过；同时不得在 `.env` 已存在时因环境泄漏而失败）
- Trigger：开发者按 `make init` 流程填入真实 `.env` 后运行 `make test`（`scripts/test.sh` source `scripts/lib.sh` → `_load_env_file` 导出 `QINIU_*`），测试进程继承真实七牛凭据；`TestUploadTokenMissingConfig` 未对 `QINIU_*` 做环境隔离，`IssueToken` 的 `validateCredentials` 因凭据非空而走签发成功路径。
- Actual：签发返回 200/0，测试断言 500/17002 → FAIL：`缺凭据签发: status=200 code=0 want 500/17002`。
- Expected：测试应确定性地覆盖「缺凭据 → 17002」路径，不受进程是否继承 `.env` 影响；`make test` 在开发者已按文档填有真实 `.env` 的正常流程下应通过。
- Impact：`make test` 入口在文档化本地开发流程（先 `make init` 填 `.env` 再 `make test`）下失败；直接 `go test ./...`（无 `.env`）仍通过，但测试隔离性缺失，属可靠性/维护风险。生产行为（缺凭据返回 17002）本身正确，此为非生产代码缺陷。
- Evidence：`(source scripts/lib.sh; go test -p 1 -run TestUploadTokenMissingConfig -v ./internal/cmd/...)` → FAIL（`upload_test.go:223`）；对照组无 .env 直接 `go test -p 1 ./...` 全量通过。
- Required Fix Boundary：`TestUploadTokenMissingConfig` 需对 `QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY`/`QINIU_BUCKET`/`QINIU_DOMAIN` 做环境隔离（如 `t.Setenv(...)` 置空），确保无论进程是否继承真实 `.env` 都能确定性走「缺凭据 → 17002」路径；不得通过放宽断言或删除该测试修复。
