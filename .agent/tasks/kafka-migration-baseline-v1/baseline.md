# Kafka 改造前基线（kafka-migration-baseline-v1）

本文件记录秒杀核心在 Kafka 改造前的可信代码基线：真实调用链、既有接口、数据表、错误码、请求状态、测试证据与回归验证入口。作为后续 Kafka 改造任务「既有秒杀能力未被破坏」的对比基准。

## 1. 基线结论

- Base Commit：`8fd62c06db41098ca85e5ee5bb81f6b4e87ba5b0`（`docs: 补充 GoLand 开发环境镜像说明`），分支 `chore/kafka-migration-baseline-v1`，与 `origin/develop` 同 SHA。
- 秒杀核心当前**无外部 MQ（无 Kafka）**：异步队列由 MySQL 出队表 `flash_sale_order_requests` 实现，消费/对账由 `goroutine+ticker` 后台扫描器驱动。
- 五个业务不变量（库存 ≥ 0、成功订单数 ≤ 初始库存、一人一单、失败不建单、重复请求不重复扣减）由 MySQL 条件扣减 + 唯一约束兜底，Redis Lua 闸门仅为加速准入，非事实来源。
- 本次基线建立过程不新增/修改任何生产代码、配置、数据模型、错误码或公开接口；相对 Base Commit 的变更仅限本任务目录内的文档与基线证据。

## 2. 秒杀核心真实调用链

事实来源：`internal/logic/flashsale/*` + `internal/worker/scanner.go` + `docs/design/flash-sale.md`，逐项核对一致。

1. **Redis Lua 抢购闸门**（`internal/logic/flashsale/redis.go`）：`flashSaleGateScript` 经 `runGate` 以 `EVAL` 原子完成空值标记检查 → 活动存在/启用/时间窗 → 售罄检查 → 幂等检查 → 一人一单检查 → 库存 `DECR` 预扣；`GATE_PASSED` 时原子写入一人一单/幂等标记。返回 `NOT_FOUND`/`NOT_IN_WINDOW`/`SOLD_OUT`/`ALREADY_PURCHASED`/`IDEMPOTENT_HIT`/`IDEMPOTENT_CONFLICT`/`SKU_NOT_BOUND`/`GATE_PASSED`。Redis 不可用/执行失败 → fail-closed（503 `1005`）。
2. **MySQL 出队表入队**（`internal/logic/flashsale/request.go` `enqueue`）：`GATE_PASSED` 后 `INSERT flash_sale_order_requests(status=queued)` 持久受理并快速返回 `status=queued`，不在同请求内同步落单；入队失败时权威值收敛 + 清除标记，不残留「已预扣但无 request」。
3. **异步消费落单**（`internal/logic/flashsale/consume.go` `ConsumeQueued`→`consumeOne`）：出队 `WHERE status=queued AND (next_attempt_at IS NULL OR next_attempt_at<=NOW()) ORDER BY id LIMIT N FOR UPDATE SKIP LOCKED`，单事务「SKU/商品校验 + 活动/时间窗校验 + 条件扣库存 + 建单 + `requests.status=success`」；业务失败落 `failed`，技术失败退避重试、超上限落 `dead`。后台驱动 `internal/worker/scanner.go` `StartFlashSaleConsumeScanner`（goroutine+ticker，默认 1s 周期）。
4. **库存对账收敛**（`internal/logic/flashsale/redis.go` `ReconcileCache`/`convergeStock`/`convergeEndedActivity`）：后台 `StartFlashSaleReconcileScanner`（默认 60s 周期）将 Redis `remaining` 幂等 `SET` 为权威值 `total_stock - sold - inflight_queued`（`inflight_queued` = 该活动×SKU 下 `status=queued` 请求数）；活动结束且在途归零后终态收敛 `remaining = total_stock - sold` 并失效缓存。

## 3. 既有接口

事实来源：`api/flashsale/v1/flash_sale.go`，与 `docs/design/flash-sale.md` §7 一致。

前台（`Auth` 分组，作用于 `Principal.UserID`）：
- `POST /flash-sales/:id/orders`：秒杀下单（body `{sku_id, idempotency_key}`，不提交价格/身份），V3 起返回排队受理结果（`status=queued`）。
- `GET /flash-sales/:id/orders/result?idempotency_key=K`：查询本人下单结果，归属隔离，不存在/非本人统一 404。

后台写（`AdminAuth` + `RequirePermission`）：
- `POST /admin/flash-sales`（`flash_sale:create`）
- `PUT /admin/flash-sales/:id`（`flash_sale:update`）
- `POST /admin/flash-sales/requests/:id/repair`（`flash_sale:repair`，仅 `dead→queued`）
- `GET /admin/flash-sales/requests/:id/audits`（`flash_sale:repair`，append-only）

## 4. 数据库表结构与请求状态

五张表（migrations `20261001000011` / `20261001000015` / `20261001000018`），与 `docs/design/flash-sale.md` §2 一致：

| 表 | 迁移 | 关键约束/索引 |
| --- | --- | --- |
| `flash_sale_activities` | 000011 | `idx_status`、`idx_time(start_time,end_time)` |
| `flash_sale_activity_skus` | 000011 | `uk_activity_sku(activity_id,sku_id)` UNIQUE、FK→activities `ON DELETE CASCADE` |
| `flash_sale_orders` | 000011 | `uk_flash_order_no`、`uk_flash_idempotency(user_id,idempotency_key)`、`uk_flash_one_per_user(activity_id,sku_id,user_id)` |
| `flash_sale_order_requests` | 000015 | `uk_request_idempotency(user_id,idempotency_key)`、`idx_dequeue(status,next_attempt_at,id)` |
| `flash_sale_request_audits` | 000018 | `idx_request_id`、`idx_operator`（append-only） |

异步请求状态（`flash_sale_order_requests.status`，DB TINYINT / API 字符串）：
`0=queued`、`1=success`、`2=failed`、`3=dead`。

## 5. 错误码域

事实来源：`internal/codes/codes.go`，秒杀域 12000-12999，已用 12001~12010：

| code | 语义 | HTTP |
| --- | --- | --- |
| 12001 | 活动不存在/已下架/未预热 | 404 |
| 12002 | 不在时间窗 | 409 |
| 12003 | 库存不足/售罄 | 409 |
| 12004 | 一人一单已购 | 409 |
| 12005 | 幂等键冲突 | 409 |
| 12006 | SKU 禁用/商品下架 | 409 |
| 12007 | 活动参数非法 | 400 |
| 12008 | 请求不可修复（仅 dead 可修复） | 409 |
| 12009 | 限流拒绝（无副作用） | 429 |
| 12010 | 排队软上限拒绝（无副作用） | 429 |

## 6. 测试证据

环境：Go 1.24.1、Docker 29.6.2；MySQL/Redis 经 `make deps-start`（`MYSQL_ROOT_PASSWORD=root` + 占位 `AUTH_JWT_SECRET`）就绪为 `my-shop-mysql` / `my-shop-redis` 容器。

| 检查 | 命令 | 结果 | 说明 |
| --- | --- | --- | --- |
| 纯内存单元测试 | `make test-unit` | PASS | 含 flashsale `TestCircuitBreakerStateMachineTransitions`/`TestCircuitBreakerStateMachineRace`/`TestOrderOutcomeMapping` |
| race 门禁 | `make test-race` | PASS | `TestCircuitBreakerStateMachine*`（熔断器状态机并发） |
| 集成测试 | `make test-integration`（`go test -p 1 ./...`） | PASS | 含 `internal/logic/flashsale/*` 全量集成测试与 e2e 秒杀核心（见下） |
| 静态检查 | `go vet ./...` | PASS | — |
| 格式检查 | `gofmt -l .` | PASS | 无输出（无格式问题） |
| 构建 | `go build ./...` | PASS | — |
| Registry 一致性 | `scripts/check-registry.sh` | PASS | 无 Reservation 重复或 Registry↔实现漂移 |
| Prometheus 静态断言 | `scripts/test-prometheus.sh` | PASS（部分 NOT_VERIFIED） | 无 promtool/prometheus 镜像，recording rules 语法校验跳过 |
| 容器化静态断言 | `scripts/test-container.sh` | PASS | — |
| Nginx 静态断言 | `scripts/test-nginx.sh` | PASS（部分 NOT_VERIFIED） | 无已构建 `my-shop-nginx:local` 镜像，`nginx -t` 跳过 |
| lib.sh 语义 | `scripts/test-lib.sh` | PASS | — |

秒杀核心 e2e（`test/e2e/flashsale_test.go` 等，`make test-integration` 内全部通过）：`TestFlashSaleConcurrentNoOversell`、`TestFlashSaleOnePerUser`、`TestFlashSaleIdempotency`、`TestFlashSaleOrderTimeWindow`、`TestFlashSaleOrderPricingSnapshot`、`TestFlashSaleOrderFailures`、`TestFlashSaleRepairAndAudit`、`TestFlashSaleV2RedisPreDeductNoOversell`/`Reconcile`/`SoldOutFastFail`、`TestFlashSaleV3EnqueueAndResultQuery`/`ConsumeIdempotent`/`Compensation`/`FailClosed`、`TestFlashSaleV5RateLimitHTTP`。

`internal/logic/flashsale/*` 集成测试（`make test-integration` 内全部通过）：库存收敛幂等、在途计数、结束收敛、修复审计、孤儿标记自愈、消费重试死信、崩溃重投幂等、幂等冲突补偿一次、限流/排队软上限/熔断恢复。

### 关键测试入口

1. `TestFlashSaleConcurrentNoOversell`（`test/e2e/flashsale_test.go`）：并发下单后断言 `sold ≤ total_stock` 且成功订单数 = `sold`，直接证明库存 ≥ 0、成功订单数 ≤ 初始库存两个不变量在并发下成立。
2. `TestFlashSaleIdempotency`（`test/e2e/flashsale_test.go`）：同幂等键重复请求不重复扣减库存，证明「重复请求不重复扣减」不变量。

## 7. 回归验证入口（后续 Kafka 改造必须保留）

固定 Base Commit `8fd62c06db41098ca85e5ee5bb81f6b4e87ba5b0`，可重复执行：

```text
make test-unit          # 纯内存单元测试（无 MySQL/Redis）
make test-race          # 熔断器状态机 race 门禁（无 MySQL/Redis）
make test-integration   # 依赖 MySQL/Redis 的集成测试 + e2e（需先 make deps-start）
make test               # 完整门禁：vet + lib + unit + integration + registry + 静态断言
go vet ./...
go build ./...
```

改造后按上述命令对比，断言秒杀核心 e2e 与 `internal/logic/flashsale/*` 集成测试保持通过，并核对 `sold ≤ total_stock`、成功订单数 = `sold`。

## 8. 已知问题与 NOT_VERIFIED

- **`make test-unit` 中 `TestScannersStopOnContextCancel` 漏执行**：`scripts/test-unit.sh` 用 `./internal/cmd/...` 匹配该测试，但测试实际位于 `internal/worker/scanner_test.go`（package `worker`），故该命令输出 `[no tests to run]`。该测试在 `make test-integration`（`go test -p 1 ./...`）中经 `internal/worker` 包实际执行并通过。属既有事实，非本次引入；不修改测试脚本（Out of Scope）。
- **Prometheus recording rules 语法校验（promtool）NOT_VERIFIED**：环境无 `promtool`/`prometheus` 镜像，`test-prometheus.sh` 跳过语法校验，仅执行静态断言。影响：recording rules 表达式未经 `promtool check rules` 验证，但不影响秒杀核心行为。
- **Nginx `nginx -t` NOT_VERIFIED**：环境无已构建 `my-shop-nginx:local` 镜像，`test-nginx.sh` 跳过 `nginx -t`，仅执行静态断言。影响：Nginx 配置未做运行时语法校验，但不影响秒杀核心行为。

以上 NOT_VERIFIED 项均与环境不具备相关，不影响 AC-001~AC-004 的结论；后续在具备完整镜像/工具的环境可补齐。
