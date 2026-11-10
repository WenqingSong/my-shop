package worker

// T4-02：Kafka Consumer Lag 采样器的生命周期与失败语义测试。
//
// 这些用例不连接 Broker：注入 fakeLagSampler 验证「采样是否发生、失败是否保留上次值、
// 关闭是否释放资源」。协议层（OffsetFetch/ListOffsets 解析与 Lag 计算）另有
// internal/mq/kafka/lag_test.go 用协议级假响应覆盖；真实 Broker 验证单独标记。

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	kafkamq "cnb.cool/go-cloud-devops/my-shop/internal/mq/kafka"
)

const (
	kafkaLagMetricName  = "surgecart_kafka_consumer_lag"
	samplerUpMetricName = "surgecart_sampler_up"
)

// fakeLagSampler 是可编程的 Lag 采样替身，同时统计 Fetch 与 Close 次数。
type fakeLagSampler struct {
	probe *runtimeProbe
}

func (s *fakeLagSampler) Fetch(ctx context.Context) ([]kafkamq.PartitionLag, error) {
	call := int(s.probe.lagFetchCalls.Add(1))
	select {
	case s.probe.lagEntered <- struct{}{}:
	default:
	}
	s.probe.mu.Lock()
	behavior := s.probe.lagFetchBehavior
	s.probe.mu.Unlock()
	if behavior != nil {
		return behavior(ctx, call)
	}
	return nil, nil
}

func (s *fakeLagSampler) Close() error {
	s.probe.lagCloseCalls.Add(1)
	return nil
}

// gatherSeries 按指标名 + 标签精确匹配一条样本值。
func gatherSeries(t *testing.T, name string, want map[string]string) (float64, bool) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			got := make(map[string]string, len(metric.GetLabel()))
			for _, label := range metric.GetLabel() {
				got[label.GetName()] = label.GetValue()
			}
			match := true
			for k, v := range want {
				if got[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			switch {
			case metric.GetGauge() != nil:
				return metric.GetGauge().GetValue(), true
			case metric.GetCounter() != nil:
				return metric.GetCounter().GetValue(), true
			}
		}
	}
	return 0, false
}

// kafkaLagGauge 读取指定分区当前写入的 Lag 值。
func kafkaLagGauge(t *testing.T, group, topic string, partition int32) (float64, bool) {
	t.Helper()
	return gatherSeries(t, kafkaLagMetricName, map[string]string{
		"consumer_group": group,
		"topic":          topic,
		"partition":      strconv.Itoa(int(partition)),
	})
}

// samplerUpGauge 读取指定采样器的最近一次采样状态。
func samplerUpGauge(t *testing.T, sampler string) (float64, bool) {
	t.Helper()
	return gatherSeries(t, samplerUpMetricName, map[string]string{"sampler": sampler})
}

// TestFlashSaleRuntimeKafkaLagSampler 覆盖 Lag 采样的完整语义：
//   - kafka 模式由后台采样写入分区级 Lag（不依赖 /metrics 抓取）；
//   - 采样失败只把 sampler_up 置 0，业务指标保留上一次有效值；
//   - 采样恢复后 sampler_up 回到 1，并写入新值；
//   - 分区变化（消失）后清理陈旧序列；
//   - Shutdown 后采样循环退出、独立客户端被关闭。
func TestFlashSaleRuntimeKafkaLagSampler(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	probe := newRuntimeProbe()
	const (
		group     = "surgecart-test-group"
		topic     = "surgecart.test.flashsale.requested.v1"
		partition = int32(901)
	)

	var phase int
	probe.lagFetchBehavior = func(context.Context, int) ([]kafkamq.PartitionLag, error) {
		probe.mu.Lock()
		current := phase
		probe.mu.Unlock()
		switch current {
		case 0:
			return []kafkamq.PartitionLag{{Partition: partition, Lag: 42}}, nil
		case 1:
			return nil, errors.New("kafka: broker unavailable")
		default:
			return []kafkamq.PartitionLag{{Partition: partition, Lag: 7}}, nil
		}
	}

	rt, err := startFlashSaleRuntime(context.Background(), newProbeDeps(testRuntimeConfig(flashSaleModeKafka, true), probe))
	if err != nil {
		t.Fatalf("kafka 模式启动失败: %v", err)
	}

	waitFor(t, 2*time.Second, "首次采样写入 Lag", func() bool {
		v, ok := kafkaLagGauge(t, group, topic, partition)
		return ok && v == 42
	})
	waitFor(t, 2*time.Second, "采样成功后 sampler_up=1", func() bool {
		v, ok := samplerUpGauge(t, metrics.SamplerKafkaLag)
		return ok && v == 1
	})

	// 采样失败：指标保留 42，状态变为 0。
	probe.mu.Lock()
	phase = 1
	probe.mu.Unlock()
	waitFor(t, 2*time.Second, "采样失败后 sampler_up=0", func() bool {
		v, ok := samplerUpGauge(t, metrics.SamplerKafkaLag)
		return ok && v == 0
	})
	if v, ok := kafkaLagGauge(t, group, topic, partition); !ok || v != 42 {
		t.Fatalf("采样失败后 Lag=(%v,%v)，必须保留上一次有效值 42", v, ok)
	}

	// 采样恢复：状态回到 1，值更新。
	probe.mu.Lock()
	phase = 2
	probe.mu.Unlock()
	waitFor(t, 2*time.Second, "采样恢复后写入新值", func() bool {
		v, ok := kafkaLagGauge(t, group, topic, partition)
		return ok && v == 7
	})
	waitFor(t, 2*time.Second, "采样恢复后 sampler_up=1", func() bool {
		v, ok := samplerUpGauge(t, metrics.SamplerKafkaLag)
		return ok && v == 1
	})

	if err := rt.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if closes := probe.lagCloseCalls.Load(); closes != 1 {
		t.Fatalf("Shutdown 必须关闭独立采样客户端，实际 %d 次", closes)
	}
	stopped := probe.lagFetchCalls.Load()
	time.Sleep(50 * time.Millisecond)
	if after := probe.lagFetchCalls.Load(); after != stopped {
		t.Fatalf("Shutdown 后仍在采样：%d → %d（采样循环未纳入生命周期）", stopped, after)
	}
}

// TestFlashSaleRuntimeKafkaLagSamplerPrunesStalePartitions 覆盖「分区数量变化」：
// 整轮采样成功后，消失的分区序列必须被删除，避免陈旧 Lag 永久残留。
func TestFlashSaleRuntimeKafkaLagSamplerPrunesStalePartitions(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	probe := newRuntimeProbe()
	const (
		group = "surgecart-test-group"
		topic = "surgecart.test.flashsale.requested.v1"
	)

	var twoPartitions = true
	probe.lagFetchBehavior = func(context.Context, int) ([]kafkamq.PartitionLag, error) {
		probe.mu.Lock()
		full := twoPartitions
		probe.mu.Unlock()
		if full {
			return []kafkamq.PartitionLag{
				{Partition: 910, Lag: 3},
				{Partition: 911, Lag: 9},
			}, nil
		}
		return []kafkamq.PartitionLag{{Partition: 910, Lag: 0}}, nil
	}

	rt, err := startFlashSaleRuntime(context.Background(), newProbeDeps(testRuntimeConfig(flashSaleModeKafka, true), probe))
	if err != nil {
		t.Fatalf("kafka 模式启动失败: %v", err)
	}
	defer func() { _ = rt.Shutdown() }()

	waitFor(t, 2*time.Second, "两个分区都被采样", func() bool {
		_, ok0 := kafkaLagGauge(t, group, topic, 910)
		v1, ok1 := kafkaLagGauge(t, group, topic, 911)
		return ok0 && ok1 && v1 == 9
	})

	probe.mu.Lock()
	twoPartitions = false
	probe.mu.Unlock()
	waitFor(t, 2*time.Second, "消失分区的序列被删除", func() bool {
		_, ok := kafkaLagGauge(t, group, topic, 911)
		return !ok
	})
	if v, ok := kafkaLagGauge(t, group, topic, 910); !ok || v != 0 {
		t.Fatalf("保留分区的 Lag=(%v,%v) want (true,0)", v, ok)
	}
}

// TestFlashSaleRuntimeKafkaLagSamplerInitFailureSkipped 覆盖「初始化失败如何处理」：
// 采样客户端构造失败只跳过该指标，秒杀链路必须照常启动（监控故障不得导致服务不可用）。
func TestFlashSaleRuntimeKafkaLagSamplerInitFailureSkipped(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	probe := newRuntimeProbe()
	probe.lagReaderErr = errors.New("kgo: invalid seed broker")

	rt, err := startFlashSaleRuntime(context.Background(), newProbeDeps(testRuntimeConfig(flashSaleModeKafka, true), probe))
	if err != nil {
		t.Fatalf("Lag 采样器构造失败不得导致启动失败: %v", err)
	}
	defer func() { _ = rt.Shutdown() }()

	// 业务组件（发布循环、消费循环）必须照常运行。
	waitFor(t, 2*time.Second, "发布循环仍运行", func() bool {
		select {
		case <-probe.outboxEntered:
			return true
		default:
			return false
		}
	})
	waitFor(t, 2*time.Second, "消费循环仍运行", func() bool {
		select {
		case <-probe.consumerStarted:
			return true
		default:
			return false
		}
	})
	if calls := probe.lagFetchCalls.Load(); calls != 0 {
		t.Fatalf("构造失败后不得进入采样循环，实际 Fetch %d 次", calls)
	}
}

// TestFlashSaleRuntimeKafkaLagSamplingDisabledWithMetrics 覆盖 FLASH_SALE_METRICS_ENABLED：
// 指标关闭时不创建采样客户端、不发起任何 Kafka 管理请求。
func TestFlashSaleRuntimeKafkaLagSamplingDisabledWithMetrics(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "false")
	probe := newRuntimeProbe()

	rt, err := startFlashSaleRuntime(context.Background(), newProbeDeps(testRuntimeConfig(flashSaleModeKafka, true), probe))
	if err != nil {
		t.Fatalf("kafka 模式启动失败: %v", err)
	}
	defer func() { _ = rt.Shutdown() }()

	time.Sleep(60 * time.Millisecond)
	if calls := probe.lagFetchCalls.Load(); calls != 0 {
		t.Fatalf("指标关闭时不得采样 Kafka Lag，实际 %d 次", calls)
	}
	if closes := probe.lagCloseCalls.Load(); closes != 0 {
		t.Fatalf("指标关闭时不应创建采样客户端，实际关闭 %d 次", closes)
	}
}

// TestFlashSaleRuntimeDBScannerModeSkipsKafkaLagSampling 覆盖模式判断：
// db_scanner 模式没有 Kafka Consumer，不得采集（也不得导出为 0 的假 Lag）。
func TestFlashSaleRuntimeDBScannerModeSkipsKafkaLagSampling(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	probe := newRuntimeProbe()
	cfg := testRuntimeConfig(flashSaleModeDBScanner, false)
	cfg.Kafka = kafkamq.Config{Enabled: false}

	rt, err := startFlashSaleRuntime(context.Background(), newProbeDeps(cfg, probe))
	if err != nil {
		t.Fatalf("db_scanner 启动失败: %v", err)
	}
	defer func() { _ = rt.Shutdown() }()

	time.Sleep(50 * time.Millisecond)
	if calls := probe.lagFetchCalls.Load(); calls != 0 {
		t.Fatalf("db_scanner 模式不得采样 Kafka Lag，实际 %d 次", calls)
	}
}
