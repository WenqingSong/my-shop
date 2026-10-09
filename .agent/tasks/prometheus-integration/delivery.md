# Delivery Verification

## Milestone and Target
- Milestone：测试环境 Prometheus 部署与秒杀指标采集接入
- Delivery Target：`feat/prometheus-metrics-integration`（feature_head `bc3c859`，代码与 review.target `753228e` 逐字一致，其后仅任务元数据提交）
- Cleaner Review Target：`753228e99e386989e23e6c71757d6d31460656a6`
- Target Match：YES（`delivery.review_target` == `review.target` == `owner.review_target`）

## 三个不可变 SHA
- `review_target`：`753228e99e386989e23e6c71757d6d31460656a6`
- `feature_head`：`bc3c85939d7ad1c05edf3f22070f74b33ac2559b`（当前 feature HEAD，其生产代码与 `753228e` 相同）
- `develop_base`：`99ace202a90de7699ab7ea3cd24dd7fa9742ef8c`（`origin/develop`）

## Environment
- OS：Alpine Linux（容器化 workspace，rootless Docker-in-Docker，`/.dockerenv` 存在）
- Go：`go1.24.1 linux/amd64`
- Docker：server `29.6.2`；Docker Compose `v5.3.1`
- MySQL：`mysql:8.0` 容器（`my-shop-mysql`，healthy，端口 3306）
- Redis：`redis:7-alpine` 容器（`my-shop-redis`，healthy，端口 6379）
- Prometheus：`prom/prometheus:v2.53.0` 容器（端口 9090，本次验收启动）
- 应用：GoFrame 进程（`bin/my-shop serve`，监听 `:8000`，`FLASH_SALE_METRICS_ENABLED=true` 启动）
- 配置来源：`manifest/config/config.yaml` 默认值 + 环境变量覆盖；未记录任何 Secret 值
- 测试数据与隔离：复用现有 MySQL/Redis 容器；秒杀下单用「不存在的活动 id=1」触发 `gate_rejected` 拒绝分支（无真实业务数据写入）；验收后清理运行时产物（`storage/`、临时容器/卷、`tmp/delivery-*` 日志）

### 环境与 Task 的差异（重要，非缺陷）
Task 目标环境为「应用宿主机进程 + Docker 宿主机」，`docker-compose.yml` 中 Prometheus 的单文件 bind mount `./prometheus/prometheus.yml:/etc/prometheus/prometheus.yml:ro` 是该目标环境下的标准正确写法。本验收 Sandbox 为 rootless DinD：Docker daemon 的挂载命名空间看不到 workspace 的 `/root/...` 路径，直接 `docker compose up -d prometheus` 会因 bind mount 被当作目录而启动失败（已实测复现）。因此本次用命名卷 `promcfg` + `!override` 替换挂载机制，其余（image/command/extra_hosts/端口/healthcheck/配置内容）完全按 compose 服务定义等价启动。网络打通 `host.docker.internal:host-gateway` 实测解析到 workspace 容器 IP 且应用 `:8000` 可达。

## Verification

| Check | Result | Evidence |
|---|---|---|
| 正式构建 `go build ./...` + `go build -o bin/my-shop .` | PASS | 两者 exit 0 |
| 脚本语法 `bash -n`（9 个脚本） | PASS | `up/down/restart/status/logs/health/test/test-prometheus/lib` 全部通过 |
| `docker compose -f docker-compose.yml config -q` | PASS | exit 0 |
| `promtool check config`（官方 `prom/prometheus:v2.53.0` 镜像） | PASS | `SUCCESS: ... is valid prometheus config file syntax` |
| `make test`（go vet + go test -p 1 + test-prometheus.sh） | PASS | exit 0，全部包 ok，`Prometheus 接入静态校验通过`、`测试全部通过` |
| AC-001 Prometheus Server 可访问 | PASS | `/-/ready`=200；`/api/v1/targets`、`/api/v1/query`、`/api/v1/status/config` 均 `status:success`；容器 `running (healthy)` |
| AC-002 抓取任务已配置 | PASS | 运行时 `/api/v1/status/config` 回读：`job_name=my-shop`、`metrics_path=/metrics`、`scrape_interval=15s`、target `host.docker.internal:8000`，与 `prometheus.yml` 一致 |
| AC-003 Targets UP | PASS | `/api/v1/targets`：`health="up"`、`lastError=""`、`scrapeUrl=http://host.docker.internal:8000/metrics`、lastScrape 持续前进 |
| AC-004 histogram 更新与对账 | PASS | 3 次真实下单（`POST /flash-sales/1/orders` → `code=12001` `gate_rejected`）后，服务端 `/metrics` 直读 `count=3`、`sum=0.0007308639999999999`、`bucket{le="+Inf"}=3`、`bucket{le="0.0005"}=3`、`requests_total=3`；等一个抓取周期后 Prometheus `/api/v1/query` 逐值一致（count=3、sum=0.0007308639999999999、bucket+Inf=3、bucket 0.0005=3、requests_total=3） |
| AC-005 无回归 | PASS | `make health`：App/MySQL/Redis/Prometheus 全部 healthy（exit 0）；`/metrics` 开关语义：关=404、开=200（可逆）；`status`/`logs` 脚本正确纳入 prometheus；无任何 `.go` 生产代码改动（diff 确认） |

## Acceptance Evidence

- AC-001：Prometheus 容器 `running (healthy)`，`/-/ready`=200，API 均 `success`。
- AC-002：`promtool check config` SUCCESS；运行时 `/api/v1/status/config` 回读的 `scrape_configs` 与配置文件一致（`job_name: my-shop`、`metrics_path: /metrics`、`scrape_interval: 15s`、target `host.docker.internal:8000`）。
- AC-003：`/api/v1/targets` 返回 `health:"up"`、`lastError:""`、`lastScrape` 前进、无 dropped targets。
- AC-004：3 次真实下单（拒绝分支 `gate_rejected`）后，Prometheus `flashsale_request_duration_seconds_count=3`、`_sum=0.0007308639999999999`、`_bucket{le="+Inf"}=3`，与服务端 `/metrics` 直读逐值对账一致；count 单调非降（0→3）、sum>0、bucket 分布与耗时量级（avg≈0.24ms）一致。
- AC-005：`make health` exit 0；`/metrics` 仅在 `FLASH_SALE_METRICS_ENABLED=true` 时暴露（关=404、开=200），未引入鉴权绕过（本任务未改任何 `.go` 代码）。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| `docker compose up -d prometheus` 的 bind mount 原样路径 | 本 Sandbox 为 rootless DinD，daemon 看不到 workspace `/root/...`，bind mount 被当作目录 | 低：bind mount 是目标环境标准写法，`config -q` 已校验；已用命名卷等价验证运行时行为 |
| `make up` / `make down` 全生命周期原样循环 | 同上（up.sh 内 `docker compose up -d mysql redis prometheus` 会撞 bind mount） | 低：`up/down/status/health/logs/test` 均已静态确认纳入 prometheus，`make health`/`make status` 已实测 |
| Race Test / 性能压测 | Task 未要求（本任务为部署/观测，非并发一致性或性能） | 无 |
| Grafana / 告警 / Alertmanager / 生产部署 | Task Out of Scope | 无 |

## Remaining Risks
- 唯一环境性差异：交付的 bind mount 写法未在本 Sandbox 中「原样」跑通 `docker compose up`，仅通过 `config -q` 静态校验 + 命名卷等价运行时验证覆盖。该写法对目标环境（宿主机 Docker）是标准正确写法，风险低；若 Owner 需在本 DinD 环境长期跑，需另配命名卷/挂载策略（属环境适配，非交付缺陷）。
- 对账验证仅覆盖 `gate_rejected` 拒绝分支（`interface=order`）；`queued` 成功入队与 `consume` 链路指标未在本环境发真实成功单验证（已有 `internal/metrics` 单测 + 上一任务结论覆盖，本任务不改指标定义）。

## Result
PASS
