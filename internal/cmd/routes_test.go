package cmd

import (
	"testing"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"

	"cnb.cool/go-cloud-devops/my-shop/internal/controller/health"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
)

// collectRoutes 注册生产同款路由（Health + 前台 + 后台），返回 method+route 集合。
// 不启动服务器、不依赖数据库，仅验证路由表结构与生产入口完全一致。
func collectRoutes(t *testing.T) map[string]bool {
	t.Helper()
	s := g.Server("routes-" + guid.S())
	s.SetAddr(":0")
	s.Group("/", func(root *ghttp.RouterGroup) {
		root.Middleware(middleware.Response)
		root.Bind(health.NewV1())
		RegisterFrontendRoutes(root)
		RegisterAdminRoutes(root)
	})
	s.SetDumpRouterMap(false)
	// 路由在 Start 时才真正绑定到 server（handlePreBindItems），故须先启动再取路由表。
	if err := s.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown() })

	routes := s.GetRoutes()
	got := make(map[string]bool, len(routes))
	for _, r := range routes {
		got[r.Method+" "+r.Route] = true
	}
	return got
}

// TestRouteTable 锁定最终路由表：前台 /api/v1、后台 /admin/v1 的完整路径存在，
// 且不存在旧路径、旧分类写路径、公开注册入口或重复前缀。
func TestRouteTable(t *testing.T) {
	got := collectRoutes(t)

	want := []string{
		// 前台公开接口。
		"POST /api/v1/register",
		"POST /api/v1/login",
		"GET /api/v1/categories",
		"GET /api/v1/categories/:id",
		// 前台登录用户接口。
		"GET /api/v1/me",
		"POST /api/v1/logout",
		// 健康检查。
		"GET /health",
		// 后台公开接口。
		"POST /admin/v1/login",
		"POST /admin/v1/logout",
		// 后台认证接口。
		"GET /admin/v1/me",
		// 管理员管理。
		"POST /admin/v1/admins",
		"PUT /admin/v1/admins/:id/status",
		"DELETE /admin/v1/admins/:id",
		"POST /admin/v1/admins/:id/roles",
		"DELETE /admin/v1/admins/:id/roles/:role_id",
		// 角色管理。
		"POST /admin/v1/roles",
		"GET /admin/v1/roles",
		"PUT /admin/v1/roles/:id",
		"DELETE /admin/v1/roles/:id",
		"POST /admin/v1/roles/:id/permissions",
		"DELETE /admin/v1/roles/:id/permissions/:permission_id",
		// 权限管理。
		"POST /admin/v1/permissions",
		"GET /admin/v1/permissions",
		"PUT /admin/v1/permissions/:id",
		"DELETE /admin/v1/permissions/:id",
		// 后台分类管理。
		"POST /admin/v1/categories",
		"PUT /admin/v1/categories/:id",
		"DELETE /admin/v1/categories/:id",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("缺少路由 %q", w)
		}
	}

	forbidden := []string{
		// 旧前台路径。
		"POST /register",
		"POST /login",
		"GET /me",
		"POST /logout",
		"GET /categories",
		"GET /categories/:id",
		// 旧分类写路径。
		"POST /categories",
		"PUT /categories/:id",
		"DELETE /categories/:id",
		// 旧后台路径（无 /v1）。
		"POST /admin/login",
		"POST /admin/logout",
		"GET /admin/me",
		"POST /admin/admins",
		// 公开注册入口。
		"POST /admin/v1/register",
		// 重复前缀。
		"POST /api/v1/api/v1/register",
		"GET /api/v1/api/v1/me",
		"POST /admin/v1/admin/v1/login",
		"GET /admin/v1/admin/v1/me",
	}
	for _, f := range forbidden {
		if got[f] {
			t.Errorf("不应存在路由 %q", f)
		}
	}
}
