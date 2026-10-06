// Package review 实现商品评价 v1 API（前台用户侧与后台管理员侧）。
package review

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/review/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现商品评价前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回商品评价前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Create 处理提交评价（仅登录用户，作用于 Principal.UserID）。
func (c *ControllerV1) Create(ctx context.Context, req *v1.CreateReq) (res *v1.CreateRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Review().Create(ctx, userID, req)
}

// ListProduct 处理商品公开评价列表与汇总（无需 token）。
func (c *ControllerV1) ListProduct(ctx context.Context, req *v1.ListReq) (res *v1.ListRes, err error) {
	return service.Review().ListProduct(ctx, req)
}

// MyList 处理本人全部评价列表（仅登录用户）。
func (c *ControllerV1) MyList(ctx context.Context, req *v1.MyListReq) (res *v1.MyListRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Review().MyList(ctx, userID, req)
}

// Update 处理修改本人评价（仅登录用户）。
func (c *ControllerV1) Update(ctx context.Context, req *v1.UpdateReq) (res *v1.UpdateRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Review().Update(ctx, userID, req)
}

// Delete 处理删除本人评价（仅登录用户）。
func (c *ControllerV1) Delete(ctx context.Context, req *v1.DeleteReq) (res *v1.DeleteRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Review().Delete(ctx, userID, req.Id)
}

// AdminControllerV1 实现商品评价后台 v1 API。
type AdminControllerV1 struct{}

// NewAdminV1 创建并返回商品评价后台 v1 控制器。
func NewAdminV1() *AdminControllerV1 {
	return &AdminControllerV1{}
}

// TakeDown 处理下架违规评价（仅管理员，经 RequirePermission("review:take_down")）。
func (c *AdminControllerV1) TakeDown(ctx context.Context, req *v1.TakeDownReq) (res *v1.TakeDownRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Review().TakeDown(ctx, req.Id)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
