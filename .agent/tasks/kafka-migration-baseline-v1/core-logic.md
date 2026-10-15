# Owner Core Logic 验证卡

本任务为「Kafka 改造前基线」，不新增/修改生产代码；以下核心逻辑是**既有秒杀能力中真正决定业务正确性**的机制，也是后续 Kafka 改造必须原样保留、不能破坏的验证入口。验证证据由 Cleaner 在 `review.target = a2d6870` 上独立复跑。

## CL-001：防超卖——Redis Lua 闸门限量（快速失败）+ MySQL 条件扣减兜底

- Owner 需要理解：秒杀库存正确性的**事实来源是 MySQL**，但热路径由 Redis Lua 闸门原子 `DECR` 限量快速失败；破坏任一层都会导致「超卖/库存为负」（成功订单数 > 初始库存）。
- 生产代码：
  - 闸门：`internal/logic/flashsale/redis.go:65-141`（`flashSaleGateScript`：`remaining <= 0` 售罄、`DECR` 后 `after < 0` 回滚并售罄）
  - 事实来源兜底：`internal/logic/flashsale/flashsale.go:527-540`（`UPDATE ... SET sold = sold + 1 WHERE sold < total_stock` + `RowsAffected` 判定）
- 关键测试：`test/e2e/flashsale_test.go:477` `TestFlashSaleConcurrentNoOversell`（20 并发对 stock=5，断言 `success == 5`、`sold == 5`、`total-sold ≥ 0`、`COUNT(订单) == sold`）
- 基线验证：`make test-integration`（需 MySQL/Redis 就绪）→ 全部 PASS（Cleaner 独立复跑）。
- Mutation（已实测，隔离 worktree）：把 `redis.go` 闸门两处库存判断 `if remaining <= 0 then`、`if after < 0 then` 改为 `if false then`（闸门不再限量）。
- 预期失败：`TestFlashSaleConcurrentNoOversell` 断言 `success == totalStock(5)` 失败。实测输出 `flashsale_test.go:517: successful enqueues=20 want 5` → `FAIL`，证明该测试确实保护「不超卖」不变量。
- 恢复确认：还原 `redis.go` 后重新 `make test-integration` → 全部 PASS（Mutation 未遗留，主工作区 `redis.go` 已还原为 `if remaining <= 0 then` / `if after < 0 then`）。

## CL-002：一人一单 / 幂等 / 失败不建单——唯一约束兜底 + 消费单事务

- Owner 需要理解：`flash_sale_orders` 与 `flash_sale_order_requests` 的唯一约束是「一人一单 / 重复请求不重复扣减」的**最终兜底**；异步消费在单事务内完成「校验 + 条件扣库存 + 建单 + 状态更新」，崩溃回滚后重投只产生一次业务效果，业务失败落 `failed` 不建单。
- 生产代码：
  - 唯一约束：`internal/migrations/sql/20261001000011_flash_sale.up.sql`（`uk_flash_one_per_user(activity_id,sku_id,user_id)`、`uk_flash_idempotency(user_id,idempotency_key)`、`uk_flash_order_no`）
  - 消费单事务：`internal/logic/flashsale/consume.go:70-169`（`consumeOne`：`FOR UPDATE SKIP LOCKED` 出队 → 单事务落单 → 状态更新；业务失败 `failed`、技术失败退避/死信；事务提交后才补偿 Redis）
- 关键测试：
  - `test/e2e/flashsale_test.go:400` `TestFlashSaleOnePerUser`（二次下单 409/12004、订单数=1、sold=1）
  - `test/e2e/flashsale_test.go:432` `TestFlashSaleIdempotency`（同键仅 1 单/扣 1 次；同键异内容 12005 不扣库存）
  - `internal/logic/flashsale/consume_test.go:363` `TestConsumeCrashBeforeCommitRedeliversOnce`（注入提交前崩溃 → 回滚无订单 → 重投仅 1 单）
  - `test/e2e/flashsale_test.go:538` `TestFlashSaleOrderFailures`（失败不建单、不扣库存）
- 基线验证：`make test-integration` → 全部 PASS（Cleaner 独立复跑）。
- 可选 Mutation（供 Owner 后续自查）：临时移除 `uk_flash_one_per_user` 唯一约束后重跑 `TestFlashSaleOnePerUser`，预期二次下单不再返回 12004 且订单数=2 → 测试失败；恢复约束后重跑恢复 PASS。
- 恢复确认：还原迁移文件并 `make test-integration` 全部 PASS。
