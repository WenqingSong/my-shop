// 秒杀 V3（异步下单）请求生命周期实现：入队、结果查询与请求状态映射。
// flash_sale_order_requests 承载异步请求的 queued/success/failed/dead 生命周期，
// 同时充当消费者出队队列；它不改变成功订单事实来源（flash_sale_orders 行存在即成功订单）。
package flashsale

import (
	"context"
	"fmt"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// 异步请求状态（flash_sale_order_requests.status 的 DB TINYINT）。
const (
	requestStatusQueued  = 0 // 排队中（含退避重试，retry_count/next_attempt_at 承载技术重试）
	requestStatusSuccess = 1 // 落单成功（与成功订单创建同事务）
	requestStatusFailed  = 2 // 业务失败终态（库存不足/时间窗结束/已购等）
	requestStatusDead    = 3 // 技术失败重试超限（死信/待修复）
)

// orderRequestRow 是 flash_sale_order_requests 表的一条记录。
type orderRequestRow struct {
	Id             int64       `json:"id"`
	UserId         int64       `json:"user_id"`
	ActivityId     int64       `json:"activity_id"`
	SkuId          int64       `json:"sku_id"`
	IdempotencyKey string      `json:"idempotency_key"`
	RequestHash    string      `json:"request_hash"`
	Status         int         `json:"status"`
	RetryCount     int         `json:"retry_count"`
	NextAttemptAt  *gtime.Time `json:"next_attempt_at"`
	LastErrorCode  *int        `json:"last_error_code"`
	FlashOrderId   *int64      `json:"flash_order_id"`
}

// requestStatusString 将 DB 状态映射为 API 字符串枚举。
func requestStatusString(s int) string {
	switch s {
	case requestStatusSuccess:
		return v1.RequestStatusSuccess
	case requestStatusFailed:
		return v1.RequestStatusFailed
	case requestStatusDead:
		return v1.RequestStatusDead
	default:
		return v1.RequestStatusQueued
	}
}

// enqueue 插入秒杀请求（status=queued）并返回排队受理结果。
// 撞 uk_request_idempotency 时补偿本次预扣/标记并读回既有请求结果（同 hash 返回既有状态，异 hash 12005）；
// 其余 DB 错误返回内部错误，由调用方补偿预扣/标记。
func (s *sFlashSale) enqueue(ctx context.Context, userID, activityID, skuID int64, idempotencyKey, hash string) (*v1.CreateOrderRes, error) {
	_, err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).Data(g.Map{
		"user_id":         userID,
		"activity_id":     activityID,
		"sku_id":          skuID,
		"idempotency_key": idempotencyKey,
		"request_hash":    hash,
		"status":          requestStatusQueued,
	}).InsertAndGetId()
	if err != nil {
		if key := duplicateKeyName(err); key != "" && strings.Contains(key, "uk_request_idempotency") {
			// 幂等键已存在（idem 标记 TTL 过期但请求仍落库）：补偿本次闸门的预扣/标记，再读回既有结果。
			s.compensatePreDeductAndMarkers(ctx, activityID, skuID, userID, idempotencyKey)
			return s.resultForExistingRequest(ctx, userID, idempotencyKey, hash)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入秒杀请求: %w", err))
	}
	return &v1.CreateOrderRes{
		Status:         v1.RequestStatusQueued,
		IdempotencyKey: idempotencyKey,
		ActivityId:     activityID,
		SkuId:          skuID,
	}, nil
}

// resultForExistingRequest 按 user_id + idempotency_key 读回既有异步请求并映射为下单响应。
// 同请求指纹返回既有状态；异指纹返回 12005；未命中（极端崩溃窗口）返回 503 提示稍后重试。
func (s *sFlashSale) resultForExistingRequest(ctx context.Context, userID int64, idempotencyKey, hash string) (*v1.CreateOrderRes, error) {
	r, err := s.findRequestByIdempotencyKey(ctx, userID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, codes.New(codes.CodeServiceUnavailable)
	}
	if r.RequestHash != hash {
		return nil, codes.New(codes.CodeFlashSaleIdempotencyConflict)
	}
	return &v1.CreateOrderRes{
		Status:         requestStatusString(r.Status),
		IdempotencyKey: r.IdempotencyKey,
		ActivityId:     r.ActivityId,
		SkuId:          r.SkuId,
	}, nil
}

// GetOrderResult 查询本人秒杀下单结果（排队/成功/失败/死信），归属隔离。
// 按 user_id + activity_id + idempotency_key 验证归属；不存在/非本人统一 404。
func (s *sFlashSale) GetOrderResult(ctx context.Context, userID, activityID int64, idempotencyKey string) (*v1.GetOrderResultRes, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	r, err := s.findRequestByActivityKey(ctx, userID, activityID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, codes.New(codes.CodeNotFound)
	}

	res := &v1.GetOrderResultRes{Status: requestStatusString(r.Status)}
	if r.Status == requestStatusSuccess {
		if r.FlashOrderId == nil {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("秒杀请求 success 但未关联成功订单"))
		}
		o, err := s.loadOrder(ctx, userID, *r.FlashOrderId)
		if err != nil {
			return nil, err
		}
		if o == nil {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("秒杀请求 success 但未找到成功订单"))
		}
		res.Order = o
	}
	if r.Status == requestStatusFailed || r.Status == requestStatusDead {
		if r.LastErrorCode != nil {
			res.FailCode = *r.LastErrorCode
		}
	}
	return res, nil
}

// findRequestByIdempotencyKey 按 user_id + idempotency_key 查询异步请求（全局唯一，跨活动），未命中返回 nil。
func (s *sFlashSale) findRequestByIdempotencyKey(ctx context.Context, userID int64, key string) (*orderRequestRow, error) {
	var rows []*orderRequestRow
	if err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).
		Where("user_id", userID).Where("idempotency_key", key).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按幂等键查询秒杀请求: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// findInflightQueued 统计指定活动下 status=queued 的请求数（按 SKU 分组），
// 供对账/预热公式 remaining = total_stock - sold - inflight_queued 使用（计入在途预扣，避免错误回补超预扣）。
func (s *sFlashSale) findInflightQueued(ctx context.Context, activityID int64) (map[int64]int64, error) {
	var rows []struct {
		SkuId int64 `json:"sku_id"`
		N     int64 `json:"n"`
	}
	if err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).
		Fields("sku_id", "COUNT(*) AS n").
		Where("activity_id", activityID).
		Where("status", requestStatusQueued).
		Group("sku_id").
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计在途秒杀请求: %w", err))
	}
	m := make(map[int64]int64, len(rows))
	for _, r := range rows {
		m[r.SkuId] = r.N
	}
	return m, nil
}

// findRequestByActivityKey 按 user_id + activity_id + idempotency_key 查询异步请求（结果查询归属隔离），未命中返回 nil。
func (s *sFlashSale) findRequestByActivityKey(ctx context.Context, userID, activityID int64, key string) (*orderRequestRow, error) {
	var rows []*orderRequestRow
	if err := g.DB().Model("flash_sale_order_requests").Ctx(ctx).
		Where("user_id", userID).
		Where("activity_id", activityID).
		Where("idempotency_key", key).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询秒杀请求结果: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}
