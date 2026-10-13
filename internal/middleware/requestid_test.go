package middleware_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"

	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
)

// requestIDEchoHandler 将 RequestID 中间件注入到上下文中的 request_id 原样写进响应体，
// 用于验证「注入上下文」这一可观察行为（handler 直接 WriteJson，不经 Response 二次包裹）。
func requestIDEchoHandler(r *ghttp.Request) {
	r.Response.WriteJson(g.Map{"request_id": middleware.RequestIDFromContext(r.Context())})
}

// setupRequestIDServer 启动一个仅含 RequestID + Response 中间件的独立服务（无 DB 依赖），
// 验证 RequestID 中间件本身的行为，避免耦合到需要真实 MySQL/Redis 的鉴权测试。
func setupRequestIDServer(t *testing.T) string {
	t.Helper()
	s := g.Server(guid.S())
	s.SetAddr(":0")
	s.Group("/", func(root *ghttp.RouterGroup) {
		root.Middleware(middleware.RequestID)
		root.Middleware(middleware.Response)
		root.GET("/echo", requestIDEchoHandler)
	})
	s.SetDumpRouterMap(false)
	s.Start()
	t.Cleanup(func() { _ = s.Shutdown() })
	time.Sleep(100 * time.Millisecond)
	return fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
}

// getWithHeader 发起一次 GET 并返回原始响应对象与已解码的 JSON 响应体。
func getWithHeader(t *testing.T, base, path string, headers map[string]string) (*gclient.Response, map[string]any) {
	t.Helper()
	c := g.Client()
	if len(headers) > 0 {
		c = c.Header(headers)
	}
	r, err := c.Get(context.Background(), base+path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { _ = r.Close() })
	var body map[string]any
	_ = json.Unmarshal(r.ReadAll(), &body)
	return r, body
}

// TestRequestIDEchoPreservesHeader 覆盖 AC-006：上游（Nginx）生成的 X-Request-ID 被原样回写，
// 并注入请求上下文（handler 读到的值 == 响应头值）。若中间件漏掉回写或漏注入，本测试会失败。
func TestRequestIDEchoPreservesHeader(t *testing.T) {
	base := setupRequestIDServer(t)
	const want = "fixed-request-id-123"
	r, body := getWithHeader(t, base, "/echo", map[string]string{"X-Request-ID": want})

	if got := r.Header.Get("X-Request-ID"); got != want {
		t.Fatalf("响应头 X-Request-ID 应为 %q，实际 %q", want, got)
	}
	if got := body["request_id"]; got != want {
		t.Fatalf("上下文注入的 request_id 应为 %q，实际 %v", want, got)
	}
}

// TestRequestIDGeneratedWhenMissing 覆盖 AC-006：客户端不带 X-Request-ID 时，
// 中间件自生成一个非空 ID 并回写、注入上下文，且二者一致（直连/健康检查路径）。
func TestRequestIDGeneratedWhenMissing(t *testing.T) {
	base := setupRequestIDServer(t)
	r, body := getWithHeader(t, base, "/echo", nil)

	rid := r.Header.Get("X-Request-ID")
	if rid == "" {
		t.Fatalf("缺少 X-Request-ID 时应自生成非空 Request ID")
	}
	if got := body["request_id"]; got != rid {
		t.Fatalf("上下文注入的 request_id 应与响应头一致：header=%q body=%v", rid, got)
	}
}
