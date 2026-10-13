package worker

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestScannersStopOnContextCancel 覆盖 AC-007/INV-006：
// 三个后台扫描器（订单超时取消、秒杀对账、秒杀消费）收到 ctx 取消（SIGINT/SIGTERM 语义）后
// 停止领取新任务并退出，wg.Wait 在限定时间内返回。
// 若扫描器未接线 ctx（忽略取消），goroutine 永不退出，wg.Wait 阻塞导致本测试超时失败。
func TestScannersStopOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	StartOrderCancelScanner(ctx, &wg)
	StartFlashSaleReconcileScanner(ctx, &wg)
	StartFlashSaleConsumeScanner(ctx, &wg)

	// 立即取消（模拟停机）：扫描器应在首个 tick 前退出，不触及 DB/Redis 业务调用。
	cancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("扫描器未在 ctx 取消后停止（wg.Wait 阻塞）")
	}
}
