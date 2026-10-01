package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/sku/v1"
)

// ISku 定义 SKU 服务。
type ISku interface {
	// Create 创建 SKU（校验商品存在性与字段合法性，name 撞名 409）。
	Create(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error)
	// Update 更新 SKU 的 name/price/status（product_id 不可变）。
	Update(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error)
	// Delete 物理删除 SKU（不存在返回 404）。
	Delete(ctx context.Context, id int64) error
	// ListByProduct 按商品查询 SKU（供商品详情组合），onlyEnabled=true 时仅返回 enabled，按 id 升序。
	ListByProduct(ctx context.Context, productID int64, onlyEnabled bool) ([]*v1.Sku, error)
}

var localSku ISku

// Sku 返回 SKU 服务实现。
func Sku() ISku {
	if localSku == nil {
		panic("implement not found for interface ISku, forgot register?")
	}
	return localSku
}

// RegisterSku 注册 SKU 服务实现。
func RegisterSku(s ISku) {
	localSku = s
}
