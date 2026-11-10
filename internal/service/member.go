package service

import (
	"context"

	"github.com/gogf/gf/v2/database/gdb"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/member/v1"
)

// IMember 定义会员等级、会员权益与成长值服务：
// 等级配置查询/维护、用户当前会员信息、成长值流水，以及订单/退款链路的 tx 感知联动。
type IMember interface {
	// Levels 查询启用中的会员等级配置（前台只读，按 level_no 升序）。
	Levels(ctx context.Context) (*v1.LevelsRes, error)
	// My 查询指定用户的当前会员等级、成长值与下一等级进度（无档案时按成长值 0 解析）。
	My(ctx context.Context, userID int64) (*v1.MyRes, error)
	// GrowthLogs 分页查询指定用户的成长值流水（按 id 倒序）。
	GrowthLogs(ctx context.Context, userID int64, req *v1.GrowthLogsReq) (*v1.GrowthLogsRes, error)

	// AdminListLevels 查询全部等级配置（含停用，后台只读）。
	AdminListLevels(ctx context.Context) (*v1.AdminListLevelsRes, error)
	// AdminCreateLevel 创建等级配置（校验序号/门槛唯一与「序号-门槛同向递增」）。
	AdminCreateLevel(ctx context.Context, req *v1.AdminCreateLevelReq) (*v1.AdminCreateLevelRes, error)
	// AdminUpdateLevel 全量更新等级配置（同一套校验，冲突返回 20004、破坏单调性返回 20002）。
	AdminUpdateLevel(ctx context.Context, req *v1.AdminUpdateLevelReq) (*v1.AdminUpdateLevelRes, error)
	// AdminDeleteLevel 删除未被引用的等级配置（被 user_memberships 引用返回 20003）。
	AdminDeleteLevel(ctx context.Context, id int64) (*v1.AdminDeleteLevelRes, error)
	// AdminMember 后台查询指定用户的会员信息（含是否已建档案）。
	AdminMember(ctx context.Context, userID int64) (*v1.AdminMemberRes, error)

	// DiscountRateBp 返回用户下单时生效的会员折扣率（万分比，10000=原价）。
	// 只读、不落库、不创建会员档案；无适用等级时返回 10000。
	DiscountRateBp(ctx context.Context, userID int64) (int, error)
	// AccrueForOrderInTx 在既有事务内按订单实付金额发放成长值并推进等级，不自开事务。
	// 幂等键 growth:order_reward:<order_id>：重复调用（重复支付回调）视为已处理，返回 nil。
	// 平台未配置可用等级时按 no-op 处理（不阻塞支付），不写任何流水。
	AccrueForOrderInTx(ctx context.Context, tx gdb.TX, orderID int64) error
	// DeductForRefundInTx 在既有事务内扣减该订单已发放的成长值（退款成功），不自开事务。
	// 幂等键 growth:refund_deduct:<refund_id>：重复调用视为已处理，返回 nil。
	// 订单未发放过成长值时 no-op，不伪造流水。等级只升不降，扣减不回退 level_id。
	DeductForRefundInTx(ctx context.Context, tx gdb.TX, orderID, refundID int64) error
}

var localMember IMember

// Member 返回会员服务实现。
func Member() IMember {
	if localMember == nil {
		panic("implement not found for interface IMember, forgot register?")
	}
	return localMember
}

// RegisterMember 注册会员服务实现。
func RegisterMember(s IMember) {
	localMember = s
}
