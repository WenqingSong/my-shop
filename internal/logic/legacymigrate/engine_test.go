package legacymigrate

// 迁移引擎集成测试：需真实 MySQL（staging 测试库 + 目标库）。
// 覆盖 CLEAN-001（跨父级同名分类）、CLEAN-002（报告统计一致）、
// CLEAN-003（幂等 / 失败原子性 / ID 映射持久化）三类核心不变量。
// 环境假设与 internal/migrations、internal/cmd 集成测试一致：MySQL 就绪 + DATABASE_DEFAULT_PASS 注入。

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

// setupEngineTest 建立隔离的 staging 测试库 + 目标测试库连接：
//   - 先对配置目标库执行 migrations.Up，确保 schema 为当前版本；
//   - 复制目标库结构到独立测试库（进程号后缀），测试只清理测试库，绝不删除配置库数据；
//   - staging 库同样按进程号隔离，并建立历史源表。
//
// cleanup 关闭连接并 drop 两个测试库。
func setupEngineTest(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()
	ctx := context.Background()

	mustConfig := func(key config.Key) string {
		t.Helper()
		value, err := config.String(ctx, key)
		if err != nil {
			t.Fatalf("读取配置 %s: %v", key, err)
		}
		return value
	}
	host := mustConfig(config.KeyDatabaseDefaultHost)
	port := mustConfig(config.KeyDatabaseDefaultPort)
	user := mustConfig(config.KeyDatabaseDefaultUser)
	pass := mustConfig(config.KeyDatabaseDefaultPass)
	configuredTarget := mustConfig(config.KeyDatabaseDefaultName)

	// 确保配置目标库 schema 就绪（幂等），后续复制其结构。
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	suffix := fmt.Sprintf("%d", os.Getpid())
	stagingName := "my_shop_staging_test_" + suffix
	targetName := "my_shop_b10304_test_" + suffix

	root, err := sql.Open("mysql", buildDSN(host, port, user, pass, ""))
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()
	for _, name := range []string{stagingName, targetName} {
		if _, err := root.ExecContext(ctx, "DROP DATABASE IF EXISTS `"+name+"`"); err != nil {
			t.Fatalf("drop test db %s: %v", name, err)
		}
		if _, err := root.ExecContext(ctx, "CREATE DATABASE `"+name+"` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
			t.Fatalf("create test db %s: %v", name, err)
		}
	}

	cloneSchema(t, host, port, user, pass, configuredTarget, targetName)

	source, err := sql.Open("mysql", buildDSN(host, port, user, pass, stagingName))
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	createLegacyTables(t, source)

	target, err := sql.Open("mysql", buildDSN(host, port, user, pass, targetName))
	if err != nil {
		t.Fatalf("open target: %v", err)
	}

	t.Cleanup(func() {
		source.Close()
		target.Close()
		if rc, err := sql.Open("mysql", buildDSN(host, port, user, pass, "")); err == nil {
			_, _ = rc.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+stagingName+"`")
			_, _ = rc.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+targetName+"`")
			rc.Close()
		}
	})
	return source, target
}

// cloneSchema 把 from 库的全部表结构复制到 to 库。
// 仅测试夹具使用 SET FOREIGN_KEY_CHECKS=0 以消除建表顺序依赖；
// 迁移引擎连接绝不关闭外键校验（INV-004）。
func cloneSchema(t *testing.T, host, port, user, pass, from, to string) {
	t.Helper()
	ctx := context.Background()

	src, err := sql.Open("mysql", buildDSN(host, port, user, pass, from))
	if err != nil {
		t.Fatalf("open clone source %s: %v", from, err)
	}
	defer src.Close()
	dst, err := sql.Open("mysql", buildDSN(host, port, user, pass, to))
	if err != nil {
		t.Fatalf("open clone target %s: %v", to, err)
	}
	defer dst.Close()

	if _, err := dst.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
		t.Fatalf("clone disable fk: %v", err)
	}
	defer dst.ExecContext(context.Background(), "SET FOREIGN_KEY_CHECKS=1") //nolint:errcheck

	rows, err := src.QueryContext(ctx,
		"SELECT table_name FROM information_schema.tables WHERE table_schema = ?", from)
	if err != nil {
		t.Fatalf("list tables %s: %v", from, err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("scan table: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate tables: %v", err)
	}
	rows.Close()

	for _, name := range tables {
		var got, ddl string
		if err := src.QueryRowContext(ctx, "SHOW CREATE TABLE `"+name+"`").Scan(&got, &ddl); err != nil {
			t.Fatalf("show create table %s: %v", name, err)
		}
		if _, err := dst.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("clone table %s: %v", name, err)
		}
	}
}

// createLegacyTables 在 staging 库建立历史源表（字段与迁移引擎查询一致）。
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
			cate_id VARCHAR(64) NOT NULL DEFAULT '',
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
			create_time DATETIME NULL,
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
			` + "`unique`" + ` VARCHAR(100) NOT NULL DEFAULT '',
			PRIMARY KEY (id),
			KEY idx_product_id (product_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_product_attr_result (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			product_id BIGINT UNSIGNED NOT NULL,
			result TEXT NOT NULL,
			change_time DATETIME NULL,
			PRIMARY KEY (id),
			KEY idx_product_id (product_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE eshop_user (
			uid BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			username VARCHAR(255) NULL,
			password VARCHAR(255) NULL,
			nickname VARCHAR(100) NULL,
			avatar VARCHAR(256) NULL,
			phone CHAR(15) NULL,
			now_money DECIMAL(8,2) NOT NULL DEFAULT 0,
			integral DECIMAL(8,2) NOT NULL DEFAULT 0,
			status TINYINT NOT NULL DEFAULT 1,
			is_del TINYINT NOT NULL DEFAULT 0,
			create_time DATETIME NULL,
			PRIMARY KEY (uid)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE user_address (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			uid BIGINT UNSIGNED NOT NULL,
			real_name VARCHAR(32) NOT NULL DEFAULT '',
			phone VARCHAR(16) NOT NULL DEFAULT '',
			province VARCHAR(64) NOT NULL DEFAULT '',
			city VARCHAR(64) NOT NULL DEFAULT '',
			district VARCHAR(64) NOT NULL DEFAULT '',
			detail VARCHAR(256) NOT NULL DEFAULT '',
			city_id INT NULL,
			is_default TINYINT NOT NULL DEFAULT 0,
			is_del TINYINT NOT NULL DEFAULT 0,
			create_time DATETIME NULL,
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE system_city (
			id INT NOT NULL AUTO_INCREMENT,
			city_id INT NOT NULL DEFAULT 0,
			level INT NOT NULL DEFAULT 0,
			parent_id INT NOT NULL DEFAULT 0,
			name VARCHAR(100) NOT NULL DEFAULT '',
			PRIMARY KEY (id),
			KEY idx_city_id (city_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_order (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			order_id VARCHAR(32) NOT NULL,
			uid BIGINT UNSIGNED NOT NULL,
			real_name VARCHAR(32) NOT NULL DEFAULT '',
			user_phone VARCHAR(18) NOT NULL DEFAULT '',
			user_address VARCHAR(100) NOT NULL DEFAULT '',
			freight_price DECIMAL(8,2) NOT NULL DEFAULT 0,
			total_price DECIMAL(8,2) NOT NULL DEFAULT 0,
			total_postage DECIMAL(8,2) NOT NULL DEFAULT 0,
			pay_price DECIMAL(8,2) NOT NULL DEFAULT 0,
			pay_postage DECIMAL(8,2) NOT NULL DEFAULT 0,
			deduction_price DECIMAL(8,2) NOT NULL DEFAULT 0,
			coupon_id INT UNSIGNED NOT NULL DEFAULT 0,
			coupon_price DECIMAL(8,2) NOT NULL DEFAULT 0,
			paid TINYINT NOT NULL DEFAULT 0,
			pay_time DATETIME NULL,
			pay_type VARCHAR(32) NOT NULL DEFAULT '',
			create_time DATETIME NULL,
			status TINYINT NOT NULL DEFAULT 0,
			refund_status TINYINT NOT NULL DEFAULT 0,
			delivery_name VARCHAR(64) NULL,
			delivery_sn VARCHAR(100) NULL,
			delivery_type VARCHAR(32) NULL,
			is_del TINYINT NOT NULL DEFAULT 0,
			PRIMARY KEY (id),
			UNIQUE KEY uk_order_id (order_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_order_cart_info (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			oid BIGINT UNSIGNED NOT NULL,
			product_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
			cart_info TEXT NOT NULL,
			` + "`unique`" + ` VARCHAR(50) NOT NULL DEFAULT '',
			PRIMARY KEY (id),
			KEY idx_oid (oid)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_order_status (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			oid BIGINT UNSIGNED NOT NULL,
			change_type VARCHAR(32) NOT NULL,
			change_message VARCHAR(256) NOT NULL DEFAULT '',
			change_time DATETIME NOT NULL,
			PRIMARY KEY (id),
			KEY idx_oid (oid)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_coupon (
			id INT UNSIGNED NOT NULL AUTO_INCREMENT,
			title VARCHAR(64) NOT NULL,
			coupon_price DECIMAL(8,2) NOT NULL DEFAULT 0,
			use_min_price DECIMAL(8,2) NOT NULL DEFAULT 0,
			coupon_time INT UNSIGNED NOT NULL DEFAULT 0,
			status TINYINT NOT NULL DEFAULT 1,
			product_id VARCHAR(200) NULL,
			type TINYINT NULL DEFAULT 0,
			create_time DATETIME NULL,
			is_del TINYINT NOT NULL DEFAULT 0,
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_coupon_issue (
			id INT UNSIGNED NOT NULL AUTO_INCREMENT,
			cname VARCHAR(50) NULL,
			cid INT NULL,
			ctype TINYINT NULL,
			start_time DATETIME NULL,
			end_time DATETIME NULL,
			total_count INT NULL,
			remain_count INT NULL,
			is_permanent TINYINT NOT NULL DEFAULT 0,
			status TINYINT NOT NULL DEFAULT 1,
			is_del TINYINT NOT NULL DEFAULT 0,
			create_time DATETIME NULL,
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_coupon_issue_user (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			uid BIGINT NULL,
			issue_coupon_id INT NULL,
			create_time DATETIME NULL,
			is_del TINYINT NOT NULL DEFAULT 0,
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_coupon_user (
			id BIGINT NOT NULL AUTO_INCREMENT,
			cid INT UNSIGNED NOT NULL DEFAULT 0,
			uid BIGINT UNSIGNED NOT NULL DEFAULT 0,
			coupon_title VARCHAR(32) NOT NULL DEFAULT '',
			coupon_price DECIMAL(8,2) NOT NULL DEFAULT 0,
			use_min_price DECIMAL(8,2) NOT NULL DEFAULT 0,
			create_time DATETIME NOT NULL,
			end_time DATETIME NOT NULL,
			use_time DATETIME NULL,
			type VARCHAR(32) NOT NULL DEFAULT 'send',
			status TINYINT NOT NULL DEFAULT 0,
			is_fail TINYINT NOT NULL DEFAULT 0,
			is_del TINYINT NOT NULL DEFAULT 0,
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_product_reply (
			id BIGINT NOT NULL AUTO_INCREMENT,
			uid BIGINT NOT NULL,
			oid BIGINT NOT NULL,
			` + "`unique`" + ` CHAR(32) NOT NULL DEFAULT '',
			product_id BIGINT NOT NULL,
			reply_type VARCHAR(32) NOT NULL DEFAULT 'product',
			product_score TINYINT NOT NULL,
			comment VARCHAR(512) NOT NULL DEFAULT '',
			pics TEXT NOT NULL,
			create_time DATETIME NOT NULL,
			is_del TINYINT NOT NULL DEFAULT 0,
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE store_product_relation (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			uid BIGINT UNSIGNED NOT NULL,
			product_id BIGINT UNSIGNED NOT NULL,
			type VARCHAR(32) NULL,
			category VARCHAR(32) NULL,
			create_time DATETIME NOT NULL,
			is_del TINYINT NULL DEFAULT 0,
			PRIMARY KEY (id)
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
