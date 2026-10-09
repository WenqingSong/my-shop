# Core Logic 验证卡

本任务核心是把秒杀 Prometheus 指标标签从「接口/活动/结果」收敛为低基数「接口/结果」，并保证下单/消费链路的耗时埋点口径正确。Owner 需要理解并亲自验证的两条机制如下。

## CL-001：指标标签必须低基数（INV-M01）

- Owner 需要理解：`flashsale_request_duration_seconds` / `flashsale_requests_total` 的标签维度只能是 `interface`（order|consume）× `result`（≤8 个稳定枚举），绝不能带 `activity_id`/`user_id`/`order_no` 等 ID 型标签，否则新增活动/用户/订单会让标签基数随业务量无界增长、耗尽 Prometheus 时序存储与查询性能。
- 生产代码：`internal/metrics/metrics.go` 第 43~56 行（两个 `prometheus.NewCounterVec/NewHistogramVec` 的 `[]string{"interface", "result"}` 标签定义）与第 71~87 行（`ObserveOrder`/`ObserveConsume` 以固定 `InterfaceOrder`/`InterfaceConsume` + `result` 调用 `WithLabelValues`）。
- 关键测试：`internal/metrics/metrics_test.go` 的 `TestLabelSchemaLowCardinality`（逐条断言每个 metric 除 `interface`/`result` 外无任何其它标签，且 `interface` 取值属于稳定枚举）。
- 基线验证：`go test ./internal/metrics/... -run TestLabelSchemaLowCardinality -v` → PASS。
- 可选 Mutation：把两个指标的标签改回 `[]string{"interface", "activity", "result"}`，并同步在 `ObserveOrder`/`ObserveConsume` 里 `WithLabelValues(InterfaceOrder, <activity>, result)` 传第三个值。
- 预期失败：`TestLabelSchemaLowCardinality` 必须失败——它发现每个 metric 出现了非 `interface`/`result` 的 `activity` 标签（`unexpected label "activity"`）。
- 恢复确认：还原为 `[]string{"interface", "result"}` 后再次运行同一命令 → 恢复 PASS。

## CL-002：每次下单/消费恰好观测一次且记录真实耗时（INV-M02）

- Owner 需要理解：order 维度每次 `CreateOrder` 恰好一次观测、consume 维度每次「出队成功」恰好一次观测，且同一调用内 `Counter`(Inc) 与 `Histogram`(Observe) 必须同时发生、耗时取真实 `time.Since`——否则指标无法与 MySQL 订单数/queued 数/拒绝数对账（INV-021），或 p95/p99 失真。
- 生产代码：`internal/logic/flashsale/flashsale.go:260-264`（`CreateOrder` 的 `defer` 用 `orderOutcome(res, err)` + `time.Since(started)` 观测）；`internal/logic/flashsale/consume.go:127-145`（`consumeOne` 在事务回滚或提交后各观测一次，`!processed` 不观测）；`internal/metrics/metrics.go:71-87`（`Observe*` 内 `Inc` + `Observe` 成对执行）。
- 关键测试：
  - `internal/metrics/metrics_test.go` 的 `TestObserveOrderRecordsCountAndDuration`（Counter delta=1、Histogram sample_count delta=1、sample_sum≈真实耗时）。
  - `internal/logic/flashsale/consume_metrics_test.go` 的 `TestConsumeMetricsCounting`（空队列不观测、撞号技术失败 error+1、成功终态 success+1，真实 MySQL+Redis 环境）。
- 基线验证：`go test ./internal/metrics/... -run TestObserveOrderRecordsCountAndDuration -v` 与 `go test ./internal/logic/flashsale/... -run TestConsumeMetricsCounting -v` → 均 PASS。
- 可选 Mutation：在 `ObserveOrder` 里删掉 `requestDuration.WithLabelValues(...).Observe(d.Seconds())` 这一行（只保留 Counter 的 `Inc`）。
- 预期失败：`TestObserveOrderRecordsCountAndDuration` 必须失败——Histogram `sample_count` delta 变为 0，违反「Counter 与 Histogram 计数一致」断言。
- 恢复确认：还原被删行后再次运行同一命令 → 恢复 PASS。
