package legacymigrate

// 迁移引擎集成测试：需真实 MySQL（staging 测试库 + 目标库）。
// 覆盖 CLEAN-001（跨父级同名分类）、CLEAN-002（报告统计一致）、
// CLEAN-003（幂等 / 失败原子性 / ID 映射持久化）三类核心不变量。
// 环境假设与 internal/migrations、internal/cmd 集成测试一致：MySQL 就绪 + DATABASE_DEFAULT_PASS 注入。

import (
	"context"
	"database/sql"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

// testStagingDB 是集成测试使用的隔离 staging 库名（与生产 staging 库名区分，避免污染）。
const testStagingDB = "my_shop_staging_test"

// setupEngineTest 建立隔离的 staging 测试库 + 目标库连接：
//   - staging 库：建历史 4 表（store_category/store_product/store_product_attr/store_product_attr_value）；
//   - 目标库：migrations.Up 确保 schema 就绪，并清理迁移相关业务数据。
//
// cleanup 关闭连接并 drop staging 测试库。
func setupEngineTest(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()
	ctx := context.Background()

	host := cfgString(ctx, "database.default.host", defaultDBHost)
	port := cfgString(ctx, "database.default.port", defaultDBPort)
	user := cfgString(ctx, "database.default.user", defaultDBUser)
	pass := cfgString(ctx, "database.default.pass", "")
	targetName := cfgString(ctx, "database.default.name", defaultDBName)

	// root 连接（无库名）用于创建/清理 staging 测试库。
	root, err := sql.Open("mysql", buildDSN(host, port, user, pass, ""))
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()
	if _, err := root.ExecContext(ctx, "DROP DATABASE IF EXISTS `"+testStagingDB+"`"); err != nil {
		t.Fatalf("drop staging db: %v", err)
	}
	if _, err := root.ExecContext(ctx, "CREATE DATABASE `"+testStagingDB+"` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatalf("create staging db: %v", err)
	}

	source, err := sql.Open("mysql", buildDSN(host, port, user, pass, testStagingDB))
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	createLegacyTables(t, source)

	target, err := sql.Open("mysql", buildDSN(host, port, user, pass, targetName))
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	// 确保目标库 schema 就绪（幂等）。
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	// 清理迁移相关业务数据（按 FK 依赖顺序：被引用方先删）。
	for _, table := range []string{"inventories", "skus", "product_images", "products", "categories", "legacy_id_map"} {
		if _, err := target.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}

	t.Cleanup(func() {
		source.Close()
		target.Close()
		if rc, err := sql.Open("mysql", buildDSN(host, port, user, pass, "")); err == nil {
			_, _ = rc.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+testStagingDB+"`")
			rc.Close()
		}
	})
	return source, target
}

// createLegacyTables 在 staging 库建立历史 4 表（字段与引擎查询一致）。
func createLegacyTables(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	ddls := []string{
		`CREATE TABLE store_category (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			pid BIGINT UNSIGNED NOT NULL DEFAULT 0,
			cate_name VARCHAR(64) NOT NULL,
			sort INT NOT NULL DEFAULT 0,
			pic VARCHAR(512) NOT NULL DEFAULT '',
			is_show TINYINT NOT NULL DEFAULT 1,
			is_del TINYINT NOT NULL DEFAULT 0,
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_product (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			cate_id VARCHAR(32) NOT NULL DEFAULT '',
			price DECIMAL(8,2) NULL,
			stock INT NOT NULL DEFAULT 0,
			is_show TINYINT NOT NULL DEFAULT 1,
			is_del TINYINT NOT NULL DEFAULT 0,
			spec_type TINYINT NOT NULL DEFAULT 0,
			image VARCHAR(512) NOT NULL DEFAULT '',
			slider_image TEXT NULL,
			store_name VARCHAR(128) NOT NULL DEFAULT '',
			store_info TEXT NULL,
			description TEXT NULL,
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_product_attr (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			product_id BIGINT UNSIGNED NOT NULL,
			attr_name VARCHAR(64) NOT NULL,
			attr_values TEXT NULL,
			PRIMARY KEY (id),
			KEY idx_product_id (product_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_product_attr_value (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			product_id BIGINT UNSIGNED NOT NULL,
			sku VARCHAR(128) NOT NULL DEFAULT '',
			stock INT NOT NULL DEFAULT 0,
			price DECIMAL(8,2) NULL,
			image VARCHAR(512) NOT NULL DEFAULT '',
			PRIMARY KEY (id),
			KEY idx_product_id (product_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, ddl := range ddls {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("create legacy table: %v", err)
		}
	}
}

// countRows 返回表行数。
func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestEngineCategorySameNameDifferentParent 覆盖 CLEAN-001 回归：
// 不同父级下的同名分类是合法数据，应按各自最终 parent_id 落库，不再因两阶段临时 parent_id=0 触发 1062。
func TestEngineCategorySameNameDifferentParent(t *testing.T) {
	source, target := setupEngineTest(t)
	ctx := context.Background()

	// 数码(顶级) → 配件(数码下)；配件(顶级)。两个「配件」分属不同父级。
	for _, row := range [][3]any{
		{1, 0, "数码"},
		{2, 1, "配件"},
		{3, 0, "配件"},
	} {
		if _, err := source.ExecContext(ctx, "INSERT INTO store_category (id, pid, cate_name, sort, is_show, is_del) VALUES (?, ?, ?, 0, 1, 0)", row[0], row[1], row[2]); err != nil {
			t.Fatalf("insert store_category: %v", err)
		}
	}

	result, err := NewEngine(source, target).Run(ctx, false)
	if err != nil {
		t.Fatalf("import 失败: %v", err)
	}
	if result.CategoriesMigrated != 3 {
		t.Fatalf("期望迁移 3 个分类, 实际 %d", result.CategoriesMigrated)
	}
	if result.CategoriesSkipped != 0 {
		t.Fatalf("期望 0 跳过, 实际 %d（报告: %s）", result.CategoriesSkipped, result.String())
	}
	if n := countRows(t, target, "categories"); n != 3 {
		t.Fatalf("期望 categories 3 行, 实际 %d", n)
	}
	// 数码(pid=0) 的子分类「配件」应挂在数码下（parent_id != 0）。
	var childParent int64
	if err := target.QueryRowContext(ctx, "SELECT parent_id FROM categories WHERE name = '配件' AND parent_id != 0").Scan(&childParent); err != nil {
		t.Fatalf("查询子分类 parent_id: %v", err)
	}
	if childParent == 0 {
		t.Fatalf("跨父级同名分类应保留父级关系, 但子分类 parent_id 为 0")
	}
}

// TestEngineCategoryReportConsistency 覆盖 CLEAN-002 回归：
// 父缺失时子分类降级为顶级（成功），不应被重复计入「跳过」；报告成功+跳过 == 源分类数。
func TestEngineCategoryReportConsistency(t *testing.T) {
	source, target := setupEngineTest(t)
	ctx := context.Background()

	// 99 名为空（跳过）；100 的父 99 被跳过 → 100 降级为顶级（成功）；1 正常。
	for _, row := range [][3]any{
		{99, 0, ""},
		{100, 99, "孤儿分类"},
		{1, 0, "正常"},
	} {
		if _, err := source.ExecContext(ctx, "INSERT INTO store_category (id, pid, cate_name, sort, is_show, is_del) VALUES (?, ?, ?, 0, 1, 0)", row[0], row[1], row[2]); err != nil {
			t.Fatalf("insert store_category: %v", err)
		}
	}

	result, err := NewEngine(source, target).Run(ctx, false)
	if err != nil {
		t.Fatalf("import 失败: %v", err)
	}
	if result.CategoriesMigrated != 2 || result.CategoriesSkipped != 1 {
		t.Fatalf("期望成功 2 / 跳过 1, 实际成功 %d / 跳过 %d", result.CategoriesMigrated, result.CategoriesSkipped)
	}
	if n := countRows(t, target, "categories"); n != 2 {
		t.Fatalf("期望 categories 2 行, 实际 %d", n)
	}
	// 降级说明应存在（父缺失置 0），但孤儿分类本身算成功、不在跳过明细里。
	if len(result.Warnings) == 0 {
		t.Fatalf("期望存在父缺失降级说明, 实际无 Warnings")
	}
	for _, s := range result.Skipped {
		if s.OldID == 100 {
			t.Fatalf("孤儿分类不应计入跳过明细: %+v", s)
		}
	}
}

// TestEngineCategoryCyclicParent 覆盖 CLEAN-004 回归：
// 环状父链（A.pid=B 且 B.pid=A）被打破时，每个分类只插入一次，无重复分类行，报告与库内一致。
func TestEngineCategoryCyclicParent(t *testing.T) {
	source, target := setupEngineTest(t)
	ctx := context.Background()

	// A(id=1,pid=2) ↔ B(id=2,pid=1) 形成环。
	for _, row := range [][3]any{
		{1, 2, "A"},
		{2, 1, "B"},
	} {
		if _, err := source.ExecContext(ctx, "INSERT INTO store_category (id, pid, cate_name, sort, is_show, is_del) VALUES (?, ?, ?, 0, 1, 0)", row[0], row[1], row[2]); err != nil {
			t.Fatalf("insert store_category: %v", err)
		}
	}

	result, err := NewEngine(source, target).Run(ctx, false)
	if err != nil {
		t.Fatalf("import 失败: %v", err)
	}
	if result.CategoriesMigrated != 2 || result.CategoriesSkipped != 0 {
		t.Fatalf("期望成功 2 / 跳过 0, 实际成功 %d / 跳过 %d（报告: %s）", result.CategoriesMigrated, result.CategoriesSkipped, result.String())
	}
	if n := countRows(t, target, "categories"); n != 2 {
		t.Fatalf("环被打破后应只有 2 行, 实际 %d", n)
	}
	// 不允许出现重复分类名（环被打破时每个节点只插入一次）。
	var dup int
	if err := target.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT name FROM categories GROUP BY name HAVING COUNT(*) > 1) t").Scan(&dup); err != nil {
		t.Fatalf("查询重复分类: %v", err)
	}
	if dup != 0 {
		t.Fatalf("存在重复分类名, 重复行数 %d", dup)
	}
	// 报告处理数（成功+跳过）与库内实际行数一致（AC-012）。
	if got := result.CategoriesMigrated + result.CategoriesSkipped; got != countRows(t, target, "categories") {
		t.Fatalf("报告处理数 %d 与库内行数 %d 不一致", got, countRows(t, target, "categories"))
	}
}

// TestEngineImportIdempotentAndIDMap 覆盖 CLEAN-003（INV-003 幂等 + ID 映射持久化）：
// 重复 import 行数不变、不重复插入；legacy_id_map 记录 category/product/sku 映射。
func TestEngineImportIdempotentAndIDMap(t *testing.T) {
	source, target := setupEngineTest(t)
	ctx := context.Background()

	seedLegacyData(t, source)

	engine := NewEngine(source, target)
	first, err := engine.Run(ctx, false)
	if err != nil {
		t.Fatalf("第一次 import 失败: %v", err)
	}
	if first.CategoriesMigrated == 0 || first.ProductsMigrated == 0 || first.SkusMigrated == 0 {
		t.Fatalf("首次迁移统计异常: %s", first.String())
	}

	// 记录首次落库行数。
	base := map[string]int{
		"categories":    countRows(t, target, "categories"),
		"products":      countRows(t, target, "products"),
		"skus":          countRows(t, target, "skus"),
		"inventories":   countRows(t, target, "inventories"),
		"legacy_id_map": countRows(t, target, "legacy_id_map"),
	}

	second, err := engine.Run(ctx, false)
	if err != nil {
		t.Fatalf("第二次 import 失败: %v", err)
	}
	if second.CategoriesMigrated != 0 || second.ProductsMigrated != 0 || second.SkusMigrated != 0 || second.InventoriesMigrated != 0 {
		t.Fatalf("第二次 import 应 0 新增, 实际 %s", second.String())
	}
	for table, want := range base {
		if got := countRows(t, target, table); got != want {
			t.Fatalf("幂等失败：%s 行数 %d → %d", table, want, got)
		}
	}

	// ID 映射持久化：三种实体类型均有映射行。
	for _, et := range []string{entityCategory, entityProduct, entitySKU} {
		var n int
		if err := target.QueryRowContext(ctx, "SELECT COUNT(*) FROM legacy_id_map WHERE source = ? AND entity_type = ?", SourceName, et).Scan(&n); err != nil {
			t.Fatalf("查询映射 %s: %v", et, err)
		}
		if n == 0 {
			t.Fatalf("legacy_id_map 缺少 entity_type=%s 映射", et)
		}
	}
}

// TestEngineImportSkipsInvalidNoResidue 覆盖 CLEAN-003（INV-006 失败原子/无残留）：
// 引用不存在分类的商品被跳过，目标库不留下该商品的任何孤儿行。
func TestEngineImportSkipsInvalidNoResidue(t *testing.T) {
	source, target := setupEngineTest(t)
	ctx := context.Background()

	// 正常分类 + 正常商品；坏商品引用不存在的分类 cate_id=999。
	if _, err := source.ExecContext(ctx, "INSERT INTO store_category (id, pid, cate_name, sort, is_show, is_del) VALUES (1, 0, '正常', 0, 1, 0)"); err != nil {
		t.Fatalf("insert category: %v", err)
	}
	if _, err := source.ExecContext(ctx, "INSERT INTO store_product (id, cate_id, price, stock, is_show, is_del, image, store_name) VALUES (10, '1', '9.90', 5, 1, 0, '/a.jpg', '好商品')"); err != nil {
		t.Fatalf("insert good product: %v", err)
	}
	if _, err := source.ExecContext(ctx, "INSERT INTO store_product (id, cate_id, price, stock, is_show, is_del, image, store_name) VALUES (11, '999', '9.90', 5, 1, 0, '/b.jpg', '坏商品')"); err != nil {
		t.Fatalf("insert bad product: %v", err)
	}

	result, err := NewEngine(source, target).Run(ctx, false)
	if err != nil {
		t.Fatalf("import 失败: %v", err)
	}
	if result.ProductsMigrated != 1 || result.ProductsSkipped != 1 {
		t.Fatalf("期望商品成功 1 / 跳过 1, 实际 %d / %d", result.ProductsMigrated, result.ProductsSkipped)
	}
	// 坏商品不产生任何 products/skus/inventories/legacy_id_map 孤儿行。
	if n := countRows(t, target, "products"); n != 1 {
		t.Fatalf("期望 products 1 行, 实际 %d", n)
	}
	if n := countRows(t, target, "skus"); n != 1 {
		t.Fatalf("期望 skus 1 行, 实际 %d", n)
	}
	if n := countRows(t, target, "inventories"); n != 1 {
		t.Fatalf("期望 inventories 1 行, 实际 %d", n)
	}
	var orphan int
	if err := target.QueryRowContext(ctx, "SELECT COUNT(*) FROM legacy_id_map WHERE entity_type = 'product' AND old_id = 11").Scan(&orphan); err != nil {
		t.Fatalf("查询孤儿映射: %v", err)
	}
	if orphan != 0 {
		t.Fatalf("坏商品不应写入映射, 实际 %d 行", orphan)
	}
}

// seedLegacyData 向 staging 库插入一组含单/多规格的合成数据，供幂等测试复用。
func seedLegacyData(t *testing.T, source *sql.DB) {
	t.Helper()
	ctx := context.Background()

	// 分类：数码(1,顶级)、数码→手机(2)。
	for _, row := range [][3]any{{1, 0, "数码"}, {2, 1, "手机"}} {
		if _, err := source.ExecContext(ctx, "INSERT INTO store_category (id, pid, cate_name, sort, is_show, is_del) VALUES (?, ?, ?, 0, 1, 0)", row[0], row[1], row[2]); err != nil {
			t.Fatalf("insert store_category: %v", err)
		}
	}
	// 单规格商品 10（无 attr_value 行，商品级 stock=50，price=19.90）。
	if _, err := source.ExecContext(ctx, "INSERT INTO store_product (id, cate_id, price, stock, is_show, is_del, image, store_name, description) VALUES (10, '2', '19.90', 50, 1, 0, '/main1.jpg', '单规格手机', 'desc')"); err != nil {
		t.Fatalf("insert single product: %v", err)
	}
	// 多规格商品 20（2 个 SKU 明细）。
	if _, err := source.ExecContext(ctx, "INSERT INTO store_product (id, cate_id, price, stock, is_show, is_del, image, store_name) VALUES (20, '2', '29.90', 99, 1, 0, '/main2.jpg', '多规格手机')"); err != nil {
		t.Fatalf("insert multi product: %v", err)
	}
	// 多规格维度（2 个 attr_name）。
	for _, row := range [][3]any{{1, 20, "颜色"}, {2, 20, "容量"}} {
		if _, err := source.ExecContext(ctx, "INSERT INTO store_product_attr (id, product_id, attr_name, attr_values) VALUES (?, ?, ?, 'x')", row[0], row[1], row[2]); err != nil {
			t.Fatalf("insert store_product_attr: %v", err)
		}
	}
	// 多规格 SKU 明细。
	for _, row := range [][5]any{
		{1, 20, "4+64G,黑", 10, "29.90"},
		{2, 20, "8+128G,白", 20, "39.90"},
	} {
		if _, err := source.ExecContext(ctx, "INSERT INTO store_product_attr_value (id, product_id, sku, stock, price, image) VALUES (?, ?, ?, ?, ?, '')", row[0], row[1], row[2], row[3], row[4]); err != nil {
			t.Fatalf("insert store_product_attr_value: %v", err)
		}
	}
}
