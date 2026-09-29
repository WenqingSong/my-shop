package cmd

import (
	"github.com/gogf/gf/v2/net/ghttp"

	"cnb.cool/go-cloud-devops/my-shop/internal/controller/categories"
	"cnb.cool/go-cloud-devops/my-shop/internal/controller/iam"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
)

// RegisterFrontendRoutes 挂载前台路由（统一前缀 /api/v1）：
//   - 前台公开接口：注册、登录、分类树形列表与详情（无需 token）。
//   - 前台登录用户接口：/me（Auth）、/logout（AuthSignatureOnly，仅验签幂等撤销）。
//
// 生产入口与集成测试复用本函数，避免安全路由在两地维护出现漂移。
//
// 注意：API 请求结构体的 g.Meta path 标签为完整路径，GoFrame 在绑定时会用它覆盖
// 显式 path，因此这里必须用完整路径注册、且不再叠加 /api/v1 前缀，否则会得到
// /api/v1/api/v1/... 之类的重复前缀。
func RegisterFrontendRoutes(root *ghttp.RouterGroup) {
	iamCtrl := iam.NewV1()
	categoriesCtrl := categories.NewV1()

	// 前台公开接口：注册、登录、分类查询（无需 token）。
	root.POST("/api/v1/register", iamCtrl.Register)
	root.POST("/api/v1/login", iamCtrl.Login)
	root.GET("/api/v1/categories", categoriesCtrl.List)
	root.GET("/api/v1/categories/:id", categoriesCtrl.Detail)

	// 前台登录用户接口：/me 需要认证 + 会话有效性校验。
	root.Group("/", func(user *ghttp.RouterGroup) {
		user.Middleware(middleware.Auth)
		user.GET("/api/v1/me", iamCtrl.Me)
	})

	// 前台登出：仅验签（幂等撤销，即使 session 已撤销/缺失也能到达 handler）。
	root.Group("/", func(user *ghttp.RouterGroup) {
		user.Middleware(middleware.AuthSignatureOnly)
		user.POST("/api/v1/logout", iamCtrl.Logout)
	})
}
