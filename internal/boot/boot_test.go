package boot

import (
	"context"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/database/gredis"
	"github.com/gogf/gf/v2/frame/g"
)

func TestCfgStringEnvOverride(t *testing.T) {
	ctx := context.Background()
	// 使用 t.Setenv 而非 genv.Set/os.Setenv：t.Setenv 在测试结束后自动恢复原值，
	// 避免把 DATABASE_DEFAULT_HOST 等进程级环境变量永久覆盖/清除，污染后续测试
	// （尤其 test.sh 注入的 DATABASE_DEFAULT_PASS 不能被 boot 包内其它测试清除）。
	t.Setenv("DATABASE_DEFAULT_HOST", "env-host")

	if got := cfgString(ctx, "database.default.host", "default-host"); got != "env-host" {
		t.Fatalf("expected env override %q, got %q", "env-host", got)
	}
}

func TestCfgStringDefaultFallback(t *testing.T) {
	ctx := context.Background()
	if got := cfgString(ctx, "nonexistent.config.key", "fallback"); got != "fallback" {
		t.Fatalf("expected default %q, got %q", "fallback", got)
	}
}

func TestApplyDatabaseConfigEnvOverride(t *testing.T) {
	ctx := context.Background()
	envs := map[string]string{
		"DATABASE_DEFAULT_HOST": "env-host",
		"DATABASE_DEFAULT_PORT": "13306",
		"DATABASE_DEFAULT_USER": "env-user",
		"DATABASE_DEFAULT_PASS": "env-pass",
		"DATABASE_DEFAULT_NAME": "env_db",
	}
	for k, v := range envs {
		t.Setenv(k, v)
	}

	if err := applyDatabaseConfig(ctx); err != nil {
		t.Fatalf("applyDatabaseConfig failed: %v", err)
	}

	group, err := gdb.GetConfigGroup(gdb.DefaultGroupName)
	if err != nil {
		t.Fatalf("expected database config group: %v", err)
	}
	if len(group) == 0 {
		t.Fatal("expected database config group to contain a node")
	}
	node := group[0]
	if node.Host != "env-host" ||
		node.Port != "13306" ||
		node.User != "env-user" ||
		node.Pass != "env-pass" ||
		node.Name != "env_db" {
		t.Fatalf("unexpected database config node: %+v", node)
	}
}

// TestApplyDatabaseConfigRejectsEmptyPass 覆盖 AC-003/INV-001：数据库密码缺失
// （config 空且无 DATABASE_DEFAULT_PASS）时 applyDatabaseConfig fail-fast，不回退 "root" 等内置默认密码。
func TestApplyDatabaseConfigRejectsEmptyPass(t *testing.T) {
	ctx := context.Background()
	t.Setenv("DATABASE_DEFAULT_PASS", "")
	if err := applyDatabaseConfig(ctx); err == nil {
		t.Fatal("expected error for empty database password, got nil")
	}
}

func TestDependencyTimeoutEnvOverride(t *testing.T) {
	ctx := context.Background()
	t.Setenv("STARTUP_DEPENDENCY_TIMEOUT", "5")

	if got := dependencyTimeout(ctx); got != 5*time.Second {
		t.Fatalf("expected %v, got %v", 5*time.Second, got)
	}
}

func TestDatabaseTimeZoneExtra(t *testing.T) {
	ctx := context.Background()
	t.Setenv("DASHBOARD_TIMEZONE", "Asia/Shanghai")

	extra, err := databaseTimeZoneExtra(ctx)
	if err != nil {
		t.Fatalf("databaseTimeZoneExtra(Asia/Shanghai) 意外错误: %v", err)
	}
	if extra != "time_zone=%27%2B08%3A00%27" {
		t.Fatalf("databaseTimeZoneExtra = %q, want time_zone=%%27%%2B08%%3A00%%27", extra)
	}
}

// TestDatabaseTimeZoneExtraRejectsDST 覆盖 CLEAN-002：含 DST 的时区应在启动时被拒绝，
// 避免用固定偏移表达 DST 时区导致跨切换漂移。
func TestDatabaseTimeZoneExtraRejectsDST(t *testing.T) {
	ctx := context.Background()
	t.Setenv("DASHBOARD_TIMEZONE", "America/New_York")

	if _, err := databaseTimeZoneExtra(ctx); err == nil {
		t.Fatal("databaseTimeZoneExtra(America/New_York) 应拒绝 DST 时区，却返回成功")
	}
}

// TestHasDST 验证 DST 判定：Asia/Shanghai 无 DST，America/New_York 含 DST。
func TestHasDST(t *testing.T) {
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("系统缺少 Asia/Shanghai 时区数据: %v", err)
	}
	if hasDST(sh) {
		t.Fatal("Asia/Shanghai 不应判定为含 DST")
	}

	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("系统缺少 America/New_York 时区数据: %v", err)
	}
	if !hasDST(ny) {
		t.Fatal("America/New_York 应判定为含 DST")
	}
}

// TestApplyServerConfigGracefulTimeoutEnvOverride 覆盖 CLEAN-001：
// 优雅关闭 drain 超时的环境变量名必须为 SERVER_GRACEFUL_TIMEOUT（配置键 server.graceful_timeout 蛇形命名，
// FormatEnvKey 才能产出该名）。若键退回 camelCase（server.gracefulTimeout），环境变量名会变成
// SERVER_GRACEFULTIMEOUT，此处的 SERVER_GRACEFUL_TIMEOUT 将被静默忽略、回落默认值 10，本测试失败。
func TestApplyServerConfigGracefulTimeoutEnvOverride(t *testing.T) {
	ctx := context.Background()
	t.Setenv("SERVER_GRACEFUL_TIMEOUT", "42")

	applyServerConfig(ctx)

	if got := g.Server().GetGracefulTimeout(); got != 42 {
		t.Fatalf("expected graceful timeout 42 from SERVER_GRACEFUL_TIMEOUT, got %d", got)
	}
}

func TestApplyRedisConfigEnvOverride(t *testing.T) {
	ctx := context.Background()
	envs := map[string]string{
		"REDIS_DEFAULT_ADDRESS": "redis-host:16379",
		"REDIS_DEFAULT_PASS":    "redis-pass",
		"REDIS_DEFAULT_DB":      "3",
	}
	for k, v := range envs {
		t.Setenv(k, v)
	}

	applyRedisConfig(ctx)

	cfg, ok := gredis.GetConfig(gredis.DefaultGroupName)
	if !ok {
		t.Fatal("expected redis config to be set")
	}
	if cfg.Address != "redis-host:16379" || cfg.Pass != "redis-pass" || cfg.Db != 3 {
		t.Fatalf("unexpected redis config: %+v", cfg)
	}
}
