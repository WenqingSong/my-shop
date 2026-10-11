# Task: Nginx Reverse Proxy V1 — SurgeCart 统一 HTTP 入口

## Goal

为 SurgeCart 后端接入 Nginx 反向代理，使其成为面向外部的唯一 HTTP 入口：对外统一承载转发、连接复用、请求超时与请求体大小限制，对内保护 Go 应用不被绕过直连，并在不改变现有 API 语义、错误码、响应体及秒杀限流/排队/熔断机制的前提下，落实代理安全（客户端 IP 防伪造）、监控隔离与请求日志关联。

## Scope

- 新增 Nginx 配置与 Compose 服务，接入现有 `docker-compose.yml`（当前含 mysql / redis / app / prometheus），Nginx 作为外部唯一 HTTP 入口。
- 配置反向代理 upstream、连接复用（keepalive）、代理/连接/发送/读取超时，以及请求体大小限制。
- 代理安全：正确处理 `X-Forwarded-For`、`X-Real-IP` 等请求头，防止客户端伪造 IP 污染后端；明确 Nginx 与 Go 应用之间的代理信任边界，以及容器网络隔离策略（后端 app 不再对外暴露端口）。
- 配置必要的安全响应头（如 `X-Content-Type-Options`、`X-Frame-Options` 等）。
- 业务兼容：保证 IAM、普通订单、秒杀、后台管理等现有 API 经 Nginx 正常访问，HTTP 状态码、业务错误码、响应体语义不变；不引入对非幂等请求的自动重试导致重复业务操作；不与现有 Redis Lua 秒杀限流/排队/熔断机制冲突。
- 监控与日志：限制公共入口对 `/metrics` 的访问，保持 Prometheus 内部抓取正常；明确 `/health` 的访问策略；配置结构化访问日志、Request ID 与 upstream 响应耗时记录，并建立 Nginx 访问日志与 Go 应用日志的关联方式。
- 部署与配置管理：复用现有 Dockerfile、Compose、环境变量、健康检查与数据库迁移启动机制，保证配置可维护、部署可重复；不引入硬编码凭据；兼顾未来 Kubernetes Ingress 迁移，避免重复代理层。
- 测试与验收：验证 Compose 冷启动/重启/服务恢复、全部关键 API 经 Nginx 访问、客户端 IP 防伪造、内部监控隔离、直连绕过防护、代理异常/超时/上游不可用等故障场景，以及秒杀请求与压力测试接入前后的业务正确性与性能影响；输出实际测试证据。
- 更新项目级长期设计 `docs/design/deployment.md`，纳入 Nginx 反向代理模型（统一入口、信任边界、网络隔离、日志关联、监控访问策略）。

## Out of Scope

- 不重复开发现有 Go 后端容器化能力（`backend-container-v1` 已交付并合并）。
- 不在本任务实施 Kubernetes / Ingress 部署（仅保持后续迁移兼容，不重复搭建代理层）。
- 不引入 Nginx Lua / njs 业务逻辑。
- 不新增无关电商业务功能、数据库 migration、错误码域。
- 不实现 HTTPS / TLS 终止（如有需要另行任务）。
- 不以增加配置项数量作为完成标准。

## Milestone

Milestone: nginx-reverse-proxy-v1

## Design Impact

Design Impact: UPDATE
Design Artifact: docs/design/deployment.md

## Acceptance Criteria

- [ ] AC-001：给定 `docker compose up`，Nginx 容器随服务编排健康启动，作为外部唯一 HTTP 入口可达；后端 `app` 服务不再直接映射宿主机端口，外部无法绕过 Nginx 直连 Go API。
- [ ] AC-002：给定经 Nginx 访问的关键 API（IAM 登录/刷新、普通订单、秒杀下单、后台管理），HTTP 状态码、业务错误码与响应体语义与直连 `app:8000` 完全一致，无回归。
- [ ] AC-003：给定客户端伪造 `X-Forwarded-For` / `X-Real-IP` 请求头经 Nginx 访问，后端 IAM 会话记录到的客户端 IP 为 Nginx 连接的真实对端 IP，而非伪造值；伪造的转发头不被透传给后端信任。
- [ ] AC-004：给定 Nginx 作为反向代理，对非幂等请求（POST/PUT/DELETE，含秒杀下单、下单、支付等）在上游连接失败或超时时不自动重试，不产生重复业务写入；对幂等请求（GET/HEAD）的重试行为有明确且不破坏业务的边界。
- [ ] AC-005：给定公共入口访问 `/metrics`，默认被拒绝或不可达，Prometheus 仍能经内部网络正常抓取 `app:8000/metrics` 并保持指标语义不变；`/health` 的对外访问策略明确且与探针语义一致。
- [ ] AC-006：给定一次经 Nginx 的请求，访问日志为结构化格式，包含 Request ID 与 upstream 响应耗时；同一 Request ID 能关联到 Go 应用侧对应请求日志。
- [ ] AC-007：给定上游不可用/超时场景，Nginx 返回明确且不泄漏内部细节的错误响应（如 502/504），且 Go 应用的错误语义与状态码未被 Nginx 改写。
- [ ] AC-008：给定请求体超过配置上限，Nginx 返回 413 且不将请求转发到后端；后端自身校验语义保持不变。
- [ ] AC-009：给定 Compose 冷启动、容器重启与 `down`+`up` 重建，服务恢复正常，秒杀限流/排队/熔断机制（Redis Lua 限流、排队软上限、进程内熔断）在代理接入前后行为一致，无因代理引入的冲突。
- [ ] AC-010：给定秒杀压测（并发下单请求经 Nginx），业务正确性（一人一单、幂等、库存一致）与接入前一致，并给出代理接入前后的性能对比证据。
- [ ] AC-011：`docs/design/deployment.md` 与最终实现一致，纳入 Nginx 反向代理模型（统一入口、信任边界、网络隔离、日志关联、监控访问策略），且不宣称 Kubernetes / Ingress 已部署。
- [ ] AC-012：`docker compose config` 校验通过；`go build ./...`、`go vet ./...`、`go test -p 1 ./...` 通过（若本次不改动 Go 代码则说明无需重跑并标注 `NOT_VERIFIED` 原因）；Nginx 配置语法校验通过。

## Relevant Context

已核实事实：

- 当前 `docker-compose.yml` 含 4 个服务：`mysql`（3306）、`redis`（6379）、`app`（`${APP_PORT:-8000}:8000`）、`prometheus`（`${PROMETHEUS_PORT:-9090}:9090`）。`app` 当前直接映射宿主机端口 8000，是可被外部直连的现状。
- 后端容器化 `backend-container-v1` 已交付并合并（delivery PASS）；其长期设计 `docs/design/deployment.md` 已落地，其中 §10 明确把「Nginx 反向代理、Kubernetes/Ingress、HTTPS/TLS、多实例负载均衡」列为 Deferred 后续任务，§6 指出 `app:8000` 是供「后续 Nginx V1 使用」的稳定 Compose 内部入口。
- 路由入口在 `internal/cmd/cmd.go`：全局 `middleware.Response` 统一包装响应；`/health` 是 liveness（返回 `{"code":0,...data:{status:"ok",time:...}}`），经 `Response` 中间件包装；`/metrics` 在 `registerMetricsRoute` 中独立挂载于 `Response` 之外，直接输出 Prometheus 文本，仅当 `flash_sale.metrics.enabled=true`（`FLASH_SALE_METRICS_ENABLED`）时注册。
- 现有中间件仅 `auth.go`（Auth / AdminAuth / RequirePermission 等）、`principal.go`、`response.go`；无日志中间件、无 CORS、无 Request ID 中间件；日志用 GoFrame `glog`，默认文本格式，无 `request_id` 结构化字段。
- 秒杀限流/排队/熔断**不依赖客户端 IP**：限流 key 仅 `userID` / `activityID`（`flashsale:rl:user:`、`flashsale:rl:activity:`）；排队软上限 key 仅 `activityID:skuID`；熔断为进程内状态机无 key；一人一单/幂等 key 仅 `userID` / `idempotencyKey`。即代理改写转发头不会破坏秒杀限流/排队/熔断语义。
- 全仓无任何文件显式读取 `X-Forwarded-For` / `X-Real-IP`；但 IAM 登录/刷新（`internal/controller/iam/iam.go`）经 GoFrame `r.GetClientIp()` 读取客户端 IP 作为会话设备元数据，因此转发头伪造可能污染 IAM 会话 IP 元数据，需由代理正确重写。
- 服务默认监听 `:8000`（`0.0.0.0`，所有网卡），Compose 通过 `${APP_PORT:-8000}:8000` 映射到宿主机；Prometheus 抓取目标为 Compose DNS 名 `app:8000`。
- 当前 Git 状态：分支 `develop`，HEAD `554cd21a0d87386f8e9348e5780b98a3330f4470`，working tree clean，无 staged / unstaged / untracked 变更。

Assumption：

- Nginx 使用官方稳定镜像，配置以文件 COPY 进镜像或 Compose bind mount 的方式提供（具体以 Analyst 分析为准，考虑 rootless/嵌套容器下 bind mount 单文件退化问题）。
- 未来 Kubernetes Ingress 迁移时，Nginx 的纯反向代理职责（不含 Lua 业务逻辑）可被 Ingress 取代，本次不搭建可复用的二次代理层。

OPEN QUESTION：见「Analyst Questions」，均属影响安全/运维结果的设计选择，交由 Analyst 分析并由 Owner 决定，不在本任务预设方案。

## Verification

- AC-001 → `docker compose config` + `up` 后 `docker compose ps`，确认 Nginx 服务 healthy、对外入口可达、`app` 无宿主机端口映射；从宿主机尝试直连 `app` 原端口确认不可达。
- AC-002 → 以真实 MySQL/Redis 环境，逐项对比「经 Nginx」与「直连 app:8000」的 IAM 登录/刷新、普通订单、秒杀下单、后台管理接口的状态码/错误码/响应体，逐字段一致。
- AC-003 → 用 curl 伪造 `X-Forwarded-For: 1.2.3.4` 经 Nginx 登录，核对 IAM 会话元数据 IP 为 Nginx 真实对端 IP 而非伪造值。
- AC-004 → 构造上游连接失败/超时，观察非幂等请求（如 POST 下单/秒杀）无自动重试与重复写入（核对数据库/Redis 无重复记录）；GET/HEAD 重试行为符合设计。
- AC-005 → 公共入口访问 `/metrics` 确认默认拒绝/不可达；`FLASH_SALE_METRICS_ENABLED=true` 时 Prometheus Targets 页 `app` 为 UP、指标 `flashsale_*` 正常；核对 `/health` 对外策略。
- AC-006 → 请求带/不带 `X-Request-ID` 均返回稳定 Request ID，Nginx 访问日志与 Go 侧日志能用同一 ID 关联，且含 upstream 耗时。
- AC-007 → 停掉 app 容器后经 Nginx 访问，返回明确 502/504 且不含内部路径/堆栈/凭据。
- AC-008 → 发送超限请求体，确认返回 413 且后端未收到该请求（后端日志无记录）。
- AC-009 → 冷启动、重启、`down`+`up` 后，跑秒杀下单验证限流/排队/熔断行为与接入前一致。
- AC-010 → 用现有压测脚本/工具对秒杀下单接口做代理接入前后压测，比对业务正确性（一人一单、幂等、库存）与 p95/p99 延迟，输出证据。
- AC-011 → 人工核对 `docs/design/deployment.md` 与最终实现一致，无 K8s/Ingress 提前宣称。
- AC-012 → `docker compose config` 退出码 0；`nginx -t` 校验通过；`go build ./...` / `go vet ./...` / `go test -p 1 ./...`（本次不改 Go 代码则标注 `NOT_VERIFIED` 并说明原因）。

## Complexity

COMPLEX

原因：本任务涉及反向代理信任边界、客户端 IP 防伪造、容器网络隔离、非幂等请求重试策略、监控隔离、日志关联等多个会产生不同安全、可靠性与运维结果的设计选择；同时会修改既有的项目级长期部署模型与安全边界（Design Impact = UPDATE）。按 Owner 约束，关键设计决策应在 Analyst 阶段明确并由 Owner 决定，不预设方案。

## Analyst Questions

- 信任边界与 IP 防伪造：Nginx 如何重写 `X-Forwarded-For` / `X-Real-IP`（`proxy_set_header` + `real_ip` 模块 or 手动构造），后端 `r.GetClientIp()` 的取值链路如何保证只信任 Nginx，且伪造转发头无法污染 IAM 会话元数据。
- 网络隔离：Nginx 作为唯一入口后，`app`、`prometheus`、`mysql`、`redis` 的端口映射如何收敛（哪些仅内网、哪些对外）；仅用 Compose 内部网络能否满足「防外部绕过直连」，还是需要额外配置。
- 重试边界：`proxy_next_upstream` 的开启条件与 `proxy_next_upstream_tries`，如何严格保证 POST/PUT/DELETE（秒杀下单、下单、支付等）不自动重试，只对幂等方法在何种失败下重试。
- 监控与探针访问策略：`/metrics` 的公共入口隔离方式（Nginx deny + 仅 Prometheus 内网抓取），`/health` 是否对外公开、还是仅限 Compose 内部健康检查。
- 日志关联：Request ID 由 Nginx 生成（`$request_id`）还是 Go 侧生成；如何在不改动业务代码的前提下让 Go 应用日志关联同一 Request ID（如透传 `X-Request-ID` 并接入日志字段），以及结构化访问日志字段清单。
- 配置管理与镜像：Nginx 配置以镜像内置还是 volume 挂载（结合 rootless/嵌套容器 bind mount 单文件退化约束）；安全响应头清单与超时/请求体大小的合理默认值。
- 未来 K8s 迁移兼容：如何避免在本次搭建会被 Ingress 取代的二次代理层，同时保证当前 Compose 部署的可用性。

## Review Baseline

- Base commit：`554cd21a0d87386f8e9348e5780b98a3330f4470`（分支 `develop`）。
- 任务开始时已有修改：无（working tree clean，无 staged / unstaged / untracked 变更）。
- 重叠修改的区分方式：本任务主要改动集中在 `docker-compose.yml`、新增 Nginx 配置（如 `nginx/` 目录或等价文件）、`docs/design/deployment.md`，以及必要的 `scripts/*` / `.env.example` 接入；不触碰 `internal/` 业务代码（除非日志关联需要最小接入，须经 Analyst 明确边界）。

## Initial Route

交 Analyst（COMPLEX）。
