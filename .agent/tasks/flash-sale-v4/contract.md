# Technical Contract

## Decision Status
APPROVED

## Problem

秒杀 V3（Redis 闸门 + MySQL 出队表 + `goroutine+ticker` 消费者）已证明异步下单正确性，但**故障恢复能力缺失**：现有补偿为**非幂等 `INCR`**（`compensatePreDeduct`/`compensatePreDeductAndMarkers`），无持久化补偿台账；对账扫描器不覆盖已结束活动；无人工修复接口与审计记录。导致「闸门预扣→入队」「消费领取→事务提交」「终态提交→补偿」三个崩溃窗口下，补偿可能丢失或重复（超预扣），且异常请求无法人工修复、不可审计。

本任务在不改变 V1/V2/V3 五个业务不变量的事实来源（MySQL 条件扣减 + 唯一约束）前提下，补齐七类故障/恢复场景与「补偿不重不漏」。

## Verified Current Behavior

- VERIFIED：下单 = Redis Lua 闸门（`GATE_PASSED` 时原子 `DECR remaining` + `SET bought/idem` 标记）→ `INSERT flash_sale_order_requests(status=queued)` → 后台消费者 `FOR UPDATE SKIP LOCKED` 出队 → 单事务「活动校验 + 时间窗 + 条件扣 `sold` + `INSERT flash_sale_orders` + `requests.status=success`」。文件：`internal/logic/flashsale/redis.go`（Lua 脚本、`compensatePreDeduct`/`compensatePreDeductAndMarkers`）、`request.go`（`enqueue`）、`consume.go`（`consumeOne`）。
- VERIFIED：补偿为非幂等 `INCR remaining` + `DEL soldout/bought/idem`；`enqueue` 失败与 `consumeOne` 终态失败各调用一次。崩溃于「DB 终态提交后、补偿前」丢补偿；重复补偿会 `INCR` 多次造成 `remaining` 超权威值（超预扣）。无持久化台账。
- VERIFIED：对账 `ReconcileCache` 仅扫 `status=enabled AND end_time > NOW()`，公式 `remaining = total_stock - sold - inflight_queued`（`findInflightQueued` 统计 `status=queued`），无状态、幂等写入权威值。**不覆盖已结束活动**，活动域 key 仅靠 TTL（`end-now+60s`）过期，无显式终态收敛。
- VERIFIED：消费出队不区分活动是否结束；`insertOrderInTx` 内 `isInTimeWindow` 使「已结束活动的 queued 请求」落 `failed`(12002)，退避中的请求在退避到期（≤60s）后同样收敛为 `failed`。→ 在途请求在活动结束后约一个退避周期内自然落终态。
- VERIFIED：崩溃重投由「`FOR UPDATE SKIP LOCKED` + 事务回滚行回 `queued`」保证同一行只被领取一次；重复处理由 `uk_request_idempotency`/`uk_flash_idempotency`/`uk_flash_one_per_user` 兜底，只产生一次业务效果。**无需新增 processing/心跳/租约字段**。
- VERIFIED：RBAC 完整存在（`admins`/`roles`/`permissions`/`admin_roles`/`role_permissions` + `AdminAuth` + `RequirePermission`，超管 `IsSuper` 直接放行）。`internal/middleware/auth.go`、`internal/boot/seed.go`。task.md 与 `AGENTS.md §11` 的「无角色体系」表述过时。
- VERIFIED：无人工修复后台接口、无审计表（`docs/design/flash-sale.md §10` 声明死信/待修复管理接口属后续扩展）。
- VERIFIED（全局资源，`origin/develop` Registry 已核对）：migration 最新 `20261001000017` → 下一 `20261001000018`；错误码域 `12000-12999` 由 flash-sale-v1 `RESERVED`（域内 12001-12007 已用，可续 12008，无需新域 reservation）。
- UNKNOWN：无（影响设计的核心机制均有代码/表结构依据）。

## Recommendation

RECOMMENDATION：以 **MySQL 权威值收敛**取代非幂等 `INCR` 补偿，作为唯一补偿/恢复机制；并补齐「活动结束终态收敛」「人工修复 + 审计」。

1. **补偿 = 权威值收敛（幂等）**：新增 `convergeStock(activityID, skuID)`，将 Redis `remaining` 重算并 `SET` 为 `total_stock - sold - inflight_queued`（复用 `findInflightQueued`/`findBindings`），替代 `compensatePreDeduct` 里的 `INCR`；标记清除保留 `DEL`（天然幂等）。`enqueue` 失败、`consumeOne` 终态失败（failed/dead）后立即触发一次 per-sku 收敛；收敛丢失（崩溃）由对账扫描器 ≤60s 兜底。收敛幂等 → 「补偿只执行一次」升级为「执行次数无关」，天然满足「不重不漏」。
2. **重启恢复不加字段**：依赖既有事务回滚 + 唯一约束（AC-006 仅补测试）。
3. **活动结束终态收敛**：扩展对账扫描器覆盖「已结束、尚在 grace 窗口（`end_time > NOW()-grace`）」的活动；终态判定 = `COUNT(status=queued)=0`；清空后写 `remaining = total_stock - sold`（inflight=0）并清理活动域缓存（DEL activity/stock/bought/idem/soldout + 置 null 标记），使 AC-005 可观测。
4. **标记自愈**：`IDEMPOTENT_HIT`/`ALREADY_PURCHASED` 命中后经 MySQL 校验，若对应 request/订单不存在（崩溃窗口孤儿标记），清除该标记并继续入队；MySQL 唯一约束为永久兜底。消除入队崩溃导致的错误 12004/503。
5. **人工修复 + 审计**：新增 `POST /admin/flash-sales/requests/:id/repair`（仅 `dead→queued`，条件更新 `WHERE status=3` + RowsAffected），权限 `flash_sale:repair`（seed）；修复与审计记录同事务写 `flash_sale_request_audits`（append-only）；查询接口 `GET /admin/flash-sales/requests/:id/audits`。

关键取舍：权威值收敛是「最终一致」而非「立即精确补偿」——崩溃窗口下 Redis `remaining` 可能短暂偏离权威值（最多一个对账周期 ≤60s，且方向不超 `total_stock - sold`），换取**零新增热路径表写入、天然幂等、复用既有对账**。相比「补偿台账/outbox」（严格 exactly-once 但每次入队多写一行、热路径成本高、仍需处理台账自身崩溃窗口），更贴合当前「正确性优先、MySQL 权威 + Redis 最终一致」的既有哲学。

## Selected Design

（Owner 已确认，见 Owner Decision Record）

1. **补偿 = MySQL 权威值收敛（幂等，不采用 compensation outbox）**：Redis `remaining` 不再依赖非幂等 `INCR`；按 MySQL 权威事实 `total_stock`/`sold`/`inflight_queued(status=queued)` 重算目标值，幂等 `SET` 收敛；Redis marker 清理由幂等 `DEL` 完成；MySQL 条件扣减仍为最终库存 Safety Boundary。
2. **活动进行中的并发语义（Owner 补充约束）**：存在「Redis Gate 已预扣、MySQL request 尚未持久化」的短窗口，因此**不宣称** DB snapshot + `SET` 在并发 admission 下天然始终精确、绝不瞬时高估。active reconciliation 的并发语义需在实现与测试中明确；**真正精确的终态收敛在活动结束、停止新 admission 后完成**（此时 `inflight_queued=0`，`remaining = total_stock - sold` 精确成立）。
3. **人工修复范围 = 仅 `dead→queued`**：`dead` 表示技术性终止，可在故障排除后重新处理；`failed` 是业务终态，本任务不允许管理员直接重新排队；未来 `failed` 人工复核作为独立能力设计。
4. **审计表 = 专用 `flash_sale_request_audits`**：不新增通用 Audit Framework；repair 状态迁移与 audit INSERT 处于同一 MySQL 事务；至少记录 request、操作者（admin_id + username 快照）、动作、from/to status、reason、时间。
5. **错误码新增 `12008`**：表示请求当前状态不可修复；使用现有 `12000-12999` 秒杀错误码域，不新增 domain reservation。
6. **权限 = `flash_sale:repair`**：现有 RBAC 体系内新增，由 `AdminAuth` + `RequirePermission("flash_sale:repair")` 保护；不另造管理员身份体系。
7. 重启恢复不加 `processing`/心跳/租约字段（`FOR UPDATE SKIP LOCKED` + 事务回滚 + 唯一约束兜底）。
8. 对账扫描器扩展覆盖「已结束、尚在 grace 窗口」活动的终态收敛。

## Interfaces and Data

- 新表 `flash_sale_request_audits`（migration `20261001000018`，append-only）：
  - `id` BIGINT UNSIGNED PK AUTO_INCREMENT
  - `request_id` BIGINT UNSIGNED NOT NULL（软关联 `flash_sale_order_requests.id`）
  - `operator_admin_id` BIGINT UNSIGNED NOT NULL（软关联 `admins.id`）
  - `operator_username` VARCHAR(64) NOT NULL（操作时用户名快照，抗 admin 删除）
  - `action` VARCHAR(32) NOT NULL（如 `dead_to_queued`）
  - `before_status` TINYINT NOT NULL、`after_status` TINYINT NOT NULL
  - `reason` VARCHAR(255) NOT NULL（操作者填写的修复原因）
  - `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
  - KEY `idx_request_id(request_id)`、KEY `idx_operator(operator_admin_id)`
  - 无 UPDATE/DELETE 接口（只追加），实现「不可篡改」。
- 新后台 API（`internal/controller/flashsale` + `api/flashsale/v1` + `internal/cmd/routes_admin.go`）：
  - `POST /admin/flash-sales/requests/:id/repair`（body `{target_status:"queued", reason:"..."}`，仅 `dead→queued`，`reason` 必填）
  - `GET /admin/flash-sales/requests/:id/audits`
- 新权限 code `flash_sale:repair`（B 类 namespace，`internal/boot/seed.go` seed，非 Registry 资源）。
- 新错误码 `12008` `FLASH_SALE_REQUEST_NOT_REPAIRABLE`（HTTP 409，请求状态不可修复，仅 `dead` 可修复）；复用 `1004`（请求不存在）、`1001`（参数非法）、`1002/1003`（认证/授权由中间件）。
- 内部改造：`internal/service/flashsale.go` 接口新增 `RepairRequest`、`ListRequestAudits`；`internal/logic/flashsale` 补偿改权威值收敛、对账扫描器扩展结束收敛、标记命中自愈；`internal/cmd/cmd.go` 对账扫描器 scan 窗口调整。

## Business Invariants

既有 INV-001~013 不变（仍为 MySQL 权威事实来源），本任务新增：

- INV-014（补偿幂等/不重不漏）：任意时刻 Redis `remaining ≤ total_stock - sold`（MySQL 条件扣减兜底，绝不超卖）；补偿/收敛重复执行或崩溃重放不改变收敛结果（不多补、不漏补，崩溃后 ≤ 一个对账周期收敛）。活动进行中收敛目标为 `remaining = total_stock - sold - inflight_queued`，但因「Redis 预扣已发生、request 未持久化」短窗口，DB snapshot + SET 允许瞬时高估，不宣称并发 admission 下始终精确；精确终态 `remaining = total_stock - sold` 在活动结束、停止新 admission 后达成。
- INV-015（崩溃重投幂等）：消费者领取后、事务提交前崩溃，请求回到 `queued` 被重新领取重处理，只产生一次业务效果（成功订单数、`sold` 扣减各一次）。
- INV-016（活动结束收敛清理）：活动结束且该活动 `status=queued` 计数归零后，Redis `remaining = total_stock - sold`，活动域缓存被清理，无残留脏数据。
- INV-017（人工修复授权与审计原子性）：仅持有 `flash_sale:repair`（或超管）可修复，且仅 `dead→queued`；修复与审计记录同一事务提交；审计只追加、不可修改删除；越权/未授权稳定拒绝且无副作用。

## Failure and Consistency Semantics

- 事实来源：MySQL 权威（`flash_sale_activities`/`flash_sale_activity_skus`/`flash_sale_orders`/`flash_sale_order_requests`/`flash_sale_request_audits`）；Redis 为派生闸门缓存，经收敛/对账最终一致。
- 下单成功 = 消费者事务提交（`sold+1` + `flash_sale_orders` 落库 + `requests.status=success` 同一事务）；入队成功（`queued`）不代表下单成功。
- 失败：业务失败 → `failed`（终态，不自动重试）；技术失败 → 退避重试，超上限 → `dead`。两类终态失败在提交后触发收敛（释放预扣）+ DEL 标记，不建单、不扣 `sold`。
- 崩溃窗口：① 闸门通过但入队失败/崩溃 → 收敛 + DEL 标记（幂等），崩溃残留由对账/标记自愈兜底；② 领取后提交前崩溃 → 事务回滚、行回 `queued` 重投，唯一约束兜底；③ 终态提交后收敛前崩溃 → 对账 ≤60s 收敛。
- 重复/乱序/部分完成：请求无顺序语义；消费事务原子（`success` 全有或全无）；重复消费由唯一约束兜底。
- 并发语义（活动进行中）：active reconciliation 是「读 MySQL 快照 + `SET` Redis」的无事务两步，与并发 admission（`GATE_PASSED` DECR 预扣 + 待 `INSERT` request）存在竞态：若收敛发生在「预扣已发生、request 未持久化」窗口，`inflight_queued` 少计，`SET` 会瞬时高估 `remaining`（相当于把该在途预扣「回补」）。该高估不破坏 MySQL 安全边界（条件扣减 `sold < total_stock` + 唯一约束仍杜绝超卖），且随 request 持久化/下一周期收敛自愈；实现与测试须明确此语义，不得宣称并发下始终精确。真正精确终态收敛仅在活动结束、停止新 admission 后（`inflight_queued=0`）达成。
- 审计记录与修复同事务（要么都写，要么都回滚）；审计只追加，无修改/删除路径。

## Allowed / Forbidden Changes

- 允许：修改 `internal/logic/flashsale`（补偿收敛/对账结束收敛/标记自愈/修复/审计）、`internal/service/flashsale.go`、`internal/controller/flashsale`、`api/flashsale/v1`、`internal/cmd`（扫描器/路由）、`internal/boot/seed.go`（新权限）、`internal/codes`（12008）、migration（`20261001000018`）、对应测试；`docs/design/flash-sale.md`（Approved 后由 Analyst 更新）。
- 禁止：改变 `CreateOrder`/`GetOrderResult` 的公开语义与路径；改变五个核心不变量的事实来源；引入外部 MQ；改变 Redis 不可用 fail-closed 语义；修改普通订单/库存/SKU/商品/IAM 模块行为；为请求表新增 processing/心跳/租约字段；新建通用审计表；擅自新增 Registry 未声明的 resource kind。

## Verification Requirements

- INV-014 → MySQL+Redis：对同一失败重复触发补偿/收敛、构造补偿中途崩溃后重启/对账，断言 `remaining` 收敛到 `total_stock - sold - inflight_queued` 且不超 `total_stock - sold`（AC-001/AC-004）。
- INV-015 → MySQL+Redis（`go test -race`）：构造消费者领取后提交前崩溃，断言请求被重投且订单数/`sold` 只增加一次（AC-003）。
- INV-016 → MySQL+Redis：活动结束后（在途落终态后）触发结束收敛，断言 `remaining = total_stock - sold`、活动缓存清理、无脏数据，收敛结果可查询佐证（AC-005）。
- INV-017 → MySQL+Redis：管理员经 `flash_sale:repair` 修复 `dead→queued`，断言状态迁移正确且审计记录落库；越权/未登录断言 403/401 且无副作用；审计无修改/删除路径（AC-007）。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；MySQL 8.0 + Redis 容器就绪。

## Open Risks

- 权威值收敛为最终一致：读改写竞态下 `remaining` 可能短暂偏高（≤ 一个周期），但不超 MySQL 权威上限、自愈；MySQL 条件扣减仍是超卖兜底。
- 崩溃窗口孤儿 `bought` 标记在极端情况下可能造成用户短暂错误 `12004`（UX 级，非正确性），由 TTL + 命中自愈收敛。
- `AGENTS.md §11`/task.md「无角色体系」表述与实际 RBAC 不符，建议后续修正（不阻塞本任务）。

## Owner Decision Record

Owner 决策（2026-10-07，适用于本任务全部 Scope 与 AC）：

1. 补偿机制：ACCEPT「MySQL 权威值收敛」，不采用 compensation outbox。Redis `remaining` 不再依赖非幂等 `INCR`；按 MySQL 权威事实重算目标值以幂等 `SET` 收敛；marker 清理由幂等 `DEL` 完成；MySQL 条件扣减仍为最终库存 Safety Boundary。补充并发约束：活动进行中存在「Redis Gate 已预扣、MySQL request 未持久化」短窗口，不得宣称 DB snapshot + SET 在并发 admission 下天然始终精确/绝不瞬时高估；active reconciliation 并发语义需在实现与测试中明确；真正精确终态收敛在活动结束、停止新 admission 后完成。
2. 人工修复范围：ACCEPT，仅允许 `dead → queued`。`failed` 为业务终态，本任务不允许管理员直接重新排队；未来 `failed` 人工复核作为独立能力设计。
3. 审计表：ACCEPT 专用 `flash_sale_request_audits`，不新增通用 Audit Framework；repair 状态迁移与 audit INSERT 同一 MySQL 事务；至少记录 request、操作者、动作、from/to status、reason、时间。
4. 错误码：ACCEPT 新增 `12008`（请求当前状态不可修复），使用现有 12000-12999 域，不新增 domain reservation。
5. 资源：ACCEPT Contract APPROVED 后由 Analyst 按 Registry 流程预留 migration `20261001000018`（`flash_sale_request_audits`）；ACCEPT `flash_sale:repair` 作为现有 RBAC 内新增权限，由 `AdminAuth + RequirePermission("flash_sale:repair")` 保护，不另造管理员身份体系。
