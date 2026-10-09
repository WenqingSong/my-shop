# Cleaner Findings

## Review Target

- Task：`.agent/tasks/flashsale-performance-baseline/task.md`
- 分支：`test/flashsale-performance-baseline`
- 任务基线（Base）：`8106d22b851e73f8cfe5f7770e0cfcfc5e29aad1`
- 被审查对象（C1，implementation Evidence Commit）：`e67cf2829ecdf6d9ff0febc66f10a406a5fa092f`
- 审查时 HEAD（C2 metadata commit，仅写 `state.review.*`）：`42bf7f5137bffaf091771961526377a374227933`
- 工作区状态：clean（`git status --short` 为空）
- 变更范围（`8106d22..e67cf28`，仅 3 个脚本，无 Go / migration / 协议 / 错误码改动）：
  - `scripts/flashsale-loadtest/run.sh`（修改：新增硬件/运行时采集、JSON 转义与数值兜底、PromQL/命令/时间范围字段）
  - `scripts/flashsale-loadtest/test-run.sh`（扩充：新增 json_escape/num_or_null/collect_*/save_baseline/make-summary 测试，既有测试全部保留）
  - `scripts/flashsale-loadtest/make-summary.sh`（新增：基线摘要生成）
- 全局资源：无（本任务不占用 `migration_version` / `error_code_domain`，Contract `NOT_REQUIRED`，无 Registry 三边一致性检查项）
- Design Impact：NONE（`contract.status = NOT_REQUIRED`，无 `docs/design/*` 一致性与四者核对要求）

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `save_baseline()` 新增 `hardware`（`cpu_cores`/`cpu_model`/`memory_total_kb` + `collection_method`）与 `runtime`（`go_version`/`mysql_version`/`redis_version` + `collection_method`），采集用真实系统命令（`nproc`/`lscpu`/`/proc/meminfo`/`go version`/`mysql SELECT VERSION()`/`redis-cli INFO server`，非硬编码）；库存（`total_stock`/`sold`）与并发/请求参数（`concurrency`/`users`/`requests_per_user`）沿用既有字段一致持久化。`test-run.sh` 覆盖字段记录与采集缺失降级；`bash -n` 通过。真实环境值一致性的最终实证属 Deliverer Milestone（Cleaner 不执行）。 |
| AC-002 | PASS | 基线新增 `promql`（`source`/`scrape_interval`/`queries` 含 `record`/`window`/`promql`）与 `loadtest`（`command`/`key_env_vars`/`start_epoch_seconds`/`end_epoch_seconds`/`duration_seconds`）。PromQL 与 `prometheus/rules/flashsale_latency.yml` 逐字一致（已核对 p95/p99 表达式与 record 名）；命令记录生效值且不含 MySQL 凭据。`test-run.sh` 断言 PromQL/命令/时间范围字段。 |
| AC-003 | PASS | `make-summary.sh` 仅读取基线 JSON 实测字段，`fmt()`/`latency_ms()` 对 None/空串显式输出「未测量」，不做估算填充；每档结果表最后一列为基线文件名（可回溯），环境信息注明来源基线。`test-run.sh` 覆盖实测数字、未测量标记、可回溯文件名与缺失档位「无基线」；Cleaner 独立运行三档 fixture 复核输出正确。 |
| AC-004 | PASS（实现机制）/ 运行时实证归 Deliverer | 三档机制复用既有 `low/medium/high`（`run.sh` main 的 case 分支）；样本下限判定沿用 `analyze()` 的 `SAMPLE_INSUFFICIENT` 并写入 `sample_insufficient`；`make-summary.sh` 按三档输出并处理缺失档位。`test-run.sh` 覆盖三档 fixture 汇总、样本不足/充足两条路径与缺失档位标记。真实三档压测产出与每档 `sample_insufficient=false` 的运行时实证属 Deliverer Milestone（见 task Verification 的 Milestone 项），Cleaner 阶段不执行。 |
| AC-005 | PASS | `bash -n scripts/flashsale-loadtest/run.sh|make-summary.sh|test-run.sh` 全部通过；`bash scripts/flashsale-loadtest/test-run.sh` 通过（EXIT=0）。变更范围仅 3 个脚本文件，无 Go 生产代码 / migration / 协议 / 错误码改动（`git diff 8106d22..e67cf28 --stat` 确认）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `bash -n` 三个脚本 | PASS | 均无语法错误 |
| `bash scripts/flashsale-loadtest/test-run.sh` | PASS | 输出 `test-run.sh 全部通过`，EXIT=0 |
| make-summary.sh 三档 fixture 独立运行 | PASS | 三档数据齐全、环境信息注明来源基线、Redis 空值标记「未测量」、每档含基线文件名、PromQL 与时间范围正确输出 |
| PromQL 一致性 | PASS | `run.sh` 中 `PROMQL_P95/P99` 与 `prometheus/rules/flashsale_latency.yml` 的 `flashsale_order_request_duration_p95/p99_seconds` 表达式逐字一致 |
| 无 Go / migration / 协议改动 | PASS | `git diff 8106d22..e67cf28 --stat` 仅 3 个脚本（+ 任务占位 artifact），无 `internal/`、`manifest/`、migration 文件 |
| 全局资源一致性 | N/A | 任务无 `migration_version`/`error_code_domain` 资源需求，Contract `NOT_REQUIRED` |
| 真实三档压测（sample_insufficient=false 实证） | NOT_EXECUTED | 属 Deliverer Milestone，Cleaner 不执行 |

## Findings

No actionable findings.

说明（不构成阻塞 Finding）：

- 三档压测的真实运行产出（每档 `sample_insufficient=false` 的实证）在本阶段未执行，属 Deliverer 的 Milestone 验收，Cleaner 审查对象是「实现与测试」，不执行压测运行。
- `make-summary.sh` 内 `get(*keys)` 为未使用的辅助函数；`save_baseline` 中字符串类未测量字段以空串 `""`、数值类以 `null` 记录，二者在摘要中均正确显示「未测量」。均为低风险内部实现细节，不影响任何 AC。
