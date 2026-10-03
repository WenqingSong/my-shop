package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/product/v1"
)

// IProduct 定义商品 SPU 服务。
type IProduct interface {
	// List 前台列表：仅 on_shelf，支持分页/分类筛选/关键词/排序。
	List(ctx context.Context, req *v1.ListReq) (*v1.ListRes, error)
	// Detail 前台详情：仅 on_shelf 可见，否则 404。
	Detail(ctx context.Context, id int64) (*v1.DetailRes, error)
	// AdminList 后台列表：查看全部状态。
	AdminList(ctx context.Context, req *v1.AdminListReq) (*v1.AdminListRes, error)
	// AdminDetail 后台详情：查看全部状态。
	AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error)
	// Create 创建商品（强制 draft）。
	Create(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error)
	// Update 更新商品（普通更新不能改变状态）。
	Update(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error)
	// OnShelf 上架（合法迁移 draft→on_shelf、off_shelf→on_shelf）。
	OnShelf(ctx context.Context, id int64) (*v1.OnShelfRes, error)
	// OffShelf 下架（合法迁移 on_shelf→off_shelf）。
	OffShelf(ctx context.Context, id int64) (*v1.OffShelfRes, error)
	// CountByCategory 统计分类下商品数量（供分类删除保护调用）。
	CountByCategory(ctx context.Context, categoryID int64) (int64, error)
	// Exists 判断商品是否存在（供 SKU 校验 product_id 存在性）。
	Exists(ctx context.Context, id int64) (bool, error)
	// GetByID 按 id 查询单个商品全字段（含任意 status），不存在返回 nil（供购物车读取下架商品状态）。
	GetByID(ctx context.Context, id int64) (*v1.Product, error)
}

var localProduct IProduct

// Product 返回商品 SPU 服务实现。
func Product() IProduct {
	if localProduct == nil {
		panic("implement not found for interface IProduct, forgot register?")
	}
	return localProduct
}

// RegisterProduct 注册商品 SPU 服务实现。
func RegisterProduct(p IProduct) {
	localProduct = p
}
