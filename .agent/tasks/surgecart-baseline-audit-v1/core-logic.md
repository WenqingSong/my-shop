# Owner Core Logic（surgecart-baseline-audit-v1）

> 本文件只承载 Owner 核心机制验证卡，不构成 Owner 决策；`state.owner` 由 OwnerGate 在 Owner 明确决定后持久化。
> 审查对象：C1 `1ca5445ef3d9cc04a7f11f78eb44638376d1eb78`（报告修正快照）；被审计代码锚点 `7ba5a31077a7b8977473765c2d9a7d402ddadb8c`。
> 结论：该任务交付的是「特定版本上的证据报告」，因此核心逻辑卡验证的是**报告所描述的不变量与它的证据强度**，不是新增业务实现。

## CL-001：MySQL 请求表 = 状态机 + 出队队列，消费者在单事务内领取并回写

- Owner 需要理解：`flash_sale_order_requests` 只是「受理凭据 + 状态机 + 重试账本」，不是订单事实，也不是库存事实；消费者每事务用 `FOR UPDATE SKIP LOCKED` 领 1 条 `queued` 请求，把「订单插入 + `sold` 扣减 + 请求状态回写」放在同一事务里。一旦把 DB 抖动之类的技术错误误判为业务终态（R1），用户的请求会被永久判 `failed`，且没有自动恢复路径。
- 生产代码：`internal/logic/flashsale/consume.go:76-131`（事务 `:76`、领取 `:79-85`、`resolveSku` 失败直接 `failed` `:99-105`、落单/失败分流 `:108-118`、成功回写 `:130`）；`internal/logic/flashsale/consume.go:195-222`（技术失败 5/10/20s 退避，`retry_count=4` 落 `dead`）；`internal/logic/flashsale/request.go:61-92`（入队 INSERT 与幂等冲突读回）。
- 关键测试：`internal/logic/flashsale/consume_test.go:151` `TestConsumeRetryThenDeadLetter`；`:363` `TestConsumeCrashBeforeCommitRedeliversOnce`；`test/e2e/flashsale_v3_test.go:60` `TestFlashSaleV3EnqueueAndResultQuery`。
- 基线验证（Cleaner 实跑）：`make test-integration` exit 0 → `internal/logic/flashsale 3.702s`、`internal/worker 0.006s`、`test/e2e 90.317s`；`make test` 同样 exit 0。
- 可选 Mutation：把 `consume.go:80` 的 `Where("status", requestStatusQueued)` 放宽为 `Where("status >= 0")`，使消费者可能重领已终态请求。
- 预期失败：`TestFlashSaleV3ConsumeIdempotent` 或 `TestConsumeCrashBeforeCommitRedeliversOnce` 应因重复落单触发唯一约束或状态断言失败（Cleaner 未执行该 Mutation，仅作 Owner 可选验证）。
- 恢复确认：还原该行并重跑 `make test-integration`，`internal/logic/flashsale` 与 `test/e2e` 均应恢复 `ok`。

## CL-002：库存不变量由 MySQL 兜底，Redis 只是准入闸门（含已登记的覆盖缺口）

- Owner 需要理解：Redis Lua 只决定「谁进入队列」（预扣 + 一人一单/幂等标记）；即使 Redis 多放行，最终不超卖仍靠绑定行 `FOR UPDATE` + `sold >= total_stock` 预检 + `UPDATE … WHERE sold < total_stock` + `RowsAffected` + 三个订单唯一约束。**现有并发 e2e 能发现闸门被破坏，但发现不了 MySQL 两道兜底被削弱**——这正是本轮报告 §6.4 第 1 条与 §8.2 第 14 项如实登记、并留给 T02 的覆盖缺口。
- 生产代码：`internal/logic/flashsale/flashsale.go:463-542`（预检 `:499-501`、插单 `:506-519`、条件扣减 `:530-540`）；`internal/logic/flashsale/redis.go:65-141`（Lua 判定顺序与 `DECR` 预扣，`EVAL` 在 `:207`）。
- 关键测试：`test/e2e/flashsale_test.go:477` `TestFlashSaleConcurrentNoOversell`（20 并发抢 5 件，断言受理数=5、`sold=5`、订单数=5、`total-sold ≥ 0`）。
- 基线验证：`make test-integration` exit 0（`test/e2e 90.317s`）；隔离库单跑 `TestFlashSaleConcurrentNoOversell` → `ok 2.619s`。
- 可选 Mutation A（闸门）：把 `redis.go:122` 与 `:128` 的库存判断改为 `if false`。
- 预期失败 A：`TestFlashSaleConcurrentNoOversell` 在 `flashsale_test.go:517` 报 `successful enqueues=20 want 5`。Cleaner 本轮实测：FAIL 0.803s。
- 可选 Mutation B（MySQL 兜底）：把 `flashsale.go:499` 的 `>=` 改为 `>`，并把 `:532` 的 `sold < total_stock` 改为 `1 = 1`。
- 预期失败 B：**无**。Cleaner 本轮实测该用例仍 `ok 0.906s`，因为 Redis 闸门只放行 5 条，MySQL 侧从未出现超额并发。这说明该测试只保护「受理数 + 最终结果」，不保护 MySQL 兜底本身；报告已据此修正表述并把缺口登记为 T02 事项，未修改生产代码或测试。
- 恢复确认：删除隔离 worktree 与隔离库；`git status --short` 为空；重跑 `make test-integration` 全绿（Cleaner 已完成清理与复核）。
