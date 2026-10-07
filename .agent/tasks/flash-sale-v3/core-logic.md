# Owner Core Logic 验证卡

本文件由 Cleaner 整理，承载值得 Owner 亲自理解的 1~2 个核心机制及其验证入口。Owner 决策状态由 OwnerGate 在 Owner 明确决定后持久化，本文件不承担该状态权威。

## CL-001：异步消费落单如何保持五个业务不变量

- Owner 需要理解：秒杀库存与订单的一致性最终由 MySQL 单事务兜底——消费者在同一事务内「行锁读秒杀价 → 条件扣减 `sold < total_stock` → 插入订单 → 标记请求 success」，并靠 `uk_flash_one_per_user`/`uk_flash_idempotency`/`uk_flash_order_no` 唯一约束防一人一单/重复扣减/订单号冲突。异步只是把「下单」拆成入队+消费，MySQL 事实来源不变；任何一步失败整体回滚，不残留半成品订单。
- 生产代码：`internal/logic/flashsale/flashsale.go` `insertOrderInTx`（行锁 + 条件扣减 `Where("sold < total_stock")` + `RowsAffected` 判定 + 先建单后扣库存）、`internal/logic/flashsale/consume.go` `consumeOne`（`FOR UPDATE SKIP LOCKED` 出队 + 事务内落单）。
- 关键测试：`TestFlashSaleConcurrentNoOversell`（`internal/cmd/flashsale_test.go`，20 并发入队、stock=5）、`TestFlashSaleOnePerUser`、`TestFlashSaleIdempotency`。
- 基线验证：`go test -race -run TestFlashSale ./internal/cmd/...`（预期全部 PASS，断言成功订单数 = sold = 5、不超卖、一人一单、幂等只扣一次）。
- 可选 Mutation：把 `insertOrderInTx` 中扣减库存的条件 `Where("sold < total_stock")` 删除，改为无条件 `SET sold = sold + 1`（保留 `RowsAffected` 判定但去掉库存上限）。
- 预期失败：`TestFlashSaleConcurrentNoOversell` 必须失败——`sold` 会超过 `total_stock`（超卖），断言 `sold != totalStock` 或 `total-sold < 0` 触发 `t.Fatalf`。
- 恢复确认：恢复 `Where("sold < total_stock")` 后重跑 `go test -race -run TestFlashSaleConcurrentNoOversell ./internal/cmd/...`，恢复 PASS。

## CL-002：异步请求状态机与失败重试/死信/补偿

- Owner 需要理解：请求经历 `queued(0)→success(1)/failed(2)/dead(3)` 四态。消费技术失败（如订单号撞号）按指数退避重试、超上限落 `dead` 不再无限重试；业务失败（库存不足/时间窗结束/已购等）直接落 `failed`；两类终态失败都在事务提交后补偿 Redis 预扣并清除一人一单/幂等标记，不留「预扣但无请求」残留。`success` 与成功订单创建同事务，保证结果查询与最终落库事实一致。
- 生产代码：`internal/logic/flashsale/consume.go` `classifyConsumeError`/`retryOrDeadRequestInTx`/`consumeBackoffSeconds`；`internal/logic/flashsale/request.go` 状态映射；`internal/logic/flashsale/redis.go` `compensatePreDeductAndMarkers`。
- 关键测试：`TestConsumeRetryThenDeadLetter`（`internal/logic/flashsale/consume_test.go`，覆盖 retry→dead→`dead→queued` 重处理成功）、`TestFlashSaleV3Compensation`（`internal/cmd/flashsale_v3_test.go`，覆盖业务失败补偿）。
- 基线验证：`go test -race -run TestConsumeRetryThenDeadLetter ./internal/logic/flashsale/...` 与 `go test -race -run TestFlashSaleV3Compensation ./internal/cmd/...`（预期 PASS）。
- 可选 Mutation：把 `retryOrDeadRequestInTx` 中 `if newRetry > maxConsumeRetryAttempts` 的超限落 `dead` 分支注释掉，使技术失败无限退避重试（永远保持 `queued`）。
- 预期失败：`TestConsumeRetryThenDeadLetter` 必须失败——第二次消费后断言 `req.Status == requestStatusDead` 不再成立（仍为 `queued`），触发 `t.Fatalf`。
- 恢复确认：恢复超限落 `dead` 分支后重跑 `go test -race -run TestConsumeRetryThenDeadLetter ./internal/logic/flashsale/...`，恢复 PASS。
