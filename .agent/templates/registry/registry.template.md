# 全局 <RESOURCE_KIND> Registry 模板

> Adoption 用法：将本模板复制为 `.agent/registry/<kind>.md`（`<kind>` 为合法 `lower_snake_case`，须与 `.agent/workflow.yaml` 中声明的 resource kind 一致），填写本 kind 的分配规则后使用。初始分配表必须为空，不得携带任何真实分配行。

本文件是 **<RESOURCE_KIND> 分配**的权威事实源（位于共享集成分支上的 `.agent/registry/`）。任何需要新增该资源的 Task，必须在进入 Coder 前，通过一个只改本 Registry 文件的 commit 把 `RESERVED` 条目提交到共享集成分支，才能使用对应资源；仅在 Feature Branch 内自行声明不构成有效预留。

机制、生命周期与角色职责见 `.agent/specs/AgentCollaborationSpecification.md`（跨任务全局资源预留）与 `.agent/specs/Five-AgentResponsibilityBoundary.md`。

> 说明：`migration_version` / `error_code_domain` 只是宿主项目可能声明的 resource kind 示例，不是 Workflow Core built-in 资源类型。Workflow Core 只认识 Generic Shared Resource Reservation（`Value / Owner / Status`），具体 kind 由宿主项目通过 `.agent/workflow.yaml` 声明。

## 状态与生命周期

- `RESERVED`：Reservation 已通过「只改 Registry 的 commit」落到共享集成分支生效，但拥有该资源的 Feature 尚未合并。
- `ACTIVE`：已合并进共享集成分支，资源实际生效（终态）。
- `RELEASED`：Task 取消释放（记录保留）。

转换：`RESERVED → ACTIVE`（feature 合并进共享集成分支时由合并任务同步）；`RESERVED → RELEASED`（Task 取消时由 Analyst/Owner 标记）。

> 复用规则由本 resource kind 的 Project Policy 决定（Adoption 时明确），Workflow Core 不承载具体业务复用语义。

## 分配规则

> Adoption 时填写本 kind 的具体分配规则（值格式、`next` 派生规则、区间/复用策略等）。

## 分配表

| 值 | 拥有方（任务） | 状态 | 备注 |
| --- | --- | --- | --- |

> 列协议（Workflow Core 解析约定）：第 1 列 = Value、倒数第 3 列 = Owner、倒数第 2 列 = Status，最后一列 = 备注。可在 Owner 与 Status 之间插入 kind 专属的中间列（例如 `title`），但核心三列（Value / Owner / Status）必须保留。初始为空。
