# Task: 数据库 Migration 机制（db-migration）

## Goal

建立带时间戳的数据库 Migration 机制：把 `internal/boot/boot.go` 中 7 张既有表的幂等建表逻辑，迁移为按序执行、可追踪已应用状态的 migration 文件；启动时自动执行未应用的 migration；并让后续任务（如商品 SPU）能通过该机制新增数据表。迁移后的既有表结构与迁移前完全等价，seed 逻辑保持不变。

## Scope

允许完成的内容：

- Migration 机制本身：
  - migration 文件的存放目录与命名规范（带时间戳，保证执行顺序）。
  - 已应用迁移的追踪方式（如 `schema_migrations` 表，最终形态由 Analyst 固化）。
  - 启动时在 `boot.Bootstrap` 流程中执行未应用 migration（替换现有 `ensureTables`）。
  - 从空库可重复执行：每日重置（CNB DinD）环境下每次从零建表。
- 既有表迁移：把 `users`、`categories`、`admins`、`roles`、`permissions`、`admin_roles`、`role_permissions` 共 7 张表的 DDL，从 `ensureTables` + 建表常量 + `ensureXxxTable` 函数迁移到 migration 文件，字段、索引、约束与迁移前严格等价。
- 移除 `boot.go` 的建表职责（`ensureTables` 及相关建表常量/函数），`Bootstrap` 改调用 migration 执行。
- 保持 seed 逻辑不变：`seedSuperAdmin`、`seedPermissions` 语义与迁移前一致，且必须在 migration 之后执行（依赖表已存在）。
- 必要的测试：从空库执行迁移、重复执行幂等、已应用不重复执行、迁移失败 fail-fast、既有表结构与迁移前等价。

## Out of Scope

明确本次不处理：

- 不新增任何业务表：`products`/`product_images` 由商品 SPU 任务经本机制新增，不归本任务。
- 不改动既有表的字段、索引、约束（纯迁移，保持等价）。
- 不改动 seed 语义（超级管理员、权限 seed 的内容与幂等行为不变）。
- 不引入数据回滚/降级（down migration）能力，除非 Owner 明确要求（见 Analyst Questions）。
- 不改动既有公开接口、错误语义或路由。

## Acceptance Criteria

- [ ] AC-001：从空库启动（MySQL 就绪后），`users`/`categories`/`admins`/`roles`/`permissions`/`admin_roles`/`role_permissions` 共 7 张表全部创建，字段/索引/约束与迁移前 `boot.go` DDL 定义等价，且已应用迁移被记录（`schema_migrations` 或等价）。
- [ ] AC-002：在已有表且已记录迁移的库上重复启动，不重复执行已应用迁移，启动成功无报错。
- [ ] AC-003：新增一个 migration 文件后重启，仅该未应用迁移被按序执行并记录，旧迁移不重复执行。
- [ ] AC-004：某个 migration 执行失败时，启动流程 fail-fast 返回错误，不静默吞掉失败，也不继续执行后续迁移。
- [ ] AC-005：`internal/boot/boot.go` 不再包含直接 `CREATE TABLE` 的建表逻辑；`seedSuperAdmin` 与 `seedPermissions` 仍在 migration 完成后正常执行（超级管理员与 16 个权限 seed 结果与迁移前一致）。
- [ ] AC-006：多实例同时执行迁移时，迁移最多由单实例执行、其余安全跳过，不产生重复建表或重复记录错误（是否需保护见 Analyst Questions #4）。

## Relevant Context

已核实的事实：

- `internal/boot/boot.go` 当前通过 `ensureTables` + 7 个 `createXxxTableSQL` 常量 + 7 个 `ensureXxxTable` 函数，用 `CREATE TABLE IF NOT EXISTS` 幂等建表；`Bootstrap` 顺序为 `applyServerConfig → applyDatabaseConfig → applyRedisConfig → auth.Secret → waitForDependencies → ensureTables → seedSuperAdmin → seedPermissions`。
- 7 张既有表：`users`、`categories`、`admins`、`roles`、`permissions`、`admin_roles`、`role_permissions`。
- 全仓库（含 git 历史）无任何 migration 机制或文件；`go.mod` 无第三方 migration 依赖（仅 `go-sql-driver/mysql`、gf 系列、`golang-jwt`、`bcrypt`）。前端 `dist` 中命中的 `migrat*` 是 elementUI 压缩产物，与本任务无关。
- 环境每日重置（CNB DinD）、数据不持久，migration 必须能从空库重复执行（每次从零建表）。
- 数据访问无 dao/model 层，直接 `g.DB().Model()` / `g.DB().Exec()`；迁移可复用 `g.DB().Exec()` 执行 DDL。
- 受影响测试：`internal/boot/seed_test.go` 的 `setupAdminSeed` 直接调用 `ensureTables(ctx)` 建表；`boot_test.go` 只测配置读取，不依赖建表。
- 前置依赖：product-spu-v1（`feat/spu`）Owner 已确认「方案 B」——先建 db-migration 机制，SPU 经该机制新增 `products`/`product_images` 迁移，不回退到 `boot.go` 建表。本任务是 SPU 的阻塞前置项。

Assumption：

- 优先采用自研轻量 migration 机制（`schema_migrations` 表 + 启动时按序执行 `.sql` 文件），不引入第三方迁移框架，与「不为未来假设提前引入基础设施」一致；最终选型是架构选择，交 Analyst 决策（见 Analyst Questions #1）。

OPEN QUESTION：见下节 Analyst Questions。

## Verification

- AC-001 → 需真实 MySQL（`docker compose up -d`）：清空库后启动，`SHOW TABLES` 确认 7 表存在；`SHOW CREATE TABLE` 逐一比对字段/索引/约束与迁移前等价；查追踪表确认 7 条迁移已记录。
- AC-002 → 不清理库再次启动，断言启动成功、追踪表记录数不变、无重复建表报错。
- AC-003 → 新增一个迁移文件后启动，断言仅新增 1 条记录，旧迁移未重跑。
- AC-004 → 构造一个语法错误的迁移文件，断言启动失败且返回错误，后续迁移未执行。
- AC-005 → 代码审查确认 `boot.go` 无 `CREATE TABLE`；启动后 `SELECT` 超级管理员数量=1、权限数量=16，与迁移前一致。
- AC-006 → 并发集成测试：多 goroutine（或两个进程）并发执行迁移，断言最终表结构正确、追踪表无重复记录、无报错。
- 通用命令：`go build ./...`、`go vet ./...`、`go test ./...`；MySQL 集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：这是重要的架构/数据模型选择（自研 migration 机制 vs 引入第三方框架；迁移追踪表结构与并发执行保护），并涉及既有 7 张表的迁移、启动顺序调整、迁移失败语义与多实例并发一致性。选型与并发语义会直接影响后续所有任务的建表方式与运维可靠性，需 Analyst 比较方案后由 Owner 确认。

## Analyst Questions

1. **Migration 机制选型**：自研轻量机制（`schema_migrations` 表 + 启动时按序执行 `.sql` 文件）vs 引入第三方框架（golang-migrate / pressly/goose 等）？各自对依赖、维护、事务语义、并发保护的成本与结果不同。
2. **迁移文件命名与顺序保证**：时间戳精度与格式（如 `YYYYMMDDHHMMSS`）、同秒冲突处理、是否允许多个迁移，如何保证稳定有序执行。
3. **执行事务语义与失败处理**：MySQL DDL 隐式提交，每个迁移是否/能否包在事务里；失败后已执行部分如何标记（是否写入记录、是否支持重试）；迁移文件是否需与追踪记录绑定校验（如 hash）。
4. **多实例并发启动保护**：每日重置/CI 环境下是否可能出现多实例同时迁移，是否需要 MySQL `GET_LOCK`/advisory lock 或追踪表唯一约束兜底。
5. **既有表迁移的等价性保证**：迁移文件是否直接复用现有 DDL（`CREATE TABLE IF NOT EXISTS` vs 迁移语义下的普通 `CREATE TABLE`），如何确保与迁移前结构严格等价并写入测试。
6. **`boot.go` 职责收敛与测试迁移**：`ensureTables` 移除后 `seed_test.go` 的 `setupAdminSeed` 如何改为依赖 migration；seed 与 migration 的启动顺序如何固定。

## Review Baseline

- Base commit：`14d2e7b921b23be045bd9f8e2d18b7ebe2a52486`（分支 `feat/spu`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/db-migration/`、migration 文件目录/文件、`internal/boot/boot.go` 建表职责移除及对应测试调整；与 product-spu-v1 的任务文档（`.agent/tasks/product-spu-v1/`）无文件重叠，SPU 后续新增的 `products`/`product_images` 迁移不归本任务。当前工作区干净，无需要区分的既有修改。

## Initial Route

READY_FOR_ANALYST
