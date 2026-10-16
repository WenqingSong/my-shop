# Task: Kafka 改造前不变量回归（kafka-invariant-regression-v1）

## Goal

把现有秒杀业务不变量转化为能够检测回归的真实测试：在 Task 01（`kafka-migration-baseline-v1`）已固化的基线与既有测试之上，逐项核对并补齐关键正确性/一致性不变量的覆盖缺口（库存不超卖、一人一单、幂等、请求状态与订单事务一致、Redis 预扣与 MySQL 在途对账、失败事务无半成品、人工恢复不破坏成功订单），为适用不变量建立并发验证，并证明关键测试能在对应业务实现被破坏时失败（能识别错误实现），从而形成后续 Kafka 改造共同使用的正确性回归防线。整个过程不改变既有公开业务行为。

## Scope

- 建立「受保护业务不变量 → 测试入口」映射清单：逐项核对 INV-001~021 中与正确性/一致性相关的部分在现有测试中的覆盖情况，明确哪些已有、哪些缺失、哪些已具备「能识别错误实现」的判别力、哪些缺并发验证。
- 补充缺失的关键回归测试，至少覆盖：并发库存不超卖与成功订单唯一性、请求状态（`success`/`failed`/`dead`）与订单事务一致性、失败/崩溃事务不留部分订单、Redis 预扣与 MySQL 在途请求对账收敛、人工修复（`dead→queued`）不破坏既有成功订单。
- 为适用的不变量建立并发验证（库存不超卖、一人一单、幂等、消费器并发领取唯一性等，按 Analyst 确认的清单）。
- 证明关键不变量测试能识别错误业务实现（以可控、可逆的 mutation 验证测试判别力；mutation 仅在验证期间临时存在，不进入交付）。
- 固化可重复执行的回归验证命令（含 `-race`），作为 Kafka 改造前后对比的正确性回归入口。

## Out of Scope

- 不实现 Kafka 改造本身：不引入、不接入 Kafka/MQ，不改动异步消费与库存对账的实现路径（属后续任务）。
- 不改变任何生产代码、配置、数据模型、错误码或公开接口；仅新增测试文件与必要的测试辅助（测试专用 hook/工具不得改变生产行为、生产环境恒为 no-op）。
- 不改动 `docs/design/*`（Design Impact = NONE）；秒杀业务语义以现有 `docs/design/flash-sale.md` 为准。
- 若分析发现现有业务规则本身需要变化，走 Contract Revision / Task 更新，不允许借「补测试」直接改变业务语义。
- 不做容量/性能压测基线对比（属 `flash-sale-v5` 已交付产物，本任务只做正确性回归，不新增/重跑为验收必需项）。
- 不新增 migration、不占用错误码域（无全局资源预留需求）。

## Acceptance Criteria

- [ ] AC-001（并发不超卖 + 成功订单唯一性，INV-001/002/003/005）：并发下单（需真实 MySQL/Redis + `-race`）后，数据库 `sold ≤ total_stock` 且 `sold ≥ 0`，成功订单数 = `sold`；同一 `(activity_id, sku_id, user_id)` 至多一个成功订单（一人一单），同一 `(user_id, idempotency_key)` 至多一个成功订单（幂等），唯一约束兜底不违例。
- [ ] AC-002（事务失败不留部分订单或错误状态，INV-007/013/004/015）：消费事务失败（业务失败 `failed`、技术失败死信 `dead`、领取后提交前崩溃回滚）时不产生「订单已建但 request 非 `success`」或「`sold` 已扣但无订单」的中间态；`requests.status=success` 与成功订单创建同事务，`failed`/`dead` 时无对应成功订单且不扣 `sold`；崩溃后重投只产生一次业务效果。
- [ ] AC-003（对账验证最终业务数据，INV-011/014/016）：Redis 与 MySQL 的对账/收敛测试验证「收敛后的最终业务数据」而非只断言调用成功——收敛后 `remaining = total_stock - sold - inflight_queued`（在途 `queued` 计入口径正确，不错误回补在途预扣）；活动结束且在途归零后 `remaining = total_stock - sold`；重复收敛/崩溃重放幂等（不超预扣）。
- [ ] AC-004（新增测试不改变既有公开业务行为）：相对 Base Commit 的变更仅限测试文件与任务文档（及必要的测试辅助）；不新增或修改生产代码、配置、数据模型、错误码或公开接口；既有秒杀能力（含 V5 限流/熔断/指标）行为不变，既有测试全部保持通过。
- [ ] AC-005（人工恢复不破坏已成功订单，INV-017）：`dead→queued` 人工修复与审计记录同事务、审计 append-only；修复不会重放/破坏既有成功订单（不重复扣库存、不重复建单）；非 `dead` 状态修复被稳定拒绝（12008）且无副作用。
- [ ] AC-006（关键测试能识别错误业务实现）：每个关键不变量至少有一个测试能在对应业务实现被破坏（可控 mutation，如放宽条件扣减、删除唯一约束、拆散事务边界）时失败，证明其判别力而非只验证调用成功；mutation 仅在验证期间临时存在、验证后复原，不进入交付。

## Relevant Context

已核实事实：

- Git 基线：分支 `chore/kafka-invariant-regression-v1`，HEAD = `e4ffdffede08b94955b20f638c10917f33d24c58`（Task 01 `kafka-migration-baseline-v1` 的 Deliverer 验收提交）；working tree clean（`git status --short` 为空）。前置 Task 01 已交付（其 `state.yaml` `delivery.status=PASS`）。
- 秒杀业务不变量权威来源 `docs/design/flash-sale.md` §4（INV-001~021）：INV-001/002（库存不为负、成功订单数 ≤ 初始库存）、INV-003（一人一单）、INV-004（失败不建单）、INV-005（幂等防重复扣减）、INV-007（事务原子性）、INV-011（对账收敛）、INV-013（请求状态与成功订单一致）、INV-014（补偿幂等/不重不漏）、INV-015（崩溃重投幂等）、INV-016（活动结束收敛）、INV-017（人工修复授权与审计原子性）等；事实来源 MySQL 权威 + Redis 闸门派生。
- 秒杀核心调用链（改造前）：Redis Lua 闸门预扣 → `flash_sale_order_requests`（`queued`/`success`/`failed`/`dead`）→ `goroutine+ticker` 消费者单事务「校验 + 条件扣库存 + 建单 + 状态更新」→ 权威值收敛对账；无外部 MQ。
- 现有测试已较全面覆盖：V1（`TestFlashSaleConcurrentNoOversell`/`TestFlashSaleOnePerUser`/`TestFlashSaleIdempotency`/`TestFlashSaleOrderFailures` 等）、V2（`TestFlashSaleV2RedisPreDeductNoOversell`/`TestFlashSaleV2Reconcile`/`TestFlashSaleV3FailClosed`）、V3（入队/结果查询/消费幂等/补偿）、V4 白盒（`TestConsumeCrashBeforeCommitRedeliversOnce`/`TestConsumeInterruptedByCtxCancel`/`TestConvergeStockIdempotentAndClearsSoldout`/`TestRepairRequestDeadToQueuedAndAudit` 等）。
- 已存在测试专用故障注入点 `consumeBeforeCommitHook`（生产环境恒为 nil），用于模拟「领取后提交前崩溃」。
- 测试入口：`make test-unit`（纯内存）、`make test-integration`（`go test -p 1 ./...`，需 MySQL/Redis）、`make test-race`、`make test`（完整门禁）。

Assumption：

- 现有测试虽覆盖面广，但「能识别错误实现」的判别力未经系统性 mutation 验证；并发验证目前主要覆盖库存不超卖，一人一单/幂等/消费器并发领取唯一性的并发覆盖程度待 Analyst 核实。
- 任务执行需 MySQL/Redis/容器就绪；任务定义阶段未实际启动这些依赖，环境可用性由 Coder 执行时确认。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 「验证测试能识别错误实现」的 mutation 方法（注入方式、可逆性、是否覆盖所有关键不变量还是仅最高风险项），需 Analyst 固化。
- 并发验证的适用不变量清单与确定性断言口径（并发下成功数/`sold`/唯一约束不违例），需 Analyst 界定。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis（`docker compose up -d`）；并发断言需 `go test -race`；正确性核对需真实 MySQL 数据（非只看 HTTP 200）。

- AC-001 → 并发下单后断言 `sold ≤ total_stock`、`sold ≥ 0`、`COUNT(flash_sale_orders) = sold`，且无 `uk_flash_one_per_user`/`uk_flash_idempotency` 唯一约束违例；以 `-race` 运行。
- AC-002 → 注入业务失败/技术失败/提交前崩溃（复用/扩展测试 hook），断言消费事务回滚后无残留订单、`sold` 不变、`requests.status` 与订单事实一致；重投后恰好一次业务效果。
- AC-003 → 构造 Redis 预扣残留/虚高/在途 `queued` 场景，运行对账/收敛后断言 `remaining` 收敛到权威值，重复收敛幂等。
- AC-004 → `git diff <Base Commit>..HEAD` 相对 Base Commit 无生产代码/配置/migration/错误码/接口变更，仅含测试文件与任务文档；既有测试全绿。
- AC-005 → 修复 `dead→queued` 后断言既有成功订单不变量不被破坏（订单数/`sold` 不变、无重复），审计 append-only 且字段正确；非 `dead` 修复返回 12008 且无副作用。
- AC-006 → 对关键不变量测试做可控 mutation（临时破坏实现）断言测试失败，验证后复原并再次全绿；mutation 过程记录证据，不进入交付。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`、`go test -race ./...`。

## Complexity

COMPLEX

原因：本任务的核心交付不是「写测试」而是「确认哪些业务语义必须受保护 + 证明测试具备判别力」，涉及事务/库存/并发/跨系统一致性回归的判别力界定；「受保护业务语义」清单、并发验证口径与 mutation 方法论存在多个会产生不同结果的现实方案，需 Analyst 固化 Contract 后交 Owner 确认。

## Analyst Questions

1. 受保护业务语义清单：INV-001~021 中哪些属于「后续 Kafka 改造必须保留的正确性语义」、纳入本次回归防线（至少含库存不超卖、一人一单、幂等、请求状态与订单同事务、失败无半成品、对账收敛、人工修复原子性）；哪些属 V5 容量保护/可观测性（限流/熔断/指标）不纳入本次正确性回归。请固化「受保护不变量 → 测试入口」清单与缺口判定标准。
2. 现有测试判别力：逐项判断现有测试是否「能区分正确与错误实现」而非只验证调用成功；确定哪些不变量缺「能识别错误实现」的测试、哪些缺并发验证，给出待补测试清单与优先级。
3. 并发验证口径：为库存不超卖、一人一单、幂等、消费器并发领取唯一性建立并发验证时，如何定义确定性断言口径；`-race` 与业务并发验证如何分工；是否需真实 MySQL/Redis 及并发规模。
4. 识别错误实现的方法论：采用何种可逆 mutation 证明测试判别力（如临时放宽条件扣减、删除唯一约束、拆散事务边界），如何保证 mutation 只在验证期间临时存在、验证后复原、不进入交付；验证对象是最终实现还是对照实现。
5. 测试基础设施：是否需要新增测试专用 hook/辅助（现有 `consumeBeforeCommitHook` 是否足够），新增 hook 是否会被视为「改变生产代码」，如何保证其生产行为恒为 no-op。
6. 业务规则是否需要变化：分析中若发现现有不变量/实现存在缺陷（而非仅测试缺失），需明确走 Contract Revision / Task 更新，不得借「补测试」改变业务语义——请确认当前不存在此类情况，或显式标出并回 Owner/Task Builder。

## Review Baseline

- Base commit：`e4ffdffede08b94955b20f638c10917f33d24c58`（分支 `chore/kafka-invariant-regression-v1`，Task 01 Deliverer 验收提交）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务产物为 `.agent/tasks/kafka-invariant-regression-v1/` 任务文档，以及测试文件（`test/e2e/*_test.go`、`internal/logic/flashsale/*_test.go`）与必要的测试辅助；不修改任何生产代码/配置/migration/错误码。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）
