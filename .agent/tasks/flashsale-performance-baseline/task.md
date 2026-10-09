# Task: 秒杀性能基线保存与简历可引用数据整理

## Goal

让秒杀下单压测的每一档结果，连同其运行环境（硬件资源、Go/MySQL/Redis 版本）、库存与并发参数、所用 PromQL 查询、压测命令与观测时间范围，一并保存为可复现的 JSON 基线；并基于这些实测基线整理出一份只含实测数字、可引用到简历的性能数据摘要。

## Scope

- 基线保存增强（复用 `scripts/flashsale-loadtest/run.sh` 的 `save_baseline`，不新写压测工具）：
  - 记录硬件资源（CPU 核数 / 型号、内存总量）与运行时环境（Go 版本、MySQL 版本、Redis 版本），采集方式在基线中显式留痕。
  - 补齐库存参数（`total_stock` / `sold` 等）与并发 / 请求参数（`concurrency` / `users` / `requests_per_user` 等，已在既有基线中，需与新增字段一致持久化）。
  - 记录本次压测所用 PromQL 查询（下单 p95 / p99 等）与对应观测窗口。
  - 记录本次实际执行的压测命令（含关键环境变量）与观测时间范围（压测起止时间戳）。
- 产出简历可引用的性能数据摘要（Markdown）：只汇总基线 JSON 中已实测的字段，禁止填写任何未经实测 / 预估的数字；未测量的项显式标记为未测量，不加以估计值填充。

## Out of Scope

- 不新增 / 修改生产 Go 代码、数据模型、状态机、公开协议或错误码。
- 不新增 migration，不占用 `migration_version` / `error_code_domain` 等全局资源。
- 不调整容量保护参数语义或生产阈值，不把压测结论固化为容量调优 / 生产容量规划决策。
- 不做热点 Key 分析 / 防护（`hotkeys.sh` 已有，非本任务目标）。
- 摘要中的每个数字一律来自实测基线，不引入估算、理论值或未执行的预测。

## Milestone

Milestone: 秒杀性能基线保存与简历可引用数据整理

## Acceptance Criteria

- [ ] AC-001：给定一档压测完成，则其基线 JSON 除既有性能指标外，还包含硬件资源（CPU 核数 / 型号、内存总量）、Go/MySQL/Redis 版本、库存参数（`total_stock` / `sold`）与并发 / 请求参数，且这些字段值与实际运行环境一致。
- [ ] AC-002：给定一档压测完成，则其基线 JSON 记录本次所用 PromQL 查询（下单 p95 / p99 等）、实际执行的压测命令（含关键环境变量）与观测时间范围（压测起止时间戳），可供他人据此复现同一查询与命令。
- [ ] AC-003：产出一份性能数据摘要（Markdown），其中每个数字都能回溯到对应基线 JSON 的实测字段；不存在的测量项被显式标记为「未测量」，不得出现估算 / 理论值 / 未执行预测；无法回溯到基线的数字视为不合格。
- [ ] AC-004：低 / 中 / 高三档并发均产出基线并纳入摘要（复用现有 `low` / `medium` / `high` 三档机制），每档样本满足既有样本下限判定（`sample_insufficient=false`）。
- [ ] AC-005：不改 Go 生产代码 / migration / 协议 / 错误码；`run.sh` 既有正确性核对与分位统计能力保留，`bash -n` 与 `test-run.sh` 通过。

## Relevant Context

已核实事实：

- 现有 `scripts/flashsale-loadtest/run.sh` 的 `save_baseline()` 已把每档结果保存为 JSON（含 `scenario` / `timestamp` / `activity_id` / `sku_id` / `users` / `requests_per_user` / `concurrency` / `min_samples` / 吞吐 / p50/p95/p99 / 429/503 / `sold` / `total_stock` / queued / `correctness` 等），文件位于 `baselines/<scenario>-<ts>.json`，且被 `.gitignore`（`/scripts/flashsale-loadtest/baselines/*.json`）排除出版本库。
- 当前基线 JSON 未记录：硬件资源（CPU / 内存）、Go / MySQL / Redis 版本、所用 PromQL 查询、压测命令、观测时间范围——这正是本任务要补齐的字段。
- 既有 PromQL 位于 `prometheus/rules/flashsale_latency.yml`（p95 / p99 recording rules，`histogram_quantile` + `rate(...[5m])` + `interface="order"`）；`prometheus/prometheus.yml` 抓取 my-shop `GET /metrics`（`scrape_interval=15s`）。
- 环境版本事实（来自上一任务 `flashsale-v5-loadtest` 交付记录）：Go 1.24.1、MySQL 8.0.46、Redis 7.4.11；`docker-compose.yml` 声明 `mysql:8.0` / `redis:7-alpine` / `prom/prometheus:v2.53.0`；`go.mod` 声明 `go 1.23.0`。
- 上一任务已产出三级基线（low / medium / high，各 1000 有效请求，`correctness=PASS`），可作为本任务基线字段扩充的参照。
- 当前分支 `test/flashsale-performance-baseline`，工作区干净，HEAD `8106d22`。

Assumption：

- 「硬件资源」采集口径为 CPU 核数 / 型号与内存总量，运行时通过系统命令（`lscpu` / `/proc/meminfo` 或 `docker inspect`）采集并写入基线；具体命令与字段由 Coder 实现并在基线中显式留痕，不视为业务规则。
- 「简历可引用摘要」为 Markdown 文档，仅从基线 JSON 实测字段汇总；其存放位置与是否纳入版本库由 Coder 在实现中确定，但必须满足「每个数字可回溯到基线」。
- 「观测时间范围」以压测起止时间戳 + Prometheus `rate` 窗口（5m）为口径。

## Verification

环境：隔离测试环境需可连接的 MySQL 8.0 与 Redis（docker compose）、Prometheus（docker compose），一个进行中的秒杀活动与绑定 SKU（`ACTIVITY_ID` / `SKU_ID`），以及启用容量保护与 `/metrics` 的运行中服务。

- AC-001 → 运行 `run.sh` 三档后，检查基线 JSON 含硬件 / Go / MySQL / Redis 版本 / 库存 / 并发字段，且与实际环境核对一致（`lscpu` / `/proc/meminfo`、`go version`、`mysql SELECT VERSION()`、`redis-cli INFO server`）。
- AC-002 → 检查基线 JSON 含 PromQL 查询、压测命令（含关键环境变量）与观测时间范围字段；命令可原样重放复现。
- AC-003 → 逐项核对摘要文档中每个数字在对应基线 JSON 中均有实测来源；扫描摘要不存在估算 / 理论值表述；未测量项被标记为未测量而非填估计值。
- AC-004 → 三档基线存在，样本判定 `sample_insufficient=false`（每档有效请求 ≥ 1000）。
- AC-005 → `bash -n scripts/flashsale-loadtest/*.sh` 通过、`bash scripts/flashsale-loadtest/test-run.sh` 通过；确认无 Go 生产代码 / migration / 协议改动。
- Milestone（Deliverer）→ 在隔离环境真实执行三档压测，产出含完整环境元数据与 PromQL / 命令 / 时间范围的基线，并产出只含实测数字的简历可引用摘要，给出可回溯性核对结论。

## Complexity

NORMAL

原因：本任务是压测脚本字段扩充 + 摘要整理，不涉及数据模型、状态机、事务 / 并发一致性、MQ、权限或安全边界的选择，也不改变任何既有业务语义；硬件 / 版本采集与摘要「仅含实测数字」均为可机械核对的项，不存在会产生不同业务结果的方案分歧。无全局资源需求（不涉及 `migration_version` / `error_code_domain`）。

## Review Baseline

- Base commit：`8106d22b851e73f8cfe5f7770e0cfcfc5e29aad1`（分支 `test/flashsale-performance-baseline`，HEAD）。
- 任务开始时已有修改：无（`git status --short` 为空，working tree clean）。
- 重叠修改的区分方式：本任务产物集中在 `scripts/flashsale-loadtest/`（`run.sh` 字段扩充，`baselines/` 为 gitignore 运行时产物）与新增简历可引用摘要文档；不改 Go 生产代码、`manifest/config/config.yaml`、migration、`.agent/registry/*`。工作区干净，无既有未提交修改需区分。

## Initial Route

交 Coder（NORMAL）
