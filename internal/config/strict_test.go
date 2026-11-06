package config

import (
	"context"
	"strings"
	"testing"
)

// strictOnYAML 开启严格模式，并在 YAML 中放入 localhost 默认值：
// 严格模式必须无视这些 YAML 值，只认环境变量。
const strictOnYAML = `
deployment:
  require_external_endpoints: true
database:
  default:
    host: "127.0.0.1"
    name: "my_shop"
    pass: ""
redis:
  default:
    address: "127.0.0.1:6379"
`

// strictOffYAML 是本地开发形态：严格模式关闭，YAML 使用本机默认值。
const strictOffYAML = `
deployment:
  require_external_endpoints: false
database:
  default:
    host: "127.0.0.1"
    name: "my_shop"
redis:
  default:
    address: "127.0.0.1:6379"
`

// setStrictEnv 设置严格模式要求的三项环境变量；空串表示「调用方不设置」。
func setStrictEnv(t *testing.T, dbHost, dbName, redisAddress string) {
	t.Helper()
	if dbHost != "" {
		t.Setenv("DATABASE_DEFAULT_HOST", dbHost)
	}
	if dbName != "" {
		t.Setenv("DATABASE_DEFAULT_NAME", dbName)
	}
	if redisAddress != "" {
		t.Setenv("REDIS_DEFAULT_ADDRESS", redisAddress)
	}
}

// TestStrictModeDisabledAllowsLocalDefaults 覆盖场景 1：
// 严格模式关闭时，YAML/代码默认值中的本机地址仍可正常使用（保持本地开发兼容）。
func TestStrictModeDisabledAllowsLocalDefaults(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, strictOffYAML)

	required, err := loader.RequireExternalEndpoints(context.Background())
	if err != nil {
		t.Fatalf("读取严格模式开关: %v", err)
	}
	if required {
		t.Fatal("deployment.require_external_endpoints=false 应关闭严格模式")
	}
	if err := loader.ValidateExternalEndpoints(context.Background()); err != nil {
		t.Fatalf("严格模式关闭时不应校验外部地址: %v", err)
	}
}

// TestStrictModeMissingDatabaseHostFails 覆盖场景 2：缺少 DATABASE_DEFAULT_HOST 启动前失败。
func TestStrictModeMissingDatabaseHostFails(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, strictOnYAML)
	setStrictEnv(t, "", "my_shop", "redis.cluster.local:6379")

	err := loader.ValidateExternalEndpoints(context.Background())
	if err == nil {
		t.Fatal("严格模式缺少 DATABASE_DEFAULT_HOST 必须失败")
	}
	if !strings.Contains(err.Error(), "DATABASE_DEFAULT_HOST") {
		t.Fatalf("错误信息必须指出缺失的环境变量名，实际: %v", err)
	}
}

// TestStrictModeMissingDatabaseNameFails 覆盖场景 3：缺少 DATABASE_DEFAULT_NAME 启动前失败。
func TestStrictModeMissingDatabaseNameFails(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, strictOnYAML)
	setStrictEnv(t, "mysql.cluster.local", "", "redis.cluster.local:6379")

	err := loader.ValidateExternalEndpoints(context.Background())
	if err == nil {
		t.Fatal("严格模式缺少 DATABASE_DEFAULT_NAME 必须失败")
	}
	if !strings.Contains(err.Error(), "DATABASE_DEFAULT_NAME") {
		t.Fatalf("错误信息必须指出缺失的环境变量名，实际: %v", err)
	}
}

// TestStrictModeMissingRedisAddressFails 覆盖场景 4：缺少 REDIS_DEFAULT_ADDRESS 启动前失败。
func TestStrictModeMissingRedisAddressFails(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, strictOnYAML)
	setStrictEnv(t, "mysql.cluster.local", "my_shop", "")

	err := loader.ValidateExternalEndpoints(context.Background())
	if err == nil {
		t.Fatal("严格模式缺少 REDIS_DEFAULT_ADDRESS 必须失败")
	}
	if !strings.Contains(err.Error(), "REDIS_DEFAULT_ADDRESS") {
		t.Fatalf("错误信息必须指出缺失的环境变量名，实际: %v", err)
	}
}

// TestStrictModeIgnoresYAMLLocalhost 覆盖场景 5：
// 即使 YAML 里存在 localhost，也不能绕过「必须由环境变量显式提供」的要求。
func TestStrictModeIgnoresYAMLLocalhost(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, strictOnYAML)

	err := loader.ValidateExternalEndpoints(context.Background())
	if err == nil {
		t.Fatal("YAML 中的 127.0.0.1 不得绕过严格模式的环境变量要求")
	}
	for _, env := range []string{"DATABASE_DEFAULT_HOST", "DATABASE_DEFAULT_NAME", "REDIS_DEFAULT_ADDRESS"} {
		if !strings.Contains(err.Error(), env) {
			t.Fatalf("错误信息应包含缺失的环境变量 %s，实际: %v", env, err)
		}
	}
}

// TestStrictModeAcceptsExternalEndpoints 覆盖场景 6：显式提供有效外部地址时通过。
func TestStrictModeAcceptsExternalEndpoints(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, strictOnYAML)
	setStrictEnv(t, "mysql.default.svc.cluster.local", "my_shop", "redis.default.svc.cluster.local:6379")

	if err := loader.ValidateExternalEndpoints(context.Background()); err != nil {
		t.Fatalf("有效外部地址应通过严格模式校验: %v", err)
	}
}

// TestStrictModeRejectsLoopbackEndpoints 覆盖场景 7：显式提供的回环地址必须被拒绝。
func TestStrictModeRejectsLoopbackEndpoints(t *testing.T) {
	cases := []struct {
		name         string
		dbHost       string
		redisAddress string
		wantEnv      string
	}{
		{"ipv4 loopback host", "127.0.0.1", "redis.cluster.local:6379", "DATABASE_DEFAULT_HOST"},
		{"localhost host", "localhost", "redis.cluster.local:6379", "DATABASE_DEFAULT_HOST"},
		{"ipv6 loopback host", "::1", "redis.cluster.local:6379", "DATABASE_DEFAULT_HOST"},
		{"ipv4 loopback redis", "mysql.cluster.local", "127.0.0.1:6379", "REDIS_DEFAULT_ADDRESS"},
		{"localhost redis", "mysql.cluster.local", "localhost:6379", "REDIS_DEFAULT_ADDRESS"},
		{"ipv6 loopback redis", "mysql.cluster.local", "[::1]:6379", "REDIS_DEFAULT_ADDRESS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateRegisteredEnv(t)
			loader := newTestLoader(t, strictOnYAML)
			setStrictEnv(t, tc.dbHost, "my_shop", tc.redisAddress)

			err := loader.ValidateExternalEndpoints(context.Background())
			if err == nil {
				t.Fatalf("回环地址 %q / %q 必须被拒绝", tc.dbHost, tc.redisAddress)
			}
			if !strings.Contains(err.Error(), tc.wantEnv) {
				t.Fatalf("错误信息应指出 %s，实际: %v", tc.wantEnv, err)
			}
		})
	}
}

// TestStrictModeRejectsEmptyEnvironmentValue 验证空字符串环境变量同样失败（禁止静默回退）。
func TestStrictModeRejectsEmptyEnvironmentValue(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, strictOnYAML)
	t.Setenv("DATABASE_DEFAULT_HOST", "")
	t.Setenv("DATABASE_DEFAULT_NAME", "my_shop")
	t.Setenv("REDIS_DEFAULT_ADDRESS", "redis.cluster.local:6379")

	err := loader.ValidateExternalEndpoints(context.Background())
	if err == nil {
		t.Fatal("空字符串 DATABASE_DEFAULT_HOST 必须失败")
	}
	if !strings.Contains(err.Error(), "DATABASE_DEFAULT_HOST") || !strings.Contains(err.Error(), "不得为空") {
		t.Fatalf("错误信息应指出空环境变量，实际: %v", err)
	}
}

// TestStrictModeErrorNeverLeaksCredentials 验证严格模式错误信息不含密码等凭据。
func TestStrictModeErrorNeverLeaksCredentials(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, strictOnYAML)
	t.Setenv("DATABASE_DEFAULT_PASS", "sentinel-db-password")
	t.Setenv("REDIS_DEFAULT_PASS", "sentinel-redis-password")

	err := loader.ValidateExternalEndpoints(context.Background())
	if err == nil {
		t.Fatal("缺少三项环境变量时必须失败")
	}
	for _, secret := range []string{"sentinel-db-password", "sentinel-redis-password"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("严格模式错误信息泄露了凭据 %q: %v", secret, err)
		}
	}
}

// TestStrictModeSwitchReadsEnvironment 验证开关本身可被环境变量覆盖
// （Kubernetes ConfigMap 通过 DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS 打开严格模式）。
func TestStrictModeSwitchReadsEnvironment(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, strictOffYAML)
	t.Setenv("DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS", "true")

	required, err := loader.RequireExternalEndpoints(context.Background())
	if err != nil {
		t.Fatalf("读取严格模式开关: %v", err)
	}
	if !required {
		t.Fatal("DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true 应打开严格模式")
	}
	if err := loader.ValidateExternalEndpoints(context.Background()); err == nil {
		t.Fatal("开关被环境变量打开后，缺少外部地址必须失败")
	}
}

// TestIsLoopbackHost 覆盖回环判定边界。
func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"localhost":         true,
		"LOCALHOST":         true,
		"api.localhost":     true,
		"127.0.0.1":         true,
		"127.1.2.3":         true,
		"::1":               true,
		"[::1]":             true,
		"mysql":             false,
		"db.cluster.local":  false,
		"10.0.0.5":          false,
		"0.0.0.0":           false,
		"redis.external.io": false,
	}
	for host, want := range cases {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v，期望 %v", host, got, want)
		}
	}
}

// TestHostOnly 覆盖 host:port 与纯主机名的解析。
func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:6379": "127.0.0.1",
		"[::1]:6379":     "::1",
		"redis.io:16379": "redis.io",
		"127.0.0.1":      "127.0.0.1",
		"mysql.internal": "mysql.internal",
	}
	for input, want := range cases {
		if got := hostOnly(input); got != want {
			t.Errorf("hostOnly(%q) = %q，期望 %q", input, got, want)
		}
	}
}
