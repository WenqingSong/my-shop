package kafka

// T4-02：Consumer Lag 的真实 Kafka 集成验证。
//
// 未提供 Broker 时自动跳过（不得用 Mock 冒充真实 Kafka 验证）：
//
//	FLASH_SALE_KAFKA_TEST_BROKER=127.0.0.1:29092 go test -run TestLagReaderRealKafka -v ./internal/mq/kafka/
//
// 使用一次性 Topic 与一次性消费组（时间戳命名），不触碰任何既有业务 Topic/消费组；
// 验证的是「真实 Broker 上的已提交位移与 Log End/Start Offset 之差」是否等于本采样器
// 计算出的 Lag，而不是 Mock 的调用顺序。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// realKafkaBroker 读取真实 Broker 地址；未设置时返回空串（调用方 t.Skip）。
func realKafkaBroker() string {
	return strings.TrimSpace(os.Getenv("FLASH_SALE_KAFKA_TEST_BROKER"))
}

// createTestTopic 显式创建一次性 Topic（本地 Broker 关闭了自动建 Topic）。
func createTestTopic(t *testing.T, broker, topic string, partitions int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ClientID("t4-02-lag-admin"))
	if err != nil {
		t.Fatalf("创建管理客户端失败: %v", err)
	}
	defer client.Close()

	req := kmsg.NewPtrCreateTopicsRequest()
	req.TimeoutMillis = 10000
	topicReq := kmsg.NewCreateTopicsRequestTopic()
	topicReq.Topic = topic
	topicReq.NumPartitions = partitions
	topicReq.ReplicationFactor = 1
	req.Topics = append(req.Topics, topicReq)

	resp, err := req.RequestWith(ctx, client)
	if err != nil {
		t.Fatalf("创建 Topic %s 失败: %v", topic, err)
	}
	for _, topicResp := range resp.Topics {
		if topicResp.ErrorCode == 0 {
			continue
		}
		if code := kerr.ErrorForCode(topicResp.ErrorCode); code != nil && !errors.Is(code, kerr.TopicAlreadyExists) {
			t.Fatalf("创建 Topic %s 失败: %v", topic, code)
		}
	}
}

// produceTestRecords 同步投递 count 条消息到指定分区，等待 Broker 确认（保证 LEO 已推进）。
func produceTestRecords(t *testing.T, broker, topic string, partition int32, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ClientID("t4-02-lag-producer"))
	if err != nil {
		t.Fatalf("创建 Producer 失败: %v", err)
	}
	defer client.Close()

	for i := 0; i < count; i++ {
		record := &kgo.Record{
			Topic:     topic,
			Partition: partition,
			Value:     []byte(fmt.Sprintf("lag-sample-%d", i)),
		}
		results := client.ProduceSync(ctx, record)
		if err := results.FirstErr(); err != nil {
			t.Fatalf("投递第 %d 条消息失败: %v", i+1, err)
		}
	}
}

// commitTestOffsets 用「只存位移」的方式提交消费组位移
// （Generation=-1、MemberID 为空：不参与 rebalance，仅写 __consumer_offsets）。
func commitTestOffsets(t *testing.T, broker, group, topic string, offsets map[int32]int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ClientID("t4-02-lag-offset-committer"))
	if err != nil {
		t.Fatalf("创建位移提交客户端失败: %v", err)
	}
	defer client.Close()

	req := kmsg.NewPtrOffsetCommitRequest()
	req.Group = group
	req.Generation = -1
	topicReq := kmsg.NewOffsetCommitRequestTopic()
	topicReq.Topic = topic
	for partition, offset := range offsets {
		partitionReq := kmsg.NewOffsetCommitRequestTopicPartition()
		partitionReq.Partition = partition
		partitionReq.Offset = offset
		topicReq.Partitions = append(topicReq.Partitions, partitionReq)
	}
	req.Topics = append(req.Topics, topicReq)

	if _, err := req.RequestWith(ctx, client); err != nil {
		t.Fatalf("提交消费组 %s 位移失败: %v", group, err)
	}
}

// TestLagReaderRealKafka 在真实 Broker 上验证 Lag 采样的三段语义：
//  1. 消费组未提交位移 → Lag = LEO - LogStart（不是 0）；
//  2. 提交 offset=4 后 → Lag = LEO - 4；
//  3. 追加消息后 Lag 随之增大；提交到 LEO 后 Lag = 0。
func TestLagReaderRealKafka(t *testing.T) {
	broker := realKafkaBroker()
	if broker == "" {
		t.Skip("未设置 FLASH_SALE_KAFKA_TEST_BROKER，跳过真实 Kafka Lag 验证")
	}

	const partition = int32(0)
	suffix := time.Now().UnixNano()
	topic := fmt.Sprintf("surgecart.test.t4-02.lag.%d", suffix)
	group := fmt.Sprintf("surgecart-test-t4-02-lag-%d", suffix)
	createTestTopic(t, broker, topic, 1)
	produceTestRecords(t, broker, topic, partition, 10)

	reader, err := NewLagReader(Config{
		Enabled:       true,
		Brokers:       []string{broker},
		Topic:         topic,
		DLQTopic:      topic + ".dlq",
		ConsumerGroup: group,
	})
	if err != nil {
		t.Fatalf("创建 Lag 采样器失败: %v", err)
	}
	defer func() { _ = reader.Close() }()

	fetch := func() PartitionLag {
		t.Helper()
		// 刚启动的 Broker 可能在加载 __consumer_offsets 协调器时短暂返回
		// COORDINATOR_NOT_AVAILABLE；生产采样器按周期重试，这里同样允许有限重试，
		// 否则会把「协调器加载中」误判为计算错误。
		deadline := time.Now().Add(30 * time.Second)
		var lastErr error
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			samples, err := reader.Fetch(ctx)
			cancel()
			if err == nil {
				for _, sample := range samples {
					if sample.Partition == partition {
						return sample
					}
				}
				t.Fatalf("采样结果缺少分区 %d: %+v", partition, samples)
			}
			lastErr = err
			if time.Now().After(deadline) {
				t.Fatalf("采样 Lag 失败: %v", lastErr)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	// 1) 未提交位移：初始积压 = LEO - LogStart = 10。
	first := fetch()
	if first.HasCommit {
		t.Fatalf("消费组 %s 从未提交位移，HasCommit 必须为 false：%+v", group, first)
	}
	if first.LogEndOffset != 10 || first.Lag != 10 {
		t.Fatalf("未提交位移时 LEO=%d Lag=%d，期望 10/10：%+v", first.LogEndOffset, first.Lag, first)
	}

	// 2) 提交 offset=4：Lag = 10 - 4 = 6。
	commitTestOffsets(t, broker, group, topic, map[int32]int64{partition: 4})
	second := fetch()
	if !second.HasCommit || second.CommittedOffset != 4 || second.Lag != 6 {
		t.Fatalf("提交 4 后应得到 committed=4 lag=6，实际 %+v", second)
	}

	// 3) 再投 5 条：LEO=15，Lag = 15 - 4 = 11。
	produceTestRecords(t, broker, topic, partition, 5)
	third := fetch()
	if third.LogEndOffset != 15 || third.Lag != 11 {
		t.Fatalf("追加 5 条后应得到 LEO=15 lag=11，实际 %+v", third)
	}

	// 4) 提交到 LEO：Lag = 0。
	commitTestOffsets(t, broker, group, topic, map[int32]int64{partition: 15})
	fourth := fetch()
	if fourth.Lag != 0 {
		t.Fatalf("追平后 Lag 必须为 0，实际 %+v", fourth)
	}
}
