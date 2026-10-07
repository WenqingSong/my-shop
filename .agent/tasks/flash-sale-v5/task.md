# Task: 秒杀（Flash Sale）V5（容量保护与可观测性）

## Goal

在秒杀 V1-V4 已证明「异步下单 + 故障恢复」正确性的基础上，补齐秒杀下单链路的**容量保护**与**可观测性**，并用**逐级压测**验证其真实效果、保存基线：在瞬时洪峰、少库存等压力下，系统能通过「用户级限流 + 活动级限流 + 排队长度限制 + 熔断降级」在 Redis 闸门/MySQL 之前快速拒绝过量流量，保护数据库与队列不被击穿；同时通过「指标（QPS/成功率/拒绝率/p95/p99）与热点 Key 分析」让容量表现可观测；最后用「正常库存/少库存/瞬时洪峰」三级压测证明五个业务不变量（库存不为负、成功订单数 ≤ 初始库存、一人一单、失败不建单、幂等防重复扣减）在压力下依然成立，并检查无超卖、无重复订单、消息积压有界、数据库连接数不超阈值，保存优化前后对比基线。

## Scope

- 用户级限流：对秒杀下单（及必要的秒杀读接口）按用户维度限流，超阈值请求被快速拒绝（稳定错误码 + 无副作用），不进入 Redis 闸门、不预扣库存、不产生排队请求。
- 活动级限流：对单个秒杀活动在单位时间内的总请求量限流，超阈值部分被快速拒绝，保护闸门与数据库不被单活动洪峰击穿。
- 排队长度限制：为 `flash_sale_order_requests`（`status=queued`）设置队列容量上界，达到上限时新请求被快速拒绝，保证消息/排队积压有界，不无限堆积。
- 熔断与降级：对秒杀依赖的关键组件（至少 Redis 闸门）建立熔断，故障/超时达到阈值后进入明确可定义的降级行为，并在恢复后自动关闭；降级期间不破坏五个业务不变量。
- 指标：暴露 QPS、成功率、拒绝率、p95/p99 等指标（按接口/活动等必要维度），可被采集观测。
- 热点 Key 分析：能够识别/分析秒杀 Redis 热点 Key（如 `flashsale:stock:{activity}:{sku}`），并输出可用的分析结果。
- 逐级压测：提供可重复执行的压测脚本/工具，覆盖「正常库存、少库存、瞬时洪峰」三级场景。
- 正确性检查：压测后核对无超卖、无重复订单、消息积压有界、数据库连接数不超阈值。
- 基线与对比：保存压测基线，支持优化前后（如限流/熔断开关、参数调整）对比。
- 长期设计：更新 `docs/design/flash-sale.md`（Design Impact = UPDATE），沉淀容量保护、熔断降级与可观测性模型。

## Out of Scope

- 秒杀五个业务不变量及其 MySQL 事实来源兜底（V1-V4 已证明，本任务不改其语义）。
- 引入外部 MQ、Redis 集群/哨兵/多级缓存/CDN（现有「MySQL 出队表 + 单实例 Redis」不改变）。
- 秒杀配额从普通库存的自动划拨、取消/退款/超时未支付与库存恢复（V1 已明确不做）。
- 风控/防机器人/验证码/黑名单。
- 前端页面改造（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。
- 修改普通订单/库存/SKU/商品/IAM 模块行为（除秒杀所需最小只读引用）。
- 全站通用限流/熔断/指标平台化：本任务默认聚焦秒杀链路；是否抽象为跨模块通用中间件由 Analyst 在 Contract 中判断，若判断为通用基础设施需在 Scope/Design 中显式确认（见 Analyst Questions）。

## Milestone

Milestone: 秒杀 V5 容量保护与压测闭环

## Design Impact

Design Impact: UPDATE
Design Artifact: docs/design/flash-sale.md

## Acceptance Criteria

- [ ] AC-001（用户级限流）：给定单位时间窗口与阈值，当同一用户对秒杀下单的请求速率超过阈值时，后续请求被快速拒绝（稳定业务错误码，建议 429 语义），且不进入 Redis 闸门、不预扣库存、不写入 `flash_sale_order_requests`；未超阈值用户不受影响。
- [ ] AC-002（活动级限流）：给定单位时间窗口与阈值，当某秒杀活动的总请求速率超过阈值时，超出部分被快速拒绝，保护闸门与数据库不被单活动洪峰击穿；不同活动之间互不影响。
- [ ] AC-003（排队长度限制）：当某活动（或活动×SKU）`status=queued` 请求数达到上限时，新入队请求被快速拒绝（稳定错误码 + 无副作用），队列积压有界、不无限增长；已有排队请求继续被正常消费。
- [ ] AC-004（熔断与降级）：当 Redis 闸门故障/超时达到阈值时触发熔断，进入可定义、可观测的降级行为（如快速失败/限流放行，由 Contract 固化），降级期间不产生超卖、不产生重复订单；组件恢复后熔断自动关闭，服务恢复正常处理。
- [ ] AC-005（指标）：系统暴露 QPS、成功率、拒绝率、p95/p99 等指标（至少覆盖秒杀下单接口与排队消费链路，可按接口/活动维度区分），能够被采集读取；指标数值与真实请求结果一致（成功/拒绝/失败可对账）。
- [ ] AC-006（热点 Key 分析）：能够识别秒杀 Redis 热点 Key（如库存 Key `flashsale:stock:{activity}:{sku}`、活动元数据 Key），输出可用的分析结果（工具/日志/接口均可，形态由 Contract 固化）。
- [ ] AC-007（逐级压测）：提供可重复执行的压测脚本/工具，覆盖「正常库存、少库存、瞬时洪峰」三级场景，并输出每级场景的吞吐、时延（含 p95/p99）、成功/拒绝/失败分布。
- [ ] AC-008（正确性检查）：压测后核对证明——无超卖（`sold ≤ total_stock` 且成功订单数 = `sold`）、无重复订单（一人一单/幂等唯一约束不违例）、消息/排队积压有界（`status=queued` 数量不超过上限）、数据库连接数不超阈值（无连接耗尽）。
- [ ] AC-009（基线与对比）：能够保存压测基线数据，并支持优化前后（如限流/熔断开关、阈值/容量参数调整前后）对比输出；基线数据可查询、可复现。
- [ ] AC-010（长期设计）：更新 `docs/design/flash-sale.md`，沉淀容量保护（限流/排队/熔断降级）、可观测性（指标/热点分析）与压测基线模型，与 APPROVED Contract、最终实现一致。

## Relevant Context

已核实事实：

- V1-V4 已实现并落地 `docs/design/flash-sale.md`。V3 异步下单（Redis Lua 闸门 + MySQL 出队表 `flash_sale_order_requests` + `goroutine+ticker` 消费者，`startFlashSaleConsumeScanner` 默认 1s 周期）；V4 故障恢复（权威值收敛补偿 + 结束收敛 + 人工修复审计）。**无外部 MQ**，本任务不引入。
- 秒杀下单入口 `POST /flash-sales/:id/orders`（`routes_frontend.go`）仅受 `middleware.Auth` 保护，用户身份取自 `Principal.UserID`；结果查询 `GET /flash-sales/:id/orders/result`。中间件目录当前仅 `auth.go`/`principal.go`/`response.go`，**无任何限流/熔断/降级/指标中间件**。
- **当前无任何限流、熔断、降级、指标基础设施**：`go.mod` 无 Prometheus client（仅 `go.opentelemetry.io/otel*` 为 `go-redis` 的 indirect 依赖）；配置 `manifest/config/config.yaml` 的 `flash_sale` 段仅 `reconcile_scan_interval`/`consume_scan_interval`。
- Redis 故障当前为 **fail-closed**（`CodeServiceUnavailable=1005` → 503），无降级路径；`docs/design/flash-sale.md` §5.4/§10 明确「Redis outage fallback（容量保护/降级）应作为独立容量保护/降级设计处理」。
- 异步队列 = MySQL `flash_sale_order_requests`（`status=queued`），**当前无队列长度上限**，积压无界；消费者出队 `FOR UPDATE SKIP LOCKED` 逐条单事务落单。
- 错误码：通用域 1000-1999 已用 1000-1005；秒杀域 12000-12999（flash-sale-v1 在 `.agent/registry/error-codes.md` 为 RESERVED）已用 12001-12008。migration 最新 `20261001000018`（flash-sale-v4 RESERVED）。
- 权限模型已有 RBAC（`admins`/`roles`/`permissions` + `RequirePermission` + `internal/boot/seed.go` 权限 seed），秒杀权限 `flash_sale:create`/`update`/`repair`；无独立「限流/熔断配置管理」权限。

Assumption（合理但未经 Owner 确认，交 Analyst 核实）：

- 限流/熔断/指标默认聚焦**秒杀链路**，不扩展为全站通用平台；若 Analyst 判断应做成通用中间件，需在 Contract 中显式界定边界并评估 Design Impact 是否升级为 NEW（新增通用可观测性/容量保护设计文档）。
- 压测工具采用自研脚本（`scripts/` 下）或引入成熟工具（如 vegeta/k6/wrk），由 Analyst 比较后固化；压测执行与基线对比属 Deliverer 里程碑，压测脚本/工具本身属 Coder 实现。
- 限流计数可能基于 Redis 或进程内（多实例语义不同），熔断状态基于进程内 + 阈值；具体选型由 Analyst 决定，不改变五个业务不变量。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 「热点 Key 分析」是「可观测分析（识别并输出热点）」还是「热点防护（打散/本地缓存）」——两者业务与实现结果不同。
- 「熔断与降级」的降级行为语义：是「快速失败（保持 fail-closed 但熔断缩短失败路径）」还是「降级到某种放行/同步路径」，会直接改变失败语义，需 Owner 决策。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis（`docker compose up -d`）；限流/熔断/指标/排队长度断言需真实并发压测（如自研脚本或 vegeta/k6）与 `go test -race`；正确性检查需真实 MySQL 数据核对（非只看 HTTP 200）。

- AC-001 → 并发下单：同用户高频请求，断言超阈值请求返回稳定限流错误码，且 `flash_sale_order_requests` 无新增、Redis `remaining` 不变、无预扣；低频用户正常排队。
- AC-002 → 并发下单：构造单活动洪峰，断言超阈值部分被快速拒绝、不穿透闸门；另一活动不受影响。
- AC-003 → 排队：构造 `queued` 达到上限后继续下单，断言新请求被拒绝、`queued` 计数不超上限；消费后释放容量、后续请求可继续入队。
- AC-004 → 熔断：注入 Redis 故障/超时（如关闭 Redis 或注入超时），断言熔断触发并进入 Contract 固化的降级行为；恢复 Redis 后熔断自动关闭、服务恢复；期间核对 `sold`/订单数不违例（无超卖、无重复）。
- AC-005 → 指标：压测期间读取指标端点/接口，断言 QPS/成功率/拒绝率/p95/p99 与真实请求结果一致（可与 MySQL 订单数、`queued`/拒绝数对账）。
- AC-006 → 热点：压测期间运行热点 Key 分析，断言能识别库存/活动热点 Key 并输出结果。
- AC-007 → 压测：分别以「正常库存、少库存、瞬时洪峰」三级场景运行压测脚本，输出吞吐/时延/成功与拒绝分布，且脚本可重复执行、结果可复现。
- AC-008 → 数据核对：压测后断言 `sold ≤ total_stock`、成功订单数 = `sold`、无一人一单/幂等唯一约束违例、`status=queued` 不超过上限、MySQL 连接数不超阈值。
- AC-009 → 基线：保存基线数据，调整限流/熔断开关或参数后再压测，断言可输出前后对比；基线数据可查询、可复现。
- AC-010 → 文档审查：`docs/design/flash-sale.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis/压测的集成验证需说明容器与压测工具就绪。

## Complexity

COMPLEX

原因：涉及限流算法（令牌桶/滑动窗口/固定窗口，进程内 vs Redis，多实例一致性）、熔断与降级策略（降级行为语义直接改变失败结果）、排队容量语义（是否改变既有 `flash_sale_order_requests` 生命周期）、指标方案（Prometheus/OpenTelemetry/自建端点）与压测基线的数据模型等多个现实方案会产生不同业务、可靠性与运维结果；需修改既有秒杀一致性模型（Redis fail-closed 语义）与新增错误码/可能新增 migration，必须由 Analyst 固化 Contract 后交 Owner 确认。

## Analyst Questions

1. 限流算法与维度：用户级/活动级限流采用何种算法（固定窗口/滑动窗口/令牌桶）与存储（进程内 vs Redis）；多实例部署下限流语义（全局一致 vs 单实例近似）如何界定；限流计数的 Redis Key 命名与 TTL（B 类 namespace，是否复用 `flashsale:` 前缀）。
2. 熔断与降级语义（关键）：熔断对象（Redis 闸门？MySQL？），熔断阈值/时间窗/半开恢复策略；降级行为是「快速失败（缩短 fail-closed 路径）」还是「降级到同步/放行路径」——会改变失败语义与「失败不建单」边界，需 Owner 决策。
3. 排队长度限制：队列容量上界按什么维度（活动 vs 活动×SKU）、超限响应（拒绝 vs 等待）、超限错误码与是否计入指标；是否改变 `flash_sale_order_requests` 现有状态机/消费语义。
4. 指标方案：Prometheus 端点 / OpenTelemetry / 自建 metrics 接口，指标维度（接口/活动/用户/状态），p95/p99 直方图桶；是否引入新依赖及其成本。
5. 热点 Key 分析：是「可观测分析（识别并输出热点）」还是「热点防护（打散/本地缓存）」；输出形态（工具/日志/接口）；与现有 `flashsale:` Key 结构的关系。
6. 压测与基线：压测工具选型（自研 vs 引入），基线数据存储（文件 vs DB 表，若 DB 则需 migration），「优化前后对比」的「优化」边界（限流/熔断开关、阈值/容量参数）。
7. 全局资源：限流拒绝/熔断/队列满等新错误码的域归属（秒杀域 12000-12999 内派生 vs 通用域 1000-1999 新增）；压测基线/限流配置若需持久化则新增 migration；具体编号/版本由 Analyst 读 `.agent/registry/*` 派生并写入 Contract，Coder 不得自行推断。

## Review Baseline

- Base commit：`d05fc74611dd490015bb286feb59771983842089`（分支 `feat/flash-sale-v5`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空；local == `origin/feat/flash-sale-v5`）。
- 重叠修改的区分方式：本任务新增/修改产物为 `.agent/tasks/flash-sale-v5/`、`internal/middleware`（限流/熔断中间件，如需）、`internal/logic/flashsale`（排队容量/熔断降级/热点分析）、`internal/cmd`（指标端点/扫描器扩展，如需）、`internal/codes`（新错误码，如需）、`internal/boot`（权限 seed，如需）、`api/flashsale/v1` 或新 `api` 模块（指标/热点查询，如需）、`manifest/config/config.yaml`（限流/熔断/指标配置）、migration（如需）、`scripts/`（压测脚本/工具）及对应测试；`docs/design/flash-sale.md` 由 Analyst 更新。当前工作区干净，无既有未提交修改。

## Initial Route

交 Analyst（COMPLEX）
