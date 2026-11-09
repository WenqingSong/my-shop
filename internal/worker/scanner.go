package worker

import (
	"context"
	"sync"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 扫描周期代码默认值（秒），与 manifest/config/config.yaml 保持一致。
const (
	// orderCancelScanIntervalDefault 是订单超时取消扫描周期默认值。
	defaultOrderCancelScanInterval = 60
	// defaultFlashSaleConsumeScanInterval 是秒杀异步消费扫描周期默认值（保证低延迟落单）。
	defaultFlashSaleConsumeScanInterval = 1
	// defaultFlashSaleReconcileScanInterval 是秒杀缓存对账扫描周期默认值。
	defaultFlashSaleReconcileScanInterval = 60
)

// orderCancelScanBatch 是每轮超时取消扫描处理的最大订单数。
const orderCancelScanBatch = 100

// scanInterval 读取扫描周期（秒）并转为 duration。
//
// 配置非法或非正值时记录 Error 级日志并退回代码默认值：扫描周期在 goroutine 启动前
// 读取，签名不携带 error；启动期的 config.Validate 已把非法配置拦成启动失败，
// 此处仅保证后台循环不会因为周期为 0 而退化成忙等。
func scanInterval(ctx context.Context, key config.Key, fallbackSeconds int) time.Duration {
	seconds, err := config.Int(ctx, key)
	if err != nil {
		logx.Error(ctx, "scan_interval_config_read_failed", "读取扫描周期配置失败，按代码默认值处理", logx.Fields{
			"config_key": string(key),
			"error":      err.Error(),
		})
		seconds = fallbackSeconds
	}
	if seconds <= 0 {
		seconds = fallbackSeconds
	}
	return time.Duration(seconds) * time.Second
}

// StartOrderCancelScanner 启动订单超时取消后台扫描器（goroutine + ticker）。
// 周期由 order.cancel_scan_interval 配置（秒，默认 60）；多实例并发依赖「条件状态更新 + RowsAffected」
// 原子闸门保证同一订单只被取消并恢复一次库存，故无需优雅停机通知。
// ctx 取消（SIGINT/SIGTERM）时停止领取新任务；在途单事务由数据库原子性保证回滚或完成，无半成品。
func StartOrderCancelScanner(ctx context.Context, wg *sync.WaitGroup) {
	period := scanInterval(ctx, config.KeyOrderCancelScanInterval, defaultOrderCancelScanInterval)
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(period)
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
	period := scanInterval(ctx, config.KeyFlashSaleConsumeScanInterval, defaultFlashSaleConsumeScanInterval)
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(period)
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
	period := scanInterval(ctx, config.KeyFlashSaleReconcileScanInterval, defaultFlashSaleReconcileScanInterval)
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(period)
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
