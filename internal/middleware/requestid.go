package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/glog"
	"github.com/gogf/gf/v2/util/guid"
)

// requestIDHeader 是请求/响应中携带 Request ID 的头名。
const requestIDHeader = "X-Request-ID"

type requestIDContextKey struct{}

var requestIDKey = requestIDContextKey{}

// RequestID 读取并回写 Request ID、注入请求上下文，并在请求结束时输出一条结构化访问日志。
//
// 取值链路：Nginx 统一生成并覆盖客户端 X-Request-ID（值为 $request_id）透传给本服务；
// 直连 / 健康检查路径缺失该头时由本中间件自生成，保证两条路径都有可关联的 request_id。
// 回写响应头使客户端与上游都能拿到同一 ID，用于与 Nginx 访问日志关联。
//
// 注册顺序约束：本中间件必须注册于 middleware.Response 之前（外层），
// 以便在 Response 写完状态码/响应体后再读取最终 status 记录访问日志。
func RequestID(r *ghttp.Request) {
	rid := r.Header.Get(requestIDHeader)
	if rid == "" {
		rid = guid.S()
	}
	r.SetCtxVar(requestIDKey, rid)
	r.Response.Header().Set(requestIDHeader, rid)

	start := time.Now()
	r.Middleware.Next()

	status := r.Response.Status
	if status == 0 {
		// 成功路径 Response 未显式 WriteHeader，默认按 200 记录。
		status = http.StatusOK
	}
	logAccess(r.Context(), accessLog{
		RequestID: rid,
		Method:    r.Method,
		Path:      r.URL.Path,
		Status:    status,
		Duration:  time.Since(start).Milliseconds(),
		ClientIP:  r.GetClientIp(),
	})
}

// RequestIDFromContext 从请求上下文读取 RequestID 中间件注入的 Request ID。
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// accessLog 是访问日志的结构化字段（字段名英文，与 Nginx JSON 访问日志对应）。
// 刻意不含 Authorization / Cookie / 请求体等敏感信息。
type accessLog struct {
	RequestID string `json:"request_id"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Status    int    `json:"status"`
	// Duration 为本次请求处理耗时，单位毫秒。
	Duration int64  `json:"duration"`
	ClientIP string `json:"client_ip"`
}

// logAccess 输出一条结构化访问日志（JSON）。
// 序列化失败仅降级为不记录访问日志，不影响请求处理（字段均为简单类型，理论不会失败）。
func logAccess(ctx context.Context, l accessLog) {
	b, err := json.Marshal(l)
	if err != nil {
		return
	}
	glog.Info(ctx, string(b))
}
