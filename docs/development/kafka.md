# Kafka 开发说明

本文面向本地开发与后续批次实现者，说明 SurgeCart 引入 Kafka 的职责边界、配置方式、
本地 KRaft 环境、Producer/Consumer 使用方式，以及各批次的功能边界。

> **当前状态（B4.1）**：Kafka 基础设施与消息契约（B1）、Request Outbox 发布器（B2/B2.1）、
> 业务 Consumer 核心（B3）与 **serve 集成/生命周期闭环（B4，见第 10 节）** 均已完成并有测试覆盖；
> **B4.1 已补齐真实 serve 进程级验收**（操作系统 SIGTERM、在途请求收敛、关闭顺序与数据一致性，
> 见 10.11）。
> **默认仍是 `dispatch_mode=db_scanner`**（只运行 MySQL 扫描器，不需要 Kafka）。
> 启用 Kafka 需要同时设置 `dispatch_mode=kafka` 与 `kafka.enabled=true`，并按第 5 节显式创建 Topic；
> 两种模式严格互斥，切换需重启进程。单节点 KRaft 只用于本地联调，不代表生产高可用。

## 1. Kafka 在 SurgeCart 中承担什么职责

Kafka 只承担「已受理的秒杀请求需要异步落单」这一事件的**传输与分发**：

- 不承载订单、库存、请求生命周期等业务状态；
- 不替代 MySQL 的权威地位，也不改变 Redis 闸门的准入语义；
- 消费方收到事件后必须按 `request_id` 回到 MySQL 读取权威数据再决定是否落单；
  读取必须发生在订单事务内并对请求行加锁（见 8.5），不能先做事务外普通 `SELECT` 判断状态。

它要解决的问题是现有 MySQL 出队表的吞吐与解耦上限：当前消费由单实例
`goroutine + ticker` 轮询（默认 1s 周期、每轮限量）驱动，消费能力受轮询周期与行锁竞争约束。
引入 Kafka 后，消费组可按分区并行消费，生产与消费进程解耦，并可观测 Consumer Lag。

引入 Kafka **不会**削弱既有五个业务不变量（库存 ≥ 0、成功订单数 ≤ 初始库存、一人一单、
失败不建单、重复请求不重复扣减）。这些不变量仍由 MySQL 条件扣减 + 唯一约束兜底。

## 2. 为什么采用 Request 表兼作 Outbox

后续批次（B2）的目标方案是：让 `flash_sale_order_requests` 这一行**同时充当受理事实与 Outbox 记录**，
而不是再建一张独立的 Outbox 表。原因：

1. **受理与事件同事务**：`INSERT ... status=queued` 本身就是「事件已产生」的事实。
   再写一张 Outbox 表意味着同事务双写两行，增加写放大与维护成本，却没有增加业务事实。
2. **载荷可确定性重建**：`RequestedEvent` 的所有字段都能从 request 行（及活动/SKU 绑定）推导，
   `event_id` 由 `request_id` 确定性生成，不需要在发送时临时拼装随机标识。
3. **重复投递可被识别**：同一 `request_id` 重复发布得到相同 `event_id`，
   消费方可用它关联日志、追踪与识别 Kafka 重复投递；但业务幂等必须回到
   MySQL 请求行状态，不能把 `event_id` 当永久去重台账（原因见 8.5）。
4. **避免双写不一致**：独立 Outbox 表需要额外的清理、归档与「已发布」状态维护；
   request 行本来就有 `status` 生命周期，增加发布标记的复杂度更低。

B2 需要补充的是**发布标记/扫描索引**（例如「未发布的 queued 请求」查询能力）与发布器本身；
B1 不新增任何 migration、不改 request 表结构，也不实现发布逻辑。

## 3. Redis、MySQL、Kafka 的职责边界

| 数据 / 能力 | 权威来源 | Redis | Kafka |
| --- | --- | --- | --- |
| 秒杀订单是否成功 | MySQL `flash_sale_orders` 行 | 不承载 | 不承载 |
| 秒杀已售库存 | MySQL `flash_sale_activity_skus.sold` | 派生 `remaining`，对账收敛 | 不承载 |
| 请求生命周期 | MySQL `flash_sale_order_requests.status` | 派生去重/幂等标记 | 不承载业务状态 |
| 受理事件 | MySQL（受理行即事件事实，B2 增加发布标记） | 无 | 仅传输，不持久化业务事实 |
| 准入限流/闸门 | Redis Lua（加速与保护） | 预扣 + 标记 | 不参与 |

一句话：**MySQL 是唯一致信来源，Redis 是可收敛的加速层，Kafka 是可重放的传输管道。**
因此消息中不得携带秒杀价格、当前库存、已售数量、Redis `remaining` 作为落单依据。

## 4. 本地 Kafka 启动方式

本地使用官方 Apache Kafka 镜像的 **KRaft 单节点**（Broker + Controller 合一，无 ZooKeeper），
Compose 服务名为 `kafka`，并通过 `profiles: ["kafka"]` 与默认启动隔离——
`docker compose up -d` 仍然只启动原有的 MySQL/Redis/应用/Prometheus，不会拉起 Kafka。

```bash
# 仅启动 Kafka（不影响其他服务）
docker compose --profile kafka up -d kafka

# 查看容器与健康状态
docker compose --profile kafka ps kafka
docker inspect -f '{{.State.Health.Status}}' my-shop-kafka
```

说明：Compose 会对整个文件做变量插值，因此执行上述命令时仍需 `.env`（`make init` 生成）
或显式导出 `MYSQL_ROOT_PASSWORD` / `AUTH_JWT_SECRET`；这与 Kafka 本身是否启用无关。
显式指定服务名（`docker compose up -d kafka`）同样会启用其所在 profile。

健康检查使用 Kafka 自带 CLI 调用 Broker API，而不是只探测端口：
端口可连但 KRaft 尚未完成选主时，生产/消费依然会失败。

数据保存在命名卷 `kafka_data`（容器内 `/var/lib/kafka/data`），容器重建不丢 Topic 元数据与消息。
若要彻底重置本地开发数据：

```bash
docker compose --profile kafka down
docker volume rm my-shop_kafka_data
```

## 5. Topic 创建方式

Topic **不依赖 Broker 自动创建**（`KAFKA_AUTO_CREATE_TOPICS_ENABLE=false`），必须显式创建。
本地开发统一 12 分区、副本数 1：

```bash
# 正常事件 Topic
docker exec my-shop-kafka /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server kafka:9092 \
  --create --if-not-exists \
  --topic surgecart.flashsale.order.requested.v1 \
  --partitions 12 --replication-factor 1

# 死信 Topic
docker exec my-shop-kafka /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server kafka:9092 \
  --create --if-not-exists \
  --topic surgecart.flashsale.order.requested.v1.dlq \
  --partitions 12 --replication-factor 1

# 校验分区与副本
docker exec my-shop-kafka /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server kafka:9092 --describe --topic surgecart.flashsale.order.requested.v1
```

分区数决定消费组内最大并行度：同一消费组内一个分区只能分配给一个消费者，
12 分区最多支撑 12 个消费者实例并行（更多实例会空闲）。

## 6. Kafka Broker 地址

| 客户端位置 | 地址 | 说明 |
| --- | --- | --- |
| 宿主机进程（本地 `go run`/`my-shop serve`） | `127.0.0.1:29092` | Compose 端口映射到宿主机回环地址 |
| Compose 网络内（app 容器等） | `kafka:9092` | Compose DNS 名，走内部监听器 |
| KRaft Controller | `kafka:9093` | 仅 Broker 内部使用，不对外暴露 |

两个监听器分别广播不同的 `advertised.listeners`：客户端拿到的地址必须与它实际能访问的地址一致，
否则会出现「能连上 Broker 却无法拉取元数据/收发消息」。

从宿主机验证（宿主机无需安装 Kafka CLI，可借用官方镜像）：

```bash
docker run --rm --network host --entrypoint /opt/kafka/bin/kafka-topics.sh \
  apache/kafka:3.9.1 --bootstrap-server 127.0.0.1:29092 --list
```

## 7. 配置项说明

配置位于 `manifest/config/config.yaml` 的 `flash_sale` 节点：

```yaml
flash_sale:
  dispatch_mode: "db_scanner"   # db_scanner | kafka（互斥，切换需重启）
  shutdown_timeout: "30s"       # 秒杀后台组件统一关闭预算
  kafka:
    enabled: false
    brokers: "127.0.0.1:29092"
    topic: "surgecart.flashsale.order.requested.v1"
    dlq_topic: "surgecart.flashsale.order.requested.v1.dlq"
    consumer_group: "surgecart-flashsale-order-worker-v1"
    publisher:
      batch_size: 100
      poll_interval: "200ms"
      error_backoff: "2s"
```

| 配置项 | 环境变量 | 说明 |
| --- | --- | --- |
| `flash_sale.dispatch_mode` | `FLASH_SALE_DISPATCH_MODE` | `db_scanner`（默认）或 `kafka`；其他值启动失败 |
| `flash_sale.shutdown_timeout` | `FLASH_SALE_SHUTDOWN_TIMEOUT` | 停止 Publisher/Consumer/Scanner 的总预算；超时记录明确日志 |
| `flash_sale.kafka.enabled` | `FLASH_SALE_KAFKA_ENABLED` | Kafka 总开关，默认 `false`；必须与 `dispatch_mode` 一致 |
| `flash_sale.kafka.brokers` | `FLASH_SALE_KAFKA_BROKERS` | 逗号分隔的 `host:port`，自动去空格；拒绝空项、重复项、非法端口 |
| `flash_sale.kafka.topic` | `FLASH_SALE_KAFKA_TOPIC` | 正常事件 Topic，启用时必填 |
| `flash_sale.kafka.dlq_topic` | `FLASH_SALE_KAFKA_DLQ_TOPIC` | 死信 Topic，启用时必填且不得与 `topic` 相同 |
| `flash_sale.kafka.consumer_group` | `FLASH_SALE_KAFKA_CONSUMER_GROUP` | 消费组名，启用时必填 |
| `flash_sale.kafka.publisher.batch_size` | `FLASH_SALE_KAFKA_PUBLISHER_BATCH_SIZE` | Outbox 单轮领取上限，默认 100，硬上限 1000 |
| `flash_sale.kafka.publisher.poll_interval` | `FLASH_SALE_KAFKA_PUBLISHER_POLL_INTERVAL` | 发布轮次最小间隔（Go duration），默认 `200ms` |
| `flash_sale.kafka.publisher.error_backoff` | `FLASH_SALE_KAFKA_PUBLISHER_ERROR_BACKOFF` | 无进展时的退避起点（Go duration），默认 `2s`，指数增长、30s 封顶 |

校验规则要点：

- `enabled=false` 时不要求 Kafka 配置齐全，但**已填写的值仍会校验**，避免配置错误被开关掩盖；
- `enabled=true` 时 Broker 至少一个，Topic/DLQ/消费组必填，错误信息会指出具体配置项；
- `enabled` 必须是合法布尔值；`"ture"` 这类拼写错误会直接报错，而不是被静默当成 `false`；
- **模式互斥**：`dispatch_mode=kafka` 必须 `enabled=true`；`dispatch_mode=db_scanner` 必须
  `enabled=false`。任一矛盾都在启动期 fail-fast，不允许静默退回另一种模式；
- 默认 `db_scanner + enabled=false` 不创建任何 Kafka 客户端/后台循环，本地开发无需安装 Kafka。

**配置读取注意**：GoFrame 的 `GetEffective` 会把配置键中的 `_` 归一化为 `.`
（`flash_sale.kafka.enabled` 变成 `flash.sale.kafka.enabled`），无法命中本项目的 `flash_sale` 命名空间。
因此 `internal/mq/kafka/config.go` 采用「环境变量优先 + `cfg.Get` 读取配置文件」的方式，
并用测试覆盖「文件值生效」和「环境变量覆盖文件值」两条路径，而不是只凭命名推断。

默认值保持不变：`dispatch_mode=db_scanner`、`kafka.enabled=false`，现有业务行为不受影响。

## 8. Producer / Consumer 使用方式

通用契约在 `internal/mq/mq.go`（`Message`/`Publisher`/`Consumer`/`Handler`/`Disposition`），
Kafka 实现在 `internal/mq/kafka`。业务代码只依赖通用契约，不直接依赖 franz-go。

### 8.1 Producer

```go
cfg, err := kafka.Load(ctx)
if err != nil {
    return err
}
producer, err := kafka.NewProducer(cfg)
if err != nil {
    return err
}
defer producer.Close(ctx)

payload, err := event.Encode()
if err != nil {
    return err
}
results, err := producer.Publish(ctx, mq.Message{
    // 显式主题：调用方必须自己指定，不要依赖 Producer 的默认 Topic（见 8.4 / F-02）。
    Topic: "surgecart.flashsale.order.requested.v1",
    // 分区键固定为 <activity_id>:<sku_id>，同一活动 SKU 进入同一分区（见 8.4）。
    Key:   []byte(strconv.FormatInt(event.ActivityID, 10) + ":" + strconv.FormatInt(event.SkuID, 10)),
    Value: payload,
})
for _, r := range results {
    if r.Err != nil {
        // 单条失败：不要标记为已发布，交给上层重试
    }
}
```

可靠性参数：`acks=all`、幂等写入、重试 5 次 + 100ms~2s 有界指数退避、投递超时 30s、
单次 Produce 请求超时 10s、linger 5ms、批次 64KB、zstd 压缩、本地缓冲上限 1 万条 / 64MB。
`Publish` 同步等待逐条确认，返回结果与入参顺序一致，部分失败不会被伪装成整批成功。

**F-03（B4）后的超时语义**：`ctx` 到期时不再丢弃整批结果，而是返回等长的逐条结果：

- 已获得 ACK 的条目 `Err == nil`，保留 partition/offset 证据；
- 明确失败的条目保留具体错误；
- 尚未收到回调的条目包装 `mq.ErrPublishUnknown`（只能按「可能已发送」重试）。

返回后已入队的消息仍可能由驱动在后台投递（最迟受 30s 投递超时约束），因此下游仍必须幂等；
但调用方不能再把「超时」当成「整批未知」而重发全部消息。

### 8.2 Consumer

```go
consumer, err := kafka.NewConsumer(cfg)
if err != nil {
    return err
}
defer consumer.Close(ctx)

messages, err := consumer.Poll(ctx, 100)
if err != nil {
    return err
}
// 逐条处理；只有业务副作用持久化后才允许提交
if err := consumer.Commit(ctx, messages...); err != nil {
    return err
}
consumer.AllowRebalance()
```

关键约束：

- 构造时设置 `DisableAutoCommit`，**不会自动提交位移**；
- `BlockRebalanceOnPoll`：`Poll` 返回的批次处理完必须调用 `AllowRebalance`，
  否则后续 rebalance 会一直等待；
- 允许重复投递（位移提交前崩溃、rebalance 都会重投），消费逻辑必须幂等；
- franz-go 没有 `max.poll.interval.ms` 客户端参数（心跳由后台协程维持），
  等价约束必须由消费循环保证：单批处理时间有界，处理完尽快 `AllowRebalance`；
- 上例只演示接口用法。**完整的业务消费循环**（解析校验、重试预算、死信、数据库事务与位移提交
  顺序、Rebalance 生命周期）见第 9 节与 `internal/logic/flashsale/kafka_consumer.go`。

### 8.3 处置语义（B1 只冻结接口）

`mq.Disposition` 明确三种结论：

| 处置 | 含义 | 位移 |
| --- | --- | --- |
| `DispositionAck` | 业务成功且副作用已持久化 | 允许提交 |
| `DispositionRetry` | 可重试失败（如 DB 暂时不可用） | 不提交，有限次数/有界退避后重试，超预算升级为死信 |
| `DispositionDeadLetter` | 不可重试或重试耗尽 | 先写死信主题，成功后再提交原位移 |

消费循环本身不在 B1 范围内（B3 实现）。

### 8.4 Kafka Message Key 策略（B2.1 冻结）

`RequestedEvent` 的 Kafka Message Key 固定为 `<activity_id>:<sku_id>`，**不改用 `request_id`**。

理由：

1. 同一活动 SKU 的消息进入同一 Partition；
2. 保持同一活动 SKU 的分区内处理顺序；
3. 避免多个 Consumer 同时争抢同一个 SKU 的库存记录；
4. 让 Kafka 分区策略与秒杀库存资源的组织方式保持一致（库存行与 Redis 计数都按 activity+sku 组织）。

必须明确这一策略的代价：

- 若某个秒杀活动只有一个热门 SKU，该 SKU 的消息会集中到一个 Partition；
- 即使 Topic 配置了 12 个 Partition，热门 SKU 也不能自动利用全部 12 个 Partition 的消费并行度；
- 单 SKU 的消费并行度上限就是 1，这是当前明确接受的架构取舍，不在文档中隐瞒。

顺序边界同样必须说清：

- Kafka 只保证**同一 Partition 内的追加顺序**；
- 多 Publisher 并发发布时，Kafka 消息顺序与 MySQL `request_id` 顺序**不一致**；
- 不能把 Kafka 分区顺序当作数据库事务正确性的保证——正确性仍由 MySQL 行锁、
  条件更新与唯一约束保证。

后续通过真实压测比较不同分区策略（按 activity、按 request_id 等），不在 B2.1 擅自修改。

### 8.5 event_id 与业务幂等契约（B2.1 冻结）

`event_id` 形如 `flashsale.requested:<request_id>`，由 `request_id` 确定性推导。

它承担三件事：

1. 标识逻辑事件；
2. 关联日志、追踪与消息排查；
3. 识别 Kafka 重复投递（at-least-once、Outbox 重发、租约过期回收）。

它**不承担**的事：event_id 只标识逻辑事件，不是业务处理完成的凭证，
不能作为永久去重台账的键。

原因是人工修复语义：`dead → queued` 会重置发布状态并重新投递同一条请求，
重新发布的事件与修复前那条旧事件 event_id 完全相同（B2.1 不引入 repair_generation，
也不修改 event_id 生成算法）。若消费方维护永久 event_id 消费台账，
修复后的合法请求会被当成重复消息直接丢弃。

真正的业务幂等边界是 MySQL 请求行状态。消费方（B3）必须：

1. 在开启订单事务之后重新读取 `flash_sale_order_requests` 记录；
2. 锁定请求行（`SELECT ... FOR UPDATE`，或出队场景的 `FOR UPDATE SKIP LOCKED`）；
3. 检查当前 `status`；
4. 仅 `queued` 的请求才执行订单业务；
5. `success` / `failed` / `dead` 等终态请求一律跳过，不得重复执行业务副作用；
6. 订单唯一约束与库存条件更新继续作为数据库层面的最后保护。

禁止「事务外普通 SELECT 判断状态，再执行订单创建」：两次读取之间状态可能被其他
消费者或人工修复改变，event_id 去重台账无法弥补这个竞态。

## 9. B3 业务 Consumer 实现

> B3 只交付「业务消费核心」，不包含服务启动装配与 dispatch_mode 切换；
> 这两项已由 B4 补齐（见第 10 节）。本节保留 B3 的组件语义与事务边界说明。
> 因此本节的组件在当前代码里存在并有测试覆盖，但 `serve` 默认仍然只启动 MySQL Scanner。

### 9.1 组件与调用链

| 组件 | 位置 | 职责 |
| --- | --- | --- |
| `RequestedEventConsumer` | `internal/logic/flashsale/kafka_consumer.go` | 拉取、解析校验、重试/DLQ、位移提交、再平衡生命周期 |
| `sFlashSale.ConsumeRequest` | `internal/logic/flashsale/consume.go` | 按 `request_id` 执行订单事务（Kafka 入口） |
| `sFlashSale.ConsumeQueued` | `internal/logic/flashsale/consume.go` | 原 MySQL Scanner 入口（出队） |
| `consumeLockedRequestInTx` | `internal/logic/flashsale/consume.go` | 两条入口共享的订单事务核心 |
| `RebalanceConsumer` | `internal/logic/flashsale/kafka_consumer.go` | `mq.Consumer` + `AllowRebalance` 的组合约束 |

调用链（顺序消费，默认 `BatchSize=1`）：

```text
Poll（BlockRebalanceOnPoll）
  → parseRequestedEvent（JSON / schema_version / event_type / 字段 / 消息头）
  → ConsumeRequest
       BEGIN
         SELECT request FOR UPDATE
         身份比对（消息字段 vs MySQL）
         status != queued  → duplicate（安全跳过）
         retry_count > 预算 → dead（不再执行业务）
         resolveSku → SAVEPOINT → 建单 → 条件扣库存 → 状态更新
       COMMIT
  → 终态失败 → Redis 补偿（提交后执行）
  → Kafka Commit
  → AllowRebalance
```

为什么 Kafka 用 `FOR UPDATE` 而不是 Scanner 的 `FOR UPDATE SKIP LOCKED`：
Scanner 是「抢一条没人处理的行」，跳过被锁行是正确行为；Kafka 是「必须处理这一条」，
跳过锁行等于把处理结论建立在旧快照上。等待锁之后读到的是最新已提交状态，
并发的第二个消费者因此会看到终态并安全跳过。两条路径最终进入同一个事务核心，
库存扣减与建单逻辑只有一份。

### 9.2 消息校验与权威数据

校验顺序固定为：JSON → `schema_version` → `event_type` → 必填字段/身份推导 → 消息头一致性。
任何一项失败都是**永久错误**：进入死信、提交位移，不调用业务、不改数据库。
永久错误的原因被细分为低基数枚举（`parse_error` / `schema_version` / `event_type` /
`invalid_field` / `identity_mismatch` / `request_missing`），既用于指标标签，也写入死信头。

`user_id` / `activity_id` / `sku_id` / `idempotency_key` / `request_hash` 只用于与 MySQL
请求行比对；订单价格、库存、已售数量一律在事务内重新读取。身份不一致时**不改数据库**：
消息不可信，不能据此改写权威请求行。

### 9.3 事务边界

订单事务只在两种情况下写业务终态：

```text
BEGIN
  SELECT Request FOR UPDATE                          -- 行锁：并发消费者串行化
  status != queued            → 不改任何行，返回 duplicate
  retry_count > 3             → 不改任何行，返回 dead（等调用方先死信）
  resolveSku 失败（业务）      → status = failed, last_error_code
  SAVEPOINT flashsale_consume_order
  建单 + 条件扣减 sold
  任一失败                    → ROLLBACK TO SAVEPOINT → status = failed
  成功                        → status = success, flash_order_id
COMMIT
```

三个关键点：

1. **保存点**：建单与扣库存必须同生共死。历史实现把「建单失败」的 error 转成状态更新后
   继续提交同一事务，一旦扣减分支失败就会残留「有订单但没扣库存」。B3 用保存点把这对
   写操作回滚干净，再写终态。
2. **技术失败不写终态**：连接中断、死锁、事务提交失败、Redis 不可用都不属于业务结论。
   业务事务整体回滚（不写 `failed`、不写 `retry_count`），重试预算由**独立短事务**推进
   （`persistConsumeFailure`）。这样既不会把基础设施故障固化成业务失败，也不会让半成品
   随状态更新一起提交。
3. **重试预算与终态分离**：预算耗尽时业务事务不改行，只由调用方在死信 ACK 之后落 `dead`
   （见 9.5 的顺序约束）。

`retry_count` / `next_attempt_at` 的真实语义是「该请求已消耗的技术重试次数」与
「下一次允许尝试的时间」，由 Scanner 与 Kafka Consumer 共同使用；`publish_attempts` /
`next_publish_at` 属于 Outbox 发布重试，与消费无关，B3 **不复用**。因此本批次不需要新增
migration。

### 9.4 Offset 提交语义

严格顺序：**MySQL COMMIT 先于 Kafka Offset COMMIT**。

```text
Poll → 业务事务 → MySQL COMMIT → Kafka Commit → AllowRebalance
```

批次内顺序处理，成功一条进入待提交集合；一旦某条消息需要「停止本轮」（技术错误未收敛、
死信未 ACK、无法持久化重试预算、位移提交失败），立即停止本批：

- 只提交**该消息之前**已确认的消息（同分区只提交最大 offset+1）；
- 绝不提交失败消息及其后同分区消息的位移；
- 位移提交失败时不吞错误：`Run` 返回错误并停止拉取，未提交的消息在重启后重投。

故障窗口与预期结果：

| 故障场景 | 实现行为 | 结果 |
| --- | --- | --- |
| MySQL 事务失败 | 不提交位移 | 消息重投，请求仍 queued，可重试 |
| MySQL COMMIT 成功、Offset 提交失败 | `Run` 报错退出，位移未推进 | 消息重投 → 请求已 success → duplicate → ACK，不重复落单 |
| 消费过程中进程崩溃 | 未提交位移 | 重启后重投，状态闸门保证只落一单 |
| 相同消息重复投递 | 行锁 + status 闸门 | duplicate，安全 ACK |
| 请求已终态 | status 闸门 | duplicate，安全 ACK |

### 9.5 Retry、退避与 DLQ

错误分类：

| 类别 | 例子 | 处置 |
| --- | --- | --- |
| 永久错误 | 非法 JSON、未知版本/类型、字段非法、身份不一致、请求不存在 | 死信 + 提交位移 |
| 业务失败 | 库存不足、时间窗结束、已购、幂等冲突 | 写 `failed` + 提交位移 |
| 技术错误 | MySQL 短暂不可用、死锁、提交失败、Redis 不可用 | 持久化重试预算 → 退避重试；预算耗尽 → 死信 + `dead` |

技术错误的重试预算持久化在 `retry_count` 上，预算是 3 次：

- Kafka 路径退避：1s → 2s → 4s（封顶 5s），累计不超过 7s，远低于 30s 会话超时，
  因此不会长时间阻塞分区再平衡；
- Scanner 路径保持原有 5s → 10s → 20s（封顶 60s）；
- 每次失败都写 `retry_count+1` 与 `next_attempt_at`，进程重启后继续累加，不会归零；
- 本地还保留一份 `MaxDeliveryAttempts`（默认 4）兜底：连重试次数都写不进去时
  （典型是 MySQL 不可用），只做有界本地退避后停止，**绝不因为本地计数达到阈值就丢弃消息**，
  位移保持未提交，由重启后的重投继续尝试。

预算耗尽后的顺序不可交换：

```text
1. 发布原始消息到 DLQ（同步等待 Broker ACK）
2. MySQL: status = dead（条件更新 WHERE status=queued）
3. Redis 补偿（convergeStockAndMarkers）
4. Kafka Commit 原始位移
```

- DLQ 未 ACK → 不提交位移，消息会重投；
- DLQ 已 ACK 但 `dead` 写入失败 → 不提交位移，消息重投（死信可能重复，允许），
  原始业务请求不会丢。

死信消息头（低基数诊断信息，Value 原样搬运以便重放）：
`dlq_reason`、`dlq_error`、`dlq_source_topic`、`dlq_source_partition`、
`dlq_source_offset`、`dlq_retry_count`；Key 仍为 `<activity_id>:<sku_id>`。

### 9.6 Redis 一致性

Kafka 消费不新建第二套 Redis 库存体系，完全复用既有机制：

- 终态失败（`failed`/`dead`）在 MySQL 提交后调用 `convergeStockAndMarkers`：
  `remaining` 用**权威值 SET**（`total_stock - sold - inflight_queued`）、
  一人一单/幂等标记用幂等 `DEL` 清除；
- 不使用 `INCR` 回补：重复消费、Kafka 重投、崩溃重放都会再次进入补偿，只有按权威值
  `SET` 才能保证「不重不漏」；
- 补偿失败只记录日志，不阻塞消费（MySQL 终态已经正确）：库存由对账扫描器
  （`ReconcileCache`）按权威值收敛，标记由 TTL 自愈；
- 已 success 的请求再次投递时走 duplicate 分支，**不执行任何 Redis 副作用**。

### 9.7 Rebalance 与位移安全

- B1 客户端固定 `BlockRebalanceOnPoll`：Poll 返回批次后到 `AllowRebalance` 之前，
  分区不会被再平衡抢走；
- 消费循环用 `defer c.client.AllowRebalance()` 包住「处理 + 提交」，
  **包括错误路径与 context 取消**，避免组内永久阻塞；
- `Run` 在 `Poll` 返回空批次时也调用一次 `AllowRebalance`（不同驱动实现的批次计数不同，
  统一释放更安全）；
- 首版顺序消费、默认单条批量：把阻塞窗口压到「单条消息处理时间」，
  并保证同分区位移连续提交；
- 无界并发消费被明确禁止：并发会打破位移连续性，且无法保证失败消息之后的位移不提交。

### 9.8 指标

`internal/metrics` 新增（全部为进程内观测，非业务事实，标签均为固定枚举）：

| 指标 | 说明 |
| --- | --- |
| `flashsale_consumer_records_total` | 消费消息总数（含重复投递） |
| `flashsale_consumer_processed_total{result}` | 处置结果（success/failed/dead/duplicate/invalid/error） |
| `flashsale_consumer_retries_total` | 已持久化重试预算的技术失败次数 |
| `flashsale_consumer_dlq_total{reason}` | 死信投递成功数（按原因） |
| `flashsale_consumer_commit_failures_total` | 位移提交失败次数 |
| `flashsale_consumer_restarts_total` | 意外退出后的重建次数（正常 SIGTERM 不计数） |
| `flashsale_consumer_processing_seconds{result}` | 单条消息处理耗时 |
| `flashsale_outbox_mark_skipped_total{reason="lock_busy"}` | 标记遇到业务行锁（NOWAIT），等待租约恢复 |

业务侧仍复用既有的 `flashsale_requests_total{interface="consume", result=...}` 与耗时直方图。
不包含 `request_id` / `user_id` / `activity_id` 等高基数标签。

**Consumer Lag 未实现**：当前只依赖 `franz-go/pkg/kgo`（v1.18.1），
Lag 需要消费组元数据 API（`kadm`），为避免在 B3 引入新依赖并伪造数据，本批次不提供
Lag 指标，列为后续监控集成事项（可用 `kafka-consumer-groups.sh --describe` 人工核查）。

### 9.9 已知限制

- **技术错误无法 seek 重试**：B1 客户端没有暴露 `SeekPartitions`，同一进程内无法把位移
  拨回失败消息。当前策略是「持久化预算 + 停止消费循环」，由 B4 的 Consumer 监督循环
  按有界指数退避重建客户端；重启后从已提交位移重投，不会丢消息，但会带来额外的组再平衡开销。
- **默认单条批量**：牺牲吞吐换位移连续性，后续需基于真实压测决定是否提高批量。
- **单 SKU 单分区**：分区键 `<activity_id>:<sku_id>` 决定热点 SKU 的消费并行度上限为 1，
  这是 B2.1 已接受的架构取舍。
- **无 Lag / 无消费堆积告警**：见 9.8。
- **模式切换需重启**：B4 不实现运行中热切换，只能通过重启进程在 `db_scanner` / `kafka`
  之间切换（见第 10 节）。

### 9.10 测试与门禁

```bash
# 纯内存用例（无需 MySQL/Redis），已登记到 scripts/test-unit.sh
make test-unit

# 真实 MySQL/Redis 集成用例（含消费事务、终态幂等、并发、重试预算、DLQ）
DATABASE_DEFAULT_PASS=root go test -p 1 -count=1 -run 'TestConsumeRequest|TestConsumer' ./internal/logic/flashsale/

# 真实 Kafka 集成用例（需 Broker），建议指向一次性隔离库
docker exec -e MYSQL_PWD=root my-shop-mysql mysql -uroot \
  -e "CREATE DATABASE IF NOT EXISTS \`my_shop_b3_kafka\` CHARACTER SET utf8mb4"
FLASH_SALE_KAFKA_TEST_BROKER=127.0.0.1:29092 DATABASE_DEFAULT_PASS=root \
  DATABASE_DEFAULT_NAME=my_shop_b3_kafka \
  go test -p 1 -count=1 -run TestConsumerRealKafka -v ./internal/logic/flashsale/

# 并发/超卖/双入口竞态 + F-03/F-04（-race，隔离库，已纳入 scripts/test-race.sh）
make test-race

# B4 真实端到端（专用库/Redis DB/唯一 Topic；含 Kafka/MySQL 停机恢复）
make test-kafka-e2e
```

F-03/F-04 专项用例：

```bash
DATABASE_DEFAULT_PASS=root DATABASE_DEFAULT_NAME=my_shop_b4_test \
  go test -count=1 -v -run 'TestOutboxPartialAckTimeoutDoesNotAmplifyDuplicates|TestOutboxMarkDoesNotWaitBusinessRowLock' \
  ./internal/logic/flashsale/

go test -count=1 -v -run 'TestProducerPublishResults' ./internal/mq/kafka/
```

集成测试会清理 flashsale 与商品/SKU 域的表，**必须指向测试库**（`DATABASE_DEFAULT_NAME`），
不要对保存真实数据的实例执行；Race 门禁由脚本自建自删 `my_shop_outbox_race`；
端到端脚本拒绝 `my_shop` / `my_shop_staging` 等库名并自建自删专用库。

## 10. B4：serve 集成与运行闭环

### 10.1 两种模式

`serve` 在 `boot.Bootstrap` 之后、HTTP 监听之前调用
`worker.StartFlashSaleRuntime`，按 `flash_sale.dispatch_mode` 只装配一套消费入口：

| 模式 | MySQL Scanner | Outbox Publisher | Kafka Consumer | 订单取消 / 缓存对账扫描器 |
| --- | --- | --- | --- | --- |
| `db_scanner`（默认） | 启动 | 不启动 | 不启动 | 启动 |
| `kafka` | **不启动** | 启动 | 启动 | 启动 |

互斥由启动层代码保证（不是配置注释）：两种模式共用同一批 `status=queued` 请求行，
同时运行会争抢行锁并重复消耗 `retry_count` 预算。启动层测试直接断言实际创建/运行的组件集合
（`internal/worker/flashsale_runtime_test.go`）。

### 10.2 启动顺序

```text
serve
  └─ boot.Bootstrap（MySQL/Redis/schema/seed）
       └─ service.Upload().ValidateConfig
            └─ worker.StartFlashSaleRuntime
                 ├─ db_scanner: StartOrderCancelScanner + Reconcile + ConsumeScanner
                 └─ kafka:      Producer → OutboxPublisher → Consumer 客户端
                                → Publisher 发布循环 + Consumer 监督循环
                                + OrderCancelScanner + Reconcile
                     └─ g.Server().Run()（HTTP 入口）
```

Kafka 模式下，配置矛盾、Kafka 配置非法、客户端构造失败都会在监听端口之前 fail-fast。
客户端构造是惰性的：Broker 暂时不可用不会阻塞启动，故障会在发布/拉取结果中暴露并退避重试。

### 10.3 Outbox 后台发布循环

- 复用 B2 的 `OutboxPublisher.PublishBatch`，不重新实现领取/发布/标记逻辑；
- 每轮使用有界批次（`publisher.batch_size`，硬上限 1000）；
- 轮与轮之间至少等待 `poll_interval`，有数据时也不会无间隔空转；
- 本轮有进展（Published/Retried > 0）时保持正常节奏；本轮完全无进展才使用
  `error_backoff` 起步的有界指数退避（30s 封顶），避免一条坏记录拖慢整个积压；
- ctx 取消后当前轮次返回即退出；`Shutdown` 会等待该 goroutine 结束；
- Publisher 不负责业务状态迁移：它只写发布相关列，`status/retry_count/flash_order_id` 由 Consumer 维护。

### 10.4 Consumer 监督与异常恢复

Consumer 在独立 goroutine 中运行 `RequestedEventConsumer.Run`：

1. `Run` 返回后先判断 `ctx.Err()`：主动关闭直接退出，不重建；
2. 意外退出时先关闭旧客户端（`Close` 有界），**确认旧实例已停止后才创建新客户端**，
   保证同一时刻最多一个 Consumer 实例；
3. 重建使用同一 ConsumerGroup，从已提交位移继续；未提交位移的消息由 Kafka 重投，
   `retry_count/next_attempt_at` 从 MySQL 恢复，不依赖进程内状态；
4. 退避 1s 起步、30s 封顶，可被 ctx 取消；一次运行超过 1 分钟视为稳定并重置退避；
5. 重建次数记录 `flashsale_consumer_restarts_total`，正常 SIGTERM 不计数。

### 10.5 优雅关闭

`FlashSaleRuntime.Shutdown` 使用统一预算 `flash_sale.shutdown_timeout`（默认 30s）：

1. 取消 Publisher ctx，等待当前 `PublishBatch` 结束（不再领取新记录）；
2. 取消 Consumer ctx，等待 `Run` 返回；监督循环退出前关闭 Consumer 客户端；
3. 关闭 Producer（有界 Flush）；此后不会再有 Publish；
4. 取消根 ctx，等待订单取消/缓存对账扫描器退出。

任何一步超时都会记录 `flashsale_runtime_shutdown_timeout` 并继续执行后续步骤，
不会因单个组件挂起而永久阻塞其他资源释放。未提交的 MySQL 事务由数据库回滚；
未提交的 Kafka Offset 会在重启后重投。

每一步都会输出带 `elapsed_ms` / `timed_out` 的结构化事件（`flashsale_runtime_shutdown_begin` →
`flashsale_publisher_stopped` → `flashsale_consumer_stopped` → `flashsale_producer_closed` →
`flashsale_scanners_stopped` → `flashsale_runtime_shutdown_complete`），`serve` 在 HTTP drain
结束后输出 `http_server_stopped`；这些事件是进程级关闭时序的可观测证据。

**B4.1 修复：信号 ctx 不得作为 runtime 的父 ctx。** 原实现把 `signal.NotifyContext` 的 ctx
传给 `StartFlashSaleRuntime`，SIGTERM 会在信号到达瞬间同时取消 Publisher / Consumer / Scanner，
绕过上面的关闭顺序，并让正在进行的 `PublishBatch` 被就地中断（已领取记录只能等租约到期后重发）。
现在运行期使用 `context.WithoutCancel` 派生 ctx，关闭顺序只由 `Shutdown()` 编排；
HTTP drain 期间 Publisher/Consumer 仍在工作，drain 结束后再按序停止。

**BlockRebalanceOnPoll 的关闭语义**：Consumer 客户端 `Close` 先 `AllowRebalance` 再离组，
避免未处理完的 Poll 批次阻塞分区回收；`Run` 在批次处理完（含异常路径）通过 `defer AllowRebalance`
释放阻塞。B4 还修复了一个关键泄漏：`Poll` 返回错误/空批次时低层适配器必须自行
`AllowRebalance`，否则 Broker 恢复后的再平衡会永久等待（真实停机测试复现并回归）。

### 10.6 F-03：Producer 超时不再丢弃逐条结果

**原问题**：B1 `Producer.Publish` 在 context 超时时返回 `nil, error`，B2 只能把整批视为未知；
100 条中 99 条已 ACK 时，整批重试会把 99 条再次放大。

**修复**：`Publish` 改为按下标收集回调结果，超时返回等长快照：

- 已 ACK 保留成功与 partition/offset；
- 明确失败保留原错误；
- 未回调标记 `mq.ErrPublishUnknown`；
- 快照在互斥锁内复制，晚到回调只写内部状态，不能改写已返回结果；
- 用原子计数代替等待协程，回调永不触发时也不会遗留阻塞 goroutine。

Outbox 对这些结果逐条处理：ACK 的立即 `markPublished`，未知/失败的写入退避，
下一轮只重发未确认项。

### 10.7 F-04：发布标记不再等待业务行锁

**原问题**：`markPublished/markPublishFailure` 直接 UPDATE；Consumer 持有同一 Request 行锁时，
Publisher 可能等待 MySQL 默认锁等待（最长 50s），而租约只有 60s，导致租约过期后重复发布。

**修复**：

- 标记事务先执行 `SELECT ... FOR UPDATE NOWAIT`；取不到锁立即返回 `lock_busy`，
  不写任何列（尤其不写 `published_at`），记录保留未发布 + 原租约；
  该语法要求 MySQL 8.0+（本项目 Compose 与 CI 均为 mysql:8.0）；
- NOWAIT 的瞬时索引锁冲突在**整轮标记预算内**做至多 3 次、每次 20ms 的有界重试，
  不会退化为等待业务行锁；持续占用时仍走租约恢复路径；
- 取到锁后在同一事务内做条件 UPDATE，token 条件保证旧 Publisher 不能覆盖新租约；
- 领取阶段有 `ClaimTimeout`、标记阶段有整轮 `MarkTimeout` 总预算（默认 10s，不是单条预算）；
- 租约约束改为 `Lease > PublishTimeout + MarkTimeout + 10s 安全余量`，
  默认 60s > 20s + 10s + 10s；不靠调大租约掩盖锁等待；
- 锁被占用计入 `flashsale_outbox_mark_skipped_total{reason="lock_busy"}` 并输出
  `flashsale_outbox_mark_lock_busy` 日志；租约到期后恢复路径会重新发布（允许重复，绝不丢事件）。
- **终态窗口的投递凭证补写**：如果 Publisher 在 ACK 后写标记失败，而 Consumer 又先把请求
  推进到 success/failed，Outbox 的 claim 不会再选中该记录。Consumer 在消费事务内
  （身份校验通过、`published_at IS NULL`）用消息的 partition/offset 补写发布凭证，
  使「已消费」这一事实成为第二份 ACK 证据，避免记录永久停留在「已投递但未标记」。
  该补写只改发布列，不改 status/retry_count/flash_order_id。

### 10.8 模式切换

运行中不做热切换。切换步骤：

```bash
# 1) 停旧进程（SIGTERM，等待 shutdown_timeout 内的优雅关闭）
# 2) 修改配置（两个值必须同时改，否则启动 fail-fast）
FLASH_SALE_DISPATCH_MODE=kafka
FLASH_SALE_KAFKA_ENABLED=true
# 3) 启动新进程
```

反向切回时把两个值同时改回 `db_scanner` / `false`。先停旧、再启新，
不允许两套消费入口同时工作。

### 10.9 本地完整环境与端到端验证

```bash
# 1) 启动 MySQL/Redis（默认不启动 Kafka）
make deps-start

# 2) 启动 Kafka（profile 隔离）并创建 Topic
docker compose --profile kafka up -d kafka
docker exec my-shop-kafka /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server kafka:9092 --create --if-not-exists \
  --topic surgecart.flashsale.order.requested.v1 --partitions 12 --replication-factor 1
docker exec my-shop-kafka /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server kafka:9092 --create --if-not-exists \
  --topic surgecart.flashsale.order.requested.v1.dlq --partitions 12 --replication-factor 1

# 3) 一条命令跑真实 Kafka 秒杀端到端（专用库/Redis DB/唯一 Topic，含故障注入）
make test-kafka-e2e
```

`make test-kafka-e2e` 覆盖：db_scanner 模式（无需 Kafka）、Kafka 完整链路（HTTP → Redis →
MySQL Outbox → Kafka → Consumer → 订单 → Offset）、模式切换、Publisher/Consumer 重启恢复、
Kafka/MySQL 停机恢复、Publisher 与 Consumer 同时运行时的关闭。
设置 `B4_E2E_FAULTS=0` 可跳过 stop/start 容器的故障注入；`B4_E2E_RUN='^TestName$'` 可单用例调试。

### 10.10 B4.1 进程级 serve 生命周期门禁

```bash
make test-serve-lifecycle    # = bash scripts/test-serve-lifecycle.sh
```

该门禁启动**真实编译产物**（`test/serveproc`，与生产 `main.go` 同一命令装配），而不是在
测试进程内调用 `runtime.Shutdown()`：

1. 专用 MySQL 库（`my_shop_serve_lifecycle`）、专用 Redis DB（默认 7）、唯一 Topic/DLQ/Group；
2. 真实 `serve`：`boot.Bootstrap` → 七牛启动校验 → `StartFlashSaleRuntime` → HTTP 监听 → `/health`；
3. 预热一条秒杀请求，证明 Publisher/Consumer 已真实工作；
4. 持续提交秒杀请求后，向应用 PID 发送操作系统 SIGTERM；
5. 断言退出码 0、未被信号杀死、关闭事件顺序、`within_budget=true`、无组件 `timed_out`；
6. 同配置重启，等待全部已受理请求收敛，核对订单数 / `sold` / 重复订单 / Kafka 消息 /
   Consumer Offset 已提交。

**七牛校验的处理**：测试环境没有真实七牛凭据，SDK 也不支持仅靠配置改 UC endpoint，因此
测试专用入口在 `serve_lifecycle` 构建标签下把 `storage.SetUcHosts` 指向本地 HTTPS Mock
（`mock` 只返回 UC/bucketInfo 协议响应）。生产二进制不包含这段代码，`upload.ValidateConfig`
的结构校验、必填校验、签名与 `GetBucketInfo` 调用全部保留；不存在 `skip_qiniu_validation`
一类开关。真实七牛外网 endpoint 的校验在本环境标记为 NOT_VERIFIED。

### 10.11 单节点局限

Compose 中的 Kafka 是单节点 KRaft（RF=1）。它只能验证「Broker 短时不可用后恢复」，
**不能**证明 Broker 永久故障下的数据不丢失、副本切换或高可用；这些需要 3 Broker / RF=3 /
`min.insync.replicas=2` 的集群环境，属于后续部署阶段。

## 11. 如何检查 Kafka 是否正常运行

```bash
# 1) 容器健康（healthy 才代表 Broker API 可用）
docker inspect -f '{{.State.Health.Status}}' my-shop-kafka

# 2) 查看 Topic 列表
docker exec my-shop-kafka /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server kafka:9092 --list

# 3) 写入一条消息
echo '{"event_id":"flashsale.requested:1"}' | \
  docker exec -i my-shop-kafka /opt/kafka/bin/kafka-console-producer.sh \
    --bootstrap-server kafka:9092 \
    --topic surgecart.flashsale.order.requested.v1

# 4) 从头读取一条消息
docker exec my-shop-kafka /opt/kafka/bin/kafka-console-consumer.sh \
  --bootstrap-server kafka:9092 \
  --topic surgecart.flashsale.order.requested.v1 \
  --from-beginning --max-messages 1 --timeout-ms 20000

# 5) 查看消费组 Lag
docker exec my-shop-kafka /opt/kafka/bin/kafka-consumer-groups.sh \
  --bootstrap-server kafka:9092 \
  --describe --group surgecart-flashsale-order-worker-v1
```

如果容器一直 `starting`，查看日志：

```bash
docker compose --profile kafka logs -f kafka
```

## 12. 单节点开发环境与生产环境的区别

| 维度 | 本地开发（本项目 Compose） | 生产建议 |
| --- | --- | --- |
| Broker 数 | 1（Broker + Controller 合一） | 3 个独立 Broker + 3 个 Controller |
| 副本数 RF | 1 | 3 |
| `min.insync.replicas` | 1 | 2 |
| `acks` | `all` | `all` |
| 故障容忍 | **无**：单节点宕机即不可用/可能丢消息 | 容忍 1 个 Broker 故障 |
| Unclean Leader Election | 未开启 | 必须禁止 |
| Topic 分区 | 12 | 按吞吐评估，创建后只能增不能减 |

本地 KRaft 单节点只用于开发联调，**不得据此声明生产级 Broker 故障容忍能力**。
生产集群的部署、监控、容量评估不在 B1–B4 范围，也未在本仓库完成；Kubernetes 适配属于后续阶段。

## 13. 已实现 / 未实现的功能边界（B4）

已实现：

- B1：`github.com/twmb/franz-go/pkg/kgo` 接入（v1.18.1，见下节选型说明）、通用消息契约
  `internal/mq/mq.go`、配置读取/校验、同步确认 Producer、Consumer Group 基础客户端、
  RequestedEvent v1 编解码与校验、本地 KRaft Compose 服务与显式 Topic 创建；
- B2：Request Outbox Publisher（短事务领取 + `FOR UPDATE SKIP LOCKED`、租约与 claim token、
  多实例并发发布、失败指数退避、逐条 ACK 后标记、人工修复重发、Prometheus 指标）；
- B2.1：event_id / 业务幂等语义冻结、Outbox Topic 显式路由、Message Key 策略冻结、
  Outbox 并发 Race 测试进入 CI 门禁；
- B3：Kafka 业务 Consumer 核心（`RequestedEventConsumer`）、与 Scanner 共享的订单事务核心、
  消息校验与身份比对、MySQL 先于 Offset 的提交顺序、持久化重试预算 + 退避 + 死信、
  Redis 补偿复用、Rebalance 生命周期、Consumer 指标；真实 MySQL/Kafka 集成测试与
  Consumer 并发 Race 门禁；
- B4：`serve` 启动装配与 `dispatch_mode` 二选一、Publisher 后台发布循环、Consumer 监督与
  有界重启、统一优雅关闭预算、Scanner 与 Kafka 严格互斥（启动层测试）、F-03 逐条结果语义、
  F-04 NOWAIT 标记 + 预算、真实 MySQL/Redis/Kafka/HTTP 端到端门禁与故障注入。

未实现（后续阶段）：

- Kubernetes/Helm 部署、多 Broker 生产集群、Kafka 多实例容量验证；
- Consumer Lag 指标与消费堆积告警（需要消费组元数据 API，见 9.8）；
- 分区暂停/恢复、消费端 seek 重试；
- 运行中热切换模式（B4 明确要求通过重启生效）；
- Schema Registry、Kafka 分区键重新设计。

**B4 后秒杀 Kafka 链路可运行但不是生产高可用**：默认仍是 `db_scanner`；
启用 Kafka 需同时设置 `dispatch_mode=kafka` 与 `enabled=true`，并使用显式创建的 Topic。
单节点 KRaft 只用于本地联调，生产部署与容量评估属于后续阶段。

## 14. franz-go 版本与选型说明

采用 `github.com/twmb/franz-go v1.18.1`（`pkg/kgo`），理由：

1. **Go 版本兼容**：其 `go.mod` 要求 Go 1.21，与仓库声明的 `go 1.23.0` 兼容；
   更新的 v1.19+ 要求 Go 1.23.8 及以上、v1.20+ 要求 Go 1.24，会迫使升级项目 Go 指令或工具链。
2. **纯 Go、无 cgo**：压缩（含 zstd）由 `klauspost/compress` 实现，`CGO_ENABLED=0` 静态构建可用。
3. **能力完整且依赖克制**：单客户端同时支持幂等 Producer、Consumer Group、手动提交、
   阻塞 rebalance、zstd；只引入 `kmsg`、`lz4`、`compress` 三个传递依赖，不额外引入 Schema Registry 等生态组件。
4. **版本成熟稳定**：v1.18.1 是 1.18 系列收尾版本，API 稳定，避免为追新引入大版本行为变化。

升级 franz-go 前需重新核对 `go.mod` 的 Go 指令要求，并运行仓库统一质量门禁
（`go build ./...`、`go vet ./...`、`make test-unit`、`go test ./internal/mq/...`）。
