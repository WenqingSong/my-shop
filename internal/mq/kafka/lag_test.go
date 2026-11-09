package kafka

// T4-02：Consumer Lag 计算的协议级单元测试。
//
// 通过注入假 requestor 直接返回 kmsg 响应（Metadata/OffsetFetch/ListOffsets），
// 覆盖真实 Broker 下难以构造的分支：未提交位移、OffsetFetch 部分失败、
// Log End Offset 查询失败、分区数量变化、Lag 为负等。
//
// 注意：本文件不验证「真实 Broker 上的 Lag 数值」，那属于真实 Kafka 集成验证
// （REAL_KAFKA_LAG_VALIDATION），不在单元测试里冒充。

import (
	"context"
	"errors"
	"testing"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// fakeBroker 按请求类型返回预置响应，并记录收到的请求（用于断言采样确实发了协议请求）。
type fakeBroker struct {
	metadata    func(*kmsg.MetadataRequest) (kmsg.Response, error)
	offsetFetch func(*kmsg.OffsetFetchRequest) (kmsg.Response, error)
	listOffsets func(*kmsg.ListOffsetsRequest) (kmsg.Response, error)

	metadataCalls    int
	offsetFetchCalls int
	listOffsetsCalls []int64 // 每次 ListOffsets 的 Timestamp（-1=latest, -2=earliest）
}

func (f *fakeBroker) Request(_ context.Context, req kmsg.Request) (kmsg.Response, error) {
	switch typed := req.(type) {
	case *kmsg.MetadataRequest:
		f.metadataCalls++
		if f.metadata != nil {
			return f.metadata(typed)
		}
	case *kmsg.OffsetFetchRequest:
		f.offsetFetchCalls++
		if f.offsetFetch != nil {
			return f.offsetFetch(typed)
		}
	case *kmsg.ListOffsetsRequest:
		if len(typed.Topics) > 0 && len(typed.Topics[0].Partitions) > 0 {
			f.listOffsetsCalls = append(f.listOffsetsCalls, typed.Topics[0].Partitions[0].Timestamp)
		}
		if f.listOffsets != nil {
			return f.listOffsets(typed)
		}
	}
	return nil, errors.New("fake broker: unexpected request")
}

// stringPtr 返回字符串指针（Metadata 响应的 Topic 字段是 *string）。
func stringPtr(v string) *string { return &v }

// metadataWithPartitions 构造「主题存在且包含指定分区」的元数据响应。
func metadataWithPartitions(topic string, partitions ...int32) func(*kmsg.MetadataRequest) (kmsg.Response, error) {
	return func(*kmsg.MetadataRequest) (kmsg.Response, error) {
		resp := kmsg.NewPtrMetadataResponse()
		topicResp := kmsg.NewMetadataResponseTopic()
		topicResp.Topic = stringPtr(topic)
		for _, partition := range partitions {
			partitionResp := kmsg.NewMetadataResponseTopicPartition()
			partitionResp.Partition = partition
			topicResp.Partitions = append(topicResp.Partitions, partitionResp)
		}
		resp.Topics = append(resp.Topics, topicResp)
		return resp, nil
	}
}

// offsetFetchResponse 构造 OffsetFetch 响应：offsets 中 -1 表示无提交位移；
// errCodes 中的分区返回分区级错误。
func offsetFetchResponse(
	topic string, offsets map[int32]int64, errCodes map[int32]int16,
) func(*kmsg.OffsetFetchRequest) (kmsg.Response, error) {
	return func(*kmsg.OffsetFetchRequest) (kmsg.Response, error) {
		resp := kmsg.NewPtrOffsetFetchResponse()
		topicResp := kmsg.NewOffsetFetchResponseTopic()
		topicResp.Topic = topic
		for partition, offset := range offsets {
			partitionResp := kmsg.NewOffsetFetchResponseTopicPartition()
			partitionResp.Partition = partition
			partitionResp.Offset = offset
			partitionResp.ErrorCode = errCodes[partition]
			topicResp.Partitions = append(topicResp.Partitions, partitionResp)
		}
		resp.Topics = append(resp.Topics, topicResp)
		return resp, nil
	}
}

// listOffsetsResponse 构造 ListOffsets 响应：offsets 为分区 → 位移；errCodes 为分区级错误。
func listOffsetsResponse(
	topic string, offsets map[int32]int64, errCodes map[int32]int16,
) func(*kmsg.ListOffsetsRequest) (kmsg.Response, error) {
	return func(req *kmsg.ListOffsetsRequest) (kmsg.Response, error) {
		resp := kmsg.NewPtrListOffsetsResponse()
		topicResp := kmsg.NewListOffsetsResponseTopic()
		topicResp.Topic = topic
		for _, partitionReq := range req.Topics[0].Partitions {
			partitionResp := kmsg.NewListOffsetsResponseTopicPartition()
			partitionResp.Partition = partitionReq.Partition
			partitionResp.Offset = offsets[partitionReq.Partition]
			partitionResp.ErrorCode = errCodes[partitionReq.Partition]
			topicResp.Partitions = append(topicResp.Partitions, partitionResp)
		}
		resp.Topics = append(resp.Topics, topicResp)
		return resp, nil
	}
}

// partitionLagByID 把结果转成 map 便于断言。
func partitionLagByID(samples []PartitionLag) map[int32]PartitionLag {
	out := make(map[int32]PartitionLag, len(samples))
	for _, sample := range samples {
		out[sample.Partition] = sample
	}
	return out
}

// TestFetchPartitionLagWithCommits 覆盖正常 Lag 计算与零 Lag。
func TestFetchPartitionLagWithCommits(t *testing.T) {
	const topic = "t"
	broker := &fakeBroker{
		metadata: metadataWithPartitions(topic, 0, 1),
		offsetFetch: offsetFetchResponse(topic, map[int32]int64{
			0: 8,
			1: 10,
		}, nil),
		listOffsets: listOffsetsResponse(topic, map[int32]int64{
			0: 10,
			1: 10,
		}, nil),
	}

	samples, err := fetchPartitionLag(context.Background(), broker, "g", topic)
	if err != nil {
		t.Fatalf("正常采样不应返回错误: %v", err)
	}
	byID := partitionLagByID(samples)
	if got := byID[0]; got.Lag != 2 || !got.HasCommit || got.BaseOffset != 8 || got.LogEndOffset != 10 {
		t.Fatalf("分区 0 结果=%+v want lag=2 base=8 end=10 hasCommit=true", got)
	}
	if got := byID[1]; got.Lag != 0 {
		t.Fatalf("分区 1 Lag=%d want 0（已追平）", got.Lag)
	}
	// 已提交位移的分区不需要 Log Start Offset 查询。
	for _, timestamp := range broker.listOffsetsCalls {
		if timestamp == listOffsetsEarliest {
			t.Fatal("全部分区都有提交位移时不应查询 Log Start Offset")
		}
	}
}

// TestFetchPartitionLagNoCommitUsesLogStart 覆盖「committed offset 不存在」：
// 基线是 Log Start Offset，且必须显式标记 HasCommit=false（不能当 Lag=0）。
func TestFetchPartitionLagNoCommitUsesLogStart(t *testing.T) {
	const topic = "t"
	broker := &fakeBroker{
		metadata:    metadataWithPartitions(topic, 0),
		offsetFetch: offsetFetchResponse(topic, map[int32]int64{0: -1}, nil),
		listOffsets: func(req *kmsg.ListOffsetsRequest) (kmsg.Response, error) {
			timestamp := req.Topics[0].Partitions[0].Timestamp
			if timestamp == listOffsetsEarliest {
				return listOffsetsResponse(topic, map[int32]int64{0: 40}, nil)(req)
			}
			return listOffsetsResponse(topic, map[int32]int64{0: 100}, nil)(req)
		},
	}

	samples, err := fetchPartitionLag(context.Background(), broker, "g", topic)
	if err != nil {
		t.Fatalf("未提交位移不应产生错误: %v", err)
	}
	got := partitionLagByID(samples)[0]
	if got.HasCommit {
		t.Fatal("未提交位移的分区必须标记 HasCommit=false")
	}
	if got.BaseOffset != 40 || got.Lag != 60 {
		t.Fatalf("未提交位移应按 Log Start Offset 计算：base=%d lag=%d want 40/60", got.BaseOffset, got.Lag)
	}
	found := false
	for _, timestamp := range broker.listOffsetsCalls {
		if timestamp == listOffsetsEarliest {
			found = true
		}
	}
	if !found {
		t.Fatal("未提交位移必须查询 Log Start Offset（ListOffsets timestamp=-2）")
	}
}

// TestFetchPartitionLagGroupIDNotFound 覆盖「未初始化的 Consumer Group」：
// GROUP_ID_NOT_FOUND 等价于「没有任何提交位移」，按 Log Start Offset 计算初始积压。
func TestFetchPartitionLagGroupIDNotFound(t *testing.T) {
	const topic = "t"
	broker := &fakeBroker{
		metadata: metadataWithPartitions(topic, 0),
		offsetFetch: func(*kmsg.OffsetFetchRequest) (kmsg.Response, error) {
			resp := kmsg.NewPtrOffsetFetchResponse()
			resp.ErrorCode = kerr.GroupIDNotFound.Code
			return resp, nil
		},
		listOffsets: func(req *kmsg.ListOffsetsRequest) (kmsg.Response, error) {
			if req.Topics[0].Partitions[0].Timestamp == listOffsetsEarliest {
				return listOffsetsResponse(topic, map[int32]int64{0: 5}, nil)(req)
			}
			return listOffsetsResponse(topic, map[int32]int64{0: 25}, nil)(req)
		},
	}

	samples, err := fetchPartitionLag(context.Background(), broker, "g", topic)
	if err != nil {
		t.Fatalf("未初始化消费组不应产生错误: %v", err)
	}
	got := partitionLagByID(samples)[0]
	if got.Lag != 20 || got.HasCommit {
		t.Fatalf("未初始化消费组应得到初始积压 20（且 HasCommit=false），实际 %+v", got)
	}
}

// TestFetchPartitionLagOffsetFetchFailure 覆盖 Kafka 请求失败：必须返回错误，不得报告 Lag=0。
func TestFetchPartitionLagOffsetFetchFailure(t *testing.T) {
	const topic = "t"
	broker := &fakeBroker{
		metadata: metadataWithPartitions(topic, 0),
		offsetFetch: func(*kmsg.OffsetFetchRequest) (kmsg.Response, error) {
			return nil, errors.New("dial tcp: connection refused")
		},
	}

	samples, err := fetchPartitionLag(context.Background(), broker, "g", topic)
	if err == nil {
		t.Fatal("OffsetFetch 请求失败必须返回错误")
	}
	if len(samples) != 0 {
		t.Fatalf("请求失败不得返回任何分区结果，实际 %+v", samples)
	}
}

// TestFetchPartitionLagOffsetFetchPartialFailure 覆盖 OffsetFetch 部分失败：
// 成功分区照常返回，失败分区不出现在结果中，并以 error 标记本轮不完整。
func TestFetchPartitionLagOffsetFetchPartialFailure(t *testing.T) {
	const topic = "t"
	broker := &fakeBroker{
		metadata: metadataWithPartitions(topic, 0, 1),
		offsetFetch: offsetFetchResponse(topic, map[int32]int64{
			0: 3,
			1: 0,
		}, map[int32]int16{
			1: kerr.UnknownServerError.Code,
		}),
		listOffsets: listOffsetsResponse(topic, map[int32]int64{
			0: 10,
			1: 10,
		}, nil),
	}

	samples, err := fetchPartitionLag(context.Background(), broker, "g", topic)
	if err == nil {
		t.Fatal("分区级位移错误必须体现在 error 中（采样状态据此置 0）")
	}
	byID := partitionLagByID(samples)
	if len(byID) != 1 {
		t.Fatalf("只应返回成功的分区，实际 %+v", samples)
	}
	if got := byID[0]; got.Lag != 7 {
		t.Fatalf("分区 0 Lag=%d want 7", got.Lag)
	}
	if _, ok := byID[1]; ok {
		t.Fatal("位移读取失败的分区不得出现在结果中（应保留上一次有效值）")
	}
}

// TestFetchPartitionLagLogEndFailure 覆盖 Log End Offset 查询失败：
// 失败分区不返回，成功分区照常返回，并且不触发 Log Start Offset 查询时报错退出。
func TestFetchPartitionLagLogEndFailure(t *testing.T) {
	const topic = "t"
	broker := &fakeBroker{
		metadata: metadataWithPartitions(topic, 0, 1),
		offsetFetch: offsetFetchResponse(topic, map[int32]int64{
			0: 1,
			1: 2,
		}, nil),
		listOffsets: func(req *kmsg.ListOffsetsRequest) (kmsg.Response, error) {
			return listOffsetsResponse(topic, map[int32]int64{0: 9, 1: 0}, map[int32]int16{
				1: kerr.NotLeaderForPartition.Code,
			})(req)
		},
	}

	samples, err := fetchPartitionLag(context.Background(), broker, "g", topic)
	if err == nil {
		t.Fatal("Log End Offset 分区错误必须体现为 error")
	}
	byID := partitionLagByID(samples)
	if len(byID) != 1 {
		t.Fatalf("只应返回 LEO 读取成功的分区，实际 %+v", samples)
	}
	if got := byID[0]; got.Lag != 8 {
		t.Fatalf("分区 0 Lag=%d want 8", got.Lag)
	}
}

// TestFetchPartitionLagLatestRequestError 覆盖整轮 LEO 请求失败：必须返回错误且没有结果。
func TestFetchPartitionLagLatestRequestError(t *testing.T) {
	const topic = "t"
	broker := &fakeBroker{
		metadata:    metadataWithPartitions(topic, 0),
		offsetFetch: offsetFetchResponse(topic, map[int32]int64{0: 1}, nil),
		listOffsets: func(*kmsg.ListOffsetsRequest) (kmsg.Response, error) {
			return nil, errors.New("broker closed connection")
		},
	}

	samples, err := fetchPartitionLag(context.Background(), broker, "g", topic)
	if err == nil || len(samples) != 0 {
		t.Fatalf("LEO 请求失败必须返回错误且无结果：samples=%v err=%v", samples, err)
	}
}

// TestFetchPartitionLagPartitionGrowth 覆盖分区数量变化：
// 新增分区没有提交位移时按 Log Start Offset 计算，而不是报错或用其它分区的值。
func TestFetchPartitionLagPartitionGrowth(t *testing.T) {
	const topic = "t"
	broker := &fakeBroker{
		metadata: metadataWithPartitions(topic, 0, 1),
		offsetFetch: offsetFetchResponse(topic, map[int32]int64{
			0: 5,
			1: -1,
		}, nil),
		listOffsets: func(req *kmsg.ListOffsetsRequest) (kmsg.Response, error) {
			if req.Topics[0].Partitions[0].Timestamp == listOffsetsEarliest {
				// 只应查询新增分区（分区 1）的起始位移。
				if len(req.Topics[0].Partitions) != 1 || req.Topics[0].Partitions[0].Partition != 1 {
					t.Fatalf("Log Start Offset 只应查询未提交位移的分区，实际 %+v", req.Topics[0].Partitions)
				}
				return listOffsetsResponse(topic, map[int32]int64{1: 50}, nil)(req)
			}
			return listOffsetsResponse(topic, map[int32]int64{0: 11, 1: 60}, nil)(req)
		},
	}

	samples, err := fetchPartitionLag(context.Background(), broker, "g", topic)
	if err != nil {
		t.Fatalf("分区变化不应产生错误: %v", err)
	}
	byID := partitionLagByID(samples)
	if got := byID[0]; got.Lag != 6 {
		t.Fatalf("分区 0 Lag=%d want 6", got.Lag)
	}
	if got := byID[1]; got.Lag != 10 || got.HasCommit {
		t.Fatalf("新增分区 Lag=%d hasCommit=%v want 10/false", got.Lag, got.HasCommit)
	}
}

// TestFetchPartitionLagClampsNegative 覆盖「已提交位移大于 LEO」（Topic 重建/日志截断）：
// Lag 必须夹到 0，不得出现负值。
func TestFetchPartitionLagClampsNegative(t *testing.T) {
	const topic = "t"
	broker := &fakeBroker{
		metadata:    metadataWithPartitions(topic, 0),
		offsetFetch: offsetFetchResponse(topic, map[int32]int64{0: 100}, nil),
		listOffsets: listOffsetsResponse(topic, map[int32]int64{0: 20}, nil),
	}

	samples, err := fetchPartitionLag(context.Background(), broker, "g", topic)
	if err != nil {
		t.Fatalf("采样失败: %v", err)
	}
	if got := partitionLagByID(samples)[0]; got.Lag != 0 {
		t.Fatalf("Lag=%d want 0（负值必须夹到 0）", got.Lag)
	}
}

// TestFetchPartitionLagMissingTopic 覆盖主题不存在：必须返回错误，不得当作 Lag=0。
func TestFetchPartitionLagMissingTopic(t *testing.T) {
	broker := &fakeBroker{
		metadata: func(*kmsg.MetadataRequest) (kmsg.Response, error) {
			resp := kmsg.NewPtrMetadataResponse()
			topicResp := kmsg.NewMetadataResponseTopic()
			topicResp.Topic = stringPtr("other")
			resp.Topics = append(resp.Topics, topicResp)
			return resp, nil
		},
	}

	if samples, err := fetchPartitionLag(context.Background(), broker, "g", "missing"); err == nil || len(samples) != 0 {
		t.Fatalf("主题不存在必须返回错误：samples=%v err=%v", samples, err)
	}
}

// TestNewLagReaderRequiresEnabledConfig 覆盖配置校验与幂等关闭（不连接 Broker）。
func TestNewLagReaderRequiresEnabledConfig(t *testing.T) {
	if _, err := NewLagReader(Config{Enabled: false}); err == nil {
		t.Fatal("Kafka 未启用时不得创建 Lag 采样客户端")
	}

	reader, err := NewLagReader(Config{
		Enabled:       true,
		Brokers:       []string{"127.0.0.1:29092"},
		Topic:         "t",
		DLQTopic:      "t.dlq",
		ConsumerGroup: "g",
	})
	if err != nil {
		t.Fatalf("构造 Lag 采样客户端失败: %v", err)
	}
	// 只创建客户端、不连接 Broker：重复 Close 必须幂等。
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("重复 Close 必须幂等: %v", err)
	}
}
