# Owner 核心逻辑验证

## CL-001：用户隔离与越权 404（INV-001）

- Owner 需要理解：购物车条目只归属 `Principal.UserID`，所有读写在 `user_id` 维度过滤；改数量/勾选/删除按 `id AND user_id` 定位，未命中统一 404（防枚举，不泄露他人条目是否存在）。若过滤缺失，用户 B 可直接改/删用户 A 的条目，属越权数据破坏。
- 生产代码：`internal/logic/cart/cart.go` 的 `List`（`Where("ci.user_id", userID)`）、`UpdateQuantity`/`UpdateSelected`/`Delete`（`Where("id", itemID).Where("user_id", userID)` + `RowsAffected`/`itemExists`）。
- 关键测试：`TestCartUserIsolation`（`internal/controller/cart/cart_test.go`）。
- 基线验证：`go test -run TestCartUserIsolation -v ./internal/controller/cart/` → PASS。
- 可选 Mutation：删除 `UpdateQuantity` 的 `.Where("user_id", userID)`（仅保留 `Where("id", itemID)`）。
- 预期失败：`TestCartUserIsolation` 中「用户 B 修改 A 的条目」由 404 变为 200 且 A 的数量被篡改，断言 `res.Status != 404 || res.Code != 7001` 失败。
- 恢复确认：恢复 `.Where("user_id", userID)`，重跑 `go test -run TestCartUserIsolation -v ./internal/controller/cart/` 通过。

## CL-002：并发原子累加与上限（INV-002）

- Owner 需要理解：同一用户同一 SKU 至多一条（`uk_user_sku` 兜底），重复加购用单语句原子累加 `INSERT ... AS new ON DUPLICATE KEY UPDATE cart_items.quantity = cart_items.quantity + new.quantity`，累加后超 999 回滚拒绝、不静默截断。若改成无锁「先 SELECT 再 UPDATE」，并发加购会丢失更新（最终数量少于各次之和）。
- 生产代码：`internal/logic/cart/cart.go:80` `Add`（事务内原子 upsert + 读回校验上限）。
- 关键测试：`TestCartConcurrentAdd`（20×5=100 断言无丢失更新）、`TestCartAddAccumulate`（3+5=8 单条）、`TestCartQuantityBoundary`（累加超限 400 且原值不变）。
- 基线验证：`go test -race -run 'TestCartConcurrentAdd|TestCartAddAccumulate|TestCartQuantityBoundary' -v ./internal/controller/cart/` → PASS。
- 可选 Mutation：把 `Add` 的 upsert 改为先 `SELECT quantity` 再 `UPDATE quantity = 读到的值 + N`（去掉 `ON DUPLICATE KEY UPDATE` 原子累加）。
- 预期失败：`TestCartConcurrentAdd` 最终数量 < 100（并发丢失更新）断言失败。
- 恢复确认：恢复原子 upsert，重跑上述测试通过。
