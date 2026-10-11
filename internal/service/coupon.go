package service

import (
	"context"

	"github.com/gogf/gf/v2/database/gdb"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/coupon/v1"
)

// ICoupon 定义优惠券服务：模板管理（后台）、领取/我的券（前台）与订单联动（tx 感知）。
type ICoupon interface {
	// AdminCreate 创建优惠券模板（后台）。
	AdminCreate(ctx context.Context, req *v1.AdminCreateReq) (*v1.AdminCreateRes, error)
	// AdminList 查询全部优惠券模板（后台，按 id 倒序）。
	AdminList(ctx context.Context) (*v1.AdminListRes, error)
	// AdminStop 停止新领取（后台，不影响已领取券）。
	AdminStop(ctx context.Context, id int64) (*v1.AdminStopRes, error)
	// Claim 用户领取优惠券（每用户每模板至多一张、发放总量不超发、并发不重复/不超发）。
	Claim(ctx context.Context, userID, templateID int64) (*v1.ClaimRes, error)
	// ListMine 查询当前用户的优惠券及其当前状态（含模板关键字段快照）。
	ListMine(ctx context.Context, userID int64) (*v1.ListMineRes, error)
	// GetDiscount 校验券归属/使用期/满减门槛并返回优惠金额（只读，不落库）。
	// 券不存在或不属于当前用户 → 19003；不可用（已使用/被占用/不在使用期/未达门槛）→ 19007。
	GetDiscount(ctx context.Context, userID, couponID, originalAmount int64) (int64, error)
	// LockInTx 在既有事务内原子预占券（available→locked），RowsAffected=0 时返回 19007。
	// 供订单模块「创建订单 + 预占券」同事务调用，不自开事务。
	LockInTx(ctx context.Context, tx gdb.TX, userID, couponID, orderID int64) error
	// MarkUsedInTx 在既有事务内核销券（locked→used，终态），供支付成功复用，不自开事务。
	MarkUsedInTx(ctx context.Context, tx gdb.TX, couponID int64) error
	// ReleaseInTx 在既有事务内释放订单占用的券（locked→available，清空 order_id/locked_at）。
	// 供取消/超时取消复用，不自开事务；订单未占券时为 no-op。
	ReleaseInTx(ctx context.Context, tx gdb.TX, orderID int64) error
}

var localCoupon ICoupon

// Coupon 返回优惠券服务实现。
func Coupon() ICoupon {
	if localCoupon == nil {
		panic("implement not found for interface ICoupon, forgot register?")
	}
	return localCoupon
}

// RegisterCoupon 注册优惠券服务实现。
func RegisterCoupon(s ICoupon) {
	localCoupon = s
}
