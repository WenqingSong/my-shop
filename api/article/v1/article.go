// Package v1 定义用户文章（Article）前台 API 契约。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
)

// Article 完整文章（详情与写接口响应，含正文）。
type Article struct {
	Id             int64       `json:"id" dc:"文章 id"`
	AuthorId       int64       `json:"author_id" dc:"作者用户 id（取自 Principal.UserID）"`
	AuthorUsername string      `json:"author_username" dc:"作者用户名（LEFT JOIN users.username）"`
	Title          string      `json:"title" dc:"标题"`
	Content        string      `json:"content" dc:"正文"`
	CreatedAt      *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt      *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// ArticleItem 列表条目（不含正文）。
type ArticleItem struct {
	Id             int64       `json:"id" dc:"文章 id"`
	AuthorId       int64       `json:"author_id" dc:"作者用户 id"`
	AuthorUsername string      `json:"author_username" dc:"作者用户名"`
	Title          string      `json:"title" dc:"标题"`
	CreatedAt      *gtime.Time `json:"created_at" dc:"创建时间"`
	UpdatedAt      *gtime.Time `json:"updated_at" dc:"更新时间"`
}

// ListReq 公开文章列表请求（无需 token，分页，按 id 倒序）。
type ListReq struct {
	g.Meta `path:"/articles" method:"get" tags:"文章" summary:"公开文章列表"`
	Page   int `json:"page" dc:"页码，默认 1"`
	Size   int `json:"size" dc:"每页数量，默认 20，最大 100"`
}

// ListRes 公开文章列表响应。
type ListRes struct {
	Items []*ArticleItem `json:"items" dc:"文章列表（不含正文）"`
	Total int            `json:"total" dc:"总数量"`
	Page  int            `json:"page" dc:"当前页码"`
	Size  int            `json:"size" dc:"每页数量"`
}

// DetailReq 公开文章详情请求（无需 token）。
type DetailReq struct {
	g.Meta `path:"/articles/:id" method:"get" tags:"文章" summary:"公开文章详情"`
	Id     int64 `json:"id" in:"path" dc:"文章 id"`
}

// DetailRes 公开文章详情响应。
type DetailRes struct {
	Article
}

// CreateReq 发布文章请求（仅登录用户，作者身份取自 Principal.UserID，客户端身份字段被忽略）。
type CreateReq struct {
	g.Meta  `path:"/articles" method:"post" tags:"文章" summary:"发布文章"`
	Title   string `json:"title" dc:"标题（trim 后非空且 ≤64 字符）"`
	Content string `json:"content" dc:"正文（trim 后非空且 ≤10000 字符）"`
}

// CreateRes 发布文章响应。
type CreateRes struct {
	Article
}

// UpdateReq 修改文章请求（仅作者本人）。
type UpdateReq struct {
	g.Meta  `path:"/articles/:id" method:"put" tags:"文章" summary:"修改本人文章"`
	Id      int64  `json:"id" in:"path" dc:"文章 id"`
	Title   string `json:"title" dc:"标题（trim 后非空且 ≤64 字符）"`
	Content string `json:"content" dc:"正文（trim 后非空且 ≤10000 字符）"`
}

// UpdateRes 修改文章响应。
type UpdateRes struct {
	Article
}

// DeleteReq 删除文章请求（仅作者本人，硬删除 + 事务清理点赞/收藏）。
type DeleteReq struct {
	g.Meta `path:"/articles/:id" method:"delete" tags:"文章" summary:"删除本人文章"`
	Id     int64 `json:"id" in:"path" dc:"文章 id"`
}

// DeleteRes 删除文章响应（无业务字段）。
type DeleteRes struct{}

// MyListReq 我的文章列表请求（仅登录用户本人）。
type MyListReq struct {
	g.Meta `path:"/my/articles" method:"get" tags:"文章" summary:"我的文章列表"`
	Page   int `json:"page" dc:"页码，默认 1"`
	Size   int `json:"size" dc:"每页数量，默认 20，最大 100"`
}

// MyListRes 我的文章列表响应。
type MyListRes struct {
	Items []*Article `json:"items" dc:"本人文章列表"`
	Total int        `json:"total" dc:"总数量"`
	Page  int        `json:"page" dc:"当前页码"`
	Size  int        `json:"size" dc:"每页数量"`
}

// LikeCountReq 公开点赞数请求（无需 token）。
type LikeCountReq struct {
	g.Meta `path:"/articles/:id/like/count" method:"get" tags:"文章" summary:"公开点赞数"`
	Id     int64 `json:"id" in:"path" dc:"文章 id"`
}

// LikeCountRes 公开点赞数响应。
type LikeCountRes struct {
	Count int `json:"count" dc:"该文章点赞数（实时 COUNT 聚合）"`
}

// LikeReq 点赞文章请求（仅登录用户，幂等）。
type LikeReq struct {
	g.Meta `path:"/articles/:id/like" method:"post" tags:"文章" summary:"点赞文章"`
	Id     int64 `json:"id" in:"path" dc:"文章 id"`
}

// LikeRes 点赞文章响应（点赞后本人必为已点赞）。
type LikeRes struct {
	Liked bool `json:"liked" dc:"是否已点赞（成功后恒为 true）"`
}

// UnlikeReq 取消点赞请求（仅登录用户，幂等）。
type UnlikeReq struct {
	g.Meta `path:"/articles/:id/like" method:"delete" tags:"文章" summary:"取消点赞"`
	Id     int64 `json:"id" in:"path" dc:"文章 id"`
}

// UnlikeRes 取消点赞响应（无业务字段）。
type UnlikeRes struct{}

// LikeCheckReq 是否已点赞请求（仅登录用户，独立鉴权查询）。
type LikeCheckReq struct {
	g.Meta `path:"/articles/:id/like/check" method:"get" tags:"文章" summary:"是否已点赞"`
	Id     int64 `json:"id" in:"path" dc:"文章 id"`
}

// LikeCheckRes 是否已点赞响应。
type LikeCheckRes struct {
	Liked bool `json:"liked" dc:"当前用户是否已点赞该文章"`
}

// FavoriteReq 收藏文章请求（仅登录用户，幂等）。
type FavoriteReq struct {
	g.Meta `path:"/articles/:id/favorite" method:"post" tags:"文章" summary:"收藏文章"`
	Id     int64 `json:"id" in:"path" dc:"文章 id"`
}

// FavoriteRes 收藏文章响应（收藏后本人必为已收藏）。
type FavoriteRes struct {
	Favorited bool `json:"favorited" dc:"是否已收藏（成功后恒为 true）"`
}

// UnfavoriteReq 取消收藏请求（仅登录用户，幂等）。
type UnfavoriteReq struct {
	g.Meta `path:"/articles/:id/favorite" method:"delete" tags:"文章" summary:"取消收藏"`
	Id     int64 `json:"id" in:"path" dc:"文章 id"`
}

// UnfavoriteRes 取消收藏响应（无业务字段）。
type UnfavoriteRes struct{}

// FavoriteCheckReq 是否已收藏请求（仅登录用户，独立鉴权查询）。
type FavoriteCheckReq struct {
	g.Meta `path:"/articles/:id/favorite/check" method:"get" tags:"文章" summary:"是否已收藏"`
	Id     int64 `json:"id" in:"path" dc:"文章 id"`
}

// FavoriteCheckRes 是否已收藏响应。
type FavoriteCheckRes struct {
	Favorited bool `json:"favorited" dc:"当前用户是否已收藏该文章"`
}

// MyFavoritesReq 我的收藏列表请求（仅登录用户本人）。
type MyFavoritesReq struct {
	g.Meta `path:"/my/articles/favorites" method:"get" tags:"文章" summary:"我的收藏列表"`
	Page   int `json:"page" dc:"页码，默认 1"`
	Size   int `json:"size" dc:"每页数量，默认 20，最大 100"`
}

// MyFavoritesRes 我的收藏列表响应（按收藏时间倒序）。
type MyFavoritesRes struct {
	Items []*ArticleItem `json:"items" dc:"收藏的文章列表（不含正文）"`
	Total int            `json:"total" dc:"总数量"`
	Page  int            `json:"page" dc:"当前页码"`
	Size  int            `json:"size" dc:"每页数量"`
}
