// Package bargain 实现砍价 v1 API（前台公开活动浏览 + 登录用户发起/助力/查询 + 后台活动管理）。
package bargain

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/bargain/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现砍价前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回砍价前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// List 处理查询可参与砍价活动（公开接口，无需 token）。
func (c *ControllerV1) List(ctx context.Context, req *v1.ListReq) (res *v1.ListRes, err error) {
	return service.Bargain().List(ctx, req)
}

// Detail 处理查询砍价活动详情（公开接口，无需 token）。
func (c *ControllerV1) Detail(ctx context.Context, req *v1.DetailReq) (res *v1.DetailRes, err error) {
	return service.Bargain().Detail(ctx, req.Id)
}

// Start 处理发起砍价（仅登录用户，作用于 Principal.UserID）。
func (c *ControllerV1) Start(ctx context.Context, req *v1.StartReq) (res *v1.StartRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Bargain().Start(ctx, userID, req.ActivityId)
}

// MyList 处理查询我的砍价记录（仅登录用户，作用于 Principal.UserID）。
func (c *ControllerV1) MyList(ctx context.Context, req *v1.MyListReq) (res *v1.MyListRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Bargain().MyInstances(ctx, userID, req)
}

// InstanceDetail 处理查询砍价实例详情（登录用户均可查看，便于好友打开分享链接）。
func (c *ControllerV1) InstanceDetail(ctx context.Context, req *v1.InstanceDetailReq) (res *v1.InstanceDetailRes, err error) {
	if _, err = currentUserID(ctx); err != nil {
		return nil, err
	}
	return service.Bargain().InstanceDetail(ctx, req.Id)
}

// Help 处理好友助力（仅登录用户，作用于 Principal.UserID）。
func (c *ControllerV1) Help(ctx context.Context, req *v1.HelpReq) (res *v1.HelpRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Bargain().Help(ctx, userID, req.Id)
}

// Helps 处理查询助力记录（仅登录用户）。
func (c *ControllerV1) Helps(ctx context.Context, req *v1.HelpsReq) (res *v1.HelpsRes, err error) {
	if _, err = currentUserID(ctx); err != nil {
		return nil, err
	}
	return service.Bargain().Helps(ctx, req.Id)
}

// Abandon 处理放弃砍价（仅实例发起者本人）。
func (c *ControllerV1) Abandon(ctx context.Context, req *v1.AbandonReq) (res *v1.AbandonRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Bargain().Abandon(ctx, userID, req.Id)
}

// AdminControllerV1 实现砍价后台 v1 API。
type AdminControllerV1 struct{}

// NewAdminV1 创建并返回砍价后台 v1 控制器。
func NewAdminV1() *AdminControllerV1 {
	return &AdminControllerV1{}
}

// AdminCreate 处理创建砍价活动（经 RequirePermission("bargain:create")）。
func (c *AdminControllerV1) AdminCreate(ctx context.Context, req *v1.AdminCreateReq) (res *v1.AdminCreateRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Bargain().AdminCreate(ctx, req)
}

// AdminUpdate 处理编辑砍价活动（经 RequirePermission("bargain:update")）。
func (c *AdminControllerV1) AdminUpdate(ctx context.Context, req *v1.AdminUpdateReq) (res *v1.AdminUpdateRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Bargain().AdminUpdate(ctx, req)
}

// AdminStatus 处理启用/停用活动（经 RequirePermission("bargain:update")）。
func (c *AdminControllerV1) AdminStatus(ctx context.Context, req *v1.AdminStatusReq) (res *v1.AdminStatusRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Bargain().AdminStatus(ctx, req)
}

// AdminList 处理后台活动分页列表（经 RequirePermission("bargain:list")）。
func (c *AdminControllerV1) AdminList(ctx context.Context, req *v1.AdminListReq) (res *v1.AdminListRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Bargain().AdminList(ctx, req)
}

// AdminDetail 处理后台活动详情（经 RequirePermission("bargain:view")）。
func (c *AdminControllerV1) AdminDetail(ctx context.Context, req *v1.AdminDetailReq) (res *v1.AdminDetailRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Bargain().AdminDetail(ctx, req.Id)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
