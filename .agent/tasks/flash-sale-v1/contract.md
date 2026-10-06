# Technical Contract

## Decision Status
APPROVED

## Problem
交付「秒杀核心闭环」V1：管理员创建秒杀活动（绑定 SKU、秒杀价、秒杀库存、起止时间），登录用户在活动时间窗内以秒杀价下单，在**单一 MySQL** 上以「一人一单 + 请求幂等键 + 条件库存扣减」证明五个并发不变量（库存 ≥ 0、成功订单数 ≤ 初始库存、一人一单、失败不建单、重复请求不重复扣库存）。

核心待决（需 Owner 确认）：
1. 秒杀库存独立建模，还是复用普通 `inventories`；秒杀订单是否联动扣减普通库存。
2. 秒杀订单复用 `orders` 还是独立表；「成功订单」的统计口径（是否取消/超时未支付并恢复秒杀库存）。
3. 一人一单唯一约束的作用域（活动 × SKU × 用户，还是活动 × 用户）。
4. 时间窗开闭边界与活动状态模型。

## Verified Current Behavior
- VERIFIED：技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；事实来源单一 MySQL（Redis 仅会话），无 MQ、无异步队列、无 cron（仅普通订单超时取消 ticker 扫描）。
- VERIFIED：分层 `api/<module>/v1` → `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册）；数据访问 `g.DB().Model()`，无 dao/model 层。秒杀模块应沿用。
- VERIFIED：普通订单已有成熟范式（`internal/logic/order/order.go`）：`orders.idempotency_key` + `request_hash` + `uk_user_idempotency(user_id, idempotency_key)`；下单单事务内写 `orders` + `order_items` + `service.Inventory().DeductInTx`（`UPDATE ... WHERE quantity >= N` + `RowsAffected`）。
- VERIFIED：`IInventory` 提供 tx 感知 `DeductInTx`/`IncreaseInTx`（复用「条件更新 + RowsAffected + 流水」，不自开事务），普通库存 `inventories`（1:1 `sku_id`，`quantity ≥ 0`）。
- VERIFIED：身份域隔离：前台 `Principal{UserID, Sid}`（`middleware.Auth`）；后台 `AdminPrincipal{AdminID, Sid, IsSuper}`（`AdminAuth` + `RequirePermission`，`IsSuper` 放行、fail-closed）。
- VERIFIED：错误码集中 `internal/codes/codes.go`，当前最大域序 9（order 9000-9999），**下一空闲域 = 10000-10999（域序 10）**。
- VERIFIED：迁移 golang-migrate v4，最新 `20261001000008`（refresh_tokens），**下一 version = 20261001000009**；`serve` 不自动执行迁移。
- VERIFIED：RBAC seed（`internal/boot/seed.go` `seedPermissionList`）现含 27 个权限，秒杀写权限需新增登记。
- VERIFIED：`skus` 不含 `stock`（二态 enabled/disabled，`price` 整数分上限 99,999,999）；`products` 三态 draft/on_shelf/off_shelf；`inventories.sku_id` FK `ON DELETE RESTRICT`；`order_items.sku_id/product_id` 为软引用（快照自足）。
- UNKNOWN：无（关键事实均已从代码/文档核实）。

## Recommendation
RECOMMENDATION（默认唯一推荐，不扩大 Scope）：

1. **秒杀库存独立建模**：新增 `flash_sale_activity_skus`（活动 × SKU）承载 `flash_price`（秒杀价）+ `total_stock`（初始库存）+ `sold`（已售），剩余 = `total_stock - sold`。**V1 秒杀下单不联动扣减普通 `inventories`**；秒杀库存语义上是从普通可售库存中预分配/预留的活动配额（避免双库存池全局超卖），V1 暂不实现自动划拨（见 Selected Design 与 INV-009）。
2. **秒杀订单独立表 `flash_sale_orders`**，不复用 `orders`/`order_items`、不进入普通订单列表/支付/取消/退款流程。**V1 秒杀订单「下单即成交」**（单步：下单事务提交即成功），无待支付/取消/超时未支付/退款状态机，秒杀库存一经扣减**不因取消恢复**。由此「成功订单」= 落库的秒杀订单，`成功订单数 = 累计扣减 = sold ≤ 初始库存`，统计口径简单可验证。
3. **一人一单作用域 = `(activity_id, sku_id, user_id)`**：DB 唯一约束 `uk_flash_one_per_user` 为主兜底，应用层校验为辅；用户可在同一活动的不同 SKU 各买一单。
4. **幂等键与一人一单分层共存**：幂等键作用域 `(user_id, idempotency_key)`（秒杀订单表内，与普通订单幂等键互不影响，不同表），`uk_flash_idempotency` 兜底；同键同 `request_hash` 返回既有订单（幂等成功），同键不同 `request_hash` 返回 `12005`。
5. **时间窗**：`NOW() >= start_time AND NOW() < end_time`（左闭右开），判定与扣库存、建单处于**同一事务**的 MySQL 条件写入中（避免 Go↔MySQL 时区漂移）。开始前/结束后均稳定拒绝（`12002`）。
6. **活动状态**：`status` TINYINT，`1=enabled`（默认）/`0=disabled`（下架）；下单可售 = `status=enabled` 且时间窗命中（「已结束」由 `end_time` 派生，不存冗余终态、无后台状态翻转任务）。
7. **秒杀价**：正整数分（`> 0` 且 ≤ 99,999,999）；下单成交价 = 服务端在下单事务内重读的 `flash_price` 快照（非客户端提交价、非普通 `skus.price`）；V1 不强制校验秒杀价与普通价大小关系。
8. **下单事务**：单事务内按「时间窗校验 → 读秒杀价与 SKU/商品可用性 → 条件扣减秒杀库存（`UPDATE ... SET sold = sold + 1 WHERE ... AND sold < total_stock` + `RowsAffected`）→ 插入秒杀订单（幂等/一人一单唯一约束兜底）」，任一步失败整体回滚；幂等键命中回滚扣减后读回既有订单。
9. **全局资源**：错误码域 `12000-12999`（序 12）、migration `20261001000011`、权限 `flash_sale:create`/`flash_sale:update`（派生规则见 Registry）。

关键取舍：独立秒杀库存 + 独立订单 + 「下单即成交」使五个不变量的统计口径最简、可被并发集成测试直接证明；代价是秒杀订单与普通订单/普通库存暂不打通（取消/退款/普通库存联动留待后续），且 V1 不支持取消后恢复库存。

## Selected Design
（Owner 已确认，2026-10-05）

1. **秒杀订单「下单即成交」**：独立表 `flash_sale_orders`，单步下单事务提交即成功；V1 不做支付、取消、退款、超时恢复；秒杀库存一经扣减不因任何后续操作恢复。「成功订单」= 落库的秒杀订单。
2. **一人一单作用域 = `(activity_id, sku_id, user_id)`**：`uk_flash_one_per_user` 兜底；用户可在同一活动的不同 SKU 各买一单。
3. **秒杀库存 = 普通可售库存的预分配/预留活动配额**：运行时使用独立 `flash_sale_activity_skus`（`total_stock`/`sold`），抢购事务内不联动普通 `inventories`；但语义上秒杀配额必须已从普通可售库存划拨/预留，避免双库存池全局超卖。V1 暂不实现自动划拨，此前提为运营前置约束（见「一致性模型」与 INV-009）。
4. **时间窗 `start_time <= NOW() < end_time`**（左闭右开），判定与扣库存、建单处于同一事务。
5. **秒杀价**：正整数分（`> 0` 且 ≤ 99,999,999），下单成交价 = 服务端重读快照；V1 不强制秒杀价 ≤ 普通价。

## Interfaces and Data

### 数据模型（新增 3 表，经 migration `20261001000011`）

`flash_sale_activities`（秒杀活动）
| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| id | BIGINT UNSIGNED | 主键自增 |
| name | VARCHAR(64) | 非空 |
| status | TINYINT | 非空默认 1（1=enabled、0=disabled/下架） |
| start_time | DATETIME | 非空 |
| end_time | DATETIME | 非空，应用层校验 `end_time > start_time` |
| created_at / updated_at | DATETIME | 默认 CURRENT_TIMESTAMP |

索引：`idx_status(status)`、`idx_time(start_time, end_time)`。

`flash_sale_activity_skus`（活动 × SKU 绑定：秒杀价 + 秒杀库存）
| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| id | BIGINT UNSIGNED | 主键自增 |
| activity_id | BIGINT UNSIGNED | 非空，FK → `flash_sale_activities.id` `ON DELETE CASCADE` |
| sku_id | BIGINT UNSIGNED | 非空，软引用（无 FK，下单时经 `ISku.GetByID` 校验） |
| flash_price | INT UNSIGNED | 非空，秒杀价整数分（>0，≤99,999,999） |
| total_stock | INT UNSIGNED | 非空，秒杀初始库存（正整数） |
| sold | INT UNSIGNED | 非空默认 0，已售 |
| created_at / updated_at | DATETIME | 默认 CURRENT_TIMESTAMP |

索引：`uk_activity_sku(activity_id, sku_id)` UNIQUE、`idx_sku_id(sku_id)`。剩余库存 = `total_stock - sold`（恒 ≥ 0）。

`flash_sale_orders`（秒杀订单，软引用快照自足）
| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| id | BIGINT UNSIGNED | 主键自增 |
| order_no | VARCHAR(32) | 非空，`uk_flash_order_no` 唯一（秒杀前缀，独立于普通订单号命名空间） |
| user_id | BIGINT UNSIGNED | 非空，软引用，取自 `Principal.UserID` |
| activity_id | BIGINT UNSIGNED | 非空，软引用 |
| sku_id | BIGINT UNSIGNED | 非空，软引用 |
| product_id | BIGINT UNSIGNED | 非空，软引用 |
| sku_name | VARCHAR(128) | 非空，快照 |
| product_name | VARCHAR(128) | 非空，快照 |
| product_main_image | VARCHAR(512) | 非空默认 ''，快照 |
| flash_price | INT UNSIGNED | 非空，成交价快照 = 下单时 `flash_price` |
| quantity | INT UNSIGNED | 非空默认 1（V1 每次购买恒 1 件） |
| idempotency_key | VARCHAR(64) | 非空，客户端幂等键 |
| request_hash | VARCHAR(64) | 非空，请求指纹（sha256 hex） |
| created_at / updated_at | DATETIME | 默认 CURRENT_TIMESTAMP |

索引：`uk_flash_order_no(order_no)` UNIQUE、`uk_flash_idempotency(user_id, idempotency_key)` UNIQUE、`uk_flash_one_per_user(activity_id, sku_id, user_id)` UNIQUE、`idx_activity(activity_id)`、`idx_user(user_id)`。

### 接口

前台（`routes_frontend.go` 的 `Auth` 分组，作用于 `Principal.UserID`）：
- `POST /flash-sales/:id/orders`：秒杀下单。请求体 `{sku_id, idempotency_key}`（客户端不提交价格/身份）。响应为秒杀订单。

后台写（`routes_admin.go` 的 `AdminAuth` + `RequirePermission`）：
- `POST /admin/flash-sales` → `flash_sale:create`：创建活动（含 SKU 绑定、秒杀价、秒杀库存、起止时间）。
- `PUT /admin/flash-sales/:id` → `flash_sale:update`：更新活动（改秒杀价/库存/时间/状态/下架）。

V1 不新增前台活动列表/详情、不新增后台活动列表（非 AC 必需，可后续按需扩展）。

### 错误码（域 12000-12999，序 12）
| code | 语义 | HTTP |
| --- | --- | --- |
| 12001 | FLASH_SALE_ACTIVITY_NOT_FOUND（活动不存在或已下架） | 404 |
| 12002 | FLASH_SALE_NOT_IN_TIME_WINDOW（活动未开始或已结束） | 409 |
| 12003 | FLASH_SALE_STOCK_INSUFFICIENT（秒杀库存不足） | 409 |
| 12004 | FLASH_SALE_ALREADY_PURCHASED（一人一单，已购买） | 409 |
| 12005 | FLASH_SALE_IDEMPOTENCY_CONFLICT（同幂等键不同请求内容） | 409 |
| 12006 | FLASH_SALE_SKU_UNAVAILABLE（下单时 SKU 禁用或商品下架） | 409 |
| 12007 | FLASH_SALE_INVALID_ARGUMENT（活动参数非法：秒杀价/库存/时间/状态） | 400 |

复用：`1001`（400）、`1002`（401）、`1003`（403）、`4001`（商品不存在）、`5001`（SKU 不存在）。

### 权限（新增登记 `internal/boot/seed.go`）
- `flash_sale:create`（创建秒杀活动）
- `flash_sale:update`（更新秒杀活动）

## Business Invariants
- INV-001（库存不为负）：任何成功下单后 `total_stock - sold ≥ 0`，并发下单亦不例外（条件更新 `sold < total_stock` 兜底 + `INT UNSIGNED` 类型兜底）。
- INV-002（成功订单数 ≤ 初始库存）：`sold ≤ total_stock` 且 `COUNT(flash_sale_orders) = sold`（同事务保证订单数与扣减一致）。
- INV-003（一人一单）：同一 `(activity_id, sku_id, user_id)` 至多一个成功订单，由 `uk_flash_one_per_user` 兜底；重复购买返回 `12004` 且无写入。
- INV-004（失败不建订单）：时间窗不符/库存不足/SKU 不可用/已购/参数非法/越权等失败请求不创建订单、不扣减秒杀库存、不留半成品（单事务回滚）。
- INV-005（幂等防重复扣减）：同一 `(user_id, idempotency_key)` 至多一个订单；同键同 `request_hash` 返回既有订单、不重复扣库存；同键不同 `request_hash` 返回 `12005`（`uk_flash_idempotency` 兜底）。
- INV-006（服务端定价与快照）：成交价 = 下单时 `flash_price` 重读快照，不信任客户端价与普通 SKU 价；下单后改价不影响已生成订单。
- INV-007（事务原子性）：下单 = 单事务「时间窗校验 + 一人一单/幂等校验 + 条件扣秒杀库存 + 创建订单」，任一步失败整体回滚。
- INV-008（权限）：管理员创建/更新活动需 `flash_sale:*`（含超管）；无权限 403 且无 DB 写入；用户身份取自 `Principal.UserID`，不信任请求自带身份。
- INV-009（配额预分配前提）：秒杀库存是普通可售库存的预分配/预留活动配额；全局不超卖依赖「秒杀配额已从普通库存划拨」。V1 不实现自动划拨，此约束为运营前置约定（非机械校验）；后续引入自动划拨时转为可机械验证的不变量。

## Failure and Consistency Semantics
- 事实来源：单一 MySQL。`flash_sale_activities`/`flash_sale_activity_skus`（活动与秒杀库存事实）、`flash_sale_orders`（秒杀订单事实，软引用快照自足）、`skus`/`products`（下单时存在性与可用性事实来源，读后快照）。
- 成功语义：下单成功 = 秒杀库存 `sold` 增加 1 且秒杀订单落库，同事务提交；不代表普通库存变更（V1 不联动）、不代表可取消。
- 原子性：下单为单事务，无跨系统/跨存储，不存在部分完成或中间态残留。
- 并发：同一 SKU 并发下单由 `sold < total_stock` 条件更新 + 行锁串行化，成功扣减总量 = 初始 − 剩余、成功次数 ≤ 初始库存；一人一单与幂等由 DB 唯一约束兜底「先查再写」窗口。
- 重复/重试：幂等键重复（同 hash）返回既有订单；一人一单重复返回 `12004`；均不重复扣库存。
- 失败语义：时间窗不符 → `12002`；库存不足 → `12003`；已购 → `12004`；幂等冲突 → `12005`；SKU/商品不可用 → `12006`/`5001`/`4001`；越权 → 403；参数非法 → `12007`/`1001`；DB 技术错误 → `1000`（500），均无写入、不泄漏底层细节。
- 无超时取消/退款：V1 不引入取消/超时未支付/退款，秒杀库存不因任何后续操作恢复。
- **库存配额前提（V1 运营前置约定，非机械强制）**：秒杀库存 `total_stock` 语义上是从该 SKU 普通可售库存中「预分配/预留的活动配额」，非凭空新增可售库存；全局不超卖的前提为「秒杀配额 + 普通可售库存 ≤ 真实库存」。V1 不实现自动划拨：不自动扣减/冻结普通 `inventories` 做预留，配额划拨由运营/管理员在创建活动前完成；若未按前提预留，可能出现普通 + 秒杀双池合计超卖，属 V1 已知边界（本任务五个不变量均限定在秒杀库存域内）。

## Allowed / Forbidden Changes
- 允许：新增 `api/flashsale/*`、`internal/controller/flashsale/*`、`internal/logic/flashsale/*`、`internal/service` 的 `IFlashSale` 接口、`internal/codes` 秒杀域（12000-12999）、migration `20261001000011`（3 张秒杀表）、`internal/boot/seed.go` 的 2 个秒杀权限、`internal/cmd` 路由扩展及对应测试、`docs/design/flash-sale.md`。
- 允许：秒杀模块只读引用 `ISku.GetByID`/`IProduct.GetByID` 做存在性与可用性校验。
- 禁止：修改既有普通订单/库存/购物车/商品/SKU/身份模块的行为（含 SKU 删除、普通库存扣减、普通订单状态机）。
- 禁止：引入 Redis/Lua/MQ/令牌桶/限流/削峰/异步下单/缓存库存/活动预热。
- 禁止：秒杀下单（抢购事务）联动扣减普通 `inventories`（V1 明确不联动；秒杀配额由运营在创建活动前于普通库存侧预留，不在抢购事务内划拨）。
- 禁止：Coder 自行推断错误码编号、migration version 或权限 code（一律以本 Contract 与 Registry RESERVED 为准）。

## Verification Requirements
- INV-001/INV-002 → 需 MySQL + `-race`：并发对同一活动下单，断言最终 `total_stock - sold ≥ 0`、成功订单数 ≤ 初始库存、`成功扣减总量 = 初始 − 剩余`、`COUNT(flash_sale_orders) = sold`。
- INV-003 → 需 MySQL：同一用户第二次购买同一 `(activity, sku)`，断言拒绝（`12004`）且无订单、无库存变化。
- INV-004 → 需 MySQL：构造库存不足/时间窗外/已购/参数非法/越权/SKU 不可用场景，断言无订单、库存不变、无半成品。
- INV-005 → 需 MySQL：同一幂等键重复提交，断言仅一个订单、库存仅扣一次；同键不同 `request_hash` 返回 `12005`。
- INV-006 → 需 MySQL：下单后断言成交价 = 秒杀价（非客户端值/普通 SKU 价），改秒杀价后已生成订单快照不变。
- INV-007 → 需 MySQL：构造任一步失败（如库存不足）场景，断言整体回滚（订单与库存均不变）。
- INV-008 → 需 MySQL：真实路由 + `middleware.Auth`/`AdminAuth`/`RequirePermission`，用真实用户/管理员断言；无权限管理员返回 403 且无写入。
- AC-001 → 需 MySQL：管理员经真实路由创建活动，断言活动/SKU 绑定/秒杀价/秒杀库存/起止时间落库。
- AC-002 → 需 MySQL：构造「开始前」「结束后」活动，用户下单断言 `12002` 且订单与库存均不变。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；并发断言需 `go test -race`；migration 结构快照需同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion`、`businessTables`、`expectedSchema`。

## Open Risks
- 秒杀配额未自动划拨：V1 依赖运营前置预留；若运营未在普通库存侧预留配额，可能出现普通 + 秒杀双池合计超卖。后续任务应实现自动划拨/预留并转为机械不变量，届时走 `CONTRACT_REVISION`。
- 秒杀 SKU 绑定为软引用：SKU 被删除后活动绑定悬空，下单时由 `ISku.GetByID` 校验拒绝（`5001`），属已知可接受边界。
- 全局资源（错误码域 12000-12999、migration 20261001000011）为 `RESERVED`，需经「只改 Registry 的 commit」进入共享 `develop` 后方可进入 `IMPLEMENTING`；Feature Branch 内私留无效。
- 权限粒度（`flash_sale:create`/`update` 二权限）为推荐默认，Owner 可后续要求拆分。

## Owner Decision Record
- 2026-10-05 Owner 确认：
  1. 秒杀订单「下单即成交」，V1 不做支付、取消、退款、超时恢复（解决 task.md OPEN QUESTION「成功订单」口径与支付引用方式）。
  2. 一人一单作用域 = `(activity_id, sku_id, user_id)`。
  3. 秒杀库存独立运行，抢购事务内不联动普通 `inventories`；定义秒杀库存为普通可售库存的预分配/预留活动配额，V1 暂不实现自动划拨、此前提写入 Contract。
  4. 时间窗 `start_time <= NOW() < end_time`（左闭右开）。
  5. V1 不强制秒杀价 ≤ 普通价。
- 上述决定与 Task 兼容（解决 OPEN QUESTION 与 Analyst Questions，未改变 Goal/AC/Scope）。
