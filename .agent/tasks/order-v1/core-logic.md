Owner Verification Status: PENDING

# Owner 核心逻辑验证

本次订单闭环有两处决定资金/库存正确性的核心机制。Owner 已完成首轮 Review：CL-001 已 ACCEPTED；CL-002 机制已接受、待补充并发退款回归测试后记为 ACCEPTED（详见各节状态）。

## CL-001：并发下单不超卖（条件扣减 + RowsAffected）

- 状态：ACCEPTED（Owner 确认接受：`DeductInTx` 的条件扣减 + `RowsAffected`，以及 `TestOrderConcurrentNoOversell` 对并发超卖场景的验证足够支撑该不变量）

- Owner 需要理解：下单在单事务内「写订单 + 写订单项 + 扣库存」。扣库存不是「先查后减」，而是 `UPDATE inventories SET quantity = quantity - N WHERE sku_id = ? AND quantity >= N` 再核对 `RowsAffected`。只有满足「库存 ≥ N」才命中并扣减，命中 0 行即返回库存不足(6001)并整体回滚，因此并发下单不可能把库存扣成负数、也不会超卖。若改成无条件扣减，多个并发请求会同时扣减同一份库存导致超卖。
- 生产代码：`internal/logic/inventory/inventory.go` 的 `DeductInTx`（条件更新 SQL + RowsAffected 判断），被 `internal/logic/order/order.go` 的 `insertOrder` 在单事务内逐行调用。
- 关键测试：`TestOrderConcurrentNoOversell`（`internal/cmd/order_test.go`）——并发 20 单买同一 SKU（库存 5），断言恰好 5 单成功、库存为 0。
- 基线验证：`go test -race -count=1 -run TestOrderConcurrentNoOversell ./internal/cmd/`，预期通过。
- 可选 Mutation：把 `DeductInTx` 的 SQL 从
  `"UPDATE inventories SET quantity = quantity - ? WHERE sku_id = ? AND quantity >= ?"`（参数 `qty, skuID, qty`）
  临时改为
  `"UPDATE inventories SET quantity = quantity - ? WHERE sku_id = ?"`（去掉 `quantity >= ?` 条件与第三个参数）。
- 预期失败：`TestOrderConcurrentNoOversell` 失败——成功订单数 > 5 或库存为负。
- 恢复确认：改回原 SQL 后重新运行 `go test -race -count=1 -run TestOrderConcurrentNoOversell ./internal/cmd/`，应再次通过；并确认工作区无残留改动。

## CL-002：取消/退款库存只补偿一次（条件状态更新原子闸门）

- 状态：PENDING（机制已接受，待补充并发退款回归测试后记为 ACCEPTED）
- Owner 结论：取消/退款必须通过条件状态更新 + `RowsAffected` 抢占唯一状态迁移权，只有成功迁移的一方允许执行库存补偿——该机制已确认接受。但当前验证只直接覆盖了并发取消（`TestOrderCancelRestoresInventoryOnce`），尚未直接覆盖并发退款只补偿一次。
- 待办（退回 Coder）：补充并发退款回归测试，至少验证：
  - paid 订单并发多次 refund；
  - 恰好一次成功完成 paid → refunded；
  - 库存只恢复一次；
  - 其它请求不得再次补偿库存。

- Owner 需要理解：取消（含超时自动取消）和退款恢复库存前，先用「条件状态更新 + 核对 RowsAffected」抢状态：`UPDATE orders SET status=60 WHERE id=? AND status=10`（退款为 `status=20→70`）。只有把状态真正改过来的那一方（RowsAffected=1）才继续恢复库存，其余并发/重复请求命中 0 行直接退出。这样即使多实例扫描或并发重复取消，库存也只会回补一次。若去掉状态条件，重复请求会各自再恢复一次库存，造成重复补偿。
- 生产代码：`internal/logic/order/order.go` 的 `cancelInTx`（取消，`Where("status", statusPendingPayment)` + RowsAffected 闸门）与 `Refund`（`Where("status", statusPaid)` + RowsAffected 闸门）。
- 关键测试：`TestOrderCancelRestoresInventoryOnce`（`internal/cmd/order_test.go`）——并发 8 次取消，断言仅 1 次成功、库存只恢复一次（回到原值 5）。
- 基线验证：`go test -race -count=1 -run TestOrderCancelRestoresInventoryOnce ./internal/cmd/`，预期通过。
- 可选 Mutation：把 `cancelInTx` 中 `model := tx.Model("orders").Ctx(ctx).Where("id", orderID).Where("status", statusPendingPayment)` 临时去掉 `Where("status", statusPendingPayment)`，使条件更新退化为仅按 `id` 命中。
- 预期失败：`TestOrderCancelRestoresInventoryOnce` 失败——成功取消次数 > 1 或库存被恢复多次（> 原值 5）。
- 恢复确认：恢复 `Where("status", statusPendingPayment)` 后重新运行 `go test -race -count=1 -run TestOrderCancelRestoresInventoryOnce ./internal/cmd/`，应再次通过；并确认工作区无残留改动。
