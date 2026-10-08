# Owner Decision

## Review Target

`2deecfda31d8f7effd8512d21fd68eb90d67ea35`（Cleaner 复审 CLEAN 的实现 snapshot，`fix(flash-sale-v5): 修复压测脚本用户名非法并补齐并发下单与基线保存缺陷`）

## Core Logic

- CL-001：容量保护的无副作用拒绝顺序——用户级/活动级限流（12009/429）与排队软上限（12010/429）必须在 Redis 闸门 `runGate` 之前执行，被拒请求不预扣库存、不写一人一单/幂等标记、不写 `flash_sale_order_requests`（INV-018/INV-019）。
- CL-002：Redis 闸门熔断的快速失败——连续失败达阈值熔断 Open → 直接 503/1005 快速失败（不预扣、不建单、不排队），超时后半开探测验证恢复后自动关闭；保持 V3 fail-closed 语义、不降级到同步 MySQL 路径（INV-020）。

## Owner Decision

ACCEPTED

## Decision Evidence

Owner 明确接受 Cleaner 复审 CLEAN 的新 Review Target `2deecfda31d8f7effd8512d21fd68eb90d67ea35`：

- 复审背景：上一轮 `ACCEPT` 的 `35498b0` 经 Deliverer 交付验证 FAIL（`scripts/flashsale-loadtest/run.sh` 用户名 `fslt_{activity}_{i}` 含下划线被 IAM `^[a-zA-Z0-9]{3,24}$` 拒绝，脚本在 `setup_users` 即退出，AC-007/008/009 被阻塞）。Coder 在 `2deecfd` 修复 4 处压测脚本缺陷（用户名、worker 机制、printf 尾换行、空值假 PASS），Cleaner 复审 CLEAN，`review.target` 前移。
- CL-001/CL-002 核心机制未变：`2deecfd` 仅改 `scripts/flashsale-loadtest/run.sh`，未改动任何 Go 生产代码、测试或业务约束，接受本次压测脚本修复。
- 剩余 P3 风险（`CLEAN-001` `verify()` 未自动判定 queued 有界/连接数阈值、`CLEAN-002` 热点 Key 分析依赖 Redis LFU 且默认未启用、`CLEAN-003` `setupConsumeTest` 清理缺 `inventories` 表）接受为非阻塞项。
- 附加要求（传递 Deliverer）：脚本语法检查与模拟测试不能替代实际压测，须真实执行三级压测并核对数据结果；若再次失败，按 Workflow 返回修复。容量保护相关里程碑验证须显式启用对应配置（`flash_sale.rate_limit.enabled` / `queue_capacity.enabled` / `circuit_breaker.enabled` / `metrics.enabled`），不得以默认 `enabled: false` 状态替代实际容量保护验收。
