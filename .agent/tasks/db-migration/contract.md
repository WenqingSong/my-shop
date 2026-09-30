# Technical Contract

## Decision Status

APPROVED

## Problem

把 `internal/boot/boot.go` 中 7 张既有表的幂等建表逻辑（`ensureTables` + 7 个 `createXxxTableSQL` 常量 + 7 个 `ensureXxxTable` 函数）迁移为带版本、按序执行、可追踪已应用状态的 migration 机制。迁移后既有表结构与迁移前严格等价，seed 逻辑（`seedSuperAdmin` / `seedPermissions`）语义不变且在 migration 之后执行。本机制同时是 product-spu-v1（SPU）新增 `products`/`product_images` 表的前置依赖。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2.10.3 / Go 1.23+，模块 `cnb.cool/go-cloud-devops/my-shop`；数据访问无 `dao`/`model` 层，直接用 `g.DB().Model()` / `g.DB().Exec()`。
- VERIFIED：`internal/boot/boot.go` 当前用 `ensureTables` + 7 个 `CREATE TABLE IF NOT EXISTS` DDL 常量 + 7 个 `ensureXxxTable` 函数幂等建表；`Bootstrap` 顺序为 `applyServerConfig → applyDatabaseConfig → applyRedisConfig → auth.Secret → waitForDependencies → ensureTables → seedSuperAdmin → seedPermissions`。
- VERIFIED：7 张表为 `users`、`categories`、`admins`、`roles`、`permissions`、`admin_roles`、`role_permissions`；DDL 均无 `FOREIGN KEY` 约束（关联表仅复合主键），5 张主表有 `UNIQUE` 索引（`uk_username` / `uk_parent_name` / `uk_admin_username` / `uk_role_name` / `uk_permission_code`）。
- VERIFIED：`applyDatabaseConfig` 构造 `gdb.ConfigNode` 时未开启 `multiStatements`；golang-migrate 的 mysql 驱动要求迁移 DSN 显式 `multiStatements=true`（与应用 DSN 分离）。
- VERIFIED：全仓库无任何 migration 机制、无 `.sql` 文件、无 `go:embed` 用法；`go.mod` 无迁移依赖（仅 `go-sql-driver/mysql`、gf 系列、`golang-jwt`、`bcrypt`）。
- VERIFIED：环境每日重置（CNB DinD）、数据不持久；`docker-compose.yml` 用 `mysql:8.0`。CI（`.cnb.yml`）流程为 `docker compose up -d → wait_for_deps → make test`（`go vet` + `go test -p 1 ./...`）。
- VERIFIED：`internal/boot/seed_test.go` 的 `setupAdminSeed` 直接调用 `ensureTables(ctx)` 建表；`boot_test.go` 只测配置读取。`ensureTables` 全仓库仅在 `boot.go` 内部与 `seed_test.go` 两处引用。
- VERIFIED：`internal/boot/seed.go` 的 `seedSuperAdmin` / `seedPermissions` 已通过「先查再写 + 1062 兜底」实现幂等，seed 本身不依赖建表方式。

## Selected Design

Owner 已确认方向性决定与最终执行模型（见 Owner Decision Record），据此固化的方案如下。

### 1. Migration 工具与接入方式

- 采用 **golang-migrate v4** 作为库：`github.com/golang-migrate/migrate/v4` + `database/mysql` 驱动 + `source/iofs`，**不自研 migration runner**。
- 迁移文件放 `internal/migration/sql/`，经 `embed.FS` + `source/iofs` 内嵌进二进制，部署无需外挂 `.sql`。
- 迁移状态表由 golang-migrate 维护，默认 `schema_migrations(version BIGINT PRIMARY KEY, dirty BOOLEAN NOT NULL)`。
- 迁移 DSN 为 `user:pass@tcp(host:port)/dbname?multiStatements=true`，复用现有 database 配置（config.yaml + 环境变量）解析出 host/port/user/pass/name；应用自身的 GoFrame DSN 保持原样（不开启 multiStatements）。

### 2. 执行模型：单二进制，Migration 与应用启动分离

- 同一个二进制提供两个入口：
  - `my-shop migrate up`：在应用启动前单独执行迁移，只做 schema，不做 seed。
  - `my-shop serve`：启动 HTTP 服务（默认 `main` 入口）。
- `my-shop serve` 的 `Bootstrap` **不创建任何 schema**，顺序为 `applyServerConfig → applyDatabaseConfig → applyRedisConfig → auth.Secret → waitForDependencies → 只读 schema readiness check → seedSuperAdmin → seedPermissions → HTTP`。
- **只读 schema readiness check**：serve 阶段做只读校验（`schema_migrations` 存在、无 dirty 记录、7 张既有表存在），任一不满足则 fail-fast 并明确提示「先执行 `my-shop migrate up`」，不落到 seed 撞 MySQL 报错。
- 生命周期脚本（`scripts/up.sh`、`bootstrap.sh` 等）与任何启动入口必须先 `migrate up` 成功后再 `serve`；`migrate up` 非零退出则中止，不启动应用。

### 3. 失败 / dirty 状态处理（依赖 golang-migrate 内建机制）

- 并发串行化：mysql 驱动在 `Up()` 前通过 `GET_LOCK` 获取排他锁（锁名由 database name + migrations 表名派生），拿不到锁返回 `ErrLocked`（非零退出，可重试）。
- 状态机：每个迁移执行前 `dirty=true`，成功后 `dirty=false`；执行失败或进程崩溃后 `dirty=true` 保持。
- `dirty=true` 时 `Up()` 拒绝继续执行并报错；恢复方式为人工定位并修复后 `migrate force <last-good-version>` 标记干净，再继续。

### 4. Baseline 迁移与接管（7 个独立迁移文件）

- Baseline 由 **7 个独立迁移文件**组成（非单文件多语句），顺序与当前 `ensureTables` 一致：
  `000001_create_users.up.sql`、`000002_create_categories.up.sql`、`000003_create_admins.up.sql`、`000004_create_roles.up.sql`、`000005_create_permissions.up.sql`、`000006_create_admin_roles.up.sql`、`000007_create_role_permissions.up.sql`。
- 每个文件含单条**普通 `CREATE TABLE`（不含 `IF NOT EXISTS`）**，字段/索引/约束与迁移前 `boot.go` 常量逐字一致。
- 新环境：`migrate up` 从 0 依序执行 7 个 baseline 文件，建 7 表，记录 version=1…7。
- 已有环境（7 表已存在但 `schema_migrations` 缺失）：**必须显式操作，禁止自动 force**。提供独立命令 `my-shop migrate baseline`：先只读验证 7 表 `SHOW CREATE TABLE` 与 baseline 期望等价，验证通过后 `force 7` 标记已应用（不重跑 DDL）；验证失败（存在 drift）则 fail-fast，不强行接管。
- 迁移正式接管后的新增变更一律严格执行，**禁止用 `IF NOT EXISTS` 掩盖 Schema Drift**。

### 5. Seed 顺序

- seed 保留在 `Bootstrap`（serve 阶段）中；因 `migrate up` 阶段先于 serve 完成，seed 天然在迁移成功后执行。
- `boot.go` 不再负责 Schema 创建；`seed_test.go` 的 `setupAdminSeed` 改为先执行迁移（复用迁移入口）再 seed。

## Interfaces and Data

- 新增依赖：`github.com/golang-migrate/migrate/v4`、`.../database/mysql`、`.../source/iofs`。
- 迁移文件命名：`{NNNNNN}_{description}.up.sql`（golang-migrate 数字版本前缀，6 位补零，顺序即执行顺序）。
- 追踪表（golang-migrate 自动创建）：`schema_migrations(version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL)`。
- 新增子命令：`my-shop migrate up`、`my-shop migrate baseline`（显式接管既有库）、`my-shop serve`。
- 新包 `internal/migration` 承载迁移子命令与 `embed.FS`；提供可程序化调用的迁移入口（`up` / `baseline` / `force`）供测试复用。
- `Bootstrap` 签名不变，但删除 `ensureTables`、7 个 DDL 常量、7 个 `ensureXxxTable`。

## Business Invariants

- INV-001：每个迁移 SQL 在重复/并发启动下**实际只执行一次**并被记录一次（golang-migrate `GET_LOCK` 串行化 + `schema_migrations.version` 主键 + dirty 状态机保证，不依赖 `IF NOT EXISTS`）。
- INV-002：迁移失败 fail-fast：`dirty=true` 阻断后续迁移，`migrate up` 非零退出，应用不启动；未经人工修复并 `force` 前不自动继续。
- INV-003：baseline 迁移建出的 7 张表字段/索引/约束与迁移前 `boot.go` DDL 严格等价。
- INV-004：seed 仅在迁移成功后执行，且结果与迁移前一致（超级管理员=1、权限=16），幂等语义不变。

## Failure and Consistency Semantics

- 事实来源：MySQL 是 schema 的唯一事实来源；`schema_migrations` 是「已应用版本 + dirty 状态」的唯一事实来源。无 Redis/MQ 参与。
- 成功：`migrate up` 返回 0 代表全部待应用迁移已执行且 `dirty=false`；随后 serve 才可能启动。
- 失败：迁移失败 → `dirty=true`，`migrate up` 非零退出；已成功执行的 DDL 不回滚（DDL 隐式提交）；后续启动被 dirty 阻断直到修复 + `force`。
- 并发：多实例同时 `migrate up` 时 `GET_LOCK` 保证只有一个执行，其余锁超时返回 `ErrLocked`（非零退出，可重试）。

## Allowed / Forbidden Changes

- 允许：新增 golang-migrate v4 依赖；新增 `internal/migration`（迁移子命令 + `sql/*.sql` + embed）；`boot.go` 移除建表职责、`Bootstrap` 不再建 schema；新增 `my-shop migrate up` / `migrate baseline` 入口并调整生命周期脚本先 migrate 后 serve；serve 增加只读 schema readiness check；调整 `seed_test.go` 先迁移再 seed；新增迁移相关测试。
- 禁止：不新增业务表（`products`/`product_images` 归 SPU）；不改既有表字段/索引/约束；不改 seed 语义；不在迁移文件用 `IF NOT EXISTS` 掩盖 drift；不自研迁移锁/状态/runner；不改既有公开接口、错误语义或路由；不引入 down migration（V1）；不修改已执行/已合入的迁移文件；不在 `migrate up` 中自动 force。

## Verification Requirements

- INV-001 → 并发 `migrate up`（多 goroutine/双进程）：仅一个执行成功、其余 `ErrLocked` 或安全跳过；最终 7 表结构正确、`schema_migrations` 无重复 version。
- INV-002 → 构造语法错误迁移：`migrate up` 非零退出、`dirty=true`、后续迁移未执行；`force` 修复后恢复可继续。
- INV-003 → 新环境从 0 建表后 `SHOW CREATE TABLE` 逐表与迁移前 DDL 比对等价。
- INV-004 → `migrate up` 后 `serve` 启动，`SELECT` 超级管理员数量=1、权限数量=16，与迁移前一致。
- Baseline 接管 → 已有环境（预置 7 表、无 `schema_migrations`）经 `migrate baseline` 验证后 `force 7` 不重跑 DDL；验证失败（drift）fail-fast；`migrate up` 路径不触发自动 force。
- 只读 readiness check → 迁移未执行时 `serve` fail-fast 并提示先 `migrate up`。
- 通用：`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；MySQL 集成验证需容器就绪（`docker compose up -d` + `wait_for_deps`）。

## Open Risks

- golang-migrate mysql 驱动要求迁移 DSN `multiStatements=true`，与应用 DSN 分离，需避免误用（应用 DSN 保持原样）。
- Baseline 接管依赖 `migrate baseline` 中 `SHOW CREATE TABLE` 验证的准确性：验证不充分会把 drift 误判为「已应用」，需把对比做成可重复脚本/测试。
- V1 不提供 down migration；golang-migrate 惯例为 up/down 成对，本任务仅提供 `.up.sql`（down 缺失不影响 `up`，影响 `down` 命令，后者不在 V1 范围）。
- 无 checksum：已合入迁移文件若被事后修改，会在未来环境产生 drift，靠「已合入迁移不可改 + 变更须新增迁移」的纪律约束。

## Owner Decision Record

Owner 于 2026-09-30 给出方向性决定：

1. 采用 golang-migrate v4（库），不自研 migration runner，避免自行承担 lock/dirty/失败恢复/未来非幂等 ALTER 的正确性问题。
2. 并发保证使用工具内建锁/状态机制（非「幂等 DDL + version 唯一约束」）；迁移与应用启动职责分离，由独立 `migrate up` 阶段完成后启动应用。
3. 建立 Initial/Baseline migration：新环境从 0 执行 baseline；已有环境经 schema 验证后标记 baseline 已应用，实现无损接管；接管后新增变更严格执行，不依赖 `IF NOT EXISTS` 掩盖 drift。
4. V1 不增加 checksum/hash；约束已执行/合入的 migration 不允许修改，schema 变更必须新增 migration。

Owner 于 2026-09-30 确认最终执行模型：

1. 单二进制：`my-shop migrate up` + `my-shop serve`。
2. `serve` 保留只读 schema readiness check。
3. Baseline 采用 7 个独立迁移文件（非单文件 7 条 SQL）。
4. 已有库 baseline 接管必须显式操作（`migrate baseline`），禁止自动 force。

关键问题均已解决，Contract 标记 APPROVED，可交 Coder 实现。
