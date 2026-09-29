package cmd

import (
	"context"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gcmd"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/health"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
)

// Main 是应用启动入口：Bootstrap → 创建 Server → 全局 Response → Health → 前台路由 →
// 后台路由 → 启动 Server。路由按职责拆分到 routes_frontend.go 与 routes_admin.go。
var (
	Main = gcmd.Command{
		Name:  "main",
		Usage: "main",
		Brief: "start http server of my-shop",
		Func: func(ctx context.Context, parser *gcmd.Parser) (err error) {
			if err = boot.Bootstrap(ctx); err != nil {
				return err
			}

			s := g.Server()
			s.Group("/", func(root *ghttp.RouterGroup) {
				root.Middleware(middleware.Response)
				root.Bind(health.NewV1())

				RegisterFrontendRoutes(root)
				RegisterAdminRoutes(root)
			})
			s.Run()
			return nil
		},
	}
)
