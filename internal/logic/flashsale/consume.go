// 秒杀异步下单消费核心（V3 MySQL Scanner + B3 Kafka Consumer 共用）。
//
// 两条入口的唯一差别是「如何定位待处理的请求行」：
//   - MySQL Scanner（ConsumeQueued）：从 flash_sale_order_requests 出队一条 queued 且已到尝试
//     时间的请求，FOR UPDATE SKIP LOCKED 让多实例互斥领取不同行；
//   - Kafka Consumer（ConsumeRequest）：按消息里的 request_id 定位请求行，SELECT ... FOR UPDATE
//     而非 SKIP LOCKED —— 并发处理同一 Request 的另一个消费者必须等待行锁，随后在锁内读到
//     最新状态并安全跳过，而不是各自持有一条旧快照分别落单。
//
// 进入行锁之后，两条入口执行完全相同的业务事务：
//
//	BEGIN
//	  SELECT Request FOR UPDATE        -- 行锁：并发消费者串行化
//	  检查 status（仅 queued 执行业务） -- 业务幂等闸门（event_id 不是闸门）
//	  resolveSku（活动/SKU/商品可用性快照）
//	  SAVEPOINT
//	  INSERT 订单 → 条件扣减 sold       -- 唯一约束 + sold < total_stock 兜底防超卖
//	  ROLLBACK TO SAVEPOINT（任一失败）
//	  更新 Request 状态（success/failed）
//	COMMIT
//
// 事务只负责「业务终态」；技术失败（连接中断、死锁、Redis 不可用等）一律不写终态，
// 由调用方用独立短事务推进重试预算，避免把可恢复的基础设施故障固化成业务失败。
package flashsale

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
)

const (
	// maxConsumeRetryAttempts 是单条请求的技术失败重试预算（两条入口共用同一列 retry_count）。
	// 语义：retry_count 表示「已经消耗的重试次数」；再失败一次就会得到 retry_count+1 > 预算，
	// 于是该请求必须进入 dead，而不再重新执行业务。
	maxConsumeRetryAttempts = 3
	// consumeBackoffBaseSeconds 是 Scanner 技术失败退避的基础秒数（指数退避：5s、10s、20s，封顶 60s）。
	// Scanner 以秒级轮询出队，退避起点必须大于轮询周期才有意义。
	consumeBackoffBaseSeconds = 5

	// consumeOrderSavePoint 是「建单 + 扣库存」的保存点名。
	// 订单与库存必须同生共死：任一失败都回滚到该保存点，随后才能安全地把请求写成 failed。
	consumeOrderSavePoint = "flashsale_consume_order"

	// requestTable 是异步请求表（同时充当 MySQL 出队队列与 B2 Outbox）。
	requestTable = "flash_sale_order_requests"
)

// consumeBeforeCommitHook 是消费事务提交前注入故障的测试钩子（生产环境恒为 nil）。
// 返回非 nil 使当前消费事务整体回滚，模拟「消费者领取请求后、事务提交前崩溃」，
// 用于验证 AC-003/INV-015「回滚后重投只产生一次业务效果」。测试使用后须复原为 nil。
var consumeBeforeCommitHook func(ctx context.Context, r *orderRequestRow) error

// ConsumeOutcome 是业务消费核心给出的处置结论（Kafka 入口据此决定 ACK / 重试 / 死信）。
// 结论与「是否已经写入 MySQL」是两件事：只有 success/failed 写入业务终态，
// retry 与 dead 的重试预算、终态迁移由调用方推进（见 B3 的死信先于终态顺序）。
type ConsumeOutcome string

const (
	// ConsumeOutcomeSuccess：落单成功，订单与请求状态已在同一事务提交。
	ConsumeOutcomeSuccess ConsumeOutcome = "success"
	// ConsumeOutcomeFailed：业务失败终态（库存不足/时间窗结束/已购/幂等冲突等），已提交 failed。
	ConsumeOutcomeFailed ConsumeOutcome = "failed"
	// ConsumeOutcomeDuplicate：请求已处于终态（success/failed/dead），本次投递只做安全跳过。
	// 这是 Kafka 重复投递与「MySQL 提交成功但 Offset 提交失败」的正常结果，不是错误。
	ConsumeOutcomeDuplicate ConsumeOutcome = "duplicate"
	// ConsumeOutcomeDead：重试预算已耗尽，不得再执行业务，应立即推进 dead 终态并进入死信。
	ConsumeOutcomeDead ConsumeOutcome = "dead"
	// ConsumeOutcomeInvalid：消息与 MySQL 权威数据不一致（请求不存在/身份字段不匹配），
	// 属于永久错误：不执行业务、不改动数据库，直接死信。
	ConsumeOutcomeInvalid ConsumeOutcome = "invalid"
	// ConsumeOutcomeRetry：技术失败且未写入任何终态，调用方应推进重试预算后重投。
	ConsumeOutcomeRetry ConsumeOutcome = "retry"
)

// 处置原因（低基数：只区分失败类别，用于指标标签与死信头，不含 request_id/user_id）。
const (
	ConsumeReasonRequestMissing = "request_missing" // 消息指向的请求在 MySQL 中不存在
	ConsumeReasonIdentity       = "identity_mismatch"
	ConsumeReasonRetryExhausted = "retry_exhausted"
)

// ConsumeRequest 是 Kafka Consumer 交给业务核心的单条请求。
//
// 所有字段都只用于「定位 + 一致性校验」，不是落单依据：订单价格、库存、已售数量
// 一律在事务内从 MySQL 重新读取（见 message.go 的契约说明）。
type ConsumeRequest struct {
	RequestID      int64
	UserID         int64
	ActivityID     int64
	SkuID          int64
	IdempotencyKey string
	RequestHash    string
}

// ConsumeRequestResult 是 ConsumeRequest 的处置结论（error 为 nil 时有效）。
type ConsumeRequestResult struct {
	Outcome    ConsumeOutcome
	Reason     string     // Outcome 为 invalid/dead 时的低基数原因
	OrderID    int64      // 成功落单时的订单 id
	ErrorCode  codes.Code // 终态失败/死信的业务错误码
	RetryCount int        // Outcome 为 dead 时已消耗的重试次数
}

// consumeOutcome 记录单条消费的事务内结论，供事务提交后执行 Redis 补偿与指标观测
// （补偿不能先于 MySQL 终态提交）。
type consumeOutcome struct {
	result          string // 指标结果标签
	terminalFailure bool   // 业务失败/死信：需权威值收敛 + 清除标记（释放预扣）
	leftQueue       bool   // 请求已离开 queued（success/failed/dead）：需释放排队软上限计数
	duplicate       bool   // 请求已处于终态：安全跳过，无业务副作用
	technical       bool   // 技术失败：事务未写入终态，调用方需推进重试预算
	exhausted       bool   // 重试预算已耗尽：不得再执行业务
	technicalErr    error  // 技术失败原因（用于错误码与日志）

	requestID      int64
	activityID     int64
	skuID          int64
	userID         int64
	orderID        int64
	retryCount     int
	errorCode      codes.Code
	idempotencyKey string
}

// ConsumeFailureState 是一次技术失败记账的结果，供 Kafka 消费循环决定等待、死信或停止。
type ConsumeFailureState struct {
	// Resolved 表示请求已不在 queued（终态、被其他消费者处理或已不存在）：
	// 本条消息无需重试，可安全提交位移。
	Resolved bool
	// Exhausted 表示重试预算已耗尽：必须走死信并落 dead 终态。
	Exhausted bool
	// RetryCount 是本次记账后的重试次数（= 已消耗的重试次数）。
	RetryCount int
	// Backoff 是本次记账要求的退避时长（Exhausted 时无意义）。
	Backoff time.Duration
}

// ConsumeQueued 消费秒杀异步请求队列（供后台扫描器复用）。
// 出队 status=queued 且可重试的请求，逐条单事务落单/失败/重试/死信；返回本轮处理的请求数。
func (s *sFlashSale) ConsumeQueued(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	processed := 0
	for processed < limit {
		ok, err := s.consumeOne(ctx)
		if err != nil {
			return processed, err
		}
		if !ok {
			break
		}
		processed++
	}
	return processed, nil
}

// consumeOne 消费一条请求：出队（FOR UPDATE SKIP LOCKED 互斥领取）→ 快照 → 单事务落单 → 状态更新。
// 返回 (是否有请求被领取, error)。
func (s *sFlashSale) consumeOne(ctx context.Context) (bool, error) {
	var (
		processed bool
		outcome   consumeOutcome
	)
	started := time.Now()
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// 1. 出队并锁定一条可处理的 queued 请求（FOR UPDATE SKIP LOCKED 保证多实例互斥领取同一行）。
		//    next_attempt_at 过滤同时承担两件事：Scanner 自己的技术失败退避，以及
		//    Kafka 消费路径写入的退避（两条入口共享同一列时不至于互相抢跑）。
		var rows []*orderRequestRow
		if e := tx.Model(requestTable).Ctx(ctx).
			Where("status", requestStatusQueued).
			Where("next_attempt_at IS NULL OR next_attempt_at <= NOW()").
			Order("id").
			Limit(1).
			LockUpdateSkipLocked().
			Scan(&rows); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("出队秒杀请求: %w", e))
		}
		if len(rows) == 0 {
			return nil
		}
		processed = true
		return s.consumeLockedRequestInTx(ctx, tx, rows[0], &outcome)
	})
	if !processed {
		// 无请求可领取，无业务效果，不观测指标。
		return false, nil
	}
	if err != nil {
		// 技术失败：事务回滚，请求仍 queued（无终态），仅观测本次失败。
		metrics.ObserveConsume(ctx, metrics.ResultError, time.Since(started))
		return true, err
	}
	if outcome.technical {
		// 技术失败由独立短事务记账：业务事务已回滚，不能在同一事务里写 retry_count，
		// 否则「建单后扣库存失败」之类的半成品会被连带提交。
		// Scanner 没有死信主题，预算耗尽时直接落 dead（保持既有 AC-004 语义）。
		state, retryErr := s.persistConsumeFailure(ctx, outcome.requestID, outcome.errorCode, true, consumeBackoffSeconds)
		if retryErr != nil {
			return true, retryErr
		}
		switch {
		case state.Resolved:
			// 请求已被其他路径推进到终态：本次不再产生业务效果。
			outcome.duplicate = true
			outcome.result = metrics.ResultDuplicate
		case state.Exhausted:
			outcome.exhausted = true
			outcome.terminalFailure = true
			outcome.leftQueue = true
			outcome.result = metrics.ResultDead
			outcome.retryCount = state.RetryCount
		default:
			// 未达上限：保持 queued 与退避时间，本轮视为技术错误（不释放排队计数）。
			outcome.result = metrics.ResultError
			outcome.retryCount = state.RetryCount
		}
	}
	s.applyConsumeOutcome(ctx, &outcome, started)
	return true, nil
}

// ConsumeRequest 是 Kafka 消费入口的业务执行：针对消息指定的 request_id 跑一次完整消费事务。
//
// 与 Scanner 的关键差异：
//   - 只处理消息指向的请求，绝不扫描其他 queued 请求（不调用全局批量入口）；
//   - 使用 FOR UPDATE 而不是 SKIP LOCKED：并发消费者必须排队等锁，拿到锁后读到的是
//     最新已提交状态，因此第二个消费者会看到终态并安全跳过；
//   - 消息字段只做一致性校验，任何不匹配都按永久错误处理（不执行业务、不改数据库）。
//
// 返回 error 表示技术失败：调用方不得提交 Kafka 位移，应先推进重试预算再重投。
func (s *sFlashSale) ConsumeRequest(ctx context.Context, in ConsumeRequest) (ConsumeRequestResult, error) {
	var (
		outcome consumeOutcome
		result  ConsumeRequestResult
	)
	started := time.Now()
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		r, e := lockRequestByIDInTx(ctx, tx, in.RequestID)
		if e != nil {
			return e
		}
		if r == nil {
			result.Outcome = ConsumeOutcomeInvalid
			result.Reason = ConsumeReasonRequestMissing
			return nil
		}
		if reason := requestIdentityMismatch(in, r); reason != "" {
			result.Outcome = ConsumeOutcomeInvalid
			result.Reason = ConsumeReasonIdentity
			return nil
		}
		return s.consumeLockedRequestInTx(ctx, tx, r, &outcome)
	})
	if err != nil {
		// 事务级技术失败：没有任何终态写入，请求仍可被重试。
		metrics.ObserveConsume(ctx, metrics.ResultError, time.Since(started))
		return ConsumeRequestResult{}, err
	}
	if result.Outcome == ConsumeOutcomeInvalid {
		// 永久错误：不执行业务、不改数据库（消息不可信，不能据此改写权威行）。
		metrics.ObserveConsume(ctx, metrics.ResultInvalid, time.Since(started))
		return result, nil
	}
	if outcome.technical {
		metrics.ObserveConsume(ctx, metrics.ResultError, time.Since(started))
		return ConsumeRequestResult{
			Outcome:   ConsumeOutcomeRetry,
			ErrorCode: outcome.errorCode,
		}, outcome.technicalErr
	}
	if outcome.exhausted {
		// 预算已在之前的投递中耗尽：不再执行业务，交给调用方「先死信、后 dead 终态」。
		metrics.ObserveConsume(ctx, metrics.ResultDead, time.Since(started))
		return ConsumeRequestResult{
			Outcome:    ConsumeOutcomeDead,
			Reason:     ConsumeReasonRetryExhausted,
			ErrorCode:  outcome.errorCode,
			RetryCount: outcome.retryCount,
		}, nil
	}
	if outcome.duplicate {
		metrics.ObserveConsume(ctx, metrics.ResultDuplicate, time.Since(started))
		return ConsumeRequestResult{Outcome: ConsumeOutcomeDuplicate}, nil
	}

	// 正常业务终态：事务已提交，再执行事务外副作用（Redis 补偿、排队计数、日志与指标）。
	s.applyConsumeOutcome(ctx, &outcome, started)
	if outcome.result == metrics.ResultSuccess {
		return ConsumeRequestResult{Outcome: ConsumeOutcomeSuccess, OrderID: outcome.orderID}, nil
	}
	return ConsumeRequestResult{Outcome: ConsumeOutcomeFailed, ErrorCode: outcome.errorCode}, nil
}

// consumeLockedRequestInTx 是两条入口共享的业务事务核心。
//
// 前置条件：r 已被当前事务以 FOR UPDATE 锁定（调用方负责事务边界与提交）。
// 本函数只负责「状态闸门 + 业务副作用」，绝不推进重试预算与 dead 终态：
// 技术失败必须让事务干净回滚，否则半成品（订单已插入、库存未扣减）会被连带提交。
func (s *sFlashSale) consumeLockedRequestInTx(ctx context.Context, tx gdb.TX, r *orderRequestRow, out *consumeOutcome) error {
	out.requestID = r.Id
	out.activityID = r.ActivityId
	out.skuID = r.SkuId
	out.userID = r.UserId
	out.idempotencyKey = r.IdempotencyKey
	out.retryCount = r.RetryCount

	// 1. 状态闸门：只有 queued 才允许执行业务。
	//    success/failed/dead 一律跳过 —— 这是 Kafka 重复投递、Offset 提交失败重投、
	//    人工修复后重发同一条事件时唯一的业务幂等闸门（event_id 不作为去重台账）。
	if r.Status != requestStatusQueued {
		out.duplicate = true
		out.result = metrics.ResultDuplicate
		return nil
	}

	// 2. 重试预算闸门：计数已超预算的请求不再执行业务，避免「死信未落地却被反复执行」。
	if r.RetryCount > maxConsumeRetryAttempts {
		out.exhausted = true
		out.result = metrics.ResultDead
		out.errorCode = lastErrorCodeOf(r)
		return nil
	}

	// 3. SKU/商品可用性校验 + 快照（读后快照，服务端定价不信任客户端/消息）。
	//    这里的失败必须分类：SKU 不存在/下架是业务失败终态，数据库读失败是技术失败（可重试）。
	snap, err := s.resolveSku(ctx, r.SkuId)
	if err != nil {
		return s.finishFailureInTx(ctx, tx, r, err, out)
	}

	// 4. 建单 + 条件扣库存（复用 V1 条件扣减 + 唯一约束）。
	//    两者放在同一个保存点上：任一失败都回滚到保存点，保证「有订单必有扣减、有扣减必有订单」，
	//    随后写 failed 终态的事务提交时不会残留半成品。
	if err := tx.SavePoint(consumeOrderSavePoint); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("创建秒杀订单保存点: %w", err))
	}
	orderID, err := s.insertOrderInTx(ctx, tx, r.UserId, r.ActivityId, r.SkuId, snap, r.IdempotencyKey, r.RequestHash, generateOrderNo())
	if err != nil {
		if rbErr := tx.RollbackTo(consumeOrderSavePoint); rbErr != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("回滚秒杀订单保存点: %w", rbErr))
		}
		return s.finishFailureInTx(ctx, tx, r, err, out)
	}
	// 测试故障注入点：在「订单已写入、状态未更新」处返回 error → 整个事务回滚，
	// 模拟「消费者领取请求后、事务提交前崩溃」。生产环境 hook 恒为 nil，不改变事务边界。
	if consumeBeforeCommitHook != nil {
		if err := consumeBeforeCommitHook(ctx, r); err != nil {
			return err
		}
	}
	out.leftQueue = true
	out.result = metrics.ResultSuccess
	out.orderID = orderID
	return s.successRequestInTx(ctx, tx, r, orderID)
}

// finishFailureInTx 对一次消费失败分类并落库：
//   - 业务失败：写 failed 终态并提交（终态本身就是业务结论，不是技术错误）；
//   - 技术失败：不写任何终态，由调用方在事务之外推进重试预算（保证业务事务干净回滚）。
func (s *sFlashSale) finishFailureInTx(ctx context.Context, tx gdb.TX, r *orderRequestRow, cause error, out *consumeOutcome) error {
	business, code := classifyConsumeError(cause)
	out.errorCode = code
	if business {
		out.terminalFailure = true
		out.leftQueue = true
		out.result = metrics.ResultFailed
		return s.failRequestInTx(ctx, tx, r, code)
	}
	out.technical = true
	out.technicalErr = cause
	out.result = metrics.ResultError
	return nil
}

// applyConsumeOutcome 执行事务提交后的副作用：排队计数、Redis 补偿、结构化日志与指标。
// 这些动作都必须在 MySQL 终态提交之后执行，且本身幂等（重复执行结果不变）。
func (s *sFlashSale) applyConsumeOutcome(ctx context.Context, out *consumeOutcome, started time.Time) {
	if out.leftQueue {
		// 请求离开 queued，释放排队软上限计数（best-effort）。
		s.decrQueueCount(ctx, out.activityID, out.skuID)
	}
	if out.terminalFailure {
		// 终态失败（failed/dead）在事务提交后权威值收敛（幂等）+ 清除标记，释放预扣、无半成品。
		s.convergeStockAndMarkers(ctx, out.activityID, out.skuID, out.userID, out.idempotencyKey)
	}
	// 结构化日志：仅记录落单成功与终态失败，技术重试（ResultError）不逐条记录，避免高频噪声。
	switch out.result {
	case metrics.ResultSuccess:
		logx.Info(ctx, "flashsale_consumed", "秒杀异步落单成功", logx.Fields{
			"user_id":      out.userID,
			"flashsale_id": out.activityID,
			"order_id":     out.orderID,
			"sku_id":       out.skuID,
			"request_id":   out.requestID,
		})
	case metrics.ResultFailed, metrics.ResultDead:
		logx.Warning(ctx, "flashsale_consume_failed", "秒杀异步落单终态失败", logx.Fields{
			"user_id":      out.userID,
			"flashsale_id": out.activityID,
			"sku_id":       out.skuID,
			"request_id":   out.requestID,
			"result":       out.result,
			"error_code":   out.errorCode,
		})
	}
	metrics.ObserveConsume(ctx, out.result, time.Since(started))
}

// successRequestInTx 将请求标记为 success 并关联成功订单 id（与成功订单创建同一事务）。
func (s *sFlashSale) successRequestInTx(ctx context.Context, tx gdb.TX, r *orderRequestRow, orderID int64) error {
	if _, err := tx.Model(requestTable).Ctx(ctx).Where("id", r.Id).Data(g.Map{
		"status":         requestStatusSuccess,
		"flash_order_id": orderID,
	}).Update(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新秒杀请求成功状态: %w", err))
	}
	return nil
}

// failRequestInTx 将请求标记为业务失败终态（failed），记录失败码。
func (s *sFlashSale) failRequestInTx(ctx context.Context, tx gdb.TX, r *orderRequestRow, code codes.Code) error {
	if _, err := tx.Model(requestTable).Ctx(ctx).Where("id", r.Id).Data(g.Map{
		"status":          requestStatusFailed,
		"last_error_code": code,
	}).Update(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新秒杀请求失败状态: %w", err))
	}
	return nil
}

// lockRequestByIDInTx 在事务内按 id 锁定请求行（FOR UPDATE），未命中返回 nil。
// 使用 FOR UPDATE 而非 SKIP LOCKED：同一 Request 的并发消费者必须排队等锁，
// 后来者拿到锁后读到的是前一个事务已提交的终态，从而安全跳过而不是重复落单。
func lockRequestByIDInTx(ctx context.Context, tx gdb.TX, requestID int64) (*orderRequestRow, error) {
	if requestID <= 0 {
		return nil, nil
	}
	var rows []*orderRequestRow
	if err := tx.Model(requestTable).Ctx(ctx).
		Where("id", requestID).
		LockUpdate().
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("锁定秒杀请求 %d: %w", requestID, err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// requestIdentityMismatch 校验消息中的身份字段与 MySQL 权威行是否一致，一致返回空串。
// 消息字段只用于校验：不一致说明消息不属于这条请求（错投/伪造/上游 bug），
// 必须按永久错误进入死信，绝不能按消息内容改写数据库里的权威请求。
func requestIdentityMismatch(in ConsumeRequest, r *orderRequestRow) string {
	switch {
	case in.UserID != r.UserId:
		return "user_id"
	case in.ActivityID != r.ActivityId:
		return "activity_id"
	case in.SkuID != r.SkuId:
		return "sku_id"
	case in.IdempotencyKey != r.IdempotencyKey:
		return "idempotency_key"
	case in.RequestHash != r.RequestHash:
		return "request_hash"
	}
	return ""
}

// PersistConsumeFailure 记录一次 Kafka 消费的技术失败并推进重试预算（独立短事务）。
//
// 只在「业务事务已回滚、没有任何终态写入」时调用：
//   - 预算未耗尽：retry_count+1 + next_attempt_at 退避，请求保持 queued；
//   - 预算耗尽：retry_count 推进到预算外（仍保持 queued），由调用方在死信 Broker ACK
//     之后再落 dead —— 顺序不能颠倒，否则死信投递失败时终态已改、原始请求却无人再处理。
//
// 返回 error 表示连重试次数都无法持久化：此时调用方既不能提交位移、也不能擅自丢弃消息。
func (s *sFlashSale) PersistConsumeFailure(ctx context.Context, requestID int64, code codes.Code) (ConsumeFailureState, error) {
	state, err := s.persistConsumeFailure(ctx, requestID, code, false, kafkaConsumeBackoff)
	if err != nil {
		return ConsumeFailureState{}, err
	}
	return ConsumeFailureState{
		Resolved:   state.Resolved,
		Exhausted:  state.Exhausted,
		RetryCount: state.RetryCount,
		Backoff:    state.Backoff,
	}, nil
}

// consumeFailureRecord 是一次技术失败记账的完整结果（含 Redis 补偿所需的业务字段）。
type consumeFailureRecord struct {
	Resolved       bool
	Exhausted      bool
	Dead           bool
	RetryCount     int
	Backoff        time.Duration
	ActivityID     int64
	SkuID          int64
	UserID         int64
	IdempotencyKey string
}

// persistConsumeFailure 在独立事务中记录一次技术失败并推进重试预算。
//
// deadInline=true：预算耗尽时立即把请求落成 dead（MySQL Scanner 路径，无死信主题）；
// deadInline=false：只推进计数，dead 由调用方在死信 ACK 之后写入（Kafka 路径）。
//
// 计数与状态都用条件更新（WHERE status=queued）保证幂等：并发下重复记账不会把
// 已经进入终态的请求改回 queued，也不会覆盖其他消费者写入的终态。
func (s *sFlashSale) persistConsumeFailure(
	ctx context.Context,
	requestID int64,
	code codes.Code,
	deadInline bool,
	backoffFor func(retryCount int) time.Duration,
) (*consumeFailureRecord, error) {
	rec := &consumeFailureRecord{}
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		r, err := lockRequestByIDInTx(ctx, tx, requestID)
		if err != nil {
			return err
		}
		if r == nil || r.Status != requestStatusQueued {
			// 请求不存在或已终态：本次失败记账没有意义，交给调用方按重复消息处理。
			rec.Resolved = true
			return nil
		}
		rec.ActivityID = r.ActivityId
		rec.SkuID = r.SkuId
		rec.UserID = r.UserId
		rec.IdempotencyKey = r.IdempotencyKey

		newRetry := r.RetryCount + 1
		rec.RetryCount = newRetry
		if newRetry > maxConsumeRetryAttempts {
			rec.Exhausted = true
			data := g.Map{
				"retry_count":     newRetry,
				"last_error_code": code,
				"next_attempt_at": nil,
			}
			if deadInline {
				data["status"] = requestStatusDead
			}
			affected, e := tx.Model(requestTable).Ctx(ctx).
				Where("id", requestID).
				Where("status", requestStatusQueued).
				Data(data).Update()
			if e != nil {
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("推进秒杀请求重试预算: %w", e))
			}
			if rows, _ := affected.RowsAffected(); rows == 0 {
				rec.Resolved = true
				rec.Exhausted = false
				return nil
			}
			rec.Dead = deadInline
			return nil
		}

		backoff := backoffFor(newRetry)
		rec.Backoff = backoff
		seconds := int64(backoff / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		result, e := tx.Model(requestTable).Ctx(ctx).
			Where("id", requestID).
			Where("status", requestStatusQueued).
			Data(g.Map{
				"retry_count":     newRetry,
				"last_error_code": code,
				"next_attempt_at": gdb.Raw("DATE_ADD(NOW(), INTERVAL " + strconv.FormatInt(seconds, 10) + " SECOND)"),
			}).Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("记录秒杀请求重试: %w", e))
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			rec.Resolved = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !deadInline {
		// Kafka 路径下 dead 尚未写入，Redis 补偿由 MarkRequestDead 在终态提交后执行。
		rec.Dead = false
	}
	return rec, nil
}

// MarkRequestDead 在死信消息获得 Broker ACK 之后把请求落成 dead 终态（条件更新，幂等），
// 随后执行既有 Redis 补偿（权威值收敛 + 清除一个人一单/幂等标记）。
//
// 返回 (是否由本次调用完成迁移, error)：已处于终态时返回 (false, nil)，属于重复消息的安全结果。
// 调用顺序不可颠倒：先死信 ACK、后 MySQL 终态，避免「终态已改但死信没发出去」导致请求彻底消失。
func (s *sFlashSale) MarkRequestDead(ctx context.Context, requestID int64, code codes.Code) (bool, error) {
	var (
		moved  bool
		target *orderRequestRow
	)
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		r, err := lockRequestByIDInTx(ctx, tx, requestID)
		if err != nil {
			return err
		}
		if r == nil || r.Status != requestStatusQueued {
			return nil
		}
		target = r
		result, e := tx.Model(requestTable).Ctx(ctx).
			Where("id", requestID).
			Where("status", requestStatusQueued).
			Data(g.Map{
				"status":          requestStatusDead,
				"last_error_code": code,
				"next_attempt_at": nil,
			}).Update()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("标记秒杀请求死信终态: %w", e))
		}
		if rows, _ := result.RowsAffected(); rows > 0 {
			moved = true
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if !moved || target == nil {
		return false, nil
	}
	// MySQL 终态提交后才补偿 Redis：顺序颠倒会在补偿成功、终态回滚时造成「已回补但请求仍 queued」。
	s.convergeStockAndMarkers(ctx, target.ActivityId, target.SkuId, target.UserId, target.IdempotencyKey)
	return true, nil
}

// lastErrorCodeOf 返回请求行上记录的最近一次失败码；缺失时按内部错误处理。
func lastErrorCodeOf(r *orderRequestRow) codes.Code {
	if r.LastErrorCode == nil {
		return codes.CodeInternalError
	}
	return *r.LastErrorCode
}

// classifyConsumeError 分类消费失败：业务失败（终态 failed）与技术失败（重试/死信）。
// 返回 (业务失败 bool, 失败码 codes.Code)。
func classifyConsumeError(err error) (bool, codes.Code) {
	var dk *duplicateKeyErr
	if errors.As(err, &dk) {
		switch {
		case strings.Contains(dk.key, "uk_flash_one_per_user"):
			return true, codes.CodeFlashSaleAlreadyPurchased
		case strings.Contains(dk.key, "uk_flash_idempotency"):
			return true, codes.CodeFlashSaleIdempotencyConflict
		default: // uk_flash_order_no 撞号 → 技术重试
			return false, codes.CodeInternalError
		}
	}
	code := codes.FromError(err)
	switch code {
	case codes.CodeFlashSaleActivityNotFound,
		codes.CodeFlashSaleNotInTimeWindow,
		codes.CodeFlashSaleStockInsufficient,
		codes.CodeFlashSaleAlreadyPurchased,
		codes.CodeFlashSaleIdempotencyConflict,
		codes.CodeFlashSaleSkuUnavailable,
		codes.CodeFlashSaleInvalidArgument,
		codes.CodeInvalidArgument,
		codes.CodeSkuNotFound,
		codes.CodeProductNotFound:
		return true, code
	default:
		return false, code
	}
}

// consumeBackoffSeconds 计算 Scanner 技术失败退避秒数：指数退避 5s/10s/20s，封顶 60s。
func consumeBackoffSeconds(retryCount int) time.Duration {
	return time.Duration(consumeBackoffSecondsValue(retryCount)) * time.Second
}

// consumeBackoffSecondsValue 是 consumeBackoffSeconds 的秒数版本（供 SQL 间隔与测试复用）。
func consumeBackoffSecondsValue(retryCount int) int64 {
	s := int64(consumeBackoffBaseSeconds)
	for i := 1; i < retryCount && s < 60; i++ {
		s *= 2
		if s > 60 {
			s = 60
		}
	}
	return s
}

// Kafka 消费路径的退避参数：单条消息内的重试等待必须显著短于 Scanner 的秒级退避，
// 因为这段等待发生在持有 Poll 批次的窗口内（BlockRebalanceOnPoll），
// 过长会拖延分区再平衡。1s→2s→4s，封顶 5s，累计不超过 7s，远低于 30s 会话超时。
const (
	kafkaConsumeBackoffBase = time.Second
	kafkaConsumeBackoffMax  = 5 * time.Second
)

// kafkaConsumeBackoff 计算第 retryCount 次重试前的等待时长（指数退避、封顶）。
// 定义为变量以便测试注入毫秒级退避（生产路径始终是上面的 1s→5s 区间）。
var kafkaConsumeBackoff = func(retryCount int) time.Duration {
	if retryCount <= 1 {
		return kafkaConsumeBackoffBase
	}
	d := kafkaConsumeBackoffBase
	for i := 1; i < retryCount; i++ {
		if d >= kafkaConsumeBackoffMax {
			return kafkaConsumeBackoffMax
		}
		d *= 2
	}
	if d > kafkaConsumeBackoffMax {
		d = kafkaConsumeBackoffMax
	}
	return d
}
