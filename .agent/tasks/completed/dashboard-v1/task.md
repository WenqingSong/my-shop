# Task: Dashboard V1 — 电商运营数据大屏后端

## Goal

为 SurgeCart 后台新增管理员运营数据大屏后端 API，通过 MySQL 业务数据提供运营概览、订单趋势、订单状态分布、商品销量排行与秒杀业务统计，供后续 Vue + ECharts 前端直接消费。本阶段只交付 Go 后端接口，不实现前端页面；所有指标均可追溯到真实业务数据，金额、订单、销量口径互不混淆，接入现有 Admin JWT + RBAC 权限边界。

## Scope

- 运营概览 API：用户总数、今日新增用户数、商品总数、订单总数、今日新增订单数；今日订单金额仅在现有数据模型支持可靠计算时提供，且不得命名为「实收销售额」。
- 订单分析 API：最近 7 天每日订单数量趋势（无数据日期返回 0，趋势连续）、订单状态分布、指定时间范围内订单总数。
- 商品销量分析 API：商品销量 TOP 10，返回商品 ID、名称、销量、排名，基于订单与订单明细聚合。
- 秒杀运营统计 API：秒杀活动总数、活动状态分布、秒杀请求状态分布、当前 `queued` 请求数量、秒杀成功订单数量。
- 上述接口接入现有 Admin JWT（`AdminAuth`）+ RBAC，禁止普通用户访问；复用现有统一响应结构（`{code,message,data}`）与错误码机制；时间范围、排行榜数量等参数必须校验并设合理上限。
- 使用 MySQL 聚合查询完成统计（不逐条读取订单到 Go 内存计算），优先利用现有索引，避免 N+1 与无界数据扫描；不引入 Redis 统计缓存或预聚合表。
- 核心统计口径编写自动化测试：覆盖空数据、日期边界、不同订单状态、非法参数、管理员权限与统计结果正确性。
- 提供各 API 的请求与响应示例（供后续前端接入）。
- 长期设计：新增 `docs/design/dashboard.md`（Design Impact = NEW），沉淀统计口径、数据来源与 API 契约。

## Out of Scope

- 前端 Vue / ECharts 页面实现（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程，本任务不触碰）。
- Grafana / Prometheus 改造（系统性能指标 p95/p99、CPU、QPS 继续由 Prometheus 负责，本任务只做业务运营统计）。
- WebSocket / SSE 实时推送。
- Redis 统计缓存、定时统计任务、物化汇总表。
- 数据导出（CSV/Excel）。
- 新增订单、支付、退款等交易业务；修改秒杀核心交易逻辑（状态机、Redis Lua Gate、消费者）。
- 不必要的数据库迁移（除非 Analyst 评估确认必须新增统计索引）。

## Milestone

Milestone: Dashboard V1 运营统计后端

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/dashboard.md

## Acceptance Criteria

- [ ] AC-001（身份与权限边界）：管理员携带有效 `type=admin` token 可访问全部 Dashboard 接口并返回 `code=0` 与完整 data；普通用户 token（`type=user`）访问返回 403，无/非法/失效 token 返回 401；任何拒绝路径均不产生数据库写入。
- [ ] AC-002（运营概览指标）：`/admin/dashboard/overview` 返回用户总数、今日新增用户数、商品总数、订单总数、今日新增订单数；各指标均可经 MySQL 聚合查询对账（值与真实表计数一致）。
- [ ] AC-003（今日订单金额口径）：概览中的「今日订单金额」（若提供）基于真实 `orders.total_amount` 聚合，指标命名与文案不得表述为「实收销售额」；其统计口径（纳入/排除哪些订单状态）与实现一致且可追溯。
- [ ] AC-004（订单趋势）：`/admin/dashboard/orders/trend` 返回最近 7 天每日订单数量，时间窗连续、无数据的日期返回 0；日期边界不产生重复或遗漏统计。
- [ ] AC-005（订单状态分布）：`/admin/dashboard/orders/status` 返回订单状态分布，使用现有订单状态枚举（10/20/30/40/50/60/70），并明确已取消、未支付等状态是否纳入及如何呈现。
- [ ] AC-006（指定时间范围订单总数）：`/admin/dashboard/orders/status`（或等价接口）支持指定时间范围返回该范围内订单总数，使用统一时间范围规则（左闭右开等），与趋势/分布口径一致、不重复统计。
- [ ] AC-007（商品销量排行）：`/admin/dashboard/products/top` 返回销量 TOP 10，含商品 ID、名称、销量、排名；使用 SQL 聚合 + 排序 + LIMIT 完成，无 N+1 查询；销量只来自真实订单明细，不把浏览量（`view_count`）或库存减少量当作销量。
- [ ] AC-008（销量口径明确）：商品销量按明确的「有效订单状态」口径统计（排除已取消、已退款等无效订单），口径在实现、文档与测试中一致；若现有数据无法支撑可靠销量统计，须明确说明缺失条件，不得伪造数据。
- [ ] AC-009（秒杀运营统计）：`/admin/dashboard/flash-sales` 返回秒杀活动总数、活动状态分布（enabled/disabled）、秒杀请求状态分布（queued/success/failed/dead）、当前 `queued` 请求数量、秒杀成功订单数量；`queued` 请求不计入成功订单，成功订单以 `flash_sale_orders` 行为唯一事实来源。
- [ ] AC-010（参数校验与上限）：时间范围、排行榜数量（默认/上限）等参数被服务端校验，非法参数返回 400 稳定错误码，排行榜数量不超过合理上限。
- [ ] AC-011（自动化测试）：核心统计口径有自动化测试，覆盖空数据、日期边界、不同订单状态、非法参数、管理员/普通用户权限、统计结果正确性；关键口径至少有一个能区分正确与错误实现的测试。
- [ ] AC-012（无回归）：不改变现有订单、秒杀、商品、IAM 等模块行为；`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...` 通过。
- [ ] AC-013（长期设计落地）：新增 `docs/design/dashboard.md`，沉淀各指标口径、数据来源、API 契约与错误语义，与 APPROVED Contract、最终实现一致。
- [ ] AC-014（接口示例）：提供每个 Dashboard API 的请求与响应示例，方便后续 Vue + ECharts 前端直接接入。

## Relevant Context

已核实事实：

- 身份与权限：后台为独立身份域 `admins` + `type=admin` JWT + `iam:admin:session:{sid}`；路由 `internal/cmd/routes_admin.go` 中后台只读接口（如 `GET /admin/products`、`GET /admin/banners`、`GET /admin/inventories/:sku_id`）仅挂 `AdminAuth`（无细粒度权限 code），写接口叠加 `RequirePermission(code)`。`AdminAuth` 拒绝 `type=user`（403）、无/失效 token（401）。
- 响应与错误码：统一响应 `{code,message,data}`（`internal/middleware/response.go`）；错误码集中在 `internal/codes/codes.go`，通用域已有 `1001`（参数 400）/`1002`（401）/`1003`（403）/`1004`（404）。
- 前台用户表 `users`：`id`/`username`/`password_hash`/`created_at`/`updated_at`，`created_at` 可用于「今日新增用户数」。
- 商品表 `products`：`id`/`name`/`brand`/`category_id`/`price`/`status`(0=draft,1=on_shelf,2=off_shelf)/`view_count`/`created_at`；无删除接口。
- 订单表 `orders`：`status` 状态机 10 待支付 / 20 已支付 / 30 已发货 / 40 已收货 / 50 已完成 / 60 已取消 / 70 已退款；`total_amount` 为成交总价整数分快照；`paid_at`/`cancelled_at`/`refunded_at`/`created_at` 等时间字段齐全；索引含 `idx_user_id`、`idx_status_expire(status, expire_at)`。订单项 `order_items`：`order_id`(FK CASCADE)/`product_id`/`product_name`/`price`/`quantity`，索引 `idx_order_id`。
- 支付现状：订单支付为 Mock（无真实支付平台），`paid_at` 仅记录 Mock 支付时间，不存在真实资金到账，因此「订单金额」不能等同于「实收销售额」。
- 秒杀表：`flash_sale_activities`（`status` 0=disabled/1=enabled，`start_time`/`end_time`）、`flash_sale_orders`（秒杀成功订单，行存在即成功，无状态机）、`flash_sale_order_requests`（`status` 0=queued/1=success/2=failed/3=dead，`idx_dequeue(status,next_attempt_at,id)`）。成功订单事实来源是 `flash_sale_orders` 行，`queued` 请求不代表成功订单。
- 无现有 Dashboard/统计后端实现（仅前端模板工程的 mock 页面，与本后端无关）；`manifest/config/config.yaml` 无时区配置，DB 时间列为 `DATETIME`（`CURRENT_TIMESTAMP`）。
- 全局资源：本任务预期不新增数据表，参数校验复用 `1001`；若 Analyst 评估需新增统计索引/迁移，则需按流程预留 `migration_version`。

Assumption：

- 「用户总数」「今日新增用户数」指前台用户表 `users`，不含后台管理员表 `admins`。
- Dashboard 只读接口沿用现有后台只读接口约定（仅 `AdminAuth`，不新增细粒度权限 code）；若 Owner 希望增加独立的 `dashboard:view` 权限，由 Analyst 评估并在 Contract 中界定（权限 code 属 B 类 namespace，不占全局资源）。
- 接口路径最终遵循仓库现有规范；Owner 建议的 `GET /admin/dashboard/*` 前缀作为默认方案，具体由 Analyst 在 Contract 中固化。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 「订单」边界：Overview 与订单分析中的「订单」是否仅指普通 `orders`（秒杀订单在「秒杀运营统计」单独呈现），还是需要合并口径。两者会导致「订单总数」等数字不同。
- 「今日订单金额」口径：支付为 Mock，金额按哪些订单状态计入（是否排除待支付 10 / 已取消 60 / 已退款 70），指标如何命名以避免与「实收」混淆。
- 「有效销量」口径：商品销量排行按哪些订单状态计入有效销量（是否排除待支付/已取消/已退款）。
- 「今日」与趋势日期的时区边界：`DATETIME` 无时区，按固定 `Asia/Shanghai`、服务器本地时区，还是新增时区配置切分。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis（`docker compose up -d`）；权限类 AC 需真实管理员与普通用户双身份验证；统计正确性需真实 MySQL 数据核对（不能只看 HTTP 200）。

- AC-001 → 分别以管理员 token、普通用户 token、无/失效 token 请求各 Dashboard 接口，断言 200/403/401，且拒绝路径下数据库计数不变化。
- AC-002 → 对 `users`、`products`、`orders` 直接执行 `COUNT`/`COUNT WHERE created_at>=今日零点` 聚合，与 overview 返回值逐项一致。
- AC-003 → 核对金额值与 `SUM(total_amount)`（按固化口径过滤状态）一致；断言字段名/文案不含「实收销售额」等表述。
- AC-004 → 构造「某天有订单、某天无订单」数据，断言 7 天序列连续、空日期为 0、总数与逐日求和一致。
- AC-005 → 构造覆盖各订单状态的样本，断言分布计数与 `GROUP BY status` 一致、状态枚举正确。
- AC-006 → 以「起止时间含边界订单」验证左闭右开等时间范围规则，断言总数与趋势/分布不重复。
- AC-007 → 构造多商品多订单明细样本，断言 TOP 10 顺序、销量、排名与 SQL 聚合一致；代码审查确认无 N+1（单次聚合查询或一次 JOIN 完成）。
- AC-008 → 构造含已取消/已退款订单的样本，断言其不计入销量；文档、实现、测试口径一致。
- AC-009 → 对 `flash_sale_activities`、`flash_sale_order_requests`、`flash_sale_orders` 分别聚合，断言各计数一致且 `queued` 未计入成功订单。
- AC-010 → 传入非法时间范围/超上限 top N，断言 400 与稳定错误码。
- AC-011 → `go test -p 1 ./...` 覆盖关键口径；对「有效销量」「有效金额」等口径至少一个测试能在实现口径错误时失败。
- AC-012 → `gofmt`、`go build ./...`、`go vet ./...`、`go test ./...` 通过；确认无订单/秒杀/商品/IAM 行为改动。
- AC-013 → `docs/design/dashboard.md` 与 APPROVED Contract、最终实现一致。
- AC-014 → 检查任务产物中含各 API 请求/响应示例（可位于 `docs/design/dashboard.md` 或 API 定义注释）。

## Complexity

COMPLEX

原因：本任务的关键难点不是编码，而是**统计口径选择**——金额、销量、订单边界、时区切分存在多个现实方案，会产生不同业务数字且直接影响 Owner 关心的「金额、订单、销量不混淆」；同时新增 5 个后台公开 API 契约（长期公开协议）。这些口径与协议选择需 Analyst 调查现有数据模型、比较方案并交 Owner Contract Decision，不能由 Coder 自行拍板。全局资源：预期不新增迁移/错误码域，除非 Analyst 评估需新增统计索引。

## Analyst Questions

1. 统计口径（关键，需 Owner 决策）：
   - 「订单」边界：Overview/订单分析中的「订单」是否仅含普通 `orders`，秒杀订单仅在秒杀模块单独统计？
   - 金额口径：是否提供「今日订单金额」/时间范围金额；以哪些订单状态为有效（是否排除待支付 10 / 已取消 60 / 已退款 70）；指标命名如何规避「实收」歧义。
   - 销量口径：商品销量排行以哪些订单状态为「有效销量」；是否与金额口径共用同一套状态过滤规则。
2. 时区边界：`DATETIME` 无时区，「今日」与趋势日期按什么时区切分（固定 `Asia/Shanghai` vs 服务器本地时区 vs 新增配置），趋势 7 天的日期锚点与边界规则。
3. 权限模型：Dashboard 只读接口沿用 `AdminAuth`（无细粒度权限 code，与现有后台只读接口一致），还是新增 `dashboard:view` 权限（若新增需在 `internal/boot/seed.go` seed，属 RBAC 扩展）。
4. 商品总数口径：全部 `products` 还是仅 `on_shelf`。
5. 秒杀统计展示维度：请求状态分布 / `queued` 计数按全局聚合还是按活动过滤；`queued` 是否需叠加时间窗过滤（避免历史残留 `queued` 计入「当前排队」）。
6. 索引与性能：现有索引是否足以支撑聚合（`orders.created_at`、`orders.status`、`order_items.product_id` 等）；是否需新增统计索引（若新增则产生 migration，需按流程预留 `migration_version`）。

## Review Baseline

- Base commit：`f0bf42cbf8592215a76173ef75d2eac5078960e2`（分支 `feat/backend-dashboard`，HEAD）。
- 任务开始时已有修改：无（`git status --short` 为空，working tree clean）。
- 重叠修改的区分方式：本任务产物集中在新增 `api/dashboard`（或 `api/admin/v1` 扩展）、`internal/controller/dashboard`、`internal/logic/dashboard`、`internal/service/dashboard.go`、`internal/cmd/routes_admin.go`（挂载新路由）、对应测试，以及 `docs/design/dashboard.md`（由 Analyst 新增）；不改现有 `orders`/`flashsale`/`product`/`iam` 生产逻辑。工作区干净，无既有未提交修改需区分。

## Initial Route

交 Analyst（COMPLEX）
