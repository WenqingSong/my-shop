# Delivery Verification

## Milestone and Target

- Milestone：Dashboard V1 运营统计后端
- Delivery Target：`feat/backend-dashboard` 的 CLEAN 实现 snapshot（review.target + review-neutral tail）
- Cleaner Review Target：`37467cfd2456eb5c0207c12b84b8108791ed7100`
- Target Match：YES（`feature_head=e6d93e2` 为 `37467cf` + 仅 review-neutral tail `675f2b2`/`9ac8fd3`/`e6d93e2`；`git diff --name-only 37467cf..e6d93e2` 仅含 `.agent/tasks/dashboard-v1/{state.yaml,findings.md,core-logic.md,owner-decision.md}`，无实质代码变化）

## Environment

- OS：Linux（容器内执行）
- Go：1.24.1
- MySQL：8.0.46（docker-compose，healthy；`@@system_time_zone=UTC`，会话时区经 DSN `time_zone` 对齐为 `+08:00`）
- Redis：7.4.11（docker-compose，healthy）
- Docker：29.6.2 / Compose v5.3.1
- 交付对象：`go build ./...` 源码构建 + `go build -o /tmp/my-shop-delivery .` 产物；无未提交文件 / 本机绝对路径依赖
- 配置来源：`manifest/config/config.yaml`（默认值）+ 环境变量（`AUTH_JWT_SECRET`、`ADMIN_SUPER_PASSWORD`，由测试 harness `t.Setenv` 注入，不记录 Secret 值）
- 隔离与清理：Dashboard 集成测试启动前清空身份/业务表 + `redis FLUSHDB`，与共享/生产数据隔离；未部署或修改生产环境

## Verification

| Check | Result | Evidence |
|---|---|---|
| `delivery-start` Gate | PASS | `.agent/bin/workflow-check gate delivery-start .agent/tasks/dashboard-v1` exit 0 |
| `gofmt -l .` | PASS | 无输出 |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `go build -o /tmp/my-shop-delivery .` | PASS | 产物可生成 |
| `go test -p 1 -count=1 ./...` | PASS | 全包 `ok`、无 FAIL（含 order/flashsale/iam/product/migrations 回归） |
| Dashboard 集成测试（独立执行） | PASS | `go test -count=1 -v -run 'TestDashboard' ./internal/cmd/` 8 个用例全 PASS（真实路由 + 真实 MySQL/Redis）；`TestParseLimit`/`TestDashboardHasDST`/`TestDatabaseTimeZone*`/`TestHasDST` 全 PASS |
| `read_lints` | PASS | totalCount=0 |

## Acceptance Evidence

本次由 Deliverer 独立执行的证据（非引用 Cleaner/Coder 报告）：

- AC-001 / INV-001（身份与权限边界）：`TestDashboardPermissionBoundary` PASS —— 超管 5 接口全 200/0；无 `dashboard:view` 普通管理员 403/1003；普通用户 token 403/1003；无 token 401/1002；拒绝路径 `orders` 计数不变（零写入）。
- AC-002 / AC-003 / INV-002（运营概览与金额口径）：`TestDashboardOverviewMetrics` PASS —— `user_total=3 / today_new_users=1 / product_total=3 / order_total=6 / today_new_orders=5 / today_order_amount=3000`（仅状态 20/30 计入，排除 10/60/70）；字段名 `today_order_amount`，无 `received_sales`/「实收销售额」。
- AC-004 / INV-005（订单趋势）：`TestDashboardOrdersTrend` PASS —— 7 天连续、空日 0、边界（6 天前含/7 天前不含）不重不漏、逐日求和=7。
- AC-005 / AC-006（状态分布与时间范围）：`TestDashboardOrdersStatusAndRange` PASS —— 7 状态显式返回（含 0）；时间范围左闭右开 `[start,end)`，边界订单正确排除。
- AC-007 / AC-008 / INV-003（销量排行与口径）：`TestDashboardProductsTop` PASS —— 单条 SQL 聚合+排序+LIMIT；已取消(60)/待支付(10)明细不计销量；排名 P3(10)>P1(7)>P2(3)；limit=2 生效。
- AC-009 / INV-004（秒杀运营统计）：`TestDashboardFlashSales` PASS —— `activity_total=2 / enabled=1 / disabled=1 / queued=2 / success=1 / failed=3 / dead=1 / current_queued=2 / success_orders=4`；`queued` 不计入成功订单（成功订单以 `flash_sale_orders` 行为事实源）。
- AC-010（参数校验与上限）：`TestDashboardInvalidParams` PASS —— 非法时间范围（单边缺省/start≥end/格式错）与超上限/负数/非整数 limit 均 400/1001。
- AC-011（自动化测试）：`TestParseLimit`（10 例）、`TestDashboardHasDST`、`TestDatabaseTimeZoneExtra`/`TestDatabaseTimeZoneExtraRejectsDST`/`TestHasDST` 全 PASS；关键口径测试可区分正确/错误实现。
- AC-012（无回归）：`go test -p 1 -count=1 ./...` 全包 `ok`。
- AC-013（长期设计）：`docs/design/dashboard.md`（10878 字节）与 APPROVED Contract 口径/权限/时区/API 一致。
- AC-014（接口示例）：`docs/design/dashboard.md` §4 API 契约表 + `api/dashboard/v1/dashboard.go` 结构体提供各接口请求/响应。
- INV-006（时区一致）：`TestDashboardTimezoneSessionAlignment` PASS —— `@@session.time_zone=+08:00`，与 `dashboard.timezone=Asia/Shanghai` 对齐；启动 fail-fast 拒绝 DST 时区。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 完整 `serve` 启动 smoke | 预存在七牛 required-dependency：`serve` 在 `service.Upload().ValidateConfig` fail-fast，本环境无七牛凭据（`qiniu.access_key 未配置`，exit 1） | 该依赖在本任务基线（`f0bf42c` / develop_base `3523cd2`）已存在，由 object-storage-upload 任务引入，与 Dashboard 只读统计无关；Dashboard 的「启动+运行+API+数据」已由集成测试（真实路由 + 真实 MySQL/Redis）完整验证 |

## Remaining Risks

- 完整 `serve` 启动依赖七牛凭据（AK/SK/bucket/domain + 真实 `GetBucketInfo`），本环境无凭据无法演练生产入口启动。此为**预存在、跨模块**的环境要求，非本里程碑引入、非 Dashboard 缺陷；Owner 可决定是否将「完整 serve 启动」纳入本里程碑交付范围。
- MySQL 服务器时钟为 UTC（`@@system_time_zone=UTC`），Dashboard 会话经 DSN `time_zone='+08:00'` 对齐，与 Contract 一致；生产若为其他时区需按 `dashboard.timezone` 重新对齐。
- `origin/develop` 在验证期间前进 1 个 registry-only commit（`3523cd2`，iam-v5 的 migration 预留），仅改 `.agent/registry/migrations.md`，无 Go 代码冲突，不影响本交付。

## Result

PASS
