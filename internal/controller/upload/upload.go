// Package upload 实现文件上传 v1 API（前台登录用户 + 后台管理员两个签发端点）。
package upload

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/upload/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现文件上传 v1 API。认证由路由层中间件（Auth / AdminAuth）保证。
type ControllerV1 struct{}

// NewV1 创建并返回文件上传 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// FrontendToken 处理前台签发七牛云上传凭证（Auth：登录用户）。
func (c *ControllerV1) FrontendToken(ctx context.Context, req *v1.FrontendTokenReq) (res *v1.TokenRes, err error) {
	return service.Upload().IssueToken(ctx, req.Filename, req.ContentType)
}

// AdminToken 处理后台签发七牛云上传凭证（AdminAuth：管理员）。
func (c *ControllerV1) AdminToken(ctx context.Context, req *v1.AdminTokenReq) (res *v1.TokenRes, err error) {
	return service.Upload().IssueToken(ctx, req.Filename, req.ContentType)
}
