package distribution

// 一级分销真实 MySQL 集成测试。
//
// 覆盖：推荐关系绑定（自荐/推荐人无效/重复绑定/成环/幂等）、佣金计提（基数与取整、一单一佣金、
// 无推荐人/未支付 no-op）、佣金结算状态机、退款冲回幂等、并发计提、前台/后台查询与汇总。
//
// 不 Mock 数据库：唯一约束、条件更新、行锁与并发语义都在真实 MySQL 上验证。

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/distribution/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

const testJWTSecret = "test-secret-0123456789-0123456789-0123456789"

// orderNoSeq 生成进程内唯一订单号，避免跨用例撞 uk_order_no。
var orderNoSeq atomic.Int64

// setupDistributionTest 建立隔离的分销测试环境。
func setupDistributionTest(t *testing.T) {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", "test-admin-password-123")

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	cleanDistributionTables(t, ctx)
	t.Cleanup(func() { cleanDistributionTables(t, context.Background()) })
}

// cleanDistributionTables 按外键顺序清理本测试涉及的域表。
func cleanDistributionTables(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, table := range []string{
		"distribution_commissions", "user_referrals",
		"member_growth_logs", "user_memberships", "member_levels",
		"order_items", "orders", "users",
	} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
}

// insertDistUser 插入一个前台用户，status=1 为启用、0 为禁用。
func insertDistUser(t *testing.T, username string, status int) int64 {
	t.Helper()
	id, err := g.DB().Model("users").Ctx(context.Background()).Data(g.Map{
		"username":      username,
		"password_hash": "x",
		"status":        status,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	return id
}

// insertDistOrder 插入订单；status 传 orderStatusPaid 表示已支付。
func insertDistOrder(t *testing.T, userID, original, discount, memberDiscount int64, status int) int64 {
	t.Helper()
	total := original - discount - memberDiscount
	orderNo := fmt.Sprintf("B10408-D-%d-%d", userID, orderNoSeq.Add(1))
	id, err := g.DB().Model("orders").Ctx(context.Background()).Data(g.Map{
		"order_no":               orderNo,
		"user_id":                userID,
		"status":                 status,
		"total_amount":           total,
		"original_amount":        original,
		"discount_amount":        discount,
		"member_discount_amount": memberDiscount,
		"idempotency_key":        orderNo,
		"request_hash":           "hash",
		"recipient_name":         "李四",
		"phone":                  "13900000000",
		"province":               "省",
		"city":                   "市",
		"district":               "区",
		"detail":                 "地址",
		"expire_at":              gtime.Now(),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	return id
}

// distService 返回被测服务（包内直连，便于断言未导出语义）。
func distService() *sDistribution { return New() }

// accrueInTx 在独立事务内计提佣金（模拟支付成功事务）。
func accrueInTx(t *testing.T, orderID int64) {
	t.Helper()
	if err := g.DB().Transaction(context.Background(), func(ctx context.Context, tx gdb.TX) error {
		return distService().AccrueForOrderInTx(ctx, tx, orderID)
	}); err != nil {
		t.Fatalf("计提佣金(订单 %d): %v", orderID, err)
	}
}

// reverseInTx 在独立事务内冲回佣金（模拟退款成功事务）。
func reverseInTx(t *testing.T, orderID, refundID int64) {
	t.Helper()
	if err := g.DB().Transaction(context.Background(), func(ctx context.Context, tx gdb.TX) error {
		return distService().ReverseForRefundInTx(ctx, tx, orderID, refundID)
	}); err != nil {
		t.Fatalf("冲回佣金(订单 %d): %v", orderID, err)
	}
}

// bind 调用绑定并返回错误码。
func bind(t *testing.T, userID, referrerID int64) (int, error) {
	t.Helper()
	_, err := distService().Bind(context.Background(), userID, &v1.BindReq{ReferrerUserId: referrerID})
	return codes.FromError(err), err
}

// loadCommission 读取指定订单的佣金记录（不存在返回 nil）。
func loadCommission(t *testing.T, orderID int64) *commissionRow {
	t.Helper()
	var rows []*commissionRow
	if err := g.DB().Model("distribution_commissions").Ctx(context.Background()).
		Where("order_id", orderID).Scan(&rows); err != nil {
		t.Fatalf("读取佣金: %v", err)
	}
	if len(rows) == 0 {
		return nil
	}
	return rows[0]
}

// TestBindRules 覆盖推荐关系绑定的全部拒绝路径与幂等语义。
func TestBindRules(t *testing.T) {
	setupDistributionTest(t)
	ctx := context.Background()

	inviter := insertDistUser(t, "dist-inviter", 1)
	invitee := insertDistUser(t, "dist-invitee", 1)
	disabled := insertDistUser(t, "dist-disabled", 0)
	other := insertDistUser(t, "dist-other", 1)

	// 自荐 → 21003。
	if code, _ := bind(t, invitee, invitee); code != codes.CodeReferralSelfBind {
		t.Fatalf("自荐期望 21003，实际 %d", code)
	}
	// 推荐人不存在 → 21005。
	if code, _ := bind(t, invitee, 999999); code != codes.CodeReferralInvalidReferrer {
		t.Fatalf("推荐人不存在期望 21005，实际 %d", code)
	}
	// 推荐人已禁用 → 21005。
	if code, _ := bind(t, invitee, disabled); code != codes.CodeReferralInvalidReferrer {
		t.Fatalf("推荐人禁用期望 21005，实际 %d", code)
	}
	// 非法入参 → 1001。
	if code, _ := bind(t, invitee, 0); code != codes.CodeInvalidArgument {
		t.Fatalf("referrer_user_id=0 期望 1001，实际 %d", code)
	}

	// 正常绑定。
	res, err := distService().Bind(ctx, invitee, &v1.BindReq{ReferrerUserId: inviter})
	if err != nil {
		t.Fatalf("正常绑定: %v", err)
	}
	if res.Referral.UserId != invitee || res.Referral.ReferrerUserId != inviter || res.Referral.ReferrerUsername != "dist-inviter" {
		t.Fatalf("绑定结果不符: %+v", res.Referral)
	}

	// 重复绑定同一推荐人：幂等成功，只有一条关系。
	if code, err := bind(t, invitee, inviter); err != nil {
		t.Fatalf("重复绑定同一推荐人应幂等成功，实际 %d/%v", code, err)
	}
	if cnt, _ := g.DB().Model("user_referrals").Ctx(ctx).Where("user_id", invitee).Count(); cnt != 1 {
		t.Fatalf("推荐关系应只有 1 条，实际 %d", cnt)
	}
	// 改绑到其他人 → 21002，且关系不变。
	if code, _ := bind(t, invitee, other); code != codes.CodeReferralAlreadyBound {
		t.Fatalf("改绑期望 21002，实际 %d", code)
	}

	// 反向绑定形成 2 环 → 21004。
	if code, _ := bind(t, inviter, invitee); code != codes.CodeReferralCycle {
		t.Fatalf("反向绑定成环期望 21004，实际 %d", code)
	}
	inviterReferrals, err := g.DB().Model("user_referrals").Ctx(ctx).Where("user_id", inviter).Count()
	if err != nil {
		t.Fatalf("统计推荐关系: %v", err)
	}
	if inviterReferrals != 0 {
		t.Fatal("成环的绑定必须整体回滚，不应留下关系行")
	}
}

// TestBindRejectsLongerCycle 覆盖三级链成环（A→B、B→C 后 C→A 必须被拒绝）。
func TestBindRejectsLongerCycle(t *testing.T) {
	setupDistributionTest(t)

	a := insertDistUser(t, "cycle-a", 1)
	b := insertDistUser(t, "cycle-b", 1)
	c := insertDistUser(t, "cycle-c", 1)

	if code, err := bind(t, a, b); err != nil {
		t.Fatalf("A→B 期望成功，实际 %d/%v", code, err)
	}
	if code, err := bind(t, b, c); err != nil {
		t.Fatalf("B→C 期望成功，实际 %d/%v", code, err)
	}
	if code, _ := bind(t, c, a); code != codes.CodeReferralCycle {
		t.Fatalf("C→A 形成 3 环期望 21004，实际 %d", code)
	}
}

// TestCommissionAccrualAndIdempotency 覆盖计佣基数、取整、一单一佣金与全部 no-op 情形。
func TestCommissionAccrualAndIdempotency(t *testing.T) {
	setupDistributionTest(t)

	buyer := insertDistUser(t, "comm-buyer", 1)
	referrer := insertDistUser(t, "comm-referrer", 1)
	lonely := insertDistUser(t, "comm-no-referrer", 1)
	if code, err := bind(t, buyer, referrer); err != nil {
		t.Fatalf("绑定推荐关系: %d/%v", code, err)
	}

	// 基数 = 1000 - 100(券) - 50(会员折扣) = 850；佣金 = floor(850 × 500 / 10000) = 42。
	orderID := insertDistOrder(t, buyer, 1000, 100, 50, orderStatusPaid)
	accrueInTx(t, orderID)
	row := loadCommission(t, orderID)
	if row == nil {
		t.Fatal("已支付且有推荐人的订单应生成佣金")
	}
	if row.BaseAmount != 850 {
		t.Fatalf("计佣基数期望 850（商品实收，不含运费/优惠），实际 %d", row.BaseAmount)
	}
	if row.RateBp != commissionRateBp || row.CommissionAmount != 42 {
		t.Fatalf("比例/金额期望 %d/42，实际 %d/%d", commissionRateBp, row.RateBp, row.CommissionAmount)
	}
	if row.Status != commissionStatusPending || row.SettledAt != nil || row.ReversedAmount != 0 {
		t.Fatalf("新佣金应为待结算且未冲回: %+v", row)
	}
	if row.BuyerUserId != buyer || row.BeneficiaryUserId != referrer {
		t.Fatalf("身份快照不符: %+v", row)
	}

	// 重复计提：幂等，不产生第二条记录。
	accrueInTx(t, orderID)
	if cnt, _ := g.DB().Model("distribution_commissions").Where("order_id", orderID).Count(); cnt != 1 {
		t.Fatalf("一单只能有一条佣金，实际 %d 条", cnt)
	}

	// 未支付订单：不计佣。
	unpaid := insertDistOrder(t, buyer, 1000, 0, 0, 10)
	accrueInTx(t, unpaid)
	if loadCommission(t, unpaid) != nil {
		t.Fatal("未支付订单不应计提佣金")
	}

	// 无推荐人的买家：不计佣。
	lonelyOrder := insertDistOrder(t, lonely, 1000, 0, 0, orderStatusPaid)
	accrueInTx(t, lonelyOrder)
	if loadCommission(t, lonelyOrder) != nil {
		t.Fatal("无推荐人的订单不应计提佣金")
	}

	// 佣金向下取整为 0：不写记录（避免 0 元佣金占用 uk_order_id）。
	tiny := insertDistOrder(t, buyer, 10, 0, 0, orderStatusPaid)
	accrueInTx(t, tiny)
	if loadCommission(t, tiny) != nil {
		t.Fatal("佣金为 0 时不应写佣金记录")
	}

	// 全额优惠（基数为 0）：不写记录。
	zeroBase := insertDistOrder(t, buyer, 500, 500, 0, orderStatusPaid)
	accrueInTx(t, zeroBase)
	if loadCommission(t, zeroBase) != nil {
		t.Fatal("基数为 0 时不应写佣金记录")
	}
}

// TestCommissionReversalIdempotency 覆盖退款冲回：同额冲回、状态置 30、同一退款只冲回一次。
func TestCommissionReversalIdempotency(t *testing.T) {
	setupDistributionTest(t)

	buyer := insertDistUser(t, "rev-buyer", 1)
	referrer := insertDistUser(t, "rev-referrer", 1)
	if code, err := bind(t, buyer, referrer); err != nil {
		t.Fatalf("绑定: %d/%v", code, err)
	}
	orderID := insertDistOrder(t, buyer, 1000, 0, 0, orderStatusPaid)
	accrueInTx(t, orderID)
	original := loadCommission(t, orderID)
	if original == nil || original.CommissionAmount != 50 {
		t.Fatalf("佣金期望 50，实际 %+v", original)
	}

	reverseInTx(t, orderID, 700001)
	reversed := loadCommission(t, orderID)
	if reversed.Status != commissionStatusReversed {
		t.Fatalf("冲回后状态期望 30，实际 %d", reversed.Status)
	}
	if reversed.ReversedAmount != original.CommissionAmount {
		t.Fatalf("冲回金额应等于原佣金 %d，实际 %d", original.CommissionAmount, reversed.ReversedAmount)
	}
	if reversed.RefundId == nil || *reversed.RefundId != 700001 || reversed.ReversalReason != v1.ReversalReasonRefund {
		t.Fatalf("冲回溯源字段不符: %+v", reversed)
	}
	if reversed.ReversedAt == nil {
		t.Fatal("冲回应写 reversed_at")
	}

	// 同一退款重复冲回：no-op，金额不变。
	reverseInTx(t, orderID, 700001)
	again := loadCommission(t, orderID)
	if again.ReversedAmount != original.CommissionAmount {
		t.Fatalf("重复冲回不应改变金额，实际 %d", again.ReversedAmount)
	}

	// 另一退款单对已冲回佣金再冲回：状态条件更新命中 0 行，no-op（不会二次冲回）。
	reverseInTx(t, orderID, 700002)
	final := loadCommission(t, orderID)
	if final.ReversedAmount != original.CommissionAmount {
		t.Fatalf("已冲回佣金不应再次冲回，实际 %d", final.ReversedAmount)
	}

	// 无佣金记录的订单冲回：no-op，不伪造记录。
	otherOrder := insertDistOrder(t, buyer, 1000, 0, 0, orderStatusPaid)
	reverseInTx(t, otherOrder, 700003)
	if loadCommission(t, otherOrder) != nil {
		t.Fatal("无佣金记录的订单冲回不应写入记录")
	}
}

// TestCommissionSettleStateMachine 覆盖 10→20 合法、重复结算幂等、30→20 拒绝。
func TestCommissionSettleStateMachine(t *testing.T) {
	setupDistributionTest(t)
	ctx := context.Background()
	svc := distService()

	buyer := insertDistUser(t, "settle-buyer", 1)
	referrer := insertDistUser(t, "settle-referrer", 1)
	if code, err := bind(t, buyer, referrer); err != nil {
		t.Fatalf("绑定: %d/%v", code, err)
	}
	orderID := insertDistOrder(t, buyer, 1000, 0, 0, orderStatusPaid)
	accrueInTx(t, orderID)
	row := loadCommission(t, orderID)

	// 结算 pending→settled。
	res, err := svc.AdminSettleCommission(ctx, 1, row.Id)
	if err != nil {
		t.Fatalf("结算: %v", err)
	}
	if res.Commission.Status != v1.CommissionStatusSettled || res.Commission.SettledAt == nil {
		t.Fatalf("结算结果不符: %+v", res.Commission)
	}
	// 重复结算：幂等成功。
	if _, err := svc.AdminSettleCommission(ctx, 1, row.Id); err != nil {
		t.Fatalf("重复结算应幂等: %v", err)
	}
	// 冲回后结算：21007。
	reverseInTx(t, orderID, 700101)
	if _, err := svc.AdminSettleCommission(ctx, 1, row.Id); codes.FromError(err) != codes.CodeCommissionInvalidStatus {
		t.Fatalf("已冲回佣金结算期望 21007，实际 %v", err)
	}
	// 不存在的佣金：21006。
	if _, err := svc.AdminSettleCommission(ctx, 1, 999999); codes.FromError(err) != codes.CodeCommissionNotFound {
		t.Fatalf("不存在佣金期望 21006，实际 %v", err)
	}
}

// TestCommissionConcurrentAccrual 覆盖并发计提：同一订单只产生一条佣金，多订单各一条。
func TestCommissionConcurrentAccrual(t *testing.T) {
	setupDistributionTest(t)

	buyer := insertDistUser(t, "conc-buyer", 1)
	referrer := insertDistUser(t, "conc-referrer", 1)
	if code, err := bind(t, buyer, referrer); err != nil {
		t.Fatalf("绑定: %d/%v", code, err)
	}

	const n = 6
	orders := make([]int64, n)
	for i := 0; i < n; i++ {
		orders[i] = insertDistOrder(t, buyer, 1000, 0, 0, orderStatusPaid)
	}
	// 额外压测同一订单的并发重复计提。
	sameOrder := insertDistOrder(t, buyer, 2000, 0, 0, orderStatusPaid)

	var wg sync.WaitGroup
	errs := make(chan error, n*2)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(orderID int64) {
			defer wg.Done()
			errs <- g.DB().Transaction(context.Background(), func(ctx context.Context, tx gdb.TX) error {
				return distService().AccrueForOrderInTx(ctx, tx, orderID)
			})
		}(orders[i])
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- g.DB().Transaction(context.Background(), func(ctx context.Context, tx gdb.TX) error {
				return distService().AccrueForOrderInTx(ctx, tx, sameOrder)
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发计提失败: %v", err)
		}
	}

	for i, orderID := range orders {
		if loadCommission(t, orderID) == nil {
			t.Fatalf("订单 #%d 应生成佣金", i)
		}
	}
	if cnt, _ := g.DB().Model("distribution_commissions").Where("order_id", sameOrder).Count(); cnt != 1 {
		t.Fatalf("同一订单并发计提只能有 1 条佣金，实际 %d 条", cnt)
	}
}

// TestAdminAndUserQueries 覆盖前台/后台查询、过滤、汇总与严格分页校验。
func TestAdminAndUserQueries(t *testing.T) {
	setupDistributionTest(t)
	ctx := context.Background()
	svc := distService()

	buyer := insertDistUser(t, "query-buyer", 1)
	referrer := insertDistUser(t, "query-referrer", 1)
	if code, err := bind(t, buyer, referrer); err != nil {
		t.Fatalf("绑定: %d/%v", code, err)
	}
	o1 := insertDistOrder(t, buyer, 1000, 0, 0, orderStatusPaid) // 佣金 50
	o2 := insertDistOrder(t, buyer, 2000, 0, 0, orderStatusPaid) // 佣金 100
	accrueInTx(t, o1)
	accrueInTx(t, o2)

	// 推荐人本身没有上级（bound=false），但应看到 1 个直接下级。
	myRef, err := svc.MyReferral(ctx, referrer)
	if err != nil {
		t.Fatalf("MyReferral: %v", err)
	}
	if myRef.Bound || myRef.ReferrerUserId != nil || myRef.InviteeCount != 1 {
		t.Fatalf("推荐人无上级且有 1 个下级，实际 %+v", myRef)
	}
	buyerRef, err := svc.MyReferral(ctx, buyer)
	if err != nil {
		t.Fatalf("MyReferral(买家): %v", err)
	}
	if !buyerRef.Bound || buyerRef.ReferrerUserId == nil || *buyerRef.ReferrerUserId != referrer {
		t.Fatalf("买家推荐关系不符: %+v", buyerRef)
	}
	// 无推荐人的用户：bound=false。
	stranger := insertDistUser(t, "query-stranger", 1)
	strangerRef, err := svc.MyReferral(ctx, stranger)
	if err != nil {
		t.Fatalf("MyReferral(无关用户): %v", err)
	}
	if strangerRef.Bound || strangerRef.InviteeCount != 0 {
		t.Fatalf("无关用户不应有推荐关系: %+v", strangerRef)
	}

	// 我的佣金：只返回本人受益部分。
	mine, err := svc.MyCommissions(ctx, referrer, &v1.MyCommissionsReq{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("MyCommissions: %v", err)
	}
	if mine.Total != 2 {
		t.Fatalf("推荐人应有 2 条佣金，实际 %d", mine.Total)
	}
	if mine.Items[0].OrderNo == "" {
		t.Fatal("佣金明细应联查订单号")
	}
	// 他人看不到。
	otherMine, err := svc.MyCommissions(ctx, stranger, &v1.MyCommissionsReq{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("MyCommissions(无关用户): %v", err)
	}
	if otherMine.Total != 0 {
		t.Fatalf("无关用户不应看到佣金，实际 %d", otherMine.Total)
	}
	// 状态过滤非法 → 400。
	if _, err := svc.MyCommissions(ctx, referrer, &v1.MyCommissionsReq{Status: "bogus"}); codes.FromError(err) != codes.CodeInvalidArgument {
		t.Fatalf("非法状态过滤期望 1001，实际 %v", err)
	}

	// 汇总：待结算 150。
	summary, err := svc.MyCommissionSummary(ctx, referrer)
	if err != nil {
		t.Fatalf("MyCommissionSummary: %v", err)
	}
	if summary.PendingCount != 2 || summary.PendingAmount != 150 || summary.TotalCommission != 150 {
		t.Fatalf("汇总不符: %+v", summary)
	}

	// 结算一条后汇总拆分为 pending/settled。
	settledRow := loadCommission(t, o2)
	if _, err := svc.AdminSettleCommission(ctx, 1, settledRow.Id); err != nil {
		t.Fatalf("结算: %v", err)
	}
	summary, err = svc.MyCommissionSummary(ctx, referrer)
	if err != nil {
		t.Fatalf("MyCommissionSummary: %v", err)
	}
	if summary.PendingAmount != 50 || summary.SettledAmount != 100 {
		t.Fatalf("结算后汇总不符: %+v", summary)
	}

	// 后台列表：按受益人与状态过滤。
	beneficiary := referrer
	adminList, err := svc.AdminCommissions(ctx, &v1.AdminCommissionsReq{
		BeneficiaryUserId: &beneficiary,
		Status:            v1.CommissionStatusSettled,
	})
	if err != nil {
		t.Fatalf("AdminCommissions: %v", err)
	}
	if adminList.Total != 1 || adminList.Items[0].CommissionAmount != 100 {
		t.Fatalf("后台过滤结果不符: %+v", adminList)
	}
	// 严格分页：越界 → 400。
	badSize := 0
	if _, err := svc.AdminCommissions(ctx, &v1.AdminCommissionsReq{Size: &badSize}); codes.FromError(err) != codes.CodeInvalidArgument {
		t.Fatalf("size=0 期望 1001，实际 %v", err)
	}
	// 非法排序 → 400。
	if _, err := svc.AdminCommissions(ctx, &v1.AdminCommissionsReq{Sort: "commission_amount"}); codes.FromError(err) != codes.CodeInvalidArgument {
		t.Fatalf("非法排序期望 1001，实际 %v", err)
	}
	// 时间范围非法 → 400。
	if _, err := svc.AdminCommissions(ctx, &v1.AdminCommissionsReq{StartTime: "2026-11-10"}); codes.FromError(err) != codes.CodeInvalidArgument {
		t.Fatalf("非法时间期望 1001，实际 %v", err)
	}
	// 后台推荐关系过滤。
	adminRefs, err := svc.AdminReferrals(ctx, &v1.AdminReferralsReq{ReferrerUserId: &referrer})
	if err != nil {
		t.Fatalf("AdminReferrals: %v", err)
	}
	if adminRefs.Total != 1 || adminRefs.Items[0].ReferrerUserId != referrer || adminRefs.Items[0].Username != "query-buyer" {
		t.Fatalf("后台推荐关系结果不符: %+v", adminRefs)
	}
	// 佣金详情。
	detail, err := svc.AdminCommission(ctx, settledRow.Id)
	if err != nil {
		t.Fatalf("AdminCommission: %v", err)
	}
	if detail.Id != settledRow.Id || detail.OrderNo == "" {
		t.Fatalf("佣金详情不符: %+v", detail.Commission)
	}
	if _, err := svc.AdminCommission(ctx, 999999); codes.FromError(err) != codes.CodeCommissionNotFound {
		t.Fatalf("不存在佣金详情期望 21006，实际 %v", err)
	}
}
