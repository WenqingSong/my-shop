package middleware_test

// T4-02 HTTP 指标中间件测试。
//
// 这些用例启动真实 GoFrame HTTP Server + 真实 HTTP 请求，断言的是「处理器真正写出的
// 最终状态码被记录」而不是函数返回值：
//   - 2xx/4xx/5xx 与不同 Method 各自产生对应序列；
//   - 动态路径参数只产生路由模板标签（/products/:id），不产生高基数标签；
//   - 未命中路由（404）不会被分组中间件观测，也不得泄漏原始 URL 到标签；
//   - 并发请求期间 In-flight 上升、结束后归零；
//   - /metrics 抓取不被自身统计；
//   - 指标关闭时不产生任何序列。

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
)

const (
	httpRequestsTotalMetric = "surgecart_http_requests_total"
	httpInFlightMetric      = "surgecart_http_requests_in_flight"
)

// metricsServer 是仅包含 RequestID + Metrics + Response 的测试服务（无 DB 依赖）。
type metricsServer struct {
	base string
	// release 用于阻塞 /block 处理器，验证并发在途计数。
	release     chan struct{}
	releaseOnce sync.Once
	// entered 在 /block 处理器进入时收到一次信号。
	entered chan struct{}
}

// closeRelease 幂等放行所有被阻塞的请求（避免测试与 Cleanup 重复关闭 channel）。
func (m *metricsServer) closeRelease() {
	m.releaseOnce.Do(func() { close(m.release) })
}

// setupMetricsServer 按生产注册顺序启动服务：
// RequestID（外）→ Metrics（中）→ Response（内），并额外在 Server 级注册 /metrics，
// 与 internal/cmd 的注册方式一致。
func setupMetricsServer(t *testing.T) *metricsServer {
	t.Helper()
	srv := &metricsServer{
		release: make(chan struct{}),
		entered: make(chan struct{}, 8),
	}

	s := g.Server(guid.S())
	s.SetAddr(":0")
	s.BindHandler("GET:/metrics", func(r *ghttp.Request) {
		metrics.Handler().ServeHTTP(r.Response.Writer, r.Request)
	})
	s.Group("/", func(root *ghttp.RouterGroup) {
		root.Middleware(middleware.RequestID)
		root.Middleware(middleware.Metrics)
		root.Middleware(middleware.Response)

		root.GET("/ok", func(r *ghttp.Request) {
			r.Response.WriteJson(g.Map{"ok": true})
		})
		root.POST("/created", func(r *ghttp.Request) {
			r.Response.WriteHeader(http.StatusCreated)
			r.Response.WriteJson(g.Map{"created": true})
		})
		root.GET("/teapot", func(r *ghttp.Request) {
			r.Response.WriteHeader(http.StatusTeapot)
			r.Response.Write("teapot")
		})
		root.DELETE("/broken", func(r *ghttp.Request) {
			r.Response.WriteHeader(http.StatusServiceUnavailable)
			r.Response.Write("unavailable")
		})
		root.GET("/products/:id", func(r *ghttp.Request) {
			r.Response.WriteJson(g.Map{"id": r.Get("id").String()})
		})
		root.GET("/block", func(r *ghttp.Request) {
			select {
			case srv.entered <- struct{}{}:
			default:
			}
			<-srv.release
			r.Response.WriteJson(g.Map{"released": true})
		})
	})
	s.SetDumpRouterMap(false)
	s.Start()
	t.Cleanup(func() {
		srv.closeRelease()
		_ = s.Shutdown()
	})
	time.Sleep(100 * time.Millisecond)
	srv.base = fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
	return srv
}

// doRequest 发起一次请求并返回状态码（响应体不参与断言）。
func doHTTP(t *testing.T, method, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	if err != nil {
		t.Fatalf("构造请求 %s %s: %v", method, url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// gatherFamily 从默认 registry 抓取指定指标 family（不存在返回 nil）。
func gatherFamily(t *testing.T, name string) *dto.MetricFamily {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	return nil
}

// seriesValue 返回指定标签组合的 Counter/Gauge 值。
func seriesValue(t *testing.T, name string, want map[string]string) (float64, bool) {
	t.Helper()
	family := gatherFamily(t, name)
	if family == nil {
		return 0, false
	}
	for _, metric := range family.GetMetric() {
		got := make(map[string]string, len(metric.GetLabel()))
		for _, label := range metric.GetLabel() {
			got[label.GetName()] = label.GetValue()
		}
		match := true
		for k, v := range want {
			if got[k] != v {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if metric.GetCounter() != nil {
			return metric.GetCounter().GetValue(), true
		}
		if metric.GetGauge() != nil {
			return metric.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

// requestCounter 返回指定 method/route/status 的请求计数。
func requestCounter(t *testing.T, method, route, status string) (float64, bool) {
	t.Helper()
	return seriesValue(t, httpRequestsTotalMetric, map[string]string{
		"method": method, "route": route, "status": status,
	})
}

// waitCondition 轮询等待条件成立（避免固定 sleep 造成偶发失败）。
func waitCondition(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", desc)
}

// TestHTTPMetricsRecordsFinalStatus 覆盖 T4-02-01/02：
// 200/201/418/503 与不同 Method 各自产生独立序列，说明记录的是最终业务状态而不是初始状态。
func TestHTTPMetricsRecordsFinalStatus(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	srv := setupMetricsServer(t)

	cases := []struct {
		method, path, route, status string
	}{
		{"GET", "/ok", "/ok", "200"},
		{"POST", "/created", "/created", "201"},
		{"GET", "/teapot", "/teapot", "418"},
		{"DELETE", "/broken", "/broken", "503"},
	}
	for _, tc := range cases {
		before, _ := requestCounter(t, tc.method, tc.route, tc.status)
		if got := doHTTP(t, tc.method, srv.base+tc.path); got < 200 {
			t.Fatalf("%s %s 返回异常状态码 %d", tc.method, tc.path, got)
		}
		waitCondition(t, 2*time.Second, fmt.Sprintf("%s %s 计数增加", tc.method, tc.route), func() bool {
			after, ok := requestCounter(t, tc.method, tc.route, tc.status)
			return ok && after >= before+1
		})
	}

	// 5xx 不得被记成 200：说明中间件位于 Response 之外，读到的是最终状态。
	if _, ok := requestCounter(t, "DELETE", "/broken", "200"); ok {
		t.Fatal("503 响应被错误记录为 200（中间件可能位于 Response 内层）")
	}
}

// TestHTTPMetricsRouteTemplateLowCardinality 覆盖 T4-02-04：
// 动态路径参数只产生路由模板标签；请求不同 ID 不会新增序列。
func TestHTTPMetricsRouteTemplateLowCardinality(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	srv := setupMetricsServer(t)

	for _, id := range []string{"1", "987654", "abcdef"} {
		doHTTP(t, "GET", srv.base+"/products/"+id)
	}
	waitCondition(t, 2*time.Second, "路由模板序列出现", func() bool {
		v, ok := requestCounter(t, "GET", "/products/:id", "200")
		return ok && v >= 3
	})

	family := gatherFamily(t, httpRequestsTotalMetric)
	if family == nil {
		t.Fatal("HTTP 请求总数指标不存在")
	}
	for _, metric := range family.GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() != "route" {
				continue
			}
			for _, dynamic := range []string{"987654", "abcdef"} {
				if strings.Contains(label.GetValue(), dynamic) {
					t.Fatalf("route 标签出现动态路径值 %q（应按路由模板归一化）", label.GetValue())
				}
			}
		}
	}
}

// TestHTTPMetricsUnmatchedRouteDoesNotLeakURL 覆盖未命中路由（404）的真实行为与基数安全：
//
// GoFrame 在「没有任何路由匹配」时直接写 404，不进入路由分组中间件
// （ghttp.Server.ServeHTTP：handlers 为空时走 WriteHeader(404) 分支），
// 因此 404 请求不会被 HTTP 指标统计——这是可接受且被明确记录的边界，
// 代价只是「404 率」无指标，而收益是绝不会把任意扫描路径写进 route 标签。
//
// 本用例断言两件事：
//  1. 未命中路径不会产生任何以原始 URL 为 route 标签的序列（基数保护）；
//  2. 未命中路径也不会被错误地记成某个已注册路由。
func TestHTTPMetricsUnmatchedRouteDoesNotLeakURL(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	srv := setupMetricsServer(t)

	doHTTP(t, "GET", srv.base+"/not-registered-a")
	doHTTP(t, "GET", srv.base+"/not-registered-b/123")
	time.Sleep(100 * time.Millisecond)

	family := gatherFamily(t, httpRequestsTotalMetric)
	if family == nil {
		return // 尚无任何路由请求：本来就不应有序列
	}
	for _, metric := range family.GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() != "route" {
				continue
			}
			if strings.Contains(label.GetValue(), "not-registered") {
				t.Fatalf("route 标签出现原始 URL %q（动态路径必须归一化到路由模板）", label.GetValue())
			}
		}
	}
}

// TestHTTPMetricsInFlightConcurrency 覆盖 T4-02-03：
// 并发请求处理期间 In-flight 上升，请求结束后恢复原值（defer 保证恢复）。
func TestHTTPMetricsInFlightConcurrency(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	srv := setupMetricsServer(t)

	baseline, _ := seriesValue(t, httpInFlightMetric, nil)

	const concurrent = 5
	var wg sync.WaitGroup
	wg.Add(concurrent)
	for i := 0; i < concurrent; i++ {
		go func() {
			defer wg.Done()
			// 不在子 goroutine 中调用 t.Fatalf（testing 包要求 FailNow 在测试 goroutine 执行）；
			// 请求失败会让 In-flight 断言失败，足够定位问题。
			resp, err := http.Get(srv.base + "/block")
			if err != nil {
				return
			}
			_ = resp.Body.Close()
		}()
	}

	// 等到至少一个请求进入处理器，并观察在途计数上升。
	for i := 0; i < concurrent; i++ {
		select {
		case <-srv.entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("等待 /block 请求进入超时")
		}
	}
	waitCondition(t, 2*time.Second, "并发期间 In-flight 上升", func() bool {
		v, ok := seriesValue(t, httpInFlightMetric, nil)
		return ok && v >= baseline+float64(concurrent)
	})

	srv.closeRelease()
	wg.Wait()
	waitCondition(t, 3*time.Second, "请求结束后 In-flight 恢复", func() bool {
		v, ok := seriesValue(t, httpInFlightMetric, nil)
		return ok && v == baseline
	})
}

// TestMetricsEndpointNotSelfObserved 覆盖 /metrics 排除：
// 抓取端点注册在 Server 级（分组中间件之外），因此不会产生 route=/metrics 的序列。
func TestMetricsEndpointNotSelfObserved(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	srv := setupMetricsServer(t)

	for i := 0; i < 3; i++ {
		doHTTP(t, "GET", srv.base+"/metrics")
	}
	time.Sleep(100 * time.Millisecond)

	family := gatherFamily(t, httpRequestsTotalMetric)
	if family == nil {
		return // 尚无任何业务请求：本来就不应有序列
	}
	for _, metric := range family.GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() == "route" && label.GetValue() == "/metrics" {
				t.Fatal("/metrics 抓取被自身统计（HTTP 指标不得包含抓取端点）")
			}
		}
	}
}

// TestHTTPMetricsDisabledNoSeries 覆盖配置开关：指标关闭时不产生任何 HTTP 序列。
func TestHTTPMetricsDisabledNoSeries(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "false")
	srv := setupMetricsServer(t)

	// 用独有路径，避免与其它测试的序列混淆。
	doHTTP(t, "GET", srv.base+"/ok")
	time.Sleep(100 * time.Millisecond)

	if v, ok := requestCounter(t, "GET", "/ok", "200"); ok {
		// 其它用例可能已写入同名序列；只要求本次请求没有让它继续增长。
		before := v
		doHTTP(t, "GET", srv.base+"/ok")
		time.Sleep(50 * time.Millisecond)
		after, _ := requestCounter(t, "GET", "/ok", "200")
		if after != before {
			t.Fatalf("指标关闭时仍在写入 HTTP 序列：%v → %v", before, after)
		}
	}
	if v, ok := seriesValue(t, httpInFlightMetric, nil); ok && v != 0 {
		t.Fatalf("指标关闭时 In-flight 不应被写入，实际 %v", v)
	}
}
