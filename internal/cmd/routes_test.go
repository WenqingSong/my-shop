package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
	"gopkg.in/yaml.v3"

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
		// GoFrame 会为根分组自动注册 ALL /* 兜底路由，不属于业务 API。
		if r.Method == "ALL" && r.Route == "/*" {
			continue
		}
		got[r.Method+" "+r.Route] = true
	}
	return got
}

// fullRouteTable 是 142 条常驻 JSON API 的完整清单（B1-01 §3.1 冻结，B1-02-04 补齐）。
// 任何新增、删除或路径/方法变更都必须同步更新本清单与 openapi/。
//
// 不计入本清单的路由：
//   - GET /metrics：仅 flash_sale.metrics.enabled=true 时注册，Prometheus 文本响应；
//   - /storage/banners/*：本地图片静态目录，非 JSON API。
func fullRouteTable() []string {
	return []string{
		// ---- 健康检查（2）----
		"GET /health",
		"GET /ready",

		// ---- 前台公开接口（14）----
		"POST /register",
		"POST /login",
		"POST /refresh",
		"GET /categories",
		"GET /categories/:id",
		"GET /products",
		"GET /products/:id",
		"GET /products/:id/reviews",
		"GET /likes/count",
		"GET /banners",
		"GET /recommendations/:code",
		"GET /articles",
		"GET /articles/:id",
		"GET /articles/:id/like/count",

		// ---- 前台用户认证（52）----
		"GET /me",
		"POST /addresses",
		"GET /addresses",
		"GET /addresses/:id",
		"PUT /addresses/:id",
		"DELETE /addresses/:id",
		"GET /cart",
		"POST /cart/items",
		"PUT /cart/items/:id",
		"PUT /cart/items/:id/selected",
		"DELETE /cart/items/:id",
		"POST /orders",
		"GET /orders",
		"GET /orders/:id",
		"POST /orders/:id/pay",
		"POST /orders/:id/cancel",
		"POST /orders/:id/receive",
		"POST /orders/:id/refund",
		"GET /refunds",
		"GET /refunds/:id",
		"POST /refunds/:id/revoke",
		"POST /flash-sales/:id/orders",
		"GET /flash-sales/:id/orders/result",
		"POST /reviews",
		"GET /my/reviews",
		"PUT /reviews/:id",
		"DELETE /reviews/:id",
		"POST /favorites",
		"DELETE /favorites/:product_id",
		"GET /favorites",
		"GET /favorites/check",
		"POST /likes",
		"DELETE /likes/:product_id",
		"GET /likes/check",
		"POST /articles",
		"PUT /articles/:id",
		"DELETE /articles/:id",
		"GET /my/articles",
		"POST /articles/:id/like",
		"DELETE /articles/:id/like",
		"GET /articles/:id/like/check",
		"POST /articles/:id/favorite",
		"DELETE /articles/:id/favorite",
		"GET /articles/:id/favorite/check",
		"GET /my/articles/favorites",
		"POST /coupons/:template_id/claim",
		"GET /my/coupons",
		"GET /sessions",
		"DELETE /sessions/:sid",
		"POST /sessions/revoke-others",
		"POST /sessions/revoke-all",
		"GET /qiniu/upload/token",

		// ---- 前台仅验签登出（1）----
		"POST /logout",

		// ---- 后台公开 / 仅验签（2）----
		"POST /admin/login",
		"POST /admin/logout",
		"GET /admin/me",
		"GET /admin/qiniu/upload/token",

		// ---- 后台用户状态（2）----
		"GET /admin/users/:id/status",
		"PUT /admin/users/:id/status",

		// ---- 管理员管理（5）----
		"POST /admin/admins",
		"PUT /admin/admins/:id/status",
		"DELETE /admin/admins/:id",
		"POST /admin/admins/:id/roles",
		"DELETE /admin/admins/:id/roles/:role_id",

		// ---- 角色管理（6）----
		"POST /admin/roles",
		"GET /admin/roles",
		"PUT /admin/roles/:id",
		"DELETE /admin/roles/:id",
		"POST /admin/roles/:id/permissions",
		"DELETE /admin/roles/:id/permissions/:permission_id",

		// ---- 权限管理（4）----
		"POST /admin/permissions",
		"GET /admin/permissions",
		"PUT /admin/permissions/:id",
		"DELETE /admin/permissions/:id",

		// ---- 分类写接口（3，路径复用 /categories）----
		"POST /categories",
		"PUT /categories/:id",
		"DELETE /categories/:id",

		// ---- 商品（8）----
		"GET /admin/products",
		"GET /admin/products/:id",
		"POST /admin/products",
		"PUT /admin/products/:id",
		"POST /admin/products/:id/on-shelf",
		"POST /admin/products/:id/off-shelf",

		// ---- SKU（3）----
		"POST /admin/skus",
		"PUT /admin/skus/:id",
		"DELETE /admin/skus/:id",

		// ---- 库存（4）----
		"GET /admin/inventories/:sku_id",
		"GET /admin/inventories/:sku_id/logs",
		"POST /admin/inventories/:sku_id/increase",
		"POST /admin/inventories/:sku_id/deduct",

		// ---- 订单后台（4）----
		"POST /admin/orders/:id/ship",
		"GET /admin/orders",
		"GET /admin/orders/stats",
		"GET /admin/orders/:id",

		// ---- 退款后台（6）----
		"GET /admin/refunds",
		"GET /admin/refunds/:id",
		"GET /admin/refunds/:id/audits",
		"POST /admin/refunds/:id/review",
		"POST /admin/refunds/:id/execute",
		"POST /admin/refunds/:id/close",

		// ---- 秒杀后台（4）----
		"POST /admin/flash-sales",
		"PUT /admin/flash-sales/:id",
		"POST /admin/flash-sales/requests/:id/repair",
		"GET /admin/flash-sales/requests/:id/audits",

		// ---- 评价后台（1）----
		"POST /admin/reviews/:id/take-down",

		// ---- 轮播图后台（5）----
		"GET /admin/banners",
		"GET /admin/banners/:id",
		"POST /admin/banners",
		"PUT /admin/banners/:id",
		"DELETE /admin/banners/:id",

		// ---- 推荐位后台（8）----
		"GET /admin/recommend-positions",
		"GET /admin/recommend-positions/:id",
		"POST /admin/recommend-positions",
		"PUT /admin/recommend-positions/:id",
		"DELETE /admin/recommend-positions/:id",
		"POST /admin/recommend-positions/:id/items",
		"DELETE /admin/recommend-positions/:id/items/:product_id",
		"PUT /admin/recommend-positions/:id/items/sort",

		// ---- 运营数据大屏（5）----
		"GET /admin/dashboard/overview",
		"GET /admin/dashboard/orders/trend",
		"GET /admin/dashboard/orders/status",
		"GET /admin/dashboard/products/top",
		"GET /admin/dashboard/flash-sales",

		// ---- 优惠券后台（3）----
		"POST /admin/coupons",
		"GET /admin/coupons",
		"POST /admin/coupons/:id/stop",
	}
}

// TestRouteTable 双向锁定最终路由表：
// 期望的 142 条必须存在，且不允许出现任何未登记的额外路由。
func TestRouteTable(t *testing.T) {
	got := collectRoutes(t)
	want := fullRouteTable()

	if len(want) != 142 {
		t.Fatalf("期望路由清单为 142 条，实际 %d 条（清单本身写错了）", len(want))
	}

	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		if wantSet[w] {
			t.Errorf("期望清单存在重复项 %q", w)
		}
		wantSet[w] = true
		if !got[w] {
			t.Errorf("缺少路由 %q", w)
		}
	}

	var extra []string
	for route := range got {
		if !wantSet[route] {
			extra = append(extra, route)
		}
	}
	sort.Strings(extra)
	for _, route := range extra {
		t.Errorf("出现未登记路由 %q（新增路由必须同步更新测试清单与 openapi/）", route)
	}

	if len(got) != len(want) {
		t.Errorf("实际路由数 %d，期望 %d", len(got), len(want))
	}
}

// TestRouteForbiddenPrefixes 锁定历史约束：不得引入 /api/v1 等版本前缀或公开注册入口。
func TestRouteForbiddenPrefixes(t *testing.T) {
	got := collectRoutes(t)
	forbidden := []string{
		"POST /api/v1/register",
		"POST /api/v1/login",
		"GET /api/v1/me",
		"POST /admin/v1/login",
		"GET /admin/v1/me",
		"POST /api/v1/api/v1/register",
		"POST /admin/register",
	}
	for _, f := range forbidden {
		if got[f] {
			t.Errorf("不应存在路由 %q", f)
		}
	}
	for route := range got {
		if strings.HasPrefix(route, "GET /api/") || strings.Contains(route, " /api/v1/") ||
			strings.Contains(route, " /admin/v1/") {
			t.Errorf("不应存在带版本前缀的路由 %q", route)
		}
	}
}

// TestRouteTableMatchesContract 校验路由注册表与 OpenAPI 契约双向一致。
// planned 接口（x-status=planned）不计入实际路由，也不得与真实路由冲突。
func TestRouteTableMatchesContract(t *testing.T) {
	got := collectRoutes(t)
	contract := readContractPaths(t)

	for route := range got {
		if !contract[route] {
			t.Errorf("路由 %q 未出现在 openapi/surgecart.openapi.yaml 中", route)
		}
	}
	for route := range contract {
		if !got[route] {
			t.Errorf("OpenAPI 中的 %q 不是实际注册路由（虚构接口）", route)
		}
	}
	if len(contract) != 142 {
		t.Errorf("OpenAPI 实际 API 数为 %d，期望 142", len(contract))
	}
}

// readContractPaths 解析唯一契约入口，返回 "METHOD /path" 集合（排除 planned）。
func readContractPaths(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join("..", "..", "openapi", "surgecart.openapi.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取契约失败（契约文件是必需交付物）: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("契约不是合法 YAML: %v", err)
	}
	if v, _ := doc["openapi"].(string); v != "3.1.0" {
		t.Fatalf("契约版本必须是 3.1.0，实际 %q", v)
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("契约缺少 paths")
	}

	out := map[string]bool{}
	for p, itemAny := range paths {
		item, ok := itemAny.(map[string]any)
		if !ok {
			continue
		}
		for m, opAny := range item {
			method := strings.ToUpper(m)
			switch method {
			case "GET", "POST", "PUT", "DELETE", "PATCH":
			default:
				continue
			}
			op, ok := opAny.(map[string]any)
			if !ok {
				continue
			}
			if status, _ := op["x-status"].(string); status == "planned" {
				if impl, ok := op["x-implemented"].(bool); ok && impl {
					t.Errorf("planned 接口 %s %s 不得声明已实现", method, p)
				}
				continue
			}
			out[method+" "+gomethodPath(p)] = true
		}
	}
	return out
}

// gomethodPath 把 OpenAPI 的 {param} 转回 GoFrame 的 :param。
func gomethodPath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			parts[i] = ":" + strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
		}
	}
	return strings.Join(parts, "/")
}

// TestContractPlannedNotRouted 确认 planned 占位接口没有对应的真实路由。
func TestContractPlannedNotRouted(t *testing.T) {
	got := collectRoutes(t)
	planned := []string{
		"GET /flash-sales",
		"GET /flash-sales/:id",
		"GET /coupons",
		"GET /orders/:id/coupons",
	}
	for _, p := range planned {
		if got[p] {
			t.Errorf("planned 接口 %q 已经实现，必须从 planned 升级为正式契约", p)
		}
	}
}

// TestRouteCountByScope 锁定前台/后台/健康的路由数量分布。
func TestRouteCountByScope(t *testing.T) {
	got := collectRoutes(t)
	frontend, admin, healthCount := 0, 0, 0
	for route := range got {
		switch {
		case route == "GET /health" || route == "GET /ready":
			healthCount++
		case strings.HasPrefix(route, "POST /admin/login") ||
			strings.HasPrefix(route, "POST /admin/logout") ||
			strings.Contains(route, " /admin/") ||
			route == "POST /categories" || route == "PUT /categories/:id" || route == "DELETE /categories/:id":
			admin++
		default:
			frontend++
		}
	}
	if frontend != 67 || admin != 73 || healthCount != 2 {
		t.Errorf("路由分布 frontend=%d admin=%d health=%d，期望 67/73/2", frontend, admin, healthCount)
	}
	if total := frontend + admin + healthCount; total != 142 {
		t.Errorf("常驻路由总数为 %d，期望 142: %s", total, fmt.Sprint(got))
	}
}
