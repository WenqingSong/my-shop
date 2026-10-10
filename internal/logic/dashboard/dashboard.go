// Package dashboard 实现「运营数据大屏」业务逻辑：对 MySQL 业务数据做只读聚合统计，
// 提供运营概览、订单趋势、订单状态分布、商品销量排行与秒杀运营统计。
//
// 关键口径（与 docs/design/dashboard.md 一致）：
//   - 订单边界：普通订单指标只统计 orders；秒杀订单只在秒杀统计中单独呈现。
//   - 数量类指标（订单总数/今日新增/趋势/状态分布/时间范围总数）按 created_at + 全部订单状态。
//   - 金额/销量按「当前」订单状态 {20,30,40,50}（已支付/已发货/已收货/已完成）过滤，非事件时间口径。
//   - 时区：以 dashboard.timezone（默认 Asia/Shanghai）为权威业务时区切分日期，左闭右开 [start,end)。
package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/dashboard/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// defaultTimezone 是 Dashboard 日期切分时区的默认值（系统权威业务时区）。
const defaultTimezone = "Asia/Shanghai"

// defaultTopLimit / maxTopLimit 是商品销量排行返回数量的默认值与上限。
const (
	defaultTopLimit = 10
	maxTopLimit     = 50
)

// validOrderStatuses 是金额与销量口径的「有效订单状态」集合。
// 排除：待支付(10)、已取消(60)、已退款(70)。
var validOrderStatuses = []int{20, 30, 40, 50}

// allOrderStatuses 是订单状态机的全部状态枚举（10/20/30/40/50/60/70），用于状态分布显式返回。
var allOrderStatuses = []int{10, 20, 30, 40, 50, 60, 70}

type sDashboard struct{}

func init() {
	service.RegisterDashboard(New())
}

// New 创建并返回运营数据大屏服务实现。
func New() *sDashboard {
	return &sDashboard{}
}

// Overview 返回运营概览：用户总数、今日新增用户、商品总数、订单总数、今日新增订单、今日订单金额。
// 各指标独立聚合、可逐项对账；今日按 dashboard.timezone 零点切分。
func (s *sDashboard) Overview(ctx context.Context) (*v1.OverviewRes, error) {
	loc, err := businessLocation(ctx)
	if err != nil {
		return nil, err
	}
	todayStart, todayEnd := todayBounds(time.Now(), loc)

	userTotal, err := s.count(ctx, "users", "", "")
	if err != nil {
		return nil, err
	}
	todayNewUsers, err := s.count(ctx, "users", todayStart, todayEnd)
	if err != nil {
		return nil, err
	}
	productTotal, err := s.count(ctx, "products", "", "")
	if err != nil {
		return nil, err
	}
	orderTotal, err := s.count(ctx, "orders", "", "")
	if err != nil {
		return nil, err
	}
	todayNewOrders, err := s.count(ctx, "orders", todayStart, todayEnd)
	if err != nil {
		return nil, err
	}
	todayOrderAmount, err := s.sumOrderAmount(ctx, todayStart, todayEnd)
	if err != nil {
		return nil, err
	}

	return &v1.OverviewRes{
		UserTotal:        userTotal,
		TodayNewUsers:    todayNewUsers,
		ProductTotal:     productTotal,
		OrderTotal:       orderTotal,
		TodayNewOrders:   todayNewOrders,
		TodayOrderAmount: todayOrderAmount,
	}, nil
}

// Trend 返回最近 7 天每日订单数量趋势：单次聚合 + Go 侧补零，保证时间窗连续、无数据日期为 0。
// 时间窗整体为 [6 天前 00:00:00, 明日 00:00:00)。
func (s *sDashboard) Trend(ctx context.Context) (*v1.TrendRes, error) {
	loc, err := businessLocation(ctx)
	if err != nil {
		return nil, err
	}
	start, end, dates := trendBounds(time.Now(), loc)

	rows, err := s.groupCountByDate(ctx, start, end)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, r := range rows {
		counts[r.Date] = r.Cnt
	}

	days := make([]*v1.TrendItem, 0, len(dates))
	for _, d := range dates {
		days = append(days, &v1.TrendItem{Date: d, OrderCount: counts[d]})
	}
	return &v1.TrendRes{Days: days}, nil
}

// Status 返回订单状态分布与指定时间范围内订单总数。
// 未提供时间范围时统计全部订单；提供时必须 start_time/end_time 同传、RFC3339 且 start < end。
// 状态分布显式返回全部 7 个状态（含 0）。
func (s *sDashboard) Status(ctx context.Context, req *v1.StatusReq) (*v1.StatusRes, error) {
	start, end, err := parseRange(ctx, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}

	total, err := s.count(ctx, "orders", start, end)
	if err != nil {
		return nil, err
	}

	distMap, err := s.groupCountByStatus(ctx, "orders", start, end)
	if err != nil {
		return nil, err
	}

	distribution := make([]*v1.StatusItem, 0, len(allOrderStatuses))
	for _, status := range allOrderStatuses {
		distribution = append(distribution, &v1.StatusItem{Status: status, Count: distMap[status]})
	}

	return &v1.StatusRes{Total: total, StatusDistribution: distribution}, nil
}

// TopProducts 返回商品销量 TOP 榜：单条 SQL 聚合（order_items JOIN orders 过滤有效状态 +
// LEFT JOIN products 取当前名 + GROUP BY product_id + ORDER BY SUM DESC + LIMIT），无 N+1。
func (s *sDashboard) TopProducts(ctx context.Context, req *v1.TopReq) (*v1.TopRes, error) {
	limit, err := parseLimit(req.Limit)
	if err != nil {
		return nil, err
	}

	var rows []*topRow
	if err := g.DB().Model("order_items oi").Ctx(ctx).
		InnerJoin("orders o", "o.id = oi.order_id").
		LeftJoin("products p", "p.id = oi.product_id").
		Fields("oi.product_id AS product_id", "COALESCE(MAX(p.name), MAX(oi.product_name)) AS product_name", "SUM(oi.quantity) AS sales").
		WhereIn("o.status", validOrderStatuses).
		Group("oi.product_id").
		Order("sales DESC", "oi.product_id ASC").
		Limit(limit).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("聚合商品销量排行: %w", err))
	}

	items := make([]*v1.TopItem, 0, len(rows))
	for i, r := range rows {
		items = append(items, &v1.TopItem{
			Rank:        i + 1,
			ProductId:   r.ProductId,
			ProductName: r.ProductName,
			Sales:       r.Sales,
		})
	}
	return &v1.TopRes{Items: items}, nil
}

// FlashSales 返回秒杀运营统计：活动总数/状态分布、请求状态分布、当前 queued 数、成功订单数。
// 全部为全局聚合（不按活动过滤）；success_orders 以 flash_sale_orders 行为唯一事实来源。
func (s *sDashboard) FlashSales(ctx context.Context) (*v1.FlashSalesRes, error) {
	activityTotal, err := s.count(ctx, "flash_sale_activities", "", "")
	if err != nil {
		return nil, err
	}
	activityDist, err := s.groupCountByStatus(ctx, "flash_sale_activities", "", "")
	if err != nil {
		return nil, err
	}
	requestDist, err := s.groupCountByStatus(ctx, "flash_sale_order_requests", "", "")
	if err != nil {
		return nil, err
	}
	successOrders, err := s.count(ctx, "flash_sale_orders", "", "")
	if err != nil {
		return nil, err
	}

	activityStatus := &v1.ActivityStatus{
		Enabled:  activityDist[1],
		Disabled: activityDist[0],
	}
	requestStatus := &v1.RequestStatus{
		Queued:  requestDist[0],
		Success: requestDist[1],
		Failed:  requestDist[2],
		Dead:    requestDist[3],
	}
	return &v1.FlashSalesRes{
		ActivityTotal:  activityTotal,
		ActivityStatus: activityStatus,
		RequestStatus:  requestStatus,
		CurrentQueued:  requestDist[0], // 与 request_status.queued 同源：status=0 即「尚未终态、正在排队」。
		SuccessOrders:  successOrders,
	}, nil
}

// businessLocation 加载 Dashboard 日期切分时区；配置非法时 fail-closed 返回 500，不静默回退。
func businessLocation(ctx context.Context) (*time.Location, error) {
	name := g.Cfg().MustGet(ctx, "dashboard.timezone", defaultTimezone).String()
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("dashboard.timezone 配置非法: %w", err))
	}
	return loc, nil
}

// todayBounds 返回 [今日 00:00:00, 明日 00:00:00) 的 DATETIME 字符串边界（按指定时区）。
func todayBounds(now time.Time, loc *time.Location) (string, string) {
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return formatDateTime(start), formatDateTime(start.AddDate(0, 0, 1))
}

// trendBounds 返回最近 7 天的整体时间窗 [6 天前 00:00:00, 明日 00:00:00) 与 7 个自然日桶。
func trendBounds(now time.Time, loc *time.Location) (string, string, []string) {
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	start := today.AddDate(0, 0, -6)
	end := today.AddDate(0, 0, 1)
	dates := make([]string, 7)
	for i := 0; i < 7; i++ {
		dates[i] = start.AddDate(0, 0, i).Format("2006-01-02")
	}
	return formatDateTime(start), formatDateTime(end), dates
}

// parseRange 校验并解析时间范围：均为空 → 无范围（返回空边界）；仅单边 → 400；RFC3339 非法 → 400；start>=end → 400。
// 返回的边界为 dashboard.timezone 下的 DATETIME 字符串（左闭右开 [start,end)）。
func parseRange(ctx context.Context, startRaw, endRaw string) (string, string, error) {
	if startRaw == "" && endRaw == "" {
		return "", "", nil
	}
	if startRaw == "" || endRaw == "" {
		return "", "", codes.New(codes.CodeInvalidArgument)
	}
	loc, err := businessLocation(ctx)
	if err != nil {
		return "", "", err
	}
	start, err := time.Parse(time.RFC3339, startRaw)
	if err != nil {
		return "", "", codes.New(codes.CodeInvalidArgument)
	}
	end, err := time.Parse(time.RFC3339, endRaw)
	if err != nil {
		return "", "", codes.New(codes.CodeInvalidArgument)
	}
	if !start.Before(end) {
		return "", "", codes.New(codes.CodeInvalidArgument)
	}
	return formatDateTime(start.In(loc)), formatDateTime(end.In(loc)), nil
}

// parseLimit 校验并解析排行榜数量：未提供(0) → 默认 10；负数或超上限 → 400。
func parseLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultTopLimit, nil
	}
	if limit < 0 || limit > maxTopLimit {
		return 0, codes.New(codes.CodeInvalidArgument)
	}
	return limit, nil
}

// formatDateTime 将 time.Time 格式化为 MySQL DATETIME 字符串（用于字符串范围比较，避免函数包裹列）。
func formatDateTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

// count 统计表行数；start/end 均非空时按 created_at ∈ [start,end) 过滤。
func (s *sDashboard) count(ctx context.Context, table, start, end string) (int64, error) {
	m := g.DB().Model(table).Ctx(ctx)
	if start != "" && end != "" {
		m = m.Where("created_at >= ?", start).Where("created_at < ?", end)
	}
	n, err := m.Count()
	if err != nil {
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计 %s: %w", table, err))
	}
	return int64(n), nil
}

// sumOrderAmount 统计 orders 中 created_at ∈ [start,end) 且当前状态为有效状态的 total_amount 之和（分）。
// 语义为「创建于该区间、且当前处于有效状态的订单金额」，非「该区间支付金额」。
func (s *sDashboard) sumOrderAmount(ctx context.Context, start, end string) (int64, error) {
	record, err := g.DB().Model("orders").Ctx(ctx).
		Fields("COALESCE(SUM(total_amount), 0) AS amount").
		Where("created_at >= ?", start).
		Where("created_at < ?", end).
		WhereIn("status", validOrderStatuses).
		One()
	if err != nil {
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计今日订单金额: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return 0, nil
	}
	return record["amount"].Int64(), nil
}

// groupCountByDate 按 DATE(created_at) 聚合订单数，返回日期桶 → 计数。
func (s *sDashboard) groupCountByDate(ctx context.Context, start, end string) ([]*trendRow, error) {
	var rows []*trendRow
	if err := g.DB().Model("orders").Ctx(ctx).
		Fields("DATE(created_at) AS date", "COUNT(*) AS cnt").
		Where("created_at >= ?", start).
		Where("created_at < ?", end).
		Group("DATE(created_at)").
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("聚合订单趋势: %w", err))
	}
	return rows, nil
}

// groupCountByStatus 按 status 聚合表行数（可选 created_at 范围），返回 status → 计数。
func (s *sDashboard) groupCountByStatus(ctx context.Context, table, rangeStart, rangeEnd string) (map[int]int64, error) {
	m := g.DB().Model(table).Ctx(ctx).
		Fields("status", "COUNT(*) AS cnt")
	if rangeStart != "" && rangeEnd != "" {
		m = m.Where("created_at >= ?", rangeStart).Where("created_at < ?", rangeEnd)
	}
	var rows []*statusRow
	if err := m.Group("status").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("聚合 %s 状态分布: %w", table, err))
	}
	out := make(map[int]int64, len(rows))
	for _, r := range rows {
		out[r.Status] = r.Cnt
	}
	return out, nil
}

// trendRow 是订单趋势聚合结果（单日桶）。
type trendRow struct {
	Date string `json:"date"`
	Cnt  int64  `json:"cnt"`
}

// statusRow 是状态分布聚合结果。
type statusRow struct {
	Status int   `json:"status"`
	Cnt    int64 `json:"cnt"`
}

// topRow 是商品销量排行聚合结果。
type topRow struct {
	ProductId   int64  `json:"product_id"`
	ProductName string `json:"product_name"`
	Sales       int64  `json:"sales"`
}
