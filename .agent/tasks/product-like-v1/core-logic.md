# Owner 核心逻辑验证卡

> 说明：本文件只承载「值得 Owner 亲自理解的核心机制」验证卡与验证证据，不承载 Owner 决策状态。Owner 决策（`state.owner`）由 OwnerGate 在 Owner 明确决定后持久化。

本任务「商品点赞」是 per-user 新实体（`product_likes`）+ 新公开协议（点赞 API + 错误码域 `13000-13999`），核心正确性由两个机制决定：**并发去重**（数据一致性）与**可点赞校验**（业务规则）。以下两卡足以让 Owner 通过「代码位置 → 业务后果 → 测试失败」理解因果关系。

## CL-001：同一用户同一商品至多一条点赞（唯一约束兜底并发）

- Owner 需要理解：同一用户对同一商品重复/并发点赞，最终库中必须**只有一条**记录；如果并发时没有唯一约束兜底，会落多条脏数据，导致公开点赞数被污染。实现用 `uk_user_product(user_id, product_id)` 唯一约束兜底，命中 `1062`（重复键）视为幂等成功。
- 生产代码：`internal/logic/like/like.go` 的 `Like`（单条 `INSERT`，`isDuplicateKeyError` 识别 1062 后幂等成功）+ `internal/migrations/sql/20261001000013_product_likes.up.sql`（`UNIQUE KEY uk_user_product (user_id, product_id)`）。
- 关键测试：`internal/cmd/like_test.go` 的 `TestLikeDuplicateIdempotent`（串行重复仅 1 条）、`TestLikeConcurrentDuplicateSingleRow`（`-race` 下 8 并发均成功、库中仅 1 条）。
- 基线验证：`go test -race -run 'TestLikeDuplicateIdempotent|TestLikeConcurrentDuplicateSingleRow' ./internal/cmd/ -v` → 预期 `PASS`。
- 可选 Mutation：临时把 `Like` 中重复键的幂等处理去掉（让 `isDuplicateKeyError(err)` 恒为 `false`，即 1062 也当作错误返回 500）。
- 预期失败：`TestLikeDuplicateIdempotent` 与 `TestLikeConcurrentDuplicateSingleRow` 必须失败（第二次/并发点赞不再幂等成功）。
- 恢复确认：还原 `isDuplicateKeyError` 判断，重新运行上述命令 → 恢复 `PASS`。

## CL-002：点赞仅允许「存在且在售」商品（否则拒绝且无写入）

- Owner 需要理解：点赞目标必须满足「商品存在且 `status=on_shelf`」；不存在 → 404（`4001`）、非在售（draft/off_shelf）→ 409（`13001`），且**不产生任何写入**。这是点赞与收藏同构、但独立承载的校验规则。
- 生产代码：`internal/logic/like/like.go` 的 `Like`（先 `IProduct.GetByID` 校验存在性，再校验 `product.Status == on_shelf`）+ `internal/codes/codes.go`（`CodeLikeProductUnavailable=13001` → 409）。
- 关键测试：`internal/cmd/like_test.go` 的 `TestLikeProductValidation`（不存在 404/4001、off_shelf/draft 409/13001、非法 `product_id` 400/1001，且 `product_likes` 行数为 0）。
- 基线验证：`go test -run 'TestLikeProductValidation' ./internal/cmd/ -v` → 预期 `PASS`。
- 可选 Mutation：临时把可点赞校验放宽（例如将 `product.Status != productv1.StatusOnShelf` 判断去掉，使 draft 商品也可点赞）。
- 预期失败：`TestLikeProductValidation` 必须失败（draft/off_shelf 商品点赞返回 200 而非期望的 409/13001）。
- 恢复确认：还原 `on_shelf` 校验，重新运行上述命令 → 恢复 `PASS`。
