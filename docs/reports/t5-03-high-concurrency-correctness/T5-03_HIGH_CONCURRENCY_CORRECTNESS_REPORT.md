# T5-03 高并发秒杀业务正确性验证 — 交付报告

- 项目：SurgeCart
- 任务：T5-03（任务五第 3 子任务）
- 分支：`feat/k8s-t5-03-concurrency-correctness`
- 基线：`feat/k8s-t5-02-load-performance-analysis` @ `ce8d86c8ee0958962cc656d15160f34b10a25ba1`
- 实验时间：2026-11-10 08:25–08:29 UTC（16:25–16:29 Asia/Shanghai）
- 集群：本地 kind 单节点（`kind-surgecart-local`）

> **结论摘要**：在 `kafka` 消费模式下，用真实并发请求 + 真实 MySQL 核验，验证了三条不变量
> **不超卖、一人一单、请求幂等**均成立；异步请求全部收敛到终态；Kafka Lag 归零、Outbox 无积压。
> `db_scanner` 模式 **NOT_VERIFIED**（原因见 §11）。
> 过程中发现一个**真实的时区缺陷**：用 UTC 时间创建的活动会被系统少算 8 小时，
> 导致活动立即被判定为已结束（详见 §5）。未修改任何业务代码。

---

## 1. 实验环境

| 项 | 值 |
|---|---|
| 集群 | kind `surgecart-local-control-plane`，单节点，Kubernetes v1.37.0，containerd 2.3.4 |
| 应用 | `surgecart-7d4d84b88-xn4tb`，1/1 Running，0 重启，镜像 `surgecart:64ebbbd-t4-03` |
| 应用资源 | requests 100m/256Mi，limits **500m/512Mi** |
| MySQL | `mysql:8.0`，库 `my_shop`，容器时区 UTC，`@@session.time_zone=SYSTEM` |
| Redis | `redis:7.4.12`，db0，与应用同命名空间 |
| Kafka | `apache/kafka:3.9.1`（KRaft 单节点），Topic `surgecart.flashsale.order.requested.v1` |
| 消费模式 | `FLASH_SALE_DISPATCH_MODE=kafka`，`FLASH_SALE_KAFKA_ENABLED=true` |
| 消费组 | `surgecart-flashsale-order-worker-v1`，单成员，实验后 `CURRENT-OFFSET=34 / LOG-END=34 / LAG=0` |
| 指标 | `FLASH_SALE_METRICS_ENABLED=true`，Prometheus `job=surgecart`（scrape 15s） |
| k6 | `grafana/k6:0.54.0`，经 `kubectl port-forward 18000:8000` 访问应用 |
| 业务时区 | `dashboard.timezone=Asia/Shanghai`（**同时决定 MySQL 会话时区**，只支持无 DST 时区） |

宿主机：ASUS TUF F15 / i7-12700H（14C20T）/ 31.6 GiB；Docker Desktop 分配 20 CPU / 15.8 GiB。

---

## 2. 实验数据准备

### 2.1 隔离原则

- 三个实验各用**全新活动 + 全新 SKU + 全新商品**，不复用 T3-02 / T5-02 的任何活动；
- 测试用户名带 `DATA_PREFIX=k6t503` + run 短哈希，与历史用户不重名；
- 不修改已有商品库存、不删除任何订单、不清库、不动 Redis 数据库级状态、不动 Kafka Topic；
- 通过管理员 API 创建（`POST /admin/products|skus|flash-sales`），不直接写库。

### 2.2 数据清单（实测）

| 实验 | 活动 ID | SKU | 商品 | 初始库存 | 活动时间窗（+08 墙钟） | run ID |
|---|---:|---:|---:|---:|---|---|
| A 库存并发竞争 | 13 | 11 | 11 | 20 | 16:24:02 → 18:25:02 | `t503-a-c1` |
| B 一人一单并发 | 14 | 12 | 12 | 5 | 16:24:06 → 18:25:06 | `t503-b-c1` |
| C 重复请求幂等 | 15 | 13 | 13 | 5 | 16:24:10 → 18:25:10 | `t503-c-c1` |

测试用户：
- A：40 个独立用户（`k6t503<runHash>u1..u40`），一次性注册+登录，密码来自环境变量（不入库）；
- B、C：各 1 个独立用户（`FLASH_SHARED_USER=true`，10 个 VU 共用同一账号）。

初始订单数：三个活动均为 0（创建后未做过任何下单）。

### 2.3 活动窗口的时区核验（本任务最容易翻车的地方）

核验 SQL（只读，按应用会话时区 `+08:00` 解释 DATETIME）：

```sql
SELECT a.id, a.start_time, a.end_time,
       DATE_ADD(UTC_TIMESTAMP(), INTERVAL 8 HOUR) AS now_sh,
       UNIX_TIMESTAMP(a.end_time) - 28800 AS end_epoch_app,
       UNIX_TIMESTAMP(UTC_TIMESTAMP()) AS now_epoch,
       (a.start_time <= DATE_ADD(UTC_TIMESTAMP(), INTERVAL 8 HOUR)
        AND DATE_ADD(UTC_TIMESTAMP(), INTERVAL 8 HOUR) < a.end_time) AS in_window,
       b.total_stock, b.sold
FROM flash_sale_activities a JOIN flash_sale_activity_skus b ON b.activity_id = a.id
WHERE a.id IN (13,14,15) ORDER BY a.id;
```

实测输出：

```text
13  2026-11-10 16:24:02  2026-11-10 18:25:02  2026-11-10 16:25:35  1794306302  1794299135  1  20  0
14  2026-11-10 16:24:06  2026-11-10 18:25:06  2026-11-10 16:25:35  1794306306  1794299135  1  5   0
15  2026-11-10 16:24:10  2026-11-10 18:25:10  2026-11-10 16:25:35  1794306310  1794299135  1  5   0
```

`in_window=1` 三条全部成立，且 Redis 中原生活动 key 已由应用自行预热：

```text
flashsale:activity:13 / :14 / :15   (HSET status=1 start/end)
```

> 为什么特意做这一步：**第一次尝试用 UTC(RFC3339 `Z`) 创建活动，40 个请求全部返回
> `12001 活动不存在`**。原因见 §5.6，属于本任务发现的真实缺陷，不是测试脚本问题。

---

## 3. 实验设计

三个实验共用同一套"once 形态"脚本（`FLASH_EXECUTOR=once`，`per-vu-iterations`）：

- 请求总数 = VU 数 × 迭代数，**完全确定**，不随时长漂移（负载形态 `stages` 不适用正确性验证）；
- 每个 VU 只发 1 个请求，提交后立即轮询结果接口直到终态（`FLASH_VERIFY=true`）；
- 逐条输出 `FLASH_TRACE`（VU/用户/幂等键/HTTP 状态/业务码/分类/返回状态），作为逐请求证据。

| 实验 | VU | 账号 | 幂等键 | 业务预期 |
|---|---:|---|---|---|
| A 库存 | 40 | 40 个独立用户 | 每用户唯一 | 20 成功，20 因库存不足被拒 |
| B 一人一单 | 10 | 1 个共享用户 | 每 VU 唯一 | 最多 1 单，其余被一人一单拦截 |
| C 幂等 | 10 | 1 个共享用户 | **全部同一个字面量键** | 只产生 1 条请求 + 1 单，其余返回既有状态 |

必须区分的两类语义（本任务重点）：

| 概念 | 保证机制 | 作用域 |
|---|---|---|
| **一人一单** | DB 唯一索引 `uk_flash_one_per_user(activity_id, sku_id, user_id)` + Redis `bought` 标记（快路径） | 同活动同 SKU 同用户 |
| **请求幂等** | 幂等键 + 请求指纹 `sha256(activity_id\|sku_id)`；DB 唯一索引 `uk_request_idempotency(user_id, idempotency_key)`；Redis `idem` 标记 | 同用户的同一逻辑请求 |

两者不是一回事：B 用不同幂等键模拟"同一用户重复抢"，C 用相同幂等键模拟"同一请求重发"。

---

## 4. 执行过程

```powershell
# 0) 环境/数据：见 §2（prepare-data + 时区核验）
# 1) A：40 用户各抢 1 次，库存 20
$env:ACTIVITY_ID='13'; $env:SKU_ID='11'; $env:FLASH_USERS='40'; $env:FLASH_VUS='40'
$env:FLASH_EXECUTOR='once'; $env:FLASH_ITERATIONS='1'; $env:FLASH_VERIFY='true'; $env:FLASH_TRACE='true'
test/k6/scripts/run.ps1 -Scenario flashsale-order -RunId t503-a-c1 -BaseUrl http://host.docker.internal:18000

# 2) B：同一用户 10 并发，库存 5
$env:ACTIVITY_ID='14'; $env:SKU_ID='12'; $env:FLASH_USERS='1'; $env:FLASH_VUS='10'; $env:FLASH_SHARED_USER='true'
$env:FLASH_FIXED_KEY=''; ... -RunId t503-b-c1

# 3) C：同一用户 + 同一幂等键 10 并发
$env:ACTIVITY_ID='15'; $env:SKU_ID='13'; $env:FLASH_FIXED_KEY='t503-c-fixed-key-1'
... -RunId t503-c-c1
```

每次实验独立 run ID，结果位于 `test/k6/results/<run_id>.{summary.json,meta.json,k6.log}`。

---

## 5. k6 运行结果与业务响应分布

### 5.1 客户端汇总

| 指标 | A（40 VU/40 用户） | B（10 VU/1 用户） | C（10 VU/1 用户同键） |
|---|---:|---:|---:|
| 迭代数（=下单请求数） | 40 | 10 | 10 |
| HTTP 请求总数（含 setup 与轮询） | 148 | 22 | 22 |
| 运行时长 | 10.1 s（含 40 用户注册登录） | 0.79 s | 0.83 s |
| `http_req_duration` P95 | 133.9 ms | 103.4 ms | 121.8 ms |
| 受理→终态 P95（`k6_flash_order_final_state_ms`） | 1150 ms | 553 ms | 568 ms |
| `k6_request_errors` | 0 | 0 | 0 |
| `checks` | 268/268 通过 | 53/53 | 62/62 |

### 5.2 下单响应分布（`FLASH_TRACE` 逐条统计）

| 实验 | HTTP 200 / `accepted` | HTTP 409 / `stock_insufficient` | 其他 |
|---|---:|---:|---:|
| A | 20 | 20 | 0 |
| B | 10 | 0 | 0 |
| C | 10 | 0 | 0 |

- A：20 个请求拿到"已受理"，另 20 个被闸门以库存不足拒绝（**HTTP 409 + 业务码 12003**），
  拒绝发生在入队之前，因此**这 20 个请求没有产生任何请求行/订单**。
- B：10 个请求全部返回"已受理"（**不是** 409）。原因见 §9.2——一人一单命中时系统会经
  MySQL 校验判定"标记是否为崩溃窗口孤儿"，并发下这些请求被判定为可自愈并重新入队，
  最终由数据库唯一约束在消费阶段拒绝（9 条 failed，业务码 **12004**）。
- C：10 个请求全部返回 `200 + status=queued`，其中 1 个是首次受理，9 个是**幂等命中**
  返回既有请求的当前状态（受理瞬间还没落单，所以也是 queued）。

### 5.3 服务端指标交叉验证（`/metrics` 累计值，与客户端逐一吻合）

| 指标序列 | 实测值 | 对应客户端事实 |
|---|---:|---|
| `surgecart_http_requests_total{route="/flash-sales/:id/orders",status="200"}` | 40 | A20 + B10 + C10 |
| `...{route="/flash-sales/:id/orders",status="409"}` | 20 | A 的库存不足 |
| `...{route="/flash-sales/:id/orders",status="404"}` | 41 | 时区缺陷导致的 40 次 12001 + 1 次探针 |
| `flashsale_requests_total{interface="order",result="queued"}` | 40 | 受理总数 20+10+10 |
| `flashsale_requests_total{interface="order",result="gate_rejected"}` | 61 | 20(409) + 41(404) |
| `flashsale_requests_total{interface="consume",result="success"}` | 22 | A20 + B1 + C1 |
| `flashsale_requests_total{interface="consume",result="failed"}` | 9 | B 的 9 条一人一单重复 |

### 5.4 异步终态（等待消费完成后再核验）

所有实验在提交后轮询结果接口到终态；实验结束再以 MySQL 复核，**三个活动均无 queued 残留**：

| 活动 | queued | success | failed | dead |
|---|---:|---:|---:|---:|
| 13（A） | 0 | 20 | 0 | 0 |
| 14（B） | 0 | 1 | 9 | 0 |
| 15（C） | 0 | 1 | 0 | 0 |

---

## 6. MySQL 权威核验

> MySQL 是订单、库存、请求状态的权威事实源；Redis 只是派生缓存与闸门。
> 会话由 `kubectl exec` 进入 MySQL Pod 执行，密码取自容器内环境变量，**未出现在任何命令或文档中**。

### 6.1 库存不变量

```sql
SELECT activity_id, total_stock, sold, total_stock - sold AS remaining
FROM flash_sale_activity_skus WHERE activity_id IN (13,14,15) ORDER BY activity_id;
```

```text
13  20  20  0
14  5   1   4
15  5   1   4
```

- `sold <= total_stock` 恒成立（20≤20、1≤5、1≤5）；
- `remaining >= 0` 恒成立（0、4、4），**无负库存**；
- A 场景 40 个并发请求、库存 20 → 恰好成功 20 单，**未超卖**。

Redis 侧同口径核对：`flashsale:stock:13:11 = 0`、`flashsale:stock:14:12 = 4`、`flashsale:stock:15:13 = 4`，
与 MySQL `remaining` 一致。

### 6.2 订单与"一人一单"

```sql
SELECT activity_id, COUNT(*) AS orders, COUNT(DISTINCT user_id) AS users
FROM flash_sale_orders WHERE activity_id IN (13,14,15) GROUP BY activity_id ORDER BY activity_id;

SELECT COUNT(*) AS dup_one_per_user FROM (
  SELECT activity_id, sku_id, user_id FROM flash_sale_orders
  WHERE activity_id IN (13,14,15)
  GROUP BY activity_id, sku_id, user_id HAVING COUNT(*) > 1
) t;
```

```text
13  20  20     -- 20 单，20 个不同用户
14  1   1      -- 同 1 个用户只有 1 单
15  1   1
0              -- 一人多单违例数：0
```

- A：`orders = 20 = 成功用户数`，说明每个用户最多一单且总单数等于库存；
- B：同一用户在 10 个并发请求下**只有 1 笔成功订单**，`sold` 只 +1；
- 唯一索引 `uk_flash_one_per_user` 是最终兜底，`dup_one_per_user = 0` 直接证明未被突破。

### 6.3 请求幂等

```sql
SELECT COUNT(*) AS dup_idem FROM (
  SELECT user_id, idempotency_key FROM flash_sale_order_requests
  WHERE activity_id IN (13,14,15)
  GROUP BY user_id, idempotency_key HAVING COUNT(*) > 1
) t;

SELECT activity_id, COUNT(*) AS reqs, COUNT(DISTINCT idempotency_key) AS distinct_keys,
       SUM(status=1) AS success, SUM(status<>1) AS not_success
FROM flash_sale_order_requests WHERE activity_id IN (13,14,15) GROUP BY activity_id;
```

```text
dup_idem = 0

13  20  20  20  0
14  10  10  1   9
15  1   1   1   0     -- 10 个相同幂等键的请求，只落 1 条请求行
```

- C：10 个**完全相同**的请求（同用户、同活动、同 SKU、同幂等键）只产生 **1 条请求行 + 1 笔订单**，
  `sold` 只 +1，`remaining` 保持 4；
- `dup_idem = 0` 说明 `uk_request_idempotency(user_id, idempotency_key)` 未被突破；
- 重放请求返回的是既有请求状态（`200 + queued/success`），符合源码契约
  （`handleIdempotentHit`，同键同指纹 → 返回既有状态；同键不同指纹 → 12005 冲突，本次未构造）。

### 6.4 幂等键冲突（对照检查，未触发）

C 场景所有请求都使用同一 `(activity, sku)`，因此请求指纹相同 → 走幂等命中而不是冲突。
`12005` 冲突路径本次**未覆盖**（需要"同键不同内容"，不属于本次三个核心场景），标记为未验证。

### 6.5 Kafka / Outbox 状态

```sql
SELECT activity_id, COUNT(*) AS unpublished FROM flash_sale_order_requests
WHERE activity_id IN (13,14,15) AND published_at IS NULL GROUP BY activity_id;
```

```text
(空结果集)   -- 三个活动的请求行全部已被 Broker 确认发布
```

消费组状态：

```text
GROUP surgecart-flashsale-order-worker-v1
TOPIC surgecart.flashsale.order.requested.v1  PARTITION 0
CURRENT-OFFSET 34  LOG-END-OFFSET 34  LAG 0
```

34 = 本次 31 条（20+10+1）+ 活动 7 探针 1 条 + T3-02 历史 2 条，与库内请求行数一致。

---

## 7. Prometheus 辅助观察

时间窗口取自每次运行的 `meta.json`（UTC），全部查询在窗口结束 +60s 执行，
确保覆盖最后一个 15s 抓取周期。

| 指标 | A | B | C | 说明 |
|---|---:|---:|---:|---|
| `surgecart_kafka_consumer_lag` 最大 | 0 | 0 | 0 | 消费完全跟上，无积压 |
| `flashsale_outbox_backlog_pending` 最大 | 0 | 0 | 0 | Outbox 无待发布堆积 |
| `surgecart_outbox_oldest_age_seconds` 最大 | 0 | 0 | 0 | 最老待发布项年龄 0s |
| `surgecart_sampler_up` 最小 | 1 / 1 | 1 / 1 | 1 / 1 | 两个采样器全程健康 |
| `surgecart_http_requests_in_flight` 最大 | 0 | 0 | 0 | 请求极短，抓取周期内未命中 |

PromQL（示例）：

```promql
max_over_time(surgecart_kafka_consumer_lag{job="surgecart"}[5m])
max_over_time(flashsale_outbox_backlog_pending[5m])
min_over_time(surgecart_sampler_up{job="surgecart"}[5m])
sum by (interface,result) (increase(flashsale_requests_total[5m]))
increase(surgecart_http_requests_total{job="surgecart",route="/flash-sales/:id/orders"}[5m])
```

说明：每次实验只有 10–40 个请求、持续 1–13 秒，**小于一个抓取周期**，
因此 in-flight / lag 这类瞬时量在 15s 采样下大多只能读到 0。
这不是"没有问题"，而是**采样分辨率不足以观测短脉冲**——真正能对上的是累计计数器（§5.3），
两者结论一致。

---

## 8. 正确性结论（逐条对应验收项）

| 不变量 | 判定 | 证据 |
|---|---|---|
| 不超卖：成功订单 ≤ 初始库存 | **PASS** | A：40 请求 / 库存 20 → 20 单，`sold=20` |
| 已售数量 ≤ 初始库存 | **PASS** | 20≤20、1≤5、1≤5 |
| 剩余库存不为负 | **PASS** | 0、4、4；DB 列本身为 `INT UNSIGNED` |
| 一人一单（同活动同 SKU 同用户 ≤1 单） | **PASS** | B：10 并发 → 1 单；`dup_one_per_user=0` |
| 请求幂等（同键重放不产生额外订单/扣减） | **PASS** | C：10 同键并发 → 1 请求行 + 1 单，`sold` +1 |
| 异步请求全部进入明确终态 | **PASS** | 三个活动 queued 残留 = 0 |
| 失败请求符合业务码语义 | **PASS** | A 的 20 条 = 12003；B 的 9 条 = 12004 |
| Outbox / Kafka 消费语义一致 | **PASS** | `published_at IS NULL` 为空；消费组 LAG=0 |
| Redis 与 MySQL 库存一致 | **PASS** | 0/4/4 完全一致 |

**总判定：三条核心不变量在 `kafka` 模式下全部成立（PASS）。**

---

## 9. 实验中发现的问题

### 9.1 时区缺陷（真实缺陷，未修复，已定位）

**现象**：按 UTC(RFC3339 `Z`) 创建 2 小时窗口的活动，40 个下单请求全部返回
`HTTP 404 / 12001 活动不存在`。

**证据链**：
1. MySQL 中活动存在且 `status=1`，`UNIX_TIMESTAMP(end_time) > NOW()`；
2. Redis `MONITOR` 显示应用对新建活动立即执行
   `Del flashsale:activity:N / stock / soldout` + `SetEX flashsale:null:N 1 60`
   —— 即 `invalidateActivityCache`，说明应用判定"已下架或已结束"；
3. Redis 中始终没有 `flashsale:activity:N` hash；
4. 换成 12 小时窗口后，同一路径写出 `HSet flashsale:activity:12 status=1 start=1794270181 end=1794313441`，
   而真实结束时刻应为 `1794342241` —— **恰好少 28800 秒（8 小时）**。

**根因**：应用把 MySQL 会话时区设为业务时区 `dashboard.timezone=Asia/Shanghai(+08:00)`，
而写库时 DATETIME 由驱动按 UTC 格式化成字面量；于是"按 UTC 墙钟写入"的时间被当成 +08:00
解读，`UNIX_TIMESTAMP()` 少 8 小时。对 2 小时窗口的活动而言，它在闸门看来**已经结束 8 小时**，
预热直接走失效分支，所有下单返回 12001。

**影响**：任何用 UTC 时间（`...Z`）通过后台接口创建的活动都无法下单；
若运营用本地时间（+08:00 墙钟）创建则正常。这与 T3-02 记录的"UTC 与 +08:00 会话时区不一致"
是同一类问题，**在当前部署上仍然存在**。

**本次处置**：不修改业务代码（任务禁止）。测试数据改为按 Asia/Shanghai 墙钟提交
（`prepare-data.js` 新增 `FLASH_TZ_OFFSET_SECONDS`，默认 28800），并在报告中保留完整证据。
建议后续以独立任务修复（例如统一用 `CONVERT_TZ` 或在 DSN 显式固定 `loc`/`time_zone`）。

### 9.2 一人一单并发路径的"自愈"放大了受理量（观察项，非正确性缺陷）

B 场景 10 个并发请求全部返回 `200 + queued`（预期是 9 个 `409/12004`）。
源码原因：闸门命中"已购"后，`handleAlreadyPurchased` 会查 MySQL 区分"真已购"与
"崩溃窗口孤儿标记"；首次请求此时仍处于 `queued`（尚未落单），于是被判为孤儿标记并自愈放行。

**结果**：最终仍然只有 1 单（DB 唯一约束兜底），库存只扣 1，9 条请求在消费阶段以 12004 失败。
**代价**：多 9 条入队与 9 次消费，用户侧看到"已受理"后失败。
**判定**：不变量成立；属于"自愈语义在并发下的误判"，建议后续评估（例如把
`queued` 视为"真已购"而不是孤儿，仅在标记存在但请求行不存在时自愈）。

### 9.3 工程问题（已在框架内修复）

| 问题 | 后果 | 修复 |
|---|---|---|
| `import { execution } from 'k6/execution'` | 运行期 `Cannot read property 'vu'`，40 个 VU 全部失败 | 改为默认导入 `import exec from 'k6/execution'` |
| 新开关未加入 `run.ps1` 白名单 | `FLASH_EXECUTOR=once` 被丢弃，实验静默退化为 165s 持续压测 | 补齐白名单并加注释 |
| 脱敏正则只允许字母 | `K6_TEST_PASSWORD` 漏出明文（键名含数字） | 正则改为 `[A-Za-z0-9_]*` |
| `shortRunId` 哈希溢出 | 不同 run 得到相同用户名（用户隔离失效） | 改为模 36^6 的多项式哈希 |
| `K6_DATA_MARKER` 解析假设行首 | 数据 ID 未落盘 | 按 k6 结构化日志格式提取并还原转义 |
| `envInt` 不接受负数 | 无法调整时间窗偏移 | 新增本地 `envSignedInt` |

---

## 10. 验收结果

| 验收项 | 结论 |
|---|---|
| 1. Git 基线正确 | ✅ `ce8d86c` → `feat/k8s-t5-03-concurrency-correctness` |
| 2. 新分支建立 | ✅ |
| 3. 真实 Kubernetes 集群 | ✅ kind 单节点，真实 MySQL/Redis/Kafka |
| 4. 测试数据隔离 | ✅ 独立活动 13/14/15 + 独立商品/SKU/用户 |
| 5. 库存并发竞争实验 | ✅ A：40 用户抢 20 件 |
| 6. 一人一单并发实验 | ✅ B：1 用户 10 并发 |
| 7. 重复请求幂等实验 | ✅ C：同键 10 并发 |
| 8. 区分 queued/success/failed/dead | ✅ 逐条 trace + DB 状态分组 |
| 9. 等待异步终态 | ✅ queued 残留 0 |
| 10. 真实 MySQL 核验 | ✅ §6 全部 SQL 与输出 |
| 11. 不超卖 | ✅ PASS |
| 12. 一人一单 | ✅ PASS |
| 13. 请求幂等 | ✅ PASS |
| 14. Prometheus 关联 | ✅ §7（累计计数器可对上，瞬时量受采样限制） |
| 15. 开发知识文档 | ✅ `.agent/tasks/dev/T5-03-high-concurrency-correctness.md` |
| 16. 正式报告 | ✅ 本文件 |
| 17. 原始数据可追溯 | ✅ `test/k6/results/` + 本目录 `raw/` |
| 18. 门禁 | ✅ `k6 inspect` 通过；实验后应用 0 重启、Target UP |
| 19. Git 推送 | 见 §12（推送后回填） |
| 20. 工作区干净 | 见 §12 |

---

## 11. 未验证项（NOT_VERIFIED）

| 项 | 状态 | 原因 |
|---|---|---|
| `db_scanner` 模式的正确性 | **NOT_VERIFIED** | 切换需改 ConfigMap `FLASH_SALE_DISPATCH_MODE` 并重启应用，属于修改 Kubernetes 配置（任务 §12 明确禁止）；且需重建三套数据并重跑，风险与范围都超出本任务授权。结论只适用于 `kafka` 模式。 |
| 幂等键冲突（12005） | NOT_VERIFIED | 需要"同键不同内容"，不属于本次三个核心场景 |
| DLQ / 重试预算耗尽（dead） | NOT_VERIFIED | 属 T5-04 故障恢复范围 |
| Kafka 积压与恢复 | NOT_VERIFIED（未执行） | 属 T5-04 |
| 多副本并发 | NOT_VERIFIED | 当前单副本 |
| 生产环境容量/正确性 | NOT_VERIFIED | 单节点本地集群 |

---

## 12. Git 信息

| 项 | 值 |
|---|---|
| 分支 | `feat/k8s-t5-03-concurrency-correctness` |
| 基线 | `ce8d86c8ee0958962cc656d15160f34b10a25ba1` |
| 实现提交 | `0127614fd4d651e211eee9db6ec9074292f01c03` |
| 远端分支 | `origin/feat/k8s-t5-03-concurrency-correctness`（与本地一致） |
| upstream | 已设置（`git push -u`） |
| 工作区 | clean |
| force push / 合并 main / 删除历史分支 | 均未执行 |

12.1 变更清单：

```text
修改：test/k6/scenarios/flashsale-order.js   正确性形态（once/shared_user/fixed_key/vus/trace）+ k6/execution 导入修复
修改：test/k6/tools/prepare-data.js          时间窗按业务时区墙钟提交 + 可调偏移（修复时区缺陷的测试侧规避）
修改：test/k6/config/default.js              shortRunId 哈希精度修复（用户隔离）
修改：test/k6/scripts/run.ps1                白名单补齐、凭据脱敏修正、K6_DATA_MARKER 解析修复
新增：.agent/tasks/dev/T5-03-high-concurrency-correctness.md
新增：docs/reports/t5-03-high-concurrency-correctness/（本报告 + raw/ 证据）
```

12.2 提交与推送核验：

| 项 | 值 |
|---|---|
| 分支 tip（实现提交） | `0127614fd4d651e211eee9db6ec9074292f01c03` |
| 远端 SHA | 与本地一致（`git ls-remote` 核验） |
| 差异基线 | `ce8d86c` → `0127614`：16 个文件、+1957/−29 |
| 业务代码改动 | 无（仅 `test/k6/**`、`.agent/tasks/dev/**`、`docs/reports/t5-03-*/**`） |
| 入库后 `run.ps1` BOM | 239,187,191（UTF-8 BOM 保留，PowerShell 5.1 可正常解析） |

本节由后续"报告回填"提交补齐（与 T4-03/T4-04/T5-01/T5-02 一致做法），回填只改元数据，
不改任何实现或结论。

---

## 13. 原始数据位置

本机（`.gitignore` 排除）：

```text
test/k6/results/t503-a-c1.summary.json / .meta.json / .k6.log
test/k6/results/t503-b-c1.summary.json / .meta.json / .k6.log
test/k6/results/t503-c-c1.summary.json / .meta.json / .k6.log
test/k6/data/t503-a2-data.data.json / t503-b2-data.data.json / t503-c2-data.data.json
```

入库归档：

```text
docs/reports/t5-03-high-concurrency-correctness/raw/
├── t503-a-c1.summary.json / .meta.json
├── t503-b-c1.summary.json / .meta.json
├── t503-c-c1.summary.json / .meta.json
├── data-a.json / data-b.json / data-c.json        （被测活动与 SKU 清单）
└── business-verification.md                       （SQL 原文与实测输出）
```
