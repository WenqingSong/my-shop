# Owner Decision

## Review Target
cb2a8fd62342aaab611fbd857ad715052b3d0ea9

## Core Logic
- CL-001：未测量的数据必须明确标记为 `null` / 空串 /「未测量」，禁止使用估算值、默认值或 0 代替真实测量结果。
- CL-002：性能摘要中的所有数字必须来自对应档位的真实基线 JSON；缺失档位必须标记「无基线」，禁止跨档填充或编造数据。

## Owner Decision
ACCEPTED

## Decision Evidence
Owner 于 2026-10-10 明确重新 ACCEPT，接受 Cleaner 已 CLEAN 的 snapshot `cb2a8fd62342aaab611fbd857ad715052b3d0ea9`（Prometheus 镜像内置配置修复），并将 `owner.review_target` 重新绑定到 `cb2a8fd`，保持 Cleaner 的 `review.*` 不变。CL-001 / CL-002 两条核心机制继续适用；`cb2a8fd` 本身为部署修复，其正确性已由 Cleaner 机械核验（路径三边一致 + `promtool check`），不构成新的业务不变量。

Owner 对 P3 Finding `CLEAN-001`（`storage/banners/banner-{1,2,3}.png` 误提交进本 commit）的决定：暂不处理，保留 OPEN，记录延期决定；本轮不删除这三个 banner 占位图。

Owner 对后续 Deliverer 交付提出的明确要求（纳入交付验收口径）：
1. 在新基线（`cb2a8fd`）上重新执行 low / medium / high 三档并发压测；旧三档结果只保留作历史对照，不作为新版本 PASS。
2. 各档记录 QPS、p50/p95/p99、HTTP 429/503 比例、网络错误和 queued 积压情况。
3. 保存对应 JSON 基线，记录硬件配置、软件版本、并发参数、库存、压测命令、Git Commit SHA、PromQL 和观测时间范围。
4. 验证各档有效样本数满足 MIN_SAMPLES，且 sample_insufficient=false。
5. 验证 MySQL 库存及订单正确性不变量。
6. 验证性能摘要中的数据与原始 JSON 一致。
7. 未实际测量的数据不得出现在简历性能结论中。
8. 若真实压测条件不满足，明确记录阻塞原因，不得使用模拟数据替代。
