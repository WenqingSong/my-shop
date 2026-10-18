# Core Logic（Owner 验证入口）

本任务真正决定库存正确性的核心机制有两个：条件扣减防负库存、以及「库存变更 + 流水」同事务的 before/after 一致性。以下验证卡让 Owner 通过「代码位置 → 业务后果 → 测试失败」理解因果，无需逐行 Review 全部 Diff。

## CL-001：条件扣减 + 防负库存（条件更新 + RowsAffected）

- Owner 需要理解：扣减必须**原子地**只在 `quantity >= N` 时生效。若实现退化为「先查再写」或「无条件扣减」，并发下会超卖、库存变负，或把不足的扣减也当成成功，直接破坏 AC-003/004/005/007 与 INV-001/002。
- 生产代码：`internal/logic/inventory/inventory.go` 的 `Deduct`（109-148 行），核心是 121-124 行的条件更新与 128-130 行的 `RowsAffected` 判定：
  - `UPDATE inventories SET quantity = quantity - ? WHERE sku_id = ? AND quantity >= ?`；
  - `affected == 0` → 返回 `6001`（库存不足，事务回滚、不写流水）。
- 关键测试：
  - `TestInventoryDeductInsufficient`（`internal/controller/inventory/inventory_test.go:405`）：不足扣减断言 409/6001、库存不变、无扣减流水。
  - `TestInventoryConcurrentDeduct`（同文件 484）：20 goroutine 各扣 10（初始 100），断言成功恰好 10、失败 10、最终 0、流水 11。
- 基线验证：`go test -p 1 ./internal/controller/inventory/ -race -count=1` → 全部 PASS。
- 可选 Mutation：把 `Deduct` 的 UPDATE 条件 `AND quantity >= ?` 删除（改为无条件扣减，去掉第三个参数）。
- 预期失败：`TestInventoryDeductInsufficient` 失败（扣 6 从 5 不再返回 6001）；`TestInventoryConcurrentDeduct` 失败（成功次数 > 10，最终库存不再为 0，甚至为负/报错）。
- 恢复确认：恢复 `AND quantity >= ?` 与 `RowsAffected` 判定，重跑 `go test -p 1 ./internal/controller/inventory/ -run 'TestInventoryDeductInsufficient|TestInventoryConcurrentDeduct' -count=1` → PASS。

## CL-002：库存变更与流水同事务、before/after 一致（INV-003）

- Owner 需要理解：每次成功变更必须与一条流水**同事务**提交，且 `after_qty = before_qty ± change_qty`、`after_qty` 等于变更后当前库存。若流水写错 before/after，或把流水写到事务外，审计流水与真实库存脱节，无法回溯库存变化。
- 生产代码：`internal/logic/inventory/inventory.go`
  - `Increase`（74-107 行）：事务内 upsert 后读回最新值，97 行写流水 `before = rec.Quantity - qty`、`after = rec.Quantity`。
  - `Deduct`（109-148 行）：138 行写流水 `before = rec.Quantity + qty`、`after = rec.Quantity`。
  - `insertLog`（203-219 行）：在事务内 `tx.Model("inventory_logs").Insert`，与库存变更同事务。
- 关键测试：
  - `TestInventoryDeductSuccess`（`inventory_test.go:378`）：断言扣减流水 before=30/after=18。
  - `TestInventoryIncreaseInitAndAccumulate`（同文件 338）：断言增加流水 before/after 链 0→10→25。
  - `TestInventoryLogsOrdering`（同文件 446）：多次变更后流水 id 倒序、字段完整、operator 记录。
- 基线验证：`go test -p 1 ./internal/controller/inventory/ -race -count=1` → 全部 PASS。
- 可选 Mutation：把 `Deduct` 里 138 行的 `before` 参数由 `rec.Quantity+qty` 改成 `rec.Quantity`（使 before == after，破坏 `after = before - change` 不变式）。
- 预期失败：`TestInventoryDeductSuccess` 失败（期望 before=30，实得 18）。
- 恢复确认：恢复 `rec.Quantity+qty`，重跑 `go test -p 1 ./internal/controller/inventory/ -run 'TestInventoryDeductSuccess|TestInventoryLogsOrdering' -count=1` → PASS。
