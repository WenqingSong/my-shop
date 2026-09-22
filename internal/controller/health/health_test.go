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

	healthController "cnb.cool/go-cloud-devops/my-shop/internal/controller/health"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
)

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
