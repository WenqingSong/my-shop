package cmd

import (
	"context"
	"strings"
	"testing"
	"time"
)

// prepareMigrationEnv 固定 migrate 子命令读取的全部数据库环境变量，
// 避免开发机/CI 上的同名变量影响断言（t.Setenv 在用例结束后自动恢复）。
func prepareMigrationEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_DEFAULT_TYPE", "mysql")
	t.Setenv("DATABASE_DEFAULT_HOST", "127.0.0.1")
	t.Setenv("DATABASE_DEFAULT_PORT", "1")
	t.Setenv("DATABASE_DEFAULT_USER", "root")
	t.Setenv("DATABASE_DEFAULT_PASS", "cmd-test-password")
	t.Setenv("DATABASE_DEFAULT_NAME", "my_shop")
	t.Setenv("DATABASE_DEFAULT_CHARSET", "utf8mb4")
	t.Setenv("DATABASE_DEFAULT_DEBUG", "false")
	t.Setenv("DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS", "false")
}

// TestMigrateUpRejectsStrictLoopbackBeforeConnecting 覆盖 T2-01 F4 修复：
// migrate up 必须先做配置校验；严格模式下的回环地址必须在连接数据库之前失败。
//
// 端口使用 1（不可达）：如果实现顺序错误（先连接再校验），错误会是 connection refused，
// 而不是严格模式校验失败。
func TestMigrateUpRejectsStrictLoopbackBeforeConnecting(t *testing.T) {
	const sentinel = "cmd-test-password"
	prepareMigrationEnv(t)
	t.Setenv("DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS", "true")
	t.Setenv("DATABASE_DEFAULT_PASS", sentinel)

	started := time.Now()
	err := migrateUp(context.Background(), nil)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("严格模式 + 回环地址时 migrate up 必须失败")
	}
	if !strings.Contains(err.Error(), "严格配置模式") {
		t.Fatalf("错误必须是严格配置校验，而不是连接错误: %v", err)
	}
	if !strings.Contains(err.Error(), "DATABASE_DEFAULT_HOST") {
		t.Fatalf("错误必须指出 DATABASE_DEFAULT_HOST: %v", err)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("错误信息泄露了数据库密码: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("配置校验应在连接之前立即失败，实际耗时 %s", elapsed)
	}
}

// TestMigrateUpRequiresPasswordBeforeConnecting 覆盖「密码缺失」：
// 密码为空的报错必须发生在连接之前，并指出环境变量名。
func TestMigrateUpRequiresPasswordBeforeConnecting(t *testing.T) {
	prepareMigrationEnv(t)
	t.Setenv("DATABASE_DEFAULT_PASS", "")

	err := migrateUp(context.Background(), nil)
	if err == nil {
		t.Fatal("缺少 DATABASE_DEFAULT_PASS 时 migrate up 必须失败")
	}
	if !strings.Contains(err.Error(), "DATABASE_DEFAULT_PASS") {
		t.Fatalf("错误必须指出 DATABASE_DEFAULT_PASS: %v", err)
	}
	if strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("密码校验必须先于数据库连接: %v", err)
	}
}

// TestMigrateVersionAlsoRunsPreflight 覆盖「所有 migrate 子命令共用同一前置校验」：
// 只读的 migrate version 同样不得绕过严格配置校验。
func TestMigrateVersionAlsoRunsPreflight(t *testing.T) {
	prepareMigrationEnv(t)
	t.Setenv("DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS", "true")

	err := migrateVersion(context.Background(), nil)
	if err == nil {
		t.Fatal("migrate version 必须执行同一前置配置校验")
	}
	if !strings.Contains(err.Error(), "严格配置模式") || !strings.Contains(err.Error(), "DATABASE_DEFAULT_HOST") {
		t.Fatalf("错误必须是严格配置校验: %v", err)
	}
}

// TestMigrateUpAllowsLocalDefaults 覆盖「本地开发体验不变」：
// 严格模式关闭时，允许使用 config.yaml/环境变量里的本机地址；
// 由于端口不可达，此时错误应来自连接阶段（证明配置校验已通过）。
func TestMigrateUpAllowsLocalDefaults(t *testing.T) {
	prepareMigrationEnv(t)

	err := migrateUp(context.Background(), nil)
	if err == nil {
		t.Fatal("数据库不可达时 migrate up 必须失败")
	}
	if strings.Contains(err.Error(), "严格配置模式") {
		t.Fatalf("严格模式关闭时不应做外部地址校验: %v", err)
	}
	if !strings.Contains(err.Error(), "connect") && !strings.Contains(err.Error(), "打开数据库连接") {
		t.Fatalf("错误应来自数据库连接阶段: %v", err)
	}
}
