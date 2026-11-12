package e2e

// B1-04-02 下单服务端定价端到端测试：
//   - 订单按 sku_code 定位 SKU，成交价只取 skus.price；
//   - 客户端伪造/篡改单价被拒绝且不产生任何写入；
//   - SKU 编码不存在、编码与 id 不匹配、SKU 停用、商品下架均被拒绝；
//   - 订单行保存 sku_code / spec_text / original_price 快照；
//   - 无规格 SKU 正常下单；库存不足沿用 6001 且事务整体回滚；
//   - 购物车链路不接受客户端价格，price_snapshot 只取服务端 SKU 价格。

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/gogf/gf/v2/frame/g"
)

// orderInsertSpecSku 写入一个带规格取值的可售 SKU（含 sku_code、原价与组合指纹）。
// specs 为 (规格组名, 规格值文本) 列表，规格组/值不存在时自动创建。
func orderInsertSpecSku(
	t *testing.T,
	productID int64,
	code, skuName string,
	price, originalPrice int64,
	status int,
	specs [][2]string,
) int64 {
	t.Helper()
	ctx := context.Background()

	signature := md5.Sum([]byte(code))
	skuID, err := g.DB().Model("skus").Ctx(ctx).Data(g.Map{
		"sku_code":       code,
		"product_id":     productID,
		"name":           skuName,
		"price":          price,
		"original_price": originalPrice,
		"status":         status,
		"spec_signature": hex.EncodeToString(signature[:]),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert spec sku %s: %v", code, err)
	}

	for _, pair := range specs {
		groupName, value := pair[0], pair[1]
		var groupID int64
		if v, e := g.DB().Model("product_specs").Ctx(ctx).
			Fields("id").Where("product_id", productID).Where("name", groupName).Value(); e != nil {
			t.Fatalf("query spec group: %v", e)
		} else if v != nil && !v.IsEmpty() {
			groupID = v.Int64()
		} else if id, e := g.DB().Model("product_specs").Ctx(ctx).Data(g.Map{
			"product_id": productID, "name": groupName, "sort": 0,
		}).InsertAndGetId(); e != nil {
			t.Fatalf("insert spec group: %v", e)
		} else {
			groupID = id
		}

		var valueID int64
		if v, e := g.DB().Model("product_spec_values").Ctx(ctx).
			Fields("id").Where("spec_id", groupID).Where("value", value).Value(); e != nil {
			t.Fatalf("query spec value: %v", e)
		} else if v != nil && !v.IsEmpty() {
			valueID = v.Int64()
		} else if id, e := g.DB().Model("product_spec_values").Ctx(ctx).Data(g.Map{
			"spec_id": groupID, "value": value, "sort": 0,
		}).InsertAndGetId(); e != nil {
			t.Fatalf("insert spec value: %v", e)
		} else {
			valueID = id
		}

		if _, e := g.DB().Model("sku_spec_values").Ctx(ctx).Data(g.Map{
			"sku_id": skuID, "spec_id": groupID, "spec_value_id": valueID,
		}).Insert(); e != nil {
			t.Fatalf("insert sku spec value: %v", e)
		}
	}
	return skuID
}

// orderSkuCode 返回 SKU 的编码（用于断言）。
func orderSkuCode(t *testing.T, skuID int64) string {
	t.Helper()
	v, err := g.DB().Model("skus").Ctx(context.Background()).Fields("sku_code").Where("id", skuID).Value()
	if err != nil {
		t.Fatalf("query sku code: %v", err)
	}
	if v == nil {
		t.Fatalf("sku %d not found", skuID)
	}
	return v.String()
}

// orderUserWithAddress 建立前台用户、登录并写入收货地址，返回 (token, userID, addressID)。
func orderUserWithAddress(t *testing.T, base, username string) (string, int64, int64) {
	t.Helper()
	isoInsertUser(t, username, "buyerpass123")
	token, _ := isoFrontendLogin(t, base, username, "buyerpass123")
	userID, err := parseUserID(t, isoClaims(t, token).Subject)
	if err != nil {
		t.Fatalf("parse user id: %v", err)
	}
	return token, userID, orderInsertAddress(t, userID)
}

// TestOrderPricingBySkuCode 覆盖正常按 sku_code 下单：服务端定价 + 快照 + 幂等。
func TestOrderPricingBySkuCode(t *testing.T) {
	base := setupOrderServer(t)
	categoryID := orderInsertCategory(t, "定价分类")
	productID := orderInsertProduct(t, categoryID, "定价商品", 1)
	skuID := orderInsertSpecSku(t, productID, "SKU-PRICE-001", "红色,S", 5000, 8000, 1,
		[][2]string{{"颜色", "红色"}, {"尺码", "S"}})
	orderInsertInventory(t, skuID, 10)

	token, userID, addressID := orderUserWithAddress(t, base, "pricingbuyer")

	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-code-1",
		"sku_code": "SKU-PRICE-001", "quantity": 2,
	})
	if env.Status != 200 || env.Code != 0 || env.Data == nil {
		t.Fatalf("order by sku_code: status=%d code=%d msg=%q", env.Status, env.Code, env.Message)
	}
	if env.Data.TotalAmount != 10000 {
		t.Fatalf("total_amount=%d want 10000（服务端 skus.price×qty）", env.Data.TotalAmount)
	}
	item := env.Data.Items[0]
	if item.SkuId != skuID || item.SkuCode != "SKU-PRICE-001" {
		t.Fatalf("item sku 标识不符: %+v", item)
	}
	if item.Price != 5000 || item.OriginalPrice != 8000 {
		t.Fatalf("item 价格快照不符: price=%d original_price=%d", item.Price, item.OriginalPrice)
	}
	if item.SpecText != "红色,S" {
		t.Fatalf("item spec_text=%q want 红色,S", item.SpecText)
	}
	if got := orderStock(t, skuID); got != 8 {
		t.Fatalf("库存扣减后=%d want 8", got)
	}

	// 客户端提交与服务端一致的单价：允许（不参与计算），幂等键相同 → 返回同一订单。
	envSame := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-code-1",
		"sku_code": "SKU-PRICE-001", "quantity": 2, "price": 5000,
	})
	if envSame.Status != 200 || envSame.Code != 0 || envSame.Data.Id != env.Data.Id {
		t.Fatalf("idempotent replay: status=%d code=%d id=%v want %d", envSame.Status, envSame.Code, envSame.Data, env.Data.Id)
	}
	if n := orderCount(t, userID); n != 1 {
		t.Fatalf("幂等重放不应新增订单，实际 %d 单", n)
	}
	if got := orderStock(t, skuID); got != 8 {
		t.Fatalf("幂等重放不应重复扣库存，stock=%d want 8", got)
	}

	// 同一 SKU 也可用 sku_id + sku_code 同时提交（必须一致）。
	envBoth := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-code-2",
		"sku_id": skuID, "sku_code": "SKU-PRICE-001", "quantity": 1,
	})
	if envBoth.Status != 200 || envBoth.Code != 0 {
		t.Fatalf("sku_id + sku_code: status=%d code=%d msg=%q", envBoth.Status, envBoth.Code, envBoth.Message)
	}
}

// TestOrderRejectsForgedPrice 覆盖客户端伪造/篡改单价：拒绝且无订单、无库存变化。
func TestOrderRejectsForgedPrice(t *testing.T) {
	base := setupOrderServer(t)
	categoryID := orderInsertCategory(t, "伪价分类")
	productID := orderInsertProduct(t, categoryID, "伪价商品", 1)
	skuID := orderInsertSpecSku(t, productID, "SKU-PRICE-002", "蓝色,M", 5000, 7000, 1,
		[][2]string{{"颜色", "蓝色"}, {"尺码", "M"}})
	orderInsertInventory(t, skuID, 10)

	token, userID, addressID := orderUserWithAddress(t, base, "forger")

	for _, forged := range []int64{1, 0, 4999, 5001, 999999} {
		env := orderCall(t, base, "POST", "/orders", token, map[string]any{
			"source": "direct", "address_id": addressID,
			"idempotency_key": fmt.Sprintf("key-forged-%d", forged),
			"sku_code":        "SKU-PRICE-002", "quantity": 1, "price": forged,
		})
		if env.Status != 409 || env.Code != 9007 {
			t.Fatalf("forged price %d: status=%d code=%d want 409/9007", forged, env.Status, env.Code)
		}
	}
	if n := orderCount(t, userID); n != 0 {
		t.Fatalf("伪造价格不应创建订单，实际 %d 单", n)
	}
	if got := orderStock(t, skuID); got != 10 {
		t.Fatalf("伪造价格不应扣库存，stock=%d want 10", got)
	}

	// 购物车下单不接受客户端单价（多行订单没有单一客户端单价语义）。
	orderInsertCartItem(t, userID, skuID, 1, true)
	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "cart", "address_id": addressID, "idempotency_key": "key-cart-price", "price": 1,
	})
	if env.Status != 409 || env.Code != 9007 {
		t.Fatalf("cart with client price: status=%d code=%d want 409/9007", env.Status, env.Code)
	}
	if n := orderCount(t, userID); n != 0 {
		t.Fatalf("购物车伪造价格不应创建订单，实际 %d 单", n)
	}
	if got := orderStock(t, skuID); got != 10 {
		t.Fatalf("购物车伪造价格不应扣库存，stock=%d want 10", got)
	}
}

// TestOrderRejectsInvalidSkuCode 覆盖不存在编码、编码与 id 不匹配、SKU 停用、商品下架。
func TestOrderRejectsInvalidSkuCode(t *testing.T) {
	base := setupOrderServer(t)
	categoryID := orderInsertCategory(t, "非法编码分类")
	productID := orderInsertProduct(t, categoryID, "非法编码商品", 1)
	skuA := orderInsertSpecSku(t, productID, "SKU-CODE-A", "A", 3000, 3000, 1, nil)
	skuB := orderInsertSpecSku(t, productID, "SKU-CODE-B", "B", 4000, 4000, 1, nil)
	orderInsertInventory(t, skuA, 10)
	orderInsertInventory(t, skuB, 10)

	token, userID, addressID := orderUserWithAddress(t, base, "invalidbuyer")

	// 不存在的 sku_code → 404/5001。
	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-bad-1",
		"sku_code": "SKU-NOT-EXIST", "quantity": 1,
	})
	if env.Status != 404 || env.Code != 5001 {
		t.Fatalf("missing sku_code: status=%d code=%d want 404/5001", env.Status, env.Code)
	}

	// sku_code 与 sku_id 指向不同 SKU → 拒绝（409/9003）。
	env = orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-bad-2",
		"sku_id": skuA, "sku_code": "SKU-CODE-B", "quantity": 1,
	})
	if env.Status != 409 || env.Code != 9003 {
		t.Fatalf("mismatched sku id/code: status=%d code=%d want 409/9003", env.Status, env.Code)
	}

	// SKU 停用 → 409/9003。
	if _, err := g.DB().Model("skus").Ctx(context.Background()).Where("id", skuA).Data(g.Map{"status": 0}).Update(); err != nil {
		t.Fatalf("disable sku: %v", err)
	}
	env = orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-bad-3",
		"sku_code": "SKU-CODE-A", "quantity": 1,
	})
	if env.Status != 409 || env.Code != 9003 {
		t.Fatalf("disabled sku: status=%d code=%d want 409/9003", env.Status, env.Code)
	}

	// 商品下架 → 409/9003。
	if _, err := g.DB().Model("products").Ctx(context.Background()).Where("id", productID).Data(g.Map{"status": 2}).Update(); err != nil {
		t.Fatalf("off shelf product: %v", err)
	}
	env = orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-bad-4",
		"sku_code": "SKU-CODE-B", "quantity": 1,
	})
	if env.Status != 409 || env.Code != 9003 {
		t.Fatalf("off-shelf product: status=%d code=%d want 409/9003", env.Status, env.Code)
	}

	if n := orderCount(t, userID); n != 0 {
		t.Fatalf("非法 SKU 不应创建订单，实际 %d 单", n)
	}
	if orderStock(t, skuA) != 10 || orderStock(t, skuB) != 10 {
		t.Fatalf("非法 SKU 不应扣库存")
	}
}

// TestOrderNoSpecSkuAndInsufficientStock 覆盖无规格 SKU 正常下单与库存不足回滚。
func TestOrderNoSpecSkuAndInsufficientStock(t *testing.T) {
	base := setupOrderServer(t)
	categoryID := orderInsertCategory(t, "库存分类")

	// 无规格 SKU（spec_signature 为空、无 sku_spec_values）。
	noSpecProduct := orderInsertProduct(t, categoryID, "无规格商品", 1)
	noSpecSku, err := g.DB().Model("skus").Ctx(context.Background()).Data(g.Map{
		"sku_code": "SKU-NOSPEC-001", "product_id": noSpecProduct, "name": "默认",
		"price": 2500, "original_price": 3000, "status": 1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert no-spec sku: %v", err)
	}
	orderInsertInventory(t, noSpecSku, 5)

	token, _, addressID := orderUserWithAddress(t, base, "nospecbuyer")
	env := orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-nospec-1",
		"sku_code": "SKU-NOSPEC-001", "quantity": 2,
	})
	if env.Status != 200 || env.Code != 0 || env.Data == nil {
		t.Fatalf("no-spec order: status=%d code=%d msg=%q", env.Status, env.Code, env.Message)
	}
	if env.Data.TotalAmount != 5000 || env.Data.Items[0].SpecText != "" {
		t.Fatalf("no-spec order 快照不符: %+v", env.Data.Items[0])
	}

	// 库存不足：新 SKU 库存 1，下单 2 → 409/6001；订单与库存均不变。
	lowProduct := orderInsertProduct(t, categoryID, "低库存商品", 1)
	lowSku := orderInsertSpecSku(t, lowProduct, "SKU-LOW-STOCK", "低库存", 1000, 1000, 1, nil)
	orderInsertInventory(t, lowSku, 1)
	env = orderCall(t, base, "POST", "/orders", token, map[string]any{
		"source": "direct", "address_id": addressID, "idempotency_key": "key-low-stock",
		"sku_code": "SKU-LOW-STOCK", "quantity": 2,
	})
	if env.Status != 409 || env.Code != 6001 {
		t.Fatalf("insufficient stock: status=%d code=%d want 409/6001", env.Status, env.Code)
	}
	// 事务整体回滚：不产生订单项、不扣库存。
	if v, err := g.DB().Model("order_items").Ctx(context.Background()).Where("sku_id", lowSku).Count(); err != nil {
		t.Fatalf("count order items: %v", err)
	} else if v != 0 {
		t.Fatalf("库存不足不应写入订单项，实际 %d 行", v)
	}
	if got := orderStock(t, lowSku); got != 1 {
		t.Fatalf("库存不足事务应回滚，stock=%d want 1", got)
	}
}

// TestCartIgnoresClientPrice 覆盖购物车加购不信任客户端价格：price_snapshot 只取 skus.price。
func TestCartIgnoresClientPrice(t *testing.T) {
	base := setupOrderServer(t)
	categoryID := orderInsertCategory(t, "购物车分类")
	productID := orderInsertProduct(t, categoryID, "购物车商品", 1)
	skuID := orderInsertSpecSku(t, productID, "SKU-CART-001", "红色,L", 6600, 8000, 1,
		[][2]string{{"颜色", "红色"}, {"尺码", "L"}})

	token, userID, _ := orderUserWithAddress(t, base, "cartbuyer")
	res := isoDo(t, base, "POST", "/cart/items", map[string]any{
		"sku_id": skuID, "quantity": 1, "price": 1, "price_snapshot": 1,
	}, isoAuthHeader(token))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("cart add: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}

	v, err := g.DB().Model("cart_items").Ctx(context.Background()).Fields("price_snapshot").
		Where("user_id", userID).Where("sku_id", skuID).Value()
	if err != nil {
		t.Fatalf("query cart snapshot: %v", err)
	}
	if v == nil || v.Int64() != 6600 {
		t.Fatalf("cart price_snapshot=%v want 6600（服务端 SKU 价格）", v)
	}
}
