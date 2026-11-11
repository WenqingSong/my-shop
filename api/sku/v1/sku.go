package v1

import (
	"encoding/json"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// SKU 状态枚举（API 出参为字符串枚举；DB 存 TINYINT 1/0）。
const (
	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"
)

// SkuSpecValue 是 SKU 的一个规格取值（SKU → 规格组/规格值反向展示）。
type SkuSpecValue struct {
	SpecId      int64  `json:"spec_id" dc:"规格组 id"`
	SpecName    string `json:"spec_name" dc:"规格组名（如「颜色」）"`
	SpecValueId int64  `json:"spec_value_id" dc:"规格值 id"`
	Value       string `json:"value" dc:"规格值文本（如「红色」）"`
	Sort        int    `json:"sort" dc:"规格组排序值（升序展示）"`
}

// Sku 商品 SKU 结构，前台/后台商品详情内嵌复用。
type Sku struct {
	Id            int64           `json:"id" dc:"SKU id（稳定引用键，不重用）"`
	SkuCode       string          `json:"sku_code" dc:"SKU 业务唯一编码（下单按它定位 SKU）"`
	ProductId     int64           `json:"product_id" dc:"所属商品 id"`
	Name          string          `json:"name" dc:"SKU 名称"`
	ImageUrl      string          `json:"image_url" dc:"SKU 规格图 URL；空表示回退商品主图"`
	Price         int64           `json:"price" dc:"售价（整数分，下单定价唯一来源）"`
	OriginalPrice int64           `json:"original_price" dc:"原价/划线价（整数分）"`
	Status        string          `json:"status" dc:"状态：enabled/disabled"`
	SpecKey       string          `json:"spec_key" dc:"规格组合指纹（MD5，32 位小写 hex；无规格为空串）"`
	SpecValues    []*SkuSpecValue `json:"spec_values" dc:"规格取值列表（按规格组 sort 升序；无规格为空数组）"`
	CreatedAt     *gtime.Time     `json:"created_at" dc:"创建时间"`
	UpdatedAt     *gtime.Time     `json:"updated_at" dc:"更新时间"`
}

// CreateReq 创建 SKU 请求（product_id 在请求体，指向必须存在的商品）。
// sku_code 由服务端生成，不接受客户端指定；无规格 SKU 使用本接口创建。
type CreateReq struct {
	g.Meta        `path:"/admin/skus" method:"post" tags:"SKU" summary:"创建 SKU"`
	ProductId     int64        `json:"product_id" v:"required" dc:"所属商品 id（必须存在）"`
	Name          string       `json:"name" v:"required" dc:"SKU 名称，非空且不超过 128 字符"`
	ImageUrl      string       `json:"image_url" dc:"SKU 规格图 URL，可选，不超过 512 字符"`
	Price         json.Number  `json:"price" v:"required" dc:"售价（整数分，0~99999999）"`
	OriginalPrice *json.Number `json:"original_price" dc:"原价/划线价（整数分，0~99999999）；缺省等于售价"`
	Status        *string      `json:"status" dc:"状态：enabled/disabled，默认 enabled"`
}

// CreateRes 创建 SKU 响应。
type CreateRes struct {
	Sku
}

// UpdateReq 更新 SKU 请求（product_id 不可变）。
type UpdateReq struct {
	g.Meta        `path:"/admin/skus/:id" method:"put" tags:"SKU" summary:"更新 SKU"`
	Id            int64        `json:"id" in:"path" v:"required" dc:"SKU id"`
	Name          *string      `json:"name" dc:"SKU 名称，非空且不超过 128 字符"`
	ImageUrl      *string      `json:"image_url" dc:"SKU 规格图 URL，不超过 512 字符"`
	Price         *json.Number `json:"price" dc:"售价（整数分，0~99999999）"`
	OriginalPrice *json.Number `json:"original_price" dc:"原价/划线价（整数分，0~99999999）"`
	Status        *string      `json:"status" dc:"状态：enabled/disabled"`
}

// UpdateRes 更新 SKU 响应。
type UpdateRes struct {
	Sku
}

// DeleteReq 删除 SKU 请求（物理删除）。
type DeleteReq struct {
	g.Meta `path:"/admin/skus/:id" method:"delete" tags:"SKU" summary:"删除 SKU"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"SKU id"`
}

// DeleteRes 删除 SKU 响应（无业务字段）。
type DeleteRes struct{}

// GenerateReq 按商品规格组与规格值生成 SKU 组合请求。
//
// 组合由服务端按商品规格组的笛卡尔积生成；已存在的组合（同商品同 spec_key）保留原 SKU
// 与 sku_code，不覆盖其价格/状态/图片。price 等默认值仅用于本次新建的 SKU。
type GenerateReq struct {
	g.Meta        `path:"/admin/products/:id/skus/generate" method:"post" tags:"SKU" summary:"按规格组合生成 SKU"`
	Id            int64        `json:"id" in:"path" v:"required" dc:"商品 id"`
	Price         *json.Number `json:"price" dc:"新建 SKU 的售价（整数分）；缺省取商品价格"`
	OriginalPrice *json.Number `json:"original_price" dc:"新建 SKU 的原价（整数分）；缺省等于售价"`
	ImageUrl      string       `json:"image_url" dc:"新建 SKU 的规格图 URL，可选"`
	Status        *string      `json:"status" dc:"新建 SKU 的状态，默认 enabled"`
}

// GenerateRes 生成 SKU 组合响应（items 为该商品全部 SKU，含本次保留的既有 SKU）。
type GenerateRes struct {
	Created int    `json:"created" dc:"本次新建的 SKU 数量"`
	Kept    int    `json:"kept" dc:"已存在而保留（未改动）的 SKU 数量"`
	Items   []*Sku `json:"items" dc:"该商品全部 SKU（按 id 升序）"`
}
