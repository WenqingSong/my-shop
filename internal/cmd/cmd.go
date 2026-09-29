package cmd

import (
	"context"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gcmd"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	adminController "cnb.cool/go-cloud-devops/my-shop/internal/controller/admin"
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

				// 受保护接口：/me 需要 Bearer Token 认证 + 会话有效性校验（前台用户侧）。
				// 分类写操作已迁移到后台管理（AdminAuth + RequirePermission），见 RegisterAdminRoutes。
				group.Group("/", func(protected *ghttp.RouterGroup) {
					protected.Middleware(middleware.Auth)
					protected.GET("/me", iamCtrl.Me)
				})

				// 受保护接口：/logout 仅验签（幂等撤销，即使 session 已撤销/缺失也能到达 handler）。
				group.Group("/", func(protected *ghttp.RouterGroup) {
					protected.Middleware(middleware.AuthSignatureOnly)
					protected.POST("/logout", iamCtrl.Logout)
				})

				RegisterAdminRoutes(group)
			})
			s.Run()
			return nil
		},
	}
)

// RegisterAdminRoutes 挂载后台管理员身份与 RBAC 路由：/admin/login（公开）、
// /admin/logout（仅验签）以及 /admin/me 与 admins/roles/permissions 管理接口
// （AdminAuth + RequirePermission 双层保护）；同时承载分类写接口的迁移挂载
// （/categories POST/PUT/DELETE，由前台 Auth 迁至 AdminAuth + RequirePermission）。
//
// 抽为独立函数供生产入口与集成测试复用，避免安全路由在生产与测试两处维护出现漂移。
//
// 注意：API 请求结构体的 g.Meta path 标签为完整路径，GoFrame 在绑定时会用它覆盖
// 显式 path，因此这里必须用完整路径注册、且不再叠加 /admin 前缀，否则会得到
// /admin/admin/... 之类的重复前缀。
func RegisterAdminRoutes(group *ghttp.RouterGroup) {
	adminCtrl := adminController.NewV1()
	categoriesCtrl := categories.NewV1()
	// 公开接口：管理员登录（无需 token）。不存在任何公开注册入口。
	group.POST("/admin/login", adminCtrl.Login)

	// /admin/logout 仅验签（幂等撤销），即使会话已撤销/缺失也能到达 handler。
	group.Group("/", func(protected *ghttp.RouterGroup) {
		protected.Middleware(middleware.AdminAuthSignatureOnly)
		protected.POST("/admin/logout", adminCtrl.Logout)
	})

	// 后台管理接口：认证（AdminAuth）+ 授权（RequirePermission）双层保护。
	group.Group("/", func(admin *ghttp.RouterGroup) {
		admin.Middleware(middleware.AdminAuth)
		admin.GET("/admin/me", adminCtrl.Me)

		// require 返回一个继承了 AdminAuth、再叠加指定权限校验的子分组。
		require := func(code string) *ghttp.RouterGroup {
			g := admin.Group("/")
			g.Middleware(middleware.RequirePermission(code))
			return g
		}

		// 管理员管理。
		require("admin:create").POST("/admin/admins", adminCtrl.CreateAdmin)
		require("admin:disable").PUT("/admin/admins/:id/status", adminCtrl.UpdateAdminStatus)
		require("admin:delete").DELETE("/admin/admins/:id", adminCtrl.DeleteAdmin)
		require("admin:assign_role").POST("/admin/admins/:id/roles", adminCtrl.AssignAdminRole)
		require("admin:assign_role").DELETE("/admin/admins/:id/roles/:role_id", adminCtrl.RemoveAdminRole)

		// 角色管理。
		require("role:create").POST("/admin/roles", adminCtrl.CreateRole)
		require("role:list").GET("/admin/roles", adminCtrl.ListRole)
		require("role:update").PUT("/admin/roles/:id", adminCtrl.UpdateRole)
		require("role:delete").DELETE("/admin/roles/:id", adminCtrl.DeleteRole)
		require("role:assign_permission").POST("/admin/roles/:id/permissions", adminCtrl.AssignRolePermission)
		require("role:assign_permission").DELETE("/admin/roles/:id/permissions/:permission_id", adminCtrl.RemoveRolePermission)

		// 权限管理。
		require("permission:create").POST("/admin/permissions", adminCtrl.CreatePermission)
		require("permission:list").GET("/admin/permissions", adminCtrl.ListPermission)
		require("permission:update").PUT("/admin/permissions/:id", adminCtrl.UpdatePermission)
		require("permission:delete").DELETE("/admin/permissions/:id", adminCtrl.DeletePermission)

		// 分类写接口迁移：由前台 Auth（仅登录）迁至 AdminAuth + RequirePermission。
		require("category:create").POST("/categories", categoriesCtrl.Create)
		require("category:update").PUT("/categories/:id", categoriesCtrl.Update)
		require("category:delete").DELETE("/categories/:id", categoriesCtrl.Delete)
	})
}
