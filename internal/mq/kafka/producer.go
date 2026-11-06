package kafka

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"cnb.cool/go-cloud-devops/my-shop/internal/mq"
)

// Producer 的可靠性参数（本地与生产共用；具体取值见注释）。
//
// 这些参数不是「越大越好」：投递超时、重试次数与缓冲上限共同决定 Kafka 故障时
// 发布调用的最坏等待时间与内存占用，必须保持有界，避免拖垮 HTTP 或 Outbox 发布任务。
const (
	// producerClientID 是 Broker 侧可见的客户端标识，便于排查连接来源。
	producerClientID = "surgecart-flashsale-producer"

	// producerLinger 是批量聚合等待窗口：5ms 换取更高压缩率与更少请求，
	// 同时把「单条发布额外延迟」限制在毫秒级。
	producerLinger = 5 * time.Millisecond

	// producerBatchMaxBytes 是单批次未压缩上限 64KB。
	// 与 linger 配合控制请求大小；Kafka 的 message.max.bytes 默认约 1MB，64KB 留足余量。
	producerBatchMaxBytes = 64 << 10

	// producerDeliveryTimeout 是单条消息从入队到确认的最长时间（含重试）。
	// 超过后 franz-go 返回失败，调用方（后续 Outbox Publisher）据此重试，
	// 不会出现「永远等不到结果」的消息。
	producerDeliveryTimeout = 30 * time.Second

	// producerRequestTimeout 是单次 Produce 请求的超时（10 秒级别），
	// 防止 Broker 无响应时长时间占用连接；它小于投递超时，给重试留出预算。
	producerRequestTimeout = 10 * time.Second

	// producerMaxRetries 是单条消息的最大重试次数。必须有限：
	// 无限重试会在 Broker 持续不可用时堆积消息并掩盖故障。
	producerMaxRetries = 5

	// producerMinRetryBackoff / producerMaxRetryBackoff 限定指数退避区间，
	// 避免重试风暴；退避达到上限后保持 2s，仍受投递超时总预算约束。
	producerMinRetryBackoff = 100 * time.Millisecond
	producerMaxRetryBackoff = 2 * time.Second

	// producerMaxBufferedRecords / producerMaxBufferedBytes 限定本地缓冲上限。
	// 达到上限时 franz-go 会阻塞 Produce（背压），而不是无限占用内存。
	producerMaxBufferedRecords = 10_000
	producerMaxBufferedBytes   = 64 << 20
)

// Producer 是基于 franz-go 的同步确认发布器。
//
// 可靠性语义：
//   - acks=all：只有 ISR 全部确认后才算成功；
//   - 幂等写入：franz-go 默认开启（未设置 DisableIdempotentWrite），
//     Broker 按 producer id/epoch/sequence 去重，网络重试不会产生重复消息；
//   - 重试有界：次数与指数退避均有上限，并受 30s 投递超时约束；
//   - 逐条结果：批量发布按入参顺序返回每条消息的确认结果，部分失败不会被伪装成整批成功。
//
// 注意：本类型只保证「单次 Produce 的可靠投递」，不保证「业务事件与数据库写入原子」。
// at-least-once 的业务发布语义由后续 Outbox Publisher（B2）在 MySQL 事务提交后实现。
type Producer struct {
	cl *kgo.Client
	// defaultTopic 是 Message.Topic 为空时使用的主题（来自配置）。
	defaultTopic string

	// mu 保护 closed/closeErr，并保证 Close 不会与仍在进行的 Publish 交叉执行：
	// Publish 持读锁直到批次结果确定，Close 持写锁，避免关闭瞬间仍有新消息入队。
	mu       sync.RWMutex
	closed   bool
	closeErr error
}

// 编译期断言：Producer 必须满足通用发布端契约。
var _ mq.Publisher = (*Producer)(nil)

// NewProducer 构造 Kafka Producer。
//
// 构造阶段只做配置校验与客户端对象初始化，不主动连接 Broker：
// franz-go 的 kgo.NewClient 是惰性的，首次 Produce 才建立连接。
// 因此 Broker 暂时不可用不会阻塞启动，故障会在发布结果中明确暴露。
func NewProducer(cfg Config) (*Producer, error) {
	if !cfg.Enabled {
		return nil, disabledError("Producer")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("初始化 Kafka Producer: %w", err)
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(producerClientID),
		// acks=all 是幂等写入的前提（Kafka 要求幂等 producer 必须使用 acks=all）。
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchCompression(kgo.ZstdCompression()),
		kgo.ProducerLinger(producerLinger),
		kgo.ProducerBatchMaxBytes(producerBatchMaxBytes),
		kgo.RecordDeliveryTimeout(producerDeliveryTimeout),
		kgo.ProduceRequestTimeout(producerRequestTimeout),
		kgo.RecordRetries(producerMaxRetries),
		kgo.RetryBackoffFn(producerRetryBackoff),
		kgo.MaxBufferedRecords(producerMaxBufferedRecords),
		kgo.MaxBufferedBytes(producerMaxBufferedBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 Kafka Producer（brokers=%v）: %w", cfg.Brokers, err)
	}
	return &Producer{cl: cl, defaultTopic: cfg.Topic}, nil
}

// producerRetryBackoff 计算第 attempt 次重试前的退避时长（100ms 起步、2s 封顶）。
// 指数增长但严格有界，避免 Broker 抖动时的重试风暴。
func producerRetryBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	backoff := producerMinRetryBackoff << attempt
	if backoff <= 0 || backoff > producerMaxRetryBackoff {
		return producerMaxRetryBackoff
	}
	return backoff
}

// Publish 同步发布一批消息，等待每条消息的 Broker 确认后返回。
//
// 返回语义：
//   - results 与 messages 等长且顺序一致；results[i].Err 非 nil 表示第 i 条失败；
//   - error 非 nil 且 results 为 nil 表示批次未完整投递（context 取消/超时、客户端已关闭），
//     此时整批结果未知，调用方不得把任何一条视为已发布；
//   - error 非 nil 且 results 非 nil 表示部分（或全部）消息失败，逐条错误仍可用；
//   - 单条失败不会中断其他消息，只有 context 结束才会提前返回。
//
// context 控制调用方愿意等待的时长：到期后立即返回，尚未确认的消息由驱动在后台
// 按投递超时收敛，绝不能在到期后断言「消息未发送」——这正是下游必须幂等的原因。
func (p *Producer) Publish(ctx context.Context, messages ...mq.Message) ([]mq.PublishResult, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	// 持读锁贯穿整个确认过程，防止 Close 在批次确认前关闭客户端。
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return nil, fmt.Errorf("发布 %d 条消息: %w", len(messages), mq.ErrClosed)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("发布 %d 条消息: %w", len(messages), err)
	}

	records := make([]*kgo.Record, len(messages))
	for i, msg := range messages {
		topic := msg.Topic
		if topic == "" {
			topic = p.defaultTopic
		}
		headers := make([]kgo.RecordHeader, len(msg.Headers))
		for j, h := range msg.Headers {
			headers[j] = kgo.RecordHeader{Key: h.Key, Value: h.Value}
		}
		records[i] = &kgo.Record{
			Topic:     topic,
			Key:       msg.Key,
			Value:     msg.Value,
			Headers:   headers,
			Timestamp: msg.Timestamp,
		}
		// 回填实际 Topic，使返回结果对调用方自描述。
		messages[i].Topic = topic
	}

	// 逐条回调、按下标写结果：ProduceSync 的结果顺序取决于回调完成顺序，不能直接使用。
	results := make([]mq.PublishResult, len(messages))
	var wg sync.WaitGroup
	wg.Add(len(records))
	for i, record := range records {
		idx := i
		p.cl.Produce(ctx, record, func(r *kgo.Record, err error) {
			result := mq.PublishResult{Message: messages[idx], Err: err}
			if r != nil {
				result.Partition = r.Partition
				result.Offset = r.Offset
			}
			results[idx] = result
			wg.Done()
		})
	}

	// 用一个有界的等待协程把 context 取消转换为及时返回。
	// 该协程在驱动的回调全部触发后自然退出（最迟受投递超时约束），不会永久泄漏。
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, fmt.Errorf("发布 %d 条消息: %w", len(messages), ctx.Err())
	}

	failed := 0
	var firstErr error
	for _, result := range results {
		if result.Err != nil {
			failed++
			if firstErr == nil {
				firstErr = result.Err
			}
		}
	}
	if failed > 0 {
		return results, fmt.Errorf("发布 %d 条消息: %d 条未获 Broker 确认，首个错误: %w", len(messages), failed, firstErr)
	}
	return results, nil
}

// Close 优雅关闭 Producer：先刷新本地缓冲的消息，再释放连接与后台协程。
//
// ctx 只约束刷新阶段的最长等待；刷新超时后仍然执行底层 Close（在途请求由 30s
// 投递超时兜底），因此 Close 不会无限阻塞，也不会遗留 franz-go 的后台协程。
// 可重复调用，后续调用返回首次关闭的结果。
func (p *Producer) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return p.closeErr
	}
	p.closed = true

	kgoClient := p.cl
	if err := kgoClient.Flush(ctx); err != nil {
		p.closeErr = fmt.Errorf("关闭 Kafka Producer 前刷新缓冲消息: %w", err)
	}
	kgoClient.Close()
	return p.closeErr
}
