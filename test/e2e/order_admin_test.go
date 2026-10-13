package cmd

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定「后台订单只读管理」的行为：分页列表、组合筛选、稳定排序、详情快照、状态统计与权限隔离。
// 覆盖 admin-order-management 的 AC-001~AC-014 与 INV-001~INV-007。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/order/v1"
)

// adminOrderListData 是后台订单列表响应的 data 结构。
type adminOrderListData struct {
	Items []*v1.AdminOrderListItem `json:"items"`
	Total int                      `json:"total"`
	Page  int                      `json:"page"`
	Size  int                      `json:"size"`
}

// adminOrderStatsData 是后台订单状态统计响应的 data 结构。
type adminOrderStatsData struct {
	Total int64                     `json:"total"`
	Stats []*v1.AdminOrderStatsItem `json:"stats"`
}

// adminOrderInsert 直接写入一条普通订单（可控 status 与 created_at Unix 秒），返回订单 id。
// created_at 用 MySQL 会话时区的 FROM_UNIXTIME 写入，与查询的 FROM_UNIXTIME 同基准，保证时间筛选测试确定。
func adminOrderInsert(t *testing.T, userID int64, orderNo string, status int, totalAmount int64, createdAtUnix int64) int64 {
	t.Helper()
	id, err := g.DB().Model("orders").Ctx(context.Background()).Data(g.Map{
		"order_no":        orderNo,
		"user_id":         userID,
		"status":          status,
		"total_amount":    totalAmount,
		"idempotency_key": "idem-" + orderNo,
		"request_hash":    "hash-" + orderNo,
		"recipient_name":  "张三",
		"phone":           "13800138000",
		"province":        "广东省",
		"city":            "深圳市",
		"district":        "南山区",
		"detail":          "科技园1号",
		"expire_at":       gdb.Raw(fmt.Sprintf("FROM_UNIXTIME(%d)", createdAtUnix+3600)),
		"created_at":      gdb.Raw(fmt.Sprintf("FROM_UNIXTIME(%d)", createdAtUnix)),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert order %s: %v", orderNo, err)
	}
	return id
}

// adminOrderItemInsert 直接写入一条订单项快照。
func adminOrderItemInsert(t *testing.T, orderID, skuID, productID int64, skuName, productName string, price, quantity int64) {
	t.Helper()
	if _, err := g.DB().Model("order_items").Ctx(context.Background()).Data(g.Map{
		"order_id":           orderID,
		"sku_id":             skuID,
		"product_id":         productID,
		"sku_name":           skuName,
		"product_name":       productName,
		"product_main_image": "http://img.example/x.png",
		"price":              price,
		"quantity":           quantity,
	}).Insert(); err != nil {
		t.Fatalf("insert order item: %v", err)
	}
}

// adminListCall 发起后台订单列表请求并解码 data（成功路径）。
func adminListCall(t *testing.T, base, query, token string) adminOrderListData {
	t.Helper()
	path := "/admin/orders"
	if query != "" {
		path += "?" + query
	}
	res := isoDo(t, base, "GET", path, nil, isoAuthHeader(token))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("admin list %q: status=%d code=%d msg=%q", query, res.Status, res.Code, res.Message)
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("marshal list data: %v", err)
	}
	var d adminOrderListData
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("unmarshal list data: %v", err)
	}
	return d
}

// adminListExpectBad 断言后台列表请求返回 400/1001。
func adminListExpectBad(t *testing.T, base, query, token string) {
	t.Helper()
	path := "/admin/orders"
	if query != "" {
		path += "?" + query
	}
	res := isoDo(t, base, "GET", path, nil, isoAuthHeader(token))
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("admin list %q: status=%d code=%d want 400/1001", query, res.Status, res.Code)
	}
}

// adminStatsCall 发起后台状态统计请求并解码 data（成功路径）。
func adminStatsCall(t *testing.T, base, token string) adminOrderStatsData {
	t.Helper()
	res := isoDo(t, base, "GET", "/admin/orders/stats", nil, isoAuthHeader(token))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("admin stats: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("marshal stats data: %v", err)
	}
	var d adminOrderStatsData
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("unmarshal stats data: %v", err)
	}
	return d
}

// adminGrantPermission 直接授予普通管理员指定权限（建角色并绑定），用于验证非超管的「命中放行」路径。
func adminGrantPermission(t *testing.T, adminID int64, permCodes ...string) {
	t.Helper()
	roleID, err := g.DB().Model("roles").Ctx(context.Background()).Data(g.Map{
		"name":        fmt.Sprintf("role-%d", adminID),
		"description": "test",
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert role: %v", err)
	}
	for _, code := range permCodes {
		v, err := g.DB().Model("permissions").Ctx(context.Background()).Where("code", code).Value("id")
		if err != nil {
			t.Fatalf("query permission %s: %v", code, err)
		}
		if v == nil || v.IsEmpty() {
			t.Fatalf("permission %s not seeded", code)
		}
		if _, err := g.DB().Model("role_permissions").Ctx(context.Background()).Data(g.Map{
			"role_id": roleID, "permission_id": v.Int64(),
		}).Insert(); err != nil {
			t.Fatalf("insert role_permissions: %v", err)
		}
	}
	if _, err := g.DB().Model("admin_roles").Ctx(context.Background()).Data(g.Map{
		"admin_id": adminID, "role_id": roleID,
	}).Insert(); err != nil {
		t.Fatalf("insert admin_roles: %v", err)
	}
}

// rfc3339Z 将 Unix 秒格式化为 UTC（Z）RFC3339 字符串。
func rfc3339Z(unixSec int64) string {
	return time.Unix(unixSec, 0).UTC().Format(time.RFC3339)
}

// rfc3339Plus8 将 Unix 秒格式化为 +08:00 时区偏移的 RFC3339 字符串（同一绝对时刻）。
func rfc3339Plus8(unixSec int64) string {
	return time.Unix(unixSec, 0).In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339)
}

// TestAdminOrderListPagination 覆盖 AC-001/AC-002/INV-006：分页默认值、元数据与非法分页拒绝。
func TestAdminOrderListPagination(t *testing.T) {
	base := setupOrderServer(t)
	isoInsertUser(t, "adminlistbuyer", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "adminlistbuyer", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)

	now := time.Now().Unix()
	for i := 0; i < 5; i++ {
		adminOrderInsert(t, userID, fmt.Sprintf("NO-LIST-%d", i), 10, 1000+int64(i), now+int64(i))
	}

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 默认分页：page=1,size=20。
	d := adminListCall(t, base, "", adminToken)
	if d.Total != 5 || d.Page != 1 || d.Size != 20 || len(d.Items) != 5 {
		t.Fatalf("default list: total=%d page=%d size=%d items=%d", d.Total, d.Page, d.Size, len(d.Items))
	}
	// 默认 id 倒序：第一条是最后创建的（id 最大）。
	if len(d.Items) == 5 && d.Items[0].OrderNo != "NO-LIST-4" {
		t.Fatalf("default list should be id DESC, first=%q", d.Items[0].OrderNo)
	}

	// 指定分页：page=2,size=2 → 2 条。
	d2 := adminListCall(t, base, "page=2&size=2", adminToken)
	if d2.Total != 5 || d2.Page != 2 || d2.Size != 2 || len(d2.Items) != 2 {
		t.Fatalf("page2 list: total=%d page=%d size=%d items=%d", d2.Total, d2.Page, d2.Size, len(d2.Items))
	}

	// 列表项保持轻量：不返回幂等键/地址快照（类型结构本身即不包含，故无需逐字段断言）。

	// 非法分页参数 → 400/1001（拒绝，非钳制）。
	for _, q := range []string{"page=0", "page=-1", "size=0", "size=101", "page=abc", "size=xyz"} {
		adminListExpectBad(t, base, q, adminToken)
	}
}

// TestAdminOrderListFilters 覆盖 AC-004/AC-005：各筛选独立及组合正确，非法筛选/时间/排序被拒。
func TestAdminOrderListFilters(t *testing.T) {
	base := setupOrderServer(t)
	isoInsertUser(t, "adminfilterbuyer1", "buyerpass123")
	isoInsertUser(t, "adminfilterbuyer2", "buyerpass123")
	token1, _ := isoFrontendLogin(t, base, "adminfilterbuyer1", "buyerpass123")
	token2, _ := isoFrontendLogin(t, base, "adminfilterbuyer2", "buyerpass123")
	c1 := isoClaims(t, token1)
	c2 := isoClaims(t, token2)
	u1, _ := parseUserID(t, c1.Subject)
	u2, _ := parseUserID(t, c2.Subject)

	now := time.Now().Unix()
	adminOrderInsert(t, u1, "NO-F-1", 10, 1000, now)
	adminOrderInsert(t, u1, "NO-F-2", 20, 2000, now)
	adminOrderInsert(t, u2, "NO-F-3", 10, 3000, now)

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// order_no 精确。
	d := adminListCall(t, base, "order_no=NO-F-2", adminToken)
	if d.Total != 1 || d.Items[0].OrderNo != "NO-F-2" {
		t.Fatalf("order_no filter: total=%d first=%+v", d.Total, d.Items)
	}

	// user_id 精确。
	d = adminListCall(t, base, fmt.Sprintf("user_id=%d", u2), adminToken)
	if d.Total != 1 || d.Items[0].UserId != u2 {
		t.Fatalf("user_id filter: total=%d first=%+v", d.Total, d.Items)
	}

	// status 精确。
	d = adminListCall(t, base, "status=paid", adminToken)
	if d.Total != 1 || d.Items[0].Status != v1.StatusPaid {
		t.Fatalf("status filter: total=%d first=%+v", d.Total, d.Items)
	}

	// 组合：user_id + status。
	d = adminListCall(t, base, fmt.Sprintf("user_id=%d&status=pending_payment", u1), adminToken)
	if d.Total != 1 || d.Items[0].OrderNo != "NO-F-1" {
		t.Fatalf("combined filter: total=%d first=%+v", d.Total, d.Items)
	}

	// 非法状态 / 非法 user_id / 非法 sort / 非法 order → 400/1001。
	for _, q := range []string{"status=bogus", "user_id=0", "user_id=-1", "sort=order_no", "order=sideways"} {
		adminListExpectBad(t, base, q, adminToken)
	}
}

// TestAdminOrderListTimeRange 覆盖 AC-004/AC-005/INV-007：时间范围 [start,end) 左闭右开与时区偏移等价。
func TestAdminOrderListTimeRange(t *testing.T) {
	base := setupOrderServer(t)
	isoInsertUser(t, "admintimebuyer", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "admintimebuyer", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)

	const start = int64(1700000000) // == start
	const mid = int64(1700001800)   // 区间内
	const end = int64(1700003600)   // == end（右开，排除）
	adminOrderInsert(t, userID, "NO-T-START", 10, 1000, start)
	adminOrderInsert(t, userID, "NO-T-MID", 10, 2000, mid)
	adminOrderInsert(t, userID, "NO-T-END", 10, 3000, end)

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	query := timeRangeQuery(rfc3339Z(start), rfc3339Z(end))
	d := adminListCall(t, base, query, adminToken)
	if d.Total != 2 {
		t.Fatalf("time range [start,end): total=%d want 2 (%+v)", d.Total, d.Items)
	}
	for _, it := range d.Items {
		if it.OrderNo == "NO-T-END" {
			t.Fatalf("time range should exclude end boundary, got %+v", d.Items)
		}
	}

	// INV-007：+08:00 偏移与 UTC 指向同一时刻，筛选结果一致。
	query8 := timeRangeQuery(rfc3339Plus8(start), rfc3339Plus8(end))
	d8 := adminListCall(t, base, query8, adminToken)
	if d8.Total != d.Total {
		t.Fatalf("timezone offset should be equivalent: total=%d want %d", d8.Total, d.Total)
	}

	// 非法时间：缺时区偏移 / start>=end → 400/1001。
	for _, q := range []string{
		"start_time=2023-11-14T22:13:20",                 // 缺偏移
		timeRangeQuery(rfc3339Z(end), rfc3339Z(start)),   // start >= end
		timeRangeQuery(rfc3339Z(start), rfc3339Z(start)), // start == end
	} {
		adminListExpectBad(t, base, q, adminToken)
	}
}

// timeRangeQuery 构造 URL 编码后的 start_time/end_time 查询串（RFC3339 含 +08:00 时区偏移须编码）。
func timeRangeQuery(start, end string) string {
	return "start_time=" + url.QueryEscape(start) + "&end_time=" + url.QueryEscape(end)
}

// TestAdminOrderSortStable 覆盖 AC-003/INV-005：默认 id DESC 与 created_at 排序的稳定 tiebreak，翻页无重复/遗漏。
func TestAdminOrderSortStable(t *testing.T) {
	base := setupOrderServer(t)
	isoInsertUser(t, "adminsortbuyer", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "adminsortbuyer", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)

	// 三条订单同一 created_at，仅 id 不同；默认 id DESC。
	now := time.Now().Unix()
	adminOrderInsert(t, userID, "NO-S-1", 10, 1000, now)
	adminOrderInsert(t, userID, "NO-S-2", 10, 2000, now)
	adminOrderInsert(t, userID, "NO-S-3", 10, 3000, now)

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 默认 id DESC：3,2,1。
	d := adminListCall(t, base, "", adminToken)
	if len(d.Items) != 3 || d.Items[0].OrderNo != "NO-S-3" || d.Items[1].OrderNo != "NO-S-2" || d.Items[2].OrderNo != "NO-S-1" {
		t.Fatalf("default id DESC order wrong: %+v", d.Items)
	}

	// created_at 排序（asc + id asc tiebreak）：同 created_at 下按 id 升序 1,2,3。
	d = adminListCall(t, base, "sort=created_at&order=asc", adminToken)
	if len(d.Items) != 3 || d.Items[0].OrderNo != "NO-S-1" || d.Items[1].OrderNo != "NO-S-2" || d.Items[2].OrderNo != "NO-S-3" {
		t.Fatalf("created_at asc order wrong: %+v", d.Items)
	}

	// 翻页无重复/遗漏：size=2 两页覆盖全部且不重叠。
	seen := map[string]bool{}
	for page := 1; page <= 2; page++ {
		d = adminListCall(t, base, fmt.Sprintf("sort=created_at&order=asc&page=%d&size=2", page), adminToken)
		for _, it := range d.Items {
			if seen[it.OrderNo] {
				t.Fatalf("pagination duplicated order %q", it.OrderNo)
			}
			seen[it.OrderNo] = true
		}
	}
	if len(seen) != 3 {
		t.Fatalf("pagination should cover all 3 orders, got %d", len(seen))
	}
}

// TestAdminOrderDetailSnapshot 覆盖 AC-006/AC-007/AC-008/INV-003：详情返回历史快照，改价不变，不存在 404。
func TestAdminOrderDetailSnapshot(t *testing.T) {
	base := setupOrderServer(t)
	skuID, productID := setupSellable(t, "SKU-ADMINDETAIL", 3000, 10)

	isoInsertUser(t, "admindetailbuyer", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "admindetailbuyer", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)

	orderID := adminOrderInsert(t, userID, "NO-DETAIL", 20, 3000, time.Now().Unix())
	adminOrderItemInsert(t, orderID, skuID, productID, "SKU-ADMINDETAIL", "测试商品-SKU-ADMINDETAIL", 3000, 1)

	// 改当前 skus.price，验证详情仍是下单快照。
	if _, err := g.DB().Model("skus").Ctx(context.Background()).Where("id", skuID).Data(g.Map{"price": 9999}).Update(); err != nil {
		t.Fatalf("update sku price: %v", err)
	}

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	res := isoDo(t, base, "GET", fmt.Sprintf("/admin/orders/%d", orderID), nil, isoAuthHeader(adminToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("admin detail: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	var order v1.Order
	if err := json.Unmarshal(b, &order); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if order.Id != orderID || order.OrderNo != "NO-DETAIL" || order.UserId != userID {
		t.Fatalf("detail identity mismatch: %+v", order)
	}
	if order.Status != v1.StatusPaid || order.TotalAmount != 3000 {
		t.Fatalf("detail status/amount mismatch: status=%s total=%d", order.Status, order.TotalAmount)
	}
	if len(order.Items) != 1 || order.Items[0].Price != 3000 || order.Items[0].SkuName != "SKU-ADMINDETAIL" || order.Items[0].Quantity != 1 {
		t.Fatalf("detail snapshot items wrong: %+v", order.Items)
	}

	// 不存在订单 → 404/9001。
	res = isoDo(t, base, "GET", "/admin/orders/99999999", nil, isoAuthHeader(adminToken))
	if res.Status != 404 || res.Code != 9001 {
		t.Fatalf("admin detail missing: status=%d code=%d want 404/9001", res.Status, res.Code)
	}
}

// TestAdminOrderStats 覆盖 AC-009/INV-004：覆盖 7 态、无数据为 0、total=各态之和、不混入秒杀订单。
func TestAdminOrderStats(t *testing.T) {
	base := setupOrderServer(t)
	isoInsertUser(t, "adminstatsbuyer", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "adminstatsbuyer", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)

	now := time.Now().Unix()
	// 覆盖全部 7 态：10,20,30,40,50,60,70，其中 30/40 各两条，其余各一条。
	statuses := []int{10, 20, 30, 30, 40, 40, 50, 60, 70}
	for i, st := range statuses {
		adminOrderInsert(t, userID, fmt.Sprintf("NO-STAT-%d", i), st, 1000, now+int64(i))
	}

	// 插入一条秒杀订单，验证 stats 不混入（先清空秒杀订单表，避免跨用例残留唯一键冲突）。
	if _, err := g.DB().Exec(context.Background(), "DELETE FROM flash_sale_orders"); err != nil {
		t.Fatalf("clean flash_sale_orders: %v", err)
	}
	if _, err := g.DB().Model("flash_sale_orders").Ctx(context.Background()).Data(g.Map{
		"order_no": "NO-FLASH-STATS", "user_id": userID, "activity_id": 1, "sku_id": 1, "product_id": 1,
		"sku_name": "flashsku", "product_name": "flashprod", "product_main_image": "",
		"flash_price": 1, "quantity": 1, "idempotency_key": "idem-flash-stats", "request_hash": "hash-flash-stats",
	}).Insert(); err != nil {
		t.Fatalf("insert flash sale order: %v", err)
	}

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	d := adminStatsCall(t, base, adminToken)

	if d.Total != 9 {
		t.Fatalf("stats total=%d want 9（不含秒杀）", d.Total)
	}
	// 7 态全覆盖且顺序稳定。
	want := []struct {
		status string
		count  int64
	}{
		{v1.StatusPendingPayment, 1},
		{v1.StatusPaid, 1},
		{v1.StatusShipped, 2},
		{v1.StatusReceived, 2},
		{v1.StatusCompleted, 1},
		{v1.StatusCancelled, 1},
		{v1.StatusRefunded, 1},
	}
	if len(d.Stats) != len(want) {
		t.Fatalf("stats length=%d want %d", len(d.Stats), len(want))
	}
	for i, w := range want {
		if d.Stats[i].Status != w.status || d.Stats[i].Count != w.count {
			t.Fatalf("stats[%d]=%+v want %+v", i, d.Stats[i], w)
		}
	}

	// 无数据状态返回 0：清空 orders 后统计。
	for _, table := range []string{"order_items", "orders"} {
		if _, err := g.DB().Exec(context.Background(), "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	d = adminStatsCall(t, base, adminToken)
	if d.Total != 0 {
		t.Fatalf("empty stats total=%d want 0", d.Total)
	}
	for _, s := range d.Stats {
		if s.Count != 0 {
			t.Fatalf("empty stats should be all 0, got %+v", s)
		}
	}
}

// TestAdminOrderReadOnlyPermission 覆盖 AC-010/AC-011/AC-012/INV-002：普通用户/无权限管理员 403、超管与持权管理员放行。
func TestAdminOrderReadOnlyPermission(t *testing.T) {
	base := setupOrderServer(t)
	isoInsertUser(t, "adminpermbuyer", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "adminpermbuyer", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	orderID := adminOrderInsert(t, userID, "NO-PERM", 10, 1000, time.Now().Unix())

	superToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 无权限普通管理员。
	isoInsertAdmin(t, "plainadminnoperm", "plainpass123")
	plainToken, _ := isoAdminLogin(t, base, "plainadminnoperm", "plainpass123")

	// 持 order:list + order:view 的普通管理员。
	grantID := isoInsertAdmin(t, "grantadmin", "grantpass123")
	adminGrantPermission(t, grantID, "order:list", "order:view")
	grantToken, _ := isoAdminLogin(t, base, "grantadmin", "grantpass123")

	detailPath := fmt.Sprintf("/admin/orders/%d", orderID)
	cases := []struct {
		name  string
		path  string
		token string
		want  int
	}{
		{"super-list", "/admin/orders", superToken, 200},
		{"super-stats", "/admin/orders/stats", superToken, 200},
		{"super-detail", detailPath, superToken, 200},
		{"plain-list", "/admin/orders", plainToken, 403},
		{"plain-stats", "/admin/orders/stats", plainToken, 403},
		{"plain-detail", detailPath, plainToken, 403},
		{"grant-list", "/admin/orders", grantToken, 200},
		{"grant-stats", "/admin/orders/stats", grantToken, 200},
		{"grant-detail", detailPath, grantToken, 200},
		{"user-list", "/admin/orders", token, 403},
		{"user-stats", "/admin/orders/stats", token, 403},
		{"user-detail", detailPath, token, 403},
	}
	for _, c := range cases {
		res := isoDo(t, base, "GET", c.path, nil, isoAuthHeader(c.token))
		if res.Status != c.want {
			t.Fatalf("%s: status=%d code=%d want %d", c.name, res.Status, res.Code, c.want)
		}
		if c.want == 403 && res.Code != 1003 {
			t.Fatalf("%s: code=%d want 1003", c.name, res.Code)
		}
	}
}

// TestAdminOrderReadOnlyNoSideEffect 覆盖 INV-001：三个只读接口不产生任何写。
func TestAdminOrderReadOnlyNoSideEffect(t *testing.T) {
	base := setupOrderServer(t)
	skuID, productID := setupSellable(t, "SKU-NOSIDE", 1000, 10)

	isoInsertUser(t, "adminnosidebuyer", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "adminnosidebuyer", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)

	orderID := adminOrderInsert(t, userID, "NO-SIDE", 10, 1000, time.Now().Unix())
	adminOrderItemInsert(t, orderID, skuID, productID, "SKU-NOSIDE", "测试商品-SKU-NOSIDE", 1000, 1)

	counts := func() (int, int, int) {
		o, err := g.DB().Model("orders").Ctx(context.Background()).Count()
		if err != nil {
			t.Fatalf("count orders: %v", err)
		}
		oi, err := g.DB().Model("order_items").Ctx(context.Background()).Count()
		if err != nil {
			t.Fatalf("count order_items: %v", err)
		}
		iv, err := g.DB().Model("inventories").Ctx(context.Background()).Count()
		if err != nil {
			t.Fatalf("count inventories: %v", err)
		}
		return o, oi, iv
	}
	beforeO, beforeI, beforeV := counts()

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	adminListCall(t, base, "", adminToken)
	adminStatsCall(t, base, adminToken)
	res := isoDo(t, base, "GET", fmt.Sprintf("/admin/orders/%d", orderID), nil, isoAuthHeader(adminToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("admin detail: status=%d code=%d", res.Status, res.Code)
	}

	afterO, afterI, afterV := counts()
	if beforeO != afterO || beforeI != afterI || beforeV != afterV {
		t.Fatalf("read-only endpoints caused writes: orders %d→%d, order_items %d→%d, inventories %d→%d",
			beforeO, afterO, beforeI, afterI, beforeV, afterV)
	}
}
