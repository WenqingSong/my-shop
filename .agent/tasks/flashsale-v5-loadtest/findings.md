# Cleaner Findings

## Review Target

- 任务：`flashsale-v5-loadtest`（秒杀 V5 三级并发压测脚本增强）
- 审查对象：implementation Evidence Commit `C1 = 3a78a53f1f9bfa6b0d14135a6f0bfdd45c4a1554`（`fix(flashsale-loadtest): 修复网络失败行解析为坏行，污染有效样本与 p99`）
- 任务基线 Base：`aa272f485c6ea53131464361aa117bcb0d555667`
- 当前 HEAD：`67c475f`（C2 metadata：`chore(flashsale-v5-loadtest): 发起 review request（PENDING，绑定新 C1）`，仅改 `state.yaml.review.*`）
- C1 相对 Base 的实质变更：`scripts/flashsale-loadtest/run.sh`（三级并发档位 + 429/503 指标 + 正确性核对 + 网络失败解析修复）、`scripts/flashsale-loadtest/test-run.sh`（回归测试）
- 与任务前已有修改的区分：Base 时 working tree clean、无既有未提交修改；C1 未触碰 Go 生产代码 / migration / config / Registry
- Contract：`NOT_REQUIRED`（NORMAL，无全局资源，不涉及 `error_code_domain` / `migration_version`）

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（隔离环境 + 容量保护启用） | PASS | 脚本以 `BASE_URL` 指向隔离服务、用户按档位前缀隔离；容量保护开关经环境变量在服务侧启用（服务侧职责，脚本不触碰共享/生产数据）。实跑属 Deliverer 里程碑。 |
| AC-002（三级并发负载） | PASS | `main()` 按 `low=20 / medium=100 / high=200` 三档明确区分 worker 数，`CONCURRENCY` 可覆盖；复用 worker + xargs；每档独立产出 `baselines/<scenario>-<ts>.json`，可重复执行（幂等键稳定）。 |
| AC-003（足够样本） | PASS | 每档默认 `USERS=1000 × 1` = 1000 有效请求；`analyze` 对 `valid < MIN_SAMPLES` 显式提示并写 `sample_insufficient`；网络失败已正确排除在有效样本之外（CLEAN-001 已修复）。 |
| AC-004（指标记录） | PASS | 报告与基线 JSON 含 QPS/p50/p95/p99/429 比例/503 比例/`queued_peak`/`queued_backlog`；429/503 计数 = 逐请求 `http_code` 聚合；网络失败行 `http_code=-1` 归类 error、不参与时延统计。 |
| AC-005（正确性核对） | PASS | `verify()` 核对 `sold ≤ total_stock`、`COUNT(flash_sale_orders)=sold`、一人一单/幂等违例为 0，字段与 schema 一致；`judge_correctness` 空值/超卖/订单数/违例均 FAIL，由 `test-run.sh` 覆盖。 |
| AC-006（无回归） | PASS | `bash -n` 全部脚本通过；未改 Go 生产代码/migration/协议/错误码/config/Registry；原正确性核对能力保留并增强。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `bash -n scripts/flashsale-loadtest/{run,test-run,compare,hotkeys}.sh` | PASS | 全部语法通过 |
| `bash scripts/flashsale-loadtest/test-run.sh` | PASS | 输出「test-run.sh 全部通过」 |
| `parse_worker_out` 各分支独立复现 | PASS | fallback `|-1|0`→`0,-1,-1`；正常→`0.010,200,0`；限流→`0.030,429,12009`；熔断→`0.040,503,1005`；无业务码→`0.050,500,-1` |
| worker 端到端网络失败回归 | PASS | `test-run.sh` 用 `BASE_URL=http://127.0.0.1:1` 强制 curl 失败，断言 RESULTS 行为 `0,-1,-1` |
| CLEAN-001 Mutation 验证（隔离临时副本） | PASS | 改坏 `parse_worker_out` 归一逻辑后 `test-run.sh` FAIL，改动前 PASS，证明回归测试能区分正确/错误实现 |
| working tree 与 C1 一致 | PASS | `git diff 3a78a53 -- run.sh test-run.sh` 无输出 |
| 三档并发 worker 区分 | PASS | 代码核对：low=20 / medium=100 / high=200 |
| 响应契约/用户名校验一致性 | PASS | `{code,message,data}` 统一响应；IAM `^[a-zA-Z0-9]{3,24}$` 与脚本用户名匹配 |

## Findings

- CLEAN-001（网络失败行解析污染有效样本与 p99）——**CLOSED**。修复 `3a78a53` 将解析收敛为单一 `parse_worker_out`，worker 用 `if !` 捕获 curl 退出码并在失败时归一为 `|-1|0`（→ `0,-1,-1`），并新增覆盖 fallback 与端到端网络失败的回归断言；独立复现与 Mutation 验证均确认修复有效。

No actionable findings.
