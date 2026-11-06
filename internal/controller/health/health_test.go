package health_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/health/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	healthController "cnb.cool/go-cloud-devops/my-shop/internal/controller/health"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

type unavailableHealth struct{}

func (unavailableHealth) Liveness(context.Context) (*v1.LivenessRes, error) {
	return &v1.LivenessRes{Status: "ok"}, nil
}

func (unavailableHealth) Readiness(context.Context) (*v1.ReadinessRes, error) {
	return nil, codes.New(codes.CodeServiceUnavailable)
}

func TestLivenessHTTP(t *testing.T) {
	s := g.Server(guid.S())
	s.Group("/", func(group *ghttp.RouterGroup) {
		group.Middleware(ghttp.MiddlewareHandlerResponse)
		group.Bind(healthController.NewV1())
	})
	s.SetDumpRouterMap(false)
	s.Start()
	defer s.Shutdown()

	time.Sleep(100 * time.Millisecond)

	ctx := context.Background()
	client := g.Client()
	client.SetPrefix(fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort()))

	resp, err := client.Get(ctx, "/health")
	if err != nil {
		t.Fatalf("request /health failed: %v", err)
	}
	defer resp.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	body := resp.ReadAllString()
	if !strings.Contains(body, "ok") {
		t.Fatalf("expected body to contain %q, got %q", "ok", body)
	}
}

// TestProbeHTTPStatus 验证生产响应中间件将依赖故障映射为可供编排器使用的 HTTP 503，
// 同时 /health 仍返回 200，避免依赖抖动触发存活探针重启。
func TestProbeHTTPStatus(t *testing.T) {
	previous := service.Health()
	service.RegisterHealth(unavailableHealth{})
	t.Cleanup(func() { service.RegisterHealth(previous) })

	s := g.Server(guid.S())
	s.Group("/", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.Response)
		group.Bind(healthController.NewV1())
	})
	s.SetDumpRouterMap(false)
	s.Start()
	defer s.Shutdown()
	time.Sleep(100 * time.Millisecond)

	client := g.Client()
	client.SetPrefix(fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort()))
	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/health", want: 200},
		{path: "/ready", want: 503},
	} {
		resp, err := client.Get(context.Background(), tc.path)
		if err != nil {
			t.Fatalf("request %s failed: %v", tc.path, err)
		}
		if resp.StatusCode != tc.want {
			t.Errorf("%s: expected HTTP %d, got %d", tc.path, tc.want, resp.StatusCode)
		}
		resp.Close()
	}
}
