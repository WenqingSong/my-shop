package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/recommendation/v1"
)

// IRecommendation 定义推荐位服务。
type IRecommendation interface {
	// Frontend 返回单个推荐位的可售商品快照（仅启用位 + on_shelf 商品，按 sort,id 升序）；
	// 推荐位不存在或禁用时返回空结果（不区分，避免泄露内部状态）。
	Frontend(ctx context.Context, code string) (*v1.FrontendRes, error)
	// AdminList 返回后台全部推荐位列表（全部状态）。
	AdminList(ctx context.Context) (*v1.AdminListRes, error)
	// AdminDetail 返回单个推荐位完整字段与推荐商品关系；不存在返回 15001。
	AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error)
	// Create 校验并持久化新推荐位；code 冲突返回 15002。
	Create(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error)
	// Update 按提交字段更新推荐位（name/status，code 不可变）；不存在返回 15001。
	Update(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error)
	// Delete 物理删除推荐位（级联删除推荐商品关系）；不存在返回 15001。
	Delete(ctx context.Context, id int64) error
	// AddItem 向推荐位添加商品（仅校验商品存在）；重复返回 15005。
	AddItem(ctx context.Context, req *v1.AddItemReq) (*v1.AddItemRes, error)
	// RemoveItem 移除推荐商品关系；不存在返回 15004。
	RemoveItem(ctx context.Context, req *v1.RemoveItemReq) error
	// UpdateSort 按给定商品顺序调整排序；关系不存在返回 15004。
	UpdateSort(ctx context.Context, req *v1.UpdateSortReq) (*v1.UpdateSortRes, error)
}

var localRecommendation IRecommendation

// Recommendation 返回推荐位服务实现。
func Recommendation() IRecommendation {
	if localRecommendation == nil {
		panic("implement not found for interface IRecommendation, forgot register?")
	}
	return localRecommendation
}

// RegisterRecommendation 注册推荐位服务实现。
func RegisterRecommendation(s IRecommendation) {
	localRecommendation = s
}
