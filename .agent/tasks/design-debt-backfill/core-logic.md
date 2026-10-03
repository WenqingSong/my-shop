# Core Logic

本任务为文档回填任务，交付物是 `docs/design/*`，不改生产代码/测试。因此 Owner 需要亲自验证的核心逻辑，是 **Design 文档是否忠实反映了真正决定系统正确性的机制**，而非审查新代码。

以下验证卡让 Owner 通过「Design 陈述 → 生产代码 → 业务后果 → 测试」理解并确认最关键的机制。这两项是本系统决定「库存一致性」与「状态流转正确性」的核心，也是本次回填最需要保证不被写错的地方。

## CL-001：库存条件扣减与防负库存

- Owner 需要理解：扣减必须「充足才成功、绝不扣成负数」，且每次成功变更与流水在同一事务内落库。若 Design 把扣减写成「先查后写」或宣称幂等，会掩盖真实的并发正确性语义，误导后续订单模块复用。
- 生产代码：`internal/logic/inventory/inventory.go` 的 `Deduct`（`UPDATE inventories SET quantity = quantity - ? WHERE sku_id = ? AND quantity >= ?` + 核对 `RowsAffected`，命中 0 行返回 6001 且事务回滚不写流水）。
- 关键测试：`internal/controller/inventory/inventory_test.go` 的 `TestInventoryDeductInsufficient`、`TestInventoryConcurrentDeduct`（20 并发各扣 10、初始 100，断言恰好 10 次成功、10 次 6001、最终 quantity=0、流水数 = 1 增 + 10 扣）。
- 基线验证：`docs/design/inventory.md` 第 3/4 节对「条件更新 + `RowsAffected`」「防负库存」「同事务流水」「非幂等」的表述，须与 `inventory.go:111-148` 完全一致；可运行 `go test -count=1 -run 'TestInventoryDeduct|TestInventoryConcurrentDeduct' ./internal/controller/inventory/`（需 MySQL 容器就绪）预期通过。
- 可选 Mutation（文档侧，可逆）：把 `inventory.md` 的扣减表述临时改为「先 SELECT 剩余量再写回」，对照 `inventory.go` 会立即发现实现用的是单条条件 UPDATE + `RowsAffected`，而非 check-then-act。
- 预期失败：上述错误表述与 `inventory.go:121-130` 的「条件更新 + `RowsAffected==0 → 6001」矛盾，Owner 对照代码即可发现。
- 恢复确认：改回 `inventory.md` 原始表述，再次通读确认与 `inventory.go` 一致。

## CL-002：商品状态机条件 UPDATE 并发迁移

- Owner 需要理解：上下架用「条件 UPDATE（`WHERE status IN (0,2)` / `WHERE status=1`）+ `RowsAffected`」保证并发重复迁移最多一次成功，其余归为 409，最终状态正确。若 Design 把迁移写成「先查再写」或说成三态可任意跳转，会破坏并发正确性与状态机不变量。
- 生产代码：`internal/logic/product/product.go` 的 `transition`（`OnShelf` 用 `WhereIn("status", [draft,off_shelf])`、`OffShelf` 用 `Where("status", on_shelf)`，`RowsAffected==0` → `CodeProductInvalidStatusTransition` 409）；上架前重新校验分类「存在+叶子+enabled」。
- 关键测试：`internal/controller/product/product_test.go` 的 `TestProductStateMachine`、`TestProductConcurrentTransition`（10 并发上架，断言恰好 1 次成功、9 次 4005、最终 `status=on_shelf`）。
- 基线验证：`docs/design/product.md` 第 3 节状态机与「条件 UPDATE + `RowsAffected`」表述须与 `product.go:312-353` 一致；`TestProductConcurrentTransition` 已锁定「并发最多一次成功」，可运行 `go test -count=1 -run 'TestProductStateMachine|TestProductConcurrentTransition' ./internal/controller/product/`（需 MySQL 容器就绪）预期通过。
- 可选 Mutation（文档侧，可逆）：把 `product.md` 的迁移表述临时改为「上架直接 `SET status=1` 无条件更新」，对照 `product.go:330-350` 会立即发现实现有 `WHERE status IN (0,2)` / `WHERE status=1` 条件与 `RowsAffected` 判定。
- 预期失败：上述错误表述与 `product.go` 条件 UPDATE 矛盾，Owner 对照代码即可发现。
- 恢复确认：改回 `product.md` 原始表述，再次通读确认与 `product.go` 一致。
