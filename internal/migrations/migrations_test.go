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

// businessTables 是 migration 应建立的 7 张业务表。
var businessTables = []string{
	"users", "categories", "admins", "roles", "permissions", "admin_roles", "role_permissions",
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
// 空库执行 Up 建立 7 张业务表 + schema_migrations；再次 Up 幂等、版本不变。
// 说明：baseline 为无 IF NOT EXISTS 的普通 CREATE TABLE，若被重复执行会因表已存在而报错，
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
	if v := currentVersion(t, db); v != baselineVersion {
		t.Errorf("expected current version %d after up, got %d", baselineVersion, v)
	}
	if dirtyState(t, db) {
		t.Errorf("expected dirty=false after successful up")
	}

	if err := Up(ctx); err != nil {
		t.Fatalf("second up should be idempotent, got error: %v", err)
	}
	if v := currentVersion(t, db); v != baselineVersion {
		t.Errorf("expected current version unchanged (%d) after second up, got %d", baselineVersion, v)
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
	if latest != baselineVersion {
		t.Errorf("expected latest=%d, got %d", baselineVersion, latest)
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
	if current != baselineVersion || dirty {
		t.Errorf("expected current=%d dirty=false, got current=%d dirty=%t", baselineVersion, current, dirty)
	}
	if latest != baselineVersion {
		t.Errorf("expected latest=%d, got %d", baselineVersion, latest)
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
	if v := currentVersion(t, db); v != baselineVersion {
		t.Fatalf("expected baseline version %d, got %d", baselineVersion, v)
	}

	migrationFS = sourceWithExtra(map[string]string{
		"20261001000002_probe.up.sql": "CREATE TABLE migration_probe (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, PRIMARY KEY (id)) ENGINE=InnoDB;",
	})

	if err := Up(ctx); err != nil {
		t.Fatalf("incremental up: %v", err)
	}
	if v := currentVersion(t, db); v != uint(20261001000002) {
		t.Errorf("expected current version %d after incremental up, got %d", uint(20261001000002), v)
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
		"20261001000002_broken.up.sql": "THIS IS NOT VALID SQL;",
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
	if err := Force(ctx, uint(20261001000002)); err != nil {
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
	if v := currentVersion(t, db); v != baselineVersion {
		t.Errorf("expected current version %d after concurrent up, got %d", baselineVersion, v)
	}
	for _, table := range businessTables {
		if !tableExists(t, db, table) {
			t.Errorf("expected table %s to exist after concurrent up", table)
		}
	}
}

// uniqueIndexes 返回表的唯一索引名 → 列（按序）映射，用于校验结构与迁移前等价。
func uniqueIndexes(t *testing.T, db *sql.DB, table string) map[string][]string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT DISTINCT index_name, column_name, seq_in_index
		 FROM information_schema.statistics
		 WHERE table_schema = DATABASE() AND table_name = ? AND non_unique = 0
		 ORDER BY index_name, seq_in_index`, table)
	if err != nil {
		t.Fatalf("query indexes of %s: %v", table, err)
	}
	defer rows.Close()

	indexes := map[string][]string{}
	for rows.Next() {
		var name, col string
		var seq int
		if err := rows.Scan(&name, &col, &seq); err != nil {
			t.Fatalf("scan index row of %s: %v", table, err)
		}
		indexes[name] = append(indexes[name], col)
	}
	return indexes
}

// TestSchemaStructureMatchesBaseline 覆盖 INV-003（结构严格等价）：
// 校验 7 张表的唯一索引（名称与列）与迁移前 DDL 一致，捕获索引/约束漂移。
func TestSchemaStructureMatchesBaseline(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	want := map[string]map[string][]string{
		"users":            {"PRIMARY": {"id"}, "uk_username": {"username"}},
		"categories":       {"PRIMARY": {"id"}, "uk_parent_name": {"parent_id", "name"}},
		"admins":           {"PRIMARY": {"id"}, "uk_admin_username": {"username"}},
		"roles":            {"PRIMARY": {"id"}, "uk_role_name": {"name"}},
		"permissions":      {"PRIMARY": {"id"}, "uk_permission_code": {"code"}},
		"admin_roles":      {"PRIMARY": {"admin_id", "role_id"}},
		"role_permissions": {"PRIMARY": {"role_id", "permission_id"}},
	}

	for table, expected := range want {
		got := uniqueIndexes(t, db, table)
		if len(got) != len(expected) {
			t.Errorf("%s: expected %d unique indexes, got %d (%v)", table, len(expected), len(got), got)
		}
		for name, cols := range expected {
			gotCols, ok := got[name]
			if !ok {
				t.Errorf("%s: missing unique index %q", table, name)
				continue
			}
			if len(gotCols) != len(cols) {
				t.Errorf("%s: index %q columns mismatch: want %v, got %v", table, name, cols, gotCols)
				continue
			}
			for i := range cols {
				if gotCols[i] != cols[i] {
					t.Errorf("%s: index %q column order mismatch: want %v, got %v", table, name, cols, gotCols)
					break
				}
			}
		}
	}
}
