package legacymigrate

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// 本文件覆盖 B1-03-04 历史数据迁移的核心不变量：
// 商品规格/指纹、软删除但仍被引用的商品、用户密码强制重置、
// 订单状态流水重建、金额元→分、优惠券批次/快照、钱包/积分期初不重复入账、
// 评价/收藏关联与二次执行幂等。

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func TestMigrateProductSpecsAndReferencedDeleted(t *testing.T) {
	source, target := setupEngineTest(t)
	ctx := context.Background()

	mustExec(t, source, "INSERT INTO store_category (id, pid, cate_name, sort, is_show, is_del) VALUES (1, 0, '数码', 0, 1, 0)")
	mustExec(t, source, `INSERT INTO store_product
		(id, cate_id, price, stock, is_show, is_del, spec_type, image, slider_image, store_name, description)
		VALUES (1, '1', 100.00, 999, 1, 0, 1, '/p1.jpg', '/s1.jpg,/s2.jpg', 'P1', 'd1')`)
	// 软删除商品 2：仅因被收藏引用而保留（收藏用户不存在，收藏本身会跳过）。
	mustExec(t, source, `INSERT INTO store_product
		(id, cate_id, price, stock, is_show, is_del, spec_type, image, slider_image, store_name, description)
		VALUES (2, '1', 2.00, 0, 1, 1, 0, '/p2.jpg', '', 'P2-deleted', 'd2')`)

	attrResult := `{"attr":[{"value":"颜色","detail":["黑色","白色"]},{"value":"容量","detail":["64G","128G"]}],` +
		`"value":[{"detail":{"颜色":"黑色","容量":"64G"}},{"detail":{"颜色":"白色","容量":"128G"}}]}`
	mustExec(t, source, "INSERT INTO store_product_attr_result (product_id, result) VALUES (1, ?)", attrResult)
	mustExec(t, source, "INSERT INTO store_product_attr (id, product_id, attr_name, attr_values) VALUES (10, 1, '颜色', '黑色,白色'), (11, 1, '容量', '64G,128G')")
	mustExec(t, source, `INSERT INTO store_product_attr_value (id, product_id, sku, stock, price, image, `+"`unique`"+`)
		VALUES (101, 1, '64G,黑色', 5, 90.00, '/sku1.jpg', 'u1'), (102, 1, '128G,白色', 7, 110.00, '', 'u2'),
		       (201, 2, '默认', 0, 2.00, '', 'u3')`)
	mustExec(t, source, "INSERT INTO store_product_relation (id, uid, product_id, type, create_time, is_del) VALUES (1, 99, 2, 'collect', NOW(), 0)")

	result, err := NewEngine(source, target).Run(ctx, false)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.domain("products").Migrated != 2 || result.domain("skus").Migrated != 3 || result.domain("inventories").Migrated != 3 {
		t.Fatalf("商品统计异常: %s", result.String())
	}

	var p1ID int64
	if err := target.QueryRowContext(ctx, "SELECT id FROM products WHERE name = 'P1'").Scan(&p1ID); err != nil {
		t.Fatalf("query P1: %v", err)
	}
	var p2Status int
	if err := target.QueryRowContext(ctx, "SELECT status FROM products WHERE name = 'P2-deleted'").Scan(&p2Status); err != nil {
		t.Fatalf("query P2: %v", err)
	}
	if p2Status != productStatusOffShelf {
		t.Fatalf("被引用软删除商品状态 = %d, 期望 off_shelf", p2Status)
	}

	var specs, values int
	if err := target.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM product_specs WHERE product_id = ?", p1ID).Scan(&specs); err != nil {
		t.Fatalf("count specs: %v", err)
	}
	if err := target.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM product_spec_values pv JOIN product_specs ps ON ps.id=pv.spec_id WHERE ps.product_id = ?", p1ID).Scan(&values); err != nil {
		t.Fatalf("count spec values: %v", err)
	}
	if specs != 2 || values != 4 {
		t.Fatalf("规格统计 = %d/%d, 期望 2/4", specs, values)
	}

	rows, err := target.QueryContext(ctx,
		`SELECT s.id, s.name, s.spec_signature, s.image_url, s.status,
		        GROUP_CONCAT(CONCAT(sv.spec_id, ':', sv.spec_value_id) ORDER BY sv.spec_id) AS sig_src
		 FROM skus s LEFT JOIN sku_spec_values sv ON sv.sku_id = s.id
		 WHERE s.product_id = ? GROUP BY s.id ORDER BY s.name`, p1ID)
	if err != nil {
		t.Fatalf("query skus: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var id int64
		var name string
		var sig, image, sigSrc sql.NullString
		var status int
		if err := rows.Scan(&id, &name, &sig, &image, &status, &sigSrc); err != nil {
			t.Fatalf("scan sku: %v", err)
		}
		seen++
		if !sig.Valid || sig.String == "" {
			t.Fatalf("SKU %q 应有 spec_signature", name)
		}
		sum := md5.Sum([]byte(sigSrc.String))
		if sig.String != hex.EncodeToString(sum[:]) {
			t.Fatalf("SKU %q 指纹 %q 与 sku_spec_values %q 不匹配", name, sig.String, sigSrc.String)
		}
		if status != skuStatusEnabled {
			t.Fatalf("在售商品 SKU %q 应为 enabled", name)
		}
		if name == "64G,黑色" && image.String != "/sku1.jpg" {
			t.Fatalf("SKU 图未迁移: %q", image.String)
		}
	}
	if seen != 2 {
		t.Fatalf("P1 SKU 数 = %d, 期望 2", seen)
	}

	var p2Sig sql.NullString
	if err := target.QueryRowContext(ctx,
		"SELECT spec_signature FROM skus WHERE name='默认'").Scan(&p2Sig); err != nil {
		t.Fatalf("query P2 sku: %v", err)
	}
	if p2Sig.Valid {
		t.Fatalf("无规格商品 SKU 的 spec_signature 应为 NULL, 实际 %q", p2Sig.String)
	}

	// 二次执行：0 新增、行数不变。
	second, err := NewEngine(source, target).Run(ctx, false)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.ProductsMigrated != 0 || second.SkusMigrated != 0 {
		t.Fatalf("二次执行应 0 新增: %s", second.String())
	}
	for table, want := range map[string]int{"products": 2, "skus": 3, "inventories": 3, "product_specs": 2, "sku_spec_values": 4} {
		if got := countRows(t, target, table); got != want {
			t.Fatalf("二次执行后 %s = %d, 期望 %d", table, got, want)
		}
	}
}

func TestMigrateUsersAddressesWalletPoints(t *testing.T) {
	source, target := setupEngineTest(t)
	ctx := context.Background()

	md5Password := "e10adc3949ba59abbe56e057f20f883e" // md5("123456") 仅用于验证不会被迁移
	mustExec(t, source, `INSERT INTO eshop_user
		(uid, username, password, nickname, avatar, phone, now_money, integral, status, is_del, create_time)
		VALUES (1, 'user1', ?, '昵称1', '/a.png', '13800000000', 12.34, 10.00, 1, 0, '2021-01-02 03:04:05'),
		       (2, 'user2', ?, '昵称2', '', '13900000000', 0.00, 0.00, 1, 0, '2021-01-03 03:04:05')`,
		md5Password, md5Password)
	mustExec(t, source, `INSERT INTO user_address
		(id, uid, real_name, phone, province, city, district, detail, city_id, is_default, is_del, create_time)
		VALUES (1, 1, '张三', '13800000000', '北京市', '北京市', '东城区', 'A', 2, 1, 0, NOW()),
		       (2, 1, '张三2', '13800000000', '北京市', '北京市', '西城区', 'B', 2, 1, 0, NOW()),
		       (3, 99, '孤儿', '13700000000', '北京市', '北京市', '东城区', 'C', 2, 0, 0, NOW()),
		       (4, 1, '已删', '13600000000', '北京市', '北京市', '东城区', 'D', 2, 0, 1, NOW())`)

	result, err := NewEngine(source, target).Run(ctx, false)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.domain("users").Migrated != 2 || result.domain("addresses").Migrated != 2 {
		t.Fatalf("用户/地址统计异常: %s", result.String())
	}

	var hash string
	if err := target.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE username='user1'").Scan(&hash); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if hash != legacyPasswordResetMarker {
		t.Fatalf("历史密码必须替换为强制重置占位值, 实际 %q", hash)
	}
	if len(hash) == 32 {
		t.Fatalf("不得保留 32 位 MD5")
	}

	var addrCount, defaultCount int
	if err := target.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(is_default),0) FROM addresses").Scan(&addrCount, &defaultCount); err != nil {
		t.Fatalf("count addresses: %v", err)
	}
	if addrCount != 2 || defaultCount != 1 {
		t.Fatalf("地址数/默认数 = %d/%d, 期望 2/1", addrCount, defaultCount)
	}

	var balance int64
	if err := target.QueryRowContext(ctx, "SELECT balance FROM wallet_accounts").Scan(&balance); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balance != 1234 {
		t.Fatalf("钱包余额 = %d, 期望 1234", balance)
	}
	var txCount int
	if err := target.QueryRowContext(ctx, "SELECT COUNT(*) FROM wallet_transactions WHERE direction=1 AND amount=1234 AND balance_before=0 AND balance_after=1234").Scan(&txCount); err != nil {
		t.Fatalf("query wallet tx: %v", err)
	}
	if txCount != 1 {
		t.Fatalf("期初钱包流水 = %d, 期望 1", txCount)
	}

	var points int64
	if err := target.QueryRowContext(ctx, "SELECT points FROM user_points_accounts").Scan(&points); err != nil {
		t.Fatalf("query points: %v", err)
	}
	if points != 10 {
		t.Fatalf("积分 = %d, 期望 10", points)
	}

	// 二次执行不得重复入账。
	if _, err := NewEngine(source, target).Run(ctx, false); err != nil {
		t.Fatalf("second import: %v", err)
	}
	if got := countRows(t, target, "wallet_transactions"); got != 1 {
		t.Fatalf("二次执行钱包流水 = %d, 期望 1", got)
	}
	if got := countRows(t, target, "user_points_logs"); got != 1 {
		t.Fatalf("二次执行积分流水 = %d, 期望 1", got)
	}
	if got := countRows(t, target, "users"); got != 2 {
		t.Fatalf("二次执行用户 = %d, 期望 2", got)
	}
}

func TestMigrateOrdersCouponsReviewsFavorites(t *testing.T) {
	source, target := setupEngineTest(t)
	ctx := context.Background()

	mustExec(t, source, "INSERT INTO eshop_user (uid, username, password, nickname, now_money, integral, status, is_del) VALUES (1, 'u1', 'x', 'n', 0, 0, 1, 0)")
	mustExec(t, source, "INSERT INTO store_category (id, pid, cate_name, sort, is_show, is_del) VALUES (1, 0, '数码', 0, 1, 0)")
	mustExec(t, source, `INSERT INTO store_product (id, cate_id, price, stock, is_show, is_del, spec_type, image, store_name)
		VALUES (1, '1', 50.00, 10, 1, 0, 0, '/p.jpg', 'P1')`)
	mustExec(t, source, `INSERT INTO store_product_attr_value (id, product_id, sku, stock, price, image, `+"`unique`"+`)
		VALUES (11, 1, '默认', 10, 50.00, '', 'u1')`)

	cartInfo := `{"cartNum":2,"truePrice":50.00,"productInfo":{"storeName":"P1","image":"/p.jpg","attrInfo":{"unique":"u1","sku":"默认","price":50.00}}}`
	mustExec(t, source, `INSERT INTO store_order
		(id, order_id, uid, real_name, user_phone, user_address, total_price, total_postage, pay_price,
		 deduction_price, coupon_id, coupon_price, paid, pay_time, pay_type, create_time, status, refund_status, is_del)
		VALUES (1, 'O1', 1, '张三', '13800000000', '北京市 东城区 北京市 123', 100.00, 0.00, 99.00,
		        0.00, 9, 1.00, 1, '2021-08-17 09:00:00', 'yue', '2021-08-17 09:00:00', 3, 0, 1),
		       (2, 'O2', 1, '张三', '13800000000', '北京市 东城区 北京市 123', 10.00, 0.00, 10.00,
		        0.00, 0, 0.00, 0, NULL, 'yue', '2021-08-18 09:00:00', 0, 0, 0)`)
	mustExec(t, source, "INSERT INTO store_order_cart_info (id, oid, product_id, cart_info, `unique`) VALUES (21, 1, 1, ?, 'cart-u1')", cartInfo)
	for _, row := range []struct {
		typ  string
		msg  string
		time string
	}{
		{"yshop_create_order", "订单生成", "2021-08-17 09:00:00"},
		{"pay_success", "用户付款成功", "2021-08-17 09:01:00"},
		{"delivery_goods", "已发货", "2021-08-17 10:00:00"},
		{"user_take_delivery", "用户已收货", "2021-08-17 11:00:00"},
		{"order_eval", "用户评价", "2021-08-17 12:00:00"},
		{"remove_order", "删除订单", "2021-08-17 13:00:00"},
	} {
		mustExec(t, source, "INSERT INTO store_order_status (oid, change_type, change_message, change_time) VALUES (1, ?, ?, ?)", row.typ, row.msg, row.time)
	}
	mustExec(t, source, "INSERT INTO store_order_status (oid, change_type, change_message, change_time) VALUES (2, 'yshop_create_order', '订单生成', '2021-08-18 09:00:00')")

	mustExec(t, source, `INSERT INTO store_coupon (id, title, coupon_price, use_min_price, coupon_time, status, product_id, type, create_time, is_del)
		VALUES (6, '全场通用', 1.00, 10.00, 100, 1, '1', 1, '2021-06-22 17:57:02', 0)`)
	mustExec(t, source, `INSERT INTO store_coupon_issue (id, cname, cid, ctype, start_time, end_time, total_count, remain_count, is_permanent, status, is_del, create_time)
		VALUES (7, '全场通用', 6, 1, '2021-06-21 18:07:25', '2021-07-02 00:00:00', 100, 99, 0, 1, 0, '2021-06-21 18:07:25')`)
	mustExec(t, source, "INSERT INTO store_coupon_issue_user (id, uid, issue_coupon_id, create_time, is_del) VALUES (1, 1, 7, '2021-07-14 09:22:42', 0)")
	mustExec(t, source, `INSERT INTO store_coupon_user
		(id, cid, uid, coupon_title, coupon_price, use_min_price, create_time, end_time, use_time, type, status, is_fail, is_del)
		VALUES (9, 6, 1, '全场通用', 1.00, 10.00, '2021-07-14 09:22:42', '2021-10-22 09:22:42', '2021-08-17 09:00:00', 'get', 1, 0, 0)`)

	mustExec(t, source, `INSERT INTO store_product_reply (id, uid, oid, `+"`unique`"+`, product_id, reply_type, product_score, comment, pics, create_time, is_del)
		VALUES (1, 1, 1, 'r1', 1, 'product', 5, '很好', '/a.jpg,/b.jpg', '2021-08-17 12:00:00', 0)`)
	mustExec(t, source, "INSERT INTO store_product_relation (id, uid, product_id, type, create_time, is_del) VALUES (1, 1, 1, 'collect', NOW(), 0), (2, 1, 1, 'foot', NOW(), 0), (3, 1, 1, 'collect', NOW(), 1)")

	result, err := NewEngine(source, target).Run(ctx, false)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.domain("orders").Migrated != 2 || result.domain("order_items").Migrated != 1 || result.domain("order_status_logs").Migrated != 7 {
		t.Fatalf("订单统计异常: %s", result.String())
	}

	var status int
	var original, discount, total int64
	var payType sql.NullString
	var userCouponID sql.NullInt64
	var paidAt, shippedAt, receivedAt, completedAt sql.NullTime
	if err := target.QueryRowContext(ctx,
		`SELECT status, original_amount, discount_amount, total_amount, pay_type, user_coupon_id,
		        paid_at, shipped_at, received_at, completed_at FROM orders WHERE order_no='O1'`).
		Scan(&status, &original, &discount, &total, &payType, &userCouponID, &paidAt, &shippedAt, &receivedAt, &completedAt); err != nil {
		t.Fatalf("query order: %v", err)
	}
	if status != statusCompleted {
		t.Fatalf("remove_order 不应把已完成订单改成取消, status=%d", status)
	}
	if original != 10000 || discount != 100 || total != 9900 {
		t.Fatalf("金额分转换 = %d/%d/%d, 期望 10000/100/9900", original, discount, total)
	}
	if payType.String != "balance" {
		t.Fatalf("pay_type = %q, 期望 balance", payType.String)
	}
	if !userCouponID.Valid {
		t.Fatalf("订单应关联用户券")
	}
	if !paidAt.Valid || !shippedAt.Valid || !receivedAt.Valid || !completedAt.Valid {
		t.Fatalf("履约时间点未迁移: %+v", []sql.NullTime{paidAt, shippedAt, receivedAt, completedAt})
	}

	var pendingPayType sql.NullString
	if err := target.QueryRowContext(ctx, "SELECT pay_type FROM orders WHERE order_no='O2'").Scan(&pendingPayType); err != nil {
		t.Fatalf("query O2: %v", err)
	}
	if pendingPayType.Valid && pendingPayType.String != "" {
		t.Fatalf("待支付订单 pay_type 应为空, 实际 %q", pendingPayType.String)
	}

	var deleteLogs int
	if err := target.QueryRowContext(ctx, "SELECT COUNT(*) FROM order_status_logs WHERE change_type='delete'").Scan(&deleteLogs); err != nil {
		t.Fatalf("count delete logs: %v", err)
	}
	if deleteLogs != 1 {
		t.Fatalf("remove_order 应保留 1 条 delete 审计流水, 实际 %d", deleteLogs)
	}

	var batchID sql.NullInt64
	var claimNo int
	var couponStatus int
	if err := target.QueryRowContext(ctx, "SELECT batch_id, claim_no, status FROM user_coupons").Scan(&batchID, &claimNo, &couponStatus); err != nil {
		t.Fatalf("query user coupon: %v", err)
	}
	if !batchID.Valid || claimNo != 1 || couponStatus != 30 {
		t.Fatalf("用户券批次/领取序号/状态 = %v/%d/%d", batchID, claimNo, couponStatus)
	}

	var linkedOrders int
	if err := target.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_coupons WHERE order_id IS NOT NULL").Scan(&linkedOrders); err != nil {
		t.Fatalf("count linked coupons: %v", err)
	}
	if linkedOrders != 1 {
		t.Fatalf("已使用券应回填订单关联, 实际 %d", linkedOrders)
	}

	var picsJSON string
	if err := target.QueryRowContext(ctx, "SELECT image_urls FROM reviews").Scan(&picsJSON); err != nil {
		t.Fatalf("query review: %v", err)
	}
	var pics []string
	if err := json.Unmarshal([]byte(picsJSON), &pics); err != nil || len(pics) != 2 {
		t.Fatalf("评价图片 JSON 异常: %q err=%v", picsJSON, err)
	}
	if got := countRows(t, target, "favorites"); got != 1 {
		t.Fatalf("收藏 = %d, 期望仅 1 条 collect+未删除", got)
	}

	// 二次执行幂等。
	if _, err := NewEngine(source, target).Run(ctx, false); err != nil {
		t.Fatalf("second import: %v", err)
	}
	for table, want := range map[string]int{"orders": 2, "order_items": 1, "order_status_logs": 7, "user_coupons": 1, "reviews": 1, "favorites": 1} {
		if got := countRows(t, target, table); got != want {
			t.Fatalf("二次执行后 %s = %d, 期望 %d", table, got, want)
		}
	}
}
