package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sync"
	"testing"
	"testing/fstest"
)

// baselineVersion 是内嵌 baseline 迁移的版本号（14 位时间戳）。
const baselineVersion = uint(20261001000001)

// latestMigrationVersion 是当前内嵌迁移的最高版本（baseline + products + skus + inventory + addresses + cart_items + orders）。
const latestMigrationVersion = uint(20261001000007)

// businessTables 是 migration 应建立的 16 张业务表。
// 注意顺序：order_items 通过外键引用 orders（ON DELETE CASCADE），故 order_items 排在 orders 之前；
// cart_items 无外键、置前；addresses 通过外键引用 users（ON DELETE CASCADE），
// 故 addresses 排在 users 之前；inventories/inventory_logs 通过外键引用 skus，skus 通过外键引用
// products，products 通过外键引用 categories（均 ON DELETE RESTRICT），因此被引用方必须排在引用方之后，
// 即 inventories/inventory_logs 排在 skus 之前、skus 排在 products 之前、products 排在 categories 之前，
// 否则 DROP TABLE 会因外键依赖失败。
var businessTables = []string{
	"order_items", "orders", "cart_items", "addresses", "users", "inventory_logs", "inventories", "skus", "products", "product_images", "categories", "admins", "roles", "permissions", "admin_roles", "role_permissions",
}

// allTables 含业务表与追踪表。
var allTables = append(append([]string{}, businessTables...), "schema_migrations")

// openTestDB 打开测试数据库连接（复用 buildDSN，指向与配置一致的 MySQL）。
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", buildDSN(context.Background()))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// dropAllTables 清空本任务涉及的全部表（含测试探针表），模拟空库。失败返回错误（供 cleanup 复用）。
func dropAllTables(db *sql.DB) error {
	for _, table := range append(allTables, "migration_probe") {
		if _, err := db.ExecContext(context.Background(), "DROP TABLE IF EXISTS `"+table+"`"); err != nil {
			return fmt.Errorf("drop %s: %w", table, err)
		}
	}
	return nil
}

// setupCleanDB 清空相关表，并在测试结束后恢复为干净的 baseline 已迁移状态，
// 保证测试之间以及与其他包测试（go test -p 1 串行）互不污染。
func setupCleanDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openTestDB(t)
	if err := dropAllTables(db); err != nil {
		t.Fatalf("drop all tables: %v", err)
	}
	t.Cleanup(func() {
		migrationFS = embeddedMigrations
		if err := dropAllTables(db); err != nil {
			t.Errorf("cleanup drop tables: %v", err)
			return
		}
		if err := Up(context.Background()); err != nil {
			t.Errorf("cleanup restore baseline: %v", err)
		}
	})
	return db
}

// tableExists 判断表是否存在。
func tableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table).Scan(&n)
	if err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	return n > 0
}

// currentVersion 返回 schema_migrations 当前版本（表不存在或为空时返回 0）。
// golang-migrate 的追踪表始终只有一行，记录当前版本。
func currentVersion(t *testing.T, db *sql.DB) uint {
	t.Helper()
	if !tableExists(t, db, "schema_migrations") {
		return 0
	}
	var v uint64
	err := db.QueryRowContext(context.Background(), "SELECT version FROM schema_migrations LIMIT 1").Scan(&v)
	if err == sql.ErrNoRows {
		return 0
	}
	if err != nil {
		t.Fatalf("query version: %v", err)
	}
	return uint(v)
}

// dirtyState 返回 schema_migrations 的 dirty 状态（表不存在或为空时返回 false）。
func dirtyState(t *testing.T, db *sql.DB) bool {
	t.Helper()
	if !tableExists(t, db, "schema_migrations") {
		return false
	}
	var dirty bool
	err := db.QueryRowContext(context.Background(), "SELECT dirty FROM schema_migrations LIMIT 1").Scan(&dirty)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("query dirty: %v", err)
	}
	return dirty
}

// sourceWithExtra 构造一个包含全部内嵌迁移 + 额外迁移的测试用文件系统。
// 用于覆盖「增量迁移」与「失败迁移」场景，不修改生产内嵌文件。
func sourceWithExtra(extra map[string]string) fs.FS {
	m := fstest.MapFS{}
	entries, err := fs.ReadDir(embeddedMigrations, migrationDir)
	if err == nil {
		for _, e := range entries {
			data, rerr := fs.ReadFile(embeddedMigrations, migrationDir+"/"+e.Name())
			if rerr != nil {
				continue
			}
			m[migrationDir+"/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	for name, content := range extra {
		m[migrationDir+"/"+name] = &fstest.MapFile{Data: []byte(content)}
	}
	return m
}

// TestUpCreatesSchemaAndIsIdempotent 覆盖 AC-001/AC-002（INV-001 幂等按序一次）：
// 空库执行 Up 建立 16 张业务表 + schema_migrations；再次 Up 幂等、版本不变。
// 说明：迁移为无 IF NOT EXISTS 的普通 CREATE TABLE，若被重复执行会因表已存在而报错，
// 因此「再次 Up 成功」本身就是「旧迁移未重跑」的直接证明。
func TestUpCreatesSchemaAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("first up: %v", err)
	}
	for _, table := range businessTables {
		if !tableExists(t, db, table) {
			t.Errorf("expected table %s to exist after up", table)
		}
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Errorf("expected current version %d after up, got %d", latestMigrationVersion, v)
	}
	if dirtyState(t, db) {
		t.Errorf("expected dirty=false after successful up")
	}

	if err := Up(ctx); err != nil {
		t.Fatalf("second up should be idempotent, got error: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Errorf("expected current version unchanged (%d) after second up, got %d", latestMigrationVersion, v)
	}
}

// TestStatusIsReadOnly 覆盖 INV-005（serve 无 DDL）：Status 只读，
// 空库上不创建任何表（含 schema_migrations），并返回 current=0。
func TestStatusIsReadOnly(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	current, dirty, latest, err := Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if current != 0 || dirty {
		t.Errorf("expected current=0 dirty=false on empty db, got current=%d dirty=%t", current, dirty)
	}
	if latest != latestMigrationVersion {
		t.Errorf("expected latest=%d, got %d", latestMigrationVersion, latest)
	}
	if tableExists(t, db, "schema_migrations") {
		t.Errorf("Status must not create schema_migrations")
	}
	for _, table := range businessTables {
		if tableExists(t, db, table) {
			t.Errorf("Status must not create business table %s", table)
		}
	}
}

// TestStatusAfterUp 覆盖 Status 在已迁移库上的只读结果。
func TestStatusAfterUp(t *testing.T) {
	ctx := context.Background()
	setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	current, dirty, latest, err := Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if current != latestMigrationVersion || dirty {
		t.Errorf("expected current=%d dirty=false, got current=%d dirty=%t", latestMigrationVersion, current, dirty)
	}
	if latest != latestMigrationVersion {
		t.Errorf("expected latest=%d, got %d", latestMigrationVersion, latest)
	}
}

// TestForceBaselineDoesNotExecuteSQL 覆盖 force 语义（baseline 接管 / dirty 恢复）：
// force 只标记版本已应用，不执行 SQL、不建业务表。
func TestForceBaselineDoesNotExecuteSQL(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Force(ctx, baselineVersion); err != nil {
		t.Fatalf("force: %v", err)
	}
	current, dirty, _, err := Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if current != baselineVersion || dirty {
		t.Errorf("expected current=%d dirty=false after force, got current=%d dirty=%t", baselineVersion, current, dirty)
	}
	for _, table := range businessTables {
		if tableExists(t, db, table) {
			t.Errorf("force must not create business table %s", table)
		}
	}
}

// TestUpAppliesOnlyPendingMigration 覆盖 AC-003（INV-001 增量）：
// 已有库上新增一个合法迁移后 Up，仅新增迁移被执行（版本前进），旧迁移不重跑。
func TestUpAppliesOnlyPendingMigration(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("baseline up: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Fatalf("expected latest version %d, got %d", latestMigrationVersion, v)
	}

	migrationFS = sourceWithExtra(map[string]string{
		"20261001000008_probe.up.sql": "CREATE TABLE migration_probe (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, PRIMARY KEY (id)) ENGINE=InnoDB;",
	})

	if err := Up(ctx); err != nil {
		t.Fatalf("incremental up: %v", err)
	}
	if v := currentVersion(t, db); v != uint(20261001000008) {
		t.Errorf("expected current version %d after incremental up, got %d", uint(20261001000008), v)
	}
	if !tableExists(t, db, "migration_probe") {
		t.Errorf("expected migration_probe table created by incremental migration")
	}
}

// TestUpFailsFastAndMarksDirty 覆盖 AC-004（INV-002 fail-fast 与 dirty 恢复）：
// 失败迁移使 Up 返回错误、置 dirty；dirty 下再次 Up 拒绝执行；force 后恢复。
func TestUpFailsFastAndMarksDirty(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	migrationFS = sourceWithExtra(map[string]string{
		"20261001000008_broken.up.sql": "THIS IS NOT VALID SQL;",
	})

	if err := Up(ctx); err == nil {
		t.Fatalf("expected Up to fail on broken migration")
	}
	if !dirtyState(t, db) {
		t.Errorf("expected dirty=true after failed migration")
	}

	// dirty 状态下再次 Up 应拒绝执行。
	if err := Up(ctx); err == nil {
		t.Fatalf("expected Up to refuse while dirty")
	}

	// force 恢复 dirty。
	if err := Force(ctx, uint(20261001000008)); err != nil {
		t.Fatalf("force recover: %v", err)
	}
	if dirtyState(t, db) {
		t.Errorf("expected dirty=false after force")
	}
}

// TestConcurrentUp 覆盖 AC-006（INV-006 并发单实例执行）：
// 多 goroutine 并发 Up 由 GET_LOCK 串行化，最终结构正确、版本无重复。
func TestConcurrentUp(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	const n = 4
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- Up(ctx)
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent up should be safe, got error: %v", err)
		}
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Errorf("expected current version %d after concurrent up, got %d", latestMigrationVersion, v)
	}
	for _, table := range businessTables {
		if !tableExists(t, db, table) {
			t.Errorf("expected table %s to exist after concurrent up", table)
		}
	}
}

// columnSpec 描述一列的期望结构，对应 information_schema.columns 的归一化字段。
type columnSpec struct {
	Name     string  // COLUMN_NAME
	Type     string  // COLUMN_TYPE（含 unsigned、长度/精度）
	Nullable bool    // true 表示允许 NULL；默认 false（仅 orders/order_items 的部分可空字段为 true）
	Default  *string // 期望默认值；nil 表示无默认值（COLUMN_DEFAULT IS NULL）
	Extra    string  // EXTRA：auto_increment / DEFAULT_GENERATED on update CURRENT_TIMESTAMP 等
}

// indexSpec 描述一个索引的期望结构（索引内字段顺序敏感）。
type indexSpec struct {
	Name    string
	Unique  bool
	Columns []string
}

// tableSpec 描述一张表的期望结构（列顺序敏感）。
type tableSpec struct {
	Name      string
	Engine    string
	Collation string
	Columns   []columnSpec
	Indexes   []indexSpec
}

// strPtr 便于书写字符串默认值（区分「默认空字符串」与「无默认值」）。
func strPtr(s string) *string { return &s }

// expectedSchema 是「7 张 baseline 表 + orders + order_items」DDL 的精确结构快照，
// 是 INV-003「结构严格等价」的权威基准。它独立于迁移文件硬编码，因此任何对相关迁移的
// 列/类型/空值/默认值/索引/引擎/字符集改动若不同步更新此处，等价性测试都会失败——
// 这正是它能够识别错误实现的原因。
var expectedSchema = []tableSpec{
	{
		Name:      "users",
		Engine:    "InnoDB",
		Collation: "utf8mb4_unicode_ci",
		Columns: []columnSpec{
			{Name: "id", Type: "bigint unsigned", Extra: "auto_increment"},
			{Name: "username", Type: "varchar(24)"},
			{Name: "password_hash", Type: "varchar(60)"},
			{Name: "created_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED"},
			{Name: "updated_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED on update CURRENT_TIMESTAMP"},
		},
		Indexes: []indexSpec{
			{Name: "PRIMARY", Unique: true, Columns: []string{"id"}},
			{Name: "uk_username", Unique: true, Columns: []string{"username"}},
		},
	},
	{
		Name:      "categories",
		Engine:    "InnoDB",
		Collation: "utf8mb4_unicode_ci",
		Columns: []columnSpec{
			{Name: "id", Type: "bigint unsigned", Extra: "auto_increment"},
			{Name: "parent_id", Type: "bigint unsigned", Default: strPtr("0")},
			{Name: "name", Type: "varchar(64)"},
			{Name: "sort", Type: "int", Default: strPtr("0")},
			{Name: "status", Type: "tinyint", Default: strPtr("1")},
			{Name: "created_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED"},
			{Name: "updated_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED on update CURRENT_TIMESTAMP"},
		},
		Indexes: []indexSpec{
			{Name: "PRIMARY", Unique: true, Columns: []string{"id"}},
			{Name: "uk_parent_name", Unique: true, Columns: []string{"parent_id", "name"}},
		},
	},
	{
		Name:      "admins",
		Engine:    "InnoDB",
		Collation: "utf8mb4_unicode_ci",
		Columns: []columnSpec{
			{Name: "id", Type: "bigint unsigned", Extra: "auto_increment"},
			{Name: "username", Type: "varchar(24)"},
			{Name: "password_hash", Type: "varchar(60)"},
			{Name: "status", Type: "tinyint", Default: strPtr("1")},
			{Name: "is_super", Type: "tinyint", Default: strPtr("0")},
			{Name: "created_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED"},
			{Name: "updated_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED on update CURRENT_TIMESTAMP"},
		},
		Indexes: []indexSpec{
			{Name: "PRIMARY", Unique: true, Columns: []string{"id"}},
			{Name: "uk_admin_username", Unique: true, Columns: []string{"username"}},
		},
	},
	{
		Name:      "roles",
		Engine:    "InnoDB",
		Collation: "utf8mb4_unicode_ci",
		Columns: []columnSpec{
			{Name: "id", Type: "bigint unsigned", Extra: "auto_increment"},
			{Name: "name", Type: "varchar(64)"},
			{Name: "description", Type: "varchar(255)", Default: strPtr("")},
			{Name: "created_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED"},
			{Name: "updated_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED on update CURRENT_TIMESTAMP"},
		},
		Indexes: []indexSpec{
			{Name: "PRIMARY", Unique: true, Columns: []string{"id"}},
			{Name: "uk_role_name", Unique: true, Columns: []string{"name"}},
		},
	},
	{
		Name:      "permissions",
		Engine:    "InnoDB",
		Collation: "utf8mb4_unicode_ci",
		Columns: []columnSpec{
			{Name: "id", Type: "bigint unsigned", Extra: "auto_increment"},
			{Name: "code", Type: "varchar(64)"},
			{Name: "name", Type: "varchar(64)"},
			{Name: "description", Type: "varchar(255)", Default: strPtr("")},
			{Name: "created_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED"},
			{Name: "updated_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED on update CURRENT_TIMESTAMP"},
		},
		Indexes: []indexSpec{
			{Name: "PRIMARY", Unique: true, Columns: []string{"id"}},
			{Name: "uk_permission_code", Unique: true, Columns: []string{"code"}},
		},
	},
	{
		Name:      "admin_roles",
		Engine:    "InnoDB",
		Collation: "utf8mb4_unicode_ci",
		Columns: []columnSpec{
			{Name: "admin_id", Type: "bigint unsigned"},
			{Name: "role_id", Type: "bigint unsigned"},
		},
		Indexes: []indexSpec{
			{Name: "PRIMARY", Unique: true, Columns: []string{"admin_id", "role_id"}},
		},
	},
	{
		Name:      "role_permissions",
		Engine:    "InnoDB",
		Collation: "utf8mb4_unicode_ci",
		Columns: []columnSpec{
			{Name: "role_id", Type: "bigint unsigned"},
			{Name: "permission_id", Type: "bigint unsigned"},
		},
		Indexes: []indexSpec{
			{Name: "PRIMARY", Unique: true, Columns: []string{"role_id", "permission_id"}},
		},
	},
	{
		Name:      "orders",
		Engine:    "InnoDB",
		Collation: "utf8mb4_unicode_ci",
		Columns: []columnSpec{
			{Name: "id", Type: "bigint unsigned", Extra: "auto_increment"},
			{Name: "order_no", Type: "varchar(32)"},
			{Name: "user_id", Type: "bigint unsigned"},
			{Name: "status", Type: "tinyint", Default: strPtr("10")},
			{Name: "total_amount", Type: "int unsigned"},
			{Name: "idempotency_key", Type: "varchar(64)"},
			{Name: "request_hash", Type: "varchar(64)"},
			{Name: "recipient_name", Type: "varchar(32)"},
			{Name: "phone", Type: "varchar(20)"},
			{Name: "province", Type: "varchar(32)"},
			{Name: "city", Type: "varchar(32)"},
			{Name: "district", Type: "varchar(32)"},
			{Name: "detail", Type: "varchar(255)"},
			{Name: "address_id", Type: "bigint unsigned", Nullable: true},
			{Name: "expire_at", Type: "datetime"},
			{Name: "cancel_reason", Type: "tinyint", Nullable: true},
			{Name: "paid_at", Type: "datetime", Nullable: true},
			{Name: "shipped_at", Type: "datetime", Nullable: true},
			{Name: "received_at", Type: "datetime", Nullable: true},
			{Name: "completed_at", Type: "datetime", Nullable: true},
			{Name: "cancelled_at", Type: "datetime", Nullable: true},
			{Name: "refunded_at", Type: "datetime", Nullable: true},
			{Name: "created_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED"},
			{Name: "updated_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED on update CURRENT_TIMESTAMP"},
		},
		Indexes: []indexSpec{
			{Name: "PRIMARY", Unique: true, Columns: []string{"id"}},
			{Name: "uk_order_no", Unique: true, Columns: []string{"order_no"}},
			{Name: "uk_user_idempotency", Unique: true, Columns: []string{"user_id", "idempotency_key"}},
			{Name: "idx_user_id", Unique: false, Columns: []string{"user_id"}},
			{Name: "idx_status_expire", Unique: false, Columns: []string{"status", "expire_at"}},
		},
	},
	{
		Name:      "order_items",
		Engine:    "InnoDB",
		Collation: "utf8mb4_unicode_ci",
		Columns: []columnSpec{
			{Name: "id", Type: "bigint unsigned", Extra: "auto_increment"},
			{Name: "order_id", Type: "bigint unsigned"},
			{Name: "sku_id", Type: "bigint unsigned"},
			{Name: "product_id", Type: "bigint unsigned"},
			{Name: "sku_name", Type: "varchar(128)"},
			{Name: "product_name", Type: "varchar(128)"},
			{Name: "product_main_image", Type: "varchar(512)", Default: strPtr("")},
			{Name: "price", Type: "int unsigned"},
			{Name: "quantity", Type: "int unsigned"},
			{Name: "created_at", Type: "datetime", Default: strPtr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED"},
		},
		Indexes: []indexSpec{
			{Name: "PRIMARY", Unique: true, Columns: []string{"id"}},
			{Name: "idx_order_id", Unique: false, Columns: []string{"order_id"}},
		},
	},
}

// readActualTable 从 information_schema 读取某表的实际结构，返回与 tableSpec 对齐的结构。
func readActualTable(t *testing.T, db *sql.DB, name string) tableSpec {
	t.Helper()
	var out tableSpec
	out.Name = name

	if err := db.QueryRowContext(context.Background(),
		`SELECT ENGINE, TABLE_COLLATION FROM information_schema.tables
		 WHERE table_schema = DATABASE() AND table_name = ?`, name).Scan(&out.Engine, &out.Collation); err != nil {
		t.Fatalf("query table %s: %v", name, err)
	}

	rows, err := db.QueryContext(context.Background(),
		`SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA
		 FROM information_schema.columns
		 WHERE table_schema = DATABASE() AND table_name = ?
		 ORDER BY ORDINAL_POSITION`, name)
	if err != nil {
		t.Fatalf("query columns of %s: %v", name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var colName, colType, isNullable, extra string
		var colDefault sql.NullString
		if err := rows.Scan(&colName, &colType, &isNullable, &colDefault, &extra); err != nil {
			t.Fatalf("scan column of %s: %v", name, err)
		}
		c := columnSpec{Name: colName, Type: colType, Nullable: isNullable == "YES", Extra: extra}
		if colDefault.Valid {
			c.Default = &colDefault.String
		}
		out.Columns = append(out.Columns, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns of %s: %v", name, err)
	}

	irows, err := db.QueryContext(context.Background(),
		`SELECT index_name, non_unique, seq_in_index, column_name
		 FROM information_schema.statistics
		 WHERE table_schema = DATABASE() AND table_name = ?
		 ORDER BY index_name, seq_in_index`, name)
	if err != nil {
		t.Fatalf("query indexes of %s: %v", name, err)
	}
	defer irows.Close()
	byName := map[string]*indexSpec{}
	for irows.Next() {
		var idxName, colName string
		var nonUnique, seq int
		if err := irows.Scan(&idxName, &nonUnique, &seq, &colName); err != nil {
			t.Fatalf("scan index of %s: %v", name, err)
		}
		spec, ok := byName[idxName]
		if !ok {
			spec = &indexSpec{Name: idxName, Unique: nonUnique == 0}
			byName[idxName] = spec
		}
		spec.Columns = append(spec.Columns, colName)
	}
	if err := irows.Err(); err != nil {
		t.Fatalf("iterate indexes of %s: %v", name, err)
	}
	for _, idx := range byName {
		out.Indexes = append(out.Indexes, *idx)
	}
	return out
}

// assertTableEquivalent 逐项比较实际结构与期望结构（列顺序、索引字段顺序均敏感）。
func assertTableEquivalent(t *testing.T, want, actual tableSpec) {
	t.Helper()
	if actual.Engine != want.Engine {
		t.Errorf("%s: ENGINE mismatch: want %q, got %q", want.Name, want.Engine, actual.Engine)
	}
	if actual.Collation != want.Collation {
		t.Errorf("%s: 字符集/排序规则 mismatch: want %q, got %q", want.Name, want.Collation, actual.Collation)
	}

	if len(actual.Columns) != len(want.Columns) {
		t.Errorf("%s: 列数量 mismatch: want %d, got %d", want.Name, len(want.Columns), len(actual.Columns))
	}
	for i := 0; i < len(want.Columns) && i < len(actual.Columns); i++ {
		w, a := want.Columns[i], actual.Columns[i]
		if a.Name != w.Name {
			t.Errorf("%s: 第 %d 列名 mismatch: want %q, got %q", want.Name, i+1, w.Name, a.Name)
			continue
		}
		if a.Type != w.Type {
			t.Errorf("%s.%s: 类型 mismatch: want %q, got %q", want.Name, w.Name, w.Type, a.Type)
		}
		if a.Nullable != w.Nullable {
			t.Errorf("%s.%s: NULL 属性 mismatch: want nullable=%t, got %t", want.Name, w.Name, w.Nullable, a.Nullable)
		}
		if !defaultEqual(a.Default, w.Default) {
			t.Errorf("%s.%s: DEFAULT mismatch: want %v, got %v", want.Name, w.Name, w.Default, a.Default)
		}
		if a.Extra != w.Extra {
			t.Errorf("%s.%s: EXTRA mismatch: want %q, got %q", want.Name, w.Name, w.Extra, a.Extra)
		}
	}

	if len(actual.Indexes) != len(want.Indexes) {
		t.Errorf("%s: 索引数量 mismatch: want %d, got %d", want.Name, len(want.Indexes), len(actual.Indexes))
	}
	actualIdx := map[string]indexSpec{}
	for _, idx := range actual.Indexes {
		actualIdx[idx.Name] = idx
	}
	wantIdx := map[string]indexSpec{}
	for _, idx := range want.Indexes {
		wantIdx[idx.Name] = idx
	}
	for _, w := range want.Indexes {
		a, ok := actualIdx[w.Name]
		if !ok {
			t.Errorf("%s: 缺少索引 %q", want.Name, w.Name)
			continue
		}
		if a.Unique != w.Unique {
			t.Errorf("%s: 索引 %q 唯一性 mismatch: want unique=%t, got %t", want.Name, w.Name, w.Unique, a.Unique)
		}
		if !equalStrings(a.Columns, w.Columns) {
			t.Errorf("%s: 索引 %q 字段/顺序 mismatch: want %v, got %v", want.Name, w.Name, w.Columns, a.Columns)
		}
	}
	for _, a := range actual.Indexes {
		if _, ok := wantIdx[a.Name]; !ok {
			t.Errorf("%s: 出现多余索引 %q", want.Name, a.Name)
		}
	}
}

func defaultEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSchemaStructureMatchesBaseline 覆盖 INV-003（结构严格等价）：
// 对 baseline 与订单相关表逐表校验表级属性（ENGINE/字符集排序规则）与逐列（名称/顺序/类型/空值/默认值/AUTO_INCREMENT），
// 以及索引（名称/唯一性/字段及顺序），能够识别任何字段类型、默认值、空值约束、字符集或索引的漂移。
func TestSchemaStructureMatchesBaseline(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	for _, want := range expectedSchema {
		actual := readActualTable(t, db, want.Name)
		assertTableEquivalent(t, want, actual)
	}
}
