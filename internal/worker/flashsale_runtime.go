// 秒杀后台链路（B4）：把 Scanner / Outbox Publisher / Kafka Consumer 接入 serve 生命周期。
//
// 两种运行模式严格互斥（dispatch_mode）：
//
//	db_scanner：MySQL 消费扫描器（ConsumeQueued），不创建任何 Kafka 组件；
//	kafka      ：Producer + Outbox Publisher + Kafka Consumer，不启动 MySQL 消费扫描器。
//
// 互斥的必要性：Scanner 与 Consumer 都会处理 status=queued 的同一批请求行，
// 同时运行会争抢行锁、重复消耗 retry_count 预算。模式在进程启动时确定，
// 运行中切换通过重启生效（本批次不做热切换）。
package worker

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gogf/gf/v2/container/gvar"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/genv"

	"cnb.cool/go-cloud-devops/my-shop/internal/logic/flashsale"
	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	"cnb.cool/go-cloud-devops/my-shop/internal/mq"
	kafkamq "cnb.cool/go-cloud-devops/my-shop/internal/mq/kafka"
)

// 秒杀分发模式。db_scanner 是默认值，保持既有 MySQL 出队表扫描行为。
const (
	flashSaleModeDBScanner = "db_scanner"
	flashSaleModeKafka     = "kafka"
)

// 运行参数默认值与硬上限。
const (
	// defaultFlashSaleShutdownTimeout 是秒杀后台组件的统一关闭预算。
	// 它约束「等待 Publisher 轮次、等待 Consumer.Run、关闭客户端、停止扫描器」的总时长。
	defaultFlashSaleShutdownTimeout = 30 * time.Second

	defaultFlashSalePublisherBatchSize    = 100
	defaultFlashSalePublisherPollInterval = 200 * time.Millisecond
	defaultFlashSalePublisherErrorBackoff = 2 * time.Second
	// maxFlashSalePublisherErrorBackoff 限制发布循环的失败退避：
	// Broker 持续不可用时既不能紧密重试，也不能把恢复时间拉到不可接受的长度。
	maxFlashSalePublisherErrorBackoff = 30 * time.Second
	maxFlashSaleRuntimeBatchSize      = 1000

	// Consumer 意外退出后的重建退避：1s 起步、30s 封顶。
	// 连续失败不会形成高速重启风暴；一次运行超过 resetAfter 后视为稳定，重置退避。
	defaultConsumerRestartBase = time.Second
	defaultConsumerRestartMax  = 30 * time.Second
	consumerRestartResetAfter  = time.Minute
	// defaultConsumerCloseTimeout 约束单次 Consumer.Close（离组可能因 Broker 不可达而等待）。
	defaultConsumerCloseTimeout = 5 * time.Second
)

// flashSalePublisherConfig 是 Outbox 后台发布循环的参数。
type flashSalePublisherConfig struct {
	BatchSize    int
	PollInterval time.Duration
	ErrorBackoff time.Duration
}

// flashSaleRuntimeConfig 是模式选择与后台循环所需的完整配置。
type flashSaleRuntimeConfig struct {
	Mode            string
	Kafka           kafkamq.Config
	Publisher       flashSalePublisherConfig
	ShutdownTimeout time.Duration
}

// outboxRunner 是发布循环依赖的最小能力，便于用测试替身验证装配与生命周期。
type outboxRunner interface {
	PublishBatch(ctx context.Context, limit int) (flashsale.OutboxStats, error)
}

// consumerRunner 是 Consumer 监督循环依赖的最小能力。
type consumerRunner interface {
	Run(ctx context.Context) error
}

// flashSaleRuntimeDeps 是装配入口的可替换依赖。
//
// 生产环境使用 defaultFlashSaleRuntimeDeps；测试注入可控的构造器与启动器，
// 从而在不连接 MySQL/Redis/Kafka 的情况下断言「哪个组件被创建/运行」。
// 这比只检查日志或配置注释更能证明模式互斥。
type flashSaleRuntimeDeps struct {
	loadConfig func(ctx context.Context) (flashSaleRuntimeConfig, error)

	newProducer        func(kafkamq.Config) (mq.Publisher, error)
	newConsumerClient  func(kafkamq.Config) (flashsale.RebalanceConsumer, error)
	newOutbox          func(mq.Publisher, string, flashsale.OutboxOptions) (outboxRunner, error)
	newEventConsumer   func(flashsale.RebalanceConsumer, mq.Publisher, flashsale.RequestConsumer, flashsale.ConsumerOptions) (consumerRunner, error)
	newRequestConsumer func() flashsale.RequestConsumer

	startOrderCancel    func(ctx context.Context, wg *sync.WaitGroup)
	startReconcile      func(ctx context.Context, wg *sync.WaitGroup)
	startConsumeScanner func(ctx context.Context, wg *sync.WaitGroup)

	// 测试注入的可调时长；为零时使用生产默认值。
	consumerRestartBase  time.Duration
	consumerRestartMax   time.Duration
	consumerCloseTimeout time.Duration
}

// defaultFlashSaleRuntimeDeps 返回生产装配使用的依赖。
func defaultFlashSaleRuntimeDeps() flashSaleRuntimeDeps {
	return flashSaleRuntimeDeps{
		loadConfig: loadFlashSaleRuntimeConfig,
		newProducer: func(cfg kafkamq.Config) (mq.Publisher, error) {
			return kafkamq.NewProducer(cfg)
		},
		newConsumerClient: func(cfg kafkamq.Config) (flashsale.RebalanceConsumer, error) {
			return kafkamq.NewConsumer(cfg)
		},
		newOutbox: func(pub mq.Publisher, topic string, opts flashsale.OutboxOptions) (outboxRunner, error) {
			return flashsale.NewOutboxPublisher(pub, topic, opts)
		},
		newEventConsumer: func(
			client flashsale.RebalanceConsumer, dlq mq.Publisher,
			svc flashsale.RequestConsumer, opts flashsale.ConsumerOptions,
		) (consumerRunner, error) {
			return flashsale.NewRequestedEventConsumer(client, dlq, svc, opts)
		},
		// 消费路径只访问 MySQL/Redis，不经过下单闸门熔断器，因此新建实例是安全的；
		// 避免把 internal/logic 的具体类型断言暴露到组合根。
		newRequestConsumer:  func() flashsale.RequestConsumer { return flashsale.New() },
		startOrderCancel:    StartOrderCancelScanner,
		startReconcile:      StartFlashSaleReconcileScanner,
		startConsumeScanner: StartFlashSaleConsumeScanner,
	}
}

// FlashSaleRuntime 是秒杀后台组件生命周期句柄。
//
// 生命周期约定：
//   - 启动失败时不会留下运行中的 goroutine 或未关闭的 Kafka 客户端；
//   - Shutdown 按「Publisher → Consumer → Producer → Scanner」顺序停止，
//     每一步都受统一 shutdown budget 约束；
//   - Shutdown 可重复调用（幂等），重复调用返回首次结果。
type FlashSaleRuntime struct {
	mode            string
	shutdownTimeout time.Duration

	// baseCtx 是扫描器以及 Publisher/Consumer 子 ctx 的父 ctx；
	// 子组件各自持有可单独取消的 ctx，用于在 Shutdown 中控制停止顺序。
	baseCtx    context.Context
	baseCancel context.CancelFunc

	publisherCancel context.CancelFunc
	consumerCancel  context.CancelFunc

	publisherWG sync.WaitGroup
	consumerWG  sync.WaitGroup
	scannerWG   sync.WaitGroup

	producer mq.Publisher

	kafkaConfig  kafkamq.Config
	consumerOpts flashsale.ConsumerOptions

	publisherBatchSize    int
	publisherPollInterval time.Duration
	publisherErrorBackoff time.Duration

	consumerRestartBase  time.Duration
	consumerRestartMax   time.Duration
	consumerCloseTimeout time.Duration

	shutdownOnce sync.Once
	shutdownErr  error
}

// StartFlashSaleRuntime 按 flash_sale.dispatch_mode 启动唯一一套秒杀消费入口。
//
// 调用方（serve）必须在启动 HTTP 服务前调用：Kafka 模式下配置非法、Topic 缺失等问题
// 必须 fail-fast，不能带着「看起来启动了、实际没有消费者」的状态对外提供服务。
// 返回的 runtime 必须在 serve 退出时调用 Shutdown。
func StartFlashSaleRuntime(ctx context.Context) (*FlashSaleRuntime, error) {
	return startFlashSaleRuntime(ctx, defaultFlashSaleRuntimeDeps())
}

// startFlashSaleRuntime 是可注入依赖的装配实现（测试使用）。
func startFlashSaleRuntime(ctx context.Context, deps flashSaleRuntimeDeps) (*FlashSaleRuntime, error) {
	raw, err := deps.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := resolveFlashSaleRuntimeConfig(raw)
	if err != nil {
		return nil, err
	}

	baseCtx, baseCancel := context.WithCancel(ctx)
	rt := &FlashSaleRuntime{
		mode:                  cfg.Mode,
		shutdownTimeout:       cfg.ShutdownTimeout,
		baseCtx:               baseCtx,
		baseCancel:            baseCancel,
		publisherBatchSize:    cfg.Publisher.BatchSize,
		publisherPollInterval: cfg.Publisher.PollInterval,
		publisherErrorBackoff: cfg.Publisher.ErrorBackoff,
		kafkaConfig:           cfg.Kafka,
		consumerOpts:          flashsale.ConsumerOptions{DLQTopic: cfg.Kafka.DLQTopic},
		consumerRestartBase:   deps.consumerRestartBase,
		consumerRestartMax:    deps.consumerRestartMax,
		consumerCloseTimeout:  deps.consumerCloseTimeout,
	}
	rt.applyTimingDefaults()

	switch cfg.Mode {
	case flashSaleModeDBScanner:
		// 原有路径：三个 MySQL 扫描器，不触碰 Kafka 客户端/后台循环。
		deps.startOrderCancel(baseCtx, &rt.scannerWG)
		deps.startReconcile(baseCtx, &rt.scannerWG)
		deps.startConsumeScanner(baseCtx, &rt.scannerWG)
		logx.Info(ctx, "flashsale_runtime_started", "秒杀链路以 DB Scanner 模式启动", logx.Fields{
			"mode": cfg.Mode,
		})
		return rt, nil
	case flashSaleModeKafka:
		if err := rt.startKafka(baseCtx, deps); err != nil {
			// 清理已经创建的组件；baseCancel 同时停止可能已启动的扫描器（本模式先不启动）。
			baseCancel()
			rt.closeProducer()
			return nil, err
		}
		return rt, nil
	default:
		// resolveFlashSaleRuntimeConfig 已拒绝未知模式；此处防御未来新增分支遗漏。
		baseCancel()
		return nil, fmt.Errorf("不支持的秒杀分发模式 %q", cfg.Mode)
	}
}

// startKafka 创建并启动 Kafka 模式组件。任一构造失败都不留下后台 goroutine。
func (r *FlashSaleRuntime) startKafka(ctx context.Context, deps flashSaleRuntimeDeps) error {
	producer, err := deps.newProducer(r.kafkaConfig)
	if err != nil {
		return fmt.Errorf("初始化 Kafka Producer: %w", err)
	}
	r.producer = producer

	outbox, err := deps.newOutbox(producer, r.kafkaConfig.Topic, flashsale.OutboxOptions{
		BatchSize: r.publisherBatchSize,
	})
	if err != nil {
		return fmt.Errorf("初始化秒杀 Outbox Publisher: %w", err)
	}

	client, err := deps.newConsumerClient(r.kafkaConfig)
	if err != nil {
		return fmt.Errorf("初始化 Kafka Consumer: %w", err)
	}

	// 死信与正常事件共用一个 Producer：它已经过 acks=all/幂等写入配置，
	// 单独再建一个客户端只会增加连接与关闭顺序的复杂度。
	consumer, err := deps.newEventConsumer(client, producer, deps.newRequestConsumer(), r.consumerOpts)
	if err != nil {
		closeConsumerClientBounded(client, r.consumerCloseTimeout)
		return fmt.Errorf("初始化秒杀 Kafka Consumer: %w", err)
	}

	// Publisher：独立可取消 ctx，Shutdown 时先停它、等当前轮次结束。
	pubCtx, pubCancel := context.WithCancel(ctx)
	r.publisherCancel = pubCancel
	r.publisherWG.Add(1)
	go func() {
		defer r.publisherWG.Done()
		r.runOutboxPublisher(pubCtx, outbox)
	}()

	// Consumer：监督循环负责意外退出后的有界重建，并在退出前关闭当前客户端。
	consCtx, consCancel := context.WithCancel(ctx)
	r.consumerCancel = consCancel
	r.consumerWG.Add(1)
	go func() {
		defer r.consumerWG.Done()
		r.superviseConsumer(consCtx, deps, consumer, client)
	}()

	// 订单超时取消与 Redis 对账与分发模式无关：两种模式都必须继续运行。
	deps.startOrderCancel(ctx, &r.scannerWG)
	deps.startReconcile(ctx, &r.scannerWG)

	logx.Info(ctx, "flashsale_runtime_started", "秒杀链路以 Kafka 模式启动", logx.Fields{
		"mode":              r.mode,
		"brokers":           strings.Join(r.kafkaConfig.Brokers, ","),
		"topic":             r.kafkaConfig.Topic,
		"dlq_topic":         r.kafkaConfig.DLQTopic,
		"consumer_group":    r.kafkaConfig.ConsumerGroup,
		"publisher_batch":   r.publisherBatchSize,
		"poll_interval":     r.publisherPollInterval.String(),
		"publisher_backoff": r.publisherErrorBackoff.String(),
		"shutdown_budget":   r.shutdownTimeout.String(),
	})
	return nil
}

// applyTimingDefaults 补齐测试未注入的时长参数。
func (r *FlashSaleRuntime) applyTimingDefaults() {
	if r.consumerRestartBase <= 0 {
		r.consumerRestartBase = defaultConsumerRestartBase
	}
	if r.consumerRestartMax <= 0 {
		r.consumerRestartMax = defaultConsumerRestartMax
	}
	if r.consumerRestartMax < r.consumerRestartBase {
		r.consumerRestartMax = r.consumerRestartBase
	}
	if r.consumerCloseTimeout <= 0 {
		r.consumerCloseTimeout = defaultConsumerCloseTimeout
	}
	if r.shutdownTimeout <= 0 {
		r.shutdownTimeout = defaultFlashSaleShutdownTimeout
	}
	if r.consumerCloseTimeout > r.shutdownTimeout {
		// 单次客户端关闭不能单独突破统一关闭预算。
		r.consumerCloseTimeout = r.shutdownTimeout
	}
}

// runOutboxPublisher 是 Outbox 后台发布循环。
//
// 节奏约束：
//   - 每轮使用有界批次（BatchSize）；
//   - 轮与轮之间至少等待 PollInterval，有数据时也不会无间隔空转；
//   - 本轮没有任何进展（领取失败/全部无法标记）才使用有界指数退避，
//     部分失败但仍有进展时保持正常节奏，避免一条坏记录拖慢整个积压；
//   - ctx 取消时立即退出，退出前当前 PublishBatch 已返回（Shutdown 等待本 goroutine）。
func (r *FlashSaleRuntime) runOutboxPublisher(ctx context.Context, outbox outboxRunner) {
	backoff := r.publisherErrorBackoff
	for {
		stats, err := outbox.PublishBatch(ctx, r.publisherBatchSize)
		if ctx.Err() != nil {
			return
		}

		wait := r.publisherPollInterval
		progressed := stats.Published > 0 || stats.Retried > 0
		if err != nil && !progressed {
			wait = backoff
			backoff = nextBoundedBackoff(backoff, maxFlashSalePublisherErrorBackoff)
			logx.Warning(ctx, "flashsale_outbox_loop_backoff", "秒杀 Outbox 本轮无进展，按有界退避后重试", logx.Fields{
				"backoff": wait.String(),
				"claimed": stats.Claimed,
				"failed":  stats.Failed,
				"error":   err.Error(),
			})
		} else {
			backoff = r.publisherErrorBackoff
		}
		if sleepCtx(ctx, wait) != nil {
			return
		}
	}
}

// superviseConsumer 监督 Consumer.Run，并在意外退出后按有界指数退避重建。
//
// 关键不变量：
//   - 同一时刻最多一个 Consumer 实例：必须等 Run 返回并 Close 旧客户端后才创建新实例；
//   - 正常关闭（ctx 取消）不重建：Run 返回后直接关闭客户端并退出；
//   - 重建复用同一 ConsumerGroup 与已持久化的 MySQL 重试状态；未提交位移由 Kafka 重投；
//   - 重建退避可被 ctx 取消，应用关闭时不会卡在 sleep 中。
func (r *FlashSaleRuntime) superviseConsumer(
	ctx context.Context, deps flashSaleRuntimeDeps,
	current consumerRunner, client flashsale.RebalanceConsumer,
) {
	backoff := r.consumerRestartBase
	for {
		if current == nil {
			// 上一次重建失败：先按退避重试构造，成功后才进入 Run。
			var err error
			client, current, err = r.newConsumerPair(deps)
			if err != nil {
				logx.Error(ctx, "flashsale_consumer_rebuild_failed", "秒杀 Kafka Consumer 重建失败，按有界退避重试", logx.Fields{
					"backoff": backoff.String(),
					"error":   err.Error(),
				})
				if sleepCtx(ctx, backoff) != nil {
					return
				}
				backoff = nextBoundedBackoff(backoff, r.consumerRestartMax)
				continue
			}
		}

		startedAt := time.Now()
		runErr := current.Run(ctx)
		if ctx.Err() != nil {
			// 主动关闭：Run 已返回（含 AllowRebalance 的 defer），关闭客户端后退出。
			closeConsumerClientBounded(client, r.consumerCloseTimeout)
			return
		}

		// Run 意外返回：旧实例已不再拉取消息。必须先关闭旧客户端，
		// 再按退避创建新客户端，确保不存在两个 Consumer 同时运行。
		closeConsumerClientBounded(client, r.consumerCloseTimeout)
		client, current = nil, nil

		if runErr == nil {
			runErr = errors.New("消费循环在 context 未取消时退出")
		}
		if time.Since(startedAt) >= consumerRestartResetAfter {
			// 运行足够久视为稳定：重置退避，避免长期运行后偶发失败的退避被历史放大。
			backoff = r.consumerRestartBase
		}
		metrics.ObserveConsumerRestart(ctx)
		logx.Error(ctx, "flashsale_consumer_unexpected_exit", "秒杀 Kafka Consumer 意外退出，准备重建", logx.Fields{
			"backoff":     backoff.String(),
			"run_seconds": time.Since(startedAt).Seconds(),
			"error":       runErr.Error(),
		})
		if sleepCtx(ctx, backoff) != nil {
			return
		}
		backoff = nextBoundedBackoff(backoff, r.consumerRestartMax)
	}
}

// newConsumerPair 重建客户端 + 消费循环实例。客户端构造成功但消费者构造失败时，
// 立即关闭客户端，避免连接泄漏。
func (r *FlashSaleRuntime) newConsumerPair(deps flashSaleRuntimeDeps) (flashsale.RebalanceConsumer, consumerRunner, error) {
	client, err := deps.newConsumerClient(r.kafkaConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("重建 Kafka Consumer 客户端: %w", err)
	}
	consumer, err := deps.newEventConsumer(client, r.producer, deps.newRequestConsumer(), r.consumerOpts)
	if err != nil {
		closeConsumerClientBounded(client, r.consumerCloseTimeout)
		return nil, nil, fmt.Errorf("重建秒杀 Kafka Consumer: %w", err)
	}
	return client, consumer, nil
}

// Shutdown 按固定顺序停止组件，所有等待受统一 budget 约束。
//
// 顺序：
//  1. 取消 Publisher ctx 并等待当前 PublishBatch 结束（不再领取新记录）；
//  2. 取消 Consumer ctx 并等待 Run 返回 + 监督循环关闭客户端；
//  3. 关闭 Producer（有界 Flush；此后不再有 Publish）；
//  4. 取消根 ctx 并等待扫描器退出。
//
// 任何一步超时都继续执行后续步骤，避免单个组件挂起导致其他资源无法释放。
func (r *FlashSaleRuntime) Shutdown() error {
	r.shutdownOnce.Do(func() {
		r.shutdownErr = r.shutdown()
	})
	return r.shutdownErr
}

func (r *FlashSaleRuntime) shutdown() error {
	deadline := time.Now().Add(r.shutdownTimeout)
	var errs []error

	if r.publisherCancel != nil {
		r.publisherCancel()
		if !waitGroupUntil(&r.publisherWG, deadline) {
			errs = append(errs, r.shutdownTimeoutError("Outbox Publisher"))
		}
	}

	if r.consumerCancel != nil {
		r.consumerCancel()
		if !waitGroupUntil(&r.consumerWG, deadline) {
			errs = append(errs, r.shutdownTimeoutError("Kafka Consumer"))
		}
	}

	// Producer 最后关闭：Consumer 的死信投递依赖它；此时消费循环已停止，不会再有新 Publish。
	// 这里不清空 r.producer：若 Consumer 监督循环因超时仍在收尾，读取该字段不会与写并发；
	// Producer.Close 本身幂等，重复关闭是安全的。
	if r.producer != nil {
		closeCtx, cancel := context.WithDeadline(context.Background(), deadline)
		closeErr := r.producer.Close(closeCtx)
		cancel()
		if closeErr != nil {
			errs = append(errs, fmt.Errorf("关闭 Kafka Producer: %w", closeErr))
		}
	}

	r.baseCancel()
	if !waitGroupUntil(&r.scannerWG, deadline) {
		errs = append(errs, r.shutdownTimeoutError("后台扫描器"))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// closeProducer 用于启动失败路径：尽力关闭已创建的 Producer，同样受关闭预算约束。
func (r *FlashSaleRuntime) closeProducer() {
	if r.producer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.shutdownTimeout)
	defer cancel()
	err := r.producer.Close(ctx)
	r.producer = nil
	if err != nil {
		logx.Warning(context.Background(), "flashsale_producer_close_failed", "启动失败后关闭 Kafka Producer 出错", logx.Fields{
			"error": err.Error(),
		})
	}
}

// shutdownTimeoutError 记录并返回一个明确的超时错误（不能静默吞掉关闭超时）。
func (r *FlashSaleRuntime) shutdownTimeoutError(component string) error {
	logx.Warning(context.Background(), "flashsale_runtime_shutdown_timeout", "秒杀后台组件未在关闭预算内退出", logx.Fields{
		"component": component,
		"timeout":   r.shutdownTimeout.String(),
	})
	return fmt.Errorf("%s 未在关闭预算 %s 内退出", component, r.shutdownTimeout)
}

// waitGroupUntil 在 deadline 前等待 wg 完成，返回是否在预算内完成。
// 超时后等待协程会在 wg 真正完成时退出；只用于进程关闭路径，数量有界。
func waitGroupUntil(wg *sync.WaitGroup, deadline time.Time) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	remaining := time.Until(deadline)
	if remaining <= 0 {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// sleepCtx 是可取消的等待：ctx 结束立即返回错误，关闭流程不会等满退避窗口。
func sleepCtx(ctx context.Context, d time.Duration) error {
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

// nextBoundedBackoff 返回翻倍后的退避时长，封顶 max；非法值退回 1 个单位。
func nextBoundedBackoff(current, max time.Duration) time.Duration {
	if current <= 0 {
		current = time.Second
	}
	if max <= 0 || max < current {
		max = current
	}
	next := current * 2
	if next <= 0 || next > max {
		return max
	}
	return next
}

// closeConsumerClientBounded 关闭 Consumer 客户端；离组失败只记录，不阻塞关闭流程。
func closeConsumerClientBounded(client flashsale.RebalanceConsumer, timeout time.Duration) {
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		logx.Warning(context.Background(), "flashsale_consumer_close_failed", "关闭 Kafka Consumer 客户端出错（资源已释放）", logx.Fields{
			"error": err.Error(),
		})
	}
}

// ---------- 配置加载与校验 ----------

// loadFlashSaleRuntimeConfig 读取 mode / kafka / publisher / shutdown 配置。
//
// 读取方式与其他 flash_sale 配置一致：环境变量优先，配置文件用不做键归一化的 cfg.Get
// （GoFrame 的 GetEffective 会把 flash_sale 归一化成 flash.sale，见 mq/kafka/config.go）。
func loadFlashSaleRuntimeConfig(ctx context.Context) (flashSaleRuntimeConfig, error) {
	mode, err := readFlashSaleString(ctx, "flash_sale.dispatch_mode", flashSaleModeDBScanner)
	if err != nil {
		return flashSaleRuntimeConfig{}, err
	}
	kafkaCfg, err := kafkamq.Load(ctx)
	if err != nil {
		return flashSaleRuntimeConfig{}, fmt.Errorf("读取 flash_sale.kafka 配置: %w", err)
	}
	batch, err := readFlashSaleInt(ctx, "flash_sale.kafka.publisher.batch_size", defaultFlashSalePublisherBatchSize)
	if err != nil {
		return flashSaleRuntimeConfig{}, err
	}
	poll, err := readFlashSaleDuration(ctx, "flash_sale.kafka.publisher.poll_interval", defaultFlashSalePublisherPollInterval)
	if err != nil {
		return flashSaleRuntimeConfig{}, err
	}
	backoff, err := readFlashSaleDuration(ctx, "flash_sale.kafka.publisher.error_backoff", defaultFlashSalePublisherErrorBackoff)
	if err != nil {
		return flashSaleRuntimeConfig{}, err
	}
	shutdown, err := readFlashSaleDuration(ctx, "flash_sale.shutdown_timeout", defaultFlashSaleShutdownTimeout)
	if err != nil {
		return flashSaleRuntimeConfig{}, err
	}
	return flashSaleRuntimeConfig{
		Mode:  mode,
		Kafka: kafkaCfg,
		Publisher: flashSalePublisherConfig{
			BatchSize:    batch,
			PollInterval: poll,
			ErrorBackoff: backoff,
		},
		ShutdownTimeout: shutdown,
	}, nil
}

// resolveFlashSaleRuntimeConfig 校验模式与 Kafka 开关的关系，并补齐运行参数默认值。
//
// 配置矛盾必须启动失败，不允许静默退回另一种模式：
//   - kafka 模式要求 kafka.enabled=true 且 Broker/Topic/DLQ/ConsumeGroup 完整；
//   - db_scanner 模式要求 kafka.enabled=false（否则「启用 Kafka」与「不运行 Kafka」矛盾，
//     静默忽略 enabled=true 会让运维误以为 Kafka 链路已生效）。
func resolveFlashSaleRuntimeConfig(cfg flashSaleRuntimeConfig) (flashSaleRuntimeConfig, error) {
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	if cfg.Mode == "" {
		cfg.Mode = flashSaleModeDBScanner
	}
	switch cfg.Mode {
	case flashSaleModeDBScanner:
		if cfg.Kafka.Enabled {
			return cfg, fmt.Errorf(
				"配置矛盾：flash_sale.dispatch_mode=%q 时 flash_sale.kafka.enabled 必须为 false；"+
					"如需启用 Kafka 链路，请同时设置 dispatch_mode=kafka", cfg.Mode)
		}
	case flashSaleModeKafka:
		if !cfg.Kafka.Enabled {
			return cfg, fmt.Errorf(
				"配置矛盾：flash_sale.dispatch_mode=%q 需要 flash_sale.kafka.enabled=true", cfg.Mode)
		}
		if err := cfg.Kafka.Validate(); err != nil {
			return cfg, fmt.Errorf("flash_sale.dispatch_mode=%q 的 Kafka 配置无效: %w", cfg.Mode, err)
		}
	default:
		return cfg, fmt.Errorf(
			"不支持的 flash_sale.dispatch_mode=%q（仅支持 %q 或 %q）",
			cfg.Mode, flashSaleModeDBScanner, flashSaleModeKafka)
	}

	if cfg.Publisher.BatchSize <= 0 {
		cfg.Publisher.BatchSize = defaultFlashSalePublisherBatchSize
	}
	if cfg.Publisher.BatchSize > maxFlashSaleRuntimeBatchSize {
		cfg.Publisher.BatchSize = maxFlashSaleRuntimeBatchSize
	}
	if cfg.Publisher.PollInterval <= 0 {
		cfg.Publisher.PollInterval = defaultFlashSalePublisherPollInterval
	}
	if cfg.Publisher.ErrorBackoff <= 0 {
		cfg.Publisher.ErrorBackoff = defaultFlashSalePublisherErrorBackoff
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = defaultFlashSaleShutdownTimeout
	}
	return cfg, nil
}

// lookupFlashSaleConfig 读取单个配置项：环境变量优先，其次配置文件。
func lookupFlashSaleConfig(ctx context.Context, key string) (*gvar.Var, error) {
	envKey := strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
	if v := genv.Get(envKey); v != nil {
		return v, nil
	}
	return g.Cfg().Get(ctx, key)
}

func readFlashSaleString(ctx context.Context, key, def string) (string, error) {
	v, err := lookupFlashSaleConfig(ctx, key)
	if err != nil {
		return "", fmt.Errorf("读取配置项 %s: %w", key, err)
	}
	if v == nil {
		return def, nil
	}
	text := strings.TrimSpace(v.String())
	if text == "" {
		return def, nil
	}
	return text, nil
}

func readFlashSaleInt(ctx context.Context, key string, def int) (int, error) {
	text, err := readFlashSaleString(ctx, key, "")
	if err != nil {
		return 0, err
	}
	if text == "" {
		return def, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("配置项 %s 需要整数，实际为 %q", key, text)
	}
	return n, nil
}

func readFlashSaleDuration(ctx context.Context, key string, def time.Duration) (time.Duration, error) {
	text, err := readFlashSaleString(ctx, key, "")
	if err != nil {
		return 0, err
	}
	if text == "" {
		return def, nil
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("配置项 %s 需要 Go duration（如 200ms/2s），实际为 %q", key, text)
	}
	if d <= 0 {
		return 0, fmt.Errorf("配置项 %s 必须大于 0，实际为 %s", key, d)
	}
	return d, nil
}
