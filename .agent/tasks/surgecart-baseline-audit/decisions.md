# Decisions — SurgeCart 秒杀链路工程基线审计

> 记录已发生的决策与待决问题。Owner 的决策必须由 Lead 誊录，未誊录视为未发生。

## 已决

### D1 撤销上一轮 `surgecart-baseline-audit-v2`，重建正式任务

- Decision: 删除 `task/surgecart-baseline-audit-v2` 的本地分支、远端分支及 `.agent/tasks/surgecart-baseline-audit-v2/`，以无后缀名称重建任务。
- Decided by: Owner
- Decided at: 2026-11-06T01:57:32Z
- Rationale: Owner 要求「之前的 V2 开发全部撤销，重新从头执行」，且正式名称不得使用 `-v2`、`-v3` 等后缀。
- 撤销前核实（安全删除依据）:
  - V2 相对 `473d6f5` 只有两个提交：`269c780 chore(task): 建立 surgecart-baseline-audit-v2 任务记录`、`d9c505c chore(task): 补记 ... SHA`。
  - `git diff --stat 473d6f5..task/surgecart-baseline-audit-v2` 只有 6 个新增文件，全部位于 `.agent/tasks/surgecart-baseline-audit-v2/`，无业务代码、无构建脚本、无 CI 改动。
  - `git branch -a --contains 269c780` 只返回 V2 自身及其远端跟踪分支，说明未进入 `develop` / `main` 或任何其他分支。
  - `.agent/tasks/surgecart-baseline-audit-v2/` 不在 `develop` 上，删除分支即完全移除该目录。
  - 删除后 `git ls-remote --heads origin refs/heads/task/surgecart-baseline-audit-v2` 为空，本地 `git rev-parse --verify` 失败。
- Alternatives: 保留 V2 分支仅新建正式任务 → 与 Owner「全部撤销」指令冲突，且留下两套并行任务记录；把 V2 改名 → 会重写已推送分支历史。
- Impact: V2 的两个提交不再被任何分支引用，仅可能通过 reflog 找回；其任务定义、计划、决策一律不继承。

### D2 基线取 `develop` 的 `473d6f5`，不取 `main`

- Decision: 以 `develop` 最新提交 `473d6f5253e3abcb98ece9d79a5a8059563a3e8d` 作为本任务起点。
- Decided by: TASK-BUILDER（Owner 授权自行核实基线，此处只记录 Git 依据）
- Decided at: 2026-11-06T01:57:32Z
- Rationale:
  - `develop` 最新提交时间 2026-11-05，`main` 最新提交时间 2026-10-14；`git rev-list --count origin/main..origin/develop` = 29。
  - 历史任务分支合并进入 `develop`：`a01a626 合并 task/surgecart-baseline-audit-v1`、`f7b7a8d`、`49430ab`；`main` 头部提交为「合并来自 develop 的合并请求」，处于下游。
  - `main` 独有提交 14 个，其中非合并提交只有 `d96bd28 docs(workflow): 创建后台订单查询管理 V1 任务定义`，仅涉及 `.agent/tasks/admin-order-management/`，不含业务代码；`develop` 另有 `main` 未包含的 `95344f5 feat(inventory): 库存流水查询支持分页并补充边界测试`。
- Alternatives: 以 `main` 为基线 → 审计的是 2026-10-14 的代码，缺少 29 个集成提交（含 Workflow 4.0.0 与 v1 审计报告），与「当前真实基线」目标相反。
- Impact: 报告中所有结论只对 `473d6f5` 成立；后续代码变更须重新锚定。

### D3 任务命名不使用版本后缀

- Decision: Task Slug `surgecart-baseline-audit`，分支 `task/surgecart-baseline-audit`，目录 `.agent/tasks/surgecart-baseline-audit/`。
- Decided by: Owner（硬约束）
- Decided at: 2026-11-06T01:57:32Z
- Rationale: 正式任务不复用 `-v2` 后缀；执行前核实该目录、远端同名分支、`docs/reports/surgecart-baseline-audit/` 均不存在，无命名冲突。
- Alternatives: 复用 `surgecart-baseline-audit-v1` 目录 → 属既有历史任务，不可覆盖。
- Impact: 后续引用一律使用无后缀名称。

## 待决

- 暂无。
