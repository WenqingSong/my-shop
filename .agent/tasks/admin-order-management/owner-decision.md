# Owner Decision

## Review Target
6e0e3c89a8e32d8be601fe58ea676968955a5d42

## Core Logic
- CL-001：后台订单只读授权隔离（`order:list` / `order:view`）。无对应权限的非超管访问列表/统计/详情返回 403 且零写入；普通用户 token（`type=user`）访问 `/admin/orders*` 返回 403；超管放行。生产代码：`internal/cmd/routes_admin.go:123-125` + `internal/middleware/auth.go` `RequirePermission`。关键测试：`internal/cmd/order_admin_test.go` `TestAdminOrderReadOnlyPermission`。
- CL-002：订单详情历史快照自足。成交金额/商品/SKU 名/单价/数量来自 `orders`/`order_items` 下单快照，不重读 `skus.price`/商品当前售价。生产代码：`internal/logic/order/order.go` `AdminDetail` → `loadByIDOrder` → `toOrder` → `loadItems`。关键测试：`internal/cmd/order_admin_test.go` `TestAdminOrderDetailSnapshot`。

## Owner Decision
ACCEPTED

## Decision Evidence
- 2026-10-10，Owner 在 OwnerGate Decision Checkpoint 明确回复 `ACCEPT`。
- 接受对象：Cleaner 已 CLEAN 的 `review.target = 6e0e3c89a8e32d8be601fe58ea676968955a5d42`（本任务「后台订单只读管理」实现 snapshot）。
- 适用范围：上述 CL-001、CL-002 两个核心机制及其对应实现与测试；不含对交易正确性机制（状态机/库存/事务/发货/退款）的改动（本任务未触碰）。
