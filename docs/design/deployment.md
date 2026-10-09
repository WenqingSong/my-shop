# 部署与反向代理设计（Deployment / Nginx）

本文面向项目接手者，说明「部署拓扑、Nginx 反向代理入口、可信代理边界、网络隔离与 Prometheus 直采」的项目级长期事实。事实来源为 `nginx-reverse-proxy` 的 APPROVED Contract 与最终实现；与 `contract.md`（本任务决策过程）分工，本文只沉淀长期稳定事实。

## 1. 职责与边界

- Go 应用以**宿主机进程**运行，监听 `:8000`（`manifest/config/config.yaml` 的 `server.address`，环境变量 `SERVER_ADDRESS` 覆盖），由 `scripts/up.sh` 启动；**不容器化**。
- MySQL、Redis、Prometheus、Nginx 经 `docker-compose.yml` 部署。
- Nginx 是客户端访问 Go HTTP API 的**唯一对外入口**（宿主机 `:8080`，`NGINX_PORT` 覆盖），作为无状态反向代理，不做缓存、负载均衡、HTTPS 终结、业务限流。
- Prometheus **不经过 Nginx**，继续直接抓取 Go 的 `/metrics`。

边界（V1）：
- 不容器化 Go、不引入 Go 多实例与 Nginx 负载均衡。
- 不引入 HTTPS/TLS 证书管理。
- 不引入 Kubernetes / Ingress。
- 不做 Nginx 业务限流。
- 静态资源（轮播图 `/storage/banners`）仍由 Go 服务，Nginx 只做反向代理，不做宿主机目录挂载直供。

## 2. 部署拓扑

```text
客户端 ──HTTP──▶ Nginx（容器，:8080）──proxy_pass──▶ Go（宿主机进程，:8000）
                                                    │
Prometheus（容器）──直采 /metrics────────────────────┘
（经 host.docker.internal:host-gateway）
```

- Nginx 容器经 `extra_hosts: host.docker.internal:host-gateway` 访问宿主机 Go `:8000`（与 Prometheus 同一打通方式，需 Docker 20.10+）。
- Nginx 对外端口 `:8080` 与 Go `:8000` 隔离，两者默认不冲突。

## 3. Nginx 反向代理契约

- 全量转发现有 Go HTTP API（Method、请求体、状态码、响应体、`Authorization` Header 正确传递），`/health` 经 Nginx 返回 200 且响应体含 `status=ok`。
- 转发头（Nginx 作为唯一可信边缘，覆盖设置，见 §4）：
  - `X-Real-IP $remote_addr;`
  - `X-Forwarded-For $remote_addr;`（**覆盖**，丢弃客户端伪造值）
  - `X-Forwarded-Proto $scheme;`
  - `Host $host;`
- 超时：`proxy_connect_timeout`（建议 3-5s）、`proxy_read_timeout` / `proxy_send_timeout`（建议 60s）。
- `/metrics` 经 Nginx 显式 `return 404`（`location = /metrics` 置于 catch-all 之前）；不新增 `/healthz`、`/readyz`。
- Nginx 使用 `restart: unless-stopped` + 静态配置，重启后自动恢复代理。

## 4. 可信代理边界（客户端 IP 语义）

- **事实源**：GoFrame `GetClientIp()`（v2.10.3）依次读取 `X-Forwarded-For`（首个逗号值）→ `Proxy-Client-IP` → `WL-Proxy-Client-IP` → `HTTP_CLIENT_IP` → `HTTP_X_FORWARDED_FOR` → `X-Real-IP` → `RemoteAddr`，**盲信**这些头。
- **可信代理 = 仅 Nginx**：Nginx 是唯一边缘，上游无其它可信 LB/CDN，故 Nginx 用 `$remote_addr` **覆盖** `X-Forwarded-For` / `X-Real-IP`（丢弃客户端注入的伪造值），Go 侧 `GetClientIp()` 读到真实客户端 IP。
- **防伪造依赖双重边界**：① Nginx 覆盖转发头（丢弃客户端伪造值）；② 网络隔离（§5）阻止绕过 Nginx 直连 `:8000` 注入伪造头。
- 会话 `ip`（`SessionMeta.IP`）仅作设备区分元数据，**不参与鉴权**（`ValidateSession` 只校验 `sid/userID`）；IP 语义变化不影响登录/鉴权行为。
- 未来若在 Nginx 之前引入可信上游 LB/CDN，须改用 `real_ip` 模块 + 可信代理白名单，属后续任务。

## 5. 网络隔离模型

- 客户端入口 = Nginx `:8080`；Go `:8000` **不作为对外客户端入口**。
- Go 保持绑定 `0.0.0.0:8000`：`host-gateway` 将 `host.docker.internal` 解析为宿主机 Docker 网桥网关 IP（非 loopback），Nginx 与 Prometheus 容器必须能连到该非-loopback 地址，故 Go 不能只绑 `127.0.0.1`。
- **测试/开发（默认）**：无防火墙，`:8000` 在宿主机自身网卡可达，属「软隔离」——边界为「客户端一律走 Nginx `:8080`」。
- **生产**：硬边界 = 主机防火墙/云安全组：`8000` 与 `9090` 仅允许来自受信网络（Docker 网桥网段 + loopback），公网只暴露 `8080`（Nginx）。此规则由部署环境落实，不在 `docker-compose.yml` 内实现。
- **安全就绪判定**：只有实际验证「外部（非可信来源）无法直连 `:8000`」后，该环境才可声明生产安全就绪；未验证（含无防火墙的测试环境）一律标注为 dev-only，不得声明生产安全就绪。

## 6. 静态资源（轮播图）

- 轮播图仍由 Go `AddStaticPath("/storage/banners", <storage.local.root>/banners)` 服务（`internal/storage`），Nginx 全量反向代理（含 `/storage/banners/*`），URL 与返回内容不变。
- 不引入宿主机目录只读挂载（避免 `STORAGE_LOCAL_ROOT` 覆盖、SELinux、权限、空目录竞态等耦合）。
- 若未来静态体量增大，可由后续任务引入「共享卷 + Nginx 直供」。

## 7. 指标与 Prometheus

- `/metrics` 仅在 `flash_sale.metrics.enabled=true`（`FLASH_SALE_METRICS_ENABLED`）时由 Go 挂载为 `GET /metrics`，无鉴权，位于统一 JSON 响应包装之外。
- Prometheus 继续直采 `host.docker.internal:8000/metrics`，`scrape_interval=15s`，p95/p99 Recording Rules 不变（`prometheus/prometheus.yml`、`prometheus/rules/*`）。
- Nginx 对外入口默认不暴露 `/metrics`（返回 404）。

## 8. 业务不变量

- INV-001：经 Nginx 访问时 `GetClientIp()` 返回真实客户端 IP（`$remote_addr`），非 Nginx 容器 IP。
- INV-002：客户端携带伪造 `X-Forwarded-For`/`X-Real-IP` 经 Nginx 访问时，最终记录 IP 不被伪造值冒充。
- INV-003：经 Nginx 访问现有 API 的 Method/请求体/状态码/响应体/`Authorization` 与直连 Go 一致；登录、JWT 鉴权、秒杀下单行为不变。
- INV-004：Nginx 对外 `/metrics` 返回 404；Prometheus 直采 `:8000/metrics` 且 `scrape_interval=15s` 不变。
- INV-005：`/storage/banners/<file>` 的 URL 与返回内容与现有 Go 静态服务一致。
- INV-006：客户端入口为 Nginx `:8080`；生产环境外部不可直连 Go `:8000`（防火墙/安全组），且该隔离经实际验证。

## 9. 失败语义与可恢复性

- Nginx 为无状态代理，不缓存，无多存储一致性问题；事实源为 Go 服务与 MySQL/Redis。
- `GET /health` 经 Nginx 返回 200 + `status=ok` 代表 Nginx→Go 代理链路通且 Go 存活。
- Go 不可用时 Nginx 返回 502/504，无数据写入/破坏；超时返回 504。
- Nginx 重启后自动恢复代理，无需手工步骤。

## 10. 验证入口

- `docker compose config -q`、`docker compose exec nginx nginx -t`（配置可解析）。
- `make test-nginx`：Nginx 配置与转发头/`/metrics` 拒绝等静态断言（`scripts/test-nginx.sh`）。
- `make test-prometheus`：确认 Prometheus 直采配置未被改动。
- `make health` / `make status`：纳入 Nginx 健康/状态。
- 真实链路 Smoke Test：`/health`、`/login`、`/refresh`、`/flash-sales/:id/orders` 经 Nginx 与直连 Go 对比；伪造头防伪验证；`/metrics` 经 Nginx 404；`/storage/banners/*` 内容一致。
- 网络隔离验证：实际验证非可信来源直连 `:8000` 被隔离（生产环境）；无防火墙环境标注 dev-only。

## 11. 已知留白

- 生产硬边界（防火墙/安全组）由部署环境落实，本仓库只提供验证方法与文档约束。
- 若未来引入上游可信 LB/CDN，需切换 `real_ip` 模块 + 可信代理白名单。
- Nginx 直供静态资源（共享卷）属后续任务。
