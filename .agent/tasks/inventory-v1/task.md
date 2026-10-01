# Task: 库存（Inventory）V1 / 普通库存

## Goal

交付「普通库存」基础能力：在已实现的 SKU 之上，基于 `sku_id` 建立独立库存模型，提供库存查询、库存初始化/增加、条件扣减（库存充足才成功）、防止负库存（库存永不为负）、库存变更记录（流水），并以并发扣减测试证明核心不变量（库存不为负、扣减总量正确）。为后续订单模块预留稳定的库存扣减语义。

## Scope

- 数据表：独立 inventory 模型/表，基于 `sku_id` 建立（1:1 关系），字段含库存数量等；另建库存变更流水表记录每次变更（具体 schema 与字段由 Analyst 固化）。经既有 golang-migrate 机制新增迁移文件，不回退 `boot.go` 建表。
- 库存查询：查询指定 SKU 的当前库存数量（入口与可见性由 Analyst 固化）。
- 库存初始化/增加：库存需要有初始值与增加入口（这是扣减与流水能成立的前提；具体形态——独立「设置/调整库存」后台接口，还是 SKU 创建时自动建立 0 库存记录——由 Analyst 固化）。
- 条件扣减：按指定数量扣减库存，仅当库存充足时才成功；库存不足返回稳定错误且库存不变。
- 防止负库存：任何情况下（含并发）库存不为负（DB 约束或条件更新兜底，由 Analyst 固化）。
- 库存变更记录：每次成功的库存变更（增加/扣减/设置）产生一条流水记录，可查询。
- 错误码：新增库存域（建议 6000-6999，具体编号由 Analyst 固化）；写操作权限 code（`inventory:*` 或 `stock:*`）由 Analyst 固化。
- 必要的测试：查询、初始化/增加、条件扣减成功/拒绝、防负库存、流水记录、并发扣减。

## Out of Scope

- 订单模块、下单、购物车（扣减暂以后台/测试入口暴露；订单调用扣减的最终形态延后到订单模块）。
- MQ、异步扣减、跨系统一致性、幂等去重/补偿（当前无 MQ、无订单模块；如需幂等键由订单模块任务补）。
- 库存锁定/预占（下单冻结库存）、超卖以外的复杂库存控制。
- 多仓库、批次/序列号、效期、出入库单据。
- 低库存预警、补货提醒。
- 缓存库存（Redis 库存）——当前事实来源为单一 MySQL。
- 前端页面改造（`frotend_web` / `frotend_manage`；库存展示如需接前端，另议）。
- 修改既有 SKU/商品删除语义（除非为库存 FK 兜底需最小调整）。

## Acceptance Criteria

- [ ] AC-001（库存查询）：对存在的 SKU 可查询到其当前库存数量；SKU 不存在时返回稳定的 404 错误。
- [ ] AC-002（库存初始化/增加）：库存可经受保护的入口初始化或增加；成功后库存按提交量增加，并产生一条「增加」变更记录（入口与权限形态以 Analyst 固化的 contract 为准）。
- [ ] AC-003（条件扣减成功）：库存充足时扣减指定数量成功，库存减少对应数量，并产生一条「扣减」变更记录。
- [ ] AC-004（条件扣减拒绝）：库存不足时扣减被拒绝，返回稳定的「库存不足」错误（错误码与 HTTP 状态以 contract 为准），库存不变，且不产生成功扣减的变更记录。
- [ ] AC-005（防负库存）：在任何情况下（含并发扣减）库存值均不小于 0。
- [ ] AC-006（库存变更记录）：每次成功的库存变更都产生一条流水记录，包含 `sku_id`、变更类型、变更量、变更前后库存、时间（及操作者，如适用）；流水可查询。
- [ ] AC-007（并发扣减）：对同一 SKU 并发发起扣减，最终库存不为负，成功扣减总量 = 库存实际减少量，成功次数不超过可扣减上限。

## Relevant Context

已核实的事实：

- 技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + Register）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。库存模块应沿用此结构。
- `skus` 表已存在（迁移 `20261001000003_skus.up.sql`），**不含 `stock` 字段**；迁移注释与 `sku-v1` Contract 已明确：库存数据与操作交由后续「4.3 普通库存」基于 `sku_id` 独立建立（Owner 已确认库存与 SKU 元数据解耦）。
- SKU 服务接口 `ISku`（`internal/service/sku.go`）仅有 `Create`/`Update`/`Delete`/`ListByProduct`，**无 `Exists` 方法**（库存校验 `sku_id` 存在性时可能需要新增，或经 Analyst 固化）。
- RBAC 完整实现：`internal/middleware/auth.go` 提供 `AdminAuth` 与 `RequirePermission(code)`（超管 `IsSuper` 放行、DB 授权失败 fail-closed）；`internal/boot/seed.go` 的 `seedPermissionList` 已 seed 23 个权限（含 3 个 `sku:*`），库存写权限需新增。
- 错误码集中在 `internal/codes/codes.go` 并映射 HTTP 状态：通用 1000-1005、IAM 2001-2010、分类 3001-3005、商品 4001-4007、SKU 5001-5004；库存域 6000-6999 空闲。
- 迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，baseline `20261001000001`、products `20261001000002`、skus `20261001000003`；`serve` 不自动执行 migration（需 `my-shop migrate up`）。
- 路由分离：`internal/cmd/routes_admin.go`（`AdminAuth` + `require(permission)`）与 `routes_frontend.go`（公开），统一响应 `{code,message,data}`，客户端靠 `code` 判型。
- 事实来源为单一 MySQL；Redis 仅用于会话（管理员会话校验）。无 MQ、无异步、无订单模块。
- SKU 删除为物理删除且不级联（`skus.product_id` FK `ON DELETE RESTRICT`）；SKU 删除后其库存记录与流水的交互目前未定义。

Assumption：

- 库存为单值数量（非多仓/批次），数量为整数（整数个件），与价格整数分风格一致（具体以 Analyst 固化为准）。
- 扣减暂以后台/测试入口暴露（订单模块接入延后），但核心扣减逻辑应封装为可复用的 service 方法，供未来订单模块调用。
- 库存写操作（调整/扣减）复用 `AdminAuth + RequirePermission` 保护；读操作可见性待确认（后台查询可能仅 `AdminAuth`，参考后台商品查询无读权限的先例）。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 库存如何初始化与增加（入库/设置库存入口）——Owner 需求未明确列出该入口，但扣减与流水需要库存有初始值才能闭环。
- 扣减是否需要幂等/去重键（涉及未来订单模块可靠性，但当前无订单模块）。

## Verification

- AC-001 → 需可连接 MySQL：对存在 SKU 查询库存，断言返回当前数量；不存在 SKU 断言 404 且无写入。
- AC-002 → 需 MySQL：初始化/增加库存后查询，断言数量按提交量增加，且流水表新增一条「增加」记录（含变更前后值）。
- AC-003 → 需 MySQL：库存充足时扣减，断言成功、库存减少对应数量、流水新增「扣减」记录。
- AC-004 → 需 MySQL：库存不足时扣减，断言拒绝（稳定错误码）、库存不变、无成功扣减流水。
- AC-005 → 需 MySQL：构造并发扣减与单次不足扣减，断言最终库存 ≥ 0。
- AC-006 → 需 MySQL：多次变更后查流水表，断言每条记录字段完整、与库存变化一致、可查询。
- AC-007 → 需 MySQL + `-race`：多 goroutine 并发扣减同一 SKU，断言最终库存 ≥ 0，成功扣减总量 = 初始库存 − 最终库存，成功次数 ≤ ⌊初始库存/单次扣减量⌋。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪（`docker compose up -d`）。

## Complexity

COMPLEX

原因：涉及独立库存数据模型选择、库存初始化/增加入口与权限边界、条件扣减的并发一致性（防负库存）、库存流水 schema 与事务一致性，以及为后续订单模块预留的扣减语义与幂等性；不同方案会导致不同的数据模型、接口形态与业务/可靠性结果，需要 Analyst 固化 contract 后由 Owner 确认。

## Analyst Questions

1. 库存数据模型与初始化/增加入口：库存表 schema（`quantity` 类型与是否 `INT UNSIGNED`、`sku_id` 唯一约束与 FK 行为 `ON DELETE RESTRICT`/`CASCADE`）；库存如何初始化与增加——SKU 创建时自动建立 0 库存记录，还是提供独立的「设置/调整库存（入库）」后台接口？
2. 扣减接口形态与语义：扣减是后台 HTTP 接口（供运营/测试）还是仅内部 service 方法（供未来订单模块），还是两者都要？扣减请求参数（`sku_id` + 数量）；是否需要幂等键（`request_id`/`deduction_no`）防重复扣减？
3. 库存不足错误语义：错误码与 HTTP 状态（如 409/422），新增库存域错误段（建议 6000-6999）的具体编号。
4. 库存查询可见性与权限：后台查询接口形态（独立 `GET /admin/skus/:id/stock` 或内嵌到后台商品/SKU 详情）；前台是否需要公开库存可见性（此前 SKU V1 前台详情不含库存）；写接口权限 code 命名（`inventory:adjust`/`inventory:deduct` 或 `stock:*`）。
5. 变更流水 schema 与语义：流水字段（`sku_id`、变更类型枚举、变更量、变更前后值、操作者 `admin_id`、原因/备注、时间）；哪些操作记流水；扣减失败（库存不足）是否记流水。
6. 防负库存与并发机制：条件更新（`UPDATE ... WHERE quantity >= N` + `RowsAffected`）还是 `SELECT ... FOR UPDATE`；是否加 DB CHECK 约束（`quantity >= 0`）作为最后兜底（取决于 MySQL 版本，8.0.16+ 支持 CHECK）；并发扣减测试的并发度与验收断言。
7. SKU 删除与库存的交互：SKU 物理删除后，其库存记录与流水如何处理（FK RESTRICT 阻止删除、级联删除，还是保留孤儿记录）。

## Review Baseline

- Base commit：`90f05bdf74e2305d0d6a6ccbef1bb3b8a4584404`（分支 `feat/product`，HEAD 为「docs(sku): 补充 SKU V1 交付验收报告」）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/inventory-v1/`、`api/inventory*/`（或等价库存 API 包）、`internal/controller/inventory*/`、`internal/logic/inventory*/`、`internal/service` 的 `IInventory` 接口、`internal/codes` 库存域扩展、migration 文件（`inventory`/`inventory_log`，经既有机制新增）、`internal/boot/seed.go` 权限 seed 扩展、`internal/cmd` 路由扩展及对应测试。当前工作区干净，无既有未提交修改。

## Initial Route

READY_FOR_ANALYST
