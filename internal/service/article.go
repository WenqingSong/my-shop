package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/article/v1"
)

// IArticle 定义用户文章服务。
type IArticle interface {
	// List 公开文章列表（无需登录，分页，按 id 倒序）。
	List(ctx context.Context, req *v1.ListReq) (*v1.ListRes, error)
	// Detail 公开文章详情（无需登录）；不存在返回 16001。
	Detail(ctx context.Context, id int64) (*v1.DetailRes, error)
	// Create 发布文章：作者身份取自 Principal.UserID，忽略客户端身份字段。
	Create(ctx context.Context, userID int64, req *v1.CreateReq) (*v1.CreateRes, error)
	// Update 修改本人文章；非本人或不存在统一 16001。
	Update(ctx context.Context, userID int64, req *v1.UpdateReq) (*v1.UpdateRes, error)
	// Delete 删除本人文章 + 事务清理点赞/收藏；非本人或不存在统一 16001。
	Delete(ctx context.Context, userID, id int64) (*v1.DeleteRes, error)
	// MyList 查询本人文章列表（分页）。
	MyList(ctx context.Context, userID int64, req *v1.MyListReq) (*v1.MyListRes, error)
	// Like 点赞文章（幂等，唯一约束兜底并发）；目标不存在返回 16001。
	Like(ctx context.Context, userID int64, req *v1.LikeReq) (*v1.LikeRes, error)
	// Unlike 取消点赞（幂等 no-op）。
	Unlike(ctx context.Context, userID, id int64) (*v1.UnlikeRes, error)
	// LikeCheck 查询本人是否已点赞。
	LikeCheck(ctx context.Context, userID, id int64) (*v1.LikeCheckRes, error)
	// LikeCount 查询公开点赞数（无需登录，实时 COUNT）。
	LikeCount(ctx context.Context, id int64) (*v1.LikeCountRes, error)
	// Favorite 收藏文章（幂等，唯一约束兜底并发）；目标不存在返回 16001。
	Favorite(ctx context.Context, userID int64, req *v1.FavoriteReq) (*v1.FavoriteRes, error)
	// Unfavorite 取消收藏（幂等 no-op）。
	Unfavorite(ctx context.Context, userID, id int64) (*v1.UnfavoriteRes, error)
	// FavoriteCheck 查询本人是否已收藏。
	FavoriteCheck(ctx context.Context, userID, id int64) (*v1.FavoriteCheckRes, error)
	// MyFavorites 查询本人收藏列表（分页，按收藏时间倒序）。
	MyFavorites(ctx context.Context, userID int64, req *v1.MyFavoritesReq) (*v1.MyFavoritesRes, error)
}

var localArticle IArticle

// Article 返回用户文章服务实现。
func Article() IArticle {
	if localArticle == nil {
		panic("implement not found for interface IArticle, forgot register?")
	}
	return localArticle
}

// RegisterArticle 注册用户文章服务实现。
func RegisterArticle(s IArticle) {
	localArticle = s
}
