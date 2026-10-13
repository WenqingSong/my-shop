package flashsale

// 熔断器状态机纯内存测试（无 MySQL/Redis 依赖），是 race 门禁的目标：
// 覆盖 Closed→Open→Half-Open→Closed 状态迁移与并发访问同一进程内共享内存状态的数据竞争检测。
// 与 capacity_test.go 的 TestCircuitBreakerOpensAndRecovers（DB 集成、真实 Redis）互补：
// 本文件两个测试只依赖环境变量 + 纯内存，可在无 DB 的隔离环境中独立运行。

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestCircuitBreakerStateMachineTransitions 确定性验证状态机迁移（关键不变量）：
// Closed 放行 → 连续失败达阈值 Open 快速失败 → Open 超时转 Half-Open 放行有限探测 → 探测成功恢复 Closed。
// 纯内存、无时间抖动：Open→Half-Open 通过直接回拨 openedAt 模拟超时到期（白盒、同包可访问）。
// 若熔断开关/阈值/状态迁移逻辑被改坏，本测试会在对应断言处失败。
func TestCircuitBreakerStateMachineTransitions(t *testing.T) {
	t.Setenv("FLASH_SALE_CIRCUIT_BREAKER_ENABLED", "true")
	t.Setenv("FLASH_SALE_CIRCUIT_BREAKER_FAILURE_THRESHOLD", "2")
	t.Setenv("FLASH_SALE_CIRCUIT_BREAKER_OPEN_TIMEOUT_SECONDS", "10")
	t.Setenv("FLASH_SALE_CIRCUIT_BREAKER_HALF_OPEN_MAX_PROBES", "1")
	ctx := context.Background()
	cb := newCircuitBreaker()

	// Closed：未达阈值前放行。
	if !cb.allow(ctx) {
		t.Fatal("Closed 状态应放行")
	}
	cb.recordFailure(ctx) // consecutiveFails=1，未达阈值 2
	if !cb.allow(ctx) {
		t.Fatal("未达失败阈值仍应放行")
	}

	// 第 2 次失败 → Open：快速失败。
	cb.recordFailure(ctx)
	if cb.allow(ctx) {
		t.Fatal("Open 状态应快速失败")
	}
	// Open 期间失败/成功均不改变状态（忽略）。
	cb.recordFailure(ctx)
	cb.recordSuccess(ctx)
	if cb.allow(ctx) {
		t.Fatal("Open 状态应快速失败")
	}

	// 回拨 openedAt 模拟超时到期 → Half-Open：放行 1 个探测。
	cb.mu.Lock()
	cb.openedAt = time.Now().Add(-11 * time.Second)
	cb.mu.Unlock()
	if !cb.allow(ctx) {
		t.Fatal("Open 超时后应进入 Half-Open 并放行探测")
	}
	// Half-Open 探测上限=1，第 2 个探测应拒绝。
	if cb.allow(ctx) {
		t.Fatal("Half-Open 探测数达上限应拒绝")
	}

	// 探测成功 → 恢复 Closed，重新放行。
	cb.recordSuccess(ctx)
	if !cb.allow(ctx) {
		t.Fatal("探测成功后应恢复 Closed 并放行")
	}
}

// TestCircuitBreakerStateMachineRace 是 race 门禁的核心：多 goroutine 并发读写熔断器共享内存状态。
// 阈值设大使熔断器长期停留在 Closed，持续对 consecutiveFails 做并发读（阈值比较）与写（+1 / 清零），
// 从而在有锁时通过、移除 cb.mu 锁时被 go test -race 检出数据竞争并非零退出。
func TestCircuitBreakerStateMachineRace(t *testing.T) {
	t.Setenv("FLASH_SALE_CIRCUIT_BREAKER_ENABLED", "true")
	t.Setenv("FLASH_SALE_CIRCUIT_BREAKER_FAILURE_THRESHOLD", "1000000")
	t.Setenv("FLASH_SALE_CIRCUIT_BREAKER_OPEN_TIMEOUT_SECONDS", "1")
	t.Setenv("FLASH_SALE_CIRCUIT_BREAKER_HALF_OPEN_MAX_PROBES", "1")
	ctx := context.Background()

	// 确认熔断开关经环境变量生效（否则状态机不被驱动，后续并发访问失去意义）。
	if !circuitBreakerEnabled(ctx) {
		t.Fatal("FLASH_SALE_CIRCUIT_BREAKER_ENABLED=true 未生效（环境变量映射失效）")
	}

	cb := newCircuitBreaker()
	const (
		workers = 16
		rounds  = 200
	)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				switch (seed + i) % 4 {
				case 0:
					cb.allow(ctx)
				case 1, 2:
					cb.recordFailure(ctx)
				default:
					cb.recordSuccess(ctx)
				}
			}
		}(w)
	}
	wg.Wait()

	// 最终一致性抽查：阈值巨大下应保持 Closed；consecutiveFails 恒非负。
	cb.mu.Lock()
	state := cb.state
	fails := cb.consecutiveFails
	cb.mu.Unlock()
	if state != breakerClosed {
		t.Fatalf("阈值 1000000 下熔断器应保持 Closed，实际 state=%d", state)
	}
	if fails < 0 {
		t.Fatalf("consecutiveFails 非法: %d", fails)
	}
}
