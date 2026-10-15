# Kafka 异步链路设计

本文面向项目接手者，说明「秒杀异步落单链路引入 Kafka」后的架构边界、事件契约、权威数据边界、消费切换状态机与模块改造边界。本文描述**目标架构**（Kafka 落地为后续实现任务）；当前 `docs/design/architecture.md` §5「无外部 MQ」与 `docs/design/flash-sale.md` 的现状描述仍准确，待实现任务落地时同步更新。事实来源为 `kafka-architecture-contract-v1` 的 APPROVED Contract 与 Owner 决策（D1~D4）。

## 1. 职责与边界

Kafka 在秒杀链路中承担「已受理请求待落单事件」的**传输与分发**，不承载任何业务状态；MySQL 仍是订单、库存、请求生命周期与事件事实的**唯一业务权威**；Redis 仍是加速闸门与派生缓存（对账收敛到 MySQL）。

引入 Kafka 要解决的具体问题（对比现有 MySQL 出队表 + `goroutine+ticker` 消费者）：

| 维度 | 现状（MySQL 出队表 + 扫描器） | 引入 Kafka 后 |
| --- | --- | --- |
| 消费吞吐 | 受 1s 轮询周期 × 每轮 100 条上限 × 出队行锁竞争限制 | 消费组分区并行、水平扩展 |
| 生产消费耦合 | 生产者（HTTP 入队）与消费者（扫描器）同进程紧耦合 | 解耦，事件经 Kafka 投递 |
| 削峰 | 队列在 MySQL 表内，扫描器串行消费 | Broker 缓冲 + 分区并行消费 |
| 可观测 | 无标准 Lag/积压指标 | Consumer Lag、CDC 延迟可观测 |
| 扩展 | 单实例部署（`architecture.md` §5） | 消费组水平扩展 |

不引入 Kafka 的后果：消费吞吐受单实例轮询与锁竞争限制，无法获得水平扩展与运维解耦收益。Kafka 的引入**不改变**五个核心业务不变量（INV-001~005），正确性始终由 MySQL 兜底。

边界：本任务覆盖秒杀链路（Step 1）；订单生命周期事件（Step 2/3）属后续 Task 14~15，本文只做架构预留，不实现、不改现有订单事务。不引入分布式事务，不建立 Kafka 事件回环。

## 2. 架构

```text
HTTP POST /flash-sales/:id/orders
   │  限流 / 排队软上限 / 熔断（不变）
   │  Redis Lua 闸门：预扣 remaining + 写 bought/idem 标记（不变，非权威）
   ▼
MySQL 单事务：
   INSERT flash_sale_order_requests(status=queued)     ← 受理事实
   INSERT outbox_events(event_type=FlashSaleOrderRequested)  ← 事件事实（同事务，Transactional Outbox）
   │  提交后立即返回 queued
   │
   │  Debezium MySQL Connector 读 binlog + Outbox Event Router
   ▼
Kafka topic: outbox.event.flashsale  ──►  Kafka Consumer Group（秒杀落单）
                                              │  按 request_id 锁行 + 校验 status=queued（幂等闸门）
                                              ▼
                                    MySQL 单事务落单（复用 insertOrderInTx）：
                                    活动/时间窗校验 + INSERT flash_sale_orders
                                    + 条件扣 sold + requests.status=success
                                    失败：failed（业务终态）/ 重试 / dead（技术超限）
                                    提交后确认 offset（at-least-once + 幂等）

（对账扫描器 StartFlashSaleReconcileScanner 不变：remaining = total_stock - sold - inflight_queued）
```

权威数据边界：

| 数据 | 权威事实来源 | Redis | Kafka / Debezium |
| --- | --- | --- | --- |
| 秒杀订单成功 | MySQL `flash_sale_orders` | 不承载 | 不承载（事件是 MySQL 行投影） |
| 秒杀已售库存 | MySQL `flash_sale_activity_skus.sold` | 派生 `remaining`，对账收敛 | 不承载 |
| 请求生命周期 | MySQL `flash_sale_order_requests` | 派生标记 | 不承载业务状态 |
| 受理/事件 | MySQL（`flash_sale_order_requests` + `outbox_events` 同事务） | 无 | 传输管道，非业务事实 |

## 3. ADR（Architecture Decision Records）

### ADR-001：引入 Kafka 驱动秒杀异步落单

- **Context**：秒杀异步落单当前用 MySQL 出队表 + `goroutine+ticker` 扫描器，正确性已由 MySQL 兜底，但消费吞吐受单实例轮询与出队锁竞争限制，无法水平扩展。
- **Decision**：引入 Kafka 作为「已受理请求待落单事件」的传输与分发层，Kafka Consumer 作为迁移后的唯一正常落单入口。
- **Consequence**：获得水平扩展、削峰、可观测；引入 Debezium/Kafka 基础设施与运维成本；正确性仍由 MySQL 兜底，不引入分布式事务。
- **被拒方案**：继续用 MySQL 出队表不引入 Kafka——被拒理由是它无法获得消费水平扩展与运维解耦收益，但它是本项目正确性基线，作为回滚兜底路径保留。

### ADR-002：独立 `outbox_events` 表 + Debezium CDC（Transactional Outbox）

- **Context**：需把「受理事实」与「事件投递」原子绑定，且支持后续订单领域事件与多个 Consumer Group。
- **Decision**：新增独立 `outbox_events` 表，`queued` 请求与 `FlashSaleOrderRequested` 事件同一 MySQL 事务持久化；Debezium MySQL Connector + Outbox Event Router 读 binlog 发布到 Kafka；业务 API 不直接发 Kafka。
- **Consequence**：受理与事件原子一致，事件通道可扩展（aggregate_type 路由）；引入 Debezium Connector 运维与 binlog 保留约束；业务事务与投递解耦。
- **被拒方案**：复用/扩展 `flash_sale_order_requests` 作为 Outbox——被拒理由是它把「请求生命周期状态」与「事件投递状态」耦合在同一行，且不利于支撑后续订单领域事件与事件契约演进；独立表关注点分离、契约更清晰。

### ADR-003：Kafka 单向投递受理事件，秒杀结果以 MySQL 权威

- **Context**：秒杀请求受理后需异步落单，订单成功/失败结果需可查询。
- **Decision**：Kafka 只投递 `FlashSaleOrderRequested` 受理事件（单向）；订单成功事实在 `flash_sale_orders`、失败状态在 `requests.status`，客户端统一经 `GET /flash-sales/:id/orders/result` 查 MySQL，不回投 Kafka。
- **Consequence**：避免第二套状态同步与回执对账，正确性单一来源在 MySQL。
- **被拒方案**：把订单成功/失败结果也发 Kafka（双向事件流）——被拒理由是引入第二套状态同步与回执对账，无正确性收益，超出最小 Scope。

### ADR-004：`consume_driver` 开关三阶段切换 + 回滚

- **Context**：新旧消费路径（MySQL 扫描器 vs Kafka Consumer）不能无协调双跑。
- **Decision**：`consume_driver ∈ {mysql, kafka}` 任一时刻只有一个 driver 领取正常任务；迁移前 `mysql`、Shadow（可选）只读观察、迁移中/后 `kafka`、回滚切回 `mysql`。
- **Consequence**：切换/回滚可控，回滚无需回放 Kafka（消费事实源始终是 MySQL `flash_sale_order_requests`）。
- **被拒方案**：MySQL 扫描器与 Kafka Consumer 长期双跑——被拒理由是即使幂等兜底，也会重复领取同一请求、放大竞争与资源消耗，且难以界定「谁是最新状态」。

### ADR-005：完整业务事件流分阶段实施

- **Context**：Owner 诉求是支持订单领域事件、多个 Consumer Group 与事件契约演进，而不只是秒杀受理通知。
- **Decision**：Step 1 实现 `FlashSaleOrderRequested`（Kafka 驱动可靠异步秒杀）；Step 2 订单生命周期事件 `OrderCreated/OrderPaid/OrderCancelled/OrderRefunded`；Step 3 独立订单事件消费者（如运营统计投影）。
- **Consequence**：事件基础设施通用化（单 Outbox 表多 aggregate_type 路由）；订单事件属后续 Task 14~15，不阻塞秒杀核心链路验收。
- **被拒方案**：Kafka 永久只承载秒杀受理事件——被拒理由是后续订单事件无法复用同一事件通道，需重复建设。

## 4. 数据模型

新增 `outbox_events`（append-only 事件记录，DDL 由实现任务预留 migration_version）：

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | VARCHAR(36) | 主键，UUID，稳定事件 ID |
| `aggregate_type` | VARCHAR(64) | 非空，聚合类型，topic 路由（`flashsale` / `order`） |
| `aggregate_id` | VARCHAR(64) | 非空，聚合 ID，Kafka message key（秒杀=`request_id`；订单=`order_no`） |
| `event_type` | VARCHAR(64) | 非空，事件类型 |
| `schema_version` | INT | 非空，事件 Schema 版本 |
| `payload` | JSON | 非空，事件体 |
| `occurred_at` | DATETIME | 非空，事件时间 |
| `created_at` | DATETIME | 非空，落库时间 |

索引：`idx_aggregate(aggregate_type, aggregate_id, id)`、`idx_created(created_at)`（最终索引由实现任务定）。

既有表不变：`flash_sale_order_requests`（请求生命周期/幂等/查询/在途统计/审计关联，四态状态机不变）；`flash_sale_orders`（成功订单唯一事实来源）；`flash_sale_activity_skus`（秒杀库存）。

## 5. 事件契约与 Topic

- 路由：单一 `outbox_events` 表按 `aggregate_type` 路由到 topic（Debezium Outbox Event Router 默认 `outbox.event.<aggregate_type>`）：
  - `flashsale` → `outbox.event.flashsale`，消费方 = 秒杀落单 Kafka Consumer。
  - `order` → `outbox.event.order`，消费方 = 订单事件消费者（后续 Task 14~15）。
- `FlashSaleOrderRequested`（`schema_version=1`）：

```json
{ "request_id": 123, "user_id": 456, "activity_id": 789, "sku_id": 101,
  "idempotency_key": "k", "request_hash": "sha256hex" }
```

- Kafka message key = `aggregate_id`；秒杀请求无顺序语义，分区不为正确性约束。
- 契约演进：`schema_version` 承载版本；向后兼容新增可选字段；破坏性变更走新 `event_type` 或新 `schema_version`，保留旧消费者兼容窗口。
- 状态映射（消费 → MySQL）：`success`（与 `flash_sale_orders` 同事务）→ `requests.status=success`；业务失败 → `failed`；技术失败 → 退避重试超限 `dead`；处理完成（无论 success/failed/dead）后提交 offset。

## 6. 模块改造边界表

| 模块/文件 | 现状职责 | 改造后职责 | 改造类型 |
| --- | --- | --- | --- |
| `internal/logic/flashsale/flashsale.go`（`CreateOrder` 入队段） | Redis 闸门 + `INSERT flash_sale_order_requests` | 同事务追加 `INSERT outbox_events`（Transactional Outbox） | 扩展（受理事务） |
| `internal/logic/flashsale/consume.go`（`ConsumeQueued`/`consumeOne`） | MySQL 出队消费 | 迁移后退役（MySQL driver 保留为回滚） | 保留/退役 |
| `internal/logic/flashsale/flashsale.go`（`insertOrderInTx`） | 单事务落单核心 | 复用为 Kafka Consumer 落单核心，不改事务边界与唯一约束 | 复用（不改） |
| `internal/worker/scanner.go`（`StartFlashSaleConsumeScanner`） | 秒杀消费扫描器 | `mysql` driver 时运行；`kafka` driver 时停用/退役 | 保留/退役 |
| `internal/worker/scanner.go`（`StartFlashSaleReconcileScanner`） | 秒杀对账收敛 | 不变 | 不变 |
| 新增：Outbox 写入逻辑 | — | 受理事务内写 `outbox_events` | 新增 |
| 新增：Kafka Consumer（`logic/flashsale` 或 `worker`） | — | 订阅 `outbox.event.flashsale`，幂等落单，提交后置 offset | 新增 |
| 新增：Debezium Connector + Event Router 配置 | — | 读 binlog 发布 outbox 事件 | 新增（基础设施） |
| `manifest/config/config.yaml` | `flash_sale.*` 容量/对账配置 | 新增 `flash_sale.consume_driver`、`kafka.*`、`debezium.*`、积压/背压阈值 | 扩展（配置） |
| `docs/design/kafka.md` | 不存在 | 本文（长期设计） | 新增 |

## 7. 消费切换状态机（`consume_driver`）

| 状态 | consume_driver | MySQL 出队消费扫描器 | Outbox 写入 + Debezium | Kafka Consumer |
| --- | --- | --- | --- | --- |
| 迁移前 | `mysql` | 运行（唯一入口） | 不启用 | 不启动 |
| Shadow（可选） | `mysql` + `kafka` 只读 | 运行 | 启用 | 只读观察（不执行订单事务、不确认生产 offset） |
| 迁移中 | `kafka`（灰度） | 停止 | 启用 | 运行（唯一落单入口） |
| 迁移后 | `kafka`（全量） | 退役 | 启用 | 运行 |
| 回滚 | `mysql` | 重新运行 | 停用 | 停止 |

安全条件：

- `kafka` 模式下正常任务分发必须由 Kafka Consumer Group 驱动；禁止旧 Scanner 与 Kafka Consumer 无协调同时领取正常任务（Shadow 只读除外）。
- 历史 queued 请求迁移（backfill）：切 Kafka 前对存量 `status=queued` 且无 Outbox 事件的请求一次性回填 `outbox_events`，或先用 MySQL Scanner 排空再切换。
- 在途任务：已锁行未提交的消费事务随回滚回到 queued，由新 driver 自然接管。
- 保留对账扫描器与 `dead→queued` 人工修复，但修复不得代替 Kafka 正常消费。

## 8. 业务不变量

- INV-K1（受理不丢）：`status=queued` 的已受理请求（含 Outbox 事件）在 Kafka 故障恢复后最终到终态。
- INV-K2（重复投递不重复建单）：同一 `request_id` 重复投递只产生一次业务效果。
- INV-K3（成功订单事实来源不变）：`flash_sale_orders` 行存在仍是成功订单唯一事实（INV-002 不变）。
- INV-K4（单一消费入口）：迁移任一阶段同一 request 不被双跑重复成功落单。
- INV-K5（受理与事件同事务）：`queued` 受理与 `FlashSaleOrderRequested` 事件同事务原子。
- INV-K6（Redis 闸门地位不变）：Kafka 不改变 Redis 预扣闸门地位，`remaining` 仍对账收敛到 MySQL。

以上是对既有 INV-001~021 的 Kafka 场景延伸，不新增业务事实来源，不削弱 MySQL 兜底。

## 9. 一致性与失败语义

- 事实来源：MySQL 为唯一业务权威（含事件事实）；Redis 派生、对账最终一致；Kafka 传输管道。
- 受理一致性：`request + outbox` 同事务；提交即「已受理 + 事件已持久」。
- 投递：Debezium 读 binlog，从上次 offset 续传；binlog 保留 ≥ 最长断连窗口；Kafka 故障不丢已提交事件（事件在 MySQL）。
- 消费一致性：MySQL 单事务落单，提交后确认 offset；at-least-once + 幂等闸门收敛重复投递。
- 失败：Redis 不可用 fail-closed；MySQL 不可用 fail-closed；Kafka 不可用不 fail-closed 但有界（积压/背压阈值）。
- 事件去重：`outbox_events.id` 主键唯一 → Kafka key = `aggregate_id` → 消费者 `request_id` 幂等闸门。
- Outbox 安全清理：所有 Consumer Group 消费确认 + 保留窗口后归档/删除，不早于最慢消费者，清理不产生新业务事件。

## 10. 配置契约

新增配置（用途/必填/安全默认值由实现任务落地，敏感凭据走环境变量）：

- `flash_sale.consume_driver`：`mysql` | `kafka`，切换消费路径。
- `kafka.*`：broker、topic 前缀、consumer group、consumer 并发、手动 offset 提交。
- `debezium.*`：connector 配置、binlog 保留、投递重试。
- 积压/背压：Outbox/请求积压数量、最老积压时间、CDC 延迟、Consumer Lag 阈值。

## 11. 实施阶段

- **Step 1（秒杀核心，Task 08~13）**：`FlashSaleOrderRequested` + 独立 `outbox_events` + Debezium + Kafka Consumer 可靠异步秒杀。
- **Step 2（订单事件，Task 14~15）**：`OrderCreated/OrderPaid/OrderCancelled/OrderRefunded`，事件名/时机/Schema 与业务状态对应；`OrderPaid` 对应 Mock 支付，不描述为真实第三方支付。
- **Step 3（订单事件消费者，Task 14~15）**：运营统计投影等独立消费者，验证多 Consumer Group、幂等消费、事件重放。

## 12. Deferred / 已知留白

- 订单生命周期事件与订单事件消费者属 Task 14~15，本任务只做架构预留。
- `outbox_events` DDL、Debezium Connector/Event Router 具体配置、binlog 保留时长、清理策略参数、积压/背压阈值由实现任务定。
- 「24 个 Kafka 工程 Task」完整清单为 Owner 路线图，仓库内尚未展开为独立 task.md。
- Kafka/Debezium 真实吞吐/延迟/运维成本需实现任务在目标环境验证。
