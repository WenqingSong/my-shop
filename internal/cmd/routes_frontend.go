package cmd

import (
	"github.com/gogf/gf/v2/net/ghttp"

	"cnb.cool/go-cloud-devops/my-shop/internal/controller/categories"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/iam"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/product"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
)

// RegisterFrontendRoutes 挂载前台路由（无版本前缀，路径与 Contract 一致）：
//   - 前台公开接口：注册、登录、分类树形列表与详情（无需 token）。
//   - 前台登录用户接口：/me（Auth）、/logout（AuthSignatureOnly，仅验签幂等撤销）。
//
// 生产入口与集成测试复用本函数，避免安全路由在两地维护出现漂移。
//
// 注意：API 请求结构体的 g.Meta path 标签为完整路径，GoFrame 在绑定时会用它覆盖
// 显式 path，因此这里必须用完整路径注册、且与 g.Meta path 保持一致，否则会出现路径漂移。
func RegisterFrontendRoutes(root *ghttp.RouterGroup) {
	iamCtrl := iam.NewV1()
	categoriesCtrl := categories.NewV1()
	productCtrl := product.NewV1()

	// 前台公开接口：注册、登录、分类查询、商品查询（无需 token）。
	root.POST("/register", iamCtrl.Register)
	root.POST("/login", iamCtrl.Login)
	root.GET("/categories", categoriesCtrl.List)
	root.GET("/categories/:id", categoriesCtrl.Detail)
	root.GET("/products", productCtrl.List)
	root.GET("/products/:id", productCtrl.Detail)

	// 前台登录用户接口：/me、会话列表与主动撤销均需要认证 + 会话有效性校验。
	root.Group("/", func(user *ghttp.RouterGroup) {
		user.Middleware(middleware.Auth)
		user.GET("/me", iamCtrl.Me)
		user.GET("/sessions", iamCtrl.ListSessions)
		user.DELETE("/sessions/:sid", iamCtrl.RevokeSession)
		user.POST("/sessions/revoke-others", iamCtrl.RevokeOthers)
		user.POST("/sessions/revoke-all", iamCtrl.RevokeAll)
	})

	// 前台登出：仅验签（幂等撤销，即使 session 已撤销/缺失也能到达 handler）。
	root.Group("/", func(user *ghttp.RouterGroup) {
		user.Middleware(middleware.AuthSignatureOnly)
		user.POST("/logout", iamCtrl.Logout)
	})
}
