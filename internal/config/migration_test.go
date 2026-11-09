package config

import (
	"context"
	"strings"
	"testing"
)

// migrationYAML 是本地开发形态的配置：严格模式关闭，数据库与 Redis 都是本机默认值。
const migrationYAML = `
deployment:
  require_external_endpoints: false
database:
  default:
    type: "mysql"
    host: "127.0.0.1"
    port: "3306"
    user: "root"
    pass: ""
    name: "my_shop"
    charset: "utf8mb4"
    debug: false
redis:
  default:
    address: "127.0.0.1:6379"
`

// setMigrationEnv 设置 migration 校验需要的数据库环境变量；空字符串表示不设置。
func setMigrationEnv(t *testing.T, host, name, pass string) {
	t.Helper()
	if host != "" {
		t.Setenv("DATABASE_DEFAULT_HOST", host)
	}
	if name != "" {
		t.Setenv("DATABASE_DEFAULT_NAME", name)
	}
	if pass != "" {
		t.Setenv("DATABASE_DEFAULT_PASS", pass)
	}
}

// TestValidateMigrationAllowsLocalDefaults 覆盖「本地模式」：
// 严格模式关闭时，migration 与 serve 一样可以使用 config.yaml 的本机默认地址与库名，
// 只需显式注入密码（密码无内置默认值）。
func TestValidateMigrationAllowsLocalDefaults(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, migrationYAML)
	t.Setenv("DATABASE_DEFAULT_PASS", "local-password")

	if err := loader.ValidateMigration(context.Background()); err != nil {
		t.Fatalf("本地模式（严格模式关闭）应通过 migration 校验: %v", err)
	}
}

// TestValidateMigrationRequiresPassword 覆盖「数据库凭据错误/缺失」的配置侧：
// 密码缺失必须在连接之前失败，且错误信息只指出环境变量名。
func TestValidateMigrationRequiresPassword(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, migrationYAML)

	err := loader.ValidateMigration(context.Background())
	if err == nil {
		t.Fatal("缺少 DATABASE_DEFAULT_PASS 时 migration 校验必须失败")
	}
	if !strings.Contains(err.Error(), "DATABASE_DEFAULT_PASS") {
		t.Fatalf("错误信息必须指出 DATABASE_DEFAULT_PASS，实际: %v", err)
	}
}

// TestValidateMigrationRejectsEmptyEnvironmentPassword 验证「已设置但为空」的密码不被当作未设置。
func TestValidateMigrationRejectsEmptyEnvironmentPassword(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, migrationYAML)
	t.Setenv("DATABASE_DEFAULT_PASS", "")

	err := loader.ValidateMigration(context.Background())
	if err == nil {
		t.Fatal("空字符串 DATABASE_DEFAULT_PASS 必须失败（空值不视为未设置）")
	}
	if !strings.Contains(err.Error(), "DATABASE_DEFAULT_PASS") {
		t.Fatalf("错误信息必须指出 DATABASE_DEFAULT_PASS，实际: %v", err)
	}
}

// TestValidateMigrationStrictRequiresDatabaseEnv 覆盖「缺少 DATABASE_DEFAULT_HOST」：
// 严格模式打开后，YAML 中的 127.0.0.1 不得绕过环境变量要求。
func TestValidateMigrationStrictRequiresDatabaseEnv(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, migrationYAML)
	t.Setenv("DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS", "true")
	t.Setenv("DATABASE_DEFAULT_PASS", "strict-password")

	err := loader.ValidateMigration(context.Background())
	if err == nil {
		t.Fatal("严格模式缺少 DATABASE_DEFAULT_HOST/DATABASE_DEFAULT_NAME 必须失败")
	}
	for _, env := range []string{"DATABASE_DEFAULT_HOST", "DATABASE_DEFAULT_NAME"} {
		if !strings.Contains(err.Error(), env) {
			t.Fatalf("错误信息应包含缺失的环境变量 %s，实际: %v", env, err)
		}
	}
}

// TestValidateMigrationStrictRejectsLoopback 覆盖 localhost / 127.0.0.1 / ::1 等回环地址。
func TestValidateMigrationStrictRejectsLoopback(t *testing.T) {
	cases := []string{"localhost", "127.0.0.1", "127.1.2.3", "::1", "db.localhost"}
	for _, host := range cases {
		t.Run(host, func(t *testing.T) {
			isolateRegisteredEnv(t)
			loader := newTestLoader(t, migrationYAML)
			t.Setenv("DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS", "true")
			setMigrationEnv(t, host, "my_shop", "strict-password")

			err := loader.ValidateMigration(context.Background())
			if err == nil {
				t.Fatalf("严格模式必须拒绝回环地址 %q", host)
			}
			if !strings.Contains(err.Error(), "DATABASE_DEFAULT_HOST") {
				t.Fatalf("错误信息应指出 DATABASE_DEFAULT_HOST，实际: %v", err)
			}
			if !strings.Contains(err.Error(), "回环") {
				t.Fatalf("错误信息应说明回环地址被拒绝，实际: %v", err)
			}
		})
	}
}

// TestValidateMigrationStrictAcceptsExternalWithoutRedis 覆盖「外部数据库地址」：
// migration 只要求数据库两项，不要求 REDIS_DEFAULT_ADDRESS（migration 不连接 Redis）。
func TestValidateMigrationStrictAcceptsExternalWithoutRedis(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, migrationYAML)
	t.Setenv("DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS", "true")
	setMigrationEnv(t, "mysql.default.svc.cluster.local", "my_shop", "strict-password")

	if err := loader.ValidateMigration(context.Background()); err != nil {
		t.Fatalf("提供外部数据库地址且未提供 Redis 地址时 migration 应通过校验: %v", err)
	}

	// 同一组配置下，应用严格模式仍要求 Redis 地址：证明 migration 的「少要求」
	// 没有削弱 T2-01 冻结的应用部署严格模式。
	err := loader.ValidateExternalEndpoints(context.Background())
	if err == nil {
		t.Fatal("应用严格模式必须仍然要求 REDIS_DEFAULT_ADDRESS")
	}
	if !strings.Contains(err.Error(), "REDIS_DEFAULT_ADDRESS") {
		t.Fatalf("应用严格模式错误信息应指出 REDIS_DEFAULT_ADDRESS，实际: %v", err)
	}
}

// TestValidateMigrationErrorNeverLeaksPassword 验证校验失败信息不含数据库密码。
func TestValidateMigrationErrorNeverLeaksPassword(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, migrationYAML)
	t.Setenv("DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS", "true")
	setMigrationEnv(t, "", "", "sentinel-migration-password")

	err := loader.ValidateMigration(context.Background())
	if err == nil {
		t.Fatal("严格模式缺少数据库地址时必须失败")
	}
	if strings.Contains(err.Error(), "sentinel-migration-password") {
		t.Fatalf("migration 校验错误泄露了数据库密码: %v", err)
	}
}

// TestValidateMigrationRejectsInvalidType 验证非法布尔值不会被静默回退（与统一配置契约一致）。
func TestValidateMigrationRejectsInvalidType(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, migrationYAML)
	t.Setenv("DATABASE_DEFAULT_PASS", "local-password")
	t.Setenv("DATABASE_DEFAULT_DEBUG", "not-a-bool")

	err := loader.ValidateMigration(context.Background())
	if err == nil {
		t.Fatal("非法 DATABASE_DEFAULT_DEBUG 必须使 migration 校验失败")
	}
	if !strings.Contains(err.Error(), "DATABASE_DEFAULT_DEBUG") {
		t.Fatalf("错误信息必须指出 DATABASE_DEFAULT_DEBUG，实际: %v", err)
	}
}

// TestValidateMigrationReadsRepositoryDefaults 验证仓库自带 config.yaml 与注册表一致：
// 在只注入密码的前提下，本地模式可以直接通过 migration 校验。
func TestValidateMigrationReadsRepositoryDefaults(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newRepositoryLoader(t)
	t.Setenv("DATABASE_DEFAULT_PASS", "local-password")

	if err := loader.ValidateMigration(context.Background()); err != nil {
		t.Fatalf("仓库默认配置在注入密码后应通过 migration 校验: %v", err)
	}
}
