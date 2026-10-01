# Delivery Verification

## Milestone and Target
- Milestone：数据库 Migration 机制（db-migration）
- Delivery Target：HEAD `b987b37c06fb9c6f36a359cfc8480e304e9d6ee8`（分支 `feat/db-migration`）
- Cleaner Review Target：HEAD `2ba1c4c4548d93c778ec6d53ec5abcde9fd2dd5c`（实现提交 `eeddc32` + 修复提交 `2ba1c4c`）
- Target Match：YES（`b987b37` 相对 `2ba1c4c` 仅新增一个 docs-only 提交，改动 `.agent/tasks/db-migration/findings.md` 与 `core-logic.md`；生产代码、测试、迁移文件与 Cleaner Review Target 完全一致）

## Environment
- OS：linux/amd64
- Go：1.24.1
- MySQL：8.0.46（`docker compose` 的 `mysql:8.0`，容器 `my-shop-mysql` healthy）
- Redis：7-alpine（容器 `my-shop-redis` healthy）
- Docker：29.6.2；Docker Compose：v5.3.1
- golang-migrate：v4.19.0（库嵌入）
- baseline 迁移版本：`20261001000001`
- 配置来源：`manifest/config/config.yaml` 默认值；`ADMIN_SUPER_PASSWORD` 通过环境变量注入（值不记录）；数据库 `root/root` 为本地开发默认值
- 测试数据与清理：验证在 `my_shop` 库上清空后执行；验证结束已停止应用进程、工作区干净（`git status` 空）、容器恢复验证前原状

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build | PASS | `go build ./...` 退出 0；`go build -o bin/my-shop .` 成功 |
| Vet | PASS | `go vet ./...` 退出 0 |
| Unit/Integration Test | PASS | `go test -p 1 ./...` 全包通过（migrations/boot/cmd/controller/admin|categories|iam/middleware 等） |
| Race Test | PASS | `go test -race -p 1 ./internal/migrations/ ./internal/boot/` 无数据竞争 |
| AC-001 空库迁移建表 | PASS | 清空库后 `./bin/my-shop migrate up`，`SHOW TABLES` = 7 业务表 + `schema_migrations`；`schema_migrations` = `version=20261001000001, dirty=0` |
| AC-002 幂等 | PASS | 再次 `migrate up` 日志「没有待执行的 migration」退出 0，version 不变 |
| AC-003 增量迁移 | PASS | 临时注入 `20261001000002_probe.up.sql` 重建后 `migrate up`，version 前进到 02、`migration_probe` 建出、7 业务表仍存在（baseline 未重跑） |
| AC-004 失败 fail-fast + dirty + force | PASS | 注入非法 `20261001000003_broken.up.sql` 后 `migrate up` 退出 1 报 SQL 1064；`schema_migrations` = `version=20261001000003, dirty=1`；再次 `up` 拒绝「Dirty database version ... Fix and force version.」；`migrate force 20261001000003` 恢复 dirty=0；缺参/非法 version 正确报错 |
| AC-005 serve 只读 readiness + seed | PASS | `boot.go`/`cmd.go` 生产代码 grep 无 `CREATE TABLE`；空库 `serve` 退出 1 报「schema 未就绪」且未建任何表；`migrate up` 后 `ADMIN_SUPER_PASSWORD` 注入启动 `serve`，`/health` 返回 `{"code":0,"message":"OK"}`，`admins` 超管（is_super=1）=1，`permissions`=16 |
| AC-006 并发迁移 | PASS | 空库并发 3 个 `migrate up` 进程全部退出 0，最终 `schema_migrations` = `version=20261001000001, dirty=0`，表数=8 无重复 |
| 结构等价 INV-003 | PASS | 测试 `TestSchemaStructureMatchesBaseline`（information_schema 全维度比对）通过；独立 `SHOW CREATE TABLE users/categories/admin_roles` 与 baseline SQL 严格一致 |
| 部署脚本流程 | PASS | 清空库后 `ADMIN_SUPER_PASSWORD` 注入 `make up` 完成 build → migrate up → serve，`make health` App/MySQL/Redis 全健康，seed 核对 super_admin=1、permissions=16、tables=8 |

## Acceptance Evidence

- AC-001/AC-002：空库 `migrate up` 建 8 表、version=baseline、dirty=0；重复 `up` 幂等（本次 CLI 实测）。
- AC-003：注入增量迁移后仅新迁移执行、version 前进、旧迁移不重跑（本次 CLI 实测）。
- AC-004：失败迁移使 `up` 退出 1、dirty=1、再次 `up` 拒绝、`force` 恢复（本次 CLI 实测）。
- AC-005：serve 空库 fail-fast 且不建表；migrate 后 serve 完成 seed（超管=1、权限=16）并健康响应（本次 CLI 实测）。
- AC-006：3 进程并发 `migrate up` 全部成功、无重复版本、结构正确（本次 CLI 实测）。
- INV-001~INV-006：分别由上述 AC 与 `go test`（含 `-race`）覆盖。

## Not Executed

（无核心交付检查未执行。全链路均在本次真实 MySQL/Redis 环境独立重跑。）

## Remaining Risks

- baseline 接管：已有环境需人工 `migrate force <baseline 时间戳>`，不自动 force；daily-reset（CNB DinD）环境走空库 `up`，几乎无额外成本。
- 仅提供 `.up.sql`：V1 禁止 down，任何 down 调用会报错，风险可控。
- `multiStatements=true` 依赖 DSN 正确设置：已由本次 baseline 多语句迁移实测通过。
- dirty 恢复依赖人工 `force`：已通过 CLI 实测并文档化，存在误操作风险需在交接中说明。
- 上述均为 Contract 已记录的 Open Risks，非本次新发现问题。

## Result

PASS
