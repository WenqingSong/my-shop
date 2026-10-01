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

// TestRouteTable 锁定最终路由表：Contract 原路径（前台无版本前缀、后台 /admin、分类写 /categories）存在，
// 且不存在 /api/v1、/admin/v1 前缀、公开注册入口或重复前缀。
func TestRouteTable(t *testing.T) {
	got := collectRoutes(t)

	want := []string{
		// 前台公开接口。
		"POST /register",
		"POST /login",
		"GET /categories",
		"GET /categories/:id",
		"GET /products",
		"GET /products/:id",
		// 前台登录用户接口。
		"GET /me",
		"POST /logout",
		// 健康检查。
		"GET /health",
		// 后台公开接口。
		"POST /admin/login",
		"POST /admin/logout",
		// 后台认证接口。
		"GET /admin/me",
		// 管理员管理。
		"POST /admin/admins",
		"PUT /admin/admins/:id/status",
		"DELETE /admin/admins/:id",
		"POST /admin/admins/:id/roles",
		"DELETE /admin/admins/:id/roles/:role_id",
		// 角色管理。
		"POST /admin/roles",
		"GET /admin/roles",
		"PUT /admin/roles/:id",
		"DELETE /admin/roles/:id",
		"POST /admin/roles/:id/permissions",
		"DELETE /admin/roles/:id/permissions/:permission_id",
		// 权限管理。
		"POST /admin/permissions",
		"GET /admin/permissions",
		"PUT /admin/permissions/:id",
		"DELETE /admin/permissions/:id",
		// 分类写接口（复用 /categories 路径，AdminAuth + RequirePermission 保护）。
		"POST /categories",
		"PUT /categories/:id",
		"DELETE /categories/:id",
		// 商品查询（后台仅 AdminAuth，查看全部状态）。
		"GET /admin/products",
		"GET /admin/products/:id",
		// 商品写接口（AdminAuth + RequirePermission 保护）。
		"POST /admin/products",
		"PUT /admin/products/:id",
		"POST /admin/products/:id/on-shelf",
		"POST /admin/products/:id/off-shelf",
		// 库存查询与写操作（后台 AdminAuth；写操作额外 RequirePermission）。
		"GET /admin/inventories/:sku_id",
		"GET /admin/inventories/:sku_id/logs",
		"POST /admin/inventories/:sku_id/increase",
		"POST /admin/inventories/:sku_id/deduct",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("缺少路由 %q", w)
		}
	}

	forbidden := []string{
		// /api/v1/* 前缀路径不应存在。
		"POST /api/v1/register",
		"POST /api/v1/login",
		"GET /api/v1/me",
		"POST /api/v1/logout",
		"GET /api/v1/categories",
		"GET /api/v1/categories/:id",
		"POST /api/v1/categories",
		"PUT /api/v1/categories/:id",
		"DELETE /api/v1/categories/:id",
		"GET /api/v1/products",
		"GET /api/v1/products/:id",
		"POST /api/v1/products",
		"PUT /api/v1/products/:id",
		// /admin/v1/* 前缀路径不应存在。
		"POST /admin/v1/login",
		"POST /admin/v1/logout",
		"GET /admin/v1/me",
		"POST /admin/v1/admins",
		"PUT /admin/v1/admins/:id/status",
		"DELETE /admin/v1/admins/:id",
		"POST /admin/v1/roles",
		"GET /admin/v1/roles",
		"PUT /admin/v1/roles/:id",
		"DELETE /admin/v1/roles/:id",
		"POST /admin/v1/permissions",
		"GET /admin/v1/permissions",
		"PUT /admin/v1/permissions/:id",
		"DELETE /admin/v1/permissions/:id",
		"POST /admin/v1/categories",
		"PUT /admin/v1/categories/:id",
		"DELETE /admin/v1/categories/:id",
		"GET /admin/v1/products",
		"GET /admin/v1/products/:id",
		"POST /admin/v1/products",
		"PUT /admin/v1/products/:id",
		"POST /admin/v1/products/:id/on-shelf",
		"POST /admin/v1/products/:id/off-shelf",
		// 公开注册入口。
		"POST /admin/register",
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
