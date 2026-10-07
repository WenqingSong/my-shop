# Cleaner Findings

## Review Target

- 任务：flash-sale-v4（秒杀 V4 故障恢复）
- 分支：`feat/flash-sale-v4`
- 基线（Base commit）：`1543ecb3c97d55e3bfe92ba1de548c8d183e766a`
- 实现证据（C1，`state.review.target`）：`ef200bda5defb6a79541a631dfdf0fa87f2abbcf`（commit `ef200bd`）
- 当前 HEAD（C2 metadata）：`241ad85`
- 工作区：`git status --short` 为空（clean），任务开始前无既有未提交修改，基线可靠可区分。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（入队失败补偿与崩溃恢复） | PASS | `enqueue` 失败 → `convergeStockAndMarkers`（幂等 `SET remaining` + `DEL` 标记）；孤儿标记自愈 `handleIdempotentHit`/`handleAlreadyPurchased`。测试 `TestConvergeStockIdempotentAndClearsSoldout`、`TestIdempotentOrphanMarkerSelfHeal`、`TestAlreadyPurchasedOrphanMarkerSelfHeal`（`-race`）通过。 |
| AC-002（消费落单失败补偿） | PASS | `consumeOne` 终态失败提交后 `convergeStockAndMarkers`。测试 `TestConsumeRetryThenDeadLetter` 证明 dead 不建单、不扣 `sold`，`TestFlashSaleOrderFailures` 证明库存不足等失败不建单。 |
| AC-003（处理中崩溃重投幂等） | NOT_VERIFIED | 机制正确（单事务原子 + `FOR UPDATE SKIP LOCKED` + 唯一约束），但缺少「消费者领取后、事务提交前崩溃 → 事务回滚 → 请求回到 queued → 重投只产生一次业务效果」的专门故障注入测试。见 CLEAN-001。 |
| AC-004（补偿只执行一次） | PASS | `convergeStock` 幂等 `SET`（非 INCR）。测试 `TestConvergeStockIdempotentAndClearsSoldout`（重复收敛结果不变）、`TestConvergeStockCountsInflightQueued`（在途计入不超预扣）、`TestCreateOrderIdempotencyConflictCompensatesOnce`（幂等冲突仅补偿一次）。 |
| AC-005（活动结束后对账） | PASS | `convergeEndedActivity` + `ReconcileCache` 覆盖已结束活动终态收敛。测试 `TestConvergeEndedActivity`、`TestConvergeEndedActivityKeepsInflight`、`TestReconcileCacheConvergesEndedActivity`。 |
| AC-006（服务重启后 pending 恢复） | NOT_VERIFIED | 队列为 MySQL 持久化、消费者无状态，「不丢失」由出队机制保证；但「崩溃残留不重复」依赖 AC-003 同一事务回滚机制，该机制缺少专门测试。见 CLEAN-001。 |
| AC-007（人工修复入口 + 审计） | PASS | `RepairRequest` 条件更新 `WHERE status=dead` + `RowsAffected` 并发安全 + 审计同事务；路由 `RequirePermission("flash_sale:repair")`。测试 `TestRepairRequestDeadToQueuedAndAudit`、`TestFlashSaleRepairAndAudit`（含 403/401/12008 拒绝且无副作用）。 |
| AC-008（长期设计） | PASS | `docs/design/flash-sale.md` 已更新 V4 故障恢复模型；与实现存在一处轻微 key 清理范围漂移（P3，见 CLEAN-002）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 退出码 0，无输出。 |
| `go vet ./...` | PASS | 退出码 0，无输出。 |
| `go test -race -p 1 ./internal/logic/flashsale/...` | PASS | `ok`（含全部 V4 白盒测试）。 |
| `go test -race -p 1 -run 'TestFlashSale|TestRouteTable' ./internal/cmd/...` | PASS | `ok`（真实路由 + 并发不超卖 `-race`）。 |
| `go test -race -p 1 ./internal/migrations/... ./internal/boot/...` | PASS | `ok`（新表结构等价 + 迁移幂等）。 |
| `go test -p 1 ./...`（全量） | PASS | 全部包 `ok`，无失败。 |
| 全局资源三边一致性 | PASS | migration `20261001000018`：`origin/develop` Registry 已 `RESERVED`（flash-sale-v4）↔ Contract `20261001000018` ↔ 实现 `20261001000018_flash_sale_request_audits.up.sql`。错误码 `12008`：位于 `12000-12999` flash-sale-v1 `RESERVED` 域内，Contract 声明复用、实现 `CodeFlashSaleRequestNotRepairable=12008`。三边一致。 |
| `scripts/check-registry.sh` | 说明 | 报「迁移 20261001000018 未登记」1 处，系脚本读取**本地 feature 分支旧版** `.agent/registry/migrations.md`（止于 00017）所致；经 `git show origin/develop:.agent/registry/migrations.md` 核实 00018 已由 Analyst 落 `RESERVED`，非真实漂移。 |

## Findings

### CLEAN-001：AC-003/AC-006 缺少「崩溃重投/重启恢复」的专门故障注入测试

- Severity：P2
- Status：OPEN
- Location：`internal/logic/flashsale/consume.go`（`consumeOne` 单事务出队+落单）、`internal/logic/flashsale/*_test.go`（测试集）
- AC / Invariant：AC-003（处理中崩溃重投幂等）/ INV-015；AC-006（服务重启后 pending 恢复）
- Trigger：消费者在 `FOR UPDATE SKIP LOCKED` 锁定请求后、事务提交前崩溃（进程 kill/panic）。
- Actual：实现依赖「单事务原子 + 事务回滚释放行锁 + 唯一约束」保证请求回到 `queued` 且只产生一次业务效果，机制本身正确；但测试集中**没有**任何「事务中途崩溃」的故障注入测试。`TestConsumeRetryThenDeadLetter` 覆盖的是「技术失败 → 事务内 `retry_count+1` 并提交」与「dead→queued 显式重处理」，事务是**提交**而非**回滚**；`TestFlashSaleConcurrentNoOversell`（`-race`）覆盖并发不超卖，但均未验证「回滚后重投只产生一次效果」这一核心路径。
- Expected：Contract `Verification Requirements`（INV-015）与 `task.md` AC-003 明确要求「构造消费者领取后提交前崩溃，断言请求被重投且订单数/`sold` 只增加一次」，即应提供可区分「事务被拆开/状态先于订单更新」等错误实现的可信测试（如注入提交前故障点后，断言请求仍为 `queued`、无订单、无扣减，再重投消费后订单数/`sold` 各恰增加一次）。
- Impact：故障恢复是本次任务的核心目标，AC-003/AC-006 正是「崩溃/重启」场景；缺少直接证据使这两项 AC 只能靠代码推理而非运行证据支撑，无法识别「事务边界被破坏」类回归。
- Evidence：`internal/logic/flashsale/consume.go:62-102`（整段出队+落单+状态更新在同一 `g.DB().Transaction` 内）；`consume_test.go`、`recovery_test.go`、`redis_test.go` 全部测试函数中无 crash 注入 hook（唯一测试钩子为 `generateOrderNo`，用于订单号撞号，非崩溃注入）。
- Required Fix Boundary：补一个能证明「回滚后重投只产生一次业务效果」的测试（可注入事务提交前故障点，或等价地构造「已领取未提交」中间态并断言回滚后无残留 + 重投单次效果），不规定具体注入方式；不得因此改动生产事务边界。

### CLEAN-002：设计文档/Contract 与实现在「结束活动缓存清理」key 范围上轻微漂移

- Severity：P3
- Status：OPEN
- Location：`docs/design/flash-sale.md` §5.5（第 179 行）、`contract.md` Recommendation#3 vs `internal/logic/flashsale/redis.go:convergeEndedActivity`
- AC / Invariant：AC-008（Design 与最终实现一致）
- Trigger：阅读结束收敛的缓存清理描述与实现对照。
- Actual：文档与 Contract 表述为「清理活动域缓存（DEL activity/stock/bought/idem/soldout + 置 null 标记）」；实现实际为「DEL activity 元数据 + DEL soldout + 置 null 标记，**保留 stock key**（`remaining = total_stock - sold` 可观测、靠 TTL 过期），bought/idem 标记**不显式 DEL**（按 TTL 过期）」。二者在 stock / bought / idem 的清理方式上不一致。
- Expected：设计文档与 Contract 的清理描述应与实现精确一致（建议文档侧改为「DEL activity/soldout + 置 null 标记；stock 保留 remaining 供观测；bought/idem 靠 TTL 过期」）。
- Impact：无功能正确性影响——null 标记 + activity 元数据 DEL 后闸门在检查 bought/idem 前即快速失败，残留标记不会导致错误抢购；stock 保留反而更贴合 AC-005「收敛结果可观测」。属文档一致性瑕疵，由 Analyst/Owner 决定是否随本轮对齐。
- Evidence：`redis.go:430-471`（`convergeEndedActivity` 仅 DEL activity + soldout，注释「stock 保留 remaining 可观测」）；`docs/design/flash-sale.md:179`。
- Required Fix Boundary：对齐文档与实现（建议改文档，不改实现），不改变业务行为。

### CLEAN-003：RepairRequest 未校验 `reason` 长度上限

- Severity：P3
- Status：OPEN
- Location：`internal/logic/flashsale/repair.go:37-48`（`RepairRequest` 入参校验）
- AC / Invariant：AC-007（人工修复入口，参数校验）；`AGENTS.md` §7（按风险校验长度）
- Trigger：管理员提交 `reason` 超过 255 字符。
- Actual：`reason` 仅校验 `TrimSpace` 后非空，未校验长度；超长时 `INSERT flash_sale_request_audits` 命中 `VARCHAR(255)` 数据过长错误，返回 500（`CodeInternalError`），而非稳定的 400。
- Expected：对 `reason` 增加长度上限校验（≤255），超限返回 `1001`（400）稳定拒绝、无副作用。
- Impact：低（后台受限接口、仅管理员触发、无安全泄漏，属输入校验健壮性缺口）。
- Evidence：`repair.go:41-44` 仅非空校验；`20261001000018_flash_sale_request_audits.up.sql` `reason VARCHAR(255) NOT NULL`。
- Required Fix Boundary：补 `reason` 长度上限校验并返回稳定参数错误，不改变其他语义。
