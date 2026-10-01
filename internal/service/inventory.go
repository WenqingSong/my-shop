package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/inventory/v1"
)

// IInventory 定义库存服务。
type IInventory interface {
	// Get 查询指定 SKU 的当前库存数量（无记录 = 0；SKU 不存在返回 5001）。
	Get(ctx context.Context, skuID int64) (*v1.Inventory, error)
	// Increase 增加/初始化库存（原子 upsert，产生「增加」流水）。
	// operatorID 为 nil 表示无操作者（供未来订单模块/系统调用）。
	Increase(ctx context.Context, skuID, qty int64, operatorID *int64) (*v1.Inventory, error)
	// Deduct 条件扣减库存（仅库存充足时成功，产生「扣减」流水；不足返回 6001）。
	Deduct(ctx context.Context, skuID, qty int64, operatorID *int64) (*v1.Inventory, error)
	// ListLogs 查询指定 SKU 的库存流水（id 倒序；SKU 不存在返回 5001）。
	ListLogs(ctx context.Context, skuID int64) ([]*v1.Log, error)
}

var localInventory IInventory

// Inventory 返回库存服务实现。
func Inventory() IInventory {
	if localInventory == nil {
		panic("implement not found for interface IInventory, forgot register?")
	}
	return localInventory
}

// RegisterInventory 注册库存服务实现。
func RegisterInventory(s IInventory) {
	localInventory = s
}
