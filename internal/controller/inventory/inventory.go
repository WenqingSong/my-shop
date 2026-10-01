// Package inventory 实现库存 v1 API。
package inventory

import (
	"context"
	"encoding/json"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/inventory/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现库存 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回库存 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Get 处理查询库存。
func (c *ControllerV1) Get(ctx context.Context, req *v1.GetReq) (res *v1.GetRes, err error) {
	inv, err := service.Inventory().Get(ctx, req.SkuId)
	if err != nil {
		return nil, err
	}
	return &v1.GetRes{Inventory: *inv}, nil
}

// Increase 处理增加库存：数量经 json.Number 解析，操作者取自 AdminPrincipal。
func (c *ControllerV1) Increase(ctx context.Context, req *v1.IncreaseReq) (res *v1.IncreaseRes, err error) {
	qty, err := parseQuantity(req.Quantity)
	if err != nil {
		return nil, err
	}
	operatorID, err := adminOperatorID(ctx)
	if err != nil {
		return nil, err
	}
	inv, err := service.Inventory().Increase(ctx, req.SkuId, qty, operatorID)
	if err != nil {
		return nil, err
	}
	return &v1.IncreaseRes{Inventory: *inv}, nil
}

// Deduct 处理条件扣减库存。
func (c *ControllerV1) Deduct(ctx context.Context, req *v1.DeductReq) (res *v1.DeductRes, err error) {
	qty, err := parseQuantity(req.Quantity)
	if err != nil {
		return nil, err
	}
	operatorID, err := adminOperatorID(ctx)
	if err != nil {
		return nil, err
	}
	inv, err := service.Inventory().Deduct(ctx, req.SkuId, qty, operatorID)
	if err != nil {
		return nil, err
	}
	return &v1.DeductRes{Inventory: *inv}, nil
}

// ListLogs 处理查询库存流水。
func (c *ControllerV1) ListLogs(ctx context.Context, req *v1.ListLogsReq) (res *v1.ListLogsRes, err error) {
	logs, err := service.Inventory().ListLogs(ctx, req.SkuId)
	if err != nil {
		return nil, err
	}
	return &v1.ListLogsRes{Items: logs}, nil
}

// adminOperatorID 从 AdminPrincipal 取当前管理员 id 作为操作者；缺失返回 401。
func adminOperatorID(ctx context.Context) (*int64, error) {
	p, ok := middleware.AdminPrincipalFromContext(ctx)
	if !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return &p.AdminID, nil
}

// parseQuantity 将 json.Number 解析为 int64 原始值；非整数/缺失返回 6002，
// 是否为正整数（qty >= 1）由 service 层校验。
func parseQuantity(n json.Number) (int64, error) {
	v, err := n.Int64()
	if err != nil {
		return 0, codes.New(codes.CodeInventoryInvalidQuantity)
	}
	return v, nil
}
