# Cleaner Findings

## Review Target

- 任务：`nginx-reverse-proxy-v1`（分支 `feat/ningx-dev`）
- 任务基线（base）：`554cd21a0d87386f8e9348e5780b98a3330f4470`（`develop`，任务开始 working tree clean，无已有修改）
- Implementation Evidence Commit（C1，`review.target`）：`e0fa9b10d6a2cb3a3b4af00b09257aa9a5bfc4e5`
- 当前 HEAD（C2，Coder 的 Review Request metadata commit，非审查对象）：`ec35e18`
- 审查对象 = C1 及其相对 base 的全部相关变更（含 `a6ee8e2` 的 `docs/design/deployment.md` 更新）
- 工作区状态：`git status` clean，local HEAD == `origin/feat/ningx-dev`
- 关键配置：无新增 migration / 错误码域（本任务无需 `.agent/registry/*` 全局资源预留）；Nginx 镜像锁定 `nginx:1.27.3-alpine`

## Result

CHANGES_REQUIRED

存在一个阻塞性 P2 缺陷（`CLEAN-001`：`test-nginx.sh` 的 `nginx -t` 校验用单文件 bind mount，在目标 rootless/嵌套容器环境必然失败，导致 `make test` 无法通过），另有 P3 级文档/默认值漂移。实现本身（Nginx 配置、Compose 编排、Request ID 中间件）经静态与运行时验证正确。

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `docker-compose.yml`：`app` 已删除 `ports` 映射、新增 `nginx` 服务发布 `${NGINX_PORT:-8080}:80`（`docker compose config -q` 退出 0）；运行时 `my-shop-nginx:local` 可独立启动并响应。app 不对外暴露端口为静态事实，直连原 8000 不可达。 |
| AC-002 | NOT_VERIFIED | Nginx 未设 `proxy_intercept_errors`、未改写响应体，透传语义在配置层正确；但「逐字段一致」需真实 app（含七牛凭据）运行对比，本环境无七牛凭据，未运行。 |
| AC-003 | NOT_VERIFIED | `nginx.conf` 覆盖式重写 `X-Forwarded-For`/`X-Real-IP` 为 `$remote_addr`（禁用 `$proxy_add_x_forwarded_for`），且 `test-nginx.sh` 静态断言通过；但 IAM 会话 IP 的运行时验证需真实登录，未运行。 |
| AC-004 | PASS | `proxy_next_upstream off;` 静态断言通过（`test-nginx.sh`）；该指令确定性关闭代理层所有方法的重试，无运行时歧义。 |
| AC-005 | PASS | 运行时验证：`curl /metrics` → 404；`/health` 在上游不可达时透传路径返回 502（证明经 `location /` 透传）；Prometheus 抓取目标 `app:8000/metrics` 未改动。 |
| AC-006 | NOT_VERIFIED | `requestid` 中间件单测通过（回写响应头 + 注入上下文）；`nginx.conf` `log_format` 含 `$request_id` 与 `$upstream_response_time`（静态断言通过）；但 Nginx 日志与 Go 日志的跨端运行时关联需真实 app，未运行。 |
| AC-007 | PASS | 运行时验证：上游不可达时返回 502「Service temporarily unavailable.」（通用最小响应，不含内部路径/堆栈/凭据/Nginx 版本，`server_tokens off` 生效）。 |
| AC-008 | PASS | 运行时验证：2MB 请求体返回 413「Request entity too large.」；`client_max_body_size 1m` 在 nginx 层拦截，未转发后端（nginx 语义）。 |
| AC-009 | NOT_VERIFIED | `resolver 127.0.0.11 valid=10s` + `server app:8000 resolve` 静态断言通过；但冷启动/重启/容器 IP 变化的 DNS 再解析恢复需完整栈实测，未运行。 |
| AC-010 | NOT_VERIFIED | 压测脚本存在；需真实 app + 秒杀活动数据运行，未运行（另见 `CLEAN-002`：默认 `BASE_URL` 仍指向已移除的 8000）。 |
| AC-011 | PASS | `docs/design/deployment.md` 已更新并纳入 Nginx 反向代理模型（统一入口/信任边界/网络隔离/日志关联/监控访问策略/DNS 再解析），与 Contract 及实现一致，未宣称 K8s/Ingress 已部署。 |
| AC-012 | PASS | `go build ./...` ✓；`go vet ./...` ✓；`go test -p 1 ./...`（`DATABASE_DEFAULT_PASS=root` + MySQL/Redis 就绪）✓；`docker compose config -q` ✓；`nginx -t`（经已构建 `my-shop-nginx:local` 镜像）✓。但自动化 `test-nginx.sh` 的 `nginx -t` 因 bind mount 失败（见 `CLEAN-001`）。 |

说明：AC-002/003/006/009/010 的 `NOT_VERIFIED` 均因需要真实 app（含七牛凭据）+ 完整栈/压测环境，属运行时集成验证，需 Deliverer 阶段或提供环境后完成；其配置层机制已全部经静态断言或运行时（nginx 独立）验证正确。

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
| `bash scripts/test-nginx.sh` | FAIL | `nginx -t` 单文件 bind mount 在 DinD 环境报 "not a directory"，退出 1（`CLEAN-001`） |
| Nginx 运行时（`docker run -d my-shop-nginx:local`） | PASS | `/metrics`→404、`/health`(上游 down)→502 通用页、2MB body→413、安全响应头 `nosniff`/`DENY`/`Server: nginx`（无版本） |
| 全局资源三边一致性 | N/A | 本任务不新增 migration/错误码域，无 Registry 预留 |

## Findings

### CLEAN-001：`test-nginx.sh` 的 `nginx -t` 校验使用单文件 bind mount，在目标环境必然失败

- Severity：P2
- Status：OPEN
- Location：`scripts/test-nginx.sh` 第 101~114 行（`docker run --rm -v "${NGINX_CONF}:/etc/nginx/nginx.conf:ro" -v "${NGINX_DIR}/error:/etc/nginx/error:ro"`）
- AC / Invariant：AC-012（`nginx -t` 校验通过）；「部署可重复」与 `make test` 统一入口可用
- Trigger：在 rootless/嵌套容器（CNB DinD）环境运行 `make test-nginx` 或 `make test`，且本地存在基础镜像 `nginx:1.27.3-alpine`（执行 `docker compose build` 后必然存在）。
- Actual：`docker run -v <单文件>` 单文件 bind mount 在该环境退化为目录挂载，容器启动报 `error mounting ... not a directory`，脚本 `fail` 退出 1，`make test` 随之失败。
- Expected：复用项目既有模式——Nginx 配置经 `nginx/Dockerfile` COPY 内置进 `my-shop-nginx:local`，`nginx -t` 应直接 `docker run --rm my-shop-nginx:local nginx -t`（无 bind mount）；或参考 `test-prometheus.sh` 用 `docker run --entrypoint cat` 提取二进制规避 bind mount。禁用单文件 bind mount。
- Impact：`make test`/`make test-nginx` 在目标环境退出非零，AC-012 的 `nginx -t` 无法经自动化验证；CI 集成测试门被阻断。生产部署不受影响（镜像可正常构建，`nginx -t` 经已构建镜像验证通过）。
- Evidence：`bash scripts/test-nginx.sh` 实测退出 1，报 `error mounting "/root/projects/my-shop/nginx/nginx.conf" ... not a directory`；而 `docker run --rm my-shop-nginx:local nginx -t` 实测 "syntax is ok / test is successful"。`docker-compose.yml` 第 125~127 行注释、`nginx/Dockerfile` 注释均明确记录「bind mount 单文件退化」约束；`test-prometheus.sh` 已用镜像提取方式规避。
- Required Fix Boundary：使 `test-nginx.sh` 的 `nginx -t` 不再使用单文件 bind mount，改用已构建镜像（或构建镜像后）运行 `nginx -t`，并保持「语法校验失败时非零退出」语义；不得改动其它校验项与生产配置。

### CLEAN-002：秒杀压测脚本默认 `BASE_URL` 仍指向已移除的宿主端口 8000

- Severity：P3
- Status：OPEN
- Location：`scripts/flashsale-loadtest/run.sh` 第 34 行（`BASE_URL="${BASE_URL:-http://127.0.0.1:8000}"`）及第 16 行注释
- AC / Invariant：AC-010（压测经 Nginx 入口执行）；Contract 验证要求 `BASE_URL=http://127.0.0.1:${NGINX_PORT}`
- Trigger：按脚本自身用法示例（不显式传 `BASE_URL`）直接 `./run.sh` 发起压测。
- Actual：app 已删除宿主端口映射，`http://127.0.0.1:8000` 连接被拒，压测与用户注册/登录全部失败。
- Expected：默认入口更新为 Nginx 对外端口（如 `http://127.0.0.1:${NGINX_PORT:-8080}`），或至少在注释/用法中明确 `BASE_URL` 必须指向 Nginx 入口。
- Impact：压测脚本默认值失效，容易误导后续 AC-010 验证；不影响生产。
- Evidence：`docker-compose.yml` 中 `app` 无 `ports` 映射（已核对）；`run.sh:34` 默认 8000；Contract「Verification Requirements」明确要求 `BASE_URL=http://127.0.0.1:${NGINX_PORT}`。
- Required Fix Boundary：将压测默认入口对齐 Nginx 端口，或显式文档化该覆盖要求；不改动压测正确性判定逻辑。

### CLEAN-003：`README.md` 快速开始仍指向已移除的 8000 端口与「四容器/后续 Nginx」旧表述

- Severity：P3
- Status：OPEN
- Location：`README.md` 第 72 行（「默认访问 `http://127.0.0.1:8000/health`」「四个容器」）、第 100 行（`curl http://127.0.0.1:8000/health`）、第 106 行（「供 Prometheus 及后续 Nginx 使用」）
- AC / Invariant：与最终实现一致的对外访问入口（统一入口 `${NGINX_PORT:-8080}`）
- Trigger：用户按 README「快速开始」直接 curl 8000 端口。
- Actual：8000 已不可达，文档给出错误的访问方式与过期的服务数量/「后续 Nginx」表述。
- Expected：更新为经 Nginx 的对外入口（`http://127.0.0.1:${NGINX_PORT:-8080}/health`），并同步容器数量与「Nginx 已实现」的事实。
- Impact：面向使用者的文档与实现漂移，属低风险维护项。
- Evidence：`README.md` 上述行；`docs/design/deployment.md` 已正确更新为 Nginx 统一入口（对比可见 README 未同步）。
- Required Fix Boundary：仅修正 README 中与本次部署模型相关的过期表述，不扩大文档改动范围。
