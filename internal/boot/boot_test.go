package boot

import (
	"context"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/database/gredis"
	"github.com/gogf/gf/v2/os/genv"
)

func TestCfgStringEnvOverride(t *testing.T) {
	ctx := context.Background()
	const envKey = "DATABASE_DEFAULT_HOST"
	if err := genv.Set(envKey, "env-host"); err != nil {
		t.Fatal(err)
	}
	defer genv.Remove(envKey)

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
		if err := genv.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for k := range envs {
			_ = genv.Remove(k)
		}
	}()

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

func TestDependencyTimeoutEnvOverride(t *testing.T) {
	ctx := context.Background()
	if err := genv.Set("STARTUP_DEPENDENCY_TIMEOUT", "5"); err != nil {
		t.Fatal(err)
	}
	defer genv.Remove("STARTUP_DEPENDENCY_TIMEOUT")

	if got := dependencyTimeout(ctx); got != 5*time.Second {
		t.Fatalf("expected %v, got %v", 5*time.Second, got)
	}
}

func TestDatabaseTimeZoneExtra(t *testing.T) {
	ctx := context.Background()
	if err := genv.Set("DASHBOARD_TIMEZONE", "Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	defer genv.Remove("DASHBOARD_TIMEZONE")

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
	if err := genv.Set("DASHBOARD_TIMEZONE", "America/New_York"); err != nil {
		t.Fatal(err)
	}
	defer genv.Remove("DASHBOARD_TIMEZONE")

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

func TestApplyRedisConfigEnvOverride(t *testing.T) {
	ctx := context.Background()
	envs := map[string]string{
		"REDIS_DEFAULT_ADDRESS": "redis-host:16379",
		"REDIS_DEFAULT_PASS":    "redis-pass",
		"REDIS_DEFAULT_DB":      "3",
	}
	for k, v := range envs {
		if err := genv.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for k := range envs {
			_ = genv.Remove(k)
		}
	}()

	applyRedisConfig(ctx)

	cfg, ok := gredis.GetConfig(gredis.DefaultGroupName)
	if !ok {
		t.Fatal("expected redis config to be set")
	}
	if cfg.Address != "redis-host:16379" || cfg.Pass != "redis-pass" || cfg.Db != 3 {
		t.Fatalf("unexpected redis config: %+v", cfg)
	}
}
