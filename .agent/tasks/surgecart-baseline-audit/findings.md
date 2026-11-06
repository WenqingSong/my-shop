# Findings — SurgeCart 秒杀链路工程基线审计

## Review Target

`95c13910c7226b4e32487ed046702caf75d74fc6`（源码逐字节等价于 `473d6f5253e3abcb98ece9d79a5a8059563a3e8d`；`git diff --stat 473d6f5..95c1391` 仅任务记录文件）

## Review Conclusion

- 结论: NOT_REVIEWED
- 审查者: —（未指派）
- 时间: —
- 说明: 本任务为只读审计，报告作者即 LEAD，单 Agent Session 内不存在独立审查者。按 `AgentCollaborationSpecification.md` §4.3，独立审查未满足，不得声称已通过审查。审计事实本身的证据见 `docs/reports/surgecart-baseline-audit/` 五个文件；风险项均为风险登记，不是审查结论。

## Findings

> 本任务的 Finding 全部为「审计发现的风险」，状态保持 `OPEN`，`Required fix` 写为后续任务处理范围；本任务不修复任何业务代码。

### F1 对账/预热固定 LIMIT 100 导致活动饥饿

- Severity: MAJOR
- Status: OPEN
- Category: 并发 / 持久化
- Evidence: `internal/logic/flashsale/redis.go:390-397`、`:409-418`（`Order("id").Limit(limit)` 无游标）；`internal/worker/scanner.go:45`（batch=100）。无 >100 活动测试。
- Impact: 活跃活动或 grace 结束活动超过 100 个时，id 较大的活动长期不被预热/收敛，Redis remaining 偏离权威值，可能出现误售罄或额外无效请求。
- Required fix: 后续专项任务评估分页/游标/轮转策略；本任务不修复。
- Review Target: `95c13910c7226b4e32487ed046702caf75d74fc6`
- Resolution: 未解决

### F2 扫描周期配置项宣称支持环境变量，实际不生效

- Severity: MAJOR
- Status: OPEN
- Category: 可维护性 / 配置
- Evidence: `internal/worker/scanner.go:22`、`:55`、`:82` 使用 `MustGet`；GoFrame `gcfg.go:96-112`（`Get` 不读 env）、`:216-226`（`MustGet`→`Get`）；`manifest/config/config.yaml:94` 注释宣称 `FLASH_SALE_RECONCILE_SCAN_INTERVAL / FLASH_SALE_CONSUME_SCAN_INTERVAL`。
- Impact: 通过环境变量调整消费/对账周期会被静默忽略，运维与压测参数失真。
- Required fix: 后续任务确认是否改为 `GetEffective`（并补测试）；本任务不修复。
- Review Target: 同上
- Resolution: 未解决

### F3 消费吞吐上限与绑定行串行化

- Severity: MAJOR
- Status: OPEN
- Category: 并发 / 容量
- Evidence: `internal/worker/scanner.go:54-75`（单 goroutine、1s ticker、批量 100）；`internal/logic/flashsale/consume.go:36-52`（串行）；`internal/logic/flashsale/flashsale.go:489`、`:702-715`（同 activity×SKU 的绑定行 `FOR UPDATE` 串行化）。
- Impact: 峰值下 queued 积压、结果查询延迟上升；未经压测，容量数值未知。
- Required fix: 由后续压测/容量任务量化并决定是否并行化；本任务不修复。
- Review Target: 同上
- Resolution: 未解决

### F4 请求表无生命周期管理

- Severity: MAJOR
- Status: OPEN
- Category: 持久化 / 运维
- Evidence: `internal/migrations/sql/20261001000015_flash_sale_order_requests.up.sql:11-28`；全仓库无对该表的 DELETE/归档/分区（`grep` 无结果）。
- Impact: 表与 `idx_dequeue` 无界增长，出队、`COUNT(status=queued)`、备份恢复成本随时间上升。
- Required fix: 保留策略属产品/架构决策，交后续任务与 Owner；本任务不修复。
- Review Target: 同上
- Resolution: 未解决

### F5 可观测性覆盖不足

- Severity: MINOR
- Status: OPEN
- Category: 可维护性
- Evidence: `internal/metrics/metrics.go:44-56`（仅 2 个指标，标签 `interface`/`result`）；全仓库无其他自定义 collector；无 HTTP 全站、Redis、MySQL 指标，无队列深度/`sold`/独立 dead 指标。
- Impact: 积压、死信、依赖健康需直接查库；容量告警缺乏数据源。
- Required fix: 后续可观测性任务评估；本任务不修复。
- Review Target: 同上
- Resolution: 未解决

### F6 `/metrics` 无鉴权且绕过统一响应中间件

- Severity: MINOR
- Status: OPEN
- Category: 安全
- Evidence: `internal/cmd/cmd.go:180-187`（`s.BindHandler("GET:/metrics", ...)` 直接写响应）；开关默认 false（`config.yaml:139-141`）；设计文档提示需内网可达。
- Impact: 开启后未授权方可读取流量量级/拒绝率/延迟分布（标签低基数，不含用户/活动）。
- Required fix: 由部署/安全边界任务评估；本任务不修复。
- Review Target: 同上
- Resolution: 未解决

### F7 设计文档与 V3+ 实现不一致

- Severity: MINOR
- Status: OPEN
- Category: 可维护性
- Evidence: `docs/design/flash-sale.md:160`（描述 `INCR remaining` 补偿、标记「下单成功后才写入」）与 `:207-208`；实现为 `SET` 权威值收敛（`redis.go:327-347`）、`GATE_PASSED` 写标记（`redis.go:134-140`）；同文档 `:166-168`、`:224` 已是 V3 语义，前后矛盾。
- Impact: 后续任务若以文档为准会误判补偿语义与标记时机。
- Required fix: 文档修订任务；本任务不修改文档。
- Review Target: 同上
- Resolution: 未解决

### F8 DB 级防御纵深缺口

- Severity: MINOR
- Status: OPEN
- Category: 持久化 / 正确性
- Evidence: `20261001000011_...up.sql:24-37` 无 `CHECK sold<=total_stock`；`20261001000015_...up.sql:22` `flash_order_id` 无 FK/唯一约束；一致性仅由同一事务（`consume.go:130`）保证。
- Impact: 直接改库或未来新增写入路径可破坏 `sold<=total_stock` 或请求↔订单关联；查询层会以 500 暴露（`request.go:131-142`）。
- Required fix: 后续数据模型任务评估；本任务不改 Schema。
- Review Target: 同上
- Resolution: 未解决

### F9 幂等键为用户维度全局唯一且必须客户端提供

- Severity: MINOR
- Status: OPEN
- Category: 正确性 / 接口契约
- Evidence: `uk_request_idempotency(user_id,idempotency_key)`（migration 15:26）、`uk_flash_idempotency(user_id,idempotency_key)`（migration 11:57）；`requestHash` 含 activity|sku（`flashsale.go:817-820`）；空键 1001（`:276-279`）。
- Impact: 同一用户跨活动复用同一键 → 12005；客户端不传键 → 400；无服务端兜底。
- Required fix: 接口契约评估；本任务不修改契约。
- Review Target: 同上
- Resolution: 未解决

### F10 容量保护与指标默认全部关闭

- Severity: MINOR
- Status: OPEN
- Category: 配置 / 容量
- Evidence: `manifest/config/config.yaml:103-141`（rate_limit/queue_capacity/circuit_breaker/metrics 均 false）；读取实现 `ratelimit.go:42`、`queue_capacity.go:27`、`circuit_breaker.go:40`、`metrics.go:63`。
- Impact: 使用默认配置部署时不具备限流/排队上限/熔断/指标能力。
- Required fix: 部署配置评审；本任务不改配置。
- Review Target: 同上
- Resolution: 未解决

### F11 消费批次遇首个技术错误即中断

- Severity: MINOR
- Status: OPEN
- Category: 错误处理
- Evidence: `internal/logic/flashsale/consume.go:41-46`（`consumeOne` 返回 error 时立即返回）；`scanner.go:69-71` 仅记录日志。
- Impact: 同批剩余请求延迟到下一 tick；持续错误会反复中断（死信上限仍生效）。
- Required fix: 后续可靠性任务评估；本任务不修复。
- Review Target: 同上
- Resolution: 未解决

### F12 对账与消费并发时可能瞬时低估计 remaining

- Severity: MINOR
- Status: OPEN
- Category: 并发 / 可用性
- Evidence: 推断项（`docs/reports/surgecart-baseline-audit/facts-and-risks.md` B1、F12）：对账在事务外读 `sold` 与 `COUNT(status=queued)`（`redis.go:304-321`），与消费事务（`flashsale.go:463-542`）并发时，已预扣但未提交的请求会被重复计一次。
- Impact: remaining 短暂偏小 → 少量用户收到 12003，≤60s 自愈；不破坏 MySQL 不变量。**未复现**。
- Required fix: 后续并发实验确认；本任务不修复。
- Review Target: 同上
- Resolution: 未解决

## 审计证据文件索引

| 交付物 | 文件 |
| --- | --- |
| D1 调用链 | `docs/reports/surgecart-baseline-audit/call-chain-map.md` |
| D2 数据层 | `docs/reports/surgecart-baseline-audit/data-layer-map.md` |
| D3 测试基线 | `docs/reports/surgecart-baseline-audit/test-baseline.md` |
| D4 事实与风险 | `docs/reports/surgecart-baseline-audit/facts-and-risks.md` |
| D5 T02/T03 交接 | `docs/reports/surgecart-baseline-audit/review-input-t02-t03.md` |
| 工程盘点 | `docs/reports/surgecart-baseline-audit/engineering-inventory.md` |
