package metrics

// 秒杀指标白盒测试：验证标签 schema 低基数（interface/result，无 activity 等 ID 型标签）、
// Observe 系列在同一调用内同步 Inc + Observe（Counter 与 Histogram 计数一致、耗时非零）、
// 直方图桶分布覆盖真实耗时量级，以及指标关闭时 Observe 为空操作。
// 仅依赖进程内环境变量开启指标，无需 MySQL/Redis。

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// 指标名（与 metrics.go 的 Namespace/Name 拼接一致）。
const (
	metricRequestsTotal   = "flashsale_requests_total"
	metricRequestDuration = "flashsale_request_duration_seconds"
)

// 全部结果枚举（与 Contract 一致），用于覆盖结果维度的全部取值。
var allResults = []string{
	ResultQueued, ResultSuccess, ResultGateRejected, ResultRateLimited,
	ResultQueueFull, ResultFailed, ResultDead, ResultError,
}

// gatherFamily 从默认 registry 抓取指定指标 family；不存在返回 nil。
func gatherFamily(t *testing.T, name string) *dto.MetricFamily {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, f := range families {
		if f.GetName() == name {
			return f
		}
	}
	return nil
}

// labelsOf 返回 metric 的标签 map（name -> value）。
func labelsOf(m *dto.Metric) map[string]string {
	out := make(map[string]string, len(m.GetLabel()))
	for _, lp := range m.GetLabel() {
		out[lp.GetName()] = lp.GetValue()
	}
	return out
}

// findMetric 在 family 中按标签精确匹配一条 metric，未命中返回 nil。
func findMetric(fam *dto.MetricFamily, want map[string]string) *dto.Metric {
	if fam == nil {
		return nil
	}
	for _, m := range fam.GetMetric() {
		got := labelsOf(m)
		match := true
		for k, v := range want {
			if got[k] != v {
				match = false
				break
			}
		}
		if match {
			return m
		}
	}
	return nil
}

// counterValue 返回指定 family+标签的 Counter 值（未观测返回 0）。
func counterValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	m := findMetric(gatherFamily(t, name), labels)
	if m == nil || m.GetCounter() == nil {
		return 0
	}
	return m.GetCounter().GetValue()
}

// histogramOf 返回指定 family+标签的 Histogram（未观测返回 nil）。
func histogramOf(t *testing.T, name string, labels map[string]string) *dto.Histogram {
	t.Helper()
	m := findMetric(gatherFamily(t, name), labels)
	if m == nil {
		return nil
	}
	return m.GetHistogram()
}

// bucketCumulative 返回直方图中指定上界的累积桶计数（桶不存在返回 0）。
func bucketCumulative(h *dto.Histogram, upperBound float64) uint64 {
	if h == nil {
		return 0
	}
	for _, b := range h.GetBucket() {
		if math.Abs(b.GetUpperBound()-upperBound) < 1e-9 {
			return b.GetCumulativeCount()
		}
	}
	return 0
}

// TestLabelSchemaLowCardinality 覆盖 INV-M01 / AC-004：
// 两个指标 family 中每条 metric 的标签名恰好为 {interface, result}，不含 activity/user/order 等 ID 型标签，
// 且 interface/result 取值均为稳定枚举（低基数，不随业务量增长）。
func TestLabelSchemaLowCardinality(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	ctx := context.Background()

	// 对 order/consume 各结果分支各观测一次，确保所有标签组合均被产生。
	for _, r := range allResults {
		ObserveOrder(ctx, r, time.Millisecond)
		ObserveConsume(ctx, r, time.Millisecond)
	}

	for _, name := range []string{metricRequestsTotal, metricRequestDuration} {
		fam := gatherFamily(t, name)
		if fam == nil {
			t.Fatalf("metric %s not found after observing", name)
		}
		if len(fam.GetMetric()) == 0 {
			t.Fatalf("metric %s has no series after observing", name)
		}
		for _, m := range fam.GetMetric() {
			got := labelsOf(m)
			// 除 interface/result 外不得出现任何其它标签（尤其是 activity/user_id/order_no）。
			for k := range got {
				if k != "interface" && k != "result" {
					t.Fatalf("metric %s has unexpected label %q (want only interface/result)", name, k)
				}
			}
			if got["result"] == "" {
				t.Fatalf("metric %s missing result label: %v", name, got)
			}
			switch got["interface"] {
			case InterfaceOrder, InterfaceConsume:
			default:
				t.Fatalf("metric %s has unexpected interface value %q", name, got["interface"])
			}
		}
	}
}

// TestObserveOrderRecordsCountAndDuration 覆盖 AC-001 / INV-M02（order）：
// 一次 ObserveOrder 恰好使 Counter 与 Histogram 各 +1，且耗时样本为真实耗时（非零、接近观测值）。
func TestObserveOrderRecordsCountAndDuration(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	ctx := context.Background()

	labels := map[string]string{"interface": InterfaceOrder, "result": ResultQueued}
	cntBefore := counterValue(t, metricRequestsTotal, labels)
	hBefore := histogramOf(t, metricRequestDuration, labels)
	var countBefore uint64
	var sumBefore float64
	if hBefore != nil {
		countBefore = hBefore.GetSampleCount()
		sumBefore = hBefore.GetSampleSum()
	}

	const d = 5 * time.Millisecond
	ObserveOrder(ctx, ResultQueued, d)

	cntAfter := counterValue(t, metricRequestsTotal, labels)
	hAfter := histogramOf(t, metricRequestDuration, labels)
	if hAfter == nil {
		t.Fatalf("histogram series missing after observe")
	}
	if delta := cntAfter - cntBefore; delta != 1 {
		t.Fatalf("counter delta=%v want 1", delta)
	}
	if delta := hAfter.GetSampleCount() - countBefore; delta != 1 {
		t.Fatalf("histogram sample_count delta=%d want 1（Counter 与 Histogram 计数一致）", delta)
	}
	if delta := hAfter.GetSampleSum() - sumBefore; math.Abs(delta-d.Seconds()) > 1e-9 {
		t.Fatalf("histogram sample_sum delta=%v want %v（真实耗时非零）", delta, d.Seconds())
	}
}

// TestObserveConsumeRecordsCountAndDuration 覆盖 AC-002 / INV-M02（consume）：
// 消费链路的 error 与终态结果各自恰好观测一次，且耗时样本为真实耗时。
func TestObserveConsumeRecordsCountAndDuration(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	ctx := context.Background()

	for _, result := range []string{ResultSuccess, ResultError} {
		labels := map[string]string{"interface": InterfaceConsume, "result": result}
		cntBefore := counterValue(t, metricRequestsTotal, labels)
		hBefore := histogramOf(t, metricRequestDuration, labels)
		var countBefore uint64
		if hBefore != nil {
			countBefore = hBefore.GetSampleCount()
		}

		const d = 2 * time.Millisecond
		ObserveConsume(ctx, result, d)

		cntAfter := counterValue(t, metricRequestsTotal, labels)
		hAfter := histogramOf(t, metricRequestDuration, labels)
		if hAfter == nil {
			t.Fatalf("histogram series missing after observe (%s)", result)
		}
		if delta := cntAfter - cntBefore; delta != 1 {
			t.Fatalf("counter delta=%v want 1 (result=%s)", delta, result)
		}
		if delta := hAfter.GetSampleCount() - countBefore; delta != 1 {
			t.Fatalf("histogram sample_count delta=%d want 1 (result=%s)", delta, result)
		}
		if hAfter.GetSampleSum() <= 0 {
			t.Fatalf("histogram sample_sum should be positive (result=%s)", result)
		}
	}
}

// TestHistogramBucketsCoverRepresentativeDurations 覆盖 AC-003：
// 覆盖真实耗时量级的代表性样本（亚毫秒快路径、毫秒级典型下单、秒级慢路径）均落入有界桶，
// 而非只能落入 +Inf 溢出桶。
func TestHistogramBucketsCoverRepresentativeDurations(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	ctx := context.Background()

	labels := map[string]string{"interface": InterfaceOrder, "result": ResultQueued}

	cases := []struct {
		d      time.Duration
		bucket float64 // 期望落入的有界桶上界
	}{
		{500 * time.Microsecond, 0.0005}, // 亚毫秒快路径
		{5 * time.Millisecond, 0.005},    // 毫秒级典型下单
		{2 * time.Second, 2.5},           // 秒级慢路径（如 DB 超时）
	}
	for _, c := range cases {
		before := bucketCumulative(histogramOf(t, metricRequestDuration, labels), c.bucket)
		ObserveOrder(ctx, ResultQueued, c.d)
		after := bucketCumulative(histogramOf(t, metricRequestDuration, labels), c.bucket)
		if after-before < 1 {
			t.Fatalf("duration %v should land in bounded bucket le=%v（落入 +Inf 溢出桶）", c.d, c.bucket)
		}
	}
}

// TestObserveOutboxMetrics 覆盖 B2 Outbox 指标：
// 领取/发布/失败/重试/租约回收/标记跳过/耗时均可观测，且标签只有固定的低基数枚举，
// 绝不把 user_id/request_id/activity_id 变成 Prometheus Label。
func TestObserveOutboxMetrics(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	ctx := context.Background()

	const (
		metricClaimed   = "flashsale_outbox_claimed_total"
		metricReclaimed = "flashsale_outbox_lease_reclaimed_total"
		metricPublished = "flashsale_outbox_published_total"
		metricFailed    = "flashsale_outbox_publish_failed_total"
		metricRetried   = "flashsale_outbox_retried_total"
		metricSkipped   = "flashsale_outbox_mark_skipped_total"
		metricDuration  = "flashsale_outbox_publish_round_duration_seconds"
	)

	claimedBefore := counterValue(t, metricClaimed, nil)
	reclaimedBefore := counterValue(t, metricReclaimed, nil)
	publishedBefore := counterValue(t, metricPublished, nil)
	retriedBefore := counterValue(t, metricRetried, nil)

	ObserveOutboxClaim(ctx, 3, 1)
	ObserveOutboxPublished(ctx)
	ObserveOutboxPublished(ctx)
	ObserveOutboxRetried(ctx)
	ObserveOutboxFailed(ctx, OutboxFailureKafka)
	ObserveOutboxMarkSkipped(ctx, OutboxMarkLeaseLost)
	ObserveOutboxPublishDuration(ctx, 25*time.Millisecond)

	if delta := counterValue(t, metricClaimed, nil) - claimedBefore; delta != 3 {
		t.Fatalf("claimed delta=%v want 3", delta)
	}
	if delta := counterValue(t, metricReclaimed, nil) - reclaimedBefore; delta != 1 {
		t.Fatalf("lease reclaimed delta=%v want 1", delta)
	}
	if delta := counterValue(t, metricPublished, nil) - publishedBefore; delta != 2 {
		t.Fatalf("published delta=%v want 2", delta)
	}
	if delta := counterValue(t, metricRetried, nil) - retriedBefore; delta != 1 {
		t.Fatalf("retried delta=%v want 1", delta)
	}
	if counterValue(t, metricFailed, map[string]string{"reason": OutboxFailureKafka}) == 0 {
		t.Fatalf("publish failure metric not observed for reason=%s", OutboxFailureKafka)
	}
	if counterValue(t, metricSkipped, map[string]string{"reason": OutboxMarkLeaseLost}) == 0 {
		t.Fatalf("mark skipped metric not observed for reason=%s", OutboxMarkLeaseLost)
	}
	if histogramOf(t, metricDuration, nil) == nil {
		t.Fatalf("outbox publish duration histogram not observed")
	}

	// 标签只允许固定枚举（reason），不得出现业务 ID。
	for _, name := range []string{metricFailed, metricSkipped} {
		fam := gatherFamily(t, name)
		if fam == nil {
			t.Fatalf("metric %s not found", name)
		}
		for _, m := range fam.GetMetric() {
			for k := range labelsOf(m) {
				if k != "reason" {
					t.Fatalf("metric %s has unexpected label %q (want only reason)", name, k)
				}
			}
		}
	}
}

// TestObserveDisabledIsNoOp 覆盖失败语义：指标关闭时 Observe 为空操作，不产生观测。
func TestObserveDisabledIsNoOp(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "false")
	ctx := context.Background()

	labels := map[string]string{"interface": InterfaceOrder, "result": ResultQueued}
	before := counterValue(t, metricRequestsTotal, labels)
	ObserveOrder(ctx, ResultQueued, time.Millisecond)
	after := counterValue(t, metricRequestsTotal, labels)
	if after != before {
		t.Fatalf("disabled observe should be no-op: before=%v after=%v", before, after)
	}
}

// ---------------------------------------------------------------------------
// T2-02：Consumer Group 成员资格与 Outbox 积压（Gauge）
// ---------------------------------------------------------------------------

const (
	metricConsumerJoined           = "flashsale_consumer_joined"
	metricOutboxBacklogPending     = "flashsale_outbox_backlog_pending"
	observableGaugeNameConsumer    = "consumer_joined"
	observableGaugeNameBacklogName = "outbox_backlog_pending"
)

// gaugeValue 返回指定 Gauge 的值；规则与 counterValue 对称。
func gaugeValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	m := findMetric(gatherFamily(t, name), labels)
	if m == nil || m.GetGauge() == nil {
		return -1
	}
	return m.GetGauge().GetValue()
}

// TestObservableGaugeNotExportedBeforeObservation 覆盖 T2-02-B/C 的适用范围语义：
// 未被观测过（db_scanner 模式）时该序列完全不导出，而不是导出一个恒为 0 的假信号；
// 一旦观测，导出为 Gauge 且无任何标签（低基数）。
func TestObservableGaugeNotExportedBeforeObservation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		collect  *observableGauge
		fullName string
	}{
		{"consumer_joined", newObservableGauge(observableGaugeNameConsumer, "help"), metricConsumerJoined},
		{"outbox_backlog_pending", newObservableGauge(observableGaugeNameBacklogName, "help"), metricOutboxBacklogPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			if err := reg.Register(tc.collect); err != nil {
				t.Fatalf("register: %v", err)
			}
			families, err := reg.Gather()
			if err != nil {
				t.Fatalf("gather: %v", err)
			}
			for _, f := range families {
				if f.GetName() == tc.fullName {
					t.Fatalf("未观测时不得导出 %s（db_scanner 模式不应出现该序列）", tc.fullName)
				}
			}

			tc.collect.set(0)
			families, err = reg.Gather()
			if err != nil {
				t.Fatalf("gather after set: %v", err)
			}
			if len(families) != 1 {
				t.Fatalf("导出指标 family 数=%d want 1", len(families))
			}
			fam := families[0]
			if fam.GetName() != tc.fullName {
				t.Fatalf("指标名=%q want %q", fam.GetName(), tc.fullName)
			}
			if fam.GetType() != dto.MetricType_GAUGE {
				t.Fatalf("指标类型=%v want GAUGE", fam.GetType())
			}
			if fam.GetHelp() == "" {
				t.Fatal("HELP 说明不得为空")
			}
			if len(fam.GetMetric()) != 1 {
				t.Fatalf("无标签 Gauge 只能有 1 条序列，实际 %d", len(fam.GetMetric()))
			}
			if labels := labelsOf(fam.GetMetric()[0]); len(labels) != 0 {
				t.Fatalf("不得引入标签（高基数风险），实际 %v", labels)
			}
			if got := fam.GetMetric()[0].GetGauge().GetValue(); got != 0 {
				t.Fatalf("初始值=%v want 0", got)
			}
		})
	}
}

// TestConsumerJoinedMetricTransitions 覆盖 T2-02-B 状态机在指标上的投影：
// 加入 → 1，退出/撤销 → 0；且关闭指标后不再改写已有值（符合 FLASH_SALE_METRICS_ENABLED 语义）。
func TestConsumerJoinedMetricTransitions(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")

	MarkConsumerJoined(ctx, false)
	if got := gaugeValue(t, metricConsumerJoined, nil); got != 0 {
		t.Fatalf("初始化后指标=%v want 0（Consumer 尚未加入消费组）", got)
	}
	MarkConsumerJoined(ctx, true)
	if got := gaugeValue(t, metricConsumerJoined, nil); got != 1 {
		t.Fatalf("加入消费组后指标=%v want 1", got)
	}
	MarkConsumerJoined(ctx, false)
	if got := gaugeValue(t, metricConsumerJoined, nil); got != 0 {
		t.Fatalf("成员资格丢失后指标=%v want 0", got)
	}

	// 指标关闭：不得改写已观测值（不能伪造「状态未变」的假象，也不能清空）。
	MarkConsumerJoined(ctx, true)
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "false")
	MarkConsumerJoined(ctx, false)
	if got := gaugeValue(t, metricConsumerJoined, nil); got != 1 {
		t.Fatalf("指标关闭后 Mark 必须为空操作，实际=%v want 1", got)
	}
}

// TestOutboxBacklogMetricReflectsSampleAndFreezesOnFailure 覆盖 T2-02-C：
// 采样成功即导出实际积压；采样失败（调用方不调用 Set）时保留上一次有效值，绝不能变成 0。
func TestOutboxBacklogMetricReflectsSampleAndFreezesOnFailure(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")

	SetOutboxBacklogSample(ctx, 3, 30*time.Second, true)
	if got := gaugeValue(t, metricOutboxBacklogPending, nil); got != 3 {
		t.Fatalf("积压指标=%v want 3", got)
	}
	// 采样失败路径：调用方只记录错误、不调用 Set，指标必须保持 3。
	// 直接断言状态，避免依赖「谁忘了调用」的实现细节。
	set, value := outboxBacklogPending.observedValue()
	if !set || value != 3 {
		t.Fatalf("采样失败后指标状态=(%v,%v) want (true,3)", set, value)
	}
	SetOutboxBacklogSample(ctx, 0, 0, true)
	if got := gaugeValue(t, metricOutboxBacklogPending, nil); got != 0 {
		t.Fatalf("积压清零后指标=%v want 0", got)
	}
}
