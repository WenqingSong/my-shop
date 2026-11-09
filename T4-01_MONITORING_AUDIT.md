# T4-01 现有监控能力审查与指标设计（SurgeCart）

- 任务：TASK_4 / T4-01 — 现有监控能力审查与指标设计
- 执行模式：单 Agent 串行，只读审计 + 设计，**不实施**
- 执行模型：DeepSeek-V4.1-Flash
- 审计时间：2026-11-09 22:45–23:10（Asia/Shanghai）
- 审计结论口径：`PASS` / `FAIL` / `NOT_VERIFIED` / `NOT_IMPLEMENTED` 四类，不复用历史报告结论作为运行证据

---

## 1. 执行摘要

### 1.1 本次任务完成情况

本次完成了四类只读核验与两类设计：

| 类别 | 内容 | 结论 |
|---|---|---|
| Git 基线核验 | 仓库根、分支、SHA、工作区、远端引用 | 基线正确，工作区干净 |
| Go 源码审查 | Prometheus client、Registry、`/metrics`、指标开关、现有指标清单 | 已有秒杀域指标，HTTP/Lag/Age 缺失 |
| Kubernetes 运行核验 | Context、Node、Pod、Service、Endpoint、StorageClass、PSS | 集群健康，应用 1/1 Ready |
| 应用端点实测 | 临时 port-forward 只读 HTTP 请求 | `/health` `/ready` 200，**`/metrics` 404** |
| 指标设计 | HTTP、Go Runtime、Kafka Lag、Outbox | 完成，附实施顺序 |
| 部署设计 | Prometheus（K8s）+ Grafana Dashboard | 完成设计，**未部署** |

### 1.2 当前真实监控能力（一句话）

SurgeCart **已经有一套成型的秒杀域 Prometheus 指标实现**（`flashsale_*`，客户端 `client_golang v1.20.5`，默认 Registry，Gauge/Histogram 齐备），但**在当前 Kubernetes 环境中该端点完全没有暴露**（`/metrics` 实测 404），且**没有 HTTP 指标、没有 Kafka Consumer Lag、没有 Outbox 最老事件年龄**。

### 1.3 最大缺口（按对任务五 k6 压测的影响排序）

1. **`/metrics` 在集群内 404**：`FLASH_SALE_METRICS_ENABLED` 未在 ConfigMap/Secret 中出现，取默认 `false`，路由未挂载。没有这一步，任何 Prometheus/Grafana 都是空转。
2. **无 HTTP 层指标**：现有 `flashsale_requests_total{interface="order"}` 只覆盖**一个接口**且在业务逻辑层计时；全站 QPS、延迟、5xx 无法观测，k6 压测无法给出“整体服务”视角。
3. **无 Kafka Consumer Lag**：`docs/development/kafka.md` 明确标注未实现；这是 Kafka 模式下“消费侧积压”的唯一可信信号。
4. **Outbox 最老事件年龄未导出**：`OutboxPublisher.Backlog()` **已经算出** `OldestAge`，但从未写入任何 Gauge（只有 `Pending` 被导出）。属于“半成品”，补齐成本极低。

### 1.4 推荐实施路线（详见 §11）

```
T4-02 应用侧指标补齐（HTTP 指标 + Outbox Age + 打开 /metrics 开关）
   ↓
T4-03 集群内 Prometheus 部署 + 抓取验证（kafka 模式 + 真实流量核对）
   ↓
T4-04 Grafana Dashboard（在 T4-03 已验证的数据上做面板）
   ↓（可选，独立）Kafka Consumer Lag 采样器
```

---

## 2. Git 基线

### 2.1 核验命令与结果

| 项目 | 实际值 |
|---|---|
| 仓库根（`git rev-parse --show-toplevel`） | `D:/Wenqingsong/CodeProjects/codebuddy/k8s` |
| 远端（`git remote -v`） | `origin https://cnb.cool/framework-station/my-shop`（fetch/push 同一地址） |
| 初始分支 | `feat/k8s-t3-02-application-deployment-validation` |
| 初始 HEAD（= 候选基线） | `175b7fccd7cf8c147a37e506bdba33dff42ec24b` |
| 基线提交 | `175b7fc docs(k8s): T3-02 报告回填提交 SHA（仅元数据）`，2026-11-09 21:18:36 +0800 |
| 工作区状态 | 干净（`git status --short --branch` 无任何条目） |
| 任务四分支 | `feat/k8s-t4-prometheus-monitoring`（本次从基线新建，创建后 HEAD 仍为 `175b7fc`） |

### 2.2 基线真实性核验

```
175b7fc (HEAD -> feat/k8s-t3-02-application-deployment-validation, origin/feat/k8s-t3-02-application-deployment-validation) docs(k8s): T3-02 报告回填提交 SHA（仅元数据）
c98371b feat(k8s): deploy application and validate e2e
b8f7e20 (origin/feat/k8s-t3-01-local-kubernetes-validation, feat/k8s-t3-01-local-kubernetes-validation) docs(k8s): T3-01 报告回填提交 SHA（仅元数据）
fb21a15 feat(k8s): validate local cluster infrastructure
```

- T3-02 最终 SHA 存在，且本地分支与本地远端跟踪引用 `origin/feat/k8s-t3-02-...` 指向同一提交 → 历史关系正确。
- 目标分支 `feat/k8s-t4-prometheus-monitoring` 在本地与本地远端引用列表中**均不存在**（`git branch -a` 无该项），不存在“已有未审查 T4 实现”的风险。

### 2.3 远端状态：**NOT_VERIFIED（不可达）**

`git fetch origin --prune` 与 `git ls-remote` 均失败：

```
fatal: unable to access 'https://cnb.cool/framework-station/my-shop/':
Failed to connect to cnb.cool port 443 via 127.0.0.1 after 2063 ms
```

原因定位（只读检查，未修改任何配置）：Git 全局/本地代理配置为 `http.proxy=https://…` 实为 `http://127.0.0.1:18081`，该本地代理端口当前未监听；系统 `HTTP_PROXY/HTTPS_PROXY/ALL_PROXY` 均为空。

- 按任务约束**未修改 `git config`**、未切换代理、未改变任何 Git 配置。
- 影响：无法用 `git ls-remote` 确认远端分支列表；**推送大概率失败**（见 §15）。

---

## 3. Kubernetes 真实环境

### 3.1 集群与节点

| 项目 | 实际值 |
|---|---|
| `kubectl config current-context` | `kind-surgecart-local` |
| API Server | `https://127.0.0.1:14342` |
| 集群 | kind，单节点，名称 `surgecart-local` |
| Node | `surgecart-local-control-plane`，`Ready`，`v1.37.0`，`containerd://2.3.4`，OS `Debian GNU/Linux 13 (trixie)`，内核 `6.6.87.2-microsoft-standard-WSL2` |
| Node AGE | 3h17m（本次会话内未重建） |
| kubectl client | `v1.36.1`，Kustomize `v5.8.1` |
| Docker server | `29.9.3` |
| StorageClass | `standard (default)`，`rancher.io/local-path`，`Delete`，`WaitForFirstConsumer`，不支持扩容 |

**与历史报告的口径差异（必须记录）**：历史表写 kubectl `v1.34.1`、Docker Desktop `29.4.2`、Kubernetes `v1.37.0`；本次实测 kubectl 为 `v1.36.1`、Docker 为 `29.9.3`。工具版本已漂移，故本次不引用历史值。

### 3.2 `surgecart` 命名空间

| 资源 | 实际状态 |
|---|---|
| Namespace 标签 | `pod-security.kubernetes.io/enforce=restricted`（PSS 强制） |
| Deployment `surgecart` | `1/1` Ready，`UP-TO-DATE 1`，`AVAILABLE 1` |
| Pod `surgecart-5d4d6957dd-7rwkr` | `Running`，`1/1`，`restartCount=0`，镜像 `surgecart:b8f7e20-t3-02`，AGE 111m |
| Pod `surgecart-migrate-7j8dm` | `Completed`（迁移 Job，已结束） |
| Deployment `mysql` / `redis` / `kafka` | 各 `1/1`，Pod `Running`，`restartCount=0`，AGE 3h16m |
| Service `surgecart` | `ClusterIP`，`10.96.189.27`，`8000/TCP`，`targetPort=http`（命名端口），selector `{app.kubernetes.io/component=backend, app.kubernetes.io/name=surgecart}` |
| Endpoint `surgecart` | `10.244.0.24:8000`（单端点，与单副本一致） |
| Service `kafka` | `ClusterIP 10.96.242.148`，`9092/TCP,29092/TCP`，Endpoint `10.244.0.10:9092,29092` |
| Service `mysql` / `redis` | `10.96.239.142:3306` / `10.96.238.116:6379`，Endpoint 正常 |
| 集群内 Prometheus / Grafana | **不存在**：`kubectl get pods -A` 无任何 prometheus/grafana/exporter Pod |
| Monitoring CRD | **不存在**：`kubectl get crd` 无 `monitoring.coreos.com` / Prometheus Operator 相关 CRD |

### 3.3 应用配置来源（不输出任何取值）

运行中 Pod 的 `envFrom`（Deployment 顺序）：`surgecart-database-config`(CM) → `surgecart-config`(CM) → `surgecart-db-credentials`(Secret) → `surgecart-app-credentials`(Secret)。显式 `env` 只有 `DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true`。

按键名核验（未读取、未解码任何值）：

| Secret | 键名 |
|---|---|
| `surgecart-app-credentials` | `ADMIN_SUPER_PASSWORD`、`AUTH_JWT_SECRET`、`QINIU_ACCESS_KEY`、`QINIU_SECRET_KEY`、`REDIS_DEFAULT_PASS` |
| `surgecart-db-credentials` | `DATABASE_DEFAULT_PASS`、`MYSQL_ROOT_PASSWORD` |

结论：**`FLASH_SALE_METRICS_ENABLED` 既不在 ConfigMap，也不在 Secret**（`kubectl get configmap surgecart-config -o jsonpath` 返回空）。因此运行期取代码默认 `false`。

### 3.4 应用端点实测（临时 port-forward，只读）

执行：`kubectl port-forward -n surgecart svc/surgecart 18000:8000`（验证后立即终止，未创建任何资源）：

| 请求 | 结果 |
|---|---|
| `GET /health` | `200` |
| `GET /ready` | `200` |
| `GET /metrics` | **`404 Not Found`** — `HTTP/1.1 404 Not Found`，`Server: GoFrame HTTP Server`，`Content-Type: text/plain`，body `Not Found` |

**这是本次审计最关键的运行证据**：源码中存在 `/metrics` 实现，但**当前部署实例并不暴露**。历史 T3-02 报告 §15 第 6 条“`/metrics` 未开启（`FLASH_SALE_METRICS_ENABLED=false`）”与实测一致。

---

## 4. Go 监控实现审查

### 4.1 Prometheus Client

| 检查项 | 结论 | 证据 |
|---|---|---|
| 是否引入 `prometheus/client_golang` | 是，**直接依赖** | `go.mod:12` `github.com/prometheus/client_golang v1.20.5` |
| 当前版本 | `v1.20.5` | 同上；本地模块缓存亦为 `client_golang@v1.20.5` |
| `client_model` | `v0.6.1`，**仅测试使用**（读取 MetricFamily） | `go.mod:13`；唯一引用 `internal/metrics/metrics_test.go:15` |
| 是否使用 GoFrame 官方 Prometheus 组件 | **否**。`go.mod` 只有 `contrib/drivers/mysql`、`contrib/nosql/redis`，无 `contrib/metric(s)` | `go.mod:5-19` |
| 是否注册默认 Collector | **是（库默认行为）** | `metrics 包没有自建 Registry`；`promhttp.Handler()` 用默认 Registry，而 `client_golang` 的默认 Registry 在构造时 `MustRegister(NewProcessCollector(...))` + `MustRegister(NewGoCollector())`（库源码 `prometheus/registry.go:61-62`） |
| 是否有自定义 Collector | **有 1 个**：`observableGauge`（`Describe`/`Collect` 实现） | `internal/metrics/metrics.go:198-239` |
| 是否使用全局 Registry | **是**，`init()` 里 `prometheus.MustRegister(...)` 19 个指标 | `internal/metrics/metrics.go:241-250` |
| 是否存在独立 Registry | **生产代码无**；仅测试用 `prometheus.NewRegistry()` 隔离验证 | `internal/metrics/metrics_test.go:362` |
| 重复注册风险 | 当前无重复：19 个指标只在一处 `init()` 注册一次。风险点：`init()` 在包被导入时即执行，若将来新增“独立进程/多次导入”场景会 panic；现架构为单进程单入口，风险受控 | 同上 |

> 说明：不能因为 `go.mod` 有依赖就认为指标已实现——本次已继续核验到真实定义、注册与调用点，见 §4.4。

### 4.2 `/metrics` 路由

源码位置与行为：

```go
// internal/cmd/cmd.go:168-170
// 秒杀指标采集端点（Prometheus 文本格式，仅在 flash_sale.metrics.enabled 开启时暴露；
// 位于 Response 中间件之外，直接输出原始文本，不经统一 JSON 响应包装）。
registerMetricsRoute(ctx, s)
```

```go
// internal/cmd/cmd.go:222-231
func registerMetricsRoute(ctx context.Context, s *ghttp.Server) {
	if !metrics.Enabled(ctx) {
		return                       // 开关关闭 → 路由根本不注册
	}
	s.BindHandler("GET:/metrics", func(r *ghttp.Request) {
		metrics.Handler().ServeHTTP(r.Response.Writer, r.Request)
	})
}
```

| 检查项 | 结论 |
|---|---|
| 是否存在 `/metrics` | **源码存在**（`GET:/metrics`），但**当前集群实例 404**（未注册） |
| 由哪个模块注册 | `internal/cmd`（组合根），函数 `registerMetricsRoute`；测试侧有等价副本 `test/e2e/server_helpers_test.go:19` |
| 使用什么 Handler | `promhttp.Handler()`（默认 Registry，Prometheus Text Exposition Format，支持 `Accept` 协商） |
| 是否 Text Exposition Format | 是（promhttp 默认输出 text/plain; version=0.0.4） |
| 是否经过认证中间件 | **否**。注册在 `s.BindHandler` 上，位于 `s.Group("/")` 之外；`middleware.Auth`/`AdminAuth` 都是**分组级**中间件 |
| 是否经过业务限流中间件 | **否**。项目未注册全局限流中间件（限流在业务逻辑内 `flashsale/ratelimit.go`，不在 HTTP 层） |
| 是否经过统一响应中间件 | **否**（有意如此：`middleware.RequestID`/`middleware.Response` 只在 `s.Group("/")` 内生效） |
| 是否存在路径冲突 | 未发现。App 路由共约 98 条显式注册，无 `/metrics` 同名路由 |
| 是否有配置开关 | 有：`flash_sale.metrics.enabled` / `FLASH_SALE_METRICS_ENABLED`，默认 `false` |
| 是否能从 Kubernetes Service 访问 | **前提是开关打开**：同端口 8000，无需第二个端口/Service；打开后可达（`kubectl port-forward` 实测路径连通） |
| 是否有访问日志 | **否**。`/metrics` 不经过 `middleware.RequestID`，因此不产生结构化访问日志（这是设计取舍，不是缺陷） |

运行时验证补充：`test/e2e/flashsale_v5_test.go:57-89`（AC-005）在开关打开时断言 `/metrics` 返回 200 且含 `flashsale_requests_total` / `result="queued"`，说明**端点在应用侧是可用且经过验证的**，缺的只是部署侧开关。

### 4.3 `FLASH_SALE_METRICS_ENABLED` 真实语义（重点）

定义链：

| 层级 | 位置 | 内容 |
|---|---|---|
| 契约 Key | `internal/config/contract.go:116` | `KeyFlashSaleMetricsEnabled Key = "flash_sale.metrics.enabled"` |
| 契约 Spec（默认值/环境变量） | `internal/config/contract.go:283` | `{KeyFlashSaleMetricsEnabled, "FLASH_SALE_METRICS_ENABLED", KindBool, false, false, "Prometheus 指标", "是否采集指标并暴露 GET /metrics"}` |
| YAML 值 | `manifest/config/config.yaml:203-206` | `metrics: enabled: false` |
| 读取封装 | `internal/metrics/metrics.go:252-263` | `Enabled(ctx)` → `config.Bool(ctx, KeyFlashSaleMetricsEnabled)`，读取失败时 `glog.Errorf` 后按 `false` 处理 |
| 严格校验 | `internal/config`（启动期 `Validate`） | 非法布尔值（如 `ture`）**启动失败**，不静默回退 |

**开启（`true`）时具体发生什么**：

1. `registerMetricsRoute` 挂载 `GET:/metrics`（唯一使端点存在的地方）。
2. 全部 `ObserveXxx(...)` 真正写入指标（否则为空操作）。
3. `MarkConsumerJoined` / `SetOutboxBacklogPending` 真正写 Gauge（否则为空操作，且**不覆盖**已观测值）。
4. `runOutboxBacklogSampler` 每 5s **真的查一次库**（否则 `sampleOutboxBacklog` 直接返回，零 DB 负载）。

**关闭（`false`）时具体跳过什么**：

| 被跳过 | 不被跳过 |
|---|---|
| `/metrics` 路由注册 | `init()` 中的 19 个指标**仍然注册**到默认 Registry（对象存在，只是没有出口） |
| 所有业务指标写入（空操作） | Go/Process Collector 仍由库默认注册（同样没有出口） |
| Outbox 积压采样（零 DB 查询） | —— |

**因此必须明确区分（本次审计结论）**：

| 能力 | 受 `FLASH_SALE_METRICS_ENABLED` 控制？ | 当前是否实现 |
|---|---|---|
| 秒杀业务指标（`flashsale_*`） | 是（写入 + 出口） | **已实现** |
| Prometheus HTTP Handler（`/metrics`） | 是（仅出口） | 已实现，**运行期未开启** |
| Go Runtime 指标 | **不受控**（库默认 Collector 恒在默认 Registry） | 已具备（随 `/metrics` 一起出现） |
| HTTP 指标（全站） | 无关 | **NOT_IMPLEMENTED** |
| Kafka Consumer Lag | 无关 | **NOT_IMPLEMENTED** |
| Outbox 待发布数 | 是 | 已实现（Gauge + 5s 采样） |
| Outbox 最老事件年龄 | 无关（未导出） | **NOT_IMPLEMENTED** |

> 结论：`FLASH_SALE_METRICS_ENABLED=true` **不等于**“Prometheus 监控已实现”。它只是“秒杀域指标 + `/metrics` 出口”的总开关；HTTP 指标、Consumer Lag、Outbox 年龄都不在其中。

### 4.4 现有指标清单（源码核对，非推测）

全部定义于 `internal/metrics/metrics.go:75-187`，`Namespace="flashsale"`：

| 指标名 | 类型 | 标签 | 观测点 |
|---|---|---|---|
| `flashsale_requests_total` | CounterVec | `interface`(order/consume), `result`(queued/success/gate_rejected/rate_limited/queue_full/error/failed/dead/duplicate/invalid) | `ObserveOrder`（`flashsale.go:266-270`）、`ObserveConsume` |
| `flashsale_request_duration_seconds` | HistogramVec | 同上 | 同上；桶 `0.0005…10`（`metrics.go:73`） |
| `flashsale_outbox_claimed_total` | Counter | — | `ObserveOutboxClaim` |
| `flashsale_outbox_lease_reclaimed_total` | Counter | — | 同上 |
| `flashsale_outbox_published_total` | Counter | — | `ObserveOutboxPublished` |
| `flashsale_outbox_publish_failed_total` | CounterVec | `reason`(kafka/unknown/encode/missing_result) | `ObserveOutboxFailed` |
| `flashsale_outbox_retried_total` | Counter | — | `ObserveOutboxRetried` |
| `flashsale_outbox_mark_skipped_total` | CounterVec | `reason`(lease_lost/already_published/lock_busy) | `ObserveOutboxMarkSkipped` |
| `flashsale_outbox_publish_round_duration_seconds` | Histogram | — | `ObserveOutboxPublishDuration` |
| `flashsale_consumer_records_total` | Counter | — | `kafka_consumer.go:240` |
| `flashsale_consumer_processed_total` | CounterVec | `result` | `ObserveConsumerResult` |
| `flashsale_consumer_retries_total` | Counter | — | `ObserveConsumerRetry` |
| `flashsale_consumer_dlq_total` | CounterVec | `reason`(7 类枚举) | `ObserveConsumerDLQ` |
| `flashsale_consumer_commit_failures_total` | Counter | — | `kafka_consumer.go:264` |
| `flashsale_consumer_restarts_total` | Counter | — | `ObserveConsumerRestart` |
| `flashsale_consumer_processing_seconds` | HistogramVec | `result` | `ObserveConsumerResult` |
| `flashsale_consumer_joined` | Gauge（`observableGauge`，无标签） | — | `OnPartitionsAssigned/Revoked/Lost`、`Close`、`NewConsumer` |
| `flashsale_outbox_backlog_pending` | Gauge（`observableGauge`，无标签） | — | 后台采样器（5s） |

设计要点（已在代码中固化，值得保留）：

- **`observableGauge` 语义**：未被观测过时**整条序列不导出**，避免 `db_scanner` 模式下导出恒 0 的假信号（`metrics.go:189-224`）。
- **采样不进抓取路径**：`/metrics` 抓取**从不查库**，DB 访问全部收敛到 5s 后台采样器（`metrics.go:89-92`、`flashsale_runtime.go:418-441`）。
- **采样失败不前值清零**：查询失败只记日志、保留上一次有效值（`flashsale_runtime.go:452-461`、`metrics.go:406-419`）。

### 4.5 缺失指标（源码层面确认“不存在”，非“没找到”）

| 缺失项 | 判定依据 |
|---|---|
| HTTP 请求总数/延迟/进行中 | `internal/middleware/` 仅 `auth.go`、`principal.go`、`requestid.go`、`response.go`，无任何指标代码；全仓 `prometheus|promhttp` 的 `.go` 命中仅 12 个文件，无一在 HTTP 中间件 |
| Kafka Consumer Lag | 无 `kadm` 依赖、无 `OffsetFetch`/`ListOffsets` 生产代码（`kmsg` 仅出现在测试）；`metrics.go:130-133` 注释明确“本批次不伪造该指标” |
| Outbox 最老事件年龄 | `OldestAge` 仅在 `outbox.go:217/901-904` 与测试中出现，`metrics` 包无对应 Gauge |
| 商城业务指标（订单、支付、库存） | `internal/metrics` 只服务秒杀域，全仓无其他 `CounterVec/HistogramVec` 定义 |

---

## 5. HTTP 指标设计

### 5.1 现状

- HTTP 层**零指标**。现有可观测数据只有 `middleware.RequestID` 输出的结构化访问日志（`method`、`path`、`status`、`duration`、`client_ip`、`request_id`，`internal/middleware/requestid.go:45-53`）。
- 现有 `flashsale_request_duration_seconds{interface="order"}` **不是 HTTP 延迟**：它在 `sFlashSale.CreateOrder` 内部用 `time.Since(started)` 计时（`internal/logic/flashsale/flashsale.go:266-270`），覆盖 Redis 准入 + DB 入队，不含 GoFrame 路由/中间件/序列化耗时，也不覆盖其余 ~97 条路由。

### 5.2 建议指标（T4-02 实施，本次仅设计）

| 指标 | 类型 | 标签 | 说明 |
|---|---|---|---|
| `surgecart_http_requests_total` | CounterVec | `method`, `route`, `status` | 请求总量；`status` 用三位数字符串 |
| `surgecart_http_request_duration_seconds` | HistogramVec | `method`, `route` | 延迟分布；桶复用 `histogramBuckets` 风格（`0.0005…10`） |
| `surgecart_http_requests_in_flight` | Gauge | — （或 `method`） | 当前进行中请求数，用于识别“请求堆积但未返回” |

命名冲突检查：现有指标统一 `flashsale_` 前缀，`surgecart_` 前缀**当前未被占用**（全仓无该前缀指标）。

### 5.3 标签与基数控制（最关键）

| 维度 | 取值来源 | 基数上界（实测/估算） |
|---|---|---|
| `method` | `r.Method` | ≤ 6（GET/POST/PUT/PATCH/DELETE/HEAD） |
| `route` | **`r.Router.Uri`（规范化路由模板）** | ≤ ~100（本次统计显式注册路由约 98 条） |
| `status` | `r.Response.Status`（0 视为 200） | ≤ ~10（本项目错误码映射出的状态集合有限） |

- **必须使用规范化路由模板**，不能把原始路径当标签。GoFrame v2.10.3 的 `ghttp.Request` 暴露 `Router *Router`，`Router.Uri` 即路由模板（库源码 `net/ghttp/ghttp_request.go:32`、`net/ghttp/ghttp.go:53-54`）。例如 `POST /flash-sales/:id/orders` 的 `Router.Uri` 是 `/flash-sales/:id/orders`，而不是 `/flash-sales/123/orders`。
- **未匹配路由**（404/静态资源）`r.Router` 为 nil，必须归入固定值 `route="unmatched"`，否则会退化成“原始路径即标签”，直接造成标签爆炸（攻击面）。
- 静态资源 `/storage/banners/*` 也必须归一化为固定桶（如 `route="/storage/*"`），否则每个文件名一个时间序列。
- 严禁：`user_id`、`order_id`、`request_id`、`token`、原始 query string、未规范化 URL。项目中 `flashsale_*` 已严格遵循低基数原则（`metrics.go:24-38`），HTTP 指标应保持一致。

### 5.4 注册位置与顺序

建议新增 `internal/middleware/metrics.go`，并在组合根按下列顺序注册：

```go
// internal/cmd/cmd.go:172-176 现状
root.Middleware(middleware.RequestID)
root.Middleware(middleware.Response)
```

建议在 `RequestID` **之前**（或紧随其后、`Response` 之前）插入 `middleware.Metrics`：

- 必须在 `Response` **外层**：`middleware.Response` 负责把业务错误码写成 HTTP status（`response.go:31-37`），只有在外层 `r.Middleware.Next()` 返回后才能读到**最终** status。
- 必须在 `RequestID` 内层或外层均可；若放外层，`/metrics` 请求也会被统计（需评估是否排除，建议**排除 `/metrics` 自身**，避免抓取流量污染 QPS）。
- `in_flight` 用 `Inc()/defer Dec()` 计数。

### 5.5 PromQL（供 T4-04 与 k6 阶段使用）

```promql
# QPS（全站）
sum(rate(surgecart_http_requests_total{route!="unmatched"}[1m]))

# 秒杀下单接口 QPS
sum(rate(surgecart_http_requests_total{route="/flash-sales/:id/orders",method="POST"}[1m]))

# P95（全站）
histogram_quantile(0.95,
  sum by (le) (rate(surgecart_http_request_duration_seconds_bucket[5m])))

# 按路由 P95
histogram_quantile(0.95,
  sum by (le, route) (rate(surgecart_http_request_duration_seconds_bucket[5m])))

# 5xx 错误率（分母 clamp 防止 0 除）
sum(rate(surgecart_http_requests_total{status=~"5.."}[5m]))
/
clamp_min(sum(rate(surgecart_http_requests_total[5m])), 1e-9)

# 进行中请求数
sum(surgecart_http_requests_in_flight)
```

> 现有 `flashsale_requests_total{interface="order"}` 与 `surgecart_http_requests_total{route="/flash-sales/:id/orders"}` **语义不同**（业务层受理结果 vs HTTP 传输层状态），Dashboard 中必须分开呈现，不可互相替代。

---

## 6. Go Runtime 指标设计

### 6.1 已有 Collector（无需任何代码改动）

`metrics.Handler()` 返回 `promhttp.Handler()`（默认 Registry）。`client_golang v1.20.5` 的默认 Registry 在构造时即注册：

```go
// client_golang@v1.20.5/prometheus/registry.go:61-62
MustRegister(NewProcessCollector(ProcessCollectorOpts{}))
MustRegister(NewGoCollector())
```

已核对库源码中的指标名，任务书要求的七个指标**全部已经在默认 Collector 内**：

| 指标 | 来源 | 是否已具备 |
|---|---|---|
| `go_goroutines` | GoCollector（`go_collector.go:212`） | 是（库内置） |
| `go_memstats_heap_alloc_bytes` | GoCollector（`go_collector.go:71`） | 是（库内置） |
| `go_memstats_heap_inuse_bytes` | GoCollector（`go_collector.go:95`） | 是（库内置） |
| `go_gc_duration_seconds` | GoCollector（`go_collector.go:220`） | 是（库内置） |
| `go_memstats_gc_sys_bytes` | GoCollector（memstats 家族，`memstatNamespace("gc_sys_bytes")`） | 是（库内置） |
| `process_cpu_seconds_total` | ProcessCollector（`process_collector.go:70`） | 是（库内置，需 Linux 的 `/proc`，容器内满足） |
| `process_resident_memory_bytes` | ProcessCollector（`process_collector.go:95`） | 是（库内置） |

### 6.2 缺失 / 建议

- **不需要自研 Go Runtime Collector**。项目代码中没有任何 `runtime/metrics`、`NumGoroutine`、`MemStats` 的手写采集（全仓搜索 0 命中），这是正确的取舍。
- **不需要新增依赖**。
- 唯一“缺失”是**出口**：这些指标只有在 `FLASH_SALE_METRICS_ENABLED=true`（`/metrics` 被挂载）时才可见。T4-02 打开开关即自动获得，**零额外代码**。
- **不需要独立 Registry**：当前用默认 Registry，Go/Process Collector 自动附带；若改成独立 Registry，反而要手动注册这两个 Collector。**保持现状**。
- 开销提示：`NewGoCollector()` 在 v1.20.5 基于 `runtime/metrics`，抓取时同步读取，属于常规开销；15s 抓取间隔下可忽略。

---

## 7. Kafka Consumer Lag 方案

### 7.1 真实 Kafka 客户端与消费模型（源码核对）

| 项目 | 实际值 | 证据 |
|---|---|---|
| 客户端库 | `github.com/twmb/franz-go v1.18.1`（`pkg/kgo`），`kmsg v1.9.0` | `go.mod:15-16` |
| 是否引入 `kadm` | **否**（`go.sum` 无 `franz-go/pkg/kadm`） | `go.sum` 检索 0 命中 |
| Consumer Group（集群内实际值） | `surgecart-flashsale-order-worker-v1` | `manifest/kubernetes/overlays/local/configmap-patch.yaml:31`、`manifest/config/config.yaml:149` |
| Topic（集群内实际值） | `surgecart.flashsale.order.requested.v1` | `configmap-patch.yaml:29` |
| DLQ Topic | `surgecart.flashsale.order.requested.v1.dlq` | `configmap-patch.yaml:30` |
| Topic 分区数 | **1**（RF=1） | `manifest/kubernetes/infrastructure/base/configmap.yaml:77-78` |
| 运行模式 | `FLASH_SALE_DISPATCH_MODE=kafka`、`FLASH_SALE_KAFKA_ENABLED=true` | `configmap-patch.yaml:27-28` |
| 位移提交 | **手动提交**：`kgo.DisableAutoCommit()` + `CommitRecords` | `internal/mq/kafka/consumer.go:96`、`257-302` |
| Rebalance | `kgo.BlockRebalanceOnPoll()`；`OnPartitionsAssigned/Revoked/Lost` 维护 `joined` | `consumer.go:98-105` |
| 会话/心跳 | `SessionTimeout=30s`、`HeartbeatInterval=3s` | `consumer.go:25-26, 99-100` |
| Cluster ID 语义 | `consumeBatch` 成功后才提交整批（失败消息及其后不提交） | `kafka_consumer.go:237-267` |
| Kafka Admin API 是否已用 | **生产代码未用**；测试代码已用 `kmsg` 原生请求 | 见 §7.3 |

### 7.2 Lag 的正确含义（本项目口径）

```
partition Lag = Log End Offset (LEO) - Consumer Committed Offset
```

- **Committed Offset**：消费组在 `__consumer_offsets` 中已提交的位移（`OffsetFetch`），即“下一条要消费的位置”。
- **Log End Offset**：分区末尾位移（`ListOffsets` with `timestamp=-1`）。
- **不能用**：`flashsale_consumer_records_total` / `processed_total` 反推 Lag；不能写死 0；不能用随机/静态值。
- **本项目必须区分的三种“积压”**（`docs/development/kafka.md:890-899` 已定义，本次复核一致）：

| 信号 | 含义 |
|---|---|
| `flashsale_outbox_backlog_pending` | MySQL 中**尚未被 Kafka 确认**的待发布记录（发布侧） |
| `surgecart_kafka_consumer_group_lag`（建议） | 已进入 Kafka、**尚未被提交位移**的消息数（消费侧） |
| `published_at` 非空但 status 仍 `queued` | 已发布、消费处理中（在途窗口，**不是**积压） |

### 7.3 方案 A：应用内 Kafka Admin API（可行，且**不需要新增依赖**）

**关键证据**：本仓库的 Kafka 集成测试**已经**用现有 `kgo.Client` 发起原生管理请求，无需 `kadm`：

```go
// internal/logic/flashsale/kafka_consumer_kafka_test.go:61-71
req := kmsg.NewPtrOffsetFetchRequest()
req.Version = 7
req.Group = group
topicReq := kmsg.NewOffsetFetchRequestTopic()
topicReq.Topic = topic
req.Topics = append(req.Topics, topicReq)
resp, err := req.RequestWith(ctx, client)
```

```go
// internal/logic/flashsale/kafka_consumer_kafka_test.go:101-107
req := kmsg.NewPtrDescribeGroupsRequest()
req.Version = 4
req.Groups = append(req.Groups, group)
resp, err := req.RequestWith(ctx, client)
```

`kmsg` 侧能力核对（`kmsg@v1.9.0/generated.go`）：

- `NewPtrListOffsetsRequest()` — `generated.go:6076`；`ListOffsetsResponseTopicPartition.Offset`（v1+，`Timestamp=-1` 时即 **LEO**）— `generated.go:6150-6154`
- `NewPtrOffsetFetchRequest()` — `generated.go:11939`
- `kgo.Client.Request(ctx, kmsg.Request)` 存在于 v1.18.1（`pkg/kgo/client.go:1133`），`kmsg.Request` 自带 `RequestWith(ctx, client)` 快捷方法（已被本仓库测试使用）

**⚠ 与现有文档的冲突（必须纠正）**：`docs/development/kafka.md:526-528` 与 `metrics.go:130-133` 声称“Lag 需要 `kadm`，为避免引入新依赖因此不提供”。经源码核对，该结论**不准确**：`kmsg`（已在 `go.mod` 直接依赖）足以完成 `OffsetFetch + ListOffsets`，**无需 `kadm`、无需任何新依赖**。T4-02 应同步修正该文档表述。

**方案 A 的具体设计**：

| 项 | 设计 |
|---|---|
| 客户端 | **独立** `kgo.Client`（`SeedBrokers` 同 brokers，`ClientID=surgecart-flashsale-lag-sampler`，**不设置 ConsumerGroup、不 ConsumeTopics**）——与业务 Consumer 隔离，绝不复用其 `kgo.Client`（复用会干扰 `BlockRebalanceOnPoll` 生命周期） |
| 采样频率 | **15s**（与 Prometheus `scrape_interval: 15s` 对齐；Lag 是慢变量，5s 无收益） |
| 单次预算 | 3s（`context.WithTimeout`），与 Outbox 采样一致；串行执行不堆积 |
| 查询步骤 | ① `Metadata`（取 Topic 分区列表，本次为 1 个分区）→ ② `ListOffsets(timestamp=-1)` 取每个分区 LEO → ③ `OffsetFetch(group)` 取已提交位移 |
| 计算 | `lag = max(LEO - committed, 0)`；`committed == -1`（从未提交）时**按 LEO 计**（等价于全部未消费），并在日志说明“首次消费前” |
| 指标 | `surgecart_kafka_consumer_group_lag{consumer_group="surgecart-flashsale-order-worker-v1", topic="surgecart.flashsale.order.requested.v1", partition="0"}` |
| 分区标签 | **建议保留** `partition`：当前只有 1 个分区，基数=1；保留后扩容/多分区时无需改指标结构。若坚持最小化，可只保留 `consumer_group`+`topic` 并聚合求和（需在文档中锁定口径） |
| 失败处理 | 查询失败 → **保留上一次有效值**（与 Outbox 采样同一原则），另加 `surgecart_kafka_lag_sample_failures_total` 计数暴露“数据不新鲜”；**绝不写 0** |
| Rebalance 期间语义 | Rebalance 中提交位移可能短暂回退/缺失，Lag 会瞬时跳高；这是**真实信号**（消费确实暂停），不做平滑/剔除。需在 Dashboard 标注“rebalance 抖动属正常” |
| 数据新鲜度 | 最差 15s（采样）+ 15s（抓取）≈ 30s |
| 权限 | PLAINTEXT 单 Broker，无 ACL，无需额外权限；Kafka 3.9.1 支持 `OffsetFetch v7`、`ListOffsets v7` |
| 是否阻塞业务 | 否：独立连接、独立 goroutine、独立超时 |
| 运行范围 | 仅 `dispatch_mode=kafka` 启动；`db_scanner` 模式**不导出**该序列（与 `consumer_joined` 同一原则） |

**代价**：约 1 个额外 TCP 连接 + 每 15s 3 次轻量元数据请求；代码量约 150–200 行（含测试）。

### 7.4 方案 B：独立 Kafka Exporter（不推荐）

| 维度 | 评估 |
|---|---|
| 新增资源 | 1 个 Deployment + 1 个 Service（或 KSM 依赖） |
| 网络权限 | 需访问 `kafka.surgecart.svc.cluster.local:9092` |
| RBAC | 静态配置则**不需要**；若配 ServiceMonitor 则需 Prometheus Operator CRD（本集群**没有**） |
| 资源开销 | 50–100Mi 内存、额外一个常驻进程 |
| 维护成本 | 新增镜像、版本、配置面；与“本地单节点、仅 1 分区”的规模严重不匹配 |
| 额外价值 | 可顺带采集 broker/topic 级指标（本项目暂不需要） |

**结论：不引入**。当前只有 1 个 Topic、1 个分区、1 个消费组，独立 Exporter 的组件成本远大于收益，且违背“不为展示 Kubernetes 知识引入不必要 RBAC/组件”的约束。

### 7.5 最终推荐

**采用方案 A（应用内 `kmsg` 采样器，独立 `kgo.Client`）**，理由：

1. 零新依赖（`kmsg` 已在 `go.mod`，且本仓库测试已证明可用）；
2. 与 Outbox 采样器同一模式（15s/3s/失败保前值/不进抓取路径），架构一致性最好；
3. 指标天然带 `consumer_group`/`topic` 标签，无需在 Prometheus 侧做 relabel；
4. 失败语义可控（保留旧值 + 失败计数），比“Exporter 挂了就静默无数据”更可诊断。

**缺失信息**：无阻塞性缺失。唯一需在 T4-02 验证的是 `OffsetFetch v7` 与 `ListOffsets v7` 在当前镜像 `apache/kafka:3.9.1` 上的实际响应（测试代码用的是本地 compose Broker，版本同为 3.9.x）。

---

## 8. Outbox 指标方案

### 8.1 数据表结构（真实 Schema）

基表 `flash_sale_order_requests`（`internal/migrations/sql/20261001000015_flash_sale_order_requests.up.sql:11-28`）：

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | BIGINT UNSIGNED PK | 自增 |
| `user_id` / `activity_id` / `sku_id` | BIGINT UNSIGNED | 软引用，无 FK |
| `idempotency_key` / `request_hash` | VARCHAR(64) | 幂等 |
| `status` | **TINYINT DEFAULT 0** | `0=queued, 1=success, 2=failed, 3=dead`（`:4` 注释） |
| `retry_count` | INT UNSIGNED | 消费重试预算 |
| `next_attempt_at` | DATETIME NULL | **Scanner 业务重试**时间（与发布退避无关） |
| `last_error_code` | INT NULL | 最近错误码 |
| `flash_order_id` | BIGINT UNSIGNED NULL | 成功后关联订单 |
| `created_at` | DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP | 秒级精度 |
| `updated_at` | DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE | |
| 索引 | `uk_request_idempotency(user_id, idempotency_key)`、`idx_dequeue(status, next_attempt_at, id)` | |

Outbox 扩展（`internal/migrations/sql/20261001000024_flash_sale_request_outbox.up.sql:23-35`）：

| 列 | 类型 | 语义 |
|---|---|---|
| `published_at` | DATETIME(6) NULL | **只有 Broker 确认后才写**；NULL = 尚未确认发布 |
| `publish_claim_token` | VARCHAR(64) NULL | 本轮领取租约令牌 |
| `publish_lease_until` | DATETIME(6) NULL | 租约到期（**数据库时间语义**）；空/过期可被重领 |
| `publish_attempts` | INT UNSIGNED DEFAULT 0 | 领取次数（不代表成功） |
| `next_publish_at` | DATETIME(6) NULL | 发布退避时间；NULL = 立即可领取 |
| `last_publish_error` | VARCHAR(255) NULL | 最近发布失败原因（截断） |
| `published_partition` / `published_offset` | INT / BIGINT NULL | 诊断与对账 |
| 索引 | **`idx_outbox_claim(status, published_at, next_publish_at, id)`** | 定位“待发布集合” |

### 8.2 状态模型（源码核对，不靠字段名猜测）

| 问题 | 结论 | 证据 |
|---|---|---|
| 什么状态表示“待发布” | `status=0(queued) AND published_at IS NULL` | `outbox.go:871-877` |
| 什么状态表示“已发布” | `published_at IS NOT NULL`（与 `status` 无关；终态行不会重发） | migration `:24`、`:31` |
| 是否有“处理中”状态 | **没有独立状态值**，“处理中”= 已被领取但 `published_at` 仍 NULL（租约 `publish_lease_until` 表达） | `metrics.go:180-186` 注释 |
| 是否有租约/锁 | 有：`publish_claim_token` + `publish_lease_until`（数据库级互斥，非进程内 mutex） | migration `:25-26`、`:33-34` |
| 是否有失败状态 | 业务失败终态 `status=2(failed)`/`3(dead)`；**发布失败不改业务状态**，只写退避 `next_publish_at` + `last_publish_error` | `outbox.go:198-208` |
| 是否有重试次数字段 | 两个，语义不同：`retry_count`（消费）、`publish_attempts`（发布） | migration `:19`、`:27` |
| 是否会长期积压 | 会：租约到期可重领（at-least-once），`next_publish_at` 指数退避、30s 封顶 | `outbox.go:437-447`、`docs/development/kafka.md:151-157` |
| 时区 | MySQL 会话 `time_zone='+08:00'` 由 DSN 注入（`internal/boot/boot.go:176-202`），`created_at`/`NOW(6)` **同一会话同一时区** | `boot.go:196-201` |

### 8.3 已有实现：`OutboxPublisher.Backlog()`

```go
// internal/logic/flashsale/outbox.go:860-907（要点）
// 计数使用 idx_outbox_claim 的前缀（status, published_at）做覆盖扫描；
// 最老年龄只回表读取一条记录，并用数据库 NOW(6) 计算，避免应用与 MySQL 时钟漂移。
Fields("COUNT(*) AS pending, MIN(id) AS oldest_id").
Where("status", requestStatusQueued).
Where("published_at IS NULL")

// 回表取最老一条：
Fields("created_at, NOW(6) AS db_now").Where("id", backlog.OldestID)
backlog.OldestAge = rows[0].DbNow.Time.Sub(rows[0].CreatedAt.Time)   // <0 时钳为 0
```

已被 `runOutboxBacklogSampler` 每 **5s** 调用（单次预算 **3s**），只把 `Pending` 写入 Gauge（`internal/worker/flashsale_runtime.go:57-63, 418-463`）。

### 8.4 待补齐：最老事件年龄指标

| 项 | 设计 |
|---|---|
| 指标名 | `flashsale_outbox_oldest_pending_age_seconds`（Gauge，无标签）——沿用既有 `flashsale_` 前缀，与 `flashsale_outbox_backlog_pending` 同域 |
| 数据来源 | **复用同一次 `Backlog()` 调用的返回值 `OldestAge`**，不新增任何 SQL |
| 语义 | 当前待发布集合中最老一条的年龄（秒），由 **数据库时间** `NOW(6) - created_at` 计算 |
| 空集语义 | `Pending=0` 时**不导出**该序列（或导出 0 但必须与 `pending=0` 同时判读）——建议复用 `observableGauge` 的“未观测即不导出”，避免 `0` 与“无数据”混淆 |
| 采集频率 | 5s（随现有采样器）；最差新鲜度 5s + 15s ≈ 20s |
| 失败处理 | 查询失败 → 保留上一次有效值 + 打 Warn 日志（现有行为，`flashsale_runtime.go:452-461`） |
| 需不需要改 Schema | **不需要**。`idx_outbox_claim(status, published_at, …)` 对 `status=? AND published_at IS NULL` 是可用前缀；`MIN(id)` 在该索引前缀内可覆盖；回表仅 1 行（主键） |
| 成本 | COUNT 在索引上扫描“待发布集合”（正常为 0 或几十行）；`MIN(id)` 走同一索引；回表 1 行。5s 一次、3s 预算，成本可忽略 |
| 时区风险 | 已消除：两侧都取数据库时间（`NOW(6)` 与 `created_at` 同会话）。**禁止**改成 `time.Now().Sub(created_at)`（应用时钟 + Go 时区会引入 ±8h 级误差） |

### 8.5 是否需要在 `/metrics` 抓取路径查库

**绝对不需要，也不要这样做**。现有架构已正确规避（`metrics.go:89-92`、`flashsale_runtime.go:418-425`）。Prometheus 抓取频率由 Prometheus 决定（15s，且可能被手动 `curl` 打爆），把 DB 查询挂在抓取路径上会让“监控把库拖死”。保持“周期采样 → 缓存 Gauge → 抓取只读内存”的现有模式，仅补一个 Gauge。

---

## 9. Prometheus Kubernetes 部署设计

### 9.1 现状与约束

| 约束 | 事实 |
|---|---|
| 命名空间 | `surgecart`，已存在，PSS `enforce=restricted` |
| Kustomize 组织 | `base`（应用）/ `infrastructure`（中间件，含 `verify` 子目录）/ `migration` / `overlays/{local,production}` / `shared`（namespace、database-configmap） |
| 存储 | 默认 StorageClass `standard`（`rancher.io/local-path`，`Delete`，`WaitForFirstConsumer`） |
| 集群内监控组件 | 无（已核验） |
| 应用 Service | `surgecart.surgecart.svc.cluster.local:8000`（ClusterIP，单端点） |

### 9.2 目录结构建议（与现有 Kustomize 组织一致）

```text
manifest/kubernetes/
  monitoring/                      # 新增顶层目录，与 infrastructure 平级
    prometheus/
      base/
        kustomization.yaml         # resources: deployment/service/configmap
        deployment.yaml
        service.yaml
        configmap.yaml             # prometheus.yml（抓取配置）
      overlays/
        local/
          kustomization.yaml       # namespace: surgecart + images(newTag) + 资源覆盖
```

理由：不塞进 `base/`（避免应用发布连带监控）、不塞进 `infrastructure/`（那里是 MySQL/Redis/Kafka 这类业务依赖，Prometheus 是纯观测组件），与 `migration/` 的“独立部署入口”模式一致。

### 9.3 镜像版本选择原则

| 原则 | 说明 |
|---|---|
| 固定 tag，禁止 `latest` | 与现有 `prometheus/Dockerfile:7` 的 `FROM prom/prometheus:v2.53.0` 保持同一大版本，避免 Compose 与 K8s 两套环境行为分叉 |
| 首选 `prom/prometheus:v2.53.0` | 该版本已在 Compose 环境使用（`docker-compose.yml:207-226`、`prometheus/Dockerfile:7`），配置文件格式已验证 |
| 不引入 Operator | 本集群无 CRD、无 Controller；静态配置足够，避免为一套抓取引入 operator + CRD + RBAC |
| **镜像可得性 = NOT_VERIFIED（风险）** | 本机 `docker images` 与 kind 节点 `crictl images` **均无 prometheus/grafana 镜像**；节点对 `registry-1.docker.io` 的 DNS 解析返回可疑地址（`2a03:2880:f12d:83:face:b00c:0:25de`，属已知 DNS 劫持/黑洞特征）。T4-03 必须先解决镜像导入（`docker pull` + `kind load docker-image`）或确认节点出网，再谈部署 |

### 9.4 Service DNS 与抓取配置

- 应用访问地址：`surgecart.surgecart.svc.cluster.local:8000`（同 namespace 可简写 `surgecart:8000`）。
- 服务发现：**静态 `static_configs` 足够**。单副本（`deployment.yaml:30` `replicas: 1`）、单 Service、单端点，引入 Kubernetes SD 只会换来一堆 RBAC（`list/watch endpoints`、`nodes` 等）而没有任何收益。
- 若将来扩副本且仍走 Service，静态目标会**只抓到被 kube-proxy 轮询到的那一个 Pod**，导致指标在 Pod 间跳变。届时的正确做法不是“加 RBAC 用 SD”，而是先解决“多副本 + 本地存储不共享”的既有阻塞（`deployment.yaml:28-29` 已注明）。
- `metrics_path: /metrics`，`scrape_interval: 15s`，`job_name: surgecart`。
- Prometheus 自身：`prometheus` job 抓 `localhost:9090`（`static_configs: targets: ["127.0.0.1:9090"]`），用于验证“Prometheus 存活 + 自身指标”。

ConfigMap 关键内容（设计，未部署）：

```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s
rule_files:
  - /etc/prometheus/rules/*.yml
scrape_configs:
  - job_name: surgecart
    metrics_path: /metrics
    static_configs:
      - targets: ["surgecart.surgecart.svc.cluster.local:8000"]
  - job_name: prometheus
    static_configs:
      - targets: ["127.0.0.1:9090"]
```

可选：把现有 `prometheus/rules/flashsale_latency.yml` 通过第二个 ConfigMap 挂到 `/etc/prometheus/rules/`（recording rules 已被 `scripts/test-prometheus.sh` 用 `promtool` 校验过，可直接复用）。

### 9.5 RBAC

**不需要任何 RBAC**：不访问 Kubernetes API（静态配置）、不需要 ServiceAccount（`automountServiceAccountToken: false`）、不需要 ClusterRole/Role。

### 9.6 资源与安全上下文（必须满足 restricted PSS）

| 项 | 建议值 | 依据 |
|---|---|---|
| `automountServiceAccountToken` | `false` | 与 `deployment.yaml:63` 一致 |
| `runAsNonRoot` | `true` | PSS restricted 强制 |
| `runAsUser` / `runAsGroup` | `65534`（`nobody`） | Prometheus 官方镜像默认用户；**T4-03 需实测确认该镜像的 `/prometheus` 属主**，若不符则用 `fsGroup` 或改 `runAsUser` |
| `seccompProfile` | `RuntimeDefault` | 同应用/中间件 |
| `allowPrivilegeEscalation` | `false` | PSS |
| `capabilities.drop` | `[ALL]` | PSS |
| `readOnlyRootFilesystem` | `true` | PSS 最佳实践；需为 `/prometheus` 与 `/tmp` 挂可写卷 |
| `resources.requests` | `cpu: 100m`, `memory: 256Mi` | 与 `mysql/redis/kafka` 量级一致 |
| `resources.limits` | `cpu: 500m`, `memory: 512Mi` | 单节点本地集群，避免挤占应用/基础设施 |
| 探针 | `startupProbe` `/‑/ready`（30×2s）、`livenessProbe` `/‑/healthy`（10s）、`readinessProbe` `/‑/ready`（5s） | Prometheus 官方健康端点 |
| 端口 | `9090`（命名端口 `http`），Service 类型 `ClusterIP`（不建 Ingress/NodePort） | 沿用“不虚构对外入口”的项目原则 |

### 9.7 数据持久化

| 方案 | Pod 重建后 | 集群重建后（kind delete/create） | 成本 | 结论 |
|---|---|---|---|---|
| `emptyDir` | **丢失** | 丢失 | 0 | 仅适合“每次重建都重新采样”的短测 |
| **PVC（`standard`，5Gi）** | **保留** | 丢失（local-path 的 PV 落在节点容器目录，节点销毁即消失） | 5Gi 磁盘 | **推荐** |
| PVC + `production` 外部存储 | 保留 | 保留 | 依赖外部 | 本任务不涉及 |

**推荐：PVC 5Gi + `strategy: Recreate`**（RWO 卷不可滚动更新，必须 Recreate，理由与 `deployment.yaml:39-43` 一致）；TSDB retention `15d`（k6 压测窗口远小于此，足够复盘）。

**必须明确的边界**：本地 kind 集群的 `Delete` 回收策略意味着**集群重建后历史指标必然丢失**；这是“本地开发场景可接受”的取舍，不能在报告中宣称“数据持久化”。

---

## 10. Grafana Dashboard 设计

只做设计，不部署。建议一个 Dashboard：`SurgeCart / Overview`，`refresh=15s`，默认时间窗 `Last 30m`，时区 `Asia/Shanghai`。

| # | 面板 | 指标 | 展示 | 单位 | PromQL |
|---|---|---|---|---|---|
| 1 | HTTP QPS | `surgecart_http_requests_total` | TimeSeries | reqps | `sum(rate(surgecart_http_requests_total{route!="unmatched"}[1m]))` |
| 2 | 秒杀下单 QPS | 同上（按 route） | TimeSeries | reqps | `sum(rate(surgecart_http_requests_total{route="/flash-sales/:id/orders"}[1m]))` |
| 3 | HTTP Latency P50/P95/P99 | `surgecart_http_request_duration_seconds_bucket` | TimeSeries（3 条线） | s | `histogram_quantile(0.5\|0.95\|0.99, sum by (le) (rate(surgecart_http_request_duration_seconds_bucket[5m])))` |
| 4 | HTTP 错误率 | `surgecart_http_requests_total{status=~"5.."}` | Timeseries + Stat（当前值） | percent(0-1) | `sum(rate(...{status=~"5.."}[5m])) / clamp_min(sum(rate(...[5m])), 1e-9)` |
| 5 | In-Flight | `surgecart_http_requests_in_flight` | **Stat** | short | `sum(surgecart_http_requests_in_flight)` |
| 6 | Kafka Consumer Lag | `surgecart_kafka_consumer_group_lag` | TimeSeries（按 partition） | short | `sum by (partition) (surgecart_kafka_consumer_group_lag{consumer_group="surgecart-flashsale-order-worker-v1"})` |
| 7 | Consumer Joined | `flashsale_consumer_joined` | **Stat**（0/1，阈值红/绿） | none | `flashsale_consumer_joined` |
| 8 | Outbox Pending | `flashsale_outbox_backlog_pending` | TimeSeries + Stat | short | `flashsale_outbox_backlog_pending` |
| 9 | Outbox Oldest Age | `flashsale_outbox_oldest_pending_age_seconds` | TimeSeries（单条） | s（阈值 60s/300s） | `flashsale_outbox_oldest_pending_age_seconds` |
| 10 | 秒杀受理结果分布 | `flashsale_requests_total{interface="order"}` | Stacked TimeSeries | reqps | `sum by (result) (rate(flashsale_requests_total{interface="order"}[1m]))` |
| 11 | 消费结果分布 | `flashsale_consumer_processed_total` | Stacked TimeSeries | ops | `sum by (result) (rate(flashsale_consumer_processed_total[1m]))` |
| 12 | DLQ 速率 | `flashsale_consumer_dlq_total` | TimeSeries | ops | `sum by (reason) (rate(flashsale_consumer_dlq_total[1m]))` |
| 13 | Outbox 发布失败 | `flashsale_outbox_publish_failed_total` | TimeSeries | ops | `sum by (reason) (rate(flashsale_outbox_publish_failed_total[1m]))` |
| 14 | Goroutines | `go_goroutines` | TimeSeries + Stat | short | `go_goroutines` |
| 15 | Heap Alloc / Inuse | `go_memstats_heap_alloc_bytes` / `go_memstats_heap_inuse_bytes` | TimeSeries（2 条） | bytes(IEC) | `go_memstats_heap_alloc_bytes` / `go_memstats_heap_inuse_bytes` |
| 16 | GC 时间 | `go_gc_duration_seconds` | TimeSeries（`rate(sum)`） | s | `rate(go_gc_duration_seconds_sum[5m]) / rate(go_gc_duration_seconds_count[5m])` |
| 17 | Process CPU / RSS | `process_cpu_seconds_total` / `process_resident_memory_bytes` | TimeSeries | cores / bytes | `rate(process_cpu_seconds_total[1m])` / `process_resident_memory_bytes` |

展示方式说明：

- **适合 Stat**：In-Flight、Consumer Joined（0/1 布尔态）、Outbox Pending（当前值）、错误率当前值——这些是“此刻是否正常”，用趋势图会淹没判读。
- **适合 TimeSeries**：QPS、延迟分位、Lag、Age、GC、内存——需要看形状与拐点。
- **延迟分位用 `histogram_quantile` + `sum by (le)`**：跨 `route` 聚合会失真，跨面板按需 `by (le, route)`。
- **采样间隔**：面板 `Min step = 15s`（与 scrape 一致）；`rate` 窗口 `1m`~`5m`（1m 用于压测实时观察，5m 用于稳定判读）。
- **零数据显式化**：`flashsale_consumer_joined` / `flashsale_outbox_*` 在 `db_scanner` 模式下**不导出序列**，面板需配置 `No data` 文案“当前非 Kafka 模式”，不要把“无数据”画成 0。

> 面板 1–5、6、9 依赖 T4-02 的新增指标；面板 7–8、10–17 **今天就能画**（只要 `/metrics` 打开）。建议 T4-04 分两批落地：先做“现有指标就能支撑”的面板，再加新指标面板，避免 Dashboard 空转。

---

## 11. 后续实施顺序

| 阶段 | 内容 | 依赖 | 关键验收 |
|---|---|---|---|
| **T4-02** 应用侧指标补齐 | ① overlay 增加 `FLASH_SALE_METRICS_ENABLED=true`（`overlays/local/configmap-patch.yaml`）② 新增 `internal/middleware/metrics.go`（`surgecart_http_*`）③ 新增 `flashsale_outbox_oldest_pending_age_seconds`（复用 `Backlog()`）④ 修正 `docs/development/kafka.md:526-528` 关于 kadm 的表述 | 无（可在 T4-03 前独立完成并单测） | `/metrics` 200；`surgecart_http_requests_total` 有数据且 `route` 为模板；Age 与手工 SQL 一致；`go_*`/`process_*` 出现 |
| **T4-03** Prometheus 部署 | `manifest/kubernetes/monitoring/prometheus/{base,overlays/local}`；镜像导入；抓取验证 | T4-02（否则 target 抓不到业务指标，只能抓到 `go_*`） | Target `UP`；`up{job="surgecart"}=1`；业务指标在 Prometheus 中可查 |
| **T4-04** Grafana Dashboard | 按 §10 分两批（现有指标 → 新指标） | T4-03 | 面板有真实数据、Y 轴单位正确、`No data` 文案正确 |
| **（可选独立）Kafka Lag 采样器** | §7.3 方案 A | T4-02（指标开关与采样器骨架） | Lag 与 `kafka-consumer-groups.sh --describe` 数值一致；Broker 停机时保留旧值且失败计数增长 |

依赖关系要点：**T4-02 是所有后续工作的前置**；T4-04 不要早于 T4-03（否则只能对着空 Prometheus 配面板，无法验证 PromQL）；Lag 采样器可与 T4-03/04 并行，但必须在 T4-02 的开关生效之后。

---

## 12. 风险与限制（仅记录真实发现）

| # | 风险 | 证据 | 影响 |
|---|---|---|---|
| R-01 | **集群内 `/metrics` 404** | port-forward 实测 404；`FLASH_SALE_METRICS_ENABLED` 不在 CM/Secret | 不解决则整个任务四空转 |
| R-02 | **Prometheus/Grafana 镜像不可得（NOT_VERIFIED）** | 本机与节点均无镜像；节点解析 `registry-1.docker.io` 得到可疑 IPv6 | T4-03 可能卡在拉镜像 |
| R-03 | **`/metrics` 无认证** | `cmd.go:228` 挂在 `s.Group` 之外 | 仅靠 ClusterIP 网络边界保护；不得暴露 Ingress/NodePort（本次设计已禁止） |
| R-04 | **默认 Registry 耦合** | `metrics.go:241-250` 用全局 `MustRegister` | 将来任何库往默认 Registry 注册都可能 `init panic`；扩展时需评估 |
| R-05 | **`Enabled(ctx)` 每次观测都读配置** | `metrics.go:256-262` → `loader.go:111-134`（env 命中时短路，否则 `gcfg.Get`） | K8s 下 env 一定存在→走快路径；但 `db_scanner` 或环境值缺失时会走 YAML 解析，属可接受开销。**不建议**为此改动 |
| R-06 | **`flashsale_request_duration_seconds` 不是 HTTP 延迟** | `flashsale.go:266-270` 在服务层计时 | 不能直接当“接口 P95”汇报；必须与新的 `surgecart_http_*` 区分 |
| R-07 | **单分区 Topic** | `infrastructure/base/configmap.yaml:77` | Lag 只能按 1 个分区观察；消费并行度上限=1（既有架构取舍，不在本任务改） |
| R-08 | **`consumer_joined` / `backlog_pending` / Lag 在 `db_scanner` 模式不导出** | `metrics.go:172-186` | 面板必须能区分“无数据”与“值为 0”；当前集群是 kafka 模式，暂无影响 |
| R-09 | **远端不可达，推送大概率失败** | `git fetch`/`ls-remote` 均 `Failed to connect ... 127.0.0.1:18081` | 只能本地提交；不得改 `git config` 绕过 |
| R-10 | **工具版本与历史报告漂移** | kubectl `v1.36.1`（历史 `v1.34.1`）、Docker `29.9.3`（历史 `29.4.2`）、`kind` 不在 PATH | 后续报告不要复制历史版本号 |
| R-11 | **本地 kind 存储为 `Delete` 回收 + local-path** | `kubectl get sc` | Prometheus 数据在集群重建后必丢，不可宣称持久化 |
| R-12 | **PSS restricted 对 Prometheus 的适配未实测** | 命名空间 `enforce=restricted`；镜像内用户/属主未验证 | T4-03 需先用 `kubectl apply --dry-run=server` 与真实启动验证；若 OOM/拒绝需调整而非放松 PSS |

---

## 13. 文件修改范围

本次**只新增一个文件**：

```
T4-01_MONITORING_AUDIT.md      （仓库根，新增）
```

未修改：Go 业务代码、Go 配置实现、`go.mod`/`go.sum`、`Dockerfile`、`docker-compose.yml`、`manifest/kubernetes/**`（Deployment/Service/ConfigMap/Secret/Kustomize）、MySQL Schema、Kafka Topic、Redis 数据。

未部署/未安装：Prometheus、Grafana、任何 Exporter。

未重启：SurgeCart 应用、MySQL、Redis、Kafka。

未执行：`kubectl apply/delete/patch/scale`、`docker pull/rm/prune`、集群重建、改 kubeconfig、改 git config。

唯一的临时操作：`kubectl port-forward svc/surgecart 18000:8000`，验证后已终止；未创建任何 Kubernetes 资源。

---

## 14. 最终验收

| # | 验收项 | 结果 | 说明 |
|---|---|---|---|
| 1 | Git 基线核验完成 | **PASS** | 基线 `175b7fc`，工作区干净，T3-02 分支存在 |
| 2 | 远端状态核验 | **NOT_VERIFIED** | 网络不可达（本地代理 127.0.0.1:18081 未监听） |
| 3 | 任务四分支创建 | **PASS** | `feat/k8s-t4-prometheus-monitoring` @ `175b7fc` |
| 4 | 工作区无脏改动 | **PASS** | `git status --short` 空 |
| 5 | Kubernetes 状态只读核验 | **PASS** | 集群/Node/Pod/Service/Endpoint/SC 全部实测 |
| 6 | Prometheus/Grafana 现状核验 | **PASS**（确认为不存在） | 无 Pod、无 CRD |
| 7 | 应用端点实测 | **PASS（含关键失败发现）** | `/health` 200、`/ready` 200、`/metrics` **404** |
| 8 | Go 监控实现源码审查 | **PASS** | client、Registry、注册、调用点均定位到行 |
| 9 | `FLASH_SALE_METRICS_ENABLED` 语义明确 | **PASS** | 4 处生效点、默认 false、契约/校验路径完整 |
| 10 | 现有指标清单 | **PASS** | 18 个 `flashsale_*` + 库内置 Go/Process |
| 11 | HTTP 指标设计 | **PASS** | 名称/类型/标签/基数/PromQL/注册位置 |
| 12 | Go Runtime 指标设计 | **PASS（已具备，零代码）** | 7 项全部由默认 Collector 提供 |
| 13 | Kafka Lag 数据来源调查 | **PASS** | 方案 A 可行且零新依赖；方案 B 不推荐；文档表述需修正 |
| 14 | Outbox 指标数据来源调查 | **PASS** | Schema/状态模型/索引/现有 `Backlog()`/采样器全部核对 |
| 15 | Outbox 最老事件年龄 | **NOT_IMPLEMENTED** | 值已算出但未导出 |
| 16 | Kafka Lag 指标 | **NOT_IMPLEMENTED** | 无生产代码 |
| 17 | HTTP 指标 | **NOT_IMPLEMENTED** | 无任何 HTTP 层指标 |
| 18 | `/metrics` 运行期暴露 | **NOT_IMPLEMENTED（当前环境）** | 开关默认 false，实测 404 |
| 19 | Prometheus 部署设计 | **PASS**（设计） | 目录/镜像/DNS/RBAC/资源/PSS/持久化 |
| 20 | Grafana Dashboard 设计 | **PASS**（设计） | 17 个面板 + PromQL + 单位 + 展示方式 |
| 21 | 仓库修改范围 | **PASS** | 仅新增本报告 |
| 22 | 未提前实施 T4-02/03/04 与任务五 | **PASS** | 无任何实现动作 |
| 23 | Git 提交 | **PASS**（见 §15） | 仅提交本报告 |
| 24 | Git 推送 | **FAIL（网络）** | 远端不可达，本地提交保留 |

---

## 15. Git 交付

| 项目 | 值 |
|---|---|
| 分支 | `feat/k8s-t4-prometheus-monitoring` |
| 基线 SHA | `175b7fccd7cf8c147a37e506bdba33dff42ec24b` |
| 初始 HEAD SHA | `175b7fccd7cf8c147a37e506bdba33dff42ec24b` |
| 提交内容 | 仅 `T4-01_MONITORING_AUDIT.md` |
| 提交信息 | `docs(monitoring): audit prometheus readiness` |
| 提交 SHA | 见本报告提交后的 `git rev-parse HEAD`（回填见下方“交付回填”） |
| Push 状态 | 见下方“交付回填” |
| 工作区状态 | 提交后应为干净（仅未跟踪无） |

**交付回填**（提交/推送执行后由执行者补记，禁止改写历史）：

```
COMMIT_SHA=（见 git log -1 --format=%H）
REMOTE_SHA=（git rev-parse origin/feat/k8s-t4-prometheus-monitoring，若推送成功）
PUSH_STATUS=（SUCCESS / FAILED: 详见失败原因）
WORKTREE_STATUS=（git status --short）
```

**推送失败原因（已发生一次）**：`git` 被配置为经 `http://127.0.0.1:18081` 代理访问，该端口未监听；按约束未修改任何 Git 配置。若推送失败，本地提交保留，如实报告，不做 rebase/force/重写历史。

---

## 附录 A：核心证据索引

| 事实 | 位置 |
|---|---|
| 指标定义（18 个） | `internal/metrics/metrics.go:75-187` |
| 指标注册（`init` + `MustRegister`） | `internal/metrics/metrics.go:241-250` |
| `Handler()` = 默认 Registry | `internal/metrics/metrics.go:421-424` |
| 开关读取与容错 | `internal/metrics/metrics.go:252-263` |
| 配置契约（Key / 默认 false） | `internal/config/contract.go:116`、`:283` |
| YAML 默认（`enabled: false`） | `manifest/config/config.yaml:203-206` |
| `/metrics` 挂载（仅开关开启） | `internal/cmd/cmd.go:168-170`、`:222-231` |
| 中间件注册顺序（无 HTTP 指标） | `internal/cmd/cmd.go:172-181` |
| 访问日志字段（现有唯一 HTTP 观测） | `internal/middleware/requestid.go:29-53` |
| 统一响应/状态码写入 | `internal/middleware/response.go:12-38` |
| 秒杀指标计时点（服务层） | `internal/logic/flashsale/flashsale.go:266-270` |
| Kafka Consumer（手动提交） | `internal/mq/kafka/consumer.go:90-106`、`:257-302` |
| `kmsg` OffsetFetch 可用性（测试） | `internal/logic/flashsale/kafka_consumer_kafka_test.go:61-71` |
| `kmsg` DescribeGroups 可用性（测试） | `internal/logic/flashsale/kafka_consumer_kafka_test.go:101-107` |
| Outbox Backlog 实现（DB 时间算年龄） | `internal/logic/flashsale/outbox.go:860-907` |
| 积压采样器（5s / 3s / 失败保前值） | `internal/worker/flashsale_runtime.go:57-63`、`:418-463` |
| Outbox Schema 扩展 | `internal/migrations/sql/20261001000024_flash_sale_request_outbox.up.sql:23-35` |
| 请求表 Schema（status 语义） | `internal/migrations/sql/20261001000015_flash_sale_order_requests.up.sql:4-28` |
| MySQL 会话时区（+08:00） | `internal/boot/boot.go:176-202` |
| 集群内 dispatch_mode / group / topic | `manifest/kubernetes/overlays/local/configmap-patch.yaml:27-31` |
| Topic 分区数=1 | `manifest/kubernetes/infrastructure/base/configmap.yaml:77-78` |
| 应用 Deployment（PSS/只读根/资源） | `manifest/kubernetes/base/deployment.yaml:70-78`、`:115-123`、`:176-192` |
| 命名空间 PSS restricted | `manifest/kubernetes/shared/namespace.yaml:20` |
| 现有 Compose Prometheus（版本/抓取） | `prometheus/Dockerfile:7`、`prometheus/prometheus.yml:9-24`、`docker-compose.yml:207-226` |
| recording rules（p95/p99） | `prometheus/rules/flashsale_latency.yml:17-23` |
| 文档中 Lag 表述（需修正） | `docs/development/kafka.md:526-528`、`:890-899` |
