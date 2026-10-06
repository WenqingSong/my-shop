package flashsale

// 秒杀 V2 Redis 热路径单元回归测试（包内，直接验证私有方法的行为边界）。
// 需 Redis 就绪（与集成测试同环境假设）。

import (
	"context"
	"testing"

	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/database/gredis"
	"github.com/gogf/gf/v2/frame/g"
)

// TestCompensatePreDeductClearsSoldout 回归 P3：
// MySQL 下单失败补偿 Redis 预扣时，除 INCR remaining 外必须清除售罄标记，
// 否则「预扣最后一单 → 并发置售罄 → 该单补偿」后会残留伪售罄（有库存却快速失败）。
func TestCompensatePreDeductClearsSoldout(t *testing.T) {
	gredis.SetConfig(&gredis.Config{Address: "127.0.0.1:6379", Db: 0, Pass: ""})

	ctx := context.Background()
	const activityID, skuID = int64(9000001), int64(9000002)
	stockKey := flashSaleStockKey(activityID, skuID)
	soldoutKey := flashSaleSoldoutKey(activityID, skuID)

	// 前置：模拟「预扣最后一单后并发置售罄」的残留状态 remaining=0、soldout=1。
	if _, err := g.Redis().Set(ctx, stockKey, "0"); err != nil {
		t.Fatalf("set stock: %v", err)
	}
	if _, err := g.Redis().Set(ctx, soldoutKey, "1"); err != nil {
		t.Fatalf("set soldout: %v", err)
	}
	t.Cleanup(func() {
		_, _ = g.Redis().Del(ctx, stockKey, soldoutKey)
	})

	s := New()
	s.compensatePreDeduct(ctx, activityID, skuID)

	// remaining 回补为 1。
	if got, err := g.Redis().Get(ctx, stockKey); err != nil || got.String() != "1" {
		t.Fatalf("remaining=%q err=%v want 1", got.String(), err)
	}
	// 售罄标记被清除（消除伪售罄）。
	if n, err := g.Redis().Exists(ctx, soldoutKey); err != nil || n != 0 {
		t.Fatalf("soldout marker should be cleared after compensation, exists=%d err=%v", n, err)
	}
}
