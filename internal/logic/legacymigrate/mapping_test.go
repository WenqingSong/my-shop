package legacymigrate

import (
	"errors"
	"testing"
)

// TestYuanToCents 覆盖 INV-001（金额元→整数分定点转换）：
// 正常、NULL、非法、负数、超限、多位小数等边界，验证无浮点误差。
func TestYuanToCents(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{"典型两位小数", "19.90", 1990, false},
		{"一位小数", "19.9", 1990, false},
		{"无小数", "19", 1900, false},
		{"零元", "0.00", 0, false},
		{"零整数", "0", 0, false},
		{"NULL/空串", "", 0, false},
		{"空白串", "   ", 0, false},
		{"上限边界 999999.99", "999999.99", 99999999, false},
		{"负数", "-1.00", 0, true},
		{"非数字", "abc", 0, true},
		{"三位小数", "1.234", 0, true},
		{"超过上限", "1000000.00", 0, true},
		{"正号", "+1.50", 150, false},
		{"含空格", " 19.90 ", 1990, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := YuanToCents(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("YuanToCents(%q) 期望报错，实际返回 %d", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("YuanToCents(%q) 意外报错: %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("YuanToCents(%q) = %d, 期望 %d", c.in, got, c.want)
			}
		})
	}
}

// TestMapCategoryStatus 覆盖分类状态映射（D2）：is_del=1→禁用、is_del=0→启用。
func TestMapCategoryStatus(t *testing.T) {
	if got := MapCategory(&LegacyCategory{ID: 1, IsDel: 0}).Status; got != categoryStatusEnabled {
		t.Fatalf("is_del=0 分类 status = %d, 期望 %d", got, categoryStatusEnabled)
	}
	if got := MapCategory(&LegacyCategory{ID: 2, IsDel: 1}).Status; got != categoryStatusDisabled {
		t.Fatalf("is_del=1 分类 status = %d, 期望 %d", got, categoryStatusDisabled)
	}
}

// TestMapProductStatus 覆盖商品状态映射：is_del=1→off_shelf；
// is_del=0 时 is_show=1→on_shelf、is_show=0→off_shelf。
func TestMapProductStatus(t *testing.T) {
	cases := []struct {
		name       string
		isDel      int
		isShow     int
		wantStatus int
	}{
		{"软删除", 1, 1, productStatusOffShelf},
		{"上架", 0, 1, productStatusOnShelf},
		{"下架", 0, 0, productStatusOffShelf},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pt, _, err := MapProduct(&LegacyProduct{IsDel: c.isDel, IsShow: c.isShow, Price: "1.00", StoreName: "n"})
			if err != nil {
				t.Fatalf("MapProduct 意外报错: %v", err)
			}
			if pt.Status != c.wantStatus {
				t.Fatalf("status = %d, 期望 %d", pt.Status, c.wantStatus)
			}
		})
	}
}

// TestSkuStatusForProduct 覆盖 INV-007（SKU 可见性安全）：
// SKU enabled 当且仅当商品 on_shelf，其余一律 disabled。
func TestSkuStatusForProduct(t *testing.T) {
	cases := []struct {
		productStatus int
		want          int
	}{
		{productStatusOnShelf, skuStatusEnabled},
		{productStatusOffShelf, skuStatusDisabled},
		{productStatusDraft, skuStatusDisabled},
	}
	for _, c := range cases {
		if got := SkuStatusForProduct(c.productStatus); got != c.want {
			t.Fatalf("SkuStatusForProduct(%d) = %d, 期望 %d", c.productStatus, got, c.want)
		}
	}
}

// TestSplitSliderImages 覆盖轮播图拆分：空、单图、多图、含空项。
func TestSplitSliderImages(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"空", "", nil},
		{"单图", "/file/pic/a.jpg", []string{"/file/pic/a.jpg"}},
		{"多图", "/a.jpg,/b.jpg", []string{"/a.jpg", "/b.jpg"}},
		{"含空项", "/a.jpg, ,/b.jpg", []string{"/a.jpg", "/b.jpg"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SplitSliderImages(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("SplitSliderImages(%q) = %v, 期望 %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("SplitSliderImages(%q)[%d] = %q, 期望 %q", c.in, i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestMapSkusInventoryDedup 覆盖 INV-002（库存不重复计算）与 AC-006（默认 SKU 有据判定）：
//   - 有明细：每行一个 SKU + 库存（quantity=行 stock），商品级 stock 忽略；
//   - 无明细单规格：1 个默认 SKU，quantity=商品级 stock；
//   - 无明细多规格：ErrMultiSpecMissingDetail；
//   - 重复规格：报错跳过。
func TestMapSkusInventoryDedup(t *testing.T) {
	const productPrice = 1990
	const productStock = 100
	const productImage = "/main.jpg"
	const productStatus = productStatusOnShelf

	t.Run("有明细-多规格按行库存", func(t *testing.T) {
		attrValues := []*LegacyAttrValue{
			{ID: 1, SKU: "4+64G,金色", Stock: 10, Price: "19.90"},
			{ID: 2, SKU: "8+128G,黑色", Stock: 20, Price: "29.90"},
		}
		mappings, err := MapSkus(productPrice, productStock, productImage, productStatus, attrValues, 2)
		if err != nil {
			t.Fatalf("MapSkus 意外报错: %v", err)
		}
		if len(mappings) != 2 {
			t.Fatalf("期望 2 个 SKU, 实际 %d", len(mappings))
		}
		if mappings[0].Inventory.Quantity != 10 || mappings[1].Inventory.Quantity != 20 {
			t.Fatalf("库存应按 SKU 行取值: got %d/%d", mappings[0].Inventory.Quantity, mappings[1].Inventory.Quantity)
		}
		// 商品级 stock 必须被忽略（不叠加）。
		for _, m := range mappings {
			if m.Inventory.Quantity == productStock {
				t.Fatalf("不应使用商品级库存 %d", productStock)
			}
		}
	})

	t.Run("无明细-单规格默认SKU用商品级库存", func(t *testing.T) {
		mappings, err := MapSkus(productPrice, productStock, productImage, productStatus, nil, 1)
		if err != nil {
			t.Fatalf("MapSkus 意外报错: %v", err)
		}
		if len(mappings) != 1 {
			t.Fatalf("期望 1 个默认 SKU, 实际 %d", len(mappings))
		}
		if mappings[0].Sku.Name != defaultSKUName {
			t.Fatalf("默认 SKU 名 = %q, 期望 %q", mappings[0].Sku.Name, defaultSKUName)
		}
		if mappings[0].Inventory.Quantity != productStock {
			t.Fatalf("默认 SKU 库存 = %d, 期望商品级 %d", mappings[0].Inventory.Quantity, productStock)
		}
		if mappings[0].OldID != 0 {
			t.Fatalf("默认 SKU OldID 应为 0, 实际 %d", mappings[0].OldID)
		}
	})

	t.Run("无明细-多规格缺明细跳过", func(t *testing.T) {
		_, err := MapSkus(productPrice, productStock, productImage, productStatus, nil, 2)
		if !errors.Is(err, ErrMultiSpecMissingDetail) {
			t.Fatalf("期望 ErrMultiSpecMissingDetail, 实际 %v", err)
		}
	})

	t.Run("重复规格报错", func(t *testing.T) {
		attrValues := []*LegacyAttrValue{
			{ID: 1, SKU: "默认", Stock: 10, Price: "1.00"},
			{ID: 2, SKU: "默认", Stock: 20, Price: "2.00"},
		}
		if _, err := MapSkus(productPrice, productStock, productImage, productStatus, attrValues, 1); err == nil {
			t.Fatalf("重复规格应报错")
		}
	})
}
