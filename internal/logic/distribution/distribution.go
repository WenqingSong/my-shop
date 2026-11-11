// Package distribution 实现一级分销（Distribution）：
// 推荐关系绑定、订单支付时计提一级佣金、退款成功时冲回佣金，以及用户端/后台只读查询。
//
// 边界（见 docs/design/distribution.md）：
//   - 只做一级：佣金只给买家的直接推荐人，不存在多级链；
//   - 佣金记录是应收/已冲回事实，不写入钱包，不实现提现与结算任务；
//   - 事实来源为单一 MySQL，计佣与冲回都在调用方已有事务内完成（不自开事务）。
package distribution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/distribution/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 佣金状态（distribution_commissions.status，见 docs/design/distribution.md §3.3）。
const (
	commissionStatusPending  = 10 // 待结算
	commissionStatusSettled  = 20 // 已结算
	commissionStatusReversed = 30 // 已冲回（终态）
)

// 可被退款冲回的状态集合：终态 30 不在其中，重复冲回自然 0 行。
var reversibleStatuses = []int{commissionStatusPending, commissionStatusSettled}

// 跨模块枚举（订单/用户模块未导出常量，按设计文档固化，避免造成包依赖）。
const (
	orderStatusPaid   = 20 // orders.status 已支付
	userStatusEnabled = 1  // users.status 启用
)

// 计佣公式常量：commission_amount = base_amount × rate_bp / 10000（向下取整到分）。
const (
	commissionScale = 10000
	maxRateBp       = 10000
)

// maxReferralChainDepth 是推荐关系向上遍历的深度上限。
// 一级分销下每个用户至多一个上级，正常数据深度很小；上限只用于防御脏数据导致遍历不终止。
const maxReferralChainDepth = 200

// errReferralDuplicate 表示绑定推荐关系命中 uk_user_id（并发下另一请求已先绑定）。
// 事务内不做「回读 + 判定」，而是整体回滚后在新快照中判定，避免 RR 快照读不到已提交行。
var errReferralDuplicate = errors.New("distribution: referral already bound")

// reversalReasonRefund 是退款触发的冲回原因（distribution_commissions.reversal_reason）。
const reversalReasonRefund = "refund"

// 分页默认值/上限。
const (
	defaultPage = 1
	defaultSize = 20
	maxPageSize = 100
)

type sDistribution struct{}

func init() {
	service.RegisterDistribution(New())
}

// New 创建并返回一级分销服务实现。
func New() *sDistribution {
	return &sDistribution{}
}

// referralRow 是 user_referrals 表的一条记录。
type referralRow struct {
	Id             int64       `json:"id"`
	UserId         int64       `json:"user_id"`
	ReferrerUserId int64       `json:"referrer_user_id"`
	CreatedAt      *gtime.Time `json:"created_at"`
}

// commissionRow 是 distribution_commissions 表的一条记录。
type commissionRow struct {
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
	CreatedAt         *gtime.Time `json:"created_at"`
	UpdatedAt         *gtime.Time `json:"updated_at"`
}

// BindReferral 绑定当前登录用户的上级推荐人。
//
// 不变量：
//   - 不能自荐（20001）；
//   - 推荐人必须存在且 status=1（20002）；
//   - 一人一个上级：同人重复提交幂等返回既有关系，换人返回 20003（不可改绑）；
//   - 不成环（20004）：从推荐人向上遍历，回到本人即成环（吸收 B1-04-08 的环校验）；
//   - 并发安全：绑定在单事务内完成，先按 user_id 升序锁定双方推荐关系行（不存在的行由
//     唯一索引间隙锁兜底），写入后做写后环检测；uk_user_id 仍是最终仲裁，
//     1062 时以已落库关系为准判定幂等/冲突。
func (s *sDistribution) BindReferral(ctx context.Context, userID, referrerUserID int64) (*v1.BindReferralRes, error) {
	if userID <= 0 || referrerUserID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if userID == referrerUserID {
		return nil, codes.New(codes.CodeDistributionSelfReferral)
	}

	referrerCount, err := g.DB().Model("users").Ctx(ctx).
		Where("id", referrerUserID).
		Where("status", userStatusEnabled).
		Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("校验推荐人可用性: %w", err))
	}
	if referrerCount == 0 {
		return nil, codes.New(codes.CodeDistributionReferrerUnavailable)
	}

	var bound *v1.Referral
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// 不使用 SELECT ... FOR UPDATE：不存在的行在 REPEATABLE READ 下会加间隙锁，
		// 并发绑定同一用户到不同推荐人时容易死锁。并发仲裁交给 uk_user_id：
		// 只有一个事务能插入成功，其余事务命中 1062 后结束本事务，由调用方在新快照中判定幂等/冲突。
		existing, e := s.findReferralInTx(ctx, tx, userID)
		if e != nil {
			return e
		}
		if existing != nil {
			res, e := s.existingReferralResult(existing, referrerUserID)
			if e != nil {
				return e
			}
			bound = &res.Referral
			return nil
		}

		if _, e := tx.Model("user_referrals").Ctx(ctx).Data(g.Map{
			"user_id":          userID,
			"referrer_user_id": referrerUserID,
		}).Insert(); e != nil {
			if duplicateKeyName(e) != "" {
				return errReferralDuplicate
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("绑定推荐关系: %w", e))
		}

		// 写后环检测：本事务内已可见新关系，从本人向上遍历，回到本人即成环并整体回滚。
		if e := s.requireAcyclicInTx(ctx, tx, userID); e != nil {
			return e
		}

		row, e := s.findReferralInTx(ctx, tx, userID)
		if e != nil {
			return e
		}
		if row == nil {
			return codes.Wrap(codes.CodeInternalError, errors.New("绑定推荐关系后未找到记录"))
		}
		bound = row
		return nil
	})
	// 唯一键冲突：本事务已回滚，重新以新快照读取并发落库的关系再判定幂等/改绑冲突。
	if errors.Is(err, errReferralDuplicate) {
		concurrent, e := s.findReferral(ctx, userID)
		if e != nil {
			return nil, e
		}
		if concurrent == nil {
			return nil, codes.Wrap(codes.CodeInternalError, errors.New("推荐关系唯一冲突后未找到记录"))
		}
		return s.existingReferralResult(concurrent, referrerUserID)
	}
	if err != nil {
		return nil, err
	}
	if bound == nil {
		return nil, codes.Wrap(codes.CodeInternalError, errors.New("绑定推荐关系后未找到结果"))
	}
	return &v1.BindReferralRes{Referral: *bound}, nil
}

// requireAcyclicInTx 在事务内校验推荐关系不成环：从 userID 沿 referrer 向上遍历，
// 回到 userID 即成环。一级分销下每层只有一条关系，深度上限只用于防御脏数据导致死循环。
func (s *sDistribution) requireAcyclicInTx(ctx context.Context, tx gdb.TX, userID int64) error {
	seen := map[int64]bool{userID: true}
	current := userID
	for depth := 0; depth <= maxReferralChainDepth; depth++ {
		row, err := s.findReferralInTx(ctx, tx, current)
		if err != nil {
			return err
		}
		if row == nil || row.ReferrerUserId <= 0 {
			return nil
		}
		if seen[row.ReferrerUserId] {
			return codes.New(codes.CodeDistributionCycle)
		}
		seen[row.ReferrerUserId] = true
		current = row.ReferrerUserId
	}
	return codes.New(codes.CodeDistributionCycle)
}

// existingReferralResult 处理「已存在推荐关系」：同一推荐人幂等返回，不同推荐人拒绝改绑。
func (s *sDistribution) existingReferralResult(existing *v1.Referral, referrerUserID int64) (*v1.BindReferralRes, error) {
	if existing.ReferrerUserId == referrerUserID {
		return &v1.BindReferralRes{Referral: *existing}, nil
	}
	return nil, codes.New(codes.CodeDistributionAlreadyBound)
}

// MyInfo 查询我的分销信息（上级关系 + 直接下级人数 + 佣金汇总）。
func (s *sDistribution) MyInfo(ctx context.Context, userID int64) (*v1.MyInfoRes, error) {
	if userID <= 0 {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	referral, err := s.findReferral(ctx, userID)
	if err != nil {
		return nil, err
	}
	subordinateCount, err := g.DB().Model("user_referrals").Ctx(ctx).
		Where("referrer_user_id", userID).Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计直接下级: %w", err))
	}
	summary, err := s.commissionSummary(ctx, userID)
	if err != nil {
		return nil, err
	}
	res := &v1.MyInfoRes{
		UserId:           userID,
		SubordinateCount: subordinateCount,
		Commission:       *summary,
	}
	if referral != nil {
		res.Referrer = referral
	}
	return res, nil
}

// ListSubordinates 分页查询我的直接下级（按关系 id 倒序，即后绑定在前）。
func (s *sDistribution) ListSubordinates(ctx context.Context, userID int64, req *v1.ListSubordinatesReq) (*v1.ListSubordinatesRes, error) {
	if userID <= 0 {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	page, size := clampPageParams(req.Page, req.Size)
	model := g.DB().Model("user_referrals").Ctx(ctx).Where("referrer_user_id", userID)
	total, err := model.Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计直接下级: %w", err))
	}
	var rows []*referralRow
	if err := model.Order("id DESC").Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询直接下级: %w", err))
	}

	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserId)
	}
	names, err := s.usernames(ctx, ids)
	if err != nil {
		return nil, err
	}
	items := make([]*v1.Subordinate, 0, len(rows))
	for _, r := range rows {
		items = append(items, &v1.Subordinate{
			UserId:   r.UserId,
			Username: names[r.UserId],
			BoundAt:  r.CreatedAt,
		})
	}
	return &v1.ListSubordinatesRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// ListMyCommissions 分页查询我作为受益推荐人的佣金记录（按 id 倒序）。
func (s *sDistribution) ListMyCommissions(ctx context.Context, userID int64, req *v1.ListCommissionsReq) (*v1.ListCommissionsRes, error) {
	if userID <= 0 {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	page, size := clampPageParams(req.Page, req.Size)
	model := g.DB().Model("distribution_commissions").Ctx(ctx).Where("beneficiary_user_id", userID)
	if status := strings.TrimSpace(req.Status); status != "" {
		st, err := commissionStatusFromText(status)
		if err != nil {
			return nil, err
		}
		model = model.Where("status", st)
	}
	total, err := model.Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计佣金记录: %w", err))
	}
	var rows []*commissionRow
	if err := model.Order("id DESC").Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询佣金记录: %w", err))
	}
	items := make([]*v1.Commission, 0, len(rows))
	for _, r := range rows {
		items = append(items, toCommission(r))
	}
	return &v1.ListCommissionsRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// AdminListReferrals 后台分页查询分销关系（支持 user_id/referrer_user_id 精确过滤）。
func (s *sDistribution) AdminListReferrals(ctx context.Context, req *v1.AdminListReferralsReq) (*v1.AdminListReferralsRes, error) {
	page, size, err := strictPageParams(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	model := g.DB().Model("user_referrals").Ctx(ctx)
	if req.UserId != nil {
		if *req.UserId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		model = model.Where("user_id", *req.UserId)
	}
	if req.ReferrerUserId != nil {
		if *req.ReferrerUserId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		model = model.Where("referrer_user_id", *req.ReferrerUserId)
	}
	total, err := model.Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计分销关系: %w", err))
	}
	var rows []*referralRow
	if err := model.Order("id DESC").Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询分销关系: %w", err))
	}
	items := make([]*v1.Referral, 0, len(rows))
	for _, r := range rows {
		items = append(items, toReferral(r))
	}
	return &v1.AdminListReferralsRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// AdminListDistributors 后台按推荐人维度汇总分销用户：
// 直接下级人数来自 user_referrals 分组，佣金合计来自 distribution_commissions 分组（两段查询，避免 N+1）。
func (s *sDistribution) AdminListDistributors(ctx context.Context, req *v1.AdminListDistributorsReq) (*v1.AdminListDistributorsRes, error) {
	page, size, err := strictPageParams(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	total, err := g.DB().GetCount(ctx, "SELECT COUNT(DISTINCT referrer_user_id) FROM user_referrals")
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计推荐人总数: %w", err))
	}

	var rows []struct {
		ReferrerUserId   int64       `json:"referrer_user_id"`
		SubordinateCount int         `json:"subordinate_count"`
		FirstBoundAt     *gtime.Time `json:"first_bound_at"`
	}
	err = g.DB().Model("user_referrals").Ctx(ctx).
		Fields("referrer_user_id", "COUNT(*) AS subordinate_count", "MIN(created_at) AS first_bound_at").
		Group("referrer_user_id").
		Order("referrer_user_id ASC").
		Page(page, size).
		Scan(&rows)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询分销用户汇总: %w", err))
	}

	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ReferrerUserId)
	}
	agg, err := s.commissionAggByBeneficiary(ctx, ids)
	if err != nil {
		return nil, err
	}
	items := make([]*v1.Distributor, 0, len(rows))
	for _, r := range rows {
		item := &v1.Distributor{
			ReferrerUserId:   r.ReferrerUserId,
			SubordinateCount: r.SubordinateCount,
			FirstBoundAt:     r.FirstBoundAt,
		}
		if a, ok := agg[r.ReferrerUserId]; ok {
			item.CommissionCount = a.Count
			item.CommissionAmount = a.Amount
			item.ReversedAmount = a.ReversedAmount
		}
		items = append(items, item)
	}
	return &v1.AdminListDistributorsRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// AdminListCommissions 后台分页查询佣金记录（order_id/buyer/beneficiary/status 组合过滤）。
func (s *sDistribution) AdminListCommissions(ctx context.Context, req *v1.AdminListCommissionsReq) (*v1.AdminListCommissionsRes, error) {
	page, size, err := strictPageParams(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	model := g.DB().Model("distribution_commissions").Ctx(ctx)
	if req.OrderId != nil {
		if *req.OrderId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		model = model.Where("order_id", *req.OrderId)
	}
	if req.BuyerUserId != nil {
		if *req.BuyerUserId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		model = model.Where("buyer_user_id", *req.BuyerUserId)
	}
	if req.BeneficiaryUserId != nil {
		if *req.BeneficiaryUserId <= 0 {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		model = model.Where("beneficiary_user_id", *req.BeneficiaryUserId)
	}
	if status := strings.TrimSpace(req.Status); status != "" {
		st, e := commissionStatusFromText(status)
		if e != nil {
			return nil, e
		}
		model = model.Where("status", st)
	}
	total, err := model.Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计佣金记录: %w", err))
	}
	var rows []*commissionRow
	if err := model.Order("id DESC").Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询佣金记录: %w", err))
	}
	items := make([]*v1.Commission, 0, len(rows))
	for _, r := range rows {
		items = append(items, toCommission(r))
	}
	return &v1.AdminListCommissionsRes{Items: items, Total: total, Page: page, Size: size}, nil
}

// AdminSettleCommission 人工结算佣金：pending(10) → settled(20) 并写 settled_at。
//
// 边界（见 docs/design/distribution.md §3.4）：
//   - 只做状态流转，**不代表真实资金转账**，V1 不产生任何钱包流水或打款记录；
//   - 幂等：已结算直接返回当前记录，不重复写 settled_at；
//   - 已冲回(30) 是终态，返回 20006；记录不存在返回 20005（防枚举）；
//   - 并发安全：先锁佣金行，再用 status=10 条件更新，0 行视为状态已变。
func (s *sDistribution) AdminSettleCommission(ctx context.Context, adminID, commissionID int64) (*v1.AdminSettleCommissionRes, error) {
	if adminID <= 0 {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	if commissionID <= 0 {
		return nil, codes.New(codes.CodeDistributionCommissionNotFound)
	}

	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var rows []*commissionRow
		if e := tx.Model("distribution_commissions").Ctx(ctx).
			Where("id", commissionID).
			LockUpdate().
			Scan(&rows); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定佣金记录: %w", e))
		}
		if len(rows) == 0 {
			return codes.New(codes.CodeDistributionCommissionNotFound)
		}
		switch rows[0].Status {
		case commissionStatusSettled:
			return nil // 幂等：已结算直接返回当前记录
		case commissionStatusPending:
		default:
			return codes.New(codes.CodeDistributionCommissionStatus)
		}
		result, e := tx.Model("distribution_commissions").Ctx(ctx).
			Where("id", commissionID).
			Where("status", commissionStatusPending).
			Data(g.Map{"status": commissionStatusSettled, "settled_at": gdb.Raw("NOW()")}).
			Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("结算佣金: %w", e))
		}
		if affected, e := result.RowsAffected(); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取结算结果: %w", e))
		} else if affected == 0 {
			return codes.New(codes.CodeDistributionCommissionStatus)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var rows []*commissionRow
	if err := g.DB().Model("distribution_commissions").Ctx(ctx).
		Where("id", commissionID).Limit(1).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("回读佣金记录: %w", err))
	}
	if len(rows) == 0 {
		return nil, codes.New(codes.CodeDistributionCommissionNotFound)
	}
	return &v1.AdminSettleCommissionRes{Commission: *toCommission(rows[0])}, nil
}

// AdminCommissionStats 按状态统计佣金；固定返回 pending/settled/reversed 三项（无数据为 0），保证契约稳定。
func (s *sDistribution) AdminCommissionStats(ctx context.Context) (*v1.AdminCommissionStatsRes, error) {
	var rows []struct {
		Status         int   `json:"status"`
		Cnt            int   `json:"cnt"`
		Amount         int64 `json:"amount"`
		ReversedAmount int64 `json:"reversed_amount"`
	}
	err := g.DB().Model("distribution_commissions").Ctx(ctx).
		Fields("status", "COUNT(*) AS cnt",
			"COALESCE(SUM(commission_amount), 0) AS amount",
			"COALESCE(SUM(reversed_amount), 0) AS reversed_amount").
		Group("status").
		Scan(&rows)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计佣金状态: %w", err))
	}
	byStatus := map[int]struct {
		cnt      int
		amount   int64
		reversed int64
	}{}
	for _, r := range rows {
		byStatus[r.Status] = struct {
			cnt      int
			amount   int64
			reversed int64
		}{cnt: r.Cnt, amount: r.Amount, reversed: r.ReversedAmount}
	}
	order := []struct {
		status int
		text   string
	}{
		{commissionStatusPending, v1.CommissionStatusPending},
		{commissionStatusSettled, v1.CommissionStatusSettled},
		{commissionStatusReversed, v1.CommissionStatusReversed},
	}
	items := make([]*v1.CommissionStatsItem, 0, len(order))
	totalCount := 0
	var totalAmount int64
	for _, o := range order {
		v := byStatus[o.status]
		items = append(items, &v1.CommissionStatsItem{
			Status:         o.text,
			Count:          v.cnt,
			Amount:         v.amount,
			ReversedAmount: v.reversed,
		})
		totalCount += v.cnt
		totalAmount += v.amount
	}
	return &v1.AdminCommissionStatsRes{Items: items, TotalCount: totalCount, TotalAmount: totalAmount}, nil
}

// AccrueOrderCommissionInTx 在订单支付成功事务内计提一级佣金（不自开事务）。
//
// 计佣口径（docs/design/distribution.md §3.2）：
//   - 只在订单 status=20（已支付）时计提；
//   - 基数 = orders.total_amount（实付金额快照，整数分）；
//   - 比例 = 配置 distribution.commission_rate_bp（万分比，0=关闭，>10000 视为配置非法）；
//   - 金额 = base × rate / 10000，整数运算向下取整；不足 1 分不计佣；
//   - 买家无直接推荐人 → no-op，不伪造佣金记录；
//   - 幂等：uk_order_id 唯一约束兜底，1062 视为已计提（重复支付/重复回调不产生第二笔）。
func (s *sDistribution) AccrueOrderCommissionInTx(ctx context.Context, tx gdb.TX, orderID int64) error {
	if orderID <= 0 {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("计佣订单 id 非法: %d", orderID))
	}
	var order struct {
		UserId      int64 `json:"user_id"`
		Status      int   `json:"status"`
		TotalAmount int64 `json:"total_amount"`
	}
	if err := tx.Model("orders").Ctx(ctx).
		Fields("user_id", "status", "total_amount").
		Where("id", orderID).
		Scan(&order); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取计佣订单: %w", err))
	}
	if order.UserId <= 0 {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("计佣订单不存在: %d", orderID))
	}
	// 非支付成功状态不计佣（防御：正常调用点已在同一事务内把订单推进到 20）。
	if order.Status != orderStatusPaid {
		return nil
	}

	referral, err := s.findReferralInTx(ctx, tx, order.UserId)
	if err != nil {
		return err
	}
	if referral == nil {
		return nil
	}
	beneficiary := referral.ReferrerUserId
	if beneficiary <= 0 {
		return nil
	}

	rate, err := config.Int(ctx, config.KeyDistributionCommissionRateBP)
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取分销佣金比例: %w", err))
	}
	if rate < 0 || rate > maxRateBp {
		return codes.Wrap(codes.CodeInternalError,
			fmt.Errorf("分销佣金比例配置非法（distribution.commission_rate_bp=%d，允许 0~%d）", rate, maxRateBp))
	}
	if rate == 0 {
		// 0 = 关闭计佣：未配置比例时不产生任何佣金事实。
		return nil
	}

	base := order.TotalAmount
	if base <= 0 {
		return nil
	}
	amount := base * int64(rate) / commissionScale
	if amount <= 0 {
		return nil
	}

	_, err = tx.Model("distribution_commissions").Ctx(ctx).Data(g.Map{
		"order_id":            orderID,
		"buyer_user_id":       order.UserId,
		"beneficiary_user_id": beneficiary,
		"base_amount":         base,
		"rate_bp":             rate,
		"commission_amount":   amount,
		"status":              commissionStatusPending,
	}).Insert()
	if err != nil {
		if duplicateKeyName(err) != "" {
			// uk_order_id：该订单已计提（重复支付/重试），幂等忽略。
			return nil
		}
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("计提订单佣金: %w", err))
	}
	return nil
}

// ReverseOrderCommissionInTx 在退款成功事务内冲回该订单佣金（不自开事务）。
//
// 语义（docs/design/distribution.md §4）：
//   - 只冲回 status ∈ {10,20} 的佣金，条件更新 + RowsAffected 保证只冲一次；
//   - V1 仅整单全额退款：reversed_amount = commission_amount（SQL 内引用原列，无浮点、无差值计算）；
//   - 同一退款单只冲回一条：uk_refund_id 兜底，1062 视为已冲回；
//   - 订单无佣金（无推荐人/未计佣）→ 0 行 no-op，不伪造记录；
//   - status=30 终态不可回退：重复执行 0 行。
func (s *sDistribution) ReverseOrderCommissionInTx(ctx context.Context, tx gdb.TX, refundID, orderID int64) error {
	if refundID <= 0 || orderID <= 0 {
		return codes.Wrap(codes.CodeInternalError,
			fmt.Errorf("佣金冲回参数非法: refund_id=%d order_id=%d", refundID, orderID))
	}
	_, err := tx.Model("distribution_commissions").Ctx(ctx).
		Where("order_id", orderID).
		WhereIn("status", reversibleStatuses).
		Data(g.Map{
			"status":          commissionStatusReversed,
			"reversed_amount": gdb.Raw("commission_amount"),
			"reversed_at":     gdb.Raw("NOW()"),
			"refund_id":       refundID,
			"reversal_reason": reversalReasonRefund,
		}).Update()
	if err != nil {
		if duplicateKeyName(err) != "" {
			// uk_refund_id：同一退款单已冲回过佣金，幂等忽略。
			return nil
		}
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("冲回订单佣金: %w", err))
	}
	return nil
}

// findReferral 查询用户的一级推荐关系；未绑定返回 (nil, nil)。
func (s *sDistribution) findReferral(ctx context.Context, userID int64) (*v1.Referral, error) {
	var rows []*referralRow
	if err := g.DB().Model("user_referrals").Ctx(ctx).
		Where("user_id", userID).Limit(1).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询推荐关系: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return toReferral(rows[0]), nil
}

// findReferralInTx 在调用方事务内查询用户的一级推荐关系。
func (s *sDistribution) findReferralInTx(ctx context.Context, tx gdb.TX, userID int64) (*v1.Referral, error) {
	var rows []*referralRow
	if err := tx.Model("user_referrals").Ctx(ctx).
		Where("user_id", userID).Limit(1).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询推荐关系: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return toReferral(rows[0]), nil
}

// commissionSummary 汇总某受益人各状态的佣金笔数与金额。
func (s *sDistribution) commissionSummary(ctx context.Context, beneficiaryID int64) (*v1.CommissionSummary, error) {
	var rows []struct {
		Status         int   `json:"status"`
		Cnt            int   `json:"cnt"`
		Amount         int64 `json:"amount"`
		ReversedAmount int64 `json:"reversed_amount"`
	}
	err := g.DB().Model("distribution_commissions").Ctx(ctx).
		Fields("status", "COUNT(*) AS cnt",
			"COALESCE(SUM(commission_amount), 0) AS amount",
			"COALESCE(SUM(reversed_amount), 0) AS reversed_amount").
		Where("beneficiary_user_id", beneficiaryID).
		Group("status").
		Scan(&rows)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("汇总佣金: %w", err))
	}
	out := &v1.CommissionSummary{}
	for _, r := range rows {
		switch r.Status {
		case commissionStatusPending:
			out.PendingAmount = r.Amount
			out.PendingCount = r.Cnt
		case commissionStatusSettled:
			out.SettledAmount = r.Amount
			out.SettledCount = r.Cnt
		case commissionStatusReversed:
			out.ReversedAmount = r.ReversedAmount
			out.ReversedCount = r.Cnt
		}
	}
	return out, nil
}

// commissionAgg 是某受益人的佣金聚合结果。
type commissionAgg struct {
	Count          int
	Amount         int64
	ReversedAmount int64
}

// commissionAggByBeneficiary 批量汇总一批受益人的佣金笔数与金额（单条分组查询，避免 N+1）。
func (s *sDistribution) commissionAggByBeneficiary(ctx context.Context, beneficiaryIDs []int64) (map[int64]commissionAgg, error) {
	out := make(map[int64]commissionAgg, len(beneficiaryIDs))
	if len(beneficiaryIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		BeneficiaryUserId int64 `json:"beneficiary_user_id"`
		Cnt               int   `json:"cnt"`
		Amount            int64 `json:"amount"`
		ReversedAmount    int64 `json:"reversed_amount"`
	}
	err := g.DB().Model("distribution_commissions").Ctx(ctx).
		Fields("beneficiary_user_id", "COUNT(*) AS cnt",
			"COALESCE(SUM(commission_amount), 0) AS amount",
			"COALESCE(SUM(reversed_amount), 0) AS reversed_amount").
		WhereIn("beneficiary_user_id", beneficiaryIDs).
		Group("beneficiary_user_id").
		Scan(&rows)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("汇总分销用户佣金: %w", err))
	}
	for _, r := range rows {
		out[r.BeneficiaryUserId] = commissionAgg{
			Count:          r.Cnt,
			Amount:         r.Amount,
			ReversedAmount: r.ReversedAmount,
		}
	}
	return out, nil
}

// usernames 批量查询用户名（仅用于直接下级列表展示）。
func (s *sDistribution) usernames(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		Id       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := g.DB().Model("users").Ctx(ctx).
		Fields("id", "username").WhereIn("id", ids).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询下级用户名: %w", err))
	}
	for _, r := range rows {
		out[r.Id] = r.Username
	}
	return out, nil
}

// toReferral 把内部行转换为 API 实体。
func toReferral(r *referralRow) *v1.Referral {
	return &v1.Referral{
		UserId:         r.UserId,
		ReferrerUserId: r.ReferrerUserId,
		CreatedAt:      r.CreatedAt,
	}
}

// toCommission 把内部行转换为 API 实体。
func toCommission(r *commissionRow) *v1.Commission {
	return &v1.Commission{
		Id:                r.Id,
		OrderId:           r.OrderId,
		BuyerUserId:       r.BuyerUserId,
		BeneficiaryUserId: r.BeneficiaryUserId,
		BaseAmount:        r.BaseAmount,
		RateBp:            r.RateBp,
		CommissionAmount:  r.CommissionAmount,
		Status:            commissionStatusText(r.Status),
		SettledAt:         r.SettledAt,
		ReversedAmount:    r.ReversedAmount,
		ReversedAt:        r.ReversedAt,
		RefundId:          r.RefundId,
		ReversalReason:    r.ReversalReason,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

// commissionStatusText 把 DB 状态映射为 API 字符串枚举。
func commissionStatusText(status int) string {
	switch status {
	case commissionStatusPending:
		return v1.CommissionStatusPending
	case commissionStatusSettled:
		return v1.CommissionStatusSettled
	case commissionStatusReversed:
		return v1.CommissionStatusReversed
	default:
		return ""
	}
}

// commissionStatusFromText 把 API 字符串枚举解析为 DB 状态；非法值返回 400。
func commissionStatusFromText(text string) (int, error) {
	switch text {
	case v1.CommissionStatusPending:
		return commissionStatusPending, nil
	case v1.CommissionStatusSettled:
		return commissionStatusSettled, nil
	case v1.CommissionStatusReversed:
		return commissionStatusReversed, nil
	default:
		return 0, codes.New(codes.CodeInvalidArgument)
	}
}

// clampPageParams 归一用户端分页参数：page<1 → 1；size<1 → 默认值；size>100 → 100。
func clampPageParams(page, size *int) (int, int) {
	p := defaultPage
	if page != nil && *page > 0 {
		p = *page
	}
	sz := defaultSize
	if size != nil {
		if *size > 0 {
			sz = *size
		}
		if sz > maxPageSize {
			sz = maxPageSize
		}
	}
	return p, sz
}

// strictPageParams 严格校验后台分页参数：page<1 或 size<1/size>100 返回 400。
func strictPageParams(page, size *int) (int, int, error) {
	p := defaultPage
	if page != nil {
		if *page < 1 {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		p = *page
	}
	sz := defaultSize
	if size != nil {
		if *size < 1 || *size > maxPageSize {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		sz = *size
	}
	return p, sz, nil
}

// duplicateKeyName 从错误链中提取 MySQL 唯一约束冲突（1062）的消息，非重复键错误返回空串。
func duplicateKeyName(err error) string {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return mysqlErr.Message
	}
	return ""
}
