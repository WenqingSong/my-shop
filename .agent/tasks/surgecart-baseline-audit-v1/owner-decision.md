# Owner Decision

## Review Target

757ed22301b402f2cf29fbc89b80b5f8a6c9d4cb（C1，仅新增 `docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md` 468 行；被审计代码锚点 `7ba5a31077a7b8977473765c2d9a7d402ddadb8c`）

## Core Logic

- CL-001：MySQL 请求表 = 状态机 + 出队队列。消费者每事务用 `FOR UPDATE SKIP LOCKED` 领取 1 条 `queued` 请求，订单插入、`sold` 扣减与请求状态回写同事务；技术错误退避重试、`retry_count=4` 落 `dead`，不得误判为业务终态。
- CL-002：库存不变量由 MySQL 兜底（行锁 + `sold >= total_stock` 预检 + `UPDATE … WHERE sold < total_stock` + `RowsAffected` + 唯一约束），Redis Lua 只决定准入；现有并发 e2e 无法检测 MySQL 兜底被移除。

## Owner Decision

REJECTED

## Decision Evidence

- 决定来源：Owner 于 2026-10-30 在本 Session 明确回复「REJECT」。
- 拒绝理由范围（Owner 原话要点）：**仅限审计报告的事实准确性**，不涉及生产代码实现，也不扩大当前 Task 范围。
- 要求修正项 1（对应 CLEAN-001）：修正报告 §6.4 中把 `TestFlashSaleConcurrentNoOversell` 描述为同时覆盖 Redis 准入与 MySQL 库存兜底的错误表述；明确该测试目前无法独立证明 MySQL 库存预检与条件扣减的正确性；把该覆盖缺口登记为 T02 后续审查事项。
- 要求修正项 2（对应 CLEAN-002）：修正 E12 与 §6.4 的 e2e 秒杀测试数量与分组错误，统一为实际核实的 20 个。
- 附加约束（Owner 原话要点）：仅修改审计报告，不修改生产代码、测试与既有实现；保留当前审计结论与原始代码锚点；修正后形成新的审查快照 C2；由 Cleaner 对新快照重新独立审查并形成新的 CLEAN；不提前执行 `delivery-start`；完成后重新提交 OwnerGate 决策。
- 适用范围：本次拒绝使 C1 `757ed22301b402f2cf29fbc89b80b5f8a6c9d4cb` 不被接受；`review.status=CLEAN` 的既有结论不被本次决定改写，但新报告快照必须形成新的 C1/C2 并由 Cleaner 重新审查。`owner.status=REJECTED`、`owner.review_target=757ed22301b402f2cf29fbc89b80b5f8a6c9d4cb`。

## Return To

Coder
