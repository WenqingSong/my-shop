# SurgeCart 配置契约（T2-01）

本文是 SurgeCart 全项目配置的唯一权威说明。**所有业务配置读取必须经过 `internal/config`**，
禁止在业务代码中直接调用 `g.Cfg().GetEffective()` / `Get()` / `MustGet()`，也禁止自己写
`os.Getenv` 解析。框架自身（GoFrame Server / 日志 / 驱动）的初始化配置不在此约束内。

配置契约的机器可读版本是 `internal/config/contract.go` 中的注册表；本文的映射表由该注册表
生成并由 `internal/config/contract_test.go` 自动比对，任何一侧漂移都会让测试失败。

---

## 1. 配置优先级

```text
读取配置项
    │
    ▼
检查对应环境变量是否存在（存在性判断，不看是否为空）
    │
    ├── 存在 → 使用环境变量值
    │
    └── 不存在
          │
          ▼
      读取 YAML 配置文件（manifest/config/config.yaml）
          │
          ├── 存在 → 使用 YAML 值
          │
          └── 不存在
                │
                ▼
             使用代码默认值（internal/config/contract.go）
```

优先级固定为：

**环境变量 > YAML 配置文件 > 代码默认值**

实现要点（`internal/config/loader.go`）：

1. 文件读取使用 **`gcfg.Config.Get`**，不做键归一化。
   GoFrame 的 `GetEffective` 会先用 `internal/utils.FormatCmdKey` 把配置键里的 `_` 换成 `.`，
   于是 `flash_sale.metrics.enabled` 会被读成 `flash.sale.metrics.enabled`，永远命中不了
   `config.yaml` 里的 `flash_sale` 命名空间——表现为「环境变量与代码默认值都生效，唯独 YAML 不生效」。
2. 环境变量名来自契约中的**显式登记**，不依赖任何推导规则。
3. 缺失（既无环境变量也无 YAML 值）时才使用代码默认值。

> 本项目不使用 GoFrame 的命令行参数覆盖配置，因此优先级链中没有 `--key=value` 这一段。

---

## 2. 环境变量命名规则

| 规则 | 说明 |
|---|---|
| 形态 | 全大写 `SNAKE_CASE`，与配置路径一一对应 |
| 映射 | 点分路径的 `.` 替换为 `_`，例如 `flash_sale.kafka.brokers` → `FLASH_SALE_KAFKA_BROKERS` |
| 唯一性 | 环境变量名在注册表内**不得重复**（由契约测试断言） |
| 显式性 | 名称写在 `Spec.Env` 字段里，**不允许**运行时推导；推导会在 `flash_sale` 这类下划线命名空间上出错 |
| 空值语义 | 环境变量「已设置但为空」视为**显式存在**，不会被当成「未设置」而回退 YAML 或默认值 |

`docker-compose.yml` 的 `env_file` / `environment`、Kubernetes 的 `envFrom` 注入都遵循同一套名称。

---

## 3. 类型解析规则

| 类型 | 解析规则 | 非法示例 |
|---|---|---|
| `string` | 原样返回，不 trim（保留运维输入语义） | — |
| `bool` | `strconv.ParseBool`：`true/false/1/0/t/f/T/F/TRUE/FALSE/True/False` | `"ture"` → 报错 |
| `int` | `strconv.Atoi` | `"abc"`、`"1.5"` → 报错 |
| `int64` | `strconv.ParseInt(…, 10, 64)` | 同上 |
| `duration` | `time.ParseDuration`，且**必须大于 0** | `"5"`（缺单位）、`"0s"` → 报错 |
| `string_list` | YAML 数组按元素取值；环境变量按逗号分隔，逐项 trim 并丢弃空项 | — |

强制规则：

1. **非法值一律返回错误，不静默回退默认值。**
   例如 `FLASH_SALE_METRICS_ENABLED=ture` 会让进程启动失败，而不是被当成 `false`。
2. **错误信息包含配置项路径、环境变量名与解析原因，但不包含敏感值。**
   敏感项（`Sensitive=true`）的错误信息输出 `<敏感值已隐藏>`。
3. **`duration` 保留现有单位与数值语义**：`200ms`、`2s`、`30s`、`60s` 各自按 Go duration 解析，
   不做隐式换算。
4. **启动期整表校验。** `serve` 启动第一步调用 `config.Validate`（见 `boot.ValidateConfig`），
   逐项解析全部登记配置，把非法项一次性汇总成启动错误：

   ```text
   配置校验失败（2 项）：
     - 配置项 flash_sale.metrics.enabled（环境变量 FLASH_SALE_METRICS_ENABLED）来自环境变量，需要布尔值（true/false），实际为 "ture"
     - 配置项 lifecycle.pre_stop_delay（环境变量 LIFECYCLE_PRE_STOP_DELAY）来自配置文件，需要Go duration（如 5s/60s/200ms），实际为 "5"
   ```

5. **运行期兜底不算静默回退。** 少量请求链路配置（秒杀容量保护开关、扫描周期、支付超时、
   指标开关）在签名无法携带 `error` 的位置读取，出错时会输出 Error 级结构化日志
   （事件名 `flashsale_config_read_failed` / `scan_interval_config_read_failed` /
   `order_config_read_failed`）后按代码默认值处理。这些值在启动期已通过 `config.Validate`
   校验，运行期兜底只用于防止单个开关的读取异常放大成请求失败。

---

## 4. Kubernetes 严格配置模式

### 4.1 开关

```yaml
deployment:
  require_external_endpoints: false
```

环境变量：`DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS`（默认 `false`，保持本地开发兼容）。

### 4.2 开启后的启动前校验

`DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true` 时，进程在**建立任何数据库/Redis 连接之前**
（`boot.Bootstrap` 的第一步）校验：

| 环境变量 | 要求 |
|---|---|
| `DATABASE_DEFAULT_HOST` | 必须由环境变量显式提供，非空，且不是回环地址 |
| `DATABASE_DEFAULT_NAME` | 必须由环境变量显式提供，非空 |
| `REDIS_DEFAULT_ADDRESS` | 必须由环境变量显式提供，非空，其 host 部分不是回环地址 |

「显式提供」的含义是**环境变量存在**，而不是「能从 YAML 读到一个值」：

- 即使 `config.yaml` 里写了 `host: 127.0.0.1`，只要环境变量缺失，校验就失败；
- 空字符串环境变量同样失败；
- `localhost`、`*.localhost`、`127.0.0.0/8`、`::1` 等回环地址被拒绝。

失败示例（不含任何密码）：

```text
Kubernetes 严格配置模式校验失败（2 项）：
  - 数据库主机 必须由环境变量 DATABASE_DEFAULT_HOST 显式提供（严格模式 DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true，不接受配置文件或代码默认值）
  - 环境变量 REDIS_DEFAULT_ADDRESS="127.0.0.1:6379" 指向本机回环地址，严格模式禁止回退本机；请指向集群内或外部可达地址
```

### 4.3 结论

T2-06 的 Kubernetes ConfigMap 已按此设置（`manifest/kubernetes/shared/database-configmap.yaml`，Application 与 Migration 共用）：

```yaml
DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS: "true"
```

这样 ConfigMap 漏配关键连接参数时进程启动即失败，不会静默连接容器内 localhost。

### 4.4 Migration 的严格模式（与服务部署的差异）

`my-shop migrate up/force/version` 与 `serve` 共用同一个配置层，但**只校验它真正用到的项**：

| 校验项 | `serve`（应用部署） | `migrate`（迁移 Job） |
|---|---|---|
| `DATABASE_DEFAULT_HOST` 必须由环境变量提供且非回环 | 是 | 是 |
| `DATABASE_DEFAULT_NAME` 必须由环境变量提供 | 是 | 是 |
| `DATABASE_DEFAULT_PASS` 必须显式注入 | 是 | 是 |
| `REDIS_DEFAULT_ADDRESS` 必须由环境变量提供且非回环 | 是 | **否**（迁移不连接 Redis） |
| 其余业务配置（JWT/七牛/Kafka/秒杀） | 逐项解析 | **不读取** |

要点：

- 这是「migration 少要求」，不是「应用少要求」。`ValidateExternalEndpoints` 仍然要求三项，T2-01 冻结的应用严格模式未被放宽（`internal/config/migration_test.go` 有对照断言）。
- 校验发生在**建立任何数据库连接之前**（`migrations.Preflight` → `config.ValidateMigration`），漏配时错误直接指出环境变量名，而不是连接超时。
- 迁移 Job 与 Application 共用数据库 ConfigMap（`manifest/kubernetes/shared/database-configmap.yaml`），完整运维语义见 `docs/development/migrations.md` 与 `docs/development/kubernetes.md`。

---

## 5. Secret 与 ConfigMap 的职责边界

| 载体 | 承载内容 | 判定标准 |
|---|---|---|
| **Secret** | `DATABASE_DEFAULT_PASS`、`REDIS_DEFAULT_PASS`、`AUTH_JWT_SECRET`、`ADMIN_SUPER_PASSWORD`、`QINIU_ACCESS_KEY`、`QINIU_SECRET_KEY` | 泄露后可被直接用于认证/签名/解密 |
| **ConfigMap** | 其余全部配置项（地址、端口、库名、开关、阈值、周期、时区、白名单） | 泄露不构成直接凭据风险 |

规则：

1. 本表中「敏感级别 = 敏感」的项**只能**经 Secret 注入，禁止写入 `manifest/config/config.yaml`、
   `docker-compose.yml` 的明文默认值或镜像层。
2. `config.yaml` 中敏感项一律为空串（由 `scripts/test-container.sh` 断言）。
3. 七牛 Access Key 与 Secret Key 均按 Secret 处理（AK 虽非机密，但与 SK 成对泄露即可签发凭证）。
4. 任何日志、错误信息、HTTP 响应都不得输出敏感值；配置层的错误信息对敏感项统一输出
   `<敏感值已隐藏>`。

---

## 6. 本地开发如何配置

1. **默认值即可跑通大部分只读功能**：`config.yaml` 自带开发默认值（DB `127.0.0.1:3306`、
   Redis `127.0.0.1:6379`、时区 `Asia/Shanghai`、秒杀走 `db_scanner`）。
2. **必填的敏感项通过 `.env` 注入**：

   ```bash
   cp .env.example .env
   # 至少填写：MYSQL_ROOT_PASSWORD / AUTH_JWT_SECRET / ADMIN_SUPER_PASSWORD
   # 需要上传功能再填：QINIU_ACCESS_KEY / QINIU_SECRET_KEY / QINIU_BUCKET / QINIU_DOMAIN
   make up
   ```

3. **临时改单项优先用环境变量**，不要改 `config.yaml`（避免误提交）：

   ```bash
   ORDER_PAY_TIMEOUT=60 go run . serve
   ```

4. **严格模式保持关闭**：本地 `DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS` 不设置或设为 `false`。
5. **不要**为了本地能跑而把敏感值写进 `config.yaml`——该文件会被打包进镜像。

---

## 7. 如何排查「配置没生效」

按以下顺序逐项检查：

1. **确认进程实际读到的值**：`GET /metrics` 不暴露配置；改用启动日志中的
   `termination_budget`（含 `http_drain_timeout` / `runtime_shutdown_timeout`）与
   `flashsale.runtime_assembly` 事件核对关键预算与 Kafka 参数。
2. **确认环境变量是否真的进入进程**：
   `docker exec <container> env | grep -E 'FLASH_SALE|DATABASE|REDIS'`。
   注意 `docker-compose.yml` 只把显式列出的变量注入容器，宿主机 `export` 不会自动生效。
3. **确认变量名拼写**：必须是全大写蛇形，且与本文映射表一致。
   典型错误：`FLASH_SALE_METRIC_ENABLED`（漏了 `S`）、`DB_HOST`（缺少 `DATABASE_DEFAULT_` 前缀）。
4. **确认下划线命名空间**：`flash_sale.*`、`rate_limit.*`、`queue_capacity.*`、`circuit_breaker.*`、
   `graceful_timeout`、`cache_ttl` 这类带下划线的键在旧实现里会被 GoFrame 归一化成点号而读不到
   YAML 值。迁移到 `internal/config` 后已修复；若新增配置项绕过 `internal/config` 直接调
   `GetEffective`，该问题会复现——这是代码评审的必查项。
5. **确认优先级**：环境变量存在时永远覆盖 YAML。若怀疑被环境变量「压住」，
   先 `unset` 再重启。
6. **确认是否被启动期校验拒绝**：非法 bool/int/duration 会让 `serve` 直接退出，
   错误信息会指出具体配置项与环境变量名。
7. **确认严格模式**：`DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true` 时，
   `DATABASE_DEFAULT_HOST` / `DATABASE_DEFAULT_NAME` / `REDIS_DEFAULT_ADDRESS`
   必须由环境变量提供，YAML 值不参与。

---

## 8. 完整配置映射表

> 由 `internal/config/contract.go` 生成；`contract_test.go` 会断言本表与注册表逐行一致。
> 「默认值」列是**代码默认值**，`config.yaml` 中的显式值优先于它。

| 配置路径 | 环境变量 | 类型 | 默认值 | 敏感级别 | 生效路径 |
|---|---|---|---|---|---|
| `server.address` | `SERVER_ADDRESS` | string | :8000 | 非敏感 | HTTP Server 监听地址 |
| `server.graceful_timeout` | `SERVER_GRACEFUL_TIMEOUT` | int | 10 | 非敏感 | HTTP drain 预算 / T2-03 终止预算 |
| `lifecycle.pre_stop_delay` | `LIFECYCLE_PRE_STOP_DELAY` | duration | 5s | 非敏感 | 容器终止预算（preStop 摘流窗口） |
| `lifecycle.termination_grace_period` | `LIFECYCLE_TERMINATION_GRACE_PERIOD` | duration | 60s | 非敏感 | 终止预算一致性校验 |
| `readiness.probe_timeout` | `READINESS_PROBE_TIMEOUT` | int | 1 | 非敏感 | /ready 依赖探测 |
| `readiness.cache_ttl` | `READINESS_CACHE_TTL` | int | 3 | 非敏感 | /ready 结果缓存 |
| `database.default.type` | `DATABASE_DEFAULT_TYPE` | string | mysql | 非敏感 | DB 初始化（gdb.SetConfigGroup） |
| `database.default.host` | `DATABASE_DEFAULT_HOST` | string | 127.0.0.1 | 非敏感 | DB 初始化（gdb.SetConfigGroup） |
| `database.default.port` | `DATABASE_DEFAULT_PORT` | string | 3306 | 非敏感 | DB 初始化（gdb.SetConfigGroup） |
| `database.default.user` | `DATABASE_DEFAULT_USER` | string | root | 非敏感 | DB 初始化（gdb.SetConfigGroup） |
| `database.default.pass` | `DATABASE_DEFAULT_PASS` | string | 空 | 敏感 | DB 初始化（gdb.SetConfigGroup） |
| `database.default.name` | `DATABASE_DEFAULT_NAME` | string | my_shop | 非敏感 | DB 初始化（gdb.SetConfigGroup） |
| `database.default.charset` | `DATABASE_DEFAULT_CHARSET` | string | utf8mb4 | 非敏感 | DB 初始化（gdb.SetConfigGroup） |
| `database.default.debug` | `DATABASE_DEFAULT_DEBUG` | bool | false | 非敏感 | DB 初始化（gdb.SetConfigGroup） |
| `redis.default.address` | `REDIS_DEFAULT_ADDRESS` | string | 127.0.0.1:6379 | 非敏感 | Redis 初始化（gredis.SetConfig） |
| `redis.default.db` | `REDIS_DEFAULT_DB` | int | 0 | 非敏感 | Redis 初始化（gredis.SetConfig） |
| `redis.default.pass` | `REDIS_DEFAULT_PASS` | string | 空 | 敏感 | Redis 初始化（gredis.SetConfig） |
| `legacy.staging.name` | `LEGACY_STAGING_NAME` | string | my_shop_staging | 非敏感 | legacy 数据迁移 |
| `auth.jwt.secret` | `AUTH_JWT_SECRET` | string | 空 | 敏感 | JWT 签发与校验 |
| `auth.session.ttl` | `AUTH_SESSION_TTL` | int | 3600 | 非敏感 | 会话 TTL |
| `auth.refresh.ttl` | `AUTH_REFRESH_TTL` | int | 2592000 | 非敏感 | Refresh token TTL |
| `admin.super.username` | `ADMIN_SUPER_USERNAME` | string | admin | 非敏感 | 超级管理员 seed |
| `admin.super.password` | `ADMIN_SUPER_PASSWORD` | string | 空 | 敏感 | 超级管理员 seed |
| `dashboard.timezone` | `DASHBOARD_TIMEZONE` | string | Asia/Shanghai | 非敏感 | Dashboard 日期切分 / MySQL 会话时区 |
| `order.pay_timeout` | `ORDER_PAY_TIMEOUT` | int64 | 900 | 非敏感 | 订单创建 |
| `order.cancel_scan_interval` | `ORDER_CANCEL_SCAN_INTERVAL` | int | 60 | 非敏感 | 订单超时取消扫描器 |
| `flash_sale.dispatch_mode` | `FLASH_SALE_DISPATCH_MODE` | string | db_scanner | 非敏感 | 秒杀 Runtime 装配 |
| `flash_sale.shutdown_timeout` | `FLASH_SALE_SHUTDOWN_TIMEOUT` | duration | 30s | 非敏感 | 秒杀 Runtime 关闭 |
| `flash_sale.reconcile_scan_interval` | `FLASH_SALE_RECONCILE_SCAN_INTERVAL` | int | 60 | 非敏感 | 秒杀缓存对账扫描器 |
| `flash_sale.consume_scan_interval` | `FLASH_SALE_CONSUME_SCAN_INTERVAL` | int | 1 | 非敏感 | 秒杀消费扫描器 |
| `flash_sale.rate_limit.enabled` | `FLASH_SALE_RATE_LIMIT_ENABLED` | bool | false | 非敏感 | 秒杀限流 |
| `flash_sale.rate_limit.user.max_requests` | `FLASH_SALE_RATE_LIMIT_USER_MAX_REQUESTS` | int64 | 0 | 非敏感 | 秒杀限流 |
| `flash_sale.rate_limit.user.window_seconds` | `FLASH_SALE_RATE_LIMIT_USER_WINDOW_SECONDS` | int64 | 1 | 非敏感 | 秒杀限流 |
| `flash_sale.rate_limit.activity.max_requests` | `FLASH_SALE_RATE_LIMIT_ACTIVITY_MAX_REQUESTS` | int64 | 0 | 非敏感 | 秒杀限流 |
| `flash_sale.rate_limit.activity.window_seconds` | `FLASH_SALE_RATE_LIMIT_ACTIVITY_WINDOW_SECONDS` | int64 | 1 | 非敏感 | 秒杀限流 |
| `flash_sale.queue_capacity.enabled` | `FLASH_SALE_QUEUE_CAPACITY_ENABLED` | bool | false | 非敏感 | 秒杀排队软上限 |
| `flash_sale.queue_capacity.max_queued_per_sku` | `FLASH_SALE_QUEUE_CAPACITY_MAX_QUEUED_PER_SKU` | int64 | 0 | 非敏感 | 秒杀排队软上限 |
| `flash_sale.queue_capacity.counter_ttl_seconds` | `FLASH_SALE_QUEUE_CAPACITY_COUNTER_TTL_SECONDS` | int64 | 86400 | 非敏感 | 秒杀排队软上限 |
| `flash_sale.circuit_breaker.enabled` | `FLASH_SALE_CIRCUIT_BREAKER_ENABLED` | bool | false | 非敏感 | 秒杀闸门熔断器 |
| `flash_sale.circuit_breaker.failure_threshold` | `FLASH_SALE_CIRCUIT_BREAKER_FAILURE_THRESHOLD` | int | 5 | 非敏感 | 秒杀闸门熔断器 |
| `flash_sale.circuit_breaker.open_timeout_seconds` | `FLASH_SALE_CIRCUIT_BREAKER_OPEN_TIMEOUT_SECONDS` | int64 | 10 | 非敏感 | 秒杀闸门熔断器 |
| `flash_sale.circuit_breaker.half_open_max_probes` | `FLASH_SALE_CIRCUIT_BREAKER_HALF_OPEN_MAX_PROBES` | int | 1 | 非敏感 | 秒杀闸门熔断器 |
| `flash_sale.metrics.enabled` | `FLASH_SALE_METRICS_ENABLED` | bool | false | 非敏感 | Prometheus 指标 |
| `flash_sale.kafka.enabled` | `FLASH_SALE_KAFKA_ENABLED` | bool | false | 非敏感 | Kafka Producer/Consumer 装配 |
| `flash_sale.kafka.brokers` | `FLASH_SALE_KAFKA_BROKERS` | string | 空 | 非敏感 | Kafka Producer/Consumer 装配 |
| `flash_sale.kafka.topic` | `FLASH_SALE_KAFKA_TOPIC` | string | 空 | 非敏感 | Kafka Producer |
| `flash_sale.kafka.dlq_topic` | `FLASH_SALE_KAFKA_DLQ_TOPIC` | string | 空 | 非敏感 | Kafka Producer |
| `flash_sale.kafka.consumer_group` | `FLASH_SALE_KAFKA_CONSUMER_GROUP` | string | 空 | 非敏感 | Kafka Consumer |
| `flash_sale.kafka.publisher.batch_size` | `FLASH_SALE_KAFKA_PUBLISHER_BATCH_SIZE` | int | 100 | 非敏感 | Outbox Publisher |
| `flash_sale.kafka.publisher.poll_interval` | `FLASH_SALE_KAFKA_PUBLISHER_POLL_INTERVAL` | duration | 200ms | 非敏感 | Outbox Publisher |
| `flash_sale.kafka.publisher.error_backoff` | `FLASH_SALE_KAFKA_PUBLISHER_ERROR_BACKOFF` | duration | 2s | 非敏感 | Outbox Publisher |
| `storage.local.root` | `STORAGE_LOCAL_ROOT` | string | ./storage | 非敏感 | 本地文件存储 |
| `qiniu.access_key` | `QINIU_ACCESS_KEY` | string | 空 | 敏感 | 七牛上传凭证签发 |
| `qiniu.secret_key` | `QINIU_SECRET_KEY` | string | 空 | 敏感 | 七牛上传凭证签发 |
| `qiniu.bucket` | `QINIU_BUCKET` | string | 空 | 非敏感 | 七牛上传凭证签发 |
| `qiniu.domain` | `QINIU_DOMAIN` | string | 空 | 非敏感 | 七牛上传凭证签发 |
| `qiniu.region` | `QINIU_REGION` | string | z2 | 非敏感 | 七牛上传凭证签发 |
| `qiniu.token_ttl` | `QINIU_TOKEN_TTL` | int64 | 3600 | 非敏感 | 七牛上传凭证签发 |
| `qiniu.max_file_size` | `QINIU_MAX_FILE_SIZE` | int64 | 10485760 | 非敏感 | 七牛上传凭证签发 |
| `qiniu.allowed_extensions` | `QINIU_ALLOWED_EXTENSIONS` | string_list | jpg,jpeg,png,gif,webp | 非敏感 | 七牛上传凭证签发 |
| `qiniu.allowed_mime_types` | `QINIU_ALLOWED_MIME_TYPES` | string_list | image/jpeg,image/png,image/gif,image/webp | 非敏感 | 七牛上传凭证签发 |
| `startup.dependency.timeout` | `STARTUP_DEPENDENCY_TIMEOUT` | int | 30 | 非敏感 | 启动依赖探测 |
| `deployment.require_external_endpoints` | `DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS` | bool | false | 非敏感 | 启动前严格校验 |

---

## 9. 契约自动化校验

`internal/config/contract_test.go` 断言以下不变量（无需数据库/Redis）：

| 断言 | 目的 |
|---|---|
| 每个注册路径都存在于 `manifest/config/config.yaml` | 防止代码登记了配置但 YAML 从未提供，或键名拼写漂移 |
| 环境变量名唯一 | 防止两个配置项抢同一个变量 |
| 每个配置项都有明确的 `Kind` 与 `Default`，且默认值类型与 `Kind` 匹配 | 防止默认值类型漂移 |
| 敏感项分类符合白名单 | 防止新增凭据被漏标为非敏感 |
| `docs/development/configuration.md` 表格与注册表逐行一致 | 防止文档与实现漂移 |
| 环境变量名与「点分路径大写蛇形」规则一致 | 防止显式映射写错 |

`scripts/test-unit.sh` 已把 `./internal/config/...` 纳入纯内存单元测试范围。

## 10. 容器运行期约束（T2-04）

配置读取契约不因容器加固改变，但运行环境增加了以下边界：

- **只读根文件系统**：`manifest/config/config.yaml` 在镜像内为 `root:root 0444`，运行用户只读；应用不得在启动时改写配置文件（GoFrame 只读取，不写回）。
- **唯一写路径**：`/app/storage`（named volume）与 `/tmp`（tmpfs）。`STORAGE_LOCAL_ROOT` 必须指向 `/app/storage`；其他以相对路径 `./storage` 写入的配置在只读根文件系统下会失败。
- **环境变量注入**：敏感项（`DATABASE_DEFAULT_PASS`、`REDIS_DEFAULT_PASS`、`AUTH_JWT_SECRET`、`ADMIN_SUPER_PASSWORD`、`QINIU_SECRET_KEY` 等）继续仅经运行时环境变量注入；镜像内默认值为空，Compose 对必填项使用 `${VAR:?}`，缺失即启动失败。
- **Secret 边界**：本地用 `.env`（Git 忽略），集群用 Kubernetes Secret（T2-06：`surgecart-db-credentials` 与 `surgecart-app-credentials`）；不要把真实值写入 Compose 默认值、镜像层、命令行或日志。
- **迁移 Job**：只读根文件系统 + 非 root + 仅 `/tmp` 可写，`DATABASE_DEFAULT_PASS` 由 `surgecart-db-credentials` 注入、其余连接参数由共享 ConfigMap 提供；见 [数据库迁移运维](migrations.md)、[Kubernetes 部署体系](kubernetes.md) 与 `manifest/kubernetes/`。

构建、非 root/只读验证、扫描与排障命令见 [容器构建与运行安全](container-security.md)。
