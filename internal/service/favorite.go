package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/favorite/v1"
)

// IFavorite 定义商品收藏服务。
type IFavorite interface {
	// Add 添加收藏：校验商品存在且在售（on_shelf），重复收藏幂等成功（唯一约束兜底并发）。
	Add(ctx context.Context, userID int64, req *v1.AddReq) (*v1.AddRes, error)
	// Remove 取消收藏：按 product_id 作用于本人，未收藏/不存在也幂等成功（no-op）。
	Remove(ctx context.Context, userID, productID int64) error
	// List 查询本人收藏列表（分页，实时联查商品信息并标识可用性）。
	List(ctx context.Context, userID int64, req *v1.ListReq) (*v1.ListRes, error)
	// Check 查询本人对指定商品是否已收藏。
	Check(ctx context.Context, userID, productID int64) (*v1.CheckRes, error)
}

var localFavorite IFavorite

// Favorite 返回商品收藏服务实现。
func Favorite() IFavorite {
	if localFavorite == nil {
		panic("implement not found for interface IFavorite, forgot register?")
	}
	return localFavorite
}

// RegisterFavorite 注册商品收藏服务实现。
func RegisterFavorite(s IFavorite) {
	localFavorite = s
}
