# SurgeCart 秒杀链路工程基线审计报告（surgecart-baseline-audit-v1）

> 本文只描述**一个特定代码版本**上的现状，不构成长期设计结论，也不修改任何生产代码、Schema、配置或文档设计。
> 行号、SQL 与行为结论均以 §0 的锚点 SHA 为准；其他版本需重新核验。
> 证据获取方式只有三种：源码/SQL 原文、可复现命令的真实输出、真实数据库 Schema。设计文档与 README 的意图性表述一律不进入「已验证事实」。

## 0. 审查锚点（AC-001）

| 项 | 值 |
| --- | --- |
| 仓库 | `cnb.cool/go-cloud-devops/my-shop`（`origin` = `https://cnb.cool/framework-station/my-shop`） |
| 分支 | `task/surgecart-baseline-audit-v1` |
| **被审计代码版本（完整 40 位 SHA）** | `7ba5a31077a7b8977473765c2d9a7d402ddadb8c` |
| 审计开工时该分支 `git rev-parse HEAD` | `7ba5a31077a7b8977473765c2d9a7d402ddadb8c`（与上一行逐字符一致） |
| 基线分支 / 基线 commit | `develop` / `85402302622d059918e1a87b83bbb55b400d1a85` |
| 审计开工时工作区状态 | `git status --short` 输出为空：无暂存、无未暂存、无未跟踪文件 |
| 基线 → 锚点的代码差异 | 仅新增 `.agent/tasks/surgecart-baseline-audit-v1/{task.md,state.yaml}`（2 files, +124）；未改动 `internal/`、`api/`、`internal/migrations/`、`Dockerfile`、`.cnb.yml`、`prometheus/`、`docs/design/` |
| 审计执行日期 | 2026-10-29（容器时间为 UTC+8） |

**锚点自引用说明（必须如实声明）**：本报告自身也是提交物，因此报告落盘后分支 HEAD 会前进到 C1 提交，`git rev-parse HEAD` 不再等于上表 SHA。上表 SHA 绑定的是**被审计代码版本**，不是报告自身的提交。可机械复核的等价命令：

```bash
git rev-parse HEAD                                   # 报告落盘后：C1 的 40 位 SHA
git diff --name-only 7ba5a31077a7b8977473765c2d9a7d402ddadb8c HEAD
# 期望：只出现 docs/reports/surgecart-baseline-audit-v1/ 下的新增文件与 .agent/tasks/.../state.yaml
git diff --stat 7ba5a31077a7b8977473765c2d9a7d402ddadb8c HEAD -- \
  internal api internal/migrations Dockerfile .cnb.yml prometheus docs/design
# 期望：空输出（被审计代码在本报告落盘前后完全一致）
```

**本报告的全部结论只对 `7ba5a31077a7b8977473765c2d9a7d402ddadb8c` 成立。**

### 0.1 审计执行环境（用于解释 §6 的可执行性）

| 项 | 值 | 来源 |
| --- | --- | --- |
| Go | `go1.24.1 linux/amd64`（`go.mod` 声明 `go 1.23.0`） | `go version` |
| Docker | `29.6.2-7` | `docker version --format '{{.Server.Version}}'` |
| MySQL | `mysql:8.0` 容器（`127.0.0.1:3306`），root/root | `docker compose` + `make deps-start` |
| Redis | `redis:7-alpine` 容器（`127.0.0.1:6379`），无密码 | 同上 |
| 依赖就绪方式 | `MYSQL_ROOT_PASSWORD=root AUTH_JWT_SECRET=<占位> make deps-start`（退出码 0） | 本次真实执行 |

依赖可用是本报告能给出**真实通过/失败**而不是全量 `NOT_EXECUTED` 的前提；这也是与「依赖缺失时只能静态阅读」的任务前提差异，已如实记录在 §6。

---

## 1. 结论摘要

1. 秒杀链路是**「Redis Lua 准入预扣 + MySQL 请求表排队 + goroutine/ticker 消费者落单」**的三段式异步结构，HTTP 下单**不同步建单**：HTTP 返回 `status=queued` 只代表请求已持久受理（`internal/migrations/sql/20261001000015_flash_sale_order_requests.up.sql:1-9`、`internal/logic/flashsale/request.go:56-92`）。
2. 成功订单的唯一业务事实仍是 `flash_sale_orders` 行存在；它由消费者在**单事务**内与 `sold` 扣减、请求状态置 `success` 一起提交（`internal/logic/flashsale/consume.go:76-131`、`internal/logic/flashsale/flashsale.go:463-542`）。
3. **MySQL 请求表 `flash_sale_order_requests` 同时是「状态机」和「出队队列」**，但它不承担「一人一单」最终约束、不承载预扣/补偿流水、也没有 `processing` 状态或租约（§5）。
4. 并发正确性最终由「绑定行 `FOR UPDATE` + 条件更新 `WHERE sold < total_stock` + `RowsAffected` + 三个唯一约束」兜底；Redis 只是加速闸门（§4）。
5. 本轮在真实 MySQL/Redis 环境下跑通了 `go build`、`go vet`、`make test-unit`、`make test-integration`、`make test-race`、`make test-prometheus`、`make test-container`、`make test-nginx` 与完整 `make test`，退出码全为 0（§6）。
6. 已记录 4 类需要 T02/T03 专项核查的疑点：**消费端 SKU 读取的技术性失败被直接判为业务终态 `failed`**、**请求表缺少一人一单唯一约束**、**容量保护开关默认关闭且配置读取失败静默回退**、**单元门禁未覆盖 `internal/worker` 测试**（§7.4、§8）。

---

## 2. HTTP 入口与鉴权（调用链第 0~4 跳）

| # | 跳 | 位置（文件:行） | 符号 | 行为 |
| --- | --- | --- | --- | --- |
| J0 | 服务装配 | `internal/cmd/cmd.go:151-160` | `serve` / `s.Group("/", …)` | 注册 `middleware.RequestID`（154）与 `middleware.Response`（155），再调用 `RegisterFrontendRoutes`（158）、`RegisterAdminRoutes`（159） |
| J1 | 路由注册 | `internal/cmd/routes_frontend.go:73-74`、`:106` | `RegisterFrontendRoutes` | 先建 `Auth` 中间件分组（74），再注册 `POST /flash-sales/:id/orders` → `flashsaleCtrl.CreateOrder`（106） |
| J2 | API 契约 | `api/flashsale/v1/flash_sale.go:104-109` | `CreateOrderReq` | `g.Meta path:"/flash-sales/:id/orders" method:"post"`；请求体只有 `sku_id` 与 `idempotency_key`，**不接受价格与身份** |
| J3 | 认证中间件 | `internal/middleware/auth.go:26`（`Auth`） | `middleware.Auth` | 验签 → `auth.ValidateSession(Redis)`（36）→ 每请求查 `users` 状态与 `auth_epoch`（50）；任一失败 fail-closed 401 |
| J4 | Controller | `internal/controller/flashsale/flashsale.go:22-28`（`currentUserID` 82-88） | `ControllerV1.CreateOrder` | 从 `Principal` 取 `UserID`，缺失 401；随后调用 `service.FlashSale().CreateOrder(ctx, userID, req.Id, req)` |
| J4' | Service 接口 | `internal/service/flashsale.go:17` | `IFlashSale.CreateOrder` | 接口定义，实现由 `internal/logic/flashsale/flashsale.go:50-52` 的 `init()` 注册 |

同源入口（同一鉴权分组）：

- 结果查询：`routes_frontend.go:108` → `controller/flashsale/flashsale.go:31-37` → `request.go:117-150`（API 契约 `api/flashsale/v1/flash_sale.go:120-124`）。
- 后台写接口：`internal/cmd/routes_admin.go:147-151`（`flash_sale:create` / `update` / `repair`）→ `controller/flashsale/flashsale.go:48-79`。

---

## 3. 下单调用链逐跳（HTTP → 受理 → 消费 → 订单）

### 3.1 同步段（HTTP 请求内，只到「入队」为止）

| # | 跳 | 位置（文件:行） | 符号 | 行为 / 关键分支 |
| --- | --- | --- | --- | --- |
| S1 | Logic 入口 | `internal/logic/flashsale/flashsale.go:266-342` | `(*sFlashSale).CreateOrder` | 入口 + `defer` 指标观测（268-270） |
| S2 | 参数校验 | `flashsale.go:272-279` | 同上 | `sku_id <= 0` 或幂等键 trim 后为空 → `CodeInvalidArgument`（1001），**无任何写操作** |
| S3 | 请求指纹 | `flashsale.go:281` → `:817-820` | `requestHash` | `sha256hex(activity_id 与 sku_id 用竖线拼接)`；不含用户与价格 |
| S4 | 用户级限流 | `flashsale.go:285-287` → `ratelimit.go:103-120` | `rateLimitUser` | Redis 固定窗口 INCR+EXPIRE（`ratelimit.go:25-31`、`:94-100`）；超限 `12009`；Redis 失败 fail-open |
| S5 | 活动级限流 | `flashsale.go:288-290` → `ratelimit.go:123-140` | `rateLimitActivity` | 同上，key `flashsale:rl:activity:{id}` |
| S6 | 排队软上限 | `flashsale.go:291-293` → `queue_capacity.go:58-75` | `checkQueueCapacity` | 读 `flashsale:queued:{activity}:{sku}`，`>= max` → `12010`；Redis 失败 fail-open |
| S7 | 熔断闸门 | `flashsale.go:296-302` → `circuit_breaker.go:86-109` | `(*circuitBreaker).allow` | 进程内 Closed/Open/Half-Open；Open 期 `1005`；多实例不共享 |
| S8 | **Redis Lua 准入** | `flashsale.go:304-314` → `redis.go:206-220`（`EVAL` 在 207） | `runGate` | 单脚本 6 KEYS / 3 ARGV（`redis.go:207-215`），脚本体 `redis.go:65-141`；失败 → `1005` 且 `recordFailure`（306） |
| S9 | Lua 判定顺序 | `redis.go:73-140` | `flashSaleGateScript` | `null 标记`(73) → `活动 Hash 存在`(77) → `status/start/end`(82-93) → `soldout 标记`(101) → **幂等**(105-111) → **一人一单**(113-115) → `remaining` 读(117-120) → `DECR` 预扣(127-132) → 写 `bought`/`idem` 标记(134-140) |
| S10 | 结果分流 | `flashsale.go:316-335` | `switch gate` | `NOT_FOUND`→12001；`NOT_IN_WINDOW`→12002；`SOLD_OUT`→12003；`ALREADY_PURCHASED`→12004 分支；`IDEMPOTENT_HIT`→既有请求；`IDEMPOTENT_CONFLICT`→12005；`SKU_NOT_BOUND`→1001 |
| S11 | 已购命中自愈 | `flashsale.go:323-325` → `:395-425` | `handleAlreadyPurchased` / `hasPurchaseEvidence` | 先查 `flash_sale_orders` 是否存在，再查 `flash_sale_order_requests` 中 `status ∈ {queued, success}`；均无则清 `bought` 标记后直接入队（**不重跑 Lua 闸门、不重新预扣**） |
| S12 | 幂等命中自愈 | `flashsale.go:326-328` → `:371-390` | `handleIdempotentHit` / `request.go:153-163` | 按 `(user_id, idempotency_key)` 读请求；存在且 hash 相同→返回既有状态；hash 不同→12005；不存在→清标记后入队 |
| S13 | **入队（生产代码中唯一的本表 INSERT）** | `flashsale.go:337-341` → `request.go:61-92`（INSERT 在 `62-69`） | `enqueue` | 单条 `INSERT flash_sale_order_requests(status=0)`，无显式事务 |
| S14 | 入队失败补偿 | `request.go:70-78` | `enqueue` | 先 `convergeStockAndMarkers`（权威值 SET + 清标记），撞 `uk_request_idempotency` 时读回既有请求（`request.go:96-113`），其余 → 1000 |
| S15 | 受理响应 | `api/flashsale/v1/flash_sale.go:111-117` | `CreateOrderRes` | 固定返回 `status=queued`；**不返回订单** |

### 3.2 异步段（后台消费者，落单与状态回写）

| # | 跳 | 位置（文件:行） | 符号 | 行为 / 关键分支 |
| --- | --- | --- | --- | --- |
| C0 | 扫描器启动 | `internal/cmd/cmd.go:139` → `internal/worker/scanner.go:54-75`（调用 69） | `StartFlashSaleConsumeScanner` | `flash_sale.consume_scan_interval` 默认 1s；每轮 `ConsumeQueued(ctx, 100)`（批量常量 47-48） |
| C1 | 批量消费入口 | `internal/logic/flashsale/consume.go:36-52` | `ConsumeQueued` | 循环调用 `consumeOne`，上限 `limit`（默认 100） |
| C2 | 单条事务 | `consume.go:76-131` | `consumeOne` / `g.DB().Transaction` | 一个事务内完成「领取 + 落单 + 状态回写」 |
| C3 | **领取（出队）** | `consume.go:79-85` | `LockUpdateSkipLocked` | 等价 SQL（GoFrame 生成）：`SELECT * FROM flash_sale_order_requests WHERE status=0 AND (next_attempt_at IS NULL OR next_attempt_at<=NOW()) ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`；无行可领则整轮退出（88-90） |
| C4 | SKU/商品快照 | `consume.go:99` → `flashsale.go:429-456` | `resolveSku` | 经 `service.Sku().GetByID` / `service.Product().GetByID` 校验 `sku.status=enabled`、`product.status=on_shelf`；**使用 `ctx`（非 `tx`）读取** |
| C5 | 落单事务体 | `consume.go:108` → `flashsale.go:463-542` | `insertOrderInTx` | ①活动存在/启用（470-476）②`MySQL NOW()` 时间窗（480-486，`isInTimeWindow` 688-698）③绑定 `FOR UPDATE` 并快照秒杀价（489-495，`loadBindingForUpdate` 702-715）④行锁下 `sold >= total_stock` 预检（499-501）⑤插入订单（506-519）⑥唯一约束冲突分流（520-525）⑦条件扣减（530-540） |
| C6 | 唯一约束分流 | `flashsale.go:520-525` + `consume.go:226-254` | `duplicateKeyName` / `classifyConsumeError` | `uk_flash_one_per_user`→业务失败 12004；`uk_flash_idempotency`→12005；`uk_flash_order_no`→技术失败重试 |
| C7 | 状态回写 | `consume.go:130`→`172-180`；`104`/`116`→`183-191`；`118`→`195-222` | `successRequestInTx` / `failRequestInTx` / `retryOrDeadRequestInTx` | `success` 同时写 `flash_order_id`；`failed` 写 `last_error_code`；技术失败 `retry_count+1` + `DATE_ADD(NOW(), INTERVAL n SECOND)`，`retry_count=4 > 3` 时落 `dead`（193-209） |
| C8 | 提交后补偿 | `consume.go:141-148` → `redis.go:368-371`（`327-347`、`350-357`）、`queue_capacity.go:100-107` | `convergeStockAndMarkers` / `decrQueueCount` | 终态失败后按权威值重算 `remaining = total_stock - sold - inflight_queued` 并 `SET`，再 `DEL` 标记；Redis 失败仅日志（`redis.go:330-346`） |
| C9 | 观测 | `consume.go:150-167` | `metrics.ObserveConsume` | 仅对「领取到行」的请求观测；`success`/`failed`/`dead`/`error` 四类结果 |

### 3.3 结果查询、对账与修复

| # | 跳 | 位置（文件:行） | 符号 | 行为 |
| --- | --- | --- | --- | --- |
| Q1 | 查询路由 | `routes_frontend.go:108` → `controller/flashsale/flashsale.go:31-37` | `GetOrderResult` | 归属取自 `Principal.UserID` |
| Q2 | 查询实现 | `internal/logic/flashsale/request.go:117-150`（`findRequestByActivityKey` 188-200） | `(*sFlashSale).GetOrderResult` | 按 `user_id + activity_id + idempotency_key` 查；未命中 404；`success` 时再 `loadOrder`（`flashsale.go:664-673`），请求与订单不一致则 500（131-142） |
| RC1 | 对账启动 | `cmd.go:137` → `scanner.go:81-102`（调用 96） | `StartFlashSaleReconcileScanner` | 默认 60s；每轮 `ReconcileCache(ctx, 100)`（44-45） |
| RC2 | 对账实现 | `redis.go:382-427`（活跃活动 389-405；已结束 grace 窗口 407-425） | `ReconcileCache` | 活跃：`syncActivityCache` 重算 `remaining`（`redis.go:225-280`，公式在 259）；已结束：`convergeEndedActivity`（434-483） |
| A1 | 人工修复路由 | `routes_admin.go:150-151` | `RequirePermission("flash_sale:repair")` | 修复 + 审计查询 |
| A2 | 修复实现 | `internal/logic/flashsale/repair.go:41-112`（事务 78-107；条件更新 79-87；`RowsAffected` 91-94；审计插入 95-105） | `RepairRequest` | 仅 `dead→queued`；重置 `retry_count/next_attempt_at/last_error_code`；审计与状态同事务 |

### 3.4 抽查复核（AC-002 的机械验证）

以下命令在锚点版本上真实执行，输出行号与上表一致：

```bash
grep -n 'EVAL", flashSaleGateScript' internal/logic/flashsale/redis.go        # 207
grep -n 'LockUpdateSkipLocked' internal/logic/flashsale/consume.go            # 84
grep -n 'Where("sold < total_stock")' internal/logic/flashsale/flashsale.go   # 532
grep -n 'flash_sale_order_requests").Ctx(ctx).Data' internal/logic/flashsale/request.go  # 62
grep -n 'flash-sales/:id/orders' internal/cmd/routes_frontend.go api/flashsale/v1/flash_sale.go  # 106 / 105
grep -n 'ConsumeQueued(ctx, flashSaleConsumeScanBatch)' internal/worker/scanner.go  # 69
```

抽查的 3 跳（Redis 准入、消费者领取、条件扣减）均能在源码中定位到同名实现，链路终点可追到 `insertOrderInTx` 的单事务（库存扣减 530-540 与订单插入 506-519 同一 `tx`）。

---

## 4. 数据层定位清单（AC-003）

### 4.1 表与 migration

| 表 | Migration | 建表行 | 用途 |
| --- | --- | --- | --- |
| `flash_sale_activities` | `internal/migrations/sql/20261001000011_flash_sale.up.sql` | 11-22 | 活动（`status/start_time/end_time`） |
| `flash_sale_activity_skus` | 同上 | 24-37 | 活动 × SKU 绑定（秒杀价 + 初始库存 + 已售） |
| `flash_sale_orders` | 同上 | 39-61 | 秒杀订单（软引用快照自足，行存在 = 成功） |
| `flash_sale_order_requests` | `..._20261001000015_flash_sale_order_requests.up.sql` | 11-28 | 异步请求生命周期 + 出队队列 |
| `flash_sale_request_audits` | `..._20261001000018_flash_sale_request_audits.up.sql` | 7-20 | 人工修复审计（append-only） |

真实库核对（`SHOW CREATE TABLE`，MySQL 8.0 容器，退出码 0）：五张表均存在，`flash_sale_activity_skus` 含 `UNIQUE KEY uk_activity_sku (activity_id,sku_id)` 与 `fk_flash_sku_activity … ON DELETE CASCADE`；`flash_sale_orders` 含三个唯一键；`flash_sale_order_requests` 含 `uk_request_idempotency (user_id,idempotency_key)` 与 `idx_dequeue (status,next_attempt_at,id)`；`flash_sale_request_audits` 含 `idx_request_id`、`idx_operator`。

### 4.2 状态取值

| 域 | 取值 | 位置 |
| --- | --- | --- |
| 活动 `status`（DB TINYINT） | `1=enabled`、`0=disabled` | `flashsale.go:36-37`；migration `11:14` |
| 请求 `status`（DB TINYINT） | `0=queued`、`1=success`、`2=failed`、`3=dead` | `request.go:20-25`；migration `15:18` |
| 请求状态（API 字符串） | `queued/success/failed/dead` | `api/flashsale/v1/flash_sale.go:19-24`；映射 `request.go:43-54` |
| 活动「已结束」 | 由 `end_time` 派生，不存终态 | 代码仅 `status 0/1`（`flashsale.go:36-37`）；缓存侧用 `a.EndTs <= now` 判结束（`redis.go:243`）；仓库内无状态翻转任务 |

### 4.3 事务边界

| 事务 | 位置 | 包含 | **不包含（易误解点）** |
| --- | --- | --- | --- |
| 创建活动 | `flashsale.go:135-158` | `flash_sale_activities` + 全部 `flash_sale_activity_skus` | Redis 预热在提交后单独执行（165-170），失败只记日志 |
| 更新活动 | `flashsale.go:224-239` | 活动行 + 绑定行（`updateBindings` 逐条 `FOR UPDATE` 后更新，548-575） | 同上，缓存同步在 246-251 |
| 入队 | `request.go:62-69` | **无显式事务**，单条 INSERT 自动提交 | 不含 Redis 预扣（前一步已发生，跨系统非原子） |
| 消费（核心） | `consume.go:76-131` | 领取行锁 + 订单插入 + `sold` 扣减 + 请求状态回写 | `resolveSku` 的 SKU/商品读取走 `ctx` 默认连接（`consume.go:99`），**不与请求行锁同事务** |
| 人工修复 | `repair.go:78-107` | 请求状态条件更新 + 审计插入 | 不含 Redis 操作 |

### 4.4 约束 / 条件更新 / 索引 → 不变量映射

| 机制 | 定义位置 | 保护的不变量 |
| --- | --- | --- |
| `UPDATE … SET sold = sold + 1 WHERE sold < total_stock` + `RowsAffected` | `flashsale.go:530-540` | **库存不为负**、**成功订单数 ≤ 初始库存**（INV-001/002）；`INT UNSIGNED` 为第二道兜底（migration `11:29-30`） |
| 绑定行 `SELECT … FOR UPDATE` | `flashsale.go:702-715`（下单）、`consume.go:84`（请求行） | 同一绑定行的并发下单串行化；请求行多实例互斥领取 |
| `uk_flash_one_per_user (activity_id, sku_id, user_id)` | migration `11:58` | **一人一单**（INV-003），并驱动消费错误分流（`consume.go:230-231`） |
| `uk_flash_idempotency (user_id, idempotency_key)` | migration `11:57` | **重复请求不重复扣库存**（INV-005） |
| `uk_flash_order_no (order_no)` | migration `11:56` | 订单号唯一；撞号按技术失败重试（`consume.go:234-235`） |
| `uk_request_idempotency (user_id, idempotency_key)` | migration `15:26` | **请求级幂等**（INV-012）；重复入队不产生第二行 |
| 单事务「建单 + 扣减 + 状态」 | `consume.go:76-131` | **失败不建单**、无「已扣库存无订单」半成品（INV-004/007/013） |
| `UPDATE … WHERE id=? AND status=dead` + `RowsAffected` | `repair.go:79-94` | 并发修复仅一次生效（INV-017） |
| `idx_dequeue (status, next_attempt_at, id)` | migration `15:27` | 出队扫描的索引支撑（非正确性约束） |
| `idx_status`、`idx_time`、`idx_sku_id`、`idx_activity`、`idx_user`、`idx_request_id`、`idx_operator` | migration `11:20-21,35,59-60`；`15:27`；`18:18-19` | 查询/对账支撑（非正确性约束） |
| Redis Lua 单脚本 `DECR` + 回滚 `INCR` | `redis.go:127-132` | Redis 侧 `remaining ≥ 0`（INV-010；仅闸门，非事实来源） |

---

## 5. MySQL 请求表职责判定（AC-004）

### 5.1 结论

`flash_sale_order_requests` 在锚点版本上同时承担 **「持久受理凭据 + 请求状态机 + 出队队列 + 重试/死信账本 + 请求级幂等 + 结果查询事实源」**，**不承担** **「成功订单事实、库存预扣、一人一单最终约束、处理中租约、补偿流水」**。

| 职责 | 是否承担 | 证据 |
| --- | --- | --- |
| 持久化受理（HTTP 返回前落库） | **是** | `request.go:62-69`；HTTP 成功返回前已完成 INSERT（`flashsale.go:341`） |
| 排队（待消费） | **是** | `status=0` + `idx_dequeue`（migration `15:18,27`）；领取 `consume.go:79-85` |
| 状态查询 | **是** | `request.go:117-150`、`188-200` |
| 请求生命周期状态机 | **是**（4 态） | `request.go:20-25`；状态迁移 `consume.go:172-222` |
| 请求级幂等 | **是** | `uk_request_idempotency`（migration `15:26`）；冲突读回 `request.go:96-113` |
| 技术重试 / 死信 | **是** | `retry_count` / `next_attempt_at` / `last_error_code`（migration `15:19-22`；`consume.go:195-222`） |
| 崩溃恢复载体 | **是**（被动） | 事务回滚 → 行仍 `queued`（`consume.go:76`；`scanner.go:50-53` 注释） |
| **成功订单事实** | **否** | 事实在 `flash_sale_orders`（migration `11:39-61`）；请求仅存 `flash_order_id` 软引用（`15:22`） |
| **库存预扣/准入判定** | **否** | Redis Lua（`redis.go:65-141`）；MySQL 侧只是最终条件扣减（`flashsale.go:530-540`） |
| **一人一单最终约束** | **否**（关键） | 本表**没有** `(activity_id, sku_id, user_id)` 唯一键；该约束只在 `flash_sale_orders`（migration `11:58`）。排队阶段仅靠 Redis `bought` 标记（`redis.go:113-115,138`）+ 应用查询（`flashsale.go:409-425`） |
| 处理中状态 / 租约 / 可见性超时 | **否** | 无 `processing` 状态；设计亦声明不设（`flash-sale.md:81`），代码无相关实现 |
| 补偿/对账流水 | **否** | 无补偿表；补偿按权威值重算（`redis.go:327-347`） |
| 取消/退款/超时终态 | **否** | 无相关列与接口；`status` 仅 4 值 |

附带的只读消费者：后台运营大屏 `dashboard.FlashSales` 直接对本表做 `status` 分组计数（`internal/logic/dashboard/dashboard.go:183-199`，其中 `:192` 为本表入口），因此该表的状态语义同时被运营看板消费；看板只读、不写。

### 5.2 消费者如何领取与处理

| 机制 | 实现 | 说明 |
| --- | --- | --- |
| 领取 SQL | `consume.go:79-85` | `SELECT … WHERE status=0 AND (next_attempt_at IS NULL OR next_attempt_at<=NOW()) ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`，每事务一条 |
| 并发保护 | `FOR UPDATE SKIP LOCKED` + 事务 + 状态原子更新 | 多实例跳过已锁行，不阻塞；状态更新在提交时生效（`consume.go:130`） |
| 失败分类 | `consume.go:226-254` | 唯一约束与业务码 → `failed` 终态；其余 → 技术重试 |
| 重试 | `consume.go:195-221` | `retry_count+1`，指数退避 5s/10s/20s（`consume.go:257-266`，封顶 60s），期间保持 `queued` |
| 死信 | `consume.go:197-209` | `retry_count` 达到 4（`newRetry > 3`）落 `dead`，不建单、不扣库存 |
| 超时回收 | **不存在** | 无「queued 超时」扫描；只有 `next_attempt_at` 退避到期才可再领 |
| 崩溃恢复 | 事务回滚 + 重领 | 领取锁随回滚释放，请求仍 `queued`；`consumeBeforeCommitHook`（`consume.go:32`）仅测试注入 |
| 人工恢复 | `repair.go:41-112` | 仅 `dead→queued`（12008 拒绝其他状态），与审计同事务 |
| 积压可见性 | 部分（仅全局聚合） | 后台大屏 `current_queued` = `COUNT(status=0)` 全局聚合（`dashboard.go:205-216`）；无按活动切片、无告警；Redis 近似计数 `flashsale:queued:*`（`queue_capacity.go:22-24`）仅用于软上限 |

### 5.3 代码与 `docs/design/flash-sale.md` 的差异（逐条）

| # | 设计文档表述 | 代码实际 | 性质 |
| --- | --- | --- | --- |
| D1 | `flash-sale.md:160`「Lua 预扣成功 → MySQL 事务失败 → 同请求补偿 Redis（`INCR remaining`）；一人一单/幂等标记因下单成功后才写入，故无需补偿」 | 补偿是**权威值 SET**（`redis.go:327-347`），且标记在 `GATE_PASSED` 时即写入（`redis.go:138-139`），失败需 `DEL` 清除（`redis.go:350-357`） | **文档陈旧（V2 描述残留在 V3/V4 段落）** |
| D2 | `flash-sale.md:207-208` Key 表：`bought`/`idem` 语义为「下单成功后写入」 | 实际在 Lua `GATE_PASSED` 内写入（`redis.go:134-140`）；同文档 `:224` 又写明 V3 行为 | **文档内部自相矛盾** |
| D3 | `flash-sale.md:88` 出队 `… ORDER BY id LIMIT N FOR UPDATE SKIP LOCKED` | 每事务 `LIMIT 1`（`consume.go:83`），批量语义由外层循环上限实现（`consume.go:41`） | 表述粒度差异，语义等价 |
| D4 | `flash-sale.md:167`「技术失败 `retry_count+1` + 退避，超上限落 `dead`」 | 只有 `insertOrderInTx` 的错误经 `classifyConsumeError` 分类；**`resolveSku` 的任何错误（含 DB 技术错误）直接落 `failed`**（`consume.go:99-105`） | **行为与设计意图不一致（见 R1）** |
| D5 | `flash-sale.md:131`「`failed/dead` 时 Redis 预扣与标记已补偿、无残留」 | 补偿在事务提交后 best-effort 执行，Redis 失败只记日志（`redis.go:330-346`），最终靠对账≤60s 收敛 | 精度差异（「已补偿」应为「提交后补偿 + 对账兜底」） |
| D6 | `flash-sale.md:339`「关键配置错误不能被静默忽略」 | 限流/排队/熔断/指标开关读取失败一律回退默认（`ratelimit.go:42-48`、`queue_capacity.go:27-33`、`circuit_breaker.go:40-46`、`metrics.go:63-69`），默认关闭 | **行为与设计表述冲突（见 R4）** |
| D7 | `flash-sale.md:242`「按 `flash_sale_id + user_id + idempotency_key` 验证归属」 | 代码字段名为 `activity_id`（`request.go:188-193`），语义相同 | 命名漂移，无行为差异 |

> 说明：`flash-sale.md` 中 V1/V2 历史段落（如 `:173` V2 降级走纯 MySQL、`:223` V2 标记写入时机）属于历史版本说明，不与当前实现冲突，未计入差异清单。

---

## 6. 测试与门禁基线（AC-005）

### 6.1 实际执行记录（全部在锚点版本上执行）

| # | 命令 | 退出码 | 状态 | 关键输出 / 说明 |
| --- | --- | --- | --- | --- |
| E1 | `go build ./...` | 0 | PASS | 无输出 |
| E2 | `go vet ./...` | 0 | PASS | 无输出 |
| E3 | `make test-unit` | 0 | PASS | `internal/auth`、`internal/metrics`、`internal/logic/health`、`internal/logic/dashboard`、`internal/controller/health` 整包通过；混合包 `-run` 精确匹配通过 |
| E3' | ↑ 的覆盖缺口 | 0 | **部分空转** | 脚本第 35 行 `-run 'TestScannersStopOnContextCancel' ./internal/cmd/...` 输出 `[no tests to run]`：该测试实际位于 `internal/worker`（`internal/worker/scanner_test.go:14`），单元门禁未执行它 |
| E4 | `MYSQL_ROOT_PASSWORD=root AUTH_JWT_SECRET=<占位> make deps-start` | 0 | PASS | MySQL 8.0 / Redis 7-alpine 容器 healthy（本地已有镜像） |
| E5 | `make test-integration`（`go test -p 1 ./...`，`DATABASE_DEFAULT_PASS=root`） | 0 | PASS | 全部包 `ok`；`internal/logic/flashsale 3.734s`、`test/e2e 89.353s`、`internal/migrations 27.206s`、`internal/boot 15.326s` |
| E6 | `make test-race` | 0 | PASS | `go test -race -count=1 -run 'TestCircuitBreakerStateMachine' ./internal/logic/flashsale/...` → `ok 1.041s` |
| E7 | `make test-prometheus` | 0 | PASS（含跳过项） | 静态断言通过；**`promtool` / `prometheus` 镜像缺失 → recording rules 语法校验跳过**（脚本自身 WARN）→ 该子项 `NOT_EXECUTED` |
| E8 | `make test-container` | 0 | PASS | Dockerfile / `.dockerignore` / compose / 凭据分离 / entrypoint 顺序 / 编排断言全过 |
| E9 | `make test-nginx` | 0 | PASS（含跳过项） | 静态断言通过；**`my-shop-nginx:local` 镜像未构建 → `nginx -t` 跳过** → `NOT_EXECUTED` |
| E10 | `make test`（vet + lib + unit + integration + registry + prometheus + container + nginx） | 0 | PASS | 末尾 `测试全部通过`；`check-registry.sh` 输出「未发现 Reservation 重复或 Registry ↔ 实现明显不一致」 |
| E11 | `go test -count=1 -p 1 ./internal/logic/flashsale/... ./internal/worker/...`（隔离库 `my_shop_audit`，`DATABASE_DEFAULT_NAME=my_shop_audit`） | 0 | PASS | `flashsale 5.673s`、`worker 0.006s`；**非缓存**，用于排除 E5 中 `(cached)` 的影响 |
| E12 | `go test -count=1 -run 'FlashSale' ./test/e2e/...`（隔离库） | 1 | 见说明 | 20 个 `TestFlashSale*` 全部 PASS；同一 `-run` 正则还匹配到 `TestDashboardFlashSales` 并失败，失败点为夹具清理 `DELETE FROM products` 被 `fk_skus_product` 阻止（`dashboard_test.go:587`），属同库残留数据，与本链路无关 |
| E13 | `SHOW CREATE TABLE` ×5（`docker exec my-shop-mysql`） | 0 | PASS | 五表结构与 §4.1 完全一致（唯一键/索引/FK 逐项核对） |
| E14 | 锚点抽查 grep ×6（§3.4） | 0 | PASS | 6 个行号全部命中 |

补充说明（诚实性）：

- E5 与 E10 中部分包显示 `(cached)`，E11/E12 是对秒杀相关包用 `-count=1` 的独立复跑。
- E12 的隔离库复跑暴露了**测试夹具对同库残留数据敏感**（不同包清理顺序会因 FK RESTRICT 失败）。在 `make test-integration` 的 `-p 1 ./...` 全量顺序下 E5/E10 通过；单独重跑某个包（脏库）会失败于 setup 清理阶段，**不是业务断言失败**。该现象已在 §7.4 记为风险。
- 本轮为执行测试创建了 Docker 容器（`my-shop-mysql`、`my-shop-redis`）与命名卷，并额外创建隔离库 `my_shop_audit`；均为本地测试环境副作用，不进入 Git。

### 6.2 `NOT_EXECUTED` 清单

| 检查 | 状态 | 原因 | 影响 |
| --- | --- | --- | --- |
| `promtool check rules prometheus/rules/flashsale_latency.yml` | **NOT_EXECUTED** | 镜像内无 `promtool`，本地无 `prometheus` 镜像 | recording rules 仅做文本/结构静态断言，语法正确性未由 Prometheus 解析器证实 |
| `nginx -t`（`make test-nginx` 动态项） | **NOT_EXECUTED** | 未构建 `my-shop-nginx:local` | Nginx 配置仅静态断言 |
| `docker build` / `make docker-build` | **NOT_EXECUTED** | 非本任务 AC 要求；避免引入无关产物 | Dockerfile 仅静态核实（§6.3） |
| 压测 / 容量基线 / p95-p99 实测 | **NOT_EXECUTED** | Task Out of Scope 明确排除压测与性能结论 | 吞吐与延迟无实测数据（§7.3） |
| 生产环境部署验证 | **NOT_EXECUTED** | 无生产环境访问权 | 生产实际开关/版本未知（§7.3） |
| `EXPLAIN` 出队 SQL 执行计划 | **NOT_EXECUTED** | 本轮未做执行计划分析 | 索引实际利用情况未知（§7.3） |

### 6.3 静态核实（`.cnb.yml` / `Dockerfile` / `prometheus/`）

| 文件 | 核实内容 |
| --- | --- |
| `.cnb.yml:25-51` | `main.pull_request` 流水线：`start-dependencies`（`make deps-start`）→ `make test`（47）→ `make test-race`（50）；无 `Dockerfile` 构建 |
| `.cnb.yml:52-88` | `main.push` 流水线：`make test`（70）→ `make test-race`（73）→ `make build`（76）→ `make docker-build`（80）→ `make docker-push`（84）；另有 `sync-to-github-mirror`（条件执行） |
| `Dockerfile:15` | 构建阶段 `golang:1.23-alpine`，`CGO_ENABLED=0` 静态编译（25） |
| `Dockerfile:30,53,58` | 运行阶段 `alpine:3.20`；`USER 10001:10001` 非 root；`ENTRYPOINT ["/app/docker-entrypoint.sh"]`（先 `migrate up` 再 `serve`，由 `scripts/test-container.sh` 断言顺序） |
| `prometheus/prometheus.yml` | `rule_files: /etc/prometheus/rules/flashsale_latency.yml`；`scrape_configs` 抓 `app:8000/metrics`，15s |
| `prometheus/rules/flashsale_latency.yml:20-23` | 两条 recording rules：p95/p99，`histogram_quantile(…, sum by (le) (rate(flashsale_request_duration_seconds_bucket{interface="order"}[5m])))` |
| `internal/metrics/metrics.go:44-55` | `flashsale_requests_total`、`flashsale_request_duration_seconds`，标签仅 `interface`、`result`（低基数） |
| `internal/cmd/cmd.go:180-187` | `/metrics` 仅在 `flash_sale.metrics.enabled=true` 时挂载，且位于统一 JSON 响应中间件之外 |
| `manifest/config/config.yaml:95-141` | `reconcile_scan_interval: 60`、`consume_scan_interval: 1`；`rate_limit.enabled: false`、`queue_capacity.enabled: false`、`circuit_breaker.enabled: false`、`metrics.enabled: false` |

### 6.4 测试资产分布（秒杀相关）

| 层 | 文件 | 用例数 | 说明 |
| --- | --- | --- | --- |
| 白盒集成（真实 MySQL/Redis） | `internal/logic/flashsale/{consume,recovery,capacity,redis,consume_metrics}_test.go` | 4+7+5+2+1 = 19 | 重试/死信、崩溃重投、孤儿标记自愈、收敛、限流/排队/熔断、指标计数 |
| 纯内存 | `internal/logic/flashsale/{circuit_breaker,order_outcome}_test.go` | 2+1（order_outcome 含 14 个子用例） | 熔断状态机（含 race 用例）、结果→指标标签映射 |
| HTTP 端到端（真实路由） | `test/e2e/flashsale{,_v2,_v3,_v5}_test.go`（`flashsale_v2_test.go` 含 1 个 V3 用例） | 10+4+4+2 = 20 | 权限、时间窗、定价快照、一人一单、幂等、并发不超卖、失败无副作用、更新边界、修复审计、V2 预热/售罄/对账、V3 fail-closed/入队查询/消费幂等/补偿、V5 限流与指标端点（按用例语义分组：`TestFlashSaleV3FailClosed` 物理位于 `flashsale_v2_test.go`，计入 V3） |
| 后台扫描器 | `internal/worker/scanner_test.go` | 1 | 三个扫描器 ctx 取消后退出（**单元门禁未执行**，见 E3'） |

关键测试入口（供 Owner 理解，最多两个）：

1. `test/e2e/flashsale_test.go:477` `TestFlashSaleConcurrentNoOversell`：20 并发抢 5 件，断言受理数=5、消费后 `sold=5`、订单数=5、`total-sold ≥ 0` —— 覆盖「Redis 准入 + 最终 MySQL 结果断言」。该用例**无法独立证明 MySQL 库存预检与条件扣减的正确性**：Redis 闸门在并发下只放行 5 条，MySQL 侧从未出现超额并发，因此同时移除 `flashsale.go:499-501` 的库存预检与 `:530-540` 的条件扣减后，该用例仍会通过；该覆盖缺口登记为 §8.2 第 14 项待核查事项。
2. `internal/logic/flashsale/consume_test.go:363` `TestConsumeCrashBeforeCommitRedeliversOnce`：注入「订单已写、状态未更新即回滚」，断言重投只产生一次业务效果（订单 1 条、`sold=1`）——覆盖 INV-015 崩溃重投幂等。

---

## 7. 事实分区（AC-006）

### 7.1 已验证事实（附证据锚点）

| # | 事实 | 证据 |
| --- | --- | --- |
| F1 | 秒杀下单 HTTP 入口为 `POST /flash-sales/:id/orders`，位于 `Auth` 分组内 | `routes_frontend.go:73-74,106`；`api/flashsale/v1/flash_sale.go:105` |
| F2 | 下单接口**不同步建单**，成功响应固定为 `status=queued` | `flashsale.go:337-341`；`api/flashsale/v1/flash_sale.go:111-117`；E5 中 `TestFlashSaleV3EnqueueAndResultQuery` PASS |
| F3 | 准入判定在单个 Redis Lua 脚本内完成，顺序为 null→活动→时间窗→售罄→幂等→一人一单→库存→预扣→写标记 | `redis.go:65-141`（`EVAL` 在 207） |
| F4 | Redis 不可用/脚本失败 → HTTP `1005`（503），不落请求、不预扣 | `flashsale.go:304-313`；`internal/codes/codes.go:24`（`CodeServiceUnavailable=1005`）；E5 中 `TestFlashSaleV3FailClosed` PASS |
| F5 | 请求入队是单条 INSERT，无显式事务，撞 `uk_request_idempotency` 时读回既有请求 | `request.go:61-92,96-113` |
| F6 | 消费者以 `FOR UPDATE SKIP LOCKED` 每事务领取 1 条，锁持有至事务结束 | `consume.go:79-85` |
| F7 | 落单事务包含「活动校验 + MySQL 时间窗 + 绑定行 `FOR UPDATE` + 库存预检 + 插单 + 条件扣减 + 状态回写」 | `consume.go:76-131`；`flashsale.go:463-542` |
| F8 | 库存扣减为条件更新 + `RowsAffected`，且 `INT UNSIGNED` 双重兜底 | `flashsale.go:530-540`；migration `11:29-30` |
| F9 | 三个订单唯一键与一个请求唯一键在真实库中存在，键名与代码分流分支一致 | E13；migration `11:56-58`、`15:26`；`consume.go:230-235` |
| F10 | 技术失败退避为 5s/10s/20s（封顶 60s），`retry_count` 到 4 落 `dead` | `consume.go:195-222,257-266`；E5 中 `TestConsumeRetryThenDeadLetter` PASS |
| F11 | 终态失败后 Redis 补偿为「权威值重算 + 幂等 SET + 清标记」，不是 `INCR` | `redis.go:304-347,350-357,368-371`；E5 中 `TestConvergeStock*` PASS |
| F12 | 人工修复仅 `dead→queued`，条件更新 + `RowsAffected` + 审计同事务 | `repair.go:41-112`；E5 中 `TestRepairRequestDeadToQueuedAndAudit` PASS |
| F13 | 三个后台扫描器由 `serve` 启动，周期分别为 60s/1s/60s，可用 ctx 取消 | `cmd.go:135-139`；`scanner.go:21-102`；E11 `TestScannersStopOnContextCancel` PASS |
| F14 | 结果查询按 `(user_id, activity_id, idempotency_key)` 隔离，`success` 时再读订单 | `request.go:117-150,188-200`；E5 `TestFlashSaleV3EnqueueAndResultQuery` PASS |
| F15 | 六个 `make` 目标与全量 `make test` 在当前环境全部退出码 0 | §6.1 E1-E10（真实输出） |
| F16 | 单元门禁未覆盖 `internal/worker` 的扫描器测试（`[no tests to run]`） | §6.1 E3 + E3'；`scripts/test-unit.sh:35` vs `internal/worker/scanner_test.go:14` |
| F17 | 限流/排队/熔断/指标四类开关在仓库默认配置中均为 `false`，代码读取失败亦回退关闭 | `manifest/config/config.yaml:105,120,130,141`；`ratelimit.go:42-48` 等 |

### 7.2 推断（附依据与缺口）

| # | 推断 | 依据 | 缺口 / 如何证伪 |
| --- | --- | --- | --- |
| I1 | 单实例消费吞吐上限约 100 条/秒 | 扫描周期 1s（`scanner.go:55`）× 每轮 100（`scanner.go:47-48`） | 未压测；未计事务耗时与锁等待；多实例可线性提升 |
| I2 | 同一活动×SKU 的并发落单被绑定行锁串行化，吞吐受单行锁竞争限制 | `flashsale.go:702-715` + `530-540` | 未压测；未测不同 `total_stock` 下的锁等待 |
| I3 | Redis 标记丢失（TTL 过期/数据丢失）时，同一用户可用不同幂等键产生多条 `queued`，最终只有一条落单、其余消费时命中 `uk_flash_one_per_user` 落 `failed(12004)` | `redis.go:136-139`（TTL = 活动结束 + grace）；`flashsale.go:409-425`；`consume.go:230-231` | 未做「删除 bought 标记后并发下单」的注入测试；结论由代码路径推出 |
| I4 | 指标仅在单实例、开关开启时可近似与 MySQL 对账；多实例需聚合后才等价 | `metrics.go:44-55,72-87`（进程内 Counter） | 多实例聚合语义未验证；无对账脚本 |
| I5 | `idx_dequeue` 对 `next_attempt_at IS NULL OR <= NOW()` 的范围利用可能受限（OR 条件） | migration `15:27`；`consume.go:81` | 未执行 `EXPLAIN`；需在真实数据量下验证 |
| I6 | 排队计数 TTL 只在首次 INCR 时设置，活动长于 86400s 时计数可能提前过期 | `queue_capacity.go:94` → `ratelimit.go:94-100`（`n==1` 才 EXPIRE） | 未构造长周期活动验证；对账会按活动 TTL 重置（`queue_capacity.go:111-119`） |
| I7 | 生产是否启用四类开关无法从仓库判断 | 仓库默认 `false`；部署期由环境变量覆盖 | 需读取部署环境配置（不在仓库内） |

### 7.3 未知事项（附核验方式）

| # | 未知 | 核验方式 |
| --- | --- | --- |
| U1 | 生产环境 MySQL 版本与隔离级别（本地为 8.0，代码未显式设置隔离级别） | 生产 `SELECT VERSION()`、`@@transaction_isolation` |
| U2 | 生产是否开启 `flash_sale.*` 四类开关与具体阈值 | 读取生产 `manifest/config` 与容器环境变量 |
| U3 | 真实流量下 `idx_dequeue` 的执行计划与回表成本 | `EXPLAIN` / `EXPLAIN ANALYZE`（需真实数据量） |
| U4 | 崩溃窗口（预扣→入队）在真实流量中的发生频率与 `remaining` 高估幅度 | 故障注入 + 对账前后 `remaining` 对比 |
| U5 | `last_error_code` 在真实运行中的分布（技术失败占比、`dead` 数量） | 生产 `SELECT last_error_code, status, COUNT(*) … GROUP BY` |
| U6 | 多实例下 Half-Open 探测与多实例各自熔断的实际行为差异 | 多实例故障注入实验（当前仅单实例 race 测试） |
| U7 | 消费积压与端到端延迟（queued → success）的实际分布 | 生产监控（当前无该指标；仅 `consume` 处理耗时） |

### 7.4 潜在风险（只记录，不修复）

| # | 风险 | 证据 | 影响面 |
| --- | --- | --- | --- |
| R1 | **消费端 SKU/商品读取的技术性失败被当作业务失败终态**：`resolveSku` 任意 error 直接 `failed`，不进入重试/死信路径 | `consume.go:99-105`（对比 `:108-118` 的分类逻辑） | 一次 DB 抖动即把请求永久判 `failed`（`last_error_code=1000`），用户需换幂等键重下；不可自动恢复，且无告警 |
| R2 | **请求表没有一人一单唯一约束**：`(activity_id, sku_id, user_id)` 唯一性只在 `flash_sale_orders` | migration `15:26`（仅 user+key）；`11:58`（orders 才有） | Redis 标记丢失/多实例标记缺失时可产生多条 `queued`；最终仅一条落单，其余以 `failed(12004)` 收场；不超卖，但状态噪声与 Redis 预扣浪费 |
| R3 | **孤儿标记自愈路径不重跑闸门、不重新预扣** | `flashsale.go:376-379`、`:403-404`（注释明示「预扣已在原 GATE_PASSED 发生」） | 若原预扣实际已丢失/已被补偿（如 `convergeStock` 已执行），`remaining` 会短暂高估；只靠 1 个对账周期（60s）修正，期间可多放行请求（MySQL 兜底不超卖） |
| R4 | **容量保护与观测开关默认关闭，配置读取失败静默回退** | `config.yaml:105,120,130,141`；`ratelimit.go:42-48`、`queue_capacity.go:27-33`、`circuit_breaker.go:40-46`、`metrics.go:63-69` | 生产误配时限流/排队/熔断/指标全部静默不生效，且无启动期校验；与设计 §11.7「关键配置错误不能被静默忽略」冲突 |
| R5 | **单元门禁未覆盖扫描器测试** | `scripts/test-unit.sh:35` 指向 `internal/cmd`，测试在 `internal/worker`；E3' 输出 `[no tests to run]` | ctx 取消/停机语义只在集成门禁（需 DB）中被覆盖；CI 若只跑 `test-unit` 会漏检 |
| R6 | **`dead` 请求无自动回收**：只有人工 `dead→queued` | `repair.go:41-112`；无 dead 超时扫描（`scanner.go` 仅三个扫描器） | 无人值守时请求永久 `dead`，用户侧永远看到 `dead`；Redis 预扣已补偿，无超卖但需人工介入 |
| R7 | **`queued` 无超时终态**：消费者长期停机时请求无限滞留 | `consume.go:79-85`（仅退避到期可领）；无 stale 扫描 | `GET result` 永远 `queued`；仅后台大屏全局 queued 计数（`dashboard.go:215`），无按活动切片与告警 |
| R8 | **活动更新无行锁/版本控制**：`UpdateActivity` 直接 `UPDATE … WHERE id=?` | `flashsale.go:224-232` | 两名管理员并发更新时后写覆盖先写（丢失更新）；影响运营配置而非库存不变量 |
| R9 | **双时钟源判时间窗**：闸门用 Go `time.Now()`，消费用 MySQL `NOW()` | `redis.go:214` vs `flashsale.go:688-693` | 进程与 DB 时钟漂移/时区配置不一致时，闸门与落单对边界请求判定可能相反（表现为入队后在消费侧 12002 失败） |
| R10 | **消费事务内的 SKU 快照读取不在同一事务** | `consume.go:99` 传 `ctx` 而非 `tx` | 快照与请求行锁不构成同一事务视图；极端并发下读到与事务内校验不同的 SKU/商品状态（影响快照一致性，不影响库存不变量） |
| R11 | **测试夹具对同库残留数据敏感**：跨包 `DELETE` 受 FK RESTRICT 阻止 | E12（`dashboard_test.go:587`）；单独重跑 `internal/logic/flashsale` 时 `recovery_test.go:22` 同类失败 | 影响可重复性：脏库上局部重跑会 setup 失败，容易被误读为业务回归 |

---

## 8. T02 / T03 审查输入（AC-007）

> `T02`、`T03` 为 Owner 后续任务编号，本报告**不推断其范围、路线与结论**，只提供可直接复核的证据锚点与待核查问题。

### 8.1 可直接复核的证据清单

| ID | 证据 | 复核方式 |
| --- | --- | --- |
| EV-01 | 审查锚点 = `7ba5a31077a7b8977473765c2d9a7d402ddadb8c`（分支 `task/surgecart-baseline-audit-v1`） | `git rev-parse 7ba5a31…`；`git diff --name-only 7ba5a31… HEAD` |
| EV-02 | 下单入口与鉴权分组 | `internal/cmd/routes_frontend.go:73-74,106,108` |
| EV-03 | Lua 准入脚本全文与判定顺序 | `internal/logic/flashsale/redis.go:65-141`（`EVAL` 207） |
| EV-04 | 入队 INSERT 与幂等冲突读回 | `internal/logic/flashsale/request.go:61-92,96-113` |
| EV-05 | 领取 SQL（`SKIP LOCKED`） | `internal/logic/flashsale/consume.go:79-85` |
| EV-06 | 落单事务与条件扣减 | `internal/logic/flashsale/flashsale.go:463-542`（扣减 530-540） |
| EV-07 | 失败分类 / 重试 / 死信 | `internal/logic/flashsale/consume.go:195-222,226-254,257-266` |
| EV-08 | 补偿为权威值收敛 | `internal/logic/flashsale/redis.go:304-347,368-371` |
| EV-09 | 请求表 Schema（唯一键/索引） | `internal/migrations/sql/20261001000015_flash_sale_order_requests.up.sql:11-28`；真实库 `SHOW CREATE TABLE`（E13） |
| EV-10 | 订单表三个唯一键 | `internal/migrations/sql/20261001000011_flash_sale.up.sql:56-58` |
| EV-11 | 人工修复 + 审计原子性 | `internal/logic/flashsale/repair.go:41-112` |
| EV-12 | 门禁真实结果（含跳过项） | §6.1 E1-E10；复跑命令见「命令」列 |
| EV-13 | 单元门禁空转证据 | `scripts/test-unit.sh:35`；`make test-unit` 输出 `[no tests to run]`（E3'） |
| EV-14 | 风险 R1 的代码锚点 | `internal/logic/flashsale/consume.go:99-105` |
| EV-15 | 开关默认值与静默回退 | `manifest/config/config.yaml:105,120,130,141`；`ratelimit.go:42-48` 等 |

### 8.2 仍需专项核查的问题清单

**一致性与原子性**

1. R1（`resolveSku` 错误直接 `failed`）是否应改为技术重试？若改为重试，`retry_count` 上限与 `dead` 语义是否需要同步调整？（锚点：`consume.go:99-105`）
2. R3（孤儿标记自愈不重跑闸门）是否会在「预扣已补偿」场景下造成可观测的 `remaining` 高估？高估窗口是否只能是 1 个对账周期？（锚点：`flashsale.go:376-379,403-404`；`redis.go:327-347`）
3. R9（双时钟源）在容器与 DB 时区一致的前提下是否仍存在边界不一致？需要什么实验证明？（锚点：`redis.go:214`；`flashsale.go:688-693`）
4. R10（SKU 快照读不在消费事务内）是否被设计接受为「读后快照」？是否需要把读取移入 `tx`？（锚点：`consume.go:99`）

**恢复与重试**

5. R6/R7：是否需要 `queued` 超时终态与 `dead` 自动回收？当前无自动恢复是否符合对外承诺的「受理即最终会有结果」？（锚点：`consume.go:79-85`；`repair.go:41-112`）
6. 崩溃窗口「预扣→入队」依赖对账释放，是否需要更强的可观测证据（如 `remaining` 与 `total-sold-inflight` 的差值指标）？（锚点：`flash-sale.md:191` 与 `redis.go:382-427`）

**容量与观测**

7. R4：四类开关默认关闭 + 静默回退是否需要启动期校验或健康检查暴露？（锚点：`config.yaml:105-141`；`metrics.go:63-69`）
8. I1/I2：在目标 QPS 下，1s ticker × 100 批量与单绑定行锁是否构成瓶颈？（需压测，本轮 `NOT_EXECUTED`）
9. I5：`idx_dequeue` 是否被 `OR` 条件削弱？是否需要 `next_attempt_at` 默认值化（如 `COALESCE` 或写入时置 NOW）？（锚点：migration `15:27`；`consume.go:81`）

**文档一致性**

10. D1/D2：`docs/design/flash-sale.md` §5.3 与 §6.1 的 V2 表述是否应清理或标注为历史？（锚点：`flash-sale.md:160,207-208` 与 `:224`）
11. D4：设计称「技术失败重试」，代码对 `resolveSku` 错误不重试，哪一侧是权威语义？（锚点：`flash-sale.md:167`；`consume.go:99-105`）

**测试与门禁**

12. R5：`scripts/test-unit.sh:35` 是否应改为 `./internal/worker/...`，并同步脚本注释中的「cmd（扫描器）」描述？
13. R11：测试夹具的清理顺序/隔离库策略是否需要独立任务处理（当前脏库局部重跑会失败）？
14. MySQL 库存兜底缺少可独立区分正确与错误的测试覆盖：`TestFlashSaleConcurrentNoOversell`（`test/e2e/flashsale_test.go:477`）只断言「Redis 准入放行数 + 最终 MySQL 结果」，而 Redis 闸门在 20 并发下只放行 5 条，MySQL 侧从未出现超额并发，因此该用例无法检测 `flashsale.go:499-501` 的库存预检与 `flashsale.go:530-540` 的条件扣减被移除。是否需要补充能在 MySQL 侧独立证伪这两道兜底的测试，待 T02 专项核查；本任务仅登记缺口，未修改生产代码与测试。

---

## 9. AC 对照与变更范围

| AC | 覆盖章节 | 可复核点 |
| --- | --- | --- |
| AC-001 | §0 | 仓库/分支/40 位 SHA/工作区状态 + 锚点自引用说明与等价核验命令 |
| AC-002 | §2、§3（J0-J4、S1-S15、C0-C9、Q1-Q2、RC1-RC2、A1-A2）+ §3.4 抽查 | 每跳含文件:行+符号；6 条 grep 命令真实命中 |
| AC-003 | §4（表/migration/状态/事务/约束/索引/条件更新 → 不变量映射） | migration 行号 + 真实库 `SHOW CREATE TABLE` |
| AC-004 | §5（职责判定 + 领取/并发/重试/死信/超时/崩溃恢复 + 与设计差异 D1-D7） | 结论均指向 SQL 或代码位置 |
| AC-005 | §6（E1-E14 命令/退出码/状态/输出 + `NOT_EXECUTED` 清单 + 静态核实） | 命令可原样复跑 |
| AC-006 | §7（已验证事实 17 / 推断 7 / 未知 7 / 风险 11） | 每条附锚点或核验方式；设计意图未进入事实 |
| AC-007 | §8（EV-01~EV-15 + 14 个待核查问题） | 只给锚点与问题，不推断 T02/T03 范围 |
| AC-008 | 本节 | 见下 |

**变更范围（AC-008）**：本任务只新增 `docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md`，C2 只修改 `.agent/tasks/surgecart-baseline-audit-v1/state.yaml`；未改动 `internal/`、`api/`、`internal/migrations/`、`Dockerfile`、`.cnb.yml`、`prometheus/`、`docs/design/`，未新增 Kafka 相关代码。复核方式：

```bash
git diff --stat <baseline> <C1>        # 只应出现 docs/reports/... 的新增
git diff --stat <C1> <C2>              # 只应出现 state.yaml
git diff --stat <baseline> <C2> -- internal api internal/migrations Dockerfile .cnb.yml prometheus docs/design
# 期望：空输出
```

---

## 10. 限制与未验证项

1. 本报告的所有行号、SQL、分支结论**只对 `7ba5a31077a7b8977473765c2d9a7d402ddadb8c` 成立**；后续任何代码改动都需重新核验。
2. 「已验证事实」限定为：源码原文、真实命令输出、真实数据库 Schema。设计文档/README 的意图性描述只用于 §5.3 的差异对照，不作为事实。
3. 未执行：压测与容量基线、性能结论、生产部署验证、`promtool check rules`、`nginx -t`、`EXPLAIN` —— 均按 `NOT_EXECUTED` 记录，不得读作通过。
4. 运行环境为本地 Docker（MySQL 8.0 / Redis 7-alpine）与 Go 1.24.1（`go.mod` 声明 1.23.0）；与生产版本差异未验证。
5. 测试执行在 MySQL/Redis 容器上进行，创建了容器、命名卷与隔离库 `my_shop_audit`；这些是环境副作用，未进入 Git。
6. 本报告不修复任何缺陷、不调整阈值、不改 Schema、不更新 `docs/design/*`；§7.4 的 11 条风险仅登记，处置由后续任务决定。
