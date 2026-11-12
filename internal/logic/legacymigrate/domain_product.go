package legacymigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// productDomain 迁移分类树、商品、商品规格、SKU 与 SKU 级库存。
// 目标表：categories/products/product_images/product_specs/product_spec_values/
// skus/sku_spec_values/inventories/legacy_id_map。
type productDomain struct{}

func (productDomain) name() string { return "products" }

func (productDomain) run(ctx context.Context, dc *domainContext) error {
	if err := migrateCategories(ctx, dc); err != nil {
		return err
	}
	return migrateProducts(ctx, dc)
}

// errSkipped 表示当前分类已按规则跳过（原因已写入 result），调用方应忽略并继续。
var errSkipped = errors.New("category skipped")

// migrateCategories 迁移分类，返回旧分类 ID → 新分类 ID 的完整映射（含已迁移与本次新增）。
// import 模式按「父先子后」顺序以最终 parent_id 单趟插入（parent_id 无自引用 FK，无需两阶段），
// 避免两阶段临时 parent_id=0 在 uk(parent_id,name) 下把「不同父级同名分类」误判为冲突（CLEAN-001）。
// 同父级重名等约束冲突逐条跳过并记录原因；父缺失/父被跳过/父链成环降级为 parent_id=0 并记警告（不计入跳过）。
func migrateCategories(ctx context.Context, dc *domainContext) error {
	stat := dc.stat("categories")
	cats, err := loadLegacyCategories(ctx, dc.source)
	if err != nil {
		return err
	}
	stat.SourceTotal = len(cats)
	stat.Eligible = len(cats)

	byID := make(map[int64]*LegacyCategory, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
	}

	oldToNew := make(map[int64]int64, len(cats))
	// 先并入已迁移的分类，供父分类/商品关联复用。
	for _, c := range cats {
		if nid, ok := dc.mapped(entityCategory, c.ID); ok {
			oldToNew[c.ID] = nid
			dc.skipAlready("categories")
		}
	}

	if dc.dryRun {
		for _, c := range cats {
			if _, ok := oldToNew[c.ID]; ok {
				continue
			}
			if strings.TrimSpace(c.Name) == "" {
				dc.skip("categories", c.ID, "分类名为空")
				continue
			}
			if c.PID != 0 {
				if _, ok := byID[c.PID]; !ok {
					dc.warn("categories", "分类 old_id=%d 的父分类 old_id=%d 不存在，parent_id 置 0", c.ID, c.PID)
				}
			}
			dc.plan(entityCategory, c.ID)
			oldToNew[c.ID] = plannedID
			stat.Migrated++
		}
		return nil
	}

	// import：父先子后单趟插入，业务记录与映射行同事务（INV-006）。
	tx, err := dc.target.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启分类迁移事务: %w", err)
	}
	defer tx.Rollback()

	resolving := make(map[int64]bool)
	skipped := make(map[int64]bool)
	var resolve func(c *LegacyCategory) (int64, error)
	resolve = func(c *LegacyCategory) (int64, error) {
		// 已迁移或本次已处理。
		if nid, ok := oldToNew[c.ID]; ok {
			return nid, nil
		}
		// 本次已跳过（可能被递归重复访问），只记一次。
		if skipped[c.ID] {
			return 0, errSkipped
		}
		if strings.TrimSpace(c.Name) == "" {
			dc.skip("categories", c.ID, "分类名为空")
			skipped[c.ID] = true
			return 0, errSkipped
		}
		// 父链成环：降级为顶级分类，避免死循环。
		if resolving[c.ID] {
			dc.warn("categories", "分类 old_id=%d 父链存在环，parent_id 置 0", c.ID)
			nid, err := insertCategory(ctx, dc, tx, c, 0)
			if err != nil {
				return 0, err
			}
			oldToNew[c.ID] = nid
			stat.Migrated++
			return nid, nil
		}
		resolving[c.ID] = true
		defer delete(resolving, c.ID)

		// 解析父分类新 ID。
		parentNewID := int64(0)
		if c.PID != 0 {
			parent, ok := byID[c.PID]
			switch {
			case !ok:
				dc.warn("categories", "分类 old_id=%d 的父分类 old_id=%d 不存在，parent_id 置 0", c.ID, c.PID)
			default:
				if pid, err := resolve(parent); err == nil {
					parentNewID = pid
				} else if errors.Is(err, errSkipped) {
					dc.warn("categories", "分类 old_id=%d 的父分类 old_id=%d 被跳过，parent_id 置 0", c.ID, c.PID)
				} else {
					return 0, err
				}
			}
		}

		// 递归解析父分类期间，当前分类可能已通过环检测分支被插入（环状父链），
		// 此时复用其新 ID，避免重复插入（CLEAN-004）。
		if nid, ok := oldToNew[c.ID]; ok {
			return nid, nil
		}

		nid, err := insertCategory(ctx, dc, tx, c, parentNewID)
		if err != nil {
			if isDuplicateKeyError(err) {
				dc.skip("categories", c.ID, fmt.Sprintf("与同父级分类重名 %q", c.Name))
				skipped[c.ID] = true
				return 0, errSkipped
			}
			return 0, err
		}
		oldToNew[c.ID] = nid
		stat.Migrated++
		return nid, nil
	}

	for _, c := range cats {
		if _, err := resolve(c); err != nil && !errors.Is(err, errSkipped) {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交分类迁移事务: %w", err)
	}
	return nil
}

// insertCategory 在事务内插入单个分类（最终 parent_id）并写映射行，返回新 ID。
// 唯一约束冲突交由调用方（resolve）判读为逐条跳过，本函数不吞掉其他错误。
func insertCategory(ctx context.Context, dc *domainContext, tx *sql.Tx, c *LegacyCategory, parentNewID int64) (int64, error) {
	res, err := tx.ExecContext(ctx,
		"INSERT INTO categories (parent_id, name, sort, status) VALUES (?, ?, ?, ?)",
		parentNewID, c.Name, c.Sort, MapCategory(c).Status)
	if err != nil {
		return 0, fmt.Errorf("插入分类 old_id=%d: %w", c.ID, err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("读取分类新 ID old_id=%d: %w", c.ID, err)
	}
	if err := dc.record(ctx, tx, entityCategory, c.ID, newID); err != nil {
		return 0, err
	}
	return newID, nil
}

// legacySpecGroup 是商品级规格组定义。
type legacySpecGroup struct {
	Name   string
	Values []string
}

// legacySpecSchema 是商品规格定义 + SKU 组合明细（来自 store_product_attr_result，
// 缺失时回退 store_product_attr）。
type legacySpecSchema struct {
	Groups []legacySpecGroup
	Combos []map[string]string
}

// skuPlan 是单个 SKU 的待写入计划（Combo 为规格名→规格值）。
type skuPlan struct {
	OldID    int64
	Name     string
	ImageURL string
	Price    int64
	Stock    int64
	Status   int
	Combo    map[string]string
}

// productPlan 是单个商品的完整迁移计划。
type productPlan struct {
	OldID      int64
	Product    ProductTarget
	Images     []string
	Schema     *legacySpecSchema
	SKUs       []*skuPlan
	MissingSKU bool
}

// migrateProducts 迁移商品及其规格、SKU、库存：每个商品一个事务（业务记录与映射行同事务）。
//
// 商品范围：is_del=0 的有效商品 + 被历史订单/评价/收藏/优惠券范围引用的软删除商品
// （后者以 off_shelf + SKU disabled 保留，保证历史关联不悬空）。
func migrateProducts(ctx context.Context, dc *domainContext) error {
	stat := dc.stat("products")
	products, err := loadLegacyProducts(ctx, dc.source)
	if err != nil {
		return err
	}
	stat.SourceTotal = len(products)

	attrValues, err := loadLegacyAttrValues(ctx, dc.source)
	if err != nil {
		return err
	}
	schemas, err := loadLegacySpecSchemas(ctx, dc.source)
	if err != nil {
		return err
	}
	referenced, err := loadReferencedProductIDs(ctx, dc.source)
	if err != nil {
		return err
	}

	skuStat := dc.stat("skus")
	invStat := dc.stat("inventories")
	specStat := dc.stat("product_specs")
	specValueStat := dc.stat("product_spec_values")
	skuSpecStat := dc.stat("sku_spec_values")
	skuStat.SourceTotal = countLegacyAttrValues(attrValues)
	invStat.SourceTotal = skuStat.SourceTotal

	for _, p := range products {
		if p.IsDel != 0 && !referenced[p.ID] {
			stat.Skipped++
			skuStat.Skipped += len(attrValues[p.ID])
			invStat.Skipped += len(attrValues[p.ID])
			continue
		}
		stat.Eligible++

		cateID, err := parseCateID(p.CateID)
		if err != nil {
			dc.skip("products", p.ID, "分类关联非法: "+err.Error())
			continue
		}
		// 分类新 ID 从共享映射解析（分类域先执行）。
		newCateID, ok := dc.mapped(entityCategory, cateID)
		if !ok {
			dc.skip("products", p.ID, fmt.Sprintf("引用不存在或未迁移的分类 cate_id=%d", cateID))
			continue
		}

		plan, err := buildProductPlan(p, newCateID, attrValues[p.ID], schemas[p.ID], dc)
		if err != nil {
			dc.skip("products", p.ID, err.Error())
			continue
		}

		specGroups, specValues, assocCount := countPlanSpecs(plan)
		specStat.SourceTotal += specGroups
		specStat.Eligible += specGroups
		specValueStat.SourceTotal += specValues
		specValueStat.Eligible += specValues
		skuSpecStat.SourceTotal += assocCount
		skuSpecStat.Eligible += assocCount
		// Eligible 只统计源 SKU 行；Migrated 可能比 Eligible 多 1（单规格商品派生默认 SKU）。
		skuStat.Eligible += len(attrValues[p.ID])
		invStat.Eligible += len(attrValues[p.ID])

		if _, ok := dc.mapped(entityProduct, p.ID); ok {
			dc.skipAlready("products")
			skuStat.Skipped += len(attrValues[p.ID])
			invStat.Skipped += len(attrValues[p.ID])
			specStat.Skipped += specGroups
			specValueStat.Skipped += specValues
			skuSpecStat.Skipped += assocCount
			continue
		}

		if dc.dryRun {
			stat.Migrated++
			skuStat.Migrated += len(plan.SKUs)
			invStat.Migrated += len(plan.SKUs)
			specStat.Migrated += specGroups
			specValueStat.Migrated += specValues
			skuSpecStat.Migrated += assocCount
			dc.plan(entityProduct, p.ID)
			for _, sku := range plan.SKUs {
				if sku.OldID > 0 {
					dc.plan(entitySKU, sku.OldID)
				}
			}
			continue
		}

		insertedAssoc, err := writeProductPlan(ctx, dc, plan)
		if err != nil {
			dc.fail("products", "商品 old_id=%d 迁移失败（已回滚）: %v", p.ID, err)
			continue
		}
		stat.Migrated++
		skuStat.Migrated += len(plan.SKUs)
		invStat.Migrated += len(plan.SKUs)
		specStat.Migrated += specGroups
		specValueStat.Migrated += specValues
		skuSpecStat.Migrated += insertedAssoc
	}
	return nil
}

// buildProductPlan 把单个历史商品映射为目标商品 + 规格 + SKU 计划。
func buildProductPlan(p *LegacyProduct, categoryNewID int64, attrValues []*LegacyAttrValue, schema *legacySpecSchema, dc *domainContext) (*productPlan, error) {
	pt, images, err := MapProduct(p)
	if err != nil {
		return nil, err
	}
	pt.CategoryID = categoryNewID
	if p.IsDel == 1 {
		pt.Status = productStatusOffShelf
	}
	if p.SpecType != 1 {
		schema = nil // spec_type=0：保持历史无规格兼容，不写规格关联。
	}
	if schema != nil && len(schema.Groups) == 0 {
		schema = nil
	}

	plan := &productPlan{OldID: p.ID, Product: pt, Images: images, Schema: schema}

	if len(attrValues) == 0 {
		if schema != nil && len(schema.Groups) > 1 {
			// 多规格但无 SKU 明细：无可信价格/库存，不补造 SKU；商品降级为不可售。
			plan.MissingSKU = true
			pt.Status = productStatusOffShelf
			plan.Product.Status = productStatusOffShelf
			dc.warn("products", "商品 old_id=%d 为多规格但缺少 SKU 明细，保留商品并下架、不生成 SKU", p.ID)
			return plan, nil
		}
		plan.SKUs = []*skuPlan{{
			Name:   defaultSKUName,
			Price:  pt.Price,
			Stock:  p.Stock,
			Status: SkuStatusForProduct(pt.Status),
		}}
		return plan, nil
	}

	seenName := make(map[string]bool, len(attrValues))
	for _, av := range attrValues {
		name := strings.TrimSpace(av.SKU)
		if name == "" {
			name = defaultSKUName
		}
		if seenName[name] {
			return nil, fmt.Errorf("商品内 SKU 名重复 %q", name)
		}
		seenName[name] = true

		price, err := YuanToCents(av.Price)
		if err != nil {
			return nil, fmt.Errorf("SKU 金额: %w", err)
		}
		// 只保留源 SKU 图；空串表示回退商品主图（目标 skus.image_url 既有语义），不复制主图。
		image := strings.TrimSpace(av.Image)
		sku := &skuPlan{
			OldID:    av.ID,
			Name:     name,
			ImageURL: truncateRunes(image, 512),
			Price:    price,
			Stock:    av.Stock,
			Status:   SkuStatusForProduct(pt.Status),
		}
		if schema != nil {
			combo, ok := matchSpecCombo(name, schema)
			if !ok {
				dc.warn("products", "商品 old_id=%d 的 SKU %q 无法匹配规格组合，SKU 不写规格关联", p.ID, name)
			} else {
				sku.Combo = combo
			}
		}
		plan.SKUs = append(plan.SKUs, sku)
	}
	return plan, nil
}

// matchSpecCombo 用 SKU 组合字符串匹配 attr_result 的规格名→值映射。
// 匹配依据是「值的多重集合」而非位置，兼容源数据同一商品内 SKU 顺序不一致的情况。
func matchSpecCombo(skuName string, schema *legacySpecSchema) (map[string]string, bool) {
	components := splitSpecComponents(skuName)
	if len(components) == 0 {
		return nil, false
	}
	if len(schema.Combos) == 0 {
		return matchSpecByMembership(components, schema)
	}
	want := make(map[string]int, len(components))
	for _, c := range components {
		want[c]++
	}
	var matched map[string]string
	for _, combo := range schema.Combos {
		if len(combo) != len(schema.Groups) {
			continue
		}
		got := make(map[string]int, len(combo))
		for _, v := range combo {
			got[v]++
		}
		if equalCounts(want, got) {
			if matched != nil {
				return nil, false // 多个组合同值集合：无法唯一判定，拒绝猜测
			}
			matched = combo
		}
	}
	return matched, matched != nil
}

// matchSpecByMembership 在缺少 attr_result 组合明细时，按「值属于唯一规格组」推断；
// 任一值缺失或跨组歧义即拒绝，不猜测。
func matchSpecByMembership(components []string, schema *legacySpecSchema) (map[string]string, bool) {
	combo := make(map[string]string, len(components))
	for _, c := range components {
		group := ""
		count := 0
		for _, g := range schema.Groups {
			for _, v := range g.Values {
				if v == c {
					group = g.Name
					count++
					break
				}
			}
		}
		if count != 1 {
			return nil, false
		}
		if _, dup := combo[group]; dup {
			return nil, false
		}
		combo[group] = c
	}
	if len(combo) != len(schema.Groups) {
		return nil, false
	}
	return combo, true
}

func splitSpecComponents(skuName string) []string {
	parts := strings.Split(skuName, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func equalCounts(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func countPlanSpecs(plan *productPlan) (groups, values, assocs int) {
	if plan.Schema != nil {
		groups = len(plan.Schema.Groups)
		for _, g := range plan.Schema.Groups {
			values += len(g.Values)
		}
	}
	for _, sku := range plan.SKUs {
		assocs += len(sku.Combo)
	}
	return groups, values, assocs
}

func countLegacyAttrValues(values map[int64][]*LegacyAttrValue) int {
	total := 0
	for _, rows := range values {
		total += len(rows)
	}
	return total
}

// writeProductPlan 在一个事务内写入商品、图册、规格、SKU、规格关联、库存及映射（INV-006），
// 返回实际写入的 sku_spec_values 行数。
func writeProductPlan(ctx context.Context, dc *domainContext, plan *productPlan) (int, error) {
	tx, err := dc.target.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	pt := plan.Product
	res, err := tx.ExecContext(ctx,
		"INSERT INTO products (name, brand, category_id, price, main_image, detail, status) VALUES (?, ?, ?, ?, ?, ?, ?)",
		pt.Name, pt.Brand, pt.CategoryID, pt.Price, pt.MainImage, pt.Detail, pt.Status)
	if err != nil {
		return 0, fmt.Errorf("插入 products: %w", err)
	}
	productNewID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("读取商品新 ID: %w", err)
	}

	for i, url := range plan.Images {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO product_images (product_id, url, sort) VALUES (?, ?, ?)",
			productNewID, url, i); err != nil {
			return 0, fmt.Errorf("插入 product_images: %w", err)
		}
	}
	if err := dc.record(ctx, tx, entityProduct, plan.OldID, productNewID); err != nil {
		return 0, err
	}

	specIDByName := make(map[string]int64)
	valueIDBySpec := make(map[string]map[string]int64)
	if plan.Schema != nil {
		for i, g := range plan.Schema.Groups {
			specRes, err := tx.ExecContext(ctx,
				"INSERT INTO product_specs (product_id, name, sort) VALUES (?, ?, ?)",
				productNewID, g.Name, i)
			if err != nil {
				return 0, fmt.Errorf("插入 product_specs %q: %w", g.Name, err)
			}
			specID, err := specRes.LastInsertId()
			if err != nil {
				return 0, fmt.Errorf("读取规格组新 ID: %w", err)
			}
			specIDByName[g.Name] = specID
			valueIDBySpec[g.Name] = make(map[string]int64, len(g.Values))
			for j, v := range g.Values {
				valueRes, err := tx.ExecContext(ctx,
					"INSERT INTO product_spec_values (spec_id, value, sort) VALUES (?, ?, ?)",
					specID, v, j)
				if err != nil {
					return 0, fmt.Errorf("插入 product_spec_values %q/%q: %w", g.Name, v, err)
				}
				valueID, err := valueRes.LastInsertId()
				if err != nil {
					return 0, fmt.Errorf("读取规格值新 ID: %w", err)
				}
				valueIDBySpec[g.Name][v] = valueID
			}
		}
	}

	assocCount := 0
	for _, sku := range plan.SKUs {
		assocs := make([]SpecAssociation, 0, len(sku.Combo))
		incomplete := false
		for name, value := range sku.Combo {
			specID, ok := specIDByName[name]
			if !ok {
				incomplete = true
				break
			}
			valueID, ok := valueIDBySpec[name][value]
			if !ok {
				incomplete = true
				break
			}
			assocs = append(assocs, SpecAssociation{SpecID: specID, ValueID: valueID})
		}
		if incomplete {
			assocs = nil
		}
		signature := SpecSignature(assocs)

		var signatureArg any
		if signature != "" {
			signatureArg = signature
		}
		skuRes, err := tx.ExecContext(ctx,
			"INSERT INTO skus (product_id, name, image_url, price, status, spec_signature) VALUES (?, ?, ?, ?, ?, ?)",
			productNewID, sku.Name, sku.ImageURL, sku.Price, sku.Status, signatureArg)
		if err != nil {
			return 0, fmt.Errorf("插入 skus %q: %w", sku.Name, err)
		}
		skuNewID, err := skuRes.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("读取 SKU 新 ID: %w", err)
		}
		for _, a := range assocs {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO sku_spec_values (sku_id, spec_id, spec_value_id) VALUES (?, ?, ?)",
				skuNewID, a.SpecID, a.ValueID); err != nil {
				return 0, fmt.Errorf("插入 sku_spec_values sku=%q: %w", sku.Name, err)
			}
			assocCount++
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO inventories (sku_id, quantity) VALUES (?, ?)",
			skuNewID, sku.Stock); err != nil {
			return 0, fmt.Errorf("插入 inventories: %w", err)
		}
		if sku.OldID > 0 {
			if err := dc.record(ctx, tx, entitySKU, sku.OldID, skuNewID); err != nil {
				return 0, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return assocCount, nil
}

// attrResultJSON 是 store_product_attr_result.result 的结构：
// attr[] 为规格组及可选值，value[].detail 为单个 SKU 的「规格名→规格值」映射。
type attrResultJSON struct {
	Attr []struct {
		Value  string   `json:"value"`
		Detail []string `json:"detail"`
	} `json:"attr"`
	Value []struct {
		Detail map[string]string `json:"detail"`
	} `json:"value"`
}

// loadLegacySpecSchemas 解析商品规格定义。
// 权威来源是 store_product_attr_result（attr[].detail 与 value[].detail），
// 解析失败/缺失时回退 store_product_attr 的 attr_name + attr_values。
func loadLegacySpecSchemas(ctx context.Context, source *sql.DB) (map[int64]*legacySpecSchema, error) {
	out := make(map[int64]*legacySpecSchema)

	rows, err := source.QueryContext(ctx,
		"SELECT product_id, result FROM store_product_attr_result ORDER BY product_id, id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_product_attr_result: %w", err)
	}
	for rows.Next() {
		var productID int64
		var raw string
		if err := rows.Scan(&productID, &raw); err != nil {
			rows.Close()
			return nil, fmt.Errorf("扫描 store_product_attr_result: %w", err)
		}
		if _, done := out[productID]; done {
			continue
		}
		var parsed attrResultJSON
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			continue
		}
		if len(parsed.Attr) == 0 {
			continue
		}
		schema := &legacySpecSchema{}
		for _, g := range parsed.Attr {
			name := strings.TrimSpace(g.Value)
			if name == "" {
				schema = nil
				break
			}
			values := make([]string, 0, len(g.Detail))
			seen := make(map[string]bool)
			for _, v := range g.Detail {
				v = strings.TrimSpace(v)
				if v == "" || seen[v] {
					continue
				}
				seen[v] = true
				values = append(values, v)
			}
			if len(values) == 0 {
				schema = nil
				break
			}
			schema.Groups = append(schema.Groups, legacySpecGroup{Name: name, Values: values})
		}
		if schema == nil {
			continue
		}
		for _, v := range parsed.Value {
			if len(v.Detail) == 0 {
				continue
			}
			combo := make(map[string]string, len(v.Detail))
			for k, val := range v.Detail {
				combo[strings.TrimSpace(k)] = strings.TrimSpace(val)
			}
			schema.Combos = append(schema.Combos, combo)
		}
		out[productID] = schema
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 store_product_attr_result: %w", err)
	}

	// 回退：仅 attr 定义（无组合明细），SKU 组合由 matchSpecCombo 的成员匹配推断。
	fallbackRows, err := source.QueryContext(ctx,
		"SELECT product_id, attr_name, attr_values FROM store_product_attr WHERE product_id > 0 ORDER BY product_id, id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_product_attr: %w", err)
	}
	defer fallbackRows.Close()
	for fallbackRows.Next() {
		var productID int64
		var name, values string
		if err := fallbackRows.Scan(&productID, &name, &values); err != nil {
			return nil, fmt.Errorf("扫描 store_product_attr: %w", err)
		}
		if _, done := out[productID]; done {
			continue
		}
		schema := out[productID]
		if schema == nil {
			schema = &legacySpecSchema{}
		}
		parts := SplitSliderImages(values)
		if len(parts) == 0 {
			continue
		}
		schema.Groups = append(schema.Groups, legacySpecGroup{Name: strings.TrimSpace(name), Values: parts})
		out[productID] = schema
	}
	return out, fallbackRows.Err()
}

// loadReferencedProductIDs 收集历史订单/评价/收藏/优惠券范围引用的商品 ID。
// 用于保留软删除但仍被历史业务引用的商品，避免关联悬空。
func loadReferencedProductIDs(ctx context.Context, source *sql.DB) (map[int64]bool, error) {
	out := make(map[int64]bool)
	rows, err := source.QueryContext(ctx,
		`SELECT product_id FROM store_order_cart_info
		 UNION SELECT product_id FROM store_product_reply
		 UNION SELECT product_id FROM store_product_relation WHERE type = 'collect' AND is_del = 0`)
	if err != nil {
		return nil, fmt.Errorf("读取被引用商品: %w", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("扫描被引用商品: %w", err)
		}
		if id > 0 {
			out[id] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历被引用商品: %w", err)
	}

	couponRows, err := source.QueryContext(ctx,
		"SELECT product_id FROM store_coupon WHERE product_id IS NOT NULL AND product_id <> ''")
	if err != nil {
		return nil, fmt.Errorf("读取优惠券适用范围: %w", err)
	}
	defer couponRows.Close()
	for couponRows.Next() {
		var raw string
		if err := couponRows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("扫描优惠券适用范围: %w", err)
		}
		for _, part := range strings.Split(raw, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err == nil && id > 0 {
				out[id] = true
			}
		}
	}
	return out, couponRows.Err()
}

// loadLegacyCategories 读取 staging 库 store_category 全量行（含软删除，按 pid, id 排序）。
func loadLegacyCategories(ctx context.Context, source *sql.DB) ([]*LegacyCategory, error) {
	rows, err := source.QueryContext(ctx,
		"SELECT id, pid, cate_name, sort, is_show, is_del FROM store_category ORDER BY pid, id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_category: %w", err)
	}
	defer rows.Close()

	var out []*LegacyCategory
	for rows.Next() {
		var c LegacyCategory
		var sort, isShow, isDel sql.NullInt64
		if err := rows.Scan(&c.ID, &c.PID, &c.Name, &sort, &isShow, &isDel); err != nil {
			return nil, fmt.Errorf("扫描 store_category: %w", err)
		}
		c.Sort = int(sort.Int64)
		c.IsShow = int(isShow.Int64)
		c.IsDel = int(isDel.Int64)
		out = append(out, &c)
	}
	return out, rows.Err()
}

// loadLegacyProducts 读取 staging 库 store_product 全量行。
func loadLegacyProducts(ctx context.Context, source *sql.DB) ([]*LegacyProduct, error) {
	rows, err := source.QueryContext(ctx,
		"SELECT id, cate_id, price, stock, is_show, is_del, spec_type, image, slider_image, store_name, description FROM store_product ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_product: %w", err)
	}
	defer rows.Close()

	var out []*LegacyProduct
	for rows.Next() {
		var p LegacyProduct
		var price, cateID, image, sliderImage, storeName, description sql.NullString
		var stock, isShow, isDel, specType sql.NullInt64
		if err := rows.Scan(&p.ID, &cateID, &price, &stock, &isShow, &isDel, &specType, &image, &sliderImage, &storeName, &description); err != nil {
			return nil, fmt.Errorf("扫描 store_product: %w", err)
		}
		p.CateID = cateID.String
		if price.Valid {
			p.Price = price.String
		}
		if stock.Valid {
			p.Stock = stock.Int64
		}
		p.IsShow = int(isShow.Int64)
		p.IsDel = int(isDel.Int64)
		p.SpecType = int(specType.Int64)
		p.Image = image.String
		p.SliderImage = sliderImage.String
		p.StoreName = storeName.String
		p.Description = description.String
		out = append(out, &p)
	}
	return out, rows.Err()
}

// loadLegacyAttrValues 读取 staging 库 store_product_attr_value 全量行并按 product_id 分组。
func loadLegacyAttrValues(ctx context.Context, source *sql.DB) (map[int64][]*LegacyAttrValue, error) {
	rows, err := source.QueryContext(ctx,
		"SELECT id, product_id, sku, stock, price, image FROM store_product_attr_value ORDER BY product_id, id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_product_attr_value: %w", err)
	}
	defer rows.Close()

	out := make(map[int64][]*LegacyAttrValue)
	for rows.Next() {
		var av LegacyAttrValue
		var price, image sql.NullString
		var stock sql.NullInt64
		if err := rows.Scan(&av.ID, &av.ProductID, &av.SKU, &stock, &price, &image); err != nil {
			return nil, fmt.Errorf("扫描 store_product_attr_value: %w", err)
		}
		if price.Valid {
			av.Price = price.String
		}
		if stock.Valid {
			av.Stock = stock.Int64
		}
		av.Image = image.String
		out[av.ProductID] = append(out[av.ProductID], &av)
	}
	return out, rows.Err()
}

// loadLegacyAttrDims 读取 staging 库 store_product_attr 每个商品的规格维度数（单/多规格判定依据）。
func loadLegacyAttrDims(ctx context.Context, source *sql.DB) (map[int64]int, error) {
	rows, err := source.QueryContext(ctx,
		"SELECT product_id, COUNT(*) FROM store_product_attr GROUP BY product_id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_product_attr 维度: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]int)
	for rows.Next() {
		var productID int64
		var n int
		if err := rows.Scan(&productID, &n); err != nil {
			return nil, fmt.Errorf("扫描 store_product_attr 维度: %w", err)
		}
		out[productID] = n
	}
	return out, rows.Err()
}

// parseCateID 把历史 cate_id（字符串）转为 int64。
func parseCateID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("cate_id 为空")
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cate_id %q 非法", s)
	}
	return v, nil
}
