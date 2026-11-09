package migrations

// 秒杀 B2（Request Outbox）迁移测试：
//   - 新增列的 up/down 可重复验证，且旧请求数据不丢失；
//   - 旧数据迁移后为「未发布」默认值（published_at=NULL / publish_attempts=0）；
//   - 既有唯一约束与 idx_dequeue 仍存在，现有 Scanner 查询仍可执行；
//   - 领取查询在真实 MySQL 8 上命中 idx_outbox_claim 且不做 filesort（执行计划验证，不靠想象）。
//
// 需真实 MySQL（与其它迁移测试同环境假设，DATABASE_DEFAULT_PASS 由测试入口注入）。

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
)

// outboxRequestTable 是本批次扩展为 Outbox 的既有请求表。
const outboxRequestTable = "flash_sale_order_requests"

// outboxAddedColumns 是本批次新增的发布状态列（down 后必须全部消失）。
var outboxAddedColumns = []string{
	"published_at", "publish_claim_token", "publish_lease_until", "publish_attempts",
	"next_publish_at", "last_publish_error", "published_partition", "published_offset",
}

// hasColumn 判断表结构中是否存在指定列。
func hasColumn(spec tableSpec, name string) bool {
	for _, c := range spec.Columns {
		if c.Name == name {
			return true
		}
	}
	return false
}

// findIndex 返回指定索引定义（不存在时 ok=false）。
func findIndex(spec tableSpec, name string) (indexSpec, bool) {
	for _, idx := range spec.Indexes {
		if idx.Name == name {
			return idx, true
		}
	}
	return indexSpec{}, false
}

// expectedOutboxTable 返回 expectedSchema 中请求表的权威结构定义。
func expectedOutboxTable(t *testing.T) tableSpec {
	t.Helper()
	for _, spec := range expectedSchema {
		if spec.Name == outboxRequestTable {
			return spec
		}
	}
	t.Fatalf("expectedSchema 缺少 %s 定义", outboxRequestTable)
	return tableSpec{}
}

// TestOutboxMigrationDownUpPreservesLegacyRequests 覆盖 B2-1：
// 先回滚到 B1 表结构并写入「旧数据」，再升级，验证数据不丢失、新列取默认值、
// 既有唯一约束与出队索引保持，且现有 Scanner 查询仍可执行。
func TestOutboxMigrationDownUpPreservesLegacyRequests(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if v := currentVersion(t, db); v != latestMigrationVersion {
		t.Fatalf("version=%d want %d", v, latestMigrationVersion)
	}

	// 回滚本批次（同时验证 down 文件可用）：恢复为 B1 的请求表结构。
	rollbackOutboxMigration(t, db)
	spec := readActualTable(t, db, outboxRequestTable)
	for _, col := range outboxAddedColumns {
		if hasColumn(spec, col) {
			t.Fatalf("down 后不应存在列 %s", col)
		}
	}
	if _, ok := findIndex(spec, "idx_outbox_claim"); ok {
		t.Fatalf("down 后不应存在 idx_outbox_claim")
	}
	if _, ok := findIndex(spec, "idx_dequeue"); !ok {
		t.Fatalf("down 后 idx_dequeue 应保持存在")
	}

	// 写入「B1 时代」的旧请求（包含 queued/success/dead 三种状态）。
	const (
		legacyQueuedID  = 1001
		legacySuccessID = 1002
		legacyDeadID    = 1003
	)
	legacyHash := strings.Repeat("a", 64)
	for _, row := range []struct {
		id     int64
		userID int64
		status int
		retry  int
	}{
		{legacyQueuedID, 7001, 0, 2},
		{legacySuccessID, 7002, 1, 0},
		{legacyDeadID, 7003, 3, 4},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO flash_sale_order_requests
			 (id, user_id, activity_id, sku_id, idempotency_key, request_hash, status, retry_count, next_attempt_at)
			 VALUES (?, ?, 88, 99, ?, ?, ?, ?, NOW())`,
			row.id, row.userID, legacyRequestKey(row.id), legacyHash, row.status, row.retry); err != nil {
			t.Fatalf("insert legacy row %d: %v", row.id, err)
		}
	}

	// 升级：旧数据保留，新列取默认值（未发布、无租约、0 次发布尝试）。
	if err := Up(ctx); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	spec = readActualTable(t, db, outboxRequestTable)
	assertTableEquivalent(t, expectedOutboxTable(t), spec)

	rows, err := db.QueryContext(ctx,
		`SELECT id, status, retry_count, published_at, publish_claim_token, publish_lease_until,
		        publish_attempts, next_publish_at, last_publish_error, published_partition, published_offset
		 FROM flash_sale_order_requests ORDER BY id`)
	if err != nil {
		t.Fatalf("query legacy rows: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		seen++
		var (
			id, retry                                         int64
			status                                            int
			publishedAt, token, lease, nextPublish, lastError sql.NullString
			attempts                                          int64
			partition, offset                                 sql.NullInt64
		)
		if err := rows.Scan(&id, &status, &retry, &publishedAt, &token, &lease, &attempts,
			&nextPublish, &lastError, &partition, &offset); err != nil {
			t.Fatalf("scan legacy row: %v", err)
		}
		if publishedAt.Valid || token.Valid || lease.Valid || nextPublish.Valid || lastError.Valid ||
			partition.Valid || offset.Valid {
			t.Fatalf("row %d 的发布状态列应为 NULL", id)
		}
		if attempts != 0 {
			t.Fatalf("row %d publish_attempts=%d want 0", id, attempts)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate legacy rows: %v", err)
	}
	if seen != 3 {
		t.Fatalf("legacy rows=%d want 3（迁移不得丢数据）", seen)
	}

	// 既有唯一约束仍生效（uk_request_idempotency）。
	if _, err := db.ExecContext(ctx,
		`INSERT INTO flash_sale_order_requests (user_id, activity_id, sku_id, idempotency_key, request_hash, status)
		 VALUES (7001, 88, 99, ?, ?, 0)`, legacyRequestKey(legacyQueuedID), legacyHash); err == nil {
		t.Fatalf("uk_request_idempotency 应继续拒绝重复请求")
	}

	// 现有 MySQL Scanner 的领取查询仍可执行（status=queued + 到期 + FOR UPDATE SKIP LOCKED）。
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	var scannerID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM flash_sale_order_requests
		 WHERE status=0 AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
		 ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&scannerID); err != nil {
		t.Fatalf("现有 Scanner 查询执行失败: %v", err)
	}
	if scannerID != legacyQueuedID {
		t.Fatalf("scanner 选中 id=%d want %d", scannerID, legacyQueuedID)
	}
}

// legacyRequestKey 返回旧数据行使用的幂等键（便于重复键断言）。
func legacyRequestKey(id int64) string {
	return "legacy-" + strconv.FormatInt(id, 10)
}

// rollbackOutboxMigration 用 golang-migrate 回滚本批次的一个版本（验证 down 文件）。
func rollbackOutboxMigration(t *testing.T, _ *sql.DB) {
	t.Helper()
	m, err := newMigrate(context.Background())
	if err != nil {
		t.Fatalf("new migrate: %v", err)
	}
	if err := m.Steps(-1); err != nil {
		closeMigrate(context.Background(), m)
		t.Fatalf("回滚 Outbox migration: %v", err)
	}
	closeMigrate(context.Background(), m)
}

// TestOutboxClaimQueryUsesIndex 覆盖 B2-1 的索引设计验证：
// 在真实 MySQL 8 上构造「大量已发布 + 到期待发布」的分布，ANALYZE 后检查领取查询的执行计划
// 命中 idx_outbox_claim、不做 filesort，且未到期（退避中）的行不进入索引范围。
func TestOutboxClaimQueryUsesIndex(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)
	if err := Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	// 单条多语句执行：CTE 递归深度是会话变量，必须与插入语句在同一连接上生效。
	if _, err := db.ExecContext(ctx, `
SET SESSION cte_max_recursion_depth = 20000;
INSERT INTO flash_sale_order_requests (user_id, activity_id, sku_id, idempotency_key, request_hash, status, published_at, publish_attempts)
WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < 3000)
SELECT 900000+n, 7, 9, CONCAT('plan-published-', n), REPEAT('a',64), 1, NOW(6), 1 FROM seq;
INSERT INTO flash_sale_order_requests (user_id, activity_id, sku_id, idempotency_key, request_hash, status, publish_attempts)
WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < 1000)
SELECT 950000+n, 7, 9, CONCAT('plan-due-', n), REPEAT('b',64), 0, 0 FROM seq;
INSERT INTO flash_sale_order_requests (user_id, activity_id, sku_id, idempotency_key, request_hash, status, publish_attempts, next_publish_at)
WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < 1000)
SELECT 960000+n, 7, 9, CONCAT('plan-backoff-', n), REPEAT('c',64), 0, 1, DATE_ADD(NOW(6), INTERVAL 3600 SECOND) FROM seq;
ANALYZE TABLE flash_sale_order_requests;`); err != nil {
		t.Fatalf("seed plan data: %v", err)
	}

	const claimQuery = `SELECT id FROM flash_sale_order_requests
		WHERE status=0 AND published_at IS NULL
		  AND (next_publish_at IS NULL OR next_publish_at <= NOW(6))
		  AND (publish_lease_until IS NULL OR publish_lease_until <= NOW(6))
		ORDER BY next_publish_at, id LIMIT 50`

	var (
		table, planType, key, extra string
		rowsExamined                int64
	)
	explain := "EXPLAIN " + claimQuery
	queryRows, err := db.QueryContext(ctx, explain)
	if err != nil {
		t.Fatalf("explain claim query: %v", err)
	}
	defer queryRows.Close()
	cols, err := queryRows.Columns()
	if err != nil {
		t.Fatalf("explain columns: %v", err)
	}
	values := make([]sql.RawBytes, len(cols))
	scanArgs := make([]any, len(cols))
	for i := range values {
		scanArgs[i] = &values[i]
	}
	if !queryRows.Next() {
		t.Fatalf("explain returned no row")
	}
	if err := queryRows.Scan(scanArgs...); err != nil {
		t.Fatalf("scan explain: %v", err)
	}
	for i, col := range cols {
		v := string(values[i])
		switch col {
		case "table":
			table = v
		case "type":
			planType = v
		case "key":
			key = v
		case "rows":
			rowsExamined = int64(parseUintOrZero(v))
		case "Extra":
			extra = v
		}
	}
	if table != outboxRequestTable {
		t.Fatalf("explain table=%q want %q", table, outboxRequestTable)
	}
	if key != "idx_outbox_claim" {
		t.Fatalf("领取查询应命中 idx_outbox_claim，实际 key=%q type=%q extra=%q", key, planType, extra)
	}
	if strings.Contains(strings.ToLower(extra), "filesort") {
		t.Fatalf("领取查询不应 filesort，Extra=%q", extra)
	}
	if rowsExamined <= 0 || rowsExamined > 5000 {
		t.Fatalf("领取查询扫描行数=%d，应限制在到期集合规模内（排除退避中的行）", rowsExamined)
	}

	// 覆盖数量口径：到期集合 = due(1000) + backoff 中已到期者(0)，不含 3000 条已发布与 1000 条退避中。
	var dueCount int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM flash_sale_order_requests
		WHERE status=0 AND published_at IS NULL
		  AND (next_publish_at IS NULL OR next_publish_at <= NOW(6))
		  AND (publish_lease_until IS NULL OR publish_lease_until <= NOW(6))`).Scan(&dueCount); err != nil {
		t.Fatalf("count due: %v", err)
	}
	if dueCount != 1000 {
		t.Fatalf("到期待发布=%d want 1000", dueCount)
	}

	// 待发布积压统计（含退避中）使用 idx_outbox_claim 前缀做覆盖扫描。
	var backlogType, backlogKey, backlogExtra string
	backlogRows, err := db.QueryContext(ctx,
		"EXPLAIN SELECT COUNT(*), MIN(id) FROM flash_sale_order_requests WHERE status=0 AND published_at IS NULL")
	if err != nil {
		t.Fatalf("explain backlog query: %v", err)
	}
	defer backlogRows.Close()
	bcols, err := backlogRows.Columns()
	if err != nil {
		t.Fatalf("backlog explain columns: %v", err)
	}
	bvalues := make([]sql.RawBytes, len(bcols))
	bargs := make([]any, len(bcols))
	for i := range bvalues {
		bargs[i] = &bvalues[i]
	}
	if !backlogRows.Next() {
		t.Fatalf("backlog explain returned no row")
	}
	if err := backlogRows.Scan(bargs...); err != nil {
		t.Fatalf("scan backlog explain: %v", err)
	}
	for i, col := range bcols {
		v := string(bvalues[i])
		switch col {
		case "type":
			backlogType = v
		case "key":
			backlogKey = v
		case "Extra":
			backlogExtra = v
		}
	}
	if backlogKey != "idx_outbox_claim" {
		t.Fatalf("积压统计应使用 idx_outbox_claim，实际 key=%q type=%q extra=%q", backlogKey, backlogType, backlogExtra)
	}
	if !strings.Contains(strings.ToLower(backlogExtra), "using index") {
		t.Fatalf("积压统计应使用覆盖索引，Extra=%q", backlogExtra)
	}
}

// parseUintOrZero 把 EXPLAIN 的数字列（可能是 NULL 或字符串）转换为整数。
func parseUintOrZero(s string) uint64 {
	var n uint64
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + uint64(r-'0')
	}
	return n
}
