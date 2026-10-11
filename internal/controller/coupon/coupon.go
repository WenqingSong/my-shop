// Package coupon 实现优惠券 v1 API（前台用户侧领取/查询 + 后台管理员侧模板管理）。
package coupon

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/coupon/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现优惠券前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回优惠券前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Claim 处理领取优惠券（仅登录用户，作用于 Principal.UserID）。
func (c *ControllerV1) Claim(ctx context.Context, req *v1.ClaimReq) (res *v1.ClaimRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Coupon().Claim(ctx, userID, req.TemplateId)
}

// ListMine 处理查询我的优惠券（仅登录用户，作用于 Principal.UserID）。
func (c *ControllerV1) ListMine(ctx context.Context, req *v1.ListMineReq) (res *v1.ListMineRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Coupon().ListMine(ctx, userID)
}

// AdminControllerV1 实现优惠券后台 v1 API。
type AdminControllerV1 struct{}

// NewAdminV1 创建并返回优惠券后台 v1 控制器。
func NewAdminV1() *AdminControllerV1 {
	return &AdminControllerV1{}
}

// AdminCreate 处理创建优惠券模板（经 RequirePermission("coupon:create")）。
func (c *AdminControllerV1) AdminCreate(ctx context.Context, req *v1.AdminCreateReq) (res *v1.AdminCreateRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Coupon().AdminCreate(ctx, req)
}

// AdminList 处理查询优惠券模板列表（经 RequirePermission("coupon:list")）。
func (c *AdminControllerV1) AdminList(ctx context.Context, req *v1.AdminListReq) (res *v1.AdminListRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Coupon().AdminList(ctx)
}

// AdminStop 处理停止领取（经 RequirePermission("coupon:stop")）。
func (c *AdminControllerV1) AdminStop(ctx context.Context, req *v1.AdminStopReq) (res *v1.AdminStopRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Coupon().AdminStop(ctx, req.Id)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
