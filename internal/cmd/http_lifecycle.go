package cmd

// HTTP Server 终止路径（T2-03.1）：
//
//	http_server_started
//	  → [SIGTERM] http_server_drain_begin
//	  → 正常：http_server_stopped（timed_out=false）
//	  → 超时：http_server_drain_timeout → http_server_force_closed → http_server_stopped（timed_out=true）
//	  → flashsale_runtime_shutdown_begin（由 serve 的 defer 在上面的返回之后触发）
//
// 为什么需要自建 listener：
//   - ghttp.Server 只暴露 Shutdown()（无 ctx），底层 graceful.Server 虽有 Close()
//     （强制关闭 listener 与连接）但不可达；
//   - 通过 SetListener 接管监听后，Drain 超时可以在进程内关闭 listener 与全部已接受连接，
//     等价于 http.Server.Close 的强制关闭语义，不依赖 SIGKILL 或进程退出。
//
// 为什么要把 GoFrame 内部 graceful 超时对齐到「预算 - 强制关闭窗口」：
//   - net/http 的 Shutdown 只在 handler 返回（连接 StateClosed）后才减少 activeConn，
//     仅关闭 socket 不会让它提前返回；
//   - 因此内部超时必须早于（或等于）外层预算到期，Shutdown 调用才会在预算内返回，
//     不会遗留仍在等待的关闭协程。

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"

	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
)

// defaultForceCloseWindow 是 HTTP drain 预算内预留给强制关闭与停止确认的窗口。
// 预算的前 (budget - window) 用于优雅 drain；剩余部分用于强制关闭并确认 Server 已停止，
// 因此总耗时不会因为等待 Shutdown 而突破预算。
const defaultForceCloseWindow = time.Second

// forceCloseWindowFor 返回指定预算下的强制关闭窗口。
// 预算过小时取一半，保证优雅 drain 与强制关闭都留有执行机会。
func forceCloseWindowFor(budget time.Duration) time.Duration {
	if budget <= 0 {
		return 0
	}
	window := defaultForceCloseWindow
	if window >= budget {
		window = budget / 2
	}
	return window
}

// drainResult 是一次 HTTP drain 的观测结果（供测试与日志使用）。
type drainResult struct {
	Elapsed      time.Duration
	TimedOut     bool
	ForcedConns  int
	ForceCloseAt time.Duration
}

// serveHTTP 启动 HTTP 服务并在预算内完成摘流、drain 与（必要时）强制关闭。
func serveHTTP(ctx context.Context, s *ghttp.Server, addr string, drainTimeout time.Duration) error {
	// 先注册信号再启动监听，缩小「已监听但信号无人处理」的窗口。
	// 与 GoFrame 一致覆盖 SIGINT/SIGQUIT/SIGTERM（SIGKILL 不可捕获）。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if addr == "" {
		return fmt.Errorf("HTTP 服务监听地址为空，无法建立受控 listener")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", addr, err)
	}
	tracked := newTrackedListener(ln)

	// GoFrame 内部 graceful 关闭超时（整秒）对齐到「预算 - 强制关闭窗口」，
	// 使 Shutdown 调用一定在终止预算内返回（见文件头说明）。
	forceWindow := forceCloseWindowFor(drainTimeout)
	gracefulWindow := drainTimeout - forceWindow
	gracefulSeconds := int(gracefulWindow / time.Second)
	if gracefulSeconds < 1 {
		gracefulSeconds = 1
	}
	s.SetGracefulTimeout(gracefulSeconds)

	// 接管 listener：SetListener 会把 config.Address 归一化为 :port，
	// graceful.Server 按端口匹配后直接复用该 listener，不再二次 Listen。
	if err := s.SetListener(tracked); err != nil {
		_ = ln.Close()
		return fmt.Errorf("接管 HTTP listener: %w", err)
	}
	if err := s.Start(); err != nil {
		_ = ln.Close()
		return err
	}
	logx.Info(ctx, "http_server_started", "HTTP Server 已启动，等待终止信号", logx.Fields{
		"address":            tracked.Addr().String(),
		"drain_timeout":      drainTimeout.String(),
		"graceful_window":    gracefulWindow.String(),
		"force_close_window": forceWindow.String(),
	})

	sig := <-sigCh
	logx.Info(ctx, "http_server_drain_begin", "收到终止信号，停止接收新连接并开始 drain 在途请求", logx.Fields{
		"signal":        sig.String(),
		"drain_timeout": drainTimeout.String(),
	})

	_, err = stopHTTPServer(ctx, sig.String(), s.Shutdown, tracked.Active, tracked.ForceClose, drainTimeout)
	return err
}

// stopHTTPServer 在预算内等待 HTTP Server 停止：
//  1. 等待 shutdown 返回，最多 budget - forceWindow；
//  2. 若超时，或 shutdown 返回时仍有活跃连接，执行强制关闭（listener + 全部连接）；
//  3. 在预算剩余时间内确认 shutdown 返回与连接清零；
//  4. 只有确认停止后才输出 http_server_stopped，并返回成功。
//
// 任一步无法在预算内完成都会返回错误，且不会输出 http_server_stopped。
func stopHTTPServer(
	ctx context.Context,
	signal string,
	shutdown func() error,
	active func() int,
	forceClose func() int,
	budget time.Duration,
) (drainResult, error) {
	started := time.Now()
	forceWindow := forceCloseWindowFor(budget)
	gracefulWindow := budget - forceWindow
	if gracefulWindow < 0 {
		gracefulWindow = 0
	}

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		if err := shutdown(); err != nil {
			logx.Warning(ctx, "http_server_shutdown_error", "HTTP Server Shutdown 返回错误", logx.Fields{
				"error": err.Error(),
			})
		}
	}()

	timedOut := false
	if gracefulWindow > 0 {
		timer := time.NewTimer(gracefulWindow)
		select {
		case <-finished:
		case <-timer.C:
			timedOut = true
		}
		timer.Stop()
	} else {
		// 预算被强制关闭窗口占满：不允许无界等待，未立即返回即按超时处理。
		select {
		case <-finished:
		default:
			timedOut = true
		}
	}
	activeBefore := active()
	if !timedOut && activeBefore > 0 {
		// Shutdown 已返回但仍有活跃连接：GoFrame 内部预算先于外层预算到期，
		// 或仍有请求未结束，同样按超时处理。
		timedOut = true
	}

	result := drainResult{ForcedConns: 0}
	if timedOut {
		logx.Warning(ctx, "http_server_drain_timeout", "HTTP drain 未在预算内完成，开始强制关闭 listener 与连接", logx.Fields{
			"elapsed_ms":         time.Since(started).Milliseconds(),
			"drain_budget":       budget.String(),
			"graceful_budget":    gracefulWindow.String(),
			"active_connections": activeBefore,
		})
		result.ForcedConns = forceClose()
		result.ForceCloseAt = time.Since(started)
		logx.Warning(ctx, "http_server_force_closed", "HTTP Server 已强制关闭 listener 与全部连接", logx.Fields{
			"elapsed_ms":         time.Since(started).Milliseconds(),
			"forced_connections": result.ForcedConns,
			"active_connections": active(),
		})

		remaining := budget - time.Since(started)
		if remaining <= 0 {
			select {
			case <-finished:
			default:
				logx.Error(ctx, "http_server_force_close_incomplete", "强制关闭后 HTTP Server 仍未停止，不能标记为已停止", logx.Fields{
					"elapsed_ms":   time.Since(started).Milliseconds(),
					"drain_budget": budget.String(),
				})
				return result, fmt.Errorf("强制关闭后 HTTP Server 未在 drain 预算 %s 内停止", budget)
			}
		} else {
			timer := time.NewTimer(remaining)
			select {
			case <-finished:
			case <-timer.C:
				logx.Error(ctx, "http_server_force_close_incomplete", "强制关闭后 HTTP Server 仍未停止，不能标记为已停止", logx.Fields{
					"elapsed_ms":   time.Since(started).Milliseconds(),
					"drain_budget": budget.String(),
				})
				return result, fmt.Errorf("强制关闭后 HTTP Server 未在 drain 预算 %s 内停止", budget)
			}
			timer.Stop()
		}
	}

	// 防御性检查：只有 listener 与全部连接都关闭、Shutdown 已返回，才允许输出 stopped。
	if remaining := active(); remaining > 0 {
		logx.Error(ctx, "http_server_stop_incomplete", "HTTP Server 仍有活跃连接，不能标记为已停止", logx.Fields{
			"active_connections": remaining,
		})
		return result, fmt.Errorf("HTTP Server 仍有 %d 个活跃连接，不能标记为已停止", remaining)
	}

	result.Elapsed = time.Since(started)
	result.TimedOut = timedOut
	message := "HTTP Server 已退出（优雅 drain 结束），开始关闭秒杀后台组件"
	if timedOut {
		message = "HTTP Server 已停止（drain 超时后强制关闭完成），开始关闭秒杀后台组件"
	}
	logx.Info(ctx, "http_server_stopped", message, logx.Fields{
		"signal":             signal,
		"elapsed_ms":         result.Elapsed.Milliseconds(),
		"timed_out":          timedOut,
		"drain_budget":       budget.String(),
		"forced_connections": result.ForcedConns,
	})
	return result, nil
}

// trackedListener 记录已接受的连接，使 Drain 超时可以像 http.Server.Close 一样
// 关闭 listener 与全部连接（GoFrame 未暴露该能力）。
type trackedListener struct {
	net.Listener

	mu      sync.Mutex
	conns   map[*trackedConn]struct{}
	closing bool
}

func newTrackedListener(ln net.Listener) *trackedListener {
	return &trackedListener{Listener: ln, conns: make(map[*trackedConn]struct{})}
}

// Accept 接受连接并登记；与 ForceClose 并发时，已关闭的 listener 不再放行新连接。
func (l *trackedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tracked := &trackedConn{Conn: conn}

	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		_ = conn.Close()
		return nil, net.ErrClosed
	}
	tracked.onClose = func() { l.remove(tracked) }
	l.conns[tracked] = struct{}{}
	l.mu.Unlock()
	return tracked, nil
}

func (l *trackedListener) remove(conn *trackedConn) {
	l.mu.Lock()
	delete(l.conns, conn)
	l.mu.Unlock()
}

// Active 返回当前登记在案的连接数。
func (l *trackedListener) Active() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.conns)
}

// ForceClose 立即关闭 listener 与全部已接受连接，返回被强制关闭的连接数。
// 语义对齐 http.Server.Close：不等待 handler 返回（Go 无法终止 goroutine），
// 但不再接受新连接、不再保留任何 socket。
func (l *trackedListener) ForceClose() int {
	l.mu.Lock()
	l.closing = true
	conns := make([]*trackedConn, 0, len(l.conns))
	for conn := range l.conns {
		conns = append(conns, conn)
	}
	l.mu.Unlock()

	_ = l.Listener.Close()
	for _, conn := range conns {
		_ = conn.Close()
	}
	return len(conns)
}

// trackedConn 在关闭时把自己从 listener 的登记表中移除。
type trackedConn struct {
	net.Conn

	once    sync.Once
	onClose func()
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		if c.onClose != nil {
			c.onClose()
		}
	})
	return err
}
