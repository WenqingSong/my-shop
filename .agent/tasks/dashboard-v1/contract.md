# Technical Contract

## Decision Status
APPROVED

## Problem

为 SurgeCart 后台新增 5 个管理员运营数据大屏只读 API（运营概览、订单趋势、订单状态分布、商品销量 TOP 10、秒杀运营统计），供 Vue + ECharts 前端消费。本阶段只交付 Go 后端，不改动前端、不改现有交易/秒杀/IAM/商品生产逻辑。

本任务关键难点是**统计口径**（金额、销量、订单边界、时区切分会产生不同业务数字），且新增 5 个后台公开 API + `docs/design/dashboard.md`（Design Impact = NEW）。口径与协议经 Analyst 调查、Owner 决策固化于本 Contract，Coder 不得自行拍板。

## Verified Current Behavior

- VERIFIED：后台为独立身份域 `admins` + `type=admin` JWT + `iam:admin:session:{sid}`；`internal/cmd/routes_admin.go` 中后台写接口叠加 `RequirePermission(code)`，读接口可叠加。`AdminAuth` 判定：验签+exp→401、type≠admin→403、会话无效→401、admins 禁用→401（`internal/middleware/auth.go`）。`RequirePermission` 对超级管理员（`IsSuper`）直接放行，否则查权限 code、未命中 403。
- VERIFIED：统一响应 `{code,message,data}`（`internal/middleware/response.go`）；错误码集中 `internal/codes/codes.go`，通用域 `1001`（400）/`1002`（401）/`1003`（403）/`1000`（500）全项目复用。权限 code 属 B 类 namespace（不占全局资源），seed 于 `internal/boot/seed.go` 的 `seedPermissionList`。
- VERIFIED：表结构（`internal/migrations/sql/*.up.sql`）：
  - `users(id, username, password_hash, created_at, updated_at)`，`created_at` 无索引。
  - `products(id, name, brand, category_id, price, main_image, detail, status[0=draft/1=on_shelf/2=off_shelf], view_count, created_at, updated_at)`，**无删除接口/无 deleted 标记**；索引 `idx_category_id`、`idx_status`。
  - `orders(id, order_no, user_id, status[10/20/30/40/50/60/70], total_amount[整数分快照], ..., paid_at/refunded_at/..., created_at, updated_at)`；索引 `uk_order_no`、`uk_user_idempotency`、`idx_user_id`、`idx_status_expire(status, expire_at)`，**无 created_at 索引**。
  - `order_items(id, order_id, sku_id, product_id, product_name, price, quantity, created_at)`，索引 `idx_order_id(order_id)`，**无 product_id 索引**。
  - `flash_sale_activities(status[0=disabled/1=enabled], start_time, end_time)`；`flash_sale_orders`（行存在即成功订单，无状态机）；`flash_sale_order_requests(status[0=queued/1=success/2=failed/3=dead], idx_dequeue(status, next_attempt_at, id))`。
- VERIFIED：支付为 Mock（`paid_at` 仅记录 Mock 支付时间，无真实到账），「订单金额」不能等同「实收销售额」。
- VERIFIED：**时间存储约定**——`manifest/config/config.yaml` 与 `internal/boot/boot.go`、`internal/migrations/migrations.go` 的 DSN 均无 `time_zone`/`loc`/`parseTime`；DATETIME 写入全部走 MySQL 时钟（`created_at` 默认 `CURRENT_TIMESTAMP`；`expire_at`/`paid_at`/`shipped_at`/`refunded_at`/秒杀时间窗判定均用 `NOW()`，见 `internal/logic/order/order.go` 注释「避免 Go 进程与 MySQL 的时区不一致导致过期判定偏移」）；Go 读取 DATETIME 为 wall-clock 字符串，无时区换算。即：**存储时钟 = MySQL 会话/服务器时区，Go 不参与时间写入**。docker MySQL 容器未设 TZ（`docker-compose.yml`），服务器时钟默认 UTC（INFERENCE，交付时以 `SELECT @@system_time_zone` 实测确认）。
- VERIFIED：无现有 Dashboard/统计后端；分支 `feat/backend-dashboard`、HEAD `f0bf42cbf8592215a76173ef75d2eac5078960e2`，工作区仅未跟踪 `.agent/tasks/dashboard-v1/`。
- VERIFIED：新增后台只读 API 范式：`api/<module>/v1`（`g.Meta path`）→ `internal/controller/<module>` → `internal/service/<module>.go`（接口+Register）→ `internal/logic/<module>`（`g.DB().Model` 聚合），参考 `banner` 模块。
- UNKNOWN：MySQL 服务器实际时区（当前环境 MySQL 未运行，`@@system_time_zone` 待实测）；各聚合查询 EXPLAIN 执行计划（待实现阶段验证）。

## Selected Design

Owner 已确认的核心口径与约束（详见 Owner Decision Record）：

1. **订单边界**：Overview / 趋势 / 状态分布 / 时间范围总数的「订单」仅指普通 `orders`；秒杀订单 `flash_sale_orders` 仅在「秒杀运营统计」单独呈现，不混合。
2. **数量类口径**：订单总数 / 今日新增订单数 / 每日趋势 / 状态分布 / 时间范围总数，按 `created_at` + **全部订单状态**统计（下单量）；状态分布显式返回 7 个状态（含 0）。
3. **金额口径**：`today_order_amount` = `SUM(orders.total_amount)` 且 `created_at ∈ 今日` 且 `status ∈ {20,30,40,50}`；命名为 `today_order_amount`，文案标注「已支付口径」，**不得出现「实收销售额」**。语义 = 「创建于今日、**当前**处于有效状态的订单金额」，**不等于今日支付金额**。
4. **销量口径**：`SUM(order_items.quantity)` JOIN `orders` 且 `status ∈ {20,30,40,50}`，与金额共用同一状态过滤；只来自真实订单明细，不用 `view_count`/库存变化。
5. **当前快照语义**：金额/销量按「**当前**订单状态」过滤（非「创建当日状态」），因此订单后续退款（20→70）会使其从历史日期的有效金额/销量中移除；文档、实现、测试必须固化此语义。
6. **时区**：新增配置 `dashboard.timezone`（默认 `Asia/Shanghai`）为系统权威业务时区；「今日零点」/趋势锚点/时间范围边界按该时区计算；时间范围左闭右开 `[start, end)`；边界必须与实际存储时区一致（见下）。
7. **权限**：Dashboard 全部接口使用 `AdminAuth + RequirePermission("dashboard:view")`；`dashboard:view` 计入 `internal/boot/seed.go` 的 `seedPermissionList`（B 类 namespace，不占全局资源）；超管 `IsSuper` 直接放行；普通用户 403、无 `dashboard:view` 管理员 403。
8. **商品总数**：`COUNT(products)`（全部 SPU，含 draft/on_shelf/off_shelf，无删除概念）。
9. **秒杀维度**：全局聚合（不按活动过滤）；「当前 queued 数量」= `COUNT(flash_sale_order_requests WHERE status=0)`，不叠加时间窗；`success_orders` = `COUNT(flash_sale_orders)`，`queued` 不计入成功订单。
10. **索引（有条件）**：不预先新增索引。Coder 先按现有索引实现聚合查询，结合真实 SQL + `EXPLAIN` + 数据规模给出证据；若确认 `orders.created_at`（优先）等谓词造成无界全表扫描，则新增必要索引，**经 Contract Revision 按 Registry 最新值预留 migration_version（不得假定 20261001000019）**。

### 时区落地设计（边界与存储一致）

- 配置 `dashboard.timezone`（默认 `Asia/Shanghai`），语义为「Dashboard 日期切分时区」= 数据落值时钟。
- 连接层使 MySQL 会话时区与该时区一致（`Asia/Shanghai` → `+08:00`，经 DSN `time_zone` 参数或等价连接初始化），使 `NOW()/CURDATE()/CURRENT_TIMESTAMP` 与 Go `time.LoadLocation(dashboard.timezone)` 一致，消除 Go↔MySQL 漂移。
- Go 端按 `time.LoadLocation(dashboard.timezone)` 计算边界并格式化为 DATETIME 字符串：
  - 「今日」= `[今日 00:00:00, 明日 00:00:00)`；
  - 「最近 7 天」= 7 个自然日桶，整体 `[6 天前 00:00:00, 明日 00:00:00)`；
  - 「时间范围」= 入参 RFC3339 转该时区后 `[start, end)`。
- SQL 用 `created_at >= ? AND created_at < ?` 字符串范围比较，不用函数包裹列（避免索引失效）。

## Interfaces and Data

新增 5 个后台只读接口，全部挂 `AdminAuth + RequirePermission("dashboard:view")`，统一响应 `{code,message,data}`；参数错误复用 `1001`（400）、未授权/越权 `1002`（401）/`1003`（403）、DB 技术错误 `1000`（500）。JSON 字段 snake_case。

1. `GET /admin/dashboard/overview`（无参数）
   - data：`{ user_total, today_new_users, product_total, order_total, today_new_orders, today_order_amount }`
   - `today_order_amount` int64（分），口径 = 创建于今日 + 当前状态 `{20,30,40,50}`；`today_new_users`/`today_new_orders` 按 `created_at ∈ 今日`（全部状态）。
2. `GET /admin/dashboard/orders/trend`（无参数，固定最近 7 天）
   - data：`{ days: [{ date: "YYYY-MM-DD", order_count }] }`，7 项连续、无数据日期 0。
3. `GET /admin/dashboard/orders/status`（可选 `start_time`/`end_time`，RFC3339，二者同传且 `start < end`）
   - data：`{ total, status_distribution: [{ status: 10|20|30|40|50|60|70, count }] }`（7 状态显式返回，含 0）；时间范围 `[start, end)`。
4. `GET /admin/dashboard/products/top`（可选 `limit`，默认 10，上限 50）
   - data：`{ items: [{ rank, product_id, product_name, sales }] }`
   - 单条 SQL：`order_items` JOIN `orders`（`status ∈ {20,30,40,50}`）+ LEFT JOIN `products`（当前名，COALESCE 快照兜底）+ `GROUP BY product_id` + `ORDER BY SUM DESC` + `LIMIT`；无 N+1。
5. `GET /admin/dashboard/flash-sales`（无参数）
   - data：`{ activity_total, activity_status: { enabled, disabled }, request_status: { queued, success, failed, dead }, current_queued, success_orders }`
   - `activity_status` 按 `flash_sale_activities.status` 字段（0/1），不按时间窗派生「已结束」；`request_status` 全局聚合；`success_orders` = `COUNT(flash_sale_orders)`。

新建模块结构：`api/dashboard/v1`、`internal/controller/dashboard`、`internal/logic/dashboard`、`internal/service/dashboard.go`，`internal/cmd/routes_admin.go` 挂载（`require("dashboard:view")`）。

## Business Invariants

- INV-001（身份与权限）：Dashboard 全部接口仅 `type=admin` 且持 `dashboard:view`（或超管）可访问并返回 `code=0`；普通用户 → 403，无/非法/失效 token → 401，无 `dashboard:view` 管理员 → 403；任何拒绝路径不产生数据库写入。
- INV-002（金额口径不可混淆）：`today_order_amount` 只统计 `orders` 且 `created_at ∈ 今日` 且 `status ∈ {20,30,40,50}`；字段名/文案不含「实收销售额」；值 = `SUM(total_amount)` 按该口径一致。
- INV-003（销量口径不可混淆）：TOP 10 销量只统计 `order_items` 关联的 `orders.status ∈ {20,30,40,50}`，不用 `view_count`/库存变化；值 = `SUM(order_items.quantity)` 一致。
- INV-004（订单边界）：普通订单指标只统计 `orders`；秒杀成功订单只统计 `flash_sale_orders`；两套数字不混合。
- INV-005（趋势连续不重不漏）：7 天序列连续、空日 0、逐日求和 = 7 天总数；左闭右开边界不重不漏。
- INV-006（时区一致）：边界按 `dashboard.timezone`（默认 Asia/Shanghai）计算且与 MySQL 会话时区一致；北京零点、跨天、连续 7 天统计正确。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL；Dashboard 纯只读聚合，无 Redis 统计缓存、无预聚合表、无 MQ、无跨系统事务、无并发写。每个接口 = 若干只读聚合 SQL，成功响应代表「该时刻的 MySQL 聚合快照」，不代表任何写入。
- 失败语义：参数非法 → `1001`（400）；未授权/越权 → `1002`/`1003`；DB 技术错误 → `1000`（500），不泄漏底层细节；全部失败路径无写入副作用。
- 一致性：多接口之间是「不同时刻的独立快照」，不承诺跨接口强一致；单接口内指标各自聚合、可逐项对账。金额/销量为「当前状态」时点快照，随后续退款等状态变化而变（历史日期回顾值可变化）。

## Allowed / Forbidden Changes

- 允许：
  - 新增 `api/dashboard/v1`、`internal/controller/dashboard`、`internal/logic/dashboard`、`internal/service/dashboard.go` 及测试。
  - `internal/cmd/routes_admin.go` 新增 5 条路由（`require("dashboard:view")`）。
  - `internal/boot/seed.go` 新增 `dashboard:view` 权限 seed。
  - 新增配置 `dashboard.timezone`（默认 `Asia/Shanghai`）与连接层时区统一。
  - 若实现阶段 EXPLAIN 证据确认需索引：经 Contract Revision 新增 migration（含 `idx_orders_created_at` 等，按需）。
  - 新增 `docs/design/dashboard.md`。
- 禁止：
  - 修改 `orders`/`order_items`/`flashsale`/`product`/`users`/`iam` 生产逻辑、状态机、Redis Lua Gate、消费者。
  - 引入 Redis 统计缓存、预聚合表、定时统计任务、WebSocket/SSE、导出。
  - 触碰 `frotend_web`/`frotend_manage`。
  - 把金额命名为「实收销售额」，或把 `view_count`/库存变化当销量。
  - 未经 Contract Revision 自行新增 migration/索引、自行 `max+1` 占 migration version。

## Verification Requirements

- INV-001 → 管理员（持 `dashboard:view`）、普通用户、无 `dashboard:view` 管理员、无/失效 token 分别请求，断言 200/403/403/401，且拒绝路径表计数不变。
- INV-002 → 与 `SUM(total_amount) WHERE status IN (20,30,40,50) AND created_at∈今日` 一致；字段名/文案不含「实收销售额」；退款后回顾历史日期金额随之变化（当前快照口径）。
- INV-003 → 含待支付/已取消/已退款订单明细样本不计入销量；与 `SUM(quantity) WHERE status IN (20,30,40,50)` 一致。
- INV-004 → 普通订单 + 秒杀订单样本，overview/趋势/分布只含 `orders`，秒杀统计只含 `flash_sale_orders`。
- INV-005 → 某天有/无订单样本，7 天连续、空日 0、逐日求和 = 总数；跨零点订单左闭右开不重不漏。
- INV-006 → 北京零点、跨天、连续 7 天统计正确；核实 MySQL `@@session.time_zone`/`@@system_time_zone` 与 `dashboard.timezone` 一致。
- AC-010 → 非法时间范围（start≥end、单边缺省、格式错）与超上限 `limit` 返回 400 `1001`。
- 索引证据 → 实现阶段对真实 SQL 跑 `EXPLAIN`（含数据规模），记录 `orders.created_at` 等谓词是否全表扫描；若需索引，附证据走 Contract Revision。
- AC-011 → `go test -p 1 ./...`；关键口径至少一个测试在口径错误时失败。
- AC-012 → `gofmt`、`go build ./...`、`go vet ./...`、`go test ./...` 通过；确认无订单/秒杀/商品/IAM 行为改动。

## Open Risks

- 时区跨模块影响：连接层会话时区统一为 `+08:00` 会改变 `NOW()/CURRENT_TIMESTAMP` 的墙钟参考（订单 `expire_at`、秒杀时间窗、各 `*_at`）。现有代码用 `NOW()` 相对一致、内部自洽，但需回归订单超时取消与秒杀时间窗测试，确认无漂移。
- 历史数据偏移：若历史 DATETIME 由非北京时间写入，统一时区后「今日」边界对历史数据偏移；dev 每日重置环境可接受，生产需一次性校正（记为已知留白）。
- 索引收益未定：`idx_order_items_product` 对 `JOIN + GROUP BY` 的实际收益需 EXPLAIN 确认；如不新增索引，TOP 10 聚合为 `order_items` 全表扫描（单查询非 N+1，V1 可接受，属已知性能边界）。
- 多接口为独立快照，跨接口数字存在非同一时刻的轻微不一致（可接受，仅要求单接口内可对账）。

## Owner Decision Record

- 2026-10-10，Owner 决定（`WAITING_FOR_OWNER_DECISION` → `APPROVED`）：
  1. **统计口径 ACCEPT**：订单数量/今日新增/趋势/状态分布仅普通 `orders`；数量按 `created_at` + 全部状态；金额/销量仅 `{20,30,40,50}`；秒杀独立；`today_order_amount` 命名且不得「实收销售额」；`today_order_amount` = 创建于今日 + 当前有效状态的订单金额，≠ 今日支付金额；退款等状态变化会改变历史日期有效金额（当前快照口径）。
  2. **时区 ACCEPT WITH CONSTRAINTS**：`dashboard.timezone` 默认 `Asia/Shanghai`；左闭右开 `[start,end)`；核实 MySQL 会话时区与 DATETIME 存储约定；边界与实际存储时区一致；测试覆盖北京零点、跨天、连续 7 天。
  3. **权限：新增 `dashboard:view`**：`AdminAuth + RequirePermission("dashboard:view")`，复用 RBAC，超管维持语义，seed 初始化，普通用户及无权限管理员不得访问。
  4. **索引：有条件批准**：允许按需新增，优先 `orders.created_at`；不预先指定 `idx_order_items_product`；结合真实 SQL/EXPLAIN/数据规模给证据；确认需 Migration 后按 Registry 预留版本，不得假定 `20261001000019` 可用。
- 适用范围：本 Task 全部 5 个 Dashboard 接口及其口径、权限、时区、索引决策。

## Global Resource Reservation

- `error_code_domain`：无新增（参数校验复用 `1001`）。
- `migration_version`：**当前不预留**。索引是否新增由实现阶段 EXPLAIN 证据决定；若确认需新增索引，走 Contract Revision，由 Analyst 重新读取 `.agent/registry/migrations.md` 按 `next = max(已记录 version)+1` 派生并预留（不预占 20261001000019）。
- `state.resources.reservations` 保持为空；`dashboard:view` 为 B 类 namespace，不入 Registry。
