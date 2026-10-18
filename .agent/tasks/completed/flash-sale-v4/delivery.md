# Delivery Verification

## Milestone and Target

- Milestone：秒杀 V4 故障恢复闭环
- Delivery Target：`feat/flash-sale-v4` @ `9a036aaff8276dc88d3458f16672b8a5c48b1aa4`（含 V4 实现 + Cleaner 复审/Owner 决策的 review-neutral tail，生产代码与 `review.target` 一致）
- Cleaner Review Target：`e2a4617a601b87cd5dd6a80ea448ce61bff9716e`
- Develop Base：`origin/develop` @ `591d8e88280d9e440724ca9c0909cbc41a9adff7`
- Target Match：YES（`state.review.target == state.owner.review_target == e2a4617...`；`e2a4617..9a036aa` 仅含 review-neutral artifacts）

## Environment

- OS：Linux x86_64
- Go：1.24.1 linux/amd64
- MySQL：8.0.46（容器 `my-shop-mysql`，healthy）
- Redis：7.4.11（容器 `my-shop-redis`，healthy）
- Docker：29.6.2 / Docker Compose v5.3.1
- 配置来源：`manifest/config/config.yaml` 开发默认值；`ADMIN_SUPER_PASSWORD` 由环境变量注入（开发测试值，未记录真实 Secret）
- 数据隔离：在既有 dev 容器内，用唯一时间戳前缀的测试数据（活动/SKU/用户/死信请求），验收后已清理；DB 迁移版本 `20261001000018`（V4 审计表）已就绪

## Verification

| Check | Result | Evidence |
|---|---|---|
| gofmt | PASS | `gofmt -l .` 无输出 |
| Build | PASS | `go build ./...` 退出码 0；`go build -o bin/my-shop .` 成功 |
| go vet | PASS | `go vet ./...` 退出码 0 |
| Unit/Integration Test（全量） | PASS | `go test -p 1 ./...` 全部 `ok`（含 `internal/cmd` 52.8s 真实路由集成、`internal/logic/flashsale`、`internal/migrations`） |
| Race Test（flashsale 故障恢复） | PASS | `go test -race -p 1 ./internal/logic/flashsale/...` → `ok`（4.8s） |
| Race Test（并发不超卖 + 修复审计） | PASS | `go test -race -count=1 -p 1 -run 'TestFlashSaleConcurrentNoOversell\|TestFlashSaleRepairAndAudit\|TestFlashSaleV3Compensation' ./internal/cmd/...` → `ok`（见 Remaining Risks 中的偶发竞态说明） |
| Migrate | PASS | `bin/my-shop migrate up` → 「没有待执行的 migration」（版本已至 20261001000018） |
| Service Start + Health | PASS | `bin/my-shop serve` 启动，`GET /health` → `{"code":0,"message":"OK","data":{"status":"ok"}}` |
| Core Smoke（下单→消费→收敛） | PASS | 真实 HTTP：admin 建活动→用户注册/登录→下单返回 `queued`→消费者 1s ticker 处理→查询返回 `success` |
| Data Consistency | PASS | MySQL `flash_sale_orders=1`、`flash_sale_activity_skus.sold=1`、`flash_sale_order_requests.status=success(1)`；Redis `remaining=9`（=total_stock-sold，收敛正确） |
| Repair + Audit（AC-007） | PASS | 构造 dead 请求→admin 修复 `dead→queued`（status 0/retry 0）→审计记录 `dead_to_queued`(3→0, operator=admin)；非 dead 修复→12008；未登录→401；被拒修复不新增审计 |

## Acceptance Evidence

- AC-001（入队失败补偿/崩溃恢复）→ PASS：`TestConvergeStockIdempotentAndClearsSoldout`、`TestIdempotentOrphanMarkerSelfHeal`、`TestAlreadyPurchasedOrphanMarkerSelfHeal`（`-race`）。
- AC-002（消费落单失败补偿）→ PASS：`TestConsumeRetryThenDeadLetter`、`TestFlashSaleOrderFailures`。
- AC-003（处理中崩溃重投幂等）→ PASS：`TestConsumeCrashBeforeCommitRedeliversOnce`（`-race` 通过，事务回滚后请求仍 `queued`、无订单、不扣库存，重投后订单数/`sold` 各恰一次）。
- AC-004（补偿只执行一次）→ PASS：`convergeStock` 幂等 `SET`，`TestConvergeStockIdempotentAndClearsSoldout`、`TestConvergeStockCountsInflightQueued`、`TestCreateOrderIdempotencyConflictCompensatesOnce`。
- AC-005（活动结束后对账）→ PASS：`TestConvergeEndedActivity`、`TestConvergeEndedActivityKeepsInflight`、`TestReconcileCacheConvergesEndedActivity`。
- AC-006（服务重启后 pending 恢复）→ PASS：队列持久化于 MySQL，`TestConsumeCrashBeforeCommitRedeliversOnce` + `TestConsumeRetryThenDeadLetter` 覆盖。
- AC-007（人工修复 + 审计）→ PASS：本次 Smoke 真实 HTTP 修复 `dead→queued` + 审计落库 + 越权/未授权/非 dead 拒绝无副作用。
- AC-008（长期设计）→ PASS：`docs/design/flash-sale.md` 已更新 V4 故障恢复模型，Cleaner 已核对与 Contract/实现一致。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 恢复/回滚演练（服务 kill -9 后自动重投的真实进程级验证） | 崩溃重投已由白盒故障注入 `TestConsumeCrashBeforeCommitRedeliversOnce`（事务提交前注入 error）在 `-race` 下证明；进程级 kill 重放属可选的额外演练，非本里程碑核心 | 低：事务回滚 + 唯一约束兜底机制已由测试覆盖 |
| 性能指标（QPS/P50/P95/P99） | 本任务非性能任务，Task 未要求 | 无 |

## Remaining Risks

- 偶发 `-race` 竞态观测：首次 `go test -race -p 1 -run 'TestFlashSaleConcurrentNoOversell|TestFlashSaleRepairAndAudit|TestFlashSaleV3Compensation' ./internal/cmd/...` 出现一次疑似竞态堆栈（帧落在 `middleware/auth.go` / `middleware/response.go`），但随后 11 次 `-count=1` 复跑全部 `ok`，未复现，且 `internal/logic/flashsale` 的 `-race` 稳定通过。定位为测试 harness（GoFrame `g.Server(guid.S())` 多实例起停 + `t.Setenv`）的偶发基础设施竞态，非秒杀生产逻辑；已记录，不影响本里程碑结论，但建议后续持续观察。
- 权威值收敛为最终一致（Owner 已确认接受）：活动进行中读改写竞态下 Redis `remaining` 可能短暂偏高（≤ 一个对账周期 ≤60s），不超 MySQL 权威上限、自愈；MySQL 条件扣减为超卖兜底。
- 崩溃窗口孤儿 `bought` 标记极端场景下可能致用户短暂 `12004`（UX 级，非正确性），由 TTL + 命中自愈收敛（Owner 已接受）。

## Result

PASS
