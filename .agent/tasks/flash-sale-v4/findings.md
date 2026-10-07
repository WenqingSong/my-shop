# Cleaner Findings

## Review Target

- 任务：flash-sale-v4（秒杀 V4 故障恢复）
- 分支：`feat/flash-sale-v4`
- 基线（Base commit）：`1543ecb3c97d55e3bfe92ba1de548c8d183e766a`
- 实现证据（C1，`state.review.target`）：`e2a4617a601b87cd5dd6a80ea448ce61bff9716e`（commit `e2a4617`，含 V4 实现 + 修复）
- 当前 HEAD（C2 metadata）：`c5fd6c1`（仅 `state.yaml` `review.target` 前移，无生产/测试改动）
- 工作区：`git status --short` 为空（clean）。

复审对象 = 首轮 C1（`ef200bd`）+ 修复提交 `e2a4617`（fix: 补崩溃重投幂等测试与 reason 长度校验、对齐结束收敛文档）的最终实现。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（入队失败补偿与崩溃恢复） | PASS | `enqueue` 失败 → `convergeStockAndMarkers`（幂等 `SET remaining` + `DEL` 标记）；孤儿标记自愈。测试 `TestConvergeStockIdempotentAndClearsSoldout`、`TestIdempotentOrphanMarkerSelfHeal`、`TestAlreadyPurchasedOrphanMarkerSelfHeal`（`-race`）通过。 |
| AC-002（消费落单失败补偿） | PASS | `consumeOne` 终态失败提交后 `convergeStockAndMarkers`。测试 `TestConsumeRetryThenDeadLetter`（dead 不建单不扣库存）、`TestFlashSaleOrderFailures`（失败不建单）。 |
| AC-003（处理中崩溃重投幂等） | PASS | 修复新增 `TestConsumeCrashBeforeCommitRedeliversOnce`：经 `consumeBeforeCommitHook` 注入提交前故障 → 事务回滚后请求仍 `queued`、无订单、不扣库存；清除故障点重投后订单数/`sold` 各恰一次。可区分「事务边界被破坏」的错误实现（`-race` 通过）。 |
| AC-004（补偿只执行一次） | PASS | `convergeStock` 幂等 `SET`。测试 `TestConvergeStockIdempotentAndClearsSoldout`、`TestConvergeStockCountsInflightQueued`、`TestCreateOrderIdempotencyConflictCompensatesOnce`。 |
| AC-005（活动结束后对账） | PASS | `convergeEndedActivity` + `ReconcileCache` 覆盖已结束活动终态收敛。测试 `TestConvergeEndedActivity`、`TestConvergeEndedActivityKeepsInflight`、`TestReconcileCacheConvergesEndedActivity`。 |
| AC-006（服务重启后 pending 恢复） | PASS | 队列为 MySQL 持久化、消费者无状态；「崩溃残留不重复」由 AC-003 同一事务回滚机制保证，现已由 `TestConsumeCrashBeforeCommitRedeliversOnce` 覆盖；退避到期后继续消费由 `TestConsumeRetryThenDeadLetter` 覆盖。 |
| AC-007（人工修复入口 + 审计） | PASS | `RepairRequest` 条件更新 `WHERE status=dead` + `RowsAffected` 并发安全 + 审计同事务；路由 `RequirePermission("flash_sale:repair")`。测试 `TestRepairRequestDeadToQueuedAndAudit`、`TestFlashSaleRepairAndAudit`（403/401/12008 拒绝无副作用）、`TestRepairRequestRejectsOverlongReason`（超长 reason 400）。 |
| AC-008（长期设计） | PASS | `docs/design/flash-sale.md` 已更新 V4 故障恢复模型，且结束收敛缓存清理描述已与实现对齐（CLEAN-002 关闭）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 退出码 0，无输出。 |
| `go vet ./...` | PASS | 退出码 0，无输出。 |
| `go test -race -p 1 ./internal/logic/flashsale/...` | PASS | `ok`（含新增崩溃重投/超长 reason 测试）。 |
| `go test -race -p 1 -run 'TestConsumeCrashBeforeCommitRedeliversOnce\|TestRepairRequestRejectsOverlongReason\|TestRepairRequestDeadToQueuedAndAudit' -v ./internal/logic/flashsale/...` | PASS | 三个目标测试逐个 `PASS`。 |
| `go test -race -p 1 -run 'TestFlashSale\|TestRouteTable' ./internal/cmd/...` | PASS | `ok`（真实路由 + 并发不超卖 `-race`）。 |
| `go test -p 1 ./...`（全量） | PASS | 全部包 `ok`，无失败。 |
| 全局资源三边一致性 | PASS | migration `20261001000018`：`origin/develop` Registry `RESERVED`（flash-sale-v4）↔ Contract ↔ 实现一致。错误码 `12008`：位于 `12000-12999` flash-sale-v1 `RESERVED` 域内，Contract 声明复用、实现 `CodeFlashSaleRequestNotRepairable=12008`。三边一致。 |

## Findings

### CLEAN-001：AC-003/AC-006 缺少「崩溃重投/重启恢复」的专门故障注入测试

- Severity：P2
- Status：CLOSED
- 修复验证：新增 `internal/logic/flashsale/consume.go` 的 `consumeBeforeCommitHook`（生产恒 nil 的测试钩子，不改事务边界）+ `TestConsumeCrashBeforeCommitRedeliversOnce`。测试注入提交前 error → 断言回滚后请求仍 `queued`、无订单、不扣库存 → 清除故障点重投 → 断言订单数/`sold` 各恰增加一次。`go test -race -run TestConsumeCrashBeforeCommitRedeliversOnce` 通过，可区分「建单/扣库存与状态更新被拆成多个已提交事务」的错误实现。结论：AC-003/AC-006 由 NOT_VERIFIED 转为 PASS。

### CLEAN-002：设计文档/Contract 与实现在「结束活动缓存清理」key 范围上轻微漂移

- Severity：P3
- Status：CLOSED
- 修复验证：`docs/design/flash-sale.md` §5.5 已改为与实现一致：「失效活动域缓存（DEL activity 元数据/soldout 标记 + 置 null 标记；stock key 保留 remaining 供观测、靠 TTL 过期；bought/idem 标记靠 TTL 过期）」。文档 ↔ 实现一致。

### CLEAN-003：RepairRequest 未校验 `reason` 长度上限

- Severity：P3
- Status：CLOSED
- 修复验证：`internal/logic/flashsale/repair.go` 新增 `utf8.RuneCountInString(reason) > maxRepairReasonLen(255)` 前置校验，返回 `1001`（400）。新增 `TestRepairRequestRejectsOverlongReason` 通过。
