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

// Sku 商品 SKU 结构，前台/后台商品详情内嵌复用。
type Sku struct {
	Id        int64       `json:"id" dc:"SKU id（稳定引用键，不重用）"`
	ProductId int64       `json:"product_id" dc:"所属商品 id"`
	Name      string      `json:"name" dc:"SKU 名称"`
	Price     int64       `json:"price" dc:"价格（整数分）"`
	Status    string      `json:"status" dc:"状态：enabled/disabled"`
	CreatedAt *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// CreateReq 创建 SKU 请求（product_id 在请求体，指向必须存在的商品）。
type CreateReq struct {
	g.Meta    `path:"/admin/skus" method:"post" tags:"SKU" summary:"创建 SKU"`
	ProductId int64       `json:"product_id" v:"required" dc:"所属商品 id（必须存在）"`
	Name      string      `json:"name" v:"required" dc:"SKU 名称，非空且不超过 128 字符"`
	Price     json.Number `json:"price" v:"required" dc:"价格（整数分，0~99999999）"`
	Status    *string     `json:"status" dc:"状态：enabled/disabled，默认 enabled"`
}

// CreateRes 创建 SKU 响应。
type CreateRes struct {
	Sku
}

// UpdateReq 更新 SKU 请求（product_id 不可变）。
type UpdateReq struct {
	g.Meta `path:"/admin/skus/:id" method:"put" tags:"SKU" summary:"更新 SKU"`
	Id     int64        `json:"id" in:"path" v:"required" dc:"SKU id"`
	Name   *string      `json:"name" dc:"SKU 名称，非空且不超过 128 字符"`
	Price  *json.Number `json:"price" dc:"价格（整数分，0~99999999）"`
	Status *string      `json:"status" dc:"状态：enabled/disabled"`
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
