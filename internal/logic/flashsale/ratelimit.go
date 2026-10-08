// 秒杀 V5 容量保护——用户级/活动级限流（Redis 固定窗口计数器）。
// 采用 Redis 固定窗口：单次 Lua 原子完成 INCR + 首增 EXPIRE，多实例经 Redis 天然全局一致。
// 检查位于 CreateOrder 入口、runGate 之前，超限请求快速拒绝（12009/429），无副作用
// （不进入闸门、不预扣库存、不写 flash_sale_order_requests）；Redis 计数失败 fail-open
// （放行到闸门，由闸门 fail-closed 兜底，避免双失败路径）。固定窗口边界毛刺对容量保护可接受。
package flashsale

import (
	"context"
	"strconv"

	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// 限流 Redis key 前缀（B 类 namespace，复用 flashsale: 前缀）。
const (
	flashSaleRLUserKeyPrefix     = "flashsale:rl:user:"
	flashSaleRLActivityKeyPrefix = "flashsale:rl:activity:"
)

// flashSaleRateLimitScript 是固定窗口计数 Lua：原子 INCR，首增时设置窗口 TTL，返回计数。
// KEYS[1]=计数 key，ARGV[1]=窗口秒数。
const flashSaleRateLimitScript = `
local n = redis.call('INCR', KEYS[1])
if n == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return n
`

func flashSaleRLUserKey(userID int64) string {
	return flashSaleRLUserKeyPrefix + strconv.FormatInt(userID, 10)
}

func flashSaleRLActivityKey(activityID int64) string {
	return flashSaleRLActivityKeyPrefix + strconv.FormatInt(activityID, 10)
}

// rateLimitEnabled 读取限流开关（flash_sale.rate_limit.enabled，默认关闭）。
func rateLimitEnabled(ctx context.Context) bool {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.rate_limit.enabled", false)
	if err != nil || v == nil {
		return false
	}
	return v.Bool()
}

// userRateLimitMax 读取用户级限流阈值（单位窗口内最大请求数，<=0 表示不限）。
func userRateLimitMax(ctx context.Context) int64 {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.rate_limit.user.max_requests", int64(0))
	if err != nil || v == nil {
		return 0
	}
	return v.Int64()
}

// userRateLimitWindow 读取用户级限流窗口秒数（<=0 回退默认 1 秒）。
func userRateLimitWindow(ctx context.Context) int64 {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.rate_limit.user.window_seconds", int64(1))
	if err != nil || v == nil {
		return 1
	}
	if n := v.Int64(); n > 0 {
		return n
	}
	return 1
}

// activityRateLimitMax 读取活动级限流阈值（单位窗口内最大请求数，<=0 表示不限）。
func activityRateLimitMax(ctx context.Context) int64 {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.rate_limit.activity.max_requests", int64(0))
	if err != nil || v == nil {
		return 0
	}
	return v.Int64()
}

// activityRateLimitWindow 读取活动级限流窗口秒数（<=0 回退默认 1 秒）。
func activityRateLimitWindow(ctx context.Context) int64 {
	v, err := g.Cfg().GetEffective(ctx, "flash_sale.rate_limit.activity.window_seconds", int64(1))
	if err != nil || v == nil {
		return 1
	}
	if n := v.Int64(); n > 0 {
		return n
	}
	return 1
}

// incrFixedWindow 对指定 key 执行固定窗口计数（INCR + 首增 EXPIRE），返回计数。
// Redis 不可用返回 error（调用方按 fail-open 处理）。
func incrFixedWindow(ctx context.Context, key string, window int64) (int64, error) {
	v, err := g.Redis().Do(ctx, "EVAL", flashSaleRateLimitScript, 1, key, window)
	if err != nil {
		return 0, err
	}
	return v.Int64(), nil
}

// rateLimitUser 用户级限流：超限返回 12009，否则 nil。限流关闭/阈值 <=0/Redis 故障时放行（fail-open）。
func (s *sFlashSale) rateLimitUser(ctx context.Context, userID int64) error {
	if !rateLimitEnabled(ctx) {
		return nil
	}
	max := userRateLimitMax(ctx)
	if max <= 0 {
		return nil
	}
	n, err := incrFixedWindow(ctx, flashSaleRLUserKey(userID), userRateLimitWindow(ctx))
	if err != nil {
		// Redis 计数失败 → fail-open（放行到闸门，闸门 fail-closed 兜底）。
		return nil
	}
	if n > max {
		return codes.New(codes.CodeFlashSaleRateLimited)
	}
	return nil
}

// rateLimitActivity 活动级限流：超限返回 12009，否则 nil。限流关闭/阈值 <=0/Redis 故障时放行（fail-open）。
func (s *sFlashSale) rateLimitActivity(ctx context.Context, activityID int64) error {
	if !rateLimitEnabled(ctx) {
		return nil
	}
	max := activityRateLimitMax(ctx)
	if max <= 0 {
		return nil
	}
	n, err := incrFixedWindow(ctx, flashSaleRLActivityKey(activityID), activityRateLimitWindow(ctx))
	if err != nil {
		// Redis 计数失败 → fail-open。
		return nil
	}
	if n > max {
		return codes.New(codes.CodeFlashSaleRateLimited)
	}
	return nil
}
