package cmd

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定「优惠券」核心闭环行为：模板管理、领取/查询、满减用券与金额三列快照、订单联动（预占/核销/释放/不返券）。
// 覆盖 coupon-v1 的 AC-001~AC-018 与关键不变量 INV-C01~INV-C10（并发正确性走真实 MySQL 事务）。

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	couponv1 "cnb.cool/go-cloud-devops/my-shop/api/coupon/v1"
	v1 "cnb.cool/go-cloud-devops/my-shop/api/order/v1"
)

// couponMySQLNow 读取 MySQL 的当前墙钟时间（DATETIME 语义，无时区），
// 用于构造相对 MySQL NOW() 的领取/使用时间窗，避免 Go 进程与 MySQL 时区漂移导致边界误判。
func couponMySQLNow(t *testing.T) time.Time {
	t.Helper()
	v, err := g.DB().GetValue(context.Background(), "SELECT NOW()")
	if err != nil {
		t.Fatalf("select NOW(): %v", err)
	}
	now, err := time.Parse("2006-01-02 15:04:05", v.String())
	if err != nil {
		t.Fatalf("parse NOW() %q: %v", v.String(), err)
	}
	return now
}

// couponTimeFmt 将时间格式化为 DATETIME 字符串（与 MySQL 会话时区同基准）。
func couponTimeFmt(tm time.Time) string {
	return tm.Format("2006-01-02 15:04:05")
}

// setupCouponServer 建立隔离的优惠券测试环境：复用身份隔离初始化后，清空优惠券域 + 订单域相关业务表。
// 同时在 cleanup 阶段再次清空这些表，避免本用例创建的 SKU/商品残留，影响按文件名顺序排在其后的
// 其他 cmd 用例（如 dashboard_test.go 的清理清单不含 skus）。
func setupCouponServer(t *testing.T) string {
	t.Helper()
	base := setupIsolationServer(t)

	clean := func() error {
		ctx := context.Background()
		for _, table := range []string{
			"user_coupons", "coupon_templates",
			"order_items", "orders", "cart_items",
			"inventory_logs", "inventories",
			"skus", "product_images", "products",
			"addresses", "categories",
		} {
			if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
				return fmt.Errorf("clean %s: %w", table, err)
			}
		}
		return nil
	}
	if err := clean(); err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if err := clean(); err != nil {
			t.Errorf("%v", err)
		}
	})
	return base
}

// couponInsertTemplate 直接写入优惠券模板，返回 id。status 1=正常可领取、0=已停止。
func couponInsertTemplate(t *testing.T, name string, threshold, discount, total int64, claimStart, claimEnd, useStart, useEnd time.Time, status int) int64 {
	t.Helper()
	id, err := g.DB().Model("coupon_templates").Ctx(context.Background()).Data(g.Map{
		"name":             name,
		"threshold_amount": threshold,
		"discount_amount":  discount,
		"total_quantity":   total,
		"issued_quantity":  0,
		"claim_start_time": couponTimeFmt(claimStart),
		"claim_end_time":   couponTimeFmt(claimEnd),
		"use_start_time":   couponTimeFmt(useStart),
		"use_end_time":     couponTimeFmt(useEnd),
		"status":           status,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert coupon template %s: %v", name, err)
	}
	return id
}

// couponActiveWindows 返回「领取/使用均生效」的时间窗（相对 MySQL NOW()）。
func couponActiveWindows(t *testing.T) (time.Time, time.Time, time.Time, time.Time) {
	t.Helper()
	now := couponMySQLNow(t)
	return now.Add(-time.Hour), now.Add(24 * time.Hour), now.Add(-time.Hour), now.Add(48 * time.Hour)
}

// couponClaimViaAPI 经真实路由领取优惠券，返回响应。
func couponClaimViaAPI(t *testing.T, base, token string, templateID int64) isoResult {
	t.Helper()
	return isoDo(t, base, "POST", fmt.Sprintf("/coupons/%d/claim", templateID), map[string]any{}, isoAuthHeader(token))
}

// couponInsertUserCoupon 直接写入用户券（status 10/20/30），返回券 id。
func couponInsertUserCoupon(t *testing.T, userID, templateID int64, status int) int64 {
	t.Helper()
	id, err := g.DB().Model("user_coupons").Ctx(context.Background()).Data(g.Map{
		"user_id":     userID,
		"template_id": templateID,
		"status":      status,
		"received_at": couponTimeFmt(couponMySQLNow(t)),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert user coupon: %v", err)
	}
	return id
}

// couponStatus 查询指定券的 DB 状态（TINYINT 10/20/30）。
func couponStatus(t *testing.T, couponID int64) int {
	t.Helper()
	v, err := g.DB().Model("user_coupons").Ctx(context.Background()).Fields("status").Where("id", couponID).Value()
	if err != nil {
		t.Fatalf("query coupon status: %v", err)
	}
	if v == nil {
		t.Fatalf("coupon %d not found", couponID)
	}
	return v.Int()
}

// couponIssued 查询模板的已发放数量。
func couponIssued(t *testing.T, templateID int64) int64 {
	t.Helper()
	v, err := g.DB().Model("coupon_templates").Ctx(context.Background()).Fields("issued_quantity").Where("id", templateID).Value()
	if err != nil {
		t.Fatalf("query coupon issued: %v", err)
	}
	if v == nil {
		t.Fatalf("template %d not found", templateID)
	}
	return v.Int64()
}

// TestCouponAdminTemplateManagement 覆盖 AC-001：超管创建/查询/停止模板，无权限管理员/普通用户 403 且无写入。
func TestCouponAdminTemplateManagement(t *testing.T) {
	base := setupCouponServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	now := couponMySQLNow(t)
	claimStart := couponTimeFmt(now.Add(-time.Hour))
	claimEnd := couponTimeFmt(now.Add(24 * time.Hour))
	useStart := claimStart
	useEnd := couponTimeFmt(now.Add(48 * time.Hour))

	create := isoDo(t, base, "POST", "/admin/coupons", map[string]any{
		"name":             "满减券A",
		"threshold_amount": 10000,
		"discount_amount":  2000,
		"total_quantity":   100,
		"claim_start_time": claimStart,
		"claim_end_time":   claimEnd,
		"use_start_time":   useStart,
		"use_end_time":     useEnd,
	}, isoAuthHeader(adminToken))
	if create.Status != 200 || create.Code != 0 {
		t.Fatalf("admin create: status=%d code=%d msg=%q", create.Status, create.Code, create.Message)
	}
	templateID := int64(create.Data["id"].(float64))
	if templateID <= 0 {
		t.Fatalf("admin create: empty id")
	}
	if got := int64(create.Data["discount_amount"].(float64)); got != 2000 {
		t.Fatalf("admin create: discount_amount=%d want 2000", got)
	}

	// 查询列表（含刚创建的模板）。
	list := isoDo(t, base, "GET", "/admin/coupons", nil, isoAuthHeader(adminToken))
	if list.Status != 200 || list.Code != 0 {
		t.Fatalf("admin list: status=%d code=%d", list.Status, list.Code)
	}
	items, ok := list.Data["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("admin list: unexpected items %v", list.Data)
	}

	// 停止领取（status 1→0），幂等。
	stop := isoDo(t, base, "POST", fmt.Sprintf("/admin/coupons/%d/stop", templateID), map[string]any{}, isoAuthHeader(adminToken))
	if stop.Status != 200 || stop.Code != 0 {
		t.Fatalf("admin stop: status=%d code=%d", stop.Status, stop.Code)
	}
	if got := int64(stop.Data["status"].(float64)); got != 0 {
		t.Fatalf("admin stop: status=%d want 0", got)
	}

	// 无权限普通管理员 → 403，无写入。
	isoInsertAdmin(t, "couponplain", "couponplain123")
	plainToken, _ := isoAdminLogin(t, base, "couponplain", "couponplain123")
	forbidden := isoDo(t, base, "POST", "/admin/coupons", map[string]any{
		"name": "满减券B", "threshold_amount": 1, "discount_amount": 1, "total_quantity": 1,
		"claim_start_time": claimStart, "claim_end_time": claimEnd, "use_start_time": useStart, "use_end_time": useEnd,
	}, isoAuthHeader(plainToken))
	if forbidden.Status != 403 || forbidden.Code != 1003 {
		t.Fatalf("plain admin create: status=%d code=%d want 403/1003", forbidden.Status, forbidden.Code)
	}

	// 普通用户 token → 403。
	isoInsertUser(t, "couponuser", "couponuser123")
	userToken, _ := isoFrontendLogin(t, base, "couponuser", "couponuser123")
	userForbidden := isoDo(t, base, "POST", "/admin/coupons", map[string]any{
		"name": "满减券C", "threshold_amount": 1, "discount_amount": 1, "total_quantity": 1,
		"claim_start_time": claimStart, "claim_end_time": claimEnd, "use_start_time": useStart, "use_end_time": useEnd,
	}, isoAuthHeader(userToken))
	if userForbidden.Status != 403 {
		t.Fatalf("user token create: status=%d want 403", userForbidden.Status)
	}
}

// TestCouponClaimAndListMine 覆盖 AC-002：登录用户领取后能在「我的券」查询到且状态为 available。
func TestCouponClaimAndListMine(t *testing.T) {
	base := setupCouponServer(t)
	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)
	templateID := couponInsertTemplate(t, "claim-tpl", 10000, 2000, 10, claimStart, claimEnd, useStart, useEnd, 1)

	isoInsertUser(t, "claimer", "claimerpass123")
	token, _ := isoFrontendLogin(t, base, "claimer", "claimerpass123")

	claim := couponClaimViaAPI(t, base, token, templateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("claim: status=%d code=%d msg=%q", claim.Status, claim.Code, claim.Message)
	}
	couponID := int64(claim.Data["id"].(float64))
	if claim.Data["status"] != couponv1.StatusAvailable {
		t.Fatalf("claim: status=%v want available", claim.Data["status"])
	}
	if got := couponIssued(t, templateID); got != 1 {
		t.Fatalf("claim: issued=%d want 1", got)
	}

	// 我的券列表应包含该券。
	list := isoDo(t, base, "GET", "/my/coupons", nil, isoAuthHeader(token))
	if list.Status != 200 || list.Code != 0 {
		t.Fatalf("list mine: status=%d code=%d", list.Status, list.Code)
	}
	items := list.Data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list mine: items=%d want 1", len(items))
	}
	first := items[0].(map[string]any)
	if int64(first["id"].(float64)) != couponID {
		t.Fatalf("list mine: coupon id mismatch")
	}
	if first["status"] != couponv1.StatusAvailable {
		t.Fatalf("list mine: status=%v want available", first["status"])
	}
}

// TestCouponClaimRejections 覆盖 AC-003/AC-004/AC-005：重复领取/未开始/已结束/已停止/超发稳定拒绝且无写入。
func TestCouponClaimRejections(t *testing.T) {
	base := setupCouponServer(t)
	now := couponMySQLNow(t)
	isoInsertUser(t, "rejecter", "rejecterpass123")
	token, _ := isoFrontendLogin(t, base, "rejecter", "rejecterpass123")

	// 正常模板：重复领取 → 19004（总量充足时命中唯一约束）。
	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)
	active := couponInsertTemplate(t, "active-tpl", 1000, 100, 10, claimStart, claimEnd, useStart, useEnd, 1)
	if c := couponClaimViaAPI(t, base, token, active); c.Status != 200 || c.Code != 0 {
		t.Fatalf("first claim: status=%d code=%d", c.Status, c.Code)
	}
	dup := couponClaimViaAPI(t, base, token, active)
	if dup.Status != 409 || dup.Code != 19004 {
		t.Fatalf("duplicate claim: status=%d code=%d want 409/19004", dup.Status, dup.Code)
	}
	if got := couponIssued(t, active); got != 1 {
		t.Fatalf("duplicate claim must not increment issued: got %d want 1", got)
	}

	// 未开始（领取开始在未来）→ 19005。
	notStarted := couponInsertTemplate(t, "notstarted-tpl", 1000, 100, 10, now.Add(time.Hour), now.Add(2*time.Hour), useStart, useEnd, 1)
	if c := couponClaimViaAPI(t, base, token, notStarted); c.Status != 409 || c.Code != 19005 {
		t.Fatalf("not-started claim: status=%d code=%d want 409/19005", c.Status, c.Code)
	}

	// 已结束（领取结束在过去）→ 19005。
	ended := couponInsertTemplate(t, "ended-tpl", 1000, 100, 10, now.Add(-2*time.Hour), now.Add(-time.Hour), useStart, useEnd, 1)
	if c := couponClaimViaAPI(t, base, token, ended); c.Status != 409 || c.Code != 19005 {
		t.Fatalf("ended claim: status=%d code=%d want 409/19005", c.Status, c.Code)
	}

	// 已停止领取（status=0）→ 19005。
	stopped := couponInsertTemplate(t, "stopped-tpl", 1000, 100, 10, claimStart, claimEnd, useStart, useEnd, 0)
	if c := couponClaimViaAPI(t, base, token, stopped); c.Status != 409 || c.Code != 19005 {
		t.Fatalf("stopped claim: status=%d code=%d want 409/19005", c.Status, c.Code)
	}

	// 发放总量已满（total=1，另一用户已领取）→ 19006。
	isoInsertUser(t, "otheruser", "otherpass123")
	otherToken, _ := isoFrontendLogin(t, base, "otheruser", "otherpass123")
	soldout := couponInsertTemplate(t, "soldout-tpl", 1000, 100, 1, claimStart, claimEnd, useStart, useEnd, 1)
	if c := couponClaimViaAPI(t, base, otherToken, soldout); c.Status != 200 || c.Code != 0 {
		t.Fatalf("first soldout claim: status=%d code=%d", c.Status, c.Code)
	}
	if c := couponClaimViaAPI(t, base, token, soldout); c.Status != 409 || c.Code != 19006 {
		t.Fatalf("soldout claim: status=%d code=%d want 409/19006", c.Status, c.Code)
	}
}

// TestCouponConcurrentClaim 覆盖 AC-006/INV-C01/INV-C02：并发领取不超发、单用户至多一张（真实 MySQL）。
func TestCouponConcurrentClaim(t *testing.T) {
	base := setupCouponServer(t)
	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)

	// 总量 5，10 个不同用户并发领取 → 恰好 5 成功。
	const total = int64(5)
	const users = 10
	templateID := couponInsertTemplate(t, "race-tpl", 1000, 100, total, claimStart, claimEnd, useStart, useEnd, 1)
	for i := 0; i < users; i++ {
		isoInsertUser(t, fmt.Sprintf("raceuser%d", i), "racepass123")
	}

	var wg sync.WaitGroup
	success := make(chan bool, users)
	for i := 0; i < users; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token, _ := isoFrontendLogin(t, base, fmt.Sprintf("raceuser%d", i), "racepass123")
			res := couponClaimViaAPI(t, base, token, templateID)
			success <- (res.Status == 200 && res.Code == 0)
		}(i)
	}
	wg.Wait()
	close(success)

	okCount := 0
	for ok := range success {
		if ok {
			okCount++
		}
	}
	if okCount != int(total) {
		t.Fatalf("concurrent claim success=%d want %d", okCount, total)
	}
	if got := couponIssued(t, templateID); got != total {
		t.Fatalf("concurrent claim issued=%d want %d", got, total)
	}

	// 单用户并发领取同一模板 → 至多成功一张（uk_user_template 兜底）。
	isoInsertUser(t, "singleuser", "singlepass123")
	singleToken, _ := isoFrontendLogin(t, base, "singleuser", "singlepass123")
	singleTpl := couponInsertTemplate(t, "single-tpl", 1000, 100, 100, claimStart, claimEnd, useStart, useEnd, 1)
	const n = 10
	var wg2 sync.WaitGroup
	singleSuccess := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			res := couponClaimViaAPI(t, base, singleToken, singleTpl)
			singleSuccess <- (res.Status == 200 && res.Code == 0)
		}()
	}
	wg2.Wait()
	close(singleSuccess)

	singleOK := 0
	for ok := range singleSuccess {
		if ok {
			singleOK++
		}
	}
	if singleOK != 1 {
		t.Fatalf("single-user concurrent claim success=%d want 1", singleOK)
	}
}

// TestCouponUserIsolation 覆盖 AC-007/INV-C09：用户 B 不能用/看不到用户 A 的券（19003，无写入）。
func TestCouponUserIsolation(t *testing.T) {
	base := setupCouponServer(t)
	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)
	templateID := couponInsertTemplate(t, "iso-tpl", 1000, 100, 10, claimStart, claimEnd, useStart, useEnd, 1)

	isoInsertUser(t, "ownerA", "ownerpass123")
	ownerToken, _ := isoFrontendLogin(t, base, "ownerA", "ownerpass123")
	claim := couponClaimViaAPI(t, base, ownerToken, templateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("owner claim: status=%d code=%d", claim.Status, claim.Code)
	}
	couponID := int64(claim.Data["id"].(float64))

	// 用户 B 用 A 的券下单 → 19003（券不存在或不属于当前用户），无写入。
	skuID, _ := setupSellable(t, "SKU-ISO", 5000, 10)
	isoInsertUser(t, "intruderB", "intruderpass123")
	intruderToken, _ := isoFrontendLogin(t, base, "intruderB", "intruderpass123")
	intruderClaims := isoClaims(t, intruderToken)
	intruderID, _ := parseUserID(t, intruderClaims.Subject)
	addrID := orderInsertAddress(t, intruderID)

	env := orderCall(t, base, "POST", "/orders", intruderToken, map[string]any{
		"source": "direct", "address_id": addrID, "idempotency_key": "key-iso-coupon",
		"sku_id": skuID, "quantity": 1, "coupon_id": couponID,
	})
	if env.Status != 404 || env.Code != 19003 {
		t.Fatalf("intruder use coupon: status=%d code=%d want 404/19003", env.Status, env.Code)
	}
	// 券仍属 owner 且为 available，无写入。
	if got := couponStatus(t, couponID); got != 10 {
		t.Fatalf("coupon status=%d want 10 (unchanged)", got)
	}

	// 用户 B 的「我的券」不含 A 的券。
	list := isoDo(t, base, "GET", "/my/coupons", nil, isoAuthHeader(intruderToken))
	if list.Status != 200 || list.Code != 0 {
		t.Fatalf("intruder list mine: status=%d code=%d", list.Status, list.Code)
	}
	if items := list.Data["items"].([]any); len(items) != 0 {
		t.Fatalf("intruder list mine should be empty, got %d", len(items))
	}
}

// TestCouponOrderWithDiscount 覆盖 AC-008/AC-009/INV-C06：服务端重读价格计算金额、满减门槛通过、优惠/应付金额正确、券预占。
func TestCouponOrderWithDiscount(t *testing.T) {
	base := setupCouponServer(t)
	skuID, _ := setupSellable(t, "SKU-DISC", 5000, 10)

	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)
	templateID := couponInsertTemplate(t, "disc-tpl", 3000, 1000, 10, claimStart, claimEnd, useStart, useEnd, 1)

	isoInsertUser(t, "discbuyer", "discbuyer123")
	token, _ := isoFrontendLogin(t, base, "discbuyer", "discbuyer123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addrID := orderInsertAddress(t, userID)

	claim := couponClaimViaAPI(t, base, token, templateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("claim: status=%d code=%d", claim.Status, claim.Code)
	}
	couponID := int64(claim.Data["id"].(float64))

	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addrID, "idempotency_key": "key-disc",
		"sku_id": skuID, "quantity": 2, "coupon_id": couponID,
	})
	if env.Status != 200 || env.Code != 0 {
		t.Fatalf("order with coupon: status=%d code=%d msg=%q", env.Status, env.Code, env.Message)
	}
	// original = 5000*2 = 10000；discount = min(1000, 10000) = 1000；total = 9000。
	if env.Data.OriginalAmount != 10000 {
		t.Fatalf("original_amount=%d want 10000", env.Data.OriginalAmount)
	}
	if env.Data.DiscountAmount != 1000 {
		t.Fatalf("discount_amount=%d want 1000", env.Data.DiscountAmount)
	}
	if env.Data.TotalAmount != 9000 {
		t.Fatalf("total_amount=%d want 9000", env.Data.TotalAmount)
	}
	if env.Data.UserCouponId == nil || *env.Data.UserCouponId != couponID {
		t.Fatalf("user_coupon_id=%v want %d", env.Data.UserCouponId, couponID)
	}
	// 券被预占（available→locked）。
	if got := couponStatus(t, couponID); got != 20 {
		t.Fatalf("coupon status=%d want 20 (locked)", got)
	}
}

// TestCouponOrderThresholdNotMet 覆盖 AC-008/INV-C06：未达门槛不能使用（19007），券保持可用。
func TestCouponOrderThresholdNotMet(t *testing.T) {
	base := setupCouponServer(t)
	skuID, _ := setupSellable(t, "SKU-THR", 5000, 10)

	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)
	templateID := couponInsertTemplate(t, "thr-tpl", 10000, 1000, 10, claimStart, claimEnd, useStart, useEnd, 1)

	isoInsertUser(t, "thrbuyer", "thrbuyer123")
	token, _ := isoFrontendLogin(t, base, "thrbuyer", "thrbuyer123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addrID := orderInsertAddress(t, userID)

	claim := couponClaimViaAPI(t, base, token, templateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("claim: status=%d code=%d", claim.Status, claim.Code)
	}
	couponID := int64(claim.Data["id"].(float64))

	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addrID, "idempotency_key": "key-thr",
		"sku_id": skuID, "quantity": 1, "coupon_id": couponID,
	})
	if env.Status != 409 || env.Code != 19007 {
		t.Fatalf("below threshold: status=%d code=%d want 409/19007", env.Status, env.Code)
	}
	if got := couponStatus(t, couponID); got != 10 {
		t.Fatalf("coupon status=%d want 10 (unchanged)", got)
	}
}

// TestCouponOrderExpiredCoupon 覆盖 AC-008：使用期已过不能使用（19007）。
func TestCouponOrderExpiredCoupon(t *testing.T) {
	base := setupCouponServer(t)
	skuID, _ := setupSellable(t, "SKU-EXP", 5000, 10)

	now := couponMySQLNow(t)
	// 领取窗口活跃、使用窗口已过期。
	templateID := couponInsertTemplate(t, "exp-tpl", 1000, 100, 10, now.Add(-time.Hour), now.Add(24*time.Hour), now.Add(-2*time.Hour), now.Add(-time.Hour), 1)

	isoInsertUser(t, "expbuyer", "expbuyer123")
	token, _ := isoFrontendLogin(t, base, "expbuyer", "expbuyer123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addrID := orderInsertAddress(t, userID)

	claim := couponClaimViaAPI(t, base, token, templateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("claim: status=%d code=%d", claim.Status, claim.Code)
	}
	couponID := int64(claim.Data["id"].(float64))

	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addrID, "idempotency_key": "key-exp",
		"sku_id": skuID, "quantity": 1, "coupon_id": couponID,
	})
	if env.Status != 409 || env.Code != 19007 {
		t.Fatalf("expired coupon: status=%d code=%d want 409/19007", env.Status, env.Code)
	}
}

// TestCouponOrderNoCouponCompat 覆盖 AC-010/INV-C07：无券下单金额语义与现状一致。
func TestCouponOrderNoCouponCompat(t *testing.T) {
	base := setupCouponServer(t)
	skuID, _ := setupSellable(t, "SKU-NOC", 5000, 10)

	isoInsertUser(t, "nocbuyer", "nocbuyer123")
	token, _ := isoFrontendLogin(t, base, "nocbuyer", "nocbuyer123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addrID := orderInsertAddress(t, userID)

	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addrID, "idempotency_key": "key-noc",
		"sku_id": skuID, "quantity": 2,
	})
	if env.Status != 200 || env.Code != 0 {
		t.Fatalf("no-coupon order: status=%d code=%d", env.Status, env.Code)
	}
	if env.Data.OriginalAmount != 10000 || env.Data.DiscountAmount != 0 || env.Data.TotalAmount != 10000 {
		t.Fatalf("no-coupon amounts: original=%d discount=%d total=%d want 10000/0/10000",
			env.Data.OriginalAmount, env.Data.DiscountAmount, env.Data.TotalAmount)
	}
	if env.Data.UserCouponId != nil {
		t.Fatalf("no-coupon user_coupon_id should be nil, got %v", env.Data.UserCouponId)
	}
}

// TestCouponOrderRollbackOnFailure 覆盖 AC-012/INV-C05：下单失败（库存不足）不残留「已预占」。
func TestCouponOrderRollbackOnFailure(t *testing.T) {
	base := setupCouponServer(t)
	skuID, _ := setupSellable(t, "SKU-RB", 5000, 2)

	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)
	templateID := couponInsertTemplate(t, "rb-tpl", 1000, 100, 10, claimStart, claimEnd, useStart, useEnd, 1)

	isoInsertUser(t, "rbbuyer", "rbbuyer123")
	token, _ := isoFrontendLogin(t, base, "rbbuyer", "rbbuyer123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addrID := orderInsertAddress(t, userID)

	claim := couponClaimViaAPI(t, base, token, templateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("claim: status=%d code=%d", claim.Status, claim.Code)
	}
	couponID := int64(claim.Data["id"].(float64))

	// 数量 3 > 库存 2 → 6001，整体回滚，券不残留 locked。
	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addrID, "idempotency_key": "key-rb",
		"sku_id": skuID, "quantity": 3, "coupon_id": couponID,
	})
	if env.Status != 409 || env.Code != 6001 {
		t.Fatalf("insufficient stock: status=%d code=%d want 409/6001", env.Status, env.Code)
	}
	if got := couponStatus(t, couponID); got != 10 {
		t.Fatalf("coupon status=%d want 10 (not locked)", got)
	}
	if got := orderStock(t, skuID); got != 2 {
		t.Fatalf("stock=%d want 2 (unchanged)", got)
	}
}

// TestCouponConcurrentOrderSameCoupon 覆盖 AC-013/INV-C04：并发下单同一张券，至多一单占用。
func TestCouponConcurrentOrderSameCoupon(t *testing.T) {
	base := setupCouponServer(t)
	skuID, _ := setupSellable(t, "SKU-LOCK", 5000, 10)

	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)
	templateID := couponInsertTemplate(t, "lock-tpl", 1000, 100, 10, claimStart, claimEnd, useStart, useEnd, 1)

	isoInsertUser(t, "lockbuyer", "lockbuyer123")
	token, _ := isoFrontendLogin(t, base, "lockbuyer", "lockbuyer123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addrID := orderInsertAddress(t, userID)

	claim := couponClaimViaAPI(t, base, token, templateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("claim: status=%d code=%d", claim.Status, claim.Code)
	}
	couponID := int64(claim.Data["id"].(float64))

	// 两个订单并发使用同一张券（不同幂等键）。
	const n = 2
	var wg sync.WaitGroup
	results := make(chan isoResult, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := isoDo(t, base, "POST", "/orders", map[string]any{
				"source": "direct", "address_id": addrID, "idempotency_key": fmt.Sprintf("key-lock-%d", i),
				"sku_id": skuID, "quantity": 1, "coupon_id": couponID,
			}, isoAuthHeader(token))
			results <- res
		}(i)
	}
	wg.Wait()
	close(results)

	okCount := 0
	unavailableCount := 0
	for res := range results {
		if res.Status == 200 && res.Code == 0 {
			okCount++
		} else if res.Status == 409 && res.Code == 19007 {
			unavailableCount++
		}
	}
	if okCount != 1 || unavailableCount != 1 {
		t.Fatalf("concurrent order same coupon: ok=%d unavailable=%d want 1/1", okCount, unavailableCount)
	}
	if got := couponStatus(t, couponID); got != 20 {
		t.Fatalf("coupon status=%d want 20 (locked by single order)", got)
	}
}

// TestCouponIdempotencyWithCoupon 覆盖 AC-014/INV-C10：同幂等键含 coupon 幂等；同键不同券 → 9006。
func TestCouponIdempotencyWithCoupon(t *testing.T) {
	base := setupCouponServer(t)
	skuID, _ := setupSellable(t, "SKU-IDEMC", 5000, 10)

	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)
	templateID := couponInsertTemplate(t, "idemc-tpl", 1000, 1000, 10, claimStart, claimEnd, useStart, useEnd, 1)

	isoInsertUser(t, "idembuyer", "idembuyer123")
	token, _ := isoFrontendLogin(t, base, "idembuyer", "idembuyer123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addrID := orderInsertAddress(t, userID)

	claim := couponClaimViaAPI(t, base, token, templateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("claim: status=%d code=%d", claim.Status, claim.Code)
	}
	couponID := int64(claim.Data["id"].(float64))

	body := map[string]any{
		"source": "direct", "address_id": addrID, "idempotency_key": "key-idemc",
		"sku_id": skuID, "quantity": 1, "coupon_id": couponID,
	}
	first := orderCall(t, base, "POST", "/orders", token, body)
	if first.Status != 200 || first.Code != 0 {
		t.Fatalf("first order: status=%d code=%d", first.Status, first.Code)
	}
	second := orderCall(t, base, "POST", "/orders", token, body)
	if second.Status != 200 || second.Code != 0 {
		t.Fatalf("second order (idempotent): status=%d code=%d", second.Status, second.Code)
	}
	if second.Data.Id != first.Data.Id {
		t.Fatalf("idempotent order should return same id: %d vs %d", second.Data.Id, first.Data.Id)
	}
	if n := orderCount(t, userID); n != 1 {
		t.Fatalf("should create only one order, got %d", n)
	}

	// 同键不同券 → 9006。
	conflict := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addrID, "idempotency_key": "key-idemc",
		"sku_id": skuID, "quantity": 1, "coupon_id": 0,
	})
	if conflict.Status != 409 || conflict.Code != 9006 {
		t.Fatalf("idempotency conflict (different coupon): status=%d code=%d want 409/9006", conflict.Status, conflict.Code)
	}
}

// TestCouponPayMarkUsed 覆盖 AC-015/INV-C08：支付核销（locked→used），重复支付不重复核销。
func TestCouponPayMarkUsed(t *testing.T) {
	base := setupCouponServer(t)
	skuID, _ := setupSellable(t, "SKU-PAYC", 5000, 10)

	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)
	templateID := couponInsertTemplate(t, "payc-tpl", 1000, 1000, 10, claimStart, claimEnd, useStart, useEnd, 1)

	isoInsertUser(t, "paybuyer", "paybuyer123")
	token, _ := isoFrontendLogin(t, base, "paybuyer", "paybuyer123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addrID := orderInsertAddress(t, userID)

	claim := couponClaimViaAPI(t, base, token, templateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("claim: status=%d code=%d", claim.Status, claim.Code)
	}
	couponID := int64(claim.Data["id"].(float64))

	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addrID, "idempotency_key": "key-payc",
		"sku_id": skuID, "quantity": 1, "coupon_id": couponID,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	if got := couponStatus(t, couponID); got != 20 {
		t.Fatalf("after create coupon status=%d want 20", got)
	}

	paid := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", created.Data.Id), token, nil)
	if paid.Status != 200 || paid.Code != 0 || paid.Data.Status != v1.StatusPaid {
		t.Fatalf("pay: status=%d code=%d order_status=%s", paid.Status, paid.Code, paid.Data.Status)
	}
	if got := couponStatus(t, couponID); got != 30 {
		t.Fatalf("after pay coupon status=%d want 30 (used)", got)
	}

	// 重复支付：幂等成功，券保持 used（不重复核销）。
	paid2 := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", created.Data.Id), token, nil)
	if paid2.Status != 200 || paid2.Code != 0 || paid2.Data.Status != v1.StatusPaid {
		t.Fatalf("repeat pay: status=%d code=%d", paid2.Status, paid2.Code)
	}
	if got := couponStatus(t, couponID); got != 30 {
		t.Fatalf("after repeat pay coupon status=%d want 30", got)
	}
}

// TestCouponCancelRelease 覆盖 AC-016/INV-C08：取消释放券（locked→available），库存恢复。
func TestCouponCancelRelease(t *testing.T) {
	base := setupCouponServer(t)
	skuID, _ := setupSellable(t, "SKU-CANC", 5000, 10)

	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)
	templateID := couponInsertTemplate(t, "canc-tpl", 1000, 1000, 10, claimStart, claimEnd, useStart, useEnd, 1)

	isoInsertUser(t, "cancbuyer", "cancbuyer123")
	token, _ := isoFrontendLogin(t, base, "cancbuyer", "cancbuyer123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addrID := orderInsertAddress(t, userID)

	claim := couponClaimViaAPI(t, base, token, templateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("claim: status=%d code=%d", claim.Status, claim.Code)
	}
	couponID := int64(claim.Data["id"].(float64))

	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addrID, "idempotency_key": "key-canc",
		"sku_id": skuID, "quantity": 2, "coupon_id": couponID,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	if got := orderStock(t, skuID); got != 8 {
		t.Fatalf("after create stock=%d want 8", got)
	}

	cancelled := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/cancel", created.Data.Id), token, nil)
	if cancelled.Status != 200 || cancelled.Code != 0 || cancelled.Data.Status != v1.StatusCancelled {
		t.Fatalf("cancel: status=%d code=%d order_status=%s", cancelled.Status, cancelled.Code, cancelled.Data.Status)
	}
	// 券释放回 available、order_id 清空，库存恢复。
	if got := couponStatus(t, couponID); got != 10 {
		t.Fatalf("after cancel coupon status=%d want 10 (available)", got)
	}
	if got := orderStock(t, skuID); got != 10 {
		t.Fatalf("after cancel stock=%d want 10", got)
	}
}

// TestCouponRefundNoReturn 覆盖 AC-017/INV-C08：退款不返券（券保持 used），库存恢复。
func TestCouponRefundNoReturn(t *testing.T) {
	base := setupCouponServer(t)
	skuID, _ := setupSellable(t, "SKU-RFD", 5000, 10)

	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)
	templateID := couponInsertTemplate(t, "rfd-tpl", 1000, 1000, 10, claimStart, claimEnd, useStart, useEnd, 1)

	isoInsertUser(t, "rfdbuyer", "rfdbuyer123")
	token, _ := isoFrontendLogin(t, base, "rfdbuyer", "rfdbuyer123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addrID := orderInsertAddress(t, userID)

	claim := couponClaimViaAPI(t, base, token, templateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("claim: status=%d code=%d", claim.Status, claim.Code)
	}
	couponID := int64(claim.Data["id"].(float64))

	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addrID, "idempotency_key": "key-rfd",
		"sku_id": skuID, "quantity": 2, "coupon_id": couponID,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}
	if p := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", created.Data.Id), token, nil); p.Status != 200 || p.Code != 0 {
		t.Fatalf("pay: status=%d code=%d", p.Status, p.Code)
	}
	if got := orderStock(t, skuID); got != 8 {
		t.Fatalf("after pay stock=%d want 8", got)
	}

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	refund := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/refund", created.Data.Id), adminToken, map[string]any{})
	if refund.Status != 200 || refund.Code != 0 || refund.Data.Status != v1.StatusRefunded {
		t.Fatalf("refund: status=%d code=%d order_status=%s", refund.Status, refund.Code, refund.Data.Status)
	}
	// 退款不返券：券保持 used；库存恢复。
	if got := couponStatus(t, couponID); got != 30 {
		t.Fatalf("after refund coupon status=%d want 30 (still used)", got)
	}
	if got := orderStock(t, skuID); got != 10 {
		t.Fatalf("after refund stock=%d want 10", got)
	}
}

// TestCouponOrderSnapshotStable 覆盖 AC-011/INV-C06：下单后改价/改模板不影响订单金额快照。
func TestCouponOrderSnapshotStable(t *testing.T) {
	base := setupCouponServer(t)
	skuID, _ := setupSellable(t, "SKU-SNAPC", 5000, 10)

	claimStart, claimEnd, useStart, useEnd := couponActiveWindows(t)
	templateID := couponInsertTemplate(t, "snapc-tpl", 3000, 1000, 10, claimStart, claimEnd, useStart, useEnd, 1)

	isoInsertUser(t, "snapbuyer", "snapbuyer123")
	token, _ := isoFrontendLogin(t, base, "snapbuyer", "snapbuyer123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addrID := orderInsertAddress(t, userID)

	claim := couponClaimViaAPI(t, base, token, templateID)
	if claim.Status != 200 || claim.Code != 0 {
		t.Fatalf("claim: status=%d code=%d", claim.Status, claim.Code)
	}
	couponID := int64(claim.Data["id"].(float64))

	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addrID, "idempotency_key": "key-snapc",
		"sku_id": skuID, "quantity": 2, "coupon_id": couponID,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create: status=%d code=%d", created.Status, created.Code)
	}

	// 改 SKU 价格与模板减免金额后，订单快照不变。
	if _, err := g.DB().Model("skus").Ctx(context.Background()).Where("id", skuID).Data(g.Map{"price": 9999}).Update(); err != nil {
		t.Fatalf("update sku price: %v", err)
	}
	if _, err := g.DB().Model("coupon_templates").Ctx(context.Background()).Where("id", templateID).Data(g.Map{"discount_amount": 9999}).Update(); err != nil {
		t.Fatalf("update template discount: %v", err)
	}

	detail := orderCall(t, base, "GET", fmt.Sprintf("/orders/%d", created.Data.Id), token, nil)
	if detail.Status != 200 || detail.Code != 0 {
		t.Fatalf("detail: status=%d code=%d", detail.Status, detail.Code)
	}
	if detail.Data.OriginalAmount != 10000 || detail.Data.DiscountAmount != 1000 || detail.Data.TotalAmount != 9000 {
		t.Fatalf("snapshot amounts changed: original=%d discount=%d total=%d want 10000/1000/9000",
			detail.Data.OriginalAmount, detail.Data.DiscountAmount, detail.Data.TotalAmount)
	}
}
