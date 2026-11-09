// Package config 是 SurgeCart 的统一配置访问层。
//
// 读取优先级固定为「环境变量 > YAML 配置文件 > 代码默认值」，并满足三条约束：
//
//  1. 环境变量名必须显式登记在契约注册表（Spec）中，不依赖 GoFrame 的
//     「下划线 → 点号」键推导。GoFrame 的 GetEffective/GetWithEnv 会先用
//     internal/utils.FormatCmdKey 把配置键里的 "_" 替换成 "."，导致
//     flash_sale.metrics.enabled 被读成 flash.sale.metrics.enabled，永远命中不了
//     config.yaml 里的 flash_sale 命名空间（表现为「环境变量与代码默认值生效，
//     唯独 YAML 不生效」）。本包的文件读取直接使用不做归一化的 gcfg.Config.Get。
//  2. 默认值集中在注册表，业务代码不再各自维护一份默认值，避免默认值漂移。
//  3. 类型解析严格：非法 bool/int/int64/duration 返回带配置项与环境变量名的错误，
//     不静默回退默认值；环境变量「已设置但为空」按显式存在性规则处理，不当作不存在。
//
// 敏感配置（数据库/Redis 密码、JWT 密钥、超管密码、七牛 AK/SK）只登记名称与敏感级别，
// 任何错误信息、日志与文档都不输出其真实值。
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kind 是配置项的解析类型。新增类型时必须同时实现 loader.go 中的解析分支。
type Kind string

const (
	// KindString 原样返回字符串（不 trim，保留运维输入语义）。
	KindString Kind = "string"
	// KindBool 使用 strconv.ParseBool 严格解析，非法值报错。
	KindBool Kind = "bool"
	// KindInt 解析为 int。
	KindInt Kind = "int"
	// KindInt64 解析为 int64。
	KindInt64 Kind = "int64"
	// KindDuration 使用 time.ParseDuration 解析（如 200ms/5s/30s），必须大于 0。
	KindDuration Kind = "duration"
	// KindStringList 解析为字符串列表：YAML 数组按元素取值，环境变量按逗号分隔。
	KindStringList Kind = "string_list"
)

// Key 是配置项的编译期标识，取值即 config.yaml 中的点分路径。
type Key string

// 契约注册表中登记的配置项（与 manifest/config/config.yaml 一一对应）。
const (
	// server.*
	KeyServerAddress         Key = "server.address"
	KeyServerGracefulTimeout Key = "server.graceful_timeout"

	// lifecycle.*（T2-03 冻结的容器终止预算）
	KeyLifecyclePreStopDelay           Key = "lifecycle.pre_stop_delay"
	KeyLifecycleTerminationGracePeriod Key = "lifecycle.termination_grace_period"

	// readiness.*
	KeyReadinessProbeTimeout Key = "readiness.probe_timeout"
	KeyReadinessCacheTTL     Key = "readiness.cache_ttl"

	// database.default.*
	KeyDatabaseDefaultType    Key = "database.default.type"
	KeyDatabaseDefaultHost    Key = "database.default.host"
	KeyDatabaseDefaultPort    Key = "database.default.port"
	KeyDatabaseDefaultUser    Key = "database.default.user"
	KeyDatabaseDefaultPass    Key = "database.default.pass"
	KeyDatabaseDefaultName    Key = "database.default.name"
	KeyDatabaseDefaultCharset Key = "database.default.charset"
	KeyDatabaseDefaultDebug   Key = "database.default.debug"

	// redis.default.*
	KeyRedisDefaultAddress Key = "redis.default.address"
	KeyRedisDefaultDB      Key = "redis.default.db"
	KeyRedisDefaultPass    Key = "redis.default.pass"

	// legacy.*
	KeyLegacyStagingName Key = "legacy.staging.name"

	// auth.* / admin.* / dashboard.*
	KeyAuthJWTSecret      Key = "auth.jwt.secret"
	KeyAuthSessionTTL     Key = "auth.session.ttl"
	KeyAuthRefreshTTL     Key = "auth.refresh.ttl"
	KeyAdminSuperUsername Key = "admin.super.username"
	KeyAdminSuperPassword Key = "admin.super.password"
	KeyDashboardTimezone  Key = "dashboard.timezone"

	// order.*
	KeyOrderPayTimeout         Key = "order.pay_timeout"
	KeyOrderCancelScanInterval Key = "order.cancel_scan_interval"

	// flash_sale.*
	KeyFlashSaleDispatchMode          Key = "flash_sale.dispatch_mode"
	KeyFlashSaleShutdownTimeout       Key = "flash_sale.shutdown_timeout"
	KeyFlashSaleReconcileScanInterval Key = "flash_sale.reconcile_scan_interval"
	KeyFlashSaleConsumeScanInterval   Key = "flash_sale.consume_scan_interval"

	// flash_sale.rate_limit.*
	KeyFlashSaleRateLimitEnabled               Key = "flash_sale.rate_limit.enabled"
	KeyFlashSaleRateLimitUserMaxRequests       Key = "flash_sale.rate_limit.user.max_requests"
	KeyFlashSaleRateLimitUserWindowSeconds     Key = "flash_sale.rate_limit.user.window_seconds"
	KeyFlashSaleRateLimitActivityMaxRequests   Key = "flash_sale.rate_limit.activity.max_requests"
	KeyFlashSaleRateLimitActivityWindowSeconds Key = "flash_sale.rate_limit.activity.window_seconds"

	// flash_sale.queue_capacity.*
	KeyFlashSaleQueueCapacityEnabled         Key = "flash_sale.queue_capacity.enabled"
	KeyFlashSaleQueueCapacityMaxQueuedPerSKU Key = "flash_sale.queue_capacity.max_queued_per_sku"
	KeyFlashSaleQueueCapacityCounterTTL      Key = "flash_sale.queue_capacity.counter_ttl_seconds"

	// flash_sale.circuit_breaker.*
	KeyFlashSaleCircuitBreakerEnabled          Key = "flash_sale.circuit_breaker.enabled"
	KeyFlashSaleCircuitBreakerFailureThreshold Key = "flash_sale.circuit_breaker.failure_threshold"
	KeyFlashSaleCircuitBreakerOpenTimeout      Key = "flash_sale.circuit_breaker.open_timeout_seconds"
	KeyFlashSaleCircuitBreakerHalfOpenProbes   Key = "flash_sale.circuit_breaker.half_open_max_probes"

	// flash_sale.metrics.*
	KeyFlashSaleMetricsEnabled Key = "flash_sale.metrics.enabled"

	// flash_sale.kafka.*
	KeyFlashSaleKafkaEnabled       Key = "flash_sale.kafka.enabled"
	KeyFlashSaleKafkaBrokers       Key = "flash_sale.kafka.brokers"
	KeyFlashSaleKafkaTopic         Key = "flash_sale.kafka.topic"
	KeyFlashSaleKafkaDLQTopic      Key = "flash_sale.kafka.dlq_topic"
	KeyFlashSaleKafkaConsumerGroup Key = "flash_sale.kafka.consumer_group"

	// flash_sale.kafka.publisher.*
	KeyFlashSaleKafkaPublisherBatchSize    Key = "flash_sale.kafka.publisher.batch_size"
	KeyFlashSaleKafkaPublisherPollInterval Key = "flash_sale.kafka.publisher.poll_interval"
	KeyFlashSaleKafkaPublisherErrorBackoff Key = "flash_sale.kafka.publisher.error_backoff"

	// storage.*
	KeyStorageLocalRoot Key = "storage.local.root"

	// qiniu.*
	KeyQiniuAccessKey         Key = "qiniu.access_key"
	KeyQiniuSecretKey         Key = "qiniu.secret_key"
	KeyQiniuBucket            Key = "qiniu.bucket"
	KeyQiniuDomain            Key = "qiniu.domain"
	KeyQiniuRegion            Key = "qiniu.region"
	KeyQiniuTokenTTL          Key = "qiniu.token_ttl"
	KeyQiniuMaxFileSize       Key = "qiniu.max_file_size"
	KeyQiniuAllowedExtensions Key = "qiniu.allowed_extensions"
	KeyQiniuAllowedMimeTypes  Key = "qiniu.allowed_mime_types"

	// startup.*
	KeyStartupDependencyTimeout Key = "startup.dependency.timeout"

	// deployment.*（T2-01 Kubernetes 严格配置模式）
	KeyDeploymentRequireExternalEndpoints Key = "deployment.require_external_endpoints"
)

// Spec 是一个配置项的完整契约：路径、显式环境变量名、类型、默认值、敏感级别与生效路径。
//
// Default 的类型必须与 Kind 匹配：
//
//	KindString     -> string
//	KindBool       -> bool
//	KindInt        -> int
//	KindInt64      -> int64
//	KindDuration   -> time.Duration
//	KindStringList -> []string
type Spec struct {
	// Key 是配置项路径（也是注册表主键）。
	Key Key
	// Env 是显式映射的环境变量名，优先级高于配置文件。
	Env string
	// Kind 是解析类型。
	Kind Kind
	// Default 是代码默认值（文件与环境变量都不存在时生效）。
	Default any
	// Sensitive 标记敏感凭据：禁止写入日志、错误信息与文档示例值。
	Sensitive bool
	// Consumer 描述该配置实际生效的路径，便于排查「改了配置但不生效」。
	Consumer string
	// Description 是配置项用途说明。
	Description string
}

// Path 返回配置项的点分路径。
func (s Spec) Path() string { return string(s.Key) }

// DefaultText 返回默认值的稳定文本形式，供文档与配置矩阵一致性测试共用。
//
// 规则：字符串原样（空串写作「空」）；bool/int/int64 十进制；duration 整秒写作 Ns、
// 否则用 Go 原生形式（如 200ms）；字符串列表用逗号连接。
// 敏感配置的默认值一律为空串，因此本方法不会泄露任何凭据。
func (s Spec) DefaultText() string {
	switch value := s.Default.(type) {
	case string:
		if value == "" {
			return "空"
		}
		return value
	case bool:
		return strconv.FormatBool(value)
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case time.Duration:
		if value%time.Second == 0 {
			return strconv.FormatInt(int64(value/time.Second), 10) + "s"
		}
		return value.String()
	case []string:
		return strings.Join(value, ",")
	default:
		return fmt.Sprintf("%v", value)
	}
}

// specs 是配置契约注册表，顺序即文档与校验顺序（按模块分组）。
//
// 新增配置项必须同时：登记到此处、在 manifest/config/config.yaml 写出该路径、
// 在 docs/development/configuration.md 补充行；三者一致性由 contract_test.go 断言。
var specs = []Spec{
	// ---------- server ----------
	{KeyServerAddress, "SERVER_ADDRESS", KindString, ":8000", false, "HTTP Server 监听地址", "serve 受控 listener 与 GoFrame Server 监听地址"},
	{KeyServerGracefulTimeout, "SERVER_GRACEFUL_TIMEOUT", KindInt, 10, false, "HTTP drain 预算 / T2-03 终止预算", "SIGTERM 后等待在途请求完成的最长秒数"},

	// ---------- lifecycle（T2-03 冻结契约）----------
	{KeyLifecyclePreStopDelay, "LIFECYCLE_PRE_STOP_DELAY", KindDuration, 5 * time.Second, false, "容器终止预算（preStop 摘流窗口）", "Kubernetes preStop 钩子时长"},
	{KeyLifecycleTerminationGracePeriod, "LIFECYCLE_TERMINATION_GRACE_PERIOD", KindDuration, 60 * time.Second, false, "终止预算一致性校验", "容器终止宽限期（K8s terminationGracePeriodSeconds）"},

	// ---------- readiness ----------
	{KeyReadinessProbeTimeout, "READINESS_PROBE_TIMEOUT", KindInt, 1, false, "/ready 依赖探测", "DB/Redis 探测超时秒数，超时视为未就绪"},
	{KeyReadinessCacheTTL, "READINESS_CACHE_TTL", KindInt, 3, false, "/ready 结果缓存", "探测结果进程内缓存 TTL 秒数"},

	// ---------- database ----------
	{KeyDatabaseDefaultType, "DATABASE_DEFAULT_TYPE", KindString, "mysql", false, "DB 初始化（gdb.SetConfigGroup）", "数据库驱动类型"},
	{KeyDatabaseDefaultHost, "DATABASE_DEFAULT_HOST", KindString, "127.0.0.1", false, "DB 初始化（gdb.SetConfigGroup）", "数据库主机；严格模式要求环境变量显式提供且非回环地址"},
	{KeyDatabaseDefaultPort, "DATABASE_DEFAULT_PORT", KindString, "3306", false, "DB 初始化（gdb.SetConfigGroup）", "数据库端口"},
	{KeyDatabaseDefaultUser, "DATABASE_DEFAULT_USER", KindString, "root", false, "DB 初始化（gdb.SetConfigGroup）", "数据库用户名"},
	{KeyDatabaseDefaultPass, "DATABASE_DEFAULT_PASS", KindString, "", true, "DB 初始化（gdb.SetConfigGroup）", "数据库密码，无内置默认值，缺失即启动失败"},
	{KeyDatabaseDefaultName, "DATABASE_DEFAULT_NAME", KindString, "my_shop", false, "DB 初始化（gdb.SetConfigGroup）", "数据库名；严格模式要求环境变量显式提供"},
	{KeyDatabaseDefaultCharset, "DATABASE_DEFAULT_CHARSET", KindString, "utf8mb4", false, "DB 初始化（gdb.SetConfigGroup）", "连接字符集"},
	{KeyDatabaseDefaultDebug, "DATABASE_DEFAULT_DEBUG", KindBool, false, false, "DB 初始化（gdb.SetConfigGroup）", "是否输出 SQL 调试日志"},

	// ---------- redis ----------
	{KeyRedisDefaultAddress, "REDIS_DEFAULT_ADDRESS", KindString, "127.0.0.1:6379", false, "Redis 初始化（gredis.SetConfig）", "Redis 地址；严格模式要求环境变量显式提供且非回环地址"},
	{KeyRedisDefaultDB, "REDIS_DEFAULT_DB", KindInt, 0, false, "Redis 初始化（gredis.SetConfig）", "Redis 逻辑库编号"},
	{KeyRedisDefaultPass, "REDIS_DEFAULT_PASS", KindString, "", true, "Redis 初始化（gredis.SetConfig）", "Redis 密码"},

	// ---------- legacy ----------
	{KeyLegacyStagingName, "LEGACY_STAGING_NAME", KindString, "my_shop_staging", false, "legacy 数据迁移", "staging 库名，必须与目标库不同"},

	// ---------- auth / admin / dashboard ----------
	{KeyAuthJWTSecret, "AUTH_JWT_SECRET", KindString, "", true, "JWT 签发与校验", "HS256 签名密钥，至少 32 字节，缺失或过短即启动失败"},
	{KeyAuthSessionTTL, "AUTH_SESSION_TTL", KindInt, 3600, false, "会话 TTL", "access token 会话秒数"},
	{KeyAuthRefreshTTL, "AUTH_REFRESH_TTL", KindInt, 2592000, false, "Refresh token TTL", "refresh token 绝对有效秒数"},
	{KeyAdminSuperUsername, "ADMIN_SUPER_USERNAME", KindString, "admin", false, "超级管理员 seed", "超级管理员用户名"},
	{KeyAdminSuperPassword, "ADMIN_SUPER_PASSWORD", KindString, "", true, "超级管理员 seed", "超级管理员初始密码，仅首次创建时使用"},
	{KeyDashboardTimezone, "DASHBOARD_TIMEZONE", KindString, "Asia/Shanghai", false, "Dashboard 日期切分 / MySQL 会话时区", "业务时区，仅支持无 DST 时区"},

	// ---------- order ----------
	{KeyOrderPayTimeout, "ORDER_PAY_TIMEOUT", KindInt64, int64(900), false, "订单创建", "支付超时秒数"},
	{KeyOrderCancelScanInterval, "ORDER_CANCEL_SCAN_INTERVAL", KindInt, 60, false, "订单超时取消扫描器", "扫描周期秒数"},

	// ---------- flash_sale 基础 ----------
	{KeyFlashSaleDispatchMode, "FLASH_SALE_DISPATCH_MODE", KindString, "db_scanner", false, "秒杀 Runtime 装配", "消费入口模式：db_scanner | kafka"},
	{KeyFlashSaleShutdownTimeout, "FLASH_SALE_SHUTDOWN_TIMEOUT", KindDuration, 30 * time.Second, false, "秒杀 Runtime 关闭", "后台组件统一关闭预算"},
	{KeyFlashSaleReconcileScanInterval, "FLASH_SALE_RECONCILE_SCAN_INTERVAL", KindInt, 60, false, "秒杀缓存对账扫描器", "对账扫描周期秒数"},
	{KeyFlashSaleConsumeScanInterval, "FLASH_SALE_CONSUME_SCAN_INTERVAL", KindInt, 1, false, "秒杀消费扫描器", "异步消费扫描周期秒数"},

	// ---------- flash_sale.rate_limit ----------
	{KeyFlashSaleRateLimitEnabled, "FLASH_SALE_RATE_LIMIT_ENABLED", KindBool, false, false, "秒杀限流", "是否启用用户级/活动级限流"},
	{KeyFlashSaleRateLimitUserMaxRequests, "FLASH_SALE_RATE_LIMIT_USER_MAX_REQUESTS", KindInt64, int64(0), false, "秒杀限流", "用户级限流阈值，<=0 表示不限"},
	{KeyFlashSaleRateLimitUserWindowSeconds, "FLASH_SALE_RATE_LIMIT_USER_WINDOW_SECONDS", KindInt64, int64(1), false, "秒杀限流", "用户级限流窗口秒数"},
	{KeyFlashSaleRateLimitActivityMaxRequests, "FLASH_SALE_RATE_LIMIT_ACTIVITY_MAX_REQUESTS", KindInt64, int64(0), false, "秒杀限流", "活动级限流阈值，<=0 表示不限"},
	{KeyFlashSaleRateLimitActivityWindowSeconds, "FLASH_SALE_RATE_LIMIT_ACTIVITY_WINDOW_SECONDS", KindInt64, int64(1), false, "秒杀限流", "活动级限流窗口秒数"},

	// ---------- flash_sale.queue_capacity ----------
	{KeyFlashSaleQueueCapacityEnabled, "FLASH_SALE_QUEUE_CAPACITY_ENABLED", KindBool, false, false, "秒杀排队软上限", "是否启用 queued 计数软上限"},
	{KeyFlashSaleQueueCapacityMaxQueuedPerSKU, "FLASH_SALE_QUEUE_CAPACITY_MAX_QUEUED_PER_SKU", KindInt64, int64(0), false, "秒杀排队软上限", "单 activity×SKU 软上限，<=0 表示不限"},
	{KeyFlashSaleQueueCapacityCounterTTL, "FLASH_SALE_QUEUE_CAPACITY_COUNTER_TTL_SECONDS", KindInt64, int64(86400), false, "秒杀排队软上限", "排队计数 key 兜底 TTL 秒数"},

	// ---------- flash_sale.circuit_breaker ----------
	{KeyFlashSaleCircuitBreakerEnabled, "FLASH_SALE_CIRCUIT_BREAKER_ENABLED", KindBool, false, false, "秒杀闸门熔断器", "是否启用 Redis 闸门熔断"},
	{KeyFlashSaleCircuitBreakerFailureThreshold, "FLASH_SALE_CIRCUIT_BREAKER_FAILURE_THRESHOLD", KindInt, 5, false, "秒杀闸门熔断器", "连续失败进入 Open 的阈值"},
	{KeyFlashSaleCircuitBreakerOpenTimeout, "FLASH_SALE_CIRCUIT_BREAKER_OPEN_TIMEOUT_SECONDS", KindInt64, int64(10), false, "秒杀闸门熔断器", "Open 持续秒数"},
	{KeyFlashSaleCircuitBreakerHalfOpenProbes, "FLASH_SALE_CIRCUIT_BREAKER_HALF_OPEN_MAX_PROBES", KindInt, 1, false, "秒杀闸门熔断器", "Half-Open 放行的最大探测数"},

	// ---------- flash_sale.metrics ----------
	{KeyFlashSaleMetricsEnabled, "FLASH_SALE_METRICS_ENABLED", KindBool, false, false, "Prometheus 指标", "是否采集指标并暴露 GET /metrics"},

	// ---------- flash_sale.kafka ----------
	{KeyFlashSaleKafkaEnabled, "FLASH_SALE_KAFKA_ENABLED", KindBool, false, false, "Kafka Producer/Consumer 装配", "Kafka 总开关"},
	{KeyFlashSaleKafkaBrokers, "FLASH_SALE_KAFKA_BROKERS", KindString, "", false, "Kafka Producer/Consumer 装配", "Broker 列表，逗号分隔"},
	{KeyFlashSaleKafkaTopic, "FLASH_SALE_KAFKA_TOPIC", KindString, "", false, "Kafka Producer", "秒杀请求事件主题"},
	{KeyFlashSaleKafkaDLQTopic, "FLASH_SALE_KAFKA_DLQ_TOPIC", KindString, "", false, "Kafka Producer", "死信主题，必须与 topic 不同"},
	{KeyFlashSaleKafkaConsumerGroup, "FLASH_SALE_KAFKA_CONSUMER_GROUP", KindString, "", false, "Kafka Consumer", "消费组名"},

	// ---------- flash_sale.kafka.publisher ----------
	{KeyFlashSaleKafkaPublisherBatchSize, "FLASH_SALE_KAFKA_PUBLISHER_BATCH_SIZE", KindInt, 100, false, "Outbox Publisher", "单轮领取上限"},
	{KeyFlashSaleKafkaPublisherPollInterval, "FLASH_SALE_KAFKA_PUBLISHER_POLL_INTERVAL", KindDuration, 200 * time.Millisecond, false, "Outbox Publisher", "轮询间隔"},
	{KeyFlashSaleKafkaPublisherErrorBackoff, "FLASH_SALE_KAFKA_PUBLISHER_ERROR_BACKOFF", KindDuration, 2 * time.Second, false, "Outbox Publisher", "错误退避起点，指数增长、30s 封顶"},

	// ---------- storage ----------
	{KeyStorageLocalRoot, "STORAGE_LOCAL_ROOT", KindString, "./storage", false, "本地文件存储", "轮播图等运行时静态资源根目录"},

	// ---------- qiniu ----------
	{KeyQiniuAccessKey, "QINIU_ACCESS_KEY", KindString, "", true, "七牛上传凭证签发", "AccessKey，启动 required dependency"},
	{KeyQiniuSecretKey, "QINIU_SECRET_KEY", KindString, "", true, "七牛上传凭证签发", "SecretKey，启动 required dependency"},
	{KeyQiniuBucket, "QINIU_BUCKET", KindString, "", false, "七牛上传凭证签发", "对象存储空间名，启动 required dependency"},
	{KeyQiniuDomain, "QINIU_DOMAIN", KindString, "", false, "七牛上传凭证签发", "对外访问域名，启动 required dependency"},
	{KeyQiniuRegion, "QINIU_REGION", KindString, "z2", false, "七牛上传凭证签发", "区域 ID（z0/z1/z2/na0/as0）"},
	{KeyQiniuTokenTTL, "QINIU_TOKEN_TTL", KindInt64, int64(3600), false, "七牛上传凭证签发", "上传凭证有效秒数"},
	{KeyQiniuMaxFileSize, "QINIU_MAX_FILE_SIZE", KindInt64, int64(10 * 1024 * 1024), false, "七牛上传凭证签发", "单文件大小上限字节数"},
	{KeyQiniuAllowedExtensions, "QINIU_ALLOWED_EXTENSIONS", KindStringList, []string{"jpg", "jpeg", "png", "gif", "webp"}, false, "七牛上传凭证签发", "扩展名白名单，环境变量用逗号分隔"},
	{KeyQiniuAllowedMimeTypes, "QINIU_ALLOWED_MIME_TYPES", KindStringList, []string{"image/jpeg", "image/png", "image/gif", "image/webp"}, false, "七牛上传凭证签发", "MIME 白名单，环境变量用逗号分隔"},

	// ---------- startup ----------
	{KeyStartupDependencyTimeout, "STARTUP_DEPENDENCY_TIMEOUT", KindInt, 30, false, "启动依赖探测", "MySQL/Redis 连通性重试总超时秒数"},

	// ---------- deployment（T2-01）----------
	{KeyDeploymentRequireExternalEndpoints, "DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS", KindBool, false, false, "启动前严格校验", "为 true 时要求 DB Host/Name 与 Redis Address 由环境变量显式注入且非回环地址"},
}

// specIndex 是 Key 到 Spec 的索引，构建失败说明注册表存在重复键。
var specIndex = func() map[Key]Spec {
	index := make(map[Key]Spec, len(specs))
	for _, spec := range specs {
		if _, exists := index[spec.Key]; exists {
			panic("internal/config: 配置契约重复登记 " + string(spec.Key))
		}
		index[spec.Key] = spec
	}
	return index
}()

// Specs 返回按注册顺序排列的配置契约副本，供文档生成与自动化一致性测试使用。
func Specs() []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	return out
}

// SpecOf 返回指定配置项的契约。
func SpecOf(key Key) (Spec, bool) {
	spec, ok := specIndex[key]
	return spec, ok
}

// MustSpecOf 返回指定配置项的契约，未登记时 panic（属于编译期/编码错误，不是运行时配置错误）。
func MustSpecOf(key Key) Spec {
	spec, ok := specIndex[key]
	if !ok {
		panic("internal/config: 配置项未登记到契约注册表: " + string(key))
	}
	return spec
}
