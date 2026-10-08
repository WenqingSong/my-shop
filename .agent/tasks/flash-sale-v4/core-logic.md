# Owner 核心逻辑验证

本文件只承载 Owner 需要理解的核心机制验证卡，不承载 Owner 决策状态（决策状态由 OwnerGate 持久化）。

## CL-001：权威值收敛补偿（幂等，不重不漏）

- Owner 需要理解：秒杀库存的 Redis `remaining` 只是**闸门预扣**，不是事实来源；补偿不靠「INCR 加回去」（重复/崩溃会加多次导致超预扣），而是按 MySQL 权威事实重算 `remaining = total_stock - sold - inflight_queued` 后**幂等 SET**。任何失败、重试、崩溃重放，收敛结果都相同；真正防超卖的是 MySQL 条件扣减 `sold < total_stock` + 唯一约束。
- 生产代码：`internal/logic/flashsale/redis.go` 的 `convergeStock` / `authoritativeRemaining`；调用点 `request.go:enqueue`（入队失败）与 `consume.go:consumeOne`（消费终态失败后）。
- 关键测试：`internal/logic/flashsale/redis_test.go` 的 `TestConvergeStockIdempotentAndClearsSoldout`、`TestConvergeStockCountsInflightQueued`。
- 基线验证：`go test -race -p 1 -run 'TestConvergeStock' ./internal/logic/flashsale/...` → ok。
- 可选 Mutation：把 `convergeStock` 里的 `g.Redis().SetEX(...)` 改成 `g.Redis().Incr(...)`（退回非幂等 INCR 补偿）。
- 预期失败：`TestConvergeStockIdempotentAndClearsSoldout` 的「再次收敛结果仍为 10」断言会失败——INCR 会把 `remaining` 变成 11，暴露超预扣。
- 恢复确认：还原为 `SetEX` 后重跑 `go test -race -run 'TestConvergeStock' ./internal/logic/flashsale/...` 通过。

## CL-002：崩溃重投幂等（事务原子 + 唯一约束）

- Owner 需要理解：消费者「出队（`FOR UPDATE SKIP LOCKED`）→ 建单 → 条件扣库存 → 状态置 `success`」在**同一个 MySQL 事务**内完成；若领取后、提交前崩溃，事务整体回滚（无半成品订单、无残留扣减），请求回到 `queued` 被重新领取，且只产生一次业务效果（订单数与库存扣减各一次）。`uk_flash_one_per_user` / `uk_flash_idempotency` 是永久兜底。
- 生产代码：`internal/logic/flashsale/consume.go` 的 `consumeOne`（整段在 `g.DB().Transaction` 内）；`consumeBeforeCommitHook` 为生产恒 nil 的测试故障注入钩子。
- 关键测试：`internal/logic/flashsale/consume_test.go` 的 `TestConsumeCrashBeforeCommitRedeliversOnce`。
- 基线验证：`go test -race -p 1 -run TestConsumeCrashBeforeCommitRedeliversOnce ./internal/logic/flashsale/...` → ok。
- 可选 Mutation：把 `successRequestInTx`（状态更新）从 `consumeOne` 事务内移出、改用独立已提交的 DB 调用，即把「建单/扣库存」与「状态更新」拆成两个已提交事务。
- 预期失败：`TestConsumeCrashBeforeCommitRedeliversOnce` 的「回滚后无订单、不扣库存」断言会失败——订单/`sold` 已独立提交，无法随故障回滚。
- 恢复确认：还原单事务后重跑同一测试通过。
