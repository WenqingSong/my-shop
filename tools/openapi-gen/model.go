package main

import (
	"fmt"
	"sort"
	"strings"
)

// TypeKind 是 OpenAPI 字段类型的来源分类。
type TypeKind int

const (
	KindScalar TypeKind = iota
	KindRef
	KindArray
	KindAny
)

// TypeRef 是 Go AST 类型到契约类型的中间表示。
type TypeRef struct {
	Kind    TypeKind
	Scalar  string // string/int/int64/uint/bool/float/jsonnumber/gtime/time/any
	Ref     string // 引用的 Schema 名
	Elem    *TypeRef
	Pointer bool // 可空（JSON null）
}

// Field 是 DTO 结构体字段的契约事实。
type Field struct {
	GoName    string
	JSONName  string
	Dc        string
	V         []string
	In        string
	Default   string
	Type      *TypeRef
	OmitEmpty bool
	Embedded  bool
	Line      int
}

// Required 表示字段是否参与 v:"required"。
func (f *Field) Required() bool {
	for _, r := range f.V {
		if r == "required" {
			return true
		}
	}
	return false
}

// EnumValues 返回 v:"in:a,b" 声明的取值（只做机械提取，不做业务推断）。
func (f *Field) EnumValues() []string {
	for _, r := range f.V {
		if strings.HasPrefix(r, "in:") {
			raw := strings.TrimPrefix(r, "in:")
			if raw == "" {
				return nil
			}
			return strings.Split(raw, ",")
		}
	}
	return nil
}

// StructMeta 是 g.Meta 中的路由元数据（仅 Req 结构体具备）。
type StructMeta struct {
	Path    string
	Method  string
	Tags    string
	Summary string
}

// StructDef 是一个 DTO 结构体。
type StructDef struct {
	Domain     string
	Name       string
	SchemaName string
	Doc        string
	Meta       StructMeta
	Fields     []*Field
	// Shape 为 request / response。请求 DTO 的 required 由 v:"required" 决定，
	// 响应 DTO 的 required 由 Go 序列化行为（非 omitempty 即必出现）决定。
	Shape      string
	SourceFile string
	Line       int
}

// API 是一条实际注册的路由及其契约元数据。
type API struct {
	Method      string
	Path        string
	Handler     string
	Domain      string
	Auth        string // public | user | admin
	AuthMode    string // "" | signature_only
	Permission  string
	OperationID string
	Summary     string
	Tags        string
	Req         *StructDef
	Res         *StructDef
	Planned     bool
	Conditional bool
	Source      string
}

// Key 返回 "METHOD /path"，与实际路由表口径一致。
func (a *API) Key() string { return a.Method + " " + a.Path }

// DomainInfo 是 API 业务域到契约命名的映射。
type DomainInfo struct {
	Name   string // api/<name>/v1
	Prefix string // Schema 前缀
	Camel  string // operationId 前缀
}

var domains = map[string]DomainInfo{
	"address":        {Name: "address", Prefix: "Address", Camel: "address"},
	"admin":          {Name: "admin", Prefix: "Admin", Camel: "admin"},
	"article":        {Name: "article", Prefix: "Article", Camel: "article"},
	"banner":         {Name: "banner", Prefix: "Banner", Camel: "banner"},
	"cart":           {Name: "cart", Prefix: "Cart", Camel: "cart"},
	"categories":     {Name: "categories", Prefix: "Category", Camel: "categories"},
	"coupon":         {Name: "coupon", Prefix: "Coupon", Camel: "coupon"},
	"dashboard":      {Name: "dashboard", Prefix: "Dashboard", Camel: "dashboard"},
	"favorite":       {Name: "favorite", Prefix: "Favorite", Camel: "favorite"},
	"flashsale":      {Name: "flashsale", Prefix: "FlashSale", Camel: "flashSale"},
	"health":         {Name: "health", Prefix: "Health", Camel: "health"},
	"iam":            {Name: "iam", Prefix: "User", Camel: "user"},
	"inventory":      {Name: "inventory", Prefix: "Inventory", Camel: "inventory"},
	"like":           {Name: "like", Prefix: "Like", Camel: "like"},
	"order":          {Name: "order", Prefix: "Order", Camel: "order"},
	"product":        {Name: "product", Prefix: "Product", Camel: "product"},
	"recommendation": {Name: "recommendation", Prefix: "Recommend", Camel: "recommend"},
	"refund":         {Name: "refund", Prefix: "Refund", Camel: "refund"},
	"review":         {Name: "review", Prefix: "Review", Camel: "review"},
	"sku":            {Name: "sku", Prefix: "Sku", Camel: "sku"},
	"upload":         {Name: "upload", Prefix: "Upload", Camel: "upload"},
}

// entitySchemaNames 为业务实体指定稳定 Schema 名。
// 未登记的类型由 Req/Res 后缀规则推导；既非实体也未登记的命名一律报错，
// 避免出现不稳定的包路径名或静默猜测。
var entitySchemaNames = map[string]string{
	"address.Address":             "Address",
	"admin.Role":                  "AdminRole",
	"admin.Permission":            "Permission",
	"article.Article":             "Article",
	"article.ArticleItem":         "ArticleItem",
	"banner.BannerItem":           "BannerItem",
	"banner.Banner":               "Banner",
	"cart.CartItem":               "CartItem",
	"categories.Category":         "Category",
	"coupon.Template":             "CouponTemplate",
	"coupon.UserCoupon":           "UserCoupon",
	"dashboard.TrendItem":         "DashboardTrendItem",
	"dashboard.StatusItem":        "DashboardStatusItem",
	"dashboard.TopItem":           "DashboardTopItem",
	"dashboard.ActivityStatus":    "DashboardActivityStatus",
	"dashboard.RequestStatus":     "DashboardRequestStatus",
	"favorite.FavoriteItem":       "FavoriteItem",
	"flashsale.ActivitySku":       "FlashSaleActivitySku",
	"flashsale.Activity":          "FlashSaleActivity",
	"flashsale.FlashOrder":        "FlashSaleOrder",
	"flashsale.ActivitySkuInput":  "FlashSaleActivitySkuInput",
	"flashsale.RequestAudit":      "FlashSaleRequestAudit",
	"iam.Session":                 "UserSession",
	"inventory.Inventory":         "Inventory",
	"inventory.Log":               "InventoryLog",
	"order.Order":                 "Order",
	"order.OrderItem":             "OrderItem",
	"order.AdminOrderListItem":    "AdminOrderListItem",
	"order.AdminOrderStatsItem":   "OrderStatsItem",
	"product.Product":             "Product",
	"recommendation.ItemSnapshot": "RecommendItemSnapshot",
	"recommendation.Item":         "RecommendItem",
	"recommendation.Position":     "RecommendPosition",
	"refund.Refund":               "Refund",
	"refund.RefundAudit":          "RefundAudit",
	"review.ReviewItem":           "ReviewItem",
	"review.Review":               "Review",
	"sku.Sku":                     "Sku",
}

// explicitSchemaOverrides 覆盖 Req/Res 规则推导结果，消除重复词。
var explicitSchemaOverrides = map[string]string{
	"order.AdminOrderStatsRes":  "OrderStatsResponse",
	"order.AdminOrderDetailRes": "OrderAdminDetailResponse",
}

// schemaNameFor 返回结构体的稳定业务 Schema 名。
func schemaNameFor(domain, typeName string) (string, error) {
	key := domain + "." + typeName
	if n, ok := explicitSchemaOverrides[key]; ok {
		return n, nil
	}
	if n, ok := entitySchemaNames[key]; ok {
		return n, nil
	}
	d, ok := domains[domain]
	if !ok {
		return "", fmt.Errorf("未登记的 API 业务域 %q（类型 %s）", domain, typeName)
	}
	switch {
	case strings.HasSuffix(typeName, "Req"):
		base := strings.TrimSuffix(typeName, "Req")
		if base == "" {
			return "", fmt.Errorf("%s 无法推导 Schema 名：Req 基名为空", key)
		}
		return d.Prefix + base + "Request", nil
	case strings.HasSuffix(typeName, "Res"):
		base := strings.TrimSuffix(typeName, "Res")
		if base == "" {
			return "", fmt.Errorf("%s 无法推导 Schema 名：Res 基名为空", key)
		}
		return d.Prefix + base + "Response", nil
	}
	return "", fmt.Errorf(
		"%s 未登记稳定 Schema 名；请在 tools/openapi-gen/model.go 的 entitySchemaNames 中补充，"+
			"禁止使用包路径名或猜测命名", key)
}

// operationIDFor 生成稳定且唯一的 operationId。
func operationIDFor(domain, handler string) (string, error) {
	d, ok := domains[domain]
	if !ok {
		return "", fmt.Errorf("未登记的 API 业务域 %q", domain)
	}
	if handler == "" {
		return "", fmt.Errorf("业务域 %q 的路由缺少 handler 名", domain)
	}
	return d.Camel + handler, nil
}

// sortedKeys 返回 map 的有序键，保证生成结果确定性。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
