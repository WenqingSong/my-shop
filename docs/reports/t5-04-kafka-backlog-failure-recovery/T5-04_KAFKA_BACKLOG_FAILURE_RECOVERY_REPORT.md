# T5-04 Kafka 积压与故障恢复实验 — 交付报告

- 项目：SurgeCart
- 任务：T5-04（任务五第 4 子任务）
- 分支：`feat/k8s-t5-04-kafka-backlog-failure-recovery`
- 基线：`feat/k8s-t5-03-concurrency-correctness` @ `be7fbe429791577679e5e1dcb12e4fe44f6f17b7`
- 实验时间：2026-11-10 17:28–19:45 Asia/Shanghai（09:28–11:45 UTC）
- 集群：本地 kind 单节点（`kind-surgecart-local`）
- 原始证据：`docs/reports/t5-04-kafka-backlog-failure-recovery/raw/`

> **结论摘要**
>
> 在**真实 kind 集群 + 真实 MySQL/Redis/Kafka** 上完成了 4 组故障注入：
> ① 消费端暂停 → 积压 20 条、恢复后 Lag 归零；② Broker 停机 59s → 10 条事件保留在
> Outbox、恢复后自动重发并消费，**零丢失**；③ 应用 Pod `SIGKILL` → 10 条未提交消息
> 跨崩溃重投、订单不重不漏；④ 4 类永久错误全部进 DLQ（`dlq_source_offset` 与投递偏移
> 完全对应），重试预算耗尽走「先死信、后 dead 终态」。
>
> 最终一致性：Topic `LOG-END=136 / COMMITTED=136 / LAG=0`，全库 `queued=0`、
> `orders` 无重复 `order_no`、无「同活动同用户多单」、无 `sold > total_stock`。
>
> **发现 1 个真实的监控缺陷（未修复，按任务要求只报告）**：
> `surgecart_outbox_oldest_age_seconds` 在亚洲/上海时区下**恒定多报 8 小时**
> （实测 103s 的真实年龄被报成 28903.5s），根因是 `NOW(6)`（会话时区 +08:00）与
> `created_at`（驱动写入的 UTC 墙钟）混用。见 §10.1。
>
> **NOT_VERIFIED**：`MySQL 事务已提交、Kafka 位移尚未提交` 的毫秒级窗口无法在真实集群里
> 精确命中；本任务只证明了该窗口两侧（未提交消息不丢、重复投递被幂等闸门挡住），
> 不声称"实测命中窗口"。

---

## 1. 实验环境

| 项 | 值 |
|---|---|
| 集群 | kind `surgecart-local-control-plane`，单节点，Kubernetes v1.37.0，containerd 2.3.4 |
| 应用 | Deployment `surgecart`，1/1 Running，镜像 `surgecart:64ebbbd-t4-03` |
| 应用资源 | requests 100m/256Mi，limits 500m/512Mi |
| MySQL | `mysql:8.0`，库 `my_shop`，PVC `mysql-data`(2Gi) Bound |
| Redis | `redis:7-alpine`，PVC `redis-data`(1Gi) Bound |
| Kafka | `apache/kafka:3.9.1`（KRaft 单节点）**Deployment**（非 StatefulSet），PVC `kafka-data`(2Gi) Bound |
| Topic | `surgecart.flashsale.order.requested.v1`，**PartitionCount=1 / RF=1 / min.insync.replicas=1** |
| DLQ Topic | `surgecart.flashsale.order.requested.v1.dlq` |
| 消费组 | `surgecart-flashsale-order-worker-v1` |
| 消费模式 | `FLASH_SALE_DISPATCH_MODE=kafka`（`db_scanner` 未参与本任务） |
| 指标 | `FLASH_SALE_METRICS_ENABLED=true`，Prometheus `job=surgecart`（scrape 15s，2 个 target 全 up） |
| k6 | `grafana/k6:0.54.0`，经 `kubectl port-forward svc/surgecart 18000:8000` 压真实 HTTP 入口 |
| 业务时区 | `dashboard.timezone=Asia/Shanghai`（同时决定 MySQL 会话时区 `+08:00`） |

宿主机：ASUS TUF F15 / i7-12700H / 31.6 GiB；Docker Desktop 分配 20 CPU / 15.8 GiB。

---

## 2. 实验架构（源码事实，非推测）

### 2.1 Producer 与 Consumer 在同一个进程里

`internal/cmd/cmd.go` → `worker.StartFlashSaleRuntime()`（`internal/worker/flashsale_runtime.go`）
在 `dispatch_mode=kafka` 时按顺序创建并启动：

| 组件 | 位置 | 线程模型 |
|---|---|---|
| HTTP 服务（业务入口） | 同 Pod、同进程 | 主服务 |
| Kafka Producer | `internal/mq/kafka/producer.go`，client.id=`surgecart-flashsale-producer` | 同步 `acks=all` + 幂等写入 |
| Outbox Publisher | `internal/logic/flashsale/outbox.go`，goroutine `runOutboxPublisher` | 每 200ms 一轮，批量 100 |
| Kafka Consumer | `internal/mq/kafka/consumer.go` + `internal/logic/flashsale/kafka_consumer.go`，client.id=`surgecart-flashsale-consumer` | `BlockRebalanceOnPoll` + 手动提交 |
| Outbox 积压采样 | `runOutboxBacklogSampler` | 5s 周期 |
| Kafka Lag 采样 | `runKafkaLagSampler`（**独立 Kafka 客户端**，不入组） | 10s 周期（单次预算 5s） |

**这决定了本任务的实验设计边界**：不存在独立的 Consumer Deployment，也没有"只关消费者"的开关。
`kubectl scale deploy/surgecart --replicas=0` 会连 HTTP 入口一起干掉，
网络策略阻断 Kafka 会连生产者一起阻断。因此实验 A 只能从 **Broker 侧客户端配额** 入手。

### 2.2 消息链路与两条硬顺序

```text
HTTP 受理（Redis 预扣 + MySQL INSERT status=0 + published_at IS NULL）
   │
   ├─(1) Outbox Publisher：SKIP LOCKED 领取 → 写租约 → 事务提交 → Kafka Publish(acks=all)
   │        → 逐条标记 published_at / published_partition / published_offset
   │        → 失败则写 next_publish_at 指数退避（2s→5min），**status 保持 0**
   │
   └─(2) Consumer：Poll(1 条) → 解析校验 → ConsumeRequest（MySQL 事务，FOR UPDATE 锁行）
            → 状态闸门（只有 status=0 才执行业务）
            → 建单 + 条件扣库存（同一保存点）
            → **MySQL COMMIT**
            → **再提交 Kafka Offset（offset+1）**
            → AllowRebalance
```

两条顺序是本任务验证的核心：

1. **MySQL 提交必须先于 Kafka 位移提交**。反过来会丢单：位移推进了、事务回滚了，消息不再投递，
   而请求永远停在 `queued`。
2. **死信必须"先 Broker ACK、后 MySQL dead 终态、最后提交原位移"**。若先改终态再发死信，
   死信一旦失败，原始业务请求就彻底无人处理。

### 2.3 Consumer Group 与 Offset 机制

- `kgo.DisableAutoCommit()`：**禁止自动提交**，只在业务处理成功后显式 `CommitRecords`；
- `kgo.BlockRebalanceOnPoll()`：Poll 返回的批次处理完之前冻结再平衡，避免处理中的分区被抢走；
- 批次大小 `defaultConsumerBatchSize = 1`：逐条拉取、逐条提交，天然保证位移连续；
- `consumerSessionTimeout=30s` / `HeartbeatInterval=3s`：实例崩溃后约半分钟内触发 rebalance；
- 位移只在"本条消息已得出结论"后提交：业务成功/业务失败终态/重复跳过/死信已 ACK，四类都算结论；
  技术失败则**不提交**并停止本批，未提交的消息在重启后由 Broker 重投。

### 2.4 重试与死信（源码口径）

| 项 | 值 | 出处 |
|---|---|---|
| 单条消息本地兜底尝试 | 4 次（首次 + 3 重试），退避 200ms→1s | `defaultConsumerMaxDeliveryAttempts` / `RetryBackoffBase/Max` |
| 持久化重试预算 | `retry_count > 3` 即不再执行业务 | `maxConsumeRetryAttempts = 3`（`consume.go`） |
| 预算耗尽动作 | 死信 → MySQL `status=3(dead)` + Redis 补偿 | `deadLetter()` |
| 永久错误 | JSON 非法 / schema_version / event_type / 字段非法 / 请求不存在 / 身份不一致 → 直接死信 | `parseRequestedEvent()` / `settleResult()` |
| 死信头部 | `dlq_reason`、`dlq_error`、`dlq_source_topic/partition/offset`、`dlq_retry_count` | `publishDLQ()` |
| 死信发布 | 复用业务 Producer（acks=all），同步等 ACK | `flashsale_runtime.go` 装配注释 |
| **DLQ 是否存在** | **存在且可用**（非 NOT_IMPLEMENTED） | 见 §7 |

### 2.5 观测指标（本次实际用到的）

| 指标 | 周期/来源 | 说明 |
|---|---|---|
| `surgecart_kafka_consumer_lag{consumer_group}` | 10s 采样 + 15s 抓取 | 独立客户端拉取 log-end 与已提交位移计算 |
| `flashsale_outbox_backlog_pending` | 5s 采样 | 直接查库 `status=0 AND published_at IS NULL` |
| `surgecart_outbox_oldest_age_seconds` | 5s 采样（同一次 Backlog） | **本任务发现其有 8 小时偏差，见 §10.1** |
| `surgecart_sampler_up{sampler}` | 采样是否成功 | 失败保留上一次有效值，不伪造 0 |
| `flashsale_consumer_processed_total{result}` | 每条消息 | success/failed/dead/duplicate/invalid/error |
| `flashsale_consumer_dlq_total{reason}` | 每条死信 | 低基数原因标签 |
| `flashsale_consumer_commit_failures_total` | 每次提交失败 | 本任务全程 0 |
| `surgecart_http_requests_total` | 每请求 | 验证 HTTP 侧未因 Kafka 故障不可用 |

---

## 3. 故障注入方案

| 实验 | 注入手段 | 为什么这样注入 | 回滚方式 | 是否破坏业务入口 |
|---|---|---|---|---|
| A | Kafka **客户端配额** `consumer_byte_rate=1`（entity-type=clients，entity-name=`surgecart-flashsale-consumer`） | 生产/消费是两个 client.id；配额按 client.id 生效，是**唯一**能"只把消费拉取压到 0、生产者继续发布"的杠杆 | `--delete-config consumer_byte_rate` | 否 |
| B | `kubectl scale deploy/kafka --replicas=0` → 59s → `--replicas=1` | Kafka 是 Deployment 且数据在 PVC；scale 制造"Broker 进程不存在"，KRaft 元数据留在 PVC | scale 回 1，等 `kafka-get-offsets` 可响应 | 否（HTTP `/health` `/ready` 均 200） |
| C-crash | `kubectl delete pod --grace-period=0 --force`（SIGKILL，无优雅关闭） | 就是"进程被硬杀"，没有离组、没有 flush、没有提交位移 | ReplicaSet 自动重建 Pod | Pod 级中断，ReplicaSet 自愈 |
| C-dup | 从 Topic 精确读回历史事件，原样再投递 3 条 | 直接复现"at-least-once 重复投递"的后果 | 无状态操作 | 否 |
| D-1 | 手工投递 4 类不可处理消息（坏 JSON / schema_version=99 / 错 event_type / 不存在的 request_id） | 永久错误的确定性构造，不依赖中间件故障 | 无状态操作 | 否 |
| D-2 | 真实 HTTP 受理 1 条请求（Redis 已预扣、已发布、仍 queued），把该行 `retry_count` 置 4 | 见下方"边界说明" | 无状态操作 | 否 |

**D-2 的验证边界（必须如实标注）**：真实制造 4 次技术失败的最短路径是停 MySQL，
那会同时打挂 HTTP 入口与 Outbox，属于本任务之外的破坏性故障。因此 D-2 注入的是
**"已失败 4 次"这一持久化状态**，让消费端走同一条「重试预算闸门 → 死信 → dead 终态」代码路径。
验证的是"预算耗尽后的处置"，**不是**"retry_count 如何被推进"（后者由单元测试覆盖）。

**未使用的手段及原因**：
- `kubectl scale deploy/surgecart --replicas=0`：HTTP 一起消失，无法证明"业务请求仍被受理"；
- NetworkPolicy / iptables 阻断到 Kafka：生产者同样被阻断，会混淆 A 与 B；
- 删除 Kafka PVC / 重建集群 / `docker compose`：任务禁止，且会销毁 KRaft 元数据。

---

## 4. 实验 A：Consumer 积压与恢复（Broker 全程存活）

### 4.1 实验数据

| 项 | 值 |
|---|---|
| 活动 / SKU | activity=16 / sku=14（`t504ae-flash-hvva72`），初始库存 20 |
| 用户 | 20 个独立测试用户（`t504ae...u1..u20`） |
| 注入时刻 | 17:31 注入配额，17:34:50–17:35:17 观察，17:36 撤销配额（CST） |
| 注入方式 | `consumer_byte_rate=1`（1 字节/秒 ≈ 停止拉取） |

### 4.2 Lag 三方口径时间线（原始：`raw/a/a3-paused.lag-timeline.txt`）

```text
2026-11-10T09:34:50Z  committed=42  logEnd=62  broker_lag=20  prom_lag=0   backlog=0
2026-11-10T09:35:01Z  committed=42  logEnd=62  broker_lag=20  prom_lag=20  backlog=0
2026-11-10T09:35:17Z  committed=42  logEnd=62  broker_lag=20  prom_lag=20  backlog=0
```

- **Broker 侧事实**：`LOG-END=62`、`COMMITTED=42`、`LAG=20`，且 27 秒内完全不前进 —— 真积压；
- **Prometheus 侧**：第一行 `prom_lag=0` 是采样窗口内尚未更新（10s 采样 + 15s 抓取），
  第二行起与 Broker 一致 → 说明"积压可被监控观测"，但**存在采样延迟**；
- **Outbox backlog = 0**：因为 20 条事件已成功发布（生产端健康），积压发生在**消费端**，
  这两个指标是两个不同的故障面，不能互相替代。

### 4.3 恢复（原始：`raw/a/a4-recovery.lag-timeline.txt`）

```text
2026-11-10T09:36:02Z  committed=62  logEnd=62  broker_lag=0  prom_lag=20  backlog=0
```

撤销配额后：消费端重新加入消费组，一次性追平 20 条（`COMMITTED 42→62`），Lag 归零。
`prom_lag=20` 仍是上一次采样值，下一轮采样后才归零 —— 这就是"指标滞后于事实"的证据。

### 4.4 业务结果

| 时点 | requests | queued | success | orders | sold | remaining |
|---|---:|---:|---:|---:|---:|---:|
| 暂停期间 | 20 | **20** | 0 | 0 | 0 | 20 |
| 恢复后 | 20 | **0** | 20 | 20 | 20 | 0 |

结论：**消息积压不等于消息丢失**。积压期间 20 条请求全部停在 `queued`（HTTP 已受理、
未经业务处理），恢复后全部转化为成交，无重复订单（`orders_dup_per_user=0`、`orders_dup_no=0`）。

### 4.5 架构限制的如实记录

配额暂停时，消费端不仅拉不到数据，**还会因为心跳/组协调报文同样被限流而丢失组成员资格**：

```text
2026-11-10T10:11:34.512Z [WARN] {"event":"flashsale_consumer_group_lost","reason":"lost",...}
2026-11-10T10:11:34.512Z [WARN] {"error":"拉取 Kafka 消息: /0: unable to join group session:
                                   UNKNOWN_MEMBER_ID: The coordinator is not aware of this member.",
                           "event":"flashsale_consumer_poll_failed",...}
```

因此实验 A 验证的是"**消费能力被压到接近 0（等价于消费者不可用）**时，Kafka 保留消息、
Lag 可观测、恢复后继续消费"，**不是**"消费者进程被 kill"——后者是实验 C。

---

## 5. 实验 B：Kafka Broker 故障与恢复

### 5.1 实验数据

| 项 | 值 |
|---|---|
| 活动 / SKU | activity=18 / sku=16（`t504bf-flash-und11b`），初始库存 10 |
| 用户 | 10 个独立测试用户 |
| 故障窗口 | 17:51:03 → 17:52:02 CST（**59 秒**，`kubectl scale deploy/kafka --replicas=0`） |
| Pod 消失确认 | `Wait-KafkaPodGone = True` |
| 恢复确认 | `kubectl wait` Ready + `kafka-get-offsets` 能返回位移（`kafkaUp=True`） |

### 5.2 故障期间表现（原始：`raw/b/b3-outage-timeline.txt`）

```text
09:51:14Z  backlog=0   lag=NA  joined=0  publish_failed=40  retried=40
09:51:50Z  backlog=10  lag=NA  joined=0  publish_failed=50  retried=50
09:51:55Z  backlog=10  lag=NA  joined=0  publish_failed=50  retried=50
```

关键读数解释：

- `lag=NA`：Broker 不存在，**Lag 无从查询**。脚本把"查不到"显式写成 `NA`，绝不当成 0
  （`snapshot.ps1` 里 `lag_total=-1` + `kafka_errors` 保留原始报错）；
- `backlog=0 → 10`：10 条请求在 Broker 停机期间被正常受理，事件**留在 MySQL**（`published_at IS NULL`）；
- `publish_failed 40 → 50`、`retried 40 → 50`：每条记录的发布失败被计数并写入 `next_publish_at` 指数退避；
- `joined=0`：应用侧 `flashsale_consumer_joined=0`，消费者已失去组成员资格。

HTTP 入口在故障期间完全正常：

```text
/health=HTTP 200: {"code":0,"message":"OK","data":{"status":"ok",...}}
/ready =HTTP 200: {"code":0,"message":"OK","data":{"status":"ready"}}
```

**这是 Outbox 模式的核心价值**：Broker 挂了，业务入口不降级，消息暂存在数据库而不是内存队列。

### 5.3 恢复（原始：`raw/b/b4-recovery-timeline.txt`）

```text
09:52:44Z  backlog=4  lag=0  joined=1
09:52:56Z  backlog=0  lag=0  joined=1
```

- Broker 恢复后 `joined=1`（消费者重新入组），Outbox 按 `next_publish_at` 到期重发，
  `backlog 10 → 4 → 0`，`lag` 保持 0（发布后立刻被消费）；
- **恢复耗时 18 秒**（从 `kafkaUp` 到 backlog 与 lag 同时为 0）。

### 5.4 业务结果

| 时点 | requests | queued | unpublished | orders | sold | remaining |
|---|---:|---:|---:|---:|---:|---:|
| 故障期间 | 10 | 10 | **10** | 0 | 0 | 10 |
| 恢复后 | 10 | 0 | **0** | 10 | 10 | 0 |

`COMMITTED 72 → 82`（10 条全部被消费）。**零丢失、零重复**。

### 5.5 重复发布的判定

本次没有观测到重复订单，但这**不代表** Outbox 保证了 exactly-once：
`published_at` 标记失败、租约过期回收都会导致同一事件被再次发布（`event_id` 不变）。
真正的兜底是消费端的 MySQL 请求行状态闸门（见 §7.2）。

---

## 6. 实验 C：Consumer 异常退出与 Offset 恢复

### 6.1 C-dup：重复消息安全（确定性验证）

做法：从 Topic 精确读回 3 条**已经 success** 的事件（offset 94/95/96，对应 request 93/94/95），
原样再投递一次（`event_id` 与内容完全不变）。

| 指标 | 复投前 | 复投后 |
|---|---|---|
| `flashsale_consumer_processed_total{result="duplicate"}` | 序列不存在 | **3** |
| 订单数 | 10 | 10 |
| 库存 sold / remaining | 10 / 10 | **10 / 10** |
| 请求终态分布 | status=1 n=10 | status=1 n=10 |

关键点：复投发生在**库存仍有剩余（10 件）**的活动上，因此"没有新订单"只能由幂等闸门解释，
而不是"卖完了所以失败"。幂等闸门是 `consume.go` 里的事务内状态检查：
`FOR UPDATE` 锁行 → `if r.Status != queued { duplicate }`。

### 6.2 C-crash：未提交消息跨 SIGKILL 恢复

步骤与读数（原始：`raw/c/c2-crash-*`）：

| 阶段 | committed | logEnd | lag | 说明 |
|---|---:|---:|---:|---|
| 暂停消费 + 提交 10 条请求 | 120 | 130 | **10** | 消息已发布，位移未提交 |
| `kubectl delete pod --grace-period=0 --force` | 120 | 130 | 10 | 无优雅关闭、无离组、无 flush |
| Pod 重建后（配额仍在） | 120 | 130 | 10 | 位移仍是 120 → Broker 保留消息 |
| 撤销配额后 10s | 120 | 130 | 10 | 消费者重新入组中 |
| 撤销配额后 23s | **130** | 130 | **0** | 未提交的 10 条被重新投递并处理完 |

业务结果：`requests=10 / success=10 / orders=10 / distinct_users=10 / dup=0`，
`sold=10 / remaining=0`。**没有任何消息因为进程被杀而丢失。**

### 6.3 关于"处理成功但位移未提交"的窗口（NOT_VERIFIED）

源码顺序是「MySQL COMMIT → CommitRecords」，两者之间有一个毫秒级窗口。
本任务**没有**在真实集群里精确命中它，理由与后果分析：

- 无法用可复现手段把进程停在两条语句之间（没有注入点，也不允许为实验改业务代码）；
- 该窗口的两种后果都可以从本实验的两侧结论推出：
  ① 位移未提交 → 消息重投（已实测：C-crash）；
  ② 重投后业务已终态 → 被 `status != queued` 挡住（已实测：C-dup）；
  因此窗口的后果是"**至多重复投递一次，不会丢单**"。
- 但**不能**声称"实测命中了该窗口"，故标记 **NOT_VERIFIED**。

---

## 7. 实验 D：重试与 DLQ 行为核验

### 7.1 D-1：永久错误 → DLQ（原始：`raw/d/d1-result.json`、`d1-dlq-messages.json`）

投递 4 条消息到 offset 130–133，消费后 DLQ 从 16 → 20，且 `dlq_source_offset` **精确对应**：

| 构造 | 期望 `dlq_reason` | 实测 | 是否写库 |
|---|---|---|---|
| `{"this is":"not valid json"` | `parse_error` | ✅ | 否 |
| `schema_version=99` | `schema_version` | ✅ | 否 |
| `event_type=flashsale.order.created` | `event_type` | ✅ | 否 |
| `request_id=99999999`（不存在） | `request_missing` | ✅ | 否 |

指标同步：`flashsale_consumer_processed_total{result="invalid"}=4`，
`flashsale_consumer_dlq_total{reason=...}` 每个原因各 1。
数据库侧 `orders 0 → 0`、`requests 0 → 0`：**不可信消息绝不改写权威业务表**。

> 说明：DLQ 中 offset 0–9 是 T5-04 前期摸底时手工投递的无意义负载（含 BOM 前缀、纯文本、中文文本，
> `dlq_source_offset=34..41`），它们证明 DLQ 通路在正式实验前就已可用；本任务的正式结论以上表为准。

### 7.2 D-2：重试预算耗尽 → DLQ + dead 终态（原始：`raw/d/d2-result.json`）

| 阶段 | 事实 |
|---|---|
| 受理 | request=124 经真实 HTTP 入口受理，`status=0`，`published_offset=135`，Redis 已预扣 |
| 注入 | `UPDATE ... SET retry_count=4, last_error_code=500 WHERE id=124 AND status=0` |
| 消费 | 消费端读到 `retry_count(4) > 3` → 不执行业务 → 死信 → `MarkRequestDead` |
| DLQ | 21 → 22，`dlq_reason=retry_exhausted` |
| 终态 | `status=3(dead)`、`retry_count=4`、`flash_order_id=0`（**没有建单**） |
| 位移 | `committed 135 → 136`，lag 归零 |
| 指标 | `flashsale_consumer_processed_total{result="dead"}=2`（含前一次同路径样本） |

### 7.3 DLQ 结论

- **DLQ 已实现**，不是 NOT_IMPLEMENTED；
- 死信与业务共用 Producer（acks=all），**Broker 未确认就不提交原位移**（`deadLetter()` 的注释与代码一致）；
- 本任务**没有修改**任何重试语义、Topic 结构或 Consumer Group。

---

## 8. 最终业务一致性核验

原始证据：`raw/final/final-summary.txt`、`kafka.json`、`mysql.json`、`redis.json`、
`prometheus-window.json`、`kubernetes.json`、`redis-active-check.txt`。

### 8.1 Kafka

```text
topic  partition=0 earliest=0 latest=136
dlq    partition=0 earliest=0 latest=22
group  surgecart-flashsale-order-worker-v1  CURRENT=136  LOG-END=136  LAG=0
unconsumed_records = 0
```

### 8.2 MySQL（本任务 8 个测试活动的终态）

下表省略了原始输出的最后一列 `activity_end_epoch`（保留在 `raw/final/mysql.json` 中）。

```text
activity|sku|total|sold|remaining|req_total|queued|success|failed|dead|unpublished|orders|distinct_users|dup_user|dup_no|retry_exhausted
16|14|20|20|0|20|0|20|0|0|0|20|20|0|0|0
17|15|10|10|0|10|0|10|0|0|0|10|10|0|0|0
18|16|10|10|0|10|0|10|0|0|0|10|10|0|0|0
19|17|10|10|0|10|0|10|0|0|0|10|10|0|0|0
20|18|10|10|0|10|0|10|0|0|0|10|10|0|0|0
21|19|20|10|10|10|0|10|0|0|0|10|10|0|0|0
22|20|10|10|0|10|0|10|0|0|0|10|10|0|0|0
23|21|5|0|5|2|0|0|0|2|0|0|0|0|0|2
```

- **queued = 0**：没有任何请求永久滞留在 `queued`；
- **unpublished = 0**：Outbox 无残留；
- 活动 21 的 `remaining=10` 正是 C-dup 幂等性的直接证据（20 件库存只卖出 10 件，3 次重复投递未多扣）；
- 活动 23 的 `dead=2 / retry_exhausted=2` 是 D-2 的两次同路径样本（request 123/124），
  两次都 `orders=0`、`sold=0`（预算耗尽的请求绝不建单，也绝不扣库存）。

全库不变量（含历史数据）：

```text
requests_total=115 | queued=0 | unpublished=0 | orders_total=104
dup_order_no=0 | dup_user_per_activity=0 | sold_gt_stock=0
```

### 8.3 Redis

| 项 | 值 |
|---|---|
| `flashsale:stock:*` 键数 | 1（仅剩仍在时间窗内的既有活动 12） |
| `flashsale:bought:*` / `flashsale:idem:*` | 0 / 0（无残留标记） |
| 活跃活动交叉校验 | `redis_stock=3`，MySQL 权威值 `total-sold-inflight=3-0-0=3` → **一致** |

本任务的测试活动（16–23）在核验时均已越过 `end_time`，缓存被对账扫描器清理
（`redis_stock=(nil)`、`ttl=-2`），这是设计行为（`ReconcileCache` 对已结束活动做终态收敛/失效），
不是"Redis 与 MySQL 不一致"。

### 8.4 Prometheus（最近 180 分钟，step 30s）

```text
kafka_consumer_lag            min=0   max=20   last=0
outbox_backlog_pending        min=0   max=10   last=0
outbox_oldest_age_seconds     min=0   max=28903.504  last=0
sampler_up{outbox}            min=1   max=1    last=1
sampler_up{kafka_lag}         min=0   max=1    last=1
consumer_joined               min=0   max=1    last=1
consumer_processed_total      min=10  max=104  last=16
consumer_processed_invalid    min=1   max=13   last=4
consumer_processed_duplicate  min=1   max=3    last=3
consumer_processed_dead       min=1   max=2    last=2
consumer_dlq_total            min=1   max=13   last=6
outbox_published              min=0   max=101  last=2
outbox_publish_failed         min=10  max=32   last=32
http_requests (rate/s)        min=0.244 max=1.644 last=0.311
```

- `lag max=20` ↔ 实验 A；`backlog max=10` ↔ 实验 B；`duplicate=3` ↔ C-dup；`dead=2` ↔ D-2；
  `sampler_up{kafka_lag}` 出现 0 是因为 Broker 停机期间采样失败（**失败即置 0，不伪造数据**）；
- 计数器在 19:24 的 Pod 重建后归零重计（`consumer_processed_total` 从 104 掉到 16），
  这是进程级计数器，Prometheus 侧表现为序列重置 —— 分析区间数据时必须考虑这一点。

---

## 9. 关键日志摘录

```text
# 实验 A/C：配额把消费拉取压到 0 后，消费者被踢出组（心跳/组协调报文同样被限流）
[WARN] {"event":"flashsale_consumer_group_lost","reason":"lost","partitions":1,...}
[WARN] {"event":"flashsale_consumer_poll_failed",
        "error":"拉取 Kafka 消息: /0: unable to join group session: UNKNOWN_MEMBER_ID:
                 The coordinator is not aware of this member."}

# 实验 B：Broker 停机期间 Outbox 的失败与退避（本层绝不改写业务状态）
[WARN] {"event":"flashsale_outbox_publish_failed","publish_attempts":2,
        "error":"... broker ... 不可用"}
[WARN] {"event":"flashsale_outbox_loop_backoff","backoff":"2s","claimed":10,"failed":10,...}

# 死信（D-1 / D-2）
[WARN] {"event":"flashsale_consumer_invalid_message","reason":"parse_error",
        "topic":"surgecart.flashsale.order.requested.v1","partition":0,"offset":130,...}
[WARN] {"event":"flashsale_consumer_dead_letter","reason":"retry_exhausted",
        "request_id":124,"retry_count":4,"source_partition":0,"source_offset":135}
```

---

## 10. 真实故障发现

### 10.1 【缺陷 · 未修复】`surgecart_outbox_oldest_age_seconds` 恒定多报 8 小时

**现象**：实验 B 期间该 Gauge 报 `28873–28903.5` 秒（8h01m），而那批记录的真实年龄约 73–103 秒。
非零样本仅出现在 17:44:58–17:52:28（即 Broker 停机、Outbox 里确实存在 pending 记录的窗口）——
因为**只有存在待发布记录时该指标才非零**。

**定位**（`internal/logic/flashsale/outbox.go` `Backlog()`）：

```sql
SELECT created_at, NOW(6) AS db_now FROM flash_sale_order_requests WHERE id = ?   -- 同一条记录、同一会话
```

应用 DSN 把会话时区设为 `+08:00`（`internal/boot/boot.go` `databaseTimeZoneExtra`），
而 `created_at` 是 Go 侧时间经驱动格式化后的**UTC 墙钟**（同一份数据实测：

```sql
created_at=2026-11-10 09:51:11   now=2026-11-10 11:42:22   diff_s=6671
```

即同一会话里 `created_at` 是 UTC 墙钟、`NOW(6)` 是 +08 墙钟）。
Go 侧 `DbNow.Sub(CreatedAt)` 于是恒定多出 `28800s`。

**影响**：
- 该指标**不能用于告警**（任何新鲜积压都会显示为 8 小时前）；
- `flashsale_outbox_backlog_pending` 是 `COUNT(*)`，**不受影响**（实验 B 报的 10 是正确的）；
- 业务数据不受影响（只影响观测口径）。

**建议**（未实施，任务禁止改业务/监控实现）：Backlog 查询改用 `TIMESTAMPDIFF(SECOND, created_at, NOW(6))`
在 SQL 内计算，或统一让 `created_at` 与 `NOW(6)` 走同一时区语义。已在代码层留下复现步骤，
由后续任务决策。

### 10.2 【发现 · 非缺陷】Prometheus Lag 指标滞后于 Broker 事实

`surgecart_kafka_consumer_lag` 是**异步采样**（10s 周期）+ 15s 抓取。实验里出现多次
`broker_lag=0` 而 `prom_lag=20/10/1` 的窗口（见 §4.3、§6.2、§7.2）。
结论：排查积压必须以 `kafka-consumer-groups.sh` 为权威，Gauge 只用于趋势与告警；
把采样周期和抓取间隔当成"实时"会导致误判。

### 10.3 【发现 · 环境事实】配额暂停会连带踢出消费组

见 §4.5。它意味着实验 A 的"暂停"比"单纯不拉取"更强：消费者连成员资格都丢了，
恢复时要走完整 rebalance。报告按更强语义描述，不夸大为"进程被 kill"。

### 10.4 【工具缺陷 · 已修复】实验脚本自身的 4 个坑

| # | 现象 | 根因 | 修复 |
|---|---|---|---|
| 1 | 复投脚本读到 offset 1，报"未读到 JSON 消息" | `@([long]$x, [long]$x + 1)` 被 PowerShell 拆成多个元素（实测 `82,82,1,82,2`） | 先把偏移算进变量 / 加括号 |
| 2 | `kubectl delete pod` NotFound，Pod 名是两段拼接 | `-l app=surgecart` 同时匹配 Completed 的 migrate Job，`(-join '')` 把两行拼在一起 | 加 `--field-selector=status.phase=Running` 并只取第一个 |
| 3 | 复投的消息被判 `parse_error`（`\ufeff`） | PowerShell 5.1 向 native stdin 传文本会写入 UTF-8 BOM（`$OutputEncoding` 无效） | 改成"无 BOM 临时文件 + `Start-Process -RedirectStandardInput`" |
| 4 | 收尾阶段无输出忙循环、`ConvertTo-Json` 抛 `OutOfMemoryException` | `Get-Content` 返回的字符串带 ETS 属性（`PSProvider` 引用），JSON 序列化顺着引用展开对象图 | 改用 `[System.IO.File]::ReadAllLines` |

此外还踩到：单元素数组被解包成标量（`$row[0]` 取到首字符 '1'）、
`-DupReplayOffsets 94,95,96` 被当成一个字符串（`949596`）、
无 BOM 的 `.ps1` 被 PowerShell 5.1 按 ANSI 解析导致中文注释乱码并解析失败。
这些都写在脚本注释里，避免下次重犯。

**证据文件重建声明**：坑 #4 的直接后果是 `raw/c/` 下三个 JSON
（`c1-dup-result.json` 230MB、`c2-crash-result.json` 115MB、`c.summary.json` 234MB）
被 provider 对象图撑爆而不可用。这三个文件已在实验结束后**按同一批事实重建**
（来源：`c1-dup-before` / `c2-crash-paused` / `c2-crash-restarted` / `c2-crash-recovered` 快照、
`exp-c-crash2-console.log`、MySQL 权威查询），每个文件内都有 `artifact_note` 字段说明重建来源；
所有数值与原 console 日志一致，未修改任何结论，原始日志仍完整保留。

---

## 11. NOT_VERIFIED / 未覆盖项

| 项 | 状态 | 说明 |
|---|---|---|
| "MySQL 已提交、位移未提交"毫秒级窗口 | **NOT_VERIFIED** | 无法在真实集群精确命中；其两侧后果已实测（§6.3） |
| `retry_count` 如何被真实技术失败推进 | **NOT_VERIFIED**（真实集群）/ 单元测试覆盖 | 需停 MySQL 才能制造真实技术失败，风险大于收益；D-2 注入的是状态 |
| 多分区并发消费、分区再平衡下的位移正确性 | 未覆盖 | 本环境 Topic 仅 1 分区、单消费者实例 |
| 多应用实例同时消费 | 未覆盖 | Deployment 1 副本 |
| 长时断流（分钟级）后的 Broker 日志恢复 | 部分覆盖 | 只做了 59s scale-to-0；未做磁盘/日志损坏场景 |
| `db_scanner` 模式的重试语义 | 未覆盖 | 本任务只验证 `kafka` 模式 |
| Outbox "Broker 已 ACK 但 published_at 写入失败"的窗口 | 未覆盖 | 源码有 `outboxBeforeMarkHook` 测试钩子，仅在单测里可注入，本任务未在集群验证 |

---

## 12. 环境副作用与残留数据

| 项 | 影响范围 | 处理 |
|---|---|---|
| 新增测试活动 16–23（含 SKU 14–21、商品 12–21） | 仅测试数据，名称带 `t504*` 前缀 | **保留**（任务禁止清理历史业务数据），标识见 §8.2 |
| 新增测试用户 `t504ae/t504be/t504cf/t504cg/t504dx/t504d...` | 仅测试账号 | 保留 |
| Kafka Topic 位移从 34 增长到 136 | 只增不删，未清理历史 | 保留 |
| DLQ 位移 2 → 22（含 8 条前期摸底负载、5 条 BOM 污染样本、4 条 D-1、1 条 D-2） | DLQ 只增不删 | 保留，原因已在 §7.1 注明 |
| Kafka 配额 `consumer_byte_rate` | 已 `--delete-config` 回滚（`raw/a/a4-quota-removed.txt`） | 已恢复 |
| Kafka Deployment 副本数 | 已 scale 回 1，`ready=1` | 已恢复 |
| 应用 Pod 被 SIGKILL 2 次（C-crash 正式 1 次 + 1 次误触发） | ReplicaSet 自愈，无状态丢失 | 已恢复 |
| 端口转发 | Pod 重建后 `kubectl port-forward` 会失效，实验脚本未自动重建 | 已手工重建；已知限制 |
| Schema / Redis Lua / Kafka 协议 / Consumer Group / 重试语义 | **均未修改** | — |

---

## 13. 自动化门禁（如实记录）

原始日志：`raw/gates/go-gates-linux.log`、`raw/gates/test-unit-official.log`。

本任务**未修改任何 Go 代码 / Schema / Lua / Kafka 协议**，门禁用于证明"没有把仓库弄坏"。

| # | 门禁 | 实际命令 | 环境 | 结果 |
|---|---|---|---|---|
| 1 | gofmt | `gofmt -l api internal main.go` | Windows 宿主 | **PASS**（无输出，exit 0） |
| 2 | go build（全量） | `go build ./...` | Windows 宿主 | **FAIL（平台限制，非本任务引入）**：`test/deployk8s/fakestate/state.go` 使用 `syscall.Flock/LOCK_EX`，Windows 无此 API |
| 2b | go build（全量，复核） | `go build ./...` | Linux 容器 `golang:1.26` | **PASS**（`BUILD_EXIT=0`） |
| 2c | go build（非 test 包） | `go build ./api/... ./internal/... .` | Windows 宿主 | **PASS**（exit 0） |
| 3 | go vet（全量，复核） | `go vet ./...` | Linux 容器 `golang:1.26` | **PASS**（`VET_EXIT=0`） |
| 3b | go vet（非 test 包） | `go vet ./api/... ./internal/... .` | Windows 宿主 | **PASS**（exit 0） |
| 4 | 官方单元测试门禁 | `bash scripts/test-unit.sh` | Linux 容器 `golang:1.26` | **PASS**（输出 `[INFO] 单元测试全部通过`；覆盖 `internal/mq/kafka`、`internal/worker`、`internal/logic/flashsale` 含 `TestConsumer*DLQ*` / `TestConsumerRetryBudgetExhaustedGoesToDLQ`） |
| 5 | Manifest 测试 | `kubectl kustomize manifest/kubernetes/{overlays,migration/overlays}/production` + `go test ./test/manifest/...` | Windows 宿主（真实 kubectl） | **PASS**（两次构建 exit 0；`ok ... 12.604s`） |
| 6 | 新增脚本静态检查 | PowerShell `Parser::ParseFile` + UTF-8 BOM 校验（8 个 `.ps1`） | Windows 宿主 | **PASS**（8/8 语法 OK、均有 BOM） |
| 7 | 可安全复现的自动化验证 | k6 真实 HTTP + 真实 Kafka/MySQL/Redis（实验 A–D 与最终核验） | kind 集群 | **PASS**（见 §4–§8） |
| 8 | 未执行 | `make test-integration` / `test-race` / `test-kafka-e2e` / `scripts/test-k8s-manifest.sh` | — | **未执行**：前三项需要独立 MySQL/Redis 依赖容器（本任务未改 Go 代码，且真实集群实验已覆盖同路径）；后一项需要 bash（宿主 `bash.exe` 指向不可用的 WSL），其核心两层（kustomize 构建 + `test/manifest` 断言）已按第 5 项直接执行 |

> 说明：门禁脚本 `raw/gates/go-gates.sh` 用 `| tail` 捕获输出，
> 因此日志中 `UNIT_*_EXIT` 是 `tail` 的退出码、**不作为判定依据**；
> 判定依据是每个包输出的 `ok` 行与官方脚本最后一句 `[INFO] 单元测试全部通过`。

---

## 14. 验收对照

| 验收项 | 结果 | 证据 |
|---|---|---|
| Kafka 架构调查 | PASS | §2（Producer/Consumer 同进程、client.id、Group、Topic 分区数、Outbox、指标） |
| Consumer Offset 机制调查 | PASS | §2.3（`DisableAutoCommit`、批次 1、BlockRebalanceOnPoll、提交时机） |
| Broker 故障实验 | PASS | §5.2（59s 停机、backlog 10、publish_failed +10、lag NA） |
| Broker 恢复实验 | PASS | §5.3（18s 恢复、backlog 0、lag 0、committed 72→82） |
| Consumer 积压实验 | PASS | §4（配额暂停，lag 0→20→0，交叉验证） |
| Consumer 重启恢复 | PASS | §6.2（SIGKILL，committed 120→130，无丢失） |
| 重试策略调查 | PASS | §2.4（预算 3、单次投递 4 次、指数退避、`next_attempt_at`） |
| DLQ 行为 | PASS（已实现） | §7（4 类永久错误 + retry_exhausted，头部与偏移可追溯） |
| Kafka Lag 监控 | PASS | §4.2、§8.4（故障前/中/后区间数据） |
| Outbox 恢复 | PASS | §5.2–5.4（10 条保留 → 重发 → 消费） |
| MySQL 最终一致性 | PASS | §8.2（queued=0、无重复、无超卖） |
| 自动化门禁 | 见 §13 | 如实记录，未执行项不写 PASS |
| 技术学习文档 | 完成 | `.agent/tasks/dev/T5-04-kafka-backlog-failure-recovery.md` |
| 正式实验报告 | 完成 | 本文件 |
| Git 推送 | 完成 | 见下 |

---

## 15. 提交记录

| 项 | 值 |
|---|---|
| 基线 SHA | `be7fbe429791577679e5e1dcb12e4fe44f6f17b7` |
| 开发分支 | `feat/k8s-t5-04-kafka-backlog-failure-recovery` |
| 实现提交 SHA | 见下一次元数据提交回填 |
| 最终 SHA | 见下一次元数据提交回填 |
| 远端 SHA | 见下一次元数据提交回填 |
