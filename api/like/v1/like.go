// Package v1 定义商品点赞（Like）前台 API 契约。
package v1

import "github.com/gogf/gf/v2/frame/g"

// LikeReq 点赞请求（仅登录用户，作用于 Principal.UserID；重复点赞幂等成功）。
type LikeReq struct {
	g.Meta    `path:"/likes" method:"post" tags:"点赞" summary:"点赞商品"`
	ProductId int64 `json:"product_id" dc:"商品 id（必填，缺失或 ≤0 返回 400）"`
}

// LikeRes 点赞响应（点赞后本人对商品必为已点赞）。
type LikeRes struct {
	Liked bool `json:"liked" dc:"是否已点赞（点赞成功后恒为 true）"`
}

// CancelReq 取消点赞请求（按 product_id 幂等，未点赞/不存在也成功）。
type CancelReq struct {
	g.Meta    `path:"/likes/:product_id" method:"delete" tags:"点赞" summary:"取消点赞"`
	ProductId int64 `json:"product_id" in:"path" v:"required" dc:"商品 id"`
}

// CancelRes 取消点赞响应（无业务字段）。
type CancelRes struct{}

// CheckReq 是否已点赞请求（独立鉴权查询，供商品详情展示点赞态）。
type CheckReq struct {
	g.Meta    `path:"/likes/check" method:"get" tags:"点赞" summary:"是否已点赞"`
	ProductId int64 `json:"product_id" in:"query" dc:"商品 id（必填，缺失或 ≤0 返回 400）"`
}

// CheckRes 是否已点赞响应。
type CheckRes struct {
	Liked bool `json:"liked" dc:"当前用户是否已点赞该商品"`
}

// CountReq 公开点赞数请求（无需 token）。
type CountReq struct {
	g.Meta    `path:"/likes/count" method:"get" tags:"点赞" summary:"公开点赞数"`
	ProductId int64 `json:"product_id" in:"query" dc:"商品 id（必填，缺失或 ≤0 返回 400）"`
}

// CountRes 公开点赞数响应。
type CountRes struct {
	Count int `json:"count" dc:"该商品点赞数（实时 COUNT 聚合）"`
}
