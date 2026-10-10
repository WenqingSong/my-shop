package cmd

// 本文件通过真实路由（RegisterAdminRoutes）+ 真实 MySQL/Redis 锁定「运营数据大屏」核心闭环，
// 覆盖 dashboard-v1 的 AC-001 至 AC-011 与关键不变量（INV-001 至 INV-006）。
// 统计口径与 docs/design/dashboard.md 一致：订单边界、金额/销量有效状态 {20,30,40,50}、北京时区左闭右开。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

// 订单状态机枚举（与 internal/logic/order 一致）。
const (
	dStatusPending   = 10 // 待支付（金额/销量无效）
	dStatusPaid      = 20 // 已支付（有效）
	dStatusShipped   = 30 // 已发货（有效）
	dStatusReceived  = 40 // 已收货（有效）
	dStatusCompleted = 50 // 已完成（有效）
	dStatusCancelled = 60 // 已取消（无效）
	dStatusRefunded  = 70 // 已退款（无效）
)

// dashboardBeijing 返回北京时区位置（与 dashboard.timezone 默认值一致）。
func dashboardBeijing(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}
	return loc
}

// dashboardDayStart 返回指定时刻在北京时区的当日 00:00:00。
func dashboardDayStart(t *testing.T, now time.Time) time.Time {
	t.Helper()
	local := now.In(dashboardBeijing(t))
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, dashboardBeijing(t))
}

// dashboardDT 将时刻格式化为 MySQL DATETIME 字符串。
func dashboardDT(ts time.Time) string {
	return ts.Format("2006-01-02 15:04:05")
}

// dashboardDecode 将统一响应中的 data 解码为指定结构。
func dashboardDecode[T any](t *testing.T, data map[string]any) T {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal dashboard data: %v", err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal dashboard data: %v", err)
	}
	return out
}

// setupDashboardServer 建立隔离的运营大屏测试环境：复用真实路由 + 真实 MySQL/Redis，
// 并清空身份与业务表后二次 Bootstrap（重建超管与权限 seed）。
func setupDashboardServer(t *testing.T) string {
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
	// 先迁移建表 + Bootstrap 确保 seed 就绪，再清空身份与业务表，随后二次 Bootstrap 重建超管与权限 seed。
	for _, table := range []string{
		"order_items", "orders",
		"flash_sale_order_requests", "flash_sale_orders", "flash_sale_activity_skus", "flash_sale_activities",
		"product_images", "products", "categories",
		"users", "admin_roles", "role_permissions", "roles", "permissions", "admins",
	} {
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

// dashboardAuth 构造带管理员 token 的请求头。
func dashboardAuth(token string) map[string]string {
	return isoAuthHeader(token)
}

// 以下为测试数据写入助手（直接写库，不绕业务接口，保证统计口径独立可对账）。

func dashboardInsertCategory(t *testing.T, name string) int64 {
	t.Helper()
	id, err := g.DB().Model("categories").Ctx(context.Background()).Data(g.Map{
		"name": name, "parent_id": 0, "sort": 0, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert category: %v", err)
	}
	return id
}

func dashboardInsertProduct(t *testing.T, name string, categoryID int64, status int) int64 {
	t.Helper()
	id, err := g.DB().Model("products").Ctx(context.Background()).Data(g.Map{
		"name": name, "brand": "测试", "category_id": categoryID, "price": 100,
		"main_image": "", "detail": "", "status": status,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	return id
}

func dashboardInsertUser(t *testing.T, username, createdAt string) int64 {
	t.Helper()
	id, err := g.DB().Model("users").Ctx(context.Background()).Data(g.Map{
		"username": username, "password_hash": "test-hash-not-used",
		"created_at": createdAt,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func dashboardInsertOrder(t *testing.T, userID int64, status int, amount int64, createdAt string) int64 {
	t.Helper()
	no := guid.S()
	id, err := g.DB().Model("orders").Ctx(context.Background()).Data(g.Map{
		"order_no": no, "user_id": userID, "status": status, "total_amount": amount,
		"idempotency_key": "idem-" + no, "request_hash": "hash-" + no,
		"recipient_name": "测试", "phone": "13800000000",
		"province": "广东", "city": "深圳", "district": "南山", "detail": "测试地址",
		"expire_at": createdAt, "created_at": createdAt,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	return id
}

func dashboardInsertOrderItem(t *testing.T, orderID, productID int64, quantity int) int64 {
	t.Helper()
	id, err := g.DB().Model("order_items").Ctx(context.Background()).Data(g.Map{
		"order_id": orderID, "sku_id": productID, "product_id": productID,
		"sku_name": "SKU", "product_name": "商品", "product_main_image": "",
		"price": 100, "quantity": quantity,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert order item: %v", err)
	}
	return id
}

func dashboardInsertFlashActivity(t *testing.T, status int) int64 {
	t.Helper()
	id, err := g.DB().Model("flash_sale_activities").Ctx(context.Background()).Data(g.Map{
		"name": "秒杀", "status": status,
		"start_time": "2026-01-01 00:00:00", "end_time": "2030-01-01 00:00:00",
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert flash activity: %v", err)
	}
	return id
}

func dashboardInsertFlashRequest(t *testing.T, activityID, userID int64, status int) int64 {
	t.Helper()
	no := guid.S()
	id, err := g.DB().Model("flash_sale_order_requests").Ctx(context.Background()).Data(g.Map{
		"user_id": userID, "activity_id": activityID, "sku_id": 1,
		"idempotency_key": "req-" + no, "request_hash": "rh-" + no, "status": status,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert flash request: %v", err)
	}
	return id
}

func dashboardInsertFlashOrder(t *testing.T, activityID, userID, skuID int64) int64 {
	t.Helper()
	no := guid.S()
	id, err := g.DB().Model("flash_sale_orders").Ctx(context.Background()).Data(g.Map{
		"order_no": no, "user_id": userID, "activity_id": activityID, "sku_id": skuID, "product_id": skuID,
		"sku_name": "SKU", "product_name": "商品", "product_main_image": "",
		"flash_price": 100, "quantity": 1,
		"idempotency_key": "fo-" + no, "request_hash": "fh-" + no,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert flash order: %v", err)
	}
	return id
}

// TestDashboardTimezoneSessionAlignment 覆盖 INV-006：
// 连接层会话时区已与 dashboard.timezone（Asia/Shanghai → +08:00）对齐，消除 Go↔MySQL 漂移。
func TestDashboardTimezoneSessionAlignment(t *testing.T) {
	setupDashboardServer(t)

	record, err := g.DB().Ctx(context.Background()).Raw("SELECT @@session.time_zone AS tz").One()
	if err != nil {
		t.Fatalf("query session time_zone: %v", err)
	}
	if record == nil || record.IsEmpty() || record["tz"].String() != "+08:00" {
		t.Fatalf("session time_zone=%v want +08:00", record)
	}
}

// TestDashboardPermissionBoundary 覆盖 AC-001/INV-001：
// 超管可访问全部接口（200）；无 dashboard:view 普通管理员 403；普通用户 token 403；无 token 401；拒绝路径零写入。
func TestDashboardPermissionBoundary(t *testing.T) {
	base := setupDashboardServer(t)

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	isoInsertUser(t, "userA", "userpass123")
	userToken, _ := isoFrontendLogin(t, base, "userA", "userpass123")
	isoInsertAdmin(t, "plain", "plainpass123")
	plainToken, _ := isoAdminLogin(t, base, "plain", "plainpass123")

	// seed 已写入 dashboard:view 权限。
	if n, _ := g.DB().Model("permissions").Ctx(context.Background()).Where("code", "dashboard:view").Count(); n != 1 {
		t.Fatalf("dashboard:view permission seed count=%d want 1", n)
	}

	before, _ := g.DB().Model("orders").Ctx(context.Background()).Count()

	paths := []string{
		"/admin/dashboard/overview",
		"/admin/dashboard/orders/trend",
		"/admin/dashboard/orders/status",
		"/admin/dashboard/products/top",
		"/admin/dashboard/flash-sales",
	}
	// 超管（IsSuper）全部 200/0。
	for _, p := range paths {
		res := isoDo(t, base, "GET", p, nil, dashboardAuth(adminToken))
		if res.Status != 200 || res.Code != 0 {
			t.Fatalf("super admin %s: status=%d code=%d msg=%q", p, res.Status, res.Code, res.Message)
		}
	}
	// 无 dashboard:view 普通管理员 → 403/1003。
	for _, p := range paths {
		res := isoDo(t, base, "GET", p, nil, dashboardAuth(plainToken))
		if res.Status != 403 || res.Code != 1003 {
			t.Fatalf("plain admin %s: status=%d code=%d want 403/1003", p, res.Status, res.Code)
		}
	}
	// 普通用户 token → 403/1003。
	for _, p := range paths {
		res := isoDo(t, base, "GET", p, nil, dashboardAuth(userToken))
		if res.Status != 403 || res.Code != 1003 {
			t.Fatalf("user token %s: status=%d code=%d want 403/1003", p, res.Status, res.Code)
		}
	}
	// 无 token → 401/1002。
	for _, p := range paths {
		res := isoDo(t, base, "GET", p, nil, nil)
		if res.Status != 401 || res.Code != 1002 {
			t.Fatalf("no token %s: status=%d code=%d want 401/1002", p, res.Status, res.Code)
		}
	}

	// 拒绝路径不产生任何写入（orders 计数不变）。
	if after, _ := g.DB().Model("orders").Ctx(context.Background()).Count(); after != before {
		t.Fatalf("reject path must not write, orders %d -> %d", before, after)
	}
}

// dashboardOverview 用于解码 overview 响应。
type dashboardOverview struct {
	UserTotal        int64 `json:"user_total"`
	TodayNewUsers    int64 `json:"today_new_users"`
	ProductTotal     int64 `json:"product_total"`
	OrderTotal       int64 `json:"order_total"`
	TodayNewOrders   int64 `json:"today_new_orders"`
	TodayOrderAmount int64 `json:"today_order_amount"`
}

// TestDashboardOverviewMetrics 覆盖 AC-002/AC-003/INV-002：
// 各指标与真实表聚合一致；金额只含有效状态 {20,30,40,50} 且命名为 today_order_amount（非实收）。
func TestDashboardOverviewMetrics(t *testing.T) {
	base := setupDashboardServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	today := dashboardDayStart(t, time.Now())
	todayStart := dashboardDT(today)
	yesterday := dashboardDT(today.AddDate(0, 0, -1).Add(2 * time.Hour))

	// 用户：1 个今日新增、1 个昨日（user_total=2, today_new_users=1）。
	dashboardInsertUser(t, "today-user", todayStart)
	dashboardInsertUser(t, "yesterday-user", yesterday)

	// 商品：3 个 SPU（draft/on_shelf/off_shelf 均计入）。
	catID := dashboardInsertCategory(t, "分类")
	dashboardInsertProduct(t, "P1", catID, 0)
	dashboardInsertProduct(t, "P2", catID, 1)
	dashboardInsertProduct(t, "P3", catID, 2)

	uid := dashboardInsertUser(t, "buyer", yesterday)
	// 今日订单：状态 20/30/10/60/70（金额有效仅 20+30=3000）。
	dashboardInsertOrder(t, uid, dStatusPaid, 1000, todayStart)
	dashboardInsertOrder(t, uid, dStatusShipped, 2000, todayStart)
	dashboardInsertOrder(t, uid, dStatusPending, 500, todayStart)
	dashboardInsertOrder(t, uid, dStatusCancelled, 300, todayStart)
	dashboardInsertOrder(t, uid, dStatusRefunded, 400, todayStart)
	// 昨日订单：1 个（不计入今日）。
	dashboardInsertOrder(t, uid, dStatusPaid, 9999, yesterday)

	res := isoDo(t, base, "GET", "/admin/dashboard/overview", nil, dashboardAuth(adminToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("overview: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	ov := dashboardDecode[dashboardOverview](t, res.Data)

	if ov.UserTotal != 3 {
		t.Fatalf("user_total=%d want 3", ov.UserTotal)
	}
	if ov.TodayNewUsers != 1 {
		t.Fatalf("today_new_users=%d want 1", ov.TodayNewUsers)
	}
	if ov.ProductTotal != 3 {
		t.Fatalf("product_total=%d want 3", ov.ProductTotal)
	}
	if ov.OrderTotal != 6 {
		t.Fatalf("order_total=%d want 6", ov.OrderTotal)
	}
	if ov.TodayNewOrders != 5 {
		t.Fatalf("today_new_orders=%d want 5", ov.TodayNewOrders)
	}
	if ov.TodayOrderAmount != 3000 {
		t.Fatalf("today_order_amount=%d want 3000（只含 20/30 有效状态）", ov.TodayOrderAmount)
	}
	// 命名约束：不含「实收销售额」，字段名为 today_order_amount。
	if _, has := res.Data["today_order_amount"]; !has {
		t.Fatalf("missing today_order_amount field")
	}
	if _, has := res.Data["received_sales"]; has {
		t.Fatalf("must not expose received_sales/实收 语义字段")
	}
}

// dashboardTrendItem / dashboardTrendRes 用于解码趋势响应。
type dashboardTrendItem struct {
	Date       string `json:"date"`
	OrderCount int64  `json:"order_count"`
}
type dashboardTrendRes struct {
	Days []*dashboardTrendItem `json:"days"`
}

// TestDashboardOrdersTrend 覆盖 AC-004/INV-005：
// 7 天序列连续、空日期 0、边界（6 天前含、7 天前不含）不重不漏、逐日求和 = 总数。
func TestDashboardOrdersTrend(t *testing.T) {
	base := setupDashboardServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	loc := dashboardBeijing(t)
	today := dashboardDayStart(t, time.Now())
	uid := dashboardInsertUser(t, "buyer", dashboardDT(today.Add(-24*time.Hour)))

	dayOffsets := []int{0, 1, 1, 2, 4, 6, 6, 7} // 日偏移（0=今日, 6=6天前, 7=7天前应排除）
	for _, off := range dayOffsets {
		created := today.AddDate(0, 0, -off).Add(time.Hour)
		dashboardInsertOrder(t, uid, dStatusPaid, 100, dashboardDT(created))
	}
	// 期望：今日 1、昨日 2、前2天 1、前3天 0、前4天 1、前5天 0、前6天 2；前7天排除。
	expected := []int64{2, 0, 1, 0, 1, 2, 1}

	res := isoDo(t, base, "GET", "/admin/dashboard/orders/trend", nil, dashboardAuth(adminToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("trend: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	tr := dashboardDecode[dashboardTrendRes](t, res.Data)
	if len(tr.Days) != 7 {
		t.Fatalf("days=%d want 7", len(tr.Days))
	}
	var sum int64
	for i, d := range tr.Days {
		if d.OrderCount != expected[i] {
			t.Fatalf("day[%d]=%s count=%d want %d", i, d.Date, d.OrderCount, expected[i])
		}
		sum += d.OrderCount
	}
	// 逐日求和 = 窗口内总数（8 条 - 1 条 7 天前排除 = 7）。
	if sum != 7 {
		t.Fatalf("sum(days)=%d want 7", sum)
	}
	// 日期连续（6 天前 ~ 今日）。
	first, _ := time.ParseInLocation("2006-01-02", tr.Days[0].Date, loc)
	last, _ := time.ParseInLocation("2006-01-02", tr.Days[6].Date, loc)
	if first.Format("2006-01-02") != today.AddDate(0, 0, -6).Format("2006-01-02") ||
		last.Format("2006-01-02") != today.Format("2006-01-02") {
		t.Fatalf("trend date window wrong: %s ~ %s", tr.Days[0].Date, tr.Days[6].Date)
	}
}

// dashboardStatusItem / dashboardStatusRes 用于解码状态分布响应。
type dashboardStatusItem struct {
	Status int   `json:"status"`
	Count  int64 `json:"count"`
}
type dashboardStatusRes struct {
	Total              int64                  `json:"total"`
	StatusDistribution []*dashboardStatusItem `json:"status_distribution"`
}

// TestDashboardOrdersStatusAndRange 覆盖 AC-005/AC-006：
// 状态分布 7 状态显式返回；时间范围左闭右开 [start,end) 不重不漏。
func TestDashboardOrdersStatusAndRange(t *testing.T) {
	base := setupDashboardServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	uid := dashboardInsertUser(t, "buyer", "2026-01-01 00:00:00")
	// 固定历史窗口 [2026-01-05, 2026-01-06) 内的订单：状态 20 与 60 各 1；边界 06 日 00:00 应排除。
	dashboardInsertOrder(t, uid, dStatusPaid, 100, "2026-01-05 10:00:00")
	dashboardInsertOrder(t, uid, dStatusCancelled, 100, "2026-01-05 23:59:59")
	dashboardInsertOrder(t, uid, dStatusShipped, 100, "2026-01-06 00:00:00") // 边界排除
	dashboardInsertOrder(t, uid, dStatusCompleted, 100, "2026-01-04 23:59:59")

	start := "2026-01-05T00:00:00+08:00"
	end := "2026-01-06T00:00:00+08:00"
	q := url.Values{}
	q.Set("start_time", start)
	q.Set("end_time", end)

	res := isoDo(t, base, "GET", "/admin/dashboard/orders/status?"+q.Encode(), nil, dashboardAuth(adminToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("status range: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	sr := dashboardDecode[dashboardStatusRes](t, res.Data)
	if sr.Total != 2 {
		t.Fatalf("range total=%d want 2（边界 06 日 00:00 与 04 日应排除）", sr.Total)
	}
	if len(sr.StatusDistribution) != 7 {
		t.Fatalf("status_distribution=%d want 7", len(sr.StatusDistribution))
	}
	byStatus := map[int]int64{}
	for _, it := range sr.StatusDistribution {
		byStatus[it.Status] = it.Count
	}
	if byStatus[dStatusPaid] != 1 || byStatus[dStatusCancelled] != 1 {
		t.Fatalf("range distribution wrong: %+v", byStatus)
	}
	if byStatus[dStatusShipped] != 0 || byStatus[dStatusCompleted] != 0 {
		t.Fatalf("boundary orders must be excluded: %+v", byStatus)
	}

	// 无时间范围：total = 全部 4 单，7 状态显式返回。
	all := isoDo(t, base, "GET", "/admin/dashboard/orders/status", nil, dashboardAuth(adminToken))
	ar := dashboardDecode[dashboardStatusRes](t, all.Data)
	if ar.Total != 4 {
		t.Fatalf("no-range total=%d want 4", ar.Total)
	}
	if len(ar.StatusDistribution) != 7 {
		t.Fatalf("no-range status_distribution=%d want 7", len(ar.StatusDistribution))
	}
}

// dashboardTopItem / dashboardTopRes 用于解码销量排行响应。
type dashboardTopItem struct {
	Rank        int    `json:"rank"`
	ProductId   int64  `json:"product_id"`
	ProductName string `json:"product_name"`
	Sales       int64  `json:"sales"`
}
type dashboardTopRes struct {
	Items []*dashboardTopItem `json:"items"`
}

// TestDashboardProductsTop 覆盖 AC-007/AC-008/INV-003：
// 销量只来自有效状态订单明细，排除待支付/已取消/已退款；排序/排名正确；limit 生效。
func TestDashboardProductsTop(t *testing.T) {
	base := setupDashboardServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	catID := dashboardInsertCategory(t, "分类")
	p1 := dashboardInsertProduct(t, "P1", catID, 1)
	p2 := dashboardInsertProduct(t, "P2", catID, 1)
	p3 := dashboardInsertProduct(t, "P3", catID, 1)
	uid := dashboardInsertUser(t, "buyer", "2026-01-01 00:00:00")

	// 有效订单 A(20)/B(30)；无效订单 C(60 取消)/D(10 待支付)。
	oA := dashboardInsertOrder(t, uid, dStatusPaid, 100, "2026-01-01 10:00:00")
	oB := dashboardInsertOrder(t, uid, dStatusShipped, 100, "2026-01-01 10:00:00")
	oC := dashboardInsertOrder(t, uid, dStatusCancelled, 100, "2026-01-01 10:00:00")
	oD := dashboardInsertOrder(t, uid, dStatusPending, 100, "2026-01-01 10:00:00")

	dashboardInsertOrderItem(t, oA, p1, 5)
	dashboardInsertOrderItem(t, oA, p2, 3)
	dashboardInsertOrderItem(t, oB, p1, 2)
	dashboardInsertOrderItem(t, oB, p3, 10)
	dashboardInsertOrderItem(t, oC, p1, 100) // 取消，不计
	dashboardInsertOrderItem(t, oD, p2, 50)  // 待支付，不计

	res := isoDo(t, base, "GET", "/admin/dashboard/products/top", nil, dashboardAuth(adminToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("top: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	tr := dashboardDecode[dashboardTopRes](t, res.Data)
	if len(tr.Items) != 3 {
		t.Fatalf("top items=%d want 3", len(tr.Items))
	}
	// 期望：P3(10) > P1(5+2=7) > P2(3)。
	want := []struct {
		id    int64
		name  string
		sales int64
		rank  int
	}{
		{p3, "P3", 10, 1},
		{p1, "P1", 7, 2},
		{p2, "P2", 3, 3},
	}
	for i, w := range want {
		it := tr.Items[i]
		if it.ProductId != w.id || it.ProductName != w.name || it.Sales != w.sales || it.Rank != w.rank {
			t.Fatalf("top[%d]=%+v want id=%d name=%s sales=%d rank=%d", i, it, w.id, w.name, w.sales, w.rank)
		}
	}

	// limit=2 仅返回 2 条。
	top2 := isoDo(t, base, "GET", "/admin/dashboard/products/top?limit=2", nil, dashboardAuth(adminToken))
	tr2 := dashboardDecode[dashboardTopRes](t, top2.Data)
	if len(tr2.Items) != 2 {
		t.Fatalf("top limit=2 items=%d want 2", len(tr2.Items))
	}
}

// dashboardFlashRes 用于解码秒杀统计响应。
type dashboardFlashRes struct {
	ActivityTotal  int64 `json:"activity_total"`
	ActivityStatus struct {
		Enabled  int64 `json:"enabled"`
		Disabled int64 `json:"disabled"`
	} `json:"activity_status"`
	RequestStatus struct {
		Queued  int64 `json:"queued"`
		Success int64 `json:"success"`
		Failed  int64 `json:"failed"`
		Dead    int64 `json:"dead"`
	} `json:"request_status"`
	CurrentQueued int64 `json:"current_queued"`
	SuccessOrders int64 `json:"success_orders"`
}

// TestDashboardFlashSales 覆盖 AC-009/INV-004：
// 活动/请求状态分布、当前 queued 与成功订单各计数一致；queued 不计入成功订单。
func TestDashboardFlashSales(t *testing.T) {
	base := setupDashboardServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	uid := dashboardInsertUser(t, "buyer", "2026-01-01 00:00:00")
	enabled := dashboardInsertFlashActivity(t, 1)
	_ = dashboardInsertFlashActivity(t, 0) // disabled 活动（仅计数）

	// 请求状态：queued x2、success x1、failed x3、dead x1。
	dashboardInsertFlashRequest(t, enabled, uid, 0)
	dashboardInsertFlashRequest(t, enabled, uid, 0)
	dashboardInsertFlashRequest(t, enabled, uid, 1)
	dashboardInsertFlashRequest(t, enabled, uid, 2)
	dashboardInsertFlashRequest(t, enabled, uid, 2)
	dashboardInsertFlashRequest(t, enabled, uid, 2)
	dashboardInsertFlashRequest(t, enabled, uid, 3)

	// 成功订单：4 行（唯一事实来源）。因 uk_flash_one_per_user 唯一约束，各使用不同 sku_id。
	for i := 0; i < 4; i++ {
		dashboardInsertFlashOrder(t, enabled, uid, int64(i+1))
	}

	res := isoDo(t, base, "GET", "/admin/dashboard/flash-sales", nil, dashboardAuth(adminToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("flash-sales: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	fs := dashboardDecode[dashboardFlashRes](t, res.Data)

	if fs.ActivityTotal != 2 {
		t.Fatalf("activity_total=%d want 2", fs.ActivityTotal)
	}
	if fs.ActivityStatus.Enabled != 1 || fs.ActivityStatus.Disabled != 1 {
		t.Fatalf("activity_status=%+v want enabled=1 disabled=1", fs.ActivityStatus)
	}
	if fs.RequestStatus.Queued != 2 || fs.RequestStatus.Success != 1 || fs.RequestStatus.Failed != 3 || fs.RequestStatus.Dead != 1 {
		t.Fatalf("request_status=%+v want queued=2 success=1 failed=3 dead=1", fs.RequestStatus)
	}
	if fs.CurrentQueued != 2 {
		t.Fatalf("current_queued=%d want 2", fs.CurrentQueued)
	}
	if fs.SuccessOrders != 4 {
		t.Fatalf("success_orders=%d want 4（queued 不计入）", fs.SuccessOrders)
	}
}

// TestDashboardInvalidParams 覆盖 AC-010：
// 非法时间范围（单边缺省/start>=end/格式错）与超上限/负数 limit 返回 400/1001。
func TestDashboardInvalidParams(t *testing.T) {
	base := setupDashboardServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	auth := dashboardAuth(adminToken)

	// 单边缺省。
	res := isoDo(t, base, "GET", "/admin/dashboard/orders/status?start_time=2026-01-05T00:00:00Z", nil, auth)
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("only start_time: status=%d code=%d want 400/1001", res.Status, res.Code)
	}
	res = isoDo(t, base, "GET", "/admin/dashboard/orders/status?end_time=2026-01-05T00:00:00Z", nil, auth)
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("only end_time: status=%d code=%d want 400/1001", res.Status, res.Code)
	}
	// start >= end。
	res = isoDo(t, base, "GET", "/admin/dashboard/orders/status?start_time=2026-01-05T00:00:00Z&end_time=2026-01-05T00:00:00Z", nil, auth)
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("start==end: status=%d code=%d want 400/1001", res.Status, res.Code)
	}
	// 格式错。
	res = isoDo(t, base, "GET", "/admin/dashboard/orders/status?start_time=abc&end_time=def", nil, auth)
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("bad format: status=%d code=%d want 400/1001", res.Status, res.Code)
	}
	// limit 超上限与负数。
	res = isoDo(t, base, "GET", "/admin/dashboard/products/top?limit=51", nil, auth)
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("limit=51: status=%d code=%d want 400/1001", res.Status, res.Code)
	}
	res = isoDo(t, base, "GET", "/admin/dashboard/products/top?limit=-1", nil, auth)
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("limit=-1: status=%d code=%d want 400/1001", res.Status, res.Code)
	}
	// 非整数 limit（CLEAN-001 回归）：不得被 gconv 静默转为默认值/截断，必须 400/1001。
	res = isoDo(t, base, "GET", "/admin/dashboard/products/top?limit=abc", nil, auth)
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("limit=abc: status=%d code=%d want 400/1001", res.Status, res.Code)
	}
	res = isoDo(t, base, "GET", "/admin/dashboard/products/top?limit=12.5", nil, auth)
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("limit=12.5: status=%d code=%d want 400/1001", res.Status, res.Code)
	}
}
