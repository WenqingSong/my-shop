package worker

// Kafka Consumer Lag 采样循环（T4-02）。
//
// 调度约束与 Outbox 积压采样一致：/metrics 抓取路径绝不发起 Kafka 请求
// （抓取频率由 Prometheus 决定，且管理请求可能因 Broker 不可达而长时间阻塞），
// 由独立 goroutine 周期性采样一次，把结果写入进程内 Gauge。
//
// 周期 10s（小于抓取间隔 15s，保证每次抓取都能看到较新的值）、单次预算 5s
// （小于周期，慢请求不会堆积）。均为代码常量，不新增配置项。
//
// 失败语义（最重要）：
//   - 采样失败只写 sampler_up=0，业务指标（lag）保留上一次有效值，绝不写 0；
//   - 部分分区失败时更新成功的分区，失败分区保留上一次值；
//   - 只有整轮完全成功时才清理「已消失分区」的序列（部分失败时无法区分
//     「分区消失」与「分区读取失败」，此时删除会丢掉仍然有效的观测值）。

import (
	"context"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	kafkamq "cnb.cool/go-cloud-devops/my-shop/internal/mq/kafka"
)

const (
	// kafkaLagSampleInterval 是 Consumer Lag 的采样周期。
	kafkaLagSampleInterval = 10 * time.Second
	// kafkaLagSampleTimeout 是单次采样的请求预算（必须小于采样周期）。
	kafkaLagSampleTimeout = 5 * time.Second
)

// kafkaLagReader 是 Lag 采样依赖的最小能力（由 kafkamq.LagReader 实现）。
// 抽成接口让采样器可以用协议级替身在无 Broker 环境下验证失败语义。
type kafkaLagReader interface {
	Fetch(ctx context.Context) ([]kafkamq.PartitionLag, error)
}

// kafkaLagSampler 是「可采样 + 可关闭」的完整能力：采样器关闭时必须释放独立 Kafka 客户端。
type kafkaLagSampler interface {
	kafkaLagReader
	Close() error
}

// runKafkaLagSampler 周期性采样并写入 Gauge，直到 ctx 取消（Runtime Shutdown）。
func (r *FlashSaleRuntime) runKafkaLagSampler(ctx context.Context, reader kafkaLagReader) {
	ticker := time.NewTicker(r.kafkaLagSampleInterval)
	defer ticker.Stop()
	defer logx.Info(context.Background(), "flashsale_kafka_lag_sampler_exited", "Kafka Consumer Lag 采样循环已退出", nil)

	// 先采样一次：让指标在启动后尽快具备确定值，而不是等一个完整周期。
	r.sampleKafkaLag(ctx, reader)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sampleKafkaLag(ctx, reader)
		}
	}
}

// sampleKafkaLag 执行一次有界采样，并按失败语义更新指标。
func (r *FlashSaleRuntime) sampleKafkaLag(ctx context.Context, reader kafkaLagReader) {
	// 指标关闭时不发起任何 Kafka 管理请求。
	if !metrics.Enabled(ctx) {
		return
	}
	sampleCtx, cancel := context.WithTimeout(ctx, r.kafkaLagSampleTimeout)
	lags, err := reader.Fetch(sampleCtx)
	cancel()
	if ctx.Err() != nil {
		// 关闭期间取消导致的失败不是故障，不刷错误日志、不改写指标。
		return
	}

	if len(lags) > 0 {
		for _, lag := range lags {
			metrics.SetKafkaConsumerLag(r.kafkaConfig.ConsumerGroup, r.kafkaConfig.Topic, lag.Partition, lag.Lag)
		}
		// 整轮成功才清理消失的分区：部分失败时缺失的分区可能只是读取失败。
		if err == nil {
			r.pruneKafkaLagPartitions(lags)
		}
	}
	metrics.SetSamplerUp(metrics.SamplerKafkaLag, err == nil)

	if err != nil {
		logx.Warning(ctx, "flashsale_kafka_lag_sample_failed", "Kafka Consumer Lag 采样未完整成功，缺失分区保留上一次有效值", logx.Fields{
			"consumer_group":     r.kafkaConfig.ConsumerGroup,
			"topic":              r.kafkaConfig.Topic,
			"sampled_partitions": len(lags),
			"error":              err.Error(),
		})
	}
}

// pruneKafkaLagPartitions 删除本轮已不存在的分区序列（Topic 分区收缩/重建）。
// 只在整轮采样完全成功时调用；r.lagPartitions 只由采样 goroutine 读写，无需加锁。
func (r *FlashSaleRuntime) pruneKafkaLagPartitions(lags []kafkamq.PartitionLag) {
	current := make(map[int32]struct{}, len(lags))
	for _, lag := range lags {
		current[lag.Partition] = struct{}{}
	}
	for partition := range r.lagPartitions {
		if _, ok := current[partition]; ok {
			continue
		}
		metrics.DeleteKafkaConsumerLag(r.kafkaConfig.ConsumerGroup, r.kafkaConfig.Topic, partition)
	}
	r.lagPartitions = current
}
