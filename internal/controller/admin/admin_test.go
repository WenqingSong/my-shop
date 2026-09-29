package admin_test

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
	"cnb.cool/go-cloud-devops/my-shop/internal/cmd"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
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

// setupAdminServer 清空 RBAC 表、Bootstrap（重新 seed 超级管理员与权限）、
// 挂载真实 admin 路由（复用 cmd.RegisterAdminRoutes），返回 base 与超级管理员 token。
func setupAdminServer(t *testing.T) (base, superToken string) {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)

	ctx := context.Background()
	// 先 Bootstrap 一次确保 RBAC 表存在（幂等建表），再清空以测试密码重建超级管理员。
	// 避免在全新数据库（每日重置）上「先删后建」因表不存在而报 1146。
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// 清空全部 RBAC 表，保证超级管理员由 Bootstrap 以测试密码重新创建（幂等，不覆盖已有密码）。
	for _, table := range []string{"admin_roles", "role_permissions", "roles", "permissions", "admins"} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// Bootstrap 后保留超级管理员，清空测试按需创建的角色/权限。
	for _, table := range []string{"admin_roles", "role_permissions", "roles", "permissions"} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}

	s := g.Server(guid.S())
	s.SetAddr(":0")
	s.Group("/", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.Response)
		cmd.RegisterAdminRoutes(group)
	})
	s.SetDumpRouterMap(false)
	s.Start()
	t.Cleanup(func() { _ = s.Shutdown() })
	time.Sleep(100 * time.Millisecond)

	base = fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
	superToken = loginAdmin(t, base, testSuperUsername, testAdminPassword)
	return base, superToken
}

func loginAdmin(t *testing.T, base, username, password string) string {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/v1/login", map[string]any{"username": username, "password": password}, nil)
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

func createAdmin(t *testing.T, base, token, username, password string) int64 {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/v1/admins", map[string]any{"username": username, "password": password}, authHeader(token))
	assertOK(t, res, "create admin "+username)
	var d struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal create admin res: %v", err)
	}
	if d.Id <= 0 {
		t.Fatalf("expected positive admin id, got %d", d.Id)
	}
	return d.Id
}

func createRole(t *testing.T, base, token, name string) int64 {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/v1/roles", map[string]any{"name": name, "description": "desc-" + name}, authHeader(token))
	assertOK(t, res, "create role "+name)
	var d struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal create role res: %v", err)
	}
	return d.Id
}

func createPermission(t *testing.T, base, token, code, name string) int64 {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/v1/permissions", map[string]any{"code": code, "name": name}, authHeader(token))
	assertOK(t, res, "create permission "+code)
	var d struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal create permission res: %v", err)
	}
	return d.Id
}

// ensurePermission 返回指定 code 的权限 id：优先复用已存在的（可能被启动 seed 或并行测试包创建），
// 否则经 API 创建；创建遇到 2010（并发下被抢先创建）时重查复用。
// 用于授权矩阵等「必须使用 seed 标准权限 code」的场景，避免与共享 DB 的并发 seed 在唯一约束上冲突。
func ensurePermission(t *testing.T, base, token, code, name string) int64 {
	t.Helper()
	if v, err := g.DB().Model("permissions").Ctx(context.Background()).Where("code", code).Value("id"); err != nil {
		t.Fatalf("query permission %s: %v", code, err)
	} else if v != nil && !v.IsEmpty() {
		return v.Int64()
	}

	res := doRequest(t, base, "POST", "/admin/v1/permissions", map[string]any{"code": code, "name": name}, authHeader(token))
	if res.Status == 200 && res.Code == 0 {
		var d struct {
			Id int64 `json:"id"`
		}
		if err := json.Unmarshal(res.Data, &d); err != nil {
			t.Fatalf("unmarshal create permission res: %v", err)
		}
		return d.Id
	}
	if res.Code == 2010 {
		// 并发下被 seed 抢先创建，重查复用。
		if v, err := g.DB().Model("permissions").Ctx(context.Background()).Where("code", code).Value("id"); err != nil {
			t.Fatalf("re-query permission %s: %v", code, err)
		} else if v != nil && !v.IsEmpty() {
			return v.Int64()
		}
	}
	t.Fatalf("create permission %s: status=%d code=%d msg=%q", code, res.Status, res.Code, res.Message)
	return 0
}

func dbCount(t *testing.T, table string, where map[string]any) int {
	t.Helper()
	m := g.DB().Model(table).Ctx(context.Background())
	for k, v := range where {
		m = m.Where(k, v)
	}
	n, err := m.Count()
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func adminIsSuper(t *testing.T, id int64) int {
	t.Helper()
	v, err := g.DB().Model("admins").Ctx(context.Background()).Where("id", id).Value("is_super")
	if err != nil {
		t.Fatalf("query is_super: %v", err)
	}
	return v.Int()
}

func adminStatus(t *testing.T, id int64) int {
	t.Helper()
	v, err := g.DB().Model("admins").Ctx(context.Background()).Where("id", id).Value("status")
	if err != nil {
		t.Fatalf("query status: %v", err)
	}
	return v.Int()
}

// TestAdminLogin 覆盖 AC-002：正确凭据登录签发 type=admin token；错误密码与不存在管理员统一返回 2002（防枚举）。
func TestAdminLogin(t *testing.T) {
	base, _ := setupAdminServer(t)

	res := doRequest(t, base, "POST", "/admin/v1/login", map[string]any{"username": testSuperUsername, "password": testAdminPassword}, nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("login success: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	var d struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal login res: %v", err)
	}
	if d.AccessToken == "" || d.TokenType != "Bearer" || d.ExpiresIn != auth.ExpiresIn {
		t.Fatalf("unexpected login data: %+v", d)
	}
	claims, err := auth.ParseWithSecret([]byte(testJWTSecret), d.AccessToken)
	if err != nil {
		t.Fatalf("parse admin token: %v", err)
	}
	if claims.Type != auth.TypeAdmin {
		t.Fatalf("expected token type %q, got %q", auth.TypeAdmin, claims.Type)
	}
	if claims.Subject == "" || claims.Sid == "" {
		t.Fatalf("expected non-empty sub/sid, got sub=%q sid=%q", claims.Subject, claims.Sid)
	}

	// 错误密码与不存在管理员返回相同的 code/message（防枚举）。
	wrong := doRequest(t, base, "POST", "/admin/v1/login", map[string]any{"username": testSuperUsername, "password": "wrongpass123"}, nil)
	ghost := doRequest(t, base, "POST", "/admin/v1/login", map[string]any{"username": "ghost", "password": testAdminPassword}, nil)
	if wrong.Status != 401 || wrong.Code != 2002 {
		t.Fatalf("wrong password: status=%d code=%d", wrong.Status, wrong.Code)
	}
	if ghost.Status != wrong.Status || ghost.Code != wrong.Code {
		t.Fatalf("anti-enumeration mismatch: wrong=%+v ghost=%+v", wrong, ghost)
	}
}

// TestAdminMe 覆盖 AC-003/AC-013：/admin/me 返回 id/username/is_super/roles；无 token 返回 401。
func TestAdminMe(t *testing.T) {
	base, superToken := setupAdminServer(t)

	res := doRequest(t, base, "GET", "/admin/v1/me", nil, authHeader(superToken))
	assertOK(t, res, "me")
	var d struct {
		Id       int64    `json:"id"`
		Username string   `json:"username"`
		IsSuper  bool     `json:"is_super"`
		Roles    []string `json:"roles"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal me res: %v", err)
	}
	if d.Username != testSuperUsername || !d.IsSuper || d.Id <= 0 {
		t.Fatalf("unexpected me data: %+v", d)
	}
	if len(d.Roles) != 0 {
		t.Fatalf("expected no roles for fresh super admin, got %v", d.Roles)
	}

	// 无 token 访问 → 401。
	noToken := doRequest(t, base, "GET", "/admin/v1/me", nil, nil)
	if noToken.Status != 401 || noToken.Code != 1002 {
		t.Fatalf("me without token: status=%d code=%d", noToken.Status, noToken.Code)
	}
}

// grantRole 创建带指定权限的角色并分配给管理员，返回角色 id。
// 用于让普通管理员持有管理权限，从而触达逻辑层的自操作/超级管理员防护（否则会被 RequirePermission 先行 403）。
func grantRole(t *testing.T, base, token string, adminID int64, roleName string, permCodes []string) int64 {
	t.Helper()
	roleID := createRole(t, base, token, roleName)
	for _, code := range permCodes {
		permID := ensurePermission(t, base, token, code, code)
		res := doRequest(t, base, "POST", fmt.Sprintf("/admin/v1/roles/%d/permissions", roleID), map[string]any{"permission_id": permID}, authHeader(token))
		assertOK(t, res, "assign permission "+code)
	}
	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/v1/admins/%d/roles", adminID), map[string]any{"role_id": roleID}, authHeader(token))
	assertOK(t, res, "assign role "+roleName)
	return roleID
}

// TestAdminCreateDeleteAndProtection 覆盖 AC-004/006/007/008：
// 创建/删除普通管理员、无公开注册入口、超级管理员受保护、普通管理员不能操作自身。
func TestAdminCreateDeleteAndProtection(t *testing.T) {
	base, superToken := setupAdminServer(t)
	superH := authHeader(superToken)

	// 通过 /admin/me 获取超级管理员 id。
	me := doRequest(t, base, "GET", "/admin/v1/me", nil, superH)
	var meData struct {
		Id int64 `json:"id"`
	}
	_ = json.Unmarshal(me.Data, &meData)
	superID := meData.Id

	// AC-004：不存在公开注册入口（/admin/register → 404，路由不存在）。
	reg := doRequest(t, base, "POST", "/admin/v1/register", map[string]any{"username": "x", "password": "password123"}, nil)
	if reg.Status != 404 {
		t.Fatalf("admin register should be 404, got status=%d code=%d", reg.Status, reg.Code)
	}

	// AC-004：超级管理员创建普通管理员。
	aliceID := createAdmin(t, base, superToken, "alice", "alicepass123")
	if adminIsSuper(t, aliceID) != 0 {
		t.Fatalf("created admin should not be super")
	}

	// 给 alice 授予管理员管理权限，使其能触达自操作防护（2006）。
	managerRoleID := grantRole(t, base, superToken, aliceID, "manager",
		[]string{"admin:disable", "admin:delete", "admin:assign_role"})

	// AC-007：超级管理员不可被禁用/删除/改角色，返回 2005 且无写入。
	res := doRequest(t, base, "PUT", fmt.Sprintf("/admin/v1/admins/%d/status", superID), map[string]any{"status": 0}, superH)
	if res.Status != 403 || res.Code != 2005 {
		t.Fatalf("disable super: status=%d code=%d", res.Status, res.Code)
	}
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/admin/v1/admins/%d", superID), nil, superH)
	if res.Status != 403 || res.Code != 2005 {
		t.Fatalf("delete super: status=%d code=%d", res.Status, res.Code)
	}
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/v1/admins/%d/roles", superID), map[string]any{"role_id": managerRoleID}, superH)
	if res.Status != 403 || res.Code != 2005 {
		t.Fatalf("assign role to super: status=%d code=%d", res.Status, res.Code)
	}
	if adminIsSuper(t, superID) != 1 || adminStatus(t, superID) != 1 {
		t.Fatalf("super admin should remain unchanged (is_super=1, status=1)")
	}

	// AC-008：普通管理员不能操作自身，返回 2006 且无写入（即使持有对应权限）。
	aliceToken := loginAdmin(t, base, "alice", "alicepass123")
	aliceH := authHeader(aliceToken)
	res = doRequest(t, base, "PUT", fmt.Sprintf("/admin/v1/admins/%d/status", aliceID), map[string]any{"status": 0}, aliceH)
	if res.Status != 403 || res.Code != 2006 {
		t.Fatalf("self disable: status=%d code=%d", res.Status, res.Code)
	}
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/admin/v1/admins/%d", aliceID), nil, aliceH)
	if res.Status != 403 || res.Code != 2006 {
		t.Fatalf("self delete: status=%d code=%d", res.Status, res.Code)
	}
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/v1/admins/%d/roles", aliceID), map[string]any{"role_id": managerRoleID}, aliceH)
	if res.Status != 403 || res.Code != 2006 {
		t.Fatalf("self assign role: status=%d code=%d", res.Status, res.Code)
	}
	if adminStatus(t, aliceID) != 1 {
		t.Fatalf("alice should remain enabled after self-operation refused")
	}

	// 普通管理员可操作他人（非自身、非超级），验证防护仅针对自身。
	bobID := createAdmin(t, base, superToken, "bob", "bobpass123")
	res = doRequest(t, base, "PUT", fmt.Sprintf("/admin/v1/admins/%d/status", bobID), map[string]any{"status": 0}, aliceH)
	assertOK(t, res, "alice disable bob")

	// AC-006：超级管理员删除普通管理员成功。
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/admin/v1/admins/%d", aliceID), nil, superH)
	assertOK(t, res, "delete alice")
	if n := dbCount(t, "admins", map[string]any{"id": aliceID}); n != 0 {
		t.Fatalf("alice should be deleted, got %d rows", n)
	}
}

// TestAdminDisableImmediateRevocation 覆盖 AC-005：禁用管理员后旧 token 立即失效（401），重新登录也被拒。
func TestAdminDisableImmediateRevocation(t *testing.T) {
	base, superToken := setupAdminServer(t)
	superH := authHeader(superToken)

	aliceID := createAdmin(t, base, superToken, "alice", "alicepass123")
	aliceToken := loginAdmin(t, base, "alice", "alicepass123")

	// 禁用前 /admin/me 可用。
	me := doRequest(t, base, "GET", "/admin/v1/me", nil, authHeader(aliceToken))
	assertOK(t, me, "me before disable")

	// 超级管理员禁用 alice。
	res := doRequest(t, base, "PUT", fmt.Sprintf("/admin/v1/admins/%d/status", aliceID), map[string]any{"status": 0}, superH)
	assertOK(t, res, "disable alice")

	// 旧 token 立即失效 → 401。
	after := doRequest(t, base, "GET", "/admin/v1/me", nil, authHeader(aliceToken))
	if after.Status != 401 || after.Code != 1002 {
		t.Fatalf("me after disable: status=%d code=%d", after.Status, after.Code)
	}

	// 重新登录也被拒 → 401（禁用）。
	relogin := doRequest(t, base, "POST", "/admin/v1/login", map[string]any{"username": "alice", "password": "alicepass123"}, nil)
	if relogin.Status != 401 {
		t.Fatalf("relogin disabled admin: status=%d code=%d", relogin.Status, relogin.Code)
	}

	// 重新启用后可再次登录。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/admin/v1/admins/%d/status", aliceID), map[string]any{"status": 1}, superH)
	assertOK(t, res, "re-enable alice")
	_ = loginAdmin(t, base, "alice", "alicepass123")
}

// TestRolePermissionManagement 覆盖 AC-009/010/011/012：角色/权限 CRUD 与角色↔权限、管理员↔角色分配。
func TestRolePermissionManagement(t *testing.T) {
	base, superToken := setupAdminServer(t)
	superH := authHeader(superToken)

	// AC-009：角色创建/列表/更新/删除。
	roleID := createRole(t, base, superToken, "editor")
	dup := doRequest(t, base, "POST", "/admin/v1/roles", map[string]any{"name": "editor"}, superH)
	if dup.Status != 409 || dup.Code != 2008 {
		t.Fatalf("duplicate role: status=%d code=%d", dup.Status, dup.Code)
	}
	list := doRequest(t, base, "GET", "/admin/v1/roles", nil, superH)
	assertOK(t, list, "list roles")
	var listData struct {
		Items []struct {
			Id   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Data, &listData); err != nil {
		t.Fatalf("unmarshal list roles: %v", err)
	}
	if len(listData.Items) != 1 || listData.Items[0].Id != roleID || listData.Items[0].Name != "editor" {
		t.Fatalf("unexpected role list: %+v", listData.Items)
	}
	upd := doRequest(t, base, "PUT", fmt.Sprintf("/admin/v1/roles/%d", roleID), map[string]any{"name": "editor-v2"}, superH)
	assertOK(t, upd, "update role")

	// AC-010：权限创建/列表/更新。
	permID := createPermission(t, base, superToken, "article:create", "创建文章")
	dupPerm := doRequest(t, base, "POST", "/admin/v1/permissions", map[string]any{"code": "article:create", "name": "重复"}, superH)
	if dupPerm.Status != 409 || dupPerm.Code != 2010 {
		t.Fatalf("duplicate permission: status=%d code=%d", dupPerm.Status, dupPerm.Code)
	}

	// AC-011：角色↔权限分配/移除。
	assign := doRequest(t, base, "POST", fmt.Sprintf("/admin/v1/roles/%d/permissions", roleID), map[string]any{"permission_id": permID}, superH)
	assertOK(t, assign, "assign role permission")
	// 重复分配幂等。
	assign = doRequest(t, base, "POST", fmt.Sprintf("/admin/v1/roles/%d/permissions", roleID), map[string]any{"permission_id": permID}, superH)
	assertOK(t, assign, "assign role permission again")

	// AC-012：管理员↔角色分配/移除。
	aliceID := createAdmin(t, base, superToken, "alice", "alicepass123")
	assignRole := doRequest(t, base, "POST", fmt.Sprintf("/admin/v1/admins/%d/roles", aliceID), map[string]any{"role_id": roleID}, superH)
	assertOK(t, assignRole, "assign admin role")

	// /admin/me 展示所属角色。
	aliceToken := loginAdmin(t, base, "alice", "alicepass123")
	me := doRequest(t, base, "GET", "/admin/v1/me", nil, authHeader(aliceToken))
	var meData struct {
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(me.Data, &meData); err != nil {
		t.Fatalf("unmarshal me: %v", err)
	}
	if len(meData.Roles) != 1 || meData.Roles[0] != "editor-v2" {
		t.Fatalf("unexpected admin roles: %v", meData.Roles)
	}

	// 移除角色后 /admin/me 不再展示。
	removeRole := doRequest(t, base, "DELETE", fmt.Sprintf("/admin/v1/admins/%d/roles/%d", aliceID, roleID), nil, superH)
	assertOK(t, removeRole, "remove admin role")
	me = doRequest(t, base, "GET", "/admin/v1/me", nil, authHeader(aliceToken))
	_ = json.Unmarshal(me.Data, &meData)
	if len(meData.Roles) != 0 {
		t.Fatalf("expected no roles after remove, got %v", meData.Roles)
	}

	// 删除角色（级联清理关联）后角色不存在。
	del := doRequest(t, base, "DELETE", fmt.Sprintf("/admin/v1/roles/%d", roleID), nil, superH)
	assertOK(t, del, "delete role")
	if n := dbCount(t, "roles", map[string]any{"id": roleID}); n != 0 {
		t.Fatalf("role should be deleted, got %d rows", n)
	}
	// 删除权限。
	del = doRequest(t, base, "DELETE", fmt.Sprintf("/admin/v1/permissions/%d", permID), nil, superH)
	assertOK(t, del, "delete permission")
	if n := dbCount(t, "permissions", map[string]any{"id": permID}); n != 0 {
		t.Fatalf("permission should be deleted, got %d rows", n)
	}
}

// TestAuthorizationMatrix 覆盖 AC-014/015/016/017/018/019：
// 无权限 403、有权限放行、超级管理员放行、拒绝不产生写入、请求体 id 不作为权限证明。
func TestAuthorizationMatrix(t *testing.T) {
	base, superToken := setupAdminServer(t)
	superH := authHeader(superToken)

	// 前台用户合法 token 访问 /admin → 403（AC-014）。
	userToken, err := auth.GenerateWithSecret([]byte(testJWTSecret), auth.TypeUser, 1, "deadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatalf("generate user token: %v", err)
	}
	res := doRequest(t, base, "GET", "/admin/v1/me", nil, authHeader(userToken))
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("user token on /admin: status=%d code=%d", res.Status, res.Code)
	}

	// 创建无角色普通管理员 bob。
	bobID := createAdmin(t, base, superToken, "bob", "bobpass123")
	bobToken := loginAdmin(t, base, "bob", "bobpass123")
	bobH := authHeader(bobToken)

	// AC-015：无权限访问 /admin/roles → 403。
	res = doRequest(t, base, "GET", "/admin/v1/roles", nil, bobH)
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("no-perm list roles: status=%d code=%d", res.Status, res.Code)
	}

	// AC-018：无权限调用写接口「创建角色」→ 403 且无写入。
	before := dbCount(t, "roles", nil)
	res = doRequest(t, base, "POST", "/admin/v1/roles", map[string]any{"name": "should-not-exist"}, bobH)
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("no-perm create role: status=%d code=%d", res.Status, res.Code)
	}
	if after := dbCount(t, "roles", nil); after != before {
		t.Fatalf("no write expected on 403, roles %d -> %d", before, after)
	}

	// AC-019：bob 尝试通过请求体给自己/他人分配角色 → 403 且无写入（目标 id 不作为权限证明）。
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/v1/admins/%d/roles", bobID), map[string]any{"role_id": 1}, bobH)
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("self-elevate via body: status=%d code=%d", res.Status, res.Code)
	}
	if n := dbCount(t, "admin_roles", map[string]any{"admin_id": bobID}); n != 0 {
		t.Fatalf("no admin_role write expected on 403, got %d rows", n)
	}

	// 超级管理员创建 role:list 权限并赋给 bob 的角色。
	permID := ensurePermission(t, base, superToken, "role:list", "查看角色")
	roleID := createRole(t, base, superToken, "viewer")
	doRequest(t, base, "POST", fmt.Sprintf("/admin/v1/roles/%d/permissions", roleID), map[string]any{"permission_id": permID}, superH)
	doRequest(t, base, "POST", fmt.Sprintf("/admin/v1/admins/%d/roles", bobID), map[string]any{"role_id": roleID}, superH)

	// AC-016：有权限后 bob 可访问 /admin/roles。
	res = doRequest(t, base, "GET", "/admin/v1/roles", nil, bobH)
	assertOK(t, res, "bob list roles after grant")

	// AC-017：超级管理员无论是否显式持有权限均放行。
	res = doRequest(t, base, "GET", "/admin/v1/roles", nil, superH)
	assertOK(t, res, "super list roles")
}
