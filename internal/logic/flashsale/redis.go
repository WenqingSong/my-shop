// Redis 热路径实现（秒杀 V2）：活动/库存预热、Lua 原子预扣闸门、售罄/穿透快速失败、
// 一人一单/幂等标记与对账收敛。Redis 是派生缓存与加速闸门，MySQL 仍是权威事实来源与正确性兜底。
package flashsale

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// Redis key 前缀（B 类 namespace，与会话 iam: 前缀隔离）。
const (
	flashSaleActivityKeyPrefix = "flashsale:activity:"
	flashSaleStockKeyPrefix    = "flashsale:stock:"
	flashSaleBoughtKeyPrefix   = "flashsale:bought:"
	flashSaleIdemKeyPrefix     = "flashsale:idem:"
	flashSaleSoldoutKeyPrefix  = "flashsale:soldout:"
	flashSaleNullKeyPrefix     = "flashsale:null:"
)

// 活动元数据 Hash 字段名（与 Lua 脚本约定一致）。
const (
	flashSaleFieldStatus = "status"
	flashSaleFieldStart  = "start"
	flashSaleFieldEnd    = "end"
)

// TTL 常量（秒）。
const (
	// flashSaleGraceTTL 活动域 key 在活动结束后保留的宽限秒数（覆盖在途请求，保证不残留脏数据）。
	flashSaleGraceTTL = 60
	// flashSaleNullTTL 空值/负缓存标记的短 TTL。
	flashSaleNullTTL = 60
)

// gateResult 是 Lua 抢购闸门的返回码（内部协议，非客户端错误码）。
type gateResult string

const (
	gateNotFound           gateResult = "NOT_FOUND"
	gateNotInWindow        gateResult = "NOT_IN_WINDOW"
	gateSoldOut            gateResult = "SOLD_OUT"
	gateAlreadyPurchased   gateResult = "ALREADY_PURCHASED"
	gateIdempotentHit      gateResult = "IDEMPOTENT_HIT"
	gateIdempotentConflict gateResult = "IDEMPOTENT_CONFLICT"
	gateSkuNotBound        gateResult = "SKU_NOT_BOUND"
	gatePassed             gateResult = "GATE_PASSED"
)

// flashSaleGateScript 是抢购热路径 Lua 脚本：原子完成空值标记检查 → 活动存在/启用/时间窗检查 →
// 售罄检查 → 幂等检查 → 一人一单检查 → 剩余库存检查与 DECR 预扣。
// 幂等检查先于一人一单：同幂等键重试（同 hash）应返回既有请求状态（200），而非已购（12004），与 V1 语义一致。
// KEYS：1=null 2=activity 3=stock 4=bought 5=idem 6=soldout
// ARGV：1=now(unix 秒) 2=request_hash 3=grace(秒)
//
// 注意：SKU 未绑定（活动已预热但无该 SKU 库存 key）时返回 SKU_NOT_BOUND，透传 MySQL 走既有 1001 语义；
// 预扣成功（GATE_PASSED）≠ 下单成功，MySQL 事务失败时由调用方补偿预扣。
// V3：GATE_PASSED 时原子写入一人一单/幂等标记（去重并发入队），消费/入队失败时由调用方清除。
const flashSaleGateScript = `
local function field(t, name)
  for i = 1, #t, 2 do
    if t[i] == name then return t[i + 1] end
  end
  return nil
end

if redis.call('EXISTS', KEYS[1]) == 1 then
  return 'NOT_FOUND'
end

local a = redis.call('HGETALL', KEYS[2])
if #a == 0 then
  return 'NOT_FOUND'
end

local status = field(a, 'status')
local start = tonumber(field(a, 'start'))
local finish = tonumber(field(a, 'end'))
if status ~= '1' then
  return 'NOT_FOUND'
end

local now = tonumber(ARGV[1])
local grace = tonumber(ARGV[3])
if start == nil or finish == nil or now < start or now >= finish then
  return 'NOT_IN_WINDOW'
end

local function markSoldout()
  local ttl = finish - now + grace
  if ttl < 1 then ttl = 1 end
  redis.call('SET', KEYS[6], '1', 'EX', ttl)
end

if redis.call('EXISTS', KEYS[6]) == 1 then
  return 'SOLD_OUT'
end

local idem = redis.call('GET', KEYS[5])
if idem then
  if idem == ARGV[2] then
    return 'IDEMPOTENT_HIT'
  end
  return 'IDEMPOTENT_CONFLICT'
end

if redis.call('EXISTS', KEYS[4]) == 1 then
  return 'ALREADY_PURCHASED'
end

local remaining = tonumber(redis.call('GET', KEYS[3]))
if remaining == nil then
  return 'SKU_NOT_BOUND'
end

if remaining <= 0 then
  markSoldout()
  return 'SOLD_OUT'
end

local after = redis.call('DECR', KEYS[3])
if after < 0 then
  redis.call('INCR', KEYS[3])
  markSoldout()
  return 'SOLD_OUT'
end

-- GATE_PASSED（V3）：预扣成功时原子写入一人一单/幂等标记，去重并发入队；
-- 消费失败或入队失败时由调用方清除标记 + 补偿预扣，避免孤儿标记。
local ttl = finish - now + grace
if ttl < 1 then ttl = 1 end
redis.call('SET', KEYS[4], '1', 'EX', ttl)
redis.call('SET', KEYS[5], ARGV[2], 'EX', ttl)
return 'GATE_PASSED'
`

func flashSaleActivityKey(activityID int64) string {
	return flashSaleActivityKeyPrefix + strconv.FormatInt(activityID, 10)
}

func flashSaleStockKey(activityID, skuID int64) string {
	return flashSaleStockKeyPrefix + strconv.FormatInt(activityID, 10) + ":" + strconv.FormatInt(skuID, 10)
}

func flashSaleBoughtKey(activityID, skuID, userID int64) string {
	return flashSaleBoughtKeyPrefix + strconv.FormatInt(activityID, 10) + ":" + strconv.FormatInt(skuID, 10) + ":" + strconv.FormatInt(userID, 10)
}

func flashSaleIdemKey(userID int64, idempotencyKey string) string {
	return flashSaleIdemKeyPrefix + strconv.FormatInt(userID, 10) + ":" + idempotencyKey
}

func flashSaleSoldoutKey(activityID, skuID int64) string {
	return flashSaleSoldoutKeyPrefix + strconv.FormatInt(activityID, 10) + ":" + strconv.FormatInt(skuID, 10)
}

func flashSaleNullKey(activityID int64) string {
	return flashSaleNullKeyPrefix + strconv.FormatInt(activityID, 10)
}

// flashSaleActivityTTL 计算活动域 key 的 TTL（秒）：end - now + grace，至少 1 秒。
func flashSaleActivityTTL(nowUnix, endUnix int64) int64 {
	ttl := endUnix - nowUnix + flashSaleGraceTTL
	if ttl < 1 {
		ttl = 1
	}
	return ttl
}

// activityCacheRow 是预热/对账所需的精简活动行；StartTs/EndTs 为 MySQL UNIX_TIMESTAMP
// 计算的绝对秒，避免 Go↔MySQL 时区漂移导致闸门时间窗误判。
type activityCacheRow struct {
	Id      int64 `json:"id"`
	Status  int   `json:"status"`
	StartTs int64 `json:"start_ts"`
	EndTs   int64 `json:"end_ts"`
}

// findActivityCache 查询活动（status + UNIX_TIMESTAMP 起止），不存在返回 nil。
func (s *sFlashSale) findActivityCache(ctx context.Context, activityID int64) (*activityCacheRow, error) {
	rec, err := g.DB().Model("flash_sale_activities").Ctx(ctx).
		Fields("id", "status", "UNIX_TIMESTAMP(start_time) AS start_ts", "UNIX_TIMESTAMP(end_time) AS end_ts").
		Where("id", activityID).One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询秒杀活动缓存: %w", err))
	}
	if rec == nil || rec.IsEmpty() {
		return nil, nil
	}
	return &activityCacheRow{
		Id:      rec["id"].Int64(),
		Status:  rec["status"].Int(),
		StartTs: rec["start_ts"].Int64(),
		EndTs:   rec["end_ts"].Int64(),
	}, nil
}

// runGate 执行 Lua 抢购闸门。返回 (gateResult, nil) 表示 Lua 正常执行（含各类快速失败）；
// 返回 error 表示 Redis 不可用或 Lua 执行失败，调用方应降级走纯 MySQL 路径。
func (s *sFlashSale) runGate(ctx context.Context, activityID, skuID, userID int64, idempotencyKey, hash string) (gateResult, error) {
	v, err := g.Redis().Do(ctx, "EVAL", flashSaleGateScript, 6,
		flashSaleNullKey(activityID),
		flashSaleActivityKey(activityID),
		flashSaleStockKey(activityID, skuID),
		flashSaleBoughtKey(activityID, skuID, userID),
		flashSaleIdemKey(userID, idempotencyKey),
		flashSaleSoldoutKey(activityID, skuID),
		time.Now().Unix(), hash, flashSaleGraceTTL,
	)
	if err != nil {
		return "", fmt.Errorf("执行秒杀闸门 Lua: %w", err)
	}
	return gateResult(v.String()), nil
}

// syncActivityCache 将指定活动同步到 Redis：enabled 且未结束 → 预热（活动元数据 + 剩余库存）；
// 下架/已结束/不存在 → 失效（清除活动与库存/售罄标记，并置空值标记）。
// 返回 error 表示 Redis 同步失败（MySQL 已提交的事实不受影响，由后台扫描器兜底）。
func (s *sFlashSale) syncActivityCache(ctx context.Context, activityID int64) error {
	a, err := s.findActivityCache(ctx, activityID)
	if err != nil {
		return err
	}
	if a == nil {
		return s.setNullMarker(ctx, activityID)
	}
	skus, err := s.findBindings(ctx, activityID)
	if err != nil {
		return err
	}
	// 在途预扣统计（status=queued 的请求数）：对账/预热须计入，避免把在途预扣错误回补导致超预扣。
	inflight, err := s.findInflightQueued(ctx, activityID)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	if a.Status != statusEnabled || a.EndTs <= now {
		return s.invalidateActivityCache(ctx, activityID, skus)
	}

	ttl := flashSaleActivityTTL(now, a.EndTs)
	if _, err := g.Redis().HSet(ctx, flashSaleActivityKey(activityID), map[string]any{
		flashSaleFieldStatus: strconv.Itoa(a.Status),
		flashSaleFieldStart:  strconv.FormatInt(a.StartTs, 10),
		flashSaleFieldEnd:    strconv.FormatInt(a.EndTs, 10),
	}); err != nil {
		return fmt.Errorf("预热活动元数据: %w", err)
	}
	if _, err := g.Redis().Expire(ctx, flashSaleActivityKey(activityID), ttl); err != nil {
		return fmt.Errorf("设置活动 TTL: %w", err)
	}
	for _, b := range skus {
		remaining := b.TotalStock - b.Sold - inflight[b.SkuId]
		if remaining < 0 {
			remaining = 0
		}
		if err := g.Redis().SetEX(ctx, flashSaleStockKey(activityID, b.SkuId), strconv.FormatInt(remaining, 10), ttl); err != nil {
			return fmt.Errorf("预热库存: %w", err)
		}
		// 排队软上限计数收敛为权威值（MySQL COUNT(status=queued)），幂等 SET + 活动 TTL。
		if err := s.convergeQueueCount(ctx, activityID, b.SkuId, inflight[b.SkuId], ttl); err != nil {
			return fmt.Errorf("收敛排队计数: %w", err)
		}
	}
	if _, err := g.Redis().Del(ctx, flashSaleNullKey(activityID)); err != nil {
		return fmt.Errorf("清除空值标记: %w", err)
	}
	for _, b := range skus {
		if _, err := g.Redis().Del(ctx, flashSaleSoldoutKey(activityID, b.SkuId)); err != nil {
			return fmt.Errorf("清除售罄标记: %w", err)
		}
	}
	return nil
}

// invalidateActivityCache 下架/结束活动时清除活动域缓存并置空值标记，使后续抢购快速失败（12001）。
func (s *sFlashSale) invalidateActivityCache(ctx context.Context, activityID int64, skus []*activitySkuRow) error {
	keys := []string{flashSaleActivityKey(activityID)}
	for _, b := range skus {
		keys = append(keys, flashSaleStockKey(activityID, b.SkuId), flashSaleSoldoutKey(activityID, b.SkuId))
	}
	if _, err := g.Redis().Del(ctx, keys...); err != nil {
		return fmt.Errorf("失效活动缓存: %w", err)
	}
	return s.setNullMarker(ctx, activityID)
}

// setNullMarker 写入空值/负缓存标记（短 TTL），使不存在/下架/未预热活动快速失败。
func (s *sFlashSale) setNullMarker(ctx context.Context, activityID int64) error {
	if err := g.Redis().SetEX(ctx, flashSaleNullKey(activityID), "1", flashSaleNullTTL); err != nil {
		return fmt.Errorf("写入空值标记: %w", err)
	}
	return nil
}

// authoritativeRemaining 计算指定活动×SKU 的权威剩余库存 = total_stock - sold - inflight_queued（≥0）。
// 绑定不存在返回 0（无可收敛）；DB 读取失败返回错误。
func (s *sFlashSale) authoritativeRemaining(ctx context.Context, activityID, skuID int64) (int64, error) {
	binding, err := s.findBinding(ctx, activityID, skuID)
	if err != nil {
		return 0, err
	}
	if binding == nil {
		return 0, nil
	}
	inflight, err := s.findInflightQueued(ctx, activityID)
	if err != nil {
		return 0, err
	}
	remaining := binding.TotalStock - binding.Sold - inflight[skuID]
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

// convergeStock 权威值收敛（V4）：将指定活动×SKU 的 Redis remaining 重算并幂等 SET 为
// total_stock - sold - inflight_queued，并清除售罄标记。取代 V3 非幂等 INCR 补偿：
// 重复执行/崩溃重放结果不变（幂等），满足「补偿不重不漏」；不依赖进程内补偿次数。
// Redis 失败仅记录日志（由对账扫描器 ≤ 一个周期兜底收敛，不破坏不变量）。
func (s *sFlashSale) convergeStock(ctx context.Context, activityID, skuID int64) {
	remaining, err := s.authoritativeRemaining(ctx, activityID, skuID)
	if err != nil {
		glog.Warningf(ctx, "计算秒杀权威剩余失败(activity=%d sku=%d): %v", activityID, skuID, err)
		return
	}
	// TTL 复用活动 TTL；活动已结束或读取失败时用 grace TTL 兜底（结束收敛会清理缓存）。
	ttl := int64(flashSaleGraceTTL)
	if a, e := s.findActivityCache(ctx, activityID); e == nil && a != nil {
		if t := flashSaleActivityTTL(time.Now().Unix(), a.EndTs); t > 0 {
			ttl = t
		}
	}
	if err := g.Redis().SetEX(ctx, flashSaleStockKey(activityID, skuID), strconv.FormatInt(remaining, 10), ttl); err != nil {
		glog.Warningf(ctx, "收敛秒杀库存失败(activity=%d sku=%d): %v", activityID, skuID, err)
		return
	}
	if _, err := g.Redis().Del(ctx, flashSaleSoldoutKey(activityID, skuID)); err != nil {
		glog.Warningf(ctx, "清除售罄标记失败(activity=%d sku=%d): %v", activityID, skuID, err)
	}
}

// clearMarkers 清除一人一单/幂等标记（幂等 DEL）。GATE_PASSED 时原子写入，失败/孤儿须清除避免残留。
func (s *sFlashSale) clearMarkers(ctx context.Context, activityID, skuID, userID int64, idempotencyKey string) {
	if _, err := g.Redis().Del(ctx, flashSaleBoughtKey(activityID, skuID, userID)); err != nil {
		glog.Warningf(ctx, "清除一人一单标记失败(activity=%d sku=%d user=%d): %v", activityID, skuID, userID, err)
	}
	if _, err := g.Redis().Del(ctx, flashSaleIdemKey(userID, idempotencyKey)); err != nil {
		glog.Warningf(ctx, "清除幂等标记失败(user=%d): %v", userID, err)
	}
}

// clearBoughtMarker 仅清除一人一单标记（幂等 DEL），供「已购标记孤儿自愈」复用。
func (s *sFlashSale) clearBoughtMarker(ctx context.Context, activityID, skuID, userID int64) {
	if _, err := g.Redis().Del(ctx, flashSaleBoughtKey(activityID, skuID, userID)); err != nil {
		glog.Warningf(ctx, "清除一人一单标记失败(activity=%d sku=%d user=%d): %v", activityID, skuID, userID, err)
	}
}

// convergeStockAndMarkers 在「入队失败」或「消费终态失败」时补偿 Redis：
// 权威值收敛 remaining（幂等 SET）+ 清除一人一单/幂等标记（幂等 DEL）。Redis 失败仅记录日志。
func (s *sFlashSale) convergeStockAndMarkers(ctx context.Context, activityID, skuID, userID int64, idempotencyKey string) {
	s.convergeStock(ctx, activityID, skuID)
	s.clearMarkers(ctx, activityID, skuID, userID, idempotencyKey)
}

// SyncCache 同步指定活动缓存（预热或失效），供管理端、后台扫描器与测试复用。
func (s *sFlashSale) SyncCache(ctx context.Context, activityID int64) error {
	return s.syncActivityCache(ctx, activityID)
}

// ReconcileCache 对账/回补（供后台扫描器复用）：
// 1) 扫描启用且未结束的活动，将 Redis remaining 刷成 total_stock - sold - inflight_queued、回补缺失预热；
// 2) 扫描「已结束、尚在 grace 窗口」的活动做终态收敛（remaining = total_stock - sold + 失效缓存）。
// 返回本次处理的活动数。
func (s *sFlashSale) ReconcileCache(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	count := 0

	// 1. 活跃活动：收敛 + 回补预热。
	var active []*activityRow
	if err := g.DB().Model("flash_sale_activities").Ctx(ctx).
		Fields("id").
		Where("status", statusEnabled).
		Where("end_time > NOW()").
		Order("id").
		Limit(limit).
		Scan(&active); err != nil {
		return 0, codes.Wrap(codes.CodeInternalError, fmt.Errorf("扫描秒杀活动: %w", err))
	}
	for _, r := range active {
		if err := s.syncActivityCache(ctx, r.Id); err != nil {
			glog.Warningf(ctx, "同步秒杀活动缓存失败(activity=%d): %v", r.Id, err)
			continue
		}
		count++
	}

	// 2. 已结束且在 grace 窗口内的活动：终态收敛（含活动结束后的缓存清理）。
	var ended []*activityRow
	if err := g.DB().Model("flash_sale_activities").Ctx(ctx).
		Fields("id").
		Where("status", statusEnabled).
		Where("end_time <= NOW()").
		Where("end_time > DATE_SUB(NOW(), INTERVAL ? SECOND)", flashSaleGraceTTL).
		Order("id").
		Limit(limit).
		Scan(&ended); err != nil {
		return count, codes.Wrap(codes.CodeInternalError, fmt.Errorf("扫描已结束秒杀活动: %w", err))
	}
	for _, r := range ended {
		if err := s.convergeEndedActivity(ctx, r.Id); err != nil {
			glog.Warningf(ctx, "结束秒杀活动终态收敛失败(activity=%d): %v", r.Id, err)
			continue
		}
		count++
	}
	return count, nil
}

// convergeEndedActivity 对「已结束、尚在 grace 窗口」的活动做终态收敛（幂等）：
// 将 remaining 刷成 total_stock - sold - inflight_queued（inflight 计入在途预扣，避免错误回补）；
// 当该活动在途请求归零（inflight=0）时，remaining 收敛为 total_stock - sold，并失效活动域缓存
// （DEL activity 元数据/售罄标记 + 置空值标记；stock key 保留 remaining 便于观测，靠 TTL 过期），
// 使活动结束状态可观测、无残留脏数据。幂等（重复执行结果不变）。
func (s *sFlashSale) convergeEndedActivity(ctx context.Context, activityID int64) error {
	skus, err := s.findBindings(ctx, activityID)
	if err != nil {
		return err
	}
	inflight, err := s.findInflightQueued(ctx, activityID)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	ttl := int64(flashSaleGraceTTL)
	if a, e := s.findActivityCache(ctx, activityID); e == nil && a != nil {
		if t := flashSaleActivityTTL(now, a.EndTs); t > 0 {
			ttl = t
		}
	}
	var totalInflight int64
	for _, b := range skus {
		remaining := b.TotalStock - b.Sold - inflight[b.SkuId]
		if remaining < 0 {
			remaining = 0
		}
		totalInflight += inflight[b.SkuId]
		if err := g.Redis().SetEX(ctx, flashSaleStockKey(activityID, b.SkuId), strconv.FormatInt(remaining, 10), ttl); err != nil {
			return fmt.Errorf("收敛结束活动库存: %w", err)
		}
		// 排队软上限计数收敛为权威值（MySQL COUNT(status=queued)），幂等 SET + 活动 TTL。
		if err := s.convergeQueueCount(ctx, activityID, b.SkuId, inflight[b.SkuId], ttl); err != nil {
			return fmt.Errorf("收敛结束活动排队计数: %w", err)
		}
	}
	if totalInflight > 0 {
		// 仍有在途请求：仅收敛 remaining，等消费完成后下一周期再失效缓存。
		return nil
	}
	// 在途归零：失效活动元数据与售罄标记（stock 保留 remaining 可观测），置空值标记使后续抢购快速失败。
	if _, err := g.Redis().Del(ctx, flashSaleActivityKey(activityID)); err != nil {
		return fmt.Errorf("失效结束活动元数据: %w", err)
	}
	for _, b := range skus {
		if _, err := g.Redis().Del(ctx, flashSaleSoldoutKey(activityID, b.SkuId)); err != nil {
			return fmt.Errorf("失效结束活动售罄标记: %w", err)
		}
		// 活动结束、在途归零：清理排队软上限计数 key（幂等 DEL）。
		if _, err := g.Redis().Del(ctx, flashSaleQueuedKey(activityID, b.SkuId)); err != nil {
			return fmt.Errorf("失效结束活动排队计数: %w", err)
		}
	}
	return s.setNullMarker(ctx, activityID)
}
