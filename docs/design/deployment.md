# 容器化部署模型（Deployment / Compose）

本文面向项目接手者，说明 SurgeCart 后端（及依赖）的容器化部署模型：服务编排、镜像与配置模型、启动生命周期、持久化、网络与健康检查语义。事实来源为 `backend-container-v1` 的 APPROVED Contract。Nginx 反向代理 / Kubernetes / Ingress / 多实例负载均衡不在本文范围（另见各相关任务）。

## 1. 职责与边界

后端（GoFrame `my-shop`）以**容器**运行，与 MySQL、Redis、Prometheus 统一经 Docker Compose 编排，提供稳定的 Compose 内部服务入口 `app:8000`，供 Prometheus 及后续 Nginx V1 使用。

边界：

- 仅单实例部署（无多实例/负载均衡），后台扫描器（订单超时取消、秒杀对账/消费）的单实例并发安全已由「条件状态更新 + RowsAffected」/「FOR UPDATE SKIP LOCKED」保证，不因容器化引入新的并发语义。
- 事实来源：MySQL 为业务数据与 migration 状态权威源；Redis 为会话/秒杀缓存；Prometheus 为监控；无 MQ。
- 不改动业务逻辑；`serve`/`migrate`/`qiniu check`/`admin check` 子命令与公开 HTTP API/错误码/响应格式不变。

## 2. 服务编排模型

Compose 服务（`docker-compose.yml`）：

| 服务 | 镜像 | 端口 | 持久化卷 | 健康检查 |
| --- | --- | --- | --- | --- |
| `mysql` | `mysql:8.0` | `3306` | `mysql_data` | `mysqladmin ping` |
| `redis` | `redis:7-alpine` | `6379` | `redis_data` | `redis-cli ping` |
| `prometheus` | `my-shop-prometheus:local`（`prometheus/Dockerfile` 构建，配置 COPY 内置） | `9090` | — | `wget /-/ready` |
| `app` | `my-shop-app`（多阶段构建，见 §3） | `8000`（`APP_PORT` 可覆盖） | `app_storage` | `wget /health` |

服务间通过 Compose DNS 名通信（`mysql`、`redis`、`app`），不依赖宿主机 `host.docker.internal`。`app` 依赖 `mysql`/`redis` 的 `condition: service_healthy` 才启动；`restart: unless-stopped`。

## 3. 镜像与配置模型（凭据分离）

- **多阶段构建**：builder（Go 1.23，`CGO_ENABLED=0` 静态编译，依赖全为纯 Go）→ runtime（alpine + `tzdata` + `ca-certificates`），工作目录 `/app`。`manifest/config/config.yaml` COPY 进镜像默认路径，保证 `time.LoadLocation("Asia/Shanghai")` 等时区语义可用（无 DST）。
- **配置与敏感凭据分离**：
  - `manifest/config/config.yaml` 仅保留普通非敏感配置（`server.address`、`dashboard.timezone`、`storage.local.root`、`order.*`、`flash_sale.*`、`qiniu.region/token_ttl/max_file_size/白名单` 等），敏感字段（`auth.jwt.secret`、`database.default.pass`）默认值为空，可安全打包进镜像。
  - 敏感凭据（JWT secret、MySQL/Redis 密码、七牛 AK/SK/bucket/domain、超管密码）统一经运行时环境变量注入（Compose `environment` + `.env`），缺失时 `auth.Secret`/MySQL 认证/七牛 `ValidateConfig` fail-fast，**不回退公开默认密钥**。
  - 开发测试凭据（如 MySQL `root`）仅出现在 `docker-compose.yml` 的 `${VAR:-default}`（明确标记，供 CNB DinD 开发环境），不进镜像、不进 `config.yaml`；生产经平台 Secret/环境变量注入。
  - `.env.example` 为模板（敏感字段留空）；`.env` 不提交 Git、不进镜像（`.dockerignore` 排除 `.env`、`bin/`、`tmp/`、`storage/` 运行数据）。

## 4. 启动生命周期

容器 entrypoint（镜像内 shell 脚本，不改 Go 代码）：

1. `my-shop migrate up`（幂等：已全部应用则成功返回；golang-migrate MySQL driver `GET_LOCK` 串行化并发迁移）。
2. `exec my-shop serve`：`boot.Bootstrap`（依赖连通重试 → schema 就绪只读校验 → 超管/权限 seed）→ 七牛 `ValidateConfig` → 后台扫描器 → 轮播图占位图 seed → 路由 → `s.Run()`。

- 依赖未就绪：Compose `depends_on: condition: service_healthy` 先行等待，app 内 `waitForDependencies`（`STARTUP_DEPENDENCY_TIMEOUT`，默认 30s）再重试兜底。
- 启动幂等不并发：重复 `up`、容器重启、并发启动，migration 不重复、超管/权限不重复（`schema_migrations` 追踪 + `GET_LOCK` + 唯一约束 1062 兜底）。

## 5. 持久化

- MySQL/Redis：named volume `mysql_data`/`redis_data`，`down`（保留卷）+ `up` 重建不丢数据。
- 本地文件：named volume `app_storage` → `/app/storage`（容器内 `STORAGE_LOCAL_ROOT=/app/storage`）。V1 本地存储仅幂等 seed 轮播图占位图（可重建），卷的「不丢失」语义面向未来落本地的文件；用 named volume（非 bind mount）规避 rootless/嵌套容器下 bind mount 单文件退化为目录的问题。

## 6. 网络与健康检查

- `app:8000` 是稳定的 Compose 内部服务入口；宿主机经 `ports: ${APP_PORT:-8000}:8000` 访问。
- `/health` 为 liveness（`status=ok` + 时间）；serve 仅在 bootstrap 全部通过后才监听端口，故可响应 `/health` 即代表依赖已就绪、schema 已就绪、七牛已校验。不引入独立 readiness 端点（单实例、无摘流量需求）。
- Prometheus 抓取目标为 `app:8000`（不再经 `host.docker.internal`）；`/metrics` 仅在 `FLASH_SALE_METRICS_ENABLED=true`（`flash_sale.metrics.enabled`）时暴露，抓取内容与开关语义不变。

## 7. 业务不变量

- INV-001（启动幂等不并发）：重复 `up`/重启/并发启动，migration 不重复、超管不重复、权限不重复。
- INV-002（schema 未就绪不对外）：MySQL 未就绪 / schema 未迁移 / dirty 时，app 不以可用业务服务对外。
- INV-003（凭据不落镜像/仓库）：`.env`、真实 AK/SK、真实 JWT secret、超管密码、七牛密钥不进镜像层、构建上下文或受跟踪提交物。
- INV-004（持久数据不因重建丢失）：`down`（保留卷）+ `up` 后 MySQL/Redis 数据与本地存储文件仍在。
- INV-005（监控指标语义不变）：指标开关开启时 Prometheus `app` 目标 UP、`/metrics` 含 `flashsale_*`；关闭时不暴露。

## 8. 一致性模型与失败语义

- **成功**：`docker compose up` 后 app 容器 healthy、`/health` ok、关键业务 API 正常；仅代表「schema 已就绪」，不代表「刚执行过 migration」。
- **MySQL 未就绪**：`depends_on` 等待 + `waitForDependencies` 重试，超时非零退出，`restart` 重试。
- **schema 未迁移**：entrypoint `migrate up` 执行；依赖未就绪致连接失败 → 非零退出重试；已迁移则幂等通过。
- **dirty / 迁移确定性失败**：`migrate up` 非零退出 → 容器 crash-loop，日志明确，需人工 `migrate force <version>`（不自动 force、不自动回滚）。
- **七牛凭据缺失 / 首次建号缺 `ADMIN_SUPER_PASSWORD`**：`serve` fail-fast 非零退出，日志指认字段名、不泄漏凭据值。
- 部分完成：migration dirty 需人工 `force` 恢复。

## 9. 安全与凭据边界

- 敏感凭据仅经运行时环境变量注入，不进镜像、不进 `config.yaml` 默认值、不进日志/响应。
- `make init`/`make up` 复用两阶段初始化：`make init` 只起 MySQL/Redis、生成 `.env`（不要求七牛凭据、不启动后端）；`make up` 起 app（Compose 注入 `.env` 变量）。
- 开发命令（`make up/down/status/logs/health`）全面容器化，不再启动宿主机进程，杜绝双启动。

## 10. Deferred / 已知留白

- Nginx 反向代理、Kubernetes/Ingress、HTTPS/TLS、多实例负载均衡（后续任务）。
- readiness 独立端点（当前 `/health` 语义足够，单实例无摘流量需求）。
- CNB DinD 环境的真实构建/卷/网络能力以集成验证（AC-013）为准。
