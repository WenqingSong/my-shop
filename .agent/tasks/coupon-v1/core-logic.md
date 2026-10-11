# Owner 核心逻辑验证

以下验证卡让 Owner 通过「代码位置 → 业务后果 → 测试失败」理解优惠券 V1 的两个核心不变量。普通 DTO、字段搬运与样板 CRUD 不列入。

## CL-001：并发领取不超发 / 不重复（原子闸门 + 唯一约束）

- Owner 需要理解：领取 = 单事务「先条件原子 +1 `issued_quantity`（`WHERE issued_quantity < total_quantity`）→ 再插入用户券（`uk_user_template` 唯一约束）」，任一步失败整体回滚。破坏该顺序或条件会导致并发超发或同一用户重复领券，造成券总量被透支。
- 生产代码：`internal/logic/coupon/coupon.go` `Claim()`（原子闸门第 199-211 行、插入与 1062 分流第 213-226 行）。
- 关键测试：`internal/cmd/coupon_test.go` `TestCouponConcurrentClaim`（第 313 行）、`TestCouponClaimRejections`（第 262 行）。
- 基线验证：`go test -race -run 'TestCouponConcurrentClaim|TestCouponClaimRejections' ./internal/cmd/` → 预期 `ok`。
- 可选 Mutation：将原子闸门 UPDATE 的 `issued_quantity < total_quantity` 条件删除（改为无条件 +1）。
- 预期失败：`TestCouponConcurrentClaim` 中「10 用户抢 5 张」成功数将 > 5，断言 `okCount == total` 失败。
- 恢复确认：还原 `issued_quantity < total_quantity` 条件后重跑 `go test -race -run 'TestCouponConcurrentClaim' ./internal/cmd/` 通过。

## CL-002：订单与券同事务预占 / 并发不重复占券（LockInTx 原子闸门）

- Owner 需要理解：创建订单与券预占在同一 MySQL 事务内；预占是 `UPDATE user_coupons SET status=locked ... WHERE id=? AND user_id=? AND status=available`，`RowsAffected=0` 即返回 19007 并整体回滚（订单、库存、券都不落库）。破坏 `status=available` 条件会让同一张券被多个订单同时占用，导致一券多用。
- 生产代码：`internal/logic/coupon/coupon.go` `LockInTx()`（第 319-332 行）；`internal/logic/order/order.go` `insertOrder()` 第 347-352 行（在扣库存之后、事务内调用预占）。
- 关键测试：`internal/cmd/coupon_test.go` `TestCouponConcurrentOrderSameCoupon`（第 597 行）、`TestCouponOrderRollbackOnFailure`（第 561 行）。
- 基线验证：`go test -race -run 'TestCouponConcurrentOrderSameCoupon|TestCouponOrderRollbackOnFailure' ./internal/cmd/` → 预期 `ok`。
- 可选 Mutation：将 `LockInTx` 的 `WHERE ... AND status = ?`（available）删除状态条件。
- 预期失败：`TestCouponConcurrentOrderSameCoupon` 中两个并发订单都会成功占券，`okCount == 1` 断言失败。
- 恢复确认：还原 `status` 条件后重跑 `go test -race -run 'TestCouponConcurrentOrderSameCoupon' ./internal/cmd/` 通过。
