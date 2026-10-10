# Cleaner Findings

## Review Target

- 任务基线（Base）：`72e066553f9f0947dac2862736cb95668765348a`（分支 `feat/admin-order-management`，working tree 干净）。
- Contract：`APPROVED`，target `b8c4cd34acc1299eb050deaeaa3738d62af4b72e`。
- 实现 Evidence Commit C1（最终审查对象）：`6e0e3c89a8e32d8be601fe58ea676968955a5d42`（`docs(order): 补充后台只读查询 EXPLAIN 证据与 created_at 无索引性能风险`）。该 commit 的祖先链完整包含业务代码 `b0e93d6`、长期设计 `76c03da` 与 EXPLAIN 证据本身。
- 首次审查对象：`b0e93d68ee266a4620248be39b4b3b11694fe3d4`；复审（本轮）审 C1' = `6e0e3c8`。
- 复审触发：CLEAN-001（AC-014 EXPLAIN 证据缺失）。修复提交 `6e0e3c8`（仅 `docs/design/order.md` +21 行）；`0802a5f` 为 C2'（仅 state.yaml 前移 `review.target`）。
- 全局资源：本任务不新增错误码域、不新增 migration，复用订单域 `9001` 与通用 `1001/1003`。
- 环境：MySQL 8.0（会话时区 = SYSTEM = UTC）、Redis 7 均经 docker-compose 就绪。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 分页列表 | PASS | `TestAdminOrderListPagination`（真实 MySQL）断言默认分页 total=5/page=1/size=20 与 id 倒序。 |
| AC-002 分页元数据 | PASS | 同用例覆盖默认值与 `page=0/-1`、`size=0/101`、`page=abc`、`size=xyz` 均 400/1001（拒绝非钳制）。 |
| AC-003 稳定排序 | PASS | `TestAdminOrderSortStable` 断言默认 `id DESC`、`created_at` 同键按 id tiebreak、翻页无重复/遗漏。 |
| AC-004 组合筛选 | PASS | `TestAdminOrderListFilters` + `TestAdminOrderListTimeRange`（`[start,end)` 左闭右开）。 |
| AC-005 参数校验 | PASS | 非法 status/user_id/sort/order/time 均 400/1001。 |
| AC-006 订单详情 | PASS | `TestAdminOrderDetailSnapshot` 断言任意用户订单详情完整字段。 |
| AC-007 历史快照 | PASS | 同用例：改 `skus.price=9999` 后详情金额/单价/名称仍为下单快照。 |
| AC-008 订单不存在 | PASS | 同用例断言 404/9001，与越权 403/1003 不混淆。 |
| AC-009 状态统计 | PASS | `TestAdminOrderStats` 覆盖 7 态、无数据态为 0、total=各态之和、不混入秒杀。 |
| AC-010 普通用户拒绝 | PASS | `TestAdminOrderReadOnlyPermission`：普通用户 token 三接口 403/1003。 |
| AC-011 无权限管理员拒绝 | PASS | 同用例：无权限管理员三接口 403/1003。 |
| AC-012 超级管理员 | PASS | 同用例：超管三接口 200；持权普通管理员命中放行。 |
| AC-013 无 N+1 | PASS | `AdminList` 仅 2 次查询（Count + Fields/Page/Scan），`AdminOrderListItem` 无 `items` 字段。 |
| AC-014 性能证据 | PASS | `docs/design/order.md` §10 已提交真实 SQL 与 EXPLAIN 证据（CLEAN-001 修复）；Cleaner 独立复核 6 条 EXPLAIN 与文档一致。 |
| AC-015 用户端无回归 | PASS | `go test -p 1 ./...` 全量通过（本轮复审复跑）。 |
| AC-016 发货/退款无回归 | PASS | 同上；未改动 `Ship`/`Refund`/`order:ship`/`order:refund`。 |
| AC-017 交易机制无回归 | PASS | 同上；未改状态机/库存补偿/事务。 |
| AC-018 长期设计更新 | PASS | `docs/design/order.md`（§1/§6/§9/§10）+ `docs/design/rbac.md`（§2.3 权限清单 40 项）与 APPROVED Contract、最终实现一致。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 本轮复审 exit 0 |
| `go vet ./...` | PASS | 本轮复审 exit 0 |
| `gofmt -l`（8 个变更 .go 文件） | PASS | 无输出（代码自首次审查未变） |
| `go test -p 1 ./internal/cmd -run 'TestAdminOrder|TestRouteTable'` | PASS | 本轮复审全 PASS |
| `go test -p 1 ./...` | PASS | 本轮复审全包通过（无 FAIL） |
| 生产代码是否变更（复审） | 无 | `git diff --stat b0e93d6..6e0e3c8 -- '*.go'` 为空；仅 `docs/design/order.md` 与 Cleaner artifacts 变化。 |
| EXPLAIN 证据复核 | PASS | 独立重跑 6 条核心查询 + 组合筛选（`status+user_id+created_at` → `idx_status_expire`/`Using where; Using filesort`），与 `docs/design/order.md` §10 表格逐项一致。 |
| 全局资源三边一致性 | PASS | Registry ↔ Contract ↔ 实现一致；`scripts/check-registry.sh` 无漂移。 |
| MySQL 会话时区 | PASS | `@@session.time_zone=SYSTEM`、`@@system_time_zone=UTC`，与 INV-007 一致。 |

## Findings

### CLEAN-001：AC-014 要求的真实 SQL 与 EXPLAIN 证据未提交

- Severity：P2
- Status：CLOSED
- Location：已修复于 `docs/design/order.md` §10（commit `6e0e3c8`）。
- AC / Invariant：AC-014（性能证据）。
- 修复验证：新增「后台只读查询性能与索引（EXPLAIN 证据）」小节，含 6 条核心查询的真实 SQL 形状 + `EXPLAIN` 关键列（type/key/Extra）+ 结论，以及 `created_at` 无索引全表扫描的性能风险、优化触发信号与后续走 Registry 预留的约束。Cleaner 独立重跑 EXPLAIN（含组合筛选）逐项一致，未发现编造或漂移。
- 结论：修复满足 Required Fix Boundary，证据真实、可复核、与 APPROVED Contract 索引决策一致；无新风险（纯文档，未动任何 .go 文件）。

（无其他开放 Finding）
