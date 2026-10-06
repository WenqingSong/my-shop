# Task: 秒杀（Flash Sale）V1

## Goal

交付「秒杀核心闭环」V1：管理员可创建秒杀活动（绑定商品/SKU、秒杀价、秒杀库存、开始/结束时间），登录用户在活动时间窗内以秒杀价下单，系统以「一人一单 + 请求幂等键 + 条件库存扣减」在**单一 MySQL** 上保证以下五个不变量可被并发集成测试证明：

1. 秒杀库存永远不小于零；
2. 成功订单总数不超过秒杀初始库存；
3. 同一用户对同一活动商品不能重复购买；
4. 失败请求不创建订单；
5. 重复请求不会重复扣减库存。

V1 只用 MySQL 证明正确性，Redis、Lua、MQ 等并发/削峰手段明确不在本次范围（留待证明正确后再谈）。

## Scope

- 秒杀活动数据模型：活动（起止时间、状态）、活动内商品/SKU 绑定、秒杀价、秒杀库存（含已售/剩余），经既有 golang-migrate 机制新增迁移文件（schema 由 Analyst 固化），并同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion`、`businessTables` 与 `expectedSchema`。
- 秒杀下单（均需登录，作用于 `Principal.UserID`）：
  - 活动时间窗校验（开始前/结束后拒绝）；
  - 服务端以秒杀价定价并快照（不信任客户端提交价）；
  - 一人一单（同一用户对同一活动商品至多成功一单）；
  - 请求幂等键（重复请求不重复扣库存、不重复建单）；
  - 条件扣减秒杀库存（防负库存、防超卖）；
  - 事务内「校验 + 扣秒杀库存 + 创建订单」，任一步失败整体回滚。
- 错误码：新增秒杀错误码域（语义：秒杀），具体编号由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。
- 权限：管理员创建/管理秒杀活动的写操作需相应权限（语义：秒杀活动管理），并在 `internal/boot/seed.go` 登记。
- 长期设计：新增 `docs/design/flash-sale.md`（Design Impact = NEW），沉淀秒杀数据模型、秒杀库存与一人一单/幂等的一致性模型、时间窗语义、权限边界与错误码域。
- 必要测试：**并发集成测试为核心验收**，证明上述五个不变量（含 `-race`），并覆盖正常、关键拒绝与边界路径。

## Out of Scope

- Redis、Lua、MQ、Redis 预扣库存、令牌桶/限流、削峰、异步下单、缓存库存、活动预热——本次只用 MySQL 证明正确性，先不引入。
- 真实支付平台（支付沿用普通订单的 Mock 语义或由 Analyst 明确引用方式）。
- 优惠券/促销叠加、会员价、多币种、运费/税费计算。
- 秒杀页面/CDN/静态化、验证码、风控/防机器人、黑名单。
- 修改既有普通订单/库存/购物车/商品/SKU/身份模块的行为（除秒杀所需的最小只读引用与必要的库存协同）。
- 前端页面改造（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/flash-sale.md

## Acceptance Criteria

- [ ] AC-001（活动创建与可见）：管理员可创建秒杀活动，指定活动商品/SKU、秒杀价、秒杀库存、开始/结束时间；无权限访问返回稳定错误（403）且无数据库写入。
- [ ] AC-002（时间窗拒绝）：活动开始前或结束后，用户下单被稳定拒绝（含时间边界语义由 contract 固化），且不产生订单、不扣减秒杀库存。
- [ ] AC-003（秒杀价生效与快照）：下单成交价 = 服务端在下单时重读的秒杀价（非客户端提交价、非普通 SKU 价），并快照；下单后改价不影响已生成订单。
- [ ] AC-004（一人一单）：同一用户对同一活动商品只能成功购买一次；重复购买请求被稳定拒绝且无数据库写入。
- [ ] AC-005（幂等防重复扣库存）：携带同一幂等键的重复创建请求只产生一个订单、秒杀库存只扣一次；后续重复请求返回既有结果（幂等语义由 contract 固化），不产生第二个订单、不重复扣库存。
- [ ] AC-006（库存不为负）：任何情况下（含并发下单）秒杀库存不小于零。
- [ ] AC-007（成功订单数 ≤ 初始库存）：并发下单下，成功订单总数不超过该活动的秒杀初始库存。
- [ ] AC-008（失败不建订单）：库存不足、超时、已购、参数非法、越权等失败请求不创建订单、不扣减秒杀库存、不留下半成品订单/订单项。
- [ ] AC-009（事务原子性）：下单 = 单事务「时间窗校验 + 一人一单校验 + 条件扣秒杀库存 + 创建订单」，任一步失败整体回滚，秒杀库存与订单均不变。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。秒杀模块应沿用此结构。
- 身份域：前台用户经 `middleware.Auth` 注入 `Principal{UserID, Sid}`；后台管理员经 `AdminAuth` + `RequirePermission(code)`（`IsSuper` 放行、fail-closed）。前台受保护路由挂 `routes_frontend.go` 的 `Auth` 分组，后台写路由挂 `routes_admin.go` 的 `require(code)` 分组。
- 普通订单已实现幂等与条件扣减的成熟范式：`orders.idempotency_key` + `request_hash` + `uk_user_idempotency(user_id, idempotency_key)` 唯一约束；下单事务内写 `orders` + `order_items` + 经 `IInventory.DeductInTx` 条件扣减（`UPDATE ... WHERE quantity >= N` + `RowsAffected`）。秒杀的幂等/扣减可参照或复用该范式。
- 库存服务 `IInventory` 已提供 tx 感知方法 `DeductInTx`/`IncreaseInTx`（复用「条件更新 + RowsAffected + 流水」，不自开事务），可供秒杀在下单事务内调用（是否复用普通库存由 Analyst 决定，见 Analyst Questions）。
- 错误码集中在 `internal/codes/codes.go`：通用 1000-1005、IAM 2000-2999、分类 3000-3999、商品 4000-4999、SKU 5000-5999、库存 6000-6999、地址 7000-7999、购物车 8000-8999、订单 9000-9999。秒杀为下一空闲错误码域（具体域号由 Analyst 读 `.agent/registry/error-codes.md` 派生）。
- 迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，当前最新 `20261001000008_refresh_tokens`；`serve` 不自动执行 migration。新增秒杀表需同步更新 `migrations_test.go` 的 `latestMigrationVersion`、`businessTables` 与 `expectedSchema`（硬编码结构快照）。
- RBAC seed：`internal/boot/seed.go` 的 `seedPermissionList` 现含 27 个权限，秒杀活动管理写权限需新增并登记。
- 事实来源为单一 MySQL；Redis 仅用于会话。代码库中无 MQ、无异步队列、无 cron/定时调度基础设施。
- 后端当前无任何秒杀表、无秒杀模块；`frotend_web`/`frotend_manage` 为 vue-element-admin 模板工程，未接入本后端。

Assumption：

- 秒杀下单发生在活动时间窗内，时间判定与扣库存应处于同一事务的 MySQL 条件写入中（以 `NOW()` 与起止时间比较），避免 Go 进程与 MySQL 时区漂移导致的边界误判。
- 「一人一单」与「请求幂等键」是两层不同语义：幂等键用于重复请求去重，一人一单是业务唯一约束；二者最终都应有 DB 唯一约束兜底，而非仅应用层「先查再写」。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 「成功订单」的定义：秒杀订单是否可取消/超时未支付，取消后是否恢复秒杀库存——这会直接影响「成功订单数 ≤ 初始库存」的统计口径。

## Verification

环境：需可连接的 MySQL 8.0（`docker compose up -d`）；集成测试必须走真实 `RegisterFrontendRoutes` + `middleware.Auth` 与 `RegisterAdminRoutes` + `AdminAuth/RequirePermission`，用真实用户/管理员账号断言；并发断言需真实并发请求 + `go test -race`。

- AC-001 → 需 MySQL：管理员经真实路由创建活动，断言活动/SKU/秒杀价/秒杀库存/起止时间落库；无权限管理员返回 403 且无写入。
- AC-002 → 需 MySQL：构造「开始前」「结束后」活动，用户下单断言稳定拒绝且订单与秒杀库存均不变。
- AC-003 → 需 MySQL：下单后断言成交价 = 秒杀价而非客户端提交值/普通 SKU 价，且改秒杀价后已生成订单快照不变。
- AC-004 → 需 MySQL：同一用户第二次购买同一活动商品，断言拒绝且无写入。
- AC-005 → 需 MySQL：同一幂等键重复提交下单，断言仅一个订单、秒杀库存仅扣一次。
- AC-006/AC-007 → 需 MySQL + `-race`：并发对同一活动下单，断言秒杀库存 ≥ 0 且成功订单数 ≤ 初始库存、成功扣减总量 = 初始 − 最终。
- AC-008 → 需 MySQL：构造库存不足/超时/已购/参数非法/越权场景，断言无订单、库存不变、无半成品。
- AC-009 → 需 MySQL：构造任一步失败（如库存不足）场景，断言整体回滚。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及新数据模型（秒杀活动/秒杀库存）、新状态机（活动生命周期）与跨模块不变量（秒杀库存与普通库存、一人一单、幂等去重、时间窗的一致性模型），并发一致性（条件扣减防超卖、一人一单唯一约束、幂等去重）与事务边界；多个现实方案（秒杀库存独立 vs 复用普通库存、秒杀订单复用 `orders` vs 独立表、一人一单唯一约束作用域）会产生不同业务、可靠性与运维结果，需 Analyst 固化 contract 后由 Owner 确认。

## Analyst Questions

1. 秒杀库存建模：独立秒杀库存表（活动 × SKU 的 `quantity`/`sold`）还是复用普通 `inventories`；秒杀订单是否同时联动扣减普通库存；秒杀库存与普通库存的一致性语义。
2. 秒杀订单与普通订单的关系：复用 `orders`/`order_items`（增加活动关联字段 + 秒杀价快照）还是独立 `flash_sale_orders` 表；秒杀订单是否进入普通订单列表/支付/取消流程；「成功订单数」的统计口径与取消/超时未支付是否恢复秒杀库存。
3. 一人一单唯一约束的作用域与实现：`(activity_id, user_id)` 还是 `(activity_id, sku_id, user_id)`/`(activity_id, product_id, user_id)`；DB 唯一约束为主 vs 应用层校验 + 唯一约束兜底。
4. 幂等键与一人一单的共存：幂等键作用域（是否沿用 `(user_id, idempotency_key)`）、重复请求返回语义（返回既有订单 vs 冲突错误）、与一人一单唯一约束的分层与兜底。
5. 时间窗判定：DB 条件写入（`NOW() BETWEEN start_time AND end_time`）vs 应用层校验；起止端点的开闭语义；活动提前开始/延后结束等运营操作是否影响已下订单。
6. 活动状态机与生命周期：活动状态集合（未开始/进行中/已结束/下架等）与合法迁移；谁（权限）可创建/启用/停用活动；活动能否修改秒杀价/库存。
7. 秒杀价的定价与校验：秒杀价是否允许高于普通价、是否要求为正整数分上限；下单时以活动价为准的校验顺序（时间窗 → 库存 → 一人一单 → 价格）。
8. 全局资源：新增秒杀错误码域（语义：秒杀）、新增 migration（秒杀活动/秒杀库存等表）、可能的 `flash_sale:*` 权限 code；具体编号/版本由 Analyst 读 `.agent/registry/*` 派生。

## Review Baseline

- Base commit：`e0a48e9f023809d279e61fe7a0052a7cff461095`（分支 `feat/flash-sale-v1`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/flash-sale-v1/`、`api/flashsale*/`（或等价秒杀 API 包）、`internal/controller/flashsale*/`、`internal/logic/flashsale*/`、`internal/service` 的 `IFlashSale` 接口、`internal/codes` 秒杀域扩展、migration 文件（秒杀相关表，经既有机制新增）、`internal/boot/seed.go` 的秒杀权限 seed、`internal/cmd` 路由扩展及对应测试；`docs/design/flash-sale.md` 由 Analyst 写入。当前工作区干净，无既有未提交修改。

## Initial Route

READY_FOR_ANALYST
