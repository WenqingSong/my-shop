package cmd

// T4-02：/metrics 路由与业务中间件的边界测试。
//
// 复用生产入口 registerMetricsRoute（serve 实际调用的函数），断言：
//   - /metrics 返回 Prometheus 文本格式，并包含默认 Registry 自带的 Go Runtime 指标
//     （不重复实现，直接复用 prometheus client_golang 默认 Collector）；
//   - /metrics 注册在路由分组之外，因此不会被 HTTP 业务指标自身统计；
//   - 业务分组里的普通请求仍然被统计（证明中间件确实生效，排除「因为没挂中间件所以没统计」）。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
	"github.com/prometheus/client_golang/prometheus"

	"cnb.cool/go-cloud-devops/my-shop/internal/metrics"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
)

// findSeries 在默认 Registry 中按标签精确匹配一条样本值。
func findSeries(t *testing.T, name string, want map[string]string) (float64, bool) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
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
	}
	return 0, false
}

// startedCmdServer 按 serve 的方式装配：Server 级 /metrics + 根分组业务中间件。
func startedCmdServer(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	s := g.Server(guid.S())
	s.SetAddr(":0")
	// 与 serve 一致：/metrics 在分组之外注册（不经过认证/响应包装/访问日志/HTTP 指标）。
	registerMetricsRoute(ctx, s)
	s.Group("/", func(root *ghttp.RouterGroup) {
		root.Middleware(middleware.RequestID)
		root.Middleware(middleware.Metrics)
		root.Middleware(middleware.Response)
		root.GET("/ok", func(r *ghttp.Request) {
			r.Response.WriteJson(g.Map{"ok": true})
		})
	})
	s.SetDumpRouterMap(false)
	s.Start()
	t.Cleanup(func() { _ = s.Shutdown() })
	time.Sleep(100 * time.Millisecond)
	return fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
}

// getBody 发起 GET 并返回状态码与响应体。
func getBody(t *testing.T, url string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 %s 响应: %v", url, err)
	}
	return resp.StatusCode, string(body)
}

// TestMetricsRouteServesRuntimeMetricsAndSkipsSelfObservation 覆盖 T4-02-12 / T4-02-09：
//   - /metrics 可用且包含 Go Runtime 指标（复用默认 Collector）；
//   - 抓取 /metrics 不产生 HTTP 业务指标序列；
//   - 同一次运行中普通路由请求确实被统计。
func TestMetricsRouteServesRuntimeMetricsAndSkipsSelfObservation(t *testing.T) {
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "true")
	base := startedCmdServer(t)

	status, body := getBody(t, base+"/metrics")
	if status != http.StatusOK {
		t.Fatalf("/metrics 状态码=%d want 200", status)
	}
	for _, want := range []string{
		"# HELP go_goroutines ",
		"# HELP go_memstats_heap_alloc_bytes ",
		"# HELP process_cpu_seconds_total ",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/metrics 缺少默认 Go Runtime/Process 指标：%q", want)
		}
	}

	// 业务路由请求必须被统计（证明中间件生效）。
	before, _ := findSeries(t, "surgecart_http_requests_total", map[string]string{
		"method": "GET", "route": "/ok", "status": "200",
	})
	if status, _ := getBody(t, base+"/ok"); status != http.StatusOK {
		t.Fatalf("/ok 状态码=%d want 200", status)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		after, ok := findSeries(t, "surgecart_http_requests_total", map[string]string{
			"method": "GET", "route": "/ok", "status": "200",
		})
		if ok && after >= before+1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("业务请求未被 HTTP 指标统计（before=%v after=%v ok=%v）", before, after, ok)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 抓取端点不得被自身统计。
	if v, ok := findSeries(t, "surgecart_http_requests_total", map[string]string{
		"route": metrics.UnmatchedRoute,
	}); ok && v > 0 {
		t.Logf("存在 unmatched 序列（值=%v），但 /metrics 不应出现在 route 标签中", v)
	}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "surgecart_http_requests_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "route" && label.GetValue() == "/metrics" {
					t.Fatal("/metrics 抓取被自身统计（必须排除抓取端点）")
				}
			}
		}
	}
}
