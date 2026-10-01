package product_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
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

type productJSON struct {
	Id         int64    `json:"id"`
	Name       string   `json:"name"`
	Brand      string   `json:"brand"`
	CategoryId int64    `json:"category_id"`
	Price      int64    `json:"price"`
	MainImage  string   `json:"main_image"`
	Detail     string   `json:"detail"`
	Status     string   `json:"status"`
	Images     []string `json:"images"`
}

type productListData struct {
	Items []*productJSON `json:"items"`
	Total int            `json:"total"`
	Page  int            `json:"page"`
	Size  int            `json:"size"`
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

// setupProductServer 迁移 + Bootstrap + 清空 RBAC/分类/商品表，挂载真实路由，返回 base 与超级管理员 token。
func setupProductServer(t *testing.T) (base, token string) {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)
	t.Setenv("REDIS_DEFAULT_DB", "2")

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
	// 商品表先于分类表清空（products 外键引用 categories）。
	for _, table := range []string{"product_images", "products", "categories"} {
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
		// 清理本测试产生的商品数据，避免跨测试外键污染（顺序：product_images → products）。
		for _, table := range []string{"product_images", "products"} {
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

func createCategory(t *testing.T, base, token string, parentID int64, name string, status *int) int64 {
	t.Helper()
	body := map[string]any{"parent_id": parentID, "name": name}
	if status != nil {
		body["status"] = *status
	}
	res := doRequest(t, base, "POST", "/categories", body, authHeader(token))
	assertOK(t, res, "create category "+name)
	var d struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal create category res: %v", err)
	}
	return d.Id
}

func createProduct(t *testing.T, base, token string, body map[string]any) apiResult {
	t.Helper()
	return doRequest(t, base, "POST", "/admin/products", body, authHeader(token))
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

// permissionID 返回 seed 标准权限 code 对应的 id（产品权限已由 Bootstrap seed，直接查库）。
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

func getProductID(t *testing.T, res apiResult) int64 {
	t.Helper()
	var d productJSON
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal product res: %v", err)
	}
	if d.Id <= 0 {
		t.Fatalf("expected positive product id, got %d", d.Id)
	}
	return d.Id
}

func getProductDetail(t *testing.T, base string, id int64) productJSON {
	t.Helper()
	res := doRequest(t, base, "GET", fmt.Sprintf("/products/%d", id), nil, nil)
	assertOK(t, res, "detail")
	var d productJSON
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal detail res: %v", err)
	}
	return d
}

func getAdminProductDetail(t *testing.T, base, token string, id int64) productJSON {
	t.Helper()
	res := doRequest(t, base, "GET", fmt.Sprintf("/admin/products/%d", id), nil, authHeader(token))
	assertOK(t, res, "admin detail")
	var d productJSON
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal admin detail res: %v", err)
	}
	return d
}

func dbProductStatus(t *testing.T, id int64) int {
	t.Helper()
	v, err := g.DB().Model("products").Ctx(context.Background()).Where("id", id).Value("status")
	if err != nil {
		t.Fatalf("query product status: %v", err)
	}
	return v.Int()
}

func dbProductCount(t *testing.T) int {
	t.Helper()
	n, err := g.DB().Model("products").Ctx(context.Background()).Count()
	if err != nil {
		t.Fatalf("count products: %v", err)
	}
	return n
}

func dbImageURLs(t *testing.T, productID int64) []string {
	t.Helper()
	var rows []struct {
		Url  string
		Sort int
	}
	if err := g.DB().Model("product_images").Ctx(context.Background()).
		Where("product_id", productID).Order("sort", "id").Scan(&rows); err != nil {
		t.Fatalf("query product images: %v", err)
	}
	urls := make([]string, 0, len(rows))
	for _, r := range rows {
		urls = append(urls, r.Url)
	}
	return urls
}

// TestProductCreateAndValidation 覆盖 AC-001/002/003/004/018：
// 创建合法商品（status=draft、价格整数分、图片持久化）；非法价格/分类/名称均拒绝且无写入。
func TestProductCreateAndValidation(t *testing.T) {
	base, token := setupProductServer(t)

	leafID := createCategory(t, base, token, 0, "手机", nil)
	parentID := createCategory(t, base, token, 0, "数码", nil)
	createCategory(t, base, token, parentID, "数码子类", nil) // 使 parentID 成为非叶子
	disabledID := createCategory(t, base, token, 0, "停用分类", intPtr(0))

	// AC-001/002：合法创建 → status=draft、price=1234 整数分、图片持久化。
	res := createProduct(t, base, token, map[string]any{
		"name":        "iPhone 15",
		"brand":       "Apple",
		"category_id": leafID,
		"price":       1234,
		"main_image":  "https://example.com/main.jpg",
		"detail":      "全新",
		"images":      []string{"https://example.com/1.jpg", "https://example.com/2.jpg"},
	})
	assertOK(t, res, "create product")
	id := getProductID(t, res)
	if dbProductStatus(t, id) != 0 {
		t.Fatalf("expected status=draft(0), got %d", dbProductStatus(t, id))
	}
	d := getAdminProductDetail(t, base, token, id)
	if d.Price != 1234 || d.Status != "draft" || d.MainImage != "https://example.com/main.jpg" {
		t.Fatalf("unexpected product detail: %+v", d)
	}
	// AC-018：图片按 sort 顺序持久化。
	if urls := dbImageURLs(t, id); len(urls) != 2 || urls[0] != "https://example.com/1.jpg" || urls[1] != "https://example.com/2.jpg" {
		t.Fatalf("unexpected images: %v", urls)
	}

	// AC-003：非法价格（负/非整数/超上限）均 400/4002 且无写入。
	before := dbProductCount(t)
	for _, p := range []any{-1, 12.5, 100000000, "abc"} {
		r := createProduct(t, base, token, map[string]any{
			"name": "非法价格商品", "category_id": leafID, "price": p,
		})
		if r.Status != 400 || r.Code != 4002 {
			t.Fatalf("invalid price %v: status=%d code=%d", p, r.Status, r.Code)
		}
	}
	if after := dbProductCount(t); after != before {
		t.Fatalf("no write expected on invalid price, products %d -> %d", before, after)
	}

	// AC-004：非法分类（不存在 4003 / 非叶子 4004 / 禁用 4007）。
	for _, tc := range []struct {
		categoryID int64
		code       int
	}{
		{999999, 4003},
		{parentID, 4004},
		{disabledID, 4007},
	} {
		r := createProduct(t, base, token, map[string]any{
			"name": "非法分类商品", "category_id": tc.categoryID, "price": 100,
		})
		if r.Status != 400 || r.Code != tc.code {
			t.Fatalf("invalid category %d: status=%d code=%d (want %d)", tc.categoryID, r.Status, r.Code, tc.code)
		}
	}
	if after := dbProductCount(t); after != before {
		t.Fatalf("no write expected on invalid category, products %d -> %d", before, after)
	}

	// 空名称 → 400/1001。
	r := createProduct(t, base, token, map[string]any{
		"name": "  ", "category_id": leafID, "price": 100,
	})
	if r.Status != 400 || r.Code != 1001 {
		t.Fatalf("empty name: status=%d code=%d", r.Status, r.Code)
	}
}

// TestProductStateMachine 覆盖 AC-005/006/007/008/009：合法/非法状态迁移与普通 update 不改状态。
func TestProductStateMachine(t *testing.T) {
	base, token := setupProductServer(t)

	leafID := createCategory(t, base, token, 0, "手机", nil)
	id := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "iPhone", "category_id": leafID, "price": 100,
	}))

	// AC-005：普通 update 提交合法 status 不改变状态；非法 status 返回 4006。
	res := doRequest(t, base, "PUT", fmt.Sprintf("/admin/products/%d", id), map[string]any{"status": "on_shelf"}, authHeader(token))
	assertOK(t, res, "update with legal status")
	if dbProductStatus(t, id) != 0 {
		t.Fatalf("legal status in update should be ignored, got %d", dbProductStatus(t, id))
	}
	res = doRequest(t, base, "PUT", fmt.Sprintf("/admin/products/%d", id), map[string]any{"status": "bogus"}, authHeader(token))
	if res.Status != 400 || res.Code != 4006 {
		t.Fatalf("illegal status: status=%d code=%d", res.Status, res.Code)
	}

	// AC-006：draft → on_shelf 成功。
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", id), nil, authHeader(token))
	assertOK(t, res, "on-shelf draft")
	if dbProductStatus(t, id) != 1 {
		t.Fatalf("expected on_shelf(1), got %d", dbProductStatus(t, id))
	}

	// AC-009：on_shelf → on_shelf 非法。
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", id), nil, authHeader(token))
	if res.Status != 409 || res.Code != 4005 {
		t.Fatalf("on-shelf on on_shelf: status=%d code=%d", res.Status, res.Code)
	}

	// AC-007：on_shelf → off_shelf 成功。
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/off-shelf", id), nil, authHeader(token))
	assertOK(t, res, "off-shelf")
	if dbProductStatus(t, id) != 2 {
		t.Fatalf("expected off_shelf(2), got %d", dbProductStatus(t, id))
	}

	// AC-009：off_shelf → off_shelf 非法。
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/off-shelf", id), nil, authHeader(token))
	if res.Status != 409 || res.Code != 4005 {
		t.Fatalf("off-shelf on off_shelf: status=%d code=%d", res.Status, res.Code)
	}

	// AC-008：off_shelf → on_shelf 成功。
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", id), nil, authHeader(token))
	assertOK(t, res, "on-shelf from off_shelf")
	if dbProductStatus(t, id) != 1 {
		t.Fatalf("expected on_shelf(1) from off_shelf, got %d", dbProductStatus(t, id))
	}

	// AC-009：draft → off_shelf 非法（先下架回 off_shelf 再新建 draft 商品验证）。
	// 用另一个商品验证 draft → off_shelf 非法。
	id2 := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "iPad", "category_id": leafID, "price": 200,
	}))
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/off-shelf", id2), nil, authHeader(token))
	if res.Status != 409 || res.Code != 4005 {
		t.Fatalf("draft -> off_shelf: status=%d code=%d", res.Status, res.Code)
	}
	if dbProductStatus(t, id2) != 0 {
		t.Fatalf("draft status should remain 0 after illegal transition, got %d", dbProductStatus(t, id2))
	}

	// 不存在商品的上架 → 404/4001。
	res = doRequest(t, base, "POST", "/admin/products/999999/on-shelf", nil, authHeader(token))
	if res.Status != 404 || res.Code != 4001 {
		t.Fatalf("on-shelf missing: status=%d code=%d", res.Status, res.Code)
	}
}

// TestProductConcurrentTransition 覆盖 AC-010：并发重复上架最多一次成功，最终状态 on_shelf。
func TestProductConcurrentTransition(t *testing.T) {
	base, token := setupProductServer(t)

	leafID := createCategory(t, base, token, 0, "手机", nil)
	id := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "并发商品", "category_id": leafID, "price": 100,
	}))

	const n = 10
	results := make(chan apiResult, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := request(base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", id), nil, authHeader(token))
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
		case 4005:
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
	if dbProductStatus(t, id) != 1 {
		t.Fatalf("expected final status on_shelf(1), got %d", dbProductStatus(t, id))
	}
}

// TestProductVisibility 覆盖 AC-011/012/013：前台仅 on_shelf，后台查看全部状态。
func TestProductVisibility(t *testing.T) {
	base, token := setupProductServer(t)

	leafID := createCategory(t, base, token, 0, "手机", nil)
	draftID := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "draft商品", "category_id": leafID, "price": 100,
	}))
	onShelfID := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "on_shelf商品", "category_id": leafID, "price": 200,
	}))
	offShelfID := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "off_shelf商品", "category_id": leafID, "price": 300,
	}))
	doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", onShelfID), nil, authHeader(token))
	doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", offShelfID), nil, authHeader(token))
	doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/off-shelf", offShelfID), nil, authHeader(token))

	// AC-011：前台列表仅 on_shelf。
	res := doRequest(t, base, "GET", "/products", nil, nil)
	assertOK(t, res, "frontend list")
	var list productListData
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].Id != onShelfID {
		t.Fatalf("frontend list should only contain on_shelf product, got total=%d items=%+v", list.Total, list.Items)
	}

	// AC-012：前台详情对 draft/off_shelf/不存在均 404；on_shelf 可见。
	for _, tc := range []struct {
		id int64
	}{
		{draftID}, {offShelfID}, {999999},
	} {
		r := doRequest(t, base, "GET", fmt.Sprintf("/products/%d", tc.id), nil, nil)
		if r.Status != 404 || r.Code != 4001 {
			t.Fatalf("frontend detail %d: status=%d code=%d", tc.id, r.Status, r.Code)
		}
	}
	if got := getProductDetail(t, base, onShelfID); got.Status != "on_shelf" {
		t.Fatalf("unexpected on_shelf detail: %+v", got)
	}

	// AC-013：后台列表与详情查看全部状态。
	res = doRequest(t, base, "GET", "/admin/products", nil, authHeader(token))
	assertOK(t, res, "admin list")
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatalf("unmarshal admin list: %v", err)
	}
	if list.Total != 3 {
		t.Fatalf("admin list should contain all 3 products, got total=%d", list.Total)
	}
	for _, id := range []int64{draftID, onShelfID, offShelfID} {
		r := doRequest(t, base, "GET", fmt.Sprintf("/admin/products/%d", id), nil, authHeader(token))
		assertOK(t, r, fmt.Sprintf("admin detail %d", id))
	}
}

// TestProductListFilterSortPagination 覆盖 AC-014/015/016/017：分类精确匹配、分页结构、
// 关键词 LIKE（含转义）、排序白名单与非法字段回落默认。
func TestProductListFilterSortPagination(t *testing.T) {
	base, token := setupProductServer(t)

	catA := createCategory(t, base, token, 0, "手机", nil)
	catB := createCategory(t, base, token, 0, "电脑", nil)

	mk := func(name, brand string, categoryID int64, price int) int64 {
		return getProductID(t, createProduct(t, base, token, map[string]any{
			"name": name, "brand": brand, "category_id": categoryID, "price": price,
		}))
	}

	// 上架全部商品，便于前台查询。
	ids := []int64{
		mk("iPhone", "Apple", catA, 5000),
		mk("小米14", "小米", catA, 3000),
		mk("MacBook", "Apple", catB, 9000),
		mk("ThinkPad", "联想", catB, 7000),
	}
	for _, id := range ids {
		assertOK(t, doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", id), nil, authHeader(token)), "on-shelf")
	}

	// AC-014：category_id 精确匹配。
	res := doRequest(t, base, "GET", "/products?category_id="+fmt.Sprint(catA), nil, nil)
	assertOK(t, res, "list by category")
	var list productListData
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list.Total != 2 {
		t.Fatalf("expected 2 products in catA, got %d", list.Total)
	}
	for _, it := range list.Items {
		if it.CategoryId != catA {
			t.Fatalf("unexpected category in result: %+v", it)
		}
	}

	// AC-015：分页结构 {items,total,page,size}；size 超限被钳制为 100。
	res = doRequest(t, base, "GET", "/products?page=1&size=2", nil, nil)
	assertOK(t, res, "paginate")
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list.Total != 4 || list.Page != 1 || list.Size != 2 || len(list.Items) != 2 {
		t.Fatalf("unexpected pagination: total=%d page=%d size=%d len=%d", list.Total, list.Page, list.Size, len(list.Items))
	}
	res = doRequest(t, base, "GET", "/products?size=9999", nil, nil)
	assertOK(t, res, "size cap")
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list.Size != 100 {
		t.Fatalf("expected size clamped to 100, got %d", list.Size)
	}

	// AC-016：关键词匹配 name/brand。
	res = doRequest(t, base, "GET", "/products?keyword=Apple", nil, nil)
	assertOK(t, res, "keyword brand")
	_ = json.Unmarshal(res.Data, &list)
	if list.Total != 2 { // iPhone(brand Apple) + MacBook(brand Apple)
		t.Fatalf("expected 2 Apple-brand products, got %d", list.Total)
	}

	// AC-017：排序白名单 + 非法字段回落默认（created_at DESC 不报错、无注入）。
	res = doRequest(t, base, "GET", "/products?sort=price&order=asc", nil, nil)
	assertOK(t, res, "sort price asc")
	_ = json.Unmarshal(res.Data, &list)
	if len(list.Items) != 4 || list.Items[0].Price != 3000 || list.Items[1].Price != 5000 {
		t.Fatalf("unexpected sort by price asc: %+v", list.Items)
	}
	res = doRequest(t, base, "GET", "/products?sort=evil;DROP+TABLE&order=desc", nil, nil)
	assertOK(t, res, "illegal sort fallback")
	_ = json.Unmarshal(res.Data, &list)
	if list.Total != 4 {
		t.Fatalf("illegal sort should not break query, got total=%d", list.Total)
	}
}

// TestProductKeywordEscaping 覆盖 INV-009：keyword 含 %/_/\ 时按普通文本匹配，不产生通配符效果。
func TestProductKeywordEscaping(t *testing.T) {
	base, token := setupProductServer(t)

	leafID := createCategory(t, base, token, 0, "手机", nil)
	mk := func(name string) int64 {
		return getProductID(t, createProduct(t, base, token, map[string]any{
			"name": name, "category_id": leafID, "price": 100,
		}))
	}
	pctID := mk("纯棉100%上衣") // 含字面 %
	otherID := mk("纯棉100元上衣")
	underID := mk("a_b_c")
	slashID := mk("a\\b") // 含字面反斜杠
	_ = otherID

	// 上架。
	for _, id := range []int64{pctID, otherID, underID, slashID} {
		assertOK(t, doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", id), nil, authHeader(token)), "on-shelf")
	}

	// 字面 %：搜索 "100%" 应仅命中含字面 "100%" 的商品，不命中 "100元"。
	res := doRequest(t, base, "GET", "/products?keyword=100%25", nil, nil)
	assertOK(t, res, "keyword literal percent")
	var list productListData
	_ = json.Unmarshal(res.Data, &list)
	if list.Total != 1 || list.Items[0].Id != pctID {
		t.Fatalf("literal %% should only match %d, got total=%d items=%+v", pctID, list.Total, list.Items)
	}

	// 字面 _：搜索 "a_b" 应仅命中 "a_b_c"，不把 _ 当通配符命中其它。
	res = doRequest(t, base, "GET", "/products?keyword=a_b", nil, nil)
	assertOK(t, res, "keyword literal underscore")
	_ = json.Unmarshal(res.Data, &list)
	if list.Total != 1 || list.Items[0].Id != underID {
		t.Fatalf("literal _ should only match %d, got total=%d items=%+v", underID, list.Total, list.Items)
	}

	// 字面 \：搜索 "a\b" 应仅命中含字面反斜杠的 "a\b"，不命中 "ab"（反斜杠不得吞掉后续字符）。
	q := url.Values{}
	q.Set("keyword", `a\b`)
	res = doRequest(t, base, "GET", "/products?"+q.Encode(), nil, nil)
	assertOK(t, res, "keyword literal backslash")
	_ = json.Unmarshal(res.Data, &list)
	if list.Total != 1 || list.Items[0].Id != slashID {
		t.Fatalf("literal backslash should only match %d, got total=%d items=%+v", slashID, list.Total, list.Items)
	}
}

// TestProductAuthorization 覆盖 AC-019/020/021：无 token 401、用户 token 403、无权限管理员 403、超级管理员成功。
func TestProductAuthorization(t *testing.T) {
	base, superToken := setupProductServer(t)
	leafID := createCategory(t, base, superToken, 0, "手机", nil)

	// AC-019：无 token 写 → 401。
	res := createProduct(t, base, "", map[string]any{"name": "x", "category_id": leafID, "price": 1})
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("no token: status=%d code=%d", res.Status, res.Code)
	}

	// 前台用户 token 写 → 403（type=user 被 AdminAuth 拒绝）。
	userToken, err := auth.GenerateWithSecret([]byte(testJWTSecret), auth.TypeUser, 1, "deadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatalf("generate user token: %v", err)
	}
	res = createProduct(t, base, userToken, map[string]any{"name": "x", "category_id": leafID, "price": 1})
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("user token: status=%d code=%d", res.Status, res.Code)
	}

	// AC-020：无 product 权限的普通管理员 → 403 且无写入。
	doRequest(t, base, "POST", "/admin/admins", map[string]any{"username": "noperm", "password": "nopermpass123"}, authHeader(superToken))
	noPermToken := loginAdmin(t, base, "noperm", "nopermpass123")
	before := dbProductCount(t)
	res = createProduct(t, base, noPermToken, map[string]any{"name": "越权商品", "category_id": leafID, "price": 1})
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("no-perm admin: status=%d code=%d", res.Status, res.Code)
	}
	if after := dbProductCount(t); after != before {
		t.Fatalf("no write expected on 403, products %d -> %d", before, after)
	}

	// AC-021：超级管理员成功。
	res = createProduct(t, base, superToken, map[string]any{"name": "合法商品", "category_id": leafID, "price": 1})
	assertOK(t, res, "super create product")
}

// TestCategoryDeleteProtection 覆盖 AC-022/023：分类下有商品时删除 409/3005；无商品删除成功。
func TestCategoryDeleteProtection(t *testing.T) {
	base, token := setupProductServer(t)

	// AC-022：分类下有商品 → 删除 409/3005，分类与商品均不受影响。
	leafID := createCategory(t, base, token, 0, "手机", nil)
	productID := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "iPhone", "category_id": leafID, "price": 100,
	}))
	res := doRequest(t, base, "DELETE", fmt.Sprintf("/categories/%d", leafID), nil, authHeader(token))
	if res.Status != 409 || res.Code != 3005 {
		t.Fatalf("delete category with product: status=%d code=%d", res.Status, res.Code)
	}
	// 分类仍在。
	r := doRequest(t, base, "GET", fmt.Sprintf("/categories/%d", leafID), nil, nil)
	assertOK(t, r, "category still exists")
	// 商品仍在。
	if dbProductStatus(t, productID) != 0 {
		t.Fatalf("product should still exist, status=%d", dbProductStatus(t, productID))
	}

	// AC-023：无商品关联的分类 → 删除成功。
	emptyID := createCategory(t, base, token, 0, "空分类", nil)
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/categories/%d", emptyID), nil, authHeader(token))
	assertOK(t, res, "delete empty category")
	r = doRequest(t, base, "GET", fmt.Sprintf("/categories/%d", emptyID), nil, nil)
	if r.Status != 404 || r.Code != 3001 {
		t.Fatalf("deleted category should be 404, got status=%d code=%d", r.Status, r.Code)
	}
}

// TestProductUpdate 覆盖 AC-002/003/004/018 的 update 侧与 INV-005/007/008：
// 合法字段更新、非法价格/分类拒绝且无写入、images 全量替换/省略保留、图片写入失败事务回滚。
func TestProductUpdate(t *testing.T) {
	base, token := setupProductServer(t)

	leafA := createCategory(t, base, token, 0, "手机", nil)
	leafB := createCategory(t, base, token, 0, "电脑", nil)
	parentID := createCategory(t, base, token, 0, "数码", nil)
	createCategory(t, base, token, parentID, "数码子类", nil) // 使 parentID 非叶子
	disabledID := createCategory(t, base, token, 0, "停用", intPtr(0))

	id := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "旧名", "brand": "旧品牌", "category_id": leafA,
		"price": 100, "main_image": "https://a/main.jpg", "images": []string{"https://a/1.jpg", "https://a/2.jpg"},
	}))

	// 合法字段更新：name/brand/category_id/price/main_image/images 全部生效。
	res := doRequest(t, base, "PUT", fmt.Sprintf("/admin/products/%d", id), map[string]any{
		"name": "新名", "brand": "新品牌", "category_id": leafB,
		"price": 200, "main_image": "https://b/main.jpg", "images": []string{"https://b/1.jpg"},
	}, authHeader(token))
	assertOK(t, res, "update all fields")
	d := getAdminProductDetail(t, base, token, id)
	if d.Name != "新名" || d.Brand != "新品牌" || d.CategoryId != leafB || d.Price != 200 || d.MainImage != "https://b/main.jpg" {
		t.Fatalf("unexpected updated product: %+v", d)
	}
	if len(d.Images) != 1 || d.Images[0] != "https://b/1.jpg" {
		t.Fatalf("images should be fully replaced, got %v", d.Images)
	}

	// images 未提供时保留现有图片。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/admin/products/%d", id), map[string]any{"name": "改名"}, authHeader(token))
	assertOK(t, res, "update name only")
	d = getAdminProductDetail(t, base, token, id)
	if d.Name != "改名" {
		t.Fatalf("name should update, got %+v", d)
	}
	if len(d.Images) != 1 || d.Images[0] != "https://b/1.jpg" {
		t.Fatalf("images should be preserved when omitted, got %v", d.Images)
	}

	// 非法价格（负/非整数/超上限）拒绝且不改变价格。
	before := getAdminProductDetail(t, base, token, id)
	for _, p := range []any{-1, 12.5, 100000000} {
		r := doRequest(t, base, "PUT", fmt.Sprintf("/admin/products/%d", id), map[string]any{"price": p}, authHeader(token))
		if r.Status != 400 || r.Code != 4002 {
			t.Fatalf("update invalid price %v: status=%d code=%d", p, r.Status, r.Code)
		}
	}
	after := getAdminProductDetail(t, base, token, id)
	if after.Price != before.Price {
		t.Fatalf("price should not change on invalid price update: %d -> %d", before.Price, after.Price)
	}

	// 非法分类（不存在 4003 / 非叶子 4004 / 禁用 4007）拒绝且不改变分类。
	for _, tc := range []struct {
		categoryID int64
		code       int
	}{
		{999999, 4003},
		{parentID, 4004},
		{disabledID, 4007},
	} {
		r := doRequest(t, base, "PUT", fmt.Sprintf("/admin/products/%d", id), map[string]any{"category_id": tc.categoryID}, authHeader(token))
		if r.Status != 400 || r.Code != tc.code {
			t.Fatalf("update invalid category %d: status=%d code=%d (want %d)", tc.categoryID, r.Status, r.Code, tc.code)
		}
	}
	after = getAdminProductDetail(t, base, token, id)
	if after.CategoryId != before.CategoryId {
		t.Fatalf("category_id should not change on invalid category update: %d -> %d", before.CategoryId, after.CategoryId)
	}

	// 模拟图片写入失败（URL 超 VARCHAR(512)）→ 事务回滚，商品名与图片均保持原值，无半成品。
	tooLong := strings.Repeat("a", 600)
	r := doRequest(t, base, "PUT", fmt.Sprintf("/admin/products/%d", id), map[string]any{
		"name": "不应生效", "images": []string{tooLong},
	}, authHeader(token))
	if r.Status != 500 || r.Code != 1000 {
		t.Fatalf("image too long should be 500/1000, got status=%d code=%d", r.Status, r.Code)
	}
	d = getAdminProductDetail(t, base, token, id)
	if d.Name == "不应生效" {
		t.Fatalf("product name should rollback on image failure")
	}
	if len(d.Images) != 1 || d.Images[0] != "https://b/1.jpg" {
		t.Fatalf("images should rollback on failure, got %v", d.Images)
	}
}

// TestProductOnShelfCategoryRevalidation 覆盖 INV-005 上架分支与 Contract Open Risks：
// 商品已绑定分类，分类被禁用（4007）或变为非叶子（4004）后再次上架被拒且状态不变。
func TestProductOnShelfCategoryRevalidation(t *testing.T) {
	base, token := setupProductServer(t)

	leafID := createCategory(t, base, token, 0, "手机", nil)
	id := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "iPhone", "category_id": leafID, "price": 100,
	}))

	// 上架 → 下架，回到 off_shelf，为「再次上架」做准备。
	assertOK(t, doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", id), nil, authHeader(token)), "on-shelf")
	assertOK(t, doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/off-shelf", id), nil, authHeader(token)), "off-shelf")

	// 禁用分类 → 再次上架被拒 4007，状态保持 off_shelf。
	assertOK(t, doRequest(t, base, "PUT", fmt.Sprintf("/categories/%d", leafID), map[string]any{"status": 0}, authHeader(token)), "disable category")
	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", id), nil, authHeader(token))
	if res.Status != 400 || res.Code != 4007 {
		t.Fatalf("on-shelf with disabled category: status=%d code=%d", res.Status, res.Code)
	}
	if dbProductStatus(t, id) != 2 {
		t.Fatalf("status should remain off_shelf(2), got %d", dbProductStatus(t, id))
	}

	// 恢复启用后使其变为非叶子 → 再次上架被拒 4004。
	assertOK(t, doRequest(t, base, "PUT", fmt.Sprintf("/categories/%d", leafID), map[string]any{"status": 1}, authHeader(token)), "enable category")
	createCategory(t, base, token, leafID, "手机子类", nil)
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", id), nil, authHeader(token))
	if res.Status != 400 || res.Code != 4004 {
		t.Fatalf("on-shelf with non-leaf category: status=%d code=%d", res.Status, res.Code)
	}
	if dbProductStatus(t, id) != 2 {
		t.Fatalf("status should remain off_shelf(2), got %d", dbProductStatus(t, id))
	}
}

// TestProductCreateWithGrantedPermission 覆盖 AC-021：非超管管理员经角色授予 product:create 后创建成功，
// 与 AC-020 的「无权限 403」形成对照，锁定 seed 权限 code 与路由 require(...) 一致。
func TestProductCreateWithGrantedPermission(t *testing.T) {
	base, superToken := setupProductServer(t)
	leafID := createCategory(t, base, superToken, 0, "手机", nil)

	// 建角色 → 授 product:create → 建普通管理员 → 分配角色。
	roleID := createRole(t, base, superToken, "商品管理员")
	permID := permissionID(t, "product:create")
	assertOK(t, doRequest(t, base, "POST", fmt.Sprintf("/admin/roles/%d/permissions", roleID), map[string]any{"permission_id": permID}, authHeader(superToken)), "assign permission")
	adminID := createAdmin(t, base, superToken, "productmgr", "productmgrpass123")
	assertOK(t, doRequest(t, base, "POST", fmt.Sprintf("/admin/admins/%d/roles", adminID), map[string]any{"role_id": roleID}, authHeader(superToken)), "assign role")

	// 该普通管理员登录后创建商品成功。
	mgrToken := loginAdmin(t, base, "productmgr", "productmgrpass123")
	res := createProduct(t, base, mgrToken, map[string]any{"name": "授权商品", "category_id": leafID, "price": 1})
	assertOK(t, res, "create product with granted product:create")
}

func intPtr(v int) *int {
	return &v
}
