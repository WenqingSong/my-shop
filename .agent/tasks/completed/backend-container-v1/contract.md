# Technical Contract

## Decision Status

APPROVED

## Problem

将 SurgeCart GoFrame 后端容器化并接入现有 Docker Compose（当前仅 `mysql`/`redis`/`prometheus`，无 `app`），在 CNB DinD 云开发环境中实现 MySQL、Redis、Prometheus 与后端的统一启动/停止/验证，为后续 Nginx V1 提供稳定的 `app:8000` 内部入口；不改变业务行为，且不新增 migration 与错误码域。

本任务的核心是「启动生命周期与部署模型」的多个现实方案选择（migration 执行时机、依赖未就绪不启动的表达、宿主机进程与容器双启动的取舍、Prometheus 抓取目标切换、本地文件持久化、CNB DinD 环境约束），不同方案产生不同的可靠性、运维与兼容结果。

## Verified Current Behavior

- VERIFIED：后端当前以宿主机进程运行（`scripts/lib.sh` 中 `nohup $APP_BIN serve` + pid 文件）；`docker-compose.yml` 只有 `mysql`/`redis`/`prometheus`，无 `app` 服务。
- VERIFIED：`serve` 启动链路（`internal/cmd/cmd.go` `serve()`）：`boot.Bootstrap`（配置 → JWT 密钥校验 → 依赖连通重试 → schema 就绪检查 → seed 超管 → seed 权限）→ `service.Upload().ValidateConfig`（七牛 required，fail-fast）→ 三个后台扫描器 → 轮播图占位图 seed → metrics 路由 → `s.Run()`。
- VERIFIED：migration 是显式命令（`my-shop migrate up`），`serve` 不自动执行、不建表，只做只读 readiness check（`current < latest` 或 `dirty` 即 fail-fast），见 `internal/boot/boot.go` `checkSchemaReady`。迁移由 golang-migrate MySQL driver 默认 `GET_LOCK` 串行化，`schema_migrations` 追踪、幂等（`docs/design/migration.md`）。
- VERIFIED：依赖连通检查 `waitForDependencies` 每 1s 重试 MySQL `PingMaster` + Redis `PING`，超时 `STARTUP_DEPENDENCY_TIMEOUT`（默认 30s）。
- VERIFIED：环境变量命名 config 键 `.`→`_` 大写（如 `DATABASE_DEFAULT_HOST`）；`.env.example` 为完整清单；`scripts/lib.sh` 加载根目录 `.env`（空值不覆盖默认）。
- VERIFIED：本地存储 `storage.local.root` 默认 `./storage`，仅 `configureBannerStorage` 幂等 seed `storage/banners/` 3 张占位图（存在即跳过，内容固定纯色 PNG，可随时重建）。
- VERIFIED：Prometheus `prometheus/prometheus.yml` 当前抓取 `host.docker.internal:8000`（宿主机进程），compose 用 `extra_hosts: host-gateway`；指标开关 `FLASH_SALE_METRICS_ENABLED=true`（对应 `flash_sale.metrics.enabled`）才暴露 `GET /metrics`。
- VERIFIED：`/health` 是 liveness（`status=ok` + 时间），无依赖/readiness 语义；serve 仅在 bootstrap 全部通过后才监听端口。
- VERIFIED：无后端 Dockerfile、无 `.dockerignore`；`go.mod` 声明 `go 1.23.0`（README 注明开发用 1.24）；module `cnb.cool/go-cloud-devops/my-shop`。依赖均为纯 Go（含 `golang-migrate/v4`、`qiniu/go-sdk`、`golang.org/x/crypto`），可 `CGO_ENABLED=0` 静态编译。
- VERIFIED：`manifest/config/config.yaml` 已提交含开发默认值：`auth.jwt.secret`（非空开发默认值）、`database.default.pass=root`、`admin.super.password=""`（无默认，首次建号需注入）。真实凭据（`.env` 中 AK/SK/JWT secret/超管密码）已被 `.gitignore` 忽略。
- VERIFIED：`prometheus/Dockerfile` 注释说明 rootless/嵌套容器下 bind mount 单文件退化为目录挂载。
- UNKNOWN：CNB DinD 环境的真实 Docker 构建、named volume 持久化、Compose DNS（`app:8000`）解析能力，需在集成验证（AC-013）中确认。

## Recommendation

RECOMMENDATION：采用「容器 entrypoint 自动 migrate + serve，复用 serve 自身 fail-fast，全面容器化移除宿主机进程」的部署模型，具体如下（其中 D1/D2/D3 为需 Owner 确认的关键取舍，其余为随 Contract 批准的实现约束）：

1. **镜像与构建**：多阶段 Dockerfile（builder `golang:1.23-alpine` + `CGO_ENABLED=0` 静态编译，runtime `alpine` + `tzdata` + `ca-certificates`），COPY `manifest/config/config.yaml` 到镜像默认路径，工作目录 `/app`，确保 `time.LoadLocation("Asia/Shanghai")` 可用（AC-003）。`.dockerignore` 排除 `.env`、`bin/`、`tmp/`、`storage/` 运行数据、`*_test.go` 等。
2. **D1 — migration 时机（RECOMMENDATION：entrypoint 自动执行）**：容器 entrypoint（shell 脚本，不改 Go 代码）先 `my-shop migrate up`（幂等 + `GET_LOCK` 串行），成功后再 `exec my-shop serve`。满足 AC-006（migrate 失败非零退出、不对外）与 AC-007（幂等不并发）。
3. **D2 — 依赖编排与健康检查（RECOMMENDATION：不新增 readiness）**：`app` 配 `depends_on: {mysql: condition: service_healthy, redis: condition: service_healthy}`，同时保留应用内 `waitForDependencies` 重试兜底；app 健康检查复用 `/health`（serve 成功启动才监听端口，可响应即代表依赖与 schema 已就绪）。不引入独立 `/ready` 端点，避免触碰 `internal/` 与为多实例提前引入抽象。
4. **D3 — 启动前置校验（RECOMMENDATION：复用 serve fail-fast）**：entrypoint 不显式执行 `qiniu check`/`admin check`，由 `serve` 自身 fail-fast（七牛 `ValidateConfig` + 超管 seed + 权限 seed）承担，避免重复 `GetBucketInfo`；首次建号缺 `ADMIN_SUPER_PASSWORD`、缺七牛凭据均表现为容器非零退出 + 明确日志。
5. **Prometheus**：`prometheus.yml` 抓取目标由 `host.docker.internal:8000` 改为 `app:8000`，移除 prometheus 服务 `extra_hosts: host-gateway`；`FLASH_SALE_METRICS_ENABLED` 经 app 服务 `environment` 注入（`${FLASH_SALE_METRICS_ENABLED:-false}`），抓取端口仍为 8000，`/metrics` 语义不变。
6. **本地持久化**：新增 named volume `app_storage` 挂载 `/app/storage`，容器内 `STORAGE_LOCAL_ROOT=/app/storage`；即便占位图可幂等重建，仍按 AC-010 配置卷，用 named volume（非 bind mount）规避 rootless/嵌套容器退化问题。
7. **D4 — 开发脚本（RECOMMENDATION：全面容器化）**：`make up` 改为 `docker compose up -d --build`（含 app），移除 `lib.sh` 中 `nohup`/pid 进程管理，`down`/`status`/`logs`/`health`/`restart`/`clean` 全部走 Compose，从根上杜绝宿主机进程与 Compose app 双启动。

关键取舍：D1 选择 entrypoint 自动迁移会把「serve 不自动 migrate」的既有语义在容器入口处改为「启动即迁移」，但这是容器化标准做法、且不改 `internal/` Go 代码；相比一次性 migration 服务（依赖 `condition: service_completed_successfully` 编排脆弱、重启不重跑）和手动迁移（破坏一键启动），entrypoint 是可靠性与体验的平衡点。

## Selected Design

Owner 已确认三项关键取舍（详见 Owner Decision Record），最终部署模型如下：

1. **迁移时机（D1）**：容器 entrypoint（shell 脚本，位于镜像内，不改 `internal/` Go 代码）先 `my-shop migrate up`（幂等 + golang-migrate `GET_LOCK` 串行），成功后再 `exec my-shop serve`。migrate 失败 → 非零退出、不对外。
2. **脚本策略（D4）**：`make up`/`down`/`status`/`logs`/`health`/`restart`/`clean` 全面容器化（app 进 Compose），移除 `scripts/lib.sh` 的 `nohup`/pid 宿主机进程管理，杜绝双启动。
3. **凭据分离（D5，Owner 采用比「开发默认值进镜像」更严格的方案）**：
   - `manifest/config/config.yaml` 清空敏感字段默认值：`auth.jwt.secret` → `""`、`database.default.pass` → `""`（`redis.default.pass`、`admin.super.password`、`qiniu.*` 本已空）。普通非敏感配置（`server.address`、`dashboard.timezone`、`storage.local.root`、`order.*`、`flash_sale.*`、`qiniu.region/token_ttl/max_file_size/白名单` 等）保留，可打包进镜像。
   - JWT Secret、MySQL/Redis 密码、七牛 AK/SK/bucket/domain、超管密码等敏感凭据统一经运行时环境变量注入（Compose `environment` + `.env`）；缺失时 `auth.Secret`/MySQL 认证/七牛 `ValidateConfig` fail-fast，**不回退公开默认密钥**。
   - 开发测试凭据（如 MySQL `root`、开发 JWT secret）仅放在 `docker-compose.yml` 的 `${VAR:-default}`（明确标记，仅供 CNB DinD 开发环境），不进镜像、不进 `config.yaml`；生产经平台 Secret/环境变量注入。
   - `.env.example` 保持模板（敏感字段留空），`.env` 不提交 Git、不进镜像（`.dockerignore` 排除）。
4. **健康检查**：不新增 readiness；app Compose `healthcheck` 复用 `/health`（serve 成功启动才监听端口）。
5. **启动前置校验**：entrypoint 不显式执行 `qiniu check`/`admin check`，由 `serve` 自身 fail-fast 承担。
6. **Prometheus**：`prometheus.yml` 目标 `host.docker.internal:8000` → `app:8000`，移除 prometheus 服务 `extra_hosts`；`FLASH_SALE_METRICS_ENABLED` 经 app `environment` 注入。
7. **本地持久化**：named volume `app_storage` → `/app/storage`，容器内 `STORAGE_LOCAL_ROOT=/app/storage`。

**凭据分离的连带影响（需 Coder 处理并验证）**：
- config.yaml 清空 `database.default.pass` 后，「无环境变量直接 `go run . serve`/`migrate up`」不再可用，需注入 `AUTH_JWT_SECRET` 与 `DATABASE_DEFAULT_PASS`。
- `scripts/test.sh` 当前「不 source .env、走 config.yaml 默认值」，清空后缺 `DATABASE_DEFAULT_PASS` 连不上 MySQL；需在 test.sh 显式注入开发测试凭据（如 `DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-root}"`），保持「隔离真实 .env 凭据」原则不变。
- 测试代码已 `t.Setenv(AUTH_JWT_SECRET/ADMIN_SUPER_PASSWORD)`，无需改；数据库连接凭据由 test.sh 注入。README「其他启动方式」示例同步更新。

## Interfaces and Data

**保持不变（不触碰 `internal/` 业务代码）**：
- 现有公开 HTTP API、错误码、响应格式（`{code,message,data}`）。
- `serve` / `migrate up|force|version` / `qiniu check` / `admin check` 子命令语义。
- 环境变量命名（config 键 `.`→`_` 大写）与加载优先级（环境变量 > `.env` > config.yaml 默认值）。
- `/health`、`/metrics` 端点语义与指标开关。

**新增/调整**：
- 根目录 `Dockerfile`、`.dockerignore`。
- `docker-compose.yml` 新增 `app` 服务（`build: .`、`environment` 注入 Compose DNS 名 `mysql`/`redis`、`depends_on` healthy 依赖、`healthcheck`、`ports: "${APP_PORT:-8000}:8000"`、`restart: unless-stopped`）与 `app_storage` volume。
- `prometheus.yml` 目标 `app:8000`；`prometheus` 服务移除 `extra_hosts`。
- `scripts/*`、`Makefile`、`README.md`、部署文档同步更新。
- `docs/design/deployment.md`（Design Impact = NEW）。

**存储约束**：MySQL/Redis 为业务事实源；`app_storage` 为本地静态文件持久化；无 MQ、无新增跨系统事务。

## Business Invariants

- INV-001（启动幂等不并发）：重复 `up`、容器重启、并发启动，migration 不重复应用、超管不重复创建、权限不重复创建。机制：`migrate up` 幂等 + golang-migrate `GET_LOCK` 串行 + `admins.username`/`permissions.code` 唯一约束（1062）兜底。
- INV-002（schema 未就绪不对外）：MySQL 未就绪 / schema 未迁移 / dirty 时，app 不以可用业务服务对外（依赖重试超时或迁移失败 → 非零退出 / 健康检查不健康）。
- INV-003（凭据不落镜像/仓库）：`.env`、真实 AK/SK、真实 JWT secret、超管密码、七牛密钥不进入镜像层、构建上下文或任何受跟踪提交物。
- INV-004（持久数据不因重建丢失）：`down`（保留卷）+ `up` 重建后，MySQL/Redis 数据与本地存储文件仍在。
- INV-005（监控指标语义不变）：`FLASH_SALE_METRICS_ENABLED=true` 时 `GET /metrics` 暴露 `flashsale_*`，Prometheus `app` 目标 UP，抓取内容与开关关闭时一致（不暴露）。

## Failure and Consistency Semantics

- **事实来源**：MySQL 是业务数据与 migration 状态权威源；Redis 是会话/秒杀缓存；Prometheus 是监控；无 MQ。
- **成功**：`docker compose up` 后 app 容器 healthy、`/health` 返回 ok、关键业务 API 正常；不代表「刚执行过 migration」，仅代表「schema 已就绪」。
- **MySQL 未就绪**：`depends_on` 等待 `mysql` healthy，app 内 `waitForDependencies` 再重试，超时非零退出，`restart: unless-stopped` 重试。
- **schema 未迁移**：entrypoint `migrate up` 执行；若因依赖未就绪连接失败 → 非零退出重试；已迁移则幂等通过。
- **dirty / 迁移确定性失败**：`migrate up` 非零退出 → 容器 crash-loop，日志明确，需人工 `migrate force <version>` 恢复（沿用现有语义，不自动 force）。
- **七牛凭据缺失 / 首次建号缺 `ADMIN_SUPER_PASSWORD`**：`serve` fail-fast 非零退出，日志指认字段名、不泄漏凭据值。
- **重启/重复**：`migrate up`、超管/权限 seed、占位图 seed 均幂等。
- **部分完成**：migration dirty 需人工 `force` 恢复（不自动重试、不自动回滚）。

## Allowed / Forbidden Changes

- 允许：新增 `Dockerfile`、`.dockerignore`、`docker-compose.yml` 的 `app` 服务与 `app_storage` volume、`prometheus.yml` 目标切换、`scripts/*`/`Makefile`/`README`/部署文档改造、`docs/design/deployment.md`；以及为容器化必需的最小配置接入（如 `SERVER_ADDRESS`/`STORAGE_LOCAL_ROOT`/`DATABASE_DEFAULT_HOST`/`REDIS_DEFAULT_ADDRESS` 环境变量注入）。
- 禁止：修改 `internal/` 业务代码（除非容器化必需的最小配置接入，且不触碰秒杀/订单/IAM 业务逻辑）；新增数据库 migration 与错误码域（本任务**无全局资源预留需求**，不触发 Registry 流程）；修改公开接口/错误语义/数据契约；提前宣称 Nginx/K8s 已部署。

## Verification Requirements

- INV-001 → 连续两次 `docker compose up` + 重启，核对 `schema_migrations` 无重复版本、`admins` 仅一条 `is_super=1`、`permissions` 按 code 唯一（真实 MySQL）。
- INV-002 → 分别构造「MySQL 未就绪」「schema 未迁移」「dirty」三场景，确认 app 不以可用业务服务对外（非零退出或健康检查不健康）（真实 Compose）。
- INV-003 → `docker build` 后 `docker history` + 解包镜像层扫描，确认无 `.env` 与凭据值；`.dockerignore` 排除 `.env`/`bin`/`tmp`/`storage`（AC-001/AC-002）。
- INV-004 → 写持久化目录文件 → `down`（保留卷）→ `up` 重建 → 文件与 MySQL/Redis 数据仍在（AC-008/AC-010）。
- INV-005 → `FLASH_SALE_METRICS_ENABLED=true` 启动，Prometheus Targets `app` UP，`/metrics` 含 `flashsale_*`；关闭时不暴露（AC-009）。
- AC-013 → 真实 CNB DinD 环境走通 migration + `/health` + JWT 登录 + 关键业务 API 无回归；`go build ./...`、`go vet ./...`、`go test -p 1 ./...` 通过，环境不具备项标 `NOT_VERIFIED`。

## Open Risks

- CNB DinD 环境的镜像构建、named volume 持久化、Compose DNS（`app:8000`）解析能力尚未实证，属集成验证前提（AC-013）。
- `restart: unless-stopped` 下 dirty/迁移确定性失败会 crash-loop；此为诚实运维信号（日志可查、需人工 `force`），但需在部署文档明确。
- 配置默认值（`auth.jwt.secret` 开发默认值、`database.default.pass=root`）进镜像的「凭据」判定边界需 Owner 确认（见 Owner Decision Record）。

## Owner Decision Record

| 编号 | 问题 | Owner 决定 | 适用范围 |
| --- | --- | --- | --- |
| D1 | 容器中 migration 执行时机 | entrypoint 自动 `migrate up` → `serve`（不改 Go 代码，幂等 + GET_LOCK 串行） | 容器启动入口；`internal/` 与 `serve`/`migrate` 子命令语义不变 |
| D4 | 开发脚本改造策略 | 全面容器化，移除宿主机进程启动（`nohup`/pid） | `scripts/*`、`Makefile`；从根上杜绝双启动 |
| D5 | config.yaml 敏感默认值是否进镜像 | 配置与敏感凭据分离：敏感凭据（JWT secret、MySQL/Redis 密码、七牛密钥、超管密码）不固化进镜像，统一环境变量注入，缺失启动失败、不回退公开默认密钥；开发测试凭据仅放 compose `${VAR:-default}` | `manifest/config/config.yaml` 清空敏感字段默认值；Dockerfile COPY 清空后的 config.yaml；compose environment 注入 |

三项决定均与 Task 的 Goal/Scope/AC 兼容（D5 强化 AC-001「不落凭据」、与 AC-006 未就绪不启动精神一致），不改变 Scope 与 AC，无需修改 Task。
