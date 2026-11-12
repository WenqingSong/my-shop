package concurrency

// 4.5 优惠券领取并发测试。
//
// 被测实现：internal/logic/coupon.Claim（真实 MySQL 事务）：
//   - 原子闸门：UPDATE coupon_templates SET issued_quantity = issued_quantity + 1
//     WHERE id=? AND status=1 AND issued_quantity < total_quantity AND 时间窗命中；
//   - 唯一约束：user_coupons.uk_user_template_claim(user_id, template_id, claim_no)，
//     默认 claim_no=1，即「同一用户同一模板默认只能领一次」；
//   - 插入撞唯一键 → 返回 19004 并整体回滚，使 issued_quantity 的 +1 一并回滚。
//
// 本文件验证的业务不变量：
//   CP-1 不允许超发：成功领取数 ≤ total_quantity，issued_quantity == 实际用户券数；
//   CP-2 单用户限领：同一用户并发领取只成功一次，失败请求不得虚增 issued_quantity；
//   CP-3 失败请求不产生领取记录，券与用户一一对应；
//   CP-4 数据库唯一键是并发重复领取的最终防线（绕过服务直插 claim_no=1 同样被拒）。
//
// 已知基线缺口（不在本任务修改生产代码）：coupon_batches（发行批次）在 B1-03-03 建表，
// 但当前 Claim 只走「模板级总量闸门」，没有批次级领取入口；因此本文件覆盖的是实现中
// 真实存在的模板闸门 + 唯一约束路径，批次级限制作为遗留问题记录在测试报告中。

import (
	"context"
	"testing"

	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// couponTemplate 建立启用且在领取时间窗内的优惠券模板，返回模板 id。
func couponTemplate(t *testing.T, totalQuantity int64) int64 {
	t.Helper()
	templateID, err := g.DB().Model("coupon_templates").Ctx(context.Background()).Data(g.Map{
		"name":             "并发券-" + uniqueSuffix(),
		"threshold_amount": 10000,
		"discount_amount":  1000,
		"total_quantity":   totalQuantity,
		"issued_quantity":  0,
		"per_user_limit":   1,
		"claim_start_time": mysqlNowOffset(-60),
		"claim_end_time":   mysqlNowOffset(60),
		"use_start_time":   mysqlNowOffset(-60),
		"use_end_time":     mysqlNowOffset(1440),
		"status":           1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("插入优惠券模板失败: %v", err)
	}
	return templateID
}

// couponIssuedQuantity 读取模板已发放数量。
func couponIssuedQuantity(t *testing.T, templateID int64) int64 {
	t.Helper()
	value, err := g.DB().Model("coupon_templates").Ctx(context.Background()).
		Where("id", templateID).Value("issued_quantity")
	if err != nil {
		t.Fatalf("查询已发放数量失败: %v", err)
	}
	if value == nil || value.IsNil() {
		return 0
	}
	return value.Int64()
}

// countUserCoupons 统计模板下真实落库的用户券数量。
func countUserCoupons(t *testing.T, templateID int64) int {
	t.Helper()
	n, err := g.DB().Model("user_coupons").Ctx(context.Background()).
		Where("template_id", templateID).Count()
	if err != nil {
		t.Fatalf("统计用户券失败: %v", err)
	}
	return n
}

// TestCouponConcurrentClaimNoOversell 覆盖 CP-1/CP-3：
// 发行量 5，30 个不同用户并发抢券：恰好 5 人成功，其余 19006；
// issued_quantity 与实际用户券数都等于 5，不存在超发或幽灵券。
func TestCouponConcurrentClaimNoOversell(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		totalQuantity = int64(5)
		claimers      = 30
	)
	templateID := couponTemplate(t, totalQuantity)

	userIDs := make([]int64, claimers)
	for i := 0; i < claimers; i++ {
		userIDs[i] = newTestUser(t, "coupon-oversell")
	}

	errs := make([]error, claimers)
	runConcurrently(claimers, func(i int) {
		_, err := service.Coupon().Claim(ctx, userIDs[i], templateID)
		errs[i] = err
	})

	var successes, soldOut int
	successUsers := make([]int64, 0, totalQuantity)
	for i, err := range errs {
		if err == nil {
			successes++
			successUsers = append(successUsers, userIDs[i])
			continue
		}
		if code := codes.FromError(err); code != codes.CodeCouponClaimSoldOut {
			t.Fatalf("第 %d 个领取请求非预期错误码 %d: %v", i, code, err)
		}
		soldOut++
	}
	if successes != int(totalQuantity) {
		t.Fatalf("成功领取 %d 张，期望恰好 %d 张（超发或少发）", successes, totalQuantity)
	}
	if soldOut != claimers-successes {
		t.Fatalf("已领完拒绝 %d 次，期望 %d 次", soldOut, claimers-successes)
	}

	if issued := couponIssuedQuantity(t, templateID); issued != totalQuantity {
		t.Fatalf("issued_quantity=%d，期望 %d", issued, totalQuantity)
	}
	if n := countUserCoupons(t, templateID); n != int(totalQuantity) {
		t.Fatalf("用户券=%d 张，期望 %d 张（剩余数量与领取记录不一致）", n, totalQuantity)
	}

	// 成功者与落库券必须一一对应，且券状态为可用（10）。
	for _, userID := range successUsers {
		n, err := g.DB().Model("user_coupons").Ctx(ctx).
			Where("user_id", userID).Where("template_id", templateID).Where("status", 10).Count()
		if err != nil {
			t.Fatalf("查询用户券失败: %v", err)
		}
		if n != 1 {
			t.Fatalf("成功用户 %d 的可用券=%d 张，期望 1 张", userID, n)
		}
	}
}

// TestCouponConcurrentClaimSameUserSingleCoupon 覆盖 CP-2/CP-3：
// 同一用户并发领取 10 次（总量充足）：只成功 1 次，其余 19004；
// issued_quantity 必须回落到 1（失败请求的 +1 随事务回滚，不得虚增发放量）。
func TestCouponConcurrentClaimSameUserSingleCoupon(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		totalQuantity = int64(100)
		attempts      = 10
	)
	templateID := couponTemplate(t, totalQuantity)
	userID := newTestUser(t, "coupon-same-user")

	errs := make([]error, attempts)
	runConcurrently(attempts, func(i int) {
		_, err := service.Coupon().Claim(ctx, userID, templateID)
		errs[i] = err
	})

	var successes int
	for i, err := range errs {
		if err == nil {
			successes++
			continue
		}
		if code := codes.FromError(err); code != codes.CodeCouponAlreadyClaimed {
			t.Fatalf("第 %d 次重复领取非预期错误码 %d: %v", i, code, err)
		}
	}
	if successes != 1 {
		t.Fatalf("同一用户成功领取 %d 次，期望 1 次（突破单用户限领）", successes)
	}
	if issued := couponIssuedQuantity(t, templateID); issued != 1 {
		t.Fatalf("issued_quantity=%d，期望 1（失败领取虚增了发放量）", issued)
	}
	if n := countUserCoupons(t, templateID); n != 1 {
		t.Fatalf("用户券=%d 张，期望 1 张", n)
	}
}

// TestCouponDuplicateClaimKeepsIssuedQuantity 覆盖 CP-2/CP-4：
// 串行重复领取返回 19004 且 issued_quantity 不变；
// 绕过服务直接插入 claim_no=1 的第二张券会被 uk_user_template_claim 拒绝（1062），
// 说明「单用户限领」不仅有应用层判断，还有数据库唯一约束兜底。
func TestCouponDuplicateClaimKeepsIssuedQuantity(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	templateID := couponTemplate(t, 10)
	userID := newTestUser(t, "coupon-duplicate")

	if _, err := service.Coupon().Claim(ctx, userID, templateID); err != nil {
		t.Fatalf("首次领取失败: %v", err)
	}
	if issued := couponIssuedQuantity(t, templateID); issued != 1 {
		t.Fatalf("首次领取后 issued_quantity=%d，期望 1", issued)
	}

	_, err := service.Coupon().Claim(ctx, userID, templateID)
	if code := codes.FromError(err); code != codes.CodeCouponAlreadyClaimed {
		t.Fatalf("重复领取错误码=%d，期望 %d: %v", code, codes.CodeCouponAlreadyClaimed, err)
	}
	if issued := couponIssuedQuantity(t, templateID); issued != 1 {
		t.Fatalf("重复领取后 issued_quantity=%d，期望 1", issued)
	}
	if n := countUserCoupons(t, templateID); n != 1 {
		t.Fatalf("重复领取后用户券=%d 张，期望 1 张", n)
	}

	// 数据库唯一约束兜底：绕过服务直插同一 (user_id, template_id, claim_no=1) 必须失败。
	if _, err := g.DB().Exec(ctx,
		"INSERT INTO user_coupons (user_id, template_id, claim_no, status, received_at) VALUES (?, ?, 1, 10, NOW())",
		userID, templateID,
	); err == nil {
		t.Fatalf("绕过服务插入重复券成功，uk_user_template_claim 未生效")
	}
}
