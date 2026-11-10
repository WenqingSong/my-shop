package migrations

// B1-03-01 商品规格 / 用户资料 / 地区迁移测试：
//   - 三个新增迁移可 up → down → up，回滚只影响新增表/列，既有 SKU 行不丢失；
//   - 历史 SKU（无 sku_spec_values、spec_signature=NULL）仍可被购物车/订单/库存/秒杀引用；
//   - 同一商品同一规格组合（spec_signature）不能重复创建 SKU，NULL 指纹允许多行；
//   - 不同规格组合可关联到不同 SKU，且一个 SKU 在同一规格组下只能取一个值；
//   - 规格值必须属于声明的规格组（复合外键），被引用的规格值不可删除（RESTRICT）；
//   - users.phone 唯一且允许多个 NULL；regions 支持显式 ID 导入与父子层级查询。
//
// 这些断言需要真实 MySQL（与其它迁移测试同一环境假设，DATABASE_DEFAULT_PASS 由测试入口注入），
// 不使用 Mock 冒充迁移验证。

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// 本批次新增的迁移版本（与 sql/ 下的文件名保持一致）。
const (
	productSpecMigrationVersion = uint(20261110000001)
	userProfileMigrationVersion = uint(20261110000002)
	regionsMigrationVersion     = uint(20261110000003)
)

// 迁移新增/回滚涉及的表与列。
var (
	b103AddedTables = []string{"product_specs", "product_spec_values", "sku_spec_values", "regions"}
	b103SkuColumns  = []string{"image_url", "spec_signature"}
	b103UserColumns = []string{"nickname", "avatar_url", "phone"}
)

// migrateSteps 执行指定步数的迁移（负数表示回滚），用于验证 down 文件。
func migrateSteps(t *testing.T, steps int) {
	t.Helper()
	m, err := newMigrate(context.Background())
	if err != nil {
		t.Fatalf("new migrate: %v", err)
	}
	defer closeMigrate(context.Background(), m)
	if err := m.Steps(steps); err != nil {
		t.Fatalf("migration steps %d: %v", steps, err)
	}
}

// requireMySQLError 断言错误为指定 MySQL 错误码（1062 唯一冲突 / 1451 父行被引用 / 1452 子行外键失败）。
func requireMySQLError(t *testing.T, err error, code uint16, scene string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s：期望 MySQL 错误 %d，实际成功", scene, code)
	}
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		t.Fatalf("%s：期望 MySQL 错误 %d，实际 %v", scene, code, err)
	}
	if mysqlErr.Number != code {
		t.Fatalf("%s：期望 MySQL 错误 %d，实际 %d（%v）", scene, code, mysqlErr.Number, err)
	}
}

// insertCategory 写入一个可用分类并返回 id（products 的分类外键依赖）。
func insertCategory(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), "INSERT INTO categories (parent_id, name) VALUES (0, ?)", name)
	if err != nil {
		t.Fatalf("insert category: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("category last insert id: %v", err)
	}
	return id
}

// insertProduct 写入一个商品并返回 id（skus 的商品外键依赖）。
func insertProduct(t *testing.T, db *sql.DB, categoryID int64, name string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		"INSERT INTO products (name, category_id, price) VALUES (?, ?, 100)", name, categoryID)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("product last insert id: %v", err)
	}
	return id
}

// insertSku 写入一个 SKU 并返回 id；signature 为 nil 表示历史/无规格 SKU。
func insertSku(t *testing.T, db *sql.DB, productID int64, name string, price int64, signature *string, imageURL string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		"INSERT INTO skus (product_id, name, price, status, image_url, spec_signature) VALUES (?, ?, ?, 1, ?, ?)",
		productID, name, price, imageURL, signature)
	if err != nil {
		t.Fatalf("insert sku %s: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("sku last insert id: %v", err)
	}
	return id
}

// insertSpec 写入一个规格组并返回 id。
func insertSpec(t *testing.T, db *sql.DB, productID int64, name string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		"INSERT INTO product_specs (product_id, name) VALUES (?, ?)", productID, name)
	if err != nil {
		t.Fatalf("insert spec %s: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("spec last insert id: %v", err)
	}
	return id
}

// insertSpecValue 写入一个规格值并返回 id。
func insertSpecValue(t *testing.T, db *sql.DB, specID int64, value string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		"INSERT INTO product_spec_values (spec_id, value) VALUES (?, ?)", specID, value)
	if err != nil {
		t.Fatalf("insert spec value %s: %v", value, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("spec value last insert id: %v", err)
	}
	return id
}

// sig 构造测试用规格指纹（生产规则为 32 位小写 hex MD5，见 docs/design/sku.md）。
func sig(ch string) *string {
	s := strings.Repeat(ch, 32)
	return &s
}

// TestProductModelMigrationUpDownUp 覆盖「Migration Up / Down / Up」：
// 回滚到本批次之前后，新增表与列全部消失、既有 SKU 行保留；再次 up 后结构恢复且默认值正确。
func TestProductModelMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Fatalf("version=%d want %d", v, latestMigrationVersion)
	}

	categoryID := insertCategory(t, db, "测试分类")
	productID := insertProduct(t, db, categoryID, "测试商品")
	skuID := insertSku(t, db, productID, "历史规格名", 1999, nil, "")

	// down：依次回滚 regions / user_profile / product_spec。
	migrateSteps(t, -3)
	if v := currentVersion(t, db); v != 20261001000024 {
		t.Fatalf("down 后 version=%d want 20261001000024", v)
	}
	for _, table := range b103AddedTables {
		if tableExists(t, db, table) {
			t.Fatalf("down 后不应存在表 %s", table)
		}
	}
	skuSpec := readActualTable(t, db, "skus")
	for _, col := range b103SkuColumns {
		if hasColumn(skuSpec, col) {
			t.Fatalf("down 后 skus 不应存在列 %s", col)
		}
	}
	if _, ok := findIndex(skuSpec, "uk_product_signature"); ok {
		t.Fatalf("down 后不应存在索引 uk_product_signature")
	}
	userSpec := readActualTable(t, db, "users")
	for _, col := range b103UserColumns {
		if hasColumn(userSpec, col) {
			t.Fatalf("down 后 users 不应存在列 %s", col)
		}
	}

	// 既有 SKU 行不因回滚丢失或改变（历史数据兼容底线）。
	var name string
	var price int64
	if err := db.QueryRowContext(ctx, "SELECT name, price FROM skus WHERE id = ?", skuID).Scan(&name, &price); err != nil {
		t.Fatalf("down 后读取历史 SKU: %v", err)
	}
	if name != "历史规格名" || price != 1999 {
		t.Fatalf("down 后历史 SKU 被修改: name=%q price=%d", name, price)
	}

	// up：结构恢复，历史 SKU 取新增列默认值（image_url=''、spec_signature=NULL）。
	if err := Up(ctx); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Fatalf("重新 up 后 version=%d want %d", v, latestMigrationVersion)
	}
	for _, table := range b103AddedTables {
		if !tableExists(t, db, table) {
			t.Fatalf("重新 up 后应存在表 %s", table)
		}
	}
	skuSpec = readActualTable(t, db, "skus")
	for _, col := range b103SkuColumns {
		if !hasColumn(skuSpec, col) {
			t.Fatalf("重新 up 后 skus 应存在列 %s", col)
		}
	}
	if _, ok := findIndex(skuSpec, "uk_product_signature"); !ok {
		t.Fatalf("重新 up 后应存在索引 uk_product_signature")
	}

	var imageURL string
	var signature sql.NullString
	if err := db.QueryRowContext(ctx,
		"SELECT image_url, spec_signature FROM skus WHERE id = ?", skuID).Scan(&imageURL, &signature); err != nil {
		t.Fatalf("重新 up 后读取历史 SKU: %v", err)
	}
	if imageURL != "" {
		t.Fatalf("历史 SKU image_url 应为默认空串，实际 %q", imageURL)
	}
	if signature.Valid {
		t.Fatalf("历史 SKU spec_signature 应为 NULL，实际 %q", signature.String)
	}
}

// TestLegacySkuWithoutSpecRemainsUsable 覆盖「旧 SKU 无规格关联时仍可用」：
// 不写 sku_spec_values 的历史 SKU，仍能被购物车、普通订单、库存与秒杀引用。
func TestLegacySkuWithoutSpecRemainsUsable(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	categoryID := insertCategory(t, db, "历史分类")
	productID := insertProduct(t, db, categoryID, "历史商品")
	skuID := insertSku(t, db, productID, "默认", 2999, nil, "")

	// 无规格关联是合法状态，不是缺失数据。
	var linked int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sku_spec_values WHERE sku_id = ?", skuID).Scan(&linked); err != nil {
		t.Fatalf("count sku_spec_values: %v", err)
	}
	if linked != 0 {
		t.Fatalf("历史 SKU 不应有规格关联，实际 %d 行", linked)
	}

	// 购物车（软引用）与库存（FK → skus）可继续写入。
	if _, err := db.ExecContext(ctx,
		"INSERT INTO cart_items (user_id, sku_id, quantity, price_snapshot) VALUES (1, ?, 2, 2999)", skuID); err != nil {
		t.Fatalf("历史 SKU 加入购物车: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO inventories (sku_id, quantity) VALUES (?, 10)", skuID); err != nil {
		t.Fatalf("历史 SKU 建库存: %v", err)
	}

	// 普通订单与秒杀活动 SKU 绑定（均为软引用，但必须仍能按 sku_id 写入）。
	orderRes, err := db.ExecContext(ctx, `INSERT INTO orders
		(order_no, user_id, total_amount, idempotency_key, request_hash, recipient_name, phone, province, city, district, detail, expire_at)
		VALUES ('B1030001', 1, 2999, 'b103-idem', ?, '张三', '13800000000', '省', '市', '区', '地址', NOW())`,
		strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	orderID, err := orderRes.LastInsertId()
	if err != nil {
		t.Fatalf("order last insert id: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO order_items
		(order_id, sku_id, product_id, sku_name, product_name, price, quantity)
		VALUES (?, ?, ?, '默认', '历史商品', 2999, 1)`, orderID, skuID, productID); err != nil {
		t.Fatalf("insert order item: %v", err)
	}

	activityRes, err := db.ExecContext(ctx, `INSERT INTO flash_sale_activities (name, start_time, end_time)
		VALUES ('历史秒杀', NOW(), DATE_ADD(NOW(), INTERVAL 1 HOUR))`)
	if err != nil {
		t.Fatalf("insert flash sale activity: %v", err)
	}
	activityID, err := activityRes.LastInsertId()
	if err != nil {
		t.Fatalf("activity last insert id: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO flash_sale_activity_skus
		(activity_id, sku_id, flash_price, total_stock) VALUES (?, ?, 1999, 5)`, activityID, skuID); err != nil {
		t.Fatalf("bind legacy sku to flash sale: %v", err)
	}

	for _, q := range []string{
		"SELECT COUNT(*) FROM cart_items WHERE sku_id = ?",
		"SELECT COUNT(*) FROM inventories WHERE sku_id = ?",
		"SELECT COUNT(*) FROM order_items WHERE sku_id = ?",
		"SELECT COUNT(*) FROM flash_sale_activity_skus WHERE sku_id = ?",
	} {
		var n int
		if err := db.QueryRowContext(ctx, q, skuID).Scan(&n); err != nil {
			t.Fatalf("检查历史 SKU 引用（%s）: %v", q, err)
		}
		if n != 1 {
			t.Fatalf("历史 SKU 引用行数=%d，期望 1（%s）", n, q)
		}
	}
}

// TestSkuSpecCombinationUniqueness 覆盖「相同商品 + 相同规格组合不能重复建 SKU」：
// uk_product_signature 生效；NULL 指纹（历史/无规格）可多行；不同商品间指纹互不影响。
func TestSkuSpecCombinationUniqueness(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	categoryID := insertCategory(t, db, "规格分类")
	productID := insertProduct(t, db, categoryID, "多规格商品")
	specID := insertSpec(t, db, productID, "颜色")
	blackID := insertSpecValue(t, db, specID, "黑色")
	whiteID := insertSpecValue(t, db, specID, "白色")

	blackSku := insertSku(t, db, productID, "黑色", 1000, sig("a"), "")
	if _, err := db.ExecContext(ctx,
		"INSERT INTO sku_spec_values (sku_id, spec_id, spec_value_id) VALUES (?, ?, ?)", blackSku, specID, blackID); err != nil {
		t.Fatalf("关联黑色规格值: %v", err)
	}

	// 同商品 + 相同指纹 → 唯一约束拒绝。
	_, err := db.ExecContext(ctx,
		"INSERT INTO skus (product_id, name, price, status, spec_signature) VALUES (?, '黑色副本', 1000, 1, ?)",
		productID, sig("a"))
	requireMySQLError(t, err, 1062, "同商品同规格组合重复建 SKU")

	// NULL 指纹允许多行（历史/无规格 SKU）。
	insertSku(t, db, productID, "无规格A", 1000, nil, "")
	insertSku(t, db, productID, "无规格B", 1000, nil, "")

	// 不同商品可以使用相同指纹（指纹作用域为单商品）。
	otherCategory := insertCategory(t, db, "规格分类2")
	otherProduct := insertProduct(t, db, otherCategory, "另一商品")
	insertSku(t, db, otherProduct, "黑色", 1000, sig("a"), "")

	// 不同规格组合 → 不同 SKU，关联关系可正确反查。
	whiteSku := insertSku(t, db, productID, "白色", 1100, sig("b"), "")
	if _, err := db.ExecContext(ctx,
		"INSERT INTO sku_spec_values (sku_id, spec_id, spec_value_id) VALUES (?, ?, ?)", whiteSku, specID, whiteID); err != nil {
		t.Fatalf("关联白色规格值: %v", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT ssv.sku_id, ssv.spec_value_id
		FROM sku_spec_values ssv JOIN skus s ON s.id = ssv.sku_id
		WHERE s.product_id = ? ORDER BY ssv.sku_id`, productID)
	if err != nil {
		t.Fatalf("查询 SKU 规格关联: %v", err)
	}
	defer rows.Close()
	got := map[int64]int64{}
	for rows.Next() {
		var skuID, valueID int64
		if err := rows.Scan(&skuID, &valueID); err != nil {
			t.Fatalf("scan 规格关联: %v", err)
		}
		got[skuID] = valueID
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate 规格关联: %v", err)
	}
	if len(got) != 2 || got[blackSku] != blackID || got[whiteSku] != whiteID {
		t.Fatalf("不同规格组合未正确关联不同 SKU: %v", got)
	}
}

// TestSkuSpecValueIntegrity 覆盖关联约束：
// 同一 SKU 在同一规格组下只能取一个值；规格值必须属于声明的规格组；被引用值不可删除；删除 SKU 级联清理关联。
func TestSkuSpecValueIntegrity(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	categoryID := insertCategory(t, db, "约束分类")
	productID := insertProduct(t, db, categoryID, "约束商品")
	colorSpec := insertSpec(t, db, productID, "颜色")
	blackID := insertSpecValue(t, db, colorSpec, "黑色")
	whiteID := insertSpecValue(t, db, colorSpec, "白色")
	sizeSpec := insertSpec(t, db, productID, "尺码")
	size39ID := insertSpecValue(t, db, sizeSpec, "39")

	skuID := insertSku(t, db, productID, "黑色", 1000, sig("c"), "")
	if _, err := db.ExecContext(ctx,
		"INSERT INTO sku_spec_values (sku_id, spec_id, spec_value_id) VALUES (?, ?, ?)", skuID, colorSpec, blackID); err != nil {
		t.Fatalf("关联黑色: %v", err)
	}

	// 同一 SKU、同一规格组再取另一个值 → 主键冲突。
	_, err := db.ExecContext(ctx,
		"INSERT INTO sku_spec_values (sku_id, spec_id, spec_value_id) VALUES (?, ?, ?)", skuID, colorSpec, whiteID)
	requireMySQLError(t, err, 1062, "同一 SKU 同规格组取两个值")

	// 规格值不属于声明的规格组 → 复合外键拒绝（1452）。
	_, err = db.ExecContext(ctx,
		"INSERT INTO sku_spec_values (sku_id, spec_id, spec_value_id) VALUES (?, ?, ?)", skuID, sizeSpec, blackID)
	requireMySQLError(t, err, 1452, "规格值与规格组不匹配")

	// 被 SKU 引用的规格值不可删除（RESTRICT）。
	_, err = db.ExecContext(ctx, "DELETE FROM product_spec_values WHERE id = ?", blackID)
	requireMySQLError(t, err, 1451, "删除被引用的规格值")

	// 未使用的规格值可以正常删除。
	if _, err := db.ExecContext(ctx, "DELETE FROM product_spec_values WHERE id = ?", size39ID); err != nil {
		t.Fatalf("删除未使用规格值: %v", err)
	}

	// 删除 SKU 级联清理其规格关联（不影响已验证的规格组与规格值）。
	if _, err := db.ExecContext(ctx, "DELETE FROM skus WHERE id = ?", skuID); err != nil {
		t.Fatalf("删除 SKU: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sku_spec_values WHERE sku_id = ?", skuID).Scan(&n); err != nil {
		t.Fatalf("统计 SKU 关联: %v", err)
	}
	if n != 0 {
		t.Fatalf("删除 SKU 后关联应为 0，实际 %d", n)
	}
}

// TestUserPhoneUniqueAndNullable 覆盖「手机号允许未设置，但已设置的有效手机号唯一」。
func TestUserPhoneUniqueAndNullable(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	hash := strings.Repeat("h", 60)
	res, err := db.ExecContext(ctx, "INSERT INTO users (username, password_hash) VALUES ('u1', ?), ('u2', ?)", hash, hash)
	if err != nil {
		t.Fatalf("insert users: %v", err)
	}
	firstID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("users last insert id: %v", err)
	}
	secondID := firstID + 1

	// 新增列默认值：昵称/头像空串、手机号 NULL，既有认证列不变。
	var nickname, avatar string
	var phone sql.NullString
	var status int
	var authEpoch int64
	if err := db.QueryRowContext(ctx,
		"SELECT nickname, avatar_url, phone, status, auth_epoch FROM users WHERE id = ?", firstID).
		Scan(&nickname, &avatar, &phone, &status, &authEpoch); err != nil {
		t.Fatalf("读取用户资料默认值: %v", err)
	}
	if nickname != "" || avatar != "" || phone.Valid || status != 1 || authEpoch != 0 {
		t.Fatalf("用户默认值不符: nickname=%q avatar=%q phone=%v status=%d auth_epoch=%d",
			nickname, avatar, phone, status, authEpoch)
	}

	// 多个 NULL 手机号允许共存（已由上面两行证明）。
	if _, err := db.ExecContext(ctx, "UPDATE users SET phone = '13800000001' WHERE id = ?", firstID); err != nil {
		t.Fatalf("设置手机号: %v", err)
	}
	_, err = db.ExecContext(ctx, "UPDATE users SET phone = '13800000001' WHERE id = ?", secondID)
	requireMySQLError(t, err, 1062, "手机号重复")

	// 改回未设置（NULL）后可正常保存。
	if _, err := db.ExecContext(ctx, "UPDATE users SET phone = NULL WHERE id = ?", secondID); err != nil {
		t.Fatalf("清空手机号: %v", err)
	}
}

// TestRegionsHierarchy 覆盖地区三级结构与「显式 ID 导入」兼容：
// 允许按 system_city.city_id 指定主键，父子关系可按 parent_id 查询。
func TestRegionsHierarchy(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	const (
		provinceID = int64(110000)
		cityID     = int64(110100)
		districtID = int64(110101)
	)
	if _, err := db.ExecContext(ctx, `INSERT INTO regions (id, parent_id, level, name) VALUES
		(?, 0, 1, '北京市'), (?, ?, 2, '北京市'), (?, ?, 3, '东城区')`,
		provinceID, cityID, provinceID, districtID, cityID); err != nil {
		t.Fatalf("显式 ID 导入地区: %v", err)
	}

	var level int
	var parentID int64
	if err := db.QueryRowContext(ctx,
		"SELECT level, parent_id FROM regions WHERE id = ?", districtID).Scan(&level, &parentID); err != nil {
		t.Fatalf("读取区级地区: %v", err)
	}
	if level != 3 || parentID != cityID {
		t.Fatalf("区级父子关系不符: level=%d parent_id=%d", level, parentID)
	}

	rows, err := db.QueryContext(ctx,
		"SELECT id, level FROM regions WHERE parent_id = ? ORDER BY id", provinceID)
	if err != nil {
		t.Fatalf("查询下级地区: %v", err)
	}
	defer rows.Close()
	var children []int64
	for rows.Next() {
		var childID int64
		var childLevel int
		if err := rows.Scan(&childID, &childLevel); err != nil {
			t.Fatalf("scan 下级地区: %v", err)
		}
		if childLevel != 2 {
			t.Fatalf("下级地区 level=%d，期望 2", childLevel)
		}
		children = append(children, childID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate 下级地区: %v", err)
	}
	if len(children) != 1 || children[0] != cityID {
		t.Fatalf("省级下级地区=%v，期望 [%d]", children, cityID)
	}

	// 未指定 parent_id 时默认 0（省级约定），且主键唯一。
	res, err := db.ExecContext(ctx, "INSERT INTO regions (level, name) VALUES (1, '默认父级省')")
	if err != nil {
		t.Fatalf("insert 默认父级地区: %v", err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("regions last insert id: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT parent_id FROM regions WHERE id = ?", newID).Scan(&parentID); err != nil {
		t.Fatalf("读取默认 parent_id: %v", err)
	}
	if parentID != 0 {
		t.Fatalf("默认 parent_id=%d，期望 0", parentID)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO regions (id, level, name) VALUES (?, 1, '重复主键')", newID)
	requireMySQLError(t, err, 1062, "地区主键重复")
}

// TestB103MigrationFilesArePaired 覆盖迁移文件完整性：
// 本批次三个版本必须同时存在 up/down，版本号唯一且严格大于上一批次最高版本。
func TestB103MigrationFilesArePaired(t *testing.T) {
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

	for _, v := range []uint{productSpecMigrationVersion, userProfileMigrationVersion, regionsMigrationVersion} {
		if !ups[v] {
			t.Fatalf("缺少 up 迁移: %d", v)
		}
		if !downs[v] {
			t.Fatalf("缺少 down 迁移: %d", v)
		}
		if v <= 20261001000024 {
			t.Fatalf("迁移版本 %d 必须大于上一批次最高版本 20261001000024", v)
		}
	}

	// up 版本号唯一（同一版本不能有两个 up 文件），且本批次顺序递增。
	if !(productSpecMigrationVersion < userProfileMigrationVersion &&
		userProfileMigrationVersion < regionsMigrationVersion) {
		t.Fatalf("本批次迁移版本必须递增")
	}
}
