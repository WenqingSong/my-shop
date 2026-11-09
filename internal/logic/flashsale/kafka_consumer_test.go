package flashsale

// 秒杀 B3 Kafka 业务 Consumer 测试。
//
// 分三层，互不冒充：
//   - 纯内存：消息解析/死信分类、位移提交与 AllowRebalance 生命周期、重试退避；
//     用 fake Consumer / fake Publisher / fake 业务处理器驱动，不需要 MySQL。
//   - 真实 MySQL（+Redis）：ConsumeRequest 的事务语义、终态幂等、并发行锁、重试预算、
//     死信终态与 Redis 补偿恢复；用真实 sFlashSale 驱动。
//   - 真实 Kafka：见 kafka_consumer_kafka_test.go（需显式提供 Broker 才会执行）。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	"cnb.cool/go-cloud-devops/my-shop/internal/mq"
)

// ---------------------------------------------------------------------------
// fake 客户端
// ---------------------------------------------------------------------------

// errFakeConsumerExhausted 表示脚本化批次已耗尽：包装 mq.ErrClosed，
// 让 Run 按「客户端已关闭」的正常语义退出，而不是当成可恢复的拉取错误做无限退避重试。
var errFakeConsumerExhausted = fmt.Errorf("fake consumer: 没有更多批次: %w", mq.ErrClosed)

// fakeConsumer 是可编程的 mq.Consumer + AllowRebalance：
// 批次按顺序返回，并记录提交与再平衡解除次数，用于断言位移提交策略。
type fakeConsumer struct {
	mu sync.Mutex

	batches [][]mq.Message
	// pollErr 非 nil 时下一次 Poll 返回该错误（只生效一次）。
	pollErr error
	// commitFailures 是剩余「提交失败」次数，用于模拟 MySQL 成功但 Offset 提交失败。
	commitFailures int

	committed  []mq.Message
	pollCalls  int
	allowCalls int
	closed     bool
}

func (f *fakeConsumer) Poll(ctx context.Context, maxMessages int) ([]mq.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pollCalls++
	if f.pollErr != nil {
		err := f.pollErr
		f.pollErr = nil
		return nil, err
	}
	if len(f.batches) == 0 {
		return nil, errFakeConsumerExhausted
	}
	batch := f.batches[0]
	f.batches = f.batches[1:]
	if maxMessages > 0 && len(batch) > maxMessages {
		// 保留剩余部分，模拟真实客户端的批量截断。
		f.batches = append([][]mq.Message{batch[maxMessages:]}, f.batches...)
		batch = batch[:maxMessages]
	}
	return batch, nil
}

func (f *fakeConsumer) Commit(ctx context.Context, messages ...mq.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commitFailures > 0 {
		f.commitFailures--
		return errors.New("fake consumer: 位移提交失败")
	}
	f.committed = append(f.committed, messages...)
	return nil
}

func (f *fakeConsumer) Close(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeConsumer) AllowRebalance() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allowCalls++
}

func (f *fakeConsumer) committedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.committed)
}

func (f *fakeConsumer) allowRebalanceCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.allowCalls
}

// scriptedConsumerService 是纯内存测试用的业务处理器：按脚本返回结论，不需要 MySQL。
type scriptedConsumerService struct {
	mu sync.Mutex

	consumeCalls int
	// consume 为 nil 时返回成功。
	consume func(call int, in ConsumeRequest) (ConsumeRequestResult, error)
	// persist 为 nil 时返回「预算未耗尽、退避 1ms」。
	persist func(requestID int64, code codes.Code) (ConsumeFailureState, error)
	// markDead 为 nil 时返回 (true, nil)。
	markDead func(requestID int64, code codes.Code) (bool, error)

	deadMarked  []int64
	persistedID []int64
}

func (s *scriptedConsumerService) ConsumeRequest(ctx context.Context, in ConsumeRequest) (ConsumeRequestResult, error) {
	s.mu.Lock()
	s.consumeCalls++
	call := s.consumeCalls
	consume := s.consume
	s.mu.Unlock()
	if consume == nil {
		return ConsumeRequestResult{Outcome: ConsumeOutcomeSuccess}, nil
	}
	return consume(call, in)
}

func (s *scriptedConsumerService) PersistConsumeFailure(ctx context.Context, requestID int64, code codes.Code) (ConsumeFailureState, error) {
	s.mu.Lock()
	s.persistedID = append(s.persistedID, requestID)
	persist := s.persist
	s.mu.Unlock()
	if persist == nil {
		return ConsumeFailureState{RetryCount: 1, Backoff: time.Millisecond}, nil
	}
	return persist(requestID, code)
}

func (s *scriptedConsumerService) MarkRequestDead(ctx context.Context, requestID int64, code codes.Code) (bool, error) {
	s.mu.Lock()
	s.deadMarked = append(s.deadMarked, requestID)
	markDead := s.markDead
	s.mu.Unlock()
	if markDead == nil {
		return true, nil
	}
	return markDead(requestID, code)
}

// ---------------------------------------------------------------------------
// 纯内存测试辅助
// ---------------------------------------------------------------------------

// consumerTestOptions 返回毫秒级退避的测试参数，避免单测因真实退避而变慢。
func consumerTestOptions() ConsumerOptions {
	return ConsumerOptions{
		BatchSize:           10,
		DLQTopic:            "surgecart.test.flashsale.requested.v1.dlq",
		MaxDeliveryAttempts: maxConsumeRetryAttempts + 1,
		RetryBackoffBase:    time.Millisecond,
		RetryBackoffMax:     4 * time.Millisecond,
		CommitTimeout:       time.Second,
		MessageTimeout:      5 * time.Second,
		FetchBackoff:        time.Millisecond,
	}
}

// newTestConsumer 构造被测 Consumer（fake 三件套）。
func newTestConsumer(t *testing.T, client RebalanceConsumer, dlq mq.Publisher, svc RequestConsumer) *RequestedEventConsumer {
	t.Helper()
	c, err := NewRequestedEventConsumer(client, dlq, svc, consumerTestOptions())
	if err != nil {
		t.Fatalf("new consumer: %v", err)
	}
	return c
}

// runConsumer 运行消费循环直到脚本批次耗尽（或超时），返回 Run 的错误。
func runConsumer(t *testing.T, c *RequestedEventConsumer) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		t.Fatalf("消费循环未在超时内退出")
		return nil
	}
}

// testMessage 构造一条带标准头的消息。
func testMessage(t *testing.T, event *RequestedEvent, partition int32, offset int64) mq.Message {
	t.Helper()
	value, err := event.Encode()
	if err != nil {
		t.Fatalf("encode event: %v", err)
	}
	return mq.Message{
		Topic:     "surgecart.test.flashsale.requested.v1",
		Key:       []byte(requestedMessageKey(event.ActivityID, event.SkuID)),
		Value:     value,
		Partition: partition,
		Offset:    offset,
		Headers: []mq.Header{
			{Key: "event_id", Value: []byte(event.EventID)},
			{Key: "event_type", Value: []byte(event.EventType)},
			{Key: "schema_version", Value: []byte("1")},
		},
	}
}

// testEvent 构造一条合法事件（不依赖数据库）。
func testEvent(requestID, userID, activityID, skuID int64) *RequestedEvent {
	return NewRequestedEvent(RequestedEventInput{
		RequestID:      requestID,
		UserID:         userID,
		ActivityID:     activityID,
		SkuID:          skuID,
		IdempotencyKey: fmt.Sprintf("idem-%d", requestID),
		RequestHash:    requestHash(activityID, skuID),
		OccurredAt:     time.Now().UTC(),
	})
}

// dlqMessages 返回 fake Publisher 收到的全部死信消息。
func dlqMessages(pub *fakePublisher) []mq.Message { return pub.allMessages() }

// ---------------------------------------------------------------------------
// 纯内存：消息解析与永久错误分类
// ---------------------------------------------------------------------------

// TestConsumerParseRequestedEvent 覆盖 B3-2 的消息解析/校验分类：
// 非法 JSON、未知版本、未知事件类型、字段非法、消息头与消息体不一致都必须给出精确原因。
func TestConsumerParseRequestedEvent(t *testing.T) {
	valid := testEvent(11, 22, 33, 44)
	validMsg := testMessage(t, valid, 0, 0)

	cases := []struct {
		name       string
		msg        mq.Message
		wantReason string
		wantErr    bool
	}{
		{"合法消息", validMsg, "", false},
		{"空消息体", mq.Message{}, metrics.ConsumerDLQParse, true},
		{"非 JSON", mq.Message{Value: []byte("{not json")}, metrics.ConsumerDLQParse, true},
		{"未知版本", mq.Message{Value: []byte(`{"event_type":"flashsale.order.requested","schema_version":2}`)},
			metrics.ConsumerDLQSchemaVersion, true},
		{"未知事件类型", mq.Message{Value: []byte(`{"event_type":"other","schema_version":1}`)},
			metrics.ConsumerDLQEventType, true},
		{"event_id 与 request_id 不一致", mq.Message{Value: []byte(
			`{"event_id":"flashsale.requested:999","event_type":"flashsale.order.requested","schema_version":1,` +
				`"request_id":11,"user_id":22,"activity_id":33,"sku_id":44,"idempotency_key":"k",` +
				`"request_hash":"` + requestHash(33, 44) + `","occurred_at":"2026-01-01T00:00:00Z"}`)},
			metrics.ConsumerDLQInvalidField, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, reason, err := parseRequestedEvent(tc.msg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got event %+v", event)
				}
				if reason != tc.wantReason {
					t.Fatalf("reason=%q want %q (err=%v)", reason, tc.wantReason, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if event.RequestID != valid.RequestID || event.EventID != valid.EventID {
				t.Fatalf("unexpected event: %+v", event)
			}
		})
	}

	// 消息头与消息体不一致：生产端拼装错误，属于永久错误。
	broken := validMsg
	broken.Headers = []mq.Header{{Key: "event_id", Value: []byte("flashsale.requested:999")}}
	if _, reason, err := parseRequestedEvent(broken); err == nil || reason != metrics.ConsumerDLQInvalidField {
		t.Fatalf("header mismatch: reason=%q err=%v", reason, err)
	}
}

// TestConsumerInvalidMessageGoesToDLQAndCommits 覆盖「永久错误必须死信，不能阻塞分区」：
// 死信获得确认后才提交原位移，且不调用业务处理器。
func TestConsumerInvalidMessageGoesToDLQAndCommits(t *testing.T) {
	client := &fakeConsumer{batches: [][]mq.Message{{
		{Topic: "t", Partition: 3, Offset: 7, Value: []byte("not-json")},
	}}}
	dlq := &fakePublisher{}
	svc := &scriptedConsumerService{}
	c := newTestConsumer(t, client, dlq, svc)

	err := runConsumer(t, c)
	if !errors.Is(err, errFakeConsumerExhausted) {
		t.Fatalf("run error=%v want exhausted", err)
	}
	if svc.consumeCalls != 0 {
		t.Fatalf("不可处理消息不得进入业务处理，calls=%d", svc.consumeCalls)
	}
	if got := dlqMessages(dlq); len(got) != 1 {
		t.Fatalf("dlq messages=%d want 1", len(got))
	} else {
		msg := got[0]
		if msg.Topic != consumerTestOptions().DLQTopic {
			t.Fatalf("dlq topic=%q", msg.Topic)
		}
		if reason, _ := msg.HeaderValue(dlqHeaderReason); string(reason) != metrics.ConsumerDLQParse {
			t.Fatalf("dlq reason=%q want %q", reason, metrics.ConsumerDLQParse)
		}
		if string(msg.Value) != "not-json" {
			t.Fatalf("死信必须原样保留原始载荷，got %q", msg.Value)
		}
	}
	if client.committedCount() != 1 {
		t.Fatalf("死信 ACK 后必须提交原位移，committed=%d", client.committedCount())
	}
	if client.allowRebalanceCount() == 0 {
		t.Fatalf("每个 Poll 批次都必须 AllowRebalance")
	}
}

// TestConsumerDLQFailureKeepsOffset 覆盖「死信投递失败不得提交原位移」。
func TestConsumerDLQFailureKeepsOffset(t *testing.T) {
	client := &fakeConsumer{batches: [][]mq.Message{{
		{Topic: "t", Partition: 0, Offset: 5, Value: []byte("not-json")},
	}}}
	dlq := &fakePublisher{publish: func(ctx context.Context, msgs []mq.Message) ([]mq.PublishResult, error) {
		return nil, errors.New("broker unavailable")
	}}
	c := newTestConsumer(t, client, dlq, &scriptedConsumerService{})

	err := runConsumer(t, c)
	if err == nil {
		t.Fatalf("死信失败时 Run 必须返回错误而不是静默继续")
	}
	if errors.Is(err, errFakeConsumerExhausted) {
		t.Fatalf("不应走到批次耗尽，err=%v", err)
	}
	if client.committedCount() != 0 {
		t.Fatalf("死信未确认时不得提交位移，committed=%d", client.committedCount())
	}
	if client.allowRebalanceCount() == 0 {
		t.Fatalf("异常路径同样必须 AllowRebalance，否则消费组永久阻塞")
	}
}

// TestConsumerCommitFailureStopsProgress 覆盖「MySQL 成功但 Offset 提交失败」：
// 处理成功但提交失败时 Run 必须报错退出（不能假装成功），消息会被重新投递。
func TestConsumerCommitFailureStopsProgress(t *testing.T) {
	event := testEvent(101, 201, 301, 401)
	client := &fakeConsumer{
		batches:        [][]mq.Message{{testMessage(t, event, 0, 1), testMessage(t, event, 0, 2)}},
		commitFailures: 1,
	}
	svc := &scriptedConsumerService{}
	c := newTestConsumer(t, client, &fakePublisher{}, svc)

	err := runConsumer(t, c)
	if err == nil || errors.Is(err, errFakeConsumerExhausted) {
		t.Fatalf("提交失败必须让 Run 返回错误，got %v", err)
	}
	if client.committedCount() != 0 {
		t.Fatalf("提交失败后不得有任何已提交位移，committed=%d", client.committedCount())
	}
	if svc.consumeCalls != 2 {
		t.Fatalf("同一批次内的两条消息都应被处理（幂等由 Request 状态保证），calls=%d", svc.consumeCalls)
	}
}

// TestConsumerIdentityMismatchGoesToDLQ 覆盖「消息与 MySQL 身份不一致 → 死信，不改数据库」。
func TestConsumerIdentityMismatchGoesToDLQ(t *testing.T) {
	event := testEvent(102, 202, 302, 402)
	client := &fakeConsumer{batches: [][]mq.Message{{testMessage(t, event, 0, 0)}}}
	dlq := &fakePublisher{}
	svc := &scriptedConsumerService{consume: func(call int, in ConsumeRequest) (ConsumeRequestResult, error) {
		return ConsumeRequestResult{Outcome: ConsumeOutcomeInvalid, Reason: ConsumeReasonIdentity}, nil
	}}
	c := newTestConsumer(t, client, dlq, svc)

	if err := runConsumer(t, c); !errors.Is(err, errFakeConsumerExhausted) {
		t.Fatalf("run error=%v", err)
	}
	if got := dlqMessages(dlq); len(got) != 1 {
		t.Fatalf("dlq messages=%d want 1", len(got))
	}
	if len(svc.deadMarked) != 0 {
		t.Fatalf("身份不一致时不得改写数据库终态，deadMarked=%v", svc.deadMarked)
	}
	if client.committedCount() != 1 {
		t.Fatalf("永久错误死信后应提交位移，committed=%d", client.committedCount())
	}
}

// TestConsumerRetryBudgetExhaustedGoesToDLQ 覆盖「预算耗尽 → 死信 → dead → 提交位移」顺序。
func TestConsumerRetryBudgetExhaustedGoesToDLQ(t *testing.T) {
	event := testEvent(103, 203, 303, 403)
	client := &fakeConsumer{batches: [][]mq.Message{{testMessage(t, event, 1, 9)}}}
	dlq := &fakePublisher{}
	svc := &scriptedConsumerService{
		consume: func(call int, in ConsumeRequest) (ConsumeRequestResult, error) {
			return ConsumeRequestResult{}, errors.New("mysql deadlock")
		},
		persist: func(requestID int64, code codes.Code) (ConsumeFailureState, error) {
			return ConsumeFailureState{Exhausted: true, RetryCount: maxConsumeRetryAttempts + 1}, nil
		},
	}
	c := newTestConsumer(t, client, dlq, svc)

	if err := runConsumer(t, c); !errors.Is(err, errFakeConsumerExhausted) {
		t.Fatalf("run error=%v", err)
	}
	if len(dlqMessages(dlq)) != 1 {
		t.Fatalf("预算耗尽必须投递死信，got %d", len(dlqMessages(dlq)))
	}
	if len(svc.deadMarked) != 1 || svc.deadMarked[0] != event.RequestID {
		t.Fatalf("死信 ACK 后必须落 dead 终态，deadMarked=%v", svc.deadMarked)
	}
	if client.committedCount() != 1 {
		t.Fatalf("死信+dead 之后才提交位移，committed=%d", client.committedCount())
	}
}

// TestConsumerDeadLetterFailureKeepsOffset 覆盖「死信成功但 dead 终态写入失败」：
// 位移不提交、消息会被重投，死信可能重复但原始请求不丢。
func TestConsumerDeadLetterFailureKeepsOffset(t *testing.T) {
	event := testEvent(104, 204, 304, 404)
	client := &fakeConsumer{batches: [][]mq.Message{{testMessage(t, event, 0, 3)}}}
	dlq := &fakePublisher{}
	svc := &scriptedConsumerService{
		consume: func(call int, in ConsumeRequest) (ConsumeRequestResult, error) {
			return ConsumeRequestResult{}, errors.New("mysql deadlock")
		},
		persist: func(requestID int64, code codes.Code) (ConsumeFailureState, error) {
			return ConsumeFailureState{Exhausted: true, RetryCount: 4}, nil
		},
		markDead: func(requestID int64, code codes.Code) (bool, error) {
			return false, errors.New("mysql down")
		},
	}
	c := newTestConsumer(t, client, dlq, svc)

	err := runConsumer(t, c)
	if err == nil || errors.Is(err, errFakeConsumerExhausted) {
		t.Fatalf("dead 迁移失败时 Run 必须报错，got %v", err)
	}
	if client.committedCount() != 0 {
		t.Fatalf("dead 未落地时不得提交位移，committed=%d", client.committedCount())
	}
	if len(dlqMessages(dlq)) != 1 {
		t.Fatalf("死信应先于终态投递，got %d", len(dlqMessages(dlq)))
	}
}

// TestConsumerRetryThenSuccess 覆盖「技术失败有界重试后成功」：
// 每一次失败都持久化重试预算，退避后重试同一条消息，最终成功并提交位移。
func TestConsumerRetryThenSuccess(t *testing.T) {
	restore := kafkaConsumeBackoff
	kafkaConsumeBackoff = func(int) time.Duration { return time.Millisecond }
	defer func() { kafkaConsumeBackoff = restore }()

	event := testEvent(105, 205, 305, 405)
	client := &fakeConsumer{batches: [][]mq.Message{{testMessage(t, event, 0, 0)}}}
	svc := &scriptedConsumerService{
		consume: func(call int, in ConsumeRequest) (ConsumeRequestResult, error) {
			if call < 3 {
				return ConsumeRequestResult{}, errors.New("mysql unavailable")
			}
			return ConsumeRequestResult{Outcome: ConsumeOutcomeSuccess}, nil
		},
		persist: func(requestID int64, code codes.Code) (ConsumeFailureState, error) {
			return ConsumeFailureState{RetryCount: 1, Backoff: time.Millisecond}, nil
		},
	}
	c := newTestConsumer(t, client, &fakePublisher{}, svc)

	if err := runConsumer(t, c); !errors.Is(err, errFakeConsumerExhausted) {
		t.Fatalf("run error=%v", err)
	}
	if svc.consumeCalls != 3 {
		t.Fatalf("consumeCalls=%d want 3", svc.consumeCalls)
	}
	if len(svc.persistedID) != 2 {
		t.Fatalf("每次技术失败都必须持久化重试预算，got %d", len(svc.persistedID))
	}
	if client.committedCount() != 1 {
		t.Fatalf("最终成功后才提交位移，committed=%d", client.committedCount())
	}
}

// TestConsumerPersistenceFailureNeverDropsMessage 覆盖 B3-5 的关键约束：
// 无法持久化重试次数时，即使本地尝试次数达到阈值，也不能把消息当死信丢弃（不提交位移）。
func TestConsumerPersistenceFailureNeverDropsMessage(t *testing.T) {
	event := testEvent(106, 206, 306, 406)
	client := &fakeConsumer{batches: [][]mq.Message{{testMessage(t, event, 0, 0)}}}
	dlq := &fakePublisher{}
	svc := &scriptedConsumerService{
		consume: func(call int, in ConsumeRequest) (ConsumeRequestResult, error) {
			return ConsumeRequestResult{}, errors.New("mysql down")
		},
		persist: func(requestID int64, code codes.Code) (ConsumeFailureState, error) {
			return ConsumeFailureState{}, errors.New("mysql down")
		},
	}
	c := newTestConsumer(t, client, dlq, svc)

	err := runConsumer(t, c)
	if err == nil || errors.Is(err, errFakeConsumerExhausted) {
		t.Fatalf("无法持久化预算时必须停止并保留位移，got %v", err)
	}
	if len(dlqMessages(dlq)) != 0 {
		t.Fatalf("不能因为本地计数达到阈值就投递死信丢弃有效消息")
	}
	if client.committedCount() != 0 {
		t.Fatalf("位移不得提交，committed=%d", client.committedCount())
	}
	if svc.consumeCalls > c.opts.MaxDeliveryAttempts {
		t.Fatalf("本地尝试次数必须有界，calls=%d max=%d", svc.consumeCalls, c.opts.MaxDeliveryAttempts)
	}
}

// TestConsumerBackoffBounded 校验退避单调、有上限，不允许无限增长。
func TestConsumerBackoffBounded(t *testing.T) {
	prev := time.Duration(0)
	for i := 1; i <= 10; i++ {
		d := kafkaConsumeBackoff(i)
		if d > kafkaConsumeBackoffMax {
			t.Fatalf("backoff(%d)=%s 超过上限 %s", i, d, kafkaConsumeBackoffMax)
		}
		if d < prev {
			t.Fatalf("backoff 不应回退：backoff(%d)=%s < %s", i, d, prev)
		}
		prev = d
	}
	if kafkaConsumeBackoff(100) != kafkaConsumeBackoffMax {
		t.Fatalf("退避必须封顶")
	}
}

// TestConsumerOptionsValidation 覆盖构造期校验与批量上限截断。
func TestConsumerOptionsValidation(t *testing.T) {
	if _, err := NewRequestedEventConsumer(nil, &fakePublisher{}, &scriptedConsumerService{}, ConsumerOptions{DLQTopic: "d"}); err == nil {
		t.Fatal("nil 客户端必须构造失败")
	}
	if _, err := NewRequestedEventConsumer(&fakeConsumer{}, nil, &scriptedConsumerService{}, ConsumerOptions{DLQTopic: "d"}); err == nil {
		t.Fatal("nil 死信发布器必须构造失败")
	}
	if _, err := NewRequestedEventConsumer(&fakeConsumer{}, &fakePublisher{}, nil, ConsumerOptions{DLQTopic: "d"}); err == nil {
		t.Fatal("nil 业务处理器必须构造失败")
	}
	if _, err := NewRequestedEventConsumer(&fakeConsumer{}, &fakePublisher{}, &scriptedConsumerService{}, ConsumerOptions{}); err == nil {
		t.Fatal("空死信 Topic 必须构造失败")
	}
	c, err := NewRequestedEventConsumer(&fakeConsumer{}, &fakePublisher{}, &scriptedConsumerService{},
		ConsumerOptions{DLQTopic: "dlq", BatchSize: 10_000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.opts.BatchSize != maxConsumerBatchSize {
		t.Fatalf("批量上限必须截断到 %d，got %d", maxConsumerBatchSize, c.opts.BatchSize)
	}
}

// TestConsumerContextCancelReleasesRebalance 覆盖「Context 取消时释放 Rebalance 阻塞」。
func TestConsumerContextCancelReleasesRebalance(t *testing.T) {
	event := testEvent(107, 207, 307, 407)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	svc := &scriptedConsumerService{consume: func(call int, in ConsumeRequest) (ConsumeRequestResult, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return ConsumeRequestResult{Outcome: ConsumeOutcomeSuccess}, nil
	}}
	client := &fakeConsumer{batches: [][]mq.Message{{testMessage(t, event, 0, 0)}}}
	c := newTestConsumer(t, client, &fakePublisher{}, svc)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	<-entered
	cancel()
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("context 取消后消费循环未及时退出")
	}
	if client.allowRebalanceCount() == 0 {
		t.Fatalf("context 取消路径也必须 AllowRebalance")
	}
}

// ---------------------------------------------------------------------------
// 真实 MySQL / Redis：ConsumeRequest 业务语义
// ---------------------------------------------------------------------------

// consumerInsertRequest 直接写入一条异步请求（不经闸门），返回 id。
func consumerInsertRequest(t *testing.T, userID, activityID, skuID int64, key string, overrides g.Map) int64 {
	t.Helper()
	data := g.Map{
		"user_id":         userID,
		"activity_id":     activityID,
		"sku_id":          skuID,
		"idempotency_key": key,
		"request_hash":    requestHash(activityID, skuID),
		"status":          requestStatusQueued,
	}
	for k, v := range overrides {
		data[k] = v
	}
	id, err := g.DB().Model(requestTable).Ctx(context.Background()).Data(data).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert request %s: %v", key, err)
	}
	return id
}

// consumerLoadRow 读取请求行（不存在直接失败）。
func consumerLoadRow(t *testing.T, requestID int64) *orderRequestRow {
	t.Helper()
	var rows []*orderRequestRow
	if err := g.DB().Model(requestTable).Ctx(context.Background()).Where("id", requestID).Scan(&rows); err != nil {
		t.Fatalf("load request %d: %v", requestID, err)
	}
	if len(rows) == 0 {
		t.Fatalf("request %d not found", requestID)
	}
	return rows[0]
}

// consumerEventOf 按数据库行构造一条「身份一致」的事件（模拟 Outbox 发布的消息）。
func consumerEventOf(t *testing.T, requestID int64) *RequestedEvent {
	t.Helper()
	r := consumerLoadRow(t, requestID)
	return NewRequestedEvent(RequestedEventInput{
		RequestID:      r.Id,
		UserID:         r.UserId,
		ActivityID:     r.ActivityId,
		SkuID:          r.SkuId,
		IdempotencyKey: r.IdempotencyKey,
		RequestHash:    r.RequestHash,
		OccurredAt:     time.Now().UTC(),
	})
}

// consumerConsume 把事件交给业务核心执行一次（等价于 Kafka 消费到一条消息）。
func consumerConsume(t *testing.T, s *sFlashSale, event *RequestedEvent) (ConsumeRequestResult, error) {
	t.Helper()
	return s.ConsumeRequest(context.Background(), ConsumeRequest{
		RequestID:      event.RequestID,
		UserID:         event.UserID,
		ActivityID:     event.ActivityID,
		SkuID:          event.SkuID,
		IdempotencyKey: event.IdempotencyKey,
		RequestHash:    event.RequestHash,
	})
}

// TestConsumeRequestCreatesOrder 覆盖正常创建订单（消息驱动，而非扫描 queued 队列）。
func TestConsumeRequestCreatesOrder(t *testing.T) {
	setupConsumeTest(t)
	s := New()
	skuID := consumeTestSku(t, "kafka-ok")
	activityID := consumeTestActivity(t, skuID)
	requestID := consumerInsertRequest(t, 910001, activityID, skuID, "kafka-ok", nil)

	res, err := consumerConsume(t, s, consumerEventOf(t, requestID))
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if res.Outcome != ConsumeOutcomeSuccess || res.OrderID <= 0 {
		t.Fatalf("outcome=%s order=%d want success", res.Outcome, res.OrderID)
	}
	req := consumerLoadRow(t, requestID)
	if req.Status != requestStatusSuccess || req.FlashOrderId == nil || *req.FlashOrderId != res.OrderID {
		t.Fatalf("request status=%d order=%v want success/%d", req.Status, req.FlashOrderId, res.OrderID)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("sold=%d want 1", sold)
	}
	var orderCount int
	if orderCount, _ = g.DB().Model("flash_sale_orders").Ctx(context.Background()).
		Where("activity_id", activityID).Count(); orderCount != 1 {
		t.Fatalf("order count=%d want 1", orderCount)
	}
}

// TestConsumeRequestBusinessFailures 覆盖库存不足、活动结束、用户重复购买三类业务失败终态。
func TestConsumeRequestBusinessFailures(t *testing.T) {
	t.Run("库存不足", func(t *testing.T) {
		setupConsumeTest(t)
		s := New()
		skuID := consumeTestSku(t, "kafka-soldout")
		activityID := consumeTestActivity(t, skuID)
		// 把库存压到 0（sold=total）→ 条件判定不足。
		if _, err := g.DB().Exec(context.Background(),
			"UPDATE flash_sale_activity_skus SET sold = total_stock WHERE activity_id = ? AND sku_id = ?",
			activityID, skuID); err != nil {
			t.Fatalf("sold out: %v", err)
		}
		requestID := consumerInsertRequest(t, 910002, activityID, skuID, "kafka-soldout", nil)

		res, err := consumerConsume(t, s, consumerEventOf(t, requestID))
		if err != nil {
			t.Fatalf("consume: %v", err)
		}
		if res.Outcome != ConsumeOutcomeFailed || res.ErrorCode != codes.CodeFlashSaleStockInsufficient {
			t.Fatalf("outcome=%s code=%d want failed/12003", res.Outcome, res.ErrorCode)
		}
		req := consumerLoadRow(t, requestID)
		if req.Status != requestStatusFailed || req.LastErrorCode == nil || *req.LastErrorCode != codes.CodeFlashSaleStockInsufficient {
			t.Fatalf("request status=%d code=%v want failed/12003", req.Status, req.LastErrorCode)
		}
		if n, _ := g.DB().Model("flash_sale_orders").Ctx(context.Background()).Where("activity_id", activityID).Count(); n != 0 {
			t.Fatalf("业务失败不得建单，count=%d", n)
		}
	})

	t.Run("活动结束", func(t *testing.T) {
		setupConsumeTest(t)
		s := New()
		skuID := consumeTestSku(t, "kafka-ended")
		activityID := consumeTestActivity(t, skuID)
		if _, err := g.DB().Exec(context.Background(),
			"UPDATE flash_sale_activities SET end_time = DATE_SUB(NOW(), INTERVAL 1 SECOND) WHERE id = ?", activityID); err != nil {
			t.Fatalf("end activity: %v", err)
		}
		requestID := consumerInsertRequest(t, 910003, activityID, skuID, "kafka-ended", nil)

		res, err := consumerConsume(t, s, consumerEventOf(t, requestID))
		if err != nil {
			t.Fatalf("consume: %v", err)
		}
		if res.Outcome != ConsumeOutcomeFailed || res.ErrorCode != codes.CodeFlashSaleNotInTimeWindow {
			t.Fatalf("outcome=%s code=%d want failed/12002", res.Outcome, res.ErrorCode)
		}
	})

	t.Run("用户重复购买", func(t *testing.T) {
		setupConsumeTest(t)
		s := New()
		skuID := consumeTestSku(t, "kafka-repeat-buy")
		activityID := consumeTestActivity(t, skuID)
		// 同用户已有成功订单 → 命中 uk_flash_one_per_user。
		if _, err := g.DB().Model("flash_sale_orders").Ctx(context.Background()).Data(g.Map{
			"order_no": "FS-EXISTED", "user_id": 910004, "activity_id": activityID, "sku_id": skuID,
			"product_id": 1, "sku_name": "x", "product_name": "x", "product_main_image": "",
			"flash_price": 1, "quantity": 1, "idempotency_key": "existed", "request_hash": "existed",
		}).Insert(); err != nil {
			t.Fatalf("seed order: %v", err)
		}
		requestID := consumerInsertRequest(t, 910004, activityID, skuID, "kafka-repeat-buy", nil)

		res, err := consumerConsume(t, s, consumerEventOf(t, requestID))
		if err != nil {
			t.Fatalf("consume: %v", err)
		}
		if res.Outcome != ConsumeOutcomeFailed || res.ErrorCode != codes.CodeFlashSaleAlreadyPurchased {
			t.Fatalf("outcome=%s code=%d want failed/12004", res.Outcome, res.ErrorCode)
		}
	})
}

// TestConsumeRequestDuplicateDelivery 覆盖同一消息重复消费：第二次必须安全跳过，不重复落单。
func TestConsumeRequestDuplicateDelivery(t *testing.T) {
	setupConsumeTest(t)
	s := New()
	skuID := consumeTestSku(t, "kafka-dup")
	activityID := consumeTestActivity(t, skuID)
	requestID := consumerInsertRequest(t, 910005, activityID, skuID, "kafka-dup", nil)
	event := consumerEventOf(t, requestID)

	if res, err := consumerConsume(t, s, event); err != nil || res.Outcome != ConsumeOutcomeSuccess {
		t.Fatalf("first consume: res=%+v err=%v", res, err)
	}
	for i := 0; i < 3; i++ {
		res, err := consumerConsume(t, s, event)
		if err != nil {
			t.Fatalf("duplicate consume %d: %v", i, err)
		}
		if res.Outcome != ConsumeOutcomeDuplicate {
			t.Fatalf("duplicate consume %d outcome=%s want duplicate", i, res.Outcome)
		}
	}
	if n, _ := g.DB().Model("flash_sale_orders").Ctx(context.Background()).Where("activity_id", activityID).Count(); n != 1 {
		t.Fatalf("order count=%d want 1", n)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("sold=%d want 1", sold)
	}
}

// TestConsumeRequestConcurrentSameRequest 覆盖两个 Consumer 并发处理同一 Request：
// 行锁串行化 + 状态闸门保证只有一次业务效果。
func TestConsumeRequestConcurrentSameRequest(t *testing.T) {
	setupConsumeTest(t)
	skuID := consumeTestSku(t, "kafka-race")
	activityID := consumeTestActivity(t, skuID)
	requestID := consumerInsertRequest(t, 910006, activityID, skuID, "kafka-race", nil)
	event := consumerEventOf(t, requestID)

	const workers = 4
	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		success atomic.Int32
		dup     atomic.Int32
		failed  atomic.Int32
	)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// 每个并发的「消费者」持有独立的服务实例（模拟多实例部署）。
			res, err := consumerConsume(t, New(), event)
			if err != nil {
				errs <- err
				return
			}
			switch res.Outcome {
			case ConsumeOutcomeSuccess:
				success.Add(1)
			case ConsumeOutcomeDuplicate:
				dup.Add(1)
			default:
				failed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent consume: %v", err)
	}
	if success.Load() != 1 {
		t.Fatalf("success=%d want 1（行锁 + 状态闸门只允许一次落单）", success.Load())
	}
	if dup.Load()+failed.Load() != workers-1 {
		t.Fatalf("其余 %d 个并发消费者必须安全跳过，dup=%d other=%d", workers-1, dup.Load(), failed.Load())
	}
	if n, _ := g.DB().Model("flash_sale_orders").Ctx(context.Background()).Where("activity_id", activityID).Count(); n != 1 {
		t.Fatalf("order count=%d want 1", n)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("sold=%d want 1", sold)
	}
}

// TestConsumeRequestTerminalStatusesSkipped 覆盖已是终态的请求（success/failed/dead）重新消费。
func TestConsumeRequestTerminalStatusesSkipped(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{"success", requestStatusSuccess},
		{"failed", requestStatusFailed},
		{"dead", requestStatusDead},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupConsumeTest(t)
			s := New()
			skuID := consumeTestSku(t, "kafka-terminal-"+tc.name)
			activityID := consumeTestActivity(t, skuID)
			requestID := consumerInsertRequest(t, 910007, activityID, skuID, "kafka-terminal", g.Map{
				"status": tc.status,
			})
			res, err := consumerConsume(t, s, consumerEventOf(t, requestID))
			if err != nil {
				t.Fatalf("consume: %v", err)
			}
			if res.Outcome != ConsumeOutcomeDuplicate {
				t.Fatalf("outcome=%s want duplicate", res.Outcome)
			}
			if n, _ := g.DB().Model("flash_sale_orders").Ctx(context.Background()).Where("activity_id", activityID).Count(); n != 0 {
				t.Fatalf("终态请求不得落单，count=%d", n)
			}
			if _, sold := consumeBindingStock(t, activityID, skuID); sold != 0 {
				t.Fatalf("终态请求不得扣库存，sold=%d", sold)
			}
		})
	}
}

// TestConsumeRequestRepairDeadToQueued 覆盖人工 dead→queued 修复后重新消费：
// 修复会复用同一 event_id，因此消费方绝不能依赖 event_id 去重。
func TestConsumeRequestRepairDeadToQueued(t *testing.T) {
	setupConsumeTest(t)
	s := New()
	ctx := context.Background()
	skuID := consumeTestSku(t, "kafka-repair")
	activityID := consumeTestActivity(t, skuID)
	requestID := consumerInsertRequest(t, 910008, activityID, skuID, "kafka-repair", g.Map{
		"status":      requestStatusDead,
		"retry_count": maxConsumeRetryAttempts + 1,
	})
	before := consumerLoadRow(t, requestID)

	adminID, err := g.DB().Model("admins").Ctx(ctx).Data(g.Map{
		"username": "repair-admin", "password_hash": "x", "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	if _, err := s.RepairRequest(ctx, adminID, &v1.RepairRequestReq{
		Id:           requestID,
		TargetStatus: v1.RequestStatusQueued,
		Reason:       "b3 重放验证",
	}); err != nil {
		t.Fatalf("repair: %v", err)
	}

	repaired := consumerLoadRow(t, requestID)
	if repaired.Status != requestStatusQueued || repaired.RetryCount != 0 {
		t.Fatalf("修复后 status=%d retry=%d want queued/0", repaired.Status, repaired.RetryCount)
	}
	// 修复前后事件身份完全相同（B2.1 冻结：event_id 由 request_id 稳定推导）。
	after := consumerEventOf(t, requestID)
	if after.EventID != RequestedEventID(before.Id) || after.EventID == "" {
		t.Fatalf("修复后 event_id 必须保持不变，got %q", after.EventID)
	}
	res, err := consumerConsume(t, s, after)
	if err != nil {
		t.Fatalf("consume after repair: %v", err)
	}
	if res.Outcome != ConsumeOutcomeSuccess {
		t.Fatalf("outcome=%s want success（event_id 不能当永久去重闸门）", res.Outcome)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("sold=%d want 1", sold)
	}
}

// TestConsumeRequestRollbackOnCrash 覆盖「事务回滚后重投只产生一次业务效果」。
func TestConsumeRequestRollbackOnCrash(t *testing.T) {
	setupConsumeTest(t)
	s := New()
	skuID := consumeTestSku(t, "kafka-rollback")
	activityID := consumeTestActivity(t, skuID)
	requestID := consumerInsertRequest(t, 910009, activityID, skuID, "kafka-rollback", nil)
	event := consumerEventOf(t, requestID)

	consumeBeforeCommitHook = func(ctx context.Context, r *orderRequestRow) error {
		return errors.New("simulated crash before commit")
	}
	defer func() { consumeBeforeCommitHook = nil }()

	if _, err := consumerConsume(t, s, event); err == nil {
		t.Fatalf("expected transaction failure")
	}
	req := consumerLoadRow(t, requestID)
	if req.Status != requestStatusQueued {
		t.Fatalf("回滚后 status=%d want queued", req.Status)
	}
	if n, _ := g.DB().Model("flash_sale_orders").Ctx(context.Background()).Where("activity_id", activityID).Count(); n != 0 {
		t.Fatalf("回滚后不得残留订单，count=%d", n)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 0 {
		t.Fatalf("回滚后不得扣库存，sold=%d", sold)
	}

	consumeBeforeCommitHook = nil
	res, err := consumerConsume(t, s, event)
	if err != nil || res.Outcome != ConsumeOutcomeSuccess {
		t.Fatalf("重投结果 res=%+v err=%v", res, err)
	}
	if n, _ := g.DB().Model("flash_sale_orders").Ctx(context.Background()).Where("activity_id", activityID).Count(); n != 1 {
		t.Fatalf("重投后订单数=%d want 1", n)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("重投后 sold=%d want 1", sold)
	}
}

// TestConsumeRequestIdentityMismatch 覆盖消息与 MySQL 身份不一致：永久错误，不改数据库。
func TestConsumeRequestIdentityMismatch(t *testing.T) {
	setupConsumeTest(t)
	s := New()
	skuID := consumeTestSku(t, "kafka-identity")
	activityID := consumeTestActivity(t, skuID)
	requestID := consumerInsertRequest(t, 910010, activityID, skuID, "kafka-identity", nil)
	event := consumerEventOf(t, requestID)

	cases := []struct {
		name   string
		mutate func(e *RequestedEvent)
	}{
		{"user_id", func(e *RequestedEvent) { e.UserID++ }},
		{"activity_id", func(e *RequestedEvent) { e.ActivityID++ }},
		{"sku_id", func(e *RequestedEvent) { e.SkuID++ }},
		{"idempotency_key", func(e *RequestedEvent) { e.IdempotencyKey = "other" }},
		{"request_hash", func(e *RequestedEvent) { e.RequestHash = requestHash(activityID+1, skuID) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := *event
			tc.mutate(&mutated)
			// event_id 必须随 request_id 重新推导，否则会先被字段校验拦下。
			mutated.EventID = RequestedEventID(mutated.RequestID)
			res, err := consumerConsume(t, s, &mutated)
			if err != nil {
				t.Fatalf("identity mismatch must not be a technical error: %v", err)
			}
			if res.Outcome != ConsumeOutcomeInvalid || res.Reason != ConsumeReasonIdentity {
				t.Fatalf("outcome=%s reason=%s want invalid/identity", res.Outcome, res.Reason)
			}
			req := consumerLoadRow(t, requestID)
			if req.Status != requestStatusQueued {
				t.Fatalf("身份不一致不得改写数据库，status=%d", req.Status)
			}
		})
	}

	// 请求不存在：同样按永久错误处理，不建单、不改库。
	if res, err := s.ConsumeRequest(context.Background(), ConsumeRequest{
		RequestID: 999999999, UserID: 1, ActivityID: 1, SkuID: 1,
		IdempotencyKey: "missing", RequestHash: requestHash(1, 1),
	}); err != nil || res.Outcome != ConsumeOutcomeInvalid || res.Reason != ConsumeReasonRequestMissing {
		t.Fatalf("missing request: res=%+v err=%v", res, err)
	}
}

// TestConsumerRetryBudgetPersisted 覆盖重试次数持久化：
// 两次独立调用（模拟进程重启）必须继续累加同一预算，而不是每次重置。
func TestConsumerRetryBudgetPersisted(t *testing.T) {
	setupConsumeTest(t)
	s := New()
	ctx := context.Background()
	skuID := consumeTestSku(t, "kafka-retry-persist")
	activityID := consumeTestActivity(t, skuID)
	requestID := consumerInsertRequest(t, 910011, activityID, skuID, "kafka-retry-persist", nil)

	// 固定订单号制造 uk_flash_order_no 撞号（技术失败），保证事务干净回滚。
	if _, err := g.DB().Model("flash_sale_orders").Ctx(ctx).Data(g.Map{
		"order_no": "FS-COLLIDE", "user_id": 1, "activity_id": 999999, "sku_id": 999999,
		"product_id": 1, "sku_name": "x", "product_name": "x", "product_main_image": "",
		"flash_price": 1, "quantity": 1, "idempotency_key": "collide", "request_hash": "collide",
	}).Insert(); err != nil {
		t.Fatalf("seed colliding order: %v", err)
	}
	orig := generateOrderNo
	generateOrderNo = func() string { return "FS-COLLIDE" }
	defer func() { generateOrderNo = orig }()

	event := consumerEventOf(t, requestID)
	for i := 1; i <= 2; i++ {
		// 每次用新的服务实例，模拟进程重启：预算必须从数据库继续，而不是本地归零。
		_, err := consumerConsume(t, New(), event)
		if err == nil {
			t.Fatalf("第 %d 次消费应因订单号撞号失败", i)
		}
		state, perr := s.PersistConsumeFailure(ctx, requestID, codes.CodeInternalError)
		if perr != nil {
			t.Fatalf("persist failure %d: %v", i, perr)
		}
		if state.Exhausted || state.Resolved {
			t.Fatalf("第 %d 次记账状态异常: %+v", i, state)
		}
		row := consumerLoadRow(t, requestID)
		if row.RetryCount != i {
			t.Fatalf("第 %d 次后 retry_count=%d want %d", i, row.RetryCount, i)
		}
		if row.Status != requestStatusQueued {
			t.Fatalf("技术失败不得改变状态，status=%d", row.Status)
		}
		if row.NextAttemptAt == nil || !row.NextAttemptAt.Time.After(time.Now().Add(-time.Second)) {
			t.Fatalf("技术失败必须写入退避时间，got %+v", row.NextAttemptAt)
		}
		if n, _ := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("activity_id", activityID).Count(); n != 0 {
			t.Fatalf("技术失败不得残留订单，count=%d", n)
		}
	}
}

// TestConsumerRetryExhaustedDeadAndDLQ 覆盖超出重试预算 → 死信 → dead → Redis 补偿。
func TestConsumerRetryExhaustedDeadAndDLQ(t *testing.T) {
	restore := kafkaConsumeBackoff
	kafkaConsumeBackoff = func(int) time.Duration { return time.Millisecond }
	defer func() { kafkaConsumeBackoff = restore }()

	setupConsumeTest(t)
	ctx := context.Background()
	s := New()
	skuID := consumeTestSku(t, "kafka-exhaust")
	activityID := consumeTestActivity(t, skuID)
	userID := int64(910012)
	const key = "kafka-exhaust"
	requestID := consumerInsertRequest(t, userID, activityID, skuID, key, g.Map{
		"retry_count": maxConsumeRetryAttempts,
	})

	// 模拟闸门预扣已经发生：Redis 库存 -1 并写入一人一单/幂等标记。
	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}
	if _, err := g.Redis().Do(ctx, "DECR", flashSaleStockKey(activityID, skuID)); err != nil {
		t.Fatalf("pre-deduct: %v", err)
	}
	if _, err := g.Redis().Do(ctx, "SET", flashSaleBoughtKey(activityID, skuID, userID), "1"); err != nil {
		t.Fatalf("set bought marker: %v", err)
	}
	if _, err := g.Redis().Do(ctx, "SET", flashSaleIdemKey(userID, key), requestHash(activityID, skuID)); err != nil {
		t.Fatalf("set idem marker: %v", err)
	}

	// 固定订单号制造技术失败：本次投递会把 retry_count 推到预算之外。
	if _, err := g.DB().Model("flash_sale_orders").Ctx(ctx).Data(g.Map{
		"order_no": "FS-COLLIDE", "user_id": 1, "activity_id": 999999, "sku_id": 999999,
		"product_id": 1, "sku_name": "x", "product_name": "x", "product_main_image": "",
		"flash_price": 1, "quantity": 1, "idempotency_key": "collide", "request_hash": "collide",
	}).Insert(); err != nil {
		t.Fatalf("seed colliding order: %v", err)
	}
	orig := generateOrderNo
	generateOrderNo = func() string { return "FS-COLLIDE" }
	defer func() { generateOrderNo = orig }()

	event := consumerEventOf(t, requestID)
	client := &fakeConsumer{batches: [][]mq.Message{{testMessage(t, event, 0, 0)}}}
	dlq := &fakePublisher{}
	c := newTestConsumer(t, client, dlq, s)

	if err := runConsumer(t, c); !errors.Is(err, errFakeConsumerExhausted) {
		t.Fatalf("run error=%v", err)
	}
	if len(dlqMessages(dlq)) != 1 {
		t.Fatalf("预算耗尽必须投递死信，got %d", len(dlqMessages(dlq)))
	}
	row := consumerLoadRow(t, requestID)
	if row.Status != requestStatusDead {
		t.Fatalf("status=%d want dead", row.Status)
	}
	if row.RetryCount <= maxConsumeRetryAttempts {
		t.Fatalf("retry_count=%d 必须超出预算 %d", row.RetryCount, maxConsumeRetryAttempts)
	}
	if client.committedCount() != 1 {
		t.Fatalf("死信 + dead 之后才提交位移，committed=%d", client.committedCount())
	}
	// Redis 补偿：isn't 无条件 INCR，而是按权威值收敛，标记必须被清除。
	if got, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID)); err != nil || got.String() != "10" {
		t.Fatalf("Redis remaining=%q want 10 (authoritative converge)", got.String())
	}
	if n, err := g.Redis().Exists(ctx, flashSaleBoughtKey(activityID, skuID, userID)); err != nil || n != 0 {
		t.Fatalf("一人一单标记应被清除，exists=%d err=%v", n, err)
	}
}

// TestConsumerCompensationFailureRecoverable 覆盖「MySQL 已提交、Redis 补偿失败」的可恢复路径：
// 补偿失败不阻塞消费（终态已提交），库存由对账扫描器按权威值收敛，且重复补偿幂等。
func TestConsumerCompensationFailureRecoverable(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()
	skuID := consumeTestSku(t, "kafka-compensate")
	activityID := consumeTestActivity(t, skuID)
	userID := int64(910013)
	const key = "kafka-compensate"
	requestID := consumerInsertRequest(t, userID, activityID, skuID, key, nil)

	if err := s.SyncCache(ctx, activityID); err != nil {
		t.Fatalf("sync cache: %v", err)
	}
	// 模拟闸门预扣 + 标记。
	if _, err := g.Redis().Do(ctx, "DECR", flashSaleStockKey(activityID, skuID)); err != nil {
		t.Fatalf("pre-deduct: %v", err)
	}
	if _, err := g.Redis().Do(ctx, "SET", flashSaleBoughtKey(activityID, skuID, userID), "1"); err != nil {
		t.Fatalf("set bought marker: %v", err)
	}
	if _, err := g.Redis().Do(ctx, "SET", flashSaleIdemKey(userID, key), requestHash(activityID, skuID)); err != nil {
		t.Fatalf("set idem marker: %v", err)
	}

	// 活动立即结束（仍在 grace 窗口内）→ 业务失败终态，触发补偿。
	if _, err := g.DB().Exec(ctx,
		"UPDATE flash_sale_activities SET end_time = DATE_SUB(NOW(), INTERVAL 1 SECOND) WHERE id = ?", activityID); err != nil {
		t.Fatalf("end activity: %v", err)
	}

	convergeRedisFailureHook = func(ctx context.Context, activityID, skuID int64) error {
		return errors.New("redis unavailable")
	}
	res, err := consumerConsume(t, s, consumerEventOf(t, requestID))
	convergeRedisFailureHook = nil
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if res.Outcome != ConsumeOutcomeFailed {
		t.Fatalf("outcome=%s want failed（补偿失败不影响 MySQL 终态，也不阻塞消费）", res.Outcome)
	}
	preDeducted, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID))
	if err != nil {
		t.Fatalf("read remaining: %v", err)
	}
	if preDeducted.String() == "10" {
		t.Fatalf("前置条件错误：预扣未发生，remaining=%s", preDeducted.String())
	}

	// 对账扫描器按权威值收敛（total 10 - sold 0 - inflight 0 = 10）。
	if _, err := s.ReconcileCache(ctx, 10); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got, _ := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID)); got.String() != "10" {
		t.Fatalf("对账后 Redis remaining=%q want 10", got.String())
	}
	// 再次补偿必须幂等：不重复回补、不越界。
	s.convergeStockAndMarkers(ctx, activityID, skuID, userID, key)
	if got, _ := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID)); got.String() != "10" {
		t.Fatalf("重复补偿必须幂等，remaining=%q want 10", got.String())
	}
}

// TestConsumerDuplicateDeliveryDoesNotTouchRedis 覆盖「重复消息不重复执行已完成的副作用」：
// 已 success 的请求再次投递时不得改动 Redis 库存与标记。
func TestConsumerDuplicateDeliveryDoesNotTouchRedis(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	s := New()
	skuID := consumeTestSku(t, "kafka-dup-redis")
	activityID := consumeTestActivity(t, skuID)
	requestID := consumerInsertRequest(t, 910014, activityID, skuID, "kafka-dup-redis", nil)
	event := consumerEventOf(t, requestID)

	if res, err := consumerConsume(t, s, event); err != nil || res.Outcome != ConsumeOutcomeSuccess {
		t.Fatalf("first consume: %+v %v", res, err)
	}
	s.convergeStockAndMarkers(ctx, activityID, skuID, 910014, "kafka-dup-redis")
	before, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID))
	if err != nil {
		t.Fatalf("read remaining: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := consumerConsume(t, s, event); err != nil {
			t.Fatalf("duplicate consume: %v", err)
		}
	}
	after, err := g.Redis().Get(ctx, flashSaleStockKey(activityID, skuID))
	if err != nil {
		t.Fatalf("read remaining: %v", err)
	}
	if before.String() != after.String() {
		t.Fatalf("重复投递不得改动 Redis 库存：before=%s after=%s", before.String(), after.String())
	}
}

// ---------------------------------------------------------------------------
// 并发 / Race 用例（纳入 scripts/test-race.sh）
// ---------------------------------------------------------------------------

// TestConsumerRaceOversellGuard 覆盖并发消费者对同一活动×SKU 的库存竞争：
// 条件扣减 + 行锁必须保证成功订单数恰好等于库存，绝不超过。
// 正确性不得依赖 Kafka 分区顺序（同一 SKU 会落在同一分区，但并发消费者仍可能交错）。
func TestConsumerRaceOversellGuard(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	skuID := consumeTestSku(t, "race-oversell")
	activityID := consumeTestActivity(t, skuID)

	const stock = 5
	const buyers = 10
	if _, err := g.DB().Exec(ctx,
		"UPDATE flash_sale_activity_skus SET total_stock = ?, sold = 0 WHERE activity_id = ? AND sku_id = ?",
		stock, activityID, skuID); err != nil {
		t.Fatalf("resize stock: %v", err)
	}

	events := make([]*RequestedEvent, 0, buyers)
	for i := 0; i < buyers; i++ {
		id := consumerInsertRequest(t, int64(960000+i), activityID, skuID, fmt.Sprintf("race-oversell-%d", i), nil)
		events = append(events, consumerEventOf(t, id))
	}

	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		success atomic.Int32
		failed  atomic.Int32
		other   atomic.Int32
	)
	errs := make(chan error, buyers)
	for _, event := range events {
		wg.Add(1)
		go func(e *RequestedEvent) {
			defer wg.Done()
			<-start
			res, err := consumerConsume(t, New(), e)
			if err != nil {
				errs <- err
				return
			}
			switch res.Outcome {
			case ConsumeOutcomeSuccess:
				success.Add(1)
			case ConsumeOutcomeFailed:
				failed.Add(1)
			default:
				other.Add(1)
			}
		}(event)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent consume: %v", err)
	}
	if success.Load() != stock {
		t.Fatalf("成功落单 %d 单 want %d（不得超卖）", success.Load(), stock)
	}
	if failed.Load() != buyers-stock {
		t.Fatalf("库存不足失败 %d 单 want %d", failed.Load(), buyers-stock)
	}
	if other.Load() != 0 {
		t.Fatalf("不应出现其它结论 %d 次", other.Load())
	}
	if n, _ := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("activity_id", activityID).Count(); n != stock {
		t.Fatalf("订单数=%d want %d", n, stock)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != stock {
		t.Fatalf("sold=%d want %d", sold, stock)
	}
}

// TestConsumerRaceKafkaAndScannerSameRequest 覆盖「Kafka 消费与 MySQL Scanner 并发接管同一请求」
// （B4 切换期的真实风险）：两条入口最终进入同一套事务核心，只允许一次业务效果。
func TestConsumerRaceKafkaAndScannerSameRequest(t *testing.T) {
	setupConsumeTest(t)
	ctx := context.Background()
	skuID := consumeTestSku(t, "race-mixed")
	activityID := consumeTestActivity(t, skuID)
	requestID := consumerInsertRequest(t, 970001, activityID, skuID, "race-mixed", nil)
	event := consumerEventOf(t, requestID)

	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
	)
	errs := make(chan error, 6)
	// 一条 Kafka 消息被重复投递 3 次。
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := consumerConsume(t, New(), event); err != nil {
				errs <- err
			}
		}()
	}
	// 同时 MySQL Scanner 也在扫描同一张表。
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := New().ConsumeQueued(ctx, 100); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent consume: %v", err)
	}

	req := consumerLoadRow(t, requestID)
	if req.Status != requestStatusSuccess || req.FlashOrderId == nil {
		t.Fatalf("并发后 status=%d order=%v want success", req.Status, req.FlashOrderId)
	}
	if n, _ := g.DB().Model("flash_sale_orders").Ctx(ctx).Where("activity_id", activityID).Count(); n != 1 {
		t.Fatalf("订单数=%d want 1", n)
	}
	if _, sold := consumeBindingStock(t, activityID, skuID); sold != 1 {
		t.Fatalf("sold=%d want 1", sold)
	}
}

// TestConsumerPollErrorReleasesRebalance 覆盖 BlockRebalanceOnPoll 的计数泄漏：
// Poll 返回错误时业务层拿不到消息，若不同步 AllowRebalance，消费组会在 Broker 恢复后
// 永久卡在再平衡等待（真实 Broker 停机/恢复场景已复现，见 B4 e2e）。
func TestConsumerPollErrorReleasesRebalance(t *testing.T) {
	client := &fakeConsumer{pollErr: errors.New("broker 暂时不可用")}
	c := newTestConsumer(t, client, &fakePublisher{}, &scriptedConsumerService{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if client.allowRebalanceCount() > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	select {
	case <-done:
		// fake 在脚本批次耗尽时返回包装 ErrClosed 的错误，Run 据此退出属预期；
		// 本用例关心的是错误路径上的 AllowRebalance 与位移提交。
	case <-time.After(3 * time.Second):
		t.Fatal("消费循环未在 ctx 取消后退出")
	}

	if got := client.allowRebalanceCount(); got == 0 {
		t.Fatal("Poll 错误路径必须 AllowRebalance，否则再平衡会永久阻塞")
	}
	if client.committedCount() != 0 {
		t.Fatal("拉取失败不得提交任何位移")
	}
}
