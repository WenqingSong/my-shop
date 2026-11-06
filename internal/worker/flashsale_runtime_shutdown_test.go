package worker

// T2-03 终止预算回归：
//  1. 关闭预算接近耗尽时，后续组件仍按 Publisher → Consumer → Producer → Scanner 顺序执行；
//  2. 某个组件超时（占用整个预算）时，后续组件仍然得到关闭机会，Shutdown 在预算附近返回。
//
// 这些用例是进程级 SIGTERM 测试的补充：进程级用例证明真实信号路径，
// 这里用可控替身证明「预算耗尽」这一分支不会被静默跳过。

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/logic/flashsale"
)

// probeEventIndex 返回事件首次出现的下标，未出现返回 -1。
func probeEventIndex(events []string, name string) int {
	for i, event := range events {
		if event == name {
			return i
		}
	}
	return -1
}

// waitProbeEvent 等待事件出现（并发关闭路径下事件写入有微小延迟）。
func waitProbeEvent(t *testing.T, probe *runtimeProbe, name string, timeout time.Duration) {
	t.Helper()
	waitFor(t, timeout, "事件 "+name, func() bool {
		return probeEventIndex(probe.eventLog(), name) >= 0
	})
}

// TestFlashSaleRuntimeShutdownNearBudgetExhaustionKeepsOrder 覆盖「预算接近耗尽但未超时」：
// Publisher 占掉大部分预算后，Consumer / Producer / Scanner 仍然被依次停止，顺序不变。
func TestFlashSaleRuntimeShutdownNearBudgetExhaustionKeepsOrder(t *testing.T) {
	probe := newRuntimeProbe()
	probe.outboxBehavior = func(ctx context.Context, _ int, _ int) (flashsale.OutboxStats, error) {
		// 忽略取消并占用大部分关闭预算（预算 500ms，本轮 300ms），
		// 留给后续组件的余量足以让它们真实执行但明显被压缩。
		time.Sleep(300 * time.Millisecond)
		if ctx.Err() != nil {
			probe.record("publisher_exited")
		}
		return flashsale.OutboxStats{}, nil
	}
	cfg := testRuntimeConfig(flashSaleModeKafka, true)
	cfg.ShutdownTimeout = 500 * time.Millisecond

	rt, err := startFlashSaleRuntime(context.Background(), newProbeDeps(cfg, probe))
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	waitFor(t, time.Second, "Outbox 发布循环运行", func() bool {
		select {
		case <-probe.outboxEntered:
			return true
		default:
			return false
		}
	})
	waitFor(t, time.Second, "Consumer.Run 运行", func() bool {
		select {
		case <-probe.consumerStarted:
			return true
		default:
			return false
		}
	})

	started := time.Now()
	err = rt.Shutdown()
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("Publisher 在预算内退出时 Shutdown 不应报错: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Shutdown 耗时 %s，预算未生效", elapsed)
	}

	for _, name := range []string{
		"publisher_exited", "consumer_loop_exited", "consumer_client_closed",
		"producer_closed", "scanner_stopped:order_cancel",
	} {
		waitProbeEvent(t, probe, name, 2*time.Second)
	}

	events := probe.eventLog()
	order := []string{
		"publisher_exited",
		"consumer_loop_exited",
		"producer_closed",
		"scanner_stopped:order_cancel",
	}
	last := -1
	for _, name := range order {
		idx := probeEventIndex(events, name)
		if idx <= last {
			t.Fatalf("关闭顺序被改变：%q 未发生在上一事件之后（events=%v）", name, events)
		}
		last = idx
	}
}

// TestFlashSaleRuntimeShutdownContinuesAfterComponentTimeout 覆盖「组件超时」：
// Publisher 完全不响应取消并占满预算，Shutdown 必须返回明确超时错误且不阻塞，
// 同时 Consumer / Producer / Scanner 仍然得到关闭机会（不能被整体跳过）。
func TestFlashSaleRuntimeShutdownContinuesAfterComponentTimeout(t *testing.T) {
	probe := newRuntimeProbe()
	block := make(chan struct{})
	var releaseOnce sync.Once
	probe.outboxBehavior = func(context.Context, int, int) (flashsale.OutboxStats, error) {
		<-block
		probe.record("publisher_exited")
		return flashsale.OutboxStats{}, nil
	}
	cfg := testRuntimeConfig(flashSaleModeKafka, true)
	cfg.ShutdownTimeout = 120 * time.Millisecond

	rt, err := startFlashSaleRuntime(context.Background(), newProbeDeps(cfg, probe))
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	waitFor(t, time.Second, "Outbox 发布循环运行", func() bool {
		select {
		case <-probe.outboxEntered:
			return true
		default:
			return false
		}
	})
	waitFor(t, time.Second, "Consumer.Run 运行", func() bool {
		select {
		case <-probe.consumerStarted:
			return true
		default:
			return false
		}
	})

	started := time.Now()
	err = rt.Shutdown()
	elapsed := time.Since(started)

	if err == nil || !strings.Contains(err.Error(), "Outbox Publisher") ||
		!strings.Contains(err.Error(), "关闭预算") {
		t.Fatalf("Publisher 超时必须返回明确错误，实际: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Shutdown 耗时 %s，预算未生效", elapsed)
	}

	// 关键断言：预算耗尽后，后续组件仍然执行（不是完全跳过）。
	probe.mu.Lock()
	producerClosed := probe.producerCloseCalls
	probe.mu.Unlock()
	if producerClosed != 1 {
		t.Fatalf("Publisher 超时后仍必须关闭 Producer，实际 %d 次", producerClosed)
	}
	for _, name := range []string{
		"consumer_loop_exited", "consumer_client_closed",
		"producer_closed", "scanner_stopped:order_cancel", "scanner_stopped:reconcile",
	} {
		waitProbeEvent(t, probe, name, 2*time.Second)
	}

	// 清理卡住的 Publisher goroutine，避免影响后续用例。
	releaseOnce.Do(func() { close(block) })
	waitProbeEvent(t, probe, "publisher_exited", 2*time.Second)
}
