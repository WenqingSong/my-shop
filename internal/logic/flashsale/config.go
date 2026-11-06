package flashsale

import (
	"context"

	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
)

// 本文件是秒杀容量保护（限流 / 排队软上限 / 熔断器）读取配置的统一入口。
//
// 这些开关在下单请求链路上按请求读取，签名无法携带 error（熔断器状态机、
// best-effort 计数器等）。因此这里的策略是：
//
//   - 合法配置：严格按 config 层的「环境变量 > YAML > 代码默认值」取值；
//   - 非法配置：记录 Error 级结构化日志（不静默），并退回该配置项的代码默认值。
//
// 该兜底不是首选防线：serve 启动时 boot.ValidateConfig 会调用 config.Validate
// 逐项解析全部登记配置，非法值在建立连接之前就让进程启动失败。此处只保证
// 「配置热更新/异常场景下不把单个开关的读取故障放大成下单 500」。

// flashSaleBool 读取秒杀布尔开关；非法值记录错误并按 fallback 处理。
func flashSaleBool(ctx context.Context, key config.Key, fallback bool) bool {
	value, err := config.Bool(ctx, key)
	if err != nil {
		logFlashSaleConfigError(ctx, key, err)
		return fallback
	}
	return value
}

// flashSaleInt 读取秒杀 int 配置；非法值记录错误并按 fallback 处理。
func flashSaleInt(ctx context.Context, key config.Key, fallback int) int {
	value, err := config.Int(ctx, key)
	if err != nil {
		logFlashSaleConfigError(ctx, key, err)
		return fallback
	}
	return value
}

// flashSaleInt64 读取秒杀 int64 配置；非法值记录错误并按 fallback 处理。
func flashSaleInt64(ctx context.Context, key config.Key, fallback int64) int64 {
	value, err := config.Int64(ctx, key)
	if err != nil {
		logFlashSaleConfigError(ctx, key, err)
		return fallback
	}
	return value
}

// logFlashSaleConfigError 输出配置读取失败的结构化错误日志（只含键名与原因，不含取值）。
func logFlashSaleConfigError(ctx context.Context, key config.Key, err error) {
	logx.Error(ctx, "flashsale_config_read_failed", "读取秒杀配置失败，按代码默认值处理", logx.Fields{
		"config_key": string(key),
		"error":      err.Error(),
	})
}
