// Package order 实现订单 v1 API（前台用户侧与后台管理员侧）。
package order

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/order/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现订单前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回订单前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Create 处理创建订单。
func (c *ControllerV1) Create(ctx context.Context, req *v1.CreateReq) (res *v1.CreateRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Order().Create(ctx, userID, req)
}

// List 处理查询本人订单列表。
func (c *ControllerV1) List(ctx context.Context, req *v1.ListReq) (res *v1.ListRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Order().List(ctx, userID)
}

// Detail 处理查询本人订单详情。
func (c *ControllerV1) Detail(ctx context.Context, req *v1.DetailReq) (res *v1.DetailRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Order().Detail(ctx, userID, req.Id)
}

// Pay 处理 Mock 支付。
func (c *ControllerV1) Pay(ctx context.Context, req *v1.PayReq) (res *v1.PayRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Order().Pay(ctx, userID, req.Id)
}

// Cancel 处理取消订单。
func (c *ControllerV1) Cancel(ctx context.Context, req *v1.CancelReq) (res *v1.CancelRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Order().Cancel(ctx, userID, req.Id)
}

// Receive 处理确认收货。
func (c *ControllerV1) Receive(ctx context.Context, req *v1.ReceiveReq) (res *v1.ReceiveRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Order().Receive(ctx, userID, req.Id)
}

// AdminControllerV1 实现订单后台 v1 API。
type AdminControllerV1 struct{}

// NewAdminV1 创建并返回订单后台 v1 控制器。
func NewAdminV1() *AdminControllerV1 {
	return &AdminControllerV1{}
}

// Ship 处理发货（仅管理员，经 RequirePermission("order:ship")）。
func (c *AdminControllerV1) Ship(ctx context.Context, req *v1.ShipReq) (res *v1.ShipRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Order().Ship(ctx, req.Id)
}

// Refund 处理退款（仅管理员，经 RequirePermission("order:refund")）。
func (c *AdminControllerV1) Refund(ctx context.Context, req *v1.RefundReq) (res *v1.RefundRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Order().Refund(ctx, req.Id)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
