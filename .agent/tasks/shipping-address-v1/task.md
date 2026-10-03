# Task: 收货地址（Shipping Address）V1

## Goal

交付前台用户的「收货地址」能力：登录用户可对自己的收货地址做增删改查，支持默认地址，且严格隔离数据（只能访问自己的地址）。为未来订单模块「下单时保存地址快照」预留稳定可读的地址详情能力，但本次不实现订单与快照。

## Scope

- 收货地址数据模型/表（字段、约束由 Analyst 固化，经既有 golang-migrate 机制新增迁移文件，版本接 inventory `20261001000004` 之后）。
- 前台用户侧地址接口（`middleware.Auth` 保护，挂载到 `routes_frontend.go`）：创建、查询列表、查询详情、更新、删除。
- 数据隔离：所有地址查询/修改/删除都按 `Principal.UserID` 过滤，绝不信任客户端传入的 `user_id`。
- 默认地址：支持标记默认地址；默认地址的唯一性语义与并发保证由 Analyst 固化。
- 错误码：新增地址域（建议 7000-7999，具体编号由 Analyst 固化）。
- 必要测试：正常路径、关键拒绝路径（访问他人地址、未登录）、默认地址边界、数据隔离。

## Out of Scope

- 下单、订单模块、以及「下单时保存地址快照」本身——订单模块当前不存在（`inventory-v1` 已确认无订单模块），快照属未来订单模块能力；本次仅保证地址详情可被稳定读取，供未来订单模块读取后快照。
- 行政区划/省市区码表数据与选择器（是否需要结构化地区、是否引入码表，由 Analyst 固化；若需码表且体量过大另议）。
- 地址数量上限、地址标签（家/公司）、地址分组等扩展。
- 后台（管理员）查看/管理用户地址。
- 前端页面改造（`frotend_web` / `frotend_manage`）。
- 修改既有 IAM / 认证 / 会话语义。

## Acceptance Criteria

- [ ] AC-001（创建地址）：登录用户携带有效 token 可创建收货地址，成功后返回该地址 id，且该地址归属当前用户。
- [ ] AC-002（查询列表）：登录用户可查询自己的地址列表，返回内容仅包含当前用户自己的地址。
- [ ] AC-003（查询详情）：登录用户可查询自己的单个地址详情；查询不存在的地址或他人的地址时返回稳定错误（404 或 403，以 Analyst 固化的 contract 为准），且不得泄露该地址是否存在、归属何人。
- [ ] AC-004（更新地址）：登录用户可更新自己的地址；更新他人的地址或不存在的地址被拒绝且不产生写入。
- [ ] AC-005（删除地址）：登录用户可删除自己的地址；删除他人的地址或不存在的地址被拒绝且不产生写入。
- [ ] AC-006（数据隔离）：以两个不同用户账号，通过真实 HTTP 路由验证：用户 A 无法读取、修改、删除用户 B 的任何地址（返回稳定错误且无数据变化）。
- [ ] AC-007（默认地址唯一性）：每个用户最多一个默认地址；设置某地址为默认后，原默认地址自动变为非默认（具体语义与并发保证以 Analyst 固化的 contract 为准）。
- [ ] AC-008（未登录拒绝）：未携带有效 token 访问任一地址接口返回 401，且不产生任何写入。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2，模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。地址模块应沿用此结构。
- `users` 表已存在（baseline `20261001000001_baseline.up.sql`）：`id`、`username`、`password_hash`、`created_at`、`updated_at`。目前**不存在** `addresses` 表，**不存在**任何订单相关表。
- 前台用户受保护接口用 `middleware.Auth` 注入 `Principal{UserID, Sid}`；Controller 通过 `middleware.PrincipalFromContext(ctx)` 取当前用户（参见 `internal/controller/iam/iam.go` 的 `Me`）。地址数据隔离必须基于 `Principal.UserID`。
- 路由分离：`internal/cmd/routes_frontend.go`（前台公开 + `Auth`）与 `routes_admin.go`（后台）。目前前台用户侧只有 `/me`、`/logout` 两个受保护接口，**尚无「用户自有资源」CRUD 先例**，地址将是首个。
- 错误码集中在 `internal/codes/codes.go` 并映射 HTTP 状态：通用 1000-1005、IAM 2001-2010、分类 3001-3005、商品 4001-4007、SKU 5001-5004、库存 6001-6002；地址域 7000-7999 空闲。
- 迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，当前最新为 inventory `20261001000004`；`serve` 不自动执行迁移（需 `my-shop migrate up`）。
- 事实来源为单一 MySQL；Redis 仅用于会话。无 MQ、无异步、无订单模块。

Assumption：

- 「下单时保存地址快照」属于未来订单模块能力，不在本任务实现；地址模块只需提供稳定的地址详情读取，供订单模块未来读取后复制字段形成快照。
- 默认地址采用电商常见语义：每用户最多一个默认地址；首次创建地址（或首个地址）自动成为默认（具体是否自动，交 Analyst 固化）。
- 前台地址接口不涉及 RBAC 权限 code（当前前台用户侧无 RBAC，仅登录认证；见 `AGENTS.md` 第 11 节「权限/RBAC 留白」），隔离靠 `Principal.UserID` 数据归属控制。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 地址字段结构（收货人、手机号、省/市/区、详细地址等）与地区表示方式（结构化 region code + 码表 vs 自由文本）。
- 「下单时保存地址快照」的边界确认：快照确属未来订单模块，本任务不做订单/快照表。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试必须走真实 `RegisterFrontendRoutes` + `middleware.Auth`，用两个真实用户账号断言隔离；断言 HTTP status、JSON 业务 code、拒绝场景无意外 DB 写入。

- AC-001 → 需 MySQL：登录用户创建地址，断言返回 id，且 `addresses` 表新增记录归属该 `user_id`。
- AC-002 → 需 MySQL：某用户创建若干地址后查列表，断言仅返回本人地址，不含他人地址。
- AC-003 → 需 MySQL：查询本人地址详情成功；查询他人地址/不存在地址断言稳定错误（404 或 403，以 contract 为准）。
- AC-004/AC-005 → 需 MySQL：更新/删除本人地址成功；更新/删除他人或不存在地址断言稳定错误且 DB 无变化。
- AC-006 → 需 MySQL + Redis：用户 A、B 各建地址，A 用自己 token 无法读/改/删 B 的地址。
- AC-007 → 需 MySQL（+ `-race`）：设置默认地址，断言同一用户原默认地址被取消、任意时刻默认地址数量 ≤ 1。
- AC-008 → 需 Redis：无 token 访问地址接口断言 401/`1002`，且 `addresses` 表无新增。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及新数据模型（地址字段结构）、默认地址唯一性的并发一致性保证（每用户最多一个默认地址）、数据隔离安全边界，以及「下单快照」与订单模块的边界划分；不同方案会产生不同的字段结构、接口语义与可靠性结果，需 Analyst 固化 contract 后由 Owner 确认。

## Analyst Questions

1. 地址数据模型与字段结构：收货地址字段（收货人姓名、手机号、省/市/区、详细地址、是否默认等）；地区表示方式（结构化 `province/city/district` + region code 码表 vs 自由文本）；各字段类型、长度、必填与格式校验（如手机号）。
2. 默认地址语义：是否每用户最多一个默认地址；首次创建是否自动成为默认；设置新默认时旧默认是否自动取消；删除默认地址后是否自动指定新默认或允许无默认；是否必须始终存在一个默认地址。
3. 默认地址唯一性实现机制：DB 约束（MySQL 无原生 partial unique index，可用 generated column + `UNIQUE(user_id, is_default)` 或 NULL 技巧）vs 应用层事务 + 条件更新；并发下如何保证「每用户最多一个默认」。
4. 数据隔离边界：资源不存在与「访问他人地址」是否统一返回 404（避免泄露存在性）还是区分 403；客户端是否可提交 `user_id`（应禁止并忽略）。
5. 下单快照边界：确认「下单时保存地址快照」属未来订单模块，本任务不实现订单/快照表；地址模块应提供的可读能力（详情查询）是否足以支持未来快照。
6. 接口形态与错误码：前台地址接口路径（如 `/addresses`，`Auth` 保护）；地址域错误码段（建议 7000-7999）的具体编号；是否需要每用户地址数量上限。

## Review Baseline

- Base commit：`5f160ab632e719d1fce2167bf6cada2541c74ae1`（分支 `feat/address`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/shipping-address-v1/`、`api/address*/`（或等价地址 API 包）、`internal/controller/address*/`、`internal/logic/address*/`、`internal/service` 的 `IAddress` 接口、`internal/codes` 地址域扩展、migration 文件（`addresses`，经既有机制新增）、`internal/cmd/routes_frontend.go` 路由扩展及对应测试。当前工作区干净，无既有未提交修改。

## Initial Route

READY_FOR_ANALYST
