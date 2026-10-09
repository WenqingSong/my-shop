# Delivery Verification

## Milestone and Target
- Milestone：秒杀 Prometheus Histogram 指标标签收敛为低基数「接口/结果」+ 桶分布覆盖真实耗时
- Delivery Target（review.target / C1）：`6602531953ef7343f5f33c97dedbf9632bdbcb3f`
- Cleaner Review Target：`6602531953ef7343f5f33c97dedbf9632bdbcb3f`
- Target Match：YES
- feature_head（本次实际验证的 feature snapshot）：`dcbefb82e31b57fab6fc7cd4d2ead4c28de33042`
- develop_base（验证时 origin/develop）：`7a335cc1b3c5fbb7d9dcafa5e546f9aae163a952`

> C1（review.target）之后的变更仅为 `.agent/tasks/verify-metrics-histogram/` 下的 review/owner 元数据
> （findings.md / core-logic.md / owner-decision.md / state.yaml），无生产代码变化；本次验证的生产代码即 CLEAN 对象。

## Environment
- OS：Linux（linux/amd64）
- Go：1.24.1（go.mod 要求 1.23.0）
- MySQL：8.0（docker，`127.0.0.1:3306`，healthy）
- Redis：7-alpine（docker，`127.0.0.1:6379`，healthy）
- Docker 29.6.2 / Compose v5.3.1
- Migration：`20261001000018`（dirty=false）
- 时区差异：MySQL `NOW()`=UTC，Go 进程=+08:00；冒烟测试以宽时间窗规避（非交付缺陷，属环境差异）
- 配置来源：`manifest/config/config.yaml` 开发默认值 + 环境变量覆盖（`FLASH_SALE_METRICS_ENABLED=true` 等），无 Secret 记录

## Verification
| Check | Result | Evidence |
|---|---|---|
| Build | PASS | `go build ./...` exit 0；`go build -o bin/my-shop .` exit 0 |
| gofmt | PASS | `gofmt -l`（7 个变更文件）无输出 |
| go vet | PASS | `go vet ./...` exit 0 |
| Unit（metrics） | PASS | `go test ./internal/metrics/...` 5 测试全通过（标签 schema / 计数 / 桶 / 开关关闭） |
| Integration（flashsale） | PASS | `go test ./internal/logic/flashsale/...` ok（含 `TestConsumeMetricsCounting` 真实 MySQL+Redis） |
| Real HTTP（cmd） | PASS | `go test ./internal/cmd/ -run 'TestFlashSaleV5...'` 真实下单 → `/metrics` 含 `result="queued"` |
| Runtime Smoke | PASS | 真实二进制启动 → 注册/登录 → 下单(queued) → 后台消费 → `/metrics` 与 MySQL 核对（见下） |
| 全量 `go test ./...` | 见「Not Executed」 | 存在既有 migration 隔离问题，与本里程碑无关 |

### Runtime Smoke 证据（真实运行）
- 下单接口 `POST /flash-sales/37/orders` → HTTP 200，`data.status="queued"`。
- `/metrics` 观测结果（本次运行，真实样本）：
  - `flashsale_request_duration_seconds{interface="order",result="queued"}` count=1，sum≈4.4ms；
  - `flashsale_request_duration_seconds{interface="order",result="gate_rejected"}` count=1，sum≈0.24ms（首次时间窗误判的拒绝请求）；
  - `flashsale_request_duration_seconds{interface="consume",result="success"}` count=2，sum≈130ms（1 条遗留 + 1 条本次下单）。
  - 每个序列 `flashsale_requests_total` == `flashsale_request_duration_seconds_count`（Counter 与 Histogram 成对）。
- 标签维度全集：仅 `interface` ∈ {order,consume} × `result` ∈ {queued,gate_rejected,success,...} + 标准 `le` 桶标签；**无 `activity` / `user_id` / `order_no`**。
- 桶分布（无 `+Inf` 溢出）：queued 4.4ms 落入 `le="0.005"`、consume ~60ms 落入 `le="0.1"`、rejected 0.24ms 落入 `le="0.0005"`；各序列 `le="+Inf"` 计数与最大有界桶相同。
- MySQL 最终数据：`flash_sale_orders`=1、`flash_sale_order_requests.status=success`、`flash_sale_activity_skus.sold=1/10`；无超卖、无重复。

## Acceptance Evidence
- AC-001（下单耗时被 Histogram 记录）→ PASS：每次 `CreateOrder` 恰好一次 order 观测（含拒绝分支），耗时非零真实。
- AC-002（消费耗时被 Histogram 记录）→ PASS：真实消费链路产生 consume 观测，计数与 Counter 一致。
- AC-003（桶分布覆盖实际耗时）→ PASS：真实样本均落入有界桶，无 `+Inf` 溢出。
- AC-004（低基数标签、无 ID 标签）→ PASS：真实 `/metrics` 输出仅 `interface`/`result`（+`le`），无 activity/user/order ID。
- AC-005（设计文档同步）→ PASS：`docs/design/flash-sale.md` §11.4 与 Contract、实现三者一致。
- INV-M01（标签低基数）→ PASS（运行时验证）。
- INV-M02（计数口径 + 真实耗时）→ PASS（Counter==Histogram、耗时非零）。
- INV-021（指标可对账）→ PASS（1 queued → 1 订单、sold=1；拒绝请求无副作用）。

## Not Executed
| Check | Reason | Risk |
|---|---|---|
| 全量 `go test ./...`（一次通过） | `internal/migrations` 测试会 DROP TABLE（`admins`/`permissions`/`schema_migrations`）重建 schema，与并行包（middleware/auth/controller）共用 `my_shop` 库产生「表不存在」竞态；此为 Cleaner 已记录的既有隔离问题，与本里程碑无关。已执行一次复现确认，随后 `migrate up` 恢复 schema 并重新 seed 超级管理员/权限 | 低（相关包 metrics/flashsale/cmd 均已独立通过；不阻塞本里程碑结论） |

## Remaining Risks
- CLEAN-001（P3，预先存在、Out of Scope）：幂等重放已终态请求被 `orderOutcome` 计为 `queued`，order 维度计数轻微高估；由 Owner 决定是否后续处理。
- 生产级压测 p95/p99 未实测：本验证以毫秒级真实样本确认桶分布不溢出；Contract 已将该真实样本标记为 UNKNOWN，并预留「实测溢出时扩展桶上界」的调整路径。
- 单活动 p99/p95 切片能力已移除（by design，Scope 外后续独立设计）。

## Result
PASS
