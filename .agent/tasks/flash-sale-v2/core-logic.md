# Core Logic 验证卡（秒杀 V2）

> Owner 只需通过「代码位置 → 业务后果 → 测试失败」理解因果关系，不必逐行 Review 全部 Diff。

## CL-001：MySQL 条件扣减兜底（不超卖）

- Owner 需要理解：秒杀最终正确性由 MySQL 条件扣减保证——`UPDATE ... SET sold = sold + 1 WHERE sold < total_stock` 配合 `RowsAffected` 判定，任何并发下成功订单数都不会超过初始库存（`sold ≤ total_stock`）。若去掉这个条件，并发下单会超卖。
- 生产代码：`internal/logic/flashsale/flashsale.go` `insertOrder`（条件扣减 L420~L432）。
- 关键测试：`TestFlashSaleConcurrentNoOversell`（`internal/cmd/flashsale_test.go`，20 并发抢 5 库存）。
- 基线验证：`go test -race -run TestFlashSaleConcurrentNoOversell ./internal/cmd/` 通过（success=5、sold=5、订单数=5）。
- 可选 Mutation：删除条件扣减里的 `Where("sold < total_stock")`（变为无条件 `sold = sold + 1`）。
- 预期失败：`TestFlashSaleConcurrentNoOversell` 失败——`sold` 或成功订单数 > `totalStock`（5）。
- 恢复确认：恢复 `Where("sold < total_stock")` 后重跑同一命令，测试重新通过。

## CL-002：Redis Lua 闸门 + 补偿 + 对账收敛（预扣不超卖、最终一致）

- Owner 需要理解：Redis 预扣只是加速闸门、非事实来源。Lua 原子 `DECR` 保证 Redis 预扣总量不超过预热库存；MySQL 下单失败时 `compensatePreDeduct` 会 `INCR` 回补并清除售罄标记；进程崩溃等残留漂移由后台对账把 `remaining` 刷成 `total_stock - sold` 收敛。若补偿只 INCR 不清售罄标记，会出现「有库存却快速失败」的伪售罄。
- 生产代码：`internal/logic/flashsale/redis.go` `flashSaleGateScript`（L64~L133）、`compensatePreDeduct`（L301~L313）、`ReconcileCache`（L322+）。
- 关键测试：`TestFlashSaleV2RedisPreDeductNoOversell`、`TestFlashSaleV2Reconcile`（`internal/cmd/flashsale_v2_test.go`）、`TestCompensatePreDeductClearsSoldout`（`internal/logic/flashsale/redis_test.go`）。
- 基线验证：`go test -race -run 'TestFlashSaleV2|TestCompensatePreDeduct' ./internal/cmd/ ./internal/logic/flashsale/` 通过。
- 可选 Mutation：删除 `compensatePreDeduct` 中的 `g.Redis().Del(ctx, flashSaleSoldoutKey(...))`（回到只 INCR）。
- 预期失败：`TestCompensatePreDeductClearsSoldout` 失败——售罄标记未被清除（`exists != 0`）。
- 恢复确认：恢复 `Del(soldoutKey)` 后重跑同一命令，测试重新通过。
