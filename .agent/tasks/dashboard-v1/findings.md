# Cleaner Findings

## Review Target

- Task：`dashboard-v1`
- 分支：`feat/backend-dashboard`
- 任务基线 Base：`f0bf42cbf8592215a76173ef75d2eac5078960e2`
- 首次审查对象 C1：`65f7ed8dd4ec51610ed7787f81b9f7094f21c6fb`（结论 CHANGES_REQUIRED）
- **复审对象 C1'（本次审查的 immutable Implementation Evidence Commit）**：`37467cfd2456eb5c0207c12b84b8108791ed7100`
- 复审 metadata C2'（`review.status=PENDING`）：`675f2b2`
- 审查范围：`Base..C1'` 的全部相关变更（含修复 commit `37467cf`，已用 `git diff --stat` 与 `git show` 双确认）
- 任务前已有修改：无（working tree 起点 clean）
- 全局资源：本任务及修复均未新增 migration、未新增错误码域（复用 `1001`），Registry ↔ Contract ↔ 实现三边一致（`scripts/check-registry.sh` 校验通过，无漂移）
- 关键配置：`dashboard.timezone=Asia/Shanghai`（默认），连接层 `time_zone=+08:00` 对齐；仅支持无 DST 时区

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestDashboardPermissionBoundary`：超管全接口 200/0；无 `dashboard:view` 普通管理员 403/1003；普通用户 token 403/1003；无 token 401/1002；拒绝路径 orders 计数不变。非法/失效 token→401 由未改动的 `AdminAuth` 中间件继承（`internal/middleware/auth_test.go` 覆盖，全量测试通过） |
| AC-002 | PASS | `TestDashboardOverviewMetrics`：user_total=3 / today_new_users=1 / product_total=3 / order_total=6 / today_new_orders=5，逐项与真实表聚合一致 |
| AC-003 | PASS | 同 `TestDashboardOverviewMetrics`：`today_order_amount=3000`（仅 20/30 有效状态，排除 10/60/70）；字段名 `today_order_amount`，无「实收销售额」/`received_sales` |
| AC-004 | PASS | `TestDashboardOrdersTrend`：7 天连续、空日 0、边界（6 天前含、7 天前不含）不重不漏、逐日求和=7 |
| AC-005 | PASS | `TestDashboardOrdersStatusAndRange`：7 状态显式返回（含 0），分布计数与 `GROUP BY status` 一致 |
| AC-006 | PASS | 同测试：时间范围左闭右开 `[start,end)`，边界订单（06 日 00:00、04 日）正确排除，total=2 |
| AC-007 | PASS | `TestDashboardProductsTop`：单条 SQL 聚合 + 排序 + LIMIT，无 N+1；排名/销量/名称正确（P3>P1>P2）；limit=2 生效 |
| AC-008 | PASS | 同测试：已取消(60)/待支付(10)订单明细不计入销量，销量只来自 `order_items` JOIN `orders.status∈{20,30,40,50}` |
| AC-009 | PASS | `TestDashboardFlashSales`：activity_total=2 / enabled=1 / disabled=1 / queued=2 / success=1 / failed=3 / dead=1 / current_queued=2 / success_orders=4；`queued` 不计入成功订单（成功订单以 `flash_sale_orders` 行为事实源） |
| AC-010 | PASS | `TestDashboardInvalidParams`：时间范围非法（单边缺省/start≥end/格式错）→400/1001、limit=51/-1→400/1001、**limit=abc→400/1001、limit=12.5→400/1001**（CLEAN-001 修复后实测）；`TestParseLimit` 单元覆盖非整数/0/负数/超上限均拒绝 |
| AC-011 | PASS | 关键口径均有可信测试，覆盖空数据、日期边界、不同状态、非法参数、双身份权限、结果正确性；`TestDashboardOrdersTrend`/`TestDashboardOverviewMetrics`/`TestParseLimit` 可区分口径正确/错误实现 |
| AC-012 | PASS | `gofmt`（无输出）、`go build ./...`（exit 0）、`go vet ./...`（exit 0）、`go test -p 1 -count=1 ./...`（exit 0，全包 ok，含 order/flashsale/product/iam 回归） |
| AC-013 | PASS | `docs/design/dashboard.md` 与 APPROVED Contract、最终实现四者一致（口径/权限/时区/API/不变量逐项核对，含修复后的 limit 参数与 DST 限制） |
| AC-014 | PASS | `docs/design/dashboard.md` §4 API 契约表 + 响应结构 + `api/dashboard/v1/dashboard.go` 结构体提供各接口请求参数与响应字段 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `gofmt -l .` | PASS | 无输出 |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `go test -count=1 -run 'TestParseLimit\|TestDashboardHasDST' ./internal/logic/dashboard/` | PASS | `ok ... 0.005s`，CLEAN-001/CLEAN-002 回归单测通过 |
| `go test -count=1 -run 'TestDatabaseTimeZoneExtra\|TestHasDST\|...' ./internal/boot/` | PASS | `ok ... 0.007s`，含 DST 拒绝与 DSN 参数断言 |
| `go test -count=1 -run 'TestDashboard' ./internal/cmd/` | PASS | `ok ... 4.264s`，含 HTTP 层 limit=abc/12.5→400/1001 回归 |
| `go test -p 1 -count=1 ./...` | PASS | exit 0，全包 `ok`（含 middleware/migrations/cmd/flashsale/iam 等），无 FAIL/panic |
| `read_lints` | PASS | totalCount=0 |
| `scripts/check-registry.sh` | PASS | 校验通过：未发现 Reservation 重复或 Registry ↔ 实现明显不一致 |

## Findings

### CLEAN-001：非整数 limit 参数被静默接受而非返回 400（已修复）

- Severity：P2
- Status：CLOSED
- Location：`api/dashboard/v1/dashboard.go`（`TopReq.Limit` 改为 `string`）+ `internal/logic/dashboard/dashboard.go`（`parseLimit` 改用 `strconv.Atoi` 严格解析）
- AC / Invariant：AC-010「非法参数返回 400 稳定错误码」；Contract「参数错误复用 1001（400）」
- 修复验证：`parseLimit` 对空串→默认 10；`strconv.Atoi` 失败（abc/12.5）或 `n<=0`/`n>50` → `1001`；`TestParseLimit`（10 组用例）与 `TestDashboardInvalidParams`（HTTP 层 `limit=abc`/`limit=12.5`→400/1001）均 PASS
- 复审结论：原触发条件已消除，回归测试可区分「拒绝」与「静默按默认值」，关闭

### CLEAN-002：连接层时区采用固定偏移，DST 时区跨切换会漂移（已修复）

- Severity：P3
- Status：CLOSED
- Location：`internal/boot/boot.go`（`databaseTimeZoneExtra` 增加 `hasDST` fail-fast）+ `internal/logic/dashboard/dashboard.go`（`businessLocation` 增加 `hasDST` fail-closed）
- AC / Invariant：INV-006（时区一致）；仅当配置含 DST 的时区时影响
- 修复验证：`hasDST` 采样当前年每日偏移，`Asia/Shanghai` 判定 false、`America/New_York` 判定 true；`TestDatabaseTimeZoneExtraRejectsDST`/`TestHasDST`/`TestDashboardHasDST` 均 PASS；`docs/design/dashboard.md` §3/§11 与 `config.yaml` 注释同步说明「仅支持无 DST 时区」
- 复审结论：默认 `Asia/Shanghai` 无 DST，且对 DST 时区 fail-fast 拒绝（不再静默漂移），关闭

无其他可行动 Finding。
