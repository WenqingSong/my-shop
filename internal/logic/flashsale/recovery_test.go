package flashsale

// 秒杀 V4 故障恢复白盒测试：活动结束终态收敛（AC-005/INV-016）与人工修复/审计（AC-007/INV-017）。
// 需 MySQL + Redis 就绪（复用 consume_test.go 的 setupConsumeTest 环境假设）。

import (
	"context"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// TestConvergeEndedActivity 覆盖 AC-005/INV-016：
// 活动结束且在途请求归零后，convergeEndedActivity 将 remaining 刷成 total_stock - sold、
// 失效活动元数据（DEL activity hash + 置空值标记），无残留脏数据，且结果可观测。
func TestConvergeEndedActivity(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "ended-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10, sold=0
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}

	// 模拟已售出 3 件（直接写 MySQL 权威事实）。
	if _, err := g.DB().Exec(ctx,
		"UPDATE flash_sale_activity_skus SET sold=3 WHERE activity_id=? AND sku_id=?", activityID, skuID); err != nil {
		t.Fatalf("set sold: %v", err)
	}

	if err := s.convergeEndedActivity(ctx, activityID); err != nil {
		t.Fatalf("converge ended: %v", err)
	}

	// remaining = total_stock - sold = 7（无在途，可观测）。
	if got, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID)); err != nil || got.String() != "7" {
		t.Fatalf("ended remaining=%q err=%v want 7", got.String(), err)
	}
	// 活动元数据已失效，后续抢购快速失败。
	if n, err := g.Redis().Exists(ctx, flashSaleActivityKey(activityID)); err != nil || n != 0 {
		t.Fatalf("ended activity hash should be cleared, exists=%d err=%v", n, err)
	}
	// 空值标记已置位。
	if n, err := g.Redis().Exists(ctx, flashSaleNullKey(activityID)); err != nil || n != 1 {
		t.Fatalf("ended null marker should be set, exists=%d err=%v", n, err)
	}
}

// TestConvergeEndedActivityKeepsInflight 覆盖 AC-005 在途口径：
// 活动结束但仍有 queued 请求时，convergeEndedActivity 只收敛 remaining（计入在途），不失效缓存。
func TestConvergeEndedActivityKeepsInflight(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "ended-inflight-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10, sold=0
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}

	// 预置一个 queued 在途请求。
	if _, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Data(g.Map{
		"user_id":         900002,
		"activity_id":     activityID,
		"sku_id":          skuID,
		"idempotency_key": "ended-inflight",
		"request_hash":    requestHash(activityID, skuID),
		"status":          requestStatusQueued,
	}).InsertAndGetId(); err != nil {
		t.Fatalf("insert queued request: %v", err)
	}

	if err := s.convergeEndedActivity(ctx, activityID); err != nil {
		t.Fatalf("converge ended: %v", err)
	}

	// remaining = 10 - 0 - 1 = 9（在途计入）。
	if got, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID)); err != nil || got.String() != "9" {
		t.Fatalf("ended remaining=%q err=%v want 9", got.String(), err)
	}
	// 在途未归零，活动缓存不失效（保留等待消费完成）。
	if n, err := g.Redis().Exists(ctx, flashSaleActivityKey(activityID)); err != nil || n != 1 {
		t.Fatalf("activity hash should remain while inflight, exists=%d err=%v", n, err)
	}
}

// TestRepairRequestDeadToQueuedAndAudit 覆盖 AC-007/INV-017 核心逻辑：
// dead→queued 修复成功、retry_count 清零、审计同事务落库且字段正确；非 dead 拒绝（12008）。
func TestRepairRequestDeadToQueuedAndAudit(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "repair-sku")
	activityID := consumeTestActivity(t, skuID)

	// 预置一个 dead 请求。
	reqID, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Data(g.Map{
		"user_id":         900003,
		"activity_id":     activityID,
		"sku_id":          skuID,
		"idempotency_key": "repair-key",
		"request_hash":    requestHash(activityID, skuID),
		"status":          requestStatusDead,
		"retry_count":     4,
		"last_error_code": codes.CodeInternalError,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert dead request: %v", err)
	}

	// 预置一个操作者管理员。
	adminID, err := g.DB().Model("admins").Ctx(ctx).Data(g.Map{
		"username": "repairer", "password_hash": "x", "status": 1, "is_super": 0,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert admin: %v", err)
	}

	res, err := s.RepairRequest(ctx, adminID, &v1.RepairRequestReq{
		Id: reqID, TargetStatus: v1.RequestStatusQueued, Reason: "故障排除后重投",
	})
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if res.Id != reqID || res.Status != v1.RequestStatusQueued {
		t.Fatalf("unexpected repair res: %+v", res)
	}

	// 状态迁移正确：dead→queued，retry_count 清零、next_attempt_at 清空。
	req := consumeRequestRowByID(t, reqID)
	if req.Status != requestStatusQueued {
		t.Fatalf("after repair status=%d want queued", req.Status)
	}
	if req.RetryCount != 0 {
		t.Fatalf("after repair retry_count=%d want 0", req.RetryCount)
	}
	if req.NextAttemptAt != nil {
		t.Fatalf("after repair next_attempt_at should be NULL")
	}

	// 审计记录同事务落库且字段正确。
	audits, err := s.ListRequestAudits(ctx, reqID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(audits.Items) != 1 {
		t.Fatalf("audit count=%d want 1", len(audits.Items))
	}
	a := audits.Items[0]
	if a.Action != repairActionDeadToQueued || a.BeforeStatus != requestStatusDead ||
		a.AfterStatus != requestStatusQueued || a.OperatorAdminId != adminID ||
		a.OperatorUsername != "repairer" || a.Reason != "故障排除后重投" {
		t.Fatalf("unexpected audit: %+v", a)
	}

	// 已修复为 queued，再次修复 → 12008（仅 dead 可修复）。
	_, err = s.RepairRequest(ctx, adminID, &v1.RepairRequestReq{
		Id: reqID, TargetStatus: v1.RequestStatusQueued, Reason: "again",
	})
	if code := codes.FromError(err); code != codes.CodeFlashSaleRequestNotRepairable {
		t.Fatalf("repair non-dead: code=%d want 12008, err=%v", code, err)
	}
	// 未新增审计（修复被拒绝，无副作用）。
	audits, _ = s.ListRequestAudits(ctx, reqID)
	if len(audits.Items) != 1 {
		t.Fatalf("rejected repair must not append audit, count=%d want 1", len(audits.Items))
	}
}

// TestIdempotentOrphanMarkerSelfHeal 覆盖 AC-001 的孤儿幂等标记自愈：
// 崩溃窗口「闸门已预扣/写 idem 标记、request 落库失败」残留孤儿标记，同键重试不再 503，
// 而是经 MySQL 校验后清除标记并继续入队（不重复预扣），request 成功落库。
func TestIdempotentOrphanMarkerSelfHeal(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "orphan-idem-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}

	const (
		userID = int64(900010)
		key    = "k-orphan-idem"
	)
	hash := requestHash(activityID, skuID)

	// 模拟崩溃窗口孤儿：GATE_PASSED 已预扣 remaining(10→9) + 写 idem/bought 标记，request 落库失败。
	if _, err := g.Redis().Set(ctx, flashSaleStockKey(activityID, skuID), "9"); err != nil {
		t.Fatalf("set stock: %v", err)
	}
	if _, err := g.Redis().Set(ctx, flashSaleIdemKey(userID, key), hash); err != nil {
		t.Fatalf("set idem: %v", err)
	}
	if _, err := g.Redis().Set(ctx, flashSaleBoughtKey(activityID, skuID, userID), "1"); err != nil {
		t.Fatalf("set bought: %v", err)
	}

	// 同键重试：闸门 IDEMPOTENT_HIT → 孤儿自愈 → 继续入队成功（非 503）。
	res, err := s.CreateOrder(ctx, userID, activityID, &v1.CreateOrderReq{SkuId: skuID, IdempotencyKey: key})
	if err != nil {
		t.Fatalf("create order should self-heal, err=%v", err)
	}
	if res.Status != v1.RequestStatusQueued {
		t.Fatalf("orphan idem self-heal status=%q want queued", res.Status)
	}
	// request 已落库，无「已预扣但无 request」残留。
	if r, err := s.findRequestByIdempotencyKey(ctx, userID, key); err != nil || r == nil {
		t.Fatalf("orphan idem should enqueue request, r=%+v err=%v", r, err)
	}
	// 未重复预扣：remaining 保持 9（孤儿预扣被本次入队复用）。
	if got, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID)); err != nil || got.String() != "9" {
		t.Fatalf("remaining=%q err=%v want 9（重复预扣）", got.String(), err)
	}
}

// TestAlreadyPurchasedOrphanMarkerSelfHeal 覆盖 AC-001 的孤儿已购标记自愈：
// 崩溃窗口残留孤儿 bought 标记，用户换新幂等键重试不再 12004，而是经 MySQL 校验后
// 清除孤儿标记并继续入队，request 成功落库。
func TestAlreadyPurchasedOrphanMarkerSelfHeal(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "orphan-bought-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}

	const userID = int64(900011)

	// 模拟崩溃窗口孤儿：GATE_PASSED 已预扣 remaining(10→9) + 写 bought 标记，request 落库失败（无 idem 残留）。
	if _, err := g.Redis().Set(ctx, flashSaleStockKey(activityID, skuID), "9"); err != nil {
		t.Fatalf("set stock: %v", err)
	}
	if _, err := g.Redis().Set(ctx, flashSaleBoughtKey(activityID, skuID, userID), "1"); err != nil {
		t.Fatalf("set bought: %v", err)
	}

	// 换新幂等键重试：闸门 ALREADY_PURCHASED → MySQL 校验无证据 → 孤儿自愈 → 继续入队成功（非 12004）。
	res, err := s.CreateOrder(ctx, userID, activityID, &v1.CreateOrderReq{SkuId: skuID, IdempotencyKey: "k-orphan-bought-new"})
	if err != nil {
		t.Fatalf("create order should self-heal, err=%v", err)
	}
	if res.Status != v1.RequestStatusQueued {
		t.Fatalf("orphan bought self-heal status=%q want queued", res.Status)
	}
	if r, err := s.findRequestByIdempotencyKey(ctx, userID, "k-orphan-bought-new"); err != nil || r == nil {
		t.Fatalf("orphan bought should enqueue request, r=%+v err=%v", r, err)
	}
}

// TestReconcileCacheConvergesEndedActivity 覆盖 AC-005 的对账扫描入口：
// ReconcileCache 覆盖「已结束、尚在 grace 窗口」的活动，触发终态收敛并失效缓存。
func TestReconcileCacheConvergesEndedActivity(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "reconcile-ended-sku")
	now := consumeMySQLNow(t)
	// 活动刚结束（end_time 在 grace 窗口内），status=enabled。
	activityID, err := g.DB().Model("flash_sale_activities").Ctx(ctx).Data(g.Map{
		"name":       "ended",
		"status":     statusEnabled,
		"start_time": now.Add(-time.Hour).Format("2006-01-02 15:04:05"),
		"end_time":   now.Add(-5 * time.Second).Format("2006-01-02 15:04:05"),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert ended activity: %v", err)
	}
	if _, err := g.DB().Model("flash_sale_activity_skus").Ctx(ctx).Data(g.Map{
		"activity_id": activityID, "sku_id": skuID, "flash_price": 1000, "total_stock": 10, "sold": 3,
	}).Insert(); err != nil {
		t.Fatalf("insert binding: %v", err)
	}
	// 预热（写入活动缓存），再让 ReconcileCache 收敛。
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}

	if _, err := s.ReconcileCache(ctx, 100); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// remaining = total_stock - sold = 7。
	if got, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID)); err != nil || got.String() != "7" {
		t.Fatalf("reconciled remaining=%q err=%v want 7", got.String(), err)
	}
	// 活动元数据失效、空值标记置位。
	if n, err := g.Redis().Exists(ctx, flashSaleActivityKey(activityID)); err != nil || n != 0 {
		t.Fatalf("ended activity hash should be cleared after reconcile, exists=%d err=%v", n, err)
	}
	if n, err := g.Redis().Exists(ctx, flashSaleNullKey(activityID)); err != nil || n != 1 {
		t.Fatalf("ended null marker should be set after reconcile, exists=%d err=%v", n, err)
	}
}

// consumeRequestRowByID 按 id 查询异步请求行。
func consumeRequestRowByID(t *testing.T, id int64) *orderRequestRow {
	t.Helper()
	var rows []*orderRequestRow
	if err := g.DB().Model("flash_sale_order_requests").Ctx(context.Background()).
		Where("id", id).Scan(&rows); err != nil {
		t.Fatalf("query request: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("request %d not found", id)
	}
	return rows[0]
}
