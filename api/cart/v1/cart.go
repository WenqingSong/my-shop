package v1

import (
	"encoding/json"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// CartItem 购物车条目（查询与写接口复用）。异常状态由查询时动态计算：
// available / unavailable_reason 标识「SKU 不存在 / SKU 禁用 / 商品下架」，
// insufficient 标识库存不足，price_changed 标识价格相对加购快照发生变化。
type CartItem struct {
	Id                int64       `json:"id" dc:"条目 id"`
	SkuId             int64       `json:"sku_id" dc:"SKU id"`
	Quantity          int64       `json:"quantity" dc:"数量"`
	Selected          bool        `json:"selected" dc:"勾选状态"`
	PriceSnapshot     int64       `json:"price_snapshot" dc:"加购时 SKU 价格快照（整数分，不锁价）"`
	ProductId         *int64      `json:"product_id" dc:"所属商品 id；SKU 被删除时为 null"`
	ProductName       string      `json:"product_name" dc:"商品名；SKU 被删除时为空"`
	ProductMainImage  string      `json:"product_main_image" dc:"商品主图；SKU 被删除时为空"`
	SkuName           string      `json:"sku_name" dc:"SKU 名；SKU 被删除时为空"`
	CurrentPrice      *int64      `json:"current_price" dc:"当前 SKU 价格；SKU 被删除时为 null"`
	PriceChanged      bool        `json:"price_changed" dc:"当前价与加购快照是否不一致"`
	Available         bool        `json:"available" dc:"是否可购（SKU 存在且 enabled 且商品 on_shelf）"`
	UnavailableReason string      `json:"unavailable_reason" dc:"不可购原因：空/sku_deleted/disabled/off_shelf"`
	Stock             *int64      `json:"stock" dc:"当前库存（无记录为 0）；SKU 被删除时为 null"`
	Insufficient      bool        `json:"insufficient" dc:"是否库存不足（quantity > stock）"`
	CreatedAt         *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt         *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// ListReq 列出当前用户购物车条目请求。
type ListReq struct {
	g.Meta `path:"/cart" method:"get" tags:"购物车" summary:"列出购物车条目"`
}

// ListRes 购物车条目列表响应（无分页，返回全部本人条目）。
type ListRes struct {
	Items []*CartItem `json:"items" dc:"购物车条目列表"`
}

// AddReq 添加 SKU 到购物车请求（重复添加同 SKU 累加数量）。
type AddReq struct {
	g.Meta   `path:"/cart/items" method:"post" tags:"购物车" summary:"添加 SKU 到购物车"`
	SkuId    int64        `json:"sku_id" dc:"SKU id（必填；缺失或 ≤0 由 service 显式校验返回 400）"`
	Quantity *json.Number `json:"quantity" dc:"数量（正整数，默认 1，上限 999）"`
}

// AddRes 添加购物车条目响应（返回该条目当前快照）。
type AddRes struct {
	CartItem
}

// UpdateQuantityReq 修改购物车条目数量请求。
type UpdateQuantityReq struct {
	g.Meta   `path:"/cart/items/:id" method:"put" tags:"购物车" summary:"修改条目数量"`
	Id       int64       `json:"id" in:"path" v:"required" dc:"条目 id"`
	Quantity json.Number `json:"quantity" dc:"数量（正整数，上限 999）"`
}

// UpdateQuantityRes 修改数量响应。
type UpdateQuantityRes struct {
	CartItem
}

// UpdateSelectedReq 勾选/取消勾选购物车条目请求。
type UpdateSelectedReq struct {
	g.Meta   `path:"/cart/items/:id/selected" method:"put" tags:"购物车" summary:"勾选/取消勾选条目"`
	Id       int64 `json:"id" in:"path" v:"required" dc:"条目 id"`
	Selected bool  `json:"selected" dc:"勾选状态"`
}

// UpdateSelectedRes 勾选响应。
type UpdateSelectedRes struct {
	CartItem
}

// DeleteReq 删除购物车条目请求。
type DeleteReq struct {
	g.Meta `path:"/cart/items/:id" method:"delete" tags:"购物车" summary:"删除条目"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"条目 id"`
}

// DeleteRes 删除购物车条目响应（无业务字段）。
type DeleteRes struct{}
