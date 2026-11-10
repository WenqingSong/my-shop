package profile_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	"golang.org/x/crypto/bcrypt"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/profile/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/cmd"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// safeTestDB 本任务专用的独立测试库。测试会 DELETE users/addresses，
// 必须在写操作前核实真实连接库名，绝不落到 my_shop / my_shop_b10304_verify。
const safeTestDB = "my_shop_b104_test"

const (
	testJWTSecret     = "test-secret-0123456789-0123456789-0123456789"
	testAdminPassword = "test-admin-password-123"
)

type apiResult struct {
	Status  int
	Code    int
	Message string
	Data    json.RawMessage
}

type profileJSON struct {
	Id        int64   `json:"id"`
	Username  string  `json:"username"`
	Nickname  string  `json:"nickname"`
	AvatarUrl string  `json:"avatar_url"`
	Phone     *string `json:"phone"`
	Birthday  *string `json:"birthday"`
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

// requireIsolatedTestDB 在迁移/写库之前做第一道隔离检查：只允许本任务测试库。
func requireIsolatedTestDB(t *testing.T) {
	t.Helper()
	if got := os.Getenv("DATABASE_DEFAULT_NAME"); got != safeTestDB {
		t.Fatalf("拒绝执行破坏性测试：DATABASE_DEFAULT_NAME=%q，必须显式设为独立测试库 %q", got, safeTestDB)
	}
}

// requireActualDB 在 Bootstrap 之后核实真实连接库名（不依赖环境变量名称推断）。
func requireActualDB(t *testing.T) {
	t.Helper()
	v, err := g.DB().GetValue(context.Background(), "SELECT DATABASE()")
	if err != nil {
		t.Fatalf("查询当前数据库失败: %v", err)
	}
	if got := v.String(); got != safeTestDB {
		t.Fatalf("拒绝执行破坏性测试：实际连接库为 %q，不是独立测试库 %q", got, safeTestDB)
	}
}

// setupProfileServer 迁移 + Bootstrap + 清空 users/addresses + flush Redis，挂载真实前台路由。
func setupProfileServer(t *testing.T) string {
	t.Helper()
	requireIsolatedTestDB(t)
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
	requireActualDB(t)

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

func insertUser(t *testing.T, username, password string) int64 {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
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

func loginUser(t *testing.T, base, username, password string) string {
	t.Helper()
	res := doRequest(t, base, "POST", "/login", map[string]any{"username": username, "password": password}, nil)
	assertOK(t, res, "login "+username)
	var d struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal login: %v", err)
	}
	if d.AccessToken == "" {
		t.Fatalf("login %s: empty access_token", username)
	}
	return d.AccessToken
}

func updateProfile(t *testing.T, base, token string, body map[string]any) profileJSON {
	t.Helper()
	res := doRequest(t, base, "PUT", "/profile", body, authHeader(token))
	assertOK(t, res, "update profile")
	var p profileJSON
	if err := json.Unmarshal(res.Data, &p); err != nil {
		t.Fatalf("unmarshal update res: %v", err)
	}
	return p
}

// TestProfileGetAndUpdateFields 覆盖：读取默认资料、更新昵称/头像/手机号/生日、字段未提供保持不变。
func TestProfileGetAndUpdateFields(t *testing.T) {
	base := setupProfileServer(t)
	userID := insertUser(t, "profilemain", "userpass123")
	token := loginUser(t, base, "profilemain", "userpass123")

	res := doRequest(t, base, "GET", "/profile", nil, authHeader(token))
	assertOK(t, res, "get profile")
	var p profileJSON
	if err := json.Unmarshal(res.Data, &p); err != nil {
		t.Fatalf("unmarshal get res: %v", err)
	}
	if p.Id != userID || p.Username != "profilemain" {
		t.Fatalf("unexpected identity: %+v", p)
	}
	if p.Nickname != "" || p.AvatarUrl != "" || p.Phone != nil || p.Birthday != nil {
		t.Fatalf("new user should have empty profile, got %+v", p)
	}

	// 昵称（trim）与头像。
	p = updateProfile(t, base, token, map[string]any{
		"nickname":   "  小明  ",
		"avatar_url": "https://cdn.example.com/avatar/a.png",
	})
	if p.Nickname != "小明" || p.AvatarUrl != "https://cdn.example.com/avatar/a.png" {
		t.Fatalf("nickname/avatar update failed: %+v", p)
	}

	// 手机号与生日。
	p = updateProfile(t, base, token, map[string]any{"phone": "13800138000", "birthday": "1990-05-20"})
	if p.Phone == nil || *p.Phone != "13800138000" {
		t.Fatalf("phone update failed: %+v", p)
	}
	if p.Birthday == nil || *p.Birthday != "1990-05-20" {
		t.Fatalf("birthday update failed: %+v", p)
	}

	// 仅更新昵称：其余字段必须保持不变（区分「未提供」与「清空」）。
	p = updateProfile(t, base, token, map[string]any{"nickname": "小明二"})
	if p.Nickname != "小明二" || p.Phone == nil || *p.Phone != "13800138000" ||
		p.Birthday == nil || *p.Birthday != "1990-05-20" ||
		p.AvatarUrl != "https://cdn.example.com/avatar/a.png" {
		t.Fatalf("omitted fields must not change: %+v", p)
	}

	// 落库校验：DB 中手机号/生日与响应一致。
	var dbRow struct {
		Nickname  string  `json:"nickname"`
		AvatarUrl string  `json:"avatar_url"`
		Phone     *string `json:"phone"`
		Birthday  *string `json:"birthday"`
	}
	if err := g.DB().Model("users").Ctx(context.Background()).Where("id", userID).Scan(&dbRow); err != nil {
		t.Fatalf("query db profile: %v", err)
	}
	if dbRow.Nickname != "小明二" || dbRow.Phone == nil || *dbRow.Phone != "13800138000" {
		t.Fatalf("db profile mismatch: %+v", dbRow)
	}
}

// TestProfileClearVersusOmit 覆盖：空串主动清空 vs 字段缺失不修改。
func TestProfileClearVersusOmit(t *testing.T) {
	base := setupProfileServer(t)
	userID := insertUser(t, "profileclear", "userpass123")
	token := loginUser(t, base, "profileclear", "userpass123")

	updateProfile(t, base, token, map[string]any{
		"nickname":   "待清空",
		"avatar_url": "https://cdn.example.com/a.png",
		"phone":      "13900139000",
		"birthday":   "1999-01-02",
	})

	// 空请求体：所有字段保持不变。
	p := updateProfile(t, base, token, map[string]any{})
	if p.Nickname != "待清空" || p.Phone == nil || *p.Phone != "13900139000" ||
		p.Birthday == nil || *p.Birthday != "1999-01-02" {
		t.Fatalf("empty update must not change fields: %+v", p)
	}

	// 只清空昵称：其余字段保留。
	p = updateProfile(t, base, token, map[string]any{"nickname": ""})
	if p.Nickname != "" {
		t.Fatalf("nickname should be cleared: %+v", p)
	}
	if p.Phone == nil || *p.Phone != "13900139000" {
		t.Fatalf("phone should be preserved when only nickname cleared: %+v", p)
	}

	// 清空手机号与生日：DB 应为 NULL（而不是空串）。
	p = updateProfile(t, base, token, map[string]any{"phone": "", "birthday": ""})
	if p.Phone != nil || p.Birthday != nil {
		t.Fatalf("phone/birthday should be NULL after clear: %+v", p)
	}
	dbPhone, err := g.DB().Model("users").Ctx(context.Background()).Where("id", userID).Value("phone")
	if err != nil {
		t.Fatalf("query phone: %v", err)
	}
	dbBirthday, err := g.DB().Model("users").Ctx(context.Background()).Where("id", userID).Value("birthday")
	if err != nil {
		t.Fatalf("query birthday: %v", err)
	}
	if !dbPhone.IsNil() || !dbBirthday.IsNil() {
		t.Fatalf("cleared fields must be NULL in DB, phone=%v birthday=%v", dbPhone, dbBirthday)
	}
}

// TestProfileValidation 覆盖：手机号格式/生日格式/头像协议/昵称超长 → 400/1001 且不改动已有数据。
func TestProfileValidation(t *testing.T) {
	base := setupProfileServer(t)
	userID := insertUser(t, "profileval", "userpass123")
	token := loginUser(t, base, "profileval", "userpass123")
	updateProfile(t, base, token, map[string]any{"nickname": "保持", "phone": "13700137000"})

	longNickname := strings.Repeat("字", 33)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"phone too short", map[string]any{"phone": "12345"}},
		{"phone bad prefix", map[string]any{"phone": "12000120000"}},
		{"birthday overflow", map[string]any{"birthday": "1990-02-30"}},
		{"birthday not padded", map[string]any{"birthday": "1990-2-3"}},
		{"birthday garbage", map[string]any{"birthday": "abc"}},
		{"avatar javascript scheme", map[string]any{"avatar_url": "javascript:alert(1)"}},
		{"avatar not url", map[string]any{"avatar_url": "not-a-url"}},
		{"nickname too long", map[string]any{"nickname": longNickname}},
	}
	for _, c := range cases {
		res := doRequest(t, base, "PUT", "/profile", c.body, authHeader(token))
		if res.Status != 400 || res.Code != 1001 {
			t.Fatalf("%s: status=%d code=%d msg=%q", c.name, res.Status, res.Code, res.Message)
		}
	}

	var dbRow struct {
		Nickname string  `json:"nickname"`
		Phone    *string `json:"phone"`
	}
	if err := g.DB().Model("users").Ctx(context.Background()).Where("id", userID).Scan(&dbRow); err != nil {
		t.Fatalf("query profile: %v", err)
	}
	if dbRow.Nickname != "保持" || dbRow.Phone == nil || *dbRow.Phone != "13700137000" {
		t.Fatalf("validation failures must not change data: %+v", dbRow)
	}
}

// TestProfilePhoneUnique 覆盖：手机号唯一由 DB uk_phone 保证（并发写入同一号码只有一个成功）。
func TestProfilePhoneUnique(t *testing.T) {
	base := setupProfileServer(t)
	insertUser(t, "phonea", "userpass123")
	insertUser(t, "phoneb", "userpass123")
	tokenA := loginUser(t, base, "phonea", "userpass123")
	tokenB := loginUser(t, base, "phoneb", "userpass123")

	const phone = "13600136000"
	resA := doRequest(t, base, "PUT", "/profile", map[string]any{"phone": phone}, authHeader(tokenA))
	assertOK(t, resA, "A set phone")

	// B 设置同一手机号 → 409/2016，且 B 的号码保持 NULL。
	resB := doRequest(t, base, "PUT", "/profile", map[string]any{"phone": phone}, authHeader(tokenB))
	if resB.Status != 409 || resB.Code != 2016 {
		t.Fatalf("duplicate phone: status=%d code=%d", resB.Status, resB.Code)
	}
	bPhone, err := g.DB().Model("users").Ctx(context.Background()).Where("username", "phoneb").Value("phone")
	if err != nil {
		t.Fatalf("query B phone: %v", err)
	}
	if !bPhone.IsNil() {
		t.Fatalf("B phone should remain NULL, got %v", bPhone)
	}

	// 并发把同一新号码设置到两个用户：最多一个成功，DB 中该号码只出现一次。
	const racePhone = "13500135000"
	var wg sync.WaitGroup
	results := make(chan apiResult, 2)
	for _, token := range []string{tokenA, tokenB} {
		wg.Add(1)
		go func(token string) {
			defer wg.Done()
			r, err := request(base, "PUT", "/profile", map[string]any{"phone": racePhone}, authHeader(token))
			if err != nil {
				results <- apiResult{Code: -1}
				return
			}
			results <- r
		}(token)
	}
	wg.Wait()
	close(results)

	success, conflict := 0, 0
	for r := range results {
		switch {
		case r.Code == 0:
			success++
		case r.Code == 2016:
			conflict++
		default:
			t.Fatalf("unexpected concurrent phone result: status=%d code=%d", r.Status, r.Code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("expected exactly 1 success and 1 conflict, got success=%d conflict=%d", success, conflict)
	}
	n, err := g.DB().Model("users").Ctx(context.Background()).Where("phone", racePhone).Count()
	if err != nil {
		t.Fatalf("count phone: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 user with phone %s, got %d", racePhone, n)
	}
}

// TestProfileIsolation 覆盖：只能改本人资料（忽略请求体 user_id）、未登录 401、目标用户不存在 2015。
func TestProfileIsolation(t *testing.T) {
	base := setupProfileServer(t)
	insertUser(t, "isoa", "userpass123")
	userB := insertUser(t, "isob", "userpass123")
	tokenA := loginUser(t, base, "isoa", "userpass123")

	// A 在请求体中伪造 user_id 指向 B：必须被忽略，只改 A。
	p := updateProfile(t, base, tokenA, map[string]any{"nickname": "A改名", "user_id": userB})
	if p.Id == userB {
		t.Fatalf("user_id in body must be ignored, got %+v", p)
	}
	bNickname, err := g.DB().Model("users").Ctx(context.Background()).Where("id", userB).Value("nickname")
	if err != nil {
		t.Fatalf("query B nickname: %v", err)
	}
	if bNickname.String() != "" {
		t.Fatalf("B profile must not be modified, got nickname=%q", bNickname.String())
	}

	// 未登录：GET/PUT 均 401/1002。
	for _, tc := range []struct {
		method string
		body   any
	}{
		{"GET", nil},
		{"PUT", map[string]any{"nickname": "x"}},
	} {
		res := doRequest(t, base, tc.method, "/profile", tc.body, nil)
		if res.Status != 401 || res.Code != 1002 {
			t.Fatalf("%s /profile without token: status=%d code=%d", tc.method, res.Status, res.Code)
		}
	}

	// 目标用户不存在：直接调用 service（绕过认证中间件）应返回 2015。
	_, err = service.Profile().Update(context.Background(), 999999999, &v1.UpdateReq{Nickname: strPtr("x")})
	if got := codes.FromError(err); got != codes.CodeUserNotFound {
		t.Fatalf("update nonexistent user: code=%d err=%v", got, err)
	}
}

func strPtr(s string) *string { return &s }
