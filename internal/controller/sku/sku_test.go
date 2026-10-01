package sku_test

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
	"github.com/gogf/gf/v2/net/gclient"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"

	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
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

type skuJSON struct {
	Id        int64  `json:"id"`
	ProductId int64  `json:"product_id"`
	Name      string `json:"name"`
	Price     int64  `json:"price"`
	Status    string `json:"status"`
}

type productDetailJSON struct {
	Id     int64     `json:"id"`
	Status string    `json:"status"`
	Skus   []skuJSON `json:"skus"`
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

// setupSkuServer 迁移 + Bootstrap + 清空 RBAC/分类/商品/SKU 表，挂载真实路由，返回 base 与超级管理员 token。
func setupSkuServer(t *testing.T) (base, token string) {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)
	t.Setenv("REDIS_DEFAULT_DB", "3")

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
	// 先清 SKU（FK → products），再清商品（FK → categories），最后清分类。
	for _, table := range []string{"skus", "product_images", "products", "categories"} {
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
		// 清理本测试产生的 SKU/商品数据，避免跨测试外键污染（顺序：skus → product_images → products）。
		for _, table := range []string{"skus", "product_images", "products"} {
			_, _ = g.DB().Exec(context.Background(), "DELETE FROM "+table)
		}
	})

	time.Sleep(100 * time.Millisecond)

	base = fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
	token = loginAdmin(t, base, testSuperUsername, testAdminPassword)
	return base, token
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
	if d.AccessToken == "" {
		t.Fatalf("login %s: empty access_token", username)
	}
	return d.AccessToken
}

func createCategory(t *testing.T, base, token string, parentID int64, name string) int64 {
	t.Helper()
	res := doRequest(t, base, "POST", "/categories", map[string]any{"parent_id": parentID, "name": name}, authHeader(token))
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

func createSku(t *testing.T, base, token string, body map[string]any) apiResult {
	t.Helper()
	return doRequest(t, base, "POST", "/admin/skus", body, authHeader(token))
}

func createAdmin(t *testing.T, base, token, username, password string) int64 {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/admins", map[string]any{"username": username, "password": password}, authHeader(token))
	assertOK(t, res, "create admin "+username)
	var d struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal create admin res: %v", err)
	}
	return d.Id
}

func createRole(t *testing.T, base, token, name string) int64 {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/roles", map[string]any{"name": name}, authHeader(token))
	assertOK(t, res, "create role "+name)
	var d struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal create role res: %v", err)
	}
	return d.Id
}

func permissionID(t *testing.T, code string) int64 {
	t.Helper()
	v, err := g.DB().Model("permissions").Ctx(context.Background()).Where("code", code).Value("id")
	if err != nil {
		t.Fatalf("query permission %s: %v", code, err)
	}
	if v == nil || v.IsEmpty() {
		t.Fatalf("permission %s not seeded", code)
	}
	return v.Int64()
}

func getSkuID(t *testing.T, res apiResult) int64 {
	t.Helper()
	var d skuJSON
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal sku res: %v", err)
	}
	if d.Id <= 0 {
		t.Fatalf("expected positive sku id, got %d", d.Id)
	}
	return d.Id
}

func dbSkuCount(t *testing.T) int {
	t.Helper()
	n, err := g.DB().Model("skus").Ctx(context.Background()).Count()
	if err != nil {
		t.Fatalf("count skus: %v", err)
	}
	return n
}

// dbSku 按 id 查询 SKU 的 DB 原始行（含 status 的 TINYINT 值），用于核对持久化与状态。
func dbSku(t *testing.T, id int64) (productID int64, name string, price int64, status int) {
	t.Helper()
	row, err := g.DB().Model("skus").Ctx(context.Background()).Where("id", id).One()
	if err != nil {
		t.Fatalf("query sku %d: %v", id, err)
	}
	if row == nil || row.IsEmpty() {
		t.Fatalf("sku %d not found in db", id)
	}
	return row["product_id"].Int64(), row["name"].String(), row["price"].Int64(), row["status"].Int()
}

func getFrontendDetail(t *testing.T, base string, id int64) productDetailJSON {
	t.Helper()
	res := doRequest(t, base, "GET", fmt.Sprintf("/products/%d", id), nil, nil)
	assertOK(t, res, "frontend detail")
	return unmarshalDetail(t, res)
}

func getAdminDetail(t *testing.T, base, token string, id int64) productDetailJSON {
	t.Helper()
	res := doRequest(t, base, "GET", fmt.Sprintf("/admin/products/%d", id), nil, authHeader(token))
	assertOK(t, res, "admin detail")
	return unmarshalDetail(t, res)
}

func unmarshalDetail(t *testing.T, res apiResult) productDetailJSON {
	t.Helper()
	var d productDetailJSON
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal detail res: %v", err)
	}
	return d
}

// TestSkuCreateAndValidation 覆盖 AC-001/002/004/010、INV-001/002/004：
// 创建合法 SKU（默认 enabled、价格整数分、product_id 归属正确）；非法价格/商品不存在/空名称/非法状态均拒绝且无写入。
func TestSkuCreateAndValidation(t *testing.T) {
	base, token := setupSkuServer(t)

	leafID := createCategory(t, base, token, 0, "手机")
	productID := createProduct(t, base, token, map[string]any{
		"name": "iPhone 15", "category_id": leafID, "price": 500000,
	})

	// AC-001/AC-010：合法创建，默认 status=enabled，price 整数分。
	res := createSku(t, base, token, map[string]any{
		"product_id": productID, "name": "128GB 黑色", "price": 499900,
	})
	assertOK(t, res, "create sku")
	id := getSkuID(t, res)
	pid, name, price, status := dbSku(t, id)
	if pid != productID || name != "128GB 黑色" || price != 499900 || status != 1 {
		t.Fatalf("unexpected persisted sku: product_id=%d name=%q price=%d status=%d", pid, name, price, status)
	}

	// AC-010：显式 status=disabled 创建成功。
	res = createSku(t, base, token, map[string]any{
		"product_id": productID, "name": "256GB 蓝色", "price": 549900, "status": "disabled",
	})
	assertOK(t, res, "create disabled sku")
	if _, _, _, st := dbSku(t, getSkuID(t, res)); st != 0 {
		t.Fatalf("expected status=disabled(0), got %d", st)
	}

	// AC-002：非法价格（负/非整数/超上限）→ 400/5002 且无写入。
	before := dbSkuCount(t)
	for _, p := range []any{-1, 12.5, 100000000} {
		r := createSku(t, base, token, map[string]any{
			"product_id": productID, "name": "非法价格", "price": p,
		})
		if r.Status != 400 || r.Code != 5002 {
			t.Fatalf("invalid price %v: status=%d code=%d", p, r.Status, r.Code)
		}
	}
	if after := dbSkuCount(t); after != before {
		t.Fatalf("no write expected on invalid price, skus %d -> %d", before, after)
	}

	// AC-004：product_id 不存在 → 404/4001 且无写入。
	r := createSku(t, base, token, map[string]any{
		"product_id": 999999, "name": "孤儿", "price": 100,
	})
	if r.Status != 404 || r.Code != 4001 {
		t.Fatalf("missing product: status=%d code=%d", r.Status, r.Code)
	}
	if after := dbSkuCount(t); after != before {
		t.Fatalf("no write expected on missing product, skus %d -> %d", before, after)
	}

	// 空名称 → 400/1001。
	r = createSku(t, base, token, map[string]any{
		"product_id": productID, "name": "  ", "price": 100,
	})
	if r.Status != 400 || r.Code != 1001 {
		t.Fatalf("empty name: status=%d code=%d", r.Status, r.Code)
	}

	// AC-010：非法状态 → 400/5003 且无写入。
	r = createSku(t, base, token, map[string]any{
		"product_id": productID, "name": "非法状态", "price": 100, "status": "bogus",
	})
	if r.Status != 400 || r.Code != 5003 {
		t.Fatalf("invalid status: status=%d code=%d", r.Status, r.Code)
	}
	if after := dbSkuCount(t); after != before {
		t.Fatalf("no write expected on invalid status, skus %d -> %d", before, after)
	}
}

// TestSkuUpdate 覆盖 AC-005、INV-002/004：更新 name/price/status 生效；非法价格/状态拒绝且原值不变；更新不存在 404/5001。
func TestSkuUpdate(t *testing.T) {
	base, token := setupSkuServer(t)

	leafID := createCategory(t, base, token, 0, "手机")
	productID := createProduct(t, base, token, map[string]any{
		"name": "iPhone 15", "category_id": leafID, "price": 500000,
	})
	id := getSkuID(t, createSku(t, base, token, map[string]any{
		"product_id": productID, "name": "128GB", "price": 499900,
	}))

	// 合法更新 name/price/status。
	res := doRequest(t, base, "PUT", fmt.Sprintf("/admin/skus/%d", id), map[string]any{
		"name": "128GB 新", "price": 489900, "status": "disabled",
	}, authHeader(token))
	assertOK(t, res, "update sku")
	pid, name, price, status := dbSku(t, id)
	if pid != productID || name != "128GB 新" || price != 489900 || status != 0 {
		t.Fatalf("unexpected updated sku: product_id=%d name=%q price=%d status=%d", pid, name, price, status)
	}

	// 非法价格拒绝且原值不变。
	for _, p := range []any{-1, 12.5, 100000000} {
		r := doRequest(t, base, "PUT", fmt.Sprintf("/admin/skus/%d", id), map[string]any{"price": p}, authHeader(token))
		if r.Status != 400 || r.Code != 5002 {
			t.Fatalf("update invalid price %v: status=%d code=%d", p, r.Status, r.Code)
		}
	}
	if _, _, price, _ := dbSku(t, id); price != 489900 {
		t.Fatalf("price should not change on invalid price, got %d", price)
	}

	// 非法状态拒绝且原值不变。
	r := doRequest(t, base, "PUT", fmt.Sprintf("/admin/skus/%d", id), map[string]any{"status": "bogus"}, authHeader(token))
	if r.Status != 400 || r.Code != 5003 {
		t.Fatalf("update invalid status: status=%d code=%d", r.Status, r.Code)
	}
	if _, _, _, status := dbSku(t, id); status != 0 {
		t.Fatalf("status should not change on invalid status, got %d", status)
	}

	// 更新不存在的 SKU → 404/5001。
	r = doRequest(t, base, "PUT", "/admin/skus/999999", map[string]any{"name": "x"}, authHeader(token))
	if r.Status != 404 || r.Code != 5001 {
		t.Fatalf("update missing sku: status=%d code=%d", r.Status, r.Code)
	}
}

// TestSkuDelete 覆盖 AC-006/007、INV-003：删除成功；重复删除 404；删除不影响 SPU 与同商品其他 SKU。
func TestSkuDelete(t *testing.T) {
	base, token := setupSkuServer(t)

	leafID := createCategory(t, base, token, 0, "手机")
	productID := createProduct(t, base, token, map[string]any{
		"name": "iPhone 15", "category_id": leafID, "price": 500000,
	})
	id1 := getSkuID(t, createSku(t, base, token, map[string]any{
		"product_id": productID, "name": "128GB", "price": 499900,
	}))
	id2 := getSkuID(t, createSku(t, base, token, map[string]any{
		"product_id": productID, "name": "256GB", "price": 549900,
	}))

	// 删除 id1 成功。
	res := doRequest(t, base, "DELETE", fmt.Sprintf("/admin/skus/%d", id1), nil, authHeader(token))
	assertOK(t, res, "delete sku")

	// 重复删除 → 404/5001。
	r := doRequest(t, base, "DELETE", fmt.Sprintf("/admin/skus/%d", id1), nil, authHeader(token))
	if r.Status != 404 || r.Code != 5001 {
		t.Fatalf("re-delete sku: status=%d code=%d", r.Status, r.Code)
	}

	// 删除不影响 SPU：商品仍存在，详情仍包含同商品其他 SKU。
	d := getAdminDetail(t, base, token, productID)
	if d.Id != productID {
		t.Fatalf("product should still exist, got %+v", d)
	}
	if len(d.Skus) != 1 || d.Skus[0].Id != id2 {
		t.Fatalf("remaining sku should still be id2=%d, got %+v", id2, d.Skus)
	}

	// INV-006：删除后自增不回退——新 SKU 获得更大的 id，不重用被删除的 id。
	id3 := getSkuID(t, createSku(t, base, token, map[string]any{
		"product_id": productID, "name": "512GB", "price": 599900,
	}))
	if id3 <= id1 {
		t.Fatalf("auto-increment should not reuse deleted id %d, got new id %d", id1, id3)
	}
}

// TestSkuNameUnique 覆盖 INV-008、AC-008 部分：同商品同名 → 409/5004 且无新行；不同商品同名可共存。
func TestSkuNameUnique(t *testing.T) {
	base, token := setupSkuServer(t)

	leafID := createCategory(t, base, token, 0, "手机")
	p1 := createProduct(t, base, token, map[string]any{"name": "iPhone", "category_id": leafID, "price": 500000})
	p2 := createProduct(t, base, token, map[string]any{"name": "小米", "category_id": leafID, "price": 300000})

	assertOK(t, createSku(t, base, token, map[string]any{
		"product_id": p1, "name": "标准版", "price": 499900,
	}), "create first sku")

	// 同商品同名 → 409/5004 且无新行。
	before := dbSkuCount(t)
	r := createSku(t, base, token, map[string]any{
		"product_id": p1, "name": "标准版", "price": 1,
	})
	if r.Status != 409 || r.Code != 5004 {
		t.Fatalf("duplicate name in same product: status=%d code=%d", r.Status, r.Code)
	}
	if after := dbSkuCount(t); after != before {
		t.Fatalf("no new row expected on duplicate name, skus %d -> %d", before, after)
	}

	// 不同商品同名 → 成功。
	res := createSku(t, base, token, map[string]any{
		"product_id": p2, "name": "标准版", "price": 299900,
	})
	assertOK(t, res, "create same name in different product")
}

// TestSkuConcurrentCreateSameName 覆盖 INV-008 并发兜底：同商品并发创建同名 SKU 最终至多一行，
// 唯一约束兜底「先查再写」竞争窗口，成功恰好一次、其余 409/5004。
func TestSkuConcurrentCreateSameName(t *testing.T) {
	base, token := setupSkuServer(t)

	leafID := createCategory(t, base, token, 0, "手机")
	productID := createProduct(t, base, token, map[string]any{
		"name": "iPhone", "category_id": leafID, "price": 500000,
	})

	const n = 10
	results := make(chan apiResult, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := request(base, "POST", "/admin/skus", map[string]any{
				"product_id": productID, "name": "同名", "price": 100,
			}, authHeader(token))
			if err != nil {
				results <- apiResult{Code: -1}
				return
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)

	var success, conflict int
	for res := range results {
		switch res.Code {
		case 0:
			success++
		case 5004:
			conflict++
		default:
			t.Fatalf("unexpected result code %d (status=%d)", res.Code, res.Status)
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly 1 success, got %d", success)
	}
	if conflict != n-1 {
		t.Fatalf("expected %d conflicts, got %d", n-1, conflict)
	}

	// 最终该商品下同名 SKU 至多一行。
	cnt, err := g.DB().Model("skus").Ctx(context.Background()).
		Where("product_id", productID).Where("name", "同名").Count()
	if err != nil {
		t.Fatalf("count same-name skus: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected exactly 1 same-name sku, got %d", cnt)
	}
}

// TestSkuOneToManyIsolation 覆盖 AC-008、INV-003：同一商品多 SKU，详情只返回本商品 SKU，商品间不串号。
func TestSkuOneToManyIsolation(t *testing.T) {
	base, token := setupSkuServer(t)

	leafID := createCategory(t, base, token, 0, "手机")
	p1 := createProduct(t, base, token, map[string]any{"name": "iPhone", "category_id": leafID, "price": 500000})
	p2 := createProduct(t, base, token, map[string]any{"name": "小米", "category_id": leafID, "price": 300000})

	s1 := getSkuID(t, createSku(t, base, token, map[string]any{"product_id": p1, "name": "128GB", "price": 499900}))
	s2 := getSkuID(t, createSku(t, base, token, map[string]any{"product_id": p1, "name": "256GB", "price": 549900}))
	getSkuID(t, createSku(t, base, token, map[string]any{"product_id": p2, "name": "12GB+256GB", "price": 299900}))

	// 后台详情只返回本商品 SKU，且按 id 升序。
	d := getAdminDetail(t, base, token, p1)
	if len(d.Skus) != 2 || d.Skus[0].Id != s1 || d.Skus[1].Id != s2 {
		t.Fatalf("product p1 should own exactly s1,s2 in id order, got %+v", d.Skus)
	}
	for _, sk := range d.Skus {
		if sk.ProductId != p1 {
			t.Fatalf("sku %d should belong to p1, got product_id=%d", sk.Id, sk.ProductId)
		}
	}

	// 商品 p2 详情只返回自己的 SKU。
	d2 := getAdminDetail(t, base, token, p2)
	if len(d2.Skus) != 1 {
		t.Fatalf("product p2 should own exactly 1 sku, got %d", len(d2.Skus))
	}
}

// TestSkuDetailVisibility 覆盖 AC-009、INV-007：前台详情仅 on_shelf 商品返回 enabled SKU；后台详情返回全部状态 SKU。
func TestSkuDetailVisibility(t *testing.T) {
	base, token := setupSkuServer(t)

	leafID := createCategory(t, base, token, 0, "手机")
	onShelf := createProduct(t, base, token, map[string]any{"name": "上架商品", "category_id": leafID, "price": 500000})
	draft := createProduct(t, base, token, map[string]any{"name": "草稿商品", "category_id": leafID, "price": 300000})

	createSku(t, base, token, map[string]any{"product_id": onShelf, "name": "启用SKU", "price": 100})
	createSku(t, base, token, map[string]any{"product_id": onShelf, "name": "停用SKU", "price": 200, "status": "disabled"})
	createSku(t, base, token, map[string]any{"product_id": draft, "name": "草稿SKU", "price": 300})

	// 上架 onShelf 商品。
	assertOK(t, doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", onShelf), nil, authHeader(token)), "on-shelf")

	// 前台详情：on_shelf 商品返回，且仅 enabled SKU。
	fd := getFrontendDetail(t, base, onShelf)
	if len(fd.Skus) != 1 || fd.Skus[0].Status != "enabled" {
		t.Fatalf("frontend detail should only contain enabled skus, got %+v", fd.Skus)
	}

	// 后台详情：返回全部状态 SKU（含 disabled）。
	ad := getAdminDetail(t, base, token, onShelf)
	if len(ad.Skus) != 2 {
		t.Fatalf("admin detail should contain all skus, got %d", len(ad.Skus))
	}

	// 前台详情：draft 商品 404，不暴露 SKU。
	r := doRequest(t, base, "GET", fmt.Sprintf("/products/%d", draft), nil, nil)
	if r.Status != 404 || r.Code != 4001 {
		t.Fatalf("frontend detail for draft: status=%d code=%d", r.Status, r.Code)
	}
}

// TestSkuAuthorization 覆盖 AC-011、INV-005：无 token 401、无权限管理员 403 且无写入、超管成功。
func TestSkuAuthorization(t *testing.T) {
	base, superToken := setupSkuServer(t)
	leafID := createCategory(t, base, superToken, 0, "手机")
	productID := createProduct(t, base, superToken, map[string]any{
		"name": "iPhone", "category_id": leafID, "price": 500000,
	})

	// 无 token 写 → 401/1002。
	res := createSku(t, base, "", map[string]any{"product_id": productID, "name": "x", "price": 1})
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("no token: status=%d code=%d", res.Status, res.Code)
	}

	// 前台用户 token 写 → 403（type=user 被 AdminAuth 拒绝）。
	userToken, err := auth.GenerateWithSecret([]byte(testJWTSecret), auth.TypeUser, 1, "deadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatalf("generate user token: %v", err)
	}
	res = createSku(t, base, userToken, map[string]any{"product_id": productID, "name": "x", "price": 1})
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("user token: status=%d code=%d", res.Status, res.Code)
	}

	// 无 sku 权限的普通管理员 → 403 且无写入。
	doRequest(t, base, "POST", "/admin/admins", map[string]any{"username": "noperm", "password": "nopermpass123"}, authHeader(superToken))
	noPermToken := loginAdmin(t, base, "noperm", "nopermpass123")
	before := dbSkuCount(t)
	res = createSku(t, base, noPermToken, map[string]any{"product_id": productID, "name": "越权", "price": 1})
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("no-perm admin: status=%d code=%d", res.Status, res.Code)
	}
	if after := dbSkuCount(t); after != before {
		t.Fatalf("no write expected on 403, skus %d -> %d", before, after)
	}

	// 超管成功。
	res = createSku(t, base, superToken, map[string]any{"product_id": productID, "name": "合法", "price": 1})
	assertOK(t, res, "super create sku")
}

// TestSkuCreateWithGrantedPermission 覆盖 AC-011：非超管管理员经角色授予 sku:create 后创建成功，
// 与「无权限 403」形成对照，锁定 seed 权限 code 与路由 require(...) 一致。
func TestSkuCreateWithGrantedPermission(t *testing.T) {
	base, superToken := setupSkuServer(t)
	leafID := createCategory(t, base, superToken, 0, "手机")
	productID := createProduct(t, base, superToken, map[string]any{
		"name": "iPhone", "category_id": leafID, "price": 500000,
	})

	roleID := createRole(t, base, superToken, "SKU管理员")
	permID := permissionID(t, "sku:create")
	assertOK(t, doRequest(t, base, "POST", fmt.Sprintf("/admin/roles/%d/permissions", roleID), map[string]any{"permission_id": permID}, authHeader(superToken)), "assign permission")
	adminID := createAdmin(t, base, superToken, "skumgr", "skumgrpass123")
	assertOK(t, doRequest(t, base, "POST", fmt.Sprintf("/admin/admins/%d/roles", adminID), map[string]any{"role_id": roleID}, authHeader(superToken)), "assign role")

	mgrToken := loginAdmin(t, base, "skumgr", "skumgrpass123")
	res := createSku(t, base, mgrToken, map[string]any{"product_id": productID, "name": "授权SKU", "price": 1})
	assertOK(t, res, "create sku with granted sku:create")
}
