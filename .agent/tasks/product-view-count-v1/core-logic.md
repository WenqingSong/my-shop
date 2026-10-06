# Core Logic（Owner 核心验证卡）

Cleaner 审查结论：CLEAN（target `df272012781fa29120f5a1d5aec09cbde041d02b`）。

以下为真正决定本任务业务正确性的两个机制，Owner 可据此用「代码位置 → 业务后果 → 测试失败」理解不变量。

## CL-001：单条条件 UPDATE 原子自增（计数不丢失 + 404 无写入）

- Owner 需要理解：浏览量的并发正确性与「不存在/非上架不计数」都由**同一条** `UPDATE products SET view_count = view_count + 1, updated_at = updated_at WHERE id = ? AND status = ?` 保证。它把「可见性校验 + 计数 + 404 判定」合并为一次原子写：InnoDB 行锁串行化并发自增（无 lost update），`RowsAffected=0` 表示商品不存在或非 `on_shelf`（返回 404 且无写入）。若改成「先 SELECT 再 UPDATE」的读改写，并发下会丢失计数；若去掉 `status` 条件，`draft/off_shelf` 会被误计数。
- 生产代码：`internal/logic/product/product.go:102-112`（`Detail` 的计数 UPDATE + `RowsAffected` 判定）
- 关键测试：`TestProductViewCountConcurrent`（`internal/controller/product/product_test.go:1051`）、`TestProductViewCountNoWriteOn404`（`product_test.go:1016`）
- 基线验证：`go test ./internal/controller/product/ -race -count=1 -run 'TestProductViewCountConcurrent|TestProductViewCountNoWriteOn404'` → 预期 `ok`
- 可选 Mutation：把 `SET view_count = view_count + 1` 改为「先 `load` 读值再 `UPDATE ... SET view_count = <读到的值+1>`」（读改写）
- 预期失败：`TestProductViewCountConcurrent` 断言最终 `view_count==50` 失败（并发丢失）；或把 `WHERE id = ? AND status = ?` 去掉 `status` 条件后，`TestProductViewCountNoWriteOn404` 对 `off_shelf/draft` 的「404 且无写入」断言失败
- 恢复确认：还原为单条原子自增 UPDATE 后，重跑上述命令恢复 `ok`

## CL-002：`updated_at = updated_at` 保护（浏览不刷新「最近更新」）

- Owner 需要理解：`products.updated_at` 带 `ON UPDATE CURRENT_TIMESTAMP`，任何不显式赋值的 UPDATE 都会把它刷成「最近浏览时间」，从而污染列表按 `updated_at` 排序的既有语义。实现显式写 `updated_at = updated_at` 使自增只改 `view_count`、不动时间戳。
- 生产代码：`internal/logic/product/product.go:104`（UPDATE 子句中的 `updated_at = updated_at`）
- 关键测试：`TestProductViewCount`（`product_test.go:950`，含 `time.Sleep(1100ms)` 跨秒后断言 `updated_at` 不变）
- 基线验证：`go test ./internal/controller/product/ -count=1 -run '^TestProductViewCount$'` → 预期 `ok`
- 可选 Mutation：从 UPDATE 中删除 `, updated_at = updated_at`
- 预期失败：`TestProductViewCount` 的「updated_at should not change on view」断言失败（浏览把 `updated_at` 刷新到新一秒）
- 恢复确认：补回 `updated_at = updated_at` 后重跑恢复 `ok`
