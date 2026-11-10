// Package distribution 实现「一级分销与佣金管理」业务逻辑：
//   - 推荐关系（user_referrals）：一次性绑定、禁止自荐、禁止成环、不可改绑（一人一行）；
//   - 佣金记录（distribution_commissions）：支付成功按比例计提（一单一条）、后台结算（10→20）、
//     退款成功按原额冲回（10/20→30，同一退款单只冲回一次）；
//   - 管理端只读查询与人工结算。
//
// 边界（见 docs/design/distribution.md）：
//   - 只做一级分销，`user_referrals.referrer_user_id` 就是最终受益人，不存在层级链与多级返佣；
//   - 佣金记录是「应收/已结算事实」，不是可消费余额：不写钱包、不提供提现、不产生资金流水；
//   - 金额一律整数分，比例一律万分比，佣金 = floor(基数 × 比例 / 10000)。
package distribution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/distribution/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// 佣金状态（DB TINYINT）。
	commissionStatusPending  = 10 // 待结算
	commissionStatusSettled  = 20 // 已结算
	commissionStatusReversed = 30 // 已冲回（终态）

	// commissionRateBp 是 V1 佣金比例（万分比，500 = 5%）。
	//
	// V1 固定为常量：比例的配置来源（系统配置/活动配置/商品配置）在
	// docs/design/distribution.md §7 仍为 Deferred，本期不引入配置表/配置项；
	// 写入时作为 rate_bp 快照落库，后续改常量不影响历史佣金。
	commissionRateBp = 500

	// rateBpBase 是万分比基数。
	rateBpBase = 10000

	// orderStatusPaid 是 orders.status 的「已支付」值：只有已支付订单计提佣金。
	orderStatusPaid = 20

	// userStatusEnabled 是 users.status 的启用值：推荐人必须状态正常。
	userStatusEnabled = 1

	// maxReferralChainDepth 是推荐关系向上遍历的深度上限。
	// 一级分销下每个用户至多一个推荐人，正常数据深度很小；上限只用于防御脏数据导致遍历不终止。
	maxReferralChainDepth = 200

	// 分页默认值与上限（clamp 风格用于前台，严格校验用于后台）。
	defaultPage = 1
	defaultSize = 20
	maxSize     = 100

	// 后台分页默认值与上限（strict 风格）。
	adminDefaultPage = 1
	adminDefaultSize = 20
	adminMaxSize     = 100

	// adminDefaultSort 是后台列表默认排序列。
	adminDefaultSort = "id"
)

// adminSortColumns 是后台列表允许的排序字段白名单。
var adminSortColumns = map[string]bool{"id": true, "created_at": true}

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

// commissionRow 是 distribution_commissions 的一条记录。
type commissionRow struct {
	Id                int64       `json:"id"`
	OrderId           int64       `json:"order_id"`
	BuyerUserId       int64       `json:"buyer_user_id"`
	BeneficiaryUserId int64       `json:"beneficiary_user_id"`
	BaseAmount        int64       `json:"base_amount"`
	RateBp            int         `json:"rate_bp"`
	CommissionAmount  int64       `json:"commission_amount"`
	Status            int         `json:"status"`
	SettledAt         *gtime.Time `json:"settled_at"`
	ReversedAmount    int64       `json:"reversed_amount"`
	ReversedAt        *gtime.Time `json:"reversed_at"`
	RefundId          *int64      `json:"refund_id"`
	ReversalReason    string      `json:"reversal_reason"`
	CreatedAt         *gtime.Time `json:"created_at"`
}

// commissionOrderSnapshot 是计提佣金所需的订单金额与身份事实。
type commissionOrderSnapshot struct {
	Id                   int64 `json:"id"`
	UserId               int64 `json:"user_id"`
	Status               int   `json:"status"`
	OriginalAmount       int64 `json:"original_amount"`
	DiscountAmount       int64 `json:"discount_amount"`
	MemberDiscountAmount int64 `json:"member_discount_amount"`
}

// userStateRow 是绑定时校验推荐人状态所需的用户事实。
type userStateRow struct {
	Id       int64  `json:"id"`
	Username string `json:"username"`
	Status   int    `json:"status"`
}

// Bind 绑定一级推荐人。
//
// 不变量（docs/design/distribution.md INV-D02/03/04）：
//   - 禁止自荐：referrer == self → 21003；
//   - 推荐人必须存在且状态启用：否则 21005；
//   - 已绑定不可改绑：同一推荐人重复绑定幂等成功，不同推荐人 → 21002；
//   - 禁止成环：绑定后从本人向上遍历，若回到本人则 21004 并回滚。
//
// 并发：事务内按 id 升序锁定「本人 + 推荐人」两行 users，使涉及同一用户的并发绑定串行化；
// 首次绑定失败/成环时整体回滚，不留下半条关系。
func (s *sDistribution) Bind(ctx context.Context, userID int64, req *v1.BindReq) (*v1.BindRes, error) {
	if userID <= 0 {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	referrerID := req.ReferrerUserId
	if referrerID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if referrerID == userID {
		return nil, codes.New(codes.CodeReferralSelfBind)
	}

	var bound *referralRow
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// 1) 锁定双方用户行（按 id 升序，避免交叉死锁），并在锁内校验推荐人状态。
		var users []*userStateRow
		if e := tx.Model("users").Ctx(ctx).
			Fields("id, username, status").
			WhereIn("id", []int64{userID, referrerID}).
			Order("id ASC").
			LockUpdate().
			Scan(&users); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定用户行用于推荐关系绑定: %w", e))
		}
		var referrerState *userStateRow
		foundSelf := false
		for _, u := range users {
			if u.Id == referrerID {
				referrerState = u
			}
			if u.Id == userID {
				foundSelf = true
			}
		}
		if !foundSelf {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("当前用户 %d 不存在，无法绑定推荐关系", userID))
		}
		if referrerState == nil || referrerState.Status != userStatusEnabled {
			return codes.New(codes.CodeReferralInvalidReferrer)
		}

		// 2) 锁内判断是否已绑定：同一推荐人幂等成功，不同推荐人拒绝改绑。
		var existing []*referralRow
		if e := tx.Model("user_referrals").Ctx(ctx).
			Where("user_id", userID).
			LockUpdate().
			Scan(&existing); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询既有推荐关系: %w", e))
		}
		if len(existing) > 0 {
			if existing[0].ReferrerUserId != referrerID {
				return codes.New(codes.CodeReferralAlreadyBound)
			}
			bound = existing[0]
			return nil
		}

		// 3) 写入关系；并发重复绑定由 uk_user_id 兜底（1062 视为已被并发绑定）。
		if _, e := tx.Model("user_referrals").Ctx(ctx).Data(g.Map{
			"user_id":          userID,
			"referrer_user_id": referrerID,
		}).Insert(); e != nil {
			if !isDuplicateKeyError(e) {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入推荐关系: %w", e))
			}
			var concurrent []*referralRow
			if e := tx.Model("user_referrals").Ctx(ctx).Where("user_id", userID).LockUpdate().Scan(&concurrent); e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("并发绑定后读取推荐关系: %w", e))
			}
			if len(concurrent) == 0 || concurrent[0].ReferrerUserId != referrerID {
				return codes.New(codes.CodeReferralAlreadyBound)
			}
			bound = concurrent[0]
			return nil
		}

		// 4) 成环检测（含并发已提交的关系）：从本人向上遍历，回到本人即成环。
		if e := requireAcyclic(ctx, tx, userID); e != nil {
			return e
		}

		var created []*referralRow
		if e := tx.Model("user_referrals").Ctx(ctx).Where("user_id", userID).Scan(&created); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读回推荐关系: %w", e))
		}
		if len(created) == 0 {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入推荐关系后未找到记录"))
		}
		bound = created[0]
		return nil
	})
	if err != nil {
		return nil, err
	}

	username, err := findUsername(ctx, referrerID)
	if err != nil {
		return nil, err
	}
	logx.Info(ctx, "referral_bound", "推荐关系绑定成功", logx.Fields{
		"user_id":          userID,
		"referrer_user_id": referrerID,
	})
	return &v1.BindRes{Referral: v1.Referral{
		UserId:           bound.UserId,
		ReferrerUserId:   bound.ReferrerUserId,
		ReferrerUsername: username,
		CreatedAt:        bound.CreatedAt,
	}}, nil
}

// MyReferral 查询本人的推荐关系与直接下级数量。
func (s *sDistribution) MyReferral(ctx context.Context, userID int64) (*v1.MyReferralRes, error) {
	out := &v1.MyReferralRes{}
	var rows []*referralRow
	if err := g.DB().Model("user_referrals").Ctx(ctx).Where("user_id", userID).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询本人推荐关系: %w", err))
	}
	if len(rows) > 0 {
		referrerID := rows[0].ReferrerUserId
		out.Bound = true
		out.ReferrerUserId = &referrerID
		out.BoundAt = rows[0].CreatedAt
		username, err := findUsername(ctx, referrerID)
		if err != nil {
			return nil, err
		}
		out.ReferrerUsername = username
	}

	invitees, err := g.DB().Model("user_referrals").Ctx(ctx).Where("referrer_user_id", userID).Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计直接下级数量: %w", err))
	}
	out.InviteeCount = int(invitees)
	return out, nil
}

// MyCommissions 分页查询本人作为受益人的佣金明细（按 id 倒序，联查订单号）。
func (s *sDistribution) MyCommissions(ctx context.Context, userID int64, req *v1.MyCommissionsReq) (*v1.MyCommissionsRes, error) {
	page, size := normalizePage(req.Page, req.Size)
	status, hasStatus, err := parseCommissionStatus(req.Status)
	if err != nil {
		return nil, err
	}

	model := g.DB().Model("distribution_commissions").Ctx(ctx).Where("beneficiary_user_id", userID)
	if hasStatus {
		model = model.Where("status", status)
	}
	total, err := model.Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计本人佣金: %w", err))
	}

	rows, err := s.queryCommissions(ctx, g.DB().Model("distribution_commissions", "c"), func(q *gdb.Model) *gdb.Model {
		q = q.Where("c.beneficiary_user_id", userID)
		if hasStatus {
			q = q.Where("c.status", status)
		}
		return q
	}, "c.id DESC", page, size)
	if err != nil {
		return nil, err
	}
	return &v1.MyCommissionsRes{Items: rows, Total: int(total), Page: page, Size: size}, nil
}

// MyCommissionSummary 查询本人佣金按状态聚合的汇总。
func (s *sDistribution) MyCommissionSummary(ctx context.Context, userID int64) (*v1.MyCommissionSummaryRes, error) {
	var rows []struct {
		Status int   `json:"status"`
		Count  int64 `json:"count"`
		Amount int64 `json:"amount"`
	}
	if err := g.DB().Model("distribution_commissions").Ctx(ctx).
		Fields("status", "COUNT(*) AS count", "COALESCE(SUM(commission_amount), 0) AS amount").
		Where("beneficiary_user_id", userID).
		Group("status").
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计本人佣金汇总: %w", err))
	}

	out := &v1.MyCommissionSummaryRes{}
	for _, r := range rows {
		switch r.Status {
		case commissionStatusPending:
			out.PendingCount = r.Count
			out.PendingAmount = r.Amount
		case commissionStatusSettled:
			out.SettledCount = r.Count
			out.SettledAmount = r.Amount
		case commissionStatusReversed:
			out.ReversedCount = r.Count
			out.ReversedAmount = r.Amount
		}
		out.TotalCommission += r.Amount
	}
	return out, nil
}

// AdminReferrals 后台分页查询推荐关系（支持被推荐人/推荐人精确过滤）。
func (s *sDistribution) AdminReferrals(ctx context.Context, req *v1.AdminReferralsReq) (*v1.AdminReferralsRes, error) {
	page, size, err := adminPageParams(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	if req.UserId != nil && *req.UserId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if req.ReferrerUserId != nil && *req.ReferrerUserId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	model := g.DB().Model("user_referrals r").Ctx(ctx)
	if req.UserId != nil {
		model = model.Where("r.user_id", *req.UserId)
	}
	if req.ReferrerUserId != nil {
		model = model.Where("r.referrer_user_id", *req.ReferrerUserId)
	}
	total, err := model.Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计推荐关系: %w", err))
	}

	var rows []struct {
		UserId           int64       `json:"user_id"`
		Username         string      `json:"username"`
		ReferrerUserId   int64       `json:"referrer_user_id"`
		ReferrerUsername string      `json:"referrer_username"`
		CreatedAt        *gtime.Time `json:"created_at"`
	}
	if err := model.
		LeftJoin("users u1", "u1.id = r.user_id").
		LeftJoin("users u2", "u2.id = r.referrer_user_id").
		Fields("r.user_id, r.referrer_user_id, r.created_at, u1.username, u2.username AS referrer_username").
		Order("r.id DESC").
		Page(page, size).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询推荐关系列表: %w", err))
	}

	items := make([]*v1.AdminReferralItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, &v1.AdminReferralItem{
			UserId:           r.UserId,
			Username:         r.Username,
			ReferrerUserId:   r.ReferrerUserId,
			ReferrerUsername: r.ReferrerUsername,
			CreatedAt:        r.CreatedAt,
		})
	}
	return &v1.AdminReferralsRes{Items: items, Total: int(total), Page: page, Size: size}, nil
}

// AdminCommissions 后台分页查询佣金（多条件过滤 + 白名单排序 + 严格分页）。
func (s *sDistribution) AdminCommissions(ctx context.Context, req *v1.AdminCommissionsReq) (*v1.AdminCommissionsRes, error) {
	page, size, err := adminPageParams(req.Page, req.Size)
	if err != nil {
		return nil, err
	}
	sortCol, direction, err := adminSortParams(req.Sort, req.Order)
	if err != nil {
		return nil, err
	}
	if req.OrderId != nil && *req.OrderId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if req.BuyerUserId != nil && *req.BuyerUserId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if req.BeneficiaryUserId != nil && *req.BeneficiaryUserId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	status, hasStatus, err := parseCommissionStatus(req.Status)
	if err != nil {
		return nil, err
	}
	startUnix, endUnix, err := adminTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}

	base := func(q *gdb.Model) *gdb.Model {
		if req.OrderId != nil {
			q = q.Where("c.order_id", *req.OrderId)
		}
		if req.BuyerUserId != nil {
			q = q.Where("c.buyer_user_id", *req.BuyerUserId)
		}
		if req.BeneficiaryUserId != nil {
			q = q.Where("c.beneficiary_user_id", *req.BeneficiaryUserId)
		}
		if hasStatus {
			q = q.Where("c.status", status)
		}
		if startUnix != nil {
			q = q.Where("c.created_at >= FROM_UNIXTIME(?)", *startUnix)
		}
		if endUnix != nil {
			q = q.Where("c.created_at < FROM_UNIXTIME(?)", *endUnix)
		}
		return q
	}

	total, err := base(g.DB().Model("distribution_commissions", "c")).Ctx(ctx).Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计佣金: %w", err))
	}

	orderBy := "c." + sortCol + " " + direction
	if sortCol != "id" {
		orderBy += ", c.id " + direction // id 作稳定 tiebreak，避免翻页重复/遗漏
	}
	items, err := s.queryCommissions(ctx, g.DB().Model("distribution_commissions", "c"), base, orderBy, page, size)
	if err != nil {
		return nil, err
	}
	return &v1.AdminCommissionsRes{Items: items, Total: int(total), Page: page, Size: size}, nil
}

// AdminCommission 后台查询佣金详情。
func (s *sDistribution) AdminCommission(ctx context.Context, id int64) (*v1.AdminCommissionRes, error) {
	if id <= 0 {
		return nil, codes.New(codes.CodeCommissionNotFound)
	}
	rows, err := s.queryCommissions(ctx, g.DB().Model("distribution_commissions", "c"), func(q *gdb.Model) *gdb.Model {
		return q.Where("c.id", id)
	}, "c.id DESC", 1, 1)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, codes.New(codes.CodeCommissionNotFound)
	}
	return &v1.AdminCommissionRes{Commission: *rows[0]}, nil
}

// AdminSettleCommission 结算佣金：pending(10) → settled(20)，写 settled_at。
//
// 幂等与状态机：仅 10→20 合法；已结算(20) 重复调用返回当前记录（幂等成功）；
// 已冲回(30) 是终态，返回 21007 且不改数据。
func (s *sDistribution) AdminSettleCommission(ctx context.Context, adminID, id int64) (*v1.AdminSettleCommissionRes, error) {
	if id <= 0 {
		return nil, codes.New(codes.CodeCommissionNotFound)
	}
	if adminID <= 0 {
		return nil, codes.New(codes.CodeUnauthorized)
	}

	var settled bool
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var rows []*commissionRow
		if e := tx.Model("distribution_commissions").Ctx(ctx).
			Where("id", id).
			LockUpdate().
			Scan(&rows); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定佣金记录: %w", e))
		}
		if len(rows) == 0 {
			return codes.New(codes.CodeCommissionNotFound)
		}
		switch rows[0].Status {
		case commissionStatusSettled:
			return nil // 幂等：已结算直接返回当前记录
		case commissionStatusReversed:
			return codes.New(codes.CodeCommissionInvalidStatus)
		case commissionStatusPending:
		default:
			return codes.New(codes.CodeCommissionInvalidStatus)
		}
		result, e := tx.Model("distribution_commissions").Ctx(ctx).
			Where("id", id).
			Where("status", commissionStatusPending).
			Data(g.Map{"status": commissionStatusSettled, "settled_at": gdb.Raw("NOW()")}).
			Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("结算佣金: %w", e))
		}
		affected, e := result.RowsAffected()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取结算结果: %w", e))
		}
		if affected == 0 {
			return codes.New(codes.CodeCommissionInvalidStatus)
		}
		settled = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if settled {
		logx.Info(ctx, "commission_settled", "佣金结算完成", logx.Fields{
			"commission_id": id,
			"admin_id":      adminID,
		})
	}

	detail, err := s.AdminCommission(ctx, id)
	if err != nil {
		return nil, err
	}
	return &v1.AdminSettleCommissionRes{Commission: detail.Commission}, nil
}

// AccrueForOrderInTx 在既有事务内为已支付订单计提一级佣金。
//
// 计佣公式（docs/design/distribution.md §3.2）：
//
//	base_amount       = max(0, original_amount - discount_amount - member_discount_amount)
//	                    （商品实收金额，不含运费；与 pricing.md 的金额守恒一致）
//	rate_bp           = commissionRateBp（V1 固定 500 = 5%，写库快照）
//	commission_amount = floor(base_amount × rate_bp / 10000)
//
// 幂等：distribution_commissions.uk_order_id，重复计提命中 1062 视为已计提（返回 nil）。
// no-op 情形：订单未支付 / 无推荐人 / 推荐人就是买家（防御，正常数据不可能）/ 基数或佣金为 0。
func (s *sDistribution) AccrueForOrderInTx(ctx context.Context, tx gdb.TX, orderID int64) error {
	if orderID <= 0 {
		return codes.New(codes.CodeInvalidArgument)
	}

	var orders []*commissionOrderSnapshot
	if err := tx.Model("orders").Ctx(ctx).
		Fields("id, user_id, status, original_amount, discount_amount, member_discount_amount").
		Where("id", orderID).
		Scan(&orders); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取订单 %d 用于计提佣金: %w", orderID, err))
	}
	if len(orders) == 0 {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("订单 %d 不存在，无法计提佣金", orderID))
	}
	order := orders[0]
	if order.Status != orderStatusPaid {
		return nil
	}

	base := order.OriginalAmount - order.DiscountAmount - order.MemberDiscountAmount
	if base <= 0 {
		return nil
	}

	var referrals []*referralRow
	if err := tx.Model("user_referrals").Ctx(ctx).
		Fields("id, user_id, referrer_user_id").
		Where("user_id", order.UserId).
		Scan(&referrals); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取买家推荐关系: %w", err))
	}
	if len(referrals) == 0 {
		return nil
	}
	beneficiary := referrals[0].ReferrerUserId
	if beneficiary <= 0 || beneficiary == order.UserId {
		// 自荐在绑定时已被拒绝；这里是防御性判断，绝不给自己计佣。
		logx.Warning(ctx, "commission_self_referral_skipped", "推荐人与买家相同，跳过计提佣金", logx.Fields{
			"order_id":      orderID,
			"buyer_user_id": order.UserId,
		})
		return nil
	}

	commission := base * int64(commissionRateBp) / rateBpBase
	if commission <= 0 {
		return nil
	}

	if _, err := tx.Model("distribution_commissions").Ctx(ctx).Data(g.Map{
		"order_id":            orderID,
		"buyer_user_id":       order.UserId,
		"beneficiary_user_id": beneficiary,
		"base_amount":         base,
		"rate_bp":             commissionRateBp,
		"commission_amount":   commission,
		"status":              commissionStatusPending,
	}).Insert(); err != nil {
		if isDuplicateKeyError(err) {
			return nil
		}
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入佣金记录(订单 %d): %w", orderID, err))
	}

	logx.Info(ctx, "commission_accrued", "订单佣金计提完成", logx.Fields{
		"order_id":            orderID,
		"buyer_user_id":       order.UserId,
		"beneficiary_user_id": beneficiary,
		"base_amount":         base,
		"rate_bp":             commissionRateBp,
		"commission_amount":   commission,
	})
	return nil
}

// ReverseForRefundInTx 在既有事务内按订单冲回佣金（退款成功）。
//
// 条件更新：WHERE order_id = ? AND status IN (10,20) → 30，reversed_amount 直接取 commission_amount，
// 因此「冲回不超过原佣金」由同一行快照保证，无需读后写。
// 幂等：RowsAffected=0 表示无佣金记录或已冲回，均为 no-op；同一退款单重复冲回由 uk_refund_id 兜底。
func (s *sDistribution) ReverseForRefundInTx(ctx context.Context, tx gdb.TX, orderID, refundID int64) error {
	if orderID <= 0 || refundID <= 0 {
		return codes.New(codes.CodeInvalidArgument)
	}
	result, err := tx.Model("distribution_commissions").Ctx(ctx).
		Where("order_id", orderID).
		WhereIn("status", []int{commissionStatusPending, commissionStatusSettled}).
		Data(g.Map{
			"status":          commissionStatusReversed,
			"reversed_amount": gdb.Raw("commission_amount"),
			"reversed_at":     gdb.Raw("NOW()"),
			"refund_id":       refundID,
			"reversal_reason": v1.ReversalReasonRefund,
		}).
		Update()
	if err != nil {
		if isDuplicateKeyError(err) {
			// uk_refund_id 冲突：该退款单已冲回过佣金，视为已处理。
			return nil
		}
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("冲回佣金(订单 %d): %w", orderID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取佣金冲回结果: %w", err))
	}
	if affected > 0 {
		logx.Info(ctx, "commission_reversed", "订单佣金已冲回", logx.Fields{
			"order_id":  orderID,
			"refund_id": refundID,
			"reversed":  affected,
		})
	}
	return nil
}

// commissionListRow 是佣金列表/详情的联查结果（distribution_commissions LEFT JOIN orders）。
type commissionListRow struct {
	Id                int64       `json:"id"`
	OrderId           int64       `json:"order_id"`
	OrderNo           string      `json:"order_no"`
	BuyerUserId       int64       `json:"buyer_user_id"`
	BeneficiaryUserId int64       `json:"beneficiary_user_id"`
	BaseAmount        int64       `json:"base_amount"`
	RateBp            int         `json:"rate_bp"`
	CommissionAmount  int64       `json:"commission_amount"`
	Status            int         `json:"status"`
	SettledAt         *gtime.Time `json:"settled_at"`
	ReversedAmount    int64       `json:"reversed_amount"`
	ReversedAt        *gtime.Time `json:"reversed_at"`
	RefundId          *int64      `json:"refund_id"`
	ReversalReason    string      `json:"reversal_reason"`
	CreatedAt         *gtime.Time `json:"created_at"`
}

// queryCommissions 以固定字段集合分页查询佣金并左连订单号（列表与详情复用）。
func (s *sDistribution) queryCommissions(
	ctx context.Context,
	model *gdb.Model,
	filter func(*gdb.Model) *gdb.Model,
	orderBy string,
	page, size int,
) ([]*v1.Commission, error) {
	var rows []*commissionListRow
	q := filter(model).
		LeftJoin("orders o", "o.id = c.order_id").
		Fields("c.id, c.order_id, c.buyer_user_id, c.beneficiary_user_id, c.base_amount, c.rate_bp, " +
			"c.commission_amount, c.status, c.settled_at, c.reversed_amount, c.reversed_at, c.refund_id, " +
			"c.reversal_reason, c.created_at, o.order_no")
	if err := q.Ctx(ctx).Order(orderBy).Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询佣金列表: %w", err))
	}
	items := make([]*v1.Commission, 0, len(rows))
	for _, r := range rows {
		items = append(items, &v1.Commission{
			Id:                r.Id,
			OrderId:           r.OrderId,
			OrderNo:           r.OrderNo,
			BuyerUserId:       r.BuyerUserId,
			BeneficiaryUserId: r.BeneficiaryUserId,
			BaseAmount:        r.BaseAmount,
			RateBp:            r.RateBp,
			CommissionAmount:  r.CommissionAmount,
			Status:            commissionStatusToString(r.Status),
			SettledAt:         r.SettledAt,
			ReversedAmount:    r.ReversedAmount,
			ReversedAt:        r.ReversedAt,
			RefundId:          r.RefundId,
			ReversalReason:    r.ReversalReason,
			CreatedAt:         r.CreatedAt,
		})
	}
	return items, nil
}

// requireAcyclic 从 userID 沿推荐人链向上遍历，若回到 userID 说明成环。
// 使用锁定读（FOR UPDATE）以读到并发事务已提交的最新关系，而不是事务起始快照。
func requireAcyclic(ctx context.Context, tx gdb.TX, userID int64) error {
	visited := make([]int64, 0, 8)
	cur := userID
	for depth := 0; depth <= maxReferralChainDepth; depth++ {
		var rows []*referralRow
		if err := tx.Model("user_referrals").Ctx(ctx).
			Fields("id, user_id, referrer_user_id").
			Where("user_id", cur).
			LockUpdate().
			Scan(&rows); err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("遍历推荐关系链: %w", err))
		}
		if len(rows) == 0 {
			return nil
		}
		next := rows[0].ReferrerUserId
		if next == userID {
			return codes.Wrap(codes.CodeReferralCycle, fmt.Errorf("推荐关系成环：%v -> %d -> %d", visited, cur, userID))
		}
		visited = append(visited, cur)
		cur = next
	}
	// 超过深度上限：正常一级分销不可能出现，保守拒绝而不是放行未知结构。
	return codes.Wrap(codes.CodeReferralCycle, fmt.Errorf("推荐关系链深度超过上限 %d，拒绝绑定", maxReferralChainDepth))
}

// findUsername 查询用户名（软引用；用户已被删除时返回空串，不报错）。
func findUsername(ctx context.Context, userID int64) (string, error) {
	var rows []*userStateRow
	if err := g.DB().Model("users").Ctx(ctx).Fields("id, username").Where("id", userID).Scan(&rows); err != nil {
		return "", codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询用户 %d: %w", userID, err))
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].Username, nil
}

// parseCommissionStatus 把 API 字符串状态转为 DB TINYINT；空串表示不过滤。
func parseCommissionStatus(s string) (int, bool, error) {
	switch strings.TrimSpace(s) {
	case "":
		return 0, false, nil
	case v1.CommissionStatusPending:
		return commissionStatusPending, true, nil
	case v1.CommissionStatusSettled:
		return commissionStatusSettled, true, nil
	case v1.CommissionStatusReversed:
		return commissionStatusReversed, true, nil
	default:
		return 0, false, codes.New(codes.CodeInvalidArgument)
	}
}

// commissionStatusToString 把 DB 状态转为 API 字符串枚举。
func commissionStatusToString(status int) string {
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

// normalizePage 归一化前台分页参数（clamp 风格）。
func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = defaultPage
	}
	if size < 1 {
		size = defaultSize
	}
	if size > maxSize {
		size = maxSize
	}
	return page, size
}

// adminPageParams 校验后台分页参数：缺失取默认值，越界返回 400（拒绝而非钳制）。
func adminPageParams(page, size *int) (int, int, error) {
	p := adminDefaultPage
	if page != nil {
		if *page < 1 {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		p = *page
	}
	sz := adminDefaultSize
	if size != nil {
		if *size < 1 || *size > adminMaxSize {
			return 0, 0, codes.New(codes.CodeInvalidArgument)
		}
		sz = *size
	}
	return p, sz, nil
}

// adminSortParams 校验排序参数：sort 白名单、order 取 asc/desc；非法返回 400。
func adminSortParams(sortField, order string) (string, string, error) {
	col := adminDefaultSort
	if f := strings.TrimSpace(sortField); f != "" {
		if !adminSortColumns[f] {
			return "", "", codes.New(codes.CodeInvalidArgument)
		}
		col = f
	}
	direction := "DESC"
	if d := strings.TrimSpace(order); d != "" {
		switch strings.ToLower(d) {
		case "asc":
			direction = "ASC"
		case "desc":
			direction = "DESC"
		default:
			return "", "", codes.New(codes.CodeInvalidArgument)
		}
	}
	return col, direction, nil
}

// adminTimeRange 解析时间范围：RFC3339（必须含时区偏移）→ Unix 秒；非法或 start>=end 返回 400。
func adminTimeRange(start, end string) (*int64, *int64, error) {
	var startUnix, endUnix *int64
	if s0 := strings.TrimSpace(start); s0 != "" {
		t, err := time.Parse(time.RFC3339, s0)
		if err != nil {
			return nil, nil, codes.New(codes.CodeInvalidArgument)
		}
		ts := t.Unix()
		startUnix = &ts
	}
	if e0 := strings.TrimSpace(end); e0 != "" {
		t, err := time.Parse(time.RFC3339, e0)
		if err != nil {
			return nil, nil, codes.New(codes.CodeInvalidArgument)
		}
		ts := t.Unix()
		endUnix = &ts
	}
	if startUnix != nil && endUnix != nil && *startUnix >= *endUnix {
		return nil, nil, codes.New(codes.CodeInvalidArgument)
	}
	return startUnix, endUnix, nil
}

// isDuplicateKeyError 判断是否为 MySQL 唯一键冲突（1062）。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
