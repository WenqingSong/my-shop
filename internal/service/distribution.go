package service

import (
	"context"

	"github.com/gogf/gf/v2/database/gdb"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/distribution/v1"
)

// IDistribution 定义一级分销服务：推荐关系绑定/查询、佣金计提/查询/结算/冲回。
//
// 边界：佣金记录是应收/已结算事实，不是可消费余额；不进入钱包、无提现。
type IDistribution interface {
	// Bind 绑定一级推荐人：禁止自荐、禁止成环、已绑定不可改绑（同一推荐人重复绑定幂等成功）。
	Bind(ctx context.Context, userID int64, req *v1.BindReq) (*v1.BindRes, error)
	// MyReferral 查询本人的推荐关系与直接下级数量；未绑定时返回 bound=false。
	MyReferral(ctx context.Context, userID int64) (*v1.MyReferralRes, error)
	// MyCommissions 分页查询本人作为受益人的佣金明细。
	MyCommissions(ctx context.Context, userID int64, req *v1.MyCommissionsReq) (*v1.MyCommissionsRes, error)
	// MyCommissionSummary 查询本人佣金按状态聚合的汇总。
	MyCommissionSummary(ctx context.Context, userID int64) (*v1.MyCommissionSummaryRes, error)

	// AdminReferrals 后台分页查询推荐关系（支持被推荐人/推荐人精确过滤）。
	AdminReferrals(ctx context.Context, req *v1.AdminReferralsReq) (*v1.AdminReferralsRes, error)
	// AdminCommissions 后台分页查询佣金（多条件过滤 + 白名单排序）。
	AdminCommissions(ctx context.Context, req *v1.AdminCommissionsReq) (*v1.AdminCommissionsRes, error)
	// AdminCommission 后台查询佣金详情。
	AdminCommission(ctx context.Context, id int64) (*v1.AdminCommissionRes, error)
	// AdminSettleCommission 结算佣金（pending→settled，幂等；已冲回返回 21007）。
	AdminSettleCommission(ctx context.Context, adminID, id int64) (*v1.AdminSettleCommissionRes, error)

	// AccrueForOrderInTx 在既有事务内为已支付订单计提一级佣金，不自开事务。
	// 幂等：distribution_commissions.uk_order_id（重复计提命中 1062 视为已计提，返回 nil）。
	// 无推荐人 / 基数为 0 / 佣金为 0 时 no-op，不写记录。
	AccrueForOrderInTx(ctx context.Context, tx gdb.TX, orderID int64) error
	// ReverseForRefundInTx 在既有事务内按订单冲回佣金，不自开事务。
	// 幂等：status IN (10,20) 条件更新 + uk_refund_id；无佣金记录时 no-op（不伪造记录）。
	ReverseForRefundInTx(ctx context.Context, tx gdb.TX, orderID, refundID int64) error
}

var localDistribution IDistribution

// Distribution 返回一级分销服务实现。
func Distribution() IDistribution {
	if localDistribution == nil {
		panic("implement not found for interface IDistribution, forgot register?")
	}
	return localDistribution
}

// RegisterDistribution 注册一级分销服务实现。
func RegisterDistribution(s IDistribution) {
	localDistribution = s
}
