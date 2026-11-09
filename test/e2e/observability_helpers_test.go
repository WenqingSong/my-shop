package e2e

// T2-02：可观测性与健康检查的 HTTP 验证辅助。
//
// 这里的所有断言都走真实 HTTP（GET /metrics、GET /health、GET /ready），
// 不直接读取进程内 registry 或 service 返回值：
//   - 指标验收要求「至少一次真实 HTTP 抓取」；
//   - 健康检查验收要求「HTTP 状态码」而不是函数返回值。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// T2-02 新增指标名（无标签 Gauge）。
const (
	metricConsumerJoined       = "flashsale_consumer_joined"
	metricOutboxBacklogPending = "flashsale_outbox_backlog_pending"
)

// scrapeMetrics 抓取真实 /metrics 端点并返回文本；非 200 视为失败（未启用指标时端点不存在）。
func scrapeMetrics(t *testing.T, base string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/metrics", nil)
	if err != nil {
		t.Fatalf("构造 /metrics 请求: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("抓取 /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 /metrics 响应: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics 状态码=%d want 200（body 前 200 字节: %.200s）", resp.StatusCode, string(body))
	}
	return string(body)
}

// metricValue 在 Prometheus 文本输出中匹配「无标签」指标的样本值。
// 只做精确名匹配（name + 空格），不会误命中 *_total / *_bucket 等其它序列。
func metricValue(body, name string) (float64, bool) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, name+" ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		return v, true
	}
	return 0, false
}

// metricHelp 返回指标 HELP 行（用于断言说明存在且非空）。
func metricHelp(body, name string) (string, bool) {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# HELP "+name+" ") {
			return strings.TrimPrefix(line, "# HELP "+name+" "), true
		}
	}
	return "", false
}

// metricType 返回指标 TYPE 行（gauge/counter/histogram）。
func metricType(body, name string) (string, bool) {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# TYPE "+name+" ") {
			return strings.TrimPrefix(line, "# TYPE "+name+" "), true
		}
	}
	return "", false
}

// waitMetricValue 轮询 /metrics，直到无标签指标取到期望值或超时。
func waitMetricValue(t *testing.T, base, name string, want float64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		body := scrapeMetrics(t, base)
		if v, ok := metricValue(body, name); ok {
			last = fmt.Sprintf("%v", v)
			if v == want {
				return
			}
		} else {
			last = "序列不存在"
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("指标 %s 未在 %s 内变为 %v（当前 %s）", name, timeout, want, last)
}

// waitHTTPStatus 轮询指定路径，直到返回期望状态码或超时。
func waitHTTPStatus(t *testing.T, base, path string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		res, err := isoRequest(base, "GET", path, nil, nil)
		if err == nil {
			last = fmt.Sprintf("status=%d code=%d", res.Status, res.Code)
			if res.Status == want {
				return
			}
		} else {
			last = err.Error()
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s 未在 %s 内返回 %d（当前 %s）", path, timeout, want, last)
}

// assertHealthAndReady 断言 /health 与 /ready 的当前状态码。
func assertHealthAndReady(t *testing.T, base string, wantHealth, wantReady int) {
	t.Helper()
	if res := isoDo(t, base, "GET", "/health", nil, nil); res.Status != wantHealth {
		t.Fatalf("/health 状态码=%d want %d（body code=%d msg=%q）", res.Status, wantHealth, res.Code, res.Message)
	}
	if res := isoDo(t, base, "GET", "/ready", nil, nil); res.Status != wantReady {
		t.Fatalf("/ready 状态码=%d want %d（body code=%d msg=%q）", res.Status, wantReady, res.Code, res.Message)
	}
}
