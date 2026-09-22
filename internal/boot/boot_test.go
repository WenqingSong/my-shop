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
