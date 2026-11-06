// 秒杀 B2（Kafka 可靠发布）：Request Outbox Publisher。
//
// 本文件把既有 flash_sale_order_requests 表当作 Outbox 使用：HTTP 准入后的 Request INSERT
// 提交即意味着「事件具备可恢复的发布来源」，无需第二张 Outbox 表，也不额外写库。
//
// 核心不变量（与设计文档一一对应）：
//   - INV-1：Request 提交即事件可恢复；发布只在事务提交之后进行。
//   - INV-2：只有 Kafka Broker 明确确认（acks=all）之后才允许写 published_at；
//     任何「先标记后发布」的写法都会在 Broker 故障时永久丢事件。
//   - INV-3：发布失败、进程崩溃、租约过期都不会让请求永久丢失——租约到期后重新可领取。
//   - INV-4：event_id 由 request_id 稳定推导，重发不变（见 message.go）。
//   - INV-5：多实例互斥依赖数据库行锁 + 租约，而不是进程内 mutex；
//     租约过期后的重复发布属于允许的 at-least-once 语义。
//   - INV-6：Publisher 只读写发布相关列，不改 status/retry_count/flash_order_id，
//     也不触碰 Redis 预扣、订单与库存。
//
// 发布流程固定为四段，边界不可合并：
//  1. 短事务领取：SELECT ... FOR UPDATE SKIP LOCKED + 写 token/租约/attempts，随后立刻提交；
//  2. 发布：事务已结束才调用 Kafka，绝不在持有行锁时等待 ACK；
//  3. 标记：逐条以 request_id + claim token 为条件更新（逐条结果，绝不整批成功化）；
//  4. 失败退避：写 next_publish_at 并释放租约，下一轮再试。
//
// 特别注意：同一 SKU 固定分区只保证「同 key 消息在分区内有序」，
// 多 Publisher 并发下 Kafka 中的消息顺序不等价于 MySQL id 顺序。业务正确性不得依赖该顺序。
package flashsale

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	"cnb.cool/go-cloud-devops/my-shop/internal/mq"
)

// outboxTable 是承载 Outbox 语义的既有请求表（不新建表）。
const outboxTable = "flash_sale_order_requests"

// 发布参数的默认值与上限。
const (
	// defaultOutboxBatchSize 是单轮领取上限：批量太大既拉长单轮耗时，也会放大部分失败的重发代价。
	defaultOutboxBatchSize = 100
	// maxOutboxBatchSize 是调用方可请求的硬上限，避免单轮无界占用连接与内存。
	maxOutboxBatchSize = 1000

	// defaultOutboxLease 是领取租约时长。它必须覆盖「最坏发布耗时 + 标记耗时」：
	// Kafka 投递超时 30s 由发布超时（20s）先截断，留 40s 给标记与网络抖动。
	defaultOutboxLease = 60 * time.Second
	// defaultOutboxPublishTimeout 是单批等待 Broker 确认的上限。
	defaultOutboxPublishTimeout = 20 * time.Second
	// outboxLeaseSafetyMargin 是租约相对发布超时的最小安全余量：
	// 租约必须显著长于发布超时，否则正常的慢批次会在标记前被他人重新领取，产生不必要的重复投递。
	outboxLeaseSafetyMargin = 10 * time.Second

	// defaultOutboxBackoffBase / defaultOutboxBackoffMax 是发布失败退避的指数区间（2s 起步、5min 封顶）。
	// 有界退避的意义：Kafka 短时故障时既不会紧密循环重发，也不会把记录永远压在很长的时间里。
	defaultOutboxBackoffBase = 2 * time.Second
	defaultOutboxBackoffMax  = 5 * time.Minute

	// maxPublishErrorLength 与 last_publish_error VARCHAR(255) 对齐；超长错误截断而不报错，
	// 因为诊断信息不应反过来让标记失败。
	maxPublishErrorLength = 255

	// outboxClaimTokenBytes 是租约令牌的随机字节数（16 字节 → 32 位十六进制，远小于 VARCHAR(64)）。
	outboxClaimTokenBytes = 16
)

// OutboxOptions 是 Publisher 的运行参数；零值字段使用默认值。
type OutboxOptions struct {
	// BatchSize 是单轮默认领取上限（PublishBatch 的 limit<=0 时使用）。
	BatchSize int
	// Lease 是领取租约时长，必须显著大于 PublishTimeout。
	Lease time.Duration
	// PublishTimeout 是单批等待 Broker 确认的上限，必须小于 Lease。
	PublishTimeout time.Duration
	// BackoffBase / BackoffMax 是失败退避的指数基础值与上限。
	BackoffBase time.Duration
	BackoffMax  time.Duration
}

// withDefaults 用默认值补齐零值字段。
func (o OutboxOptions) withDefaults() OutboxOptions {
	if o.BatchSize <= 0 {
		o.BatchSize = defaultOutboxBatchSize
	}
	if o.BatchSize > maxOutboxBatchSize {
		o.BatchSize = maxOutboxBatchSize
	}
	if o.Lease <= 0 {
		o.Lease = defaultOutboxLease
	}
	if o.PublishTimeout <= 0 {
		o.PublishTimeout = defaultOutboxPublishTimeout
	}
	if o.BackoffBase <= 0 {
		o.BackoffBase = defaultOutboxBackoffBase
	}
	if o.BackoffMax <= 0 {
		o.BackoffMax = defaultOutboxBackoffMax
	}
	return o
}

// validate 校验参数自洽性：租约必须显著长于发布超时，退避上限不得小于基础值。
// 这些约束如果被违反，不会让数据丢失（终态仍可由租约过期恢复），但会产生本可避免的重复投递。
func (o OutboxOptions) validate() error {
	if o.Lease <= o.PublishTimeout+outboxLeaseSafetyMargin {
		return fmt.Errorf("Outbox 租约 %s 必须显著大于发布超时 %s（至少多 %s），否则正常批次会在标记前被重新领取",
			o.Lease, o.PublishTimeout, outboxLeaseSafetyMargin)
	}
	if o.BackoffMax < o.BackoffBase {
		return fmt.Errorf("Outbox 退避上限 %s 不得小于基础值 %s", o.BackoffMax, o.BackoffBase)
	}
	return nil
}

// OutboxPublisher 把 queued 且未发布的请求可靠地发布到 Kafka Requested Topic。
//
// 生命周期：底层 mq.Publisher（通常是 Kafka Producer）由装配方（B4）创建与关闭；
// 本类型不接管其 Close，避免出现「谁关闭谁负责」的歧义。
// 本类型可被多实例/多 goroutine 同时使用：互斥完全依赖数据库行锁与租约，
// 不依赖任何进程内状态（因此不能把安全性寄托在 mutex 上）。
type OutboxPublisher struct {
	pub   mq.Publisher
	topic string
	opts  OutboxOptions
}

// NewOutboxPublisher 构造 Outbox Publisher。
// topic 为空或 pub 为 nil 时直接失败：宁可装配期报错，也不要在运行期把事件发到未知主题。
func NewOutboxPublisher(pub mq.Publisher, topic string, opts OutboxOptions) (*OutboxPublisher, error) {
	if pub == nil {
		return nil, errors.New("创建秒杀 Outbox Publisher: 发布器不能为空")
	}
	if topic == "" {
		return nil, errors.New("创建秒杀 Outbox Publisher: Kafka Topic 不能为空")
	}
	o := opts.withDefaults()
	if err := o.validate(); err != nil {
		return nil, fmt.Errorf("创建秒杀 Outbox Publisher: %w", err)
	}
	return &OutboxPublisher{pub: pub, topic: topic, opts: o}, nil
}

// OutboxStats 是一轮发布的观测结果（非业务事实）：发布失败不会改变 Request 的 queued 状态。
type OutboxStats struct {
	Claimed        int           // 本轮领取到的待发布记录数
	LeaseReclaimed int           // 其中「租约已过期后被重新领取」的记录数
	Published      int           // 获得 Broker 确认的条数（含紧随其后标记失败、需要重发的条数）
	Failed         int           // 未获得 Broker 确认或无法构造事件的条数
	Retried        int           // 已成功写入 next_publish_at 退避记录的条数
	MarkFailed     int           // Broker 已确认但 MySQL 标记失败的条数（记录仍可被重新发布）
	MarkSkipped    int           // 标记时租约已被他人接管、或已被发布的条数（不得覆盖）
	Duration       time.Duration // 本轮总耗时
}

// OutboxBacklog 是待发布积压快照，供后续 Prometheus 采集或健康检查接入。
// Pending 统计 queued 且 published_at IS NULL 的全部记录（含退避中、租约中的记录）；
// OldestAge 用数据库时间计算，避免应用与 MySQL 时钟漂移造成负年龄或虚高年龄。
type OutboxBacklog struct {
	Pending         int64
	OldestID        int64
	OldestCreatedAt time.Time
	OldestAge       time.Duration
}

// outboxBeforeMarkHook 是「Kafka 已确认、写 published_at 之前」注入故障的测试钩子（生产恒为 nil）。
// 用于验证本层最危险的故障窗口：Broker 已收到消息但数据库标记失败，
// 记录必须保持未发布并在租约到期后可重新发布（允许重复投递，绝不丢事件）。
var outboxBeforeMarkHook func(ctx context.Context, requestID int64) error

// outboxClaim 是一条被本轮领取的待发布请求（只读取发布所需的列）。
type outboxClaim struct {
	Id              int64       `json:"id"`
	UserId          int64       `json:"user_id"`
	ActivityId      int64       `json:"activity_id"`
	SkuId           int64       `json:"sku_id"`
	IdempotencyKey  string      `json:"idempotency_key"`
	RequestHash     string      `json:"request_hash"`
	PublishAttempts int         `json:"publish_attempts"`
	LeaseUntil      *gtime.Time `json:"publish_lease_until"`
	CreatedAt       *gtime.Time `json:"created_at"`
}

// outboxMarkOutcome 描述条件标记的结果，用于区分「已确认终态」与「租约丢失」。
type outboxMarkOutcome int

const (
	// markApplied：条件更新命中，发布事实已写入。
	markApplied outboxMarkOutcome = iota
	// markSkippedLeaseLost：租约令牌已失效（被其他 Publisher 重新领取或人工修复重置），不得覆盖。
	markSkippedLeaseLost
	// markSkippedAlreadyPublished：记录已由其他 Publisher 标记发布，属于重复投递下的幂等结果。
	markSkippedAlreadyPublished
)

// PublishBatch 执行一轮「领取 → 发布 → 标记」，返回本轮统计。
//
// limit<=0 时使用配置的 BatchSize；超过硬上限时按上限截断。
// 返回的 error 只表示「本轮有失败项或上下文结束」，调用方应记录并继续下一轮，
// 不应据此终止 Publisher：失败记录已写入退避时间，后续轮次会自动重试。
func (p *OutboxPublisher) PublishBatch(ctx context.Context, limit int) (stats OutboxStats, err error) {
	if limit <= 0 {
		limit = p.opts.BatchSize
	}
	if limit > maxOutboxBatchSize {
		limit = maxOutboxBatchSize
	}

	started := time.Now()
	defer func() {
		stats.Duration = time.Since(started)
		metrics.ObserveOutboxPublishDuration(ctx, stats.Duration)
	}()

	claims, token, reclaimed, err := p.claim(ctx, limit)
	if err != nil {
		return stats, err
	}
	stats.Claimed = len(claims)
	stats.LeaseReclaimed = reclaimed
	metrics.ObserveOutboxClaim(ctx, stats.Claimed, stats.LeaseReclaimed)
	if len(claims) == 0 {
		return stats, nil
	}
	logx.Info(ctx, "flashsale_outbox_claimed", "秒杀 Outbox 领取待发布请求", logx.Fields{
		"claimed":         stats.Claimed,
		"lease_reclaimed": stats.LeaseReclaimed,
		"claim_token":     claimTokenDigest(token),
	})

	// 阶段 1：构造事件。构造失败的记录无法发布（例如历史数据字段不满足契约），
	// 按失败退避并把错误留痕，绝不写 published_at，也不丢记录。
	var (
		pendings   []outboxPending
		errs       []error
		roundError error
	)
	for _, c := range claims {
		msg, buildErr := buildRequestedMessage(c)
		if buildErr != nil {
			errs = append(errs, buildErr)
			stats.Failed++
			metrics.ObserveOutboxFailed(ctx, metrics.OutboxFailureEncode)
			outcome, markErr := p.markPublishFailure(ctx, c, token, buildErr)
			switch {
			case markErr != nil:
				stats.MarkFailed++
				errs = append(errs, markErr)
			case outcome != markApplied:
				stats.MarkSkipped++
				metrics.ObserveOutboxMarkSkipped(ctx, markOutcomeReason(outcome))
			default:
				stats.Retried++
				metrics.ObserveOutboxRetried(ctx)
			}
			continue
		}
		pendings = append(pendings, outboxPending{claim: c, message: msg})
	}

	// 阶段 2：发布。注意此处领取事务已经提交——Kafka 网络调用期间不持有任何 MySQL 行锁。
	if len(pendings) > 0 {
		messages := make([]mq.Message, len(pendings))
		for i := range pendings {
			messages[i] = pendings[i].message
		}
		publishCtx, cancel := context.WithTimeout(ctx, p.opts.PublishTimeout)
		results, publishErr := p.pub.Publish(publishCtx, messages...)
		cancel()

		if publishErr != nil && results == nil {
			// 整批结果未知（context 到期/取消、客户端已关闭）：按「可能已发送」处理，
			// 不写 published_at，只安排退避重发。消费者按 event_id 去重承担最终业务幂等。
			errs = append(errs, fmt.Errorf("秒杀 Outbox 发布 %d 条消息结果未知: %w", len(pendings), publishErr))
			for _, item := range pendings {
				stats.Failed++
				metrics.ObserveOutboxFailed(ctx, metrics.OutboxFailureUnknown)
				if e := p.applyPublishFailure(ctx, item.claim, token, publishErr, &stats, &errs); e != nil {
					roundError = errors.Join(roundError, e)
				}
			}
		} else {
			if publishErr != nil {
				errs = append(errs, fmt.Errorf("秒杀 Outbox 部分消息未获 Broker 确认: %w", publishErr))
			}
			// 阶段 3：逐条处理结果。一条失败不得影响其他条的成功标记，
			// 也不允许把整批重新标记为成功。
			for i, item := range pendings {
				res, ok := pickPublishResult(results, i)
				if !ok {
					stats.Failed++
					metrics.ObserveOutboxFailed(ctx, metrics.OutboxFailureMissingResult)
					if e := p.applyPublishFailure(ctx, item.claim, token,
						fmt.Errorf("发布结果缺失（Broker 未返回第 %d 条结果）", i), &stats, &errs); e != nil {
						roundError = errors.Join(roundError, e)
					}
					continue
				}
				if res.Err != nil {
					stats.Failed++
					metrics.ObserveOutboxFailed(ctx, metrics.OutboxFailureKafka)
					if e := p.applyPublishFailure(ctx, item.claim, token, res.Err, &stats, &errs); e != nil {
						roundError = errors.Join(roundError, e)
					}
					continue
				}
				// Broker 已确认：即使随后 DB 标记失败，也必须计入 Published——
				// 这两件事的观测口径不同，混在一起会掩盖「ACK 成功但标记失败」的故障窗口。
				stats.Published++
				metrics.ObserveOutboxPublished(ctx)
				outcome, markErr := p.markPublished(ctx, item.claim, token, res)
				switch {
				case markErr != nil:
					// 最危险的窗口：消息可能已进入 Kafka，但 published_at 未落库。
					// 记录保持未发布、租约到期后会被重新发布，允许重复投递但绝不丢事件。
					stats.MarkFailed++
					roundError = errors.Join(roundError, markErr)
				case outcome != markApplied:
					stats.MarkSkipped++
					metrics.ObserveOutboxMarkSkipped(ctx, markOutcomeReason(outcome))
					logx.Warning(ctx, "flashsale_outbox_mark_skipped", "秒杀 Outbox 标记未命中（租约已被接管或已发布）", logx.Fields{
						"request_id":  item.claim.Id,
						"claim_token": claimTokenDigest(token),
						"reason":      markOutcomeReason(outcome),
					})
				}
			}
		}
	}

	if len(errs) > 0 {
		roundError = errors.Join(roundError, errors.Join(errs...))
	}
	if roundError != nil {
		logx.Warning(ctx, "flashsale_outbox_round_failed", "秒杀 Outbox 本轮存在失败项", logx.Fields{
			"claimed":         stats.Claimed,
			"published":       stats.Published,
			"failed":          stats.Failed,
			"retried":         stats.Retried,
			"mark_failed":     stats.MarkFailed,
			"mark_skipped":    stats.MarkSkipped,
			"lease_reclaimed": stats.LeaseReclaimed,
			"error":           roundError.Error(),
		})
	}
	return stats, roundError
}

// outboxPending 是「已领取且已成功构造事件」的待发布项。
type outboxPending struct {
	claim   *outboxClaim
	message mq.Message
}

// applyPublishFailure 记录一次发布失败的退避，并集计统计。
// 失败退避本身是 MySQL 写入：它失败时只记录错误，记录会凭租约到期恢复，不会丢失。
func (p *OutboxPublisher) applyPublishFailure(
	ctx context.Context, c *outboxClaim, token string, cause error, stats *OutboxStats, errs *[]error,
) error {
	outcome, err := p.markPublishFailure(ctx, c, token, cause)
	if err != nil {
		stats.MarkFailed++
		*errs = append(*errs, err)
		return nil
	}
	if outcome != markApplied {
		// 租约已失效或已被发布：不能覆盖他人的标记，记录由当前持有者负责。
		stats.MarkSkipped++
		metrics.ObserveOutboxMarkSkipped(ctx, markOutcomeReason(outcome))
		return nil
	}
	stats.Retried++
	metrics.ObserveOutboxRetried(ctx)
	logx.Warning(ctx, "flashsale_outbox_publish_failed", "秒杀 Outbox 发布失败，已安排退避重试", logx.Fields{
		"request_id":       c.Id,
		"flashsale_id":     c.ActivityId,
		"publish_attempts": c.PublishAttempts + 1,
		"claim_token":      claimTokenDigest(token),
		"error":            cause.Error(),
	})
	return nil
}

// claim 执行领取阶段：一个短事务内完成「加锁选择 + 计数回收 + 写 token/租约/attempts」，
// 事务在返回前提交，Kafka 调用不持有任何行锁。
//
// 多实例安全的三重保证：
//   - FOR UPDATE SKIP LOCKED：并发领取时直接跳过已被他人加锁的行，既不阻塞也不重复领取；
//   - 数据库时间 NOW(6)：租约到期判定使用 MySQL 时钟，不依赖各实例本地时间；
//   - 每次领取生成新的 claim token：旧持有者即使租约过期后恢复，也无法凭旧 token 写入新租约的状态。
func (p *OutboxPublisher) claim(ctx context.Context, limit int) (claims []*outboxClaim, token string, reclaimed int, err error) {
	token, err = newClaimToken()
	if err != nil {
		return nil, "", 0, err
	}
	leaseMicros := p.opts.Lease.Microseconds()

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var rows []*outboxClaim
		if e := tx.Model(outboxTable).Ctx(ctx).
			Fields("id,user_id,activity_id,sku_id,idempotency_key,request_hash,publish_attempts,created_at,publish_lease_until").
			Where("status", requestStatusQueued).
			Where("published_at IS NULL").
			Where("next_publish_at IS NULL OR next_publish_at <= NOW(6)").
			Where("publish_lease_until IS NULL OR publish_lease_until <= NOW(6)").
			// 按到期时间（其次 id）领取：与 idx_outbox_claim(status, published_at, next_publish_at, id)
			// 的索引顺序一致，避免 filesort，也避免把退避中的行读入并加锁。
			Order("next_publish_at", "id").
			Limit(limit).
			LockUpdateSkipLocked().
			Scan(&rows); e != nil {
			return fmt.Errorf("领取待发布秒杀请求: %w", e)
		}
		if len(rows) == 0 {
			return nil
		}

		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.Id)
		}

		// 租约过期回收计数：必须在覆盖租约之前统计，且用数据库时间判定。
		if n, e := tx.Model(outboxTable).Ctx(ctx).
			WhereIn("id", ids).
			Where("publish_lease_until IS NOT NULL").
			Where("publish_lease_until <= NOW(6)").
			Count(); e != nil {
			return fmt.Errorf("统计过期租约回收: %w", e)
		} else {
			reclaimed = int(n)
		}

		if _, e := tx.Model(outboxTable).Ctx(ctx).
			WhereIn("id", ids).
			Data(g.Map{
				"publish_claim_token": token,
				"publish_lease_until": gdb.Raw("DATE_ADD(NOW(6), INTERVAL " + strconv.FormatInt(leaseMicros, 10) + " MICROSECOND)"),
				"publish_attempts":    gdb.Raw("publish_attempts + 1"),
			}).Update(); e != nil {
			return fmt.Errorf("写入秒杀请求发布租约: %w", e)
		}
		claims = rows
		return nil
	})
	if err != nil {
		return nil, "", 0, err
	}
	return claims, token, reclaimed, nil
}

// markPublished 以 request_id + claim token 为条件写入发布事实。
//
// 条件里刻意不包含 status：Kafka ACK 与 DB 标记之间存在天然故障窗口，
// 消费者可能在 ACK 之后已经把请求改成 success/failed。
// 此时仍然必须记录「事件已发布」，但绝不回写业务状态（更不允许把终态改回 queued）。
// token 条件同时保证：旧 Publisher 的迟到标记不会覆盖人工修复后的新一代租约。
func (p *OutboxPublisher) markPublished(
	ctx context.Context, c *outboxClaim, token string, res mq.PublishResult,
) (outboxMarkOutcome, error) {
	if outboxBeforeMarkHook != nil {
		if hookErr := outboxBeforeMarkHook(ctx, c.Id); hookErr != nil {
			return markApplied, fmt.Errorf("标记秒杀请求 %d 已发布（测试故障注入）: %w", c.Id, hookErr)
		}
	}
	result, err := g.DB().Model(outboxTable).Ctx(ctx).
		Where("id", c.Id).
		Where("publish_claim_token", token).
		Where("published_at IS NULL").
		Data(g.Map{
			"published_at":        gdb.Raw("NOW(6)"),
			"publish_claim_token": nil,
			"publish_lease_until": nil,
			"next_publish_at":     nil,
			"last_publish_error":  nil,
			"published_partition": res.Partition,
			"published_offset":    res.Offset,
		}).Update()
	if err != nil {
		return markApplied, fmt.Errorf("标记秒杀请求 %d 已发布: %w", c.Id, err)
	}
	if affected, _ := result.RowsAffected(); affected > 0 {
		return markApplied, nil
	}
	return p.explainMarkMiss(ctx, c.Id, token)
}

// markPublishFailure 记录发布失败：释放租约 + 按尝试次数设置指数退避时间 + 留痕错误原因。
//
// 这里绝不修改 status：短时 Kafka 故障不是业务失败，不能把请求标成 failed/dead。
// 记录保持 queued（业务上仍然「排队中」），由 Outbox 自己按 next_publish_at 重试。
func (p *OutboxPublisher) markPublishFailure(
	ctx context.Context, c *outboxClaim, token string, cause error,
) (outboxMarkOutcome, error) {
	attempts := c.PublishAttempts + 1
	backoff := publishBackoff(attempts, p.opts.BackoffBase, p.opts.BackoffMax)

	result, err := g.DB().Model(outboxTable).Ctx(ctx).
		Where("id", c.Id).
		Where("publish_claim_token", token).
		Where("published_at IS NULL").
		Data(g.Map{
			"publish_claim_token": nil,
			"publish_lease_until": nil,
			"next_publish_at":     gdb.Raw("DATE_ADD(NOW(6), INTERVAL " + strconv.FormatInt(backoff.Microseconds(), 10) + " MICROSECOND)"),
			"last_publish_error":  truncatePublishError(cause.Error()),
		}).Update()
	if err != nil {
		return markApplied, fmt.Errorf("记录秒杀请求 %d 发布失败: %w", c.Id, err)
	}
	if affected, _ := result.RowsAffected(); affected > 0 {
		return markApplied, nil
	}
	return p.explainMarkMiss(ctx, c.Id, token)
}

// explainMarkMiss 在条件更新未命中时判定原因，避免把「已发布」「租约被接管」误判成错误。
// 三种情况必须区分：已确认终态（别人已标记发布）、租约丢失、以及真正的异常（理论上不可达）。
func (p *OutboxPublisher) explainMarkMiss(ctx context.Context, requestID int64, token string) (outboxMarkOutcome, error) {
	var rows []struct {
		PublishedAt       *gtime.Time `json:"published_at"`
		PublishClaimToken *string     `json:"publish_claim_token"`
	}
	if err := g.DB().Model(outboxTable).Ctx(ctx).
		Fields("published_at,publish_claim_token").
		Where("id", requestID).
		Scan(&rows); err != nil {
		return markSkippedLeaseLost, fmt.Errorf("核对秒杀请求 %d 的发布标记: %w", requestID, err)
	}
	if len(rows) == 0 {
		// 记录已被删除（本项目无删除接口，属异常），按租约丢失处理：不覆盖任何状态。
		return markSkippedLeaseLost, nil
	}
	if rows[0].PublishedAt != nil {
		return markSkippedAlreadyPublished, nil
	}
	if rows[0].PublishClaimToken == nil || *rows[0].PublishClaimToken != token {
		return markSkippedLeaseLost, nil
	}
	// 条件更新理论上不会在 token 相同且未发布时未命中；若发生，说明存在本层不可见的并发写入。
	return markSkippedLeaseLost, nil
}

// pickPublishResult 按下标取逐条结果；结果缺失时返回 ok=false（按结果未知处理）。
func pickPublishResult(results []mq.PublishResult, i int) (mq.PublishResult, bool) {
	if i < 0 || i >= len(results) {
		return mq.PublishResult{}, false
	}
	return results[i], true
}

// buildRequestedMessage 由持久化请求构造 RequestedEvent v1 消息。
//
// 字段全部来自 MySQL 权威行：occurred_at 使用请求实际创建时间（重发不变），
// 不掺入价格/库存/sold，也不在每次重发时生成新时间。
// Kafka Key 固定为 <activity_id>:<sku_id>，使同一 SKU 的消息进入同一分区；
// 但这只保证分区内有序，不保证与会话/MySQL id 的全局顺序一致。
func buildRequestedMessage(c *outboxClaim) (mq.Message, error) {
	if c.CreatedAt == nil {
		return mq.Message{}, fmt.Errorf("秒杀请求 %d 缺少 created_at，无法构造事件", c.Id)
	}
	event := NewRequestedEvent(RequestedEventInput{
		RequestID:      c.Id,
		UserID:         c.UserId,
		ActivityID:     c.ActivityId,
		SkuID:          c.SkuId,
		IdempotencyKey: c.IdempotencyKey,
		RequestHash:    c.RequestHash,
		OccurredAt:     c.CreatedAt.Time,
	})
	value, err := event.Encode()
	if err != nil {
		return mq.Message{}, fmt.Errorf("构造秒杀请求 %d 的事件消息: %w", c.Id, err)
	}
	return mq.Message{
		Topic: "",
		Key:   []byte(requestedMessageKey(c.ActivityId, c.SkuId)),
		Value: value,
		Headers: []mq.Header{
			{Key: "event_id", Value: []byte(event.EventID)},
			{Key: "event_type", Value: []byte(event.EventType)},
			{Key: "schema_version", Value: []byte(strconv.Itoa(event.SchemaVersion))},
		},
		// 传输层时间戳留空：由驱动在发送时填充当前时间。
		// 不能用 occurred_at 充当记录时间戳——它是 DATETIME 墙钟（无时区），且重投时可能已过去很久，
		// 而 franz-go 会用记录时间戳判断 RecordDeliveryTimeout，历史时间会导致消息被立即判为超时。
		// 「事件发生时间」以消息体里的 occurred_at 为准（跨重投稳定）。
	}, nil
}

// requestedMessageKey 返回 Kafka 分区键 <activity_id>:<sku_id>。
// 同 SKU 固定分区只用于聚合与顺序性便利，业务正确性不依赖跨请求顺序。
func requestedMessageKey(activityID, skuID int64) string {
	return strconv.FormatInt(activityID, 10) + ":" + strconv.FormatInt(skuID, 10)
}

// publishBackoff 计算第 attempts 次发布尝试失败后的指数退避（base 起步、max 封顶）。
// 循环次数受 max/base 的对数约束，不会随 attempts 线性增长。
func publishBackoff(attempts int, base, max time.Duration) time.Duration {
	if base <= 0 {
		base = defaultOutboxBackoffBase
	}
	if max <= 0 {
		max = defaultOutboxBackoffMax
	}
	if max < base {
		// 上限小于基础值属于非法配置（构造期已拒绝）；此处退化为「不放大」，而不是悄悄放大到默认上限。
		max = base
	}
	if attempts <= 1 {
		return base
	}
	d := base
	for i := 1; i < attempts; i++ {
		if d >= max || d > max/2 {
			return max
		}
		d *= 2
	}
	return d
}

// truncatePublishError 按列宽截断错误文本（不会因为诊断信息过长而让标记失败）。
func truncatePublishError(msg string) string {
	if len(msg) <= maxPublishErrorLength {
		return msg
	}
	return msg[:maxPublishErrorLength]
}

// newClaimToken 生成一轮领取的租约令牌：随机 16 字节的十六进制串。
// 随机性保证「新一轮领取」与「旧租约」在数据库条件更新中不会混淆。
func newClaimToken() (string, error) {
	var b [outboxClaimTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成秒杀 Outbox 领取令牌: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// claimTokenDigest 返回令牌的安全摘要（sha256 前 8 位十六进制），用于日志关联而不泄露完整令牌。
func claimTokenDigest(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:8]
}

// markOutcomeReason 把标记结果映射为低基数指标标签。
func markOutcomeReason(o outboxMarkOutcome) string {
	switch o {
	case markSkippedAlreadyPublished:
		return metrics.OutboxMarkAlreadyPublished
	default:
		return metrics.OutboxMarkLeaseLost
	}
}

// Backlog 返回待发布积压快照（待发布数量与最老待发布事件年龄）。
//
// 计数使用 idx_outbox_claim 的前缀（status, published_at）做覆盖扫描；
// 最老年龄只回表读取一条记录，并用数据库 NOW(6) 计算，避免应用与 MySQL 时钟漂移。
func (p *OutboxPublisher) Backlog(ctx context.Context) (OutboxBacklog, error) {
	var backlog OutboxBacklog

	var agg []struct {
		Pending  int64 `json:"pending"`
		OldestId int64 `json:"oldest_id"`
	}
	if err := g.DB().Model(outboxTable).Ctx(ctx).
		Fields("COUNT(*) AS pending, MIN(id) AS oldest_id").
		Where("status", requestStatusQueued).
		Where("published_at IS NULL").
		Scan(&agg); err != nil {
		return backlog, fmt.Errorf("统计待发布秒杀请求: %w", err)
	}
	if len(agg) == 0 {
		return backlog, nil
	}
	backlog.Pending = agg[0].Pending
	backlog.OldestID = agg[0].OldestId
	if backlog.OldestID <= 0 {
		return backlog, nil
	}

	var rows []struct {
		CreatedAt *gtime.Time `json:"created_at"`
		DbNow     *gtime.Time `json:"db_now"`
	}
	if err := g.DB().Model(outboxTable).Ctx(ctx).
		Fields("created_at, NOW(6) AS db_now").
		Where("id", backlog.OldestID).
		Scan(&rows); err != nil {
		return backlog, fmt.Errorf("查询最老待发布秒杀请求 %d: %w", backlog.OldestID, err)
	}
	if len(rows) == 0 || rows[0].CreatedAt == nil || rows[0].DbNow == nil {
		// 统计与回表之间记录被并发处理掉：保留计数，年龄留空（不谎报）。
		return backlog, nil
	}
	backlog.OldestCreatedAt = rows[0].CreatedAt.Time
	backlog.OldestAge = rows[0].DbNow.Time.Sub(rows[0].CreatedAt.Time)
	if backlog.OldestAge < 0 {
		backlog.OldestAge = 0
	}
	return backlog, nil
}
