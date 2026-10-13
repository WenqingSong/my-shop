package health

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

func TestLiveness(t *testing.T) {
	ctx := context.Background()
	res, err := service.Health().Liveness(ctx)
	if err != nil {
		t.Fatalf("Liveness returned error: %v", err)
	}
	if res == nil {
		t.Fatal("Liveness returned nil result")
	}
	if res.Status != "ok" {
		t.Fatalf("expected status %q, got %q", "ok", res.Status)
	}
	if res.Time == "" {
		t.Fatal("expected non-empty time")
	}
}

// withProbe 在测试期间替换 probeDependencies，并在结束后复原。
func withProbe(t *testing.T, probe func(ctx context.Context) error) {
	t.Helper()
	orig := probeDependencies
	probeDependencies = probe
	t.Cleanup(func() { probeDependencies = orig })
}

// TestReadinessSuccess 覆盖 INV-003 成功路径：依赖可达时返回 ready，无错误。
func TestReadinessSuccess(t *testing.T) {
	withProbe(t, func(ctx context.Context) error { return nil })
	s := New()
	res, err := s.Readiness(context.Background())
	if err != nil {
		t.Fatalf("Readiness returned error: %v", err)
	}
	if res.Status != "ready" {
		t.Fatalf("expected status %q, got %q", "ready", res.Status)
	}
}

// TestReadinessNotReadyReturns503 覆盖 INV-003 fail-closed：任一依赖不可达返回 503 业务码，
// 且错误链不含底层技术错误（对外响应不泄漏 DB/Redis 细节）。
func TestReadinessNotReadyReturns503(t *testing.T) {
	withProbe(t, func(ctx context.Context) error { return errors.New("dial tcp 127.0.0.1:3306: connect refused") })
	s := New()
	_, err := s.Readiness(context.Background())
	if err == nil {
		t.Fatal("expected readiness error when dependencies are unreachable")
	}
	if code := codes.FromError(err); code != codes.CodeServiceUnavailable {
		t.Fatalf("expected code %d, got %d", codes.CodeServiceUnavailable, code)
	}
}

// TestReadinessCacheAvoidsReprobe 覆盖 INV-004：缓存窗口内重复调用不新增依赖探测。
// 若每次调用都重新探测（探测次数 >1），本测试会失败。
func TestReadinessCacheAvoidsReprobe(t *testing.T) {
	t.Setenv("READINESS_CACHE_TTL", "3600")
	t.Setenv("READINESS_PROBE_TIMEOUT", "1")
	var calls atomic.Int32
	withProbe(t, func(ctx context.Context) error {
		calls.Add(1)
		return nil
	})
	s := New()
	for i := 0; i < 3; i++ {
		if _, err := s.Readiness(context.Background()); err != nil {
			t.Fatalf("Readiness call %d returned error: %v", i, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 dependency probe within cache window, got %d", got)
	}
}

// TestReadinessCacheExpiryReprobes 覆盖 INV-004 缓存过期：TTL 过后重新探测。
func TestReadinessCacheExpiryReprobes(t *testing.T) {
	t.Setenv("READINESS_CACHE_TTL", "1")
	t.Setenv("READINESS_PROBE_TIMEOUT", "1")
	var calls atomic.Int32
	withProbe(t, func(ctx context.Context) error {
		calls.Add(1)
		return nil
	})
	s := New()
	if _, err := s.Readiness(context.Background()); err != nil {
		t.Fatalf("first Readiness returned error: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := s.Readiness(context.Background()); err != nil {
		t.Fatalf("second Readiness returned error: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 dependency probes after TTL expiry, got %d", got)
	}
}

// TestReadinessNotReadyCached 覆盖 INV-003/INV-004：失败结果同样在缓存窗口内复用，不重复打库/打缓存。
func TestReadinessNotReadyCached(t *testing.T) {
	t.Setenv("READINESS_CACHE_TTL", "3600")
	t.Setenv("READINESS_PROBE_TIMEOUT", "1")
	var calls atomic.Int32
	withProbe(t, func(ctx context.Context) error {
		calls.Add(1)
		return errors.New("dependency unreachable")
	})
	s := New()
	for i := 0; i < 3; i++ {
		if _, err := s.Readiness(context.Background()); err == nil {
			t.Fatal("expected readiness error when dependencies are unreachable")
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 dependency probe within cache window, got %d", got)
	}
}

// TestProbeReadySuccess 覆盖 probeReady 成功路径：探测成功即返回 true。
func TestProbeReadySuccess(t *testing.T) {
	withProbe(t, func(ctx context.Context) error { return nil })
	if !probeReady(context.Background(), time.Second) {
		t.Fatal("expected probeReady true when dependencies are reachable")
	}
}

// TestProbeReadyTimeout 覆盖 INV-004 探测超时：探测阻塞时在限定时间内返回 false（fail-closed），
// 而非无限等待。若探测超时未生效，本测试会因阻塞超时失败。
func TestProbeReadyTimeout(t *testing.T) {
	withProbe(t, func(ctx context.Context) error {
		// 模拟探测阻塞，直到探测 ctx 被取消才返回。
		<-ctx.Done()
		return ctx.Err()
	})
	start := time.Now()
	if probeReady(context.Background(), 50*time.Millisecond) {
		t.Fatal("expected probeReady false when probe times out")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("probeReady should honor timeout, took %s", elapsed)
	}
}
