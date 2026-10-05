# Cleaner Findings

## Review Target

- 分支：`feat/flash-sale-v1`
- 任务基线 Base commit：`e0a48e9f023809d279e61fe7a0052a7cff461095`（与 task.md Review Baseline 一致，任务前 working tree clean）
- 当前 HEAD（本次 CLEAN 绑定 target_base）：`ca4c92a71cc458aaed3d81a9ae0b8ef14315b6f3`（`fix(flash-sale): renumber reserved resources`）
- 本次为**复审（re_review）**：原因 = 秒杀全局资源从「10000-10999 / 20261001000009」重编号为「12000-12999 / 20261001000011」并合并 `develop` 后，前次 CLEAN 被机械失效（`review.status → STALE`）；复审验证 CLEAN-001 修复在重编号后仍成立，并重验全局资源三边一致性与全部 AC。
- 被审查文件（`state.yaml` `review.target_paths` 17 项）：秒杀 API/controller/logic/service、migration、集成测试、design、registry、codes、seed、routes、logic、migrations_test。
- 环境：MySQL 8.0（`my-shop-mysql` healthy）、Redis 7（`my-shop-redis` healthy）、Go 1.24.1。
- Contract：APPROVED（2026-10-05）；Design Impact = NEW，`docs/design/flash-sale.md` 已存在并与 Contract 同步。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（活动创建与可见+权限） | PASS | `TestFlashSaleCreateActivityAndPermission`（超管经真实路由创建落库；无权限普通管理员 403/1003 且无写入）通过 |
| AC-002（时间窗拒绝） | PASS | `TestFlashSaleOrderTimeWindow`（开始前/结束后 409/12002，订单与库存均不变）通过 |
| AC-003（秒杀价生效与快照） | PASS | `TestFlashSaleOrderPricingSnapshot`（成交价=秒杀价 1000 非 SKU 价 5000，改价后快照不变）通过 |
| AC-004（一人一单） | PASS | `TestFlashSaleOnePerUser`（第二次购买 409/12004，仅 1 单、sold=1）通过 |
| AC-005（幂等防重复扣库存） | PASS | `TestFlashSaleIdempotency`（同键同内容返回既有订单、仅 1 单扣 1 次；同键不同 SKU → 12005）通过 |
| AC-006（库存不为负） | PASS | `TestFlashSaleConcurrentNoOversell`（-race，sold 恒 ≤ total_stock）通过 |
| AC-007（成功订单数 ≤ 初始库存） | PASS | 同上（20 并发抢 5 库存，成功恰 5，`COUNT(orders)=sold=5`）通过 |
| AC-008（失败不建订单） | PASS | `TestFlashSaleOrderFailures`（库存不足 12003/参数非法 1001/不存在 12001/下架 12001/未绑定 1001/越权 1002 均无订单、无扣减、无半成品）通过 |
| AC-009（事务原子性） | PASS | `TestFlashSaleOrderFailures`（库存不足整体回滚，sold 与订单均不变）+ 代码审查确认单事务 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| go build ./... | PASS | 退出码 0、无输出 |
| go vet ./... | PASS | 退出码 0、无输出 |
| gofmt -l（秒杀相关文件） | PASS | 无输出（无格式问题） |
| go test -race -count=1 -run 'TestFlashSale' ./internal/cmd/ | PASS | ok（17.891s），含全部秒杀测试 |
| go test -race -count=1 -run 'TestFlashSaleUpdateCannotAddOrRemoveBindings' ./internal/cmd/ | PASS | 回归测试通过（2.62s），能区分修复前后 |
| go test -p 1 -count=1 ./... | PASS | 全量测试通过（含 migrations/boot/cmd 等全部包） |
| scripts/check-registry.sh | PASS | 错误码 12001-12007 落在 RESERVED 域 12000-12999（预期中间态，非漂移）、migration 20261001000011 无重复/漂移 |
| Registry ↔ Contract ↔ 实现 三边一致性 | PASS | 错误码域 12000-12999（Registry）↔ 12000-12999（Contract）↔ codes.go 12001-12007（实现）；migration 20261001000011（Registry）↔ 20261001000011（Contract）↔ `20261001000011_flash_sale.up.sql`（实现）三者一致 |
| Task ↔ Contract ↔ Design ↔ 实现 四者一致性 | PASS | `docs/design/flash-sale.md` 的错误码域/表结构/不变量/权限/时间窗语义均与 APPROVED Contract 及最终实现一致 |

## Findings

### CLEAN-001：更新活动可删除已售（sold>0）绑定，破坏库存/订单计数不变量

- Severity：P2
- Status：CLOSED（复审确认）
- Location：`internal/logic/flashsale/flashsale.go` `updateBindings`（L425-452，原 `syncBindings` 已移除）
- AC / Invariant：INV-002（`sold ≤ total_stock` 且 `COUNT(flash_sale_orders) = sold`）；Goal 不变量 2「成功订单总数不超过初始库存」
- Trigger：`PUT /admin/flash-sales/:id` 提交的 `skus` 列表省略某个已售（`sold>0`）的 SKU 绑定，或提交未绑定的新 SKU
- Actual（修复后）：`updateBindings` 仅更新已存在绑定的 `flash_price`/`total_stock`（`FOR UPDATE` 锁行读 `sold`，校验 `total_stock ≥ sold` 后保留 `sold`）；请求中的 `sku_id` 未绑定该活动时返回 12007（`CodeFlashSaleInvalidArgument`）；不存在任何 DELETE/INSERT 绑定分支。
- Expected：更新不得新增/删除绑定，不得把 `sold` 重置或使 `total_stock < sold`（否则可重售完整库存导致超卖、破坏 `COUNT(orders)=sold`）。
- Impact：已消除。原实现可「删除已售绑定再重加 sold=0」重置库存超卖，现被禁止。
- Evidence：
  - `updateBindings` 无 INSERT/DELETE 分支，仅 `UPDATE flash_price/total_stock`；`total_stock < sold` 时返回 12007。
  - 回归测试 `TestFlashSaleUpdateCannotAddOrRemoveBindings` 覆盖：①省略 skuB 不删除（count 仍 1）；②提交未绑定 skuC 返回 400/12007 且不新增；③构造 `sold=6` 后仅提交 skuA 更新，断言 `sold` 保留为 6（能区分修复前后）。
  - `syncBindings` 全仓 0 引用（已彻底移除）。
- Required Fix Boundary：Update 必须保持「绑定集合只能在 Create 时确定」的不变量，仅允许改已绑定 SKU 的秒杀价/库存且 `total_stock ≥ sold`，不得新增/删除绑定、不得重置 `sold`。
- 结论：关闭。重编号（12007 替换 10007）后修复逻辑与回归测试均保持成立。

### 附注（P3，不阻塞，交 Owner 决定）

- `api/flashsale/v1/flash_sale.go` L100 `CreateOrderReq.IdempotencyKey` 的 `dc` 注释仍写「同键不同内容 10005」，实际错误码已重编号为 12005，属重编号遗漏的陈旧注释。
- `internal/migrations/migrations_test.go` L145 注释「建立 16 张业务表」与 L365 注释「7 张 baseline 表 + orders + order_items」与实际（现 21 张业务表，含秒杀/评价）不符，属陈旧注释。
- `internal/migrations/sql/20261001000011_flash_sale.up.sql` 头注释「version 紧随 refresh_tokens 20261001000008」已过时（实际为 20261001000011，中间隔着 reviews/favorites），属陈旧注释。

以上均为文档注释，不影响业务正确性与测试结论。
