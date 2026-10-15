# Task: Kafka 改造架构边界与技术 Contract

## Goal

确定「秒杀异步落单链路引入 Kafka」的架构边界，产出经 Owner 确认的正式技术 Contract 与配套 ADR / 架构图 / 模块改造边界表，明确：Kafka 为什么存在、为什么仍保留 MySQL 请求表、哪些业务状态由 MySQL 负责、如何保证已受理请求不因 Kafka 故障永久丢失、为什么 Kafka 重复投递不会导致重复订单，以及新旧消费路径的切换与回滚策略。

## Scope

- 梳理当前秒杀异步链路已核实事实（Redis Lua 闸门、MySQL `flash_sale_order_requests` 出队、消费者事务、库存对账的依赖关系），作为改造基线。
- 明确 Kafka Consumer 是否为「正常异步落单的唯一任务分发入口」，以及现有消费扫描器在迁移前 / 迁移中 / 迁移后的职责。
- 明确 `flash_sale_order_requests` 在改造后的保留职责（受理事实 / Outbox / 死信兜底等，由 Analyst 决定并说明理由）。
- 明确 Redis / MySQL / Kafka 三者的权威数据边界：哪些业务状态由 MySQL 负责（权威），Redis 的闸门地位是否改变，Kafka 的传输责任边界。
- 确定 Outbox 使用独立事件表还是复用 / 扩展 `flash_sale_order_requests`。
- 明确秒杀请求、订单成功事件、失败事件之间的关系（事件 Schema、Topic 划分、状态映射）。
- 明确 Kafka 不可用时的请求受理策略、数据库不可用时的失败关闭策略。
- 明确新旧消费路径的切换与回滚策略。
- 产出 ADR、架构图、技术 Contract（`contract.md`）、模块改造边界表。
- 产出长期设计 `docs/design/kafka.md`（承载 ADR / 架构图 / 模块改造边界表）。

## Out of Scope

- 不实现任何生产代码、不建表、不改迁移、不改秒杀入队 / 消费逻辑（Kafka 落地实现属后续实现任务）。
- 不修改 `docs/design/flash-sale.md`、`docs/design/architecture.md` 的现状描述（当前「无外部 MQ」描述仍准确，待实现任务落地时同步更新）。
- 不在本任务预留 migration version / error_code_domain 具体编号（无代码与 DDL 变更）；Contract 只需识别后续实现任务需要预留的资源类型与语义。
- 不涉及普通订单 / 退款 / 购物车 / 库存等非秒杀模块的 Kafka 改造（若 Owner 有全局 MQ 平台化诉求，属后续独立设计）。

## Design Impact

Design Impact: NEW

Design Artifact: `docs/design/kafka.md`（新增，承载 Kafka 异步链路架构、ADR、架构图与模块改造边界表）

## Acceptance Criteria

- [ ] AC-001：Contract 用独立章节说明「Kafka 为什么存在」——对比现有 MySQL 出队表 + goroutine/ticker 消费者方案，明确引入 Kafka 要解决的具体问题与不引入的后果，理由与当前实现事实一致。
- [ ] AC-002：Contract 说明「为什么仍保留 MySQL `flash_sale_order_requests`」及其改造后的保留职责，且不破坏既有不变量「`flash_sale_orders` 行存在是成功订单唯一业务事实」。
- [ ] AC-003：Contract 给出 Redis / MySQL / Kafka 三者权威数据边界表，明确哪些业务状态由 MySQL 负责（权威），Redis 与 Kafka 各自承载什么、谁是最终事实来源。
- [ ] AC-004：Contract 明确「已受理请求不因 Kafka 故障永久丢失」的保证机制（受理持久化与事件投递的事务边界、投递确认 / 重试 / 死信语义），并能证明 Kafka 恢复后所有已受理请求仍可被处理或可观测。
- [ ] AC-005：Contract 明确「Kafka 重复投递不产生重复订单」的幂等机制（去重键、复用唯一约束、重复消息失败语义），并给出重复投递的边界行为。
- [ ] AC-006：Contract 明确 Kafka 不可用时的请求受理策略与数据库不可用时的失败关闭策略，说明与现有 V3 / V5 fail-closed 语义的关系（保持一致或显式变更并给理由）。
- [ ] AC-007：Contract 明确新旧消费路径的切换与回滚策略，覆盖迁移前 / 迁移中 / 迁移后三阶段职责、灰度 / 开关机制、回滚触发条件与操作步骤。
- [ ] AC-008：交付物齐全——架构图、ADR、技术 Contract、模块改造边界表均已产出，四者对权威边界 / 状态机 / 幂等 / 一致性 / 失败语义的表述一致、无自相矛盾。
- [ ] AC-009：每个关键架构取舍（含 Outbox 独立表 vs 复用扩展）在 ADR 中记录 context / decision / consequence，并给出未选方案的被拒理由。

## Relevant Context

已核实事实：

- 主集成分支为 `develop`（`.agent/workflow.yaml` `git.integration_branch: develop`，非 `main`）。任务开始时 `origin/develop` 与 feature 分支 `chore/kafka-architecture-contract-v1` 均指向 `8fd62c06db41098ca85e5ee5bb81f6b4e87ba5b0`，working tree clean、与远程同步，无 divergence、无已有修改。
- 当前代码库不存在 Kafka 依赖（`go.mod` 无相关依赖；仅 `docs/design/architecture.md` §5 与历史文档提及「无外部 MQ」边界）。
- 秒杀异步下单（V3-V5）现状：`POST /flash-sales/:id/orders` 经 Redis Lua 闸门预扣 + 写标记 → `INSERT flash_sale_order_requests(status=queued)` 入队 → 后台 `goroutine+ticker` 扫描器（`internal/worker/scanner.go` `StartFlashSaleConsumeScanner`）经 `FOR UPDATE SKIP LOCKED` 出队 → 单事务落单（`sold+1` + `INSERT flash_sale_orders` + `status=success`）。
- 三个后台扫描器并列：订单超时取消、秒杀缓存对账（`StartFlashSaleReconcileScanner`）、秒杀消费（`StartFlashSaleConsumeScanner`）。
- `flash_sale_order_requests` 四态：queued(0) / success(1) / failed(2) / dead(3)，`uk_request_idempotency` 兜底请求级幂等；成功订单唯一事实来源仍是 `flash_sale_orders`（`uk_flash_idempotency` / `uk_flash_one_per_user` 兜底）。
- 权威数据边界现状：MySQL 为订单 / 库存 / 请求生命周期权威；Redis 为加速闸门与派生缓存（对账收敛到 MySQL）；出队表「承担外部 MQ 的角色」（`architecture.md` §3.3、§5）。
- 既有不变量 INV-001~021（`docs/design/flash-sale.md` §4），改造不得破坏其中 MySQL 兜底的正确性边界。

Assumption：

- 「核对当前 main 的准确 Git Commit」中的 `main` 指项目主集成分支，本仓库实际为 `develop`（见上），按 `develop` 处理。
- Owner 意图是「确定架构边界并形成 Contract」，本任务为纯架构 / 分析任务，不含实现。

OPEN QUESTION：

- （不阻塞，交 Analyst / Owner 在 Contract 决策中明确）Kafka 是否仅用于秒杀链路，还是作为全站通用 MQ 平台的第一步——本任务按「秒杀链路」边界编写，全局平台化留后续。

## Verification

- AC-001~AC-009 的验证方式：由 Analyst 在 `contract.md` 与 `docs/design/kafka.md` 中逐条对应说明，并进入 Owner Contract Decision（`contract.status=APPROVED`）确认；Owner 依据本文 AC 逐条审阅 Contract / ADR / 架构图 / 边界表。
- 交付物一致性：架构图 / ADR / Contract / 边界表之间权威边界、状态机、幂等、一致性、失败语义无矛盾（本阶段由 Analyst 自检 + Owner 决策；后续进入实现时由 Cleaner 做四者一致性复审）。
- 本任务无代码、无 DDL、无运行时依赖，不要求 `go build` / `go test` / 集成环境验证。

## Complexity

COMPLEX

原因：涉及消息可靠性（Kafka 故障不丢单）、幂等与重复投递、跨系统一致性（MySQL↔Redis↔Kafka 权威边界）、重试 / 补偿，以及多个现实方案（Outbox 独立表 vs 复用扩展、新旧消费路径切换）会产生不同业务 / 可靠性 / 运维结果，属重要架构与数据模型选择，必须先经 Analyst 形成 Contract 并由 Owner 决策。

## Analyst Questions

1. 当前 Redis Lua 闸门 → 入队 → 消费落单 → 对账收敛的完整依赖链与崩溃窗口（已核实部分见 Relevant Context），改造后哪些环节被 Kafka 替代、哪些保留？
2. Kafka Consumer 是否作为「正常异步落单的唯一任务分发入口」？现有消费扫描器在迁移前 / 中 / 后三阶段各自职责如何定义？
3. Redis / MySQL / Kafka 三者权威数据边界如何划分？MySQL 仍负责哪些业务状态（权威）？Redis 闸门地位是否改变？
4. Outbox 用独立事件表还是复用 / 扩展 `flash_sale_order_requests`？各自对 INV-012 / 013 / 014 / 015 与现有对账 / 死信修复的影响？
5. 秒杀请求、订单成功事件、失败事件之间的关系如何建模（事件类型、Topic / 分区、Key 设计、事件与请求状态机映射）？
6. Kafka 不可用时的请求受理策略（受理后入 Outbox 待投递 vs fail-closed）与数据库不可用时的失败关闭策略？
7. 「已受理不因 Kafka 故障永久丢失」的实现机制（Outbox 事务边界、投递确认、重试、死信）与「重复投递不重复建单」的幂等去重边界？
8. 新旧消费路径的切换与回滚策略（灰度 / 开关、迁移三阶段、回滚触发条件与步骤）？
9. 该改造是否引入新表 / 新错误码 / 新配置，后续实现任务需要预留哪些全局资源类型与语义（migration_version / error_code_domain）？（本任务不预留、不实现）

## Review Baseline

- Base commit：`8fd62c06db41098ca85e5ee5bb81f6b4e87ba5b0`（feature 分支 HEAD == origin/develop，无 divergence）
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）
- 重叠修改的区分方式：本任务仅产出 `.agent/tasks/kafka-architecture-contract-v1/` 与 `docs/design/kafka.md`，不触碰生产代码与既有 `docs/design/*`；后续实现任务以本 Contract 为边界另行评审。

## Initial Route

交 Analyst（COMPLEX）
