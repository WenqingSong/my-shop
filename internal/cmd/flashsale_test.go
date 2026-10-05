package cmd

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定「秒杀核心闭环」V1 的行为，覆盖 AC-001 至 AC-009 与关键不变量（INV-001 至 INV-008）。
// 核心验收为并发下单不超卖（TestFlashSaleConcurrentNoOversell，需 go test -race）。

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
)

// flashOrderEnvelope 是秒杀下单接口的统一响应封装。
type flashOrderEnvelope struct {
	Status  int
	Code    int
	Message string
	Data    *v1.FlashOrder
}

// setupFlashSaleServer 建立隔离的秒杀测试环境：复用身份隔离初始化后，清空秒杀域与商品域相关业务表。
func setupFlashSaleServer(t *testing.T) string {
	t.Helper()
	base := setupIsolationServer(t)

	ctx := context.Background()
	// 按外键依赖顺序清空（子表先于父表）。
	for _, table := range []string{
		"flash_sale_orders", "flash_sale_activity_skus", "flash_sale_activities",
		"order_items", "orders", "cart_items",
		"inventory_logs", "inventories",
		"skus", "product_images", "products",
		"addresses", "categories",
	} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	return base
}

// flashOrderCall 发起秒杀下单请求并解码响应中的秒杀订单结构。
func flashOrderCall(t *testing.T, base string, activityID int64, token string, body map[string]any) flashOrderEnvelope {
	t.Helper()
	res := isoDo(t, base, "POST", fmt.Sprintf("/flash-sales/%d/orders", activityID), body, isoAuthHeader(token))
	env := flashOrderEnvelope{Status: res.Status, Code: res.Code, Message: res.Message}
	if res.Data != nil {
		b, err := json.Marshal(res.Data)
		if err != nil {
			t.Fatalf("marshal flash order data: %v", err)
		}
		var o v1.FlashOrder
		if err := json.Unmarshal(b, &o); err != nil {
			t.Fatalf("unmarshal flash order data: %v", err)
		}
		env.Data = &o
	}
	return env
}

// flashMintUserToken 为指定 user_id 直接签发有效 token + 会话（绕过 bcrypt 登录），
// 用于并发测试批量构造不同用户；仍走真实 middleware.Auth（验签 + 会话校验）。
func flashMintUserToken(t *testing.T, userID int64) string {
	t.Helper()
	sid, err := auth.NewSid()
	if err != nil {
		t.Fatalf("new sid: %v", err)
	}
	token, err := auth.GenerateWithSecret([]byte(isoJWTSecret), auth.TypeUser, userID, sid)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	ttl, err := auth.SessionTTL(context.Background())
	if err != nil {
		t.Fatalf("session ttl: %v", err)
	}
	if err := auth.CreateSession(context.Background(), sid, userID, ttl, auth.SessionMeta{LoginAt: time.Now().Unix()}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return token
}

// flashMySQLNow 读取 MySQL 的当前墙钟时间（DATETIME 语义，无时区），
// 用于构造相对 MySQL NOW() 的时间窗，避免 Go 进程与 MySQL 时区漂移导致边界误判。
func flashMySQLNow(t *testing.T) time.Time {
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

// flashSetupSku 建立一个可售 SKU（商品 on_shelf、SKU enabled），返回 skuID。
func flashSetupSku(t *testing.T, name string, price int64, status int) int64 {
	t.Helper()
	categoryID := orderInsertCategory(t, "flash-cat-"+name)
	productID := orderInsertProduct(t, categoryID, "flash-product-"+name, 1)
	return orderInsertSku(t, productID, name, price, status)
}

// flashInsertActivity 直接写入秒杀活动（status 1=enabled、0=disabled），返回 id。
func flashInsertActivity(t *testing.T, name string, status int, start, end time.Time) int64 {
	t.Helper()
	id, err := g.DB().Model("flash_sale_activities").Ctx(context.Background()).Data(g.Map{
		"name":       name,
		"status":     status,
		"start_time": start.Format("2006-01-02 15:04:05"),
		"end_time":   end.Format("2006-01-02 15:04:05"),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert flash activity: %v", err)
	}
	return id
}

// flashInsertBinding 直接写入活动 SKU 绑定（秒杀价 + 初始库存），返回 id。
func flashInsertBinding(t *testing.T, activityID, skuID, flashPrice, totalStock int64) int64 {
	t.Helper()
	id, err := g.DB().Model("flash_sale_activity_skus").Ctx(context.Background()).Data(g.Map{
		"activity_id": activityID,
		"sku_id":      skuID,
		"flash_price": flashPrice,
		"total_stock": totalStock,
		"sold":        0,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert flash binding: %v", err)
	}
	return id
}

// flashBindingStock 查询绑定的 (total_stock, sold)。
func flashBindingStock(t *testing.T, activityID, skuID int64) (int64, int64) {
	t.Helper()
	rec, err := g.DB().Model("flash_sale_activity_skus").Ctx(context.Background()).
		Fields("total_stock", "sold").
		Where("activity_id", activityID).Where("sku_id", skuID).One()
	if err != nil {
		t.Fatalf("query flash binding: %v", err)
	}
	if rec == nil || rec.IsEmpty() {
		t.Fatalf("flash binding (%d,%d) not found", activityID, skuID)
	}
	return rec["total_stock"].Int64(), rec["sold"].Int64()
}

// flashOrderCount 统计指定活动的秒杀订单数。
func flashOrderCount(t *testing.T, activityID int64) int {
	t.Helper()
	n, err := g.DB().Model("flash_sale_orders").Ctx(context.Background()).Where("activity_id", activityID).Count()
	if err != nil {
		t.Fatalf("count flash orders: %v", err)
	}
	return n
}

// flashPut 发起 PUT 请求（isoDo 仅支持 GET/POST），用于秒杀活动更新接口。
func flashPut(t *testing.T, base, path string, body map[string]any, token string) isoResult {
	t.Helper()
	c := g.Client().Header(isoAuthHeader(token))
	r, err := c.ContentJson().Put(context.Background(), base+path, body)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer r.Close()
	var env struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	_ = json.Unmarshal(r.ReadAll(), &env)
	return isoResult{Status: r.StatusCode, Code: env.Code, Message: env.Message, Data: env.Data}
}

// flashOrderFlashPrice 查询指定秒杀订单的成交价快照。
func flashOrderFlashPrice(t *testing.T, orderID int64) int64 {
	t.Helper()
	v, err := g.DB().Model("flash_sale_orders").Ctx(context.Background()).Fields("flash_price").Where("id", orderID).Value()
	if err != nil {
		t.Fatalf("query flash order price: %v", err)
	}
	if v == nil {
		t.Fatalf("flash order %d not found", orderID)
	}
	return v.Int64()
}

// TestFlashSaleCreateActivityAndPermission 覆盖 AC-001/INV-008：
// 超管经真实路由创建活动，断言活动/SKU/秒杀价/秒杀库存/起止时间落库；无权限管理员 403 且无写入。
func TestFlashSaleCreateActivityAndPermission(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-CREATE", 5000, 1)

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	start := flashMySQLNow(t).Add(-time.Hour).Format("2006-01-02 15:04:05")
	end := flashMySQLNow(t).Add(time.Hour).Format("2006-01-02 15:04:05")

	create := isoDo(t, base, "POST", "/admin/flash-sales", map[string]any{
		"name":       "秒杀活动A",
		"start_time": start,
		"end_time":   end,
		"skus": []map[string]any{
			{"sku_id": skuID, "flash_price": 1000, "total_stock": 10},
		},
	}, isoAuthHeader(adminToken))
	if create.Status != 200 || create.Code != 0 {
		t.Fatalf("create activity: status=%d code=%d msg=%q", create.Status, create.Code, create.Message)
	}
	activityID := int64(create.Data["id"].(float64))
	if activityID <= 0 {
		t.Fatalf("create activity: empty id")
	}
	// 活动与绑定落库。
	if n, _ := g.DB().Model("flash_sale_activities").Ctx(context.Background()).Where("id", activityID).Count(); n != 1 {
		t.Fatalf("activity not persisted")
	}
	rec, err := g.DB().Model("flash_sale_activity_skus").Ctx(context.Background()).
		Fields("sku_id", "flash_price", "total_stock", "sold").
		Where("activity_id", activityID).Where("sku_id", skuID).One()
	if err != nil || rec == nil || rec.IsEmpty() {
		t.Fatalf("binding not persisted: %v", err)
	}
	if rec["flash_price"].Int64() != 1000 || rec["total_stock"].Int64() != 10 || rec["sold"].Int64() != 0 {
		t.Fatalf("unexpected binding: %+v", rec)
	}

	// 无权限普通管理员创建 → 403，无写入。
	isoInsertAdmin(t, "flashplain", "flashplain123")
	plainToken, _ := isoAdminLogin(t, base, "flashplain", "flashplain123")
	forbidden := isoDo(t, base, "POST", "/admin/flash-sales", map[string]any{
		"name":       "秒杀活动B",
		"start_time": start,
		"end_time":   end,
		"skus": []map[string]any{
			{"sku_id": skuID, "flash_price": 1000, "total_stock": 10},
		},
	}, isoAuthHeader(plainToken))
	if forbidden.Status != 403 || forbidden.Code != 1003 {
		t.Fatalf("plain admin create: status=%d code=%d want 403/1003", forbidden.Status, forbidden.Code)
	}
	if n, _ := g.DB().Model("flash_sale_activities").Ctx(context.Background()).Where("name", "秒杀活动B").Count(); n != 0 {
		t.Fatalf("forbidden create must not write activity")
	}
}

// TestFlashSaleOrderTimeWindow 覆盖 AC-002：活动开始前/结束后下单被稳定拒绝（12002），且不产生订单、不扣库存。
func TestFlashSaleOrderTimeWindow(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-TIME", 5000, 1)
	userToken := flashMintUserToken(t, 500001)

	// 未开始。
	notStarted := flashInsertActivity(t, "未开始", 1, flashMySQLNow(t).Add(24*time.Hour), flashMySQLNow(t).Add(48*time.Hour))
	flashInsertBinding(t, notStarted, skuID, 1000, 10)
	before := flashOrderCall(t, base, notStarted, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "k-before"})
	if before.Status != 409 || before.Code != 12002 {
		t.Fatalf("before start: status=%d code=%d want 409/12002", before.Status, before.Code)
	}

	// 已结束。
	ended := flashInsertActivity(t, "已结束", 1, flashMySQLNow(t).Add(-48*time.Hour), flashMySQLNow(t).Add(-24*time.Hour))
	flashInsertBinding(t, ended, skuID, 1000, 10)
	after := flashOrderCall(t, base, ended, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "k-after"})
	if after.Status != 409 || after.Code != 12002 {
		t.Fatalf("after end: status=%d code=%d want 409/12002", after.Status, after.Code)
	}

	// 均无订单、无库存扣减。
	if n := flashOrderCount(t, notStarted); n != 0 {
		t.Fatalf("not-started activity created orders: %d", n)
	}
	if n := flashOrderCount(t, ended); n != 0 {
		t.Fatalf("ended activity created orders: %d", n)
	}
	if _, sold := flashBindingStock(t, notStarted, skuID); sold != 0 {
		t.Fatalf("not-started activity sold=%d want 0", sold)
	}
	if _, sold := flashBindingStock(t, ended, skuID); sold != 0 {
		t.Fatalf("ended activity sold=%d want 0", sold)
	}
}

// TestFlashSaleOrderPricingSnapshot 覆盖 AC-003/INV-006：
// 成交价 = 服务端重读的秒杀价（非普通 SKU 价、非客户端提交价），改秒杀价后已生成订单快照不变。
func TestFlashSaleOrderPricingSnapshot(t *testing.T) {
	base := setupFlashSaleServer(t)
	// 普通 SKU 价 5000，秒杀价 1000，应成交 1000。
	skuID := flashSetupSku(t, "SKU-SNAP", 5000, 1)
	userToken := flashMintUserToken(t, 500002)

	activityID := flashInsertActivity(t, "快照", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, 10)

	env := flashOrderCall(t, base, activityID, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "k-snap"})
	if env.Status != 200 || env.Code != 0 {
		t.Fatalf("order: status=%d code=%d msg=%q", env.Status, env.Code, env.Message)
	}
	if env.Data == nil {
		t.Fatalf("order: empty data")
	}
	if env.Data.FlashPrice != 1000 {
		t.Fatalf("flash price=%d want 1000", env.Data.FlashPrice)
	}
	orderID := env.Data.Id

	// 改秒杀价后，已生成订单快照不变。
	if _, err := g.DB().Model("flash_sale_activity_skus").Ctx(context.Background()).
		Where("activity_id", activityID).Where("sku_id", skuID).
		Data(g.Map{"flash_price": 2000}).Update(); err != nil {
		t.Fatalf("update flash price: %v", err)
	}
	if got := flashOrderFlashPrice(t, orderID); got != 1000 {
		t.Fatalf("snapshot flash price changed to %d, want 1000", got)
	}
}

// TestFlashSaleOnePerUser 覆盖 AC-004/INV-003：同一用户对同一活动商品只能成功购买一次，重复被 12004 拒绝且无写入。
func TestFlashSaleOnePerUser(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-ONE", 5000, 1)

	isoInsertUser(t, "flashbuyer1", "flashbuyer123")
	userToken, _ := isoFrontendLogin(t, base, "flashbuyer1", "flashbuyer123")

	activityID := flashInsertActivity(t, "一人一单", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, 10)

	first := flashOrderCall(t, base, activityID, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "k-one-1"})
	if first.Status != 200 || first.Code != 0 {
		t.Fatalf("first order: status=%d code=%d", first.Status, first.Code)
	}

	second := flashOrderCall(t, base, activityID, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "k-one-2"})
	if second.Status != 409 || second.Code != 12004 {
		t.Fatalf("second order: status=%d code=%d want 409/12004", second.Status, second.Code)
	}
	// 仍只有一个订单、库存只扣一次。
	if n := flashOrderCount(t, activityID); n != 1 {
		t.Fatalf("order count=%d want 1", n)
	}
	if _, sold := flashBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("sold=%d want 1", sold)
	}
}

// TestFlashSaleIdempotency 覆盖 AC-005/INV-005：
// 同幂等键重复提交只产生一个订单、库存只扣一次、返回既有订单；同键不同内容返回 12005。
func TestFlashSaleIdempotency(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-IDEM", 5000, 1)
	skuID2 := flashSetupSku(t, "SKU-IDEM2", 5000, 1)
	userToken := flashMintUserToken(t, 500003)

	activityID := flashInsertActivity(t, "幂等", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, 10)
	flashInsertBinding(t, activityID, skuID2, 1000, 10)

	body := map[string]any{"sku_id": skuID, "idempotency_key": "k-idem"}
	first := flashOrderCall(t, base, activityID, userToken, body)
	if first.Status != 200 || first.Code != 0 {
		t.Fatalf("first order: status=%d code=%d", first.Status, first.Code)
	}
	second := flashOrderCall(t, base, activityID, userToken, body)
	if second.Status != 200 || second.Code != 0 {
		t.Fatalf("idempotent retry: status=%d code=%d", second.Status, second.Code)
	}
	if second.Data.Id != first.Data.Id {
		t.Fatalf("idempotent retry should return same order, got %d vs %d", second.Data.Id, first.Data.Id)
	}
	if n := flashOrderCount(t, activityID); n != 1 {
		t.Fatalf("order count=%d want 1", n)
	}
	if _, sold := flashBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("sku sold=%d want 1", sold)
	}

	// 同键不同内容（不同 SKU）→ 12005，且不扣新 SKU 库存。
	conflict := flashOrderCall(t, base, activityID, userToken, map[string]any{"sku_id": skuID2, "idempotency_key": "k-idem"})
	if conflict.Status != 409 || conflict.Code != 12005 {
		t.Fatalf("idempotency conflict: status=%d code=%d want 409/12005", conflict.Status, conflict.Code)
	}
	if _, sold := flashBindingStock(t, activityID, skuID2); sold != 0 {
		t.Fatalf("sku2 sold=%d want 0", sold)
	}
}

// TestFlashSaleConcurrentNoOversell 覆盖 AC-006/AC-007/INV-001/INV-002（核心并发验收）：
// 并发对同一活动下单，断言成功订单数 ≤ 初始库存、库存 ≥ 0、COUNT(订单) = sold、扣减总量 = 初始 - 剩余。
func TestFlashSaleConcurrentNoOversell(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-RACE", 5000, 1)

	const (
		totalStock = 5
		n          = 20
	)
	activityID := flashInsertActivity(t, "并发", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, totalStock)

	// 预先为 n 个不同用户签发 token（不依赖用户表，flash_sale_orders.user_id 为软引用）。
	tokens := make([]string, n)
	for i := 0; i < n; i++ {
		tokens[i] = flashMintUserToken(t, int64(600000+i))
	}

	var wg sync.WaitGroup
	results := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := flashOrderCall(t, base, activityID, tokens[i], map[string]any{
				"sku_id": skuID, "idempotency_key": fmt.Sprintf("k-race-%d", i),
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
		t.Fatalf("successful orders=%d want %d", success, totalStock)
	}
	total, sold := flashBindingStock(t, activityID, skuID)
	if total != totalStock {
		t.Fatalf("total_stock=%d want %d", total, totalStock)
	}
	if sold != totalStock {
		t.Fatalf("sold=%d want %d", sold, totalStock)
	}
	if total-sold < 0 {
		t.Fatalf("remaining stock negative: total=%d sold=%d", total, sold)
	}
	if got := flashOrderCount(t, activityID); got != totalStock {
		t.Fatalf("flash order count=%d want %d (=sold)", got, totalStock)
	}
}

// TestFlashSaleOrderFailures 覆盖 AC-008/AC-009/INV-004/INV-007：
// 库存不足、参数非法、活动不存在/下架、SKU 未绑定、越权等失败请求不创建订单、不扣库存、无半成品。
func TestFlashSaleOrderFailures(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-FAIL", 5000, 1)
	otherSku := flashSetupSku(t, "SKU-FAIL2", 5000, 1)
	userToken := flashMintUserToken(t, 500004)
	userToken2 := flashMintUserToken(t, 500005)

	active := flashInsertActivity(t, "失败场景", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, active, skuID, 1000, 1)

	// 库存不足：第二个用户下单，库存已耗尽 → 12003，无订单、库存不变。
	first := flashOrderCall(t, base, active, userToken, map[string]any{"sku_id": skuID, "idempotency_key": "k-fail-1"})
	if first.Status != 200 || first.Code != 0 {
		t.Fatalf("first order: status=%d code=%d", first.Status, first.Code)
	}
	insufficient := flashOrderCall(t, base, active, userToken2, map[string]any{"sku_id": skuID, "idempotency_key": "k-fail-2"})
	if insufficient.Status != 409 || insufficient.Code != 12003 {
		t.Fatalf("insufficient: status=%d code=%d want 409/12003", insufficient.Status, insufficient.Code)
	}
	if n := flashOrderCount(t, active); n != 1 {
		t.Fatalf("insufficient should not create order, count=%d want 1", n)
	}
	if _, sold := flashBindingStock(t, active, skuID); sold != 1 {
		t.Fatalf("insufficient should not deduct stock, sold=%d want 1", sold)
	}

	// 参数非法：sku_id ≤ 0 → 400/1001，无写入。
	badArg := flashOrderCall(t, base, active, userToken2, map[string]any{"sku_id": 0, "idempotency_key": "k-bad"})
	if badArg.Status != 400 || badArg.Code != 1001 {
		t.Fatalf("invalid sku: status=%d code=%d want 400/1001", badArg.Status, badArg.Code)
	}
	// 幂等键为空 → 400/1001。
	badKey := flashOrderCall(t, base, active, userToken2, map[string]any{"sku_id": skuID, "idempotency_key": "  "})
	if badKey.Status != 400 || badKey.Code != 1001 {
		t.Fatalf("invalid key: status=%d code=%d want 400/1001", badKey.Status, badKey.Code)
	}

	// 活动不存在 → 404/12001。
	notFound := flashOrderCall(t, base, 999999, userToken2, map[string]any{"sku_id": skuID, "idempotency_key": "k-nf"})
	if notFound.Status != 404 || notFound.Code != 12001 {
		t.Fatalf("not found: status=%d code=%d want 404/12001", notFound.Status, notFound.Code)
	}

	// 活动下架（status=0）→ 404/12001。
	disabled := flashInsertActivity(t, "下架", 0, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, disabled, skuID, 1000, 10)
	disRes := flashOrderCall(t, base, disabled, userToken2, map[string]any{"sku_id": skuID, "idempotency_key": "k-dis"})
	if disRes.Status != 404 || disRes.Code != 12001 {
		t.Fatalf("disabled activity: status=%d code=%d want 404/12001", disRes.Status, disRes.Code)
	}

	// SKU 未绑定该活动 → 400/1001。
	notBound := flashOrderCall(t, base, active, userToken2, map[string]any{"sku_id": otherSku, "idempotency_key": "k-nb"})
	if notBound.Status != 400 || notBound.Code != 1001 {
		t.Fatalf("not bound: status=%d code=%d want 400/1001", notBound.Status, notBound.Code)
	}

	// 越权：无 token → 401/1002。
	unauth := flashOrderCall(t, base, active, "", map[string]any{"sku_id": skuID, "idempotency_key": "k-unauth"})
	if unauth.Status != 401 || unauth.Code != 1002 {
		t.Fatalf("unauthorized: status=%d code=%d want 401/1002", unauth.Status, unauth.Code)
	}

	// 最终无半成品：订单数仍为 1，库存仍为 1。
	if n := flashOrderCount(t, active); n != 1 {
		t.Fatalf("final order count=%d want 1", n)
	}
	if _, sold := flashBindingStock(t, active, skuID); sold != 1 {
		t.Fatalf("final sold=%d want 1", sold)
	}
}

// TestFlashSaleUpdateActivity 覆盖更新接口（改名称/状态/秒杀价/库存）：
// 超管经真实路由 PUT 更新活动，断言变更落库；已售绑定保留 sold 且要求新库存 ≥ 已售。
func TestFlashSaleUpdateActivity(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuID := flashSetupSku(t, "SKU-UPDATE", 5000, 1)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	activityID := flashInsertActivity(t, "更新前", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuID, 1000, 10)

	upd := flashPut(t, base, fmt.Sprintf("/admin/flash-sales/%d", activityID), map[string]any{
		"name":   "更新后",
		"status": "disabled",
		"skus": []map[string]any{
			{"sku_id": skuID, "flash_price": 2000, "total_stock": 20},
		},
	}, adminToken)
	if upd.Status != 200 || upd.Code != 0 {
		t.Fatalf("update activity: status=%d code=%d msg=%q", upd.Status, upd.Code, upd.Message)
	}

	rec, err := g.DB().Model("flash_sale_activities").Ctx(context.Background()).
		Fields("name", "status").Where("id", activityID).One()
	if err != nil {
		t.Fatalf("query activity: %v", err)
	}
	if rec["name"].String() != "更新后" || rec["status"].Int() != 0 {
		t.Fatalf("unexpected activity after update: name=%q status=%d", rec["name"].String(), rec["status"].Int())
	}
	b, err := g.DB().Model("flash_sale_activity_skus").Ctx(context.Background()).
		Fields("flash_price", "total_stock", "sold").
		Where("activity_id", activityID).Where("sku_id", skuID).One()
	if err != nil {
		t.Fatalf("query binding: %v", err)
	}
	if b["flash_price"].Int64() != 2000 || b["total_stock"].Int64() != 20 || b["sold"].Int64() != 0 {
		t.Fatalf("unexpected binding after update: %+v", b)
	}
}

// TestFlashSaleUpdateCannotAddOrRemoveBindings 回归 CLEAN-001：
// Update 只能修改已存在绑定的秒杀价/库存，不允许新增或删除绑定，
// 防止「删除已售绑定 → 重加 sold=0 → 重新出售完整库存」导致的超卖。
func TestFlashSaleUpdateCannotAddOrRemoveBindings(t *testing.T) {
	base := setupFlashSaleServer(t)
	skuA := flashSetupSku(t, "SKU-CLEAN-A", 5000, 1)
	skuB := flashSetupSku(t, "SKU-CLEAN-B", 5000, 1)
	skuC := flashSetupSku(t, "SKU-CLEAN-C", 5000, 1)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	activityID := flashInsertActivity(t, "增删绑定", 1, flashMySQLNow(t).Add(-time.Hour), flashMySQLNow(t).Add(time.Hour))
	flashInsertBinding(t, activityID, skuA, 1000, 10)
	flashInsertBinding(t, activityID, skuB, 1000, 10)

	// 卖出 skuA 6 件（6 个不同用户），使 skuA.sold=6。
	for i := 0; i < 6; i++ {
		token := flashMintUserToken(t, int64(700000+i))
		res := flashOrderCall(t, base, activityID, token, map[string]any{
			"sku_id": skuA, "idempotency_key": fmt.Sprintf("clean-%d", i),
		})
		if res.Status != 200 || res.Code != 0 {
			t.Fatalf("order %d: status=%d code=%d", i, res.Status, res.Code)
		}
	}
	if _, sold := flashBindingStock(t, activityID, skuA); sold != 6 {
		t.Fatalf("skuA sold=%d want 6", sold)
	}

	// 1) 更新时只提交 skuA（省略 skuB）→ skuB 不能被删除，skuA.sold 保留。
	upd := flashPut(t, base, fmt.Sprintf("/admin/flash-sales/%d", activityID), map[string]any{
		"skus": []map[string]any{
			{"sku_id": skuA, "flash_price": 1500, "total_stock": 10},
		},
	}, adminToken)
	if upd.Status != 200 || upd.Code != 0 {
		t.Fatalf("update subset: status=%d code=%d msg=%q", upd.Status, upd.Code, upd.Message)
	}
	if n, _ := g.DB().Model("flash_sale_activity_skus").Ctx(context.Background()).
		Where("activity_id", activityID).Where("sku_id", skuB).Count(); n != 1 {
		t.Fatalf("skuB binding should not be deleted, count=%d", n)
	}
	if _, sold := flashBindingStock(t, activityID, skuA); sold != 6 {
		t.Fatalf("skuA sold after subset update=%d want 6 (must be preserved)", sold)
	}

	// 2) 更新时提交未绑定的 skuC → 拒绝 12007，且不新增绑定。
	bad := flashPut(t, base, fmt.Sprintf("/admin/flash-sales/%d", activityID), map[string]any{
		"skus": []map[string]any{
			{"sku_id": skuC, "flash_price": 1000, "total_stock": 10},
		},
	}, adminToken)
	if bad.Status != 400 || bad.Code != 12007 {
		t.Fatalf("add unbound sku: status=%d code=%d want 400/12007", bad.Status, bad.Code)
	}
	if n, _ := g.DB().Model("flash_sale_activity_skus").Ctx(context.Background()).
		Where("activity_id", activityID).Where("sku_id", skuC).Count(); n != 0 {
		t.Fatalf("skuC binding should not be created, count=%d", n)
	}
}
