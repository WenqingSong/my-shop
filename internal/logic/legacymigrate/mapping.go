// Package legacymigrate 实现历史电商数据迁移：把 staging 库中 CRMEB 导出表
// （store_category/store_product/store_product_attr/store_product_attr_value）
// 适配导入 SurgeCart 目标库（categories/products/product_images/skus/inventories/legacy_id_map）。
//
// 本文件只包含「纯映射逻辑」：不访问数据库、不依赖连接，便于单元测试覆盖
// 字段映射、金额定点转换、状态映射、图片拆分与库存去重等关键不变量。
package legacymigrate

import (
	"fmt"
	"strconv"
	"strings"
)

// 目标状态机语义（与 docs/design/* 及既有代码一致）。
const (
	// 分类状态：1=启用、0=禁用。
	categoryStatusEnabled  = 1
	categoryStatusDisabled = 0

	// 商品状态：0=draft、1=on_shelf、2=off_shelf。
	productStatusDraft    = 0
	productStatusOnShelf  = 1
	productStatusOffShelf = 2

	// SKU 状态：1=enabled、0=disabled。
	skuStatusEnabled  = 1
	skuStatusDisabled = 0
)

// maxPriceCents 是金额（整数分）的上限，对应历史 decimal(8,2) 元的可表达范围（INV-001）。
const maxPriceCents = int64(99999999)

// defaultSKUName 是单规格商品无明细时的默认 SKU 名称。
const defaultSKUName = "默认"

// ErrMultiSpecMissingDetail 表示多规格商品缺少 SKU 明细（store_product_attr_value 无行），
// 无法安全映射 SKU 与库存，必须跳过并记录原因（AC-006/INV-002）。
var ErrMultiSpecMissingDetail = fmt.Errorf("多规格商品缺少 SKU 明细")

// LegacyCategory 是 staging 库 store_category 表中参与迁移的字段。
type LegacyCategory struct {
	ID     int64
	PID    int64
	Name   string
	Sort   int
	IsShow int
	IsDel  int
}

// LegacyProduct 是 staging 库 store_product 表中参与迁移的字段。
type LegacyProduct struct {
	ID          int64
	CateID      string // 历史为字符串，需转 int64
	Price       string // decimal(8,2) 元，字符串解析定点转分
	Stock       int64  // 商品级库存
	IsShow      int
	IsDel       int
	SpecType    int
	Image       string
	SliderImage string
	StoreName   string
	Description string
}

// LegacyAttrValue 是 staging 库 store_product_attr_value 表中参与迁移的字段（SKU 权威来源）。
type LegacyAttrValue struct {
	ID        int64
	ProductID int64
	SKU       string
	Stock     int64
	Price     string // decimal(8,2) 元
	Image     string
}

// CategoryTarget 是待写入目标 categories 表的映射结果。
type CategoryTarget struct {
	ParentID int64
	Name     string
	Sort     int
	Status   int
}

// ProductTarget 是待写入目标 products 表的映射结果。
type ProductTarget struct {
	Name       string
	Brand      string
	CategoryID int64
	Price      int64
	MainImage  string
	Detail     string
	Status     int
}

// SkuTarget 是待写入目标 skus 表的映射结果。
type SkuTarget struct {
	Name   string
	Price  int64
	Status int
}

// InventoryTarget 是待写入目标 inventories 表的映射结果（与 SkuTarget 一一对应）。
type InventoryTarget struct {
	Quantity int64
}

// SkuMapping 是单个 SKU 及其库存、历史 old_id 的映射结果。
// OldID 为 store_product_attr_value.id；单规格默认 SKU（无明细行）OldID 为 0，
// 因它没有独立历史主键、由商品级数据派生（AC-006 不无依据补造），不写入 SKU 级映射。
type SkuMapping struct {
	Sku       SkuTarget
	Inventory InventoryTarget
	OldID     int64
}

// YuanToCents 把「元」字符串精确转换为「整数分」，全程定点、不经过浮点（INV-001）。
// 规则（Contract 已确认）：
//   - 空串/NULL → 0；
//   - 负数、非数字、小数超过 2 位、超过上限 → 返回错误（调用方跳过并记录原因）；
//   - "19.90"→1990、"19.9"→1990、"19"→1900、"0.00"→0。
func YuanToCents(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, nil
	}
	if strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("负金额 %q", raw)
	}
	s = strings.TrimPrefix(s, "+")

	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	if intPart == "" {
		intPart = "0"
	}
	if !isDigits(intPart) {
		return 0, fmt.Errorf("非法金额 %q", raw)
	}
	if len(fracPart) > 2 {
		return 0, fmt.Errorf("金额小数超过 2 位 %q", raw)
	}
	if fracPart != "" && !isDigits(fracPart) {
		return 0, fmt.Errorf("非法金额 %q", raw)
	}
	for len(fracPart) < 2 {
		fracPart += "0"
	}

	yuan, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("非法金额 %q", raw)
	}
	cents, err := strconv.ParseInt(fracPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("非法金额 %q", raw)
	}
	total := yuan*100 + cents
	if total > maxPriceCents {
		return 0, fmt.Errorf("金额超过上限 %q", raw)
	}
	return total, nil
}

// isDigits 判断字符串是否全为 ASCII 数字（空串视为 false）。
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// MapCategory 映射单个历史分类行：pid→parent_id、cate_name→name、sort→sort；
// is_del=1→status=0（禁用）、is_del=0→status=1（启用）；is_show 与 pic 不映射（D2）。
func MapCategory(src *LegacyCategory) CategoryTarget {
	status := categoryStatusEnabled
	if src.IsDel == 1 {
		status = categoryStatusDisabled
	}
	return CategoryTarget{
		ParentID: src.PID,
		Name:     src.Name,
		Sort:     src.Sort,
		Status:   status,
	}
}

// MapProduct 映射单个历史商品行：store_name→name、price 元→整数分、description→detail、
// image→main_image；状态 is_del=1→off_shelf、is_del=0 时 is_show=1→on_shelf 否则 off_shelf。
// 返回商品目标、轮播图拆分结果（按逗号拆分、保持顺序）与错误。
func MapProduct(src *LegacyProduct) (ProductTarget, []string, error) {
	price, err := YuanToCents(src.Price)
	if err != nil {
		return ProductTarget{}, nil, fmt.Errorf("商品金额: %w", err)
	}
	status := productStatusOffShelf
	if src.IsDel != 1 && src.IsShow == 1 {
		status = productStatusOnShelf
	}
	return ProductTarget{
		Name:       strings.TrimSpace(src.StoreName),
		Brand:      "",
		CategoryID: 0, // 分类关联由引擎解析 cate_id 后填充
		Price:      price,
		MainImage:  strings.TrimSpace(src.Image),
		Detail:     src.Description,
		Status:     status,
	}, SplitSliderImages(src.SliderImage), nil
}

// SplitSliderImages 把轮播图字符串（逗号分隔）拆分为有序 URL 列表，忽略空项。
func SplitSliderImages(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// SkuStatusForProduct 计算 SKU 状态：enabled(1) 当且仅当商品 on_shelf(1)，其余 disabled(0)。
// 这是 INV-007 的关键：软删除/下架商品的 SKU 不得被下单或前台展示。
func SkuStatusForProduct(productStatus int) int {
	if productStatus == productStatusOnShelf {
		return skuStatusEnabled
	}
	return skuStatusDisabled
}

// MapSkus 把某商品的 SKU 明细映射为 skus + inventories 目标（库存去重，INV-002/AC-006）：
//   - 有 attrValue 行：每行一个 SKU + 一个 inventory（quantity=该行 stock），商品级 stock 忽略；
//   - 无 attrValue 行：单规格（维度数 <= 1）用商品级 stock/price 生成 1 个默认 SKU；
//     多规格（维度数 > 1）缺明细 → 返回 ErrMultiSpecMissingDetail 跳过；
//   - 绝不把商品级与 SKU 级库存相加；同一商品下重复规格名 → 跳过并记录原因。
func MapSkus(productPriceCents int64, productStock int64, productImage string, productStatus int, attrValues []*LegacyAttrValue, attrDimensionCount int) ([]SkuMapping, error) {
	if len(attrValues) > 0 {
		out := make([]SkuMapping, 0, len(attrValues))
		seen := make(map[string]bool, len(attrValues))
		for _, av := range attrValues {
			name := strings.TrimSpace(av.SKU)
			if name == "" {
				name = defaultSKUName
			}
			if seen[name] {
				return nil, fmt.Errorf("重复规格 %q", name)
			}
			seen[name] = true

			price, err := YuanToCents(av.Price)
			if err != nil {
				return nil, fmt.Errorf("SKU 金额: %w", err)
			}
			image := strings.TrimSpace(av.Image)
			if image == "" {
				image = productImage
			}
			out = append(out, SkuMapping{
				Sku:       SkuTarget{Name: name, Price: price, Status: SkuStatusForProduct(productStatus)},
				Inventory: InventoryTarget{Quantity: av.Stock},
				OldID:     av.ID,
			})
		}
		return out, nil
	}

	// 无明细：多规格缺明细无法安全映射，跳过。
	if attrDimensionCount > 1 {
		return nil, ErrMultiSpecMissingDetail
	}
	// 单规格：以商品级 stock/price 为据创建默认 SKU（有据判定，AC-006）。
	return []SkuMapping{{
		Sku:       SkuTarget{Name: defaultSKUName, Price: productPriceCents, Status: SkuStatusForProduct(productStatus)},
		Inventory: InventoryTarget{Quantity: productStock},
		OldID:     0,
	}}, nil
}
