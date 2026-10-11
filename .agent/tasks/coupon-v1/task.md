# Task: 优惠券（Coupon）V1 — 优惠券与普通订单优惠计算

## Goal

交付「平台通用固定金额满减券」最小完整闭环：管理员创建优惠券模板（设置满减门槛、减免金额、领取与使用有效期、发放总量，可停止新领取）→ 登录用户领取（每人每模板最多一张，总量不超发，并发不重复/不超发）→ 普通订单下单可选一张自己的券，服务端重读 SKU 价格计算原始金额、校验归属/有效期/使用状态/满减门槛后计算优惠金额与最终应付金额并写入订单快照 → 创建订单与优惠券预占在同一 MySQL 事务内完成 → Mock 支付成功核销（已预占→已使用）→ 未支付订单用户取消或超时取消时释放占用的券；已支付退款不返券。不使用优惠券时现有下单行为与金额语义保持不变。本任务只开发后端，不实现前端。

## Scope

- 优惠券数据模型：优惠券模板表 + 用户已领取优惠券实例表（含领取/预占/使用/释放状态），经既有 golang-migrate 机制新增迁移，并同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion` 与 `businessTables`（schema 由 Analyst 固化）。
- 管理员优惠券模板管理 API（接入 `AdminAuth` + `RequirePermission`，新增 `coupon:*` 权限 code 并登记 `internal/boot/seed.go`）：
  - 创建优惠券模板（满减门槛、减免金额、领取与使用有效期、发放总量）；
  - 查询优惠券模板；
  - 停止新领取（不影响已领取券，语义由 Analyst 固化）。
- 用户优惠券能力（接入现有 `Auth`，作用于 `Principal.UserID`）：
  - 领取优惠券（每用户每模板最多一张、发放总量不超发、并发不超发/不重复领取）；
  - 查询自己的优惠券及其当前可用状态；
  - 普通用户不能查看或使用他人优惠券。
- 普通订单用券与优惠计算（在现有下单流程内扩展，不改状态机、不改 IAM）：
  - 下单可选一张自己的券（一笔订单最多一张，不叠加）；
  - 服务端重读 SKU 价格 → 原始金额 → 校验券归属/有效期/使用状态/满减门槛 → 计算优惠金额 → 最终应付金额；
  - 金额统一整数分，优惠金额不大于原始金额，不信任客户端提交的优惠金额或最终金额；
  - 将金额与优惠券关联信息写入订单快照，下单后改价/改模板不改变历史订单；
  - 不使用优惠券时保持现有下单行为与金额语义兼容。
- 优惠券与订单生命周期联动：
  - 订单创建与优惠券预占在同一 MySQL 事务内；订单创建失败不残留「已预占」；
  - 一张券不能被两个不同订单同时成功占用；幂等判定考虑 coupon_id，同幂等键重试不重复占券；
  - Mock 支付成功核销；未支付订单用户取消/超时取消释放；已支付退款不返券；
  - 支付/取消/退款并发不产生非法状态组合；保持原有库存扣减与恢复正确。
- 错误码：新增优惠券错误码域（语义：优惠券），具体编号由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。
- 长期设计：新增 `docs/design/coupon.md`（Design Impact = NEW，沉淀数据模型、状态机与合法迁移、领取/用券的一致性模型、权限边界、错误码域与权限 code）；更新 `docs/design/order.md`（订单金额快照语义：original_amount / discount_amount / total_amount 的兼容方案与 Dashboard 影响）。
- 必要测试：覆盖创建/领取/查询、领取资格（未开始/已结束/已停止/超发）、并发领取不超发、重复领取、用户隔离、满减门槛、过期/已使用不能复用、金额计算、无券兼容、并发下单不重复占券、幂等重试、下单失败不残留预占、支付核销、取消/超时释放、退款不返券、改价快照不变，以及现有 Order/Dashboard/IAM 回归；并发正确性必须走真实 MySQL 事务，不允许仅 Mock 证明。

## Out of Scope

- 前端（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。
- 百分比折扣、指定商品券/品类券/品牌券、多券叠加、优惠分摊到多订单项、秒杀订单用券。
- 复杂价格/结算预览系统、结算规则引擎。
- 积分抵扣、会员折扣、自动发券、优惠券转赠、营销活动推荐。
- Redis 优惠券缓存、分布式锁。
- 真实支付/退款资金处理、真实退款到账/对账。
- 重构现有订单状态机或 IAM；修改既有购物车/库存/地址/SKU/商品/分类行为（除下单用券所需的最小只读引用与必要的库存/优惠券事务协同）。

## Milestone

Milestone: 优惠券（Coupon）V1 核心闭环（管理员模板管理 + 用户领取/查询 + 普通订单用券与优惠计算 + Mock 支付核销 / 取消退券）

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/coupon.md（新增）；docs/design/order.md（更新订单金额快照语义，兼容 original_amount / discount_amount / total_amount 与 Dashboard 口径）

## Acceptance Criteria

- [ ] AC-001（模板管理）：管理员（持 `coupon:*` 权限或超管）可创建/查询优惠券模板，设置满减门槛、减免金额、领取与使用有效期、发放总量，并可停止新领取；普通用户或无权限管理员访问被拒绝（401/403）且不产生写入。
- [ ] AC-002（领取与查询）：登录用户可领取优惠券，随后能在「我的优惠券」中查询到该券及其当前可用状态（可用/已预占/已使用等）。
- [ ] AC-003（重复领取拦截）：同一用户对同一模板最多领取一张，重复领取被稳定拒绝且不重复写入。
- [ ] AC-004（领取资格校验）：未开始、已结束、已停止领取的优惠券模板不能违规领取，返回稳定错误且无写入。
- [ ] AC-005（发放总量限制）：领取数量达到发放总量后不能再领取，返回稳定错误。
- [ ] AC-006（并发领取不超发）：并发领取同一模板时，成功发放数量 ≤ 发放总量，且同一用户至多成功一张（真实 MySQL 唯一约束/条件写入兜底）。
- [ ] AC-007（用户券隔离）：用户只能查看/使用自己的优惠券；查看或使用他人券、或不存在的券，返回稳定错误且无写入。
- [ ] AC-008（满减门槛校验）：选券下单时服务端重读 SKU 价格计算原始金额，并校验券归属、有效期、使用状态与满减门槛；未达门槛、过期、已使用等均不能使用。
- [ ] AC-009（服务端金额计算）：优惠金额与最终应付金额由服务端计算（整数分），不信任客户端提交的优惠/最终金额；优惠金额不大于原始金额。
- [ ] AC-010（无券下单兼容）：不使用优惠券时，下单行为、订单金额字段语义与现有行为保持一致。
- [ ] AC-011（金额快照不变）：订单创建后修改商品价格或优惠券模板，不影响已生成订单的金额快照（原始金额/优惠金额/应付金额均不变）。
- [ ] AC-012（订单与券同事务预占）：创建订单与优惠券预占在同一 MySQL 事务内完成；订单创建失败（含库存不足等）整体回滚，不残留「已预占」的优惠券。
- [ ] AC-013（并发下单不重复占券）：并发下多个订单尝试使用同一张券，至多一个订单成功占用该券。
- [ ] AC-014（幂等重试不重复扣券）：同一幂等键（判定考虑 coupon_id）的重试请求不产生重复订单、不重复占券。
- [ ] AC-015（Mock 支付核销）：Mock 支付成功将订单推进为已支付，同时完成优惠券核销（已预占→已使用），重复支付回调不重复核销。
- [ ] AC-016（取消/超时释放）：未支付订单用户主动取消或超时自动取消时，释放其占用的优惠券（已预占→释放），且原有库存恢复保持正确。
- [ ] AC-017（退款不返券）：已支付订单退款不返还优惠券（V1 约定），库存处理保持原有正确性。
- [ ] AC-018（并发无非法状态组合）：支付、取消、退款与优惠券状态联动在并发下不产生非法状态组合（条件状态更新 + RowsAffected 原子闸门）。
- [ ] AC-019（现有回归）：现有 Order、Dashboard、IAM、RBAC 测试全部通过。
- [ ] AC-020（长期设计一致）：`docs/design/coupon.md` 与 `docs/design/order.md` 更新完成，且与 APPROVED Contract、`.agent/registry/*`、实现三方一致（数据模型、状态机、不变量、错误码域、权限 code）。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2（Go 1.23+），分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层；优惠券模块应沿用此结构。
- **当前后端无任何优惠券代码/表**：`coupon` 关键字仅出现在 `frotend_web`/`frotend_manage` 前端模板工程的 `productCoupons` 页面及 `dist` 构建产物中（未接入后端），`internal/`、`api/` 下零实现。
- 订单金额语义（`docs/design/order.md` §2.3 + `20261001000007_orders.up.sql`）：`orders.total_amount` = Σ `order_items.price×quantity`（成交总价整数分快照），**当前无 original_amount / discount_amount 字段**；`order_items.price` = 下单时重读 `skus.price` 的快照。
- 订单幂等（INV-002）：`uk_user_idempotency(user_id, idempotency_key)` 唯一兜底 + `request_hash` 判同 key 内容一致；当前 `request_hash` 计算范围是否含 coupon_id 未核实，需 Analyst 确认幂等键纳入 coupon_id 的方式。
- 订单创建（INV-003）：单事务「写 orders + 写 order_items + 扣库存 +（购物车模式）删已购条目」，库存经 `IInventory.DeductInTx` tx 感知条件扣减（`WHERE quantity >= N` + RowsAffected）。
- 订单状态机：10待支付→20已支付→30已发货→40已收货→50已完成；旁路 10→60已取消、20→70已退款；取消/退款恢复库存（条件状态更新 + RowsAffected 原子闸门）；超时自动取消 = 后台 scanner（ticker）+ 查询/支付入口懒取消兜底。
- 后台管理鉴权（`docs/design/rbac.md`）：`AdminAuth`（认证）+ `RequirePermission(code)`（授权，`IsSuper` 放行、fail-closed）；权限 code 为 B 类 namespace，seed 于 `internal/boot/seed.go` 的 `seedPermissionList`（不占 Registry），新增 `coupon:*` 需登记。前台用户侧经 `middleware.Auth` 注入 `Principal.UserID`。
- Dashboard 金额口径（`docs/design/dashboard.md` §2.3）：`today_order_amount = SUM(orders.total_amount)` 且 `status∈{20,30,40,50}` 且 `created_at∈今日`。**若 `total_amount` 语义改为「优惠后实付」，此口径对用券订单变小、对无券订单不变**，属本次需重点分析并做兼容方案的关键依赖。
- 错误码 Registry：订单域 9000-9999 ACTIVE；下一空闲域序为 18（区间 18000-18999，因 17000-17999 已被 object-storage-upload RESERVED）。优惠券需新增独立错误码域，由 Analyst 读 `.agent/registry/error-codes.md` 派生。
- 迁移 Registry：golang-migrate，`internal/migrations/sql/{14位时间戳}_{title}.up.sql`；当前 `.agent/registry/migrations.md` 最大 version 为 `20261001000019`（iam-v5 RESERVED）。优惠券需新增迁移，由 Analyst 读 Registry 派生。
- 事实来源单一 MySQL；Redis 仅会话；无 MQ、无跨系统事务。支付为 Mock（幂等），退款仅 `paid→refunded`（管理员 `order:refund`）。

Assumption：

- 优惠券仅支持平台通用固定金额满减券（Owner 明确范围），金额整数分。
- 「停止新领取」仅阻止后续领取，不影响已领取券的使用状态（具体语义由 Analyst 核实并固化）。
- 优惠券「领取/预占/使用/释放」的具体状态集、合法迁移与事务顺序由 Analyst 基于真实源码制定，本任务不预判最终方案。
- 「未发布/未开始」对应模板是否显式引入「发布态」字段，还是仅由有效期派生，由 Analyst 核实现有模块惯例后确定。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- `total_amount` 兼容方案：新增 `original_amount`/`discount_amount` 列并保留 `total_amount` 语义（`total_amount = original - discount`）vs 重定义 `total_amount` 为实付；对历史订单与 Dashboard `today_order_amount` 的影响。
- 优惠券生命周期精确状态集与合法迁移（可用/已预占/已使用/已释放 等），以及「已预占」与订单状态解耦还是强绑定的选择。
- 幂等键纳入 coupon_id 的实现方式（并入 `request_hash` 计算 vs 新增订单字段）。
- 领取防超发/防重复的具体写入方式（唯一约束 `uk(user_id, template_id)` + 总量条件更新 + RowsAffected 的事务顺序）。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试必须走真实路由 + `middleware.Auth` 与 `AdminAuth`/`RequirePermission`，用真实用户/管理员账号断言；并发正确性 AC 必须用真实 MySQL 事务与 `-race` 验证，不允许仅 Mock 证明。

- AC-001 → 需 MySQL + Redis：管理员创建/查询模板、停止领取，断言成功且字段正确；普通用户 token/无权限管理员访问断言 401/403 且无写入。
- AC-002/AC-007 → 需 MySQL + Redis：领取后查「我的优惠券」断言状态；用户 B 查看/使用用户 A 的券断言稳定拒绝且无写入。
- AC-003/AC-004/AC-005 → 需 MySQL：重复领取、未开始/已结束/已停止/超发领取分别断言稳定拒绝。
- AC-006 → 需 MySQL + `-race`：并发领取同一模板，断言发放总量 ≤ 上限、单用户至多一张。
- AC-008/AC-009 → 需 MySQL：选券下单断言门槛/有效期/状态校验、优惠与应付金额 = 服务端计算值、优惠 ≤ 原始金额。
- AC-010/AC-011 → 需 MySQL：无券下单断言金额语义不变；下单后改价/改模板断言快照不变。
- AC-012/AC-013/AC-014 → 需 MySQL + `-race`：下单失败（如库存不足）断言券不残留「已预占」；并发下单同一张券断言至多一单占用；同幂等键重试断言不重复占券。
- AC-015/AC-016/AC-017 → 需 MySQL：Mock 支付断言核销；取消/超时断言释放且库存恢复；退款断言不返券且库存恢复正确。
- AC-018 → 需 MySQL + `-race`：支付/取消/退款并发断言无非法状态组合。
- AC-019 → `go test -p 1 ./...`（含现有 order/dashboard/iam/rbac 包）全绿。
- AC-020 → Cleaner 做 Task ↔ Contract ↔ `docs/design/*` ↔ Registry ↔ 实现 多边一致性核对。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及新数据模型（优惠券模板/用户券实例）、新状态机（领取→预占→使用/释放）、订单与优惠券的跨模块事务一致（订单创建 + 预占同事务、并发不重复占券、取消/超时释放、退款不返券）、幂等键扩展（coupon_id 参与判定）、并发领取防超发、以及 `orders.total_amount` 语义兼容方案对历史订单与 Dashboard 口径的影响；多个现实方案会产生不同的数据模型、金额语义与可靠性结果，需 Analyst 固化 contract 后由 Owner 确认。

## Analyst Questions

1. 优惠券数据模型与状态机：模板表与用户券实例表 schema；「可用→已预占→已使用/已释放」精确状态集与合法迁移；「停止新领取」「未开始/已结束」的判定字段（发布态 vs 有效期派生）。
2. 订单金额语义兼容：`original_amount`/`discount_amount`/`total_amount` 的新增/重定义方案，对历史订单、Dashboard `today_order_amount`、后台订单列表的影响与兼容。
3. 订单与券的事务顺序：创建订单 + 预占同事务的具体写入顺序与条件更新闸门；预占与订单状态强绑定还是独立状态列。
4. 幂等扩展：幂等键如何纳入 coupon_id（request_hash 计算范围 vs 新增字段），同 key 不同券的语义。
5. 并发领取防超发/防重复：唯一约束 + 总量条件更新 + RowsAffected 的具体组合与事务边界。
6. 券生命周期与订单联动：核销（支付成功）、释放（取消/超时）、不返券（退款）的触发点与条件更新；并发下无非法状态组合的保证。
7. 权限与错误语义：`coupon:*` 权限 code 命名与 seed 登记；前台领券/查券路由与后台模板管理路由；越权/不存在/重复/过期等稳定错误码划分。
8. 全局资源：新增优惠券错误码域（语义：优惠券）、新增 migration（优惠券模板表 + 用户券实例表及可能的订单字段改动）、新增 `coupon:*` 权限 code；具体域号/version 由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。

## Review Baseline

- Base commit：`15bcc4be24fd00dc7811db5e549d7ad60e1519c7`（当前检出分支 `main`，working tree clean）。
- 任务开始时已有修改：无（`git status --short` 为空）。
- 分支提示：当前检出为 `main`（非 feature 分支）；TaskBuilder 不创建/切换分支，coupon 任务的 feature 分支需由 Owner 在 Analyst/Coder 开始前准备。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/coupon-v1/`、`api/coupon*/`（或等价优惠券 API 包）、`internal/controller/coupon*/`、`internal/logic/coupon*/`、`internal/service` 的优惠券接口、`internal/codes` 优惠券域扩展、migration 文件（优惠券表及可能的 orders 字段）、`internal/boot/seed.go` 的 `coupon:*` 权限 seed、`internal/cmd` 路由扩展与下单用券逻辑及对应测试；`docs/design/coupon.md` 新增与 `docs/design/order.md` 更新由 Analyst 写入。当前工作区干净，无既有未提交修改。

## Initial Route

READY_FOR_ANALYST
