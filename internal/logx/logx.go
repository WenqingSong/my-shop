// Package logx 提供关键业务链路的结构化日志输出：统一补充 request_id 关联字段，
// 供登录、下单、秒杀、消费任务等复用。不引入 tracing、不改变业务流。
//
// 输出为单行 JSON，键名保持稳定英文（request_id / user_id / order_id / flashsale_id 等）；
// 值只允许非敏感标量，禁止 password / token / secret 等敏感信息进入日志。
package logx

import (
	"context"
	"encoding/json"

	"github.com/gogf/gf/v2/os/glog"

	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
)

// Fields 是结构化日志字段集合。键名保持稳定英文，值只允许非敏感标量。
type Fields map[string]any

// Info 输出 Info 级结构化业务日志（单行 JSON）。
func Info(ctx context.Context, event, message string, fields Fields) {
	emit(glog.Info, ctx, event, message, fields)
}

// Warning 输出 Warning 级结构化业务日志（单行 JSON）。
func Warning(ctx context.Context, event, message string, fields Fields) {
	emit(glog.Warning, ctx, event, message, fields)
}

// Error 输出 Error 级结构化业务日志（单行 JSON）。
func Error(ctx context.Context, event, message string, fields Fields) {
	emit(glog.Error, ctx, event, message, fields)
}

// emit 序列化一条结构化日志并经给定 logger 输出。序列化失败仅降级为不记录，不影响业务
// （字段均为简单标量，理论不会失败）。
func emit(logger func(ctx context.Context, v ...any), ctx context.Context, event, message string, fields Fields) {
	b, err := render(ctx, event, message, fields)
	if err != nil {
		return
	}
	logger(ctx, string(b))
}

// render 组装结构化日志字段并序列化：event、message、request_id（若上下文可取得）与调用方字段。
// request_id 取自已注入的中间件；后台任务上下文无该值时不输出该字段，避免空值噪声。
func render(ctx context.Context, event, message string, fields Fields) ([]byte, error) {
	m := make(map[string]any, len(fields)+3)
	m["event"] = event
	if message != "" {
		m["message"] = message
	}
	if rid := middleware.RequestIDFromContext(ctx); rid != "" {
		m["request_id"] = rid
	}
	for k, v := range fields {
		m[k] = v
	}
	return json.Marshal(m)
}
