package flashsale

// 秒杀 B3 真实 Kafka 集成测试。
//
// 这些用例使用真实 Broker + 真实 Consumer Group + 真实 MySQL/Redis，验证的是
// 「Kafka 消息真正驱动秒杀订单」这条链路本身，而不是 Mock 的调用顺序：
//   - Consumer Group 正常消费与位移提交；
//   - 重复消息；
//   - MySQL 成功但 Offset 提交失败后的重新消费（不重复落单）；
//   - 死信正常投递；
//   - 死信投递失败时原位移不提交；
//   - Rebalance 期间不丢消息；
//   - Consumer 关闭与重启恢复。
//
// 未提供 Broker 时自动跳过（不得用 Mock 冒充真实 Kafka 集成测试）：
//
//	FLASH_SALE_KAFKA_TEST_BROKER=127.0.0.1:29092 \
//	DATABASE_DEFAULT_PASS=root go test -run TestConsumerRealKafka -v ./internal/logic/flashsale/
//
// 需要 MySQL/Redis 就绪（复用 setupConsumeTest 环境）与 Kafka Broker 可连。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"

	kafkamq "cnb.cool/go-cloud-devops/my-shop/internal/mq/kafka"

	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	"cnb.cool/go-cloud-devops/my-shop/internal/mq"
)

// newB3KafkaTopic 创建一个唯一的测试主题（本地 Broker 关闭了自动建 Topic）。
func newB3KafkaTopic(t *testing.T, broker, prefix string, partitions int32) string {
	t.Helper()
	topic := fmt.Sprintf("surgecart.test.b3.%s.%d", prefix, time.Now().UnixNano())
	kafkaCreateTopic(t, broker, topic, partitions)
	return topic
}

// kafkaGroupOffsets 读取消费组在指定主题上的已提交位移（未提交的分区返回 -1）。
func kafkaGroupOffsets(t *testing.T, broker, group, topic string, partitions int32) map[int32]int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ClientID("b3-test-offset-reader"))
	if err != nil {
		t.Fatalf("create admin client: %v", err)
	}
	defer client.Close()

	req := kmsg.NewPtrOffsetFetchRequest()
	req.Version = 7
	req.Group = group
	topicReq := kmsg.NewOffsetFetchRequestTopic()
	topicReq.Topic = topic
	for p := int32(0); p < partitions; p++ {
		topicReq.Partitions = append(topicReq.Partitions, p)
	}
	req.Topics = append(req.Topics, topicReq)

	resp, err := req.RequestWith(ctx, client)
	if err != nil {
		t.Fatalf("offset fetch: %v", err)
	}
	out := make(map[int32]int64, partitions)
	for _, tp := range resp.Topics {
		if tp.Topic != topic {
			continue
		}
		for _, part := range tp.Partitions {
			if part.ErrorCode != 0 {
				t.Fatalf("offset fetch partition %d: error code %d", part.Partition, part.ErrorCode)
			}
			out[part.Partition] = part.Offset
		}
	}
	return out
}

// kafkaGroupMemberCount 读取消费组当前成员数，用于确认 Rebalance 真的发生过。
func kafkaGroupMemberCount(t *testing.T, broker, group string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ClientID("b3-test-group-reader"))
	if err != nil {
		t.Fatalf("create admin client: %v", err)
	}
	defer client.Close()

	req := kmsg.NewPtrDescribeGroupsRequest()
	req.Version = 4
	req.Groups = append(req.Groups, group)
	resp, err := req.RequestWith(ctx, client)
	if err != nil {
		t.Fatalf("describe group: %v", err)
	}
	for _, g := range resp.Groups {
		if g.Group == group {
			return len(g.Members)
		}
	}
	return 0
}

// kafkaPublishRequestedEvents 用真实 Producer 把事件写到指定主题（Key 与 Outbox 一致）。
func kafkaPublishRequestedEvents(t *testing.T, broker, topic string, events ...*RequestedEvent) {
	t.Helper()
	producer, err := kafkamq.NewProducer(kafkamq.Config{
		Enabled:       true,
		Brokers:       []string{broker},
		Topic:         topic,
		DLQTopic:      topic + ".dlq",
		ConsumerGroup: "surgecart-test-b3-producer",
	})
	if err != nil {
		t.Fatalf("new producer: %v", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := producer.Close(closeCtx); err != nil {
			t.Logf("close producer: %v", err)
		}
	}()

	messages := make([]mq.Message, 0, len(events))
	for _, event := range events {
		msg := testMessage(t, event, 0, 0)
		msg.Topic = topic
		messages = append(messages, msg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results, err := producer.Publish(ctx, messages...)
	if err != nil && results == nil {
		t.Fatalf("publish: %v", err)
	}
	if len(results) != len(messages) {
		t.Fatalf("publish results=%d want %d (err=%v)", len(results), len(messages), err)
	}
	for i, res := range results {
		if res.Err != nil {
			t.Fatalf("publish message %d: %v", i, res.Err)
		}
	}
}

// newB3RealConsumer 构造真实 Kafka 客户端 + 业务消费组件。
func newB3RealConsumer(t *testing.T, broker, topic, group, dlqTopic string, opts ConsumerOptions) (*RequestedEventConsumer, *kafkamq.Consumer) {
	t.Helper()
	client, err := kafkamq.NewConsumer(kafkamq.Config{
		Enabled:       true,
		Brokers:       []string{broker},
		Topic:         topic,
		DLQTopic:      dlqTopic,
		ConsumerGroup: group,
	})
	if err != nil {
		t.Fatalf("new consumer: %v", err)
	}
	opts.DLQTopic = dlqTopic
	c, err := NewRequestedEventConsumer(client, newB3DLQPublisher(t, broker, topic, dlqTopic), New(), opts)
	if err != nil {
		t.Fatalf("new requested consumer: %v", err)
	}
	return c, client
}

// newB3DLQPublisher 返回真实的死信发布器（Producer 默认 Topic 就是死信主题）。
func newB3DLQPublisher(t *testing.T, broker, topic, dlqTopic string) mq.Publisher {
	t.Helper()
	producer, err := kafkamq.NewProducer(kafkamq.Config{
		Enabled:       true,
		Brokers:       []string{broker},
		Topic:         topic,
		DLQTopic:      dlqTopic,
		ConsumerGroup: "surgecart-test-b3-dlq",
	})
	if err != nil {
		t.Fatalf("new dlq producer: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := producer.Close(closeCtx); err != nil {
			t.Logf("close dlq producer: %v", err)
		}
	})
	return producer
}

// startRealConsumer 启动消费循环，返回 stop（取消 + 等待退出 + 关闭客户端）。
func startRealConsumer(t *testing.T, c *RequestedEventConsumer, client *kafkamq.Consumer) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Errorf("消费循环未在关闭超时内退出")
			}
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer closeCancel()
			if err := client.Close(closeCtx); err != nil {
				t.Logf("close consumer: %v", err)
			}
		})
	}
}

// waitB3 轮询等待条件成立，超时即失败（不使用固定 sleep 猜测消费进度）。
func waitB3(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", what)
}

// b3RequestStatus 读取请求状态（不存在返回 -1）。
func b3RequestStatus(t *testing.T, requestID int64) int {
	t.Helper()
	v, err := g.DB().Model(requestTable).Ctx(context.Background()).
		Fields("status").Where("id", requestID).Value()
	if err != nil {
		t.Fatalf("query request %d status: %v", requestID, err)
	}
	if v == nil || v.IsNil() {
		return -1
	}
	return v.Int()
}

// b3OrderCount 统计活动下的秒杀订单数。
func b3OrderCount(t *testing.T, activityID int64) int {
	t.Helper()
	n, err := g.DB().Model("flash_sale_orders").Ctx(context.Background()).Where("activity_id", activityID).Count()
	if err != nil {
		t.Fatalf("count orders: %v", err)
	}
	return n
}

// TestConsumerRealKafkaEndToEnd 覆盖 Consumer Group 正常消费 + 位移提交 + 重复消息：
// 真实 Broker 上的消息驱动 MySQL 落单，重复投递被状态闸门跳过，位移持续推进。
func TestConsumerRealKafkaEndToEnd(t *testing.T) {
	broker := kafkaTestBroker(t)
	setupConsumeTest(t)

	skuID := consumeTestSku(t, "real-kafka-b3")
	activityID := consumeTestActivity(t, skuID)
	topic := newB3KafkaTopic(t, broker, "e2e", 2)
	dlqTopic := topic + ".dlq"
	kafkaCreateTopic(t, broker, dlqTopic, 1)
	group := fmt.Sprintf("surgecart-test-b3-e2e-%d", time.Now().UnixNano())

	requestIDs := make([]int64, 0, 3)
	events := make([]*RequestedEvent, 0, 3)
	for i := 0; i < 3; i++ {
		id := consumerInsertRequest(t, int64(920000+i), activityID, skuID, fmt.Sprintf("real-kafka-%d", i), nil)
		requestIDs = append(requestIDs, id)
		events = append(events, consumerEventOf(t, id))
	}
	kafkaPublishRequestedEvents(t, broker, topic, events...)

	c, client := newB3RealConsumer(t, broker, topic, group, dlqTopic, consumerTestOptions())
	stop := startRealConsumer(t, c, client)
	defer stop()

	waitB3(t, 60*time.Second, "三条请求全部落单", func() bool {
		for _, id := range requestIDs {
			if b3RequestStatus(t, id) != requestStatusSuccess {
				return false
			}
		}
		return true
	})
	if got := b3OrderCount(t, activityID); got != 3 {
		t.Fatalf("order count=%d want 3", got)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 3 {
		t.Fatalf("sold=%d want 3", sold)
	}

	// 重复投递同一条事件：必须被状态闸门跳过，不重复落单。
	kafkaPublishRequestedEvents(t, broker, topic, events[0])
	waitB3(t, 30*time.Second, "重复消息被消费并提交", func() bool {
		offsets := kafkaGroupOffsets(t, broker, group, topic, 2)
		var total int64
		for _, off := range offsets {
			if off > 0 {
				total += off
			}
		}
		return total >= 4
	})
	if got := b3OrderCount(t, activityID); got != 3 {
		t.Fatalf("重复消息不得重复落单，order count=%d want 3", got)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 3 {
		t.Fatalf("重复消息不得重复扣库存，sold=%d want 3", sold)
	}
}

// faultCommitConsumer 包装真实客户端，让首次位移提交失败，模拟
// 「MySQL 事务已提交、Kafka 位移提交失败」这一最危险的故障窗口。
type faultCommitConsumer struct {
	RebalanceConsumer
	once   sync.Once
	failed chan struct{}
}

func (f *faultCommitConsumer) Commit(ctx context.Context, messages ...mq.Message) error {
	var fail bool
	f.once.Do(func() { fail = true })
	if fail {
		close(f.failed)
		return errors.New("simulated kafka commit failure")
	}
	return f.RebalanceConsumer.Commit(ctx, messages...)
}

// TestConsumerRealKafkaCommitFailureRedelivers 覆盖「MySQL 成功但 Offset 提交失败」：
// 消息会被重新投递，但请求已 success，第二次消费只做安全跳过，不重复建单。
func TestConsumerRealKafkaCommitFailureRedelivers(t *testing.T) {
	broker := kafkaTestBroker(t)
	setupConsumeTest(t)

	skuID := consumeTestSku(t, "real-kafka-commit")
	activityID := consumeTestActivity(t, skuID)
	topic := newB3KafkaTopic(t, broker, "commit", 1)
	dlqTopic := topic + ".dlq"
	kafkaCreateTopic(t, broker, dlqTopic, 1)
	group := fmt.Sprintf("surgecart-test-b3-commit-%d", time.Now().UnixNano())

	requestID := consumerInsertRequest(t, 930001, activityID, skuID, "real-kafka-commit", nil)
	kafkaPublishRequestedEvents(t, broker, topic, consumerEventOf(t, requestID))

	// 第一轮：真实客户端 + 首次提交失败。
	rawClient, err := kafkamq.NewConsumer(kafkamq.Config{
		Enabled: true, Brokers: []string{broker}, Topic: topic,
		DLQTopic: dlqTopic, ConsumerGroup: group,
	})
	if err != nil {
		t.Fatalf("new consumer: %v", err)
	}
	fault := &faultCommitConsumer{RebalanceConsumer: rawClient, failed: make(chan struct{})}
	opts := consumerTestOptions()
	opts.DLQTopic = dlqTopic
	firstConsumer, err := NewRequestedEventConsumer(fault, newB3DLQPublisher(t, broker, topic, dlqTopic), New(), opts)
	if err != nil {
		t.Fatalf("new requested consumer: %v", err)
	}

	runCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- firstConsumer.Run(runCtx) }()
	select {
	case err := <-runErr:
		if err == nil {
			t.Fatalf("提交失败必须让消费循环返回错误")
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("等待首次提交失败超时")
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := rawClient.Close(closeCtx); err != nil {
		t.Logf("close first consumer: %v", err)
	}
	closeCancel()

	select {
	case <-fault.failed:
	default:
		t.Fatalf("首次位移提交未触发故障注入，用例前提不成立")
	}
	if got := b3RequestStatus(t, requestID); got != requestStatusSuccess {
		t.Fatalf("MySQL 事务应已提交，status=%d want success", got)
	}
	offsets := kafkaGroupOffsets(t, broker, group, topic, 1)
	if offsets[0] > 0 {
		t.Fatalf("提交失败后位移不应推进，got %d", offsets[0])
	}

	// 第二轮：新的消费者（同组）从已提交位移重投同一条消息。
	c, client := newB3RealConsumer(t, broker, topic, group, dlqTopic, consumerTestOptions())
	stop := startRealConsumer(t, c, client)
	defer stop()

	waitB3(t, 60*time.Second, "重投消息被提交", func() bool {
		return kafkaGroupOffsets(t, broker, group, topic, 1)[0] >= 1
	})
	if got := b3OrderCount(t, activityID); got != 1 {
		t.Fatalf("重投不得重复建单，order count=%d want 1", got)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("重投不得重复扣库存，sold=%d want 1", sold)
	}
}

// TestConsumerRealKafkaDeadLetter 覆盖死信正常投递：不可处理消息进入死信主题，
// 原消息位移在死信 ACK 之后提交。
func TestConsumerRealKafkaDeadLetter(t *testing.T) {
	broker := kafkaTestBroker(t)
	setupConsumeTest(t)

	topic := newB3KafkaTopic(t, broker, "dlq", 1)
	dlqTopic := topic + ".dlq"
	kafkaCreateTopic(t, broker, dlqTopic, 1)
	group := fmt.Sprintf("surgecart-test-b3-dlq-%d", time.Now().UnixNano())

	// 直接投递一条非法消息（真实 Producer，Value 不是 JSON）。
	producer, err := kafkamq.NewProducer(kafkamq.Config{
		Enabled: true, Brokers: []string{broker}, Topic: topic,
		DLQTopic: dlqTopic, ConsumerGroup: "surgecart-test-b3-dlq-producer",
	})
	if err != nil {
		t.Fatalf("new producer: %v", err)
	}
	results, err := producer.Publish(context.Background(), mq.Message{
		Topic: topic, Key: []byte("1:1"), Value: []byte("not-json"),
	})
	if err != nil || len(results) != 1 || results[0].Err != nil {
		t.Fatalf("publish invalid message: results=%v err=%v", results, err)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := producer.Close(closeCtx); err != nil {
		t.Logf("close producer: %v", err)
	}
	closeCancel()

	c, client := newB3RealConsumer(t, broker, topic, group, dlqTopic, consumerTestOptions())
	stop := startRealConsumer(t, c, client)
	defer stop()

	records := kafkaFetchUntil(t, broker, dlqTopic, 1)
	if got := string(records[0].Value); got != "not-json" {
		t.Fatalf("死信必须原样保留载荷，got %q", got)
	}
	var reason, sourceTopic string
	for _, h := range records[0].Headers {
		switch h.Key {
		case dlqHeaderReason:
			reason = string(h.Value)
		case dlqHeaderSourceTopic:
			sourceTopic = string(h.Value)
		}
	}
	if reason != metrics.ConsumerDLQParse {
		t.Fatalf("死信 reason=%q want %q", reason, metrics.ConsumerDLQParse)
	}
	if sourceTopic != topic {
		t.Fatalf("死信应标注来源主题，got %q want %q", sourceTopic, topic)
	}
	waitB3(t, 30*time.Second, "原消息位移提交", func() bool {
		return kafkaGroupOffsets(t, broker, group, topic, 1)[0] >= 1
	})
}

// failingDLQPublisher 模拟死信 Broker 不可用。
type failingDLQPublisher struct{}

func (failingDLQPublisher) Publish(ctx context.Context, messages ...mq.Message) ([]mq.PublishResult, error) {
	return nil, errors.New("dlq broker unavailable")
}

func (failingDLQPublisher) Close(ctx context.Context) error { return nil }

// TestConsumerRealKafkaDLQFailureKeepsOffset 覆盖「死信投递失败时原位移不提交」：
// 死信不可用时消费循环停止且位移保持未提交，死信恢复后同一条消息仍能被完整处理。
func TestConsumerRealKafkaDLQFailureKeepsOffset(t *testing.T) {
	broker := kafkaTestBroker(t)
	setupConsumeTest(t)

	topic := newB3KafkaTopic(t, broker, "dlqfail", 1)
	dlqTopic := topic + ".dlq"
	kafkaCreateTopic(t, broker, dlqTopic, 1)
	group := fmt.Sprintf("surgecart-test-b3-dlqfail-%d", time.Now().UnixNano())

	producer, err := kafkamq.NewProducer(kafkamq.Config{
		Enabled: true, Brokers: []string{broker}, Topic: topic,
		DLQTopic: dlqTopic, ConsumerGroup: "surgecart-test-b3-dlqfail-producer",
	})
	if err != nil {
		t.Fatalf("new producer: %v", err)
	}
	results, err := producer.Publish(context.Background(), mq.Message{
		Topic: topic, Key: []byte("1:1"), Value: []byte("not-json"),
	})
	if err != nil || len(results) != 1 || results[0].Err != nil {
		t.Fatalf("publish invalid message: results=%v err=%v", results, err)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := producer.Close(closeCtx); err != nil {
		t.Logf("close producer: %v", err)
	}
	closeCancel()

	// 第一轮：死信发布器不可用 → 消费循环报错退出，位移不提交。
	client, err := kafkamq.NewConsumer(kafkamq.Config{
		Enabled: true, Brokers: []string{broker}, Topic: topic,
		DLQTopic: dlqTopic, ConsumerGroup: group,
	})
	if err != nil {
		t.Fatalf("new consumer: %v", err)
	}
	opts := consumerTestOptions()
	opts.DLQTopic = dlqTopic
	c, err := NewRequestedEventConsumer(client, failingDLQPublisher{}, New(), opts)
	if err != nil {
		t.Fatalf("new requested consumer: %v", err)
	}
	runCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- c.Run(runCtx) }()
	select {
	case err := <-runErr:
		if err == nil {
			t.Fatalf("死信失败必须让消费循环报错")
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("等待死信失败超时")
	}
	closeCtx2, closeCancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	if err := client.Close(closeCtx2); err != nil {
		t.Logf("close first consumer: %v", err)
	}
	closeCancel2()

	if off := kafkaGroupOffsets(t, broker, group, topic, 1)[0]; off > 0 {
		t.Fatalf("死信未确认时不得提交位移，got %d", off)
	}

	// 第二轮：死信恢复 → 同一条消息被完整处理（死信 + 位移提交）。
	c2, client2 := newB3RealConsumer(t, broker, topic, group, dlqTopic, consumerTestOptions())
	stop := startRealConsumer(t, c2, client2)
	defer stop()
	records := kafkaFetchUntil(t, broker, dlqTopic, 1)
	if string(records[0].Value) != "not-json" {
		t.Fatalf("死信恢复后应收到原始消息，got %q", records[0].Value)
	}
	waitB3(t, 30*time.Second, "恢复后位移提交", func() bool {
		return kafkaGroupOffsets(t, broker, group, topic, 1)[0] >= 1
	})
}

// TestConsumerRealKafkaRebalanceNoLoss 覆盖 Rebalance 期间不丢消息：
// 两个同组消费者分摊 4 个分区，全部消息都必须被处理（重复由请求状态兜底）。
func TestConsumerRealKafkaRebalanceNoLoss(t *testing.T) {
	broker := kafkaTestBroker(t)
	setupConsumeTest(t)

	skuID := consumeTestSku(t, "real-kafka-rebalance")
	activityID := consumeTestActivity(t, skuID)
	topic := newB3KafkaTopic(t, broker, "rebalance", 4)
	dlqTopic := topic + ".dlq"
	kafkaCreateTopic(t, broker, dlqTopic, 1)
	group := fmt.Sprintf("surgecart-test-b3-rebalance-%d", time.Now().UnixNano())

	const wave = 4
	requestIDs := make([]int64, 0, wave*2)
	events := make([]*RequestedEvent, 0, wave*2)
	insertWave := func(offset int) []*RequestedEvent {
		batch := make([]*RequestedEvent, 0, wave)
		for i := 0; i < wave; i++ {
			id := consumerInsertRequest(t, int64(940000+offset+i), activityID, skuID,
				fmt.Sprintf("real-rebalance-%d", offset+i), nil)
			requestIDs = append(requestIDs, id)
			events = append(events, consumerEventOf(t, id))
			batch = append(batch, events[len(events)-1])
		}
		return batch
	}
	firstWave := insertWave(0)
	kafkaPublishRequestedEvents(t, broker, topic, firstWave...)

	first, firstClient := newB3RealConsumer(t, broker, topic, group, dlqTopic, consumerTestOptions())
	stopFirst := startRealConsumer(t, first, firstClient)
	defer stopFirst()
	waitB3(t, 60*time.Second, "第一波消息全部处理", func() bool {
		for _, id := range requestIDs {
			if b3RequestStatus(t, id) != requestStatusSuccess {
				return false
			}
		}
		return true
	})

	// 第二个消费者加入：必须观察到组内成员数变为 2（即真的完成了一次 Rebalance）。
	second, secondClient := newB3RealConsumer(t, broker, topic, group, dlqTopic, consumerTestOptions())
	stopSecond := startRealConsumer(t, second, secondClient)
	defer stopSecond()
	waitB3(t, 60*time.Second, "消费组完成 Rebalance（成员数 2）", func() bool {
		return kafkaGroupMemberCount(t, broker, group) == 2
	})

	// Rebalance 之后才投递第二波：分区归属已变，消息仍然不能丢、不能重复落单。
	secondWave := insertWave(wave)
	kafkaPublishRequestedEvents(t, broker, topic, secondWave...)
	waitB3(t, 90*time.Second, "Rebalance 之后的消息全部处理", func() bool {
		for _, id := range requestIDs {
			if b3RequestStatus(t, id) != requestStatusSuccess {
				return false
			}
		}
		return true
	})
	total := len(requestIDs)
	if got := b3OrderCount(t, activityID); got != total {
		t.Fatalf("Rebalance 不得丢消息或重复落单，order count=%d want %d", got, total)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != int64(total) {
		t.Fatalf("sold=%d want %d", sold, total)
	}
}

// TestConsumerRealKafkaRestartRecovery 覆盖 Consumer 关闭与重启恢复：
// 中途取消后未提交的消息必须由新实例继续处理，最终不丢不重。
func TestConsumerRealKafkaRestartRecovery(t *testing.T) {
	broker := kafkaTestBroker(t)
	setupConsumeTest(t)

	skuID := consumeTestSku(t, "real-kafka-restart")
	activityID := consumeTestActivity(t, skuID)
	topic := newB3KafkaTopic(t, broker, "restart", 1)
	dlqTopic := topic + ".dlq"
	kafkaCreateTopic(t, broker, dlqTopic, 1)
	group := fmt.Sprintf("surgecart-test-b3-restart-%d", time.Now().UnixNano())

	const total = 4
	requestIDs := make([]int64, 0, total)
	events := make([]*RequestedEvent, 0, total)
	for i := 0; i < total; i++ {
		id := consumerInsertRequest(t, int64(950000+i), activityID, skuID, fmt.Sprintf("real-restart-%d", i), nil)
		requestIDs = append(requestIDs, id)
		events = append(events, consumerEventOf(t, id))
	}
	kafkaPublishRequestedEvents(t, broker, topic, events...)

	first, firstClient := newB3RealConsumer(t, broker, topic, group, dlqTopic, consumerTestOptions())
	stopFirst := startRealConsumer(t, first, firstClient)
	waitB3(t, 60*time.Second, "第一个消费者处理至少一条", func() bool {
		for _, id := range requestIDs {
			if b3RequestStatus(t, id) == requestStatusSuccess {
				return true
			}
		}
		return false
	})
	stopFirst()

	second, secondClient := newB3RealConsumer(t, broker, topic, group, dlqTopic, consumerTestOptions())
	stopSecond := startRealConsumer(t, second, secondClient)
	defer stopSecond()

	waitB3(t, 90*time.Second, "重启后全部消息被处理", func() bool {
		for _, id := range requestIDs {
			if b3RequestStatus(t, id) != requestStatusSuccess {
				return false
			}
		}
		return true
	})
	if got := b3OrderCount(t, activityID); got != total {
		t.Fatalf("重启恢复不得丢消息或重复落单，order count=%d want %d", got, total)
	}
}
