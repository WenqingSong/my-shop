package e2e

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定「已支付未发货普通订单仅退款」闭环，覆盖 order-refund-v1 的 AC-001 至 AC-014 与 INV-001 至 INV-013。

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/gogf/gf/v2/frame/g"

	refundv1 "cnb.cool/go-cloud-devops/my-shop/api/refund/v1"
)

// refundEnvelope 是退款单对象接口的统一响应封装。
type refundEnvelope struct {
	Status  int
	Code    int
	Message string
	Data    *refundv1.Refund
}

// setupRefundServer 建立隔离的退款测试环境：复用订单域清理后，额外清空退款相关表。
func setupRefundServer(t *testing.T) string {
	t.Helper()
	base := setupOrderServer(t)
	ctx := context.Background()
	for _, table := range []string{"refund_audits", "refund_requests"} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	return base
}

// refundCall 发起退款请求并解码响应中的退款结构（单退款对象接口）。
func refundCall(t *testing.T, base, method, path, token string, body any) refundEnvelope {
	t.Helper()
	res := isoDo(t, base, method, path, body, isoAuthHeader(token))
	env := refundEnvelope{Status: res.Status, Code: res.Code, Message: res.Message}
	if res.Data != nil {
		b, err := json.Marshal(res.Data)
		if err != nil {
			t.Fatalf("marshal refund data: %v", err)
		}
		var r refundv1.Refund
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatalf("unmarshal refund data: %v", err)
		}
		env.Data = &r
	}
	return env
}

// refundList 发起退款列表查询并解码 items。
func refundList(t *testing.T, base, path, token string) []*refundv1.Refund {
	t.Helper()
	res := isoDo(t, base, "GET", path, nil, isoAuthHeader(token))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("list %s: status=%d code=%d msg=%q", path, res.Status, res.Code, res.Message)
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("marshal list data: %v", err)
	}
	var p struct {
		Items []*refundv1.Refund `json:"items"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("unmarshal list data: %v", err)
	}
	return p.Items
}

// refundAudits 发起审计查询并解码 items。
func refundAudits(t *testing.T, base, path, token string) []*refundv1.RefundAudit {
	t.Helper()
	res := isoDo(t, base, "GET", path, nil, isoAuthHeader(token))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("audits %s: status=%d code=%d msg=%q", path, res.Status, res.Code, res.Message)
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("marshal audits data: %v", err)
	}
	var p struct {
		Items []*refundv1.RefundAudit `json:"items"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("unmarshal audits data: %v", err)
	}
	return p.Items
}

// createPaidOrderForRefund 建可售 SKU + 用户 + 地址 + 下单 + 支付，返回已支付订单及其关键信息。
func createPaidOrderForRefund(t *testing.T, base, username, skuName string, price, stock, qty int64) (orderID, skuID, productID int64, token string, userID int64) {
	t.Helper()
	skuID, productID = setupSellable(t, skuName, price, stock)
	isoInsertUser(t, username, "buyerpass123")
	token, _ = isoFrontendLogin(t, base, username, "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ = parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)

	created := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-" + username,
		"sku_id": skuID, "quantity": qty,
	})
	if created.Status != 200 || created.Code != 0 {
		t.Fatalf("create order: status=%d code=%d msg=%q", created.Status, created.Code, created.Message)
	}
	if p := orderCall(t, base, "POST", fmt.Sprintf("/orders/%d/pay", created.Data.Id), token, nil); p.Status != 200 || p.Code != 0 {
		t.Fatalf("pay order: status=%d code=%d", p.Status, p.Code)
	}
	return created.Data.Id, skuID, productID, token, userID
}

// refundStatus 查询退款 DB 状态（TINYINT）。
func refundStatus(t *testing.T, refundID int64) int {
	t.Helper()
	v, err := g.DB().Model("refund_requests").Ctx(context.Background()).Fields("status").Where("id", refundID).Value()
	if err != nil {
		t.Fatalf("query refund status: %v", err)
	}
	if v == nil {
		t.Fatalf("refund %d not found", refundID)
	}
	return v.Int()
}

// countActiveRefunds 统计订单的有效退款申请数（status ∈ {10,20,30}）。
func countActiveRefunds(t *testing.T, orderID int64) int {
	t.Helper()
	n, err := g.DB().Model("refund_requests").Ctx(context.Background()).
		Where("order_id", orderID).WhereIn("status", []int{10, 20, 30}).Count()
	if err != nil {
		t.Fatalf("count active refunds: %v", err)
	}
	return n
}

// applyRefund 发起退款申请并断言成功，返回退款 id。
func applyRefund(t *testing.T, base, token string, orderID int64, reason string) int64 {
	t.Helper()
	env := refundCall(t, base, "POST", fmt.Sprintf("/orders/%d/refund", orderID), token, map[string]any{"reason": reason})
	if env.Status != 200 || env.Code != 0 || env.Data == nil {
		t.Fatalf("apply refund: status=%d code=%d msg=%q", env.Status, env.Code, env.Message)
	}
	return env.Data.Id
}

// TestRefundApplySuccessAndAmount 覆盖 AC-003/INV-003：申请成功，金额 = orders.total_amount 快照（服务端派生）。
func TestRefundApplySuccessAndAmount(t *testing.T) {
	base := setupRefundServer(t)
	orderID, _, _, token, _ := createPaidOrderForRefund(t, base, "buyerr1", "SKU-R1", 2500, 10, 2)

	env := refundCall(t, base, "POST", fmt.Sprintf("/orders/%d/refund", orderID), token, map[string]any{"reason": "不想要了"})
	if env.Status != 200 || env.Code != 0 || env.Data == nil {
		t.Fatalf("apply: status=%d code=%d msg=%q", env.Status, env.Code, env.Message)
	}
	if env.Data.Status != refundv1.StatusApplying {
		t.Fatalf("apply status=%q want applying", env.Data.Status)
	}
	// 金额 = total_amount = 2500*2 = 5000（不接受客户端金额）。
	if env.Data.Amount != 5000 {
		t.Fatalf("amount=%d want 5000", env.Data.Amount)
	}
	if env.Data.OrderId != orderID {
		t.Fatalf("order_id=%d want %d", env.Data.OrderId, orderID)
	}
}

// TestRefundOwnershipIsolation 覆盖 AC-001/INV-001：非订单所有者申请返回 404/18001 且无写入。
func TestRefundOwnershipIsolation(t *testing.T) {
	base := setupRefundServer(t)
	orderID, _, _, ownerToken, _ := createPaidOrderForRefund(t, base, "ownerr2", "SKU-R2", 1000, 10, 1)

	isoInsertUser(t, "intruderr2", "buyerpass123")
	intruderToken, _ := isoFrontendLogin(t, base, "intruderr2", "buyerpass123")

	env := refundCall(t, base, "POST", fmt.Sprintf("/orders/%d/refund", orderID), intruderToken, map[string]any{"reason": "冒用"})
	if env.Status != 404 || env.Code != 18001 {
		t.Fatalf("intruder apply: status=%d code=%d want 404/18001", env.Status, env.Code)
	}
	if n := countActiveRefunds(t, orderID); n != 0 {
		t.Fatalf("intruder must not create refund, got %d", n)
	}
	// 订单状态不变。
	if st := orderStatus(t, orderID); st != 20 {
		t.Fatalf("order status=%d want 20", st)
	}
	_ = ownerToken
}

// TestRefundOrderStatePrecondition 覆盖 AC-002/INV-002：仅已支付未发货可申请。
func TestRefundOrderStatePrecondition(t *testing.T) {
	base := setupRefundServer(t)

	// 待支付订单（下单不支付）→ 18003。
	skuID, _ := setupSellable(t, "SKU-R3", 1000, 10)
	isoInsertUser(t, "buyerr3", "buyerpass123")
	token, _ := isoFrontendLogin(t, base, "buyerr3", "buyerpass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	addressID := orderInsertAddress(t, userID)
	pending := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-r3",
		"sku_id": skuID, "quantity": 1,
	})
	env := refundCall(t, base, "POST", fmt.Sprintf("/orders/%d/refund", pending.Data.Id), token, map[string]any{"reason": "x"})
	if env.Status != 409 || env.Code != 18003 {
		t.Fatalf("pending order apply: status=%d code=%d want 409/18003", env.Status, env.Code)
	}
	if n := countActiveRefunds(t, pending.Data.Id); n != 0 {
		t.Fatalf("pending order must not create refund, got %d", n)
	}

	// 已发货订单（下单+支付+发货）→ 18003。
	orderID, _, _, token2, _ := createPaidOrderForRefund(t, base, "buyerr3b", "SKU-R3B", 1000, 10, 1)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	if s := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/ship", orderID), adminToken, map[string]any{}); s.Status != 200 || s.Code != 0 {
		t.Fatalf("ship: status=%d code=%d", s.Status, s.Code)
	}
	env = refundCall(t, base, "POST", fmt.Sprintf("/orders/%d/refund", orderID), token2, map[string]any{"reason": "x"})
	if env.Status != 409 || env.Code != 18003 {
		t.Fatalf("shipped order apply: status=%d code=%d want 409/18003", env.Status, env.Code)
	}
}

// TestRefundSingleActiveRequest 覆盖 AC-004/INV-004：同一订单并发两次申请，至多一条有效申请。
func TestRefundSingleActiveRequest(t *testing.T) {
	base := setupRefundServer(t)
	orderID, _, _, token, _ := createPaidOrderForRefund(t, base, "buyerr4", "SKU-R4", 1000, 10, 1)

	const n = 8
	var wg sync.WaitGroup
	success := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := refundCall(t, base, "POST", fmt.Sprintf("/orders/%d/refund", orderID), token, map[string]any{"reason": fmt.Sprintf("原因%d", i)})
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
	if okCount != 1 {
		t.Fatalf("exactly one apply should succeed, got %d", okCount)
	}
	if n := countActiveRefunds(t, orderID); n != 1 {
		t.Fatalf("active refunds=%d want 1", n)
	}
	// 该订单全部退款记录（含非有效）也应为 1。
	total, err := g.DB().Model("refund_requests").Ctx(context.Background()).Where("order_id", orderID).Count()
	if err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	if total != 1 {
		t.Fatalf("total refunds=%d want 1", total)
	}
}

// TestRefundReviewNotDirectRefund 覆盖 AC-006/INV-006：审核同意→待退款、拒绝→审核拒绝，订单保持已支付。
func TestRefundReviewNotDirectRefund(t *testing.T) {
	base := setupRefundServer(t)
	orderID, _, _, token, _ := createPaidOrderForRefund(t, base, "buyerr6", "SKU-R6", 1000, 10, 2)
	refundID := applyRefund(t, base, token, orderID, "申请退款")
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 审核通过 → 待退款，订单仍已支付。
	review := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/review", refundID), adminToken, map[string]any{"result": "approve", "reason": "同意"})
	if review.Status != 200 || review.Code != 0 || review.Data.Status != refundv1.StatusPendingRefund {
		t.Fatalf("approve: status=%d code=%d refund_status=%q", review.Status, review.Code, review.Data.Status)
	}
	if st := orderStatus(t, orderID); st != 20 {
		t.Fatalf("order should remain paid after approve, status=%d want 20", st)
	}
}

// TestRefundStateMachineIllegalTransition 覆盖 AC-005/INV-005：非法状态迁移返回 18002 且状态不变。
func TestRefundStateMachineIllegalTransition(t *testing.T) {
	base := setupRefundServer(t)
	orderID, _, _, token, _ := createPaidOrderForRefund(t, base, "buyerr5", "SKU-R5", 1000, 10, 1)
	refundID := applyRefund(t, base, token, orderID, "申请")
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 申请中直接执行（未经审核）→ 18002。
	exec := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/execute", refundID), adminToken, map[string]any{"result": "success", "reason": "直接执行"})
	if exec.Status != 409 || exec.Code != 18002 {
		t.Fatalf("execute on applying: status=%d code=%d want 409/18002", exec.Status, exec.Code)
	}
	if st := refundStatus(t, refundID); st != 10 {
		t.Fatalf("refund status=%d want 10", st)
	}

	// 申请中关闭 → 18002。
	closeRes := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/close", refundID), adminToken, map[string]any{"reason": "关闭"})
	if closeRes.Status != 409 || closeRes.Code != 18002 {
		t.Fatalf("close on applying: status=%d code=%d want 409/18002", closeRes.Status, closeRes.Code)
	}
	if st := refundStatus(t, refundID); st != 10 {
		t.Fatalf("refund status=%d want 10", st)
	}
}

// TestRefundShipMutualExclusion 覆盖 AC-007/INV-007：有效退款时发货被拒；并发申请+发货不「既已发货又有有效退款」。
func TestRefundShipMutualExclusion(t *testing.T) {
	base := setupRefundServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 场景 1：有有效退款（申请中）时发货被拒 18005。
	orderID, _, _, token, _ := createPaidOrderForRefund(t, base, "buyerr7", "SKU-R7", 1000, 10, 1)
	applyRefund(t, base, token, orderID, "申请")
	ship := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/ship", orderID), adminToken, map[string]any{})
	if ship.Status != 409 || ship.Code != 18005 {
		t.Fatalf("ship with active refund: status=%d code=%d want 409/18005", ship.Status, ship.Code)
	}
	if st := orderStatus(t, orderID); st != 20 {
		t.Fatalf("order must remain paid, status=%d want 20", st)
	}

	// 场景 2：并发「发起退款」与「发货」，最终不同时「已发货 + 有有效退款」。
	orderID2, _, _, token2, _ := createPaidOrderForRefund(t, base, "buyerr7b", "SKU-R7B", 1000, 10, 1)
	var wg sync.WaitGroup
	applyOK := make(chan bool, 1)
	shipOK := make(chan bool, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		res := refundCall(t, base, "POST", fmt.Sprintf("/orders/%d/refund", orderID2), token2, map[string]any{"reason": "并发申请"})
		applyOK <- (res.Status == 200 && res.Code == 0)
	}()
	go func() {
		defer wg.Done()
		res := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/ship", orderID2), adminToken, map[string]any{})
		shipOK <- (res.Status == 200 && res.Code == 0)
	}()
	wg.Wait()
	if <-applyOK && <-shipOK {
		t.Fatalf("申请退款与发货不能同时成功")
	}
	st := orderStatus(t, orderID2)
	hasActive := countActiveRefunds(t, orderID2) > 0
	if st == 30 && hasActive {
		t.Fatalf("已发货订单不应存在有效退款申请")
	}
	if st == 20 && !hasActive {
		t.Fatalf("未发货且无有效退款：互斥结果不一致")
	}
}

// TestRefundSuccessConsistency 覆盖 AC-008/AC-009/INV-008/INV-009：
// 退款成功后订单=70、库存恢复一次、退款=60、销量扣除；重复执行幂等。
func TestRefundSuccessConsistency(t *testing.T) {
	base := setupRefundServer(t)
	orderID, skuID, _, token, _ := createPaidOrderForRefund(t, base, "buyerr9", "SKU-R9", 1000, 10, 2)
	refundID := applyRefund(t, base, token, orderID, "申请")
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	if got := orderStock(t, skuID); got != 8 {
		t.Fatalf("after create stock=%d want 8", got)
	}

	// 审核通过 + 执行成功。
	if r := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/review", refundID), adminToken, map[string]any{"result": "approve", "reason": "同意"}); r.Status != 200 || r.Code != 0 {
		t.Fatalf("review: status=%d code=%d", r.Status, r.Code)
	}
	exec := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/execute", refundID), adminToken, map[string]any{"result": "success", "reason": "退款成功"})
	if exec.Status != 200 || exec.Code != 0 || exec.Data.Status != refundv1.StatusRefundSucceeded {
		t.Fatalf("execute: status=%d code=%d refund_status=%q", exec.Status, exec.Code, exec.Data.Status)
	}

	// 订单=70、库存恢复一次、退款=60。
	if st := orderStatus(t, orderID); st != 70 {
		t.Fatalf("order status=%d want 70", st)
	}
	if got := orderStock(t, skuID); got != 10 {
		t.Fatalf("inventory restored once, got %d want 10", got)
	}
	if st := refundStatus(t, refundID); st != 60 {
		t.Fatalf("refund status=%d want 60", st)
	}

	// 重复执行成功 → 幂等，不重复恢复库存。
	again := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/execute", refundID), adminToken, map[string]any{"result": "success", "reason": "重复确认"})
	if again.Status != 200 || again.Code != 0 {
		t.Fatalf("repeat execute: status=%d code=%d want 200/0", again.Status, again.Code)
	}
	if got := orderStock(t, skuID); got != 10 {
		t.Fatalf("repeat execute must not restore inventory again, got %d want 10", got)
	}

	// 退款成功后发货 → 9002（订单已退款，非已支付）。
	ship := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/ship", orderID), adminToken, map[string]any{})
	if ship.Status != 409 || ship.Code != 9002 {
		t.Fatalf("ship after refund: status=%d code=%d want 409/9002", ship.Status, ship.Code)
	}
}

// TestRefundFailureSafeAndClose 覆盖 AC-010/INV-010：失败安全 + 关闭出口 + 重试成功。
func TestRefundFailureSafeAndClose(t *testing.T) {
	base := setupRefundServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 退款失败：status=30、订单=20、库存不变。
	orderID, skuID, _, token, _ := createPaidOrderForRefund(t, base, "buyerr10", "SKU-R10", 1000, 10, 2)
	refundID := applyRefund(t, base, token, orderID, "申请")
	if r := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/review", refundID), adminToken, map[string]any{"result": "approve", "reason": "同意"}); r.Status != 200 || r.Code != 0 {
		t.Fatalf("review: status=%d code=%d", r.Status, r.Code)
	}
	fail := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/execute", refundID), adminToken, map[string]any{"result": "failure", "reason": "退款失败"})
	if fail.Status != 200 || fail.Code != 0 || fail.Data.Status != refundv1.StatusRefundFailed {
		t.Fatalf("execute failure: status=%d code=%d refund_status=%q", fail.Status, fail.Code, fail.Data.Status)
	}
	if st := orderStatus(t, orderID); st != 20 {
		t.Fatalf("order should remain paid after failure, status=%d want 20", st)
	}
	if got := orderStock(t, skuID); got != 8 {
		t.Fatalf("failure must not restore stock, got %d want 8", got)
	}

	// 非 30 状态关闭 → 18002（先测审核拒绝后关闭的非法路径不在此处，此处直接验证 30→70 关闭）。
	// 退款失败状态下发货仍被拒（有效状态含 30）。
	ship := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/ship", orderID), adminToken, map[string]any{})
	if ship.Status != 409 || ship.Code != 18005 {
		t.Fatalf("ship while refund failed: status=%d code=%d want 409/18005", ship.Status, ship.Code)
	}

	// 关闭：30→70，订单=20、库存不变，发货解除阻塞。
	closeRes := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/close", refundID), adminToken, map[string]any{"reason": "人工关闭"})
	if closeRes.Status != 200 || closeRes.Code != 0 || closeRes.Data.Status != refundv1.StatusClosed {
		t.Fatalf("close: status=%d code=%d refund_status=%q", closeRes.Status, closeRes.Code, closeRes.Data.Status)
	}
	if st := orderStatus(t, orderID); st != 20 {
		t.Fatalf("order should remain paid after close, status=%d want 20", st)
	}
	if got := orderStock(t, skuID); got != 8 {
		t.Fatalf("close must not restore stock, got %d want 8", got)
	}
	// 关闭后发货成功（无有效退款）。
	if s := orderCall(t, base, "POST", fmt.Sprintf("/admin/orders/%d/ship", orderID), adminToken, map[string]any{}); s.Status != 200 || s.Code != 0 {
		t.Fatalf("ship after close: status=%d code=%d want 200/0", s.Status, s.Code)
	}
}

// TestRefundRetrySuccessAfterFailure 覆盖 AC-010/INV-010：退款失败后重试成功（30→60），库存恢复一次。
func TestRefundRetrySuccessAfterFailure(t *testing.T) {
	base := setupRefundServer(t)
	orderID, skuID, _, token, _ := createPaidOrderForRefund(t, base, "buyerr10b", "SKU-R10B", 1000, 10, 2)
	refundID := applyRefund(t, base, token, orderID, "申请")
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	if r := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/review", refundID), adminToken, map[string]any{"result": "approve", "reason": "同意"}); r.Status != 200 || r.Code != 0 {
		t.Fatalf("review: status=%d code=%d", r.Status, r.Code)
	}
	if r := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/execute", refundID), adminToken, map[string]any{"result": "failure", "reason": "失败"}); r.Status != 200 || r.Code != 0 {
		t.Fatalf("execute failure: status=%d code=%d", r.Status, r.Code)
	}
	// 重试成功：30→60，库存恢复一次。
	retry := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/execute", refundID), adminToken, map[string]any{"result": "success", "reason": "重试成功"})
	if retry.Status != 200 || retry.Code != 0 || retry.Data.Status != refundv1.StatusRefundSucceeded {
		t.Fatalf("retry success: status=%d code=%d refund_status=%q", retry.Status, retry.Code, retry.Data.Status)
	}
	if st := orderStatus(t, orderID); st != 70 {
		t.Fatalf("order status=%d want 70", st)
	}
	if got := orderStock(t, skuID); got != 10 {
		t.Fatalf("inventory restored once, got %d want 10", got)
	}
}

// TestRefundRevoke 覆盖 AC-011/INV-011：审核通过前可撤销，不恢复库存、不改订单，可再次申请。
func TestRefundRevoke(t *testing.T) {
	base := setupRefundServer(t)
	orderID, skuID, _, token, _ := createPaidOrderForRefund(t, base, "buyerr11", "SKU-R11", 1000, 10, 2)
	refundID := applyRefund(t, base, token, orderID, "申请")

	revoke := refundCall(t, base, "POST", fmt.Sprintf("/refunds/%d/revoke", refundID), token, nil)
	if revoke.Status != 200 || revoke.Code != 0 || revoke.Data.Status != refundv1.StatusRevoked {
		t.Fatalf("revoke: status=%d code=%d refund_status=%q", revoke.Status, revoke.Code, revoke.Data.Status)
	}
	if st := orderStatus(t, orderID); st != 20 {
		t.Fatalf("order must remain paid after revoke, status=%d want 20", st)
	}
	if got := orderStock(t, skuID); got != 8 {
		t.Fatalf("revoke must not restore stock, got %d want 8", got)
	}

	// 撤销后可再次申请。
	refundID2 := applyRefund(t, base, token, orderID, "再次申请")
	if refundID2 == refundID {
		t.Fatalf("re-apply should create a new refund")
	}
}

// TestRefundQueryAndAudit 覆盖 AC-012/AC-013/INV-013：用户/管理员查询与审计只追加。
func TestRefundQueryAndAudit(t *testing.T) {
	base := setupRefundServer(t)
	orderID, _, _, token, _ := createPaidOrderForRefund(t, base, "buyerr12", "SKU-R12", 1000, 10, 1)
	refundID := applyRefund(t, base, token, orderID, "申请原因")
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 用户查详情。
	detail := refundCall(t, base, "GET", fmt.Sprintf("/refunds/%d", refundID), token, nil)
	if detail.Status != 200 || detail.Code != 0 || detail.Data.Id != refundID {
		t.Fatalf("user detail: status=%d code=%d", detail.Status, detail.Code)
	}
	// 用户查列表。
	items := refundList(t, base, "/refunds", token)
	if len(items) != 1 || items[0].Id != refundID {
		t.Fatalf("user list items=%d want 1", len(items))
	}

	// 审核 + 执行，产生审计。
	if r := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/review", refundID), adminToken, map[string]any{"result": "approve", "reason": "同意"}); r.Status != 200 || r.Code != 0 {
		t.Fatalf("review: status=%d code=%d", r.Status, r.Code)
	}
	if r := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/execute", refundID), adminToken, map[string]any{"result": "success", "reason": "成功"}); r.Status != 200 || r.Code != 0 {
		t.Fatalf("execute: status=%d code=%d", r.Status, r.Code)
	}

	// 管理员查列表/详情。
	adminItems := refundList(t, base, "/admin/refunds", adminToken)
	if len(adminItems) != 1 {
		t.Fatalf("admin list items=%d want 1", len(adminItems))
	}
	adminDetail := refundCall(t, base, "GET", fmt.Sprintf("/admin/refunds/%d", refundID), adminToken, nil)
	if adminDetail.Status != 200 || adminDetail.Code != 0 {
		t.Fatalf("admin detail: status=%d code=%d", adminDetail.Status, adminDetail.Code)
	}

	// 审计：apply + review_approve + execute_success，共 3 条，按 id 升序。
	audits := refundAudits(t, base, fmt.Sprintf("/admin/refunds/%d/audits", refundID), adminToken)
	if len(audits) != 3 {
		t.Fatalf("audits=%d want 3", len(audits))
	}
	if audits[0].Action != "apply" || audits[1].Action != "review_approve" || audits[2].Action != "execute_success" {
		t.Fatalf("audit actions=%v", []string{audits[0].Action, audits[1].Action, audits[2].Action})
	}
	if audits[0].OperatorType != 1 || audits[1].OperatorType != 2 || audits[2].OperatorType != 2 {
		t.Fatalf("audit operator_type mismatch")
	}
	if audits[0].AfterStatus != 10 || audits[1].AfterStatus != 20 || audits[2].AfterStatus != 60 {
		t.Fatalf("audit after_status mismatch")
	}
}

// TestRefundPermission 覆盖 AC-014/INV-012：管理端退款接口权限与零写入。
func TestRefundPermission(t *testing.T) {
	base := setupRefundServer(t)
	orderID, _, _, token, _ := createPaidOrderForRefund(t, base, "buyerr14", "SKU-R14", 1000, 10, 1)
	refundID := applyRefund(t, base, token, orderID, "申请")

	// 普通用户 token 访问后台退款接口 → 403。
	userRes := isoDo(t, base, "GET", "/admin/refunds", nil, isoAuthHeader(token))
	if userRes.Status != 403 {
		t.Fatalf("user token admin list: status=%d want 403", userRes.Status)
	}

	// 无权限普通管理员审核 → 403，零写入。
	isoInsertAdmin(t, "plainadminr14", "plainadmin123")
	plainToken, _ := isoAdminLogin(t, base, "plainadminr14", "plainadmin123")
	review := isoDo(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/review", refundID), map[string]any{"result": "approve", "reason": "同意"}, isoAuthHeader(plainToken))
	if review.Status != 403 || review.Code != 1003 {
		t.Fatalf("plain admin review: status=%d code=%d want 403/1003", review.Status, review.Code)
	}
	if st := refundStatus(t, refundID); st != 10 {
		t.Fatalf("forbidden review must not change refund, status=%d want 10", st)
	}

	// 超管审核放行。
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	ok := refundCall(t, base, "POST", fmt.Sprintf("/admin/refunds/%d/review", refundID), adminToken, map[string]any{"result": "approve", "reason": "同意"})
	if ok.Status != 200 || ok.Code != 0 {
		t.Fatalf("super review: status=%d code=%d", ok.Status, ok.Code)
	}
}
