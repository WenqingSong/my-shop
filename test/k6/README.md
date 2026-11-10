# SurgeCart k6 测试框架与实验方案（T5-01）

本目录是 SurgeCart 性能与可靠性实验（任务五）的测试基础设施。它**不是**一份性能报告，
也不包含任何容量结论。框架的目标只有一个：让 T5-02 / T5-03 / T5-04 能在**同一口径**下
重复执行、结果可追溯、且不污染正常业务数据。

| 资产 | 状态 | 说明 |
|---|---|---|
| 统一配置（`config/default.js`） | IMPLEMENTED + VERIFIED | 环境变量驱动，缺必需项 fail-fast（已用真实退出码 107 验证） |
| HTTP 封装（`lib/http.js`） | IMPLEMENTED + VERIFIED | 经 Smoke 真实调用验证 |
| 指标解析与断言（`lib/metrics.js`） | IMPLEMENTED + VERIFIED | 在真实 `/metrics` 输出上断言成功 |
| 结果汇总（`lib/report.js`） | IMPLEMENTED + VERIFIED | 产出 `summary.json` / `summary.txt` |
| 执行入口（`scripts/run.ps1`） | IMPLEMENTED + VERIFIED | Docker 方式运行 + 元数据落盘 |
| Smoke 场景（`scenarios/smoke.js`） | IMPLEMENTED + VERIFIED | 51 项 check 全通过，9 个请求 |
| 只读负载场景（`scenarios/read-load.js`） | IMPLEMENTED / NOT_VERIFIED | 静态检查通过，未执行（属 T5-02） |
| 秒杀下单场景（`scenarios/flashsale-order.js`） | IMPLEMENTED / NOT_VERIFIED | 静态检查通过，未执行（属 T5-03/04） |
| 数据准备工具（`tools/prepare-data.js`） | IMPLEMENTED / NOT_VERIFIED | 本任务无管理员凭据，未真实执行 |

> 「未执行」是事实，不是缺陷。T5-01 的验收要求只到「框架可用 + Smoke 真实通过」。

---

## 1. 为什么用 k6，以及 k6 在本项目里怎么跑

k6 是 Go 编写的负载测试工具：用 JavaScript 描述「虚拟用户（VU）做什么」，
运行时把脚本编译进 Go 的调度器，以 goroutine + 连接池方式并发发请求，因此单机可以
稳定维持数千个 VU 而不像浏览器自动化那样受 JS 单线程限制。

本项目的运行约束（决定了框架形态）：

| 约束 | 事实 | 框架的应对 |
|---|---|---|
| 宿主机没有 k6 | Windows 11 未安装 k6，且不应为此改系统环境 | 使用官方镜像 `grafana/k6:0.54.0`（Docker 已存在），脚本挂载进容器执行 |
| 应用入口是 ClusterIP | `manifest/kubernetes/base/service.yaml` 明确不建 NodePort/Ingress | 先 `kubectl port-forward svc/surgecart <port>:8000`，k6 容器用 `host.docker.internal` 访问宿主机 |
| Windows 保留端口 | 本机 8000 落在 Hyper-V 排除区间，绑定失败 | 示例统一用 **18000**（`port-forward 18000:8000`），通过 `-BaseUrl` 传入 |
| 结果要可追溯 | k6 只产出指标，不知道 Git/集群/镜像 | `scripts/run.ps1` 额外写 `<run_id>.meta.json` |

### 1.1 快速开始

```powershell
# 1) 端口转发（应用 8000 被系统保留，本机用 18000）
kubectl -n surgecart port-forward svc/surgecart 18000:8000
kubectl -n monitoring port-forward svc/prometheus 9090:9090   # 需要对照 Prometheus 时

# 2) Smoke（唯一在本任务中被真实执行的场景）
$env:EXPECT_DISPATCH_MODE = 'kafka'     # 让 smoke 强制断言 Kafka/Outbox 指标族
powershell -ExecutionPolicy Bypass -File test/k6/scripts/run.ps1 `
  -Scenario smoke -RunId r20261110t040200 -BaseUrl http://host.docker.internal:18000
```

输出：`test/k6/results/<run_id>.{summary.json,summary.txt,meta.json,k6.log}`。
这些文件在 `.gitignore` 中，属于一次性运行产物。

### 1.2 静态检查（不产生任何流量）

```powershell
docker run --rm -v "D:/path/to/repo/test/k6:/scripts" -w /scripts grafana/k6:0.54.0 `
  inspect /scripts/scenarios/smoke.js
```

`k6 inspect` 会执行脚本的 init 上下文并打印解析后的 options，能发现语法错误、导入错误、
以及「init 阶段读到非法配置」。因此所有「业务前置条件」（如 `ACTIVITY_ID`）都刻意放在
`setup()` 而不是 init，保证静态检查随时可用。

---

## 2. 目录结构与职责

```text
test/k6/
├── README.md              # 本文件：框架说明 + 三组实验方案
├── config/
│   ├── default.js         # 唯一配置入口：环境变量 → 配置对象，含 fail-fast 校验
│   └── env.example        # 环境变量模板（无任何真实凭据）
├── lib/
│   ├── endpoints.js       # 路径与「路由模板」单一事实源（供 k6 name tag 使用）
│   ├── http.js            # 请求封装：超时/认证/tag/{code,message,data} 解析/错误分类
│   ├── auth.js            # 测试用户注册登录、管理员登录（凭据只从环境变量读）
│   ├── business.js        # 业务 API 封装 + 秒杀业务结果分类（HTTP 成功 ≠ 业务成功）
│   ├── metrics.js         # /metrics 文本解析与指标族断言
│   ├── stages.js          # 负载档位（low/medium/high）与阈值骨架
│   └── report.js          # handleSummary：summary.json / summary.txt
├── scenarios/
│   ├── smoke.js           # 已真实执行：健康/就绪/指标三层巡检
│   ├── read-load.js       # T5-02 入口：只读负载（未执行）
│   └── flashsale-order.js # T5-03/04 入口：秒杀下单 + 可选结果轮询（未执行）
├── tools/
│   └── prepare-data.js    # 独立测试数据准备（用户由各场景准备；本工具准备商品/SKU/活动）
├── scripts/
│   └── run.ps1            # 统一执行入口：docker k6 + 日志 + summary + meta
├── results/               # 运行产物（gitignore，仅保留 .gitkeep）
└── data/                  # run 级测试数据清单（gitignore，仅保留 .gitkeep）
```

设计取舍：

- **配置集中**：只有 `config/default.js` 读 `__ENV`，其余模块接收配置对象。这样切换环境
  （本地 kind → 其他集群）只改环境变量，不动代码。
- **路径集中**：动态路径（`/products/:id`）必须同时给出「真实路径」和「路由模板」，
  后者进 k6 `name` tag，避免每个商品 ID 生成一条 k6 指标序列。
- **业务分类集中**：`lib/business.js` 的 `classifyCreateOrder` 是唯一把响应映射为
  业务结论的地方，避免各场景各自解释 `status` 字段。
- **不加抽象层**：没有「测试用例 DSL」「Page Object」这类东西。实验脚本数量是个位数，
  过度抽象会让后来者读不懂一条请求到底怎么发出去的。

---

## 3. 配置模型

优先级：**命令行 `-Set` > 环境变量 > 代码默认值**。实现见 `config/default.js`。

| 变量 | 必需 | 默认 | 作用 |
|---|---|---|---|
| `SURGECART_BASE_URL` | 否 | `http://host.docker.internal:8000` | k6 容器视角的入口 |
| `TEST_ENV` | 否 | `local-kind` | 环境标识，进结果元数据 |
| `RUN_ID` | 否 | `r<UTC时间戳>` | 运行标识；结果文件名与测试用户名的一部分 |
| `DATA_PREFIX` | 否 | `k6t501` | 测试数据前缀，圈定独立数据范围 |
| `HTTP_TIMEOUT_MS` | 否 | `5000` | 单请求超时 |
| `EXPECT_DISPATCH_MODE` | 否 | `unknown` | `kafka`/`db_scanner`/`unknown`，只影响 smoke 的指标族断言强度 |
| `K6_TEST_PASSWORD` | 条件 | 无 | 测试用户密码（8~24 位）。涉及登录的场景必需 |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | 条件 | 无 | 仅数据准备工具使用 |
| `ACTIVITY_ID` / `SKU_ID` / `PRODUCT_ID` | 条件 | 无 | 秒杀/商品相关场景的输入 |
| `LOAD_PROFILE` / `LOAD_VUS` | 否 | `low` / 无 | 负载档位与峰值 VU 覆盖 |
| `FLASH_USERS` | 否 | `10` | 秒杀场景参与的独立测试用户数 |
| `FLASH_IDEMPOTENCY_REPLAY` | 否 | `false` | 固定幂等键，测试重复提交 |
| `FLASH_VERIFY` | 否 | `false` | 提交后轮询结果（会改变请求形状，仅正确性场景） |

**敏感信息处理**：密码类变量没有默认值，缺失时在 `requireEnv` 处直接抛错；`redact()`
保证打印配置时把 `*password*` 一律替换为 `***`；`run.ps1` 只白名单透传环境变量，
不把凭据拼进命令行（避免进入 shell 历史）。

**配置缺失的真实行为**（已执行验证，退出码 107）：

```text
Error: [k6-flashsale] 缺少 ACTIVITY_ID / SKU_ID。先执行 tools/prepare-data.js 准备独立测试活动，
再用 -e ACTIVITY_ID=<id> -e SKU_ID=<id> 传入（禁止直接复用正常业务活动做高并发压测）。
        at setup (file:///scripts/scenarios/flashsale-order.js:75:11(13))
```

失败发生在 `setup()`，此时尚未发出任何业务请求（`http_reqs=0`）。

---

## 4. 请求封装与响应语义

### 4.1 统一响应包

`internal/middleware/response.go` 对所有 handler 输出：

```json
{ "code": 0, "message": "OK", "data": { } }
```

`code` 是业务码（`internal/codes/codes.go`），HTTP 状态码由业务码映射（409/429/404…）。
因此**判断成功必须看 `code`，不能只看 HTTP 200**。唯一例外是 `GET /metrics`：
它由 `registerMetricsRoute` 直接写裸文本，不经过该中间件，所以 `lib/http.js` 用
`textual: true` 跳过解析。

### 4.2 HTTP 层错误的判定口径

`lib/http.js` 通过 `http.expectedStatuses(200,201,202,204,400,401,403,404,409,429,503)`
把「业务约定的状态码」排除在 `http_req_failed` 之外，并用自定义计数器补回真正的失败：

| 计数器 | 含义 |
|---|---|
| `k6_request_errors` | 网络失败/超时/5xx（含 503 熔断），阈值 `count==0` |
| `k6_envelope_errors` | HTTP 2xx 但响应体不是合法 `{code,...}`，阈值 `count==0` |
| `k6_business_rejected` | 业务码非 0，按 `code` 打标签，用于解释拒绝原因 |
| `k6_biz_outcomes` | 业务结果分类（`accepted`/`stock_insufficient`/…），固定枚举 |

这样「HTTP 错误率」与「业务拒绝率」是两个独立数字，不会被互相污染。

### 4.3 为什么不把用户 ID / 订单号放进 tag

Prometheus 侧标签由服务端决定（`method/route/status`），客户端无法影响；而 k6 侧若把
动态值放进 tag，会让 `--out json` 体积随请求数线性膨胀且无法按路由聚合。所以动态路径
一律使用 `lib/endpoints.js` 的路由模板。

---

## 5. 测试数据管理

### 5.1 三类数据的真实来源

| 数据 | 创建方式 | 权限 | 说明 |
|---|---|---|---|
| 测试用户 | `POST /register` + `POST /login` | 公开 | 用户名 `^[a-zA-Z0-9]{3,24}$`，密码 8~24 |
| 商品 / SKU | `POST /admin/products`、`POST /admin/skus`、`POST /admin/products/:id/on-shelf` | `product:create`、`sku:create`、`product:on_shelf` | 商品创建后恒为 `draft`，必须显式上架 |
| 秒杀活动 | `POST /admin/flash-sales` | `flash_sale:create` | `start_time < end_time`，`skus` 非空，单价 >0，库存 >0 |
| 普通库存 | `POST /admin/inventories/:sku_id/increase` | `inventory:increase` | 仅普通下单链路需要，秒杀库存独立计在活动绑定上 |

### 5.2 隔离规则（硬约束）

1. **只用自己的数据**：所有对象名带 `DATA_PREFIX + run 短标识`，测试用户名为
   `<prefix><runHash>u<index>`。跑完即可按前缀识别，不依赖「记得删过什么」。
2. **不复用正常业务活动**：秒杀活动必须新建，否则会消耗真实库存、写入真实订单。
3. **不删除**：本框架**不包含任何删除/清空操作**（不清库、不删订单、不删 PVC）。
   清理是独立决策，需要人工确认范围后执行。
4. **重复运行**：`RUN_ID` 决定用户名与对象名。同一 `RUN_ID` 重跑会命中「用户名已存在
   (2001)」并继续登录，因此**可重复执行**；换 `RUN_ID` 则得到一套全新的数据。
5. **活动时间窗**：`prepare-data.js` 用 `now-60s ~ now+3600s`，保证创建后立即可下单，
   又不会长期遗留一个「仍在进行」的活动。

### 5.3 使用数据准备工具

```powershell
$env:ADMIN_USERNAME = 'admin'          # 仅本会话注入，不写入仓库
$env:ADMIN_PASSWORD = '******'
$env:FLASH_TOTAL_STOCK = '100'
powershell -ExecutionPolicy Bypass -File test/k6/scripts/run.ps1 `
  -Scenario prepare-data -RunId r20261110t050000 -BaseUrl http://host.docker.internal:18000
```

产物：`test/k6/data/<run_id>.data.json`

```json
{
  "run_id": "r20261110t050000",
  "category_id": 3, "product_id": 41, "sku_id": 57, "activity_id": 12,
  "flash_price": 9900, "total_stock": 100, "activity_start_time": "...", "activity_end_time": "..."
}
```

后续场景复用：

```powershell
$env:ACTIVITY_ID = '12'; $env:SKU_ID = '57'; $env:PRODUCT_ID = '41'
```

> 本任务**没有执行**该工具：仓库中不存在可用的管理员凭据，且规范禁止读取 `.env`
> 与 Secret 取值。工具的正确性因此为 `NOT_VERIFIED`，其 API 契约已逐条对照源码核对。

### 5.4 数据结果核对（不靠猜）

秒杀最终状态以数据库为权威，接口只是视图。T5-03 执行时应保存下列只读查询的**原始输出**
（会话由操作者提供，凭据不入库）：

```sql
-- 1) 是否超卖 / 库存是否为负（活动绑定维度）
SELECT total_stock, sold, (total_stock - sold) AS remaining
FROM flash_sale_activity_skus WHERE activity_id = ? AND sku_id = ?;

-- 2) 成功订单数与 sold 是否一致
SELECT COUNT(*) FROM flash_sale_orders WHERE activity_id = ? AND sku_id = ?;

-- 3) 一人一单是否被破坏（应为 0 行）
SELECT user_id, COUNT(*) c FROM flash_sale_orders
WHERE activity_id = ? AND sku_id = ? GROUP BY user_id HAVING c > 1;

-- 4) 幂等是否被破坏（应为 0 行）
SELECT COUNT(*) FROM (
  SELECT user_id, idempotency_key FROM flash_sale_orders
  WHERE activity_id = ? GROUP BY user_id, idempotency_key HAVING COUNT(*) > 1
) t;

-- 5) 异步请求状态分布（0=queued 1=success 2=failed 3=dead）
SELECT status, COUNT(*) FROM flash_sale_order_requests
WHERE activity_id = ? GROUP BY status;

-- 6) 普通库存（若参与）
SELECT quantity FROM inventories WHERE sku_id = ?;
```

后台聚合接口 `GET /admin/dashboard/flash-sales` 可作交叉验证，但它是**全局聚合**，
不能代替按 activity 维度的 SQL 证据。

---

## 6. Smoke 测试

`scenarios/smoke.js`：1 VU × 3 迭代 × 3 个端点 = 9 个请求。三层断言：

1. **HTTP 层**：2xx；
2. **契约层**：`code=0`，`/health` 的 `data.status=ok` 且 `data.time` 非空，
   `/ready` 的 `data.status=ready`；
3. **指标层**：`/metrics` 是 Prometheus 文本，且必需指标族齐全：
   `go_goroutines`、`go_memstats_heap_alloc_bytes`、`process_cpu_seconds_total`、
   `surgecart_http_requests_total`、`surgecart_http_request_duration_seconds`、
   `surgecart_http_requests_in_flight`、`surgecart_sampler_up`。

指标族按 `EXPECT_DISPATCH_MODE` 分级：

- `kafka`：额外强制 `surgecart_kafka_consumer_lag`、`flashsale_outbox_backlog_pending`、
  `surgecart_outbox_oldest_age_seconds`，并要求 `surgecart_sampler_up{sampler} = 1`；
- `db_scanner`：这些序列按设计不存在，只记录实际存在情况，不判失败；
- `unknown`：只输出观测结果，不做强制断言（不用猜测的期望值制造假失败）。

真实结果（`run_id=r20261110t040200`）：`checks rate=1.0000 (passes=51, fails=0)`，
`http_reqs=9`，`http_req_duration p95≈6.1ms`，三条阈值全部 PASS。

**这份数字不构成任何性能结论**：9 个请求只能说明链路通，不能说明系统能承受多少 QPS。

---

## 7. 结果管理与「与监控关联」

### 7.1 结果文件

| 文件 | 内容 | 是否入库 |
|---|---|---|
| `results/<run_id>.summary.json` | 元数据 + 关键指标 + 阈值判定 | 否（gitignore） |
| `results/<run_id>.summary.txt` | 人读摘要 | 否 |
| `results/<run_id>.meta.json` | Git SHA/分支、k8s context、应用镜像、起止时间、退出码 | 否 |
| `results/<run_id>.k6.log` | k6 全量控制台日志 | 否 |
| `data/<run_id>.data.json` | 本次运行准备的测试数据 ID | 否 |
| `results/<run_id>.raw.json` | 原始 JSON Lines，仅 `-K6Args '--out','json=/results/x.raw.json'` 时产生 | 否 |

长期结论写入 `docs/reports/`，并引用具体 `run_id`。原始结果的保存策略：需要归档时把
`summary.json` + `meta.json` 复制进实验报告目录（体积小），原始 JSON Lines 仅在需要
复算分布时保留，且不入库。

### 7.2 时间窗口对齐

`meta.json` 的 `started_at_utc` / `finished_at_utc` 是**唯一权威**时间窗。
`summary.json` 里的 `summary_exported_at` 只是导出时刻（`handleSummary` 可能运行在
独立运行时，模块级时间戳会被重新初始化，不能当开始时间用）。

Prometheus 查询用 epoch 秒：

```promql
# 测试窗口内的服务端 RPS（按路由）
sum by (route) (rate(surgecart_http_requests_total{job="surgecart"}[1m]))

# 服务端 P95（应用自身耗时，不含客户端网络）
histogram_quantile(0.95,
  sum by (le, route) (rate(surgecart_http_request_duration_seconds_bucket{job="surgecart"}[1m])))

# 秒杀：受理速率 vs 真正成交速率（这就是「请求吞吐 ≠ 业务成功吞吐」）
sum(rate(flashsale_requests_total{interface="order",result="queued"}[1m]))
sum(rate(flashsale_requests_total{interface="consume",result="success"}[1m]))

# 积压与消费者健康
surgecart_kafka_consumer_lag{job="surgecart"}
flashsale_outbox_backlog_pending
surgecart_outbox_oldest_age_seconds
surgecart_sampler_up{job="surgecart"}
```

`curl "http://127.0.0.1:9090/api/v1/query_range?query=<promql>&start=<epoch>&end=<epoch>&step=15s"`。

T5-01 已实测的关联证据：Smoke 结束后
`sum by (route,status) (increase(surgecart_http_requests_total{job="surgecart"}[10m]))`
中 `/health`、`/ready` 均有增量，而 `route="/metrics"` **不存在**——这正好印证
`/metrics` 被排除在自身观测之外（T4-02 的设计约束）。注意该增量同时包含 kubelet 探针流量，
因此精确归因必须在「无探针干扰窗口」内做前后差值，不能直接用 `increase` 的数字当压测流量。

---

## 8. 三组实验方案（T5-02 / T5-03 / T5-04 执行）

以下只定义「怎么做 + 采什么 + 怎么判」，**不预设任何结论**。参数在 T5-02 依据本机资源定稿。

### 实验 A：负载与性能（T5-02）

**问题**：不同负载下系统表现如何？`db_scanner` 与 `kafka` 两种消费模式的差异在哪？

**变量**
- 负载档：`low`（5 VU）/ `medium`（20 VU）/ `high`（50 VU），由 `lib/stages.js` 提供，
  `LOAD_VUS` 可覆盖峰值；每档含 ramp-up / 持续 / ramp-down。
- 消费模式：`FLASH_SALE_DISPATCH_MODE=kafka|db_scanner`（需重启应用，属部署动作）。

**采集**
| 维度 | 指标 | 来源 |
|---|---|---|
| 客户端吞吐 | `http_reqs.rate` | k6 summary |
| 客户端时延 | P50/P95/P99（`http_req_duration`） | k6 summary |
| HTTP 失败率 | `http_req_failed` + `k6_request_errors` | k6 summary |
| 服务端吞吐 | `sum(rate(surgecart_http_requests_total[...]))` | Prometheus |
| 服务端时延 | `histogram_quantile(0.95/0.99, ..._duration_seconds_bucket)` | Prometheus |
| CPU/内存 | `process_cpu_seconds_total`、`process_resident_memory_bytes`、`go_memstats_heap_alloc_bytes` | Prometheus |
| Kafka 积压 | `surgecart_kafka_consumer_lag` | Prometheus |
| Outbox 积压 | `flashsale_outbox_backlog_pending`、`surgecart_outbox_oldest_age_seconds` | Prometheus |

**步骤**：固定同一 `RUN_ID`、同一份测试数据 → 只读链路先测（排除写链路干扰）→
再测秒杀写链路 → 每档开始/结束记录时间点 → 每档保存 summary + meta → 输出
`docs/reports/t5-02-*/`。

**判定**：不做「通过/不通过」的门槛，只做对比结论；若出现 `k6_request_errors > 0`，
该档数据视为无效，需排查后重跑。

### 实验 B：高并发业务正确性（T5-03）

**问题**：高并发下是否超卖、库存是否为负、一人一单与幂等是否被破坏、最终订单数据是否正确。

**方法**：`flashsale-order.js` + `FLASH_IDEMPOTENCY_REPLAY` 两种模式：
- 独立用户并发（默认）：验证一人一单与总库存；
- 固定幂等键重放：验证重复提交不产生第二张订单（幂等键 + 请求指纹 `sha256(activity|sku)`）。

**必须区分的业务事实**（`lib/business.js` 已固化）：
`accepted`（已受理）→ `success`（成交，带订单）/ `failed`（业务失败，带 `fail_code`）/
`dead`（技术失败超限）；拒绝侧分 `stock_insufficient`(12003)、`already_purchased`(12004)、
`idempotency_conflict`(12005)、`rate_limited`(12009)、`queue_full`(12010)。

**判定**（全部需要 §5.4 的 SQL 原始输出作为证据）：
1. `sold ≤ total_stock` 且 `remaining ≥ 0`；
2. `COUNT(flash_sale_orders) = sold`；
3. 一人多单查询返回 0 行；
4. 同幂等键重复订单查询返回 0 行；
5. `flash_sale_order_requests` 中 `queued` 最终收敛为 0（允许少量持续失败，需解释原因）。

### 实验 C：Kafka 积压与恢复（T5-04）

**问题**：消费能力不足时 Lag 如何变化？恢复后是否下降？Pod 重启是否 Rebalance？
未完成消息能否重投？是否产生重复业务结果？

**方法**：持续生产请求（`flashsale-order.js`，`FLASH_VERIFY=true` 记录端到端收敛）→
制造消费能力不足（限流/暂停消费者）→ 观察 `surgecart_kafka_consumer_lag`、
`flashsale_outbox_backlog_pending`、`surgecart_outbox_oldest_age_seconds`、
`flashsale_consumer_records_total` / `_processed_total` / `_retries_total` / `_dlq_total` →
恢复消费能力 → 观察 Lag 回落与 `flash_sale_order_requests.status` 收敛。

**判定**：Lag 峰值与回落时间；重复订单数为 0；最终 `success + failed + dead = 总请求数`；
`dead` 必须能用 `fail_code` 解释。

> 本任务**没有**执行消费者限速、Pod 删除、Kafka 故障注入等任何实验动作。

---

## 9. 为后续任务的扩展点

| 需求 | 现有支撑 | 还需做什么 |
|---|---|---|
| T5-02 调负载曲线 | `lib/stages.js`（档位 + `LOAD_VUS`） | 按实测资源定稿档位，写 `docs/reports/t5-02-*` |
| T5-02 切消费模式 | 文档化部署动作 | 修改集群 ConfigMap 并重启（属部署，不在测试框架内） |
| T5-03 多用户 | `lib/auth.js::provisionUsers`（幂等，可复用账号） | 注意 bcrypt 成本：用户多时固定 `RUN_ID` 复用 |
| T5-03 最终状态核验 | `getFlashOrderResult` + SQL 证据清单 | 编写 `scenarios/flashsale-correctness.js` 与结论表 |
| T5-04 故障注入 | `FLASH_VERIFY` 记录端到端收敛 | 引入 phase 标记（`tags.phase`）区分故障前/中/后 |
| 结果图表 | `summary.json` 结构化 | 由 T5-05 汇总进最终报告 |

---

## 10. 已知限制

1. **k6 跑在容器里，经 port-forward 访问**：port-forward 是单条 TCP 隧道，高 VU 时会成为
   客户端侧瓶颈，测出来的吞吐是「隧道吞吐」而非集群内真实吞吐。T5-02 若需要更高压力，
   应评估把 k6 作为集群内 Pod 运行（本任务未实现，也不在允许范围内）。
2. **k6 版本固定为 0.54.0**：镜像 tag 写死在 `run.ps1`。升级需重新验证脚本兼容性。
3. **未安装 k6 二进制**：所有执行都要经过 Docker，宿主机无法直接 `k6 run`。
4. **数据准备未执行**：`prepare-data.js` 需要管理员凭据，本任务未验证。
5. **`http_req_failed` 的口径是自定义的**：通过 `expectedStatuses` 把业务 4xx/429/503 排除，
   真正的失败看 `k6_request_errors`。跨工具对比时不要直接拿 `http_req_failed` 与别处比较。
6. **覆盖率**：只封装了本任务需要的接口（健康、商品、SKU、秒杀、订单查询、库存）。
   购物车/优惠券/评价等链路没有封装，后续如需使用按 `lib/business.js` 的写法追加。
7. **中文注释依赖 UTF-8**：`run.ps1` 带 BOM，才能在 Windows PowerShell 5.1 下正确解析
   （无 BOM 会被按 GBK 解码导致语法错误）。修改该文件时请保持 BOM。

---

## 11. 源码依据（本文件所有结论的来源）

- 路由：`internal/cmd/routes_frontend.go`、`internal/cmd/routes_admin.go`、`internal/cmd/cmd.go`
- 响应包装：`internal/middleware/response.go`
- 错误码与 HTTP 映射：`internal/codes/codes.go`
- 秒杀契约：`api/flashsale/v1/flash_sale.go`、`internal/logic/flashsale/flashsale.go`、`request.go`
- IAM 校验：`internal/logic/iam/iam.go`（用户名/密码规则）
- 指标定义：`internal/metrics/application.go`、`internal/metrics/metrics.go`
- 部署与监控：`manifest/kubernetes/base/service.yaml`、
  `manifest/kubernetes/monitoring/prometheus/base/configmap.yaml`
