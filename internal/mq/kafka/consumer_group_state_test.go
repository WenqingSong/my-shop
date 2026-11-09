package kafka

// T2-02-B：Consumer Group 成员资格状态机的纯内存测试（不连接 Broker）。
//
// 覆盖的关键判断：
//   - 初始化（NewConsumer 成功）不得被当作「已加入消费组」；
//   - OnPartitionsAssigned 才是加入证据，且**空分区分配同样代表已加入**
//     （franz-go 明确保证「即使没有新分区也总是回调」，把空分配当未加入是错的）；
//   - OnPartitionsRevoked / OnPartitionsLost / Run 退出 / Shutdown 都归零；
//   - 状态并发安全（race 门禁执行本文件）；
//   - 首次加入日志每个客户端实例只发一次（rebalance 不重复刷「首次加入」）。

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// consumerJoinedMetricName 是 T2-02-B 的指标名。
const consumerJoinedMetricName = "flashsale_consumer_joined"

// consumerJoinedGauge 读取成员资格指标的当前值；未导出时 ok=false。
func consumerJoinedGauge(t *testing.T) (value float64, ok bool) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, fam := range families {
		if fam.GetName() != consumerJoinedMetricName {
			continue
		}
		for _, m := range fam.GetMetric() {
			if m.GetGauge() != nil {
				return m.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

func testConsumerConfig() Config {
	return Config{
		Enabled:       true,
		Brokers:       []string{"127.0.0.1:1"},
		Topic:         "surgecart.test.flashsale.requested.v1",
		DLQTopic:      "surgecart.test.flashsale.requested.v1.dlq",
		ConsumerGroup: "surgecart-test-group",
	}
}

// TestConsumerGroupJoinedStateMachine 覆盖 T2-02-B 的完整状态转换：
//
//	初始化 0 → OnPartitionsAssigned（空分配）1 → 撤销 0 → 重新加入 1
//	→ OnPartitionsLost 0 → Close 0
func TestConsumerGroupJoinedStateMachine(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	ctx := context.Background()

	c, err := NewConsumer(testConsumerConfig())
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	if c.joined.Load() {
		t.Fatal("构造成功不得被视为已加入消费组")
	}
	if v, ok := consumerJoinedGauge(t); !ok || v != 0 {
		t.Fatalf("初始化后指标=(%v,%v) want (true,0)", v, ok)
	}

	// 空分配：franz-go 在 JoinGroup/SyncGroup 成功后总会回调，即使没有新分区。
	c.onPartitionsAssigned(ctx, nil, map[string][]int32{})
	if !c.joined.Load() {
		t.Fatal("空分区分配的 OnPartitionsAssigned 同样代表已加入消费组")
	}
	if v, ok := consumerJoinedGauge(t); !ok || v != 1 {
		t.Fatalf("加入后指标=(%v,%v) want (true,1)", v, ok)
	}
	if !c.joinLogged.Load() {
		t.Fatal("首次成功加入必须输出一次 flashsale_consumer_joined 日志")
	}

	// rebalance：撤销 → 0；重新加入 → 1，但不得重复输出「首次加入」日志。
	c.onPartitionsRevoked(ctx, nil, map[string][]int32{"topic": {0, 1}})
	if c.joined.Load() {
		t.Fatal("会话结束后不得仍标记为已加入")
	}
	if v, _ := consumerJoinedGauge(t); v != 0 {
		t.Fatalf("撤销后指标=%v want 0", v)
	}

	c.onPartitionsAssigned(ctx, nil, map[string][]int32{"topic": {0}})
	if v, _ := consumerJoinedGauge(t); v != 1 {
		t.Fatalf("重新加入后指标=%v want 1", v)
	}
	if !c.joinLogged.Load() {
		t.Fatal("重新加入后 joinLogged 不应被重置（否则每次 rebalance 都会刷首次加入日志）")
	}

	// 致命组错误：成员资格丢失。
	c.onPartitionsLost(ctx, nil, map[string][]int32{"topic": {0}})
	if c.joined.Load() {
		t.Fatal("OnPartitionsLost 后不得仍标记为已加入")
	}
	if v, _ := consumerJoinedGauge(t); v != 0 {
		t.Fatalf("丢失成员资格后指标=%v want 0", v)
	}

	// 从未加入也可能收到 OnPartitionsLost（franz-go 语义）：不得 panic、状态仍为 0。
	fresh, err := NewConsumer(testConsumerConfig())
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	fresh.onPartitionsLost(ctx, nil, nil)
	if fresh.joined.Load() {
		t.Fatal("未加入时收到 OnPartitionsLost 不得把状态置为已加入")
	}

	// 关闭：离组不可达时不阻塞测试，状态必须为 0。
	closeCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	_ = c.Close(closeCtx)
	cancel()
	if c.joined.Load() {
		t.Fatal("Close 后不得仍标记为已加入")
	}
	if v, _ := consumerJoinedGauge(t); v != 0 {
		t.Fatalf("Close 后指标=%v want 0", v)
	}
}

// TestConsumerGroupJoinedConcurrentCallbacksRaceFree 在 -race 下覆盖回调并发路径：
// 组回调来自 franz-go 管理协程，Run 退出/Shutdown 又会从监督协程置 0，
// 两者并发写状态必须是安全的（atomic + 指标内 RWMutex）。
func TestConsumerGroupJoinedConcurrentCallbacksRaceFree(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	ctx := context.Background()

	c, err := NewConsumer(testConsumerConfig())
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				c.onPartitionsAssigned(ctx, nil, map[string][]int32{"topic": {int32(j)}})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				c.onPartitionsRevoked(ctx, nil, map[string][]int32{"topic": {int32(j)}})
				c.onPartitionsLost(ctx, nil, nil)
			}
		}()
	}
	wg.Wait()

	if v, ok := consumerJoinedGauge(t); !ok {
		t.Fatal("并发回调后指标应已导出")
	} else if v != 0 && v != 1 {
		t.Fatalf("指标值只能是 0 或 1，实际 %v", v)
	}
}
