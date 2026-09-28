package cmd

import (
	"context"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gcmd"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/categories"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/health"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/iam"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
)

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
			s.Group("/", func(group *ghttp.RouterGroup) {
				group.Middleware(middleware.Response)
				group.Bind(health.NewV1())

				iamCtrl := iam.NewV1()
				// 公开接口：注册、登录（无需 token）。
				group.POST("/register", iamCtrl.Register)
				group.POST("/login", iamCtrl.Login)

				categoriesCtrl := categories.NewV1()
				// 公开接口：分类树形列表与详情（无需 token）。
				group.GET("/categories", categoriesCtrl.List)
				group.GET("/categories/:id", categoriesCtrl.Detail)

				// 受保护接口：/me 与分类写操作需要 Bearer Token 认证 + 会话有效性校验。
				group.Group("/", func(protected *ghttp.RouterGroup) {
					protected.Middleware(middleware.Auth)
					protected.GET("/me", iamCtrl.Me)
					protected.POST("/categories", categoriesCtrl.Create)
					protected.PUT("/categories/:id", categoriesCtrl.Update)
					protected.DELETE("/categories/:id", categoriesCtrl.Delete)
				})

				// 受保护接口：/logout 仅验签（幂等撤销，即使 session 已撤销/缺失也能到达 handler）。
				group.Group("/", func(protected *ghttp.RouterGroup) {
					protected.Middleware(middleware.AuthSignatureOnly)
					protected.POST("/logout", iamCtrl.Logout)
				})
			})
			s.Run()
			return nil
		},
	}
)
