// 秒杀 B3：Kafka 业务 Consumer（RequestedEvent v1 的真正消费方）。
//
// 本组件把 Kafka 从「消息发布基础设施」变成「秒杀请求的业务驱动」，完整链路是：
//
//	Poll（BlockRebalanceOnPoll）
//	  → 解析并校验消息（X 校验失败 = 永久错误）
//	  → 调用业务核心 ConsumeRequest（MySQL 事务：请求行锁 → 状态闸门 → 建单/扣库存 → 终态）
//	  → MySQL COMMIT
//	  → 提交 Kafka Offset
//	  → AllowRebalance
//
// 三条硬约束（顺序不可交换）：
//  1. MySQL 提交必须先于 Kafka Offset 提交。反过来会丢单：位移已推进、事务回滚，
//     消息不会再投递，而请求仍停在 queued。
//  2. 死信必须「先 Broker ACK、后 MySQL dead 终态、最后提交原 Offset」。
//     若先改终态再发死信，死信一旦失败，请求已不在 queued，原始业务请求就彻底无人处理。
//  3. 每次 Poll 返回的批次处理完（含提交/放弃位移）必须 AllowRebalance，
//     异常路径同样要释放，否则整个消费组会永久卡在再平衡等待上。
//
// 不做什么：
//   - 不建立 event_id 永久去重台账（人工 dead→queued 修复会重发同一 event_id，
//     真正的幂等闸门是 MySQL 请求行状态，见 message.go / repair.go）；
//   - 不并发消费同一批次（首版顺序消费，保证同分区顺序与位移连续提交）；
//   - 不把消息里的 user_id/activity_id/sku_id 当作落单依据，只用于与 MySQL 比对。
package flashsale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	"cnb.cool/go-cloud-devops/my-shop/internal/mq"
)

// 死信消息头（低基数诊断信息，不含价格/库存等业务快照）：
// 原始 Value 原样搬运，保证死信可被下游按 mk 原样重放。
const (
	dlqHeaderReason          = "dlq_reason"
	dlqHeaderError           = "dlq_error"
	dlqHeaderSourceTopic     = "dlq_source_topic"
	dlqHeaderSourcePartition = "dlq_source_partition"
	dlqHeaderSourceOffset    = "dlq_source_offset"
	dlqHeaderRetryCount      = "dlq_retry_count"
	dlqHeaderAttempts        = "dlq_attempts"

	// dlqMaxErrorLength 限制死信头里的错误文本长度，避免消息头无限膨胀。
	dlqMaxErrorLength = 512
)

// ConsumerOptions 是 Kafka 业务 Consumer 的运行参数；零值字段使用默认值。
type ConsumerOptions struct {
	// BatchSize 是单次 Poll 的最大消息数。首版默认 1：逐条拉取、逐条提交，
	// 把 BlockRebalanceOnPoll 的阻塞窗口压到「单条消息处理时间」，并天然保证位移连续提交。
	BatchSize int
	// DLQTopic 是死信主题，必填，且必须与业务主题不同（由 kafka 配置层保证）。
	DLQTopic string
	// MaxDeliveryAttempts 是单条消息在一次投递内的最大处理尝试次数（含首次）。
	// 它只是「无法持久化重试预算」时的本地兜底，正常路径由 retry_count 预算收敛。
	MaxDeliveryAttempts int
	// RetryBackoffBase / RetryBackoffMax 是本地兜底重试的指数退避区间。
	RetryBackoffBase time.Duration
	RetryBackoffMax  time.Duration
	// CommitTimeout 是单次位移提交的超时。
	CommitTimeout time.Duration
	// MessageTimeout 是单条消息（含重试与死信）的处理超时，保证单批处理时间有界。
	MessageTimeout time.Duration
	// FetchBackoff 是拉取错误的退避时长（Broker 短时不可用时避免紧密循环）。
	FetchBackoff time.Duration
}

// 默认值与上限。
const (
	defaultConsumerBatchSize           = 1
	defaultConsumerMaxDeliveryAttempts = 4 // = 重试预算 3 + 首次尝试
	defaultConsumerRetryBackoffBase    = 200 * time.Millisecond
	defaultConsumerRetryBackoffMax     = time.Second
	defaultConsumerCommitTimeout       = 10 * time.Second
	defaultConsumerMessageTimeout      = 20 * time.Second
	defaultConsumerFetchBackoff        = time.Second
	maxConsumerBatchSize               = 100
)

// withDefaults 用默认值补齐零值字段，并把批量上限截断到硬上限。
func (o ConsumerOptions) withDefaults() ConsumerOptions {
	if o.BatchSize <= 0 {
		o.BatchSize = defaultConsumerBatchSize
	}
	if o.BatchSize > maxConsumerBatchSize {
		o.BatchSize = maxConsumerBatchSize
	}
	if o.MaxDeliveryAttempts <= 0 {
		o.MaxDeliveryAttempts = defaultConsumerMaxDeliveryAttempts
	}
	if o.RetryBackoffBase <= 0 {
		o.RetryBackoffBase = defaultConsumerRetryBackoffBase
	}
	if o.RetryBackoffMax <= 0 {
		o.RetryBackoffMax = defaultConsumerRetryBackoffMax
	}
	if o.RetryBackoffMax < o.RetryBackoffBase {
		o.RetryBackoffMax = o.RetryBackoffBase
	}
	if o.CommitTimeout <= 0 {
		o.CommitTimeout = defaultConsumerCommitTimeout
	}
	if o.MessageTimeout <= 0 {
		o.MessageTimeout = defaultConsumerMessageTimeout
	}
	if o.FetchBackoff <= 0 {
		o.FetchBackoff = defaultConsumerFetchBackoff
	}
	return o
}

// RebalanceConsumer 是消费循环需要的完整客户端能力：在 mq.Consumer 之外补上
// 「解除 Poll 批次对分区再平衡的阻塞」（franz-go 的 BlockRebalanceOnPoll 配对调用）。
//
// 为什么用组合接口而不是直接扩展 mq.Consumer：通用契约保持最小，而本组件的正确性
// 强制依赖 AllowRebalance —— 漏掉它会让整个消费组永久卡在再平衡等待。
// 把它写进构造函数入参，缺能力就在装配期暴露，而不是运行期才发现分区搬不走。
type RebalanceConsumer interface {
	mq.Consumer
	AllowRebalance()
}

// RequestConsumer 是消费循环依赖的业务能力，由 *sFlashSale 实现。
//
// 单独抽成接口是为了让消费循环可以被假实现驱动做单元测试，
// 同时保持「消费循环只依赖通用 mq 契约 + 业务接口」，不反向依赖具体实现。
type RequestConsumer interface {
	// ConsumeRequest 针对指定 request_id 执行业务消费事务（成功/业务失败/重复/预算耗尽）。
	ConsumeRequest(ctx context.Context, in ConsumeRequest) (ConsumeRequestResult, error)
	// PersistConsumeFailure 持久化一次技术失败的重试预算；无法持久化时返回 error。
	PersistConsumeFailure(ctx context.Context, requestID int64, code codes.Code) (ConsumeFailureState, error)
	// MarkRequestDead 在死信投递成功后落 dead 终态并执行 Redis 补偿。
	MarkRequestDead(ctx context.Context, requestID int64, code codes.Code) (bool, error)
}

// RequestedEventConsumer 消费 RequestedEvent v1 并驱动秒杀订单创建。
//
// 生命周期：底层 mq.Consumer / mq.Publisher 由装配方（B4）创建与关闭，
// 本类型只负责消费循环，不接管它们的 Close（避免「谁关闭谁负责」的歧义）。
// Run 返回后不再拉取消息；未提交位移的消息会在进程重启后重新投递。
type RequestedEventConsumer struct {
	client RebalanceConsumer
	dlq    mq.Publisher
	svc    RequestConsumer
	opts   ConsumerOptions
}

// NewRequestedEventConsumer 构造业务 Consumer。缺少必要依赖或 Topic 时构造期失败：
// 宁可装配期报错，也不要在运行期把死信发到未知主题或对 nil 客户端轮询。
func NewRequestedEventConsumer(client RebalanceConsumer, dlq mq.Publisher, svc RequestConsumer, opts ConsumerOptions) (*RequestedEventConsumer, error) {
	if client == nil {
		return nil, errors.New("创建秒杀 Kafka Consumer: 消费客户端不能为空")
	}
	if dlq == nil {
		return nil, errors.New("创建秒杀 Kafka Consumer: 死信发布器不能为空")
	}
	if svc == nil {
		return nil, errors.New("创建秒杀 Kafka Consumer: 业务处理器不能为空")
	}
	if strings.TrimSpace(opts.DLQTopic) == "" {
		return nil, errors.New("创建秒杀 Kafka Consumer: 死信 Topic 不能为空")
	}
	o := opts.withDefaults()
	o.DLQTopic = strings.TrimSpace(opts.DLQTopic)
	return &RequestedEventConsumer{client: client, dlq: dlq, svc: svc, opts: o}, nil
}

// Run 运行消费循环，直到 context 取消（返回 nil）或遇到不可继续的技术错误（返回该错误）。
//
// 返回错误时调用方不得认为消息已被处理：未提交位移的消息会在重启后重新投递，
// 业务侧依赖 MySQL 请求行状态幂等。循环本身不重试「消息级」错误——
// 重试已经由 handleMessage 内部的重试预算 + 死信顺序完成。
func (c *RequestedEventConsumer) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		messages, err := c.client.Poll(ctx, c.opts.BatchSize)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, mq.ErrClosed) {
				return err
			}
			// 拉取失败时本批次没有交给业务处理：必须解除 BlockRebalanceOnPoll 的阻塞。
			// 低层 Poll 已在错误路径调用一次；这里再防御性调用，保证即使未来适配器
			// 忘记释放，也不会把消费组卡在永久再平衡等待上（AllowRebalance 幂等）。
			c.client.AllowRebalance()
			// 拉取失败不推进任何位移：退避后继续拉取，避免紧密循环打爆 Broker。
			logx.Warning(ctx, "flashsale_consumer_poll_failed", "秒杀 Kafka Consumer 拉取失败", logx.Fields{
				"error": err.Error(),
			})
			if sleepErr := sleepWithContext(ctx, c.opts.FetchBackoff); sleepErr != nil {
				return nil
			}
			continue
		}
		if len(messages) == 0 {
			// 空批次同样可能持有 Poll 批次计数（不同驱动实现不同），统一解除阻塞。
			c.client.AllowRebalance()
			continue
		}
		if err := c.handlePolled(ctx, messages); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// handlePolled 在一个函数内收口「Poll 批次 → 处理 → 提交 → AllowRebalance」的生命周期。
// defer 保证异常路径（包括 panic 展开）也会解除再平衡阻塞，不会永久卡住消费组。
func (c *RequestedEventConsumer) handlePolled(ctx context.Context, messages []mq.Message) error {
	defer c.client.AllowRebalance()
	return c.consumeBatch(ctx, messages)
}

// consumeBatch 顺序处理一批消息，并在安全边界提交位移。
//
// 位移提交策略：
//   - 顺序处理，成功一条就进入待提交集合；
//   - 一旦某条消息出现「技术错误且无法就地结论」，立即停止本批，
//     只提交它之前的消息 —— 绝不提交超过失败消息的同分区后续 Offset；
//   - 全部成功则一次性提交整批（同分区只提交最大位移）。
func (c *RequestedEventConsumer) consumeBatch(ctx context.Context, messages []mq.Message) error {
	acked := make([]mq.Message, 0, len(messages))
	for _, msg := range messages {
		metrics.ObserveConsumerRecord(ctx)
		if err := c.handleMessage(ctx, msg); err != nil {
			// 失败消息与其后的消息都不提交；先提交此前已确认的部分，
			// 否则进程重启会把它们全部重放一遍（幂等但无谓放大）。
			if commitErr := c.commit(ctx, acked); commitErr != nil {
				return errors.Join(err, commitErr)
			}
			return fmt.Errorf("处理 Kafka 消息（%s/%d@%d）失败，位移未提交: %w",
				msg.Topic, msg.Partition, msg.Offset, err)
		}
		acked = append(acked, msg)
	}
	return c.commit(ctx, acked)
}

// commit 提交给定消息的位移（同分区取最大 offset+1）。提交失败不得被吞掉：
// 位移未推进意味着消息会重投，调用方必须知道这次处理没有被「记账」。
func (c *RequestedEventConsumer) commit(ctx context.Context, messages []mq.Message) error {
	if len(messages) == 0 {
		return nil
	}
	commitCtx, cancel := context.WithTimeout(ctx, c.opts.CommitTimeout)
	defer cancel()
	if err := c.client.Commit(commitCtx, messages...); err != nil {
		metrics.ObserveConsumerCommitFailure(ctx)
		return fmt.Errorf("提交 Kafka 位移（%d 条）: %w", len(messages), err)
	}
	return nil
}

// handleMessage 处理单条消息，返回 nil 表示可以提交位移。
//
// 返回 error 的语义只有一种：本条消息暂时无法得出结论（技术错误、死信未 ACK、
// 无法持久化重试预算）。此时调用方不得提交位移。
func (c *RequestedEventConsumer) handleMessage(parent context.Context, msg mq.Message) error {
	started := time.Now()
	// 单条消息的处理时间必须有界：超时后停止本轮处理并放弃位移提交，
	// 保证 BlockRebalanceOnPoll 的阻塞窗口不会因为外部依赖挂起而无限拉长。
	ctx, cancel := context.WithTimeout(parent, c.opts.MessageTimeout)
	defer cancel()

	event, reason, decodeErr := parseRequestedEvent(msg)
	if decodeErr != nil {
		// 永久错误：消息本身不可处理，重投只会再次失败 —— 先死信、再 ACK。
		if err := c.publishDLQ(ctx, msg, reason, decodeErr, 0); err != nil {
			metrics.ObserveConsumerResult(ctx, metrics.ResultError, time.Since(started))
			return fmt.Errorf("投递死信失败（原消息位移保留）: %w", err)
		}
		metrics.ObserveConsumerDLQ(ctx, reason)
		metrics.ObserveConsumerResult(ctx, metrics.ResultInvalid, time.Since(started))
		logx.Warning(ctx, "flashsale_consumer_invalid_message", "秒杀 Kafka 消息不可处理，已投递死信", logx.Fields{
			"reason":    reason,
			"topic":     msg.Topic,
			"partition": msg.Partition,
			"offset":    msg.Offset,
			"error":     decodeErr.Error(),
		})
		return nil
	}

	in := ConsumeRequest{
		RequestID:      event.RequestID,
		UserID:         event.UserID,
		ActivityID:     event.ActivityID,
		SkuID:          event.SkuID,
		IdempotencyKey: event.IdempotencyKey,
		RequestHash:    event.RequestHash,
		// 传递传输元数据，供消费事务补写 Outbox 投递凭证（F-04 的终态窗口）。
		Delivery: &KafkaDelivery{Partition: msg.Partition, Offset: msg.Offset},
	}

	for attempt := 1; ; attempt++ {
		result, err := c.svc.ConsumeRequest(ctx, in)
		if err == nil {
			return c.settleResult(ctx, msg, event, result, started)
		}
		code := codes.FromError(err)

		// 技术错误：先持久化重试预算，再决定等待 / 死信 / 停止。
		state, persistErr := c.svc.PersistConsumeFailure(ctx, event.RequestID, code)
		if persistErr != nil {
			// 连重试次数都写不进去（典型：MySQL 不可用）：绝不因为本地计数达到阈值就丢弃
			// 有效业务消息。做有界本地退避后停止，未提交的位移会在重启后重新投递。
			if attempt >= c.opts.MaxDeliveryAttempts || ctx.Err() != nil {
				metrics.ObserveConsumerResult(ctx, metrics.ResultError, time.Since(started))
				return fmt.Errorf("无法持久化请求 %d 的重试预算（已尝试 %d 次），位移未提交: %w",
					event.RequestID, attempt, errors.Join(err, persistErr))
			}
			if sleepErr := sleepWithContext(ctx, c.retryBackoff(attempt)); sleepErr != nil {
				return sleepErr
			}
			continue
		}
		metrics.ObserveConsumerRetry(ctx)

		switch {
		case state.Resolved:
			// 请求已被其他消费者/人工修复推进到终态：本条消息属于重复投递，安全 ACK。
			metrics.ObserveConsumerResult(ctx, metrics.ResultDuplicate, time.Since(started))
			return nil
		case state.Exhausted:
			return c.deadLetter(ctx, msg, event, metrics.ConsumerDLQRetryExhausted, code, err, state.RetryCount, started)
		}

		if attempt >= c.opts.MaxDeliveryAttempts {
			// 本地投递预算先于持久化预算耗尽（例如每次失败都由不同原因触发）：
			// 停止本轮，保留位移，等下一次投递继续消耗持久化预算。
			metrics.ObserveConsumerResult(ctx, metrics.ResultError, time.Since(started))
			return fmt.Errorf("请求 %d 本次投递尝试 %d 次仍未成功，位移未提交: %w", event.RequestID, attempt, err)
		}
		// 退避时长来自已持久化的下一次尝试时间，而不是本地常量：
		// 进程重启后重投仍然遵守同一节奏（重试状态持久化的意义就在这里）。
		if sleepErr := sleepWithContext(ctx, state.Backoff); sleepErr != nil {
			return sleepErr
		}
	}
}

// settleResult 把业务结论转换为「提交/死信」动作。
func (c *RequestedEventConsumer) settleResult(
	ctx context.Context, msg mq.Message, event *RequestedEvent, result ConsumeRequestResult, started time.Time,
) error {
	switch result.Outcome {
	case ConsumeOutcomeSuccess:
		metrics.ObserveConsumerResult(ctx, metrics.ResultSuccess, time.Since(started))
		return nil
	case ConsumeOutcomeFailed:
		// 业务失败终态（库存不足/时间窗结束/已购/幂等冲突）：已提交 failed，属于正常 ACK。
		metrics.ObserveConsumerResult(ctx, metrics.ResultFailed, time.Since(started))
		return nil
	case ConsumeOutcomeDuplicate:
		metrics.ObserveConsumerResult(ctx, metrics.ResultDuplicate, time.Since(started))
		return nil
	case ConsumeOutcomeDead:
		// 重试预算在之前的投递中已耗尽：本轮不执行业务，直接进入死信收尾。
		// 错误码沿用请求行上记录的最近一次失败码，便于运维定位。
		return c.deadLetter(ctx, msg, event, metrics.ConsumerDLQRetryExhausted, result.ErrorCode,
			fmt.Errorf("请求 %d 技术失败重试预算已耗尽（最近错误码 %d）", event.RequestID, result.ErrorCode),
			result.RetryCount, started)
	case ConsumeOutcomeInvalid:
		// 永久错误：不执行业务、不改数据库（消息不可信，不能据此改写权威请求行）。
		cause := fmt.Errorf("请求 %d 与消息不一致或不存在（%s）", event.RequestID, result.Reason)
		if err := c.publishDLQ(ctx, msg, result.Reason, cause, 0); err != nil {
			metrics.ObserveConsumerResult(ctx, metrics.ResultError, time.Since(started))
			return fmt.Errorf("投递死信失败（原消息位移保留）: %w", err)
		}
		metrics.ObserveConsumerDLQ(ctx, result.Reason)
		metrics.ObserveConsumerResult(ctx, metrics.ResultInvalid, time.Since(started))
		logx.Warning(ctx, "flashsale_consumer_identity_mismatch", "秒杀 Kafka 消息与权威请求不一致，已投递死信", logx.Fields{
			"reason":     result.Reason,
			"request_id": event.RequestID,
			"topic":      msg.Topic,
			"partition":  msg.Partition,
			"offset":     msg.Offset,
		})
		return nil
	default:
		metrics.ObserveConsumerResult(ctx, metrics.ResultError, time.Since(started))
		return fmt.Errorf("未知的消费处置结论 %q（请求 %d）", result.Outcome, event.RequestID)
	}
}

// deadLetter 按固定顺序收尾：死信 ACK → MySQL dead 终态（含 Redis 补偿）→ 允许提交原位移。
// code 是写入 last_error_code 的业务/技术错误码（供运维按码定位失败原因）。
func (c *RequestedEventConsumer) deadLetter(
	ctx context.Context, msg mq.Message, event *RequestedEvent, reason string, code codes.Code, cause error, retries int, started time.Time,
) error {
	if err := c.publishDLQ(ctx, msg, reason, cause, retries); err != nil {
		// 死信未获 Broker 确认：原消息位移绝不能提交，否则原始业务请求就丢了。
		metrics.ObserveConsumerResult(ctx, metrics.ResultError, time.Since(started))
		return fmt.Errorf("死信投递失败，保留原消息位移: %w", err)
	}
	metrics.ObserveConsumerDLQ(ctx, reason)

	if _, err := c.svc.MarkRequestDead(ctx, event.RequestID, code); err != nil {
		// 死信已 ACK，但终态迁移失败：消息会重新投递（死信可能出现重复，允许），
		// 原始业务请求不会丢 —— 这正是「先死信、后终态」顺序的价值。
		metrics.ObserveConsumerResult(ctx, metrics.ResultError, time.Since(started))
		return fmt.Errorf("死信已投递，但请求 %d 落 dead 失败: %w", event.RequestID, err)
	}
	metrics.ObserveConsumerResult(ctx, metrics.ResultDead, time.Since(started))
	logx.Warning(ctx, "flashsale_consumer_dead_letter", "秒杀请求重试预算耗尽，已进入死信", logx.Fields{
		"reason":           reason,
		"request_id":       event.RequestID,
		"flashsale_id":     event.ActivityID,
		"sku_id":           event.SkuID,
		"retry_count":      retries,
		"source_partition": msg.Partition,
		"source_offset":    msg.Offset,
	})
	return nil
}

// publishDLQ 同步发布死信并等待 Broker 确认（acks=all 语义由 Publisher 适配器保证）。
// 返回 nil 才代表死信已被 Broker 接收。
func (c *RequestedEventConsumer) publishDLQ(ctx context.Context, msg mq.Message, reason string, cause error, retries int) error {
	headers := make([]mq.Header, 0, len(msg.Headers)+6)
	headers = append(headers, msg.Headers...)
	headers = append(headers,
		mq.Header{Key: dlqHeaderReason, Value: []byte(reason)},
		mq.Header{Key: dlqHeaderError, Value: []byte(truncateDLQError(cause.Error()))},
		mq.Header{Key: dlqHeaderSourceTopic, Value: []byte(msg.Topic)},
		mq.Header{Key: dlqHeaderSourcePartition, Value: []byte(strconv.Itoa(int(msg.Partition)))},
		mq.Header{Key: dlqHeaderSourceOffset, Value: []byte(strconv.FormatInt(msg.Offset, 10))},
		mq.Header{Key: dlqHeaderRetryCount, Value: []byte(strconv.Itoa(retries))},
	)
	results, err := c.dlq.Publish(ctx, mq.Message{
		// 显式指定死信主题：绝不依赖发布器的默认 Topic。
		Topic: c.opts.DLQTopic,
		// 保留原分区键 <activity_id>:<sku_id>，便于按 SKU 排查与重放。
		Key:     msg.Key,
		Value:   msg.Value,
		Headers: headers,
	})
	if err != nil && results == nil {
		return fmt.Errorf("发布死信消息: %w", err)
	}
	if len(results) == 0 {
		return errors.New("发布死信消息: 发布器未返回任何结果")
	}
	if results[0].Err != nil {
		return fmt.Errorf("发布死信消息: %w", results[0].Err)
	}
	return nil
}

// parseRequestedEvent 解析并校验消息体，返回事件对象、死信原因（失败时）与错误。
//
// 校验顺序固定为：JSON → schema_version → event_type → 必填字段/身份推导。
// 这样死信原因能精确区分「不是 JSON」「版本不认识」「事件类型不认识」「字段非法」，
// 而不是笼统的一条 decode error。
func parseRequestedEvent(msg mq.Message) (*RequestedEvent, string, error) {
	if len(msg.Value) == 0 {
		return nil, metrics.ConsumerDLQParse, errors.New("消息体为空")
	}
	var event RequestedEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		return nil, metrics.ConsumerDLQParse, fmt.Errorf("解析秒杀请求事件: %w", err)
	}
	if event.SchemaVersion != RequestedSchemaVersion {
		return nil, metrics.ConsumerDLQSchemaVersion, fmt.Errorf("不支持的 schema_version %d（仅支持 %d）",
			event.SchemaVersion, RequestedSchemaVersion)
	}
	if event.EventType != RequestedEventType {
		return nil, metrics.ConsumerDLQEventType, fmt.Errorf("不支持的 event_type %q（期望 %q）",
			event.EventType, RequestedEventType)
	}
	if err := event.Validate(); err != nil {
		return nil, metrics.ConsumerDLQInvalidField, fmt.Errorf("校验秒杀请求事件: %w", err)
	}
	if err := validateEventHeaders(msg, &event); err != nil {
		return nil, metrics.ConsumerDLQInvalidField, err
	}
	return &event, "", nil
}

// validateEventHeaders 校验消息头与消息体一致。
// 头是可选元数据（历史/手工消息可能没有），但一旦携带就必须与消息体一致：
// 不一致说明生产端拼装逻辑出错，按永久错误处理，避免把两条事实混在一起。
func validateEventHeaders(msg mq.Message, event *RequestedEvent) error {
	checks := []struct {
		key  string
		want string
	}{
		{"event_id", event.EventID},
		{"event_type", event.EventType},
		{"schema_version", strconv.Itoa(event.SchemaVersion)},
	}
	for _, c := range checks {
		raw, ok := msg.HeaderValue(c.key)
		if !ok {
			continue
		}
		if got := string(raw); got != c.want {
			return fmt.Errorf("消息头 %s=%q 与消息体 %q 不一致", c.key, got, c.want)
		}
	}
	return nil
}

// retryBackoff 计算本地兜底重试的指数退避（base 起步、max 封顶）。
func (c *RequestedEventConsumer) retryBackoff(attempt int) time.Duration {
	base, max := c.opts.RetryBackoffBase, c.opts.RetryBackoffMax
	if attempt <= 1 {
		return base
	}
	d := base
	for i := 1; i < attempt; i++ {
		if d >= max || d > max/2 {
			return max
		}
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}

// truncateDLQError 按死信头长度限制截断错误文本（诊断信息不应反过来让投递失败）。
func truncateDLQError(msg string) string {
	if len(msg) <= dlqMaxErrorLength {
		return msg
	}
	return msg[:dlqMaxErrorLength]
}

// sleepWithContext 是可取消的退避等待：context 结束立刻返回错误，
// 让消费循环能及时退出而不必等满整个退避窗口。
func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
