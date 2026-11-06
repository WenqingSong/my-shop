package flashsale

// 秒杀 B2 Outbox Publisher 测试。
//
// 分两类：
//   - 纯内存单元测试：退避计算、租约令牌、参数校验、事件构造（不需要 MySQL/Redis）；
//   - 真实 MySQL 集成测试：领取/租约/标记/退避/并发/竞态，使用可控的 fake Publisher 替代 Kafka。
//
// 真实 Kafka 的端到端验证见 outbox_kafka_test.go（需显式提供 Broker 才会执行）。

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
	"cnb.cool/go-cloud-devops/my-shop/internal/mq"
)

// ---------------------------------------------------------------------------
// 可控 fake Publisher
// ---------------------------------------------------------------------------

// fakePublisher 是可编程的发布器：记录每次批次，并按测试意图返回成功/失败/未知结果。
type fakePublisher struct {
	mu      sync.Mutex
	batches [][]mq.Message

	// publish 自定义单次调用行为；为 nil 时所有消息成功（Partition/Offset 由下标派生）。
	publish func(ctx context.Context, msgs []mq.Message) ([]mq.PublishResult, error)
	// entered 非 nil 时，每次进入 Publish 后发送一次信号（用于并发/竞态的确定性同步）。
	entered chan<- struct{}
	// gate 非 nil 时，Publish 在返回前等待 gate 关闭或 ctx 结束（用于卡住租约持有者）。
	gate <-chan struct{}
}

func (f *fakePublisher) Publish(ctx context.Context, msgs ...mq.Message) ([]mq.PublishResult, error) {
	f.mu.Lock()
	copied := make([]mq.Message, len(msgs))
	copy(copied, msgs)
	f.batches = append(f.batches, copied)
	publish := f.publish
	entered := f.entered
	gate := f.gate
	f.mu.Unlock()

	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, fmt.Errorf("fake publisher: %w", ctx.Err())
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("fake publisher: %w", ctxErr)
	}
	if publish != nil {
		return publish(ctx, copied)
	}
	return fakeSuccessResults(copied, 1, 100), nil
}

func (f *fakePublisher) Close(ctx context.Context) error { return nil }

// batchCount 返回已发生的发布批次数量。
func (f *fakePublisher) batchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.batches)
}

// lastBatch 返回最近一次批次的消息（无批次时返回 nil）。
func (f *fakePublisher) lastBatch() []mq.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.batches) == 0 {
		return nil
	}
	return f.batches[len(f.batches)-1]
}

// allMessages 返回全部批次的扁平消息列表。
func (f *fakePublisher) allMessages() []mq.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []mq.Message
	for _, b := range f.batches {
		out = append(out, b...)
	}
	return out
}

// fakeSuccessResults 构造逐条成功的发布结果。
func fakeSuccessResults(msgs []mq.Message, partition int32, offsetBase int64) []mq.PublishResult {
	results := make([]mq.PublishResult, len(msgs))
	for i := range msgs {
		results[i] = mq.PublishResult{Message: msgs[i], Partition: partition, Offset: offsetBase + int64(i)}
	}
	return results
}

// defaultTopicPublisher 模拟「底层 Producer 自带默认 Topic」的发布器：
// Message.Topic 为空时回退到 defaultTopic，与 internal/mq/kafka.Producer 的行为一致。
// 用于验证 OutboxPublisher 构造参数与底层默认主题不一致时，实际路由仍由 Outbox 决定。
type defaultTopicPublisher struct {
	mu           sync.Mutex
	defaultTopic string
	effective    []string
}

func (d *defaultTopicPublisher) Publish(_ context.Context, msgs ...mq.Message) ([]mq.PublishResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range msgs {
		topic := msgs[i].Topic
		if topic == "" {
			topic = d.defaultTopic
		}
		msgs[i].Topic = topic
		d.effective = append(d.effective, topic)
	}
	return fakeSuccessResults(msgs, 1, 1), nil
}

func (d *defaultTopicPublisher) Close(context.Context) error { return nil }

// effectiveTopics 返回每次实际投递使用的主题（已按默认值回落后的结果）。
func (d *defaultTopicPublisher) effectiveTopics() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.effective))
	copy(out, d.effective)
	return out
}

// fakeFailureResults 构造逐条失败（或指定下标失败）的发布结果。
func fakeFailureResults(msgs []mq.Message, failIdx map[int]bool, err error) []mq.PublishResult {
	results := make([]mq.PublishResult, len(msgs))
	for i := range msgs {
		if failIdx == nil || failIdx[i] {
			results[i] = mq.PublishResult{Message: msgs[i], Err: err}
		} else {
			results[i] = mq.PublishResult{Message: msgs[i], Partition: 2, Offset: int64(500 + i)}
		}
	}
	return results
}

// ---------------------------------------------------------------------------
// 测试辅助
// ---------------------------------------------------------------------------

// outboxTestTopic 是测试中构造 OutboxPublisher 时显式指定的目标主题。
// 断言消息 Topic 时必须引用它，而不是底层 Publisher 的默认主题。
const outboxTestTopic = "surgecart.test.flashsale.requested.v1"

// outboxTestOptions 返回测试用发布参数：批量小、租约短（但仍满足安全余量），便于构造过期场景。
func outboxTestOptions() OutboxOptions {
	return OutboxOptions{
		BatchSize:      10,
		Lease:          15 * time.Second,
		PublishTimeout: 3 * time.Second,
		BackoffBase:    time.Second,
		BackoffMax:     8 * time.Second,
	}
}

// setupOutboxTest 建立 Outbox 测试的隔离环境：migration + Bootstrap（DB/Redis 配置）+ 清空请求域表。
//
// 只清理本批次真正使用的表（请求与审计），不删除 skus/inventories 等其它域的数据：
// 那些表存在跨包共享的外键关系（inventories → skus ON DELETE RESTRICT），
// 在其它包的测试留下数据后清理会失败，属于与 Outbox 无关的噪声。
func setupOutboxTest(t *testing.T) {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", "test-secret-0123456789-0123456789-0123456789")
	t.Setenv("ADMIN_SUPER_PASSWORD", "test-admin-password-123")

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for _, table := range []string{"flash_sale_request_audits", "flash_sale_order_requests"} {
		if _, err := g.DB().Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
}

// outboxInsertRequest 插入一条秒杀请求，返回 id。
// key 是幂等键（同一测试内必须唯一）；overrides 覆盖默认列值。
func outboxInsertRequest(t *testing.T, key string, overrides g.Map) int64 {
	t.Helper()
	data := g.Map{
		"user_id":         810001,
		"activity_id":     710001,
		"sku_id":          610001,
		"idempotency_key": key,
		"request_hash":    requestHash(710001, 610001),
		"status":          requestStatusQueued,
	}
	for k, v := range overrides {
		data[k] = v
	}
	id, err := g.DB().Model("flash_sale_order_requests").Ctx(context.Background()).Data(data).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert request %s: %v", key, err)
	}
	return id
}

// outboxRow 是发布相关列的完整快照，用于断言状态迁移。
type outboxRow struct {
	Id                 int64       `json:"id"`
	Status             int         `json:"status"`
	RetryCount         int         `json:"retry_count"`
	PublishedAt        *gtime.Time `json:"published_at"`
	PublishClaimToken  *string     `json:"publish_claim_token"`
	PublishLeaseUntil  *gtime.Time `json:"publish_lease_until"`
	PublishAttempts    int         `json:"publish_attempts"`
	NextPublishAt      *gtime.Time `json:"next_publish_at"`
	LastPublishError   *string     `json:"last_publish_error"`
	PublishedPartition *int        `json:"published_partition"`
	PublishedOffset    *int64      `json:"published_offset"`
	CreatedAt          *gtime.Time `json:"created_at"`
}

// outboxLoad 读取指定请求的发布状态。
func outboxLoad(t *testing.T, id int64) *outboxRow {
	t.Helper()
	var rows []*outboxRow
	if err := g.DB().Model("flash_sale_order_requests").Ctx(context.Background()).
		Where("id", id).Scan(&rows); err != nil {
		t.Fatalf("load request %d: %v", id, err)
	}
	if len(rows) == 0 {
		t.Fatalf("request %d not found", id)
	}
	return rows[0]
}

// outboxExpireLease 把指定请求的租约改成已过期（模拟进程崩溃/超时后的回收场景）。
func outboxExpireLease(t *testing.T, id int64) {
	t.Helper()
	if _, err := g.DB().Exec(context.Background(),
		"UPDATE flash_sale_order_requests SET publish_lease_until = DATE_SUB(NOW(6), INTERVAL 1 SECOND) WHERE id = ?", id); err != nil {
		t.Fatalf("expire lease of %d: %v", id, err)
	}
}

// outboxDBNow 返回数据库当前时间（用于比较退避时间）。
func outboxDBNow(t *testing.T) time.Time {
	t.Helper()
	v, err := g.DB().GetValue(context.Background(), "SELECT NOW(6)")
	if err != nil {
		t.Fatalf("select now: %v", err)
	}
	parsed, err := gtime.StrToTime(v.String())
	if err != nil {
		t.Fatalf("parse now %q: %v", v.String(), err)
	}
	return parsed.Time
}

// newTestOutboxPublisher 构造被测 Publisher（Topic 固定为 outboxTestTopic）。
func newTestOutboxPublisher(t *testing.T, pub mq.Publisher, opts OutboxOptions) *OutboxPublisher {
	t.Helper()
	return newTestOutboxPublisherWithTopic(t, pub, outboxTestTopic, opts)
}

// newTestOutboxPublisherWithTopic 构造指定 Topic 的被测 Publisher。
// 真实 Kafka 用例必须显式传入唯一主题：Topic 参数已生效（F-02），
// 不能再依赖 Producer 的默认 Topic 兜底。
func newTestOutboxPublisherWithTopic(t *testing.T, pub mq.Publisher, topic string, opts OutboxOptions) *OutboxPublisher {
	t.Helper()
	p, err := NewOutboxPublisher(pub, topic, opts)
	if err != nil {
		t.Fatalf("new outbox publisher: %v", err)
	}
	return p
}

// ---------------------------------------------------------------------------
// 纯内存单元测试
// ---------------------------------------------------------------------------

// TestOutboxPublishBackoff 校验指数退避的单调性与上限（不允许无界增长或紧密循环）。
func TestOutboxPublishBackoff(t *testing.T) {
	base, max := 2*time.Second, 5*time.Minute
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, base}, {1, base}, {2, 4 * time.Second}, {3, 8 * time.Second},
		{4, 16 * time.Second}, {5, 32 * time.Second}, {8, 256 * time.Second},
		{9, max}, {10, max}, {1000, max},
	}
	prev := time.Duration(0)
	for _, c := range cases {
		got := publishBackoff(c.attempts, base, max)
		if got != c.want {
			t.Errorf("publishBackoff(%d)=%s want %s", c.attempts, got, c.want)
		}
		if got > max {
			t.Errorf("publishBackoff(%d)=%s exceeds max %s", c.attempts, got, max)
		}
		if c.attempts >= 1 && got < prev {
			t.Errorf("publishBackoff(%d)=%s should not shrink from %s", c.attempts, got, prev)
		}
		prev = got
	}
	// 上限小于基础值时仍不得越界。
	if got := publishBackoff(5, 10*time.Second, time.Second); got != 10*time.Second {
		t.Errorf("publishBackoff with max<base=%s want base 10s", got)
	}
}

// TestOutboxClaimToken 校验令牌长度/编码与随机性（同一轮不能与旧轮相同）。
func TestOutboxClaimToken(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		token, err := newClaimToken()
		if err != nil {
			t.Fatalf("new claim token: %v", err)
		}
		if len(token) != outboxClaimTokenBytes*2 {
			t.Fatalf("token %q length=%d want %d", token, len(token), outboxClaimTokenBytes*2)
		}
		if strings.ContainsAny(token, "0123456789abcdef") == false || strings.ToLower(token) != token {
			t.Fatalf("token %q should be lowercase hex", token)
		}
		if seen[token] {
			t.Fatalf("duplicate claim token generated: %q", token)
		}
		seen[token] = true
		if digest := claimTokenDigest(token); len(digest) != 8 {
			t.Fatalf("unexpected digest %q for token %q", digest, token)
		}
	}
	if claimTokenDigest("") != "" {
		t.Fatalf("empty token digest should be empty")
	}
}

// TestOutboxOptionsValidation 校验租约/发布超时/退避区间的自洽性约束。
func TestOutboxOptionsValidation(t *testing.T) {
	if _, err := NewOutboxPublisher(nil, "t", outboxTestOptions()); err == nil {
		t.Fatalf("nil publisher should be rejected")
	}
	if _, err := NewOutboxPublisher(&fakePublisher{}, "", outboxTestOptions()); err == nil {
		t.Fatalf("empty topic should be rejected")
	}
	// 租约不足以覆盖发布超时 + 安全余量 → 装配期拒绝。
	bad := outboxTestOptions()
	bad.Lease = bad.PublishTimeout + time.Second
	if _, err := NewOutboxPublisher(&fakePublisher{}, "t", bad); err == nil {
		t.Fatalf("lease shorter than publish timeout + margin should be rejected")
	}
	// 退避上限小于基础值 → 拒绝。
	bad = outboxTestOptions()
	bad.BackoffMax = time.Millisecond
	if _, err := NewOutboxPublisher(&fakePublisher{}, "t", bad); err == nil {
		t.Fatalf("backoff max smaller than base should be rejected")
	}
	// 零值 options 使用默认值并可构造。
	if _, err := NewOutboxPublisher(&fakePublisher{}, "t", OutboxOptions{}); err != nil {
		t.Fatalf("zero options should use defaults: %v", err)
	}
}

// TestOutboxMessageContract 校验消息字段全部来自持久化事实，且 topic/key/header 稳定。
func TestOutboxMessageContract(t *testing.T) {
	created := time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local)
	claim := &outboxClaim{
		Id: 42, UserId: 7, ActivityId: 9, SkuId: 11,
		IdempotencyKey: "idem-42",
		RequestHash:    requestHash(9, 11),
		CreatedAt:      gtime.NewFromTime(created),
	}
	msg, err := buildRequestedMessage(outboxTestTopic, claim)
	if err != nil {
		t.Fatalf("build message: %v", err)
	}
	// F-02：目标主题必须由 Outbox 构造参数显式写入，不能留空交给 Producer 默认值。
	if msg.Topic != outboxTestTopic {
		t.Fatalf("message topic=%q want %q", msg.Topic, outboxTestTopic)
	}
	if got, want := string(msg.Key), "9:11"; got != want {
		t.Fatalf("key=%q want %q", got, want)
	}
	// F-08：分区键是 <activity_id>:<sku_id>，绝不是 request_id。
	if string(msg.Key) == strconv.FormatInt(claim.Id, 10) {
		t.Fatalf("message key must be <activity_id>:<sku_id>, not request_id: %q", msg.Key)
	}
	// 传输层时间戳必须留空（由驱动填充发送时间），事件时间只在消息体内表达。
	if !msg.Timestamp.IsZero() {
		t.Fatalf("message timestamp should be zero (driver-filled), got %s", msg.Timestamp)
	}
	event, err := DecodeRequestedEvent(msg.Value)
	if err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.RequestID != 42 || event.UserID != 7 || event.ActivityID != 9 || event.SkuID != 11 ||
		event.IdempotencyKey != "idem-42" || event.RequestHash != claim.RequestHash {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.EventID != RequestedEventID(42) || event.EventType != RequestedEventType ||
		event.SchemaVersion != RequestedSchemaVersion {
		t.Fatalf("unexpected contract fields: %+v", event)
	}
	if !event.OccurredAt.Equal(created) {
		t.Fatalf("occurred_at=%s want %s", event.OccurredAt, created)
	}
	for name, want := range map[string]string{
		"event_id":       RequestedEventID(42),
		"event_type":     RequestedEventType,
		"schema_version": "1",
	} {
		got, ok := msg.HeaderValue(name)
		if !ok || string(got) != want {
			t.Fatalf("header %s=%q ok=%t want %q", name, got, ok, want)
		}
	}

	// 缺少 created_at 时必须失败，而不是编造时间。
	claim.CreatedAt = nil
	if _, err := buildRequestedMessage(outboxTestTopic, claim); err == nil {
		t.Fatalf("missing created_at should fail")
	}
	// 缺少目标 Topic 时必须失败：绝不能静默回退到底层 Producer 的默认主题。
	claim.CreatedAt = gtime.NewFromTime(created)
	if _, err := buildRequestedMessage("", claim); err == nil {
		t.Fatalf("empty topic should fail")
	}
}

// TestOutboxMessageKeyStrategy 冻结 Kafka 分区键策略（F-08）：
// 同一 activity+sku 的请求共享同一 Key，不同 SKU 不共享；Key 与 request_id 无关。
// 这保证同一活动 SKU 的消息进入同一分区（分区内有序、不争抢同一 SKU 库存行），
// 同时明确 Key 不是 request_id，不能据此推断 MySQL id 顺序。
func TestOutboxMessageKeyStrategy(t *testing.T) {
	created := gtime.NewFromTime(time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local))
	newClaim := func(id, activityID, skuID int64) *outboxClaim {
		return &outboxClaim{
			Id: id, UserId: 1, ActivityId: activityID, SkuId: skuID,
			IdempotencyKey: fmt.Sprintf("idem-%d", id),
			RequestHash:    requestHash(activityID, skuID),
			CreatedAt:      created,
		}
	}

	first, err := buildRequestedMessage(outboxTestTopic, newClaim(101, 9, 11))
	if err != nil {
		t.Fatalf("build first message: %v", err)
	}
	second, err := buildRequestedMessage(outboxTestTopic, newClaim(102, 9, 11))
	if err != nil {
		t.Fatalf("build second message: %v", err)
	}
	otherSku, err := buildRequestedMessage(outboxTestTopic, newClaim(103, 9, 12))
	if err != nil {
		t.Fatalf("build other-sku message: %v", err)
	}

	if got, want := string(first.Key), "9:11"; got != want {
		t.Fatalf("key=%q want %q", got, want)
	}
	if string(second.Key) != string(first.Key) {
		t.Fatalf("same activity/sku must share partition key: %q vs %q", first.Key, second.Key)
	}
	if string(otherSku.Key) == string(first.Key) {
		t.Fatalf("different sku must not share partition key: %q", otherSku.Key)
	}
	if string(first.Key) == strconv.FormatInt(101, 10) || string(second.Key) == strconv.FormatInt(102, 10) {
		t.Fatalf("partition key must not be request_id: %q / %q", first.Key, second.Key)
	}
	// Key 与 event_id 属于不同维度：前者聚合活动 SKU，后者标识单条逻辑事件。
	event, err := DecodeRequestedEvent(first.Value)
	if err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if string(first.Key) == event.EventID {
		t.Fatalf("partition key must not reuse event_id: %q", first.Key)
	}
}

// ---------------------------------------------------------------------------
// MySQL 集成测试（fake Publisher）
// ---------------------------------------------------------------------------

// TestOutboxPublishSuccess 覆盖正常发布：ACK 后才写 published_at，并清理租约/退避。
func TestOutboxPublishSuccess(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()
	fake := &fakePublisher{publish: func(_ context.Context, msgs []mq.Message) ([]mq.PublishResult, error) {
		return fakeSuccessResults(msgs, 3, 700), nil
	}}
	pub := newTestOutboxPublisher(t, fake, outboxTestOptions())
	id := outboxInsertRequest(t, "ok-1", g.Map{"user_id": 811001})

	stats, err := pub.PublishBatch(ctx, 10)
	if err != nil {
		t.Fatalf("publish batch: %v", err)
	}
	if stats.Claimed != 1 || stats.Published != 1 || stats.Failed != 0 || stats.Retried != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	row := outboxLoad(t, id)
	if row.PublishedAt == nil {
		t.Fatalf("published_at should be set after broker ack")
	}
	if row.PublishClaimToken != nil || row.PublishLeaseUntil != nil {
		t.Fatalf("lease should be cleared after publish: token=%v lease=%v", row.PublishClaimToken, row.PublishLeaseUntil)
	}
	if row.NextPublishAt != nil || row.LastPublishError != nil {
		t.Fatalf("backoff fields should be cleared: next=%v err=%v", row.NextPublishAt, row.LastPublishError)
	}
	if row.PublishAttempts != 1 {
		t.Fatalf("publish_attempts=%d want 1", row.PublishAttempts)
	}
	if row.PublishedPartition == nil || *row.PublishedPartition != 3 || row.PublishedOffset == nil || *row.PublishedOffset != 700 {
		t.Fatalf("published partition/offset mismatch: %v/%v", row.PublishedPartition, row.PublishedOffset)
	}
	if row.Status != requestStatusQueued || row.RetryCount != 0 {
		t.Fatalf("publisher must not touch business status: status=%d retry=%d", row.Status, row.RetryCount)
	}
	batch := fake.lastBatch()
	if len(batch) != 1 {
		t.Fatalf("expected exactly one published message, got %d", len(batch))
	}
	// F-02：Fake Publisher 捕获到的消息必须携带 Outbox 构造时指定的 Topic。
	if batch[0].Topic != outboxTestTopic {
		t.Fatalf("captured message topic=%q want %q", batch[0].Topic, outboxTestTopic)
	}
}

// TestOutboxExplicitTopicOverridesProducerDefault 覆盖 F-02 的核心场景：
// Outbox 构造参数与底层 Producer 默认 Topic 不一致时，消息仍必须投递到 Outbox 指定的 Topic。
// 若构造参数失效，Message.Topic 会留空并回退到 Producer 默认值——本用例会直接失败。
func TestOutboxExplicitTopicOverridesProducerDefault(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()

	const producerDefaultTopic = "surgecart.test.flashsale.producer-default.v1"
	routing := &defaultTopicPublisher{defaultTopic: producerDefaultTopic}
	pub, err := NewOutboxPublisher(routing, outboxTestTopic, outboxTestOptions())
	if err != nil {
		t.Fatalf("new outbox publisher: %v", err)
	}
	id := outboxInsertRequest(t, "explicit-topic", g.Map{"user_id": 811002})

	stats, err := pub.PublishBatch(ctx, 10)
	if err != nil {
		t.Fatalf("publish batch: %v", err)
	}
	if stats.Published != 1 {
		t.Fatalf("expected one published message, got %+v", stats)
	}
	topics := routing.effectiveTopics()
	if len(topics) != 1 {
		t.Fatalf("expected one effective topic, got %v", topics)
	}
	if topics[0] != outboxTestTopic {
		t.Fatalf("effective topic=%q want outbox topic %q (producer default %q must not win)",
			topics[0], outboxTestTopic, producerDefaultTopic)
	}
	if outboxLoad(t, id).PublishedAt == nil {
		t.Fatalf("record should be marked published after broker ack")
	}
}

// TestOutboxPublishBrokerError 覆盖 Broker 明确失败：不写 published_at，写退避与错误原因。
func TestOutboxPublishBrokerError(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()
	brokerErr := errors.New("broker: not enough replicas")
	fake := &fakePublisher{publish: func(_ context.Context, msgs []mq.Message) ([]mq.PublishResult, error) {
		return fakeFailureResults(msgs, nil, brokerErr), brokerErr
	}}
	pub := newTestOutboxPublisher(t, fake, outboxTestOptions())
	id := outboxInsertRequest(t, "broker-err", g.Map{"user_id": 811002})

	before := outboxDBNow(t)
	stats, err := pub.PublishBatch(ctx, 10)
	if err == nil {
		t.Fatalf("expected publish failure to be reported")
	}
	if stats.Published != 0 || stats.Failed != 1 || stats.Retried != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	row := outboxLoad(t, id)
	if row.PublishedAt != nil {
		t.Fatalf("published_at must stay NULL on broker failure")
	}
	if row.PublishClaimToken != nil || row.PublishLeaseUntil != nil {
		t.Fatalf("lease should be released on failure: token=%v lease=%v", row.PublishClaimToken, row.PublishLeaseUntil)
	}
	if row.NextPublishAt == nil || !row.NextPublishAt.Time.After(before) {
		t.Fatalf("next_publish_at should be in the future: %v (now %v)", row.NextPublishAt, before)
	}
	if row.LastPublishError == nil || !strings.Contains(*row.LastPublishError, "not enough replicas") {
		t.Fatalf("last_publish_error should record cause, got %v", row.LastPublishError)
	}
	if row.Status != requestStatusQueued {
		t.Fatalf("status=%d want queued (publish failure is not a business failure)", row.Status)
	}
}

// TestOutboxPublishPartialSuccess 覆盖部分成功：逐条标记，不得整批成功化或丢失失败项。
func TestOutboxPublishPartialSuccess(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()
	fake := &fakePublisher{publish: func(_ context.Context, msgs []mq.Message) ([]mq.PublishResult, error) {
		return fakeFailureResults(msgs, map[int]bool{1: true}, errors.New("record too large")), errors.New("1 条未确认")
	}}
	pub := newTestOutboxPublisher(t, fake, outboxTestOptions())
	ids := []int64{
		outboxInsertRequest(t, "partial-1", g.Map{"user_id": 811003}),
		outboxInsertRequest(t, "partial-2", g.Map{"user_id": 811004}),
		outboxInsertRequest(t, "partial-3", g.Map{"user_id": 811005}),
	}

	if _, err := pub.PublishBatch(ctx, 10); err == nil {
		t.Fatalf("expected partial failure to be reported")
	}
	// 第 1、3 条已发布，第 2 条保持未发布并退避。
	for _, i := range []int{0, 2} {
		row := outboxLoad(t, ids[i])
		if row.PublishedAt == nil {
			t.Fatalf("request %d should be published", ids[i])
		}
	}
	failed := outboxLoad(t, ids[1])
	if failed.PublishedAt != nil {
		t.Fatalf("request %d must not be marked published", ids[1])
	}
	if failed.NextPublishAt == nil {
		t.Fatalf("request %d should be scheduled for retry", ids[1])
	}
	if failed.LastPublishError == nil || !strings.Contains(*failed.LastPublishError, "record too large") {
		t.Fatalf("failed request error mismatch: %v", failed.LastPublishError)
	}
}

// TestOutboxPublishUnknownResult 覆盖整批结果未知：按可能已发送处理，保留重新发布能力。
func TestOutboxPublishUnknownResult(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()
	fake := &fakePublisher{publish: func(context.Context, []mq.Message) ([]mq.PublishResult, error) {
		return nil, errors.New("client closed")
	}}
	pub := newTestOutboxPublisher(t, fake, outboxTestOptions())
	id := outboxInsertRequest(t, "unknown-1", g.Map{"user_id": 811006})

	stats, err := pub.PublishBatch(ctx, 10)
	if err == nil {
		t.Fatalf("unknown result should be reported as an error")
	}
	if stats.Failed != 1 || stats.Published != 0 || stats.Retried != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	row := outboxLoad(t, id)
	if row.PublishedAt != nil {
		t.Fatalf("unknown result must not be marked published")
	}
	if row.NextPublishAt == nil || row.PublishClaimToken != nil {
		t.Fatalf("unknown result should schedule retry and release lease: next=%v token=%v", row.NextPublishAt, row.PublishClaimToken)
	}

	// 退避到期后可重新发布（重复投递允许，event_id 稳定）。
	if _, err := g.DB().Exec(ctx,
		"UPDATE flash_sale_order_requests SET next_publish_at = NULL WHERE id = ?", id); err != nil {
		t.Fatalf("clear backoff: %v", err)
	}
	fake.publish = func(_ context.Context, msgs []mq.Message) ([]mq.PublishResult, error) {
		return fakeSuccessResults(msgs, 0, 1), nil
	}
	if _, err := pub.PublishBatch(ctx, 10); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if outboxLoad(t, id).PublishedAt == nil {
		t.Fatalf("record should be publishable again after backoff")
	}
}

// TestOutboxPublishContextCanceled 覆盖上下文取消：不标记发布，只安排有界退避。
func TestOutboxPublishContextCanceled(t *testing.T) {
	setupOutboxTest(t)
	fake := &fakePublisher{publish: func(ctx context.Context, _ []mq.Message) ([]mq.PublishResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	opts := outboxTestOptions()
	opts.PublishTimeout = 100 * time.Millisecond
	pub := newTestOutboxPublisher(t, fake, opts)
	id := outboxInsertRequest(t, "cancel-1", g.Map{"user_id": 811007})

	stats, err := pub.PublishBatch(context.Background(), 10)
	if err == nil {
		t.Fatalf("timeout should be reported")
	}
	if stats.Failed != 1 || stats.Published != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	row := outboxLoad(t, id)
	if row.PublishedAt != nil || row.NextPublishAt == nil {
		t.Fatalf("canceled publish must stay unpublished with backoff: published=%v next=%v", row.PublishedAt, row.NextPublishAt)
	}
}

// TestOutboxRepublishKeepsEventID 覆盖重复发布：event_id 与 occurred_at 必须稳定不变。
func TestOutboxRepublishKeepsEventID(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()
	fake := &fakePublisher{}
	pub := newTestOutboxPublisher(t, fake, outboxTestOptions())
	id := outboxInsertRequest(t, "republish-1", g.Map{"user_id": 811008})

	if _, err := pub.PublishBatch(ctx, 10); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	first := fake.lastBatch()
	// 模拟「ACK 后标记丢失/租约过期」：清空发布状态但保留业务行。
	if _, err := g.DB().Exec(ctx,
		"UPDATE flash_sale_order_requests SET published_at = NULL WHERE id = ?", id); err != nil {
		t.Fatalf("reset published_at: %v", err)
	}
	if _, err := pub.PublishBatch(ctx, 10); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	second := fake.lastBatch()
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected 1 message per publish, got %d/%d", len(first), len(second))
	}
	e1, err := DecodeRequestedEvent(first[0].Value)
	if err != nil {
		t.Fatalf("decode first: %v", err)
	}
	e2, err := DecodeRequestedEvent(second[0].Value)
	if err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if e1.EventID != e2.EventID || e1.EventID != RequestedEventID(id) {
		t.Fatalf("event_id must be stable: %q vs %q", e1.EventID, e2.EventID)
	}
	if !e1.OccurredAt.Equal(e2.OccurredAt) {
		t.Fatalf("occurred_at must be stable: %s vs %s", e1.OccurredAt, e2.OccurredAt)
	}
	if string(first[0].Key) != string(second[0].Key) {
		t.Fatalf("message key must be stable")
	}
}

// TestOutboxSkipsNotDueAndNonQueued 覆盖领取过滤：未到期、终态、已发布的记录都不得发送。
func TestOutboxSkipsNotDueAndNonQueued(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()
	fake := &fakePublisher{}
	pub := newTestOutboxPublisher(t, fake, outboxTestOptions())

	dueID := outboxInsertRequest(t, "skip-due", g.Map{"user_id": 811009})
	notDueID := outboxInsertRequest(t, "skip-not-due", g.Map{
		"user_id":         811010,
		"next_publish_at": gdbNowPlus(t, time.Hour),
	})
	publishedID := outboxInsertRequest(t, "skip-published", g.Map{
		"user_id":      811011,
		"published_at": gdbNowPlus(t, -time.Minute),
	})
	successID := outboxInsertRequest(t, "skip-success", g.Map{"user_id": 811012, "status": requestStatusSuccess})
	failedID := outboxInsertRequest(t, "skip-failed", g.Map{"user_id": 811013, "status": requestStatusFailed})
	deadID := outboxInsertRequest(t, "skip-dead", g.Map{"user_id": 811014, "status": requestStatusDead})
	leasedID := outboxInsertRequest(t, "skip-leased", g.Map{
		"user_id":             811015,
		"publish_claim_token": "active-other-token",
		"publish_lease_until": gdbNowPlus(t, time.Minute),
	})

	stats, err := pub.PublishBatch(ctx, 10)
	if err != nil {
		t.Fatalf("publish batch: %v", err)
	}
	if stats.Claimed != 1 || stats.Published != 1 {
		t.Fatalf("only the due queued request should be claimed, stats=%+v", stats)
	}
	if outboxLoad(t, dueID).PublishedAt == nil {
		t.Fatalf("due request should be published")
	}
	if outboxLoad(t, publishedID).PublishedAt == nil {
		t.Fatalf("already published request lost its published_at")
	}
	if outboxLoad(t, notDueID).PublishedAt != nil {
		t.Fatalf("not-due request must not be published")
	}
	if outboxLoad(t, leasedID).PublishedAt != nil {
		t.Fatalf("actively leased request must not be published by another publisher")
	}
	for _, id := range []int64{successID, failedID, deadID} {
		row := outboxLoad(t, id)
		if row.PublishedAt != nil {
			t.Fatalf("non-queued request %d must not be published", id)
		}
		if row.Status == requestStatusQueued {
			t.Fatalf("publisher must not change status of %d", id)
		}
	}
}

// gdbNowPlus 返回相对数据库当前时间偏移后的时间（用于构造到期/未到期数据）。
func gdbNowPlus(t *testing.T, d time.Duration) string {
	t.Helper()
	return outboxDBNow(t).Add(d).Format("2006-01-02 15:04:05.000000")
}

// TestOutboxBacklog 覆盖待发布数量与最老事件年龄统计。
func TestOutboxBacklog(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()
	fake := &fakePublisher{}
	pub := newTestOutboxPublisher(t, fake, outboxTestOptions())

	if backlog, err := pub.Backlog(ctx); err != nil {
		t.Fatalf("empty backlog: %v", err)
	} else if backlog.Pending != 0 || backlog.OldestID != 0 || backlog.OldestAge != 0 {
		t.Fatalf("expected empty backlog, got %+v", backlog)
	}

	first := outboxInsertRequest(t, "backlog-1", g.Map{"user_id": 811016})
	if _, err := g.DB().Exec(ctx,
		"UPDATE flash_sale_order_requests SET created_at = DATE_SUB(NOW(), INTERVAL 90 SECOND) WHERE id = ?", first); err != nil {
		t.Fatalf("backdate request: %v", err)
	}
	outboxInsertRequest(t, "backlog-2", g.Map{"user_id": 811017})
	outboxInsertRequest(t, "backlog-published", g.Map{"user_id": 811018, "published_at": gdbNowPlus(t, -time.Minute)})
	outboxInsertRequest(t, "backlog-success", g.Map{"user_id": 811019, "status": requestStatusSuccess})

	backlog, err := pub.Backlog(ctx)
	if err != nil {
		t.Fatalf("backlog: %v", err)
	}
	if backlog.Pending != 2 {
		t.Fatalf("pending=%d want 2", backlog.Pending)
	}
	if backlog.OldestID != first {
		t.Fatalf("oldest id=%d want %d", backlog.OldestID, first)
	}
	if backlog.OldestAge < 80*time.Second || backlog.OldestAge > 300*time.Second {
		t.Fatalf("oldest age=%s want ~90s", backlog.OldestAge)
	}
}

// TestOutboxSuccessThenMarkFailureRecoverable 覆盖最危险窗口：
// Broker 已确认但 MySQL 标记失败 → published_at 保持 NULL，租约到期后可重新发布。
func TestOutboxSuccessThenMarkFailureRecoverable(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()
	fake := &fakePublisher{}
	pub := newTestOutboxPublisher(t, fake, outboxTestOptions())
	id := outboxInsertRequest(t, "mark-fail", g.Map{"user_id": 811020})

	original := outboxBeforeMarkHook
	outboxBeforeMarkHook = func(context.Context, int64) error { return errors.New("injected mark failure") }
	stats, err := pub.PublishBatch(ctx, 10)
	outboxBeforeMarkHook = original
	if err == nil {
		t.Fatalf("mark failure should be reported")
	}
	if stats.Published != 1 || stats.MarkFailed != 1 {
		t.Fatalf("stats should expose ack-success/mark-failure window: %+v", stats)
	}
	row := outboxLoad(t, id)
	if row.PublishedAt != nil {
		t.Fatalf("mark failure must not set published_at")
	}
	if row.PublishLeaseUntil == nil {
		t.Fatalf("lease should still be held after mark failure (recovery relies on expiry)")
	}

	// 租约到期后重新领取并发布：允许重复，但记录必须可恢复。
	outboxExpireLease(t, id)
	if _, err := pub.PublishBatch(ctx, 10); err != nil {
		t.Fatalf("recover publish: %v", err)
	}
	if outboxLoad(t, id).PublishedAt == nil {
		t.Fatalf("record must be recoverable after lease expiry")
	}
	if len(fake.allMessages()) != 2 {
		t.Fatalf("expected duplicate delivery (2 messages), got %d", len(fake.allMessages()))
	}
}

// TestOutboxRestartRecovery 覆盖进程重启恢复：
// 崩溃发生在「领取已提交、Kafka 未发布」之间——内存中的 claim 全部丢失，
// 新进程只凭数据库中的租约到期即可接管，无需任何进程内状态。
func TestOutboxRestartRecovery(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()

	crashing := newTestOutboxPublisher(t, &fakePublisher{}, outboxTestOptions())
	id := outboxInsertRequest(t, "restart-1", g.Map{"user_id": 811023})

	// 模拟崩溃前已完成领取事务：租约与 token 已落库，但进程随即消失。
	claims, token, _, err := crashing.claim(ctx, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claims) != 1 || token == "" {
		t.Fatalf("expected one claimed request, got %d (token=%q)", len(claims), token)
	}
	row := outboxLoad(t, id)
	if row.PublishClaimToken == nil || row.PublishLeaseUntil == nil {
		t.Fatalf("claim must be persisted before publishing: %+v", row)
	}
	if row.PublishedAt != nil {
		t.Fatalf("crash before publish must not leave published_at set")
	}

	// 新进程（全新 Publisher 实例）在租约未过期时无法领取；过期后恢复发布。
	restarted := newTestOutboxPublisher(t, &fakePublisher{}, outboxTestOptions())
	if stats, err := restarted.PublishBatch(ctx, 10); err != nil || stats.Claimed != 0 {
		t.Fatalf("unexpired lease must block takeover: stats=%+v err=%v", stats, err)
	}
	outboxExpireLease(t, id)
	stats, err := restarted.PublishBatch(ctx, 10)
	if err != nil {
		t.Fatalf("restart recovery publish: %v", err)
	}
	if stats.LeaseReclaimed != 1 || stats.Published != 1 {
		t.Fatalf("restart should reclaim and publish, got %+v", stats)
	}
	if outboxLoad(t, id).PublishedAt == nil {
		t.Fatalf("request should be published after restart recovery")
	}
}

// TestOutboxTerminalStatusNotReverted 覆盖「消费者先落终态」竞态：
// ACK 成功时请求已是 success，标记只写发布事实，绝不把状态改回 queued。
func TestOutboxTerminalStatusNotReverted(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	fake := &fakePublisher{
		entered: entered,
		gate:    gate,
		publish: func(_ context.Context, msgs []mq.Message) ([]mq.PublishResult, error) {
			return fakeSuccessResults(msgs, 1, 11), nil
		},
	}
	pub := newTestOutboxPublisher(t, fake, outboxTestOptions())
	id := outboxInsertRequest(t, "terminal-first", g.Map{"user_id": 811021})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = pub.PublishBatch(ctx, 10)
	}()
	<-entered
	// 消费者在 ACK 前后把请求改成 success（业务终态）。
	if _, err := g.DB().Exec(ctx,
		"UPDATE flash_sale_order_requests SET status = ?, flash_order_id = 1 WHERE id = ?", requestStatusSuccess, id); err != nil {
		t.Fatalf("set terminal status: %v", err)
	}
	close(gate)
	<-done

	row := outboxLoad(t, id)
	if row.Status != requestStatusSuccess {
		t.Fatalf("publisher must not revert terminal status, got %d", row.Status)
	}
	if row.PublishedAt == nil {
		t.Fatalf("ack must be recorded even though the request is already terminal")
	}
}

// TestOutboxMarkSkippedWhenLeaseTaken 覆盖旧 token 无权标记：
// 租约过期后由新 Publisher 接管并发布，旧持有者的迟到标记必须被丢弃。
func TestOutboxMarkSkippedWhenLeaseTaken(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	stale := &fakePublisher{
		entered: entered,
		gate:    gate,
		publish: func(_ context.Context, msgs []mq.Message) ([]mq.PublishResult, error) {
			return fakeSuccessResults(msgs, 9, 9999), nil
		},
	}
	stalePub := newTestOutboxPublisher(t, stale, outboxTestOptions())
	id := outboxInsertRequest(t, "stale-token", g.Map{"user_id": 811022})

	done := make(chan struct{})
	var staleStats OutboxStats
	go func() {
		defer close(done)
		staleStats, _ = stalePub.PublishBatch(ctx, 10)
	}()
	<-entered
	if row := outboxLoad(t, id); row.PublishClaimToken == nil {
		t.Fatalf("stale publisher should hold a claim token")
	}
	outboxExpireLease(t, id)

	fresh := &fakePublisher{publish: func(_ context.Context, msgs []mq.Message) ([]mq.PublishResult, error) {
		return fakeSuccessResults(msgs, 4, 444), nil
	}}
	freshPub := newTestOutboxPublisher(t, fresh, outboxTestOptions())
	stats, err := freshPub.PublishBatch(ctx, 10)
	if err != nil {
		t.Fatalf("fresh publish: %v", err)
	}
	if stats.LeaseReclaimed != 1 {
		t.Fatalf("expected lease reclaim to be counted, got %+v", stats)
	}
	if stats.Published != 1 {
		t.Fatalf("fresh publisher should publish the reclaimed record, got %+v", stats)
	}

	close(gate)
	<-done
	row := outboxLoad(t, id)
	if row.PublishedAt == nil {
		t.Fatalf("record should be marked published by the fresh publisher")
	}
	if row.PublishedOffset == nil || *row.PublishedOffset != 444 {
		t.Fatalf("stale publisher must not overwrite mark: offset=%v want 444", row.PublishedOffset)
	}
	if staleStats.MarkSkipped != 1 {
		t.Fatalf("stale publisher should report a skipped mark, got %+v", staleStats)
	}
}

// TestOutboxConcurrentClaimNoOverlap 覆盖多实例并发领取：两个 Publisher 的领取集合不重叠。
func TestOutboxConcurrentClaimNoOverlap(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()

	const total = 10
	ids := make([]int64, 0, total)
	for i := 0; i < total; i++ {
		ids = append(ids, outboxInsertRequest(t, fmt.Sprintf("concurrent-%d", i), g.Map{"user_id": 811030 + i}))
	}

	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	first := &fakePublisher{entered: entered, gate: gate}
	firstPub := newTestOutboxPublisher(t, first, outboxTestOptions())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = firstPub.PublishBatch(ctx, 5)
	}()
	<-entered

	second := &fakePublisher{}
	secondPub := newTestOutboxPublisher(t, second, outboxTestOptions())
	secondStats, err := secondPub.PublishBatch(ctx, 5)
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	close(gate)
	<-done

	if len(first.lastBatch()) != 5 || secondStats.Claimed != 5 {
		t.Fatalf("expected 5+5 claims, got %d + %d", len(first.lastBatch()), secondStats.Claimed)
	}
	seen := map[string]bool{}
	for _, msg := range append(first.allMessages(), second.allMessages()...) {
		if seen[string(msg.Key)+"|"+string(msg.Value)] {
			t.Fatalf("duplicate message published by two publishers")
		}
		seen[string(msg.Key)+"|"+string(msg.Value)] = true
	}
	if len(seen) != total {
		t.Fatalf("expected %d distinct messages, got %d", total, len(seen))
	}
	for _, id := range ids {
		if outboxLoad(t, id).PublishedAt == nil {
			t.Fatalf("request %d should be published exactly once", id)
		}
	}
}

// TestOutboxConcurrentRoundsNoDeadlock 覆盖多实例多轮并发：不死锁、不长时间阻塞、全部发布。
func TestOutboxConcurrentRoundsNoDeadlock(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()

	const requests = 20
	const workers = 8
	for i := 0; i < requests; i++ {
		outboxInsertRequest(t, fmt.Sprintf("rounds-%d", i), g.Map{"user_id": 811100 + i})
	}

	fake := &fakePublisher{}
	pub := newTestOutboxPublisher(t, fake, OutboxOptions{
		BatchSize:      3,
		Lease:          15 * time.Second,
		PublishTimeout: 3 * time.Second,
		BackoffBase:    time.Second,
		BackoffMax:     4 * time.Second,
	})

	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 4; round++ {
				if _, err := pub.PublishBatch(ctx, 3); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}

	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(60 * time.Second):
		t.Fatalf("concurrent publish rounds blocked for more than 60s (possible deadlock)")
	}
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent publish failed: %v", err)
	}

	n, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).
		Where("published_at IS NULL").Count()
	if err != nil {
		t.Fatalf("count unpublished: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d requests left unpublished", n)
	}
	if len(fake.allMessages()) != requests {
		t.Fatalf("expected %d messages, got %d", requests, len(fake.allMessages()))
	}
}

// TestOutboxRepairDeadToQueuedRepublishes 覆盖人工修复：dead→queued 重置发布状态后可重新发布，
// 且修复后到达的旧租约迟到标记不得污染结果。
func TestOutboxRepairDeadToQueuedRepublishes(t *testing.T) {
	setupOutboxTest(t)
	ctx := context.Background()
	s := New()

	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	stale := &fakePublisher{
		entered: entered,
		gate:    gate,
		publish: func(_ context.Context, msgs []mq.Message) ([]mq.PublishResult, error) {
			return fakeSuccessResults(msgs, 8, 8888), nil
		},
	}
	stalePub := newTestOutboxPublisher(t, stale, outboxTestOptions())
	id := outboxInsertRequest(t, "repair-republish", g.Map{"user_id": 811200})

	// 旧 Publisher 领取并卡在发布中（持有一代租约）。
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = stalePub.PublishBatch(ctx, 10)
	}()
	<-entered

	// 消费者把请求判定为 dead（技术失败），随后管理员修复为 queued。
	if _, err := g.DB().Exec(ctx, "UPDATE flash_sale_order_requests SET status = ? WHERE id = ?", requestStatusDead, id); err != nil {
		t.Fatalf("mark dead: %v", err)
	}
	adminID, _ := consumeSuperAdmin(t)
	if _, err := s.RepairRequest(ctx, adminID, &v1.RepairRequestReq{
		Id: id, TargetStatus: v1.RequestStatusQueued, Reason: "B2 修复后重投",
	}); err != nil {
		t.Fatalf("repair: %v", err)
	}
	row := outboxLoad(t, id)
	if row.Status != requestStatusQueued || row.PublishedAt != nil || row.PublishClaimToken != nil ||
		row.PublishLeaseUntil != nil || row.NextPublishAt != nil || row.PublishAttempts != 0 {
		t.Fatalf("repair must reset publish state: %+v", row)
	}

	// 修复后由新 Publisher 重新发布。
	fresh := &fakePublisher{publish: func(_ context.Context, msgs []mq.Message) ([]mq.PublishResult, error) {
		return fakeSuccessResults(msgs, 5, 555), nil
	}}
	freshPub := newTestOutboxPublisher(t, fresh, outboxTestOptions())
	stats, err := freshPub.PublishBatch(ctx, 10)
	if err != nil {
		t.Fatalf("fresh publish: %v", err)
	}
	if stats.Published != 1 {
		t.Fatalf("repaired request should be republished, got %+v", stats)
	}

	// 旧 Publisher 迟到返回：ACK 成功但 token 已被修复重置 → 标记必须落空。
	close(gate)
	<-done
	row = outboxLoad(t, id)
	if row.PublishedAt == nil || row.PublishedOffset == nil || *row.PublishedOffset != 555 {
		t.Fatalf("late mark from stale generation must not overwrite: %+v", row)
	}
	if row.Status != requestStatusQueued {
		t.Fatalf("late mark must not change status: %d", row.Status)
	}

	// F-01 契约证据：修复前（旧租约）与修复后（新租约）两代消息的 event_id 完全相同，
	// 因为 event_id 只由 request_id 推导，B2.1 不引入 repair_generation。
	// 因此消费者若以永久 event_id 台账去重，修复后的合法请求会被直接丢弃；
	// 唯一正确的幂等边界是 MySQL 请求行状态（事务内锁读，见 message.go）。
	staleBatch, freshBatch := stale.lastBatch(), fresh.lastBatch()
	if len(staleBatch) != 1 || len(freshBatch) != 1 {
		t.Fatalf("expected one message per generation, got %d and %d", len(staleBatch), len(freshBatch))
	}
	staleEvent, err := DecodeRequestedEvent(staleBatch[0].Value)
	if err != nil {
		t.Fatalf("decode stale event: %v", err)
	}
	freshEvent, err := DecodeRequestedEvent(freshBatch[0].Value)
	if err != nil {
		t.Fatalf("decode fresh event: %v", err)
	}
	if staleEvent.EventID != freshEvent.EventID {
		t.Fatalf("event_id must stay identical across repair generations: %q vs %q",
			staleEvent.EventID, freshEvent.EventID)
	}
	if staleEvent.EventID != RequestedEventID(id) {
		t.Fatalf("event_id=%q want %q", staleEvent.EventID, RequestedEventID(id))
	}
}
