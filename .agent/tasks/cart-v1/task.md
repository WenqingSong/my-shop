# Task: 购物车（Cart）V1

## Goal

交付用户侧购物车基础能力：登录用户可将有效 SKU 加入购物车、修改数量、删除条目、查看列表、勾选/取消勾选商品；购物车仅归属于当前登录用户，任何操作只作用于本人数据；购物车能正确反映商品下架、价格变化、库存不足这三类异常，并给出可观察的状态标识。为后续订单/结算模块预留稳定的购物车数据与查询语义（本任务不实现下单、不扣减库存）。

## Scope

- 购物车数据模型：以「用户 + SKU」为粒度的购物车条目（含数量、勾选状态等），经既有 golang-migrate 机制新增迁移文件（具体 schema 由 Analyst 固化）。
- 购物车写操作（均需登录，作用于当前用户）：
  - 添加 SKU（校验 SKU 存在、所属商品可售，重复添加的语义由 Analyst/Owner 固化）。
  - 修改数量（校验正整数等边界）。
  - 删除条目。
  - 勾选/取消勾选条目。
- 购物车查询：列出当前用户全部条目（含每条目的商品/SKU 快照信息、价格、勾选状态与异常状态标识）。
- 异常状态处理：商品下架（或 SKU 禁用）、价格变化、库存不足三类异常，在购物车中必须可被用户观察到（具体标识形态与语义由 Analyst/Owner 固化）。
- 用户隔离：所有读写在 `Principal.UserID` 维度过滤，用户只能操作自己的购物车。
- 错误码：新增购物车域（建议 7000-7999，具体编号由 Analyst 固化）。
- 必要的测试：列表、添加（含重复）、改数量、删除、勾选、异常状态（下架/价格/库存）、用户隔离的正常与拒绝路径。

## Out of Scope

- 下单/结算/订单模块：购物车勾选后如何生成订单、何时真正扣减库存，均不在本任务内（勾选仅维护购物车状态，结算流程延后到订单模块）。
- 库存扣减/预占/锁定：购物车只「读取并标识」当前库存是否充足，不扣减、不预占库存；库存事实来源与扣减语义沿用 `inventory-v1`。
- MQ、异步、跨系统一致性、幂等键、补偿（购物车为单 MySQL 事实来源，无此需要）。
- 价格策略本身（促销、会员价、折扣、多币种）——仅处理「价格变化后购物车如何呈现」。
- 购物车上限、批量操作、购物车与订单的合并/失效策略（如登录后合并匿名购物车）。
- 前端页面改造（`frotend_web` / `frotend_manage` 为未接入本后端的模板工程，购物车 UI 另议）。
- 修改既有 IAM/商品/SKU/库存/分类模块行为（除购物车引用所需的最小只读查询）。

## Acceptance Criteria

- [ ] AC-001（列表）：登录用户可列出自己的购物车条目，空购物车返回空列表（非错误）。
- [ ] AC-002（添加 SKU）：登录用户可将一个存在、所属商品可售（on_shelf 且 SKU enabled）的 SKU 加入购物车，成功后该条目归属该用户且可见；SKU 不存在、商品已下架或 SKU 已禁用时返回稳定错误且不产生写入。
- [ ] AC-003（重复添加）：同一用户对同一 SKU 重复添加时的行为（累加数量或拒绝）符合 Owner 确认的语义，且不会产生重复条目或数据错乱。
- [ ] AC-004（修改数量）：登录用户可将条目数量修改为合法正整数；数量为 0、负数、非整数或超过边界时返回参数错误且原数量不变。
- [ ] AC-005（删除条目）：登录用户可删除自己购物车中的条目；删除不存在的条目返回稳定错误（具体语义由 Analyst/Owner 固化）。
- [ ] AC-006（勾选）：登录用户可勾选/取消勾选条目，勾选状态被持久化并在列表中可见。
- [ ] AC-007（下架/禁用）：条目对应的商品被下架或 SKU 被禁用后，购物车列表能明确标识该条目「不可购买」，且无法继续作为可结算条目（具体标识字段与是否自动清理由 Analyst/Owner 固化）。
- [ ] AC-008（价格变化）：条目对应 SKU 价格发生变化后，购物车按 Owner 确认的语义呈现价格（快照价或实时价），且价格不会出现越界/错误值。
- [ ] AC-009（库存不足）：条目数量超过当前库存时，购物车列表能明确标识「库存不足」及其影响（具体标识字段与是否自动调整数量由 Analyst/Owner 固化）。
- [ ] AC-010（用户隔离）：用户 A 无法查看、修改或删除用户 B 的购物车条目；跨用户操作返回不泄露他人数据存在性的稳定错误（404 或 403 由 Analyst/Owner 固化），且无任何写入。
- [ ] AC-011（必须登录）：所有购物车接口均需有效用户凭证，无 token/非法 token 返回 401，不返回任何购物车数据。

## Relevant Context

已核实的事实：

- 技术栈 GoFrame v2.10.3（Go 1.23），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。购物车模块应沿用此结构。
- 用户身份：`internal/middleware/auth.go` 的 `Auth` 解析用户侧 token（type=user）并注入 `Principal{UserID, Sid}`，controller 经 `middleware.PrincipalFromContext(ctx)` 读取（`internal/controller/iam/iam.go` 已有示范）。前台受保护路由挂载于 `internal/cmd/routes_frontend.go` 的 `user.Middleware(middleware.Auth)` 分组，购物车路由应挂在此分组下（具体路径由 Analyst 固化）。
- 商品/SKU 数据模型已存在：`products`（`status` TINYINT 0=draft/1=on_shelf/2=off_shelf，`price` INT UNSIGNED 分）、`skus`（`product_id`、`price` INT UNSIGNED 分、`status` TINYINT 1=enabled/0=disabled）。**`products.price` 与 `skus.price` 并存**，购物车条目为 SKU 粒度，有效单价通常取 `skus.price`（价格语义待 Analyst/Owner 固化）。
- 库存模型已存在（`inventory-v1` 已 APPROVED）：`inventories`（`sku_id` 唯一、`quantity` INT UNSIGNED，无记录=0）。**库存扣减语义已明确留给订单模块**（`IInventory.Deduct` 供未来订单调用）；购物车本任务不应扣减库存，仅需读取并标识是否充足。
- 错误码集中在 `internal/codes/codes.go`：通用 1000-1005、IAM/管理员 2000-2999、分类 3000-3999、商品 4000-4999、SKU 5000-5999、库存 6000-6999；**购物车域 7000-7999 空闲**。
- 迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，当前最新版本 `20261001000004_inventory`；`serve` 不自动执行 migration（需 `my-shop migrate up`）。新增购物车表需同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion` 与 `businessTables`。
- 事实来源为单一 MySQL；Redis 仅用于会话。无 MQ、无订单模块。
- `frotend_web`/`frotend_manage` 为 vue-element-admin 模板工程，`src/api` 仅含模板示例（`article.js`/`user.js` 等），未接入本 Go 后端，无购物车前端。

Assumption：

- 购物车条目粒度为 SKU（非商品 SPU），「添加 SKU」即按 `sku_id` 记一条目；数量为整数（整数个件），与库存 `quantity`、价格「整数分」风格一致。
- 勾选状态为购物车条目的持久化布尔字段，仅表示用户的选择意图，不触发任何下单/扣减（结算延后到订单模块）。
- 购物车查询需联查 SKU/商品/库存以返回展示信息与异常标识；读库存用现有 `IInventory.Get` 或等价的只读查询，不改动库存语义。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 价格呈现语义：购物车存「加购时快照价」还是「实时 SKU 价」，两者导致不同数据模型与结算行为。
- 异常处理语义：商品下架 / SKU 禁用 / 库存不足时，购物车条目的具体标识形态（例如 `available`/`reason` 字段）与是否自动清理或自动调整数量。
- 重复添加同一 SKU 的语义（累加数量 vs 拒绝）与数量上限（是否有业务上限、是否以库存为上限）。
- 删除不存在条目的语义（404 vs 幂等成功）。
- 跨用户越权的错误语义（404 防枚举 vs 403）与购物车条目是否允许被 SKU/商品删除所影响（FK 行为）。

## Verification

- AC-001/002/003 → 需可连接 MySQL + Redis：用两个真实用户分别注册登录，覆盖列表、添加、重复添加，直接查库确认条目归属与数量。
- AC-004/005/006 → 需 MySQL：覆盖数量边界（0/负数/非整数/超上限）与删除、勾选，断言库中状态与返回值一致、非法输入无写入。
- AC-007 → 需 MySQL：加入条目后，经后台接口下架商品（或禁用 SKU），再查购物车，断言条目被标识为不可购买（标识字段以 contract 为准）。
- AC-008 → 需 MySQL：加入条目后经后台改价，再查购物车，断言价格符合 Owner 确认的语义。
- AC-009 → 需 MySQL：加入条目后经后台扣减库存使 `quantity < 条目数量`，再查购物车，断言被标识为库存不足（标识字段以 contract 为准）。
- AC-010 → 需 MySQL + Redis：用户 B 尝试查看/修改/删除用户 A 的条目，断言返回稳定拒绝错误（404/403 以 contract 为准）且查库无任何写入。
- AC-011 → 需 MySQL + Redis：无 token/非法 token 访问购物车接口，断言 401 且无数据返回。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪（`docker compose up -d`）。

## Complexity

COMPLEX

原因：涉及购物车数据模型选择（价格快照 vs 实时价、异常状态字段设计）、商品下架/价格变化/库存不足三类异常的业务语义（不同方案导致不同数据模型与用户可见行为），以及购物车与商品/SKU/库存的跨模块一致性边界；关键业务规则缺失会导致不同实现结果，需 Analyst 固化 contract 后由 Owner 确认。

## Analyst Questions

1. 购物车数据模型：`cart_items` 表 schema（`user_id`/`sku_id` 联合唯一、`quantity`、`selected`、是否存价格快照、异常标识字段）；`sku_id` FK 行为（`ON DELETE RESTRICT` 或软引用）；`user_id` 是否建 FK 到 `users`。
2. 价格语义：加购时快照价 vs 实时 `skus.price`；若快照，价格变化后是否提示差异、何时刷新。
3. 异常处理语义：商品下架 / SKU 禁用 / 库存不足分别如何标识（例如条目级 `available` 布尔 + `reason` 枚举，或查询时动态计算）；是否自动清理不可购条目；库存不足是否自动把数量钳制到库存上限。
4. 添加/修改数量边界：重复添加同一 SKU 的语义（累加 vs 拒绝）；数量上限与是否以库存为上限；数量 0 是否等价于删除。
5. 用户隔离与越权错误语义：跨用户操作返回 404（防枚举）还是 403；删除不存在条目的语义（404 vs 幂等成功）。
6. 购物车与既有模块的引用边界：读取 SKU 存在性/价格/状态、商品状态、库存数量所需的最小服务方法是否已具备（`ISku`/`IProduct`/`IInventory`），还是需新增只读方法；购物车条目在 SKU 被物理删除时的行为。

## Review Baseline

- Base commit：`5f160ab632e719d1fce2167bf6cada2541c74ae1`（分支 `feat/cart`，HEAD 为「合并来自 develop 的合并请求 #11」）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/cart-v1/`、`api/cart*/`（或等价购物车 API 包）、`internal/controller/cart*/`、`internal/logic/cart*/`、`internal/service` 的 `ICart` 接口、`internal/codes` 购物车域扩展、migration 文件（`cart_items` 等，经既有机制新增）、`internal/cmd/routes_frontend.go` 路由扩展及对应测试。当前工作区干净，无既有未提交修改。

## Initial Route

READY_FOR_ANALYST
