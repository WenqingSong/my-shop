package bargain_test

// 本文件是砍价 API 的真实 HTTP 集成测试：真实 Gin/GoFrame 路由 + 真实 MySQL + 真实 Redis 会话。
// 覆盖认证/权限、错误码、响应结构与端到端状态推进；业务不变量与并发由
// internal/logic/bargain/integration_test.go 在数据库层证明。
//
// 运行前需准备隔离 MySQL（DATABASE_DEFAULT_*）与 Redis（默认 127.0.0.1:6379）。

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gtime"
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
	testUserPassword  = "test-user-password-123"
)

// apiResult 是统一响应信封。
type apiResult struct {
	Status  int
	Code    int
	Message string
	Data    json.RawMessage
}

// nameSeq 为测试用户名/商品名提供进程内唯一序号。
var nameSeq atomic.Int64

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, nameSeq.Add(1))
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

func doRequest(t *testing.T, base, method, path string, body any, token string) apiResult {
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
	default:
		t.Fatalf("不支持的方法 %s", method)
	}
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer r.Close()
	return decode(r)
}

// requireStatus 断言 HTTP 状态与业务码。
func requireStatus(t *testing.T, res apiResult, status, code int, op string) {
	t.Helper()
	if res.Status != status || res.Code != code {
		t.Fatalf("%s: status=%d code=%d msg=%q, want %d/%d", op, res.Status, res.Code, res.Message, status, code)
	}
}

// setupServer 迁移 + Bootstrap + 挂载真实路由，返回 base 地址与超级管理员 token。
func setupServer(t *testing.T) (base, adminToken string) {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)
	t.Setenv("REDIS_DEFAULT_DB", "5")

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// 清理砍价与目录数据（外键顺序：子表先删）。
	for _, table := range []string{
		"bargain_help_records", "bargain_instances", "bargain_activities",
		"skus", "product_images", "products", "categories",
	} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("清理 %s: %v", table, err)
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
	if err := s.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Shutdown()
		for _, table := range []string{"bargain_help_records", "bargain_instances", "bargain_activities", "skus", "product_images", "products", "categories"} {
			_, _ = g.DB().Exec(context.Background(), "DELETE FROM "+table)
		}
	})

	base = fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
	adminToken = loginAdmin(t, base, testSuperUsername, testAdminPassword)
	return base, adminToken
}

func loginAdmin(t *testing.T, base, username, password string) string {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/login", map[string]any{"username": username, "password": password}, "")
	requireStatus(t, res, 200, 0, "管理员登录")
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(res.Data, &payload); err != nil {
		t.Fatalf("解析管理员登录响应: %v", err)
	}
	return payload.AccessToken
}

// registerUser 注册并登录一个前台用户，返回 token。
func registerUser(t *testing.T, base string) string {
	t.Helper()
	username := uniqueName("bgu")
	res := doRequest(t, base, "POST", "/register", map[string]any{"username": username, "password": testUserPassword}, "")
	requireStatus(t, res, 200, 0, "注册用户")
	res = doRequest(t, base, "POST", "/login", map[string]any{"username": username, "password": testUserPassword}, "")
	requireStatus(t, res, 200, 0, "用户登录")
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(res.Data, &payload); err != nil {
		t.Fatalf("解析用户登录响应: %v", err)
	}
	return payload.AccessToken
}

// createCategoryProductSku 直接写入目录数据，避免测试依赖商品模块的建号流程。
func createCategoryProductSku(t *testing.T, price int64) (productID, skuID int64) {
	t.Helper()
	ctx := context.Background()
	categoryID, err := g.DB().Model("categories").Ctx(ctx).Data(g.Map{
		"name": uniqueName("砍价分类"), "parent_id": 0, "sort": 0, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("创建分类: %v", err)
	}
	productID, err = g.DB().Model("products").Ctx(ctx).Data(g.Map{
		"name": uniqueName("砍价商品"), "category_id": categoryID, "price": price, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("创建商品: %v", err)
	}
	skuID, err = g.DB().Model("skus").Ctx(ctx).Data(g.Map{
		"product_id": productID, "name": "默认规格", "price": price, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("创建 SKU: %v", err)
	}
	return productID, skuID
}

func createActivityRequest(productID, skuID int64, startPrice, floorPrice, helpAmount, maxHelpCount int64) map[string]any {
	return map[string]any{
		"name":           uniqueName("砍价活动"),
		"product_id":     productID,
		"sku_id":         skuID,
		"start_price":    startPrice,
		"floor_price":    floorPrice,
		"help_amount":    helpAmount,
		"max_help_count": maxHelpCount,
		"start_time":     gtime.NewFromTime(time.Now().Add(-time.Hour)).Format("Y-m-d H:i:s"),
		"end_time":       gtime.NewFromTime(time.Now().Add(24 * time.Hour)).Format("Y-m-d H:i:s"),
	}
}

type activityPayload struct {
	Id          int64  `json:"id"`
	Status      int    `json:"status"`
	ProductName string `json:"product_name"`
}
type instancePayload struct {
	Id              int64  `json:"id"`
	Status          string `json:"status"`
	CurrentPrice    int64  `json:"current_price"`
	RemainingAmount int64  `json:"remaining_amount"`
	HelpedCount     int64  `json:"helped_count"`
	FloorPrice      int64  `json:"floor_price"`
	OrderId         *int64 `json:"order_id"`
	ProductName     string `json:"product_name"`
}

func createActivity(t *testing.T, base, adminToken string, body map[string]any) activityPayload {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/bargain-activities", body, adminToken)
	requireStatus(t, res, 200, 0, "创建砍价活动")
	var payload activityPayload
	if err := json.Unmarshal(res.Data, &payload); err != nil {
		t.Fatalf("解析活动响应: %v", err)
	}
	return payload
}

// TestBargainHTTPFlow 覆盖认证/权限、错误码、公开查询与端到端砍价流程。
func TestBargainHTTPFlow(t *testing.T) {
	base, adminToken := setupServer(t)
	productID, skuID := createCategoryProductSku(t, 10000)

	// 认证与权限：未登录 401、普通用户访问后台 403、无角色管理员 403。
	t.Run("认证与权限", func(t *testing.T) {
		requireStatus(t, doRequest(t, base, "GET", "/my/bargain-instances", nil, ""), 401, 1002, "未登录查询我的砍价")
		userToken := registerUser(t, base)
		requireStatus(t, doRequest(t, base, "POST", "/admin/bargain-activities", createActivityRequest(productID, skuID, 10000, 9000, 100, 20), userToken), 403, 1003, "用户 token 访问后台")

		// 无角色管理员：认证通过但无 bargain:create 权限。
		adminUsername := uniqueName("bga")
		res := doRequest(t, base, "POST", "/admin/admins", map[string]any{"username": adminUsername, "password": testAdminPassword}, adminToken)
		requireStatus(t, res, 200, 0, "创建无角色管理员")
		limitedToken := loginAdmin(t, base, adminUsername, testAdminPassword)
		requireStatus(t, doRequest(t, base, "POST", "/admin/bargain-activities", createActivityRequest(productID, skuID, 10000, 9000, 100, 20), limitedToken), 403, 1003, "无权限管理员创建活动")
	})

	// 活动管理：创建 → 详情 → 列表 → 编辑 → 停用。
	activity := createActivity(t, base, adminToken, createActivityRequest(productID, skuID, 10000, 9000, 100, 20))
	if activity.Id <= 0 || activity.ProductName == "" || activity.Status != 1 {
		t.Fatalf("创建活动响应异常: %+v", activity)
	}

	t.Run("活动查询与编辑", func(t *testing.T) {
		res := doRequest(t, base, "GET", fmt.Sprintf("/admin/bargain-activities/%d", activity.Id), nil, adminToken)
		requireStatus(t, res, 200, 0, "后台活动详情")

		res = doRequest(t, base, "GET", "/admin/bargain-activities?page=1&size=10", nil, adminToken)
		requireStatus(t, res, 200, 0, "后台活动列表")

		publicRes := doRequest(t, base, "GET", "/bargain-activities", nil, "")
		requireStatus(t, publicRes, 200, 0, "前台可参与活动列表")
		var list struct {
			Items []struct {
				Id int64 `json:"id"`
			} `json:"items"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(publicRes.Data, &list); err != nil {
			t.Fatalf("解析前台活动列表: %v", err)
		}
		if list.Total < 1 || list.Items[0].Id != activity.Id {
			t.Fatalf("前台列表应包含新活动: %+v", list)
		}

		update := createActivityRequest(productID, skuID, 10000, 9000, 100, 20)
		update["name"] = uniqueName("改名后的砍价活动")
		res = doRequest(t, base, "PUT", fmt.Sprintf("/admin/bargain-activities/%d", activity.Id), update, adminToken)
		requireStatus(t, res, 200, 0, "编辑砍价活动")

		// 改绑商品被拒绝：21002。
		otherProductID, otherSkuID := createCategoryProductSku(t, 10000)
		rebind := createActivityRequest(otherProductID, otherSkuID, 10000, 9000, 100, 20)
		res = doRequest(t, base, "PUT", fmt.Sprintf("/admin/bargain-activities/%d", activity.Id), rebind, adminToken)
		requireStatus(t, res, 400, 21002, "编辑活动改绑商品")
	})

	// 发起 → 自助拒绝 → 重复助力 → 助力 → 越权放弃 → 放弃。
	ownerToken := registerUser(t, base)
	helperToken := registerUser(t, base)

	var instanceID int64
	t.Run("发起砍价", func(t *testing.T) {
		res := doRequest(t, base, "POST", fmt.Sprintf("/bargain-activities/%d/instances", activity.Id), nil, ownerToken)
		requireStatus(t, res, 200, 0, "发起砍价")
		var inst instancePayload
		if err := json.Unmarshal(res.Data, &inst); err != nil {
			t.Fatalf("解析实例: %v", err)
		}
		if inst.Status != "in_progress" || inst.CurrentPrice != 10000 || inst.RemainingAmount != 1000 || inst.OrderId != nil {
			t.Fatalf("实例快照异常: %+v", inst)
		}
		instanceID = inst.Id

		res = doRequest(t, base, "POST", fmt.Sprintf("/bargain-activities/%d/instances", activity.Id), nil, ownerToken)
		requireStatus(t, res, 409, 21005, "重复发起砍价")

		res = doRequest(t, base, "GET", "/bargain-instances/999999", nil, ownerToken)
		requireStatus(t, res, 404, 21004, "查询不存在实例")
	})

	t.Run("助力与重复助力", func(t *testing.T) {
		res := doRequest(t, base, "POST", fmt.Sprintf("/bargain-instances/%d/help", instanceID), nil, ownerToken)
		requireStatus(t, res, 409, 21006, "给自己助力")

		res = doRequest(t, base, "POST", fmt.Sprintf("/bargain-instances/%d/help", instanceID), nil, helperToken)
		requireStatus(t, res, 200, 0, "好友助力")
		var payload struct {
			CurrentPrice int64 `json:"current_price"`
			HelpedCount  int64 `json:"helped_count"`
			Help         struct {
				CutAmount int64 `json:"cut_amount"`
			} `json:"help"`
		}
		if err := json.Unmarshal(res.Data, &payload); err != nil {
			t.Fatalf("解析助力响应: %v", err)
		}
		if payload.CurrentPrice != 9900 || payload.HelpedCount != 1 || payload.Help.CutAmount != 100 {
			t.Fatalf("助力结果异常: %+v", payload)
		}

		res = doRequest(t, base, "POST", fmt.Sprintf("/bargain-instances/%d/help", instanceID), nil, helperToken)
		requireStatus(t, res, 409, 21008, "重复助力")

		res = doRequest(t, base, "GET", fmt.Sprintf("/bargain-instances/%d/helps", instanceID), nil, helperToken)
		requireStatus(t, res, 200, 0, "查询助力记录")
		var helps struct {
			Items []struct {
				HelperUserId int64 `json:"helper_user_id"`
			} `json:"items"`
		}
		if err := json.Unmarshal(res.Data, &helps); err != nil {
			t.Fatalf("解析助力记录: %v", err)
		}
		if len(helps.Items) != 1 {
			t.Fatalf("助力记录数 = %d, want 1", len(helps.Items))
		}

		res = doRequest(t, base, "GET", fmt.Sprintf("/bargain-instances/%d", instanceID), nil, helperToken)
		requireStatus(t, res, 200, 0, "好友查看实例详情")
	})

	t.Run("放弃砍价", func(t *testing.T) {
		otherToken := registerUser(t, base)
		requireStatus(t, doRequest(t, base, "POST", fmt.Sprintf("/bargain-instances/%d/abandon", instanceID), nil, otherToken), 404, 21004, "非本人放弃")

		res := doRequest(t, base, "POST", fmt.Sprintf("/bargain-instances/%d/abandon", instanceID), nil, ownerToken)
		requireStatus(t, res, 200, 0, "本人放弃")
		var inst instancePayload
		if err := json.Unmarshal(res.Data, &inst); err != nil {
			t.Fatalf("解析放弃响应: %v", err)
		}
		if inst.Status != "failed" {
			t.Fatalf("放弃后状态 = %q, want failed", inst.Status)
		}
		requireStatus(t, doRequest(t, base, "POST", fmt.Sprintf("/bargain-instances/%d/abandon", instanceID), nil, ownerToken), 409, 21009, "重复放弃")
	})

	// 成功路径：一次助力直达底价 → 成功，终态拒绝再次助力。
	t.Run("砍价成功", func(t *testing.T) {
		fast := createActivity(t, base, adminToken, createActivityRequest(productID, skuID, 10000, 9900, 500, 5))
		res := doRequest(t, base, "POST", fmt.Sprintf("/bargain-activities/%d/instances", fast.Id), nil, ownerToken)
		requireStatus(t, res, 200, 0, "发起砍价（快速成功活动）")
		var inst instancePayload
		if err := json.Unmarshal(res.Data, &inst); err != nil {
			t.Fatalf("解析实例: %v", err)
		}

		res = doRequest(t, base, "POST", fmt.Sprintf("/bargain-instances/%d/help", inst.Id), nil, helperToken)
		requireStatus(t, res, 200, 0, "助力直达底价")
		var payload struct {
			Status       string `json:"status"`
			CurrentPrice int64  `json:"current_price"`
			CompletedAt  string `json:"completed_at"`
			Help         struct {
				CutAmount int64 `json:"cut_amount"`
			} `json:"help"`
		}
		if err := json.Unmarshal(res.Data, &payload); err != nil {
			t.Fatalf("解析助力响应: %v", err)
		}
		if payload.Status != "success" || payload.CurrentPrice != 9900 || payload.Help.CutAmount != 100 || payload.CompletedAt == "" {
			t.Fatalf("成功语义异常: %+v", payload)
		}

		thirdToken := registerUser(t, base)
		requireStatus(t, doRequest(t, base, "POST", fmt.Sprintf("/bargain-instances/%d/help", inst.Id), nil, thirdToken), 409, 21007, "成功实例继续助力")

		res = doRequest(t, base, "GET", "/my/bargain-instances", nil, ownerToken)
		requireStatus(t, res, 200, 0, "查询我的砍价记录")
		var mine struct {
			Items []instancePayload `json:"items"`
			Total int               `json:"total"`
		}
		if err := json.Unmarshal(res.Data, &mine); err != nil {
			t.Fatalf("解析我的砍价记录: %v", err)
		}
		if mine.Total < 2 {
			t.Fatalf("我的砍价记录数 = %d, want ≥2（失败 + 成功各一条）", mine.Total)
		}
	})

	// 停用只阻止新发起，不影响已创建实例。
	t.Run("停用活动阻止新发起", func(t *testing.T) {
		requireStatus(t, doRequest(t, base, "POST", fmt.Sprintf("/admin/bargain-activities/%d/status", activity.Id), map[string]any{"status": 0}, adminToken), 200, 0, "停用活动")
		res := doRequest(t, base, "POST", fmt.Sprintf("/bargain-activities/%d/instances", activity.Id), nil, registerUser(t, base))
		requireStatus(t, res, 409, 21003, "停用活动发起砍价")
	})
}
