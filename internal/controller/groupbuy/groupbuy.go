// Package groupbuy 实现拼团 v1 API（前台用户侧与后台管理员侧）。
package groupbuy

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/groupbuy/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现拼团前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回拼团前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// ListActivities 处理拼团活动列表。
func (c *ControllerV1) ListActivities(ctx context.Context, req *v1.ListActivitiesReq) (res *v1.ListActivitiesRes, err error) {
	return service.GroupBuy().ListActivities(ctx, req)
}

// ActivityDetail 处理拼团活动详情。
func (c *ControllerV1) ActivityDetail(ctx context.Context, req *v1.ActivityDetailReq) (res *v1.ActivityDetailRes, err error) {
	return service.GroupBuy().ActivityDetail(ctx, req.Id)
}

// ListTeams 处理进行中的拼团列表。
func (c *ControllerV1) ListTeams(ctx context.Context, req *v1.ListTeamsReq) (res *v1.ListTeamsRes, err error) {
	return service.GroupBuy().ListTeams(ctx, req)
}

// CreateTeam 处理发起拼团：用户身份取自已认证 Principal，不接受请求体身份。
func (c *ControllerV1) CreateTeam(ctx context.Context, req *v1.CreateTeamReq) (res *v1.CreateTeamRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.GroupBuy().CreateTeam(ctx, userID, req)
}

// JoinTeam 处理参与拼团。
func (c *ControllerV1) JoinTeam(ctx context.Context, req *v1.JoinTeamReq) (res *v1.JoinTeamRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.GroupBuy().JoinTeam(ctx, userID, req)
}

// TeamDetail 处理拼团详情。
func (c *ControllerV1) TeamDetail(ctx context.Context, req *v1.TeamDetailReq) (res *v1.TeamDetailRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.GroupBuy().TeamDetail(ctx, userID, req.Id)
}

// MyList 处理我的拼团记录。
func (c *ControllerV1) MyList(ctx context.Context, req *v1.MyListReq) (res *v1.MyListRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.GroupBuy().MyList(ctx, userID, req)
}

// AdminControllerV1 实现拼团后台 v1 API。
type AdminControllerV1 struct{}

// NewAdminV1 创建并返回拼团后台 v1 控制器。
func NewAdminV1() *AdminControllerV1 {
	return &AdminControllerV1{}
}

// AdminCreate 处理创建拼团活动（经 RequirePermission("group_buy:create")）。
func (c *AdminControllerV1) AdminCreate(ctx context.Context, req *v1.AdminCreateReq) (res *v1.AdminCreateRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.GroupBuy().AdminCreate(ctx, req)
}

// AdminUpdate 处理修改拼团活动（经 RequirePermission("group_buy:update")）。
func (c *AdminControllerV1) AdminUpdate(ctx context.Context, req *v1.AdminUpdateReq) (res *v1.AdminUpdateRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.GroupBuy().AdminUpdate(ctx, req)
}

// AdminStatus 处理启用/停用活动（经 RequirePermission("group_buy:update")）。
func (c *AdminControllerV1) AdminStatus(ctx context.Context, req *v1.AdminStatusReq) (res *v1.AdminStatusRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.GroupBuy().AdminStatus(ctx, req)
}

// AdminList 处理后台活动列表（经 RequirePermission("group_buy:list")）。
func (c *AdminControllerV1) AdminList(ctx context.Context, req *v1.AdminListReq) (res *v1.AdminListRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.GroupBuy().AdminList(ctx, req)
}

// AdminDetail 处理后台活动详情（经 RequirePermission("group_buy:view")）。
func (c *AdminControllerV1) AdminDetail(ctx context.Context, req *v1.AdminDetailReq) (res *v1.AdminDetailRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.GroupBuy().AdminDetail(ctx, req.Id)
}

// AdminListTeams 处理后台团列表（经 RequirePermission("group_buy:list")）。
func (c *AdminControllerV1) AdminListTeams(ctx context.Context, req *v1.AdminListTeamsReq) (res *v1.AdminListTeamsRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.GroupBuy().AdminListTeams(ctx, req)
}

// AdminTeamDetail 处理后台团详情（经 RequirePermission("group_buy:view")）。
func (c *AdminControllerV1) AdminTeamDetail(ctx context.Context, req *v1.AdminTeamDetailReq) (res *v1.AdminTeamDetailRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.GroupBuy().AdminTeamDetail(ctx, req.Id)
}

// AdminCloseTeam 处理运营关闭团（经 RequirePermission("group_buy:close")）。
func (c *AdminControllerV1) AdminCloseTeam(ctx context.Context, req *v1.AdminCloseTeamReq) (res *v1.AdminCloseTeamRes, err error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.GroupBuy().AdminCloseTeam(ctx, p.AdminID, req.Id)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
