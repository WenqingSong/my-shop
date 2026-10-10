// Package dashboard 实现运营数据大屏 v1 API（后台只读聚合统计）。
// 认证与授权由路由层中间件（AdminAuth + RequirePermission("dashboard:view")）保证。
package dashboard

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/dashboard/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现运营数据大屏 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回运营数据大屏 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Overview 处理运营概览。
func (c *ControllerV1) Overview(ctx context.Context, req *v1.OverviewReq) (res *v1.OverviewRes, err error) {
	return service.Dashboard().Overview(ctx)
}

// Trend 处理最近 7 天订单趋势。
func (c *ControllerV1) Trend(ctx context.Context, req *v1.TrendReq) (res *v1.TrendRes, err error) {
	return service.Dashboard().Trend(ctx)
}

// Status 处理订单状态分布与时间范围总数。
func (c *ControllerV1) Status(ctx context.Context, req *v1.StatusReq) (res *v1.StatusRes, err error) {
	return service.Dashboard().Status(ctx, req)
}

// TopProducts 处理商品销量排行。
func (c *ControllerV1) TopProducts(ctx context.Context, req *v1.TopReq) (res *v1.TopRes, err error) {
	return service.Dashboard().TopProducts(ctx, req)
}

// FlashSales 处理秒杀运营统计。
func (c *ControllerV1) FlashSales(ctx context.Context, req *v1.FlashSalesReq) (res *v1.FlashSalesRes, err error) {
	return service.Dashboard().FlashSales(ctx)
}
