package member_test

// 会员与分销 API 的 HTTP 层集成测试（真实 MySQL/Redis + 真实路由 + 真实中间件）。
//
// 覆盖：未认证 401、普通用户访问后台 403、后台权限码生效、会员等级配置读写、
// 本人会员信息与折扣权益、分销绑定（自荐拒绝）与佣金查询鉴权。
//
// 这里不 Mock 认证与 RBAC：必须走真实的 Auth / AdminAuth / RequirePermission。

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
	"cnb.cool/go-cloud-devops/my-shop/internal/cmd"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

const (
	testJWTSecret     = "test-secret-0123456789-0123456789-0123456789"
	testAdminPassword = "test-admin-password-123"
	testSuperUsername = "admin"
)

// apiResult 是统一响应体的解析结果。
type apiResult struct {
	Status  int
	Code    int
	Message string
	Data    json.RawMessage
}

// decode 解析 {code,message,data} 响应。
func decode(r *gclient.Response) apiResult {
	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(r.ReadAll(), &env)
	return apiResult{Status: r.StatusCode, Code: env.Code, Message: env.Message, Data: env.Data}
}

// doRequest 发起请求并返回解析结果。
func doRequest(t *testing.T, base, method, path string, body any, headers map[string]string) apiResult {
	t.Helper()
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
		t.Fatalf("unsupported method %s", method)
	}
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer r.Close()
	return decode(r)
}

// authHeader 构造 Bearer 认证头。
func authHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// setupServer 迁移 + Bootstrap + 清理数据 + 启动真实路由，返回 base 与超管 token。
func setupServer(t *testing.T) (base, adminToken string) {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)
	t.Setenv("REDIS_DEFAULT_DB", "9")

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	clean := func() {
		for _, table := range []string{
			"admin_roles", "role_permissions", "roles", "permissions", "admins",
			"distribution_commissions", "user_referrals",
			"member_growth_logs", "user_memberships", "member_levels",
			"order_items", "orders", "addresses", "users",
		} {
			if _, err := g.DB().Exec(context.Background(), "DELETE FROM "+table); err != nil {
				t.Fatalf("clean %s: %v", table, err)
			}
		}
	}
	clean()
	t.Cleanup(clean)
	// 权限与超管依赖 seed，清理后重建。
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("rebootstrap: %v", err)
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
	if err := s.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown() })
	time.Sleep(100 * time.Millisecond)

	base = fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
	adminToken = loginAdmin(t, base, testSuperUsername, testAdminPassword)
	return base, adminToken
}

// loginAdmin 登录管理员并返回 access token。
func loginAdmin(t *testing.T, base, username, password string) string {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/login", map[string]any{"username": username, "password": password}, nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("admin login: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	var d struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal admin login: %v", err)
	}
	return d.AccessToken
}

// registerLoginUser 注册并登录前台用户，返回 (token, userID)。
func registerLoginUser(t *testing.T, base, username, password string) (string, int64) {
	t.Helper()
	res := doRequest(t, base, "POST", "/register", map[string]any{"username": username, "password": password}, nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("register %s: status=%d code=%d", username, res.Status, res.Code)
	}
	var reg struct {
		Id int64 `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &reg); err != nil {
		t.Fatalf("unmarshal register: %v", err)
	}
	login := doRequest(t, base, "POST", "/login", map[string]any{"username": username, "password": password}, nil)
	if login.Status != 200 || login.Code != 0 {
		t.Fatalf("login %s: status=%d code=%d", username, login.Status, login.Code)
	}
	var d struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(login.Data, &d); err != nil {
		t.Fatalf("unmarshal login: %v", err)
	}
	return d.AccessToken, reg.Id
}

// TestMemberAndDistributionAuth 覆盖认证与 RBAC 边界。
func TestMemberAndDistributionAuth(t *testing.T) {
	base, adminToken := setupServer(t)
	userToken, _ := registerLoginUser(t, base, "apiuser", "password123")

	// 未认证：全部 401/1002。
	for _, path := range []string{"/members/levels", "/my/membership", "/my/membership/growth-logs", "/my/referral", "/my/commissions", "/my/commissions/summary"} {
		res := doRequest(t, base, "GET", path, nil, nil)
		if res.Status != 401 || res.Code != 1002 {
			t.Fatalf("%s 未认证期望 401/1002，实际 %d/%d", path, res.Status, res.Code)
		}
	}
	if res := doRequest(t, base, "POST", "/referrals/bind", map[string]any{"referrer_user_id": 1}, nil); res.Status != 401 || res.Code != 1002 {
		t.Fatalf("未认证绑定期望 401/1002，实际 %d/%d", res.Status, res.Code)
	}

	// 普通用户 token 访问后台：403/1003。
	adminPaths := []struct{ method, path string }{
		{"GET", "/admin/member-levels"},
		{"POST", "/admin/member-levels"},
		{"PUT", "/admin/member-levels/1"},
		{"DELETE", "/admin/member-levels/1"},
		{"GET", "/admin/members/1"},
		{"GET", "/admin/referrals"},
		{"GET", "/admin/commissions"},
		{"GET", "/admin/commissions/1"},
		{"POST", "/admin/commissions/1/settle"},
	}
	for _, ap := range adminPaths {
		res := doRequest(t, base, ap.method, ap.path, map[string]any{}, authHeader(userToken))
		if res.Status != 403 || res.Code != 1003 {
			t.Fatalf("%s %s 普通用户期望 403/1003，实际 %d/%d", ap.method, ap.path, res.Status, res.Code)
		}
	}

	// 超管可读后台列表。
	if res := doRequest(t, base, "GET", "/admin/member-levels", nil, authHeader(adminToken)); res.Status != 200 || res.Code != 0 {
		t.Fatalf("超管查询等级期望 200/0，实际 %d/%d", res.Status, res.Code)
	}
}

// TestMemberAPIContract 覆盖会员等级维护与本人权益查询的 HTTP 契约。
func TestMemberAPIContract(t *testing.T) {
	base, adminToken := setupServer(t)
	userToken, userID := registerLoginUser(t, base, "memberapi", "password123")

	// 空配置：前台列表为空、本人会员信息无等级且不打折。
	list := doRequest(t, base, "GET", "/members/levels", nil, authHeader(userToken))
	if list.Status != 200 || list.Code != 0 {
		t.Fatalf("查询等级列表: %d/%d", list.Status, list.Code)
	}
	my := doRequest(t, base, "GET", "/my/membership", nil, authHeader(userToken))
	if my.Status != 200 || my.Code != 0 {
		t.Fatalf("查询本人会员: %d/%d", my.Status, my.Code)
	}
	var myData struct {
		LevelId        *int64 `json:"level_id"`
		DiscountRateBp int    `json:"discount_rate_bp"`
		GrowthValue    int64  `json:"growth_value"`
	}
	if err := json.Unmarshal(my.Data, &myData); err != nil {
		t.Fatalf("unmarshal membership: %v", err)
	}
	if myData.LevelId != nil || myData.DiscountRateBp != 10000 || myData.GrowthValue != 0 {
		t.Fatalf("无配置时期望空等级 + 10000 折扣，实际 %+v", myData)
	}

	// 创建等级：200 + 返回配置。
	created := doRequest(t, base, "POST", "/admin/member-levels", map[string]any{
		"level_no": 1, "name": "青铜", "growth_threshold": 0, "discount_rate_bp": 9800, "status": 1,
	}, authHeader(adminToken))
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("创建等级: %d/%d %s", created.Status, created.Code, created.Message)
	}
	var level struct {
		Id             int64 `json:"id"`
		DiscountRateBp int   `json:"discount_rate_bp"`
	}
	if err := json.Unmarshal(created.Data, &level); err != nil {
		t.Fatalf("unmarshal level: %v", err)
	}
	if level.Id <= 0 || level.DiscountRateBp != 9800 {
		t.Fatalf("创建等级结果不符: %+v", level)
	}

	// 重复序号：409/20004。
	dup := doRequest(t, base, "POST", "/admin/member-levels", map[string]any{
		"level_no": 1, "name": "重复", "growth_threshold": 100, "discount_rate_bp": 9000, "status": 1,
	}, authHeader(adminToken))
	if dup.Status != 409 || dup.Code != 20004 {
		t.Fatalf("重复序号期望 409/20004，实际 %d/%d", dup.Status, dup.Code)
	}

	// 非法折扣率：400/20002。
	bad := doRequest(t, base, "POST", "/admin/member-levels", map[string]any{
		"level_no": 2, "name": "越界", "growth_threshold": 100, "discount_rate_bp": 0, "status": 1,
	}, authHeader(adminToken))
	if bad.Status != 400 || bad.Code != 20002 {
		t.Fatalf("非法折扣率期望 400/20002，实际 %d/%d", bad.Status, bad.Code)
	}

	// 前台可见启用等级；本人按门槛 0 享 9.8 折。
	after := doRequest(t, base, "GET", "/members/levels", nil, authHeader(userToken))
	var levels struct {
		Items []struct {
			LevelNo int `json:"level_no"`
		} `json:"items"`
	}
	if err := json.Unmarshal(after.Data, &levels); err != nil {
		t.Fatalf("unmarshal levels: %v", err)
	}
	if len(levels.Items) != 1 || levels.Items[0].LevelNo != 1 {
		t.Fatalf("启用等级列表不符: %+v", levels.Items)
	}
	my = doRequest(t, base, "GET", "/my/membership", nil, authHeader(userToken))
	if err := json.Unmarshal(my.Data, &myData); err != nil {
		t.Fatalf("unmarshal membership: %v", err)
	}
	if myData.DiscountRateBp != 9800 || myData.LevelId != nil {
		t.Fatalf("未建档用户应按成长值 0 解析 9800 折扣且 level_id 为 null，实际 %+v", myData)
	}

	// 后台查询该用户会员信息：has_record=false。
	adminMember := doRequest(t, base, "GET", fmt.Sprintf("/admin/members/%d", userID), nil, authHeader(adminToken))
	if adminMember.Status != 200 || adminMember.Code != 0 {
		t.Fatalf("后台查询会员信息: %d/%d", adminMember.Status, adminMember.Code)
	}
	var adminData struct {
		HasRecord bool `json:"has_record"`
	}
	if err := json.Unmarshal(adminMember.Data, &adminData); err != nil {
		t.Fatalf("unmarshal admin member: %v", err)
	}
	if adminData.HasRecord {
		t.Fatal("未消费用户 has_record 应为 false")
	}
}

// TestDistributionAPIContract 覆盖分销绑定的拒绝路径与佣金查询契约。
func TestDistributionAPIContract(t *testing.T) {
	base, adminToken := setupServer(t)
	inviteeToken, inviteeID := registerLoginUser(t, base, "distinviteeapi", "password123")
	inviterToken, inviterID := registerLoginUser(t, base, "distinviterapi", "password123")

	// 自荐：400/21003。
	self := doRequest(t, base, "POST", "/referrals/bind", map[string]any{"referrer_user_id": inviteeID}, authHeader(inviteeToken))
	if self.Status != 400 || self.Code != 21003 {
		t.Fatalf("自荐期望 400/21003，实际 %d/%d", self.Status, self.Code)
	}
	// 推荐人不存在：400/21005。
	missing := doRequest(t, base, "POST", "/referrals/bind", map[string]any{"referrer_user_id": 999999}, authHeader(inviteeToken))
	if missing.Status != 400 || missing.Code != 21005 {
		t.Fatalf("推荐人不存在期望 400/21005，实际 %d/%d", missing.Status, missing.Code)
	}
	// 正常绑定。
	ok := doRequest(t, base, "POST", "/referrals/bind", map[string]any{"referrer_user_id": inviterID}, authHeader(inviteeToken))
	if ok.Status != 200 || ok.Code != 0 {
		t.Fatalf("绑定期望 200/0，实际 %d/%d %s", ok.Status, ok.Code, ok.Message)
	}
	// 改绑：409/21002。
	rebind := doRequest(t, base, "POST", "/referrals/bind", map[string]any{"referrer_user_id": 999999}, authHeader(inviteeToken))
	if rebind.Status != 400 && rebind.Status != 409 {
		t.Fatalf("改绑应被拒绝（400/409），实际 %d/%d", rebind.Status, rebind.Code)
	}
	// 反向绑成环：409/21004。
	cycle := doRequest(t, base, "POST", "/referrals/bind", map[string]any{"referrer_user_id": inviteeID}, authHeader(inviterToken))
	if cycle.Status != 409 || cycle.Code != 21004 {
		t.Fatalf("成环期望 409/21004，实际 %d/%d", cycle.Status, cycle.Code)
	}

	// 我的推荐关系：已绑定。
	myRef := doRequest(t, base, "GET", "/my/referral", nil, authHeader(inviteeToken))
	if myRef.Status != 200 || myRef.Code != 0 {
		t.Fatalf("我的推荐关系: %d/%d", myRef.Status, myRef.Code)
	}
	var refData struct {
		Bound            bool   `json:"bound"`
		ReferrerUserId   *int64 `json:"referrer_user_id"`
		ReferrerUsername string `json:"referrer_username"`
		InviteeCount     int    `json:"invitee_count"`
	}
	if err := json.Unmarshal(myRef.Data, &refData); err != nil {
		t.Fatalf("unmarshal referral: %v", err)
	}
	if !refData.Bound || refData.ReferrerUserId == nil || *refData.ReferrerUserId != inviterID || refData.ReferrerUsername != "distinviterapi" {
		t.Fatalf("推荐关系不符: %+v", refData)
	}

	// 我的佣金：空列表 + 全零汇总。
	mine := doRequest(t, base, "GET", "/my/commissions?page=1&size=10", nil, authHeader(inviterToken))
	if mine.Status != 200 || mine.Code != 0 {
		t.Fatalf("我的佣金: %d/%d", mine.Status, mine.Code)
	}
	var listData struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(mine.Data, &listData); err != nil {
		t.Fatalf("unmarshal commissions: %v", err)
	}
	if listData.Total != 0 {
		t.Fatalf("初始佣金应为 0，实际 %d", listData.Total)
	}
	// 非法状态过滤：400/1001。
	badStatus := doRequest(t, base, "GET", "/my/commissions?status=bogus", nil, authHeader(inviterToken))
	if badStatus.Status != 400 || badStatus.Code != 1001 {
		t.Fatalf("非法状态过滤期望 400/1001，实际 %d/%d", badStatus.Status, badStatus.Code)
	}
	summary := doRequest(t, base, "GET", "/my/commissions/summary", nil, authHeader(inviterToken))
	if summary.Status != 200 || summary.Code != 0 {
		t.Fatalf("佣金汇总: %d/%d", summary.Status, summary.Code)
	}

	// 后台佣金列表与推荐关系列表可读。
	if res := doRequest(t, base, "GET", "/admin/commissions", nil, authHeader(adminToken)); res.Status != 200 || res.Code != 0 {
		t.Fatalf("后台佣金列表: %d/%d", res.Status, res.Code)
	}
	refs := doRequest(t, base, "GET", "/admin/referrals", nil, authHeader(adminToken))
	if refs.Status != 200 || refs.Code != 0 {
		t.Fatalf("后台推荐关系: %d/%d", refs.Status, refs.Code)
	}
	var adminRefs struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(refs.Data, &adminRefs); err != nil {
		t.Fatalf("unmarshal admin referrals: %v", err)
	}
	if adminRefs.Total != 1 {
		t.Fatalf("后台推荐关系应有 1 条，实际 %d", adminRefs.Total)
	}
}
