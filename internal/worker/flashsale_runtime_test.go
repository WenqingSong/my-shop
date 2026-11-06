package worker

// B4 启动装配与生命周期测试。
//
// 这些用例不连接 MySQL/Redis/Kafka：通过注入构造器与启动器，直接断言「实际创建并运行
// 了哪些组件」，从而把 Scanner 与 Kafka 的互斥从日志/配置注释变成可执行门禁。
// 真实 Kafka/MySQL/Redis 的端到端验证见 test/e2e（B4 端到端用例）。

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/logic/flashsale"
	"cnb.cool/go-cloud-devops/my-shop/internal/mq"
	kafkamq "cnb.cool/go-cloud-devops/my-shop/internal/mq/kafka"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

// runtimeProbe 记录装配期间被创建/启动的组件，以及并发运行峰值。
type runtimeProbe struct {
	mu sync.Mutex

	producersCreated   int
	consumerClients    int
	outboxesCreated    int
	eventConsumers     int
	scannerStarts      map[string]int
	producerCloseCalls int
	consumerCloseCalls int

	// active/maxActive 统计「同时运行的 Consumer 实例数」，用于证明重建期间不重叠。
	active    int
	maxActive int
	runs      atomic.Int32

	// consumerBehavior 决定第 n 次 Run 的行为（n 从 1 开始）。
	consumerBehavior func(ctx context.Context, n int) error
	// outboxBehavior 决定 PublishBatch 的行为（call 从 1 开始）。
	outboxBehavior func(ctx context.Context, limit, call int) (flashsale.OutboxStats, error)
	// consumerClientErr 非 nil 时，重建 Consumer 客户端直接失败。
	consumerClientErr error
	// outboxEntered 在每次进入 PublishBatch 时收到一次信号（非阻塞）。
	outboxEntered chan struct{}
	// consumerStarted 在每次进入 Run 时收到一次信号（非阻塞）。
	consumerStarted chan struct{}
}

func newRuntimeProbe() *runtimeProbe {
	return &runtimeProbe{
		scannerStarts:   map[string]int{},
		outboxEntered:   make(chan struct{}, 32),
		consumerStarted: make(chan struct{}, 32),
	}
}

func (p *runtimeProbe) countScanner(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scannerStarts[name]++
}

func (p *runtimeProbe) counts() (producers, clients, outboxes, consumers int, scanners map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	scanners = make(map[string]int, len(p.scannerStarts))
	for k, v := range p.scannerStarts {
		scanners[k] = v
	}
	return p.producersCreated, p.consumerClients, p.outboxesCreated, p.eventConsumers, scanners
}

func (p *runtimeProbe) runtimeCounts() (active, maxActive int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active, p.maxActive
}

func (p *runtimeProbe) enterConsumer() {
	p.mu.Lock()
	p.active++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	p.mu.Unlock()
}

func (p *runtimeProbe) leaveConsumer() {
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
}

// fakeProducer 只用于验证创建/关闭，发布由 fakeOutbox 代替。
type fakeProducer struct {
	probe *runtimeProbe
}

func (p *fakeProducer) Publish(context.Context, ...mq.Message) ([]mq.PublishResult, error) {
	return nil, errors.New("fake producer: 发布路径不应由装配测试触发")
}

func (p *fakeProducer) Close(context.Context) error {
	p.probe.mu.Lock()
	p.probe.producerCloseCalls++
	p.probe.mu.Unlock()
	return nil
}

// fakeConsumerClient 实现 RebalanceConsumer；Poll/Commit 不会在装配测试中被调用。
type fakeConsumerClient struct {
	probe *runtimeProbe
	once  sync.Once
}

func (c *fakeConsumerClient) Poll(context.Context, int) ([]mq.Message, error) {
	return nil, errors.New("fake consumer: 拉取路径不应由装配测试触发")
}

func (c *fakeConsumerClient) Commit(context.Context, ...mq.Message) error { return nil }

func (c *fakeConsumerClient) AllowRebalance() {}

func (c *fakeConsumerClient) Close(context.Context) error {
	c.once.Do(func() {
		c.probe.mu.Lock()
		c.probe.consumerCloseCalls++
		c.probe.mu.Unlock()
	})
	return nil
}

// fakeOutbox 是可编程的发布循环替身。
type fakeOutbox struct {
	probe *runtimeProbe
	calls atomic.Int32
}

func (o *fakeOutbox) PublishBatch(ctx context.Context, limit int) (flashsale.OutboxStats, error) {
	call := int(o.calls.Add(1))
	select {
	case o.probe.outboxEntered <- struct{}{}:
	default:
	}
	o.probe.mu.Lock()
	behavior := o.probe.outboxBehavior
	o.probe.mu.Unlock()
	if behavior != nil {
		return behavior(ctx, limit, call)
	}
	return flashsale.OutboxStats{}, nil
}

// fakeConsumerRunner 是可编程的消费循环替身，负责统计并发峰值。
type fakeConsumerRunner struct {
	probe *runtimeProbe
}

func (c *fakeConsumerRunner) Run(ctx context.Context) error {
	c.probe.enterConsumer()
	defer c.probe.leaveConsumer()
	select {
	case c.probe.consumerStarted <- struct{}{}:
	default:
	}

	n := int(c.probe.runs.Add(1))
	c.probe.mu.Lock()
	behavior := c.probe.consumerBehavior
	c.probe.mu.Unlock()
	if behavior != nil {
		return behavior(ctx, n)
	}
	<-ctx.Done()
	return nil
}

// newProbeDeps 构造注入替身的依赖；scanner 启动器只等待 ctx 取消，模拟真实扫描器退出。
func newProbeDeps(cfg flashSaleRuntimeConfig, probe *runtimeProbe) flashSaleRuntimeDeps {
	waitCtx := func(ctx context.Context, wg *sync.WaitGroup) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ctx.Done()
		}()
	}
	return flashSaleRuntimeDeps{
		loadConfig: func(context.Context) (flashSaleRuntimeConfig, error) { return cfg, nil },
		newProducer: func(kafkamq.Config) (mq.Publisher, error) {
			probe.mu.Lock()
			probe.producersCreated++
			probe.mu.Unlock()
			return &fakeProducer{probe: probe}, nil
		},
		newConsumerClient: func(kafkamq.Config) (flashsale.RebalanceConsumer, error) {
			probe.mu.Lock()
			defer probe.mu.Unlock()
			if probe.consumerClientErr != nil {
				return nil, probe.consumerClientErr
			}
			probe.consumerClients++
			return &fakeConsumerClient{probe: probe}, nil
		},
		newOutbox: func(mq.Publisher, string, flashsale.OutboxOptions) (outboxRunner, error) {
			probe.mu.Lock()
			probe.outboxesCreated++
			probe.mu.Unlock()
			return &fakeOutbox{probe: probe}, nil
		},
		newEventConsumer: func(
			flashsale.RebalanceConsumer, mq.Publisher, flashsale.RequestConsumer, flashsale.ConsumerOptions,
		) (consumerRunner, error) {
			probe.mu.Lock()
			probe.eventConsumers++
			probe.mu.Unlock()
			return &fakeConsumerRunner{probe: probe}, nil
		},
		newRequestConsumer: func() flashsale.RequestConsumer { return nil },
		startOrderCancel: func(ctx context.Context, wg *sync.WaitGroup) {
			probe.countScanner("order_cancel")
			waitCtx(ctx, wg)
		},
		startReconcile: func(ctx context.Context, wg *sync.WaitGroup) {
			probe.countScanner("reconcile")
			waitCtx(ctx, wg)
		},
		startConsumeScanner: func(ctx context.Context, wg *sync.WaitGroup) {
			probe.countScanner("consume")
			waitCtx(ctx, wg)
		},
		consumerRestartBase:  10 * time.Millisecond,
		consumerRestartMax:   40 * time.Millisecond,
		consumerCloseTimeout: time.Second,
	}
}

func testKafkaConfig() kafkamq.Config {
	return kafkamq.Config{
		Enabled:       true,
		Brokers:       []string{"127.0.0.1:29092"},
		Topic:         "surgecart.test.flashsale.requested.v1",
		DLQTopic:      "surgecart.test.flashsale.requested.v1.dlq",
		ConsumerGroup: "surgecart-test-group",
	}
}

func testRuntimeConfig(mode string, kafkaEnabled bool) flashSaleRuntimeConfig {
	kafkaCfg := testKafkaConfig()
	kafkaCfg.Enabled = kafkaEnabled
	return flashSaleRuntimeConfig{
		Mode:  mode,
		Kafka: kafkaCfg,
		Publisher: flashSalePublisherConfig{
			BatchSize:    10,
			PollInterval: 5 * time.Millisecond,
			ErrorBackoff: 10 * time.Millisecond,
		},
		ShutdownTimeout: 3 * time.Second,
	}
}

// waitFor 在超时前轮询条件，避免用固定 sleep 造成偶发失败。
func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", desc)
}

// ---------------------------------------------------------------------------
// 配置校验
// ---------------------------------------------------------------------------

// TestResolveFlashSaleRuntimeConfig 覆盖模式校验与默认值：配置矛盾必须启动失败，
// 不允许静默退回另一种模式。
func TestResolveFlashSaleRuntimeConfig(t *testing.T) {
	t.Run("空模式使用默认 db_scanner", func(t *testing.T) {
		cfg, err := resolveFlashSaleRuntimeConfig(flashSaleRuntimeConfig{})
		if err != nil {
			t.Fatalf("空配置应使用默认值: %v", err)
		}
		if cfg.Mode != flashSaleModeDBScanner {
			t.Fatalf("默认模式应为 %s，实际 %s", flashSaleModeDBScanner, cfg.Mode)
		}
		if cfg.Kafka.Enabled {
			t.Fatal("默认不得启用 Kafka")
		}
		if cfg.Publisher.BatchSize != defaultFlashSalePublisherBatchSize ||
			cfg.Publisher.PollInterval != defaultFlashSalePublisherPollInterval ||
			cfg.Publisher.ErrorBackoff != defaultFlashSalePublisherErrorBackoff ||
			cfg.ShutdownTimeout != defaultFlashSaleShutdownTimeout {
			t.Fatalf("默认运行参数未补齐: %+v", cfg)
		}
	})

	t.Run("db_scanner 与 enabled=true 矛盾", func(t *testing.T) {
		if _, err := resolveFlashSaleRuntimeConfig(testRuntimeConfig(flashSaleModeDBScanner, true)); err == nil ||
			!strings.Contains(err.Error(), "配置矛盾") {
			t.Fatalf("应拒绝 db_scanner + kafka.enabled=true，实际: %v", err)
		}
	})

	t.Run("kafka 模式要求 enabled=true", func(t *testing.T) {
		if _, err := resolveFlashSaleRuntimeConfig(testRuntimeConfig(flashSaleModeKafka, false)); err == nil ||
			!strings.Contains(err.Error(), "配置矛盾") {
			t.Fatalf("应拒绝 kafka + enabled=false，实际: %v", err)
		}
	})

	t.Run("未知模式直接失败", func(t *testing.T) {
		if _, err := resolveFlashSaleRuntimeConfig(testRuntimeConfig("hot_standby", false)); err == nil ||
			!strings.Contains(err.Error(), "不支持") {
			t.Fatalf("应拒绝未知模式，实际: %v", err)
		}
	})

	t.Run("kafka 模式校验 Kafka 配置", func(t *testing.T) {
		cfg := testRuntimeConfig(flashSaleModeKafka, true)
		cfg.Kafka.DLQTopic = cfg.Kafka.Topic
		if _, err := resolveFlashSaleRuntimeConfig(cfg); err == nil {
			t.Fatal("DLQ 与业务 Topic 相同应被拒绝")
		}
	})

	t.Run("批量上限收敛", func(t *testing.T) {
		cfg := testRuntimeConfig(flashSaleModeDBScanner, false)
		cfg.Publisher.BatchSize = maxFlashSaleRuntimeBatchSize + 100
		got, err := resolveFlashSaleRuntimeConfig(cfg)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got.Publisher.BatchSize != maxFlashSaleRuntimeBatchSize {
			t.Fatalf("批量应截断到 %d，实际 %d", maxFlashSaleRuntimeBatchSize, got.Publisher.BatchSize)
		}
	})
}

// ---------------------------------------------------------------------------
// 模式装配与互斥
// ---------------------------------------------------------------------------

// TestFlashSaleRuntimeDBScannerMode 证明 db_scanner 只启动三个 MySQL 扫描器，
// 不创建 Producer / Outbox / Consumer，也不需要 Kafka 配置。
func TestFlashSaleRuntimeDBScannerMode(t *testing.T) {
	probe := newRuntimeProbe()
	cfg := testRuntimeConfig(flashSaleModeDBScanner, false)
	// Kafka 地址留空：该模式不得触碰 Kafka 配置/客户端。
	cfg.Kafka = kafkamq.Config{Enabled: false}

	rt, err := startFlashSaleRuntime(context.Background(), newProbeDeps(cfg, probe))
	if err != nil {
		t.Fatalf("db_scanner 启动失败: %v", err)
	}

	producers, clients, outboxes, consumers, scanners := probe.counts()
	if producers != 0 || clients != 0 || outboxes != 0 || consumers != 0 {
		t.Fatalf("db_scanner 不得创建 Kafka 组件: producer=%d client=%d outbox=%d consumer=%d",
			producers, clients, outboxes, consumers)
	}
	for _, name := range []string{"order_cancel", "reconcile", "consume"} {
		if scanners[name] != 1 {
			t.Fatalf("db_scanner 应启动 %s 扫描器一次，实际 %d", name, scanners[name])
		}
	}

	if err := rt.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// 幂等：重复关闭不应再等待或报错。
	if err := rt.Shutdown(); err != nil {
		t.Fatalf("重复 Shutdown 应返回相同结果: %v", err)
	}
}

// TestFlashSaleRuntimeKafkaModeStartsComponents 证明 kafka 模式下 Publisher 与
// Consumer 真正运行（PublishBatch / Run 被调用），且 MySQL 消费扫描器不启动。
func TestFlashSaleRuntimeKafkaModeStartsComponents(t *testing.T) {
	probe := newRuntimeProbe()
	cfg := testRuntimeConfig(flashSaleModeKafka, true)

	rt, err := startFlashSaleRuntime(context.Background(), newProbeDeps(cfg, probe))
	if err != nil {
		t.Fatalf("kafka 模式启动失败: %v", err)
	}

	waitFor(t, time.Second, "Outbox 发布循环运行", func() bool {
		select {
		case <-probe.outboxEntered:
			return true
		default:
			return false
		}
	})
	waitFor(t, time.Second, "Consumer.Run 运行", func() bool {
		select {
		case <-probe.consumerStarted:
			return true
		default:
			return false
		}
	})

	producers, clients, outboxes, consumers, scanners := probe.counts()
	if producers != 1 || clients != 1 || outboxes != 1 || consumers != 1 {
		t.Fatalf("kafka 模式组件创建数量不符: producer=%d client=%d outbox=%d consumer=%d",
			producers, clients, outboxes, consumers)
	}
	if scanners["consume"] != 0 {
		t.Fatal("kafka 模式不得启动 MySQL 消费扫描器（会与 Consumer 争抢同一批 queued 行）")
	}
	if scanners["order_cancel"] != 1 || scanners["reconcile"] != 1 {
		t.Fatalf("与分发模式无关的扫描器仍应运行: %+v", scanners)
	}

	if err := rt.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	probe.mu.Lock()
	producerClosed := probe.producerCloseCalls
	consumerClosed := probe.consumerCloseCalls
	probe.mu.Unlock()
	if producerClosed != 1 {
		t.Fatalf("Producer 应关闭一次，实际 %d", producerClosed)
	}
	if consumerClosed != 1 {
		t.Fatalf("Consumer 客户端应关闭一次，实际 %d", consumerClosed)
	}
}

// TestFlashSaleRuntimeModeContradictionDoesNotStartAnything 证明配置矛盾在启动期失败，
// 且不会留下任何后台 goroutine/客户端。
func TestFlashSaleRuntimeModeContradictionDoesNotStartAnything(t *testing.T) {
	cases := []flashSaleRuntimeConfig{
		testRuntimeConfig(flashSaleModeDBScanner, true),
		testRuntimeConfig(flashSaleModeKafka, false),
		testRuntimeConfig("unknown", false),
	}
	for _, cfg := range cases {
		probe := newRuntimeProbe()
		if _, err := startFlashSaleRuntime(context.Background(), newProbeDeps(cfg, probe)); err == nil {
			t.Fatalf("配置 %+v 应在启动期失败", cfg)
		}
		producers, clients, outboxes, consumers, scanners := probe.counts()
		if producers+clients+outboxes+consumers+len(scanners) != 0 {
			t.Fatalf("启动失败不得创建任何组件: %+v", scanners)
		}
	}
}

// TestFlashSaleRuntimeStartupFailureClosesProducer 覆盖启动中途失败：
// Producer 已创建、Consumer 客户端构造失败时，必须关闭 Producer。
func TestFlashSaleRuntimeStartupFailureClosesProducer(t *testing.T) {
	probe := newRuntimeProbe()
	probe.consumerClientErr = errors.New("kafka 不可用")
	cfg := testRuntimeConfig(flashSaleModeKafka, true)

	if _, err := startFlashSaleRuntime(context.Background(), newProbeDeps(cfg, probe)); err == nil {
		t.Fatal("Consumer 客户端构造失败应导致启动失败")
	}
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if probe.producersCreated != 1 {
		t.Fatalf("应创建 Producer 一次，实际 %d", probe.producersCreated)
	}
	if probe.producerCloseCalls != 1 {
		t.Fatalf("启动失败后必须关闭 Producer，实际关闭 %d 次", probe.producerCloseCalls)
	}
}

// ---------------------------------------------------------------------------
// Consumer 异常恢复
// ---------------------------------------------------------------------------

// TestFlashSaleRuntimeConsumerRestartIsSerialAndBounded 覆盖：
//   - Run 意外退出后按有界退避重建；
//   - 重建时旧实例已退出、旧客户端已关闭（同一时刻最多一个 Consumer）；
//   - 关闭应用后监督循环立即停止，不再重建。
func TestFlashSaleRuntimeConsumerRestartIsSerialAndBounded(t *testing.T) {
	probe := newRuntimeProbe()
	// 前三次立即失败，第四次阻塞到 ctx 取消：模拟「持续故障后恢复」。
	probe.consumerBehavior = func(ctx context.Context, n int) error {
		switch n {
		case 1, 2, 3:
			return errors.New("broker 暂时不可用")
		default:
			<-ctx.Done()
			return nil
		}
	}
	cfg := testRuntimeConfig(flashSaleModeKafka, true)

	rt, err := startFlashSaleRuntime(context.Background(), newProbeDeps(cfg, probe))
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}

	waitFor(t, 3*time.Second, "Consumer 完成三次重建", func() bool {
		probe.mu.Lock()
		defer probe.mu.Unlock()
		return probe.eventConsumers >= 4
	})

	active, maxActive := probe.runtimeCounts()
	if maxActive != 1 {
		t.Fatalf("重建期间不得出现两个同时运行的 Consumer，峰值 %d", maxActive)
	}
	if active != 1 {
		t.Fatalf("第四次 Run 应在运行中，实际 active=%d", active)
	}

	probe.mu.Lock()
	clients := probe.consumerClients
	closes := probe.consumerCloseCalls
	probe.mu.Unlock()
	if clients != 4 {
		t.Fatalf("应重建 4 个客户端，实际 %d", clients)
	}
	// 前三个已失败退出并关闭；第四个仍在运行。
	if closes != 3 {
		t.Fatalf("每次重建前必须关闭旧客户端：期望 3 次，实际 %d", closes)
	}

	if err := rt.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	probe.mu.Lock()
	closesAfterShutdown := probe.consumerCloseCalls
	probe.mu.Unlock()
	if closesAfterShutdown != 4 {
		t.Fatalf("关闭时最后一个客户端也应关闭，实际 %d", closesAfterShutdown)
	}
	if _, maxAfter := probe.runtimeCounts(); maxAfter != 1 {
		t.Fatalf("全程并发峰值必须为 1，实际 %d", maxAfter)
	}
}

// TestFlashSaleRuntimeShutdownBoundedWhenConsumerStuck 覆盖 Consumer.Run 不响应取消：
// Shutdown 必须在预算内返回（记录超时），并且仍然关闭 Producer，不能永久阻塞。
func TestFlashSaleRuntimeShutdownBoundedWhenConsumerStuck(t *testing.T) {
	probe := newRuntimeProbe()
	release := make(chan struct{})
	probe.consumerBehavior = func(context.Context, int) error {
		// 故意忽略 ctx：模拟驱动/业务代码异常阻塞。
		<-release
		return nil
	}
	cfg := testRuntimeConfig(flashSaleModeKafka, true)
	cfg.ShutdownTimeout = 150 * time.Millisecond

	rt, err := startFlashSaleRuntime(context.Background(), newProbeDeps(cfg, probe))
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	waitFor(t, time.Second, "Consumer 启动", func() bool {
		select {
		case <-probe.consumerStarted:
			return true
		default:
			return false
		}
	})

	started := time.Now()
	err = rt.Shutdown()
	elapsed := time.Since(started)
	close(release)
	// 放行后等待监督循环真正退出，避免残留 goroutine 影响后续用例。
	waitFor(t, time.Second, "卡住的 Consumer 关闭", func() bool {
		probe.mu.Lock()
		defer probe.mu.Unlock()
		return probe.consumerCloseCalls >= 1
	})

	if err == nil {
		t.Fatal("Consumer 未在预算内退出时必须返回超时错误")
	}
	if !strings.Contains(err.Error(), "关闭预算") {
		t.Fatalf("错误应明确指出关闭预算超时，实际: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Shutdown 耗时 %v，关闭预算未生效", elapsed)
	}
	probe.mu.Lock()
	closed := probe.producerCloseCalls
	probe.mu.Unlock()
	if closed != 1 {
		t.Fatalf("即使 Consumer 卡住，也必须关闭 Producer；实际关闭 %d 次", closed)
	}
}

// TestFlashSaleRuntimeNoGoroutineLeak 覆盖正常关闭后后台 goroutine 回收。
func TestFlashSaleRuntimeNoGoroutineLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()

	probe := newRuntimeProbe()
	cfg := testRuntimeConfig(flashSaleModeKafka, true)
	probe.outboxBehavior = func(ctx context.Context, _ int, _ int) (flashsale.OutboxStats, error) {
		return flashsale.OutboxStats{}, nil
	}
	rt, err := startFlashSaleRuntime(context.Background(), newProbeDeps(cfg, probe))
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	waitFor(t, time.Second, "组件运行", func() bool {
		select {
		case <-probe.consumerStarted:
			return true
		default:
			return false
		}
	})
	if err := rt.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// 允许测试框架/运行时自身协程的少量波动；若发布循环/监督循环泄漏，数量会持续偏高。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("关闭后 goroutine 未回收：baseline=%d current=%d", baseline, runtime.NumGoroutine())
}

// TestLoadFlashSaleRuntimeConfigFromEnv 覆盖新增配置项的读取路径：
// 环境变量优先、Go duration 严格解析，且不依赖配置文件是否存在。
func TestLoadFlashSaleRuntimeConfigFromEnv(t *testing.T) {
	t.Setenv("FLASH_SALE_DISPATCH_MODE", "kafka")
	t.Setenv("FLASH_SALE_SHUTDOWN_TIMEOUT", "45s")
	t.Setenv("FLASH_SALE_KAFKA_ENABLED", "true")
	t.Setenv("FLASH_SALE_KAFKA_BROKERS", "127.0.0.1:29092")
	t.Setenv("FLASH_SALE_KAFKA_TOPIC", "surgecart.test.requested")
	t.Setenv("FLASH_SALE_KAFKA_DLQ_TOPIC", "surgecart.test.requested.dlq")
	t.Setenv("FLASH_SALE_KAFKA_CONSUMER_GROUP", "surgecart-test-group")
	t.Setenv("FLASH_SALE_KAFKA_PUBLISHER_BATCH_SIZE", "250")
	t.Setenv("FLASH_SALE_KAFKA_PUBLISHER_POLL_INTERVAL", "150ms")
	t.Setenv("FLASH_SALE_KAFKA_PUBLISHER_ERROR_BACKOFF", "3s")

	cfg, err := loadFlashSaleRuntimeConfig(context.Background())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Mode != flashSaleModeKafka || !cfg.Kafka.Enabled {
		t.Fatalf("模式/开关读取错误: %+v", cfg)
	}
	if cfg.Publisher.BatchSize != 250 ||
		cfg.Publisher.PollInterval != 150*time.Millisecond ||
		cfg.Publisher.ErrorBackoff != 3*time.Second {
		t.Fatalf("publisher 配置读取错误: %+v", cfg.Publisher)
	}
	if cfg.ShutdownTimeout != 45*time.Second {
		t.Fatalf("shutdown_timeout 读取错误: %s", cfg.ShutdownTimeout)
	}
}

// TestLoadFlashSaleRuntimeConfigRejectsInvalidDuration 覆盖 duration 拼写错误必须启动失败，
// 不能静默落到默认值（否则运维以为改生效了）。
func TestLoadFlashSaleRuntimeConfigRejectsInvalidDuration(t *testing.T) {
	t.Setenv("FLASH_SALE_DISPATCH_MODE", "db_scanner")
	t.Setenv("FLASH_SALE_KAFKA_ENABLED", "false")
	t.Setenv("FLASH_SALE_KAFKA_PUBLISHER_POLL_INTERVAL", "200")
	if _, err := loadFlashSaleRuntimeConfig(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "Go duration") {
		t.Fatalf("非法 duration 应报错并指出配置项，实际: %v", err)
	}
}
