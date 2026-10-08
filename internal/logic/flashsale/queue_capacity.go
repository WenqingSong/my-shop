// 秒杀 V5 容量保护——排队长度软上限（activity×SKU 维度）。
// 以 Redis 计数器 flashsale:queued:{activityID}:{skuID} 作为快速近似计数：入队前 GET 计数，
// 达到上限即快速拒绝（12010/429，无副作用——不预扣、不建单、不排队）；入队成功后 INCR、
// 消费终态（success/failed/dead）DECR；权威值由对账扫描器以 MySQL COUNT(status=queued) 收敛。
//
// 软上限语义：GET 计数为近似值，并发下允许短暂小幅超限，最终由权威 MySQL 计数收敛；
// 不超卖、不重复订单仍由 MySQL 唯一约束兜底（最终正确性边界不变）。Redis 故障 fail-open。
package flashsale

import (
	"context"
	"strconv"

	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// 排队计数 Redis key 前缀（B 类 namespace，复用 flashsale: 前缀）。
const flashSaleQueuedKeyPrefix = "flashsale:queued:"

func flashSaleQueuedKey(activityID, skuID int64) string {
	return flashSaleQueuedKeyPrefix + strconv.FormatInt(activityID, 10) + ":" + strconv.FormatInt(skuID, 10)
}

// queueCapacityEnabled 读取排队软上限开关（flash_sale.queue_capacity.enabled，默认关闭）。
func queueCapacityEnabled(ctx context.Context) bool {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.queue_capacity.enabled", false)
	if err != nil || v == nil {
		return false
	}
	return v.Bool()
}

// queueCapacityMax 读取排队软上限（单 activity×SKU 最大 queued 数，<=0 表示不限）。
func queueCapacityMax(ctx context.Context) int64 {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.queue_capacity.max_queued_per_sku", int64(0))
	if err != nil || v == nil {
		return 0
	}
	return v.Int64()
}

// queueCounterTTL 读取排队计数 key 的兜底 TTL（秒，<=0 回退默认 86400；对账扫描器会按活动 TTL 刷新）。
func queueCounterTTL(ctx context.Context) int64 {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.queue_capacity.counter_ttl_seconds", int64(86400))
	if err != nil || v == nil {
		return 86400
	}
	if n := v.Int64(); n > 0 {
		return n
	}
	return 86400
}

// checkQueueCapacity 入队前检查排队软上限：Redis 近似计数 >= max 时返回 12010。
// 开关关闭/max<=0/Redis 故障时放行（fail-open）。
func (s *sFlashSale) checkQueueCapacity(ctx context.Context, activityID, skuID int64) error {
	if !queueCapacityEnabled(ctx) {
		return nil
	}
	max := queueCapacityMax(ctx)
	if max <= 0 {
		return nil
	}
	n, err := s.queueCount(ctx, activityID, skuID)
	if err != nil {
		// Redis 计数失败 → fail-open（由对账扫描器权威收敛兜底）。
		return nil
	}
	if n >= max {
		return codes.New(codes.CodeFlashSaleQueueFull)
	}
	return nil
}

// queueCount 读取 Redis 排队近似计数；key 不存在返回 0。Redis 故障返回 error（调用方 fail-open）。
func (s *sFlashSale) queueCount(ctx context.Context, activityID, skuID int64) (int64, error) {
	v, err := g.Redis().Get(ctx, flashSaleQueuedKey(activityID, skuID))
	if err != nil {
		return 0, err
	}
	if v.IsNil() || v.IsEmpty() {
		return 0, nil
	}
	return v.Int64(), nil
}

// incrQueueCount 入队成功后近似计数 +1（best-effort：失败仅忽略，由对账扫描器收敛）。
func (s *sFlashSale) incrQueueCount(ctx context.Context, activityID, skuID int64) {
	if !queueCapacityEnabled(ctx) {
		return
	}
	if _, err := incrFixedWindow(ctx, flashSaleQueuedKey(activityID, skuID), queueCounterTTL(ctx)); err != nil {
		// 忽略：排队计数为近似值，对账扫描器以 MySQL COUNT 权威收敛。
	}
}

// decrQueueCount 消费终态（success/failed/dead）后近似计数 -1（best-effort）。
func (s *sFlashSale) decrQueueCount(ctx context.Context, activityID, skuID int64) {
	if !queueCapacityEnabled(ctx) {
		return
	}
	if _, err := g.Redis().Do(ctx, "DECR", flashSaleQueuedKey(activityID, skuID)); err != nil {
		// 忽略：排队计数为近似值，对账扫描器收敛。
	}
}

// convergeQueueCount 将排队计数收敛为权威值（MySQL COUNT(status=queued)），幂等 SET + 活动 TTL。
// 供对账扫描器复用（syncActivityCache / 结束收敛）。
func (s *sFlashSale) convergeQueueCount(ctx context.Context, activityID, skuID int64, count, ttl int64) error {
	if !queueCapacityEnabled(ctx) {
		return nil
	}
	if err := g.Redis().SetEX(ctx, flashSaleQueuedKey(activityID, skuID), strconv.FormatInt(count, 10), ttl); err != nil {
		return err
	}
	return nil
}
