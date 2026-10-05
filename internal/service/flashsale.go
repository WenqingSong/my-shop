package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
)

// IFlashSale 定义秒杀服务。
type IFlashSale interface {
	// CreateActivity 创建秒杀活动（含 SKU 绑定、秒杀价、秒杀库存、起止时间），事务写入活动与绑定。
	CreateActivity(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error)
	// UpdateActivity 更新秒杀活动（改名称/状态/时间/秒杀价/库存），存在性校验后事务更新。
	UpdateActivity(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error)
	// CreateOrder 秒杀下单：单事务「时间窗校验 + 条件扣秒杀库存 + 创建订单」，幂等/一人一单由 DB 唯一约束兜底。
	CreateOrder(ctx context.Context, userID, activityID int64, req *v1.CreateOrderReq) (*v1.CreateOrderRes, error)
}

var localFlashSale IFlashSale

// FlashSale 返回秒杀服务实现。
func FlashSale() IFlashSale {
	if localFlashSale == nil {
		panic("implement not found for interface IFlashSale, forgot register?")
	}
	return localFlashSale
}

// RegisterFlashSale 注册秒杀服务实现。
func RegisterFlashSale(s IFlashSale) {
	localFlashSale = s
}
