# Owner Decision

## Review Target
37467cfd2456eb5c0207c12b84b8108791ed7100

## Core Logic
- CL-001：金额与销量的「有效订单状态」口径 —— `today_order_amount` 与商品销量 `sales` 只统计当前状态 `{20,30,40,50}` 的订单，排除待支付(10)/已取消(60)/已退款(70)；金额按 `orders.total_amount` 求和、销量按 `order_items.quantity` 求和，不混用 `view_count`/库存变化。
- CL-002：权限边界 —— 5 个 Dashboard 接口挂 `AdminAuth + RequirePermission("dashboard:view")`，普通用户→403、无/失效 token→401、无 `dashboard:view` 管理员→403，拒绝路径零写入。

## Owner Decision
ACCEPTED

## Decision Evidence
- 2026-10-10，Owner 在 OwnerGate 决策点（`WAITING_FOR_OWNER_DECISION`）明确回复 `ACCEPT`。
- 接受对象：Cleaner 已 CLEAN 的 immutable snapshot `37467cfd2456eb5c0207c12b84b8108791ed7100`（含 CLEAN-001/CLEAN-002 修复后的实现），非抽象接受任务本身。
- 适用范围：本 Task 全部 5 个 Dashboard 接口（overview / orders/trend / orders/status / products/top / flash-sales）的金额口径、销量口径与权限边界（CL-001、CL-002），与 `contract.md` 及 `docs/design/dashboard.md` 一致。
