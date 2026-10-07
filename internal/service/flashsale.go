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
	// CreateOrder 秒杀下单（V3 异步）：Redis Lua 闸门快速失败 + 入队持久受理（status=queued）快速返回，不等待订单创建；
	// 幂等/一人一单由 DB 唯一约束兜底。Redis 不可用 → fail-closed（503），不落 request、不预扣、不直接同步落单。
	CreateOrder(ctx context.Context, userID, activityID int64, req *v1.CreateOrderReq) (*v1.CreateOrderRes, error)
	// GetOrderResult 查询本人秒杀下单结果（排队/成功/失败/死信），归属隔离；不存在/非本人统一 404。
	GetOrderResult(ctx context.Context, userID, activityID int64, idempotencyKey string) (*v1.GetOrderResultRes, error)
	// ConsumeQueued 消费秒杀异步请求队列（供后台扫描器复用）：出队 queued 请求并逐条事务落单/失败/重试/死信。返回本轮处理条数。
	ConsumeQueued(ctx context.Context, limit int) (int, error)
	// SyncCache 同步指定活动缓存到 Redis（预热或失效），供管理端、后台扫描器与测试复用。
	SyncCache(ctx context.Context, activityID int64) error
	// ReconcileCache 对账/回补（供后台扫描器复用）：扫描启用且未结束的活动，将 Redis remaining 刷成 total_stock - sold - inflight_queued；
	// 并覆盖「已结束、尚在 grace 窗口」活动的终态收敛（remaining = total_stock - sold + 失效缓存）。
	ReconcileCache(ctx context.Context, limit int) (int, error)
	// RepairRequest 人工修复异常请求（仅 dead→queued），与审计记录同事务写入 flash_sale_request_audits（append-only）。
	RepairRequest(ctx context.Context, adminID int64, req *v1.RepairRequestReq) (*v1.RepairRequestRes, error)
	// ListRequestAudits 查询请求的修复审计记录（append-only）。
	ListRequestAudits(ctx context.Context, requestID int64) (*v1.ListRequestAuditsRes, error)
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
