# Task: SurgeCart 秒杀链路工程基线审计（surgecart-baseline-audit-v1）

## Goal

在明确锚定的代码版本上，交付一份可复核的秒杀链路工程基线报告：每条核心结论都能回到具体源码、SQL、测试或真实执行输出，后续专项审查（T02、T03）只凭该报告与 Git 事实即可开工，不必重新从零扫描整个仓库。

## Scope

- 版本锚定：核实仓库、分支、HEAD commit 与工作区状态（暂存 / 未暂存 / 未跟踪），记录本次审计对应的精确代码版本；报告中的事实只对该版本成立。
- 调用链地图：从秒杀 HTTP 入口追踪到最终订单结果，覆盖路由注册、参数校验、限流与容量保护、Redis Lua 准入、MySQL 请求记录写入、后台消费者领取、订单创建与库存扣减、请求状态查询、对账与修复扫描之间的调用关系，逐跳给出文件与函数定位。
- 数据层定位清单：关键表与字段、状态取值、事务边界、唯一约束、索引、条件更新（`RowsAffected`）及其 migration 文件。
- 请求表职责判定：明确当前 MySQL 请求表承担排队、持久化、幂等、状态查询、恢复中的哪些职责，消费者实际如何领取与处理请求；区分「设计文档宣称」与「代码实际行为」。
- 测试与门禁基线：核实并记录现有 Go 测试、质量门禁、`Dockerfile`、CNB 流水线与 Prometheus 相关代码配置的真实状态；运行当前环境可执行的测试并保存命令、退出码与关键输出。
- 事实分区：把结论分为已验证事实、推断、未知事项、潜在风险四类；风险只记录不修复。
- 下游审查输入：给出 T02、T03 可直接使用的证据锚点与待专项核查问题清单。
- 报告落盘于 `docs/reports/surgecart-baseline-audit-v1/`。

## Out of Scope

- 不修改秒杀业务实现，不改动 `internal/`、`api/` 下任何生产代码与测试代码。
- 不新增 Kafka Producer / Consumer / Outbox，不引入任何外部 MQ，不改变现有 MySQL 出队表方案。
- 不修改数据库 Schema，不新增或修改 migration，不占用任何 `migration_version`。
- 发现疑似缺陷、竞态或不一致时只记录证据与风险，不修复、不重构、不调整阈值。
- 不更新 `docs/design/*`（本任务只记录既有事实，不新增长期设计结论）。
- 不做压测、容量规划、性能结论或生产部署验证。
- 不定义 T02、T03 的任务内容、路线与结论。
- 不修改 `.agent/workflow.yaml`、`.agent/specs/*`、`.agent/roles/*` 与 Workflow Engine。

## Milestone

Milestone: surgecart-baseline-audit-v1

## Acceptance Criteria

- [ ] AC-001：报告首页记录审查锚点（仓库、分支、完整 40 位 HEAD SHA、工作区状态），该 SHA 与所在 feature 分支的 `git rev-parse HEAD` 逐字符一致，并声明报告结论只对该版本成立。
- [ ] AC-002：报告给出从秒杀 HTTP 入口到最终订单结果的完整调用链，逐跳标注文件路径与函数/方法名（必要时行号），至少覆盖入口路由、参数校验、限流或容量保护、Redis Lua 准入、请求记录写入、消费者领取、订单创建、库存扣减、请求状态查询、对账或修复任务；抽查任意 3 跳都能在源码中定位到同名实现。
- [ ] AC-003：报告列出链路涉及的关键表与字段、状态取值、事务边界、唯一约束与索引、条件更新语句，并给出 migration 文件名与逻辑层 SQL 位置；每项约束说明它保护的是哪一条不变量（库存不为负、成功订单数不超过初始库存、一人一单、失败不建单、重复请求不重复扣库存）。
- [ ] AC-004：报告明确回答「当前 MySQL 请求表承担哪些职责」，并说明消费者实际如何领取与处理请求（领取 SQL、并发保护、失败重试、超时或死信回收、崩溃恢复），结论引用具体 SQL 或代码位置；代码与 `docs/design/*` 描述不一致时逐条列出差异。
- [ ] AC-005：报告以表格记录本次实际执行的检查：命令、退出码、通过 / 失败 / 未执行状态与关键输出摘要，至少覆盖 `go build ./...`、`go vet ./...`、可运行的单元测试、依赖 MySQL/Redis 的集成测试、`make test-prometheus`、`make test-container`、`make test`，以及 `.cnb.yml`、`Dockerfile`、`prometheus/` 的静态核实；依赖不可用或未执行项必须标 `NOT_EXECUTED` 并写明原因，不得写成通过。
- [ ] AC-006：报告把结论分为「已验证事实（附证据锚点）」「推断（附依据与缺口）」「未知事项（附核验方式）」「潜在风险（附影响面）」四类；README 或设计文档的意图性表述不得写成已实现事实，未验证内容不得进入已验证事实。
- [ ] AC-007：报告包含「T02 / T03 审查输入」一节，给出可按路径、行号、SQL、命令、SHA 复核的证据清单与仍需专项核查的问题清单；`T02`、`T03` 的指代如实标注为 Owner 后续任务编号，不臆测其范围。
- [ ] AC-008：本轮变更范围仅限 `docs/reports/surgecart-baseline-audit-v1/` 与本任务目录；`git diff --stat` 中不出现 `internal/`、`api/`、`internal/migrations/`、`Dockerfile`、`.cnb.yml`、`prometheus/`、`docs/design/` 的改动，也没有新增 Kafka 相关代码。

## Relevant Context

- 已核实事实（本次 TaskBuilder 只读核对，不构成审计结论）：
  - 审查基线：创建任务前位于 `develop`，HEAD `85402302622d059918e1a87b83bbb55b400d1a85`，与 `origin/develop` 一致；工作区 clean，无暂存与未跟踪文件。
  - 秒杀代码分布：`api/flashsale/v1/flash_sale.go`、`internal/controller/flashsale/flashsale.go`、`internal/service/flashsale.go`、`internal/logic/flashsale/`（`flashsale.go`、`request.go`、`redis.go`、`consume.go`、`ratelimit.go`、`queue_capacity.go`、`circuit_breaker.go`、`repair.go`）、`internal/worker/scanner.go`。
  - Redis Lua 以 Go 字符串内联：`internal/logic/flashsale/redis.go` 通过 `EVAL flashSaleGateScript` 调用；仓库内不存在独立 `.lua` 文件。
  - 相关 migration：`internal/migrations/sql/20261001000011_flash_sale.up.sql`、`20261001000015_flash_sale_order_requests.up.sql`、`20261001000018_flash_sale_request_audits.up.sql`（是否完整覆盖请求表职责待核实）。
  - 测试：`internal/logic/flashsale/{consume,consume_metrics,order_outcome,recovery,redis,capacity,circuit_breaker}_test.go`、`internal/worker/scanner_test.go`、`test/e2e/`。
  - 门禁与配置：`Makefile` 提供 `test`（go vet + 单元 + 集成 + Registry/容器化静态断言）、`test-unit`、`test-integration`、`test-race`、`test-prometheus`、`test-container`；`.cnb.yml` 含 PR 与 push 流水线（test / race / build / docker-build / docker-push / GitHub 镜像同步）；`Dockerfile`、`prometheus/prometheus.yml`、`prometheus/rules/flashsale_latency.yml`、`internal/metrics/metrics.go` 存在。
  - `docs/reports/` 已有报告先例 `2026-10-18-agent-task-governance.md`。
  - 上述分布只证明入口文件存在，不代表调用关系、职责划分或测试有效性已被核实——那些正是本任务要产出的结论。
- Assumption：
  - `T02`、`T03` 为 Owner 后续任务编号，本任务只提供审查输入，不推断其内容。
  - 基线报告放在 `docs/reports/surgecart-baseline-audit-v1/`，因为它描述特定版本的现状，不构成项目级长期设计事实。
- OPEN QUESTION：
  - 当前环境是否具备 Docker / MySQL / Redis，决定集成测试与 `make test` 能否执行；不阻塞，按 AC-005 记为 `NOT_EXECUTED` 并说明原因。

## Verification

- AC-001 → 用 `git rev-parse HEAD` 与报告首页 SHA 逐字符比对，并核对声明分支名。
- AC-002 → 抽查调用链中 3 跳（应包含 Redis Lua 准入与消费者领取），在标注文件中能定位到对应函数与调用点；链路终点可追到订单创建与库存扣减所在事务。
- AC-003 → 报告中的约束、索引与状态取值可在 `internal/migrations/sql/*.up.sql` 与逻辑层 SQL 中找到同名定义，且与代码判断分支一致。
- AC-004 → 结论引用的领取 SQL、条件更新与恢复逻辑可在源码中逐一找到；与 `docs/design/flash-sale.md` 的差异可逐条对照。
- AC-005 → 按报告记录的命令在同类环境重跑，退出码与通过 / 失败 / 未执行状态一致；`NOT_EXECUTED` 项确实因依赖缺失或需要真实外部环境而未执行。
- AC-006 → 抽取「已验证事实」中 3 条检查均有可复核锚点；抽取「推断」中 2 条确认未出现在已验证事实中。
- AC-007 → 只依据「T02 / T03 审查输入」的证据锚点，能定位到对应文件、命令或 SHA。
- AC-008 → 用 `git diff --stat` 与 `git status --short` 核对变更范围，`git log --oneline` 中本任务提交不含业务代码。

适用检查：

- `go build ./...`、`go vet ./...`（无需外部依赖）。
- `make test-unit`；`make test-integration`、`make test-race`、`make test` 视 Docker / MySQL / Redis 可用性而定。
- `make test-prometheus`、`make test-container`（静态断言，不依赖外部服务）。

## Complexity

NORMAL

原因：交付物是特定版本上的只读证据报告，不产生设计选择、代码变更、Schema 变更或公开协议变更；结论由源码、SQL、测试与实际执行输出这类客观事实唯一决定，可机械核对，也不存在会产生不同业务结果的多方案分歧，因此不需要独立 Contract 审批。任务主题确实涉及事务、库存并发与幂等，但本任务不改变这些语义，只记录现状，并把疑似问题留给后续任务判断。

## Review Baseline

- Base branch：`develop`
- Base commit：`85402302622d059918e1a87b83bbb55b400d1a85`
- Feature branch：`task/surgecart-baseline-audit-v1`
- Remote：`origin`（`https://cnb.cool/framework-station/my-shop`）
- 任务开始时已有修改：无（`git status --porcelain` 为空，无暂存、未暂存或未跟踪文件）
- 重叠修改的区分方式：本任务产物只允许出现在 `docs/reports/surgecart-baseline-audit-v1/` 与本任务目录；实施中若出现来源不明的既有改动，停止并上报，不覆盖、不 stash、不代提交。

## Initial Route

交 Coder（NORMAL）
