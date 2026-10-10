package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/dashboard/v1"
)

// IDashboard 定义运营数据大屏服务（只读 MySQL 聚合统计）。
type IDashboard interface {
	// Overview 返回运营概览（用户/商品/订单总量与今日增量、今日订单金额）。
	Overview(ctx context.Context) (*v1.OverviewRes, error)
	// Trend 返回最近 7 天每日订单数量趋势（时间窗连续，无数据日期为 0）。
	Trend(ctx context.Context) (*v1.TrendRes, error)
	// Status 返回订单状态分布与指定时间范围内订单总数。
	Status(ctx context.Context, req *v1.StatusReq) (*v1.StatusRes, error)
	// TopProducts 返回商品销量 TOP 榜（有效订单状态口径）。
	TopProducts(ctx context.Context, req *v1.TopReq) (*v1.TopRes, error)
	// FlashSales 返回秒杀运营统计（活动/请求状态分布、当前排队与成功订单）。
	FlashSales(ctx context.Context) (*v1.FlashSalesRes, error)
}

var localDashboard IDashboard

// Dashboard 返回运营数据大屏服务实现。
func Dashboard() IDashboard {
	if localDashboard == nil {
		panic("implement not found for interface IDashboard, forgot register?")
	}
	return localDashboard
}

// RegisterDashboard 注册运营数据大屏服务实现。
func RegisterDashboard(s IDashboard) {
	localDashboard = s
}
