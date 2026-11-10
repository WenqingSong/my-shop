# T5-02 开发知识文档：负载测试与性能分析

> 目标读者：没有参与本次实验、但需要在两周后复现实验或回答面试提问的人。
> 本文记录「实验怎么设计、怎么执行、怎么分析、哪里只能观察不能下结论」，
> 不重复 `docs/reports/t5-02-load-performance-analysis/` 里的表格（那边是交付结论）。
>
> 分支：`feat/k8s-t5-02-load-performance-analysis`
> 基线：`feat/k8s-t5-01-k6-test-framework` @ `a069b9d9d8f0f24e74c1fe4be4d5723023a31505`
> 实验时间：2026-11-10 UTC 04:31–04:45；run ID：`t502-a-1vu` / `t502-b-5vu` / `t502-c-10vu`

---

## 1. 任务背景与目标

T5-01 建好了 k6 框架并跑通 Smoke（9 个请求），但没有真正的负载实验。
T5-02 要在**不改业务代码、不改基础设施**的前提下，用已有框架对只读 HTTP 链路
施加三档递增负载，回答：

1. QPS 随并发如何变化？
2. 延迟（P50/P90/P95/P99）是否随负载上升？
3. 错误率是否变化？
4. 服务端 Prometheus 指标与客户端结果是否一致？
5. 10 VU 以内有没有拐点？
6. 哪些现象只能观察、不能下因果结论？

范围边界：只做 `read-load.js`（只读链路）；不做秒杀正确性（T5-03）、
不做 Kafka 故障恢复（T5-04）、不做 `db_scanner` vs `kafka` 对比（只读链路与消费模式无关，
且切换需要改部署配置，属禁止动作）。

---

## 2. 原有代码结构（本次改动的起点）

T5-01 交付的框架（只列与 T5-02 相关的）：

| 文件 | 与 T5-02 的关系 |
|---|---|
| `test/k6/scenarios/read-load.js` | 唯一的只读负载场景：每迭代 1 次列表 +（可选）1 次详情 + `sleep(THINK_TIME_MS)` |
| `test/k6/lib/stages.js` | 负载档位（low/medium/high）与 `LOAD_VUS` 覆盖、阈值骨架 |
| `test/k6/lib/http.js` | 请求封装：超时、tag、`{code,message,data}` 解析、错误分类 |
| `test/k6/lib/report.js` | `handleSummary` → `summary.json` / `summary.txt` |
| `test/k6/scripts/run.ps1` | docker 运行 k6 + 收集 `meta.json`（Git SHA / context / 镜像 / 起止时间） |

原始 `read-load.js` 的参数：`LOAD_PROFILE`、`LOAD_VUS`、`PRODUCT_ID`、`THINK_TIME_MS`。
原始 summary 只输出 k6 默认统计（avg/min/med/p90/p95/max），**没有 P99**，也没有按接口拆分。

---

## 3. 本次做了什么

| 改动 | 文件 | 原因 |
|---|---|---|
| 修正 `LOAD_VUS` 覆盖语义 | `lib/stages.js` | 原实现让「稳态阶段」变成 120s 慢爬坡（详见 §9 问题 1） |
| 开启 `summaryTrendStats`（含 p(50)/p(99)） | `scenarios/read-load.js` | 任务要求 P99，k6 默认不给 |
| 新增按接口 Trend `k6_products_list_ms` / `k6_products_detail_ms` | `scenarios/read-load.js` | 两个接口耗时差 3–4 倍，只有混合分位数会互相掩盖 |
| 打印「目标接口」并在缺 `PRODUCT_ID` 时告警 | `scenarios/read-load.js` | 防止漏传参数后静默降级（§9 问题 5 真实发生过） |
| README 实验 A 定稿参数 | `test/k6/README.md` | 把「建议值」替换为实测采用值 |
| 执行 A/B/C 三档实验 + Prometheus 关联分析 | —（纯执行） | 本任务主体 |

没有改：Go 业务代码、Schema、K8s 清单、Prometheus 配置、Grafana Dashboard、Secret。

---

## 4. 具体实现方式

### 4.1 负载形状怎么定义

k6 的 `stages` 里 `target` 是**该阶段结束时的 VU 数**，k6 在整个 `duration` 内做线性过渡：

```js
stages: [
  { duration: '30s',  target: 1 },   // 0 → 1（ramp-up）
  { duration: '120s', target: 1 },   // 1 → 1（真正的稳态）
  { duration: '15s',  target: 0 },   // 1 → 0（ramp-down）
]
```

只有第二个阶段的 target **等于前一阶段结束时**，才是稳态。这是本任务修 bug 的关键（§9 问题 1）。

三档只改 `target`，时长结构完全一致，因此「差异只来自并发度」：

| 实验 | 峰值 VU | 时长 | 思考时间 |
|---|---:|---|---:|
| A | 1 | 30 + 120 + 15 s | 200 ms |
| B | 5 | 同上 | 200 ms |
| C | 10 | 同上 | 200 ms |

### 4.2 一次迭代做了什么

```js
export default function () {
  const list = biz.listProducts(client, 'page=1&size=20');   // GET /products
  ...checks + listDuration.add(list.res.timings.duration)

  if (productId > 0) {
    const detail = biz.getProduct(client, productId);        // GET /products/:id
    ...checks + detailDuration.add(detail.res.timings.duration)
  }

  sleep(thinkTimeMs / 1000);                                 // 200ms 用户思考时间
}
```

- 每迭代 **2 个请求**（列表 + 详情），因此 `http_reqs = 2 × iterations`。
  这个恒等式是**验证实验是否按预期执行**的第一道自检（§9 问题 5 就是靠它发现的）。
- `expectHttpOk` / `expectEnvelopeOk` 每请求 3 个 check，所以
  `checks = 6 × iterations`（A：702×6=4212 ✓，B：3051×6=18306 ✓，C：6163×6=36978 ✓）。

### 4.3 VU → RPS 的换算

```
每 VU 迭代速率 ≈ 1 / (单迭代耗时) = 1 / (列表耗时 + 详情耗时 + 0.2s)
每 VU RPS      ≈ 2 × 迭代速率
总 RPS         ≈ VU 数 × 每 VU RPS
```

实测：每 VU 迭代 4.25–4.27 次/秒、每 VU 7.4–8.5 QPS，与上式一致
（0.2s 思考时间占单迭代 80% 以上，所以 RPS 几乎由 `THINK_TIME_MS` 决定，
而不是由服务端延迟决定——这点在解读 QPS 时非常重要）。

---

## 5. 核心逻辑：为什么这样测才有可比性

1. **同形状**：三档时长结构一致，只有 VU 不同；
2. **同数据**：固定 `page=1&size=20` 与 `product_id=4`，避免参数差异导致缓存/索引命中不同；
3. **同版本**：`meta.json` 记录 `git_sha`、`app_image`、`kube_context`，可验证三次是同版本；
4. **同口径**：客户端与 Prometheus 用同一时间窗口（`meta.json` 的起止 UTC）；
5. **有冷却**：档间 20s，避免上一档的排队与 GC 影响下一档；
6. **有自检**：`http_reqs = 2 × iterations`、`checks = 6 × iterations`、
   `view_count` 增量 = 详情请求数，三个恒等式互相印证。

---

## 6. 实验流程（可照做）

```powershell
# 0) 环境核验
kubectl config current-context                 # kind-surgecart-local
kubectl get nodes                              # Ready
kubectl get pods -n surgecart                  # app/mysql/redis/kafka 全 Running
kubectl get pods -n monitoring                 # prometheus/grafana Running
kubectl -n surgecart  port-forward svc/surgecart  18000:8000
kubectl -n monitoring port-forward svc/prometheus 9090:9090
curl http://127.0.0.1:9090/api/v1/query?query=up{job="surgecart"}   # 必须为 1

# 1) 取一个真实存在的 on_shelf 商品 id（只读，不创建数据）
curl "http://127.0.0.1:18000/products?page=1&size=5"

# 2) 三档实验（每条命令都重设全部环境变量，shell 不共享）
$env:LOAD_VUS='1'; $env:PRODUCT_ID='4'; $env:THINK_TIME_MS='200'; $env:EXPECT_DISPATCH_MODE='kafka'
powershell -ExecutionPolicy Bypass -File test/k6/scripts/run.ps1 `
  -Scenario read-load -RunId t502-a-1vu -Profile low -BaseUrl http://host.docker.internal:18000
# 冷却 20s → LOAD_VUS=5 → t502-b-5vu → 冷却 20s → LOAD_VUS=10 → t502-c-10vu

# 3) 事后核验
kubectl get pods -n surgecart -o custom-columns='NAME:.metadata.name,RESTARTS:.status.containerStatuses[0].restartCount'
```

每档约 165s（2m45s），加 docker 启动与结果写出约 2m50s。

---

## 7. k6 配置与参数含义

| 参数 | 本次取值 | 含义 / 影响 |
|---|---|---|
| `LOAD_PROFILE` | `low` | 选档位模板（时长结构来自 `lib/stages.js`） |
| `LOAD_VUS` | 1 / 5 / 10 | 覆盖峰值 VU；**修正后 ramp 直达该值**，保证稳态 |
| `THINK_TIME_MS` | 200 | 每迭代后 `sleep`；直接决定 VU→RPS 换算 |
| `PRODUCT_ID` | 4 | >0 时才压详情；缺失会告警（不静默） |
| `HTTP_TIMEOUT_MS` | 5000（默认） | 单请求超时；本次最大观测 1256ms，未触发 |
| `summaryTrendStats` | avg/min/med/p50/p90/p95/p99/max | 决定 summary 里的分位数集合 |
| `options.tags` | `scenario=read-load` | 结果可按场景区分（k6 本地 tag，不上报服务端） |
| `thresholds` | `checks rate==1`、`k6_request_errors count==0`、`k6_envelope_errors count==0` | 只拦「框架/环境坏掉」，不预设性能目标 |

自定义指标（本次新增两个）：

| 指标 | 类型 | 用途 |
|---|---|---|
| `k6_products_list_ms` | Trend（ms） | 列表接口客户端耗时分布 |
| `k6_products_detail_ms` | Trend（ms） | 详情接口客户端耗时分布 |

为什么要用自定义 Trend 而不是 `http_req_duration{name:...}`：
k6 的 tag 子指标只有在被阈值或明确引用时才会稳定导出，且命名依赖 tag 拼接；
自定义 Trend 语义明确、一定出现在 summary 里，也不会为一个「只是为了导出指标」的阈值引入误报。

---

## 8. 如何关联 Prometheus

### 8.1 唯一桥梁是时间窗口

k6 不带业务标签给服务端（否则指标基数爆炸），所以只能按时间对齐：

1. `run.ps1` 在 docker 调用前后写 `meta.json` 的 `started_at_utc` / `finished_at_utc`；
2. 用这两个时刻换算 epoch，作为 Prometheus 查询的 `time=` 参数；
3. 计数器类指标做「窗口首尾差值」，直方图类用 `increase(...[3m])` + `histogram_quantile`。

### 8.2 本次用到的查询

```promql
# 请求数（窗口首尾各查一次，相减）
surgecart_http_requests_total{job="surgecart"}

# 服务端分位数（末尾往前 3 分钟窗口）
histogram_quantile(0.99,
  sum by (le) (increase(surgecart_http_request_duration_seconds_bucket{job="surgecart",route="/products/:id"}[3m])))

# 慢请求分桶（对 status 求和后再读各 le）
sum by (le) (increase(surgecart_http_request_duration_seconds_bucket{job="surgecart",route="/products/:id",le="0.5"}[3m]))

# 运行时与依赖
max_over_time(surgecart_http_requests_in_flight{job="surgecart"}[3m])
max_over_time(go_goroutines{job="surgecart"}[3m])
max_over_time(go_memstats_heap_alloc_bytes{job="surgecart"}[3m])
max_over_time(process_resident_memory_bytes{job="surgecart"}[3m])
process_cpu_seconds_total{job="surgecart"}                      # 首尾差值 / 窗口秒数 = 平均核数
go_gc_duration_seconds_count{job="surgecart"}                   # 首尾差值 = GC 次数
max_over_time(surgecart_kafka_consumer_lag{job="surgecart"}[3m])
max_over_time(flashsale_outbox_backlog_pending[3m])
min_over_time(surgecart_sampler_up{job="surgecart"}[3m])
```

### 8.3 两侧对不上的地方要能解释

| 现象 | 解释 |
|---|---|
| 服务端请求数比 k6 少 1.4%–3.4% | 抓取间隔 15s：窗口结束时刻的最后一段流量还没被 scrape |
| 列表客户端 P50 5.0ms vs 服务端 1.9ms | port-forward + Docker 网络 + k6 自身开销；且服务端直方图桶在 2.5/5ms 处粗，P50 是插值 |
| `in_flight_max` 只有 0/1/2 | Little's law：`L = λW`，10 VU 时 λ≈74.7、W≈15.7ms → L≈1.17 |

---

## 9. 遇到的问题（全部真实发生）

### 问题 1：`LOAD_VUS` 覆盖把稳态阶段变成了爬坡

**现象**（规划阶段读代码发现）：`buildStages` 里 ramp 阶段被设成 `ceil(peak/4)`，
持续阶段设成 `peak`。而 k6 的 stage target 是阶段结束时的值，于是「持续 120s」实际是
从 `peak/4` 缓慢爬到 `peak`——**全程没有稳态**，Prometheus 只能看到单调上升的曲线。

**为什么危险**：`LOAD_VUS=10` 时前 150s 里 VU 一直在增长，
「同一负载下的延迟对比」根本不存在，但结果看起来一切正常（错误率 0、check 全绿）。

**定位**：把 k6 stage 语义与代码对照；再用探针脚本打印 `JSON.stringify(buildStages(...))` 确认。

**解决**：ramp 阶段的 target 也设为 `peak`（见 `lib/stages.js` 注释），
探针输出确认为 `[30s→10, 120s→10, 15s→0]`。

### 问题 2：`k6 inspect` 读不到容器环境变量

**现象**：为了「不跑负载也能验证参数」，用 `docker run -e LOAD_VUS=10 ... k6 inspect`，
但阶段展开仍是 5（默认档），好像 `-e` 没生效。

**定位**：
1. 先用 `--entrypoint sh -c 'echo $LOAD_VUS'` 证明容器环境里确实有该变量；
2. 再写一个探针脚本打印 `__ENV.LOAD_VUS`，`inspect` 下为 `undefined`；
3. 同一探针改用 `k6 run`，`__ENV.LOAD_VUS=10` 正常可见。

**根因**：`k6 inspect` 不会把进程环境注入 `__ENV`（只有 `run` 会）。

**影响与修正**：T5-01 文档里「`k6 inspect` 能验证配置缺失处理」的说法需要收窄——
它能验证**语法、导入、init 期纯代码逻辑**，但不能验证依赖 `__ENV` 的分支。
因此凡是「参数是否正确生效」的验证，本次改用「无请求的探针 + `k6 run`（1 迭代、不发 HTTP）」。

### 问题 3：k6 默认 summary 没有 P99

**现象**：第一次 A 档跑完，`summary.json` 里只有 `avg/min/med/p(90)/p(95)/max`，
任务要求的 P99 缺失。

**根因**：k6 的默认 `summaryTrendStats` 不含 p(99)。

**解决**：在 `read-load.js` 的 `options` 里显式声明
`summaryTrendStats: ['avg','min','med','p(50)','p(90)','p(95)','p(99)','max']`，重跑 A 档。

### 问题 4：全局 `http_req_duration` 掩盖了两个接口的差异

**现象**：A 档全局 P50 15.7ms，看起来「读接口 16ms」；拆开看列表 5.5ms、详情 22.6ms，
差 4 倍。用一个混合分位数描述系统会得出错误结论。

**解决**：新增两个 Trend（`k6_products_list_ms` / `k6_products_detail_ms`），
在请求处 `add(res.timings.duration)`。这件事同时带来 §6.5 的关键洞察
（长尾只在写路径）。

### 问题 5：环境变量不随 shell 会话保留，导致一次「静默降级」的无效实验

**现象**：第一次跑 B 档时只设置了 `LOAD_VUS=5`，忘了 `PRODUCT_ID`（每条命令是新的 shell）。
结果 `http_reqs = iterations`（每迭代 1 个请求），详情接口一次都没被压；
但阈值依然全绿、错误率 0，**结果看起来完全正常**。同时 `view_count` 增量为 0，
这个异常才暴露了问题。

**定位**：`http_reqs(3474) == iterations(3474)` 打破 `http_reqs = 2 × iterations` 恒等式；
`view_count` 增量 0 进一步确认没有任何详情请求。

**解决**：
1. 判定该次运行不可比，丢弃并重跑（B 档重跑后 `6102 = 2 × 3051`）；
2. 在 `read-load.js` 里显式打印「目标接口：GET /products + GET /products/4」，
   缺 `PRODUCT_ID` 时 `console.warn` 提示「本次只压列表页」，让降级在日志第一屏可见；
3. 此后每条命令都重设全部环境变量，并在跑完后核对恒等式。

**教训**：静默降级比报错更危险——阈值全绿不代表实验按设计执行。
凡是「参数决定请求集合」的场景，都应该有一条可见的「本次实际压了什么」的记录。

### 问题 6：PromQL 查询取到了错误的序列（结果全是 0）

**现象**：统计详情接口各桶请求数时，所有 `le` 都返回 0，而同一时刻的
`histogram_quantile` 分明有正常值。

**定位**：打开原始响应看，`route="/products/:id"` 下存在**两条序列**：
`status="200"`（真实计数）与 `status="404"`（值为 0，历史残留）。
查询没做聚合时返回多条结果，脚本取了 `result[0]`，恰好是 404 那条。

**解决**：所有按桶的查询统一加 `sum by (le) (...)`；
凡是「多标签序列取单值」的地方都要先明确聚合维度，不能依赖返回顺序。

### 问题 7：服务端直方图桶对 2–5ms 的延迟太粗

**现象**：列表服务端 P50 = 1.87ms，客户端 P50 = 4.98ms，差 3.1ms；
详情两侧只差 0.5ms。

**定位**：HTTP 直方图桶边界是
`0.0005/0.001/0.0025/0.005/0.01/...`（秒），即 0.5/1/2.5/5/10 ms。
列表的真实 P50 就在 2.5–5ms 之间，`histogram_quantile` 只能线性插值，
所以 1.87ms 是估计值而不是实测值；详情 ~18ms 落在 10–25ms 区间，插值误差相对小。

**处理**：报告里明确标注「列表服务端分位数精度不足，跨侧比较以客户端为准」，
并且**不**用这两个数的差值去推导链路开销。

### 问题 8：详情接口其实会写库（实验前必须发现）

**现象**：审查源码时发现 `internal/logic/product/product.go::Detail` 每次请求执行
`UPDATE products SET view_count = view_count + 1, updated_at = updated_at WHERE id = ? AND status = ?`。

**为什么重要**：
1. 任务要求确认「请求不会修改业务数据」——这里有一次写；
2. 性能上它是**写路径**（行锁 + 提交 fsync），与纯 SELECT 的列表接口不可混为一谈。

**处理**：
1. 不回避、不隐瞒：把「详情 = 读 + 一次计数写」写进报告与场景注释；
2. 用 `view_count` 首尾差作为**写入次数证据**，实测 702 / 3051 / 6163，
   与 k6 的详情请求数完全相等（同时也验证了「没有额外写放大」）；
3. 只影响 1 个 T3-02 测试商品的计数字段，未创建订单/未改库存/未创建活动；
4. 这个发现直接支撑了 §6.5 的分析（长尾只在写路径）。

---

## 10. 关键技术决策及原因

| 决策 | 选择 | 放弃的方案 | 原因 |
|---|---|---|---|
| 负载档位 | 1 / 5 / 10 VU | 5 / 20 / 50（T5-01 默认） | 笔记本单节点，任务要求保守；且默认值本身被标注为「待 T5-02 定稿」 |
| 稳态时长 | 120s | 更长 | 165s/档 × 3 档在可接受时间内，且 120s 已覆盖 ~10 个抓取周期 |
| 思考时间 | 200ms | 0（纯忙等） | 真实用户有间隔；0 思考时间会让 RPS 取决于服务端延迟，扭曲压力形状 |
| 接口 | 列表 + 详情 | 只测列表 | 只测列表无法暴露写路径长尾；任务明确允许商品详情 |
| 指标 | 自定义 Trend 按接口拆分 | 只看全局 `http_req_duration` | 两接口差 4 倍，混合分位数会误导 |
| 分位数来源 | k6（客户端）+ Prometheus（服务端） | 只用一侧 | 客户端含隧道开销，服务端只有应用内耗时，必须分开看 |
| 数据 | 复用已有 on_shelf 测试商品 | `prepare-data.js` 新建 | 准备工具需要管理员凭据（禁止读取），且任务禁止执行数据准备 |
| 消费模式对比 | 不做 | kafka vs db_scanner | 只读链路与消费模式无关；切换需改部署（禁止） |
| 阈值 | 只保留框架级 | 设 p95/p99 SLA | 没有基线就设 SLA 等于把猜测写成验收标准 |

---

## 11. 结果如何解释（结论与证据对应）

| 结论 | 证据 |
|---|---|
| QPS 线性增长（8.5→37→75） | k6 `http_reqs.rate`；每 VU 吞吐 7.4–8.5 稳定 |
| 延迟未恶化 | P50 15.71→13.53→13.13 ms；P95 29.35→28.13→26.70 ms |
| 无错误 | `http_req_failed=0`、`k6_request_errors=0`、checks 100%（4212/18306/36978） |
| 未触资源上限 | 进程 CPU 平均 0.153 核 = 500m 的 31%；heap ≤5.5MB；RSS ~40MB；goroutine 57 |
| 服务端与客户端一致 | 请求数差 1.4–3.4%（抓取边界可解释）；详情 P50 两侧 18.2 vs 18.7 ms |
| 长尾只在写路径 | 列表三档无一次 >26ms；>250ms 的请求 100% 落在 `/products/:id` |
| Kafka/Outbox 无变化 | lag/backlog/oldest_age 全 0，`sampler_up=1`（只读负载本就不该触及） |
| in-flight 语义正确 | Little's law：λW = 0.14/0.65/1.17，观测 max 0/1/2 |

**不能下的结论**（明确写进报告）：
- 不能推出系统容量上限或「支持 75 RPS」作为能力声明；
- 不能推出「并发越高越好」——只是本区间内未饱和；
- 不能把详情长尾归因为 fsync/行锁（缺少 MySQL 与磁盘指标）；
- 不能用 Kafka lag=0 说明秒杀链路健康。

---

## 12. 如何重新执行实验

见 §6 的命令块。补充要点：

1. **端口**：宿主机 8000 被 Windows 保留端口占用，必须用 18000（`port-forward 18000:8000`）；
2. **环境变量每条命令都要重设**，或在同一 shell 会话里连续执行；
3. **每档后核对恒等式**：`http_reqs == 2 × iterations`、`checks == 6 × iterations`、
   `view_count` 增量 == 详情请求数（用 `GET /products` 读回 `view_count`）；
4. **档间冷却 20s**；
5. **结果位置**：`test/k6/results/<run_id>.{summary.json,summary.txt,meta.json,k6.log}`；
6. **Prometheus 关联**：用 `meta.json` 的起止时间做 epoch，按 §8.2 的 PromQL 查询；
7. 若要作废一次运行，直接换 run ID，不要覆盖旧结果（覆盖会丢失「为什么作废」的证据）。

---

## 13. 已知限制与未验证内容

| 项 | 状态 | 说明 |
|---|---|---|
| 三档只读负载实验 | VERIFIED | 1/5/10 VU，各 165s，独立 run ID |
| 客户端指标（QPS/分位/错误率） | VERIFIED | k6 summary + 自定义 Trend |
| 服务端请求数与分位数 | VERIFIED | Prometheus 计数器差值 + histogram_quantile |
| 运行时指标 | VERIFIED | goroutine/heap/RSS/CPU/GC |
| Kafka/Outbox 指标 | VERIFIED（值为 0） | 只读负载下本就不应变化 |
| 长尾根因 | **NOT_VERIFIED** | 缺 MySQL/磁盘指标，只到「写路径」这一层 |
| 系统容量上限 / 拐点 | **NOT_VERIFIED** | 10 VU 未饱和，未继续加压（任务禁止） |
| `db_scanner` vs `kafka` 对比 | **NOT_VERIFIED（不在本任务范围）** | 只读链路无关，且切换需改部署 |
| 秒杀写入链路性能 | NOT_VERIFIED | 属 T5-03/T5-04 |
| container/cgroup 级 CPU 节流 | NOT_VERIFIED | 未部署 cAdvisor 指标，只有进程指标 |
| 多副本 / 生产参数 | NOT_VERIFIED | 单副本、本地集群 |

---

## 14. 面试时可展开的知识点

**k6 / 压测**
1. `stages` 的 `target` 是「阶段结束时的 VU」，不是恒定值；写错会让"稳态"变成爬坡（本次真实 bug）。
2. 为什么 VU 数不等于 RPS？RPS ≈ VU / 单迭代耗时；思考时间常常是主导项。
3. k6 默认 summary 只给 med/p90/p95，P99 需要 `summaryTrendStats`。
4. `http_req_failed` 默认口径把 4xx 当失败；业务 4xx 多的系统需要重定义（T5-01 已做）。
5. `k6 inspect` 不注入进程环境变量到 `__ENV`，不能用来验证 `-e` 参数是否生效。
6. 压测机与被测系统同机时，客户端可能先饱和；本次用 port-forward 单隧道，是已知瓶颈风险。

**可观测性**
7. 客户端 P95 与服务端 P95 的差值 ≈ 网络 + 代理 + 客户端开销；两者要分开看。
8. Histogram 桶边界决定分位数精度；桶太粗时 `histogram_quantile` 只是插值（本次列表 P50 就是例子）。
9. Counter 做窗口差值、Gauge 做 max/min、Histogram 做分位数——用错会产生荒谬结论。
10. 当一条 PromQL 返回多标签序列时，`result[0]` 是随机顺序，必须先 `sum by` 明确聚合维度。
11. `in_flight` 与 Little's law 的互相印证，是验证并发指标语义正确性的廉价手段。

**性能分析**
12. 判断「是否饱和」看每 VU 吞吐是否随并发下降，而不是只看 QPS 绝对值。
13. CPU 用量要结合 limit 看：0.153 核本身无意义，"占 500m 的 31%" 才有意义。
14. 长尾（P99）与均值是两回事：6/3256 个慢请求就能把 P99 从 40ms 抬到 560ms。
15. 「读接口」也可能是写路径：本次详情接口每次都自增 `view_count`，因此行锁与提交开销进入延迟。
16. 同一条隧道下，只有某个接口出现长尾 → 可以排除传输层，把怀疑集中到该接口的服务端逻辑。
17. 相关性不等于因果：缺 MySQL/磁盘指标时，只能说「写路径长尾」，不能说「fsync 导致」。

**工程实践**
18. 静默降级比报错更危险：参数漏传导致请求集合变化，指标却全绿；要用恒等式自检。
19. 结果要可追溯：`meta.json` 记录 Git SHA/镜像/context，否则两次结果无法证明同版本。
20. 实验作废要留证据：换 run ID 而不是覆盖，报告里写清「作废原因」。

---

## 15. 附：本次改动的文件

```text
test/k6/lib/stages.js               LOAD_VUS 覆盖语义修正
test/k6/scenarios/read-load.js      P99 + 按接口 Trend + 目标接口日志/告警 + 状态注释
test/k6/README.md                   实验 A 定稿参数与状态更新
.agent/tasks/dev/T5-02-load-performance-analysis.md   本文
docs/reports/t5-02-load-performance-analysis/
├── T5-02_LOAD_PERFORMANCE_ANALYSIS_REPORT.md
└── raw/  6 个 summary/meta 证据文件
```

未修改：Go 业务代码、数据库 Schema、Redis Lua、Kafka 架构、K8s 清单、
Prometheus 配置、Grafana Dashboard、Secret、其他任务报告。
