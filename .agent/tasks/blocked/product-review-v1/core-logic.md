# Core Logic 验证卡（Owner 核心逻辑）

本文整理「商品评价」真正决定业务不变量、需要 Owner 理解并亲自验证的核心机制。只含能区分正确与错误实现的验证入口；普通 CRUD、DTO 字段搬运不列入。

## CL-001：每个订单项最多一次评价（并发唯一约束兜底）

- Owner 需要理解：同一 `order_item_id` 至多只能产生一条评价，即使并发重复提交也只允许 1 条落库，否则「已购项一次评价」的业务语义与公开汇总的 `count` 都会被污染；删除是软删除，唯一槽位永久保留、不允许重新评价。
- 生产代码：`internal/logic/review/review.go` 的 `Create`（`InsertAndGetId` + `isDuplicateKeyError`）与 `internal/migrations/sql/20261001000009_reviews.up.sql` 的 `UNIQUE KEY uk_order_item (order_item_id)`。
- 关键测试：`internal/cmd/review_test.go` 的 `TestReviewConcurrentDuplicateSingleRow`（8 并发仅 1 成功）与 `TestReviewDuplicateRejected`（顺序重复 409/10003）。
- 基线验证：`go test -p 1 ./internal/cmd/ -run 'TestReviewConcurrentDuplicateSingleRow|TestReviewDuplicateRejected' -count=1 -race`，预期 `ok`。
- 可选 Mutation：将 `Create` 中 `isDuplicateKeyError(err)` 判定的返回从 `CodeReviewAlreadyExists` 改为 `CodeInternalError`（或临时去掉唯一约束），破坏「重复/并发提交不产生第二条」的兜底语义。
- 预期失败：`TestReviewConcurrentDuplicateSingleRow` 会因成功数 `!= 1`（或 `reviewCount != 1`）而 FAIL。
- 恢复确认：撤销 Mutation 后再次运行同一命令，预期重新 `ok`，并 `git status` 确认无遗留改动。

## CL-002：购买资格校验 + 服务端归属绑定

- Owner 需要理解：评价归属（`user_id/product_id/sku_id/order_item_id`）必须全部由服务端从 `Principal.UserID` 与「订单项 + 订单」推导，且只有「订单项属于当前用户 + 订单状态已完成(50)」才允许提交；否则他人可借伪造字段或他人订单项代评，破坏评价真实性与归属隔离。
- 生产代码：`internal/logic/review/review.go` 的 `loadOwnedCompletedItem`（`InnerJoin orders` + 校验 `item.UserId == userID && item.OrderStatus == 50`）与 `Create`（`user_id` 取 `Principal.UserID`、`product_id/sku_id` 取订单项快照）。
- 关键测试：`internal/cmd/review_test.go` 的 `TestReviewEligibilityRejected`（他人/未完成/不存在均 409/10002 且无写入）与 `TestReviewServerDerivesOwnership`（伪造归属字段被忽略）。
- 基线验证：`go test -p 1 ./internal/cmd/ -run 'TestReviewEligibilityRejected|TestReviewServerDerivesOwnership' -count=1`，预期 `ok`。
- 可选 Mutation：把 `orderStatusCompleted` 常量从 `50` 改为 `10`（待支付即可评），或把 `item.UserId != userID` 判定条件删除。
- 预期失败：`TestReviewEligibilityRejected` 会因「待支付订单也能评价」而 FAIL；删除归属校验后 `TestReviewServerDerivesOwnership` 或隔离测试会 FAIL。
- 恢复确认：撤销 Mutation 后再次运行同一命令，预期重新 `ok`，并 `git status` 确认无遗留改动。
