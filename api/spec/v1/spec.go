// Package v1 定义商品规格（规格组/规格值）的后台管理 API。
//
// 规格为商品级定义：一个商品可有多个规格组，一个规格组可有多个规格值；
// 规格值只能归属于其所在规格组所属的商品（由 product_specs.product_id 与
// sku_spec_values 复合外键共同保证）。排序字段 sort 升序展示，相同 sort 按 id 升序。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
)

// SpecValue 规格值。
type SpecValue struct {
	Id     int64  `json:"id" dc:"规格值 id"`
	SpecId int64  `json:"spec_id" dc:"所属规格组 id"`
	Value  string `json:"value" dc:"规格值文本（同规格组内唯一）"`
	Sort   int    `json:"sort" dc:"排序值（升序展示）"`
}

// SpecGroup 规格组（含其规格值列表）。
type SpecGroup struct {
	Id        int64        `json:"id" dc:"规格组 id"`
	ProductId int64        `json:"product_id" dc:"所属商品 id"`
	Name      string       `json:"name" dc:"规格组名（如「颜色」「尺码」）"`
	Sort      int          `json:"sort" dc:"排序值（升序展示）"`
	Values    []*SpecValue `json:"values" dc:"规格值列表（按 sort、id 升序）"`
}

// CreateGroupReq 创建规格组请求（product_id 在请求体，指向必须存在的商品）。
type CreateGroupReq struct {
	g.Meta    `path:"/admin/product-specs" method:"post" tags:"商品规格" summary:"创建商品规格组"`
	ProductId int64  `json:"product_id" v:"required" dc:"所属商品 id（必须存在）"`
	Name      string `json:"name" v:"required" dc:"规格组名（trim 后非空，≤32 字符，同商品内唯一）"`
	Sort      int    `json:"sort" dc:"排序值，升序展示，默认 0"`
}

// CreateGroupRes 创建规格组响应。
type CreateGroupRes struct {
	SpecGroup
}

// UpdateGroupReq 更新规格组请求（product_id 不可变）。
type UpdateGroupReq struct {
	g.Meta `path:"/admin/product-specs/:id" method:"put" tags:"商品规格" summary:"更新商品规格组"`
	Id     int64   `json:"id" in:"path" v:"required" dc:"规格组 id"`
	Name   *string `json:"name" dc:"规格组名（trim 后非空，≤32 字符，同商品内唯一）"`
	Sort   *int    `json:"sort" dc:"排序值"`
}

// UpdateGroupRes 更新规格组响应。
type UpdateGroupRes struct {
	SpecGroup
}

// DeleteGroupReq 删除规格组请求（级联删除其规格值；被 SKU 引用的规格值会阻止删除）。
type DeleteGroupReq struct {
	g.Meta `path:"/admin/product-specs/:id" method:"delete" tags:"商品规格" summary:"删除商品规格组"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"规格组 id"`
}

// DeleteGroupRes 删除规格组响应（无业务字段）。
type DeleteGroupRes struct{}

// CreateValueReq 创建规格值请求（规格组 id 在路径，值归属必须与规格组一致）。
type CreateValueReq struct {
	g.Meta `path:"/admin/product-specs/:id/values" method:"post" tags:"商品规格" summary:"创建规格值"`
	Id     int64  `json:"id" in:"path" v:"required" dc:"规格组 id"`
	Value  string `json:"value" v:"required" dc:"规格值文本（trim 后非空，≤32 字符，同规格组内唯一）"`
	Sort   int    `json:"sort" dc:"排序值，升序展示，默认 0"`
}

// CreateValueRes 创建规格值响应。
type CreateValueRes struct {
	SpecValue
}

// UpdateValueReq 更新规格值请求（spec_id 不可变）。
type UpdateValueReq struct {
	g.Meta `path:"/admin/product-spec-values/:id" method:"put" tags:"商品规格" summary:"更新规格值"`
	Id     int64   `json:"id" in:"path" v:"required" dc:"规格值 id"`
	Value  *string `json:"value" dc:"规格值文本（trim 后非空，≤32 字符，同规格组内唯一）"`
	Sort   *int    `json:"sort" dc:"排序值"`
}

// UpdateValueRes 更新规格值响应。
type UpdateValueRes struct {
	SpecValue
}

// DeleteValueReq 删除规格值请求（被 SKU 引用的规格值不可删除）。
type DeleteValueReq struct {
	g.Meta `path:"/admin/product-spec-values/:id" method:"delete" tags:"商品规格" summary:"删除规格值"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"规格值 id"`
}

// DeleteValueRes 删除规格值响应（无业务字段）。
type DeleteValueRes struct{}
