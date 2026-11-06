# D4 已验证事实、推断、未知事项与潜在风险

> Review Baseline：`95c13910c7226b4e32487ed046702caf75d74fc6`（源码等价于 `473d6f5253e3abcb98ece9d79a5a8059563a3e8d`）
> 分区规则：A 只能由源码 / SQL / 测试 / 真实命令输出直接证明；B 必须写清推断依据与缺口；C 必须写清缺什么证据；D 只登记，不修复。

## A. 已验证事实

### A1 仓库与代码基线

- 仓库 `/root/projects/my-shop`，远端 `origin` = `https://cnb.cool/framework-station/my-shop`。
- 审计时分支 `task/surgecart-baseline-audit`，HEAD `95c13910c7226b4e32487ed046702caf75d74fc6`，`git status --short` 为空。
- 审计开始时（HEAD=`95c1391`）`git diff --stat 473d6f5..HEAD` 只有 6 个任务记录文件（+266 行），业务源码零差异；交付后该区间新增 6 个报告文件，业务目录仍零差异。
- 证据：`git rev-parse HEAD`、`git status --short`、`git diff --stat 473d6f5..HEAD`（命令与输出见 `delivery.md` §3.1）。

### A2 秒杀 HTTP 入口与身份校验

- 下单入口：`POST /flash-sales/:id/orders`（`internal/cmd/routes_frontend.go:106`），注册在 `user` 组，前置 `middleware.Auth`（`:74`）。
- 结果查询：`GET /flash-sales/:id/orders/result`（`:108`）。
- `middleware.Auth` 校验 JWT 签名 → Redis 会话（`auth.ValidateSession`，`internal/middleware/auth.go:36`）→ `users.status` 与 `auth_epoch`（`:49-56`），任一失败 fail-closed 401。
- Handler 只从 `Principal` 取 `userID`（`internal/controller/flashsale/flashsale.go:23`、`:82-88`）。
- 后台入口：`POST/PUT /admin/flash-sales*` 与修复/审计接口，均绑定 `RequirePermission("flash_sale:create|update|repair")`（`internal/cmd/routes_admin.go:147-151`）。

### A3 同步链路顺序

`CreateOrder`（`internal/logic/flashsale/flashsale.go:266-342`）实际顺序：

1. `sku_id`/`idempotency_key` 非空校验（`:273-279`）
2. 用户级限流（`:285`）→ 活动级限流（`:288`）→ 排队软上限（`:291`）
3. 熔断器 `allow`（`:296`）
4. Redis Lua 闸门 `runGate`（`:304`，`EVAL` 在 `redis.go:207`）
5. 按闸门结果分流（`:316-335`）
6. `enqueue` 写 MySQL 请求（`:341`）

即：**Redis 预扣先于 MySQL 请求落库**，两步不在同一事务。

### A4 Lua 脚本内容与返回码

- 脚本位于 `internal/logic/flashsale/redis.go:65-141`，键顺序 `null, activity, stock, bought, idem, soldout`（`:207-215`）。
- 顺序：空值标记 → 活动存在/启用 → 时间窗（`now` 由 Go 传入）→ 售罄标记 → 幂等 → 一人一单 → 剩余检查 → `DECR` 预扣 → `SET bought` + `SET idem` → `GATE_PASSED`（`:73-140`）。
- `DECR` 后为负会 `INCR` 回补并置售罄（`:127-132`）。
- TTL = `finish - now + grace`，`grace=60`（`:37`、`:136-139`）。

### A5 请求落库（入队）

- `enqueue`（`internal/logic/flashsale/request.go:61-92`）只有一条 `INSERT flash_sale_order_requests`，**无显式事务**。
- 撞 `uk_request_idempotency` 时先 `convergeStockAndMarkers`（`:72`）再按 `user_id+key` 回读（`:73-76`、`:96-113`），同 hash 返回既有状态、异 hash 返回 12005。
- 成功后 `incrQueueCount`（`:80`，best-effort）。

### A6 消费者启动与调度

- 启动点：`serve` → `worker.StartFlashSaleConsumeScanner`（`internal/cmd/cmd.go:139`），`ctx` 为 SIGINT/SIGTERM 派生（`:130-131`），进程退出前 `wg.Wait()`（`:163`）。
- 扫描器：`goroutine + ticker`，周期 `flash_sale.consume_scan_interval` 默认 1s（`internal/worker/scanner.go:54-58`），每 tick 调 `ConsumeQueued(ctx, 100)`（`:69`）。
- `ConsumeQueued`（`internal/logic/flashsale/consume.go:36-52`）串行循环 `consumeOne`，最多 `limit` 条；任一 `consumeOne` 返回 error 即结束本轮并返回错误（`:41-46`）。

### A7 领取 SQL（实测）

`consumeOne` 在事务内执行（`consume.go:76-87`），生成的 SQL（`DATABASE_DEFAULT_DEBUG=true` 实测）：

```sql
SELECT `id`,... FROM `flash_sale_order_requests`
WHERE (`status`=0) AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
ORDER BY `id` LIMIT 1 FOR UPDATE SKIP LOCKED
```

### A8 落单事务（实测同一 `txid`）

```sql
BEGIN
INSERT INTO `flash_sale_orders`(...) VALUES(...)
UPDATE `flash_sale_activity_skus` SET `sold`=sold + 1 ... WHERE (`id`=?) AND (sold < total_stock)
UPDATE `flash_sale_order_requests` SET `status`=1,`flash_order_id`=? ... WHERE `id`=?
COMMIT
```

因此「订单创建 + 秒杀库存扣减 + 请求状态 success」在同一 MySQL 事务内（`insertOrderInTx` `flashsale.go:463-542` + `successRequestInTx` `consume.go:172-180`）。

### A9 失败分类与重试/死信

- 业务失败 → `status=2`（`failRequestInTx` `consume.go:183-191`），分类见 `classifyConsumeError` `:226-254`。
- 技术失败 → `retry_count+1`、`next_attempt_at=NOW()+5/10/20s`（`retryOrDeadRequestInTx` `:195-222`、`consumeBackoffSeconds` `:257-266`）。
- `retry_count>3`（第 4 次技术失败）→ `status=3`（dead）（`:196-209`）。
- 终态失败在事务提交后收敛 Redis + 清标记（`consume.go:145-148`、`redis.go:368-371`）。
- 实测日志显示 `retry_count=1 → 4` 与 `status=3` 均在同一事务内提交。

### A10 数据表与 Migration

- 5 张表与文件：`20261001000011_flash_sale.up.sql`（activities / activity_skus / orders）、`20261001000015_flash_sale_order_requests.up.sql`（requests）、`20261001000018_flash_sale_request_audits.up.sql`（audits）。
- 唯一键：`uk_activity_sku`、`uk_flash_order_no`、`uk_flash_idempotency`、`uk_flash_one_per_user`、`uk_request_idempotency`。
- 出队索引：`idx_dequeue(status,next_attempt_at,id)`。
- 运行期只读核对：`schema_migrations` 版本 `20261001000023`、`dirty=0`，5 张表存在（MySQL 8.0.46 测试库）。

### A11 对账任务的实际职责

- `ReconcileCache`（`redis.go:382-427`）扫描两类活动：
  1. `status=1 AND end_time > NOW()`（`:390-397`）→ `syncActivityCache`（重算 `remaining=total-sold-inflight`、`HSET` 活动、`SET` 库存与排队计数、清空值/售罄标记）；
  2. `status=1 AND end_time <= NOW() AND end_time > NOW()-60s`（`:409-418`）→ `convergeEndedActivity`（`inflight=0` 时失效活动/售罄/排队 key 并置空值标记）。
- 两类扫描均 `Order("id").Limit(limit)`，`limit` 由扫描器固定为 100（`scanner.go:45`、`:96`）。
- 对账代码只读写 `flash_sale_activities` / `flash_sale_activity_skus`（读）与 Redis（写），**不写 `flash_sale_order_requests` / `flash_sale_orders`**。

### A12 人工修复与审计

- `RepairRequest`（`repair.go:41-112`）：仅 `target_status=queued`、仅 `status=dead` 可修；事务内条件 `UPDATE ... WHERE status=3` + `RowsAffected` 判定（`:79-94`），随后 `INSERT flash_sale_request_audits`（`:95-105`）。
- 无 UPDATE/DELETE 审计的接口；`ListRequestAudits` 只读（`:115-139`）。

### A13 结果查询

- `GetOrderResult`（`request.go:117-150`）按 `user_id + activity_id + idempotency_key` 查询（`:188-200`），非本人/不存在统一 404（`:127`）。
- `status=success` 时按 `flash_order_id` 加载订单并校验归属（`:131-142`）；`failed/dead` 返回 `last_error_code`（`:144-148`）。

### A14 秒杀与普通库存/订单隔离

- `internal/logic/flashsale/` 生产代码中不存在对 `inventories`、`inventory_logs`、`orders`、`order_items` 的引用（`grep` 无结果）。
- 秒杀库存只存在 `flash_sale_activity_skus.total_stock/sold`；秒杀订单只存在 `flash_sale_orders`。
- 与设计文档 INV-009「配额需运营侧从普通库存划拨」一致，且无机械校验。

### A15 测试与质量门禁（实际执行）

- `go build ./...`、`go vet ./...`、`gofmt -l .`（0 行）、`scripts/test-unit.sh`、`scripts/test-race.sh`、`scripts/test-integration.sh`、`scripts/test-container.sh`、`scripts/test-prometheus.sh`、`scripts/test-lib.sh`、`scripts/check-registry.sh`、`scripts/test-nginx.sh` 全部退出码 0。
- 秒杀相关：`internal/logic/flashsale` 22 个顶层用例、`test/e2e` 20 个秒杀用例全部 PASS（D3 §3）。
- 明细与限制见 D3。

### A16 指标实现

- 仅两个指标：`flashsale_requests_total`（CounterVec）、`flashsale_request_duration_seconds`（HistogramVec），标签均为 `interface`、`result`（`internal/metrics/metrics.go:43-56`）。
- 注册在 `init()`（`:58-60`）；开关 `flash_sale.metrics.enabled`（`GetEffective`，`:63-69`）；`/metrics` 仅在开启时挂载（`internal/cmd/cmd.go:180-187`，直接 `BindHandler`，绕过统一 JSON 包装）。
- 全仓库无其他 Prometheus 自定义 collector（`grep -rn "NewCounter\|NewGauge\|NewHistogram\|MustRegister" internal` 仅命中 `internal/metrics/metrics.go`）。

### A17 容量保护默认值

- `flash_sale.rate_limit.enabled=false`（`manifest/config/config.yaml:105`）、`queue_capacity.enabled=false`（`:120`）、`circuit_breaker.enabled=false`（`:130`）、`metrics.enabled=false`（`:141`）。
- 上述开关均通过 `GetEffective` 读取（支持环境变量覆盖）：`ratelimit.go:42-48`、`queue_capacity.go:27-33`、`circuit_breaker.go:40-46`、`metrics.go:63-69`。

### A18 扫描周期不支持环境变量覆盖

- `internal/worker/scanner.go:22`、`:55`、`:82` 使用 `g.Cfg().MustGet`。
- GoFrame v2.10.3 实现：`MustGet` → `Get`（`gcfg.go:216-226`），`Get` 只读配置适配器（`gcfg.go:96-112`），**不查环境变量**；只有 `GetEffective` 才以「命令行 > 环境变量 > 配置文件」优先级读取（`gcfg.go:172-213`）。
- 因此 `config.yaml:94` 注释宣称的 `FLASH_SALE_RECONCILE_SCAN_INTERVAL` / `FLASH_SALE_CONSUME_SCAN_INTERVAL` 对这两个键**不生效**（除非通过配置文件本身修改）。
- 对照：`flash_sale.metrics.enabled` 用 `GetEffective`，`FLASH_SALE_METRICS_ENABLED` 实测生效（`TestFlashSaleV5MetricsEndpoint` 通过 `t.Setenv` 开启并成功取到 `/metrics`）。

### A19 请求表无生命周期管理

- 全仓库对 `flash_sale_order_requests` 的引用共 18 处（生产代码），无 `DELETE`/归档/分区/过期清理（`grep -rn ... | grep -iE "delete|archive|truncate"` 为空）。
- Migration 中该表无 TTL 字段、无分区定义。

### A20 Docker / Compose / CNB / Prometheus 资产

- `Dockerfile`：多阶段（`golang:1.23-alpine` builder → `alpine:3.20`），`CGO_ENABLED=0`、`-trimpath -ldflags="-s -w"`，非 root `USER 10001:10001`，`EXPOSE 8000`，`ENTRYPOINT ["/app/docker-entrypoint.sh"]`；Dockerfile 内**无 HEALTHCHECK**。
- `scripts/docker-entrypoint.sh`：先 `/app/my-shop migrate up`，再 `exec /app/my-shop serve`。
- `docker-compose.yml`：5 服务（mysql/redis/app/nginx/prometheus）；mysql/redis 仅回环绑定；app 不映射宿主端口；nginx 为唯一对外入口（`${NGINX_PORT:-8080}:80`）；四者均有 healthcheck；`app` 依赖 mysql/redis `service_healthy`。
- `.cnb.yml`：`pull_request` 跑 `make deps-start` → `make test` → `make test-race`；`push`（`main`）额外 `make build` → `make docker-build` → `make docker-push` 与 GitHub 镜像同步；**无部署/发布环境步骤**。
- `prometheus/prometheus.yml`：抓取 `app:8000/metrics`，`rule_files` 引用 `rules/flashsale_latency.yml`；规则为 `flashsale_order_request_duration_p95/p99_seconds`（`histogram_quantile` + `interface="order"`）。
- `test-container.sh` / `test-prometheus.sh` 通过（静态断言 + `docker compose config -q`）。

### A21 E2E 测试的数据副作用

- `setupIsolationServer` 执行 `migrations.Up` + `boot.Bootstrap`，并启动真实 HTTP server（`test/e2e/identity_isolation_test.go:93-133`）。
- `setupFlashSaleServer` 会 `DELETE` 秒杀/商品/库存等相关表，`FlushDB` Redis（`flashsale_test.go:41-51`、`identity_isolation_test.go:106-116`）。
- 本次执行在 `make deps-start` 新建的本地容器库上进行，无既有数据库被清理。

## B. 合理推断

| # | 推断 | 依据 | 为什么还不能认定为事实 |
| --- | --- | --- | --- |
| B1 | 对账与消费并发时，`remaining` 可能被瞬时低估，导致短暂 `SOLD_OUT` | 对账在事务外读 `sold`（已提交值）与 `COUNT(status=queued)`；若某请求已被消费事务锁定但尚未提交，其 `sold+1` 不可见而 `status=queued` 仍可见，公式 `total-sold-inflight` 对该笔重复扣一次 | 需要可控的并发交错实验（注入延迟）才能观测；现有测试未构造该窗口 |
| B2 | 应用时钟与 MySQL 时钟偏差会在时间窗边界产生「闸门放行但消费失败」 | Lua 用 Go `time.Now().Unix()`（`redis.go:214`），消费用 MySQL `NOW()`（`flashsale.go:688-698`） | 未在时钟偏移环境下运行；方向为安全（MySQL 拒绝，不超卖），仅浪费请求 |
| B3 | 单 activity×SKU 的落单吞吐受绑定行 `FOR UPDATE` 串行化限制；单实例每 tick 最多 100 条 | `insertOrderInTx` 先 `loadBindingForUpdate`（`flashsale.go:489/702-715`），同一绑定行串行；`ConsumeQueued(100)` + 1s ticker | 未压测，实际 QPS 取决于事务时延与锁等待，无法从代码直接得出数值 |
| B4 | 活跃活动数 >100 时，`id` 较大的活动长期得不到对账/预热 | `Order("id").Limit(100)` 固定截断，无游标/轮转（`redis.go:390-397`、`:409-418`） | 需要 >100 活动的数据实验；当前代码逻辑高度指向该结论，但未运行 |
| B5 | `flash_sale_order_requests` 长期增长会拖慢出队与备份 | 无清理（A19）+ `idx_dequeue` 需要维护；`COUNT(status=queued)` 周期性执行 | 未造大数据量；影响程度未量化 |
| B6 | 用户首笔请求仍在 `queued` 时，用**新的**幂等键再次下单会被判 12004 | Lua 命中 `bought`（`redis.go:113-115`）→ `handleAlreadyPurchased` → `hasPurchaseEvidence` 把 `status IN (0,1)` 的请求视为已购（`flashsale.go:418-424`） | 未写测试固定该行为；属代码路径推导 |
| B7 | 指标开关每请求读配置，存在微小开销 | `ObserveOrder/ObserveConsume` 每次调用 `Enabled(ctx)`（`metrics.go:72-87`） | 未做基准测试；影响预计可忽略 |
| B8 | 生产规模下出队查询可能不走 `idx_dequeue`（走 PRIMARY 顺序扫描） | 空表 `EXPLAIN` 显示 `key=PRIMARY`、`Using where`；`ORDER BY id LIMIT 1` 与 `OR` 条件促使优化器偏向主键 | 样本仅 1 行，优化器统计不足，不能代表有数据量时的选择 |

## C. 未知事项

| # | 未知 | 缺失证据 | 如何补齐 |
| --- | --- | --- | --- |
| C1 | 生产数据量下的真实执行计划与出队延迟 | 无生产/规模数据；`EXPLAIN` 仅 1 行样本 | 造 10 万~100 万行 `queued/dead` 数据后 `EXPLAIN ANALYZE` + 压测 |
| C2 | 多实例（≥2 进程）并发消费的真实行为 | 测试全部单进程单 goroutine 驱动 | 起两个 app 实例连同一 DB/Redis，压测并核对订单/`sold` 唯一性 |
| C3 | Redis/MySQL 真实故障（停机、超时、分区）下的行为 | 仅 `WRONGTYPE` 与进程内 hook 模拟 | 停容器 / 网络隔离 / 主从切换演练 |
| C4 | 消费者进程被 `SIGKILL` 后的恢复时延与重投次数 | 未做真实 kill 实验 | `kill -9` 后重启，统计 `retry_count` 与订单数 |
| C5 | 死锁/锁等待超时（1213/1205）在高压下的发生率 | 未压测 | 高并发压测 + InnoDB 状态监控 |
| C6 | CNB 流水线当前是否绿 | 本地无法运行 CNB runner | 在 CNB 触发 PR/push 并读取流水线结果 |
| C7 | 部署环境中 Prometheus 是否真的抓取 `/metrics`、指标开关是否开启 | 只有配置文件，无运行环境 | 查看部署环境 Prometheus targets 与 `FLASH_SALE_METRICS_ENABLED` 实际值 |
| C8 | 运营侧是否已把秒杀配额从普通库存划拨（INV-009） | 代码无机械校验，仓库无运营数据 | 运营流程/盘点数据核对 |
| C9 | 历史报告 `docs/reports/surgecart-baseline-audit-v1/` 的结论是否仍成立 | 属旧基线，本任务未逐条复核 | 由后续任务按需对照，不得直接引用 |

## D. 潜在风险（只登记，不修复）

> 每条均给出代码位置、触发条件、可能后果、现有测试覆盖情况。`Status: OPEN` 表示本任务不处理。

### F1 对账/预热固定 `LIMIT 100` 导致活动饥饿

- Severity: MAJOR
- Category: 并发 / 缓存一致性
- Evidence: `internal/logic/flashsale/redis.go:390-397`（活跃活动 `Order("id").Limit(limit)`）、`:409-418`（结束活动同）、`internal/worker/scanner.go:45`（`flashSaleReconcileScanBatch = 100`）
- 触发条件: 同一时刻 `status=enabled AND end_time>NOW()` 的活动数 >100，或处于 60s grace 窗口的结束活动 >100
- 可能后果: 排在后面的活动长期不被预热/收敛；Redis `remaining` 偏离权威值（可能偏大 → 更多请求进 MySQL 后被判失败；偏小 → 误报售罄），活动结束缓存不清理
- 现有测试: `TestReconcileCacheConvergesEndedActivity` 只覆盖 1 个活动；无 >100 活动用例
- 修复归属: 后续专项任务（T02/T03），本任务不修复

### F2 扫描周期配置项宣称支持环境变量，实际不生效

- Severity: MAJOR
- Category: 配置 / 可维护性
- Evidence: `internal/worker/scanner.go:22`、`:55`、`:82` 使用 `MustGet`；GoFrame `gcfg.go:96-112`（`Get` 不读 env）、`:216-226`（`MustGet`→`Get`）；`manifest/config/config.yaml:94` 注释宣称 `FLASH_SALE_RECONCILE_SCAN_INTERVAL / FLASH_SALE_CONSUME_SCAN_INTERVAL`
- 触发条件: 运维/压测通过环境变量调整 `flash_sale.consume_scan_interval` 或 `reconcile_scan_interval`
- 可能后果: 调整被静默忽略，实际仍用配置文件值（1s / 60s）；故障排查会误判配置已生效
- 现有测试: 无（测试直接调用 `ConsumeQueued`，不依赖 ticker）
- 备注: `flash_sale.metrics.enabled` 等使用 `GetEffective` 的键不受影响（A17/A18）

### F3 消费吞吐上限与长事务

- Severity: MAJOR
- Category: 并发 / 容量
- Evidence: `internal/worker/scanner.go:54-75`（单 goroutine、1s ticker、批量 100）、`internal/logic/flashsale/consume.go:36-52`（串行）、`internal/logic/flashsale/flashsale.go:489`/`:702-715`（绑定行 `FOR UPDATE` 串行化同 activity×SKU）
- 触发条件: 秒杀峰值 QPS 远超单实例消费能力，或同一活动×SKU 大量并发落单
- 可能后果: `queued` 积压、结果查询延迟上升；锁等待增加（未量化）
- 现有测试: `TestFlashSaleConcurrentNoOversell` 只验证 20 并发的正确性，不测吞吐
- 备注: 本任务未做压测，不给出容量结论

### F4 `flash_sale_order_requests` 无生命周期管理

- Severity: MAJOR
- Category: 持久化 / 运维
- Evidence: 表定义 `20261001000015_...up.sql:11-28`；全仓库无 DELETE/归档/分区（A19）
- 触发条件: 生产长期运行
- 可能后果: 表与 `idx_dequeue` 无界增长；出队、`COUNT(status=queued)`、备份恢复成本随时间上升
- 现有测试: 无
- 备注: 是否可接受取决于运营保留策略，属产品/架构决策

### F5 可观测性覆盖不足

- Severity: MINOR
- Category: 可观测性
- Evidence: `internal/metrics/metrics.go:43-56`（仅 2 个指标，标签 `interface`/`result`）；全仓库无其他 collector（A16）；无 HTTP 全站指标、无 Redis/MySQL 客户端指标、无队列深度/`sold` 业务 gauge、无独立 `dead` 计数
- 触发条件: 需要从监控判断积压、死信、Redis/DB 健康时
- 可能后果: 排障需直接查 MySQL/Redis；`interface/result` 维度无法区分活动/SKU；`dead` 与 `failed` 混在 `result` 标签
- 现有测试: `internal/metrics/metrics_test.go` 覆盖标签基数与计数；`TestFlashSaleV5MetricsEndpoint` 覆盖端点输出

### F6 `/metrics` 无鉴权，且脱离统一响应中间件

- Severity: MINOR
- Category: 安全 / 暴露面
- Evidence: `internal/cmd/cmd.go:180-187`（`s.BindHandler("GET:/metrics", ...)` 直接写响应）；开关默认 false（`config.yaml:139-141`）
- 触发条件: 开启 `FLASH_SALE_METRICS_ENABLED=true` 且网络边界未限制
- 可能后果: 未授权方读取指标（流量量级、拒绝率、延迟分布）；不泄漏用户/活动维度（标签低基数）
- 现有测试: `TestFlashSaleV5MetricsEndpoint` 只验证 200 与指标存在，不验证鉴权
- 备注: 设计文档已提示「需内网/监控网段可达」，属边界依赖

### F7 设计文档与 V3+ 实现不一致（补偿与标记写入时机）

- Severity: MINOR
- Category: 文档一致性
- Evidence: `docs/design/flash-sale.md:160`「Lua 预扣成功 → MySQL 事务失败 → 同请求补偿 Redis（`INCR remaining`）；一人一单/幂等标记因下单成功后才写入」；`:207-208` 表格「下单成功后写入」；而实现为 `SET` 权威值收敛（`redis.go:327-347`）且标记在 `GATE_PASSED` 写入（`redis.go:134-140`）。文档 `:166-168`、`:224` 已改为 V3 语义，前后自相矛盾
- 触发条件: 后续任务以文档为事实来源
- 可能后果: 误判补偿语义（INCR vs SET）、误判标记写入时机，导致审查/实现偏差
- 现有测试: 无文档一致性检查

### F8 DB 级防御纵深缺口

- Severity: MINOR
- Category: 持久化 / 正确性
- Evidence: `20261001000011_...up.sql:24-37`（无 `CHECK sold<=total_stock`）；`20261001000015_...up.sql:22`（`flash_order_id` 无 FK/唯一约束）；关联一致性仅由同一事务保证（`consume.go:130`）
- 触发条件: 直接 SQL 改库、未来新增写入路径、人工修数据
- 可能后果: 可构造 `sold > total_stock` 或 `requests.success` 指向不存在/他人订单；应用层查询会返回 500（`request.go:131-142`）而非静默错误
- 现有测试: 无（测试不会绕过应用层写库）

### F9 幂等键为用户维度全局唯一，且必须由客户端提供

- Severity: MINOR
- Category: 接口契约
- Evidence: `uk_request_idempotency(user_id,idempotency_key)`（migration 15:26）与 `uk_flash_idempotency(user_id,idempotency_key)`（migration 11:57）；`request_hash=sha256(activity_id|sku_id)`（`flashsale.go:817-820`）；空键直接 1001（`:276-279`）
- 触发条件: 同一用户在不同活动复用同一 `idempotency_key`；或客户端不传键
- 可能后果: 跨活动复用 → 12005；不传键 → 400；无服务端自动生成兜底
- 现有测试: `TestFlashSaleIdempotency` 覆盖同活动同/异内容，不覆盖跨活动复用

### F10 容量保护与观测默认全部关闭

- Severity: MINOR（影响面取决于部署）
- Category: 配置 / 容量
- Evidence: `config.yaml:103-141` 四个开关均 false；读取实现 `ratelimit.go:42`、`queue_capacity.go:27`、`circuit_breaker.go:40`、`metrics.go:63`
- 触发条件: 直接使用仓库默认配置部署
- 可能后果: 默认部署没有限流、排队上限、熔断与 `/metrics`；V5 宣称的容量保护需显式开启后才生效
- 现有测试: 各保护均有开启状态测试（`TestRateLimitUserRejects` 等），但默认关闭路径未单独断言

### F11 消费批次遇首个技术错误即中断

- Severity: MINOR
- Category: 错误处理 / 吞吐
- Evidence: `consume.go:41-46`（`consumeOne` 返回 error 时 `return processed, err`）；`scanner.go:69-71` 仅记录日志
- 触发条件: 批次中某条请求遇到 DB 瞬时错误
- 可能后果: 同批次后续请求本轮不处理，延迟到下一 tick（1s）；持续错误会反复中断（死信上限 4 次仍生效）
- 现有测试: `TestConsumeRetryThenDeadLetter` 覆盖重试/死信，不覆盖「批次中断后续请求延后」

### F12 对账与消费并发时的瞬时低估计（推断风险）

- Severity: MINOR
- Category: 并发 / 可用性
- Evidence: 推断 B1；涉及 `redis.go:304-321` 与 `flashsale.go:463-542`
- 触发条件: 对账扫描与某请求的消费事务并发，且该请求已 `DECR` 预扣
- 可能后果: `remaining` 短暂偏小 → 少量用户收到 12003（售罄），下一周期（≤60s）自愈；不破坏 MySQL 不变量
- 现有测试: 无；未复现

## E. 明确未做的判定

以下结论本任务**不成立、不宣称**：

1. 不宣称系统「能承受某个 QPS」——无压测。
2. 不宣称生产环境「已验证」——只在本地容器 + 测试库运行。
3. 不宣称上表风险「已确认是缺陷」——除 A 类事实外，均为风险或推断。
4. 不引用 `docs/reports/surgecart-baseline-audit-v1/` 的任何结论作为本基线事实。
