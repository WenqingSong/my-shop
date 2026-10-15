# Technical Contract — 秒杀异步落单链路引入 Kafka 的架构边界

## Decision Status
APPROVED

## Problem

秒杀异步下单链路当前用「MySQL 出队表 `flash_sale_order_requests` + `goroutine+ticker` 后台扫描器」完成「受理 → 消费落单」，正确性已由 MySQL 兜底（INV-001~021）。本任务确定「引入 Kafka」的架构边界：Kafka 解决什么问题、MySQL 请求表保留什么职责、Redis/MySQL/Kafka 权威边界、Outbox 用独立表还是复用、事件与状态机映射、故障受理/失败关闭、新旧消费路径切换与回滚。经 Owner 决策，采用**独立 `outbox_events` 表 + Debezium CDC（Transactional Outbox）**，并为后续订单领域事件留出通用事件通道。

本任务为纯架构/分析任务，不实现代码、不建表、不改迁移、不预留 migration_version / error_code_domain 编号（后续实现任务再预留）。

## Verified Current Behavior

- VERIFIED：消费路径现状（`internal/worker/scanner.go`）：三个后台扫描器并列——订单超时取消（60s）、秒杀缓存对账 `StartFlashSaleReconcileScanner`（60s）、秒杀消费 `StartFlashSaleConsumeScanner`（1s）。消费扫描器调 `service.FlashSale().ConsumeQueued`。
- VERIFIED：消费实现（`internal/logic/flashsale/consume.go`）：`FOR UPDATE SKIP LOCKED WHERE status=queued AND (next_attempt_at IS NULL OR next_attempt_at<=NOW()) ORDER BY id LIMIT 1` 出队 → 单事务「活动/时间窗校验 + 行锁读绑定 + `INSERT flash_sale_orders` + 条件扣 `sold`（`sold < total_stock`）+ `requests.status=success`」。业务失败落 `failed`（终态）；技术失败 `retry_count+1` + 指数退避（5s/10s/20s 封顶 60s），>3 次落 `dead`。
- VERIFIED：入队现状（`internal/logic/flashsale/flashsale.go`、`request.go`、`redis.go`）：`POST /flash-sales/:id/orders` 经限流/排队软上限/熔断 → Redis Lua 闸门原子预扣 `remaining` + 写 `bought`/`idem` 标记 → `INSERT flash_sale_order_requests(status=queued)` → 返回 queued。闸门失败/入队失败统一 `convergeStockAndMarkers` 补偿。
- VERIFIED：表结构与幂等键（`internal/migrations/sql/20261001000011*.sql`、`...00015*.sql`）：`flash_sale_order_requests` 四态 `queued(0)/success(1)/failed(2)/dead(3)`、`uk_request_idempotency(user_id, idempotency_key)`、`idx_dequeue(status, next_attempt_at, id)`；`flash_sale_orders` 有 `uk_flash_order_no`、`uk_flash_idempotency(user_id, idempotency_key)`、`uk_flash_one_per_user(activity_id, sku_id, user_id)`；`flash_sale_activity_skus` 有 `uk_activity_sku`。
- VERIFIED：权威边界（`docs/design/flash-sale.md` §5、`docs/design/architecture.md` §1/§3.2/§5）：MySQL 为订单/库存/请求生命周期权威；Redis 为加速闸门与派生缓存（对账 `remaining = total_stock - sold - inflight_queued` 收敛到 MySQL）；出队表「承担外部 MQ 的角色」。`architecture.md` §5 明确「无外部 MQ」。
- VERIFIED：不变量 INV-001~021（`docs/design/flash-sale.md` §4），尤其 INV-002（`COUNT(flash_sale_orders)=sold`）、INV-005/012（幂等）、INV-013（`status=success` 与订单同事务）、INV-015（崩溃重投只产生一次业务效果）。
- VERIFIED：无 Kafka/Debezium 依赖（`go.mod` 无 kafka/sarama/segmentio），`manifest/config/config.yaml` 无 kafka 配置段；Git 基线 `8fd62c06...`，feature 分支 `chore/kafka-architecture-contract-v1` == `origin/develop`。
- UNKNOWN：Kafka/Debezium 落地后的真实吞吐/延迟收益、分区与消费者并发数、Broker 与 Connector 运维成本，需实现任务在目标环境验证；本任务只定架构边界，不承诺性能数值。「24 个 Kafka 工程 Task」完整清单为 Owner 提供的路线图，仓库内尚无对应 task.md（仅本 Task 01 存在）。

## Owner Decision Record

Owner 就 D1~D4 作出正式决策（本记录为 Owner 选择，非 Analyst 推荐）：

- **D1（Outbox 方案）**：选择**独立 `outbox_events` 表 + Debezium MySQL Connector + Outbox Event Router**（CDC 发布），不采用复用 `flash_sale_order_requests` 作为 Outbox 事件表。理由：需支持后续订单领域事件、多个 Consumer Group 与事件契约演进。
- **D2（Kafka 故障受理策略）**：MySQL 请求持久化成功后返回 `queued`，Kafka 恢复后继续投递，但**有界**（积压/容量阈值 + 背压拒绝）。Redis 不可用仍 fail-closed；MySQL 无法持久化请求+事件禁止返回受理成功。
- **D3（消费入口）**：Kafka Consumer 是迁移后的**唯一正常落单入口**，用 `consume_driver` 受控迁移；旧 MySQL Scanner 保留为迁移期旧实现与回滚能力；禁止无协调双跑；Shadow 阶段只读观察。
- **D4（事件边界）**：选择**完整业务事件流，分阶段实施**——Step 1 `FlashSaleOrderRequested`（本任务核心）；Step 2 订单生命周期事件 `OrderCreated/OrderPaid/OrderCancelled/OrderRefunded`；Step 3 独立订单事件消费者（运营统计投影）。订单事件属后续 Task 14~15，不阻塞秒杀核心链路（Task 08~13）验收。

以上决策已与 Task 目标、Scope（秒杀链路为当前实现边界，订单事件留后续）核对相容；D4 不改变「本任务为秒杀 Kafka 架构 Contract」的交付范围，订单事件仅作为架构预留与后续阶段声明。

## Selected Design

### 1. Outbox：独立 `outbox_events` 表 + Debezium CDC（D1）

- 新增独立 `outbox_events` 表（append-only 事件记录模型，DDL 由实现任务预留 migration_version，本任务不建表）。
- `queued` 请求与 `FlashSaleOrderRequested` Outbox 事件在**同一 MySQL 事务**持久化：`INSERT flash_sale_order_requests(status=queued)` + `INSERT outbox_events(...)`，任一步失败整体回滚。
- 发布机制：**Debezium MySQL Connector 读 binlog + Outbox Event Router SMT** 将 `outbox_events` 路由到 Kafka；**业务 API 事务提交后不直接发 Kafka**，禁止不受保护的 MySQL/Kafka 双写。
- `flash_sale_order_requests` 保留并继续承担：请求状态（queued/success/failed/dead）、幂等（`uk_request_idempotency`）、结果查询（`GetOrderResult`）、在途库存统计（`findInflightQueued`）、修复审计关联（`flash_sale_request_audits`）。**它不再是 Outbox 事件源，也不再被消费扫描器轮询出队**（迁移后）。

Outbox 表结构（概念，DDL 由实现任务定）：

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | VARCHAR(36) | 主键，UUID，稳定事件 ID |
| `aggregate_type` | VARCHAR(64) | 非空，聚合类型，topic 路由（`flashsale` / `order`） |
| `aggregate_id` | VARCHAR(64) | 非空，聚合 ID，Kafka message key（秒杀=`request_id`；订单=`order_no`） |
| `event_type` | VARCHAR(64) | 非空，事件类型（`FlashSaleOrderRequested` / `OrderCreated` …） |
| `schema_version` | INT | 非空，事件 Schema 版本 |
| `payload` | JSON | 非空，事件体（业务字段） |
| `occurred_at` | DATETIME | 非空，事件时间（业务时间） |
| `created_at` | DATETIME | 非空，落库时间（默认 CURRENT_TIMESTAMP） |

索引：`idx_aggregate(aggregate_type, aggregate_id, id)`、`idx_created(created_at)`（供清理/审计，最终索引由实现任务定）。

### 2. 权威数据边界（AC-003）

| 数据 | 权威事实来源 | Redis | Kafka / Debezium |
| --- | --- | --- | --- |
| 秒杀订单成功 | MySQL `flash_sale_orders`（行存在 = 成功订单，不变） | 不承载 | 不承载（事件是 MySQL 行投影） |
| 秒杀已售库存 | MySQL `flash_sale_activity_skus.sold` | 派生 `remaining`，对账收敛 | 不承载 |
| 请求生命周期 | MySQL `flash_sale_order_requests`（queued/success/failed/dead） | 派生（`bought`/`idem` 标记） | 不承载业务状态 |
| 受理/事件 | MySQL（`flash_sale_order_requests` + `outbox_events` 同事务，为事件事实源） | 无 | Kafka 是传输管道；Debezium 读 binlog 发布，Broker 落盘是「投递」非「业务事实」 |

结论：MySQL 仍是订单/库存/请求生命周期/事件的**唯一业务权威**；Redis 闸门地位不变（加速闸门 + 派生缓存）；Kafka 是**传输/分发层**，秒杀结果查询仍以 MySQL 为权威，不用 Kafka 事件代替订单状态。

### 3. 为什么引入 Kafka（AC-001）

见 ADR-001。引入 Kafka 解决：消费水平扩展（消费组分区并行）、生产消费解耦、削峰填谷、Consumer Lag 可观测；不引入的后果是消费吞吐受单实例轮询与出队锁竞争限制。正确性始终由 MySQL 兜底，Kafka 不改变五个核心不变量。

### 4. 为什么保留 `flash_sale_order_requests`（AC-002）

改造后保留职责：请求生命周期状态机（queued/success/failed/dead）、请求级幂等、结果查询唯一来源、在途库存统计源、修复审计关联对象。既有不变量「`flash_sale_orders` 行存在是成功订单唯一业务事实」不变；该表不再是出队队列，出队职责由 Kafka Consumer 接管。

### 5. 事件契约与 Topic（D4）

- 单一 Outbox 表，按 `aggregate_type` 路由到不同 topic（Debezium Outbox Event Router 默认 `outbox.event.<aggregate_type>`，前缀可实现任务配置）：
  - `aggregate_type=flashsale` → topic `outbox.event.flashsale`，消费方 = 秒杀落单 Kafka Consumer。
  - `aggregate_type=order` → topic `outbox.event.order`，消费方 = 订单事件消费者（后续 Task 14~15）。
- Step 1 事件：`FlashSaleOrderRequested`（`schema_version=1`），`aggregate_id = request_id`，payload：

```json
{ "request_id": 123, "user_id": 456, "activity_id": 789, "sku_id": 101,
  "idempotency_key": "k", "request_hash": "sha256hex" }
```

- 秒杀请求无顺序语义（`flash-sale.md` §5.3 乱序无影响），分区不为正确性约束；`aggregate_id` 作为 Kafka key 保证同聚合消息落同一分区（顺序可观测，非业务依赖）。
- Step 2 事件（后续 Task 14~15）：`OrderCreated/OrderPaid/OrderCancelled/OrderRefunded`，事件名、触发时机、Schema 与 SurgeCart 实际业务状态对应；`OrderPaid` 对应现有 Mock 支付，不描述为真实第三方支付集成。
- 事件契约演进：`schema_version` 承载版本；向后兼容新增可选字段；破坏性变更走新 `event_type` 或新 `schema_version`，保留旧消费者兼容窗口。

### 6. 已受理请求不因 Kafka 故障永久丢失（D1/D2，AC-004）

- 受理持久化与事件产生同事务：`INSERT flash_sale_order_requests(status=queued)` + `INSERT outbox_events` 原子；**MySQL 提交即「已受理 + 事件已持久」**。
- Debezium 从 binlog 读取已提交事务，投递确认 = binlog 读取 + Kafka 写入；Kafka 故障时事件仍在 MySQL `outbox_events`（已提交），恢复后 Debezium 继续从 offset 投递，不丢。
- CDC 恢复：Debezium 从上次提交 offset（GTID / file+pos）续传；断开重连期间 binlog 保留（`binlog_expire_logs_seconds`）必须 ≥ 最长断连窗口。
- 事件去重：`outbox_events.id` 主键唯一（行级幂等）→ Kafka key = `aggregate_id` → 消费者按 `request_id` 幂等闸门（最终兜底）。
- Outbox 安全清理：已投递且**所有 Consumer Group 消费确认 + 保留窗口**之后归档/删除；清理不早于最慢消费者；清理动作不产生新业务事件。
- 消费侧崩溃：offset 未提交 → Kafka 重投 → 幂等闸门 + 唯一约束保证只一次业务效果（INV-015 延伸）。

### 7. 幂等：Kafka 重复投递不重复建单（AC-005）

- 消费幂等闸门：Consumer 按 `request_id` 在事务内 `FOR UPDATE` 锁行并校验 `status=queued`；`status` 已非 queued 视为重复投递，跳过并提交 offset。
- 落单级幂等仍由 `uk_flash_idempotency` / `uk_flash_one_per_user` 兜底；请求级由 `uk_request_idempotency` 兜底。**不新增去重键**。
- Offset 提交：仅当 MySQL 订单事务**提交后**才确认对应 Kafka offset（关闭自动提交）；重复投递不重复建单、不错误修改成功状态。
- 边界行为：同一 `request_id` 重复投递 N 次只产生一次业务效果；同一幂等键不同内容（`request_hash` 不同）落 `failed`（12005 语义）。

### 8. 失败关闭策略（D2，AC-006）

- **Redis 不可用**：维持现状 fail-closed（`1005`/503，不预扣、不建单、不排队），语义不变。
- **MySQL 不可用**：fail-closed——受理依赖 MySQL，`request + outbox` 写失败即 5xx 并 `convergeStockAndMarkers` 补偿 Redis 预扣，无残留「已预扣但无 request」。
- **Kafka 不可用**：**不 fail-closed**——已提交的 request + outbox 事件在 MySQL 持久化，返回 `queued`，Kafka 恢复后 Debezium 续投。但**有界**：建立「Outbox/请求积压数量、最老积压时间、CDC 延迟、Consumer Lag」监控，超阈值拒绝新秒杀准入（fail-closed 背压，复用 503/或实现任务新增稳定错误码），不得无限接受请求。

### 9. 消费路径切换与回滚（D3，AC-007）

`consume_driver ∈ {mysql, kafka}`，任一时刻只有一个 driver 领取正常任务：

| 阶段 | consume_driver | MySQL 出队消费扫描器 | Outbox 写入 + Debezium | Kafka Consumer |
| --- | --- | --- | --- | --- |
| 迁移前 | `mysql` | 运行（唯一入口） | 不启用 | 不启动 |
| Shadow（可选） | `mysql`（消费）+ `kafka`（只读） | 运行 | 启用 | 只读观察，不执行订单事务、不确认生产 offset |
| 迁移中 | `kafka`（灰度：按 activity/比例） | 停止 | 启用 | 运行（唯一落单入口） |
| 迁移后 | `kafka`（全量） | 退役（保留代码待观察后删） | 启用 | 运行 |
| 回滚 | 切回 `mysql` | 重新运行 | 停用 Debezium/Consumer | 停止 |

- 切换：`kafka` 模式下正常任务分发由 Kafka Consumer Group 驱动；**禁止旧 Scanner 与 Kafka Consumer 无协调同时领取正常任务**（Shadow 阶段只读观察除外）。
- 回滚触发：Kafka 消费/投递异常（Lag 超阈值、持续投递失败、消费错误率超阈值）。回滚操作：切 `consume_driver=mysql` → MySQL Scanner 按 `status=queued` 直接出队落单，**无需回放 Kafka**；Kafka 残留消息因「status 已非 queued」被幂等跳过。
- 历史 queued 请求迁移（backfill）：切 Kafka 前，对存量 `status=queued` 且无对应 Outbox 事件的请求，需一次性回填 `outbox_events`（或先用 MySQL Scanner 排空再切换）；在途任务（已锁行未提交）由事务回滚回到 queued 自然接管。
- 保留 Redis/MySQL 对账扫描器 `StartFlashSaleReconcileScanner`（与消费路径正交，不误删）；保留 `dead→queued` 人工修复，但不得让它代替 Kafka 正常消费。

### 10. 后续实现任务需预留的资源类型与语义（本任务不预留）

- migration_version：新增 `outbox_events` 表（DDL）由实现任务（秒杀 Kafka 落地）预留 migration_version。
- error_code_domain：可能新增 Kafka 投递/消费相关错误码，优先复用现有 flash-sale 域（12000-12999）；若需新域，实现任务预留 error_code_domain。

## Interfaces and Data

- 公开 HTTP 接口不变：`POST /flash-sales/:id/orders`（返回 queued 语义不变）、`GET /flash-sales/:id/orders/result`（查 MySQL 状态不变）。
- 内部服务接口：`service.FlashSale().ConsumeQueued`（MySQL 出队消费）迁移后退役；`insertOrderInTx`（单事务落单）保持复用，是 Kafka Consumer 的落单核心，不得改变其事务边界与唯一约束依赖。
- 数据/存储约束：`flash_sale_order_requests` 四态状态机不变；`outbox_events` 为 append-only 事件表，与业务状态通过 Transactional Outbox 原子持久化；成功订单事实来源 `flash_sale_orders` 唯一约束不变。
- 配置：新增 `flash_sale.consume_driver`、`kafka.*`（broker/topic/consumer group/consumer 并发）、`debezium.*`（connector/binlog 保留/重试）与「积压/背压阈值」配置；需说明用途、必填条件与安全默认值（敏感凭据走环境变量，不硬编码）。

## Business Invariants

- INV-K1（受理不丢）：任何 `status=queued` 的已受理请求（含其 `outbox_events` 事件），在 Kafka 故障恢复后最终被消费到终态（success/failed/dead），不因 Kafka 故障永久停留在「已受理但无人处理」。验证：断 Kafka → 受理 → 恢复 Kafka → 请求到达终态。
- INV-K2（重复投递不重复建单）：同一 `request_id` 的 Kafka 消息重复投递 N 次，只产生一次业务效果（`flash_sale_orders` 行数、`sold` 扣减各一次）。验证：人为重复投递 → 订单数/sold 不变。
- INV-K3（成功订单事实来源不变）：引入 Kafka 后 `flash_sale_orders` 行存在仍是成功订单唯一业务事实，Kafka 消息不承载/替代订单事实（INV-002 不变）。验证：消费后 `COUNT(flash_sale_orders)=sold`。
- INV-K4（单一消费入口）：迁移任一阶段同一 request 不被 MySQL Scanner 与 Kafka Consumer 无协调双跑重复成功落单（Shadow 只读除外；即使双跑，`status=queued` 幂等闸门 + 唯一约束保证只一次业务效果）。验证：切换窗口下订单数/sold 无重复。
- INV-K5（受理与事件同事务）：`status=queued` 受理与 `FlashSaleOrderRequested` Outbox 事件同事务原子持久化，不存在「有 request 无事件」或「有事件无 request」。验证：事务回滚后两者都不存在。
- INV-K6（Redis 闸门地位不变）：Kafka 不改变 Redis 预扣闸门地位；`remaining` 仍对账收敛到 MySQL（INV-011/014 不变）。

（INV-K* 是对既有 INV-001~021 在 Kafka 场景下的延伸，不新增业务事实来源，不削弱 MySQL 兜底。）

## Failure and Consistency Semantics

- 成功语义：下单成功 = MySQL 事务提交（`sold+1` + `flash_sale_orders` 落库 + `requests.status=success` 同事务），同 V1-V5 不变；入队成功（queued）= 已持久受理 + 事件已持久，不代表下单成功；Kafka 消息不代表业务事实。
- 失败语义：Redis 闸门/入队失败语义不变；Kafka/Debezium 故障不影响「已受理」事实（事件已在 MySQL）；消费业务失败/技术失败分类沿用 `classifyConsumeError`。
- 一致性：MySQL 是事实来源（含事件事实）；Redis 派生、对账最终一致；Kafka 与 MySQL 之间是 at-least-once（消费幂等 + offset 提交后置兜底），**不引入分布式事务**，靠「MySQL 单事务受理 + 单事务落单 + 幂等闸门」收敛。
- 重复/乱序/重试：重复投递由幂等闸门 + 唯一约束约束；乱序无影响；重试由退避 + 死信约束；部分完成由「消费单事务原子」约束。
- 并发目标落到可验证结果：`sold ≤ total_stock`、`COUNT(flash_sale_orders)=sold`、一人一单/幂等唯一约束无违例（INV-001~005），不新增「高并发」口号。

## Allowed / Forbidden Changes

- 允许：新增 `flash_sale.consume_driver`、`kafka.*`、`debezium.*` 与积压/背压配置；实现任务新增 `outbox_events` 表 DDL、Debezium Connector/Event Router 配置、Kafka Consumer 组件；迁移后退役 MySQL 出队消费扫描器；接入 Debezium CDC 与 Kafka 基础设施。
- 禁止：改变 `flash_sale_orders` 成功订单事实来源与唯一约束；改变 `flash_sale_order_requests` 四态状态机语义；改变 `insertOrderInTx` 事务边界；改变 Redis 闸门 fail-closed 语义（Redis 不可用仍 fail-closed）；改变 `GetOrderResult`/`CreateOrder` 公开接口契约；业务 API 直接发 Kafka 或引入不受保护的 MySQL/Kafka 双写；把对账扫描器删除或复用其错误职责；重写现有正确的订单事务（订单事件用 Outbox 原子持久化，不改订单事务逻辑）；把 Mock 支付描述为真实第三方支付集成。

## Verification Requirements

- INV-K1 → 场景：停 Kafka → 受理 N 条 → 恢复 Kafka → 全部到达 success/failed/dead；预期：无请求永久停留在 queued。
- INV-K2 → 场景：同一 request_id 消息重复投递 ≥2 次；预期：`flash_sale_orders` 行数、`sold` 各只 +1。
- INV-K3 → 场景：消费后核对 `COUNT(flash_sale_orders)` 与 `sold`；预期：相等（INV-002）。
- INV-K4 → 场景：切换/Shadow 窗口下消费；预期：无重复订单、sold 无重复扣减。
- INV-K5 → 场景：受理事务回滚后核对 `flash_sale_order_requests` 与 `outbox_events`；预期：两者都不存在该记录。
- INV-K6 → 场景：对账后 `remaining = total_stock - sold - inflight_queued`；预期：Redis 收敛到 MySQL 权威值。
- 回滚验证：kafka→mysql 切换后 MySQL Scanner 能直接消费 `status=queued` 请求并落单，Kafka 残留消息幂等跳过。
- 事件契约演进验证：`schema_version` 变更时旧消费者兼容性（向后兼容新增字段）。
- 本任务无代码/DDL，上述为**实现任务的验收要求**；本任务由 Analyst 自检四者一致性（架构图/ADR/Contract/边界表）+ Owner 决策。

## Open Risks

- Kafka/Debezium 落地后的真实吞吐/延迟/运维成本未量化，需实现任务在目标环境验证。
- `outbox_events` 表 DDL、Debezium Connector/Event Router 具体配置、binlog 保留时长、清理策略参数由实现任务定（本任务只定语义）。
- 积压/背压阈值的具体数值需实现任务依据压测与容量基线确定。
- 「24 个 Kafka 工程 Task」完整清单为 Owner 路线图，仓库内尚未展开为独立 task.md；本 Contract 仅覆盖 Task 01（架构边界），Step 2/3（订单事件）对应 Task 14~15 后续展开。
