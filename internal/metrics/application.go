// 应用级监控指标（T4-02）：HTTP 请求、Kafka Consumer Lag、Outbox 采样状态。
//
// 与 metrics.go 的关系：metrics.go 承载秒杀域（flashsale_*）的业务观测指标，
// 本文件承载「应用级」指标（surgecart_* 前缀）。两者共用同一个默认 Registry
// （prometheus.DefaultRegisterer），因此 /metrics 一次抓取同时包含业务指标、
// 应用指标与默认 Registry 自带的 Go Runtime / Process Collector（不重复实现）。
//
// 三条硬约束贯穿本文件：
//  1. 不重复定义已有指标：Outbox 待发布数量沿用 flashsale_outbox_backlog_pending，
//     本文件只新增它缺失的「最老事件年龄」与采样状态；
//  2. 不把采集失败伪装成零值：采样失败时只更新 sampler_up=0，业务指标保留上一次有效值；
//  3. 标签基数受控：HTTP 只用 method/route(路由模板)/status，Kafka 只用
//     consumer_group/topic/partition，绝不使用 URL、用户/订单/商品 ID 或查询参数。
package metrics

import (
	"context"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// UnmatchedRoute 是「请求未命中任何注册路由」时使用的固定路由标签。
//
// 为什么需要它：404/静态文件/入口层兜底路径没有路由模板，若退回原始 URL.Path，
// 任意扫描路径（如 /wp-admin.php、/a/1）都会生成新的 Time Series，构成指标炸弹。
const UnmatchedRoute = "unmatched"

// sampler 标签取值（低基数枚举）。采样器名称即「进程内后台采样循环」的标识。
const (
	// SamplerOutbox 是 Outbox 积压/最老年龄采样器。
	SamplerOutbox = "outbox"
	// SamplerKafkaLag 是 Kafka Consumer Lag 采样器。
	SamplerKafkaLag = "kafka_lag"
)

var (
	// HTTP 指标：method/route/status 三个标签都是低基数枚举或模板。
	httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "surgecart",
		Name:      "http_requests_total",
		Help:      "HTTP 请求总数（按方法/路由模板/状态码）。路由为 GoFrame 已匹配的路由模板，未命中为 unmatched。",
	}, []string{"method", "route", "status"})

	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "surgecart",
		Name:      "http_request_duration_seconds",
		Help:      "HTTP 请求处理耗时（秒，按方法/路由模板/状态码），用于 p95/p99。",
		Buckets:   histogramBuckets,
	}, []string{"method", "route", "status"})

	httpRequestsInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "surgecart",
		Name:      "http_requests_in_flight",
		Help:      "当前正在处理中的 HTTP 请求数（进入中间件 +1，请求结束 -1）。",
	})

	// kafkaConsumerLag 是分区级 Lag（messages）。partition 为字符串标签，
	// 分区数由 Topic 决定（当前设计为单分区），随 Topic 变化收敛，不会随业务量增长。
	kafkaConsumerLag = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "surgecart",
		Name:      "kafka_consumer_lag",
		Help:      "Kafka 消费组对目标主题的分区 Lag（messages）= max(0, log_end_offset - 已提交位移)；无提交位移时以 log_start_offset 为基线。",
	}, []string{"consumer_group", "topic", "partition"})

	samplerUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "surgecart",
		Name:      "sampler_up",
		Help:      "后台采样器最近一次采样是否成功：1=成功，0=失败（失败时业务指标保留上一次有效值，不伪造零值）。",
	}, []string{"sampler"})

	// outboxOldestAge 与 flashsale_outbox_backlog_pending 来自同一次 Backlog 采样，
	// 因此同样「只在被成功观测后才导出」：db_scanner 模式或从未采样成功时不导出该序列。
	outboxOldestAge = newObservableGaugeNamed("surgecart_outbox_oldest_age_seconds",
		"Outbox 中最老的待发布事件年龄（秒，MySQL NOW(6) - created_at，口径同 flashsale_outbox_backlog_pending）。仅 dispatch_mode=kafka 且采样成功后导出。")
)

func init() {
	// 与 metrics.go 的 init 共用默认 Registry：只有一个 Registry，不存在重复注册。
	prometheus.MustRegister(
		httpRequestsTotal, httpRequestDuration, httpRequestsInFlight,
		kafkaConsumerLag, samplerUp, outboxOldestAge,
	)
}

// HTTPRequestStarted 记录一个请求进入处理阶段（In-flight +1）。
//
// 调用方（internal/middleware.Metrics）必须保证与 HTTPRequestFinished 成对出现，
// 且「是否启用指标」只判断一次：Enabled 在本进程内由静态配置决定，
// 但若每侧各自判断，配置抖动会导致 In-flight 计数永久偏移。
func HTTPRequestStarted() {
	httpRequestsInFlight.Inc()
}

// HTTPRequestFinished 记录请求完成：In-flight -1、请求总数 +1、耗时入库。
//
// status 必须是业务最终写出的 HTTP 状态码（由调用方在中间件 Next() 返回后读取），
// route 必须是路由模板（未命中传 UnmatchedRoute），禁止传原始 URL。
func HTTPRequestFinished(method, route string, status int, d time.Duration) {
	httpRequestsInFlight.Dec()
	if method == "" {
		method = "unknown"
	}
	if route == "" {
		route = UnmatchedRoute
	}
	statusLabel := httpStatusLabel(status)
	httpRequestsTotal.WithLabelValues(method, route, statusLabel).Inc()
	if d >= 0 {
		httpRequestDuration.WithLabelValues(method, route, statusLabel).Observe(d.Seconds())
	}
}

// httpStatusLabel 把状态码规范化为有界标签。
//
// 合法 HTTP 状态码本身就是有限集合（100-599），非法/未写出的值统一归到 unknown，
// 避免把异常实现的任意整数写进标签。
func httpStatusLabel(status int) string {
	if status < 100 || status > 599 {
		return "unknown"
	}
	return strconv.Itoa(status)
}

// SetSamplerUp 写入某个采样器最近一次采样结果（1=成功、0=失败）。
//
// 语义边界：它只表达「采样链路是否健康」，不代表被采指标的真实值；
// 采样失败时调用方必须保留业务指标的上一次有效值，不得写入 0。
// 与 HTTP 三个指标一样，本函数假设调用方已判断 Enabled（采样器只在启用时才启动）。
func SetSamplerUp(sampler string, up bool) {
	value := 0.0
	if up {
		value = 1
	}
	samplerUp.WithLabelValues(sampler).Set(value)
}

// SetKafkaConsumerLag 写入单个分区的 Lag 采样值（messages）。
//
// 只由 Kafka Lag 采样器在「该分区采样成功」后调用：部分分区失败时调用方只更新
// 成功的分区，失败分区保留上一次有效值（绝不写 0）。
func SetKafkaConsumerLag(consumerGroup, topic string, partition int32, lag int64) {
	if lag < 0 {
		lag = 0
	}
	kafkaConsumerLag.WithLabelValues(consumerGroup, topic, strconv.Itoa(int(partition))).Set(float64(lag))
}

// DeleteKafkaConsumerLag 删除已不存在分区的序列（Topic 分区收缩/重建后的陈旧序列）。
//
// 只在「整轮采样完全成功」时由调用方对消失的分区调用：部分失败时无法区分
// 「分区消失」与「分区采样失败」，此时删除会丢失仍然有效的观测值。
func DeleteKafkaConsumerLag(consumerGroup, topic string, partition int32) {
	kafkaConsumerLag.DeleteLabelValues(consumerGroup, topic, strconv.Itoa(int(partition)))
}

// SetOutboxBacklogSample 写入一次成功的 Outbox 积压采样。
//
// pending 是该口径下的待发布记录数（status=queued 且 published_at IS NULL）。
// oldestAgeKnown=false 表示本次查询「统计与回表之间记录被并发处理掉」，
// 无法确定最老事件年龄：此时保留上一次有效年龄，绝不写成 0（0 只表示真的没有积压）。
func SetOutboxBacklogSample(ctx context.Context, pending int64, oldestAge time.Duration, oldestAgeKnown bool) {
	if !Enabled(ctx) {
		return
	}
	if pending < 0 {
		pending = 0
	}
	outboxBacklogPending.set(float64(pending))
	if !oldestAgeKnown {
		return
	}
	if oldestAge < 0 {
		oldestAge = 0
	}
	outboxOldestAge.set(oldestAge.Seconds())
}
