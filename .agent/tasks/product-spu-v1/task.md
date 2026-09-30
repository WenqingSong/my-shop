# Task: 商品 SPU V1

## Goal

交付「商品 SPU」基础能力：回答“这是什么商品”。提供商品创建、更新、上下架、列表、详情、分类筛选、分页、排序、关键词搜索、图片与详情，以及 RBAC 权限保护和前后台商品可见性隔离。SPU 只承载基础展示价，不负责 SKU、库存、销量与订单能力。

## Scope

允许完成的内容：

- 数据表：
  - `products`：`id`、`name`、`brand`、`category_id`、`price`、`main_image`、`detail`、`status`、`created_at`、`updated_at`；`price` 使用整数分（如 12.34 元 = 1234）。
  - `product_images`：`id`、`product_id`、`url`、`sort`、`created_at`；关系 `Product 1 — N ProductImage`。
  - 不包含库存、SKU、销量字段。建表经 `db-migration` 前置任务建立的 Migration 机制新增（不回退到 `boot.Bootstrap` 幂等建表）。
- 商品状态机：状态固定 `draft` / `on_shelf` / `off_shelf`；创建统一为 `draft`，创建接口不能直接指定 `on_shelf`；合法迁移仅 `draft→on_shelf`、`on_shelf→off_shelf`、`off_shelf→on_shelf`；上下架必须经过独立状态迁移接口；并发迁移用条件 `UPDATE ... WHERE id=? AND status=?` + `RowsAffected` 判定，保证并发重复操作最多一次成功。
- 后台写接口：
  - 创建（`product:create`）、更新（`product:update`）、上架（`product:on_shelf`）、下架（`product:off_shelf`）。
  - 统一 `AdminAuth + RequirePermission(...)`；超级管理员沿用现有放行机制。
- 前台查询：`GET /products`（公开，仅 `on_shelf`，支持分页/分类筛选/关键词/排序）、`GET /products/:id`（公开，仅 `on_shelf` 可见）。
- 后台查询：`GET /admin/products`、`GET /admin/products/:id`（可查看全部状态）。
- 分类规则：SPU 只能绑定“存在的叶子分类”；分类筛选只做 `WHERE category_id = ?` 精确匹配。
- 分类删除保护：分类下存在商品时禁止删除，返回 HTTP 409 + `CodeCategoryHasProducts`（归分类域 3000-3999）。
- 分页：`page` / `size`，返回 `{items, total, page, size}`，限制最大 `size`。
- 关键词搜索：V1 用 SQL `LIKE`，搜索 `name`、`brand`（`name LIKE '%kw%' OR brand LIKE '%kw%'`）。
- 排序：仅白名单字段 `id` / `price` / `created_at` / `updated_at`，方向 `asc` / `desc`；非法字段用默认 `created_at DESC`，禁止拼接前端字段进 SQL。
- 图片：`main_image` + `images[]`，当前只存 URL 字符串，不负责上传/存储/压缩/审核。
- RBAC：新增权限 `product:create` / `product:update` / `product:on_shelf` / `product:off_shelf`，经现有 Seed 写入权限表。
- 错误码：新增商品域 4000-4999；`CodeCategoryHasProducts` 归分类域。
- 必要的测试：创建/更新、叶子分类校验、价格整数分、状态机全部合法与非法路径、并发重复上下架、前后台可见性、分页/排序/搜索/分类筛选、RBAC 401/403/success、分类删除保护、`products`/`product_images` 一致性。

## Out of Scope

明确本次不处理：

- SKU、spec、规格组合。
- stock / inventory / inventory_log / 库存扣减 / 库存并发。
- 销量、浏览量。
- 购物车、订单关联。
- 商品删除。
- 促销价、划线价、优惠券、`min_price`、`max_price`。
- 文件上传、对象存储、图片压缩与审核。
- 父分类包含后代商品的查询（分类筛选仅精确匹配）。
- 前端页面改造。
- 历史 Migration 体系的改造（本次不负责改造既有建表/迁移机制，仅新增商品相关表）。

以上分别留给后续 4.2 SKU、4.3 Inventory、订单模块等任务。

## Acceptance Criteria

- [ ] AC-001：管理员携带 `product:create` 权限、提交合法商品数据（`name` 非空、`category_id` 为存在的叶子分类、`price` 为合法整数分）调用创建接口成功，新商品 `status` 为 `draft`，并持久化到 `products` 表。
- [ ] AC-002：`price` 在创建/更新/查询全程以整数分传递与存储（12.34 元 = `1234`），数据库存整数、API 出参返回整数分。
- [ ] AC-003：`price` 非法（负数、非整数、超上限等）时创建与更新均被拒绝，返回价格错误，且不产生写入。
- [ ] AC-004：`category_id` 不存在或非叶子分类时，创建与更新被拒绝，返回对应分类错误。
- [ ] AC-005：普通更新接口提交 `status` 字段不改变商品状态（普通 Update 不能修改状态）。
- [ ] AC-006：`draft` 商品经上架接口 → `on_shelf` 成功。
- [ ] AC-007：`on_shelf` 商品经下架接口 → `off_shelf` 成功。
- [ ] AC-008：`off_shelf` 商品经上架接口 → `on_shelf` 成功。
- [ ] AC-009：所有非法迁移（`draft→off_shelf`、`on_shelf→on_shelf`、`off_shelf→off_shelf` 等）被拒绝，返回状态迁移错误，且原状态不变。
- [ ] AC-010：并发重复上架/下架同一商品，最多一次成功（其余得到迁移失败/幂等结果），最终状态正确。
- [ ] AC-011：前台 `GET /products` 只返回 `status=on_shelf` 的商品，`draft`/`off_shelf` 不出现。
- [ ] AC-012：前台 `GET /products/:id` 对 `draft`、`off_shelf`、不存在的商品均不可见（返回 404 或等价不可见）。
- [ ] AC-013：后台 `GET /admin/products` 与 `GET /admin/products/:id` 可查看 `draft`/`on_shelf`/`off_shelf` 全部状态。
- [ ] AC-014：分类筛选按 `category_id` 精确匹配（`WHERE category_id = ?`），不包含父分类后代商品。
- [ ] AC-015：分页返回 `{items, total, page, size}`，`size` 有最大上限（超限被限制）。
- [ ] AC-016：`keyword` 对 `name`、`brand` 做 `LIKE` 模糊匹配（等价 `name LIKE '%kw%' OR brand LIKE '%kw%'`）。
- [ ] AC-017：排序仅接受白名单字段，非法排序字段回落默认排序（`created_at DESC`），不存在 SQL 注入。
- [ ] AC-018：创建与更新时 `main_image` 与 `images[]` 正确持久化到 `products` 与 `product_images`，且二者写入一致（事务/失败回滚）。
- [ ] AC-019：未认证访问后台写接口返回 401。
- [ ] AC-020：已认证但无对应权限访问后台写接口返回 403，且不产生任何写入。
- [ ] AC-021：持有对应权限的管理员可正常执行创建/更新/上架/下架。
- [ ] AC-022：分类下存在商品时删除该分类返回 409（`CodeCategoryHasProducts`），分类与商品均不受影响。
- [ ] AC-023：无商品关联的分类仍可正常删除。

## Relevant Context

已核实的事实：

- 技术栈：GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`。
- 分层：`api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + Register 模式）→ `internal/logic`（`init()` 注册）；数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。商品模块应沿用此结构。
- RBAC 已完整实现（可复用，无需新建）：`internal/middleware/auth.go` 提供 `AdminAuth` 与 `RequirePermission(code)`（超级管理员 `IsSuper` 直接放行）；`internal/boot/seed.go` 已幂等 seed 16 个权限（含 `category:create/update/delete`）；分类写接口已在 `internal/cmd/routes_admin.go` 用 `require("category:xxx")` 挂载，商品写接口直接复用该模式。注意：`AGENTS.md` 第 11 节“RBAC 未实现”已过时，实际代码已完成 RBAC。
- 错误码体系已建立：`internal/codes/codes.go` 集中定义 + HTTP 状态映射，现有通用 1000-1005、IAM 2000-2999、分类 3000-3999（3001-3004 已用）。`CodeCategoryHasProducts` 与商品域 4000-4999 尚未建立。
- 分类模块已实现：`internal/logic/categories/categories.go` 有树形 CRUD；删除保护目前只检查“有子分类”（`CodeCategoryHasChildren` 3003），无商品关联检查；现有代码无独立“叶子分类判定”函数（只有 `maxLevel=3` 层级校验），SPU 叶子判定需新增。
- 路由分离：`internal/cmd/routes_frontend.go`（公开路由）与 `routes_admin.go`（`AdminAuth` + `require(permission)`）在 `internal/cmd/cmd.go` 的 `s.Group("/")` 下注册，外层已挂 `middleware.Response`。商品前台路由挂 frontend、后台路由挂 admin。
- 统一响应 `{code,message,data}`：`internal/middleware/response.go` 按业务错误码映射 HTTP 状态，客户端靠 `code` 判型。
- 建表现状：`internal/boot/boot.go` 通过 `ensureTables` + `CREATE TABLE IF NOT EXISTS` 幂等建表（users/categories/admins/roles/permissions/admin_roles/role_permissions）。环境每日重置（CNB DinD）、数据不持久，表必须可重复创建。本任务不再扩展 `boot.go` 建表，改经 `db-migration` 前置任务建立的 Migration 机制（迁移文件）新增 `products`/`product_images`。
- 建表机制（Analyst Question #1）已由 Owner 决策为方案 B：当前代码 `boot.go` 仍直接建表、全仓库无 migration 机制；Owner 于 2026-09-30 确认先由 Task Builder 新建 `db-migration` 前置任务（建立带时间戳 Migration 机制、迁移既有 7 张表），SPU 待其完成后经该机制新增 `products`/`product_images` 迁移，不回退到 `boot.go` 建表。

Assumption：

- 分页默认 `page=1`、`size=20`，最大 `size` 上限的具体值待 Analyst 固化（见 Analyst Questions）。
- 上下架接口路径、`status` 存储映射、`price` 上限、商品错误码具体编号均由 Analyst 固化（见 Analyst Questions），本任务不提前写死。
- 具体 JSON 字段结构与路由路径以 Analyst 固化的 contract 为准，Cleaner 按 AC 的可观察行为审查。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 见下节 Analyst Questions。#1（建表机制）已由 Owner 决策为方案 B（先建 `db-migration` 前置任务），会改变本任务 Scope 的实现基础；本任务暂停，待 `db-migration` 完成后恢复。

## Verification

- AC-001/002/003 → 需可连接 MySQL：创建合法商品断言 200/0 且查库确认 `status=draft`、`price` 为整数分；非法价格（负/非整数/超上限）断言 400 且查库无新行。
- AC-004 → 用不存在分类与非叶子分类（有子分类）创建/更新，断言拒绝且无写入；用叶子分类成功。
- AC-005 → 普通更新接口提交 `status` 字段，断言商品状态不变（查库核对）。
- AC-006/007/008/009 → 状态机集成测试：分别覆盖 3 条合法迁移成功、3 条及以上非法迁移被拒且原状态不变；需 MySQL。
- AC-010 → 并发集成测试：多 goroutine 并发调用上架/下架同一商品，断言最多一次成功、最终状态正确；建议 `-race` 下运行（并发一致性以业务断言为准）。
- AC-011/012/013 → 构造 `draft`/`on_shelf`/`off_shelf` 商品，分别访问前台与后台列表/详情，断言可见性。
- AC-014/015/016/017 → 列表接口分别用 `category_id`、`page/size`、`keyword`、`sort/order` 验证精确匹配、分页结构、LIKE 匹配、白名单排序与非法字段回落默认。
- AC-018 → 创建/更新后查 `product_images` 表核对图片行与 `sort`，并验证失败时不产生半成品（事务回滚）。
- AC-019/020/021 → RBAC 集成测试：无 token 401、有 token 无权限 403（查库无写入）、有权限成功、超级管理员放行。
- AC-022/023 → 分类下存在商品时删除分类断言 409/`CodeCategoryHasProducts` 且分类与商品不变；无商品分类删除成功。
- 通用命令：`go build ./...`、`go vet ./...`、`go test ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪（`docker compose up -d`）。

## Complexity

COMPLEX

原因：涉及商品状态机与并发状态迁移一致性、前后台数据可见性隔离、分类叶子约束、商品图片事务一致性，以及新增 RBAC 权限边界；状态迁移的并发正确性（条件 UPDATE + `RowsAffected`）和前后台可见性语义会直接决定业务结果，需要 Analyst 固化关键设计后由 Owner 确认。

## Analyst Questions

1. **建表机制（已决策为方案 B，阻塞项）**：Owner 于 2026-09-30 确认方案 B——先由 Task Builder 新建独立的 `db-migration` 前置任务（建立带时间戳 Migration 机制、迁移既有表），完成后 SPU 经该机制新增 `products`/`product_images` migration，不回退到 `boot.go` 建表。本任务据此暂停，待 `db-migration` 完成后恢复；届时确认迁移文件命名/机制后，更新本任务 contract 的「数据表」与「Allowed/Forbidden Changes」，重新交 Owner 确认。
2. `status` 数据库存储映射（如 `0=draft`、`1=on_shelf`、`2=off_shelf`）与 API 出参形态（数字还是字符串）。
3. 上下架 API 最终路径（如 `POST /admin/products/:id/on-shelf`、`POST /admin/products/:id/off-shelf`）及上下架请求/响应契约。
4. `price` 最大允许值（整数分上限）。
5. `page`/`size` 默认值与最大 `size`。
6. 商品错误码 4000-4999 的具体编号（至少覆盖 `ProductNotFound`、`ProductInvalidPrice`、`ProductInvalidCategory`、`ProductCategoryNotLeaf`、`ProductInvalidStatusTransition`、`ProductInvalidStatus`），以及 `CodeCategoryHasProducts` 的分类域编号。

## Implementation Plan（contract 确认后按依赖顺序拆分执行）

Owner 已明确：本任务为 COMPLEX，contract 经 Analyst 固化并由 Owner 确认后，拆分为多个可独立验收的 Coder Task，默认按依赖顺序执行，每个完成通过测试并形成独立 commit 后再进入下一项；除非文件修改范围不重叠，否则不并行执行多个 Coder（避免共享 service/routes/codes 等文件冲突）。建议边界：

1. **SPU Foundation**：建表（`products`/`product_images`）、状态常量、商品错误码、RBAC 权限 seed、API/Service 基础骨架。
2. **SPU Write Path**：创建、更新、叶子分类校验、图片事务、上下架状态机及并发一致性。
3. **SPU Read Path**：前后台列表/详情、分页、分类筛选、关键词搜索、排序、可见性隔离。
4. **SPU Hardening**：分类删除商品关联保护、集成测试、RBAC/状态机/查询边界回归。

## Review Baseline

- Base commit：`2d9bb938476bf9938c785abd72f95b7d0541b7f4`（分支 `feat/sku`，HEAD 为 `2d9bb93` 合并 MR #9）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/product-spu-v1/`、`api/product*/`（或等价商品 API 包）、`internal/controller/product*/`、`internal/logic/product*/`、`internal/service` 商品接口、`internal/codes` 商品域扩展、migration 文件（`products`/`product_images`，经 db-migration 机制新增）、`internal/boot/seed.go` 权限 seed 扩展、`internal/cmd` 路由扩展及对应测试。当前工作区干净，无需要区分的既有修改。

## Initial Route

READY_FOR_ANALYST
