package cmd

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定「订单核心闭环」的行为。覆盖 order-v1 的 AC-001 至 AC-015 与关键不变量。

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/order/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// parseUserID 解析 JWT subject 为用户 id。
func parseUserID(t *testing.T, subject string) (int64, error) {
	t.Helper()
	return strconv.ParseInt(subject, 10, 64)
}

// orderEnvelope 是订单相关接口的统一响应封装。
type orderEnvelope struct {
	Status  int
	Code    int
	Message string
	Data    *v1.Order
}

// setupOrderServer 建立隔离的订单测试环境：复用身份隔离初始化后，清空订单域相关业务表。
func setupOrderServer(t *testing.T) string {
	t.Helper()
	base := setupIsolationServer(t)

	ctx := context.Background()
	// 按外键依赖顺序清空（子表先于父表）。
	for _, table := range []string{
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

// orderCall 发起订单请求并解码响应中的订单结构（单订单接口）。
func orderCall(t *testing.T, base, method, path, token string, body any) orderEnvelope {
	t.Helper()
	res := isoDo(t, base, method, path, body, isoAuthHeader(token))
	env := orderEnvelope{Status: res.Status, Code: res.Code, Message: res.Message}
	if res.Data != nil {
		b, err := json.Marshal(res.Data)
		if err != nil {
			t.Fatalf("marshal order data: %v", err)
		}
		var o v1.Order
		if err := json.Unmarshal(b, &o); err != nil {
			t.Fatalf("unmarshal order data: %v", err)
		}
		env.Data = &o
	}
	return env
}

// orderInsertCategory 直接写入一个启用叶子分类，返回 id。
func orderInsertCategory(t *testing.T, name string) int64 {
	t.Helper()
	id, err := g.DB().Model("categories").Ctx(context.Background()).Data(g.Map{
		"parent_id": 0, "name": name, "sort": 0, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert category: %v", err)
	}
	return id
}

// orderInsertProduct 直接写入商品（status 1=on_shelf），返回 id。
func orderInsertProduct(t *testing.T, categoryID int64, name string, status int) int64 {
	t.Helper()
	id, err := g.DB().Model("products").Ctx(context.Background()).Data(g.Map{
		"name": name, "brand": "", "category_id": categoryID,
		"price": 10000, "main_image": "http://img.example/x.png", "status": status,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	return id
}

// orderInsertSku 直接写入 SKU（status 1=enabled），返回 id。
func orderInsertSku(t *testing.T, productID int64, name string, price int64, status int) int64 {
	t.Helper()
	id, err := g.DB().Model("skus").Ctx(context.Background()).Data(g.Map{
		"product_id": productID, "name": name, "price": price, "status": status,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert sku: %v", err)
	}
	return id
}

// orderInsertInventory 直接写入库存记录。
func orderInsertInventory(t *testing.T, skuID, qty int64) {
	t.Helper()
	if _, err := g.DB().Model("inventories").Ctx(context.Background()).Data(g.Map{
		"sku_id": skuID, "quantity": qty,
	}).Insert(); err != nil {
		t.Fatalf("insert inventory: %v", err)
	}
}

// orderInsertAddress 直接写入当前用户的收货地址，返回 id。
func orderInsertAddress(t *testing.T, userID int64) int64 {
	t.Helper()
	id, err := g.DB().Model("addresses").Ctx(context.Background()).Data(g.Map{
		"user_id": userID, "recipient_name": "张三", "phone": "13800138000",
		"province": "广东省", "city": "深圳市", "district": "南山区", "detail": "科技园1号",
		"is_default": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert address: %v", err)
	}
	return id
}

// orderInsertCartItem 直接写入购物车条目。
func orderInsertCartItem(t *testing.T, userID, skuID, qty int64, selected bool) int64 {
	t.Helper()
	sel := 0
	if selected {
		sel = 1
	}
	id, err := g.DB().Model("cart_items").Ctx(context.Background()).Data(g.Map{
		"user_id": userID, "sku_id": skuID, "quantity": qty, "price_snapshot": 0, "selected": sel,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert cart item: %v", err)
	}
	return id
}

// orderStock 查询指定 SKU 的当前库存（无记录为 0）。
func orderStock(t *testing.T, skuID int64) int64 {
	t.Helper()
	v, err := g.DB().Model("inventories").Ctx(context.Background()).Fields("quantity").Where("sku_id", skuID).Value()
	if err != nil {
		t.Fatalf("query stock: %v", err)
	}
	if v == nil {
		return 0
	}
	return v.Int64()
}

// orderCount 统计指定用户的订单数。
func orderCount(t *testing.T, userID int64) int {
	t.Helper()
	n, err := g.DB().Model("orders").Ctx(context.Background()).Where("user_id", userID).Count()
	if err != nil {
		t.Fatalf("count orders: %v", err)
	}
	return n
}

// orderStatus 查询订单 DB 状态（TINYINT）。
func orderStatus(t *testing.T, orderID int64) int {
	t.Helper()
	v, err := g.DB().Model("orders").Ctx(context.Background()).Fields("status").Where("id", orderID).Value()
	if err != nil {
		t.Fatalf("query order status: %v", err)
	}
	if v == nil {
		t.Fatalf("order %d not found", orderID)
	}
	return v.Int()
}

// orderExpire 将指定订单的 expire_at 置为过去，用于触发超时取消。
func orderExpire(t *testing.T, orderID int64) {
	t.Helper()
	if _, err := g.DB().Model("orders").Ctx(context.Background()).Where("id", orderID).Data(g.Map{
		"expire_at": time.Now().Add(-time.Minute),
	}).Update(); err != nil {
		t.Fatalf("expire order: %v", err)
	}
}

// setupSellable 建立一个可售 SKU 与库存，返回 (skuID, productID)。
func setupSellable(t *testing.T, skuName string, price, stock int64) (int64, int64) {
	t.Helper()
	categoryID := orderInsertCategory(t, "测试分类-"+skuName)
	productID := orderInsertProduct(t, categoryID, "测试商品-"+skuName, 1)
	skuID := orderInsertSku(t, productID, skuName, price, 1)
	orderInsertInventory(t, skuID, stock)
	return skuID, productID
}

// TestOrderCreateDirect 覆盖 AC-001/AC-002/AC-003：直接购买创建订单、服务端定价、唯一订单号。
func TestOrderCreateDirect(t *testing.T) {
	base := setupOrderServer(t)
	skuID, _ := setupSellable(t, "SKU-A", 5000, 10)

	isoInsertUser(t, "buyer1", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer1", "buyerpass123")

	// 获取 buyer1 的用户 id。
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)

	addressID := orderInsertAddress(t, userID)

	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-direct-1",
		"sku_id": skuID, "quantity": 2,
	})
	if env.Status != 200 || env.Code != 0 {
		t.Fatalf("direct create: status=%d code=%d msg=%q", env.Status, env.Code, env.Message)
	}
	if env.Data == nil {
		t.Fatalf("direct create: empty data")
	}
	if env.Data.OrderNo == "" {
		t.Fatalf("direct create: empty order_no")
	}
	if env.Data.UserId != userID {
		t.Fatalf("direct create: user_id=%d want %d", env.Data.UserId, userID)
	}
	if env.Data.Status != v1.StatusPendingPayment {
		t.Fatalf("direct create: status=%s want pending_payment", env.Data.Status)
	}
	// 成交价 = sku.price * qty，且 item 价格为 sku.price（服务端定价，非客户端提交）。
	if env.Data.TotalAmount != 10000 {
		t.Fatalf("direct create: total_amount=%d want 10000", env.Data.TotalAmount)
	}
	if len(env.Data.Items) != 1 || env.Data.Items[0].Price != 5000 || env.Data.Items[0].Quantity != 2 {
		t.Fatalf("direct create: unexpected items %+v", env.Data.Items)
	}
	// 库存扣减。
	if got := orderStock(t, skuID); got != 8 {
		t.Fatalf("direct create: stock=%d want 8", got)
	}

	// 唯一订单号：再建一单，订单号不同。
	env2 := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-direct-2",
		"sku_id": skuID, "quantity": 1,
	})
	if env2.Status != 200 || env2.Code != 0 {
		t.Fatalf("second direct create: status=%d code=%d", env2.Status, env2.Code)
	}
	if env2.Data.OrderNo == env.Data.OrderNo {
		t.Fatalf("orders must have unique order_no, got same %q", env.Data.OrderNo)
	}
}

// TestOrderCreateFromCart 覆盖 AC-001：从购物车勾选项创建订单，并清理已购条目。
func TestOrderCreateFromCart(t *testing.T) {
	base := setupOrderServer(t)
	sku1, _ := setupSellable(t, "SKU-C1", 1000, 10)
	sku2, _ := setupSellable(t, "SKU-C2", 2000, 10)
	sku3, _ := setupSellable(t, "SKU-C3", 5000, 10)

	isoInsertUser(t, "buyer2", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer2", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	// 两个勾选条目 + 一个未勾选条目。
	orderInsertCartItem(t, userID, sku1, 2, true)
	orderInsertCartItem(t, userID, sku2, 3, true)
	orderInsertCartItem(t, userID, sku3, 1, false)

	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "cart", "address_id": addressID, "idempotency_key": "key-cart-1",
	})
	if env.Status != 200 || env.Code != 0 {
		t.Fatalf("cart create: status=%d code=%d msg=%q", env.Status, env.Code, env.Message)
	}
	// total = 1000*2 + 2000*3 = 8000（未勾选的 sku3 不参与）。
	if env.Data.TotalAmount != 8000 {
		t.Fatalf("cart create: total_amount=%d want 8000", env.Data.TotalAmount)
	}
	if len(env.Data.Items) != 2 {
		t.Fatalf("cart create: items=%d want 2", len(env.Data.Items))
	}

	// 仅删除勾选的已购条目，未勾选条目保留。
	n, err := g.DB().Model("cart_items").Ctx(context.Background()).Where("user_id", userID).Count()
	if err != nil {
		t.Fatalf("count cart items: %v", err)
	}
	if n != 1 {
		t.Fatalf("cart should keep only unselected item, got %d", n)
	}
	v, err := g.DB().Model("cart_items").Ctx(context.Background()).Fields("sku_id").Where("user_id", userID).Value()
	if err != nil {
		t.Fatalf("query remaining cart item: %v", err)
	}
	if v == nil || v.Int64() != sku3 {
		t.Fatalf("remaining cart item sku_id=%v want %d", v, sku3)
	}
}

// TestOrderEmptyCart 覆盖 9004：购物车下单无勾选项。
func TestOrderEmptyCart(t *testing.T) {
	base := setupOrderServer(t)

	isoInsertUser(t, "buyer3", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer3", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "cart", "address_id": addressID, "idempotency_key": "key-empty",
	})
	if env.Status != 400 || env.Code != 9004 {
		t.Fatalf("empty cart: status=%d code=%d want 400/9004", env.Status, env.Code)
	}
}

// TestOrderServerPricingSnapshot 覆盖 AC-002/INV-004：下单后改价/改地址不影响快照。
func TestOrderServerPricingSnapshot(t *testing.T) {
	base := setupOrderServer(t)
	skuID, _ := setupSellable(t, "SKU-SNAP", 3000, 10)

	isoInsertUser(t, "buyer4", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer4", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-snap",
		"sku_id": skuID, "quantity": 1,
	})
	if env.Status != 200 || env.Code != 0 {
		t.Fatalf("create: status=%d code=%d", env.Status, env.Code)
	}

	// 改 SKU 价格与地址后，快照不变。
	if _, err := g.DB().Model("skus").Ctx(context.Background()).Where("id", skuID).Data(g.Map{"price": 9999}).Update(); err != nil {
		t.Fatalf("update sku price: %v", err)
	}
	if _, err := g.DB().Model("addresses").Ctx(context.Background()).Where("id", addressID).Data(g.Map{"recipient_name": "李四"}).Update(); err != nil {
		t.Fatalf("update address: %v", err)
	}

	detail := orderCall(t, base, "GET", fmt.Sprintf("/orders/%d", env.Data.Id), token, nil)
	if detail.Status != 200 || detail.Code != 0 {
		t.Fatalf("detail: status=%d code=%d", detail.Status, detail.Code)
	}
	if detail.Data.TotalAmount != 3000 {
		t.Fatalf("snapshot total_amount=%d want 3000", detail.Data.TotalAmount)
	}
	if len(detail.Data.Items) != 1 || detail.Data.Items[0].Price != 3000 {
		t.Fatalf("snapshot item price changed: %+v", detail.Data.Items)
	}
	if detail.Data.RecipientName != "张三" {
		t.Fatalf("snapshot recipient changed: %q", detail.Data.RecipientName)
	}
}

// TestOrderIdempotency 覆盖 AC-004/INV-002：同键同内容只建一单、库存只扣一次；同键不同内容 9006。
func TestOrderIdempotency(t *testing.T) {
	base := setupOrderServer(t)
	skuID, _ := setupSellable(t, "SKU-IDEM", 2000, 10)

	isoInsertUser(t, "buyer5", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer5", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	body := map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-idem",
		"sku_id": skuID, "quantity": 2,
	}
	first := orderCall(t, base, "POST", "/orders", token, body)
	if first.Status != 200 || first.Code != 0 {
		t.Fatalf("first create: status=%d code=%d", first.Status, first.Code)
	}
	second := orderCall(t, base, "POST", "/orders", token, body)
	if second.Status != 200 || second.Code != 0 {
		t.Fatalf("second create (idempotent): status=%d code=%d", second.Status, second.Code)
	}
	if second.Data.Id != first.Data.Id {
		t.Fatalf("idempotent create should return same order, got %d vs %d", second.Data.Id, first.Data.Id)
	}
	if n := orderCount(t, userID); n != 1 {
		t.Fatalf("should create only one order, got %d", n)
	}
	if got := orderStock(t, skuID); got != 8 {
		t.Fatalf("stock should be deducted once, got %d want 8", got)
	}

	// 同键不同内容（数量不同）→ 9006。
	conflict := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-idem",
		"sku_id": skuID, "quantity": 3,
	})
	if conflict.Status != 409 || conflict.Code != 9006 {
		t.Fatalf("idempotency conflict: status=%d code=%d want 409/9006", conflict.Status, conflict.Code)
	}
	if got := orderStock(t, skuID); got != 8 {
		t.Fatalf("conflict must not deduct stock, got %d want 8", got)
	}
}

// TestOrderInsufficientStockRollback 覆盖 AC-005/INV-003：库存不足整体回滚，无半成品。
func TestOrderInsufficientStockRollback(t *testing.T) {
	base := setupOrderServer(t)
	skuID, _ := setupSellable(t, "SKU-OUT", 1000, 2)

	isoInsertUser(t, "buyer6", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer6", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-out",
		"sku_id": skuID, "quantity": 3,
	})
	if env.Status != 409 || env.Code != 6001 {
		t.Fatalf("insufficient stock: status=%d code=%d want 409/6001", env.Status, env.Code)
	}
	if n := orderCount(t, userID); n != 0 {
		t.Fatalf("no order should be created, got %d", n)
	}
	// 无半成品订单项、库存不变。
	itemN, err := g.DB().Model("order_items").Ctx(context.Background()).Count()
	if err != nil {
		t.Fatalf("count order_items: %v", err)
	}
	if itemN != 0 {
		t.Fatalf("no order_items should remain, got %d", itemN)
	}
	if got := orderStock(t, skuID); got != 2 {
		t.Fatalf("stock unchanged, got %d want 2", got)
	}
}

// TestOrderStateMachine 覆盖 AC-006/AC-007/INV-005：合法迁移推进、非法迁移 9002 且状态不变。
func TestOrderStateMachine(t *testing.T) {
	base := setupOrderServer(t)
	skuID, _ := setupSellable(t, "SKU-SM", 1000, 10)

	isoInsertUser(t, "buyer7", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer7", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-sm",
		"sku_id": skuID, "quantity": 1,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	id := created.Data.Id

	// 待支付 → 已支付。
	paid := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", id), token, nil)
	if paid.Status != 200 || paid.Code != 0 || paid.Data.Status != v1.StatusPaid {
		t.Fatalf("pay: status=%d code=%d order_status=%s", paid.Status, paid.Code, paid.Data.Status)
	}
	// 待支付订单取消（非法：已支付）→ 9002。
	cancelOnPaid := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/cancel", id), token, nil)
	if cancelOnPaid.Status != 409 || cancelOnPaid.Code != 9002 {
		t.Fatalf("cancel on paid: status=%d code=%d want 409/9002", cancelOnPaid.Status, cancelOnPaid.Code)
	}

	// 管理员发货：已支付 → 已发货。
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	shipped := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/ship", id), adminToken, map[string]any{})
	if shipped.Status != 200 || shipped.Code != 0 || shipped.Data.Status != v1.StatusShipped {
		t.Fatalf("ship: status=%d code=%d order_status=%s", shipped.Status, shipped.Code, shipped.Data.Status)
	}

	// 确认收货：已发货 → 已完成（40→50 原子）。
	received := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/receive", id), token, nil)
	if received.Status != 200 || received.Code != 0 || received.Data.Status != v1.StatusCompleted {
		t.Fatalf("receive: status=%d code=%d order_status=%s", received.Status, received.Code, received.Data.Status)
	}

	// 已完成 → 再收货（非法）→ 9002。
	receiveAgain := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/receive", id), token, nil)
	if receiveAgain.Status != 409 || receiveAgain.Code != 9002 {
		t.Fatalf("receive again: status=%d code=%d want 409/9002", receiveAgain.Status, receiveAgain.Code)
	}
}

// TestOrderCancelRestoresInventoryOnce 覆盖 AC-008/AC-015/INV-007：取消恢复库存，重复取消只补偿一次。
func TestOrderCancelRestoresInventoryOnce(t *testing.T) {
	base := setupOrderServer(t)
	skuID, _ := setupSellable(t, "SKU-CANCEL", 1000, 5)

	isoInsertUser(t, "buyer8", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer8", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-cancel",
		"sku_id": skuID, "quantity": 2,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	if got := orderStock(t, skuID); got != 3 {
		t.Fatalf("after create stock=%d want 3", got)
	}

	// 并发取消：只应有一个成功，库存只恢复一次。
	const n = 8
	var wg sync.WaitGroup
	success := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/cancel", created.Data.Id), token, nil)
			success <- (res.Status == 200 && res.Code == 0)
		}()
	}
	wg.Wait()
	close(success)
	okCount := 0
	for ok := range success {
		if ok {
			okCount++
		}
	}
	if okCount != 1 {
		t.Fatalf("exactly one cancel should succeed, got %d", okCount)
	}
	if got := orderStock(t, skuID); got != 5 {
		t.Fatalf("inventory should be restored exactly once, got %d want 5", got)
	}
	if st := orderStatus(t, created.Data.Id); st != 60 {
		t.Fatalf("order status=%d want 60", st)
	}
}

// TestOrderRefundRestoresInventoryOnce 覆盖 AC-008/AC-015/INV-007：退款恢复库存，并发退款只补偿一次。
func TestOrderRefundRestoresInventoryOnce(t *testing.T) {
	base := setupOrderServer(t)
	skuID, _ := setupSellable(t, "SKU-REFUND", 1000, 5)

	isoInsertUser(t, "buyer13", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer13", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-refund",
		"sku_id": skuID, "quantity": 2,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	if got := orderStock(t, skuID); got != 3 {
		t.Fatalf("after create stock=%d want 3", got)
	}

	// 支付到已支付，才能退款。
	if p := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", created.Data.Id), token, nil); p.Status != 200 || p.Code != 0 {
		t.Fatalf("pay: status=%d code=%d", p.Status, p.Code)
	}
	if st := orderStatus(t, created.Data.Id); st != 20 {
		t.Fatalf("after pay status=%d want 20", st)
	}

	// 并发退款：只应有一个成功完成 paid → refunded，库存只恢复一次，其余请求不得再次补偿。
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	const n = 8
	var wg sync.WaitGroup
	success := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/refund", created.Data.Id), adminToken, map[string]any{})
			success <- (res.Status == 200 && res.Code == 0)
		}()
	}
	wg.Wait()
	close(success)
	okCount := 0
	for ok := range success {
		if ok {
			okCount++
		}
	}
	if okCount != 1 {
		t.Fatalf("exactly one refund should succeed, got %d", okCount)
	}
	if got := orderStock(t, skuID); got != 5 {
		t.Fatalf("inventory should be restored exactly once, got %d want 5", got)
	}
	if st := orderStatus(t, created.Data.Id); st != 70 {
		t.Fatalf("order status=%d want 70", st)
	}
}

// TestOrderPayIdempotent 覆盖 AC-010/INV-010：重复支付不重复改状态。
func TestOrderPayIdempotent(t *testing.T) {
	base := setupOrderServer(t)
	skuID, _ := setupSellable(t, "SKU-PAY", 1000, 10)

	isoInsertUser(t, "buyer9", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer9", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-pay",
		"sku_id": skuID, "quantity": 1,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	id := created.Data.Id

	paid1 := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", id), token, nil)
	if paid1.Status != 200 || paid1.Code != 0 || paid1.Data.Status != v1.StatusPaid {
		t.Fatalf("pay1: status=%d code=%d", paid1.Status, paid1.Code)
	}
	paid2 := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", id), token, nil)
	if paid2.Status != 200 || paid2.Code != 0 || paid2.Data.Status != v1.StatusPaid {
		t.Fatalf("pay2 (idempotent): status=%d code=%d", paid2.Status, paid2.Code)
	}
	if paid2.Data.PaidAt == nil {
		t.Fatalf("paid_at should be set")
	}
}

// TestOrderUserIsolation 覆盖 AC-011/AC-013/INV-001：用户只能操作自己的订单，越权 404 且无写入。
func TestOrderUserIsolation(t *testing.T) {
	base := setupOrderServer(t)
	skuID, _ := setupSellable(t, "SKU-ISO", 1000, 10)

	isoInsertUser(t, "owner1", "ownerpass123")
	ownerToken, _ := isoFrontendLogin(t, base, "owner1", "ownerpass123")
	ownerClaims := isoClaims(t, ownerToken)
	ownerID, _ := parseUserID(t, ownerClaims.Subject)
	addressID := orderInsertAddress(t, ownerID)

	created := orderCall(t, base, "POST", "/orders", ownerToken, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-iso",
		"sku_id": skuID, "quantity": 1,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	id := created.Data.Id

	// 用户 B 查看/取消用户 A 的订单 → 404/9001，且无写入。
	isoInsertUser(t, "intruder", "intruderpass123")
	intruderToken, _ := isoFrontendLogin(t, base, "intruder", "intruderpass123")

	view := orderCall(t, base, "GET", fmt.Sprintf("/orders/%d", id), intruderToken, nil)
	if view.Status != 404 || view.Code != 9001 {
		t.Fatalf("intruder view: status=%d code=%d want 404/9001", view.Status, view.Code)
	}
	cancel := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/cancel", id), intruderToken, nil)
	if cancel.Status != 404 || cancel.Code != 9001 {
		t.Fatalf("intruder cancel: status=%d code=%d want 404/9001", cancel.Status, cancel.Code)
	}
	// 无副作用：订单仍待支付、库存未恢复。
	if st := orderStatus(t, id); st != 10 {
		t.Fatalf("intruder must not change order, status=%d want 10", st)
	}
	if got := orderStock(t, skuID); got != 9 {
		t.Fatalf("intruder must not restore stock, got %d want 9", got)
	}
}

// TestOrderAdminShipRefundPermission 覆盖 AC-012/AC-013/INV-008：发货/退款权限与无副作用。
func TestOrderAdminShipRefundPermission(t *testing.T) {
	base := setupOrderServer(t)
	skuID, _ := setupSellable(t, "SKU-ADMIN", 1000, 10)

	isoInsertUser(t, "buyer10", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer10", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-admin",
		"sku_id": skuID, "quantity": 2,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	id := created.Data.Id
	// 先支付到已支付，才能发货/退款。
	if p := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", id), token, nil); p.Status != 200 {
		t.Fatalf("pay: status=%d", p.Status)
	}
	if got := orderStock(t, skuID); got != 8 {
		t.Fatalf("after create stock=%d want 8", got)
	}

	// 无权限普通管理员发货 → 403，无写入。
	isoInsertAdmin(t, "plainadmin", "plainadmin123")
	plainToken, _ := isoAdminLogin(t, base, "plainadmin", "plainadmin123")
	shipForbidden := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/ship", id), plainToken, map[string]any{})
	if shipForbidden.Status != 403 || shipForbidden.Code != 1003 {
		t.Fatalf("plain admin ship: status=%d code=%d want 403/1003", shipForbidden.Status, shipForbidden.Code)
	}
	if st := orderStatus(t, id); st != 20 {
		t.Fatalf("forbidden ship must not change order, status=%d want 20", st)
	}

	// 普通用户 token 发货 → 403（AdminAuth 拒绝 type=user）。
	userShip := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/ship", id), token, map[string]any{})
	if userShip.Status != 403 {
		t.Fatalf("user token ship: status=%d want 403", userShip.Status)
	}

	// 超管退款 → 成功，恢复库存。
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	refund := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/refund", id), adminToken, map[string]any{})
	if refund.Status != 200 || refund.Code != 0 || refund.Data.Status != v1.StatusRefunded {
		t.Fatalf("super refund: status=%d code=%d order_status=%s", refund.Status, refund.Code, refund.Data.Status)
	}
	if got := orderStock(t, skuID); got != 10 {
		t.Fatalf("refund must restore stock, got %d want 10", got)
	}

	// 已退款后再发货（非法：refunded 不是 paid）→ 9002。
	shipAfterRefund := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/ship", id), adminToken, map[string]any{})
	if shipAfterRefund.Status != 409 || shipAfterRefund.Code != 9002 {
		t.Fatalf("ship after refund: status=%d code=%d want 409/9002", shipAfterRefund.Status, shipAfterRefund.Code)
	}
}

// TestOrderConcurrentNoOversell 覆盖 AC-014/INV-006：并发下单不超卖。
func TestOrderConcurrentNoOversell(t *testing.T) {
	base := setupOrderServer(t)
	skuID, _ := setupSellable(t, "SKU-RACE", 1000, 5)

	isoInsertUser(t, "buyer11", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer11", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	const n = 20
	var wg sync.WaitGroup
	results := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := orderCall(t, base, "POST", "/orders", token, map[string]any{
				"source": "direct", "address_id": addressID,
				"idempotency_key": fmt.Sprintf("key-race-%d", i),
				"sku_id":          skuID, "quantity": 1,
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
	if success != 5 {
		t.Fatalf("successful orders=%d want 5", success)
	}
	if got := orderStock(t, skuID); got != 0 {
		t.Fatalf("stock should be 0 after selling 5, got %d", got)
	}
	if n := orderCount(t, userID); n != 5 {
		t.Fatalf("order count=%d want 5", n)
	}
}

// TestOrderTimeoutAutoCancel 覆盖 AC-009：超时未支付自动取消（懒取消 + 扫描器）。
func TestOrderTimeoutAutoCancel(t *testing.T) {
	base := setupOrderServer(t)
	skuID, _ := setupSellable(t, "SKU-TIMEOUT", 1000, 10)

	isoInsertUser(t, "buyer12", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyer12", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	// 懒取消：过期后详情查询触发取消。
	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-timeout-1",
		"sku_id": skuID, "quantity": 2,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	orderExpire(t, created.Data.Id)
	detail := orderCall(t, base, "GET", fmt.Sprintf("/orders/%d", created.Data.Id), token, nil)
	if detail.Status != 200 || detail.Code != 0 || detail.Data.Status != v1.StatusCancelled {
		t.Fatalf("lazy cancel: status=%d code=%d order_status=%s", detail.Status, detail.Code, detail.Data.Status)
	}
	if detail.Data.CancelReason == nil || *detail.Data.CancelReason != v1.CancelReasonTimeout {
		t.Fatalf("lazy cancel reason should be timeout, got %v", detail.Data.CancelReason)
	}
	if got := orderStock(t, skuID); got != 10 {
		t.Fatalf("lazy cancel must restore stock, got %d want 10", got)
	}

	// 扫描器：直接调用 CancelExpired 取消过期订单。
	created2 := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-timeout-2",
		"sku_id": skuID, "quantity": 3,
	})
	if created2.Status != 200 || created2.Code != 0 {
		t.Fatalf("create2: status=%d code=%d", created2.Status, created2.Code)
	}
	orderExpire(t, created2.Data.Id)
	cancelled, err := service.Order().CancelExpired(context.Background(), 100)
	if err != nil {
		t.Fatalf("cancel expired: %v", err)
	}
	if cancelled != 1 {
		t.Fatalf("cancel expired count=%d want 1", cancelled)
	}
	if st := orderStatus(t, created2.Data.Id); st != 60 {
		t.Fatalf("scanner cancelled order status=%d want 60", st)
	}
	if got := orderStock(t, skuID); got != 10 {
		t.Fatalf("scanner must restore stock, got %d want 10", got)
	}
}
