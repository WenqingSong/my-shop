package cmd

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runtimeContextKey 用于验证 WithoutCancel 只剥离取消传播、不丢上下文值。
type runtimeContextKey struct{}

// TestRuntimeContextDetachesSignalCancellation 是 B4.1 关键修复的确定性回归：
// 父 ctx（模拟 signal.NotifyContext）取消后，后台运行期 ctx 必须仍然可用，
// 不能让 SIGTERM 绕过 runtime.Shutdown 的顺序编排直接取消 Publisher/Consumer/Scanner。
func TestRuntimeContextDetachesSignalCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), runtimeContextKey{}, "trace-value"))
	runtimeCtx := runtimeContext(parent)

	cancel()

	select {
	case <-runtimeCtx.Done():
		t.Fatal("runtimeContext 继承了信号 ctx 的取消，SIGTERM 会绕过 Runtime Shutdown 顺序编排")
	case <-time.After(50 * time.Millisecond):
	}
	if got := runtimeCtx.Value(runtimeContextKey{}); got != "trace-value" {
		t.Fatalf("runtimeContext 丢失上下文值: %v", got)
	}
}

// TestForceCloseWindow 断言强制关闭窗口始终位于 drain 预算之内。
func TestForceCloseWindow(t *testing.T) {
	cases := map[time.Duration]time.Duration{
		10 * time.Second:       time.Second,
		2 * time.Second:        time.Second,
		100 * time.Millisecond: 50 * time.Millisecond,
		0:                      0,
	}
	for budget, want := range cases {
		if got := forceCloseWindowFor(budget); got != want {
			t.Fatalf("forceCloseWindowFor(%s) = %s，期望 %s", budget, got, want)
		}
	}
}

// TestStopHTTPServerGraceful 断言正常 drain：shutdown 返回后直接确认停止，
// 不触发强制关闭，耗时远小于预算。
func TestStopHTTPServerGraceful(t *testing.T) {
	var forced atomic.Int32

	result, err := stopHTTPServer(
		context.Background(),
		"terminated",
		func() error { return nil },
		func() int { return 0 },
		func() int { forced.Add(1); return 0 },
		200*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("正常关闭不应返回错误: %v", err)
	}
	if result.TimedOut {
		t.Fatal("正常关闭不应标记超时")
	}
	if forced.Load() != 0 {
		t.Fatalf("正常关闭不应强制关闭连接，实际调用 %d 次", forced.Load())
	}
	if result.Elapsed > 100*time.Millisecond {
		t.Fatalf("正常关闭耗时 %s，未立即返回", result.Elapsed)
	}
}

// TestStopHTTPServerForcedAfterBudget 模拟 handler 不响应取消：
// Shutdown 在优雅窗口内不返回，必须触发强制关闭并在预算内确认停止。
func TestStopHTTPServerForcedAfterBudget(t *testing.T) {
	forceCh := make(chan struct{})
	var releaseOnce sync.Once
	var forced atomic.Int32
	var closed atomic.Bool

	shutdown := func() error {
		<-forceCh
		return nil
	}
	active := func() int {
		if closed.Load() {
			return 0
		}
		return 1
	}
	forceClose := func() int {
		forced.Add(1)
		closed.Store(true)
		releaseOnce.Do(func() { close(forceCh) })
		return 1
	}

	started := time.Now()
	result, err := stopHTTPServer(context.Background(), "terminated", shutdown, active, forceClose, 200*time.Millisecond)
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("强制关闭后应在预算内完成: %v", err)
	}
	if !result.TimedOut {
		t.Fatal("Shutdown 未在优雅窗口内返回时必须标记超时")
	}
	if forced.Load() != 1 {
		t.Fatalf("必须执行一次强制关闭，实际 %d 次", forced.Load())
	}
	if result.ForcedConns != 1 {
		t.Fatalf("被强制关闭的连接数 = %d，期望 1", result.ForcedConns)
	}
	// 预算 200ms → 优雅窗口 100ms，强制关闭必须发生在优雅窗口之后、预算之内。
	if result.ForceCloseAt < 80*time.Millisecond || result.ForceCloseAt > 200*time.Millisecond {
		t.Fatalf("强制关闭时刻 %s 未落在优雅窗口之后、预算之内", result.ForceCloseAt)
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("强制关闭后总耗时 %s，未受预算约束", elapsed)
	}
}

// TestStopHTTPServerForceCloseWhenShutdownLeavesActiveConns 断言 shutdown 提前返回
// 但仍有活跃连接时，同样按超时处理：不能把仍在服务请求的 Server 记为已停止。
func TestStopHTTPServerForceCloseWhenShutdownLeavesActiveConns(t *testing.T) {
	var closed atomic.Bool
	var forced atomic.Int32

	result, err := stopHTTPServer(
		context.Background(),
		"terminated",
		func() error { return nil },
		func() int {
			if closed.Load() {
				return 0
			}
			return 1
		},
		func() int {
			forced.Add(1)
			closed.Store(true)
			return 1
		},
		200*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("强制关闭后应成功: %v", err)
	}
	if !result.TimedOut {
		t.Fatal("Shutdown 返回时仍有活跃连接，必须标记超时")
	}
	if forced.Load() != 1 {
		t.Fatalf("必须执行一次强制关闭，实际 %d 次", forced.Load())
	}
}

// TestTrackedListenerForceClose 断言受控 listener 能追踪连接并在强制关闭后清零，
// 关闭后不再接受新连接（等价 http.Server.Close 的 listener 语义）。
func TestTrackedListenerForceClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听: %v", err)
	}
	tracked := newTrackedListener(ln)
	addr := ln.Addr().String()

	accepted := make(chan struct{}, 4)
	go func() {
		for {
			conn, err := tracked.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			_ = conn
		}
	}()

	clients := make([]net.Conn, 0, 3)
	defer func() {
		for _, c := range clients {
			_ = c.Close()
		}
	}()
	for i := 0; i < 3; i++ {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			t.Fatalf("连接 %d: %v", i, err)
		}
		clients = append(clients, conn)
		select {
		case <-accepted:
		case <-time.After(time.Second):
			t.Fatalf("连接 %d 未被 Accept", i)
		}
	}

	deadline := time.Now().Add(time.Second)
	for tracked.Active() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := tracked.Active(); got != 3 {
		t.Fatalf("活跃连接数 = %d，期望 3", got)
	}

	if forced := tracked.ForceClose(); forced != 3 {
		t.Fatalf("强制关闭连接数 = %d，期望 3", forced)
	}
	if got := tracked.Active(); got != 0 {
		t.Fatalf("强制关闭后活跃连接数 = %d，期望 0", got)
	}
	if conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("强制关闭后仍可建立新连接")
	}
}

// TestStopHTTPServerDoesNotReportStoppedWhenForceCloseIncomplete 断言强制关闭后
// Shutdown 仍不返回时，不能输出 stopped（返回错误且不产生成功结果）。
func TestStopHTTPServerDoesNotReportStoppedWhenForceCloseIncomplete(t *testing.T) {
	block := make(chan struct{})
	shutdownDone := make(chan struct{})
	shutdown := func() error {
		defer close(shutdownDone)
		<-block
		return nil
	}
	defer func() {
		close(block)
		<-shutdownDone
	}()

	_, err := stopHTTPServer(
		context.Background(),
		"terminated",
		shutdown,
		func() int { return 1 },
		func() int { return 1 },
		120*time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "未在 drain 预算") {
		t.Fatalf("强制关闭后仍未停止时必须返回错误，实际: %v", err)
	}
}
