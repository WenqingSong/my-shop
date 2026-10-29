# Task: 秒杀 V5 三级并发压测（容量保护生效验证）

## Goal

在隔离测试环境启用秒杀 V5 容量保护（限流 / 排队软上限 / 熔断 / 指标）后，复用现有压测脚本对秒杀下单链路执行低、中、高三级并发负载，每档采集足够样本，逐档记录 QPS、p95、p99、429 比例、503 比例与 queued 积压，并核对 `sold ≤ total_stock`、无重复成功订单，形成可复现的压测报告。

## Scope

- 压测脚本增强（复用 `scripts/flashsale-loadtest/run.sh`，不重写新工具）：
  - 提供低 / 中 / 高三级并发档位（并发 worker 数三档明确区分，复用现有 worker + xargs 并发机制）。
  - 每档发起足够样本的下单请求，使 p99 具备统计意义。
  - 输出并记录每档 QPS（吞吐 req/s）、p95、p99、HTTP 429 比例、HTTP 503 比例、queued 积压。
  - 保留并沿用现有正确性核对（`sold ≤ total_stock`、成功订单数 = `sold`、无一人一单 / 幂等违例），并将核对判定纳入报告与基线 JSON。
- 压测执行（Deliverer 里程碑）：隔离测试环境启用 V5 容量保护（限流 / 排队软上限 / 熔断 / 指标开关开启，阈值记录在报告），分别执行低 / 中 / 高三级并发负载，保存基线、产出报告。

## Out of Scope

- 不新增 / 修改生产 Go 代码、数据模型、状态机、公开协议或错误码：V5 容量保护功能已由 `flash-sale-v5` 交付并 PASS，本任务只验证、不改变其语义。
- 不新增 migration、不占用错误码域等全局资源。
- 不调整容量保护参数语义或生产阈值（压测所用阈值仅为本次测试配置，不沉淀为生产结论）。
- 不做热点 Key 分析 / 防护（`hotkeys.sh` 已有，非本任务目标）。
- 不把压测结论固化为容量调优 / 生产容量规划决策（只记录事实与正确性核对结果）。

## Milestone

Milestone: 秒杀 V5 三级并发压测与正确性核对

## Acceptance Criteria

- [ ] AC-001（隔离环境 + 容量保护启用）：压测在隔离测试环境执行，服务启动时启用 V5 容量保护（限流、排队软上限、熔断、指标开关均为开启，实际阈值记录在报告中），健康检查通过；压测不触碰共享 / 生产数据。
- [ ] AC-002（三级并发负载）：复用 `scripts/flashsale-loadtest/run.sh`，支持低 / 中 / 高三级并发档位（并发 worker 数三档明确区分），每档对同一秒杀活动 × SKU 发起下单，脚本可重复执行、每档独立产出结果与基线。
- [ ] AC-003（足够样本）：每档发起并记录 ≥ 1000 个有效请求样本，避免少量样本导致 p99 失真；样本数不足时脚本给出明确提示或判定失败，不在报告中用少量样本冒充 p99。
- [ ] AC-004（指标记录）：每档报告输出并记录 QPS、p95、p99、429 比例（HTTP 429 请求占比）、503 比例（HTTP 503 请求占比）与 queued 积压（压测期间排队峰值及压测后稳态值）；基线 JSON 持久化上述字段。
- [ ] AC-005（正确性核对）：压测（含异步消费落单）后核对 `sold ≤ total_stock`、成功订单数 = `sold`、无重复成功订单（一人一单违例、幂等唯一约束违例均为 0），核对结果写入报告并给出 PASS / FAIL 判定。
- [ ] AC-006（无回归）：不改生产 Go 代码、数据模型、状态机、协议与错误码；`run.sh` 原有正确性核对能力保留，`bash -n` 通过。

## Relevant Context

已核实事实：

- 秒杀 V5 容量保护已由 `flash-sale-v5` 交付（`delivery.status=PASS`）：用户级 / 活动级限流（`12009`/429）、排队软上限（`12010`/429）、Redis 闸门熔断（`1005`/503）、指标（`/metrics`）均已实现；配置 `manifest/config/config.yaml` `flash_sale` 段含 `rate_limit` / `queue_capacity` / `circuit_breaker` / `metrics`，均默认 `enabled: false`，经环境变量（`FLASH_SALE_*`）或配置覆盖开启。
- 现有压测脚本 `scripts/flashsale-loadtest/run.sh` 可重复执行，已覆盖三级场景（`normal`/`low_stock`/`flood`，并发 20/100/200）、输出吞吐 / p50/p95/p99、按业务 code 统计 queued/rejected/error，并核对 `sold ≤ total_stock`、订单数 = `sold`、一人一单 / 幂等违例、queued 积压、连接数；基线存 `baselines/<scenario>-<ts>.json`。其 `analyze()` 按业务 code 统计，未单独计算 HTTP 429 / 503 比例；默认样本 100 用户 × 1 请求 = 100，p99 仅对应 1 个样本。
- HTTP 429 对应限流 `12009` 与排队满 `12010`；HTTP 503 对应熔断 / fail-closed `1005`。脚本 worker 已逐请求记录 `http_code`（RESULTS 第 2 列），可据此聚合 429 / 503 比例。
- 秒杀五个业务不变量（INV-001~005）及正确性事实来源（MySQL 唯一约束 + 条件扣减）在 `docs/design/flash-sale.md` 已固化，本任务不改变。
- 当前分支 `test/v5-three-tier-load-testing`，工作区干净，无既有未提交修改。

Assumption：

- 「足够的请求样本」以每档 ≥ 1000 有效请求为默认下限（p99 至少对应 10 个样本，具备统计意义）；具体并发 worker 数（低 / 中 / 高三档默认值）与单用户请求数由 Coder 设定并在脚本 / 报告中显式记录，不视为业务规则。
- QPS 以脚本吞吐（总请求 / 总耗时）口径记录，与 `/metrics` 的 QPS 指标语义一致；429 / 503 比例以 HTTP 状态码计数占比口径计算。
- 压测所用容量保护阈值（限流窗口与阈值、排队上限、熔断参数）为本次测试配置，默认使用合理测试值并记录，不等同于生产调优结论。

## Verification

环境：隔离测试环境需可连接的 MySQL 8.0 与 Redis（docker compose），一个进行中的秒杀活动与绑定 SKU（`ACTIVITY_ID`/`SKU_ID`），以及启用容量保护的运行中服务。

- AC-001 → 以容量保护开关开启启动服务，`/health` 返回 200；报告记录启用的开关与阈值；确认压测表 / Redis 数据为隔离数据。
- AC-002 → 分别以低 / 中 / 高三级并发档位运行 `run.sh`，断言三档并发 worker 数明确区分、每档独立产出结果与基线，脚本可重复执行（连续两次运行均成功）。
- AC-003 → 核对每档报告记录的实际请求样本数 ≥ 1000；样本不足时脚本明确提示 / 判定失败。
- AC-004 → 核对每档报告与基线 JSON 含 QPS、p95、p99、429 比例、503 比例、queued 积压字段，且数值可复现、与逐请求记录可对账（429/503 计数 = 逐请求 HTTP 状态码聚合）。
- AC-005 → 消费落单后经 MySQL 核对：`sold ≤ total_stock`、`COUNT(flash_sale_orders)=sold`、一人一单 / 幂等违例均为 0，报告判定 PASS；任一不满足判定 FAIL。
- AC-006 → `bash -n scripts/flashsale-loadtest/*.sh` 通过；确认无 Go 生产代码 / migration / 协议改动。
- Milestone（Deliverer）→ 在隔离环境真实执行低 / 中 / 高三级压测，产出报告与基线，给出正确性核对 PASS / FAIL 结论。

## Complexity

NORMAL

原因：本任务是压测脚本增强 + 压测执行与核对，V5 容量保护功能、不变量与事实来源均已由 `flash-sale-v5` 交付并固化，本任务不涉及数据模型、状态机、事务 / 并发一致性、MQ、权限或安全边界的选择，也不改变任何既有语义；429/503 比例、并发档位、样本下限均为可机械核对的测试参数，不存在会产生不同业务结果的方案分歧。无全局资源需求（不涉及 migration_version / error_code_domain）。

## Review Baseline

- Base commit：`aa272f485c6ea53131464361aa117bcb0d555667`（分支 `test/v5-three-tier-load-testing`，HEAD）。
- 任务开始时已有修改：无（`git status --short` 为空，working tree clean，local == `origin/test/v5-three-tier-load-testing`）。
- 重叠修改的区分方式：本任务产物集中在 `scripts/flashsale-loadtest/`（`run.sh` 及基线目录 `baselines/` 为 gitignore 运行时产物）；不改 Go 生产代码、`manifest/config/config.yaml`、migration、`.agent/registry/*`。工作区干净，无既有未提交修改需区分。

## Initial Route

交 Coder（NORMAL）
