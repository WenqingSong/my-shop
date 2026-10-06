package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/banner/v1"
)

// IBanner 定义轮播图服务。
type IBanner interface {
	// List 返回公开启用的轮播图列表（status=1，按 sort 升序、同值按 id 升序）。
	List(ctx context.Context) (*v1.ListRes, error)
	// AdminList 返回后台全部轮播图列表（全部状态，按 sort 升序、同值按 id 升序）。
	AdminList(ctx context.Context) (*v1.AdminListRes, error)
	// AdminDetail 返回单个轮播图完整字段（全部状态）；不存在返回 14001。
	AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error)
	// Create 校验并持久化新轮播图。
	Create(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error)
	// Update 按提交字段更新轮播图；不存在返回 14001。
	Update(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error)
	// Delete 物理删除轮播图（仅删 DB 记录，不删本地图片文件）；不存在返回 14001。
	Delete(ctx context.Context, id int64) error
}

var localBanner IBanner

// Banner 返回轮播图服务实现。
func Banner() IBanner {
	if localBanner == nil {
		panic("implement not found for interface IBanner, forgot register?")
	}
	return localBanner
}

// RegisterBanner 注册轮播图服务实现。
func RegisterBanner(s IBanner) {
	localBanner = s
}
