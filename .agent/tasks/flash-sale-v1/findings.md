# Cleaner Findings

## Review Target

- 分支：`feat/flash-sale-v1`
- Base/HEAD commit：`e0a48e9f023809d279e61fe7a0052a7cff461095`（与 task.md Review Baseline 一致，任务前 working tree clean）
- 本次审查对象 = HEAD 之上的工作区变更（含未跟踪新增文件），完整清单见 `state.yaml` 的 `review.target_paths`。
- 环境：MySQL 8.0（`my-shop-mysql`，healthy）、Redis 7（`my-shop-redis`，healthy）、Go 1.24.1。
- Contract：APPROVED（2026-10-05）；Design Impact = NEW，`docs/design/flash-sale.md` 已存在。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（活动创建与可见+权限） | PASS | `TestFlashSaleCreateActivityAndPermission`（真实路由 + 超管创建落库 + 无权限 403/1003 且无写入）通过 |
| AC-002（时间窗拒绝） | PASS | `TestFlashSaleOrderTimeWindow`（开始前/结束后 409/10002，订单与库存均不变）通过 |
| AC-003（秒杀价生效与快照） | PASS | `TestFlashSaleOrderPricingSnapshot`（成交价=秒杀价 1000 非 SKU 价 5000，改价后快照不变）通过 |
| AC-004（一人一单） | PASS | `TestFlashSaleOnePerUser`（第二次购买 409/10004，仅 1 单、sold=1）通过 |
| AC-005（幂等防重复扣库存） | PASS | `TestFlashSaleIdempotency`（同键同内容返回既有订单、仅 1 单、扣 1 次；同键不同 SKU → 10005）通过 |
| AC-006（库存不为负） | PASS | `TestFlashSaleConcurrentNoOversell`（-race，sold 恒 ≤ total_stock）通过 |
| AC-007（成功订单数 ≤ 初始库存） | PASS | 同上（20 并发对 5 库存，成功恰 5，`COUNT(orders)=sold=5`）通过 |
| AC-008（失败不建订单） | PASS | `TestFlashSaleOrderFailures`（库存不足/参数非法/不存在/下架/未绑定/越权均无订单、无扣减、无半成品）通过 |
| AC-009（事务原子性） | PASS | `TestFlashSaleOrderFailures`（库存不足回滚，sold 与订单均不变）+ 代码审查确认单事务 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| go build ./... | PASS | 退出码 0、无输出 |
| go vet ./... | PASS | 退出码 0、无输出 |
| go test -race -count=1 -run 'TestFlashSale' ./internal/cmd/ | PASS | ok（17.030s） |
| go test -race -run 'TestFlashSaleUpdate' ./internal/cmd/ -v | PASS | 含回归测试 `TestFlashSaleUpdateCannotAddOrRemoveBindings` 通过 |
| go test -p 1 -count=1 ./... | PASS | 全量测试通过 |
| Registry ↔ 实现一致性 | PASS | `scripts/check-registry.sh` 通过（错误码域 10000-10999、migration 20261001000009 无漂移） |
| 四者一致性（Task↔Contract↔Design↔实现） | PASS | 修复后 update 语义收敛为「仅改已绑定 SKU 的秒杀价/库存」，与 Contract 接口「改秒杀价/库存/时间/状态/下架」一致 |

## Findings

### CLEAN-001：更新活动可删除已售（sold>0）绑定，破坏库存/订单计数不变量

- Severity：P2
- Status：CLOSED
- Location：`internal/logic/flashsale/flashsale.go` `syncBindings`（已移除，替换为 `updateBindings`）
- AC / Invariant：INV-002（`sold ≤ total_stock` 且 `COUNT(flash_sale_orders) = sold`）；Goal 不变量 2「成功订单总数不超过初始库存」
- Trigger：`PUT /admin/flash-sales/:id` 提交的 `skus` 列表省略某个已售（`sold>0`）的 SKU 绑定
- 修复内容：删除 `syncBindings`（全量增删绑定），替换为 `updateBindings`（`flashsale.go:425-452`）——仅更新已存在绑定的 `flash_price`/`total_stock`（保留 `sold`，校验 `total_stock ≥ sold`）；请求中的 `sku_id` 未绑定该活动时返回 `10007`，不再新增、不再删除绑定。
- 复审验证：原触发条件已消除——`syncBindings` 全仓 0 引用；`updateBindings` 无 DELETE/INSERT 分支；回归测试 `TestFlashSaleUpdateCannotAddOrRemoveBindings` 覆盖「省略 skuB 不删除、提交未绑定 skuC 返回 10007 且不新增」，且构造 `sold=6` 断言更新后 `sold` 保留，能区分修复前后。
- 结论：关闭。

### 附注（P3，不阻塞，交 Owner 决定）

- `internal/migrations/migrations_test.go` 两处注释仍写「16 张业务表」「7 张 baseline 表 + orders + order_items」，与当前 20 张业务表（含 3 张秒杀表）不符，属陈旧注释。
