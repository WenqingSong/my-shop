# Cleaner Findings

## Review Target

- 任务：`flash-sale-v5`（秒杀 V5 容量保护与可观测性）
- 任务基线（Base）：`d05fc74611dd490015bb286feb59771983842089`（分支 `feat/flash-sale-v5`，任务开始时 working tree clean）
- APPROVED Contract target（A1）：`0e8089f189e275df8407d91e793996cfe0789147`
- **Implementation Evidence Commit（C1，本次审查对象）**：`35498b0340b7e8688f18e47dd3e64c11fdb9b3a7`（`feat(flash-sale-v5): 秒杀容量保护与可观测性（限流/排队软上限/熔断/指标）`）
- 审查范围：`0e8089f..35498b0`（Coder 生产代码 + 测试，共 20 文件，+1420/-9）；`docs/design/flash-sale.md`（Analyst 在 A1 更新，Cleaner 校验一致性）
- 任务前已有修改区分：任务基线处 working tree 干净，V5 相关提交链为 `36b879b`(task) → `0e8089f`(contract+design, A1) → `e685cab`(contract 绑定, A2) → `35498b0`(实现, C1) → `97b4d18`(review 元数据, C2)。审查只针对 C1 相对 A2(`e685cab`) 的实现增量。
- 关键配置：`flash_sale` 段新增 `rate_limit` / `queue_capacity` / `circuit_breaker` / `metrics`（均默认 `enabled: false`）；无新增 migration；错误码 12009/12010 复用秒杀域 12000-12999。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 用户级限流 | PASS | `TestRateLimitUserRejects`（logic）+ `TestFlashSaleV5RateLimitHTTP`（HTTP 链）。同用户超阈值返回 12009/429，且 `remaining` 不变、`flash_sale_order_requests` 无新增。真实 MySQL/Redis 验证。 |
| AC-002 活动级限流 | PASS | `TestRateLimitActivityRejects`。单活动超阈值返回 12009，不同活动计数独立（`flashsale:rl:activity:{id}`）。 |
| AC-003 排队长度限制 | PASS | `TestQueueCapacityRejects`。queued 达软上限返回 12010，计数不超上限；消费后 DECR 释放、后续可入队。 |
| AC-004 熔断降级 | PASS | `TestCircuitBreakerOpensAndRecovers`。注入真实 Redis 闸门故障（活动 key 改 string 使 Lua WRONGTYPE）→ 连续失败达阈值 Open → 快速失败 1005 且无预扣/无 request → 超时半开探测 → 恢复自动 Close。 |
| AC-005 指标 | PASS | `TestMetricsReconcile`（logic）+ `TestFlashSaleV5MetricsEndpoint`（HTTP）。Counter 对账 order/queued、consume/success 与真实结果一致；`GET /metrics` 返回 Prometheus 文本（含 `flashsale_requests_total`、`result="queued"`）。 |
| AC-006 热点 Key 分析 | PASS | `scripts/flashsale-loadtest/hotkeys.sh`（`redis-cli --hotkeys` + `OBJECT FREQ` 兜底）。形态与 Contract 一致（仅可观测分析）。注意：实际识别需 Redis LFU 策略，见 CLEAN-002。 |
| AC-007 逐级压测 | PASS | `scripts/flashsale-loadtest/run.sh` 覆盖 normal/low_stock/flood 三级，输出吞吐/时延(p50/p95/p99)/成功与拒绝分布，可重复执行。实际压测执行属 Deliverer 里程碑。 |
| AC-008 正确性检查 | PASS | `run.sh` 的 `verify()` 核对 `sold ≤ total_stock`、成功订单数 = `sold`、无一人一单/幂等违例。`queued 有界/连接数阈值`仅打印未自动判定，见 CLEAN-001。 |
| AC-009 基线与对比 | PASS | `run.sh` 保存 JSON 基线到 `baselines/`；`compare.sh` 输出前后差异。 |
| AC-010 长期设计 | PASS | `docs/design/flash-sale.md` §8/§11 与 APPROVED Contract、最终实现一致（四者一致，详见 Verification）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `gofmt -l`（改动 Go 文件） | PASS | 无输出，格式干净。 |
| `go build ./...` | PASS | exit 0。 |
| `go vet ./...` | PASS | 无输出。 |
| `go test -p 1 ./...` | PASS | 全部 package ok（含 `internal/logic/flashsale`、`internal/cmd`、`internal/migrations`）。 |
| `go test -race -run 'TestRateLimit\|TestQueueCapacity\|TestCircuitBreaker\|TestMetrics' ./internal/logic/flashsale/...` | PASS | 无数据竞争。 |
| `go test -run 'TestFlashSaleV5' ./internal/cmd/...` | PASS | HTTP 链 429/12009 与 /metrics 端点通过。 |
| `bash -n`（3 个压测脚本） | PASS | 语法通过，脚本可执行位正确。 |
| `scripts/check-registry.sh` | PASS | 12009/12010 均在 RESERVED 秒杀域 12000-12999 内，无重复/漂移。 |
| 三边一致性（Registry↔Contract↔实现） | PASS | 无新增错误码域（复用 12000-12999）、无新增 migration（`migrations.md` 最新仍 20261001000018，`state.resources.reservations` 为空，与 Contract「无需新增全局资源」一致）。 |
| 四者一致（Task↔Contract↔Design↔实现） | PASS | `docs/design/flash-sale.md` §11（限流/排队软上限/熔断/指标/热点/压测基线/配置）与 §8（12009/12010）与 Contract「Selected Design」及 `ratelimit.go`/`queue_capacity.go`/`circuit_breaker.go`/`metrics.go`/`codes.go` 逐项对应。 |

## Findings

### CLEAN-001：压测脚本 verify() 未将「queued 有界 / 连接数阈值」纳入自动判定

- Severity：P3
- Status：OPEN
- Location：`scripts/flashsale-loadtest/run.sh` `verify()`（L154-180）
- AC / Invariant：AC-008（压测后核对消息/排队积压有界、数据库连接数不超阈值）
- Trigger：压测后运行 `verify()`，当 `queued` 稳态超上限或 `Max_used_connections` 超阈值时。
- Actual：脚本打印 `QUEUED` 与 `MAX_CONNS` 数值，但 `verdict` 仅由 `sold ≤ total_stock`、`orders == sold`、无重复订单三项决定；积压有界与连接数阈值不被自动断言。
- Expected：`verdict` 纳入 `queued ≤ 上限` 与 `连接数 ≤ 阈值` 的判断（需读取运行时队列容量配置与连接阈值）。
- Impact：AC-008 这两项依赖人工目视核对，自动化压测闭环不完整；不影响正确性（无超卖/无重复已自动判定）。
- Evidence：`verify()` 中 `if [[ "$SOLD" -gt "$TOTAL_STOCK" || "$ORDERS" -ne "$SOLD" || "$DUP_ONE" -ne 0 || "$DUP_IDEM" -ne 0 ]]` 未包含 QUEUED/MAX_CONNS。
- Required Fix Boundary：在 `verify()` 中将队列积压与连接数纳入 PASS/FAIL 判定（可读配置或作为脚本参数传入阈值），不改动其它压测语义。

### CLEAN-002：热点 Key 分析依赖 Redis LFU 策略，默认 docker-compose 未启用

- Severity：P3
- Status：OPEN
- Location：`scripts/flashsale-loadtest/hotkeys.sh`、`docker-compose.yml`（redis 服务未设 `maxmemory-policy`）
- AC / Invariant：AC-006（识别秒杀热点 Key 并输出可用分析结果）
- Trigger：对默认 `docker compose up -d` 的 Redis（`redis:7-alpine`，默认 `noeviction`）运行 `hotkeys.sh`。
- Actual：`redis-cli --hotkeys` 与 `OBJECT FREQ` 均要求 Redis 启用 LFU（`maxmemory-policy=allkeys-lfu/volatile-lfu`）；未启用时两者返回错误，脚本仅输出「请确认已启用 LFU」提示，无法产出热点结果。
- Expected：交付/压测环境显式启用 LFU，或脚本/文档给出可执行的一键开启步骤，使热点分析可产出结果。
- Impact：AC-006 在默认环境无法直接产出热点结果；属环境前置条件，实际执行在 Deliverer 里程碑。
- Evidence：`docker-compose.yml` redis 服务无 `command`/`maxmemory-policy` 配置；`hotkeys.sh` L30/L43 注释自述依赖 LFU 并兜底报错提示。
- Required Fix Boundary：使热点分析在压测环境可执行（如 docker-compose 为 redis 增加 LFU 策略，或在 hotkeys.sh/README 给出 `CONFIG SET maxmemory-policy allkeys-lfu` 的明确前置步骤），不改动「仅可观测、不做打散/本地缓存」的 Contract 语义。

### CLEAN-003：setupConsumeTest 清理列表缺 inventory 表（预先存在，非 V5 引入）

- Severity：P3
- Status：OPEN
- Location：`internal/logic/flashsale/consume_test.go` `setupConsumeTest()`（L41-48）
- AC / Invariant：测试可信度（V5 测试复用 `setupConsumeTest`）
- Trigger：仓库内其它包（如 `internal/controller/inventory`）遗留 `inventories`/`inventory_logs` 数据后再单独运行 flashsale 包测试。
- Actual：`setupConsumeTest` 清理 `skus` 但未清理 `inventories`/`inventory_logs`（其 `fk_inventories_sku` 引用 `skus`），导致 `DELETE FROM skus` 以误导性 FK 错误失败（Error 1451）。本次审查首轮运行即复现，清理 inventory 表后恢复正常。
- Expected：测试清理覆盖全部引用 `skus` 的子表，使 flashsale 包测试自包含、不受跨包遗留数据影响。
- Impact：CI 或按包隔离运行时可能出现误导性失败；不改变 V5 生产行为正确性。此为 V3/V4 已有测试基础设施缺口，V5 未修改 `consume_test.go`。
- Evidence：首轮 `go test ./internal/logic/flashsale/...` 报 `DELETE FROM skus: Error 1451 ... fk_inventories_sku`；`setupInventoryServer`（`internal/controller/inventory`）已正确清理 inventory 表作为对照。
- Required Fix Boundary：在 `setupConsumeTest` 清理列表中加入 `inventory_logs`/`inventories`（置于 `skus` 之前），不改动任何生产逻辑。

## Residual Risks（非阻塞，供 Owner 知悉）

- `GET /metrics` 无应用层鉴权/IP 限制，依赖内网/监控网段网络隔离（Contract「Open Risks」已声明并接受）。
- 容量保护四特性在 `manifest/config/config.yaml` 均默认 `enabled: false`（文档注释「开发默认关闭；压测/生产建议开启」）；生产或压测前需显式开启，属部署/配置决策。
