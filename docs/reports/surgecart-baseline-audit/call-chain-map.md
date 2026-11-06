# D1 秒杀核心调用链与源码入口地图

> 任务：`surgecart-baseline-audit`（T01） · 产出：D1
> 结论仅对下列 Review Baseline 成立，任何代码变更后必须重新锚定。

## 0. 报告锚点

| 项目 | 值 | 证据 |
| --- | --- | --- |
| 仓库路径 | `/root/projects/my-shop` | `git rev-parse --show-toplevel` |
| 远端 | `origin` = `https://cnb.cool/framework-station/my-shop` | `git remote -v` |
| 审计时分支 | `task/surgecart-baseline-audit` | `git branch --show-current` |
| 审计时 HEAD | `95c13910c7226b4e32487ed046702caf75d74fc6` | `git rev-parse HEAD` |
| 工作区 | clean（`git status --short` 为空） | `git status --short` |
| 代码基线等价性 | `git diff --stat 473d6f5253e3abcb98ece9d79a5a8059563a3e8d..HEAD` 仅 6 个 `.agent/tasks/surgecart-baseline-audit/*.md`（+266 行），`internal/`、`api/`、`test/`、`go.mod`、`go.sum`、`Dockerfile`、`.cnb.yml`、`prometheus/` 均无差异 | 同左命令输出 |
| 因此 | 本报告关于源码的结论对 `95c1391` 与 `473d6f5` 同时成立（源码逐字节相同） | 同上 |

审计执行环境：`go1.24.1 linux/amd64`、Docker `29.9.2`、Docker Compose `v5.6.1`、MySQL 容器 `8.0.46`（由 `make deps-start` 在本机新建）、Redis `7-alpine`（同）。实际执行命令与结果见 D3。

## 1. 链路总览（15 跳）

| # | 环节 | 文件 | 核心函数 / 位置 | 下游 | 关键数据变化 |
| --- | --- | --- | --- | --- | --- |
| 1 | 路由注册（前台） | `internal/cmd/routes_frontend.go` | `RegisterFrontendRoutes` `:33`；`user.POST("/flash-sales/:id/orders", ...)` `:106`；`user.GET("/flash-sales/:id/orders/result", ...)` `:108` | `flashsale.ControllerV1` | 无 |
| 2 | 认证中间件 | `internal/cmd/routes_frontend.go` `:74` → `internal/middleware/auth.go` | `middleware.Auth` `:26`；`auth.ValidateSession` `:36`；`findUserAuth` `:49` | Redis 会话 + `users` 表 | 注入 `Principal{UserID,Sid}`；不写业务数据 |
| 3 | Handler | `internal/controller/flashsale/flashsale.go` | `ControllerV1.CreateOrder` `:22`；`currentUserID` `:82` | `service.FlashSale().CreateOrder` `:27` | 身份只取 Principal |
| 4 | 参数解析 / 校验 | `internal/logic/flashsale/flashsale.go` | `CreateOrder` `:266`；`skuID<=0` `:273`；`idempotency_key` 非空 `:276` | — | 无写入 |
| 5 | 限流（用户级 → 活动级） | `internal/logic/flashsale/ratelimit.go` | `rateLimitUser` `:103`；`rateLimitActivity` `:123`；Lua `:25` | Redis `flashsale:rl:user:*` / `flashsale:rl:activity:*` | INCR 计数；拒绝返回 12009，无预扣 |
| 6 | 排队软上限 | `internal/logic/flashsale/queue_capacity.go` | `checkQueueCapacity` `:58` | Redis `flashsale:queued:{aid}:{sku}` | GET 近似计数；拒绝返回 12010，无预扣 |
| 7 | 熔断器 | `internal/logic/flashsale/circuit_breaker.go` | `allow` `:86`；`recordFailure` `:113`；`recordSuccess` `:135` | 进程内状态机 | Open 时返回 1005/503，无预扣 |
| 8 | Redis Lua 闸门 | `internal/logic/flashsale/redis.go` | `runGate` `:206`（`EVAL` `:207`）；脚本 `flashSaleGateScript` `:65-141` | Redis 6 个 key | `DECR stock` 预扣；`SET bought`、`SET idem` 标记；可能 `SET soldout` |
| 9 | 闸门结果分流 | `internal/logic/flashsale/flashsale.go` | `CreateOrder` switch `:316-335`；`handleIdempotentHit` `:371`；`handleAlreadyPurchased` `:395`；`hasPurchaseEvidence` `:409` | MySQL `flash_sale_orders` / `flash_sale_order_requests` | 命中幂等/已购时读 MySQL 判定「真命中」还是「孤儿标记」 |
| 10 | MySQL 请求记录写入（入队） | `internal/logic/flashsale/request.go` | `enqueue` `:61`；失败补偿 `convergeStockAndMarkers` `:72`；幂等撞键回读 `resultForExistingRequest` `:96` | `INSERT flash_sale_order_requests(status=0)` `:62-69` | 新增 1 行 queued（无显式事务，单语句自动提交）；成功后 `INCR queued` 计数 `:80` |
| 11 | 后台消费者启动 | `internal/cmd/cmd.go` `:139` → `internal/worker/scanner.go` | `StartFlashSaleConsumeScanner` `:54`（ticker，默认 1s，`config.yaml:99`） | `service.FlashSale().ConsumeQueued(ctx,100)` `:69` | 无 |
| 12 | 消费者领取 | `internal/logic/flashsale/consume.go` | `ConsumeQueued` `:36`；`consumeOne` `:70`；领取 SQL `:79-85` | MySQL | `SELECT ... WHERE status=0 AND (next_attempt_at IS NULL OR next_attempt_at<=NOW()) ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`（SQL 已实测，见 §3.1） |
| 13 | 请求处理 / 订单创建 / 库存扣减 / 状态迁移 | `internal/logic/flashsale/flashsale.go` `insertOrderInTx` `:463` + `consume.go` | `resolveSku` `:429`（读 `skus`/`products`）；`insertOrderInTx` `:463-542`；`loadBindingForUpdate` `:702`；`isInTimeWindow` `:688` | 同一事务：`flash_sale_activity_skus` / `flash_sale_orders` / `flash_sale_order_requests` | 锁定绑定行 → `INSERT flash_sale_orders` → `UPDATE ... SET sold=sold+1 WHERE sold<total_stock` → `UPDATE requests SET status=1,flash_order_id=?` |
| 14 | 失败 / 重试 / 死信 | `internal/logic/flashsale/consume.go` | `failRequestInTx` `:183`；`retryOrDeadRequestInTx` `:195`；`classifyConsumeError` `:226`；`consumeBackoffSeconds` `:257` | MySQL 同事务 + 提交后 Redis 收敛 `convergeStockAndMarkers` `:147` | 业务失败 → `status=2`；技术失败 → `retry_count+1`、`next_attempt_at=NOW()+5/10/20s`；第 4 次失败 → `status=3`（dead） |
| 15 | 结果查询 / 对账 | `internal/logic/flashsale/request.go` `GetOrderResult` `:117`；`internal/logic/flashsale/redis.go` `ReconcileCache` `:382` | 查询 `findRequestByActivityKey` `:188`；`loadOrder` `flashsale.go:664`；对账 `syncActivityCache` `:225` / `convergeEndedActivity` `:434` | MySQL 读取；Redis `SET/HSet/Del` | 查询无写入；对账只改 Redis，不改订单与请求 |

### 1.1 管理员侧入口（同一模块）

| 接口 | 路由位置 | Handler | 服务函数 |
| --- | --- | --- | --- |
| `POST /admin/flash-sales` | `internal/cmd/routes_admin.go:147`（`RequirePermission("flash_sale:create")`） | `AdminControllerV1.Create` `controller/flashsale/flashsale.go:48` | `CreateActivity` `flashsale.go:120`（事务写活动+绑定，提交后预热 Redis） |
| `PUT /admin/flash-sales/:id` | `routes_admin.go:148`（`flash_sale:update`） | `Update` `:56` | `UpdateActivity` `:182`（事务更新；`updateBindings` `:548` 行锁校验 `total_stock>=sold`） |
| `POST /admin/flash-sales/requests/:id/repair` | `routes_admin.go:150`（`flash_sale:repair`） | `RepairRequest` `:65` | `RepairRequest` `repair.go:41`（仅 `dead→queued`，条件更新 + 审计同事务） |
| `GET /admin/flash-sales/requests/:id/audits` | `routes_admin.go:151`（`flash_sale:repair`） | `ListRequestAudits` `:74` | `ListRequestAudits` `repair.go:115` |

## 2. 逐跳细节与数据变化

### 2.1 入口 → Handler（同步部分）

```text
POST /flash-sales/:id/orders
  routes_frontend.go:106 (user group, middleware.Auth :74)
    → middleware.Auth (auth.go:26)  [JWT 验签 → Redis 会话校验 :36 → users.status/auth_epoch 校验 :49-56]
    → ControllerV1.CreateOrder (controller/flashsale/flashsale.go:22)
    → service.FlashSale().CreateOrder(ctx, userID, activityID, req) (:27)
```

- `CreateOrderReq` 只含 `Id`(path)、`SkuId`、`IdempotencyKey`（`api/flashsale/v1/flash_sale.go:104-109`）；价格、用户身份均不接受客户端提交。
- 返回 `CreateOrderRes{Status:"queued", ...}`（`api/flashsale/v1/flash_sale.go:112-117`），即「已受理」而非「已成交」。

### 2.2 同步段：限流 → 熔断 → Lua → 入队（`flashsale.go:266-342`）

| 顺序 | 代码 | Redis key | 副作用 |
| --- | --- | --- | --- |
| 1 | `rateLimitUser` `:285` | `flashsale:rl:user:{userID}` | INCR+EXPIRE；超限 12009 |
| 2 | `rateLimitActivity` `:288` | `flashsale:rl:activity:{activityID}` | 同上 |
| 3 | `checkQueueCapacity` `:291` | `flashsale:queued:{activityID}:{skuID}` | 只读；超限 12010 |
| 4 | `breaker.allow` `:296` | 无 | 进程内状态 |
| 5 | `runGate` `:304` | `flashsale:null/activity/stock/bought/idem/soldout` | 原子预扣与标记 |
| 6 | `enqueue` `:341` | 无 | `INSERT flash_sale_order_requests` |

**先 Redis 后 MySQL**：Lua（第 8 跳）在 `INSERT`（第 10 跳）之前，两者不在同一事务内；顺序由 `CreateOrder` 的语句顺序决定（`:304` 之后才在 `:341` 调 `enqueue`）。

### 2.3 Lua 闸门（`redis.go:65-141`）

脚本内部顺序（全部在 Redis 内原子完成）：

1. `EXISTS null` → `NOT_FOUND`（空值/负缓存）
2. `HGETALL activity`；`status != '1'` → `NOT_FOUND`
3. 时间窗：`now < start or now >= finish` → `NOT_IN_WINDOW`（`now` 由 Go 传入 `time.Now().Unix()`，`redis.go:214`）
4. `EXISTS soldout` → `SOLD_OUT`
5. `GET idem`：与 `ARGV[2]`（request_hash）相同 → `IDEMPOTENT_HIT`；不同 → `IDEMPOTENT_CONFLICT`
6. `EXISTS bought` → `ALREADY_PURCHASED`
7. `GET stock` 为 nil → `SKU_NOT_BOUND`
8. `remaining<=0` → `markSoldout` + `SOLD_OUT`
9. `DECR stock`；`after<0` 则 `INCR` 回补 + `SOLD_OUT`
10. `SET bought=1 EX ttl`、`SET idem=hash EX ttl` → `GATE_PASSED`（V3 起标记在此写入）

TTL 统一为 `finish - now + grace`（`grace=60s`，`redis.go:37`）。

### 2.4 入队写入（`request.go:61-92`）

- 单条 `INSERT`（`g.DB().Model(...).InsertAndGetId()`），**没有外层事务**（自动提交）。
- 撞 `uk_request_idempotency` 时先 `convergeStockAndMarkers`（`:72`）再读回既有请求（`:73-76`）。
- 成功后 `incrQueueCount`（`:80`，best-effort）并返回 queued。

### 2.5 消费者领取（`consume.go:70-131`）

实测 SQL（`DATABASE_DEFAULT_DEBUG=true` 运行 `TestConsumeRetryThenDeadLetter` 抓取，见 D3 §6）：

```sql
SELECT `id`,`user_id`,`activity_id`,`sku_id`,`idempotency_key`,`request_hash`,`status`,`retry_count`,
       `next_attempt_at`,`last_error_code`,`flash_order_id`
FROM `flash_sale_order_requests`
WHERE (`status`=0) AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
ORDER BY `id` LIMIT 1
FOR UPDATE SKIP LOCKED
```

- 领取在消费事务内完成（`g.DB().Transaction` `:76`），行锁持续到提交/回滚。
- 多实例并发时 `SKIP LOCKED` 跳过被其他事务锁定的行，实现互斥领取。
- 无独立「已领取/处理中」状态，也无租约/可见性超时；进程崩溃 → 事务回滚 → 行保持 `queued` 可被重新领取。

### 2.6 单事务落单（`consume.go:107-130` + `flashsale.go:463-542`）

实测事务序列（同一 `txid:3`，D3 §6 原始日志）：

```sql
BEGIN
SELECT ... FROM flash_sale_orders WHERE ...              -- 唯一约束预检失败路径（撞号测试）
INSERT INTO flash_sale_orders(...) VALUES(...)           -- 订单
UPDATE flash_sale_activity_skus SET sold=sold + 1 ... WHERE (id=?) AND (sold < total_stock)
UPDATE flash_sale_order_requests SET status=1, flash_order_id=? WHERE id=?
COMMIT
```

`insertOrderInTx` 内部顺序：活动存在/启用校验（`:470-476`）→ MySQL `NOW()` 时间窗（`:480`）→ `FOR UPDATE` 锁绑定行（`:489`）→ 行锁下判 `sold>=total_stock`（`:499`）→ 插入订单（`:506`）→ 条件扣减 `sold`（`:530-540`）。

### 2.7 客户端查询结果（`request.go:117-150`）

```text
GET /flash-sales/:id/orders/result?idempotency_key=...
  → ControllerV1.GetOrderResult (controller:31)
  → s.GetOrderResult (request.go:117)
  → findRequestByActivityKey (user_id + activity_id + idempotency_key) (:188)
  → status=success 时 loadOrder(user_id, flash_order_id) (:135, flashsale.go:664)
  → 返回 status / order / fail_code
```

不存在与非本人统一 404（`:127`）；`success` 但订单缺失 → 500（`:133`、`:140`）为防御性不一致告警。

### 2.8 后台任务（三个扫描器，均 `goroutine + ticker`）

| 扫描器 | 启动位置 | 周期 | 实际职责 |
| --- | --- | --- | --- |
| 订单超时取消 | `cmd.go:135` → `scanner.go:21` | `order.cancel_scan_interval`（60s） | 与秒杀无关（普通订单） |
| 秒杀缓存对账 | `cmd.go:137` → `scanner.go:81` | `flash_sale.reconcile_scan_interval`（60s，`config.yaml:97`） | 只收敛 Redis：活跃活动重算 `remaining=total-sold-inflight` 并回补预热；已结束且在 60s grace 内的活动做终态收敛与缓存失效（`redis.go:382-483`）。**不扫描/不修改 `flash_sale_order_requests`、`flash_sale_orders`** |
| 秒杀异步消费 | `cmd.go:139` → `scanner.go:54` | `flash_sale.consume_scan_interval`（1s，`config.yaml:99`） | 每 tick 调 `ConsumeQueued(ctx,100)`，串行逐条消费 |

`ctx` 取消（SIGINT/SIGTERM）后扫描器停止领取新任务（`cmd.go:130-139`、`scanner.go:65-67`），`s.Run()` 返回后 `wg.Wait()`（`cmd.go:161-163`）。

## 3. 必答问题

| 问题 | 结论 | 证据 |
| --- | --- | --- |
| 请求从哪个路由进入 | `POST /flash-sales/:id/orders`（前台登录用户组，`middleware.Auth`） | `routes_frontend.go:106`、`:74` |
| 调用了哪些 Handler / Service / Repository | `ControllerV1.CreateOrder` → `service.FlashSale().CreateOrder`（接口 `internal/service/flashsale.go:17`）→ `sFlashSale`（`logic/flashsale/flashsale.go`）；数据访问直接用 `g.DB()` / `g.Redis()`（GoFrame Active Record），**无独立 Repository 层** | 同左 |
| Redis Lua 在哪个位置执行 | `CreateOrder` → `runGate`（`redis.go:206`）→ `g.Redis().Do(ctx,"EVAL",script,6,...)`（`:207-215`） | 同左 |
| Lua 与 MySQL 的先后关系 | 先 Lua 预扣+写标记，后 MySQL `INSERT` 请求；两者非跨系统原子，失败由 `convergeStockAndMarkers` 幂等收敛 | `flashsale.go:304`→`:341`；`request.go:72`；`redis.go:327-347` |
| MySQL 请求记录在哪个函数写入 | `enqueue`（`request.go:61`），`INSERT flash_sale_order_requests`（`:62-69`） | 同左 |
| 消费者从哪里启动 | `cmd.serve`（`cmd.go:139`）→ `worker.StartFlashSaleConsumeScanner`（`scanner.go:54`）→ `ConsumeQueued`（`consume.go:36`） | 同左 |
| 消费者如何并发领取任务 | 每个实例独立 ticker + 每次 `consumeOne` 单事务 + `FOR UPDATE SKIP LOCKED`（实测 SQL） | `consume.go:76-87`；D3 §6 |
| 领取后如何避免重复处理 | 行锁 + 同事务内状态更新（`status=1/2/3`）+ 订单唯一约束（`uk_flash_one_per_user`/`uk_flash_idempotency`/`uk_flash_order_no`） | `consume.go:172-222`；migration 11:56-58 |
| 订单、库存、请求状态是否同一事务 | 是。同一 `txid` 内 `INSERT order` → `UPDATE sold` → `UPDATE requests.status` | D3 §6 实测事务日志 |
| 失败时如何处理 | 业务失败→`failed(2)`；技术失败→退避重试（5/10/20s），第 4 次→`dead(3)`；终态失败提交后收敛 Redis + 清标记；入队失败同请求补偿并返回 5xx；Lua/Redis 故障→503 fail-closed | `consume.go:104/116/118/195-222`；`request.go:70-78`；`flashsale.go:305-313` |
| 客户端如何查询最终结果 | `GET /flash-sales/:id/orders/result?idempotency_key=`，按 `user_id+activity_id+key` 查请求，`success` 时返回订单 | `request.go:117-150` |
| 对账任务具体扫描什么、修改什么 | 扫描 `flash_sale_activities`（`status=1 AND end_time>NOW()`，以及 `end_time<=NOW()` 且在 60s grace 内），按活动×SKU 重算并 `SET` Redis `remaining`、收敛排队计数、失效/置空活动缓存；**不修改 MySQL 请求与订单** | `redis.go:382-427`、`:225-280`、`:434-483` |

## 4. 闸门返回码 → 客户端语义

| Lua 返回 | 处理位置 | 业务码 / HTTP |
| --- | --- | --- |
| `NOT_FOUND` | `flashsale.go:317` | 12001 / 404 |
| `NOT_IN_WINDOW` | `:319` | 12002 / 409 |
| `SOLD_OUT` | `:321` | 12003 / 409 |
| `ALREADY_PURCHASED` | `:323` → `handleAlreadyPurchased` `:395` | 真已购 12004 / 409；孤儿标记则清标记后继续入队 |
| `IDEMPOTENT_HIT` | `:326` → `handleIdempotentHit` `:371` | 返回既有请求状态；孤儿标记则清标记后继续入队 |
| `IDEMPOTENT_CONFLICT` | `:329` | 12005 / 409 |
| `SKU_NOT_BOUND` | `:331` | 1001（400） |
| `GATE_PASSED` | `:333` | 继续 `enqueue` |
| Lua 执行错误 / Redis 不可用 | `:305-313` | 1005 / 503（fail-closed） |
| 熔断 Open | `:296-302` | 1005 / 503 |

业务码与 HTTP 映射见 `internal/codes/codes.go:104-113` 与 `:226-235`。

## 5. 端到端数据流（文本图）

```text
客户端
  │ POST /flash-sales/:id/orders  (idempotency_key, sku_id)
  ▼
middleware.Auth ── JWT ──▶ Redis 会话 (iam:*) ──▶ users 表
  ▼
限流(Redis rl:*) → 排队上限(Redis queued:*) → 熔断(进程内)
  ▼
Lua 闸门 (Redis)：null → activity → window → soldout → idem → bought → stock DECR
  │ GATE_PASSED（已预扣 + bought/idem 标记）
  ▼
INSERT flash_sale_order_requests (status=0 queued)     ← 非事务，单语句
  ▼  返回 200 {status:"queued"}
………… 异步 …………
ticker 1s → ConsumeQueued(100) → 每 ticker 循环 consumeOne
  ▼
tx: SELECT ... FOR UPDATE SKIP LOCKED
    → resolveSku (skus/products 读)
    → loadBindingForUpdate (FOR UPDATE)
    → INSERT flash_sale_orders
    → UPDATE sold=sold+1 WHERE sold<total_stock
    → UPDATE requests SET status=1, flash_order_id=?
  COMMIT
  ▼
成功：订单行 + sold+1 + request=success（原子）
失败：failed(2) / 重试(queued, backoff) / dead(3)
  ▼
提交后：DECR 排队计数；终态失败 → 幂等 SET remaining + DEL 标记
  ▼
客户端轮询 GET .../orders/result → status / order / fail_code
  ▼
每 60s 对账：Redis remaining = total_stock - sold - inflight_queued
```

## 6. 本文件的范围声明

- 只描述当前 `95c1391` 的实现，不评价设计优劣，不给修复建议（风险见 D4）。
- 行号来自上述 SHA 的文件内容；代码变更后行号可能漂移。
