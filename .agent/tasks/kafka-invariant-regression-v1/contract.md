# Technical Contract

## Decision Status
APPROVED

## Problem

Task 01 已固化 Kafka 改造前的基线与既有测试，但「既有测试是否真正能识别错误实现」未经系统性验证，且部分正确性不变量的并发验证存在缺口。本任务要在不改变任何生产行为的前提下，把秒杀正确性/一致性不变量固化为可复用的回归防线：明确受保护不变量清单、补齐关键缺口测试、为适用不变量建立并发验证、用可逆 mutation 证明关键测试的判别力、固化可重复执行的回归命令（含 `-race`）。

范围边界：本任务**纯测试/文档任务**，不引入 Kafka/MQ、不改生产代码/配置/数据模型/错误码/接口、不改 `docs/design/*`（Design Impact = NONE）、不新增 migration、不占用错误码域（无全局资源预留）。

## Verified Current Behavior

- VERIFIED：秒杀业务不变量权威来源为 `docs/design/flash-sale.md` §4（INV-001~021）；MySQL 是权威事实源，Redis 是加速闸门/派生缓存。Kafka 改造的消费路径（当前 `goroutine+ticker` 消费者）改造后必须保留 §4 正确性/一致性不变量。
- VERIFIED：核心调用链（改造前）＝ `CreateOrder`（限流→排队软上限→熔断→Redis Lua 闸门预扣/写标记→入队 `flash_sale_order_requests(status=queued)`）→ `ConsumeQueued`（`FOR UPDATE SKIP LOCKED` 出队 → 单事务「活动校验+时间窗+条件扣 `sold`+建单+`status=success`」）→ 失败 `failed`/重试/`dead`，终态失败后权威值收敛。关键实现见 `internal/logic/flashsale/consume.go`、`flashsale.go`（`insertOrderInTx` 先建单后条件扣减 + 唯一约束兜底）、`redis.go`（`convergeStock` 幂等 SET）、`request.go`、`repair.go`（`dead→queued` 条件更新 + 审计同事务）。
- VERIFIED：现有测试已覆盖大部分正确性不变量（映射表见下）；已存在测试专用故障注入 `consumeBeforeCommitHook`（生产恒 nil，`consume.go`）与 `generateOrderNo`（订单号固定，`flashsale.go`），二者均为「生产环境恒为 no-op」的既有 hook。
- VERIFIED：测试入口 `make test-unit`（纯内存）、`make test-integration`（`go test -p 1 ./...`，需 MySQL/Redis）、`make test-race`（当前仅 `-run 'TestCircuitBreakerStateMachine'`，纯内存、不连 DB）、`make test`（完整门禁）。`test-race` 现不覆盖业务并发正确性测试。
- VERIFIED：任务无全局资源需求（不新增 migration、不占错误码域）；`docs/design/flash-sale.md` 已是最新权威，本任务不改动（Design Impact = NONE）。
- VERIFIED：Git 基线分支 `chore/kafka-invariant-regression-v1`，HEAD=`e4ffdffede08b94955b20f638c10917f33d24c58`，working tree 仅 `.agent/tasks/kafka-invariant-regression-v1/` 未跟踪（TaskBuilder 产物，未提交）。
- UNKNOWN：MySQL/Redis 测试环境实际就绪状态（由 Coder 执行时 `make deps-start` 确认）；未实际运行全套测试，既有测试「全绿」为 Task 01 基线结论，本轮由 Coder 复跑确认。

### 受保护不变量 → 测试入口 → 缺口 映射

| 不变量 | 现有测试入口 | 判别力 | 并发验证 | 结论 |
| --- | --- | --- | --- | --- |
| INV-001 库存≥0 | `TestFlashSaleConcurrentNoOversell`、`TestFlashSaleV2RedisPreDeductNoOversell` | 有（放宽条件扣减→失败） | 有 | 需 mutation 验证 |
| INV-002 订单数=sold≤stock | 同上 | 有 | 有 | 需 mutation 验证 |
| INV-003 一人一单 | `TestFlashSaleOnePerUser`（顺序） | 有（删唯一约束→失败） | **缺** | 补并发一人一单 |
| INV-004 失败不建单 | `TestFlashSaleOrderFailures`、`TestFlashSaleV3Compensation` | 有 | 部分 | — |
| INV-005 幂等不重复扣减 | `TestFlashSaleIdempotency`（顺序） | 有（删唯一约束→失败） | **缺** | 补并发幂等 |
| INV-006 定价快照 | `TestFlashSaleOrderPricingSnapshot` | 有 | 不适用 | — |
| INV-007 事务原子性 | `TestConsumeCrashBeforeCommitRedeliversOnce`、`TestConsumeInterruptedByCtxCancel` | 有（拆事务→失败） | — | 需 mutation 验证 |
| INV-010 Redis 预扣不超预热 | `TestFlashSaleV2RedisPreDeductNoOversell` | 有 | 有 | — |
| INV-011 对账收敛 | `TestFlashSaleV2Reconcile`、`TestConvergeStockCountsInflightQueued` | 有 | — | — |
| INV-012 请求级幂等 | `TestFlashSaleV3ConsumeIdempotent`、`TestCreateOrderIdempotencyConflictCompensatesOnce` | 有 | **缺**（消费并发领取唯一性） | 补并发消费 |
| INV-013 状态↔订单同事务 | `TestConsumeCrashBeforeCommitRedeliversOnce`、`TestConsumeRetryThenDeadLetter`、`TestFlashSaleV3Compensation` | 有 | — | 需 mutation 验证 |
| INV-014 补偿幂等 | `TestConvergeStockIdempotentAndClearsSoldout` | 有（INCR 化→失败） | — | 需 mutation 验证 |
| INV-015 崩溃重投幂等 | `TestConsumeCrashBeforeCommitRedeliversOnce` | 有 | — | — |
| INV-016 结束收敛 | `TestConvergeEndedActivity`、`TestConvergeEndedActivityKeepsInflight`、`TestReconcileCacheConvergesEndedActivity` | 有 | — | — |
| INV-017 修复原子+审计 | `TestFlashSaleRepairAndAudit`、`TestRepairRequestDeadToQueuedAndAudit` | 有（去条件更新→失败） | — | **补修复不破坏既有成功订单** |

不纳入本次回归防线（明确排除）：
- INV-008（权限）：正确性/安全不变量，但 Kafka 改造只动消费路径、不触及下单/管理权限；保留既有 `TestFlashSaleCreateActivityAndPermission` 测试，不新增回归工作。
- INV-009（配额预分配）：运营前置约定，非机械可验证。
- INV-018~021（V5 限流/排队软上限/熔断/指标）：容量保护与可观测性，属 `flash-sale-v5` 已交付产物，Out of Scope（对应测试保留，不纳入 Kafka 正确性回归命令）。

## Recommendation

RECOMMENDATION：把「正确性/一致性」不变量集（INV-001~007、010~017，共 15 条）定为 Kafka 改造必须保留的回归防线；补 4 个关键缺口测试 + 1 套可逆 mutation 判别力验证 + 1 条独立回归命令，全程不新增生产代码/接口/配置/错误码。

关键取舍：**判别力（mutation）采用「人工临时可逆 mutation」而非「自动化 mutation 框架」**——自动化框架需引入额外测试基建并可能改动生产代码以暴露注入点，与本任务「零生产代码变更 + mutation 不进入交付」的约束相冲突；人工 mutation 足够证明「测试能识别错误实现」，且 AC-004 的 `git diff <base>..HEAD` 天然保证 mutation 不进入交付。

### 待补测试清单（4 个，均为新增测试文件/用例）

1. **并发一人一单**（INV-003，AC-001）：白盒 `internal/logic/flashsale/` 内，绕过 Redis 闸门直接写 N 条 `(activity_id, sku_id, user_id)` 相同、`idempotency_key` 不同的 `queued` 请求，并发 `ConsumeQueued`；断言恰好 1 条 `success`、N-1 条 `failed`、订单数=1、`sold`=1、无唯一约束违例。依赖 `uk_flash_one_per_user` 兜底（并发下 `loadBindingForUpdate` 行锁串行 + 唯一约束拒绝重复）。
2. **并发幂等**（INV-005/012，AC-001）：白盒内写 N 条 `(user_id, idempotency_key)` 相同、`request_hash` 相同的 `queued` 请求，并发消费；断言恰好 1 条 `success`、订单数=1、`sold`=1、无 `uk_flash_idempotency` 违例。
3. **消费器并发领取唯一性**（INV-012/015，AC-001）：M 条不同用户 `queued` 请求，N 个 goroutine 并发 `ConsumeQueued`；断言 M 条全部 `success`、订单数=M、`sold`=M、无重复建单/重复扣减、无请求停留在 `queued`/半状态（`FOR UPDATE SKIP LOCKED` 领取互斥）。
4. **修复不破坏既有成功订单**（INV-017，AC-005）：活动已有成功订单（`sold>0`）+ 一条 `dead` 请求；执行 `dead→queued` 修复后重消费；断言既有成功订单数/`sold` 不被重放（最终订单数=原有+1、`sold` 相应 +1、无重复建单），审计 append-only。

### mutation 判别力验证（AC-006，人工临时可逆）

固定 6 个 mutation 目标（覆盖全部 AC），施加于「现有生产代码」后跑对应测试必须失败，`git restore`/重建索引复原后再全绿；证据记录于 Coder 交接说明，mutation 本身不进入交付：

| Mutation | 破坏 | 期望失败的测试 |
| --- | --- | --- |
| M-1 `insertOrderInTx` 条件扣减 `WHERE sold < total_stock` 改为无条件 | INV-001/002 | `TestFlashSaleConcurrentNoOversell` |
| M-2 测试环境 `ALTER TABLE flash_sale_orders DROP INDEX uk_flash_one_per_user` | INV-003 | 新增并发一人一单测试 |
| M-3 `ALTER TABLE flash_sale_orders DROP INDEX uk_flash_idempotency` | INV-005/012 | 新增并发幂等测试 |
| M-4 将 `successRequestInTx` 状态更新移出消费事务（拆散事务边界） | INV-007/013 | `TestConsumeCrashBeforeCommitRedeliversOnce` |
| M-5 `convergeStock` 由幂等 `SET` 改为增量 `INCR`（非幂等） | INV-011/014 | `TestConvergeStockIdempotentAndClearsSoldout` |
| M-6 `RepairRequest` 条件更新移除 `WHERE status=dead` | INV-017 | `TestRepairRequestDeadToQueuedAndAudit` |

### 并发验证口径（AC-001）

- **确定性断言**：并发验证一律断言精确终值（成功数、订单数、`sold`、`remaining`、状态分布），不用「≤ / 范围 / 近似」弱断言；唯一约束不违例由「无错误 + 精确计数」共同体现。
- **`-race` 与业务并发的分工**：`-race` 只检测 Go 进程内共享内存数据竞争（编译期插桩），**不**验证数据库唯一约束/行锁/条件写等业务并发正确性；业务并发正确性由「真实 MySQL/Redis + 并发 goroutine + 精确终值断言」验证。二者互补、都要跑。

### 回归命令（可重复，含 -race）

新增 `scripts/test-flashsale-regression.sh` + `make test-flashsale-regression` 目标（需 MySQL/Redis 就绪），作为 Kafka 改造前后正确性对比的唯一入口，按序执行：
1. 白盒正确性/一致性：`go test -p 1 ./internal/logic/flashsale/...`
2. 秒杀 e2e：`go test -p 1 -run 'TestFlashSale' ./test/e2e/...`
3. 并发 + race（需 DB/Redis）：`go test -race -run '<秒杀并发测试匹配>' ./internal/logic/flashsale/... ./test/e2e/...`

不改动既有 `make test-race`（纯内存、不连 DB）与 `make test` 门禁语义；新脚本独立、按需运行。

## Selected Design

经 Owner 批准（2026-10-16），采用以下设计：

1. **受保护不变量**：专项正确性回归范围 = INV-001~007、INV-010~017 共 15 条，语义以 `docs/design/flash-sale.md` §4 为唯一权威。INV-008/009/018~021 不纳入本次新增专项测试，但保留既有测试，且不意味着后续 Kafka 改造可破坏这些不变量。
2. **判别力验证 = 人工临时可逆 Mutation**：固定 6 个目标（M-1~M-6，覆盖全部 AC），不引入自动化 Mutation 框架。Mutation 必须在隔离 Worktree 或等价隔离副本中执行；涉及数据库约束的修改只能作用于一次性测试数据库，不得影响共享环境。每项 Mutation 记录实际执行结果，不得预设一定失败；若 Mutation 后测试仍通过，必须分析原因并补足测试保护，不得记录为成功。Mutation 验证独立执行，临时错误实现不进入常规回归流程。
3. **待补 4 类测试**：并发一人一单、并发幂等、消费器并发领取唯一性、修复不破坏既有成功订单。测试验证最终业务状态，区分「HTTP 受理成功 / 异步订单成功 / 幂等重放结果」；精确终值断言建立在明确测试数据、事务完成状态、库存统计口径之上。
4. **并发口径**：`-race` 只检测 Go 进程内数据竞争，真实 MySQL 并发测试验证业务正确性，二者不互相替代。
5. **回归入口**：新增 `make test-flashsale-regression`（独立脚本，需 MySQL/Redis），不改变既有 `make test-race` 语义，不替代项目全量测试。
6. **零生产代码行为变更**：不新增生产故障注入 Hook，不修改公开 API、业务状态机与现有业务语义。

## Interfaces and Data

- 不新增/不修改任何生产接口、数据模型、表结构、索引、配置、错误码、Redis key、迁移文件。
- 测试复用既有私有 hook `consumeBeforeCommitHook` / `generateOrderNo`（生产恒 no-op）；**不新增生产代码 hook**——已确认现有 hook 足够覆盖本轮新增测试的故障注入需求。
- 新增文件仅限：`test/e2e/*_test.go`、`internal/logic/flashsale/*_test.go`、`scripts/test-flashsale-regression.sh`（+ Makefile 目标）、`.agent/tasks/kafka-invariant-regression-v1/` 任务文档。

## Business Invariants

本任务要保护、且 Kafka 改造后必须保持的回归不变量（语义以 `docs/design/flash-sale.md` §4 为唯一权威，不在此重复全文）：

- INV-001 库存不为负；INV-002 订单数=sold≤初始库存；INV-003 一人一单；INV-004 失败不建单不扣库存；INV-005 幂等不重复扣减；INV-006 服务端定价快照；INV-007 下单/消费事务原子。
- INV-010 Redis 预扣不超预热库存；INV-011 对账收敛 `remaining = total - sold - inflight_queued`；INV-012 请求级幂等；INV-013 `status=success` 与成功订单同事务；INV-014 补偿幂等不重不漏；INV-015 崩溃重投恰好一次业务效果；INV-016 活动结束在途归零后 `remaining = total - sold`；INV-017 人工修复仅 `dead→queued` + 审计同事务 + append-only。

## Failure and Consistency Semantics

本任务不改变生产失败/一致性语义。测试自身的失败语义：mutation 验证必须满足「mutation 施加 → 目标测试失败（证明判别力）→ 复原 → 目标测试通过 + 全量绿（证明未残留）」，任何一步失败视为验证未通过。每项 Mutation 必须记录实际执行结果，不得预设一定失败；若 Mutation 后测试仍通过，必须分析原因并补足测试保护，不得将其记录为成功。唯一约束类 mutation（M-2/M-3）在一次性测试数据库 `ALTER TABLE DROP INDEX`/重建实现（或代码层等价 mutation），不改迁移文件、不提交、不影响共享环境。Mutation 在隔离 Worktree 或等价隔离副本中执行，独立于常规回归流程。

## Allowed / Forbidden Changes

- 允许：新增/补充测试文件（`test/e2e/*_test.go`、`internal/logic/flashsale/*_test.go`）；新增回归脚本与 Makefile 目标；维护 `.agent/tasks/kafka-invariant-regression-v1/` 任务文档；在**隔离 Worktree 或等价隔离副本**中做可逆 mutation 验证，数据库约束类 mutation 仅作用于一次性测试数据库。
- 禁止：修改任何生产代码（`internal/**` 非 `_test.go`、`cmd/**`、`api/**`、`manifest/**`、`config`）；新增/修改 migration、错误码、Redis key、接口；修改 `docs/design/*`；引入 Kafka/MQ；新增生产代码 hook；借「补测试」改变既有业务语义（若分析发现业务缺陷，走 Contract Revision / Task 更新，不在此任务内改）；删除或削弱既有测试（含 INV-008/009/018~021 的既有测试）。

## Verification Requirements

- AC-001 → 并发下单/消费（真实 MySQL/Redis + `-race`），断言 `sold≤total_stock`、`sold≥0`、`COUNT(flash_sale_orders)=sold`，并发一人一单/幂等各恰好 1 成功，无唯一约束违例。
- AC-002 → 注入业务失败/技术失败/提交前崩溃（复用 `consumeBeforeCommitHook`），断言事务回滚后无残留订单、`sold` 不变、`status` 与订单事实一致、重投恰好一次效果。
- AC-003 → 构造 Redis 预扣残留/虚高/在途 `queued` 场景，收敛后 `remaining = total - sold - inflight_queued`，结束在途归零后 `= total - sold`，重复收敛/崩溃重放幂等。
- AC-004 → `git diff e4ffdffede08b94955b20f638c10917f33d24c58..HEAD` 仅含测试文件/脚本/任务文档，无生产代码/配置/migration/错误码/接口变更；既有测试全绿。
- AC-005 → 修复 `dead→queued` 后既有成功订单数/`sold` 不变、无重复，审计 append-only 字段正确；非 `dead` 修复返回 12008 无副作用。
- AC-006 → 6 个 mutation 目标逐一：在隔离 Worktree/一次性测试库施加→目标测试失败→复原→目标测试通过+全量绿；每项记录实际执行结果，不预设失败，若仍通过须分析原因并补足测试保护；mutation 不进入交付、不纳入常规回归流程。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`、`go test -race`（并发测试）。

## Open Risks

- 环境风险：并发/集成测试需真实 MySQL/Redis，环境不可用则相应测试 `NOT_VERIFIED` 并如实记录，不影响纯内存门禁。
- 唯一约束类 mutation（M-2/M-3）在一次性测试数据库执行 `ALTER TABLE`（或代码层等价 mutation），不影响共享环境；需 Coder 执行时确认隔离环境可用。
- 并发断言依赖实现已保证的精确收敛语义；若 Coder 复跑发现既有实现与设计文档存在偏差（而非仅测试缺失），停止并回本 Analyst 走 Contract Revision / Task 更新，不得借测试掩盖。

## Owner Decision Record

- 2026-10-16：Owner 批准本 Task 技术 Contract（采纳 RECOMMENDATION 并补充约束）。决定：①受保护不变量 = INV-001~007、INV-010~017 共 15 条，排除 INV-008/009/018~021 但保留既有测试且 Kafka 改造不得破坏；②人工临时可逆 Mutation（6 目标、隔离 Worktree/一次性测试库、记录实际结果、不预设失败、若仍通过须分析补足）；③新增 4 类测试，验证最终业务状态（区分 HTTP 受理成功/异步订单成功/幂等重放），精确终值断言建立在明确数据与口径上，`-race` 与 MySQL 并发不互相替代；④新增 `make test-flashsale-regression`，Mutation 独立执行、临时错误实现不纳入常规回归。本决定仅批准本 Task 技术 Contract，不代表测试已通过，也不代表后续 Kafka 任务获得设计批准。
