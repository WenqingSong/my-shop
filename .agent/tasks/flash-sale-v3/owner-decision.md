# Owner Decision

## Review Target

`8f0db1137d78505368f855830a15f0adf8833935`（Cleaner 已 CLEAN 的复审 snapshot，对应修复 commit C1' `8f0db11`）

## Core Logic

- CL-001（异步消费落单守住五个业务不变量）：消费者在同事务内「行锁读秒杀价 → 判 `sold < total_stock` → 插入订单（唯一约束兜底）→ 条件扣减 + RowsAffected 判定」，任一步失败整体回滚，不残留半成品。生产代码 `internal/logic/flashsale/flashsale.go` `insertOrderInTx`、`internal/logic/flashsale/consume.go` `consumeOne`；测试 `TestFlashSaleConcurrentNoOversell`/`TestFlashSaleOnePerUser`/`TestFlashSaleIdempotency`。
- CL-002（异步请求四态状态机 + 失败重试/死信/补偿）：请求经历 `queued→success/failed/dead`；技术失败指数退避重试、超上限落 `dead`，业务失败直接落 `failed`，两类终态失败事务提交后补偿 Redis 预扣并清标记。生产代码 `internal/logic/flashsale/consume.go` `classifyConsumeError`/`retryOrDeadRequestInTx`/`consumeBackoffSeconds`、`internal/logic/flashsale/redis.go` `compensatePreDeductAndMarkers`；测试 `TestConsumeRetryThenDeadLetter`/`TestFlashSaleV3Compensation`。

## Owner Decision

ACCEPTED

## Decision Evidence

Owner 于 OwnerGate 会话中明确回复「ACCEPT」，接受 Cleaner 已 CLEAN 的该具体 snapshot（`review.target = 8f0db1137d78505368f855830a15f0adf8833935`），而非抽象接受任务。接受范围覆盖上述 CL-001 / CL-002 两个核心机制所承载的异步下单业务正确性，及 Cleaner 复审结论（CLEAN-001 已关闭、无开放 P0/P1/P2/P3）。

剩余非阻塞风险（已在会话中告知 Owner，不阻断接受）：`FOR UPDATE SKIP LOCKED` 依赖 MySQL 8.0（部署版本待确认）；死信修复本任务仅提供运维 SQL/日志最小途径；Redis 无持久化配置不影响正确性（中间态已落 MySQL）。
