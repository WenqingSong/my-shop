# Owner Core Logic 验证卡

本任务为后端容器化，核心机制集中在「启动生命周期」与「凭据分离」两处——它们直接决定 INV-001/INV-002/INV-003 三项业务不变量（启动幂等不并发、schema 未就绪不对外、凭据不落镜像/仓库）。普通 DTO/脚本搬运不列入。

## CL-001：容器启动生命周期（先 migrate 再 serve，未就绪不对外、幂等不并发）

- Owner 需要理解：容器 entrypoint 先幂等执行 `my-shop migrate up`（golang-migrate 的 MySQL driver 用 `GET_LOCK` 串行 + `schema_migrations` 追踪，已全部应用则成功返回），成功后才 `exec serve`；`serve` 的 `Bootstrap` 再做依赖连通重试 → schema 就绪只读校验（`current<latest` 或 `dirty` 即 fail-fast）→ 超管/权限 seed → 七牛 `ValidateConfig`。任一步失败即 `set -e` 非零退出，容器不对外，由 `restart: unless-stopped` 重试。错误后果：若去掉 migrate 或颠倒顺序，会导致「schema 未迁移也对外提供服务」或「并发重复迁移/重复建号」。
- 生产代码：`scripts/docker-entrypoint.sh`（`migrate up` → `exec serve`）；`internal/boot/boot.go` 的 `checkSchemaReady` / `waitForDependencies` / `seedSuperAdmin`
- 关键测试：`scripts/test-container.sh`（静态断言 entrypoint 先 `migrate up` 再 `exec serve`）；`internal/migrations`（真实 MySQL 幂等迁移）
- 基线验证：`bash scripts/test-container.sh` 通过；`make up` 后 app `(healthy)`；多次 `up/down` 后 `schema_migrations` 仍 1 行、`admins` 超管仍 1 条
- 可选 Mutation：删除 `scripts/docker-entrypoint.sh` 中的 `migrate up` 行（模拟错误实现）
- 预期失败：`bash scripts/test-container.sh` 的「entrypoint 未执行 migrate up」断言非零退出（已隔离验证，exit 1）
- 恢复确认：还原 entrypoint 后重跑 `bash scripts/test-container.sh` 通过

## CL-002：凭据分离（敏感字段不进镜像/仓库，运行时环境变量注入）

- Owner 需要理解：`manifest/config/config.yaml` 的敏感字段（`auth.jwt.secret`、`database.default.pass`）已清空为空串，凭据仅经运行时环境变量注入（Compose `environment` + `.env`）；缺失时 `serve` fail-fast、不回退公开默认密钥。`.dockerignore` 排除 `.env`/`bin`/`tmp`/`storage`，镜像内不含 `.env` 与真实凭据。错误后果：若 config.yaml 残留开发默认 JWT 密钥或非空 MySQL 密码，则「公开默认凭据进镜像」，任何人拉取镜像即可签名任意 JWT 或直连数据库。
- 生产代码：`manifest/config/config.yaml`（`secret: ""`、`pass: ""`）；`Dockerfile`（仅 COPY 清空后的 config.yaml）；`.dockerignore`；`docker-compose.yml`（`environment` 注入）
- 关键测试：`scripts/test-container.sh`（断言 config.yaml 无历史 dev JWT 默认值、`pass` 为空、`.dockerignore` 排除 `.env`/`bin`/`tmp`/`storage`）
- 基线验证：`bash scripts/test-container.sh` 通过；`docker run --rm my-shop-app:local sh -c 'cat /app/manifest/config/config.yaml'` 确认敏感字段为空且镜像内无 `.env`
- 可选 Mutation：将 config.yaml 的 `auth.jwt.secret: ""` 写回历史 dev 默认值 `c49d0fea5cf77761ca6d0275545b9501686dd40cc313b072c36972d6128fc403`
- 预期失败：`bash scripts/test-container.sh` 的「config.yaml 仍残留开发默认 JWT 密钥」断言非零退出（已隔离验证，exit 1）
- 恢复确认：还原 config.yaml 后重跑 `bash scripts/test-container.sh` 通过
