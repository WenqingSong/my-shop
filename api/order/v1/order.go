package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 订单状态枚举（API 出参为字符串枚举；DB 存 TINYINT 10/20/30/40/50/60/70）。
const (
	StatusPendingPayment = "pending_payment" // 待支付（10）
	StatusPaid           = "paid"            // 已支付（20）
	StatusShipped        = "shipped"         // 已发货（30）
	StatusReceived       = "received"        // 已收货（40）
	StatusCompleted      = "completed"       // 已完成（50，终态）
	StatusCancelled      = "cancelled"       // 已取消（60，终态）
	StatusRefunded       = "refunded"        // 已退款（70，终态）
)

// 下单来源枚举。
const (
	SourceCart   = "cart"   // 从购物车勾选项下单
	SourceDirect = "direct" // 直接购买（单个 SKU）
)

// 取消原因枚举（API 出参为整数）。
const (
	CancelReasonUser    = 1 // 用户取消
	CancelReasonTimeout = 2 // 超时未支付自动取消
)

// Order 订单结构（查询与写接口复用）。
type Order struct {
	Id             int64        `json:"id" dc:"订单 id"`
	OrderNo        string       `json:"order_no" dc:"全局唯一订单号"`
	UserId         int64        `json:"user_id" dc:"归属用户 id"`
	Status         string       `json:"status" dc:"状态：pending_payment/paid/shipped/received/completed/cancelled/refunded"`
	TotalAmount    int64        `json:"total_amount" dc:"成交总价（整数分，快照）"`
	IdempotencyKey string       `json:"idempotency_key" dc:"幂等键"`
	RecipientName  string       `json:"recipient_name" dc:"收货人姓名快照"`
	Phone          string       `json:"phone" dc:"手机号快照"`
	Province       string       `json:"province" dc:"省快照"`
	City           string       `json:"city" dc:"市快照"`
	District       string       `json:"district" dc:"区快照"`
	Detail         string       `json:"detail" dc:"详细地址快照"`
	AddressId      *int64       `json:"address_id" dc:"下单时地址 id（软引用，可空）"`
	ExpireAt       *gtime.Time  `json:"expire_at" dc:"支付截止时间"`
	CancelReason   *int         `json:"cancel_reason" dc:"取消原因：1=用户、2=超时；未取消为 null"`
	PaidAt         *gtime.Time  `json:"paid_at" dc:"支付时间"`
	ShippedAt      *gtime.Time  `json:"shipped_at" dc:"发货时间"`
	ReceivedAt     *gtime.Time  `json:"received_at" dc:"收货时间"`
	CompletedAt    *gtime.Time  `json:"completed_at" dc:"完成时间"`
	CancelledAt    *gtime.Time  `json:"cancelled_at" dc:"取消时间"`
	RefundedAt     *gtime.Time  `json:"refunded_at" dc:"退款时间"`
	CreatedAt      *gtime.Time  `json:"created_at" dc:"创建时间"`
	UpdatedAt      *gtime.Time  `json:"updated_at" dc:"更新时间"`
	Items          []*OrderItem `json:"items" dc:"订单项列表"`
}

// OrderItem 订单项（商品/SKU/价格快照，自足）。
type OrderItem struct {
	Id               int64  `json:"id" dc:"订单项 id"`
	SkuId            int64  `json:"sku_id" dc:"SKU id（软引用）"`
	ProductId        int64  `json:"product_id" dc:"商品 id（软引用）"`
	SkuName          string `json:"sku_name" dc:"SKU 名称快照"`
	ProductName      string `json:"product_name" dc:"商品名快照"`
	ProductMainImage string `json:"product_main_image" dc:"商品主图快照"`
	Price            int64  `json:"price" dc:"成交价（整数分，下单时 skus.price 快照）"`
	Quantity         int64  `json:"quantity" dc:"数量"`
}

// CreateReq 创建订单请求（source=cart 时从购物车勾选项下单，忽略 sku_id/quantity；
// source=direct 时按单个 SKU + 数量直接购买）。
type CreateReq struct {
	g.Meta         `path:"/orders" method:"post" tags:"订单" summary:"创建订单"`
	Source         string `json:"source" v:"required" dc:"来源：cart/direct"`
	AddressId      int64  `json:"address_id" v:"required" dc:"收货地址 id（必须属于当前用户）"`
	IdempotencyKey string `json:"idempotency_key" v:"required" dc:"幂等键（同键同内容幂等，同键不同内容 9006）"`
	SkuId          int64  `json:"sku_id" dc:"直接购买 SKU id（source=direct 必填）"`
	Quantity       int64  `json:"quantity" dc:"直接购买数量（source=direct 必填，正整数）"`
}

// CreateRes 创建订单响应。
type CreateRes struct {
	Order
}

// ListReq 查询本人订单列表请求。
type ListReq struct {
	g.Meta `path:"/orders" method:"get" tags:"订单" summary:"查询本人订单列表"`
}

// ListRes 订单列表响应（仅本人，按 id 倒序）。
type ListRes struct {
	Items []*Order `json:"items" dc:"订单列表（仅本人，按 id 倒序）"`
}

// DetailReq 查询本人订单详情请求。
type DetailReq struct {
	g.Meta `path:"/orders/:id" method:"get" tags:"订单" summary:"查询本人订单详情"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"订单 id"`
}

// DetailRes 订单详情响应。
type DetailRes struct {
	Order
}

// PayReq 支付请求（Mock 支付，仅本人）。
type PayReq struct {
	g.Meta `path:"/orders/:id/pay" method:"post" tags:"订单" summary:"支付订单（Mock）"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"订单 id"`
}

// PayRes 支付响应。
type PayRes struct {
	Order
}

// CancelReq 取消订单请求（仅本人，待支付订单）。
type CancelReq struct {
	g.Meta `path:"/orders/:id/cancel" method:"post" tags:"订单" summary:"取消订单"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"订单 id"`
}

// CancelRes 取消订单响应。
type CancelRes struct {
	Order
}

// ReceiveReq 确认收货请求（仅本人，已发货订单）。
type ReceiveReq struct {
	g.Meta `path:"/orders/:id/receive" method:"post" tags:"订单" summary:"确认收货"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"订单 id"`
}

// ReceiveRes 确认收货响应。
type ReceiveRes struct {
	Order
}

// ShipReq 发货请求（仅管理员，持 order:ship）。
type ShipReq struct {
	g.Meta `path:"/admin/orders/:id/ship" method:"post" tags:"订单" summary:"发货"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"订单 id"`
}

// ShipRes 发货响应。
type ShipRes struct {
	Order
}

// RefundReq 退款请求（仅管理员，持 order:refund，仅已支付未发货可退）。
type RefundReq struct {
	g.Meta `path:"/admin/orders/:id/refund" method:"post" tags:"订单" summary:"退款"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"订单 id"`
}

// RefundRes 退款响应。
type RefundRes struct {
	Order
}
