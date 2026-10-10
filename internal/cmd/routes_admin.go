package cmd

import (
	"github.com/gogf/gf/v2/net/ghttp"

	adminController "cnb.cool/go-cloud-devops/my-shop/internal/controller/admin"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/banner"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/categories"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/dashboard"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/flashsale"
	iamController "cnb.cool/go-cloud-devops/my-shop/internal/controller/iam"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/inventory"
	orderController "cnb.cool/go-cloud-devops/my-shop/internal/controller/order"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/product"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/recommendation"
	reviewController "cnb.cool/go-cloud-devops/my-shop/internal/controller/review"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/sku"
	uploadCtrl "cnb.cool/go-cloud-devops/my-shop/internal/controller/upload"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
)

// RegisterAdminRoutes 挂载后台路由（前缀 /admin，路径与 Contract 一致）：
//   - 后台公开接口：/admin/login（公开）、/admin/logout（仅验签）。不存在任何公开注册入口。
//   - 后台认证接口：/admin/me（AdminAuth）。
//   - 管理员/角色/权限管理、分类写操作：AdminAuth + RequirePermission 双层保护。
//     分类写操作仍挂载在 /categories 路径（前台查询为公开接口，写接口复用同一路径前缀）。
//
// 生产入口与集成测试复用本函数，避免安全路由在两地维护出现漂移。
//
// 注意：API 请求结构体的 g.Meta path 标签为完整路径，GoFrame 在绑定时会用它覆盖
// 显式 path，因此这里必须用完整路径注册、且与 g.Meta path 保持一致，否则会出现路径漂移。
func RegisterAdminRoutes(root *ghttp.RouterGroup) {
	adminCtrl := adminController.NewV1()
	iamCtrl := iamController.NewV1()
	categoriesCtrl := categories.NewV1()
	productCtrl := product.NewV1()
	skuCtrl := sku.NewV1()
	inventoryCtrl := inventory.NewV1()
	orderAdminCtrl := orderController.NewAdminV1()
	flashsaleAdminCtrl := flashsale.NewAdminV1()
	reviewAdminCtrl := reviewController.NewAdminV1()
	bannerCtrl := banner.NewV1()
	recommendationCtrl := recommendation.NewV1()
	uploadCtrlV1 := uploadCtrl.NewV1()
	dashboardCtrl := dashboard.NewV1()

	// 后台公开接口：管理员登录（无需 token）。
	root.POST("/admin/login", adminCtrl.Login)

	// 后台登出：仅验签（幂等撤销），即使会话已撤销/缺失也能到达 handler。
	root.Group("/", func(sig *ghttp.RouterGroup) {
		sig.Middleware(middleware.AdminAuthSignatureOnly)
		sig.POST("/admin/logout", adminCtrl.Logout)
	})

	// 后台认证与授权接口：认证（AdminAuth）+ 授权（RequirePermission）双层保护。
	root.Group("/", func(admin *ghttp.RouterGroup) {
		admin.Middleware(middleware.AdminAuth)
		admin.GET("/admin/me", adminCtrl.Me)

		// 文件上传（管理员，签发七牛云直传凭证；低敏感操作，不叠加独立权限 code）。
		admin.GET("/admin/qiniu/upload/token", uploadCtrlV1.AdminToken)

		// require 返回一个继承了 AdminAuth、再叠加指定权限校验的子分组。
		require := func(code string) *ghttp.RouterGroup {
			permissionGroup := admin.Group("/")
			permissionGroup.Middleware(middleware.RequirePermission(code))
			return permissionGroup
		}

		// 用户账号状态管理（查询/禁用/启用普通用户）。
		require("user:read").GET("/admin/users/:id/status", iamCtrl.GetUserStatus)
		require("user:status").PUT("/admin/users/:id/status", iamCtrl.UpdateUserStatus)

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

		// 分类管理（写操作，前台查询为公开接口，写接口复用 /categories 路径前缀）。
		require("category:create").POST("/categories", categoriesCtrl.Create)
		require("category:update").PUT("/categories/:id", categoriesCtrl.Update)
		require("category:delete").DELETE("/categories/:id", categoriesCtrl.Delete)

		// 商品查询（后台查询仅 AdminAuth，无读权限；查看全部状态）。
		admin.GET("/admin/products", productCtrl.AdminList)
		admin.GET("/admin/products/:id", productCtrl.AdminDetail)

		// 商品写操作（AdminAuth + RequirePermission）。
		require("product:create").POST("/admin/products", productCtrl.Create)
		require("product:update").PUT("/admin/products/:id", productCtrl.Update)
		require("product:on_shelf").POST("/admin/products/:id/on-shelf", productCtrl.OnShelf)
		require("product:off_shelf").POST("/admin/products/:id/off-shelf", productCtrl.OffShelf)

		// SKU 写操作（AdminAuth + RequirePermission，扁平路由）。
		require("sku:create").POST("/admin/skus", skuCtrl.Create)
		require("sku:update").PUT("/admin/skus/:id", skuCtrl.Update)
		require("sku:delete").DELETE("/admin/skus/:id", skuCtrl.Delete)

		// 库存查询（后台仅 AdminAuth，无读权限）。
		admin.GET("/admin/inventories/:sku_id", inventoryCtrl.Get)
		admin.GET("/admin/inventories/:sku_id/logs", inventoryCtrl.ListLogs)

		// 库存写操作（AdminAuth + RequirePermission）。
		require("inventory:increase").POST("/admin/inventories/:sku_id/increase", inventoryCtrl.Increase)
		require("inventory:deduct").POST("/admin/inventories/:sku_id/deduct", inventoryCtrl.Deduct)

		// 订单后台写操作（AdminAuth + RequirePermission）。
		require("order:ship").POST("/admin/orders/:id/ship", orderAdminCtrl.Ship)
		require("order:refund").POST("/admin/orders/:id/refund", orderAdminCtrl.Refund)

		// 订单后台只读操作（AdminAuth + RequirePermission）：
		//   - 列表与统计持 order:list；详情持 order:view。
		//   - stats 静态段优先于 /admin/orders/:id，避免被 :id 参数段吞掉。
		require("order:list").GET("/admin/orders", orderAdminCtrl.AdminList)
		require("order:list").GET("/admin/orders/stats", orderAdminCtrl.AdminStats)
		require("order:view").GET("/admin/orders/:id", orderAdminCtrl.AdminDetail)

		// 秒杀活动后台写操作（AdminAuth + RequirePermission）。
		require("flash_sale:create").POST("/admin/flash-sales", flashsaleAdminCtrl.Create)
		require("flash_sale:update").PUT("/admin/flash-sales/:id", flashsaleAdminCtrl.Update)
		// 秒杀请求人工修复 + 审计查询（AdminAuth + RequirePermission("flash_sale:repair")）。
		require("flash_sale:repair").POST("/admin/flash-sales/requests/:id/repair", flashsaleAdminCtrl.RepairRequest)
		require("flash_sale:repair").GET("/admin/flash-sales/requests/:id/audits", flashsaleAdminCtrl.ListRequestAudits)

		// 商品评价后台写操作（AdminAuth + RequirePermission）。
		require("review:take_down").POST("/admin/reviews/:id/take-down", reviewAdminCtrl.TakeDown)

		// 轮播图后台查询（仅 AdminAuth，无读权限；查看全部状态）。
		admin.GET("/admin/banners", bannerCtrl.AdminList)
		admin.GET("/admin/banners/:id", bannerCtrl.AdminDetail)

		// 轮播图后台写操作（AdminAuth + RequirePermission）。
		require("banner:create").POST("/admin/banners", bannerCtrl.Create)
		require("banner:update").PUT("/admin/banners/:id", bannerCtrl.Update)
		require("banner:delete").DELETE("/admin/banners/:id", bannerCtrl.Delete)

		// 推荐位后台查询（仅 AdminAuth，无读权限；查看全部状态）。
		admin.GET("/admin/recommend-positions", recommendationCtrl.AdminList)
		admin.GET("/admin/recommend-positions/:id", recommendationCtrl.AdminDetail)

		// 推荐位后台写操作（AdminAuth + RequirePermission）。
		require("recommend:create").POST("/admin/recommend-positions", recommendationCtrl.Create)
		require("recommend:update").PUT("/admin/recommend-positions/:id", recommendationCtrl.Update)
		require("recommend:delete").DELETE("/admin/recommend-positions/:id", recommendationCtrl.Delete)
		require("recommend:item").POST("/admin/recommend-positions/:id/items", recommendationCtrl.AddItem)
		require("recommend:item").DELETE("/admin/recommend-positions/:id/items/:product_id", recommendationCtrl.RemoveItem)
		require("recommend:item").PUT("/admin/recommend-positions/:id/items/sort", recommendationCtrl.UpdateSort)

		// 运营数据大屏（AdminAuth + RequirePermission("dashboard:view")）。
		require("dashboard:view").GET("/admin/dashboard/overview", dashboardCtrl.Overview)
		require("dashboard:view").GET("/admin/dashboard/orders/trend", dashboardCtrl.Trend)
		require("dashboard:view").GET("/admin/dashboard/orders/status", dashboardCtrl.Status)
		require("dashboard:view").GET("/admin/dashboard/products/top", dashboardCtrl.TopProducts)
		require("dashboard:view").GET("/admin/dashboard/flash-sales", dashboardCtrl.FlashSales)
	})
}
