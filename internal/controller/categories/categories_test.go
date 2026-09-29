package categories_test

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
	categoriesController "cnb.cool/go-cloud-devops/my-shop/internal/controller/categories"
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

type categoryJSON struct {
	Id       int64           `json:"id"`
	ParentId int64           `json:"parent_id"`
	Name     string          `json:"name"`
	Sort     int             `json:"sort"`
	Status   int             `json:"status"`
	Children []*categoryJSON `json:"children"`
}

type listData struct {
	Items []*categoryJSON `json:"items"`
}

type detailData struct {
	Id        int64  `json:"id"`
	ParentId  int64  `json:"parent_id"`
	Name      string `json:"name"`
	Sort      int    `json:"sort"`
	Status    int    `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
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

// setupCategoriesServer 清空 RBAC 表后 Bootstrap（以测试密码重建超级管理员）、清空 categories，
// 挂载真实后台路由（复用 cmd.RegisterAdminRoutes，含 /admin/login 与分类写接口迁移挂载），
// 返回 base 与超级管理员 token。分类写接口现由 AdminAuth + RequirePermission 保护，
// 因此用超级管理员 token（IsSuper 放行）驱动写操作。
func setupCategoriesServer(t *testing.T) (base, token string) {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)

	ctx := context.Background()
	// 先 Bootstrap 一次确保 RBAC 表存在（幂等建表），再清空以测试密码重建超级管理员。
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
		t.Fatalf("rebootstrap: %v", err)
	}
	if _, err := g.DB().Exec(ctx, "DELETE FROM categories"); err != nil {
		t.Fatalf("clean categories: %v", err)
	}

	s := g.Server(guid.S())
	// 覆盖配置里的 server.address(:8000)，绑定随机端口，避免与其他测试（IAM）并行时端口冲突。
	s.SetAddr(":0")
	s.Group("/", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.Response)
		catCtrl := categoriesController.NewV1()
		// 公开接口：分类树形列表与详情（无需 token）。
		group.GET("/categories", catCtrl.List)
		group.GET("/categories/:id", catCtrl.Detail)
		// 后台路由：/admin/login、管理接口与分类写接口迁移挂载。
		cmd.RegisterAdminRoutes(group)
	})
	s.SetDumpRouterMap(false)
	s.Start()
	t.Cleanup(func() { _ = s.Shutdown() })

	time.Sleep(100 * time.Millisecond)

	base = fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
	token = loginAdmin(t, base, testSuperUsername, testAdminPassword)
	return base, token
}

// loginAdmin 经 /admin/login 登录取管理员 token。
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

// createAdmin 经 /admin/admins 创建普通管理员，返回其 id。
func createAdmin(t *testing.T, base, token, username, password string) int64 {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/admins", map[string]any{"username": username, "password": password}, map[string]string{"Authorization": "Bearer " + token})
	assertOK(t, res, "create admin "+username)
	var d struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal create admin res: %v", err)
	}
	return d.Id
}

func dbCount(t *testing.T, where map[string]any) int {
	t.Helper()
	m := g.DB().Model("categories").Ctx(context.Background())
	for k, v := range where {
		m = m.Where(k, v)
	}
	n, err := m.Count()
	if err != nil {
		t.Fatalf("count categories: %v", err)
	}
	return n
}

func dbStatus(t *testing.T, id int64) int {
	t.Helper()
	v, err := g.DB().Model("categories").Ctx(context.Background()).Where("id", id).Value("status")
	if err != nil {
		t.Fatalf("query status: %v", err)
	}
	return v.Int()
}

func createCategory(t *testing.T, base, token string, parentID int64, name string, sort int) int64 {
	t.Helper()
	res := doRequest(t, base, "POST", "/categories", map[string]any{
		"parent_id": parentID,
		"name":      name,
		"sort":      sort,
	}, map[string]string{"Authorization": "Bearer " + token})
	assertOK(t, res, "create "+name)
	var d struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal create res: %v", err)
	}
	if d.Id <= 0 {
		t.Fatalf("expected positive id, got %d", d.Id)
	}
	return d.Id
}

func getList(t *testing.T, base string) listData {
	t.Helper()
	res := doRequest(t, base, "GET", "/categories", nil, nil)
	assertOK(t, res, "list")
	var d listData
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal list res: %v", err)
	}
	return d
}

func getDetail(t *testing.T, base string, id int64) detailData {
	t.Helper()
	res := doRequest(t, base, "GET", fmt.Sprintf("/categories/%d", id), nil, nil)
	assertOK(t, res, "detail")
	var d detailData
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal detail res: %v", err)
	}
	return d
}

func TestCategoriesEndToEnd(t *testing.T) {
	base, token := setupCategoriesServer(t)
	authHeader := map[string]string{"Authorization": "Bearer " + token}

	// AC-004：无 token 创建返回 401。
	res := doRequest(t, base, "POST", "/categories", map[string]any{"name": "手机"}, nil)
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("create without token: status=%d code=%d", res.Status, res.Code)
	}

	// 带 token 创建两个顶级分类（sort 升序应体现）。
	phoneID := createCategory(t, base, token, 0, "手机", 20)
	pcID := createCategory(t, base, token, 0, "电脑", 10)

	// AC-005：同级重复创建同名返回 409，且不产生重复行。
	dup := doRequest(t, base, "POST", "/categories", map[string]any{"parent_id": 0, "name": "手机"}, authHeader)
	if dup.Status != 409 || dup.Code != 3002 {
		t.Fatalf("duplicate create: status=%d code=%d", dup.Status, dup.Code)
	}
	if n := dbCount(t, map[string]any{"parent_id": 0, "name": "手机"}); n != 1 {
		t.Fatalf("expected 1 top-level 手机, got %d", n)
	}

	// AC-002：无 token 列表成功，仅启用项，按 sort 升序。
	list := getList(t, base)
	if len(list.Items) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(list.Items))
	}
	if list.Items[0].Name != "电脑" || list.Items[0].Sort != 10 || list.Items[1].Name != "手机" || list.Items[1].Sort != 20 {
		t.Fatalf("unexpected root order: %+v", list.Items)
	}

	// 创建子分类，验证嵌套。
	accessoryID := createCategory(t, base, token, phoneID, "手机配件", 0)
	list = getList(t, base)
	if len(list.Items) != 2 || list.Items[1].Id != phoneID {
		t.Fatalf("unexpected roots after child create")
	}
	children := list.Items[1].Children
	if len(children) != 1 || children[0].Id != accessoryID || children[0].Name != "手机配件" {
		t.Fatalf("unexpected children: %+v", children)
	}

	// AC-003：详情返回完整字段；不存在的 id 返回 404/3001 且 data null。
	d := getDetail(t, base, phoneID)
	if d.Id != phoneID || d.ParentId != 0 || d.Name != "手机" || d.Sort != 20 || d.Status != 1 {
		t.Fatalf("unexpected detail: %+v", d)
	}
	if d.CreatedAt == "" || d.UpdatedAt == "" {
		t.Fatalf("expected non-empty created_at/updated_at: %+v", d)
	}
	missing := doRequest(t, base, "GET", "/categories/999999", nil, nil)
	if missing.Status != 404 || missing.Code != 3001 {
		t.Fatalf("missing detail: status=%d code=%d", missing.Status, missing.Code)
	}
	if string(missing.Data) != "null" {
		t.Fatalf("expected data null for missing detail, got %s", missing.Data)
	}

	// AC-006：无 token 更新返回 401；带 token 分别改名/改排序/改状态/改父。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/categories/%d", pcID), map[string]any{"name": "电脑设备"}, nil)
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("update without token: status=%d code=%d", res.Status, res.Code)
	}
	res = doRequest(t, base, "PUT", fmt.Sprintf("/categories/%d", pcID), map[string]any{"name": "电脑设备", "sort": 5}, authHeader)
	assertOK(t, res, "rename+resort pc")
	d = getDetail(t, base, pcID)
	if d.Name != "电脑设备" || d.Sort != 5 {
		t.Fatalf("unexpected pc after update: %+v", d)
	}
	// AC-006（CLEAN-002）：更新不存在的 id 返回 404/3001。
	res = doRequest(t, base, "PUT", "/categories/999999", map[string]any{"name": "不存在"}, authHeader)
	if res.Status != 404 || res.Code != 3001 {
		t.Fatalf("update missing id: status=%d code=%d", res.Status, res.Code)
	}
	// AC-005（改名）：在手机下新增兄弟分类「手机壳」，再将「手机配件」改名为「手机壳」→ 同级重名 409。
	createCategory(t, base, token, phoneID, "手机壳", 0)
	res = doRequest(t, base, "PUT", fmt.Sprintf("/categories/%d", accessoryID), map[string]any{"name": "手机壳"}, authHeader)
	if res.Status != 409 || res.Code != 3002 {
		t.Fatalf("rename to sibling name: status=%d code=%d", res.Status, res.Code)
	}
	if got := getDetail(t, base, accessoryID); got.Name != "手机配件" {
		t.Fatalf("accessory name should be unchanged after conflict, got %q", got.Name)
	}

	// AC-007：更新 parent_id 指向自身或后代返回 400，数据不变。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/categories/%d", phoneID), map[string]any{"parent_id": phoneID}, authHeader)
	if res.Status != 400 || res.Code != 3004 {
		t.Fatalf("parent self: status=%d code=%d", res.Status, res.Code)
	}
	res = doRequest(t, base, "PUT", fmt.Sprintf("/categories/%d", phoneID), map[string]any{"parent_id": accessoryID}, authHeader)
	if res.Status != 400 || res.Code != 3004 {
		t.Fatalf("parent descendant: status=%d code=%d", res.Status, res.Code)
	}
	if got := getDetail(t, base, phoneID); got.ParentId != 0 {
		t.Fatalf("phone parent should remain 0, got %d", got.ParentId)
	}

	// AC-008：三级分类下再建子分类（或改父导致超 3 级）返回 400。
	midID := createCategory(t, base, token, pcID, "电脑子类", 0)
	deepID := createCategory(t, base, token, midID, "三级子类", 0)
	res = doRequest(t, base, "POST", "/categories", map[string]any{"parent_id": deepID, "name": "四级子类"}, authHeader)
	if res.Status != 400 || res.Code != 3004 {
		t.Fatalf("create level-4: status=%d code=%d", res.Status, res.Code)
	}
	// 把 phoneID（顶级）改到 deepID（三级）下会变成 4 级。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/categories/%d", phoneID), map[string]any{"parent_id": deepID}, authHeader)
	if res.Status != 400 || res.Code != 3004 {
		t.Fatalf("reparent to level-4: status=%d code=%d", res.Status, res.Code)
	}
	// CLEAN-001 回归：移动含子节点的子树，自身层级 ≤3 但后代将超过 3 级，必须 400/3004 且父子关系不变。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/categories/%d", phoneID), map[string]any{"parent_id": midID}, authHeader)
	if res.Status != 400 || res.Code != 3004 {
		t.Fatalf("move subtree causing descendant level-4: status=%d code=%d", res.Status, res.Code)
	}
	if got := getDetail(t, base, phoneID); got.ParentId != 0 {
		t.Fatalf("phone parent should remain 0 after refused subtree move, got %d", got.ParentId)
	}

	// AC-009：无 token 删除返回 401；有子分类删除返回 409 且数据不变；叶子删除成功且详情 404。
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/categories/%d", phoneID), nil, nil)
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("delete without token: status=%d code=%d", res.Status, res.Code)
	}
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/categories/%d", phoneID), nil, authHeader)
	if res.Status != 409 || res.Code != 3003 {
		t.Fatalf("delete with children: status=%d code=%d", res.Status, res.Code)
	}
	if n := dbCount(t, map[string]any{"id": phoneID}); n != 1 {
		t.Fatalf("phone should still exist after refused delete")
	}
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/categories/%d", accessoryID), nil, authHeader)
	assertOK(t, res, "delete leaf")
	after := doRequest(t, base, "GET", fmt.Sprintf("/categories/%d", accessoryID), nil, nil)
	if after.Status != 404 || after.Code != 3001 {
		t.Fatalf("deleted detail: status=%d code=%d", after.Status, after.Code)
	}
	// AC-009（CLEAN-002）：删除不存在的 id 返回 404/3001。
	res = doRequest(t, base, "DELETE", "/categories/999999", nil, authHeader)
	if res.Status != 404 || res.Code != 3001 {
		t.Fatalf("delete missing id: status=%d code=%d", res.Status, res.Code)
	}

	// AC-010：禁用分类后记录仍在、树不再展示该分类及其子树、详情仍可访问。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/categories/%d", phoneID), map[string]any{"status": 0}, authHeader)
	assertOK(t, res, "disable phone")
	if dbStatus(t, phoneID) != 0 {
		t.Fatalf("phone should be status=0 in DB")
	}
	if n := dbCount(t, map[string]any{"id": phoneID}); n != 1 {
		t.Fatalf("phone should still exist in DB after disable")
	}
	list = getList(t, base)
	for _, item := range list.Items {
		if item.Id == phoneID {
			t.Fatalf("disabled phone should not appear in tree: %+v", list.Items)
		}
	}
	_ = getDetail(t, base, phoneID) // 详情仍可访问
}

// TestConcurrentCreateSameName 验证并发创建同级同名分类时仅一个成功（INV：DB 复合唯一键兜底）。
func TestConcurrentCreateSameName(t *testing.T) {
	base, token := setupCategoriesServer(t)
	authHeader := map[string]string{"Authorization": "Bearer " + token}

	const (
		n    = 10
		name = "racecategory"
	)
	results := make(chan apiResult, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := request(base, "POST", "/categories", map[string]any{"parent_id": 0, "name": name}, authHeader)
			if err != nil {
				res = apiResult{Code: -1}
			}
			results <- res
		}()
	}
	wg.Wait()
	close(results)

	var success, conflict int
	for res := range results {
		switch res.Code {
		case 0:
			success++
		case 3002:
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
	if c := dbCount(t, map[string]any{"parent_id": 0, "name": name}); c != 1 {
		t.Fatalf("expected 1 row for %q, got %d", name, c)
	}
}

// TestCategoryWriteAuthorization 覆盖分类写接口迁移后的授权边界（INV-004/005）：
// 无 token → 401；前台用户 token → 403（type 隔离）；无 category 权限的普通管理员 → 403 且无写入；
// 超级管理员 → 成功（IsSuper 放行）。用于锁定「AdminAuth + RequirePermission」已真正挂载到分类写接口。
func TestCategoryWriteAuthorization(t *testing.T) {
	base, superToken := setupCategoriesServer(t)
	superH := map[string]string{"Authorization": "Bearer " + superToken}

	// 无 token 写 → 401。
	res := doRequest(t, base, "POST", "/categories", map[string]any{"name": "x"}, nil)
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("no token: status=%d code=%d", res.Status, res.Code)
	}

	// 前台用户 token 写 → 403（type=user 被 AdminAuth 拒绝）。
	userToken, err := auth.GenerateWithSecret([]byte(testJWTSecret), auth.TypeUser, 1, "deadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatalf("generate user token: %v", err)
	}
	res = doRequest(t, base, "POST", "/categories", map[string]any{"name": "x"}, map[string]string{"Authorization": "Bearer " + userToken})
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("user token: status=%d code=%d", res.Status, res.Code)
	}

	// 无 category 权限的普通管理员写 → 403 且无写入。
	createAdmin(t, base, superToken, "catwriter", "catwriterpass123")
	writerToken := loginAdmin(t, base, "catwriter", "catwriterpass123")
	before := dbCount(t, nil)
	res = doRequest(t, base, "POST", "/categories", map[string]any{"parent_id": 0, "name": "越权分类"}, map[string]string{"Authorization": "Bearer " + writerToken})
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("no-perm admin: status=%d code=%d", res.Status, res.Code)
	}
	if after := dbCount(t, nil); after != before {
		t.Fatalf("no write expected on 403, categories %d -> %d", before, after)
	}

	// 超级管理员写 → 成功。
	res = doRequest(t, base, "POST", "/categories", map[string]any{"parent_id": 0, "name": "手机"}, superH)
	assertOK(t, res, "super create category")
}
