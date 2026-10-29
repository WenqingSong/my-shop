# Technical Contract

## Decision Status
APPROVED

## Problem

秒杀 V5 引入的 Prometheus 指标（`flashsale_requests_total` / `flashsale_request_duration_seconds`）当前标签维度为「interface / activity / result」，其中 `activity` 是活动 ID（`strconv.FormatInt(activityID, 10)`），属 ID 型、随活动数量增长的基数标签，不符合可观测最佳实践的低基数要求。任务要求核实下单与消费链路的耗时埋点、桶分布覆盖真实耗时，并修正标签 schema 为低基数（去掉 ID 型标签），同步更新 `docs/design/flash-sale.md` §11.4 并补齐 `internal/metrics` 测试。

## Verified Current Behavior

- VERIFIED：`internal/metrics/metrics.go` 定义 `requestsTotal`（CounterVec）与 `requestDuration`（HistogramVec），标签均为 `[]string{"interface","activity","result"}`；桶 `[]float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}`（秒，覆盖 0.5ms~10s）。
- VERIFIED：结果枚举共 8 个（`queued / success / gate_rejected / rate_limited / queue_full / failed / dead / error`）；order 链路使用 `queued/rate_limited/queue_full/gate_rejected/error`，consume 链路使用 `success/failed/dead/error`。（注：task.md「Relevant Context」写「7 个枚举」与实际 8 个不符，以代码为准。）
- VERIFIED：下单耗时已埋点（`internal/logic/flashsale/flashsale.go` `CreateOrder` 用 `defer` 调用 `metrics.ObserveOrder(ctx, activityID, orderOutcome(res, err), time.Since(started))`）；每次 `CreateOrder` 调用恰好观测一次，与结果无关。
- VERIFIED：消费耗时已埋点（`internal/logic/flashsale/consume.go` `consumeOne`）：技术失败（事务回滚、非终态）观测 `error`；终态观测 `success/failed/dead`；「无请求可领取」不观测。每次出队成功的处理恰好观测一次。
- VERIFIED：`interface` 取值 `order/consume`；`activity` 为活动 ID；`result` 为上述枚举。**未使用 user_id / order_no 标签**，但 `activity` 是 ID 型标签。
- VERIFIED：`ObserveOrder`/`ObserveConsume` 在 `flash_sale.metrics.enabled=false`（默认）时为空操作；`GET /metrics` 仅在开启时挂载（`internal/cmd/cmd.go` `registerMetricsRoute`）。
- VERIFIED：下单路由 `POST /flash-sales/:id/orders`（`internal/cmd/routes_frontend.go`，路径含 `:id`）；消费链路为后台 `goroutine+ticker`（`startFlashSaleConsumeScanner`），无 HTTP 路由模板。
- VERIFIED：`internal/metrics` 无任何测试文件。
- VERIFIED：`docs/design/flash-sale.md` §11.4 现描述标签为「接口/活动/结果」。
- UNKNOWN：真实压测样本下 p95/p99 是否落入现有有界桶（是否 `+Inf` 溢出）；task.md 假设毫秒级，但尚未以样本证实。

## Recommendation

RECOMMENDATION：标签 schema 收敛为 `interface`（order|consume）+ `result`（8 枚举），移除 `activity`（活动 ID）标签；`ObserveOrder`/`ObserveConsume` 签名移除 `activityID` 参数并更新两个埋点调用点；指标名与桶分布默认保持不变，仅当 AC-003 验证证实 p99 溢出 `+Inf` 时才扩展桶上界。

关键取舍：

- 「路由模板」在本库的最简等价物即现有 `interface` 维度：`order` 对应 `POST /flash-sales/:id/orders`（唯一下单路由），`consume` 对应后台消费者（无 HTTP 路由，天然需要合成标签）。不引入字面 GoFrame pattern 字符串（`/flash-sales/:id/orders`）作为标签值：只有一条下单路由时它不带来额外信息，且会耦合路由注册、消费链路无法对齐。
- 移除 `activity` 会丢失「每活动粒度」的指标切片能力（QPS/p95 按活动过滤）。该粒度运维需求可由 §11.5 热点 Key 分析（容量侧）+ MySQL `flash_sale_orders`/`flash_sale_order_requests`（业务事实侧）满足；指标侧坚持低基数最佳实践。
- 指标名不变（`flashsale_requests_total` / `flashsale_request_duration_seconds`），避免破坏 scrape 目标。

## Selected Design

采用 `interface`（order/consume）+ `result`（8 枚举）固定低基数标签，移除 `activity`；保留现有指标名与桶分布，桶分布是否调整由实际观测验证驱动（AC-003）。

- `ObserveOrder(ctx, result, d)`、`ObserveConsume(ctx, result, d)`：移除 `activityID` 参数，内部只以 `interface`/`result` 两个标签 `Inc` + `Observe`。
- 指标名、Namespace、桶 `[]float64{0.0005, ..., 10}` 保持不变。
- 「路由模板」= 现有 `interface` 维度（`order` = 唯一下单路由，`consume` = 后台消费者合成标签），不引入字面 pattern 字符串。
- 不提供通用 Histogram 的单活动 p99/p95 切片；该能力不在本任务 Scope，也不由其它数据源等价替代（见「Interfaces and Data」边界）。

## Interfaces and Data

- 指标名保持不变：`flashsale_requests_total`（CounterVec）、`flashsale_request_duration_seconds`（HistogramVec）。
- 标签集改为 `["interface", "result"]`（移除 `activity`）。
  - `interface` ∈ {`order`, `consume`}。
  - `result` ∈ {`queued`, `success`, `gate_rejected`, `rate_limited`, `queue_full`, `failed`, `dead`, `error`}。
- 函数签名：`ObserveOrder(ctx context.Context, result string, d time.Duration)`、`ObserveConsume(ctx context.Context, result string, d time.Duration)`（移除 `activityID int64`）。
- 埋点调用点同步更新：`internal/logic/flashsale/flashsale.go`（`CreateOrder` 的 defer）、`internal/logic/flashsale/consume.go`（`consumeOne` 技术失败与终态两处）。
- 桶分布默认保持 `[]float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}`；仅当 AC-003 验证证实溢出时扩展上界。
- `docs/design/flash-sale.md` §11.4 标签维度由「接口/活动/结果」改为「接口/结果」。
- 可观测边界：移除 `activity` 后，通用 Histogram **不再提供单活动 p99/p95**；§11.5 热点 Key 分析（容量侧）与 MySQL `flash_sale_orders`/`flash_sale_order_requests`（业务事实侧）只提供活动维度的容量/业务计数，**不能视为单活动耗时指标的等价替代**。如需单活动耗时分布，属本任务 Scope 外的后续独立设计。

## Business Invariants

- INV-021（指标可对账，保持）：`flashsale_requests_total` 的成功/拒绝/失败计数与真实请求结果一致（可与 MySQL 订单数、`status=queued`、拒绝数对账）。移除 `activity` 后该不变量仍按总量成立（对账不再按活动切片）。
- INV-M01（标签低基数）：`flashsale_request_duration_seconds` 与 `flashsale_requests_total` 的标签取值仅为 `interface`（2 值）× `result`（≤8 值）稳定枚举，不含 user_id/order_no/activity_id 等 ID；新增活动/用户/订单不改变标签基数。
- INV-M02（计数口径与真实耗时）：
  - order：每次 `CreateOrder` 调用恰好产生一次 order 观测（`defer`），结果由 `orderOutcome` 映射（`queued/rate_limited/queue_full/gate_rejected/error`）。
  - consume：每次「出队成功」的 `consumeOne` 恰好产生一次 consume 观测；终态观测 `success/failed/dead`，技术失败（事务回滚、`retry_count+1`、非终态）观测 `error`。「无请求可领取」不观测。**计数口径为「领取尝试」而非「请求」**：同一请求经多次技术失败重试会产生多次 `error` 观测（每次领取一次），故 `consume` 计数 = 出队成功处理次数，而非请求数。
  - 观测耗时 = 该次处理真实耗时（非零、非固定值）；`Inc`（Counter）与 `Observe`（Histogram）在同一调用内完成，二者计数一致。

## Failure and Consistency Semantics

- 指标为进程内观测计数（非业务事实），不参与任何事务、缓存或持久化；`Observe*` 不影响业务结果，`flash_sale.metrics.enabled=false` 时为空操作。
- 下单观测在 `defer` 中执行，覆盖成功入队与所有拒绝/失败结果。
- 消费观测在事务提交后（终态）或技术失败回滚后执行；「无请求可领取」不观测——保证 order 维度「每请求一次」、consume 维度「每领取一次」，而非「每轮扫描一次」。
- 技术失败重试不产生额外业务效果（事务回滚、请求保持 `queued`），但每次失败领取都会产生一次 `error` 观测；该计数为观测性事实，不与 MySQL 请求行一一对应。

## Allowed / Forbidden Changes

- 允许：修改 `internal/metrics/metrics.go` 的标签定义与 `Observe*` 签名；更新 `internal/logic/flashsale/` 埋点调用点；新增 `internal/metrics` 测试；更新 `docs/design/flash-sale.md` §11.4。
- 禁止：新增指标类型或新依赖；改动业务不变量 INV-001~005、INV-012~021 及限流/熔断/排队软上限语义；改动非秒杀模块埋点；新增错误码或 migration（本任务无需全局资源，`.agent/workflow.yaml` 声明的 migration_version / error_code_domain 均不涉及）。

## Verification Requirements

- INV-M01 / AC-004 → 单元测试：以 `prometheus/testutil` 断言两个指标的标签集合仅含 `interface`/`result`，无 `activity`；对多个 activityID 观测后标签基数不变。
- INV-M02 / AC-001 / AC-002 → 单元测试：构造下单各结果分支，断言 `order` 的 Histogram 观测次数与 Counter 计数一致、`result` 映射正确、耗时非零；构造消费各结果分支（含技术失败重试多次）断言 `consume` 每次出队成功恰一次观测、`error` 计数等于失败领取次数、终态计数等于 `success/failed/dead` 请求数；断言「无请求可领取」不产生观测。
- AC-003 → 用真实或模拟请求产生样本，断言 p95/p99 落入有界桶（不含 `+Inf` 溢出）；必要时以 `docker compose up -d`（MySQL + Redis）+ 开启 `flash_sale.metrics.enabled` 发起请求、读取 `/metrics` 核对桶计数分布。
- AC-005 → 文档审查：`docs/design/flash-sale.md` §11.4 与 Contract、实现一致。
- 回归：标签变更不破坏 INV-021；通用命令 `gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`。

## Open Risks

- 单活动耗时观测能力缺失：移除 `activity` 后通用 Histogram 不再提供单活动 p99/p95；§11.5 与 MySQL 仅提供容量/业务计数，**非耗时指标的等价替代**，该能力属 Scope 外后续独立设计。
- 既有监控契约：若已有 PromQL/告警/面板引用 `{activity=...}` 会失效；因 `metrics.enabled` 默认 false 且 V5 刚合入，风险低，无需迁移说明。
- 桶分布未经真实压测样本证实；若 p99 溢出 `+Inf`，需扩展上界（契约已预留该调整路径）。

## Owner Decision Record

Owner 决定（2026-10-09）：接受全部三项推荐——采用 `interface` + `result` 固定低基数标签，移除 `activity`，保留现有指标名与 Bucket，并通过实际观测验证桶分布。同时要求明确两点：①通用 Histogram 不再提供单活动 p99，其他数据源不能视为直接等价替代；②明确 HTTP 下单与 Consumer 重试场景的指标计数口径，确保 INV-M02 可验证。上述均已写入本 Contract（见「Interfaces and Data」可观测边界、「Business Invariants」INV-M02、「Failure and Consistency Semantics」）。
