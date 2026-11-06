// Package metrics 承载秒杀链路（V5 容量保护与可观测性）的 Prometheus 指标。
//
// 本包聚焦秒杀下单接口与排队消费链路，非全站通用指标平台：只注册秒杀域指标
// （flashsale_*），按「接口 / 结果」低基数维度区分，指标为进程内观测计数（非业务事实），
// 用于 QPS / 成功率 / 拒绝率 / p95 / p99 采集，且可与 MySQL 订单数、queued 数、拒绝数对账。
//
// 指标采集默认关闭（flash_sale.metrics.enabled 默认 false），关闭时 Observe 系列为空操作；
// 对外暴露 GET /metrics 由调用方（cmd 组合根）在开关开启时挂载。
package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// 结果标签取值（与 Contract 的 success/queued/gate_rejected/rate_limited/queue_full/error 枚举一致，
// consume 链路扩展 failed/dead 表示业务失败终态与死信）。
const (
	ResultQueued       = "queued"        // 下单接口：受理入队成功
	ResultSuccess      = "success"       // 消费链路：落单成功
	ResultGateRejected = "gate_rejected" // 下单接口：闸门业务拒绝（售罄/时间窗/已购/幂等冲突等）
	ResultRateLimited  = "rate_limited"  // 下单接口：限流拒绝 12009
	ResultQueueFull    = "queue_full"    // 下单接口：排队满拒绝 12010
	ResultFailed       = "failed"        // 消费链路：业务失败终态
	ResultDead         = "dead"          // 消费链路：技术失败重试超限（死信）
	ResultError        = "error"         // 技术错误（503/500/参数错误）
)

// 接口标签取值。
const (
	InterfaceOrder   = "order"   // 秒杀下单接口
	InterfaceConsume = "consume" // 排队消费链路
)

// Outbox 发布失败原因标签取值（低基数：只区分故障类别，不含 request_id/user_id/activity_id）。
const (
	OutboxFailureKafka         = "kafka"          // Broker 明确返回失败（超时/重试耗尽等）
	OutboxFailureUnknown       = "unknown"        // 批次级结果未知（context 到期/取消、客户端关闭）
	OutboxFailureEncode        = "encode"         // 事件构造/校验失败（历史数据不满足契约）
	OutboxFailureMissingResult = "missing_result" // 发布器未按入参返回对应结果
)

// Outbox 标记未命中原因标签取值（低基数）。
const (
	OutboxMarkLeaseLost        = "lease_lost"        // 租约令牌已被他人接管或人工修复重置
	OutboxMarkAlreadyPublished = "already_published" // 记录已由其他 Publisher 标记发布
)

// histogramBuckets 是秒杀请求耗时的直方图桶（秒），覆盖微秒~10 秒量级，保证 p95/p99 分辨率。
var histogramBuckets = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

var (
	requestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flashsale",
		Name:      "requests_total",
		Help:      "秒杀请求总数（按接口/结果），用于 QPS、成功率、拒绝率。",
	}, []string{"interface", "result"})

	requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "flashsale",
		Name:      "request_duration_seconds",
		Help:      "秒杀请求耗时（按接口/结果），用于 p95/p99。",
		Buckets:   histogramBuckets,
	}, []string{"interface", "result"})

	// B2 Outbox Publisher 指标：只做进程内观测计数，不是业务事实。
	// 未发布数量与最老待发布年龄需要查库，不在此处用 Gauge 回调（避免抓取时阻塞 DB），
	// 由 OutboxPublisher.Backlog 提供统计接口，供后续 Prometheus 采集或健康检查接入。
	outboxClaimed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "flashsale",
		Name:      "outbox_claimed_total",
		Help:      "Outbox 领取到的待发布请求总数。",
	})
	outboxLeaseReclaimed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "flashsale",
		Name:      "outbox_lease_reclaimed_total",
		Help:      "Outbox 领取时发现租约已过期并回收的请求总数（进程崩溃/超时后的恢复量）。",
	})
	outboxPublished = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "flashsale",
		Name:      "outbox_published_total",
		Help:      "Outbox 获得 Broker 确认的消息总数。",
	})
	outboxPublishFailed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flashsale",
		Name:      "outbox_publish_failed_total",
		Help:      "Outbox 未获得 Broker 确认的消息总数（按失败类别）。",
	}, []string{"reason"})
	outboxRetried = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "flashsale",
		Name:      "outbox_retried_total",
		Help:      "Outbox 已写入退避时间、等待下一轮重试的请求总数。",
	})
	outboxMarkSkipped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flashsale",
		Name:      "outbox_mark_skipped_total",
		Help:      "Outbox 条件标记未命中的请求总数（租约丢失/已被发布）。",
	}, []string{"reason"})
	outboxPublishDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "flashsale",
		Name:      "outbox_publish_round_duration_seconds",
		Help:      "Outbox 单轮领取+发布+标记耗时。",
		Buckets:   histogramBuckets,
	})
)

func init() {
	prometheus.MustRegister(
		requestsTotal, requestDuration,
		outboxClaimed, outboxLeaseReclaimed, outboxPublished, outboxPublishFailed,
		outboxRetried, outboxMarkSkipped, outboxPublishDuration,
	)
}

// Enabled 读取指标开关（flash_sale.metrics.enabled，默认关闭）。
func Enabled(ctx context.Context) bool {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.metrics.enabled", false)
	if err != nil || v == nil {
		return false
	}
	return v.Bool()
}

// ObserveOrder 记录一次秒杀下单请求（接口=order）。
func ObserveOrder(ctx context.Context, result string, d time.Duration) {
	if !Enabled(ctx) {
		return
	}
	requestsTotal.WithLabelValues(InterfaceOrder, result).Inc()
	requestDuration.WithLabelValues(InterfaceOrder, result).Observe(d.Seconds())
}

// ObserveConsume 记录一次排队消费处理（接口=consume）。
func ObserveConsume(ctx context.Context, result string, d time.Duration) {
	if !Enabled(ctx) {
		return
	}
	requestsTotal.WithLabelValues(InterfaceConsume, result).Inc()
	requestDuration.WithLabelValues(InterfaceConsume, result).Observe(d.Seconds())
}

// ObserveOutboxClaim 记录一轮 Outbox 领取结果：claimed 为领取条数，leaseReclaimed 为其中租约过期回收条数。
func ObserveOutboxClaim(ctx context.Context, claimed, leaseReclaimed int) {
	if !Enabled(ctx) || claimed <= 0 {
		return
	}
	outboxClaimed.Add(float64(claimed))
	if leaseReclaimed > 0 {
		outboxLeaseReclaimed.Add(float64(leaseReclaimed))
	}
}

// ObserveOutboxPublished 记录一条获得 Broker 确认的消息。
func ObserveOutboxPublished(ctx context.Context) {
	if !Enabled(ctx) {
		return
	}
	outboxPublished.Inc()
}

// ObserveOutboxFailed 记录一条未获 Broker 确认（或事件无法构造）的消息，reason 为低基数类别。
func ObserveOutboxFailed(ctx context.Context, reason string) {
	if !Enabled(ctx) {
		return
	}
	outboxPublishFailed.WithLabelValues(reason).Inc()
}

// ObserveOutboxRetried 记录一条已写入退避时间、等待重试的请求。
func ObserveOutboxRetried(ctx context.Context) {
	if !Enabled(ctx) {
		return
	}
	outboxRetried.Inc()
}

// ObserveOutboxMarkSkipped 记录一条条件标记未命中的请求（租约丢失/已被发布），reason 为低基数类别。
func ObserveOutboxMarkSkipped(ctx context.Context, reason string) {
	if !Enabled(ctx) {
		return
	}
	outboxMarkSkipped.WithLabelValues(reason).Inc()
}

// ObserveOutboxPublishDuration 记录一轮 Outbox 发布耗时。
func ObserveOutboxPublishDuration(ctx context.Context, d time.Duration) {
	if !Enabled(ctx) || d <= 0 {
		return
	}
	outboxPublishDuration.Observe(d.Seconds())
}

// Handler 返回 Prometheus 指标采集 handler（默认 registry 的文本格式）。
func Handler() http.Handler {
	return promhttp.Handler()
}
