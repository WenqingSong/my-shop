// Package spec 实现商品规格（规格组/规格值）v1 API。
package spec

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/spec/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现商品规格 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回商品规格 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// CreateGroup 处理创建规格组。
func (c *ControllerV1) CreateGroup(ctx context.Context, req *v1.CreateGroupReq) (res *v1.CreateGroupRes, err error) {
	return service.Spec().CreateGroup(ctx, req)
}

// UpdateGroup 处理更新规格组。
func (c *ControllerV1) UpdateGroup(ctx context.Context, req *v1.UpdateGroupReq) (res *v1.UpdateGroupRes, err error) {
	return service.Spec().UpdateGroup(ctx, req)
}

// DeleteGroup 处理删除规格组。
func (c *ControllerV1) DeleteGroup(ctx context.Context, req *v1.DeleteGroupReq) (res *v1.DeleteGroupRes, err error) {
	if err := service.Spec().DeleteGroup(ctx, req.Id); err != nil {
		return nil, err
	}
	return &v1.DeleteGroupRes{}, nil
}

// CreateValue 处理创建规格值。
func (c *ControllerV1) CreateValue(ctx context.Context, req *v1.CreateValueReq) (res *v1.CreateValueRes, err error) {
	return service.Spec().CreateValue(ctx, req)
}

// UpdateValue 处理更新规格值。
func (c *ControllerV1) UpdateValue(ctx context.Context, req *v1.UpdateValueReq) (res *v1.UpdateValueRes, err error) {
	return service.Spec().UpdateValue(ctx, req)
}

// DeleteValue 处理删除规格值。
func (c *ControllerV1) DeleteValue(ctx context.Context, req *v1.DeleteValueReq) (res *v1.DeleteValueRes, err error) {
	if err := service.Spec().DeleteValue(ctx, req.Id); err != nil {
		return nil, err
	}
	return &v1.DeleteValueRes{}, nil
}
