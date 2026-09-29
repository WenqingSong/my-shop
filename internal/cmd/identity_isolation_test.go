package cmd

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定「前台用户 / 后台管理员」两个身份域之间的隔离边界。覆盖 task 中的 AC-001 至 AC-007。

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
	"golang.org/x/crypto/bcrypt"

	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	// 触发各 logic 包的 init()，注册 service.Iam / service.Admin。
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
)

const (
	isoJWTSecret     = "test-secret-0123456789-0123456789-0123456789" // >= 32 bytes
	isoAdminPassword = "test-admin-password-123"
	isoSuperUsername = "admin"
)

type isoResult struct {
	Status  int
	Code    int
	Message string
	Data    map[string]any
}

func isoDecode(r *gclient.Response) isoResult {
	var env struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	_ = json.Unmarshal(r.ReadAll(), &env)
	return isoResult{Status: r.StatusCode, Code: env.Code, Message: env.Message, Data: env.Data}
}

func isoRequest(base, method, path string, body any, headers map[string]string) (isoResult, error) {
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
	default:
		return isoResult{}, fmt.Errorf("unsupported method %s", method)
	}
	if err != nil {
		return isoResult{}, err
	}
	defer r.Close()
	return isoDecode(r), nil
}

func isoDo(t *testing.T, base, method, path string, body any, headers map[string]string) isoResult {
	t.Helper()
	res, err := isoRequest(base, method, path, body, headers)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return res
}

func isoAuthHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// setupIsolationServer 同时挂载前台与后台真实路由，返回 base URL。
// 清空前后台身份表并重建超级管理员、flush Redis，保证每个用例从一个干净且隔离的状态开始。
func setupIsolationServer(t *testing.T) string {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", isoJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", isoAdminPassword)

	ctx := context.Background()
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// 先 Bootstrap 确保表存在，再清空身份相关表，随后二次 Bootstrap 以测试密码重建超级管理员。
	for _, table := range []string{"users", "admin_roles", "role_permissions", "roles", "permissions", "admins"} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("re-bootstrap: %v", err)
	}
	if err := g.Redis().FlushDB(ctx); err != nil {
		t.Fatalf("clean redis: %v", err)
	}

	s := g.Server(guid.S())
	s.SetAddr(":0")
	s.Group("/", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.Response)
		RegisterFrontendRoutes(group)
		RegisterAdminRoutes(group)
	})
	s.SetDumpRouterMap(false)
	s.Start()
	t.Cleanup(func() { _ = s.Shutdown() })
	time.Sleep(100 * time.Millisecond)

	return fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
}

// isoInsertUser 直接写入 users 表（含真实 bcrypt 哈希），返回用户 id。
func isoInsertUser(t *testing.T, username, password string) int64 {
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

// isoInsertAdmin 直接写入 admins 表（普通管理员，is_super=0、status=1），返回管理员 id。
func isoInsertAdmin(t *testing.T, username, password string) int64 {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash admin password: %v", err)
	}
	id, err := g.DB().Model("admins").Ctx(context.Background()).Data(g.Map{
		"username":      username,
		"password_hash": string(hash),
		"status":        1,
		"is_super":      0,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert admin %s: %v", username, err)
	}
	return id
}

// isoClaims 解析 token 并返回其 Claims。
func isoClaims(t *testing.T, token string) *auth.Claims {
	t.Helper()
	claims, err := auth.ParseWithSecret([]byte(isoJWTSecret), token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	return claims
}

// isoFrontendLogin 前台登录成功并返回 (token, sid)。
func isoFrontendLogin(t *testing.T, base, username, password string) (string, string) {
	t.Helper()
	res := isoDo(t, base, "POST", "/api/v1/login", map[string]any{"username": username, "password": password}, nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("frontend login %s: status=%d code=%d msg=%q", username, res.Status, res.Code, res.Message)
	}
	token, _ := res.Data["access_token"].(string)
	if token == "" {
		t.Fatalf("frontend login %s: empty access_token", username)
	}
	return token, isoClaims(t, token).Sid
}

// isoAdminLogin 后台登录成功并返回 (token, sid)。
func isoAdminLogin(t *testing.T, base, username, password string) (string, string) {
	t.Helper()
	res := isoDo(t, base, "POST", "/admin/v1/login", map[string]any{"username": username, "password": password}, nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("admin login %s: status=%d code=%d msg=%q", username, res.Status, res.Code, res.Message)
	}
	token, _ := res.Data["access_token"].(string)
	if token == "" {
		t.Fatalf("admin login %s: empty access_token", username)
	}
	return token, isoClaims(t, token).Sid
}

// isoSessionKeys 返回匹配 pattern 的 Redis key 列表。
func isoSessionKeys(t *testing.T, pattern string) []string {
	t.Helper()
	keys, err := g.Redis().Keys(context.Background(), pattern)
	if err != nil {
		t.Fatalf("redis keys %s: %v", pattern, err)
	}
	return keys
}

func isoUserSessionFields(t *testing.T, sid string) map[string]string {
	t.Helper()
	v, err := g.Redis().HGetAll(context.Background(), auth.SessionKey(sid))
	if err != nil {
		t.Fatalf("hgetall user session: %v", err)
	}
	return v.MapStrStr()
}

func isoAdminSessionFields(t *testing.T, sid string) map[string]string {
	t.Helper()
	v, err := g.Redis().HGetAll(context.Background(), auth.AdminSessionKey(sid))
	if err != nil {
		t.Fatalf("hgetall admin session: %v", err)
	}
	return v.MapStrStr()
}

// TestIdentityDomainLoginSourceIsolation 覆盖 AC-001/AC-002/AC-007：
// 前台登录只查 users、后台登录只查 admins，且不存在公开管理员注册入口。
func TestIdentityDomainLoginSourceIsolation(t *testing.T) {
	base := setupIsolationServer(t)

	// AC-001：只存在于 admins 的账号调用前台登录 → 401/2002，且不创建前台 session。
	isoInsertAdmin(t, "adminonly", "adminpass123")
	res := isoDo(t, base, "POST", "/api/v1/login", map[string]any{"username": "adminonly", "password": "adminpass123"}, nil)
	if res.Status != 401 || res.Code != 2002 {
		t.Fatalf("frontend login with admin-only account: status=%d code=%d", res.Status, res.Code)
	}
	if res.Data != nil {
		t.Fatalf("frontend login with admin-only account: expected data=null, got %v", res.Data)
	}
	if keys := isoSessionKeys(t, "iam:session:*"); len(keys) != 0 {
		t.Fatalf("frontend login with admin-only account must not create frontend session, got %v", keys)
	}

	// AC-002：只存在于 users 的账号调用后台登录 → 401/2002，且不创建管理员 session。
	isoInsertUser(t, "useronly", "userpass123")
	res = isoDo(t, base, "POST", "/admin/v1/login", map[string]any{"username": "useronly", "password": "userpass123"}, nil)
	if res.Status != 401 || res.Code != 2002 {
		t.Fatalf("admin login with user-only account: status=%d code=%d", res.Status, res.Code)
	}
	if res.Data != nil {
		t.Fatalf("admin login with user-only account: expected data=null, got %v", res.Data)
	}
	if keys := isoSessionKeys(t, "iam:admin:session:*"); len(keys) != 0 {
		t.Fatalf("admin login with user-only account must not create admin session, got %v", keys)
	}

	// AC-007：不存在公开管理员注册入口，POST /admin/v1/register → 404。
	res = isoDo(t, base, "POST", "/admin/v1/register", map[string]any{"username": "x", "password": "password123"}, nil)
	if res.Status != 404 {
		t.Fatalf("admin register should be 404, got status=%d code=%d", res.Status, res.Code)
	}
}

// TestIdentityDomainSameNameDifferentPasswords 覆盖 AC-003：
// 同名 username 在两表各自独立，登录结果由目标接口对应的数据表决定。
func TestIdentityDomainSameNameDifferentPasswords(t *testing.T) {
	base := setupIsolationServer(t)

	const (
		username = "shared"
		userPwd  = "shared-user-password"
		adminPwd = "shared-admin-password"
	)
	isoInsertUser(t, username, userPwd)
	isoInsertAdmin(t, username, adminPwd)

	// 前台登录 + 前台密码 → 成功，type=user。
	token, _ := isoFrontendLogin(t, base, username, userPwd)
	if typ := isoClaims(t, token).Type; typ != auth.TypeUser {
		t.Fatalf("frontend login with user password: expected type=%q, got %q", auth.TypeUser, typ)
	}

	// 前台登录 + 后台密码 → 认证失败。
	res := isoDo(t, base, "POST", "/api/v1/login", map[string]any{"username": username, "password": adminPwd}, nil)
	if res.Status != 401 || res.Code != 2002 {
		t.Fatalf("frontend login with admin password: status=%d code=%d", res.Status, res.Code)
	}

	// 后台登录 + 后台密码 → 成功，type=admin。
	adminToken, _ := isoAdminLogin(t, base, username, adminPwd)
	if typ := isoClaims(t, adminToken).Type; typ != auth.TypeAdmin {
		t.Fatalf("admin login with admin password: expected type=%q, got %q", auth.TypeAdmin, typ)
	}

	// 后台登录 + 前台密码 → 认证失败。
	res = isoDo(t, base, "POST", "/admin/v1/login", map[string]any{"username": username, "password": userPwd}, nil)
	if res.Status != 401 || res.Code != 2002 {
		t.Fatalf("admin login with user password: status=%d code=%d", res.Status, res.Code)
	}
}

// TestIdentityDomainTokenTypeIsolation 覆盖 AC-004：
// type=user token 打后台、type=admin token 打前台，均通过真实路由 + 中间件返回 403/1003。
func TestIdentityDomainTokenTypeIsolation(t *testing.T) {
	base := setupIsolationServer(t)

	isoInsertUser(t, "userA", "userpass123")
	userToken, _ := isoFrontendLogin(t, base, "userA", "userpass123")
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// type=user token 访问后台受保护接口 → 403/1003。
	res := isoDo(t, base, "GET", "/admin/v1/me", nil, isoAuthHeader(userToken))
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("user token on /admin/v1/me: status=%d code=%d", res.Status, res.Code)
	}

	// type=admin token 访问前台受保护接口 → 403/1003。
	res = isoDo(t, base, "GET", "/api/v1/me", nil, isoAuthHeader(adminToken))
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("admin token on /api/v1/me: status=%d code=%d", res.Status, res.Code)
	}
}

// TestIdentityDomainSessionKeyIsolation 覆盖 AC-005：
// 前台登录只创建 iam:session:{sid}，后台登录只创建 iam:admin:session:{sid}，互不越界。
func TestIdentityDomainSessionKeyIsolation(t *testing.T) {
	base := setupIsolationServer(t)

	isoInsertUser(t, "userA", "userpass123")
	_, userSid := isoFrontendLogin(t, base, "userA", "userpass123")

	// 前台登录只创建本域 key。
	if n, err := g.Redis().Exists(context.Background(), auth.SessionKey(userSid)); err != nil || n != 1 {
		t.Fatalf("frontend session key should exist: n=%d err=%v", n, err)
	}
	if n, err := g.Redis().Exists(context.Background(), auth.AdminSessionKey(userSid)); err != nil || n != 0 {
		t.Fatalf("frontend login must not create admin session key: n=%d err=%v", n, err)
	}
	if keys := isoSessionKeys(t, "iam:admin:session:*"); len(keys) != 0 {
		t.Fatalf("frontend login must not create any admin session key, got %v", keys)
	}

	// 后台登录只创建本域 key，不新增前台 session。
	_, adminSid := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	if n, err := g.Redis().Exists(context.Background(), auth.AdminSessionKey(adminSid)); err != nil || n != 1 {
		t.Fatalf("admin session key should exist: n=%d err=%v", n, err)
	}
	if n, err := g.Redis().Exists(context.Background(), auth.SessionKey(adminSid)); err != nil || n != 0 {
		t.Fatalf("admin login must not create frontend session key: n=%d err=%v", n, err)
	}
	// 前台 session 集合应仍只有一个（userA 的），后台登录未新增前台 session。
	if keys := isoSessionKeys(t, "iam:session:*"); len(keys) != 1 {
		t.Fatalf("admin login must not create frontend session key, got %v", keys)
	}
}

// TestAdminLogoutFullChain 覆盖 AC-006 主流程：
// 管理员登出真实撤销管理员会话且幂等，原 token 立即失效，且不影响前台用户 session。
func TestAdminLogoutFullChain(t *testing.T) {
	base := setupIsolationServer(t)

	// 前台用户，用于验证管理员登出不影响前台 session。
	isoInsertUser(t, "userA", "userpass123")
	userToken, userSid := isoFrontendLogin(t, base, "userA", "userpass123")

	// 管理员登录。
	adminToken, adminSid := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// (1) 登出前 /admin/v1/me 可用。
	me := isoDo(t, base, "GET", "/admin/v1/me", nil, isoAuthHeader(adminToken))
	if me.Status != 200 || me.Code != 0 {
		t.Fatalf("admin me before logout: status=%d code=%d", me.Status, me.Code)
	}

	// (2) 调用 /admin/v1/logout 成功，data=null。
	lo := isoDo(t, base, "POST", "/admin/v1/logout", nil, isoAuthHeader(adminToken))
	if lo.Status != 200 || lo.Code != 0 {
		t.Fatalf("admin logout: status=%d code=%d", lo.Status, lo.Code)
	}
	if lo.Data != nil {
		t.Fatalf("admin logout should return data=null, got %v", lo.Data)
	}

	// (3) 对应管理员 session 被撤销。
	if fields := isoAdminSessionFields(t, adminSid); fields["revoked"] != "1" {
		t.Fatalf("expected admin session revoked=1, got %q", fields["revoked"])
	}

	// (4) 原 token 再访问 /admin/v1/me 失败。
	me = isoDo(t, base, "GET", "/admin/v1/me", nil, isoAuthHeader(adminToken))
	if me.Status != 401 || me.Code != 1002 {
		t.Fatalf("admin me after logout: status=%d code=%d", me.Status, me.Code)
	}

	// (5) 再次登出保持幂等。
	lo = isoDo(t, base, "POST", "/admin/v1/logout", nil, isoAuthHeader(adminToken))
	if lo.Status != 200 || lo.Code != 0 {
		t.Fatalf("admin repeat logout: status=%d code=%d", lo.Status, lo.Code)
	}

	// (6) 不影响前台用户 session。
	meU := isoDo(t, base, "GET", "/api/v1/me", nil, isoAuthHeader(userToken))
	if meU.Status != 200 || meU.Code != 0 {
		t.Fatalf("frontend me after admin logout: status=%d code=%d", meU.Status, meU.Code)
	}
	if fields := isoUserSessionFields(t, userSid); fields["revoked"] != "0" {
		t.Fatalf("frontend session should not be revoked by admin logout, got %q", fields["revoked"])
	}
}

// TestAdminLogoutRedisErrorReturns500 覆盖 AC-006 Redis 故障场景：
// Redis 查询/写失败时不得伪装成登出成功，必须返回 500/1000（不吞错误）。
func TestAdminLogoutRedisErrorReturns500(t *testing.T) {
	base := setupIsolationServer(t)

	adminToken, adminSid := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 将管理员 session key 改成 string 类型，使撤销脚本里的 HSET 返回 WRONGTYPE 错误，
	// 模拟 Redis 操作失败。
	if _, err := g.Redis().Do(context.Background(), "SET", auth.AdminSessionKey(adminSid), "not-a-hash"); err != nil {
		t.Fatalf("corrupt admin session key: %v", err)
	}

	lo := isoDo(t, base, "POST", "/admin/v1/logout", nil, isoAuthHeader(adminToken))
	if lo.Status != 500 || lo.Code != 1000 {
		t.Fatalf("admin logout with redis error: expected 500/1000, got status=%d code=%d", lo.Status, lo.Code)
	}
}
