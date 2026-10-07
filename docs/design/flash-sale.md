# 秒杀设计（Flash Sale）

本文面向项目接手者，说明「秒杀核心闭环」的架构、数据模型、并发/一致性、权限边界与错误码域。V1 以单一 MySQL 证明正确性；V2 在其上引入 Redis + Lua 抢购热路径（活动/库存预热、Lua 原子预扣、售罄/穿透快速失败、对账与降级）；V3 在 V2 基础上把「下单」拆成「入队快速响应 + 后台消费落单」两阶段（MySQL 出队表 + `goroutine+ticker` 消费者），异步化不改变五个业务不变量及其 MySQL 事实来源兜底。MySQL 始终是最终事实来源与正确性兜底。事实来源为 `flash-sale-v1`/`flash-sale-v2`/`flash-sale-v3` 的最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

秒杀回答「活动在什么时间、以什么价格、卖出多少」：管理员创建秒杀活动（绑定 SKU、秒杀价、秒杀库存、起止时间），登录用户在活动时间窗内以秒杀价下单。系统以「一人一单 + 请求幂等键 + 条件库存扣减」保证五个不变量（库存 ≥ 0、成功订单数 ≤ 初始库存、一人一单、失败不建单、重复请求不重复扣库存）。

V1 只用 MySQL 证明正确性。V2 引入 Redis + Lua 作为**加速闸门**：活动与秒杀库存预热到 Redis，Lua 原子完成资格校验、一人一单/幂等去重与库存预扣，让售罄、穿透、未预热等无效请求在 Redis 层快速失败、减少打到 MySQL 的无效请求。**Redis 预扣只是闸门，不是事实来源**；最终正确性仍由 MySQL 条件扣减 + 唯一约束保证，Redis 与 MySQL 经对账最终一致。

边界：不引入 Redis 集群/哨兵/多级缓存/**外部 MQ**/限流/风控/验证码；V3 的异步队列用 MySQL 出队表实现（不引入独立 MQ）。秒杀订单「下单即成交」：不做支付、取消、退款、超时恢复，秒杀库存一经扣减不因任何后续操作恢复。秒杀库存是**普通可售库存的预分配/预留活动配额**，运行时独立、抢购事务内不联动普通 `inventories`，V1/V2/V3 均不实现自动划拨。不修改普通订单/库存/购物车/商品/SKU/身份模块行为（除下单时对 SKU/商品的只读校验）。

## 2. 数据模型

V2 不新增、不修改表结构与索引；V3 新增 `flash_sale_order_requests`（异步请求生命周期表，§2.4）。以下三表为 V1 已建、V2/V3 复用。

### 2.1 `flash_sale_activities`（秒杀活动）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `name` | VARCHAR(64) | 非空 |
| `status` | TINYINT | 非空默认 1（`1=enabled`、`0=disabled`/下架） |
| `start_time` | DATETIME | 非空，活动开始 |
| `end_time` | DATETIME | 非空，活动结束（应用层校验 `end_time > start_time`） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`idx_status(status)`、`idx_time(start_time, end_time)`。

### 2.2 `flash_sale_activity_skus`（活动 × SKU 绑定：秒杀价 + 秒杀库存）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `activity_id` | BIGINT UNSIGNED | 非空，FK → `flash_sale_activities.id` `ON DELETE CASCADE` |
| `sku_id` | BIGINT UNSIGNED | 非空，软引用（无 FK，下单时经 `ISku.GetByID` 校验） |
| `flash_price` | INT UNSIGNED | 非空，秒杀价整数分（`>0`，≤99,999,999） |
| `total_stock` | INT UNSIGNED | 非空，秒杀初始库存（正整数，语义为预分配配额） |
| `sold` | INT UNSIGNED | 非空默认 0，已售 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_activity_sku(activity_id, sku_id)` UNIQUE、`idx_sku_id(sku_id)`。

剩余库存 = `total_stock - sold`（恒 ≥ 0）。

### 2.3 `flash_sale_orders`（秒杀订单，软引用快照自足）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `order_no` | VARCHAR(32) | 非空，`uk_flash_order_no` 唯一（秒杀前缀，独立于普通订单号命名空间） |
| `user_id` | BIGINT UNSIGNED | 非空，软引用，取自 `Principal.UserID` |
| `activity_id` | BIGINT UNSIGNED | 非空，软引用 |
| `sku_id` | BIGINT UNSIGNED | 非空，软引用 |
| `product_id` | BIGINT UNSIGNED | 非空，软引用 |
| `sku_name` | VARCHAR(128) | 非空，快照 |
| `product_name` | VARCHAR(128) | 非空，快照 |
| `product_main_image` | VARCHAR(512) | 非空默认 `''`，快照 |
| `flash_price` | INT UNSIGNED | 非空，成交价快照 = 下单时 `flash_price` |
| `quantity` | INT UNSIGNED | 非空默认 1（每次购买恒 1 件） |
| `idempotency_key` | VARCHAR(64) | 非空，客户端幂等键 |
| `request_hash` | VARCHAR(64) | 非空，请求指纹（sha256 hex） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_flash_order_no(order_no)` UNIQUE、`uk_flash_idempotency(user_id, idempotency_key)` UNIQUE、`uk_flash_one_per_user(activity_id, sku_id, user_id)` UNIQUE、`idx_activity(activity_id)`、`idx_user(user_id)`。

### 2.4 `flash_sale_order_requests`（V3 异步请求生命周期，出队表）

V3 把「下单」拆成「入队快速响应 + 消费落单」，本表承载异步请求的排队/成功/失败/死信生命周期，同时充当消费者的出队队列。它**不**改变成功订单事实来源：`flash_sale_orders` 行存在仍是「成功订单」唯一业务事实。

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（取自 `Principal.UserID`） |
| `activity_id` | BIGINT UNSIGNED | 非空，软引用 |
| `sku_id` | BIGINT UNSIGNED | 非空，软引用 |
| `idempotency_key` | VARCHAR(64) | 非空，客户端幂等键 |
| `request_hash` | VARCHAR(64) | 非空，请求指纹（sha256 hex） |
| `status` | TINYINT | 非空默认 0（`0=queued`、`1=success`、`2=failed`、`3=dead`） |
| `retry_count` | INT UNSIGNED | 非空默认 0，技术重试次数 |
| `next_attempt_at` | DATETIME | 可空，退避重试时间（`queued` 状态承载技术重试，不设 `processing` 状态） |
| `last_error_code` | INT | 可空，最近失败码（观测用，不泄露细节） |
| `flash_order_id` | BIGINT UNSIGNED | 可空，成功后关联 `flash_sale_orders.id` |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_request_idempotency(user_id, idempotency_key)` UNIQUE、`idx_dequeue(status, next_attempt_at, id)`（消费出队扫描用）。

消费者出队：`WHERE status=queued AND (next_attempt_at IS NULL OR next_attempt_at <= NOW()) ORDER BY id LIMIT N FOR UPDATE SKIP LOCKED`，靠行锁 + 状态原子更新保证同一行只被领取一次。

## 3. 状态机与生命周期

- 活动 `status`：二态 `enabled`（默认）/`disabled`（下架），创建默认 `enabled`，写接口直接设置与校验；「已结束」由 `end_time` 派生，不存冗余终态、无后台状态翻转任务。
- 秒杀订单（`flash_sale_orders`）无状态机：下单事务提交即成功（行存在 = 成功订单），无待支付/取消/退款。V3 异步下该口径不变：`flash_sale_orders` 行存在仍是「成功订单」唯一业务事实。
- 异步请求（`flash_sale_order_requests.status`）四态状态机：`queued`（排队中，含退避重试）→ `success`（与成功订单创建同事务）或 `failed`（业务失败终态，不自动重试）或 `dead`（技术失败重试超限，死信/待修复，可 `dead→queued` 重新处理）。`success` 与成功订单创建必须在同一 MySQL 事务。
- 可售判定（下单时，同事务 MySQL 条件写入）：`status=enabled` 且 `start_time <= NOW() < end_time`（左闭右开）。

## 4. 业务不变量

V1 五个核心不变量（INV-001~005）在 V2/V3 下依然成立，最终由 MySQL 兜底；V2 额外引入 Redis 闸门不变量（INV-010~011）；V3 异步化**不改变** INV-001~005 的事实来源（仍为 MySQL 条件扣减 + 唯一约束），仅把「下单」拆成「入队 + 消费落单」，并新增请求生命周期不变量（INV-012~013）。

- INV-001（库存不为负）：任何成功下单后 `total_stock - sold ≥ 0`，并发下单亦不例外（条件更新 `sold < total_stock` 兜底 + `INT UNSIGNED` 类型兜底）；Redis 侧 `remaining ≥ 0`（Lua 原子 DECR 兜底）。
- INV-002（成功订单数 ≤ 初始库存）：`sold ≤ total_stock` 且 `COUNT(flash_sale_orders) = sold`（同事务保证订单数与扣减一致）。
- INV-003（一人一单）：同一 `(activity_id, sku_id, user_id)` 至多一个成功订单，由 `uk_flash_one_per_user` 兜底；重复购买返回 `12004` 且无写入。Redis `bought` 标记为快速失败优化（下单成功后写入，无孤儿）。
- INV-004（失败不建订单）：时间窗不符/库存不足/SKU 不可用/已购/参数非法/越权等失败请求不创建订单、不扣减秒杀库存、不留半成品（单事务回滚）；Redis 预扣在 MySQL 失败时补偿回滚。
- INV-005（幂等防重复扣减）：同一 `(user_id, idempotency_key)` 至多一个订单；同键同 `request_hash` 返回既有订单、不重复扣库存；同键不同 `request_hash` 返回 `12005`（`uk_flash_idempotency` 兜底）。Redis `idem` 为快速路径。
- INV-006（服务端定价与快照）：成交价 = 下单时 `flash_price` 重读快照，不信任客户端价与普通 SKU 价；下单后改价不影响已生成订单。
- INV-007（事务原子性）：下单 = 单事务「时间窗校验 + 一人一单/幂等校验 + 条件扣秒杀库存 + 创建订单」，任一步失败整体回滚。
- INV-008（权限）：管理员创建/更新活动需 `flash_sale:*`（含超管）；无权限 403 且无 DB 写入；用户身份取自 `Principal.UserID`，不信任请求自带身份。
- INV-009（配额预分配前提）：秒杀库存是普通可售库存的预分配/预留活动配额；全局不超卖依赖「秒杀配额已从普通库存划拨」。V1/V2 不实现自动划拨，此约束为运营前置约定（非机械校验）；后续引入自动划拨时转为可机械验证的不变量。
- INV-010（Redis 预扣不超预热库存）：并发下 Redis 预扣总量 ≤ 预热库存（Lua 原子 DECR 兜底）；Redis 超卖只会增加 MySQL 无效请求，不会造成 MySQL 超卖。
- INV-011（对账收敛）：对账后 Redis `remaining = total_stock - MySQL sold - inflight_queued`（`inflight_queued` = 该活动×SKU 下 `status=queued` 的请求数，计入在途预扣，避免把在途预扣错误回补导致超预扣），Redis 预扣计数与 MySQL 最终一致。
- INV-012（请求级幂等）：同一 `(user_id, idempotency_key)` 至多一个异步请求（`uk_request_idempotency` 兜底）；重复消费不重复扣库存、不重复建单。
- INV-013（请求状态与成功订单一致）：`requests.status=success` 必须与成功订单创建同事务；`requests.status=failed/dead` 时不存在对应成功订单、不扣减 `sold`、Redis 预扣与标记已补偿、无残留。

## 5. 一致性模型与失败语义

### 5.1 事实来源

- **MySQL 为权威事实来源**：`flash_sale_activities`/`flash_sale_activity_skus`（活动与秒杀库存事实）、`flash_sale_orders`（秒杀订单事实，软引用快照自足）、`flash_sale_order_requests`（V3 异步请求生命周期与排队/失败/死信状态）、`skus`/`products`（下单时存在性与可用性事实来源，读后快照）。
- **Redis 为加速闸门与派生缓存**（非权威）：活动元数据、剩余库存预扣计数、一人一单/幂等标记、售罄标记、空值标记。最终经对账收敛到 MySQL。V3 下 Redis 仅承担闸门/预扣快路径，不承担请求生命周期（排队/失败/死信状态落在 MySQL `flash_sale_order_requests`）。

### 5.2 成功与失败语义

- 下单成功 = MySQL 事务提交（秒杀库存 `sold` 增加 1 且秒杀订单落库），同 V1；不代表普通库存变更、不代表可取消。V3 异步下「下单成功」发生在消费者事务内（`sold+1` + `flash_sale_orders` 落库 + `requests.status=success` 同一事务）。
- 入队成功（V3，HTTP 返回 `status=queued`）= 请求已被持久受理（`flash_sale_order_requests` 落 `queued`），**不代表下单成功**。
- Redis 预扣成功（Lua 返回 GATE_PASSED）仅代表「通过闸门」，**不代表下单成功**。
- 失败语义：时间窗不符 → `12002`；库存不足/售罄 → `12003`；已购 → `12004`；幂等冲突 → `12005`；SKU/商品不可用 → `12006`/`5001`/`4001`；越权 → 403；参数非法 → `12007`/`1001`；活动不存在/下架/未预热 → `12001`；DB 技术错误 → `1000`（500），均无写入、不泄漏底层细节。
- 无超时取消/退款：秒杀库存不因任何后续操作恢复。

### 5.3 跨系统一致性与原子性

- Redis Lua 原子（单脚本内不可分割）；MySQL 下单单事务原子。
- Redis 预扣与 MySQL 落单为**两步，非跨系统原子**：
  - 正常路径：Lua 预扣成功 → MySQL 事务失败 → 同请求补偿 Redis（`INCR remaining`）；一人一单/幂等标记因下单成功后才写入，故无需补偿。
  - 异常路径：进程崩溃于「Lua 预扣成功、MySQL 未提交/未消费」之间 → 残留预扣计数，由对账修正（`remaining = total - sold - inflight_queued`）。
- 重复/重试：幂等键重复（同 hash）经 Redis 快速命中 → 读 MySQL 既有订单返回；一人一单重复经 Redis 快速拒绝 → `12004`；均不重复扣库存。

V3 异步一致性（队列 vs MySQL）：

- 入队（HTTP 内）：Redis Lua 闸门（预扣 `remaining--` + 原子写 `bought`/`idem` 标记）→ `INSERT flash_sale_order_requests(status=queued)`。这两步非跨系统原子：若 INSERT 失败，补偿 Redis（`INCR remaining` + 清标记）并返回 5xx，不残留「已预扣但无 request」。
- 消费（后台）：出队 `FOR UPDATE SKIP LOCKED` → 单事务「活动校验 + 时间窗 + 条件扣 `sold` + `INSERT flash_sale_orders` + `requests.status=success`」。失败分两类：业务失败（12001~12007）落 `failed`；技术失败 `retry_count+1` + 退避 `next_attempt_at`，超上限落 `dead`。两类失败都补偿 Redis（`INCR remaining` + 清标记）。
- 消费幂等：请求级由 `uk_request_idempotency` 兜底（同一幂等键至多一个请求）；落单级由 `uk_flash_idempotency`/`uk_flash_one_per_user` 兜底；出队行由「`FOR UPDATE SKIP LOCKED` + 状态原子更新」保证只被领取一次。
- 乱序/部分完成：秒杀请求无顺序语义，乱序无影响；消费事务原子，`success` 要么全有要么全无。

### 5.4 降级容错

- V2：Redis 不可用或 Lua 执行失败 → 跳过 Redis 闸门、走 V1 纯 MySQL 路径（正确性由 MySQL 保证；售罄/穿透快速失败能力在该窗口失效）。
- **V3：Redis 不可用 → fail closed（5xx，`CodeServiceUnavailable=1005`），不创建 request、不预扣、不直接同步落单**。秒杀场景 Redis 正是高并发闸门，Redis 故障时把流量打回 MySQL 会在最需要保护数据库时绕过保护层；同一异步 API 不应同时存在「queued」与「同步成功」两套执行语义。Redis 闸门成功但 request INSERT 失败 → 补偿预扣/标记 + 5xx，不得留下「已预扣但无 request」残留。
- 注：前台会话也依赖同一 Redis（`iam:*`），全量 Redis 宕机时前台鉴权先 fail-closed。
- Redis 恢复后由预热/对账兜底收敛。Redis outage fallback 如需后续支持，应作为独立容量保护/降级设计处理，不复用 V1 绕过闸门。

### 5.5 对账

- 后台扫描器（`goroutine + ticker`，复用订单超时取消扫描范式，非 MQ、非 gcron）按「活动 × SKU」粒度，周期将 Redis `remaining` 刷成 `total_stock - MySQL sold - inflight_queued`（`inflight_queued` = 该活动×SKU 下 `status=queued` 的请求数），并回补缺失的活跃活动预热。
- 修正为无状态、幂等（写入权威值）、多实例并发安全。
- 无持久化补偿/对账记录表；不一致通过「把 Redis 刷成 MySQL 事实」收敛，观测依赖日志。
- V3 新增后台消费者扫描器（`goroutine + ticker`）：`FOR UPDATE SKIP LOCKED` 出队 `queued` 请求 → 消费落单；与对账扫描器并列，复用同一后台扫描范式。

## 6. Redis 热路径设计

### 6.1 Key 命名与 TTL

命名空间前缀 `flashsale:`（B 类 namespace，避免与 `iam:` 会话前缀冲突）：

| Key | 结构 | 语义 | TTL |
| --- | --- | --- | --- |
| `flashsale:activity:{activityID}` | Hash（status/start/end） | 活动元数据 | end_time − now + grace |
| `flashsale:stock:{activityID}:{skuID}` | 整数（remaining） | 剩余秒杀库存（预扣计数） | 同活动 |
| `flashsale:bought:{activityID}:{skuID}:{userID}` | 标记 | 一人一单（下单成功后写入） | 同活动 |
| `flashsale:idem:{userID}:{idempotencyKey}` | request_hash | 幂等（下单成功后写入） | 同活动 |
| `flashsale:soldout:{activityID}:{skuID}` | 标记 | 售罄 | 同活动 |
| `flashsale:null:{activityID}` | 标记 | 空值/负缓存（不存在/下架/未预热） | 短 TTL（如 60s） |

- TTL 关系：活动域 key 的 TTL ≤ 活动 `end_time` + grace（grace 为小缓冲，覆盖活动结束后在途请求），保证「结束/下架后缓存自动过期，不残留脏数据」；下架/更新时显式 DEL/重载。
- 一人一单/幂等标记 TTL 过期后不损失正确性：MySQL 唯一约束（`uk_flash_one_per_user`/`uk_flash_idempotency`）是永久兜底，标记仅为快速失败优化。

### 6.2 Lua 原子性边界（仅 Redis 内，不访问 MySQL）

抢购 Lua 脚本原子完成：空值标记检查 → 活动存在/启用/时间窗检查 → 售罄检查 → 幂等检查 → 一人一单检查 → 剩余库存检查与 DECR 预扣。返回码：`NOT_FOUND` / `NOT_IN_WINDOW` / `SOLD_OUT` / `ALREADY_PURCHASED` / `IDEMPOTENT_HIT` / `IDEMPOTENT_CONFLICT` / `GATE_PASSED`。

- 预扣成功（`GATE_PASSED`）≠ 下单成功；后续 MySQL 事务失败时由调用方补偿预扣。
- V2：一人一单/幂等标记**在 MySQL 下单成功后**才写入 Redis（无孤儿标记），Lua 仅对已存在的标记做快速失败/快速命中检查。
- V3：一人一单/幂等标记在 **`GATE_PASSED`（入队）时原子写入**（随预扣一起），用于去重并发入队；消费失败时清除标记 + 补偿预扣（避免孤儿标记）；`uk_request_idempotency`/`uk_flash_one_per_user` 仍是永久兜底。
- 检查顺序幂等先于一人一单：同幂等键重试（同 hash）应返回既有结果（成功/排队）而非已购（12004），与 V1 语义一致。

### 6.3 预热与缓存一致性

- 管理端创建/更新/下架活动在 MySQL 提交后同步维护 Redis 缓存（预热/失效/重载）。
- 后台扫描器兜底回补缺失的活跃活动预热，并执行对账。
- 不采用下单热路径的懒加载回源（避免 Redis 未命中时穿透 MySQL，违背防穿透目标）。

### 6.4 售罄快速失败与防穿透

- 售罄：Redis 库存耗尽后 Lua 置 `soldout` 标记，后续请求快速失败 `12003`，不穿透 MySQL 查询/扣减库存、不产生订单。
- 防穿透：不存在/已下架/未预热的活动或 SKU，经 `null` 空值标记（短 TTL）快速失败 `12001`，不穿透 Redis 到 MySQL。

## 7. 安全与权限边界

- 前台（`routes_frontend.go` `Auth` 分组，作用于 `Principal.UserID`）：
  - `POST /flash-sales/:id/orders`：秒杀下单（请求体 `{sku_id, idempotency_key}`，不提交价格/身份）。V3 起响应改为「排队受理结果」（`status=queued`），不再同步返回已创建订单。
  - `GET /flash-sales/:id/orders/result?idempotency_key=K`：查询本人秒杀下单结果（V3 新增），按 `flash_sale_id + user_id + idempotency_key` 验证归属；不存在/非本人统一 404（`CodeNotFound=1004`）。
- 后台写（`routes_admin.go` `AdminAuth` + `RequirePermission`）：
  - `POST /admin/flash-sales` → `flash_sale:create`
  - `PUT /admin/flash-sales/:id` → `flash_sale:update`
- V1/V2 不新增前台活动列表/详情、不新增后台活动列表（非 AC 必需）。
- 权限 code：`flash_sale:create`、`flash_sale:update`（`internal/boot/seed.go` 登记）。
- 身份信任：用户身份取自 `Principal.UserID`，管理员经 `RequirePermission`；均不信任请求自带身份。

## 8. 错误码域

V2/V3 复用 V1 域 12000-12999，不新增错误码（V3 结果查询复用 `1004`、fail-closed 复用 `1005`）：

| code | 语义 | HTTP |
| --- | --- | --- |
| 12001 | FLASH_SALE_ACTIVITY_NOT_FOUND（活动不存在/已下架/未预热） | 404 |
| 12002 | FLASH_SALE_NOT_IN_TIME_WINDOW（活动未开始或已结束） | 409 |
| 12003 | FLASH_SALE_STOCK_INSUFFICIENT（秒杀库存不足/售罄） | 409 |
| 12004 | FLASH_SALE_ALREADY_PURCHASED（一人一单，已购买） | 409 |
| 12005 | FLASH_SALE_IDEMPOTENCY_CONFLICT（同幂等键不同请求内容） | 409 |
| 12006 | FLASH_SALE_SKU_UNAVAILABLE（下单时 SKU 禁用或商品下架） | 409 |
| 12007 | FLASH_SALE_INVALID_ARGUMENT（活动参数非法：秒杀价/库存/时间/状态） | 400 |

复用：`1001`（400）、`1002`（401）、`1003`（403）、`4001`（商品不存在）、`5001`（SKU 不存在）。

## 9. 跨模块关系

- `flash_sale_activity_skus.sku_id`、`flash_sale_orders.sku_id`/`product_id`/`user_id`/`activity_id`、`flash_sale_order_requests.sku_id`/`user_id`/`activity_id` → 软引用（无 FK），下单/消费时经 `ISku.GetByID`/`IProduct.GetByID` 校验存在性与可用性（SKU `enabled`、商品 `on_shelf`），快照自足。
- `flash_sale_activity_skus.activity_id` → `flash_sale_activities.id`（FK `ON DELETE CASCADE`，模块内从属清理）。
- 秒杀库存与普通 `inventories` 的关系：秒杀库存是普通可售库存的预分配配额（语义预留），V1/V2 运行时独立、不联动、不自动划拨（见 INV-009）。
- 秒杀订单不复用 `orders`/`order_items`、不进入普通订单列表/支付/取消/退款流程。
- Redis：秒杀缓存使用独立 `flashsale:` 前缀，与会话 `iam:*` 隔离；秒杀后台扫描器复用订单超时取消扫描的 `goroutine + ticker` 范式，不引入 MQ/gcron。
- 建表经 golang-migrate 迁移（`20261001000011_flash_sale.up.sql` 建三表；`20261001000015_flash_sale_order_requests.up.sql` 建异步请求表，V3 新增），见 `migration.md`。

## 10. Deferred / 已知留白

- 自动划拨/预留：不实现秒杀配额从普通库存的自动划拨，依赖运营前置预留；后续任务应实现并在配额耗尽/普通库存侧扣减上形成机械不变量。
- 取消/退款/超时未支付与库存恢复：明确不做，后续需重新定义「成功订单」口径与库存恢复语义（`CONTRACT_REVISION`）。
- 死信/待修复管理接口：V3 死信（`status=dead`）修复仅提供运维 SQL/日志最小满足，管理接口（`dead→queued` 重处理）属后续按需扩展。
- Redis outage fallback（Redis 不可用时的容量保护/降级）：V3 采用 fail-closed，如需后续支持应作为独立容量保护/降级设计处理。
- 前台活动列表/详情、后台活动列表/统计：非 AC 必需，后续按需扩展。
- 秒杀 SKU 绑定为软引用：SKU 被删除后活动绑定悬空，下单时由 `ISku.GetByID` 校验拒绝（`5001`），属可接受边界。
- 「未预热」活动在预热/回补窗口内与「不存在/下架」共用 `12001` 语义；如需区分可后续新增错误码（当前不做）。
