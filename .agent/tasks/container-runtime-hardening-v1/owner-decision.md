# Owner Decision

## Review Target

`41c12a7`（`fix(container): 修复 CLEAN-001 优雅关闭超时键改为蛇形 graceful_timeout`）

## Core Logic

- CL-001：`/ready` readiness 判定——fail-closed；MySQL 或 Redis 任一不可达返回 503；探测超时 + 进程内 TTL 缓存降低探测压力；`/health` 保持 liveness 语义（不依赖外部组件状态）。
- CL-002：优雅关闭与后台扫描器停机一致性——SIGTERM/SIGINT → `signalCtx` cancel → 后台扫描器停止领取新任务 → 在途事务由数据库事务保证一致性 → `queued` 请求可恢复消费。

## Owner Decision

ACCEPTED

## Decision Evidence

Owner 明确接受 `review.target=41c12a7` 的 CLEAN snapshot，并确认两个核心机制：

- CL-001 接受不变量：`ready=true` 代表实例具备接收流量条件；禁止「依赖故障 → `/ready` 返回成功 → 坏实例继续接收请求」这条路径。
- CL-002 接受行为：消费事务中断时回滚、不产生半完成订单、重启后依赖现有重试/幂等机制恢复、`WaitGroup` 等待后台 goroutine 完成退出。

关于剩余风险 CLEAN-002（`probeReady` 探测失败无排障日志）：Owner 决定**暂不阻塞当前交付**，理由为当前核心不变量未受影响、fail-closed 行为正确、不泄漏内部信息优先级高于日志增强。记录为后续 P3 改进项，目标为增加 sanitized readiness failure log（不输出 DSN/密码/Redis 地址等敏感信息、不改变 readiness 返回行为），后续可创建独立 cleanup task 处理 CLEAN-002 readiness observability enhancement。

适用范围：本次决定仅约束 `container-runtime-hardening-v1` 的容器运行时/生命周期实现，不改变业务规则、公开业务 API、错误码语义或数据模型，不产出 K8s 编排清单。
