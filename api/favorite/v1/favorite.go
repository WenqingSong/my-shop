package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// FavoriteItem 收藏条目（列表与添加响应复用）。
// 商品信息（名称/主图/价格）不落库，查询时实时联查 products；
// 商品被下架/删除后收藏保留为软引用，available/unavailable_reason 动态标识其可用性。
type FavoriteItem struct {
	Id                int64       `json:"id" dc:"收藏条目 id"`
	ProductId         int64       `json:"product_id" dc:"商品 id"`
	ProductName       string      `json:"product_name" dc:"商品名（实时联查；商品删除时为空）"`
	ProductMainImage  string      `json:"product_main_image" dc:"商品主图（实时联查；商品删除时为空）"`
	ProductPrice      *int64      `json:"product_price" dc:"商品价格整数分（实时联查；商品删除时为 null）"`
	Available         bool        `json:"available" dc:"商品当前是否 on_shelf"`
	UnavailableReason string      `json:"unavailable_reason" dc:"不可用原因：空/off_shelf/product_deleted"`
	CreatedAt         *gtime.Time `json:"created_at" dc:"收藏时间"`
}

// AddReq 添加收藏请求（仅登录用户，作用于 Principal.UserID；重复收藏幂等成功）。
type AddReq struct {
	g.Meta    `path:"/favorites" method:"post" tags:"收藏" summary:"添加收藏"`
	ProductId int64 `json:"product_id" dc:"商品 id（必填，缺失或 ≤0 返回 400）"`
}

// AddRes 添加收藏响应（返回该收藏条目，幂等重复时返回既有条目）。
type AddRes struct {
	FavoriteItem
}

// RemoveReq 取消收藏请求（按 product_id 幂等，未收藏/不存在也成功）。
type RemoveReq struct {
	g.Meta    `path:"/favorites/:product_id" method:"delete" tags:"收藏" summary:"取消收藏"`
	ProductId int64 `json:"product_id" in:"path" v:"required" dc:"商品 id"`
}

// RemoveRes 取消收藏响应（无业务字段）。
type RemoveRes struct{}

// ListReq 收藏列表请求（分页，仅登录用户本人收藏）。
type ListReq struct {
	g.Meta `path:"/favorites" method:"get" tags:"收藏" summary:"收藏列表"`
	Page   int `json:"page" dc:"页码，默认 1"`
	Size   int `json:"size" dc:"每页数量，默认 20，最大 100"`
}

// ListRes 收藏列表响应（按收藏时间倒序）。
type ListRes struct {
	Items []*FavoriteItem `json:"items" dc:"收藏列表"`
	Total int             `json:"total" dc:"总数量"`
	Page  int             `json:"page" dc:"当前页码"`
	Size  int             `json:"size" dc:"每页数量"`
}

// CheckReq 是否已收藏请求（独立鉴权查询，供商品详情展示收藏态）。
type CheckReq struct {
	g.Meta    `path:"/favorites/check" method:"get" tags:"收藏" summary:"是否已收藏"`
	ProductId int64 `json:"product_id" in:"query" dc:"商品 id（必填，缺失或 ≤0 返回 400）"`
}

// CheckRes 是否已收藏响应。
type CheckRes struct {
	Favorited bool `json:"favorited" dc:"当前用户是否已收藏该商品"`
}
