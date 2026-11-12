// Package region 实现地区字典只读 v1 API。
package region

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/region/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现地区字典 v1 API（公开只读，无写入端点）。
type ControllerV1 struct{}

// NewV1 创建并返回地区字典 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// List 按父级查询直接子级；parent_id 缺省或 0 返回省级列表。
func (c *ControllerV1) List(ctx context.Context, req *v1.ListReq) (res *v1.ListRes, err error) {
	return service.Region().List(ctx, req.ParentId)
}

// Tree 返回省/市/区三级树。
func (c *ControllerV1) Tree(ctx context.Context, req *v1.TreeReq) (res *v1.TreeRes, err error) {
	return service.Region().Tree(ctx)
}

// Detail 查询单个地区及其完整三级链路。
func (c *ControllerV1) Detail(ctx context.Context, req *v1.DetailReq) (res *v1.DetailRes, err error) {
	return service.Region().Detail(ctx, req.Id)
}
