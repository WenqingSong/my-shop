package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/like/v1"
)

// ILike 定义商品点赞服务。
type ILike interface {
	// Like 点赞：校验商品存在且在售（on_shelf），重复点赞幂等成功（唯一约束兜底并发）。
	Like(ctx context.Context, userID int64, req *v1.LikeReq) (*v1.LikeRes, error)
	// Cancel 取消点赞：按 product_id 作用于本人，未点赞/不存在也幂等成功（no-op）。
	Cancel(ctx context.Context, userID, productID int64) error
	// Check 查询本人对指定商品是否已点赞。
	Check(ctx context.Context, userID, productID int64) (*v1.CheckRes, error)
	// Count 查询指定商品的公开点赞数（实时 COUNT 聚合，无需登录）。
	Count(ctx context.Context, productID int64) (*v1.CountRes, error)
}

var localLike ILike

// Like 返回商品点赞服务实现。
func Like() ILike {
	if localLike == nil {
		panic("implement not found for interface ILike, forgot register?")
	}
	return localLike
}

// RegisterLike 注册商品点赞服务实现。
func RegisterLike(s ILike) {
	localLike = s
}
