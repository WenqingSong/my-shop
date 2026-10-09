# Delivery Verification

## Milestone and Target

- Milestone：秒杀 V5 三级并发压测与正确性核对
- Delivery Target：`test/v5-three-tier-load-testing` HEAD `79d1b9b8c756e222cbfd6c2a4fa3900b7e01b09b`
- Cleaner Review Target：`3a78a53f1f9bfa6b0d14135a6f0bfdd45c4a1554`
- Target Match：YES（`79d1b9b` 相对 `3a78a53` 仅新增 review/owner/delivery 元数据提交，`run.sh`/Go 生产代码未变）

## Re-verification（本轮背景）

上一轮 `delivery.status=BLOCKED`：`serve` 启动 fail-fast 于 `qiniu.access_key 未配置`（ENVIRONMENT_GAP）。Owner 恢复 `.env` 七牛凭据后，本轮在隔离环境真实执行三级压测并给出正确性核对结论。

## Environment

- OS：Linux（容器内执行）
- Go：1.24.1
- MySQL：8.0.46（docker-compose，healthy，隔离卷本次新建）
- Redis：7.4.11（docker-compose，healthy，隔离卷本次新建）
- Docker：29.6.2 / Compose v5.3.1
- 交付对象：`go build ./...` 源码构建产物（本轮以 `/tmp/my-shop` 运行），无未提交文件/本机绝对路径依赖
- 配置来源：`.env`（数据库/Redis/七牛凭据，凭据不落库不打印）+ 环境变量容量保护开关（`FLASH_SALE_*`）
- 隔离与清理：全新 docker 卷、`redis FLUSHDB`、清理 `flash_sale_*`/`users`/`skus`/`products`/`categories` 等业务表后造数；未触碰共享/生产数据

## 容量保护启用配置（AC-001 阈值记录）

| 特性 | 开关 | 阈值 |
|---|---|---|
| 用户级限流 | 开启 | window=1s，max_requests=0（不限，每用户仅 1 请求） |
| 活动级限流 | 开启 | window=1s，max_requests=100 |
| 排队软上限 | 开启 | max_queued_per_sku=100 |
| Redis 闸门熔断 | 开启 | failure_threshold=5 / open_timeout=10s / half_open_max_probes=1（默认） |
| 指标 | 开启 | `GET /metrics` 暴露 `flashsale_*` |

测试活动：`activity_id=1`、`sku_id=1`、`total_stock=1000`、`flash_price=50`、时间窗 `NOW()-1h ~ NOW()+2h`。

## Verification

| Check | Result | Evidence |
|---|---|---|
| `delivery-start` Gate | PASS | exit 0 |
| `go build ./...` / `go vet ./...` | PASS | exit 0 |
| `bash -n scripts/flashsale-loadtest/*.sh` | PASS | 4 个脚本语法通过 |
| `bash scripts/flashsale-loadtest/test-run.sh` | PASS | 输出「test-run.sh 全部通过」 |
| 服务启动 + 健康检查 + 容量保护加载 | PASS | `/health` 200；`/metrics` 200；日志确认「all dependencies reachable」「schema 已就绪」 |
| 三级并发压测（low/medium/high） | PASS | 三档各 1000 有效请求，逐档产出报告与基线 JSON，可重复执行 |
| 正确性核对（压测后 MySQL 独立复核） | PASS | `sold=460 ≤ 1000`、订单数=460=sold、一人一单/幂等违例=0、`status=queued` 积压=0 |

### 三级压测结果

| 档位 | 并发 | 总请求 | 有效样本 | 成功受理 | HTTP 429（比例） | HTTP 503（比例） | p95 | p99 | queued 峰值 | sold（累计） |
|---|---|---|---|---|---|---|---|---|---|---|
| low | 20 | 1000 | 1000 | 139 | 861（86.10%） | 0（0%） | 0.073s | 0.316s | 113 | 139 |
| medium | 100 | 1000 | 1000 | 200 | 800（80.00%） | 0（0%） | 0.543s | 0.608s | 196 | 339 |
| high | 200 | 1000 | 1000 | 121 | 879（87.90%） | 0（0%） | 0.333s | 0.365s | 100 | 460 |

### 指标可对账（INV-021）

`GET /metrics` 秒杀指标累计：`order/queued=460`、`order/rate_limited=2194`、`order/queue_full=346`、`consume/success=460`，合计 3000 = 三档总请求；`queued=460` 与 MySQL `sold=460`、`orders=460` 对账一致。

## Acceptance Evidence

- AC-001（隔离环境 + 容量保护启用）：PASS——服务以四特性全开启动，`/health` 200，`/metrics` 200；阈值已记录；全新卷 + `FLUSHDB` + 业务表清理，未触碰共享/生产数据。
- AC-002（三级并发负载）：PASS——复用 `run.sh`，`low=20 / medium=100 / high=200` 三档明确区分，逐档独立产出结果与基线，可重复执行。
- AC-003（足够样本）：PASS——每档 1000 有效请求 ≥ 1000（`sample_insufficient=false`），p99 具备统计意义。
- AC-004（指标记录）：PASS——每档报告与基线 JSON 含 QPS、p50/p95/p99、429 比例、503 比例、queued 峰值/稳态，429/503 计数与逐请求 `http_code` 聚合对账一致。
- AC-005（正确性核对）：PASS——压测消费落单后 MySQL 独立复核 `sold=460 ≤ total_stock=1000`、`COUNT(flash_sale_orders)=460=sold`、一人一单/幂等违例均为 0、`queued` 稳态=0，报告判定 PASS。
- AC-006（无回归）：PASS——`bash -n` 通过、`test-run.sh` 全部通过、未改 Go 生产代码/migration/协议/错误码。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 熔断 Open→Half-Open 实际触发（503 非零） | Redis 全程健康，闸门无故障，熔断保持 Closed（符合预期，不人为制造故障） | 熔断降级路径未在本轮真实触发，其正确性由 `flash-sale-v5` 交付验证覆盖 |

## Remaining Risks

- QPS 口径为脚本「总请求 / 总耗时」（含用户注册阶段约 2 分钟），约 7.0 req/s；纯下单阶段吞吐远高于此。该口径为 `task.md` Assumption 明示，非本轮缺陷。
- 活动级限流阈值（100/s）在本地低延迟下对三档均为主要拒绝来源，故三档 429 比例接近（80%~88%）；并发差异主要体现在 p95/p99 与 queued 峰值上。
- 服务运行会在仓库根生成未跟踪的 `storage/`（轮播图占位图运行时产物），`.gitignore` 未覆盖；非本任务范围，不影响结论。

## Result

PASS
