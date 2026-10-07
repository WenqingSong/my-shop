// Package v1 定义秒杀（Flash Sale）V1 的 API 契约：活动创建/更新（后台）与秒杀下单（前台）。
// 秒杀订单「下单即成交」：下单事务提交即成功，无支付/取消/退款状态机，行存在即成功订单。
package v1

import (
	"encoding/json"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 活动状态枚举（API 出参为字符串枚举；DB 存 TINYINT 1/0）。
const (
	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"
)

// 异步请求状态枚举（API 出参为字符串枚举；DB 存 TINYINT 0/1/2/3）。
const (
	RequestStatusQueued  = "queued"  // 已受理/排队中（含退避重试）
	RequestStatusSuccess = "success" // 落单成功（与成功订单创建同事务）
	RequestStatusFailed  = "failed"  // 业务失败终态（库存不足/时间窗结束/已购等）
	RequestStatusDead    = "dead"    // 技术失败重试超限（死信/待修复）
)

// ActivitySku 秒杀活动 × SKU 绑定（秒杀价 + 秒杀库存）。
type ActivitySku struct {
	Id         int64 `json:"id" dc:"绑定 id"`
	SkuId      int64 `json:"sku_id" dc:"SKU id（软引用）"`
	FlashPrice int64 `json:"flash_price" dc:"秒杀价（整数分）"`
	TotalStock int64 `json:"total_stock" dc:"秒杀初始库存"`
	Sold       int64 `json:"sold" dc:"已售数量"`
}

// Activity 秒杀活动（含 SKU 绑定列表）。
type Activity struct {
	Id        int64          `json:"id" dc:"活动 id"`
	Name      string         `json:"name" dc:"活动名"`
	Status    string         `json:"status" dc:"状态：enabled/disabled"`
	StartTime *gtime.Time    `json:"start_time" dc:"开始时间"`
	EndTime   *gtime.Time    `json:"end_time" dc:"结束时间"`
	Skus      []*ActivitySku `json:"skus" dc:"SKU 绑定列表"`
	CreatedAt *gtime.Time    `json:"created_at" dc:"创建时间"`
	UpdatedAt *gtime.Time    `json:"updated_at" dc:"更新时间"`
}

// FlashOrder 秒杀订单（软引用快照自足）。
type FlashOrder struct {
	Id               int64       `json:"id" dc:"订单 id"`
	OrderNo          string      `json:"order_no" dc:"全局唯一秒杀订单号"`
	UserId           int64       `json:"user_id" dc:"归属用户 id"`
	ActivityId       int64       `json:"activity_id" dc:"活动 id（软引用）"`
	SkuId            int64       `json:"sku_id" dc:"SKU id（软引用）"`
	ProductId        int64       `json:"product_id" dc:"商品 id（软引用）"`
	SkuName          string      `json:"sku_name" dc:"SKU 名称快照"`
	ProductName      string      `json:"product_name" dc:"商品名快照"`
	ProductMainImage string      `json:"product_main_image" dc:"商品主图快照"`
	FlashPrice       int64       `json:"flash_price" dc:"成交价快照 = 下单时秒杀价"`
	Quantity         int64       `json:"quantity" dc:"数量（V1 恒为 1）"`
	IdempotencyKey   string      `json:"idempotency_key" dc:"幂等键"`
	CreatedAt        *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt        *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// ActivitySkuInput 创建/更新活动时的 SKU 绑定输入。
// 字段由 service 显式校验（不依赖 gvalid），避免非业务 gcode 透出为 500。
type ActivitySkuInput struct {
	SkuId      int64       `json:"sku_id" dc:"SKU id（必须存在）"`
	FlashPrice json.Number `json:"flash_price" dc:"秒杀价（整数分，>0 且 ≤99999999）"`
	TotalStock int64       `json:"total_stock" dc:"秒杀库存（正整数）"`
}

// CreateReq 创建秒杀活动请求（后台，需 flash_sale:create）。
type CreateReq struct {
	g.Meta    `path:"/admin/flash-sales" method:"post" tags:"秒杀" summary:"创建秒杀活动"`
	Name      string              `json:"name" dc:"活动名，trim 后 1~64 字符"`
	StartTime *gtime.Time         `json:"start_time" dc:"开始时间（必须早于结束时间）"`
	EndTime   *gtime.Time         `json:"end_time" dc:"结束时间"`
	Skus      []*ActivitySkuInput `json:"skus" dc:"SKU 绑定列表（非空）"`
}

// CreateRes 创建秒杀活动响应。
type CreateRes struct {
	Activity
}

// UpdateReq 更新秒杀活动请求（后台，需 flash_sale:update；未提交字段保持不变）。
type UpdateReq struct {
	g.Meta    `path:"/admin/flash-sales/:id" method:"put" tags:"秒杀" summary:"更新秒杀活动"`
	Id        int64               `json:"id" in:"path" v:"required" dc:"活动 id"`
	Name      *string             `json:"name" dc:"活动名"`
	Status    *string             `json:"status" dc:"状态：enabled/disabled"`
	StartTime *gtime.Time         `json:"start_time" dc:"开始时间"`
	EndTime   *gtime.Time         `json:"end_time" dc:"结束时间"`
	Skus      []*ActivitySkuInput `json:"skus" dc:"SKU 绑定列表（提供则仅更新已存在绑定的秒杀价/库存；不允许新增或删除绑定，sku_id 必须已绑定该活动）"`
}

// UpdateRes 更新秒杀活动响应。
type UpdateRes struct {
	Activity
}

// CreateOrderReq 秒杀下单请求（前台登录用户，作用于 Principal.UserID；不提交价格/身份）。
type CreateOrderReq struct {
	g.Meta         `path:"/flash-sales/:id/orders" method:"post" tags:"秒杀" summary:"秒杀下单"`
	Id             int64  `json:"id" in:"path" v:"required" dc:"活动 id"`
	SkuId          int64  `json:"sku_id" dc:"SKU id（必须为该活动绑定的 SKU）"`
	IdempotencyKey string `json:"idempotency_key" dc:"幂等键（同键同内容幂等返回既有请求状态，同键不同内容 12005）"`
}

// CreateOrderRes 秒杀下单响应：V3 起为「排队受理结果」，不再同步返回已创建订单。
type CreateOrderRes struct {
	Status         string `json:"status" dc:"受理状态：queued（已受理/排队中，或幂等命中时返回既有请求状态）"`
	IdempotencyKey string `json:"idempotency_key" dc:"幂等键"`
	ActivityId     int64  `json:"activity_id" dc:"活动 id"`
	SkuId          int64  `json:"sku_id" dc:"SKU id"`
}

// GetOrderResultReq 查询秒杀下单结果请求（前台登录用户，作用于 Principal.UserID）。
type GetOrderResultReq struct {
	g.Meta         `path:"/flash-sales/:id/orders/result" method:"get" tags:"秒杀" summary:"查询秒杀下单结果"`
	Id             int64  `json:"id" in:"path" v:"required" dc:"活动 id"`
	IdempotencyKey string `json:"idempotency_key" in:"query" dc:"幂等键"`
}

// GetOrderResultRes 查询秒杀下单结果响应。
type GetOrderResultRes struct {
	Status   string      `json:"status" dc:"处理状态：queued/success/failed/dead"`
	Order    *FlashOrder `json:"order,omitempty" dc:"success 时的秒杀订单"`
	FailCode int         `json:"fail_code,omitempty" dc:"失败码（failed/dead 时的最近失败码，观测用）"`
}
