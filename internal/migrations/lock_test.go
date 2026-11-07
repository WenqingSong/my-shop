package migrations

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4/database"

	"cnb.cool/go-cloud-devops/my-shop/internal/config"
)

// migrationLockID 返回 golang-migrate MySQL driver 使用的会话级锁名。
//
// 与 driver 内部实现一致：GenerateAdvisoryLockId(fmt.Sprintf("%s:%s", DatabaseName, MigrationsTable))。
// 用它可以在测试里模拟「另一个 migration 进程正持有锁」，而不需要真的启动第二个进程
// （真实双进程并发由 process_test.go 覆盖）。
func migrationLockID(t *testing.T) string {
	t.Helper()
	name, err := config.String(context.Background(), config.KeyDatabaseDefaultName)
	if err != nil {
		t.Fatalf("读取数据库名: %v", err)
	}
	id, err := database.GenerateAdvisoryLockId(name + ":schema_migrations")
	if err != nil {
		t.Fatalf("生成 advisory lock id: %v", err)
	}
	return id
}

// holdLock 用独立连接池获取 migration 锁并保持会话存活；
// 返回的 *sql.DB 被 Close 时即模拟「持锁进程被杀」，MySQL 会释放会话级锁。
func holdLock(t *testing.T) *sql.DB {
	t.Helper()
	dsn, err := buildDSN(context.Background())
	if err != nil {
		t.Fatalf("构建 DSN: %v", err)
	}
	holder, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("打开持锁连接: %v", err)
	}
	// 单连接池：保证 GET_LOCK 与后续会话绑定在同一条连接上。
	holder.SetMaxOpenConns(1)
	holder.SetMaxIdleConns(1)

	var acquired int
	if err := holder.QueryRowContext(context.Background(),
		"SELECT GET_LOCK(?, 0)", migrationLockID(t)).Scan(&acquired); err != nil {
		_ = holder.Close()
		t.Fatalf("获取测试用 migration 锁: %v", err)
	}
	if acquired != 1 {
		_ = holder.Close()
		t.Fatal("测试未能获取 migration 锁（可能残留了其它持锁会话）")
	}
	t.Cleanup(func() { _ = holder.Close() })
	return holder
}

// TestUpFailsWhenLockHeldByAnotherSession 覆盖「获取锁超时」：
// 锁被占用时 Up 必须等待 driver 内置的 10s 超时后明确失败，不执行任何 DDL、
// 不创建 schema_migrations；锁释放后同一进程可以正常完成迁移。
//
// 该用例直接对应 K8s 场景：两个 Migration Job 同时启动，一个执行、另一个失败退出
// 并由 Job backoffLimit 重试，而不是并发改 Schema。
func TestUpFailsWhenLockHeldByAnotherSession(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	holder := holdLock(t)

	started := time.Now()
	err := Up(ctx)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("migration 锁被占用时 Up 必须失败，不能并发执行 DDL")
	}
	if !strings.Contains(err.Error(), "未获取到数据库级 migration 锁") {
		t.Fatalf("错误信息必须明确说明锁冲突，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "未执行任何 DDL") {
		t.Fatalf("错误信息必须说明本次未执行 DDL，实际: %v", err)
	}
	if elapsed < 9*time.Second {
		t.Fatalf("应等待 driver 内置 GET_LOCK 10s 超时，实际耗时 %s", elapsed)
	}
	if tableExists(t, db, "schema_migrations") {
		t.Error("未获取锁时不得创建 schema_migrations")
	}
	for _, table := range businessTables {
		if tableExists(t, db, table) {
			t.Errorf("未获取锁时不得创建业务表 %s", table)
		}
	}

	// 释放锁（关闭持锁会话）后必须可以正常迁移。
	if err := holder.Close(); err != nil {
		t.Logf("关闭持锁连接: %v", err)
	}
	if err := Up(ctx); err != nil {
		t.Fatalf("锁释放后 Up 应成功，实际: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Errorf("锁释放后应迁移到最新版本 %d，实际 %d", latestMigrationVersion, v)
	}
}

// TestUpSucceedsAfterLockHolderDisconnects 覆盖「Job 被终止」：
// 持锁会话断开（Pod 被删除 / 进程被杀）后 MySQL 自动释放锁，不会永久阻塞后续迁移。
func TestUpSucceedsAfterLockHolderDisconnects(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	holder := holdLock(t)
	// 关闭连接池 = 关闭会话 = MySQL 释放会话级 GET_LOCK。
	if err := holder.Close(); err != nil {
		t.Fatalf("关闭持锁连接: %v", err)
	}

	started := time.Now()
	if err := Up(ctx); err != nil {
		t.Fatalf("持锁会话断开后 Up 应成功（锁已自动释放），实际: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 9*time.Second {
		t.Fatalf("锁应在连接断开后立即释放，实际等待 %s", elapsed)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Errorf("应迁移到最新版本 %d，实际 %d", latestMigrationVersion, v)
	}
}

// TestUpReleasesDatabaseConnections 覆盖「连接释放」：
// 每次 migrate 都独立打开并关闭数据库连接；反复执行不应留下驻留连接。
func TestUpReleasesDatabaseConnections(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("首次 up: %v", err)
	}
	before := threadsConnected(t, db)

	for i := 0; i < 3; i++ {
		if err := Up(ctx); err != nil {
			t.Fatalf("第 %d 次重复 up: %v", i+1, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		after := threadsConnected(t, db)
		if after <= before+1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("migration 连接未释放：执行前 %d 条连接，执行后 %d 条（预期回落）", before, after)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// threadsConnected 返回 MySQL 当前连接线程数（含测试自身的连接）。
func threadsConnected(t *testing.T, db *sql.DB) int {
	t.Helper()
	var name string
	var value string
	if err := db.QueryRowContext(context.Background(),
		"SHOW GLOBAL STATUS LIKE 'Threads_connected'").Scan(&name, &value); err != nil {
		t.Fatalf("查询 Threads_connected: %v", err)
	}
	count, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("Threads_connected 非数字: %q", value)
	}
	return count
}
