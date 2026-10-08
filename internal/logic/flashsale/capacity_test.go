package flashsale

// 秒杀 V5 容量保护与可观测性白盒测试：限流（用户级/活动级）、排队软上限、熔断降级、指标可对账。
// 复用 consume_test.go 的 setupConsumeTest / consumeTestSku / consumeTestActivity 环境假设（MySQL + Redis 就绪），
// 通过环境变量（FLASH_SALE_*）开启对应能力，直接驱动 CreateOrder / ConsumeQueued 验证可观察业务结果。

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/prometheus/client_golang/prometheus"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// TestRateLimitUserRejects 覆盖 AC-001/INV-018（用户级限流）：
// 同一用户在窗口内请求速率超阈值后，后续请求快速拒绝 12009 且无副作用（不预扣、不写 request）；
// 首个请求正常排队。
func TestRateLimitUserRejects(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	t.Setenv("FLASH_SALE_RATE_LIMIT_ENABLED", "true")
	t.Setenv("FLASH_SALE_RATE_LIMIT_USER_MAX_REQUESTS", "1")
	t.Setenv("FLASH_SALE_RATE_LIMIT_USER_WINDOW_SECONDS", "60")
	// 活动级限流关闭（仅测用户级）。
	t.Setenv("FLASH_SALE_RATE_LIMIT_ACTIVITY_MAX_REQUESTS", "0")

	skuID := consumeTestSku(t, "rl-user-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}

	const userID = int64(901000)

	// 第 1 次：正常排队（预扣 remaining 10→9）。
	res, err := s.CreateOrder(ctx, userID, activityID, &v1.CreateOrderReq{SkuId: skuID, IdempotencyKey: "rl-user-1"})
	if err != nil || res.Status != v1.RequestStatusQueued {
		t.Fatalf("first order: res=%+v err=%v", res, err)
	}

	// 第 2 次（换幂等键）：用户级限流拒绝 12009（先于闸门/一人一单，无预扣、无 request）。
	_, err = s.CreateOrder(ctx, userID, activityID, &v1.CreateOrderReq{SkuId: skuID, IdempotencyKey: "rl-user-2"})
	if code := codes.FromError(err); code != codes.CodeFlashSaleRateLimited {
		t.Fatalf("second order: code=%d want 12009, err=%v", code, err)
	}

	// 无副作用：remaining 仍为 9（第 2 次未预扣）；request 仅 1 条。
	if got, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID)); err != nil || got.String() != "9" {
		t.Fatalf("remaining=%q err=%v want 9（限流不应预扣）", got.String(), err)
	}
	if n, _ := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Count(); n != 1 {
		t.Fatalf("request count=%d want 1（限流不应写 request）", n)
	}
}

// TestRateLimitActivityRejects 覆盖 AC-002/INV-018（活动级限流）：
// 单个活动单位时间总请求超阈值后，超出部分快速拒绝 12009；不同活动互不影响（阈值按活动独立计数）。
func TestRateLimitActivityRejects(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	t.Setenv("FLASH_SALE_RATE_LIMIT_ENABLED", "true")
	t.Setenv("FLASH_SALE_RATE_LIMIT_ACTIVITY_MAX_REQUESTS", "2")
	t.Setenv("FLASH_SALE_RATE_LIMIT_ACTIVITY_WINDOW_SECONDS", "60")
	// 用户级限流关闭（仅测活动级）。
	t.Setenv("FLASH_SALE_RATE_LIMIT_USER_MAX_REQUESTS", "0")

	skuID := consumeTestSku(t, "rl-activity-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}

	// 前 2 个不同用户正常排队（活动计数=2）。
	for i := 0; i < 2; i++ {
		res, err := s.CreateOrder(ctx, int64(902000+i), activityID, &v1.CreateOrderReq{
			SkuId: skuID, IdempotencyKey: "rl-activity-" + string(rune('a'+i)),
		})
		if err != nil || res.Status != v1.RequestStatusQueued {
			t.Fatalf("order %d: res=%+v err=%v", i, res, err)
		}
	}

	// 第 3 个用户：活动级限流拒绝 12009（不预扣、不写 request）。
	_, err := s.CreateOrder(ctx, int64(902999), activityID, &v1.CreateOrderReq{SkuId: skuID, IdempotencyKey: "rl-activity-z"})
	if code := codes.FromError(err); code != codes.CodeFlashSaleRateLimited {
		t.Fatalf("third order: code=%d want 12009, err=%v", code, err)
	}

	// remaining 仍为 8（前 2 次各预扣 1，第 3 次未预扣）；request 仅 2 条。
	if got, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID)); err != nil || got.String() != "8" {
		t.Fatalf("remaining=%q err=%v want 8", got.String(), err)
	}
	if n, _ := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Count(); n != 2 {
		t.Fatalf("request count=%d want 2", n)
	}
}

// TestQueueCapacityRejects 覆盖 AC-003/INV-019（排队软上限）：
// 单 activity×SKU 的 queued 计数达上限后，新入队请求快速拒绝 12010（无副作用，不预扣、不写 request）；
// 消费释放容量后后续请求可继续入队。
func TestQueueCapacityRejects(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	t.Setenv("FLASH_SALE_QUEUE_CAPACITY_ENABLED", "true")
	t.Setenv("FLASH_SALE_QUEUE_CAPACITY_MAX_QUEUED_PER_SKU", "2")

	skuID := consumeTestSku(t, "queue-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}

	// 前 2 个不同用户入队成功（排队计数=2）。
	for i := 0; i < 2; i++ {
		res, err := s.CreateOrder(ctx, int64(903000+i), activityID, &v1.CreateOrderReq{
			SkuId: skuID, IdempotencyKey: "queue-" + string(rune('a'+i)),
		})
		if err != nil || res.Status != v1.RequestStatusQueued {
			t.Fatalf("order %d: res=%+v err=%v", i, res, err)
		}
	}

	// 第 3 个用户：排队满拒绝 12010（不预扣、不写 request）。
	_, err := s.CreateOrder(ctx, int64(903999), activityID, &v1.CreateOrderReq{SkuId: skuID, IdempotencyKey: "queue-z"})
	if code := codes.FromError(err); code != codes.CodeFlashSaleQueueFull {
		t.Fatalf("third order: code=%d want 12010, err=%v", code, err)
	}

	// 无副作用：remaining=8（前 2 次各预扣 1）、request=2、queued 计数=2。
	if got, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID)); err != nil || got.String() != "8" {
		t.Fatalf("remaining=%q err=%v want 8", got.String(), err)
	}
	if n, _ := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Where("status", requestStatusQueued).Count(); n != 2 {
		t.Fatalf("queued count=%d want 2", n)
	}

	// 消费释放容量：2 条全部落单成功后，queued 归零，后续请求可继续入队。
	if n, err := s.ConsumeQueued(ctx, 10); err != nil || n != 2 {
		t.Fatalf("consume: n=%d err=%v", n, err)
	}
	// 消费后排队计数释放（DECR 到 0）。
	if got, err := g.Redis().Get(ctx, flashSaleQueuedKey(activityID, skuID)); err != nil || got.String() != "0" {
		t.Fatalf("queued counter=%q err=%v want 0", got.String(), err)
	}
	// 容量释放后新用户可继续入队。
	res, err := s.CreateOrder(ctx, int64(904000), activityID, &v1.CreateOrderReq{SkuId: skuID, IdempotencyKey: "queue-after"})
	if err != nil || res.Status != v1.RequestStatusQueued {
		t.Fatalf("after release order: res=%+v err=%v", res, err)
	}
}

// TestCircuitBreakerOpensAndRecovers 覆盖 AC-004/INV-020（熔断降级）：
// Redis 闸门连续失败达阈值进入 Open（快速失败 503，不预扣、不建单、不排队）；
// Open 期间即使 Redis 恢复仍快速失败；超时后经半开探测自动关闭、恢复正常处理。
func TestCircuitBreakerOpensAndRecovers(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	t.Setenv("FLASH_SALE_CIRCUIT_BREAKER_ENABLED", "true")
	t.Setenv("FLASH_SALE_CIRCUIT_BREAKER_FAILURE_THRESHOLD", "2")
	t.Setenv("FLASH_SALE_CIRCUIT_BREAKER_OPEN_TIMEOUT_SECONDS", "1")
	t.Setenv("FLASH_SALE_CIRCUIT_BREAKER_HALF_OPEN_MAX_PROBES", "1")

	skuID := consumeTestSku(t, "cb-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}

	// 注入 Redis 闸门故障：活动 key 改为 string 类型，使 Lua HGETALL 返回 WRONGTYPE。
	if _, err := g.Redis().Do(ctx, "SET", flashSaleActivityKey(activityID), "not-a-hash"); err != nil {
		t.Fatalf("corrupt activity key: %v", err)
	}

	// 连续 2 次闸门失败 → 熔断 Open（阈值=2）。
	for i := 0; i < 2; i++ {
		_, err := s.CreateOrder(ctx, int64(905000+i), activityID, &v1.CreateOrderReq{
			SkuId: skuID, IdempotencyKey: "cb-" + string(rune('a'+i)),
		})
		if code := codes.FromError(err); code != codes.CodeServiceUnavailable {
			t.Fatalf("gate fail %d: code=%d want 1005, err=%v", i, code, err)
		}
	}

	// 恢复 Redis：先删除被污染的活动 key（string 类型），再重新预热活动缓存。
	if _, err := g.Redis().Del(ctx, flashSaleActivityKey(activityID)); err != nil {
		t.Fatalf("del corrupted activity key: %v", err)
	}
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("restore cache: %v", err)
	}

	// Open 未超时：即使 Redis 已恢复，仍快速失败（熔断短路，不进入闸门、无预扣）。
	_, err := s.CreateOrder(ctx, int64(905999), activityID, &v1.CreateOrderReq{SkuId: skuID, IdempotencyKey: "cb-open"})
	if code := codes.FromError(err); code != codes.CodeServiceUnavailable {
		t.Fatalf("open fast-fail: code=%d want 1005, err=%v", code, err)
	}
	// 无预扣、无 request（熔断期间不产生业务效果）。
	if got, _ := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID)); got.String() != "10" {
		t.Fatalf("remaining=%q want 10（熔断不应预扣）", got.String())
	}
	if n, _ := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Count(); n != 0 {
		t.Fatalf("request count=%d want 0（熔断不应排队）", n)
	}

	// 等待 Open 超时 → 半开探测：真实请求成功 → 熔断自动关闭，恢复正常处理。
	time.Sleep(1100 * time.Millisecond)
	res, err := s.CreateOrder(ctx, int64(906000), activityID, &v1.CreateOrderReq{SkuId: skuID, IdempotencyKey: "cb-recover"})
	if err != nil || res.Status != v1.RequestStatusQueued {
		t.Fatalf("recover order: res=%+v err=%v", res, err)
	}
}

// TestMetricsReconcile 覆盖 AC-005/INV-021（指标可对账）：
// 开启指标后，下单成功（queued）与消费成功（success）计数与真实请求结果一致（可对账）。
func TestMetricsReconcile(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")

	skuID := consumeTestSku(t, "metrics-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}

	const userID = int64(907000)

	activityLabel := strconv.FormatInt(activityID, 10)

	// 下单前记录基线。
	queuedBefore := gatherMetricValue(t, "flashsale_requests_total", map[string]string{
		"interface": "order", "activity": activityLabel, "result": "queued",
	})
	successBefore := gatherMetricValue(t, "flashsale_requests_total", map[string]string{
		"interface": "consume", "activity": activityLabel, "result": "success",
	})

	// 下单 + 消费各 1 次。
	res, err := s.CreateOrder(ctx, userID, activityID, &v1.CreateOrderReq{SkuId: skuID, IdempotencyKey: "metrics-1"})
	if err != nil || res.Status != v1.RequestStatusQueued {
		t.Fatalf("create order: res=%+v err=%v", res, err)
	}
	if n, err := s.ConsumeQueued(ctx, 1); err != nil || n != 1 {
		t.Fatalf("consume: n=%d err=%v", n, err)
	}

	// 指标与真实结果一致：order/queued +1、consume/success +1。
	queuedAfter := gatherMetricValue(t, "flashsale_requests_total", map[string]string{
		"interface": "order", "activity": activityLabel, "result": "queued",
	})
	successAfter := gatherMetricValue(t, "flashsale_requests_total", map[string]string{
		"interface": "consume", "activity": activityLabel, "result": "success",
	})
	if queuedAfter-queuedBefore != 1 {
		t.Fatalf("order/queued delta=%v want 1", queuedAfter-queuedBefore)
	}
	if successAfter-successBefore != 1 {
		t.Fatalf("consume/success delta=%v want 1", successAfter-successBefore)
	}
}

// gatherMetricValue 从默认 registry 按指标名 + 标签读取 Counter 值；未观测到返回 0。
func gatherMetricValue(t *testing.T, name string, want map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := make(map[string]string)
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if matchLabels(labels, want) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func matchLabels(got, want map[string]string) bool {
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
