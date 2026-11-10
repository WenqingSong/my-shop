package member

// 会员等级与成长值真实 MySQL 集成测试。
//
// 覆盖：等级配置 CRUD 与校验（唯一性、单调性、引用保护）、等级解析与折扣率、
// 成长值发放/升级/幂等、退款扣减/幂等/不降级、并发成长值变更、未配置等级时 no-op、
// 成长值流水分页与前后值一致性。
//
// 不 Mock 数据库：事务、唯一约束、行锁与并发语义都必须在真实 MySQL 上验证。

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

	v1 "cnb.cool/go-cloud-devops/my-shop/api/member/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

const testJWTSecret = "test-secret-0123456789-0123456789-0123456789"

// orderNoSeq 生成进程内唯一订单号，避免跨用例撞 uk_order_no。
var orderNoSeq atomic.Int64

// setupMemberTest 建立隔离的会员测试环境：migration + Bootstrap + 清空会员/订单域表。
func setupMemberTest(t *testing.T) {
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
	cleanMemberTables(t, ctx)
	t.Cleanup(func() { cleanMemberTables(t, context.Background()) })
}

// cleanMemberTables 按外键顺序清理本测试涉及的域表。
func cleanMemberTables(t *testing.T, ctx context.Context) {
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

// insertMemberTestUser 插入一个启用状态的前台用户并返回 id。
func insertMemberTestUser(t *testing.T, username string, status int) int64 {
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

// insertPaidOrder 插入一笔已支付订单并返回 id（金额口径：original/discount/memberDiscount/total）。
func insertPaidOrder(t *testing.T, userID, original, discount, memberDiscount int64) int64 {
	t.Helper()
	total := original - discount - memberDiscount
	return insertOrderWithStatus(t, userID, original, discount, memberDiscount, total, orderStatusPaid)
}

// insertOrderWithStatus 插入指定状态的订单。
func insertOrderWithStatus(t *testing.T, userID, original, discount, memberDiscount, total int64, status int) int64 {
	t.Helper()
	orderNo := fmt.Sprintf("B10408-M-%d-%d", userID, orderNoSeq.Add(1))
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
		"recipient_name":         "张三",
		"phone":                  "13800000000",
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

// createLevel 通过服务创建等级配置。
func createLevel(t *testing.T, levelNo int, name string, threshold int64, rateBp, status int) *v1.MemberLevel {
	t.Helper()
	res, err := memberService().AdminCreateLevel(context.Background(), &v1.AdminCreateLevelReq{
		LevelNo:         levelNo,
		Name:            name,
		GrowthThreshold: threshold,
		DiscountRateBp:  rateBp,
		Status:          status,
	})
	if err != nil {
		t.Fatalf("create level %d: %v", levelNo, err)
	}
	return &res.MemberLevel
}

// memberService 返回被测会员服务（包内直连，便于断言未导出行为）。
func memberService() *sMember { return New() }

// accrual 在独立事务内调用成长值发放（模拟支付成功事务）。
func accrual(t *testing.T, orderID int64) {
	t.Helper()
	if err := g.DB().Transaction(context.Background(), func(ctx context.Context, tx gdb.TX) error {
		return memberService().AccrueForOrderInTx(ctx, tx, orderID)
	}); err != nil {
		t.Fatalf("accrue order %d: %v", orderID, err)
	}
}

// TestAdminLevelCRUDValidation 覆盖等级配置的创建/更新/删除与全部校验拒绝路径。
func TestAdminLevelCRUDValidation(t *testing.T) {
	setupMemberTest(t)
	m := memberService()
	ctx := context.Background()

	l1 := createLevel(t, 1, "普通会员", 0, 10000, v1.LevelStatusEnabled)
	l2 := createLevel(t, 2, "银牌会员", 1000, 9500, v1.LevelStatusEnabled)
	if l1.GrowthThreshold != 0 || l2.DiscountRateBp != 9500 {
		t.Fatalf("创建结果不符: %+v %+v", l1, l2)
	}

	// 序号重复 / 门槛重复 → 20004。
	if _, err := m.AdminCreateLevel(ctx, &v1.AdminCreateLevelReq{LevelNo: 1, Name: "重复序号", GrowthThreshold: 2000, DiscountRateBp: 9000, Status: 1}); codes.FromError(err) != codes.CodeMemberLevelDuplicate {
		t.Fatalf("重复 level_no 期望 20004，实际 %v", err)
	}
	if _, err := m.AdminCreateLevel(ctx, &v1.AdminCreateLevelReq{LevelNo: 3, Name: "重复门槛", GrowthThreshold: 1000, DiscountRateBp: 9000, Status: 1}); codes.FromError(err) != codes.CodeMemberLevelDuplicate {
		t.Fatalf("重复门槛期望 20004，实际 %v", err)
	}

	// 破坏单调性：level_no=3 的门槛低于 level_no=2 → 20002 且不落库。
	if _, err := m.AdminCreateLevel(ctx, &v1.AdminCreateLevelReq{LevelNo: 3, Name: "倒挂", GrowthThreshold: 500, DiscountRateBp: 9000, Status: 1}); codes.FromError(err) != codes.CodeMemberLevelInvalid {
		t.Fatalf("破坏单调性期望 20002，实际 %v", err)
	}
	if cnt, _ := g.DB().Model("member_levels").Ctx(ctx).Where("level_no", 3).Count(); cnt != 0 {
		t.Fatalf("破坏单调性的等级不应落库，实际存在 %d 行", cnt)
	}

	// 参数非法：折扣率越界、名称为空、门槛为负、状态非法 → 20002。
	bad := []*v1.AdminCreateLevelReq{
		{LevelNo: 3, Name: "折扣越界", GrowthThreshold: 2000, DiscountRateBp: 0, Status: 1},
		{LevelNo: 3, Name: "折扣越界", GrowthThreshold: 2000, DiscountRateBp: 10001, Status: 1},
		{LevelNo: 3, Name: "   ", GrowthThreshold: 2000, DiscountRateBp: 9000, Status: 1},
		{LevelNo: 3, Name: "负门槛", GrowthThreshold: -1, DiscountRateBp: 9000, Status: 1},
		{LevelNo: 0, Name: "序号为0", GrowthThreshold: 2000, DiscountRateBp: 9000, Status: 1},
		{LevelNo: 3, Name: "状态非法", GrowthThreshold: 2000, DiscountRateBp: 9000, Status: 2},
	}
	for i, req := range bad {
		if _, err := m.AdminCreateLevel(ctx, req); codes.FromError(err) != codes.CodeMemberLevelInvalid {
			t.Fatalf("非法入参 #%d 期望 20002，实际 %v", i, err)
		}
	}

	// 更新不存在 → 20001。
	if _, err := m.AdminUpdateLevel(ctx, &v1.AdminUpdateLevelReq{Id: 999999, LevelNo: 9, Name: "x", GrowthThreshold: 99999, DiscountRateBp: 9000, Status: 1}); codes.FromError(err) != codes.CodeMemberLevelNotFound {
		t.Fatalf("更新不存在期望 20001，实际 %v", err)
	}

	// 更新成与另一等级同门槛 → 20004。
	if _, err := m.AdminUpdateLevel(ctx, &v1.AdminUpdateLevelReq{Id: l2.Id, LevelNo: 2, Name: "银牌", GrowthThreshold: 0, DiscountRateBp: 9500, Status: 1}); codes.FromError(err) != codes.CodeMemberLevelDuplicate {
		t.Fatalf("更新成重复门槛期望 20004，实际 %v", err)
	}

	// 正常更新（含停用）。
	updated, err := m.AdminUpdateLevel(ctx, &v1.AdminUpdateLevelReq{Id: l2.Id, LevelNo: 2, Name: "银牌会员", GrowthThreshold: 1000, DiscountRateBp: 9400, Status: v1.LevelStatusDisabled})
	if err != nil {
		t.Fatalf("更新等级: %v", err)
	}
	if updated.MemberLevel.Status != v1.LevelStatusDisabled || updated.MemberLevel.DiscountRateBp != 9400 {
		t.Fatalf("更新结果不符: %+v", updated.MemberLevel)
	}

	// 前台列表只返回启用等级。
	levels, err := m.Levels(ctx)
	if err != nil {
		t.Fatalf("查询等级: %v", err)
	}
	if len(levels.Items) != 1 || levels.Items[0].LevelNo != 1 {
		t.Fatalf("启用等级列表应只有 level_no=1，实际 %+v", levels.Items)
	}

	// 后台列表含停用。
	adminLevels, err := m.AdminListLevels(ctx)
	if err != nil {
		t.Fatalf("后台查询等级: %v", err)
	}
	if len(adminLevels.Items) != 2 {
		t.Fatalf("后台等级列表应有 2 条，实际 %d", len(adminLevels.Items))
	}

	// 删除未被引用的等级成功；被引用的等级返回 20003。
	if _, err := m.AdminDeleteLevel(ctx, l2.Id); err != nil {
		t.Fatalf("删除未引用等级: %v", err)
	}
	if _, err := m.AdminDeleteLevel(ctx, l2.Id); codes.FromError(err) != codes.CodeMemberLevelNotFound {
		t.Fatalf("重复删除期望 20001，实际 %v", err)
	}
	userID := insertMemberTestUser(t, "member-del-ref", 1)
	orderID := insertPaidOrder(t, userID, 500, 0, 0)
	accrual(t, orderID)
	if _, err := m.AdminDeleteLevel(ctx, l1.Id); codes.FromError(err) != codes.CodeMemberLevelInUse {
		t.Fatalf("删除被引用等级期望 20003，实际 %v", err)
	}
}

// TestMembershipDefaultsAndDiscount 覆盖无档案时的等级解析、折扣率与升级进度。
func TestMembershipDefaultsAndDiscount(t *testing.T) {
	setupMemberTest(t)
	m := memberService()
	ctx := context.Background()

	// 无任何等级配置：无等级、无折扣、无副作用。
	userID := insertMemberTestUser(t, "member-default", 1)
	before, err := m.My(ctx, userID)
	if err != nil {
		t.Fatalf("My(无等级): %v", err)
	}
	if before.LevelId != nil || before.DiscountRateBp != noDiscountRateBp || before.GrowthValue != 0 {
		t.Fatalf("无等级配置时期望空等级 + 不打折，实际 %+v", before.Membership)
	}
	rate, err := m.DiscountRateBp(ctx, userID)
	if err != nil || rate != noDiscountRateBp {
		t.Fatalf("无等级配置时折扣率期望 %d，实际 %d/%v", noDiscountRateBp, rate, err)
	}
	if cnt, _ := g.DB().Model("user_memberships").Ctx(ctx).Where("user_id", userID).Count(); cnt != 0 {
		t.Fatalf("只读查询不应创建会员档案，实际 %d 行", cnt)
	}

	// 配置 3 级：0 → 9.8 折、1000 → 9.5 折、5000 → 9 折。
	createLevel(t, 1, "青铜", 0, 9800, v1.LevelStatusEnabled)
	createLevel(t, 2, "白银", 1000, 9500, v1.LevelStatusEnabled)
	createLevel(t, 3, "黄金", 5000, 9000, v1.LevelStatusEnabled)

	// 未建档用户按成长值 0 解析 → 青铜 9.8 折，level_id 仍为 null（区分未建档）。
	my, err := m.My(ctx, userID)
	if err != nil {
		t.Fatalf("My: %v", err)
	}
	if my.LevelId != nil || my.LevelName != "青铜" || my.DiscountRateBp != 9800 {
		t.Fatalf("未建档应按成长值 0 解析出青铜，实际 %+v", my.Membership)
	}
	if my.NextThreshold == nil || *my.NextThreshold != 1000 {
		t.Fatalf("下一等级门槛期望 1000，实际 %v", my.NextThreshold)
	}
	rate, err = m.DiscountRateBp(ctx, userID)
	if err != nil || rate != 9800 {
		t.Fatalf("折扣率期望 9800，实际 %d/%v", rate, err)
	}

	// 后台查询：未建档时 has_record=false。
	admin, err := m.AdminMember(ctx, userID)
	if err != nil {
		t.Fatalf("AdminMember: %v", err)
	}
	if admin.HasRecord {
		t.Fatal("未建档用户 has_record 应为 false")
	}
}

// TestGrowthAccrualUpgradeAndIdempotency 覆盖成长值发放、等级升级与重复事件幂等。
func TestGrowthAccrualUpgradeAndIdempotency(t *testing.T) {
	setupMemberTest(t)
	m := memberService()
	ctx := context.Background()

	createLevel(t, 1, "青铜", 0, 9800, v1.LevelStatusEnabled)
	createLevel(t, 2, "白银", 1000, 9500, v1.LevelStatusEnabled)
	createLevel(t, 3, "黄金", 5000, 9000, v1.LevelStatusEnabled)

	userID := insertMemberTestUser(t, "member-accrue", 1)
	order1 := insertPaidOrder(t, userID, 1000, 0, 0)

	accrual(t, order1)
	my, err := m.My(ctx, userID)
	if err != nil {
		t.Fatalf("My: %v", err)
	}
	if my.GrowthValue != 1000 {
		t.Fatalf("成长值期望 1000，实际 %d", my.GrowthValue)
	}
	if my.LevelNo == nil || *my.LevelNo != 2 {
		t.Fatalf("达到 1000 门槛应升到 level_no=2，实际 %v", my.LevelNo)
	}
	if my.UpgradedAt == nil {
		t.Fatal("升级应写 upgraded_at")
	}

	logs, err := m.GrowthLogs(ctx, userID, &v1.GrowthLogsReq{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("GrowthLogs: %v", err)
	}
	if logs.Total != 1 || logs.Items[0].ChangeAmount != 1000 || logs.Items[0].GrowthBefore != 0 || logs.Items[0].GrowthAfter != 1000 {
		t.Fatalf("成长值流水不符: %+v", logs.Items)
	}
	if logs.Items[0].ChangeType != changeTypeOrderReward || logs.Items[0].BizType != bizTypeOrder {
		t.Fatalf("流水类型不符: %+v", logs.Items[0])
	}

	// 重复事件（重复支付回调）：不重复增加成长值、不重复写流水。
	accrual(t, order1)
	if growth := currentGrowth(t, userID); growth != 1000 {
		t.Fatalf("重复发放后成长值期望仍为 1000，实际 %d", growth)
	}
	if cnt, _ := g.DB().Model("member_growth_logs").Ctx(ctx).Where("user_id", userID).Count(); cnt != 1 {
		t.Fatalf("重复发放不应新增流水，实际 %d 行", cnt)
	}

	// 第二笔订单跨级升级到黄金（1000+4000=5000）。
	order2 := insertPaidOrder(t, userID, 4000, 0, 0)
	accrual(t, order2)
	my, err = m.My(ctx, userID)
	if err != nil {
		t.Fatalf("My: %v", err)
	}
	if my.GrowthValue != 5000 || my.LevelNo == nil || *my.LevelNo != 3 {
		t.Fatalf("跨级升级结果不符: growth=%d level=%v", my.GrowthValue, my.LevelNo)
	}
	// 已达最高等级：无下一等级。
	if my.NextLevelId != nil || my.NextThreshold != nil {
		t.Fatalf("最高等级不应有下一等级: %v/%v", my.NextLevelId, my.NextThreshold)
	}

	// 未支付订单不发放成长值。
	orderUnpaid := insertOrderWithStatus(t, userID, 999, 0, 0, 999, 10)
	accrual(t, orderUnpaid)
	if growth := currentGrowth(t, userID); growth != 5000 {
		t.Fatalf("未支付订单不应发放成长值，实际 %d", growth)
	}
}

// TestGrowthRefundDeductAndNoDowngrade 覆盖退款扣减成长值、幂等与「等级只升不降」。
func TestGrowthRefundDeductAndNoDowngrade(t *testing.T) {
	setupMemberTest(t)
	m := memberService()
	ctx := context.Background()

	createLevel(t, 1, "青铜", 0, 9800, v1.LevelStatusEnabled)
	createLevel(t, 2, "白银", 1000, 9500, v1.LevelStatusEnabled)

	userID := insertMemberTestUser(t, "member-refund", 1)
	orderID := insertPaidOrder(t, userID, 1000, 0, 0)
	accrual(t, orderID)

	if err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		return m.DeductForRefundInTx(ctx, tx, orderID, 500001)
	}); err != nil {
		t.Fatalf("退款扣减成长值: %v", err)
	}

	my, err := m.My(ctx, userID)
	if err != nil {
		t.Fatalf("My: %v", err)
	}
	if my.GrowthValue != 0 {
		t.Fatalf("退款后成长值期望 0，实际 %d", my.GrowthValue)
	}
	// 等级只升不降：仍保留白银。
	if my.LevelNo == nil || *my.LevelNo != 2 {
		t.Fatalf("退款不应降级，期望 level_no=2，实际 %v", my.LevelNo)
	}
	if my.DiscountRateBp != 9500 {
		t.Fatalf("退款后仍应按当前等级折扣 9500，实际 %d", my.DiscountRateBp)
	}

	// 同一退款重复扣减：幂等（1062），不产生第二条流水，不出现负成长值。
	if err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		return m.DeductForRefundInTx(ctx, tx, orderID, 500001)
	}); err != nil {
		t.Fatalf("重复退款扣减应幂等: %v", err)
	}
	if growth := currentGrowth(t, userID); growth != 0 {
		t.Fatalf("重复扣减后成长值期望 0，实际 %d", growth)
	}
	deductLogs, err := g.DB().Model("member_growth_logs").Ctx(ctx).
		Where("user_id", userID).Where("change_type", changeTypeRefundDeduct).Count()
	if err != nil {
		t.Fatalf("统计扣减流水: %v", err)
	}
	if deductLogs != 1 {
		t.Fatalf("扣减流水期望 1 条，实际 %d", deductLogs)
	}

	// 订单未发放过成长值：退款扣减 no-op，不伪造流水。
	other := insertPaidOrder(t, userID, 300, 0, 0)
	if err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		return m.DeductForRefundInTx(ctx, tx, other, 500002)
	}); err != nil {
		t.Fatalf("未发放订单退款扣减应 no-op: %v", err)
	}
}

// TestGrowthConcurrentAccrual 覆盖同一用户并发发放成长值：余额与流水必须逐笔自洽。
func TestGrowthConcurrentAccrual(t *testing.T) {
	setupMemberTest(t)
	m := memberService()
	ctx := context.Background()

	createLevel(t, 1, "青铜", 0, 9800, v1.LevelStatusEnabled)
	createLevel(t, 2, "白银", 500, 9500, v1.LevelStatusEnabled)

	userID := insertMemberTestUser(t, "member-concurrent", 1)
	const n = 8
	orders := make([]int64, n)
	for i := 0; i < n; i++ {
		orders[i] = insertPaidOrder(t, userID, 100, 0, 0)
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = g.DB().Transaction(context.Background(), func(ctx context.Context, tx gdb.TX) error {
				return m.AccrueForOrderInTx(ctx, tx, orders[idx])
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发发放 #%d 失败: %v", i, err)
		}
	}

	// 余额 = 全部订单之和；流水条数 = n，且每条 after = before + change。
	if growth := currentGrowth(t, userID); growth != 100*n {
		t.Fatalf("并发发放后成长值期望 %d，实际 %d", 100*n, growth)
	}
	logs, err := m.GrowthLogs(ctx, userID, &v1.GrowthLogsReq{Page: 1, Size: 100})
	if err != nil {
		t.Fatalf("GrowthLogs: %v", err)
	}
	if logs.Total != n {
		t.Fatalf("流水分期望 %d，实际 %d", n, logs.Total)
	}
	var sum int64
	for _, l := range logs.Items {
		if l.GrowthAfter != l.GrowthBefore+l.ChangeAmount {
			t.Fatalf("流水前后值不自洽: %+v", l)
		}
		if l.GrowthAfter < 0 {
			t.Fatalf("成长值不应为负: %+v", l)
		}
		sum += l.ChangeAmount
	}
	if sum != growthOf(t, userID) {
		t.Fatalf("流水合计 %d 与余额 %d 不一致", sum, growthOf(t, userID))
	}
	// 一次跨越 500 门槛后应升到白银。
	my, err := m.My(ctx, userID)
	if err != nil {
		t.Fatalf("My: %v", err)
	}
	if my.LevelNo == nil || *my.LevelNo != 2 {
		t.Fatalf("成长值 800 应升到 level_no=2，实际 %v", my.LevelNo)
	}
}

// TestAccrualSkipsWhenLevelNotConfigured 覆盖平台未配置等级时的 no-op 语义。
func TestAccrualSkipsWhenLevelNotConfigured(t *testing.T) {
	setupMemberTest(t)
	m := memberService()
	ctx := context.Background()

	userID := insertMemberTestUser(t, "member-nolevel", 1)
	orderID := insertPaidOrder(t, userID, 1000, 0, 0)

	if err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		return m.AccrueForOrderInTx(ctx, tx, orderID)
	}); err != nil {
		t.Fatalf("未配置等级时应 no-op 而不是报错: %v", err)
	}
	if cnt, _ := g.DB().Model("user_memberships").Ctx(ctx).Where("user_id", userID).Count(); cnt != 0 {
		t.Fatalf("未配置等级时不应建档，实际 %d 行", cnt)
	}
	if cnt, _ := g.DB().Model("member_growth_logs").Ctx(ctx).Where("user_id", userID).Count(); cnt != 0 {
		t.Fatalf("未配置等级时不应写流水，实际 %d 行", cnt)
	}
}

// TestGrowthLogsPagination 覆盖成长值流水分页与越界归一。
func TestGrowthLogsPagination(t *testing.T) {
	setupMemberTest(t)
	m := memberService()
	ctx := context.Background()

	createLevel(t, 1, "青铜", 0, 9800, v1.LevelStatusEnabled)
	userID := insertMemberTestUser(t, "member-pages", 1)
	for i := 0; i < 3; i++ {
		accrual(t, insertPaidOrder(t, userID, 10, 0, 0))
	}

	first, err := m.GrowthLogs(ctx, userID, &v1.GrowthLogsReq{Page: 0, Size: 0})
	if err != nil {
		t.Fatalf("GrowthLogs(默认): %v", err)
	}
	if first.Page != defaultPage || first.Size != defaultSize || first.Total != 3 || len(first.Items) != 3 {
		t.Fatalf("默认分页不符: %+v", first)
	}
	// 按 id 倒序：第一条是最新流水。
	if first.Items[0].GrowthAfter != 30 {
		t.Fatalf("期望按 id 倒序（最新 after=30），实际 %d", first.Items[0].GrowthAfter)
	}
	clamped, err := m.GrowthLogs(ctx, userID, &v1.GrowthLogsReq{Page: 1, Size: 1000})
	if err != nil {
		t.Fatalf("GrowthLogs(越界): %v", err)
	}
	if clamped.Size != maxSize {
		t.Fatalf("size 越界应截断到 %d，实际 %d", maxSize, clamped.Size)
	}
	// 他人看不到我的流水。
	other := insertMemberTestUser(t, "member-pages-other", 1)
	otherLogs, err := m.GrowthLogs(ctx, other, &v1.GrowthLogsReq{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("GrowthLogs(他人): %v", err)
	}
	if otherLogs.Total != 0 {
		t.Fatalf("他人流水应为 0，实际 %d", otherLogs.Total)
	}
}

// TestLevelSnapshotSurvivesDisable 覆盖「等级停用不影响已入会用户折扣」的快照语义。
func TestLevelSnapshotSurvivesDisable(t *testing.T) {
	setupMemberTest(t)
	m := memberService()
	ctx := context.Background()

	l1 := createLevel(t, 1, "青铜", 0, 9800, v1.LevelStatusEnabled)
	userID := insertMemberTestUser(t, "member-disable", 1)
	accrual(t, insertPaidOrder(t, userID, 500, 0, 0))

	// 停用该等级：已入会用户仍按档案等级享受折扣，不会被静默降级为原价。
	if _, err := m.AdminUpdateLevel(ctx, &v1.AdminUpdateLevelReq{Id: l1.Id, LevelNo: 1, Name: "青铜", GrowthThreshold: 0, DiscountRateBp: 9800, Status: v1.LevelStatusDisabled}); err != nil {
		t.Fatalf("停用等级: %v", err)
	}
	rate, err := m.DiscountRateBp(ctx, userID)
	if err != nil {
		t.Fatalf("DiscountRateBp: %v", err)
	}
	if rate != 9800 {
		t.Fatalf("已入会用户折扣率期望 9800，实际 %d", rate)
	}
	// 未建档用户：没有启用等级可用 → 不打折。
	newUser := insertMemberTestUser(t, "member-disable-new", 1)
	rate, err = m.DiscountRateBp(ctx, newUser)
	if err != nil {
		t.Fatalf("DiscountRateBp(新用户): %v", err)
	}
	if rate != noDiscountRateBp {
		t.Fatalf("无启用等级时新用户折扣率期望 %d，实际 %d", noDiscountRateBp, rate)
	}
}

// currentGrowth 读取用户当前成长值（无档案返回 0）。
func currentGrowth(t *testing.T, userID int64) int64 {
	t.Helper()
	return growthOf(t, userID)
}

// growthOf 读取用户成长值，不存在返回 0。
func growthOf(t *testing.T, userID int64) int64 {
	t.Helper()
	var rows []*membershipRow
	if err := g.DB().Model("user_memberships").Ctx(context.Background()).Where("user_id", userID).Scan(&rows); err != nil {
		t.Fatalf("读取会员档案: %v", err)
	}
	if len(rows) == 0 {
		return 0
	}
	return rows[0].GrowthValue
}
