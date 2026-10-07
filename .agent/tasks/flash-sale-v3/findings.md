# Cleaner Findings

## Review Target

- 任务：flash-sale-v3（秒杀异步下单）
- 分支：`feat/flash-sale-v3`
- 任务基线 Base Commit：`d8438b4938a9b6b2e2f82fb87d9c6c9a60b6662b`（合并 main #17，working tree clean，无既有未提交修改）
- Contract target（A1，APPROVED）：`d1d594cd01c4b82febc2809b9f70512d4e914d8f`
- **Implementation Evidence Commit（C1，本次审查对象）**：`129ddb227c7563f08f4c05de7bca448c7fd9a51a`
- C2（Coder metadata commit，非审查对象）：`93f8677b231433f0fb915bd467800f06f1888779`（仅 `review.status=PENDING` / `review.target=C1`）
- 审查范围：C1 相对 Base 的完整变更（含新增文件），非默认 `git diff`。
- 全局资源：migration `20261001000015`（title `flash_sale_order_requests`）；无新增错误码（复用秒杀域 12001~12007 与通用 1004/1005）。

C1 变更文件清单（18 个）：`api/flashsale/v1/flash_sale.go`、`internal/boot/boot_migration_test.go`、`internal/cmd/cmd.go`、`internal/cmd/flashsale_test.go`、`internal/cmd/flashsale_v2_test.go`、`internal/cmd/flashsale_v3_test.go`、`internal/cmd/routes_frontend.go`、`internal/cmd/routes_test.go`、`internal/controller/flashsale/flashsale.go`、`internal/logic/flashsale/consume.go`、`internal/logic/flashsale/consume_test.go`、`internal/logic/flashsale/flashsale.go`、`internal/logic/flashsale/redis.go`、`internal/logic/flashsale/request.go`、`internal/migrations/migrations_test.go`、`internal/migrations/sql/20261001000015_flash_sale_order_requests.up.sql`、`internal/service/flashsale.go`、`manifest/config/config.yaml`。

## Result

CLEAN（无开放 P0/P1/P2；保留 1 项 P3 供 Owner 决定）

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（入队与快速响应） | PASS | `CreateOrder` 闸门通过后 `enqueue`（`status=queued`）快速返回，不等待落单；`TestFlashSaleV3EnqueueAndResultQuery` 断言返回 `queued` 且此时 `flash_sale_orders` 计数为 0。 |
| AC-002（消费者落单与不变量保持） | PASS | 消费者 `ConsumeQueued`→`insertOrderInTx` 复用条件扣减 `sold < total_stock` + 唯一约束兜底；`TestFlashSaleConcurrentNoOversell`（20 并发入队、stock=5，`-race`）断言成功订单数=sold=5、不超卖。 |
| AC-003（消费幂等与重复消息去重） | PASS | `FOR UPDATE SKIP LOCKED` + 状态原子更新；`TestFlashSaleV3ConsumeIdempotent` 断言再次消费不重复建单、`sold` 不重复扣减。 |
| AC-004（失败重试与死信/待修复） | PASS | `retryOrDeadRequestInTx` 指数退避重试、超 `maxConsumeRetryAttempts=3` 落 `dead`；`TestConsumeRetryThenDeadLetter` 白盒覆盖 retry→dead→`dead→queued` 重处理成功。 |
| AC-005（结果查询与归属隔离） | PASS | `GET /flash-sales/:id/orders/result` 按 `(user_id, activity_id, idempotency_key)` 校验；`TestFlashSaleV3EnqueueAndResultQuery` 断言 queued→success 状态、他人/不存在统一 404(1004)。 |
| AC-006（失败补偿与无残留） | PASS | 消费终态失败后 `compensatePreDeductAndMarkers`（INCR remaining + 清 bought/idem 标记）；`TestFlashSaleV3Compensation` 断言时间窗结束→failed、无订单、无扣库存、Redis 预扣回补、标记清除。 |
| AC-007（队列不可用/降级） | PASS | Redis 闸门 Lua 失败 → fail-closed 5xx(1005)，不落 request、不预扣、不同步落单；`TestFlashSaleV3FailClosed` 构造 WRONGTYPE 断言 503 且无副作用。 |
| AC-008（长期设计） | PASS | `docs/design/flash-sale.md` 更新 §2.4（出队表）、§3（四态状态机）、§5.3/5.4/5.5（异步一致性/fail-closed/消费扫描器）、INV-012/013、§6.2（GATE_PASSED 写标记）、§8（复用 1004/1005），与 Contract、实现一致。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| gofmt | PASS | 对全部改动 Go 文件 `gofmt -l` 输出为空。 |
| go build ./... | PASS | exit 0。 |
| go vet ./... | PASS | exit 0。 |
| go test -p 1 ./... | PASS | 全量包通过（含 `internal/cmd` 38.6s、`internal/migrations`、`internal/logic/flashsale`）。 |
| go test -race -run TestFlashSale ./internal/cmd/... | PASS | 秒杀 V1/V2/V3 全量集成测试含并发用例通过。 |
| go test -race -run TestConsumeRetryThenDeadLetter ./internal/logic/flashsale/... | PASS | 死信/重试白盒测试通过。 |
| go test ./internal/migrations/... | PASS | namespace 解耦后全套稳定（synthetic 版本 1/2 与生产 00015 隔离）。 |
| 全局资源三边一致性 | PASS | Registry(origin/develop)：`20261001000015 flash_sale_order_requests flash-sale-v3 RESERVED` ↔ Contract：`20261001000015` ↔ 实现：`20261001000015_flash_sale_order_requests.up.sql` 一致；错误码无新增，复用 12001~12007/1001/1004/1005。 |

环境：MySQL 8.0 与 Redis 7（`docker compose` 均 healthy）。

说明：`scripts/check-registry.sh` 报告「20261001000015 未在 Registry 登记」为**脚本读本地（feature 分支）Registry 文件导致的误报**——Analyst 的 Registry-only commit 只落在 `origin/develop`，feature 分支未合并该 develop 提交。已用 `origin/develop` 权威 Registry 手工核对，`00015` 确为 `RESERVED`、owner `flash-sale-v3`，三边一致，非真实漂移。

## Findings

### CLEAN-001：入队撞幂等键且内容冲突时可能重复补偿 Redis 预扣

- Severity：P3
- Status：OPEN
- Location：`internal/logic/flashsale/flashsale.go` `CreateOrder`（`enqueue` 出错后再次 `compensatePreDeductAndMarkers`）与 `internal/logic/flashsale/request.go` `enqueue`（撞 `uk_request_idempotency` 时已内部补偿一次）
- AC / Invariant：AC-006 / INV-013（失败补偿无残留、预扣计数准确）
- Trigger：闸门 `GATE_PASSED`（已 DECR 预扣 + 写 `idem` 标记）后，`enqueue` INSERT 命中 `uk_request_idempotency` 重复键（Redis `idem` 标记因 TTL/Flush 缺失但 MySQL 请求行仍在），且 `resultForExistingRequest` 读到既有请求 `request_hash` 与新请求不同 → 返回 12005。
- Actual：`enqueue` 内部先补偿一次（INCR remaining + 清标记），再返回 12005 error；`CreateOrder` 收到 error 后**再次** `compensatePreDeductAndMarkers`，导致 Redis `remaining` 多 INCR 一次。
- Expected：同一次预扣只补偿一次；`enqueue` 已处理幂等冲突时 `CreateOrder` 不应重复补偿。
- Impact：仅 Redis 预扣计数短暂虚高（多 +1），可能在该窗口多放行若干请求（最终在消费侧以 12003 落 `failed` 并补偿）；**不影响 MySQL 正确性**（条件扣减 + 唯一约束兜底不超卖），且对账扫描器（`remaining = total - sold - inflight_queued`）在 60s 内收敛修正。
- Evidence：代码路径 `CreateOrder`→`enqueue`（duplicate 分支内部补偿）→`resultForExistingRequest`（hash 不符→12005）→`CreateOrder` 再次补偿；当前无测试覆盖「幂等键存在 + 内容冲突 + 经入队重复键」这一窄路径。
- Required Fix Boundary：保证「一次预扣只补偿一次」——可在 `enqueue` 已内部补偿的幂等冲突路径上让 `CreateOrder` 不再二次补偿（例如 `enqueue` 返回带「已补偿」语义的哨兵，或把补偿统一收敛到 `CreateOrder` 单点），不改变其它路径行为、不改变 MySQL 不变量。

其余：No actionable findings（P0/P1/P2 无）。
