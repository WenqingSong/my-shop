// Package metrics 承载秒杀链路（V5 容量保护与可观测性）的 Prometheus 指标。
//
// 本包聚焦秒杀下单接口与排队消费链路，非全站通用指标平台：只注册秒杀域指标
// （flashsale_*），按「接口 / 活动 / 结果」维度区分，指标为进程内观测计数（非业务事实），
// 用于 QPS / 成功率 / 拒绝率 / p95 / p99 采集，且可与 MySQL 订单数、queued 数、拒绝数对账。
//
// 指标采集默认关闭（flash_sale.metrics.enabled 默认 false），关闭时 Observe 系列为空操作；
// 对外暴露 GET /metrics 由调用方（cmd 组合根）在开关开启时挂载。
package metrics

import (
	"context"
	"net/http"
	"strconv"
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

// histogramBuckets 是秒杀请求耗时的直方图桶（秒），覆盖微秒~10 秒量级，保证 p95/p99 分辨率。
var histogramBuckets = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

var (
	requestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flashsale",
		Name:      "requests_total",
		Help:      "秒杀请求总数（按接口/活动/结果），用于 QPS、成功率、拒绝率。",
	}, []string{"interface", "activity", "result"})

	requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "flashsale",
		Name:      "request_duration_seconds",
		Help:      "秒杀请求耗时（按接口/活动/结果），用于 p95/p99。",
		Buckets:   histogramBuckets,
	}, []string{"interface", "activity", "result"})
)

func init() {
	prometheus.MustRegister(requestsTotal, requestDuration)
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
func ObserveOrder(ctx context.Context, activityID int64, result string, d time.Duration) {
	if !Enabled(ctx) {
		return
	}
	activity := strconv.FormatInt(activityID, 10)
	requestsTotal.WithLabelValues(InterfaceOrder, activity, result).Inc()
	requestDuration.WithLabelValues(InterfaceOrder, activity, result).Observe(d.Seconds())
}

// ObserveConsume 记录一次排队消费处理（接口=consume）。
func ObserveConsume(ctx context.Context, activityID int64, result string, d time.Duration) {
	if !Enabled(ctx) {
		return
	}
	activity := strconv.FormatInt(activityID, 10)
	requestsTotal.WithLabelValues(InterfaceConsume, activity, result).Inc()
	requestDuration.WithLabelValues(InterfaceConsume, activity, result).Observe(d.Seconds())
}

// Handler 返回 Prometheus 指标采集 handler（默认 registry 的文本格式）。
func Handler() http.Handler {
	return promhttp.Handler()
}
