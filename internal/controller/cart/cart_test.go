package cart_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/cmd"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

const (
	testJWTSecret     = "test-secret-0123456789-0123456789-0123456789" // >= 32 bytes
	testAdminPassword = "test-admin-password-123"
	testSuperUsername = "admin"
)

type apiResult struct {
	Status  int
	Code    int
	Message string
	Data    json.RawMessage
}

type cartItemJSON struct {
	Id                int64  `json:"id"`
	SkuId             int64  `json:"sku_id"`
	Quantity          int64  `json:"quantity"`
	Selected          bool   `json:"selected"`
	PriceSnapshot     int64  `json:"price_snapshot"`
	ProductId         *int64 `json:"product_id"`
	ProductName       string `json:"product_name"`
	ProductMainImage  string `json:"product_main_image"`
	SkuName           string `json:"sku_name"`
	CurrentPrice      *int64 `json:"current_price"`
	PriceChanged      bool   `json:"price_changed"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason"`
	Stock             *int64 `json:"stock"`
	Insufficient      bool   `json:"insufficient"`
}

func decode(r *gclient.Response) apiResult {
	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(r.ReadAll(), &env)
	return apiResult{Status: r.StatusCode, Code: env.Code, Message: env.Message, Data: env.Data}
}

func request(base, method, path string, body any, headers map[string]string) (apiResult, error) {
	c := g.Client()
	if len(headers) > 0 {
		c = c.Header(headers)
	}
	ctx := context.Background()
	var (
		r   *gclient.Response
		err error
	)
	switch method {
	case "GET":
		r, err = c.Get(ctx, base+path)
	case "POST":
		r, err = c.ContentJson().Post(ctx, base+path, body)
	case "PUT":
		r, err = c.ContentJson().Put(ctx, base+path, body)
	case "DELETE":
		r, err = c.Delete(ctx, base+path)
	default:
		return apiResult{}, fmt.Errorf("unsupported method %s", method)
	}
	if err != nil {
		return apiResult{}, err
	}
	defer r.Close()
	return decode(r), nil
}

func doRequest(t *testing.T, base, method, path string, body any, headers map[string]string) apiResult {
	t.Helper()
	res, err := request(base, method, path, body, headers)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return res
}

func assertOK(t *testing.T, res apiResult, op string) {
	t.Helper()
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("%s: status=%d code=%d msg=%q", op, res.Status, res.Code, res.Message)
	}
}

func authHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// setupCartServer 迁移 + Bootstrap + 清空相关表（含 cart_items/users），挂载前台+后台真实路由，
// 返回 base 与超级管理员 token。
func setupCartServer(t *testing.T) (base, adminToken string) {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)
	t.Setenv("REDIS_DEFAULT_DB", "4")

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for _, table := range []string{"admin_roles", "role_permissions", "roles", "permissions", "admins"} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("rebootstrap: %v", err)
	}
	if err := g.Redis().FlushDB(ctx); err != nil {
		t.Fatalf("clean redis: %v", err)
	}
	// 清理顺序遵循 FK：inventory_logs/inventories → skus → product_images/products → categories。
	for _, table := range []string{"cart_items", "users", "inventory_logs", "inventories", "skus", "product_images", "products", "categories"} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}

	s := g.Server(guid.S())
	s.SetAddr(":0")
	s.Group("/", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.Response)
		cmd.RegisterFrontendRoutes(group)
		cmd.RegisterAdminRoutes(group)
	})
	s.SetDumpRouterMap(false)
	s.Start()
	t.Cleanup(func() {
		_ = s.Shutdown()
		for _, table := range []string{"cart_items", "users", "inventory_logs", "inventories", "skus", "product_images", "products"} {
			_, _ = g.DB().Exec(context.Background(), "DELETE FROM "+table)
		}
	})

	time.Sleep(100 * time.Millisecond)
	base = fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
	adminToken = loginAdmin(t, base, testSuperUsername, testAdminPassword)
	return base, adminToken
}

func loginAdmin(t *testing.T, base, username, password string) string {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/login", map[string]any{"username": username, "password": password}, nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("login %s: status=%d code=%d msg=%q", username, res.Status, res.Code, res.Message)
	}
	var d struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal login res: %v", err)
	}
	return d.AccessToken
}

// registerLoginUser 注册并登录前台用户，返回 (token, userID)。
func registerLoginUser(t *testing.T, base, username, password string) (string, int64) {
	t.Helper()
	res := doRequest(t, base, "POST", "/register", map[string]any{"username": username, "password": password}, nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("register %s: status=%d code=%d", username, res.Status, res.Code)
	}
	var reg struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &reg); err != nil {
		t.Fatalf("unmarshal register res: %v", err)
	}
	login := doRequest(t, base, "POST", "/login", map[string]any{"username": username, "password": password}, nil)
	if login.Status != 200 || login.Code != 0 {
		t.Fatalf("login %s: status=%d code=%d", username, login.Status, login.Code)
	}
	var d struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(login.Data, &d); err != nil {
		t.Fatalf("unmarshal login res: %v", err)
	}
	return d.AccessToken, reg.Id
}

func createCategory(t *testing.T, base, token, name string) int64 {
	t.Helper()
	res := doRequest(t, base, "POST", "/categories", map[string]any{"parent_id": 0, "name": name}, authHeader(token))
	assertOK(t, res, "create category "+name)
	var d struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal create category res: %v", err)
	}
	return d.Id
}

func createProduct(t *testing.T, base, token string, body map[string]any) int64 {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/products", body, authHeader(token))
	assertOK(t, res, "create product")
	var d struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal create product res: %v", err)
	}
	return d.Id
}

func createSku(t *testing.T, base, token string, body map[string]any) int64 {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/skus", body, authHeader(token))
	assertOK(t, res, "create sku")
	var d struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal create sku res: %v", err)
	}
	return d.Id
}

func onShelf(t *testing.T, base, token string, productID int64) {
	t.Helper()
	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", productID), nil, authHeader(token))
	assertOK(t, res, "on-shelf product")
}

func offShelf(t *testing.T, base, token string, productID int64) {
	t.Helper()
	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/off-shelf", productID), nil, authHeader(token))
	assertOK(t, res, "off-shelf product")
}

func increaseStock(t *testing.T, base, token string, skuID, qty int64) {
	t.Helper()
	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/increase", skuID), map[string]any{"quantity": qty}, authHeader(token))
	assertOK(t, res, "increase stock")
}

// cartSeq 为每个用例生成唯一命名，避免同测试内分类/商品/SKU 撞唯一约束。
var cartSeq int64

// newAddableSku 创建「分类 → 上架商品 → enabled SKU」链路，返回 (skuID, productID)。
func newAddableSku(t *testing.T, base, token string) (int64, int64) {
	t.Helper()
	n := atomic.AddInt64(&cartSeq, 1)
	catID := createCategory(t, base, token, fmt.Sprintf("购物车分类%d", n))
	productID := createProduct(t, base, token, map[string]any{
		"name": fmt.Sprintf("购物车商品%d", n), "category_id": catID, "price": 100,
	})
	onShelf(t, base, token, productID)
	skuID := createSku(t, base, token, map[string]any{
		"product_id": productID, "name": fmt.Sprintf("购物车SKU%d", n), "price": 100,
	})
	return skuID, productID
}

func listCart(t *testing.T, base, token string) []cartItemJSON {
	t.Helper()
	res := doRequest(t, base, "GET", "/cart", nil, authHeader(token))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("list cart: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	var lr struct {
		Items []cartItemJSON `json:"items"`
	}
	if err := json.Unmarshal(res.Data, &lr); err != nil {
		t.Fatalf("unmarshal cart list: %v", err)
	}
	return lr.Items
}

func addCart(t *testing.T, base, token string, skuID int64, qty any) apiResult {
	t.Helper()
	return doRequest(t, base, "POST", "/cart/items", map[string]any{"sku_id": skuID, "quantity": qty}, authHeader(token))
}

func addCartItem(t *testing.T, res apiResult) cartItemJSON {
	t.Helper()
	var it cartItemJSON
	if err := json.Unmarshal(res.Data, &it); err != nil {
		t.Fatalf("unmarshal add res: %v", err)
	}
	return it
}

// dbCartRow 查询指定 (user_id, sku_id) 的 DB 行（用于核对持久化与唯一性）。
func dbCartRow(t *testing.T, userID, skuID int64) (quantity int64, exists bool) {
	t.Helper()
	row, err := g.DB().Model("cart_items").Ctx(context.Background()).
		Where("user_id", userID).Where("sku_id", skuID).One()
	if err != nil {
		t.Fatalf("query cart item: %v", err)
	}
	if row == nil || row.IsEmpty() {
		return 0, false
	}
	return row["quantity"].Int64(), true
}

func dbCartCount(t *testing.T, userID int64) int {
	t.Helper()
	n, err := g.DB().Model("cart_items").Ctx(context.Background()).Where("user_id", userID).Count()
	if err != nil {
		t.Fatalf("count cart items: %v", err)
	}
	return n
}

// TestCartListEmptyAndAdd 覆盖 AC-001/AC-002：空购物车返回空列表；加购后条目可见且字段正确。
func TestCartListEmptyAndAdd(t *testing.T) {
	base, adminToken := setupCartServer(t)
	userToken, _ := registerLoginUser(t, base, "alice", "password123")

	if items := listCart(t, base, userToken); len(items) != 0 {
		t.Fatalf("expected empty cart, got %d items", len(items))
	}

	skuID, productID := newAddableSku(t, base, adminToken)
	res := addCart(t, base, userToken, skuID, nil) // 缺省数量 1
	assertOK(t, res, "add cart")
	it := addCartItem(t, res)
	if it.SkuId != skuID || it.Quantity != 1 || !it.Selected {
		t.Fatalf("unexpected add item: %+v", it)
	}
	if it.PriceSnapshot != 100 || it.CurrentPrice == nil || *it.CurrentPrice != 100 {
		t.Fatalf("price mismatch: snapshot=%d current=%v", it.PriceSnapshot, it.CurrentPrice)
	}
	if !it.Available || it.UnavailableReason != "" {
		t.Fatalf("expected available, got available=%t reason=%q", it.Available, it.UnavailableReason)
	}
	if it.ProductId == nil || *it.ProductId != productID {
		t.Fatalf("expected product_id=%d, got %v", productID, it.ProductId)
	}
	// 无库存记录 → stock=0，且 1 > 0 → 库存不足。
	if it.Stock == nil || *it.Stock != 0 || !it.Insufficient {
		t.Fatalf("expected stock=0 insufficient=true, got stock=%v insufficient=%t", it.Stock, it.Insufficient)
	}

	items := listCart(t, base, userToken)
	if len(items) != 1 || items[0].SkuId != skuID {
		t.Fatalf("expected 1 item sku=%d, got %+v", skuID, items)
	}
}

// TestCartAddAccumulate 覆盖 AC-003、INV-002：重复添加同 SKU 累加数量且仅一条记录。
func TestCartAddAccumulate(t *testing.T) {
	base, adminToken := setupCartServer(t)
	userToken, userID := registerLoginUser(t, base, "alice", "password123")
	skuID, _ := newAddableSku(t, base, adminToken)

	assertOK(t, addCart(t, base, userToken, skuID, 3), "first add")
	assertOK(t, addCart(t, base, userToken, skuID, 5), "second add")

	qty, exists := dbCartRow(t, userID, skuID)
	if !exists || qty != 8 {
		t.Fatalf("expected single row quantity=8, got exists=%t quantity=%d", exists, qty)
	}
	if n := dbCartCount(t, userID); n != 1 {
		t.Fatalf("expected exactly 1 cart item, got %d", n)
	}
}

// TestCartConcurrentAdd 覆盖 INV-002 并发不变量：并发重复加购最终数量 = 各次累加之和，无丢失更新。
func TestCartConcurrentAdd(t *testing.T) {
	base, adminToken := setupCartServer(t)
	userToken, userID := registerLoginUser(t, base, "alice", "password123")
	skuID, _ := newAddableSku(t, base, adminToken)

	const (
		n    = 20
		each = 5
	)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = request(base, "POST", "/cart/items", map[string]any{"sku_id": skuID, "quantity": each}, authHeader(userToken))
		}()
	}
	wg.Wait()

	qty, exists := dbCartRow(t, userID, skuID)
	if !exists || qty != n*each {
		t.Fatalf("expected final quantity=%d, got exists=%t quantity=%d", n*each, exists, qty)
	}
	if c := dbCartCount(t, userID); c != 1 {
		t.Fatalf("expected exactly 1 cart item, got %d", c)
	}
}

// TestCartAddValidation 覆盖 AC-002、INV-004：SKU 不存在 5001、下架/禁用 7003，均无写入。
func TestCartAddValidation(t *testing.T) {
	base, adminToken := setupCartServer(t)
	userToken, userID := registerLoginUser(t, base, "alice", "password123")

	// SKU 不存在 → 5001。
	res := addCart(t, base, userToken, 999999, 1)
	if res.Status != 404 || res.Code != 5001 {
		t.Fatalf("missing sku: status=%d code=%d", res.Status, res.Code)
	}

	// 下架商品 → 7003。
	skuOff, productID := newAddableSku(t, base, adminToken)
	offShelf(t, base, adminToken, productID)
	res = addCart(t, base, userToken, skuOff, 1)
	if res.Status != 409 || res.Code != 7003 {
		t.Fatalf("off-shelf: status=%d code=%d", res.Status, res.Code)
	}

	// SKU 禁用 → 7003。
	skuDisabled, _ := newAddableSku(t, base, adminToken)
	upd := doRequest(t, base, "PUT", fmt.Sprintf("/admin/skus/%d", skuDisabled), map[string]any{"status": "disabled"}, authHeader(adminToken))
	assertOK(t, upd, "disable sku")
	res = addCart(t, base, userToken, skuDisabled, 1)
	if res.Status != 409 || res.Code != 7003 {
		t.Fatalf("disabled sku: status=%d code=%d", res.Status, res.Code)
	}

	if n := dbCartCount(t, userID); n != 0 {
		t.Fatalf("expected no cart writes on validation failure, got %d", n)
	}
}

// TestCartQuantityBoundary 覆盖 AC-004、INV-003：数量 0/负数/非整数/超上限/累加超限均 400 且原值不变。
func TestCartQuantityBoundary(t *testing.T) {
	base, adminToken := setupCartServer(t)
	userToken, userID := registerLoginUser(t, base, "alice", "password123")
	skuID, _ := newAddableSku(t, base, adminToken)

	// 加购非法数量。
	for _, q := range []any{0, -1, 1.5, 1000} {
		res := addCart(t, base, userToken, skuID, q)
		if res.Status != 400 || res.Code != 7002 {
			t.Fatalf("add quantity=%v: status=%d code=%d", q, res.Status, res.Code)
		}
	}
	if n := dbCartCount(t, userID); n != 0 {
		t.Fatalf("expected no write on invalid add quantity, got %d", n)
	}

	// 合法加购后改数量边界。
	assertOK(t, addCart(t, base, userToken, skuID, 5), "add")
	items := listCart(t, base, userToken)
	itemID := items[0].Id
	for _, q := range []any{0, -1, 1.5, 1000} {
		res := doRequest(t, base, "PUT", fmt.Sprintf("/cart/items/%d", itemID), map[string]any{"quantity": q}, authHeader(userToken))
		if res.Status != 400 || res.Code != 7002 {
			t.Fatalf("update quantity=%v: status=%d code=%d", q, res.Status, res.Code)
		}
	}
	if qty, _ := dbCartRow(t, userID, skuID); qty != 5 {
		t.Fatalf("quantity should remain 5 on invalid update, got %d", qty)
	}

	// 累加超上限：现有 5 + 996 = 1001 > 999 → 拒绝且不变。
	res := addCart(t, base, userToken, skuID, 996)
	if res.Status != 400 || res.Code != 7002 {
		t.Fatalf("accumulate over cap: status=%d code=%d", res.Status, res.Code)
	}
	if qty, _ := dbCartRow(t, userID, skuID); qty != 5 {
		t.Fatalf("quantity should remain 5 on over-cap accumulate, got %d", qty)
	}

	// 边界内累加：5 + 994 = 999 成功。
	assertOK(t, addCart(t, base, userToken, skuID, 994), "accumulate to cap")
	if qty, _ := dbCartRow(t, userID, skuID); qty != 999 {
		t.Fatalf("expected quantity=999, got %d", qty)
	}
}

// TestCartDelete 覆盖 AC-005：删除成功；删除不存在/他人条目 404。
func TestCartDelete(t *testing.T) {
	base, adminToken := setupCartServer(t)
	userToken, userID := registerLoginUser(t, base, "alice", "password123")
	skuID, _ := newAddableSku(t, base, adminToken)

	assertOK(t, addCart(t, base, userToken, skuID, 2), "add")
	items := listCart(t, base, userToken)
	itemID := items[0].Id

	res := doRequest(t, base, "DELETE", fmt.Sprintf("/cart/items/%d", itemID), nil, authHeader(userToken))
	assertOK(t, res, "delete")
	if n := dbCartCount(t, userID); n != 0 {
		t.Fatalf("expected 0 items after delete, got %d", n)
	}

	// 重复删除 → 404/7001。
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/cart/items/%d", itemID), nil, authHeader(userToken))
	if res.Status != 404 || res.Code != 7001 {
		t.Fatalf("re-delete: status=%d code=%d", res.Status, res.Code)
	}
}

// TestCartSelected 覆盖 AC-006：勾选状态可持久化并在列表中可见。
func TestCartSelected(t *testing.T) {
	base, adminToken := setupCartServer(t)
	userToken, _ := registerLoginUser(t, base, "alice", "password123")
	skuID, _ := newAddableSku(t, base, adminToken)

	assertOK(t, addCart(t, base, userToken, skuID, 1), "add")
	items := listCart(t, base, userToken)
	if !items[0].Selected {
		t.Fatal("expected selected=true by default")
	}

	// 取消勾选。
	res := doRequest(t, base, "PUT", fmt.Sprintf("/cart/items/%d/selected", items[0].Id), map[string]any{"selected": false}, authHeader(userToken))
	assertOK(t, res, "unselect")
	it := addCartItem(t, res)
	if it.Selected {
		t.Fatal("expected selected=false after unselect")
	}
	items = listCart(t, base, userToken)
	if items[0].Selected {
		t.Fatal("expected selected=false persisted in list")
	}

	// 重新勾选。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/cart/items/%d/selected", items[0].Id), map[string]any{"selected": true}, authHeader(userToken))
	assertOK(t, res, "select")
	if it := addCartItem(t, res); !it.Selected {
		t.Fatal("expected selected=true after select")
	}
}

// TestCartUnavailable 覆盖 AC-007、INV-005：下架/禁用后条目保留且标识不可购、原因正确。
func TestCartUnavailable(t *testing.T) {
	base, adminToken := setupCartServer(t)
	userToken, userID := registerLoginUser(t, base, "alice", "password123")

	// 下架。
	skuOff, productID := newAddableSku(t, base, adminToken)
	assertOK(t, addCart(t, base, userToken, skuOff, 3), "add")
	offShelf(t, base, adminToken, productID)
	items := listCart(t, base, userToken)
	if len(items) != 1 || items[0].Available || items[0].UnavailableReason != "off_shelf" {
		t.Fatalf("expected off_shelf unavailable, got %+v", items)
	}

	// 禁用。
	skuDisabled, _ := newAddableSku(t, base, adminToken)
	assertOK(t, addCart(t, base, userToken, skuDisabled, 3), "add")
	upd := doRequest(t, base, "PUT", fmt.Sprintf("/admin/skus/%d", skuDisabled), map[string]any{"status": "disabled"}, authHeader(adminToken))
	assertOK(t, upd, "disable sku")
	items = listCart(t, base, userToken)
	found := false
	for _, it := range items {
		if it.SkuId == skuDisabled {
			found = true
			if it.Available || it.UnavailableReason != "disabled" {
				t.Fatalf("expected disabled unavailable, got %+v", it)
			}
		}
	}
	if !found {
		t.Fatal("expected disabled sku item retained in list")
	}
	// 条目未被清理、数量未变。
	if n := dbCartCount(t, userID); n != 2 {
		t.Fatalf("expected 2 items retained, got %d", n)
	}
}

// TestCartPriceChanged 覆盖 AC-008、INV-006：改价后快照不变、current_price=新价、price_changed=true。
func TestCartPriceChanged(t *testing.T) {
	base, adminToken := setupCartServer(t)
	userToken, _ := registerLoginUser(t, base, "alice", "password123")
	skuID, _ := newAddableSku(t, base, adminToken)

	assertOK(t, addCart(t, base, userToken, skuID, 1), "add")
	upd := doRequest(t, base, "PUT", fmt.Sprintf("/admin/skus/%d", skuID), map[string]any{"price": 250}, authHeader(adminToken))
	assertOK(t, upd, "change price")

	items := listCart(t, base, userToken)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	it := items[0]
	if it.PriceSnapshot != 100 {
		t.Fatalf("snapshot should stay 100, got %d", it.PriceSnapshot)
	}
	if it.CurrentPrice == nil || *it.CurrentPrice != 250 {
		t.Fatalf("expected current_price=250, got %v", it.CurrentPrice)
	}
	if !it.PriceChanged {
		t.Fatal("expected price_changed=true")
	}
}

// TestCartInsufficient 覆盖 AC-009、INV-005：库存少于数量时标识库存不足，且不调整数量。
func TestCartInsufficient(t *testing.T) {
	base, adminToken := setupCartServer(t)
	userToken, _ := registerLoginUser(t, base, "alice", "password123")
	skuID, _ := newAddableSku(t, base, adminToken)

	increaseStock(t, base, adminToken, skuID, 10)
	assertOK(t, addCart(t, base, userToken, skuID, 5), "add")
	if items := listCart(t, base, userToken); len(items) == 1 && items[0].Insufficient {
		t.Fatalf("expected sufficient when stock>=quantity, got %+v", items[0])
	}

	// 改数量 15 > 库存 10 → 库存不足。
	items := listCart(t, base, userToken)
	res := doRequest(t, base, "PUT", fmt.Sprintf("/cart/items/%d", items[0].Id), map[string]any{"quantity": 15}, authHeader(userToken))
	assertOK(t, res, "update quantity")
	items = listCart(t, base, userToken)
	if !items[0].Insufficient || items[0].Quantity != 15 {
		t.Fatalf("expected insufficient=true quantity=15, got %+v", items[0])
	}
}

// TestCartUserIsolation 覆盖 AC-010、INV-001：用户 B 无法查看/修改/删除 A 的条目，统一 404 且无写入。
func TestCartUserIsolation(t *testing.T) {
	base, adminToken := setupCartServer(t)
	tokenA, userA := registerLoginUser(t, base, "alice", "password123")
	tokenB, userB := registerLoginUser(t, base, "bob", "password123")
	skuID, _ := newAddableSku(t, base, adminToken)

	assertOK(t, addCart(t, base, tokenA, skuID, 2), "add for A")
	items := listCart(t, base, tokenA)
	itemID := items[0].Id

	// B 看不到 A 的条目。
	if bItems := listCart(t, base, tokenB); len(bItems) != 0 {
		t.Fatalf("expected B sees empty cart, got %+v", bItems)
	}

	// B 改/勾选/删 A 的条目 → 404 且无写入。
	res := doRequest(t, base, "PUT", fmt.Sprintf("/cart/items/%d", itemID), map[string]any{"quantity": 9}, authHeader(tokenB))
	if res.Status != 404 || res.Code != 7001 {
		t.Fatalf("B update A item: status=%d code=%d", res.Status, res.Code)
	}
	res = doRequest(t, base, "PUT", fmt.Sprintf("/cart/items/%d/selected", itemID), map[string]any{"selected": false}, authHeader(tokenB))
	if res.Status != 404 || res.Code != 7001 {
		t.Fatalf("B select A item: status=%d code=%d", res.Status, res.Code)
	}
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/cart/items/%d", itemID), nil, authHeader(tokenB))
	if res.Status != 404 || res.Code != 7001 {
		t.Fatalf("B delete A item: status=%d code=%d", res.Status, res.Code)
	}

	// A 的条目数量、归属不变。
	if qty, _ := dbCartRow(t, userA, skuID); qty != 2 {
		t.Fatalf("A quantity should remain 2, got %d", qty)
	}
	if n := dbCartCount(t, userB); n != 0 {
		t.Fatalf("B should have no items, got %d", n)
	}
}

// TestCartRequiresAuth 覆盖 AC-011：无 token 访问购物车接口 401 且无数据。
func TestCartRequiresAuth(t *testing.T) {
	base, _ := setupCartServer(t)

	if res := doRequest(t, base, "GET", "/cart", nil, nil); res.Status != 401 || res.Code != 1002 {
		t.Fatalf("list no token: status=%d code=%d", res.Status, res.Code)
	}
	if res := doRequest(t, base, "POST", "/cart/items", map[string]any{"sku_id": 1}, nil); res.Status != 401 || res.Code != 1002 {
		t.Fatalf("add no token: status=%d code=%d", res.Status, res.Code)
	}
	if res := doRequest(t, base, "DELETE", "/cart/items/1", nil, nil); res.Status != 401 || res.Code != 1002 {
		t.Fatalf("delete no token: status=%d code=%d", res.Status, res.Code)
	}
}

// TestCartSkuDeleted 覆盖 INV-007：SKU 物理删除后条目保留为不可购，标识 sku_deleted、价格/库存为 null。
func TestCartSkuDeleted(t *testing.T) {
	base, adminToken := setupCartServer(t)
	userToken, userID := registerLoginUser(t, base, "alice", "password123")
	skuID, _ := newAddableSku(t, base, adminToken)

	assertOK(t, addCart(t, base, userToken, skuID, 2), "add")

	// 删除 SKU（无库存记录，删除成功且不被购物车引用阻塞）。
	res := doRequest(t, base, "DELETE", fmt.Sprintf("/admin/skus/%d", skuID), nil, authHeader(adminToken))
	assertOK(t, res, "delete sku")

	items := listCart(t, base, userToken)
	if len(items) != 1 {
		t.Fatalf("expected item retained after sku deleted, got %d", len(items))
	}
	it := items[0]
	if it.Available || it.UnavailableReason != "sku_deleted" {
		t.Fatalf("expected sku_deleted unavailable, got %+v", it)
	}
	if it.CurrentPrice != nil || it.Stock != nil || it.ProductId != nil {
		t.Fatalf("expected current_price/stock/product_id null after sku deleted, got %+v", it)
	}
	if n := dbCartCount(t, userID); n != 1 {
		t.Fatalf("expected item retained, got %d", n)
	}
}
