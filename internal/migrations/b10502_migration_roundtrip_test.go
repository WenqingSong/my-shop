package migrations

// B1-05-02：Migration Up / Down / Up 往返与 Schema 完整性验证。
//
// 覆盖目标：
//  1. 空白独立库执行 Up，建立全部 63 张业务表 + 追踪表，版本为内嵌最新版本且 dirty=false；
//  2. 表、索引、唯一约束、外键完整（按名称与列顺序断言，不只看表是否存在）；
//  3. 对「有 down 文件」的 20 个版本执行 Down，确认 Schema 真正被移除、更早的 Schema 与业务行保留；
//  4. 再次 Up 完整重建，版本回到最新且 dirty=false，历史业务行仍保留。
//
// 约束说明：20261001000001..20261001000023 为仅 up 的历史基线（仓库内没有对应 down 文件），
// golang-migrate 无法回滚到空库；本测试因此以 Down 20 步（20261001000024 起）作为可验证下界，
// 并在报告中明确记录「回滚到空库不受支持」这一既有设计事实，而不是伪造回滚结果。

import (
	"context"
	"database/sql"
	"testing"
)

const (
	// b10502LastIrreversibleVersion 是最后一批无 down 文件的 baseline 迁移版本。
	b10502LastIrreversibleVersion = uint(20261001000023)
	// b10502ReversibleSteps 是从最新版本回滚到 b10502LastIrreversibleVersion 的步数。
	b10502ReversibleSteps = 20
)

// b10502IndexColumns 返回指定表的索引列顺序（含 PRIMARY/UNIQUE/普通索引）。
func b10502IndexColumns(t *testing.T, db *sql.DB, table, index string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT column_name FROM information_schema.statistics
		 WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?
		 ORDER BY seq_in_index`, table, index)
	if err != nil {
		t.Fatalf("查询索引 %s.%s: %v", table, index, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("扫描索引列 %s.%s: %v", table, index, err)
		}
		cols = append(cols, col)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历索引列 %s.%s: %v", table, index, err)
	}
	return cols
}

// requireIndex 断言索引存在且列顺序一致。
func requireIndex(t *testing.T, db *sql.DB, table, index string, want []string) {
	t.Helper()
	got := b10502IndexColumns(t, db, table, index)
	if len(got) != len(want) {
		t.Fatalf("索引 %s.%s 列期望 %v，实际 %v", table, index, want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("索引 %s.%s 列期望 %v，实际 %v", table, index, want, got)
		}
	}
}

// requireFK 断言外键存在且指向期望的父表。
func requireFK(t *testing.T, db *sql.DB, child, parent string) {
	t.Helper()
	var cnt int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM information_schema.referential_constraints
		 WHERE constraint_schema = DATABASE() AND table_name = ? AND referenced_table_name = ?`,
		child, parent).Scan(&cnt); err != nil {
		t.Fatalf("查询外键 %s→%s: %v", child, parent, err)
	}
	if cnt == 0 {
		t.Fatalf("外键 %s→%s 不存在", child, parent)
	}
}

// b10502TableNames 返回当前库的全部基础表名。
func b10502TableNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY table_name`)
	if err != nil {
		t.Fatalf("查询表清单: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("扫描表名: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历表名: %v", err)
	}
	return names
}

// b10502KeyUniqueConstraints 是本项目关键业务唯一约束（名称 + 列顺序）。
var b10502KeyUniqueConstraints = []struct {
	table string
	index string
	cols  []string
}{
	{"orders", "uk_order_no", []string{"order_no"}},
	{"orders", "uk_user_idempotency", []string{"user_id", "idempotency_key"}},
	{"skus", "uk_sku_code", []string{"sku_code"}},
	{"skus", "uk_product_signature", []string{"product_id", "spec_signature"}},
	{"inventories", "uk_sku_id", []string{"sku_id"}},
	{"wallet_accounts", "uk_user_id", []string{"user_id"}},
	{"wallet_transactions", "uk_idempotency", []string{"idempotency_key"}},
	{"reviews", "uk_order_item", []string{"order_item_id"}},
	{"legacy_id_map", "uk_source_entity_old", []string{"source", "entity_type", "old_id"}},
	{"user_coupons", "uk_user_template_claim", []string{"user_id", "template_id", "claim_no"}},
	{"cart_items", "uk_user_sku", []string{"user_id", "sku_id"}},
	{"freight_template_rules", "uk_template_region", []string{"template_id", "region_id"}},
	{"distribution_commissions", "uk_order_id", []string{"order_id"}},
}

// b10502KeyIndexes 是本项目关键查询索引（名称 + 列顺序）。
var b10502KeyIndexes = []struct {
	table string
	index string
	cols  []string
}{
	{"orders", "idx_status_expire", []string{"status", "expire_at"}},
	{"orders", "idx_user_id", []string{"user_id"}},
	{"products", "idx_freight_template_id", []string{"freight_template_id"}},
	{"addresses", "idx_region_id", []string{"region_id"}},
	{"flash_sale_order_requests", "idx_outbox_claim", []string{"status", "published_at", "next_publish_at", "id"}},
	{"wallet_transactions", "idx_account_id", []string{"account_id", "id"}},
}

// b10502KeyForeignKeys 是本项目关键外键（子表 → 父表）。
var b10502KeyForeignKeys = []struct{ child, parent string }{
	{"addresses", "users"},
	{"products", "categories"},
	{"skus", "products"},
	{"inventories", "skus"},
	{"order_items", "orders"},
	{"wallet_transactions", "wallet_accounts"},
	{"distribution_commissions", "orders"},
	{"sku_spec_values", "skus"},
	{"coupon_batches", "coupon_templates"},
	{"freight_template_rules", "freight_templates"},
	{"group_buy_teams", "group_buy_activities"},
	{"bargain_instances", "bargain_activities"},
	{"user_memberships", "member_levels"},
}

// assertB10502FullSchema 断言完整 Schema（表集合 + 关键唯一约束/索引/外键 + 版本与 dirty）。
func assertB10502FullSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	if got, want := currentVersion(t, db), LatestVersion(); got != want {
		t.Fatalf("迁移版本期望 %d，实际 %d", want, got)
	}
	if dirtyState(t, db) {
		t.Fatal("迁移完成后 dirty 必须为 false")
	}

	names := b10502TableNames(t, db)
	wantSet := make(map[string]bool, len(allTables))
	for _, table := range allTables {
		wantSet[table] = true
		if !tableExists(t, db, table) {
			t.Fatalf("缺失表 %s", table)
		}
	}
	// 只允许合成迁移测试探针表额外存在；其它多余表说明 Schema 未被正确管理。
	allowedExtra := map[string]bool{"migration_probe": true, "migration_synthetic_base": true}
	for _, name := range names {
		if !wantSet[name] && !allowedExtra[name] {
			t.Fatalf("出现未登记的表 %s", name)
		}
	}
	if len(wantSet) != len(allTables) {
		t.Fatalf("期望表集合构建异常：%d != %d", len(wantSet), len(allTables))
	}

	for _, u := range b10502KeyUniqueConstraints {
		requireIndex(t, db, u.table, u.index, u.cols)
	}
	for _, idx := range b10502KeyIndexes {
		requireIndex(t, db, idx.table, idx.index, idx.cols)
	}
	for _, fk := range b10502KeyForeignKeys {
		requireFK(t, db, fk.child, fk.parent)
	}
	requireReferralGraphGuardSingleton(t, db)
}

// b10502SeedBusinessRows 写入迁移前后必须保留的业务行，返回 (productID, skuID, orderID)。
func b10502SeedBusinessRows(t *testing.T, db *sql.DB) (int64, int64, int64) {
	t.Helper()
	ctx := context.Background()

	res, err := db.ExecContext(ctx,
		"INSERT INTO categories (parent_id, name, sort, status) VALUES (0, 'B10502往返验证分类', 0, 1)")
	if err != nil {
		t.Fatalf("insert category: %v", err)
	}
	categoryID, _ := res.LastInsertId()

	res, err = db.ExecContext(ctx,
		"INSERT INTO products (name, category_id, price, status) VALUES ('B10502往返验证商品', ?, 2000, 1)",
		categoryID)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	productID, _ := res.LastInsertId()

	res, err = db.ExecContext(ctx,
		`INSERT INTO skus (product_id, name, price, original_price, status, sku_code)
		 VALUES (?, '默认', 2000, 2500, 1, 'SKU-B10502-ROUNDTRIP')`, productID)
	if err != nil {
		t.Fatalf("insert sku: %v", err)
	}
	skuID, _ := res.LastInsertId()

	if _, err := db.ExecContext(ctx,
		"INSERT INTO inventories (sku_id, quantity) VALUES (?, 7)", skuID); err != nil {
		t.Fatalf("insert inventory: %v", err)
	}

	res, err = db.ExecContext(ctx,
		`INSERT INTO orders (order_no, user_id, status, total_amount, original_amount, discount_amount,
		 idempotency_key, request_hash, recipient_name, phone, province, city, district, detail, expire_at)
		 VALUES ('B10502-ROUNDTRIP-1', 1, 10, 2000, 2000, 0, 'B10502-ROUNDTRIP-1', 'h',
		         '张三', '13800000000', '广东省', '深圳市', '南山区', '科技园1号', NOW())`)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	orderID, _ := res.LastInsertId()
	return productID, skuID, orderID
}

// TestB10502UpFromEmptySchemaInventory 覆盖「空白独立库 → Up → 全量 Schema」。
func TestB10502UpFromEmptySchemaInventory(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("空库 Up 失败: %v", err)
	}
	assertB10502FullSchema(t, db)

	// 表数量：63 张业务表 + schema_migrations。
	if got := len(b10502TableNames(t, db)); got != 64 {
		t.Fatalf("基础表数量期望 64（63 业务表 + schema_migrations），实际 %d", got)
	}

	// 再次 Up 必须幂等（迁移文件无 IF NOT EXISTS，重复执行会因表已存在报错）。
	if err := Up(ctx); err != nil {
		t.Fatalf("第二次 Up 必须幂等，实际失败: %v", err)
	}
	if got, want := currentVersion(t, db), LatestVersion(); got != want {
		t.Fatalf("幂等 Up 后版本期望 %d，实际 %d", want, got)
	}
	if dirtyState(t, db) {
		t.Fatal("幂等 Up 后 dirty 必须为 false")
	}
}

// TestB10502DownUpRoundTrip 覆盖「Up → Down 20 步 → 校验 → Up → 校验」。
func TestB10502DownUpRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("Up 失败: %v", err)
	}
	productID, skuID, orderID := b10502SeedBusinessRows(t, db)

	m, err := newMigrate(ctx)
	if err != nil {
		t.Fatalf("初始化 migrate: %v", err)
	}
	if err := m.Steps(-b10502ReversibleSteps); err != nil {
		sourceErr, dbErr := m.Close()
		t.Fatalf("Down %d 步失败: %v（source=%v db=%v）", b10502ReversibleSteps, err, sourceErr, dbErr)
	}
	if sourceErr, dbErr := m.Close(); sourceErr != nil || dbErr != nil {
		t.Fatalf("关闭 migrate 失败: source=%v db=%v", sourceErr, dbErr)
	}

	if got, want := currentVersion(t, db), b10502LastIrreversibleVersion; got != want {
		t.Fatalf("Down 后版本期望 %d，实际 %d", want, got)
	}
	if dirtyState(t, db) {
		t.Fatal("Down 后 dirty 必须为 false")
	}

	// 本批次新增的表必须真正消失。
	for _, table := range []string{
		"wallet_accounts", "wallet_transactions", "recharge_orders",
		"user_points_accounts", "user_points_logs", "order_status_logs",
		"regions", "product_specs", "product_spec_values", "sku_spec_values",
		"group_buy_activities", "group_buy_teams", "group_buy_participants",
		"bargain_activities", "bargain_instances", "bargain_help_records",
		"member_levels", "user_memberships", "member_growth_logs",
		"distribution_commissions", "user_referrals",
		"freight_templates", "freight_template_rules", "referral_graph_guard",
	} {
		requireTable(t, db, table, false)
	}

	// 本批次新增的列必须真正消失。
	for _, col := range []struct{ table, column string }{
		{"skus", "sku_code"}, {"skus", "original_price"}, {"skus", "spec_signature"},
		{"order_items", "sku_code"}, {"order_items", "original_price"},
		{"orders", "member_discount_amount"}, {"orders", "pay_type"}, {"orders", "shipping_fee"},
		{"products", "freight_template_id"}, {"users", "birthday"}, {"addresses", "region_id"},
		{"flash_sale_order_requests", "published_at"}, {"user_coupons", "claim_no"},
	} {
		requireColumn(t, db, col.table, col.column, false)
	}

	// 更早的 Schema 与业务行必须保留（Down 只回滚 DDL，不删业务数据）。
	for _, col := range []struct{ table, column string }{
		{"orders", "total_amount"}, {"orders", "status"}, {"skus", "price"}, {"users", "username"},
	} {
		requireColumn(t, db, col.table, col.column, true)
	}
	var cnt int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM products WHERE id = ?", productID).Scan(&cnt); err != nil || cnt != 1 {
		t.Fatalf("Down 后商品应保留，count=%d err=%v", cnt, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM orders WHERE id = ?", orderID).Scan(&cnt); err != nil || cnt != 1 {
		t.Fatalf("Down 后订单应保留，count=%d err=%v", cnt, err)
	}
	var stock int64
	if err := db.QueryRowContext(ctx, "SELECT quantity FROM inventories WHERE sku_id = ?", skuID).Scan(&stock); err != nil || stock != 7 {
		t.Fatalf("Down 后库存应保留 7，实际 %d err=%v", stock, err)
	}

	// 再次 Up：完整重建，且不重算历史订单金额。
	if err := Up(ctx); err != nil {
		t.Fatalf("二次 Up 失败: %v", err)
	}
	assertB10502FullSchema(t, db)

	var total, memberDiscount int64
	if err := db.QueryRowContext(ctx,
		"SELECT total_amount, member_discount_amount FROM orders WHERE id = ?", orderID).
		Scan(&total, &memberDiscount); err != nil {
		t.Fatalf("读取往返后订单: %v", err)
	}
	if total != 2000 || memberDiscount != 0 {
		t.Fatalf("历史订单金额不应被重算：total=%d member_discount=%d", total, memberDiscount)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM products WHERE id = ?", productID).Scan(&cnt); err != nil || cnt != 1 {
		t.Fatalf("二次 Up 后商品应保留，count=%d err=%v", cnt, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT quantity FROM inventories WHERE sku_id = ?", skuID).Scan(&stock); err != nil || stock != 7 {
		t.Fatalf("二次 Up 后库存应保留 7，实际 %d err=%v", stock, err)
	}

	// 关键唯一约束在重建后仍然存在：同订单号重复插入必须被拒绝。
	if _, err := db.ExecContext(ctx,
		`INSERT INTO orders (order_no, user_id, status, total_amount, idempotency_key, request_hash,
		 recipient_name, phone, province, city, district, detail, expire_at)
		 VALUES ('B10502-ROUNDTRIP-1', 1, 10, 2000, 'B10502-ROUNDTRIP-2', 'h',
		         '张三', '13800000000', '广东省', '深圳市', '南山区', '科技园1号', NOW())`); err == nil {
		t.Fatal("重复 order_no 必须被唯一约束拒绝")
	}
}
