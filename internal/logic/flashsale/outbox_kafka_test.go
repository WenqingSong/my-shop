package flashsale

// 秒杀 B2 真实 Kafka 集成测试。
//
// 与 outbox_test.go 的 fake Publisher 不同：本文件使用真实 franz-go Producer 与真实 Broker，
// 验证 Key、Header、Value、Partition 与「Broker 确认后才标记」的完整链路。
// 未提供 Broker 时自动跳过（不得用 Mock 冒充真实 Kafka 集成测试）：
//
//	FLASH_SALE_KAFKA_TEST_BROKER=127.0.0.1:29092 \
//	DATABASE_DEFAULT_PASS=root go test -run TestOutboxRealKafka -v ./internal/logic/flashsale/
//
// 需要 MySQL/Redis 就绪（复用 setupConsumeTest 环境），并需要 Kafka Broker 可连。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"

	kafkamq "cnb.cool/go-cloud-devops/my-shop/internal/mq/kafka"
)

// kafkaTestBroker 返回测试用 Broker 地址；未配置时跳过。
func kafkaTestBroker(t *testing.T) string {
	t.Helper()
	broker := os.Getenv("FLASH_SALE_KAFKA_TEST_BROKER")
	if broker == "" {
		t.Skip("未设置 FLASH_SALE_KAFKA_TEST_BROKER，跳过真实 Kafka 集成测试")
	}
	return broker
}

// kafkaCreateTopic 显式创建 Topic（本地 Broker 关闭了自动建 Topic），已存在视为成功。
func kafkaCreateTopic(t *testing.T, broker, topic string, partitions int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ClientID("outbox-test-admin"))
	if err != nil {
		t.Fatalf("create admin client: %v", err)
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
		t.Fatalf("create topic %s: %v", topic, err)
	}
	for _, tr := range resp.Topics {
		if tr.ErrorCode == 0 {
			continue
		}
		if err := kerr.ErrorForCode(tr.ErrorCode); err != nil && !errors.Is(err, kerr.TopicAlreadyExists) {
			t.Fatalf("create topic %s: %v", topic, err)
		}
	}
}

// kafkaFetchUntil 以「无消费组、从头读取」的方式收集消息，直到数量满足或超时。
func kafkaFetchUntil(t *testing.T, broker, topic string, want int) []*kgo.Record {
	t.Helper()
	client, err := kgo.NewClient(
		kgo.SeedBrokers(broker),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.ClientID("outbox-test-consumer"),
	)
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out []*kgo.Record
	for len(out) < want {
		fetches := client.PollFetches(ctx)
		if ctx.Err() != nil {
			t.Fatalf("等待 Kafka 消息超时：已收到 %d 条，期望 %d 条", len(out), want)
		}
		fetches.EachRecord(func(r *kgo.Record) { out = append(out, r) })
	}
	return out
}

// TestOutboxRealKafkaPublishAndRecovery 覆盖 B2-4 的真实 Broker 链路：
// 发布 → Broker 确认 → 标记 published_at → 消费侧校验 Key/Header/Value/Partition；
// 再模拟「结果未知/标记丢失」后重投，验证 event_id 稳定（at-least-once 允许重复）。
func TestOutboxRealKafkaPublishAndRecovery(t *testing.T) {
	broker := kafkaTestBroker(t)
	setupOutboxTest(t)
	ctx := context.Background()

	topic := fmt.Sprintf("surgecart.test.flashsale.requested.v1.%d", time.Now().UnixNano())
	kafkaCreateTopic(t, broker, topic, 3)

	producer, err := kafkamq.NewProducer(kafkamq.Config{
		Enabled:       true,
		Brokers:       []string{broker},
		Topic:         topic,
		DLQTopic:      topic + ".dlq",
		ConsumerGroup: "surgecart-test-outbox",
	})
	if err != nil {
		t.Fatalf("new producer: %v", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := producer.Close(closeCtx); err != nil {
			t.Logf("close producer: %v", err)
		}
	}()

	opts := outboxTestOptions()
	pub := newTestOutboxPublisher(t, producer, opts)
	id := outboxInsertRequest(t, "real-kafka-1", g.Map{"user_id": 812001, "activity_id": 820001, "sku_id": 830001})

	stats, err := pub.PublishBatch(ctx, 10)
	if err != nil {
		t.Fatalf("publish batch: %v", err)
	}
	if stats.Published != 1 {
		t.Fatalf("stats=%+v want 1 published", stats)
	}

	row := outboxLoad(t, id)
	if row.PublishedAt == nil {
		t.Fatalf("published_at should be set only after broker ack")
	}
	if row.PublishedPartition == nil || row.PublishedOffset == nil {
		t.Fatalf("broker-assigned partition/offset should be recorded: %+v", row)
	}
	if row.PublishClaimToken != nil || row.PublishLeaseUntil != nil {
		t.Fatalf("lease should be cleared after ack: %+v", row)
	}

	records := kafkaFetchUntil(t, broker, topic, 1)
	if len(records) != 1 {
		t.Fatalf("consumed %d records want 1", len(records))
	}
	rec := records[0]
	if got, want := string(rec.Key), "820001:830001"; got != want {
		t.Fatalf("kafka key=%q want %q", got, want)
	}
	if int(rec.Partition) != *row.PublishedPartition || rec.Offset != *row.PublishedOffset {
		t.Fatalf("consumed partition/offset=%d/%d, DB recorded %d/%d",
			rec.Partition, rec.Offset, *row.PublishedPartition, *row.PublishedOffset)
	}
	event, err := DecodeRequestedEvent(rec.Value)
	if err != nil {
		t.Fatalf("decode kafka value: %v", err)
	}
	if event.RequestID != id || event.ActivityID != 820001 || event.SkuID != 830001 || event.UserID != 812001 {
		t.Fatalf("event identity mismatch: %+v", event)
	}
	if event.EventID != RequestedEventID(id) {
		t.Fatalf("event_id=%q want %q", event.EventID, RequestedEventID(id))
	}
	if !event.OccurredAt.Equal(row.CreatedAt.Time) {
		t.Fatalf("occurred_at=%s want request created_at=%s", event.OccurredAt, row.CreatedAt.Time)
	}
	for name, want := range map[string]string{
		"event_id":       RequestedEventID(id),
		"event_type":     RequestedEventType,
		"schema_version": "1",
	} {
		var found bool
		for _, h := range rec.Headers {
			if h.Key == name {
				found = true
				if string(h.Value) != want {
					t.Fatalf("header %s=%q want %q", name, h.Value, want)
				}
			}
		}
		if !found {
			t.Fatalf("missing kafka header %s", name)
		}
	}

	// 模拟发布结果未知（Broker 已收到但标记丢失）：清空 published_at 后重投。
	if _, err := dbExec(ctx, "UPDATE flash_sale_order_requests SET published_at = NULL, published_offset = NULL, published_partition = NULL WHERE id = ?", id); err != nil {
		t.Fatalf("reset publish mark: %v", err)
	}
	stats, err = pub.PublishBatch(ctx, 10)
	if err != nil {
		t.Fatalf("republish: %v", err)
	}
	if stats.Published != 1 {
		t.Fatalf("republish stats=%+v", stats)
	}

	records = kafkaFetchUntil(t, broker, topic, 2)
	if len(records) != 2 {
		t.Fatalf("expected duplicate delivery (2 records), got %d", len(records))
	}
	first, err := DecodeRequestedEvent(records[0].Value)
	if err != nil {
		t.Fatalf("decode first: %v", err)
	}
	second, err := DecodeRequestedEvent(records[1].Value)
	if err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if first.EventID != second.EventID {
		t.Fatalf("event_id must stay stable across republish: %q vs %q", first.EventID, second.EventID)
	}
	if !first.OccurredAt.Equal(second.OccurredAt) {
		t.Fatalf("occurred_at must stay stable across republish")
	}
	if string(records[0].Key) != string(records[1].Key) {
		t.Fatalf("message key must stay stable across republish")
	}

	// 再次确认 DB 与 Broker 一致（重投后标记被重新写入）。
	row = outboxLoad(t, id)
	if row.PublishedAt == nil || row.PublishedOffset == nil {
		t.Fatalf("republished record should be marked again: %+v", row)
	}
}

// dbExec 是测试内的小工具：执行一条 SQL。
func dbExec(ctx context.Context, sqlText string, args ...any) (int64, error) {
	result, err := g.DB().Exec(ctx, sqlText, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
