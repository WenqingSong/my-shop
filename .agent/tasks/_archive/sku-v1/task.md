# Task: SKU / 商品规格 V1

## Goal

交付「SKU / 商品规格」基础能力：在已实现的商品 SPU 之上建立「商品 1 — N SKU」关系，SKU 承载名称、价格、状态；提供 SKU 创建、修改、删除；商品详情组合 SPU 与 SKU；SKU 写操作受 RBAC 权限保护、前后台可见性隔离。为后续订单模块预留稳定 `sku.id` 作为引用键，「禁止删除已被订单引用的 SKU」作为 deferred requirement 延后到订单模块。

## Scope

允许完成的内容：

- 数据表 `skus`：`id`（`BIGINT UNSIGNED` 自增主键，稳定引用键，不重用）、`product_id`（`BIGINT UNSIGNED NOT NULL`，FK → `products.id`）、`name`、`price`（整数分）、`status`、`created_at`、`updated_at`；关系 `Product 1 — N Sku`。经既有 golang-migrate 机制新增迁移文件（如 `20261001000003_skus.up.sql`），不回退到 `boot.go` 建表。
- SKU 后台写接口：创建、更新、删除；统一 `AdminAuth + RequirePermission(...)`，超级管理员沿用现有放行机制。
- 商品详情（前台 + 后台）组合 SPU 与 SKU：详情响应包含该商品的 SKU 列表（具体形态待 Analyst 固化）。
- RBAC：新增 `sku:create` / `sku:update` / `sku:delete` 权限，追加到 `internal/boot/seed.go` 的 `seedPermissionList`。
- 错误码：SKU 域（新段或复用商品域，待 Analyst 固化）。
- 为后续订单引用预留稳定 `sku.id`（自增主键、全局唯一、删除后不重用）。
- 必要的测试：创建/更新/删除正常路径、价格校验、商品存在性、一对多与归属隔离、详情组合、状态字段校验、RBAC 401/403/成功、删除不影响 SPU。

## Out of Scope

明确本次不处理：

- 订单模块（`orders` / `order_items`）、最小订单链路、下单、购物车。
- 「禁止删除已被订单引用的 SKU」的实际校验逻辑（deferred，见下）。
- 库存查询、库存扣减、防负库存、库存流水、并发库存控制（均属于后续 4.3 Inventory，由独立 inventory 模型基于 `sku_id` 建立）。
- 规格项 / 规格值的笛卡尔组合自动生成 SKU（本任务只做 SKU 本身，不引入规格属性树 / attr）。
- SPU 删除、商品删除。
- 促销价、划线价、优惠券、`min_price` / `max_price` 派生。
- 销量、浏览量。
- 前端页面改造（`frotend_web` / `frotend_manage`）。
- 修改既有商品 SPU 的 `price` 字段语义（除非 Analyst/Owner 决策派生，否则保持现状）。

## Deferred Requirements

- **「禁止删除已被订单引用的 SKU」延后到订单模块实现。** 本任务不引入 `orders` / `order_items` 或最小订单链路，不扩大 Scope；只保证 `skus.id` 为稳定、唯一、不重用的引用键，供订单模块后续通过外键约束或应用层校验补齐该跨模块完整性规则。该项不进入当前 Acceptance Criteria，仅在订单模块任务中重新纳入并验收。

## Acceptance Criteria

- [ ] AC-001：管理员携带 `sku:create` 权限，提交合法 SKU（`product_id` 为存在的商品、`name` 非空且长度合规、`price` 为合法整数分）调用创建接口成功，新 SKU 持久化到 `skus` 表且 `product_id` 指向正确商品。
- [ ] AC-002：`price` 全程以整数分存储与出参；负数、非整数、超上限在创建与更新时均被拒绝，且不产生写入、不改变原值。
- [ ] AC-004：`product_id` 指向不存在商品时创建被拒绝，返回商品不存在错误，且无写入。
- [ ] AC-005：管理员携带 `sku:update` 权限可修改 SKU 的 `name` / `price` / `status`，修改后查询反映新值；非法值被拒且原值不变；更新不存在的 SKU 返回 404。
- [ ] AC-006：管理员携带 `sku:delete` 权限删除 SKU 后该 SKU 不再存在；删除不存在或已删除的 SKU 返回 404。
- [ ] AC-007：删除 SKU 不影响其所属商品（SPU）及同商品其他 SKU。
- [ ] AC-008：同一商品可拥有多个 SKU；查询某商品的 SKU 只返回该商品自己的 SKU，商品之间不串号。
- [ ] AC-009：商品详情响应组合 SPU 与 SKU，包含该商品全部 SKU 的 `name` / `price` / `status`；前台详情仅对可售商品返回 SKU 信息（具体可见性规则以 Analyst 固化的 contract 为准）。
- [ ] AC-010：SKU 状态可被创建/更新设置与校验，非法状态值被拒绝且无写入；具体状态枚举与「SKU 状态 × SPU 状态」联动规则以 Analyst 固化的 contract 为准。
- [ ] AC-011：未认证访问 SKU 写接口返回 401；已认证但无对应 `sku:*` 权限返回 403 且无写入；持有对应权限（含超级管理员）成功。
- [ ] AC-012：`skus.id` 为全局唯一自增主键，作为后续订单模块引用 SKU 的稳定键（经迁移 DDL 与查库确认，删除后不重用）。

## Relevant Context

已核实的事实：

- 技术栈：GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + Register 模式）→ `internal/logic`（`init()` 注册）；数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。SKU 模块应沿用此结构。
- 商品 SPU 已实现（`product-spu-v1`，Contract `APPROVED`、Cleaner `CLEAN`、Deliverer `PASS`）：`products`（含 SPU 级 `price` 整数分、`status` draft/on_shelf/off_shelf、`category_id` FK）与 `product_images`；SPU 定位为「基础展示价」，其 Out of Scope 明确含 SKU、库存、订单。
- RBAC 已完整实现：`internal/middleware/auth.go` 提供 `AdminAuth` 与 `RequirePermission(code)`（超管 `IsSuper` 放行，DB 授权失败 fail-closed）；`internal/boot/seed.go` 已 seed 20 个权限（含 4 个 `product:*`）；SKU 需新增 `sku:*` 权限并挂到 `internal/cmd/routes_admin.go`。
- 错误码集中在 `internal/codes/codes.go` 并映射 HTTP 状态：通用 1000-1005、IAM 2001-2010、分类 3001-3005、商品 4001-4007。SKU 域新段待定（建议 5000-5999，或复用商品域）。
- 迁移机制：golang-migrate v4，文件 `internal/migrations/sql/{version}_{title}.up.sql`，version 为 14 位时间戳；baseline `20261001000001`、products `20261001000002`。SKU 新增迁移（如 `20261001000003_skus.up.sql`），不回退 `boot.go`。
- 路由分离：`internal/cmd/routes_frontend.go`（公开）与 `routes_admin.go`（`AdminAuth` + `require(permission)`），在 `cmd.go` 的 `s.Group("/")` 下注册，外层已挂 `middleware.Response`；统一响应 `{code,message,data}`，客户端靠 `code` 判型。
- 无订单模块：`api/`、`internal/`、迁移均无 `orders` / `order_items`；前端 `orderManager` 仅为 UI 模板，无后端支撑。

Assumption：

- SKU 的 `price` 为独立字段（整数分），与 SPU 既有 `price`（基础展示价）暂不建立派生关系；二者关系待 Analyst/Owner 决策。
- SKU `status` 的具体枚举与「SKU × SPU」状态联动待 Analyst 固化。
- SKU 不引入 `sku_code` 业务编码，仅以自增 `id` 作为稳定引用键（除非 Analyst/Owner 另有决定）。
- SKU 删除为物理删除（当前无订单引用；deferred requirement 生效后由订单模块补校验）。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 见下节 Analyst Questions。

## Verification

- AC-001/004 → 需可连接 MySQL：创建合法 SKU 断言 200/0 且查库确认 `skus` 行与 `product_id` 归属正确；`product_id` 不存在断言拒绝且无写入。
- AC-002 → 非法 `price`（负/非整数/超上限）在创建与更新断言拒绝，且查库无新行、原值不变；合法值成功。
- AC-005/006 → 更新/删除集成测试：更新合法/非法字段、删除成功、重复删除 404。
- AC-007/008 → 一对多与归属隔离：构造多商品多 SKU 场景，断言删除与查询不串号、不影响 SPU。
- AC-009 → 详情组合：创建商品 + 多 SKU 后访问前台/后台详情，断言 SKU 列表归属与 `name`/`price`/`status` 字段正确。
- AC-010 → 状态字段：非法状态值断言拒绝且无写入；具体枚举与联动按 contract 集成测试。
- AC-011 → RBAC：无 token 401、有 token 无权限 403（查库无写入）、有权限成功、超管放行。
- AC-012 → 迁移 DDL 核对 `skus.id` 主键 `BIGINT UNSIGNED` 自增；查库确认自增不回退。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪（`docker compose up -d`）。

## Complexity

COMPLEX

原因：涉及数据模型选择（SPU `price` 与 SKU `price` 的关系）、公开协议选择（「商品详情组合 SPU 与 SKU」的 API 形态）、SKU 状态语义与可见性业务规则，以及为后续订单引用预留稳定键的跨模块完整性约束；不同方案会导致不同数据模型、API 与业务结果，需要 Analyst 固化 contract 后由 Owner 确认。

## Analyst Questions

1. SPU `price` 与 SKU `price` 的关系：保持独立（SPU=基础展示价、SKU=实际售价）还是派生（如 SPU 价格 = SKU 最低价/起始价）？是否涉及改造既有 `products.price` 与前台价格展示。
2. SKU `status` 语义：取值（启用/停用？是否对齐 SPU 的 `draft`/`on_shelf`/`off_shelf`？），以及「SKU 状态 × SPU 状态」的联动规则（如 SPU 下架后 SKU 是否不可售；前台详情是否只展示 on_shelf 商品的 SKU）。
3. 「商品详情组合 SPU 与 SKU」的 API 形态：SKU 列表内嵌进前台/后台详情响应，还是独立接口（如 `/admin/products/:id/skus`）；列表接口是否返回 SKU 数量或价格区间。
4. SKU 标识与唯一性：是否引入 `sku_code` 业务编码；同一商品下 `name` 是否唯一。
5. SKU 错误码段与权限 code 命名：新开 5000-5999 还是复用商品域；`sku:create`/`sku:update`/`sku:delete` 命名，以及是否新增 `sku:list` 读权限。

## Review Baseline

- Base commit：`55cc88af78a00cf8e98dcbba23e2968505ef7d8f`（`main`，合并 MR #10）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/sku-v1/`、`api/sku*`（或等价 SKU API 包）、`internal/controller/sku*`、`internal/logic/sku*`、`internal/service` 的 `ISku` 接口、`internal/codes` SKU 域扩展、migration 文件（`skus`，经既有机制新增）、`internal/boot/seed.go` 权限 seed 扩展、`internal/cmd` 路由扩展，以及可能对 `api/product/v1` 商品详情响应新增 SKU 组合字段（与既有商品详情重叠，需以最小改动叠加、不改变既有商品字段语义）。当前工作区干净，无既有未提交修改。

## Initial Route

READY_FOR_CODER
