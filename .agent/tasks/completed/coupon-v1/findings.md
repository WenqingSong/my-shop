# Cleaner Findings

## Review Target

- 任务：`coupon-v1`（优惠券 V1 核心闭环）。
- 审查对象 C1（implementation Evidence Commit）：`d4bbf7b0735674a8a0be80e3f0d2bd6c80feae86`（`feat(coupon): 实现优惠券 V1（模板管理/领取/满减用券/订单联动）`）。
- 当前分支：`feat/coupon-v1`；当前 HEAD（C2，Coder 的 metadata commit，不在审查对象内）：`892d25636e998f3e1ac590cd3b06a91e30e1b3ae`。
- 分支基点：`f14a88a`（`chore(registry): order-refund-v1 预留退款错误码域 18000-18999 与 migration 20261001000020`，即 develop 当时 tip）。
  - 注：`task.md` 的 Review Baseline 记录为 `15bcc4be`（任务创建时检出于 `main`），但 feature 分支实际由 Owner 从 `develop` 的 `f14a88a` 创建，二者共享祖先 `3ad3229`。任务的实现变更以 C1 的 16 文件 diff 为准，可与分支基点可靠区分，不构成 BLOCKED。
- 实现 diff 范围（`6f396c0..d4bbf7b`，16 文件 +1861/-26）：
  - 新增：`api/coupon/v1/coupon.go`、`internal/controller/coupon/coupon.go`、`internal/logic/coupon/coupon.go`、`internal/service/coupon.go`、`internal/cmd/coupon_test.go`、`internal/migrations/sql/20261001000021_coupon.up.sql`、`internal/migrations/sql/20261001000022_order_coupon_amounts.up.sql`。
  - 修改：`api/order/v1/order.go`、`internal/logic/order/order.go`、`internal/codes/codes.go`、`internal/boot/seed.go`、`internal/cmd/routes_admin.go`、`internal/cmd/routes_frontend.go`、`internal/logic/logic.go`、`internal/migrations/migrations_test.go`、`internal/boot/boot_migration_test.go`。
- 全局资源（三边一致性已核对）：
  - Registry（`origin/develop`）：`19000-19999`（coupon-v1 RESERVED）、`20261001000021`（coupon RESERVED）、`20261001000022`（order_coupon_amounts RESERVED）。
  - Contract：`19000-19999`、`20261001000021`、`20261001000022`（一致）。
  - 实现：`codes.go` 用 19001-19007（落 19000-19999 域内）；迁移文件 `20261001000021_coupon.up.sql`、`20261001000022_order_coupon_amounts.up.sql`（一致）。
- `docs/design/*`：`docs/design/coupon.md`（新增）、`docs/design/order.md`（金额快照语义）、`docs/design/dashboard.md`（口径标注），均与 APPROVED Contract 及最终实现一致。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestCouponAdminTemplateManagement`：超管创建/查询/停止模板，无权限管理员与普通用户 403 且无写入。 |
| AC-002 | PASS | `TestCouponClaimAndListMine`：领取后「我的券」可见且状态 available。 |
| AC-003 | PASS | `TestCouponClaimRejections`：重复领取 19004 且 issued 不增量。 |
| AC-004 | PASS | `TestCouponClaimRejections`：未开始/已结束/已停止 19005 且无写入。 |
| AC-005 | PASS | `TestCouponClaimRejections`：总量已满 19006。 |
| AC-006 | PASS | `TestCouponConcurrentClaim`（-race）：10 用户抢 5 张恰好 5 成功、单用户 10 并发至多 1 成功。 |
| AC-007 | PASS | `TestCouponUserIsolation`：他人券 19003 且无写入、他人列表不可见。 |
| AC-008 | PASS | `TestCouponOrderThresholdNotMet`（门槛）、`TestCouponOrderExpiredCoupon`（过期）、`TestCouponUserIsolation`（归属）；「已使用」复用由 `LockInTx` 的 `status=available` 闸门拦截（见 CLEAN-001 P3 测试缺口）。 |
| AC-009 | PASS | `TestCouponOrderWithDiscount`：original/discount/total 由服务端计算且 discount≤original。 |
| AC-010 | PASS | `TestCouponOrderNoCouponCompat`：无券 original=total、discount=0。 |
| AC-011 | PASS | `TestCouponOrderSnapshotStable`：改价/改模板后订单快照不变。 |
| AC-012 | PASS | `TestCouponOrderRollbackOnFailure`：库存不足整体回滚、券不残留 locked。 |
| AC-013 | PASS | `TestCouponConcurrentOrderSameCoupon`（-race）：并发用同一张券至多 1 单成功。 |
| AC-014 | PASS | `TestCouponIdempotencyWithCoupon`：同键同券幂等返回同一订单、同键不同券 9006。 |
| AC-015 | PASS | `TestCouponPayMarkUsed`：支付核销 locked→used、重复支付不重复核销。 |
| AC-016 | PASS | `TestCouponCancelRelease`：取消释放 locked→available 且库存恢复；超时取消复用同一 `cancelInTx`（见 CLEAN-001 备注）。 |
| AC-017 | PASS | `TestCouponRefundNoReturn`：退款不返券（券保持 used）、库存恢复。 |
| AC-018 | PASS | 状态联动均走「条件状态更新 + RowsAffected」原子闸门；并发领取/并发占券测试 -race 通过，状态机单步推进测试覆盖支付/取消/退款。 |
| AC-019 | PASS | `go test -p 1 ./...` 全绿（含 order/dashboard/iam/rbac/migrations）。 |
| AC-020 | PASS | Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ Registry ↔ 实现多方一致（数据模型、状态机、不变量、错误码域 19000-19999、权限 code `coupon:*`）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 无错误。 |
| `go vet ./...` | PASS | 无错误。 |
| `gofmt -l`（变更文件） | PASS | 无输出（均已格式化）。 |
| `go test -race -run 'TestCoupon' ./internal/cmd/` | PASS | `ok ... 58.346s`（并发 AC 走真实 MySQL 事务 + race）。 |
| `go test -p 1 ./...` | PASS | 全包通过，含 `internal/cmd`(79.7s)、`internal/migrations`(21.2s)、`internal/boot`(11.7s) 等。 |
| `scripts/check-registry.sh` | 预期告警 | 本地 Registry（feature 分支，未含 coupon 预留条目）报 9 处「不一致」；预留实际已在 `origin/develop` 生效（`e2f82f6`），以 `origin/develop` Registry 核对三边一致，非漂移。 |

## Findings

### CLEAN-001：「已使用」优惠券复用缺少直接测试（P3）

- Severity：P3
- Status：OPEN
- Location：`internal/cmd/coupon_test.go`
- AC / Invariant：AC-008（「过期/已使用不能复用」）／INV-C03（券状态机 `used` 为终态）
- Trigger：用户用一张已核销（`status=30`）的券再次下单。
- Actual：`LockInTx` 的 `WHERE status=available` 条件更新会返回 RowsAffected=0 → 19007，整体回滚（行为正确）；但现有测试只覆盖「被占用 locked」路径（`TestCouponConcurrentOrderSameCoupon`），未直接覆盖「已使用 used」终态复用路径。
- Expected：应有一个用例把券推进到 `used` 后再次下单，断言 19007 且无写入。
- Impact：低——当前行为正确、且与 locked 共用同一 `status=available` 闸门；但任务「必要测试」明确列出「已使用不能复用」，该分支当前无回归保护，若将来误改 `LockInTx` 状态条件（如放宽为 `status IN (available, used)`）将无人拦截。
- Evidence：全量 grep 测试函数，无「used 券再次下单」用例；`TestCouponPayMarkUsed`/`TestCouponRefundNoReturn` 均未在新订单中使用 used 券。
- Required Fix Boundary：新增一个用例：领取→下单→支付（券 used）→ 用同一券再次下单，断言 19007 且券保持 used、无新订单/无库存变化。此 Finding 不阻塞 CLEAN，由 Owner 决定是否补测。

备注（非独立 Finding）：AC-016 的「超时取消释放券」复用与用户取消完全相同的 `cancelInTx → ReleaseInTx` 路径，机制已被 `TestCouponCancelRelease` 覆盖，仅缺「超时触发」入口的直接用例，同样由 Owner 决定是否补充。
