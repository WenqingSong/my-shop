# 秒杀设计（Flash Sale）

本文面向项目接手者，说明「秒杀核心闭环」V1 的架构、数据模型、并发/一致性、权限边界与错误码域。事实来源为 `flash-sale-v1` 最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

秒杀回答「活动在什么时间、以什么价格、卖出多少」：管理员创建秒杀活动（绑定 SKU、秒杀价、秒杀库存、起止时间），登录用户在活动时间窗内以秒杀价下单，系统以「一人一单 + 请求幂等键 + 条件库存扣减」在单一 MySQL 上保证五个不变量（库存 ≥ 0、成功订单数 ≤ 初始库存、一人一单、失败不建单、重复请求不重复扣库存）。

边界：V1 只用 MySQL 证明正确性，不引入 Redis/Lua/MQ/限流/削峰/异步下单/缓存库存/活动预热。秒杀订单「下单即成交」：不做支付、取消、退款、超时恢复，秒杀库存一经扣减不因任何后续操作恢复。秒杀库存是**普通可售库存的预分配/预留活动配额**，运行时独立、抢购事务内不联动普通 `inventories`，V1 不实现自动划拨。不修改普通订单/库存/购物车/商品/SKU/身份模块行为（除下单时对 SKU/商品的只读校验）。

## 2. 数据模型

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
| `quantity` | INT UNSIGNED | 非空默认 1（V1 每次购买恒 1 件） |
| `idempotency_key` | VARCHAR(64) | 非空，客户端幂等键 |
| `request_hash` | VARCHAR(64) | 非空，请求指纹（sha256 hex） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_flash_order_no(order_no)` UNIQUE、`uk_flash_idempotency(user_id, idempotency_key)` UNIQUE、`uk_flash_one_per_user(activity_id, sku_id, user_id)` UNIQUE、`idx_activity(activity_id)`、`idx_user(user_id)`。

## 3. 状态机与生命周期

- 活动 `status`：二态 `enabled`（默认）/`disabled`（下架），创建默认 `enabled`，写接口直接设置与校验；「已结束」由 `end_time` 派生，不存冗余终态、无后台状态翻转任务。
- 秒杀订单无状态机：下单事务提交即成功（行存在 = 成功订单），无待支付/取消/退款。
- 可售判定（下单时，同事务 MySQL 条件写入）：`status=enabled` 且 `start_time <= NOW() < end_time`（左闭右开）。

## 4. 业务不变量

- INV-001（库存不为负）：任何成功下单后 `total_stock - sold ≥ 0`，并发下单亦不例外（条件更新 `sold < total_stock` 兜底 + `INT UNSIGNED` 类型兜底）。
- INV-002（成功订单数 ≤ 初始库存）：`sold ≤ total_stock` 且 `COUNT(flash_sale_orders) = sold`（同事务保证订单数与扣减一致）。
- INV-003（一人一单）：同一 `(activity_id, sku_id, user_id)` 至多一个成功订单，由 `uk_flash_one_per_user` 兜底；重复购买返回 `12004` 且无写入。
- INV-004（失败不建订单）：时间窗不符/库存不足/SKU 不可用/已购/参数非法/越权等失败请求不创建订单、不扣减秒杀库存、不留半成品（单事务回滚）。
- INV-005（幂等防重复扣减）：同一 `(user_id, idempotency_key)` 至多一个订单；同键同 `request_hash` 返回既有订单、不重复扣库存；同键不同 `request_hash` 返回 `12005`（`uk_flash_idempotency` 兜底）。
- INV-006（服务端定价与快照）：成交价 = 下单时 `flash_price` 重读快照，不信任客户端价与普通 SKU 价；下单后改价不影响已生成订单。
- INV-007（事务原子性）：下单 = 单事务「时间窗校验 + 一人一单/幂等校验 + 条件扣秒杀库存 + 创建订单」，任一步失败整体回滚。
- INV-008（权限）：管理员创建/更新活动需 `flash_sale:*`（含超管）；无权限 403 且无 DB 写入；用户身份取自 `Principal.UserID`，不信任请求自带身份。
- INV-009（配额预分配前提）：秒杀库存是普通可售库存的预分配/预留活动配额；全局不超卖依赖「秒杀配额已从普通库存划拨」。V1 不实现自动划拨，此约束为运营前置约定（非机械校验）；后续引入自动划拨时转为可机械验证的不变量。

## 5. 一致性模型与失败语义

- 事实来源：单一 MySQL。`flash_sale_activities`/`flash_sale_activity_skus`（活动与秒杀库存事实）、`flash_sale_orders`（秒杀订单事实，软引用快照自足）、`skus`/`products`（下单时存在性与可用性事实来源，读后快照）。
- 成功语义：下单成功 = 秒杀库存 `sold` 增加 1 且秒杀订单落库，同事务提交；不代表普通库存变更（V1 不联动）、不代表可取消。
- 原子性：下单为单事务，无跨系统/跨存储，不存在部分完成或中间态残留。
- 并发：同一 SKU 并发下单由 `sold < total_stock` 条件更新 + 行锁串行化，成功扣减总量 = 初始 − 剩余、成功次数 ≤ 初始库存；一人一单与幂等由 DB 唯一约束兜底「先查再写」窗口。
- 重复/重试：幂等键重复（同 hash）返回既有订单；一人一单重复返回 `12004`；均不重复扣库存。
- 失败语义：时间窗不符 → `12002`；库存不足 → `12003`；已购 → `12004`；幂等冲突 → `12005`；SKU/商品不可用 → `12006`/`5001`/`4001`；越权 → 403；参数非法 → `12007`/`1001`；DB 技术错误 → `1000`（500），均无写入、不泄漏底层细节。
- 无超时取消/退款：V1 不引入取消/超时未支付/退款，秒杀库存不因任何后续操作恢复。
- **库存配额前提（V1 运营前置约定，非机械强制）**：秒杀库存 `total_stock` 语义上是从该 SKU 普通可售库存中「预分配/预留的活动配额」，非凭空新增可售库存；全局不超卖的前提为「秒杀配额 + 普通可售库存 ≤ 真实库存」。V1 不实现自动划拨：不自动扣减/冻结普通 `inventories` 做预留，配额划拨由运营/管理员在创建活动前完成；若未按前提预留，可能出现普通 + 秒杀双池合计超卖，属 V1 已知边界（本任务五个不变量均限定在秒杀库存域内）。

## 6. 安全与权限边界

- 前台（`routes_frontend.go` `Auth` 分组，作用于 `Principal.UserID`）：
  - `POST /flash-sales/:id/orders`：秒杀下单（请求体 `{sku_id, idempotency_key}`，不提交价格/身份）。
- 后台写（`routes_admin.go` `AdminAuth` + `RequirePermission`）：
  - `POST /admin/flash-sales` → `flash_sale:create`
  - `PUT /admin/flash-sales/:id` → `flash_sale:update`
- V1 不新增前台活动列表/详情、不新增后台活动列表（非 AC 必需）。
- 权限 code：`flash_sale:create`、`flash_sale:update`（`internal/boot/seed.go` 登记）。
- 身份信任：用户身份取自 `Principal.UserID`，管理员经 `RequirePermission`；均不信任请求自带身份。

## 7. 错误码域

| code | 语义 | HTTP |
| --- | --- | --- |
| 12001 | FLASH_SALE_ACTIVITY_NOT_FOUND（活动不存在或已下架） | 404 |
| 12002 | FLASH_SALE_NOT_IN_TIME_WINDOW（活动未开始或已结束） | 409 |
| 12003 | FLASH_SALE_STOCK_INSUFFICIENT（秒杀库存不足） | 409 |
| 12004 | FLASH_SALE_ALREADY_PURCHASED（一人一单，已购买） | 409 |
| 12005 | FLASH_SALE_IDEMPOTENCY_CONFLICT（同幂等键不同请求内容） | 409 |
| 12006 | FLASH_SALE_SKU_UNAVAILABLE（下单时 SKU 禁用或商品下架） | 409 |
| 12007 | FLASH_SALE_INVALID_ARGUMENT（活动参数非法：秒杀价/库存/时间/状态） | 400 |

复用：`1001`（400）、`1002`（401）、`1003`（403）、`4001`（商品不存在）、`5001`（SKU 不存在）。

## 8. 跨模块关系

- `flash_sale_activity_skus.sku_id`、`flash_sale_orders.sku_id`/`product_id`/`user_id`/`activity_id` → 软引用（无 FK），下单时经 `ISku.GetByID`/`IProduct.GetByID` 校验存在性与可用性（SKU `enabled`、商品 `on_shelf`），快照自足。
- `flash_sale_activity_skus.activity_id` → `flash_sale_activities.id`（FK `ON DELETE CASCADE`，模块内从属清理）。
- 秒杀库存与普通 `inventories` 的关系：秒杀库存是普通可售库存的预分配配额（语义预留），V1 运行时独立、不联动、不自动划拨（见 INV-009）。
- 秒杀订单不复用 `orders`/`order_items`、不进入普通订单列表/支付/取消/退款流程。
- 建表经 golang-migrate 迁移（`20261001000011_flash_sale.up.sql`），见 `migration.md`。

## 9. Deferred / 已知留白

- 自动划拨/预留：V1 不实现秒杀配额从普通库存的自动划拨，依赖运营前置预留；后续任务应实现并在配额耗尽/普通库存侧扣减上形成机械不变量。
- 取消/退款/超时未支付与库存恢复：V1 明确不做，后续需重新定义「成功订单」口径与库存恢复语义（`CONTRACT_REVISION`）。
- 前台活动列表/详情、后台活动列表/统计：非 V1 AC 必需，后续按需扩展。
- 秒杀 SKU 绑定为软引用：SKU 被删除后活动绑定悬空，下单时由 `ISku.GetByID` 校验拒绝（`5001`），属可接受边界。
