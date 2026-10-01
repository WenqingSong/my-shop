// Package sku 实现 SKU v1 API。
package sku

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/sku/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现 SKU v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回 SKU v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Create 处理创建 SKU。
func (c *ControllerV1) Create(ctx context.Context, req *v1.CreateReq) (res *v1.CreateRes, err error) {
	return service.Sku().Create(ctx, req)
}

// Update 处理更新 SKU。
func (c *ControllerV1) Update(ctx context.Context, req *v1.UpdateReq) (res *v1.UpdateRes, err error) {
	return service.Sku().Update(ctx, req)
}

// Delete 处理删除 SKU。
func (c *ControllerV1) Delete(ctx context.Context, req *v1.DeleteReq) (res *v1.DeleteRes, err error) {
	if err := service.Sku().Delete(ctx, req.Id); err != nil {
		return nil, err
	}
	return &v1.DeleteRes{}, nil
}
