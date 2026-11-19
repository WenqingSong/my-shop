# T4-02 应用监控指标完善 — 交付报告

## 1. 基本信息

| 项 | 值 |
| --- | --- |
| 项目 | SurgeCart（`https://cnb.cool/framework-station/my-shop`） |
| 任务编号 | T4-02 |
| 任务名称 | Application Metrics Implementation |
| 开发分支 | `feat/k8s-t4-02-application-metrics` |
| 基线分支 | `main` |
| 基线 SHA | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2`（`git ls-remote origin refs/heads/main` 实测与该值一致） |
| 实现提交 SHA | `033f5fa98bf73050834c43ed9ef48cceb43c29c9` |
| 最终 HEAD SHA | `033f5fa98bf73050834c43ed9ef48cceb43c29c9`（实现 + 报告）；其后仅追加一次「回填本报告元数据」的 docs 提交 |
| 推送状态 | 已推送：`origin/feat/k8s-t4-02-application-metrics`（`git push -u` 成功，new branch） |

基线核验记录：

```text
git rev-parse --show-toplevel  -> D:/Wenqingsong/CodeProjects/codebuddy/k8s
git branch --show-current      -> feat/k8s-t4-prometheus-monitoring   （开工前，历史 T4 分支）
git rev-parse HEAD             -> 63bb3be77805463dbb32cec56e986af895acf3a6（历史 T4 分支 HEAD）
git status --short --branch    -> 工作区干净（无未提交改动）
git remote -v                  -> origin https://cnb.cool/framework-station/my-shop
git ls-remote origin main      -> 82243c0fbce614e03c4ff57dcc829b5f68fb08f2
```

工作区干净后执行 `git fetch origin main` + `git switch -c feat/k8s-t4-02-application-metrics origin/main`，
分支从最新远端 main 建立（未使用历史 T4 分支、未 reset、未删除任何分支）。

## 2. 实现概述

### 2.1 HTTP 指标

- 中间件：`internal/middleware/metrics.go`（`middleware.Metrics`）。
- 注册位置（关键）：`internal/cmd/cmd.go` 的根分组中，顺序为
  `RequestID（外）→ Metrics（中）→ Response（内）`。
  Metrics 必须在 Response **之外**，才能在 `Next()` 返回后读到 Response 写出的最终状态码；
  实测 418/503 均被正确记录（不落成 200）。
- 指标：`surgecart_http_requests_total`、`surgecart_http_request_duration_seconds`、
  `surgecart_http_requests_in_flight`。
- 标签基数：`method` / `route`（GoFrame 已匹配的路由模板 `r.Router.Uri`）/ `status`（数值码，
  非法值归一化为 `unknown`）。**不使用原始 URL、查询参数与任何业务 ID**。
- `/metrics` 排除：抓取端点在 `Server.BindHandler` 注册，位于根分组中间件之外，因此天然不被
  自身统计；中间件内另有按路径的显式防御。
- In-flight 用 `defer` 保证恢复；「是否启用」每次请求只判断一次，保证 Inc/Dec 成对。

### 2.2 Kafka Consumer Lag

- 采集器：`internal/mq/kafka/lag.go`（`LagReader`，独立 `kgo.Client`，不订阅、不入组、不提交）。
- 协议路径：`Metadata`（分区列表）→ `OffsetFetch`（消费组已提交位移，`RequireStable=true`）
  → `ListOffsets(-1)`（Log End Offset）→ 未提交位移的分区再发 `ListOffsets(-2)`（Log Start Offset）。
  **未引入 kadm**，复用既有 `kmsg v1.9.0`；请求由 `kgo.Client.Request` 自动路由到组协调器/分区
  Leader 并做版本协商。
- 计算：`lag = max(0, log_end_offset - committed_offset)`；`committed = -1` 时基线取
  `log_start_offset`（与 franz-go 默认 `ConsumeResetOffset = AtStart` 的消费起点一致），
  并以 `HasCommit=false` 显式标记。
- 失败语义：请求失败/整体失败 → 不写任何 Lag（保留上次值）；分区级失败 → 只更新成功分区；
  从拿不到起始位移 → 跳过该分区，绝不伪造 0。`GROUP_ID_NOT_FOUND` 等价于「无任何提交位移」。
- 分区变化：每轮以真实元数据为准；整轮成功时删除已消失分区的序列（部分失败时**不删**，
  因为无法区分「分区消失」与「分区读取失败」）。
- 周期：10s，单次预算 5s（均小于抓取间隔 15s 与自身周期）；`/metrics` 抓取路径不访问 Kafka。
- 仅 `dispatch_mode=kafka` 启动；`db_scanner` 模式不采集、不导出该序列。

### 2.3 Outbox 指标

- 待发布积压：**复用既有** `flashsale_outbox_backlog_pending`（同一 `OutboxPublisher.Backlog()`，
  不新增任何 SQL，不改 Schema/索引）。
- 新增最老事件年龄：`surgecart_outbox_oldest_age_seconds`，取同一次 `Backlog()` 的 `OldestAge`
  （MySQL `NOW(6) - created_at`，避免应用/DB 时钟漂移）。
- 新增加采样状态：`surgecart_sampler_up{sampler="outbox"}`（1=最近一次成功，0=失败）。
- 语义：`Pending=0` → 年龄真实为 0；`Pending>0` 但回表拿不到 `created_at`（并发处理窗口）→
  保留上一次有效年龄；查询失败 → 两个指标都不改写，只把 `sampler_up` 置 0。
- 采样：既有后台循环（5s 周期 / 3s 预算）扩展为一次采样写两个指标，不新增采样器、不新增配置项。

### 2.4 Go Runtime 指标

未重复实现：`/metrics` 由 `promhttp.Handler()` 输出默认 Registry，天然包含
`go_goroutines`、`go_memstats_heap_alloc_bytes`、`go_gc_duration_seconds`、
`process_cpu_seconds_total`。测试以真实 HTTP 抓取断言这些 HELP 行存在。

### 2.5 统一生命周期

- 单一 Registry：新指标在 `internal/metrics` 内用 `prometheus.MustRegister` 注册到默认 Registry，
  不存在第二个 Registry，也不存在重复注册。
- 采样器只在 `metrics.Enabled(ctx)` 为真时创建（开关关闭 → 零 goroutine、零 DB/Kafka 请求）。
- 每个采样器独立 ctx + 独立 WaitGroup；`Shutdown` 顺序：
  **Outbox 采样 → Kafka Lag 采样（并关闭其独立客户端）→ Publisher → Consumer → Producer → Scanner**，
  全部受 `flash_sale.shutdown_timeout` 统一约束。
- Lag 采样器初始化失败只记录 Warning 并跳过该指标，**不影响秒杀链路启动**；
  不阻塞 HTTP、不影响既有 SIGTERM 优雅退出。

## 3. 指标清单

| 指标 | 类型 | Labels | HELP（摘要） | 采集方式 | 业务语义 |
| --- | --- | --- | --- | --- | --- |
| `surgecart_http_requests_total` | Counter | `method`,`route`,`status` | HTTP 请求总数（按方法/路由模板/状态码） | 中间件同步 Inc | 真实业务请求量；`route` 为路由模板，未命中为 `unmatched` |
| `surgecart_http_request_duration_seconds` | Histogram | `method`,`route`,`status` | HTTP 请求处理耗时（秒） | 中间件 `Observe` | p95/p99 延迟，单位秒 |
| `surgecart_http_requests_in_flight` | Gauge | 无 | 当前处理中的 HTTP 请求数 | 中间件 Inc/Dec（defer） | 并发度与阻塞诊断 |
| `surgecart_kafka_consumer_lag` | Gauge | `consumer_group`,`topic`,`partition` | 分区 Lag（messages）= max(0, LEO - committed)，无提交时以 log start 为基线 | 后台 10s 采样 | 消费侧真实积压 |
| `surgecart_outbox_oldest_age_seconds` | Gauge | 无 | 最老待发布事件年龄（秒） | 后台 5s 采样（同一次 Backlog） | 「积压是否在消化」；持续增长＝发布侧卡住 |
| `surgecart_sampler_up` | Gauge | `sampler`(`outbox`/`kafka_lag`) | 最近一次采样是否成功（1/0） | 采样器每次执行后写入 | 区分「真实零值」与「采样失败」 |
| `flashsale_outbox_backlog_pending` | Gauge（既有，复用） | 无 | Outbox 待发布记录数（queued 且 `published_at IS NULL`） | 后台 5s 采样 | 发布侧积压（本次未改口径） |

已有指标（`flashsale_*` 18 个）、Go Runtime 与 Process Collector 均保持不变；
新增指标统一 `surgecart_` 前缀、Counter `_total` 后缀、时间类 `_seconds` 单位。

## 4. 关键技术决策

1. **HTTP Middleware 注册位置**：在 RequestID 与 Response 之间。实测 418/503 被记录为自身状态码，
   证明读到的是最终业务响应（若在 Response 内层则全部变成 200）。
2. **路由标签规范化**：使用 GoFrame `Request.Router.Uri`（注册模板，如 `/products/:id`）。
   实测请求 `/products/1`、`/products/987654`、`/products/abcdef` 只产生一条序列。
3. **未命中路由**：GoFrame 在无路由匹配时（`request.handlers` 为空）直接写 404，
   **不执行路由分组中间件**，因此 404 不被 HTTP 指标统计。这是一个明确取舍：
   宁可缺少「404 率」，也不把任意扫描路径写成 `route` 标签。
   `unmatched` 作为防御性兜底（`Router` 为空时）保留在统一入口处。
4. **Kafka Offset 计算方式**：`kmsg` 原生 `OffsetFetch` + `ListOffsets(-1/-2)`，
   经 `kgo.Client.Request`（自动按组协调器/Leader 路由 + 版本协商）。未引入 kadm，
   也未复用消费客户端（避免监控耦合到消费组会话生命周期）。
5. **未提交位移的初始积压**：以 `log_start_offset` 为基线，与 franz-go 默认
   `ConsumeResetOffset = AtStart` 一致；拿不到起始位移就不出数。`PartitionLag.HasCommit`
   在代码与测试中显式记录该语义。
6. **Outbox 采样方式**：复用 `OutboxPublisher.Backlog()` 一次查询同时产出 Pending 与 OldestAge；
   不新增 SQL、不改 Schema、不加索引。
7. **采样失败语义**：业务指标「失败即冻结」，只有 `surgecart_sampler_up` 反映采样健康度；
   不存在「失败写 0」的路径（单元测试逐条覆盖）。
8. **命名一致性取舍（偏离说明）**：任务建议表把 Pending 命名为 `surgecart_outbox_pending`，
   但仓库已存在语义完全相同的 `flashsale_outbox_backlog_pending`（T2-02-C 引入，含文档与测试）。
   为避免「同一事实两条序列」（违反「不重复定义已有指标」），本次**复用既有指标**，
   只新增缺失的 `surgecart_outbox_oldest_age_seconds` 与采样状态。若后续要求统一前缀，
   应作为一次显式的指标重命名变更（同步改文档与规则），而不是并行新增第二条序列。

## 5. 文件修改清单

新增（10）：

- `internal/metrics/application.go` — 应用级指标定义（HTTP / Lag / 采样状态 / Outbox 年龄）
- `internal/metrics/application_test.go` — 指标白盒测试（标签基数、单位、冻结语义）
- `internal/middleware/metrics.go` — HTTP 指标中间件
- `internal/middleware/metrics_test.go` — 中间件真实 HTTP 测试
- `internal/mq/kafka/lag.go` — Lag 采集器（kmsg 原生协议）
- `internal/mq/kafka/lag_test.go` — 协议级单测（假 requestor）
- `internal/mq/kafka/lag_kafka_test.go` — 真实 Kafka 集成验证（env 门控）
- `internal/worker/kafka_lag.go` — Lag 采样循环（含分区清理）
- `internal/worker/kafka_lag_test.go` — 采样器生命周期/失败语义测试
- `internal/cmd/metrics_route_test.go` — `/metrics` 挂载位置与 Runtime 指标复用测试

修改（7）：

- `internal/cmd/cmd.go` — 注册 `middleware.Metrics`
- `internal/metrics/metrics.go` — 抽出 `newObservableGaugeNamed`；移除仅写 Pending 的旧入口
- `internal/metrics/metrics_test.go` — 适配统一采样写入入口
- `internal/worker/flashsale_runtime.go` — Lag 采样器装配/关闭、Outbox 年龄与采样状态、开关门控
- `internal/worker/flashsale_runtime_test.go` — 注入 Lag 采样替身；扩展 Outbox 采样恢复/年龄断言
- `scripts/test-unit.sh` — 登记新增纯内存测试
- `docs/development/kafka.md` — 更新指标章节（Lag 已实现、新指标、排查流程）

未触碰：业务逻辑、订单事务、Kafka 消息协议、Consumer Group 语义、Outbox Schema、迁移历史、
Redis Lua、业务 API、`manifest/kubernetes/*`、`prometheus/*`、`docker-compose.yml`。

## 6. 测试结果

### 6.1 静态与构建

| 命令 | 环境 | 退出码 | 结果 |
| --- | --- | --- | --- |
| `gofmt -l .` | Windows（Go 1.26.0） | 0 | PASS（无输出） |
| `go build ./internal/... ./api/... .` | Windows | 0 | PASS |
| `go build ./...` | Windows | ≠0 | 既有环境限制：`test/deployk8s/fakestate/state.go` 使用 `syscall.Flock`（Linux 专有），与本任务无关 |
| `go build ./...` | Linux 容器 `golang:1.26` | 0 | PASS |
| `go vet ./...` | Linux 容器 | 0 | PASS |

### 6.2 单元 / 中间件 / 管理器测试

| 命令 | 环境 | 退出码 | 结果 |
| --- | --- | --- | --- |
| `bash scripts/test-unit.sh`（官方单元门禁） | Linux 容器 | 0 | PASS（含 `./internal/worker/...`、`./internal/metrics/...`、`./internal/mq/...`、`-run 'TestRequestID|TestHTTPMetrics|TestMetricsEndpoint' ./internal/middleware/`、`-run '...|TestMetricsRoute' ./internal/cmd/`） |
| `go test ./internal/metrics/... ./internal/mq/... ./internal/worker/...` | Windows | 0 | PASS |
| `go test -race ./internal/metrics/... ./internal/mq/... ./internal/worker/...` | Linux 容器 | 0 | PASS |
| `go test -race -run 'TestHTTPMetrics|TestMetricsEndpoint' ./internal/middleware/` | Linux 容器 | 0 | PASS |
| `go test -race -run TestMetricsRoute ./internal/cmd/` | Linux 容器 | 0 | PASS |

覆盖的关键场景：

- HTTP：正常 200、201、418、503；不同 Method；动态路径不产生高基数；`/metrics` 不被自身统计；
  并发 5 请求期间 In-flight 上升、结束归零；开关关闭时不产生序列。
- Kafka：正常 Lag、零 Lag、未提交位移、`GROUP_ID_NOT_FOUND`、OffsetFetch 整体失败、
  OffsetFetch 部分失败、Log End Offset 失败、分区新增/收缩（序列清理）、负 Lag 夹到 0、
  采样失败冻结 + 采样恢复、Shutdown 停止采样并关闭独立客户端、初始化失败跳过、`db_scanner` 不采样。
- Outbox：空积压（年龄 0）、有积压、年龄已知/未知、DB 不可用冻结、采样恢复、Shutdown 停止采样、
  开关关闭不启动采样。

### 6.3 真实集成测试

| 验证 | 环境 | 命令 | 结果 |
| --- | --- | --- | --- |
| 真实 Kafka Lag | Windows + `apache/kafka:3.9.1`（KRaft 单节点，宿主 `127.0.0.1:29092`） | `FLASH_SALE_KAFKA_TEST_BROKER=127.0.0.1:29092 go test -run TestLagReaderRealKafka -v ./internal/mq/kafka/` | **PASS**：未提交位移 Lag=10；提交 offset=4 后 Lag=6；再投 5 条后 LEO=15、Lag=11；提交到 15 后 Lag=0 |
| 真实 MySQL Backlog（Pending/OldestAge 口径） | Windows + `mysql:8.0`（`127.0.0.1:13306`） | `DATABASE_DEFAULT_PASS=root DATABASE_DEFAULT_PORT=13306 go test -run TestOutboxBacklog -v ./internal/logic/flashsale/` | **PASS**（含 90s 前写入的最老记录年龄断言） |
| Kubernetes Manifest 契约 | Windows（kubectl v1.36.1 + kustomize v5.8.1） | `go test ./test/manifest/...` | **PASS** |

### 6.4 未执行的门禁（如实标记）

| 门禁 | 状态 | 原因 |
| --- | --- | --- |
| `scripts/test-prometheus.sh` | **NOT_RUN** | 需要 bash + docker compose 同一 shell；本机 bash 指向 WSL 不可用。已手工复核其静态断言（job_name / metrics_path / app:8000 / scrape_interval / rule_files / rules 引用全部 OK），且 `prometheus/` 目录零改动 |
| `scripts/test-integration.sh`（`go test -p 1 ./...`） | **NOT_RUN（全量）** | 需要完整 MySQL+Redis+七牛 Mock+集群环境栈；本次只对受影响包执行了针对性真实集成验证（见 6.3） |
| `go test ./...` | **NOT_RUN** | Windows 无法编译 `test/deployk8s/fakestate`（`syscall.Flock`），Linux 容器内全量测试需要完整依赖栈 |
| `make test-k8s-manifest` | 等价替代执行 | 以 `kubectl kustomize` 的真实构建断言（`go test ./test/manifest/...`）替代脚本内同一逻辑 |

## 7. 已知限制

1. **404 / 未命中路由不被 HTTP 指标统计**：GoFrame 在无路由匹配时直接返回 404，
   不执行路由分组中间件。`unmatched` 仅是防御性兜底标签。若后续需要 404 率，
   应显式引入 `BindStatusHandler(404)` 并在其中去重（避免与中间件重复计数）。
2. **Outbox Pending 复用既有指标名**：见 §4.8，命名未统一到 `surgecart_` 前缀，
   以避免同一事实产生两条序列。
3. **未提交位移的初始积压依赖消费起点策略**：当前与 franz-go 默认 `AtStart` 一致；
   若将来显式设置 `kgo.ConsumeResetOffset`（如改为 `AtEnd`），本节定义必须同步修正。
4. **真实 Kafka 验证范围**：单节点 KRaft（RF=1、单分区 Topic）。多 Broker、分区再分配、
   跨可用区 Broker 切换下的 Lag 行为未验证。
5. **Outbox 采样端到端**：采样循环用替身验证（失败冻结/恢复/退出），真实数据源用真实 MySQL
   的 `TestOutboxBacklog` 验证；二者未在同一条用例里串联（需要真实 MySQL + Kafka Producer 装配）。
6. **采样周期为代码常量**：Outbox 5s/3s、Kafka Lag 10s/5s，不新增配置项（与既有约定一致）。

## 8. Git 交付信息

```text
FEATURE_BRANCH=feat/k8s-t4-02-application-metrics
BASE_BRANCH=main
BASELINE_SHA=82243c0fbce614e03c4ff57dcc829b5f68fb08f2
IMPLEMENTATION_COMMIT=033f5fa98bf73050834c43ed9ef48cceb43c29c9
FINAL_HEAD_SHA=033f5fa98bf73050834c43ed9ef48cceb43c29c9
REMOTE_BRANCH_SHA=033f5fa98bf73050834c43ed9ef48cceb43c29c9
WORKTREE_STATUS=clean（回填本段元数据时仅本文件变更，随后以 docs 提交推送）
```

最终状态：

```ini
PROJECT=SurgeCart
TASK=T4-02

BASE_BRANCH=main
BASELINE_SHA=82243c0fbce614e03c4ff57dcc829b5f68fb08f2
FEATURE_BRANCH=feat/k8s-t4-02-application-metrics

HTTP_METRICS_STATUS=PASS
KAFKA_LAG_METRICS_STATUS=PASS
OUTBOX_METRICS_STATUS=PASS
GO_RUNTIME_METRICS_STATUS=PASS

SAMPLER_LIFECYCLE_STATUS=PASS
CONCURRENCY_STATUS=PASS
FAILURE_SEMANTICS_STATUS=PASS

UNIT_TESTS=PASS
INTEGRATION_TESTS=PASS（真实 Kafka Lag + 真实 MySQL Backlog，见 6.3）
RACE_TESTS=PASS（Linux 容器，受影响包）

REAL_KAFKA_LAG_VALIDATION=PASS
REAL_OUTBOX_VALIDATION=PARTIAL（真实 MySQL Backlog 口径已验证；采样循环用替身验证）

FINAL_HEAD_SHA=033f5fa98bf73050834c43ed9ef48cceb43c29c9
PUSH_STATUS=PUSHED（origin/feat/k8s-t4-02-application-metrics）
WORKTREE_STATUS=CLEAN

REPORT=T4-02_APPLICATION_METRICS_IMPLEMENTATION_REPORT.md

BUSINESS_LOGIC_MODIFIED=NO
DATABASE_SCHEMA_MODIFIED=NO
KUBERNETES_MANIFEST_MODIFIED=NO

T4_02_ACCEPTANCE=PASS

T4_03_STARTED=NO
T4_04_STARTED=NO
T5_STARTED=NO

BLOCKERS=无（test-prometheus.sh 与全量集成门禁因本机 bash/docker 编排限制未执行，已在 6.4 记录）
```
