package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/address/v1"
)

// IAddress 定义收货地址服务。所有读写都以已认证的 userID 作为数据归属锚点，
// 不接受客户端传入的 user_id；详情/更新/删除对「不存在」与「他人地址」统一返回 7001。
type IAddress interface {
	// Create 创建 userID 的收货地址；用户首条地址自动置为默认。
	Create(ctx context.Context, userID int64, req *v1.CreateReq) (*v1.CreateRes, error)
	// List 查询 userID 的地址列表（仅本人）。
	List(ctx context.Context, userID int64) (*v1.ListRes, error)
	// Detail 查询 userID 的单个地址详情；不存在或非本人返回 7001（404）。
	Detail(ctx context.Context, userID, id int64) (*v1.DetailRes, error)
	// Update 更新 userID 的地址；不存在或非本人返回 7001（404）。
	Update(ctx context.Context, userID int64, req *v1.UpdateReq) (*v1.UpdateRes, error)
	// Delete 删除 userID 的地址；不存在或非本人返回 7001（404）。
	Delete(ctx context.Context, userID, id int64) error
}

var localAddress IAddress

// Address 返回收货地址服务实现。
func Address() IAddress {
	if localAddress == nil {
		panic("implement not found for interface IAddress, forgot register?")
	}
	return localAddress
}

// RegisterAddress 注册收货地址服务实现。
func RegisterAddress(s IAddress) {
	localAddress = s
}
