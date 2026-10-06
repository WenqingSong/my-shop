package cmd

// 秒杀 V2（Redis + Lua）集成测试：通过真实路由 + 真实 MySQL/Redis 覆盖 AC-001、AC-003、
// AC-006、AC-007 与 INV-006（Redis 预扣不超预热库存）、INV-007（对账收敛）。
// 一人一单/幂等的 Redis 快速路径由 flashsale_test.go 既有用例（经预热后走闸门）覆盖。

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 秒杀 Redis key 前缀（必须与 internal/logic/flashsale 保持一致，测试直接断言 Redis 状态）。
const (
	fsActivityKeyPrefix = "flashsale:activity:"
	fsStockKeyPrefix    = "flashsale:stock:"
	fsSoldoutKeyPrefix  = "flashsale:soldout:"
	fsNullKeyPrefix     = "flashsale:null:"
)

func fsActivityKey(activityID int64) string {
	return fsActivityKeyPrefix + strconv.FormatInt(activityID, 10)
}

func fsStockKey(activityID, skuID int64) string {
	return fsStockKeyPrefix + strconv.FormatInt(activityID, 10) + ":" + strconv.FormatInt(skuID, 10)
}

func fsSoldoutKey(activityID, skuID int64) string {
	return fsSoldoutKeyPrefix + strconv.FormatInt(activityID, 10) + ":" + strconv.FormatInt(skuID, 10)
}

func fsNullKey(activityID int64) string {
	return fsNullKeyPrefix + strconv.FormatInt(activityID, 10)
}

// fsRedisGet 读取 Redis string 值；key 不存在返回空串（本测试值均为非空，空串即未命中）。
func fsRedisGet(t *testing.T, key string) string {
	t.Helper()
	v, err := g.Redis().Get(context.Background(), key)
	if err != nil {
		t.Fatalf("redis get %s: %v", key, err)
	}
	return v.String()
}

func fsRedisExists(t *testing.T, key string) bool {
	t.Helper()
	n, err := g.Redis().Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("redis exists %s: %v", key, err)
	}
	return n == 1
}

// TestFlashSaleV2PreheatAndInvalidate 覆盖 AC-001：
// 管理端创建/更新/下架活动后，Redis 活动与库存缓存同步维护；下架后失效并置空值标记。
func TestFlashSaleV2PreheatAndInvalidate(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-V2-PRE", 5000, 1)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 用足够宽的时间窗，避免管理端 API 的本地时区解析与 MySQL UTC 之间的漂移把活动推出窗口。
	start := flashMySQLNow(t).Add(-24 * time.Hour).Format("2006-01-02 15:04:05")
	end := flashMySQLNow(t).Add(24 * time.Hour).Format("2006-01-02 15:04:05")

	create := isoDo(t, base, "POST", "/admin/flash-sales", map[string]any{
		"name": "预热测试", "start_time": start, "end_time": end,
		"skus": []map[string]any{{"sku_id": skuID, "flash_price": 1000, "total_stock": 10}},
	}, isoAuthHeader(adminToken))
	if create.Status != 200 || create.Code != 0 {
		t.Fatalf("create: status=%d code=%d msg=%q", create.Status, create.Code, create.Message)
	}
	activityID := int64(create.Data["id"].(float64))

	if !fsRedisExists(t, fsActivityKey(activityID)) {
		t.Fatalf("activity hash not preheated after create")
	}
	if got := fsRedisGet(t, fsStockKey(activityID, skuID)); got != "10" {
		t.Fatalf("stock key=%q want 10", got)
	}

	// 更新库存 → Redis 同步重载。
	upd := flashPut(t, base, fmt.Sprintf("/admin/flash-sales/%d", activityID), map[string]any{
		"skus": []map[string]any{{"sku_id": skuID, "flash_price": 1000, "total_stock": 20}},
	}, adminToken)
	if upd.Status != 200 || upd.Code != 0 {
		t.Fatalf("update: status=%d code=%d msg=%q", upd.Status, upd.Code, upd.Message)
	}
	if got := fsRedisGet(t, fsStockKey(activityID, skuID)); got != "20" {
		t.Fatalf("stock key after update=%q want 20", got)
	}

	// 下架 → 缓存失效 + 空值标记。
	off := flashPut(t, base, fmt.Sprintf("/admin/flash-sales/%d", activityID), map[string]any{"status": "disabled"}, adminToken)
	if off.Status != 200 || off.Code != 0 {
		t.Fatalf("disable: status=%d code=%d msg=%q", off.Status, off.Code, off.Message)
	}
	if fsRedisExists(t, fsActivityKey(activityID)) {
		t.Fatalf("activity hash should be invalidated after disable")
	}
	if !fsRedisExists(t, fsNullKey(activityID)) {
		t.Fatalf("null marker should be set after disable")
	}
}

// TestFlashSaleV2SoldOutFastFail 覆盖 AC-003：
// 库存耗尽后后续请求经售罄标记快速失败 12003，且不产生新订单、不扣 MySQL 库存。
func TestFlashSaleV2SoldOutFastFail(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-V2-OUT", 5000, 1)

	const totalStock = 2
	activityID := flashInsertActivity(t, "售罄", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, totalStock)
	flashSyncCache(t, activityID)

	for i := 0; i < totalStock; i++ {
		token := flashMintUserToken(t, int64(800000+i))
		res := flashOrderCall(t, base, activityID, token, map[string]any{"sku_id": skuID, "idempotency_key": fmt.Sprintf("k-out-%d", i)})
		if res.Status != 200 || res.Code != 0 {
			t.Fatalf("order %d: status=%d code=%d", i, res.Status, res.Code)
		}
	}

	extra := flashMintUserToken(t, 800099)
	res := flashOrderCall(t, base, activityID, extra, map[string]any{"sku_id": skuID, "idempotency_key": "k-out-extra"})
	if res.Status != 409 || res.Code != 12003 {
		t.Fatalf("sold out: status=%d code=%d want 409/12003", res.Status, res.Code)
	}
	// 首个耗尽后的请求应写入售罄标记，后续请求据此快速失败。
	if !fsRedisExists(t, fsSoldoutKey(activityID, skuID)) {
		t.Fatalf("soldout marker should be set after stock exhausted")
	}
	if n := flashOrderCount(t, activityID); n != totalStock {
		t.Fatalf("order count=%d want %d", n, totalStock)
	}
	if _, sold := flashBindingStock(t, activityID, skuID); sold != totalStock {
		t.Fatalf("sold=%d want %d", sold, totalStock)
	}
}

// TestFlashSaleV2RedisPreDeductNoOversell 覆盖 INV-006：
// 并发抢购下 Redis 预扣总量不超过预热库存（remaining 不为负），MySQL 最终 sold = 初始库存。
func TestFlashSaleV2RedisPreDeductNoOversell(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-V2-RACE", 5000, 1)

	const totalStock = 5
	activityID := flashInsertActivity(t, "并发V2", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, totalStock)
	flashSyncCache(t, activityID)

	const n = 20
	tokens := make([]string, n)
	for i := 0; i < n; i++ {
		tokens[i] = flashMintUserToken(t, int64(810000+i))
	}

	var wg sync.WaitGroup
	results := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := flashOrderCall(t, base, activityID, tokens[i], map[string]any{
				"sku_id": skuID, "idempotency_key": fmt.Sprintf("k-v2race-%d", i),
			})
			results <- (res.Status == 200 && res.Code == 0)
		}(i)
	}
	wg.Wait()
	close(results)

	success := 0
	for ok := range results {
		if ok {
			success++
		}
	}
	if success != totalStock {
		t.Fatalf("success=%d want %d", success, totalStock)
	}
	if _, sold := flashBindingStock(t, activityID, skuID); sold != totalStock {
		t.Fatalf("sold=%d want %d", sold, totalStock)
	}
	// 全部售出后 Redis remaining 应恰好为 0（预扣不超预热库存、无残留多扣）。
	if rem := fsRedisGet(t, fsStockKey(activityID, skuID)); rem != "0" {
		t.Fatalf("redis remaining=%q want 0", rem)
	}
}

// TestFlashSaleV2Reconcile 覆盖 AC-006/INV-007：
// 构造 Redis 预扣计数与 MySQL sold 不一致的场景，对账后 remaining 收敛为 total_stock - sold。
func TestFlashSaleV2Reconcile(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-V2-RECON", 5000, 1)

	activityID := flashInsertActivity(t, "对账", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, 10)
	flashSyncCache(t, activityID)

	// 场景一：Redis 预扣残留（remaining 被扣到 3，但 MySQL sold=0），对账后回补到 10。
	if _, err := g.Redis().Set(context.Background(), fsStockKey(activityID, skuID), "3"); err != nil {
		t.Fatalf("corrupt redis stock: %v", err)
	}
	if _, err := service.FlashSale().ReconcileCache(context.Background(), 100); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := fsRedisGet(t, fsStockKey(activityID, skuID)); got != "10" {
		t.Fatalf("reconcile remaining=%q want 10", got)
	}

	// 场景二：Redis 计数虚高（模拟 Redis 缺失期间 MySQL 已卖出），对账后刷成 total - sold = 8。
	for i, uid := range []int64{820000, 820001} {
		token := flashMintUserToken(t, uid)
		res := flashOrderCall(t, base, activityID, token, map[string]any{
			"sku_id": skuID, "idempotency_key": fmt.Sprintf("k-recon-%d", i),
		})
		if res.Status != 200 || res.Code != 0 {
			t.Fatalf("order %d: status=%d code=%d", i, res.Status, res.Code)
		}
	}
	if _, sold := flashBindingStock(t, activityID, skuID); sold != 2 {
		t.Fatalf("sold=%d want 2", sold)
	}
	if _, err := g.Redis().Set(context.Background(), fsStockKey(activityID, skuID), "50"); err != nil {
		t.Fatalf("corrupt redis stock: %v", err)
	}
	if _, err := service.FlashSale().ReconcileCache(context.Background(), 100); err != nil {
		t.Fatalf("reconcile2: %v", err)
	}
	if got := fsRedisGet(t, fsStockKey(activityID, skuID)); got != "8" {
		t.Fatalf("reconcile remaining=%q want 8", got)
	}
}

// TestFlashSaleV2Degrade 覆盖 AC-007：
// Lua 执行失败（活动 key 类型错误导致 EVAL 报错）时降级走 V1 纯 MySQL 路径，下单仍正确落库、不破坏不变量。
func TestFlashSaleV2Degrade(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-V2-DEG", 5000, 1)
	userToken := flashMintUserToken(t, 830000)

	activityID := flashInsertActivity(t, "降级", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, 10)
	flashSyncCache(t, activityID)

	// 将活动 key 改为 string 类型，使 Lua HGETALL 返回 WRONGTYPE 错误，模拟 Lua 执行失败。
	if _, err := g.Redis().Do(context.Background(), "SET", fsActivityKey(activityID), "not-a-hash"); err != nil {
		t.Fatalf("corrupt activity key: %v", err)
	}

	res := flashOrderCall(t, base, activityID, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "k-deg"})
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("degrade order: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	if n := flashOrderCount(t, activityID); n != 1 {
		t.Fatalf("order count=%d want 1", n)
	}
	if _, sold := flashBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("sold=%d want 1", sold)
	}
}
