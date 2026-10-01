// Package product 实现商品 SPU v1 API。
package product

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/product/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现商品 SPU v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回商品 SPU v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// List 处理前台商品列表（仅上架）。
func (c *ControllerV1) List(ctx context.Context, req *v1.ListReq) (res *v1.ListRes, err error) {
	return service.Product().List(ctx, req)
}

// Detail 处理前台商品详情（仅上架）。
func (c *ControllerV1) Detail(ctx context.Context, req *v1.DetailReq) (res *v1.DetailRes, err error) {
	return service.Product().Detail(ctx, req.Id)
}

// AdminList 处理后台商品列表（全部状态）。
func (c *ControllerV1) AdminList(ctx context.Context, req *v1.AdminListReq) (res *v1.AdminListRes, err error) {
	return service.Product().AdminList(ctx, req)
}

// AdminDetail 处理后台商品详情（全部状态）。
func (c *ControllerV1) AdminDetail(ctx context.Context, req *v1.AdminDetailReq) (res *v1.AdminDetailRes, err error) {
	return service.Product().AdminDetail(ctx, req.Id)
}

// Create 处理创建商品。
func (c *ControllerV1) Create(ctx context.Context, req *v1.CreateReq) (res *v1.CreateRes, err error) {
	return service.Product().Create(ctx, req)
}

// Update 处理更新商品。
func (c *ControllerV1) Update(ctx context.Context, req *v1.UpdateReq) (res *v1.UpdateRes, err error) {
	return service.Product().Update(ctx, req)
}

// OnShelf 处理上架。
func (c *ControllerV1) OnShelf(ctx context.Context, req *v1.OnShelfReq) (res *v1.OnShelfRes, err error) {
	return service.Product().OnShelf(ctx, req.Id)
}

// OffShelf 处理下架。
func (c *ControllerV1) OffShelf(ctx context.Context, req *v1.OffShelfReq) (res *v1.OffShelfRes, err error) {
	return service.Product().OffShelf(ctx, req.Id)
}
