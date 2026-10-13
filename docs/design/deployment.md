# 容器化部署模型（Deployment / Compose）

本文面向项目接手者，说明 SurgeCart 后端（及依赖）的容器化部署模型：服务编排、镜像与配置模型、启动生命周期、持久化、网络与健康检查语义。事实来源为 `backend-container-v1`、`nginx-reverse-proxy-v1` 与 `container-runtime-hardening-v1` 的 APPROVED Contract。Kubernetes / Ingress / HTTPS/TLS / 多实例负载均衡不在本文范围（另见各相关任务）；`/ready` readiness 端点已落地，但 K8s readinessProbe 编排清单不在本文范围。

## 1. 职责与边界

后端（GoFrame `my-shop`）以**容器**运行，与 MySQL、Redis、Prometheus、Nginx 统一经 Docker Compose 编排。Nginx 是面向外部的**唯一 HTTP 入口**（对外 `${NGINX_PORT:-8080}`）；`app:8000` 是稳定的 Compose 内部服务入口，供 Nginx 与 Prometheus 使用，不对外暴露宿主机端口。

边界：

- 仅单实例部署（无多实例/负载均衡），后台扫描器（订单超时取消、秒杀对账/消费）的单实例并发安全已由「条件状态更新 + RowsAffected」/「FOR UPDATE SKIP LOCKED」保证，不因容器化引入新的并发语义。
- 事实来源：MySQL 为业务数据与 migration 状态权威源；Redis 为会话/秒杀缓存；Prometheus 为监控；无 MQ。
- 不改动业务逻辑；`serve`/`migrate`/`qiniu check`/`admin check` 子命令与公开 HTTP API/错误码/响应格式不变。
- Nginx 单跳代理信任边界：后端只信任 Nginx 覆盖后的 `X-Forwarded-For`/`X-Real-IP`（值为 Nginx 真实对端 IP）；app 不对外暴露端口是信任边界成立的网络前提。

## 2. 服务编排模型

Compose 服务（`docker-compose.yml`）：

| 服务 | 镜像 | 端口 | 持久化卷 | 健康检查 |
| --- | --- | --- | --- | --- |
| `nginx` | `my-shop-nginx:local`（`nginx/Dockerfile` 构建，配置 COPY 内置） | `8080`（`NGINX_PORT` 可覆盖，唯一对外入口） | — | `wget /health`（透传 app） |
| `app` | `my-shop-app:local`（多阶段构建，见 §3） | 无（仅 Compose 网络 `8000`，不映射宿主机） | `app_storage` | `wget /health` |
| `mysql` | `mysql:8.0` | `127.0.0.1:3306`（回环绑定） | `mysql_data` | `mysqladmin ping` |
| `redis` | `redis:7-alpine` | `127.0.0.1:6379`（回环绑定） | `redis_data` | `redis-cli ping` |
| `prometheus` | `my-shop-prometheus:local`（`prometheus/Dockerfile` 构建，配置 COPY 内置） | `9090` | — | `wget /-/ready` |

服务间通过 Compose DNS 名通信（`mysql`、`redis`、`app`），不依赖宿主机 `host.docker.internal`。`app` 依赖 `mysql`/`redis` 的 `condition: service_healthy` 才启动；`nginx` 依赖 `app` 的 `condition: service_healthy` 才启动；各服务 `restart: unless-stopped`。

## 3. 镜像与配置模型（凭据分离）

- **多阶段构建**：builder（Go 1.23，`CGO_ENABLED=0` 静态编译，依赖全为纯 Go）→ runtime（alpine + `tzdata` + `ca-certificates`），工作目录 `/app`。`manifest/config/config.yaml` COPY 进镜像默认路径，保证 `time.LoadLocation("Asia/Shanghai")` 等时区语义可用（无 DST）。
- **非 root 运行**：runtime 阶段创建专用用户 `app:app`（`uid=10001`、`gid=10001`），`USER 10001:10001`；`/app` 与 `/app/storage` 提前 `chown`，保证非 root 下运行时目录可写、轮播图占位图 seed 可执行。主进程有效 UID 非 0（不用 alpine `nobody`，固定身份便于未来 K8s `SecurityContext` 对接）。
- **配置与敏感凭据分离**：
  - `manifest/config/config.yaml` 仅保留普通非敏感配置（`server.address`、`dashboard.timezone`、`storage.local.root`、`order.*`、`flash_sale.*`、`qiniu.region/token_ttl/max_file_size/白名单` 等），敏感字段（`auth.jwt.secret`、`database.default.pass`）默认值为空，可安全打包进镜像。
  - 敏感凭据（JWT secret、MySQL/Redis 密码、七牛 AK/SK/bucket/domain、超管密码）统一经运行时环境变量注入（Compose `environment` + `.env`），缺失时 `auth.Secret`/MySQL 认证/七牛 `ValidateConfig` fail-fast，**不回退任何内置默认密钥/密码**。
  - **无内置默认回退**：`docker-compose.yml` 对 `MYSQL_ROOT_PASSWORD`、`AUTH_JWT_SECRET` 使用 `${VAR:?error}` 必填语法，禁止 `${VAR:-default}`；`scripts/init.sh` 将二者加入 `REQUIRED_VARS` 缺失即报错（空字符串不可绕过）。`serve` 启动对 `DATABASE_DEFAULT_PASS`、`AUTH_JWT_SECRET` 显式非空校验，缺失非零退出、不监听端口、不回退内置默认值。
  - **连接字符集**：应用连接（`database.default.charset`）与 Migration DSN 统一为 `utf8mb4`，四字节 Unicode 字符（emoji / 补充平面字符）可经连接正确读写；表结构已为 utf8mb4，本次为连接层统一。
  - `.env.example` 为模板（敏感字段留空并注明必填）；`.env` 不提交 Git、不进镜像（`.dockerignore` 排除 `.env`、`bin/`、`tmp/`、`storage/` 运行数据）。

## 4. 启动生命周期

容器 entrypoint（镜像内 shell 脚本，不改 Go 代码）：

1. `my-shop migrate up`（幂等：已全部应用则成功返回；golang-migrate MySQL driver `GET_LOCK` 串行化并发迁移）。
2. `exec my-shop serve`：`boot.Bootstrap`（依赖连通重试 → schema 就绪只读校验 → 超管/权限 seed）→ 七牛 `ValidateConfig` → 后台扫描器 → 轮播图占位图 seed → 路由 → `s.Run()`。

- 依赖未就绪：Compose `depends_on: condition: service_healthy` 先行等待，app 内 `waitForDependencies`（`STARTUP_DEPENDENCY_TIMEOUT`，默认 30s）再重试兜底。
- 启动幂等不并发：重复 `up`、容器重启、并发启动，migration 不重复、超管/权限不重复（`schema_migrations` 追踪 + `GET_LOCK` + 唯一约束 1062 兜底）。

停止生命周期（优雅关闭）：`serve` 以 `signal.NotifyContext(SIGINT, SIGTERM)` 为根 ctx 驱动三个后台扫描器（订单超时取消、秒杀对账、秒杀消费）。停机时序：SIGTERM/SIGINT → 取消根 ctx → 扫描器停止领取新任务、在途单事务回滚或完成 → HTTP 停收新连接并 drain 在途请求（`server.graceful_timeout`，默认 10s）→ `sync.WaitGroup` 收尾后进程退出。消费事务被打断时整体回滚、请求保持 `queued`，重启后由重试/对账恢复，不产生半成品或重复下单。

## 5. 持久化

- MySQL/Redis：named volume `mysql_data`/`redis_data`，`down`（保留卷）+ `up` 重建不丢数据。
- 本地文件：named volume `app_storage` → `/app/storage`（容器内 `STORAGE_LOCAL_ROOT=/app/storage`）。V1 本地存储仅幂等 seed 轮播图占位图（可重建），卷的「不丢失」语义面向未来落本地的文件；用 named volume（非 bind mount）规避 rootless/嵌套容器下 bind mount 单文件退化为目录的问题。镜像内 `/app/storage` 已 `chown` 为 `app:app`，named volume 首次挂载继承镜像目录 owner，故非 root 下 `/app/storage/banners` 可写、占位图 seed 成功。

## 6. 网络与健康检查

- `app:8000` 是稳定的 Compose 内部服务入口，**不映射宿主机端口**；外部只能经 Nginx（`${NGINX_PORT:-8080}`）访问。
- `/health` 为 liveness（`status=ok` + 时间，判定不依赖 DB/Redis）：进程存活且 HTTP server 在监听即 200。
- `/ready` 为 readiness（fail-closed）：探测 MySQL Ping + Redis PING（探测超时 1s，结果进程内缓存 TTL 3s，缓存窗口内重复探测不新增 DB/Redis 访问）；DB 或 Redis 任一不可达 → 503（`codes.CodeServiceUnavailable`），响应/日志不泄漏数据库地址、Redis 信息或错误堆栈。schema 就绪由启动 bootstrap 保证（`serve` 仅在 `checkSchemaReady` 通过后监听），不逐次探测。
- `/health` 经 Nginx 公开透传（最小探活响应）；`/ready` 当前仅作未来 K8s readinessProbe 目标，Compose 的 app/nginx healthcheck 仍用 `/health`（Compose 无 Service 摘流机制，用 `/ready` 作 healthcheck 会放大 DB/Redis 抖动为 unhealthy + `restart` 误触发）。
- Prometheus 抓取目标为 `app:8000`（不经 `host.docker.internal`、不经 Nginx）；`/metrics` 仅在 `FLASH_SALE_METRICS_ENABLED=true`（`flash_sale.metrics.enabled`）时暴露，抓取内容与开关语义不变；Nginx 公共入口对 `/metrics` 返回 404，不转发。

## 7. Nginx 反向代理模型（统一入口）

Nginx 是面向外部的唯一 HTTP 入口，职责为纯反向代理（无 Lua/njs 业务逻辑），未来可被 Kubernetes Ingress 直接取代，不搭二次代理层。

- **统一入口与连接复用**：对外 `${NGINX_PORT:-8080}`；upstream 为 Compose DNS 名 `app:8000`，开启 keepalive（`upstream { zone …; server app:8000 resolve; keepalive 32; }` + `proxy_http_version 1.1` + `proxy_set_header Connection ""`）。
- **上游 DNS 再解析**：使用 Docker 内嵌 DNS（`resolver 127.0.0.11 valid=10s ipv6=off;`）与 upstream `server … resolve`（需 nginx ≥ 1.27.3），使 app 冷启动、重建、容器 IP 变化后能在 ~10s 内自动恢复（期间短暂 502，不重试）。
- **信任边界（客户端 IP 防伪造）**：单跳信任。Nginx **覆盖式**重写 `X-Real-IP` 与 `X-Forwarded-For` 为 `$remote_addr`（禁用 `$proxy_add_x_forwarded_for`，因后端 `GetClientIp()` 取 `X-Forwarded-For` 第一项）。app 不对外暴露端口是信任边界成立的网络前提；若未来在 Nginx 之前引入额外代理层，需加 `real_ip` 模块还原真实客户端 IP。
- **网络隔离**：仅 Nginx 对外；app 无宿主机端口映射；mysql/redis 宿主机映射绑定回环地址（`127.0.0.1`）。
- **监控访问策略**：公共入口 `/metrics` 返回 404；Prometheus 经内部网络直连 `app:8000/metrics`；`/health` 公开透传。
- **日志关联（Request ID）**：Nginx 统一生成并覆盖客户端 `X-Request-ID`（`$request_id`）透传；Go 侧 `internal/middleware/requestid.go` 读取/回写响应头并输出含 `request_id` 的结构化访问日志；Nginx 访问日志为 JSON（`escape=json`）含 `$request_id` 与 `$upstream_response_time`；两端以同一 `request_id` 关联；不记录 `Authorization`/`Cookie`/请求体。
- **安全响应头与错误语义**：`server_tokens off;`；`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`（均 `always`）；业务错误响应原样透传，上游不可达/超时返回通用 502/504，请求体超限返回 413 且不转发。
- **重试边界**：`proxy_next_upstream off;`，任何方法不自动重试；「恰好一次」由业务层幂等保证，不由代理承诺。

## 8. 业务不变量

- INV-001（启动幂等不并发）：重复 `up`/重启/并发启动，migration 不重复、超管不重复、权限不重复。
- INV-002（schema 未就绪不对外）：MySQL 未就绪 / schema 未迁移 / dirty 时，app 不以可用业务服务对外。
- INV-003（凭据不落镜像/仓库）：`.env`、真实 AK/SK、真实 JWT secret、超管密码、七牛密钥不进镜像层、构建上下文或受跟踪提交物。
- INV-004（持久数据不因重建丢失）：`down`（保留卷）+ `up` 后 MySQL/Redis 数据与本地存储文件仍在。
- INV-005（监控指标语义不变）：指标开关开启时 Prometheus `app` 目标 UP、`/metrics` 含 `flashsale_*`；关闭时不暴露。
- INV-006（统一入口不绕过）：外部只能经 Nginx 访问 Go API，app 不对外暴露端口，直连原 8000 端口不可达。
- INV-007（客户端 IP 防伪造）：经 Nginx 的登录/刷新，后端会话记录的客户端 IP 为 Nginx 真实对端 IP，伪造转发头不生效。
- INV-008（非 root 运行）：容器主进程有效 UID 非 0；`/app/storage/banners` 非 root 可写、轮播图占位图 seed 成功。
- INV-009（liveness 独立）：`/health` 判定不依赖 DB/Redis，进程存活即 200。
- INV-010（readiness 摘流）：`/ready` 在 DB 或 Redis 不可达时返回 503，响应/日志不泄漏凭据、路径、堆栈。
- INV-011（readiness 探测负载）：`/ready` 探测带超时 + 结果缓存，缓存窗口内重复调用不新增 DB/Redis 探测。
- INV-012（优雅停机一致性）：停机不产生半成品；消费事务被打断整体回滚、请求保持 `queued`，重启后不重复下单。

## 9. 一致性模型与失败语义

- **成功**：`docker compose up` 后 app 容器 healthy、`/health` 200、`/ready` 200、关键业务 API 正常；`/health` 200 代表进程存活，`/ready` 200 代表 DB/Redis 当前可达（受缓存窗口内可能延迟反映恢复），不代表「刚执行过 migration」。
- **MySQL 未就绪**：`depends_on` 等待 + `waitForDependencies` 重试，超时非零退出，`restart` 重试。
- **schema 未迁移**：entrypoint `migrate up` 执行；依赖未就绪致连接失败 → 非零退出重试；已迁移则幂等通过。
- **dirty / 迁移确定性失败**：`migrate up` 非零退出 → 容器 crash-loop，日志明确，需人工 `migrate force <version>`（不自动 force、不自动回滚）。
- **七牛凭据缺失 / 首次建号缺 `ADMIN_SUPER_PASSWORD`**：`serve` fail-fast 非零退出，日志指认字段名、不泄漏凭据值。
- 部分完成：migration dirty 需人工 `force` 恢复。
- **运行时依赖失效**：DB/Redis 不可达时 `/ready` 返回 503（fail-closed，不泄漏敏感信息），结果缓存 TTL 内不重复打库/打缓存，恢复后下一 TTL 窗口返回 200；`/health` 仍 200（liveness 不依赖依赖）。
- **优雅停机**：SIGTERM/SIGINT 触发后 HTTP 停收新连接并 drain 在途请求（`server.graceful_timeout`，超时强制退出），扫描器停止领取新任务、在途单事务回滚或完成；消费被打断 → 事务回滚 + 请求保持 `queued`，重启后重试/对账恢复，不产生半成品或重复下单。事实来源不变：MySQL 为业务权威、Redis 为派生缓存，无 MQ。

## 10. 安全与凭据边界

- 敏感凭据仅经运行时环境变量注入，不进镜像、不进 `config.yaml` 默认值、不进日志/响应；缺失即 fail-fast，**无内置默认回退**。
- `make init`/`make up` 复用两阶段初始化：`make init` 起 MySQL/Redis、生成 `.env`（要求已填 `MYSQL_ROOT_PASSWORD`，`scripts/init.sh` 缺失即报错，不要求七牛凭据、不启动后端）；`make up` 起 app（Compose 注入 `.env` 变量，要求 `AUTH_JWT_SECRET`，缺失由 `${VAR:?}` 与 serve fail-fast 双重拦截）。
- 开发命令（`make up/down/status/logs/health`）全面容器化，不再启动宿主机进程，杜绝双启动。

## 11. 镜像发布、版本标识与回滚（CNB 制品库）

`main` 分支更新后，CNB CI 在现有 `main.push`（`main-ci`）内以「单 job 顺序 stage」完成测试、构建与镜像发布；不引入多 job 隔离、不引入多架构构建。

- **镜像仓库地址**：`docker.cnb.cool/framework-station/my-shop`，由 `${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}` 派生（`CNB_DOCKER_REGISTRY = docker.cnb.cool`，仓库路径取当前仓库 slug 小写、无子路径）。fork 后 `CNB_REPO_SLUG_LOWERCASE` 指向 fork 自身 slug，不污染上游仓库。
- **版本标识**：同一次成功构建同时打两个标签——短 SHA 标签 `${CNB_COMMIT_SHORT}`（平台内置只读变量，完整提交 SHA 前 8 位）与 `latest`。二者由同一镜像经 `docker tag` 派生，指向同一 digest（双标签同源）。
- **`latest` 语义**：`latest` 始终指向最近一次「测试 + 构建 + 推送全部成功」的构建；测试或构建任一失败时不推送任何标签、`latest` 不变。
- **发布顺序**：`test` → `build`（go build）→ `docker build`（含 `docker tag :latest`）→ `docker push`（先短 SHA 后 `latest`）；任一 stage 失败即中断、后续不执行。短 SHA 先于 `latest` 推送，保证 `latest` 只会指向「短 SHA 标签已存在」的构建。
- **目标平台**：单平台 `linux/amd64`（沿用 Dockerfile `CGO_ENABLED=0 GOOS=linux` 静态编译，native build，不显式 `--platform`、不引入 buildx/QEMU）。
- **凭据**：制品库认证使用 CNB 内置凭据（流水线内置触发者凭据 / `CNB_TOKEN`），推送 CNB 制品库无需手动 `docker login`；`.cnb.yml` 与运行日志不出现硬编码密码。
- **回滚方法**：部署回滚用历史成功构建的短 SHA 标签拉取并运行目标版本（`docker run docker.cnb.cool/framework-station/my-shop:<短SHA>`），不依赖 `latest`；`latest` 仅表达「默认最新」，不作为可回滚的历史锚点。

## 12. Deferred / 已知留白

- Kubernetes/Ingress、HTTPS/TLS、多实例负载均衡（后续任务）。
- K8s 编排清单（Deployment/Pod/Probe/SecurityContext/HPA/Service/Ingress）：本次已落地容器运行能力（非 root、`/health` liveness、`/ready` readiness、优雅退出），但不产出 K8s 部署文件；`/ready` 将作为未来 readinessProbe 目标。
- CNB DinD 环境的真实构建/卷/网络能力以集成验证（AC-013）为准。
