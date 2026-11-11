package service

import (
	"context"

	"github.com/gogf/gf/v2/database/gdb"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/order/v1"
)

// GroupBuyOrderInput 是拼团订单创建的服务端输入。
//
// 金额语义沿用既有订单快照（见 docs/design/order.md）：
//   - original_amount = Σ 下单时 skus.price × quantity（服务端重读，不信任客户端）；
//   - discount_amount = (skus.price - GroupPrice) × quantity（拼团优惠）；
//   - total_amount    = GroupPrice × quantity；
//   - order_items.price = GroupPrice（成交价快照）。
//
// GroupPrice 必须由调用方从团快照读取，禁止直接透传客户端价格。
type GroupBuyOrderInput struct {
	UserID         int64
	AddressID      int64
	SkuID          int64
	Quantity       int64
	GroupPrice     int64
	IdempotencyKey string
}

// IOrder 定义订单服务。
type IOrder interface {
	// Create 创建订单（购物车勾选/直接购买），事务内写订单 + 订单项 + 扣库存（+ 清已购购物车条目）。
	Create(ctx context.Context, userID int64, req *v1.CreateReq) (*v1.CreateRes, error)
	// List 查询本人订单列表（按 id 倒序，含订单项）。
	List(ctx context.Context, userID int64) (*v1.ListRes, error)
	// Detail 查询本人订单详情；不存在或非本人统一 9001。
	Detail(ctx context.Context, userID, id int64) (*v1.DetailRes, error)
	// Quote 订单算价预览（只读，不占券/不扣库存；返回金额不是最终可信金额）。
	Quote(ctx context.Context, userID int64, req *v1.QuoteReq) (*v1.QuoteRes, error)
	// Pay 支付（仅本人，待支付→已支付，幂等；已过期订单先懒取消）。
	// payType 为空视为 mock；balance 走钱包余额渠道并与订单状态推进同事务。
	Pay(ctx context.Context, userID, id int64, payType string) (*v1.PayRes, error)
	// Cancel 取消本人待支付订单（恢复库存，原子）。
	Cancel(ctx context.Context, userID, id int64) (*v1.CancelRes, error)
	// Receive 确认收货（仅本人，已发货→已完成，同请求原子推进）。
	Receive(ctx context.Context, userID, id int64) (*v1.ReceiveRes, error)
	// Ship 发货（仅管理员，已支付→已发货；与退款申请互斥）。
	Ship(ctx context.Context, orderID int64) (*v1.ShipRes, error)
	// CompleteRefundInTx 在既有事务内完成退款推进：订单 paid(20)→refunded(70) + refunded_at + 恢复库存。
	// WHERE status=20 闸门保证「只补偿一次」；0 行返回明确错误供退款模块回滚。
	// 供退款模块在「执行退款成功」同事务中调用。
	CompleteRefundInTx(ctx context.Context, tx gdb.TX, orderID int64) error
	// CancelExpired 取消过期的待支付订单（供后台扫描器复用），返回本次实际取消数量。
	CancelExpired(ctx context.Context, limit int) (int, error)
	// CreateGroupBuyInTx 在既有事务内创建拼团订单（orders + order_items + 扣库存）。
	// 价格以服务端重读的 skus.price 为原价，拼团价仅作为成交价与优惠来源；
	// 返回新订单 id；SKU/商品不可售返回 9003/21008，拼团价非法返回 21004，
	// 幂等键冲突返回 9006。供拼团模块在「团容量更新 + 参与记录」同事务中复用。
	CreateGroupBuyInTx(ctx context.Context, tx gdb.TX, in *GroupBuyOrderInput) (int64, error)
	// CancelInTx 在既有事务内取消「待支付」订单（status=10→60 + 恢复库存 + 释放券）。
	// 返回是否实际取消（0 行表示已非待支付，幂等无副作用）。供拼团失败补偿复用。
	CancelInTx(ctx context.Context, tx gdb.TX, orderID int64) (bool, error)
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
