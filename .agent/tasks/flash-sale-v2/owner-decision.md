# Owner Decision

## Review Target
4ec27c2cb961e5dd1dbee979cbb1d241936feae6

## Core Logic
- CL-001（MySQL 条件扣减兜底 / 不超卖）：`UPDATE ... SET sold = sold + 1 WHERE sold < total_stock` + `RowsAffected` 保证任何并发下成功订单数 ≤ 初始库存。
- CL-002（Redis Lua 闸门 + 补偿 + 对账收敛 / 预扣不超卖、最终一致）：Lua 原子 `DECR` 保证预扣不超预热库存；MySQL 失败补偿 `INCR` 并清除售罄标记；残留漂移由后台对账收敛为 `remaining = total_stock − sold`。

## Owner Decision
ACCEPTED

## Decision Evidence
Owner 于 2026-10-06 在 OwnerGate Session 中明确表达 `ACCEPT`，接受 Cleaner 已 CLEAN 的 review.target `4ec27c2cb961e5dd1dbee979cbb1d241936feae6` 作为本任务交付基线（非抽象接受任务，绑定该具体 snapshot）。适用范围：秒杀 V2 全部实现及 `docs/design/flash-sale.md`（Design Impact = UPDATE）更新，对应 Contract（`APPROVED` @ `998da33`）。
