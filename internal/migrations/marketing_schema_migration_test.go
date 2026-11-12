package migrations

// B1-03-03 营销与关系模型迁移测试：
//   - 5 个新增迁移可 up → down → up，回滚只影响本批次新增表/列；
//   - 优惠券批次、适用范围、claim_no 每人限领与历史券兼容；
//   - 拼团团队容量、参与者唯一关系与订单唯一关联；
//   - 砍价实例唯一、助力去重与价格推进的数据库语义；
//   - 会员等级门槛唯一、用户会员唯一、成长值流水幂等，且不占用积分账本；
//   - 一级推荐关系唯一；佣金按订单幂等、退款冲回字段与退款单唯一。
//
// 全部使用真实 MySQL（与其它迁移测试同一环境假设），不使用 Mock 冒充数据库约束验证。

import (
	"context"
	"database/sql"
	"io/fs"
	"strings"
	"testing"
)

// 本批次新增的迁移版本。
const (
	couponBatchMigrationVersion  = uint(20261110000010)
	groupBuyMigrationVersion     = uint(20261110000011)
	bargainMigrationVersion      = uint(20261110000012)
	memberMigrationVersion       = uint(20261110000013)
	distributionMigrationVersion = uint(20261110000014)
	previousBatchMaxVersion      = uint(20261110000009)
)

// marketingTables 是本批次新增的表（down 后必须全部消失）。
var marketingTables = []string{
	"coupon_batches", "coupon_template_scopes",
	"group_buy_activities", "group_buy_teams", "group_buy_participants",
	"bargain_activities", "bargain_instances", "bargain_help_records",
	"member_levels", "user_memberships", "member_growth_logs",
	"user_referrals", "distribution_commissions",
}

// hasIndex 判断指定索引是否存在（用于校验唯一键替换与恢复）。
func hasIndex(t *testing.T, db *sql.DB, table, index string) bool {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM information_schema.statistics
		 WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`, table, index).Scan(&n); err != nil {
		t.Fatalf("check index %s.%s: %v", table, index, err)
	}
	return n > 0
}

// insertBargainActivity 写入一个砍价活动并返回 id。
func insertBargainActivity(t *testing.T, db *sql.DB, productID, skuID int64) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `INSERT INTO bargain_activities
		(product_id, sku_id, name, start_price, floor_price, help_amount, max_help_count, start_time, end_time)
		VALUES (?, ?, '砍价活动', 1000, 900, 30, 5, DATE_SUB(NOW(), INTERVAL 1 DAY), DATE_ADD(NOW(), INTERVAL 7 DAY))`,
		productID, skuID)
	if err != nil {
		t.Fatalf("insert bargain activity: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("bargain activity last insert id: %v", err)
	}
	return id
}

// TestMarketingSchemaMigrationUpDownUp 覆盖 5 个迁移的 up → down → up：
// 回滚后新增表/列/索引消失，既有订单/钱包/积分/秒杀数据保留；重新 up 后结构恢复、默认值正确。
func TestMarketingSchemaMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Fatalf("version=%d want %d", v, latestMigrationVersion)
	}
	for _, table := range marketingTables {
		if !tableExists(t, db, table) {
			t.Fatalf("up 后应存在表 %s", table)
		}
	}

	// 既有模型数据：回滚本批次后必须原样保留。
	categoryID := insertCategory(t, db, "营销历史分类")
	productID := insertProduct(t, db, categoryID, "营销历史商品")
	skuID := insertSku(t, db, productID, "默认", 1000, nil, "")
	orderID := insertHistoricalOrder(t, db, "B10303-HISTORY-1", 1000, 1000, 0)
	if _, err := db.ExecContext(ctx, "INSERT INTO wallet_accounts (user_id, balance) VALUES (1, 500)"); err != nil {
		t.Fatalf("insert wallet account: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO user_points_accounts (user_id, points) VALUES (1, 88)"); err != nil {
		t.Fatalf("insert points account: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO flash_sale_activities (name, start_time, end_time)
		VALUES ('历史秒杀', DATE_SUB(NOW(), INTERVAL 1 DAY), DATE_ADD(NOW(), INTERVAL 1 DAY))`); err != nil {
		t.Fatalf("insert flash sale activity: %v", err)
	}

	// 本批次数据：用于证明 down 能处理有数据的表（user_coupons 多领行按声明被裁剪）。
	templateID := insertCouponTemplate(t, db, "批次券模板")
	res, err := db.ExecContext(ctx, `INSERT INTO coupon_batches
		(template_id, total_quantity, claim_start_time, claim_end_time)
		VALUES (?, 10, DATE_SUB(NOW(), INTERVAL 1 DAY), DATE_ADD(NOW(), INTERVAL 7 DAY))`, templateID)
	if err != nil {
		t.Fatalf("insert coupon batch: %v", err)
	}
	batchID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("batch last insert id: %v", err)
	}
	for claimNo := 1; claimNo <= 2; claimNo++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO user_coupons
			(user_id, template_id, batch_id, claim_no, status, received_at)
			VALUES (42, ?, ?, ?, 10, NOW())`, templateID, batchID, claimNo); err != nil {
			t.Fatalf("insert user coupon claim_no=%d: %v", claimNo, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO coupon_template_scopes (template_id, product_id) VALUES (?, ?)`,
		templateID, productID); err != nil {
		t.Fatalf("insert coupon scope: %v", err)
	}

	activityRes, err := db.ExecContext(ctx, `INSERT INTO group_buy_activities
		(product_id, sku_id, name, group_price, group_size, team_ttl_minutes, start_time, end_time)
		VALUES (?, ?, '拼团活动', 800, 2, 60, DATE_SUB(NOW(), INTERVAL 1 DAY), DATE_ADD(NOW(), INTERVAL 7 DAY))`,
		productID, skuID)
	if err != nil {
		t.Fatalf("insert group buy activity: %v", err)
	}
	groupActivityID, err := activityRes.LastInsertId()
	if err != nil {
		t.Fatalf("group buy activity last insert id: %v", err)
	}
	teamRes, err := db.ExecContext(ctx, `INSERT INTO group_buy_teams
		(activity_id, leader_user_id, group_price, required_size, expire_at)
		VALUES (?, 42, 800, 2, DATE_ADD(NOW(), INTERVAL 1 HOUR))`, groupActivityID)
	if err != nil {
		t.Fatalf("insert group buy team: %v", err)
	}
	teamID, err := teamRes.LastInsertId()
	if err != nil {
		t.Fatalf("team last insert id: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO group_buy_participants (team_id, user_id) VALUES (?, 42)`, teamID); err != nil {
		t.Fatalf("insert group buy participant: %v", err)
	}

	bargainActivityID := insertBargainActivity(t, db, productID, skuID)
	instanceRes, err := db.ExecContext(ctx, `INSERT INTO bargain_instances
		(activity_id, user_id, start_price, floor_price, current_price, max_help_count, expire_at)
		VALUES (?, 42, 1000, 900, 1000, 5, DATE_ADD(NOW(), INTERVAL 1 DAY))`, bargainActivityID)
	if err != nil {
		t.Fatalf("insert bargain instance: %v", err)
	}
	instanceID, err := instanceRes.LastInsertId()
	if err != nil {
		t.Fatalf("instance last insert id: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bargain_help_records
		(instance_id, helper_user_id, cut_amount, price_before, price_after)
		VALUES (?, 43, 30, 1000, 970)`, instanceID); err != nil {
		t.Fatalf("insert bargain help: %v", err)
	}

	levelRes, err := db.ExecContext(ctx, `INSERT INTO member_levels
		(level_no, name, growth_threshold, discount_rate_bp) VALUES (1, '铜牌', 0, 9900)`)
	if err != nil {
		t.Fatalf("insert member level: %v", err)
	}
	levelID, err := levelRes.LastInsertId()
	if err != nil {
		t.Fatalf("member level last insert id: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO user_memberships (user_id, level_id, growth_value) VALUES (42, ?, 100)`, levelID); err != nil {
		t.Fatalf("insert membership: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO member_growth_logs
		(user_id, change_amount, growth_before, growth_after, change_type, biz_type, idempotency_key)
		VALUES (42, 100, 0, 100, 'order_reward', 'order', 'growth:order:1')`); err != nil {
		t.Fatalf("insert growth log: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO user_referrals (user_id, referrer_user_id) VALUES (42, 41)`); err != nil {
		t.Fatalf("insert referral: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO distribution_commissions
		(order_id, buyer_user_id, beneficiary_user_id, base_amount, rate_bp, commission_amount)
		VALUES (?, 42, 41, 1000, 500, 50)`, orderID); err != nil {
		t.Fatalf("insert commission: %v", err)
	}

	// 回滚本批次 5 个版本，回到 B1-03-02 的 schema（固定目标版本，避免后续批次改变落点）。
	migrateTo(t, previousBatchMaxVersion)
	if v := currentVersion(t, db); v != previousBatchMaxVersion {
		t.Fatalf("down 后 version=%d want %d", v, previousBatchMaxVersion)
	}
	for _, table := range marketingTables {
		if tableExists(t, db, table) {
			t.Fatalf("down 后不应存在表 %s", table)
		}
	}

	templateSpec := readActualTable(t, db, "coupon_templates")
	for _, col := range []string{"discount_type", "discount_rate_bp", "per_user_limit"} {
		if hasColumn(templateSpec, col) {
			t.Fatalf("down 后 coupon_templates 不应存在列 %s", col)
		}
	}
	couponSpec := readActualTable(t, db, "user_coupons")
	for _, col := range []string{"batch_id", "claim_no"} {
		if hasColumn(couponSpec, col) {
			t.Fatalf("down 后 user_coupons 不应存在列 %s", col)
		}
	}
	if !hasIndex(t, db, "user_coupons", "uk_user_template") {
		t.Fatalf("down 后应恢复 uk_user_template")
	}
	if hasIndex(t, db, "user_coupons", "uk_user_template_claim") {
		t.Fatalf("down 后不应存在 uk_user_template_claim")
	}

	// 多领记录按 down 文件声明被裁剪，只保留首张券；既有域数据完好。
	var remaining, deleted int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_coupons WHERE user_id = 42").Scan(&remaining); err != nil {
		t.Fatalf("count coupons after down: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("down 后应只保留 claim_no=1 的券，实际 %d 行", remaining)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_coupons WHERE user_id = 42 AND claim_no > 1").Scan(&deleted); err == nil {
		t.Fatalf("claim_no 列应已删除，查询不应成功")
	}
	var walletBalance, points int64
	if err := db.QueryRowContext(ctx, "SELECT balance FROM wallet_accounts WHERE user_id = 1").Scan(&walletBalance); err != nil {
		t.Fatalf("down 后读取钱包: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT points FROM user_points_accounts WHERE user_id = 1").Scan(&points); err != nil {
		t.Fatalf("down 后读取积分: %v", err)
	}
	if walletBalance != 500 || points != 88 {
		t.Fatalf("既有资金/积分被改变: wallet=%d points=%d", walletBalance, points)
	}
	var orderTotal int64
	if err := db.QueryRowContext(ctx, "SELECT total_amount FROM orders WHERE id = ?", orderID).Scan(&orderTotal); err != nil {
		t.Fatalf("down 后读取订单: %v", err)
	}
	if orderTotal != 1000 {
		t.Fatalf("既有订单金额被改变: %d", orderTotal)
	}
	var flashCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM flash_sale_activities").Scan(&flashCount); err != nil {
		t.Fatalf("down 后读取秒杀活动: %v", err)
	}
	if flashCount != 1 {
		t.Fatalf("既有秒杀活动被改变: %d", flashCount)
	}

	// 重新升级：新增结构恢复，历史券 claim_no 默认 1，既有数据不重算。
	if err := Up(ctx); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Fatalf("重新 up 后 version=%d want %d", v, latestMigrationVersion)
	}
	for _, table := range marketingTables {
		if !tableExists(t, db, table) {
			t.Fatalf("重新 up 后应存在表 %s", table)
		}
	}
	if !hasIndex(t, db, "user_coupons", "uk_user_template_claim") {
		t.Fatalf("重新 up 后应存在 uk_user_template_claim")
	}
	var claimNo int
	if err := db.QueryRowContext(ctx, "SELECT claim_no FROM user_coupons WHERE user_id = 42").Scan(&claimNo); err != nil {
		t.Fatalf("重新 up 后读取 claim_no: %v", err)
	}
	if claimNo != 1 {
		t.Fatalf("重新 up 后历史券 claim_no=%d want 1", claimNo)
	}
}

// TestCouponBatchAndClaimConstraints 覆盖优惠券：
// 批次外键、模板默认折扣类型/限领、claim_no 每人限领、历史默认 1 的重复领取语义、适用范围唯一。
func TestCouponBatchAndClaimConstraints(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	templateID := insertCouponTemplate(t, db, "批次约束券")
	var discountType, perUserLimit int
	var discountRate sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT discount_type, discount_rate_bp, per_user_limit
		FROM coupon_templates WHERE id = ?`, templateID).Scan(&discountType, &discountRate, &perUserLimit); err != nil {
		t.Fatalf("读取模板新增列: %v", err)
	}
	if discountType != 1 || discountRate.Valid || perUserLimit != 1 {
		t.Fatalf("模板默认值不符: type=%d rate=%v limit=%d", discountType, discountRate, perUserLimit)
	}
	if _, err := db.ExecContext(ctx, "UPDATE coupon_templates SET per_user_limit = 3 WHERE id = ?", templateID); err != nil {
		t.Fatalf("更新每人限领: %v", err)
	}

	res, err := db.ExecContext(ctx, `INSERT INTO coupon_batches
		(template_id, total_quantity, claim_start_time, claim_end_time)
		VALUES (?, 5, DATE_SUB(NOW(), INTERVAL 1 DAY), DATE_ADD(NOW(), INTERVAL 1 DAY))`, templateID)
	if err != nil {
		t.Fatalf("insert batch: %v", err)
	}
	batchID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("batch last insert id: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO coupon_batches
		(template_id, total_quantity, claim_start_time, claim_end_time)
		VALUES (999999, 1, NOW(), NOW())`)
	requireMySQLError(t, err, 1452, "批次引用不存在的模板")

	// 批次配额闸门：issued_quantity < total_quantity 才允许 +1；售罄后条件更新 0 行（INV-C12）。
	claimFromBatch := func() int64 {
		res, err := db.ExecContext(ctx, `UPDATE coupon_batches
			SET issued_quantity = issued_quantity + 1
			WHERE id = ? AND status = 1 AND issued_quantity < total_quantity
			  AND claim_start_time <= NOW() AND claim_end_time > NOW()`, batchID)
		if err != nil {
			t.Fatalf("batch quota update: %v", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			t.Fatalf("batch quota rows affected: %v", err)
		}
		return affected
	}
	for i := 0; i < 5; i++ {
		if got := claimFromBatch(); got != 1 {
			t.Fatalf("第 %d 次批次配额推进 affected=%d want 1", i+1, got)
		}
	}
	if got := claimFromBatch(); got != 0 {
		t.Fatalf("批次售罄后 affected=%d want 0", got)
	}

	// 同一用户同一模板：claim_no 1/2 均成功，重复 claim_no 冲突；per_user_limit 由应用按 1..N 取值。
	if _, err := db.ExecContext(ctx, `INSERT INTO user_coupons (user_id, template_id, batch_id, claim_no, status, received_at)
		VALUES (100, ?, ?, 1, 10, NOW())`, templateID, batchID); err != nil {
		t.Fatalf("insert claim 1: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO user_coupons (user_id, template_id, batch_id, claim_no, status, received_at)
		VALUES (100, ?, ?, 2, 10, NOW())`, templateID, batchID); err != nil {
		t.Fatalf("insert claim 2: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO user_coupons (user_id, template_id, batch_id, claim_no, status, received_at)
		VALUES (100, ?, ?, 2, 10, NOW())`, templateID, batchID)
	requireMySQLError(t, err, 1062, "同一用户同一模板重复 claim_no")

	// 历史/既有领取路径不写 claim_no：默认 1，第二次仍为 1062（既有 INV-C01 语义不变）。
	if _, err := db.ExecContext(ctx, `INSERT INTO user_coupons (user_id, template_id, status, received_at)
		VALUES (101, ?, 10, NOW())`, templateID); err != nil {
		t.Fatalf("insert legacy claim: %v", err)
	}
	var defaultClaimNo int
	if err := db.QueryRowContext(ctx, "SELECT claim_no FROM user_coupons WHERE user_id = 101").Scan(&defaultClaimNo); err != nil {
		t.Fatalf("读取默认 claim_no: %v", err)
	}
	if defaultClaimNo != 1 {
		t.Fatalf("默认 claim_no=%d want 1", defaultClaimNo)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO user_coupons (user_id, template_id, status, received_at)
		VALUES (101, ?, 10, NOW())`, templateID)
	requireMySQLError(t, err, 1062, "既有重复领取语义")

	// 适用范围：商品/分类各至多一条；product_id/category_id 恰有一个由应用保证。
	productID := insertProduct(t, db, insertCategory(t, db, "范围分类"), "范围商品")
	if _, err := db.ExecContext(ctx, `INSERT INTO coupon_template_scopes (template_id, product_id) VALUES (?, ?)`,
		templateID, productID); err != nil {
		t.Fatalf("insert product scope: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO coupon_template_scopes (template_id, product_id) VALUES (?, ?)`,
		templateID, productID)
	requireMySQLError(t, err, 1062, "同一模板重复绑定同一商品")
	_, err = db.ExecContext(ctx, `INSERT INTO coupon_template_scopes (template_id, product_id) VALUES (?, 999999)`,
		templateID)
	requireMySQLError(t, err, 1452, "适用范围引用不存在的商品")
	_, err = db.ExecContext(ctx, `INSERT INTO coupon_template_scopes (template_id, category_id) VALUES (?, 999999)`,
		templateID)
	requireMySQLError(t, err, 1452, "适用范围引用不存在的分类")

	// 批次是模板的从属数据：有批次时模板不可删除（RESTRICT）。
	_, err = db.ExecContext(ctx, "DELETE FROM coupon_templates WHERE id = ?", templateID)
	requireMySQLError(t, err, 1451, "删除仍有批次的模板")
}

// TestGroupBuyTeamParticipantConstraints 覆盖拼团：
// 团队容量条件更新、参与者唯一关系、订单唯一关联、团队/活动外键保护。
func TestGroupBuyTeamParticipantConstraints(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	categoryID := insertCategory(t, db, "拼团分类")
	productID := insertProduct(t, db, categoryID, "拼团商品")
	skuID := insertSku(t, db, productID, "默认", 1000, nil, "")

	res, err := db.ExecContext(ctx, `INSERT INTO group_buy_activities
		(product_id, sku_id, name, group_price, group_size, team_ttl_minutes, start_time, end_time)
		VALUES (?, ?, '两人团', 800, 2, 60, DATE_SUB(NOW(), INTERVAL 1 DAY), DATE_ADD(NOW(), INTERVAL 7 DAY))`,
		productID, skuID)
	if err != nil {
		t.Fatalf("insert group buy activity: %v", err)
	}
	activityID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("activity last insert id: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO group_buy_activities
		(product_id, sku_id, name, group_price, group_size, team_ttl_minutes, start_time, end_time)
		VALUES (?, 999999, '坏 SKU', 800, 2, 60, NOW(), NOW())`, productID)
	requireMySQLError(t, err, 1452, "拼团活动引用不存在的 SKU")

	teamRes, err := db.ExecContext(ctx, `INSERT INTO group_buy_teams
		(activity_id, leader_user_id, group_price, required_size, expire_at)
		VALUES (?, 1, 800, 2, DATE_ADD(NOW(), INTERVAL 1 HOUR))`, activityID)
	if err != nil {
		t.Fatalf("insert team: %v", err)
	}
	teamID, err := teamRes.LastInsertId()
	if err != nil {
		t.Fatalf("team last insert id: %v", err)
	}
	var joinedCount, teamStatus int
	if err := db.QueryRowContext(ctx, "SELECT joined_count, status FROM group_buy_teams WHERE id = ?", teamID).
		Scan(&joinedCount, &teamStatus); err != nil {
		t.Fatalf("读取团队默认值: %v", err)
	}
	if joinedCount != 1 || teamStatus != 10 {
		t.Fatalf("团队默认值不符: joined_count=%d status=%d", joinedCount, teamStatus)
	}

	// 团长与普通参与者都落参与记录；同一用户同一团唯一。
	if _, err := db.ExecContext(ctx, `INSERT INTO group_buy_participants (team_id, user_id, order_id)
		VALUES (?, 1, 1001)`, teamID); err != nil {
		t.Fatalf("insert leader participant: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO group_buy_participants (team_id, user_id, order_id)
		VALUES (?, 1, 1002)`, teamID)
	requireMySQLError(t, err, 1062, "同一用户重复参团")
	_, err = db.ExecContext(ctx, `INSERT INTO group_buy_participants (team_id, user_id, order_id)
		VALUES (999999, 2, 1003)`)
	requireMySQLError(t, err, 1452, "参与记录引用不存在的团")

	// 一个订单只对应一条参与记录；NULL 订单允许多条（未支付占位）。
	_, err = db.ExecContext(ctx, `INSERT INTO group_buy_participants (team_id, user_id, order_id)
		VALUES (?, 2, 1001)`, teamID)
	requireMySQLError(t, err, 1062, "同一订单重复参团")
	for _, uid := range []int{3, 4} {
		if _, err := db.ExecContext(ctx, `INSERT INTO group_buy_participants (team_id, user_id, order_id)
			VALUES (?, ?, NULL)`, teamID, uid); err != nil {
			t.Fatalf("insert participant with null order: %v", err)
		}
	}

	// 容量闸门：joined_count < required_size 才允许 +1；满员后条件更新 0 行。
	capacityUpdate := func() int64 {
		res, err := db.ExecContext(ctx, `UPDATE group_buy_teams
			SET joined_count = joined_count + 1
			WHERE id = ? AND status = 10 AND joined_count < required_size AND expire_at > NOW()`, teamID)
		if err != nil {
			t.Fatalf("capacity update: %v", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			t.Fatalf("capacity rows affected: %v", err)
		}
		return affected
	}
	if got := capacityUpdate(); got != 1 {
		t.Fatalf("首次容量推进 affected=%d want 1", got)
	}
	if got := capacityUpdate(); got != 0 {
		t.Fatalf("满员后容量推进 affected=%d want 0", got)
	}
	if _, err := db.ExecContext(ctx, `UPDATE group_buy_teams SET status = 20, closed_at = NOW()
		WHERE id = ? AND status = 10 AND joined_count >= required_size`, teamID); err != nil {
		t.Fatalf("成团推进: %v", err)
	}

	// 活动被团引用时不可删除。
	_, err = db.ExecContext(ctx, "DELETE FROM group_buy_activities WHERE id = ?", activityID)
	requireMySQLError(t, err, 1451, "删除仍有团的拼团活动")
}

// TestBargainHelpDuplicateConstraints 覆盖砍价：
// 一人一活动一实例、同一助力者对同一实例只能助力一次、价格推进不穿透底价、外键保护。
func TestBargainHelpDuplicateConstraints(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	categoryID := insertCategory(t, db, "砍价分类")
	productID := insertProduct(t, db, categoryID, "砍价商品")
	skuID := insertSku(t, db, productID, "默认", 1000, nil, "")
	activityID := insertBargainActivity(t, db, productID, skuID)

	res, err := db.ExecContext(ctx, `INSERT INTO bargain_instances
		(activity_id, user_id, start_price, floor_price, current_price, max_help_count, expire_at)
		VALUES (?, 1, 1000, 900, 1000, 5, DATE_ADD(NOW(), INTERVAL 1 DAY))`, activityID)
	if err != nil {
		t.Fatalf("insert bargain instance: %v", err)
	}
	instanceID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("instance last insert id: %v", err)
	}
	var helpedCount, status int
	if err := db.QueryRowContext(ctx, "SELECT helped_count, status FROM bargain_instances WHERE id = ?", instanceID).
		Scan(&helpedCount, &status); err != nil {
		t.Fatalf("读取实例默认值: %v", err)
	}
	if helpedCount != 0 || status != 10 {
		t.Fatalf("实例默认值不符: helped_count=%d status=%d", helpedCount, status)
	}

	// 同一用户同一活动只能有一个「进行中」实例。
	_, err = db.ExecContext(ctx, `INSERT INTO bargain_instances
		(activity_id, user_id, start_price, floor_price, current_price, max_help_count, expire_at)
		VALUES (?, 1, 1000, 900, 1000, 5, DATE_ADD(NOW(), INTERVAL 1 DAY))`, activityID)
	requireMySQLError(t, err, 1062, "同一用户同一活动重复发起进行中砍价")

	// 同一助力者对同一实例只能一次；不同助力者可分别助力。
	if _, err := db.ExecContext(ctx, `INSERT INTO bargain_help_records
		(instance_id, helper_user_id, cut_amount, price_before, price_after)
		VALUES (?, 2, 30, 1000, 970)`, instanceID); err != nil {
		t.Fatalf("insert help: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO bargain_help_records
		(instance_id, helper_user_id, cut_amount, price_before, price_after)
		VALUES (?, 2, 30, 970, 940)`, instanceID)
	requireMySQLError(t, err, 1062, "同一用户重复助力同一实例")
	if _, err := db.ExecContext(ctx, `INSERT INTO bargain_help_records
		(instance_id, helper_user_id, cut_amount, price_before, price_after)
		VALUES (?, 3, 30, 970, 940)`, instanceID); err != nil {
		t.Fatalf("insert another helper: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO bargain_help_records
		(instance_id, helper_user_id, cut_amount, price_before, price_after)
		VALUES (999999, 2, 30, 1000, 970)`)
	requireMySQLError(t, err, 1452, "助力记录引用不存在的砍价实例")

	// 价格推进：GREATEST(floor_price, current - help_amount) 不穿透底价；最后一段实际砍减可小于名义额。
	priceUpdate := func(helpAmount int) int64 {
		if _, err := db.ExecContext(ctx, `UPDATE bargain_instances
			SET current_price = GREATEST(floor_price, current_price - ?), helped_count = helped_count + 1
			WHERE id = ? AND status = 10 AND helped_count < max_help_count AND current_price > floor_price`,
			helpAmount, instanceID); err != nil {
			t.Fatalf("price update: %v", err)
		}
		var current int64
		if err := db.QueryRowContext(ctx, "SELECT current_price FROM bargain_instances WHERE id = ?", instanceID).
			Scan(&current); err != nil {
			t.Fatalf("读取当前价: %v", err)
		}
		return current
	}
	if current := priceUpdate(60); current != 940 {
		t.Fatalf("首次推进 current=%d want 940", current)
	}
	if current := priceUpdate(60); current != 900 {
		t.Fatalf("第二次推进应停在底价 current=%d want 900", current)
	}
	_, err = db.ExecContext(ctx, `UPDATE bargain_instances SET current_price = current_price - 10000 WHERE id = ?`, instanceID)
	requireMySQLError(t, err, 1690, "砍价价格扣减为负")

	// 一个订单只对应一个实例。
	if _, err := db.ExecContext(ctx, `INSERT INTO bargain_instances
		(activity_id, user_id, start_price, floor_price, current_price, max_help_count, expire_at, order_id)
		VALUES (?, 2, 1000, 900, 1000, 5, DATE_ADD(NOW(), INTERVAL 1 DAY), 2001)`, activityID); err != nil {
		t.Fatalf("insert instance with order: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO bargain_instances
		(activity_id, user_id, start_price, floor_price, current_price, max_help_count, expire_at, order_id)
		VALUES (?, 3, 1000, 900, 1000, 5, DATE_ADD(NOW(), INTERVAL 1 DAY), 2001)`, activityID)
	requireMySQLError(t, err, 1062, "同一订单重复关联砍价实例")

	// 失败/过期后允许重新发起：active_key 变 NULL，历史实例保留。
	if _, err := db.ExecContext(ctx, "UPDATE bargain_instances SET status = 30 WHERE id = ?", instanceID); err != nil {
		t.Fatalf("标记砍价失败: %v", err)
	}
	retryRes, err := db.ExecContext(ctx, `INSERT INTO bargain_instances
		(activity_id, user_id, start_price, floor_price, current_price, max_help_count, expire_at)
		VALUES (?, 1, 1000, 900, 1000, 5, DATE_ADD(NOW(), INTERVAL 1 DAY))`, activityID)
	if err != nil {
		t.Fatalf("失败后重新发起砍价: %v", err)
	}
	retryID, err := retryRes.LastInsertId()
	if err != nil {
		t.Fatalf("重试实例 last insert id: %v", err)
	}
	if retryID == instanceID {
		t.Fatalf("重试应创建新实例，不能复用历史实例")
	}
	var activeKey sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT active_key FROM bargain_instances WHERE id = ?", retryID).Scan(&activeKey); err != nil {
		t.Fatalf("读取重试实例 active_key: %v", err)
	}
	if !activeKey.Valid {
		t.Fatalf("进行中实例的 active_key 不应为 NULL")
	}

	// 实例有助力记录时不可删除。
	_, err = db.ExecContext(ctx, "DELETE FROM bargain_instances WHERE id = ?", instanceID)
	requireMySQLError(t, err, 1451, "删除仍有助力记录的砍价实例")
}

// TestMemberLevelAndGrowthConstraints 覆盖会员：
// 等级序号/成长门槛唯一、用户会员唯一、等级外键、成长值非负、成长流水幂等，且与积分账本相互独立。
func TestMemberLevelAndGrowthConstraints(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	res, err := db.ExecContext(ctx, `INSERT INTO member_levels (level_no, name, growth_threshold)
		VALUES (1, '普通会员', 0)`)
	if err != nil {
		t.Fatalf("insert member level: %v", err)
	}
	levelID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("level last insert id: %v", err)
	}
	var threshold, discountRate int64
	if err := db.QueryRowContext(ctx, "SELECT growth_threshold, discount_rate_bp FROM member_levels WHERE id = ?", levelID).
		Scan(&threshold, &discountRate); err != nil {
		t.Fatalf("读取等级默认值: %v", err)
	}
	if threshold != 0 || discountRate != 10000 {
		t.Fatalf("等级默认值不符: threshold=%d discount=%d", threshold, discountRate)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO member_levels (level_no, name, growth_threshold) VALUES (1, '重复序号', 100)`)
	requireMySQLError(t, err, 1062, "重复等级序号")
	_, err = db.ExecContext(ctx, `INSERT INTO member_levels (level_no, name, growth_threshold) VALUES (2, '重复门槛', 0)`)
	requireMySQLError(t, err, 1062, "重复成长门槛")

	if _, err := db.ExecContext(ctx, `INSERT INTO user_memberships (user_id, level_id, growth_value) VALUES (1, ?, 0)`, levelID); err != nil {
		t.Fatalf("insert membership: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO user_memberships (user_id, level_id, growth_value) VALUES (1, ?, 10)`, levelID)
	requireMySQLError(t, err, 1062, "同一用户重复会员记录")
	_, err = db.ExecContext(ctx, `INSERT INTO user_memberships (user_id, level_id, growth_value) VALUES (2, 999999, 0)`)
	requireMySQLError(t, err, 1452, "会员记录引用不存在的等级")
	_, err = db.ExecContext(ctx, "DELETE FROM member_levels WHERE id = ?", levelID)
	requireMySQLError(t, err, 1451, "删除仍有会员的等级")

	// 成长值余额不可为负；带符号流水可记录扣减。
	if _, err := db.ExecContext(ctx, `INSERT INTO member_growth_logs
		(user_id, change_amount, growth_before, growth_after, change_type, biz_type, idempotency_key)
		VALUES (1, 100, 0, 100, 'order_reward', 'order', 'growth:order:100')`); err != nil {
		t.Fatalf("insert growth log: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO member_growth_logs
		(user_id, change_amount, growth_before, growth_after, change_type, biz_type, idempotency_key)
		VALUES (1, 100, 0, 100, 'order_reward', 'order', 'growth:order:100')`)
	requireMySQLError(t, err, 1062, "成长值流水幂等键重复")
	if _, err := db.ExecContext(ctx, `INSERT INTO member_growth_logs
		(user_id, change_amount, growth_before, growth_after, change_type, biz_type, idempotency_key)
		VALUES (1, -30, 100, 70, 'refund_deduct', 'refund', 'growth:refund:100')`); err != nil {
		t.Fatalf("insert negative growth log: %v", err)
	}
	_, err = db.ExecContext(ctx, "UPDATE user_memberships SET growth_value = growth_value - 10 WHERE user_id = 1")
	requireMySQLError(t, err, 1690, "成长值扣减为负")

	// 积分账本独立：同一用户可同时有会员成长账本与积分账本，互不占用。
	if _, err := db.ExecContext(ctx, "INSERT INTO user_points_accounts (user_id, points) VALUES (1, 50)"); err != nil {
		t.Fatalf("insert points account: %v", err)
	}
	var points int64
	if err := db.QueryRowContext(ctx, "SELECT points FROM user_points_accounts WHERE user_id = 1").Scan(&points); err != nil {
		t.Fatalf("读取积分: %v", err)
	}
	if points != 50 {
		t.Fatalf("积分账户被成长值影响: %d", points)
	}
	var growthLogs, pointsLogs int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM member_growth_logs WHERE user_id = 1").Scan(&growthLogs); err != nil {
		t.Fatalf("count growth logs: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_points_logs WHERE user_id = 1").Scan(&pointsLogs); err != nil {
		t.Fatalf("count points logs: %v", err)
	}
	if growthLogs != 2 || pointsLogs != 0 {
		t.Fatalf("成长值与积分流水应独立: growth=%d points=%d", growthLogs, pointsLogs)
	}
}

// TestReferralUniqueness 覆盖一级推荐关系：一个用户至多一个推荐人，同一推荐人可绑定多个下级。
func TestReferralUniqueness(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO user_referrals (user_id, referrer_user_id) VALUES (10, 1)`); err != nil {
		t.Fatalf("insert referral: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO user_referrals (user_id, referrer_user_id) VALUES (11, 1)`); err != nil {
		t.Fatalf("insert second referral: %v", err)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO user_referrals (user_id, referrer_user_id) VALUES (10, 2)`)
	requireMySQLError(t, err, 1062, "同一用户重复绑定推荐人")

	var downlines int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_referrals WHERE referrer_user_id = 1").Scan(&downlines); err != nil {
		t.Fatalf("count downlines: %v", err)
	}
	if downlines != 2 {
		t.Fatalf("推荐人下级数=%d want 2", downlines)
	}
}

// TestDistributionCommissionIdempotency 覆盖佣金：
// 一订单一条佣金（重复计提 1062）、退款单唯一（重复冲回 1062）、NULL 退款单可多条、
// 冲回字段可写、订单外键保护。
func TestDistributionCommissionIdempotency(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	order1 := insertHistoricalOrder(t, db, "B10303-COMM-1", 1000, 1000, 0)
	order2 := insertHistoricalOrder(t, db, "B10303-COMM-2", 2000, 2000, 0)
	order3 := insertHistoricalOrder(t, db, "B10303-COMM-3", 3000, 3000, 0)

	res, err := db.ExecContext(ctx, `INSERT INTO distribution_commissions
		(order_id, buyer_user_id, beneficiary_user_id, base_amount, rate_bp, commission_amount)
		VALUES (?, 10, 1, 1000, 500, 50)`, order1)
	if err != nil {
		t.Fatalf("insert commission: %v", err)
	}
	commissionID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("commission last insert id: %v", err)
	}
	var status int
	var reversedAmount int64
	var settledAt, reversedAt, refundID sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT status, settled_at, reversed_amount, reversed_at, refund_id
		FROM distribution_commissions WHERE id = ?`, commissionID).
		Scan(&status, &settledAt, &reversedAmount, &reversedAt, &refundID); err != nil {
		t.Fatalf("读取佣金默认值: %v", err)
	}
	if status != 10 || settledAt.Valid || reversedAmount != 0 || reversedAt.Valid || refundID.Valid {
		t.Fatalf("佣金默认值不符: status=%d settled=%v reversed=%d/%v refund=%v",
			status, settledAt, reversedAmount, reversedAt, refundID)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO distribution_commissions
		(order_id, buyer_user_id, beneficiary_user_id, base_amount, rate_bp, commission_amount)
		VALUES (?, 10, 1, 1000, 500, 50)`, order1)
	requireMySQLError(t, err, 1062, "同一订单重复计提佣金")
	_, err = db.ExecContext(ctx, `INSERT INTO distribution_commissions
		(order_id, buyer_user_id, beneficiary_user_id, base_amount, rate_bp, commission_amount)
		VALUES (999999, 10, 1, 1000, 500, 50)`)
	requireMySQLError(t, err, 1452, "佣金引用不存在的订单")

	// 多个 NULL 退款单允许；同一退款单只能冲回一条佣金。
	if _, err := db.ExecContext(ctx, `INSERT INTO distribution_commissions
		(order_id, buyer_user_id, beneficiary_user_id, base_amount, rate_bp, commission_amount)
		VALUES (?, 20, 2, 2000, 500, 100)`, order2); err != nil {
		t.Fatalf("insert commission with null refund: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO distribution_commissions
		(order_id, buyer_user_id, beneficiary_user_id, base_amount, rate_bp, commission_amount, refund_id)
		VALUES (?, 30, 3, 3000, 500, 150, 700)`, order3); err != nil {
		t.Fatalf("insert commission with refund: %v", err)
	}
	order4 := insertHistoricalOrder(t, db, "B10303-COMM-4", 4000, 4000, 0)
	_, err = db.ExecContext(ctx, `INSERT INTO distribution_commissions
		(order_id, buyer_user_id, beneficiary_user_id, base_amount, rate_bp, commission_amount, refund_id)
		VALUES (?, 40, 4, 4000, 500, 200, 700)`, order4)
	requireMySQLError(t, err, 1062, "同一退款单重复冲回佣金")

	// 冲回：状态条件更新只生效一次，冲回金额/时间/退款单/原因同时落库。
	reverse := func() int64 {
		res, err := db.ExecContext(ctx, `UPDATE distribution_commissions
			SET status = 30, reversed_amount = commission_amount, reversed_at = NOW(),
			    refund_id = 900, reversal_reason = 'refund'
			WHERE id = ? AND status IN (10, 20)`, commissionID)
		if err != nil {
			t.Fatalf("reverse commission: %v", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			t.Fatalf("reverse rows affected: %v", err)
		}
		return affected
	}
	if got := reverse(); got != 1 {
		t.Fatalf("首次冲回 affected=%d want 1", got)
	}
	if got := reverse(); got != 0 {
		t.Fatalf("重复冲回 affected=%d want 0", got)
	}
	var finalStatus int
	var finalReversed int64
	var reason string
	if err := db.QueryRowContext(ctx, `SELECT status, reversed_amount, reversal_reason
		FROM distribution_commissions WHERE id = ?`, commissionID).
		Scan(&finalStatus, &finalReversed, &reason); err != nil {
		t.Fatalf("读取冲回结果: %v", err)
	}
	if finalStatus != 30 || finalReversed != 50 || reason != "refund" {
		t.Fatalf("冲回结果不符: status=%d reversed=%d reason=%q", finalStatus, finalReversed, reason)
	}

	// 订单有佣金时不可删除。
	_, err = db.ExecContext(ctx, "DELETE FROM orders WHERE id = ?", order1)
	requireMySQLError(t, err, 1451, "删除仍有佣金记录的订单")
}

// TestMarketingMigrationFilesArePaired 覆盖本批次迁移文件完整性：5 个版本均成对存在且严格递增。
func TestMarketingMigrationFilesArePaired(t *testing.T) {
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
		couponBatchMigrationVersion, groupBuyMigrationVersion, bargainMigrationVersion,
		memberMigrationVersion, distributionMigrationVersion,
	} {
		if !ups[v] {
			t.Fatalf("缺少 up 迁移: %d", v)
		}
		if !downs[v] {
			t.Fatalf("缺少 down 迁移: %d", v)
		}
		if v <= previousBatchMaxVersion {
			t.Fatalf("迁移版本 %d 必须大于上一批次最高版本 %d", v, previousBatchMaxVersion)
		}
	}
	if !(couponBatchMigrationVersion < groupBuyMigrationVersion &&
		groupBuyMigrationVersion < bargainMigrationVersion &&
		bargainMigrationVersion < memberMigrationVersion &&
		memberMigrationVersion < distributionMigrationVersion) {
		t.Fatalf("本批次迁移版本必须递增")
	}
}
