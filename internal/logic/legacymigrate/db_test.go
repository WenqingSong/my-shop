package legacymigrate

import (
	"context"
	"testing"

	go_mysql "github.com/go-sql-driver/mysql"
)

// TestOpenConnectionsRejectsEmptyPass 覆盖 INV-001：密码缺失（config 空且无 DATABASE_DEFAULT_PASS）
// 时 OpenConnections fail-fast，不回退任何内置默认密码（原 defaultDBPass="root"）。
func TestOpenConnectionsRejectsEmptyPass(t *testing.T) {
	t.Setenv("DATABASE_DEFAULT_PASS", "")
	if _, _, err := OpenConnections(context.Background()); err == nil {
		t.Fatal("密码缺失时 OpenConnections 应返回错误，实际成功")
	}
}

// TestBuildDSNCharsetUTF8MB4 覆盖 INV-002：legacy-migrate 连接 DSN 的 charset 必须为 utf8mb4。
func TestBuildDSNCharsetUTF8MB4(t *testing.T) {
	dsn := buildDSN("127.0.0.1", "3306", "root", "pass", "my_shop")
	cfg, err := go_mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	if got := cfg.Params["charset"]; got != "utf8mb4" {
		t.Fatalf("DSN charset = %q, want utf8mb4", got)
	}
}
