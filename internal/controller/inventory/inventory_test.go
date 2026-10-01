package inventory_test

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

type invJSON struct {
	SkuId    int64 `json:"sku_id"`
	Quantity int64 `json:"quantity"`
}

type logJSON struct {
	Id              int64  `json:"id"`
	SkuId           int64  `json:"sku_id"`
	ChangeType      string `json:"change_type"`
	ChangeQty       int64  `json:"change_qty"`
	BeforeQty       int64  `json:"before_qty"`
	AfterQty        int64  `json:"after_qty"`
	OperatorAdminId *int64 `json:"operator_admin_id"`
}

type dbLogRow struct {
	ChangeType int
	ChangeQty  int64
	BeforeQty  int64
	AfterQty   int64
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
	var (
		r   *gclient.Response
		err error
	)
	switch method {
	case "GET":
		r, err = c.Get(context.Background(), base+path)
	case "POST":
		r, err = c.ContentJson().Post(context.Background(), base+path, body)
	case "PUT":
		r, err = c.ContentJson().Put(context.Background(), base+path, body)
	case "DELETE":
		r, err = c.Delete(context.Background(), base+path)
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

// setupInventoryServer 迁移 + Bootstrap + 清空相关表（含库存表），挂载真实路由，
// 返回 base 与超级管理员 token。
func setupInventoryServer(t *testing.T) (base, token string) {
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
	// 清理顺序遵循 FK：inventory_logs/inventories → skus → product_images/products → categories。
	for _, table := range []string{"inventory_logs", "inventories", "skus", "product_images", "products", "categories"} {
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
		for _, table := range []string{"inventory_logs", "inventories", "skus", "product_images", "products"} {
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
	return d.AccessToken
}

// skuNameSeq 为每个测试用例生成唯一命名，避免同测试内分类/商品/SKU 撞唯一约束。
var skuNameSeq int64

// newSku 创建一条「分类 → 商品 → SKU」链路并返回 SKU id。
func newSku(t *testing.T, base, token string) int64 {
	t.Helper()
	n := atomic.AddInt64(&skuNameSeq, 1)
	leafID := createCategory(t, base, token, 0, fmt.Sprintf("库存分类%d", n))
	productID := createProduct(t, base, token, map[string]any{
		"name": fmt.Sprintf("库存商品%d", n), "category_id": leafID, "price": 100,
	})
	res := doRequest(t, base, "POST", "/admin/skus", map[string]any{
		"product_id": productID, "name": fmt.Sprintf("库存SKU%d", n), "price": 100,
	}, authHeader(token))
	assertOK(t, res, "create sku")
	var d struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal sku res: %v", err)
	}
	return d.Id
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

func increase(t *testing.T, base, token string, skuID, qty int64) {
	t.Helper()
	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/increase", skuID), map[string]any{"quantity": qty}, authHeader(token))
	assertOK(t, res, "increase")
}

// dbQuantity 查询指定 SKU 的 DB 库存数量；第二个返回值表示是否存在库存记录。
func dbQuantity(t *testing.T, skuID int64) (int64, bool) {
	t.Helper()
	row, err := g.DB().Model("inventories").Ctx(context.Background()).Where("sku_id", skuID).One()
	if err != nil {
		t.Fatalf("query inventory %d: %v", skuID, err)
	}
	if row == nil || row.IsEmpty() {
		return 0, false
	}
	return row["quantity"].Int64(), true
}

// dbLogs 按 id 升序返回指定 SKU 的流水记录（change_type TINYINT 原值）。
func dbLogs(t *testing.T, skuID int64) []dbLogRow {
	t.Helper()
	rows, err := g.DB().Model("inventory_logs").Ctx(context.Background()).Where("sku_id", skuID).Order("id").All()
	if err != nil {
		t.Fatalf("query logs %d: %v", skuID, err)
	}
	out := make([]dbLogRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbLogRow{
			ChangeType: r["change_type"].Int(),
			ChangeQty:  r["change_qty"].Int64(),
			BeforeQty:  r["before_qty"].Int64(),
			AfterQty:   r["after_qty"].Int64(),
		})
	}
	return out
}

// TestInventoryGetLazyZeroAndNotFound 覆盖 AC-001：存在 SKU 无记录时返回 0；不存在 SKU 返回 404/5001。
func TestInventoryGetLazyZeroAndNotFound(t *testing.T) {
	base, token := setupInventoryServer(t)
	skuID := newSku(t, base, token)

	res := doRequest(t, base, "GET", fmt.Sprintf("/admin/inventories/%d", skuID), nil, authHeader(token))
	assertOK(t, res, "get inventory")
	var inv invJSON
	if err := json.Unmarshal(res.Data, &inv); err != nil {
		t.Fatalf("unmarshal inventory: %v", err)
	}
	if inv.Quantity != 0 || inv.SkuId != skuID {
		t.Fatalf("expected sku_id=%d quantity=0, got %+v", skuID, inv)
	}

	// 不存在 SKU → 404/5001。
	res = doRequest(t, base, "GET", "/admin/inventories/999999", nil, authHeader(token))
	if res.Status != 404 || res.Code != 5001 {
		t.Fatalf("get missing sku: status=%d code=%d", res.Status, res.Code)
	}
}

// TestInventoryIncreaseInitAndAccumulate 覆盖 AC-002、INV-004：首次增加初始化、再次增加累加、
// 每 SKU 至多一条库存记录，且每条增加产生一条 before/after 一致的「增加」流水。
func TestInventoryIncreaseInitAndAccumulate(t *testing.T) {
	base, token := setupInventoryServer(t)
	skuID := newSku(t, base, token)

	if q, ok := dbQuantity(t, skuID); ok || q != 0 {
		t.Fatalf("expected no inventory record initially, got q=%d exists=%t", q, ok)
	}

	// 首次 increase 建立记录：0 → 10。
	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/increase", skuID), map[string]any{"quantity": 10}, authHeader(token))
	assertOK(t, res, "first increase")
	var inv invJSON
	if err := json.Unmarshal(res.Data, &inv); err != nil {
		t.Fatalf("unmarshal inventory: %v", err)
	}
	if inv.Quantity != 10 {
		t.Fatalf("expected quantity 10 after first increase, got %d", inv.Quantity)
	}

	// 再次 increase 累加：10 → 25。
	increase(t, base, token, skuID, 15)

	q, ok := dbQuantity(t, skuID)
	if !ok || q != 25 {
		t.Fatalf("expected quantity 25, got %d (exists=%t)", q, ok)
	}

	logs := dbLogs(t, skuID)
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}
	if logs[0].ChangeType != 1 || logs[0].ChangeQty != 10 || logs[0].BeforeQty != 0 || logs[0].AfterQty != 10 {
		t.Fatalf("first log mismatch: %+v", logs[0])
	}
	if logs[1].ChangeType != 1 || logs[1].ChangeQty != 15 || logs[1].BeforeQty != 10 || logs[1].AfterQty != 25 {
		t.Fatalf("second log mismatch: %+v", logs[1])
	}
}

// TestInventoryDeductSuccess 覆盖 AC-003、INV-003：库存充足时扣减成功、库存减少、产生「扣减」流水。
func TestInventoryDeductSuccess(t *testing.T) {
	base, token := setupInventoryServer(t)
	skuID := newSku(t, base, token)
	increase(t, base, token, skuID, 30)

	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/deduct", skuID), map[string]any{"quantity": 12}, authHeader(token))
	assertOK(t, res, "deduct")
	var inv invJSON
	if err := json.Unmarshal(res.Data, &inv); err != nil {
		t.Fatalf("unmarshal inventory: %v", err)
	}
	if inv.Quantity != 18 {
		t.Fatalf("expected quantity 18 after deduct, got %d", inv.Quantity)
	}

	logs := dbLogs(t, skuID)
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs (1 increase + 1 deduct), got %d", len(logs))
	}
	d := logs[1]
	if d.ChangeType != 2 || d.ChangeQty != 12 || d.BeforeQty != 30 || d.AfterQty != 18 {
		t.Fatalf("deduct log mismatch: %+v", d)
	}
}

// TestInventoryDeductInsufficient 覆盖 AC-004、INV-002：库存不足时扣减拒绝 409/6001、
// 库存不变、不产生成功扣减流水。
func TestInventoryDeductInsufficient(t *testing.T) {
	base, token := setupInventoryServer(t)
	skuID := newSku(t, base, token)
	increase(t, base, token, skuID, 5)

	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/deduct", skuID), map[string]any{"quantity": 6}, authHeader(token))
	if res.Status != 409 || res.Code != 6001 {
		t.Fatalf("insufficient deduct: status=%d code=%d", res.Status, res.Code)
	}

	q, _ := dbQuantity(t, skuID)
	if q != 5 {
		t.Fatalf("quantity should remain 5 on insufficient deduct, got %d", q)
	}
	if logs := dbLogs(t, skuID); len(logs) != 1 {
		t.Fatalf("expected only the increase log, got %d logs", len(logs))
	}
}

// TestInventoryInvalidQuantity 覆盖 AC 数量校验：0/负数/非整数数量 → 400/6002 且无写入。
func TestInventoryInvalidQuantity(t *testing.T) {
	base, token := setupInventoryServer(t)
	skuID := newSku(t, base, token)

	for _, op := range []string{"increase", "deduct"} {
		for _, q := range []any{0, -1, 1.5} {
			res := doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/%s", skuID, op), map[string]any{"quantity": q}, authHeader(token))
			if res.Status != 400 || res.Code != 6002 {
				t.Fatalf("%s quantity=%v: status=%d code=%d", op, q, res.Status, res.Code)
			}
		}
	}
	if q, ok := dbQuantity(t, skuID); ok && q != 0 {
		t.Fatalf("expected no inventory record on invalid quantity, got q=%d exists=%t", q, ok)
	}
	if logs := dbLogs(t, skuID); len(logs) != 0 {
		t.Fatalf("expected no logs on invalid quantity, got %d", len(logs))
	}
}

// TestInventoryLogsOrdering 覆盖 AC-006：多次变更后流水按 id 倒序、字段完整且与库存变化一致。
func TestInventoryLogsOrdering(t *testing.T) {
	base, token := setupInventoryServer(t)
	skuID := newSku(t, base, token)
	increase(t, base, token, skuID, 10) // 0→10
	increase(t, base, token, skuID, 5)  // 10→15
	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/deduct", skuID), map[string]any{"quantity": 3}, authHeader(token))
	assertOK(t, res, "deduct")

	// 查询流水（API 返回 id 倒序）。
	res = doRequest(t, base, "GET", fmt.Sprintf("/admin/inventories/%d/logs", skuID), nil, authHeader(token))
	assertOK(t, res, "list logs")
	var lr struct {
		Items []logJSON `json:"items"`
	}
	if err := json.Unmarshal(res.Data, &lr); err != nil {
		t.Fatalf("unmarshal logs: %v", err)
	}
	if len(lr.Items) != 3 {
		t.Fatalf("expected 3 logs, got %d", len(lr.Items))
	}
	// id 倒序：最新在前。
	if lr.Items[0].ChangeType != "deduct" || lr.Items[0].BeforeQty != 15 || lr.Items[0].AfterQty != 12 {
		t.Fatalf("latest log mismatch: %+v", lr.Items[0])
	}
	if lr.Items[2].ChangeType != "increase" || lr.Items[2].BeforeQty != 0 || lr.Items[2].AfterQty != 10 {
		t.Fatalf("earliest log mismatch: %+v", lr.Items[2])
	}
	// AC-006：管理员操作产生的流水应记录操作者。
	for _, it := range lr.Items {
		if it.OperatorAdminId == nil {
			t.Fatalf("expected operator_admin_id recorded, got nil in %+v", it)
		}
	}
}

// TestInventoryConcurrentDeduct 覆盖 AC-007、INV-001：并发扣减同一 SKU 最终库存 ≥ 0、
// 成功扣减总量 = 初始库存 − 最终库存、成功次数 ≤ ⌊初始库存/单次扣减量⌋。
// 该测试能区分「条件更新」与「先查再写」：先查再写在并发下会超卖或失败计数错误。
func TestInventoryConcurrentDeduct(t *testing.T) {
	base, token := setupInventoryServer(t)
	skuID := newSku(t, base, token)
	increase(t, base, token, skuID, 100)

	const (
		n    = 20
		each = 10
	)
	results := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := request(base, "POST", fmt.Sprintf("/admin/inventories/%d/deduct", skuID), map[string]any{"quantity": each}, authHeader(token))
			if err != nil {
				results <- -1
				return
			}
			results <- r.Code
		}()
	}
	wg.Wait()
	close(results)

	success, insufficient := 0, 0
	for code := range results {
		switch code {
		case 0:
			success++
		case 6001:
			insufficient++
		default:
			t.Fatalf("unexpected deduct result code %d", code)
		}
	}
	// ⌊100/10⌋ = 10 次成功。
	if success != 10 {
		t.Fatalf("expected exactly 10 successful deducts, got %d", success)
	}
	if insufficient != n-10 {
		t.Fatalf("expected %d insufficient, got %d", n-10, insufficient)
	}
	q, ok := dbQuantity(t, skuID)
	if !ok || q != 0 {
		t.Fatalf("expected final quantity 0, got %d (exists=%t)", q, ok)
	}
	// 成功扣减总量 = 初始库存 − 最终库存 = 100。
	logs := dbLogs(t, skuID)
	if len(logs) != 1+success {
		t.Fatalf("expected %d logs (1 increase + %d deduct), got %d", 1+success, success, len(logs))
	}
}

// TestInventoryConcurrentIncrease 覆盖 INV-004：并发增加无丢失更新，总量 = n×单次量。
func TestInventoryConcurrentIncrease(t *testing.T) {
	base, token := setupInventoryServer(t)
	skuID := newSku(t, base, token)

	const (
		n    = 10
		each = 5
	)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = request(base, "POST", fmt.Sprintf("/admin/inventories/%d/increase", skuID), map[string]any{"quantity": each}, authHeader(token))
		}()
	}
	wg.Wait()

	q, ok := dbQuantity(t, skuID)
	if !ok || q != n*each {
		t.Fatalf("expected total quantity %d after concurrent increase, got %d (exists=%t)", n*each, q, ok)
	}
}

// TestInventorySkuNotFound 覆盖 INV-006：不存在 SKU 的查询/增加/扣减/流水均 404/5001 且无写入。
func TestInventorySkuNotFound(t *testing.T) {
	base, token := setupInventoryServer(t)
	const missing = 999999

	if res := doRequest(t, base, "GET", fmt.Sprintf("/admin/inventories/%d", missing), nil, authHeader(token)); res.Status != 404 || res.Code != 5001 {
		t.Fatalf("get missing sku: status=%d code=%d", res.Status, res.Code)
	}
	if res := doRequest(t, base, "GET", fmt.Sprintf("/admin/inventories/%d/logs", missing), nil, authHeader(token)); res.Status != 404 || res.Code != 5001 {
		t.Fatalf("logs missing sku: status=%d code=%d", res.Status, res.Code)
	}
	if res := doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/increase", missing), map[string]any{"quantity": 1}, authHeader(token)); res.Status != 404 || res.Code != 5001 {
		t.Fatalf("increase missing sku: status=%d code=%d", res.Status, res.Code)
	}
	if res := doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/deduct", missing), map[string]any{"quantity": 1}, authHeader(token)); res.Status != 404 || res.Code != 5001 {
		t.Fatalf("deduct missing sku: status=%d code=%d", res.Status, res.Code)
	}

	n, err := g.DB().Model("inventories").Ctx(context.Background()).Where("sku_id", missing).Count()
	if err != nil {
		t.Fatalf("count inventories: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected no inventory row for missing sku, got %d", n)
	}
}

// TestInventoryAuthorization 覆盖 INV-005：无 token 401、无权限管理员 403 且无写入、
// 持有权限成功、超管放行。
func TestInventoryAuthorization(t *testing.T) {
	base, superToken := setupInventoryServer(t)
	skuID := newSku(t, base, superToken)

	// 无 token 写 → 401/1002。
	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/increase", skuID), map[string]any{"quantity": 1}, nil)
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("no token: status=%d code=%d", res.Status, res.Code)
	}

	// 无权限普通管理员 → 403 且无写入。
	doRequest(t, base, "POST", "/admin/admins", map[string]any{"username": "noperm", "password": "nopermpass123"}, authHeader(superToken))
	noPermToken := loginAdmin(t, base, "noperm", "nopermpass123")
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/increase", skuID), map[string]any{"quantity": 1}, authHeader(noPermToken))
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("no-perm admin: status=%d code=%d", res.Status, res.Code)
	}
	if q, ok := dbQuantity(t, skuID); ok && q != 0 {
		t.Fatalf("no write expected on 403, got q=%d exists=%t", q, ok)
	}

	// 超管成功。
	increase(t, base, superToken, skuID, 7)

	// 经角色授予 inventory:increase 的普通管理员成功。
	roleID := createRole(t, base, superToken, "库存管理员")
	permID := permissionID(t, "inventory:increase")
	assertOK(t, doRequest(t, base, "POST", fmt.Sprintf("/admin/roles/%d/permissions", roleID), map[string]any{"permission_id": permID}, authHeader(superToken)), "assign permission")
	adminID := createAdmin(t, base, superToken, "invmgr", "invmgrpass123")
	assertOK(t, doRequest(t, base, "POST", fmt.Sprintf("/admin/admins/%d/roles", adminID), map[string]any{"role_id": roleID}, authHeader(superToken)), "assign role")
	mgrToken := loginAdmin(t, base, "invmgr", "invmgrpass123")
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/increase", skuID), map[string]any{"quantity": 3}, authHeader(mgrToken))
	assertOK(t, res, "increase with granted inventory:increase")
}

// TestSkuDeleteWithInventoryBlocked 覆盖 INV-006 / SKU 删除交互：有库存记录的 SKU 删除 409/5005；
// 无库存记录的 SKU 删除仍成功。
func TestSkuDeleteWithInventoryBlocked(t *testing.T) {
	base, token := setupInventoryServer(t)
	skuWith := newSku(t, base, token)
	increase(t, base, token, skuWith, 10)

	res := doRequest(t, base, "DELETE", fmt.Sprintf("/admin/skus/%d", skuWith), nil, authHeader(token))
	if res.Status != 409 || res.Code != 5005 {
		t.Fatalf("delete sku with inventory: status=%d code=%d", res.Status, res.Code)
	}

	skuNoInv := newSku(t, base, token)
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/admin/skus/%d", skuNoInv), nil, authHeader(token))
	assertOK(t, res, "delete sku without inventory")
}
