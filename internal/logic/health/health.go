// 健康检查语义（T2-02 冻结，禁止扩散）：
//
//	/health（Liveness）只回答「HTTP 服务进程是否存活」。
//	  - 不访问 MySQL / Redis / Kafka；
//	  - 不检查 Consumer 是否加入消费组，也不检查 Outbox 积压；
//	  - 正常运行恒返回 200。
//
//	/ready（Readiness）只回答「HTTP 写入路径当前依赖的强一致存储是否可达」。
//	  - 判定集合被冻结为：MySQL PingMaster 成功 且 Redis PING 成功；
//	  - 任一不可达返回 503（fail-closed），依赖恢复后在缓存 TTL 窗口内恢复 200；
//	  - 探测超时默认 1s、结果缓存 TTL 默认 3s（可经 READINESS_PROBE_TIMEOUT /
//	    READINESS_CACHE_TTL 覆盖，读取方式统一走 internal/config）。
//
// 明确禁止：把 Kafka 连接状态、Consumer Group 状态、Consumer 是否在消费、Outbox 积压
// 加入 /ready 或 /health。Kafka 故障时 HTTP 写入路径仍能把请求可靠写入 MySQL Outbox，
// 系统应表现为「异步处理延迟增加」，而不是把整个 HTTP 入口摘流。
// 对应的观测能力由 Prometheus 指标承担（flashsale_consumer_joined、
// flashsale_outbox_backlog_pending），不参与健康判定。
//
// 本包刻意不依赖 internal/mq、internal/mq/kafka 与 internal/worker：
// 健康判定不存在任何摸到 Kafka 客户端的代码路径，这一点由依赖探测调用计数测试佐证。
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

// dependencyProbes 是 readiness 依赖集合的**唯一**形态：MySQL + Redis（冻结，不含 Kafka）。
//
// 拆成「集合」而不是若干包级函数变量的理由：
//   - 依赖集合是一个整体语义（改了就是改了 readiness 判定），用结构体表达更难被局部篡改；
//   - probeReady 在派生探测协程之前按值捕获该结构体，探测协程不再读写任何包级变量，
//     因此测试替换依赖集合与被取消的探测协程之间不存在数据竞争。
type dependencyProbes struct {
	mysql func(context.Context) error
	redis func(context.Context) error
}

// check 依次探测 MySQL 与 Redis；MySQL 失败即短路（不再探测 Redis），任一失败返回 error。
func (p dependencyProbes) check(ctx context.Context) error {
	if err := p.mysql(ctx); err != nil {
		return err
	}
	return p.redis(ctx)
}

// readinessProbes 是生产环境的依赖集合：MySQL PingMaster + Redis PING。
// 该变量可被测试替换，以验证「结果缓存不重复探测」「探测超时」「失败 fail-closed」
// 「MySQL 失败短路 Redis」等语义；替换只允许发生在测试的单协程上下文里。
var readinessProbes = dependencyProbes{
	mysql: func(ctx context.Context) error {
		return g.DB().PingMaster()
	},
	redis: func(ctx context.Context) error {
		_, err := g.Redis().Do(ctx, "PING")
		return err
	},
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
//
// 该实现只有「进程能走到这里」这一个条件：不读配置、不探测任何外部依赖、
// 不查询 Consumer/Outbox 状态。因此 MySQL / Redis / Kafka 全部不可用时仍然返回 200。
// 任何新增依赖探测都必须先推翻本语义，而不是在这里顺手加一个 Ping。
func (s *sHealth) Liveness(ctx context.Context) (*v1.LivenessRes, error) {
	return &v1.LivenessRes{
		Status: "ok",
		Time:   time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

// Readiness returns the service readiness status (fail-closed).
// 判定 = MySQL 与 Redis 均可达（集合冻结，Kafka 不在其中）；探测带超时、
// 结果进程内缓存 TTL 窗口内重复调用不新增探测。
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
//
// 探测协程只使用调用方同步捕获的 probes 副本：即使本函数已因超时返回、协程仍在收尾，
// 它也不会再去读任何包级变量（测试替换依赖集合、进程关闭都不会与它产生数据竞争）。
func probeReady(ctx context.Context, timeout time.Duration) bool {
	probes := readinessProbes
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ch := make(chan error, 1)
	go func() {
		ch <- probes.check(probeCtx)
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
