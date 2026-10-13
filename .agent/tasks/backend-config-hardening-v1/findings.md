# Cleaner Findings

## Review Target

- 分支：`feat/backend-config-hardening-v1`
- Task 基线（Base）：`359b8cceeebefe7b490f783c1ad5a118afd6e31e`
- Contract（APPROVED）：`c29ddca5d44da8be4a7eb4f74a951aa703dbbd9e`
- Design Artifact Commit：`d49bd7592dd7d1c0c68dcc1a87776355be10f52d`
- **Implementation Evidence Commit C1（本轮审查对象）**：`cee1e6aed2a7527998d662cd69347892ec9fa4c1`
- Coder Metadata Commit C2：`e169b96ce4f1df227302a45bf3b04dcf81e3a680`
- 审查时工作区状态：`HEAD = e169b96（C2）`，working tree clean（审查期间临时创建的 `.env` 已 gitignore，不计入变更）
- 任务开始前已有修改：无（Review Baseline 记载 clean）
- 变更区分方式：生产/测试变更集中于 C1（`git diff 359b8cc..cee1e6a`），C2 仅改 `state.yaml` 的 `review.*`
- 全局资源：`resources.reservations = {}`；C1 未新增 migration、未改 `internal/codes`，无 migration_version / error_code_domain 占用，三边一致性检查不适用

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `auth.Secret()` 空值 fail-fast（既有逻辑）+ 新增 `TestSecretRejectsMissing`；`Bootstrap` 调用 `auth.Secret()` 出错即返回，serve 不监听端口。 |
| AC-002 | PASS | `auth.Secret()` 对 `<32` 字节返回错误（既有），`TestSecretRejectsTooShort` 保留；`make test` 中 `internal/auth` 通过。 |
| AC-003 | PASS | `internal/boot/applyDatabaseConfig`、`internal/migrations/buildDSN`、`internal/logic/legacymigrate/OpenConnections` 均移除 `defaultDBPass="root"` 并对空密码 fail-fast；`TestApplyDatabaseConfigRejectsEmptyPass`、`TestBuildDSNRejectsEmptyPass`、`TestOpenConnectionsRejectsEmptyPass` 通过。migrate 子命令直连 `migrations.Up`（不经过 Bootstrap），不校验 JWT，符合 Contract Decision 4。 |
| AC-004 | PASS | compose 改用 `${MYSQL_ROOT_PASSWORD:?}` / `${AUTH_JWT_SECRET:?}`，无 `${VAR:-default}`；独立验证：无 `.env` 时 `docker compose ps` 报 `MYSQL_ROOT_PASSWORD required`；`test-container.sh` 静态断言无 `:-` 回退。 |
| AC-005 | PASS | `scripts/test.sh` 显式 `export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-root}"`（隔离测试环境注入，不依赖应用/迁移代码默认值）；`go test -p 1 ./...` 全量通过（含 boot/migrations/legacymigrate 集成测试连真实 MySQL）。 |
| AC-006 | PASS | compose `DATABASE_DEFAULT_CHARSET=utf8mb4`、`config.yaml charset: utf8mb4`、`boot.go`/`migrations.go` `defaultDBCharset=utf8mb4`、`legacymigrate/db.go` DSN `charset=utf8mb4`；`TestBuildDSNCharsetUTF8MB4`（migrations+legacymigrate）通过，`test-container.sh` 断言 charset 非 utf8。 |
| AC-007 | PASS | `TestUTF8MB4FourByteRoundtrip` 向 utf8mb4 表写入 `😀 🎉 𝄞` 并读回一致（真实 MySQL，实测 PASS）；Mutation 验证：将 `config.yaml charset` 改回 `utf8` 后该测试失败（`Conversion from collation utf8mb3_general_ci ... impossible`），可区分正确/错误实现。 |
| AC-008 | PASS | 独立执行 `go vet ./...`（exit 0）、`go test -p 1 ./...`（全 ok）、`go build ./...`（exit 0）；IAM/订单/秒杀等既有测试无回归。 |
| AC-009 | PASS | `docs/design/deployment.md` §3/§10、`docs/design/migration.md` §5 与最终实现一致（无内置默认回退 + utf8mb4 + init.sh 双层 fail-fast），四者（Task↔Contract↔Design↔实现）一致。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `git show cee1e6a --stat` 全量 Diff | PASS | 17 个文件，均为 Scope 内（配置/脚本/测试/设计） |
| `go vet ./...` | PASS | exit 0 |
| `go test -p 1 ./...` | PASS | 全包 ok（含新增负向/回归测试与既有 IAM/订单等） |
| `go build ./...` | PASS | exit 0 |
| 新增测试显式运行（`-count=1 -v`，注入 `DATABASE_DEFAULT_PASS=root`） | PASS | `TestSecretRejectsMissing`/`TestApplyDatabaseConfigRejectsEmptyPass`/`TestBuildDSNRejectsEmptyPass`/`TestBuildDSNCharsetUTF8MB4`/`TestUTF8MB4FourByteRoundtrip`(2.5s 连真实 MySQL)/`TestOpenConnectionsRejectsEmptyPass` 全部 PASS |
| `scripts/test-container.sh` / `test-prometheus.sh` | PASS | 静态断言通过（含 AC-004 无回退、AC-006 字符集） |
| `scripts/test-nginx.sh`（经 `bash` 显式执行） | PASS | 静态断言通过（见 CLEAN-002：脚本非可执行位为既有问题） |
| `docker compose ps`（无 `.env`） | PASS | 报 `MYSQL_ROOT_PASSWORD required`，证实 fail-fast |
| Mutation A：`config.yaml charset` 改回 `utf8`（隔离 worktree） | PASS | `TestUTF8MB4FourByteRoundtrip` 失败（utf8mb3 collation 无法写入 4 字节） |
| Mutation B：移除 `boot.go` 空密码 fail-fast（隔离 worktree） | PASS | `TestApplyDatabaseConfigRejectsEmptyPass` 失败 |
| 全局资源三边一致性 | N/A | 未占用 migration_version / error_code_domain |

## Findings

### CLEAN-001：`scripts/import-legacy.sh` 仍残留 `MYSQL_ROOT_PASSWORD:-root` 回退

- Severity：P3
- Status：OPEN
- Location：`scripts/import-legacy.sh` L11 `MYSQL_ROOT_PASSWORD="${MYSQL_ROOT_PASSWORD:-root}"`
- AC / Invariant：Goal「消除 MySQL root 密码内置默认回退」（Scope 明确限定「Go 应用与 Migration」，本脚本不在 Contract Allowed Changes 清单）
- Trigger：在无 `.env`、未导出 `MYSQL_ROOT_PASSWORD` 的干净 shell 直接运行该脚本
- Actual：回退到 `root` 作为 mysql 客户端密码
- Expected：缺失时显式报错，不回退内置默认值（与主路径一致）
- Impact：低。该脚本不在本任务 Scope（Contract「Verified Current Behavior」未列它，Allowed Changes 未含它）；且 `init.sh` 已强制 `.env` 含 `MYSQL_ROOT_PASSWORD`，脚本经 `source lib.sh` 加载 `.env` 后 `:-root` 实际为死代码，触发场景会以认证失败终止而非静默成功
- Evidence：`grep -n 'MYSQL_ROOT_PASSWORD.*root' scripts/import-legacy.sh` → L11
- Required Fix Boundary：是否纳入范围由 Owner 决定；若纳入，则与主路径一致改为「缺失 fail-fast」，不规定实现细节

### CLEAN-002：`scripts/test-nginx.sh` 无执行位，`make test` 末步报 Permission denied（既有问题）

- Severity：P3
- Status：OPEN
- Location：`scripts/test-nginx.sh`（git mode `100644`）
- AC / Invariant：AC-005/AC-008 的 `make test` 整体退出码（核心 `go test` 已全部通过，不影响本任务 AC 的实质验证）
- Trigger：`make test` 运行至第 39 行直接调用 `"${SCRIPT_DIR}/test-nginx.sh"`
- Actual：`Permission denied`，`make test` 以 126 退出（所有 `go test`、容器化/Prometheus 静态断言均已通过）
- Expected：脚本应具备可执行位，或 `test.sh` 经 `bash` 显式调用
- Impact：低。**既有问题**（`git ls-tree 359b8cc scripts/test-nginx.sh` = `100644`，与本次变更无关），不影响本任务 AC 验证结论，但影响 CI 的 `make test` 整体退出码
- Evidence：`ls -l scripts/test-nginx.sh` → `-rw-r--r--`；`git ls-tree 359b8cc` 与 `cee1e6a` 均为 `100644`；`bash scripts/test-nginx.sh` 可正常通过
- Required Fix Boundary：属既有仓库问题、超出本任务 Scope，是否修复由 Owner 决定

### 备注（不构成 Finding）

- `README.md` L139 仍描述「容器经 compose 注入 `${MYSQL_ROOT_PASSWORD:-root}` 开发默认值」，与本任务后的真实行为不一致（README 不在 Scope，属文档残留，建议 Owner 视需要更新）。
- Contract「Failure and Consistency Semantics」§`migrate up` 描述「DSN 密码为空 → MySQL 认证失败」，而实现/设计采用更优的「空密码 fail-fast（指认 DATABASE_DEFAULT_PASS）」（Contract Recommendation #2 与 Decision 4 明确），二者语义一致（非零退出、不回退 root），属 Contract 内部措辞滞后，非行为漂移。
- Contract「Allowed Changes」清单未列 `scripts/test-container.sh` / `test-nginx.sh` / `test-prometheus.sh`，但 Coder 为保持 compose `${VAR:?}` 变更后 `make test` 仍可通过，对这三处仅注入占位值以通过 `docker compose config -q`（非真实凭据、测试脚本、最小必要改动），视为必要的相邻改动，未混入无关重构。
