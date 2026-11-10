// Package distribution 实现一级分销（推荐关系与佣金）的 HTTP 适配（前台用户侧 + 后台管理侧）。
package distribution

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/distribution/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现一级分销前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回一级分销前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Bind 绑定推荐人（作用于 Principal.UserID，不接受客户端指定被推荐人）。
func (c *ControllerV1) Bind(ctx context.Context, req *v1.BindReq) (res *v1.BindRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Distribution().Bind(ctx, userID, req)
}

// MyReferral 查询本人推荐关系与直接下级数量。
func (c *ControllerV1) MyReferral(ctx context.Context, req *v1.MyReferralReq) (res *v1.MyReferralRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Distribution().MyReferral(ctx, userID)
}

// MyCommissions 查询本人佣金明细（作用于 Principal.UserID）。
func (c *ControllerV1) MyCommissions(ctx context.Context, req *v1.MyCommissionsReq) (res *v1.MyCommissionsRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Distribution().MyCommissions(ctx, userID, req)
}

// MyCommissionSummary 查询本人佣金汇总（作用于 Principal.UserID）。
func (c *ControllerV1) MyCommissionSummary(ctx context.Context, req *v1.MyCommissionSummaryReq) (res *v1.MyCommissionSummaryRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Distribution().MyCommissionSummary(ctx, userID)
}

// AdminControllerV1 实现一级分销后台 v1 API（AdminAuth + RequirePermission 见路由）。
type AdminControllerV1 struct{}

// NewAdminV1 创建并返回一级分销后台 v1 控制器。
func NewAdminV1() *AdminControllerV1 {
	return &AdminControllerV1{}
}

// AdminReferrals 后台查询推荐关系列表。
func (c *AdminControllerV1) AdminReferrals(ctx context.Context, req *v1.AdminReferralsReq) (res *v1.AdminReferralsRes, err error) {
	if _, err := currentAdmin(ctx); err != nil {
		return nil, err
	}
	return service.Distribution().AdminReferrals(ctx, req)
}

// AdminCommissions 后台查询佣金列表。
func (c *AdminControllerV1) AdminCommissions(ctx context.Context, req *v1.AdminCommissionsReq) (res *v1.AdminCommissionsRes, err error) {
	if _, err := currentAdmin(ctx); err != nil {
		return nil, err
	}
	return service.Distribution().AdminCommissions(ctx, req)
}

// AdminCommission 后台查询佣金详情。
func (c *AdminControllerV1) AdminCommission(ctx context.Context, req *v1.AdminCommissionReq) (res *v1.AdminCommissionRes, err error) {
	if _, err := currentAdmin(ctx); err != nil {
		return nil, err
	}
	return service.Distribution().AdminCommission(ctx, req.Id)
}

// AdminSettleCommission 后台结算佣金（pending→settled）。
func (c *AdminControllerV1) AdminSettleCommission(ctx context.Context, req *v1.AdminSettleCommissionReq) (res *v1.AdminSettleCommissionRes, err error) {
	adminID, err := currentAdmin(ctx)
	if err != nil {
		return nil, err
	}
	return service.Distribution().AdminSettleCommission(ctx, adminID, req.Id)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}

// currentAdmin 从认证中间件注入的 AdminPrincipal 取当前管理员 id；缺失返回 401。
func currentAdmin(ctx context.Context) (int64, error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.AdminID, nil
}
