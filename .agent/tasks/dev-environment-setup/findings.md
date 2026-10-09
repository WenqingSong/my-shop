# Cleaner Findings

## Review Target

- 审查对象（C1，immutable implementation Evidence Commit）：`fb5bbee68ec31893aa7ddcd0e8bb7400c6a2b222`（短 `fb5bbee`，提交信息 `feat(dev-env): 修复开发环境配置与初始化流程`）
- 当前 HEAD（C2，Coder 的 metadata commit，仅改 `state.yaml`）：`6e75b31465984f87f30a0e5264df6c1bd792f8fc`
- 任务基线 Base：`d6a6e26e28c9d382fbdef84806fd608a63318d11`
- 分支：`feat/dev-environment-setup`；`origin/feat/dev-environment-setup` = `97af077`（本地领先 C1/C2，出站收尾 push 处理）
- 任务前已有修改区分：基线上 working tree clean；C1 的变更面为 `.env.example`、`Makefile`、`internal/boot/*`、`internal/cmd/cmd.go`、`scripts/{init,lib,test,test-lib,up}.sh`，均属本任务 Scope（Contract 已授权新增 `internal/boot`/`internal/cmd` 的 Go 预检代码）。
- 全局资源：`Design Impact = NONE`；`state.yaml.resources.reservations = {}`；本任务不新增错误码域/migration，未触碰 `internal/codes`、`internal/migrations`。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | 静态核验：`.env.example` 31 个键全部为未注释 `KEY=VALUE`，与 `manifest/config/config.yaml` 及 `internal/boot/boot.go`/`cmd.go`/`auth/*`/`upload.go`/`order.go` 的 `GetEffective`/`MustGet` 读取点一一对应，命名遵循 `.`→`_` 大写；加载优先级在头注释（L6-9）明确。 |
| AC-002 | PASS | `.env.example` 全部未注释；普通配置填开发默认值；`QINIU_SECRET_KEY`/`AUTH_JWT_SECRET`/`ADMIN_SUPER_PASSWORD` 及七牛 4 必填项留空；全文无真实凭据（已通读，secrets 均为空）。 |
| AC-003 | PASS | 根因实证：GoFrame `GetEffective` 对「空但已设置」的环境变量返回空串（覆盖 config.yaml 默认 `c49d0fea…`），实测确认。`lib.sh` `_load_env_file` 增加空值跳过（L54 `[[ -n "${val}" ]] || continue`），`make test-lib`（`scripts/test-lib.sh`）全通过，含空值不导出/非空导出/引号剥离/注释剥离/已导出优先五场景。 |
| AC-004 | PASS | 实测：`.env` 已存在时 `make init` 保留不覆盖（前后 md5 一致 `df23f00a…`）；删除 `.env` 后 `make init` 从 `.env.example` 生成（`diff` 完全一致，31 键）；可重复执行。 |
| AC-005 | PASS | 实测：构造旧 `.env`（非七牛键注释态）后 `make init` 一次性列出全部缺键（ADMIN_SUPER_PASSWORD/AUTH_JWT_SECRET/…共 26 键）与必填空项（仅 ADMIN_SUPER_PASSWORD），只打印键名不打印已填 Secret 值。 |
| AC-006 | PASS | 实测：`make up` 无超管+空密码+缺七牛时，`qiniu check` 与 `admin check` 两项都执行、一次性报告，最终非零退出（exit 2）且未启动后端；`up.sh` 顺序 = deps → build → qiniu check → admin check → migrate → start_app，与 Contract 一致。 |
| AC-007 | PASS | 实测：预检失败输出仅含键名/依赖名（`qiniu.access_key 未配置`、`ADMIN_SUPER_PASSWORD` 提示），无 Secret 值/密码/token/内部路径/堆栈（`admin check`/`qiniu check` 经 gcmd 仅打印 error message）。 |
| AC-008 | PASS | Go 测试三态覆盖：已存在→通过（空密码仍通过）、不存在+空密码→fail-fast 指认 `ADMIN_SUPER_PASSWORD`、不存在+非空密码→通过；`admins` 表不存在(1146)→视为不存在（`TestSuperAdminExistsTableNotExist`）。二进制实测：空密码 `admin check` exit 1；已存在超管后空密码 `admin check` exit 0；`seedSuperAdmin` 幂等/不覆盖密码由既有 `TestSeedSuperAdminIdempotent`/`Concurrent` 覆盖。 |
| AC-009 | PASS | 实测：`make init` 生成 `.env` 无误；真实七牛凭据下 `make up` 全链路成功（qiniu check 真实 GetBucketInfo 通过 → admin check 通过 → migrate → 启动 → `/health` 返回 `code:0`），`make up exit=0`；验证后已清理生成的超管与 storage 产物。 |
| AC-010 | PASS | 实测：`.env` 含真实七牛凭据时 `make test`（`go vet` + `go test -p 1 ./...`）全通过（exit 0）；`scripts/test.sh` 刻意不再 `source lib.sh`，不加载 `.env`、不传播真实凭据。 |
| AC-011 | PASS | `scripts/test-storage.sh` 未被本任务改动（保留）；缺凭据时实测 `make test-storage` 非零退出（exit 2）并逐项指认 `QINIU_ACCESS_KEY/SECRET_KEY/BUCKET/DOMAIN`，不以退出 0 冒充通过。真实七牛正向 E2E 属 Deliverer 里程碑，本轮不执行。 |
| AC-012 | PASS | 实测：各缺项场景输出均指认具体键名+期望动作（如 `请先在 .env 中填写后再重试`、`请先执行 make init…`），无 Secret、无需猜测。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `bash -n scripts/*.sh` | PASS | 15 个脚本全部语法通过 |
| `bash scripts/test-lib.sh`（make test-lib） | PASS | `test-lib.sh 全部通过` |
| `gofmt -l .` | PASS | 无输出（全部已格式化） |
| `go build ./...` | PASS | BUILD OK |
| `go vet ./...` | PASS | VET OK |
| `go test -p 1 ./...` | PASS | 全部包 `ok`（含 `internal/boot`、`internal/cmd`、`internal/migrations` 等，无 FAIL） |
| `make test`（.env 含真实七牛凭据） | PASS | exit 0，验证 AC-010 隔离 |
| `make init`（.env 已存在） | PASS | .env 保留、md5 不变；缺键/必填提示正确 |
| `make init`（.env 不存在） | PASS | 从 `.env.example` 生成且 `diff` 一致 |
| `make up`（缺超管密码+空七牛） | PASS | 双预检一次性报告、exit 2、未启动后端 |
| `make up`（真实七牛 + ADMIN_SUPER_PASSWORD） | PASS | exit 0，`/health` 返回 `code:0`，超管 `is_super=1` 正确创建 |
| `my-shop admin check`（空密码/已存在） | PASS | 分别 exit 1 / exit 0，语义正确 |
| `make test-storage`（缺凭据） | PASS | exit 2，逐项指认 QINIU_* |
| GoFrame 空值覆盖根因 | PASS | 临时程序实证：`AUTH_JWT_SECRET=""` 时 `GetEffective` 返回空串覆盖 config.yaml 默认值；unset 时回退默认 |
| 全局资源三边一致性 | N/A | 本任务 `Design Impact = NONE`、无 Reservation、未触碰 `internal/codes`/`internal/migrations`，无资源需比对 |

## Findings

No actionable findings.

- 注（非阻塞，不在本任务 Scope）：`make up` 启动会在 `./storage/` 生成 banner 占位图，而 `.gitignore` 未忽略 `storage/`，属 object-storage-upload 既有行为，本任务 Out of Scope 明确不改 `internal/storage`，未作为 Finding；本轮验证产生的 `storage/` 已清理。
