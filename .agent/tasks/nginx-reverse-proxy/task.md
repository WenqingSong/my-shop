# Task: Nginx 反向代理接入

## Goal

在现有 my-shop 部署架构中引入 Nginx，作为 Go HTTP 服务的反向代理入口。客户端通过 Nginx 访问现有 API，同时保持认证（JWT 登录/刷新/鉴权）、秒杀下单、静态资源、客户端 IP 语义与 Prometheus 直采功能不变；明确可信代理边界与测试/生产入口的网络隔离，防止绕过 Nginx 直连公开的 Go 端口或伪造客户端 IP。

## Scope

- Nginx 部署与基础配置：`docker-compose.yml` 新增 Nginx 服务；新增版本化 Nginx 配置文件；Nginx 容器经 `host-gateway` 访问宿主机 Go 应用 `:8000`；测试环境默认经宿主机 `8080` 访问 Nginx（与 Go `8000` 隔离）；配置合理的连接/读取/发送超时；提供 `nginx -t` 配置检查与健康验证方式。
- HTTP 反向代理：代理现有 Go HTTP API；保持 Method、请求体、状态码、响应体、`Authorization` Header 正确传递；`/health` 经 Nginx 可正常访问；不新增 `/healthz`、`/readyz` 接口；验证登录、JWT 鉴权与秒杀接口行为不变。
- 真实客户端 IP：正确设置 `X-Real-IP`、`X-Forwarded-For`、`X-Forwarded-Proto`；核实 GoFrame `GetClientIp()` 在反向代理后的行为；明确可信代理边界，防止客户端伪造转发头冒充其他用户 IP；验证登录与刷新会话记录的客户端 IP 正确。
- 静态资源：核实 `/storage/banners` 的真实文件目录与存储方式；在具备安全、可靠的只读文件映射条件时由 Nginx 直接提供静态资源；若宿主机与容器间文件路径无法安全共享，则保留原有 Go 静态资源代理，不引入错误的文件映射；保持原有静态资源 URL 与访问行为兼容；不扩展前端构建、CDN 或对象存储能力。
- Prometheus 兼容：Prometheus 继续直接抓取 Go 应用 `/metrics`（不经 Nginx 负载均衡）；保持现有 `scrape_interval=15s`；不修改 Histogram、Counter、p95/p99 Recording Rules；Nginx 对外入口默认不暴露 `/metrics`；验证 Prometheus Target 仍为 UP、p95/p99 查询正常。
- 集成测试与交付验证：`docker compose config`、`nginx -t`、Nginx → Go HTTP 代理链路、JWT 登录/鉴权/秒杀下单 Smoke Test、真实 IP/防伪造请求头/静态资源访问、Nginx 重启后恢复代理、Prometheus 采集不受影响；明确测试入口与生产入口的网络隔离要求；更新必要部署文档。
- Design Impact 沉淀：新增 `docs/design/deployment.md`，记录部署拓扑（Nginx 反向代理入口、宿主 Go 应用、Prometheus 直采）、可信代理边界与网络隔离要求（见 Design Impact 节）。

## Out of Scope

- Go 应用容器化。
- Go 多实例与 Nginx 负载均衡。
- HTTPS/TLS 证书管理。
- Kubernetes / Ingress。
- Nginx 业务限流。
- 修改 Redis Lua Gate、订单事务、消费者。
- 修改 Prometheus 指标定义与 Recording Rules。
- 新增数据库迁移或错误码。
- 新增 `/healthz`、`/readyz` 探针接口。
- 扩展前端构建、CDN 或对象存储能力。

## Milestone

Milestone: Nginx 反向代理接入交付验收（反向代理 + 真实客户端 IP + 静态资源 + Prometheus 兼容 + 网络隔离）

## Design Impact

Design Impact: NEW

Design Artifact: docs/design/deployment.md

新增项目级长期事实：引入 Nginx 反向代理入口（新的 Deployment 模型），新增「可信代理边界」这一安全边界（客户端 IP 由可信 Nginx 设置转发头、拒绝客户端伪造），以及「Nginx 对外入口 vs Go :8000 直连 vs Prometheus 直采」的网络隔离拓扑。上述长期事实沉淀到 `docs/design/deployment.md`，并纳入 Scope / AC-008 / AC-009。

## Acceptance Criteria

- [ ] AC-001（反向代理链路）：给定已部署的 Nginx 入口（宿主机 `8080`）与运行中的 Go 应用（`:8000`），当客户端经 Nginx 访问现有 API（`GET /health`、`POST /login`、`POST /refresh`、`POST /flash-sales/:id/orders` 等），则请求 Method、请求体、响应状态码、响应体与 `Authorization` Header 与直连 Go 应用保持一致；`/health` 经 Nginx 返回 200 且响应体包含 `status=ok`。
- [ ] AC-002（不新增探针接口）：Nginx 入口不暴露 `/healthz`、`/readyz`，Go 应用也未新增这两个接口（访问返回 404 或不存在）。
- [ ] AC-003（真实客户端 IP）：当客户端经 Nginx 访问 `/login` 与 `/refresh`，则 GoFrame `GetClientIp()` 返回真实客户端 IP（而非 Nginx 容器 IP），登录与刷新会话记录的客户端 IP 与真实来源一致。
- [ ] AC-004（防伪造与可信边界）：当客户端在请求中携带伪造的 `X-Forwarded-For` / `X-Real-IP` 头经 Nginx 访问时，服务端最终记录的 IP 不被伪造值冒充；Nginx 正确设置 `X-Real-IP`、`X-Forwarded-For`、`X-Forwarded-Proto`，可信代理边界明确且生效。
- [ ] AC-005（静态资源）：`/storage/banners/<file>` 的访问行为与原有 Go 静态服务兼容（URL 与返回内容不变）；若采用 Nginx 直供，则文件映射为安全只读且与 `<STORAGE_LOCAL_ROOT>/banners` 一致；若保留 Go 代理，则行为不变且不引入错误的文件映射。
- [ ] AC-006（Prometheus 兼容）：Prometheus 仍直接抓取 `host.docker.internal:8000/metrics`，`scrape_interval` 保持 `15s`，Target 状态 UP，p95/p99 Recording Rules 查询正常；Nginx 对外入口默认不暴露 `/metrics`（访问被拒或 404）。
- [ ] AC-007（配置可校验与可恢复）：`docker compose config` 与 `nginx -t` 校验通过；Nginx 容器重启后能恢复对 Go API 的代理，无需额外手工步骤。
- [ ] AC-008（网络隔离）：明确测试入口与生产入口对 Go `:8000` 的暴露边界，避免绕过 Nginx 直接访问公开的 Go 端口；隔离方案在 Contract 中确认并写入部署文档。
- [ ] AC-009（部署文档与设计沉淀）：更新部署文档（`README.md` / `scripts/*` / `Makefile` 等）记录 Nginx 入口、健康验证、配置检查方式与网络隔离要求；新增 `docs/design/deployment.md` 沉淀部署拓扑、可信代理边界与 Prometheus 直采关系（Design Impact = NEW）。
- [ ] AC-010（业务不变量不变）：登录、JWT 鉴权、秒杀下单 Smoke Test 通过；认证、秒杀、静态资源与客户端 IP 语义保持不变；不修改 Redis Lua Gate、订单事务、消费者、Prometheus 指标定义与 Recording Rules。

## Relevant Context

- 已核实事实：
  - Go 应用以宿主机进程运行，监听 `:8000`（`manifest/config/config.yaml` 的 `server.address`，环境变量 `SERVER_ADDRESS` 可覆盖），由 `scripts/up.sh`（`nohup bin/my-shop serve &`）启动；MySQL、Redis、Prometheus 通过 `docker-compose.yml` 部署。
  - Prometheus 容器配置 `extra_hosts: host.docker.internal:host-gateway`，抓取 `host.docker.internal:8000/metrics`，`scrape_interval: 15s`（`prometheus/prometheus.yml`）。
  - `/health` 为 `GET /health`（`api/health/v1` 的 g.Meta path），经 `middleware.Response` 包装返回 `{"code":0,"message":"OK","data":{"status":"ok","time":...}}`；当前无 `/healthz`、`/readyz`。
  - `/metrics` 仅在 `flash_sale.metrics.enabled=true`（`FLASH_SALE_METRICS_ENABLED`）时挂载为 `GET /metrics`（`internal/cmd/cmd.go` 的 `registerMetricsRoute`），无鉴权，位于统一 JSON 响应包装之外。
  - 静态资源：`internal/storage` 的 LocalStorage，`BannerURLPrefix="/storage/banners"`，`BannerDir()=<storage.local.root>/banners`（默认 `./storage/banners`，环境变量 `STORAGE_LOCAL_ROOT` 覆盖）；`serve` 启动时 `configureBannerStorage` seed 3 张占位图并 `AddStaticPath` 映射 `/storage/banners`。
  - GoFrame `v2.10.3` 的 `GetClientIp()` 依次读取 `X-Forwarded-For`（首个逗号值）→ `Proxy-Client-IP` → `WL-Proxy-Client-IP` → `HTTP_CLIENT_IP` → `HTTP_X_FORWARDED_FOR` → `X-Real-IP` → `RemoteAddr`；源码注释明确警告「该 IP 可能被客户端 Header 篡改」。
  - 会话 IP：`internal/controller/iam/iam.go` 的 `Login`/`Refresh` 用 `r.GetClientIp()` 作为设备元数据写入会话；`middleware.Auth` 的 `ValidateSession` 只校验 sid/userID，不校验 IP（IP 当前不是鉴权门禁，但会被记录用于区分设备）。
  - 当前无 `docs/design/deployment.md`；部署事实散落在 `README.md`、`docker-compose.yml`、`prometheus/prometheus.yml`、`Makefile`、`scripts/*`。
  - 验证入口：`make test-prometheus`（`scripts/test-prometheus.sh`，含 `docker compose config` 与 promtool 校验）、`make health`、`make status`；当前无 Nginx 校验脚本。
- Assumption：
  - Nginx 官方镜像（如 `nginx:stable-alpine` 或固定 `nginx:1.x`）可作为反向代理容器，并支持经 `host-gateway` 访问宿主机（需 Docker 20.10+，与现有 Prometheus 用法一致）。
  - Nginx 对外入口端口 `8080` 可配置（如 `NGINX_PORT`），默认值与 Go `8000` 隔离。
- OPEN QUESTION：无阻塞性未决项（关键设计问题转 Analyst Questions）。

## Verification

- AC-001 → 真实环境启动 Nginx 与 Go 应用后，分别对 Nginx（`8080`）与 Go（`8000`）发起 `/health`、`/login`、`/refresh`、`/flash-sales/:id/orders` 请求，逐项对比 Method/请求体/状态码/响应体/`Authorization` Header 传递结果；预期二者一致且 `/health` 返回 200 + `status=ok`。需要 MySQL、Redis 就绪（登录/秒杀链路）。
- AC-002 → 经 Nginx 访问 `/healthz`、`/readyz` 预期 404；并核对 Go 路由注册无这两个接口。
- AC-003 → 经 Nginx 用固定来源 IP 触发 `/login`、`/refresh`，通过会话记录/日志或 `/sessions` 核实记录的 IP 等于真实客户端 IP（非容器网段 IP）。
- AC-004 → 在请求头手动注入伪造的 `X-Forwarded-For`/`X-Real-IP`，经 Nginx 访问 `/login` 后核实服务端记录 IP 为真实来源而非伪造值；并抓取后端收到的转发头验证 `X-Real-IP`/`X-Forwarded-For`/`X-Forwarded-Proto` 正确。
- AC-005 → 经 Nginx 与直连 Go 分别访问 `/storage/banners/banner-1.png`，对比状态码与内容；核对文件映射路径与 `<STORAGE_LOCAL_ROOT>/banners` 一致（若 Nginx 直供，还需验证只读且无目录逃逸）。
- AC-006 → 以 `FLASH_SALE_METRICS_ENABLED=true` 运行服务，经 `prometheus` API 验证 Target UP；经至少一个 `evaluation_interval` 后查询 p95/p99 Recording Rule 产物非空；经 Nginx 访问 `/metrics` 预期被拒/404。
- AC-007 → 运行 `docker compose config -q` 与 `nginx -t`（`docker compose exec nginx nginx -t`）；重启 Nginx 容器后再次访问 `/health` 验证代理恢复。
- AC-008 → 对照 Contract 确认测试/生产入口对 Go `:8000` 的暴露边界（如防火墙规则或监听地址），并验证从「非可信来源」直连 `:8000` 被隔离、Nginx `8080` 可用。
- AC-009 → 核对 `README.md`/`scripts/*`/`Makefile` 更新，及 `docs/design/deployment.md` 内容与实现一致（Cleaner 做 Design 一致性核对的依据之一）。
- AC-010 → 运行现有测试与 Smoke Test（登录 → 鉴权接口 → 秒杀下单），预期行为不变；`make test` / `go test -p 1 ./...` 通过（需 MySQL/Redis）。
- 构建与静态检查：`go build ./...`、`go vet ./...`；Nginx 配置静态校验与 `make test-prometheus`（确保 Prometheus 采集配置未被改动）。

## Complexity

COMPLEX

原因：本任务涉及「重要安全边界」与「多个现实方案会产生不同运维/可靠性结果」：
1. 可信代理边界：GoFrame `GetClientIp()` 当前信任 `X-Forwarded-For`，引入 Nginx 后如何在「覆盖/追加转发头」与「防止客户端伪造」之间取舍，存在不同安全语义，需 Analyst 定方案。
2. 网络隔离 vs Prometheus 直采冲突：要求「避免绕过 Nginx 直连 `:8000`」同时「Prometheus 继续直采 `host.docker.internal:8000/metrics`」，二者在监听地址/防火墙层面存在冲突，需明确可行的测试/生产隔离方案。
3. 静态资源：Nginx 只读挂载宿主 `/storage/banners` vs 保留 Go 静态代理，涉及路径共享安全性与一致性，需核实后决策。

## Analyst Questions

1. 可信代理边界：Nginx 应如何设置转发头（覆盖 vs 追加 `X-Forwarded-For`），结合 GoFrame `GetClientIp()` 当前实现，Go 侧是否需要/如何区分「来自可信代理」与「直连伪造」，给出防伪造的具体边界与验证方式。
2. 网络隔离与 Prometheus 直采：如何在「避免绕过 Nginx 直连 Go `:8000`」与「Prometheus 继续直采 `host.docker.internal:8000/metrics`」之间取得一致（防火墙 / 监听地址 / 独立 metrics 端口等），给出可执行的测试环境与生产环境隔离方案。
3. 静态资源：核实 `/storage/banners` 真实目录（`<STORAGE_LOCAL_ROOT>/banners`，默认 `./storage/banners`）能否被 Nginx 容器安全只读挂载；决定「Nginx 直供」还是「保留 Go 静态代理」，并明确占位图 seed 时序与路径一致性。
4. Nginx 不暴露 `/metrics` 的配置方式：proxy 全部路由但对 `/metrics` 单独拒绝/404，或仅转发白名单路径，给出默认安全配置。

## Review Baseline

- Base commit：`aa272f485c6ea53131464361aa117bcb0d555667`（`Merge branch 'feat/p95-p99-query' into develop`）。
- 分支：`feat/nginx`，local HEAD == `origin/feat/nginx` == `origin/develop`，working tree clean。
- 任务开始时已有修改：无。
- 重叠修改的区分方式：本任务仅新增 Nginx 配置/脚本/文档，以及对 `docker-compose.yml`、`Makefile`、`scripts/*`、`README.md` 的必要增改；`prometheus/prometheus.yml`、`prometheus/rules/*` 与 Go 业务代码原则上不改动（若 Analyst 结论要求对 Go 侧可信代理做极小调整，须在 Contract 中明确），基线后所有变更即本任务变更。

## Initial Route

交 Analyst（COMPLEX）
