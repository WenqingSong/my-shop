# Technical Contract

## Decision Status
APPROVED

## Problem

为 my-shop 补齐「已支付未发货普通订单」的完整仅退款闭环：用户申请 → 管理员审核（同意/拒绝）→ 执行退款并确认结果 → 退款成功后推进订单与库存一致；退款失败后提供人工关闭退款、恢复订单履约的出口。当前系统把「退款」当成一次管理员直接操作（`paid→refunded` 单事务 + 恢复库存），无申请/审核/执行流程、无退款记录、无审计，正是本任务要纠正的对象（「审核通过不等于退款成功」）。

## Verified Current Behavior

- VERIFIED：当前分支 `feat/order-refund-v1`，基线 HEAD `3ad3229`，working tree 仅 task 产物（无生产代码改动）。
- VERIFIED：订单状态机常量 10~70（`internal/logic/order/order.go`）；`paid(20)→refunded(70)` 触发者是 `sOrder.Refund`（`POST /admin/orders/:id/refund`，`order:refund` 权限，`routes_admin.go:126`），单事务「条件更新 `status=20→70` + RowsAffected + `IInventory.IncreaseInTx`」。
- VERIFIED：`sOrder.Ship`（`order.go:534`）当前是**非事务**直接 `UPDATE orders SET status=30 WHERE status=20`，无 `FOR UPDATE`、无退款申请检查——并发互斥需改造的入口。
- VERIFIED：`IInventory.IncreaseInTx`（`inventory.go:88`）tx 感知、原子 upsert + 流水，可复用。
- VERIFIED：支付 Mock，无 `paid_amount`；实付金额 = `orders.total_amount`（整数分快照）。
- VERIFIED：销量口径 `validOrderStatuses={20,30,40,50}`（`dashboard.go:36`）已排除 `refunded(70)`，退款后自动扣除。
- VERIFIED：秒杀订单独立、无退款语义，正确排除。
- VERIFIED：RBAC seed 于 `internal/boot/seed.go`；管理员身份 `AdminPrincipal{AdminID,Sid,IsSuper}`；审计操作者 username 按需快照（`findAdminUsername`）。
- VERIFIED：Registry：错误码域已至域序 17（`17000-17999`）、migration 已至 `20261001000019`。

## Recommendation

RECOMMENDATION：废弃「管理员直接退款」路径，建立独立退款模块（`refund_requests` + `refund_audits`），退款成功成为 `paid→refunded(70)` 唯一触发者；状态机为七态（六态基础 + `已关闭` 终态）；库存只在退款成功时恢复（与订单推进同事务）；「申请中/待退款/退款失败」期间阻止发货；申请与发货、退款成功与发货的并发用订单行锁 + 状态检查保证；退款执行失败后管理员可人工关闭退款（`已关闭` 终态）恢复履约。

## Selected Design

### 退款状态机（DB TINYINT，七态）

- 10 申请中、20 待退款、30 退款失败、40 审核拒绝、50 已撤销、60 退款成功、70 已关闭。
- 合法迁移：
  - 10→20（审核同意）、10→40（审核拒绝）、10→50（用户撤销）
  - 20→60（执行成功）、20→30（执行失败）
  - 30→60（失败后重试成功）、30→70（人工关闭，恢复履约）
- 终态：40、50、60、70。
- 有效（阻塞发货、阻止重复申请）状态 = {10, 20, 30}；非有效 = {40, 50, 60, 70}。
- 订单状态映射：10/20/30/40/50/70 期间订单保持 `paid(20)`；仅 60 退款成功推进订单 `paid→refunded(70)`；退款失败/已关闭订单仍 `paid`。

### 数据模型（新 migration `20261001000020`）

- `refund_requests`：`id` PK；`order_id`/`user_id`（软引用，无 FK）；`status`；`amount`（= `orders.total_amount` 快照，服务端派生，不接受客户端金额）；`reason`（申请原因）；`review_reason`/`reviewed_by`/`reviewed_at`；`execute_reason`/`executed_by`/`executed_at`；`created_at`/`updated_at`。
  - 「至多一条有效申请」约束：生成列 `active_key = (status IN (10,20,30) ? order_id : NULL)` + `UNIQUE(active_key)`（MySQL 唯一索引允许多个 NULL）。
  - 索引：`idx_order_id`、`idx_user_id`、`uk_active(active_key)`。
- `refund_audits`（append-only）：`id` PK；`refund_id`；`action`（apply/review_approve/review_reject/execute_success/execute_failure/close/revoke）；`operator_type`（1=user、2=admin）；`operator_id`/`operator_username` 快照；`before_status`/`after_status`；`reason`；`created_at`。无 UPDATE/DELETE 接口。

### 接口

用户端（Auth，仅本人，越权统一 404）：
- `POST /orders/:id/refund`：发起申请（仅本人、`paid` 未发货）。
- `POST /refunds/:id/revoke`：撤销（仅申请中 10）。
- `GET /refunds/:id`：详情（本人）。
- `GET /refunds`：本人记录列表（可选 `order_id` 过滤）。

管理端（AdminAuth + RequirePermission，超管放行）：
- `GET /admin/refunds`（`refund:list`）、`GET /admin/refunds/:id`（`refund:view`）、`GET /admin/refunds/:id/audits`（`refund:view`）。
- `POST /admin/refunds/:id/review`（`refund:review`）：`{result: approve|reject, reason}`。
- `POST /admin/refunds/:id/execute`（`refund:execute`）：`{result: success|failure, reason}`。
- `POST /admin/refunds/:id/close`（`refund:execute`）：`{reason}`，仅退款失败 30→70。

### 权限

- 新增 `refund:list`/`refund:view`/`refund:review`/`refund:execute`（seed 登记；`execute` 覆盖执行与关闭）。
- 废弃并移除 `order:refund` 权限与 `POST /admin/orders/:id/refund` 路由；`sOrder.Refund` 改造为 tx 感知的 `IOrder.CompleteRefundInTx(ctx, tx, orderID) error`（订单 `20→70` + `refunded_at` + 恢复库存，`WHERE status=20` 闸门，0 行返回明确错误），供退款模块在事务内复用。

### 并发与事务锁（核心正确性边界）

- **申请 vs 发货**：`Apply` 事务内 `SELECT ... FROM orders WHERE id=? AND user_id=? FOR UPDATE` 锁定订单行 → 校验 `status=20` → 写 `refund_requests`；`Ship` 改为事务内 `SELECT ... FROM orders WHERE id=? FOR UPDATE` 锁定同一订单行 → 校验 `status=20` → 检查 `refund_requests` 是否存在有效申请（status ∈ {10,20,30}，经 `service.Refund().HasActiveRequestInTx`）→ 有则拒绝 18005、无则 `UPDATE status=30`。二者对同一订单行互斥，杜绝「已发货 + 申请中退款」。
- **退款成功 vs 发货**：退款成功事务**先**执行 `UPDATE orders SET status=70 WHERE id=? AND status=20`（行锁 + RowsAffected 闸门），命中才继续恢复库存 + 推进 `refund_requests`→60；0 行则整体回滚（不推进退款、不恢复库存）。与 `Ship` 的 `UPDATE orders ... status=20` 行锁互斥，仅一方命中，杜绝「已发货 + 退款成功」及「refund=60 但 orders=30」中间态。
- **执行幂等**：重复执行成功 → `refund_requests` 条件更新 0 行（已 60）→ 幂等返回；`orders` 闸门 0 行（已 70）→ 不再恢复库存。

## Business Invariants

- INV-001（归属隔离）：退款申请/详情/撤销仅限订单所有者；非所有者返回 404（18001 语义）且无写入。
- INV-002（支付与状态前置）：仅 `status=paid` 且未发货订单可申请；其余被稳定拒绝（18003，409）且无记录。
- INV-003（金额校验）：退款金额 = `orders.total_amount`（服务端派生），不信任/不接受客户端金额；V1 仅整单全额。
- INV-004（单一有效申请）：同一订单至多一条有效（status ∈ {10,20,30}）申请；`uk_active` 唯一兜底。
- INV-005（退款状态机）：仅合法迁移可推进；非法迁移 18002 且状态不变。
- INV-006（审核不直接退款）：审核同意→待退款、拒绝→审核拒绝；订单保持 `paid`。
- INV-007（退款与发货互斥）：最终不「既已发货又退款成功」；有效申请（{10,20,30}）存在即拒绝发货；申请与发货、退款成功与发货分别经订单行锁串行化（见并发设计）。
- INV-008（退款执行幂等/只补偿一次）：重复确认成功不重复推进、不重复恢复库存、不超额；退款成功单事务「订单 `20→70` 闸门 + 库存恢复一次 + 退款状态 60」。
- INV-009（退款成功一致性）：退款成功 = `refund_requests.status=60` ∧ `orders.status=70` ∧ 库存恢复一次，同事务；销量统计因排除 70 自动扣除。
- INV-010（退款失败安全 + 关闭出口）：退款失败 = `status=30`、订单保持 `paid`、库存不恢复、无中间态；可重试（30→60）或人工关闭（30→70）。**关闭仅允许从明确「退款失败」(30) 迁入（条件更新 `WHERE status=30`）；V1 执行退款为同步人工确认，无「执行中/超时/结果未知」中间态，杜绝「未确认失败即关闭」导致的重复退款或退款后继续发货。**
- INV-011（撤销）：审核通过前可撤销（10→50）；不恢复库存、不改订单状态；可再次申请。
- INV-012（权限）：管理端退款接口需 `refund:*` 权限（超管放行）；普通用户 token 返回 403 且零写入。
- INV-013（审计只追加）：审核/执行/关闭等关键操作写 `refund_audits`，无更新/删除接口。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL。`orders`（订单状态）、`inventories/inventory_logs`（库存）、`refund_requests`（退款状态）、`refund_audits`（审计）同库；无 MQ、Redis 仅会话，无跨系统事务。
- 成功语义：申请 = 写 `refund_requests`（`uk_active` 兜底）+ 审计，不碰订单/库存；审核 = 仅推进退款状态 + 审计；执行成功 = 单事务「订单 `20→70` + 库存恢复 + 退款状态 60 + 审计」；执行失败 = 退款状态 30 + 审计，订单/库存不变；关闭 = 退款状态 70 + 审计，订单/库存不变。
- 失败/重复/乱序：重复执行成功 → 幂等返回、不重复恢复库存；订单闸门 0 行（已发货/非 20）→ 执行失败整体回滚，不出现「已发货 + 退款成功」。
- 部分完成：所有写为单事务，无中间态残留。
- 退款失败后：订单 `paid`、库存不恢复；管理员可重试执行（30→60）或关闭退款（30→70）恢复履约；不自动重试、不自动退款。
- 关闭前置：仅 `status=30` 可关闭；执行是同步人工确认，不存在「超时/结果未知/仍在执行」被当失败关闭的路径。

## Allowed / Forbidden Changes

- 允许：新增 `refund_requests`/`refund_audits` 迁移（`20261001000020`）；新增退款错误码域 `18000-18999` 与 `refund:*` 权限；新增 `api/refund/v1`、controller/service/logic 与前端/后台退款路由；废弃 `POST /admin/orders/:id/refund` 与 `order:refund` 权限；订单模块新增 `IOrder.CompleteRefundInTx`、`Ship` 改为事务内 `FOR UPDATE` + 有效退款检查。
- 禁止：不实现前端；不做部分退款/多次累计退款/换货/退货物流；不做自动审核/自动退款/超时自动处理/自动重试；不接真实支付/退款回调/对账；不改秒杀订单；不修改 `orders` 既存状态码含义（10~70 保持）；不自行推断 migration version 或错误码域号；不修改 `.agent/workflow.yaml`；不将「执行中/超时/结果未知」当失败关闭。

## Verification Requirements

- INV-004 → MySQL：同订单并发发起两次退款，断言至多一条有效（`uk_active` 兜底）。
- INV-007 → MySQL：并发「发起退款 + 发货」断言不「已发货 + 申请中退款」且不「已发货 + 退款成功」；有有效退款时发货被拒；并发「执行退款成功 + 发货」断言不出现 `refund=60 且 orders=30` 中间态。
- INV-008 → MySQL：重复执行成功断言退款记录与库存只变化一次。
- INV-009 → MySQL：退款成功后断言 `orders.status=70`、库存恢复一次、`refund_requests.status=60` 与库存流水一致；`GET /admin/dashboard/products/top` 销量不再含该订单。
- INV-010 → MySQL：退款失败后断言状态=30、订单=20、库存不变；关闭后断言状态=70、订单=20、可发货；重试成功断言 30→60 且库存恢复一次；非 30 状态关闭被拒（18002）。
- 其余 INV 依 `task.md` Verification（AC-001~AC-014）映射：均需真实 MySQL（docker compose），并发需真实条件更新/唯一约束/FOR UPDATE，普通用户 token 访问管理端断言 403 且零写入。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；并发关键路径按需 `go test -race`。

## Global Resources

- 错误码域：退款域 = `18000-18999`（域序 18 = max(17)+1）。
  - 18001 REFUND_NOT_FOUND(404)、18002 REFUND_INVALID_STATUS_TRANSITION(409)、18003 REFUND_ORDER_NOT_REFUNDABLE(409)、18004 REFUND_ALREADY_EXISTS(409)、18005 REFUND_SHIP_CONFLICT(409)。
- migration：`20261001000020`（= max(20261001000019)+1，建 `refund_requests` + `refund_audits`）。

## Owner Decision Record

- D1（已确认）：废弃旧直接退款接口 `POST /admin/orders/:id/refund` 与 `order:refund` 权限。
- D2（已确认）：接受六态基础状态机（扩展为七态，含「已撤销」与「已关闭」）。
- D3（已确认）：已关闭(70) 仅从退款失败(30) 迁入；关闭后不恢复库存、不改订单状态、解除发货阻塞；订单仍 `paid(20)` 且未发货时允许重新申请；申请退款与发货必须通过事务与订单行锁保证互斥。
- D4（已确认）：拒绝/撤销后允许重新申请；同一订单同时至多一条有效申请。
- D5（已确认）：退款错误码域 `18000-18999`、migration `20261001000020`。
- 补充安全约束（已确认）：只有明确确认退款执行失败（`status=30`）才允许关闭；超时/结果未知/执行中不得按失败处理，避免重复退款或退款后继续发货。
