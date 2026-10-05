package cmd

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定「商品评价」核心闭环。覆盖 product-review-v1 的 AC-001 至 AC-008 与关键不变量。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"
)

// reviewData 是评价写接口/本人列表返回的完整评价结构。
type reviewData struct {
	Id          int64  `json:"id"`
	UserId      int64  `json:"user_id"`
	OrderItemId int64  `json:"order_item_id"`
	ProductId   int64  `json:"product_id"`
	SkuId       int64  `json:"sku_id"`
	Rating      int    `json:"rating"`
	Content     string `json:"content"`
	Status      string `json:"status"`
}

// reviewListData 是商品公开评价列表与汇总响应。
type reviewListData struct {
	Items     []reviewItemData `json:"items"`
	Page      int              `json:"page"`
	Size      int              `json:"size"`
	Total     int              `json:"total"`
	AvgRating float64          `json:"avg_rating"`
	Count     int              `json:"count"`
}

// reviewItemData 是公开评价条目。
type reviewItemData struct {
	Id        int64  `json:"id"`
	ProductId int64  `json:"product_id"`
	SkuId     int64  `json:"sku_id"`
	Rating    int    `json:"rating"`
	Content   string `json:"content"`
	UserId    int64  `json:"user_id"`
}

// myListData 是本人全部评价列表响应。
type myListData struct {
	Items []reviewData `json:"items"`
	Total int          `json:"total"`
	Page  int          `json:"page"`
	Size  int          `json:"size"`
}

// reviewFixture 是一次「已完成订单 + 买家」测试夹具。
type reviewFixture struct {
	base        string
	token       string
	userID      int64
	addressID   int64
	orderItemID int64
	productID   int64
	skuID       int64
}

// setupReviewServer 建立隔离的评价测试环境：复用订单域初始化后清空 reviews 表。
func setupReviewServer(t *testing.T) string {
	t.Helper()
	base := setupOrderServer(t)
	if _, err := g.DB().Exec(context.Background(), "DELETE FROM reviews"); err != nil {
		t.Fatalf("clean reviews: %v", err)
	}
	return base
}

// reviewCall 发起评价相关请求（支持 GET/POST/PUT/DELETE），解码统一响应。
func reviewCall(t *testing.T, base, method, path, token string, body any) isoResult {
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

// decodeData 将统一响应中的 data 解码为具体结构。
func decodeData[T any](t *testing.T, data map[string]any) T {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	return out
}

// reviewSetupCompletedOrder 建立一个已完成订单的买家并返回夹具：
// 建可售 SKU → 下单 → 支付 → 发货 → 收货（completed），得到可评价的订单项。
func reviewSetupCompletedOrder(t *testing.T, username string) reviewFixture {
	t.Helper()
	base := setupReviewServer(t)
	skuID, productID := setupSellable(t, "SKU-"+username, 1000, 10)
	isoInsertUser(t, username, "buyerpass123")
	token, _ := isoFrontendLogin(t, base, username, "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-" + username,
		"sku_id": skuID, "quantity": 1,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create order: status=%d code=%d", created.Status, created.Code)
	}
	orderID := created.Data.Id

	detail := orderCall(t, base, "GET", fmt.Sprintf("/orders/%d", orderID), token, nil)
	if len(detail.Data.Items) == 0 {
		t.Fatalf("order %d has no items", orderID)
	}
	orderItemID := detail.Data.Items[0].Id

	if p := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", orderID), token, nil); p.Status != 200 || p.Code != 0 {
		t.Fatalf("pay: status=%d code=%d", p.Status, p.Code)
	}
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	if sh := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/ship", orderID), adminToken, map[string]any{}); sh.Status != 200 || sh.Code != 0 {
		t.Fatalf("ship: status=%d code=%d", sh.Status, sh.Code)
	}
	if rc := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/receive", orderID), token, nil); rc.Status != 200 || rc.Code != 0 {
		t.Fatalf("receive: status=%d code=%d", rc.Status, rc.Code)
	}

	return reviewFixture{base: base, token: token, userID: userID, addressID: addressID, orderItemID: orderItemID, productID: productID, skuID: skuID}
}

// reviewCount 统计 reviews 表当前行数。
func reviewCount(t *testing.T) int {
	t.Helper()
	n, err := g.DB().Model("reviews").Ctx(context.Background()).Count()
	if err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	return n
}

// TestReviewCreatePublicListAndSummary 覆盖 AC-001/AC-005 与 INV-003/INV-005/INV-006：
// 登录买家对已完成订单项提交评价成功且归属正确；公开列表与汇总（avg/count）正确。
func TestReviewCreatePublicListAndSummary(t *testing.T) {
	fx := reviewSetupCompletedOrder(t, "buyer1")

	res := reviewCall(t, fx.base, "POST", "/reviews", fx.token, map[string]any{
		"order_item_id": fx.orderItemID, "rating": 5, "content": "很好用",
	})
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("create review: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	rv := decodeData[reviewData](t, res.Data)
	if rv.UserId != fx.userID {
		t.Fatalf("review user_id=%d want %d", rv.UserId, fx.userID)
	}
	if rv.ProductId != fx.productID || rv.SkuId != fx.skuID {
		t.Fatalf("derived product/sku=%d/%d want %d/%d", rv.ProductId, rv.SkuId, fx.productID, fx.skuID)
	}
	if rv.Status != "published" {
		t.Fatalf("review status=%q want published", rv.Status)
	}

	lr := reviewCall(t, fx.base, "GET", fmt.Sprintf("/products/%d/reviews", fx.productID), "", nil)
	if lr.Status != 200 || lr.Code != 0 {
		t.Fatalf("public list: status=%d code=%d", lr.Status, lr.Code)
	}
	ld := decodeData[reviewListData](t, lr.Data)
	if ld.Count != 1 || ld.Total != 1 {
		t.Fatalf("count/total=%d/%d want 1", ld.Count, ld.Total)
	}
	if ld.AvgRating != 5.0 {
		t.Fatalf("avg_rating=%v want 5.0", ld.AvgRating)
	}
	if len(ld.Items) != 1 || ld.Items[0].Rating != 5 || ld.Items[0].Content != "很好用" {
		t.Fatalf("public item mismatch: %+v", ld.Items)
	}
}

// TestReviewEligibilityRejected 覆盖 AC-002/INV-003：未购买、订单未完成、订单项不存在均 409/10002 且无写入。
func TestReviewEligibilityRejected(t *testing.T) {
	fx := reviewSetupCompletedOrder(t, "owner1")

	// 未购买该商品的用户评价他人订单项 → 409/10002。
	isoInsertUser(t, "intruder1", "buyerpass123")
	intruderToken, _ := isoFrontendLogin(t, fx.base, "intruder1", "buyerpass123")
	res := reviewCall(t, fx.base, "POST", "/reviews", intruderToken, map[string]any{
		"order_item_id": fx.orderItemID, "rating": 5, "content": "评价",
	})
	if res.Status != 409 || res.Code != 10002 {
		t.Fatalf("intruder review: status=%d code=%d want 409/10002", res.Status, res.Code)
	}

	// 订单未完成（待支付）→ 409/10002。
	sku2, _ := setupSellable(t, "SKU-pending", 1000, 10)
	created := orderCall(t, fx.base, "POST", "/orders", fx.token, map[string]any{
		"source": "direct", "address_id": fx.addressID, "idempotency_key": "key-pending",
		"sku_id": sku2, "quantity": 1,
	})
	if created.Status != 200 {
		t.Fatalf("create pending order: %d", created.Status)
	}
	detail := orderCall(t, fx.base, "GET", fmt.Sprintf("/orders/%d", created.Data.Id), fx.token, nil)
	pendingItemID := detail.Data.Items[0].Id
	res = reviewCall(t, fx.base, "POST", "/reviews", fx.token, map[string]any{
		"order_item_id": pendingItemID, "rating": 5, "content": "评价",
	})
	if res.Status != 409 || res.Code != 10002 {
		t.Fatalf("pending order review: status=%d code=%d want 409/10002", res.Status, res.Code)
	}

	// 不存在的订单项 → 409/10002。
	res = reviewCall(t, fx.base, "POST", "/reviews", fx.token, map[string]any{
		"order_item_id": 999999, "rating": 5, "content": "评价",
	})
	if res.Status != 409 || res.Code != 10002 {
		t.Fatalf("nonexistent item review: status=%d code=%d want 409/10002", res.Status, res.Code)
	}

	if reviewCount(t) != 0 {
		t.Fatalf("ineligible submissions must not write, count=%d", reviewCount(t))
	}
}

// TestReviewServerDerivesOwnership 覆盖 AC-003/INV-001：伪造归属字段被服务端忽略，以 Principal 与订单项为准。
func TestReviewServerDerivesOwnership(t *testing.T) {
	fx := reviewSetupCompletedOrder(t, "buyer3")

	res := reviewCall(t, fx.base, "POST", "/reviews", fx.token, map[string]any{
		"order_item_id": fx.orderItemID, "rating": 4, "content": "不错",
		"user_id": 999999, "product_id": 999998, "sku_id": 999997,
	})
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("create review: status=%d code=%d", res.Status, res.Code)
	}
	rv := decodeData[reviewData](t, res.Data)
	if rv.UserId != fx.userID || rv.ProductId != fx.productID || rv.SkuId != fx.skuID {
		t.Fatalf("forged fields not ignored: user=%d product=%d sku=%d", rv.UserId, rv.ProductId, rv.SkuId)
	}
}

// TestReviewDuplicateRejected 覆盖 AC-004/INV-002：同一订单项重复提交 409/10003，仅一条。
func TestReviewDuplicateRejected(t *testing.T) {
	fx := reviewSetupCompletedOrder(t, "buyer4")

	body := map[string]any{"order_item_id": fx.orderItemID, "rating": 5, "content": "第一次"}
	if first := reviewCall(t, fx.base, "POST", "/reviews", fx.token, body); first.Status != 200 || first.Code != 0 {
		t.Fatalf("first review: status=%d code=%d", first.Status, first.Code)
	}
	if second := reviewCall(t, fx.base, "POST", "/reviews", fx.token, body); second.Status != 409 || second.Code != 10003 {
		t.Fatalf("dup review: status=%d code=%d want 409/10003", second.Status, second.Code)
	}
	if reviewCount(t) != 1 {
		t.Fatalf("review count=%d want 1", reviewCount(t))
	}
}

// TestReviewConcurrentDuplicateSingleRow 覆盖 AC-004/INV-002（并发）：同一订单项并发提交至多一条。
func TestReviewConcurrentDuplicateSingleRow(t *testing.T) {
	fx := reviewSetupCompletedOrder(t, "buyer5")

	const n = 8
	var wg sync.WaitGroup
	results := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := reviewCall(t, fx.base, "POST", "/reviews", fx.token, map[string]any{
				"order_item_id": fx.orderItemID, "rating": 5, "content": "并发",
			})
			results <- (res.Status == 200 && res.Code == 0)
		}()
	}
	wg.Wait()
	close(results)

	ok := 0
	for r := range results {
		if r {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("exactly one concurrent review should succeed, got %d", ok)
	}
	if reviewCount(t) != 1 {
		t.Fatalf("review count=%d want 1", reviewCount(t))
	}
}

// TestReviewUserIsolation 覆盖 AC-006/INV-004：他人/不存在评价统一 404/10001 且无写入。
func TestReviewUserIsolation(t *testing.T) {
	fx := reviewSetupCompletedOrder(t, "owner2")

	created := reviewCall(t, fx.base, "POST", "/reviews", fx.token, map[string]any{
		"order_item_id": fx.orderItemID, "rating": 4, "content": "自己的评价",
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	rv := decodeData[reviewData](t, created.Data)

	isoInsertUser(t, "intruder2", "buyerpass123")
	intruderToken, _ := isoFrontendLogin(t, fx.base, "intruder2", "buyerpass123")

	upd := reviewCall(t, fx.base, "PUT", fmt.Sprintf("/reviews/%d", rv.Id), intruderToken, map[string]any{
		"rating": 1, "content": "篡改",
	})
	if upd.Status != 404 || upd.Code != 10001 {
		t.Fatalf("intruder update: status=%d code=%d want 404/10001", upd.Status, upd.Code)
	}
	del := reviewCall(t, fx.base, "DELETE", fmt.Sprintf("/reviews/%d", rv.Id), intruderToken, nil)
	if del.Status != 404 || del.Code != 10001 {
		t.Fatalf("intruder delete: status=%d code=%d want 404/10001", del.Status, del.Code)
	}
	// 评价仍为 published，未被篡改。
	v, err := g.DB().Model("reviews").Ctx(context.Background()).Fields("status").Where("id", rv.Id).Value()
	if err != nil {
		t.Fatalf("query review status: %v", err)
	}
	if v == nil || v.Int() != 1 {
		t.Fatalf("intruder must not change review, status=%v want 1", v)
	}

	// 不存在评价 → 404/10001。
	miss := reviewCall(t, fx.base, "DELETE", "/reviews/999999", fx.token, nil)
	if miss.Status != 404 || miss.Code != 10001 {
		t.Fatalf("missing review delete: status=%d code=%d want 404/10001", miss.Status, miss.Code)
	}
}

// TestReviewAdminTakeDownPermission 覆盖 AC-007/INV-007：无权限 403、超管下架成功且评价从公开列表消失。
func TestReviewAdminTakeDownPermission(t *testing.T) {
	fx := reviewSetupCompletedOrder(t, "owner3")

	created := reviewCall(t, fx.base, "POST", "/reviews", fx.token, map[string]any{
		"order_item_id": fx.orderItemID, "rating": 3, "content": "违规内容",
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	rv := decodeData[reviewData](t, created.Data)

	// 无权限普通管理员 → 403/1003，无副作用。
	isoInsertAdmin(t, "plainadmin1", "plainadmin123")
	plainToken, _ := isoAdminLogin(t, fx.base, "plainadmin1", "plainadmin123")
	res := reviewCall(t, fx.base, "POST", fmt.Sprintf("/admin/reviews/%d/take-down", rv.Id), plainToken, map[string]any{})
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("plain admin take-down: status=%d code=%d want 403/1003", res.Status, res.Code)
	}

	// 普通用户 token 打后台 → 403。
	res = reviewCall(t, fx.base, "POST", fmt.Sprintf("/admin/reviews/%d/take-down", rv.Id), fx.token, map[string]any{})
	if res.Status != 403 {
		t.Fatalf("user token take-down: status=%d want 403", res.Status)
	}

	// 评价仍公开。
	lr := reviewCall(t, fx.base, "GET", fmt.Sprintf("/products/%d/reviews", fx.productID), "", nil)
	if ld := decodeData[reviewListData](t, lr.Data); ld.Count != 1 {
		t.Fatalf("review should still be public, count=%d want 1", ld.Count)
	}

	// 超管下架 → 成功，评价从公开列表与汇总消失。
	adminToken, _ := isoAdminLogin(t, fx.base, isoSuperUsername, isoAdminPassword)
	res = reviewCall(t, fx.base, "POST", fmt.Sprintf("/admin/reviews/%d/take-down", rv.Id), adminToken, map[string]any{})
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("super take-down: status=%d code=%d", res.Status, res.Code)
	}
	lr = reviewCall(t, fx.base, "GET", fmt.Sprintf("/products/%d/reviews", fx.productID), "", nil)
	if ld := decodeData[reviewListData](t, lr.Data); ld.Count != 0 || ld.AvgRating != 0 {
		t.Fatalf("taken_down review should be invisible, count=%d avg=%v", ld.Count, ld.AvgRating)
	}
}

// TestReviewInvalidInputRejected 覆盖 AC-008：星级越界、内容为空/超长均 400/10004 且无写入。
func TestReviewInvalidInputRejected(t *testing.T) {
	fx := reviewSetupCompletedOrder(t, "buyer6")

	cases := []map[string]any{
		{"order_item_id": fx.orderItemID, "rating": 0, "content": "内容"},                     // 星级下越界
		{"order_item_id": fx.orderItemID, "rating": 6, "content": "内容"},                     // 星级上越界
		{"order_item_id": fx.orderItemID, "rating": 5, "content": ""},                       // 内容为空
		{"order_item_id": fx.orderItemID, "rating": 5, "content": "   "},                    // 内容 trim 后为空
		{"order_item_id": fx.orderItemID, "rating": 5, "content": strings.Repeat("a", 501)}, // 内容超长
	}
	for i, body := range cases {
		res := reviewCall(t, fx.base, "POST", "/reviews", fx.token, body)
		if res.Status != 400 || res.Code != 10004 {
			t.Fatalf("case %d: status=%d code=%d want 400/10004", i, res.Status, res.Code)
		}
	}

	// order_item_id 缺失 → 400/1001（参数错误）。
	res := reviewCall(t, fx.base, "POST", "/reviews", fx.token, map[string]any{"rating": 5, "content": "内容"})
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("missing order_item_id: status=%d code=%d want 400/1001", res.Status, res.Code)
	}

	if reviewCount(t) != 0 {
		t.Fatalf("invalid input must not write, count=%d", reviewCount(t))
	}
}

// TestReviewUpdateAndDelete 覆盖 AC-006/INV-005/INV-006 与「软删除 + 永久唯一」：
// 修改/删除本人评价正确更新公开列表与汇总；删除后不可重新评价。
func TestReviewUpdateAndDelete(t *testing.T) {
	fx := reviewSetupCompletedOrder(t, "buyer7")

	created := reviewCall(t, fx.base, "POST", "/reviews", fx.token, map[string]any{
		"order_item_id": fx.orderItemID, "rating": 5, "content": "原始内容",
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	rv := decodeData[reviewData](t, created.Data)

	// 修改本人评价。
	upd := reviewCall(t, fx.base, "PUT", fmt.Sprintf("/reviews/%d", rv.Id), fx.token, map[string]any{
		"rating": 3, "content": "修改后",
	})
	if upd.Status != 200 || upd.Code != 0 {
		t.Fatalf("update: status=%d code=%d", upd.Status, upd.Code)
	}
	if uv := decodeData[reviewData](t, upd.Data); uv.Rating != 3 || uv.Content != "修改后" {
		t.Fatalf("update mismatch: %+v", uv)
	}

	// 公开列表反映修改（avg=3）。
	lr := reviewCall(t, fx.base, "GET", fmt.Sprintf("/products/%d/reviews", fx.productID), "", nil)
	if ld := decodeData[reviewListData](t, lr.Data); ld.Count != 1 || ld.AvgRating != 3.0 {
		t.Fatalf("after update count/avg=%d/%v want 1/3.0", ld.Count, ld.AvgRating)
	}

	// 本人列表含该评价且 status=published。
	my := reviewCall(t, fx.base, "GET", "/my/reviews", fx.token, nil)
	if md := decodeData[myListData](t, my.Data); md.Total != 1 || len(md.Items) != 1 || md.Items[0].Status != "published" {
		t.Fatalf("my list mismatch: total=%d items=%d", md.Total, len(md.Items))
	}

	// 删除本人评价 → 200，公开列表 count=0、avg=0。
	del := reviewCall(t, fx.base, "DELETE", fmt.Sprintf("/reviews/%d", rv.Id), fx.token, nil)
	if del.Status != 200 || del.Code != 0 {
		t.Fatalf("delete: status=%d code=%d", del.Status, del.Code)
	}
	if dv := decodeData[reviewData](t, del.Data); dv.Status != "deleted" {
		t.Fatalf("deleted status=%q want deleted", dv.Status)
	}
	lr = reviewCall(t, fx.base, "GET", fmt.Sprintf("/products/%d/reviews", fx.productID), "", nil)
	if ld := decodeData[reviewListData](t, lr.Data); ld.Count != 0 || ld.AvgRating != 0 {
		t.Fatalf("after delete count/avg=%d/%v want 0/0", ld.Count, ld.AvgRating)
	}

	// 删除后不可重新评价（永久唯一槽位）→ 409/10003。
	re := reviewCall(t, fx.base, "POST", "/reviews", fx.token, map[string]any{
		"order_item_id": fx.orderItemID, "rating": 5, "content": "重新评价",
	})
	if re.Status != 409 || re.Code != 10003 {
		t.Fatalf("re-review after delete: status=%d code=%d want 409/10003", re.Status, re.Code)
	}
}
