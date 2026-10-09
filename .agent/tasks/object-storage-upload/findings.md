# Cleaner Findings

## Review Target

- 任务：`object-storage-upload`（文件上传与对象存储 Qiniu V1）
- 任务基线（base commit）：`7de6e74904e2cd95cfc931bb8cd4628c4e23a859`（`feat/object-storage-upload`，任务开始时 working tree clean）
- 审查对象（implementation Evidence Commit C1）：`0414bff4116952b5a7bce1f6c4a53934d7f95a85`
  - 该 commit 为 `dev-environment-setup` 合并后（HEAD=`821e253`）+ TaskBuilder 核对 task.md 之后的合并态快照，包含全部上传业务代码与 `dev-environment-setup` 落在本任务 scope 内文件的增量（`Makefile`、`scripts/*`、`.env.example`、`internal/cmd/cmd.go`、`internal/boot/*`）。
- Coder metadata commit（C2，非审查对象）：`e3937d1aa13184274031c66d64ae005ca72e25a7`（`chore(workflow): 重新确立 review.target 至 dev-environment-setup 合并后 HEAD`），当前 HEAD，local == `origin/feat/object-storage-upload`，working tree clean。C2 相对 C1 仅改 `state.yaml` 的 `review.target`（`6e3f1f9` → `0414bff`），无业务代码变化。
- 审查范围：`7de6e74..0414bff` 完整变更（44 文件，生产代码 + 测试 + `docs/design/storage.md` + 脚本 + `Makefile` + `manifest/config/config.yaml` qiniu 段 + `.env.example` + `.gitignore`）。
- 新增文件：`api/upload/v1/upload.go`、`internal/controller/upload/upload.go`、`internal/logic/upload/{upload.go,upload_test.go}`、`internal/service/upload.go`、`internal/cmd/{upload_test.go,storage_e2e_test.go}`、`internal/boot/{admin_check.go,admin_check_test.go}`、`scripts/{init.sh,test-lib.sh,test-storage.sh}`、`docs/design/storage.md`、`.env.example`、`.agent/tasks/dev-environment-setup/*`（另一任务的已验收 artifacts）。
- 修改文件：`internal/codes/codes.go`、`internal/cmd/{cmd.go,routes_admin.go,routes_frontend.go,routes_test.go}`、`internal/boot/seed.go`、`internal/logic/logic.go`、`manifest/config/config.yaml`、`Makefile`、`scripts/{lib.sh,up.sh,test.sh}`、`README.md`、`.gitignore`、`go.mod`/`go.sum`。
- 任务前已有修改的区分方式：上传业务代码自 `6e3f1f9`（CLEAN-003 修复）后未变；`dev-environment-setup` 增量已由该任务独立 CLEAN+ACCEPTED+PASS。合并态 C1=`0414bff` 为本次复审对象，不沿用旧 review 结论。
- 全局资源：错误码域 `17000-17999`（`origin/develop` Registry 已 RESERVED，owner=object-storage-upload，域序 17）；无 migration（V1 不落库）。
- 关键配置/迁移版本：schema 版本 `20261001000017`（`articles` 迁移后）；`qiniu` 段 AK/SK/bucket/domain 均为空默认值。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（凭证签发） | PASS | `TestUploadTokenHappyPath`：token/key/upload_url/domain/final_url/expires_at 齐全；解码 token 内嵌 put policy，scope=bucket:key、deadline≈expires_at、mimeLimit/fsizeLimit 与配置一致；后台端点同样可用；连续签发 key 不同（INV-004）。`go test -p 1 ./...` 通过。 |
| AC-002（真实端到端） | NOT_VERIFIED | Deliverer 里程碑（真实上传/删除 + 真实凭据）。Cleaner 不执行 Deliverer 里程碑验收。已核实真实凭据可用（`qiniu check` 经 `.env` 真实凭据 exit 0）、E2E 测试编译通过（`go vet -tags storage_e2e`）、链路逻辑审查无缺陷，待 Deliverer 经 `make test-storage` 执行。 |
| AC-003（鉴权拒绝） | PASS | `TestUploadTokenAuthBoundary`：未认证 401/1002；user token 打后台、admin token 打前台均 403/1003，不签发。 |
| AC-004（类型/大小边界） | PASS | `TestUploadTokenInvalidInput`：非法扩展名/非法 MIME/无扩展名 → 400/17001，不签发。大小上限经 token `fsizeLimit`（10485760）由七牛服务端执行（HappyPath 已断言）。 |
| AC-005（配置与凭据安全） | PASS | `config.yaml` qiniu 段 AK/SK/bucket/domain 空默认值；`git grep` 受跟踪文件无真实凭据；无 dotenv；错误信息只指认字段名/bucket（`qiniu.access_key 未配置`、`no such entry`），不泄漏凭据。 |
| AC-006（Secret 加载链路） | PASS | `git check-ignore -v .env` 命中（.gitignore:16），`.env.example` 未被忽略；`go.mod` 无 dotenv；缺 `.env` 时 `go build ./...`/`go test -p 1 ./...` 通过；env 覆盖 JSON 数组（`QINIU_ALLOWED_MIME_TYPES=["image/png"]`）被正确解析（实测 mimeLimit 生效）。 |
| AC-007（错误码域） | PASS | 三边一致：`origin/develop` Registry=`17000-17999` RESERVED（owner=object-storage-upload）；Contract=`17000-17999`（17001/17002/17003）；实现 `codes.go`=17001/17002/17003。无新增 migration。 |
| AC-008（长期设计） | PASS | `docs/design/storage.md` 与 APPROVED Contract、最终实现一致：上传模型、配置模型、Secret 边界、启动 fail-fast（D5）、两阶段初始化（D7）、`make test-storage` 真实 HTTP 链路（D8/D9）、错误码域。 |
| AC-009（环境传播边界） | PASS | task.md/README/storage.md 均明示 `.env` 为 Owner 本地 runtime setup、非 Git artifact、跨 Agent/worktree 不传播；构建与单测不以 `.env` 存在为通过前提（实测无 `.env` 环境通过）。 |
| AC-010（启动依赖与真实可用性检查） | PASS | `serve`（配超管密码、无七牛凭据）经 bootstrap 后 `ValidateConfig` fail-fast：exit 1 `qiniu.access_key 未配置`（不泄漏）；`ValidateConfig` 用指定 bucket 的 `GetBucketInfo`（非 `Buckets()`）；`qiniu check` 三态实测：缺配置 exit 1 / 真实凭据 exit 0 / bucket 不存在 exit 1 `no such entry`。 |
| AC-011（make init 第一阶段） | PASS | `make init` 实测：Docker/Compose 检查通过、容器启动、检测已有 `.env` 保留不覆盖、输出编辑提示、不启动后端、无交互 Secret 输入。 |
| AC-012（make init 幂等） | PASS | `make init` 前后 `.env` md5 不变（`df23f00a...`）；容器已运行幂等不破坏。 |
| AC-013（make up 第二阶段） | PASS | `up.sh` 顺序为 build → `qiniu check` → `admin check` → migrate → start，`set -euo pipefail` + 任一预检失败非零退出、不启动后端；`qiniu check` 三态实测（缺配置/真实凭据/bucket 不存在）符合 AC-013 语义。 |
| AC-014（make test-storage 独立 E2E） | NOT_VERIFIED | Deliverer 里程碑（真实上传/删除）。同 AC-002。`test-storage.sh` 校验四 `QINIU_*` 非空、缺任一 exit 1；E2E 测试编译通过、链路逻辑审查无缺陷，待 Deliverer 执行。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `gofmt -l`（新增/修改 Go 文件） | PASS | 无输出 |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `go vet -tags storage_e2e ./internal/cmd/...` | PASS | exit 0（E2E 测试编译通过） |
| `go test -p 1 ./...` | PASS | 全量通过（无 `.env` 环境），含 `internal/cmd`（55s）、`internal/migrations`（27s） |
| `make test`（test.sh 不 source lib.sh） | PASS | `scripts/test.sh` 不 source lib.sh（已读文件确认），`go vet`+`go test -p 1 ./...` 通过 |
| CLEAN-003 回归：`source scripts/lib.sh`（加载 .env 真实凭据）后 `go test -run TestUploadTokenMissingConfig` | PASS | `t.Setenv("QINIU_*","")` 隔离生效，返回 500/17002，不再因继承 .env 而误判（双保险修复验证） |
| `make test-lib` | PASS | `test-lib.sh 全部通过` |
| `bash -n scripts/{init,up,lib,test-lib,test-storage,test}.sh` | PASS | 语法通过 |
| `./bin/my-shop qiniu check`（无凭据） | PASS | exit 1 `qiniu.access_key 未配置`（不泄漏） |
| `./bin/my-shop qiniu check`（真实凭据，经 lib.sh 加载 .env） | PASS | exit 0 `七牛云配置与 bucket 可用性校验通过` |
| `./bin/my-shop qiniu check`（真实凭据 + 不存在 bucket） | PASS | exit 1 `七牛 bucket "..." 可用性检查失败: no such entry`（不泄漏凭据） |
| `./bin/my-shop serve`（无 Qiniu 凭据、无超管密码） | PASS | exit 1（先 fail 于 admin seed，属 dev-env 行为） |
| `ADMIN_SUPER_PASSWORD=<临时> ./bin/my-shop serve`（无 Qiniu 凭据） | PASS | bootstrap 通过后 `ValidateConfig` fail-fast：exit 1 `qiniu.access_key 未配置`（AC-010 精确证据）；已清理临时超管，DB 恢复 0 超管 |
| `make init`（.env 已存在） | PASS | exit 0，`.env` md5 不变（幂等），不启动后端，无交互输入 |
| env 覆盖 JSON 数组解析 | PASS | `QINIU_ALLOWED_MIME_TYPES=["image/png"]` 后 `TestUploadTokenHappyPath` 因 `image/jpeg` 不在白名单而 400/17001，证明 JSON 数组 env 覆盖被正确解析为 []string |
| `git check-ignore .env` / `.env.example` | PASS | .env 命中 ignore；.env.example 未被忽略 |
| `go.mod` 无 dotenv | PASS | 仅新增 `github.com/qiniu/go-sdk/v7` |
| Registry ↔ Contract ↔ 实现（错误码域） | PASS | `origin/develop` Registry=`17000-17999` RESERVED（owner=object-storage-upload）；Contract 一致；`codes.go`=17001/17002/17003。本地 feature 分支 registry 滞后于 develop 属预期（Reservation 仅落 develop），权威以 `origin/develop` 为准。`scripts/check-registry.sh` 读本地 registry 报 17001-17003 不在域内，为工具未取 develop 的假阳性，非真实漂移。 |
| 受跟踪文件无真实凭据 | PASS | `git grep` 受跟踪文件 QINIU_ 均为变量名/占位；`config.yaml` AK/SK 空；`.env` 为 gitignore 未跟踪文件 |

## Findings

### CLEAN-001（历史，已关闭）：bucket/domain 配置校验语义与 Contract/Design 漂移

- Severity：P2
- Status：CLOSED
- 说明：D5 的 `validateRequired`（`internal/logic/upload/upload.go`）已在启动 fail-fast 校验 bucket/domain；`ValidateConfig` = `validateStructural` → `validateRequired` → `checkBucketAvailable`。已与 Contract/Design 一致，`TestValidateRequired` 覆盖缺 bucket/domain 拒绝。

### CLEAN-002（历史，已关闭）：工作区 `manifest/config/config.yaml` 硬编码真实凭据

- Severity：P1
- Status：CLOSED
- 说明：当前 `manifest/config/config.yaml` qiniu 段 AK/SK/bucket/domain 为空默认值，无真实凭据残留；受跟踪文件 grep 无真实凭据。

### CLEAN-003（已关闭）：`TestUploadTokenMissingConfig` 在 `make test`（加载 `.env`）环境下失败

- Severity：P2
- Status：CLOSED
- 说明：双保险修复已落地并独立验证：(1) `scripts/test.sh` 不再 source `lib.sh`（避免 `.env` 真实凭据污染 `go test`）；(2) `TestUploadTokenMissingConfig` 用 `t.Setenv("QINIU_*","")` 隔离。实测 `source scripts/lib.sh`（加载真实 .env）后运行该测试返回 500/17002、PASS，原触发条件（继承 .env → 误判 200）已消除，回归通过。

无新 Finding。
