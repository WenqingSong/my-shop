# Task: Order Refund V1 — 已支付未发货订单仅退款业务闭环

## Goal

为 my-shop 补齐「已支付未发货普通订单」的完整**仅退款**业务闭环：用户可对本人已支付未发货订单发起全额退款申请，管理员审核（同意/拒绝）、执行退款并确认结果，系统保证退款申请与发货互斥、退款执行幂等、退款成功后订单状态与库存/销量统计一致，并以独立退款状态机、退款记录与审计支撑全程可追溯。本阶段仅 Go 后端 API，不实现前端；支付与退款执行为 Mock（无真实支付网关），审核通过不等于退款成功。

## Scope

- 独立退款申请状态机（`申请中/审核拒绝/待退款/退款成功/退款失败`）与退款记录表（`refund_requests`）、退款审计表（`refund_audits`，append-only），经 golang-migrate 迁移（遵循 Registry 预留，不自行推断 version）。
- 用户端接口（仅本人数据）：发起退款申请、撤销申请、查询退款详情与记录。
- 管理端接口（RBAC 保护）：退款列表、退款详情、审核同意/拒绝、退款结果处理（人工退款执行与成功/失败确认）。
- 退款金额校验：以订单实际支付金额为准，V1 仅整单全额退款，不信任客户端提交金额。
- 退款与发货互斥、退款执行幂等（防重复退款/超额退款）、退款成功后订单状态推进与库存恢复（复用 `IInventory.IncreaseInTx`，保持「只补偿一次」不变量）。
- 退款操作与审核审计：记录操作人、时间、前后状态、原因，只追加不可修改。
- 长期设计（Design Impact = NEW）：新增 `docs/design/refund.md`；同步 UPDATE `docs/design/order.md`（`paid→refunded` 迁移触发者与 `order:refund` 语义变化）。
- 全局资源：新增 1 个退款错误码域（语义：退款）；新增 1 个 migration（建退款相关表）。只声明类型与语义，具体域号/version 由 Analyst 读 Registry 派生。
- 必要测试：覆盖成功、拒绝、边界与并发场景（详见 Verification），并保证既有订单/库存/发货/秒杀流程无回归。

## Out of Scope

- 退货物流（寄回、物流单号、入库验收）。
- 部分退款、多次累计退款、换货。
- 复杂自动售后规则（自动同意、自动退款、超时自动处理）。
- 秒杀订单退款：`flash_sale_orders` 无支付、下单即成交、库存独立且不恢复，不进入普通订单退款流程。
- `shipped/received/completed` 后的退款/退货售后（本次仅「已支付未发货」）。
- 真实支付平台退款、资金到账、对账、资金回调（退款执行为人工 Mock 确认）。
- Vue 前端（`frotend_web`/`frotend_manage`）。
- 优惠券/积分/促销等对退款金额的联动。

## Milestone

Milestone: order-refund-v1

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/refund.md（新增，主）；docs/design/order.md（UPDATE，次：`paid→refunded` 触发者与 `order:refund` 语义）

## Acceptance Criteria

- [ ] AC-001（归属隔离）：订单所有者可对本人「已支付未发货」订单发起退款申请；非订单所有者访问同一订单退款申请返回 404（9001 语义），且不产生任何退款记录/写入。
- [ ] AC-002（支付与状态前置）：仅「已支付、未发货」订单可发起退款申请；待支付/已取消/已发货/已收货/已完成订单申请退款被稳定拒绝（非法状态迁移 409），且不产生退款记录。
- [ ] AC-003（金额校验）：退款金额 = 订单实际支付金额（Mock 支付下为 `orders.total_amount` 快照）；V1 仅整单全额退款，系统不信任客户端提交的退款金额，部分/超额退款请求被拒绝。
- [ ] AC-004（单一有效退款）：同一订单至多存在一个「有效（非审核拒绝/非已撤销）退款申请」；重复申请被稳定拒绝，不产生并行有效退款。
- [ ] AC-005（退款状态机）：退款申请有独立状态机（申请中/审核拒绝/待退款/退款成功/退款失败），非法状态迁移被拒绝且状态不变。
- [ ] AC-006（审核不直接退款）：管理员可审核同意/拒绝；同意进入「待退款」而非「退款成功」，拒绝进入「审核拒绝」。审核通过不等于退款成功。
- [ ] AC-007（退款与发货互斥）：有效退款申请与发货互斥；并发发起退款与发货时，最终同一订单不会既「已发货」又「退款成功」。
- [ ] AC-008（退款执行幂等）：退款执行/人工确认成功幂等；重复回调/重复确认不重复退款、不重复恢复库存、不超额退款。
- [ ] AC-009（退款成功一致性）：退款成功后订单状态推进为「已退款」(70)、库存恢复一次、销量统计（基于订单有效状态 {20,30,40,50} 派生）自动扣除该订单销量；退款记录、订单状态、库存三者一致。
- [ ] AC-010（退款失败安全处理）：退款失败进入「退款失败」，订单保持「已支付」、库存不恢复、无部分退款中间态残留；可安全重试或人工介入。
- [ ] AC-011（撤销申请）：用户在审核通过前可撤销本人申请；撤销后不恢复库存、不改变订单状态，且该订单可再次发起申请。
- [ ] AC-012（查询能力）：用户可查询本人退款详情与记录；管理员可查询退款列表、详情与审计记录。
- [ ] AC-013（审计）：退款审核、执行等关键操作记录审计（操作人、时间、前后状态、原因），审计只追加、不可修改删除。
- [ ] AC-014（权限）：管理端退款审核/执行/列表/详情接口需对应管理员权限（含超管放行）；普通用户 token 访问返回 403 且无写入。

## Relevant Context

已核实事实：

- 基线：当前在 `develop` 分支，HEAD `3ad322992663da9129aa95de8c64b4ab8bba6801`，working tree clean，无既有未提交修改。任务将在 Owner 准备的 feature 分支（如 `feat/order-refund-v1`）上执行（TaskBuilder 不创建分支）。
- 技术栈与分层：GoFrame v2；`api/<module>/v1`（`g.Meta` path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问 `g.DB().Model()`，无 `dao`/`model` 层。
- 现状已有「管理员直接退款」Mock：`POST /admin/orders/:id/refund`（`internal/cmd/routes_admin.go`，`order:refund` 权限），实现于 `internal/logic/order/order.go` 的 `Refund`——条件更新 `status: paid→refunded(70)` + `RowsAffected` 原子闸门 + `IInventory.IncreaseInTx` 恢复库存，单事务「状态推进 + 库存恢复」且只补偿一次。该端点把「退款」当作一次管理员操作直接完成，**无申请/审核/退款执行流程、无退款记录表、无审计**，正是本任务 Owner 第 10 点「审核通过不等于退款成功」要纠正的对象。
- 订单状态机（`docs/design/order.md` §3，`internal/logic/order/order.go` 常量 10~70）：`paid(20)→refunded(70)` 合法迁移已存在，触发者目前是管理员直接退款；终态含 `completed(50)/cancelled(60)/refunded(70)`。
- 支付为 Mock：无真实支付网关、无支付流水表、无独立 `paid_amount` 字段；`orders.total_amount` 是下单时成交总价整数分快照（`docs/design/order.md` §2.1/§2.3）。「实际支付金额」在 Mock 下即 `total_amount`。
- 库存：普通库存 `inventories`/`inventory_logs`，取消/退款经 `IInventory.IncreaseInTx` 恢复（`docs/design/inventory.md`），订单侧已保证「取消/退款只补偿一次」（INV-007）。
- 秒杀订单独立：`flash_sale_orders` 无状态机、下单即成交、无支付/取消/退款；秒杀库存（`flash_sale_activity_skus.sold`）独立于普通 `inventories` 且不因后续操作恢复（`docs/design/flash-sale.md` §1/§3/§9）。退款范围仅限普通 `orders`。
- 销量统计派生：`internal/logic/dashboard/dashboard.go` 金额/销量口径用 `validOrderStatuses = {20,30,40,50}`，已排除 `refunded(70)`；销量 = `order_items.quantity` 聚合。products/skus 上无冗余销量计数字段，订单退款后自动从销量统计扣除，无需额外计数器。
- 错误码域：`.agent/registry/error-codes.md` 已登记至域序 17（17000-17999 object-storage-upload RESERVED）；订单域 9000-9999 已 ACTIVE。退款需新增错误码域（本任务声明「新增 1 个退款错误码域」，具体域号由 Analyst 派生）。
- migration：`.agent/registry/migrations.md` 已登记至 `20261001000019`（iam-v5 RESERVED）。退款需新增 migration（声明「新增 1 个 migration」，version 由 Analyst 派生）。
- 权限：RBAC 权限 code 在 `internal/boot/seed.go` 的 `seedPermissionList` 登记；当前含 `order:ship`/`order:refund`/`order:list`/`order:view`。退款需新增权限 code（如 `refund:*`）。

Assumption（合理但未经确认，交 Analyst 固化并向 Owner 确认）：

- 「实际支付金额」= `orders.total_amount`：Mock 支付下无独立支付流水/实付金额字段，全额退款金额即下单成交总价快照。
- 退款范围仅限普通 `orders`，秒杀订单明确排除（无支付状态、无库存恢复语义）。

OPEN QUESTION（影响关键行为，交 Analyst 调查、Owner 确认）：

- 现有 `order:refund` 直接退款端点的去留：新流程下是废弃该端点、改为「退款结果处理」终态步骤，还是并存；`refunded(70)` 触发者由「管理员直接退款」变为「退款成功后系统推进」。
- 库存恢复时机（申请时 / 审核通过时 / 退款成功时）与「只补偿一次」不变量、退款失败不恢复的一致性关系。
- 退款状态机与订单状态映射：申请中/待退款期间订单保持 `paid`；退款失败订单状态。
- 撤销/拒绝后是否允许再次申请及其边界。
- 退款失败后的处理与重试/人工介入策略。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试必须走真实 `RegisterFrontendRoutes`/`RegisterAdminRoutes` + `Auth`/`AdminAuth`/`RequirePermission`，用真实用户/管理员/超级管理员账号断言；并发场景需真实 MySQL 条件更新与唯一约束验证。

- AC-001 → 需 MySQL：用户 A 发起退款成功；用户 B 对同一订单发起退款断言 404 且无退款记录新增。
- AC-002 → 需 MySQL：预置待支付/已支付/已发货/已收货/已完成/已取消订单，仅「已支付未发货」可发起退款，其余被稳定拒绝且无记录。
- AC-003 → 需 MySQL：客户端提交非法/超额/部分退款金额被拒绝；退款金额恒等于订单 `total_amount`。
- AC-004 → 需 MySQL：同一订单并发发起两次退款，断言至多一个有效申请（唯一约束/条件写入兜底）。
- AC-005 → 需 MySQL：对退款记录执行非法状态迁移断言被拒且状态不变（与订单状态机 9002 语义一致）。
- AC-006 → 需 MySQL：审核通过后断言退款状态为「待退款」、订单仍为「已支付」，不直接变为「退款成功」。
- AC-007 → 需 MySQL：并发「发起退款」与「发货」，断言最终不会同时出现已发货与退款成功；有有效退款时发货被拒。
- AC-008 → 需 MySQL：重复调用退款确认/回调断言退款记录与库存只变化一次、无超额退款。
- AC-009 → 需 MySQL：退款成功后断言订单状态=70、库存恢复一次、退款记录与订单状态与库存流水一致；`GET /admin/dashboard/products/top` 销量不再含该订单。
- AC-010 → 需 MySQL：退款失败后断言退款状态=退款失败、订单仍=已支付、库存不变、无中间态残留。
- AC-011 → 需 MySQL：审核通过前撤销断言成功且不恢复库存、不改订单状态；撤销后可再次申请。
- AC-012 → 需 MySQL：用户查询本人退款详情/记录正确、不可见他人；管理员查询退款列表/详情/审计正确。
- AC-013 → 需 MySQL：审核/执行后审计表新增记录且内容正确；审计无更新/删除接口。
- AC-014 → 需 MySQL/Redis：普通用户 token、无权限管理员、超级管理员分别访问管理端退款接口，断言 401/403/放行语义与零写入。
- 回归：现有订单创建/支付/取消/收货、管理员发货、库存、秒杀流程测试无回归。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；并发关键路径按风险运行 `go test -race`。

## Complexity

COMPLEX

原因：(1) 新增独立业务模块与状态机、新表、新错误码域、新权限边界（Design Impact = NEW）；(2) 退款与发货互斥、退款执行幂等/防超额、库存恢复时机涉及事务与并发一致性，存在多个会产生不同业务结果的现实方案；(3) 与现有 `order:refund` 直接退款端点及 `refunded(70)` 语义存在兼容取舍，需 Owner 确认；(4) Mock 支付下「实际支付金额」「人工退款确认」等语义需 Analyst 固化、Owner 决策。

## Analyst Questions

1. 现有 `order:refund` 直接退款端点的去留与 `refunded(70)` 触发者语义（替换 / 改为退款结果处理终态 / 并存），`order:refund` 权限与路由是否保留或重定向。需 Owner 确认。
2. 退款状态机定义：状态枚举、合法迁移、终态；订单状态映射（申请中/待退款期间订单保持 `paid`，`refunded` 仅退款成功推进，退款失败订单状态）。
3. 库存恢复时机与「只补偿一次」不变量：申请/审核通过/退款成功哪个时点恢复库存，退款失败不恢复的一致性如何机械保证。
4. 退款与发货互斥的并发控制机制（条件更新 + `RowsAffected` / 唯一约束 / 状态检查），给出可验证的互斥不变量与异常路径。
5. 退款执行幂等与人工确认机制（无真实支付接口）：幂等键/回调去重、重复确认防护、超额退款防护。
6. 退款金额语义：Mock 支付下「实际支付金额」是否即 `orders.total_amount`；全额退款金额校验规则。
7. 撤销/拒绝后是否允许再次申请及其边界（次数/频率限制）。
8. 退款失败后的处理与重试/人工介入策略（状态复位 vs 人工重新执行 vs 自动重试）。
9. 全局资源：新增 1 个退款错误码域 + 1 个 migration（建 `refund_requests`/`refund_audits`）的预留；退款域内具体错误码清单与语义。
10. 权限 code 设计（`refund:*` 列表/详情/审核/执行）与 `internal/boot/seed.go` seed 登记。
11. 确认秒杀订单排除于退款范围（无支付/下单即成交/库存独立不恢复）。
12. 长期设计：`docs/design/refund.md` 新增内容与 `docs/design/order.md` UPDATE 的具体章节一致性。

## Review Baseline

- Base commit：`3ad322992663da9129aa95de8c64b4ab8bba6801`（当前 `develop` 分支 HEAD）。
- 任务开始时已有修改：无（working tree clean，`git status --porcelain` 为空）。
- 重叠修改的区分方式：本任务新增/修改产物为 `.agent/tasks/order-refund-v1/`、`api/refund/v1/`（或 `api/order/v1` 扩展）、`internal/controller/refund/`、`internal/service/refund.go`、`internal/logic/refund/`、`internal/migrations/sql/`（新 migration）、`internal/boot/seed.go`（新增 `refund:*` 权限）、`internal/codes/codes.go`（新增退款错误码域）、`internal/cmd/routes_frontend.go`/`routes_admin.go`（新退款路由）、`docs/design/refund.md`/`docs/design/order.md`（Analyst 更新）。当前工作区干净，无既有未提交修改；当前在 `develop` 分支，Owner 需准备 feature 分支后再交 Analyst。

## Initial Route

交 Analyst（COMPLEX）
