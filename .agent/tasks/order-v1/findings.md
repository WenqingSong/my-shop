# Cleaner Findings

## Review Target

- 任务：`order-v1`（订单核心闭环 V1），Design Impact = NEW，Artifact `docs/design/order.md`。
- 任务基线（Task 声明）：`f197bc20f41a8a545cef3d5b7b8d8f1e3435aa6b`（分支 `feat/order`，任务开始时 working tree clean）。
- 当前审查对象：HEAD = `e65f1e2d94bdfb7a8e1fc7b853c3e2d78176cb08`（`feat(order): 新增订单 API 定义与超时取消扫描`）。
- 完整相关变更范围：`f197bc2..HEAD`，含两个 commit：
  - `11b8607`（docs：Contract、Design、Registry、task 等 8 文件）
  - `e65f1e2`（实现 + 测试：18 文件）
- 工作区状态：clean（`git status --short` 为空），即审查对象已全部提交，无未提交修改需要区分。
- 新增未跟踪文件已全部纳入 commit，不存在仅靠 `git diff` 会遗漏的新增文件。

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
| AC-015 取消只补偿一次 | PASS | `TestOrderCancelRestoresInventoryOnce`（并发取消库存仅恢复一次）；`cancelInTx` 条件状态更新 + RowsAffected 原子闸门。已用 `-race` 运行通过。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0，无编译错误。 |
| `go vet ./...` | PASS | exit 0，无静态检查问题。 |
| `gofmt -l`（变更文件） | PASS | 无输出，格式一致。 |
| `go test -p 1 -count=1 ./...` | PASS | 全部包 ok，含 migrations/boot/cmd/controller/*。 |
| `go test -race -count=1 -run TestOrder ./internal/cmd/` | PASS | ok（39.4s），覆盖并发不超卖/取消只补偿一次等 AC。 |
| `scripts/check-registry.sh` | 见 Findings（非阻塞） | 报 6 处「9001-9006 不在 ACTIVE 域」，实为订单域处于 RESERVED 的预期中间态，非真实漂移（见 CLEAN-001）。 |
| Registry ↔ Contract ↔ 实现 三边一致性（语义） | PASS | 错误码域 9000-9999、migration 20261001000007 在 Registry(RESERVED)/Contract/实现三方一致；权限 code `order:ship`/`order:refund` 已 seed。 |
| Task ↔ APPROVED Contract ↔ docs/design/order.md ↔ 实现 四者一致 | PASS | 数据模型、状态机、不变量、错误码、权限、路由、配置契约均一致。 |

## Findings

### CLEAN-001：`check-registry.sh` 对 RESERVED 域的错误码报漂移（非真实漂移）

- Severity：P3
- Status：OPEN
- Location：`scripts/check-registry.sh` 的 `check_error_drift`（仅校验 ACTIVE 域）
- AC / Invariant：全局资源三边一致性（Registry ↔ Contract ↔ 实现）
- Trigger：在 feature 分支上运行 `./scripts/check-registry.sh`
- Actual：脚本对 9001-9006 报「不在任何 ACTIVE 域内」，exit 1。
- Expected：脚本应识别 RESERVED 域内的错误码为「已预留、未合并」而非漂移；或在三边语义审查中人工确认一致。
- Impact：无业务影响。订单错误码域 9000-9999 在 Registry 标记 RESERVED（未合并进 develop），Contract 与实现均使用 9001-9006，三方语义一致。脚本只做 ACTIVE 域机械校验，未覆盖 RESERVED 中间态，属脚本已知局限（脚本自述「不替代语义判断」）。
- Evidence：Registry `error-codes.md` 中 `9000-9999 | order | RESERVED`；Contract「错误码域 9000-9999 域序 9 RESERVED」；`internal/codes/codes.go` 定义 `9001-9006`。三边无冲突。脚本运行输出见上方。
- Required Fix Boundary：无需 Coder 修改生产代码；可选改进脚本对 RESERVED 域的判断，或维持现状由 Cleaner 语义审查兜底。由 Owner 决定是否处理。

### CLEAN-002：`migrations_test.go` 的精确结构等价测试未覆盖新增订单表

- Severity：P3
- Status：OPEN
- Location：`internal/migrations/migrations_test.go` 的 `expectedSchema`（仅覆盖 7 张基线表）
- AC / Invariant：schema 结构严格等价（`TestSchemaStructureMatchesBaseline` 的覆盖范围）
- Trigger：对 `orders`/`order_items` DDL 做列类型/默认值/索引的错误改动，仍可能通过结构等价测试。
- Actual：`expectedSchema` 仅含 `users/categories/admins/roles/permissions/admin_roles/role_permissions` 7 张表，未覆盖新增 `orders`/`order_items`。
- Expected：订单表的关键结构（唯一约束、FK CASCADE、快照字段、索引）有结构级回归保护。
- Impact：低。订单表唯一约束（`uk_user_idempotency`/`uk_order_no`）与 FK 语义已被集成测试功能化覆盖（幂等/唯一订单号/下单回滚）；`TestUpCreatesSchemaAndIsIdempotent` 断言 16 张表均存在。仅列级结构（类型/默认值/`idx_status_expire` 等）无等价性测试。另附：文件内「13 张业务表」「7 张表」等注释与当前 16 张业务表的事实不符，属陈旧注释。
- Evidence：`migrations_test.go` `businessTables`（16 项）vs `expectedSchema`（7 项）；`TestSchemaStructureMatchesBaseline` 仅遍历 `expectedSchema`。
- Required Fix Boundary：由 Owner 决定是否补齐订单表结构等价快照与更新陈旧注释；不阻塞本任务 CLEAN。

## 结论说明

无开放 P0/P1/P2。全部 15 项 AC 有可信集成测试（真实 `RegisterFrontendRoutes`/`RegisterAdminRoutes` + `middleware.Auth`/`AdminAuth`/`RequirePermission` + 真实 MySQL/Redis）与必要运行证据支持 PASS。两个 P3 由 Owner 决定是否处理，不阻塞接受。
