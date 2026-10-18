# Owner Core Logic 验证卡

> 仅列出真正决定正确性的核心机制；普通 DTO、字段搬运与样板 CRUD 不列入。
> Owner Verification 状态唯一事实源是 `state.yaml.owner_verification.status`，本文件只承载验证卡与证据。

## CL-001：条件库存扣减（防超卖 / 库存不为负）

- Owner 需要理解：并发下多个请求同时抢同一秒杀 SKU，仅靠「先查剩余再写」会在检查与写入之间出现竞争窗口导致超卖。真正的兜底是 `UPDATE ... SET sold = sold + 1 WHERE id=? AND sold < total_stock` 的**条件更新**，以 `RowsAffected == 0` 判定失败并整体回滚；`sold`/`total_stock` 为 `INT UNSIGNED`，从类型上杜绝负值。若把条件更新退化为无条件 `sold = sold + 1`，会突破「成功订单数 ≤ 初始库存」。
- 生产代码：`internal/logic/flashsale/flashsale.go` `insertOrder`（约 L363-373，条件扣减 + `RowsAffected` 判定）
- 关键测试：`TestFlashSaleConcurrentNoOversell`（`internal/cmd/flashsale_test.go`，20 并发抢 5 库存，断言成功恰 5、`COUNT(orders)=sold=5`、库存 ≥ 0）
- 基线验证：`go test -race -run 'TestFlashSaleConcurrentNoOversell' ./internal/cmd/`
- 可选 Mutation：删除条件扣减中的 `Where("sold < total_stock")`（改为无条件 `sold=sold+1`）
- 预期失败：`TestFlashSaleConcurrentNoOversell` 中 `success != totalStock` 或 `sold > total_stock` 断言失败
- 恢复确认：还原条件更新后重跑上述命令，恢复通过

## CL-002：一人一单 + 幂等去重（DB 唯一约束兜底）

- Owner 需要理解：一人一单与幂等去重是两层语义，且都**不能只靠应用层「先查再写」**——并发下会漏。真正兜底是 `flash_sale_orders` 表上的两个唯一约束：`uk_flash_one_per_user(activity_id, sku_id, user_id)`（一人一单）与 `uk_flash_idempotency(user_id, idempotency_key)`（幂等）。插入命中唯一约束时事务回滚（连同已做的库存扣减），再按键名分流：幂等键命中返回既有订单（同 hash）或 `12005`（不同 hash），一人一单命中返回 `12004`。若删掉任一唯一约束，重复购买/重复请求会各建出第二单并重复扣库存。
- 生产代码：`internal/logic/flashsale/flashsale.go` `insertOrder`（L376-395 插入 + `duplicateKeyName` 提取键名）、`CreateOrder`（L266-274 按键名分流）、`handleIdempotency`（L406-418）；约束定义于 `internal/migrations/sql/20261001000011_flash_sale.up.sql`
- 关键测试：`TestFlashSaleOnePerUser`、`TestFlashSaleIdempotency`（`internal/cmd/flashsale_test.go`）
- 基线验证：`go test -race -run 'TestFlashSaleOnePerUser|TestFlashSaleIdempotency' ./internal/cmd/`
- 可选 Mutation：从 `flash_sale_orders` 移除 `uk_flash_one_per_user` 或 `uk_flash_idempotency` 唯一约束
- 预期失败：`TestFlashSaleOnePerUser`（第二次购买会成功建单而非 12004）或 `TestFlashSaleIdempotency`（重复请求会建第二个订单而非返回既有订单）失败
- 恢复确认：还原唯一约束后重跑上述命令，恢复通过
