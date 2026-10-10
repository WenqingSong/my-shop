# 订单设计（Order）

本文面向项目接手者，说明「普通订单核心闭环」的架构、数据模型、状态机、并发/一致性与权限边界。事实来源为 `order-v1` 最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

订单回答「用户买到了什么、多少钱、发到哪里、现在处于哪个状态」，承载订单主数据（`orders`）与订单项（`order_items`），实现：下单（购物车勾选/直接购买）、服务端定价与快照、唯一订单号、幂等去重、状态机推进、取消/退款恢复库存、超时未支付自动取消、支付 Mock（幂等）、用户隔离、管理员发货/退款权限，以及后台订单只读管理（分页列表 + 组合筛选 + 详情 + 状态统计）。

边界：不接入真实支付平台/网关/退款到账/对账/资金回调（支付仅 Mock）；不做物流/快递单号/配送轨迹；不做评价/售后/退换货（`shipped/received/completed` 后的退款/退货/售后留给未来 After-sales/Refund 模块）；不做优惠券/促销/积分/计价引擎；不修改既有购物车/库存/地址/SKU/商品/分类/身份模块行为（除库存 tx 协同、必要的只读引用与购物车下单删已购条目）。事实来源为单一 MySQL；无 MQ；Redis 仅会话。

## 2. 数据模型

### 2.1 `orders`（订单主数据）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `order_no` | VARCHAR(32) | 非空，唯一 `uk_order_no`（全局唯一订单号） |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），取自 `Principal.UserID` |
| `status` | TINYINT | 非空默认 10（见状态机） |
| `total_amount` | INT UNSIGNED | 非空，成交总价整数分快照（= Σ 订单项 `price×quantity`） |
| `idempotency_key` | VARCHAR(64) | 非空，客户端幂等键 |
| `request_hash` | VARCHAR(64) | 非空，请求指纹（sha256 hex），用于同 key 判内容一致 |
| `recipient_name`/`phone`/`province`/`city`/`district`/`detail` | VARCHAR(32/20/32/32/32/255) | 非空，收货地址快照 |
| `address_id` | BIGINT UNSIGNED | 可空，软引用（无 FK），仅追溯 |
| `expire_at` | DATETIME | 非空，支付截止 = `created_at + order.pay_timeout` |
| `cancel_reason` | TINYINT | 可空，`1=用户取消`、`2=超时取消` |
| `paid_at`/`shipped_at`/`received_at`/`completed_at`/`cancelled_at`/`refunded_at` | DATETIME | 可空，各状态推进时间 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_order_no(order_no)`、`uk_user_idempotency(user_id, idempotency_key)`、`idx_user_id(user_id)`、`idx_status_expire(status, expire_at)`（支撑超时扫描）。

**快照自足**：`user_id`/`address_id` 软引用；商品/SKU/价格/地址均快照，SKU 或地址被删除后订单仍可读（与 cart 软引用约定一致）。

### 2.2 `order_items`（订单项）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `order_id` | BIGINT UNSIGNED | 非空，FK → `orders.id` `ON DELETE CASCADE` |
| `sku_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），指向 `skus.id` |
| `product_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），指向 `products.id` |
| `sku_name` | VARCHAR(128) | 非空，快照 |
| `product_name` | VARCHAR(128) | 非空，快照 |
| `product_main_image` | VARCHAR(512) | 非空默认 `''`，快照 |
| `price` | INT UNSIGNED | 非空，成交价整数分 = 下单时 `skus.price` |
| `quantity` | INT UNSIGNED | 非空，正整数 |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`idx_order_id(order_id)`。

### 2.3 成交价语义

成交价为下单时 `skus.price`（整数分）重读结果，不信任客户端提交价、也不信任购物车 `price_snapshot`；订单快照为「锁定成交价」，下单后改价不影响订单。`products.price` 与订单无关（订单为 SKU 粒度）。

## 3. 状态机与合法迁移

| from | to | 触发者 | 备注 |
| --- | --- | --- | --- |
| 待支付(10) | 已支付(20) | 用户 mock 支付（本人） | 幂等 |
| 待支付(10) | 已取消(60) | 用户取消 / 超时自动取消 | 恢复库存 |
| 已支付(20) | 已发货(30) | 管理员发货（`order:ship`） | |
| 已支付(20) | 已退款(70) | 管理员退款（`order:refund`） | 恢复库存，仅「已支付、未发货」可退 |
| 已发货(30) | 已收货(40) | 用户确认收货（本人） | |
| 已收货(40) | 已完成(50) | 系统（确认收货同请求内原子推进） | 终态 |

终态：已完成(50)、已取消(60)、已退款(70)。非法迁移返回 9002 且状态不变。

退款边界：Order V1 仅 `paid → refunded`；`shipped/received/completed` 后的退款、退货、售后不在 Order V1（留未来 After-sales/Refund 模块）。`cancelled`（未支付取消）与 `refunded`（已支付退款）语义不同，不可互转。

## 4. 业务不变量

- INV-001（归属与隔离）：`orders.user_id` 取自 `Principal.UserID`；用户只能查看/取消/支付/收货自己的订单；跨用户操作返回 9001（404）且无写入。
- INV-002（幂等去重）：同一 `(user_id, idempotency_key)` 至多产生一个成功订单（`uk_user_idempotency` 兜底）；同 key 同 `request_hash` 返回既有订单、不重复扣库存；同 key 不同 `request_hash` 返回 9006。
- INV-003（事务原子性）：创建订单 = 单事务「写 orders + 写 order_items + 扣库存 +（购物车模式）删已购条目」，任一步失败整体回滚，无半成品、库存与购物车均不变。
- INV-004（服务端定价与快照）：成交价 = 下单时 `skus.price`；快照自足，改价/改地址不影响订单。
- INV-005（状态机）：仅合法迁移可推进；非法迁移 9002 且状态不变；`refunded` 仅从 `paid` 可达。
- INV-006（防超卖）：并发同一 SKU 下单成功扣减总量 ≤ 可售库存且库存 ≥ 0（条件扣减 + `RowsAffected`）。
- INV-007（取消/退款只补偿一次）：同一订单取消/退款仅恢复一次库存（条件状态更新 + `RowsAffected` 原子闸门）。
- INV-008（权限）：发货/退款仅管理员（含超管，持 `order:ship`/`order:refund`）；无权限 403、越权 404，均无 DB 副作用。
- INV-009（唯一订单号）：`order_no` 全局唯一（`uk_order_no` 兜底，撞号重试）。
- INV-010（支付幂等）：重复支付回调不重复改状态，仅待支付→已支付合法。

## 5. 一致性模型与失败语义

- 事实来源：单一 MySQL。`orders`/`order_items`（订单事实）、`inventories`（库存事实）、`inventory_logs`（库存流水，只追加）、`skus`（下单时价格/状态事实来源，读后快照）。Redis 仅会话；无 MQ、无跨系统事务。
- 成功语义：创建成功 = orders + order_items + 库存扣减（+ 购物车条目删除）同事务提交；取消/退款成功 = 状态变更 + 库存恢复同事务；支付/发货/收货成功 = 状态推进。
- 原子性：下单与取消/退款均为单事务；库存变更复用 `IInventory` 的 tx 感知方法（`DeductInTx`/`IncreaseInTx`），Order 只协调外层事务、不复制扣减/增加业务规则。
- 幂等与重复：幂等键重复（同 hash）→ 返回既有订单；支付/取消/退款重复 → 状态机 + 条件更新保证不重复改状态/不重复补偿。
- 超时取消：后台 scanner（ticker，周期 `order.cancel_scan_interval`）扫描 `status=10 AND expire_at < now` 逐单原子取消；查询/支付入口懒取消兜底；多实例并发靠「条件状态 UPDATE + RowsAffected」原子闸门，仅迁移成功者补偿库存。
- 失败语义：库存不足 → 整体回滚 6001；非法迁移 → 9002 状态不变；幂等冲突 → 9006；越权 → 403/404 无写入；DB 技术错误 → 1000（500）。
- 部分完成：无跨系统，不存在部分完成/中间态残留。

## 6. 安全与权限边界

- 前台受保护路由（`routes_frontend.go` Auth 分组）：`POST /orders`、`GET /orders`、`GET /orders/:id`、`POST /orders/:id/pay`、`POST /orders/:id/cancel`、`POST /orders/:id/receive`。
- 后台写路由（`routes_admin.go` require 分组）：`POST /admin/orders/:id/ship`（`order:ship`）、`POST /admin/orders/:id/refund`（`order:refund`）。
- 后台只读路由（`routes_admin.go` require 分组，AdminAuth + RequirePermission）：`GET /admin/orders`（`order:list`）、`GET /admin/orders/stats`（`order:list`）、`GET /admin/orders/:id`（`order:view`）。
- 身份信任：`Principal.UserID` 是用户侧唯一身份来源；发货/退款/后台只读查询仅管理员（含超管），经 `RequirePermission`；普通用户 token（`type=user`）访问后台路由返回 403。
- 越权语义：用户越权与不存在统一 9001（404，防枚举）；后台详情按 id 查任意订单（无归属过滤），不存在返回 9001（404）；无权限管理员 403；均无 DB 副作用。
- 权限 code：`order:ship`、`order:refund`、`order:list`（列表 + 统计）、`order:view`（详情）（seed 登记 `internal/boot/seed.go`）。
- 只读查询语义：后台列表为轻量分页（不含订单明细，杜绝 N+1），支持 `order_no`/`user_id`/`status`/`start_time`/`end_time` 组合筛选与 `{id, created_at}` 白名单稳定排序（默认 `id DESC`，恒以 `id` 作 tiebreak）；详情返回历史快照（不重读 `skus.price`）；统计按 `status` `GROUP BY` 覆盖 7 态。时间范围入参为 RFC3339（含时区偏移），经 `FROM_UNIXTIME(Unix秒)` 在 MySQL 会话时区内转换，与 `created_at`（同会话时区 `NOW()` 写入）同基准比较，左闭右开 `[start,end)`。

## 7. 错误码域

| code | 语义 | HTTP |
| --- | --- | --- |
| 9001 | ORDER_NOT_FOUND（订单不存在或不属于当前用户） | 404 |
| 9002 | ORDER_INVALID_STATUS_TRANSITION（非法状态迁移） | 409 |
| 9003 | ORDER_SKU_UNAVAILABLE（下单时 SKU 禁用或商品下架） | 409 |
| 9004 | ORDER_CART_EMPTY（购物车下单无勾选项） | 400 |
| 9005 | ORDER_INVALID_QUANTITY（直接购买数量非法） | 400 |
| 9006 | ORDER_IDEMPOTENCY_CONFLICT（同 key 不同请求内容） | 409 |

复用：`1001`（400）、`1002`（401）、`1003`（403）、`5001`（SKU 不存在）、`6001`（库存不足）、`7001`（地址不存在）。

## 8. 跨模块关系

- `orders.user_id` → `users.id`（软引用，无 FK），取自 `Principal.UserID`。
- `order_items.sku_id`/`product_id` → `skus.id`/`products.id`（软引用，无 FK），快照自足；SKU/商品删除后订单仍可读。
- 库存：下单经 `IInventory.DeductInTx` 扣减、取消/退款经 `IInventory.IncreaseInTx` 恢复，复用其「条件更新 + RowsAffected + 流水」规则；`operator_admin_id` 落 NULL（用户/系统驱动）。
- 定价：成交价重读 `ISku.GetByID` 的 `skus.price`，校验 SKU enabled 且商品 on_shelf（否则 9003）。
- 地址：快照自 `addresses`，下单后地址变更不影响订单。
- 建表经 golang-migrate 迁移（`20261001000007_orders.up.sql`），见 `migration.md`。
- 配置：`order.pay_timeout`（支付超时秒，默认 900）、`order.cancel_scan_interval`（扫描周期秒，默认 60）。

## 9. Deferred / 已知留白

- 真实支付/退款到账/对账/资金回调（支付仅 Mock）。
- 物流/快递单号、配送轨迹、发货单号回传。
- `shipped/received/completed` 后的退款/退货/售后（留 After-sales/Refund 模块）。
- 订单导出、收货人姓名/手机号模糊搜索等高级筛选（后台列表已支持 `order_no`/`user_id`/`status`/`start_time`/`end_time` 组合筛选与 7 态状态统计）。
- 库存流水 `reason` 字段固定为空，订单驱动的扣减/恢复不落 `reason`，审计粒度有限。
- 后台扫描 goroutine 的优雅停机（依赖条件更新幂等保证半次扫描安全）。
