# Technical Contract

## Decision Status

WAITING_FOR_OWNER_DECISION

## Problem

秒杀 V5 需在 V1-V4 已证明的「异步下单 + 故障恢复」正确性基础上，补齐容量保护（用户级限流、活动级限流、排队长度限制、熔断降级）与可观测性（指标、热点 Key 分析），并用「正常库存 / 少库存 / 瞬时洪峰」三级压测验证五个业务不变量在压力下依然成立、保存优化前后对比基线。

当前链路在 Redis 闸门之前没有任何限流 / 熔断 / 指标；`flash_sale_order_requests(status=queued)` 无容量上限、积压无界；Redis 故障为 fail-closed（503）但无熔断短路。需要固化限流算法、熔断降级语义、排队容量、指标方案、热点分析形态与压测基线存储，再交 Coder 实现。

## Verified Current Behavior

- VERIFIED：秒杀下单入口 `POST /flash-sales/:id/orders`（`internal/cmd/routes_frontend.go`）仅受 `middleware.Auth` 保护；中间件目录仅 `auth.go`/`principal.go`/`response.go`，无任何限流 / 熔断 / 降级 / 指标中间件。
- VERIFIED：`CreateOrder`（`internal/logic/flashsale/flashsale.go`）先 `runGate`（Redis Lua）再 `enqueue`；Redis 不可用 → `CodeServiceUnavailable(1005)` 503 fail-closed，不落 request、不预扣、不同步落单（`docs/design/flash-sale.md` §5.4）。
- VERIFIED：`flash_sale_order_requests`（`status=queued`）无队列容量上限；消费出队 `FOR UPDATE SKIP LOCKED` 逐条单事务；`findInflightQueued`（`internal/logic/flashsale/request.go`）已按 activity×SKU 统计 queued 数，供对账 `remaining = total_stock - sold - inflight_queued` 使用。
- VERIFIED：`go.mod` 无 Prometheus / OpenTelemetry 直接依赖（`otel*` 为 go-redis 的 indirect）；配置 `flash_sale` 段仅 `reconcile_scan_interval`/`consume_scan_interval`（`manifest/config/config.yaml`）。
- VERIFIED：秒杀错误码域 12000-12999 已用 12001-12008；通用域 1000-1999 已用 1000-1005。migration 最新 `20261001000018`（`.agent/registry/migrations.md`）。
- VERIFIED：五个业务不变量 INV-001~005 及 V3/V4 的 INV-012~017 已沉淀于 `docs/design/flash-sale.md`，MySQL 始终为权威事实来源。
- UNKNOWN：实际生产多实例部署规模（当前为单实例 Redis + 单进程）；压测工具（vegeta/k6/wrk）是否已在验证环境可用，需在 Deliverer 阶段确认。

## Recommendation

RECOMMENDATION（聚焦秒杀链路、复用 V4 权威值收敛范式、控制 Scope，共 6 项）：

1. 限流算法与存储：Redis 固定窗口计数器（`INCR`+`EXPIRE`，单次往返），用户级 key `flashsale:rl:user:{userID}`、活动级 key `flashsale:rl:activity:{activityID}`，窗口与阈值取配置；多实例经 Redis 天然全局一致；Redis 故障 fail-open（放行到闸门，由闸门 fail-closed 兜底，避免双失败路径）。检查位于 `CreateOrder` 入口、`runGate` 之前，秒杀专用，不抽象为全站通用中间件。

2. 熔断降级语义（关键决策）：熔断对象 = Redis 闸门；进程内状态机 Closed→Open→Half-Open（连续失败阈值 + 时间窗 + 半开探测）；降级行为 = **快速失败**（熔断 Open 直接 503/1005，不预扣、不建单、不排队），保持 V3 fail-closed 语义、仅缩短失败路径；**不降级到同步 MySQL 路径**（与 V3 已固化语义冲突，且把流量打回 MySQL 违背容量保护目标）。

3. 排队长度限制：维度 = activity×SKU（与库存粒度、`findInflightQueued` 一致）；超限 = 快速拒绝（429/12010，不等待）；Redis 计数器 `flashsale:queued:{activity}:{sku}` 快速近似 + MySQL `COUNT(status=queued)` 权威收敛（复用对账扫描器）。不改变 `flash_sale_order_requests` 现有状态机与消费语义。

4. 指标方案：引入 `prometheus/client_golang`，暴露 `GET /metrics`（Prometheus 文本格式），Counter + Histogram 记录 QPS / 成功率 / 拒绝率 / p95 / p99，标签维度：接口 / 活动 / 结果（success / queued / gate_rejected / rate_limited / queue_full / error），覆盖下单接口与排队消费链路。

5. 热点 Key 分析（关键决策）：**可观测分析**——用 Redis 命令/脚本（`redis-cli --hotkeys` / `OBJECT FREQ`）识别秒杀热点 Key（`flashsale:stock:*`、`flashsale:activity:*`）并输出分析结果；**不做热点防护**（打散 / 本地缓存），因 Out of Scope 排除多级缓存/CDN、本地缓存破坏多实例一致性。

6. 压测与基线：自研压测脚本（`scripts/flashsale-loadtest/`，可重复执行），覆盖正常库存 / 少库存 / 瞬时洪峰三级；基线存文件（JSON，`scripts/flashsale-loadtest/baselines/`），**不新增 DB 表**（避免 migration 与后台查询接口）；优化前后对比 = 调整限流 / 熔断开关或参数后重跑、对比两次 JSON。

关键取舍：以「最小 Scope + 复用 V4 权威值收敛范式」为原则——容量保护聚焦秒杀链路、可观测性用成熟 Prometheus 依赖换取 p95/p99 直方图正确性、基线用文件避免 migration。代价是引入 1 个标准依赖（Prometheus client）与 1 套自研压测脚本；风险是固定窗口限流的边界毛刺（对容量保护可接受）。

## Selected Design

等待 Owner 确认。

## Interfaces and Data

- 新增错误码（秒杀域 12000-12999 内派生，复用既有域，无新域预留）：
  - 12009 `FLASH_SALE_RATE_LIMITED` → 429（用户级 / 活动级限流拒绝，无副作用）
  - 12010 `FLASH_SALE_QUEUE_FULL` → 429（排队容量满拒绝，无副作用）
- 新增 Redis Key（B 类 namespace，`flashsale:` 前缀）：
  - `flashsale:rl:user:{userID}`（用户级限流计数，窗口 TTL）
  - `flashsale:rl:activity:{activityID}`（活动级限流计数，窗口 TTL）
  - `flashsale:queued:{activityID}:{skuID}`（排队长度近似计数，活动 TTL）
- 新增配置（`manifest/config/config.yaml` `flash_sale` 段）：用户级 / 活动级限流窗口与阈值、排队容量上限、熔断阈值 / 时间窗 / 半开参数、指标开关。
- 新增路由 `GET /metrics`（Prometheus 采集端点，见 Open Risks 暴露面）。
- 不改动现有公开接口与响应格式；`CreateOrder` 仅新增 12009/12010 两条「无副作用快速拒绝」路径，`POST /flash-sales/:id/orders` 成功语义不变（入队受理 `status=queued`）。
- 无需新增 migration（基线文件存储，限流 / 熔断 / 指标均无新表）。

## Business Invariants

- INV-018（限流无副作用）：被用户级 / 活动级限流拒绝的请求，不进入 Redis 闸门、不预扣库存、不写入 `flash_sale_order_requests`，返回 12009；未超限用户不受影响。
- INV-019（排队有界）：任意时刻某活动×SKU 下 `status=queued` 请求数 ≤ 配置的队列容量上限；超限请求被拒绝（12010）不新增 queued；消费释放容量后后续请求可继续入队。
- INV-020（熔断降级不破坏不变量）：熔断 Open 期间秒杀下单快速失败（不预扣、不建单、不排队），不产生超卖、不产生重复订单；Redis 恢复后经半开探测自动关闭、恢复正常。
- INV-021（指标可对账）：指标中成功 / 拒绝 / 失败计数与真实请求结果一致（可与 MySQL 订单数、`status=queued` 数、拒绝数对账）。

## Failure and Consistency Semantics

- 事实来源：MySQL 仍是权威（订单 `flash_sale_orders`、库存 `flash_sale_activity_skus`、请求状态 `flash_sale_order_requests`）；Redis 是闸门 + 限流 / 排队近似计数（派生，非权威）；指标为进程内观测计数（非业务事实）。
- 限流计数 Redis 故障 → fail-open（放行到闸门），闸门 Redis 亦故障则 fail-closed 503——净效果仍安全（不预扣、不建单、不排队）。
- 熔断状态为进程内（多实例各自独立，不共享）；熔断 Open 快速失败，半开探测用真实请求（或探测请求）验证 Redis 恢复后关闭。
- 排队计数（Redis 计数器）在「计数已增、request 未落库」崩溃窗口可能漂移，由对账扫描器以 MySQL `COUNT(status=queued)` 权威收敛（复用 V4 收敛周期，≤1 周期）。
- 成功响应含义不变：入队成功仍代表「已受理排队」，不代表下单成功；下单成功仍发生在消费事务内。限流 / 队列满拒绝为「无副作用快速拒绝」，不产生任何写入。

## Allowed / Forbidden Changes

- 允许：在秒杀链路内新增限流 / 排队 / 熔断 / 指标逻辑与配置；新增错误码 12009/12010；新增 Redis 计数 Key；新增 `GET /metrics`；新增压测脚本与基线文件；更新 `docs/design/flash-sale.md`（Design Impact = UPDATE）。
- 禁止：改变五个业务不变量 INV-001~005 及 V3/V4 已固化的异步语义、`flash_sale_order_requests` 状态机与消费出队语义；把限流 / 熔断 / 指标抽象为全站通用中间件平台（Design Impact 保持 UPDATE，不升级 NEW）；新增 DB 表 / migration（基线用文件）；降级到同步 MySQL 路径；把 Redis fail-closed 改为绕过闸门；改动普通订单 / 库存 / SKU / 商品 / IAM 模块行为。

## Verification Requirements

- INV-018 → 并发下单：同用户高频请求，断言超阈值请求返回 12009（429）且 `flash_sale_order_requests` 无新增、Redis `remaining` 不变、无预扣；低频用户正常排队。
- INV-019 → 排队：构造 `queued` 达上限后继续下单，断言新请求返回 12010、`queued` 计数不超上限；消费后释放容量、后续可入队。
- INV-020 → 熔断：注入 Redis 故障 / 超时，断言熔断触发并快速失败（503）；恢复 Redis 后自动关闭；期间 `sold` / 订单数不违例（无超卖、无重复）。
- INV-021 → 指标：压测期间读 `/metrics`，断言 QPS / 成功率 / 拒绝率 / p95 / p99 与 MySQL 订单数、queued / 拒绝数对账一致。
- 三级压测（正常库存 / 少库存 / 瞬时洪峰）输出吞吐 / 时延（p95/p99）/ 成功与拒绝分布；压测后核对 `sold ≤ total_stock`、成功订单数 = `sold`、无一人一单 / 幂等违例、`queued ≤ 上限`、MySQL 连接数不超阈值。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；并发与不变量用 `go test -race` + 真实 MySQL / Redis 集成验证（`docker compose up -d` 环境）。

## Open Risks

- 指标端点 `GET /metrics` 的暴露面：需确认仅内网 / 监控网段可达，避免泄漏流量与业务维度信息。
- 固定窗口限流存在窗口边界毛刺（瞬时双倍流量），对容量保护可接受；若需精确可后续升级滑动窗口（本任务不阻塞）。
- 多实例部署下熔断状态不共享（各实例独立熔断），Redis 故障时各实例独立快速失败，符合 fail-closed 目标；但半开探测可能放大恢复期流量，需在压测中观察。
- 排队 Redis 计数器与 MySQL 权威值的收敛存在 ≤1 对账周期窗口，该窗口内可能瞬时偏紧（多拒）或偏松（瞬时超限），由 MySQL 唯一约束兜底不超卖。

## Owner Decision Record

等待 Owner 确认。
