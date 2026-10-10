# Task: Admin Order Management V1 — 后台订单只读管理

## Goal

为 SurgeCart（my-shop）补齐管理员普通订单的**只读管理**能力：基于现有 GoFrame、MySQL、Order Service、Admin JWT 与 RBAC，交付后台订单分页列表、多条件组合筛选、订单详情（含商品明细）与订单状态统计，并以独立的 `order:list` / `order:view` 只读权限保护。本阶段只开发 Go 后端 API，不实现前端；只补齐缺失功能、复用现有订单领域模型，**不改动交易正确性机制**（状态机、库存补偿、事务、发货/退款）。

## Scope

- 后台只读接口（复用现有 `api/order/v1` 包与 `AdminControllerV1`，路径与既有 `/admin/orders/:id/ship`、`/admin/orders/:id/refund` 同域）：
  - `GET /admin/orders`：MySQL 分页查询全部普通订单，支持 `page`/`size`（有默认值与最大上限），默认按订单 id 倒序，排序字段走白名单且稳定；返回总记录数、当前页、每页数量与列表所需订单基本信息（订单 id、订单号、用户 id、状态、总额、创建时间及必要状态时间）。列表保持轻量，**不含逐单加载的商品明细**。
  - `GET /admin/orders/:id`：管理员按订单 id 查看任意普通订单详情，复用 `loadByIDOrder`/`loadItems` 等现有内部逻辑（不含用户端 `findOwned` 归属限制），返回订单编号/id、用户 id、状态、金额快照、商品明细（商品/SKU 名称、单价、数量等快照）、收货地址快照、创建时间、已有状态时间戳与取消原因等业务字段；不重读当前售价、不暴露内部敏感字段；不存在订单返回规范错误。
  - `GET /admin/orders/stats`：按现有 `orders.status` 做 `GROUP BY status` 统计全部普通订单数量，覆盖全部 7 态，无数据状态返回 0，不将秒杀 queued 请求计入，结果便于前端直接展示。
- 组合筛选：列表接口支持 `order_no`（精确）、`user_id`（精确）、`status`（枚举）、`start_time`/`end_time`（创建时间，左闭右开 `[start,end)`）并可组合；状态严格使用现有订单枚举，非法参数明确拒绝，参数化 SQL，不按订单号做不必要的模糊扫描，不引入任意 SQL 排序字段。
- RBAC：新增只读权限 `order:list`（列表与统计）、`order:view`（详情），在 `internal/boot/seed.go` 的 `seedPermissionList` 登记；路由挂 `AdminAuth + RequirePermission`；超级管理员放行语义不变；无权限 403、缺失/非法管理员认证返回规范错误、普通用户 JWT 不能访问后台接口；不修改既有 `order:ship`/`order:refund`。
- 性能与数据库：评估现有索引（`uk_order_no`/`uk_user_idempotency`/`idx_user_id`/`idx_status_expire`/`idx_order_id`）对列表、组合筛选与排序 SQL 的支撑；对核心查询提供 EXPLAIN 证据；确认是否需额外 `created_at` 相关索引（若需则新增 migration，遵循 Registry 预留，明确收益/写入开销/兼容性）；避免 N+1 与无界全量返回；不引入 Redis 缓存/Elasticsearch/汇总表。
- 长期设计更新（Design Impact = UPDATE）：更新 `docs/design/order.md`（权限边界、后台路由、移除「后台订单列表/筛选/统计」留白）与 `docs/design/rbac.md`（权限清单）。
- 必要测试：覆盖空数据、多用户订单、多状态、分页边界、多条件筛选、非法时间范围、权限拒绝、订单不存在、大量订单明细查询行为，以及现有用户端/发货/退款/状态机/库存/事务无回归。

## Out of Scope

- Vue / ECharts 前端（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。
- 管理员取消订单、强制修改任意订单状态、重新实现发货/退款。
- 订单操作审计、管理员内部备注、批量订单操作、订单导出。
- 支付网关、真实资金退款、退货售后。
- 秒杀订单后台管理、秒杀库存与消费者改造。
- 订单状态机重构、Redis 查询缓存、Elasticsearch、无关数据库 Migration。
- 收货人姓名/手机号模糊搜索（本阶段不实现）。

## Design Impact

Design Impact: UPDATE
Design Artifact: docs/design/order.md（主）；docs/design/rbac.md（权限清单，次）

## Acceptance Criteria

- [ ] AC-001（分页列表）：管理员携带 `order:list` 权限访问 `GET /admin/orders`，返回全部普通订单的分页结果，结果来自真实 MySQL `orders` 数据。
- [ ] AC-002（分页元数据）：返回的总记录数、当前页、每页数量与实际数据一致；`page`/`size` 有默认值与最大上限，越界/非法分页参数被稳定拒绝，不产生无界全量返回。
- [ ] AC-003（稳定排序）：默认按订单 id 倒序；指定的排序字段仅限白名单，且排序稳定（相同排序键不会因翻页出现重复/遗漏）。
- [ ] AC-004（组合筛选）：`order_no`/`user_id`/`status`/`start_time`/`end_time` 各自筛选结果正确，且可任意组合；时间范围为左闭右开 `[start,end)`。
- [ ] AC-005（参数校验）：非法状态枚举、非法时间范围（如 `start_time >= end_time`）、非法分页参数被稳定拒绝（错误码 + 4xx），不落入任意 SQL 排序或模糊扫描。
- [ ] AC-006（订单详情）：管理员携带 `order:view` 权限按 id 查看任意用户普通订单，返回订单编号/id、用户 id、状态、金额快照、商品明细、地址快照、状态时间戳与取消原因等业务字段。
- [ ] AC-007（历史快照）：详情中的成交金额与商品名称/SKU/单价/数量均来自下单时的历史快照（`orders`/`order_items`），不重读当前 `skus.price`/商品当前售价。
- [ ] AC-008（订单不存在）：查询不存在的订单 id 返回规范错误（`9001` 语义，404），且与越权场景不混淆。
- [ ] AC-009（状态统计）：`GET /admin/orders/stats` 覆盖全部 7 种状态，无数据状态返回 0，只统计普通 `orders`（不混入秒杀订单）。
- [ ] AC-010（普通用户拒绝）：普通用户 JWT 访问 `/admin/orders*` 返回规范错误（401/403），且不产生任何写入。
- [ ] AC-011（无权限管理员拒绝）：已认证但无 `order:list`/`order:view` 权限的管理员访问列表/统计/详情分别返回 403，且无写入。
- [ ] AC-012（超级管理员）：超级管理员访问订单列表、详情、统计被放行，语义与既有 `RequirePermission` 一致。
- [ ] AC-013（无 N+1）：后台订单列表查询不产生逐订单加载明细的 N+1 查询（列表保持轻量；如含明细须批量查询）。
- [ ] AC-014（性能证据）：核心列表/筛选/排序/统计查询有可核实的真实 SQL 与 EXPLAIN 证据；如确认需 `created_at` 索引，说明收益、写入开销与兼容性影响并走 Registry 预留。
- [ ] AC-015（用户端无回归）：现有用户端订单查询、支付、取消、确认收货行为不变。
- [ ] AC-016（发货/退款无回归）：现有管理员发货 `/admin/orders/:id/ship`、退款 `/admin/orders/:id/refund` 行为与权限不变。
- [ ] AC-017（交易机制无回归）：订单状态机、库存补偿与事务逻辑无回归。
- [ ] AC-018（长期设计更新）：`docs/design/order.md` 与 `docs/design/rbac.md` 更新后与 APPROVED Contract、最终实现一致（权限边界、后台路由、权限清单、移除后台订单列表留白）。

## Relevant Context

已核实事实：

- 基线：分支 `feat/admin-order-management`，HEAD `72e066553f9f0947dac2862736cb95668765348a`，working tree clean，无既有未提交修改（与 Owner 提供基线一致）。
- 技术栈与分层：GoFrame v2；`api/<module>/v1`（`g.Meta` path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问 `g.DB().Model()`，无 `dao`/`model` 层。
- 订单模块：`api/order/v1/order.go`（`Order`/`OrderItem`、状态字符串枚举 + DB TINYINT 10~70、`ShipReq/ShipRes`、`RefundReq/RefundRes` 已存在）；`internal/controller/order/order.go` 已有 `AdminControllerV1`（`Ship`/`Refund`，仅 `AdminPrincipal` 校验 + 路由层 `RequirePermission`）；`internal/logic/order/order.go` 已有 `findByID`（无归属过滤）、`loadByIDOrder`、`loadItems`/`loadItemsInTx`、`toOrder`、`statusToString`；`internal/service/order.go` 的 `IOrder` 已含 `List`/`Detail`/`Ship`/`Refund`/`CancelExpired` 等。
- 用户端 `List` 缺陷（本任务不得复制）：`internal/logic/order/order.go` 的 `List` 无分页（`Order("id DESC")` 全量 Scan），并逐单经 `toOrder` → `loadItems` 加载明细，存在 N+1 查询风险；用户端 `Detail` 走 `findOwned`（`user_id` 归属过滤）。
- 路由：`internal/cmd/routes_admin.go` 已有 `require("order:ship").POST("/admin/orders/:id/ship")`、`require("order:refund").POST("/admin/orders/:id/refund")`；无 `/admin/orders`、`/admin/orders/:id`、`/admin/orders/stats`。`internal/cmd/routes_test.go` 用「must-exist + forbidden」两段式锁定路由，新增路由不破坏现有断言，但需补新路由断言。
- RBAC：`internal/middleware/auth.go` 的 `AdminAuth`（验签→401、`type≠admin`→403、会话无效→401、禁用/不存在→401）与 `RequirePermission`（`IsSuper` 放行、命中放行、未命中 403、DB 查询失败 fail-closed 500）已完整；权限 seed 在 `internal/boot/seed.go` 的 `seedPermissionList`，当前含 `order:ship`/`order:refund`，缺 `order:list`/`order:view`。`docs/design/rbac.md` §2.3 权限清单未覆盖 order 域权限。
- 错误码：订单域 `9000-9999` 已在 `.agent/registry/error-codes.md` 登记为 ACTIVE（order-v1），已用 9001~9006；通用 `CodeInvalidArgument`(1001, 400) 可承接非法筛选参数。**本任务不新增错误码域**（订单域已 ACTIVE）；如需新增订单域内具体 code（如非法筛选参数），属域内扩展、无需新域预留。
- 表结构/索引：`orders` 有 `uk_order_no(order_no)`、`uk_user_idempotency(user_id, idempotency_key)`、`idx_user_id(user_id)`、`idx_status_expire(status, expire_at)`；`order_items` 有 `idx_order_id(order_id)`。**无 `created_at` 索引**。时间范围筛选可能触及全表扫描，需 EXPLAIN 评估。
- 统计：当前后端**不存在** Dashboard/统计聚合模块（`internal` 内无 `dashboard`/`GroupBy` 相关代码），Owner 所述「若 Dashboard V1 已实现则优先复用」的条件不成立，状态统计需按 `GROUP BY status` 新写。
- 长期设计现状：`docs/design/order.md` §6 仅记录 ship/refund 路由与 `order:ship`/`order:refund` 权限，§9 明确「后台订单列表与高级筛选/统计/导出」为 Deferred 留白；`docs/design/rbac.md` §2.3 权限清单未含 order 域权限。本任务补该留白并扩展权限边界。

Assumption（合理但未经 Owner 确认，交 Analyst 固化并向 Owner 确认）：

- 后台列表不含商品明细（保持轻量，仅详情返回明细）；这与 Owner「列表只返回必要字段、避免过度加载」一致。
- 分页默认值与上限、排序白名单字段（至少含 id，默认 `id DESC`）由 Analyst 给出合理推荐并写入 Contract。
- 列表/详情出参的 `status` 沿用用户侧字符串枚举（`pending_payment`…`refunded`），不引入新的整数/字符串双态。
- 非法筛选参数优先复用 `CodeInvalidArgument`(1001)；若 Owner 需要区分「非法状态筛选」等更细语义，可在订单域内新增具体 code（域内扩展，无新域预留）。

OPEN QUESTION（不阻塞任务创建，交 Analyst 调查、Owner 确认）：

- 时间范围参数格式与时区转换约定（客户端时间 → MySQL DATETIME 的转换、`[start,end)` 边界实现、与 `CURRENT_TIMESTAMP`/MySQL 会话时区的一致性）。
- 是否需新增 `created_at` 相关索引（需 EXPLAIN 证据；若需则新增 migration 并走 Registry 预留）。
- 列表「必要的订单状态时间信息」具体字段集合（如 `paid_at`/`shipped_at`/`cancelled_at` 等哪些进入列表）。
- 统计接口的响应结构（7 态按字符串枚举还是整数键，是否附 `total`）。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试必须走真实 `RegisterAdminRoutes` + `AdminAuth`/`RequirePermission`，用真实管理员/超级管理员/普通用户账号断言。

- AC-001/AC-002 → 需 MySQL：预置多用户、多状态普通订单后，管理员请求 `GET /admin/orders`，断言分页结果、总记录数、页码、每页数量正确；非法/越界分页参数被拒。
- AC-003 → 需 MySQL：预置同 `created_at` 的多条订单，断言默认 `id DESC` 与白名单排序稳定，翻页无重复/遗漏。
- AC-004 → 需 MySQL：分别及组合传入 `order_no`/`user_id`/`status`/`start_time`/`end_time`，断言筛选结果正确且 `[start,end)` 左闭右开。
- AC-005 → 需 MySQL：非法状态、`start_time >= end_time`、非法分页参数，断言稳定错误码 + 4xx 且无写入。
- AC-006/AC-007 → 需 MySQL：按 id 查询任意用户订单详情，断言金额/商品/SKU/单价/数量为历史快照（修改当前 `skus.price` 后详情不变）。
- AC-008 → 需 MySQL：查询不存在 id 断言 404 规范错误。
- AC-009 → 需 MySQL：预置覆盖 7 态的订单，断言统计各态计数正确、无数据状态为 0、不混入秒杀订单。
- AC-010/AC-011/AC-012 → 需 MySQL/Redis：普通用户 JWT、无权限管理员、超级管理员分别访问列表/详情/统计，断言 401/403/放行语义与零写入。
- AC-013 → 需 MySQL：在 DB 层开启/统计查询日志或断言 SQL 调用次数，证明列表查询不逐单加载明细（无 N+1）。
- AC-014 → 需 MySQL：对核心查询执行 `EXPLAIN`，记录执行计划证据；索引决策附收益/开销/兼容性说明。
- AC-015/AC-016/AC-017 → 需 MySQL：运行现有订单相关测试，确认用户端查询/支付/取消/收货、发货/退款、状态机/库存/事务无回归。
- AC-018 → 文档审查：`docs/design/order.md` 与 `docs/design/rbac.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：本任务虽为只读扩展，但存在需要 Analyst 调查并可能升级 Owner 的设计选择——(1) `created_at` 索引是否需新增 migration（含全局资源 `migration_version` 预留、收益/写入开销/兼容性权衡）；(2) 时间范围与统计的时区转换约定会产生不同查询语义；(3) 新增 `order:list`/`order:view` 权限扩展了订单模块安全边界（Design Impact = UPDATE，需 Analyst 在 Contract APPROVED 后更新 `docs/design/order.md`/`docs/design/rbac.md`）；(4) 列表响应字段形状、分页默认/上限、排序白名单、非法筛选错误码等需 Contract 固化，避免 Coder 猜测。

## Analyst Questions

1. 索引与迁移：在现有索引下对「时间范围筛选 + `ORDER BY id DESC LIMIT`」与「`status` 精确筛选」执行 EXPLAIN，评估是否需要新增 `created_at` 相关索引；若需，明确复合索引方向、收益、写入开销与兼容性影响，并申请 `migration_version` 预留（语义：orders 增加 created_at 相关索引）；若不需要，说明现有索引如何支撑并给出 EXPLAIN 证据。
2. 时区与时间范围：`start_time`/`end_time` 的入参格式与时区语义；客户端时间 → MySQL DATETIME 的转换约定；左闭右开 `[start,end)` 的实现与边界校验；与 `created_at` 默认 `CURRENT_TIMESTAMP`（MySQL 会话时区）的一致性。
3. 列表响应形状：列表返回字段集合（含哪些状态时间信息）、`status` 出参形式（字符串枚举 vs 整数）、是否含明细（建议不含，详情才含）。
4. 分页与排序：`page`/`size` 默认值与上限、排序白名单字段及稳定排序规则（默认 `id DESC`）。
5. 错误语义：非法状态/非法时间范围/非法分页的稳定错误码（复用 `1001` vs 订单域内新增具体 code）与 HTTP 状态；详情「不存在」与「越权」的错误码边界。
6. 权限与 seed：`order:list`/`order:view` 权限 code 语义、seed 登记位置、路由挂载（列表/统计 `order:list`、详情 `order:view`），确认超管放行与 403 语义沿用现有 `RequirePermission`。
7. 统计响应结构：7 态计数结果的键形态（字符串枚举 vs 整数）与是否附 `total`。
8. 长期设计：更新 `docs/design/order.md`（§6 权限边界/路由、§9 移除后台订单列表留白）与 `docs/design/rbac.md`（§2.3 权限清单）的具体内容与一致性。

## Review Baseline

- Base commit：`72e066553f9f0947dac2862736cb95668765348a`（分支 `feat/admin-order-management`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增/修改产物为 `.agent/tasks/admin-order-management/`、`api/order/v1/order.go`（新增后台 list/detail/stats 请求响应）、`internal/controller/order/order.go`（新增后台只读 handler）、`internal/service/order.go`（扩展 `IOrder`）、`internal/logic/order/order.go`（新增后台 list/detail/stats 方法）、`internal/boot/seed.go`（新增 `order:list`/`order:view`）、`internal/cmd/routes_admin.go` 与 `internal/cmd/routes_test.go`（新增路由与断言）、`docs/design/order.md`/`docs/design/rbac.md`（Analyst 更新），以及（若需）1 个 `created_at` 索引 migration 文件。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）

---

## Owner 学习材料（设计原因说明）

以下解释为何本任务采用这些设计，每项对应真实代码路径：

1. **为什么不能直接复用用户端订单列表？** 用户端 `internal/logic/order/order.go` 的 `List` 只查本人订单（`Where("user_id", userID)`），且无分页、逐单加载明细（N+1）；后台需要「全部订单 + 分页 + 组合筛选 + 轻量列表」，语义不同，直接复用会引入全量加载与 N+1。
2. **为什么需要数据库分页？** 用户端 `List` 用 `Order("id DESC").Scan(&rows)` 无 `Limit/Page`，全量返回；后台面对全部订单，必须 `LIMIT/OFFSET` 在 MySQL 侧分页，避免无界全量返回与内存放大。
3. **N+1 查询如何产生？** `List` 循环内 `toOrder(ctx, r)` 会调用 `loadItems(ctx, r.Id)` 为每个订单单独查一次 `order_items`，N 个订单 = 1 + N 次查询。后台列表因此保持轻量（不含明细），详情才按单查明细。
4. **为什么使用独立的订单查询 RBAC？** 现有后台只读接口（product/banner/inventory/recommendation）仅 `AdminAuth`、无读权限；但订单是敏感交易数据，Owner 要求独立 `order:list`/`order:view` 读权限，遵循最小权限原则（`internal/middleware/auth.go` 的 `RequirePermission`）。
5. **为什么普通订单与秒杀订单分开管理？** `orders` 与 `flash_sale_orders`/`flash_sale_order_requests` 是两套独立事实：状态机、库存模型、异步队列语义均不同；本任务只读普通 `orders`，避免口径混入。
6. **本任务为什么不修改订单状态机？** 状态机是交易正确性核心（`docs/design/order.md` §3 与 `internal/logic/order/order.go` 的 `status*` 常量 + 条件更新 + `RowsAffected` 原子闸门），本任务是纯只读查询，改动状态机会引入交易回归风险且超出范围。
