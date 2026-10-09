package flashsale

// orderOutcome 结果映射白盒测试（INV-M02「result 映射正确」，纯函数、无 MySQL/Redis 依赖）：
// 每种下单结果必须映射到稳定的指标结果枚举，且与 Contract 的 order 结果集合
// {queued, rate_limited, queue_full, gate_rejected, error} 一致。
// 若映射被破坏（如拒绝码误归为 error、成功误归为 gate_rejected），本测试失败。

import (
	"testing"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/flashsale/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
)

func TestOrderOutcomeMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"成功入队", nil, metrics.ResultQueued},
		{"用户级限流", codes.New(codes.CodeFlashSaleRateLimited), metrics.ResultRateLimited},
		{"活动级限流", codes.New(codes.CodeFlashSaleRateLimited), metrics.ResultRateLimited},
		{"排队满", codes.New(codes.CodeFlashSaleQueueFull), metrics.ResultQueueFull},
		{"活动不存在", codes.New(codes.CodeFlashSaleActivityNotFound), metrics.ResultGateRejected},
		{"不在时间窗", codes.New(codes.CodeFlashSaleNotInTimeWindow), metrics.ResultGateRejected},
		{"库存不足", codes.New(codes.CodeFlashSaleStockInsufficient), metrics.ResultGateRejected},
		{"一人一单", codes.New(codes.CodeFlashSaleAlreadyPurchased), metrics.ResultGateRejected},
		{"幂等冲突", codes.New(codes.CodeFlashSaleIdempotencyConflict), metrics.ResultGateRejected},
		{"SKU 不可用", codes.New(codes.CodeFlashSaleSkuUnavailable), metrics.ResultGateRejected},
		{"秒杀参数非法", codes.New(codes.CodeFlashSaleInvalidArgument), metrics.ResultGateRejected},
		{"通用参数非法", codes.New(codes.CodeInvalidArgument), metrics.ResultGateRejected},
		{"技术错误", codes.New(codes.CodeInternalError), metrics.ResultError},
		{"服务不可用(熔断)", codes.New(codes.CodeServiceUnavailable), metrics.ResultError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var res *v1.CreateOrderRes
			if c.err == nil {
				res = &v1.CreateOrderRes{Status: v1.RequestStatusQueued}
			}
			if got := orderOutcome(res, c.err); got != c.want {
				t.Fatalf("orderOutcome(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}
