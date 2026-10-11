# Owner Core Logic 验证卡

本文件由 Cleaner 在 `CLEAN` 后整理，供 OwnerGate 提炼核心机制并向 Owner 解释。仅承载验证卡与因果说明，不承担 Owner 决策状态权威。

## CL-001：退款成功一致性 ——「订单 20→70 闸门 + 库存只恢复一次 + 退款→60」同事务

- Owner 需要理解：退款成功是唯一把订单从 `paid(20)` 推进到 `refunded(70)` 并恢复库存的路径；若这个闸门不严，会出现「重复退款/重复恢复库存」或「已发货又退款成功」的资损。核心是 `WHERE status=20` 条件更新的 `RowsAffected` 原子闸门——只有把订单从 20 改成 70 的那一个事务才有资格恢复库存，其余一律整体回滚。
- 生产代码：`internal/logic/refund/refund.go` 的 `Execute`（success 分支，先 `service.Order().CompleteRefundInTx` 再锁退款推进）；`internal/logic/order/order.go` 的 `CompleteRefundInTx`（`UPDATE orders SET status=70 WHERE status=20` + `RowsAffected==0 → error` + `IInventory.IncreaseInTx` 恢复库存）。
- 关键测试：`internal/cmd/refund_test.go` 的 `TestRefundSuccessConsistency`（订单=70、库存恢复一次、refund=60、重复执行库存不再变、退款后发货 9002）、`TestRefundRetrySuccessAfterFailure`（30→60 库存恢复一次）。
- 基线验证：`go test ./internal/cmd/ -run 'TestRefundSuccessConsistency|TestRefundRetrySuccessAfterFailure' -count=1`，预期 `ok`。
- 可选 Mutation：把 `CompleteRefundInTx` 的 `Where("status", statusPaid)` 去掉（或把 `RowsAffected==0` 的分支改成忽略错误继续恢复库存）。
- 预期失败：去掉闸门后，`TestRefundSuccessConsistency` 的「重复执行库存仍=10」断言会失败（第二次执行会再次 `IncreaseInTx`，库存超恢复到 12），证明「只补偿一次」不变量受此测试保护。
- 恢复确认：还原 `CompleteRefundInTx` 后重跑 `go test ./internal/cmd/ -run 'TestRefundSuccessConsistency' -count=1`，预期 `ok`。

## CL-002：退款与发货互斥 —— 同一 `orders` 行锁串行化「申请 / 发货 / 退款成功」

- Owner 需要理解：退款申请、发货、退款成功三者必须对同一订单串行，否则会出现「既已发货又退款成功」或「已发货后冒出有效退款申请」。核心是三方都先锁同一 `orders` 行（`SELECT ... FOR UPDATE` 或 `UPDATE ... WHERE status=20`），发货侧再在事务内检查是否存在有效退款申请（status ∈ {10,20,30}）。
- 生产代码：`internal/logic/refund/refund.go` 的 `Apply`（`lockOrderInTx` FOR UPDATE）与 `HasActiveRequestInTx`；`internal/logic/order/order.go` 的 `Ship`（事务内 `LockUpdate` + `HasActiveRequestInTx` 拒绝 18005 + `UPDATE status=30`）。
- 关键测试：`internal/cmd/refund_test.go` 的 `TestRefundShipMutualExclusion`（有效申请时发货 18005；并发「申请+发货」不双成功；最终不「已发货+有效退款」）。
- 基线验证：`go test ./internal/cmd/ -run 'TestRefundShipMutualExclusion' -count=1`，预期 `ok`。
- 可选 Mutation：删除 `Ship` 事务内对 `service.Refund().HasActiveRequestInTx` 的检查。
- 预期失败：删除后，`TestRefundShipMutualExclusion` 场景 1（有申请中退款时发货应 18005）会失败——发货将直接成功，出现「已发货 + 申请中退款」，证明互斥不变量受此测试保护。
- 恢复确认：还原 `Ship` 的 `HasActiveRequestInTx` 检查后重跑 `go test ./internal/cmd/ -run 'TestRefundShipMutualExclusion' -count=1`，预期 `ok`。
