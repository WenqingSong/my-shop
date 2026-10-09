package flashsale

// 消费链路指标计数口径测试（INV-M02 的 consume 侧）：
// 每次「出队成功」恰好一次观测；技术失败重试的每次领取各产生一次 error 观测；
// 「无请求可领取」不产生观测。复用 consume_test.go 的 setupConsumeTest 环境（MySQL + Redis）。
// 通过覆盖 generateOrderNo 固定订单号，确定性触发 uk_flash_order_no 撞号（技术失败）。

import (
	"context"
	"testing"

	"github.com/gogf/gf/v2/frame/g"
)

// consumeMetric 读取指定 consume 结果标签的 Counter 值（未观测返回 0）。
func consumeMetric(t *testing.T, result string) float64 {
	t.Helper()
	return gatherMetricValue(t, "flashsale_requests_total", map[string]string{
		"interface": "consume", "result": result,
	})
}

func TestConsumeMetricsCounting(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")

	skuID := consumeTestSku(t, "consume-metrics-sku")
	activityID := consumeTestActivity(t, skuID)
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}

	const (
		userID = int64(908000)
		key    = "k-consume-metrics"
	)

	// 1) 无请求可领取：不产生任何 consume 观测（success/error 均不变）。
	successBefore := consumeMetric(t, "success")
	errorBefore := consumeMetric(t, "error")
	if n, err := s.ConsumeQueued(ctx, 1); err != nil || n != 0 {
		t.Fatalf("empty consume: n=%d err=%v", n, err)
	}
	if consumeMetric(t, "success") != successBefore || consumeMetric(t, "error") != errorBefore {
		t.Fatalf("empty consume must not observe any metric")
	}

	// 2) 技术失败（订单号撞号）：领取一次 → error +1（error 计数 = 失败领取次数）。
	if _, err := g.DB().Model("flash_sale_orders").Ctx(ctx).Data(g.Map{
		"order_no": "FS-METRICS-FIXED", "user_id": 1, "activity_id": 999999, "sku_id": 999999,
		"product_id": 1, "sku_name": "x", "product_name": "x", "product_main_image": "",
		"flash_price": 1, "quantity": 1, "idempotency_key": "occupied-metrics", "request_hash": "occupied-metrics",
	}).Insert(); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if _, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Data(g.Map{
		"user_id": userID, "activity_id": activityID, "sku_id": skuID,
		"idempotency_key": key, "request_hash": requestHash(activityID, skuID),
		"status": requestStatusQueued,
	}).InsertAndGetId(); err != nil {
		t.Fatalf("insert request: %v", err)
	}
	orig := generateOrderNo
	generateOrderNo = func() string { return "FS-METRICS-FIXED" }
	errorBefore = consumeMetric(t, "error")
	n, err := s.ConsumeQueued(ctx, 1)
	generateOrderNo = orig
	if err != nil || n != 1 {
		t.Fatalf("fail consume: n=%d err=%v", n, err)
	}
	if delta := consumeMetric(t, "error") - errorBefore; delta != 1 {
		t.Fatalf("technical failure should observe error once, got delta=%v", delta)
	}

	// 3) 成功终态：恢复随机订单号后重处理成功 → success +1。
	successBefore = consumeMetric(t, "success")
	if _, err := g.DB().Exec(ctx,
		"UPDATE flash_sale_order_requests SET status=0, retry_count=0, next_attempt_at=NULL WHERE user_id=? AND idempotency_key=?", userID, key); err != nil {
		t.Fatalf("reset request: %v", err)
	}
	if n, err := s.ConsumeQueued(ctx, 1); err != nil || n != 1 {
		t.Fatalf("success consume: n=%d err=%v", n, err)
	}
	if delta := consumeMetric(t, "success") - successBefore; delta != 1 {
		t.Fatalf("success terminal should observe success once, got delta=%v", delta)
	}
}
