package product_test

// B1-04-02 集成测试：商品规格组/规格值、SKU 组合生成、商品详情 van-sku 契约。
// 使用真实路由 + 真实 MySQL/Redis（与 product_test.go 同一环境假设），不使用 Mock。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gogf/gf/v2/frame/g"
)

type specValueJSON struct {
	Id     int64  `json:"id"`
	SpecId int64  `json:"spec_id"`
	Value  string `json:"value"`
	Sort   int    `json:"sort"`
}

type specGroupJSON struct {
	Id        int64           `json:"id"`
	ProductId int64           `json:"product_id"`
	Name      string          `json:"name"`
	Sort      int             `json:"sort"`
	Values    []specValueJSON `json:"values"`
}

type skuSpecValueJSON struct {
	SpecId      int64  `json:"spec_id"`
	SpecName    string `json:"spec_name"`
	SpecValueId int64  `json:"spec_value_id"`
	Value       string `json:"value"`
	Sort        int    `json:"sort"`
}

type skuDetailJSON struct {
	Id            int64              `json:"id"`
	SkuCode       string             `json:"sku_code"`
	ProductId     int64              `json:"product_id"`
	Name          string             `json:"name"`
	ImageUrl      string             `json:"image_url"`
	Price         int64              `json:"price"`
	OriginalPrice int64              `json:"original_price"`
	Status        string             `json:"status"`
	SpecKey       string             `json:"spec_key"`
	SpecValues    []skuSpecValueJSON `json:"spec_values"`
}

type skuProductValueJSON struct {
	Unique        string `json:"unique"`
	Sku           string `json:"sku"`
	SkuId         int64  `json:"sku_id"`
	SkuName       string `json:"sku_name"`
	Price         int64  `json:"price"`
	OriginalPrice int64  `json:"original_price"`
	Stock         int64  `json:"stock"`
	Image         string `json:"image"`
}

type skuAttrJSON struct {
	AttrName     string   `json:"attrName"`
	AttrValueArr []string `json:"attrValueArr"`
}

type detailContractJSON struct {
	Id           int64                          `json:"id"`
	Status       string                         `json:"status"`
	Skus         []skuDetailJSON                `json:"skus"`
	Attr         []skuAttrJSON                  `json:"attr"`
	ProductValue map[string]skuProductValueJSON `json:"productValue"`
	Specs        []specGroupJSON                `json:"specs"`
}

type generateResJSON struct {
	Created int             `json:"created"`
	Kept    int             `json:"kept"`
	Items   []skuDetailJSON `json:"items"`
}

// cleanupSkuArtifacts 清理本测试产生的 SKU/规格/库存数据（外键顺序：子表先删）。
// 注册顺序晚于 setupProductServer 的 cleanup，因此会先执行，避免 products 被 SKU 外键阻塞。
func cleanupSkuArtifacts(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []string{
			"DELETE FROM inventory_logs",
			"DELETE FROM inventories",
			"DELETE FROM cart_items",
			"DELETE FROM sku_spec_values",
			"DELETE FROM skus",
			"DELETE FROM product_spec_values",
			"DELETE FROM product_specs",
		} {
			if _, err := g.DB().Exec(ctx, q); err != nil {
				t.Logf("cleanup %q: %v", q, err)
			}
		}
	})
}

func createSpecGroup(t *testing.T, base, token string, productID int64, name string, sort int) specGroupJSON {
	t.Helper()
	res := doRequest(t, base, "POST", "/admin/product-specs", map[string]any{
		"product_id": productID, "name": name, "sort": sort,
	}, authHeader(token))
	assertOK(t, res, "create spec group "+name)
	var d specGroupJSON
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal spec group: %v", err)
	}
	return d
}

func createSpecValue(t *testing.T, base, token string, groupID int64, value string, sort int) specValueJSON {
	t.Helper()
	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/product-specs/%d/values", groupID),
		map[string]any{"value": value, "sort": sort}, authHeader(token))
	assertOK(t, res, "create spec value "+value)
	var d specValueJSON
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal spec value: %v", err)
	}
	return d
}

func generateSkus(t *testing.T, base, token string, productID int64, body map[string]any) generateResJSON {
	t.Helper()
	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/skus/generate", productID), body, authHeader(token))
	assertOK(t, res, "generate skus")
	var d generateResJSON
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal generate res: %v", err)
	}
	return d
}

// TestProductSpecCRUDAndRejections 覆盖规格组/规格值的写入、读取、重复、跨商品与权限边界。
func TestProductSpecCRUDAndRejections(t *testing.T) {
	base, token := setupProductServer(t)
	cleanupSkuArtifacts(t)

	categoryID := createCategory(t, base, token, 0, "规格分类", nil)
	productID := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "规格商品", "category_id": categoryID, "price": 100,
	}))

	color := createSpecGroup(t, base, token, productID, "颜色", 1)
	size := createSpecGroup(t, base, token, productID, "尺码", 2)
	red := createSpecValue(t, base, token, color.Id, "红色", 1)
	createSpecValue(t, base, token, color.Id, "蓝色", 2)
	createSpecValue(t, base, token, size.Id, "S", 1)

	// 重复规格组 / 重复规格值 → 409。
	res := doRequest(t, base, "POST", "/admin/product-specs", map[string]any{
		"product_id": productID, "name": "颜色",
	}, authHeader(token))
	if res.Status != 409 || res.Code != 5011 {
		t.Fatalf("duplicate spec group: status=%d code=%d want 409/5011", res.Status, res.Code)
	}
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/product-specs/%d/values", color.Id),
		map[string]any{"value": "红色"}, authHeader(token))
	if res.Status != 409 || res.Code != 5012 {
		t.Fatalf("duplicate spec value: status=%d code=%d want 409/5012", res.Status, res.Code)
	}

	// 跨商品：另一商品可使用同名规格组与同名值（规格为商品级定义）。
	otherProductID := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "规格商品B", "category_id": categoryID, "price": 200,
	}))
	otherColor := createSpecGroup(t, base, token, otherProductID, "颜色", 1)
	createSpecValue(t, base, token, otherColor.Id, "红色", 1)

	// 非法规格组关联：商品不存在 → 404/4001。
	res = doRequest(t, base, "POST", "/admin/product-specs", map[string]any{
		"product_id": int64(9_999_999), "name": "不存在",
	}, authHeader(token))
	if res.Status != 404 || res.Code != 4001 {
		t.Fatalf("spec group on missing product: status=%d code=%d want 404/4001", res.Status, res.Code)
	}

	// 更新规格组与规格值。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/admin/product-specs/%d", color.Id),
		map[string]any{"name": "颜色（新）", "sort": 5}, authHeader(token))
	assertOK(t, res, "update spec group")
	res = doRequest(t, base, "PUT", fmt.Sprintf("/admin/product-spec-values/%d", red.Id),
		map[string]any{"value": "大红", "sort": 3}, authHeader(token))
	assertOK(t, res, "update spec value")

	// 后台详情返回规格定义（含 id，供维护）。
	adminRes := doRequest(t, base, "GET", fmt.Sprintf("/admin/products/%d", productID), nil, authHeader(token))
	assertOK(t, adminRes, "admin detail")
	var adminDetail detailContractJSON
	if err := json.Unmarshal(adminRes.Data, &adminDetail); err != nil {
		t.Fatalf("unmarshal admin detail: %v", err)
	}
	if len(adminDetail.Specs) != 2 {
		t.Fatalf("admin detail specs=%d want 2: %+v", len(adminDetail.Specs), adminDetail.Specs)
	}
	// 排序：更新后 颜色 sort=5 排在 尺码 sort=2 之后（按 sort 升序，不是创建顺序）。
	if adminDetail.Specs[0].Name != "尺码" || adminDetail.Specs[1].Name != "颜色（新）" {
		t.Fatalf("admin detail spec order unexpected: %+v", adminDetail.Specs)
	}
	colorGroup := adminDetail.Specs[1]
	if len(colorGroup.Values) != 2 {
		t.Fatalf("颜色规格值数量=%d want 2", len(colorGroup.Values))
	}
	foundUpdated := false
	for _, v := range colorGroup.Values {
		if v.Value == "大红" && v.Sort == 3 {
			foundUpdated = true
		}
	}
	if !foundUpdated {
		t.Fatalf("spec value update not persisted: %+v", colorGroup.Values)
	}

	// 未认证 → 401；无 spec:create 权限的普通管理员 → 403 且无写入。
	res = doRequest(t, base, "POST", "/admin/product-specs", map[string]any{
		"product_id": productID, "name": "越权",
	}, nil)
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("no token spec create: status=%d code=%d want 401/1002", res.Status, res.Code)
	}
	doRequest(t, base, "POST", "/admin/admins", map[string]any{
		"username": "specnoperm", "password": "specnoperm123",
	}, authHeader(token))
	noPermToken := loginAdmin(t, base, "specnoperm", "specnoperm123")
	res = doRequest(t, base, "POST", "/admin/product-specs", map[string]any{
		"product_id": productID, "name": "越权",
	}, authHeader(noPermToken))
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("no-perm spec create: status=%d code=%d want 403/1003", res.Status, res.Code)
	}
	count, err := g.DB().Model("product_specs").Ctx(context.Background()).Where("name", "越权").Count()
	if err != nil {
		t.Fatalf("count spec groups: %v", err)
	}
	if count != 0 {
		t.Fatalf("403 不应写入规格组，实际 %d 行", count)
	}

	// 删除未被引用的规格值成功；删除规格组级联清理其规格值。
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/admin/product-spec-values/%d", red.Id), nil, authHeader(token))
	assertOK(t, res, "delete unused spec value")
	res = doRequest(t, base, "DELETE", fmt.Sprintf("/admin/product-specs/%d", size.Id), nil, authHeader(token))
	assertOK(t, res, "delete spec group")
	count, err = g.DB().Model("product_spec_values").Ctx(context.Background()).Where("spec_id", size.Id).Count()
	if err != nil {
		t.Fatalf("count values after group delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("删除规格组后其规格值应级联删除，实际 %d 行", count)
	}
}

// TestSkuGenerateCombinations 覆盖笛卡尔积、spec_key 稳定性、幂等/并发唯一与跨商品隔离。
func TestSkuGenerateCombinations(t *testing.T) {
	base, token := setupProductServer(t)
	cleanupSkuArtifacts(t)

	categoryID := createCategory(t, base, token, 0, "组合分类", nil)
	productID := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "组合商品", "category_id": categoryID, "price": 999,
	}))
	color := createSpecGroup(t, base, token, productID, "颜色", 1)
	createSpecValue(t, base, token, color.Id, "红色", 1)
	createSpecValue(t, base, token, color.Id, "蓝色", 2)
	size := createSpecGroup(t, base, token, productID, "尺码", 2)
	for i, v := range []string{"S", "M", "L"} {
		createSpecValue(t, base, token, size.Id, v, i+1)
	}

	first := generateSkus(t, base, token, productID, map[string]any{"price": 1999, "original_price": 2599})
	if first.Created != 6 || first.Kept != 0 || len(first.Items) != 6 {
		t.Fatalf("generate: created=%d kept=%d items=%d want 6/0/6", first.Created, first.Kept, len(first.Items))
	}
	codes := map[string]bool{}
	keys := map[string]bool{}
	for _, item := range first.Items {
		if item.SkuCode == "" || len(item.SkuCode) > 64 {
			t.Fatalf("sku_code 非法: %q", item.SkuCode)
		}
		if codes[item.SkuCode] {
			t.Fatalf("sku_code 重复: %q", item.SkuCode)
		}
		codes[item.SkuCode] = true
		if len(item.SpecKey) != 32 || strings.ToLower(item.SpecKey) != item.SpecKey {
			t.Fatalf("spec_key 应为 32 位小写 hex: %q", item.SpecKey)
		}
		if keys[item.SpecKey] {
			t.Fatalf("spec_key 重复: %q", item.SpecKey)
		}
		keys[item.SpecKey] = true
		if item.Price != 1999 || item.OriginalPrice != 2599 || item.Status != "enabled" {
			t.Fatalf("新建 SKU 默认值不符: %+v", item)
		}
		if len(item.SpecValues) != 2 {
			t.Fatalf("SKU 规格取值数量=%d want 2", len(item.SpecValues))
		}
	}

	// 重复生成：不新增、不更换编码与名称。
	second := generateSkus(t, base, token, productID, nil)
	if second.Created != 0 || second.Kept != 6 || len(second.Items) != 6 {
		t.Fatalf("regenerate: created=%d kept=%d items=%d want 0/6/6", second.Created, second.Kept, len(second.Items))
	}
	for i := range first.Items {
		if first.Items[i].SkuCode != second.Items[i].SkuCode || first.Items[i].Name != second.Items[i].Name {
			t.Fatalf("重新生成不得更换 SKU 身份: %+v -> %+v", first.Items[i], second.Items[i])
		}
	}

	// 并发重新生成：唯一约束兜底，不产生重复 SKU，且全部请求成功（幂等）。
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := request(base, "POST", fmt.Sprintf("/admin/products/%d/skus/generate", productID), nil, authHeader(token))
			if err != nil {
				t.Errorf("concurrent generate: %v", err)
				return
			}
			if res.Status != 200 || res.Code != 0 {
				t.Errorf("concurrent generate: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
			}
		}()
	}
	wg.Wait()
	total, err := g.DB().Model("skus").Ctx(context.Background()).Where("product_id", productID).Count()
	if err != nil {
		t.Fatalf("count skus: %v", err)
	}
	if total != 6 {
		t.Fatalf("并发重新生成后 SKU 总数=%d want 6", total)
	}

	// 生成不建立库存事实（库存由 Inventory 模块按 sku_id 维护，无记录 = 0）。
	skuIDs := make([]int64, 0, len(first.Items))
	for _, item := range first.Items {
		skuIDs = append(skuIDs, item.Id)
	}
	inventoryRows, err := g.DB().Model("inventories").Ctx(context.Background()).WhereIn("sku_id", skuIDs).Count()
	if err != nil {
		t.Fatalf("count inventories: %v", err)
	}
	if inventoryRows != 0 {
		t.Fatalf("SKU 组合生成不应建立库存记录，实际 %d 行", inventoryRows)
	}

	// 已被购物车引用的 SKU：重新生成不得删除或更换编码。
	cartSku := first.Items[0]
	if _, err := g.DB().Model("cart_items").Ctx(context.Background()).Data(g.Map{
		"user_id": 1, "sku_id": cartSku.Id, "quantity": 1, "price_snapshot": cartSku.Price, "selected": 1,
	}).Insert(); err != nil {
		t.Fatalf("insert cart item: %v", err)
	}
	generateSkus(t, base, token, productID, nil)
	stillThere, err := g.DB().Model("skus").Ctx(context.Background()).Where("id", cartSku.Id).Count()
	if err != nil {
		t.Fatalf("count cart sku: %v", err)
	}
	if stillThere != 1 {
		t.Fatalf("重新生成组合不得删除被购物车引用的 SKU")
	}

	// 不同商品的相同规格组合互不冲突（spec_key 作用域为商品）。
	otherProductID := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "组合商品B", "category_id": categoryID, "price": 888,
	}))
	otherColor := createSpecGroup(t, base, token, otherProductID, "颜色", 1)
	createSpecValue(t, base, token, otherColor.Id, "红色", 1)
	createSpecValue(t, base, token, otherColor.Id, "蓝色", 2)
	otherSize := createSpecGroup(t, base, token, otherProductID, "尺码", 2)
	for i, v := range []string{"S", "M", "L"} {
		createSpecValue(t, base, token, otherSize.Id, v, i+1)
	}
	other := generateSkus(t, base, token, otherProductID, nil)
	if other.Created != 6 {
		t.Fatalf("另一商品同规格组合应生成 6 个 SKU，实际 created=%d", other.Created)
	}

	// 无规格商品：不能生成组合（400/5015），应按 POST /admin/skus 创建单 SKU。
	noSpecProductID := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "无规格商品", "category_id": categoryID, "price": 500,
	}))
	res := doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/skus/generate", noSpecProductID), nil, authHeader(token))
	if res.Status != 400 || res.Code != 5015 {
		t.Fatalf("generate without specs: status=%d code=%d want 400/5015", res.Status, res.Code)
	}
}

// TestProductDetailVanSkuContract 覆盖前台详情 van-sku 契约、库存/价格、可见性与无规格兼容。
func TestProductDetailVanSkuContract(t *testing.T) {
	base, token := setupProductServer(t)
	cleanupSkuArtifacts(t)

	categoryID := createCategory(t, base, token, 0, "详情分类", nil)
	productID := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "详情商品", "category_id": categoryID, "price": 1000, "main_image": "http://img.example/main.png",
	}))
	color := createSpecGroup(t, base, token, productID, "颜色", 1)
	createSpecValue(t, base, token, color.Id, "红色", 1)
	createSpecValue(t, base, token, color.Id, "蓝色", 2)
	size := createSpecGroup(t, base, token, productID, "尺码", 2)
	createSpecValue(t, base, token, size.Id, "S", 1)
	createSpecValue(t, base, token, size.Id, "M", 2)

	generated := generateSkus(t, base, token, productID, map[string]any{"price": 1000, "original_price": 1500})
	if generated.Created != 4 {
		t.Fatalf("generate 2x2: created=%d want 4", generated.Created)
	}

	// 调整其中一个 SKU 的售价/原价与库存，验证详情返回的是服务端权威值。
	target := generated.Items[0]
	res := doRequest(t, base, "PUT", fmt.Sprintf("/admin/skus/%d", target.Id),
		map[string]any{"price": 1200, "original_price": 1600}, authHeader(token))
	assertOK(t, res, "update sku price")
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/inventories/%d/increase", target.Id),
		map[string]any{"quantity": 7}, authHeader(token))
	assertOK(t, res, "increase inventory")

	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", productID), nil, authHeader(token))
	assertOK(t, res, "on shelf")

	detailRes := doRequest(t, base, "GET", fmt.Sprintf("/products/%d", productID), nil, nil)
	assertOK(t, detailRes, "public detail")
	var detail detailContractJSON
	if err := json.Unmarshal(detailRes.Data, &detail); err != nil {
		t.Fatalf("unmarshal public detail: %v", err)
	}

	if len(detail.Attr) != 2 {
		t.Fatalf("attr 数量=%d want 2", len(detail.Attr))
	}
	if detail.Attr[0].AttrName != "颜色" || strings.Join(detail.Attr[0].AttrValueArr, ",") != "红色,蓝色" {
		t.Fatalf("attr[0] 不符: %+v", detail.Attr[0])
	}
	if detail.Attr[1].AttrName != "尺码" || strings.Join(detail.Attr[1].AttrValueArr, ",") != "S,M" {
		t.Fatalf("attr[1] 不符: %+v", detail.Attr[1])
	}
	if len(detail.ProductValue) != 4 {
		t.Fatalf("productValue 数量=%d want 4", len(detail.ProductValue))
	}
	if len(detail.Skus) != 4 {
		t.Fatalf("skus 数量=%d want 4", len(detail.Skus))
	}

	// 键为按 attr 顺序逗号连接的规格值文本。
	hit := false
	for key, pv := range detail.ProductValue {
		if pv.SkuId != target.Id {
			continue
		}
		hit = true
		if key != "红色,S" {
			t.Fatalf("productValue 键=%q want %q", key, "红色,S")
		}
		if pv.Unique != target.SkuCode {
			t.Fatalf("productValue.unique=%q want %q", pv.Unique, target.SkuCode)
		}
		if pv.Sku != "红色,S" {
			t.Fatalf("productValue.sku=%q want %q", pv.Sku, "红色,S")
		}
		if pv.Price != 1200 || pv.OriginalPrice != 1600 {
			t.Fatalf("productValue 价格不符: %+v", pv)
		}
		if pv.Stock != 7 {
			t.Fatalf("productValue.stock=%d want 7", pv.Stock)
		}
		if pv.SkuName == "" {
			t.Fatalf("productValue.sku_name 不应为空")
		}
	}
	if !hit {
		t.Fatalf("productValue 未包含目标 SKU %d", target.Id)
	}
	for _, sku := range detail.Skus {
		if sku.SkuCode == "" || len(sku.SpecValues) != 2 {
			t.Fatalf("skus 元素缺少编码或规格取值: %+v", sku)
		}
	}

	// 不泄露内部敏感字段（成本价等）。
	if strings.Contains(string(detailRes.Data), "cost") {
		t.Fatalf("详情响应不应包含成本价字段: %s", string(detailRes.Data))
	}

	// 停用 SKU：前台详情不返回该 SKU，productValue 同步收敛。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/admin/skus/%d", target.Id),
		map[string]any{"status": "disabled"}, authHeader(token))
	assertOK(t, res, "disable sku")
	detailRes = doRequest(t, base, "GET", fmt.Sprintf("/products/%d", productID), nil, nil)
	assertOK(t, detailRes, "public detail after disable")
	detail = detailContractJSON{}
	if err := json.Unmarshal(detailRes.Data, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if len(detail.Skus) != 3 || len(detail.ProductValue) != 3 {
		t.Fatalf("停用 SKU 后: skus=%d productValue=%d want 3/3", len(detail.Skus), len(detail.ProductValue))
	}

	// 无规格商品：attr 为空数组、productValue 为空对象（不是 null），SKU 仍可正常返回。
	noSpecProductID := getProductID(t, createProduct(t, base, token, map[string]any{
		"name": "无规格详情商品", "category_id": categoryID, "price": 500,
	}))
	noSpecSku := doRequest(t, base, "POST", "/admin/skus", map[string]any{
		"product_id": noSpecProductID, "name": "默认", "price": 500,
	}, authHeader(token))
	assertOK(t, noSpecSku, "create no-spec sku")
	var noSpecSkuJSON skuDetailJSON
	if err := json.Unmarshal(noSpecSku.Data, &noSpecSkuJSON); err != nil {
		t.Fatalf("unmarshal no-spec sku: %v", err)
	}
	if noSpecSkuJSON.SkuCode == "" || noSpecSkuJSON.OriginalPrice != 500 || noSpecSkuJSON.SpecKey != "" {
		t.Fatalf("无规格 SKU 默认值不符: %+v", noSpecSkuJSON)
	}
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/on-shelf", noSpecProductID), nil, authHeader(token))
	assertOK(t, res, "on shelf no-spec product")

	noSpecDetail := doRequest(t, base, "GET", fmt.Sprintf("/products/%d", noSpecProductID), nil, nil)
	assertOK(t, noSpecDetail, "no-spec detail")
	noSpecRaw := string(noSpecDetail.Data)
	if !strings.Contains(noSpecRaw, `"attr":[]`) {
		t.Fatalf("无规格商品 attr 应为空数组，实际: %s", noSpecRaw)
	}
	if !strings.Contains(noSpecRaw, `"productValue":{}`) {
		t.Fatalf("无规格商品 productValue 应为空对象，实际: %s", noSpecRaw)
	}
	var noSpecParsed detailContractJSON
	if err := json.Unmarshal(noSpecDetail.Data, &noSpecParsed); err != nil {
		t.Fatalf("unmarshal no-spec detail: %v", err)
	}
	if len(noSpecParsed.Skus) != 1 || noSpecParsed.Skus[0].SkuCode != noSpecSkuJSON.SkuCode {
		t.Fatalf("无规格商品详情 SKU 不符: %+v", noSpecParsed.Skus)
	}

	// 下架商品前台详情 → 404/4001（保持既有可见性规则）。
	res = doRequest(t, base, "POST", fmt.Sprintf("/admin/products/%d/off-shelf", productID), nil, authHeader(token))
	assertOK(t, res, "off shelf")
	res = doRequest(t, base, "GET", fmt.Sprintf("/products/%d", productID), nil, nil)
	if res.Status != 404 || res.Code != 4001 {
		t.Fatalf("off-shelf detail: status=%d code=%d want 404/4001", res.Status, res.Code)
	}
}
