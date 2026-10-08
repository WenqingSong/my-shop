// 秒杀 V3（异步下单）消费者实现：从 MySQL 出队表 flash_sale_order_requests 出队 queued 请求，
// 逐条在单事务内完成「活动校验 + 时间窗 + 条件扣库存 + 建单 + 状态更新」，复用 V1 条件扣减与唯一约束兜底。
// 失败分两类：业务失败 → 终态 failed；技术失败 → 退避重试，超上限 → 死信 dead。两类终态失败均在事务提交后补偿 Redis。
package flashsale

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

const (
	// maxConsumeRetryAttempts 技术失败的最大重试次数；超过后落 dead（死信/待修复）。
	maxConsumeRetryAttempts = 3
	// consumeBackoffBaseSeconds 技术失败退避的基础秒数（指数退避：5s、10s、20s，封顶 60s）。
	consumeBackoffBaseSeconds = 5
)

// consumeBeforeCommitHook 是消费事务提交前注入故障的测试钩子（生产环境恒为 nil）。
// 返回非 nil 使当前消费事务整体回滚，模拟「消费者领取请求后、事务提交前崩溃」，
// 用于验证 AC-003/INV-015「回滚后重投只产生一次业务效果」。测试使用后须复原为 nil。
var consumeBeforeCommitHook func(ctx context.Context, r *orderRequestRow) error

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

// consumeOutcome 记录单条消费的事务内终态结果，供事务提交后执行 Redis 补偿（补偿不能先于 MySQL 终态提交）。
type consumeOutcome struct {
	terminalFailure bool
	activityID      int64
	skuID           int64
	userID          int64
	idempotencyKey  string
}

// consumeOne 消费一条请求：出队（FOR UPDATE SKIP LOCKED 互斥领取）→ 快照 → 单事务落单 → 状态更新。
// 返回 (是否有请求被领取, error)。
func (s *sFlashSale) consumeOne(ctx context.Context) (bool, error) {
	var (
		processed bool
		outcome   consumeOutcome
	)
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// 1. 出队并锁定一条可处理的 queued 请求（FOR UPDATE SKIP LOCKED 保证多实例互斥领取同一行）。
		var rows []*orderRequestRow
		if err := tx.Model("flash_sale_order_requests").Ctx(ctx).
			Where("status", requestStatusQueued).
			Where("next_attempt_at IS NULL OR next_attempt_at <= NOW()").
			Order("id").
			Limit(1).
			LockUpdateSkipLocked().
			Scan(&rows); err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("出队秒杀请求: %w", err))
		}
		if len(rows) == 0 {
			return nil
		}
		r := rows[0]
		processed = true
		outcome.activityID = r.ActivityId
		outcome.skuID = r.SkuId
		outcome.userID = r.UserId
		outcome.idempotencyKey = r.IdempotencyKey

		// 2. SKU/商品可用性校验 + 快照（读后快照，服务端定价不信任客户端）。
		snap, err := s.resolveSku(ctx, r.SkuId)
		if err != nil {
			outcome.terminalFailure = true
			return s.failRequestInTx(ctx, tx, r, codes.FromError(err))
		}

		// 3. 单事务落单（复用 V1 条件扣减 + 唯一约束）；成功则关联 flash_order_id。
		orderID, err := s.insertOrderInTx(ctx, tx, r.UserId, r.ActivityId, r.SkuId, snap, r.IdempotencyKey, r.RequestHash, generateOrderNo())
		if err != nil {
			business, code := classifyConsumeError(err)
			if business {
				outcome.terminalFailure = true
				return s.failRequestInTx(ctx, tx, r, code)
			}
			return s.retryOrDeadRequestInTx(ctx, tx, r, code, &outcome)
		}
		// 测试故障注入点：在「订单已写入、状态未更新」处返回 error → 整个事务回滚，
		// 模拟「消费者领取请求后、事务提交前崩溃」。生产环境 hook 恒为 nil，不改变事务边界。
		if consumeBeforeCommitHook != nil {
			if err := consumeBeforeCommitHook(ctx, r); err != nil {
				return err
			}
		}
		return s.successRequestInTx(ctx, tx, r, orderID)
	})
	if err != nil {
		return processed, err
	}
	if outcome.terminalFailure {
		// 终态失败（failed/dead）在事务提交后权威值收敛（幂等）+ 清除标记，释放预扣、无半成品。
		s.convergeStockAndMarkers(ctx, outcome.activityID, outcome.skuID, outcome.userID, outcome.idempotencyKey)
	}
	return processed, nil
}

// successRequestInTx 将请求标记为 success 并关联成功订单 id（与成功订单创建同一事务）。
func (s *sFlashSale) successRequestInTx(ctx context.Context, tx gdb.TX, r *orderRequestRow, orderID int64) error {
	if _, err := tx.Model("flash_sale_order_requests").Ctx(ctx).Where("id", r.Id).Data(g.Map{
		"status":         requestStatusSuccess,
		"flash_order_id": orderID,
	}).Update(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新秒杀请求成功状态: %w", err))
	}
	return nil
}

// failRequestInTx 将请求标记为业务失败终态（failed），记录失败码。
func (s *sFlashSale) failRequestInTx(ctx context.Context, tx gdb.TX, r *orderRequestRow, code codes.Code) error {
	if _, err := tx.Model("flash_sale_order_requests").Ctx(ctx).Where("id", r.Id).Data(g.Map{
		"status":          requestStatusFailed,
		"last_error_code": code,
	}).Update(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新秒杀请求失败状态: %w", err))
	}
	return nil
}

// retryOrDeadRequestInTx 处理技术失败：重试次数 +1 并退避；超上限则落 dead（死信/待修复）。
// 落 dead 时置 outcome.terminalFailure，提交后补偿 Redis 预扣/标记。
func (s *sFlashSale) retryOrDeadRequestInTx(ctx context.Context, tx gdb.TX, r *orderRequestRow, code codes.Code, outcome *consumeOutcome) error {
	newRetry := r.RetryCount + 1
	if newRetry > maxConsumeRetryAttempts {
		outcome.terminalFailure = true
		if _, err := tx.Model("flash_sale_order_requests").Ctx(ctx).Where("id", r.Id).Data(g.Map{
			"status":          requestStatusDead,
			"retry_count":     newRetry,
			"last_error_code": code,
		}).Update(); err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新秒杀请求死信状态: %w", err))
		}
		return nil
	}

	backoff := consumeBackoffSeconds(newRetry)
	if _, err := tx.Model("flash_sale_order_requests").Ctx(ctx).Where("id", r.Id).Data(g.Map{
		"retry_count":     newRetry,
		"last_error_code": code,
		"next_attempt_at": gdb.Raw("DATE_ADD(NOW(), INTERVAL " + strconv.FormatInt(backoff, 10) + " SECOND)"),
	}).Update(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新秒杀请求重试状态: %w", err))
	}
	return nil
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

// consumeBackoffSeconds 计算技术失败退避秒数：指数退避 5s/10s/20s，封顶 60s。
func consumeBackoffSeconds(retryCount int) int64 {
	s := int64(consumeBackoffBaseSeconds)
	for i := 1; i < retryCount && s < 60; i++ {
		s *= 2
		if s > 60 {
			s = 60
		}
	}
	return s
}
