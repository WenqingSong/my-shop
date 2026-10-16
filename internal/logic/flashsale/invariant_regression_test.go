package flashsale

// Kafka 改造前秒杀正确性/一致性不变量回归测试（本任务新增）。
// 覆盖既有测试的并发验证缺口：并发一人一单（INV-003）、并发幂等（INV-005 订单级）、
// 消费器并发领取唯一性（INV-012/015）、人工修复不破坏既有成功订单（INV-017）。
// 全部测试只验证最终业务状态（精确终值断言），复用 consume_test.go / recovery_test.go 的白盒环境假设
// （setupConsumeTest、consumeTestSku、consumeTestActivity、consumeBindingStock、consumeSuperAdmin 等），
// 需真实 MySQL + Redis 就绪；并发测试需 go test -race。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// requestStatusCount 统计指定活动下处于给定状态的异步请求数。
func requestStatusCount(t *testing.T, activityID int64, status int) int {
	t.Helper()
	n, err := g.DB().Model("flash_sale_order_requests").Ctx(context.Background()).
		Where("activity_id", activityID).Where("status", status).Count()
	if err != nil {
		t.Fatalf("count requests status=%d: %v", status, err)
	}
	return n
}

// isIdempotencyDup 判断错误是否为命中订单级幂等唯一约束 uk_flash_idempotency 的重复键冲突。
func isIdempotencyDup(err error) bool {
	var dk *duplicateKeyErr
	return errors.As(err, &dk) && strings.Contains(dk.key, "uk_flash_idempotency")
}

// TestConsumeConcurrentOnePerUser 覆盖 INV-003/AC-001 的并发一人一单：
// 绕过 Redis 闸门直接写 N 条 (activity_id, sku_id, user_id) 相同、idempotency_key 不同的 queued 请求，
// N 个 goroutine 并发消费，断言恰好 1 条 success、N-1 条 failed、订单数=1、sold=1、无请求滞留在 queued。
// 依赖 uk_flash_one_per_user 唯一约束兜底（并发下 loadBindingForUpdate 行锁串行 + 唯一约束拒绝重复）。
func TestConsumeConcurrentOnePerUser(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()
	skuID := consumeTestSku(t, "one-per-user-concurrent-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10

	const (
		userID = int64(901000)
		n      = 20
	)

	// 直接写入 N 条同用户、不同幂等键的 queued 请求（测试消费者，不经过闸门）。
	for i := 0; i < n; i++ {
		if _, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Data(g.Map{
			"user_id":         userID,
			"activity_id":     activityID,
			"sku_id":          skuID,
			"idempotency_key": fmt.Sprintf("one-per-user-%d", i),
			"request_hash":    requestHash(activityID, skuID),
			"status":          requestStatusQueued,
		}).InsertAndGetId(); err != nil {
			t.Fatalf("insert request %d: %v", i, err)
		}
	}

	// N 个 goroutine 各消费一条（FOR UPDATE SKIP LOCKED 互斥领取，每个请求恰好被处理一次）。
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ConsumeQueued(ctx, 1); err != nil {
				t.Errorf("concurrent consume: %v", err)
			}
		}()
	}
	wg.Wait()

	// 恰好 1 成功、N-1 失败（一人一单拒绝重复），无请求滞留在 queued。
	if got := requestStatusCount(t, activityID, requestStatusSuccess); got != 1 {
		t.Fatalf("success=%d want 1（并发一人一单未拦截重复下单）", got)
	}
	if got := requestStatusCount(t, activityID, requestStatusFailed); got != n-1 {
		t.Fatalf("failed=%d want %d", got, n-1)
	}
	if got := requestStatusCount(t, activityID, requestStatusQueued); got != 0 {
		t.Fatalf("queued=%d want 0（存在未处理请求）", got)
	}
	// 订单数 = 1、sold = 1，无重复建单/重复扣减。
	if n, err := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("activity_id", activityID).Count(); err != nil || n != 1 {
		t.Fatalf("order count=%d err=%v want 1", n, err)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("sold=%d want 1", sold)
	}
}

// TestInsertOrderConcurrentIdempotency 覆盖 INV-005/AC-001 的并发幂等（订单级 uk_flash_idempotency）：
// 同一 (user_id, idempotency_key)、不同 SKU 的 N 个并发订单写入，断言恰好 1 个成功、N-1 个命中
// uk_flash_idempotency 唯一约束被拒绝、订单数=1、总 sold=1。
// 说明：订单级幂等键（user_id, idempotency_key）全局唯一（跨活动/SKU），故用「同用户同幂等键、不同 SKU」
// 隔离一人一单约束 uk_flash_one_per_user，使 uk_flash_idempotency 成为唯一兜底——删除该约束（mutation M-3）
// 时本测试必然失败（N 个订单全部落库）。因 uk_request_idempotency 阻止在请求表写入 N 条同键请求，
// 此处直接驱动 insertOrderInTx（与消费者同一事务内落单路径），从订单层验证幂等约束的并发判别力。
func TestInsertOrderConcurrentIdempotency(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	const (
		userID = int64(902000)
		key    = "k-concurrent-idem"
		n      = 10
	)

	// 建立 N 个不同 SKU 并绑定同一活动，逐个解析下单快照。
	skuIDs := make([]int64, n)
	snaps := make([]*skuSnapshot, n)
	for i := 0; i < n; i++ {
		skuIDs[i] = consumeTestSku(t, fmt.Sprintf("idem-concurrent-sku-%d", i))
	}
	activityID := consumeTestActivity(t, skuIDs...)
	for i := 0; i < n; i++ {
		snap, err := s.resolveSku(ctx, skuIDs[i])
		if err != nil {
			t.Fatalf("resolve sku %d: %v", i, err)
		}
		snaps[i] = snap
	}

	// N 个 goroutine 并发调用 insertOrderInTx（同用户、同幂等键、不同 SKU），
	// 仅订单级 uk_flash_idempotency 能阻止重复订单。
	var (
		mu         sync.Mutex
		successCnt int
		dupCnt     int
	)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
				_, e := s.insertOrderInTx(ctx, tx, userID, activityID, skuIDs[i], snaps[i], key,
					requestHash(activityID, skuIDs[i]), fmt.Sprintf("FS-IDEM-CONCURRENT-%d", i))
				return e
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successCnt++
			case isIdempotencyDup(err):
				dupCnt++
			default:
				t.Errorf("unexpected insertOrderInTx error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if successCnt != 1 {
		t.Fatalf("success=%d want 1（并发幂等未拦截重复订单）", successCnt)
	}
	if dupCnt != n-1 {
		t.Fatalf("dup=%d want %d（应全部命中 uk_flash_idempotency）", dupCnt, n-1)
	}
	// 订单数=1，且只有一个 SKU 被扣 1 件，总 sold=1。
	if n, err := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("activity_id", activityID).Count(); err != nil || n != 1 {
		t.Fatalf("order count=%d err=%v want 1", n, err)
	}
	var totalSold int64
	for _, sid := range skuIDs {
		_, sold := consumeBindingStock(t, activityID, sid)
		totalSold += sold
	}
	if totalSold != 1 {
		t.Fatalf("total sold=%d want 1（并发幂等重复扣减）", totalSold)
	}
}

// TestConsumeConcurrentClaimUniqueness 覆盖 INV-012/015/AC-001 的消费器并发领取唯一性：
// M 条不同用户的 queued 请求，N 个 goroutine 并发 ConsumeQueued，断言 M 条全部 success、
// 订单数=M、sold=M、无请求停留在 queued/failed/dead 半状态、无重复建单/重复扣减
// （FOR UPDATE SKIP LOCKED 保证每行恰好被一个消费者领取并处理一次）。
func TestConsumeConcurrentClaimUniqueness(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()
	skuID := consumeTestSku(t, "claim-uniqueness-sku")

	const (
		m = 20 // 请求数 = 并发消费者数
	)
	// 建立库存充足的活动（consumeTestActivity 硬编码 total_stock=10，此处需 ≥ m，故内联建活动）。
	now := consumeMySQLNow(t)
	activityID, err := g.DB().Model("flash_sale_activities").Ctx(ctx).Data(g.Map{
		"name":       "claim-uniqueness",
		"status":     statusEnabled,
		"start_time": now.Add(-time.Hour).Format("2006-01-02 15:04:05"),
		"end_time":   now.Add(time.Hour).Format("2006-01-02 15:04:05"),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	if _, err := g.DB().Model("flash_sale_activity_skus").Ctx(ctx).Data(g.Map{
		"activity_id": activityID, "sku_id": skuID, "flash_price": 1000, "total_stock": m, "sold": 0,
	}).Insert(); err != nil {
		t.Fatalf("insert binding: %v", err)
	}

	// 写入 M 条不同用户（避免一人一单约束干扰）、不同幂等键的 queued 请求。
	for i := 0; i < m; i++ {
		if _, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Data(g.Map{
			"user_id":         int64(903000 + i),
			"activity_id":     activityID,
			"sku_id":          skuID,
			"idempotency_key": fmt.Sprintf("claim-%d", i),
			"request_hash":    requestHash(activityID, skuID),
			"status":          requestStatusQueued,
		}).InsertAndGetId(); err != nil {
			t.Fatalf("insert request %d: %v", i, err)
		}
	}

	// N 个 goroutine 各消费一条。
	var wg sync.WaitGroup
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ConsumeQueued(ctx, 1); err != nil {
				t.Errorf("concurrent consume: %v", err)
			}
		}()
	}
	wg.Wait()

	// M 条全部 success，无请求停留在 queued/failed/dead 半状态。
	if got := requestStatusCount(t, activityID, requestStatusSuccess); got != m {
		t.Fatalf("success=%d want %d（存在领取遗漏或重复）", got, m)
	}
	if got := requestStatusCount(t, activityID, requestStatusQueued); got != 0 {
		t.Fatalf("queued=%d want 0", got)
	}
	if got := requestStatusCount(t, activityID, requestStatusFailed); got != 0 {
		t.Fatalf("failed=%d want 0", got)
	}
	if got := requestStatusCount(t, activityID, requestStatusDead); got != 0 {
		t.Fatalf("dead=%d want 0", got)
	}
	// 订单数 = sold = M，无重复建单/重复扣减。
	if n, err := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("activity_id", activityID).Count(); err != nil || n != m {
		t.Fatalf("order count=%d err=%v want %d", n, err, m)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != m {
		t.Fatalf("sold=%d want %d", sold, m)
	}
}

// TestRepairDoesNotBreakExistingOrders 覆盖 INV-017/AC-005：
// 活动已有成功订单（sold>0）时，对一条 dead 请求执行 dead→queued 修复并重消费，
// 断言既有成功订单不被重放/破坏（订单数 = 原有 + 1、sold 相应 +1、无重复建单/重复扣减），
// 修复的用户恰好新增 1 单，审计 append-only 且仅新增一条记录。
func TestRepairDoesNotBreakExistingOrders(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()
	skuID := consumeTestSku(t, "repair-keeps-orders-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10

	// 先成交 2 单（2 个不同用户，各一条 queued 请求 → 消费 → 成功）。
	const existingOrders = 2
	for i := 0; i < existingOrders; i++ {
		if _, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Data(g.Map{
			"user_id":         int64(904000 + i),
			"activity_id":     activityID,
			"sku_id":          skuID,
			"idempotency_key": fmt.Sprintf("existing-%d", i),
			"request_hash":    requestHash(activityID, skuID),
			"status":          requestStatusQueued,
		}).InsertAndGetId(); err != nil {
			t.Fatalf("insert existing request %d: %v", i, err)
		}
	}
	if _, err := s.ConsumeQueued(ctx, existingOrders); err != nil {
		t.Fatalf("consume existing: %v", err)
	}
	if n, err := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("activity_id", activityID).Count(); err != nil || n != existingOrders {
		t.Fatalf("existing order count=%d err=%v want %d", n, err, existingOrders)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != existingOrders {
		t.Fatalf("existing sold=%d want %d", sold, existingOrders)
	}

	// 预置一条 dead 请求（新用户，无既有订单），修复 dead→queued 后重消费应新增一单。
	deadUserID := int64(904000 + existingOrders)
	deadReqID, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Data(g.Map{
		"user_id":         deadUserID,
		"activity_id":     activityID,
		"sku_id":          skuID,
		"idempotency_key": "repair-keeps-orders-dead",
		"request_hash":    requestHash(activityID, skuID),
		"status":          requestStatusDead,
		"retry_count":     4,
		"last_error_code": codes.CodeInternalError,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert dead request: %v", err)
	}

	adminID, _ := consumeSuperAdmin(t)
	if _, err := s.RepairRequest(ctx, adminID, &v1.RepairRequestReq{
		Id: deadReqID, TargetStatus: v1.RequestStatusQueued, Reason: "故障排除后重投",
	}); err != nil {
		t.Fatalf("repair: %v", err)
	}

	// 重消费：修复后的 dead 请求成功落单。
	if _, err := s.ConsumeQueued(ctx, 1); err != nil {
		t.Fatalf("reconsume: %v", err)
	}

	// 既有成功订单不重放：订单数 = 原有 + 1、sold = 原有 + 1。
	if n, err := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("activity_id", activityID).Count(); err != nil || n != existingOrders+1 {
		t.Fatalf("order count after repair=%d err=%v want %d（修复重放/破坏既有订单）", n, err, existingOrders+1)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != existingOrders+1 {
		t.Fatalf("sold after repair=%d want %d（修复重复扣减）", sold, existingOrders+1)
	}
	// 修复的用户恰好 1 单，未重复建单。
	if n, err := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("activity_id", activityID).Where("user_id", deadUserID).Count(); err != nil || n != 1 {
		t.Fatalf("repaired user order count=%d err=%v want 1", n, err)
	}

	// 审计 append-only：仅 1 条，字段正确（dead→queued）。
	audits, err := s.ListRequestAudits(ctx, deadReqID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(audits.Items) != 1 {
		t.Fatalf("audit count=%d want 1", len(audits.Items))
	}
	if a := audits.Items[0]; a.Action != repairActionDeadToQueued || a.BeforeStatus != requestStatusDead || a.AfterStatus != requestStatusQueued {
		t.Fatalf("unexpected audit: %+v", a)
	}
}
