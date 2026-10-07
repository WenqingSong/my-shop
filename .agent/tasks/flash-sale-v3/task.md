# Task: 秒杀（Flash Sale）V3（异步下单）

## Goal

在秒杀 V2（Redis + Lua 同步落单，已证明正确性）基础上引入异步下单：用户秒杀下单请求通过资格/幂等/库存闸门校验后**进入异步队列并快速返回排队结果**，由后台消费者异步完成订单创建；通过消费幂等、重复消息去重、失败重试与死信/待修复队列保证可靠消费，并提供用户查询秒杀结果的能力。异步化不改变 V1/V2 的五个业务不变量（库存不为负、成功订单数 ≤ 初始库存、一人一单、失败不建单、幂等防重复扣减）及其 MySQL 事实来源兜底，只把「下单」拆成「入队快速响应 + 消费落单」两阶段。

## Scope

- 下单请求入队：登录用户在活动时间窗内的秒杀下单请求，通过既有资格/幂等/一人一单/库存闸门（Redis Lua）后进入异步队列，不再同请求内同步落单；请求快速返回「已受理/排队中」结果。
- 消费者创建订单：后台消费者从队列取消息，最终在 MySQL 创建秒杀订单，复用 V1 的条件库存扣减 + 唯一约束（`uk_flash_one_per_user`/`uk_flash_idempotency`/`uk_flash_order_no`）保证五个业务不变量在异步下依然成立。
- 消费幂等与重复消息处理：同一消息重复投递/重复消费只产生一次业务效果（不重复扣库存、不重复建单）；重复消息被识别并安全去重。
- 失败重试与死信/待修复队列：消费者处理失败按既定策略重试；超过重试上限的消息进入死信/待修复队列，不再无限重试，且可通过既定途径修复/重新处理，不丢失、不静默丢弃。
- 用户查询秒杀结果：用户可查询其秒杀下单的处理结果（状态集合与终态由 contract 固化），查询结果与最终落库事实一致，归属隔离（仅本人可查）。
- 补偿与降级：入队成功但消费落单失败时正确补偿（如回滚 Redis 预扣、清除中间态）；队列不可用/入队失败时行为可预测且不破坏不变量（具体方案由 contract 固化）。
- 长期设计：更新 `docs/design/flash-sale.md`（Design Impact = UPDATE），沉淀异步队列模型、秒杀订单状态机/「成功订单」口径、消费幂等与重复消息/重试/死信语义、跨系统一致性（队列 vs MySQL）与补偿、降级语义。
- 必要测试：并发与重试/死信/幂等集成测试（含 `-race`），覆盖五个不变量在异步下的保持、重复消息去重、重试/死信、失败补偿与结果查询。

## Out of Scope

- 令牌桶/限流、验证码、风控/防机器人、黑名单（V2 已排除，本任务不引入）。
- Redis 集群/哨兵/多级缓存/CDN/页面静态化。
- 秒杀配额从普通库存的自动划拨（V1 运营前置约定，非本任务）。
- 取消/退款/超时未支付与库存恢复（V1 已明确不做，本任务不改变）。
- 修改普通订单/库存/SKU/商品/IAM 模块行为（除秒杀所需最小只读引用）。
- 前端页面改造（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。

## Milestone

Milestone: 秒杀 V3 异步下单闭环

## Design Impact

Design Impact: UPDATE
Design Artifact: docs/design/flash-sale.md

## Acceptance Criteria

- [ ] AC-001（入队与快速响应）：登录用户在活动时间窗内提交秒杀下单，系统通过资格/幂等/一人一单/库存闸门校验后，将下单请求进入异步队列并快速返回「已受理/排队中」结果，不等待订单实际创建完成；响应时延不依赖消费者处理。
- [ ] AC-002（消费者落单与不变量保持）：后台消费者从队列消费消息并最终在 MySQL 创建秒杀订单（复用条件扣减 + 唯一约束）；V1/V2 五个业务不变量（库存不为负、成功订单数 ≤ 初始库存、一人一单、失败不建单、幂等防重复扣减）在异步下依然成立，并发集成测试（含 `-race`）可证明。
- [ ] AC-003（消费幂等与重复消息去重）：同一消息被重复投递/重复消费时只产生一次业务效果（不重复扣减库存、不重复创建订单）；重复消息被识别并安全忽略或返回既有结果。
- [ ] AC-004（失败重试与死信/待修复）：消费者处理失败时按既定策略自动重试；超过重试上限的消息进入死信/待修复队列，不再无限重试，且可通过既定途径被修复或重新处理，不丢失、不静默丢弃。
- [ ] AC-005（结果查询与归属隔离）：登录用户可查询其秒杀下单的处理结果（排队中/成功/失败等状态，由 contract 固化），查询结果与最终落库事实一致；仅本人可查，越权返回稳定错误（404/403）且无副作用。
- [ ] AC-006（失败补偿与无残留）：入队成功但最终落单失败（如库存不足/时间窗结束/已购/SKU 不可用/参数非法）时，系统正确补偿（如回滚 Redis 预扣、清除中间态），不残留半成品订单、不造成超卖或多扣。
- [ ] AC-007（队列不可用/降级）：队列不可用或入队失败时，系统行为可预测（由 contract 固化，如快速失败或降级），且不破坏业务不变量、不产生脏数据。
- [ ] AC-008（长期设计）：更新 `docs/design/flash-sale.md`，沉淀异步队列模型、秒杀订单状态机/「成功订单」口径、消费幂等与重复消息/重试/死信语义、跨系统一致性（队列 vs MySQL）与补偿、降级语义，与 APPROVED Contract、最终实现一致。

## Relevant Context

已核实事实：

- V1/V2 秒杀已实现并落地 `docs/design/flash-sale.md`：Redis Lua 闸门（资格/时间窗/一人一单/幂等/库存预扣）+ MySQL 单事务同步落单；三表 `flash_sale_activities`/`flash_sale_activity_skus`/`flash_sale_orders`；`flash_sale_orders` **无状态字段，行存在即成功订单**（「下单即成交」，无支付/取消/退款状态机）；错误码 12001~12007；权限 `flash_sale:create`/`flash_sale:update`。
- 下单入口 `POST /flash-sales/:id/orders`（前台 `Auth`，作用于 `Principal.UserID`），链路 `controller.flashsale.CreateOrder → service.FlashSale().CreateOrder → logic.flashsale.CreateOrder`（`internal/logic/flashsale/flashsale.go`），当前为同步落单。
- 项目**无 MQ、无异步队列、无 gcron**；Redis 已接入（会话 + 秒杀闸门），后台已有 `goroutine + ticker` 扫描器范式（`internal/cmd/cmd.go` 的 `startOrderCancelScanner`/`startFlashSaleReconcileScanner`）。
- `go.mod` 中 `github.com/redis/go-redis/v9` 已作为间接依赖存在（经 gogf redis contrib），Redis Streams/consumer group/List 可作为候选队列载体而不引入全新外部 MQ 依赖。
- 错误码域 12000-12999 在 `.agent/registry/error-codes.md` 中为 flash-sale-v1 `RESERVED`；migration 最新 `20261001000014`（banners）。
- 秒杀订单无「排队/处理中/失败」状态，异步化需引入订单处理状态（数据模型或状态机变更，属关键设计选择）。

Assumption（合理但未经 Owner 确认，交 Analyst 核实）：

- 异步化不改 V1/V2 五个业务不变量的事实来源（仍以 MySQL 条件扣减 + 唯一约束兜底），只是把「下单」拆成「入队快速响应 + 消费落单」两阶段；「成功订单」口径与秒杀订单状态机需重新定义。
- 复用现有 Redis 作为队列载体是候选之一（不引入全新 MQ 依赖），最终选型由 Analyst 比较后交 Owner 决定。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 队列基础设施选型（Redis Streams/List vs DB 出队表 vs 引入外部 MQ）直接决定可靠性（at-least-once/exactly-once）、顺序性、多实例并发与运维成本，且会产生不同业务/可靠性结果。
- 「成功订单」口径与秒杀订单状态机（排队/处理中/成功/失败/死信）直接决定结果查询语义、库存/一人一单/幂等在异步下的时点与 V1「行存在即成功订单」不变量如何迁移。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis（`docker compose up -d`）；集成测试走真实 `RegisterFrontendRoutes` + `middleware.Auth`，用真实用户断言；并发/重试/死信断言需真实并发 + `go test -race`。

- AC-001 → MySQL+Redis：下单请求断言快速返回「已受理/排队中」，且此时订单未同步落库（可断言订单表无成功态记录或状态为排队）。
- AC-002 → MySQL+Redis（+ `-race`）：并发入队后等待消费，断言最终 MySQL 成功订单数 ≤ 初始库存、库存 ≥ 0、一人一单、幂等扣减一次。
- AC-003 → MySQL+Redis：重复投递同一消息，断言只产生一次业务效果（订单数/库存扣减不变）。
- AC-004 → MySQL+Redis：构造消费失败，断言按策略重试；超过上限后进入死信/待修复且不再无限重试，可被修复重新处理。
- AC-005 → MySQL+Redis：下单后查询结果，断言状态与最终落库一致；他人查询/未登录断言稳定拒绝。
- AC-006 → MySQL+Redis：构造入队成功但落单失败（库存不足/时间窗结束/已购等），断言 Redis 预扣回滚、无半成品订单、不超卖。
- AC-007 → MySQL+Redis：队列不可用/入队失败，断言降级行为可预测且不破坏不变量、无脏数据。
- AC-008 → 文档审查：`docs/design/flash-sale.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及 MQ/队列可靠性与跨系统一致性（队列 vs MySQL）、消费幂等/重复消息/重试/死信、订单状态机与「成功订单」口径的重定义、失败补偿与降级；「队列选型（Redis Streams vs DB 出队表 vs 外部 MQ）」与「同步 → 异步」等多个现实方案会产生不同业务、可靠性与运维结果；且需修改既有秒杀一致性模型与订单语义，必须由 Analyst 固化 Contract 后交 Owner 确认。

## Analyst Questions

1. 队列基础设施选型：Redis Streams/List（复用现有 Redis）vs DB 出队表（polling，无新依赖）vs 引入外部 MQ；各自的可靠性（at-least-once/exactly-once）、顺序性、消费者组/多实例并发语义、运维与依赖成本。
2. 秒杀订单状态机与「成功订单」口径：异步下 `flash_sale_orders` 是否新增处理状态（排队/处理中/成功/失败/死信）；「成功订单」如何定义（落库即成功 vs 额外状态）；V1「行存在即成功订单」不变量如何迁移；结果查询的状态集合与终态。
3. 闸门/预扣时点与补偿：Redis Lua 闸门与库存预扣在入队时做还是消费时做；入队后消费落单失败如何补偿预扣；一人一单/幂等标记在异步下的写入时点与孤儿风险；SKU/商品快照在入队时还是消费时解析。
4. 消费幂等与消息去重边界：消息唯一标识（msg id/dedup key）与业务幂等键（idempotency_key/request_hash/一人一单）的关系；重复投递与重复消费的去重边界（消费者侧去重 vs 业务侧唯一约束兜底）。
5. 重试与死信策略：重试次数/退避/可重试错误分类（技术错误 vs 业务失败）；死信/待修复队列的载体、进入条件、人工/系统修复路径；不丢失、不静默丢弃的保证与观测（日志/指标）。
6. 结果查询契约：查询接口 path/入参/出参状态集合；归属隔离（仅本人）；与最终落库事实的一致性；不存在/未入队时的语义。
7. 降级容错：队列不可用/入队失败时的行为（快速失败 vs 降级同步落单），如何不破坏不变量、不产生脏数据；恢复后的收敛。
8. 全局资源：是否需新增错误码（排队/处理中/失败/死信语义）——复用秒杀域 12000-12999（flash-sale-v1 已 RESERVED）内编号，还是现有 12001~12007 足够；是否需新增 migration（队列表或秒杀订单状态/结果字段）。由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。

## Review Baseline

- Base commit：`d8438b4938a9b6b2e2f82fb87d9c6c9a60b6662b`（分支 `eat/flash-sale-v3`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空；local == `origin/eat/flash-sale-v3`）。
- 重叠修改的区分方式：本任务新增/修改产物为 `.agent/tasks/flash-sale-v3/`、`internal/logic/flashsale`（异步入队/消费/幂等/重试/死信/补偿/查询）、`internal/service/flashsale.go`、`internal/controller/flashsale`、`api/flashsale/v1`（如新增查询接口）、队列相关实现、`internal/codes`（如需）、migration（如需）、`internal/cmd` 后台消费者（如需）及对应测试；`docs/design/flash-sale.md` 由 Analyst 更新。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）
