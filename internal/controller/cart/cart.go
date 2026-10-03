// Package cart 实现购物车 v1 API。
package cart

import (
	"context"
	"encoding/json"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/cart/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现购物车 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回购物车 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// List 处理列出当前用户购物车条目。
func (c *ControllerV1) List(ctx context.Context, req *v1.ListReq) (res *v1.ListRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Cart().List(ctx, userID)
}

// Add 处理添加 SKU 到购物车（数量缺省由 service 层归一为 1）。
func (c *ControllerV1) Add(ctx context.Context, req *v1.AddReq) (res *v1.AddRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Cart().Add(ctx, userID, req)
}

// UpdateQuantity 处理修改条目数量：数量经 json.Number 解析为整数后再交 service 校验范围。
func (c *ControllerV1) UpdateQuantity(ctx context.Context, req *v1.UpdateQuantityReq) (res *v1.UpdateQuantityRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	qty, err := parseQuantity(req.Quantity)
	if err != nil {
		return nil, err
	}
	return service.Cart().UpdateQuantity(ctx, userID, req.Id, qty)
}

// UpdateSelected 处理勾选/取消勾选条目。
func (c *ControllerV1) UpdateSelected(ctx context.Context, req *v1.UpdateSelectedReq) (res *v1.UpdateSelectedRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Cart().UpdateSelected(ctx, userID, req.Id, req.Selected)
}

// Delete 处理删除购物车条目。
func (c *ControllerV1) Delete(ctx context.Context, req *v1.DeleteReq) (res *v1.DeleteRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.Cart().Delete(ctx, userID, req.Id); err != nil {
		return nil, err
	}
	return &v1.DeleteRes{}, nil
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}

// parseQuantity 将 json.Number 解析为 int64 原始值；非整数/缺失返回 7002，
// 是否为正整数（1..999）由 service 层校验。
func parseQuantity(n json.Number) (int64, error) {
	v, err := n.Int64()
	if err != nil {
		return 0, codes.New(codes.CodeCartInvalidQuantity)
	}
	return v, nil
}
