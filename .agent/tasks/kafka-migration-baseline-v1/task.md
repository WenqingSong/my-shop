# Task: Kafka 改造前基线（kafka-migration-baseline-v1）

## Goal

建立 Kafka 改造前的可信代码基线：核实并记录秒杀核心的**真实调用链**（Redis Lua 闸门、MySQL 请求表、异步消费、库存对账），运行现有秒杀核心测试并保存成功/失败证据，固化「固定 Commit + 可重复验证命令」的回归验证入口，证明现有秒杀能力（五个业务不变量）能够被完整复现与保留，且建立基线的过程不改变任何生产行为。

## Scope

- 核对并记录当前 Git Commit、分支与已有工作区修改（Review Baseline）。
- 确认并记录秒杀核心真实调用链：Redis Lua 抢购闸门 → `flash_sale_order_requests` 出队表入队 → `goroutine+ticker` 后台消费者异步落单 → 库存对账权威值收敛。明确当前**无外部 MQ（无 Kafka）**，异步队列由 MySQL 出队表实现。
- 记录既有接口（前台 `POST /flash-sales/:id/orders`、`GET /flash-sales/:id/orders/result`；后台活动创建/更新/修复/审计接口）、数据库表结构（`flash_sale_activities` / `flash_sale_activity_skus` / `flash_sale_orders` / `flash_sale_order_requests` / `flash_sale_request_audits`）、错误码（秒杀域 12001~12010）与请求状态（`queued` / `success` / `failed` / `dead`），以上述真实代码与 `docs/design/flash-sale.md` 为唯一事实来源，记录不创造新行为。
- 运行现有秒杀核心测试（纯内存单元测试、依赖 MySQL/Redis 的集成测试、race、静态检查），保存成功与失败证据。
- 固化可重复执行的验证命令（`make test-unit` / `make test-integration` / `make test-race` / `make test` 等）与固定 Base Commit，作为后续 Kafka 改造任务必须保留的回归验证入口。
- 记录因环境不具备（如无 MySQL/Redis/容器）而无法运行的关键检查，如实标记 `NOT_VERIFIED` 并说明原因与影响。
- 将基线证据与已知问题记录持久化在本任务目录内，供 Cleaner 审查与后续 Kafka 改造对比。

## Out of Scope

- 不实现 Kafka 改造本身：不引入、不接入 Kafka / MQ，不改动异步消费与库存对账的实现路径（属后续任务）。
- 不新增或修改任何生产代码、配置、数据模型、错误码或公开接口；不新增 migration、不占用错误码域（无全局资源预留需求）。
- 不修改 `docs/design/*`（Design Impact = NONE）；秒杀架构事实以现有 `docs/design/flash-sale.md` 与 `docs/design/architecture.md` 为准。
- 不做 Kafka 架构设计、选型、消息协议或幂等/重试方案（属后续 Analyst 职责）。
- 不修改已有测试的断言标准，禁止通过删除/跳过失败测试、放宽关键断言、忽略错误制造 `PASS`。
- 不进行压测基线对比（`scripts/flashsale-loadtest/` 属 V5 已交付产物，本次仅作为可选证据引用，不新增/重跑为验收必需项）。

## Acceptance Criteria

- [ ] AC-001（基线可复现）：给定记录在案的固定 Base Commit 与可重复验证命令，能够在干净环境复现秒杀核心业务的基线行为——Redis Lua 闸门快速失败、异步消费落单、库存对账收敛；五个业务不变量（库存 ≥ 0、成功订单数 ≤ 初始库存、一人一单、失败不建单、重复请求不重复扣减）保持成立，复现结果与基线证据一致。
- [ ] AC-002（调用链与事实记录准确）：秒杀核心真实调用链、既有接口、数据库表结构、错误码（12001~12010）与请求状态（`queued`/`success`/`failed`/`dead`）被如实记录，与当前代码及 `docs/design/flash-sale.md` 逐项一致；记录明确标注当前异步队列为 MySQL 出队表、**无 Kafka**。
- [ ] AC-003（测试证据诚实）：所有已执行的秒杀核心检查保存成功或失败证据；因环境不具备（如无 MySQL/Redis/容器）而无法运行的关键检查，明确标记 `NOT_VERIFIED` 并说明原因与影响，不得被记录为通过。
- [ ] AC-004（回归入口可保留）：固化后续 Kafka 改造必须保留的回归验证入口——固定 Base Commit + 明确可重复的测试命令（纯内存单元测试、集成测试、race、静态检查），改造后可据此对比验证既有秒杀能力未被破坏。
- [ ] AC-005（无行为变更）：建立基线的过程不新增或修改任何生产代码、配置、数据模型、错误码或公开接口，不新增 migration 或错误码；相对 Base Commit 的变更仅限本任务目录内的任务文档与基线证据。

## Relevant Context

已核实事实：

- Git 基线：分支 `chore/kafka-migration-baseline-v1`，HEAD = `8fd62c06db41098ca85e5ee5bb81f6b4e87ba5b0`（`docs: 补充 GoLand 开发环境镜像说明`）；working tree clean（`git status --short` 为空）；local == `origin/chore/kafka-migration-baseline-v1` == `origin/develop`（三者同 SHA，分支起点即 develop 当前 HEAD）。
- 秒杀调用链（改造前，事实来源 `docs/design/flash-sale.md` + `internal/logic/flashsale/*`）：
  - Redis Lua 闸门：`internal/logic/flashsale/redis.go` 的 `flashSaleGateScript`（`runGate`），原子完成空值标记/活动存在与时间窗/售罄/幂等/一人一单检查与库存 `DECR` 预扣；返回 `GATE_PASSED` 等内部码。
  - MySQL 请求表（出队表）：`flash_sale_order_requests`（`status` 0=queued / 1=success / 2=failed / 3=dead），入队见 `request.go`，出队 `WHERE status=queued AND (next_attempt_at IS NULL OR next_attempt_at<=NOW()) ORDER BY id LIMIT N FOR UPDATE SKIP LOCKED`。
  - 异步消费：`internal/logic/flashsale/consume.go`（`ConsumeQueued` → `consumeOne`，单事务「校验 + 条件扣库存 + 建单 + 状态更新」）+ `internal/worker/scanner.go`（`StartFlashSaleConsumeScanner`，goroutine+ticker，默认 1s 周期）。**无外部 MQ**，`docs/design/architecture.md` §已知留白明确「未引入 RabbitMQ/Kafka」。
  - 库存对账：`redis.go`（`ReconcileCache` / `convergeStock` / `convergeEndedActivity`）+ `StartFlashSaleReconcileScanner`（默认 60s 周期），权威值收敛 `remaining = total_stock - sold - inflight_queued`。
- 数据表：`flash_sale_activities`、`flash_sale_activity_skus`、`flash_sale_orders`、`flash_sale_order_requests`、`flash_sale_request_audits`（migrations `20261001000011` / `20261001000015` / `20261001000018`）。
- 接口（`api/flashsale/v1/flash_sale.go`）：前台 `POST /flash-sales/:id/orders`、`GET /flash-sales/:id/orders/result`；后台 `POST /admin/flash-sales`、`PUT /admin/flash-sales/:id`、`POST /admin/flash-sales/requests/:id/repair`、`GET /admin/flash-sales/requests/:id/audits`。
- 错误码（`internal/codes/codes.go`）：秒杀域 12000-12999，已用 12001~12010（活动不存在/时间窗/库存不足/已购/幂等冲突/SKU 不可用/参数非法/不可修复/限流/队列满）。
- 测试入口：`make test-unit`（纯内存，含秒杀 `TestCircuitBreakerStateMachineTransitions` / `TestCircuitBreakerStateMachineRace` / `TestOrderOutcomeMapping`）；`make test-integration`（`go test -p 1 ./...`，需 MySQL/Redis，覆盖 `internal/logic/flashsale/*` 全量集成测试与 e2e `TestFlashSaleConcurrentNoOversell` / `TestFlashSaleOnePerUser` / `TestFlashSaleIdempotency` / `TestFlashSaleV5RateLimitHTTP` 等）；`make test-race`；`make test`（完整门禁：go vet + 单元 + 集成 + Registry 一致性 + 容器/nginx/prometheus 静态断言）。
- 环境：`docker-compose.yml` 提供 mysql + redis + app + nginx + prometheus，**无 Kafka**。

Assumption：

- 建立基线「运行测试」所需的 MySQL/Redis/容器环境当前可能未就绪；任务定义阶段未实际启动这些依赖，环境可用性由 Coder 执行时确认，不可用部分按 AC-003 标记 `NOT_VERIFIED`，不得假设为通过。
- 本次基线记录引用现有 `docs/design/flash-sale.md` 与 `docs/design/architecture.md` 作为事实来源，不重复誊写其全文，仅确认与当前代码一致。

## Verification

- AC-001 → 在 MySQL/Redis 就绪的干净环境运行 `make test-integration`（或 `go test -p 1 ./...`），断言秒杀核心 e2e（`TestFlashSaleConcurrentNoOversell` / `TestFlashSaleOnePerUser` / `TestFlashSaleIdempotency`）与 `internal/logic/flashsale/*` 集成测试通过；并对账核对 `sold ≤ total_stock`、成功订单数 = `sold`。
- AC-002 → 将记录与 `internal/logic/flashsale/*`、`docs/design/flash-sale.md`、`internal/codes/codes.go`、`api/flashsale/v1/flash_sale.go`、migration SQL 逐项比对一致。
- AC-003 → 无法运行的关键检查如实标记 `NOT_VERIFIED`，附原因与影响；证据持久化于本任务目录。
- AC-004 → 回归入口命令清单可重复执行，固定 Base Commit 已记录，改造后据此对比。
- AC-005 → `git diff <Base Commit>..HEAD` 相对 Base Commit 无生产代码/配置/migration/错误码变更，仅含本任务目录文档与证据。
- 通用命令：`make test-unit`（无 MySQL/Redis）、`make test-integration`（需 MySQL/Redis）、`make test-race`、`make test`（完整门禁）、`gofmt`、`go build ./...`、`go vet ./...`。

## Complexity

NORMAL

原因：本任务不涉及架构/数据模型/协议选择、无并发一致性新设计、无 MQ 可靠性/幂等新设计、无权限边界变更、无根因不明 bug；本质是「记录 + 验证既有行为」，无需 Analyst 固化 Contract，直接交 Coder。

## Review Baseline

- Base commit：`8fd62c06db41098ca85e5ee5bb81f6b4e87ba5b0`（分支 `chore/kafka-migration-baseline-v1`，`docs: 补充 GoLand 开发环境镜像说明`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空；local == `origin/chore/kafka-migration-baseline-v1` == `origin/develop`）。
- 重叠修改的区分方式：本任务仅新增 `.agent/tasks/kafka-migration-baseline-v1/` 任务文档与基线证据，不改动任何生产代码/配置/migration/错误码。当前工作区干净，无既有未提交修改。

## Initial Route

交 Coder（NORMAL）
