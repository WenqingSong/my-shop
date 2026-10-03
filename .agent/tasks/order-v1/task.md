# Task: 订单核心闭环（Order）V1

## Goal

交付「普通订单核心闭环」：登录用户可从购物车勾选项或直接购买创建订单，服务端在下单时重读价格并保存商品/价格/地址快照，生成唯一订单号，用幂等键防重复下单，在单个事务内完成「创建订单 + 订单项 + 扣库存」，任一步失败整体回滚；订单状态机按「待支付 → 已支付 → 已发货 → 已收货 → 已完成（旁路：待支付 → 已取消）」推进，非法迁移失败，取消订单恢复库存，超时未支付自动取消，支付先做 Mock；用户只能查看/取消自己的订单，管理员才能发货/退款，所有越权操作无数据库副作用；并以「重复请求只建一单、并发下单不超卖、失败不留半成品、取消只补偿一次、重复支付回调不重复改状态」为一致性验收基准。

## Scope

- 订单数据模型（`orders` + `order_items`，必要时含订单状态/操作记录表），经既有 golang-migrate 机制新增迁移文件（schema 由 Analyst 固化），并同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion` 与 `businessTables`。
- 创建订单（均需登录，作用于 `Principal.UserID`）：
  - 从购物车勾选项创建订单；
  - 直接购买（单个 SKU + 数量）创建订单；
  - 服务端重读 `skus.price` 作为成交价，不信任客户端提交价格；
  - 保存商品、SKU、价格、收货地址快照；
  - 生成全局唯一订单号（DB 唯一约束兜底）；
  - 幂等键防重复下单；
  - 事务内「写订单 + 写订单项 + 扣库存」，任一步失败（含库存不足）整体回滚。
- 订单状态机：待支付 → 已支付 → 已发货 → 已收货 → 已完成；旁路 待支付 → 已取消；明确合法迁移表；非法迁移失败；取消订单恢复库存；超时未支付自动取消。
- 支付 Mock（不接真实支付平台），并把待支付推进为已支付（幂等，重复支付回调不重复改状态）。
- 权限：用户只能查看、取消自己的订单；管理员（含超管）才能发货或后台退款；所有越权操作无数据库副作用。
- 错误码：新增订单错误码域（语义：订单），具体编号由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。
- 权限 code：新增订单域权限（语义：发货、退款等写操作），并在 `internal/boot/seed.go` 的 seed 清单中登记。
- 长期设计：新增 `docs/design/order.md`（Design Impact = NEW），沉淀订单数据模型、状态机与合法迁移、下单/取消的一致性模型与补偿语义、权限边界、错误码域与权限 code。
- 必要测试：创建（购物车/直接购买）、服务端定价与快照、幂等去重、事务回滚、状态机合法/非法迁移、取消恢复库存、超时自动取消、支付 Mock 幂等、用户隔离、管理员权限、并发不超卖、取消只补偿一次。

## Out of Scope

- 真实支付平台接入、支付网关、退款到账、对账、资金侧回调（支付仅 Mock）。
- 物流/快递单号、配送轨迹、发货单号回传。
- 评价、售后、退换货流程（若退款需退货物流，另议）。
- 优惠券、促销、积分、会员价、多币种、运费/税费计算（订单金额仅商品价快照，不引入计价引擎）。
- 修改既有购物车/库存/地址/SKU/商品/分类/身份模块的行为（除订单所需的最小只读引用与必要的库存扣减/恢复协同）。
- 前端页面改造（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。
- 订单列表的高级筛选/统计/导出（本任务仅满足查询本人订单的最小能力）。

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/order.md

## Acceptance Criteria

- [ ] AC-001（创建订单·入口）：登录用户可从购物车勾选项或直接购买（单个 SKU）创建订单，成功后返回唯一订单号，订单归属该用户。
- [ ] AC-002（服务端定价 + 快照）：订单项成交价由服务端在下单时重读 `skus.price` 确定并快照，不信任客户端提交价格；订单保存商品/SKU/价格/收货地址快照，下单后修改商品价格或收货地址不影响已生成订单的快照内容。
- [ ] AC-003（唯一订单号）：每个订单生成全局唯一订单号，由 DB 唯一约束兜底，并发下单不产生重复订单号。
- [ ] AC-004（幂等防重复下单）：携带同一幂等键的重复创建请求只创建一个订单，后续重复请求返回已创建订单（或稳定幂等语义），不重复扣减库存、不产生第二个订单。
- [ ] AC-005（事务原子性）：创建订单 = 事务内「写订单 + 写订单项 + 扣库存」；任一步失败（含库存不足）整体回滚，不留下半成品订单/订单项，库存不变。
- [ ] AC-006（状态机合法迁移）：订单状态按 Owner 确认的合法迁移推进（待支付→已支付→已发货→已收货→已完成，旁路 待支付→已取消）。
- [ ] AC-007（非法迁移失败）：对处于某状态的订单执行非法迁移（如已支付→待支付、已完成→已发货等）被拒绝，返回稳定错误且状态不变。
- [ ] AC-008（取消恢复库存）：待支付订单被取消后，其下单时扣减的库存被恢复；取消与库存恢复原子完成。
- [ ] AC-009（超时未支付自动取消）：超过未支付时限的待支付订单被自动取消并恢复库存（触发机制与时限由 contract 固化）。
- [ ] AC-010（支付 Mock 幂等）：存在 Mock 支付入口将待支付订单推进为已支付；对同一订单重复发起支付（回调）不重复改变状态。
- [ ] AC-011（用户订单隔离）：用户只能查看、取消自己的订单；查看/取消他人订单或不存在的订单返回稳定错误（404/403 由 contract 固化）且无写入。
- [ ] AC-012（管理员发货/退款权限）：仅管理员（含超管，持有对应权限）能发货或后台退款；无权限访问返回 403 且无数据库副作用。
- [ ] AC-013（越权无副作用）：所有越权操作（用户操作他人订单、无权限管理员发货/退款）均不产生任何数据库写入。
- [ ] AC-014（并发不超卖）：并发对同一 SKU 下单，最终成功订单扣减库存总量不超过可售库存，库存不为负。
- [ ] AC-015（取消只补偿一次）：订单被取消（含并发/重复取消）只补偿一次库存，库存不会重复回补。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。订单模块应沿用此结构。
- 身份域：前台用户经 `middleware.Auth` 注入 `Principal{UserID, Sid}`；后台管理员经 `AdminAuth` + `RequirePermission(code)`（`IsSuper` 放行、fail-closed）。前台受保护路由挂 `routes_frontend.go` 的 `Auth` 分组，后台写路由挂 `routes_admin.go` 的 `require(code)` 分组。
- 商品/SKU：`products.status`（0=draft/1=on_shelf/2=off_shelf）、`products.price`（分）；`skus.price`（分，SKU 售价）、`skus.status`（1=enabled/0=disabled）、`skus.product_id` FK `ON DELETE RESTRICT`。成交价通常取 `skus.price`（与购物车一致）。
- 库存：`inventories`（`sku_id` 唯一、`quantity` INT UNSIGNED）；`IInventory.Deduct(ctx, skuID, qty, operatorID)` 为条件扣减（`UPDATE ... WHERE quantity >= N` + `RowsAffected`，不足返回 6001）。**关键约束：`Deduct` 内部自开 `g.DB().Transaction` 独立事务**，与订单创建的「同事务原子扣库存」诉求存在冲突，需 Analyst 明确协同方案（如新增 tx 感知的扣减方法）。
- 地址：`addresses` 含 `recipient_name`/`phone`/`province`/`city`/`district`/`detail` 等可快照字段，归属 `user_id`。
- 购物车：`cart_items`（`user_id`+`sku_id` 唯一、`quantity`、`selected`、`price_snapshot`）；`ICart.List` 返回含 `Selected`/`Available`/`Insufficient` 的条目，供「从购物车下单」读取勾选项（是否新增「仅取勾选项」方法由 Analyst 固化）。
- 错误码集中在 `internal/codes/codes.go`：通用 1000-1005、IAM 2000-2999、分类 3000-3999、商品 4000-4999、SKU 5000-5999、库存 6000-6999、地址 7000-7999、购物车 8000-8999。订单域为下一空闲错误码域（具体域号由 Analyst 读 Registry 派生）。
- 迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，当前最新 `20261001000006_cart_items`；`serve` 不自动执行 migration。新增订单表需同步更新 `migrations_test.go` 的 `latestMigrationVersion` 与 `businessTables`。
- RBAC seed：`internal/boot/seed.go` 的 `seedPermissionList` 现含 25 个权限（`category/product/sku/inventory/admin/role/permission` 各动作），订单发货/退款等写权限需新增并登记。
- 事实来源为单一 MySQL；Redis 仅用于会话。**代码库中无 MQ、无异步队列、无 cron/定时调度/后台 job 基础设施**（`serve` 仅 bootstrap + seed + 路由）。
- 后端当前无任何订单表、无订单模块；`frotend_web`/`frotend_manage` 为 vue-element-admin 模板工程，未接入本后端。

Assumption：

- 成交价为下单时 `skus.price`（非客户端提交、非 `products.price`），与购物车 `price_snapshot` 语义一致但订单快照为「锁定成交价」，不随后续改价变动。
- 库存在下单时扣减（非支付时），与 Owner「事务内创建订单、订单项并扣库存」一致；取消订单恢复库存。
- 「收货」由订单归属用户确认，「已完成」为终态；「发货」「退款」由管理员触发。
- 支付 Mock 为某种受控入口/端点，将待支付订单推进为已支付并具备幂等语义（具体形态由 Analyst 固化）。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 退款语义：Owner 状态机未列「已退款」，但需求提到「管理员后台退款」——是否引入「已退款」状态及其迁移路径、是否恢复库存，需 Owner 确认。
- 超时未支付自动取消：未支付时限（时长）与实现载体（懒取消 vs 后台定时扫描 vs 引入 cron/goroutine），当前无现成调度基础设施。
- 幂等键来源与形态：客户端生成 `request_id` 还是服务端预下单令牌；唯一约束作用域（`(user_id, key)` 还是全局唯一）。
- 直接购买与购物车购买的请求结构与校验边界（如是否校验勾选状态、是否清空已购购物车条目）。
- 订单号生成规则（格式、是否含日期/随机/序号）与唯一性重试策略。
- 订单对 SKU/地址/购物车条目的引用语义（快照后软引用无 FK vs 强引用 FK；SKU 被删后订单是否仍可读）。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试必须走真实 `RegisterFrontendRoutes` + `middleware.Auth` 与 `RegisterAdminRoutes` + `AdminAuth/RequirePermission`，用真实用户/管理员账号断言。

- AC-001 → 需 MySQL + Redis：登录用户分别经购物车勾选与直接购买下单，断言返回唯一订单号、`orders`/`order_items` 记录归属该用户。
- AC-002 → 需 MySQL：下单后经后台改 `skus.price`/改地址，断言订单快照价格与地址不变；断言成交价 = 下单时 `skus.price` 而非客户端提交值。
- AC-003 → 需 MySQL：并发/连续下单断言订单号全局唯一（DB 唯一约束兜底，重复订单号无法落库）。
- AC-004 → 需 MySQL：同一幂等键重复提交下单，断言仅一个订单、库存仅扣一次。
- AC-005 → 需 MySQL：构造库存不足/订单项写入失败的场景，断言无半成品订单/订单项、库存不变。
- AC-006/AC-007 → 需 MySQL：遍历合法与非法迁移，断言合法成功、非法被拒且状态不变。
- AC-008/AC-015 → 需 MySQL（+ `-race`）：取消订单断言库存恢复一次；并发/重复取消断言只补偿一次。
- AC-009 → 需 MySQL：构造超时待支付订单，触发自动取消（机制以 contract 为准），断言状态变为已取消且库存恢复。
- AC-010 → 需 MySQL：对已支付订单重复发起 Mock 支付，断言状态不重复改变。
- AC-011/AC-013 → 需 MySQL + Redis：用户 B 查看/取消用户 A 的订单，断言稳定拒绝且无写入。
- AC-012/AC-013 → 需 MySQL + Redis：无权限管理员（及普通用户 token）发货/退款，断言 403 且无写入；超管/有权限管理员成功。
- AC-014 → 需 MySQL + `-race`：并发对同一 SKU 下单，断言成功订单扣减总量 ≤ 可售库存、库存 ≥ 0。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及新数据模型（`orders`/`order_items` 与快照语义）、新状态机（合法迁移表）、下单与库存的跨模块原子一致性（`IInventory.Deduct` 现为独立事务，需重新设计协同方式）、并发防超卖、幂等键去重、取消库存补偿的幂等与并发、超时自动取消所需的调度机制（当前无任何异步/定时基础设施），以及「管理员退款」的状态机缺口与权限边界；多个现实方案会产生不同的数据模型、接口形态与可靠性结果，需 Analyst 固化 contract 后由 Owner 确认。

## Analyst Questions

1. 订单数据模型与快照语义：`orders`/`order_items` schema；哪些字段快照（商品名、SKU 名、价格、主图、收货地址），哪些保留引用；`user_id`/`sku_id`/`address_id` 的引用语义（强 FK vs 快照后软引用）；SKU 被删后订单是否仍可读（快照自足）。
2. 下单与库存原子一致性：`IInventory.Deduct` 现为独立事务，如何与「写订单 + 写订单项」同事务（新增 tx 感知扣减方法 vs 其他），并发下单防超卖与条件扣减的协同。
3. 幂等键：来源（客户端 `request_id` vs 预下单令牌）、DB 唯一约束作用域、重复请求返回语义（返回既有订单 vs 冲突错误）。
4. 订单号：生成格式与唯一性保证、撞号重试策略。
5. 状态机与退款：完整合法迁移表；「后台退款」是否需「已退款」状态及其迁移与库存补偿；「收货」「已完成」的触发者与时机；非法迁移的错误语义。
6. 支付 Mock 形态：把待支付推进为已支付的入口（用户侧 mock 支付接口 vs 模拟支付回调端点）与幂等实现。
7. 超时未支付自动取消：未支付时限（Owner 决定）；实现载体（懒取消：查询/操作时判定过期并补偿 vs 后台定时扫描 vs 引入 cron/goroutine），以及取消补偿库存的幂等与并发。
8. 权限与错误语义：发货/退款/取消对应的权限 code 命名与 seed 登记；前台订单路径与后台订单路径；越权与不存在的统一错误语义（404 防枚举 vs 403）；用户取消 vs 系统自动取消的权限归属。
9. 全局资源：新增订单错误码域（语义：订单）、新增 migration（`orders`/`order_items` 及可能的订单操作记录表）、新增 `order:*` 权限 code；具体编号/版本由 Analyst 读 `.agent/registry/*` 派生。

## Review Baseline

- Base commit：`f197bc20f41a8a545cef3d5b7b8d8f1e3435aa6b`（分支 `feat/order`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/order-v1/`、`api/order*/`（或等价订单 API 包）、`internal/controller/order*/`、`internal/logic/order*/`、`internal/service` 的 `IOrder` 接口、`internal/codes` 订单域扩展、migration 文件（`orders`/`order_items` 等，经既有机制新增）、`internal/boot/seed.go` 的 `order:*` 权限 seed、`internal/cmd` 路由扩展及对应测试；`docs/design/order.md` 由 Analyst 写入。当前工作区干净，无既有未提交修改。

## Initial Route

READY_FOR_ANALYST
