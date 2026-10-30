# Owner Core Logic（surgecart-baseline-audit-v1）

> 本文件只承载 Owner 核心机制验证卡，不构成 Owner 决策；`state.owner` 由 OwnerGate 在 Owner 明确决定后持久化。
> 审查对象：C1 `757ed22301b402f2cf29fbc89b80b5f8a6c9d4cb`；被审计代码锚点 `7ba5a31077a7b8977473765c2d9a7d402ddadb8c`。

## CL-001：MySQL 请求表 = 状态机 + 出队队列，消费者在单事务内领取并回写

- Owner 需要理解：`flash_sale_order_requests` 只是「受理凭据 + 状态机 + 重试账本」，不是订单事实，也不是库存事实；消费者每事务用 `FOR UPDATE SKIP LOCKED` 领 1 条，把「订单插入 + `sold` 扣减 + 请求状态回写」放在同一事务里。若把技术错误误判为业务终态（R1），一次 DB 抖动就会把用户请求永久判 `failed`。
- 生产代码：`internal/logic/flashsale/consume.go:70-131`（领取 `:79-85`，`resolveSku` 失败直接 `failed` `:99-105`，落单/失败分流 `:108-118`，成功回写 `:130`）；`internal/logic/flashsale/consume.go:195-222`（重试 5/10/20s，`retry_count=4` 落 `dead`）；`internal/logic/flashsale/request.go:61-92`（入队 INSERT 与幂等冲突读回）。
- 关键测试：`internal/logic/flashsale/consume_test.go:151` `TestConsumeRetryThenDeadLetter`；`:363` `TestConsumeCrashBeforeCommitRedeliversOnce`；`test/e2e/flashsale_v3_test.go` `TestFlashSaleV3ConsumeIdempotent`。
- 基线验证：`MYSQL_ROOT_PASSWORD=root AUTH_JWT_SECRET=<占位> make test-integration` → `internal/logic/flashsale ok 3.947s`、`test/e2e ok 93.904s`（Cleaner 实跑）；全量 `make test` 同样 exit 0。
- 可选 Mutation：把 `consume.go:80` 的 `Where("status", requestStatusQueued)` 放宽为 `Where("status >= 0")`，使消费者可能重领已终态请求。
- 预期失败：`TestFlashSaleV3ConsumeIdempotent` 或 `TestConsumeCrashBeforeCommitRedeliversOnce` 应因重复落单触发唯一约束/状态断言失败（Cleaner 未执行该 Mutation，仅作 Owner 可选验证）。
- 恢复确认：还原该行并重跑 `make test-integration`，`internal/logic/flashsale` 与 `test/e2e` 均应恢复 `ok`。

## CL-002：库存不变量由 MySQL 兜底，Redis 闸门不是事实源（含覆盖缺口）

- Owner 需要理解：Redis Lua 只决定「谁进入队列」（预扣 + 一人一单/幂等标记）；即使 Redis 多放行，最终不超卖仍靠绑定行 `FOR UPDATE` + 库存预检 + `UPDATE … WHERE sold < total_stock` + `RowsAffected` + 三个订单唯一约束。**现有 e2e 并发用例能发现闸门被破坏，但发现不了 MySQL 兜底被移除**，这正是 CLEAN-001 记录的证据缺口。
- 生产代码：`internal/logic/flashsale/flashsale.go:463-542`（预检 `:499-501`、插单 `:506-519`、条件扣减 `:530-540`）；`internal/logic/flashsale/redis.go:65-141`（Lua 判定与 `DECR` 预扣）。
- 关键测试：`test/e2e/flashsale_test.go:477` `TestFlashSaleConcurrentNoOversell`（20 并发抢 5 件）。
- 基线验证：`make test-integration` → `test/e2e ok 93.904s`；单跑 `go test -count=1 -run TestFlashSaleConcurrentNoOversell ./test/e2e/...` → `ok`。
- 可选 Mutation A（闸门）：把 `redis.go:122` 与 `:128` 的库存判断改为 `if false`，使 Lua 不再拒绝超额预扣。
- 预期失败 A：`TestFlashSaleConcurrentNoOversell` 在 `successful enqueues=20 want 5` 处失败（Cleaner 已实测：FAIL 0.83s）。
- 可选 Mutation B（MySQL 兜底）：把 `flashsale.go:499` 的 `>=` 改为 `>`，并把 `:532` 的 `sold < total_stock` 改为 `1 = 1`。
- 预期失败 B：**无**。Cleaner 实测该用例仍 PASS（0.795s），因为 Redis 只放行 5 个请求，MySQL 侧从未出现超额并发；说明该测试只覆盖最终结果断言，不能检测兜底被移除，需按 CLEAN-001 修正报告表述或补专项核查。
- 恢复确认：删除隔离 worktree；`git status --short` 为空；重跑 `make test-integration` 全绿（Cleaner 已完成清理与复核）。
