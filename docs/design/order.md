# 订单设计（Order）

本文面向项目接手者，说明「普通订单核心闭环」的架构、数据模型、状态机、并发/一致性与权限边界。事实来源为 `order-v1` 最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

订单回答「用户买到了什么、多少钱、发到哪里、现在处于哪个状态」，承载订单主数据（`orders`）与订单项（`order_items`），实现：下单（购物车勾选/直接购买）、服务端定价与快照、唯一订单号、幂等去重、状态机推进、取消恢复库存、超时未支付自动取消、支付 Mock（幂等）、用户隔离、管理员发货权限，以及后台订单只读管理（分页列表 + 组合筛选 + 详情 + 状态统计）。退款由 refund 模块负责（见 `refund.md`），订单侧仅提供退款成功推进的 tx 复用方法。

边界：不接入真实支付平台/网关/退款到账/对账/资金回调（支付仅 Mock）；不做物流/快递单号/配送轨迹；不做评价/售后/退换货（`shipped/received/completed` 后的退款/退货/售后留给未来 After-sales/Refund 模块）；不做优惠券/促销/积分/计价引擎；不修改既有购物车/库存/地址/SKU/商品/分类/身份模块行为（除库存 tx 协同、必要的只读引用与购物车下单删已购条目）。事实来源为单一 MySQL；无 MQ；Redis 仅会话。

## 2. 数据模型

### 2.1 `orders`（订单主数据）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `order_no` | VARCHAR(32) | 非空，唯一 `uk_order_no`（全局唯一订单号） |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），取自 `Principal.UserID` |
| `status` | TINYINT | 非空默认 10（见状态机） |
| `total_amount` | INT UNSIGNED | 非空，应付金额整数分快照（= `original_amount` - `discount_amount` - `member_discount_amount`） |
| `original_amount` | INT UNSIGNED | 非空，优惠前金额快照（= Σ 订单项 `price×quantity`） |
| `discount_amount` | INT UNSIGNED | 非空，**优惠券**优惠金额快照（默认 0）；会员折扣不写此列 |
| `member_discount_amount` | INT UNSIGNED | 非空默认 0，**会员等级折扣**金额快照（B1-04-08 新增，`20261110000015`；无折扣为 0） |
| `shipping_fee` | INT UNSIGNED | 非空默认 0，运费快照（整数分）；历史订单保持 0，迁移不重算 `total_amount` |
| `user_coupon_id` | BIGINT UNSIGNED | 可空，下单时使用的用户券 id（软引用，快照，见 `coupon.md`） |
| `pay_type` | VARCHAR(16) | 非空默认 ''，支付方式（Mock；空串=未支付/未选择，见 §2.5） |
| `transaction_no` | VARCHAR(64) | 非空默认 ''，支付交易编号（Mock；仅追溯，不保证全局唯一） |
| `idempotency_key` | VARCHAR(64) | 非空，客户端幂等键 |
| `request_hash` | VARCHAR(64) | 非空，请求指纹（sha256 hex），用于同 key 判内容一致 |
| `recipient_name`/`phone`/`province`/`city`/`district`/`detail` | VARCHAR(32/20/32/32/32/255) | 非空，收货地址快照 |
| `address_id` | BIGINT UNSIGNED | 可空，软引用（无 FK），仅追溯 |
| `expire_at` | DATETIME | 非空，支付截止 = `created_at + order.pay_timeout` |
| `cancel_reason` | TINYINT | 可空，`1=用户取消`、`2=超时取消` |
| `paid_at`/`shipped_at`/`received_at`/`completed_at`/`cancelled_at`/`refunded_at` | DATETIME | 可空，各状态推进时间 |
| `delivery_type` | VARCHAR(32) | 非空默认 ''，物流方式（内嵌履约信息） |
| `delivery_name` | VARCHAR(64) | 非空默认 ''，物流公司名称 |
| `delivery_sn` | VARCHAR(64) | 非空默认 ''，物流单号 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_order_no(order_no)`、`uk_user_idempotency(user_id, idempotency_key)`、`idx_user_id(user_id)`、`idx_status_expire(status, expire_at)`（支撑超时扫描）。

**快照自足**：`user_id`/`address_id` 软引用；商品/SKU/价格/地址均快照，SKU 或地址被删除后订单仍可读（与 cart 软引用约定一致）。

### 2.2 `order_items`（订单项）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `order_id` | BIGINT UNSIGNED | 非空，FK → `orders.id` `ON DELETE CASCADE` |
| `sku_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），指向 `skus.id` |
| `sku_code` | VARCHAR(64) | 非空默认 ''，下单时 `skus.sku_code` 快照（B1-04-02）；历史订单为空串 |
| `product_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），指向 `products.id` |
| `sku_name` | VARCHAR(128) | 非空，快照 |
| `spec_text` | VARCHAR(128) | 非空默认 ''，下单时 SKU 规格文本快照（如 `红色,S`）；历史订单为空串，不回填 |
| `product_name` | VARCHAR(128) | 非空，快照 |
| `product_main_image` | VARCHAR(512) | 非空默认 `''`，快照 |
| `price` | INT UNSIGNED | 非空，成交价整数分 = 下单时 `skus.price` |
| `original_price` | INT UNSIGNED | 非空默认 0，原价快照（下单时 `skus.original_price`，B1-04-02） |
| `quantity` | INT UNSIGNED | 非空，正整数 |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`idx_order_id(order_id)`。

### 2.3 金额快照语义

订单金额分四列快照：`original_amount`（优惠前金额）= Σ `order_items.price×quantity`（下单时重读 `skus.price`，不信任客户端提交价、也不信任购物车 `price_snapshot`）；`discount_amount`（**券**优惠，由服务端按已选券计算，无券为 0）；`member_discount_amount`（**会员折扣**，按当前等级折扣率计算并按券后剩余金额夹紧，B1-04-08）；`total_amount`（应付金额）= `original_amount - discount_amount - member_discount_amount + shipping_fee`。订单快照为「锁定金额」，下单后改价/改模板不影响订单；`products.price` 与订单无关（订单为 SKU 粒度）。无券且无会员折扣时 `original_amount = total_amount`、`discount_amount = 0`、`member_discount_amount = 0`，
与历史订单语义一致（历史订单迁移回填 `original_amount = total_amount`，`member_discount_amount` 默认 0）。
另见 `pricing.md` 中的叠加顺序与夹紧规则：两项优惠合计不超过 `original_amount`。

`shipping_fee` 为运费快照（整数分），由 migration `20261110000004_order_shipping_payment` 新增，默认 0。B1-04-03 已实现运费计算模块（`service.IFreight.Quote`，见 `freight.md`）与统一计价入口（`pricing.Quote`，见 `pricing.md`），但**订单链路尚未接入**：下单仍写 `shipping_fee = 0`，`total_amount = original_amount - discount_amount` 仍等价成立。

订单接入的边界（待 B1-04-02 的 SKU 规格快照改造进入共同基线后实施）：

- 新增 `/orders/quote` 返回六槽位明细（`original_amount`/`coupon_discount`/`member_discount`/`points_discount`/`shipping_fee`/`payable_amount`）；
- 下单事务内重读 SKU 价格、商品状态、券有效性、运费模板与区域规则后重新计价，落库金额取事务内结果；
- 幂等指纹纳入 address_id、coupon_id、SKU 与数量等全部计价参数；
- 状态推进同事务写入 `order_status_logs`。

在接入完成前，订单金额仍以数据库现有三列 + `shipping_fee` 快照为准。历史订单不重算 `total_amount`（保持迁移前已存值）。

### 2.4 `order_status_logs`（订单状态流水）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `order_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），指向 `orders.id`；一单多条 |
| `from_status` | TINYINT | 非空，变更前状态；创建事件写 0（无前态） |
| `to_status` | TINYINT | 非空，变更后状态，取值与 `orders.status` 一致（10/20/30/40/50/60/70） |
| `change_type` | VARCHAR(32) | 非空，语义类型：create/pay/cancel/timeout_cancel/ship/receive/complete/refund |
| `operator_type` | TINYINT | 非空，`1=user`、`2=admin`、`3=system`（沿用并扩展 `refund_audits` 约定） |
| `operator_id` | BIGINT UNSIGNED | 非空默认 0，操作者 id（软引用）；system 驱动写 0 |
| `remark` | VARCHAR(255) | 非空默认 '' |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`idx_order_time(order_id, created_at)`（按订单时间线查询）。流水 append-only，无 UPDATE/DELETE 业务路径；**当前尚未写入**（既有状态推进逻辑未改），由后续 B1-04 在状态推进事务内补齐。

### 2.5 支付与履约字段（内嵌模型）

- 支付仅 Mock：`pay_type`（如 `mock`）、`transaction_no`、`paid_at` 记录支付事实；**不新增独立 `payments` 表，也不新增支付状态列**。当前 Mock 支付是「待支付→已支付」的一次性幂等推进，没有多次支付尝试/渠道流水/对账需求，订单现有字段足够承载；若未来出现多次支付尝试或独立对账语义，需先向 Owner 报告再扩展模型。
- 订单状态仍是唯一状态机（10..70）：是否已支付由 `status` 与 `paid_at` 判定，`pay_type`/`transaction_no` 不参与状态迁移判定，避免出现第二套支付状态机。
- 履约信息内嵌：`delivery_type`/`delivery_name`/`delivery_sn` 默认空串，随发货动作写入；不引入物流子表、物流轨迹或外部物流服务，发货仍由 `status=30` 推进。

## 3. 状态机与合法迁移

| from | to | 触发者 | 备注 |
| --- | --- | --- | --- |
| 待支付(10) | 已支付(20) | 用户 mock 支付（本人） | 幂等；同事务内发放会员成长值、计提一级分销佣金 |
| 待支付(10) | 已取消(60) | 用户取消 / 超时自动取消 | 恢复库存 |
| 已支付(20) | 已发货(30) | 管理员发货（`order:ship`） | |
| 已支付(20) | 已退款(70) | 退款成功（refund 模块执行） | 恢复库存 + 冲回佣金 + 扣减成长值，仅「已支付、未发货」可退（见 `refund.md`） |
| 已发货(30) | 已收货(40) | 用户确认收货（本人） | |
| 已收货(40) | 已完成(50) | 系统（确认收货同请求内原子推进） | 终态 |

终态：已完成(50)、已取消(60)、已退款(70)。非法迁移返回 9002 且状态不变。

退款边界：`paid → refunded` 的触发者为 refund 模块的「退款成功」（见 `refund.md`），非管理员直接退款；`shipped/received/completed` 后的退款、退货、售后不在 Order V1。`cancelled`（未支付取消）与 `refunded`（已支付退款）语义不同，不可互转。

## 4. 业务不变量

- INV-001（归属与隔离）：`orders.user_id` 取自 `Principal.UserID`；用户只能查看/取消/支付/收货自己的订单；跨用户操作返回 9001（404）且无写入。
- INV-002（幂等去重）：同一 `(user_id, idempotency_key)` 至多产生一个成功订单（`uk_user_idempotency` 兜底）；同 key 同 `request_hash` 返回既有订单、不重复扣库存；同 key 不同 `request_hash` 返回 9006。
- INV-003（事务原子性）：创建订单 = 单事务「写 orders + 写 order_items + 扣库存 +（若用券）预占券 +（购物车模式）删已购条目」，任一步失败整体回滚，无半成品、库存、券与购物车均不变。
- INV-003b（支付事务边界）：支付成功 = 单事务「订单 `10→20` + 券核销 + 会员成长值发放 + 一级佣金计提」，任一步失败整体回滚；
  两步联动均幂等（成长值 `growth:order_reward:<id>`、佣金 `uk_order_id`），重复支付不重复发放/计提。
- INV-004（服务端定价与快照）：`original_amount` = Σ 下单时 `skus.price×quantity`；
  `total_amount = original_amount - discount_amount - member_discount_amount + shipping_fee`
  （`discount` 与 `member_discount` 均由服务端计算且两者之和 ≤ `original`，故 `total ≥ 0`；`shipping_fee` 当前恒 0）；
  快照自足，改价/改模板/改等级/改地址不影响订单。
- INV-005（状态机）：仅合法迁移可推进；非法迁移 9002 且状态不变；`refunded` 仅从 `paid` 可达。
- INV-006（防超卖）：并发同一 SKU 下单成功扣减总量 ≤ 可售库存且库存 ≥ 0（条件扣减 + `RowsAffected`）。
- INV-007（取消/退款只补偿一次）：同一订单取消/退款仅恢复一次库存（条件状态更新 + `RowsAffected` 原子闸门）。
- INV-008（权限）：发货仅管理员（含超管，持 `order:ship`）；退款走 refund 模块（`refund:*` 权限，见 `refund.md`）；无权限 403、越权 404，均无 DB 副作用。
- INV-009（唯一订单号）：`order_no` 全局唯一（`uk_order_no` 兜底，撞号重试）。
- INV-010（支付幂等）：重复支付回调不重复改状态，仅待支付→已支付合法。
- INV-011（运费历史兼容）：`shipping_fee` 默认 0，迁移不回填、不重算历史 `total_amount`；运费计算模块已由 B1-04-03 交付，但订单链路尚未接入（当前恒写 0），接入条件与范围见 §2.3 与 §9。
- INV-012（支付事实单一）：`pay_type`/`transaction_no`/`paid_at` 只记录支付事实，不构成第二套支付状态机；订单是否已支付以 `status` + `paid_at` 判定，Mock 支付仍仅 `10→20` 一次推进。
- INV-013（状态流水只追加）：`order_status_logs` 每个订单可多条、状态值取自 10..70；流水写入必须与对应状态推进同事务（B1-04 实现），当前尚未写入，不改变既有状态机行为。

## 5. 一致性模型与失败语义

- 事实来源：单一 MySQL。`orders`/`order_items`（订单事实）、`order_status_logs`（状态流水，只追加，当前未写入）、`inventories`（库存事实）、`inventory_logs`（库存流水，只追加）、`skus`（下单时价格/状态事实来源，读后快照）。Redis 仅会话；无 MQ、无跨系统事务。
- 成功语义：创建成功 = orders + order_items + 库存扣减（+ 购物车条目删除）（+ 用券预占）同事务提交；取消成功 = 状态变更 + 库存恢复 + 券释放同事务；支付成功 = 状态推进 + 券核销 + 一级分销计佣（`IDistribution.AccrueOrderCommissionInTx`，无推荐人/比例为 0 时 no-op）同事务；退款成功 = 状态变更 + 库存恢复 + 佣金冲回（不返券）；发货/收货成功 = 状态推进。
- 原子性：下单与取消/退款均为单事务；库存变更复用 `IInventory` 的 tx 感知方法（`DeductInTx`/`IncreaseInTx`），Order 只协调外层事务、不复制扣减/增加业务规则。
- 幂等与重复：幂等键重复（同 hash）→ 返回既有订单；支付/取消/退款重复 → 状态机 + 条件更新保证不重复改状态/不重复补偿。
- 超时取消：后台 scanner（ticker，周期 `order.cancel_scan_interval`）扫描 `status=10 AND expire_at < now` 逐单原子取消；查询/支付入口懒取消兜底；多实例并发靠「条件状态 UPDATE + RowsAffected」原子闸门，仅迁移成功者补偿库存。
- 失败语义：库存不足 → 整体回滚 6001；非法迁移 → 9002 状态不变；幂等冲突 → 9006；越权 → 403/404 无写入；DB 技术错误 → 1000（500）。
- 部分完成：无跨系统，不存在部分完成/中间态残留。

## 6. 安全与权限边界

- 前台受保护路由（`routes_frontend.go` Auth 分组）：`POST /orders`、`GET /orders`、`GET /orders/:id`、`POST /orders/:id/pay`、`POST /orders/:id/cancel`、`POST /orders/:id/receive`。
- 后台写路由（`routes_admin.go` require 分组）：`POST /admin/orders/:id/ship`（`order:ship`）。退款后台路由见 `refund.md`（`/admin/refunds/*`，`refund:*` 权限）。
- 后台只读路由（`routes_admin.go` require 分组，AdminAuth + RequirePermission）：`GET /admin/orders`（`order:list`）、`GET /admin/orders/stats`（`order:list`）、`GET /admin/orders/:id`（`order:view`）。
- 身份信任：`Principal.UserID` 是用户侧唯一身份来源；发货/退款/后台只读查询仅管理员（含超管），经 `RequirePermission`；普通用户 token（`type=user`）访问后台路由返回 403。
- 越权语义：用户越权与不存在统一 9001（404，防枚举）；后台详情按 id 查任意订单（无归属过滤），不存在返回 9001（404）；无权限管理员 403；均无 DB 副作用。
- 权限 code：`order:ship`、`order:list`（列表 + 统计）、`order:view`（详情）（seed 登记 `internal/boot/seed.go`）；退款权限为 `refund:list`/`refund:view`/`refund:review`/`refund:execute`（见 `refund.md`）。
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

复用：`1001`（400）、`1002`（401）、`1003`（403）、`5001`（SKU 不存在）、`6001`（库存不足）、`7001`（地址不存在）、`19003`（券不存在）、`19007`（券不可用，见 `coupon.md`）。

## 8. 跨模块关系

- `orders.user_id` → `users.id`（软引用，无 FK），取自 `Principal.UserID`。
- `order_items.sku_id`/`product_id` → `skus.id`/`products.id`（软引用，无 FK），快照自足；SKU/商品删除后订单仍可读。
- 库存：下单经 `IInventory.DeductInTx` 扣减、取消经 `IInventory.IncreaseInTx` 恢复，复用其「条件更新 + RowsAffected + 流水」规则；`operator_admin_id` 落 NULL（用户/系统驱动）。
- 退款：退款成功推进订单 `paid→refunded` 由 refund 模块经 `IOrder.CompleteRefundInTx` 复用（订单 `20→70` + `refunded_at` + 恢复库存，`WHERE status=20` 闸门），见 `refund.md`。
- 定价：`original_amount` 重读 `skus.price` 求和，校验 SKU enabled 且商品 on_shelf（否则 9003）；用券经 `ICoupon` 校验归属/有效期/状态/门槛并计算 `discount_amount`。金额统一由 `pricing.Quote` 产出，运费由 `IFreight.Quote` 产出，见 `pricing.md`、`freight.md`。
- SKU 定位（B1-04-02）：`source=direct` 优先按 `sku_code` 查库（`ISku.GetByCode`），兼容 `sku_id`；两者同时提交必须一致（否则 9003）。
  编码不存在 → 5001；客户端 `price` 仅用于防伪比对，与服务端售价不一致 → 9007（`source=cart` 提交 `price` 一律 9007），
  该字段永不参与金额计算。订单项快照 `sku_code`/`spec_text`/`original_price`。
- 运费：`products.freight_template_id` → `freight_templates.id`（软引用，可空）；已绑定模板的商品由 `IFreight.Quote` 按区域规则计算运费，未绑定模板（NULL）保持零运费兼容，见 `freight.md`。
- 优惠券：`orders.user_coupon_id` → `user_coupons.id`（软引用，快照）；预占/核销/释放经 `ICoupon` 与订单状态变更同事务，见 `coupon.md`。
- 地址：快照自 `addresses`，下单后地址变更不影响订单。
- 状态流水：`order_status_logs.order_id` → `orders.id`（软引用，无 FK，只追加）；写入时机与事务边界在 B1-04 与状态推进一起实现。
- 一级分销：`POST /orders/:id/pay` 的事务内、订单置 `paid(20)` 后调用 `IDistribution.AccrueOrderCommissionInTx`，
  以 `orders.total_amount` 为基数、配置 `distribution.commission_rate_bp` 为比例写入佣金；计佣失败整体回滚
  （订单保持 10，可重试）。订单状态机与错误码不因分销改变，详见 `distribution.md`。
- 会员：下单经 `IMember.DiscountRateBp` 读取当前等级折扣率，会员折扣金额作为 `pricing.Quote` 的
  `MemberDiscount` 槽位参与统一计价并写 `member_discount_amount` 快照；支付成功经
  `IMember.AccrueForOrderInTx` 发放成长值（同事务、幂等键 `growth:order_reward:<order_id>`）；
  退款成功经 `IMember.DeductForRefundInTx` 扣减成长值，见 `member.md`。
- 钱包/积分：`wallet_transactions`/`user_points_logs` 通过 `biz_type` + `biz_id` 软关联订单（无 FK）。
  余额支付渠道在 `10→20` 同一事务内调用 `IWallet.DebitForOrderInTx`（幂等键 `order_pay:<order_id>`，
  `pay_type='balance'`）；积分抵扣由 `pricing.Quote` 的 `points_discount` 槽位承载，
  当前无积分账本实现（无对应任务分支），恒为 0。
- 建表经 golang-migrate 迁移（`20261001000007_orders.up.sql`）；金额三列（`original_amount`/`discount_amount`/`user_coupon_id`）由 `20261001000022_order_coupon_amounts.up.sql` 增列并回填；会员折扣金额列 `member_discount_amount` 由 `20261111000003_order_member_discount` 增列（默认 0、无回填）；运费/支付/履约字段与 `order_items.spec_text` 由 `20261110000004_order_shipping_payment.up.sql` 增列（无回填）；状态流水由 `20261110000005_order_status_logs.up.sql` 建表，见 `migration.md`。
- 配置：`order.pay_timeout`（支付超时秒，默认 900）、`order.cancel_scan_interval`（扫描周期秒，默认 60）。

## 9. Deferred / 已知留白

- 真实支付/退款到账/对账/资金回调（支付仅 Mock）。
- 履约字段（`delivery_type`/`delivery_name`/`delivery_sn`）只有结构与默认值：发货接口仍只推进状态，承运商/运单号写入、物流轨迹查询与发货单回传未实现。
- 积分抵扣/返还未实现：`user_points_accounts`/`user_points_logs` 只有 Schema，无账本实现（B1-04 无对应分支），
  `pricing.Quote` 的 `points_discount` 槽位当前恒为 0（见 `pricing.md`）。
- 会员价、等级专享商品未建模：当前只有「等级折扣率 → 订单金额折扣」。
- 历史订单金额不重算：`member_discount_amount`/`shipping_fee` 对迁移前订单保持 0，`total_amount` 不被回填改写。
- `shipped/received/completed` 后的退款/退货/售后（留 After-sales/Refund 模块）。
- 订单导出、收货人姓名/手机号模糊搜索等高级筛选（后台列表已支持 `order_no`/`user_id`/`status`/`start_time`/`end_time` 组合筛选与 7 态状态统计）。
- 库存流水 `reason` 字段固定为空，订单驱动的扣减/恢复不落 `reason`，审计粒度有限。
- 后台扫描 goroutine 的优雅停机（依赖条件更新幂等保证半次扫描安全）。

## 10. 后台只读查询性能与索引（EXPLAIN 证据）

本节记录后台订单只读接口（分页列表、组合筛选、状态统计）的索引支撑与真实执行计划证据，作为「V1 不新增 `created_at` 索引」这一已批准决策的可复核依据。证据环境：MySQL 8.0（docker `mysql:8.0`，会话时区 `SYSTEM=UTC`），采集于 2026-10-10；数据规模为 7 行样本（执行计划由查询结构与索引决定，与数据量基本无关）。

核心查询（`internal/logic/order/order.go` 的 `AdminList`/`AdminStats` 经 GoFrame 生成的真实 SQL 形状）与 `EXPLAIN` 关键列：

| # | 查询形状 | type | key | Extra | 结论 |
| --- | --- | --- | --- | --- | --- |
| 1 | `SELECT id,order_no,user_id,status,total_amount,created_at FROM orders ORDER BY id DESC LIMIT ? OFFSET ?`（默认列表/排序） | `index` | `PRIMARY` | `Backward index scan` | 主键倒序扫描，无 filesort，最优 |
| 2 | 上形 + `WHERE status=? AND user_id=? AND created_at>=FROM_UNIXTIME(?) AND created_at<FROM_UNIXTIME(?)`（组合筛选） | `ref` | `idx_status_expire` | `Using where; Using filesort` | `status` 走 `idx_status_expire` 最左前缀定位，`user_id`/时间范围由 `Using where` 过滤，`ORDER BY id` 需 filesort |
| 3 | `WHERE order_no=?` | `const` | `uk_order_no` | — | 唯一索引常量查找，最优 |
| 4 | `WHERE user_id=? ORDER BY id DESC` | `ref` | `uk_user_idempotency`（最左前缀 `user_id`） | `Using filesort` | 走 `user_id` 前缀（`uk_user_idempotency` 或 `idx_user_id`，优化器按统计择一），`ORDER BY id` 需 filesort |
| 5 | `WHERE created_at>=FROM_UNIXTIME(?) AND created_at<FROM_UNIXTIME(?) ORDER BY id DESC`（仅时间范围） | `index` | `PRIMARY` | `Using where; Backward index scan` | **无 `created_at` 索引** → 主键倒序全扫 + `Using where` 过滤，见下方风险 |
| 6 | `SELECT status, COUNT(*) AS count FROM orders GROUP BY status`（统计） | `index` | `idx_status_expire` | `Using index` | 覆盖索引扫描，无需回表 |

性能风险与取舍（与 APPROVED Contract 索引决策一致）：

- **`created_at` 时间范围筛选无索引**：查询走主键倒序扫描 + `Using where` 过滤（表 #5）。因 `ORDER BY id DESC` 与 `created_at` 序不同，即便新增 `created_at` 索引仍无法消除 `id` 排序的 filesort，收益仅缩小扫描范围；当前为低 QPS 后台只读场景，故 V1 不新增索引、不新增 migration。
- **优化触发信号**：当「后台时间范围筛选出现可感知延迟」或「`EXPLAIN` 显示时间范围过滤后扫描行数超阈值」时，再评估新增 `created_at` 相关索引；届时需走 Registry 预留 migration version，并说明收益、写入开销与兼容性影响。
- **其余路径已覆盖**：默认 `id DESC`、`order_no`、`user_id`、`status` 精确筛选与状态统计分别由 `PRIMARY`/`uk_order_no`/`uk_user_idempotency`/`idx_user_id`/`idx_status_expire` 支撑，无需新增索引。
