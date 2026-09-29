package cmd

import (
	"github.com/gogf/gf/v2/net/ghttp"

	adminController "cnb.cool/go-cloud-devops/my-shop/internal/controller/admin"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/categories"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
)

// RegisterAdminRoutes 挂载后台路由（统一前缀 /admin/v1）：
//   - 后台公开接口：/admin/v1/login（公开）、/admin/v1/logout（仅验签）。不存在任何公开注册入口。
//   - 后台认证接口：/admin/v1/me（AdminAuth）。
//   - 管理员/角色/权限管理、分类写操作：AdminAuth + RequirePermission 双层保护。
//
// 生产入口与集成测试复用本函数，避免安全路由在两地维护出现漂移。
//
// 注意：API 请求结构体的 g.Meta path 标签为完整路径，GoFrame 在绑定时会用它覆盖
// 显式 path，因此这里必须用完整路径注册、且不再叠加 /admin/v1 前缀，否则会得到
// /admin/v1/admin/v1/... 之类的重复前缀。
func RegisterAdminRoutes(root *ghttp.RouterGroup) {
	adminCtrl := adminController.NewV1()
	categoriesCtrl := categories.NewV1()

	// 后台公开接口：管理员登录（无需 token）。
	root.POST("/admin/v1/login", adminCtrl.Login)

	// 后台登出：仅验签（幂等撤销），即使会话已撤销/缺失也能到达 handler。
	root.Group("/", func(sig *ghttp.RouterGroup) {
		sig.Middleware(middleware.AdminAuthSignatureOnly)
		sig.POST("/admin/v1/logout", adminCtrl.Logout)
	})

	// 后台认证与授权接口：认证（AdminAuth）+ 授权（RequirePermission）双层保护。
	root.Group("/", func(admin *ghttp.RouterGroup) {
		admin.Middleware(middleware.AdminAuth)
		admin.GET("/admin/v1/me", adminCtrl.Me)

		// require 返回一个继承了 AdminAuth、再叠加指定权限校验的子分组。
		require := func(code string) *ghttp.RouterGroup {
			permissionGroup := admin.Group("/")
			permissionGroup.Middleware(middleware.RequirePermission(code))
			return permissionGroup
		}

		// 管理员管理。
		require("admin:create").POST("/admin/v1/admins", adminCtrl.CreateAdmin)
		require("admin:disable").PUT("/admin/v1/admins/:id/status", adminCtrl.UpdateAdminStatus)
		require("admin:delete").DELETE("/admin/v1/admins/:id", adminCtrl.DeleteAdmin)
		require("admin:assign_role").POST("/admin/v1/admins/:id/roles", adminCtrl.AssignAdminRole)
		require("admin:assign_role").DELETE("/admin/v1/admins/:id/roles/:role_id", adminCtrl.RemoveAdminRole)

		// 角色管理。
		require("role:create").POST("/admin/v1/roles", adminCtrl.CreateRole)
		require("role:list").GET("/admin/v1/roles", adminCtrl.ListRole)
		require("role:update").PUT("/admin/v1/roles/:id", adminCtrl.UpdateRole)
		require("role:delete").DELETE("/admin/v1/roles/:id", adminCtrl.DeleteRole)
		require("role:assign_permission").POST("/admin/v1/roles/:id/permissions", adminCtrl.AssignRolePermission)
		require("role:assign_permission").DELETE("/admin/v1/roles/:id/permissions/:permission_id", adminCtrl.RemoveRolePermission)

		// 权限管理。
		require("permission:create").POST("/admin/v1/permissions", adminCtrl.CreatePermission)
		require("permission:list").GET("/admin/v1/permissions", adminCtrl.ListPermission)
		require("permission:update").PUT("/admin/v1/permissions/:id", adminCtrl.UpdatePermission)
		require("permission:delete").DELETE("/admin/v1/permissions/:id", adminCtrl.DeletePermission)

		// 分类管理（写操作，前台查询仍为公开接口，写操作只存在于后台）。
		require("category:create").POST("/admin/v1/categories", categoriesCtrl.Create)
		require("category:update").PUT("/admin/v1/categories/:id", categoriesCtrl.Update)
		require("category:delete").DELETE("/admin/v1/categories/:id", categoriesCtrl.Delete)
	})
}
