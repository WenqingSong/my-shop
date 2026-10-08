# Task: 秒杀（Flash Sale）V4（故障恢复）

## Goal

在秒杀 V3（Redis 闸门 + MySQL 出队表 `flash_sale_order_requests` + `goroutine+ticker` 消费者）已证明异步下单正确性的基础上，补齐**故障恢复**能力：覆盖「闸门预扣成功但入队失败」「入队成功但订单写入失败」「消费者处理中崩溃后重投」「库存补偿只执行一次」「活动结束后 Redis/MySQL 对账」「服务重启后 pending 请求恢复」以及「人工修复入口 + 审计记录」七类故障/恢复场景，保证任意崩溃或重试下五个业务不变量（库存不为负、成功订单数 ≤ 初始库存、一人一单、失败不建单、幂等防重复扣减）与「补偿不重不漏」依然成立，且异常可观测、可修复、可审计。

## Scope

- 入队失败补偿与崩溃恢复：Redis 闸门通过（已预扣 `remaining` + 写入 `bought`/`idem` 标记）但 request 落库失败（`INSERT flash_sale_order_requests` 失败或崩溃）时，正确补偿预扣/标记且**只补偿一次**，不留「已预扣但无 request」残留，不多补导致超预扣。
- 消费落单失败补偿：request 已落库但消费者订单写入失败（业务失败 → `failed`、技术失败重试超限 → `dead`）时，不建成功订单、不扣 `sold`，Redis 预扣/标记被补偿，无半成品、不超卖。
- 处理中崩溃重投幂等：消费者领取请求后、事务提交前崩溃，该 `queued` 请求被重新领取重处理，只产生一次业务效果（不重复扣库存、不重复建单）。
- 库存补偿只执行一次：任何失败补偿（预扣回补 + 标记清除）幂等，同一失败只补偿一次；重复触发/重试/崩溃恢复不造成 `remaining` 超过权威值（超预扣）。
- 活动结束后对账：活动结束后（含在途请求全部落终态后），Redis 计数与 MySQL 事实收敛一致（`remaining = total_stock - sold`，无在途残留），活动缓存正确清理、不残留脏数据。
- 服务重启后 pending 恢复：服务重启后 `queued`（含退避中）请求被消费者继续处理，不丢失、不重复；崩溃残留的异常请求被收敛到正确终态。
- 人工修复入口 + 审计记录：管理员可通过后台接口人工修复异常请求（如 `dead→queued` 重处理），修复操作产生审计记录（操作者/时间/对象/动作/前后状态），审计记录可查询且不可篡改；越权拒绝且无副作用。
- 长期设计：更新 `docs/design/flash-sale.md`（Design Impact = UPDATE），沉淀故障恢复模型（崩溃窗口、幂等补偿、对账收敛、重启恢复、人工修复与审计）。
- 必要测试：覆盖七类故障/恢复场景的集成测试（含 `-race`），证明五个不变量与「补偿不重不漏」在崩溃/重试下依然成立。

## Out of Scope

- 令牌桶/限流、验证码、风控/防机器人、黑名单（V1/V2/V3 已排除）。
- Redis 集群/哨兵/多级缓存/CDN/页面静态化；引入外部 MQ（V3 已选 MySQL 出队表，本任务不改变）。
- 秒杀配额从普通库存的自动划拨、取消/退款/超时未支付与库存恢复（V1 已明确不做）。
- Redis outage fallback（Redis 不可用时的容量保护/降级）：V3 采用 fail-closed，本任务不改为「同步落单绕过闸门」。
- 修改普通订单/库存/SKU/商品/IAM 模块行为（除秒杀所需最小只读引用）。
- 前端页面改造（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。

## Milestone

Milestone: 秒杀 V4 故障恢复闭环

## Design Impact

Design Impact: UPDATE
Design Artifact: docs/design/flash-sale.md

## Acceptance Criteria

- [ ] AC-001（入队失败补偿与崩溃恢复）：闸门通过（Redis 已预扣 + 写标记）但 request 落库失败时，系统补偿 Redis 预扣/标记且只补偿一次，无「已预扣但无 request」残留；若补偿过程崩溃，重启后或一个对账周期内收敛，`remaining` 不超权威值（不多补）。
- [ ] AC-002（消费落单失败补偿）：request 已落库但订单写入失败（业务失败/技术失败）时，不创建成功订单、不扣减 `sold`，request 落 `failed`/`dead`，Redis 预扣/标记被补偿，无半成品订单、不超卖。
- [ ] AC-003（处理中崩溃重投幂等）：消费者领取请求后、事务提交前崩溃，该请求被重新领取重处理，只产生一次业务效果（不重复扣库存、不重复建单），最终库存与成功订单数一致。
- [ ] AC-004（补偿只执行一次）：任何失败补偿（预扣回补 + 标记清除）幂等，同一失败只执行一次；重复触发/重试/崩溃恢复不造成 `remaining` 超过权威值（超预扣）。
- [ ] AC-005（活动结束后对账）：活动结束后，Redis 计数与 MySQL 事实收敛一致（`remaining = total_stock - sold`，无在途残留），活动缓存正确清理、不残留导致错误抢购的脏数据；收敛结果可观测/可验证。
- [ ] AC-006（服务重启后 pending 恢复）：服务重启后，`queued`（含退避中）请求被消费者继续处理，不丢失、不重复；崩溃残留的异常请求被收敛到正确终态。
- [ ] AC-007（人工修复入口 + 审计）：管理员可通过后台接口人工修复异常请求（如 `dead→queued` 重处理），修复操作产生审计记录（操作者/时间/对象/动作/前后状态），审计记录可查询且不可篡改；越权/未授权返回稳定错误且无副作用。
- [ ] AC-008（长期设计）：更新 `docs/design/flash-sale.md`，沉淀故障恢复模型（崩溃窗口、幂等补偿、对账收敛、重启恢复、人工修复与审计），与 APPROVED Contract、最终实现一致。

## Relevant Context

已核实事实：

- V1/V2/V3 已实现并落地 `docs/design/flash-sale.md`。V3 采用 **MySQL 出队表** `flash_sale_order_requests`（migration `20261001000015`）承载异步请求生命周期（`queued`/`success`/`failed`/`dead` 四态），消费者复用 `goroutine + ticker` 扫描范式（`internal/cmd/cmd.go` 的 `startFlashSaleConsumeScanner`，默认 1s 周期，`FOR UPDATE SKIP LOCKED` 出队），对账扫描器 `startFlashSaleReconcileScanner`（默认 60s）按 `remaining = total_stock - sold - inflight_queued` 收敛。
- **Owner 需求中的「消息」术语对应 V3 架构**：「消息」= `flash_sale_order_requests` 的一行（出队行）；「消息发送失败」= 闸门通过后 `INSERT flash_sale_order_requests` 失败；「确认(ack)」= 消费者事务提交（无独立 ack 步骤，`success` 与成功订单创建同一 MySQL 事务）；「重投」= 事务回滚后该 `queued` 行被再次 `FOR UPDATE SKIP LOCKED` 领取；「Pending 消息」= `status=queued` 的请求行。项目**无外部 MQ**，本任务不引入。
- 现有补偿实现为**非幂等 INCR**（`internal/logic/flashsale/redis.go` 的 `compensatePreDeduct`/`compensatePreDeductAndMarkers`）：入队失败（`request.go` 的 `enqueue`）与消费终态失败（`consume.go` 的 `consumeOne`，事务提交后）各调用一次；`INCR remaining + DEL soldout/bought/idem`。**崩溃于「DB 终态提交后、补偿执行前」会丢失补偿；重复补偿会 INCR 多次造成超预扣**，无持久化补偿台账。
- 消费出队与落单在同一事务内（`consumeOne` 的 `g.DB().Transaction`），`FOR UPDATE SKIP LOCKED` + 状态原子更新保证同一行只被领取一次；事务回滚后行自动回到 `queued`，重处理由唯一约束（`uk_flash_one_per_user`/`uk_flash_idempotency`/`uk_flash_order_no`）兜底幂等。
- 死信修复当前仅提供「运维 SQL/日志」最小满足（`docs/design/flash-sale.md` §10 已声明「死信/待修复管理接口属后续按需扩展」），**无人工修复后台接口、无审计记录表**。
- 对账/补偿均**无持久化记录表**，不一致通过「把 Redis 刷成 MySQL 事实」收敛，观测依赖日志（`docs/design/flash-sale.md` §5.5）。
- 错误码域 12000-12999 在 `.agent/registry/error-codes.md` 为 flash-sale-v1 `RESERVED`；migration 最新 `20261001000017`（articles）。
- 权限模型仅有登录认证 + `RequirePermission`（无角色体系，见 `AGENTS.md` §11）；人工修复属管理写操作，需新增权限 code（B 类 namespace，非 Registry 资源）。

Assumption（合理但未经 Owner 确认，交 Analyst 核实）：

- 「补偿只执行一次」可通过「补偿台账/outbox 表（MySQL 权威、处理标记）」或「放弃 INCR、改为对账权威值收敛（天然幂等）」实现，最终方案由 Analyst 比较后交 Owner 决定，不改变五个业务不变量的事实来源（仍为 MySQL 条件扣减 + 唯一约束）。
- 审计记录为新增表（软关联 `flash_sale_order_requests`/操作者），只追加、不可修改删除；具体 schema 与保留策略由 Analyst 固化。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 补偿幂等与崩溃恢复机制（关键）：现有 INCR 补偿不幂等、崩溃会丢补偿/重复补偿超预扣；需覆盖「闸门预扣→入队」「消费领取→事务提交」「终态提交→补偿」三个崩溃窗口，在「补偿台账/outbox」vs「权威值收敛」vs「幂等标记」间选择。
- 活动结束后的对账触发与终态判定：活动结束 + grace 后如何判定「在途请求已全部落终态」，何时把 `remaining` 安全刷成 `total_stock - sold`（不再含 `inflight_queued`），如何清理缓存并确认无残留。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis（`docker compose up -d`）；集成测试走真实 `RegisterFrontendRoutes` + `middleware.Auth` 与 `RegisterAdminRoutes` + `AdminAuth/RequirePermission`，用真实用户/管理员断言；崩溃/重投/补偿断言需真实并发 + `go test -race`，并可注入「事务提交后、补偿前」等崩溃点或直接构造异常中间态。

- AC-001 → MySQL+Redis：构造闸门通过但 `INSERT flash_sale_order_requests` 失败，断言 Redis `remaining` 回补、`bought`/`idem`/`soldout` 清除、无「已预扣无 request」残留；构造补偿中途崩溃，断言重启/对账后 `remaining` 收敛到权威值且不多补。
- AC-002 → MySQL+Redis：构造消费落单业务失败/技术失败，断言无成功订单、`sold` 不变、request 落 `failed`/`dead`、Redis 预扣/标记被补偿。
- AC-003 → MySQL+Redis（+ `-race`）：构造消费者领取后事务提交前崩溃，断言请求被重投重处理且只产生一次业务效果（订单数/库存扣减不变）。
- AC-004 → MySQL+Redis：对同一失败重复触发补偿/崩溃恢复，断言 `remaining` 不超权威值（`total_stock - sold - inflight`），无超预扣。
- AC-005 → MySQL+Redis：活动结束后（在途请求落终态后）触发对账，断言 Redis `remaining = total_stock - sold`、活动缓存清理、无脏数据；可查询对账结果佐证。
- AC-006 → MySQL+Redis：服务重启后断言 `queued`（含退避中）请求被继续消费，不丢失、不重复；异常残留请求收敛到正确终态。
- AC-007 → MySQL+Redis：管理员经后台接口修复异常请求，断言状态正确迁移（如 `dead→queued`）且产生审计记录；越权/未登录断言稳定拒绝且无副作用；审计记录不可篡改。
- AC-008 → 文档审查：`docs/design/flash-sale.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及跨系统（Redis 预扣 vs MySQL 出队/订单）一致性下的崩溃恢复与补偿幂等、对账收敛、重启恢复与人工修复/审计；「补偿台账/outbox vs 权威值收敛」等多个现实方案会产生不同业务、可靠性与运维结果；且需修改既有秒杀一致性模型（补偿、对账、死信修复语义）与新增数据模型（审计表），必须由 Analyst 固化 Contract 后交 Owner 确认。

## Analyst Questions

1. 补偿幂等与崩溃恢复机制：现有 INCR 补偿不幂等、崩溃会丢补偿/重复补偿超预扣；在「补偿台账/outbox 表（MySQL 权威、处理标记、只执行一次）」vs「权威值收敛（放弃 INCR、刷成 total-sold-inflight，天然幂等）」vs「幂等标记」之间选择；明确覆盖「闸门预扣→入队」「消费领取→事务提交」「终态提交→补偿」三个崩溃窗口的收敛方式。
2. 崩溃中间态与重启恢复：是否需要持久化 processing/心跳/租约字段或超时回收，把「已领取但未提交」的请求在重启后收敛；`FOR UPDATE SKIP LOCKED` 事务回滚已天然释放行锁，需确认无残留 Redis 预扣。
3. 活动结束后对账的触发与终态判定：活动结束 + grace 后如何判定「在途请求已全部落终态」，何时把 `remaining` 安全刷成 `total_stock - sold`（不再含 `inflight_queued`），如何清理缓存与确认无残留。
4. 人工修复入口形态：后台 admin API 的 path/权限 code/入参出参、可修复状态集合（`dead→queued` 重处理？`failed` 复核？）、与结果查询/审计的一致性；权限模型（仅登录认证 + `RequirePermission`，无角色体系）下如何界定「管理员」。
5. 审计记录数据模型：新表 schema（字段/唯一性/与 request 关联/保留策略）、谁可查、是否可删除（不可篡改约束）；是否新建秒杀审计表 vs 复用通用审计。
6. 全局资源：审计表/补偿台账表需新增 migration（1~2 个，语义：审计记录/补偿台账）；人工修复接口如需新错误码，在秒杀域 12000-12999（flash-sale-v1 已 RESERVED）内派生（无需新域 reservation）；具体编号/版本由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。

## Review Baseline

- Base commit：`1543ecb3c97d55e3bfe92ba1de548c8d183e766a`（分支 `feat/flash-sale-v4`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空；local == `origin/feat/flash-sale-v4`）。
- 重叠修改的区分方式：本任务新增/修改产物为 `.agent/tasks/flash-sale-v4/`、`internal/logic/flashsale`（补偿/对账/重启恢复/人工修复/审计）、`internal/service/flashsale.go`、`internal/controller/flashsale`（如需人工修复接口）、`api/flashsale/v1`（如需）、`internal/codes`（如需）、migration（审计/补偿台账，如需）、`internal/cmd`（后台扫描扩展，如需）、`internal/boot/seed.go`（新权限 seed，如需）及对应测试；`docs/design/flash-sale.md` 由 Analyst 更新。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）
