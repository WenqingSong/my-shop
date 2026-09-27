package iam

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/iam/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现 IAM v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回 IAM v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Register 处理用户注册。
func (c *ControllerV1) Register(ctx context.Context, req *v1.RegisterReq) (res *v1.RegisterRes, err error) {
	return service.Iam().Register(ctx, req)
}

// Login 处理用户登录并返回 access token。
func (c *ControllerV1) Login(ctx context.Context, req *v1.LoginReq) (res *v1.LoginRes, err error) {
	return service.Iam().Login(ctx, req)
}

// Me 返回当前登录用户。
func (c *ControllerV1) Me(ctx context.Context, req *v1.MeReq) (res *v1.MeRes, err error) {
	principal, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Iam().Me(ctx, principal.UserID)
}
