# T5-01 测试方案与 k6 测试框架 — 交付报告

- 项目：SurgeCart
- 任务：T5-01（任务五第 1 子任务）
- 分支：`feat/k8s-t5-01-k6-test-framework`
- 基线：`feat/k8s-t4-04-grafana-dashboard` @ `0062c4a5a5da2b637967591988b4bfde246d1d58`
- 性质：测试基础设施建设，**未开展正式性能压测**

状态口径（全文统一）：

| 标记 | 含义 |
|---|---|
| IMPLEMENTED | 代码/文档已存在，但未经真实执行验证 |
| VERIFIED | 已真实执行并得到可复现结果 |
| PARTIALLY_VERIFIED | 部分环节已真实执行，其余未覆盖 |
| NOT_VERIFIED | 未执行（原因明确） |
| NOT_IMPLEMENTED | 明确不在本任务范围内，未实现 |

---

## 1. 任务目标

建立一套可重复执行、可配置、可扩展、结果可追溯的 k6 测试框架，为后续三个实验提供统一基础：

1. 从本机向 Kubernetes 中的 SurgeCart 产生测试流量；
2. 统一管理测试环境与参数；
3. 可复用的 k6 脚本结构；
4. 独立测试用户/商品/SKU/秒杀活动数据；
5. 区分 HTTP 请求成功与真正的业务成功；
6. 保存测试原始结果；
7. 将 k6 结果与 Prometheus/Grafana 指标关联；
8. 可重复执行且不污染正常业务数据。

**本任务不产出任何容量/QPS 结论。** Smoke 测试通过不等于性能达标。

---

## 2. 实现范围

### 2.1 已实现

| 交付物 | 说明 | 状态 |
|---|---|---|
| `test/k6/config/default.js` | 环境变量驱动的统一配置，含 fail-fast 与脱敏 | VERIFIED |
| `test/k6/config/env.example` | 变量模板（无真实凭据） | VERIFIED（静态） |
| `test/k6/lib/http.js` | 请求封装：超时/认证/tag/响应包解析/错误分类 | VERIFIED |
| `test/k6/lib/endpoints.js` | 路径与路由模板单一事实源 | VERIFIED（经 smoke 间接验证） |
| `test/k6/lib/auth.js` | 测试用户/管理员登录封装 | IMPLEMENTED（注册路径未随 smoke 执行） |
| `test/k6/lib/business.js` | 商品/SKU/秒杀/订单 API + 业务结果分类 | IMPLEMENTED |
| `test/k6/lib/metrics.js` | `/metrics` 文本解析与指标族断言 | VERIFIED |
| `test/k6/lib/stages.js` | 负载档位与阈值骨架（T5-02 使用） | IMPLEMENTED / NOT_VERIFIED |
| `test/k6/lib/report.js` | `handleSummary` → summary.json/txt | VERIFIED |
| `test/k6/scenarios/smoke.js` | 健康/就绪/指标三层巡检 | VERIFIED |
| `test/k6/scenarios/read-load.js` | 只读负载（T5-02 入口） | IMPLEMENTED / NOT_VERIFIED |
| `test/k6/scenarios/flashsale-order.js` | 秒杀下单（T5-03/04 入口） | IMPLEMENTED / NOT_VERIFIED |
| `test/k6/tools/prepare-data.js` | 独立测试数据准备 | IMPLEMENTED / NOT_VERIFIED |
| `test/k6/scripts/run.ps1` | 统一执行入口 + 元数据落盘 | VERIFIED |
| `test/k6/README.md` | 框架说明 + 实验 A/B/C 方案 | VERIFIED（文档） |
| `.gitignore` | 新增 k6 结果/数据/本地配置忽略规则 | VERIFIED |
| `.agent/tasks/dev/T5-01-k6-test-framework.md` | 开发知识文档 | VERIFIED（文档） |

### 2.2 明确未做（遵守任务边界）

| 项 | 原因 |
|---|---|
| 50/100/500 VU 正式负载测试 | 属 T5-02 |
| db_scanner 与 kafka 吞吐对比 | 属 T5-02 |
| 高并发秒杀 | 属 T5-03 |
| Kafka 积压制造/消费者限速/Pod 删除/故障注入 | 属 T5-04 |
| 修改 Go 业务代码 / Schema / Redis Lua / Kafka 架构 / K8s Deployment / Prometheus / Grafana | 任务禁止 |
| 读取 `.env` 或 Secret 取值 | 任务禁止（因此数据准备工具未执行） |

---

## 3. 最终目录结构

```text
test/k6/
├── README.md
├── config/
│   ├── default.js
│   └── env.example
├── lib/
│   ├── auth.js
│   ├── business.js
│   ├── endpoints.js
│   ├── http.js
│   ├── metrics.js
│   ├── report.js
│   └── stages.js
├── scenarios/
│   ├── flashsale-order.js
│   ├── read-load.js
│   └── smoke.js
├── tools/
│   └── prepare-data.js
├── scripts/
│   └── run.ps1
├── results/
│   └── .gitkeep          （运行产物，gitignore）
└── data/
    └── .gitkeep          （run 级测试数据清单，gitignore）
```

新增文件 17 个，修改文件 1 个（`.gitignore`）。未创建空目录、无占位文件（`.gitkeep` 用于保留
受 gitignore 的目录结构）。

---

## 4. 核心实现

### 4.1 配置模型（`config/default.js`）

- 唯一配置入口：环境变量 → 配置对象，优先级 `-Set` > 环境变量 > 默认值；
- 必需项（`K6_TEST_PASSWORD`、`ADMIN_USERNAME`、`ADMIN_PASSWORD`）**无默认值**，
  缺失时 `requireEnv` 抛出含「变量名 + 用途 + 取值方式」的错误；
- `envInt`/`envBool` 对显式非法值直接失败，不静默回退；
- `redact()` 保证任何日志/结果中 `*password*|*secret*|*token*` 均为 `***`。

### 4.2 请求封装（`lib/http.js`）

- 统一 Base URL、超时、`Authorization`、k6 tag（`name` 取路由模板，杜绝高基数）；
- 解析 `{code,message,data}`（`/metrics` 用 `textual:true` 跳过）；
- 三类自定义计数：`k6_request_errors`（网络/超时/5xx）、`k6_envelope_errors`（契约漂移）、
  `k6_business_rejected`（业务码非 0）；
- 通过 `http.expectedStatuses(...)` 把业务状态码（400/401/403/404/409/429/503）
  排除出 `http_req_failed`，避免「售罄导致大量 409」被误读为 HTTP 故障；
- **不自动重试**：重试属于被测系统的能力，框架重试会掩盖真实失败率。

### 4.3 业务成功判定（`lib/business.js`）

本项目秒杀是异步受理模型，因此固化三级事实：

| 层级 | 判定依据 |
|---|---|
| HTTP 成功 | 2xx |
| 受理成功 | `code=0` 且 `data.status="queued"`（或幂等命中返回既有状态） |
| 业务成交 | 结果查询 `data.status="success"` 且 `data.order.id > 0`；最终以 `flash_sale_orders` 行为准 |

拒绝侧按业务码分类：`12003` 库存不足、`12004` 一人一单、`12005` 幂等冲突、
`12006` SKU 不可购买、`12009` 限流、`12010` 排队满、`12002` 不在时间窗、`12001` 活动不存在；
`1000/1005/transport_error` 归入「系统侧不确定失败」（`AMBIGUOUS_OUTCOMES`），单独统计。

### 4.4 指标断言分级（`lib/metrics.js` + `smoke.js`）

- 任何开启指标的应用都必须有：`go_goroutines`、`go_memstats_heap_alloc_bytes`、
  `process_cpu_seconds_total`、`surgecart_http_requests_total`、
  `surgecart_http_request_duration_seconds`、`surgecart_http_requests_in_flight`、
  `surgecart_sampler_up`；
- 仅 `dispatch_mode=kafka` 才必然存在：`surgecart_kafka_consumer_lag`、
  `flashsale_outbox_backlog_pending`、`surgecart_outbox_oldest_age_seconds`；
- 由 `EXPECT_DISPATCH_MODE` 决定是否强制断言，避免环境切换产生假失败。

### 4.5 结果与元数据（`lib/report.js` + `scripts/run.ps1`）

- k6 侧：`<run_id>.summary.json`（元数据 + 关键指标 + 阈值判定）、`<run_id>.summary.txt`；
- runner 侧：`<run_id>.meta.json`（Git 分支/SHA、k8s context、应用镜像、起止时间、退出码、
  非敏感 env 清单）、`<run_id>.k6.log`；
- 原始 JSON Lines 默认关闭，需显式 `-K6Args '--out','json=/results/x.raw.json'`；
- `results/`、`data/` 由 `.gitignore` 排除，仅保留 `.gitkeep`。

### 4.6 测试数据隔离（`tools/prepare-data.js`）

- 全部通过真实业务 API（`POST /admin/products`、`/admin/skus`、
  `/admin/products/:id/on-shelf`、`/admin/flash-sales`），不写库、不绕校验；
- 对象名含 `DATA_PREFIX + run 短标识`，用户名 `<prefix><runHash>u<index>`（符合 IAM 规则）；
- 秒杀活动时间窗 `now-60s ~ now+3600s`，创建后立即可下单；
- **无任何删除/清空逻辑**；
- 输出一行 `K6_DATA_MARKER {json}`，由 runner 落盘为 `data/<run_id>.data.json`。

---

## 5. 测试环境

| 项 | 值 |
|---|---|
| OS | Windows 11 |
| Shell | Windows PowerShell 5.1 |
| Docker | 29.9.3（Docker Desktop） |
| kubectl | Client v1.36.1 |
| 集群 | kind `kind-surgecart-local`，node `v1.37.0` |
| 命名空间 | `surgecart`、`monitoring`、`kube-system`、`local-path-storage` |
| 应用 Pod | `surgecart-7d4d84b88-xn4tb` 1/1 Running |
| 应用镜像 | `surgecart:64ebbbd-t4-03` |
| dispatch_mode | `kafka` |
| metrics | `FLASH_SALE_METRICS_ENABLED=true` |
| Prometheus | `monitoring/prometheus`（ClusterIP:9090，`job=surgecart`，`external_labels.cluster=surgecart-local`） |
| Grafana | `monitoring/grafana`（ClusterIP:3000，dashboard uid `surgecart-overview`） |
| k6 | `grafana/k6:0.54.0`（容器内 `k6 v0.54.0, linux/amd64`） |
| 宿主机端口转发 | `18000 → svc/surgecart:8000`、`9090 → svc/prometheus:9090` |

**端口说明**：宿主机 8000 落在 Windows 保留端口区（绑定报
`An attempt was made to access a socket in a way forbidden by its access permissions`），
因此本机使用 18000 转发。本文档所有命令使用 18000。

**安全**：未读取、未打印任何 Secret 值；`kubectl get configmap` 仅用于确认
`FLASH_SALE_DISPATCH_MODE` / `FLASH_SALE_METRICS_ENABLED` 的取值（非敏感）。

---

## 6. 执行过的测试命令

```powershell
# 0) 端口转发
kubectl -n surgecart  port-forward svc/surgecart  18000:8000
kubectl -n monitoring port-forward svc/prometheus 9090:9090

# 1) 静态检查（不产生流量）——4 个脚本各执行一次
docker run --rm -v "<repo>/test/k6:/scripts" -w /scripts grafana/k6:0.54.0 `
  inspect /scripts/scenarios/smoke.js
docker run --rm -v "<repo>/test/k6:/scripts" -w /scripts grafana/k6:0.54.0 `
  inspect /scripts/scenarios/read-load.js
docker run --rm -v "<repo>/test/k6:/scripts" -w /scripts grafana/k6:0.54.0 `
  inspect /scripts/scenarios/flashsale-order.js
docker run --rm -v "<repo>/test/k6:/scripts" -w /scripts grafana/k6:0.54.0 `
  inspect /scripts/tools/prepare-data.js

# 2) Smoke 真实执行
$env:EXPECT_DISPATCH_MODE = 'kafka'
powershell -ExecutionPolicy Bypass -File test/k6/scripts/run.ps1 `
  -Scenario smoke -RunId r20261110t040200 -BaseUrl http://host.docker.internal:18000

# 3) 配置缺失的错误处理（负向验证）
powershell -ExecutionPolicy Bypass -File test/k6/scripts/run.ps1 `
  -Scenario flashsale-order -RunId r20261110t040300 -BaseUrl http://host.docker.internal:18000
powershell -ExecutionPolicy Bypass -File test/k6/scripts/run.ps1 `
  -Scenario prepare-data -RunId r20261110t040400 -BaseUrl http://host.docker.internal:18000

# 4) 监控关联核验
curl "http://127.0.0.1:9090/api/v1/query?query=sum by (route,status) (increase(surgecart_http_requests_total{job=\"surgecart\"}[10m]))"
```

---

## 7. 每项测试的真实结果

### 7.1 静态检查 — VERIFIED

| 脚本 | 结果 |
|---|---|
| `scenarios/smoke.js` | exit=0 |
| `scenarios/read-load.js` | exit=0 |
| `scenarios/flashsale-order.js` | exit=0 |
| `tools/prepare-data.js` | exit=0 |

### 7.2 Smoke — VERIFIED

`run_id=r20261110t040200`，1 VU × 3 迭代，9 个 HTTP 请求：

```text
checks             : rate=1.0000  passes=51  fails=0
http_reqs          : count=9  rate=149.41/s
http_req_duration  : avg=4.44ms  med=4.61ms  p(90)=5.94ms  p(95)=6.11ms  max=6.28ms
k6_request_errors  : count=0
k6_envelope_errors : count=0
duration_ms        : 60.2
---
PASS checks rate==1
PASS k6_request_errors count==0
PASS k6_envelope_errors count==0
```

断言覆盖：HTTP 2xx、`code=0`、`/health data.status=ok` 且 `data.time` 非空、
`/ready data.status=ready`、`/metrics` 为 Prometheus 文本且必需指标族齐全
（`EXPECT_DISPATCH_MODE=kafka` 下额外强制 Kafka/Outbox 三个指标族与
`surgecart_sampler_up{sampler=kafka_lag|outbox}=1`）。

**结论边界**：本结果只证明「测试框架 + 运行环境 + 指标导出」链路可用，
**不能**据此声称系统支持任何 QPS。

复跑证据：提交推送后，用仓库中的最终版本（LF + UTF-8 BOM）再次执行
`run_id=r20261110t041500`，结果同为 `checks rate=1.0000 passes=51 fails=0`、9 个请求、
三条阈值 PASS，说明交付物本身（而非工作区临时状态）可运行。

### 7.3 配置缺失的错误处理 — VERIFIED

| 触发 | 输出 | 退出码 | 副作用 |
|---|---|---|---|
| 秒杀场景缺 `ACTIVITY_ID`/`SKU_ID` | `[k6-flashsale] 缺少 ACTIVITY_ID / SKU_ID ... at setup` | 107 | 无（`http_reqs=0`，未发出业务请求） |
| 数据准备缺 `ADMIN_USERNAME` | `[k6-config] 缺少必需环境变量 ADMIN_USERNAME（用途：...）... at requireEnv` | 107 | 无（登录前即失败） |

### 7.4 结果输出 — VERIFIED

```text
test/k6/results/r20261110t040200.k6.log       1649 B
test/k6/results/r20261110t040200.meta.json     948 B
test/k6/results/r20261110t040200.summary.json 3540 B
test/k6/results/r20261110t040200.summary.txt   791 B
```

`meta.json` 实测字段：`git_branch=feat/k8s-t5-01-k6-test-framework`、
`git_sha=0062c4a5a5da2b637967591988b4bfde246d1d58`、`git_dirty=true`、
`kube_context=kind-surgecart-local`、`app_image=surgecart:64ebbbd-t4-03`、
`started_at_utc`/`finished_at_utc`/`exit_code=0`。

### 7.5 与 Prometheus 的关联 — PARTIALLY_VERIFIED

`sum by (route,status) (increase(surgecart_http_requests_total{job="surgecart"}[10m]))` 返回：

```text
/health        status=200   +69.74
/ready         status=200   +129.23
/products      status=200   +0
/products/:id  status=404   +0
```

结论：
- VERIFIED：服务端指标可查询，时间窗可与 `meta.json` 对齐；`route="/metrics"` **不存在**，
  证明 `/metrics` 抓取未被自身统计（T4-02 约束在真实运行中成立）。
- NOT_VERIFIED：上述增量混有 kubelet 探针流量，未做「纯 k6 流量」的精确归因。
  该工作留到 T5-02（静置窗口 + 前后差值法），本任务不声称已完成。

### 7.6 未执行的测试 — NOT_VERIFIED

| 项 | 原因 |
|---|---|
| `read-load.js` 运行 | 属 T5-02 负载测试，本任务禁止执行 |
| `flashsale-order.js` 运行 | 属 T5-03/04，本任务禁止执行 |
| `prepare-data.js` 运行 | 需要管理员凭据；规范禁止读取 `.env`/Secret |
| 数据库一致性 SQL | 同上（无数据库会话） |

---

## 8. Git 分支和 SHA

| 项 | 值 |
|---|---|
| 开发分支 | `feat/k8s-t5-01-k6-test-framework` |
| 基线分支 | `feat/k8s-t4-04-grafana-dashboard` |
| 基线 SHA | `0062c4a5a5da2b637967591988b4bfde246d1d58` |
| `main` SHA（未合并，未改动） | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2` |
| 本任务实现提交 | `00680155c44fa56bcfa88a5d5686223f2f49226c` |
| 远端 | `origin https://cnb.cool/framework-station/my-shop` |

基线核验方式：`git status` / `git branch --show-current` / `git rev-parse HEAD` /
`git log` / `git remote -v` / `git ls-remote origin`。核验时远端
`refs/heads/feat/k8s-t4-04-grafana-dashboard` = `0062c4a5...`，与本地一致，无分叉。

未执行：合并到 `main`、force push、删除历史分支、改写历史。

---

## 9. 已知限制

1. **port-forward 是客户端瓶颈**：k6 在容器内经单条 TCP 隧道访问应用，高 VU 时隧道本身
   会限制吞吐。T5-02 若需要更高压力，应评估将 k6 作为集群内 Pod 运行（本任务未实现）。
2. **k6 未安装到宿主机**：所有执行必须经 Docker；k6 版本固定 `0.54.0`，升级需重新验证。
3. **`http_req_failed` 口径自定义**：通过 `expectedStatuses` 排除了业务 4xx/429/503，
   跨工具对比时不可直接比较同名指标。
4. **数据准备工具未验证**：`prepare-data.js` 的 API 契约已逐条对照源码，但未真实执行。
5. **接口封装不完整**：只覆盖健康、商品/SKU、秒杀、订单查询、库存；购物车/优惠券/评价等
   未封装，后续按同样模式追加。
6. **`run.ps1` 必须保持 UTF-8 BOM**：无 BOM 时 Windows PowerShell 5.1 会按 GBK 解码中文字符
   导致语法错误（开发中已实际踩到）。
7. **未覆盖多副本**：当前应用单副本，框架未处理多 Pod 的流量分布。

---

## 10. 验收结论

| 验收项 | 要求 | 结论 |
|---|---|---|
| Git 基线 | 正确基于 T4-04 | ✅ PASS（`0062c4a5...`，本地/远端一致） |
| 开发分支 | 已建立独立分支 | ✅ PASS（`feat/k8s-t5-01-k6-test-framework`） |
| 测试框架 | 结构清晰、可扩展 | ✅ PASS（config/lib/scenarios/tools/scripts 分层） |
| 公共配置 | 支持环境变量配置 | ✅ PASS + VERIFIED（含缺项 fail-fast） |
| 公共请求封装 | 已实现必要能力 | ✅ PASS + VERIFIED |
| 测试数据方案 | 有明确、可执行的准备约定 | ✅ PASS（README §5 + `prepare-data.js`；执行 NOT_VERIFIED） |
| Smoke 测试 | 已实现 | ✅ PASS + VERIFIED（51 checks，0 失败） |
| 脚本验证 | 有真实执行结果或明确未验证原因 | ✅ PASS（静态 4/4；Smoke 真实；其余标注原因） |
| 后续实验方案 | 已为 T5-02/03/04 做好设计 | ✅ PASS（README §8/§9） |
| 开发知识文档 | 已创建并纳入 Git | ✅ PASS（`.agent/tasks/dev/T5-01-k6-test-framework.md`） |
| 正式报告 | 已创建并纳入 Git | ✅ PASS（本文件） |
| Git 推送 | 成功且本地远端同步 | 见 §12（推送后回填） |
| 业务代码 | 未发生未经授权的修改 | ✅ PASS（仅新增 `test/k6/**` 与 `.gitignore`） |

**总判定：T5-01 完成（框架可用，Smoke 真实验证通过）。**

明确不宣称的内容：
- 不宣称任何 QPS/吞吐/容量结论；
- 不宣称秒杀正确性已验证；
- 不宣称 Kafka 故障恢复能力已验证；
- 不把「脚本存在」当作「测试通过」。

---

## 11. 后续任务准备情况

| 任务 | 框架已提供的支撑 | 仍需完成 |
|---|---|---|
| T5-02 负载与性能 | `lib/stages.js`（low/medium/high + `LOAD_VUS`）、`read-load.js`、`flashsale-order.js`、PromQL 清单 | 按本机资源定稿档位、执行三档与两模式对比、产出 T5-02 报告 |
| T5-03 业务正确性 | `lib/business.js` 结果分类、`FLASH_IDEMPOTENCY_REPLAY`、`FLASH_VERIFY`、SQL 证据清单 | 执行独立活动压测、采集 SQL 原始输出、产出结论 |
| T5-04 故障恢复 | `phase` tag、`FLASH_VERIFY` 端到端收敛、Lag/Outbox/Sampler 指标清单 | 故障注入（限速/Pod 重启）、前后状态对照、产出结论 |
| T5-05 报告 | 统一的 `summary.json` + `meta.json` 结构 | 汇总各实验证据、最终交付 |

对后续任务的硬性提醒：
1. 必须使用 `prepare-data.js` 产出的**独立**活动，禁止复用正常业务活动；
2. 每档结果必须同时保存 `summary.json` 与 `meta.json`，否则无法证明「同一个被测版本」；
3. 出现 `k6_request_errors > 0` 的档位数据无效，需排查后重跑；
4. 判断积压是否恢复必须看 Gauge 回落曲线 + `queued` 收敛，不能用 `increase`。

---

## 12. Git 交付

### 12.1 提交

| 项 | 值 |
|---|---|
| 分支 | `feat/k8s-t5-01-k6-test-framework` |
| 提交信息 | `test(k6): add reusable performance testing framework` |
| 提交 SHA | `00680155c44fa56bcfa88a5d5686223f2f49226c` |

### 12.2 变更内容

```text
新增：test/k6/**（17 个文件）
新增：docs/reports/t5-01-k6-test-framework/T5-01_K6_TEST_FRAMEWORK_REPORT.md
新增：.agent/tasks/dev/T5-01-k6-test-framework.md
修改：.gitignore（+10 行，忽略 k6 运行产物与本地配置）
```

### 12.3 推送与核验

| 项 | 值 |
|---|---|
| 远端分支 | `origin/feat/k8s-t5-01-k6-test-framework` |
| 实现提交 SHA（本地=远端） | `00680155c44fa56bcfa88a5d5686223f2f49226c` |
| 分支 tip | 实现提交之后的文档回填提交（内容仅为 §8/§12 元数据，不含任何代码或测试变更） |
| upstream | 已设置（`git push -u`） |
| 本地/远端同步 | 同步（`## feat/k8s-t5-01-k6-test-framework...origin/feat/k8s-t5-01-k6-test-framework`，无 ahead/behind） |
| 工作区状态 | clean（回填提交前） |
| force push | 未使用 |
| 合并 main | 未执行（按要求保留独立分支） |
| 删除历史分支 | 未执行 |

推送后复核：把仓库中的 `test/k6/scripts/run.ps1`（LF 换行 + UTF-8 BOM）重新检出后
再执行一次 Smoke（`run_id=r20261110t041500`），结果 `checks rate=1.0000 passes=51 fails=0`，
三条阈值全部 PASS，证明入库后的交付物本身可用（不只是提交前的工作区版本可用）。

> 本节的 SHA、推送状态与同步结论由后续「报告回填」提交补齐（与 T4-03/T4-04 的做法一致），
> 因为这些值在提交动作完成之前不可能存在。回填只修改本节元数据，不改动任何实现。
