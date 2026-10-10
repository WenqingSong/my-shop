# Delivery Verification

## Milestone and Target

- Milestone：秒杀性能基线保存与简历可引用数据整理
- Delivery Target：`test/flashsale-performance-baseline` HEAD `78ceb69`（业务实现 `cb2a8fd`）
- Cleaner Review Target：`cb2a8fd`（Owner 已重新 ACCEPT 并绑定 `owner.review_target=cb2a8fd`）
- Target Match：YES（业务实现 = `cb2a8fd` = 当前 `review.target`）

## Re-verification（本轮背景）

上一轮 `BLOCKED`：`owner.review_target=e67cf28` 未绑定 `review.target=cb2a8fd`。Owner 于提交 `78ceb69` 重新 ACCEPT `cb2a8fd`（`CLEAN-001` banner 占位图决定延期、保留 OPEN）。本轮 `delivery-start` Gate PASS，进入真实三档压测运行验收。

## Environment

- OS：Linux（容器内执行）
- Go：1.24.1（`go version` 实测）
- MySQL：8.0.46（docker 容器 `my-shop-mysql`）
- Redis：7.4.11（docker 容器 `my-shop-redis`）
- Prometheus：`my-shop-prometheus:local`（cb2a8fd 镜像内置配置，`/-/healthy` 200，容器内 `/etc/prometheus/prometheus.yml` 882B、`/etc/prometheus/rules/flashsale_latency.yml` 1456B 就位）
- Docker：29.6.2 / Compose v5.3.1
- 服务：`go build`（HEAD `78ceb69`，`go version -m` 确认模块版本 `78ceb69c117c`）→ `serve`，容量保护全开 + `/metrics` 开启
- 容量保护阈值：活动级限流 window=1s/max=100、排队软上限 max_queued_per_sku=100、熔断默认（5/10s/1）、用户级限流不限
- 测试数据：`activity_id=1`、`sku_id=1`、`total_stock=1000`、`flash_price=50`，时间窗 `NOW()-2h ~ NOW()+12h`
- 配置来源：`.env`（七牛凭据已注入，不记录 Secret 值）+ 环境变量容量保护开关（`FLASH_SALE_*`）
- 隔离与清理：开发环境 MySQL/Redis；压测前 `sold=0` 重置、清空 `flash_sale_orders`/`flash_sale_order_requests`、`redis FLUSHDB` 后由对账扫描器重新预热
- develop 基线：验证起始 `f0bf42c`；验证期间 `origin/develop` 前进至 `3523cd20`（Analyst 的 Registry-only 提交「预留 migration 20261001000019（iam-v5）」，仅改 `.agent/registry/migrations.md`，与本 feature 无文件重叠）。已 `git merge-tree` 确认 feature 在 `3523cd20` 上无冲突，`develop_base` 校正为 `3523cd20`

## Verification

| Check | Result | Evidence |
|---|---|---|
| `delivery-start` Gate | PASS | exit 0 |
| `go build ./...` / `go vet ./...` | PASS | exit 0 |
| `bash -n scripts/flashsale-loadtest/*.sh` | PASS | 5 个脚本语法通过 |
| `bash scripts/flashsale-loadtest/test-run.sh` | PASS | 输出「test-run.sh 全部通过」，EXIT=0 |
| 无 Go 生产代码 / migration / 协议改动 | PASS | `git diff --name-status 8106d22..HEAD` 仅 loadtest 脚本 + prometheus 部署 + task artifacts，无 `*.go`/`*.sql` |
| 服务启动 + 容量保护 + /metrics | PASS | `/health` 200、`/metrics` 200（含 `flashsale_*`）；Redis 预热 `flashsale:stock:1:1=1000` |
| low/medium/high 三档压测 | PASS | 各 1000 有效请求，三档 `correctness=PASS`、`sample_insufficient=false` |
| 基线 JSON 元数据（AC-001/002） | PASS | 3 个基线 JSON 均含 hardware / runtime / promql / loadtest command+时间范围 / stock / concurrency，字段与实际环境核对一致 |
| 简历摘要可回溯（AC-003） | PASS | 摘要每个数字可回溯到对应基线 JSON，无估算/理论值，PromQL 与 rules 文件逐字一致 |
| MySQL 正确性独立复核 | PASS | `sold=559 ≤ total_stock=1000`；`orders=559=sold`；`one_per_user=0`、`idem=0`；`queued_steady=0` |

### 三档压测结果（`total_stock=1000`，累计 sold）

| 档位 | 并发 | 总请求 | 有效 | queued | 429（比例） | 503 | p50 | p95 | p99 | queued 峰值 | sold（累计） |
|---|---|---|---|---|---|---|---|---|---|---|---|
| low | 20 | 1000 | 1000 | 206 | 794（79.40%） | 0 | 1.05ms | 65.11ms | 84.20ms | 111 | 206 |
| medium | 100 | 1000 | 1000 | 182 | 818（81.80%） | 0 | 1.16ms | 243.02ms | 396.04ms | 168 | 388 |
| high | 200 | 1000 | 1000 | 171 | 829（82.90%） | 0 | 6.71ms | 338.91ms | 367.88ms | 100 | 559 |

### 指标对账（`GET /metrics` 秒杀域累计）

`flashsale_requests_total`：`order/queued=560`、`order/rate_limited=2100`、`order/queue_full=341`、`consume/success=560`。合计 `3001 = 3000（三档）+ 1（冒烟）`；`queued=560 = MySQL sold(559) + 冒烟 1 单`（冒烟单在压测前已从 MySQL 重置、但进程内指标计数器未重置，属已知 1 单位偏差，非正确性违例）。

## Acceptance Evidence

- AC-001（基线含硬件/运行时/库存/并发，与真实环境一致）：PASS——三档 baseline 均含 `hardware`（8 核 / AMD EPYC 9K65 192-Core / 16777216 kB）、`runtime`（go1.24.1 / MySQL 8.0.46 / Redis 7.4.11）、`sold`/`total_stock`、`concurrency`（20/100/200），与 `nproc`/`lscpu`/`/proc/meminfo`/`go version`/`SELECT VERSION()`/`redis-cli INFO` 实测一致。
- AC-002（基线含 PromQL / 命令 / 时间范围）：PASS——`promql.queries` 的 p95/p99 表达式与 `prometheus/rules/flashsale_latency.yml` 逐字一致；`loadtest.command` 含关键环境变量；`start/end_epoch_seconds` 与 `duration_seconds` 齐全。
- AC-003（摘要只含实测数字、可回溯）：PASS——摘要每项数字均回溯到对应基线 JSON，无估计/理论值；未测量项标记逻辑由 `test-run.sh` 覆盖（本轮所有字段均实测，无「未测量」项）。
- AC-004（三档产出、sample_insufficient=false）：PASS——low/medium/high 三档 `sample_insufficient=false`（有效请求 1000 ≥ MIN_SAMPLES=1000）。
- AC-005（不改 Go 代码、bash -n + test-run.sh 通过）：PASS——无 Go/migration/协议改动；`bash -n` 与 `test-run.sh` 均通过。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 熔断 Open→Half-Open 实际触发（503 非零） | Redis 全程健康，闸门无故障，熔断保持 Closed（符合预期，不人为制造故障） | 熔断降级路径未在本轮真实触发，其正确性由 `flash-sale-v5` 交付验证覆盖 |

## Remaining Risks

1. `runtime.redis_version` 含尾部 `\r`（`docker exec redis-cli INFO server` 输出带回车，经 `json_escape` 转义为 `\r`），为轻微数据格式瑕疵，不影响版本可读性与 AC。
2. 环境变量 `FLASH_SALE_RECONCILE_SCAN_INTERVAL` 未生效：`internal/cmd/cmd.go` 扫描器用 `g.Cfg().MustGet`（仅读 config.yaml，默认 60s），而容量保护开关用 `GetEffective`（读环境变量）。本轮 Redis 预热由对账扫描器在 ~60s 后完成。属既有代码行为、非本任务引入，不在本任务 Scope（不改 Go 代码）。
3. 指标 `order/queued=560` 比 MySQL `sold=559` 多 1，源于压测前的冒烟单（已从 MySQL 重置、进程内计数器未重置），不影响正确性结论。
4. `storage/banners/banner-{1,2,3}.png`（各 87 字节）随 `cb2a8fd` 入库，Owner 已决定延期处理（`CLEAN-001` 保留 OPEN）。

## Result

PASS
