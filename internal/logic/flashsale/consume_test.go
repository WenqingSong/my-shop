package flashsale

// 消费器白盒测试：直接驱动 ConsumeQueued，覆盖技术失败重试、超限死信与 dead→queued 重处理（AC-004）。
// 通过覆盖 generateOrderNo 固定订单号，确定性触发 uk_flash_order_no 撞号（技术失败），
// 验证「按策略重试（retry_count+1 + 退避）→ 超上限落 dead → 不再无限重试 → 可重处理成功」。
// 需 MySQL + Redis 就绪（与 cmd 集成测试同环境假设）。

import (
	"context"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
	// 触发 sku/product logic 的 init，注册 service.Sku / service.Product（resolveSku 依赖）。
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/product"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/sku"
)

// setupConsumeTest 建立隔离的消费测试环境：迁移建表 + Bootstrap（DB/Redis 配置）+ 清空秒杀/商品域相关表。
func setupConsumeTest(t *testing.T) {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", "test-secret-0123456789-0123456789-0123456789")
	t.Setenv("ADMIN_SUPER_PASSWORD", "test-admin-password-123")

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for _, table := range []string{
		"flash_sale_request_audits", "flash_sale_order_requests", "flash_sale_orders", "flash_sale_activity_skus", "flash_sale_activities",
		"skus", "product_images", "products", "categories",
	} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	if err := g.Redis().FlushDB(ctx); err != nil {
		t.Fatalf("flush redis: %v", err)
	}
}

// consumeMySQLNow 读取 MySQL 的当前墙钟时间（DATETIME 语义，无时区），用于构造相对 NOW() 的时间窗。
func consumeMySQLNow(t *testing.T) time.Time {
	t.Helper()
	v, err := g.DB().GetValue(context.Background(), "SELECT NOW()")
	if err != nil {
		t.Fatalf("select NOW(): %v", err)
	}
	now, err := time.Parse("2006-01-02 15:04:05", v.String())
	if err != nil {
		t.Fatalf("parse NOW() %q: %v", v.String(), err)
	}
	return now
}

// consumeTestSku 建立一个可售 SKU（商品 on_shelf、SKU enabled），返回 skuID。
// name 用于派生分类/商品/SKU 名，保证同一测试内多次建 SKU 不撞唯一约束。
func consumeTestSku(t *testing.T, name string) int64 {
	t.Helper()
	ctx := context.Background()
	categoryID, err := g.DB().Model("categories").Ctx(ctx).Data(g.Map{
		"parent_id": 0, "name": "cat-" + name, "sort": 0, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert category: %v", err)
	}
	productID, err := g.DB().Model("products").Ctx(ctx).Data(g.Map{
		"name": "prod-" + name, "brand": "", "category_id": categoryID,
		"price": 10000, "main_image": "http://img.example/x.png", "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	skuID, err := g.DB().Model("skus").Ctx(ctx).Data(g.Map{
		"product_id": productID, "name": name, "price": 10000, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert sku: %v", err)
	}
	return skuID
}

// consumeTestActivity 建立启用且时间窗内的活动并绑定给定 SKU（各 total_stock=10），返回 activityID。
func consumeTestActivity(t *testing.T, skuIDs ...int64) int64 {
	t.Helper()
	ctx := context.Background()
	now := consumeMySQLNow(t)
	activityID, err := g.DB().Model("flash_sale_activities").Ctx(ctx).Data(g.Map{
		"name":       "consume",
		"status":     1,
		"start_time": now.Add(-time.Hour).Format("2006-01-02 15:04:05"),
		"end_time":   now.Add(time.Hour).Format("2006-01-02 15:04:05"),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	for _, skuID := range skuIDs {
		if _, err := g.DB().Model("flash_sale_activity_skus").Ctx(ctx).Data(g.Map{
			"activity_id": activityID, "sku_id": skuID, "flash_price": 1000, "total_stock": 10, "sold": 0,
		}).Insert(); err != nil {
			t.Fatalf("insert binding: %v", err)
		}
	}
	return activityID
}

// consumeRequestRow 查询指定 (user_id, idempotency_key) 的异步请求行。
func consumeRequestRow(t *testing.T, userID int64, key string) *orderRequestRow {
	t.Helper()
	var rows []*orderRequestRow
	if err := g.DB().Model("flash_sale_order_requests").Ctx(context.Background()).
		Where("user_id", userID).Where("idempotency_key", key).Scan(&rows); err != nil {
		t.Fatalf("query request: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("request (%d,%q) not found", userID, key)
	}
	return rows[0]
}

// consumeBindingStock 查询绑定的 (total_stock, sold)。
func consumeBindingStock(t *testing.T, activityID, skuID int64) (int64, int64) {
	t.Helper()
	rec, err := g.DB().Model("flash_sale_activity_skus").Ctx(context.Background()).
		Fields("total_stock", "sold").
		Where("activity_id", activityID).Where("sku_id", skuID).One()
	if err != nil {
		t.Fatalf("query binding: %v", err)
	}
	if rec == nil || rec.IsEmpty() {
		t.Fatalf("binding (%d,%d) not found", activityID, skuID)
	}
	return rec["total_stock"].Int64(), rec["sold"].Int64()
}

// TestConsumeRetryThenDeadLetter 覆盖 AC-004：
// 订单号撞号（技术失败）→ 退避重试（retry_count+1、保持 queued、next_attempt_at 未来）；
// 超过上限 → dead；dead 不再被消费；dead→queued 可重新处理成功。
func TestConsumeRetryThenDeadLetter(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()
	skuID := consumeTestSku(t, "consume-sku")
	activityID := consumeTestActivity(t, skuID)

	const userID = int64(881000)
	const key = "k-retry"

	// 预置占用订单号 "FS-FIXED" 的订单（bogus 活动，不影响本活动订单计数），使消费时订单号撞号。
	if _, err := g.DB().Model("flash_sale_orders").Ctx(ctx).Data(g.Map{
		"order_no": "FS-FIXED", "user_id": 1, "activity_id": 999999, "sku_id": 999999,
		"product_id": 1, "sku_name": "x", "product_name": "x", "product_main_image": "",
		"flash_price": 1, "quantity": 1, "idempotency_key": "occupied", "request_hash": "occupied",
	}).Insert(); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	// 直接写入 queued 请求（测试消费者，不经过闸门）。
	if _, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Data(g.Map{
		"user_id":         userID,
		"activity_id":     activityID,
		"sku_id":          skuID,
		"idempotency_key": key,
		"request_hash":    requestHash(activityID, skuID),
		"status":          requestStatusQueued,
	}).InsertAndGetId(); err != nil {
		t.Fatalf("insert request: %v", err)
	}

	// 强制订单号固定，触发 uk_flash_order_no 撞号（技术失败）。
	orig := generateOrderNo
	generateOrderNo = func() string { return "FS-FIXED" }
	defer func() { generateOrderNo = orig }()

	// 首次消费：撞号 → 技术失败 → 重试（retry_count=1、保持 queued、退避 next_attempt_at 未来）。
	if n, err := s.ConsumeQueued(ctx, 1); err != nil || n != 1 {
		t.Fatalf("first consume: n=%d err=%v", n, err)
	}
	req := consumeRequestRow(t, userID, key)
	if req.Status != requestStatusQueued {
		t.Fatalf("after first consume status=%d want queued", req.Status)
	}
	if req.RetryCount != 1 {
		t.Fatalf("retry_count=%d want 1", req.RetryCount)
	}
	if req.NextAttemptAt == nil {
		t.Fatalf("next_attempt_at should be set after retry")
	}

	// 推到重试上限并让退避到期 → 再次消费 → 超上限 → dead。
	if _, err := g.DB().Exec(ctx,
		"UPDATE flash_sale_order_requests SET retry_count=3, next_attempt_at=NULL WHERE user_id=? AND idempotency_key=?", userID, key); err != nil {
		t.Fatalf("force retry due: %v", err)
	}
	if _, err := s.ConsumeQueued(ctx, 1); err != nil {
		t.Fatalf("second consume: %v", err)
	}
	req = consumeRequestRow(t, userID, key)
	if req.Status != requestStatusDead {
		t.Fatalf("after exhausted status=%d want dead", req.Status)
	}
	// dead 不建单、不扣库存。
	if n, _ := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("activity_id", activityID).Count(); n != 0 {
		t.Fatalf("dead must not create order, count=%d want 0", n)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 0 {
		t.Fatalf("dead must not deduct stock, sold=%d want 0", sold)
	}

	// dead→queued 重处理：恢复随机订单号后重新处理 → 成功。
	generateOrderNo = orig
	if _, err := g.DB().Exec(ctx,
		"UPDATE flash_sale_order_requests SET status=0, retry_count=0, next_attempt_at=NULL WHERE user_id=? AND idempotency_key=?", userID, key); err != nil {
		t.Fatalf("reprocess: %v", err)
	}
	if _, err := s.ConsumeQueued(ctx, 1); err != nil {
		t.Fatalf("reprocess consume: %v", err)
	}
	req = consumeRequestRow(t, userID, key)
	if req.Status != requestStatusSuccess {
		t.Fatalf("after reprocess status=%d want success", req.Status)
	}
	if n, _ := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("activity_id", activityID).Count(); n != 1 {
		t.Fatalf("reprocessed order count=%d want 1", n)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("reprocessed sold=%d want 1", sold)
	}
}

// TestCreateOrderIdempotencyConflictCompensatesOnce 回归「CreateOrder/enqueue 重复补偿」：
// 在「幂等键已存在 + 内容冲突 + 经入队重复键」窄路径下（idem 标记 TTL 过期但请求仍落库，
// 第二次请求内容不同），闸门会再次预扣并写标记，enqueue 撞 uk_request_idempotency 后须补偿
// 恰好一次（INCR remaining + 清标记）；若 CreateOrder 对 enqueue 返回的 12005 再补偿一次，
// 则 remaining 多回补（超预扣）。断言第二次请求返回 12005 且 sku2 remaining 回补到预热值。
func TestCreateOrderIdempotencyConflictCompensatesOnce(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	sku1 := consumeTestSku(t, "sku-dup-a")
	sku2 := consumeTestSku(t, "sku-dup-b")
	activityID := consumeTestActivity(t, sku1, sku2)
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}

	const (
		userID = int64(882000)
		key    = "k-dup"
	)

	// 第一次入队（sku1）成功：落库 request_hash=H1，sku1 预扣。
	if res, err := s.CreateOrder(ctx, userID, activityID, &v1.CreateOrderReq{SkuId: sku1, IdempotencyKey: key}); err != nil || res.Status != v1.RequestStatusQueued {
		t.Fatalf("first create order: res=%+v err=%v", res, err)
	}

	// 删除 idem 标记，模拟其 TTL 过期，使第二次请求走闸门预扣（而非幂等快速命中）。
	if _, err := g.Redis().Del(ctx, flashSaleIdemKey(userID, key)); err != nil {
		t.Fatalf("del idem marker: %v", err)
	}

	// 第二次入队（sku2，同 key，内容冲突）：闸门预扣 sku2（10→9）→ enqueue 撞 uk_request_idempotency → 补偿 → 12005。
	_, err := s.CreateOrder(ctx, userID, activityID, &v1.CreateOrderReq{SkuId: sku2, IdempotencyKey: key})
	if code := codes.FromError(err); code != codes.CodeFlashSaleIdempotencyConflict {
		t.Fatalf("expected 12005 idempotency conflict, got code=%d err=%v", code, err)
	}

	// 关键断言：sku2 remaining 应回补到预热值 10（仅补偿一次），若为 11 说明被重复补偿（超预扣）。
	v, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, sku2))
	if err != nil {
		t.Fatalf("get sku2 remaining: %v", err)
	}
	if got := v.String(); got != "10" {
		t.Fatalf("sku2 remaining=%q want 10（重复补偿导致超预扣）", got)
	}
}
