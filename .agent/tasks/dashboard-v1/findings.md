# Cleaner Findings

## Review Target

- Task：`dashboard-v1`
- 分支：`feat/backend-dashboard`
- 任务基线 Base：`f0bf42cbf8592215a76173ef75d2eac5078960e2`
- 审查对象 C1（Implementation Evidence Commit）：`65f7ed8dd4ec51610ed7787f81b9f7094f21c6fb`
- 当前 HEAD（C2 metadata，`review.status=PENDING`）：`54494ddcb040ca5209b38cc9c2efee760262038b`
- 审查范围：`Base..C1` 的全部相关变更（含新增文件，已用 `git diff --stat` 与 `git status` 双确认）
- 任务前已有修改：无（`git status --short` 为空，working tree clean）
- 全局资源：本任务未新增 migration、未新增错误码域（复用 `1001`），Registry ↔ Contract ↔ 实现三边一致（无漂移）
- 关键配置：新增 `dashboard.timezone=Asia/Shanghai`（默认），连接层 `time_zone=+08:00` 对齐

## Result

CHANGES_REQUIRED

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
| AC-010 | FAIL | 时间范围校验正确（单边缺省/start≥end/格式错→400/1001）、limit 上限(51)/负数(-1)→400/1001；但**非整数 limit 被静默接受**（`limit=abc`→200/0，`limit=12.5`→200/0），见 CLEAN-001 |
| AC-011 | PASS | 关键口径均有可信测试，覆盖空数据、日期边界、不同状态、非法参数、双身份权限、结果正确性；`TestDashboardOrdersTrend`/`TestDashboardOverviewMetrics` 等可区分口径正确/错误实现。唯一覆盖缺口：非整数 limit（见 CLEAN-001，随修复补回归测试） |
| AC-012 | PASS | `gofmt`（无输出）、`go build ./...`（exit 0）、`go vet ./...`（exit 0）、`go test -p 1 ./...`（全包 ok，含 order/flashsale/product/iam 回归） |
| AC-013 | PASS | `docs/design/dashboard.md` 与 APPROVED Contract、最终实现四者一致（口径/权限/时区/API/不变量逐项核对） |
| AC-014 | PASS | `docs/design/dashboard.md` §4 API 契约表 + 响应结构 + `api/dashboard/v1/dashboard.go` 结构体（json 标签 + dc 说明）提供各接口请求参数与响应字段 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `gofmt -l .` | PASS | 无输出 |
| `go test -count=1 -run 'TestDashboard' ./internal/cmd/` | PASS | `ok ... 4.801s` |
| `go test -p 1 -count=1 ./...` | PASS | 全包 `ok`（含 middleware/migrations/cmd/flashsale/iam 等） |
| `go test -count=1 ./...`（并行） | 非本任务引入的失败 | middleware/migrations 因共享 MySQL 跨包并行干扰（`admins` 表被并发 drop/recreate）。本仓库测试共用单一 DB，任务 AC-011 规定 `-p 1`；串行全量通过 |
| 隔离探针（worktree） | FAIL（复现 CLEAN-001） | `limit=abc`→status=200 code=0 data=map[items:[]]；`limit=12.5`→status=200 code=0 |
| Registry 三边一致性 | PASS | 未新增 migration/错误码域，`internal/codes/codes.go`、`internal/migrations/sql/*` 未改动 |

## Findings

### CLEAN-001：非整数 limit 参数被静默接受而非返回 400

- Severity：P2
- Status：OPEN
- Location：`api/dashboard/v1/dashboard.go`（`TopReq.Limit` 为 `int`，无 `v` 校验规则）+ `internal/logic/dashboard/dashboard.go`（`parseLimit` 收到的是 `gconv` 已静默转换后的值）
- AC / Invariant：AC-010「非法参数返回 400 稳定错误码」；Contract「参数错误复用 1001（400）」
- Trigger：`GET /admin/dashboard/products/top?limit=abc` 或 `limit=12.5`（非整数）
- Actual：GoFrame `gconv.Struct` 把 `"abc"` 静默转为 `int` 0，`parseLimit(0)` 走默认分支返回 10，接口返回 200/code=0/空 items，而非 400；`12.5` 同样返回 200
- Expected：非整数 limit 返回 400 与稳定错误码 1001
- Impact：非法参数未按契约拒绝，前端无法依赖 400 语义识别非法输入；`TestDashboardInvalidParams` 仅覆盖 limit=51/-1，未覆盖非整数，测试存在缺口
- Evidence：隔离 worktree 探针实测（见 Verification）；`TopReq.Limit` 无 `v` 标签；`gconv.Struct` 静默转换 `"abc"→0`
- Required Fix Boundary：非整数 limit 必须返回 400/1001（不得静默按默认值/截断处理），并补充覆盖该场景的回归测试（测试须能区分「拒绝」与「静默按默认值」）

### CLEAN-002：连接层时区采用固定偏移，DST 时区跨切换会漂移

- Severity：P3
- Status：OPEN
- Location：`internal/boot/boot.go` `databaseTimeZoneExtra`（用 `time.Now().In(loc).Zone()` 取「当前」偏移并固定为 DSN `time_zone`）
- AC / Invariant：INV-006（时区一致）；仅当 `dashboard.timezone` 配置为含 DST 的时区时影响
- Trigger：配置 `dashboard.timezone` 为含 DST 的时区（如 `America/New_York`），且运行跨 DST 切换
- Actual：MySQL 会话 `time_zone` 固定为 bootstrap 时刻偏移；Go 端 `businessLocation` 用 `time.LoadLocation` 为 DST 感知，切换后二者偏移不一致，日期边界漂移
- Expected：（若支持 DST 时区）连接层与 Go 边界计算在任意时刻偏移一致
- Impact：`Asia/Shanghai`（无 DST，默认且唯一指定值）无此问题，仅属潜在边界，不阻塞本任务
- Evidence：`databaseTimeZoneExtra` 取瞬时偏移 + `businessLocation` 逐请求 `LoadLocation` 的语义对比
- Required Fix Boundary：非阻塞，Owner 决定；若未来支持 DST 时区，需改为按时刻动态对齐或显式限定仅支持无 DST 时区
