# Cleaner Findings

## Review Target

- 任务基线（base）：`7a335cc1b3c5fbb7d9dcafa5e546f9aae163a952`（`feat/flash-sale-v5` 合入 develop 的 merge commit）
- 分支：`chore/verify-metrics-histogram`
- Contract APPROVED target（A1）：`ddc137feedfbeb4a41e6dd74fa6430210e0306bf`
- **Review Target（C1，本次审查的 immutable implementation Evidence Commit）**：`6602531953ef7343f5f33c97dedbf9632bdbcb3f`
- 审查时 HEAD（C2，Coder metadata commit）：`618e055fb7cb79a85fc54acb295ff88a4c897593`

C1 相对基线的完整相关变更（含新增文件）：

| 文件 | 变更 |
|---|---|
| `internal/metrics/metrics.go` | 标签 `["interface","activity","result"]` → `["interface","result"]`；`ObserveOrder/ObserveConsume` 签名移除 `activityID`；删除 `strconv` 依赖 |
| `internal/metrics/metrics_test.go` | 新增（258 行）：标签 schema / 计数 / 桶 / 开关关闭 5 个测试 |
| `internal/logic/flashsale/flashsale.go` | `CreateOrder` defer 调用点移除 `activityID` 参数 |
| `internal/logic/flashsale/consume.go` | `consumeOne` 两处调用点移除 `activityID` 参数 |
| `internal/logic/flashsale/order_outcome_test.go` | 新增：`orderOutcome` 结果映射白盒测试 |
| `internal/logic/flashsale/consume_metrics_test.go` | 新增：消费链路计数口径集成测试（MySQL+Redis） |
| `internal/logic/flashsale/capacity_test.go` | 既有 `TestMetricsReconcile` 标签断言去掉 `activity` 维度 |

设计文档 `docs/design/flash-sale.md` §11.4 在 Contract commit `ddc137f`（Analyst 阶段，先于 C1）同步为「接口/结果」，不在 C1 内。

任务前已有修改的区分：任务基线 `7a335cc` 时 working tree clean，无既有未提交修改；本任务产物集中在上述 7 个文件 + 设计文档。

全局资源：Contract 明确「本任务无需全局资源」，`state.resources.reservations` 为空；C1 未改动 `internal/codes/codes.go` 与 `internal/migrations/sql/*`。三边一致性（Registry ↔ Contract ↔ 实现）无资源可漂移，通过。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（下单耗时被 Histogram 记录） | PASS | `CreateOrder` 以 `defer` 调用 `metrics.ObserveOrder(ctx, orderOutcome(res, err), time.Since(started))`，每次调用恰好观测一次、与结果无关；`orderOutcome` 将结果映射为 `queued/rate_limited/queue_full/gate_rejected/error`。单测 `TestObserveOrderRecordsCountAndDuration`（Counter/Histogram 各 +1、sample_sum 等于真实耗时）与集成测试 `TestMetricsReconcile`（order/queued delta=1）均通过。 |
| AC-002（消费耗时被 Histogram 记录） | PASS | `consumeOne` 每次「出队成功」恰好观测一次：技术失败（事务回滚）与重试路径观测 `error`，终态观测 `success/failed/dead`，「无请求可领取」不观测。集成测试 `TestConsumeMetricsCounting`（空队列→0、撞号技术失败→error+1、成功终态→success+1）与单测 `TestObserveConsumeRecordsCountAndDuration` 均通过。 |
| AC-003（桶分布覆盖实际耗时） | PASS | 桶保持 `[]float64{0.0005,...,10}`（0.5ms~10s）。`TestHistogramBucketsCoverRepresentativeDurations` 用代表性样本（500µs 快路径 / 5ms 典型下单 / 2s 慢路径）断言均落入有界桶而非 `+Inf`。注：为「模拟代表性样本」验证，非真实生产 p95/p99 样本——Contract 已显式标记该真实样本为 UNKNOWN，并决定桶默认保持不变、仅在实测溢出时扩展，当前证据支撑该决定成立。 |
| AC-004（低基数标签、无 ID 标签） | PASS | 两个指标标签集为 `["interface","result"]`，`interface`∈{order,consume}、`result`∈{8 个稳定枚举}；无 `activity`/user_id/order_no。`TestLabelSchemaLowCardinality` 逐条断言每个 metric 除 `interface`/`result` 外无任何其它标签。`Observe*` 签名已无 `activityID`，标签基数无法随业务量增长。 |
| AC-005（设计文档同步） | PASS | `docs/design/flash-sale.md` §11.4 已改为「接口/结果（低基数稳定枚举）」，指标名/桶/计数口径/可观测边界与 Contract、实现三者一致。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `gofmt -l`（7 个变更文件） | PASS | 无输出 |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | 无告警 |
| `go test ./internal/metrics/...` | PASS | 5 个测试全通过（含标签 schema、计数、桶、开关关闭） |
| `go test ./internal/logic/flashsale/...` | PASS | 全包通过（含新增 `TestOrderOutcomeMapping`、`TestConsumeMetricsCounting` 与改造后的 `TestMetricsReconcile`） |
| `go test ./...`（全量） | 部分失败（与本任务无关） | `internal/migrations` 测试会 `DROP TABLE`（含 `admins`/`admin_roles`/`schema_migrations`）重建 schema，与其它包（middleware/auth/controller）并行运行共用同一 `my_shop` 库，产生「表不存在」竞态。`internal/middleware` 单独运行通过（`go test ./internal/middleware/...` → ok），证明为既有测试隔离问题，非本任务引入。 |

## Findings

### CLEAN-001：幂等重放将已终态请求计为 `queued`（P3，预先存在、Out of Scope）

- Severity：P3
- Status：OPEN
- Location：`internal/logic/flashsale/flashsale.go:332` `orderOutcome`（`err == nil` 恒返回 `ResultQueued`）+ `internal/logic/flashsale/flashsale.go:358` `handleIdempotentHit`（返回既有状态、`err == nil`）
- AC / Invariant：INV-021（指标计数与真实请求结果可对账）的 order 维度精度
- Trigger：用户以相同 `idempotencyKey` 重放下单，且该请求已进入 `success`/`failed` 终态（`handleIdempotentHit` 返回既有状态、`err == nil`）
- Actual：`orderOutcome` 只看 `err`，`err == nil` 一律计 `queued`，将「已终态请求的重放」误计为 `queued`
- Expected：重放返回既有终态时，order 维度不新增观测或按实际状态归类（Contract 的 order 结果集 `{queued, rate_limited, queue_full, gate_rejected, error}` 未含 success/failed，属既有协议选择的边界）
- Impact：仅影响幂等重放这一角落场景的 order 计数精度，轻微高估 `queued`；不影响标签基数、埋点调用次数与 consume 维度口径
- Evidence：`orderOutcome` 函数本任务未改动（C1 仅改 defer 调用点移除 `activityID`）；`TestOrderOutcomeMapping` 覆盖「nil err → queued」，未覆盖「nil err + 非 queued 状态」分支
- Required Fix Boundary：本任务 Scope 未要求修正该映射，且涉及 order 结果集协议扩展，超出「低基数标签 + 桶覆盖」范围；由 Owner 决定是否在后续独立任务处理，本任务不阻塞 CLEAN

> 除上述 P3 外，无 P0/P1/P2 Finding。
