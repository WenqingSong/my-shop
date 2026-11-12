// Package profile 实现当前登录用户资料 v1 API。
package profile

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/profile/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现用户资料 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回用户资料 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Get 查询当前登录用户资料：用户 id 取自已认证 Principal，不接受请求参数。
func (c *ControllerV1) Get(ctx context.Context, req *v1.GetReq) (res *v1.GetRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Profile().Get(ctx, userID)
}

// Update 更新当前登录用户资料：用户 id 取自已认证 Principal，不接受请求体。
func (c *ControllerV1) Update(ctx context.Context, req *v1.UpdateReq) (res *v1.UpdateRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Profile().Update(ctx, userID, req)
}

// currentUserID 从 Principal 取当前用户 id；缺失返回 401（fail-closed）。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
