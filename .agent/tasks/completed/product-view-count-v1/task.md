# Task: 商品浏览量计数（Product View Count）V1

## Goal

交付「商品浏览量计数」V1：系统能累计并展示商品的浏览量。用户浏览商品详情时触发计数，商品详情（和/或列表）返回累计浏览量；并发浏览下计数不丢失。具体计数语义（总量/去重）、计数触发点、数据模型与展示位置由 Analyst 固化、Owner 确认。

## Scope

- 浏览量数据模型（`products` 增加计数字段，或独立浏览记录表，具体由 Analyst 固化），经既有 golang-migrate 机制新增迁移文件，并同步更新 `internal/migrations/migrations_test.go`（`latestMigrationVersion`/`businessTables`/`expectedSchema`）。
- 浏览量计数写入：在商品详情被浏览时触发计数（前台公开详情 `GET /products/:id` 为主；后台/admin 详情、列表是否计数由 Analyst/Owner 固化）。
- 浏览量读取/展示：商品详情响应（和/或列表）返回累计浏览量字段。
- 并发正确性：并发浏览下计数不丢失（原子自增或等价机制）。
- 错误码：预计复用既有商品域（4001）与通用域（1001/1000）；是否新增错误码域由 Analyst 判断。
- 长期设计：更新 `docs/design/product.md`（Design Impact = UPDATE；若分析结论为独立表/新实体，则校正为 NEW 并新增对应设计文档）。
- 必要测试：计数自增、并发计数不丢失、详情/列表返回浏览量、商品不存在等边界。

## Out of Scope

- SKU 粒度浏览量、按天/周期/维度的浏览明细统计、浏览量排行榜/排序。
- 去重浏览（同用户/会话/IP 去重），除非 Owner 明确要求（V1 默认总浏览量，具体由 Analyst/Owner 确认）。
- Redis 缓存计数、MQ 异步刷库、限流、防刷、防机器人。
- 后台浏览统计报表/图表。
- 修改既有商品/IAM 等模块行为（除计数所需的最小改动）。
- 前端页面改造（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。

## Milestone

Milestone: 商品浏览量计数（Product View Count）V1 核心闭环

## Design Impact

Design Impact: UPDATE
Design Artifact: docs/design/product.md

注：本判定基于「在 `products` 增加浏览量计数字段」的最小实现。若 Analyst 分析结论为独立浏览记录表/新实体，则 Design Impact 校正为 NEW 并新增对应设计文档；校正改变 Scope 时回 Owner/Task Builder。

## Acceptance Criteria

- [ ] AC-001（计数触发）：给定一个已上架商品，当用户访问前台商品详情时，系统累计该商品的浏览量（+1），且详情响应语义不变（正常返回商品详情）。
- [ ] AC-002（并发计数不丢失）：并发访问同一商品详情时，浏览量最终累计值准确、无丢失计数。
- [ ] AC-003（详情展示）：商品详情响应包含累计浏览量字段，且为最新累计值。
- [ ] AC-004（展示位置）：浏览量字段的展示位置（详情、列表、或独立统计接口）符合 Owner 确认语义。
- [ ] AC-005（边界）：访问不存在/非上架商品详情时，按既有语义返回 404，且不产生计数写入。
- [ ] AC-006（数据模型与迁移）：浏览量计数字段/表经迁移正确建立，`migrations_test.go` 同步更新，迁移可重复/幂等。
- [ ] AC-007（长期设计）：更新 `docs/design/product.md`，沉淀浏览量数据模型、计数语义、并发正确性边界与展示协议，与 APPROVED Contract、最终实现一致。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。
- 商品前台公开详情 `GET /products/:id`（`api/product/v1/product.go` 的 `DetailReq`/`DetailRes`），链路 `controller.product.Detail → service.Product().Detail → logic.product.Detail → s.load(ctx, id, onlyOnShelf=true)`，公开、无 token。
- `products` 表现有字段 `id/name/brand/category_id/price/main_image/detail/status/created_at/updated_at`，无浏览量相关字段；对外 `Product` 结构（`api/product/v1/product.go`）无 `view_count`。
- 事实来源为单一 MySQL；Redis 仅会话；无 MQ、无异步、无 cron。计数若走同步写会落在热读路径（详情），存在写放大考量，需 Analyst 权衡。
- 迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，当前最新 `20261001000011_flash_sale`（`RESERVED`）；`migrations_test.go` 有 `latestMigrationVersion`/`businessTables`/`expectedSchema` 硬编码快照，新增需同步。
- 错误码集中在 `internal/codes/codes.go`：商品域 4000-4999（`4001` 商品不存在 → 404），通用域 1000-1999（`1001` 参数、`1000` 内部）。
- 后端当前无任何浏览量计数字段/表/模块；`frotend_web`/`frotend_manage` 为 vue-element-admin 模板工程，未接入本后端。

Assumption（合理但未经 Owner 确认，交 Analyst 核实并向 Owner 确认）：

- 浏览量为商品 SPU 粒度（`products.id`），非 SKU 粒度。
- V1 默认「总浏览量」（每次详情访问 +1），不做去重。
- 计数写入为同步（详情请求内），项目无 MQ/异步基础设施可复用。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 计数语义：总浏览量 vs 去重浏览量（用户/会话/IP 维度与去重窗口）。
- 数据模型：`products.view_count` 列（原子自增）vs 独立 `product_views` 表（明细/聚合）。
- 计数触发点：前台公开详情必计；后台/admin 详情、列表是否计数。
- 展示位置：仅详情 vs 详情+列表；是否新增独立统计查询接口。
- 计数失败语义：详情请求内计数失败是否影响详情返回（best-effort 不失败 vs 失败）。
- 是否需要新增错误码域（预计复用 4001/1001/1000，可能不需要）。

## Verification

环境：需可连接的 MySQL 8.0（`docker compose up -d`）；集成测试必须走真实 `RegisterFrontendRoutes`，用真实请求断言。

- AC-001 → 需 MySQL：访问前台商品详情，断言浏览量 +1 且详情正常返回。
- AC-002 → 需 MySQL（+ `-race`）：并发访问同一商品详情，断言最终计数 = 初始值 + 成功请求数（不丢失）。
- AC-003 → 需 MySQL：详情响应含 `view_count` 字段且为最新累计值。
- AC-004 → 需 MySQL：按 Owner 确认的展示位置断言浏览量字段正确返回。
- AC-005 → 需 MySQL：访问不存在/非上架商品，断言 404 且无计数写入。
- AC-006 → 需 MySQL：执行迁移断言计数字段/表结构正确、`migrations_test.go` 通过、迁移可重复执行。
- AC-007 → 文档审查：`docs/design/product.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及新数据模型（计数字段 vs 独立表）、修改既有商品详情/列表公开协议（新增 `view_count` 字段）、并发计数正确性（原子自增/防丢失）、计数语义（总量/去重）与触发点（公开详情/后台/列表）等关键业务规则；且同步计数落在热读路径带来写放大，多个现实方案会产生不同业务、可靠性与性能结果，需 Analyst 固化 Contract 后由 Owner 确认。

## Analyst Questions

1. 计数语义与去重：总浏览量 vs 去重（用户/会话/IP）；去重维度与窗口；是否需要「今日/累计」多口径。
2. 数据模型：`products.view_count` 列（原子自增 `UPDATE ... SET view_count = view_count + 1`）vs 独立 `product_views` 表（明细/聚合）；各自的并发正确性与写放大代价。
3. 计数触发点与失败语义：前台公开详情是否必计；后台/admin 详情、列表是否计数；计数失败是否影响详情返回（best-effort）。
4. 展示位置与协议：详情响应加 `view_count` 字段 vs 独立统计接口；列表是否返回；是否支持按浏览量排序。
5. 全局资源：1 个 migration（浏览量计数字段/表）；是否需要新增错误码域（预计复用 4001/1001/1000，可能不需要）。具体 version/域号由 Analyst 读 `.agent/registry/*` 派生。

## Review Baseline

- Base commit：`019401567078329af4a42f0612028989846679b4`（分支 `feature/goods-view-count`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/product-view-count-v1/`、`api/product/v1`（或等价浏览量 API 定义改动）、`internal/controller/product`、`internal/logic/product`、`internal/service` 的 `IProduct` 接口、`internal/codes`（如需）、migration 文件（浏览量计数字段/表）、`internal/migrations/migrations_test.go`、`internal/cmd` 路由及对应测试；`docs/design/product.md`（或新增设计文档）由 Analyst 写入。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）
