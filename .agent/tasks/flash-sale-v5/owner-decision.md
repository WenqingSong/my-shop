# Owner Decision

## Review Target

`35498b0340b7e8688f18e47dd3e64c11fdb9b3a7`（Cleaner 已 CLEAN 的实现 snapshot，`feat(flash-sale-v5): 秒杀容量保护与可观测性（限流/排队软上限/熔断/指标）`）

## Core Logic

- CL-001：容量保护的无副作用拒绝顺序——用户级/活动级限流（12009/429）与排队软上限（12010/429）必须在 Redis 闸门 `runGate` 之前执行，被拒请求不预扣库存、不写一人一单/幂等标记、不写 `flash_sale_order_requests`（INV-018/INV-019）。
- CL-002：Redis 闸门熔断的快速失败——连续失败达阈值熔断 Open → 直接 503/1005 快速失败（不预扣、不建单、不排队），超时后半开探测验证恢复后自动关闭；保持 V3 fail-closed 语义、不降级到同步 MySQL 路径（INV-020）。

## Owner Decision

ACCEPTED

## Decision Evidence

Owner 明确接受 Cleaner 已 CLEAN 的具体 Review Target `35498b0340b7e8688f18e47dd3e64c11fdb9b3a7`：

- 接受 CL-001 的无副作用拒绝顺序设计。
- 接受 CL-002 的 Redis 闸门 fail-closed 熔断设计（不降级到同步 MySQL 路径）。
- 已知 P3 风险（`CLEAN-001` 压测 `verify()` 未自动判定 queued 有界/连接数阈值、`CLEAN-002` 热点 Key 分析依赖 Redis LFU 且默认未启用、`CLEAN-003` `setupConsumeTest` 清理缺 `inventories` 表）接受为非阻塞项。
- 附加要求（传递 Deliverer）：容量保护相关里程碑验证时须显式启用对应配置（`flash_sale.rate_limit.enabled` / `queue_capacity.enabled` / `circuit_breaker.enabled` / `metrics.enabled`），不得以默认 `enabled: false` 状态替代实际容量保护验收。
