# Cleaner Findings

## Review Target

- 任务基线（Base）：`72e066553f9f0947dac2862736cb95668765348a`（分支 `feat/admin-order-management`，working tree 干净，无既有未提交修改）。
- Contract：`APPROVED`，target `b8c4cd34acc1299eb050deaeaa3738d62af4b72e`。
- 实现 Evidence Commit C1（审查对象）：`b0e93d68ee266a4620248be39b4b3b11694fe3d4`（`feat(order): 后台订单只读管理…`）。
- 当前 HEAD：`3049e5f`（C2 metadata commit `chore(workflow): review PENDING`），`review.target = C1`。
- 相关变更文件（`72e0665..b0e93d6`）：`api/order/v1/order.go`、`internal/boot/seed.go`、`internal/cmd/order_admin_test.go`（新增）、`internal/cmd/routes_admin.go`、`internal/cmd/routes_test.go`、`internal/controller/order/order.go`、`internal/logic/order/order.go`、`internal/service/order.go`；长期设计 `docs/design/order.md`、`docs/design/rbac.md` 在 Analyst 提交 `76c03da` 更新。
- 全局资源：本任务不新增错误码域、不新增 migration（Contract 已声明，state `resources.reservations={}`），复用订单域 `9001` 与通用 `1001/1003`。
- 环境：MySQL 8.0（会话时区 = SYSTEM = UTC）、Redis 7 均经 docker-compose 就绪。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 分页列表 | PASS | `TestAdminOrderListPagination`（真实 MySQL）断言默认分页 total=5/page=1/size=20 与 id 倒序；`go test -p 1 ./internal/cmd -run TestAdminOrderListPagination` 通过。 |
| AC-002 分页元数据 | PASS | 同上用例覆盖默认值与 `page=0/-1`、`size=0/101`、`page=abc`、`size=xyz` 均 400/1001（拒绝非钳制）。 |
| AC-003 稳定排序 | PASS | `TestAdminOrderSortStable` 断言默认 `id DESC`、`sort=created_at&order=asc` 同键按 id tiebreak、翻页无重复/遗漏。 |
| AC-004 组合筛选 | PASS | `TestAdminOrderListFilters`（order_no/user_id/status 独立与组合）+ `TestAdminOrderListTimeRange`（`[start,end)` 左闭右开，含边界排除）。 |
| AC-005 参数校验 | PASS | 非法 status/user_id/sort/order/time（缺偏移、start>=end、start==end）均 400/1001；`order` 白名单 `{id,created_at}` + asc/desc。 |
| AC-006 订单详情 | PASS | `TestAdminOrderDetailSnapshot` 断言任意用户订单详情 id/order_no/user_id/status/金额/明细/快照。 |
| AC-007 历史快照 | PASS | 同用例：改 `skus.price=9999` 后详情 `total_amount`/`items[].price`/`sku_name` 仍为下单快照（不重读当前售价）。 |
| AC-008 订单不存在 | PASS | 同用例断言 `/admin/orders/99999999` → 404/9001，与越权（403/1003）不混淆。 |
| AC-009 状态统计 | PASS | `TestAdminOrderStats` 覆盖 7 态计数、无数据态为 0、total=各态之和、不混入 `flash_sale_orders`。 |
| AC-010 普通用户拒绝 | PASS | `TestAdminOrderReadOnlyPermission`：普通用户 token（type=user）访问列表/统计/详情均 403/1003。 |
| AC-011 无权限管理员拒绝 | PASS | 同用例：无权限普通管理员三接口均 403/1003。 |
| AC-012 超级管理员 | PASS | 同用例：超管三接口均 200；另验证持 `order:list`/`order:view` 的普通管理员命中放行。 |
| AC-013 无 N+1 | PASS | `AdminList` 仅 2 次查询（1 次 `Count` + 1 次 `Fields(...).Page(...).Scan`），`AdminOrderListItem` 无 `items` 字段，结构上无逐单加载路径。 |
| AC-014 性能证据 | FAIL | 仓库内无可核实的 EXPLAIN 证据（见 CLEAN-001）。Cleaner 独立执行 EXPLAIN 结果与 Contract 索引结论一致，但证据未以任何 committed artifact 落盘。 |
| AC-015 用户端无回归 | PASS | `go test -p 1 ./...` 全量通过（含 `internal/cmd` 订单闭环用例）。 |
| AC-016 发货/退款无回归 | PASS | 同上全量测试通过；本任务未改动 `Ship`/`Refund`/`order:ship`/`order:refund`。 |
| AC-017 交易机制无回归 | PASS | 同上全量测试通过；本任务未改状态机/库存补偿/事务。 |
| AC-018 长期设计更新 | PASS | `docs/design/order.md`（§1 职责、§6 后台只读路由/权限/查询语义、§9 移除后台列表留白）与 `docs/design/rbac.md`（§2.3 权限清单 40 项、order 4 项）与 APPROVED Contract、最终实现一致。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `gofmt -l`（8 个变更 .go 文件） | PASS | 无输出 |
| `go test -p 1 ./internal/cmd -run 'TestAdminOrder|TestRouteTable'` | PASS | 8 个 AdminOrder 用例 + TestRouteTable 全 PASS |
| `go test -p 1 ./...` | PASS | 全包通过（无回归） |
| 全局资源三边一致性 | PASS | Registry（订单域 9000-9999 ACTIVE、通用 1000-1999 ACTIVE）↔ Contract（不新增域/migration）↔ 实现（复用 1001/1003/9001，无新码、无新 migration）一致；`scripts/check-registry.sh` 校验通过（无漂移）。 |
| MySQL 会话时区 | PASS | `@@session.time_zone=SYSTEM`、`@@system_time_zone=UTC`、`NOW()` 为 UTC，与 Contract INV-007 时间基准一致。 |
| EXPLAIN（6 条核心查询，独立执行） | 一致 | 默认 `ORDER BY id DESC` 走 `PRIMARY`（Backward index scan）；`status=20` 走 `idx_status_expire`（ref）+ filesort；`order_no` 走 `uk_order_no`（const）；`user_id` 走 `uk_user_idempotency`/`idx_user_id`（ref）+ filesort；`created_at` 范围走 `PRIMARY` 全扫（Using where + Backward index scan）；`GROUP BY status` 走 `idx_status_expire`（Using index）。与 Contract「不新增 created_at 索引」的风险预判一致。 |

## Findings

### CLEAN-001：AC-014 要求的真实 SQL 与 EXPLAIN 证据未提交

- Severity：P2
- Status：OPEN
- Location：仓库内无任何 EXPLAIN/执行计划证据（`git grep -i explain` 无匹配；`docs/design/order.md` 亦未记录 created_at 全表扫描性能风险）。
- AC / Invariant：AC-014（性能证据）；Contract §Selected Design 1「Coder 须对核心 list/stats 查询提供真实 SQL 与 EXPLAIN 证据，明确现有索引适用范围与性能风险」；Owner Decision Record「要求核心 SQL 提供 EXPLAIN 证据并明确性能风险」。
- Trigger：直接核对仓库 committed 内容，无 EXPLAIN 证据 artifact。
- Actual：功能实现正确，但「真实 SQL + EXPLAIN 执行计划 + created_at 无索引全表扫描/filesort 的性能风险与优化触发信号」未以任何可复核形式落盘。
- Expected：核心 list/stats 查询的真实 SQL 与 EXPLAIN 证据应以 durable、可复核形式提交（建议 `docs/design/order.md` 新增性能小节，或任务级证据文件），并明确 created_at 时间范围筛选会全表扫描的已知取舍与后续优化触发条件。
- Impact：Owner 无法从仓库复核「不新增 created_at 索引」这一已批准决策的证据基础；性能风险说明未进入长期设计，未来维护者可能低估时间范围筛选在数据量增长后的退化。
- Evidence：`git grep -in 'explain\|执行计划\|全表扫描\|filesort'` 全仓库 0 匹配；`docs/design/order.md` §6 只描述时间范围语义（FROM_UNIXTIME），未记录性能风险。Cleaner 已独立执行 EXPLAIN（见 Verification 表），结果证明查询行为与 Contract 一致，但该证据为 Cleaner 临时运行，不能替代 Coder 的 committed 交付。
- Required Fix Boundary：Coder 补充并提交「核心 list/stats 查询的真实 SQL + EXPLAIN 执行计划 + created_at 无索引全表扫描的明确性能风险与优化触发信号」，落在 durable、可复核位置，内容须与 APPROVED Contract 的索引决策一致；不要求改动任何查询实现或新增索引。
