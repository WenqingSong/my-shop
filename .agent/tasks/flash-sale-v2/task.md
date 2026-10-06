# Task: 秒杀（Flash Sale）V2（Redis + Lua）

## Goal

在秒杀 V1（单一 MySQL 已证明正确性）基础上引入 Redis + Lua 作为抢购热路径：活动与秒杀库存预热到 Redis，Lua 原子完成资格校验、一人一单/幂等去重与库存预扣，售罄快速失败、防缓存穿透，并提供 Redis 与 MySQL 对账方案；在保持 V1 五个业务不变量（库存不为负、成功订单数 ≤ 初始库存、一人一单、失败不建单、幂等防重复扣减）不变的前提下，让抢购请求在 Redis 层快速失败、减少打到 MySQL 的无效请求，且 Redis 与 MySQL 最终一致、可对账。

## Scope

- 活动与库存预热：活动（含 SKU 绑定、秒杀价、秒杀库存、起止时间）预热到 Redis；管理员创建/更新/下架活动时同步维护 Redis 缓存一致性（失效/重载），避免抢购读到过期活动/库存。
- Lua 原子抢购：下单热路径经 Lua 脚本原子完成资格校验（活动状态/时间窗/SKU 可用）、一人一单去重、幂等去重与库存预扣，脚本内判定不可分割。
- Redis Key 与 TTL 设计：活动/库存/去重/售罄/空值标记等 Key 的命名空间、数据结构与 TTL；活动结束或下架后缓存自动过期/清理，不残留脏数据。
- 售罄快速失败：Redis 库存耗尽后，后续抢购请求快速失败（售罄），不穿透到 MySQL 查询/扣减库存、不产生订单。
- 防缓存穿透：对不存在/已下架/未预热的秒杀活动或 SKU，请求不穿透 Redis 到 MySQL（空值/负缓存标记 + 短 TTL），快速失败。
- 业务不变量保持：Redis 预扣仅为 MySQL 的加速闸门，最终正确性仍以 MySQL 条件扣减 + 唯一约束为准；V1 五个不变量在 V2 下依然成立，并由并发集成测试证明。
- Redis 与 MySQL 对账方案：能检测并修正 Redis 预扣计数与 MySQL `sold`/订单数之间的不一致（如 Redis 预扣成功但 MySQL 下单失败），最终收敛一致。
- 容错/降级：Redis 不可用或 Lua 执行失败时系统行为可预测，且不破坏上述不变量、不产生脏数据（具体方案由 Analyst 固化）。
- 长期设计：更新 `docs/design/flash-sale.md`（Design Impact = UPDATE），沉淀 Redis Key/TTL、Lua 原子性边界、预扣与 MySQL 最终一致的一致性模型、售罄/穿透/降级语义与对账方案。
- 必要测试：并发集成测试（含 `-race`），覆盖 Lua 原子性、预扣不超卖、售罄快速失败、穿透防护、对账收敛、降级容错。

## Out of Scope

- MQ 异步下单/削峰、令牌桶/限流、验证码、风控/防机器人、黑名单。
- Redis 集群/哨兵/多级缓存/CDN/页面静态化。
- 秒杀配额从普通库存的自动划拨（V1 运营前置约定，非本任务）。
- 取消/退款/超时未支付与库存恢复（V1 已明确不做，本任务不改变）。
- 修改普通订单/库存/SKU/商品/IAM 模块行为（除秒杀所需最小只读引用）。
- 前端页面改造（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。

## Milestone

Milestone: 秒杀 V2 核心闭环（Redis + Lua）

## Design Impact

Design Impact: UPDATE
Design Artifact: docs/design/flash-sale.md

## Acceptance Criteria

- [ ] AC-001（预热与一致性）：管理员创建/更新秒杀活动后，Redis 中的活动与库存缓存与 MySQL 一致；活动下架或结束后缓存失效/清理，不残留导致错误抢购的脏数据。
- [ ] AC-002（Lua 原子校验/去重/预扣）：抢购请求经 Lua 脚本原子完成资格校验、一人一单去重、幂等去重与库存预扣；并发抢购下 Redis 预扣总量不超过预热库存（不超卖）。
- [ ] AC-003（售罄快速失败）：当秒杀库存在 Redis 中耗尽后，后续抢购请求快速失败（售罄），不穿透到 MySQL 查询/扣减库存，也不产生订单。
- [ ] AC-004（防缓存穿透）：对不存在/已下架/未预热的秒杀活动或 SKU，抢购请求不穿透 Redis 到 MySQL（空值/负缓存标记 + 短 TTL），快速失败。
- [ ] AC-005（业务不变量保持）：V2 下 V1 五个不变量依然成立——最终 MySQL 库存不为负、成功订单数 ≤ 初始库存、一人一单、失败请求不建单、重复请求不重复扣减；并发集成测试（含 `-race`）可证明。
- [ ] AC-006（对账收敛）：当 Redis 预扣计数与 MySQL `sold`/订单数出现不一致（如 Redis 预扣成功但 MySQL 下单失败）时，对账/补偿能检测并修正，最终收敛一致，不残留多扣/少扣。
- [ ] AC-007（降级容错）：Redis 不可用或 Lua 执行失败时，系统行为可预测（降级直查 MySQL 或快速失败，由 contract 固化），且不破坏不变量、不产生脏数据。
- [ ] AC-008（长期设计）：更新 `docs/design/flash-sale.md`，沉淀 Redis Key/TTL、Lua 原子性边界、预扣与 MySQL 最终一致的一致性模型、售罄/穿透/降级语义、对账方案，与 APPROVED Contract、最终实现一致。

## Relevant Context

已核实事实：

- V1 秒杀已实现并落地 `docs/design/flash-sale.md`：单一 MySQL 事实来源，五不变量（INV-001~INV-009），秒杀订单「下单即成交」无状态机；三表 `flash_sale_activities`/`flash_sale_activity_skus`/`flash_sale_orders`；错误码域 12000-12999（`12001~12007`）；权限 `flash_sale:create`/`flash_sale:update`。
- 抢购入口 `POST /flash-sales/:id/orders`（前台 `Auth`，作用于 `Principal.UserID`），链路 `controller.flashsale.CreateOrder → service.FlashSale().CreateOrder → logic.flashsale.CreateOrder → insertOrder`：单事务「活动状态/时间窗（MySQL `NOW()`）→ `SELECT ... FOR UPDATE` 锁定绑定 → 条件扣减 `UPDATE ... WHERE sold < total_stock` + `RowsAffected` → 建单」，唯一约束 `uk_flash_one_per_user`/`uk_flash_idempotency`/`uk_flash_order_no` 兜底。
- Redis 已接入：`manifest/config/config.yaml` 的 `redis.default`（环境变量可覆盖），`boot.applyRedisConfig` + 启动 `g.Redis().Do(ctx,"PING")` 依赖检查；`gredis` 可用（`g.Redis()`）。当前 Redis 仅用于会话（`internal/auth/session.go`），业务侧无 Lua、无秒杀缓存使用。
- 已有后台定时任务范式：`internal/cmd/cmd.go` 的 `startOrderCancelScanner`（`goroutine + time.Ticker`，周期配置 `order.cancel_scan_interval`），可作为对账/补偿后台任务的参照（非 MQ、非 gcron）。
- 迁移机制 golang-migrate v4：当前最新 `20261001000011_flash_sale`（`RESERVED`，属 flash-sale-v1）；错误码域 12000-12999 在 `.agent/registry/error-codes.md` 中 `RESERVED`（属 flash-sale-v1，未 `ACTIVE`）。
- 项目无 MQ、无异步队列、无 gcron 基础设施。

Assumption（合理但未经 Owner 确认，交 Analyst 核实）：

- Redis 预扣是 MySQL 的「加速闸门」：只负责在 Redis 层快速拒绝无效请求，最终正确性仍以 MySQL 条件扣减 + 唯一约束为准，不改变 V1 五不变量的事实来源。
- 对账/补偿沿用既有「`goroutine + ticker`」后台扫描范式，不引入 MQ 或 gcron。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 秒杀下单在 Redis 预扣成功后的 MySQL 落单方式：同步（同请求内事务落单，失败回滚 Redis 预扣）还是异步（预扣成功即返回，订单异步落库 + 失败补偿）——直接决定「成功订单」口径、对账窗口、库存回滚语义与用户可见响应时延。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis（`docker compose up -d`）；集成测试走真实 `RegisterFrontendRoutes` + `middleware.Auth`，用真实用户断言；并发断言需真实并发 + `go test -race`。

- AC-001 → MySQL+Redis：创建/更新活动后断言 Redis 中活动/库存与 MySQL 一致；下架/结束后断言缓存失效、无脏数据。
- AC-002 → MySQL+Redis：并发抢购，断言 Redis 预扣总量 ≤ 预热库存（不超卖）。
- AC-003 → MySQL+Redis：预置库存耗尽，断言后续请求售罄快速失败且不产生 MySQL 库存查询/订单写入（可用 DB 查询计数/日志佐证）。
- AC-004 → MySQL+Redis：请求不存在/已下架活动，断言不穿透 Redis 到 MySQL，快速失败。
- AC-005 → MySQL+Redis（+ `-race`）：并发集成测试证明五个不变量。
- AC-006 → MySQL+Redis：构造「Redis 预扣成功但 MySQL 下单失败」场景，断言对账后 Redis 与 MySQL 收敛一致。
- AC-007 → MySQL+Redis：Redis 断连/故障场景，断言降级行为可预测且不破坏不变量、无脏数据。
- AC-008 → 文档审查：`docs/design/flash-sale.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及跨系统一致性（Redis 预扣 vs MySQL 最终一致）、并发一致性（Lua 原子性、预扣不超卖）、对账/补偿可靠性、缓存穿透/击穿/降级容错；「同步 vs 异步落单」等多个现实方案会产生不同业务、可靠性与运维结果；且需修改既有秒杀一致性模型（单一 MySQL → Redis 加速闸门 + MySQL 事实来源 + 对账），必须由 Analyst 固化 Contract 后交 Owner 确认。

## Analyst Questions

1. 落单与最终一致：Redis 预扣成功后的 MySQL 落单是同步（同请求事务，失败回滚预扣）还是异步（预扣成功即返回 + 异步落单 + 失败补偿）；各自「成功订单」口径、对账窗口、库存回滚/补偿语义、用户响应时延与失败语义。
2. Lua 脚本边界与原子性：Lua 内做哪些校验（活动状态/时间窗/SKU 可用/一人一单/幂等/预扣）；去重在 Redis 用哪种结构（`SET NX`/`SADD`/hash）；幂等键与一人一单在 Redis 的 key 与 TTL；Lua 失败/超时/重试语义。
3. Redis Key 与 TTL 设计：活动/库存/去重/售罄/空值标记的命名空间（B 类 namespace，需避免与既有会话 key 冲突）、数据结构与 TTL；库存 key 与活动 key 的 TTL 关系；活动结束/下架后的失效与清理。
4. 预热时机与一致性：预热触发点（创建/更新时 vs 活动开始前定时预热 vs 懒加载 + 互斥重建）；管理端改库存/下架如何使 Redis 失效/重载；Redis 缺失/过期时如何回源（防击穿）。
5. 售罄快速失败与穿透防护：售罄标记与空值/负缓存的 key/TTL；售罄后 MySQL 侧是否仍有兜底；「不存在活动/SKU」如何不穿透。
6. 对账方案：对账粒度（活动×SKU）、触发（定时扫描 vs 事件驱动）、检测与修正（Redis 计数 vs MySQL `sold`/订单数）、幂等与多实例并发、失败与告警；是否需要持久化对账/补偿记录表（→ 可能新增 migration）。
7. 降级容错：Redis 不可用/Lua 失败时的行为（降级直查 MySQL vs 快速失败），如何不破坏不变量、不产生脏数据；降级阈值与恢复。
8. 全局资源：是否需要在秒杀域内新增错误码（缓存/降级/对账语义）与新增 migration（对账日志/补偿表）；秒杀错误码域 12000-12999 在 flash-sale-v1（`RESERVED`）与 flash-sale-v2 之间的归属/复用，由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。

## Review Baseline

- Base commit：`06bc661fc50710d782e074061c594111d2c43cde`（分支 `feat/flash-sale-v2`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增/修改产物为 `.agent/tasks/flash-sale-v2/`、`internal/logic/flashsale`、`internal/service/flashsale.go`、`internal/controller/flashsale`、Redis 相关实现（Lua 脚本、活动/库存预热、售罄/穿透/降级、对账逻辑）、`internal/codes`（如需）、`internal/boot/seed.go`（如需）、migration（如需）、`internal/cmd` 后台扫描（如需）及对应测试；`docs/design/flash-sale.md` 由 Analyst 更新。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）
