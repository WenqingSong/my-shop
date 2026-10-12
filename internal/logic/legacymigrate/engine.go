package legacymigrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	go_mysql "github.com/go-sql-driver/mysql"
)

// SourceName 是迁移来源标识，写入 legacy_id_map.source。
const SourceName = "shopping.sql"

// entityType 枚举（与 legacy_id_map.entity_type 一致）。
const (
	entityCategory = "category"
	entityProduct  = "product"
	entitySKU      = "sku"
)

// Engine 执行历史数据迁移：从 staging 库（source）读历史表，写入目标库（target）。
// source 与 target 必须指向不同的数据库 schema（staging vs 目标），目标库绝不执行
// SET FOREIGN_KEY_CHECKS=0（INV-004）。
type Engine struct {
	source *sql.DB
	target *sql.DB
}

// NewEngine 构造迁移引擎。
func NewEngine(source, target *sql.DB) *Engine {
	return &Engine{source: source, target: target}
}

// Run 执行迁移。dryRun=true 时仅解析/映射/校验并统计将执行的变更，不写目标库。
func (e *Engine) Run(ctx context.Context, dryRun bool) (*Result, error) {
	result := &Result{DryRun: dryRun}

	migrated, err := e.loadMigratedMap(ctx)
	if err != nil {
		return nil, err
	}

	categoryOldToNew, err := e.migrateCategories(ctx, migrated, dryRun, result)
	if err != nil {
		return nil, err
	}

	if err := e.migrateProducts(ctx, migrated, categoryOldToNew, dryRun, result); err != nil {
		return nil, err
	}

	return result, nil
}

// entityKey 构造幂等映射键（source 固定，故只需 entity_type + old_id）。
func entityKey(entityType string, oldID int64) string {
	return entityType + ":" + strconv.FormatInt(oldID, 10)
}

// loadMigratedMap 读取目标库 legacy_id_map 中本来源已迁移的 old_id → new_id 映射。
func (e *Engine) loadMigratedMap(ctx context.Context) (map[string]int64, error) {
	rows, err := e.target.QueryContext(ctx,
		"SELECT entity_type, old_id, new_id FROM legacy_id_map WHERE source = ?", SourceName)
	if err != nil {
		if isTableNotExist(err) {
			return nil, fmt.Errorf("目标库缺少 legacy_id_map 表，请先执行 `my-shop migrate up`")
		}
		return nil, fmt.Errorf("读取 legacy_id_map: %w", err)
	}
	defer rows.Close()

	m := make(map[string]int64)
	for rows.Next() {
		var entityType string
		var oldID, newID int64
		if err := rows.Scan(&entityType, &oldID, &newID); err != nil {
			return nil, fmt.Errorf("扫描 legacy_id_map: %w", err)
		}
		m[entityKey(entityType, oldID)] = newID
	}
	return m, rows.Err()
}

// migrateCategories 迁移分类，返回旧分类 ID → 新分类 ID 的完整映射（含已迁移与本次新增）。
// import 模式在一个事务内完成「插入（parent_id 暂置 0）+ 回填 parent_id + 写映射」，
// 保证分类与其映射行同事务、任一步失败整体回滚（INV-006）。
func (e *Engine) migrateCategories(ctx context.Context, migrated map[string]int64, dryRun bool, result *Result) (map[int64]int64, error) {
	cats, err := e.loadLegacyCategories(ctx)
	if err != nil {
		return nil, err
	}

	oldToNew := make(map[int64]int64, len(cats))
	// 先并入已迁移的分类，供父分类/商品关联复用。
	for _, c := range cats {
		if nid, ok := migrated[entityKey(entityCategory, c.ID)]; ok {
			oldToNew[c.ID] = nid
		}
	}

	if dryRun {
		for _, c := range cats {
			if _, ok := oldToNew[c.ID]; ok {
				continue
			}
			if strings.TrimSpace(c.Name) == "" {
				result.addSkip(entityCategory, c.ID, "分类名为空")
				continue
			}
			oldToNew[c.ID] = 0 // 占位：将迁移，新 ID 待实际写入确定
			result.CategoriesMigrated++
		}
		return oldToNew, nil
	}

	// import：单事务内完成全部新增分类的插入与 parent_id 回填。
	tx, err := e.target.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启分类迁移事务: %w", err)
	}
	defer tx.Rollback()

	inserted := make([]*LegacyCategory, 0, len(cats))
	for _, c := range cats {
		if _, ok := oldToNew[c.ID]; ok {
			continue // 已迁移
		}
		if strings.TrimSpace(c.Name) == "" {
			result.addSkip(entityCategory, c.ID, "分类名为空")
			continue
		}
		// 第一阶段：parent_id 暂置 0，随后统一回填。
		res, err := tx.ExecContext(ctx,
			"INSERT INTO categories (parent_id, name, sort, status) VALUES (?, ?, ?, ?)",
			0, c.Name, c.Sort, MapCategory(c).Status)
		if err != nil {
			return nil, fmt.Errorf("插入分类 old_id=%d: %w", c.ID, err)
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("读取分类新 ID old_id=%d: %w", c.ID, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO legacy_id_map (source, entity_type, old_id, new_id) VALUES (?, ?, ?, ?)",
			SourceName, entityCategory, c.ID, newID); err != nil {
			return nil, fmt.Errorf("写入分类映射 old_id=%d: %w", c.ID, err)
		}
		oldToNew[c.ID] = newID
		inserted = append(inserted, c)
		result.CategoriesMigrated++
	}

	// 第二阶段：回填 parent_id（pid 引用缺失则置 0 顶级，并记录）。
	for _, c := range inserted {
		newPID := int64(0)
		if c.PID != 0 {
			if pid, ok := oldToNew[c.PID]; ok {
				newPID = pid
			} else {
				result.addSkip(entityCategory, c.ID, fmt.Sprintf("父分类 old_id=%d 未迁移，parent_id 置 0", c.PID))
			}
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE categories SET parent_id = ? WHERE id = ?", newPID, oldToNew[c.ID]); err != nil {
			return nil, fmt.Errorf("回填分类 parent_id old_id=%d: %w", c.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交分类迁移事务: %w", err)
	}
	return oldToNew, nil
}

// migrateProducts 迁移商品及其图册、SKU、库存：每个商品一个事务（业务记录与映射行同事务）。
func (e *Engine) migrateProducts(ctx context.Context, migrated map[string]int64, categoryOldToNew map[int64]int64, dryRun bool, result *Result) error {
	products, err := e.loadLegacyProducts(ctx)
	if err != nil {
		return err
	}
	attrValues, err := e.loadLegacyAttrValues(ctx)
	if err != nil {
		return err
	}
	attrDims, err := e.loadLegacyAttrDims(ctx)
	if err != nil {
		return err
	}

	for _, p := range products {
		if _, ok := migrated[entityKey(entityProduct, p.ID)]; ok {
			result.ProductsSkipped++ // 已迁移，跳过（不重复插入）
			continue
		}

		cateID, err := parseCateID(p.CateID)
		if err != nil {
			result.addSkip(entityProduct, p.ID, "分类关联非法: "+err.Error())
			continue
		}
		newCateID, ok := categoryOldToNew[cateID]
		if !ok {
			result.addSkip(entityProduct, p.ID, fmt.Sprintf("引用不存在或未迁移的分类 cate_id=%d", cateID))
			continue
		}

		pt, images, err := MapProduct(p)
		if err != nil {
			result.addSkip(entityProduct, p.ID, err.Error())
			continue
		}
		pt.CategoryID = newCateID

		skuMappings, err := MapSkus(pt.Price, p.Stock, pt.MainImage, pt.Status, attrValues[p.ID], attrDims[p.ID])
		if err != nil {
			result.addSkip(entityProduct, p.ID, err.Error())
			continue
		}

		if dryRun {
			result.ProductsMigrated++
			result.SkusMigrated += len(skuMappings)
			result.InventoriesMigrated += len(skuMappings)
			continue
		}

		if err := e.writeProduct(ctx, p.ID, pt, images, skuMappings, result); err != nil {
			result.addFailure("商品 old_id=%d 迁移失败（已回滚）: %v", p.ID, err)
			continue
		}
		result.ProductsMigrated++
		result.SkusMigrated += len(skuMappings)
		result.InventoriesMigrated += len(skuMappings)
	}
	return nil
}

// writeProduct 在一个事务内写入商品、图册、SKU、库存及对应映射行（INV-006）。
func (e *Engine) writeProduct(ctx context.Context, productOldID int64, pt ProductTarget, images []string, skuMappings []SkuMapping, result *Result) error {
	tx, err := e.target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		"INSERT INTO products (name, brand, category_id, price, main_image, detail, status) VALUES (?, ?, ?, ?, ?, ?, ?)",
		pt.Name, pt.Brand, pt.CategoryID, pt.Price, pt.MainImage, pt.Detail, pt.Status)
	if err != nil {
		return fmt.Errorf("插入 products: %w", err)
	}
	productNewID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("读取商品新 ID: %w", err)
	}

	for i, url := range images {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO product_images (product_id, url, sort) VALUES (?, ?, ?)",
			productNewID, url, i); err != nil {
			return fmt.Errorf("插入 product_images: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		"INSERT INTO legacy_id_map (source, entity_type, old_id, new_id) VALUES (?, ?, ?, ?)",
		SourceName, entityProduct, productOldID, productNewID); err != nil {
		return fmt.Errorf("写入商品映射: %w", err)
	}

	for _, sm := range skuMappings {
		res, err := tx.ExecContext(ctx,
			"INSERT INTO skus (product_id, name, price, status) VALUES (?, ?, ?, ?)",
			productNewID, sm.Sku.Name, sm.Sku.Price, sm.Sku.Status)
		if err != nil {
			return fmt.Errorf("插入 skus: %w", err)
		}
		skuNewID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("读取 SKU 新 ID: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO inventories (sku_id, quantity) VALUES (?, ?)",
			skuNewID, sm.Inventory.Quantity); err != nil {
			return fmt.Errorf("插入 inventories: %w", err)
		}
		// 仅「有历史明细行」的 SKU 记录映射；默认 SKU（OldID=0）无独立历史主键，不记录。
		if sm.OldID > 0 {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO legacy_id_map (source, entity_type, old_id, new_id) VALUES (?, ?, ?, ?)",
				SourceName, entitySKU, sm.OldID, skuNewID); err != nil {
				return fmt.Errorf("写入 SKU 映射: %w", err)
			}
		}
	}

	return tx.Commit()
}

// loadLegacyCategories 读取 staging 库 store_category 全量行（含软删除，按 pid, id 排序）。
func (e *Engine) loadLegacyCategories(ctx context.Context) ([]*LegacyCategory, error) {
	rows, err := e.source.QueryContext(ctx,
		"SELECT id, pid, cate_name, sort, is_show, is_del FROM store_category ORDER BY pid, id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_category: %w", err)
	}
	defer rows.Close()

	var out []*LegacyCategory
	for rows.Next() {
		var c LegacyCategory
		if err := rows.Scan(&c.ID, &c.PID, &c.Name, &c.Sort, &c.IsShow, &c.IsDel); err != nil {
			return nil, fmt.Errorf("扫描 store_category: %w", err)
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// loadLegacyProducts 读取 staging 库 store_product 全量行。
func (e *Engine) loadLegacyProducts(ctx context.Context) ([]*LegacyProduct, error) {
	rows, err := e.source.QueryContext(ctx,
		"SELECT id, cate_id, price, stock, is_show, is_del, spec_type, image, slider_image, store_name, description FROM store_product ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_product: %w", err)
	}
	defer rows.Close()

	var out []*LegacyProduct
	for rows.Next() {
		var p LegacyProduct
		var price, cateID sql.NullString
		var stock sql.NullInt64
		if err := rows.Scan(&p.ID, &cateID, &price, &stock, &p.IsShow, &p.IsDel, &p.SpecType, &p.Image, &p.SliderImage, &p.StoreName, &p.Description); err != nil {
			return nil, fmt.Errorf("扫描 store_product: %w", err)
		}
		p.CateID = cateID.String
		if price.Valid {
			p.Price = price.String
		}
		if stock.Valid {
			p.Stock = stock.Int64
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// loadLegacyAttrValues 读取 staging 库 store_product_attr_value 全量行并按 product_id 分组。
func (e *Engine) loadLegacyAttrValues(ctx context.Context) (map[int64][]*LegacyAttrValue, error) {
	rows, err := e.source.QueryContext(ctx,
		"SELECT id, product_id, sku, stock, price, image FROM store_product_attr_value ORDER BY product_id, id")
	if err != nil {
		return nil, fmt.Errorf("读取 store_product_attr_value: %w", err)
	}
	defer rows.Close()

	out := make(map[int64][]*LegacyAttrValue)
	for rows.Next() {
		var av LegacyAttrValue
		var price sql.NullString
		var stock sql.NullInt64
		if err := rows.Scan(&av.ID, &av.ProductID, &av.SKU, &stock, &price, &av.Image); err != nil {
			return nil, fmt.Errorf("扫描 store_product_attr_value: %w", err)
		}
		if price.Valid {
			av.Price = price.String
		}
		if stock.Valid {
			av.Stock = stock.Int64
		}
		out[av.ProductID] = append(out[av.ProductID], &av)
	}
	return out, rows.Err()
}

// loadLegacyAttrDims 读取 staging 库 store_product_attr 每个商品的规格维度数（单/多规格判定依据）。
func (e *Engine) loadLegacyAttrDims(ctx context.Context) (map[int64]int, error) {
	rows, err := e.source.QueryContext(ctx,
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

// isTableNotExist 判断是否为 MySQL「表不存在」错误（1146）。
func isTableNotExist(err error) bool {
	var mysqlErr *go_mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1146
}
