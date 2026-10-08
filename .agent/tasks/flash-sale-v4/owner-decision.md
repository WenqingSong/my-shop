# Owner Decision

## Review Target
`e2a4617a601b87cd5dd6a80ea448ce61bff9716e`（commit `e2a4617`，含 V4 实现 + 修复，Cleaner 已 CLEAN 的 immutable snapshot）

## Core Logic
- CL-001（权威值收敛补偿，幂等不重不漏）：秒杀库存 Redis `remaining` 仅为闸门预扣、非事实来源；补偿按 MySQL 权威事实重算 `remaining = total_stock - sold - inflight_queued` 后幂等 `SET`（取代非幂等 INCR），重复/崩溃重放收敛结果不变；MySQL 条件扣减 + 唯一约束为超卖兜底。生产代码 `internal/logic/flashsale/redis.go`（`convergeStock`/`authoritativeRemaining`/`convergeStockAndMarkers`），测试 `redis_test.go`（`TestConvergeStockIdempotentAndClearsSoldout`、`TestConvergeStockCountsInflightQueued`）。
- CL-002（崩溃重投幂等，事务原子 + 唯一约束）：消费者「出队（`FOR UPDATE SKIP LOCKED`）→ 建单 → 条件扣库存 → 置 `success`」在同一个 MySQL 事务内；领取后、提交前崩溃则整体回滚，请求回 `queued` 重投且只产生一次业务效果；唯一约束永久兜底。生产代码 `internal/logic/flashsale/consume.go`（`consumeOne` 整段在 `g.DB().Transaction` 内），测试 `consume_test.go`（`TestConsumeCrashBeforeCommitRedeliversOnce`）。

## Owner Decision
ACCEPTED

## Decision Evidence
Owner 于 2026-10-07 明确回复 `ACCEPT`，接受 Cleaner 已 CLEAN 的 snapshot `e2a4617`（`review.target`）所承载的上述 CL-001/CL-002 两个核心机制，并知悉并接受两条非阻塞风险：① 权威值收敛为最终一致，活动进行中读改写竞态下 `remaining` 可能短暂偏高（≤ 一个对账周期 ≤60s），但不超 MySQL 权威上限且自愈；② 崩溃窗口孤儿 `bought` 标记极端情况下可能造成用户短暂错误 `12004`（UX 级，非正确性），由 TTL + 命中自愈收敛。本决定仅对该具体 snapshot 有效，不构成对任务的抽象接受；后续任何生产代码、测试或约束发生实质变化均需复审。
