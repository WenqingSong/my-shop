package cmd

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定「轮播图」核心闭环，覆盖 banner-v1 的 AC-001 至 AC-006 与关键不变量（INV-001 至 INV-004）。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
	"cnb.cool/go-cloud-devops/my-shop/internal/storage"
)

// setupBannerServer 建立隔离的轮播图测试环境：复用身份隔离初始化 + 本地存储静态服务，
// 并额外清空 banners 表。占位图与静态路由经生产同款 configureBannerStorage 注入。
func setupBannerServer(t *testing.T) string {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", isoJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", isoAdminPassword)
	t.Setenv("STORAGE_LOCAL_ROOT", t.TempDir())

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// 清空轮播图与身份相关表，随后二次 Bootstrap 重建超级管理员与权限 seed。
	for _, table := range []string{"banners", "users", "admin_roles", "role_permissions", "roles", "permissions", "admins"} {
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
	if err := configureBannerStorage(ctx, s); err != nil {
		t.Fatalf("configure banner storage: %v", err)
	}
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

// bannerData 是轮播图响应结构（公开列表缺 status，后台完整字段）。
type bannerData struct {
	Id       int64  `json:"id"`
	Title    string `json:"title"`
	ImageUrl string `json:"image_url"`
	LinkUrl  string `json:"link_url"`
	Sort     int    `json:"sort"`
	Status   int    `json:"status"`
}

// bannerCall 发起轮播图相关请求（支持 GET/POST/PUT/DELETE），解码统一响应。
func bannerCall(t *testing.T, base, method, path, token string, body any) isoResult {
	t.Helper()
	c := g.Client()
	if token != "" {
		c = c.Header(map[string]string{"Authorization": "Bearer " + token})
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
		t.Fatalf("unsupported method %s", method)
	}
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer r.Close()
	return isoDecode(r)
}

// bannerDecodeData 将统一响应中的 data 解码为轮播图结构。
func bannerDecodeData(t *testing.T, data map[string]any) bannerData {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal banner data: %v", err)
	}
	var out bannerData
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal banner data: %v", err)
	}
	return out
}

// bannerDecodeList 将统一响应中的 data 解码为轮播图列表。
func bannerDecodeList(t *testing.T, data map[string]any) []bannerData {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal banner list: %v", err)
	}
	var env struct {
		Items []bannerData `json:"items"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("unmarshal banner list: %v", err)
	}
	return env.Items
}

// bannerFetchImage 请求静态图片资源，返回 HTTP 状态与 Content-Type（不经统一响应中间件）。
func bannerFetchImage(t *testing.T, base, path string) (int, string) {
	t.Helper()
	r, err := g.Client().Get(context.Background(), base+path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer r.Close()
	return r.StatusCode, r.Header.Get("Content-Type")
}

// bannerInsert 直接写入一条轮播图（link_url 为空时不写入，列默认 NULL）。
func bannerInsert(t *testing.T, title, imageURL, linkURL string, sort, status int) int64 {
	t.Helper()
	data := g.Map{
		"title":     title,
		"image_url": imageURL,
		"sort":      sort,
		"status":    status,
	}
	if linkURL != "" {
		data["link_url"] = linkURL
	}
	id, err := g.DB().Model("banners").Ctx(context.Background()).Data(data).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert banner: %v", err)
	}
	return id
}

// TestBannerPublicListEnabledAndSorted 覆盖 AC-001/AC-006 与 INV-001：
// 公开列表仅含启用项、按 sort 升序（同值按 id 升序）、字段完整；后台列表可见全部状态。
func TestBannerPublicListEnabledAndSorted(t *testing.T) {
	base := setupBannerServer(t)

	bannerInsert(t, "B", "/storage/banners/banner-2.png", "", 5, 1)
	bannerInsert(t, "D禁用", "/storage/banners/banner-3.png", "", 5, 0) // 禁用，同 sort=5
	idA := bannerInsert(t, "A", "/storage/banners/banner-1.png", "/products/1", 10, 1)
	idC := bannerInsert(t, "C", "/storage/banners/banner-3.png", "", 10, 1) // 同 sort=10，id 更大

	res := bannerCall(t, base, "GET", "/banners", "", nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("public list: status=%d code=%d", res.Status, res.Code)
	}
	items := bannerDecodeList(t, res.Data)
	if len(items) != 3 {
		t.Fatalf("public items=%d want 3（禁用项应被过滤）", len(items))
	}
	// 期望顺序：B(sort=5) → A(sort=10,id小) → C(sort=10,id大)。
	if items[0].Title != "B" || items[1].Title != "A" || items[2].Title != "C" {
		t.Fatalf("public order wrong: %q,%q,%q", items[0].Title, items[1].Title, items[2].Title)
	}
	if items[0].Sort != 5 || items[1].Sort != 10 || items[2].Sort != 10 {
		t.Fatalf("sort values wrong: %+v", items)
	}
	if items[1].Id != idA || items[1].LinkUrl != "/products/1" || items[1].ImageUrl != "/storage/banners/banner-1.png" {
		t.Fatalf("item A fields wrong: %+v", items[1])
	}
	if items[2].Id != idC || items[2].LinkUrl != "" {
		t.Fatalf("item C fields wrong（无跳转应为空）: %+v", items[2])
	}

	// 后台列表可见全部状态（含禁用），且按 sort 升序。
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	ares := bannerCall(t, base, "GET", "/admin/banners", adminToken, nil)
	if ares.Status != 200 || ares.Code != 0 {
		t.Fatalf("admin list: status=%d code=%d", ares.Status, ares.Code)
	}
	if aitems := bannerDecodeList(t, ares.Data); len(aitems) != 4 {
		t.Fatalf("admin items=%d want 4", len(aitems))
	}
}

// TestBannerPlaceholderImagesAccessible 覆盖 AC-002：3 张占位图经 image_url 可访问且内容类型为图片。
func TestBannerPlaceholderImagesAccessible(t *testing.T) {
	base := setupBannerServer(t)

	for _, url := range storage.BannerPlaceholderURLs() {
		status, contentType := bannerFetchImage(t, base, url)
		if status != 200 {
			t.Fatalf("%s: status=%d want 200", url, status)
		}
		if !strings.HasPrefix(contentType, "image/") {
			t.Fatalf("%s: content-type=%q want image/*", url, contentType)
		}
	}
}

// TestBannerAdminCreateAndPermission 覆盖 AC-003/INV-002：
// 超管创建落库且默认 status=1/sort=0；无权限 403、未认证 401 且均无写入。
func TestBannerAdminCreateAndPermission(t *testing.T) {
	base := setupBannerServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 超管创建（省略 status/sort，验证默认值）→ 200，落库。
	create := bannerCall(t, base, "POST", "/admin/banners", adminToken, map[string]any{
		"title": "首页主图", "image_url": "/storage/banners/banner-1.png", "link_url": "/products/1",
	})
	if create.Status != 200 || create.Code != 0 {
		t.Fatalf("create: status=%d code=%d msg=%q", create.Status, create.Code, create.Message)
	}
	bd := bannerDecodeData(t, create.Data)
	if bd.Id <= 0 || bd.Title != "首页主图" || bd.Status != 1 || bd.Sort != 0 || bd.LinkUrl != "/products/1" {
		t.Fatalf("create response wrong（默认值）: %+v", bd)
	}
	if n, _ := g.DB().Model("banners").Ctx(context.Background()).Where("id", bd.Id).Count(); n != 1 {
		t.Fatalf("banner not persisted")
	}

	// 无权限普通管理员 → 403/1003，无写入。
	isoInsertAdmin(t, "bannerplain", "bannerplain123")
	plainToken, _ := isoAdminLogin(t, base, "bannerplain", "bannerplain123")
	forbidden := bannerCall(t, base, "POST", "/admin/banners", plainToken, map[string]any{
		"title": "x", "image_url": "/storage/banners/banner-1.png",
	})
	if forbidden.Status != 403 || forbidden.Code != 1003 {
		t.Fatalf("plain admin create: status=%d code=%d want 403/1003", forbidden.Status, forbidden.Code)
	}

	// 未认证 → 401/1002，无写入。
	unauth := bannerCall(t, base, "POST", "/admin/banners", "", map[string]any{
		"title": "x", "image_url": "/storage/banners/banner-1.png",
	})
	if unauth.Status != 401 || unauth.Code != 1002 {
		t.Fatalf("unauth create: status=%d code=%d want 401/1002", unauth.Status, unauth.Code)
	}

	if n, _ := g.DB().Model("banners").Ctx(context.Background()).Count(); n != 1 {
		t.Fatalf("banner count=%d want 1", n)
	}
}

// TestBannerAdminUpdateAndSort 覆盖 AC-004/AC-006：
// 更新标题/图片/跳转/排序/状态反映新值；部分更新保留未提交字段；禁用项不出现在公开列表。
func TestBannerAdminUpdateAndSort(t *testing.T) {
	base := setupBannerServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	create := bannerCall(t, base, "POST", "/admin/banners", adminToken, map[string]any{
		"title": "原始", "image_url": "/storage/banners/banner-1.png", "sort": 1, "status": 1,
	})
	if create.Status != 200 || create.Code != 0 {
		t.Fatalf("create: status=%d code=%d", create.Status, create.Code)
	}
	id := bannerDecodeData(t, create.Data).Id

	// 全量更新。
	upd := bannerCall(t, base, "PUT", fmt.Sprintf("/admin/banners/%d", id), adminToken, map[string]any{
		"title": "更新后", "image_url": "/storage/banners/banner-2.png",
		"link_url": "/products/2", "sort": 9, "status": 0,
	})
	if upd.Status != 200 || upd.Code != 0 {
		t.Fatalf("update: status=%d code=%d msg=%q", upd.Status, upd.Code, upd.Message)
	}
	if ub := bannerDecodeData(t, upd.Data); ub.Title != "更新后" || ub.ImageUrl != "/storage/banners/banner-2.png" ||
		ub.LinkUrl != "/products/2" || ub.Sort != 9 || ub.Status != 0 {
		t.Fatalf("update response wrong: %+v", ub)
	}

	// 后台详情反映新值。
	detail := bannerCall(t, base, "GET", fmt.Sprintf("/admin/banners/%d", id), adminToken, nil)
	if detail.Status != 200 || detail.Code != 0 {
		t.Fatalf("detail: status=%d code=%d", detail.Status, detail.Code)
	}
	if db := bannerDecodeData(t, detail.Data); db.Title != "更新后" || db.Status != 0 || db.Sort != 9 {
		t.Fatalf("detail wrong: %+v", db)
	}

	// 禁用后公开列表不含该项。
	pub := bannerCall(t, base, "GET", "/banners", "", nil)
	if items := bannerDecodeList(t, pub.Data); len(items) != 0 {
		t.Fatalf("disabled banner should not be public, items=%d", len(items))
	}

	// 部分更新：仅改 status 恢复启用，其余字段保留。
	upd2 := bannerCall(t, base, "PUT", fmt.Sprintf("/admin/banners/%d", id), adminToken, map[string]any{"status": 1})
	if upd2.Status != 200 || upd2.Code != 0 {
		t.Fatalf("partial update: status=%d code=%d", upd2.Status, upd2.Code)
	}
	if ub2 := bannerDecodeData(t, upd2.Data); ub2.Title != "更新后" || ub2.Status != 1 || ub2.Sort != 9 {
		t.Fatalf("partial update should preserve other fields: %+v", ub2)
	}
	pub = bannerCall(t, base, "GET", "/banners", "", nil)
	if items := bannerDecodeList(t, pub.Data); len(items) != 1 || items[0].Title != "更新后" {
		t.Fatalf("enabled banner should be public: %+v", items)
	}
}

// TestBannerAdminDelete 覆盖 AC-005/INV-004 与不存在语义：
// 删除后公开与后台均不再出现；重复删除/详情/更新不存在统一 404/14001。
func TestBannerAdminDelete(t *testing.T) {
	base := setupBannerServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	create := bannerCall(t, base, "POST", "/admin/banners", adminToken, map[string]any{
		"title": "待删", "image_url": "/storage/banners/banner-1.png", "status": 1,
	})
	if create.Status != 200 || create.Code != 0 {
		t.Fatalf("create: status=%d code=%d", create.Status, create.Code)
	}
	id := bannerDecodeData(t, create.Data).Id

	del := bannerCall(t, base, "DELETE", fmt.Sprintf("/admin/banners/%d", id), adminToken, nil)
	if del.Status != 200 || del.Code != 0 {
		t.Fatalf("delete: status=%d code=%d", del.Status, del.Code)
	}

	// 公开与后台列表均不再包含。
	pub := bannerCall(t, base, "GET", "/banners", "", nil)
	if items := bannerDecodeList(t, pub.Data); len(items) != 0 {
		t.Fatalf("deleted banner still public")
	}
	al := bannerCall(t, base, "GET", "/admin/banners", adminToken, nil)
	if items := bannerDecodeList(t, al.Data); len(items) != 0 {
		t.Fatalf("deleted banner still in admin list")
	}

	// 重复删除 → 404/14001。
	if del2 := bannerCall(t, base, "DELETE", fmt.Sprintf("/admin/banners/%d", id), adminToken, nil); del2.Status != 404 || del2.Code != 14001 {
		t.Fatalf("re-delete: status=%d code=%d want 404/14001", del2.Status, del2.Code)
	}
	// 详情不存在 → 404/14001。
	if miss := bannerCall(t, base, "GET", "/admin/banners/999999", adminToken, nil); miss.Status != 404 || miss.Code != 14001 {
		t.Fatalf("detail missing: status=%d code=%d want 404/14001", miss.Status, miss.Code)
	}
	// 更新不存在 → 404/14001。
	if missUpd := bannerCall(t, base, "PUT", "/admin/banners/999999", adminToken, map[string]any{"title": "x"}); missUpd.Status != 404 || missUpd.Code != 14001 {
		t.Fatalf("update missing: status=%d code=%d want 404/14001", missUpd.Status, missUpd.Code)
	}
}

// TestBannerInvalidInputRejected 覆盖 INV-003：非法标题/图片/跳转/状态统一 400/14002 且无写入。
func TestBannerInvalidInputRejected(t *testing.T) {
	base := setupBannerServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	cases := []map[string]any{
		{"title": "", "image_url": "/storage/banners/banner-1.png"},                      // 标题为空
		{"title": "   ", "image_url": "/storage/banners/banner-1.png"},                   // 标题 trim 后为空
		{"title": strings.Repeat("a", 65), "image_url": "/storage/banners/banner-1.png"}, // 标题超长
		{"title": "ok", "image_url": ""},                                                 // 图片为空
		{"title": "ok", "image_url": strings.Repeat("a", 256)},                           // 图片超长
		{"title": "ok", "image_url": "/x.png", "link_url": strings.Repeat("a", 513)},     // 跳转超长
		{"title": "ok", "image_url": "/x.png", "status": 2},                              // 状态非法
	}
	for i, body := range cases {
		res := bannerCall(t, base, "POST", "/admin/banners", adminToken, body)
		if res.Status != 400 || res.Code != 14002 {
			t.Fatalf("case %d: status=%d code=%d want 400/14002", i, res.Status, res.Code)
		}
	}

	if n, _ := g.DB().Model("banners").Ctx(context.Background()).Count(); n != 0 {
		t.Fatalf("invalid input must not write, count=%d", n)
	}
}

// TestBannerUpdateRegression 回归 CLEAN-001：Update 不依据 RowsAffected 判断存在性，
// 覆盖「不存在→404」「正常修改→成功」「相同值幂等→成功」「无权限→403 且无副作用」。
func TestBannerUpdateRegression(t *testing.T) {
	base := setupBannerServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	create := bannerCall(t, base, "POST", "/admin/banners", adminToken, map[string]any{
		"title": "原始", "image_url": "/storage/banners/banner-1.png", "status": 1,
	})
	if create.Status != 200 || create.Code != 0 {
		t.Fatalf("create: status=%d code=%d", create.Status, create.Code)
	}
	id := bannerDecodeData(t, create.Data).Id

	// 1) 不存在 → 404/14001。
	if miss := bannerCall(t, base, "PUT", "/admin/banners/999999", adminToken, map[string]any{"title": "x"}); miss.Status != 404 || miss.Code != 14001 {
		t.Fatalf("missing update: status=%d code=%d want 404/14001", miss.Status, miss.Code)
	}

	// 2) 正常修改 → 成功且数据更新。
	chg := bannerCall(t, base, "PUT", fmt.Sprintf("/admin/banners/%d", id), adminToken, map[string]any{"title": "已修改"})
	if chg.Status != 200 || chg.Code != 0 {
		t.Fatalf("change update: status=%d code=%d", chg.Status, chg.Code)
	}
	if b := bannerDecodeData(t, chg.Data); b.Title != "已修改" {
		t.Fatalf("change update not applied: %+v", b)
	}

	// 3) 完全相同值重复 Update → 幂等成功（不得 404/14001）。
	same := bannerCall(t, base, "PUT", fmt.Sprintf("/admin/banners/%d", id), adminToken, map[string]any{"title": "已修改"})
	if same.Status != 200 || same.Code != 0 {
		t.Fatalf("noop update: status=%d code=%d msg=%q want 200/0", same.Status, same.Code, same.Message)
	}
	if b := bannerDecodeData(t, same.Data); b.Id != id || b.Title != "已修改" {
		t.Fatalf("noop update response wrong: %+v", b)
	}

	// 4) 无权限更新 → 403/1003 且无副作用。
	isoInsertAdmin(t, "bannerupdplain", "bannerupdplain123")
	plainToken, _ := isoAdminLogin(t, base, "bannerupdplain", "bannerupdplain123")
	forbidden := bannerCall(t, base, "PUT", fmt.Sprintf("/admin/banners/%d", id), plainToken, map[string]any{"title": "篡改"})
	if forbidden.Status != 403 || forbidden.Code != 1003 {
		t.Fatalf("plain admin update: status=%d code=%d want 403/1003", forbidden.Status, forbidden.Code)
	}
	v, err := g.DB().Model("banners").Ctx(context.Background()).Fields("title").Where("id", id).Value()
	if err != nil {
		t.Fatalf("query title: %v", err)
	}
	if v == nil || v.String() != "已修改" {
		t.Fatalf("forbidden update must not change title, got %v", v)
	}
}
