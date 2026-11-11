package e2e

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定 B1-04-06 拼团业务：活动管理、开团/参团、成团、幂等、容量并发、超时过期与退款复用。
//
// 与普通串行测试的关键差异：
//   - TestGroupBuyConcurrentJoinCapacity 用 10 个真实用户并发抢 2 个剩余名额，
//     「先查后写」的错误实现会超员（joined_count > required_size / 订单数 > 3），
//     只有条件更新闸门 + uk_team_user 兜底才能让成功数恰好等于剩余名额；
//   - TestGroupBuyConcurrentDuplicateJoin 验证同一用户并发重复提交只产生一条参与与一个订单；
//   - TestGroupBuyExpireRaceWithJoin 验证过期推进与参团请求不会同时成功。

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"

	groupbuyv1 "cnb.cool/go-cloud-devops/my-shop/api/groupbuy/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// gbRequest 发起拼团测试请求（支持 GET/POST/PUT；PUT 用于后台修改活动）。
func gbRequest(t *testing.T, base, method, path, token string, body any) isoResult {
	t.Helper()
	c := g.Client()
	if token != "" {
		c = c.Header(isoAuthHeader(token))
	}
	ctx := context.Background()
	var (
		res *gclient.Response
		err error
	)
	switch method {
	case "GET":
		res, err = c.Get(ctx, base+path)
	case "POST":
		res, err = c.ContentJson().Post(ctx, base+path, body)
	case "PUT":
		res, err = c.ContentJson().Put(ctx, base+path, body)
	default:
		t.Fatalf("gbRequest 不支持的方法 %s", method)
	}
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Close()
	return isoDecode(res)
}

// setupGroupBuyServer 建立隔离的拼团测试环境。
// setupOrderServer 已按外键顺序清理拼团三表（先于 skus/products，避免 FK RESTRICT 阻塞），
// 这里只保留语义化入口，便于拼团用例统一初始化。
func setupGroupBuyServer(t *testing.T) string {
	t.Helper()
	return setupOrderServer(t)
}

// gbInsertActivity 直接写入活动（供构造边界场景，如已过期活动、非默认有效期）。
func gbInsertActivity(t *testing.T, productID, skuID, groupPrice, groupSize, ttlMinutes int64, status int, start, end time.Time) int64 {
	t.Helper()
	id, err := g.DB().Model("group_buy_activities").Ctx(context.Background()).Data(g.Map{
		"product_id":       productID,
		"sku_id":           skuID,
		"name":             "拼团活动-" + fmt.Sprint(time.Now().UnixNano()),
		"group_price":      groupPrice,
		"group_size":       groupSize,
		"team_ttl_minutes": ttlMinutes,
		// 直接写业务时区字面量，避免 gdb 按 UTC 格式化导致与连接层 time_zone 漂移。
		"start_time": start.Format("2006-01-02 15:04:05"),
		"end_time":   end.Format("2006-01-02 15:04:05"),
		"status":     status,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert group buy activity: %v", err)
	}
	return id
}

// gbCreateUser 创建用户、登录并返回 (token, userID, addressID)。
func gbCreateUser(t *testing.T, base, username string) (string, int64, int64) {
	t.Helper()
	userID := isoInsertUser(t, username, "pass123456")
	token, _ := isoFrontendLogin(t, base, username, "pass123456")
	addressID := orderInsertAddress(t, userID)
	return token, userID, addressID
}

// gbDecodeActivity 解码活动响应。
func gbDecodeActivity(t *testing.T, res isoResult) *groupbuyv1.Activity {
	t.Helper()
	raw, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("marshal activity: %v", err)
	}
	var activity groupbuyv1.Activity
	if err := json.Unmarshal(raw, &activity); err != nil {
		t.Fatalf("unmarshal activity: %v", err)
	}
	return &activity
}

// gbDecodeTeam 解码团详情响应。
func gbDecodeTeam(t *testing.T, res isoResult) *groupbuyv1.TeamDetail {
	t.Helper()
	raw, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("marshal team detail: %v", err)
	}
	var detail groupbuyv1.TeamDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("unmarshal team detail: %v", err)
	}
	return &detail
}

// gbTeamState 查询团的 DB 状态与人数。
func gbTeamState(t *testing.T, teamID int64) (status int, joined, required int64) {
	t.Helper()
	one, err := g.DB().Model("group_buy_teams").Ctx(context.Background()).
		Fields("status", "joined_count", "required_size").Where("id", teamID).One()
	if err != nil {
		t.Fatalf("query team %d: %v", teamID, err)
	}
	if one.IsEmpty() {
		t.Fatalf("team %d not found", teamID)
	}
	return one["status"].Int(), one["joined_count"].Int64(), one["required_size"].Int64()
}

// gbParticipantCount 统计团的参与记录数。
func gbParticipantCount(t *testing.T, teamID int64) int {
	t.Helper()
	n, err := g.DB().Model("group_buy_participants").Ctx(context.Background()).Where("team_id", teamID).Count()
	if err != nil {
		t.Fatalf("count participants: %v", err)
	}
	return n
}

// gbParticipantOrder 查询指定用户在某团的参与记录订单 id。
func gbParticipantOrder(t *testing.T, teamID, userID int64) int64 {
	t.Helper()
	one, err := g.DB().Model("group_buy_participants").Ctx(context.Background()).
		Fields("order_id").Where("team_id", teamID).Where("user_id", userID).One()
	if err != nil {
		t.Fatalf("query participant: %v", err)
	}
	if one.IsEmpty() {
		t.Fatalf("participant team=%d user=%d not found", teamID, userID)
	}
	return one["order_id"].Int64()
}

// gbOrderAmounts 查询订单状态与金额快照。
func gbOrderAmounts(t *testing.T, orderID int64) (status int, total, original, discount int64) {
	t.Helper()
	one, err := g.DB().Model("orders").Ctx(context.Background()).
		Fields("status", "total_amount", "original_amount", "discount_amount").Where("id", orderID).One()
	if err != nil {
		t.Fatalf("query order %d: %v", orderID, err)
	}
	if one.IsEmpty() {
		t.Fatalf("order %d not found", orderID)
	}
	return one["status"].Int(), one["total_amount"].Int64(), one["original_amount"].Int64(), one["discount_amount"].Int64()
}

// gbOrderItemPrice 查询订单项成交价。
func gbOrderItemPrice(t *testing.T, orderID int64) int64 {
	t.Helper()
	one, err := g.DB().Model("order_items").Ctx(context.Background()).
		Fields("price").Where("order_id", orderID).One()
	if err != nil {
		t.Fatalf("query order item of %d: %v", orderID, err)
	}
	if one.IsEmpty() {
		t.Fatalf("order item of %d not found", orderID)
	}
	return one["price"].Int64()
}

// gbUserOrderCount 统计用户订单数。
func gbUserOrderCount(t *testing.T, userID int64) int {
	t.Helper()
	n, err := g.DB().Model("orders").Ctx(context.Background()).Where("user_id", userID).Count()
	if err != nil {
		t.Fatalf("count user orders: %v", err)
	}
	return n
}

// gbExpireTeam 把团的截止时间改到过去（模拟时间流逝，避免测试 sleep）。
func gbExpireTeam(t *testing.T, teamID int64) {
	t.Helper()
	if _, err := g.DB().Model("group_buy_teams").Ctx(context.Background()).Where("id", teamID).
		Data(g.Map{"expire_at": time.Now().Add(-time.Minute)}).Update(); err != nil {
		t.Fatalf("expire team: %v", err)
	}
}

// gbCreateActivityBody 构造创建活动请求体。
func gbCreateActivityBody(name string, productID, skuID, groupPrice, groupSize, ttl int64) map[string]any {
	return map[string]any{
		"name":             name,
		"product_id":       productID,
		"sku_id":           skuID,
		"group_price":      groupPrice,
		"group_size":       groupSize,
		"team_ttl_minutes": ttl,
		"start_time":       time.Now().Add(-time.Hour).Format(time.RFC3339),
		"end_time":         time.Now().Add(time.Hour).Format(time.RFC3339),
	}
}

// TestGroupBuyActivityManagement 覆盖活动创建/修改/启停与无效参数拒绝。
func TestGroupBuyActivityManagement(t *testing.T) {
	base := setupGroupBuyServer(t)
	skuID, productID := setupSellable(t, "GB-ACT-SKU", 5000, 100)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 正常创建。
	res := gbRequest(t, base, "POST", "/admin/group-buys", adminToken,
		gbCreateActivityBody("拼团活动A", productID, skuID, 3000, 2, 10))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("create activity: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	activity := gbDecodeActivity(t, res)
	if activity.Id <= 0 || activity.Status != groupbuyv1.ActivityStatusEnabled {
		t.Fatalf("unexpected activity: %+v", activity)
	}
	if activity.GroupPrice != 3000 || activity.GroupSize != 2 || activity.SkuPrice != 5000 {
		t.Fatalf("activity price/size mismatch: %+v", activity)
	}

	// 公开列表可见。
	list := gbRequest(t, base, "GET", "/group-buys", "", nil)
	if list.Status != 200 || list.Code != 0 {
		t.Fatalf("public list: status=%d code=%d", list.Status, list.Code)
	}
	if !gbListContains(t, list, "id", activity.Id) {
		t.Fatalf("public list 未包含新活动: %+v", list.Data)
	}
	// 公开详情可见。
	detail := gbRequest(t, base, "GET", fmt.Sprintf("/group-buys/%d", activity.Id), "", nil)
	if detail.Status != 200 || detail.Code != 0 {
		t.Fatalf("public detail: status=%d code=%d", detail.Status, detail.Code)
	}

	// 无效参数：人数 < 2、时间窗倒置、价格高于 SKU 现价、商品与 SKU 不匹配。
	otherProductID := orderInsertProduct(t, orderInsertCategory(t, "GB-ACT-OTHER"), "其他商品", 1)
	cases := []struct {
		name    string
		patch   map[string]any
		status  int
		bizCode int
	}{
		{"人数小于 2", map[string]any{"group_size": 1}, 400, 21002},
		{"时间窗倒置", map[string]any{
			"start_time": time.Now().Add(time.Hour).Format(time.RFC3339),
			"end_time":   time.Now().Add(-time.Hour).Format(time.RFC3339),
		}, 400, 21002},
		{"价格高于 SKU", map[string]any{"group_price": 5001}, 400, 21004},
		{"商品与 SKU 不匹配", map[string]any{"product_id": otherProductID}, 400, 21002},
	}
	for _, tc := range cases {
		body := gbCreateActivityBody("无效活动-"+tc.name, productID, skuID, 3000, 2, 10)
		for k, v := range tc.patch {
			body[k] = v
		}
		bad := gbRequest(t, base, "POST", "/admin/group-buys", adminToken, body)
		if bad.Status != tc.status || bad.Code != tc.bizCode {
			t.Errorf("%s: status=%d code=%d want %d/%d", tc.name, bad.Status, bad.Code, tc.status, tc.bizCode)
		}
	}

	// 禁用 SKU 不可建活动。
	disabledSkuID := orderInsertSku(t, productID, "GB-ACT-DISABLED", 5000, 0)
	bad := gbRequest(t, base, "POST", "/admin/group-buys", adminToken,
		gbCreateActivityBody("禁用SKU活动", productID, disabledSkuID, 3000, 2, 10))
	if bad.Status != 409 || bad.Code != 21008 {
		t.Fatalf("disabled sku: status=%d code=%d want 409/21008", bad.Status, bad.Code)
	}

	// 修改活动。
	update := gbCreateActivityBody("拼团活动A-改", productID, skuID, 2500, 3, 20)
	updated := gbRequest(t, base, "PUT", fmt.Sprintf("/admin/group-buys/%d", activity.Id), adminToken, update)
	if updated.Status != 200 || updated.Code != 0 {
		t.Fatalf("update activity: status=%d code=%d msg=%q", updated.Status, updated.Code, updated.Message)
	}
	updatedActivity := gbDecodeActivity(t, updated)
	if updatedActivity.Name != "拼团活动A-改" || updatedActivity.GroupPrice != 2500 || updatedActivity.GroupSize != 3 {
		t.Fatalf("activity not updated: %+v", updatedActivity)
	}

	// 停用：列表消失，详情仍可访问，开团被拒绝。
	stopped := gbRequest(t, base, "POST", fmt.Sprintf("/admin/group-buys/%d/status", activity.Id), adminToken,
		map[string]any{"status": groupbuyv1.ActivityStatusDisabled})
	if stopped.Status != 200 || stopped.Code != 0 {
		t.Fatalf("disable activity: status=%d code=%d", stopped.Status, stopped.Code)
	}
	if gbDecodeActivity(t, stopped).Status != groupbuyv1.ActivityStatusDisabled {
		t.Fatal("activity status 未变为 disabled")
	}
	list = gbRequest(t, base, "GET", "/group-buys", "", nil)
	if gbListContains(t, list, "id", activity.Id) {
		t.Fatal("停用活动不应出现在公开列表")
	}

	token, _, addressID := gbCreateUser(t, base, "gbactbuyer")
	res = gbRequest(t, base, "POST", fmt.Sprintf("/group-buys/%d/teams", activity.Id), token,
		map[string]any{"address_id": addressID, "idempotency_key": "gb-act-1"})
	if res.Status != 409 || res.Code != 21003 {
		t.Fatalf("disabled activity create team: status=%d code=%d want 409/21003", res.Status, res.Code)
	}

	// 重新启用后可开团。
	enabled := gbRequest(t, base, "POST", fmt.Sprintf("/admin/group-buys/%d/status", activity.Id), adminToken,
		map[string]any{"status": groupbuyv1.ActivityStatusEnabled})
	if enabled.Status != 200 || enabled.Code != 0 {
		t.Fatalf("enable activity: status=%d code=%d", enabled.Status, enabled.Code)
	}
	res = gbRequest(t, base, "POST", fmt.Sprintf("/group-buys/%d/teams", activity.Id), token,
		map[string]any{"address_id": addressID, "idempotency_key": "gb-act-2"})
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("enabled activity create team: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
}

// gbListContains 判断列表响应 items 中是否存在指定字段值的记录。
func gbListContains(t *testing.T, res isoResult, field string, value int64) bool {
	t.Helper()
	items, ok := res.Data["items"].([]any)
	if !ok {
		return false
	}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if numeric, ok := item[field].(float64); ok && int64(numeric) == value {
			return true
		}
	}
	return false
}

// TestGroupBuyCreateTeamOrdering 验证开团与拼团订单、参与记录、库存的事务一致性。
func TestGroupBuyCreateTeamOrdering(t *testing.T) {
	base := setupGroupBuyServer(t)
	skuID, productID := setupSellable(t, "GB-ORDER-SKU", 5000, 100)
	activityID := gbInsertActivity(t, productID, skuID, 3000, 2, 10, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	token, userID, addressID := gbCreateUser(t, base, "gbleader")
	res := gbRequest(t, base, "POST", fmt.Sprintf("/group-buys/%d/teams", activityID), token,
		map[string]any{"address_id": addressID, "idempotency_key": "gb-leader-1"})
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("create team: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	detail := gbDecodeTeam(t, res)
	if detail.Status != groupbuyv1.TeamStatusPending {
		t.Fatalf("新团状态应为 pending，实际 %s", detail.Status)
	}
	if detail.JoinedCount != 1 || detail.RequiredSize != 2 || detail.GroupPrice != 3000 {
		t.Fatalf("团快照错误: %+v", detail.Team)
	}
	if detail.MyOrderId == nil || *detail.MyOrderId <= 0 {
		t.Fatal("开团必须返回关联订单 id")
	}
	orderID := *detail.MyOrderId

	// 订单金额：原价为 SKU 现价，优惠为差价，应付为拼团价；订单项成交价为拼团价。
	status, total, original, discount := gbOrderAmounts(t, orderID)
	if status != 10 || total != 3000 || original != 5000 || discount != 2000 {
		t.Fatalf("订单金额快照错误: status=%d total=%d original=%d discount=%d", status, total, original, discount)
	}
	if price := gbOrderItemPrice(t, orderID); price != 3000 {
		t.Fatalf("订单项成交价应为拼团价 3000，实际 %d", price)
	}

	// 参与记录必须指向同一订单（不允许「有订单无参与」）。
	if got := gbParticipantOrder(t, detail.Id, userID); got != orderID {
		t.Fatalf("参与记录 order_id=%d，期望 %d", got, orderID)
	}
	// 团长必须有参与记录。
	if n := gbParticipantCount(t, detail.Id); n != 1 {
		t.Fatalf("团长参与记录数=%d，期望 1", n)
	}
	// 库存扣减 1。
	if stock := orderStock(t, skuID); stock != 99 {
		t.Fatalf("开团后库存=%d，期望 99", stock)
	}

	// 详情接口返回当前用户的订单与参与记录。
	detailRes := gbRequest(t, base, "GET", fmt.Sprintf("/group-buy-teams/%d", detail.Id), token, nil)
	if detailRes.Status != 200 || detailRes.Code != 0 {
		t.Fatalf("team detail: status=%d code=%d", detailRes.Status, detailRes.Code)
	}
	loaded := gbDecodeTeam(t, detailRes)
	if loaded.MyParticipant == nil || loaded.MyOrderId == nil || *loaded.MyOrderId != orderID {
		t.Fatalf("团详情缺少当前用户参与信息: %+v", loaded)
	}
}

// TestGroupBuyJoinSuccessAndIdempotency 验证参团、成团、后续拒绝与重复参与幂等。
func TestGroupBuyJoinSuccessAndIdempotency(t *testing.T) {
	base := setupGroupBuyServer(t)
	skuID, productID := setupSellable(t, "GB-JOIN-SKU", 5000, 100)
	activityID := gbInsertActivity(t, productID, skuID, 3000, 2, 10, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	leaderToken, _, leaderAddress := gbCreateUser(t, base, "gbjoinleader")
	created := gbRequest(t, base, "POST", fmt.Sprintf("/group-buys/%d/teams", activityID), leaderToken,
		map[string]any{"address_id": leaderAddress, "idempotency_key": "gbjoinleader"})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create team: status=%d code=%d", created.Status, created.Code)
	}
	teamID := gbDecodeTeam(t, created).Id

	memberToken, memberID, memberAddress := gbCreateUser(t, base, "gbjoinmember")
	joined := gbRequest(t, base, "POST", fmt.Sprintf("/group-buy-teams/%d/join", teamID), memberToken,
		map[string]any{"address_id": memberAddress, "idempotency_key": "gbjoinmember"})
	if joined.Status != 200 || joined.Code != 0 {
		t.Fatalf("join team: status=%d code=%d msg=%q", joined.Status, joined.Code, joined.Message)
	}
	joinedDetail := gbDecodeTeam(t, joined)
	if joinedDetail.Status != groupbuyv1.TeamStatusSuccess {
		t.Fatalf("达到目标人数后应成团，实际 %s", joinedDetail.Status)
	}
	if joinedDetail.JoinedCount != 2 || joinedDetail.ClosedAt == nil {
		t.Fatalf("成团状态错误: %+v", joinedDetail.Team)
	}

	status, count, required := gbTeamState(t, teamID)
	if status != 20 || count != 2 || required != 2 {
		t.Fatalf("DB 团队状态 status=%d joined=%d required=%d", status, count, required)
	}

	// 已成团后第三人加入被拒绝，且不产生订单/参与/库存副作用。
	otherToken, _, otherAddress := gbCreateUser(t, base, "gbjointhird")
	full := gbRequest(t, base, "POST", fmt.Sprintf("/group-buy-teams/%d/join", teamID), otherToken,
		map[string]any{"address_id": otherAddress, "idempotency_key": "gbjointhird"})
	if full.Status != 409 || full.Code != 21006 {
		t.Fatalf("full team join: status=%d code=%d want 409/21006", full.Status, full.Code)
	}
	if n := gbParticipantCount(t, teamID); n != 2 {
		t.Fatalf("拒绝参团后参与记录数=%d，期望 2", n)
	}

	// 同一用户重复参团：幂等返回成功，不新增订单/参与，不重复扣库存。
	beforeOrders := gbUserOrderCount(t, memberID)
	again := gbRequest(t, base, "POST", fmt.Sprintf("/group-buy-teams/%d/join", teamID), memberToken,
		map[string]any{"address_id": memberAddress, "idempotency_key": "gb-join-member-2"})
	if again.Status != 200 || again.Code != 0 {
		t.Fatalf("repeat join: status=%d code=%d msg=%q", again.Status, again.Code, again.Message)
	}
	if after := gbUserOrderCount(t, memberID); after != beforeOrders {
		t.Fatalf("重复参团新增订单: before=%d after=%d", beforeOrders, after)
	}
	if n := gbParticipantCount(t, teamID); n != 2 {
		t.Fatalf("重复参团新增参与记录: %d", n)
	}
	if status, count, _ := gbTeamState(t, teamID); status != 20 || count != 2 {
		t.Fatalf("重复参团改变团队状态: status=%d joined=%d", status, count)
	}
	if stock := orderStock(t, skuID); stock != 98 {
		t.Fatalf("两人参团后库存=%d，期望 98", stock)
	}
}

// TestGroupBuyConcurrentJoinCapacity 是核心并发测试：10 个用户竞争 2 个剩余名额。
// 正确实现（条件更新闸门 + 唯一键）成功数恰好 2；「先查后写」会超员并多建订单/多扣库存。
func TestGroupBuyConcurrentJoinCapacity(t *testing.T) {
	base := setupGroupBuyServer(t)
	skuID, productID := setupSellable(t, "GB-CONCURRENT-SKU", 5000, 100)
	activityID := gbInsertActivity(t, productID, skuID, 3000, 3, 10, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	leaderToken, _, leaderAddress := gbCreateUser(t, base, "gbccleader")
	created := gbRequest(t, base, "POST", fmt.Sprintf("/group-buys/%d/teams", activityID), leaderToken,
		map[string]any{"address_id": leaderAddress, "idempotency_key": "gbccleader"})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create team: status=%d code=%d", created.Status, created.Code)
	}
	teamID := gbDecodeTeam(t, created).Id

	const contenders = 10
	tokens := make([]string, contenders)
	addresses := make([]int64, contenders)
	for i := 0; i < contenders; i++ {
		tokens[i], _, addresses[i] = gbCreateUser(t, base, fmt.Sprintf("gbccuser%02d", i))
	}

	type joinOutcome struct {
		status int
		code   int
		err    error
	}
	outcomes := make(chan joinOutcome, contenders)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, err := isoRequest(base, "POST", fmt.Sprintf("/group-buy-teams/%d/join", teamID), map[string]any{
				"address_id":      addresses[i],
				"idempotency_key": fmt.Sprintf("gb-cc-join-%02d", i),
			}, isoAuthHeader(tokens[i]))
			outcomes <- joinOutcome{status: res.Status, code: res.Code, err: err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(outcomes)

	success, rejected := 0, 0
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("并发参团请求失败: %v", outcome.err)
		}
		switch {
		case outcome.status == 200 && outcome.code == 0:
			success++
		case outcome.status == 409:
			// 名额已满/已成团（21006）或并发重复参与（21007）都属于正确的拒绝语义。
			if outcome.code != 21006 && outcome.code != 21007 {
				t.Fatalf("并发拒绝码=%d，期望 21006/21007", outcome.code)
			}
			rejected++
		default:
			t.Fatalf("并发参团出现非预期响应: status=%d code=%d", outcome.status, outcome.code)
		}
	}

	if success != 2 || rejected != contenders-2 {
		t.Fatalf("并发抢名额结果 success=%d rejected=%d，期望 2/%d（超员说明容量闸门失效）",
			success, rejected, contenders-2)
	}
	status, joined, required := gbTeamState(t, teamID)
	if status != 20 || joined != 3 || required != 3 {
		t.Fatalf("并发后团队状态 status=%d joined=%d required=%d，期望 20/3/3", status, joined, required)
	}
	if n := gbParticipantCount(t, teamID); n != 3 {
		t.Fatalf("并发后参与记录=%d，期望 3", n)
	}
	if stock := orderStock(t, skuID); stock != 97 {
		t.Fatalf("并发后库存=%d，期望 97（每人只扣 1 件）", stock)
	}
	// 订单数：团长 + 2 个成功参与者。
	orders, err := g.DB().Model("orders").Ctx(context.Background()).Count()
	if err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if orders != 3 {
		t.Fatalf("并发后订单数=%d，期望 3（失败请求不得留下订单）", orders)
	}
}

// TestGroupBuyConcurrentDuplicateJoin 验证同一用户并发重复提交只产生一条参与与一个订单。
func TestGroupBuyConcurrentDuplicateJoin(t *testing.T) {
	base := setupGroupBuyServer(t)
	skuID, productID := setupSellable(t, "GB-DUP-SKU", 5000, 100)
	activityID := gbInsertActivity(t, productID, skuID, 3000, 5, 10, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	leaderToken, _, leaderAddress := gbCreateUser(t, base, "gbdupleader")
	created := gbRequest(t, base, "POST", fmt.Sprintf("/group-buys/%d/teams", activityID), leaderToken,
		map[string]any{"address_id": leaderAddress, "idempotency_key": "gbdupleader"})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create team: status=%d code=%d", created.Status, created.Code)
	}
	teamID := gbDecodeTeam(t, created).Id

	memberToken, memberID, memberAddress := gbCreateUser(t, base, "gbdupmember")

	const attempts = 8
	type outcome struct {
		status int
		code   int
		err    error
	}
	results := make(chan outcome, attempts)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			// 每次使用不同幂等键：若实现依赖「先查后写」，会重复建单并把 joined_count 加多次。
			res, err := isoRequest(base, "POST", fmt.Sprintf("/group-buy-teams/%d/join", teamID), map[string]any{
				"address_id":      memberAddress,
				"idempotency_key": fmt.Sprintf("gb-dup-attempt-%02d", i),
			}, isoAuthHeader(memberToken))
			results <- outcome{status: res.Status, code: res.Code, err: err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	success := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("并发重复参团请求失败: %v", result.err)
		}
		if result.status == 200 && result.code == 0 {
			success++
			continue
		}
		if result.status != 409 || (result.code != 21007 && result.code != 9006) {
			t.Fatalf("并发重复参团非预期响应: status=%d code=%d", result.status, result.code)
		}
	}
	if success == 0 {
		t.Fatal("并发重复参团没有任何成功请求（至少首次应成功）")
	}

	if n := gbParticipantCount(t, teamID); n != 2 {
		t.Fatalf("重复参团后参与记录=%d，期望 2（团长+该用户）", n)
	}
	if n := gbUserOrderCount(t, memberID); n != 1 {
		t.Fatalf("重复参团后该用户订单数=%d，期望 1", n)
	}
	if status, joined, _ := gbTeamState(t, teamID); status != 10 || joined != 2 {
		t.Fatalf("重复参团后团队状态 status=%d joined=%d，期望 10/2", status, joined)
	}
	// 团长开团扣 1，该用户参团扣 1；重复提交不得再扣。
	if stock := orderStock(t, skuID); stock != 98 {
		t.Fatalf("重复参团后库存=%d，期望 98（团长 1 + 该用户 1）", stock)
	}
}

// TestGroupBuyExpireAndRefundReuse 验证超时失败：未支付订单取消恢复库存，
// 已支付订单不伪造退款，而是复用现有退款链路完成资金/库存补偿。
func TestGroupBuyExpireAndRefundReuse(t *testing.T) {
	base := setupGroupBuyServer(t)
	skuID, productID := setupSellable(t, "GB-EXPIRE-SKU", 5000, 100)
	activityID := gbInsertActivity(t, productID, skuID, 3000, 3, 10, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	leaderToken, _, leaderAddress := gbCreateUser(t, base, "gbexpleader")
	created := gbRequest(t, base, "POST", fmt.Sprintf("/group-buys/%d/teams", activityID), leaderToken,
		map[string]any{"address_id": leaderAddress, "idempotency_key": "gbexpleader"})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create team: status=%d code=%d", created.Status, created.Code)
	}
	createdDetail := gbDecodeTeam(t, created)
	teamID := createdDetail.Id
	leaderOrderID := *createdDetail.MyOrderId

	memberToken, memberID, memberAddress := gbCreateUser(t, base, "gbexpmember")
	joined := gbRequest(t, base, "POST", fmt.Sprintf("/group-buy-teams/%d/join", teamID), memberToken,
		map[string]any{"address_id": memberAddress, "idempotency_key": "gbexpmember"})
	if joined.Status != 200 || joined.Code != 0 {
		t.Fatalf("join team: status=%d code=%d", joined.Status, joined.Code)
	}
	memberOrderID := *gbDecodeTeam(t, joined).MyOrderId

	// 参与者支付订单（拼团未满员，团仍 pending）。
	paid := gbRequest(t, base, "POST", fmt.Sprintf("/orders/%d/pay", memberOrderID), memberToken, nil)
	if paid.Status != 200 || paid.Code != 0 {
		t.Fatalf("pay group buy order: status=%d code=%d msg=%q", paid.Status, paid.Code, paid.Message)
	}
	if status, _, _, _ := gbOrderAmounts(t, memberOrderID); status != 20 {
		t.Fatalf("支付后订单状态=%d，期望 20", status)
	}
	if stock := orderStock(t, skuID); stock != 98 {
		t.Fatalf("两人参团后库存=%d，期望 98", stock)
	}

	// 团超时：扫描器入口（与生产 worker 同一实现）。
	gbExpireTeam(t, teamID)
	count, err := service.GroupBuy().ExpireTeams(context.Background(), 100)
	if err != nil {
		t.Fatalf("expire teams: %v", err)
	}
	if count < 1 {
		t.Fatalf("过期扫描未处理任何团，count=%d", count)
	}
	if status, joinedCount, _ := gbTeamState(t, teamID); status != 40 || joinedCount != 2 {
		t.Fatalf("过期团状态 status=%d joined=%d，期望 40/2", status, joinedCount)
	}

	// 未支付订单被取消并恢复库存（1 件）。
	if status, _, _, _ := gbOrderAmounts(t, leaderOrderID); status != 60 {
		t.Fatalf("团长未支付订单状态=%d，期望 60（超时取消）", status)
	}
	if stock := orderStock(t, skuID); stock != 99 {
		t.Fatalf("取消未支付订单后库存=%d，期望 99", stock)
	}
	// 已支付订单不被伪造退款，仍为已支付。
	if status, _, _, _ := gbOrderAmounts(t, memberOrderID); status != 20 {
		t.Fatalf("已支付订单状态=%d，期望保持 20（禁止伪造退款）", status)
	}

	// 复用现有退款链路完成补偿：申请 → 审核同意 → 执行成功 → 订单 refunded + 库存恢复。
	applied := gbRequest(t, base, "POST", fmt.Sprintf("/orders/%d/refund", memberOrderID), memberToken,
		map[string]any{"reason": "拼团超时未成团"})
	if applied.Status != 200 || applied.Code != 0 {
		t.Fatalf("apply refund: status=%d code=%d msg=%q", applied.Status, applied.Code, applied.Message)
	}
	refundID := int64(applied.Data["id"].(float64))

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	reviewed := gbRequest(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/review", refundID), adminToken,
		map[string]any{"result": "approve", "reason": "同意退款"})
	if reviewed.Status != 200 || reviewed.Code != 0 {
		t.Fatalf("review refund: status=%d code=%d msg=%q", reviewed.Status, reviewed.Code, reviewed.Message)
	}
	executed := gbRequest(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/execute", refundID), adminToken,
		map[string]any{"result": "success", "reason": "Mock 退款成功"})
	if executed.Status != 200 || executed.Code != 0 {
		t.Fatalf("execute refund: status=%d code=%d msg=%q", executed.Status, executed.Code, executed.Message)
	}

	if status, _, _, _ := gbOrderAmounts(t, memberOrderID); status != 70 {
		t.Fatalf("退款成功后订单状态=%d，期望 70", status)
	}
	if stock := orderStock(t, skuID); stock != 100 {
		t.Fatalf("退款成功后库存=%d，期望 100（库存只恢复一次）", stock)
	}
	// 参与记录保留，团终态以团 status 为准（不逐行改写参与记录）。
	if n := gbParticipantCount(t, teamID); n != 2 {
		t.Fatalf("退款后参与记录=%d，期望保留 2 条历史", n)
	}
	if _, err := gbParticipantOrderStrict(t, teamID, memberID); err != nil {
		t.Fatalf("退款后参与记录不应丢失: %v", err)
	}
}

// gbParticipantOrderStrict 查询参与记录订单 id，返回错误而非直接失败（用于「存在性」断言）。
func gbParticipantOrderStrict(t *testing.T, teamID, userID int64) (int64, error) {
	t.Helper()
	one, err := g.DB().Model("group_buy_participants").Ctx(context.Background()).
		Fields("order_id").Where("team_id", teamID).Where("user_id", userID).One()
	if err != nil {
		return 0, err
	}
	if one.IsEmpty() {
		return 0, fmt.Errorf("participant team=%d user=%d not found", teamID, userID)
	}
	return one["order_id"].Int64(), nil
}

// TestGroupBuyExpireRaceWithJoin 验证超时推进与参团请求并发时不会产生矛盾状态：
// 已过期团不可能再成功加入（expired 且参与数 > 1 即为矛盾）。
func TestGroupBuyExpireRaceWithJoin(t *testing.T) {
	base := setupGroupBuyServer(t)
	skuID, productID := setupSellable(t, "GB-RACE-SKU", 5000, 100)
	activityID := gbInsertActivity(t, productID, skuID, 3000, 3, 10, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	leaderToken, _, leaderAddress := gbCreateUser(t, base, "gbraceleader")
	created := gbRequest(t, base, "POST", fmt.Sprintf("/group-buys/%d/teams", activityID), leaderToken,
		map[string]any{"address_id": leaderAddress, "idempotency_key": "gbraceleader"})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create team: status=%d code=%d", created.Status, created.Code)
	}
	teamID := gbDecodeTeam(t, created).Id

	const contenders = 6
	tokens := make([]string, contenders)
	addresses := make([]int64, contenders)
	for i := 0; i < contenders; i++ {
		tokens[i], _, addresses[i] = gbCreateUser(t, base, fmt.Sprintf("gbraceuser%02d", i))
	}

	// 已过期：参团闸门（expire_at > NOW()）必然失败，过期推进必然成功。
	gbExpireTeam(t, teamID)

	var wg sync.WaitGroup
	start := make(chan struct{})
	joinResults := make(chan isoResult, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, err := isoRequest(base, "POST", fmt.Sprintf("/group-buy-teams/%d/join", teamID), map[string]any{
				"address_id":      addresses[i],
				"idempotency_key": fmt.Sprintf("gb-race-join-%02d", i),
			}, isoAuthHeader(tokens[i]))
			if err != nil {
				joinResults <- isoResult{Status: -1, Message: err.Error()}
				return
			}
			joinResults <- res
		}(i)
	}
	expireDone := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_, _ = service.GroupBuy().ExpireTeams(context.Background(), 100)
		close(expireDone)
	}()
	close(start)
	wg.Wait()
	close(joinResults)

	for res := range joinResults {
		if res.Status == -1 {
			t.Fatalf("并发参团请求失败: %s", res.Message)
		}
		if res.Status != 409 || res.Code != 21006 {
			t.Fatalf("已过期团参团应返回 409/21006，实际 status=%d code=%d", res.Status, res.Code)
		}
	}
	<-expireDone

	status, joined, _ := gbTeamState(t, teamID)
	if status != 40 {
		t.Fatalf("并发过期后团状态=%d，期望 40", status)
	}
	if joined != 1 {
		t.Fatalf("过期团 joined_count=%d，期望 1（未成功的参团不得计入）", joined)
	}
	if n := gbParticipantCount(t, teamID); n != 1 {
		t.Fatalf("过期团参与记录=%d，期望 1", n)
	}
	if stock := orderStock(t, skuID); stock != 100 {
		t.Fatalf("过期后库存=%d，期望 100（团长未支付订单被取消恢复）", stock)
	}
	// 过期后再次开团不受影响（活动仍在时间窗内）。
	again := gbRequest(t, base, "POST", fmt.Sprintf("/group-buys/%d/teams", activityID), leaderToken,
		map[string]any{"address_id": leaderAddress, "idempotency_key": "gb-race-leader-2"})
	if again.Status != 200 || again.Code != 0 {
		t.Fatalf("过期后重新开团: status=%d code=%d msg=%q", again.Status, again.Code, again.Message)
	}
}
