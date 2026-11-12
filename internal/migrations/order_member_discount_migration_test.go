package migrations

// B1-04-08 会员折扣金额快照列迁移测试：
//   - orders 新增 member_discount_amount（INT UNSIGNED NOT NULL DEFAULT 0），位于 discount_amount 之后；
//   - 历史订单不回填、不重算：迁移前已存在的订单该列为 0，total_amount 不变；
//   - down 只删除本列，既有订单数据（含 total_amount/original_amount/discount_amount）不变；
//   - 重新 up 后结构恢复，历史订单仍为默认 0。
//
// 全部使用真实 MySQL（与其它迁移测试同一环境假设），不使用 Mock 冒充数据库约束验证。

import (
	"context"
	"testing"
)

const (
	// memberDiscountColumn 是本迁移新增的唯一列。
	memberDiscountColumn = "member_discount_amount"
	// memberDiscountPrevVersion 是本迁移之前的版本（down 的回滚落点）。
	memberDiscountPrevVersion = uint(20261110000014)
)

// TestOrderMemberDiscountMigrationUpDownUp 覆盖 up → down → up 与历史数据不变性。
func TestOrderMemberDiscountMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Fatalf("version=%d want %d", v, latestMigrationVersion)
	}

	// 结构断言：类型/可空/默认值/位置。
	spec := readActualTable(t, db, "orders")
	col, ok := findColumn(spec, memberDiscountColumn)
	if !ok {
		t.Fatalf("up 后 orders 应存在列 %s", memberDiscountColumn)
	}
	if col.Type != "int unsigned" {
		t.Fatalf("member_discount_amount 类型=%s want int unsigned", col.Type)
	}
	if col.Nullable {
		t.Fatalf("member_discount_amount 不允许为 NULL")
	}
	if col.Default == nil || *col.Default != "0" {
		t.Fatalf("member_discount_amount 默认值=%v want 0", col.Default)
	}
	if got := adjacentColumn(spec, "discount_amount"); got != memberDiscountColumn {
		t.Fatalf("member_discount_amount 应紧邻 discount_amount 之后，实际后一列为 %q", got)
	}

	// 历史订单（按不含新列的列集合插入）：默认 0，且金额不被重算。
	orderID := insertHistoricalOrder(t, db, "B10408-HISTORY-1", 900, 1000, 100)
	var total, original, discount, memberDiscount int64
	if err := db.QueryRowContext(ctx, `SELECT total_amount, original_amount, discount_amount, member_discount_amount
		FROM orders WHERE id = ?`, orderID).
		Scan(&total, &original, &discount, &memberDiscount); err != nil {
		t.Fatalf("读取新列默认值: %v", err)
	}
	if total != 900 || original != 1000 || discount != 100 || memberDiscount != 0 {
		t.Fatalf("历史订单金额被改变: total=%d original=%d discount=%d member=%d",
			total, original, discount, memberDiscount)
	}

	// 会员折扣金额可以写入非 0 值（新订单路径），且不影响既有列语义。
	if _, err := db.ExecContext(ctx, `UPDATE orders SET member_discount_amount = 50 WHERE id = ?`, orderID); err != nil {
		t.Fatalf("写入 member_discount_amount: %v", err)
	}

	// down：只删除本列。
	migrateTo(t, memberDiscountPrevVersion)
	if v := currentVersion(t, db); v != memberDiscountPrevVersion {
		t.Fatalf("down 后 version=%d want %d", v, memberDiscountPrevVersion)
	}
	if col, ok := findColumn(readActualTable(t, db, "orders"), memberDiscountColumn); ok {
		t.Fatalf("down 后不应存在列 %s（实际类型 %s）", memberDiscountColumn, col.Type)
	}
	if err := db.QueryRowContext(ctx, `SELECT total_amount, original_amount, discount_amount FROM orders WHERE id = ?`, orderID).
		Scan(&total, &original, &discount); err != nil {
		t.Fatalf("down 后读取订单: %v", err)
	}
	if total != 900 || original != 1000 || discount != 100 {
		t.Fatalf("down 后既有订单金额被改变: total=%d original=%d discount=%d", total, original, discount)
	}

	// 重新 up：结构恢复，历史订单默认 0（不伪造历史折扣）。
	if err := Up(ctx); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Fatalf("重新 up 后 version=%d want %d", v, latestMigrationVersion)
	}
	if col, ok := findColumn(readActualTable(t, db, "orders"), memberDiscountColumn); !ok || col.Type != "int unsigned" {
		t.Fatalf("重新 up 后应存在列 %s（int unsigned）", memberDiscountColumn)
	}
	if err := db.QueryRowContext(ctx, `SELECT total_amount, member_discount_amount FROM orders WHERE id = ?`, orderID).
		Scan(&total, &memberDiscount); err != nil {
		t.Fatalf("重新 up 后读取订单: %v", err)
	}
	if total != 900 || memberDiscount != 0 {
		t.Fatalf("重新 up 后历史订单 total=%d member_discount=%d，期望 900/0", total, memberDiscount)
	}
}

// TestOrderMemberDiscountRejectsNegative 证明列类型为 UNSIGNED：负数写入被 MySQL 拒绝（严格模式）。
func TestOrderMemberDiscountRejectsNegative(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	orderID := insertHistoricalOrder(t, db, "B10408-HISTORY-2", 1000, 1000, 0)
	if _, err := db.ExecContext(ctx, `UPDATE orders SET member_discount_amount = -1 WHERE id = ?`, orderID); err == nil {
		t.Fatalf("member_discount_amount 为 UNSIGNED，负数写入应失败")
	}
}

// findColumn 在 tableSpec 中定位列。
func findColumn(spec tableSpec, name string) (columnSpec, bool) {
	for _, c := range spec.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return columnSpec{}, false
}

// indexOfColumn 返回列在表定义中的序号（不存在返回 -1）。
func indexOfColumn(spec tableSpec, name string) int {
	for i, c := range spec.Columns {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// adjacentColumn 返回指定列的下一列名（不存在返回空串）。
func adjacentColumn(spec tableSpec, name string) string {
	idx := indexOfColumn(spec, name)
	if idx < 0 || idx+1 >= len(spec.Columns) {
		return ""
	}
	return spec.Columns[idx+1].Name
}
