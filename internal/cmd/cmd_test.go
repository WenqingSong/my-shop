package cmd

import (
	"context"
	"errors"
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

// TestDrainHTTPRespectsBudget 断言 drain 在预算内返回超时标记，
// 不会像 GoFrame 默认信号路径那样无限等待在途请求。
func TestDrainHTTPRespectsBudget(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	elapsed, timedOut, err := drainHTTP(func() error {
		<-release
		return nil
	}, 50*time.Millisecond)

	if err != nil {
		t.Fatalf("超时不应返回关闭错误: %v", err)
	}
	if !timedOut {
		t.Fatal("drain 超时必须返回 timed_out=true")
	}
	if elapsed < 50*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("drain 耗时 %s 未贴近预算 50ms", elapsed)
	}
}

// TestDrainHTTPReturnsWhenServerStops 断言 drain 在 HTTP Server 正常停止后立即返回，
// 并把关闭错误透传给调用方（不改变后续关闭步骤）。
func TestDrainHTTPReturnsWhenServerStops(t *testing.T) {
	wantErr := errors.New("shutdown failed")
	elapsed, timedOut, err := drainHTTP(func() error {
		return wantErr
	}, time.Second)

	if timedOut {
		t.Fatal("HTTP Server 已停止时不应标记超时")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("关闭错误未透传: %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("正常关闭耗时 %s 超出预期", elapsed)
	}
}
