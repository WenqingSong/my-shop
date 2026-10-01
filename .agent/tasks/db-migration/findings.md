# Cleaner Findings

## Review Target

- Base commit：`14d2e7b921b23be045bd9f8e2d18b7ebe2a52486`（分支 `feat/spu`）
- 当前 HEAD：`eeddc32f53967434b43864359ab94423ca7f3195`（分支 `feat/db-migration`）
- 工作区状态：`git status --short` 为空（clean）
- 实现提交：`eeddc32 feat(db): 引入 golang-migrate 管理数据库迁移`
- 其余提交：`5066933`（contract/task 改 golang-migrate 方案）、`d6fe301`（.cnb.yml 加 start-dependencies）、`8d7cd29`/`fc67cf5`（任务文档）、`14d2e7b` 为基线
- 关键配置/版本：golang-migrate `v4.19.0`；baseline 迁移版本 `20261001000001`；7 张业务表 + `schema_migrations` 追踪表
- 区分方式：本任务新增/修改文件为 `internal/migrations/**`、`internal/cmd/cmd.go`、`internal/boot/boot.go`（移除建表）、`internal/boot/boot_migration_test.go`、`internal/boot/seed_test.go`、`internal/cmd/identity_isolation_test.go`、`scripts/lib.sh`、`scripts/up.sh`、`go.mod`/`go.sum`、`.cnb.yml`；与 `product-spu-v1` 无文件重叠（仅其 task.md 文档同步 Owner 决策）

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | 独立验证：清空库后 `./bin/my-shop migrate up` 建出 8 张表（7 业务表 + schema_migrations），`version=20261001000001, dirty=false`；`SHOW CREATE TABLE users/admin_roles` 与迁移前 DDL 逐字段等价（bigint unsigned/not null/auto_increment/default/唯一键/ENGINE=InnoDB/utf8mb4）；测试 `TestUpCreatesSchemaAndIsIdempotent` 通过 |
| AC-002 | PASS | `migrate up` 连续两次：第二次日志「没有待执行的 migration」返回 nil；测试 `TestUpCreatesSchemaAndIsIdempotent` 断言版本不变 |
| AC-003 | PASS | 测试 `TestUpAppliesOnlyPendingMigration` 在真实 MySQL 注入 `20261001000002_probe.up.sql`，断言仅版本前进到 02、`migration_probe` 建出、baseline 不重跑 |
| AC-004 | PASS | 测试 `TestUpFailsFastAndMarksDirty`（真实 MySQL）：broken 迁移 → Up 返回错误、dirty=true、再次 Up 拒绝、`force` 后恢复 |
| AC-005 | PASS | `internal/boot/boot.go` 无 `CREATE TABLE`（grep 确认仅测试探针含 DDL）；空库 `serve` 手动验证 exit=1 报「schema 未就绪」且未建任何表；seed 测试通过（超管=1、权限=16） |
| AC-006 | PASS | 测试 `TestConcurrentUp`（4 goroutine，真实 MySQL GET_LOCK）无报错、版本无重复；`go test -race -p 1` 无数据竞争 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 无错误 |
| `go vet ./...` | PASS | 无错误 |
| `go test -p 1 ./...` | PASS | 全包通过（依赖 MySQL/Redis 容器，健康状态 healthy） |
| `go test -race -p 1 ./internal/migrations/ ./internal/boot/` | PASS | 无数据竞争 |
| 手动 `migrate up` 空库 | PASS | 8 表创建，version=baseline，dirty=false |
| 手动 `migrate version` / `migrate force`（缺参/非法/合法） | PASS | 错误提示与成功路径均符合契约 |
| 手动 `serve` 空库 | PASS | fail-fast，未建表（exit=1） |
| 隔离运行 `go test ./internal/controller/admin/`（空库） | FAIL | 见 CLEAN-001 |

## Findings

### CLEAN-001：4 个既有测试包在独立运行时失败，依赖跨包执行顺序

- Severity：P2
- Status：OPEN
- Location：`internal/controller/admin/admin_test.go:107`、`internal/controller/categories/categories_test.go:130`、`internal/controller/iam/iam_test.go:179`、`internal/middleware/auth_test.go:83`
- AC / Invariant：AC-005（serve 只读 readiness）、任务「受影响的测试需随之调整」
- Trigger：空库上直接运行任一上述包（如 `go test ./internal/controller/admin/`），其 setup 调用 `boot.Bootstrap(ctx)`。
- Actual：`Bootstrap` 已不再建表，改为 `checkSchemaReady` 只读校验；空库上 `current=0 < latest` 直接返回错误「数据库 schema 未就绪」，测试 setup 在 `t.Fatalf("bootstrap: %v")` 处失败。
- Expected：这些测试与 `seed_test.go`、`identity_isolation_test.go` 一样，应在 `Bootstrap` 前先执行 `migrations.Up(ctx)`（或等价地确保 schema 就绪），使单包可独立运行。
- Impact：破坏「单独跑一个包」的常用开发/调试流程；`go test -p 1 ./...` 之所以通过，仅因为 `internal/boot` 包按序先运行并留下已迁移的库，形成隐蔽的跨包顺序依赖；注释「先 Bootstrap 一次确保 RBAC 表存在（幂等建表）」已过时误导维护者。一旦测试顺序改变或 CI 只跑变更包，将出现与本实现无关的失败。
- Evidence：空库 `DROP TABLE ...` 后运行 `go test -count=1 ./internal/controller/admin/`，全部用例 `--- FAIL`，错误均为 `bootstrap: 数据库 schema 未就绪（当前版本 0 < 最新版本 20261001000001）`。对照 `go test -p 1 ./...` 全绿（顺序依赖成立）。
- Required Fix Boundary：在 4 个测试文件的 setup 中，于首次 `boot.Bootstrap` 前调用 `migrations.Up(ctx)`（与 `seed_test.go`/`identity_isolation_test.go` 已采用的模式一致），并同步修正过时注释；不得为规避而删除/放宽 readiness check 或改回 `Bootstrap` 自动建表。

### CLEAN-002：结构等价性测试只校验唯一索引，未校验字段/类型/空值/默认值/引擎/字符集

- Severity：P2
- Status：OPEN
- Location：`internal/migrations/migrations_test.go:351` `TestSchemaStructureMatchesBaseline`
- AC / Invariant：INV-003（迁移后 7 张表字段/类型/空值/默认值/主键/唯一索引/引擎/字符集与迁移前严格等价）；Contract Verification Requirements 要求 `SHOW CREATE TABLE` / `information_schema` 逐一比对。
- Trigger：未来修改 baseline（或新增 ALTER 迁移）导致某列类型/长度/非空约束/默认值/引擎/字符集漂移时，运行测试。
- Actual：测试仅查询 `information_schema.statistics WHERE non_unique=0` 校验主键与唯一索引的「名称+列」，未读取 `information_schema.columns` / `SHOW CREATE TABLE`，因此列名、列类型、长度、`NOT NULL`、`DEFAULT`、`ENGINE`、`CHARSET/COLLATE` 均不在断言范围内。
- Expected：INV-003 的等价性断言应覆盖列定义（名称/类型/空值/默认值）与表级属性（引擎/字符集/排序规则），至少对关键列（如 `users.username`、`users.password_hash`、`admins.password_hash` 等）做字段级比对。
- Impact：本任务核心交付物「结构严格等价」被测试仅部分保护。例如把 `users.username` 从 `VARCHAR(24)` 改成 `VARCHAR(30)`、或去掉 `password_hash` 的 `NOT NULL`，当前测试仍会通过——无法识别这类「关键错误实现」，削弱回归保护。
- Evidence：测试文件 327-349 行 `uniqueIndexes` 仅查 `information_schema.statistics`（`non_unique=0`），断言 map 仅含 `PRIMARY`/`uk_*` 索引名与列；无任何列级断言。当前实现 DDL 经 Cleaner 手动比对确认等价（`SHOW CREATE TABLE users` 与迁移前 `createUsersTableSQL` 一致），故此为测试保护缺口而非当前结构错误。
- Required Fix Boundary：扩展该测试（或新增用例）读取 `information_schema.columns` 与表级 `ENGINE/CHARSET/COLLATE`，对 7 张表的关键列与表属性做等价断言；不改变生产 DDL，不规定具体断言实现方式。
