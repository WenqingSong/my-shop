package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/categories/v1"
)

// ICategory 定义商品分类（树形 CRUD）服务。
type ICategory interface {
	// List 返回仅含启用项的树形分类。
	List(ctx context.Context) (*v1.ListRes, error)
	// Detail 返回单个分类完整字段。
	Detail(ctx context.Context, id int64) (*v1.DetailRes, error)
	// Create 校验并持久化新分类。
	Create(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error)
	// Update 按提交字段更新分类。
	Update(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error)
	// Delete 物理删除无子分类的分类。
	Delete(ctx context.Context, id int64) error
}

var localCategory ICategory

// Category 返回商品分类服务实现。
func Category() ICategory {
	if localCategory == nil {
		panic("implement not found for interface ICategory, forgot register?")
	}
	return localCategory
}

// RegisterCategory 注册商品分类服务实现。
func RegisterCategory(c ICategory) {
	localCategory = c
}
