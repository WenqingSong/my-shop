# Delivery Verification

## Milestone and Target

- Milestone：秒杀性能基线保存与简历可引用数据整理
- Delivery Target：`test/flashsale-performance-baseline` HEAD `7409e03616cda2f3e39455974637dfa7bbc77a3b`
- Cleaner Review Target：`e67cf2829ecdf6d9ff0febc66f10a406a5fa092f`
- Target Match：YES（`7409e03` 相对 `e67cf28` 仅新增 review / owner / 上一轮 delivery 元数据提交，`run.sh` / `make-summary.sh` / `test-run.sh` 与 Go 生产代码未变）

## Environment

- OS：Linux（容器内执行）
- Go：1.24.1（`go version` 实测）
- MySQL：8.0.46（docker 容器 `my-shop-mysql`）
- Redis：7.4.11（docker 容器 `my-shop-redis`）
- Prometheus：未启动（本任务压测 p95/p99 由 run.sh 自测 curl 延迟，PromQL 为记录值；Prometheus 抓取非里程碑必需）
- 服务：`go build` → `migrate up` → `serve`（容量保护全开、`/metrics` 开启、`reconcile_scan_interval=1`）
- 测试数据：`activity_id=1`、`sku_id=1`、`total_stock=1000`、`flash_price=50`，活动时间窗用宽窗口（`2026-10-08 00:00 ~ 2026-10-12 00:00`）
- 配置来源：`.env`（七牛凭据已注入）；`ADMIN_SUPER_PASSWORD` 缺失，本轮以运行时注入 dev 值 `dev-admin-pass-123` 启动（未写入 .env，未记录生产 Secret）
- 时区说明：MySQL 容器为 UTC，服务 DB 连接按 Local(CST) 解释 DATETIME（存在 8h 漂移）；本轮以宽时间窗规避，不影响闸门时间窗判断

## Verification

| Check | Result | Evidence |
|---|---|---|
| `delivery-start` Gate | FAIL | 工作区非 clean（`docker-compose.yml`、`scripts/up.sh` 修改 + `prometheus/Dockerfile` 新增，为 Owner 的 Prometheus 环境修复，超出本任务范围） |
| `go build ./...` / `go vet ./...` | PASS | exit 0 |
| `bash -n scripts/flashsale-loadtest/*.sh` | PASS | 5 个脚本语法通过 |
| `bash scripts/flashsale-loadtest/test-run.sh` | PASS | 「test-run.sh 全部通过」，EXIT=0 |
| 无 Go 生产代码 / migration / 协议改动 | PASS | `git diff origin/develop..HEAD` 仅 3 个 loadtest 脚本 + 本 task artifacts（`scripts/test.sh` 差异为 develop 侧 `02c3b92` 独立重构） |
| 服务启动（build+migrate+serve） | PASS | `/health` 200、`/metrics` 200、超管 seed 成功、活动 Redis 预热成功（stock=1000） |
| low 档压测（并发 20） | PASS | queued=136、rejected=429 864 条、p99=89.4ms、sold=136、correctness=PASS |
| medium 档压测（并发 100） | PASS | queued=139、rejected=429 861 条、p99=385.9ms、sold=275、correctness=PASS |
| high 档压测（并发 200） | PASS | queued=200、rejected=429 800 条、p99=504.9ms、sold=475、correctness=PASS |
| 基线 JSON 元数据（AC-001/002） | PASS | 三档均含 hardware / runtime / promql（与 `prometheus/rules/flashsale_latency.yml` 逐字一致）/ loadtest command+时间窗 / stock / concurrency |
| 简历摘要可回溯（AC-003） | PASS | 摘要数字与基线 JSON 一致，仅实测数字，p95/p99 为自测值非估计值 |
| MySQL 正确性独立复核 | PASS | sold=475 ≤ total_stock=1000；orders=475=sold；one_per_user=0；idem=0；queued_backlog=0 |
| 指标对账（INV-021） | PASS | `order/queued=476`、`order/rate_limited=2222`、`order/queue_full=303`、`consume/success=476`，与三档 queued=475+1(smoke) 对账一致 |

## Acceptance Evidence

- AC-001（基线含硬件/运行时/库存/并发，与真实环境一致）：PASS——三档 baseline JSON 均含 `hardware`（8 核 / AMD EPYC / 16GB）、`runtime`（Go 1.24.1 / MySQL 8.0.46 / Redis 7.4.11）、`stock`（1000/售出）、`concurrency`（20/100/200）。
- AC-002（基线含 PromQL / 命令 / 时间范围）：PASS——`promql.queries` 的 p95/p99 表达式与 `prometheus/rules/flashsale_latency.yml` 逐字一致；`loadtest.command` 含关键环境变量；`start_epoch_seconds`/`end_epoch_seconds`/`duration_seconds` 齐全。
- AC-003（摘要只含实测数字、可回溯）：PASS——摘要每项数字均能回溯到对应基线 JSON 字段，无估计/理论值。
- AC-004（三档均产出、sample_insufficient=false）：PASS——low/medium/high 三档 `sample_insufficient=false`（valid=1000 ≥ MIN_SAMPLES=1000）。
- AC-005（不改 Go 代码、bash -n + test-run.sh 通过）：PASS——无 Go/migration/协议改动；`bash -n` 与 `test-run.sh` 均通过。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| Prometheus 抓取/查询验证 | Prometheus 容器未启动（本轮以 run.sh 自测延迟为准，PromQL 仅作记录） | 低——PromQL 已逐字核对，非本里程碑硬性运行时依赖 |

## Remaining Risks

1. 工作区非 clean：`docker-compose.yml`、`scripts/up.sh` 修改与 `prometheus/Dockerfile` 新增（Owner 为嵌套容器环境修复 Prometheus bind mount 的未提交改动，超出本任务范围），使 `delivery-start` 与 `merge-ready` Gate 的 clean 校验失败。需 Owner 将其提交至独立分支/任务或丢弃后，方可走 merge-ready。
2. `.env` 缺 `ADMIN_SUPER_PASSWORD`（`AUTH_JWT_SECRET` 为空但走 config 默认值），首次启动超管 seed 依赖该值；本轮以运行时 dev 值注入。Owner 应在 `.env` 补齐以保可复现。
3. MySQL 容器（UTC）与服务 DB 连接（Local/CST）存在 8h DATETIME 解释漂移；本轮用宽时间窗规避，未改生产代码。
4. 基线 `runtime.redis_version` 含尾部 `\r`（`docker exec redis-cli` 输出带回车），为轻微数据格式瑕疵，不影响版本可读性与 AC。

## Result

PASS
