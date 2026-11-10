package service

import (
	"context"

	"github.com/gogf/gf/v2/database/gdb"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/distribution/v1"
)

// IDistribution 定义一级分销服务（推荐关系 + 订单佣金）。
//
// 边界：只做一级分销；佣金记录是应收/已结算事实，不写入钱包（见 docs/design/distribution.md）。
type IDistribution interface {
	// BindReferral 绑定当前登录用户的上级推荐人（一人一个上级、不可改绑、禁止自荐）。
	// 重复提交同一推荐人幂等返回既有关系；已绑定其他推荐人返回 20003。
	BindReferral(ctx context.Context, userID, referrerUserID int64) (*v1.BindReferralRes, error)
	// MyInfo 查询我的分销信息：上级关系、直接下级人数、佣金汇总。
	MyInfo(ctx context.Context, userID int64) (*v1.MyInfoRes, error)
	// ListSubordinates 分页查询我的直接下级。
	ListSubordinates(ctx context.Context, userID int64, req *v1.ListSubordinatesReq) (*v1.ListSubordinatesRes, error)
	// ListMyCommissions 分页查询我作为受益推荐人的佣金记录。
	ListMyCommissions(ctx context.Context, userID int64, req *v1.ListCommissionsReq) (*v1.ListCommissionsRes, error)
	// AdminListReferrals 后台分页查询分销关系。
	AdminListReferrals(ctx context.Context, req *v1.AdminListReferralsReq) (*v1.AdminListReferralsRes, error)
	// AdminListDistributors 后台分页查询推荐人维度汇总（下级人数与佣金合计）。
	AdminListDistributors(ctx context.Context, req *v1.AdminListDistributorsReq) (*v1.AdminListDistributorsRes, error)
	// AdminListCommissions 后台分页查询佣金记录。
	AdminListCommissions(ctx context.Context, req *v1.AdminListCommissionsReq) (*v1.AdminListCommissionsRes, error)
	// AdminCommissionStats 后台按状态统计佣金。
	AdminCommissionStats(ctx context.Context) (*v1.AdminCommissionStatsRes, error)

	// AccrueOrderCommissionInTx 在订单支付成功事务内为订单计提一级佣金。
	// 幂等：同一订单至多一条佣金（uk_order_id），重复调用命中唯一键视为已计提。
	// 无推荐人 / 比例为 0 / 金额不足 1 分时是 no-op，不写记录。
	AccrueOrderCommissionInTx(ctx context.Context, tx gdb.TX, orderID int64) error
	// ReverseOrderCommissionInTx 在退款成功事务内冲回该订单佣金。
	// 幂等：仅 10/20 状态可冲回，重复执行 0 行；uk_refund_id 兜底同一退款单重复冲回。
	ReverseOrderCommissionInTx(ctx context.Context, tx gdb.TX, refundID, orderID int64) error
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
