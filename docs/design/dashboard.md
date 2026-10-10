# 运营数据大屏设计（Dashboard）

本文面向项目接手者，说明后台运营数据大屏（Dashboard V1）的统计口径、数据来源、API 契约、权限与时区边界、一致性语义与业务不变量。事实来源为 `dashboard-v1` 最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

Dashboard 回答「平台整体运营概况是什么」：为用户、商品、普通订单、秒杀业务提供只读聚合统计，供 Vue + ECharts 前端消费。本模块只做**只读 MySQL 聚合统计**，不写入、不缓存、不预聚合、不推送。

边界：不改动前端（`frotend_web`/`frotend_manage`）；不改订单/秒杀/商品/IAM 生产逻辑与状态机；不引入 Redis 统计缓存、预聚合表、定时统计任务、WebSocket/SSE、导出；系统性能指标（p95/p99、CPU、QPS）继续由 Prometheus 负责，本模块只做业务运营统计。

## 2. 统计口径（核心）

口径是 Dashboard 的最高优先级正确性约束。核心原则：**金额、订单、销量口径互不混淆**。

### 2.1 订单边界

- 普通订单指标（订单总数、今日新增、每日趋势、状态分布、时间范围总数、今日订单金额、商品销量）仅统计普通 `orders`。
- 秒杀订单 `flash_sale_orders` 仅在「秒杀运营统计」中单独呈现，不与普通订单混合。秒杀为独立闭环（下单即成交、无状态机/支付/取消），混合会破坏口径一致性。

### 2.2 数量类口径（订单数）

订单总数、今日新增订单数、每日趋势、状态分布、时间范围总数，均按 `orders.created_at` + **全部订单状态**统计（即「下单量」，不按状态预过滤）。状态分布显式返回全部 7 个状态（10/20/30/40/50/60/70，含计数 0），让「有效 vs 无效」一眼可见。

### 2.3 金额口径（今日订单金额）

- 指标：`today_order_amount`（今日订单金额）。
- 口径：`SUM(orders.total_amount)`，仅统计 `created_at ∈ 今日` 且 `status ∈ {20,30,40,50}`（已支付/已发货/已收货/已完成）。
- 排除：待支付(10)、已取消(60)、已退款(70)。
- 语义：**创建于今日、且「当前」处于有效状态的订单金额**，**不等于今日支付金额**（支付为 Mock，`paid_at` 无真实到账）。
- 命名约束：不得称为「实收销售额」，文案标注「已支付口径」。

### 2.4 销量口径（商品销量 TOP 10）

- 指标：`sales = SUM(order_items.quantity)`，按 `order_items.product_id` 聚合，关联 `orders` 过滤 `status ∈ {20,30,40,50}`。
- 与金额口径共用同一套「有效订单状态」过滤规则（`{20,30,40,50}`），保证可对账、不混淆。
- 销量只来自真实 `order_items`，**不使用 `view_count` 或库存变化**。

### 2.5 当前快照语义（重要）

金额与销量按「**当前**订单状态」过滤，而非「创建当日状态」。因此订单后续状态变化会改变历史日期的统计值：例如某订单今日创建并支付（20），明日退款（70），则该订单从「今日订单金额/销量」中移除。这是**时点快照口径**，不是事件时间口径，实现、文档与测试必须一致固化。

### 2.6 商品总数

`COUNT(products)`：全部 SPU（含 draft/on_shelf/off_shelf）。商品无删除接口、无 deleted 标记，三种状态均为真实存在的商品。

### 2.7 秒杀统计

- 全局聚合，不按活动过滤。
- 活动总数 = `COUNT(flash_sale_activities)`；活动状态分布按 `flash_sale_activities.status` 字段（0=disabled/1=enabled），**不按时间窗派生「已结束」**（「已结束」由 `end_time` 派生，无冗余终态）。
- 请求状态分布按 `flash_sale_order_requests.status`（0=queued/1=success/2=failed/3=dead）全局聚合。
- 当前 queued 数量 = `COUNT(flash_sale_order_requests WHERE status=0)`，不叠加时间窗（`status=queued` 本身即「尚未终态、正在排队」）。
- 成功订单数量 = `COUNT(flash_sale_orders)`（唯一事实来源）；`queued` 请求**不计入**成功订单。

## 3. 时区与时间边界

- 配置 `dashboard.timezone`（默认 `Asia/Shanghai`），语义为「Dashboard 日期切分时区」= 系统权威业务时区。
- 连接层使 MySQL 会话时区与该时区一致（`Asia/Shanghai` → `+08:00`），使 `NOW()/CURDATE()/CURRENT_TIMESTAMP` 与 Go `time.LoadLocation(dashboard.timezone)` 一致，消除 Go↔MySQL 漂移。
- 仅支持无夏令时（DST）的时区：连接层用固定偏移表达会话时区，无法表达 DST 切换。启动时对含 DST 的时区 fail-fast 拒绝（`America/New_York` 等），避免跨切换漂移。
- 时间边界用 DATETIME 字符串范围比较（左闭右开 `[start, end)`），不用 `DATE()`/`FROM_UNIXTIME()` 等函数包裹列（避免索引失效）。
  - 「今日」= `[今日 00:00:00, 明日 00:00:00)`。
  - 「最近 7 天」= 7 个自然日桶，整体 `[6 天前 00:00:00, 明日 00:00:00)`。
  - 「时间范围」= 入参 RFC3339 转该时区后 `[start, end)`。
- 存储约定：全库 DATETIME 由 MySQL 时钟（`NOW()`/`CURRENT_TIMESTAMP`）写入，Go 读取为 wall-clock 字符串、无时区换算（DSN 无 `loc`/`parseTime`/`time_zone`）。因此统计边界必须与实际存储时区一致。

## 4. API 契约

全部接口挂 `AdminAuth + RequirePermission("dashboard:view")`，统一响应 `{code,message,data}`，JSON 字段 snake_case。

| 方法 | 路径 | 参数 | 说明 |
| --- | --- | --- | --- |
| GET | `/admin/dashboard/overview` | 无 | 运营概览 |
| GET | `/admin/dashboard/orders/trend` | 无 | 最近 7 天订单趋势 |
| GET | `/admin/dashboard/orders/status` | `start_time`/`end_time`（可选，RFC3339，同传且 start<end） | 订单状态分布 + 时间范围总数 |
| GET | `/admin/dashboard/products/top` | `limit`（可选，正整数，默认 10，上限 50；非整数/越界返回 400） | 商品销量 TOP 10 |
| GET | `/admin/dashboard/flash-sales` | 无 | 秒杀运营统计 |

响应结构：

1. overview → `{ user_total, today_new_users, product_total, order_total, today_new_orders, today_order_amount }`
2. trend → `{ days: [{ date, order_count }] }`（7 项连续）
3. status → `{ total, status_distribution: [{ status, count }] }`（7 状态显式）
4. top → `{ items: [{ rank, product_id, product_name, sales }] }`
5. flash-sales → `{ activity_total, activity_status: { enabled, disabled }, request_status: { queued, success, failed, dead }, current_queued, success_orders }`

参数校验复用 `1001`（400）；未授权/越权 `1002`（401）/`1003`（403）；DB 技术错误 `1000`（500）。

## 5. 权限边界

- `dashboard:view` 为 B 类 namespace 权限 code，seed 于 `internal/boot/seed.go`。
- 超级管理员（`IsSuper`）经 `RequirePermission` 直接放行；普通用户（`type=user`）被 `AdminAuth` 拒绝（403）；无 `dashboard:view` 的管理员被 `RequirePermission` 拒绝（403）。
- 身份信任：管理员身份取自 `AdminPrincipal`，不信任请求自带身份。

## 6. 数据来源

| 指标 | 数据来源（事实来源单一 MySQL） |
| --- | --- |
| 用户总数 / 今日新增用户 | `users`（`created_at` 切「今日」） |
| 商品总数 | `products`（全量） |
| 订单总数 / 今日新增 / 趋势 / 分布 / 时间范围总数 | `orders`（`created_at` + 全部状态） |
| 今日订单金额 | `orders`（`created_at ∈ 今日` + `status ∈ {20,30,40,50}`，`SUM(total_amount)`） |
| 商品销量 TOP 10 | `order_items` JOIN `orders`（`status ∈ {20,30,40,50}`）GROUP BY `product_id` |
| 秒杀活动总数 / 状态分布 | `flash_sale_activities` |
| 秒杀请求状态分布 / queued 数 | `flash_sale_order_requests` |
| 秒杀成功订单数 | `flash_sale_orders` |

## 7. 一致性语义与失败语义

- 事实来源：单一 MySQL。Dashboard 纯只读聚合，无跨系统事务、无并发写、无缓存。
- 成功响应代表「该时刻的 MySQL 聚合快照」，不代表任何写入；无重试/幂等/部分完成语义（只读、无副作用）。
- 多接口之间是「不同时刻的独立快照」，不承诺跨接口强一致；单接口内指标各自聚合、可逐项对账。
- 失败语义：参数非法 → `1001`；未授权/越权 → `1002`/`1003`；DB 技术错误 → `1000`（500）；全部失败路径无写入。

## 8. 业务不变量

- INV-001（身份与权限）：仅 `type=admin` 且持 `dashboard:view`（或超管）可访问；普通用户/无权限管理员/无 token 分别 403/403/401，拒绝路径零写入。
- INV-002（金额口径不可混淆）：`today_order_amount` 只统计 `orders` 且 `created_at ∈ 今日` 且 `status ∈ {20,30,40,50}`；命名/文案不含「实收销售额」。
- INV-003（销量口径不可混淆）：销量只统计 `order_items` 关联的 `orders.status ∈ {20,30,40,50}`，不用 `view_count`/库存变化。
- INV-004（订单边界）：普通订单指标只统计 `orders`，秒杀成功订单只统计 `flash_sale_orders`，不混合。
- INV-005（趋势连续不重不漏）：7 天连续、空日 0、逐日求和 = 7 天总数；左闭右开边界不重不漏。
- INV-006（时区一致）：边界按 `dashboard.timezone`（默认 Asia/Shanghai）计算且与 MySQL 会话时区一致。

## 9. 索引与性能

- 现状索引：`orders` 无 `created_at` 索引、无 `status` 单列索引（有 `idx_status_expire(status, expire_at)`）；`order_items` 无 `product_id` 索引（有 `idx_order_id`）。
- 实现策略：先按现有索引实现聚合查询，用真实 SQL + `EXPLAIN` + 数据规模评估；若 `orders.created_at`（优先）等谓词造成无界全表扫描，则经 Contract Revision 新增必要索引（migration_version 按 Registry 规则预留，不预占具体版本）。
- 约束：单条 SQL 聚合 + 排序 + LIMIT 完成 TOP 10，无 N+1、不逐条读取订单到 Go 内存。

## 10. 错误码

复用现有错误码，不新增域：`1001`（参数 400）、`1002`（401）、`1003`（403）、`1000`（500）。

## 11. 已知留白

- 时区统一为北京时间后，若历史 DATETIME 由非北京时间写入，「今日」边界对历史数据偏移 8h；dev 每日重置环境可接受，生产需一次性校正。
- `dashboard.timezone` 仅支持无 DST 时区（默认 `Asia/Shanghai` 即满足）；含 DST 的时区在启动时被拒绝，后续如需支持需改为按时刻动态对齐连接层与 Go 边界。
- 会话时区统一会影响订单 `expire_at`/秒杀时间窗/各 `*_at` 的墙钟参考（现有代码用 `NOW()` 内部自洽），需回归订单超时取消与秒杀时间窗测试。
- 若最终不新增 `idx_order_items_product`，TOP 10 聚合为 `order_items` 全表扫描（单查询非 N+1，V1 可接受）。
