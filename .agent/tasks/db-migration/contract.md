# Technical Contract

## Decision Status

APPROVED

## Problem

把 `internal/boot/boot.go` 中 7 张既有表的幂等建表逻辑，迁移为基于 **golang-migrate v4** 的数据库 Migration 机制；Migration 与 Web Serve 职责分离（`my-shop migrate up` 先行，成功后 `my-shop serve` 启动）；启动 serve 不再自动建表，只做只读 schema readiness check；seed 保留在 serve 但以 migration 已成功为前提；后续任务（商品 SPU）通过该机制新增 `products`/`product_images`。迁移后 7 张表结构与迁移前完全等价，seed 语义不变。

本文件是 **CONTRACT_REVISION**：原 Contract（自研 Runner + `schema_migrations` + `GET_LOCK`）与 Owner 已确认的「不自研、采用 golang-migrate v4」方向冲突，已废弃并替换；修订后由 Owner 批准（2026-10-01）。

## Verified Current Behavior

- VERIFIED：`internal/boot/boot.go` 中 `Bootstrap` 顺序为 `applyServerConfig → applyDatabaseConfig → applyRedisConfig → auth.Secret → waitForDependencies → ensureTables → seedSuperAdmin → seedPermissions`；`ensureTables` 用 `CREATE TABLE IF NOT EXISTS` 幂等建 7 张表，共 7 个 DDL 常量 + 7 个 `ensureXxxTable` 函数。
- VERIFIED：7 张表为 `users`、`categories`、`admins`、`roles`、`permissions`、`admin_roles`、`role_permissions`，均为 `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`，含各自 `PRIMARY KEY` 与 `UNIQUE KEY`（`uk_username`/`uk_parent_name`/`uk_admin_username`/`uk_role_name`/`uk_permission_code`，关联表用复合主键）。
- VERIFIED：`internal/boot/seed.go` 的 `seedSuperAdmin`/`seedPermissions` 用「先查再写 + MySQL 1062 唯一约束兜底」幂等，`isDuplicateKeyError` 判定 1062；`seed_test.go` 的 `setupAdminSeed` 直接调用 `applyDatabaseConfig` + `ensureTables` + `DELETE FROM admins`。
- VERIFIED：CLI 由 GoFrame `gcmd.Command` 组织（`internal/cmd/cmd.go` 的 `Main`，当前仅一个 `main` 命令直接启动 HTTP）；`main.go` 调 `cmd.Main.Run(gctx.GetInitCtx())`；二进制名为 `my-shop`（`scripts/lib.sh` `APP_BIN`）。
- VERIFIED：`scripts/up.sh`/`bootstrap.sh` 流程为 `docker compose up → wait_for_deps → build_app → start_app`，`start_app` 用 `nohup "${APP_BIN}"`（无参数）后台启动；`scripts/test.sh` 用 `go test -p 1 ./...` 串行执行。
- VERIFIED：`go.mod` 目前无 golang-migrate 依赖；已有 `go-sql-driver/mysql v1.7.1` 可被 golang-migrate 的 mysql driver 复用。`docker-compose.yml` 为 `mysql:8.0` + `redis:7-alpine`，环境每日重置（CNB DinD），migration 必须能从空库重复执行。
- VERIFIED（golang-migrate v4 行为，经官方文档核实）：迁移文件命名 `{version}_{title}.up.{ext}`，`version` 为 64 位无符号整数，按数字升序应用；追踪表 `schema_migrations` 结构为 `version bigint`（主键）+ `dirty bool`；MySQL driver **默认用 `GET_LOCK`/`RELEASE_LOCK` 串行化并发迁移**（`x-no-lock=true` 关闭，V1 保持默认加锁）；`force <version>` 直接设置版本号与 `dirty=false`，**不执行任何 SQL**；`Up()` 首次执行时自动创建 `schema_migrations`；多语句迁移文件需在 DSN 启用 `multiStatements=true`；`migrate.Up()` 只读取 up 文件，不要求 down 文件成对存在。
- VERIFIED：前置依赖——`product-spu-v1/contract.md` 已由 Owner 确认「方案 B」，本任务是 SPU 的阻塞前置项。
- UNKNOWN：golang-migrate v4 在本仓库 Go 1.23.0 下的精确版本号需 Coder 落地时锁定（以 `go get` 最新 stable v4 为准），不影响设计选型。

## Recommendation

RECOMMENDATION：**采用 golang-migrate v4 作为库嵌入 GoFrame CLI，不自研 Runner**。逐项固化如下：

1. **机制选型**：引入 `github.com/golang-migrate/migrate/v4`（含 `database/mysql`、`source/iofs`），删除原自研方案。golang-migrate 自带有序执行、`schema_migrations` 追踪、dirty 状态与 MySQL `GET_LOCK` 并发保护，无需自研任何 Runner/追踪表/锁。

2. **CLI 结构（职责分离）**：`internal/cmd` 重构为子命令：
   - `my-shop migrate up` —— 执行所有未应用 migration；
   - `my-shop migrate force <version>` —— 标记版本已应用（baseline 接管 / dirty 恢复），不执行 SQL；
   - `my-shop migrate version` —— 只读打印当前版本与 dirty 状态；
   - `my-shop serve`（含无参数默认）—— 启动 HTTP 服务（readiness check + seed + 路由）。
   - 不暴露 `migrate down`。

3. **迁移包接口**：新增 `internal/migrations` 包，封装 golang-migrate，导出 `Up(ctx) error`、`Force(ctx, version uint) error`、`Status(ctx) (current uint, dirty bool, latest uint, err error)`（只读）。DSN 从 `database.default.*` 配置构造（复用 `go-sql-driver/mysql` 的 `mysql.Config.FormatDSN` 避免手工拼 URL），启用 `multiStatements=true`。迁移文件经 `//go:embed sql/*.sql` 内嵌，`iofs` source 挂载，运行时不依赖工作目录。

4. **目录与命名**：`internal/migrations/sql/`，文件 `{version}_{title}.up.sql`，version 用 **14 位时间戳**（`YYYYMMDDHHMMSS`，如 `20261001000001`），数字升序即时间升序。只提供 `.up.sql`，不提供 `.down.sql`。

5. **baseline 接管**：`<YYYYMMDDHHMMSS>_baseline.up.sql` 内容 = 原 7 张表 DDL（`CREATE TABLE`，去掉 `IF NOT EXISTS`），多语句单文件。新环境（空库）`migrate up` 从 baseline 建 7 表；已有环境（7 表已存在、无 `schema_migrations`）由人工显式 `migrate force <baseline 时间戳>` 标记已应用（不执行 DDL、不自动 force），此后新增 migration 正常执行。

6. **serve 只读 readiness check（严格模式）**：`Bootstrap` 删除 `ensureTables`，改为调 `migrations.Status(ctx)` 只读检查——`schema_migrations` 存在、`dirty=false`、`current >= latest`，任一不满足即 fail-fast 报错并明确提示先执行 `my-shop migrate up`（dirty 时提示需人工 `force`）。serve **不执行任何 DDL、不建表、不自动 migrate、不自动 force**。

7. **seed 归属**：`seedSuperAdmin`/`seedPermissions` 保留在 `Bootstrap`，位于 readiness check **通过之后**、HTTP 启动之前，逻辑与内容不变。

8. **部署顺序**：`scripts` 在 `build_app` 之后、`start_app` 之前插入 `migrate_app`（`"${APP_BIN}" migrate up`）；`start_app` 改为 `"${APP_BIN}" serve`。

关键取舍：**成熟工具 vs 自研** 已由 Owner 定论，不再讨论。本方案剩余实质取舍是 **baseline 接管方式**——已有环境用「人工显式 `force`」而非「自动探测后 force」，代价是多一步人工命令，换取「绝不误标记未执行迁移为已应用」的安全性与可审计性；daily-reset 环境只走空库 `up`，几乎无额外成本。

## Selected Design

已由 Owner 确认并冻结：

| 项 | 决定 | 状态 |
| --- | --- | --- |
| 机制 | golang-migrate v4（库嵌入，不自研） | Owner 已确认 |
| CLI | `migrate up/force/version` + `serve`（无参数默认 serve） | Owner 已确认 |
| 命名 | 14 位时间戳 version（`YYYYMMDDHHMMSS`），仅 up 文件 | Owner 已确认 |
| baseline | 已有环境人工 `force <baseline 时间戳>`，不自动 force | Owner 已确认 |
| serve | 严格只读 readiness check（未初始化 / dirty / 版本落后均拒绝启动） | Owner 已确认 |
| seed | 保留在 serve，readiness 通过后执行 | Owner 已确认 |
| 部署 | build → migrate up → serve | Owner 已确认 |
| Task/AC | migration 不再由 serve 启动自动执行，改为显式 `migrate up` 成功后 `serve` | Owner 已确认 |

## Interfaces and Data

### CLI 命令契约

```text
my-shop migrate up              # 应用未执行 migration，失败 fail-fast
my-shop migrate force <version> # 标记版本已应用，不执行 SQL（baseline/dirty 恢复）
my-shop migrate version         # 打印当前 version 与 dirty 状态
my-shop serve                   # 启动 HTTP（readiness check + seed + 路由）
my-shop                         # 无参数等价 serve（保持向后兼容）
```

### `internal/migrations` 包

- `Up(ctx context.Context) error`
- `Force(ctx context.Context, version uint) error`
- `Status(ctx context.Context) (current uint, dirty bool, latest uint, err error)`（只读）

DSN 从 `database.default.{host,port,user,pass,name,charset}` 配置构造，启用 `multiStatements=true`；迁移文件经 `//go:embed sql/*.sql` 内嵌。

### Migration 文件

- 目录：`internal/migrations/sql/`
- 命名：`{version}_{title}.up.sql`，version 为 14 位时间戳（`YYYYMMDDHHMMSS`），数字升序即时间升序
- `<YYYYMMDDHHMMSS>_baseline.up.sql`：7 张表 DDL，多语句单文件
- 追踪表 `schema_migrations`（`version bigint PK` + `dirty bool`）由 golang-migrate 自动创建

### 代码改动

- `internal/cmd/cmd.go`：`Main` 拆为 `serve`/`migrate` 子命令；serve 逻辑 = 原 `Main.Func` 内容
- `internal/boot/boot.go`：删除 `ensureTables`、7 个 DDL 常量、7 个 `ensureXxxTable`；`Bootstrap` 中 `ensureTables(ctx)` 改为 `migrations.Status(ctx)` 只读检查
- `internal/boot/seed_test.go`：`setupAdminSeed` 中 `ensureTables(ctx)` 改为 `migrations.Up(ctx)`
- `scripts/up.sh`、`scripts/bootstrap.sh`、`scripts/lib.sh`：新增 `migrate_app` 步骤，`start_app` 改为 `"${APP_BIN}" serve`

## Business Invariants

- INV-001（migration 幂等、按序一次）：同一 version 在任何后续 `migrate up` 中不重复执行；未应用 migration 按 version 升序各执行一次（由 golang-migrate `schema_migrations` 保证）。
- INV-002（失败 fail-fast 且 dirty 需人工恢复）：`migrate up` 遇迁移失败立即返回错误、中止后续迁移；失败置 `dirty=true` 后，后续 `up` 拒绝执行，必须人工 `force` 恢复，禁止自动 force。
- INV-003（结构严格等价）：迁移后 7 张表字段/类型/空值/默认值/主键/唯一索引/引擎/字符集与迁移前 `boot.go` DDL 完全等价。
- INV-004（seed 依赖与结果不变）：seed 仅在 readiness check 通过后执行；`seedSuperAdmin`（1 个 `is_super=1`）与 `seedPermissions`（16 个权限）结果与迁移前一致。
- INV-005（serve 无 DDL、migrate 无 serve）：serve 只做只读 readiness check，不执行任何 DDL、不建表、不自动 migrate/force；migration 仅由 `migrate` 子命令执行。
- INV-006（并发单实例执行）：多实例并发 `migrate up` 时，由 golang-migrate MySQL driver 默认 `GET_LOCK` 串行化，最终结构正确、`schema_migrations` 无重复版本。

## Failure and Consistency Semantics

- 事实来源：MySQL（`schema_migrations` 为「已应用」权威记录，7 张表为结构事实）。Redis 仅在 `waitForDependencies` 被 ping，不参与迁移状态；无 MQ、无跨系统事务。
- `migrate up` 成功 = 所有未应用 migration 按序执行成功并写入 `schema_migrations`，`dirty=false`。
- `migrate up` 失败 = 当前 migration 执行失败，返回错误、不记录该版本、不执行后续；MySQL DDL 隐式提交导致部分语句可能已生效，故依赖 `dirty` 标记 + 人工 `force` 恢复，不自动重试。
- serve 成功 = readiness check 通过（表已由 migration 建立、非 dirty、版本最新）+ seed 完成 + HTTP 启动；serve 不代表「刚执行过 migration」，只代表「schema 已就绪」。
- 重试/重复：已应用 version 被跳过；失败未记录 version 在人工 `force` 恢复 dirty 后重试。
- 并发：golang-migrate MySQL driver 默认 `GET_LOCK` 串行化迁移段，崩溃时连接关闭自动释放锁。
- 部分完成：baseline 为多语句单文件，若中途失败会进入 `dirty` 状态；V1 接受「人工 force 恢复」，不做多语句事务包裹（DDL 无法回滚）。

## Allowed / Forbidden Changes

允许：
- 引入 `github.com/golang-migrate/migrate/v4`（`database/mysql`、`source/iofs`）依赖。
- 新增 `internal/migrations` 包与 `internal/migrations/sql/` 下 migration 文件。
- 重构 `internal/cmd` 为 `serve`/`migrate` 子命令。
- 删除 `internal/boot/boot.go` 的 `ensureTables`、7 个建表常量与 7 个 `ensureXxxTable`，改为 `migrations.Status` 只读检查。
- 修改 `internal/boot/seed_test.go` 的 `setupAdminSeed` 改调 `migrations.Up`；修改 `scripts` 增加 migrate 步骤。

禁止：
- 自研 migration Runner、自建 `schema_migrations` 表结构、自实现 `GET_LOCK`/advisory lock。
- 改动 7 张既有表字段/类型/索引/约束/引擎/字符集；改动 seed 语义/内容/幂等行为。
- serve 执行任何 DDL、建表、自动 migrate 或自动 force；`migrate` 命令启动 HTTP 服务。
- 引入 down migration/回滚能力或暴露 `migrate down`；改动既有公开接口、路由、错误语义。
- 在 seed 之前跳过 readiness check 或吞掉 migration 未就绪错误。

## Verification Requirements

- INV-001 → 真实 MySQL（`docker compose up -d`）：空库 `my-shop migrate up` 后 `SHOW TABLES` 确认 7 表 + `schema_migrations` 存在、记录数正确；再次 `up` 记录数不变、无重复执行（覆盖 AC-001/002）。
- INV-001（增量）→ 新增 1 个合法 migration 后 `migrate up`，断言仅新增 1 条记录、旧 migration 未重跑（覆盖 AC-003）。
- INV-002 → 构造语法错误 migration，`migrate up` 失败返回错误、`dirty=true`，再次 `up` 拒绝执行；`migrate force` 后恢复（覆盖 AC-004）。
- INV-003 → `SHOW CREATE TABLE` / `information_schema` 逐一比对 7 表与迁移前 DDL 等价（归一化 `AUTO_INCREMENT=n` 噪声）。
- INV-004 → `my-shop serve` 后 `SELECT` 超管=1、权限=16；`boot.go` 代码审查确认无 `CREATE TABLE`（覆盖 AC-005）。
- INV-005 → serve 在未执行 migrate 的空库上启动，断言 fail-fast 报错、不建表；代码审查确认 serve 路径无 DDL。
- INV-006 → 并发集成测试：多 goroutine（或两进程）并发 `migrate up`，断言最终结构正确、`schema_migrations` 无重复版本、无报错（覆盖 AC-006，建议 `-race`）。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；MySQL 集成验证需说明容器就绪。

## Open Risks

- 14 位时间戳作 version：`force` 需指定完整时间戳数字，可读性差；需在 `migrate` 命令帮助与交接中明确 baseline 版本号及 `force` 语义（不执行 SQL、不校验结构）。
- 仅提供 `.up.sql` 不提供 `.down.sql`：`Up()` 可正常工作，但任何 `down` 相关调用会报错；V1 已禁止 down，风险可控。
- `multiStatements=true` 必须在 DSN 正确设置，否则 baseline 多语句文件会失败；由 Coder 落地时以集成测试覆盖。
- dirty 恢复依赖人工 `force`，存在误操作风险，需在交接文档明确操作步骤。

## Owner Decision Record

- 2026-09-30：Owner 在 `product-spu-v1` 确认「方案 B」——先建 db-migration 机制，SPU 经该机制新增表，不回退 `boot.go`。
- 2026-10-01（CONTRACT_REVISION）：Owner 明确本次 db-migration **不自研 Runner，采用 golang-migrate v4**，并给出 8 点方向（职责分离 `migrate up`/`serve`、serve 只读 readiness、seed 前置依赖 migration、baseline 接管、不依赖 `IF NOT EXISTS`、V1 无 checksum、禁止修改已合入 migration）。原自研方案废弃。
- 2026-10-01（APPROVED）：Owner 确认全部固化决定并冻结 Contract——
  1. CLI：`my-shop migrate up / force <version> / version` + `my-shop serve`，无参数默认 serve 保留兼容。
  2. version 命名：保持 14 位时间戳方案（`YYYYMMDDHHMMSS`），不改递增整数。
  3. serve readiness：严格模式——dirty、未初始化或 current < latest 均拒绝启动，提示先执行 `migrate up`。
  4. 同步修改 db-migration Task/AC：migration 不再由 serve 启动时自动执行，改为显式 `migrate up` 成功后再 `serve`。
  5. baseline / force / dirty / seed / boot.go 职责等其余方向不变。
