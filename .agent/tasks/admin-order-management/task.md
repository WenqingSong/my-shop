# Task: 后台订单查询管理（Admin Order Management）V1

## Goal

为 SurgeCart（my-shop）补齐管理员订单查询的只读能力：管理员携带有效后台 JWT、持有对应只读权限，可通过后台接口分页查看全部普通订单、按条件组合筛选、查看订单详情（含商品明细与快照）与订单状态统计。所有数据来自真实 MySQL 的普通订单（`orders` / `order_items`），不合并秒杀订单（`flash_sale_orders`），不重复实现已有订单业务（创建/支付/取消/收货/发货/退款），不改变现有订单状态机与库存正确性。

## Scope

- 后台订单分页列表 `GET /admin/orders`：查询全部普通订单；支持 `page`/`size` 分页（复用商品 `AdminList` 分页规范，`size` 上限封顶）；返回总记录数；支持按 `id`/`created_at` 排序（排序字段白名单）；默认按订单 `id` 倒序，追加稳定 tiebreaker 保证分页可预测；批量加载订单项（`WHERE order_id IN (...)`），避免逐订单 N+1。
- 后台订单组合筛选（列表接口内）：`order_no` 精确、`user_id` 精确、`status` 精确、`created_at` 时间范围；多条件 AND 组合；校验非法状态值、非法/倒置时间范围；参数化 SQL；避免全表扫描。
- 后台订单详情 `GET /admin/orders/:id`：复用现有 `findByID` / `loadByIDOrder` / `loadItems` / `toOrderItems`；返回订单完整字段（订单号、用户、状态、金额、商品明细、SKU/商品快照、收货地址快照、各状态变更时间、取消原因等）；管理员可查看任意普通订单，不使用用户侧 `user_id` 归属过滤，不修改用户端详情权限语义；不暴露不必要的敏感内部字段。
- 订单状态统计 `GET /admin/orders/stats`：`GROUP BY status` 统计普通订单各状态数量；缺省状态补 0；不含秒杀订单与秒杀 queued 请求；不把 Mock 支付金额描述为真实营收。
- 后台 RBAC：新增只读权限 `order:list`（列表）、`order:view`（详情），统计接口使用明确的只读权限；登记 `internal/boot/seed.go`；复用 `middleware.AdminAuth` + `middleware.RequirePermission` 与 `AdminPrincipal`；普通用户 JWT 不得访问后台接口；无对应权限管理员返回 403；保持超管放行与 `order:ship`/`order:refund` 现有语义不变。
- 长期设计：更新 `docs/design/order.md`（Design Impact = UPDATE）：新增后台只读路由、只读权限 code、（如 Analyst 确认）新增索引。
- 必要测试：覆盖正常、拒绝、边界（见 Acceptance Criteria）。

## Out of Scope

- 前端页面（`frotend_web`/`frotend_manage`）。
- 管理员取消订单、强制修改订单状态、批量订单操作。
- 管理员操作审计、订单内部备注。
- 秒杀订单后台管理（`flash_sale_orders` / `flash_sale_order_requests` 独立闭环）。
- 支付系统改造、退款业务改造、发货业务改造、订单库存补偿改造。
- Dashboard 综合数据统计（销售额趋势、用户增长、商品排行榜等）。
- Redis 缓存、Elasticsearch、数据导出。
- 不重新实现/改写已有创建、支付、取消、收货、发货、退款逻辑。

## Design Impact

Design Impact: UPDATE
Design Artifact: docs/design/order.md

## Acceptance Criteria

- [ ] AC-001（分页查看全部普通订单）：给定有效后台 JWT 且持有列表权限的管理员，当访问 `GET /admin/orders` 时，返回全部普通订单（含不同用户、不同状态的订单）的分页结果；结果仅来自 `orders`/`order_items`，不含 `flash_sale_orders` 记录。
- [ ] AC-002（分页与排序正确）：给定已知订单集合，当按 `page`/`size` 分页并按 `id`（默认倒序）或 `created_at` 排序时，返回的 `total` 与 MySQL `orders` 总数一致，页码/每页数量正确，排序稳定且分页不重不漏。
- [ ] AC-003（单条件筛选正确）：给定 `order_no`、`user_id`、`status`、`created_at` 范围任一条件，当查询时，仅返回满足条件的普通订单；`status` 使用真实订单状态枚举并映射 TINYINT。
- [ ] AC-004（多条件组合筛选正确）：给定多个筛选条件（如 `status` + `created_at` 范围 + `user_id`），当查询时，返回满足全部条件的订单（AND 语义），`total` 与 MySQL 匹配。
- [ ] AC-005（查看详情与商品明细）：给定存在的普通订单 id，当管理员访问 `GET /admin/orders/:id` 时，返回该订单完整信息与其商品明细；管理员可查看任意用户的订单（无 user_id 归属过滤）。
- [ ] AC-006（详情含真实快照）：订单详情返回的收货地址与总金额为下单时快照值（与 `orders` 行一致），商品明细的 SKU/商品名、主图、成交价、数量与 `order_items` 行一致。
- [ ] AC-007（订单不存在正确报错）：给定不存在的订单 id，当管理员访问详情时，返回稳定的「订单不存在」（404）错误；不泄露内部信息。
- [ ] AC-008（状态统计与数据一致）：当访问 `GET /admin/orders/stats` 时，返回各状态（10/20/30/40/50/60/70）订单数量；与 `SELECT status, COUNT(*) FROM orders GROUP BY status` 一致；无订单的状态返回 0；不含 `flash_sale_orders`。
- [ ] AC-009（普通用户禁止访问）：给定普通用户 JWT，当访问任一后台订单接口时，返回无权限（401/403）且无写入；后台接口不信任用户侧 token。
- [ ] AC-010（无权限管理员禁止访问）：给定持有后台 JWT 但缺少对应只读权限的非超管管理员，当访问列表/详情/统计接口时，返回 403；超管可访问。
- [ ] AC-011（非法参数拒绝）：给定非法分页（超上限 size 被正确处理或拒绝）、非法 `status` 值、倒置/非法 `created_at` 范围，当查询时，返回稳定参数错误（400），不执行无界全量查询。
- [ ] AC-012（列表无 N+1）：当分页查询 N 个订单时，`order_items` 的加载使用 `WHERE order_id IN (...)` 批量查询（查询次数与 N 无关，为常量），而非逐订单循环查询。
- [ ] AC-013（不改变现有订单逻辑）：本任务不修改发货/退款/取消/收货/支付/库存扣减与恢复逻辑；相关现有测试（Order / IAM / RBAC）仍通过，越权与状态机不变量不变。
- [ ] AC-014（现有测试通过）：现有 Order、IAM、RBAC 相关测试全部通过。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2；分层 `api/order/v1`（`g.Meta` 声明 path/method）→ `internal/controller/order` → `internal/service`（`IOrder`）→ `internal/logic/order`；数据访问 `g.DB().Model()`。
- 普通订单表 `orders`（`id`、`order_no`、`user_id`、`status` TINYINT、`total_amount`、`idempotency_key`、`request_hash`、收货地址快照字段、`address_id`、`expire_at`、`cancel_reason`、`paid_at`/`shipped_at`/`received_at`/`completed_at`/`cancelled_at`/`refunded_at`、`created_at`/`updated_at`）与 `order_items`（`order_id` FK CASCADE、`sku_id`/`product_id` 软引用、`sku_name`/`product_name`/`product_main_image`/`price`/`quantity` 快照），见 `internal/migrations/sql/20261001000007_orders.up.sql`。
- `orders` 现有索引：`uk_order_no(order_no)`、`uk_user_idempotency(user_id, idempotency_key)`、`idx_user_id(user_id)`、`idx_status_expire(status, expire_at)`；**无 `created_at` 专用索引**（Owner 要求先 EXPLAIN 评估再决定是否新增）。
- 秒杀订单表 `flash_sale_orders` 与秒杀请求队列 `flash_sale_order_requests` 为独立业务闭环，与普通订单 `orders` 分离（见 `20261001000011_flash_sale.up.sql`、`20261001000015_flash_sale_order_requests.up.sql`）。
- 订单状态：DB TINYINT 10/20/30/40/50/60/70；API 出参为字符串枚举（`pending_payment`/`paid`/`shipped`/`received`/`completed`/`cancelled`/`refunded`），映射函数 `statusToString`（`internal/logic/order/order.go`）。
- 现有后台订单仅写接口：`POST /admin/orders/:id/ship`（`order:ship`）、`POST /admin/orders/:id/refund`（`order:refund`），挂 `routes_admin.go` 的 `require(...)` 分组；控制器 `internal/controller/order/order.go` 的 `AdminControllerV1`。
- 后台认证授权：`middleware.AdminAuth`（解析 type=admin JWT，校验会话与 admins 状态，注入 `AdminPrincipal{AdminID, Sid, IsSuper}`）；`middleware.RequirePermission(code)`（超管放行，否则查权限 code，命中放行、未命中 403、DB 失败 fail-closed 500），见 `internal/middleware/auth.go`。
- 权限 seed：`internal/boot/seed.go` 的 `seedPermissionList` 已含 `order:ship`/`order:refund`，**无 `order:list`/`order:view`**；新增权限需登记该清单（`permissions.code` 唯一约束兜底）。
- 后台只读接口先例：`/admin/products`（`AdminList`）、`/admin/products/:id`、`/admin/inventories/:sku_id`、`/admin/banners`、`/admin/recommend-positions` 等仅 `AdminAuth`、无独立读权限 code（`routes_admin.go`）。本任务按 Owner 要求为订单引入 `order:list`/`order:view` 读权限，与既有先例不同，需在 Contract 中明确这一差异的取舍。
- 分页规范（商品 `AdminList`）：默认 `page=1`、`size=20`、上限 `size=100`；`normalizePage` 归一化；排序字段白名单 `sortColumns` → 非法回落默认；追加 `id` tiebreaker 保证稳定；`Count()` + `.Page(page, size)`，见 `internal/logic/product/product.go`。本任务沿用该规范。
- 复用点已存在：`findByID`（无归属过滤）、`loadByIDOrder`、`loadItems`、`toOrderItems`、`toOrder`；`List` 现实现逐订单调用 `toOrder`→`loadItems`，存在 N+1（后台列表不可照搬）。
- 错误码：订单域 9000-9999（9001 订单不存在/9002 非法迁移/9003~9006）；通用 1001（参数错误）、1002（未授权）、1003（禁止访问）。后台详情「不存在」可复用 9001（404）；非法分页/状态/时间用 1001（400）。本任务预计无需新增错误码域（如 Analyst 判断需要，须声明资源）。
- 迁移机制 golang-migrate：新增 migration 需同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion` 与 `businessTables`；当前最新 `20261001000018`。

Assumption：

- 后台订单列表/详情响应结构沿用现有 `v1.Order`（详情含 `Items`）；列表是否内联商品明细、以及是否对后台暴露 `idempotency_key`/`request_hash` 等内部字段，由 Analyst 在 Contract 中明确（倾向列表返回基本信息、详情返回完整信息，敏感字段按需脱敏）。
- 状态筛选参数形式与出参一致使用字符串枚举（映射 TINYINT），而非直接接受数值；具体以 Contract 为准。
- 统计接口复用 `order:list`（或 `order:view`）作为只读权限，不新增第三个 code；具体以 Contract 为准。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 是否需要为后台组合筛选（尤其 `created_at` 范围、`status`、`user_id` 组合）新增索引：需以真实 SQL + EXPLAIN 证据决定；若需要，新增 1 个 migration（全局资源，Analyst 派生版本并预留）。
- `created_at` 时间范围的边界语义（闭/开区间）、时区基准、是否限制最大时间跨度。
- 详情/列表对 `idempotency_key`/`request_hash` 等内部字段是否向后台暴露。
- 统计接口权限 code 的归属（`order:list` vs `order:view`）。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试走真实 `RegisterAdminRoutes` + `AdminAuth`/`RequirePermission`，用真实管理员/超管/无权限管理员/普通用户断言。

- AC-001/AC-002 → 需 MySQL：预置多用户、多状态普通订单，断言分页列表返回全部普通订单、`total` 与 `SELECT COUNT(*) FROM orders` 一致、页码/每页数量正确、排序稳定不重不漏；预置秒杀订单断言不出现于列表。
- AC-003/AC-004 → 需 MySQL：分别/组合按 order_no、user_id、status、created_at 范围查询，断言结果集与等价的参数化 SQL 查询一致。
- AC-005/AC-006/AC-007 → 需 MySQL：断言详情返回完整字段与商品明细、地址/金额快照与 `orders`/`order_items` 行一致；不存在的 id 返回 404（9001）。
- AC-008 → 需 MySQL：断言 stats 各状态数量与 `GROUP BY status` 一致、缺省状态为 0、不含秒杀订单。
- AC-009/AC-010 → 需 MySQL + Redis：普通用户 token 访问后台接口返回无权限且无写入；无对应权限的非超管管理员返回 403；超管可访问。
- AC-011 → 需 MySQL：非法 size/status/时间范围断言稳定 400、不触发全量查询。
- AC-012 → 需 MySQL：分页查询多订单时，通过查询日志/计数断言 `order_items` 批量加载查询次数为常量（非随 N 增长）。
- AC-013/AC-014 → 回归：现有 Order / IAM / RBAC 测试全部通过；Diff 确认未改动发货/退款/取消/收货/库存逻辑。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Complexity

COMPLEX

原因：本任务新增后台订单只读公开协议（列表/详情/统计）与只读权限 code（`order:list`/`order:view`），改变 `docs/design/order.md` 记录的权限边界与路由清单（Design Impact = UPDATE）；`created_at` 时间范围筛选面临「是否新增索引」的现实方案选择（需 EXPLAIN 证据，若新增则产生 1 个全局 migration 资源，需 Analyst 派生并预留）；时间范围边界/时区语义、状态筛选参数形式、统计接口权限归属、后台是否暴露内部字段等细节需固化 Contract；需交 Analyst 分析后由 Owner 确认。

## Analyst Questions

1. 索引与 Migration：对后台列表/组合筛选（`status`、`user_id`、`created_at` 范围）在真实数据下做 EXPLAIN，判断是否需要为 `created_at`（或组合）新增索引；若需要，明确索引定义并新增 1 个 migration（版本由 Analyst 读 `.agent/registry/migrations.md` 派生并预留）。
2. 时间范围语义：`created_at` 范围的闭/开区间、时区基准（与现有 `NOW()`/DATETIME 语义一致）、是否限制最大时间跨度、非法/倒置范围的错误语义。
3. 状态筛选参数：使用 API 字符串枚举（映射 TINYINT）还是直接接受数值；非法值拒绝语义（400 与错误码）。
4. 权限模型：`order:list`/`order:view` 的命名与 seed 登记；统计接口使用哪个只读权限 code（`order:list`/`order:view` 还是新增 `order:stats`）；后台只读接口引入独立读权限后，与既有「后台只读仅 AdminAuth」先例的差异取舍（是否仅订单域引入、是否需回填其他模块——本次不回填）。
5. 响应结构与字段暴露：列表是否内联商品明细；详情/列表是否对后台暴露 `idempotency_key`/`request_hash` 等内部字段（敏感字段脱敏边界）。
6. 分页与筛选 SQL 设计：稳定排序 tiebreaker、批量加载 `order_items` 的 `IN` 查询、`Count` 与分页查询的一致性（避免 N+1、避免全量扫描）。

## Review Baseline

- Base commit：`b42af81ef235c09f4e97f84ac55625507f35bf2a`（分支 `main`）。
- 任务开始时已有修改：无（`git status --short` 为空，working tree clean）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/admin-order-management/`；后续 Analyst/Coder 产物集中在 `api/order/v1/order.go`、`internal/controller/order/`、`internal/logic/order/`、`internal/service/order.go`、`internal/boot/seed.go`、`internal/cmd/routes_admin.go`、`docs/design/order.md`、可能的 migration 与测试。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）
