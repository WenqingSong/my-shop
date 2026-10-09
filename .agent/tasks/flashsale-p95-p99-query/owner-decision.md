# Owner Decision

## Review Target
23f1c429c4f87ee31c87d0c76a478db2a7dbf117

## Core Logic
- CL-001：p95/p99 只统计下单接口 `interface="order"`（`sum by (le)` 跨 `result` 聚合），不混入 `interface="consume"` 异步落单耗时。
- CL-002：单位口径正确——Histogram 桶为秒单位、`histogram_quantile` 返回秒值，指标名以 `_seconds` 结尾且不 `* 1000`，避免「秒值配毫秒命名」或「误乘/漏乘 1000」的单位错配。

## Owner Decision
ACCEPTED

## Decision Evidence
Owner 明确回复「ACCEPT」，接受 Cleaner 已 CLEAN 的当前 snapshot `23f1c429c4f87ee31c87d0c76a478db2a7dbf117`。
适用范围：本任务（flashsale-p95-p99-query）的 recording rules 实现与静态校验；接受仅绑定该 snapshot，不改变任务范围，也不替代 Deliverer 的里程碑运行验收。
