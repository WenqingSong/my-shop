package cmd

// 秒杀 V5（容量保护与可观测性）集成测试：通过真实路由 + 真实 MySQL/Redis 覆盖
// 用户级限流（429/12009）与指标采集端点（GET /metrics）。排队软上限/熔断/指标对账的
// 白盒边界在 internal/logic/flashsale/capacity_test.go 覆盖，此处验证 HTTP 链路。

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

// TestFlashSaleV5RateLimitHTTP 覆盖 AC-001 的 HTTP 链路：
// 同一用户在窗口内请求速率超阈值后，经真实路由返回 429/12009，且不写 request、不预扣；
// 首个请求正常受理排队。
func TestFlashSaleV5RateLimitHTTP(t *testing.T) {
	t.Setenv("FLASH_SALE_RATE_LIMIT_ENABLED", "true")
	t.Setenv("FLASH_SALE_RATE_LIMIT_USER_MAX_REQUESTS", "1")
	t.Setenv("FLASH_SALE_RATE_LIMIT_USER_WINDOW_SECONDS", "60")
	t.Setenv("FLASH_SALE_RATE_LIMIT_ACTIVITY_MAX_REQUESTS", "0")

	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-V5-RL", 5000, 1)
	userID := int64(850000)
	userToken := flashMintUserToken(t, userID)

	activityID := flashInsertActivity(t, "限流", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, 10)
	flashSyncCache(t, activityID)

	// 第 1 次：正常排队。
	first := flashOrderCall(t, base, activityID, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "v5-rl-1"})
	if first.Status != 200 || first.Code != 0 {
		t.Fatalf("first order: status=%d code=%d", first.Status, first.Code)
	}

	// 第 2 次（换幂等键）：用户级限流拒绝 429/12009，无副作用。
	second := flashOrderCall(t, base, activityID, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "v5-rl-2"})
	if second.Status != 429 || second.Code != 12009 {
		t.Fatalf("second order: status=%d code=%d want 429/12009", second.Status, second.Code)
	}

	// 无副作用：request 仅 1 条、remaining=9（限流不预扣）。
	if n, _ := g.DB().Model("flash_sale_order_requests").Ctx(context.Background()).Count(); n != 1 {
		t.Fatalf("request count=%d want 1", n)
	}
	if rem := fsRedisGet(t, fsStockKey(activityID, skuID)); rem != "9" {
		t.Fatalf("remaining=%q want 9（限流不应预扣）", rem)
	}
}

// TestFlashSaleV5MetricsEndpoint 覆盖 AC-005 的采集链路：
// 指标开关开启后 GET /metrics 返回 Prometheus 文本格式，且包含秒杀域指标（与真实请求结果一致）。
func TestFlashSaleV5MetricsEndpoint(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")

	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-V5-MET", 5000, 1)
	userToken := flashMintUserToken(t, 850001)

	activityID := flashInsertActivity(t, "指标", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, 10)
	flashSyncCache(t, activityID)

	// 触发一次下单（产生 order/queued 指标样本）。
	if res := flashOrderCall(t, base, activityID, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "v5-met-1"}); res.Status != 200 || res.Code != 0 {
		t.Fatalf("order: status=%d code=%d", res.Status, res.Code)
	}

	// GET /metrics 返回 Prometheus 文本（200，非统一 JSON 包装）。
	r, err := g.Client().Get(context.Background(), base+"/metrics")
	if err != nil {
		t.Fatalf("get /metrics: %v", err)
	}
	defer r.Close()
	body := string(r.ReadAll())
	if r.StatusCode != 200 {
		t.Fatalf("/metrics status=%d want 200, body=%s", r.StatusCode, body)
	}
	if !strings.Contains(body, "flashsale_requests_total") {
		t.Fatalf("/metrics missing flashsale_requests_total:\n%s", body)
	}
	if !strings.Contains(body, `result="queued"`) {
		t.Fatalf("/metrics missing queued sample:\n%s", body)
	}
}
