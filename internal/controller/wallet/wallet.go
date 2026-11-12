// Package wallet 实现钱包 v1 API（前台用户侧）：余额查询、资金流水分页查询与 Mock 充值。
//
// 薄层约束：Controller 只做身份提取与参数转交，金额校验、幂等与事务全部在 Logic。
// 归属一律取自已认证 Principal.UserID，不接受请求体/查询参数中的 user_id。
package wallet

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/wallet/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现钱包前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回钱包前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Account 处理查询本人钱包余额。
func (c *ControllerV1) Account(ctx context.Context, req *v1.AccountReq) (res *v1.AccountRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Wallet().Account(ctx, userID)
}

// Transactions 处理查询本人资金流水。
func (c *ControllerV1) Transactions(ctx context.Context, req *v1.TransactionListReq) (res *v1.TransactionListRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Wallet().Transactions(ctx, userID, req)
}

// Recharge 处理 Mock 充值（开关关闭时由 Logic 返回 21009）。
func (c *ControllerV1) Recharge(ctx context.Context, req *v1.RechargeReq) (res *v1.RechargeRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Wallet().Recharge(ctx, userID, req)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
