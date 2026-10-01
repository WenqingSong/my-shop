# Technical Contract

## Decision Status

WAITING_FOR_OWNER_APPROVAL

## Problem

把 `internal/boot/boot.go` 中 7 张既有表的幂等建表逻辑，迁移为「带时间戳、按序执行、可追踪已应用状态」的数据库 Migration 机制；启动时自动执行未应用 migration；后续任务（商品 SPU）通过该机制新增 `products`/`product_images`。迁移后 7 张表结构与迁移前完全等价，seed 语义不变。

需要 Owner 确认 6 个设计问题：机制选型、文件命名与顺序、事务/失败语义、多实例并发保护、既有表等价性保证、`boot.go` 职责收敛与测试迁移。

## Verified Current Behavior

- VERIFIED：`internal/boot/boot.go` 中 `Bootstrap` 启动顺序为 `applyServerConfig → applyDatabaseConfig → applyRedisConfig → auth.Secret → waitForDependencies → ensureTables → seedSuperAdmin → seedPermissions`；`ensureTables` 依次调用 7 个 `ensureXxxTable`，每个用 `g.DB().Exec(ctx, createXxxTableSQL)` 执行 `CREATE TABLE IF NOT EXISTS`，共 7 个 DDL 常量：`createUsersTableSQL`、`createCategoriesTableSQL`、`createAdminsTableSQL`、`createRolesTableSQL`、`createPermissionsTableSQL`、`createAdminRolesTableSQL`、`createRolePermissionsTableSQL`。
- VERIFIED：7 张表为 `users`、`categories`、`admins`、`roles`、`permissions`、`admin_roles`、`role_permissions`；均为 `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`，含各自 `PRIMARY KEY` 与 `UNIQUE KEY`（`uk_username`/`uk_parent_name`/`uk_admin_username`/`uk_role_name`/`uk_permission_code`，关联表用复合主键）。
- VERIFIED：数据访问无 dao/model 层，直接 `g.DB().Exec()` / `g.DB().Model()`；`internal/boot/seed.go` 的 `seedSuperAdmin` 与 `seedPermissions` 已用「先查再写 + MySQL 1062 唯一约束兜底」实现幂等，`isDuplicateKeyError` 判定 1062。
- VERIFIED：`go.mod`（Go 1.23.0，GoFrame v2.10.3）无任何 migration 第三方依赖，仅 `go-sql-driver/mysql`、gf 系列、`golang-jwt`、`bcrypt`；`internal/boot/boot_test.go` 只测配置读取，不依赖建表；`internal/boot/seed_test.go` 的 `setupAdminSeed` 直接调用 `applyDatabaseConfig` + `ensureTables` + `DELETE FROM admins`。
- VERIFIED：`docker-compose.yml` 为 `mysql:8.0` + `redis:7-alpine`，命名卷持久化；`scripts/test.sh` 用 `go test -p 1 ./...` 串行执行以避免共享库测试互相污染；环境每日重置（CNB DinD）、数据不持久，migration 必须能从空库重复执行。
- VERIFIED：前置依赖——`product-spu-v1/contract.md` 已由 Owner（2026-09-30）确认「方案 B」：先建 db-migration 机制，SPU 经该机制新增 `products`/`product_images`，不回退 `boot.go`；该 Contract 保持 `BLOCKED`，本任务是阻塞解除条件。
- UNKNOWN：无影响方案选择的未知项。既有本地库「已建 7 表但无 `schema_migrations`」的平滑升级路径未纳入本任务 AC（daily-reset 环境不涉及），列为 Open Risk。

## Recommendation

RECOMMENDATION（逐项对应 Analyst Questions，均为单方案推荐）：

1. **机制选型（#1）**：自研轻量机制——`schema_migrations` 追踪表 + 启动时按序执行内嵌 `.sql` 文件。不引入 golang-migrate/goose。理由：需求仅需「有序执行 + 追踪 + 幂等 + fail-fast」，无 down migration；引入框架会新增依赖与 `source`/dirty 状态等复杂度，违反 `agents.md`「不为未来假设提前引入基础设施 / 新增依赖前判断标准库是否足够」。

2. **命名与顺序（#2）**：migration 文件为 `internal/migrations/sql/<YYYYMMDDHHMMSS>_<description>.sql`（14 位秒级时间戳 + 下划线 + 描述）。执行顺序 = 按文件名**字典序升序**（等价于时间戳升序，同秒由描述确定性破序）。追踪表以**完整文件名**为主键，避免「同秒不同描述」被静默合并。新增迁移须使用严格大于既有文件的唯一时间戳。

3. **事务/失败语义（#3）**：MySQL DDL 隐式提交，事务无法回滚 DDL，故**每个 migration 文件只含单条 DDL**（7 张表对应 7 个文件），以「单语句原子性」规避半成品；执行语句成功后再写 `schema_migrations`，失败则**立即返回错误、不记录该版本、不继续后续迁移**（fail-fast），重启后安全重试。V1 **不加 checksum/hash 绑定**（daily-reset 下收益极低）。

4. **多实例并发（#4）**：用 MySQL 会话级 advisory lock `GET_LOCK('my_shop_migrations', timeout)` 串行化整段迁移；获锁后**重新查询**已应用版本（防止等待期间他实例已完成）；`schema_migrations.version` 主键唯一作为兜底。**获锁后所有迁移语句须在同一连接（会话）上执行**，连接关闭自动释放锁，实例崩溃不残留锁。

5. **等价性保证（#5）**：7 个 migration 文件内容 = 原 `boot.go` 的 7 个 DDL 常量**逐字复用**，仅把 `CREATE TABLE IF NOT EXISTS` 改为 `CREATE TABLE`（迁移语义下由追踪表保证不重复执行），其余字段/类型/索引/约束/引擎/字符集一律不变，从而严格等价。`schema_migrations` 表本身用 `CREATE TABLE IF NOT EXISTS` 幂等创建（属基础设施，不计入 7 张表）。

6. **职责收敛与测试迁移（#6）**：新建 `internal/migrations` 包（`Run(ctx) error` + `embed.FS` 内嵌 `sql/*.sql`）；`boot.go` 移除 `ensureTables` + 7 个建表常量 + 7 个 `ensureXxxTable`，`Bootstrap` 中 `ensureTables(ctx)` 替换为 `migrations.Run(ctx)`，顺序仍为 `waitForDependencies → migrate → seedSuperAdmin → seedPermissions`（seed 依赖表已由 migration 创建）。`seed_test.go` 的 `setupAdminSeed` 由 `ensureTables(ctx)` 改为 `migrations.Run(ctx)`。

关键取舍：**并发一致性 vs 依赖成本**是本次唯一实质性取舍。自研 + `GET_LOCK` 用极小的会话锁成本换取「任意迁移内容下都单实例执行」的确定性（不依赖 DDL 幂等），比「仅靠唯一约束 + `IF NOT EXISTS`」更稳，代价是需在实现中保证 `GET_LOCK` 的会话亲和（同一连接）。引入第三方框架可省自研，但带来依赖、dirty 状态与 down migration 语义等额外成本，超出当前需求。

## Selected Design

等待 Owner 确认。

## Interfaces and Data

### 追踪表 `schema_migrations`

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
  version    VARCHAR(128) NOT NULL,
  applied_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
```

- `version` 记录完整 migration 文件名（如 `20260101000001_create_users.sql`），唯一且不可变；`applied_at` 记录应用时刻。
- 该表由迁移 runner 在启动时以 `IF NOT EXISTS` 幂等创建，属基础设施，不在 7 张既有表迁移范围内。

### Migration 文件

- 目录：`internal/migrations/sql/`，经 `//go:embed sql/*.sql` 编译进二进制（运行时不依赖工作目录/外部文件）。
- 命名：`<YYYYMMDDHHMMSS>_<description>.sql`；本次迁移 7 张既有表为 7 个文件，时间戳严格递增。
- 执行顺序：对内嵌文件按文件名字典序升序。

### 代码接口

- 新增 `internal/migrations` 包，导出 `func Run(ctx context.Context) error`（启动迁移入口）。
- `internal/boot/boot.go`：删除 `ensureTables` 及 7 个 DDL 常量与 7 个 `ensureXxxTable`；`Bootstrap` 调用 `migrations.Run(ctx)` 替代 `ensureTables(ctx)`。
- `internal/boot/seed_test.go`：`setupAdminSeed` 由 `ensureTables(ctx)` 改为 `migrations.Run(ctx)`。

## Business Invariants

- INV-001（迁移幂等、按序一次）：同一版本迁移在任何后续启动中不重复执行；未应用迁移按文件名升序依次执行且各执行一次。
- INV-002（失败 fail-fast、不落库不续跑）：任一迁移语句失败即启动失败并返回错误；失败版本不写入 `schema_migrations`，后续迁移不执行。
- INV-003（结构严格等价）：迁移后 `users`/`categories`/`admins`/`roles`/`permissions`/`admin_roles`/`role_permissions` 的字段、类型、空值、默认值、主键、唯一索引、引擎、字符集与迁移前 `boot.go` DDL 定义完全等价。
- INV-004（seed 依赖与结果不变）：seed 依赖的表在 seed 前由 migration 创建；`seedSuperAdmin`（1 个 `is_super=1`）与 `seedPermissions`（16 个权限）的结果与迁移前一致。
- INV-005（并发单实例执行）：多实例并发启动时，迁移最多由单实例实际执行，其余安全跳过；最终表结构正确，`schema_migrations` 无重复版本、无重复建表/重复记录错误。

## Failure and Consistency Semantics

- 事实来源：MySQL（`schema_migrations` 为「已应用」的权威记录；7 张业务表为结构事实）。Redis 仅在 `waitForDependencies` 中被 ping，不参与迁移状态；无 MQ、无跨系统事务。
- 成功 = 所有未应用迁移按序执行成功并各写入一条 `schema_migrations` 记录，随后 seed 正常完成。
- 失败 = 某迁移语句失败：立即返回错误并中止（不记录该版本、不执行后续迁移）。因 DDL 隐式提交，事务无法回滚，故靠「每文件单条 DDL」保证无半成品。
- 重试/重复：失败版本未记录，重启后重新按序执行；已应用版本被跳过，不重跑。
- 并发：`GET_LOCK` 串行 + 获锁后重查已应用集 + `version` 主键唯一兜底；连接关闭自动释放锁，实例崩溃不残留锁。
- 部分完成：仅当单文件被写成多语句时可能出现；本机制约束「每文件单条 DDL」以消除该风险。

## Allowed / Forbidden Changes

允许：
- 新建 `internal/migrations` 包与 `internal/migrations/sql/` 下 7 个（+ 未来新增）migration 文件；启动时创建 `schema_migrations`。
- 删除 `internal/boot/boot.go` 的 `ensureTables`、7 个建表常量与 7 个 `ensureXxxTable`，`Bootstrap` 改调 `migrations.Run(ctx)`。
- 修改 `internal/boot/seed_test.go` 的 `setupAdminSeed` 改调迁移入口；新增迁移相关测试（见 Verification Requirements）。

禁止：
- 改动 7 张既有表的字段、类型、索引、约束、引擎、字符集（纯迁移，保持等价）。
- 改动 `seedSuperAdmin`/`seedPermissions` 的语义、内容与幂等行为（1 个超级管理员、16 个权限）。
- 引入第三方迁移框架（golang-migrate/goose 等）或 down migration/回滚能力（除非 Owner 明确要求）。
- 新增任何业务表（`products`/`product_images` 归 SPU 任务）；改动既有公开接口、路由、错误语义或 `Bootstrap` 中 migration 与 seed 的相对顺序。
- 在 `seed` 之前执行迁移失败时继续启动或吞掉错误。

## Verification Requirements

- INV-001 → 真实 MySQL（`docker compose up -d`）：空库启动后 `SHOW TABLES` 确认 7 表存在；查 `schema_migrations` 为 7 条；不清理再次启动，断言记录数不变、无重复建表报错（覆盖 AC-001/002）。
- INV-001（增量）→ 新增 1 个合法 migration 文件后重启，断言仅新增 1 条记录、旧迁移未重跑（覆盖 AC-003）。
- INV-002 → 构造语法错误 migration 文件，断言启动失败返回错误、该版本未记录、后续迁移未执行（覆盖 AC-004）。
- INV-003 → 用 `SHOW CREATE TABLE` 或 `information_schema`（columns/statistics/table_constraints）逐一比对 7 表与迁移前 DDL 的结构等价，注意归一化 `AUTO_INCREMENT=n` 等非结构噪声（覆盖 AC-001 等价性）。
- INV-004 → 启动后 `SELECT` 断言超级管理员数量=1、权限数量=16，与迁移前一致；`boot.go` 代码审查确认无 `CREATE TABLE`（覆盖 AC-005）。
- INV-005 → 并发集成测试：多 goroutine（或两进程）并发 `migrations.Run`，断言最终表结构正确、`schema_migrations` 无重复版本、无报错（覆盖 AC-006，建议 `-race` 下以业务断言为准）。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；MySQL 集成验证需说明容器就绪（`docker compose up -d`）。

## Open Risks

- 已有本地库（旧代码已建 7 表、无 `schema_migrations`）首次跑新代码会因 `CREATE TABLE`（已存在）报 1050 失败；daily-reset 环境不涉及，如需本地平滑升级可 `docker compose down -v` 重置。V1 不提供 baseline 迁移。
- `SHOW CREATE TABLE` 输出含 `AUTO_INCREMENT=n` 噪声，等价性测试必须归一化后再比对。
- `GET_LOCK` 为会话级锁，实现须保证获锁与迁移语句在同一连接（会话）执行；GoFrame 连接池下需显式固定连接，属实现细节由 Coder 落地。
- V1 不绑定 migration 文件 checksum：在持久化环境下「已应用 migration 被事后修改」不会被检测（daily-reset 下无影响）；如未来出现持久化生产库，再评估补 checksum。

## Owner Decision Record

等待 Owner 确认。
