package address_test

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
	"golang.org/x/crypto/bcrypt"

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

type addrJSON struct {
	Id            int64  `json:"id"`
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Province      string `json:"province"`
	City          string `json:"city"`
	District      string `json:"district"`
	Detail        string `json:"detail"`
	IsDefault     bool   `json:"is_default"`
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

// setupAddressServer 迁移 + Bootstrap + 清空 users/addresses + flush Redis，挂载真实路由，返回 base URL。
func setupAddressServer(t *testing.T) string {
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
	// 清空前台用户与地址表，保证每个用例从干净状态开始（不触碰后台 admins/RBAC seed）。
	for _, table := range []string{"addresses", "users"} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	if err := g.Redis().FlushDB(ctx); err != nil {
		t.Fatalf("clean redis: %v", err)
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
		for _, table := range []string{"addresses", "users"} {
			_, _ = g.DB().Exec(context.Background(), "DELETE FROM "+table)
		}
	})

	time.Sleep(100 * time.Millisecond)
	return fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
}

// insertUser 直接写入 users 表（真实 bcrypt 哈希），返回用户 id。
func insertUser(t *testing.T, username, password string) int64 {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash user password: %v", err)
	}
	id, err := g.DB().Model("users").Ctx(context.Background()).Data(g.Map{
		"username":      username,
		"password_hash": string(hash),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	return id
}

// loginUser 前台登录并返回 access token。
func loginUser(t *testing.T, base, username, password string) string {
	t.Helper()
	res := doRequest(t, base, "POST", "/login", map[string]any{"username": username, "password": password}, nil)
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

// newUser 创建一个用户并登录，返回 token。
func newUser(t *testing.T, base, username string) string {
	t.Helper()
	insertUser(t, username, "userpass123")
	return loginUser(t, base, username, "userpass123")
}

// addrBody 构造一个合法的地址创建体。
func addrBody(recipient, phone string) map[string]any {
	return map[string]any{
		"recipient_name": recipient,
		"phone":          phone,
		"province":       "广东省",
		"city":           "深圳市",
		"district":       "南山区",
		"detail":         "科技园路 1 号",
	}
}

// createAddress 创建地址并返回解析后的地址。
func createAddress(t *testing.T, base, token string, body map[string]any) addrJSON {
	t.Helper()
	res := doRequest(t, base, "POST", "/addresses", body, authHeader(token))
	assertOK(t, res, "create address")
	var a addrJSON
	if err := json.Unmarshal(res.Data, &a); err != nil {
		t.Fatalf("unmarshal create res: %v", err)
	}
	return a
}

func dbDefaultCount(t *testing.T, userID int64) int {
	t.Helper()
	n, err := g.DB().Model("addresses").Ctx(context.Background()).Where("user_id", userID).Where("is_default", 1).Count()
	if err != nil {
		t.Fatalf("count default: %v", err)
	}
	return n
}

func dbAddressCount(t *testing.T, userID int64) int {
	t.Helper()
	n, err := g.DB().Model("addresses").Ctx(context.Background()).Where("user_id", userID).Count()
	if err != nil {
		t.Fatalf("count addresses: %v", err)
	}
	return n
}

func dbAddressUserID(t *testing.T, id int64) int64 {
	t.Helper()
	v, err := g.DB().Model("addresses").Ctx(context.Background()).Where("id", id).Value("user_id")
	if err != nil {
		t.Fatalf("query address user_id: %v", err)
	}
	if v == nil || v.IsEmpty() {
		t.Fatalf("address %d not found", id)
	}
	return v.Int64()
}

// TestAddressCrudAndDefaultSwitch 覆盖 AC-001~005、AC-007、INV-003：
// 首条自动默认、增删改查、显式设默认取消旧默认、删除默认后允许无默认。
func TestAddressCrudAndDefaultSwitch(t *testing.T) {
	base := setupAddressServer(t)
	userID := insertUser(t, "addrcrud", "userpass123")
	token := loginUser(t, base, "addrcrud", "userpass123")

	// 首条地址（未传 is_default）→ 自动默认，且归属当前登录用户。
	a1 := createAddress(t, base, token, addrBody("张三", "13800138000"))
	if !a1.IsDefault {
		t.Fatalf("first address should be default, got is_default=%t", a1.IsDefault)
	}
	if a1.RecipientName != "张三" || a1.Phone != "13800138000" {
		t.Fatalf("unexpected created address: %+v", a1)
	}
	if got := dbAddressUserID(t, a1.Id); got != userID {
		t.Fatalf("created address should belong to user %d, got %d", userID, got)
	}

	// 第二条（未传 is_default）→ 非默认。
	a2 := createAddress(t, base, token, addrBody("李四", "13900139000"))
	if a2.IsDefault {
		t.Fatalf("second address should not be default")
	}
	if dbDefaultCount(t, userID) != 1 {
		t.Fatalf("expected exactly 1 default after two creates")
	}

	// 列表仅含本人两条，且归属正确。
	res := doRequest(t, base, "GET", "/addresses", nil, authHeader(token))
	assertOK(t, res, "list addresses")
	var list struct {
		Items []addrJSON `json:"items"`
	}
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("expected 2 addresses, got %d", len(list.Items))
	}

	// 详情。
	res = doRequest(t, base, "GET", fmt.Sprintf("/addresses/%d", a1.Id), nil, authHeader(token))
	assertOK(t, res, "detail address")
	var d addrJSON
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if d.Id != a1.Id || d.RecipientName != "张三" {
		t.Fatalf("detail mismatch: %+v", d)
	}

	// 更新 a2 为默认 → 旧默认 a1 自动取消，a2 成为默认。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/addresses/%d", a2.Id), map[string]any{"is_default": true}, authHeader(token))
	assertOK(t, res, "set a2 default")
	if dbDefaultCount(t, userID) != 1 {
		t.Fatalf("expected exactly 1 default after switch")
	}
	res = doRequest(t, base, "GET", fmt.Sprintf("/addresses/%d", a1.Id), nil, authHeader(token))
	assertOK(t, res, "detail a1 after switch")
	_ = json.Unmarshal(res.Data, &d)
	if d.IsDefault {
		t.Fatalf("a1 should no longer be default after switching to a2")
	}
	res = doRequest(t, base, "GET", fmt.Sprintf("/addresses/%d", a2.Id), nil, authHeader(token))
	assertOK(t, res, "detail a2 after switch")
	_ = json.Unmarshal(res.Data, &d)
	if !d.IsDefault {
		t.Fatalf("a2 should be default after switching")
	}

	// 删除默认地址 → 允许无默认，a1 不被自动提升。
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/addresses/%d", a2.Id), nil, authHeader(token))
	assertOK(t, res, "delete default address")
	if dbDefaultCount(t, userID) != 0 {
		t.Fatalf("expected 0 default after deleting default, no auto-promotion")
	}
	if dbAddressCount(t, userID) != 1 {
		t.Fatalf("expected 1 remaining address after delete")
	}
}

// TestAddressIsolation 覆盖 AC-006、INV-001：
// 用户 A 无法读/改/删用户 B 的地址（统一 404/7001 且无数据变化），不存在地址同样 404。
func TestAddressIsolation(t *testing.T) {
	base := setupAddressServer(t)
	_ = insertUser(t, "userisoA", "userpass123")
	userB := insertUser(t, "userisoB", "userpass123")
	tokenA := loginUser(t, base, "userisoA", "userpass123")
	tokenB := loginUser(t, base, "userisoB", "userpass123")

	a1 := createAddress(t, base, tokenA, addrBody("甲", "13800138000"))
	_ = userB

	// B 读/改/删 A 的地址 → 404/7001。
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{"GET", fmt.Sprintf("/addresses/%d", a1.Id), nil},
		{"PUT", fmt.Sprintf("/addresses/%d", a1.Id), map[string]any{"recipient_name": "篡改"}},
		{"DELETE", fmt.Sprintf("/addresses/%d", a1.Id), nil},
	} {
		res := doRequest(t, base, tc.method, tc.path, tc.body, authHeader(tokenB))
		if res.Status != 404 || res.Code != 7001 {
			t.Fatalf("%s %s by B: status=%d code=%d", tc.method, tc.path, res.Status, res.Code)
		}
	}

	// A 的地址未被 B 改动。
	res := doRequest(t, base, "GET", fmt.Sprintf("/addresses/%d", a1.Id), nil, authHeader(tokenA))
	assertOK(t, res, "A detail after B attempts")
	var d addrJSON
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if d.RecipientName != "甲" {
		t.Fatalf("A address was modified by B: %+v", d)
	}

	// 不存在的地址（A 视角）→ 404/7001。
	res = doRequest(t, base, "GET", "/addresses/999999", nil, authHeader(tokenA))
	if res.Status != 404 || res.Code != 7001 {
		t.Fatalf("nonexistent address: status=%d code=%d", res.Status, res.Code)
	}

	// B 名下无地址（B 从未成功写入 A 的地址，也没有创建过自己的地址）。
	if n := dbAddressCount(t, userB); n != 0 {
		t.Fatalf("B should have 0 addresses, got %d", n)
	}
	// A 列表仍只含自己 1 条。
	res = doRequest(t, base, "GET", "/addresses", nil, authHeader(tokenA))
	assertOK(t, res, "A list")
	var list struct {
		Items []addrJSON `json:"items"`
	}
	_ = json.Unmarshal(res.Data, &list)
	if len(list.Items) != 1 || list.Items[0].Id != a1.Id {
		t.Fatalf("A list should only contain own address, got %+v", list.Items)
	}
}

// TestAddressUnauthenticated 覆盖 AC-008、INV-004：无 token 访问任一地址接口返回 401/1002 且无写入。
func TestAddressUnauthenticated(t *testing.T) {
	base := setupAddressServer(t)

	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/addresses", addrBody("未登录", "13800138000")},
		{"GET", "/addresses", nil},
		{"GET", "/addresses/1", nil},
		{"PUT", "/addresses/1", map[string]any{"recipient_name": "x"}},
		{"DELETE", "/addresses/1", nil},
	} {
		res := doRequest(t, base, tc.method, tc.path, tc.body, nil)
		if res.Status != 401 || res.Code != 1002 {
			t.Fatalf("%s %s no token: status=%d code=%d", tc.method, tc.path, res.Status, res.Code)
		}
	}

	n, err := g.DB().Model("addresses").Ctx(context.Background()).Count()
	if err != nil {
		t.Fatalf("count addresses: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected no address write without token, got %d rows", n)
	}
}

// TestAddressValidation 覆盖字段校验：非法手机号/空收货人/空详情/空省/超长详情 → 400/1001 且无写入。
func TestAddressValidation(t *testing.T) {
	base := setupAddressServer(t)
	userID := insertUser(t, "addrval", "userpass123")
	token := loginUser(t, base, "addrval", "userpass123")

	longDetail := ""
	for i := 0; i < 256; i++ {
		longDetail += "a"
	}

	cases := []struct {
		name string
		body map[string]any
	}{
		{"invalid phone", map[string]any{"recipient_name": "张三", "phone": "12345", "province": "广东省", "city": "深圳市", "district": "南山区", "detail": "科技园"}},
		{"empty recipient", map[string]any{"recipient_name": "  ", "phone": "13800138000", "province": "广东省", "city": "深圳市", "district": "南山区", "detail": "科技园"}},
		{"empty detail", map[string]any{"recipient_name": "张三", "phone": "13800138000", "province": "广东省", "city": "深圳市", "district": "南山区", "detail": "  "}},
		{"empty province", map[string]any{"recipient_name": "张三", "phone": "13800138000", "province": "", "city": "深圳市", "district": "南山区", "detail": "科技园"}},
		{"overlong detail", map[string]any{"recipient_name": "张三", "phone": "13800138000", "province": "广东省", "city": "深圳市", "district": "南山区", "detail": longDetail}},
	}
	for _, c := range cases {
		res := doRequest(t, base, "POST", "/addresses", c.body, authHeader(token))
		if res.Status != 400 || res.Code != 1001 {
			t.Fatalf("%s: status=%d code=%d", c.name, res.Status, res.Code)
		}
	}
	if n := dbAddressCount(t, userID); n != 0 {
		t.Fatalf("expected no address write on validation failure, got %d", n)
	}
}

// TestAddressDefaultConcurrency 覆盖 AC-007、INV-002：
// 并发将不同地址设为默认，最终每用户默认地址数量恒等于 1（DB 唯一约束兜底），且无 500。
func TestAddressDefaultConcurrency(t *testing.T) {
	base := setupAddressServer(t)
	userID := insertUser(t, "addrrace", "userpass123")
	token := loginUser(t, base, "addrrace", "userpass123")

	a1 := createAddress(t, base, token, addrBody("并发一", "13800138000")) // 首条自动默认
	a2 := createAddress(t, base, token, addrBody("并发二", "13900139000"))
	a3 := createAddress(t, base, token, addrBody("并发三", "13700137000"))
	_ = a1

	const n = 20
	var wg sync.WaitGroup
	codesCh := make(chan int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := a2.Id
			if i%2 == 1 {
				target = a3.Id
			}
			r, err := request(base, "PUT", fmt.Sprintf("/addresses/%d", target), map[string]any{"is_default": true}, authHeader(token))
			if err != nil {
				codesCh <- -1
				return
			}
			codesCh <- r.Code
		}(i)
	}
	wg.Wait()
	close(codesCh)

	for code := range codesCh {
		if code != 0 && code != 7002 {
			t.Fatalf("unexpected set-default result code %d", code)
		}
	}
	// 不变量：任意时刻（最终）每用户默认地址数恒等于 1。
	if got := dbDefaultCount(t, userID); got != 1 {
		t.Fatalf("expected exactly 1 default after concurrent set-default, got %d", got)
	}
}
