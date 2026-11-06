# D3 测试基线执行记录

> Review Baseline：`95c13910c7226b4e32487ed046702caf75d74fc6`（源码等价于 `473d6f5253e3abcb98ece9d79a5a8059563a3e8d`）
> 执行环境：`go1.24.1 linux/amd64`；Docker `29.9.2`；Docker Compose `v5.6.1`；
> MySQL 容器 `8.0.46`（`make deps-start` 本机新建，`127.0.0.1:3306`）；
> Redis 容器 `redis:7-alpine`（`127.0.0.1:6379`）；
> `go.mod` 声明 `go 1.23.0`，实际工具链 1.24.1。

## 1. 执行结果总表

| # | 执行目录 | 完整命令 | 退出码 | 状态 | 关键输出 |
| --- | --- | --- | --- | --- | --- |
| 1 | `/root/projects/my-shop` | `go build ./...` | 0 | `PASS` | 无输出（仅首次依赖下载） |
| 2 | `/root/projects/my-shop` | `go vet ./...` | 0 | `PASS` | 无输出 |
| 3 | `/root/projects/my-shop` | `gofmt -l .` | 0 | `PASS` | 输出 0 行（无未格式化文件） |
| 4 | `/root/projects/my-shop` | `bash scripts/test-unit.sh` | 0 | `PASS` | 11 个包 ok（见 §2） |
| 5 | `/root/projects/my-shop` | `bash scripts/test-race.sh` | 0 | `PASS` | `ok internal/logic/flashsale 1.039s` |
| 6 | `/root/projects/my-shop` | `bash scripts/test-integration.sh`（内部 `go test -p 1 ./...`，`DATABASE_DEFAULT_PASS=root`） | 0 | `PASS` | 全部包 ok；`test/e2e 91.922s`；`internal/migrations 28.978s` |
| 7 | `/root/projects/my-shop` | `bash scripts/test-container.sh` | 0 | `PASS` | 容器化静态断言通过 |
| 8 | `/root/projects/my-shop` | `bash scripts/test-prometheus.sh` | 0 | `PASS`（含 WARN） | 静态校验通过；`未找到 promtool 或 prometheus 镜像，跳过 recording rules 语法校验` |
| 9 | `/root/projects/my-shop` | `bash scripts/test-lib.sh` | 0 | `PASS` | `test-lib.sh 全部通过` |
| 10 | `/root/projects/my-shop` | `bash scripts/check-registry.sh` | 0 | `PASS` | 未发现 Migration 版本重复或错误码域漂移 |
| 11 | `/root/projects/my-shop` | `bash scripts/test-nginx.sh` | 0 | `PASS`（含 WARN） | 静态校验通过；未构建镜像，跳过 `nginx -t` |
| 12 | `/root/projects/my-shop` | `DATABASE_DEFAULT_PASS=root go test -count=1 -v -p 1 -run 'TestFlashSale|TestConcurrent' ./test/e2e/` | 0 | `PASS` | 20 个用例全部 PASS（见 §3） |
| 13 | `/root/projects/my-shop` | `DATABASE_DEFAULT_PASS=root go test -count=1 -v -p 1 ./internal/logic/flashsale/` | 0 | `PASS` | 22 个顶层用例全部 PASS（见 §3） |
| 14 | `/root/projects/my-shop` | `DATABASE_DEFAULT_PASS=root DATABASE_DEFAULT_DEBUG=true go test -count=1 -run 'TestConsumeRetryThenDeadLetter' ./internal/logic/flashsale/ -v` | 0 | `PASS` | 用于抓取出队 SQL 与事务序列（§6） |
| 15 | `/root/projects/my-shop` | `git diff --check` | 0 | `PASS` | 无空白错误 |
| 16 | `/root/projects/my-shop` | `docker build -t my-shop-app:audit .` | — | `NOT_EXECUTED` | 未执行：本任务不要求构建镜像，且静态断言（#7）已覆盖 Dockerfile 结构；不冒充运行验证 |
| 17 | — | `docker compose up -d`（完整栈 app/nginx/prometheus） | — | `NOT_EXECUTED` | 未执行：只启动了 `mysql`、`redis`（`make deps-start`），app/nginx/prometheus 未构建未启动 |
| 18 | — | CNB 流水线 `.cnb.yml` 实跑 | — | `NOT_EXECUTED` | 未执行：本地无 CNB runner；仅做配置静态核对（D4 §5） |
| 19 | — | Prometheus 实际抓取 `/metrics` | — | `NOT_EXECUTED` | 未执行完整栈；`/metrics` 的 HTTP 行为由 `TestFlashSaleV5MetricsEndpoint` 在测试进程内覆盖（PASS） |

状态取值仅使用 `PASS` / `FAIL` / `NOT_EXECUTED` / `BLOCKED`。本任务无 `FAIL`、无 `BLOCKED` 测试项。

## 2. 单元测试明细（`scripts/test-unit.sh`，退出码 0）

```text
ok  internal/auth            0.030s
ok  internal/metrics         0.093s
ok  internal/logic/health    1.163s
ok  internal/logic/dashboard 0.026s
ok  internal/controller/health 0.216s
ok  internal/logic/iam       0.058s
ok  internal/logic/flashsale 0.010s        # -run TestCircuitBreakerStateMachine|TestOrderOutcomeMapping
ok  internal/logic/legacymigrate 0.008s
ok  internal/migrations      0.007s
ok  internal/middleware      0.211s
ok  internal/cmd             0.004s [no tests to run]   # -run 匹配到 0 个纯内存用例
```

注：`internal/cmd` 该行显示 `[no tests to run]`，即该包在单元门禁中**实际未执行任何用例**，不应计入覆盖。

## 3. 秒杀相关测试逐用例结果（真实执行）

### 3.1 `internal/logic/flashsale`（22 个顶层用例，全部 PASS，36 个 RUN 含子测试）

```text
--- PASS: TestRateLimitUserRejects (0.17s)
--- PASS: TestRateLimitActivityRejects (0.15s)
--- PASS: TestQueueCapacityRejects (0.18s)
--- PASS: TestCircuitBreakerOpensAndRecovers (1.26s)
--- PASS: TestMetricsReconcile (0.13s)
--- PASS: TestCircuitBreakerStateMachineTransitions (0.00s)
--- PASS: TestCircuitBreakerStateMachineRace (0.00s)
--- PASS: TestConsumeMetricsCounting (0.16s)
--- PASS: TestConsumeRetryThenDeadLetter (0.18s)
--- PASS: TestCreateOrderIdempotencyConflictCompensatesOnce (0.15s)
--- PASS: TestConsumeInterruptedByCtxCancel (0.12s)
--- PASS: TestConsumeCrashBeforeCommitRedeliversOnce (0.14s)
--- PASS: TestOrderOutcomeMapping (0.00s)
--- PASS: TestConvergeEndedActivity (0.16s)
--- PASS: TestConvergeEndedActivityKeepsInflight (0.13s)
--- PASS: TestRepairRequestDeadToQueuedAndAudit (0.15s)
--- PASS: TestIdempotentOrphanMarkerSelfHeal (0.14s)
--- PASS: TestAlreadyPurchasedOrphanMarkerSelfHeal (0.14s)
--- PASS: TestReconcileCacheConvergesEndedActivity (0.15s)
--- PASS: TestRepairRequestRejectsOverlongReason (0.10s)
--- PASS: TestConvergeStockIdempotentAndClearsSoldout (0.14s)
--- PASS: TestConvergeStockCountsInflightQueued (0.13s)
ok  internal/logic/flashsale 3.898s
```

### 3.2 `test/e2e`（秒杀相关 20 个用例，全部 PASS）

```text
--- PASS: TestFlashSaleCreateActivityAndPermission (0.71s)
--- PASS: TestFlashSaleOrderTimeWindow (0.54s)
--- PASS: TestFlashSaleOrderPricingSnapshot (0.47s)
--- PASS: TestFlashSaleOnePerUser (0.65s)
--- PASS: TestFlashSaleIdempotency (0.53s)
--- PASS: TestFlashSaleConcurrentNoOversell (0.70s)
--- PASS: TestFlashSaleOrderFailures (0.53s)
--- PASS: TestFlashSaleUpdateActivity (0.52s)
--- PASS: TestFlashSaleRepairAndAudit (0.66s)
--- PASS: TestFlashSaleUpdateCannotAddOrRemoveBindings (0.64s)
--- PASS: TestFlashSaleV2PreheatAndInvalidate (0.55s)
--- PASS: TestFlashSaleV2SoldOutFastFail (0.48s)
--- PASS: TestFlashSaleV2RedisPreDeductNoOversell (0.65s)
--- PASS: TestFlashSaleV2Reconcile (0.50s)
--- PASS: TestFlashSaleV3FailClosed (0.48s)
--- PASS: TestFlashSaleV3EnqueueAndResultQuery (0.53s)
--- PASS: TestFlashSaleV3ConsumeIdempotent (0.48s)
--- PASS: TestFlashSaleV3Compensation (0.46s)
--- PASS: TestFlashSaleV5RateLimitHTTP (0.48s)
--- PASS: TestFlashSaleV5MetricsEndpoint (0.45s)
ok  test/e2e 11.110s
```

### 3.3 测试方式说明（决定其证明力）

- E2E 不是在 mock 上跑：`setupIsolationServer` 会 `migrations.Up` + `boot.Bootstrap`，启动真实 `ghttp` 服务（随机端口），挂载**生产同款** `RegisterFrontendRoutes` / `RegisterAdminRoutes`，连接真实 MySQL/Redis（`test/e2e/identity_isolation_test.go:93-133`）。
- 每个 E2E 用例会 `DELETE` 秒杀/商品相关业务表并 `FlushDB` Redis（`flashsale_test.go:41-51`、`identity_isolation_test.go:106-116`）。本次执行使用 `make deps-start` 新建的本地容器库，无既有数据被破坏。
- 故障注入方式（重要限制）：
  - Redis 故障：把 activity key 改成 string 触发 `WRONGTYPE`（`flashsale_v2_test.go:253-282`），**不是停掉 Redis 或网络分区**。
  - 消费崩溃：`consumeBeforeCommitHook` 进程内注入（`consume.go:29-32`、`:120-126`），**不是 kill 进程**。
  - 库存/时间窗异常：直接改 MySQL 行（`flashsale_v3_test.go:139-175`）。
- 依赖缺失时测试**不会 Skip**，而是连接失败即 FAIL（全仓库仅 7 处 `t.Skip`，均为时区数据缺失，与秒杀无关）。

## 4. 测试文件分布（`find . -name '*_test.go'`，共 61 个）

| 区域 | 数量 | 性质 |
| --- | --- | --- |
| `internal/**`（auth、boot、cmd、controller、logic、middleware、migrations、metrics、worker 等） | 41 | 混合：纯内存单元 + 依赖 MySQL/Redis 的白盒集成 |
| `test/e2e` | 20 | 全量 HTTP E2E（真实路由 + 真实依赖），含 4 个秒杀专项文件（v1/v2/v3/v5） |

秒杀专项测试文件：

- `internal/logic/flashsale/{capacity,circuit_breaker,consume,consume_metrics,order_outcome,recovery,redis}_test.go`
- `test/e2e/{flashsale,flashsale_v2,flashsale_v3,flashsale_v5}_test.go`

## 5. 已覆盖的业务边界

| 边界 | 覆盖证据（用例） |
| --- | --- |
| 并发下单不超卖（20 并发 / 5 库存） | `TestFlashSaleConcurrentNoOversell`（断言成功入队=5、`sold=5`、订单数=5） |
| 一人一单 | `TestFlashSaleOnePerUser` |
| 幂等（同键同内容 / 异内容） | `TestFlashSaleIdempotency` |
| 时间窗（MySQL `NOW()`） | `TestFlashSaleOrderTimeWindow`、`TestFlashSaleV3Compensation` |
| 价格快照 | `TestFlashSaleOrderPricingSnapshot` |
| 失败不建单（库存不足/参数/不存在/未绑定） | `TestFlashSaleOrderFailures` |
| 活动更新不增删绑定、库存不低于已售 | `TestFlashSaleUpdateCannotAddOrRemoveBindings`、`TestFlashSaleUpdateActivity` |
| Redis 预扣 + 售罄快速失败 + 对账 | `TestFlashSaleV2PreheatAndInvalidate`、`TestFlashSaleV2SoldOutFastFail`、`TestFlashSaleV2RedisPreDeductNoOversell`、`TestFlashSaleV2Reconcile` |
| Redis 故障 fail-closed（503/1005，无写入） | `TestFlashSaleV3FailClosed` |
| 入队 + 结果查询 + 消费幂等 + 失败补偿 | `TestFlashSaleV3EnqueueAndResultQuery`、`TestFlashSaleV3ConsumeIdempotent`、`TestFlashSaleV3Compensation` |
| 技术失败重试→死信→人工修复+审计 | `TestConsumeRetryThenDeadLetter`、`TestRepairRequestDeadToQueuedAndAudit` |
| 崩溃重投只产生一次业务效果 | `TestConsumeCrashBeforeCommitRedeliversOnce` |
| 孤儿标记自愈（idem / bought） | `TestIdempotentOrphanMarkerSelfHeal`、`TestAlreadyPurchasedOrphanMarkerSelfHeal` |
| 活动结束收敛 / inflight 保护 | `TestConvergeEndedActivity`、`TestConvergeEndedActivityKeepsInflight`、`TestReconcileCacheConvergesEndedActivity` |
| 限流 / 排队上限 / 熔断 / 指标 | `TestRateLimitUserRejects`、`TestQueueCapacityRejects`、`TestCircuitBreaker*`、`TestMetricsReconcile`、`TestFlashSaleV5RateLimitHTTP`、`TestFlashSaleV5MetricsEndpoint` |

## 6. 实测 SQL 与事务序列（故障注入下的真实日志）

命令见 §1 #14。截取（`DATABASE_DEFAULT_DEBUG` 输出）：

```sql
-- 出队（每个消费事务开头）
SELECT `id`,`user_id`,`activity_id`,`sku_id`,`idempotency_key`,`request_hash`,`status`,`retry_count`,
       `next_attempt_at`,`last_error_code`,`flash_order_id`
FROM `flash_sale_order_requests`
WHERE (`status`=0) AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
ORDER BY `id` LIMIT 1 FOR UPDATE SKIP LOCKED

-- 事务 1：订单号撞号 → 技术失败 → retry_count=1（同一 txid:1）
BEGIN
INSERT INTO `flash_sale_orders`(...) VALUES('FS-FIXED',...)      -- 失败（uk_flash_order_no）
UPDATE `flash_sale_order_requests` SET `retry_count`=1,`next_attempt_at`=DATE_ADD(NOW(), INTERVAL 5 SECOND),`last_error_code`=1000 WHERE `id`=87
COMMIT

-- 事务 2：重试超限 → dead（同一 txid:2）
BEGIN
INSERT INTO `flash_sale_orders`(...) VALUES('FS-FIXED',...)      -- 失败
UPDATE `flash_sale_order_requests` SET `status`=3,`retry_count`=4,`last_error_code`=1000 WHERE `id`=87
COMMIT

-- 事务 3：成功落单（同一 txid:3）
BEGIN
INSERT INTO `flash_sale_orders`(...) VALUES('FS1793...',...)     -- rows:1
UPDATE `flash_sale_activity_skus` SET `sold`=sold + 1 ... WHERE (`id`=68) AND (sold < total_stock)   -- rows:1
UPDATE `flash_sale_order_requests` SET `status`=1,`flash_order_id`=75 ... WHERE `id`=87              -- rows:1
COMMIT
```

该日志直接证明：**订单插入、库存条件扣减、请求状态更新处于同一事务**，`retry_count`/`next_attempt_at`/`dead` 迁移也在同一事务内。

## 7. 数据库运行期只读核对

```bash
$ mysql -h127.0.0.1 -P3306 -uroot -proot my_shop -e "SELECT version, dirty FROM schema_migrations; SHOW TABLES LIKE 'flash_sale%';"
version        dirty
20261001000023 0
flash_sale_activities
flash_sale_activity_skus
flash_sale_order_requests
flash_sale_orders
flash_sale_request_audits
```

说明：这是**测试执行留下的测试库**（E2E 已执行 `migrations.Up`），不是生产库；本任务未执行任何 Migration 命令、未修改 Schema。

## 8. 尚未覆盖的关键场景（未验证，不是缺陷结论）

| 场景 | 现状 | 需要什么才能验证 |
| --- | --- | --- |
| 多实例/多 goroutine 同时领取同一行（真实 SKIP LOCKED 竞争） | 无测试：所有消费测试均单 goroutine 驱动 `ConsumeQueued` | 两个独立进程（或两个连接池）并发消费同一活动，统计订单/`sold` 唯一性 |
| Redis 真实宕机、MySQL 宕机、网络分区 | 仅 `WRONGTYPE` 与进程内 hook 模拟 | 停容器 / iptables / 主从切换等真实故障注入 |
| 进程被 SIGKILL（非事务回滚 hook） | 无 | `kill -9` 消费者进程后重启，核对请求重投一次效果 |
| 锁等待超时 / 死锁重试 | 无 | 高并发下压测并观察 `innodb_lock_wait_timeout`、`1213/1205` 错误 |
| 活动数 > 100 时的对账覆盖 | 无 | 造 >100 个活跃活动，观察对账是否轮转到后续活动 |
| 请求表长期增长后的出队计划 | 无 | 造百万级 queued/dead 行后 `EXPLAIN` 与压测 |
| Docker 镜像构建、完整 compose 栈、Nginx、Prometheus 实际抓取 | 仅静态校验 | `docker compose up -d` 后访问 `/metrics` 与 Grafana/PromQL |
| CNB 流水线 | 仅静态阅读 `.cnb.yml` | 在 CNB 上触发 PR/push 流水线 |
| 生产容量（QPS/p95/p99） | 未测 | 压测任务（`scripts/flashsale-loadtest/` 存在，但本任务未执行，见 D4 §5） |
