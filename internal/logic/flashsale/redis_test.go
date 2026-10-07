package flashsale

// 秒杀 V4 Redis 权威值收敛回归测试（包内，直接验证私有方法的行为边界）。
// 覆盖 INV-014「补偿幂等/不重不漏」：convergeStock 重算并幂等 SET remaining = total_stock - sold - inflight_queued，
// 重复执行/崩溃重放不改变结果（不再是非幂等 INCR），并清除售罄标记。需 MySQL + Redis 就绪。

import (
	"context"
	"testing"

	"github.com/gogf/gf/v2/frame/g"
)

// TestConvergeStockIdempotentAndClearsSoldout 覆盖 INV-014（AC-004）：
// 将 Redis remaining 故意置为错误值并置售罄标记，convergeStock 收敛到权威值并清除售罄标记；
// 再次收敛（模拟崩溃重放/重复触发）结果不变，证明幂等（不超预扣）。
func TestConvergeStockIdempotentAndClearsSoldout(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "conv-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10, sold=0

	stockKey := flashSaleStockKey(activityID, skuID)
	soldoutKey := flashSaleSoldoutKey(activityID, skuID)

	// 前置：模拟「预扣后残留 + 伪售罄」的中间态（remaining=3、soldout=1）。
	if _, err := g.Redis().Set(ctx, stockKey, "3"); err != nil {
		t.Fatalf("set stock: %v", err)
	}
	if _, err := g.Redis().Set(ctx, soldoutKey, "1"); err != nil {
		t.Fatalf("set soldout: %v", err)
	}

	// 收敛：remaining 应回补到权威值 10（total=10 - sold=0 - inflight=0），并清除售罄。
	s.convergeStock(ctx, activityID, skuID)
	if got, err := g.Redis().Get(ctx, stockKey); err != nil || got.String() != "10" {
		t.Fatalf("after converge remaining=%q err=%v want 10", got.String(), err)
	}
	if n, err := g.Redis().Exists(ctx, soldoutKey); err != nil || n != 0 {
		t.Fatalf("soldout should be cleared, exists=%d err=%v", n, err)
	}

	// 幂等：再次收敛（崩溃重放/重复触发）结果仍为 10，不会 INCR 超预扣。
	s.convergeStock(ctx, activityID, skuID)
	if got, err := g.Redis().Get(ctx, stockKey); err != nil || got.String() != "10" {
		t.Fatalf("after repeat converge remaining=%q err=%v want 10（非幂等导致超预扣）", got.String(), err)
	}
}

// TestConvergeStockCountsInflightQueued 覆盖 INV-014 的在途口径：
// 存在 status=queued 的在途请求时，convergeStock 收敛到 total_stock - sold - inflight_queued，
// 不得把在途预扣错误回补（否则 remaining 超权威值）。
func TestConvergeStockCountsInflightQueued(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()

	skuID := consumeTestSku(t, "conv-inflight-sku")
	activityID := consumeTestActivity(t, skuID) // total_stock=10, sold=0

	// 预置一个 queued 请求（在途预扣 1）。
	if _, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Data(g.Map{
		"user_id":         900001,
		"activity_id":     activityID,
		"sku_id":          skuID,
		"idempotency_key": "inflight-key",
		"request_hash":    requestHash(activityID, skuID),
		"status":          requestStatusQueued,
	}).InsertAndGetId(); err != nil {
		t.Fatalf("insert queued request: %v", err)
	}

	// 收敛：remaining = 10 - 0 - 1 = 9（在途计入，不错误回补到 10）。
	s.convergeStock(ctx, activityID, skuID)
	if got, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID)); err != nil || got.String() != "9" {
		t.Fatalf("remaining=%q err=%v want 9（在途预扣未计入导致超预扣）", got.String(), err)
	}
}
