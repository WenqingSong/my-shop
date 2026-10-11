// Package refund 实现退款 v1 API（前台用户侧与后台管理员侧）。
package refund

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/refund/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现退款前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回退款前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Apply 处理发起退款申请：归属取自已认证 Principal，不接受请求体身份。
func (c *ControllerV1) Apply(ctx context.Context, req *v1.ApplyReq) (res *v1.ApplyRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Refund().Apply(ctx, userID, req.Id, req.Reason)
}

// Revoke 处理撤销本人退款申请。
func (c *ControllerV1) Revoke(ctx context.Context, req *v1.RevokeReq) (res *v1.RevokeRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Refund().Revoke(ctx, userID, req.Id)
}

// Detail 处理查询本人退款详情。
func (c *ControllerV1) Detail(ctx context.Context, req *v1.DetailReq) (res *v1.DetailRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Refund().Detail(ctx, userID, req.Id)
}

// List 处理查询本人退款记录列表。
func (c *ControllerV1) List(ctx context.Context, req *v1.ListReq) (res *v1.ListRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	var orderID *int64
	if req.OrderId > 0 {
		orderID = &req.OrderId
	}
	return service.Refund().List(ctx, userID, orderID)
}

// AdminControllerV1 实现退款后台 v1 API。
type AdminControllerV1 struct{}

// NewAdminV1 创建并返回退款后台 v1 控制器。
func NewAdminV1() *AdminControllerV1 {
	return &AdminControllerV1{}
}

// AdminList 处理后台退款分页列表（经 RequirePermission("refund:list")）。
func (c *AdminControllerV1) AdminList(ctx context.Context, req *v1.AdminListReq) (res *v1.AdminListRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Refund().AdminList(ctx, req)
}

// AdminDetail 处理后台退款详情（经 RequirePermission("refund:view")）。
func (c *AdminControllerV1) AdminDetail(ctx context.Context, req *v1.AdminDetailReq) (res *v1.AdminDetailRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Refund().AdminDetail(ctx, req.Id)
}

// ListAudits 处理查询退款审计记录（经 RequirePermission("refund:view")）。
func (c *AdminControllerV1) ListAudits(ctx context.Context, req *v1.ListAuditsReq) (res *v1.ListAuditsRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Refund().ListAudits(ctx, req.Id)
}

// Review 处理审核退款申请（经 RequirePermission("refund:review")；操作者 id 取自已认证 AdminPrincipal）。
func (c *AdminControllerV1) Review(ctx context.Context, req *v1.ReviewReq) (res *v1.ReviewRes, err error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Refund().Review(ctx, p.AdminID, req)
}

// Execute 处理执行退款并确认结果（经 RequirePermission("refund:execute")）。
func (c *AdminControllerV1) Execute(ctx context.Context, req *v1.ExecuteReq) (res *v1.ExecuteRes, err error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Refund().Execute(ctx, p.AdminID, req)
}

// Close 处理关闭退款（经 RequirePermission("refund:execute")）。
func (c *AdminControllerV1) Close(ctx context.Context, req *v1.CloseReq) (res *v1.CloseRes, err error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Refund().Close(ctx, p.AdminID, req)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
