# Cleaner Findings

## Review Target

- 任务：`order-v1`（订单核心闭环 V1），Design Impact = NEW，Artifact `docs/design/order.md`。
- 任务基线（Task 声明）：`f197bc20f41a8a545cef3d5b7b8d8f1e3435aa6b`（分支 `feat/order`，任务开始时 working tree clean）。
- 当前审查对象（复审）：HEAD = `11161f428328e1b45495b37e308a6fa0d878d1e4`（`docs(order): 更新 delivery 阻塞状态与复审链路`）。
- 完整相关变更范围：`f197bc2..HEAD`，含六个 commit：
  - `11b8607`（docs：Contract、Design、Registry、task 等 8 文件）
  - `e65f1e2`（实现 + 测试：18 文件）
  - `38a5a64`（复审修复：`migrations_test.go` 补订单表结构快照、`check-registry.sh` 补 RESERVED 域判断）
  - `4cf1b04`（docs：更新复审结论文档）
  - `f0ffc6d`（test：补充并发退款只补偿一次回归测试 `TestOrderRefundRestoresInventoryOnce`）
  - `11161f4`（docs：更新 delivery 阻塞状态与复审链路）
- 工作区状态：clean（`git status --short` 为空），审查对象已全部提交，无未提交修改需要区分。
- 本次复审范围：上轮 CLEAN（`38a5a64`）之后的增量 = `f0ffc6d` 新增的并发退款回归测试 + `4cf1b04`/`11161f4` 两份 docs；生产代码与订单实现无变化。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 创建订单入口（购物车/直接购买，归属用户） | PASS | `TestOrderCreateDirect`、`TestOrderCreateFromCart`（真实路由 + MySQL + Redis，断言 order_no 非空、user_id、status、购物车清理）。已实际运行通过。 |
| AC-002 服务端定价 + 快照 | PASS | `TestOrderCreateDirect`（成交价=下单时 `skus.price`，忽略客户端）、`TestOrderServerPricingSnapshot`（改价/改地址后快照不变）。已运行通过。 |
| AC-003 唯一订单号 | PASS | `orders.uk_order_no` 唯一约束（`20261001000007_orders.up.sql`）+ `generateOrderNo` 撞号重试；`TestOrderCreateDirect` 连续两单 order_no 不同。 |
| AC-004 幂等防重复下单 | PASS | `TestOrderIdempotency`（同 key 同内容仅一单、库存扣一次；同 key 不同内容 9006）；`uk_user_idempotency` 唯一约束兜底。 |
| AC-005 事务原子性 | PASS | `TestOrderInsufficientStockRollback`（库存不足 6001，无半成品订单/订单项，库存不变）；`insertOrder` 单事务写订单+订单项+扣库存。 |
| AC-006 状态机合法迁移 | PASS | `TestOrderStateMachine`（待支付→已支付→已发货→已收货→已完成全链路）。 |
| AC-007 非法迁移失败 | PASS | `TestOrderStateMachine`（已支付取消、已完成再收货均 9002 且状态不变）。 |
| AC-008 取消恢复库存 | PASS | `TestOrderCancelRestoresInventoryOnce`（取消后库存恢复，并发 8 次仅 1 次成功、库存恢复一次）。 |
| AC-009 超时未支付自动取消 | PASS | `TestOrderTimeoutAutoCancel`（懒取消 + `CancelExpired` 扫描器均恢复库存）；`cmd.go` 后台 ticker 扫描 + `cancelInTx(onlyExpired=true)`。 |
| AC-010 支付 Mock 幂等 | PASS | `TestOrderPayIdempotent`（重复支付不重复改状态，paid_at 已设置）。 |
| AC-011 用户订单隔离 | PASS | `TestOrderUserIsolation`（他人查看/取消他人订单 404/9001，无写入）。 |
| AC-012 管理员发货/退款权限 | PASS | `TestOrderAdminShipRefundPermission`（无权限管理员 403/1003、普通用户 token 403、超管退款成功）。 |
| AC-013 越权无副作用 | PASS | `TestOrderUserIsolation` + `TestOrderAdminShipRefundPermission`（越权后状态/库存均不变）。 |
| AC-014 并发不超卖 | PASS | `TestOrderConcurrentNoOversell`（并发 20 单、库存 5，仅 5 成功，库存=0）；`DeductInTx` 条件扣减 `WHERE quantity >= N` + RowsAffected。已用 `-race` 运行通过。 |
| AC-015 取消/退款只补偿一次 | PASS | `TestOrderCancelRestoresInventoryOnce`（并发 8 取消仅 1 次成功、库存恢复一次）+ `TestOrderRefundRestoresInventoryOnce`（并发 8 退款仅 1 次 paid→refunded、库存恢复一次、终态 70）；`cancelInTx`/`Refund` 条件状态更新 + RowsAffected 原子闸门。已用 `-race` 运行通过。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0，无编译错误。 |
| `go vet ./...` | PASS | exit 0，无静态检查问题。 |
| `gofmt -l`（变更文件） | PASS | 无输出，格式一致。 |
| `go test -p 1 -count=1 ./...` | PASS | 全部包 ok，含 migrations/boot/cmd/controller/*。 |
| `go test -race -count=1 -run TestOrder ./internal/cmd/` | PASS | ok（41.6s），覆盖并发不超卖/取消/退款只补偿一次等 AC。 |
| `scripts/check-registry.sh` | PASS | 复审后 exit 0：9001-9006 落在 RESERVED 域仅提示 `[INFO]`，结尾「校验通过」（CLEAN-001 已修复）。 |
| `go test -race -count=1 -run TestOrderRefundRestoresInventoryOnce ./internal/cmd/` | PASS | ok（5.5s），新增并发退款回归测试通过。 |
| Mutation 验证（测试可信度·退款） | PASS | 临时删除 `Refund` 的 RowsAffected 核对后，`TestOrderRefundRestoresInventoryOnce` 失败（`exactly one refund should succeed, got 8`），确认能识别「只补偿一次」被破坏的实现；恢复后通过、工作区 clean。 |
| Registry ↔ Contract ↔ 实现 三边一致性（语义） | PASS | 错误码域 9000-9999、migration 20261001000007 在 Registry(RESERVED)/Contract/实现三方一致；权限 code `order:ship`/`order:refund` 已 seed。 |
| Task ↔ APPROVED Contract ↔ docs/design/order.md ↔ 实现 四者一致 | PASS | 数据模型、状态机、不变量、错误码、权限、路由、配置契约均一致。 |

## Findings

### CLEAN-001：`check-registry.sh` 对 RESERVED 域的错误码报漂移（非真实漂移）

- Severity：P3
- Status：CLOSED（复审已修复并验证）
- Location：`scripts/check-registry.sh` 的 `check_error_drift`（原仅校验 ACTIVE 域）
- AC / Invariant：全局资源三边一致性（Registry ↔ Contract ↔ 实现）
- Trigger：在 feature 分支上运行 `./scripts/check-registry.sh`
- Actual（修复前）：脚本对 9001-9006 报「不在任何 ACTIVE 域内」，exit 1。
- Expected：脚本应识别 RESERVED 域内的错误码为「已预留、未合并」而非漂移。
- 修复：`check_error_drift` 现同时收集 RESERVED 域（`rstarts/rends`），落在 RESERVED 域内的错误码仅输出 `[INFO]`、不算漂移；仅当既不落 ACTIVE 也不落 RESERVED 域才 `fail`。
- 复审证据：`./scripts/check-registry.sh` 现 exit 0，9001-9006 均输出 `[INFO] ... 在 RESERVED 域内（已预留、未合并进 develop，非漂移）`，结尾「校验通过」。三边语义一致（Registry `9000-9999 RESERVED` = Contract = 实现 `9001-9006`）。

### CLEAN-002：`migrations_test.go` 的精确结构等价测试未覆盖新增订单表

- Severity：P3
- Status：CLOSED（复审已修复并验证）
- Location：`internal/migrations/migrations_test.go` 的 `expectedSchema`
- AC / Invariant：schema 结构严格等价（`TestSchemaStructureMatchesBaseline` 的覆盖范围）
- Trigger：对 `orders`/`order_items` DDL 做列类型/默认值/索引的错误改动，仍可能通过结构等价测试。
- Actual（修复前）：`expectedSchema` 仅含 7 张基线表，未覆盖 `orders`/`order_items`；另「13 张业务表」等注释陈旧。
- 修复：`expectedSchema` 新增 `orders`（24 列 + 5 索引，含可空字段与默认值）与 `order_items`（10 列 + 2 索引）的精确结构快照，与 `20261001000007_orders.up.sql` 逐列逐索引对齐；「13 张业务表」改为「16 张」，`columnSpec.Nullable` 与 `expectedSchema` 注释同步更新。
- 复审证据：`go test -p 1 -count=1 ./internal/migrations/` 通过（含 `TestSchemaStructureMatchesBaseline` 对 9 张表的逐项结构等价校验），`go test -p 1 -count=1 ./...` 全绿。

## 结论说明

二次复审结论：无开放 Finding。本轮针对上轮 CLEAN（`38a5a64`）之后新增的 `f0ffc6d`（并发退款只补偿一次回归测试）复审：

- `TestOrderRefundRestoresInventoryOnce` 完整覆盖 Owner 对 CL-002 的待办——paid 订单并发 8 次 refund、恰好一次成功完成 paid→refunded（`okCount==1` + 终态 `status==70`）、库存只恢复一次（`stock==5`）、其余请求不再补偿；结构与被接受的 `TestOrderCancelRestoresInventoryOnce` 一致，走真实 `/admin/orders/:id/refund` 路由 + 超管 token + 真实 MySQL。
- 测试可信：临时删除 `Refund` 的 `RowsAffected` 核对后测试失败（`got 8`），能区分「只补偿一次」被破坏的实现（已恢复，工作区 clean）。
- 生产代码与订单实现本轮无变化；`go build`/`go vet`/全量测试/`-race`/`check-registry.sh` 全部通过。

CLEAN-001、CLEAN-002 两项 P3（上一轮）仍 CLOSED。另见「验证卡 Mutation 修正」说明：core-logic.md 的 CL-001/CL-002「可选 Mutation」原指向「删条件」，实测不会使并发测试失败（详见 core-logic.md 已同步修正为「删 RowsAffected 核对」），属文档准确性修正、非实现缺陷。
