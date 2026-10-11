package service

import (
	"context"

	"github.com/gogf/gf/v2/database/gdb"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/refund/v1"
)

// IRefund 定义退款服务。
type IRefund interface {
	// Apply 发起退款申请（仅本人、已支付未发货订单），写退款申请 + 审计。
	Apply(ctx context.Context, userID, orderID int64, reason string) (*v1.ApplyRes, error)
	// Revoke 撤销本人申请（仅申请中）。
	Revoke(ctx context.Context, userID, id int64) (*v1.RevokeRes, error)
	// Detail 查询本人退款详情；不存在或非本人统一 18001。
	Detail(ctx context.Context, userID, id int64) (*v1.DetailRes, error)
	// List 查询本人退款记录列表（可选 order_id 过滤）。
	List(ctx context.Context, userID int64, orderID *int64) (*v1.ListRes, error)
	// AdminList 后台分页查询全部退款申请。
	AdminList(ctx context.Context, req *v1.AdminListReq) (*v1.AdminListRes, error)
	// AdminDetail 后台按 id 查询任意退款详情；不存在返回 18001。
	AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error)
	// ListAudits 查询退款审计记录（append-only）。
	ListAudits(ctx context.Context, id int64) (*v1.ListAuditsRes, error)
	// Review 审核退款申请（同意/拒绝，仅申请中）。
	Review(ctx context.Context, adminID int64, req *v1.ReviewReq) (*v1.ReviewRes, error)
	// Execute 执行退款并确认结果（成功推进订单退款+恢复库存，失败仅推进退款状态）。
	Execute(ctx context.Context, adminID int64, req *v1.ExecuteReq) (*v1.ExecuteRes, error)
	// Close 关闭退款（仅退款失败→已关闭，恢复履约）。
	Close(ctx context.Context, adminID int64, req *v1.CloseReq) (*v1.CloseRes, error)
	// HasActiveRequestInTx 在事务内判断订单是否存在有效退款申请（status ∈ {10,20,30}）。
	// 供订单模块在发货事务内检查退款互斥。
	HasActiveRequestInTx(ctx context.Context, tx gdb.TX, orderID int64) (bool, error)
}

var localRefund IRefund

// Refund 返回退款服务实现。
func Refund() IRefund {
	if localRefund == nil {
		panic("implement not found for interface IRefund, forgot register?")
	}
	return localRefund
}

// RegisterRefund 注册退款服务实现。
func RegisterRefund(s IRefund) {
	localRefund = s
}
