package middleware_test

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

	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
)

const (
	testJWTSecret     = "test-secret-0123456789-0123456789-0123456789"
	testAdminPassword = "test-admin-password-123"
	testSid           = "deadbeefdeadbeefdeadbeefdeadbeef"
)

type apiResult struct {
	Status int
	Code   int
	Body   map[string]any
}

func decode(r *gclient.Response) apiResult {
	var env map[string]any
	_ = json.Unmarshal(r.ReadAll(), &env)
	code := 0
	if c, ok := env["code"].(float64); ok {
		code = int(c)
	}
	return apiResult{Status: r.StatusCode, Code: code, Body: env}
}

func doRequest(t *testing.T, base, path string, headers map[string]string) apiResult {
	t.Helper()
	c := g.Client()
	if len(headers) > 0 {
		c = c.Header(headers)
	}
	r, err := c.Get(context.Background(), base+path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer r.Close()
	return decode(r)
}

// ---- handlers ----

func userMeHandler(r *ghttp.Request) {
	p, _ := middleware.PrincipalFromContext(r.Context())
	r.Response.WriteJson(g.Map{"user_id": p.UserID})
}

func adminMeHandler(r *ghttp.Request) {
	p, _ := middleware.AdminPrincipalFromContext(r.Context())
	r.Response.WriteJson(g.Map{"admin_id": p.AdminID, "is_super": p.IsSuper})
}

func rolesHandler(r *ghttp.Request) {
	r.Response.WriteJson(g.Map{"ok": true})
}

// ---- setup ----

func setupMiddlewareServer(t *testing.T) string {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)

	ctx := context.Background()
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for _, table := range []string{"admins", "roles", "permissions", "admin_roles", "role_permissions"} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}

	s := g.Server(guid.S())
	s.SetAddr(":0")
	s.Group("/", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.Response)
		group.Group("/", func(user *ghttp.RouterGroup) {
			user.Middleware(middleware.Auth)
			user.GET("/me", userMeHandler)
		})
		group.Group("/admin", func(admin *ghttp.RouterGroup) {
			admin.Middleware(middleware.AdminAuth)
			admin.GET("/me", adminMeHandler)
			admin.Group("/", func(roles *ghttp.RouterGroup) {
				roles.Middleware(middleware.RequirePermission("role:list"))
				roles.GET("/roles", rolesHandler)
			})
		})
	})
	s.SetDumpRouterMap(false)
	s.Start()
	t.Cleanup(func() { _ = s.Shutdown() })
	time.Sleep(100 * time.Millisecond)
	return fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
}

// ---- helpers ----

func insertAdmin(t *testing.T, username string, isSuper, status int) int64 {
	t.Helper()
	id, err := g.DB().Model("admins").Ctx(context.Background()).Data(g.Map{
		"username":      username,
		"password_hash": "test-hash",
		"status":        status,
		"is_super":      isSuper,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	return id
}

func signToken(t *testing.T, typ string, id int64, sid string) string {
	t.Helper()
	token, err := auth.GenerateWithSecret([]byte(testJWTSecret), typ, id, sid)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func createAdminSession(t *testing.T, sid string, adminID int64) {
	t.Helper()
	if err := auth.CreateAdminSession(context.Background(), sid, adminID, 3600); err != nil {
		t.Fatalf("create admin session: %v", err)
	}
}

func grantPermission(t *testing.T, adminID int64, code string) {
	t.Helper()
	ctx := context.Background()
	pid, err := g.DB().Model("permissions").Ctx(ctx).Data(g.Map{"code": code, "name": code}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert permission: %v", err)
	}
	rid, err := g.DB().Model("roles").Ctx(ctx).Data(g.Map{"name": "role-" + code}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert role: %v", err)
	}
	if _, err := g.DB().Model("role_permissions").Ctx(ctx).Data(g.Map{"role_id": rid, "permission_id": pid}).Insert(); err != nil {
		t.Fatalf("insert role_permission: %v", err)
	}
	if _, err := g.DB().Model("admin_roles").Ctx(ctx).Data(g.Map{"admin_id": adminID, "role_id": rid}).Insert(); err != nil {
		t.Fatalf("insert admin_role: %v", err)
	}
}

func authHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// ---- tests ----

// TestAdminAuthRequiresToken 覆盖 AC-013：无 token 访问后台 → 401。
func TestAdminAuthRequiresToken(t *testing.T) {
	base := setupMiddlewareServer(t)
	res := doRequest(t, base, "/admin/me", nil)
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("no token: status=%d code=%d", res.Status, res.Code)
	}
}

// TestAdminAuthRejectsUserToken 覆盖 AC-014/INV-005：前台用户 token 访问后台 → 403。
func TestAdminAuthRejectsUserToken(t *testing.T) {
	base := setupMiddlewareServer(t)
	token := signToken(t, auth.TypeUser, 1, testSid)
	res := doRequest(t, base, "/admin/me", authHeader(token))
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("user token: status=%d code=%d", res.Status, res.Code)
	}
}

// TestAdminAuthAcceptsAdminToken 覆盖 AC-016：合法管理员 token → 200，注入 AdminID/IsSuper。
func TestAdminAuthAcceptsAdminToken(t *testing.T) {
	base := setupMiddlewareServer(t)
	adminID := insertAdmin(t, "alice-admin", 0, 1)
	createAdminSession(t, testSid, adminID)
	token := signToken(t, auth.TypeAdmin, adminID, testSid)

	res := doRequest(t, base, "/admin/me", authHeader(token))
	if res.Status != 200 {
		t.Fatalf("admin token: status=%d code=%d", res.Status, res.Code)
	}
	if got := res.Body["admin_id"].(float64); int64(got) != adminID {
		t.Fatalf("expected admin_id=%d, got %v", adminID, res.Body["admin_id"])
	}
	if got, _ := res.Body["is_super"].(bool); got {
		t.Fatalf("expected is_super=false for normal admin, got true")
	}
}

// TestAdminAuthRejectsDisabledAdmin 覆盖 AC-005：禁用管理员 → 401。
func TestAdminAuthRejectsDisabledAdmin(t *testing.T) {
	base := setupMiddlewareServer(t)
	adminID := insertAdmin(t, "disabled-admin", 0, 0)
	createAdminSession(t, testSid, adminID)
	token := signToken(t, auth.TypeAdmin, adminID, testSid)

	res := doRequest(t, base, "/admin/me", authHeader(token))
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("disabled admin: status=%d code=%d", res.Status, res.Code)
	}
}

// TestAdminAuthRejectsMissingSession 覆盖 AC-013：token 验签通过但 admin 会话缺失 → 401。
func TestAdminAuthRejectsMissingSession(t *testing.T) {
	base := setupMiddlewareServer(t)
	adminID := insertAdmin(t, "no-session-admin", 0, 1)
	token := signToken(t, auth.TypeAdmin, adminID, "deadbeefdeadbeefdeadbeefdead0000")

	res := doRequest(t, base, "/admin/me", authHeader(token))
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("missing session: status=%d code=%d", res.Status, res.Code)
	}
}

// TestUserAuthRejectsAdminToken 覆盖 INV-005：管理员 token 访问用户侧 → 403。
func TestUserAuthRejectsAdminToken(t *testing.T) {
	base := setupMiddlewareServer(t)
	token := signToken(t, auth.TypeAdmin, 1, testSid)
	res := doRequest(t, base, "/me", authHeader(token))
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("admin token on user route: status=%d code=%d", res.Status, res.Code)
	}
}

// TestRequirePermissionSuperAdminBypass 覆盖 AC-017：超级管理员无显式权限也放行。
func TestRequirePermissionSuperAdminBypass(t *testing.T) {
	base := setupMiddlewareServer(t)
	superID := insertAdmin(t, "root-admin", 1, 1)
	createAdminSession(t, testSid, superID)
	token := signToken(t, auth.TypeAdmin, superID, testSid)

	res := doRequest(t, base, "/admin/roles", authHeader(token))
	if res.Status != 200 {
		t.Fatalf("super admin: status=%d code=%d", res.Status, res.Code)
	}
}

// TestRequirePermissionWithPermission 覆盖 AC-016：有权限管理员放行。
func TestRequirePermissionWithPermission(t *testing.T) {
	base := setupMiddlewareServer(t)
	adminID := insertAdmin(t, "granted-admin", 0, 1)
	grantPermission(t, adminID, "role:list")
	createAdminSession(t, testSid, adminID)
	token := signToken(t, auth.TypeAdmin, adminID, testSid)

	res := doRequest(t, base, "/admin/roles", authHeader(token))
	if res.Status != 200 {
		t.Fatalf("granted admin: status=%d code=%d", res.Status, res.Code)
	}
}

// TestRequirePermissionWithoutPermission 覆盖 AC-015/AC-018：无权限管理员 → 403。
func TestRequirePermissionWithoutPermission(t *testing.T) {
	base := setupMiddlewareServer(t)
	adminID := insertAdmin(t, "no-perm-admin", 0, 1)
	createAdminSession(t, testSid, adminID)
	token := signToken(t, auth.TypeAdmin, adminID, testSid)

	res := doRequest(t, base, "/admin/roles", authHeader(token))
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("no permission: status=%d code=%d", res.Status, res.Code)
	}
}
