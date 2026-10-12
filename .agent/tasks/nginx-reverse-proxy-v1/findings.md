# Cleaner Findings

## Review Target

- 任务：`nginx-reverse-proxy-v1`（分支 `feat/ningx-dev`）
- 任务基线（base）：`554cd21a0d87386f8e9348e5780b98a3330f4470`（`develop`，任务开始 working tree clean，无已有修改）
- Implementation Evidence Commit（C1，`review.target`）：`f1c129b882a1fddf7edcc41a2af8a4656d8ed9e4`（首次审查后 Coder 修复 CLEAN-001/002/003 的提交）
- 首次审查对象（旧 C1）：`e0fa9b10d6a2cb3a3b4af00b09257aa9a5bfc4e5`；修复提交 `f1c129b` 仅改动 `scripts/test-nginx.sh`、`scripts/flashsale-loadtest/run.sh`、`README.md`，无生产代码/测试改动
- 当前 HEAD（C2，Coder 的 review-request metadata commit，非审查对象）：`54992cd`
- 审查对象 = C1（`f1c129b`）及其相对 base 的全部相关变更（含 `a6ee8e2` 的 `docs/design/deployment.md` 更新）
- 工作区状态：`git status` clean，local HEAD == `origin/feat/ningx-dev`
- 关键配置：无新增 migration / 错误码域（本任务无需 `.agent/registry/*` 全局资源预留）；Nginx 镜像锁定 `nginx:1.27.3-alpine`

## Result

CLEAN

首次审查的 `CLEAN-001`（P2）、`CLEAN-002`（P3）、`CLEAN-003`（P3）均已修复并经复审验证关闭；无开放 P0/P1/P2。

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `docker-compose.yml`：`app` 已删除 `ports` 映射、新增 `nginx` 服务发布 `${NGINX_PORT:-8080}:80`（`docker compose config -q` 退出 0）；运行时 `my-shop-nginx:local` 可独立启动并响应；app 不对外暴露端口为静态事实，直连原 8000 不可达。 |
| AC-002 | PASS | Nginx 未设 `proxy_intercept_errors`、未设 `sub_filter`/rewrite 等改写指令，`location /` 纯 `proxy_pass`，响应原样透传为 nginx 确定性行为；`test-nginx.sh` 第 80~82 行断言「无 proxy_intercept_errors」。运行时逐字段对比属 Deliverer 运行验收（需真实 app），本环境无七牛凭据未执行。 |
| AC-003 | PASS | `nginx.conf` 覆盖式重写 `X-Forwarded-For`/`X-Real-IP` 为 `$remote_addr`（禁用 `$proxy_add_x_forwarded_for`）；Contract 已 VERIFIED GoFrame `GetClientIp()` 取 `X-Forwarded-For` 第一项，二者构成确定性链（会话 IP = `$remote_addr`）。`test-nginx.sh` 第 30~37 行静态断言通过。运行时登录 IP 确认属 Deliverer。 |
| AC-004 | PASS | `proxy_next_upstream off;` 静态断言通过（`test-nginx.sh` 第 39~44 行）；该指令确定性关闭代理层所有方法重试。 |
| AC-005 | PASS | 运行时验证：`curl /metrics` → 404；`/health` 在上游不可达时经 `location /` 透传返回 502（证明 `/health` 走代理路径）；Prometheus 抓取目标 `app:8000/metrics` 未改动。 |
| AC-006 | PASS | `requestid` 中间件单测通过（回写响应头 + 注入上下文，真实 HTTP 行为）；`nginx.conf` `log_format` 含 `$request_id` 与 `$upstream_response_time`（`test-nginx.sh` 第 67~77 行断言通过）。运行时跨端日志关联属 Deliverer。 |
| AC-007 | PASS | 运行时验证：上游不可达时返回 502「Service temporarily unavailable.」（通用最小响应，不含内部路径/堆栈/凭据/Nginx 版本，`server_tokens off` 生效，响应头 `Server: nginx` 无版本）。 |
| AC-008 | PASS | 运行时验证：2MB 请求体返回 413「Request entity too large.」；`client_max_body_size 1m` 在 nginx 层拦截，未转发后端（nginx 语义）。 |
| AC-009 | PASS | `resolver 127.0.0.11 valid=10s` + `server app:8000 resolve` + `zone`（`test-nginx.sh` 第 46~54 行断言通过），`nginx -t` 通过（镜像内）。运行时冷启动/重建/IP 变化恢复窗口属 Deliverer。 |
| AC-010 | PASS | 业务正确性一致性由「本任务未改任何 `controller/service/logic/dao` 业务代码 + 秒杀限流/排队/熔断 key 不依赖客户端 IP（Contract VERIFIED）」保证；压测脚本已对齐 Nginx 入口（`run.sh` 默认 `BASE_URL=http://127.0.0.1:${NGINX_PORT:-8080}`）。代理接入前后性能对比证据属 Deliverer 运行验收，本环境无七牛凭据未执行。 |
| AC-011 | PASS | `docs/design/deployment.md` 已纳入 Nginx 反向代理模型（统一入口/信任边界/网络隔离/日志关联/监控访问策略/DNS 再解析），与 Contract 及实现一致，未宣称 K8s/Ingress 已部署。 |
| AC-012 | PASS | `go build ./...` ✓；`go vet ./...` ✓；`go test -p 1 ./...`（`DATABASE_DEFAULT_PASS=root` + MySQL/Redis 就绪）✓；`docker compose config -q` ✓；`nginx -t`（经 `docker run --rm my-shop-nginx:local nginx -t`）✓；`bash scripts/test-nginx.sh` ✓（修复后）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 退出 0 |
| `go vet ./...` | PASS | 退出 0 |
| `go test -p 1 ./...`（`DATABASE_DEFAULT_PASS=root`，MySQL/Redis 就绪） | PASS | 全包通过，含 `internal/middleware`（新增 RequestID 测试）与 `internal/cmd` |
| `go test ./internal/middleware -run TestRequestID -v` | PASS | 2 个测试通过，日志含 `request_id`/`status`/`client_ip` |
| `docker compose config -q` | PASS | 退出 0 |
| `nginx -t`（`docker run --rm my-shop-nginx:local nginx -t`） | PASS | "syntax is ok / test is successful" |
| `bash scripts/test-container.sh` | PASS | 静态断言通过 |
| `bash scripts/test-prometheus.sh` | PASS | 静态断言通过（promtool 缺失时优雅跳过） |
| `bash scripts/test-nginx.sh`（首次审查时） | FAIL | `nginx -t` 单文件 bind mount 报 "not a directory"（`CLEAN-001` 根因） |
| `bash scripts/test-nginx.sh`（复审时） | PASS | 改为 `docker run --rm my-shop-nginx:local nginx -t` 后通过，含新增「统一入口一致性」第 12 项 |
| `bash scripts/flashsale-loadtest/test-run.sh`（复审时） | PASS | 压测脚本回归测试通过 |
| Nginx 运行时（`docker run -d my-shop-nginx:local`） | PASS | `/metrics`→404、`/health`(上游 down)→502 通用页、2MB body→413、安全响应头 `nosniff`/`DENY`/`Server: nginx`（无版本） |
| 全局资源三边一致性 | N/A | 本任务不新增 migration/错误码域，无 Registry 预留 |

## Findings

首次审查的三条 Finding 均已修复并关闭。

### CLEAN-001：`test-nginx.sh` 的 `nginx -t` 校验使用单文件 bind mount（P2）— CLOSED

- Severity：P2
- Status：CLOSED
- Location：`scripts/test-nginx.sh`（原第 101~114 行）
- 修复：改为 `docker run --rm my-shop-nginx:local nginx -t`（复用「配置经 Dockerfile COPY 内置」的已构建镜像，无 bind mount）；移除未使用的 `NGINX_DIR` 变量。
- 复审验证：`bash scripts/test-nginx.sh` 实测退出 0，输出 "syntax is ok / test is successful"；不再出现 bind mount。
- 回归防护：新增第 12 项「统一入口一致性」，断言 `run.sh`/`README.md` 不残留 `127.0.0.1:8000`。

### CLEAN-002：秒杀压测脚本默认 `BASE_URL` 指向已移除的 8000（P3）— CLOSED

- Severity：P3
- Status：CLOSED
- Location：`scripts/flashsale-loadtest/run.sh` 第 34 行及第 16 行注释
- 修复：默认值改为 `BASE_URL="${BASE_URL:-http://127.0.0.1:${NGINX_PORT:-8080}}"`，注释同步为「默认经 Nginx 唯一入口」。
- 复审验证：`grep -n '127.0.0.1:8000' scripts/flashsale-loadtest/run.sh` 无匹配；`test-run.sh` 回归测试通过。

### CLEAN-003：`README.md` 快速开始仍指向 8000 / 「四容器」旧表述（P3）— CLOSED

- Severity：P3
- Status：CLOSED
- Location：`README.md` 第 51/66/72/100/106/180/191 行
- 修复：统一为 `nginx/app/MySQL/Redis/Prometheus 五个容器`、对外入口 `http://127.0.0.1:8080/health`、`make health` 校验五服务，`docker-compose.yml` 与容器化部署模型描述同步含 nginx。
- 复审验证：`grep -n '127.0.0.1:8000\|localhost:8000' README.md` 无匹配。

## Runtime Milestone（Deliverer 运行验收，非 Cleaner 审查范围）

以下 AC 的**运行时里程碑确认**需真实 app（含七牛凭据）+ 完整栈，属 Deliverer 运行验收职责，本环境（`.env` 无七牛 AK/bucket）无法执行，已如实标注：

- AC-002：经 Nginx 与直连 `app:8000` 的 IAM 登录/刷新、普通订单、秒杀下单、后台管理接口逐字段对比。
- AC-003：伪造 `X-Forwarded-For: 1.2.3.4` 经 Nginx 登录，核对 IAM 会话 IP 为 Nginx 真实对端 IP。
- AC-006：同一 `request_id` 在 Nginx 访问日志与 Go 访问日志间的跨端关联。
- AC-009：app 冷启动/重建/容器 IP 变化后 DNS 再解析恢复窗口实测。
- AC-010：秒杀压测（`BASE_URL=http://127.0.0.1:${NGINX_PORT}`）代理接入前后的业务正确性与 p95/p99 性能对比证据。

以上机制均已在本审查中于配置/单元/静态/nginx 独立运行时层面验证正确，仅缺完整栈运行时确认。
