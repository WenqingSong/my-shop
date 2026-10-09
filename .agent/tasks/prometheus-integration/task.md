# Task: 测试环境部署并接入 Prometheus

## Goal

在测试环境部署一个可用的 Prometheus Server（复用已有或新增），使其按固定间隔抓取 my-shop 服务的 `GET /metrics`，且目标（Target）状态为 UP；发起真实秒杀 HTTP 请求后，对应 histogram 的 bucket / count / sum 在 Prometheus 中持续更新，并可与服务端 `/metrics` 直读结果对账。

## Scope

- 测试环境具备一个可访问的 Prometheus Server：仓库当前无现成 Prometheus，默认通过 docker-compose 新增；若环境已有外部 Prometheus，则本任务退化为产出抓取配置并指向该 Server，验证标准不变。
- 配置 Prometheus 抓取 my-shop 服务的 `GET /metrics`（含 scrape job、`metrics_path=/metrics`、合理的 `scrape_interval`）。
- 打通容器内 Prometheus → 宿主机应用 `:8000` 的网络可达性（`host.docker.internal:host-gateway` 或等价方案）。
- 测试环境以 `flash_sale.metrics.enabled=true` 运行服务，使 `GET /metrics` 真实暴露。
- 将 Prometheus 的启停接入现有生命周期脚本 / Makefile，与 MySQL / Redis 的管理方式保持一致（幂等、healthcheck 探测）。
- 验证 Prometheus Targets 为 UP，并验证 histogram 的 bucket / count / sum 随真实请求更新。

## Out of Scope

- 不新增、不修改秒杀指标定义：`flashsale_*` 的标签维度与桶分布保持上一任务 `verify-metrics-histogram` 的已交付结论，不改动 `internal/metrics`。
- 不做 Grafana 面板、告警规则、Recording rules、Alertmanager 或告警通知。
- 不做生产环境部署、Kubernetes、TSDB 持久化卷与保留策略调优（仅测试环境，可接受默认）。
- 不改变 `GET /metrics` 的暴露范围与鉴权语义（维持现有 `flash_sale.metrics.enabled` 开关控制）。
- 不把 Prometheus 抓取抽象为全站通用可观测平台。

## Milestone

Milestone: 测试环境 Prometheus 部署与秒杀指标采集接入

## Acceptance Criteria

- [ ] AC-001（测试环境具备 Prometheus Server）：给定测试环境，执行项目统一生命周期命令后，存在一个可访问的 Prometheus Server（复用已有或新增），其 HTTP API 正常响应。
- [ ] AC-002（抓取任务已配置）：Prometheus 存在一个指向 my-shop 服务 `GET /metrics` 的 scrape job，按固定时间间隔定期抓取。
- [ ] AC-003（Targets 状态 UP）：当服务以指标开关开启运行、Prometheus 启动后，Prometheus 的 targets 中 my-shop 目标 `health=up`，且无持续 scrape 失败（lastScrape 持续前进、scrape 错误为空）。
- [ ] AC-004（histogram bucket/count/sum 持续更新并对账）：给定真实秒杀下单请求（成功入队 queued 及/或任一拒绝分支），当请求完成并经过至少一个抓取周期后，`flashsale_request_duration_seconds_bucket`、`flashsale_request_duration_seconds_count`、`flashsale_request_duration_seconds_sum` 在 Prometheus 中随请求数增长而更新（count 单调非降、sum>0、bucket 分布与耗时量级一致），且与服务端 `GET /metrics` 直读的对应序列值对账一致。
- [ ] AC-005（无回归）：新增 Prometheus 组件不破坏既有 MySQL / Redis / 应用启停与健康检查；`GET /metrics` 仍仅在指标开关开启时暴露，未引入绕过鉴权的暴露面。

## Relevant Context

已核实事实：

- 服务为 GoFrame HTTP 进程，监听 `:8000`（`SERVER_ADDRESS` 可覆盖），非容器化；MySQL / Redis 由 `docker-compose.yml` 提供。
- 秒杀指标定义于 `internal/metrics/metrics.go`：`flashsale_requests_total`（Counter）+ `flashsale_request_duration_seconds`（Histogram），标签为 `interface` / `result`；Histogram 经 Prometheus 文本格式暴露为 `flashsale_request_duration_seconds_bucket` / `_count` / `_sum`。
- `GET /metrics` 仅在 `flash_sale.metrics.enabled=true`（环境变量 `FLASH_SALE_METRICS_ENABLED`）时挂载（`internal/cmd/cmd.go` 的 `registerMetricsRoute`），位于 Response 中间件之外，直接输出文本格式。
- `docker-compose.yml` 目前仅含 mysql、redis，无 Prometheus 服务；仓库内无任何 prometheus 配置 / scrape 配置（已搜索确认）。
- 生命周期脚本 `scripts/{up,down,status,restart,...}.sh` + `Makefile` 统一管理启停；`scripts/lib.sh` 提供 `docker_compose` 封装与容器 healthcheck 探测。
- 上一任务 `verify-metrics-histogram` 已交付（delivery PASS），指标标签 / 桶分布已固化，本任务不改其结论。

Assumption：

- 仓库无现成 Prometheus，测试环境默认新增（docker-compose 服务）；若环境已有外部 Prometheus，则改为产出 scrape 配置并指向它，AC 不变。
- 应用以宿主机进程运行，Prometheus 容器访问宿主机 `:8000` 采用 `host.docker.internal:host-gateway`（或等价）方案。

OPEN QUESTION（不阻塞）：

- 是否存在已部署的外部测试环境 Prometheus 需要复用。本任务按「仓库内无 → 新增」推进，不影响 AC；若 Owner 明确存在外部 Prometheus，仅影响交付形态（新增容器 vs 仅 scrape 配置）。

## Verification

- AC-001 → 执行项目生命周期命令后，确认 Prometheus 容器 running（`docker compose ps`），`/api/v1/status/config` 或 `/-/ready` 正常响应。
- AC-002 → 读取 Prometheus 配置（`/api/v1/status/config` 或配置文件），确认 scrape job 的 `metrics_path=/metrics`、target 指向服务、`scrape_interval` 已设置。
- AC-003 → 访问 `/api/v1/targets`，确认对应 job 的 `health: "up"`、`lastScrape` 持续前进、无 scrape 连接错误。
- AC-004 → 以真实账号发起秒杀下单（可复用 `scripts/flashsale-loadtest` 或手工下单），等待 ≥1 个 `scrape_interval` 后，经 PromQL 或 `/api/v1/query` 查询 `flashsale_request_duration_seconds_count` / `_sum` / `_bucket`，断言数值增长，并与服务端 `GET /metrics` 直读的对应序列对账。
- AC-005 → `make health` 通过；`GET /metrics` 在开关关闭时不可访问、开启时返回 200。
- 通用检查：本任务以配置 / 脚本 / 容器为主，预期不改 Go 业务代码；如改动 shell 脚本执行 `bash -n` 校验，改动 compose 配置执行 `docker compose config` 校验；若涉及 Go 侧改动则补 `go build ./...` / `go test ./...`。

## Complexity

NORMAL

原因：任务核心是测试环境部署与采集接入，方案明确（docker-compose 新增 Prometheus + scrape 配置），不涉及业务数据模型、状态机、公开协议、并发一致性或安全边界变化；指标 schema 已由上一任务固化，本任务不改。网络拓扑 / 是否复用外部 Prometheus 等细节可沿用项目现有 docker-compose 惯例，以最小合理假设继续，无需 Analyst 出 Contract。

## Review Baseline

- Base commit：`99ace202a90de7699ab7ea3cd24dd7fa9742ef8c`（分支 `feat/prometheus-metrics-integration`，与 `origin/feat/prometheus-metrics-integration` 一致）。
- 任务开始时已有修改：无（`git status` 干净，working tree clean）。
- 重叠修改的区分方式：本任务产物集中在部署 / 观测文件（docker-compose 新增 prometheus 服务、新增 prometheus 配置、`scripts/*` 与 Makefile 接入），不涉及 `internal/metrics` 与 `docs/design/flash-sale.md`；当前工作区干净，无既有未提交修改需区分。

## Initial Route

交 Coder（NORMAL）
