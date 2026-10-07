# Cleaner Findings

## Review Target

- 任务：`object-storage-upload`（文件上传与对象存储 Qiniu V1）
- 任务基线（base commit）：`7de6e74904e2cd95cfc931bb8cd4628c4e23a859`（`feat/object-storage-upload`，任务开始时 working tree clean）
- 审查对象（implementation Evidence Commit C1）：`6890d9d16dfe514e3c7f7c6e50131383231b82b7`（`feat(upload): 接入七牛云直传凭证签发`）
- Coder metadata commit（C2，非审查对象）：`6e2bab8`（`chore(workflow): 发起 Review Request`）
- 审查范围：`7de6e74..6890d9d` 的完整变更（生产代码 + 测试 + `docs/design/storage.md` + `manifest/config/config.yaml` qiniu 段）
- 新增文件：`api/upload/v1/upload.go`、`internal/controller/upload/upload.go`、`internal/logic/upload/{upload.go,upload_test.go}`、`internal/service/upload.go`、`internal/cmd/upload_test.go`、`docs/design/storage.md`
- 修改文件：`internal/codes/codes.go`、`internal/cmd/{cmd.go,routes_admin.go,routes_frontend.go,routes_test.go}`、`internal/logic/logic.go`、`manifest/config/config.yaml`、`go.mod`/`go.sum`
- 全局资源：错误码域 `17000-17999`（`origin/develop` Registry 已 RESERVED，owner=object-storage-upload）；无 migration（V1 不落库）
- **当前工作区额外状态**：`manifest/config/config.yaml` 存在未提交修改（写入了真实七牛凭据，见 CLEAN-002）

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（凭证签发） | PASS | `TestUploadTokenHappyPath` 独立运行通过：返回 token/key/upload_url/domain/final_url/expires_at 齐全；解码 token 内嵌 put policy，`scope=bucket:key`、`deadline≈expires_at`、`mimeLimit`/`fsizeLimit` 与配置一致；后台端点同样可用 |
| AC-002（上传与可访问） | NOT_VERIFIED | 需真实七牛凭据并实际向七牛上传小文件；属 Deliverer 里程碑验收，Cleaner 不执行。当前工作区凭据注入方式本身违规（见 CLEAN-002），未进行实际上传 |
| AC-003（鉴权拒绝） | PASS | `TestUploadTokenAuthBoundary` 独立运行通过：未认证 401/1002；user token 打后台端点、admin token 打前台端点均 403/1003，不签发 |
| AC-004（类型/大小边界） | PASS | `TestUploadTokenInvalidInput` 通过：非法扩展名/非法 MIME/无扩展名 → 400/17001。大小上限无请求声明参数（API 仅 filename/content_type），经 token `fsizeLimit` 由七牛服务端执行（HappyPath 断言 `fsizeLimit=10485760`），符合 Contract「固化进 token scope」语义 |
| AC-005（配置与凭据安全） | PASS（C1） | `TestUploadTokenMissingConfig` 通过：缺凭据 → 500/17002，响应 message 为码表安全文案、不含凭据；C1 的 `config.yaml` qiniu 段为空默认值、无硬编码。**但当前工作区 `config.yaml` 被写入真实凭据，见 CLEAN-002（C1 之外的独立安全问题）** |
| AC-006（错误码域与迁移） | PASS | 三边一致：Registry（`origin/develop`）= 17000-17999 RESERVED；Contract = 17000-17999（17001/17002/17003）；实现 `internal/codes/codes.go` = 17001/17002/17003。无新增 migration（不落库），`internal/migrations` 测试通过 |
| AC-007（长期设计） | FAIL | `docs/design/storage.md` 已存在且主体与 Contract/实现一致，但 bucket/domain 校验语义与实现漂移（见 CLEAN-001） |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `gofmt -l`（全部新增/修改 Go 文件） | PASS | 无输出 |
| `go test -p 1 ./...` | PASS | 全量通过，含 `internal/cmd`（46s）、`internal/migrations`（17.5s） |
| `go test ./internal/logic/upload/...` | PASS | ok |
| `go test ./internal/cmd/... -run 'TestUploadToken\|TestRouteTable'` | PASS | 鉴权边界/HappyPath/非法输入/缺凭据/路由表均通过（需 MySQL+Redis，docker 已就绪） |
| 三边一致性（Registry↔Contract↔实现） | PASS | `git show origin/develop:.agent/registry/error-codes.md` 含 17000-17999 RESERVED |
| `scripts/check-registry.sh` | 见说明 | 本地 feature 分支的 `.agent/registry/error-codes.md` 未含 17000-17999（Reservation 仅落 develop，feature 分支 registry 为基线态），故脚本报 17001/17002/17003「不在任何 ACTIVE/RESERVED 域内」。这是本地 registry 滞后于 develop 的预期现象，非真实漂移；权威判断以 `origin/develop` Registry 为准 |
| AC-002 真实七牛上传 | NOT_EXECUTED | 需真实凭据 + 实际上传，属 Deliverer 里程碑验收 |

## Findings

### CLEAN-001：bucket/domain 配置校验语义与 Contract/Design 漂移

- Severity：P2
- Status：OPEN
- Location：`internal/logic/upload/upload.go` `validateStructural()`（L135-154）与 `validateCredentials()`（L156-162）
- AC / Invariant：AC-007（长期设计与实现一致）、Contract Recommendation §5、`docs/design/storage.md` §3 配置校验语义
- Trigger：服务启动时 `service.Upload().ValidateConfig(ctx)` 只校验 region/ttl/大小/白名单，不校验 bucket/domain；bucket/domain 的缺失校验被放到签发时的 `validateCredentials()`。
- Actual：bucket/domain 被当作「凭据类」懒校验（签发时才 17002），未在启动时 fail-fast。实现注释明确写「凭据（AK/SK）与 bucket/domain 属运行时懒校验」。
- Expected：Contract 与 Design 均明确把 bucket/domain 归入「非机密结构配置（bucket/domain/ttl/大小/白名单）」，要求「格式非法时启动 fail-fast」。实现应服从 Design（bucket/domain 启动校验），或经 Contract Revision 明确改为懒校验。
- Impact：四者一致性（Task↔Contract↔Design↔实现）在 bucket/domain 校验语义上断裂；生产环境 bucket/domain 为空时服务可启动、仅请求时才发现 17002，配置错误延迟暴露。
- Evidence：`contract.md` Recommendation §5 与 `storage.md` §3 均写「非机密结构配置（bucket/domain/ttl/大小/白名单）若格式非法，启动时 fail-fast」；`upload.go:69-71` `ValidateConfig` 仅调 `validateStructural()`，`upload.go:137-154` 不含 bucket/domain。
- Required Fix Boundary：使 bucket/domain 校验语义与 Contract/Design 一致——要么在启动 `validateStructural` 中 fail-fast 校验 bucket/domain，要么走 Contract Revision（Analyst + Owner）将 bucket/domain 明确改为懒校验。不得静默保留与已批准 Design 冲突的行为，也不得自行改 Design/Contract。

### CLEAN-002：工作区 `manifest/config/config.yaml` 被硬编码真实七牛凭据（含 secret_key）

- Severity：P1
- Status：OPEN
- Location：`manifest/config/config.yaml`（当前 working tree，未提交，非 C1 内容）
- AC / Invariant：INV-003 / AC-005（secret_key 仅环境变量注入、不提交仓库、不进 config 默认值）
- Trigger：有人将真实七牛 `access_key`/`secret_key`/`bucket`/`domain` 直接写入受跟踪文件 `config.yaml`（`git status --short` 显示 `M manifest/config/config.yaml`）。
- Actual：真实 `secret_key` 明文出现在受跟踪文件的 working tree 副本中。
- Expected：`secret_key` 仅经环境变量 `QINIU_SECRET_KEY` 注入；`config.yaml` 保持空默认值（C1 即如此）。
- Impact：一旦该文件被 `git add`/`git commit` 提交，凭据将永久进入 git 历史（安全泄漏）；违反 INV-003。同时使 working tree 不再 clean，阻塞后续 HANDOFF 的「working tree clean」前提。
- Evidence：`git --no-pager diff manifest/config/config.yaml` 显示 access_key/secret_key/bucket/domain 由空串改为真实值；`domain` 为真实七牛 CDN 域名。
- Required Fix Boundary：恢复 `config.yaml` qiniu 段为空默认值（与 C1 一致），凭据改经环境变量 `QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY`/`QINIU_BUCKET`/`QINIU_DOMAIN` 注入；该文件不得被任何角色提交。此问题非 C1 缺陷，属 working tree 状态，需先清理后方可进入 HANDOFF。
