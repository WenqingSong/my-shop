package migrations

// B1-04 八分支集成新增迁移的 up → down → up 验证：
//   - 20261111000002_freight：freight_templates + freight_template_rules + products.freight_template_id；
//   - 20261111000003_order_member_discount：orders.member_discount_amount。
//
// 断言目标：回滚只移除本批次新增的表/列，不影响已应用的 20261111000001（sku_code_pricing）；
// 再次 up 后 schema 完整恢复。数据库为真实 MySQL，不使用 Mock。

import (
	"context"
	"database/sql"
	"testing"
)

const (
	b104OrderMemberDiscountMigration   = uint(20261111000003)
	b104SkuCodePricingMigrationVersion = uint(20261111000001)
)

// columnExists 判断指定表的列是否存在。
func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var cnt int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM information_schema.columns
		 WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`,
		table, column).Scan(&cnt)
	if err != nil {
		t.Fatalf("查询列 %s.%s: %v", table, column, err)
	}
	return cnt > 0
}

func requireTable(t *testing.T, db *sql.DB, table string, want bool) {
	t.Helper()
	if got := tableExists(t, db, table); got != want {
		t.Fatalf("表 %s 存在性期望 %t，实际 %t", table, want, got)
	}
}

func requireColumn(t *testing.T, db *sql.DB, table, column string, want bool) {
	t.Helper()
	if got := columnExists(t, db, table, column); got != want {
		t.Fatalf("列 %s.%s 存在性期望 %t，实际 %t", table, column, want, got)
	}
}

// TestB104IntegrationMigrationsUpDownUp 覆盖新增迁移的可回滚性与互不影响。
func TestB104IntegrationMigrationsUpDownUp(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if v := currentVersion(t, db); v != b104OrderMemberDiscountMigration {
		t.Fatalf("最新版本期望 %d，实际 %d", b104OrderMemberDiscountMigration, v)
	}
	requireTable(t, db, "freight_templates", true)
	requireTable(t, db, "freight_template_rules", true)
	requireColumn(t, db, "products", "freight_template_id", true)
	requireColumn(t, db, "orders", "member_discount_amount", true)
	requireColumn(t, db, "skus", "sku_code", true)

	// 历史数据兼容：迁移前已存在的商品/订单在回滚与再次升级后必须原样保留，
	// 且 member_discount_amount 对历史订单默认为 0（不回填、不重算金额）。
	if _, err := db.ExecContext(ctx, "INSERT INTO categories (parent_id, name) VALUES (0, '迁移兼容分类')"); err != nil {
		t.Fatalf("insert category: %v", err)
	}
	var categoryID int64
	if err := db.QueryRowContext(ctx, "SELECT id FROM categories WHERE name = '迁移兼容分类'").Scan(&categoryID); err != nil {
		t.Fatalf("query category: %v", err)
	}
	res, err := db.ExecContext(ctx,
		"INSERT INTO products (name, category_id, price, status) VALUES ('迁移兼容商品', ?, 1000, 1)", categoryID)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	productID, _ := res.LastInsertId()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO orders (order_no, user_id, status, total_amount, original_amount, discount_amount,
		 idempotency_key, request_hash, recipient_name, phone, province, city, district, detail, expire_at)
		 VALUES ('MIG-COMPAT-1', 1, 10, 1000, 1000, 0, 'MIG-COMPAT-1', 'h', '张三', '13800000000', '省', '市', '区', '地址', NOW())`); err != nil {
		t.Fatalf("insert order: %v", err)
	}

	// 回滚到 B1-04-02 的版本：运费表与会员折扣列必须消失，SKU 编码列必须保留。
	migrateTo(t, b104SkuCodePricingMigrationVersion)
	if v := currentVersion(t, db); v != b104SkuCodePricingMigrationVersion {
		t.Fatalf("回滚后期望版本 %d，实际 %d", b104SkuCodePricingMigrationVersion, v)
	}
	requireTable(t, db, "freight_templates", false)
	requireTable(t, db, "freight_template_rules", false)
	requireColumn(t, db, "products", "freight_template_id", false)
	requireColumn(t, db, "orders", "member_discount_amount", false)
	requireColumn(t, db, "skus", "sku_code", true)

	// 数据仍在：回滚只回滚 DDL，不删除既有业务行。
	var cnt int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM products WHERE id = ?", productID).Scan(&cnt); err != nil || cnt != 1 {
		t.Fatalf("回滚后商品应保留，count=%d err=%v", cnt, err)
	}

	// 再次 up：完整恢复，且 dirty=false。
	if err := Up(ctx); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	if v := currentVersion(t, db); v != b104OrderMemberDiscountMigration {
		t.Fatalf("重新升级后期望版本 %d，实际 %d", b104OrderMemberDiscountMigration, v)
	}
	if dirtyState(t, db) {
		t.Fatal("重新升级后 dirty 应为 false")
	}
	requireTable(t, db, "freight_templates", true)
	requireTable(t, db, "freight_template_rules", true)
	requireColumn(t, db, "products", "freight_template_id", true)
	requireColumn(t, db, "orders", "member_discount_amount", true)

	// 历史订单的会员折扣默认 0，金额不被重算。
	var (
		total, memberDiscount int64
	)
	if err := db.QueryRowContext(ctx,
		"SELECT total_amount, member_discount_amount FROM orders WHERE order_no = 'MIG-COMPAT-1'").
		Scan(&total, &memberDiscount); err != nil {
		t.Fatalf("读取历史订单: %v", err)
	}
	if total != 1000 || memberDiscount != 0 {
		t.Fatalf("历史订单金额不应被重算：total=%d member_discount=%d", total, memberDiscount)
	}
}
