package kafka

// F-03 回归测试：Producer 在 context 中途结束时必须保留逐条结果。
//
// 这些用例不连接 Broker：把 (*kgo.Client).Produce 替换为可控的异步投递函数，
// 精确构造「部分 ACK + 部分超时」「明确失败 + 超时」「回调晚于返回值」等时序。
// 生产环境使用同一个 collectPublishResults，因此这里验证的就是实际发布路径。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"cnb.cool/go-cloud-devops/my-shop/internal/mq"
)

// resultTestMessages 构造 n 条带稳定 Key 的消息，便于校验返回顺序。
func resultTestMessages(n int) []mq.Message {
	messages := make([]mq.Message, n)
	for i := range messages {
		messages[i] = mq.Message{
			Topic: "test.topic",
			Key:   []byte(fmt.Sprintf("key-%d", i)),
			Value: []byte(fmt.Sprintf("value-%d", i)),
		}
	}
	return messages
}

// resultTestRecords 与 resultTestMessages 一一对应。
func resultTestRecords(messages []mq.Message) []*kgo.Record {
	records := make([]*kgo.Record, len(messages))
	for i, msg := range messages {
		records[i] = &kgo.Record{Topic: msg.Topic, Key: msg.Key, Value: msg.Value}
	}
	return records
}

// TestProducerPublishResultsAllSucceed 覆盖批次内所有消息成功：无批次错误，顺序一致。
func TestProducerPublishResultsAllSucceed(t *testing.T) {
	messages := resultTestMessages(3)
	records := resultTestRecords(messages)
	produce := func(_ context.Context, record *kgo.Record, promise func(*kgo.Record, error)) {
		record.Partition = 7
		record.Offset = int64(len(record.Key))
		promise(record, nil)
	}

	results, err := collectPublishResults(context.Background(), messages, records, produce)
	if err != nil {
		t.Fatalf("全部成功时不应返回批次错误: %v", err)
	}
	if len(results) != len(messages) {
		t.Fatalf("结果条数 %d != 输入条数 %d", len(results), len(messages))
	}
	for i, result := range results {
		if result.Err != nil {
			t.Fatalf("第 %d 条不应失败: %v", i, result.Err)
		}
		if string(result.Message.Key) != string(messages[i].Key) {
			t.Fatalf("第 %d 条结果顺序错乱: got key=%q want=%q", i, result.Message.Key, messages[i].Key)
		}
		if result.Partition != 7 || result.Offset != int64(len(messages[i].Key)) {
			t.Fatalf("第 %d 条分区/位移未保留: %+v", i, result)
		}
	}
}

// TestProducerPublishResultsPartialFailure 覆盖部分成功、部分明确失败：
// 成功条目不因同批失败而丢失，失败条目保留具体错误。
func TestProducerPublishResultsPartialFailure(t *testing.T) {
	messages := resultTestMessages(3)
	records := resultTestRecords(messages)
	boom := errors.New("record too large")
	produce := func(_ context.Context, record *kgo.Record, promise func(*kgo.Record, error)) {
		if string(record.Key) == "key-1" {
			promise(record, boom)
			return
		}
		record.Partition = 1
		record.Offset = 100
		promise(record, nil)
	}

	results, err := collectPublishResults(context.Background(), messages, records, produce)
	if err == nil {
		t.Fatal("部分失败必须返回批次错误")
	}
	if len(results) != 3 {
		t.Fatalf("部分失败仍必须返回等长结果，实际 %d", len(results))
	}
	if results[0].Err != nil || results[2].Err != nil {
		t.Fatalf("成功条目不应被标记失败: %+v", results)
	}
	if !errors.Is(results[1].Err, boom) {
		t.Fatalf("失败条目应保留原始错误，实际: %v", results[1].Err)
	}
}

// TestProducerPublishResultsPartialAckThenTimeout 是 F-03 的核心用例：
// 前两条已 ACK、后两条没有回调时 context 到期，结果必须逐条区分，而不是整批丢弃。
func TestProducerPublishResultsPartialAckThenTimeout(t *testing.T) {
	messages := resultTestMessages(4)
	records := resultTestRecords(messages)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	produce := func(_ context.Context, record *kgo.Record, promise func(*kgo.Record, error)) {
		switch string(record.Key) {
		case "key-0", "key-1":
			record.Partition = 3
			record.Offset = int64(record.Key[4] - '0')
			promise(record, nil)
		default:
			// 模拟 Broker 未在调用方预算内返回确认：回调被驱动推迟，甚至永不触发。
		}
	}

	results, err := collectPublishResults(ctx, messages, records, produce)
	if err == nil {
		t.Fatal("context 超时后仍有未确认消息，必须返回批次错误")
	}
	if len(results) != len(messages) {
		t.Fatalf("超时也必须返回等长结果，实际 %d", len(results))
	}

	// 已 ACK 的消息必须保留成功结果与分区/位移证据。
	for _, i := range []int{0, 1} {
		if results[i].Err != nil {
			t.Fatalf("已 ACK 的第 %d 条不得标记为失败/未知: %v", i, results[i].Err)
		}
		if results[i].Partition != 3 {
			t.Fatalf("已 ACK 的第 %d 条必须保留分区证据: %+v", i, results[i])
		}
	}
	// 未回调的消息必须标记为未知，且不能伪装成成功。
	for _, i := range []int{2, 3} {
		if !errors.Is(results[i].Err, mq.ErrPublishUnknown) {
			t.Fatalf("未确认的第 %d 条必须标记 ErrPublishUnknown，实际: %v", i, results[i].Err)
		}
	}
}

// TestProducerPublishResultsKeepsKnownFailureOnTimeout 覆盖「已明确失败 + 后续超时」：
// 明确失败证据不能被到期时的未知标记覆盖。
func TestProducerPublishResultsKeepsKnownFailureOnTimeout(t *testing.T) {
	messages := resultTestMessages(3)
	records := resultTestRecords(messages)
	boom := errors.New("broker rejected record")

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	produce := func(_ context.Context, record *kgo.Record, promise func(*kgo.Record, error)) {
		switch string(record.Key) {
		case "key-0":
			promise(record, nil)
		case "key-1":
			promise(record, boom)
		default:
			// 无回调
		}
	}

	results, err := collectPublishResults(ctx, messages, records, produce)
	if err == nil {
		t.Fatal("存在未知结果时必须返回批次错误")
	}
	if results[0].Err != nil {
		t.Fatalf("第 0 条应成功: %v", results[0].Err)
	}
	if !errors.Is(results[1].Err, boom) {
		t.Fatalf("第 1 条应保留明确失败，而不是未知: %v", results[1].Err)
	}
	if errors.Is(results[1].Err, mq.ErrPublishUnknown) {
		t.Fatal("明确失败不得被标记为结果未知")
	}
	if !errors.Is(results[2].Err, mq.ErrPublishUnknown) {
		t.Fatalf("第 2 条应标记未知: %v", results[2].Err)
	}
}

// TestProducerPublishResultsLateCallbackCannotMutateSnapshot 覆盖晚到回调的并发边界：
// Publish 返回后驱动才回调，返回值（快照）必须保持未知，不能被改写为成功。
// 该用例在 -race 下运行才能真正证明快照与回调之间没有数据竞争。
func TestProducerPublishResultsLateCallbackCannotMutateSnapshot(t *testing.T) {
	messages := resultTestMessages(2)
	records := resultTestRecords(messages)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var (
		mu      sync.Mutex
		pending []func(*kgo.Record, error)
	)
	produce := func(_ context.Context, record *kgo.Record, promise func(*kgo.Record, error)) {
		if string(record.Key) == "key-0" {
			promise(record, nil)
			return
		}
		mu.Lock()
		pending = append(pending, func(r *kgo.Record, err error) { promise(r, err) })
		mu.Unlock()
	}

	results, err := collectPublishResults(ctx, messages, records, produce)
	if err == nil {
		t.Fatal("存在未知结果时必须返回批次错误")
	}
	if !errors.Is(results[1].Err, mq.ErrPublishUnknown) {
		t.Fatalf("返回时应为未知: %v", results[1].Err)
	}

	// 迟到的成功回调：只能写入内部状态，不能再改写已经返回的快照。
	mu.Lock()
	late := pending[0]
	mu.Unlock()
	late(records[1], nil)

	if results[1].Err == nil {
		t.Fatal("晚到回调不得把已返回的未知结果改写为成功")
	}
	if !errors.Is(results[1].Err, mq.ErrPublishUnknown) {
		t.Fatalf("晚到回调后返回值仍应为未知: %v", results[1].Err)
	}
}

// TestProducerPublishResultsNoCallbackAllUnknown 覆盖全部回调缺失：
// 必须返回等长的未知结果，且不产生等待协程泄漏。
func TestProducerPublishResultsNoCallbackAllUnknown(t *testing.T) {
	messages := resultTestMessages(2)
	records := resultTestRecords(messages)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	results, err := collectPublishResults(ctx, messages, records,
		func(context.Context, *kgo.Record, func(*kgo.Record, error)) {})
	if err == nil {
		t.Fatal("没有回调时必须返回批次错误")
	}
	if len(results) != 2 {
		t.Fatalf("必须返回等长结果，实际 %d", len(results))
	}
	for i, result := range results {
		if !errors.Is(result.Err, mq.ErrPublishUnknown) {
			t.Fatalf("第 %d 条应为未知: %v", i, result.Err)
		}
	}
}
