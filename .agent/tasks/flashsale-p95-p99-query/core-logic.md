# Owner 核心逻辑验证卡

本任务为可观测性变更：在既有秒杀 Histogram 之上新增 Prometheus recording rules 固化 p95/p99 查询，无 Go 业务逻辑、无数据/并发/权限不变量。核心机制是「查询只取下单接口、单位口径正确」，二者任何一处出错都会静默产生错误延迟数值。

## CL-001：p95/p99 只统计下单接口 order（不混入 consume 异步耗时）

- Owner 需要理解：p95/p99 若误用 `interface="consume"` 或去掉接口过滤，会把异步落单耗时（往往远大于 HTTP 接收耗时）混入下单延迟，导致对外展示的延迟数值失真；`sum by (le)` 跨 `result` 聚合，得到下单接口整体分位。
- 生产代码：`prometheus/rules/flashsale_latency.yml` 两条 `record` 的 `expr`（`interface="order"` + `sum by (le)`）。
- 关键测试：`scripts/test-prometheus.sh` 第 6 段（`grep -qF 'interface="order"'`、`grep -qF 'sum by (le)'`、命中 `interface="consume"` 即 fail）。
- 基线验证：`./scripts/test-prometheus.sh`，预期 exit 0（含 `promtool check rules` 输出 `SUCCESS: 2 rules found`）。
- 可选 Mutation：把 `interface="order"` 改为 `interface="consume"`，或删除 `{interface="order"}`。
- 预期失败：`./scripts/test-prometheus.sh` 以 exit 1 失败，报 `recording rule 未过滤 interface="order"（应只取下单接口）`。
- 恢复确认：恢复 `interface="order"` 后再次运行 `./scripts/test-prometheus.sh`，exit 0。

## CL-002：单位口径正确（秒值配 `_seconds`，不误乘/漏乘 1000）

- Owner 需要理解：Histogram 桶为秒单位（`Observe(d.Seconds())`），`histogram_quantile` 返回秒值；指标名必须用 `_seconds` 且不加 `* 1000`，否则会出现「秒值配毫秒命名」或「漏乘/误乘 1000」的单位错配，下游引用时数值量级错误。
- 生产代码：`prometheus/rules/flashsale_latency.yml` 的 `record` 名（`..._p95_seconds` / `..._p99_seconds`）与 `expr`（无 `* 1000`）；单位说明见文件注释。
- 关键测试：`scripts/test-prometheus.sh` 第 6 段单位一致性分支（`_seconds` 出现 `* 1000` 即 fail；`_milliseconds` 缺 `* 1000` 即 fail）。
- 基线验证：`./scripts/test-prometheus.sh`，预期 exit 0。
- 可选 Mutation：把 `record` 名 `_seconds` 改为 `_milliseconds` 但不加 `* 1000`；或在 `_seconds` 不变时给 `expr` 加 `* 1000`。
- 预期失败：`./scripts/test-prometheus.sh` 以 exit 1 失败，分别报「指标名以 _milliseconds 结尾但表达式缺少 * 1000 换算」或「指标名以 _seconds 结尾但表达式出现 * 1000（单位错配）」。
- 恢复确认：恢复原名/去掉 `* 1000` 后再次运行 `./scripts/test-prometheus.sh`，exit 0。
