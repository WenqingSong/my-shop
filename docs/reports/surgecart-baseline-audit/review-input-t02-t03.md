# D5 交接材料：T02 / T03 专项审查输入

> 面向对象：T02、T03 的执行者（新 Session 或 Subagent）。
> 本文只提供入口、证据与待查问题，**不下 T02 / T03 的结论**。
> Review Baseline：`95c13910c7226b4e32487ed046702caf75d74fc6`（源码逐字节等价于 `473d6f5253e3abcb98ece9d79a5a8059563a3e8d`）。
> 若 HEAD 已变化：先 `git diff --stat 95c1391..HEAD`，只复核受影响文件；不影响的文件可继续使用本材料。

## 0. 前置说明（必读）

1. **T02 / T03 的范围与验收标准未由 Owner 提供**。本文件按两类「专项审查」组织入口：
   - 线索组 A（对应 T02 可能的范围）：秒杀调用链、请求表职责、事务与一致性、恢复与对账；
   - 线索组 B（对应 T03 可能的范围）：测试有效性、质量门禁、容器化、CI、可观测性。
   若实际范围不同，可直接按 §1 的「证据索引」取用，不必重扫仓库。
2. 本任务的结论有明确分区：`facts-and-risks.md` 的 A（事实）/ B（推断）/ C（未知）/ D（风险）。**不得把 B/C/D 当 A 使用**。
3. 旧交付 `docs/reports/surgecart-baseline-audit-v1/` 属历史基线，**只能对照，不能引用其结论**。
4. 本任务未修改任何业务代码、Schema、Migration、测试断言；差异仅限 `.agent/tasks/surgecart-baseline-audit/` 与 `docs/reports/surgecart-baseline-audit/`。

## 1. 证据索引（按需取用，不必通读）

| 想知道什么 | 直接看 |
| --- | --- |
| 链路的 15 跳与逐跳细节 | `call-chain-map.md` §1、§2 |
| 15 个必答问题 | `call-chain-map.md` §3 |
| 表 / 索引 / 约束 / 事务 / 状态迁移 | `data-layer-map.md` §1-§5 |
| 请求表到底承担什么职责 | `data-layer-map.md` §6 |
| 7 条一致性专项检查结论 | `data-layer-map.md` §7 |
| 测试怎么跑、跑了什么、没跑什么 | `test-baseline.md` §1-§5、§8 |
| 实测 SQL 与事务序列 | `test-baseline.md` §6 |
| Docker / CNB / Prometheus 盘点 | `engineering-inventory.md` §3-§5 |
| 事实 / 推断 / 未知 / 风险 | `facts-and-risks.md` A/B/C/D |

## 2. 线索组 A：链路、请求表与一致性（建议 T02 从此开始）

### 2.1 首读文件（按顺序，约 1200 行）

| 顺序 | 文件 | 必读区段 | 为什么先读 |
| --- | --- | --- | --- |
| 1 | `internal/cmd/routes_frontend.go` | `:33-110` | 路由 + 认证边界 |
| 2 | `internal/controller/flashsale/flashsale.go` | `:14-88` | Handler 只做身份提取，无业务 |
| 3 | `internal/logic/flashsale/flashsale.go` | `:266-342`（CreateOrder）、`:371-425`（幂等/已购自愈）、`:463-542`（落单事务）、`:688-715`（时间窗/行锁） | 同步链路与落单核心 |
| 4 | `internal/logic/flashsale/redis.go` | `:65-141`（Lua）、`:206-220`（runGate）、`:302-347`（权威值收敛）、`:382-483`（对账/结束收敛） | Redis 语义与对账 |
| 5 | `internal/logic/flashsale/request.go` | `:20-92`（状态/入队）、`:117-200`（查询/在途统计） | 请求表读写 |
| 6 | `internal/logic/flashsale/consume.go` | `:36-52`、`:70-226`、`:226-266` | 领取、落单、重试/死信 |
| 7 | `internal/logic/flashsale/repair.go` | `:41-112` | 死信→重排队 + 审计 |
| 8 | `internal/worker/scanner.go` | `:50-102` | 消费者/对账如何被调度 |
| 9 | `internal/cmd/cmd.go` | `:116-165` | 启动、优雅停机、三个扫描器 |

### 2.2 关键函数 → 调用关系（速查）

```text
ControllerV1.CreateOrder (controller/flashsale:22)
└─ sFlashSale.CreateOrder (logic/flashsale/flashsale.go:266)
   ├─ rateLimitUser / rateLimitActivity (ratelimit.go:103/123)         [Redis EVAL]
   ├─ checkQueueCapacity (queue_capacity.go:58)                        [Redis GET]
   ├─ breaker.allow (circuit_breaker.go:86)                            [进程内]
   ├─ runGate (redis.go:206)                                           [Redis EVAL 6 keys]
   ├─ handleIdempotentHit (flashsale.go:371) ─ findRequestByIdempotencyKey (request.go:153)
   ├─ handleAlreadyPurchased (flashsale.go:395) ─ hasPurchaseEvidence (flashsale.go:409)
   └─ enqueue (request.go:61)                                          [INSERT request]

StartFlashSaleConsumeScanner (worker/scanner.go:54)  ← cmd.go:139
└─ ConsumeQueued (consume.go:36)
   └─ consumeOne (consume.go:70)  [tx: SKIP LOCKED → 落单 → 状态]
      ├─ resolveSku (flashsale.go:429)
      ├─ insertOrderInTx (flashsale.go:463)
      │  ├─ loadActivityInTx (:676) / isInTimeWindow (:688)
      │  ├─ loadBindingForUpdate (:702)          [FOR UPDATE]
      │  ├─ INSERT flash_sale_orders (:506)
      │  └─ UPDATE sold=sold+1 WHERE sold<total_stock (:530)
      ├─ successRequestInTx (consume.go:172)
      ├─ failRequestInTx (consume.go:183)
      └─ retryOrDeadRequestInTx (consume.go:195)
   └─ 提交后：convergeStockAndMarkers (redis.go:368) / decrQueueCount (queue_capacity.go:100)
```

### 2.3 相关 SQL 与 Migration（逐条可核）

| 用途 | 位置 |
| --- | --- |
| 建表（活动/绑定/订单） | `internal/migrations/sql/20261001000011_flash_sale.up.sql` |
| 建表（请求/队列） | `internal/migrations/sql/20261001000015_flash_sale_order_requests.up.sql` |
| 建表（修复审计） | `internal/migrations/sql/20261001000018_flash_sale_request_audits.up.sql` |
| 出队（实测） | `test-baseline.md` §6；源码 `consume.go:79-85` |
| 条件扣库存 | `flashsale.go:530-540`；实测日志 `test-baseline.md` §6 `txid:3` |
| 死信/重试更新 | `consume.go:201-220`；实测日志 `txid:1/2` |
| 修复条件更新 | `repair.go:79-94` |
| 在途统计 | `request.go:167-185` |
| 对账扫描 | `redis.go:388-425` |

### 2.4 已有测试与执行方式

```bash
# 依赖（本机新建容器，勿指向生产）
MYSQL_ROOT_PASSWORD=root AUTH_JWT_SECRET=ci-placeholder-jwt-secret-not-for-prod make deps-start

# 秒杀逻辑层（22 用例）
DATABASE_DEFAULT_PASS=root go test -count=1 -v -p 1 ./internal/logic/flashsale/

# 秒杀 E2E（20 用例）
DATABASE_DEFAULT_PASS=root go test -count=1 -v -p 1 -run 'TestFlashSale|TestConcurrent' ./test/e2e/

# 全量（较慢，91s+）
bash scripts/test-integration.sh
```

注意：E2E 会 `DELETE` 秒杀/商品/库存相关表并 `FlushDB` Redis（`test/e2e/flashsale_test.go:41-51`、`identity_isolation_test.go:106-116`）。**不要在有数据的库上运行**。

### 2.5 已发现的风险（原文见 `facts-and-risks.md` D）

| 编号 | 一句话 | 建议审查切入点 |
| --- | --- | --- |
| F1 | 对账固定 `LIMIT 100`，活动多时饥饿 | 造 >100 活动，验证 `ReconcileCache` 覆盖与 `remaining` 收敛 |
| F2 | 扫描周期环境变量不生效（`MustGet`） | 改 env 后观察 ticker 实际周期；确认是否应改 `GetEffective` |
| F3 | 消费吞吐上限 / 绑定行串行化 | 压测单/多实例消费，测量 `queued` 积压与 p99 |
| F4 | 请求表无清理，无界增长 | 造大数据量看索引体积、出队与 `COUNT` 延迟 |
| F8 | 无 DB 级 `CHECK sold<=total_stock`、`flash_order_id` 无约束 | 评估是否需要 DB 级防御 |
| F9 | 幂等键用户维度全局唯一、必须客户端提供 | 复核 API 契约与客户端行为 |
| F11 | 批次遇错即中断 | 注入单条 DB 错误，观察同批后续请求延迟 |
| F12 | 对账与消费并发可能瞬时低估计 | 构造并发交错，观察是否出现误 12003 |

### 2.6 建议优先回答的问题（不预设答案）

1. `flash_sale_order_requests` 在当前业务下是否满足「可靠队列」的实际要求？缺口（租约、可见性超时、清理、死信自动恢复）是否可接受？
2. Redis 预扣与 MySQL 请求落库之间的窗口，在真实故障（进程被 kill）下的最坏后果是什么？现有自愈是否足够？
3. 对账只收敛 Redis、不修复请求，这个边界是否与运维预期一致？
4. 秒杀配额与普通库存（`inventories`）完全隔离，运营上如何保证 INV-009 的「先划拨」？
5. 消费侧串行 + 行锁的吞吐是否满足目标 QPS（需压测数据，不能只读代码）？

## 3. 线索组 B：测试、门禁、容器、CI、可观测性（建议 T03 从此开始）

### 3.1 首读文件

| 顺序 | 文件 | 必读区段 |
| --- | --- | --- |
| 1 | `scripts/test.sh` | 全文（门禁编排） |
| 2 | `scripts/test-unit.sh` | 全文（纯内存用例的硬编码清单） |
| 3 | `scripts/test-integration.sh` | 全文（`-p 1` 原因） |
| 4 | `scripts/test-race.sh` | 全文（只跑熔断器） |
| 5 | `scripts/test-prometheus.sh` / `test-container.sh` / `test-nginx.sh` | 校验性质 |
| 6 | `.cnb.yml` | `:25-123` |
| 7 | `Dockerfile` / `docker-compose.yml` / `scripts/docker-entrypoint.sh` | 构建与启动 |
| 8 | `internal/metrics/metrics.go` / `prometheus/prometheus.yml` / `prometheus/rules/flashsale_latency.yml` | 指标与规则 |
| 9 | `test/e2e/identity_isolation_test.go:93-133` | E2E 如何起真实服务/迁移/清库 |

### 3.2 已知工程事实（可直接引用，不必重跑）

- 61 个测试文件；单元门禁 11 个包/组合；集成 `go test -p 1 ./...`；race 仅熔断器。
- 本次所有门禁退出码 0（`test-baseline.md` §1）。
- Docker：多阶段、非 root、entrypoint 先 migrate 后 serve、compose 5 服务 + 健康检查；**镜像未构建、完整栈未启动**。
- CNB：PR 跑 test/race，push(main) 追加 build/docker-build/docker-push/镜像同步；**无部署步骤，未实跑**。
- Prometheus：2 个秒杀指标 + p95/p99 规则 + `app:8000` 抓取；**无 HTTP/Redis/MySQL 指标，业务指标不完整**。
- 秒杀四个 V5 开关默认全关（限流/排队/熔断/指标）。

### 3.3 已发现的风险（工程侧）

| 编号 | 一句话 | 建议审查切入点 |
| --- | --- | --- |
| F2 | env 覆盖失效（含扫描周期） | 检查其他配置读取是否同样误用 `MustGet` |
| F3 | 无消费吞吐测试 | 设计压测，不是改代码 |
| F5 | 指标覆盖不足 | 评估是否需要队列深度/死信/DB/Redis 指标 |
| F6 | `/metrics` 无鉴权 | 评估网络边界与是否需要鉴权 |
| F7 | 设计文档与实现不一致 | 修正文档（属文档任务，不在 T01） |
| F10 | 容量保护默认关闭 | 核对部署环境是否显式开启 |

### 3.4 建议优先回答的问题

1. `make test` 是否覆盖了 CI 真正执行的检查？本地 `make test` 与 `.cnb.yml` 是否有漂移？
2. race 门禁只覆盖熔断器，是否足以支撑「并发正确性」声明？
3. E2E 的故障注入（`WRONGTYPE` + 进程内 hook）能否代表真实故障？缺口如何补？
4. 指标能否支撑秒杀值班（积压、死信、售罄、DB/Redis 健康）？缺什么？
5. Docker/Compose/CNB 的「已配置但未验证运行」项，哪些必须在交付前实跑？

## 4. 未解决事项（T02/T03 需要接手）

| # | 事项 | 现状 |
| --- | --- | --- |
| U1 | 独立审查（Cleaner） | 本任务单 Agent 执行，报告作者即 LEAD，**独立审查未满足**；需 Owner 指派独立审查者 |
| U2 | 真实故障注入与多实例验证 | 未执行（D3 §8、`facts-and-risks.md` C2/C3/C4） |
| U3 | 生产规模执行计划 | 未验证（C1） |
| U4 | 压测与容量结论 | 未执行；仓库有 `scripts/flashsale-loadtest/` 但本任务未运行 |
| U5 | CNB 流水线实际状态 | 未验证（C6） |
| U6 | 部署环境指标是否抓取 | 未验证（C7） |
| U7 | T02 / T03 的范围与验收标准 | 未提供；本文件按两组线索组织，未替 Owner 定义任务 |

## 5. 一句话交接

链路、表结构、事务边界、消费者领取与状态迁移已有可复核的源码行号 + 实测 SQL/事务日志；**未验证**的是多实例/真实故障/规模性能/CI 实跑。T02 应从 `flashsale.go:266-542` + `consume.go:70-226` + `redis.go:382-483` 入手；T03 应从 `scripts/test*.sh` + `.cnb.yml` + `internal/metrics/metrics.go` + `docker-compose.yml` 入手。
