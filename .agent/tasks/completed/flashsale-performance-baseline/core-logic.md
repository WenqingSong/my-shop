# Owner Core Logic 验证卡

本任务核心价值是「基线可复现 + 摘要数据真实可回溯」，不涉及权限 / 事务 / 库存扣减 / 金额 / 幂等。以下两条机制决定产出的性能数据是否可信。

## CL-001：未测量项必须显式标记，禁止用估计值/默认值填充

- Owner 需要理解：硬件 / 版本采集命令失败时，基线应记录 `null` / 空串、摘要应显示「未测量」，绝不能填 0 或固定值，否则简历引用的数据会被污染成假数字。
- 生产代码：
  - `scripts/flashsale-loadtest/run.sh` 的 `num_or_null()`（数值字段非整数时输出 `null`）、`collect_hardware()` / `collect_runtime()`（命令失败置空、不阻塞主流程）、`save_baseline()` 的 `hardware` / `runtime` 字段写入。
  - `scripts/flashsale-loadtest/make-summary.sh` 的 `fmt()`（None / 空串 → 「未测量」）。
- 关键测试：`scripts/flashsale-loadtest/test-run.sh`
  - `[[ "$(num_or_null '')" == 'null' ]]`、`grep '"cpu_cores": null'`、`grep '"cpu_model": ""'`、collect 缺失降级断言、`grep '未测量'`。
- 基线验证：`bash scripts/flashsale-loadtest/test-run.sh`（预期 `全部通过`，EXIT=0）。
- 可选 Mutation：把 `num_or_null` 的空值分支从 `printf 'null'` 改为 `printf '0'`。
- 预期失败：`test-run.sh` 中 `num_or_null ''` 断言与 `grep '"cpu_cores": null'` 失败（未测量被写成了 0）。
- 恢复确认：改回 `printf 'null'`，重新运行 `bash scripts/flashsale-loadtest/test-run.sh` 应通过。

## CL-002：摘要每个数字可回溯到基线文件，缺失档位不跨档填充

- Owner 需要理解：简历摘要的每个数字必须能在对应基线 JSON 中找到实测来源（表格最后一列给出基线文件名），某档位基线缺失时必须标记「无基线」，不能用其他档位数据顶替，否则摘要会虚报数据覆盖范围。
- 生产代码：`scripts/flashsale-loadtest/make-summary.sh`
  - `latest()`（按文件名时间戳取每档最新基线）、结果表「基线文件」列（第 116-133 行）、缺失档位分支（`无基线`）。
- 关键测试：`scripts/flashsale-loadtest/test-run.sh` 中 make-summary 断言：`grep 'low-20261009T230000.json'`（可回溯）、`grep '无基线'`（缺失档位标记）、`grep '未测量'`（redis 空值不填充）。
- 基线验证：`bash scripts/flashsale-loadtest/test-run.sh`；或 `BASELINES_DIR=<三档fixture> TIERS="low medium high" bash scripts/flashsale-loadtest/make-summary.sh`（预期三档齐全且每档带文件名）。
- 可选 Mutation：删除结果表输出中的基线文件名（`\`{src}\``）列。
- 预期失败：`test-run.sh` 中 `grep 'low-20261009T230000.json'` 失败（数字无法回溯到基线）。
- 恢复确认：还原文件名列，重新运行测试应通过。
