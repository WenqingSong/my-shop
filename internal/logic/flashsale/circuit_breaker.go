// 秒杀 V5 容量保护——Redis 闸门熔断器（进程内状态机）。
// 状态机 Closed→Open→Half-Open：连续失败达到阈值进入 Open（快速失败 1005/503，不预扣、不建单、不排队），
// 保持 V3 fail-closed 语义、仅缩短失败路径，不降级到同步 MySQL 路径（避免把流量打回 MySQL）；
// Open 持续 open_timeout 后进入 Half-Open，放行少量探测请求（真实请求）验证 Redis 恢复，
// 探测成功自动 Close、失败回到 Open。熔断状态为进程内（多实例各自独立，不共享）。
package flashsale

import (
	"context"
	"sync"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

// breakerState 熔断器状态。
type breakerState int

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

// circuitBreaker 是进程内 Redis 闸门熔断器，非并发安全（须在锁内访问其字段）。
type circuitBreaker struct {
	mu               sync.Mutex
	state            breakerState
	consecutiveFails int
	openedAt         time.Time
	halfOpenInFlight int // Half-Open 阶段已放行、尚未得出成败结论的探测数
}

// newCircuitBreaker 创建 Closed 初始态的熔断器。
func newCircuitBreaker() *circuitBreaker {
	return &circuitBreaker{state: breakerClosed}
}

// circuitBreakerEnabled 读取熔断开关（flash_sale.circuit_breaker.enabled，默认关闭）。
func circuitBreakerEnabled(ctx context.Context) bool {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.circuit_breaker.enabled", false)
	if err != nil || v == nil {
		return false
	}
	return v.Bool()
}

// circuitBreakerFailureThreshold 连续失败阈值（<=0 回退默认 5）。
func circuitBreakerFailureThreshold(ctx context.Context) int {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.circuit_breaker.failure_threshold", 5)
	if err != nil || v == nil {
		return 5
	}
	if n := v.Int(); n > 0 {
		return n
	}
	return 5
}

// circuitBreakerOpenTimeout 熔断打开后的持续时间（秒，<=0 回退默认 10）。
func circuitBreakerOpenTimeout(ctx context.Context) time.Duration {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.circuit_breaker.open_timeout_seconds", int64(10))
	if err != nil || v == nil {
		return 10 * time.Second
	}
	if n := v.Int64(); n > 0 {
		return time.Duration(n) * time.Second
	}
	return 10 * time.Second
}

// circuitBreakerHalfOpenMaxProbes Half-Open 阶段允许放行的最大探测数（<=0 回退默认 1）。
func circuitBreakerHalfOpenMaxProbes(ctx context.Context) int {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.circuit_breaker.half_open_max_probes", 1)
	if err != nil || v == nil {
		return 1
	}
	if n := v.Int(); n > 0 {
		return n
	}
	return 1
}

// allow 判断是否允许请求进入 Redis 闸门。熔断关闭时恒放行；Open 且未到超时时间快速拒绝；
// Open 到期转 Half-Open 并放行有限探测；Half-Open 仅放行未达上限的探测数。
func (cb *circuitBreaker) allow(ctx context.Context) bool {
	if !circuitBreakerEnabled(ctx) {
		return true
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case breakerClosed:
		return true
	case breakerOpen:
		if time.Since(cb.openedAt) < circuitBreakerOpenTimeout(ctx) {
			return false
		}
		cb.state = breakerHalfOpen
		cb.halfOpenInFlight = 0
	}
	// Half-Open：放行有限探测。
	if cb.halfOpenInFlight < circuitBreakerHalfOpenMaxProbes(ctx) {
		cb.halfOpenInFlight++
		return true
	}
	return false
}

// recordFailure 记录一次闸门失败（Redis 不可用/Lua 执行失败）。Closed 累计连续失败达阈值转 Open；
// Half-Open 探测失败立即回到 Open。
func (cb *circuitBreaker) recordFailure(ctx context.Context) {
	if !circuitBreakerEnabled(ctx) {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case breakerClosed:
		cb.consecutiveFails++
		if cb.consecutiveFails >= circuitBreakerFailureThreshold(ctx) {
			cb.open()
		}
	case breakerHalfOpen:
		cb.open()
	case breakerOpen:
		// 已 Open，忽略。
	}
}

// recordSuccess 记录一次闸门成功（Redis 正常执行）。Closed 重置连续失败计数；
// Half-Open 探测成功自动关闭熔断，恢复正常处理。
func (cb *circuitBreaker) recordSuccess(ctx context.Context) {
	if !circuitBreakerEnabled(ctx) {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case breakerClosed:
		cb.consecutiveFails = 0
	case breakerHalfOpen:
		cb.state = breakerClosed
		cb.consecutiveFails = 0
		cb.halfOpenInFlight = 0
	case breakerOpen:
		// 已 Open，忽略。
	}
}

// open 将熔断器切到 Open 并重置探测计数（调用方须已持锁）。
func (cb *circuitBreaker) open() {
	cb.state = breakerOpen
	cb.openedAt = time.Now()
	cb.consecutiveFails = 0
	cb.halfOpenInFlight = 0
}
