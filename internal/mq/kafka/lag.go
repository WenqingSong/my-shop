package kafka

// Kafka Consumer Lag 采集（T4-02）。
//
// 语义：对目标 Topic 的每个分区计算
//
//	lag = max(0, log_end_offset - committed_offset)
//
// 其中 committed_offset 是消费组已提交的「下一条待消费位移」，log_end_offset 是
// 该分区当前日志末端（下一条将被写入的位置）。因此 lag = 0 表示消费组已追平；
// lag > 0 表示还有多少条消息未被消费组确认。
//
// 未提交位移（committed_offset = -1）的处理：**不把 Lag 当成 0**。本消费者未设置
// kgo.ConsumeResetOffset，franz-go 默认 NewOffset().AtStart()（见 kgo.Config 默认值），
// 即未提交位移的分区会从 Log Start Offset 开始消费；因此初始积压按
//
//	lag = max(0, log_end_offset - log_start_offset)
//
// 定义，并在 PartitionLag.HasCommit=false 中显式标记「该分区尚无提交位移」。
// 如果连 Log Start Offset 都拿不到，则跳过该分区（保留上一次有效值），绝不伪造精确 Lag。
//
// 为什么不用 kadm：kmsg v1.9.0 已含 OffsetFetch / ListOffsets 的完整编解码，
// 而 franz-go 的 kgo.Client.Request 会自动把这两类请求路由到组协调器/分区 Leader，
// 并按 Broker 版本协商（含 version 降级重试）。引入 kadm 只会增加依赖面。
//
// 为什么不用消费客户端的 kgo.Client：采样复用消费客户端会把「监控」耦合到
// 「消费组会话生命周期」（客户端在 rebalance/重建期间会被关闭），且管理请求与
// 拉取请求争用同一批连接。这里使用独立客户端（见 NewLagReader），生命周期由
// worker 的采样器单独管理。

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

const (
	// lagClientID 是采样客户端在 Broker 侧的标识（与消费客户端区分，便于排障）。
	lagClientID = "surgecart-flashsale-lag-sampler"

	// listOffsetsLatest / listOffsetsEarliest 是 ListOffsets 的两个特殊时间戳。
	listOffsetsLatest   = int64(-1)
	listOffsetsEarliest = int64(-2)

	// noCommittedOffset 是 Kafka 对「该分区没有已提交位移」的返回值。
	noCommittedOffset = int64(-1)
)

// PartitionLag 是单个分区的 Lag 采样结果。
type PartitionLag struct {
	// Partition 是分区号。
	Partition int32
	// LogEndOffset 是该分区当前日志末端位移。
	LogEndOffset int64
	// CommittedOffset 是消费组已提交的下一条待消费位移；-1 表示尚未提交。
	CommittedOffset int64
	// BaseOffset 是 Lag 计算基线：有提交位移时等于 CommittedOffset，否则等于 LogStartOffset。
	BaseOffset int64
	// Lag = max(0, LogEndOffset - BaseOffset)，单位 messages。
	Lag int64
	// HasCommit 表示该分区是否已有已提交位移（false 表示按 Log Start Offset 计算初始积压）。
	HasCommit bool
}

// LagReader 是 Kafka Consumer Lag 的只读采集器：独立的 kgo.Client（不订阅主题、
// 不加入消费组、不提交位移），可被后台采样循环反复调用。
type LagReader struct {
	cl    *kgo.Client
	group string
	topic string

	closeOnce sync.Once
}

// NewLagReader 创建 Lag 采集器。只创建客户端，不连接 Broker（首次请求才建连），
// 因此 Broker 暂时不可用不会阻塞启动；采样失败由调用方按「保留上一次有效值」处理。
func NewLagReader(cfg Config) (*LagReader, error) {
	if !cfg.Enabled {
		return nil, disabledError("Consumer Lag 采样器")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("初始化 Kafka Consumer Lag 采样器: %w", err)
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(lagClientID),
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 Kafka Consumer Lag 采样器（brokers=%v）: %w", cfg.Brokers, err)
	}
	return &LagReader{cl: cl, group: cfg.ConsumerGroup, topic: cfg.Topic}, nil
}

// Fetch 采样一轮分区级 Lag。
//
// 返回值语义：
//   - 返回「本轮成功采到」的分区；部分分区失败时仍返回其余分区，并以 error 说明失败；
//   - error 非 nil 表示本轮不完整（调用方必须把采样状态置为失败并保留缺失分区的上次值）；
//   - 完全失败时返回 nil + error，绝不返回「全 0」的伪结果。
func (r *LagReader) Fetch(ctx context.Context) ([]PartitionLag, error) {
	if r == nil || r.cl == nil {
		return nil, errors.New("Kafka Consumer Lag 采样器未初始化")
	}
	return fetchPartitionLag(ctx, r.cl, r.group, r.topic)
}

// Close 关闭采样客户端（幂等，可重复调用）。
func (r *LagReader) Close() error {
	if r == nil || r.cl == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.cl.Close()
	})
	return nil
}

// requestor 抽象「向 Broker 发一次 Kafka 协议请求」这一最小能力。
// 抽成接口是为了用协议级假响应测试 Lag 计算与失败路径，不需要真实 Broker。
type requestor interface {
	Request(ctx context.Context, req kmsg.Request) (kmsg.Response, error)
}

// fetchPartitionLag 是 Lag 计算的核心：元数据 → 已提交位移 → Log End/Start Offset → 计算。
//
// errs 汇总的是「分区级/请求级」失败；只要有分区可用就返回它们，让调用方做部分更新。
func fetchPartitionLag(ctx context.Context, req requestor, group, topic string) ([]PartitionLag, error) {
	partitions, err := topicPartitions(ctx, req, topic)
	if err != nil {
		return nil, err
	}
	if len(partitions) == 0 {
		return nil, fmt.Errorf("主题 %q 没有可用分区", topic)
	}

	committed, commitErrs, err := committedOffsets(ctx, req, group, topic, partitions)
	if err != nil {
		// 整组位移不可读：无法区分「有提交」与「未提交」，不能伪造 Lag。
		return nil, err
	}
	errs := append([]error(nil), commitErrs...)

	// 所有分区都需要 Log End Offset（无论是否已提交位移）。
	ends, endErrs, err := partitionOffsets(ctx, req, topic, partitions, listOffsetsLatest)
	if err != nil {
		return nil, err
	}
	errs = append(errs, endErrs...)

	// 只有「未提交位移」的分区才需要 Log Start Offset（初始基线）。
	var uncommitted []int32
	for _, p := range partitions {
		if offset, ok := committed[p]; ok && offset == noCommittedOffset {
			uncommitted = append(uncommitted, p)
		}
	}
	starts := map[int32]int64{}
	if len(uncommitted) > 0 {
		var startErrs []error
		var startErr error
		starts, startErrs, startErr = partitionOffsets(ctx, req, topic, uncommitted, listOffsetsEarliest)
		if startErr != nil {
			// 已提交位移的分区仍然可算：这里只把未提交分区标记为失败。
			errs = append(errs, startErr)
		}
		errs = append(errs, startErrs...)
	}

	samples := make([]PartitionLag, 0, len(partitions))
	for _, partition := range partitions {
		commitOffset, ok := committed[partition]
		if !ok {
			// 该分区位移读取失败（错误已记录）：保留上一次有效值。
			continue
		}
		endOffset, ok := ends[partition]
		if !ok {
			// 该分区 Log End Offset 读取失败（错误已记录）：保留上一次有效值。
			continue
		}

		sample := PartitionLag{
			Partition:       partition,
			LogEndOffset:    endOffset,
			CommittedOffset: commitOffset,
			HasCommit:       commitOffset != noCommittedOffset,
		}
		if sample.HasCommit {
			sample.BaseOffset = commitOffset
		} else {
			startOffset, ok := starts[partition]
			if !ok {
				// 无法确定初始消费位置：不伪造精确 Lag（错误已记录）。
				continue
			}
			sample.BaseOffset = startOffset
		}
		sample.Lag = sample.LogEndOffset - sample.BaseOffset
		if sample.Lag < 0 {
			// 已提交位移可能大于 LEO（Topic 重建/日志截断后的短暂窗口），Lag 不允许为负。
			sample.Lag = 0
		}
		samples = append(samples, sample)
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i].Partition < samples[j].Partition })

	err = errors.Join(errs...)
	if len(samples) == 0 && err == nil {
		err = fmt.Errorf("主题 %q 的 Lag 采样没有可用结果", topic)
	}
	return samples, err
}

// topicPartitions 读取主题当前的分区列表（分区变化后下一轮采样自动跟随）。
func topicPartitions(ctx context.Context, req requestor, topic string) ([]int32, error) {
	meta := kmsg.NewPtrMetadataRequest()
	// 采样只读，绝不因为主题不存在而触发自动建 Topic。
	meta.AllowAutoTopicCreation = false
	name := topic
	topicReq := kmsg.NewMetadataRequestTopic()
	topicReq.Topic = &name
	meta.Topics = append(meta.Topics, topicReq)

	resp, err := meta.RequestWith(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("查询主题 %q 元数据: %w", topic, err)
	}
	for _, topicResp := range resp.Topics {
		if topicResp.Topic == nil || *topicResp.Topic != topic {
			continue
		}
		if code := kerr.ErrorForCode(topicResp.ErrorCode); code != nil {
			return nil, fmt.Errorf("查询主题 %q 元数据: %w", topic, code)
		}
		partitions := make([]int32, 0, len(topicResp.Partitions))
		for _, partition := range topicResp.Partitions {
			if code := kerr.ErrorForCode(partition.ErrorCode); code != nil {
				return nil, fmt.Errorf("查询主题 %q 分区 %d 元数据: %w", topic, partition.Partition, code)
			}
			partitions = append(partitions, partition.Partition)
		}
		sort.Slice(partitions, func(i, j int) bool { return partitions[i] < partitions[j] })
		return partitions, nil
	}
	return nil, fmt.Errorf("元数据响应中不包含主题 %q", topic)
}

// committedOffsets 读取消费组在目标分区上的已提交位移。
//
// 返回 map 的语义：key 存在 = 该分区位移可读；value = -1 表示「尚未提交」。
// 分区级错误写入 errs（该分区不进入 map）；整体请求失败或组级错误返回 error。
func committedOffsets(
	ctx context.Context, req requestor, group, topic string, partitions []int32,
) (map[int32]int64, []error, error) {
	offsetReq := kmsg.NewPtrOffsetFetchRequest()
	offsetReq.Group = group
	topicReq := kmsg.NewOffsetFetchRequestTopic()
	topicReq.Topic = topic
	topicReq.Partitions = append(topicReq.Partitions, partitions...)
	offsetReq.Topics = append(offsetReq.Topics, topicReq)
	// RequireStable=true：rebalance 期间不返回不稳定位移，避免读到即将回滚的值。
	offsetReq.RequireStable = true

	resp, err := offsetReq.RequestWith(ctx, req)
	if err != nil {
		return nil, nil, fmt.Errorf("查询消费组 %q 已提交位移: %w", group, err)
	}

	if code := kerr.ErrorForCode(resp.ErrorCode); code != nil {
		if errors.Is(code, kerr.GroupIDNotFound) {
			// 消费组尚未提交过任何位移：等价于「全部分区都没有提交位移」，
			// 由调用方用 Log Start Offset 作为初始基线（不把 Lag 伪造成 0）。
			uncommitted := make(map[int32]int64, len(partitions))
			for _, partition := range partitions {
				uncommitted[partition] = noCommittedOffset
			}
			return uncommitted, nil, nil
		}
		return nil, nil, fmt.Errorf("查询消费组 %q 已提交位移: %w", group, code)
	}

	offsets := make(map[int32]int64, len(partitions))
	var errs []error
	for _, respTopic := range resp.Topics {
		if respTopic.Topic != topic {
			continue
		}
		for _, respPartition := range respTopic.Partitions {
			if code := kerr.ErrorForCode(respPartition.ErrorCode); code != nil {
				errs = append(errs, fmt.Errorf("分区 %d 已提交位移: %w", respPartition.Partition, code))
				continue
			}
			offsets[respPartition.Partition] = respPartition.Offset
		}
	}
	return offsets, errs, nil
}

// partitionOffsets 用 ListOffsets 读取指定时间戳对应的位移：
// listOffsetsLatest（-1）= Log End Offset，listOffsetsEarliest（-2）= Log Start Offset。
func partitionOffsets(
	ctx context.Context, req requestor, topic string, partitions []int32, timestamp int64,
) (map[int32]int64, []error, error) {
	if len(partitions) == 0 {
		return map[int32]int64{}, nil, nil
	}
	listReq := kmsg.NewPtrListOffsetsRequest()
	topicReq := kmsg.NewListOffsetsRequestTopic()
	topicReq.Topic = topic
	for _, partition := range partitions {
		partitionReq := kmsg.NewListOffsetsRequestTopicPartition()
		partitionReq.Partition = partition
		partitionReq.Timestamp = timestamp
		topicReq.Partitions = append(topicReq.Partitions, partitionReq)
	}
	listReq.Topics = append(listReq.Topics, topicReq)

	resp, err := listReq.RequestWith(ctx, req)
	if err != nil {
		return nil, nil, fmt.Errorf("查询主题 %q 位移（timestamp=%d）: %w", topic, timestamp, err)
	}

	offsets := make(map[int32]int64, len(partitions))
	var errs []error
	for _, respTopic := range resp.Topics {
		if respTopic.Topic != topic {
			continue
		}
		for _, respPartition := range respTopic.Partitions {
			if code := kerr.ErrorForCode(respPartition.ErrorCode); code != nil {
				errs = append(errs, fmt.Errorf("分区 %d 位移（timestamp=%d）: %w", respPartition.Partition, timestamp, code))
				continue
			}
			offsets[respPartition.Partition] = respPartition.Offset
		}
	}
	return offsets, errs, nil
}
