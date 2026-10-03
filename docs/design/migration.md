# 数据库 Migration 设计（golang-migrate v4）

本文面向项目接手者，说明数据库迁移的机制、职责边界与部署语义。事实来源为 `db-migration` 最终 APPROVED Contract（含 CONTRACT_REVISION）与最终实现。

## 1. 职责与边界

项目采用 **golang-migrate v4 库嵌入**（不自研 Runner），把「建表/变更 Schema」从「启动 HTTP 服务」中彻底分离：

- **`my-shop migrate up`**：显式执行所有未应用的 migration，失败 fail-fast。
- **`my-shop migrate force <version>`**：标记指定版本为已应用，**不执行任何 SQL**（用于 baseline 接管 / dirty 恢复）。
- **`my-shop migrate version`**：只读打印当前版本与 dirty 状态。
- **`my-shop serve`**（含无参数默认）：启动 HTTP 服务，只做**只读 schema readiness check** + seed + 路由，**不执行任何 DDL、不建表、不自动 migrate、不自动 force**。

追踪表 `schema_migrations` 由 golang-migrate 自动创建；并发迁移串行化由 MySQL driver 默认 `GET_LOCK` 提供；迁移文件经 `//go:embed` 内嵌，运行时经 `iofs` source 挂载，不依赖工作目录。事实来源为单一 MySQL。

不提供 `migrate down`（无回滚能力、无 `.down.sql`）。

## 2. 数据模型

### 2.1 迁移文件

- 目录：`internal/migrations/sql/`，文件命名 `{version}_{title}.up.sql`。
- `version` 为 **14 位时间戳**（`YYYYMMDDHHMMSS`），数字升序即时间升序。
- 仅提供 `.up.sql`，不提供 `.down.sql`。
- DDL 不使用 `IF NOT EXISTS`（迁移只执行一次，由 `schema_migrations` 追踪）。

当前迁移清单：

| version | title | 内容 |
| --- | --- | --- |
| `20261001000001` | `baseline` | 7 张既有表等价 DDL（多语句单文件） |
| `20261001000002` | `products` | `products` + `product_images` |
| `20261001000003` | `skus` | `skus` |
| `20261001000004` | `inventory` | `inventories` + `inventory_logs` |
| `20261001000005` | `addresses` | `addresses`（前台收货地址，含生成列 `default_key` + `uk_user_default` 唯一索引） |

### 2.2 baseline 模型

baseline 内容 = 迁移前 `boot.go` 中 7 张既有表的等价 DDL（`users`、`categories`、`admins`、`roles`、`permissions`、`admin_roles`、`role_permissions`），多语句单文件，唯一差异是去掉 `IF NOT EXISTS`；字段/类型/约束/引擎/字符集严格等价（`ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`）。

**baseline 接管**：新环境（空库）`migrate up` 从 baseline 建 7 表；已有环境（7 表已存在、无 `schema_migrations`）由人工显式 `migrate force <baseline 时间戳>` 标记已应用，**不自动 force**。

### 2.3 追踪表与 dirty

`schema_migrations` 结构为 `version bigint`（主键）+ `dirty bool`，由 golang-migrate 首次 `Up()` 时自动创建。dirty 表示「存在执行失败的中间态」。

## 3. 业务不变量

- INV-001（幂等、按序一次）：同一 version 在任何后续 `migrate up` 中不重复执行；未应用 migration 按 version 升序各执行一次（由 `schema_migrations` 保证）。
- INV-002（失败 fail-fast 且 dirty 需人工恢复）：`migrate up` 遇迁移失败立即返回错误、中止后续迁移；失败置 `dirty=true` 后，后续 `up` 拒绝执行，必须人工 `force` 恢复，禁止自动 force。
- INV-003（结构严格等价）：baseline 迁移后 7 张表与迁移前 `boot.go` DDL 严格等价。
- INV-004（seed 依赖与结果不变）：seed 仅在 readiness check 通过后执行；`seedSuperAdmin`（1 个 `is_super=1`）与 `seedPermissions` 幂等写入（按 `code` 唯一、1062 兜底），seed 语义不因迁移机制改变而改变（权限清单见 `rbac.md`）。
- INV-005（serve 无 DDL、migrate 无 serve）：serve 只做只读 readiness check，不执行任何 DDL；migration 仅由 `migrate` 子命令执行。
- INV-006（并发单实例执行）：多实例并发 `migrate up` 由 MySQL driver 默认 `GET_LOCK` 串行化，最终结构正确、`schema_migrations` 无重复版本。

## 4. 一致性模型与失败语义

- 事实来源：MySQL。`schema_migrations` 为「已应用」的权威记录，7 张表为结构事实。Redis 仅被 ping，不参与迁移状态；无 MQ、无跨系统事务。
- `migrate up` 成功 = 所有未应用 migration 按序执行成功并写入 `schema_migrations`，`dirty=false`；已全部应用时返回成功（幂等，不视为错误）。
- `migrate up` 失败 = 当前 migration 执行失败，返回错误、不记录该版本、不执行后续；MySQL DDL 隐式提交导致部分语句可能已生效，依赖 `dirty` 标记 + 人工 `force` 恢复，不自动重试。
- serve 成功 = readiness check 通过（`schema_migrations` 已初始化、非 dirty、`current >= latest`）+ seed 完成 + HTTP 启动；serve 只代表「schema 已就绪」，不代表「刚执行过 migration」。
- 并发：golang-migrate MySQL driver 默认 `GET_LOCK` 串行化迁移段，崩溃时连接关闭自动释放锁。
- 部分完成：baseline 为多语句单文件，中途失败进入 `dirty`；V1 接受「人工 force 恢复」，不做多语句事务包裹（DDL 无法回滚）。

## 5. 安全与权限边界

- 迁移是运维操作（CLI），不经 HTTP 暴露；无鉴权中间件参与。
- serve 的 readiness check 严格 fail-fast：dirty、未初始化或版本落后均拒绝启动，明确提示先执行 `migrate up`（dirty 时提示人工 `force`）。
- DSN 从 `database.default.*` 配置构造，启用 `multiStatements=true`（支持 baseline 多语句文件）；凭据不硬编码。

## 6. 错误码域

migration 不新增业务错误码；失败通过 `gerror` 返回并由 CLI 进程退出码体现，不映射 `{code,message,data}` 响应。

## 7. 跨模块关系

- 所有业务表（`products`/`product_images`、`skus`、`inventories`/`inventory_logs`、`addresses`）均经本机制新增迁移，serve 不建表。
- 部署顺序为 `build → migrate up → serve`；`product.md`/`sku.md`/`inventory.md`/`category.md`/`rbac.md`/`address.md` 的数据模型均以本机制建立，不再回退到 `boot.go` 建表。

## 8. Deferred / 已知留白

- 仅提供 `.up.sql`，任何 `down` 调用会报错；V1 已禁止 down，风险可控。
- 14 位时间戳作 version，`force` 需指定完整时间戳数字，可读性差；需在 CLI 帮助中明确 baseline 版本号与 `force` 语义（不执行 SQL、不校验结构）。
- dirty 恢复依赖人工 `force`，存在误操作风险。
