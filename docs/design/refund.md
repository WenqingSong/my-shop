# 退款设计（Refund）

本文面向项目接手者，说明「已支付未发货普通订单仅退款」的架构、数据模型、状态机、并发/一致性与权限边界。事实来源为 `order-refund-v1` APPROVED Contract 与最终实现。

## 1. 职责与边界

退款回答「一笔已支付未发货订单，用户能否退款、退款走到哪一步、最终是否成功」，承载退款申请主数据（`refund_requests`）与退款审计（`refund_audits`），实现：发起申请、撤销、审核（同意/拒绝）、执行退款并确认结果（成功/失败）、退款失败后人工关闭恢复履约，以及用户/管理员查询与审计。退款成功推进订单 `paid→refunded` 并恢复库存，退款失败/关闭不改变订单与库存。

边界：仅覆盖「已支付、未发货」普通订单的整单全额退款（退款金额恒等于 `orders.total_amount`，服务端派生）；不覆盖退货物流、部分/多次退款、换货；不做自动审核/自动退款/超时自动处理/自动重试；不接入真实支付网关退款、到账、对账与资金回调（退款执行为人工 Mock 确认）。秒杀订单（`flash_sale_orders`）无支付、无库存恢复语义，不进入退款流程。事实来源为单一 MySQL；无 MQ；Redis 仅会话。

## 2. 数据模型

### 2.1 `refund_requests`（退款申请主数据）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `order_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），指向 `orders.id` |
| `user_id` | BIGINT UNSIGNED | 非空，软引用（无 FK），申请者（订单所有者） |
| `status` | TINYINT | 非空（见状态机 10~70） |
| `amount` | INT UNSIGNED | 非空，= `orders.total_amount` 快照（服务端派生，不接受客户端金额） |
| `reason` | VARCHAR(255) | 申请原因 |
| `review_reason` | VARCHAR(255) | 审核原因（同意/拒绝） |
| `reviewed_by` | BIGINT UNSIGNED | 可空，审核管理员 id（软引用） |
| `reviewed_at` | DATETIME | 可空，审核时间 |
| `execute_reason` | VARCHAR(255) | 执行/关闭原因 |
| `executed_by` | BIGINT UNSIGNED | 可空，执行/关闭管理员 id（软引用） |
| `executed_at` | DATETIME | 可空，执行/关闭时间 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`idx_order_id(order_id)`、`idx_user_id(user_id)`、`uk_active(active_key)`。

**单一有效申请约束**：生成列 `active_key = (status IN (10,20,30) ? order_id : NULL)`，`UNIQUE(active_key)`（MySQL 唯一索引允许多个 NULL），机械保证「同一订单至多一条有效（非拒绝/非撤销/非关闭/非成功）申请」，允许拒绝/撤销/关闭后再次申请。

### 2.2 `refund_audits`（退款审计，append-only）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `refund_id` | BIGINT UNSIGNED | 非空，软引用（无 FK） |
| `action` | VARCHAR(32) | 非空：apply/review_approve/review_reject/execute_success/execute_failure/close/revoke |
| `operator_type` | TINYINT | 非空，`1=user`、`2=admin` |
| `operator_id` | BIGINT UNSIGNED | 非空，操作者 id（软引用） |
| `operator_username` | VARCHAR(64) | 非空，操作者 username 快照 |
| `before_status` | TINYINT | 非空，操作前状态 |
| `after_status` | TINYINT | 非空，操作后状态 |
| `reason` | VARCHAR(255) | 原因 |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`idx_refund_id(refund_id)`。只追加、不可修改删除（无 UPDATE/DELETE 接口）。

## 3. 状态机与合法迁移

| from | to | 触发者 | 备注 |
| --- | --- | --- | --- |
| 申请中(10) | 待退款(20) | 管理员审核同意（`refund:review`） | 订单保持 `paid` |
| 申请中(10) | 审核拒绝(40) | 管理员审核拒绝（`refund:review`） | 订单保持 `paid` |
| 申请中(10) | 已撤销(50) | 用户撤销（本人） | 订单保持 `paid` |
| 待退款(20) | 退款成功(60) | 管理员执行成功（`refund:execute`） | 订单 `paid→refunded` + 恢复库存 |
| 待退款(20) | 退款失败(30) | 管理员执行失败（`refund:execute`） | 订单/库存不变 |
| 退款失败(30) | 退款成功(60) | 管理员重试成功（`refund:execute`） | 订单 `paid→refunded` + 恢复库存 |
| 退款失败(30) | 已关闭(70) | 管理员人工关闭（`refund:execute`） | 订单/库存不变，解除发货阻塞 |

终态：审核拒绝(40)、已撤销(50)、退款成功(60)、已关闭(70)。非法迁移返回 18002 且状态不变。

有效（阻塞发货、阻止重复申请）状态 = {10, 20, 30}；非有效 = {40, 50, 60, 70}。

订单状态映射：退款申请从发起到退款失败/已关闭，订单始终 `paid(20)`；仅退款成功(60) 推进订单 `paid→refunded(70)`。

## 4. 业务不变量

- INV-001（归属隔离）：退款申请/详情/撤销仅限订单所有者；非所有者返回 404（18001 语义）且无写入。
- INV-002（支付与状态前置）：仅 `status=paid` 且未发货订单可申请；其余被稳定拒绝（18003，409）且无记录。
- INV-003（金额校验）：退款金额 = `orders.total_amount`（服务端派生），不信任/不接受客户端金额；V1 仅整单全额。
- INV-004（单一有效申请）：同一订单至多一条有效（status ∈ {10,20,30}）申请；`uk_active` 唯一兜底。
- INV-005（退款状态机）：仅合法迁移可推进；非法迁移 18002 且状态不变。
- INV-006（审核不直接退款）：审核同意→待退款、拒绝→审核拒绝；订单保持 `paid`。
- INV-007（退款与发货互斥）：最终不「既已发货又退款成功」；有效申请（{10,20,30}）存在即拒绝发货；申请与发货、退款成功与发货分别经订单行锁串行化。
- INV-008（退款执行幂等/只补偿一次）：重复确认成功不重复推进、不重复恢复库存、不超额。
- INV-009（退款成功一致性）：退款成功 = `refund_requests.status=60` ∧ `orders.status=70` ∧ 库存恢复一次，同事务。
- INV-010（退款失败安全 + 关闭出口）：退款失败 = `status=30`、订单 `paid`、库存不恢复；可重试（30→60）或人工关闭（30→70）；关闭仅从明确 `status=30` 迁入，不恢复库存、不改订单状态，仅解除发货阻塞。
- INV-011（撤销）：审核通过前可撤销（10→50）；不恢复库存、不改订单状态；可再次申请。
- INV-012（权限）：管理端退款接口需 `refund:*` 权限（超管放行）；普通用户 token 返回 403 且零写入。
- INV-013（审计只追加）：审核/执行/关闭等关键操作写 `refund_audits`，无更新/删除接口。

## 5. 一致性模型与失败语义

- 事实来源：单一 MySQL。`orders`（订单状态）、`inventories`/`inventory_logs`（库存）、`refund_requests`（退款状态）、`refund_audits`（审计）同库；无 MQ、无跨系统事务。
- 成功语义：申请 = 写 `refund_requests`（`uk_active` 兜底）+ 审计，不碰订单/库存；审核 = 仅推进退款状态 + 审计；执行成功 = 单事务「订单 `20→70` + 库存恢复 + 退款状态 60 + 审计」；执行失败 = 退款状态 30 + 审计，订单/库存不变；关闭 = 退款状态 70 + 审计，订单/库存不变。
- 并发互斥：申请与发货用「同一 `orders` 行 `SELECT ... FOR UPDATE`」串行化；退款成功与发货用「订单 `status=20` 条件更新」行锁闸门；杜绝「已发货 + 申请中退款」「已发货 + 退款成功」及「退款=60 但订单=30」中间态。
- 幂等与重复：重复执行成功 → 退款状态条件更新 0 行（已 60）→ 幂等返回；订单闸门 0 行（已 70）→ 不再恢复库存。
- 部分完成：无跨系统，所有写为单事务，无中间态残留。
- 关闭前置：仅 `status=30` 可关闭；执行是同步人工确认，不存在「超时/结果未知/仍在执行」被当失败关闭的路径。

## 6. 安全与权限边界

- 前台受保护路由（Auth 分组，仅本人）：`POST /orders/:id/refund`、`POST /refunds/:id/revoke`、`GET /refunds/:id`、`GET /refunds`。
- 后台写路由（AdminAuth + RequirePermission）：`POST /admin/refunds/:id/review`（`refund:review`）、`POST /admin/refunds/:id/execute`（`refund:execute`）、`POST /admin/refunds/:id/close`（`refund:execute`）。
- 后台只读路由：`GET /admin/refunds`（`refund:list`）、`GET /admin/refunds/:id`（`refund:view`）、`GET /admin/refunds/:id/audits`（`refund:view`）。
- 身份信任：用户侧身份来自 `Principal.UserID`；管理端来自 `AdminPrincipal`，经 `RequirePermission`；普通用户 token 访问后台路由返回 403。
- 越权语义：用户越权与不存在统一 18001（404，防枚举）；均无 DB 副作用。
- 权限 code：`refund:list`、`refund:view`、`refund:review`、`refund:execute`（`execute` 覆盖执行与关闭）。

## 7. 错误码域

| code | 语义 | HTTP |
| --- | --- | --- |
| 18001 | REFUND_NOT_FOUND（退款申请不存在或非本人/不可见） | 404 |
| 18002 | REFUND_INVALID_STATUS_TRANSITION（非法退款状态迁移） | 409 |
| 18003 | REFUND_ORDER_NOT_REFUNDABLE（订单非已支付未发货，不可退款） | 409 |
| 18004 | REFUND_ALREADY_EXISTS（同一订单已存在有效退款申请） | 409 |
| 18005 | REFUND_SHIP_CONFLICT（退款与发货互斥冲突） | 409 |

复用：`1001`（400）、`1002`（401）、`1003`（403）、`9001`（订单不存在，404）、`9002`（订单非法状态迁移，409）、`6001`（库存不足，409）。

## 8. 跨模块关系

- `refund_requests.order_id` → `orders.id`（软引用，无 FK），`refund_requests.user_id` → `users.id`（软引用）。
- 退款成功推进订单状态：复用订单模块 tx 感知方法（`IOrder.CompleteRefundInTx`），订单 `20→70` + `refunded_at` + 恢复库存（`WHERE status=20` 闸门）；退款模块只协调外层事务，不复制订单状态迁移与库存恢复业务规则。
- 库存恢复复用 `IInventory.IncreaseInTx`（「只补偿一次」不变量由订单状态条件更新闸门保证）。
- 发货互斥：订单模块 `Ship` 在事务内检查有效退款申请（`service.Refund().HasActiveRequestInTx`），存在即拒绝。
- 建表经 golang-migrate 迁移（`20261001000020_refund.up.sql`），见 `migration.md`。

## 9. Deferred / 已知留白

- 真实支付平台退款、到账、对账、资金回调（执行退款为人工 Mock 确认）。
- 部分退款、多次累计退款、换货、退货物流（寄回、物流单号、入库验收）。
- 自动审核、自动退款、超时自动处理、自动重试。
- 秒杀订单退款（无支付、下单即成交、库存独立不恢复）。
- 退款申请的用户侧频率/次数限制。
