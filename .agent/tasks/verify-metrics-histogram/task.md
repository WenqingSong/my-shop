# Task: 核实现有 Histogram 指标（标签与桶分布）

## Goal

核实并修正秒杀链路的 Prometheus Histogram 指标，使其符合可观测最佳实践：下单接口与排队消费链路的请求耗时均被 `flashsale_request_duration_seconds` 正确记录、直方图桶分布覆盖真实耗时范围、标签维度为低基数（基于路由模板/接口，而非用户 ID、订单 ID、活动 ID 等 ID 型高基数标签）；发现偏差时修复并补充回归测试，且与 `docs/design/flash-sale.md` 保持一致。

## Scope

- 核实 `internal/metrics/metrics.go` 的指标定义（Counter + Histogram）与请求耗时埋点调用点（`ObserveOrder` / `ObserveConsume`）。
- 确认秒杀下单接口耗时确实被 Histogram 记录（interface=order 路径，含成功入队与各类拒绝/失败结果）。
- 确认排队消费链路耗时确实被 Histogram 记录（interface=consume 路径）。
- 核实直方图桶分布是否覆盖下单与消费链路的实际耗时（不落入 `+Inf` 溢出桶、慢路径有可分辨桶）。
- 修正标签维度：以低基数路由模板/接口作为标签，去除或替换 `activity`（活动 ID）等 ID 型标签；确保不使用用户 ID、订单 ID。
- 为指标埋点与标签 schema 补充/更新测试（当前 `internal/metrics` 无测试）。
- 若标签 schema 变更，同步 `docs/design/flash-sale.md` §11.4。

## Out of Scope

- 不引入新指标类型或新依赖（继续使用 `prometheus/client_golang`）。
- 不改变秒杀业务不变量（INV-001~005、INV-012~021）、限流/熔断/排队软上限语义与 MySQL 事实来源。
- 不把秒杀指标抽象为全站通用指标/可观测平台（保持秒杀域聚焦）。
- 不做 Prometheus / Grafana 部署、告警规则、采集面板与抓取配置。
- 不改动普通订单、SKU、商品、IAM 等其它模块的埋点。

## Design Impact

Design Impact: UPDATE
Design Artifact: docs/design/flash-sale.md（§11.4 指标标签维度）

> 判定说明：当前标签 schema 为「接口/活动/结果」并已写入 `docs/design/flash-sale.md` §11.4；本任务要求改为「路由模板 + 低基数维度」。若 Analyst 调查后确认 `activity` 标签可保留或无需改动，则本 Design Impact 可在 Contract 中校正为 `NONE`（校正改变 Scope 时回 Owner/TaskBuilder）。

## Acceptance Criteria

- [ ] AC-001（下单耗时被 Histogram 记录）：给定一次秒杀下单请求（成功入队、限流拒绝、队列满拒绝、闸门业务拒绝、技术错误等任一结果），当请求完成时，`flashsale_request_duration_seconds` 在 interface=order 维度产生一次观测，观测次数与 `flashsale_requests_total` 的 order 计数一致，且耗时值为该请求的真实耗时（非固定/零值）。
- [ ] AC-002（消费耗时被 Histogram 记录）：给定一次排队消费处理（成功落单、业务失败终态、死信、技术错误等任一结果），当处理完成时，`flashsale_request_duration_seconds` 在 interface=consume 维度产生一次观测，观测次数与 consume 计数一致。
- [ ] AC-003（桶分布覆盖实际耗时）：直方图桶的上下界覆盖下单与消费链路的实际耗时范围——实际 p95/p99 耗时落在有界桶内而非 `+Inf` 桶；对慢路径（如熔断快速失败、DB 超时）仍有可分辨的桶。
- [ ] AC-004（低基数标签、无 ID 标签）：指标标签维度仅使用低基数、稳定枚举（路由模板/接口/结果等），不使用用户 ID、订单 ID、活动 ID 等 ID 型标签；新增活动/用户/订单不会导致标签基数随业务量无界增长。具体标签 schema 以 APPROVED Contract 为准。
- [ ] AC-005（设计文档同步）：若标签 schema 变更，`docs/design/flash-sale.md` §11.4 与 APPROVED Contract、最终实现三者一致。

## Relevant Context

已核实事实：

- `internal/metrics/metrics.go` 定义了两个指标：`requestsTotal`（CounterVec，`flashsale_requests_total`）与 `requestDuration`（HistogramVec，`flashsale_request_duration_seconds`），标签均为 `interface / activity / result`；桶为 `[]float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}`（秒，覆盖 0.5ms ~ 10s）。
- 下单耗时已埋点：`internal/logic/flashsale/flashsale.go` 的 `CreateOrder` 用 `defer` 调用 `metrics.ObserveOrder(ctx, activityID, orderOutcome(res, err), time.Since(started))`，`orderOutcome` 将结果映射为 `queued/rate_limited/queue_full/gate_rejected/error`。`ObserveOrder` 内部 `requestDuration.WithLabelValues(InterfaceOrder, activity, result).Observe(d.Seconds())`。
- 消费耗时已埋点：`internal/logic/flashsale/consume.go` 在处理终态与失败分支调用 `metrics.ObserveConsume(...)`（`success/failed/dead/error`）。
- 标签现状：`interface` 取值 `order/consume`；`activity` 取值为活动 ID（`strconv.FormatInt(activityID, 10)`）；`result` 为 7 个枚举。**未使用用户 ID / 订单 ID 标签**，但 `activity` 是 ID 型、中等基数标签（随活动数量增长），非「路由模板」。
- 指标开关 `flash_sale.metrics.enabled` 默认 false，`GET /metrics` 仅在开启时挂载（`internal/cmd/cmd.go` 的 `registerMetricsRoute`）。
- 原设计（flash-sale-v5 APPROVED Contract 与 `docs/design/flash-sale.md` §11.4）明确标签维度为「接口/活动/结果」，覆盖下单接口与排队消费链路；`internal/metrics` 目前**无任何测试**。
- 秒杀下单路由为 `POST /flash-sales/:id/orders`（`internal/cmd/routes_frontend.go`），路径含 `:id` 参数；消费链路为后台 goroutine+ticker 消费者，**无 HTTP 路由模板**。

Assumption：

- 下单同步路径耗时量级为毫秒级（Redis 限流 + Lua 闸门 + MySQL 入队），消费路径为单事务落单（毫秒级），当前桶（0.5ms ~ 10s）大概率覆盖 p95/p99；但需以真实样本或压测数据核实，尤其确认慢尾是否溢出 `+Inf`。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 「路由模板」在本代码库中的具体含义与取值：是 GoFrame 路由 pattern（如 `/flash-sales/:id/orders`），还是沿用/精简现有 `interface`（order/consume）语义即可；消费链路无路由模板，其标签如何处理。
- 是否必须去掉 `activity`（活动 ID）标签，以及去掉后如何满足「每活动粒度监控」的运维需求（该粒度是否可接受丢失）。

## Verification

- AC-001 / AC-002 → 单元测试：构造下单/消费各结果分支，断言 Histogram 观测次数与结果映射正确、耗时非零；结合 `prometheus` testutil 断言标签值。
- AC-003 → 用真实或模拟请求产生耗时样本，断言样本落入桶区间（不含 `+Inf` 溢出）；必要时以 `docker compose up -d`（MySQL + Redis）+ 开启 `flash_sale.metrics.enabled` 发起请求、读取 `/metrics` 核对桶计数分布。
- AC-004 → 读取 `/metrics` 输出，断言 `flashsale_request_duration_seconds` 的标签集合仅含低基数维度，不含 activity/user/order ID；新增活动后标签基数不增长。
- AC-005 → 文档审查：`docs/design/flash-sale.md` §11.4 与 Contract、实现一致。
- 回归：标签变更不得破坏 INV-021（指标计数与真实请求结果可对账）。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`。

## Complexity

COMPLEX

原因：本任务核心是标签 schema 的设计选择——是否以及如何把「接口/活动/结果」改为「路由模板 + 低基数维度」，涉及观测协议（`/metrics` 标签契约）的修改，直接影响既有监控查询/告警与每活动粒度监控能力，属「公开协议选择」且多个方案（保留 activity / 替换为路由模板 / 去维度）会产生不同可观测性结果；需 Analyst 固化 Contract 后交 Owner 确认。

## Analyst Questions

1. 最终标签 schema：去掉 `activity`（活动 ID）标签、仅保留低基数路由模板/接口 + 结果，还是保留活动维度但以其它低基数方式表达？「路由模板」在本代码库中的具体取值是什么（GoFrame 路由 pattern `/flash-sales/:id/orders`，还是沿用现有 `interface` 的 order/consume 语义）。
2. 消费链路（后台 goroutine，非 HTTP）无路由模板，其 `interface=consume` 标签如何处理，是否需单独命名约定。
3. 去掉 `activity` 维度后，「每活动粒度监控」的需求是否可接受丢失；若需保留，给出低基数替代方案（如活动维度上限/去重策略）并评估基数上界。
4. 变更标签 schema 是否破坏既有监控契约（PromQL 查询/告警/面板依赖 `{activity=...}`），是否需考虑兼容或迁移说明。
5. 桶分布是否需基于实际耗时样本调整（当前 0.5ms ~ 10s 是否覆盖 p95/p99、慢尾是否溢出 `+Inf`、是否需要更细/更粗分桶）。
6. 本任务是否需要新增全局资源（判断：预计不需要新错误码域或 migration；如 Analyst 判断需要，须在 Contract 中声明资源类型与语义需求）。

## Review Baseline

- Base commit：`7a335cc1b3c5fbb7d9dcafa5e546f9aae163a952`（分支 `chore/verify-metrics-histogram`，为 `feat/flash-sale-v5` 合入 develop 的 merge commit）。
- 任务开始时已有修改：无（`git status --short` 为空，working tree clean）。
- 重叠修改的区分方式：本任务产物集中在 `internal/metrics/`、`internal/logic/flashsale/`（埋点调用点）、`docs/design/flash-sale.md`（如标签变更）及对应测试；当前工作区干净，无既有未提交修改需区分。

## Initial Route

交 Analyst（COMPLEX）
