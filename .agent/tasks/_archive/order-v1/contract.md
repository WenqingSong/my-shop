# Technical Contract

## Decision Status
APPROVED

## Problem

交付「普通订单核心闭环」：登录用户从购物车勾选项或直接购买创建订单，服务端重读 `skus.price` 作为成交价并保存商品/SKU/价格/地址快照，生成唯一订单号，以幂等键防重复下单，在单个事务内完成「创建订单 + 订单项 + 扣库存」；订单状态机按合法迁移推进，非法迁移失败，取消/退款恢复库存，超时未支付自动取消，支付 Mock 且幂等；用户只能操作自己的订单，管理员（含超管）才能发货/退款，越权无 DB 副作用。

核心阻塞点：`IInventory.Deduct`/`Increase` 现为「自开 `g.DB().Transaction`」的独立事务，与「写订单 + 写订单项 + 扣库存」同事务原子性的诉求冲突；且代码库无 MQ/无 cron/无异步队列/无后台 job 基础设施，超时自动取消需要新的最小载体。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2，分层 `api/<module>/v1`（`g.Meta` path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问 `g.DB().Model()`，无 dao/model 层。
- VERIFIED：前台 `middleware.Auth` 注入 `Principal{UserID, Sid}`；后台 `AdminAuth` + `RequirePermission(code)`（`IsSuper` 放行、DB 查询失败 fail-closed 500）。
- VERIFIED：`IInventory.Deduct` 自开事务、条件扣减 `UPDATE ... WHERE quantity >= N` + `RowsAffected`（不足 6001）+ 同事务写 `inventory_logs`；`Increase` 同样自开事务 upsert + 写流水。文件：`internal/logic/inventory/inventory.go`。
- VERIFIED：`skus.price` 为 INT UNSIGNED 整数分，`skus.status` 1=enabled/0=disabled；`products.status` 0=draft/1=on_shelf/2=off_shelf；购物车成交价取 `skus.price`。文件：`20261001000003_skus.up.sql`、`internal/logic/cart/cart.go`。
- VERIFIED：`inventories`（`sku_id` 唯一、`quantity` INT UNSIGNED）+ `inventory_logs`（`operator_admin_id` 可空软引用、`reason` 默认空）。
- VERIFIED：`addresses` 含 `recipient_name/phone/province/city/district/detail` 可快照字段，归属 `user_id`。
- VERIFIED：`cart_items`（`user_id+sku_id` 唯一、`quantity`、`selected`、`price_snapshot`），`ICart.List` 返回含 `Selected` 的条目；`sku_id/user_id` 软引用无 FK。
- VERIFIED：错误码域已用 1000-8999，下一空闲域序 = 9（9000-9999）。
- VERIFIED：迁移当前最新 `20261001000006_cart_items`，`serve` 不自动执行 migration（`checkSchemaReady` 只读校验）。
- VERIFIED：RBAC seed 25 个权限，`seedPermissionList` 按 `code` 唯一幂等写入。
- VERIFIED：事实来源单一 MySQL；Redis 仅会话；无 MQ/cron/异步队列/后台 job。
- VERIFIED：后端当前无订单表、无订单模块；越权/不存在统一 404 防枚举是既有约定（address/cart）。

## Recommendation

（已由 Owner 确认，见 `Selected Design` 与 `Owner Decision Record`。）

## Selected Design

### 数据模型（migration 20261001000007_orders）

`orders`：
- `id` BIGINT UNSIGNED PK 自增；`order_no` VARCHAR(32) NOT NULL `uk_order_no` 唯一；`user_id` BIGINT UNSIGNED NOT NULL 软引用（无 FK）；`status` TINYINT NOT NULL DEFAULT 10；`total_amount` INT UNSIGNED NOT NULL（成交总价整数分快照）；`idempotency_key` VARCHAR(64) NOT NULL；`request_hash` VARCHAR(64) NOT NULL（请求指纹）；地址快照 `recipient_name`(32)/`phone`(20)/`province`(32)/`city`(32)/`district`(32)/`detail`(255) NOT NULL；`address_id` BIGINT UNSIGNED NULL 软引用；`expire_at` DATETIME NOT NULL（支付截止 = created_at + `order.pay_timeout`）；`cancel_reason` TINYINT NULL（1=用户/2=超时）；`paid_at/shipped_at/received_at/completed_at/cancelled_at/refunded_at` DATETIME NULL；`created_at/updated_at`。
- 索引：`uk_order_no(order_no)`、`uk_user_idempotency(user_id, idempotency_key)`、`idx_user_id(user_id)`、`idx_status_expire(status, expire_at)`。

`order_items`：
- `id` PK 自增；`order_id` BIGINT UNSIGNED NOT NULL FK→`orders.id ON DELETE CASCADE`；`sku_id`/`product_id` BIGINT UNSIGNED NOT NULL 软引用；`sku_name`(128)/`product_name`(128) NOT NULL 快照；`product_main_image`(512) NOT NULL DEFAULT '' 快照；`price` INT UNSIGNED NOT NULL（成交价 = 下单时 `skus.price`）；`quantity` INT UNSIGNED NOT NULL；`created_at`。
- 索引：`idx_order_id(order_id)`。

### 状态机与合法迁移

| from | to | 触发者 | 备注 |
| --- | --- | --- | --- |
| 待支付(10) | 已支付(20) | 用户 mock 支付（本人） | 幂等 |
| 待支付(10) | 已取消(60) | 用户取消 / 超时自动取消 | 恢复库存 |
| 已支付(20) | 已发货(30) | 管理员发货（`order:ship`） | |
| 已支付(20) | 已退款(70) | 管理员退款（`order:refund`） | 恢复库存，仅「已支付、未发货」可退 |
| 已发货(30) | 已收货(40) | 用户确认收货（本人） | |
| 已收货(40) | 已完成(50) | 系统（确认收货同请求内原子推进） | 终态 |

终态：已完成(50)、已取消(60)、已退款(70)。非法迁移一律 9002 且状态不变。
退款边界：仅 `paid → refunded`；`shipped/received/completed` 后的退款、退货、售后不在 Order V1，留给未来 After-sales/Refund 模块。`cancelled`（未支付取消）与 `refunded`（已支付退款）语义不同。

### 下单流程（单一事务）

1. 校验输入与归属：直接购买校验 `sku_id`/`quantity`/`address_id`；购物车购买读取 `ICart.List` 的勾选项（`selected=true`），无勾选项 → 9004。
2. 服务端定价：对每条行项目重读 `ISku.GetByID` 取 `skus.price` 与状态，校验 SKU enabled 且商品 on_shelf（否则 9003）；成交价不信任客户端/购物车快照。
3. 组装订单：生成 `order_no`（时间戳 + 随机段，`uk_order_no` 兜底撞号重试）；计算 `request_hash`（见下）；`total_amount = Σ(price × quantity)`；地址快照自 `addresses`；`expire_at = now + order.pay_timeout`。
4. 事务内：INSERT `orders`（含 `idempotency_key`/`request_hash`）→ 逐条 INSERT `order_items` → 对每条行项目 `IInventory.DeductInTx` 扣库存 → （购物车模式）删除本次已购的 `cart_items`。任一失败整体回滚。
5. 幂等：INSERT `orders` 命中 `uk_user_idempotency`（1062）时回滚，读回既有订单比较 `request_hash`：一致 → 返回既有订单（幂等成功）；不一致 → 9006。

### 幂等键与请求指纹

- 客户端提交单一 `idempotency_key`（必填），DB 唯一 `(user_id, idempotency_key)`；不另设 `request_id` 概念。
- 请求指纹 `request_hash = sha256hex(canonical(source, address_id, line_items))`，`line_items` 为按 `sku_id` 升序的 `sku_id:quantity` 列表（购物车模式取本次实际购买的勾选项，直接购买取 `sku_id`+`quantity`）。存于 `orders.request_hash`。
- 同 key + 同 hash → 返回原订单；同 key + 不同 hash → 9006（409）。

### 取消 / 退款 / 库存补偿（原子闸门）

- 取消（用户或超时）：`UPDATE orders SET status=60, cancel_reason=?, cancelled_at=now WHERE id=? AND status=10` + 核对 `RowsAffected`；仅命中者继续 `IInventory.IncreaseInTx` 恢复库存。条件更新保证并发/重复取消只补偿一次。
- 退款：`UPDATE orders SET status=70, refunded_at=now WHERE id=? AND status=20` + `RowsAffected`；仅命中者 `IncreaseInTx` 恢复库存，只补偿一次。
- 发货：`UPDATE ... SET status=30 WHERE id=? AND status=20`；收货：`UPDATE ... status=40 WHERE status=30` 后同请求原子推进 `40→50`。

### 超时自动取消

- serve 启动最小后台 scanner（goroutine + ticker，周期可配 `order.cancel_scan_interval`，默认 60s），扫描 `status=10 AND expire_at < now` 的订单，逐单执行原子取消（条件更新 + 恢复库存）。
- 查询/支付等入口发现订单已超时（`status=10 AND expire_at < now`）时，懒取消兜底：先尝试原子取消，再按结果响应。
- 多实例并发扫描依赖「条件状态 UPDATE + RowsAffected」原子闸门：仅完成状态迁移的一方执行库存补偿，保证只恢复一次；不引入 MQ。

### Inventory 事务协同

- `IInventory` 新增 `DeductInTx(ctx, tx gdb.TX, skuID, qty int64, operatorID *int64)` 与 `IncreaseInTx(ctx, tx gdb.TX, skuID, qty int64, operatorID *int64)`，复用既有「条件更新 + RowsAffected + 写流水」逻辑，不在 tx 内自开事务。
- 既有 `Deduct`/`Increase` 保持公开行为不变，改为「自开事务 → 调用 tx 版」的包装。
- Order 负责外层事务协调与调用，**不复制** Inventory 的库存扣减/增加业务规则（条件更新、流水、不足 6001 等全部复用 `IInventory`）。

### 支付 Mock

- 用户侧 `POST /orders/:id/pay`（Auth，本人）：仅 `pending_payment→paid` 合法，重复支付不重复改状态（幂等成功返回当前订单）；已过期订单先懒取消。

### 权限与错误语义

- 权限 code：`order:ship`、`order:refund`，登记进 `internal/boot/seed.go` 的 `seedPermissionList`。
- 用户侧越权与不存在统一 9001（404，防枚举）；管理员无权限 403（`RequirePermission`）；均无 DB 副作用。

### 路由

前台（`routes_frontend.go` Auth 分组）：
- `POST /orders`（创建：`source` = cart/direct）、`GET /orders`、`GET /orders/:id`
- `POST /orders/:id/pay`、`POST /orders/:id/cancel`、`POST /orders/:id/receive`

后台（`routes_admin.go` require 分组）：
- `POST /admin/orders/:id/ship`（`order:ship`）、`POST /admin/orders/:id/refund`（`order:refund`）

### 错误码域（9000-9999，域序 9）

| code | 语义 | HTTP |
| --- | --- | --- |
| 9001 | ORDER_NOT_FOUND（订单不存在或不属于当前用户，防枚举统一 404） | 404 |
| 9002 | ORDER_INVALID_STATUS_TRANSITION（非法状态迁移） | 409 |
| 9003 | ORDER_SKU_UNAVAILABLE（下单时 SKU 禁用或商品下架） | 409 |
| 9004 | ORDER_CART_EMPTY（从购物车下单无勾选项） | 400 |
| 9005 | ORDER_INVALID_QUANTITY（直接购买数量非法） | 400 |
| 9006 | ORDER_IDEMPOTENCY_CONFLICT（同 key 不同请求内容） | 409 |

复用：1001（400）、1002（401）、1003（403）、5001（SKU 不存在）、6001（库存不足）、7001（地址不存在）。

### 配置契约

- `order.pay_timeout`：支付超时时长（秒，默认 900），不硬编码。
- `order.cancel_scan_interval`：超时取消扫描周期（秒，默认 60）。

### 全局资源清单（Registry 派生，已核实无冲突）

- 错误码域：`9000-9999`（域序 9 = max(8)+1），拥有方 `order`，状态 RESERVED。
- migration version：`20261001000007`（= max(20261001000006)+1），`orders` + `order_items`，状态 RESERVED。
- 权限 code（namespace B，无需 Registry）：`order:ship`、`order:refund`。

## Business Invariants

- INV-001（归属与隔离）：`orders.user_id` 取自 `Principal.UserID`；用户只能查看/取消/支付/收货自己的订单；跨用户操作返回 9001（404）且无写入。
- INV-002（幂等去重）：同一 `(user_id, idempotency_key)` 至多产生一个成功订单（`uk_user_idempotency` 兜底）；同 key 同 `request_hash` 返回既有订单、不重复扣库存；同 key 不同 `request_hash` 返回 9006。
- INV-003（事务原子性）：创建订单 = 单事务「写 orders + 写 order_items + 扣库存 +（购物车模式）删已购条目」，任一步失败（含库存不足 6001）整体回滚，无半成品、库存与购物车均不变。
- INV-004（服务端定价与快照）：成交价 = 下单时 `skus.price`，不信任客户端提交价；商品/SKU/价格/地址快照自足，下单后改价/改地址不影响快照。
- INV-005（状态机）：订单只能沿合法迁移推进；非法迁移返回 9002 且状态不变；`refunded` 仅从 `paid` 可达。
- INV-006（防超卖）：并发对同一 SKU 下单，成功扣减总量 ≤ 可售库存且库存 ≥ 0，由条件扣减 `UPDATE ... WHERE quantity >= N` + `RowsAffected` 保证。
- INV-007（取消/退款只补偿一次）：同一订单取消/退款仅恢复一次库存，由「条件状态更新 + RowsAffected」作原子闸门保证（多实例扫描亦成立）。
- INV-008（权限）：发货/退款仅管理员（含超管，持 `order:ship`/`order:refund`）可执行；无权限 403、越权 404，均无 DB 副作用。
- INV-009（唯一订单号）：`order_no` 全局唯一，由 `uk_order_no` 兜底，撞号重试。
- INV-010（支付幂等）：重复支付回调不重复改状态，仅待支付→已支付合法。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL。`orders`/`order_items`（订单事实）、`inventories`（库存事实）、`inventory_logs`（库存流水，只追加）、`skus`（下单时价格/状态事实来源，读后快照）。Redis 仅会话。
- 成功语义：创建成功 = orders + order_items + 库存扣减（+ 购物车条目删除）同事务提交，返回唯一订单号；取消/退款成功 = 状态变更 + 库存恢复同事务提交；支付/发货/收货成功 = 状态推进。
- 幂等与重复：幂等键重复（同 hash）→ 返回既有订单；支付/取消/退款重复 → 由状态机 + 条件更新保证不重复改状态/不重复补偿。
- 失败语义：库存不足 → 整体回滚 6001；非法迁移 → 9002 状态不变；幂等冲突 → 9006；越权 → 403/404 无写入；DB 技术错误 → 1000（500）。
- 超时取消：后台 scanner + 懒取消，均以条件状态更新为原子闸门，单单失败不影响其他订单与后续轮次；取消与库存恢复同事务。
- 部分完成：所有多步写均在单事务内，无跨系统（无 MQ/Redis 写），不存在部分完成/中间态残留。

## Allowed / Forbidden Changes

- 允许：新增 `orders`/`order_items` 表与 migration；新增 `IOrder` 接口与 `api/order/v1`、controller/logic/service 实现；扩展 `IInventory` 增加 `DeductInTx`/`IncreaseInTx`（既有 `Deduct`/`Increase` 行为不变）；新增订单错误码（9001-9006）与权限 code（`order:ship`/`order:refund`）；新增前台/后台订单路由与后台超时扫描 goroutine。
- 禁止：修改既有购物车/库存/地址/SKU/商品/分类/身份模块的公开行为（除库存 tx 协同与必要的只读引用、购物车下单删已购条目）；修改任何已落地错误码（1000-8999）的取值/语义/HTTP 映射；自行 `max+1` 推断错误码域或 migration version；在 Order 内复制 Inventory 的扣减/增加业务规则；引入真实支付、MQ、外部 cron 依赖；实现 `shipped/received/completed` 后的退款/退货/售后。

## Verification Requirements

- INV-001 → 用户 B 查看/取消/支付用户 A 的订单，断言 404 且 `orders`/`order_items`/`inventories` 无变化。
- INV-002 → 同 key 同内容重复下单返回同一订单、库存仅扣一次；同 key 不同内容（改 sku/qty/address）返回 9006。
- INV-003 → 构造库存不足，断言无半成品 orders/order_items、库存与购物车均不变。
- INV-004 → 下单后改 `skus.price`/改地址，断言快照价格/地址不变；成交价 = 下单时 `skus.price`。
- INV-005/9002 → 遍历合法/非法迁移（含 `refunded` 仅从 `paid`），断言合法成功、非法 9002 且状态不变。
- INV-006 → 并发对同一 SKU 下单（`-race`），断言成功扣减总量 ≤ 可售库存、库存 ≥ 0。
- INV-007 → 并发/重复取消与退款（`-race`），断言库存仅恢复一次。
- INV-008 → 无权限管理员/普通用户 token 发货/退款，断言 403 且无写入；超管/有权限管理员成功；`shipped` 后退款被拒。
- INV-009 → 并发/连续下单，断言 `order_no` 全局唯一。
- INV-010 → 对已支付订单重复发起支付，断言状态不重复改变。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Open Risks

- 后台扫描 goroutine 的生命周期与停机语义（V1 依赖「条件更新幂等」使半次扫描安全，不实现优雅停机通知）。
- 库存流水 `reason` 字段当前固定为空，订单驱动的扣减/恢复暂不落 `reason`（`operator_admin_id` 为 NULL），审计粒度有限。
- 管理员无订单列表，发货/退款按订单 id 定位（列表/筛选 Out of Scope）。
- 支付 Mock 无真实回调/对账（Out of Scope），幂等仅靠状态机约束。

## Owner Decision Record

- D-001（退款语义）：确认新增 `refunded(70)` 状态；Order V1 仅 `paid → refunded`（已支付、未发货），成功退款恢复库存一次；`shipped/received/completed` 后退款/退货/售后留未来 After-sales/Refund 模块；`cancelled` 与 `refunded` 语义不同。→ 已写入状态机/INV-005/INV-007。
- D-002（支付超时）：默认 15 分钟，经 `order.pay_timeout` 配置，不硬编码。
- D-003（自动取消）：后台定时扫描为主 + 请求时懒取消兜底；多实例并发靠条件状态 UPDATE + RowsAffected 原子闸门，仅迁移成功者补偿库存；不引入 MQ。
- D-004（幂等）：客户端单一 `idempotency_key`，`(user_id, idempotency_key)` 唯一；同 key 同请求返回原订单、不同请求 9006；以 `request_hash` 指纹判内容一致；不保留 `request_id` 与 `idempotency_key` 两套概念。
- D-005（购物车清理）：下单成功同事务删除本次实际购买的 `cart_items`，不清空整个购物车；事务失败购物车不变；直接购买不操作购物车。
- D-006（Inventory 协同）：新增 tx 感知 `DeductInTx`/`IncreaseInTx`，既有 `Deduct`/`Increase` 行为不变；Order 负责外层事务协调，不复制 Inventory 扣减/增加业务规则。
- Global Resource Reservation 核实：`origin/develop` 上 `00007` 与 `9000-9999` 均空闲，无 IAM V4 冲突，Order 预留有效（证据见本任务交接）。
