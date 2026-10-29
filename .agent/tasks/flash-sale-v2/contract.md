# Technical Contract

## Decision Status
APPROVED

## Problem

V1 用单一 MySQL 证明秒杀正确性（五个不变量：库存不为负、成功订单数 ≤ 初始库存、一人一单、失败不建单、幂等防重复扣减）。V2 在 V1 基础上引入 Redis + Lua 作为抢购热路径：活动与秒杀库存预热到 Redis，Lua 原子完成资格校验、一人一单/幂等去重与库存预扣，让售罄、穿透、未预热等无效请求在 Redis 层快速失败，减少打到 MySQL 的无效请求；MySQL 仍是最终事实来源与正确性兜底；Redis 与 MySQL 需最终一致、可对账。核心未决：Redis 预扣成功后的 MySQL 落单方式（同步 vs 异步）及降级容错语义。

## Verified Current Behavior

- VERIFIED：V1 已实现并落地 `docs/design/flash-sale.md`。抢购入口 `POST /flash-sales/:id/orders`（前台 `Auth`，作用于 `Principal.UserID`），链路 `controller/flashsale.CreateOrder → service.FlashSale().CreateOrder → logic/flashsale.CreateOrder → insertOrder`。下单 = 单事务「活动状态/时间窗（MySQL NOW()）→ FOR UPDATE 锁定绑定 → 条件扣减 `UPDATE ... WHERE sold < total_stock` + RowsAffected → 建单」，唯一约束 `uk_flash_one_per_user`/`uk_flash_idempotency`/`uk_flash_order_no` 兜底（`internal/logic/flashsale/flashsale.go`）。
- VERIFIED：五不变量与错误码域见 `docs/design/flash-sale.md` §4/§7；域 12000-12999（12001~12007）在 `.agent/registry/error-codes.md` 中 `RESERVED`（属 flash-sale-v1）。
- VERIFIED：Redis 已接入：`manifest/config/config.yaml` 的 `redis.default`，`internal/boot/boot.go` 的 `applyRedisConfig` + 启动 `g.Redis().Do(ctx,"PING")` 依赖检查；`gredis` 可用。当前业务侧仅会话使用 Redis（`internal/auth/session.go`，前缀 `iam:session:`/`iam:admin:session:`/`iam:user:`），无 Lua、无秒杀缓存。
- VERIFIED：后台任务范式 = `goroutine + time.Ticker`（`internal/cmd/cmd.go` 的 `startOrderCancelScanner`），非 MQ、非 gcron。
- VERIFIED：迁移机制 golang-migrate v4（`internal/migrations/`），当前最高 `20261001000011_flash_sale`（`RESERVED`，flash-sale-v1）；三表已含 V1 全部字段与索引，V2 不改表结构。
- VERIFIED：无 MQ、无异步队列、无 gcron 基础设施。
- UNKNOWN：无阻塞性未知项；待 Owner 决定的事项见文末。

## Recommendation

RECOMMENDATION：**同步落单**。Redis Lua 预扣（闸门）成功后，在同请求内继续 V1 的 MySQL 事务落单；MySQL 失败则补偿 Redis 预扣。一人一单/幂等去重标记在下单成功（MySQL 提交）**之后**才写入 Redis，Lua 只做快速失败检查。Redis 不可用或 Lua 失败时降级走 V1 纯 MySQL 路径。

关键取舍：同步保持「下单即成交」与「失败不建单」语义不变；收益在于售罄/穿透/已购等无效请求在 Redis 层快速失败、不再打到 MySQL，而非缩短成功请求时延（成功请求仍需 MySQL 事务）。异步落单需要 MQ/异步队列（Out of Scope），并改变「成功订单」口径与库存回滚语义、破坏 V1 不变量，故排除。

## Selected Design

- **落单方式**：同步落单。Redis Lua 预扣（闸门）通过后，在同请求内继续 V1 的 MySQL 事务落单；MySQL 失败则补偿 Redis 预扣。一人一单/幂等去重标记在下单成功（MySQL 提交）后写入 Redis，Lua 只做快速失败检查。
- **降级容错**：Redis 不可用或 Lua 执行失败时，跳过 Redis 闸门、降级走 V1 纯 MySQL 路径（正确性由 MySQL 保证）。
- **预热与一致性**：管理端创建/更新/下架活动在 MySQL 提交后同步维护 Redis 缓存（预热/失效/重载）；后台扫描器兜底回补缺失的活跃活动预热。
- **对账**：后台扫描器（goroutine + ticker，复用 `startOrderCancelScanner` 范式）按「活动 × SKU」粒度，周期将 Redis `remaining` 刷成 `total_stock − MySQL sold`；无状态、幂等、多实例安全。
- **资源**：复用错误码域 12000-12999，不新增错误码，不新增 migration，不新增持久化补偿表。

## Interfaces and Data

### 公开接口（保持不变）
- `POST /flash-sales/:id/orders`：请求/响应结构与错误码语义与 V1 一致（`{sku_id, idempotency_key}` → `FlashOrder`）。
- `POST /admin/flash-sales`、`PUT /admin/flash-sales/:id`：行为不变，但需在 MySQL 提交后同步维护 Redis 缓存（预热/失效/重载）。

### 存储约束（MySQL 不变）
- 三表 `flash_sale_activities`/`flash_sale_activity_skus`/`flash_sale_orders` 及其索引/唯一约束保持不变；不新增 migration。

### Redis Key（B 类 namespace，前缀 `flashsale:`，避免与 `iam:` 冲突）
| Key | 结构 | 语义 | TTL |
| --- | --- | --- | --- |
| `flashsale:activity:{activityID}` | Hash（status/start/end） | 活动元数据 | end_time − now + grace |
| `flashsale:stock:{activityID}:{skuID}` | 整数（remaining） | 剩余秒杀库存（预扣计数） | 同活动 |
| `flashsale:bought:{activityID}:{skuID}:{userID}` | 标记 | 一人一单（下单成功后写入） | 同活动 |
| `flashsale:idem:{userID}:{idempotencyKey}` | request_hash | 幂等（下单成功后写入） | 同活动 |
| `flashsale:soldout:{activityID}:{skuID}` | 标记 | 售罄 | 同活动 |
| `flashsale:null:{activityID}` | 标记 | 空值/负缓存（不存在/下架/未预热） | 短 TTL（如 60s） |

- TTL 关系：活动域 key 的 TTL ≤ 活动 end_time + grace（grace 为小缓冲，覆盖活动结束后在途请求），保证「结束/下架后缓存自动过期，不残留脏数据」；下架/更新时显式 DEL/重载。
- 事实来源：MySQL 为活动/库存/订单的权威事实；Redis 为加速闸门与派生缓存，最终经对账收敛到 MySQL。

### Lua 脚本边界（仅 Redis 内，不访问 MySQL）
原子完成：空值标记检查 → 活动存在/启用/时间窗检查 → 售罄检查 → 幂等检查 → 一人一单检查 → 剩余库存检查与 DECR 预扣。返回码：NOT_FOUND / NOT_IN_WINDOW / SOLD_OUT / ALREADY_PURCHASED / IDEMPOTENT_HIT / IDEMPOTENT_CONFLICT / GATE_PASSED。预扣成功 ≠ 下单成功。

## Business Invariants

- INV-001（库存不为负）：MySQL `sold ≤ total_stock` 恒成立（条件更新兜底）；Redis `remaining ≥ 0`（Lua 原子 DECR 兜底）。
- INV-002（成功订单数 ≤ 初始库存）：`COUNT(flash_sale_orders) = sold ≤ total_stock`，以 MySQL 为权威；Redis 预扣仅减少 MySQL 无效尝试，不改变最终上限。
- INV-003（一人一单）：同一 `(activity_id, sku_id, user_id)` 至多一个成功订单；MySQL `uk_flash_one_per_user` 为最终兜底，Redis `bought` 为快速失败优化（下单成功后写入，无孤儿）。
- INV-004（失败不建单）：失败请求不创建订单、不扣 MySQL 秒杀库存；Redis 预扣在 MySQL 失败时补偿回滚。
- INV-005（幂等防重复扣减）：同一 `(user_id, idempotency_key)` 至多一个订单；MySQL `uk_flash_idempotency` 为最终兜底，Redis `idem` 为快速路径。
- INV-006（Redis 预扣不超预热库存）：并发下 Redis 预扣总量 ≤ 预热库存（Lua 原子）；Redis 超卖只增加 MySQL 无效请求，不造成 MySQL 超卖。
- INV-007（对账收敛）：对账后 Redis `remaining = total_stock − MySQL sold`，Redis 预扣计数与 MySQL 最终一致。

## Failure and Consistency Semantics

- 事实来源：MySQL（活动/库存/订单权威）；Redis（加速闸门/派生缓存，非权威）。
- 成功语义：下单成功 = MySQL 事务提交（订单行存在 + sold 增加），同 V1；Redis 预扣成功仅代表「通过闸门」，不代表下单成功。
- 失败语义：时间窗不符 → 12002；库存不足/售罄 → 12003；已购 → 12004；幂等冲突 → 12005；SKU/商品不可用 → 12006；参数非法 → 12007；活动不存在/下架/未预热 → 12001（复用）；Redis 不可用或 Lua 失败 → 降级 V1 纯 MySQL 路径。
- 跨系统一致性：Redis 预扣与 MySQL 落单非原子（两步）。正常路径：MySQL 失败 → 同请求补偿 Redis（INCR remaining）；异常路径：进程崩溃导致预扣残留 → 由对账修正（remaining = total − sold）。
- 重复/重试：幂等键重复（同 hash）经 Redis 快速命中 → 读 MySQL 既有订单返回；一人一单重复经 Redis 快速拒绝 → 12004；均不重复扣减。
- 降级：Redis 不可用或 Lua 失败 → 跳过 Redis 闸门、走 V1 纯 MySQL 路径（正确性由 MySQL 保证，售罄/穿透快速失败能力在该窗口失效）；Redis 恢复后由预热/对账兜底收敛。
- 对账：后台扫描器（goroutine + ticker，复用 `startOrderCancelScanner` 范式）按「活动 × SKU」粒度，周期将 Redis `remaining` 刷成 `total_stock − MySQL sold`，并回补缺失的活跃活动预热；修正幂等、多实例并发安全（写入权威值）。

## Allowed / Forbidden Changes

- 允许：在 `internal/logic/flashsale` 新增 Redis/Lua 热路径与预热、对账、降级逻辑；在 `internal/cmd` 新增秒杀后台扫描器；新增 Redis key 工具与 Lua 脚本文件；更新 `docs/design/flash-sale.md`（Design Impact = UPDATE）。
- 禁止：修改 V1 三表结构/索引/唯一约束（无新 migration）；修改 `POST /flash-sales/:id/orders` 与后台接口的公开契约与错误码语义；改变五个 V1 不变量；引入 MQ/异步下单/gcron；修改普通订单/库存/SKU/商品/IAM 模块行为；复用或改动 `iam:` 会话 key 前缀。

## Verification Requirements

- INV-001/INV-002 → MySQL+Redis 并发集成测试（`go test -race`）：断言 `sold ≤ total_stock`、`COUNT(orders) = sold`、成功数 = 初始库存。
- INV-003/INV-005 → 重复购买/幂等重试/同键不同内容，断言 Redis 快速失败码与 MySQL 唯一约束一致。
- INV-006 → 并发抢购，断言 Redis `remaining ≥ 0` 且预扣总量 ≤ 预热库存。
- INV-007 → 构造「Redis 预扣成功但 MySQL 失败/崩溃残留」场景，断言对账后 `remaining = total − sold`。
- AC-001 → 创建/更新/下架活动，断言 Redis 缓存与 MySQL 一致、失效/清理无脏数据。
- AC-003/AC-004 → 售罄/不存在/下架/未预热，断言快速失败且不穿透 MySQL（DB 查询计数/日志佐证）。
- AC-007 → Redis 断连/EVAL 失败，断言降级走 MySQL 且不破坏不变量。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；MySQL/Redis 集成验证需容器就绪（`docker compose up -d`）。

## Open Risks

- 秒杀热路径仍依赖 MySQL 事务（成功请求时延不缩短），V2 收益集中在「无效请求快速失败」而非「成功请求加速」；若目标是降低成功请求时延，需异步落单（Scope 变更，另议）。
- 「未预热」活动在预热/回补窗口内快速失败（12001），与「不存在/下架」共用语义；若需区分可后续新增错误码（当前不做）。
- Redis 与前端会话共享同一 Redis 实例，全量 Redis 宕机时前端鉴权先失败（fail-closed），秒杀降级路径主要覆盖 Lua 错误/部分 Redis 故障。

## Owner Decision Record

- 2026-10-06，Owner 确认三项决定（均采用 Analyst 推荐）：
  1. 落单方式 = 同步落单。
  2. 降级容错 = Redis 失败时降级 MySQL。
  3. 资源 = 不新增 migration / 错误码 / 持久化补偿表。
- 决定适用范围：本任务 V2 全部实现与 `docs/design/flash-sale.md`（Design Impact = UPDATE）更新。
- 2026-10-06，P3-1 文档措辞修订（CONTRACT_REVISION，Owner ACCEPT）：将「Lua 脚本边界」检查顺序由「一人一单检查 → 幂等检查」更正为「幂等检查 → 一人一单检查」，与实现（`internal/logic/flashsale/redis.go` Lua 脚本，幂等检查先于一人一单）及 §重复/重试 语义一致；不改接口、不变量、失败语义或数据契约。
