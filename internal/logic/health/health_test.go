package health

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	healthController "cnb.cool/go-cloud-devops/my-shop/internal/controller/health"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
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

// withProbe 在测试期间把 readiness 依赖集合替换为单个聚合探测，并在结束后复原。
// probe 的返回值即「本次 readiness 探测结果」；Redis 探测固定成功，便于聚焦聚合语义。
func withProbe(t *testing.T, probe func(ctx context.Context) error) {
	t.Helper()
	orig := readinessProbes
	readinessProbes = dependencyProbes{
		mysql: probe,
		redis: func(context.Context) error { return nil },
	}
	t.Cleanup(func() { readinessProbes = orig })
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

// ---------------------------------------------------------------------------
// T2-02-A：健康检查语义冻结的依赖调用计数与 HTTP 状态码取证
// ---------------------------------------------------------------------------

// probeStub 是可控依赖替身：分别注入 MySQL / Redis 故障，并统计各自的调用次数。
// 调用计数是「liveness 不触碰依赖」的直接证据，而不是「代码里没有 Ping」的静态推断。
type probeStub struct {
	mu       sync.Mutex
	mysqlErr error
	redisErr error

	muCalls    atomic.Int32
	redisCalls atomic.Int32
}

// install 把 readiness 依赖集合替换为可分别注入故障的替身，测试结束自动复原。
func (p *probeStub) install(t *testing.T) {
	t.Helper()
	orig := readinessProbes
	readinessProbes = dependencyProbes{
		mysql: func(context.Context) error {
			p.muCalls.Add(1)
			return p.mysqlFailure()
		},
		redis: func(context.Context) error {
			p.redisCalls.Add(1)
			return p.redisFailure()
		},
	}
	t.Cleanup(func() { readinessProbes = orig })
}

func (p *probeStub) mysqlFailure() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mysqlErr
}

func (p *probeStub) redisFailure() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.redisErr
}

// setFailures 切换依赖状态（读写在探测协程与测试协程之间发生，必须加锁）。
func (p *probeStub) setFailures(mysqlErr, redisErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mysqlErr, p.redisErr = mysqlErr, redisErr
}

func (p *probeStub) calls() (mysql, redis int32) {
	return p.muCalls.Load(), p.redisCalls.Load()
}

func (p *probeStub) resetCalls() {
	p.muCalls.Store(0)
	p.redisCalls.Store(0)
}

// TestLivenessNeverTouchesDependencies 覆盖 T2-02-A「/health 不访问 MySQL / Redis / Kafka」：
// 依赖替身全部处于故障态且计数，连续调用 Liveness 后断言 MySQL/Redis 调用次数为 0。
// Kafka 没有任何探测入口（本包不依赖 mq/worker），其「不可用仍 200」由进程级故障注入用例验证。
func TestLivenessNeverTouchesDependencies(t *testing.T) {
	stub := &probeStub{
		mysqlErr: errors.New("dial tcp 10.0.0.1:3306: connect: connection refused"),
		redisErr: errors.New("redis: PING failed"),
	}
	stub.install(t)

	s := New()
	for i := 0; i < 5; i++ {
		res, err := s.Liveness(context.Background())
		if err != nil {
			t.Fatalf("依赖全部不可用时 Liveness 返回错误: %v", err)
		}
		if res == nil || res.Status != "ok" {
			t.Fatalf("依赖全部不可用时 Liveness 状态异常: %+v", res)
		}
	}
	mysql, redis := stub.calls()
	if mysql != 0 || redis != 0 {
		t.Fatalf("Liveness 不得探测依赖：mysql 调用 %d 次、redis 调用 %d 次（期望均为 0）", mysql, redis)
	}
}

// TestReadinessDependencyMatrix 覆盖 T2-02-A 判定矩阵：
// readiness 只由 MySQL + Redis 决定，且 MySQL 失败时短路、不再探测 Redis。
func TestReadinessDependencyMatrix(t *testing.T) {
	cases := []struct {
		name      string
		mysqlErr  error
		redisErr  error
		wantReady bool
		wantRedis int32
	}{
		{"MySQL 正常/Redis 正常", nil, nil, true, 1},
		{"MySQL 不可用", errors.New("mysql down"), nil, false, 0},
		{"Redis 不可用", nil, errors.New("redis down"), false, 1},
		{"MySQL/Redis 均不可用", errors.New("mysql down"), errors.New("redis down"), false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("READINESS_CACHE_TTL", "1")
			t.Setenv("READINESS_PROBE_TIMEOUT", "1")
			stub := &probeStub{mysqlErr: c.mysqlErr, redisErr: c.redisErr}
			stub.install(t)

			s := New()
			_, err := s.Readiness(context.Background())
			ready := err == nil
			if ready != c.wantReady {
				t.Fatalf("ready=%v want %v (err=%v)", ready, c.wantReady, err)
			}
			if !ready {
				if code := codes.FromError(err); code != codes.CodeServiceUnavailable {
					t.Fatalf("未就绪必须返回 1005/503，实际 %d", code)
				}
			}
			mysql, redis := stub.calls()
			if mysql != 1 {
				t.Fatalf("readiness 必须探测 MySQL 恰好 1 次，实际 %d", mysql)
			}
			if redis != c.wantRedis {
				t.Fatalf("Redis 探测次数=%d want %d（MySQL 失败时必须短路）", redis, c.wantRedis)
			}
		})
	}
}

// TestReadinessRecoversWithinCacheTTL 覆盖 T2-02-A「依赖恢复后在缓存 TTL 内恢复 200」：
// 失败结果被缓存，恢复后必须等当前缓存窗口过去才重新探测并返回 200（窗口内仍为 503）。
func TestReadinessRecoversWithinCacheTTL(t *testing.T) {
	t.Setenv("READINESS_CACHE_TTL", "1")
	t.Setenv("READINESS_PROBE_TIMEOUT", "1")
	stub := &probeStub{mysqlErr: errors.New("mysql down")}
	stub.install(t)

	s := New()
	if _, err := s.Readiness(context.Background()); err == nil {
		t.Fatal("依赖不可用时应返回未就绪")
	}
	// 缓存窗口内：即使依赖已恢复，也不得重复探测（仍返回 503）。
	stub.setFailures(nil, nil)
	if _, err := s.Readiness(context.Background()); err == nil {
		t.Fatal("缓存窗口内不得重新探测：应继续返回未就绪")
	}

	recoveredAt := time.Now()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := s.Readiness(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("依赖恢复后未在 %s 内恢复就绪", 3*time.Second)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if elapsed := time.Since(recoveredAt); elapsed > 2*time.Second {
		t.Fatalf("恢复耗时 %s 超出缓存 TTL 预期窗口（默认 1s + 探测余量）", elapsed)
	}
}

// startHealthHTTPServer 启动与生产同款的健康检查 HTTP 路由：
// Response 中间件负责把业务错误码映射为 HTTP 状态（503），health v1 控制器绑定真实路径。
// 请求走真实 HTTP，而不是直接断言 service 返回值。
func startHealthHTTPServer(t *testing.T) string {
	t.Helper()
	s := g.Server(guid.S())
	s.SetAddr("127.0.0.1:0")
	s.Group("/", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.Response)
		group.Bind(healthController.NewV1())
	})
	s.SetDumpRouterMap(false)
	s.Start()
	t.Cleanup(func() { _ = s.Shutdown() })
	time.Sleep(100 * time.Millisecond)
	return fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
}

// getStatus 发起一次 GET 并返回 HTTP 状态码与响应体。
func getStatus(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := g.Client().Get(context.Background(), url)
	if err != nil {
		t.Fatalf("GET %s 失败: %v", url, err)
	}
	defer func() { _ = resp.Close() }()
	return resp.StatusCode, resp.ReadAllString()
}

// TestHealthHTTPStatusContract 覆盖 T2-02-A 的 HTTP 层矩阵：
//
//	场景                       /health  /ready
//	MySQL 正常、Redis 正常      200      200
//	MySQL 不可用                200      503
//	Redis 不可用                200      503
//	MySQL、Redis 均不可用       200      503
//
// 503 响应体不得出现数据库地址、Redis 信息或内部堆栈。
func TestHealthHTTPStatusContract(t *testing.T) {
	t.Setenv("READINESS_CACHE_TTL", "1")
	t.Setenv("READINESS_PROBE_TIMEOUT", "1")
	base := startHealthHTTPServer(t)

	cases := []struct {
		name     string
		mysqlErr error
		redisErr error
		want     int
	}{
		{"MySQL 正常/Redis 正常", nil, nil, 200},
		{"MySQL 不可用", errors.New("dial tcp 10.0.0.1:3306: connect: connection refused"), nil, 503},
		{"Redis 不可用", nil, errors.New("redis 10.0.0.2:6379: PING failed"), 503},
		{"MySQL/Redis 均不可用", errors.New("mysql down"), errors.New("redis down"), 503},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub := &probeStub{mysqlErr: c.mysqlErr, redisErr: c.redisErr}
			stub.install(t)
			// readiness 结果按进程内缓存 TTL 复用，切换依赖状态后需让上一个窗口过期。
			time.Sleep(1100 * time.Millisecond)

			if status, body := getStatus(t, base+"/health"); status != 200 {
				t.Fatalf("/health 必须恒为 200，实际 %d（body=%s）", status, body)
			}
			status, body := getStatus(t, base+"/ready")
			if status != c.want {
				t.Fatalf("/ready 状态码=%d want %d（body=%s）", status, c.want, body)
			}
			if c.want == 503 {
				for _, leak := range []string{"3306", "6379", "10.0.0.", "goroutine", "stack"} {
					if strings.Contains(body, leak) {
						t.Fatalf("/ready 503 响应泄漏内部信息 %q: %s", leak, body)
					}
				}
			}
		})
	}
}
