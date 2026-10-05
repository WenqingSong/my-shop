package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// 评价状态枚举（API 出参为字符串枚举；DB 存 TINYINT 1/2/3）。
const (
	StatusPublished = "published"  // 公开（1）
	StatusTakenDown = "taken_down" // 已下架（2）
	StatusDeleted   = "deleted"    // 已删除（3）
)

// ReviewItem 公开评价条目（仅 status=published，供商品详情页公开列表）。
type ReviewItem struct {
	Id        int64       `json:"id" dc:"评价 id"`
	ProductId int64       `json:"product_id" dc:"商品 id"`
	SkuId     int64       `json:"sku_id" dc:"SKU id"`
	Rating    int         `json:"rating" dc:"星级 1-5"`
	Content   string      `json:"content" dc:"评价内容"`
	UserId    int64       `json:"user_id" dc:"买家用户 id"`
	CreatedAt *gtime.Time `json:"created_at" dc:"创建时间"`
}

// Review 完整评价条目（含状态与订单项，供本人列表与写接口响应）。
type Review struct {
	Id          int64       `json:"id" dc:"评价 id"`
	UserId      int64       `json:"user_id" dc:"归属用户 id"`
	OrderItemId int64       `json:"order_item_id" dc:"订单项 id"`
	ProductId   int64       `json:"product_id" dc:"商品 id"`
	SkuId       int64       `json:"sku_id" dc:"SKU id"`
	Rating      int         `json:"rating" dc:"星级 1-5"`
	Content     string      `json:"content" dc:"评价内容"`
	Status      string      `json:"status" dc:"状态：published/taken_down/deleted"`
	CreatedAt   *gtime.Time `json:"created_at" dc:"创建时间"`
}

// ListReq 商品公开评价列表与汇总请求（无需 token）。
type ListReq struct {
	g.Meta `path:"/products/:id/reviews" method:"get" tags:"评价" summary:"商品公开评价列表与汇总"`
	Id     int64 `json:"id" in:"path" dc:"商品 id"`
	Page   int   `json:"page" dc:"页码，默认 1"`
	Size   int   `json:"size" dc:"每页数量，默认 20，最大 100"`
}

// ListRes 商品公开评价列表与汇总响应（仅 published，按 id 倒序）。
type ListRes struct {
	Items     []*ReviewItem `json:"items" dc:"评价列表（仅 published）"`
	Page      int           `json:"page" dc:"当前页码"`
	Size      int           `json:"size" dc:"每页数量"`
	Total     int           `json:"total" dc:"已发布评价总数"`
	AvgRating float64       `json:"avg_rating" dc:"平均分（1 位小数）"`
	Count     int           `json:"count" dc:"已发布评价条数"`
}

// CreateReq 提交评价请求（仅登录用户，作用于 Principal.UserID）。
// 归属字段（user_id/product_id/sku_id）由服务端从 Principal 与订单项推导，不信任客户端提交。
type CreateReq struct {
	g.Meta      `path:"/reviews" method:"post" tags:"评价" summary:"提交评价"`
	OrderItemId int64  `json:"order_item_id" dc:"订单项 id（必填，缺失或 ≤0 返回 400）"`
	Rating      int    `json:"rating" dc:"星级 1-5（越界返回 10004）"`
	Content     string `json:"content" dc:"评价内容（trim 后非空且 ≤500 字符）"`
}

// CreateRes 提交评价响应。
type CreateRes struct {
	Review
}

// MyListReq 本人全部评价列表请求（仅登录用户，含全部状态）。
type MyListReq struct {
	g.Meta `path:"/my/reviews" method:"get" tags:"评价" summary:"本人全部评价"`
	Page   int `json:"page" dc:"页码，默认 1"`
	Size   int `json:"size" dc:"每页数量，默认 20，最大 100"`
}

// MyListRes 本人全部评价列表响应（含全部状态，按 id 倒序）。
type MyListRes struct {
	Items []*Review `json:"items" dc:"本人评价列表"`
	Total int       `json:"total" dc:"总数量"`
	Page  int       `json:"page" dc:"当前页码"`
	Size  int       `json:"size" dc:"每页数量"`
}

// UpdateReq 修改本人评价请求（仅本人且 status=published 可改）。
type UpdateReq struct {
	g.Meta  `path:"/reviews/:id" method:"put" tags:"评价" summary:"修改本人评价"`
	Id      int64  `json:"id" in:"path" dc:"评价 id"`
	Rating  int    `json:"rating" dc:"星级 1-5（越界返回 10004）"`
	Content string `json:"content" dc:"评价内容（trim 后非空且 ≤500 字符）"`
}

// UpdateRes 修改本人评价响应。
type UpdateRes struct {
	Review
}

// DeleteReq 删除本人评价请求（仅本人，published → deleted 软删除）。
type DeleteReq struct {
	g.Meta `path:"/reviews/:id" method:"delete" tags:"评价" summary:"删除本人评价"`
	Id     int64 `json:"id" in:"path" dc:"评价 id"`
}

// DeleteRes 删除本人评价响应。
type DeleteRes struct {
	Review
}

// TakeDownReq 下架违规评价请求（仅管理员，持 review:take_down，published → taken_down）。
type TakeDownReq struct {
	g.Meta `path:"/admin/reviews/:id/take-down" method:"post" tags:"评价" summary:"下架违规评价"`
	Id     int64 `json:"id" in:"path" dc:"评价 id"`
}

// TakeDownRes 下架违规评价响应。
type TakeDownRes struct {
	Review
}
