package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return abs
}

func TestSchemaNameFor(t *testing.T) {
	cases := []struct {
		domain, typ, want string
	}{
		{"order", "Order", "Order"},
		{"order", "OrderItem", "OrderItem"},
		{"order", "CreateReq", "OrderCreateRequest"},
		{"order", "DetailRes", "OrderDetailResponse"},
		{"admin", "Role", "AdminRole"},
		{"iam", "MeRes", "UserMeResponse"},
		{"iam", "Session", "UserSession"},
		{"flashsale", "Activity", "FlashSaleActivity"},
		{"recommendation", "Position", "RecommendPosition"},
		{"inventory", "Log", "InventoryLog"},
		{"health", "LivenessRes", "HealthLivenessResponse"},
		{"upload", "TokenRes", "UploadTokenResponse"},
	}
	for _, c := range cases {
		got, err := schemaNameFor(c.domain, c.typ)
		if err != nil {
			t.Fatalf("schemaNameFor(%s,%s): %v", c.domain, c.typ, err)
		}
		if got != c.want {
			t.Errorf("schemaNameFor(%s,%s) = %q, want %q", c.domain, c.typ, got, c.want)
		}
	}

	if _, err := schemaNameFor("order", "UnknownThing"); err == nil {
		t.Fatal("未登记的非 Req/Res 类型必须报错，不得猜测命名")
	}
	if _, err := schemaNameFor("nosuchdomain", "XReq"); err == nil {
		t.Fatal("未登记的业务域必须报错")
	}
}

func TestParseDTOsCoversAllAPIPackages(t *testing.T) {
	defs, order, err := parseDTOs(repoRoot(t))
	if err != nil {
		t.Fatalf("parseDTOs: %v", err)
	}
	if len(defs) != len(order) {
		t.Fatalf("defs=%d order=%d，二者必须一致", len(defs), len(order))
	}
	if len(defs) == 0 {
		t.Fatal("未解析到任何 DTO")
	}

	seen := map[string]string{}
	for key, def := range defs {
		if prev, ok := seen[def.SchemaName]; ok {
			t.Fatalf("Schema 名冲突：%s 与 %s 都是 %q", prev, key, def.SchemaName)
		}
		seen[def.SchemaName] = key
		if def.SchemaName == "" {
			t.Fatalf("%s 的 Schema 名为空", key)
		}
		if strings.Contains(def.SchemaName, "/") {
			t.Fatalf("%s 的 Schema 名包含包路径：%q", key, def.SchemaName)
		}
	}

	product, ok := defs["product.Product"]
	if !ok {
		t.Fatal("缺少 product.Product")
	}
	if product.SchemaName != "Product" {
		t.Errorf("product.Product 的 Schema 名 = %q", product.SchemaName)
	}
	if len(product.Fields) != 12 {
		t.Errorf("Product 字段数 = %d, want 12", len(product.Fields))
	}

	var createdAt, price, status *Field
	for _, f := range product.Fields {
		switch f.JSONName {
		case "created_at":
			createdAt = f
		case "price":
			price = f
		case "status":
			status = f
		}
	}
	if createdAt == nil || !createdAt.Type.Pointer {
		t.Error("Product.created_at 必须为可空 *gtime.Time")
	}
	if createdAt == nil || createdAt.Type.Scalar != "gtime" {
		t.Error("Product.created_at 必须映射为 gtime")
	}
	if price == nil || !isMoneyField(price) {
		t.Error("Product.price 必须识别为金额（整数分）")
	}
	if status == nil {
		t.Fatal("缺少 Product.status")
	}
	if _, ok := enumRegistry["Product.status"]; !ok {
		t.Error("Product.status 必须登记枚举")
	}
}

func TestBuildSchemasStructure(t *testing.T) {
	defs, order, err := parseDTOs(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := buildSchemas(defs, order)
	if err != nil {
		t.Fatal(err)
	}

	raw, ok := schemas.Get("Product")
	if !ok {
		t.Fatal("缺少 Product schema")
	}
	product := raw.(*OMap)
	if v, _ := product.Get("type"); v != "object" {
		t.Errorf("Product.type = %v", v)
	}
	propsAny, ok := product.Get("properties")
	if !ok {
		t.Fatal("Product 缺少 properties")
	}
	props := propsAny.(*OMap)
	priceAny, ok := props.Get("price")
	if !ok {
		t.Fatal("Product.price 缺失")
	}
	priceSchema := priceAny.(*OMap)
	if v, _ := priceSchema.Get("x-unit"); v != "cent" {
		t.Errorf("Product.price 的 x-unit = %v, want cent", v)
	}

	createdAny, _ := props.Get("created_at")
	createdSchema := createdAny.(*OMap)
	typAny, _ := createdSchema.Get("type")
	typList, ok := typAny.(FlowList)
	if !ok || len(typList) != 2 || typList[1] != "null" {
		t.Errorf("Product.created_at 的 type 必须是 [string, null]，实际 %v", typAny)
	}

	// 嵌入结构必须用同文件相对引用（schemas.yaml 内部）。
	detailAny, ok := schemas.Get("ProductDetailResponse")
	if !ok {
		t.Fatal("缺少 ProductDetailResponse")
	}
	if got := detailAny.(*OMap).Keys(); !contains(got, "allOf") {
		t.Errorf("ProductDetailResponse 必须包含 allOf（嵌入 Product），实际键 %v", got)
	}
}

func TestPathHelpers(t *testing.T) {
	if got := toOpenAPIPath("/admin/inventories/:sku_id/logs"); got != "/admin/inventories/{sku_id}/logs" {
		t.Errorf("toOpenAPIPath = %q", got)
	}
	if got := pathToGoFrame("/admin/inventories/{sku_id}/logs"); got != "/admin/inventories/:sku_id/logs" {
		t.Errorf("pathToGoFrame = %q", got)
	}
	names := pathParamNames("/admin/roles/:id/permissions/:permission_id")
	if len(names) != 2 || names[0] != "id" || names[1] != "permission_id" {
		t.Errorf("pathParamNames = %v", names)
	}
}

func TestResolveJSONPointer(t *testing.T) {
	doc := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{"Product": "ok"},
		},
		"a/b": map[string]any{"~x": 1},
	}
	if v, err := resolveJSONPointer(doc, "/components/schemas/Product"); err != nil || v != "ok" {
		t.Errorf("解析失败: %v %v", v, err)
	}
	if _, err := resolveJSONPointer(doc, "/components/schemas/Missing"); err == nil {
		t.Error("缺失键必须报错")
	}
	if v, err := resolveJSONPointer(doc, "/a~1b/~0x"); err != nil || v != 1 {
		t.Errorf("转义解析失败: %v %v", v, err)
	}
}

func TestParseErrorCodes(t *testing.T) {
	list, err := parseErrorCodes(repoRoot(t))
	if err != nil {
		t.Fatalf("parseErrorCodes: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("未解析到错误码")
	}
	seen := map[int]bool{}
	for _, c := range list {
		if seen[c.Code] {
			t.Fatalf("错误码 %d 重复", c.Code)
		}
		seen[c.Code] = true
		if c.Message == "" {
			t.Errorf("错误码 %d 缺少 message", c.Code)
		}
		if c.HTTP == 0 {
			t.Errorf("错误码 %d 缺少 HTTP 状态", c.Code)
		}
		if c.Domain == "unknown" {
			t.Errorf("错误码 %s 未能归入业务域", c.Constant)
		}
	}
	if !seen[0] || !seen[1001] || !seen[9001] {
		t.Error("错误码集合缺少关键码（0/1001/9001）")
	}
}

func TestParseRouteFilesAndControllers(t *testing.T) {
	root := repoRoot(t)
	routes, err := parseRouteFiles(root)
	if err != nil {
		t.Fatalf("parseRouteFiles: %v", err)
	}
	if len(routes) != 140 {
		t.Errorf("routes_*.go 注册路由数 = %d, want 140（142 减去自动绑定的 2 条 health）", len(routes))
	}
	cms, err := parseControllers(root)
	if err != nil {
		t.Fatalf("parseControllers: %v", err)
	}
	for _, r := range routes {
		if _, ok := cms[r.Domain+"."+r.Handler]; !ok {
			t.Errorf("%s %s 的 handler %s.%s 在 controller 中不存在", r.Method, r.Path, r.Domain, r.Handler)
		}
		if r.Auth == "admin" && r.Permission != "" && !strings.Contains(r.Permission, ":") {
			t.Errorf("%s %s 的权限码格式非法：%q", r.Method, r.Path, r.Permission)
		}
	}
}
