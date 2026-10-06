package v1

import (
	"encoding/json"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	skuv1 "cnb.cool/go-cloud-devops/my-shop/api/sku/v1"
)

// 商品状态枚举（API 出参为字符串枚举；DB 存 TINYINT 0/1/2）。
const (
	StatusDraft    = "draft"
	StatusOnShelf  = "on_shelf"
	StatusOffShelf = "off_shelf"
)

// Product 商品结构，前台/后台查询与写接口复用。
type Product struct {
	Id         int64       `json:"id" dc:"商品 id"`
	Name       string      `json:"name" dc:"商品名"`
	Brand      string      `json:"brand" dc:"品牌"`
	CategoryId int64       `json:"category_id" dc:"分类 id"`
	Price      int64       `json:"price" dc:"价格（整数分）"`
	MainImage  string      `json:"main_image" dc:"主图 URL"`
	Detail     string      `json:"detail" dc:"商品详情"`
	Status     string      `json:"status" dc:"状态：draft/on_shelf/off_shelf"`
	ViewCount  int64       `json:"view_count" dc:"累计浏览量（只读）"`
	Images     []string    `json:"images" dc:"图片 URL 列表（按 sort 升序）"`
	CreatedAt  *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt  *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// ListReq 前台商品列表请求（仅 on_shelf）。
type ListReq struct {
	g.Meta     `path:"/products" method:"get" tags:"商品" summary:"前台商品列表（仅上架）"`
	Page       int    `json:"page" dc:"页码，默认 1"`
	Size       int    `json:"size" dc:"每页数量，默认 20，最大 100"`
	CategoryId *int64 `json:"category_id" dc:"分类 id，精确匹配"`
	Keyword    string `json:"keyword" dc:"关键词，模糊匹配 name/brand"`
	Sort       string `json:"sort" dc:"排序字段：id/price/created_at/updated_at"`
	Order      string `json:"order" dc:"排序方向：asc/desc"`
}

// ListRes 商品列表响应。
type ListRes struct {
	Items []*Product `json:"items" dc:"商品列表"`
	Total int        `json:"total" dc:"总数量"`
	Page  int        `json:"page" dc:"当前页码"`
	Size  int        `json:"size" dc:"每页数量"`
}

// DetailReq 前台商品详情请求（仅 on_shelf 可见）。
type DetailReq struct {
	g.Meta `path:"/products/:id" method:"get" tags:"商品" summary:"前台商品详情（仅上架）"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"商品 id"`
}

// DetailRes 前台商品详情响应（组合 SPU 与 SKU，仅返回 enabled SKU）。
type DetailRes struct {
	Product
	Skus []*skuv1.Sku `json:"skus" dc:"SKU 列表（前台仅 enabled）"`
}

// AdminListReq 后台商品列表请求（全部状态）。
type AdminListReq struct {
	g.Meta     `path:"/admin/products" method:"get" tags:"商品" summary:"后台商品列表（全部状态）"`
	Page       int    `json:"page" dc:"页码，默认 1"`
	Size       int    `json:"size" dc:"每页数量，默认 20，最大 100"`
	CategoryId *int64 `json:"category_id" dc:"分类 id，精确匹配"`
	Keyword    string `json:"keyword" dc:"关键词，模糊匹配 name/brand"`
	Sort       string `json:"sort" dc:"排序字段：id/price/created_at/updated_at"`
	Order      string `json:"order" dc:"排序方向：asc/desc"`
}

// AdminListRes 后台商品列表响应。
type AdminListRes struct {
	Items []*Product `json:"items" dc:"商品列表"`
	Total int        `json:"total" dc:"总数量"`
	Page  int        `json:"page" dc:"当前页码"`
	Size  int        `json:"size" dc:"每页数量"`
}

// AdminDetailReq 后台商品详情请求（全部状态）。
type AdminDetailReq struct {
	g.Meta `path:"/admin/products/:id" method:"get" tags:"商品" summary:"后台商品详情（全部状态）"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"商品 id"`
}

// AdminDetailRes 后台商品详情响应（组合 SPU 与 SKU，返回全部状态 SKU）。
type AdminDetailRes struct {
	Product
	Skus []*skuv1.Sku `json:"skus" dc:"SKU 列表（全部状态）"`
}

// CreateReq 创建商品请求（status 强制 draft，不接受客户端指定）。
type CreateReq struct {
	g.Meta     `path:"/admin/products" method:"post" tags:"商品" summary:"创建商品"`
	Name       string      `json:"name" dc:"商品名，非空且不超过 128 字符"`
	Brand      string      `json:"brand" dc:"品牌，可选，不超过 64 字符"`
	CategoryId int64       `json:"category_id" dc:"分类 id（必须为存在且启用的叶子分类）"`
	Price      json.Number `json:"price" dc:"价格（整数分，0~99999999）"`
	MainImage  string      `json:"main_image" dc:"主图 URL，可选，不超过 512 字符"`
	Detail     string      `json:"detail" dc:"商品详情，可选"`
	Images     []string    `json:"images" dc:"图片 URL 列表，可选"`
}

// CreateRes 创建商品响应（status 恒为 draft）。
type CreateRes struct {
	Product
}

// UpdateReq 更新商品请求（普通更新不能改变 status；提交合法 status 被忽略、非法值拒绝）。
type UpdateReq struct {
	g.Meta     `path:"/admin/products/:id" method:"put" tags:"商品" summary:"更新商品"`
	Id         int64        `json:"id" in:"path" v:"required" dc:"商品 id"`
	Name       *string      `json:"name" dc:"商品名"`
	Brand      *string      `json:"brand" dc:"品牌"`
	CategoryId *int64       `json:"category_id" dc:"分类 id（变更时须为存在且启用的叶子分类）"`
	Price      *json.Number `json:"price" dc:"价格（整数分）"`
	MainImage  *string      `json:"main_image" dc:"主图 URL"`
	Detail     *string      `json:"detail" dc:"商品详情"`
	Status     *string      `json:"status" dc:"普通更新不改状态：合法值被忽略、非法值拒绝"`
	Images     *[]string    `json:"images" dc:"图片 URL 列表；提供则全量替换，未提供则保留"`
}

// UpdateRes 更新商品响应。
type UpdateRes struct {
	Product
}

// OnShelfReq 上架商品请求（无请求体，仅 :id）。
type OnShelfReq struct {
	g.Meta `path:"/admin/products/:id/on-shelf" method:"post" tags:"商品" summary:"上架商品"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"商品 id"`
}

// OnShelfRes 上架商品响应。
type OnShelfRes struct {
	Product
}

// OffShelfReq 下架商品请求（无请求体，仅 :id）。
type OffShelfReq struct {
	g.Meta `path:"/admin/products/:id/off-shelf" method:"post" tags:"商品" summary:"下架商品"`
	Id     int64 `json:"id" in:"path" v:"required" dc:"商品 id"`
}

// OffShelfRes 下架商品响应。
type OffShelfRes struct {
	Product
}
