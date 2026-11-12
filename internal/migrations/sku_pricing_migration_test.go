package migrations

// B1-04-02 SKU 编码/原价迁移测试：
//   - skus 新增 sku_code（唯一）与 original_price，历史行回填为 SKU<id> 与当时售价；
//   - uk_sku_code 拒绝重复编码，同时允许历史/离线写入的多个 NULL（向前兼容）；
//   - order_items 新增 sku_code 与 original_price 快照列；
//   - down → up 后新增列/索引可正确重建，既有 SKU 行不丢失。
//
// 这些断言需要真实 MySQL（与其它迁移测试同一环境假设，DATABASE_DEFAULT_PASS 由测试入口注入）。

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

const skuCodePricingMigrationVersion = uint(20261111000001)

// TestSkuCodeAndOriginalPriceBackfill 覆盖历史行回填与唯一约束语义。
func TestSkuCodeAndOriginalPriceBackfill(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	// 先迁移到本批次之前，写入「历史 SKU」，再执行本迁移验证回填。
	migrateTo(t, 20261110000015)

	categoryID := insertCategory(t, db, "SKU 编码分类")
	productID := insertProduct(t, db, categoryID, "SKU 编码商品")
	skuID := insertSku(t, db, productID, "默认", 2999, nil, "")

	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	// 历史行回填：sku_code = SKU<10 位左补零 id>；original_price 回退售价。
	var code string
	var originalPrice int64
	if err := db.QueryRowContext(ctx,
		"SELECT sku_code, original_price FROM skus WHERE id = ?", skuID).Scan(&code, &originalPrice); err != nil {
		t.Fatalf("读取回填结果: %v", err)
	}
	wantCode := "SKU" + leftPadID(skuID)
	if code != wantCode {
		t.Fatalf("sku_code=%q want %q", code, wantCode)
	}
	if originalPrice != 2999 {
		t.Fatalf("original_price=%d want 2999", originalPrice)
	}

	// 任意商品下重复 sku_code → 唯一约束拒绝（编码是全局业务键）。
	otherCategory := insertCategory(t, db, "SKU 编码分类2")
	otherProduct := insertProduct(t, db, otherCategory, "SKU 编码商品2")
	_, err := db.ExecContext(ctx,
		"INSERT INTO skus (sku_code, product_id, name, price, status, original_price) VALUES (?, ?, '其它', 100, 1, 100)",
		code, otherProduct)
	requireMySQLError(t, err, 1062, "重复 sku_code")

	// NULL 编码允许多行：兼容迁移工具/离线脚本的写入（应用层始终生成编码）。
	if _, err := db.ExecContext(ctx,
		"INSERT INTO skus (product_id, name, price, status) VALUES (?, '历史无编码A', 100, 1)", otherProduct); err != nil {
		t.Fatalf("插入 NULL 编码 A: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO skus (product_id, name, price, status) VALUES (?, '历史无编码B', 100, 1)", otherProduct); err != nil {
		t.Fatalf("插入 NULL 编码 B: %v", err)
	}

	// order_items 快照列存在且默认空串/0。
	orderItems := readActualTable(t, db, "order_items")
	for _, col := range []string{"sku_code", "original_price"} {
		if !hasColumn(orderItems, col) {
			t.Fatalf("order_items 缺少列 %s", col)
		}
	}
}

// TestSkuCodePricingMigrationDownUp 覆盖 down → up：新增列/索引完整回滚并可重建，既有行不丢失。
func TestSkuCodePricingMigrationDownUp(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	categoryID := insertCategory(t, db, "回滚分类")
	productID := insertProduct(t, db, categoryID, "回滚商品")
	skuID := insertSku(t, db, productID, "回滚 SKU", 1999, nil, "")

	migrateTo(t, 20261110000015)
	skuSpec := readActualTable(t, db, "skus")
	for _, col := range []string{"sku_code", "original_price"} {
		if hasColumn(skuSpec, col) {
			t.Fatalf("down 后 skus 不应存在列 %s", col)
		}
	}
	if _, ok := findIndex(skuSpec, "uk_sku_code"); ok {
		t.Fatalf("down 后不应存在索引 uk_sku_code")
	}
	orderItems := readActualTable(t, db, "order_items")
	for _, col := range []string{"sku_code", "original_price"} {
		if hasColumn(orderItems, col) {
			t.Fatalf("down 后 order_items 不应存在列 %s", col)
		}
	}
	var name string
	var price int64
	if err := db.QueryRowContext(ctx, "SELECT name, price FROM skus WHERE id = ?", skuID).Scan(&name, &price); err != nil {
		t.Fatalf("down 后读取既有 SKU: %v", err)
	}
	if name != "回滚 SKU" || price != 1999 {
		t.Fatalf("down 后既有 SKU 被修改: name=%q price=%d", name, price)
	}

	if err := Up(ctx); err != nil {
		t.Fatalf("重新 up: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Fatalf("重新 up 后 version=%d want %d", v, latestMigrationVersion)
	}
	skuSpec = readActualTable(t, db, "skus")
	if _, ok := findIndex(skuSpec, "uk_sku_code"); !ok {
		t.Fatalf("重新 up 后应存在索引 uk_sku_code")
	}
	// 既有行在重新 up 时被再次回填，编码保持稳定（同一派生规则）。
	var code string
	var originalPrice int64
	if err := db.QueryRowContext(ctx,
		"SELECT sku_code, original_price FROM skus WHERE id = ?", skuID).Scan(&code, &originalPrice); err != nil {
		t.Fatalf("重新 up 后读取 SKU: %v", err)
	}
	if code != "SKU"+leftPadID(skuID) || originalPrice != 1999 {
		t.Fatalf("重新 up 后回填不符: code=%q original_price=%d", code, originalPrice)
	}
}

// TestSkuCodePricingMigrationFilesArePaired 覆盖迁移文件配对与版本递增。
func TestSkuCodePricingMigrationFilesArePaired(t *testing.T) {
	entries, err := fs.ReadDir(embeddedMigrations, migrationDir)
	if err != nil {
		t.Fatalf("read migration dir: %v", err)
	}
	ups := map[uint]bool{}
	downs := map[uint]bool{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		v, err := parseVersion(entry.Name())
		if err != nil {
			t.Fatalf("解析迁移版本 %q: %v", entry.Name(), err)
		}
		switch {
		case strings.HasSuffix(entry.Name(), ".up.sql"):
			ups[v] = true
		case strings.HasSuffix(entry.Name(), ".down.sql"):
			downs[v] = true
		}
	}
	if !ups[skuCodePricingMigrationVersion] {
		t.Fatalf("缺少 up 迁移: %d", skuCodePricingMigrationVersion)
	}
	if !downs[skuCodePricingMigrationVersion] {
		t.Fatalf("缺少 down 迁移: %d", skuCodePricingMigrationVersion)
	}
	if skuCodePricingMigrationVersion <= 20261110000015 {
		t.Fatalf("迁移版本 %d 必须大于上一批次最高版本 20261110000015", skuCodePricingMigrationVersion)
	}
}

// leftPadID 复刻迁移中的 SKU 编码派生规则（LPAD(id, 10, '0')）。
func leftPadID(id int64) string {
	return fmt.Sprintf("%010d", id)
}
