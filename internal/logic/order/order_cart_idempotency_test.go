package order

// 购物车来源下单的幂等重放（真实 MySQL）。
//
// 背景：下单事务会删除购物车中已勾选的已购条目，用同一幂等键重放时
// buildLines 只能得到 9004。docs/design/order.md INV-002 要求"同 key 同内容
// 返回既有订单、不同内容返回 9006"，因此重放必须能拿回既有订单，而不是新错误。

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"
	"github.com/gogf/gf/v2/frame/g"

	cartv1 "cnb.cool/go-cloud-devops/my-shop/api/cart/v1"
	orderv1 "cnb.cool/go-cloud-devops/my-shop/api/order/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// TestCartOrderIdempotentReplayAfterCartConsumed 覆盖：
//  1. 购物车下单成功 → 已购条目被删除；
//  2. 同键同地址重放 → 返回既有订单（修复前为 9004）；
//  3. 同键不同地址 → 9006（不得把不同内容当成幂等）。
func TestCartOrderIdempotentReplayAfterCartConsumed(t *testing.T) {
	userID, skuID, addressID := setupChainTest(t)
	ctx := context.Background()

	quantity := json.Number("2")
	if _, err := service.Cart().Add(ctx, userID, &cartv1.AddReq{
		SkuId:    skuID,
		Quantity: &quantity,
	}); err != nil {
		t.Fatalf("加入购物车: %v", err)
	}

	key := fmt.Sprintf("cart-replay-%d", chainSeq.Add(1))
	created, err := service.Order().Create(ctx, userID, &orderv1.CreateReq{
		Source:         orderv1.SourceCart,
		AddressId:      addressID,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("创建购物车订单: %v", err)
	}

	// 重放：同键、同地址、购物车已被服务端清空。
	replay, err := service.Order().Create(ctx, userID, &orderv1.CreateReq{
		Source:         orderv1.SourceCart,
		AddressId:      addressID,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("幂等重放应返回既有订单，实际报错: %v", err)
	}
	if replay.Id != created.Id {
		t.Fatalf("重放返回订单 id=%d，期望既有订单 id=%d", replay.Id, created.Id)
	}
	if replay.TotalAmount != created.TotalAmount {
		t.Fatalf("重放金额=%d，期望与既有订单一致 %d", replay.TotalAmount, created.TotalAmount)
	}

	// 同键不同地址：属于"同键不同内容"，必须拒绝。
	// 注意 uk_user_default 只允许一个默认地址，这里显式建非默认地址。
	otherAddressID, err := g.DB().Model("addresses").Ctx(ctx).Data(g.Map{
		"user_id":        userID,
		"recipient_name": "李四",
		"phone":          "13700000001",
		"province":       "省",
		"city":           "市",
		"district":       "区",
		"detail":         "另一收货地址",
		"is_default":     0,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入第二个地址: %v", err)
	}

	_, err = service.Order().Create(ctx, userID, &orderv1.CreateReq{
		Source:         orderv1.SourceCart,
		AddressId:      otherAddressID,
		IdempotencyKey: key,
	})
	if codes.FromError(err) != codes.CodeOrderIdempotencyConflict {
		t.Fatalf("同键不同地址应返回 9006，实际: %v", err)
	}
}
