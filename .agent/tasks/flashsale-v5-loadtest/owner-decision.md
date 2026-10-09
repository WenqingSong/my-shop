# Owner Decision

## Review Target

- 审查对象（CLEAN snapshot）：`3a78a53f1f9bfa6b0d14135a6f0bfdd45c4a1554`（`fix(flashsale-loadtest): 修复网络失败行解析为坏行，污染有效样本与 p99`）
- 任务基线 Base：`aa272f485c6ea53131464361aa117bcb0d555667`
- 任务：`flashsale-v5-loadtest`（秒杀 V5 三级并发压测脚本增强）

## Core Logic

- CL-001：正确性核对不变量 —— 报告 PASS/FAIL 的唯一依据是 `judge_correctness` 的五条不变量（`sold ≤ total_stock`、订单数 = `sold`、一人一单/幂等违例 = 0、活动数据非空），任一破坏或空值必须判 FAIL，防止假 PASS（`run.sh` `judge_correctness()` / `verify()`，回归 `test-run.sh`）。
- CL-002：网络失败归类 —— curl 失败必须归一为 `http_code=-1` 的 error 行、排除出有效请求与 p99 统计，否则污染 p99 并虚增样本数、破坏样本下限判定（`run.sh` `parse_worker_out()` / `make_worker()` `if !` 捕获 / `analyze()`，回归 `test-run.sh`）。

## Owner Decision

ACCEPTED

## Decision Evidence

- Owner 于 2026-10-09 明确回复 `ACCEPT`，接受 Cleaner 已 CLEAN 的当前 snapshot（`review.target = 3a78a53f1f9bfa6b0d14135a6f0bfdd45c4a1554`）作为本任务压测脚本增强的 Owner 确认，非抽象接受任务。
- 适用范围：本次压测脚本增强（三级并发档位、429/503 指标、正确性核对、网络失败解析修复）。压测执行（Deliverer 里程碑）与最终集成/合并仍由 Owner 决定，不在本决策范围。
- 绑定：`state.owner.review_target` 已机械绑定至 `review.target`。
