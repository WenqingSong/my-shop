package middleware

import (
	"net/http"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"

	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
)

// metricsPath 是 Prometheus 抓取端点路径。抓取请求本身不计入业务 HTTP 指标：
// 否则每次 scrape 都会产生一条「/metrics 被谁抓取」的观测，既无意义又会让
// 请求量随抓取频率漂移（抓取端自己观测自己，形成反馈噪声）。
const metricsPath = "/metrics"

// Metrics 采集统一的 HTTP 指标：在途请求数、请求总数、处理耗时与最终状态码。
//
// 注册顺序（重要）：必须位于 middleware.Response 之前、middleware.RequestID 之后：
//
//	root.Middleware(middleware.RequestID)   // 最外层：注入/回写 Request ID 并输出访问日志
//	root.Middleware(middleware.Metrics)     // 本中间件：读取 Response 写完之后的最终状态码
//	root.Middleware(middleware.Response)    // 内层：把 handler 结果包装成统一 JSON 与错误状态码
//
// 原因：状态码由 Response 在 Next() 返回后写入（例如业务错误 400/429/503）。
// 若本中间件位于 Response 内层，读到的会是「进入中间件时的初始状态」，
// 所有 4xx/5xx 都会被记成 200，指标失去诊断价值。
//
// 语义边界：
//   - 只记录最终业务响应状态（含 4xx/5xx），不改变任何响应内容；
//   - In-flight 用 defer 保证恢复，panic 展开时同样 -1，不会永久泄漏；
//   - route 标签只取 GoFrame 已匹配的路由模板（r.Router.Uri），未命中固定为 unmatched；
//     绝不使用原始 URL，避免动态路径/扫描路径制造高基数 Time Series；
//   - 「是否启用」只在请求入口判断一次，保证 Inc/Dec 成对（见 metrics.HTTPRequestStarted 注释）。
func Metrics(r *ghttp.Request) {
	if !metrics.Enabled(r.Context()) || isMetricsScrape(r) {
		r.Middleware.Next()
		return
	}

	start := time.Now()
	metrics.HTTPRequestStarted()
	defer func() {
		metrics.HTTPRequestFinished(r.Method, routeTemplate(r), responseStatus(r), time.Since(start))
	}()

	r.Middleware.Next()
}

// isMetricsScrape 判断当前请求是否为 /metrics 抓取。
// 只在业务中间件意外覆盖到该路径时才起作用（当前 /metrics 由 Server.BindHandler
// 注册在根分组之外，本就不经过分组中间件，这里是显式防御）。
func isMetricsScrape(r *ghttp.Request) bool {
	return r.URL != nil && r.URL.Path == metricsPath
}

// routeTemplate 返回用于 route 标签的路由模板（如 /products/:id）。
//
// GoFrame 在路由匹配完成、进入中间件之前就已确定 r.Router，因此这里读到的是
// 注册时的路径模板而不是实际请求路径；未匹配到路由（404、静态资源等）时为 nil，
// 统一归一化为 metrics.UnmatchedRoute，保证标签基数不随请求路径增长。
func routeTemplate(r *ghttp.Request) string {
	if r.Router != nil && r.Router.Uri != "" {
		return r.Router.Uri
	}
	return metrics.UnmatchedRoute
}

// responseStatus 返回本次请求最终写出的 HTTP 状态码。
// Response 中间件成功路径不显式 WriteHeader，此时 Status 为 0，按 200 记录。
func responseStatus(r *ghttp.Request) int {
	status := r.Response.Status
	if status == 0 {
		return http.StatusOK
	}
	return status
}
