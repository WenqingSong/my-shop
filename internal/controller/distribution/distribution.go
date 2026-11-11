// Package distribution 实现一级分销 v1 API（前台用户侧与后台管理员侧）。
package distribution

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/distribution/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现分销前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回分销前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// BindReferral 绑定我的上级推荐人：被推荐人恒取自已认证 Principal，不接受请求体身份。
func (c *ControllerV1) BindReferral(ctx context.Context, req *v1.BindReferralReq) (res *v1.BindReferralRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Distribution().BindReferral(ctx, userID, req.ReferrerUserId)
}

// MyInfo 查询我的分销信息与佣金汇总。
func (c *ControllerV1) MyInfo(ctx context.Context, req *v1.MyInfoReq) (res *v1.MyInfoRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Distribution().MyInfo(ctx, userID)
}

// ListSubordinates 查询我的直接下级列表（仅本人数据）。
func (c *ControllerV1) ListSubordinates(ctx context.Context, req *v1.ListSubordinatesReq) (res *v1.ListSubordinatesRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Distribution().ListSubordinates(ctx, userID, req)
}

// ListMyCommissions 查询我的佣金记录（仅本人作为受益人的数据）。
func (c *ControllerV1) ListMyCommissions(ctx context.Context, req *v1.ListCommissionsReq) (res *v1.ListCommissionsRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Distribution().ListMyCommissions(ctx, userID, req)
}

// AdminControllerV1 实现分销后台 v1 API（全部只读）。
type AdminControllerV1 struct{}

// NewAdminV1 创建并返回分销后台 v1 控制器。
func NewAdminV1() *AdminControllerV1 {
	return &AdminControllerV1{}
}

// AdminListReferrals 后台分销关系分页列表（经 RequirePermission("distribution:referral_list")）。
func (c *AdminControllerV1) AdminListReferrals(ctx context.Context, req *v1.AdminListReferralsReq) (res *v1.AdminListReferralsRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Distribution().AdminListReferrals(ctx, req)
}

// AdminListDistributors 后台分销用户汇总列表（经 RequirePermission("distribution:referral_list")）。
func (c *AdminControllerV1) AdminListDistributors(ctx context.Context, req *v1.AdminListDistributorsReq) (res *v1.AdminListDistributorsRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Distribution().AdminListDistributors(ctx, req)
}

// AdminListCommissions 后台佣金记录分页列表（经 RequirePermission("distribution:commission_list")）。
func (c *AdminControllerV1) AdminListCommissions(ctx context.Context, req *v1.AdminListCommissionsReq) (res *v1.AdminListCommissionsRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Distribution().AdminListCommissions(ctx, req)
}

// AdminCommissionStats 后台佣金按状态统计（经 RequirePermission("distribution:commission_list")）。
func (c *AdminControllerV1) AdminCommissionStats(ctx context.Context, req *v1.AdminCommissionStatsReq) (res *v1.AdminCommissionStatsRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Distribution().AdminCommissionStats(ctx)
}

// AdminSettleCommission 人工结算佣金（经 RequirePermission("distribution:settle")）。
// 只做 pending→settled 的状态流转，不做真实资金转账。
func (c *AdminControllerV1) AdminSettleCommission(ctx context.Context, req *v1.AdminSettleCommissionReq) (res *v1.AdminSettleCommissionRes, err error) {
	principal, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Distribution().AdminSettleCommission(ctx, principal.AdminID, req.Id)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
