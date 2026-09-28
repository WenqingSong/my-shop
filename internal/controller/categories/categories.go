package categories

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/categories/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现商品分类 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回商品分类 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// List 处理树形分类列表。
func (c *ControllerV1) List(ctx context.Context, req *v1.ListReq) (res *v1.ListRes, err error) {
	return service.Category().List(ctx)
}

// Detail 处理分类详情。
func (c *ControllerV1) Detail(ctx context.Context, req *v1.DetailReq) (res *v1.DetailRes, err error) {
	return service.Category().Detail(ctx, req.Id)
}

// Create 处理创建分类。
func (c *ControllerV1) Create(ctx context.Context, req *v1.CreateReq) (res *v1.CreateRes, err error) {
	return service.Category().Create(ctx, req)
}

// Update 处理更新分类。
func (c *ControllerV1) Update(ctx context.Context, req *v1.UpdateReq) (res *v1.UpdateRes, err error) {
	return service.Category().Update(ctx, req)
}

// Delete 处理删除分类。
func (c *ControllerV1) Delete(ctx context.Context, req *v1.DeleteReq) (res *v1.DeleteRes, err error) {
	if err = service.Category().Delete(ctx, req.Id); err != nil {
		return nil, err
	}
	return &v1.DeleteRes{}, nil
}
