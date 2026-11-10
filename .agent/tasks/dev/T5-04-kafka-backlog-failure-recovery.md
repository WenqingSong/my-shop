# T5-04 Kafka 积压与故障恢复：从源码到真实故障注入

- 任务：T5-04（SurgeCart 任务五第 4 子任务）
- 分支：`feat/k8s-t5-04-kafka-backlog-failure-recovery`
- 基线：`feat/k8s-t5-03-concurrency-correctness` @ `be7fbe429791577679e5e1dcb12e4fe44f6f17b7`
- 正式报告：`docs/reports/t5-04-kafka-backlog-failure-recovery/T5-04_KAFKA_BACKLOG_FAILURE_RECOVERY_REPORT.md`
- 原始证据：`docs/reports/t5-04-kafka-backlog-failure-recovery/raw/`
- 本任务**未修改任何 Go 业务逻辑 / DB Schema / Redis Lua / Kafka 协议 / Consumer Group**

本文写给"下一次要接手这套链路的人"和"准备面试复盘的人"：
先讲清这套系统**实际怎么跑**（不是通用 Kafka 教程），再讲**怎么在真实集群上把它弄坏**，
最后讲**为什么这些机制必须这样设计**。

---

## 1. 本次任务的目标

不是重构 Kafka 架构，而是回答 7 个问题，并且每个答案都要有真实运行证据：

1. Kafka 不可用时，业务请求能否被正确处理？
2. 消息来不及消费时，积压能否被**观测**？
3. Kafka 恢复后，消息能否继续发布与消费？
4. Consumer 异常退出后，未提交的消息能否重新消费？
5. 消息重试机制是否与源码定义一致？
6. 是否存在 DLQ？其行为是否符合预期？
7 故障恢复后，MySQL / Redis / Kafka 是否仍保持业务一致？

对应 4 组实验：A 消费积压、B Broker 故障、C Consumer 崩溃、D 重试与死信。

---

## 2. 实验前系统架构

### 2.1 一个 Pod 里跑着五个"组件"

```text
surgecart Pod（单容器，dispatch_mode=kafka）
├── HTTP Server                 业务入口（秒杀下单 / 结果查询 / 管理端）
├── Kafka Producer              client.id=surgecart-flashsale-producer
├── Outbox Publisher goroutine  每 200ms 一轮，批量 100，SKIP LOCKED 领取
├── Kafka Consumer goroutine    client.id=surgecart-flashsale-consumer，BatchSize=1
├── Outbox Backlog Sampler      5s 周期查库，写 flashsale_outbox_backlog_pending
└── Kafka Lag Sampler           10s 周期，**独立 Kafka 客户端**（不入组）
```

装配入口：`internal/cmd/cmd.go` → `worker.StartFlashSaleRuntime()` →
`startFlashSaleRuntime()`（`internal/worker/flashsale_runtime.go`）。

**这个事实决定了整份实验设计**：没有独立 Consumer Deployment，没有"只关消费者"的开关。
任何"停 Pod / 停 Broker / 阻断网络"的做法都会连带打断 HTTP 入口或生产者，
实验结论就会变成"整个服务都挂了"而不是"消费端出问题了"。

### 2.2 两种 dispatch_mode 严格互斥

| 模式 | 运行的组件 | 谁处理 queued 请求 |
|---|---|---|
| `db_scanner`（默认） | 三个 MySQL 扫描器 | `StartFlashSaleConsumeScanner` |
| `kafka` | Producer + Outbox + Consumer | Kafka 消息驱动 |

互斥是硬约束：两者都会处理 `status=0` 的同一批行，同时跑会争抢行锁、重复烧 `retry_count` 预算。
所以配置矛盾必须**启动失败**（`resolveFlashSaleRuntimeConfig`），不能静默退回。

---

## 3. Producer / Consumer 的运行机制

### 3.1 Producer：同步确认 + 幂等写入

`internal/mq/kafka/producer.go`：

```go
kgo.ClientID(producerClientID),                 // surgecart-flashsale-producer
kgo.RequiredAcks(kgo.AllISRAcks()),             // acks=all
// franz-go 默认开启幂等写入（producer id/epoch/sequence 去重）
kgo.RecordDeliveryTimeout(30 * time.Second),    // 投递超时有界
```

`Publish` 是**同步**的：返回 `[]PublishResult`，逐条 Err 判定，绝不把"部分失败"伪装成整批成功。
为什么必须这样：Outbox 只有在"Broker 明确确认"之后才允许写 `published_at`（INV-2）。

### 3.2 Consumer：手动提交 + 冻结再平衡

`internal/mq/kafka/consumer.go`：

```go
kgo.ConsumerGroup(cfg.ConsumerGroup),   // surgecart-flashsale-order-worker-v1
kgo.DisableAutoCommit(),                // 禁止自动提交
kgo.BlockRebalanceOnPoll(),             // 批次处理期间冻结再平衡
kgo.SessionTimeout(30s), kgo.HeartbeatInterval(3s),
```

业务消费循环（`internal/logic/flashsale/kafka_consumer.go`）：

```text
Poll(1)
  → parseRequestedEvent（JSON → schema_version → event_type → 字段 → 头部一致性）
  → svc.ConsumeRequest（MySQL 事务：FOR UPDATE 锁行 → 状态闸门 → 建单+扣库存 → 终态）
  → MySQL COMMIT
  → client.Commit(offset+1)
  → AllowRebalance（defer，异常路径也释放）
```

`BatchSize=1` 不是偷懒：它把 `BlockRebalanceOnPoll` 的阻塞窗口压到"单条消息处理时间"，
并天然保证位移连续提交（不会出现"提交了 5、漏了 3"）。

---

## 4. Consumer Group 与 Offset 的工作原理（结合本项目）

### 4.1 位移是什么

- **log-end offset**：分区里"下一条要写入的位置"，即消息总条数（从 0 开始计）。
- **committed offset**：消费组记录的"下一条要消费的位置"。注意是 **offset+1**：
  `Consumer.Commit()` 收到的是一批已处理消息，提交的是同分区的**最大 offset**，
  franz-go 的 `CommitRecords` 会把它当作"下一条待消费位置"。
- **lag = log-end − committed**。lag=20 意味着有 20 条已写入但未确认消费。

### 4.2 为什么本项目必须手动提交

自动提交（每 5s 提交一次 poll 位置）与"业务幂等 + 不丢单"的目标是冲突的：

- 自动提交提交的是**已拉取**位置，不是**已处理**位置。若拉到 10 条、处理到第 3 条时崩溃，
  自动提交已经把 7 条"确认"掉了 → **丢消息**；
- 本项目的业务副作用是 MySQL 事务（建单+扣库存），它和 Kafka 位移**没有分布式事务**，
  只能靠"先提交业务、再提交位移"的顺序逼近 at-least-once。

所以 `DisableAutoCommit()` + 显式 `Commit`。代价是必须自己保证：
异常路径不提交、提交失败不吞错（`ObserveConsumerCommitFailure` 会记指标）。

### 4.3 谁算"已得出结论"

`consumeBatch` 的规则很克制：**只有"这条消息已经有确定结论"才进入待提交集合**。

| 结论 | 是否提交位移 |
|---|---|
| 业务成功（建单成功） | ✅ |
| 业务失败终态（库存不足/一人一单/幂等冲突） | ✅ |
| 重复投递（请求已终态） | ✅ |
| 永久错误已进 DLQ（死信已 ACK） | ✅ |
| 技术失败（MySQL 报错、死信未 ACK、预算写不进去） | ❌ 停止本批，只提交它之前的消息 |

最后一行是"消息不丢"的关键：**失败消息之后的同分区消息也不能提交**，
否则跳过失败消息就永久丢了它。

---

## 5. Outbox 与 Kafka 的关系

### 5.1 要解决的问题

"先写 MySQL 再发 Kafka"这两步之间进程崩溃 → 订单在库里但没有事件 → 订单永远不被处理（丢单）。
"先发 Kafka 再写 MySQL" → 消息被消费时库里没有请求行（幽灵消息）。

### 5.2 本项目怎么解

不新建表，直接把 `flash_sale_order_requests` 当 Outbox：

```text
请求 INSERT（status=0, published_at IS NULL）  ← 事务提交即"事件可恢复"
        ↓
Publisher: SELECT ... FOR UPDATE SKIP LOCKED（status=0 AND published_at IS NULL
           AND next_publish_at <= NOW(6) AND (租约为空或已过期)）
        → 写 claim_token + 租约 60s + publish_attempts+1 → **事务提交**
        → Kafka Publish(acks=all)（此时不持任何行锁）
        → 逐条条件更新 published_at / published_partition / published_offset
        → 失败则写 next_publish_at（指数退避 2s→5min）+ last_publish_error
```

三个不可交换的边界：

1. **领取事务先提交，再发 Kafka** —— Kafka 网络调用期间不持 MySQL 行锁；
2. **只有 Broker 明确 ACK 才写 `published_at`** —— 这条是"不丢"的根；
3. **发布失败绝不改 status** —— 短时 Kafka 故障不是业务失败，请求继续 `queued`，
   由 `next_publish_at` 到期重试。

### 5.3 租约（lease）为什么必要

多实例/多 goroutine 下要互斥，但**不能用进程内 mutex**（跨 Pod 无效）也不能长时间持行锁。
租约解决"领取者崩溃"：租约 60s 过期后别人可以重新领取（`LeaseReclaimed` 指标），
代价是可能重复发布（at-least-once）。

---

## 6. 每项实验的执行过程（含关键读数）

### 6.1 实验 A：消费积压与恢复

**难点**：怎么在"HTTP 入口可用"的前提下停掉消费？

**答案**：Kafka 客户端配额。生产者和消费者用了两个不同的 `client.id`，
而配额按 `client.id` 生效：

```bash
kafka-configs.sh --alter --entity-type clients \
  --entity-name surgecart-flashsale-consumer --add-config consumer_byte_rate=1
```

1 字节/秒 ≈ 拉不动任何消息，但**生产者不受影响**（另一个 client.id）。
`--delete-config` 即可完全回滚，不重启 Broker、不动 Topic。

实测：

| 时点 | committed | logEnd | lag | Prometheus lag | Outbox backlog |
|---|---:|---:|---:|---:|---:|
| 暂停中（首样本） | 42 | 62 | **20** | 0（采样尚未更新） | 0 |
| 暂停中（后续） | 42 | 62 | **20** | 20 | 0 |
| 撤销配额后 | 62 | 62 | **0** | 20（仍滞后一拍） | 0 |

业务结果：暂停期间 20 条请求全部 `queued`（HTTP 已受理、未成交），
恢复后 20 条全部 `success`、20 张订单、库存 20/20 卖完、无重复。

**副作用**：配额连心跳/组协调报文一起限流，消费者会丢组成员资格
（`UNKNOWN_MEMBER_ID`、`flashsale_consumer_group_lost`）。
所以实验 A 的语义是"消费能力被压到接近 0"，而不是"进程被 kill"。

### 6.2 实验 B：Broker 故障与恢复

先确认工作负载类型：`manifest/kubernetes/infrastructure/base/kafka.yaml` 里 Kafka 是 **Deployment**，
数据在 PVC `kafka-data` 上。所以用 `scale --replicas=0` 制造"Broker 进程不存在"，
再 `--replicas=1` 恢复；KRaft 元数据留在 PVC，不需要重建。

停机 59 秒期间：

- `/health` `/ready` 均 200（HTTP 侧完全不降级）；
- 10 条请求被受理，`requests_unpublished=10`（事件留在 MySQL）；
- `flashsale_outbox_publish_failed_total` 40→50，`outbox_retried_total` 40→50；
- `kafka_consumer_lag` **查不到**（脚本显式写 `NA`，不当 0）。

恢复后：`joined=1` → backlog 10→4→0 → lag 0，**18 秒**收敛，10 张订单、零丢失。

### 6.3 实验 C：Consumer 崩溃

两个子实验，分开回答两个不同问题：

**C-dup（重复投递会不会重复下单）**
从 Topic 精确读回 3 条已 success 的事件 → 原样再投递 →
`consumer_processed_total{result="duplicate"}=3`，订单数 10 不变，`sold` 10 不变。
活动总库存 20、当时 remaining=10，所以"没有新订单"只能由幂等闸门解释。

**C-crash（未提交消息会不会丢）**
配额暂停 → 提交 10 条（committed 120 / logEnd 130 / lag 10）→
`kubectl delete pod --grace-period=0 --force`（SIGKILL）→ Pod 重建 →
撤销配额 → committed 130 / lag 0，10 张订单，不重不漏。

### 6.4 实验 D：重试与 DLQ

**D-1 永久错误**：投 4 条不可处理消息（坏 JSON / schema_version=99 / 错 event_type /
不存在的 request_id），DLQ 16→20，`dlq_source_offset` 精确等于投递偏移 130–133，
`dlq_reason` 四类各一条，数据库零写入。

**D-2 重试预算耗尽**：真实受理 1 条请求（Redis 已预扣、`published_offset` 已知），
把该行 `retry_count` 置 4（> 预算 3），恢复消费 → DLQ 21→22（`retry_exhausted`）、
请求落 `status=3(dead)`、`flash_order_id=0`、Redis 由补偿收敛回权威值。

---

## 7. 遇到的问题（真实踩坑清单）

| # | 问题 | 现象 |
|---|---|---|
| 1 | 脚本读到 offset 1 | 报"未读到 JSON 消息"，复投逻辑失效 |
| 2 | `kubectl delete pod` NotFound | Pod 名是 `surgecart-xxxsurgecart-migrate-yyy` |
| 3 | 复投消息被判 `parse_error` | `invalid character '\ufeff'`，全进 DLQ |
| 4 | 收尾阶段无输出忙循环 | `ConvertTo-Json` 抛 `OutOfMemoryException` |
| 5 | 单元素数组 | `$row[0]` 取到字符串首字符 '1' |
| 6 | 逗号参数 | `-DupReplayOffsets 94,95,96` 变 `949596` |
| 7 | `.ps1` 无 BOM | 中文注释乱码导致解析失败 |
| 8 | `kubectl port-forward` | Pod 重建后转发失效，k6 报 EOF |
| 9 | 脚本误触发第二次故障注入 | 复投续跑顺带又 kill 了一次 Pod，覆盖了 crash 证据 |

---

## 8. 问题的根因

1. **`@([long]$x, [long]$x + 1)`**：PowerShell 在数组字面量里把 `+ 1` 当成新的元素
   （实测得到 `82,82,1,82,2` 五个元素）。教训：**数组字面量里不要写二元运算**。
2. **`kubectl get pods -l app=surgecart -o name`**：同标签的 `surgecart-migrate-*` Job Pod
   处于 Completed 也在结果里；`(-join '')` 把两行拼成一个不存在的名字。
   教训：**取名字要加 `--field-selector=status.phase=Running` 并只取第一个**。
3. **PowerShell 5.1 向 native stdin 传文本会写 UTF-8 BOM**（实测字节 `ef bb bf`），
   而且**改 `$OutputEncoding` 无效**（ASCII 也带 BOM）。消息首字符变 U+FEFF，
   `json.Unmarshal` 直接失败 → 被（正确地）判成 `parse_error` 进 DLQ。
4. **`Get-Content` 返回的字符串带 ETS 属性**（`PSPath`/`PSProvider`/`ReadCount`），
   `ConvertTo-Json` 会顺着 `PSProvider` 引用序列化整个 provider/驱动器对象图 → OOM。
5. **PowerShell 函数返回单元素数组会被解包成标量**，`$row[0]` 于是变成首字符。
6. **`powershell -File` 把 `94,95,96` 当字符串**，`[long[]]` 转换得到单值 `949596`。
7. **无 BOM 的 UTF-8 `.ps1` 在 Windows PowerShell 5.1 下按 ANSI 解码**，中文注释变成乱码，
   引号/括号错位 → 语法错误。
8. **`port-forward` 绑定的是具体 Pod**，Pod 重建即断。
9. **故障注入脚本缺少"只跑子实验"的开关**：续跑时把别的子实验又执行了一遍。

---

## 9. 如何定位和解决

### 9.1 定位方法（可复用）

- **偏移对不对，读回来看**：`kafka-console-consumer.sh --partition 0 --offset N --max-messages 1`
  是最直接的证据；配合 `--property print.headers=true` 能看死信头。
- **死信为什么进 DLQ**：DLQ 消息头的 `dlq_reason` + `dlq_error` 直接给答案
  （本例 `dlq_error` 里那句 `invalid character '\ufeff'` 一眼定位到 BOM）。
- **指标为什么对不上**：用 Prometheus 的 `query_range` 看**历史**，而不是只看瞬时值；
  本例正是靠 `flashsale_consumer_processed_total` 的历史曲线发现"复投被计成了 invalid 而不是 duplicate"。
- **进程为什么卡住**：先看有没有子进程（没有 → 不是外部命令阻塞），再看 CPU 时间
  （持续增长 → PowerShell 内部忙循环），最后逐段 try/catch 定位到具体 cmdlet。
- **证据要能反查**：每条结论都保留 `(topic, partition, offset)` 与时间戳，
  报告里的数字必须能在 raw 文件里找到。

### 9.2 解决方式

| 问题 | 解决 |
|---|---|
| 数组字面量运算 | 先算进变量 / 加括号 |
| Pod 名 | `--field-selector=status.phase=Running` + 取第一个 |
| BOM | 写"UTF-8 无 BOM 临时文件"，用 `Start-Process -RedirectStandardInput` |
| ETS / OOM | `[System.IO.File]::ReadAllLines` 直接读文件 |
| 单元素数组 | 调用处用 `@(...)` 包住 |
| 逗号参数 | 用 `[string]` 接收后 `-split '[,\s]+'` |
| BOM 缺失 | 用 `UTF8Encoding($true)` 写 `.ps1`（Windows PowerShell 5.1 必需） |
| port-forward | Pod 重建后手工重建（已知限制，写入报告） |
| 子实验误触发 | 增加 `-SkipDup` / `-SkipCrash` / `-SkipProduce` / `-FinalizeRequestId` 开关 |

并且把每条坑的**原因**写进脚本注释——脚本本身就是文档。

---

## 10. 核心实现逻辑（读代码时该看哪几段）

| 关注点 | 文件 / 函数 |
|---|---|
| 装配与生命周期、关闭顺序 | `internal/worker/flashsale_runtime.go` `startKafka` / `Shutdown` / `superviseConsumer` |
| Outbox 领取-发布-标记 | `internal/logic/flashsale/outbox.go` `PublishBatch` / `claim` / `markPublished` / `markPublishFailure` |
| 积压与最老年龄 | `outbox.go` `Backlog()` |
| 消费循环与位移 | `internal/logic/flashsale/kafka_consumer.go` `Run` / `consumeBatch` / `handleMessage` |
| 重试预算与终态 | `internal/logic/flashsale/consume.go` `ConsumeRequest` / `consumeLockedRequestInTx` / `persistConsumeFailure` |
| 死信顺序 | `kafka_consumer.go` `deadLetter` / `publishDLQ` |
| 幂等闸门 | `consume.go` 中 `if r.Status != requestStatusQueued { duplicate }` |
| 消费者客户端语义 | `internal/mq/kafka/consumer.go` |
| 生产者可靠性参数 | `internal/mq/kafka/producer.go` |

---

## 11. 故障恢复的关键机制（4 道防线）

```text
① Outbox（MySQL 即队列）
     事件在事务里落库 → 崩溃/停机都不丢；恢复后按 next_publish_at 重发
② 手动位移 + 先业务后位移
     业务没提交就不提交位移 → 崩溃后消息重投，最多重复、绝不丢失
③ 业务幂等闸门（MySQL 请求行状态）
     重复投递看到 status != 0 直接跳过 → 重复消息不产生重复订单/重复扣库存
④ 死信兜底（DLQ）
     永久错误与重试预算耗尽不再无限重试，落到 dlq + dead 终态，可人工重放
```

四者的分工：①防"生产端丢"，②防"消费端丢"，③防"重复造成的业务错"，④防"毒消息堵死分区"。

---

## 12. 真实验证结果（一句话版）

| 实验 | 结果 |
|---|---|
| A 积压 | lag 0→20→0；暂停期间 20 条全 queued，恢复后 20 单；Broker 全程存活 |
| B Broker 故障 | 停机 59s、HTTP 200、backlog 0→10→0、lag NA→0、18s 恢复、10 单零丢失 |
| C-dup 重复投递 | duplicate=3，订单/库存不变（幂等闸门生效） |
| C-crash SIGKILL | committed 120→130，lag 10→0，10 单不重不漏 |
| D-1 永久错误 | 4 类原因全部进 DLQ，source_offset 精确可追溯，DB 零写入 |
| D-2 预算耗尽 | 死信 + dead 终态 + Redis 补偿，不建单 |
| 最终一致性 | LOG-END=COMMITTED=136、lag 0、queued 0、无重复、无超卖 |

**发现缺陷 1 个**：`surgecart_outbox_oldest_age_seconds` 在 +08:00 会话下恒定多报 8 小时
（真实 103s 被报成 28903.5s），根因是 `NOW(6)`(+08) 与 `created_at`(UTC 墙钟) 混用。
未修复（任务不允许改业务/监控实现），已记录复现步骤与建议。

---

## 13. 尚未验证的能力（不要吹）

| 能力 | 状态 | 为什么 |
|---|---|---|
| "MySQL 已提交、位移未提交"毫秒级窗口 | NOT_VERIFIED | 无注入点、不可复现；只证明了窗口两侧（不丢 / 幂等） |
| `retry_count` 由真实技术失败推进 | 未在集群验证 | 需停 MySQL，会同时打挂 HTTP 与 Outbox |
| 多分区 / 多副本 / 多消费者实例 | 未覆盖 | 本地 Topic 1 分区、RF=1、单副本 |
| 分区再平衡过程中的在途消息 | 未覆盖 | 单实例不触发 rebalance（除配额踢出） |
| Broker 数据损坏 / 长时间停机 | 未覆盖 | 只做 59s scale-to-0 |
| Outbox "ACK 成功但 published_at 写失败" | 未在集群验证 | 只有单元测试钩子 `outboxBeforeMarkHook` |
| `db_scanner` 模式的重试语义 | 未覆盖 | 本任务只验证 kafka 模式 |

---

## 14. 面试时值得掌握的技术知识点

### 14.1 为什么 Kafka 用**手动** Offset Commit？

因为"拉取位置"和"处理完成"是两件事。自动提交提交的是拉取位置，
进程在处理到一半崩溃时会丢掉"已拉未处理"的那部分消息。手动提交把"确认"的权利交给业务，
让"业务副作用已持久化"成为提交位移的前提。代价是：必须自己处理提交失败、
必须保证异常路径不提交、必须接受重复投递。

### 14.2 为什么消息可能重复消费？

至少三条路径，本项目全都存在：

1. **处理成功但位移未提交时崩溃** → 重启后 Broker 从旧位移重投；
2. **Outbox 重复发布**：租约过期被他人重新领取、`published_at` 标记失败（Broker 已 ACK）；
3. **rebalance**：分区被收回时未提交的批次会被新持有者重新消费。

对策不是"消灭重复"，而是**让重复无害**：MySQL 请求行状态闸门 + 唯一约束 + 条件扣库存。

### 14.3 为什么不能简单保证 Exactly Once？

- Kafka 事务（`sendOffsetsToTransaction`）只能覆盖"Kafka 消费 → Kafka 生产"的闭环；
  本项目是"Kafka 消费 → MySQL 写入"，MySQL 不参与 Kafka 事务；
- 跨系统 exactly-once 需要分布式事务或两阶段提交，代价（性能、可用性、复杂度）远大于收益；
- 本项目选择 **at-least-once + 幂等消费**：数学上是"至少一次"，业务效果上等价"恰好一次"。

一句话：**Exactly Once 的正确说法是"至少一次投递 + 幂等处理"**。

### 14.4 Outbox 如何避免"DB 提交成功但 Kafka 发布失败"造成的数据丢失？

关键在**方向**：把"事件"存在与业务同一个数据库事务里。
`INSERT request` 提交 → 事件已经是持久化事实，发布只是"投递"而非"创造"。
再配上三条纪律：领取事务先提交再发 Kafka、只在 ACK 后写 `published_at`、
失败只写 `next_publish_at` 不改业务状态。于是任何时刻崩溃，事件都能被重新发现并重发。

### 14.5 Consumer 恢复时如何利用 Offset 继续消费？

消费组把 `(group, topic, partition) → committed offset` 存在 `__consumer_offsets`。
重启后 JoinGroup/SyncGroup 拿到分区，从 committed offset 开始 fetch。
本项目还有两个细节：

- `BlockRebalanceOnPoll()` 保证"已 Poll 未处理"的批次不会在提交前被抢走；
- `superviseConsumer` 在消费循环意外退出时**按有界退避重建客户端**（1s→30s），
  重建后复用同一个 ConsumerGroup，未提交位移由 Broker 重投。

### 14.6 消息积压为什么不等于消息丢失？

积压 = `log-end − committed`，是"已持久化但未确认消费"的数量。
只要 ①消息还在 Broker 的分区日志里（未过期、未被删除）、②消费组位移没有越过它们，
积压就是**可恢复的**。实验 A 的 20 条积压最终全部变成订单，就是直接证据。
反过来，"Lag=0"也不等于"没丢"：如果位移被错误地提交到超前位置，Lag 也是 0 但消息已经丢了。
**所以监控要同时看 Lag、已提交位移和业务终态分布。**

### 14.7 为什么最终业务一致性比单次 HTTP 请求成功更重要？

HTTP 200 只代表"已受理"（本项目返回 `queued`），秒杀是**异步**的：
请求要经过 Outbox → Kafka → Consumer → MySQL 事务才成交。
因此验收标准不能是"接口成功率"，而是**终态收敛**：

- 没有请求永久停在 `queued`；
- 订单数 == 成功请求数，且无重复 `order_no`、无同用户多单；
- `sold <= total_stock`；
- Outbox `published_at IS NULL` 的记录数归零；
- Kafka Lag 归零。

本任务多次注入故障后，这 5 条全部成立 —— 这才是"消息没丢、业务没错"的证据。

### 14.8 附：几个容易说错的细节

- `CommitRecords` 提交的是**下一条**位置（offset+1），不是"最后处理的那条"；
- `BlockRebalanceOnPoll` 必须配对 `AllowRebalance`，漏了会让**整个消费组永久卡在再平衡**；
- `acks=all` 是幂等 producer 的前提（Kafka 强制要求）；
- 消费者 lag 指标通常是**异步采样**的，用它做告警要留出采样+抓取延迟；
- 客户端配额（`consumer_byte_rate`）会连心跳一起限流，导致成员被踢出组。

---

## 15. 附：本任务产出的脚本与用法

```powershell
# 环境自检 + 快照
./scripts/kafka-reliability/snapshot.ps1 -Phase baseline

# 实验 A：消费积压（先在 Broker 侧用配额把消费压到 1 B/s）
./scripts/kafka-reliability/exp-a-consumer-backlog.ps1 -ActivityId <id> -SkuId <sku>

# 实验 B：Broker 故障（scale deploy/kafka 0→1）
./scripts/kafka-reliability/exp-b-broker-outage.ps1 -ActivityId <id> -SkuId <sku>

# 实验 C：重复投递 / SIGKILL 恢复（支持续跑）
./scripts/kafka-reliability/exp-c-consumer-restart.ps1 -DupActivityId <id> -DupSkuId <sku> `
    -CrashActivityId <id> -CrashSkuId <sku>
#   续跑：-SkipDup / -SkipCrash / -SkipProduce / -DupReplayOffsets "94,95,96"

# 实验 D：DLQ 与重试预算
./scripts/kafka-reliability/exp-d-dlq-retry.ps1 -ActivityId <id> -SkuId <sku>
#   续跑：-SkipD1 / -ExistingRequestId <id> / -FinalizeRequestId <id>

# 第三阶段：最终一致性核验（只读）
./scripts/kafka-reliability/verify-final-consistency.ps1
```

公共约定（`lib.ps1`）：SQL 与 Kafka 消息体一律走"无 BOM 临时文件 + Start-Process 重定向"，
凭据只在 Pod 内部展开，宿主机侧从不读取明文。
