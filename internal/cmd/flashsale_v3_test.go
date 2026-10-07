package cmd

// 秒杀 V3（异步下单）集成测试：通过真实路由 + 真实 MySQL/Redis 覆盖 AC-001（入队快速响应）、
// AC-003（消费幂等/重复消息去重）、AC-005（结果查询与归属隔离）、AC-006（失败补偿）。
// AC-004（失败重试/死信）在 internal/logic/flashsale/consume_test.go 白盒测试覆盖。
// 入队后显式驱动 ConsumeQueued 消费（测试不依赖后台 ticker），再断言落库事实与 Redis 补偿。

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
)

// fsBoughtKey / fsIdemKey 与 internal/logic/flashsale 的 Redis key 约定一致，测试直接断言标记状态。
func fsBoughtKey(activityID, skuID, userID int64) string {
	return "flashsale:bought:" + strconv.FormatInt(activityID, 10) + ":" + strconv.FormatInt(skuID, 10) + ":" + strconv.FormatInt(userID, 10)
}

func fsIdemKey(userID int64, key string) string {
	return "flashsale:idem:" + strconv.FormatInt(userID, 10) + ":" + key
}

// flashResultEnvelope 是秒杀下单结果查询接口的统一响应封装。
type flashResultEnvelope struct {
	Status  int
	Code    int
	Message string
	Data    *v1.GetOrderResultRes
}

// flashResultCall 发起结果查询请求并解码响应。
func flashResultCall(t *testing.T, base string, activityID int64, token, key string) flashResultEnvelope {
	t.Helper()
	res := isoDo(t, base, "GET", fmt.Sprintf("/flash-sales/%d/orders/result?idempotency_key=%s", activityID, key), nil, isoAuthHeader(token))
	env := flashResultEnvelope{Status: res.Status, Code: res.Code, Message: res.Message}
	if res.Data != nil {
		b, err := json.Marshal(res.Data)
		if err != nil {
			t.Fatalf("marshal flash result data: %v", err)
		}
		var o v1.GetOrderResultRes
		if err := json.Unmarshal(b, &o); err != nil {
			t.Fatalf("unmarshal flash result data: %v", err)
		}
		env.Data = &o
	}
	return env
}

// TestFlashSaleV3EnqueueAndResultQuery 覆盖 AC-001/AC-005：
// 入队快速返回 queued 且不同步落库；结果查询按 (flash_sale_id, user_id, idempotency_key) 归属隔离；
// 消费后查询返回 success + 订单。
func TestFlashSaleV3EnqueueAndResultQuery(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-V3-Q", 5000, 1)
	userID := int64(832000)
	userToken := flashMintUserToken(t, userID)
	otherToken := flashMintUserToken(t, 832001)

	activityID := flashInsertActivity(t, "查询", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, 10)
	flashSyncCache(t, activityID)

	// AC-001：入队快速返回 queued，且此时订单未同步落库。
	env := flashOrderCall(t, base, activityID, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "k-q"})
	if env.Status != 200 || env.Code != 0 {
		t.Fatalf("enqueue: status=%d code=%d", env.Status, env.Code)
	}
	if env.Data == nil || env.Data.Status != v1.RequestStatusQueued {
		t.Fatalf("enqueue should return queued, got %+v", env.Data)
	}
	if n := flashOrderCount(t, activityID); n != 0 {
		t.Fatalf("order should not be created synchronously, count=%d", n)
	}

	// AC-005：查询结果（queued），仅本人可见。
	res := flashResultCall(t, base, activityID, userToken, "k-q")
	if res.Status != 200 || res.Code != 0 || res.Data == nil || res.Data.Status != v1.RequestStatusQueued {
		t.Fatalf("result queued: status=%d code=%d data=%+v", res.Status, res.Code, res.Data)
	}
	// 他人查询 → 404（归属隔离，无副作用）。
	if other := flashResultCall(t, base, activityID, otherToken, "k-q"); other.Status != 404 || other.Code != 1004 {
		t.Fatalf("other result: status=%d code=%d want 404/1004", other.Status, other.Code)
	}
	// 不存在 → 404。
	if missing := flashResultCall(t, base, activityID, userToken, "k-none"); missing.Status != 404 || missing.Code != 1004 {
		t.Fatalf("missing result: status=%d code=%d want 404/1004", missing.Status, missing.Code)
	}

	// 消费后查询 → success + 订单。
	flashConsume(t, 10)
	res = flashResultCall(t, base, activityID, userToken, "k-q")
	if res.Status != 200 || res.Code != 0 || res.Data == nil || res.Data.Status != v1.RequestStatusSuccess {
		t.Fatalf("result success: status=%d code=%d data=%+v", res.Status, res.Code, res.Data)
	}
	if res.Data.Order == nil || res.Data.Order.FlashPrice != 1000 {
		t.Fatalf("result success should include order, got %+v", res.Data.Order)
	}
}

// TestFlashSaleV3ConsumeIdempotent 覆盖 AC-003：
// 同一请求消费后再次消费不重复建单、不重复扣库存（FOR UPDATE SKIP LOCKED + 状态原子更新保证只处理一次）。
func TestFlashSaleV3ConsumeIdempotent(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-V3-ID", 5000, 1)
	userToken := flashMintUserToken(t, 833000)

	activityID := flashInsertActivity(t, "消费幂等", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, 10)
	flashSyncCache(t, activityID)

	if res := flashOrderCall(t, base, activityID, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "k-id"}); res.Status != 200 || res.Code != 0 {
		t.Fatalf("enqueue: status=%d code=%d", res.Status, res.Code)
	}
	flashConsume(t, 10)
	if n := flashOrderCount(t, activityID); n != 1 {
		t.Fatalf("order count=%d want 1", n)
	}
	// 再次消费：无 queued 请求，不重复扣库存、不重复建单。
	flashConsume(t, 10)
	if n := flashOrderCount(t, activityID); n != 1 {
		t.Fatalf("order count after reconsume=%d want 1", n)
	}
	if _, sold := flashBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("sold after reconsume=%d want 1", sold)
	}
}

// TestFlashSaleV3Compensation 覆盖 AC-006：
// 入队成功（闸门预扣 + 写标记）但消费落单失败（时间窗结束）→ 业务失败 failed，
// 补偿 Redis 预扣（remaining 回补）并清除 bought/idem 标记，不建单、不扣库存、无残留。
func TestFlashSaleV3Compensation(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-V3-COMP", 5000, 1)
	userID := int64(834000)
	userToken := flashMintUserToken(t, userID)

	activityID := flashInsertActivity(t, "补偿", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, 10)
	flashSyncCache(t, activityID)

	// 入队成功（闸门预扣 + 写标记）。
	if env := flashOrderCall(t, base, activityID, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "k-comp"}); env.Status != 200 || env.Code != 0 {
		t.Fatalf("enqueue: status=%d code=%d", env.Status, env.Code)
	}
	if rem := fsRedisGet(t, fsStockKey(activityID, skuID)); rem != "9" {
		t.Fatalf("remaining after enqueue=%q want 9", rem)
	}
	if !fsRedisExists(t, fsBoughtKey(activityID, skuID, userID)) {
		t.Fatalf("bought marker should be set after enqueue")
	}

	// 构造「入队成功但落单失败」：时间窗在入队后结束（仅改 MySQL，不改 Redis 缓存）。
	if _, err := g.DB().Model("flash_sale_activities").Ctx(context.Background()).
		Where("id", activityID).
		Data(g.Map{"end_time": flashMySQLNow(t).Add(-time.Hour).Format("2006-01-02 15:04:05")}).Update(); err != nil {
		t.Fatalf("expire activity: %v", err)
	}

	// 消费 → 时间窗结束 → 业务失败 failed。
	flashConsume(t, 10)
	if st := flashRequestStatus(t, userID, "k-comp"); st != 2 {
		t.Fatalf("request status=%d want failed(2)", st)
	}
	if n := flashOrderCount(t, activityID); n != 0 {
		t.Fatalf("failed consume must not create order, count=%d", n)
	}
	if _, sold := flashBindingStock(t, activityID, skuID); sold != 0 {
		t.Fatalf("failed consume must not deduct stock, sold=%d", sold)
	}
	// 补偿：Redis 预扣回补（9→10）、清除 bought/idem 标记。
	if rem := fsRedisGet(t, fsStockKey(activityID, skuID)); rem != "10" {
		t.Fatalf("remaining after compensation=%q want 10", rem)
	}
	if fsRedisExists(t, fsBoughtKey(activityID, skuID, userID)) {
		t.Fatalf("bought marker should be cleared after compensation")
	}
	if fsRedisExists(t, fsIdemKey(userID, "k-comp")) {
		t.Fatalf("idem marker should be cleared after compensation")
	}
}
