package cmd

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定「推荐位」核心闭环，覆盖 recommendation-v1 的 AC-001 至 AC-008 与关键不变量（INV-001 至 INV-006）。

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

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
	// 触发各 logic 包的 init()，注册 service.IRecommendation 等。
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
)

// setupRecommendationServer 建立隔离的推荐位测试环境：迁移建表 + Bootstrap（权限 seed + 超级管理员），
// 清空推荐位与身份相关表后二次 Bootstrap 重建超级管理员与权限 seed，再 flush Redis。
// 商品/分类数据不在此清空（本测试仅向自建推荐位添加自建商品，前台查询按 position 隔离，无跨测试污染）。
func setupRecommendationServer(t *testing.T) string {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", isoJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", isoAdminPassword)

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// 先清空推荐位与身份相关表（recommend_items 引用 recommend_positions，先删子表），
	// 随后二次 Bootstrap 以测试密码重建超级管理员与权限 seed。
	for _, table := range []string{"recommend_items", "recommend_positions", "users", "admin_roles", "role_permissions", "roles", "permissions", "admins"} {
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

// recCall 发起推荐位相关请求（支持 GET/POST/PUT/DELETE），解码统一响应。
func recCall(t *testing.T, base, method, path, token string, body any) isoResult {
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

// recDecode 将统一响应中的 data 解码为指定结构。
func recDecode(t *testing.T, data map[string]any, out any) {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
}

type recPosition struct {
	Id     int64  `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Status int    `json:"status"`
}

type recItem struct {
	Id         int64 `json:"id"`
	PositionId int64 `json:"position_id"`
	ProductId  int64 `json:"product_id"`
	Sort       int   `json:"sort"`
}

type recSnapshot struct {
	ProductId int64  `json:"product_id"`
	Name      string `json:"name"`
	MainImage string `json:"main_image"`
	Price     int64  `json:"price"`
	Sort      int    `json:"sort"`
}

type recFrontendData struct {
	Code  string         `json:"code"`
	Name  string         `json:"name"`
	Items []*recSnapshot `json:"items"`
}

type recDetailData struct {
	Id     int64      `json:"id"`
	Code   string     `json:"code"`
	Name   string     `json:"name"`
	Status int        `json:"status"`
	Items  []*recItem `json:"items"`
}

// recInsertCategory 直接写入一个启用分类，返回分类 id。
func recInsertCategory(t *testing.T, name string) int64 {
	t.Helper()
	id, err := g.DB().Model("categories").Ctx(context.Background()).Data(g.Map{
		"name": name, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert category %s: %v", name, err)
	}
	return id
}

// recInsertProduct 直接写入一个商品（status：0=draft/1=on_shelf/2=off_shelf），返回商品 id。
func recInsertProduct(t *testing.T, name string, categoryID int64, price int64, status int) int64 {
	t.Helper()
	id, err := g.DB().Model("products").Ctx(context.Background()).Data(g.Map{
		"name":        name,
		"category_id": categoryID,
		"price":       price,
		"status":      status,
		"main_image":  "/images/" + name + ".png",
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert product %s: %v", name, err)
	}
	return id
}

// recCreatePosition 经 API 创建推荐位，返回推荐位 id。
func recCreatePosition(t *testing.T, base, token, code, name string, status int) int64 {
	t.Helper()
	res := recCall(t, base, "POST", "/admin/recommend-positions", token, map[string]any{
		"code": code, "name": name, "status": status,
	})
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("create position %s: status=%d code=%d msg=%q", code, res.Status, res.Code, res.Message)
	}
	var p recPosition
	recDecode(t, res.Data, &p)
	if p.Id <= 0 {
		t.Fatalf("create position %s: empty id", code)
	}
	return p.Id
}

// TestRecommendationFrontendOnlyOnShelfAndSorted 覆盖 AC-006 与 INV-001：
// 前台仅返回启用推荐位中的 on_shelf 商品，按 sort 升序、同值按 id 升序；禁用位/不存在的位返回空。
func TestRecommendationFrontendOnlyOnShelfAndSorted(t *testing.T) {
	base := setupRecommendationServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	cat := recInsertCategory(t, "推荐位测试分类")
	pa := recInsertProduct(t, "商品A", cat, 100, 1) // on_shelf, sort=10
	pb := recInsertProduct(t, "商品B", cat, 200, 1) // on_shelf, sort=5
	pc := recInsertProduct(t, "商品C", cat, 300, 1) // on_shelf, sort=5（后加入，id 更大）
	pd := recInsertProduct(t, "商品D", cat, 400, 2) // off_shelf（应被过滤）
	pe := recInsertProduct(t, "商品E", cat, 500, 0) // draft（应被过滤）

	homeID := recCreatePosition(t, base, adminToken, "home", "首页推荐", 1)
	for _, it := range []struct {
		pid  int64
		sort int
	}{
		{pb, 5}, {pc, 5}, {pa, 10}, {pd, 1}, {pe, 1},
	} {
		res := recCall(t, base, "POST", fmt.Sprintf("/admin/recommend-positions/%d/items", homeID), adminToken, map[string]any{
			"product_id": it.pid, "sort": it.sort,
		})
		if res.Status != 200 || res.Code != 0 {
			t.Fatalf("add item %d: status=%d code=%d msg=%q", it.pid, res.Status, res.Code, res.Message)
		}
	}

	// 前台查询：仅 B(sort=5) → C(sort=5,id 大) → A(sort=10)，off_shelf/draft 被过滤。
	res := recCall(t, base, "GET", "/recommendations/home", "", nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("frontend: status=%d code=%d", res.Status, res.Code)
	}
	var fd recFrontendData
	recDecode(t, res.Data, &fd)
	if fd.Code != "home" || fd.Name != "首页推荐" {
		t.Fatalf("frontend meta wrong: %+v", fd)
	}
	if len(fd.Items) != 3 {
		t.Fatalf("frontend items=%d want 3（off_shelf/draft 应被过滤）: %+v", len(fd.Items), fd.Items)
	}
	if fd.Items[0].ProductId != pb || fd.Items[1].ProductId != pc || fd.Items[2].ProductId != pa {
		t.Fatalf("frontend order wrong: %d,%d,%d", fd.Items[0].ProductId, fd.Items[1].ProductId, fd.Items[2].ProductId)
	}
	if fd.Items[0].Sort != 5 || fd.Items[1].Sort != 5 || fd.Items[2].Sort != 10 {
		t.Fatalf("frontend sort values wrong: %+v", fd.Items)
	}
	if fd.Items[0].Price != 200 || fd.Items[0].Name != "商品B" {
		t.Fatalf("frontend snapshot fields wrong: %+v", fd.Items[0])
	}

	// 禁用推荐位：前台返回空。
	disabledID := recCreatePosition(t, base, adminToken, "disabled", "已禁用", 0)
	recCall(t, base, "POST", fmt.Sprintf("/admin/recommend-positions/%d/items", disabledID), adminToken, map[string]any{"product_id": pa})
	dres := recCall(t, base, "GET", "/recommendations/disabled", "", nil)
	if dres.Status != 200 || dres.Code != 0 {
		t.Fatalf("frontend disabled: status=%d code=%d", dres.Status, dres.Code)
	}
	var dd recFrontendData
	recDecode(t, dres.Data, &dd)
	if len(dd.Items) != 0 {
		t.Fatalf("disabled position should return empty, got %d items", len(dd.Items))
	}

	// 不存在的推荐位：前台返回空（与禁用不区分，不泄露内部状态）。
	mres := recCall(t, base, "GET", "/recommendations/nonexistent", "", nil)
	if mres.Status != 200 || mres.Code != 0 {
		t.Fatalf("frontend missing: status=%d code=%d", mres.Status, mres.Code)
	}
	var md recFrontendData
	recDecode(t, mres.Data, &md)
	if len(md.Items) != 0 || md.Code != "" {
		t.Fatalf("missing position should return empty, got %+v", md)
	}
}

// TestRecommendationAdminCRUD 覆盖 AC-002：创建/列表/详情/更新/删除、code 冲突与不存在语义。
func TestRecommendationAdminCRUD(t *testing.T) {
	base := setupRecommendationServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 创建（默认 status=1）。
	create := recCall(t, base, "POST", "/admin/recommend-positions", adminToken, map[string]any{
		"code": "home", "name": "首页推荐",
	})
	if create.Status != 200 || create.Code != 0 {
		t.Fatalf("create: status=%d code=%d msg=%q", create.Status, create.Code, create.Message)
	}
	var p recPosition
	recDecode(t, create.Data, &p)
	if p.Id <= 0 || p.Code != "home" || p.Name != "首页推荐" || p.Status != 1 {
		t.Fatalf("create response wrong: %+v", p)
	}

	// 重复 code → 409/15002。
	dup := recCall(t, base, "POST", "/admin/recommend-positions", adminToken, map[string]any{
		"code": "home", "name": "另一个",
	})
	if dup.Status != 409 || dup.Code != 15002 {
		t.Fatalf("duplicate code: status=%d code=%d want 409/15002", dup.Status, dup.Code)
	}

	// 列表可见。
	list := recCall(t, base, "GET", "/admin/recommend-positions", adminToken, nil)
	if list.Status != 200 || list.Code != 0 {
		t.Fatalf("list: status=%d code=%d", list.Status, list.Code)
	}
	var ld struct {
		Items []*recPosition `json:"items"`
	}
	recDecode(t, list.Data, &ld)
	if len(ld.Items) != 1 {
		t.Fatalf("list items=%d want 1", len(ld.Items))
	}

	// 更新 name/status（code 不变）。
	upd := recCall(t, base, "PUT", fmt.Sprintf("/admin/recommend-positions/%d", p.Id), adminToken, map[string]any{
		"name": "首页改", "status": 0,
	})
	if upd.Status != 200 || upd.Code != 0 {
		t.Fatalf("update: status=%d code=%d msg=%q", upd.Status, upd.Code, upd.Message)
	}
	var up recPosition
	recDecode(t, upd.Data, &up)
	if up.Name != "首页改" || up.Status != 0 || up.Code != "home" {
		t.Fatalf("update response wrong: %+v", up)
	}

	// 详情反映新值。
	detail := recCall(t, base, "GET", fmt.Sprintf("/admin/recommend-positions/%d", p.Id), adminToken, nil)
	if detail.Status != 200 || detail.Code != 0 {
		t.Fatalf("detail: status=%d code=%d", detail.Status, detail.Code)
	}
	var dd recDetailData
	recDecode(t, detail.Data, &dd)
	if dd.Name != "首页改" || dd.Status != 0 {
		t.Fatalf("detail wrong: %+v", dd)
	}

	// 删除。
	del := recCall(t, base, "DELETE", fmt.Sprintf("/admin/recommend-positions/%d", p.Id), adminToken, nil)
	if del.Status != 200 || del.Code != 0 {
		t.Fatalf("delete: status=%d code=%d", del.Status, del.Code)
	}

	// 重复删除 → 404/15001。
	if del2 := recCall(t, base, "DELETE", fmt.Sprintf("/admin/recommend-positions/%d", p.Id), adminToken, nil); del2.Status != 404 || del2.Code != 15001 {
		t.Fatalf("re-delete: status=%d code=%d want 404/15001", del2.Status, del2.Code)
	}
	// 不存在详情 → 404/15001。
	if miss := recCall(t, base, "GET", "/admin/recommend-positions/999999", adminToken, nil); miss.Status != 404 || miss.Code != 15001 {
		t.Fatalf("missing detail: status=%d code=%d want 404/15001", miss.Status, miss.Code)
	}
	// 不存在更新 → 404/15001。
	if missUpd := recCall(t, base, "PUT", "/admin/recommend-positions/999999", adminToken, map[string]any{"name": "x"}); missUpd.Status != 404 || missUpd.Code != 15001 {
		t.Fatalf("missing update: status=%d code=%d want 404/15001", missUpd.Status, missUpd.Code)
	}
}

// TestRecommendationInvalidInputRejected 覆盖错误语义（15003）：非法 code/name/status 统一 400 且无写入。
func TestRecommendationInvalidInputRejected(t *testing.T) {
	base := setupRecommendationServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	cases := []map[string]any{
		{"code": "", "name": "x"},                 // code 为空
		{"code": "UPPER", "name": "x"},            // code 含大写
		{"code": "a b", "name": "x"},              // code 含空格
		{"code": "-abc", "name": "x"},             // code 以连字符开头
		{"code": "abc", "name": ""},               // name 为空
		{"code": "abc", "name": "   "},            // name trim 后为空
		{"code": "abc", "name": "x", "status": 2}, // status 非法
	}
	for i, body := range cases {
		res := recCall(t, base, "POST", "/admin/recommend-positions", adminToken, body)
		if res.Status != 400 || res.Code != 15003 {
			t.Fatalf("case %d: status=%d code=%d want 400/15003", i, res.Status, res.Code)
		}
	}
	if n, _ := g.DB().Model("recommend_positions").Ctx(context.Background()).Count(); n != 0 {
		t.Fatalf("invalid input must not write, count=%d", n)
	}
}

// TestRecommendationItemManagement 覆盖 AC-003/AC-005/AC-007 与 INV-004/INV-005：
// 添加/移除/调整排序；商品不存在拒绝（4001）；draft/off_shelf 可加入；下架后关系保留、后台可见、前台过滤。
func TestRecommendationItemManagement(t *testing.T) {
	base := setupRecommendationServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	cat := recInsertCategory(t, "推荐位商品管理分类")
	p1 := recInsertProduct(t, "管理商品1", cat, 100, 1) // on_shelf
	p2 := recInsertProduct(t, "管理商品2", cat, 200, 2) // off_shelf（允许加入）
	p3 := recInsertProduct(t, "管理商品3", cat, 300, 0) // draft（允许加入）

	posID := recCreatePosition(t, base, adminToken, "manage", "运营位", 1)

	// 添加 on_shelf/off_shelf/draft 商品均成功（仅校验存在）。
	for _, pid := range []int64{p1, p2, p3} {
		res := recCall(t, base, "POST", fmt.Sprintf("/admin/recommend-positions/%d/items", posID), adminToken, map[string]any{"product_id": pid})
		if res.Status != 200 || res.Code != 0 {
			t.Fatalf("add item %d: status=%d code=%d msg=%q", pid, res.Status, res.Code, res.Message)
		}
	}

	// 添加不存在商品 → 404/4001，无写入。
	miss := recCall(t, base, "POST", fmt.Sprintf("/admin/recommend-positions/%d/items", posID), adminToken, map[string]any{"product_id": 999999})
	if miss.Status != 404 || miss.Code != 4001 {
		t.Fatalf("add missing product: status=%d code=%d want 404/4001", miss.Status, miss.Code)
	}

	// 前台仅 on_shelf（p1），off_shelf/draft 不展示。
	fres := recCall(t, base, "GET", "/recommendations/manage", "", nil)
	var fd recFrontendData
	recDecode(t, fres.Data, &fd)
	if len(fd.Items) != 1 || fd.Items[0].ProductId != p1 {
		t.Fatalf("frontend should only contain p1, got %+v", fd.Items)
	}

	// 后台详情可见全部 3 条关系（含 off_shelf/draft）。
	dres := recCall(t, base, "GET", fmt.Sprintf("/admin/recommend-positions/%d", posID), adminToken, nil)
	var dd recDetailData
	recDecode(t, dres.Data, &dd)
	if len(dd.Items) != 3 {
		t.Fatalf("admin detail items=%d want 3", len(dd.Items))
	}

	// 调整排序：p1,p2,p3 → 按 p3,p1,p2 顺序。
	sortRes := recCall(t, base, "PUT", fmt.Sprintf("/admin/recommend-positions/%d/items/sort", posID), adminToken, map[string]any{
		"product_ids": []int64{p3, p1, p2},
	})
	if sortRes.Status != 200 || sortRes.Code != 0 {
		t.Fatalf("update sort: status=%d code=%d msg=%q", sortRes.Status, sortRes.Code, sortRes.Message)
	}
	var sr struct {
		Items []*recItem `json:"items"`
	}
	recDecode(t, sortRes.Data, &sr)
	if len(sr.Items) != 3 || sr.Items[0].ProductId != p3 || sr.Items[1].ProductId != p1 || sr.Items[2].ProductId != p2 {
		t.Fatalf("sort result wrong: %+v", sr.Items)
	}

	// 移除商品 p2。
	rm := recCall(t, base, "DELETE", fmt.Sprintf("/admin/recommend-positions/%d/items/%d", posID, p2), adminToken, nil)
	if rm.Status != 200 || rm.Code != 0 {
		t.Fatalf("remove item: status=%d code=%d", rm.Status, rm.Code)
	}
	// 重复移除 → 404/15004。
	if rm2 := recCall(t, base, "DELETE", fmt.Sprintf("/admin/recommend-positions/%d/items/%d", posID, p2), adminToken, nil); rm2.Status != 404 || rm2.Code != 15004 {
		t.Fatalf("re-remove: status=%d code=%d want 404/15004", rm2.Status, rm2.Code)
	}

	// 商品 p1 下架：关系保留，前台过滤，后台仍可见。
	if _, err := g.DB().Model("products").Ctx(context.Background()).Where("id", p1).Data(g.Map{"status": 2}).Update(); err != nil {
		t.Fatalf("off-shelf product p1: %v", err)
	}
	fres2 := recCall(t, base, "GET", "/recommendations/manage", "", nil)
	var fd2 recFrontendData
	recDecode(t, fres2.Data, &fd2)
	if len(fd2.Items) != 0 {
		t.Fatalf("after off-shelf, frontend should be empty, got %+v", fd2.Items)
	}
	dres2 := recCall(t, base, "GET", fmt.Sprintf("/admin/recommend-positions/%d", posID), adminToken, nil)
	var dd2 recDetailData
	recDecode(t, dres2.Data, &dd2)
	// 移除 p2 后剩 p1、p3 两条关系；p1 虽下架仍保留。
	if len(dd2.Items) != 2 {
		t.Fatalf("after off-shelf, admin items=%d want 2（关系保留）", len(dd2.Items))
	}
}

// TestRecommendationDuplicateRejected 覆盖 AC-004 与 INV-002：
// 同一商品重复加入同一推荐位被拒（15005）且不产生重复关系。
func TestRecommendationDuplicateRejected(t *testing.T) {
	base := setupRecommendationServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	cat := recInsertCategory(t, "重复添加分类")
	p := recInsertProduct(t, "重复商品", cat, 100, 1)
	posID := recCreatePosition(t, base, adminToken, "dup", "去重位", 1)

	first := recCall(t, base, "POST", fmt.Sprintf("/admin/recommend-positions/%d/items", posID), adminToken, map[string]any{"product_id": p})
	if first.Status != 200 || first.Code != 0 {
		t.Fatalf("first add: status=%d code=%d", first.Status, first.Code)
	}
	second := recCall(t, base, "POST", fmt.Sprintf("/admin/recommend-positions/%d/items", posID), adminToken, map[string]any{"product_id": p})
	if second.Status != 409 || second.Code != 15005 {
		t.Fatalf("duplicate add: status=%d code=%d want 409/15005", second.Status, second.Code)
	}

	// 关系仅一条。
	if n, _ := g.DB().Model("recommend_items").Ctx(context.Background()).Where("position_id", posID).Where("product_id", p).Count(); n != 1 {
		t.Fatalf("recommend_items count=%d want 1", n)
	}
}

// TestRecommendationDeleteCascades 覆盖 INV-006：物理删除推荐位后其推荐商品关系一并删除，不留孤儿。
func TestRecommendationDeleteCascades(t *testing.T) {
	base := setupRecommendationServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	cat := recInsertCategory(t, "级联删除分类")
	p1 := recInsertProduct(t, "级联商品1", cat, 100, 1)
	p2 := recInsertProduct(t, "级联商品2", cat, 200, 1)
	posID := recCreatePosition(t, base, adminToken, "cascade", "级联位", 1)
	for _, pid := range []int64{p1, p2} {
		recCall(t, base, "POST", fmt.Sprintf("/admin/recommend-positions/%d/items", posID), adminToken, map[string]any{"product_id": pid})
	}

	del := recCall(t, base, "DELETE", fmt.Sprintf("/admin/recommend-positions/%d", posID), adminToken, nil)
	if del.Status != 200 || del.Code != 0 {
		t.Fatalf("delete: status=%d code=%d", del.Status, del.Code)
	}
	if n, _ := g.DB().Model("recommend_items").Ctx(context.Background()).Where("position_id", posID).Count(); n != 0 {
		t.Fatalf("cascade delete failed: recommend_items count=%d want 0", n)
	}
}

// TestRecommendationPermissionIsolation 覆盖 AC-008 与 INV-003：
// recommend:* 权限已 seed；未认证 401、无权限 403，且均不产生写入。
func TestRecommendationPermissionIsolation(t *testing.T) {
	base := setupRecommendationServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 权限 seed 已登记。
	for _, code := range []string{"recommend:create", "recommend:update", "recommend:delete", "recommend:item"} {
		n, err := g.DB().Model("permissions").Ctx(context.Background()).Where("code", code).Count()
		if err != nil {
			t.Fatalf("query permission %s: %v", code, err)
		}
		if n != 1 {
			t.Fatalf("permission %s should be seeded, count=%d", code, n)
		}
	}

	// 超管创建成功。
	create := recCall(t, base, "POST", "/admin/recommend-positions", adminToken, map[string]any{"code": "home", "name": "首页"})
	if create.Status != 200 || create.Code != 0 {
		t.Fatalf("super admin create: status=%d code=%d", create.Status, create.Code)
	}
	var p recPosition
	recDecode(t, create.Data, &p)

	// 无权限普通管理员：创建推荐位 → 403/1003。
	isoInsertAdmin(t, "recplain", "recplain123")
	plainToken, _ := isoAdminLogin(t, base, "recplain", "recplain123")
	forbidden := recCall(t, base, "POST", "/admin/recommend-positions", plainToken, map[string]any{"code": "x", "name": "y"})
	if forbidden.Status != 403 || forbidden.Code != 1003 {
		t.Fatalf("plain admin create: status=%d code=%d want 403/1003", forbidden.Status, forbidden.Code)
	}

	// 无权限普通管理员：添加推荐商品 → 403/1003。
	itemForbidden := recCall(t, base, "POST", fmt.Sprintf("/admin/recommend-positions/%d/items", p.Id), plainToken, map[string]any{"product_id": 1})
	if itemForbidden.Status != 403 || itemForbidden.Code != 1003 {
		t.Fatalf("plain admin add item: status=%d code=%d want 403/1003", itemForbidden.Status, itemForbidden.Code)
	}

	// 未认证：创建推荐位 → 401/1002。
	unauth := recCall(t, base, "POST", "/admin/recommend-positions", "", map[string]any{"code": "x", "name": "y"})
	if unauth.Status != 401 || unauth.Code != 1002 {
		t.Fatalf("unauth create: status=%d code=%d want 401/1002", unauth.Status, unauth.Code)
	}

	// 推荐位仍只有超管创建的那 1 条，无多余写入。
	if n, _ := g.DB().Model("recommend_positions").Ctx(context.Background()).Count(); n != 1 {
		t.Fatalf("recommend_positions count=%d want 1", n)
	}
	if n, _ := g.DB().Model("recommend_items").Ctx(context.Background()).Count(); n != 0 {
		t.Fatalf("recommend_items count=%d want 0（无权限不得写入）", n)
	}
}
