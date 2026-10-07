// Package article 实现用户文章 v1 API（前台公开读 + 登录用户写/点赞/收藏）。
package article

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/article/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 实现用户文章前台 v1 API。
type ControllerV1 struct{}

// NewV1 创建并返回用户文章前台 v1 控制器。
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// List 处理公开文章列表（无需 token）。
func (c *ControllerV1) List(ctx context.Context, req *v1.ListReq) (res *v1.ListRes, err error) {
	return service.Article().List(ctx, req)
}

// Detail 处理公开文章详情（无需 token）。
func (c *ControllerV1) Detail(ctx context.Context, req *v1.DetailReq) (res *v1.DetailRes, err error) {
	return service.Article().Detail(ctx, req.Id)
}

// LikeCount 处理公开点赞数（无需 token）。
func (c *ControllerV1) LikeCount(ctx context.Context, req *v1.LikeCountReq) (res *v1.LikeCountRes, err error) {
	return service.Article().LikeCount(ctx, req.Id)
}

// Create 处理发布文章（仅登录用户，作者身份取自 Principal.UserID）。
func (c *ControllerV1) Create(ctx context.Context, req *v1.CreateReq) (res *v1.CreateRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Article().Create(ctx, userID, req)
}

// Update 处理修改本人文章（仅登录用户）。
func (c *ControllerV1) Update(ctx context.Context, req *v1.UpdateReq) (res *v1.UpdateRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Article().Update(ctx, userID, req)
}

// Delete 处理删除本人文章（仅登录用户，事务清理点赞/收藏）。
func (c *ControllerV1) Delete(ctx context.Context, req *v1.DeleteReq) (res *v1.DeleteRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Article().Delete(ctx, userID, req.Id)
}

// MyList 处理我的文章列表（仅登录用户本人）。
func (c *ControllerV1) MyList(ctx context.Context, req *v1.MyListReq) (res *v1.MyListRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Article().MyList(ctx, userID, req)
}

// Like 处理点赞文章（仅登录用户，幂等）。
func (c *ControllerV1) Like(ctx context.Context, req *v1.LikeReq) (res *v1.LikeRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Article().Like(ctx, userID, req)
}

// Unlike 处理取消点赞（仅登录用户，幂等）。
func (c *ControllerV1) Unlike(ctx context.Context, req *v1.UnlikeReq) (res *v1.UnlikeRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Article().Unlike(ctx, userID, req.Id)
}

// LikeCheck 处理是否已点赞（仅登录用户）。
func (c *ControllerV1) LikeCheck(ctx context.Context, req *v1.LikeCheckReq) (res *v1.LikeCheckRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Article().LikeCheck(ctx, userID, req.Id)
}

// Favorite 处理收藏文章（仅登录用户，幂等）。
func (c *ControllerV1) Favorite(ctx context.Context, req *v1.FavoriteReq) (res *v1.FavoriteRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Article().Favorite(ctx, userID, req)
}

// Unfavorite 处理取消收藏（仅登录用户，幂等）。
func (c *ControllerV1) Unfavorite(ctx context.Context, req *v1.UnfavoriteReq) (res *v1.UnfavoriteRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Article().Unfavorite(ctx, userID, req.Id)
}

// FavoriteCheck 处理是否已收藏（仅登录用户）。
func (c *ControllerV1) FavoriteCheck(ctx context.Context, req *v1.FavoriteCheckReq) (res *v1.FavoriteCheckRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Article().FavoriteCheck(ctx, userID, req.Id)
}

// MyFavorites 处理我的收藏列表（仅登录用户本人）。
func (c *ControllerV1) MyFavorites(ctx context.Context, req *v1.MyFavoritesReq) (res *v1.MyFavoritesRes, err error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	return service.Article().MyFavorites(ctx, userID, req)
}

// currentUserID 从认证中间件注入的 Principal 取当前用户 id；缺失返回 401。
func currentUserID(ctx context.Context) (int64, error) {
	p, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return 0, codes.New(codes.CodeUnauthorized)
	}
	return p.UserID, nil
}
