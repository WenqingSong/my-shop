package e2e

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定一级分销（distribution-v1 / B1-04-05）的业务不变量：
//   - 推荐关系：一人一个上级、禁止自荐、不可改绑、并发绑定只留一个有效上级；
//   - 佣金生成：订单支付事务内计提、只算一级、整数分、比例快照、重复支付不重复计提；
//   - 退款冲回：退款成功事务内冲回、只冲一次、不超额、重复/并发执行幂等；
//   - 权限隔离：用户只能访问自己的分销数据，后台接口需要 distribution:* 权限。

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	distributionv1 "cnb.cool/go-cloud-devops/my-shop/api/distribution/v1"
)

const (
	// distributionRateBp 测试用佣金比例：5%（万分比 500）。
	distributionRateBp = "500"
	// distributionBuyerPass 测试用户统一密码。
	distributionBuyerPass = "buyerpass123"
)

// distributionCommissionRow 是 distribution_commissions 的一行（测试侧直接读库断言）。
type distributionCommissionRow struct {
	Id                int64       `json:"id"`
	OrderId           int64       `json:"order_id"`
	BuyerUserId       int64       `json:"buyer_user_id"`
	BeneficiaryUserId int64       `json:"beneficiary_user_id"`
	BaseAmount        int64       `json:"base_amount"`
	RateBp            int64       `json:"rate_bp"`
	CommissionAmount  int64       `json:"commission_amount"`
	Status            int         `json:"status"`
	SettledAt         *gtime.Time `json:"settled_at"`
	ReversedAmount    int64       `json:"reversed_amount"`
	ReversedAt        *gtime.Time `json:"reversed_at"`
	RefundId          *int64      `json:"refund_id"`
	ReversalReason    string      `json:"reversal_reason"`
}

// cleanDistributionTables 清空分销两表。
// 顺序敏感：distribution_commissions 有 FK → orders(id) ON DELETE RESTRICT，
// 必须在任何删除 orders 的动作之前清空，否则残留佣金会让订单域清理失败（1451）。
func cleanDistributionTables(t *testing.T) {
	t.Helper()
	for _, table := range []string{"distribution_commissions", "user_referrals"} {
		if _, err := g.DB().Exec(context.Background(), "DELETE FROM "+table); err != nil {
			t.Errorf("clean %s: %v", table, err)
		}
	}
}

// setupDistributionServer 建立隔离的分销测试环境。
//
// 顺序说明：必须先 setupIsolationServer（执行 migration + Bootstrap，完成 gdb 配置），
// 再清空分销两表（解除 distribution_commissions → orders 的 FK 阻塞），最后清空订单域。
// 订单域的清理表清单与 setupOrderServer 保持一致；此处内联是为了在「删 orders」之前
// 插入分销表清理，避免改动其它用例共用的 setupOrderServer 语义。
func setupDistributionServer(t *testing.T) string {
	t.Helper()
	base := setupIsolationServer(t)

	cleanDistributionTables(t)
	for _, table := range []string{
		"order_items", "orders", "cart_items",
		"inventory_logs", "inventories",
		"skus", "product_images", "products",
		"addresses", "categories",
	} {
		if _, err := g.DB().Exec(context.Background(), "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}

	// 用例结束后同样先清分销两表，避免同包后续订单/退款用例受 FK RESTRICT 影响。
	t.Cleanup(func() { cleanDistributionTables(t) })
	return base
}

// distributionDecode 把统一响应的 data 解码为具体类型。
func distributionDecode(t *testing.T, res isoResult, out any) {
	t.Helper()
	if res.Data == nil {
		t.Fatalf("响应 data 为空: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("序列化响应 data: %v", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("解码响应 data: %v", err)
	}
}

// distributionBind 调用绑定推荐人接口。
func distributionBind(t *testing.T, base, token string, referrerUserID int64) isoResult {
	t.Helper()
	return isoDo(t, base, "POST", "/distribution/referral", map[string]any{
		"referrer_user_id": referrerUserID,
	}, isoAuthHeader(token))
}

// distributionMe 查询我的分销信息。
func distributionMe(t *testing.T, base, token string) (*distributionv1.MyInfoRes, isoResult) {
	t.Helper()
	res := isoDo(t, base, "GET", "/distribution/me", nil, isoAuthHeader(token))
	var out distributionv1.MyInfoRes
	if res.Status == 200 && res.Code == 0 {
		distributionDecode(t, res, &out)
	}
	return &out, res
}

// distributionLogin 直插用户并登录，返回 (token, userID)。
// 用户名只允许字母数字（IAM 规则 ^[a-zA-Z0-9]{3,24}$）。
func distributionLogin(t *testing.T, base, username string) (string, int64) {
	t.Helper()
	isoInsertUser(t, username, distributionBuyerPass)
	token, _ := isoFrontendLogin(t, base, username, distributionBuyerPass)
	userID, err := parseUserID(t, isoClaims(t, token).Subject)
	if err != nil {
		t.Fatalf("解析用户 id: %v", err)
	}
	return token, userID
}

// distributionCreateOrder 为指定用户创建直购订单（不支付），返回订单 id。
func distributionCreateOrder(t *testing.T, base, token, idempotencyKey string, userID, price, qty int64) int64 {
	t.Helper()
	skuID, _ := setupSellable(t, "SKU-"+idempotencyKey, price, 100)
	addressID := orderInsertAddress(t, userID)
	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": idempotencyKey,
		"sku_id": skuID, "quantity": qty,
	})
	if env.Status != 200 || env.Code != 0 || env.Data == nil {
		t.Fatalf("create order: status=%d code=%d msg=%q", env.Status, env.Code, env.Message)
	}
	return env.Data.Id
}

// distributionPay 支付订单并断言成功。
func distributionPay(t *testing.T, base, token string, orderID int64) {
	t.Helper()
	env := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", orderID), token, nil)
	if env.Status != 200 || env.Code != 0 {
		t.Fatalf("pay order %d: status=%d code=%d msg=%q", orderID, env.Status, env.Code, env.Message)
	}
}

// distributionCommissionsOf 查询某订单的佣金记录（应为 0 或 1 条）。
func distributionCommissionsOf(t *testing.T, orderID int64) []*distributionCommissionRow {
	t.Helper()
	var rows []*distributionCommissionRow
	if err := g.DB().Model("distribution_commissions").Ctx(context.Background()).
		Where("order_id", orderID).Scan(&rows); err != nil {
		t.Fatalf("查询佣金记录: %v", err)
	}
	return rows
}

// distributionCommissionCount 统计全表佣金记录数。
func distributionCommissionCount(t *testing.T) int {
	t.Helper()
	n, err := g.DB().Model("distribution_commissions").Ctx(context.Background()).Count()
	if err != nil {
		t.Fatalf("统计佣金记录: %v", err)
	}
	return n
}

// distributionReferralCount 统计全表推荐关系数。
func distributionReferralCount(t *testing.T) int {
	t.Helper()
	n, err := g.DB().Model("user_referrals").Ctx(context.Background()).Count()
	if err != nil {
		t.Fatalf("统计推荐关系: %v", err)
	}
	return n
}

// TestDistributionBindReferral 覆盖关系绑定的正常路径与拒绝路径：
// 正常绑定落库、同一推荐人幂等、换人 409/20003、自荐 400/20001、推荐人不存在或禁用 404/20002。
func TestDistributionBindReferral(t *testing.T) {
	base := setupDistributionServer(t)

	_, referrerID := distributionLogin(t, base, "distrefa")
	_, otherReferrerID := distributionLogin(t, base, "distrefb")
	buyerToken, buyerID := distributionLogin(t, base, "distbuyera")

	// 正常绑定：关系必须真实落库，且绑定时间可读。
	res := distributionBind(t, base, buyerToken, referrerID)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("bind: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	var bound distributionv1.BindReferralRes
	distributionDecode(t, res, &bound)
	if bound.UserId != buyerID || bound.ReferrerUserId != referrerID {
		t.Fatalf("bound=(user %d, referrer %d), want (%d, %d)",
			bound.UserId, bound.ReferrerUserId, buyerID, referrerID)
	}
	if bound.CreatedAt == nil {
		t.Fatal("绑定时间不应为空")
	}
	if n := distributionReferralCount(t); n != 1 {
		t.Fatalf("user_referrals=%d want 1", n)
	}
	v, err := g.DB().Model("user_referrals").Ctx(context.Background()).
		Where("user_id", buyerID).Value("referrer_user_id")
	if err != nil {
		t.Fatalf("查询推荐关系: %v", err)
	}
	if v.Int64() != referrerID {
		t.Fatalf("db referrer=%d want %d", v.Int64(), referrerID)
	}

	// 重复绑定同一推荐人：幂等成功，不产生第二条关系。
	res = distributionBind(t, base, buyerToken, referrerID)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("repeat bind same referrer: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	if n := distributionReferralCount(t); n != 1 {
		t.Fatalf("repeat bind must not create new row, got %d", n)
	}

	// 换绑到另一个推荐人：拒绝（409/20003），既有关系不变。
	res = distributionBind(t, base, buyerToken, otherReferrerID)
	if res.Status != 409 || res.Code != 20003 {
		t.Fatalf("rebind: status=%d code=%d want 409/20003", res.Status, res.Code)
	}
	v, err = g.DB().Model("user_referrals").Ctx(context.Background()).
		Where("user_id", buyerID).Value("referrer_user_id")
	if err != nil {
		t.Fatalf("查询推荐关系: %v", err)
	}
	if v.Int64() != referrerID {
		t.Fatalf("rebind must not change referrer, got %d", v.Int64())
	}

	// 自己邀请自己：400/20001。
	res = distributionBind(t, base, buyerToken, buyerID)
	if res.Status != 400 || res.Code != 20001 {
		t.Fatalf("self referral: status=%d code=%d want 400/20001", res.Status, res.Code)
	}

	// 推荐人不存在：404/20002。
	res = distributionBind(t, base, buyerToken, 99999999)
	if res.Status != 404 || res.Code != 20002 {
		t.Fatalf("unknown referrer: status=%d code=%d want 404/20002", res.Status, res.Code)
	}

	// 推荐人被禁用：404/20002（推荐人必须 status=1）。
	_, disabledID := distributionLogin(t, base, "distrefdis")
	if _, err := g.DB().Model("users").Ctx(context.Background()).
		Where("id", disabledID).Data(g.Map{"status": 0}).Update(); err != nil {
		t.Fatalf("禁用推荐人: %v", err)
	}
	res = distributionBind(t, base, buyerToken, disabledID)
	if res.Status != 404 || res.Code != 20002 {
		t.Fatalf("disabled referrer: status=%d code=%d want 404/20002", res.Status, res.Code)
	}

	// 未登录：401。
	res = isoDo(t, base, "POST", "/distribution/referral", map[string]any{"referrer_user_id": referrerID}, nil)
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("unauthenticated bind: status=%d code=%d want 401/1002", res.Status, res.Code)
	}
}

// TestDistributionBindReferralConcurrent 覆盖并发绑定：
// 同一被推荐人并发向多个推荐人提交绑定，最终只能有一条有效关系（uk_user_id 兜底）。
func TestDistributionBindReferralConcurrent(t *testing.T) {
	base := setupDistributionServer(t)

	const n = 8
	referrerIDs := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		_, id := distributionLogin(t, base, fmt.Sprintf("distrefc%d", i))
		referrerIDs = append(referrerIDs, id)
	}
	buyerToken, buyerID := distributionLogin(t, base, "distbuyerc")

	var wg sync.WaitGroup
	okIDs := make(chan int64, n)
	conflicts := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(referrerID int64) {
			defer wg.Done()
			res := distributionBind(t, base, buyerToken, referrerID)
			switch {
			case res.Status == 200 && res.Code == 0:
				okIDs <- referrerID
			case res.Status == 409 && res.Code == 20003:
				conflicts <- true
			default:
				conflicts <- false
			}
		}(referrerIDs[i])
	}
	wg.Wait()
	close(okIDs)
	close(conflicts)

	var winners []int64
	for id := range okIDs {
		winners = append(winners, id)
	}
	if len(winners) != 1 {
		t.Fatalf("并发绑定应只有一个成功请求，实际 %d（%v）", len(winners), winners)
	}
	for ok := range conflicts {
		if !ok {
			t.Fatal("非成功请求必须是 409/20003（不可改绑），不允许其他失败语义")
		}
	}

	if n := distributionReferralCount(t); n != 1 {
		t.Fatalf("并发绑定后 user_referrals=%d want 1", n)
	}
	v, err := g.DB().Model("user_referrals").Ctx(context.Background()).
		Where("user_id", buyerID).Value("referrer_user_id")
	if err != nil {
		t.Fatalf("查询推荐关系: %v", err)
	}
	if v.Int64() != winners[0] {
		t.Fatalf("落库推荐人=%d，与成功响应 %d 不一致", v.Int64(), winners[0])
	}
}

// TestDistributionCommissionAccrual 覆盖计佣主路径：
// 有推荐人的买家支付成功 → 生成一条一级佣金，金额 = floor(实付 × rate_bp / 10000)，快照齐全。
func TestDistributionCommissionAccrual(t *testing.T) {
	t.Setenv("DISTRIBUTION_COMMISSION_RATE_BP", distributionRateBp)
	base := setupDistributionServer(t)

	_, referrerID := distributionLogin(t, base, "distrefd")
	buyerToken, buyerID := distributionLogin(t, base, "distbuyerd")
	if res := distributionBind(t, base, buyerToken, referrerID); res.Status != 200 || res.Code != 0 {
		t.Fatalf("bind: status=%d code=%d", res.Status, res.Code)
	}

	orderID := distributionCreateOrder(t, base, buyerToken, "distd1", buyerID, 5000, 2)
	distributionPay(t, base, buyerToken, orderID)

	rows := distributionCommissionsOf(t, orderID)
	if len(rows) != 1 {
		t.Fatalf("order %d commissions=%d want 1", orderID, len(rows))
	}
	c := rows[0]
	if c.BuyerUserId != buyerID || c.BeneficiaryUserId != referrerID {
		t.Fatalf("buyer/beneficiary=(%d,%d) want (%d,%d)",
			c.BuyerUserId, c.BeneficiaryUserId, buyerID, referrerID)
	}
	// 基数=实付金额 10000 分，比例 500bp → 佣金 500 分（整数运算，向下取整）。
	if c.BaseAmount != 10000 || c.RateBp != 500 || c.CommissionAmount != 500 {
		t.Fatalf("commission snapshot=(base %d, rate %d, amount %d) want (10000, 500, 500)",
			c.BaseAmount, c.RateBp, c.CommissionAmount)
	}
	if c.Status != 10 {
		t.Fatalf("status=%d want 10（待结算）", c.Status)
	}
	if c.ReversedAmount != 0 || c.RefundId != nil || c.ReversalReason != "" {
		t.Fatalf("新佣金不应带冲回字段: reversed=%d refund=%v reason=%q",
			c.ReversedAmount, c.RefundId, c.ReversalReason)
	}

	// 重复支付：订单已是 20，走幂等快路径，不产生第二笔佣金。
	distributionPay(t, base, buyerToken, orderID)
	if n := len(distributionCommissionsOf(t, orderID)); n != 1 {
		t.Fatalf("重复支付后佣金数=%d want 1", n)
	}
	if n := distributionCommissionCount(t); n != 1 {
		t.Fatalf("全表佣金数=%d want 1", n)
	}
}

// TestDistributionCommissionPaidConcurrent 覆盖并发支付：
// 同一订单多个并发支付请求，订单只推进一次，佣金只生成一条（条件更新 + uk_order_id）。
func TestDistributionCommissionPaidConcurrent(t *testing.T) {
	t.Setenv("DISTRIBUTION_COMMISSION_RATE_BP", distributionRateBp)
	base := setupDistributionServer(t)

	_, referrerID := distributionLogin(t, base, "distrefe")
	buyerToken, buyerID := distributionLogin(t, base, "distbuyere")
	if res := distributionBind(t, base, buyerToken, referrerID); res.Status != 200 || res.Code != 0 {
		t.Fatalf("bind: status=%d code=%d", res.Status, res.Code)
	}
	orderID := distributionCreateOrder(t, base, buyerToken, "diste1", buyerID, 2000, 1)

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", orderID), buyerToken, nil)
			// 幂等语义：并发支付要么成功推进，要么因条件更新失败返回 9002；不允许其它错误。
			if !(env.Status == 200 && env.Code == 0) && !(env.Status == 409 && env.Code == 9002) {
				t.Errorf("并发支付返回 status=%d code=%d，期望 200/0 或 409/9002", env.Status, env.Code)
			}
		}()
	}
	wg.Wait()

	if st := orderStatus(t, orderID); st != 20 {
		t.Fatalf("order status=%d want 20", st)
	}
	rows := distributionCommissionsOf(t, orderID)
	if len(rows) != 1 {
		t.Fatalf("并发支付后佣金数=%d want 1", len(rows))
	}
	if rows[0].CommissionAmount != 100 {
		t.Fatalf("commission=%d want 100（2000×500/10000）", rows[0].CommissionAmount)
	}
}

// TestDistributionCommissionNoReferral 覆盖「无分销关系的订单不产生佣金」。
func TestDistributionCommissionNoReferral(t *testing.T) {
	t.Setenv("DISTRIBUTION_COMMISSION_RATE_BP", distributionRateBp)
	base := setupDistributionServer(t)

	buyerToken, buyerID := distributionLogin(t, base, "distbuyerf")
	orderID := distributionCreateOrder(t, base, buyerToken, "distf1", buyerID, 3000, 1)
	distributionPay(t, base, buyerToken, orderID)

	if rows := distributionCommissionsOf(t, orderID); len(rows) != 0 {
		t.Fatalf("无推荐人不应产生佣金，got %d", len(rows))
	}
	if n := distributionCommissionCount(t); n != 0 {
		t.Fatalf("全表佣金数=%d want 0", n)
	}
}

// TestDistributionCommissionRateZero 覆盖「未配置比例（默认 0）不产生佣金」：
// 默认配置下不得凭空生成资金事实，避免硬编码比例。
func TestDistributionCommissionRateZero(t *testing.T) {
	t.Setenv("DISTRIBUTION_COMMISSION_RATE_BP", "0")
	base := setupDistributionServer(t)

	_, referrerID := distributionLogin(t, base, "distrefg")
	buyerToken, buyerID := distributionLogin(t, base, "distbuyerg")
	if res := distributionBind(t, base, buyerToken, referrerID); res.Status != 200 || res.Code != 0 {
		t.Fatalf("bind: status=%d code=%d", res.Status, res.Code)
	}

	orderID := distributionCreateOrder(t, base, buyerToken, "distg1", buyerID, 3000, 1)
	distributionPay(t, base, buyerToken, orderID)

	if rows := distributionCommissionsOf(t, orderID); len(rows) != 0 {
		t.Fatalf("比例 0 不应产生佣金，got %d", len(rows))
	}
}

// TestDistributionCommissionUnpaidOrder 覆盖「非成功订单不产生佣金」：
// 下单未支付、以及支付前取消的订单都不应留下佣金记录。
func TestDistributionCommissionUnpaidOrder(t *testing.T) {
	t.Setenv("DISTRIBUTION_COMMISSION_RATE_BP", distributionRateBp)
	base := setupDistributionServer(t)

	_, referrerID := distributionLogin(t, base, "distrefh")
	buyerToken, buyerID := distributionLogin(t, base, "distbuyerh")
	if res := distributionBind(t, base, buyerToken, referrerID); res.Status != 200 || res.Code != 0 {
		t.Fatalf("bind: status=%d code=%d", res.Status, res.Code)
	}

	// 待支付订单：不产生佣金。
	pendingID := distributionCreateOrder(t, base, buyerToken, "disth1", buyerID, 4000, 1)
	if rows := distributionCommissionsOf(t, pendingID); len(rows) != 0 {
		t.Fatalf("待支付订单不应产生佣金，got %d", len(rows))
	}

	// 取消订单：仍不产生佣金。
	if env := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/cancel", pendingID), buyerToken, nil); env.Status != 200 || env.Code != 0 {
		t.Fatalf("cancel order: status=%d code=%d", env.Status, env.Code)
	}
	if rows := distributionCommissionsOf(t, pendingID); len(rows) != 0 {
		t.Fatalf("取消订单不应产生佣金，got %d", len(rows))
	}
	if n := distributionCommissionCount(t); n != 0 {
		t.Fatalf("全表佣金数=%d want 0", n)
	}
}

// TestDistributionReversalOnRefund 覆盖退款冲回主路径：
// 退款成功 → 同事务冲回佣金（status=30、reversed_amount=commission_amount、refund_id/reason 可审计），
// 重复执行退款不重复冲回、金额不累加。
func TestDistributionReversalOnRefund(t *testing.T) {
	t.Setenv("DISTRIBUTION_COMMISSION_RATE_BP", distributionRateBp)
	base := setupDistributionServer(t)

	_, referrerID := distributionLogin(t, base, "distrefi")
	buyerToken, buyerID := distributionLogin(t, base, "distbuyeri")
	if res := distributionBind(t, base, buyerToken, referrerID); res.Status != 200 || res.Code != 0 {
		t.Fatalf("bind: status=%d code=%d", res.Status, res.Code)
	}
	orderID := distributionCreateOrder(t, base, buyerToken, "disti1", buyerID, 8000, 1)
	distributionPay(t, base, buyerToken, orderID)

	rows := distributionCommissionsOf(t, orderID)
	if len(rows) != 1 || rows[0].CommissionAmount != 400 {
		t.Fatalf("前置佣金异常: %+v", rows)
	}

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	refundID := applyRefund(t, base, buyerToken, orderID, "不想要了")
	if env := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/review", refundID), adminToken,
		map[string]any{"result": "approve", "reason": "同意"}); env.Status != 200 || env.Code != 0 {
		t.Fatalf("review refund: status=%d code=%d", env.Status, env.Code)
	}
	if env := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/execute", refundID), adminToken,
		map[string]any{"result": "success", "reason": "Mock 退款成功"}); env.Status != 200 || env.Code != 0 {
		t.Fatalf("execute refund: status=%d code=%d", env.Status, env.Code)
	}

	rows = distributionCommissionsOf(t, orderID)
	if len(rows) != 1 {
		t.Fatalf("退款后佣金记录数=%d want 1", len(rows))
	}
	c := rows[0]
	if c.Status != 30 {
		t.Fatalf("退款后佣金 status=%d want 30（已冲回）", c.Status)
	}
	if c.ReversedAmount != c.CommissionAmount || c.ReversedAmount != 400 {
		t.Fatalf("reversed_amount=%d want %d（等于佣金金额，不超额）", c.ReversedAmount, c.CommissionAmount)
	}
	if c.RefundId == nil || *c.RefundId != refundID {
		t.Fatalf("refund_id=%v want %d", c.RefundId, refundID)
	}
	if c.ReversalReason != "refund" {
		t.Fatalf("reversal_reason=%q want \"refund\"", c.ReversalReason)
	}
	if c.ReversedAt == nil {
		t.Fatal("reversed_at 不应为空")
	}

	// 重复执行退款（幂等快路径）：佣金不得二次冲回、金额不得累加。
	if env := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/execute", refundID), adminToken,
		map[string]any{"result": "success", "reason": "重复确认"}); env.Status != 200 || env.Code != 0 {
		t.Fatalf("重复执行退款应幂等成功: status=%d code=%d", env.Status, env.Code)
	}
	rows = distributionCommissionsOf(t, orderID)
	if len(rows) != 1 {
		t.Fatalf("重复退款后佣金记录数=%d want 1", len(rows))
	}
	if rows[0].ReversedAmount != 400 || rows[0].Status != 30 {
		t.Fatalf("重复退款后 reversed=%d status=%d want 400/30", rows[0].ReversedAmount, rows[0].Status)
	}
}

// TestDistributionReversalConcurrent 覆盖并发退款执行：
// 多个并发「执行退款成功」只允许一次真正推进，佣金只冲回一次且不超过原始金额。
func TestDistributionReversalConcurrent(t *testing.T) {
	t.Setenv("DISTRIBUTION_COMMISSION_RATE_BP", distributionRateBp)
	base := setupDistributionServer(t)

	_, referrerID := distributionLogin(t, base, "distrefj")
	buyerToken, buyerID := distributionLogin(t, base, "distbuyerj")
	if res := distributionBind(t, base, buyerToken, referrerID); res.Status != 200 || res.Code != 0 {
		t.Fatalf("bind: status=%d code=%d", res.Status, res.Code)
	}
	orderID := distributionCreateOrder(t, base, buyerToken, "distj1", buyerID, 6000, 1)
	distributionPay(t, base, buyerToken, orderID)

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	refundID := applyRefund(t, base, buyerToken, orderID, "并发退款")
	if env := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/review", refundID), adminToken,
		map[string]any{"result": "approve", "reason": "同意"}); env.Status != 200 || env.Code != 0 {
		t.Fatalf("review refund: status=%d code=%d", env.Status, env.Code)
	}

	const n = 8
	var wg sync.WaitGroup
	success := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			env := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/execute", refundID), adminToken,
				map[string]any{"result": "success", "reason": fmt.Sprintf("并发执行%d", i)})
			success <- (env.Status == 200 && env.Code == 0)
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
	if okCount == 0 {
		t.Fatal("并发执行退款至少应有一次成功")
	}

	rows := distributionCommissionsOf(t, orderID)
	if len(rows) != 1 {
		t.Fatalf("并发退款后佣金记录数=%d want 1", len(rows))
	}
	c := rows[0]
	if c.Status != 30 {
		t.Fatalf("并发退款后 status=%d want 30", c.Status)
	}
	if c.ReversedAmount != c.CommissionAmount || c.ReversedAmount != 300 {
		t.Fatalf("并发冲回金额=%d want %d（佣金 300，不超额）", c.ReversedAmount, c.CommissionAmount)
	}
	refundedCount, err := g.DB().Model("refund_requests").Ctx(context.Background()).
		Where("order_id", orderID).Where("status", 60).Count()
	if err != nil {
		t.Fatalf("查询退款成功记录: %v", err)
	}
	if refundedCount != 1 {
		t.Fatalf("退款成功记录数=%d want 1", refundedCount)
	}
}

// TestDistributionUserIsolation 覆盖用户端越权边界：
// 身份只来自 Principal，客户端传入 user_id 不改变数据归属；未登录返回 401。
func TestDistributionUserIsolation(t *testing.T) {
	t.Setenv("DISTRIBUTION_COMMISSION_RATE_BP", distributionRateBp)
	base := setupDistributionServer(t)

	referrerToken, referrerID := distributionLogin(t, base, "distrefk")
	buyerToken, buyerID := distributionLogin(t, base, "distbuyerk")
	if res := distributionBind(t, base, buyerToken, referrerID); res.Status != 200 || res.Code != 0 {
		t.Fatalf("bind: status=%d code=%d", res.Status, res.Code)
	}
	orderID := distributionCreateOrder(t, base, buyerToken, "distk1", buyerID, 2000, 1)
	distributionPay(t, base, buyerToken, orderID)

	// 旁观者：未绑定任何关系、也没有任何佣金。
	outsiderToken, outsiderID := distributionLogin(t, base, "distoutsiderk")

	me, res := distributionMe(t, base, outsiderToken)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("outsider me: status=%d code=%d", res.Status, res.Code)
	}
	if me.UserId != outsiderID {
		t.Fatalf("me.user_id=%d want %d（身份必须来自 Principal）", me.UserId, outsiderID)
	}
	if me.Referrer != nil {
		t.Fatalf("未绑定用户 referrer 应为 null，got %+v", me.Referrer)
	}
	if me.SubordinateCount != 0 {
		t.Fatalf("subordinate_count=%d want 0", me.SubordinateCount)
	}
	if me.Commission.PendingCount != 0 || me.Commission.SettledCount != 0 || me.Commission.ReversedCount != 0 {
		t.Fatalf("旁观者不应看到任何佣金汇总: %+v", me.Commission)
	}

	// 伪造 user_id 查询参数：不得越权看到推荐人的佣金。
	res = isoDo(t, base, "GET", fmt.Sprintf("/distribution/commissions?user_id=%d", referrerID), nil, isoAuthHeader(outsiderToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("outsider commissions: status=%d code=%d", res.Status, res.Code)
	}
	var outsiderList distributionv1.ListCommissionsRes
	distributionDecode(t, res, &outsiderList)
	if len(outsiderList.Items) != 0 || outsiderList.Total != 0 {
		t.Fatalf("越权查询泄露了 %d 条佣金，total=%d", len(outsiderList.Items), outsiderList.Total)
	}

	// 推荐人本人：能看到自己的佣金，受益人恒为自己。
	res = isoDo(t, base, "GET", "/distribution/commissions", nil, isoAuthHeader(referrerToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("referrer commissions: status=%d code=%d", res.Status, res.Code)
	}
	var referrerList distributionv1.ListCommissionsRes
	distributionDecode(t, res, &referrerList)
	if referrerList.Total != 1 || len(referrerList.Items) != 1 {
		t.Fatalf("推荐人佣金条数=%d total=%d want 1", len(referrerList.Items), referrerList.Total)
	}
	if referrerList.Items[0].BeneficiaryUserId != referrerID {
		t.Fatalf("beneficiary=%d want %d", referrerList.Items[0].BeneficiaryUserId, referrerID)
	}
	if referrerList.Items[0].Status != distributionv1.CommissionStatusPending {
		t.Fatalf("status=%q want pending", referrerList.Items[0].Status)
	}

	// 未登录访问用户端接口：401。
	for _, path := range []string{"/distribution/me", "/distribution/subordinates", "/distribution/commissions"} {
		res = isoDo(t, base, "GET", path, nil, nil)
		if res.Status != 401 || res.Code != 1002 {
			t.Fatalf("%s 未登录: status=%d code=%d want 401/1002", path, res.Status, res.Code)
		}
	}
}

// TestDistributionAdminAccess 覆盖后台权限边界：
// 无权限 403 且零数据；授权后能查询关系/分销用户/佣金/统计；普通用户 token 访问后台 403。
func TestDistributionAdminAccess(t *testing.T) {
	t.Setenv("DISTRIBUTION_COMMISSION_RATE_BP", distributionRateBp)
	base := setupDistributionServer(t)

	_, referrerID := distributionLogin(t, base, "distrefl")
	buyerToken, buyerID := distributionLogin(t, base, "distbuyerl")
	if res := distributionBind(t, base, buyerToken, referrerID); res.Status != 200 || res.Code != 0 {
		t.Fatalf("bind: status=%d code=%d", res.Status, res.Code)
	}
	orderID := distributionCreateOrder(t, base, buyerToken, "distl1", buyerID, 4000, 1)
	distributionPay(t, base, buyerToken, orderID)

	// 普通管理员（未授权）：403。
	adminID := isoInsertAdmin(t, "distadminl", "adminpass123")
	adminToken, _ := isoAdminLogin(t, base, "distadminl", "adminpass123")

	for _, path := range []string{
		"/admin/distributions/referrals",
		"/admin/distributions/distributors",
		"/admin/distributions/commissions",
		"/admin/distributions/commissions/stats",
	} {
		res := isoDo(t, base, "GET", path, nil, isoAuthHeader(adminToken))
		if res.Status != 403 || res.Code != 1003 {
			t.Fatalf("%s 无权限: status=%d code=%d want 403/1003", path, res.Status, res.Code)
		}
	}

	// 只授予关系权限：关系/分销用户 200，佣金/统计仍 403。
	grantUserPermission(t, adminID, "distribution:referral_list")

	res := isoDo(t, base, "GET", "/admin/distributions/referrals", nil, isoAuthHeader(adminToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("referrals: status=%d code=%d", res.Status, res.Code)
	}
	var referrals distributionv1.AdminListReferralsRes
	distributionDecode(t, res, &referrals)
	if referrals.Total != 1 || len(referrals.Items) != 1 {
		t.Fatalf("referrals total=%d items=%d want 1", referrals.Total, len(referrals.Items))
	}
	if referrals.Items[0].UserId != buyerID || referrals.Items[0].ReferrerUserId != referrerID {
		t.Fatalf("referral=(%d,%d) want (%d,%d)",
			referrals.Items[0].UserId, referrals.Items[0].ReferrerUserId, buyerID, referrerID)
	}

	res = isoDo(t, base, "GET", "/admin/distributions/distributors", nil, isoAuthHeader(adminToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("distributors: status=%d code=%d", res.Status, res.Code)
	}
	var distributors distributionv1.AdminListDistributorsRes
	distributionDecode(t, res, &distributors)
	if distributors.Total != 1 || len(distributors.Items) != 1 {
		t.Fatalf("distributors total=%d items=%d want 1", distributors.Total, len(distributors.Items))
	}
	d := distributors.Items[0]
	if d.ReferrerUserId != referrerID || d.SubordinateCount != 1 {
		t.Fatalf("distributor=(%d, sub %d) want (%d, 1)", d.ReferrerUserId, d.SubordinateCount, referrerID)
	}
	if d.CommissionCount != 1 || d.CommissionAmount != 200 {
		t.Fatalf("distributor 佣金=(count %d, amount %d) want (1, 200)", d.CommissionCount, d.CommissionAmount)
	}

	res = isoDo(t, base, "GET", "/admin/distributions/commissions", nil, isoAuthHeader(adminToken))
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("未授权佣金查询: status=%d code=%d want 403/1003", res.Status, res.Code)
	}

	// 再授予佣金权限：佣金/统计 200，且统计与记录一致。
	grantUserPermission(t, adminID, "distribution:commission_list")

	res = isoDo(t, base, "GET", "/admin/distributions/commissions", nil, isoAuthHeader(adminToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("commissions: status=%d code=%d", res.Status, res.Code)
	}
	var commissions distributionv1.AdminListCommissionsRes
	distributionDecode(t, res, &commissions)
	if commissions.Total != 1 || len(commissions.Items) != 1 {
		t.Fatalf("commissions total=%d items=%d want 1", commissions.Total, len(commissions.Items))
	}
	if commissions.Items[0].OrderId != orderID || commissions.Items[0].CommissionAmount != 200 {
		t.Fatalf("commission=(order %d, amount %d) want (%d, 200)",
			commissions.Items[0].OrderId, commissions.Items[0].CommissionAmount, orderID)
	}

	res = isoDo(t, base, "GET", "/admin/distributions/commissions/stats", nil, isoAuthHeader(adminToken))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("stats: status=%d code=%d", res.Status, res.Code)
	}
	var stats distributionv1.AdminCommissionStatsRes
	distributionDecode(t, res, &stats)
	if len(stats.Items) != 3 {
		t.Fatalf("stats items=%d want 3（pending/settled/reversed 固定三项）", len(stats.Items))
	}
	if stats.Items[0].Status != distributionv1.CommissionStatusPending || stats.Items[0].Count != 1 || stats.Items[0].Amount != 200 {
		t.Fatalf("pending 统计=%+v want count 1 amount 200", stats.Items[0])
	}
	if stats.Items[1].Count != 0 || stats.Items[2].Count != 0 {
		t.Fatalf("settled/reversed 统计应为 0: %+v / %+v", stats.Items[1], stats.Items[2])
	}
	if stats.TotalCount != 1 || stats.TotalAmount != 200 {
		t.Fatalf("stats total=(%d, %d) want (1, 200)", stats.TotalCount, stats.TotalAmount)
	}

	// 普通用户 token 访问后台接口：403。
	res = isoDo(t, base, "GET", "/admin/distributions/referrals", nil, isoAuthHeader(buyerToken))
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("user token on admin route: status=%d code=%d want 403/1003", res.Status, res.Code)
	}
}
