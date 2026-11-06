package kafka

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/mq"
)

// lifecycleConfig 返回一份启用 Kafka 但指向不可达 Broker 的配置。
// 用不可达地址可以在不依赖 Docker/Kafka 的情况下验证：
// 构造不阻塞、发布/拉取受 context 约束、Close 不挂住。
func lifecycleConfig() Config {
	return Config{
		Enabled:       true,
		Brokers:       []string{"127.0.0.1:1"},
		Topic:         "surgecart.flashsale.order.requested.v1",
		DLQTopic:      "surgecart.flashsale.order.requested.v1.dlq",
		ConsumerGroup: "surgecart-kafka-lifecycle-test",
	}
}

// 关闭 Kafka 时不允许创建客户端：配置错误必须在构造阶段明确失败。
func TestProducerRejectsDisabledConfig(t *testing.T) {
	cfg := lifecycleConfig()
	cfg.Enabled = false
	if _, err := NewProducer(cfg); err == nil || !strings.Contains(err.Error(), "未启用") {
		t.Fatalf("期望「未启用」错误，实际: %v", err)
	}
}

// Enabled=true 但缺少 Broker 时必须明确失败，且错误指向 brokers 配置项。
func TestProducerRejectsInvalidConfig(t *testing.T) {
	cfg := lifecycleConfig()
	cfg.Brokers = nil
	if _, err := NewProducer(cfg); err == nil || !strings.Contains(err.Error(), configKeyBrokers) {
		t.Fatalf("期望 Broker 配置错误，实际: %v", err)
	}
}

// 构造 Producer 不主动连接 Broker：Broker 不可达也不能阻塞启动。
func TestProducerConstructionDoesNotBlockOnBroker(t *testing.T) {
	start := time.Now()
	producer, err := NewProducer(lifecycleConfig())
	if err != nil {
		t.Fatalf("构造 Producer 失败: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("构造 Producer 耗时 %v，构造函数不应等待 Broker 连接", elapsed)
	}
	if err := producer.Close(context.Background()); err != nil {
		t.Fatalf("关闭 Producer 失败: %v", err)
	}
}

// context 到期必须让 Publish 及时返回，且不得返回「已成功」的结果。
func TestProducerPublishStopsOnContextDeadline(t *testing.T) {
	producer, err := NewProducer(lifecycleConfig())
	if err != nil {
		t.Fatalf("构造 Producer 失败: %v", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := producer.Close(closeCtx); err != nil {
			t.Logf("关闭 Producer 返回错误（Broker 不可达时允许）: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	results, err := producer.Publish(ctx, mq.Message{Key: []byte("1"), Value: []byte(`{"request_id":1}`)})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Broker 不可达时 Publish 不应返回成功")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("错误应可追溯为 context 超时，实际: %v", err)
	}
	if results != nil {
		t.Fatalf("批次未确认时不得返回逐条成功结果: %#v", results)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Publish 耗时 %v，context 超时未生效", elapsed)
	}
}

// Close 必须幂等：重复调用不 panic、不阻塞、不重复释放资源。
func TestProducerCloseIsIdempotent(t *testing.T) {
	producer, err := NewProducer(lifecycleConfig())
	if err != nil {
		t.Fatalf("构造 Producer 失败: %v", err)
	}
	ctx := context.Background()
	if err := producer.Close(ctx); err != nil {
		t.Fatalf("首次 Close 失败: %v", err)
	}
	start := time.Now()
	if err := producer.Close(ctx); err != nil {
		t.Fatalf("重复 Close 应返回相同结果，实际: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("重复 Close 耗时 %v，应快速返回", elapsed)
	}
}

// 关闭后发布必须返回 ErrClosed，而不是静默丢弃消息。
func TestProducerPublishAfterClose(t *testing.T) {
	producer, err := NewProducer(lifecycleConfig())
	if err != nil {
		t.Fatalf("构造 Producer 失败: %v", err)
	}
	if err := producer.Close(context.Background()); err != nil {
		t.Fatalf("关闭 Producer 失败: %v", err)
	}
	_, err = producer.Publish(context.Background(), mq.Message{Value: []byte("{}")})
	if !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("关闭后发布应返回 ErrClosed，实际: %v", err)
	}
}

// Consumer 的配置校验与 Producer 一致：未启用/缺少必填项都必须明确失败。
func TestConsumerRejectsInvalidConfig(t *testing.T) {
	disabled := lifecycleConfig()
	disabled.Enabled = false
	if _, err := NewConsumer(disabled); err == nil || !strings.Contains(err.Error(), "未启用") {
		t.Fatalf("期望「未启用」错误，实际: %v", err)
	}

	noGroup := lifecycleConfig()
	noGroup.ConsumerGroup = ""
	if _, err := NewConsumer(noGroup); err == nil || !strings.Contains(err.Error(), configKeyConsumerGroup) {
		t.Fatalf("期望消费组配置错误，实际: %v", err)
	}
}

// Consumer 构造不连接 Broker；Close 必须快速返回且幂等。
// Broker 不可达时离组会失败，Close 返回该错误（资源仍然已释放），重复 Close 返回同一结果。
func TestConsumerConstructionAndClose(t *testing.T) {
	consumer, err := NewConsumer(lifecycleConfig())
	if err != nil {
		t.Fatalf("构造 Consumer 失败: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	firstErr := consumer.Close(ctx)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Close 耗时 %v，Broker 不可达时也应快速返回", elapsed)
	}
	start = time.Now()
	secondErr := consumer.Close(ctx)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("重复 Close 耗时 %v，应快速返回", elapsed)
	}
	if (firstErr == nil) != (secondErr == nil) || (firstErr != nil && firstErr.Error() != secondErr.Error()) {
		t.Fatalf("重复 Close 应返回相同结果: first=%v second=%v", firstErr, secondErr)
	}
}

// context 到期必须让 Poll 及时返回（Broker 不可达时不能永久阻塞）。
func TestConsumerPollStopsOnContextDeadline(t *testing.T) {
	consumer, err := NewConsumer(lifecycleConfig())
	if err != nil {
		t.Fatalf("构造 Consumer 失败: %v", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := consumer.Close(closeCtx); err != nil {
			t.Logf("关闭 Consumer 返回错误（Broker 不可达时允许）: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	messages, err := consumer.Poll(ctx, 10)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("context 到期时 Poll 不应返回成功")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("错误应可追溯为 context 超时，实际: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("失败时不应返回消息: %#v", messages)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Poll 耗时 %v，context 超时未生效", elapsed)
	}
}

// 手动提交位移只接受已消费消息：缺少位移或已关闭时明确失败，不能静默提交。
func TestConsumerCommitRejectsInvalidMessages(t *testing.T) {
	consumer, err := NewConsumer(lifecycleConfig())
	if err != nil {
		t.Fatalf("构造 Consumer 失败: %v", err)
	}
	ctx := context.Background()
	if err := consumer.Commit(ctx, mq.Message{Topic: "t", Partition: 0, Offset: -1}); err == nil {
		t.Fatal("未消费消息的负位移不应被提交")
	}
	if err := consumer.Commit(ctx, mq.Message{Partition: 0, Offset: 0}); err == nil {
		t.Fatal("缺少 Topic 的消息不应被提交")
	}
	if err := consumer.Commit(ctx); err != nil {
		t.Fatalf("空提交应直接返回 nil，实际: %v", err)
	}
	if err := consumer.Close(ctx); err != nil {
		t.Logf("关闭 Consumer 返回错误（Broker 不可达时允许）: %v", err)
	}
	if err := consumer.Commit(ctx, mq.Message{Topic: "t", Partition: 0, Offset: 0}); !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("关闭后提交应返回 ErrClosed，实际: %v", err)
	}
}

// 生命周期结束后不应残留 franz-go 的后台协程。
// 断言留 2 个协程余量并轮询等待，避免把测试框架/运行时协程误判为泄漏。
func TestKafkaClientsReleaseGoroutines(t *testing.T) {
	baseline := runtime.NumGoroutine()

	producer, err := NewProducer(lifecycleConfig())
	if err != nil {
		t.Fatalf("构造 Producer 失败: %v", err)
	}
	consumer, err := NewConsumer(lifecycleConfig())
	if err != nil {
		t.Fatalf("构造 Consumer 失败: %v", err)
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := producer.Close(closeCtx); err != nil {
		t.Logf("关闭 Producer 返回错误（Broker 不可达时允许）: %v", err)
	}
	if err := consumer.Close(closeCtx); err != nil {
		t.Logf("关闭 Consumer 返回错误（Broker 不可达时允许）: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		current := runtime.NumGoroutine()
		if current <= baseline+2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("关闭后仍残留协程: baseline=%d current=%d", baseline, current)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
