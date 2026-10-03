package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/cart/v1"
)

// ICart 定义购物车服务。
type ICart interface {
	// List 列出当前用户全部条目（含实时计算的异常状态标识）。
	List(ctx context.Context, userID int64) (*v1.ListRes, error)
	// Add 添加 SKU：校验 SKU 存在/启用、商品在售，重复添加原子累加（上限 999）。
	Add(ctx context.Context, userID int64, req *v1.AddReq) (*v1.AddRes, error)
	// UpdateQuantity 修改条目数量（正整数且 ≤ 999）。
	UpdateQuantity(ctx context.Context, userID, itemID, quantity int64) (*v1.UpdateQuantityRes, error)
	// UpdateSelected 勾选/取消勾选条目。
	UpdateSelected(ctx context.Context, userID, itemID int64, selected bool) (*v1.UpdateSelectedRes, error)
	// Delete 删除条目（按 id AND userID 定位，未命中 404）。
	Delete(ctx context.Context, userID, itemID int64) error
}

var localCart ICart

// Cart 返回购物车服务实现。
func Cart() ICart {
	if localCart == nil {
		panic("implement not found for interface ICart, forgot register?")
	}
	return localCart
}

// RegisterCart 注册购物车服务实现。
func RegisterCart(c ICart) {
	localCart = c
}
