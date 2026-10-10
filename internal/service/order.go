package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/order/v1"
)

// IOrder 定义订单服务。
type IOrder interface {
	// Create 创建订单（购物车勾选/直接购买），事务内写订单 + 订单项 + 扣库存（+ 清已购购物车条目）。
	Create(ctx context.Context, userID int64, req *v1.CreateReq) (*v1.CreateRes, error)
	// List 查询本人订单列表（按 id 倒序，含订单项）。
	List(ctx context.Context, userID int64) (*v1.ListRes, error)
	// Detail 查询本人订单详情；不存在或非本人统一 9001。
	Detail(ctx context.Context, userID, id int64) (*v1.DetailRes, error)
	// Pay Mock 支付（仅本人，待支付→已支付，幂等；已过期订单先懒取消）。
	Pay(ctx context.Context, userID, id int64) (*v1.PayRes, error)
	// Cancel 取消本人待支付订单（恢复库存，原子）。
	Cancel(ctx context.Context, userID, id int64) (*v1.CancelRes, error)
	// Receive 确认收货（仅本人，已发货→已完成，同请求原子推进）。
	Receive(ctx context.Context, userID, id int64) (*v1.ReceiveRes, error)
	// Ship 发货（仅管理员，已支付→已发货）。
	Ship(ctx context.Context, orderID int64) (*v1.ShipRes, error)
	// Refund 退款（仅管理员，已支付→已退款，恢复库存）。
	Refund(ctx context.Context, orderID int64) (*v1.RefundRes, error)
	// CancelExpired 取消过期的待支付订单（供后台扫描器复用），返回本次实际取消数量。
	CancelExpired(ctx context.Context, limit int) (int, error)
	// AdminList 后台分页查询全部普通订单（轻量，不含明细，支持组合筛选与稳定排序）。
	AdminList(ctx context.Context, req *v1.AdminListReq) (*v1.AdminListRes, error)
	// AdminStats 后台按 status 统计全部普通订单数量（覆盖 7 态，无数据为 0）。
	AdminStats(ctx context.Context) (*v1.AdminOrderStatsRes, error)
	// AdminDetail 后台按 id 查询任意普通订单详情（含明细与快照），不存在返回 9001。
	AdminDetail(ctx context.Context, id int64) (*v1.AdminOrderDetailRes, error)
}

var localOrder IOrder

// Order 返回订单服务实现。
func Order() IOrder {
	if localOrder == nil {
		panic("implement not found for interface IOrder, forgot register?")
	}
	return localOrder
}

// RegisterOrder 注册订单服务实现。
func RegisterOrder(s IOrder) {
	localOrder = s
}
