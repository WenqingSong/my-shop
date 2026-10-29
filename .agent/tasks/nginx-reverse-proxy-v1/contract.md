# Technical Contract

## Decision Status
APPROVED

## Problem

为 SurgeCart 后端接入 Nginx 反向代理，使其成为面向外部的唯一 HTTP 入口。核心难点不在「加一层代理」，而在几处会产生不同安全/可靠性/运维结果的设计选择：客户端 IP 防伪造（后端 `r.GetClientIp()` 的取值链路）、容器网络隔离（app 不再对外暴露端口）、非幂等请求的重试边界（避免秒杀/下单重复写入）、`/metrics` 与 `/health` 的访问策略、跨 Nginx↔Go 的 Request ID 日志关联，以及上游 DNS 在容器 IP 变化后的解析与恢复。

## Verified Current Behavior

- VERIFIED：GoFrame v2.10.3 `ghttp.Request.GetClientIp()`（`net/ghttp/ghttp_request.go:188`）取值顺序为：`X-Forwarded-For`（取 `ipArray[0]`，即逗号列表**第一项**）→ `Proxy-Client-IP` → `WL-Proxy-Client-IP` → `HTTP_CLIENT_IP` → `HTTP_X_FORWARDED_FOR` → `X-Real-IP` → `GetRemoteIp()`。即客户端直接伪造 `X-Forwarded-For` 会被无条件信任。
- VERIFIED：IAM `Login`/`Refresh`（`internal/controller/iam/iam.go`）经 `r.GetClientIp()` 将 IP 写入会话设备元数据，故转发头伪造可污染 IAM 会话 IP。全仓仅此两处读取客户端 IP。
- VERIFIED：`docker-compose.yml` 当前 4 个服务均映射宿主机端口：`app` `${APP_PORT:-8000}:8000`、`mysql` `${MYSQL_PORT:-3306}:3306`、`redis` `${REDIS_PORT:-6379}:6379`、`prometheus` `${PROMETHEUS_PORT:-9090}:9090`。
- VERIFIED：`/health` 是 liveness，经 `middleware.Response` 包装返回 `{code:0,message:"OK",data:{status:"ok",time}}`；app 自身 healthcheck 用容器内 `wget http://127.0.0.1:8000/health`。
- VERIFIED：`/metrics` 在 `middleware.Response` 之外独立挂载，仅当 `flash_sale.metrics.enabled=true`（`FLASH_SALE_METRICS_ENABLED`）时注册；Prometheus 抓取目标为 Compose DNS 名 `app:8000`（不经 Nginx、不经宿主机）。
- VERIFIED：Go 侧**无 Request ID 中间件、无访问日志**（GoFrame `AccessLogEnabled` 默认 false，`config.yaml` 未设 `LogPath`）；日志为 `glog` 默认文本，无 `request_id` 字段。仅中间件/扫描器有少量 `glog.Warningf/Errorf`。
- VERIFIED：秒杀限流/排队/熔断/一人一单/幂等的 key 均**不依赖客户端 IP**（`userID`/`activityID`/`skuID`/`idempotencyKey`），转发头改写不会破坏秒杀语义。
- VERIFIED：Prometheus 抓取配置与 rules 经 `prometheus/Dockerfile` COPY 内置进镜像（规避 rootless/嵌套容器下 bind mount 单文件退化为目录的问题）；Nginx 配置应采用相同镜像内置方式。
- VERIFIED：压测脚本 `scripts/flashsale-loadtest/run.sh` 的 `verify()` 从宿主机 `127.0.0.1:3306`/`6379` 连 MySQL/Redis 核对业务事实；故 dev 环境 mysql/redis 的宿主机回环映射需保留，否则 AC-010 压测核对失效。
- VERIFIED：`docker compose config` 当前可解析；仓库根 `docs/design/deployment.md` §10 明确将「Nginx 反向代理 / Kubernetes / Ingress / HTTPS / 多实例」列为 Deferred，§6 指明 `app:8000` 是「供后续 Nginx V1 使用」的稳定 Compose 内部入口。
- VERIFIED：Git 基线 `develop` @ `554cd21a0d87386f8e9348e5780b98a3330f4470`；任务 `nginx-reverse-proxy-v1` 无历史 Contract（旧的 `nginx-reverse-proxy` 任务位于另一 feature 分支，与本任务无关）。
- VERIFIED（INFERENCE）：Docker Compose 内 `restart: unless-stopped` 的 app 容器在重启/重建后可能获得新 IP；若 Nginx 在启动时一次性解析 `app` 主机名，IP 变化后会 502，需运行时 DNS 再解析机制（详见 Selected Design R2）。
- UNKNOWN：CNB 运行环境在 Nginx 之前是否还存在额外代理层（影响 `$remote_addr` 是否为真实客户端 IP）；目标环境 `8080` 端口可用性；`client_max_body_size` / 各超时的精确默认值需在实现阶段实测确认。

## Recommendation

RECOMMENDATION：采用「Nginx 单跳信任边界 + 覆盖式转发头重写 + `proxy_next_upstream off` + 上游运行时 DNS 再解析 + 最小 Go 中间件做日志关联」的方案，已由 Owner 确认（见 Selected Design 与 Owner Decision Record）。

## Selected Design

以下为 Owner 确认的最终设计（Coder 实现约束）：

**D1 对外端口**：Nginx 作为唯一对外 HTTP 入口，宿主机默认端口 `8080`（`${NGINX_PORT:-8080}` 可覆盖）；Nginx 容器内部监听端口由实现确定（建议 80）。`.env.example` 新增 `NGINX_PORT=8080`。

**D2 信任边界与 IP 防伪造**：单跳代理信任模型。Nginx **覆盖式**重写转发头，丢弃客户端伪造值：

```nginx
proxy_set_header X-Real-IP        $remote_addr;
proxy_set_header X-Forwarded-For   $remote_addr;   # 必须覆盖，不能用 $proxy_add_x_forwarded_for
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header Host              $host;
```

关键约束：`GetClientIp()` 取 `X-Forwarded-For` 的**第一项**，故不能用 `$proxy_add_x_forwarded_for`（会保留伪造值在首位）。信任边界由「app 不对外暴露端口（仅 Nginx 可连）」保证，Go 侧无需改动。**验证项**：确认 CNB 运行环境在 Nginx 之前是否还有额外代理层；若存在，`$remote_addr` 为前一层 IP，需在 Nginx 用 `real_ip` 模块 + `set_real_ip_from` 才能还原真实客户端 IP，或由 Owner 明确接受前一层 IP 作为对端 IP。

**D3 重试边界**：`proxy_next_upstream off;`——任何方法都不自动重试（单 upstream、单实例）。不设置 `non_idempotent`。POST/PUT/DELETE（秒杀下单、下单、支付）在上游连接失败/超时时绝不重试；GET/HEAD 的失败重试交由客户端决定。**准确表述**：关闭代理重试仅消除「代理层」这一重复来源，**不保证**请求恰好执行一次；「恰好一次」由业务层幂等机制保证（秒杀 `idempotency_key`、一人一单、条件状态更新 `RowsAffected` 等），二者职责不同、不得混淆。

**D4 网络隔离**：
- 新增 `nginx` 服务，发布唯一对外 HTTP 端口 `${NGINX_PORT:-8080}`，`depends_on: app (condition: service_healthy)`，自身 healthcheck 探测 `http://127.0.0.1/health`（透传 app）。
- `app` 服务**删除 `ports:` 映射**（仅 Compose 网络内 `app:8000` 可达），外部无法直连。
- `mysql`/`redis` 保留宿主机端口映射（兼容现有开发与压测 `verify()`），但**优先限制在宿主回环地址**：`127.0.0.1:${MYSQL_PORT:-3306}:3306`、`127.0.0.1:${REDIS_PORT:-6379}:6379`，并**实际验证 CNB 环境兼容性**。
- `prometheus` 端口映射保持现状（Owner 未要求变更，见 Open Risks）。

**D5 上游运行时 DNS 再解析（冷启动/重建/IP 变化恢复）**：为满足「app 冷启动、重建、容器 IP 变化后 Nginx 能恢复」的额外验收要求，upstream 采用运行时解析：

```nginx
resolver 127.0.0.11 valid=10s ipv6=off;   # Docker 内嵌 DNS
upstream my_shop_backend {
    zone my_shop_backend 64k;
    server app:8000 resolve;              # 运行时再解析，需 nginx >= 1.27.3
    keepalive 32;
}
```

要求使用支持 `server ... resolve` 的官方稳定镜像（≥ 1.27.3，Coder 锁定具体稳定 tag）。`valid=10s` 意味着 IP 变化后最长 ~10s 内恢复（期间短暂 502 可接受，需验证）。

**D6 监控与探针**：Nginx `location = /metrics { return 404; }`（公共入口不可达，不暴露端点存在性）；Prometheus 仍直连 `app:8000/metrics`（不动）。Nginx 透传 `/health`（纯 liveness、公开最小探活）；app 容器内 healthcheck 保持 `wget 127.0.0.1:8000/health` 不变。

**D7 日志关联（Request ID）**：
- Nginx **统一生成并覆盖客户端传入**的 Request ID：`proxy_set_header X-Request-ID $request_id;`（丢弃客户端自带的 `X-Request-ID`）。
- Go 侧新增**唯一一个非业务中间件** `internal/middleware/requestid.go`：读取 `X-Request-ID`（缺失时自生成，用于直连/健康检查路径）、注入请求 context、回写响应头 `X-Request-ID`、输出一条含 `request_id` 的结构化访问日志（字段名英文：`request_id`/`method`/`path`/`status`/`duration`/`client_ip`），在 `serve` 中注册于 `middleware.Response` 之前。
- Nginx 访问日志用 `log_format ... escape=json`，含 `$request_id`、`$remote_addr`、`$request_method`、`$request_uri`、`$status`、`$body_bytes_sent`、`$request_time`、`$upstream_response_time`、`$upstream_status`、`$http_user_agent`。
- **敏感信息约束**：Nginx 与 Go 的访问日志均**不记录** `Authorization`、`Cookie`、请求体等敏感信息。
- 这是本任务唯一允许的 Go 代码改动，不触碰 controller/service/logic/DAO。

**D8 配置管理与安全响应头**：Nginx 配置经新增 `nginx/Dockerfile`（`FROM` 官方稳定镜像，须锁定 tag）+ `COPY nginx.conf` 镜像内置（与 Prometheus 同策略，规避 rootless/嵌套容器 bind mount 单文件退化）。`server_tokens off;`；`add_header X-Content-Type-Options nosniff always;`、`add_header X-Frame-Options DENY always;`。默认不设 `proxy_intercept_errors`，Go 的业务错误响应原样透传；仅当上游不可达/超时由 Nginx 生成 502/504（`error_page` 返回通用最小响应，不含内部路径/堆栈/凭据/Nginx 版本）；请求体超限由 Nginx 返回 413 且不转发。

**D9 超时与请求体大小**：`client_max_body_size` 默认 `1m`（现有 API 均为小 JSON，无经 app 的大文件上传——上传走七牛直传凭证）；`proxy_connect_timeout 3s`、`proxy_send_timeout 30s`、`proxy_read_timeout 30s`、`client_body_timeout 30s`；upstream keepalive（见 D5）。以上数值可经环境变量/模板参数覆盖，实现阶段实测确认。

**D10 未来 K8s 迁移**：Nginx 保持纯反向代理职责（无 Lua/njs 业务逻辑），可被 Ingress 直接取代，不搭二次代理层。

**全局资源**：本任务不新增数据库 migration、错误码域，**无需**任何 `.agent/registry/*` 全局资源预留（与 Task Out of Scope 一致）。

## Interfaces and Data

需要保持（不破坏）的既有契约：
- 所有公开 HTTP API 的路径、方法、请求/响应体、HTTP 状态码、业务错误码（`internal/codes`）、`{code,message,data}` 响应结构不变；Nginx 仅透传，不改写业务响应。
- `/health` liveness 语义不变；`/metrics` 仅 Prometheus 内网直连抓取语义不变。
- IAM 会话设备 IP 元数据的语义不变（值变为真实对端 IP，属正确性修复，非语义破坏）。
- 秒杀限流/排队/熔断/一人一单/幂等 key 与失败语义不变。

需要建立的新契约：
- `app` 服务不再对外发布端口，仅 Compose 网络内 `app:8000` 可达。
- Nginx 对外唯一 HTTP 入口（`${NGINX_PORT:-8080}`），upstream 为 Compose DNS 名 `app:8000`（运行时 `resolve` 再解析 + `resolver 127.0.0.11`）。
- `mysql`/`redis` 宿主映射改为回环绑定（`127.0.0.1:<port>:<port>`）。
- 新增请求头契约：Nginx → app 透传 `X-Request-ID`（`$request_id`，覆盖客户端值）、`X-Real-IP`、`X-Forwarded-For`（覆盖为 `$remote_addr`）；app 回写响应头 `X-Request-ID`。
- 新增 Go 中间件 `internal/middleware/requestid.go`（非业务），输出含 `request_id` 字段的结构化访问日志（不记录 Authorization/Cookie/body）。
- `docs/design/deployment.md` 同步：§10 将 Nginx 从 Deferred 移入已实现，新增反向代理模型（统一入口、信任边界、网络隔离、日志关联、监控访问策略、上游 DNS 再解析），不宣称 K8s/Ingress 已部署。

## Business Invariants

- INV-001：经 Nginx 的登录/刷新，IAM 会话记录的客户端 IP 恒为 Nginx 真实对端 IP（`$remote_addr`），客户端伪造的 `X-Forwarded-For`/`X-Real-IP` 不生效。来源：AC-003。
- INV-002：非幂等请求（POST/PUT/DELETE，含秒杀下单/下单/支付）在上游连接失败或超时时，**代理层零自动重试**（不产生代理引入的重复请求）；「恰好执行一次」由业务层幂等机制（`idempotency_key`/一人一单/条件更新）保证，不由代理配置承诺。来源：AC-004。
- INV-003：`app` 服务不对外暴露端口，外部只能经 Nginx 访问 Go API，直连原 8000 端口不可达。来源：AC-001。
- INV-004：请求体超过配置上限返回 413，且不转发到后端（后端无该请求日志、无业务写入）。来源：AC-008。
- INV-005：秒杀业务正确性（一人一单、幂等、`sold ≤ total_stock`、订单数 == sold）经 Nginx 前后一致，限流/排队/熔断行为不变。来源：AC-009/AC-010。

## Failure and Consistency Semantics

- **成功**：经 Nginx 返回的响应与直连 `app:8000` 逐字段一致（状态码/错误码/响应体）。
- **Go 返回业务错误**：Nginx 原样透传（不设 `proxy_intercept_errors`），不改写状态码与 JSON 响应体。
- **上游不可达（连接失败）**：Nginx 生成 502；**超时**：504。均为通用最小错误响应，不含内部细节；Go 未收到请求，无业务副作用。
- **请求体超限**：Nginx 生成 413，请求**不转发**到后端。
- **上游 IP 变化**：app 重建后 DNS 再解析最长 ~10s 完成，期间请求返回 502（不重试、不产生业务副作用），之后自动恢复。
- **重试**：代理层全无自动重试（`proxy_next_upstream off`）；幂等请求的失败重试由客户端自行决定。代理重试关闭**不等于**「恰好一次」——后者由业务层幂等保证。
- **无跨存储一致性新增**：本任务不新增 MySQL/Redis/MQ 写路径，仅改变 HTTP 入口；秒杀既有的一致性与失败语义不变。

## Allowed / Forbidden Changes

允许：
- 修改 `docker-compose.yml`（新增 nginx 服务、删除 app 的 ports 映射、mysql/redis 映射改为回环绑定）。
- 新增 `nginx/` 目录（`Dockerfile`、`nginx.conf`、通用错误页）与必要的 `scripts/*`（如 nginx 配置校验脚本）、`.env.example` 的 `NGINX_PORT` 项。
- 新增 `internal/middleware/requestid.go` 并在 `internal/cmd/cmd.go` 注册（唯一 Go 代码改动）。

禁止：
- 触碰 `internal/controller`、`internal/service`、`internal/logic`、`internal/dao` 及任何业务逻辑。
- 改变既有公开 API、HTTP 状态码、业务错误码、响应体、`/health`/`/metrics` 语义。
- 对非幂等请求引入任何自动重试（含 `non_idempotent`）。
- 在访问日志中记录 `Authorization`、`Cookie`、请求体等敏感信息。
- 引入 Nginx Lua/njs 业务逻辑、K8s/Ingress 部署、HTTPS/TLS 终止。
- 新增数据库 migration、错误码域（无需全局资源预留）。
- 硬编码凭据、把敏感值写入日志/错误响应。
- 扩大业务功能范围。

## Verification Requirements

- INV-001 → 真实 MySQL/Redis 环境，`curl -H 'X-Forwarded-For: 1.2.3.4'` 经 Nginx 登录，核对 IAM 会话 IP 元数据为 Nginx 真实对端 IP 而非 `1.2.3.4`（AC-003）；并**确认 CNB 环境在 Nginx 之前无额外代理层**（若有，记录并评估 `real_ip` 处理）。
- INV-002 → 构造上游连接失败/超时，对 POST 下单/秒杀观察无代理自动重试、DB/Redis 无重复记录；GET/HEAD 无自动重试（AC-004）。
- INV-003 → `docker compose up` 后从宿主机直连原 8000 端口确认不可达，仅经 `${NGINX_PORT:-8080}` 可达（AC-001）。
- INV-004 → 发送超限请求体，确认返回 413 且后端日志无该请求（AC-008）。
- INV-005 → 用 `scripts/flashsale-loadtest/run.sh` 以 `BASE_URL=http://127.0.0.1:${NGINX_PORT}` 跑秒杀压测，核对 `correctness=PASS`（`sold≤total_stock`、订单数==sold、`dup_one/dup_idem=0`），并与接入前基线对比 p95/p99（AC-010）。
- AC-002 → 逐项对比经 Nginx 与直连 `app:8000` 的 IAM 登录/刷新、普通订单、秒杀下单、后台管理接口的状态码/错误码/响应体。
- AC-006 → 请求带/不带 `X-Request-ID` 均返回稳定 ID（Nginx 覆盖客户端值），Nginx 访问日志与 Go 访问日志用同一 `request_id` 关联，且含 `$upstream_response_time`；确认日志不含 Authorization/Cookie。
- 额外 → 验证 app 冷启动、`docker compose` 重启与 `down`+`up` 重建、容器 IP 变化后，Nginx 的上游 DNS 解析与恢复能力（记录恢复窗口）。
- AC-012 → `docker compose config` 退出码 0；`nginx -t` 通过；`go build ./...`/`go vet ./...`/`go test -p 1 ./...`（因新增 Go 中间件，需重跑）。

## Open Risks

- CNB 环境在 Nginx 之前是否还有额外代理层未确认（UNKNOWN）：若存在，`$remote_addr` 非真实客户端 IP，需 `real_ip` 模块或由 Owner 明确接受前一层 IP。
- `${NGINX_PORT:-8080}` 在目标环境是否可用未确认；`client_max_body_size 1m` 与各超时默认值需实测确认。
- `prometheus` 宿主端口映射（`0.0.0.0:9090`）Owner 未要求变更，保持现状；与「监控隔离」目标存在潜在不一致，建议后续单独评估。
- 回环绑定（`127.0.0.1`）在 CNB DinD 嵌套容器环境的可达性需实测（若不可达，压测 `verify()` 与开发直连会受影响，需回退为默认绑定并记录原因）。
- 覆盖式重写丢弃上游链路信息（`$proxy_add_x_forwarded_for` 才能保留完整链路），本任务单跳无损失。

## Owner Decision Record

Owner 于 2026-10-11 审阅并作出最终决策（适用范围：本任务 `nginx-reverse-proxy-v1`）：

- **D1（调整）**：Nginx 开发环境默认宿主端口 `8080`，支持 `NGINX_PORT` 覆盖；容器内部监听端口由实现方案确定。
- **D2（接受）**：单跳代理信任模型，Nginx 覆盖 `X-Forwarded-For` 与 `X-Real-IP`，不保留客户端伪造值；补充验证 CNB 环境是否存在额外代理层。
- **D3（接受）**：`proxy_next_upstream off`，由业务层保证幂等；不得声称关闭代理重试即可保证请求恰好执行一次。
- **D4（有条件接受）**：移除 App 宿主端口映射；MySQL/Redis 为兼容现有开发及压测流程保留必要映射，优先限制在宿主回环地址，并实际验证 CNB 环境兼容性。
- **D5（接受）**：公共 `/metrics` 返回 404，Prometheus 通过内部网络抓取；`/health` 保持公开的最小探活响应。
- **D6（接受）**：新增最小化 Request ID 中间件；Nginx 统一生成并覆盖客户端传入的 Request ID，Go 负责响应透传和日志关联；避免记录 Authorization、Cookie 等敏感信息。

额外验收要求：
- 检查 Nginx 在 App 冷启动、重建、容器 IP 变化后的上游 DNS 解析与恢复能力。
- 验证 Nginx 公共入口的代理头伪造防护、直连隔离、监控隔离及关键 API 兼容性。
- 本轮不扩大业务功能范围。

Owner 参与说明：Owner 将参与核心 Nginx 配置阅读、部署验证和故障排查，不以 Agent PASS 作为最终验收。
