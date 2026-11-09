package config

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// priorityYAML 覆盖三类带下划线的关键路径，是「GetEffective 归一化」问题的回归样本。
const priorityYAML = `
server:
  address: ":9000"
lifecycle:
  pre_stop_delay: "7s"
readiness:
  cache_ttl: 9
dashboard:
  timezone: "UTC"
order:
  pay_timeout: 1200
flash_sale:
  metrics:
    enabled: true
  rate_limit:
    enabled: true
    user:
      max_requests: 12
  kafka:
    enabled: true
    brokers: "file-broker:9092"
qiniu:
  allowed_extensions: ["bmp", "tiff"]
`

// TestPriorityEnvironmentOverridesFileAndDefault 覆盖「环境变量 + YAML + 默认值同时存在」。
func TestPriorityEnvironmentOverridesFileAndDefault(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, priorityYAML)
	t.Setenv("SERVER_ADDRESS", ":7000")

	got, err := loader.String(context.Background(), KeyServerAddress)
	if err != nil {
		t.Fatalf("读取 server.address: %v", err)
	}
	if got != ":7000" {
		t.Fatalf("环境变量应优先于 YAML，期望 %q，实际 %q", ":7000", got)
	}
}

// TestPriorityFileOverridesDefault 覆盖「只有 YAML + 默认值」。
func TestPriorityFileOverridesDefault(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, priorityYAML)

	got, err := loader.String(context.Background(), KeyDashboardTimezone)
	if err != nil {
		t.Fatalf("读取 dashboard.timezone: %v", err)
	}
	if got != "UTC" {
		t.Fatalf("YAML 应优先于默认值 Asia/Shanghai，实际 %q", got)
	}
}

// TestPriorityDefaultWhenAbsent 覆盖「只有默认值」：YAML 中没有该路径。
func TestPriorityDefaultWhenAbsent(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, "server:\n  address: \":9000\"\n")

	got, err := loader.String(context.Background(), KeyDashboardTimezone)
	if err != nil {
		t.Fatalf("读取 dashboard.timezone: %v", err)
	}
	if got != "Asia/Shanghai" {
		t.Fatalf("应回退代码默认值 Asia/Shanghai，实际 %q", got)
	}
}

// TestBoolEnvironmentOverridesFile 验证 bool 类型的环境变量覆盖。
func TestBoolEnvironmentOverridesFile(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, priorityYAML)

	fromFile, err := loader.Bool(context.Background(), KeyFlashSaleMetricsEnabled)
	if err != nil {
		t.Fatalf("读取 flash_sale.metrics.enabled: %v", err)
	}
	if !fromFile {
		t.Fatal("YAML 中 flash_sale.metrics.enabled=true 应被读到（下划线路径未被归一化）")
	}

	t.Setenv("FLASH_SALE_METRICS_ENABLED", "false")
	overridden, err := loader.Bool(context.Background(), KeyFlashSaleMetricsEnabled)
	if err != nil {
		t.Fatalf("读取 flash_sale.metrics.enabled: %v", err)
	}
	if overridden {
		t.Fatal("FLASH_SALE_METRICS_ENABLED=false 应覆盖 YAML 的 true")
	}
}

// TestDurationEnvironmentOverridesFile 验证 duration 类型的环境变量覆盖与语义保持。
func TestDurationEnvironmentOverridesFile(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, priorityYAML)

	fromFile, err := loader.Duration(context.Background(), KeyLifecyclePreStopDelay)
	if err != nil {
		t.Fatalf("读取 lifecycle.pre_stop_delay: %v", err)
	}
	if fromFile != 7*time.Second {
		t.Fatalf("YAML 7s 应解析为 7s，实际 %s", fromFile)
	}

	t.Setenv("LIFECYCLE_PRE_STOP_DELAY", "9s")
	overridden, err := loader.Duration(context.Background(), KeyLifecyclePreStopDelay)
	if err != nil {
		t.Fatalf("读取 lifecycle.pre_stop_delay: %v", err)
	}
	if overridden != 9*time.Second {
		t.Fatalf("环境变量 9s 应覆盖 YAML 7s，实际 %s", overridden)
	}
}

// TestInvalidEnvironmentValuesFail 验证非法 bool/int/int64/duration 均返回明确错误，
// 不静默回退默认值，且错误含配置项与环境变量名。
func TestInvalidEnvironmentValuesFail(t *testing.T) {
	cases := []struct {
		name  string
		env   string
		value string
		key   Key
		read  func(*Loader, context.Context, Key) error
	}{
		{
			name: "invalid bool", env: "FLASH_SALE_METRICS_ENABLED", value: "ture",
			key: KeyFlashSaleMetricsEnabled,
			read: func(l *Loader, ctx context.Context, k Key) error {
				_, err := l.Bool(ctx, k)
				return err
			},
		},
		{
			name: "invalid int", env: "READINESS_CACHE_TTL", value: "3s",
			key: KeyReadinessCacheTTL,
			read: func(l *Loader, ctx context.Context, k Key) error {
				_, err := l.Int(ctx, k)
				return err
			},
		},
		{
			name: "invalid int64", env: "ORDER_PAY_TIMEOUT", value: "9e3",
			key: KeyOrderPayTimeout,
			read: func(l *Loader, ctx context.Context, k Key) error {
				_, err := l.Int64(ctx, k)
				return err
			},
		},
		{
			name: "invalid duration", env: "LIFECYCLE_PRE_STOP_DELAY", value: "5",
			key: KeyLifecyclePreStopDelay,
			read: func(l *Loader, ctx context.Context, k Key) error {
				_, err := l.Duration(ctx, k)
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateRegisteredEnv(t)
			loader := newTestLoader(t, "server:\n  address: \":9000\"\n")
			t.Setenv(tc.env, tc.value)

			err := tc.read(loader, context.Background(), tc.key)
			if err == nil {
				t.Fatalf("%s=%q 必须报错，不能静默回退默认值", tc.env, tc.value)
			}
			if !strings.Contains(err.Error(), string(tc.key)) {
				t.Fatalf("错误信息应包含配置项 %s，实际: %v", tc.key, err)
			}
			if !strings.Contains(err.Error(), tc.env) {
				t.Fatalf("错误信息应包含环境变量 %s，实际: %v", tc.env, err)
			}
		})
	}
}

// TestEmptyEnvironmentIsExplicitlyPresent 验证「环境变量为空字符串」按显式存在处理：
// 字符串类型返回空串（不回退默认值），数值/布尔/duration 类型报错（不静默回退）。
func TestEmptyEnvironmentIsExplicitlyPresent(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, priorityYAML)
	ctx := context.Background()

	t.Setenv("DASHBOARD_TIMEZONE", "")
	got, err := loader.String(ctx, KeyDashboardTimezone)
	if err != nil {
		t.Fatalf("空字符串对 string 类型是合法值: %v", err)
	}
	if got != "" {
		t.Fatalf("空环境变量应覆盖 YAML 的 UTC 并返回空串，实际 %q", got)
	}

	t.Setenv("READINESS_CACHE_TTL", "")
	if _, err := loader.Int(ctx, KeyReadinessCacheTTL); err == nil {
		t.Fatal("空环境变量对 int 类型必须报错，不能被当成未设置")
	}

	t.Setenv("FLASH_SALE_METRICS_ENABLED", "")
	if _, err := loader.Bool(ctx, KeyFlashSaleMetricsEnabled); err == nil {
		t.Fatal("空环境变量对 bool 类型必须报错")
	}

	t.Setenv("LIFECYCLE_PRE_STOP_DELAY", "")
	if _, err := loader.Duration(ctx, KeyLifecyclePreStopDelay); err == nil {
		t.Fatal("空环境变量对 duration 类型必须报错")
	}
}

// TestStringListFromFileAndEnvironment 验证列表类型：YAML 数组按元素取值、环境变量按逗号分隔。
func TestStringListFromFileAndEnvironment(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, priorityYAML)
	ctx := context.Background()

	fromFile, err := loader.Strings(ctx, KeyQiniuAllowedExtensions)
	if err != nil {
		t.Fatalf("读取 qiniu.allowed_extensions: %v", err)
	}
	if len(fromFile) != 2 || fromFile[0] != "bmp" || fromFile[1] != "tiff" {
		t.Fatalf("YAML 数组应解析为 [bmp tiff]，实际 %#v", fromFile)
	}

	t.Setenv("QINIU_ALLOWED_EXTENSIONS", " webp , png ,, ")
	fromEnv, err := loader.Strings(ctx, KeyQiniuAllowedExtensions)
	if err != nil {
		t.Fatalf("读取 qiniu.allowed_extensions: %v", err)
	}
	if len(fromEnv) != 2 || fromEnv[0] != "webp" || fromEnv[1] != "png" {
		t.Fatalf("环境变量应按逗号分隔并 trim/丢弃空项，实际 %#v", fromEnv)
	}
}

// TestResolveReportsSource 验证 Resolve 能指出实际来源，便于排查「配置没生效」。
func TestResolveReportsSource(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, priorityYAML)
	ctx := context.Background()

	fileValue, err := loader.Resolve(ctx, KeyDashboardTimezone)
	if err != nil {
		t.Fatalf("解析 dashboard.timezone: %v", err)
	}
	if fileValue.Source != SourceFile || fileValue.Text != "UTC" {
		t.Fatalf("期望来源 file / UTC，实际 %s / %q", fileValue.Source, fileValue.Text)
	}

	defaultValue, err := loader.Resolve(ctx, KeyServerGracefulTimeout)
	if err != nil {
		t.Fatalf("解析 server.graceful_timeout: %v", err)
	}
	if defaultValue.Source != SourceDefault {
		t.Fatalf("期望来源 default，实际 %s", defaultValue.Source)
	}

	t.Setenv("SERVER_ADDRESS", ":7000")
	envValue, err := loader.Resolve(ctx, KeyServerAddress)
	if err != nil {
		t.Fatalf("解析 server.address: %v", err)
	}
	if envValue.Source != SourceEnv {
		t.Fatalf("期望来源 env，实际 %s", envValue.Source)
	}
}

// TestUnregisteredKeyFails 验证未登记到契约的路径直接报错，
// 防止业务代码绕过注册表读取「无环境变量名、无默认值」的裸配置。
func TestUnregisteredKeyFails(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, "some:\n  unknown: key\n")

	if _, err := loader.String(context.Background(), Key("some.unknown")); err == nil {
		t.Fatal("未登记的配置项必须报错")
	}
}

// TestValidateAggregatesInvalidItems 验证启动期整表校验会一次性汇总全部非法项。
func TestValidateAggregatesInvalidItems(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, "server:\n  address: \":9000\"\n")
	t.Setenv("FLASH_SALE_METRICS_ENABLED", "ture")
	t.Setenv("LIFECYCLE_PRE_STOP_DELAY", "5")

	err := loader.Validate(context.Background())
	if err == nil {
		t.Fatal("存在非法配置时 Validate 必须报错")
	}
	message := err.Error()
	for _, fragment := range []string{"flash_sale.metrics.enabled", "lifecycle.pre_stop_delay", "2 项"} {
		if !strings.Contains(message, fragment) {
			t.Fatalf("校验错误应包含 %q，实际: %v", fragment, err)
		}
	}
}

// TestValidatePassesOnBaselineConfig 验证仓库自带 config.yaml 能通过整表校验
// （即所有登记项在默认文件中都是合法的）。
func TestValidatePassesOnBaselineConfig(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newRepositoryLoader(t)

	if err := loader.Validate(context.Background()); err != nil {
		t.Fatalf("仓库 config.yaml 应通过整表校验: %v", err)
	}
}

// TestSensitiveValueNeverRendered 验证敏感配置的错误信息不输出真实取值。
func TestSensitiveValueNeverRendered(t *testing.T) {
	spec := MustSpecOf(KeyDatabaseDefaultPass)
	if !spec.Sensitive {
		t.Fatal("database.default.pass 必须标记为敏感配置")
	}
	value := Value{Spec: spec, Source: SourceEnv, Text: "super-secret-pass"}
	if rendered := value.displayValue(); strings.Contains(rendered, "super-secret-pass") {
		t.Fatalf("敏感值不得出现在错误信息中，实际: %s", rendered)
	}
}

// TestNewLoaderRejectsNilConfig 验证构造读取器时拒绝空配置对象。
func TestNewLoaderRejectsNilConfig(t *testing.T) {
	if _, err := NewLoader(nil, nil); err == nil {
		t.Fatal("nil 配置对象必须报错")
	}
}

// TestMustSpecOfPanicsOnUnknownKey 验证未登记键在 MustSpecOf 上 panic（编码错误尽早暴露）。
func TestMustSpecOfPanicsOnUnknownKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("未登记配置项应 panic")
		}
	}()
	MustSpecOf(Key("not.registered"))
}

// TestErrorsAreNotWrappedSilently 保证错误链可被 errors.Is/As 正常判断（不被吞掉）。
func TestErrorsAreNotWrappedSilently(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, "")
	t.Setenv("READINESS_CACHE_TTL", "abc")

	_, err := loader.Int(context.Background(), KeyReadinessCacheTTL)
	if err == nil {
		t.Fatal("非法整数必须报错")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("错误链不应被误判")
	}
}
