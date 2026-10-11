# Owner Decision

## Review Target

`42a49efcbf41b434c9fd1407a4059c4212f59e9d`（C1，Cleaner 已 CLEAN 的 immutable snapshot）

## Core Logic

- CL-001（容器启动生命周期）：容器 entrypoint 先幂等 `migrate up`（golang-migrate `GET_LOCK` 串行 + `schema_migrations` 追踪）成功后才 `exec serve`；`serve` 的 `Bootstrap` 再做依赖连通重试 → schema 就绪只读校验（`current<latest` 或 `dirty` 即 fail-fast）→ 超管/权限 seed → 七牛 `ValidateConfig`。任一步失败非零退出、不对外，由 `restart: unless-stopped` 重试。生产代码：`scripts/docker-entrypoint.sh`、`internal/boot/boot.go`（`checkSchemaReady`/`waitForDependencies`）、`internal/boot/seed.go`（`seedSuperAdmin`/`seedPermissions`）。关键测试：`scripts/test-container.sh`（断言先 migrate 再 serve）。基线：`bash scripts/test-container.sh` 通过；多次 up/down 后 `schema_migrations` 仍 1 行、超管仍 1 条。Mutation：删除 `migrate up` 行 → 断言非零退出。
- CL-002（凭据分离）：`manifest/config/config.yaml` 敏感字段（`auth.jwt.secret`、`database.default.pass`）清空为空串，真实凭据仅经运行时环境变量注入（Compose `environment` + `.env`），缺失 fail-fast、不回退公开默认密钥；`.dockerignore` 排除 `.env`/`bin`/`tmp`/`storage`。生产代码：`manifest/config/config.yaml`、`Dockerfile`、`.dockerignore`、`docker-compose.yml`。关键测试：`scripts/test-container.sh`（断言无历史 dev JWT、`pass` 为空、`.dockerignore` 排除敏感项）。基线：`bash scripts/test-container.sh` 通过；镜像内无 `.env`、敏感字段全空。Mutation：写回 dev JWT → 断言非零退出。

## Owner Decision

ACCEPTED

## Decision Evidence

- Owner 于 2026-10-11 明确回复 `ACCEPT`，接受 Cleaner 已 CLEAN 的 snapshot `42a49efcbf41b434c9fd1407a4059c4212f59e9d` 的核心逻辑（CL-001、CL-002）。
- 接受范围：上述 snapshot 中与本任务（backend-container-v1）相关的全部实现与测试；不包括「抽象接受整个任务」。
- 非阻塞剩余项：两条 P3 Finding（`CLEAN-001` 端口冲突、`CLEAN-002` `defaultDBPass` 硬编码回退）在本次决策中保持 OPEN，Owner 未要求在本任务处理；不阻塞本次 ACCEPT，是否另开任务由 Owner 后续决定。
