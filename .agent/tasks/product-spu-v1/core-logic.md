# Core Logic

Owner 无需逐行 Review 全部 Diff。以下两张验证卡覆盖本任务真正决定业务正确性的两条机制：状态迁移的并发一致性与商品/图片的事务原子性。

## CL-001：状态迁移并发一致性（条件 UPDATE + RowsAffected）

- Owner 需要理解：商品状态只允许 `draft→on_shelf`、`on_shelf→off_shelf`、`off_shelf→on_shelf` 三条迁移。上/下架用 `UPDATE products SET status=? WHERE id=? AND status IN (...) ` 的**条件更新**并核对 `RowsAffected`，把并发重复操作收敛为「最多一次成功、其余 409」。若改成「先查后写 + 无条件 UPDATE」，并发重复上/下架会多次成功，状态机失去原子性。
- 生产代码：`internal/logic/product/product.go:295-336`（`transition`，上架 `WhereIn("status", {0,2})`、下架 `Where("status", 1)`，`affected==0 → 409 CodeProductInvalidStatusTransition`）
- 关键测试：`TestProductStateMachine`、`TestProductConcurrentTransition`（`internal/controller/product/product_test.go`）
- 基线验证：`go test -p 1 -run 'TestProductStateMachine|TestProductConcurrentTransition' ./internal/controller/product/`（预期 PASS）
- 可选 Mutation：把上架的条件更新去掉状态约束（`WhereIn("status", {0,2})` 改为无条件 `Where("id", id)`），或删掉 `affected==0` 的 409 分支。
- 预期失败：`TestProductConcurrentTransition` 会因 `success != 1` 失败（10 个并发全部成功）；`TestProductStateMachine` 的 `on_shelf→on_shelf` 非法迁移断言也会失败。
- 恢复确认：恢复条件 UPDATE 与 `RowsAffected` 判定，重新运行上述命令应回到 PASS。

## CL-002：商品与图片事务原子性（INV-007）

- Owner 需要理解：创建/更新时 `products` 与 `product_images` 必须在同一事务内写入；更新图片采用「删旧 + 重插」的全量替换，任一步失败整体回滚。若图片写入移出事务，失败时会留下「商品已改名但图片未变」或「图片删除但商品未变」的半成品，破坏数据一致性。
- 生产代码：`internal/logic/product/product.go:142-163`（Create 事务）、`237-254`（Update 事务）、`516-521`（`replaceProductImages` 删旧重插）
- 关键测试：`TestProductUpdate` 的「图片写入失败回滚」断言（`internal/controller/product/product_test.go:852-866`，用 URL 超 `VARCHAR(512)` 触发 DB 错误，断言 500/1000 且 name 与 images 均保持原值）
- 基线验证：`go test -p 1 -run 'TestProductUpdate' ./internal/controller/product/`（预期 PASS）
- 可选 Mutation：把 `replaceProductImages` 移出事务（在 `g.DB().Transaction` 之外单独执行），或把事务内的商品 `Update` 与图片替换拆成两次独立提交。
- 预期失败：`TestProductUpdate` 的图片失败用例会失败（商品名已变为「不应生效」未回滚）。
- 恢复确认：恢复「商品更新 + 图片替换」在同一 `Transaction` 内，重新运行上述命令应回到 PASS。
