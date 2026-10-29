# Owner Core Logic 验证卡

本任务为压测脚本增强（无生产 Go 代码改动）。值得 Owner 掌握的核心是脚本自身如何保证「压测结论可信」：一是正确性核对不会在异常/空值时假 PASS，二是网络失败不会污染 p99 与 429/503 计数。

## CL-001：正确性核对不变量（sold ≤ total_stock、订单数=sold、无重复）

- Owner 需要理解：压测报告 PASS/FAIL 的唯一依据是 `judge_correctness` 判定的五条不变量；若任一条被破坏（超卖、订单数不等于 sold、一人一单/幂等违例、活动数据缺失），报告必须判 FAIL，绝不能因空值被当作 0 而假 PASS。
- 生产代码：`scripts/flashsale-loadtest/run.sh` — `judge_correctness()`（约 65-74 行）与 `verify()`（MySQL 权威查询 + 判定输出）
- 关键测试：`scripts/flashsale-loadtest/test-run.sh` — `judge_correctness` 用例（正常 PASS / 超卖 FAIL / 订单数不一致 FAIL / 一人一单 FAIL / 幂等违例 FAIL / 空值 FAIL）
- 基线验证：`bash scripts/flashsale-loadtest/test-run.sh` → 输出「test-run.sh 全部通过」
- 可选 Mutation：把 `judge_correctness` 中的 `[[ -z "$total_stock" || -z "$sold" ]]` 判 FAIL 分支删除，或把 `"$sold" -gt "$total_stock"` 改成 `-ge`
- 预期失败：`test-run.sh` 的「活动不存在（空值）应 FAIL」或「超卖应 FAIL」断言会失败
- 恢复确认：撤销改动后再次运行 `bash scripts/flashsale-loadtest/test-run.sh` 应恢复通过

## CL-002：网络失败归类（不污染 p99 与 429/503 计数）

- Owner 需要理解：下单 curl 失败（连接拒绝/超时/重启窗口）时，该请求必须记为 `http_code=-1` 的 error 行、被排除出「有效请求」与 p99 统计；若解析成坏行会被当作 0 时延有效样本，直接拉低 p99 并虚增有效样本数，破坏样本下限判定（本次修复的核心 CLEAN-001）。
- 生产代码：`scripts/flashsale-loadtest/run.sh` — `parse_worker_out()`（约 82-90 行）与 `make_worker()` 的 `if !` 退出码捕获（约 150-154 行）；`analyze()` 的有效请求/error/429/503 归类
- 关键测试：`scripts/flashsale-loadtest/test-run.sh` — `parse_worker_out` 各分支 + worker 端到端（`BASE_URL=http://127.0.0.1:1` 强制失败）断言 `0,-1,-1`
- 基线验证：`bash scripts/flashsale-loadtest/test-run.sh` → 全部通过；`parse_worker_out '|-1|0'` 独立输出 `0,-1,-1`
- 可选 Mutation：把 worker 失败归一从 `out="|-1|0"` 改回旧坏行 `out="||-1|0"`（或把 `parse_worker_out` 的 `http_code` 归一改坏）
- 预期失败：`test-run.sh` 的「网络失败 fallback 应解析为 0,-1,-1」或「worker 网络失败应输出 0,-1,-1」断言失败
- 恢复确认：撤销改动后再次运行 `bash scripts/flashsale-loadtest/test-run.sh` 应恢复通过
