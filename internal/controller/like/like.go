// Package like 实现商品点赞 v1 API（前台用户侧 + 公开点赞数）。
package like

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/like/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现商品点赞前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回商品点赞前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Like 处理点赞（仅登录用户，作用于 Principal.UserID，幂等）。
func (c *ControllerV1) Like(ctx context.Context, req *v1.LikeReq) (res *v1.LikeRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Like().Like(ctx, userID, req)
}

// Cancel 处理取消点赞（仅登录用户，按 product_id 幂等）。
func (c *ControllerV1) Cancel(ctx context.Context, req *v1.CancelReq) (res *v1.CancelRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.Like().Cancel(ctx, userID, req.ProductId); err != nil {
		return nil, err
	}
	return &v1.CancelRes{}, nil
}

// Check 处理是否已点赞（仅登录用户）。
func (c *ControllerV1) Check(ctx context.Context, req *v1.CheckReq) (res *v1.CheckRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Like().Check(ctx, userID, req.ProductId)
}

// Count 处理公开点赞数（无需登录）。
func (c *ControllerV1) Count(ctx context.Context, req *v1.CountReq) (res *v1.CountRes, err error) {
	return service.Like().Count(ctx, req.ProductId)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
