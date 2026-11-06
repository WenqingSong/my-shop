# Kafka 开发说明

本文面向本地开发与后续批次实现者，说明 SurgeCart 引入 Kafka 的职责边界、配置方式、
本地 KRaft 环境、Producer/Consumer 使用方式与 B1 的功能边界。

> **当前状态（B2.1）**：Kafka 基础设施与消息契约（B1）、Request Outbox 发布器（B2/B2.1）
> 已完成，但**秒杀业务链路仍走 MySQL 出队表 + 后台扫描器（`dispatch_mode=db_scanner`）**：
> Kafka 默认关闭，没有业务消费 Handler、没有消费组装配，也没有切换 dispatch_mode，
> Outbox Publisher 同样未在生产装配。不要把本文描述的目标能力误读为已上线能力。

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
  dispatch_mode: "db_scanner"   # 占位：B1 不实现切换，当前唯一生效路径
  kafka:
    enabled: false
    brokers: "127.0.0.1:29092"
    topic: "surgecart.flashsale.order.requested.v1"
    dlq_topic: "surgecart.flashsale.order.requested.v1.dlq"
    consumer_group: "surgecart-flashsale-order-worker-v1"
```

| 配置项 | 环境变量 | 说明 |
| --- | --- | --- |
| `flash_sale.kafka.enabled` | `FLASH_SALE_KAFKA_ENABLED` | 总开关，默认 `false`；关闭时允许 Broker/Topic 留空 |
| `flash_sale.kafka.brokers` | `FLASH_SALE_KAFKA_BROKERS` | 逗号分隔的 `host:port`，自动去空格；拒绝空项、重复项、非法端口 |
| `flash_sale.kafka.topic` | `FLASH_SALE_KAFKA_TOPIC` | 正常事件 Topic，启用时必填 |
| `flash_sale.kafka.dlq_topic` | `FLASH_SALE_KAFKA_DLQ_TOPIC` | 死信 Topic，启用时必填且不得与 `topic` 相同 |
| `flash_sale.kafka.consumer_group` | `FLASH_SALE_KAFKA_CONSUMER_GROUP` | 消费组名，启用时必填 |

校验规则要点：

- `enabled=false` 时不要求 Kafka 配置齐全，但**已填写的值仍会校验**，避免配置错误被开关掩盖；
- `enabled=true` 时 Broker 至少一个，Topic/DLQ/消费组必填，错误信息会指出具体配置项；
- `enabled` 必须是合法布尔值；`"ture"` 这类拼写错误会直接报错，而不是被静默当成 `false`。

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
`Publish` 同步等待逐条确认，返回结果与入参顺序一致，部分失败不会被伪装成整批成功；
`ctx` 到期会立即返回，但已入队的消息可能仍在后台投递——下游必须幂等。

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
- 上例只演示接口用法，**不是完整的业务消费循环**：重试预算、死信写入、数据库事务与位移提交顺序
  由 B3 设计实现。

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

## 9. 如何检查 Kafka 是否正常运行

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

# 5) 查看消费组 Lag（消费接入后使用）
docker exec my-shop-kafka /opt/kafka/bin/kafka-consumer-groups.sh \
  --bootstrap-server kafka:9092 \
  --describe --group surgecart-flashsale-order-worker-v1
```

如果容器一直 `starting`，查看日志：

```bash
docker compose --profile kafka logs -f kafka
```

## 10. 单节点开发环境与生产环境的区别

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
生产集群的部署、监控、容量评估不在 B1 范围，也未在本仓库完成。

## 11. 已实现 / 未实现的功能边界（B2.1）

已实现：

- B1：`github.com/twmb/franz-go/pkg/kgo` 接入（v1.18.1，见下节选型说明）、通用消息契约
  `internal/mq/mq.go`、配置读取/校验、同步确认 Producer、Consumer Group 基础客户端、
  RequestedEvent v1 编解码与校验、本地 KRaft Compose 服务与显式 Topic 创建；
- B2：Request Outbox Publisher（短事务领取 + `FOR UPDATE SKIP LOCKED`、租约与 claim token、
  多实例并发发布、失败指数退避、逐条 ACK 后标记、人工修复重发、Prometheus 指标）；
- B2.1：event_id / 业务幂等语义冻结、Outbox Topic 显式路由、Message Key 策略冻结、
  Outbox 并发 Race 测试进入 CI 门禁。

未实现（后续批次）：

- 业务消费 Handler、消费组装配、重试/死信处置、数据库事务与位移提交顺序（B3）；
- `dispatch_mode=kafka` 切换与旧 Scanner 退役协调（B4）；
- F-03（批次超时导致重复放大）与 F-04（发布标记锁等待突破租约）的修复，
  必须在 B4 完整启用 Kafka 链路前处理；
- 分区暂停/恢复、rebalance 时在途事务处理、数据库不可用时消费策略；
- Kafka 业务指标、Consumer Lag 告警、生产集群部署与容量验证。

**B2.1 没有完成秒杀 Kafka 化**：Kafka 默认关闭，业务链路仍然走 `db_scanner`，
Outbox Publisher 未在生产装配，也没有业务消费。

## 12. franz-go 版本与选型说明

采用 `github.com/twmb/franz-go v1.18.1`（`pkg/kgo`），理由：

1. **Go 版本兼容**：其 `go.mod` 要求 Go 1.21，与仓库声明的 `go 1.23.0` 兼容；
   更新的 v1.19+ 要求 Go 1.23.8 及以上、v1.20+ 要求 Go 1.24，会迫使升级项目 Go 指令或工具链。
2. **纯 Go、无 cgo**：压缩（含 zstd）由 `klauspost/compress` 实现，`CGO_ENABLED=0` 静态构建可用。
3. **能力完整且依赖克制**：单客户端同时支持幂等 Producer、Consumer Group、手动提交、
   阻塞 rebalance、zstd；只引入 `kmsg`、`lz4`、`compress` 三个传递依赖，不额外引入 Schema Registry 等生态组件。
4. **版本成熟稳定**：v1.18.1 是 1.18 系列收尾版本，API 稳定，避免为追新引入大版本行为变化。

升级 franz-go 前需重新核对 `go.mod` 的 Go 指令要求，并运行仓库统一质量门禁
（`go build ./...`、`go vet ./...`、`make test-unit`、`go test ./internal/mq/...`）。
