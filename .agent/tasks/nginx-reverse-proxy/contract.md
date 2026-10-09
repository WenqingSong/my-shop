# Technical Contract

## Decision Status
APPROVED

## Problem

现有 my-shop 部署中，Go 应用以宿主机进程监听 `:8000`，客户端直连该端口；MySQL/Redis/Prometheus 由 `docker-compose.yml` 部署。本任务在客户端与 Go 服务之间引入 Nginx 反向代理（宿主机 `8080` 入口），同时保持登录/JWT 鉴权/秒杀/静态资源/客户端 IP 语义与 Prometheus 直采不变，并明确可信代理边界与测试/生产入口的网络隔离。

四个关键设计选择（Task `Analyst Questions`）：
1. 真实客户端 IP 与防伪造：GoFrame `GetClientIp()` 当前盲信 `X-Forwarded-For`，引入 Nginx 后如何在「覆盖转发头」与「防止客户端伪造」之间取舍。
2. 网络隔离 vs Prometheus 直采：既要「避免绕过 Nginx 直连 `:8000`」又要「Prometheus 继续直采 `host.docker.internal:8000/metrics`」。
3. 静态资源：Nginx 只读挂载直供 vs 保留 Go 静态代理。
4. Nginx 不对外暴露 `/metrics` 的配置方式。

## Verified Current Behavior

- VERIFIED：Go 以宿主机进程监听 `:8000`（`manifest/config/config.yaml` 的 `server.address`，环境变量 `SERVER_ADDRESS` 覆盖），由 `scripts/up.sh`（`nohup bin/my-shop serve &`）启动；MySQL、Redis、Prometheus 经 `docker-compose.yml` 部署。
- VERIFIED：Prometheus 容器 `extra_hosts: host.docker.internal:host-gateway`，抓取 `host.docker.internal:8000/metrics`，`scrape_interval: 15s`（`prometheus/prometheus.yml`）。`scripts/test-prometheus.sh` 静态断言目标为 `host.docker.internal:8000`。
- VERIFIED：`GET /health`（`api/health/v1/health.go` 的 `g.Meta path:"/health" method:"get"`），经 `middleware.Response` 包装返回 `{"code":0,...,"data":{"status":"ok",...}}`；当前无 `/healthz`、`/readyz`。
- VERIFIED：`/metrics` 仅在 `flash_sale.metrics.enabled=true`（`FLASH_SALE_METRICS_ENABLED`）时挂载为 `GET /metrics`（`internal/cmd/cmd.go` 的 `registerMetricsRoute`），无鉴权，位于统一 JSON 响应包装之外。
- VERIFIED：静态资源由 Go 的 `AddStaticPath("/storage/banners", <root>/banners)` 服务（`internal/storage/storage.go` 的 `BannerURLPrefix="/storage/banners"`、`BannerDir()=<storage.local.root>/banners`；`storage.local.root` 默认 `./storage`，`STORAGE_LOCAL_ROOT` 覆盖）；`serve` 启动时 `configureBannerStorage` seed 3 张占位图（`banner-1/2/3.png`）。
- VERIFIED：GoFrame `v2.10.3` 的 `GetClientIp()`（`ghttp_request.go:188`）依次读取 `X-Forwarded-For`（首个逗号值）→ `Proxy-Client-IP` → `WL-Proxy-Client-IP` → `HTTP_CLIENT_IP` → `HTTP_X_FORWARDED_FOR` → `X-Real-IP` → `RemoteAddr`；源码注释明确警告「该 IP 可能被客户端 Header 篡改」。
- VERIFIED：会话 IP 仅作设备元数据：`internal/controller/iam/iam.go` 的 `Login`/`Refresh` 用 `r.GetClientIp()` 写入 `SessionMeta.IP`（`internal/logic/iam/iam.go` / `refresh.go`）；`middleware/auth.go` 的 `ValidateSession` 只校验 `sid/userID`，不校验 IP（IP 非鉴权门禁）。
- VERIFIED：生命周期脚本 `scripts/up.sh`（`up -d mysql redis prometheus`）、`down.sh`（`docker_compose down`）、`status.sh`/`health.sh`/`logs.sh` 只引用 `mysql redis prometheus`；`Makefile` 已有 `test-prometheus`、`health`、`status`；`.env.example` 列全量配置键。
- VERIFIED：当前无 `docs/design/deployment.md`；`docs/design/storage.md` 已声明 LocalStorage（轮播图静态服务）与七牛上传为两个独立关注点；`docs/design/iam.md` 已声明 session `ip/user_agent` 为设备信息。
- UNKNOWN：无阻塞性未知项。`host-gateway` 依赖 Docker 20.10+（与现有 Prometheus 用法一致，已假设成立）；Nginx 官方镜像可用性未在本环境实际拉起（Coder/Deliverer 验证）。

## Recommendation

RECOMMENDATION：**Nginx 作为唯一可信边缘代理，全量转发到 Go（`host.docker.internal:8000`），覆盖转发头以取得真实 IP 并防伪造；不修改 Go 代码；静态资源保留 Go 代理；`/metrics` 经 Nginx 显式拒绝；隔离模型 = Nginx `8080` 为客户端入口 + Go `:8000` 内部可达 + 生产防火墙/安全组作为硬边界（文档化，不落 compose）。**

各设计点：

1. **真实客户端 IP 与防伪造（Q1）——Nginx 覆盖转发头，不改 Go。**
   - Nginx 配置（`proxy_set_header`）：
     - `X-Real-IP $remote_addr;`
     - `X-Forwarded-For $remote_addr;`（**覆盖**，不使用 `$proxy_add_x_forwarded_for`，从而丢弃客户端伪造的 `X-Forwarded-For`）
     - `X-Forwarded-Proto $scheme;`
     - `Host $host;`
   - 由于 Nginx 是边缘（上游无其它可信代理/LB/CDN），`$remote_addr` 即真实客户端 IP；GoFrame `GetClientIp()` 首读 `X-Forwarded-For` 得到真实 IP，满足 AC-003。
   - 防伪造（AC-004）：Nginx 覆盖后，客户端注入的 `X-Forwarded-For`/`X-Real-IP` 被丢弃，服务端最终记录 `$remote_addr`，不被伪造值冒充。
   - 关键取舍：不改 Go 代码，靠「Nginx 覆盖 + 网络隔离」双重边界防伪造。被否方案为「Go 侧新增可信代理中间件（校验 `RemoteAddr` 是否为 Nginx 网关 IP 才信任转发头）」——AC-004 只要求「经 Nginx 的请求不被伪造」，Nginx 覆盖已满足，Go 侧改动非必需且违反「Go 业务代码原则上不改动」。作为可选加固记入 Open Risks，不纳入本次 Scope。

2. **网络隔离 vs Prometheus 直采（Q2）——不改 Go 绑定，生产边界用防火墙/安全组。**
   - Go 保持 `:8000`（`0.0.0.0`）不变：`host-gateway` 把 `host.docker.internal` 解析为宿主机 Docker 网桥网关 IP（非 loopback），Nginx 与 Prometheus 容器必须能连到该非-loopback 地址，故 Go 不能只绑 `127.0.0.1`。
   - 隔离模型：
     - **测试/开发（默认）**：Nginx `8080` 是唯一对外客户端入口；Go `:8000` 为内部端口，仅宿主机 loopback + Docker 网桥（容器）可达。开发机无需防火墙，边界为「客户端一律走 Nginx `8080`，不对外暴露 `:8000`」。
     - **生产**：硬边界 = 主机防火墙/云安全组规则：`8000` 与 `9090` 仅允许来自受信网络（Docker 网桥网段 + loopback），公网只暴露 `8080`（Nginx）。此规则**文档化**于 `docs/design/deployment.md`，不在 `docker-compose.yml` 内实现（主机防火墙超出 compose 范围；Go 容器化在 Out of Scope）。
   - 关键取舍：由于 Go 是宿主机进程且必须对容器可达，compose 内无法实现「完全硬隔离」；生产硬隔离依赖防火墙/安全组（文档约束）。若 Owner 要求 compose 内硬隔离，需容器化 Go 或独立 metrics 监听端口——两者均在 Out of Scope，需另立任务。

3. **静态资源（Q3）——保留 Go 静态代理。**
   - Nginx 全量反向代理（含 `/storage/banners/*`），静态文件仍由 Go `AddStaticPath` 服务。
   - 理由：占位图为 3 张极小 PNG、由 Go 启动时 seed；Nginx 直供需宿主机目录只读 bind-mount，引入 `STORAGE_LOCAL_ROOT` 覆盖、SELinux、权限、root 空目录竞态等耦合，收益可忽略。保留 Go 代理零改动、完全满足 AC-005 的 URL/内容兼容。
   - 关键取舍：可逆决策；静态体量增大后，可由后续任务引入「共享卷 + Nginx 直供」。

4. **`/metrics` 不外露（Q4）——显式拒绝。**
   - Nginx 在 `location = /metrics { return 404; }` 置于 catch-all `location / { proxy_pass ... }` 之前；其余路径全量转发。`/healthz`、`/readyz` 不新增（Nginx 转发到 Go，Go 返回 404），满足 AC-002/AC-006。

## Selected Design

Owner 已 ACCEPT 推荐方案（以下为已确认设计）：

1. **客户端 IP 与防伪造**：Nginx 作为唯一可信边缘，覆盖 `X-Forwarded-For` / `X-Real-IP`（`$remote_addr`），丢弃客户端伪造值；同时设置 `X-Forwarded-Proto $scheme`、`Host $host`。不修改 Go 业务代码；依赖网络隔离防止绕过 Nginx 直连伪造。
2. **网络隔离**：Go 保持 `:8000`（`0.0.0.0`）不变；客户端入口为 Nginx `:8080`；生产环境经防火墙/安全组隔离 Go `:8000`（仅允许 Docker 网桥网段 + loopback 可达）。当前不容器化 Go、不引入独立 Metrics 端口。**交付验收必须实际验证外部无法直连 `:8000`；未验证的环境不得声明生产安全就绪。**
3. **静态资源**：保留现有 Go 静态资源服务，由 Nginx 反向代理，不引入宿主机目录挂载。
4. **Metrics**：Nginx 对外 `/metrics` 返回 404；Prometheus 继续直接抓取 `host.docker.internal:8000/metrics`。

范围约束：不新增多实例负载均衡；不引入 HTTPS/TLS；不修改秒杀业务逻辑；保持 JWT 认证、HTTP 状态码及原有 API 行为不变。

## Interfaces and Data

- 新增 `nginx/nginx.conf`（版本化，只读挂载进容器）；`docker-compose.yml` 新增 `nginx` 服务（官方镜像如 `nginx:stable-alpine`，`ports: "${NGINX_PORT:-8080}:80"`，`extra_hosts: host.docker.internal:host-gateway`，`restart: unless-stopped`，配置只读挂载）。
- 新增 `scripts/test-nginx.sh` + `Makefile` 的 `test-nginx`（`docker compose config -q` + `nginx -t` + 转发头/`/metrics` 拒绝等静态断言）。
- 修改 `scripts/up.sh`（`up -d` 增加 `nginx`）、`scripts/status.sh`/`health.sh`/`logs.sh`（纳入 `nginx`）、`Makefile`、`README.md`、`.env.example`（新增 `NGINX_PORT`）。
- 新增 `docs/design/deployment.md`（部署拓扑、可信代理边界、网络隔离、Prometheus 直采关系）。
- 保持不变：Go 业务代码、`internal/storage` 静态服务、Redis/MySQL、`prometheus/prometheus.yml` 与 `prometheus/rules/*`（Prometheus 直采目标与 `scrape_interval=15s` 不变）。
- 不新增数据库迁移、不新增错误码、不新增 `/healthz`/`/readyz` 接口。

## Business Invariants

- INV-001（真实客户端 IP）：经 Nginx 访问 `/login`、`/refresh` 时，GoFrame `GetClientIp()` 返回真实客户端 IP（`$remote_addr`），而非 Nginx 容器 IP。
- INV-002（防伪造）：客户端请求头携带伪造 `X-Forwarded-For`/`X-Real-IP` 经 Nginx 访问时，服务端最终记录的 IP 等于真实来源，不被伪造值冒充。
- INV-003（业务行为不变）：经 Nginx 访问现有 API 的 Method、请求体、状态码、响应体与 `Authorization` Header 与直连 Go 一致；登录、JWT 鉴权、秒杀下单 Smoke Test 通过。
- INV-004（指标暴露边界）：Nginx 对外入口访问 `/metrics` 返回 404；Prometheus 仍直采 `host.docker.internal:8000/metrics`，`scrape_interval=15s`、p95/p99 Recording Rules 不变。
- INV-005（静态资源兼容）：`/storage/banners/<file>` 的 URL 与返回内容与现有 Go 静态服务一致（保留 Go 代理）。
- INV-006（端口边界）：客户端入口为 Nginx `:8080`，Go `:8000` 不作为对外客户端入口（生产经防火墙/安全组仅允许 Docker 网桥 + loopback 可达）。

## Failure and Consistency Semantics

- Nginx 为无状态反向代理，不引入缓存（`proxy_cache` 默认关闭），无多存储一致性问题；事实来源仍为 Go 服务与 MySQL/Redis。
- 成功语义：经 Nginx `GET /health` 返回 200 + `status=ok` 即代表「Nginx→Go 代理链路通且 Go 存活」。
- Nginx 容器重启：`restart: unless-stopped` + 静态配置，自动恢复代理，无需手工步骤（AC-007）。
- Go 进程不可用：Nginx 返回 502/504（代理错误），不产生数据写入/破坏；`proxy_connect_timeout`/`proxy_read_timeout`/`proxy_send_timeout` 配置为合理值（建议 connect 3-5s，read/send 60s），超时返回 504。
- `/metrics` 经 Nginx 一律 404，不影响 Prometheus 直采 Go `:8000/metrics` 的现有行为。

## Allowed / Forbidden Changes

- 允许：新增 Nginx 配置与 `nginx` 服务、`scripts/test-nginx.sh`、`docs/design/deployment.md`；对 `docker-compose.yml`、`Makefile`、`scripts/up.sh`/`status.sh`/`health.sh`/`logs.sh`、`README.md`、`.env.example` 的必要增改。
- 禁止：修改 Go 业务代码（含 `internal/storage`、`internal/controller/iam`、`internal/middleware`、`internal/cmd` 的现有行为）、Redis/MySQL、`prometheus/prometheus.yml` 与 `prometheus/rules/*`、数据库迁移、错误码；新增 `/healthz`/`/readyz` 接口；Nginx 对外暴露 `/metrics`；Go 容器化、HTTPS/TLS、多实例负载均衡、Kubernetes/Ingress、Nginx 业务限流。

## Verification Requirements

- INV-001/INV-002 → 真实环境：经 Nginx（`8080`）用固定来源 IP 触发 `/login`、`/refresh`，通过会话记录/`GET /sessions` 核实记录 IP 等于真实来源；注入伪造 `X-Forwarded-For`/`X-Real-IP` 后再次触发，核实记录 IP 不被伪造值冒充。需 MySQL/Redis 就绪。
- INV-003 → 分别对 Nginx（`8080`）与 Go（`8000`）发起 `/health`、`/login`、`/refresh`、`/flash-sales/:id/orders`，逐项对比 Method/请求体/状态码/响应体/`Authorization` Header；`/health` 经 Nginx 返回 200 + `status=ok`。
- INV-004 → 以 `FLASH_SALE_METRICS_ENABLED=true` 运行，经 Prometheus API 验证 Target UP、p95/p99 Recording Rule 产物非空；经 Nginx 访问 `/metrics` 预期 404。
- INV-005 → 经 Nginx 与直连 Go 分别访问 `/storage/banners/banner-1.png`，对比状态码与内容一致。
- INV-006 → 对照 Contract 确认测试/生产入口对 `:8000` 的暴露边界并核对 `docs/design/deployment.md` 与实现一致；交付验收必须实际验证非可信来源直连 `:8000` 被隔离（生产经防火墙/安全组）。测试环境若 `:8000` 仍可达（`0.0.0.0` 且无防火墙），必须显式标注为 dev-only，不得声明生产安全就绪。
- 配置可校验与可恢复：`docker compose config -q`、`docker compose exec nginx nginx -t` 通过；重启 Nginx 容器后再次访问 `/health` 验证代理恢复。
- 构建与静态检查：`go build ./...`、`go vet ./...`、`make test-prometheus`（确认 Prometheus 采集配置未被改动）；`make test` 通过（需 MySQL/Redis）。

## Open Risks

- 开发/测试环境 Go 绑定 `0.0.0.0:8000` 且无防火墙，`:8000` 在宿主机自身网卡可达，属「软隔离」；生产硬边界依赖防火墙/安全组（文档约束，不落 compose）。本任务交付对「外部不可直连 `:8000`」的验证能力，但测试环境本身不构成生产安全证据；未落实防火墙/安全组并实测阻断的环境不得声明生产安全就绪。
- 若未来有上游可信 LB/CDN 在 Nginx 之前，`X-Forwarded-For $remote_addr` 的覆盖策略需改为 `real_ip` 模块 + 可信代理白名单，属后续任务。
- `STORAGE_LOCAL_ROOT` 若被覆盖为非仓库内绝对路径，与 Nginx 无耦合（因保留 Go 代理），无额外风险；此点在保留 Go 代理方案下不构成问题。

## Owner Decision Record

- 2026-10-09：Owner `ACCEPT` 推荐方案（Decision Authority 为 Owner）。
  - Q1 客户端 IP：Nginx 覆盖 `X-Forwarded-For` / `X-Real-IP`，不改 Go 业务代码，靠网络隔离防直连伪造。
  - Q2 网络隔离：生产经防火墙/安全组隔离 `:8000`；不容器化 Go、不引入独立 Metrics 端口；交付验收必须验证外部无法直连 `:8000`，未验证的环境不得声明生产安全就绪。
  - Q3 静态资源：保留 Go 静态服务，Nginx 反向代理，不引入宿主机目录挂载。
  - Q4 Metrics：Nginx 对外 `/metrics` 返回 404；Prometheus 继续直采不变。
  - 范围约束：不新增多实例 LB、不引入 HTTPS/TLS、不修改秒杀业务逻辑、保持 JWT 认证/HTTP 状态码/原有 API 行为不变。
