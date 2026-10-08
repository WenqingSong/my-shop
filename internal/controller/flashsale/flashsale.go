// Package flashsale 实现秒杀 v1 API（前台用户侧下单与后台管理员侧活动管理）。
package flashsale

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现秒杀前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回秒杀前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// CreateOrder 处理秒杀下单：归属取自已认证 Principal，不接受请求体身份。
func (c *ControllerV1) CreateOrder(ctx context.Context, req *v1.CreateOrderReq) (res *v1.CreateOrderRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.FlashSale().CreateOrder(ctx, userID, req.Id, req)
}

// GetOrderResult 处理查询秒杀下单结果：归属取自已认证 Principal，仅本人可查。
func (c *ControllerV1) GetOrderResult(ctx context.Context, req *v1.GetOrderResultReq) (res *v1.GetOrderResultRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.FlashSale().GetOrderResult(ctx, userID, req.Id, req.IdempotencyKey)
}

// AdminControllerV1 实现秒杀后台 v1 API。
type AdminControllerV1 struct{}

// NewAdminV1 创建并返回秒杀后台 v1 控制器。
func NewAdminV1() *AdminControllerV1 {
	return &AdminControllerV1{}
}

// Create 处理创建秒杀活动（经 RequirePermission("flash_sale:create")）。
func (c *AdminControllerV1) Create(ctx context.Context, req *v1.CreateReq) (res *v1.CreateRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.FlashSale().CreateActivity(ctx, req)
}

// Update 处理更新秒杀活动（经 RequirePermission("flash_sale:update")）。
func (c *AdminControllerV1) Update(ctx context.Context, req *v1.UpdateReq) (res *v1.UpdateRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.FlashSale().UpdateActivity(ctx, req)
}

// RepairRequest 处理人工修复秒杀请求（经 RequirePermission("flash_sale:repair")；仅 dead→queued）。
// 操作者 id 取自已认证 AdminPrincipal，不信任请求体身份。
func (c *AdminControllerV1) RepairRequest(ctx context.Context, req *v1.RepairRequestReq) (res *v1.RepairRequestRes, err error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.FlashSale().RepairRequest(ctx, p.AdminID, req)
}

// ListRequestAudits 处理查询秒杀请求审计记录（经 RequirePermission("flash_sale:repair")）。
func (c *AdminControllerV1) ListRequestAudits(ctx context.Context, req *v1.ListRequestAuditsReq) (res *v1.ListRequestAuditsRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.FlashSale().ListRequestAudits(ctx, req.Id)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
