# T5-02 负载测试与性能分析 — 交付报告

- 项目：SurgeCart
- 任务：T5-02（任务五第 2 子任务）
- 分支：`feat/k8s-t5-02-load-performance-analysis`
- 基线：`feat/k8s-t5-01-k6-test-framework` @ `a069b9d9d8f0f24e74c1fe4be4d5723023a31505`
- 实验日期：2026-11-10（UTC 04:31–04:45）
- 实验位置：本地笔记本 → Docker Desktop → kind 单节点集群

> **定位声明**：本报告是**本地单节点实验**，不是生产环境性能数据，不构成任何容量承诺。
> 所有数值均来自真实执行（k6 + Prometheus），执行命令与 run ID 可复核。

---

## 1. 实验目的

用 T5-01 建立的 k6 框架，对 SurgeCart 的**只读 HTTP 链路**施加三档递增负载，回答：

1. 增加并发用户数后，QPS 如何变化？
2. 延迟（P50/P90/P95/P99）是否随负载上升？
3. 是否出现错误率变化？
4. 服务端指标（Prometheus）与客户端结果是否一致？
5. 在 10 VU 以内是否观察到性能拐点或资源瓶颈？
6. 哪些现象只能观察、不能下因果结论？

不在本次范围：秒杀业务正确性（T5-03）、Kafka 积压与故障恢复（T5-04）、
`db_scanner` 与 `kafka` 两种消费模式的吞吐对比（只读链路与消费模式无关，且切换需改部署配置）。

---

## 2. 实验环境

| 项 | 值 | 采集方式 |
|---|---|---|
| 宿主机 | ASUS TUF Gaming F15，Intel i7-12700H（14C/20T），31.6 GiB RAM | `Get-CimInstance Win32_ComputerSystem/Processor` |
| 虚拟化 | Docker Desktop 29.9.3，分配 20 CPU / 15.8 GiB | `docker info` |
| 集群 | kind `surgecart-local-control-plane`，单节点，`Ready` | `kubectl get nodes -o wide` |
| Kubernetes | v1.37.0（kubeadm 节点），containerd 2.3.4，Debian 13 | 同上 |
| 内核 | 6.6.87.2-microsoft-standard-WSL2 | 同上 |
| 应用镜像 | `surgecart:64ebbbd-t4-03` | `meta.json.app_image` |
| 应用副本 | 1（`surgecart-7d4d84b88-xn4tb`，Pod IP 10.244.0.25） | `kubectl get pods -n surgecart` |
| 应用资源 | requests 100m/256Mi，**limits 500m/512Mi** | `kubectl get deploy` |
| MySQL | `mysql:8.0`，limits 1 CPU / 1Gi | 同上 |
| Redis | `redis:7-alpine`，limits 500m / 256Mi | 同上 |
| Kafka | `apache/kafka:3.9.1`，limits 1 CPU / 1Gi（KRaft 单节点） | 同上 |
| Prometheus | `prom/prometheus:v2.53.0`，scrape 15s / timeout 10s，`job=surgecart` | `manifest/.../configmap.yaml` |
| Grafana | `grafana/grafana:11.2.2`，dashboard uid `surgecart-overview` | `kubectl get deploy -n monitoring` |
| 应用配置 | `FLASH_SALE_DISPATCH_MODE=kafka`、`FLASH_SALE_METRICS_ENABLED=true`、`READINESS_CACHE_TTL=3` | ConfigMap `surgecart-config`（非敏感项） |
| k6 | `grafana/k6:0.54.0`（容器内 `linux/amd64`） | `docker run ... k6 version` |
| 流量入口 | `kubectl port-forward svc/surgecart 18000:8000` → k6 用 `host.docker.internal:18000` | — |

宿主机 8000 端口被 Windows 保留端口区占用，因此转发使用 18000（T5-01 已记录）。

安全：未读取或打印任何 Secret 取值；管理员凭据未使用；实验不涉及注册用户与下单。

---

## 3. 实验设计

### 3.1 被测接口（只读）

| 接口 | 说明 | 服务端行为 |
|---|---|---|
| `GET /products?page=1&size=20` | 商品列表 | SELECT（分页，含 count） |
| `GET /products/{id}`（id=4） | 商品详情 | 先 `UPDATE products SET view_count=view_count+1`，再 SELECT 详情 + SKU |

**重要事实（本次实测确认）**：详情接口不是纯读接口，每次请求都会自增
`products.view_count`（`internal/logic/product/product.go` 的 `Detail` 有意设计）。
该写入只影响 1 个商品的计数字段，不创建订单、不改库存、不创建活动。
本次三档实验对 `view_count` 的总增量为 **702 / 3051 / 6163**，与 k6 的详情请求数
**逐一相等**（见 §5.5），说明写入次数与请求数严格对应，没有额外写放大。

选择该商品的原因：它是 T3-02 端到端验证留下的 `on_shelf` 测试商品（id=1..4 之一），
不是真实客户数据；`size=20` 时列表只返回 4 条，响应体小且稳定，便于跨档比较。

### 3.2 负载形状

三档共用同一时间结构，仅峰值 VU 不同（这样差异只来自并发度）：

```
0 ──30s ramp──▶ 峰值 VU ──120s 稳态──▶ 0（15s ramp-down）   总计 165s
```

| 实验 | run ID | LOAD_PROFILE | 峰值 VU | 思考时间 | 每迭代请求数 |
|---|---|---|---:|---:|---:|
| A 基准 | `t502-a-1vu` | low | 1 | 200 ms | 2 |
| B 正常 | `t502-b-5vu` | low | 5 | 200 ms | 2 |
| C 增强 | `t502-c-10vu` | low | 10 | 200 ms | 2 |

- **思考时间 200ms**：模拟用户浏览间隔，避免 VU 以纯忙等循环刷服务端；
  它决定了「VU 数 → RPS」的换算（约 1/(0.2+延迟) 迭代/秒/VU）。
- **冷却**：三档之间静置 20s，避免上一档的排队请求与 GC 影响下一档。
- **顺序**：先 A 后 B 后 C，逐档递进；任一档出现错误率上升或 Pod 重启则停止后续。
- **隔离**：每次执行独立 `RUN_ID`、独立结果文件；环境变量在每条命令中显式重设。

### 3.3 采集方式

| 侧 | 采集 | 说明 |
|---|---|---|
| 客户端 | k6 内建 `http_reqs` / `http_req_duration` / `http_req_failed` / `checks` + 自定义 Trend `k6_products_list_ms`、`k6_products_detail_ms` | `summaryTrendStats` 显式开启 p(50)/p(90)/p(95)/p(99) |
| 服务端 | Prometheus 按**运行窗口首尾的计数器差值** 与 `histogram_quantile` | 窗口取自 `meta.json` 的 `started_at_utc` / `finished_at_utc` |

关联方式：k6 的请求不打业务标签到服务端（避免高基数），两侧靠**时间窗口**对齐；
窗口边界由 runner 记录的 `meta.json` 提供，不依赖 summary 内的导出时刻。

---

## 4. 执行过程

```powershell
# 1) 环境核验（Pod 全 Running、Target UP、无重启）
kubectl get nodes; kubectl get pods -n surgecart; kubectl get pods -n monitoring
kubectl -n surgecart  port-forward svc/surgecart  18000:8000
kubectl -n monitoring port-forward svc/prometheus 9090:9090

# 2) A：1 VU
$env:LOAD_VUS='1'; $env:PRODUCT_ID='4'; $env:THINK_TIME_MS='200'; $env:EXPECT_DISPATCH_MODE='kafka'
powershell -ExecutionPolicy Bypass -File test/k6/scripts/run.ps1 `
  -Scenario read-load -RunId t502-a-1vu -Profile low -BaseUrl http://host.docker.internal:18000
# 冷却 20s

# 3) B：5 VU（参数完全相同，仅 LOAD_VUS 变化）
$env:LOAD_VUS='5'
powershell ... -RunId t502-b-5vu ...
# 冷却 20s

# 4) C：10 VU
$env:LOAD_VUS='10'
powershell ... -RunId t502-c-10vu ...

# 5) 结果与窗口
test/k6/results/t502-{a,b,c}-*.{summary.json,summary.txt,meta.json,k6.log}

# 6) Prometheus 窗口查询（示例：详情接口 P99）
curl "http://127.0.0.1:9090/api/v1/query?query=histogram_quantile(0.99,sum by (le)(increase(surgecart_http_request_duration_seconds_bucket{job=\"surgecart\",route=\"/products/:id\"}[3m])))&time=<窗口结束 epoch>"
```

执行过程中实际发生过一次返工，如实记录：

| 轮次 | run ID | 结果 | 处置 |
|---|---|---|---|
| A（第 1 次） | `t502-a-1vu` | 缺 P99，缺按接口拆分 | 增加 `summaryTrendStats` 与自定义 Trend 后重跑 |
| A（第 2 次） | `t502-a-1vu` | 采用 | 报告数据来源 |
| B（第 1 次） | `t502-b-5vu` | **只压了列表页**：新 shell 未继承 `PRODUCT_ID`，每迭代请求数从 2 变 1，脚本静默降级且阈值全绿 | 判定不可比、丢弃；补上「目标接口」日志与告警后重跑 |
| B（第 2 次） | `t502-b-5vu` | 采用 | 报告数据来源 |
| C | `t502-c-10vu` | 采用 | 报告数据来源 |

---

## 5. 真实测试结果

### 5.1 客户端总体对比（k6）

| 指标 | A：1 VU | B：5 VU | C：10 VU |
|---|---:|---:|---:|
| 实际请求数 | 1,404 | 6,102 | 12,326 |
| 迭代数（每迭代 2 请求） | 702 | 3,051 | 6,163 |
| 平均 QPS（全窗口 165s） | **8.51** | **36.95** | **74.69** |
| 每 VU 吞吐（QPS/VU） | 8.51 | 7.39 | 7.47 |
| P50（ms） | 15.71 | 13.53 | 13.13 |
| P90（ms） | 26.68 | 25.20 | 23.31 |
| P95（ms） | 29.35 | 28.13 | 26.70 |
| P99（ms） | 37.43 | 40.26 | 37.12 |
| 平均（ms） | 16.99 | 17.52 | 15.72 |
| 最大（ms） | 932.55 | 1,165.67 | 1,255.59 |
| HTTP 错误率 | 0.00% | 0.00% | 0.00% |
| Check 成功率 | 100%（4,212 项） | 100%（18,306 项） | 100%（36,978 项） |
| 传输量（接收） | 1.54 MB | 6.68 MB | 13.49 MB |

### 5.2 按接口拆分（客户端 Trend）

| 指标 | A：1 VU | B：5 VU | C：10 VU |
|---|---:|---:|---:|
| 列表 P50 | 5.53 | 4.94 | 4.98 |
| 列表 P95 | 9.01 | 6.65 | 7.00 |
| 列表 P99 | 14.77 | 8.58 | 8.71 |
| 列表 max | 46.46 | 17.98 | 25.82 |
| 详情 P50 | 22.56 | 19.78 | 18.69 |
| 详情 P95 | 32.00 | 31.36 | 30.57 |
| 详情 P99 | 41.32 | **560.09** | 45.64 |
| 详情 max | 932.55 | 1,165.67 | 1,255.59 |

两个接口的重心差 3–4 倍：列表 ~5 ms，详情 ~19–23 ms（服务端多一次 UPDATE + 详情/SKU 查询）。

### 5.3 服务端指标对比（Prometheus）

**请求数核对**（窗口首尾计数器差值 vs k6 计数）：

| 实验 | k6 列表请求 | 服务端 `/products` | k6 详情请求 | 服务端 `/products/:id` |
|---|---:|---:|---:|---:|
| A | 702 | 692 | 702 | 691 |
| B | 3,051 | 2,986 | 3,051 | 2,985 |
| C | 6,163 | 5,957 | 6,163 | 5,956 |

差值 −1.4% / −2.1% / −3.4%，与「最后一次抓取发生在结束时刻之前」的量级一致
（A 8.5 RPS×1.2s≈10，B 37×1.8s≈65，C 74.7×2.8s≈207）。即**两侧计数在抓取分辨率内一致**，
不是丢请求。

**服务端延迟**（`histogram_quantile` over 3m 窗口）：

| 接口 | 实验 | P50 | P95 | P99 |
|---|---|---:|---:|---:|
| `/products` | A / B / C | 1.97 / 1.85 / 1.87 | 4.49 / 3.94 / 4.12 | 4.94 / 4.81 / 4.84 |
| `/products/:id` | A / B / C | 18.39 / 18.38 / 18.15 | 39.14 / 40.57 / 35.66 | 49.30 / **558.33** / 49.67 |

服务端与客户端差值：列表 ≈ 3 ms（port-forward + Docker 网络 + k6 侧开销），
详情 ≈ 0.5 ms。列表的服务端分位数需谨慎使用：直方图桶边界为
0.5 / 1 / 2.5 / 5 / 10 ms，列表 P50 恰好落在 2.5–5 ms 之间，属于**插值估计**，
精度不如客户端数值。

**慢请求分布**（3 分钟窗口累计计数）：

| 实验 | 接口 | 总数 | ≤50 ms | ≤250 ms | ≤500 ms | ≤1 s | >1 s |
|---|---|---:|---:|---:|---:|---:|---:|
| A | `/products` | 755 | 755 | 755 | 755 | 755 | 0 |
| A | `/products/:id` | 754 | 748 | 748 | 748 | 754 | 0 |
| B | `/products` | 3,257 | 3,257 | 3,257 | 3,257 | 3,257 | 0 |
| B | `/products/:id` | 3,256 | 3,202 | 3,219 | 3,220 | 3,250 | 6 |
| C | `/products` | 6,499 | 6,499 | 6,499 | 6,499 | 6,499 | 0 |
| C | `/products/:id` | 6,497 | 6,439 | 6,448 | 6,454 | 6,487 | 10 |

结论性事实：**列表接口在三档下从未超过 26 ms（客户端）/ 10 ms（服务端分桶）；
所有 >250 ms 的慢请求全部出现在详情（写）路径**，占该接口请求的 0.1%–1.1%。

**Go Runtime 与依赖**：

| 指标 | A | B | C |
|---|---:|---:|---:|
| `surgecart_http_requests_in_flight` 最大 | 0 | 1 | 2 |
| `go_goroutines` 最大 | 41 | 49 | 57 |
| `go_memstats_heap_alloc_bytes` 最大 | 5.13 MB | 5.36 MB | 5.51 MB |
| `process_resident_memory_bytes` 最大 | 39.2 MB | 40.0 MB | 39.4 MB |
| `process_cpu_seconds_total` 增量 → 平均核数 | 0.028 核 | 0.079 核 | 0.153 核 |
| 每请求 CPU 成本（含探针） | 3.37 ms | 2.18 ms | 2.09 ms |
| `go_gc_duration_seconds_count` 增量 | 45 | 160 | 302 |
| 每请求 GC 次数 | 0.032 | 0.026 | 0.025 |
| `surgecart_kafka_consumer_lag` 最大 | 0 | 0 | 0 |
| `flashsale_outbox_backlog_pending` 最大 | 0 | 0 | 0 |
| `surgecart_outbox_oldest_age_seconds` 最大 | 0 | 0 | 0 |
| `surgecart_sampler_up` 最小（kafka_lag / outbox） | 1 / 1 | 1 / 1 | 1 / 1 |

### 5.4 Prometheus 指标与 k6 的一致性判定

| 检查项 | 结论 |
|---|---|
| 请求总数 | 一致（差异可由抓取边界解释，见 §5.3） |
| 吞吐趋势 | 一致：服务端 RPS 随档位上升，与 k6 的 8.5 / 37 / 75 同向同幅 |
| 延迟量级 | 一致：详情服务端 P50 ≈ 18.2–18.4 ms，客户端 18.7–22.6 ms |
| In-flight | 与 Little's law 吻合：`RPS × 平均延迟` = 0.14 / 0.65 / 1.17，观测最大值 0 / 1 / 2 |
| Go Runtime | 无异常：goroutine 41→57，heap ≤5.5 MB，RSS 稳定在 ~39–40 MB |
| Kafka / Outbox | 无变化（lag 0、backlog 0、oldest age 0、采样器 up=1） |

---

## 6. 性能现象分析

### 6.1 QPS 随 VU 的增加是否线性？

**是，在 1–10 VU 区间近似线性**：8.51 → 36.95 → 74.69。
1→5 VU 提升 4.34 倍，5→10 VU 提升 2.02 倍；每 VU 吞吐稳定在 7.4–8.5 QPS。
每 VU 吞吐基本恒定是「尚未出现排队饱和」的典型特征：若出现资源争用，
每 VU 吞吐会随并发上升而下降。

### 6.2 延迟是否随负载上升？

**没有观察到恶化，反而略有下降**：P50 15.71 → 13.53 → 13.13 ms，P95 29.35 → 28.13 → 26.70 ms。

合理解释（均为可检验的**解释**，不是已证实的因果）：
1. A 档只有 1 VU、约 4.25 迭代/秒，连接与查询计划需要预热；首轮请求包含 JIT/连接池/InnoDB 缓冲池的冷启动成本；
2. 更高并发下 HTTP keep-alive 连接被更充分复用（`http_req_connecting` P99 ≈ 0.01 ms，说明几乎无新建连接）；
3. 165s 窗口里 A 只有 702 次迭代，单次慢请求对均值的影响被放大（A 的 avg 16.99 > P50 15.71，长尾拉高均值）。

**不能**据此得出「负载越高越快」的结论——这只说明在本实验区间内并发不是瓶颈。

### 6.3 是否出现错误率变化？

**没有**。三档均为：`http_req_failed = 0`、`k6_request_errors = 0`、
`k6_envelope_errors = 0`、`checks` 100% 通过（4,212 / 18,306 / 36,978 项）。
实验后 Pod 重启次数 0，Prometheus 两个 target 均 `up`，实验后 Smoke 复测 51 项 check 全通过。

### 6.4 是否存在性能拐点？

**在 1–10 VU 内没有观察到拐点**。两条独立证据：

1. 每 VU 吞吐没有下降（7.4–8.5 QPS/VU）；
2. 应用进程 CPU 平均仅 0.153 核，**占 500m limit 的 31%**；heap 5.5 MB、RSS 40 MB，
   均远低于 512Mi limit；goroutine 仅 57。

也就是说，限制不在应用进程的 CPU/内存/并发度上。若要找拐点，需要更高的 VU 或更重的接口，
这被本任务的保守档位约束挡住（不允许为追求 QPS 擅自加压）。

### 6.5 延迟长尾来自哪里？

**观察**：三档合计 33 次（A 6 / B 36 / C 48）慢请求，全部在详情接口；列表接口无一次超过 26 ms。

**可排除的因素**：
- 传输层与隧道：列表与详情共用同一条 port-forward 隧道、同一个 Go 进程、同一套中间件，
  列表零长尾，说明不是隧道或 HTTP 栈问题；
- Go Runtime：heap ≤5.5 MB，GC 周期与请求量同比例增长（无 GC 风暴），goroutine 平稳；
- 客户端（k6/宿主机）：服务端直方图同样记录到 558 ms 的 P99（B 档），说明慢发生在服务端内部。

**最可能的原因（未证实）**：详情接口每次请求执行
`UPDATE products SET view_count = view_count + 1, updated_at = updated_at WHERE id = ?`，
1) 同一行（id=4）的并发更新在 InnoDB 行锁上串行化；
2) autocommit 下每条语句都要提交，提交需要 redo fsync，WSL2 虚拟磁盘的偶发 I/O 抖动会把个别提交拉到数百毫秒。

**为什么只能观察不能下结论**：本次只有应用层指标，
没有 mysqld_exporter（无 InnoDB 行锁等待/redo 指标）、没有容器磁盘 I/O 指标、
也没有慢查询日志（`DATABASE_DEFAULT_DEBUG=false`）。缺少这些证据时，
把「写路径长尾」直接归因为 fsync 或行锁属于过度推断。

**可验证的后续实验（未执行）**：对同一商品与不同商品分别加压；在 MySQL 侧采集
`innodb_row_lock_waits`、`innodb_os_log_fsyncs` 等指标；把详情与列表混合比调成 0:1 / 1:1。

### 6.6 当前实验是否足以判断系统瓶颈？

**不足以**。可以确定的是「10 VU / 约 75 RPS 时应用进程未饱和」；
不能确定系统上限，因为：
- 客户端经 single-tunnel port-forward，未测出隧道自身的上限；
- 未加压到 CPU limit（仅 31%），未验证 500m 限制下的节流行为；
- 单节点 kind 上 MySQL/Redis/Kafka 与应用共享同一颗 CPU 与同一块虚拟磁盘；
- 只有 165s × 3 档样本，没有长时间稳定性数据。

### 6.7 只能观察、不能下结论的现象（明确清单）

| 现象 | 能说什么 | 不能说什么 |
|---|---|---|
| P50 随 VU 上升而下降 | 本区间内并发未造成排队 | 「并发越高性能越好」 |
| 详情 P99 在 B 档跳到 560 ms | 该档确有 0.2% 请求落在 250ms–1s 区间 | 「写路径在高并发下必然劣化」 |
| CPU 0.153 核 | 未触及 500m limit | 「系统还能承受 3 倍负载」 |
| Kafka lag / Outbox 恒为 0 | 只读负载未触及秒杀链路 | 「秒杀链路健康」 |
| 请求数差 −1.4%~−3.4% | 抓取边界效应 | 「丢请求」或「指标错误」 |

---

## 7. 实验局限

1. **单节点 kind + 笔记本**：应用与 MySQL/Redis/Kafka/Prometheus 共享一颗 i7-12700H 与同一块
   WSL2 虚拟磁盘，任何组件的 I/O 抖动都会传导到被测接口。
2. **Docker Desktop 虚拟化开销**：k6 与集群都在同一台机器上，压测机与被测系统争抢 CPU/内存；
   `host.docker.internal` 还要多一跳。
3. **单隧道入口**：`kubectl port-forward` 把所有连接复用到一条流上，高并发时会成为客户端侧瓶颈；
   本实验在 10 VU 未观察到该瓶颈，但无法证明更高 VU 下不会。
4. **样本量与时长**：每档 165s、最多 6,163 次迭代；P99 只基于约 60 个样本，置信度有限。
5. **指标覆盖不足**：只有应用进程指标，没有 container/cgroup 级 CPU 节流、磁盘 I/O、
   MySQL 内部指标，长尾成因无法定论。
6. **只测只读链路**：不涉及秒杀下单、库存扣减、Kafka 消费；`db_scanner` 与 `kafka` 的对比未做。
7. **写入副作用**：详情接口会自增 `view_count`（设计行为），三档共 **+9,916** 次；
   未创建订单、未改库存、未创建活动。该计数不可回滚，属于本地测试数据。
8. **不代表生产**：本地单节点、无真实流量、无多副本、无网络分区、无生产调优参数。

---

## 8. 验收结果

| 验收项 | 要求 | 结论 |
|---|---|---|
| 1. Git 基线 | 基于 T5-01 最终交付，独立分支 | ✅ PASS（`a069b9d` → `feat/k8s-t5-02-...`） |
| 2. 现有 read-load 审查 | 实际审查 | ✅ PASS（确认接口、认证、数据依赖、参数来源） |
| 3. 环境核验 | 真实 Kubernetes 环境 | ✅ PASS（Node/Pod/Target/资源全部实测） |
| 4. 三个负载级别 | 至少三档真实实验 | ✅ PASS（1 / 5 / 10 VU，各自独立 run ID） |
| 5. 独立 run ID | 每次实验独立 | ✅ PASS（`t502-a-1vu` / `t502-b-5vu` / `t502-c-10vu`） |
| 6. 原始结果存在 | summary/meta/log 真实存在 | ✅ PASS（本机 `test/k6/results/`，关键文件已归档到 `raw/`） |
| 7. QPS/分位/错误率对比 | 可对比 | ✅ PASS（§5.1–5.3） |
| 8. Prometheus 窗口关联 | 至少一组 | ✅ PASS（三组全部：请求数核对、分位数、慢请求分桶、Runtime） |
| 9. 结论有数据支持 | 不编造 | ✅ PASS（§6 每条结论均标注证据或「未证实」） |
| 10. 限制披露 | 明确 | ✅ PASS（§7） |
| 11. 开发知识文档 | 完成 | ✅ PASS（`.agent/tasks/dev/T5-02-load-performance-analysis.md`） |
| 12. 正式报告 | 完成 | ✅ PASS（本文件） |
| 13. 测试门禁 | 必要门禁通过 | ✅ PASS（`k6 inspect` exit=0；实验后 Smoke 51 checks 全过） |
| 14. 提交与推送 | 成功 | 见 §10（推送后回填） |
| 15. 工作区干净 | clean | 见 §10 |

**总判定：T5-02 完成（三档负载实验真实执行，数据与 Prometheus 双侧一致）。**

明确不宣称：
- 不宣称系统容量上限或「支持 N QPS」；
- 不宣称达到性能拐点；
- 不宣称已定位长尾根因；
- 不宣称秒杀链路或 Kafka 消费性能。

---

## 9. 原始结果文件

本机（已被 `.gitignore` 排除，不入库）：

```text
test/k6/results/t502-a-1vu.summary.json / .summary.txt / .meta.json / .k6.log
test/k6/results/t502-b-5vu.summary.json / .summary.txt / .meta.json / .k6.log
test/k6/results/t502-c-10vu.summary.json / .summary.txt / .meta.json / .k6.log
test/k6/results/t502-post-smoke.summary.json / .summary.txt    （实验后回归）
```

入库归档（体积小、可复核）：

```text
docs/reports/t5-02-load-performance-analysis/raw/
├── t502-a-1vu.summary.json   4.6 KB
├── t502-a-1vu.meta.json      1.0 KB
├── t502-b-5vu.summary.json   4.5 KB
├── t502-b-5vu.meta.json      1.0 KB
├── t502-c-10vu.summary.json  4.6 KB
└── t502-c-10vu.meta.json     1.0 KB
```

`meta.json` 内含每次运行的 Git SHA、kube context、应用镜像、起止时间（UTC）与退出码，
是复核「同一被测版本」的依据。完整 k6 控制台日志体积大且是一次性产物，只保留在本机。

---

## 10. Git 信息

| 项 | 值 |
|---|---|
| 分支 | `feat/k8s-t5-02-load-performance-analysis` |
| 基线 | `feat/k8s-t5-01-k6-test-framework` @ `a069b9d9d8f0f24e74c1fe4be4d5723023a31505` |
| 实现提交 | `f557fe4a1f0b5721fe8e225bb5672dded6a89c7e` |
| 远端分支 | `origin/feat/k8s-t5-02-load-performance-analysis`（与本地一致） |
| upstream | 已设置（`git push -u`） |
| 工作区 | clean |
| force push / 合并 main / 删除历史分支 | 均未执行 |

10.1 变更清单：

```text
修改：test/k6/lib/stages.js        LOAD_VUS 覆盖语义修正（ramp 直达峰值，稳态才成立）
修改：test/k6/scenarios/read-load.js  P99 + 按接口 Trend + 目标接口日志/告警
修改：test/k6/README.md            实验 A 定稿参数与状态更新
新增：.agent/tasks/dev/T5-02-load-performance-analysis.md
新增：docs/reports/t5-02-load-performance-analysis/（报告 + raw/ 6 个证据文件）
```

10.2 提交与推送核验：

| 项 | 值 |
|---|---|
| 分支 tip（实现提交） | `f557fe4a1f0b5721fe8e225bb5672dded6a89c7e` |
| 远端 SHA | 与本地一致（`git ls-remote origin refs/heads/feat/k8s-t5-02-load-performance-analysis`） |
| 差异基线 | `a069b9d` → `f557fe4`：11 个文件、+1634/−13 |
| 业务代码改动 | 无（仅 `test/k6/**`、`.agent/tasks/dev/**`、`docs/reports/t5-02-*/**`） |

本节由后续「报告回填」提交补齐（与 T4-03/T4-04/T5-01 一致做法），回填只改元数据，
不改任何实现或结论。
