package migrations

// B1-03-02 交易与资金基础模型迁移测试：
//   - 6 个新增迁移可 up → down → up，回滚只影响新增表/列；
//   - 历史订单金额不被重算、shipping_fee 默认为 0；
//   - 订单状态流水支持一单多条并按订单关联查询；
//   - 钱包账户用户唯一、流水幂等键唯一、账户不存在时写入失败、无符号余额不可为负；
//   - 充值订单状态/单号语义与「充值单 → 钱包流水」关联；
//   - 积分账户用户唯一、积分流水幂等键唯一、负向变动可记录但不允许负余额；
//   - 优惠券历史行（无快照）兼容，新快照不随模板修改而漂移。
//
// 需真实 MySQL（与其它迁移测试同一环境假设），不使用 Mock 冒充迁移验证。

import (
	"context"
	"database/sql"
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

// 本批次新增的迁移版本。
const (
	orderShippingMigrationVersion  = uint(20261110000004)
	orderStatusLogMigrationVersion = uint(20261110000005)
	walletMigrationVersion         = uint(20261110000006)
	rechargeMigrationVersion       = uint(20261110000007)
	pointsMigrationVersion         = uint(20261110000008)
	couponSnapshotMigrationVersion = uint(20261110000009)
)

// tradeFundTables 是本批次新增的表（down 后必须全部消失）。
var tradeFundTables = []string{
	"order_status_logs", "wallet_accounts", "wallet_transactions",
	"recharge_orders", "user_points_accounts", "user_points_logs",
}

// 复用 product_model_migration_test.go 的 migrateTo / requireMySQLError / 公共插入辅助。

// insertHistoricalOrder 按「B1-03-01 之前」的列集合插入历史订单，返回 id。
// 不引用 shipping_fee/pay_type/delivery_*，用于证明迁移不重算历史金额。
func insertHistoricalOrder(t *testing.T, db *sql.DB, orderNo string, total, original, discount int64) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `INSERT INTO orders
		(order_no, user_id, status, total_amount, original_amount, discount_amount,
		 idempotency_key, request_hash, recipient_name, phone, province, city, district, detail, expire_at)
		VALUES (?, 1, 10, ?, ?, ?, ?, ?, '张三', '13800000000', '省', '市', '区', '地址', NOW())`,
		orderNo, total, original, discount, orderNo, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("insert historical order %s: %v", orderNo, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("order last insert id: %v", err)
	}
	return id
}

// insertHistoricalOrderItem 按旧列集合插入订单项。
func insertHistoricalOrderItem(t *testing.T, db *sql.DB, orderID, productID int64) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `INSERT INTO order_items
		(order_id, sku_id, product_id, sku_name, product_name, price, quantity)
		VALUES (?, 0, ?, '默认', '历史商品', 900, 1)`, orderID, productID)
	if err != nil {
		t.Fatalf("insert historical order item: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("order item last insert id: %v", err)
	}
	return id
}

// insertCouponTemplate 写入一个可用券模板，返回 id（列集合在迁移前后一致）。
func insertCouponTemplate(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `INSERT INTO coupon_templates
		(name, threshold_amount, discount_amount, total_quantity,
		 claim_start_time, claim_end_time, use_start_time, use_end_time)
		VALUES (?, 1000, 100, 100, DATE_SUB(NOW(), INTERVAL 1 DAY), DATE_ADD(NOW(), INTERVAL 30 DAY),
		        DATE_SUB(NOW(), INTERVAL 1 DAY), DATE_ADD(NOW(), INTERVAL 30 DAY))`, name)
	if err != nil {
		t.Fatalf("insert coupon template: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("template last insert id: %v", err)
	}
	return id
}

// TestTradeFundMigrationUpDownUp 覆盖 6 个迁移的 up → down → up：
// 回滚后新增表/列消失，历史订单与券行保留；重新 up 后默认值正确、既有金额不变。
func TestTradeFundMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Fatalf("version=%d want %d", v, latestMigrationVersion)
	}

	// 回滚本批次 6 个版本，回到 B1-03-01 的 schema（固定目标版本，避免后续批次改变落点）。
	migrateTo(t, 20261110000003)
	if v := currentVersion(t, db); v != 20261110000003 {
		t.Fatalf("down 后 version=%d want 20261110000003", v)
	}
	for _, table := range tradeFundTables {
		if tableExists(t, db, table) {
			t.Fatalf("down 后不应存在表 %s", table)
		}
	}
	orderSpec := readActualTable(t, db, "orders")
	for _, col := range []string{"shipping_fee", "pay_type", "transaction_no", "delivery_type", "delivery_name", "delivery_sn"} {
		if hasColumn(orderSpec, col) {
			t.Fatalf("down 后 orders 不应存在列 %s", col)
		}
	}
	if hasColumn(readActualTable(t, db, "order_items"), "spec_text") {
		t.Fatalf("down 后 order_items 不应存在 spec_text")
	}
	for _, col := range []string{"coupon_title", "coupon_price", "use_min_price", "end_time"} {
		if hasColumn(readActualTable(t, db, "user_coupons"), col) {
			t.Fatalf("down 后 user_coupons 不应存在列 %s", col)
		}
	}

	// 在旧 schema 写入「历史数据」：金额三列不相等，用于证明迁移不重算 total_amount。
	categoryID := insertCategory(t, db, "历史分类")
	productID := insertProduct(t, db, categoryID, "历史商品")
	orderID := insertHistoricalOrder(t, db, "B10302-HISTORY-1", 900, 1000, 100)
	itemID := insertHistoricalOrderItem(t, db, orderID, productID)
	templateID := insertCouponTemplate(t, db, "历史券模板")
	res, err := db.ExecContext(ctx, `INSERT INTO user_coupons (user_id, template_id, status, received_at)
		VALUES (1, ?, 10, NOW())`, templateID)
	if err != nil {
		t.Fatalf("insert historical coupon: %v", err)
	}
	couponID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("coupon last insert id: %v", err)
	}

	// 重新升级：新增列取默认值，历史金额逐行不变。
	if err := Up(ctx); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Fatalf("重新 up 后 version=%d want %d", v, latestMigrationVersion)
	}
	for _, table := range tradeFundTables {
		if !tableExists(t, db, table) {
			t.Fatalf("重新 up 后应存在表 %s", table)
		}
	}

	var total, original, discount, shipping int64
	var payType, transactionNo, deliveryType, deliveryName, deliverySN string
	if err := db.QueryRowContext(ctx, `SELECT total_amount, original_amount, discount_amount, shipping_fee,
		pay_type, transaction_no, delivery_type, delivery_name, delivery_sn FROM orders WHERE id = ?`, orderID).
		Scan(&total, &original, &discount, &shipping, &payType, &transactionNo, &deliveryType, &deliveryName, &deliverySN); err != nil {
		t.Fatalf("读取历史订单: %v", err)
	}
	if total != 900 || original != 1000 || discount != 100 {
		t.Fatalf("历史订单金额被改变: total=%d original=%d discount=%d", total, original, discount)
	}
	if shipping != 0 {
		t.Fatalf("历史订单 shipping_fee=%d want 0", shipping)
	}
	if payType != "" || transactionNo != "" || deliveryType != "" || deliveryName != "" || deliverySN != "" {
		t.Fatalf("新增履约/支付列默认值应为空串: %q %q %q %q %q",
			payType, transactionNo, deliveryType, deliveryName, deliverySN)
	}

	var specText string
	if err := db.QueryRowContext(ctx, "SELECT spec_text FROM order_items WHERE id = ?", itemID).Scan(&specText); err != nil {
		t.Fatalf("读取历史订单项: %v", err)
	}
	if specText != "" {
		t.Fatalf("历史订单项 spec_text=%q want ''", specText)
	}

	var title, price, minPrice, endTime sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT coupon_title, coupon_price, use_min_price, end_time
		FROM user_coupons WHERE id = ?`, couponID).Scan(&title, &price, &minPrice, &endTime); err != nil {
		t.Fatalf("读取历史用户券: %v", err)
	}
	if title.Valid || price.Valid || minPrice.Valid || endTime.Valid {
		t.Fatalf("历史券快照列应全部为 NULL（不得伪造）: %v %v %v %v", title, price, minPrice, endTime)
	}
}

// TestOrderStatusLogsSupportMultipleRows 覆盖订单状态流水：一单多条、按订单隔离、状态值与订单状态机一致。
func TestOrderStatusLogsSupportMultipleRows(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	categoryID := insertCategory(t, db, "流水分类")
	insertProduct(t, db, categoryID, "流水商品")
	orderID := insertHistoricalOrder(t, db, "B10302-STATUS-1", 1000, 1000, 0)
	otherOrderID := insertHistoricalOrder(t, db, "B10302-STATUS-2", 1000, 1000, 0)

	rows := []struct {
		from, to int
		change   string
		opType   int
		opID     int64
		remark   string
	}{
		{0, 10, "create", 1, 1, "用户下单"},
		{10, 20, "pay", 1, 1, "Mock 支付"},
		{20, 30, "ship", 2, 99, "后台发货"},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx, `INSERT INTO order_status_logs
			(order_id, from_status, to_status, change_type, operator_type, operator_id, remark)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			orderID, r.from, r.to, r.change, r.opType, r.opID, r.remark); err != nil {
			t.Fatalf("insert order status log: %v", err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO order_status_logs
		(order_id, from_status, to_status, change_type, operator_type)
		VALUES (?, 0, 10, 'create', 1)`, otherOrderID); err != nil {
		t.Fatalf("insert other order status log: %v", err)
	}

	queryRows, err := db.QueryContext(ctx, `SELECT from_status, to_status, change_type
		FROM order_status_logs WHERE order_id = ? ORDER BY created_at, id`, orderID)
	if err != nil {
		t.Fatalf("query order status logs: %v", err)
	}
	defer queryRows.Close()
	var got []string
	for queryRows.Next() {
		var from, to int
		var change string
		if err := queryRows.Scan(&from, &to, &change); err != nil {
			t.Fatalf("scan order status log: %v", err)
		}
		got = append(got, change)
		if to < 10 || to > 70 {
			t.Fatalf("状态值 %d 超出 orders 状态定义 10..70", to)
		}
	}
	if err := queryRows.Err(); err != nil {
		t.Fatalf("iterate order status logs: %v", err)
	}
	if len(got) != 3 || got[0] != "create" || got[1] != "pay" || got[2] != "ship" {
		t.Fatalf("订单流水顺序/数量不符: %v", got)
	}

	var otherCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM order_status_logs WHERE order_id = ?", otherOrderID).Scan(&otherCount); err != nil {
		t.Fatalf("count other order logs: %v", err)
	}
	if otherCount != 1 {
		t.Fatalf("其它订单流水数=%d want 1", otherCount)
	}
}

// TestWalletAccountAndTransactionConstraints 覆盖钱包：
// 用户唯一、流水幂等键唯一、账户必须存在、无符号余额不可为负。
func TestWalletAccountAndTransactionConstraints(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	res, err := db.ExecContext(ctx, "INSERT INTO wallet_accounts (user_id) VALUES (1)")
	if err != nil {
		t.Fatalf("insert wallet account: %v", err)
	}
	accountID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("wallet account last insert id: %v", err)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO wallet_accounts (user_id) VALUES (1)")
	requireMySQLError(t, err, 1062, "同一用户重复钱包账户")

	if _, err := db.ExecContext(ctx, `INSERT INTO wallet_transactions
		(account_id, user_id, tx_type, direction, amount, balance_before, balance_after, biz_type, biz_id, idempotency_key)
		VALUES (?, 1, 'recharge', 1, 100, 0, 100, 'recharge', 1, 'recharge:1')`, accountID); err != nil {
		t.Fatalf("insert wallet transaction: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO wallet_transactions
		(account_id, user_id, tx_type, direction, amount, balance_before, balance_after, biz_type, biz_id, idempotency_key)
		VALUES (?, 1, 'recharge', 1, 100, 0, 100, 'recharge', 1, 'recharge:1')`, accountID)
	requireMySQLError(t, err, 1062, "钱包流水幂等键重复")

	_, err = db.ExecContext(ctx, `INSERT INTO wallet_transactions
		(account_id, user_id, tx_type, direction, amount, balance_before, balance_after, biz_type, idempotency_key)
		VALUES (999999, 1, 'recharge', 1, 100, 0, 100, 'recharge', 'recharge:missing-account')`)
	requireMySQLError(t, err, 1452, "钱包账户不存在时写入流水")

	if _, err := db.ExecContext(ctx, "UPDATE wallet_accounts SET balance = 50 WHERE id = ?", accountID); err != nil {
		t.Fatalf("设置余额: %v", err)
	}
	_, err = db.ExecContext(ctx, "UPDATE wallet_accounts SET balance = balance - 100 WHERE id = ?", accountID)
	requireMySQLError(t, err, 1690, "余额扣减为负")

	var balance int64
	if err := db.QueryRowContext(ctx, "SELECT balance FROM wallet_accounts WHERE id = ?", accountID).Scan(&balance); err != nil {
		t.Fatalf("读取余额: %v", err)
	}
	if balance != 50 {
		t.Fatalf("负向扣减被拒后余额应保持 50，实际 %d", balance)
	}
}

// TestRechargeOrderWalletConsistency 覆盖 Mock 充值：
// 单号唯一、状态默认待支付、支付完成时间可空、充值单与钱包流水可通过 user_id + biz 关联且不重复入账。
func TestRechargeOrderWalletConsistency(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	res, err := db.ExecContext(ctx, "INSERT INTO recharge_orders (order_no, user_id, amount) VALUES ('RECHARGE-1', 1, 500)")
	if err != nil {
		t.Fatalf("insert recharge order: %v", err)
	}
	rechargeID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("recharge last insert id: %v", err)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO recharge_orders (order_no, user_id, amount) VALUES ('RECHARGE-1', 1, 500)")
	requireMySQLError(t, err, 1062, "充值单号重复")

	var status int
	var payType string
	var paidAt sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT status, pay_type, paid_at FROM recharge_orders WHERE id = ?", rechargeID).
		Scan(&status, &payType, &paidAt); err != nil {
		t.Fatalf("读取充值单: %v", err)
	}
	if status != 10 || payType != "" || paidAt.Valid {
		t.Fatalf("充值单默认值不符: status=%d pay_type=%q paid_at=%v", status, payType, paidAt)
	}

	if _, err := db.ExecContext(ctx, `UPDATE recharge_orders
		SET status = 20, pay_type = 'mock', paid_at = NOW() WHERE id = ?`, rechargeID); err != nil {
		t.Fatalf("Mock 支付充值单: %v", err)
	}
	accRes, err := db.ExecContext(ctx, "INSERT INTO wallet_accounts (user_id, balance) VALUES (1, 0)")
	if err != nil {
		t.Fatalf("insert wallet account: %v", err)
	}
	accountID, err := accRes.LastInsertId()
	if err != nil {
		t.Fatalf("wallet account last insert id: %v", err)
	}
	idempotencyKey := "recharge:" + strconv.FormatInt(rechargeID, 10)
	if _, err := db.ExecContext(ctx, `INSERT INTO wallet_transactions
		(account_id, user_id, tx_type, direction, amount, balance_before, balance_after, biz_type, biz_id, idempotency_key, remark)
		VALUES (?, 1, 'recharge', 1, 500, 0, 500, 'recharge', ?, ?, 'Mock 充值')`,
		accountID, rechargeID, idempotencyKey); err != nil {
		t.Fatalf("充值入账: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO wallet_transactions
		(account_id, user_id, tx_type, direction, amount, balance_before, balance_after, biz_type, biz_id, idempotency_key)
		VALUES (?, 1, 'recharge', 1, 500, 0, 500, 'recharge', ?, ?)`,
		accountID, rechargeID, idempotencyKey)
	requireMySQLError(t, err, 1062, "同一充值单重复入账")

	var txCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wallet_transactions wt
		JOIN recharge_orders ro ON ro.user_id = wt.user_id AND wt.biz_type = 'recharge' AND wt.biz_id = ro.id
		WHERE ro.id = ?`, rechargeID).Scan(&txCount); err != nil {
		t.Fatalf("关联查询充值入账: %v", err)
	}
	if txCount != 1 {
		t.Fatalf("充值单关联流水数=%d want 1", txCount)
	}
}

// TestPointsAccountAndLogConstraints 覆盖积分：
// 用户唯一、流水幂等键唯一、支持带符号变动、账户余额不可为负。
func TestPointsAccountAndLogConstraints(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	if _, err := db.ExecContext(ctx, "INSERT INTO user_points_accounts (user_id) VALUES (1)"); err != nil {
		t.Fatalf("insert points account: %v", err)
	}
	_, err := db.ExecContext(ctx, "INSERT INTO user_points_accounts (user_id) VALUES (1)")
	requireMySQLError(t, err, 1062, "同一用户重复积分账户")

	if _, err := db.ExecContext(ctx, `INSERT INTO user_points_logs
		(user_id, change_amount, points_before, points_after, change_type, biz_type, biz_id, idempotency_key)
		VALUES (1, 100, 0, 100, 'sign_in', 'sign', 1, 'sign_in:1:2026-11-10')`); err != nil {
		t.Fatalf("insert points log +100: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO user_points_logs
		(user_id, change_amount, points_before, points_after, change_type, biz_type, biz_id, idempotency_key)
		VALUES (1, 100, 0, 100, 'sign_in', 'sign', 1, 'sign_in:1:2026-11-10')`)
	requireMySQLError(t, err, 1062, "积分流水幂等键重复")

	// 负向变动可记录（扣减语义由 change_amount 符号表达）。
	if _, err := db.ExecContext(ctx, `INSERT INTO user_points_logs
		(user_id, change_amount, points_before, points_after, change_type, biz_type, biz_id, idempotency_key)
		VALUES (1, -30, 100, 70, 'order_deduct', 'order', 88, 'order_deduct:88')`); err != nil {
		t.Fatalf("insert points log -30: %v", err)
	}

	if _, err := db.ExecContext(ctx, "UPDATE user_points_accounts SET points = points - 200 WHERE user_id = 1"); err == nil {
		t.Fatalf("积分扣减为负应被 MySQL 拒绝")
	} else {
		requireMySQLError(t, err, 1690, "积分扣减为负")
	}

	var points int64
	if err := db.QueryRowContext(ctx, "SELECT points FROM user_points_accounts WHERE user_id = 1").Scan(&points); err != nil {
		t.Fatalf("读取积分: %v", err)
	}
	if points != 0 {
		t.Fatalf("负向扣减被拒后积分应保持 0，实际 %d", points)
	}
}

// TestCouponSnapshotBackwardCompatibility 覆盖优惠券快照：
// 历史券 NULL 兼容既有模板联查；新快照写入后不随模板修改而漂移；领取唯一约束不变。
func TestCouponSnapshotBackwardCompatibility(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	templateID := insertCouponTemplate(t, db, "原始券名")
	if _, err := db.ExecContext(ctx, `INSERT INTO user_coupons (user_id, template_id, status, received_at)
		VALUES (1, ?, 10, NOW())`, templateID); err != nil {
		t.Fatalf("insert historical coupon: %v", err)
	}

	// 历史券：快照为 NULL，既有「券 + 模板」联查仍可执行（读取方回退模板值）。
	var title, threshold, discount, useEnd sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT c.coupon_title, t.threshold_amount, t.discount_amount, t.use_end_time
		FROM user_coupons c LEFT JOIN coupon_templates t ON t.id = c.template_id
		WHERE c.user_id = 1 AND c.template_id = ?`, templateID).Scan(&title, &threshold, &discount, &useEnd); err != nil {
		t.Fatalf("历史券模板联查: %v", err)
	}
	if title.Valid {
		t.Fatalf("历史券 coupon_title 应为 NULL，实际 %q", title.String)
	}
	if !threshold.Valid || threshold.String != "1000" {
		t.Fatalf("历史券模板块门槛读取异常: %v", threshold)
	}

	// 新券：写入快照；修改模板后快照不变、模板变化可见。
	if _, err := db.ExecContext(ctx, `INSERT INTO user_coupons
		(user_id, template_id, coupon_title, coupon_price, use_min_price, end_time, status, received_at)
		VALUES (2, ?, '快照券名', 100, 1000, DATE_ADD(NOW(), INTERVAL 7 DAY), 10, NOW())`, templateID); err != nil {
		t.Fatalf("insert coupon with snapshot: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE coupon_templates
		SET name = '改名后的券', discount_amount = 300, threshold_amount = 5000 WHERE id = ?`, templateID); err != nil {
		t.Fatalf("修改券模板: %v", err)
	}
	var snapshotTitle string
	var snapshotPrice, snapshotMin int64
	if err := db.QueryRowContext(ctx, `SELECT coupon_title, coupon_price, use_min_price
		FROM user_coupons WHERE user_id = 2 AND template_id = ?`, templateID).
		Scan(&snapshotTitle, &snapshotPrice, &snapshotMin); err != nil {
		t.Fatalf("读取券快照: %v", err)
	}
	if snapshotTitle != "快照券名" || snapshotPrice != 100 || snapshotMin != 1000 {
		t.Fatalf("券快照被模板修改影响: title=%q price=%d min=%d", snapshotTitle, snapshotPrice, snapshotMin)
	}
	var templateName string
	var templateDiscount int64
	if err := db.QueryRowContext(ctx, "SELECT name, discount_amount FROM coupon_templates WHERE id = ?", templateID).
		Scan(&templateName, &templateDiscount); err != nil {
		t.Fatalf("读取修改后的模板: %v", err)
	}
	if templateName != "改名后的券" || templateDiscount != 300 {
		t.Fatalf("模板修改未生效: name=%q discount=%d", templateName, templateDiscount)
	}

	// 领取唯一约束不变：同一用户同一模板不能出现第二张。
	_, err := db.ExecContext(ctx, `INSERT INTO user_coupons (user_id, template_id, status, received_at)
		VALUES (2, ?, 10, NOW())`, templateID)
	requireMySQLError(t, err, 1062, "同一用户重复领取同一模板")
}

// TestTradeFundMigrationFilesArePaired 覆盖本批次迁移文件完整性：6 个版本均成对存在且严格递增。
func TestTradeFundMigrationFilesArePaired(t *testing.T) {
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
		name := entry.Name()
		v, err := parseVersion(name)
		if err != nil {
			t.Fatalf("解析迁移版本 %q: %v", name, err)
		}
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			ups[v] = true
		case strings.HasSuffix(name, ".down.sql"):
			downs[v] = true
		}
	}
	for _, v := range []uint{
		orderShippingMigrationVersion, orderStatusLogMigrationVersion, walletMigrationVersion,
		rechargeMigrationVersion, pointsMigrationVersion, couponSnapshotMigrationVersion,
	} {
		if !ups[v] {
			t.Fatalf("缺少 up 迁移: %d", v)
		}
		if !downs[v] {
			t.Fatalf("缺少 down 迁移: %d", v)
		}
		if v <= 20261110000003 {
			t.Fatalf("迁移版本 %d 必须大于上一批次最高版本 20261110000003", v)
		}
	}
	if !(orderShippingMigrationVersion < orderStatusLogMigrationVersion &&
		orderStatusLogMigrationVersion < walletMigrationVersion &&
		walletMigrationVersion < rechargeMigrationVersion &&
		rechargeMigrationVersion < pointsMigrationVersion &&
		pointsMigrationVersion < couponSnapshotMigrationVersion) {
		t.Fatalf("本批次迁移版本必须递增")
	}
}
