# Technical Contract

## Decision Status
APPROVED

## Problem

为 SurgeCart（my-shop）补齐管理员普通订单的**只读管理**能力：后台订单分页列表（多条件组合筛选）、订单详情（含商品明细）、订单状态统计，并以独立 `order:list`/`order:view` 只读权限保护。本任务只新增只读查询，**不改动交易正确性机制**（状态机、库存补偿、事务、发货/退款）。复用现有订单领域模型与 `AdminControllerV1`，仅在 `api/order/v1` 包新增后台 list/detail/stats 请求响应。

## Verified Current Behavior

- VERIFIED：订单模块分层已具备复用地基——`api/order/v1/order.go` 有 `Order`/`OrderItem`、7 态字符串枚举 + DB TINYINT 10~70、`Ship`/`Refund`；`internal/controller/order/order.go` 的 `AdminControllerV1` 已有 `Ship`/`Refund`（`AdminPrincipal` 校验 + 路由层 `RequirePermission`）；`internal/logic/order/order.go` 已有 `findByID`（无归属过滤）、`loadByIDOrder`、`loadItems`、`toOrder`、`statusToString`（int→string，无反函数）。
- VERIFIED：用户端 `List` 无分页（`Order("id DESC")` 全量 Scan）且逐单 `toOrder→loadItems` 加载明细（N+1）；用户端 `Detail` 走 `findOwned`。后台列表必须**另写**，不能复制 N+1。
- VERIFIED：路由 `internal/cmd/routes_admin.go` 仅有 `order:ship`/`order:refund` 两条，无 `/admin/orders`、`/admin/orders/:id`、`/admin/orders/stats`。
- VERIFIED：`AdminAuth` 判定顺序为验签→401、`type≠admin`→403、会话无效→401、`admins` 禁用/不存在→401；`RequirePermission` 为 `IsSuper` 放行、命中放行、未命中 403、DB 查询失败 fail-closed 500。**普通用户 JWT 访问 `/admin/orders*` 返回 403（type≠admin）**。
- VERIFIED：权限 seed 在 `internal/boot/seed.go` 的 `seedPermissionList`，含 `order:ship`/`order:refund`，缺 `order:list`/`order:view`。
- VERIFIED：`orders` 现有索引 `uk_order_no`/`uk_user_idempotency`/`idx_user_id`/`idx_status_expire(status, expire_at)`；`order_items` 有 `idx_order_id`。**无 `created_at` 索引**。
- VERIFIED：统一响应 `{code,message,data}`；订单错误码域 9000-9999 已 ACTIVE（9001~9006 已用），通用 `1001`(400)/`1002`(401)/`1003`(403) 已存在。
- VERIFIED（时间基准）：`created_at`/`expire_at`/`paid_at` 等时间戳由 DDL `CURRENT_TIMESTAMP` 或应用 `NOW()`/`gdb.Raw("NOW()")` 写入，是 **MySQL 会话时区下的 naive DATETIME**（无时区字段）。全仓库无 `SET time_zone`、无 DSN `time_zone`/`loc`/`parseTime` 覆盖；docker-compose 的 MySQL 8.0 无 TZ/`command` 覆盖 → 会话时区 = 全局时区 = UTC（官方镜像默认，INFERENCE）。现有判时（如 `expire_at < NOW()`）均以 MySQL 会话时区为唯一基准。
- VERIFIED：后端无任何 Dashboard/`GROUP BY` 统计聚合模块（Dashboard V1 未实现）；秒杀订单在独立表 `flash_sale_orders`，不在 `orders`，`GROUP BY status` 仅作用于 `orders` 即天然排除秒杀。
- UNKNOWN：各核心查询的真实 EXPLAIN 执行计划（当前无 MySQL 实例运行）；真实数据量级下 `created_at` 范围扫描成本。

## Selected Design

Owner 已于 2026-10-10 确认整体推荐设计，并按以下四项固化：

1. **索引（不新增）**：V1 不新增 `created_at` 索引、不新增 migration。默认查询 `ORDER BY id DESC LIMIT/OFFSET` 走主键倒序（最优）；`status` 走 `idx_status_expire` 最左前缀；`user_id` 走 `idx_user_id`；`order_no` 走 `uk_order_no`；`created_at` 时间范围筛选无索引会全表扫描 + filesort（因 `ORDER BY id DESC` 与 `created_at` 序不同，加索引仍无法消除 id 排序的 filesort，收益仅缩小扫描范围，且当前为低 QPS 后台只读）。Coder 须对核心 list/stats 查询提供真实 SQL 与 EXPLAIN 证据，明确现有索引适用范围与性能风险；后续按实测决定是否优化。

2. **时间范围（RFC3339 + FROM_UNIXTIME，会话时区同基准）**：`start_time`/`end_time` 为 **string 查询参数，RFC3339（必须含时区偏移）**。逻辑用 `time.Parse(time.RFC3339, s)` 解析为绝对时刻（缺偏移/非法 → 400）；校验 `start < end`（比较绝对时刻，`start >= end` → 400）。SQL 用 **`FROM_UNIXTIME(?)`** 把绝对时刻的 Unix 秒转换为 MySQL 会话时区下的 naive DATETIME，与 `created_at`（同会话时区 `NOW()` 写入）同基准比较，实现左闭右开 `[start,end)`：`created_at >= FROM_UNIXTIME(?) AND created_at < FROM_UNIXTIME(?)`。此方案不依赖硬编码具体时区偏移，天然免 Go↔MySQL 漂移。此口径即订单域与未来 Dashboard V1 的统一时间口径（Dashboard V1 当前未实现）。

3. **列表响应（轻量 6 字段）**：`AdminOrderListItem` = `id`/`order_no`/`user_id`/`status`(字符串枚举)/`total_amount`/`created_at`。**不含 `items`、不含 `idempotency_key`/`request_hash`/地址快照，也不返回不完整的状态时间戳**。响应 `Items/Total/Page/Size`。详情 `AdminOrderDetailRes` 复用现有 `Order`，返回全部已存在状态时间戳（`paid_at`/`shipped_at`/`received_at`/`completed_at`/`cancelled_at`/`refunded_at`）及地址快照、`cancel_reason`、`items`。

4. **统计响应（接受）**：`{total, stats:[{status(string), count(int64)}]}`；仅统计普通 `orders`，覆盖完整 7 态、无数据状态返回 0，`total` 等于各状态数量之和。

其余固化约束：

- 三个只读接口：`GET /admin/orders`（`order:list`）、`GET /admin/orders/stats`（`order:list`）、`GET /admin/orders/:id`（`order:view`）；路由挂 `AdminAuth + RequirePermission`，超管放行沿用 `RequirePermission`。
- 分页：`page` 默认 1，`size` 默认 20、上限 100；`page<1`/`size<1`/`size>100`/非整数 → 400(1001)（**拒绝，非钳制**，与 product 的 `normalizePage` 语义不同）。
- 排序：白名单 `{id, created_at}`，默认 `id DESC`；`order` 取 `asc/desc`；SQL 恒为 `ORDER BY <col> <dir>, id <dir>`（id 稳定 tiebreak）；非法 `sort`/`order` → 400(1001)。
- 筛选：`order_no`（精确）、`user_id`（精确）、`status`（字符串枚举 → 新增 `statusFromString` 反映射，非法 → 400）、`start_time`/`end_time`（RFC3339）；参数化 SQL，不引入任意 SQL 排序字段、不按订单号模糊扫描。
- 错误语义：非法参数 → `1001`(400)；详情不存在 → `9001`(404)；无权限 → `1003`(403，中间件)；普通用户 → 403（type≠admin）。三者不混淆。
- RBAC：`seedPermissionList` 追加 `order:list`/`order:view`。
- **本任务不新增错误码域、不新增 migration、不预留任何全局资源。**

## Interfaces and Data

新增到 `api/order/v1/order.go`：

- `AdminListReq`：`GET /admin/orders`；入参 `page`/`size`(int)/`order_no`(string)/`user_id`(int64)/`status`(string)/`start_time`(string)/`end_time`(string)/`sort`(string)/`order`(string)。
- `AdminListRes`：`{items []*AdminOrderListItem, total int, page int, size int}`。
- `AdminOrderListItem`：`{id, order_no, user_id, status, total_amount, created_at}`（`created_at` 为 `*gtime.Time`）。
- `AdminOrderStatsReq`：`GET /admin/orders/stats`。
- `AdminOrderStatsRes`：`{total int64, stats []*AdminOrderStatsItem}`；`AdminOrderStatsItem{status string, count int64}`。
- `AdminOrderDetailReq`：`GET /admin/orders/:id`；`Id int64 in:"path" v:"required"`。
- `AdminOrderDetailRes`：`{Order}`（复用现有 `Order`，含 `items`、地址快照与全部状态时间戳）。

扩展：

- `internal/service/order.go` `IOrder`：新增 `AdminList(ctx, *v1.AdminListReq)`、`AdminStats(ctx)`、`AdminDetail(ctx, id)` 三个只读方法。
- `internal/logic/order/order.go`：新增 `AdminList`/`AdminStats`/`AdminDetail`；新增 `statusFromString(string)(int,error)`（7 态反映射）与时间解析辅助（`parseRFC3339ToUnix`）；复用 `findByID`/`loadItems`/`toOrder`。
- `internal/controller/order/order.go` `AdminControllerV1`：新增 `AdminList`/`AdminStats`/`AdminDetail`（`AdminPrincipal` 校验同 `Ship`/`Refund`）。
- `internal/boot/seed.go`：`seedPermissionList` 追加 `order:list`/`order:view`。
- `internal/cmd/routes_admin.go`：新增 3 条路由；`internal/cmd/routes_test.go` 补 `GET /admin/orders`、`GET /admin/orders/stats`、`GET /admin/orders/:id` must-exist 断言。

**实现注意**：`/admin/orders/stats` 与 `/admin/orders/:id` 存在路由歧义风险；GoFrame 静态段优先于命名参数，但必须在 `routes_test.go` 同时锁定两条路由正确解析（`stats` 不被 `:id` 吞掉；`:id` 绑定 int64 时 `stats` 会被拒）。

## Business Invariants

- INV-001（只读无副作用）：三个接口不产生任何 `INSERT/UPDATE/DELETE`（对 `orders`/`order_items`/`inventories` 均无写）。
- INV-002（授权隔离）：无 `order:list`/`order:view` 权限的非超管访问对应接口返回 403 且无 DB 副作用；普通用户 token（`type=user`）访问 `/admin/orders*` 返回 403。
- INV-003（快照自足）：详情中的成交价与商品/SKU 名/单价/数量来自 `orders`/`order_items` 历史快照，不重读 `skus.price`/商品当前售价。
- INV-004（统计口径）：stats 仅统计 `orders` 全部普通订单，覆盖 7 态（无数据态为 0），不混入 `flash_sale_orders`；`total` = 各状态数量之和。
- INV-005（稳定排序）：`ORDER BY <col> <dir>, id <dir>` 保证相同排序键翻页不重复/遗漏。
- INV-006（分页有界）：任意合法参数组合下 `LIMIT` 上界为 100，不产生无界全量返回。
- INV-007（时区同基准）：`start_time`/`end_time` 绝对时刻经 `FROM_UNIXTIME(Unix秒)` 在 MySQL 会话时区内转换，与 `created_at`（同会话时区 `NOW()` 写入）同基准；带不同时区偏移但指向同一时刻的入参，筛选结果一致。
- INV-008（交易机制无回归）：状态机、库存补偿、事务、发货/退款逻辑不被本任务改动。

## Failure and Consistency Semantics

- 本任务为纯只读、单数据源（MySQL），无 Redis/MQ/跨系统一致性；无部分完成或中间态。
- 成功语义：list/stats/detail 返回真实 `orders`/`order_items` 数据；detail 不存在返回 9001(404)。
- 失败语义：非法参数（状态/时间/分页/排序）→ 1001(400)；无权限 → 1003(403)；未认证/type 不符 → 1002/1003；DB 技术错误 → 1000(500)（经 `codes.Wrap` 保留错误链，不泄漏 SQL/路径/敏感信息）。
- 只读接口天然可重试，无幂等/超时语义。

## Allowed / Forbidden Changes

- 允许：新增上述 3 个只读接口、`order:list`/`order:view` 权限与路由、`AdminOrder*` API 结构、`statusFromString` 反映射、RFC3339→Unix 解析与 `FROM_UNIXTIME` 筛选、`routes_test.go` 新路由断言、更新 `docs/design/order.md` 与 `docs/design/rbac.md`。
- 禁止：修改状态机/库存补偿/事务/发货/退款逻辑；修改既有用户端 `List`/`Detail`/`Pay`/`Cancel`/`Receive` 与 `order:ship`/`order:refund` 权限；修改既有公开接口出参/错误语义；引入 Redis 缓存/Elasticsearch/汇总表；新增 `created_at` 索引或 migration；复制用户端 N+1 到后台列表。

## Verification Requirements

- INV-001 → 需 MySQL：管理员调用三接口后，断言 `orders`/`order_items`/`inventories` 行数不变。
- INV-002 → 需 MySQL/Redis：普通用户 token、无权限管理员、超管分别访问列表/统计/详情，断言 403/403/放行且零写入。
- INV-003 → 需 MySQL：修改 `skus.price` 后查详情，断言成交价/单价仍为历史快照。
- INV-004 → 需 MySQL：预置覆盖 7 态订单 + 秒杀订单，断言 stats 各态计数正确、无数据态为 0、`total`=各态之和、不混入秒杀。
- INV-005 → 需 MySQL：预置同 `created_at` 多单，断言 `id` 默认倒序与 `created_at` 排序稳定、翻页无重复/遗漏。
- INV-006 → 需 MySQL：`size>100`/`page<1`/非整数分页被拒（400/1001），正常分页 `LIMIT` ≤ 100。
- INV-007 → 需 MySQL：用带 `+08:00` 偏移的 RFC3339 与等价 UTC（`Z`）时刻分别查询，断言筛选结果一致（FROM_UNIXTIME 同基准）。
- AC-004/AC-005 → 需 MySQL：各筛选独立及组合结果正确、`[start,end)` 左闭右开；非法状态/`start>=end`/非法时间格式（缺偏移）/非法排序被拒（400）。
- AC-008 → 需 MySQL：查询不存在 id 断言 9001(404)。
- AC-013/AC-014 → 需 MySQL：DB 层开启查询日志或统计 SQL 调用次数，证明列表不逐单加载明细；对核心 list/stats 查询记录真实 SQL 与 `EXPLAIN` 证据（默认 id-DESC 走主键、status 走 `idx_status_expire`、order_no 走 `uk_order_no`、user_id 走 `idx_user_id`；时间范围全扫注明为已知取舍与性能风险）。
- AC-015/016/017 → 需 MySQL：运行现有订单相关测试，确认用户端查询/支付/取消/收货、发货/退款、状态机/库存/事务无回归。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Open Risks

- 时间范围筛选（`created_at`）在订单量很大时会全表扫描 + filesort；当前阶段可接受，优化触发信号为「后台时间筛选出现可感知延迟或 EXPLAIN 显示全扫行数超阈值」。
- `/admin/orders/stats` 与 `/admin/orders/:id` 路由歧义须以测试锁定（见 Interfaces and Data 实现注意）。
- MySQL 会话时区未显式固定（依赖官方镜像默认 UTC）；`FROM_UNIXTIME` 方案在读写同会话时区的前提下天然正确，但若未来有人改动 DB 全局时区需保证读写一致，必要时再显式固定 DSN `time_zone`。

## Owner Decision Record

- 2026-10-10，Owner 对 `admin-order-management` 的 Contract 决策：**接受整体推荐设计**。具体：
  1. 索引：接受暂不新增 `created_at` 索引/migration，要求核心 SQL 提供 EXPLAIN 证据并明确性能风险，后续按实测决定优化。
  2. 时间范围：修改为 RFC3339（含时区偏移）+ 左闭右开 `[start,end)`，要求明确转换规则、避免与数据库时间基准不一致、与 Dashboard V1 口径一致；Analyst 推荐 `FROM_UNIXTIME(Unix秒)` 会话时区同基准方案（见 Selected Design §2）。
  3. 列表字段：`AdminOrderListItem` 保持轻量（订单编号/用户/状态/金额/创建时间等），不要求返回不完整的状态时间戳；详情返回全部已存在状态时间戳。
  4. 统计响应：接受 `{total, stats:[{status,count}]}`（7 态、无数据为 0、`total`=各态之和）。
  5. 其余：接受三个只读接口、`order:list`/`order:view` RBAC、有界分页/非法参数拒绝/稳定排序、列表禁止 N+1、不改交易逻辑、不新增错误码域。
- 适用范围：本任务 `admin-order-management` 的 Contract 与后续 Design 更新；`Design Impact = UPDATE`（`docs/design/order.md` + `docs/design/rbac.md`）。
