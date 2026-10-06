// Package banner 实现轮播图 v1 API（前台公开列表 + 后台管理）。
package banner

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/banner/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现轮播图 v1 API。认证与授权由路由层中间件（AdminAuth + RequirePermission）保证。
type ControllerV1 struct{}

// NewV1 创建并返回轮播图 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// List 处理公开轮播图列表（无需 token）。
func (c *ControllerV1) List(ctx context.Context, req *v1.ListReq) (res *v1.ListRes, err error) {
	return service.Banner().List(ctx)
}

// AdminList 处理后台轮播图列表（AdminAuth，全部状态）。
func (c *ControllerV1) AdminList(ctx context.Context, req *v1.AdminListReq) (res *v1.AdminListRes, err error) {
	return service.Banner().AdminList(ctx)
}

// AdminDetail 处理后台轮播图详情（AdminAuth，全部状态）。
func (c *ControllerV1) AdminDetail(ctx context.Context, req *v1.AdminDetailReq) (res *v1.AdminDetailRes, err error) {
	return service.Banner().AdminDetail(ctx, req.Id)
}

// Create 处理创建轮播图（AdminAuth + RequirePermission("banner:create")）。
func (c *ControllerV1) Create(ctx context.Context, req *v1.CreateReq) (res *v1.CreateRes, err error) {
	return service.Banner().Create(ctx, req)
}

// Update 处理更新轮播图（AdminAuth + RequirePermission("banner:update")）。
func (c *ControllerV1) Update(ctx context.Context, req *v1.UpdateReq) (res *v1.UpdateRes, err error) {
	return service.Banner().Update(ctx, req)
}

// Delete 处理删除轮播图（AdminAuth + RequirePermission("banner:delete")）。
func (c *ControllerV1) Delete(ctx context.Context, req *v1.DeleteReq) (res *v1.DeleteRes, err error) {
	if err = service.Banner().Delete(ctx, req.Id); err != nil {
		return nil, err
	}
	return &v1.DeleteRes{}, nil
}
