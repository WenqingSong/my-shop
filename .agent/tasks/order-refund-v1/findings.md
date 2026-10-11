# Cleaner Findings

## Review Target

- Task：order-refund-v1（COMPLEX，Design Impact = NEW）
- 任务基线（base commit）：`3ad322992663da9129aa95de8c64b4ab8bba6801`（`develop` 分支任务开始时 HEAD）
- 被审查对象（implementation Evidence Commit C1）：`e7a49bc2b6719f54a6e7c24ea4572564da38c87c`（`feat(refund): 实现退款闭环`）
- Coder 元数据 commit（C2，不审）：`ba4bc7868548231091414f6de4ad0ffc54fe43b6`
- Contract APPROVED target：`537edc4b0363355c3dbab4f516c237b0d6eca9f8`
- 当前 feature 分支：`feat/order-refund-v1`，HEAD = `ba4bc78`（= C2），working tree clean
- 审查范围：`3ad3229..e7a49bc` 的全部相关变更（生产代码 + 测试 + 迁移 + 错误码 + seed + 设计文档）
- 任务前已有修改：无（TaskBuilder 基线记录 working tree clean）
- 全局资源：错误码域 `18000-18999`、migration `20261001000020`（见下方三边一致性）
- 关键环境：MySQL 8.0（`my-shop-mysql`）、Redis 7（`my-shop-redis`）均 Up healthy

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（归属隔离） | PASS | `internal/logic/refund/refund.go` `Apply` 在事务内 `SELECT ... FOR UPDATE` 后校验 `snap.UserId != userID → 18001`；`TestRefundOwnershipIsolation` 断言非所有者 404/18001 且 `countActiveRefunds==0`、订单状态不变 |
| AC-002（支付与状态前置） | PASS | `Apply` 仅 `snap.Status == 20` 放行，否则 18003；`TestRefundOrderStatePrecondition` 覆盖待支付/已发货两类拒绝（received/completed/cancelled 走同一 `status!=20` 分支，机制一致） |
| AC-003（金额校验） | PASS | `ApplyReq` 无金额字段，`amount` 恒取 `snap.TotalAmount`（服务端派生）；`TestRefundApplySuccessAndAmount` 断言 amount=5000=总价 |
| AC-004（单一有效退款） | PASS | `uk_active(active_key)` 生成列唯一兜底（`active_key = status IN (10,20,30) ? order_id : NULL`）；`TestRefundSingleActiveRequest` 8 并发仅 1 成功、有效与总数均=1 |
| AC-005（退款状态机） | PASS | 各迁移均先 `lockRefundStatusInTx` 校验 before 状态再条件更新 + `RowsAffected`；`TestRefundStateMachineIllegalTransition` 断言非法迁移 18002 且状态不变 |
| AC-006（审核不直接退款） | PASS | `Review` 同意→20、拒绝→40，均不碰订单/库存；`TestRefundReviewNotDirectRefund` 断言 approve 后 refund=20 且 order=20 |
| AC-007（退款与发货互斥） | PASS | `Apply`/`Ship`/`Execute success` 三者经同一 `orders` 行锁串行化；`Ship` 内 `HasActiveRequestInTx` 拒绝有效申请；`TestRefundShipMutualExclusion` 覆盖「有效申请发货被拒 18005」+「并发申请+发货不双成功」 |
| AC-008（退款执行幂等） | PASS | `CompleteRefundInTx` 的 `WHERE status=20` 闸门 + 重复执行 fast-path（已 60 直接返回）；`TestRefundSuccessConsistency` 重复执行库存不变 |
| AC-009（退款成功一致性） | PASS | 单事务「订单 20→70 + 恢复库存 + refund→60 + 审计」；`TestRefundSuccessConsistency` 断言 order=70、库存恢复一次、refund=60、退款后发货 9002 |
| AC-010（退款失败安全） | PASS | 失败 20→30 不碰订单/库存；关闭仅 30→70；重试 30→60；`TestRefundFailureSafeAndClose` + `TestRefundRetrySuccessAfterFailure` 全覆盖 |
| AC-011（撤销申请） | PASS | `Revoke` 10→50 不恢复库存不改订单；`TestRefundRevoke` 断言撤销后 order=20、库存不变、可再次申请 |
| AC-012（查询能力） | PASS | 用户 `Detail/List`（`findOwnedRefund` 归属过滤）+ 管理员 `AdminList/AdminDetail/ListAudits`；`TestRefundQueryAndAudit` 覆盖用户/管理员查询 |
| AC-013（审计） | PASS | 审计仅 `Insert` + `Scan`，无 UPDATE/DELETE 接口；`TestRefundQueryAndAudit` 断言 3 条审计（apply/review_approve/execute_success）动作/操作者/前后态正确 |
| AC-014（权限） | PASS | 后台退款路由 `require("refund:*")` + controller 双保险 `AdminPrincipalFromContext`；`TestRefundPermission` 断言普通用户 403、无权限管理员 403 且零写入、超管放行 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `gofmt -l`（退款/订单/编码相关目录） | PASS | 无输出 |
| `go vet ./...` | PASS | 无输出，exit 0 |
| `go build ./...` | PASS | exit 0 |
| `go test -p 1 ./...` | PASS | 全量通过（含 `internal/boot`、`internal/cmd`、`internal/migrations` 等；退款集成测试走真实路由 + MySQL + Redis） |
| `go test ./internal/cmd/ -run 'TestRefund' -count=1` | PASS | ok（约 10s） |
| `go test -race ./internal/cmd/ -run 'TestRefundSingleActiveRequest\|TestRefundShipMutualExclusion\|TestRefundSuccessConsistency\|TestRefundRetrySuccessAfterFailure' -count=1` | PASS | ok（约 17s），并发关键路径无竞态 |
| 三边一致性 Registry ↔ Contract ↔ 实现 | PASS | 见下节；`scripts/check-registry.sh` 的 FAIL 为本地 feature 分支 Registry 落后于 develop 的假阳性，非漂移 |

## 全局资源三边一致性

权威 Registry（`origin/develop`，HEAD `f14a88a`）：
- `.agent/registry/error-codes.md`：`18000-18999 | order-refund-v1 | RESERVED` ✓
- `.agent/registry/migrations.md`：`20261001000020 | refund | order-refund-v1 | RESERVED` ✓

Contract（`contract.md` Global Resources）：
- 错误码域 `18000-18999`、migration `20261001000020` ✓

实现：
- `internal/codes/codes.go`：`CodeRefundNotFound=18001`…`CodeRefundShipConflict=18005`（域内）✓
- `internal/migrations/sql/20261001000020_refund.up.sql` ✓

三者一致，无漂移。

说明：`scripts/check-registry.sh` 读取**本地** `.agent/registry/*`（feature 分支基于 `3ad3229`，尚未包含 Analyst 已 push 到 develop 的 Registry-only commit `f14a88a`），故对 18001-18005 与 20261001000020 报 FAIL。此为 feature 分支落后 develop 的 Git 拓扑事实，权威 develop Registry 已含正确 RESERVED 条目，不属于实现漂移；feature 合并时将带入该 Registry commit。

## 四者一致性（Task ↔ Contract ↔ Design ↔ Implementation）

- `docs/design/refund.md`（新增）：七态状态机、数据模型、并发锁、错误码域、权限边界均与 APPROVED Contract 及实现一致。
- `docs/design/order.md`（UPDATE）：`paid→refunded` 触发者已由「管理员直接退款」改为「退款成功（refund 模块）」，`Refund` 端点与 `order:refund` 权限移除，`Ship` 事务化 + 有效退款检查，与实现一致。
- 废弃项已清除：`POST /admin/orders/:id/refund` 路由、`RefundReq/RefundRes`、`sOrder.Refund`、`order:refund` 权限均无残留引用（全仓检索 0 命中）。

## Findings

### CLEAN-001：审核拒绝路径与「拒绝后再次申请」缺独立测试

- Severity：P3
- Status：OPEN
- Location：`internal/cmd/refund_test.go`（`TestRefundReviewNotDirectRefund` 仅覆盖 approve）
- AC / Invariant：AC-006（拒绝→审核拒绝）、INV-011/D4（拒绝后允许再次申请）
- Trigger：审核拒绝分支未直接断言；回归场景为「review reject → refund.status=40 → 订单仍 paid → 可再次申请 → 发货解除阻塞」。
- Actual：当前 `Review` 拒绝分支为 approve 的对称实现（`target=review_rejected`），代码正确；`TestRefundStateMachineIllegalTransition` 未覆盖该正向迁移，亦无「拒绝后再次申请」断言。
- Expected：补充一个覆盖 reject 正向路径（refund=40、订单=20、库存不变、可再次申请）的测试，锁住该业务分支。
- Impact：仅测试覆盖缺口，无行为缺陷；拒绝后 `active_key` 置 NULL 已由 `uk_active` 生成列机械保证可再次申请。
- Evidence：`internal/logic/refund/refund.go` `Review`（target 分支）+ 迁移 `active_key` 生成列。
- Required Fix Boundary：新增测试覆盖「审核拒绝→40→再次申请成功」，不改生产行为。

（无 P0/P1/P2。CLEAN-001 为 P3 测试覆盖建议，交由 Owner 决定是否补测。）
