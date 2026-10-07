# Technical Contract

## Decision Status
APPROVED

## Problem

秒杀 V2（Redis Lua 同步落单）已证明五个业务不变量正确。V3 需在**不改变这五个不变量及其 MySQL 事实来源兜底**的前提下，把「下单」拆成「入队快速响应 + 后台消费落单」两阶段，并具备：消费幂等、重复消息去重、失败重试、死信/待修复队列、用户结果查询、失败补偿与降级。核心难点在于队列选型（决定中间态持久性与可靠性）与「秒杀订单状态机 / 成功订单口径」的重定义。

## Verified Current Behavior

- VERIFIED：下单入口 `POST /flash-sales/:id/orders`（前台 `middleware.Auth`，作用于 `Principal.UserID`）→ `controller.flashsale.CreateOrder` → `service.FlashSale().CreateOrder` → `logic.flashsale.CreateOrder`（`internal/logic/flashsale/flashsale.go`），当前为同步落单。
- VERIFIED：V2 链路 = Redis Lua 闸门（`runGate`/`flashSaleGateScript`，`redis.go`）做空值/活动/时间窗/售罄/幂等/一人一单检查 + 库存预扣（DECR）→ `resolveSku` 快照 → `insertOrder` 单事务「活动校验 + 时间窗（MySQL NOW）+ 锁定绑定 + 条件扣减 `sold < total_stock` + 插入订单」→ `markOrderSuccess` 写 Redis 标记；失败 `compensatePreDeduct` 回补。
- VERIFIED：`flash_sale_orders` **无状态字段，行存在即成功订单**（`20261001000011_flash_sale.up.sql`）；唯一约束 `uk_flash_order_no`/`uk_flash_idempotency(user_id,idempotency_key)`/`uk_flash_one_per_user(activity_id,sku_id,user_id)` 兜底五不变量。
- VERIFIED：项目**无 MQ、无 gcron、无异步队列**；已有两个后台扫描器范式 `goroutine + ticker`（`internal/cmd/cmd.go` 的 `startOrderCancelScanner`/`startFlashSaleReconcileScanner`）。
- VERIFIED：`go.mod` 中 `github.com/redis/go-redis/v9 v9.12.1` 为间接依赖（经 gogf redis contrib），Redis Streams/consumer group 技术可用但当前未使用；gogf redis 经 `g.Redis().Do(...)` 执行任意命令。
- VERIFIED：错误码 12001~12007 已用于秒杀；`codes.go` 有 `CodeNotFound=1004(404)`、`CodeServiceUnavailable=1005(503)` 可复用；错误码域 12000-12999 在 `.agent/registry/error-codes.md` 为 flash-sale-v1 `RESERVED`（域内编号由 Analyst 在 Contract 逐个列出，域内无跨任务冲突）。
- VERIFIED：migration 最新 `20261001000014`（banners），`next = 20261001000015`；MySQL 8.0（`docker-compose.yml`，支持 `FOR UPDATE ... SKIP LOCKED`）。
- VERIFIED（本次核实）：`internal/migrations/migrations_test.go` 的 `TestUpFailsFastAndMarksDirty` 注入 `20261001000015_broken.up.sql`、`TestUpAppliesOnlyPendingMigration` 注入 `20261001000015_probe.up.sql`，二者经 `sourceWithExtra` 复用生产 `embeddedMigrations`（`//go:embed sql/*.sql`）。**一旦加入真实 `20261001000015_flash_sale_order_requests.up.sql`，这两处 synthetic migration 将与生产 version 撞号，触发 golang-migrate「duplicate migration」**。此外 `latestMigrationVersion`、`businessTables`、`expectedSchema` 均硬编码当前 schema，新增表需同步更新。
- UNKNOWN：Redis 是否开启 AOF/持久化策略（`manifest/config/config.yaml` 未配置持久化）。本方案中间态已落 MySQL，Redis 仅作非权威闸门，该未知项不影响正确性。

## Recommendation

RECOMMENDATION（已被 Owner ACCEPT）：**采用 MySQL 出队表（新增 `flash_sale_order_requests`）作为异步队列与请求状态载体，消费者复用既有 `goroutine + ticker` 扫描范式；Redis 保持为「闸门 + 预扣 + 快速失败」的非权威快路径。**

选型理由（Owner 确认）：不是「Redis Streams 不持久」（其可配持久化），而是当前项目无独立 MQ，且 AC 要求请求中间态、失败/死信状态可查询并与 MySQL 落库事实一致；把 request lifecycle 也放 MySQL，可减少 Redis/MySQL 两套事实之间的恢复与一致性复杂度。备选 Redis Streams（快但中间态非持久）、外部 MQ（引入全新依赖）均未采纳。

### 核心流程

1. **入队（HTTP 内）**：Redis Lua 闸门（复用 V2 脚本，新增「GATE_PASSED 时原子写入 `bought`/`idem` 标记」以去重并发入队）→ `INSERT flash_sale_order_requests`（status=queued）→ 返回「已受理/排队中」（HTTP 200 + code=0 + `data.status=queued`）。撞 `uk_request_idempotency` 时读回既有请求：同 `request_hash` 返回既有状态，异 hash → 12005。
2. **消费（后台 ticker）**：`SELECT ... WHERE status=queued AND (next_attempt_at IS NULL OR next_attempt_at<=NOW()) ORDER BY id LIMIT N FOR UPDATE SKIP LOCKED` → 每条 `resolveSku` 快照 + 单事务「活动校验 + 时间窗 + 条件扣 `sold` + `INSERT flash_sale_orders` + `UPDATE requests.status=success`」。
3. **结果查询**：`GET /flash-sales/:id/orders/result?idempotency_key=K`，按 `(user_id, activity_id, idempotency_key)` 查 requests 行返回状态；不存在/非本人 → 404。

### 状态机（`flash_sale_order_requests.status`）

- `queued`(0)：已受理排队中（含退避重试中，`retry_count`/`next_attempt_at` 承载技术重试，不另设 `processing` 状态；消费者靠 `FOR UPDATE SKIP LOCKED` 互斥领取）。
- `success`(1)：落单成功（`flash_order_id` 关联 `flash_sale_orders.id`），**与成功订单创建处于同一 MySQL 事务**。
- `failed`(2)：业务失败终态（库存不足/时间窗结束/已购/SKU 不可用/参数非法/活动下架），不可自动重试。
- `dead`(3)：技术失败重试超限（死信/待修复），可通过运维 SQL/后续接口 `dead→queued` 重新处理。

「成功订单」口径：**`flash_sale_orders` 行存在 = 成功订单（唯一业务事实，不变）**；`flash_sale_order_requests` 只表达异步请求生命周期。

### 一致性模型

- **MySQL 权威**：`flash_sale_orders`（成功订单）、`flash_sale_order_requests`（请求状态）、`flash_sale_activity_skus.sold`（已售）。
- **Redis 非权威**：闸门预扣、`bought`/`idem`/`soldout` 标记、`remaining` 计数。
- **库存预扣在入队时（Redis DECR），权威扣减在消费时（MySQL `sold < total_stock` 条件更新）**。
- **对账公式**：`Redis remaining = total_stock - sold - inflight_queued`，其中 `inflight_queued` = 该 (activity,sku) 下 `status=queued` 的请求数（计入在途预扣，避免对账把在途预扣错误回补导致超预扣）。
- **消费失败补偿**：`INCR remaining` + 清除 `bought`/`idem` 标记 +（库存尽时）置 `soldout`；`requests.status` 落 `failed`/`dead`，不 INSERT 成功订单、不扣 `sold`（事务回滚）。

## Selected Design

`Redis Gate → durable MySQL request queue → MySQL worker → transactional order creation`

- `Redis` = non-authoritative admission/gating layer（高并发闸门 + 库存预扣快路径）。
- `flash_sale_order_requests` = durable asynchronous request lifecycle（排队/成功/失败/死信，可查询）。
- `flash_sale_orders` = successful order source of truth（行存在即成功订单）。

### 失败与降级语义（Owner 修改后最终版）

- **Redis 不可用（闸门 Lua 失败）→ fail closed**：返回 5xx（`CodeServiceUnavailable=1005`，503），**不创建 request、不预扣、不直接同步落单**。秒杀场景 Redis 正是高并发闸门，Redis 故障时把流量打回 MySQL 会在最需要保护数据库时绕过保护层，且同一 API 同时存在「queued」与「同步成功」两套语义会增加复杂度和风险。
- **Redis 闸门成功（已预扣+写标记）、但 request INSERT 失败（DB 故障）→ 补偿 Redis 预扣（INCR）+ 清除 `bought`/`idem` 标记 → 返回 5xx**；不得留下「Redis 已预扣但 MySQL 无 request」的正常残留状态。
- **消费技术失败**：`retry_count+1` + 退避 `next_attempt_at`；超上限 → `dead`。消费业务失败：直接 `failed`（不重试）。
- **重复投递/重复消费**：请求级幂等由 `uk_request_idempotency` 兜底；落单幂等由 `uk_flash_idempotency`/`uk_flash_one_per_user` 兜底；消息去重由「FOR UPDATE SKIP LOCKED + 状态原子更新」保证同一出队行只被处理一次。
- Redis outage fallback 如需后续支持，应作为独立容量保护/降级设计处理，不得复用 V1 绕过闸门。

## Interfaces and Data

### 接口变更

- `POST /flash-sales/:id/orders`（前台 Auth）：请求体不变 `{sku_id, idempotency_key}`；响应从「同步返回 `FlashOrder`」改为「返回排队受理结果」`CreateOrderRes{ status: "queued", idempotency_key, activity_id, sku_id }`（不再返回已创建订单）。
- 新增 `GET /flash-sales/:id/orders/result?idempotency_key=K`（前台 Auth，作用于 `Principal.UserID`）：返回 `{ status: "queued"|"success"|"failed"|"dead", order?: FlashOrder, fail_code?: int }`；`success` 时附 `FlashOrder`；按 `flash_sale_id + user_id + idempotency_key` 验证归属，不存在/不属于本人统一 404（复用 `CodeNotFound=1004`，不新增错误码）。

### 数据模型（新增 migration `20261001000015`）

新增表 `flash_sale_order_requests`：

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（取自 `Principal.UserID`） |
| `activity_id` | BIGINT UNSIGNED | 非空，软引用 |
| `sku_id` | BIGINT UNSIGNED | 非空，软引用 |
| `idempotency_key` | VARCHAR(64) | 非空 |
| `request_hash` | VARCHAR(64) | 非空，请求指纹 |
| `status` | TINYINT | 非空默认 0（0=queued/1=success/2=failed/3=dead） |
| `retry_count` | INT UNSIGNED | 非空默认 0 |
| `next_attempt_at` | DATETIME | 可空，退避重试时间 |
| `last_error_code` | INT | 可空，最近失败码（观测用，不泄露细节） |
| `flash_order_id` | BIGINT UNSIGNED | 可空，成功后关联 `flash_sale_orders.id` |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_request_idempotency(user_id, idempotency_key)` UNIQUE、`idx_dequeue(status, next_attempt_at, id)`（消费出队扫描用）。

不新增 `flash_sale_orders` 状态字段、不改既有三表结构；成功订单事实来源不变。

## Business Invariants

- INV-001（库存不为负）：任意时刻 `sold ≤ total_stock`（条件扣减 `sold < total_stock` + `INT UNSIGNED`）。
- INV-002（成功订单数 = 已售）：`COUNT(flash_sale_orders) = sold`，成功订单数与扣减同事务一致。
- INV-003（一人一单）：同一 `(activity_id, sku_id, user_id)` 至多一个**成功**订单（`uk_flash_one_per_user` 兜底）；重复入队由 `bought` 标记 + 该约束最终兜底。
- INV-004（幂等防重复扣减）：同一 `(user_id, idempotency_key)` 至多一个请求（`uk_request_idempotency`）+ 至多一个成功订单（`uk_flash_idempotency`）；重复消费不重复扣库存、不重复建单。
- INV-005（失败不建单、不扣减）：消费失败不 INSERT 成功订单、不扣 `sold`（事务回滚）、补偿 Redis 预扣、清标记、落 `failed`/`dead`，不残留半成品。
- INV-006（服务端定价与快照）：成交价 = 消费时重读 `flash_price` 快照（不信任客户端价与普通 SKU 价），与 V1 INV-006 一致。
- INV-007（查询归属隔离）：结果查询仅本人可见，越权/不存在统一 404 且无副作用。

## Failure and Consistency Semantics

- 入队成功（返回 queued）= 请求已被持久受理（`flash_sale_order_requests` 落 `queued`），**不代表**订单已创建。
- 消费成功（status=success）= MySQL 事务提交（`sold+1` 且 `flash_sale_orders` 落库，同一事务），同 V1/V2「下单成功」。
- 消费失败 = 不扣 `sold`、不建成功订单；业务失败落 `failed`、技术失败重试后落 `dead`；Redis 预扣与标记被补偿，无残留。
- 重复/重试：幂等键去重（`uk_request_idempotency`）保证请求级至多一次业务效果；落单级由 `uk_flash_idempotency`/`uk_flash_one_per_user` 兜底；`FOR UPDATE SKIP LOCKED` + 状态原子更新保证同一出队行只被处理一次。
- 允许最终一致：Redis `remaining` 与 MySQL `sold` 之间允许短暂不一致，由对账（含在途）周期收敛；成功订单事实始终以 MySQL 为准。

## Allowed / Forbidden Changes

- 允许：新增 `flash_sale_order_requests` 表与 migration `20261001000015`；改造 `CreateOrder` 为入队、新增消费者、结果查询接口；修改 `redis.go` 闸门 Lua（在 GATE_PASSED 时写标记）；调整 `CreateOrderRes`；`internal/cmd` 新增消费者扫描器；更新 `docs/design/flash-sale.md`；**解耦 migration 测试 namespace（见下）**。
- 禁止：改动 `flash_sale_orders`/`flash_sale_activities`/`flash_sale_activity_skus` 既有表结构与唯一约束语义；改变五个业务不变量的事实来源（仍为 MySQL 条件扣减 + 唯一约束）；引入外部 MQ/集群/限流/验证码/风控；改动普通订单/库存/SKU/商品/IAM 模块行为（除下单所需最小只读引用）；修改 Redis 预扣对账的「最终收敛到 MySQL」语义；在 Redis 不可用时降级为「同步落单绕过闸门」。

## Implementation Prerequisites（Migration Test Namespace 解耦）

真实 `20261001000015_flash_sale_order_requests.up.sql` 会与 `migrations_test.go` 中 synthetic `20261001000015_broken.up.sql`/`20261001000015_probe.up.sql` 撞号，必须先消除该结构性耦合，否则产生 duplicate migration：

- **禁止**把测试 probe/broken 简单从 `00015` 手工 bump 到 `00016` 作为长期方案（会随真实 migration 版本持续手工 bump）。
- **优先**将 migration failure/probe 测试（`TestUpFailsFastAndMarksDirty`、`TestUpAppliesOnlyPendingMigration`）改为独立 fixture/source（最小 synthetic baseline + probe），**不复用生产 `embeddedMigrations`**，使其版本号与生产 namespace 彻底隔离。
- **至少必须保证**新增真实 `00015` 后 migration 全套测试稳定，不再需要随真实 migration 版本持续手工 bump synthetic version。
- 同步更新与 schema 硬编码耦合的 `latestMigrationVersion`（→ 20261001000015）、`businessTables`（+ `flash_sale_order_requests`）、`expectedSchema`（+ 新表结构快照）。

## Verification Requirements

- INV-001/002 → MySQL+Redis：并发入队后等待消费，断言最终 `sold ≤ total_stock`、`COUNT(orders)=sold`。
- INV-003 → MySQL+Redis：同一用户并发重复入队同一 (activity,sku)，断言至多一个成功订单。
- INV-004 → MySQL+Redis：重复投递/消费同一消息（幂等键），断言订单数/库存扣减不变。
- INV-005 → MySQL+Redis：构造消费失败（库存不足/时间窗结束/已购/SKU 不可用），断言无成功订单、无 `sold` 扣减、Redis `remaining` 回补、标记清除、`requests.status` 落 `failed`。
- INV-006 → 断言成交价 = 消费时 `flash_price`，不信任客户端提交价。
- INV-007 → MySQL+Redis：本人查询返回正确状态；他人查询/未登录断言 404/401 且无副作用。
- AC-004 重试/死信 → MySQL+Redis：构造消费技术失败，断言按策略重试；超上限落 `dead` 且不再无限重试，`dead→queued` 可重处理。
- 降级 → MySQL+Redis：Redis 不可用断言 fail-closed 5xx（不落 request/不预扣/不同步落单）；闸门成功但 INSERT 失败断言补偿预扣+清标记+5xx、无「已预扣无 request」残留。
- Migration 解耦 → 新增真实 `00015` 后 `go test ./internal/migrations/...` 全套稳定，不依赖 synthetic version 手工 bump。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；并发/重试/死信断言用真实并发 + `go test -race`。

## Global Resources

- `migration_version`：预留 `20261001000015`（title `flash_sale_order_requests`，owner `flash-sale-v3`）。Candidate = `max(已记录 version) + 1 = 20261001000014 + 1`。
- `error_code_domain`：不新增错误码（复用秒杀域 12001~12007 与通用 `1004`/`1005`），无需新 reservation。

## Open Risks

- `FOR UPDATE SKIP LOCKED` 依赖 MySQL 8.0（已验证可用）；若生产 MySQL < 8.0 需降级为 `FOR UPDATE` + 显式锁，需 Owner 确认部署版本。
- 对账公式引入 `inflight_queued`（查 requests 表），对账扫描器需跨表统计，复杂度略增；已用「消费成功/失败状态原子更新」保证公式自洽。
- 死信修复途径本任务只提供「运维 SQL/日志」最小满足（管理接口属 Out of Scope）。
- Redis 无持久化配置不影响本方案正确性（中间态已落 MySQL），仅影响闸门预扣计数（对账收敛）。

## Owner Decision Record

- **D-001 队列选型**：ACCEPT 推荐方案——MySQL 出队表 `flash_sale_order_requests` + `goroutine/ticker` consumer；Redis 仅作高并发闸门/预扣快路径，MySQL 保持最终事实源。理由：项目无独立 MQ，且 AC 要求请求中间态、失败/死信状态可查询并与 MySQL 落库事实一致；request lifecycle 落 MySQL 可减少 Redis/MySQL 两套事实的恢复与一致性复杂度。
- **D-002 状态机**：ACCEPT——`queued/success/failed/dead` 四态；`flash_sale_order_requests` 只表达异步请求生命周期；`flash_sale_orders` 行存在仍是「成功订单」唯一业务事实；`success` 必须与成功订单创建同事务；`queued` 由 `retry_count`/`next_attempt_at` 承载技术重试，不设 `processing` 状态，消费者靠 `FOR UPDATE SKIP LOCKED` 互斥领取。
- **D-003 结果查询**：ACCEPT——`GET /flash-sales/:id/orders/result?idempotency_key=K`，绑定当前登录用户，按 `flash_sale_id + user_id + idempotency_key` 验证归属；不存在/非本人统一 404，复用 `CodeNotFound=1004`，不新增错误码。
- **D-004 降级语义**：PARTIALLY REJECT——不接受「Redis 不可用 → 自动走 V1 同步 MySQL 落单」。最终：Redis 不可用 → fail closed / 5xx（503），不创建 request、不预扣、不直接同步落单；闸门成功但 INSERT 失败 → 补偿预扣/标记 + 5xx；不得留下「已预扣但无 request」正常残留。Redis outage fallback 如需后续支持，应作为独立容量保护/降级设计，不复用 V1 绕过闸门。
- **D-005 全局资源 + Migration 前置**：ACCEPT migration `20261001000015`；错误码不新增。增加强制 prerequisite：真实 `00015` 与 synthetic `00015_broken`/`00015_probe` 撞号，必须先解耦 migration 测试 namespace（改独立 fixture/source，不复用生产 embedded migrations），不得手工 bump synthetic version 作为长期方案；该 structural fix 纳入本任务实现范围/AC。

## Task Change Note

D-005 引入的「migration 测试 namespace 解耦」是 Owner 在本决策中对 Task Scope/AC 的增量要求（真实 `00015` 会直接触发已知碰撞）。该要求已在本 Contract 的「Implementation Prerequisites」固化；`task.md` 的 Scope/AC 亦需由 Owner/TaskBuilder 同步补充该条目，以保持 Task ↔ Contract 一致。
