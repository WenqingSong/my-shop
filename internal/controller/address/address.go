// Package address 实现收货地址 v1 API。
package address

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/address/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现收货地址 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回收货地址 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Create 处理创建地址：归属取自已认证 Principal，不接受请求体 user_id。
func (c *ControllerV1) Create(ctx context.Context, req *v1.CreateReq) (res *v1.CreateRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Address().Create(ctx, userID, req)
}

// List 处理查询本人地址列表。
func (c *ControllerV1) List(ctx context.Context, req *v1.ListReq) (res *v1.ListRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Address().List(ctx, userID)
}

// Detail 处理查询本人地址详情。
func (c *ControllerV1) Detail(ctx context.Context, req *v1.DetailReq) (res *v1.DetailRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Address().Detail(ctx, userID, req.Id)
}

// Update 处理更新本人地址。
func (c *ControllerV1) Update(ctx context.Context, req *v1.UpdateReq) (res *v1.UpdateRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Address().Update(ctx, userID, req)
}

// Delete 处理删除本人地址。
func (c *ControllerV1) Delete(ctx context.Context, req *v1.DeleteReq) (res *v1.DeleteRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	if err = service.Address().Delete(ctx, userID, req.Id); err != nil {
		return nil, err
	}
	return &v1.DeleteRes{}, nil
}

// currentUserID 从 Principal 取当前用户 id；缺失返回 401（fail-closed）。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
