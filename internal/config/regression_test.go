package config

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// underscoreYAML 用「与代码默认值全部不同」的哨兵值覆盖所有曾经被 GoFrame 键归一化
// 破坏的下划线配置路径。只有真正读到 YAML 才能得到这些值：一旦某条路径被归一化成
// 点号（flash_sale → flash.sale、graceful_timeout → graceful.timeout、cache_ttl → cache.ttl），
// 断言会立刻失败。
const underscoreYAML = `
server:
  address: ":9100"
  graceful_timeout: 11
lifecycle:
  pre_stop_delay: "6s"
  termination_grace_period: "61s"
readiness:
  probe_timeout: 2
  cache_ttl: 4
order:
  pay_timeout: 901
  cancel_scan_interval: 61
dashboard:
  timezone: "UTC"
flash_sale:
  dispatch_mode: "kafka"
  shutdown_timeout: "31s"
  reconcile_scan_interval: 62
  consume_scan_interval: 3
  rate_limit:
    enabled: true
    user:
      max_requests: 13
      window_seconds: 2
    activity:
      max_requests: 14
      window_seconds: 3
  queue_capacity:
    enabled: true
    max_queued_per_sku: 15
    counter_ttl_seconds: 86401
  circuit_breaker:
    enabled: true
    failure_threshold: 6
    open_timeout_seconds: 11
    half_open_max_probes: 2
  metrics:
    enabled: true
  kafka:
    enabled: true
    brokers: "file-broker:9092"
    topic: "file-topic"
    dlq_topic: "file-dlq"
    consumer_group: "file-group"
    publisher:
      batch_size: 101
      poll_interval: "201ms"
      error_backoff: "3s"
`

// TestUnderscorepathsReadFromFile 是「GoFrame GetEffective 键归一化」的专项回归测试。
//
// 覆盖：flash_sale.metrics / rate_limit / queue_capacity / circuit_breaker、
// readiness.*、server.graceful_timeout、order.cancel_scan_interval、order.pay_timeout、
// flash_sale.consume_scan_interval / reconcile_scan_interval、dashboard.timezone、
// Kafka 与 Runtime、Lifecycle。
func TestUnderscorePathsReadFromFile(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, underscoreYAML)

	cases := []struct {
		key  Key
		want string
	}{
		{KeyServerAddress, ":9100"},
		{KeyServerGracefulTimeout, "11"},
		{KeyLifecyclePreStopDelay, "6s"},
		{KeyLifecycleTerminationGracePeriod, "1m1s"},
		{KeyReadinessProbeTimeout, "2"},
		{KeyReadinessCacheTTL, "4"},
		{KeyOrderPayTimeout, "901"},
		{KeyOrderCancelScanInterval, "61"},
		{KeyDashboardTimezone, "UTC"},

		{KeyFlashSaleDispatchMode, "kafka"},
		{KeyFlashSaleShutdownTimeout, "31s"},
		{KeyFlashSaleReconcileScanInterval, "62"},
		{KeyFlashSaleConsumeScanInterval, "3"},

		{KeyFlashSaleRateLimitEnabled, "true"},
		{KeyFlashSaleRateLimitUserMaxRequests, "13"},
		{KeyFlashSaleRateLimitUserWindowSeconds, "2"},
		{KeyFlashSaleRateLimitActivityMaxRequests, "14"},
		{KeyFlashSaleRateLimitActivityWindowSeconds, "3"},

		{KeyFlashSaleQueueCapacityEnabled, "true"},
		{KeyFlashSaleQueueCapacityMaxQueuedPerSKU, "15"},
		{KeyFlashSaleQueueCapacityCounterTTL, "86401"},

		{KeyFlashSaleCircuitBreakerEnabled, "true"},
		{KeyFlashSaleCircuitBreakerFailureThreshold, "6"},
		{KeyFlashSaleCircuitBreakerOpenTimeout, "11"},
		{KeyFlashSaleCircuitBreakerHalfOpenProbes, "2"},

		{KeyFlashSaleMetricsEnabled, "true"},

		{KeyFlashSaleKafkaEnabled, "true"},
		{KeyFlashSaleKafkaBrokers, "file-broker:9092"},
		{KeyFlashSaleKafkaTopic, "file-topic"},
		{KeyFlashSaleKafkaDLQTopic, "file-dlq"},
		{KeyFlashSaleKafkaConsumerGroup, "file-group"},
		{KeyFlashSaleKafkaPublisherBatchSize, "101"},
		{KeyFlashSaleKafkaPublisherPollInterval, "201ms"},
		{KeyFlashSaleKafkaPublisherErrorBackoff, "3s"},
	}
	for _, tc := range cases {
		if got := canonicalValue(t, loader, tc.key); got != tc.want {
			t.Errorf("%s 未正确读取 YAML：期望 %q，实际 %q（键可能被归一化成点号）", tc.key, tc.want, got)
		}
	}
}

// TestBaselineEffectiveValuesUnchanged 冻结基线（273d0138）的实际生效值。
//
// 表中的期望值来自基线代码的真实行为：下划线路径在基线里读不到 YAML，落的是代码默认值；
// Kafka / Runtime / Lifecycle 走各自的自定义 lookup，读到的是 config.yaml 的值。
// 迁移到统一配置层后两者必须一致，否则说明业务阈值被静默改变。
func TestBaselineEffectiveValuesUnchanged(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newRepositoryLoader(t)

	cases := []struct {
		key  Key
		want string
	}{
		// server / lifecycle / readiness
		{KeyServerAddress, ":8000"},
		{KeyServerGracefulTimeout, "10"},
		{KeyLifecyclePreStopDelay, "5s"},
		{KeyLifecycleTerminationGracePeriod, "1m0s"},
		{KeyReadinessProbeTimeout, "1"},
		{KeyReadinessCacheTTL, "3"},

		// database / redis
		{KeyDatabaseDefaultType, "mysql"},
		{KeyDatabaseDefaultHost, "127.0.0.1"},
		{KeyDatabaseDefaultPort, "3306"},
		{KeyDatabaseDefaultUser, "root"},
		{KeyDatabaseDefaultPass, ""},
		{KeyDatabaseDefaultName, "my_shop"},
		{KeyDatabaseDefaultCharset, "utf8mb4"},
		{KeyDatabaseDefaultDebug, "false"},
		{KeyRedisDefaultAddress, "127.0.0.1:6379"},
		{KeyRedisDefaultDB, "0"},
		{KeyRedisDefaultPass, ""},

		// auth / admin / legacy / dashboard
		{KeyAuthJWTSecret, ""},
		{KeyAuthSessionTTL, "3600"},
		{KeyAuthRefreshTTL, "2592000"},
		{KeyAdminSuperUsername, "admin"},
		{KeyAdminSuperPassword, ""},
		{KeyLegacyStagingName, "my_shop_staging"},
		{KeyDashboardTimezone, "Asia/Shanghai"},

		// order
		{KeyOrderPayTimeout, "900"},
		{KeyOrderCancelScanInterval, "60"},

		// flash_sale 基础与容量保护
		{KeyFlashSaleDispatchMode, "db_scanner"},
		{KeyFlashSaleShutdownTimeout, "30s"},
		{KeyFlashSaleReconcileScanInterval, "60"},
		{KeyFlashSaleConsumeScanInterval, "1"},
		{KeyFlashSaleRateLimitEnabled, "false"},
		{KeyFlashSaleRateLimitUserMaxRequests, "0"},
		{KeyFlashSaleRateLimitUserWindowSeconds, "1"},
		{KeyFlashSaleRateLimitActivityMaxRequests, "0"},
		{KeyFlashSaleRateLimitActivityWindowSeconds, "1"},
		{KeyFlashSaleQueueCapacityEnabled, "false"},
		{KeyFlashSaleQueueCapacityMaxQueuedPerSKU, "0"},
		{KeyFlashSaleQueueCapacityCounterTTL, "86400"},
		{KeyFlashSaleCircuitBreakerEnabled, "false"},
		{KeyFlashSaleCircuitBreakerFailureThreshold, "5"},
		{KeyFlashSaleCircuitBreakerOpenTimeout, "10"},
		{KeyFlashSaleCircuitBreakerHalfOpenProbes, "1"},
		{KeyFlashSaleMetricsEnabled, "false"},

		// kafka（基线由自定义 lookup 读到 config.yaml 的值）
		{KeyFlashSaleKafkaEnabled, "false"},
		{KeyFlashSaleKafkaBrokers, "127.0.0.1:29092"},
		{KeyFlashSaleKafkaTopic, "surgecart.flashsale.order.requested.v1"},
		{KeyFlashSaleKafkaDLQTopic, "surgecart.flashsale.order.requested.v1.dlq"},
		{KeyFlashSaleKafkaConsumerGroup, "surgecart-flashsale-order-worker-v1"},
		{KeyFlashSaleKafkaPublisherBatchSize, "100"},
		{KeyFlashSaleKafkaPublisherPollInterval, "200ms"},
		{KeyFlashSaleKafkaPublisherErrorBackoff, "2s"},

		// storage / qiniu / startup / deployment
		{KeyStorageLocalRoot, "./storage"},
		{KeyQiniuAccessKey, ""},
		{KeyQiniuSecretKey, ""},
		{KeyQiniuBucket, ""},
		{KeyQiniuDomain, ""},
		{KeyQiniuRegion, "z2"},
		{KeyQiniuTokenTTL, "3600"},
		{KeyQiniuMaxFileSize, "10485760"},
		{KeyQiniuAllowedExtensions, "jpg,jpeg,png,gif,webp"},
		{KeyQiniuAllowedMimeTypes, "image/jpeg,image/png,image/gif,image/webp"},
		{KeyStartupDependencyTimeout, "30"},
		{KeyDeploymentRequireExternalEndpoints, "false"},
	}
	for _, tc := range cases {
		if got := canonicalValue(t, loader, tc.key); got != tc.want {
			t.Errorf("%s 基线生效值发生变化：期望 %q，实际 %q", tc.key, tc.want, got)
		}
	}
}

// canonicalValue 按契约类型读取配置并转成便于比较的稳定文本。
func canonicalValue(t *testing.T, loader *Loader, key Key) string {
	t.Helper()
	ctx := context.Background()
	switch MustSpecOf(key).Kind {
	case KindString:
		value, err := loader.String(ctx, key)
		if err != nil {
			t.Fatalf("读取 %s: %v", key, err)
		}
		return value
	case KindBool:
		value, err := loader.Bool(ctx, key)
		if err != nil {
			t.Fatalf("读取 %s: %v", key, err)
		}
		return strconv.FormatBool(value)
	case KindInt:
		value, err := loader.Int(ctx, key)
		if err != nil {
			t.Fatalf("读取 %s: %v", key, err)
		}
		return strconv.Itoa(value)
	case KindInt64:
		value, err := loader.Int64(ctx, key)
		if err != nil {
			t.Fatalf("读取 %s: %v", key, err)
		}
		return strconv.FormatInt(value, 10)
	case KindDuration:
		value, err := loader.Duration(ctx, key)
		if err != nil {
			t.Fatalf("读取 %s: %v", key, err)
		}
		return value.String()
	case KindStringList:
		value, err := loader.Strings(ctx, key)
		if err != nil {
			t.Fatalf("读取 %s: %v", key, err)
		}
		return strings.Join(value, ",")
	default:
		t.Fatalf("未知配置类型 %q", MustSpecOf(key).Kind)
		return ""
	}
}
