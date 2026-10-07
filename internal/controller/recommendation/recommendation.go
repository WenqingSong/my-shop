// Package recommendation 实现推荐位 v1 API（前台公开查询 + 后台管理）。
package recommendation

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/recommendation/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现推荐位 v1 API。认证与授权由路由层中间件（AdminAuth + RequirePermission）保证。
type ControllerV1 struct{}

// NewV1 创建并返回推荐位 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Frontend 处理前台推荐位查询（无需 token）。
func (c *ControllerV1) Frontend(ctx context.Context, req *v1.FrontendReq) (res *v1.FrontendRes, err error) {
	return service.Recommendation().Frontend(ctx, req.Code)
}

// AdminList 处理后台推荐位列表（AdminAuth，全部状态）。
func (c *ControllerV1) AdminList(ctx context.Context, req *v1.AdminListReq) (res *v1.AdminListRes, err error) {
	return service.Recommendation().AdminList(ctx)
}

// AdminDetail 处理后台推荐位详情（AdminAuth，全部状态，含推荐商品）。
func (c *ControllerV1) AdminDetail(ctx context.Context, req *v1.AdminDetailReq) (res *v1.AdminDetailRes, err error) {
	return service.Recommendation().AdminDetail(ctx, req.Id)
}

// Create 处理创建推荐位（AdminAuth + RequirePermission("recommend:create")）。
func (c *ControllerV1) Create(ctx context.Context, req *v1.CreateReq) (res *v1.CreateRes, err error) {
	return service.Recommendation().Create(ctx, req)
}

// Update 处理更新推荐位（AdminAuth + RequirePermission("recommend:update")）。
func (c *ControllerV1) Update(ctx context.Context, req *v1.UpdateReq) (res *v1.UpdateRes, err error) {
	return service.Recommendation().Update(ctx, req)
}

// Delete 处理删除推荐位（AdminAuth + RequirePermission("recommend:delete")）。
func (c *ControllerV1) Delete(ctx context.Context, req *v1.DeleteReq) (res *v1.DeleteRes, err error) {
	if err = service.Recommendation().Delete(ctx, req.Id); err != nil {
		return nil, err
	}
	return &v1.DeleteRes{}, nil
}

// AddItem 处理添加推荐商品（AdminAuth + RequirePermission("recommend:item")）。
func (c *ControllerV1) AddItem(ctx context.Context, req *v1.AddItemReq) (res *v1.AddItemRes, err error) {
	return service.Recommendation().AddItem(ctx, req)
}

// RemoveItem 处理移除推荐商品（AdminAuth + RequirePermission("recommend:item")）。
func (c *ControllerV1) RemoveItem(ctx context.Context, req *v1.RemoveItemReq) (res *v1.RemoveItemRes, err error) {
	if err = service.Recommendation().RemoveItem(ctx, req); err != nil {
		return nil, err
	}
	return &v1.RemoveItemRes{}, nil
}

// UpdateSort 处理调整推荐商品排序（AdminAuth + RequirePermission("recommend:item")）。
func (c *ControllerV1) UpdateSort(ctx context.Context, req *v1.UpdateSortReq) (res *v1.UpdateSortRes, err error) {
	return service.Recommendation().UpdateSort(ctx, req)
}
