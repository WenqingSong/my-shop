// Package member 实现会员等级与会员权益的 HTTP 适配（前台用户侧 + 后台管理侧）。
package member

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/member/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现会员前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回会员前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Levels 查询启用中的会员等级配置（登录用户可读）。
func (c *ControllerV1) Levels(ctx context.Context, req *v1.LevelsReq) (res *v1.LevelsRes, err error) {
	if _, err := currentUserID(ctx); err != nil {
		return nil, err
	}
	return service.Member().Levels(ctx)
}

// My 查询本人会员等级与权益（作用于 Principal.UserID）。
func (c *ControllerV1) My(ctx context.Context, req *v1.MyReq) (res *v1.MyRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Member().My(ctx, userID)
}

// GrowthLogs 查询本人成长值流水（作用于 Principal.UserID）。
func (c *ControllerV1) GrowthLogs(ctx context.Context, req *v1.GrowthLogsReq) (res *v1.GrowthLogsRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Member().GrowthLogs(ctx, userID, req)
}

// AdminControllerV1 实现会员等级后台管理 v1 API（AdminAuth + RequirePermission 见路由）。
type AdminControllerV1 struct{}

// NewAdminV1 创建并返回会员后台 v1 控制器。
func NewAdminV1() *AdminControllerV1 {
	return &AdminControllerV1{}
}

// AdminListLevels 后台查询全部会员等级配置。
func (c *AdminControllerV1) AdminListLevels(ctx context.Context, req *v1.AdminListLevelsReq) (res *v1.AdminListLevelsRes, err error) {
	if _, err := currentAdmin(ctx); err != nil {
		return nil, err
	}
	return service.Member().AdminListLevels(ctx)
}

// AdminCreateLevel 后台创建会员等级。
func (c *AdminControllerV1) AdminCreateLevel(ctx context.Context, req *v1.AdminCreateLevelReq) (res *v1.AdminCreateLevelRes, err error) {
	if _, err := currentAdmin(ctx); err != nil {
		return nil, err
	}
	return service.Member().AdminCreateLevel(ctx, req)
}

// AdminUpdateLevel 后台更新会员等级。
func (c *AdminControllerV1) AdminUpdateLevel(ctx context.Context, req *v1.AdminUpdateLevelReq) (res *v1.AdminUpdateLevelRes, err error) {
	if _, err := currentAdmin(ctx); err != nil {
		return nil, err
	}
	return service.Member().AdminUpdateLevel(ctx, req)
}

// AdminDeleteLevel 后台删除会员等级。
func (c *AdminControllerV1) AdminDeleteLevel(ctx context.Context, req *v1.AdminDeleteLevelReq) (res *v1.AdminDeleteLevelRes, err error) {
	if _, err := currentAdmin(ctx); err != nil {
		return nil, err
	}
	return service.Member().AdminDeleteLevel(ctx, req.Id)
}

// AdminMember 后台查询指定用户的会员信息。
func (c *AdminControllerV1) AdminMember(ctx context.Context, req *v1.AdminMemberReq) (res *v1.AdminMemberRes, err error) {
	if _, err := currentAdmin(ctx); err != nil {
		return nil, err
	}
	return service.Member().AdminMember(ctx, req.UserId)
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
