# 全局 Migration Version Registry

本文件是 **migration version 分配**的权威事实源（`.agent/registry/`，位于 `develop` 上）。任何需要新增 migration 的 Task，必须在进入 Coder 前，通过一个只改 Registry 文件的 commit 把 `RESERVED` 条目提交到 `develop`，才能使用对应 version；仅在 Feature Branch 内自行声明不构成有效预留。

机制、生命周期与角色职责见 `.agent/specs/AgentCollaborationSpecification.md`（跨任务全局资源预留）与 `.agent/specs/Five-AgentResponsibilityBoundary.md`。

## 状态与生命周期

- `RESERVED`：Reservation 已通过「只改 Registry 的 commit」落到共享 `develop` 生效，但拥有该资源的 Feature 尚未合并进 `develop`。
- `ACTIVE`：已合并进 `develop`，资源在 `develop` 实际生效（终态）。
- `RELEASED`：Task 取消释放（记录保留）。

转换：`RESERVED → ACTIVE`（feature 合并进 `develop` 时由合并任务同步）；`RESERVED → RELEASED`（Task 取消时由 Analyst/Owner 标记）。

复用规则：migration version 一旦被 Reservation 分配，即使 Task 后续取消也永久保留 tombstone、**不得复用**；`next` 恒为 `max(所有已记录 version, 含 RELEASED) + 1`。

## 分配规则

- 保留现有 `YYYYMMDD + 序号` 的实际格式，`next = max(所有已记录 version, 含 RELEASED) + 1`。
- **禁止 Coder 根据当前 migration 最大值自行 `max+1`**。

## 分配表

| version | title | 拥有方（任务） | 状态 | 备注 |
| --- | --- | --- | --- | --- |
| 20261001000001 | baseline | db-migration | ACTIVE | 基线 |
| 20261001000002 | products | db-migration | ACTIVE | 基线 |
| 20261001000003 | skus | db-migration | ACTIVE | 基线 |
| 20261001000004 | inventory | db-migration | ACTIVE | 基线 |
| 20261001000005 | addresses | db-migration | ACTIVE | 基线 |
| 20261001000006 | cart_items | db-migration | ACTIVE | 基线 |
| 20261001000007 | orders | order-v1 | ACTIVE | orders + order_items |
| 20261001000008 | refresh_tokens | iam-v4 | ACTIVE | IAM V4 refresh token 表（前台用户域） |
| 20261001000009 | reviews | product-review-v1 | RESERVED | reviews 评价表 |
| 20261001000010 | favorites | product-favorite-v1 | RESERVED | favorites 收藏表 |
| 20261001000011 | flash_sale | flash-sale-v1 | RESERVED | flash_sale_activities + flash_sale_activity_skus + flash_sale_orders |
| 20261001000012 | product_view_count | product-view-count-v1 | RESERVED | products.view_count 浏览量计数字段 |
| 20261001000013 | product_likes | product-like-v1 | RESERVED | product_likes 点赞表 |
| 20261001000014 | banners | banner-v1 | RESERVED | banners 轮播图表 |
| 20261001000015 | flash_sale_order_requests | flash-sale-v3 | RESERVED | flash_sale_order_requests 异步下单请求表 |
