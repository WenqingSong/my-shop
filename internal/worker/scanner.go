package worker

import (
	"context"
	"sync"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// orderCancelScanBatch 是每轮超时取消扫描处理的最大订单数。
const orderCancelScanBatch = 100

// StartOrderCancelScanner 启动订单超时取消后台扫描器（goroutine + ticker）。
// 周期由 order.cancel_scan_interval 配置（秒，默认 60）；多实例并发依赖「条件状态更新 + RowsAffected」
// 原子闸门保证同一订单只被取消并恢复一次库存，故无需优雅停机通知。
// ctx 取消（SIGINT/SIGTERM）时停止领取新任务；在途单事务由数据库原子性保证回滚或完成，无半成品。
func StartOrderCancelScanner(ctx context.Context, wg *sync.WaitGroup) {
	interval := g.Cfg().MustGet(ctx, "order.cancel_scan_interval", 60).Int()
	if interval <= 0 {
		interval = 60
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := service.Order().CancelExpired(ctx, orderCancelScanBatch); err != nil {
					logx.Warning(ctx, "order_cancel_scan_failed", "订单超时取消扫描失败", logx.Fields{"error": err.Error()})
				}
			}
		}
	}()
}

// flashSaleReconcileScanBatch 是每轮秒杀缓存对账扫描处理的最大活动数。
const flashSaleReconcileScanBatch = 100

// flashSaleConsumeScanBatch 是每轮秒杀异步消费扫描处理的最大请求数。
const flashSaleConsumeScanBatch = 100

// StartFlashSaleConsumeScanner 启动秒杀异步消费后台扫描器（goroutine + ticker）。
// 周期由 flash_sale.consume_scan_interval 配置（秒，默认 1，保证低延迟落单）；
// 出队依赖 FOR UPDATE SKIP LOCKED + 状态原子更新保证同一请求只被处理一次，多实例并发安全。
// ctx 取消时停止领取新任务；在途消费单事务回滚后请求保持 queued，重启后由重试/对账恢复。
func StartFlashSaleConsumeScanner(ctx context.Context, wg *sync.WaitGroup) {
	interval := g.Cfg().MustGet(ctx, "flash_sale.consume_scan_interval", 1).Int()
	if interval <= 0 {
		interval = 1
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := service.FlashSale().ConsumeQueued(ctx, flashSaleConsumeScanBatch); err != nil {
					logx.Warning(ctx, "flashsale_consume_scan_failed", "秒杀异步消费扫描失败", logx.Fields{"error": err.Error()})
				}
			}
		}
	}()
}

// StartFlashSaleReconcileScanner 启动秒杀缓存对账后台扫描器（goroutine + ticker）。
// 周期由 flash_sale.reconcile_scan_interval 配置（秒，默认 60）；对账为无状态、幂等写入
// 权威值（remaining = total_stock - sold），多实例并发安全。
// ctx 取消时停止领取新任务；对账为幂等 SET，中断无副作用。
func StartFlashSaleReconcileScanner(ctx context.Context, wg *sync.WaitGroup) {
	interval := g.Cfg().MustGet(ctx, "flash_sale.reconcile_scan_interval", 60).Int()
	if interval <= 0 {
		interval = 60
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := service.FlashSale().ReconcileCache(ctx, flashSaleReconcileScanBatch); err != nil {
					logx.Warning(ctx, "flashsale_reconcile_scan_failed", "秒杀缓存对账扫描失败", logx.Fields{"error": err.Error()})
				}
			}
		}
	}()
}
