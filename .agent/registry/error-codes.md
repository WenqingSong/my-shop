# 全局错误码域 Registry

本文件是**错误码域分配**的权威事实源（`.agent/registry/`，位于 `develop` 上）。任何需要新增错误码域的 Task，必须在进入 Coder 前，通过一个只改 Registry 文件的 commit 把 `RESERVED` 条目提交到 `develop`，才能使用对应域；仅在 Feature Branch 内自行声明不构成有效预留。

机制、生命周期与角色职责见 `docs/agent/AgentCollaborationSpecification.md`（跨任务全局资源预留）与 `docs/agent/Five-AgentResponsibilityBoundary.md`。

## 状态与生命周期

- `RESERVED`：Reservation 已通过「只改 Registry 的 commit」落到共享 `develop` 生效，但拥有该资源的 Feature 尚未合并进 `develop`。
- `ACTIVE`：已合并进 `develop`，资源在 `develop` 实际生效（终态）。
- `RELEASED`：Task 取消释放（记录保留）。

转换：`RESERVED → ACTIVE`（feature 合并进 `develop` 时由合并任务同步）；`RESERVED → RELEASED`（Task 取消时由 Analyst/Owner 标记）。

复用规则：错误码域仅在**纯 `RESERVED`** 阶段（尚未 APPROVED Contract 落地、尚未实现、尚未 merge）可 `RELEASE` 后复用；一旦进入实现或已合并，即**不可复用**。

## 分配规则

- 域为固定大小 1000、按 1000 对齐的连续整数区间：域序 `domain_seq = code / 1000`，域区间 `[domain_seq × 1000, domain_seq × 1000 + 999]`。
- 下一个空闲域：`domain_seq_next = max(已记录域序) + 1`，区间 `[domain_seq_next × 1000, domain_seq_next × 1000 + 999]`。
- 域序为正整数、不受现有四位数宽度限制，可按正整数域序继续扩展（`9000-9999` = 序 9、`10000-10999` = 序 10……）。
- 域内具体编号由 Analyst 在 Contract 中逐个列出（域本身独占，域内编号无跨任务冲突）。
- `next` 由规则派生，不维护易腐的显式 next 指针。

## 分配表

| 域区间 | 拥有方（任务/模块） | 状态 | 备注 |
| --- | --- | --- | --- |
| 1000-1999 | 通用 | ACTIVE | 基线 |
| 2000-2999 | IAM | ACTIVE | 基线 |
| 3000-3999 | category | ACTIVE | 基线 |
| 4000-4999 | product | ACTIVE | 基线 |
| 5000-5999 | SKU | ACTIVE | 基线 |
| 6000-6999 | inventory | ACTIVE | 基线 |
| 7000-7999 | address | ACTIVE | 基线 |
| 8000-8999 | cart | ACTIVE | 基线 |
| 9000-9999 | order | ACTIVE | order-v1 订单域（域序 9 = max(8)+1） |
| 10000-10999 | review | RESERVED | product-review-v1 评价域（域序 10 = max(9)+1） |
