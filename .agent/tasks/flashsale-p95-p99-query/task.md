# Task: 秒杀下单接口 p95/p99 延迟查询

## Goal

在已有秒杀 Prometheus 指标（`flashsale_request_duration_seconds` Histogram）基础上，提供「最近 5 分钟、秒杀下单接口」的 p95 / p99 延迟查询，并以 Prometheus recording rules 固化：查询使用 `histogram_quantile` + `rate(...[5m])`，只取 `interface="order"`（秒杀下单接口）序列，产出单位明确（秒）的稳定指标，语义为下单接口的 HTTP 接收延迟（不含异步最终订单完成耗时）。

## Scope

- 新增 Prometheus recording rules 文件（如 `prometheus/rules/flashsale_latency.yml`），定义两个查询：秒杀下单接口 p95 与 p99，PromQL 为 `histogram_quantile(0.95/0.99, sum by (le) (rate(flashsale_request_duration_seconds_bucket{interface="order"}[5m])))`，产出指标名稳定且单位后缀与单位一致。
- 在 `prometheus/prometheus.yml` 增加 `rule_files`，指向上述 rules 文件。
- 在 `docker-compose.yml` 将 rules 目录只读挂载到 prometheus 容器，使 recording rules 随 Prometheus 加载。
- 扩展 `scripts/test-prometheus.sh`（或新增等价校验），机械验证 recording rules 的关键性质：`interface="order"` 过滤、`[5m]` 窗口、`histogram_quantile` 用法、单位后缀与单位一致；如环境具备 `promtool`，用 `promtool check rules` 校验语法。
- 在 rule 文件注释中明确语义与单位：p95/p99 为下单接口 HTTP 接收延迟（`ObserveOrder` 在 `CreateOrder` 进入→返回受理结果期间测量），不含异步落单耗时（该耗时在 `interface="consume"` 单独观测）；单位秒，换算毫秒为 `× 1000`。
- 如需，在 `docs/design/flash-sale.md` §11.4 做轻量补充说明（p95/p99 查询的落点、指标名与单位），不改变既有设计事实。

## Out of Scope

- 不改 `internal/metrics` 的指标定义、桶分布或标签 schema（已由 `verify-metrics-histogram` 固化）。
- 不新增全站通用 HTTP 延迟指标或把秒杀指标抽象为全站可观测平台（保持秒杀域聚焦）。
- 不做 Grafana 面板、告警规则（Alerting rules）、Alertmanager 或告警通知（本任务只做 p95/p99 查询本身）。
- 不提供单活动（activity）粒度的 p95/p99 切片（`docs/design/flash-sale.md` §11.4 已声明通用 Histogram 不提供单活动切片）。
- 不改秒杀业务不变量、限流/熔断/排队软上限语义与 MySQL 事实来源，不改 `GET /metrics` 暴露范围与鉴权语义。

## Milestone

Milestone: 秒杀下单接口 p95/p99 延迟查询（recording rules）

## Acceptance Criteria

- [ ] AC-001（查询已固化为 recording rules）：仓库存在 recording rules 定义，用 `histogram_quantile` + `rate(...[5m])` 作用于 `flashsale_request_duration_seconds_bucket`，分别产出 p95 与 p99 两个稳定指标；`promtool check rules`（或等价语法校验）通过，无未知指标/语法错误。
- [ ] AC-002（限定秒杀下单接口）：查询表达式显式过滤 `interface="order"`，不含 `interface="consume"` 或其它接口；`sum by (le)` 跨 `result` 维度聚合，得到下单接口整体 p95/p99（不按 result 拆分）。
- [ ] AC-003（单位明确且正确）：`histogram_quantile` 基于秒单位桶返回秒值；recording rule 产出的指标名后缀与单位一致（`_seconds`，或经 `* 1000` 换算为毫秒并以 `_milliseconds` 结尾），指标帮助文本/注释明确标注单位；不存在「秒值配毫秒命名」或漏乘 1000 的单位错配。
- [ ] AC-004（语义为 HTTP 接收延迟）：p95/p99 反映的是下单接口 HTTP 接收延迟（`ObserveOrder` 在 `CreateOrder` 从进入到返回受理结果的时间），不包含异步最终订单完成耗时（该耗时为 `interface="consume"`，recording rule 不使用 consume 序列）；该语义在 rule 注释中明确。
- [ ] AC-005（静态校验能识别错误实现）：扩展后的校验脚本能机械识别「错误接口过滤（用 consume 或未过滤）」「错误窗口（非 5m）」「单位错配」等错误实现并失败；正确实现校验通过。
- [ ] AC-006（无回归）：不改 `internal/metrics` 指标定义与埋点、不改 `/metrics` 暴露语义；既有 `scripts/test-prometheus.sh` 抓取校验与 `docker compose config` 校验仍通过。

## Relevant Context

已核实事实：

- `internal/metrics/metrics.go` 已定义 `flashsale_request_duration_seconds`（HistogramVec，桶 `0.5ms~10s` 秒单位），标签为 `interface`（`order`/`consume`）+ `result`（低基数枚举）。
- `ObserveOrder` 在 `internal/logic/flashsale/flashsale.go` 的 `CreateOrder` 中用 `defer` 以 `time.Since(started)` 测量下单接口耗时（HTTP 接收延迟）；`ObserveConsume` 在 `internal/logic/flashsale/consume.go` 单独测量异步消费耗时。二者通过 `interface` 标签分离，`interface="order"` 对应唯一下单路由 `POST /flash-sales/:id/orders`。
- `prometheus/prometheus.yml` 已有抓取配置（`job_name=my-shop`、`metrics_path=/metrics`、`scrape_interval: 15s`、`evaluation_interval: 15s`），但无 `rule_files`。
- `docker-compose.yml` 已有 prometheus 服务，仅只读挂载 `prometheus/prometheus.yml`，未挂载 rules 目录。
- `scripts/test-prometheus.sh` 静态校验抓取配置，未校验 recording rules。
- `docs/design/flash-sale.md` §11.4 已声明「Counter + Histogram 覆盖 QPS/成功率/拒绝率/p95/p99」，但未定义 p95/p99 的查询表达式；§11.4 明确「通用 Histogram 不提供单活动 p99/p95 切片」。
- 历史任务：`verify-metrics-histogram`（标签/桶固化，delivery PASS）、`prometheus-integration`（Prometheus 部署 + 抓取，delivery PASS，其 Out of Scope 明确「不做 Recording rules」）。

Assumption：

- 「查询」默认落点为 Prometheus recording rules：项目已有 Prometheus、无 Grafana，recording rules 是固化查询、可运行、可机械校验、可被告警/面板引用的标准方式。若 Owner 期望 Grafana 面板或纯文档载体，不影响查询表达式语义（`histogram_quantile` + `rate` + `[5m]` + `interface="order"`），仅调整固化位置。
- p95/p99 聚合所有 `result` 维度（`sum by (le)` 跨 result），得到下单接口整体延迟分位，不按结果拆分（Owner 未要求拆分）。

## Verification

- AC-001 / AC-002 / AC-003 / AC-004 → `promtool check rules prometheus/rules/`（若环境可用，否则等价语法校验）+ 读取 rule 文件核对 PromQL：`histogram_quantile` + `rate` + `[5m]`、`interface="order"`、`sum by (le)`、指标名后缀与单位一致。
- AC-005 → 运行扩展后的 `scripts/test-prometheus.sh`（或新校验脚本），断言对错误实现（错误接口过滤/窗口/单位）失败、对正确实现通过。
- AC-006 → `docker compose config -q` 通过；`bash -n scripts/*.sh` 通过；若涉及 Go 代码改动则补 `go build ./...` / `go test ./...`（预期不改 Go 业务代码）。
- Milestone（Deliverer）→ 测试环境以 `flash_sale.metrics.enabled=true` 运行服务，发起真实秒杀下单请求，经至少一个 `evaluation_interval` 后，经 Prometheus API 查询 recording rule 产物，断言 p95/p99 指标存在、数值非空且与 `/metrics` 直读的 histogram 数据量级一致。

## Complexity

NORMAL

原因：查询方案明确（`histogram_quantile` + `rate` + `[5m]` + `interface="order"`，Prometheus 标准用法），不涉及数据模型、状态机、事务/并发一致性、MQ、权限或安全边界变化；Histogram 指标与 Prometheus 抓取均已由前序任务交付并固化，本任务仅在其上新增查询固化与校验。无全局资源需求（不涉及 migration_version / error_code_domain）。

## Review Baseline

- Base commit：`37d8ad1461943244add665cbb59e4d08522c1bf2`（分支 `feat/p95-p99-query`，HEAD）。
- 任务开始时已有修改：无（`git status --short` 为空，working tree clean）。
- 重叠修改的区分方式：本任务产物集中在 `prometheus/`（新增 rules 文件、`prometheus.yml` 增 `rule_files`、`docker-compose.yml` 挂载 rules）与 `scripts/test-prometheus.sh`（扩展校验），可选轻量同步 `docs/design/flash-sale.md` §11.4；不改 `internal/metrics`、`internal/logic/flashsale`。工作区干净，无既有未提交修改需区分。

## Initial Route

交 Coder（NORMAL）
