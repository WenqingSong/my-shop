// Package favorite 实现商品收藏 v1 API（前台用户侧）。
package favorite

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/favorite/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现商品收藏前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回商品收藏前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Add 处理添加收藏（仅登录用户，作用于 Principal.UserID）。
func (c *ControllerV1) Add(ctx context.Context, req *v1.AddReq) (res *v1.AddRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Favorite().Add(ctx, userID, req)
}

// Remove 处理取消收藏（仅登录用户，按 product_id 幂等）。
func (c *ControllerV1) Remove(ctx context.Context, req *v1.RemoveReq) (res *v1.RemoveRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.Favorite().Remove(ctx, userID, req.ProductId); err != nil {
		return nil, err
	}
	return &v1.RemoveRes{}, nil
}

// List 处理收藏列表（仅登录用户，仅返回本人收藏）。
func (c *ControllerV1) List(ctx context.Context, req *v1.ListReq) (res *v1.ListRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Favorite().List(ctx, userID, req)
}

// Check 处理是否已收藏（仅登录用户）。
func (c *ControllerV1) Check(ctx context.Context, req *v1.CheckReq) (res *v1.CheckRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Favorite().Check(ctx, userID, req.ProductId)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
