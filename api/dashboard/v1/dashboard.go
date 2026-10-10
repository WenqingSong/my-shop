// Package v1 定义运营数据大屏（Dashboard）V1 的公开 API 契约。
// 全部接口仅后台管理员（AdminAuth + RequirePermission("dashboard:view")）可访问，
// 统一响应 {code,message,data}，JSON 字段 snake_case。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
)

// OverviewReq 运营概览请求（无参数）。
type OverviewReq struct {
	g.Meta `path:"/admin/dashboard/overview" method:"get" tags:"运营数据大屏" summary:"运营概览"`
}

// OverviewRes 运营概览响应。
type OverviewRes struct {
	UserTotal        int64 `json:"user_total" dc:"用户总数（前台 users）"`
	TodayNewUsers    int64 `json:"today_new_users" dc:"今日新增用户数（created_at ∈ 今日）"`
	ProductTotal     int64 `json:"product_total" dc:"商品总数（全部 SPU）"`
	OrderTotal       int64 `json:"order_total" dc:"订单总数（普通 orders，全部状态）"`
	TodayNewOrders   int64 `json:"today_new_orders" dc:"今日新增订单数（created_at ∈ 今日，全部状态）"`
	TodayOrderAmount int64 `json:"today_order_amount" dc:"今日订单金额（分，已支付口径，非实收）"`
}

// TrendReq 订单趋势请求（无参数，固定最近 7 天）。
type TrendReq struct {
	g.Meta `path:"/admin/dashboard/orders/trend" method:"get" tags:"运营数据大屏" summary:"最近 7 天订单趋势"`
}

// TrendItem 单日订单数量。
type TrendItem struct {
	Date       string `json:"date" dc:"日期（YYYY-MM-DD）"`
	OrderCount int64  `json:"order_count" dc:"当日订单数（无数据为 0）"`
}

// TrendRes 订单趋势响应（7 项连续，无数据日期返回 0）。
type TrendRes struct {
	Days []*TrendItem `json:"days" dc:"最近 7 天每日订单数"`
}

// StatusReq 订单状态分布与时间范围总数请求。
// start_time/end_time 可选；二者必须同传、均为 RFC3339 且 start < end；缺省则统计全部订单。
type StatusReq struct {
	g.Meta    `path:"/admin/dashboard/orders/status" method:"get" tags:"运营数据大屏" summary:"订单状态分布与时间范围总数"`
	StartTime string `json:"start_time" in:"query" dc:"起始时间（RFC3339，可选，与 end_time 同传）"`
	EndTime   string `json:"end_time" in:"query" dc:"结束时间（RFC3339，可选，与 start_time 同传）"`
}

// StatusItem 单个订单状态的分布计数。
type StatusItem struct {
	Status int   `json:"status" dc:"订单状态（10/20/30/40/50/60/70）"`
	Count  int64 `json:"count" dc:"该状态订单数（无该状态为 0）"`
}

// StatusRes 订单状态分布与时间范围总数响应（7 状态显式返回，含 0）。
type StatusRes struct {
	Total              int64         `json:"total" dc:"时间范围内订单总数（全部状态）"`
	StatusDistribution []*StatusItem `json:"status_distribution" dc:"订单状态分布（7 状态）"`
}

// TopReq 商品销量排行请求（limit 可选，默认 10，上限 50）。
type TopReq struct {
	g.Meta `path:"/admin/dashboard/products/top" method:"get" tags:"运营数据大屏" summary:"商品销量 TOP 榜"`
	Limit  int `json:"limit" in:"query" dc:"返回数量（默认 10，上限 50）"`
}

// TopItem 销量排行条目。
type TopItem struct {
	Rank        int    `json:"rank" dc:"排名（从 1 开始）"`
	ProductId   int64  `json:"product_id" dc:"商品 id"`
	ProductName string `json:"product_name" dc:"商品名"`
	Sales       int64  `json:"sales" dc:"销量（有效订单明细数量之和）"`
}

// TopRes 商品销量排行响应。
type TopRes struct {
	Items []*TopItem `json:"items" dc:"销量 TOP 榜"`
}

// FlashSalesReq 秒杀运营统计请求（无参数）。
type FlashSalesReq struct {
	g.Meta `path:"/admin/dashboard/flash-sales" method:"get" tags:"运营数据大屏" summary:"秒杀运营统计"`
}

// ActivityStatus 秒杀活动状态分布（按 flash_sale_activities.status 字段，非时间窗派生）。
type ActivityStatus struct {
	Enabled  int64 `json:"enabled" dc:"enabled 活动数（status=1）"`
	Disabled int64 `json:"disabled" dc:"disabled 活动数（status=0）"`
}

// RequestStatus 秒杀请求状态分布（全局聚合）。
type RequestStatus struct {
	Queued  int64 `json:"queued" dc:"queued 请求数（status=0）"`
	Success int64 `json:"success" dc:"success 请求数（status=1）"`
	Failed  int64 `json:"failed" dc:"failed 请求数（status=2）"`
	Dead    int64 `json:"dead" dc:"dead 请求数（status=3）"`
}

// FlashSalesRes 秒杀运营统计响应。
type FlashSalesRes struct {
	ActivityTotal  int64           `json:"activity_total" dc:"秒杀活动总数"`
	ActivityStatus *ActivityStatus `json:"activity_status" dc:"活动状态分布"`
	RequestStatus  *RequestStatus  `json:"request_status" dc:"请求状态分布"`
	CurrentQueued  int64           `json:"current_queued" dc:"当前 queued 请求数量"`
	SuccessOrders  int64           `json:"success_orders" dc:"秒杀成功订单数量（flash_sale_orders 行）"`
}
