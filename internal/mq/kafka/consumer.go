package kafka

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	"cnb.cool/go-cloud-devops/my-shop/internal/mq"
)

const (
	// consumerClientID 是 Broker 侧可见的客户端标识。
	consumerClientID = "surgecart-flashsale-consumer"

	// consumerSessionTimeout / consumerHeartbeatInterval 控制消费组故障发现速度：
	// 30s 会话超时 + 3s 心跳，既不会因一次 GC 抖动误踢成员，也能在实例崩溃后
	// 半分钟内触发 rebalance 让其他实例接管分区。
	consumerSessionTimeout    = 30 * time.Second
	consumerHeartbeatInterval = 3 * time.Second

	// defaultPollRecords 是 Poll 未指定 maxMessages 时单次最多拉取的消息数。
	// 批量上限用于约束单批处理时间与内存，B3 可在此基础上再调小。
	defaultPollRecords = 100
)

// Consumer 是基于 franz-go 的 Consumer Group 基础客户端。
//
// 本类型只提供「拉取 + 手动提交位移 + 生命周期」三件基础设施能力，
// 刻意不提供消费循环：数据库事务与位移提交的先后关系、重试预算、死信处置、
// 分区暂停/恢复、rebalance 时在途事务处理属于 B3 的职责，
// 提前写一个语义不完整的循环只会被后续推翻。
//
// 关键语义：
//   - 禁止自动提交：构造时设置 DisableAutoCommit，位移只在 Commit 被调用时提交；
//   - BlockRebalanceOnPoll：Poll 返回的批次在处理期间不会被 rebalance 抢走分区，
//     但调用方必须在处理完成后调用 AllowRebalance，否则后续 rebalance 会一直阻塞；
//   - 重复消息允许发生：位移提交前崩溃或 rebalance 都会导致重投，业务必须幂等；
//   - franz-go 没有 max.poll.interval.ms 客户端参数（心跳由后台协程维持，超时不会自动离组），
//     等价约束必须由 B3 的消费循环保证：单批处理必须在有界时间内完成，
//     并在处理结束后尽快 AllowRebalance，避免长时间阻塞分区再平衡。
type Consumer struct {
	cl *kgo.Client

	// group / topic 只用于结构化日志与指标诊断，不含任何凭据。
	group string
	topic string

	// joined 记录「当前是否处于已加入消费组的会话中」，来源是 franz-go 的分区回调
	// （OnPartitionsAssigned / OnPartitionsRevoked / OnPartitionsLost），
	// 不是 NewConsumer 成功或 Run 已启动这类弱证据。
	joined atomic.Bool
	// joinLogged 保证「首次成功加入」日志每个客户端实例只输出一次：
	// rebalance 会反复 revoke/assign，但首次加入是排障锚点，不需要每次重复。
	joinLogged atomic.Bool

	// closed 供 Poll/Commit 无锁快速判断生命周期；closeMu 只保证 Close 的幂等执行。
	closed  atomic.Bool
	closeMu sync.Mutex
	// closeErr 保存首次 Close 的结果，重复 Close 返回同一结果（不重复执行资源释放）。
	closeErr error
}

// 编译期断言：Consumer 必须满足通用消费端契约。
var _ mq.Consumer = (*Consumer)(nil)

// NewConsumer 构造 Kafka Consumer Group 客户端。
//
// 构造阶段只校验配置并创建客户端，不连接 Broker、不加入消费组：
// 首次 Poll 才会触发组协调与分区分配。因此 Kafka 暂时不可用不会阻塞启动，
// 但也不要在构造后假设「消费已就绪」——就绪性需要由调用方通过 Poll 错误观察。
//
// 加入消费组的可靠证据是 franz-go 的 OnPartitionsAssigned 回调（JoinGroup/SyncGroup
// 成功后、分区分配下发时触发），本类型据此维护 joined 状态并驱动
// flashsale_consumer_joined 指标，不把「构造成功 / Run 已启动」当作加入证据。
func NewConsumer(cfg Config) (*Consumer, error) {
	if !cfg.Enabled {
		return nil, disabledError("Consumer")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("初始化 Kafka Consumer: %w", err)
	}
	c := &Consumer{group: cfg.ConsumerGroup, topic: cfg.Topic}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(consumerClientID),
		kgo.ConsumerGroup(cfg.ConsumerGroup),
		kgo.ConsumeTopics(cfg.Topic),
		// 禁止自动提交：offset 必须由业务在副作用持久化后显式提交。
		kgo.DisableAutoCommit(),
		// 拉取后到 AllowRebalance 之前冻结分区再平衡，避免处理中的分区被抢走。
		kgo.BlockRebalanceOnPoll(),
		kgo.SessionTimeout(consumerSessionTimeout),
		kgo.HeartbeatInterval(consumerHeartbeatInterval),
		// 消费组生命周期回调：只做状态维护、指标与日志，绝不阻塞回调
		// （franz-go 要求回调快，否则会拖长 rebalance 窗口）。
		kgo.OnPartitionsAssigned(c.onPartitionsAssigned),
		kgo.OnPartitionsRevoked(c.onPartitionsRevoked),
		kgo.OnPartitionsLost(c.onPartitionsLost),
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 Kafka Consumer（brokers=%v, group=%q）: %w", cfg.Brokers, cfg.ConsumerGroup, err)
	}
	c.cl = cl
	// 初始化即「未加入消费组」：构造成功只说明客户端对象存在。
	// 用 Background ctx 而非请求 ctx：这是组件级状态，不属于某一次调用。
	metrics.MarkConsumerJoined(context.Background(), false)
	return c, nil
}

// onPartitionsAssigned 是 franz-go 在「已加入消费组且分区分配完成」后触发的回调。
//
// franz-go v1.18.1 源码依据（pkg/kgo/consumer_group.go）：
//   - assign() 的注释明确说明："We always call on assigned, even if nothing new is
//     assigned." —— 空分区分配同样会回调，因此不能用「分区数为 0」推断未加入；
//   - 该回调只在 joinAndSync 成功后的 setupAssignedAndHeartbeat 中被调用，
//     是本适配器能拿到的最强「成员资格已建立」证据。
//
// 首次成功加入输出一次结构化日志 flashsale_consumer_joined（每个客户端实例一次），
// 避免每次 rebalance 都重复输出；后续 rebalance 的状态变化由指标表达。
func (c *Consumer) onPartitionsAssigned(ctx context.Context, _ *kgo.Client, assigned map[string][]int32) {
	joinedBefore := c.joined.Swap(true)
	metrics.MarkConsumerJoined(ctx, true)
	if joinedBefore {
		return
	}
	if !c.joinLogged.CompareAndSwap(false, true) {
		return
	}
	logx.Info(ctx, "flashsale_consumer_joined", "Kafka Consumer 已加入消费组", logx.Fields{
		"consumer_group": c.group,
		"topic":          c.topic,
		"partitions":     countPartitions(assigned),
		"event_time":     time.Now().Format(time.RFC3339Nano),
	})
}

// onPartitionsRevoked 在组会话结束（再平衡开始、主动离组）时触发。
//
// franz-go 语义：eager 消费者在每次会话结束时都可能收到该回调；cooperative 消费者
// 即使没有任何分区丢失，也会在会话结束时收到（"calling onRevoke at the end of a
// session even though no partitions were lost"）。因此语义是「当前会话已结束、
// 尚未重新加入」，指标置 0，等待下一次 onPartitionsAssigned 再置 1。
func (c *Consumer) onPartitionsRevoked(ctx context.Context, _ *kgo.Client, revoked map[string][]int32) {
	if c.joined.Swap(false) {
		logx.Info(ctx, "flashsale_consumer_left_group", "Kafka Consumer 已结束消费组会话，等待重新加入", logx.Fields{
			"consumer_group": c.group,
			"topic":          c.topic,
			"reason":         "revoked",
			"partitions":     countPartitions(revoked),
		})
	}
	metrics.MarkConsumerJoined(ctx, false)
}

// onPartitionsLost 在致命组错误（IllegalGeneration / UnknownMemberID / 鉴权失败）时触发。
//
// franz-go 明确说明该回调**可能在从未成功加入消费组时**触发，因此这里只把状态置 0，
// 不输出「首次加入」日志；日志用独立事件名区分「成员资格丢失」与「正常离组」。
func (c *Consumer) onPartitionsLost(ctx context.Context, _ *kgo.Client, lost map[string][]int32) {
	if c.joined.Swap(false) {
		logx.Warning(ctx, "flashsale_consumer_group_lost", "Kafka Consumer 失去消费组成员资格", logx.Fields{
			"consumer_group": c.group,
			"topic":          c.topic,
			"reason":         "lost",
			"partitions":     countPartitions(lost),
		})
	}
	metrics.MarkConsumerJoined(ctx, false)
}

// countPartitions 统计分配/撤销/丢失事件涉及的分区数（低基数，仅用于日志）。
func countPartitions(assignment map[string][]int32) int {
	total := 0
	for _, partitions := range assignment {
		total += len(partitions)
	}
	return total
}

// Poll 拉取至多 maxMessages 条消息（maxMessages<=0 时使用 defaultPollRecords）。
//
// 行为：
//   - 阻塞直到有消息、context 结束或客户端关闭；
//   - 返回的消息携带 Topic/Partition/Offset，供 Commit 定位位移；
//   - 客户端关闭时返回包装了 mq.ErrClosed 的错误，context 结束时返回包装后的 ctx 错误；
//   - 分区级拉取错误会聚合返回，调用方不得把「有错误」当作「没有消息」而静默轮询。
//
// 调用方必须单协程调用 Poll：分区位移的推进与 BlockRebalanceOnPoll 的生命周期
// 依赖单一批次串行处理，多协程并发 Poll 会破坏这个前提。
func (c *Consumer) Poll(ctx context.Context, maxMessages int) ([]mq.Message, error) {
	if maxMessages <= 0 {
		maxMessages = defaultPollRecords
	}
	if c.closed.Load() {
		return nil, fmt.Errorf("拉取 Kafka 消息: %w", mq.ErrClosed)
	}

	fetches := c.cl.PollRecords(ctx, maxMessages)

	// 关键：BlockRebalanceOnPoll 在 PollRecords 返回「非空 Fetches」时保留 poller 计数，
	// 即使这里只有分区错误、没有任何记录。调用方拿不到消息，就不会调用 AllowRebalance；
	// 计数泄漏后，下一次群体再平衡会永久等待（Broker 重启/分区迁移后的典型死锁）。
	// 因此所有不向调用方交付消息的返回路径都必须在这里解除阻塞。
	//
	// 记录本身在错误路径上会被丢弃（位移未提交，Broker 会重投），换取错误语义简单：
	// 调用方要么拿到可处理的批次，要么拿到错误，不会出现「半批消息 + 错误」的歧义。
	if err := ctx.Err(); err != nil {
		c.cl.AllowRebalance()
		return nil, fmt.Errorf("拉取 Kafka 消息: %w", err)
	}
	if err := fetches.Err(); err != nil {
		c.cl.AllowRebalance()
		if errors.Is(err, kgo.ErrClientClosed) {
			return nil, fmt.Errorf("拉取 Kafka 消息: %w", mq.ErrClosed)
		}
		// 逐个分区补充错误上下文，避免只看到 "unknown topic" 却不知道是哪个分区。
		errs := fetches.Errors()
		detailed := make([]error, 0, len(errs))
		for _, fetchErr := range errs {
			if errors.Is(fetchErr.Err, kgo.ErrClientClosed) {
				return nil, fmt.Errorf("拉取 Kafka 消息: %w", mq.ErrClosed)
			}
			detailed = append(detailed, fmt.Errorf("%s/%d: %w", fetchErr.Topic, fetchErr.Partition, fetchErr.Err))
		}
		return nil, fmt.Errorf("拉取 Kafka 消息: %w", errors.Join(detailed...))
	}
	if c.closed.Load() {
		c.cl.AllowRebalance()
		return nil, fmt.Errorf("拉取 Kafka 消息: %w", mq.ErrClosed)
	}

	messages := make([]mq.Message, 0, fetches.NumRecords())
	fetches.EachRecord(func(record *kgo.Record) {
		messages = append(messages, recordToMessage(record))
	})
	if len(messages) == 0 {
		// 空批次也可能持有 poller 计数（驱动实现差异），统一解除阻塞。
		c.cl.AllowRebalance()
	}
	return messages, nil
}

// Commit 手动提交给定消息的位移（offset+1，即下一条待消费位置）。
//
// 只提交调用方明确传入的消息：通常是一批中「已成功处理」的消息；
// 失败或未处理的消息不能出现在入参中，否则进程重启后会丢消息。
// 同一分区传入多条消息时只提交最大位移，避免重复提交导致位移回退。
// Commit 本身不具备事务性：业务写入与位移提交之间仍存在崩溃窗口，
// 「先提交数据库事务、后提交位移」的 at-least-once 语义由 B3 落实。
func (c *Consumer) Commit(ctx context.Context, messages ...mq.Message) error {
	if len(messages) == 0 {
		return nil
	}
	if c.closed.Load() {
		return fmt.Errorf("提交 Kafka 位移: %w", mq.ErrClosed)
	}

	type topicPartition struct {
		topic     string
		partition int32
	}
	// 收集每个分区的最大位移；map 遍历顺序无关，提交请求本身不要求顺序。
	offsets := make(map[topicPartition]int64, len(messages))
	for i, msg := range messages {
		if msg.Topic == "" {
			return fmt.Errorf("提交 Kafka 位移: 第 %d 条消息缺少 Topic", i+1)
		}
		if msg.Partition < 0 {
			return fmt.Errorf("提交 Kafka 位移: 第 %d 条消息分区 %d 无效", i+1, msg.Partition)
		}
		if msg.Offset < 0 {
			return fmt.Errorf("提交 Kafka 位移: 第 %d 条消息位移 %d 无效（消息可能尚未被消费）", i+1, msg.Offset)
		}
		key := topicPartition{topic: msg.Topic, partition: msg.Partition}
		if current, ok := offsets[key]; !ok || msg.Offset > current {
			offsets[key] = msg.Offset
		}
	}

	records := make([]*kgo.Record, 0, len(offsets))
	for key, offset := range offsets {
		records = append(records, &kgo.Record{
			Topic:     key.topic,
			Partition: key.partition,
			Offset:    offset,
			// LeaderEpoch 置 -1 表示「未知」：通用层不暴露 Kafka 的 epoch 元数据，
			// Broker 会跳过 epoch 校验但仍按位移提交。
			LeaderEpoch: -1,
		})
	}
	if err := c.cl.CommitRecords(ctx, records...); err != nil {
		return fmt.Errorf("提交 Kafka 位移（%d 条消息）: %w", len(messages), err)
	}
	return nil
}

// AllowRebalance 解除 Poll 批次对分区再平衡的阻塞。
//
// 启用 BlockRebalanceOnPoll 后，Poll 返回的批次处理完（含位移提交）必须调用本方法，
// 否则后续 rebalance 会一直等待，最终导致分区无法迁移、消费组失去弹性。
// B1 不提供消费循环，因此不在 Poll 内部自动调用：自动调用会让处理中的分区
// 在提交前被重新分配，正是 BlockRebalanceOnPoll 要避免的风险。
func (c *Consumer) AllowRebalance() {
	c.cl.AllowRebalance()
}

// Close 优雅关闭 Consumer：离开消费组、允许最终 rebalance、释放连接与后台协程。
//
// ctx 约束离开消费组的最长等待；即使 ctx 到期或离组失败，仍会执行底层 Close，
// 保证连接与后台协程被回收（不会因为无法联系 Broker 而永久挂住关闭流程）。
// 可重复调用，后续调用返回首次关闭的结果。
func (c *Consumer) Close(ctx context.Context) error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed.Load() {
		return c.closeErr
	}
	c.closed.Store(true)
	// 客户端即将离组：成员资格立即失效（后续 revoke 回调是幂等的重复置 0）。
	c.joined.Store(false)
	metrics.MarkConsumerJoined(ctx, false)

	// 先 AllowRebalance 再离组：BlockRebalanceOnPoll 下若有未处理完的 Poll 批次，
	// 直接离组会在等待分区回收时挂住；关闭流程本身不会再提交位移，放弃批次所有权是安全的。
	c.cl.AllowRebalance()
	if err := c.cl.LeaveGroupContext(ctx); err != nil {
		c.closeErr = fmt.Errorf("关闭 Kafka Consumer 前离开消费组: %w", err)
	}
	c.cl.CloseAllowingRebalance()
	return c.closeErr
}

// recordToMessage 把 franz-go Record 映射为通用消息，保留提交位移所需的元数据。
func recordToMessage(record *kgo.Record) mq.Message {
	headers := make([]mq.Header, len(record.Headers))
	for i, header := range record.Headers {
		headers[i] = mq.Header{Key: header.Key, Value: header.Value}
	}
	return mq.Message{
		Topic:     record.Topic,
		Key:       record.Key,
		Value:     record.Value,
		Headers:   headers,
		Partition: record.Partition,
		Offset:    record.Offset,
		Timestamp: record.Timestamp,
	}
}
