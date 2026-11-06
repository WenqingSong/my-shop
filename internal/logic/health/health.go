package health

import (
	"context"
	"sync"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/health/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// probeDependencies 探测 DB 与 Redis 连通性（readiness 的事实来源）。
// 默认实现：MySQL PingMaster + Redis PING，任一不可达返回 error。
// 该变量可被测试替换，以验证「结果缓存不重复探测」「探测超时」「失败 fail-closed」等语义。
var probeDependencies = func(ctx context.Context) error {
	if err := g.DB().PingMaster(); err != nil {
		return err
	}
	if _, err := g.Redis().Do(ctx, "PING"); err != nil {
		return err
	}
	return nil
}

type sHealth struct {
	readyMu    sync.Mutex
	readyAt    time.Time
	readyOK    bool
	readyValid bool
}

func init() {
	service.RegisterHealth(New())
}

// New creates and returns the health check service implementation.
func New() *sHealth {
	return &sHealth{}
}

// Liveness returns the service liveness status.
func (s *sHealth) Liveness(ctx context.Context) (*v1.LivenessRes, error) {
	return &v1.LivenessRes{
		Status: "ok",
		Time:   time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

// Readiness returns the service readiness status (fail-closed).
// 判定 = DB 与 Redis 均可达；探测带超时、结果进程内缓存 TTL 窗口内重复调用不新增探测。
// 任一依赖不可达返回 codes.CodeServiceUnavailable（503），不泄漏数据库地址/Redis 信息/堆栈。
func (s *sHealth) Readiness(ctx context.Context) (*v1.ReadinessRes, error) {
	ttl, err := readinessCacheTTL(ctx)
	if err != nil {
		glog.Errorf(ctx, "读取 %s 失败: %v", config.KeyReadinessCacheTTL, err)
		return nil, codes.New(codes.CodeServiceUnavailable)
	}
	timeout, err := readinessProbeTimeout(ctx)
	if err != nil {
		glog.Errorf(ctx, "读取 %s 失败: %v", config.KeyReadinessProbeTimeout, err)
		return nil, codes.New(codes.CodeServiceUnavailable)
	}

	// 探测期间持有锁：并发下同一窗口内只有首个调用真实探测，其余命中缓存（单飞语义）。
	s.readyMu.Lock()
	defer s.readyMu.Unlock()

	if s.readyValid && time.Since(s.readyAt) < ttl {
		if s.readyOK {
			return &v1.ReadinessRes{Status: "ready"}, nil
		}
		return nil, codes.New(codes.CodeServiceUnavailable)
	}

	ok := probeReady(ctx, timeout)
	s.readyOK = ok
	s.readyAt = time.Now()
	s.readyValid = true

	if !ok {
		return nil, codes.New(codes.CodeServiceUnavailable)
	}
	return &v1.ReadinessRes{Status: "ready"}, nil
}

// probeReady 在限定超时内执行依赖探测，超时视为未就绪（fail-closed）。
// PingMaster 不接收 ctx，故将探测放入 goroutine 并用超时 select 兜底，保证探测时长受控。
func probeReady(ctx context.Context, timeout time.Duration) bool {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ch := make(chan error, 1)
	go func() {
		ch <- probeDependencies(probeCtx)
	}()
	select {
	case err := <-ch:
		return err == nil
	case <-probeCtx.Done():
		return false
	}
}

// readinessCacheTTL 返回 readiness 结果缓存 TTL（秒），默认 3s，可经 READINESS_CACHE_TTL 覆盖。
// 配置非法返回错误，由调用方按 fail-closed 处理（/ready 返回 503）。
func readinessCacheTTL(ctx context.Context) (time.Duration, error) {
	secs, err := config.Int(ctx, config.KeyReadinessCacheTTL)
	if err != nil {
		return 0, err
	}
	if secs <= 0 {
		secs = 3
	}
	return time.Duration(secs) * time.Second, nil
}

// readinessProbeTimeout 返回 readiness 探测超时（秒），默认 1s，可经 READINESS_PROBE_TIMEOUT 覆盖。
// 配置非法返回错误，由调用方按 fail-closed 处理（/ready 返回 503）。
func readinessProbeTimeout(ctx context.Context) (time.Duration, error) {
	secs, err := config.Int(ctx, config.KeyReadinessProbeTimeout)
	if err != nil {
		return 0, err
	}
	if secs <= 0 {
		secs = 1
	}
	return time.Duration(secs) * time.Second, nil
}
