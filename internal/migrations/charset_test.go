package migrations

import (
	"context"
	"strings"
	"testing"

	go_mysql "github.com/go-sql-driver/mysql"
)

// TestBuildDSNRejectsEmptyPass 覆盖 AC-003/INV-001：密码缺失（config 空且无 DATABASE_DEFAULT_PASS）
// 时 buildDSN fail-fast，不回退任何内置默认密码（原 defaultDBPass="root"）。
func TestBuildDSNRejectsEmptyPass(t *testing.T) {
	ctx := context.Background()
	t.Setenv("DATABASE_DEFAULT_PASS", "")
	if _, err := buildDSN(ctx); err == nil {
		t.Fatal("密码缺失时 buildDSN 应返回错误，实际成功")
	}
}

// TestBuildDSNCharsetUTF8MB4 覆盖 AC-006/INV-002：连接 DSN 的 charset 必须为 utf8mb4。
// 若 charset 被改回 utf8（utf8mb3），本测试即失败，因此能区分正确/错误实现。
func TestBuildDSNCharsetUTF8MB4(t *testing.T) {
	ctx := context.Background()
	t.Setenv("DATABASE_DEFAULT_PASS", "test-pass")
	dsn, err := buildDSN(ctx)
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	cfg, err := go_mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	if got := cfg.Params["charset"]; got != "utf8mb4" {
		t.Fatalf("DSN charset = %q, want utf8mb4", got)
	}
	if !strings.Contains(dsn, "charset=utf8mb4") {
		t.Fatalf("DSN 缺少 charset=utf8mb4: %q", dsn)
	}
}

// TestUTF8MB4FourByteRoundtrip 覆盖 AC-007/INV-002：
// 经统一后的连接（charset=utf8mb4）向 utf8mb4 表写入四字节 Unicode 字符（emoji / 补充平面字符）
// 并读回，读回值与原值一致、无截断或报错。
// 若连接 charset 被改回 utf8（utf8mb3），写入会因 MySQL「Incorrect string value」失败，
// 本测试即失败——因此能区分「utf8 连接（错误实现）」与「utf8mb4 连接（正确实现）」。
func TestUTF8MB4FourByteRoundtrip(t *testing.T) {
	ctx := context.Background()
	db := setupCleanDB(t)

	// 独立探针表（utf8mb4），避免污染业务表；charset_probe 不在业务表清单内，测试结束显式清理。
	if _, err := db.ExecContext(ctx, "CREATE TABLE charset_probe (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, v TEXT NOT NULL, PRIMARY KEY (id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"); err != nil {
		t.Fatalf("create charset_probe: %v", err)
	}
	defer func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS charset_probe") }()

	// 😀（U+1F600）、🎉（U+1F389）为 emoji，𝄞（U+1D11E）为补充平面字符，均为四字节字符。
	want := "😀 🎉 𝄞"
	if _, err := db.ExecContext(ctx, "INSERT INTO charset_probe (v) VALUES (?)", want); err != nil {
		t.Fatalf("写入四字节字符失败（连接 charset 可能为 utf8/utf8mb3）: %v", err)
	}
	var got string
	if err := db.QueryRowContext(ctx, "SELECT v FROM charset_probe LIMIT 1").Scan(&got); err != nil {
		t.Fatalf("读回四字节字符失败: %v", err)
	}
	if got != want {
		t.Fatalf("读回值 %q 与原值 %q 不一致（发生截断或乱码）", got, want)
	}
}
