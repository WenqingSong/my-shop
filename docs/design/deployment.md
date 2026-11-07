# 容器化部署模型（Deployment / Compose）

本文面向项目接手者，说明 SurgeCart 后端（及依赖）的容器化部署模型：服务编排、镜像与配置模型、启动生命周期、持久化、网络与健康检查语义。事实来源为 `backend-container-v1`、`nginx-reverse-proxy-v1` 与 `container-runtime-hardening-v1` 的 APPROVED Contract，以及 T2-06 交付的 Kustomize 清单。Compose 单机编排是本地与演示默认形态；Kubernetes 部署（单副本 Deployment/Service/迁移 Job、探针与终止预算映射）见 §12 与 `docs/development/kubernetes.md`，Ingress/HTTPS/TLS 与环境对外入口仍不在本仓库范围（无真实域名与证书方案）。

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

- **多阶段构建**：builder（Go 1.27.3，`CGO_ENABLED=0` 静态编译，依赖全为纯 Go）→ runtime（alpine 3.24.4 + `tzdata` + `ca-certificates`），工作目录 `/app`。`manifest/config/config.yaml` COPY 进镜像默认路径，保证 `time.LoadLocation("Asia/Shanghai")` 等时区语义可用（无 DST）。
- **基础镜像与可复现构建**：`FROM` 使用 patch 级固定标签（`golang:1.27.3-alpine3.24`、`alpine:3.24.4`），不使用 `latest` 或浮动 minor；`GOTOOLCHAIN=local`、`GOFLAGS=-mod=readonly`、`-trimpath`、`-buildvcs=false`，`go mod download` 与 `go build` 使用 BuildKit cache mount。实际 digest 与依赖版本记录在 `docs/development/container-security.md`。
- **非 root 运行**：runtime 阶段创建专用用户 `app:app`（`uid=10001`、`gid=10001`），`USER 10001:10001`；仅 `/app/storage` 归属运行用户。二进制、`config.yaml`、entrypoint 保持 `root` 所有且只读（`0555` / `0444`），运行用户无法篡改镜像内容。主进程有效 UID 非 0（不用 alpine `nobody`，固定身份便于未来 K8s `SecurityContext` 对接）。
- **只读根文件系统**：容器以 `read_only: true` 运行，唯一可写路径为 `/app/storage`（named volume）与 `/tmp`（`tmpfs`，`noexec/nosuid/nodev`）。容器启动时不修改任何文件权限，不需要 root。
- **配置与敏感凭据分离**：
  - `manifest/config/config.yaml` 仅保留普通非敏感配置（`server.address`、`dashboard.timezone`、`storage.local.root`、`order.*`、`flash_sale.*`、`qiniu.region/token_ttl/max_file_size/白名单` 等），敏感字段（`auth.jwt.secret`、`database.default.pass`、`qiniu.secret_key`、`admin.super.password`）默认值为空，可安全打包进镜像。
  - 敏感凭据（JWT secret、MySQL/Redis 密码、七牛 AK/SK/bucket/domain、超管密码）统一经运行时环境变量注入（Compose `environment` + `.env`），缺失时 `auth.Secret`/MySQL 认证/七牛 `ValidateConfig` fail-fast，**不回退任何内置默认密钥/密码**。
  - **无内置默认回退**：`docker-compose.yml` 对 `MYSQL_ROOT_PASSWORD`、`AUTH_JWT_SECRET` 使用 `${VAR:?error}` 必填语法，禁止 `${VAR:-default}`；`scripts/init.sh` 将二者加入 `REQUIRED_VARS` 缺失即报错（空字符串不可绕过）。`serve` 启动对 `DATABASE_DEFAULT_PASS`、`AUTH_JWT_SECRET` 显式非空校验，缺失非零退出、不监听端口、不回退内置默认值。
  - **连接字符集**：应用连接（`database.default.charset`）与 Migration DSN 统一为 `utf8mb4`，四字节 Unicode 字符（emoji / 补充平面字符）可经连接正确读写；表结构已为 utf8mb4，本次为连接层统一。
  - `.env.example` 为模板（敏感字段留空并注明必填）；`.env` 不提交 Git、不进镜像（`.dockerignore` 排除 `.env`、`bin/`、`tmp/`、`storage/`、`docs/`、`.agent/`、测试文件与运行数据）。

### 3.1 容器运行时安全（T2-04）

app 服务在 `docker-compose.yml` 中固定以下字段（其他服务不声明，保持原有生命周期与运行语义）：

| 字段 | 值 | 作用 |
| --- | --- | --- |
| `user` | `10001:10001` | 与镜像 `USER` 一致，防止编排层以 root 运行 |
| `read_only` | `true` | 只读根文件系统，阻断运行期写入镜像层 |
| `tmpfs` | `/tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777` | 唯一临时写路径，容器重建即清空 |
| `cap_drop` | `ALL` | 应用监听 8000，不需要任何 Linux capability |
| `security_opt` | `no-new-privileges:true` | 阻断 setuid/setgid 提权 |
| `stop_grace_period` | `60s` | 与 T2-03 终止预算同源，避免 Compose 默认 10s 提前 SIGKILL |
| `volumes` | `app_storage:/app/storage` | 唯一持久化写路径 |

不使用的配置：`privileged`、`cap_add`、`network_mode: host`、`pid: host`、Docker Socket 挂载。seccomp 沿用 Docker 默认 profile；镜像与 Compose 均不发布 app 宿主机端口（Nginx 是唯一入口）。数据卷首次挂载继承镜像内 `/app/storage` 的 `app:app` 属主，非 root 可直接写入，无需启动期 `chown`。

完整构建、验证、扫描与排障命令见 `docs/development/container-security.md`。

## 4. 启动生命周期

容器 entrypoint（镜像内 shell 脚本，不改 Go 代码）：

1. `my-shop migrate up`（幂等：已全部应用则成功返回；golang-migrate MySQL driver `GET_LOCK` 串行化并发迁移）。
2. `exec my-shop serve`：`boot.Bootstrap`（依赖连通重试 → schema 就绪只读校验 → 超管/权限 seed）→ 七牛 `ValidateConfig` → 终止预算校验（`termination_budget`）→ 秒杀后台组件装配 → 轮播图占位图 seed → 路由 → `s.Start()`（后续由 serve 自行接管终止信号）。

- 依赖未就绪：Compose `depends_on: condition: service_healthy` 先行等待，app 内 `waitForDependencies`（`STARTUP_DEPENDENCY_TIMEOUT`，默认 30s）再重试兜底。
- 启动幂等不并发：重复 `up`、容器重启、并发启动，migration 不重复、超管/权限不重复（`schema_migrations` 追踪 + `GET_LOCK` + 唯一约束 1062 兜底）。

停止生命周期（优雅关闭，T2-03 / T2-03.1 冻结契约）：`serve` 自行接管 `SIGINT/SIGTERM/SIGQUIT`，停机时序为：

1. 收到信号后先停收新连接（`net/http` Shutdown 立即关闭 listener），在 `server.graceful_timeout`（默认 10s，划分见 §4.1）内等待在途请求完成；
2. 若优雅 drain 未完成：输出 `http_server_drain_timeout` → 强制关闭 listener 与全部连接（等价 `http.Server.Close`，`http_server_force_closed`）→ 确认 Shutdown 返回且连接清零后输出 `http_server_stopped`（`timed_out=true`）。**总耗时不超过 10s，不会因为等待 Shutdown 而突破预算**；
3. 输出 `http_server_stopped` 之后才调用 `FlashSaleRuntime.Shutdown`，按 Publisher → Consumer → Producer → Scanner 顺序停止，整体受 `flash_sale.shutdown_timeout`（默认 30s）约束；
4. 关闭流程结束、`serve` 返回，进程以退出码 0 结束。

正常路径事件为 `http_server_drain_begin → http_server_stopped → flashsale_runtime_shutdown_begin`；超时路径为
`http_server_drain_begin → http_server_drain_timeout → http_server_force_closed → http_server_stopped → flashsale_runtime_shutdown_begin`。
强制关闭只保证 listener 与连接关闭、Server 停止；Go 无法终止忽略取消的 handler goroutine，这类 goroutine 随进程退出终止（不会阻塞关闭预算）。

后台组件不继承信号 ctx（B4.1：`context.WithoutCancel`），SIGTERM 不会绕过上述顺序并发取消 Publisher/Consumer/Scanner。消费事务被打断时整体回滚、请求保持 `queued`，重启后由重试/对账恢复，不产生半成品或重复下单。

### 4.1 容器终止预算（T2-03 冻结）

| 段 | 数值 | 配置来源 |
|---|---:|---|
| preStop | 5s | `lifecycle.pre_stop_delay` / `LIFECYCLE_PRE_STOP_DELAY` |
| HTTP drain | 10s | `server.graceful_timeout` / `SERVER_GRACEFUL_TIMEOUT` |
| Runtime Shutdown | 30s | `flash_sale.shutdown_timeout` / `FLASH_SALE_SHUTDOWN_TIMEOUT` |
| 安全余量 | 15s | `internal/termination.DefaultSafetyMargin` |
| terminationGracePeriodSeconds / Compose `stop_grace_period` | 60s | `lifecycle.termination_grace_period` / `docker-compose.yml` 的 app 服务 |

预算关系：`60s >= 5s + 10s + 30s + 15s`。**preStop 的耗时计入 60s 宽限期，不是额外的 5 秒。**

HTTP drain 的 10s 划分：前 9s 用于优雅 drain（同时作为 GoFrame 内部 graceful 关闭超时，保证 `Shutdown` 调用一定在预算内返回），最后 1s 用于强制关闭 listener/连接并确认真实停止。预算不足 2s 时按一半划分。

- 应用启动时校验该关系（不足即 fail-fast）并输出 `termination_budget` 结构化日志，字段为 `pre_stop_delay` / `http_drain_timeout` / `runtime_shutdown_timeout` / `required_termination_budget` / `termination_grace_period` / `safety_margin`。
- `docker compose stop/down` 使用 `stop_grace_period: 60s`，避免 Compose 默认 10s 提前 SIGKILL；其他容器不声明该字段。
- Kubernetes 清单（T2-06）使用 `terminationGracePeriodSeconds: 60` 与 5s preStop（`/bin/sh -c "sleep 5"`），并由 `test/manifest` 从 Kustomize 构建产物断言 preStop、宽限期与 `LIFECYCLE_*` 环境变量同源；`internal/termination` 的契约测试继续断言 Go 默认值、`config.yaml`、`docker-compose.yml` 三方一致，两处断言互不替代。

### 4.2 生产迁移独立执行（T2-05）

生产发布把 schema 变更从应用启动流程中拆出来：

```text
构建镜像（短 SHA） → 执行 Migration Job（migrate up） → 成功 → 更新 Deployment
                          └─ 失败 → 阻止升级，人工排障
```

| 维度 | Compose（本地） | Kubernetes（生产） |
|---|---|---|
| 迁移执行者 | `docker-entrypoint.sh`：`migrate up` → `exec serve` | 独立 Job：`/app/my-shop migrate up` |
| 应用镜像默认入口 | 同左（镜像默认 `serve`，Compose 覆盖为 entrypoint） | `serve`，不执行任何 DDL |
| 迁移失败表现 | 容器非零退出被 `restart` 重试（dirty 需人工） | Job `Failed` + `kubectl wait` 超时，流水线中止 |
| 并发保护 | 同一 Compose 单实例；多容器并发同样由 `GET_LOCK` 串行 | 跨 Pod 由数据库 `GET_LOCK` 互斥，Job `parallelism: 1` |
| 配置校验 | 严格模式默认关闭（本机地址可用） | `DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true`，迁移只校验数据库两项 |

要点：

- **Job 先于 Deployment**：`kubectl wait --for=condition=complete job/surgecart-migrate-<短SHA>` 成功后才允许更新应用清单；`scripts/deploy-k8s.sh` 在迁移失败时以非零退出码终止且不 apply 应用。
- **最后防线是 serve**：`serve` 启动时只读校验 `schema_migrations`（非 dirty 且 `current >= latest`），未就绪直接非零退出，不会以「旧 schema + 新代码」对外服务。
- **不使用 `latest` 作为迁移锚点**：Job 镜像使用短 SHA，保证回滚时能回到确切版本对应的 schema 状态。
- **不使用 `migrate force` 作为 Job 命令**：force 是人工风险操作（见 `docs/development/migrations.md` §9）。
- 清单与运维命令见 `manifest/kubernetes/migration/`、`docs/development/migrations.md`；Kustomize 部署体系见 `docs/development/kubernetes.md`。

## 5. 持久化

- MySQL/Redis：named volume `mysql_data`/`redis_data`，`down`（保留卷）+ `up` 重建不丢数据。
- 本地文件：named volume `app_storage` → `/app/storage`（容器内 `STORAGE_LOCAL_ROOT=/app/storage`）。V1 本地存储仅幂等 seed 轮播图占位图（可重建），卷的「不丢失」语义面向未来落本地的文件；用 named volume（非 bind mount）规避 rootless/嵌套容器下 bind mount 单文件退化为目录的问题。镜像内 `/app/storage` 已 `chown` 为 `app:app`，named volume 首次挂载继承镜像目录 owner，故非 root 下 `/app/storage/banners` 可写、占位图 seed 成功。

## 6. 网络与健康检查

- `app:8000` 是稳定的 Compose 内部服务入口，**不映射宿主机端口**；外部只能经 Nginx（`${NGINX_PORT:-8080}`）访问。
- 健康检查语义（T2-02 冻结，后续 K8s probe 直接复用）：

  | 接口 | 检查内容 | Kafka 故障时 |
  | --- | --- | --- |
  | `/health`（liveness） | 仅判断 HTTP 服务进程是否存活；不访问 MySQL/Redis/Kafka，不检查消费组/积压 | 200 |
  | `/ready`（readiness） | MySQL `PingMaster` + Redis `PING` 均成功；任一不可达 503（fail-closed） | 200（前提是两者正常） |

  探测超时默认 1s（`READINESS_PROBE_TIMEOUT`），结果进程内缓存默认 3s（`READINESS_CACHE_TTL`），
  缓存窗口内重复调用不新增 DB/Redis 访问，依赖恢复后在一个 TTL 窗口内恢复 200；响应/日志不泄漏
  数据库地址、Redis 信息或错误堆栈。schema 就绪由启动 bootstrap 保证（`serve` 仅在
  `checkSchemaReady` 通过后监听），不逐次探测。
- **Kafka 明确不在健康判定内**：Broker 不可达、Consumer 未加入消费组、Outbox 积压都不影响
  `/health` 与 `/ready`。Kafka 故障时 HTTP 写入路径仍可把请求可靠写入 MySQL Outbox，
  系统表现为异步处理延迟增加；把 Kafka 纳入 readiness 会让一次 Broker 抖动摘掉整个 HTTP 入口，
  这是被禁止的（`internal/logic/health` 用依赖调用计数与真实 Kafka 停机故障注入锁定该语义）。
  异步侧状态由 `flashsale_consumer_joined`、`flashsale_outbox_backlog_pending` 观测，
  详见 `docs/development/kafka.md` §10.12 / §10.13。
- `/health` 经 Nginx 公开透传（最小探活响应）；`/ready` 当前仅作未来 K8s readinessProbe 目标，Compose 的 app/nginx healthcheck 仍用 `/health`（Compose 无 Service 摘流机制，用 `/ready` 作 healthcheck 会放大 DB/Redis 抖动为 unhealthy + `restart` 误触发）。
- **健康检查职责分工（T2-04）**：镜像内 `HEALTHCHECK`（`--interval=10s --timeout=5s --start-period=60s --retries=5`，`wget /health`）服务于 `docker run` 场景；Compose 显式声明同值 healthcheck（`scripts/test-container.sh` 断言两处一致）；Kubernetes 忽略 Dockerfile `HEALTHCHECK`，改用 livenessProbe `/health` 与 readinessProbe `/ready`。镜像不额外安装 curl/wget/bash，健康检查复用 alpine 自带 busybox wget。
- Prometheus 抓取目标为 `app:8000`（不经 `host.docker.internal`、不经 Nginx）；`/metrics` 仅在 `FLASH_SALE_METRICS_ENABLED=true`（`flash_sale.metrics.enabled`）时暴露，抓取内容与开关语义不变；Nginx 公共入口对 `/metrics` 返回 404，不转发。`/metrics` 抓取路径不查数据库：Outbox 积压由进程内 5s 采样器写入 Gauge（查询失败保留上一次值），因此抓取超时不等于依赖故障。

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
- INV-013（Kafka 不参与健康判定）：`/health` 与 `/ready` 的判定集合不包含 Kafka 连接、消费组状态、Consumer 运行状态与 Outbox 积压；Kafka 故障期间进程不退出、两个端点仍按各自语义返回 200。

## 9. 一致性模型与失败语义

- **成功**：`docker compose up` 后 app 容器 healthy、`/health` 200、`/ready` 200、关键业务 API 正常；`/health` 200 代表进程存活，`/ready` 200 代表 DB/Redis 当前可达（受缓存窗口内可能延迟反映恢复），不代表「刚执行过 migration」。
- **MySQL 未就绪**：`depends_on` 等待 + `waitForDependencies` 重试，超时非零退出，`restart` 重试。
- **schema 未迁移**：entrypoint `migrate up` 执行；依赖未就绪致连接失败 → 非零退出重试；已迁移则幂等通过。
- **dirty / 迁移确定性失败**：`migrate up` 非零退出 → 容器 crash-loop，日志明确，需人工 `migrate force <version>`（不自动 force、不自动回滚）。
- **七牛凭据缺失 / 首次建号缺 `ADMIN_SUPER_PASSWORD`**：`serve` fail-fast 非零退出，日志指认字段名、不泄漏凭据值。
- 部分完成：migration dirty 需人工 `force` 恢复。
- **运行时依赖失效**：DB/Redis 不可达时 `/ready` 返回 503（fail-closed，不泄漏敏感信息），结果缓存 TTL 内不重复打库/打缓存，恢复后下一 TTL 窗口返回 200；`/health` 仍 200（liveness 不依赖依赖）。
- **Kafka 失效（Broker 停机 / Consumer 未加入 / 发布持续失败）**：`/health` 与 `/ready` 均保持 200，进程不退出；HTTP 继续受理并把请求写入 MySQL Outbox（`status=queued`、`published_at IS NULL`），`flashsale_consumer_joined` 变为 0、`flashsale_outbox_backlog_pending` 上升；Broker 恢复后 Consumer 重新加入（指标回到 1）、积压收敛、请求进入正确终态且不重复下单/不超卖。
- **优雅停机**：SIGTERM/SIGINT 触发后 HTTP 停收新连接并在 `server.graceful_timeout`（默认 10s）内 drain 在途请求；超时则在预算内强制关闭 listener 与全部连接，确认 `http_server_stopped` 后才按 Publisher → Consumer → Producer → Scanner 顺序关闭后台组件（`flash_sale.shutdown_timeout`，默认 30s），扫描器停止领取新任务、在途单事务回滚或完成；预算不足时 serve 启动 fail-fast，实际终止宽限期与预算见 §4.1。消费被打断 → 事务回滚 + 请求保持 `queued`，重启后重试/对账恢复，不产生半成品或重复下单。事实来源不变：MySQL 为业务权威、Redis 为派生缓存，无 MQ。

## 10. 安全与凭据边界

- 敏感凭据仅经运行时环境变量注入，不进镜像、不进 `config.yaml` 默认值、不进日志/响应；缺失即 fail-fast，**无内置默认回退**。
- 镜像安全扫描（Trivy 镜像/配置 + govulncheck）的命令、分级结果与未修复漏洞说明见 `docs/development/container-security.md`；扫描是发布前门禁的一部分，不得以「工具未安装」代替通过结论。
- `make init`/`make up` 复用两阶段初始化：`make init` 起 MySQL/Redis、生成 `.env`（要求已填 `MYSQL_ROOT_PASSWORD`，`scripts/init.sh` 缺失即报错，不要求七牛凭据、不启动后端）；`make up` 起 app（Compose 注入 `.env` 变量，要求 `AUTH_JWT_SECRET`，缺失由 `${VAR:?}` 与 serve fail-fast 双重拦截）。
- 开发命令（`make up/down/status/logs/health`）全面容器化，不再启动宿主机进程，杜绝双启动。

## 11. 镜像发布、版本标识与回滚（CNB 制品库）

`main` 分支更新后，CNB CI 在现有 `main.push`（`main-ci`）内以「单 job 顺序 stage」完成测试、构建与镜像发布；不引入多 job 隔离、不引入多架构构建。

- **镜像仓库地址**：`docker.cnb.cool/framework-station/my-shop`，由 `${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}` 派生（`CNB_DOCKER_REGISTRY = docker.cnb.cool`，仓库路径取当前仓库 slug 小写、无子路径）。fork 后 `CNB_REPO_SLUG_LOWERCASE` 指向 fork 自身 slug，不污染上游仓库。
- **版本标识**：同一次成功构建同时打两个标签——短 SHA 标签 `${CNB_COMMIT_SHORT}`（平台内置只读变量，完整提交 SHA 前 8 位）与 `latest`。二者由同一镜像经 `docker tag` 派生，指向同一 digest（双标签同源）。
- **`latest` 语义**：`latest` 始终指向最近一次「测试 + 构建 + 推送全部成功」的构建；测试或构建任一失败时不推送任何标签、`latest` 不变。
- **发布顺序**：`make test` → `make test-race` → `make build`（go build）→ `make docker-build`（含 `docker tag :latest`）→ `make docker-push`（先短 SHA 后 `latest`）；任一 stage 失败即中断、后续不执行。短 SHA 先于 `latest` 推送，保证 `latest` 只会指向「短 SHA 标签已存在」的构建。
- **目标平台**：单平台 `linux/amd64`（沿用 Dockerfile `CGO_ENABLED=0 GOOS=linux` 静态编译，native build，不显式 `--platform`、不引入 buildx/QEMU）。
- **凭据**：制品库认证使用 CNB 内置凭据（流水线内置触发者凭据 / `CNB_TOKEN`），推送 CNB 制品库无需手动 `docker login`；`.cnb.yml` 与运行日志不出现硬编码密码。
- **回滚方法**：部署回滚用历史成功构建的短 SHA 标签拉取并运行目标版本（`docker run docker.cnb.cool/framework-station/my-shop:<短SHA>`），不依赖 `latest`；`latest` 仅表达「默认最新」，不作为可回滚的历史锚点。Kubernetes 回滚同理：把三处版本（`images.newTag` / `app.kubernetes.io/version` / 迁移 Job `nameSuffix`）改回上一个短 SHA，由人工显式执行 `bash scripts/deploy-k8s.sh --allow-downgrade`（T2-06.1 默认禁止降级，CI 还需 `DEPLOY_K8S_ALLOW_DOWNGRADE_IN_CI=1`）；迁移不回滚（schema 向前兼容是发布前提），见 `docs/development/kubernetes.md` §9.5 与 §10。

## 12. Kubernetes 部署体系（T2-06）

生产部署从「Compose 单机编排」扩展到「Kustomize + Kubernetes 资源」，但**不改变**启动、
探针与终止语义。完整操作手册见 `docs/development/kubernetes.md`，这里只记录与 Compose 模型
的对应关系与差异。

| 维度 | Compose（本地） | Kubernetes（生产） |
|---|---|---|
| 部署单元 | `app` 服务（单实例） | `Deployment/surgecart`（`replicas: 1`，`Recreate`） |
| 配置 | `.env` + `manifest/config/config.yaml` | ConfigMap（非敏感）+ Secret（凭据） |
| 入口 | Nginx → `app:8000`（Compose 网络） | `Service/surgecart`（ClusterIP，集群内） |
| 迁移 | `docker-entrypoint.sh`：`migrate up` → `exec serve` | 独立 Migration Job（版本化 Job 名称），先于应用 |
| 存储 | named volume `app_storage` | PVC `surgecart-storage`（RWO 5Gi，单副本） |
| 健康检查 | Docker `HEALTHCHECK /health` | `startupProbe`/`livenessProbe` `/health`、`readinessProbe` `/ready` |
| 终止预算 | `stop_grace_period: 60s` | `terminationGracePeriodSeconds: 60` + preStop 5s |
| 安全上下文 | `read_only`、非 root、`cap_drop: ALL` | Pod/Container `securityContext`（同等级，见 `docs/development/container-security.md`） |

发布顺序（两入口独立，不可合并）：

```text
构建并推送镜像（短 SHA）
    → 更新三处版本（镜像 tag / 版本标记 / 迁移 Job 后缀，三者同值）
    → 获取发布锁 Lease/surgecart-deploy-lock（原子 create + resourceVersion CAS；
      接管前先核验上一轮 Migration Job 是否已终态，否则拒绝接管）
    → 锁内版本降级保护（当前版本 vs 目标版本，Git 祖先关系）
    → 构建 Migration 清单 → 记录预期 Job 名（Lease 注解）→ 创建 Job → 回写真实 Job UID
    → 等待完成
         ├─ Failed=True     → 阻止应用升级（退出码 2，不 force、不删 Job）
         ├─ 等待超时        → 不删 Job、不应用（退出码 6，超时≠失败；锁不释放，
         │                     关联记录保留，下一个 Runner 会先核验该 Job）
         ├─ Job 状态不明    → 阻止发布（退出码 7，人工核实）
         └─ Complete        → 应用 Application 清单 → 等待 Deployment 收敛
    → 发布成功判定（镜像 / 版本标记 / generation / 副本 / 实际 Pod）
    → 释放发布锁（保留迁移记录）→ 打印 DEPLOY_K8S_SUCCESS
```

发布锁的设计、迁移生命周期绑定、CAS 语义与剩余风险（没有 fencing token、时钟依赖、
当前是 cooperative/advisory 互斥）见 `docs/design/deploy-lock.md`。

已知边界（详见 `docs/development/kubernetes.md`）：

- 单副本是**当前存储模型**的结论，不是临时保守：本地文件目录不是共享存储，多 Pod 会各写各的。
- 没有 Ingress/TLS：仓库不虚构域名与证书方案，对外入口由环境叠加。
- 未在真实集群验证：调度、滚动、PVC 绑定、preStop/SIGTERM 实测、Lease 的真实并发语义均为 NOT_VERIFIED。
- 发布锁只串行化「经过 `scripts/deploy-k8s.sh` 的发布」；直接 `kubectl apply` 不受保护。

## 13. Deferred / 已知留白

- Kubernetes/Ingress、HTTPS/TLS、多实例负载均衡（后续任务；T2-06 已交付单副本 Deployment/Service/Kustomize，Ingress 与环境对外入口仍留待真实集群方案）。
- 应用侧 HPA：当前存储模型为单副本 + RWO PVC，扩容前置条件是共享存储或对象存储改造，暂不引入 HPA。
- 迁移 Job 未在真实集群验证：调度、`kubectl wait` 流转、TTL 清理、NetworkPolicy 连通性、镜像拉取凭据均为 NOT_VERIFIED（见 `docs/development/migrations.md` §12）。
- 生产镜像的 `serve` 启动需要真实七牛凭据（`GetBucketInfo` 外部只读校验）。容器生命周期验证使用同一运行层、仅额外启用 `serve_lifecycle` 构建标签的 `lifecycle-verify` 目标（见 `docs/development/container-security.md`）；生产二进制本身不包含测试路由或绕过开关。
- CNB DinD 环境的真实构建/卷/网络能力以集成验证（AC-013）为准。
