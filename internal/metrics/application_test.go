package metrics

// T4-02 应用级指标白盒测试：
//   - HTTP 三个指标（总数/耗时/在途）确实被写入，且标签 schema 低基数；
//   - 路由标签未命中时为固定值 unmatched，状态码非法时归一化为 unknown；
//   - Kafka Lag 区分分区并支持删除消失分区；
//   - Outbox 最老年龄的「未知不覆盖、空积压写 0」语义；
//   - 采样器状态指标只在观测后导出。
// 仅依赖进程内 registry，不需要 MySQL/Redis/Kafka。

import (
	"context"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

const (
	metricHTTPRequestsTotal   = "surgecart_http_requests_total"
	metricHTTPRequestDuration = "surgecart_http_request_duration_seconds"
	metricHTTPInFlight        = "surgecart_http_requests_in_flight"
	metricKafkaConsumerLag    = "surgecart_kafka_consumer_lag"
	metricSamplerUp           = "surgecart_sampler_up"
	metricOutboxOldestAge     = "surgecart_outbox_oldest_age_seconds"
)

// gaugeValueOrMissing 返回 Gauge 值；不存在时 ok=false（用于区分 0 与「未导出」）。
func gaugeValueOrMissing(t *testing.T, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	metric := findMetric(gatherFamily(t, name), labels)
	if metric == nil || metric.GetGauge() == nil {
		return 0, false
	}
	return metric.GetGauge().GetValue(), true
}

// TestHTTPMetricsRecordRequest 覆盖 T4-02-01/02/03：
// 一次请求完成后 Counter +1、Histogram 样本 +1 且记录真实耗时、In-flight 恢复原值。
func TestHTTPMetricsRecordRequest(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")

	labels := map[string]string{"method": "GET", "route": "/products/:id", "status": "200"}
	before := counterValue(t, metricHTTPRequestsTotal, labels)
	histBefore := histogramOf(t, metricHTTPRequestDuration, labels)
	var samplesBefore, inFlightBefore float64
	if histBefore != nil {
		samplesBefore = float64(histBefore.GetSampleCount())
	}
	inFlightBefore, _ = gaugeValueOrMissing(t, metricHTTPInFlight, nil)

	HTTPRequestStarted()
	if v, ok := gaugeValueOrMissing(t, metricHTTPInFlight, nil); !ok || v != inFlightBefore+1 {
		t.Fatalf("In-flight=(%v,%v)，进入请求后应为 %v", v, ok, inFlightBefore+1)
	}

	const d = 12 * time.Millisecond
	HTTPRequestFinished("GET", "/products/:id", 200, d)

	if delta := counterValue(t, metricHTTPRequestsTotal, labels) - before; delta != 1 {
		t.Fatalf("HTTP 请求总数 delta=%v want 1", delta)
	}
	histAfter := histogramOf(t, metricHTTPRequestDuration, labels)
	if histAfter == nil {
		t.Fatal("HTTP 耗时直方图未观测")
	}
	if delta := float64(histAfter.GetSampleCount()) - samplesBefore; delta != 1 {
		t.Fatalf("直方图样本数 delta=%v want 1", delta)
	}
	if sum := histAfter.GetSampleSum(); sum <= 0 {
		t.Fatalf("直方图和应为正的真实耗时，实际 %v", sum)
	}
	if v, ok := gaugeValueOrMissing(t, metricHTTPInFlight, nil); !ok || v != inFlightBefore {
		t.Fatalf("In-flight=(%v,%v)，请求结束后应恢复 %v", v, ok, inFlightBefore)
	}
}

// TestHTTPMetricsLabelCardinality 覆盖 T4-02-04：
// 未命中路由使用固定 unmatched、非法状态码归一化为 unknown，标签集合只有 method/route/status。
func TestHTTPMetricsLabelCardinality(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")

	// 模拟两条「动态 URL」请求：它们没有路由模板，必须落到同一个 unmatched 序列。
	HTTPRequestFinished("GET", "", 404, time.Millisecond)
	HTTPRequestFinished("GET", "", 404, time.Millisecond)
	// 模拟异常状态码：不得把任意整数写成标签。
	HTTPRequestFinished("POST", "/orders", 0, time.Millisecond)

	unmatched := findMetric(gatherFamily(t, metricHTTPRequestsTotal), map[string]string{
		"method": "GET", "route": UnmatchedRoute, "status": "404",
	})
	if unmatched == nil {
		t.Fatalf("未命中路由必须归一到 route=%s", UnmatchedRoute)
	}
	if got := unmatched.GetCounter().GetValue(); got < 2 {
		t.Fatalf("unmatched 序列计数=%v，动态 URL 应被归一化到同一序列", got)
	}
	if findMetric(gatherFamily(t, metricHTTPRequestsTotal), map[string]string{
		"method": "POST", "route": "/orders", "status": "unknown",
	}) == nil {
		t.Fatal("非法状态码必须归一化为 status=unknown")
	}

	// 标签 schema 只允许 method/route/status。
	for _, name := range []string{metricHTTPRequestsTotal, metricHTTPRequestDuration} {
		family := gatherFamily(t, name)
		if family == nil {
			t.Fatalf("指标 %s 不存在", name)
		}
		for _, metric := range family.GetMetric() {
			for label := range labelsOf(metric) {
				switch label {
				case "method", "route", "status":
				default:
					t.Fatalf("指标 %s 出现非预期标签 %q（只允许 method/route/status）", name, label)
				}
			}
		}
	}
}

// TestKafkaLagMetricPerPartitionAndDelete 覆盖 T4-02-05：
// 每个分区一条序列，标签为 consumer_group/topic/partition；分区消失后可删除序列。
func TestKafkaLagMetricPerPartitionAndDelete(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")

	SetKafkaConsumerLag("g1", "t1", 0, 5)
	SetKafkaConsumerLag("g1", "t1", 1, -3) // 负值必须被夹到 0（Lag 不允许为负）
	SetKafkaConsumerLag("g1", "t1", 2, 0)

	if v, ok := gaugeValueOrMissing(t, metricKafkaConsumerLag, map[string]string{
		"consumer_group": "g1", "topic": "t1", "partition": "0",
	}); !ok || v != 5 {
		t.Fatalf("分区 0 Lag=(%v,%v) want (5,true)", v, ok)
	}
	if v, ok := gaugeValueOrMissing(t, metricKafkaConsumerLag, map[string]string{
		"consumer_group": "g1", "topic": "t1", "partition": "1",
	}); !ok || v != 0 {
		t.Fatalf("负 Lag 必须夹到 0，实际 (%v,%v)", v, ok)
	}

	family := gatherFamily(t, metricKafkaConsumerLag)
	for _, metric := range family.GetMetric() {
		for label := range labelsOf(metric) {
			switch label {
			case "consumer_group", "topic", "partition":
			default:
				t.Fatalf("Kafka Lag 出现非预期标签 %q", label)
			}
		}
	}

	DeleteKafkaConsumerLag("g1", "t1", 2)
	if _, ok := gaugeValueOrMissing(t, metricKafkaConsumerLag, map[string]string{
		"consumer_group": "g1", "topic": "t1", "partition": "2",
	}); ok {
		t.Fatal("删除后分区 2 的序列必须消失")
	}
}

// TestSamplerUpMetric 覆盖失败状态指标：1=最近一次采样成功，0=失败。
func TestSamplerUpMetric(t *testing.T) {
	SetSamplerUp(SamplerKafkaLag, true)
	if v, ok := gaugeValueOrMissing(t, metricSamplerUp, map[string]string{"sampler": SamplerKafkaLag}); !ok || v != 1 {
		t.Fatalf("采样成功时 sampler_up=(%v,%v) want (1,true)", v, ok)
	}
	SetSamplerUp(SamplerKafkaLag, false)
	if v, ok := gaugeValueOrMissing(t, metricSamplerUp, map[string]string{"sampler": SamplerKafkaLag}); !ok || v != 0 {
		t.Fatalf("采样失败时 sampler_up=(%v,%v) want (0,true)", v, ok)
	}
}

// TestOutboxOldestAgeMetric 覆盖 T4-02-08 的语义：
//   - 采样成功且年龄已知 → 写入 age；
//   - 年龄未知（并发处理窗口）→ 保留上一次有效年龄，不写 0；
//   - 空积压（Pending=0）→ 年龄确实是 0；
//   - 指标关闭 → 不写任何值。
func TestOutboxOldestAgeMetric(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")

	SetOutboxBacklogSample(ctx, 3, 90*time.Second, true)
	if v, ok := gaugeValueOrMissing(t, metricOutboxOldestAge, nil); !ok || v != 90 {
		t.Fatalf("最老年龄=(%v,%v) want (90,true)", v, ok)
	}
	if v := gaugeValue(t, metricOutboxBacklogPending, nil); v != 3 {
		t.Fatalf("积压=%v want 3", v)
	}

	// 年龄未知：保留 90，不得写成 0。
	SetOutboxBacklogSample(ctx, 2, 0, false)
	if v, ok := gaugeValueOrMissing(t, metricOutboxOldestAge, nil); !ok || v != 90 {
		t.Fatalf("年龄未知时最老年龄=(%v,%v)，必须保留上一次有效值 90", v, ok)
	}
	if v := gaugeValue(t, metricOutboxBacklogPending, nil); v != 2 {
		t.Fatalf("积压应更新为 2，实际 %v", v)
	}

	// 空积压：年龄真实为 0。
	SetOutboxBacklogSample(ctx, 0, 0, true)
	if v, ok := gaugeValueOrMissing(t, metricOutboxOldestAge, nil); !ok || v != 0 {
		t.Fatalf("空积压时最老年龄=(%v,%v) want (0,true)", v, ok)
	}

	// 指标关闭：不改写。
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "false")
	SetOutboxBacklogSample(ctx, 9, 4*time.Second, true)
	if v := gaugeValue(t, metricOutboxBacklogPending, nil); v != 0 {
		t.Fatalf("指标关闭时不得改写积压，实际 %v", v)
	}
}

// TestApplicationMetricsHelpAndType 覆盖指标规范：HELP 非空、TYPE 正确、单位含 _seconds。
func TestApplicationMetricsHelpAndType(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	HTTPRequestFinished("GET", "/health", 200, time.Millisecond)
	SetOutboxBacklogSample(context.Background(), 1, time.Second, true)

	cases := map[string]dto.MetricType{
		metricHTTPRequestsTotal:   dto.MetricType_COUNTER,
		metricHTTPRequestDuration: dto.MetricType_HISTOGRAM,
		metricHTTPInFlight:        dto.MetricType_GAUGE,
		metricOutboxOldestAge:     dto.MetricType_GAUGE,
	}
	for name, wantType := range cases {
		family := gatherFamily(t, name)
		if family == nil {
			t.Fatalf("指标 %s 未导出", name)
		}
		if family.GetHelp() == "" {
			t.Fatalf("指标 %s 的 HELP 不得为空", name)
		}
		if family.GetType() != wantType {
			t.Fatalf("指标 %s 类型=%v want %v", name, family.GetType(), wantType)
		}
	}
}
