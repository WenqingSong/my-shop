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

### D4 继续使用既有任务分支与目录，不新建 `-v1`

- Decision: 任务 slug 仍为 `surgecart-baseline-audit`，分支仍为 `task/surgecart-baseline-audit`，目录仍为 `.agent/tasks/surgecart-baseline-audit/`；不创建 `task/surgecart-baseline-audit-v1`，不新建 `.agent/tasks/surgecart-baseline-audit-v1/`。
- Decided by: LEAD（依 V4 记录冲突处理与 Git 历史保护；冲突点已记录，供 Owner 复核）
- Decided at: 2026-11-06T02:33:05Z
- Rationale（Git 事实）:
  - 本次指令要求的分支名 `task/surgecart-baseline-audit-v1` 在本地与远端均不存在（`git ls-remote origin refs/heads/task/surgecart-baseline-audit-v1` 为空）。
  - 但 `.agent/tasks/surgecart-baseline-audit-v1/` **已存在且已被 Git 跟踪**（7 个文件，V3 时代记录），`docs/reports/surgecart-baseline-audit-v1/` 也已存在；同名新建会覆盖既有有效证据，违反本任务「不得覆盖已有有效记录」。
  - 既有分支 `task/surgecart-baseline-audit` 由上一个 Session 依 Owner 授权创建（D1、D3），本地与远端同为 `95c1391`，其任务目录 `task.md` 的 Goal 与本次 T01 目标一致，计划处于 PLANNING，尚无审计结论可被覆盖。
  - 记录 `D3` 明确记录 Owner 硬约束「正式任务不复用 `-v2`/`-v1` 后缀」，且「复用 `-v1` 目录属既有历史任务，不可覆盖」。
- Alternatives:
  - 新建 `task/surgecart-baseline-audit-v1` 分支并把记录写入 `-v1` 目录 → 覆盖历史任务文件，违反历史保护；且与已推送的 `task/surgecart-baseline-audit` 形成两个并存任务记录。
  - 在现有分支上另建 `-v1` 目录 → 同一分支两个任务目录，事实来源分裂。
- Impact: 本次交付的分支名为 `task/surgecart-baseline-audit`（非指令中的 `-v1`）。若 Owner 坚持 `-v1` 命名，需要一次显式的改名/迁移授权（涉及已推送分支与历史目录），由 Owner 决定后另行执行。
- Owner 复核状态: 待 Owner 在验收时确认或否决（本条不冒充 Owner 决策）。

### D5 审计锚点取任务分支 HEAD `95c1391`

- Decision: Review Baseline 记录为 `95c13910c7226b4e32487ed046702caf75d74fc6`，并同时给出源码等价基线 `473d6f5253e3abcb98ece9d79a5a8059563a3e8d`。
- Decided by: LEAD
- Decided at: 2026-11-06T02:33:05Z
- Rationale: Git 生命周期要求结论绑定可核验 commit；审计对象是当前检出的代码。`git diff --stat 473d6f5..95c1391` 仅 6 个任务记录文件，`internal/`、`api/`、`test/`、`go.mod`、`Dockerfile`、`.cnb.yml`、`prometheus/` 无差异，故两个 SHA 的源码事实相同。
- Alternatives: 仅记录 `473d6f5` → 与「审计时 HEAD」不一致，交付物无法直接对应 `git rev-parse HEAD`。
- Impact: 所有报告结论对两 SHA 同时成立；后续代码变更需重新锚定。

### D6 独立审查不可得，如实披露

- Decision: 本次不声称已通过独立审查；`findings.md` 的 Review Conclusion 记 `NOT_REVIEWED`，`delivery.md` §1 与 §4 披露「审查为作者自审，独立性未满足」。
- Decided by: LEAD
- Decided at: 2026-11-06T02:33:05Z
- Rationale: 本 Session 内报告作者即 LEAD；平台虽提供只读 code-explorer Subagent，但其不具备「未参与实现」之外的审查权威，且本次审计为单 Agent 完成，无法构造真实的作者/审查者分离。V4 明确要求不得伪造独立审查。
- Alternatives: 用 Subagent 形式跑一遍并标 CLEAN → 属形式化审查，违反 `AgentCollaborationSpecification.md` §4 与真实性要求。
- Impact: 交付可被 Owner 接受为审计事实，但「独立审查」一项未满足；需 Owner 指派新 Session / 外部审查者补做。

## 待决

### Q1 任务与分支命名是否必须改为 `-v1`

- 背景: 本次 Owner 指令写 `surgecart-baseline-audit-v1` / `task/surgecart-baseline-audit-v1`；仓库现状是既有 `task/surgecart-baseline-audit` 分支（已推送）与历史 `-v1` 目录/报告（已交付）。
- 选项:
  - A（本次执行）: 沿用 `task/surgecart-baseline-audit`，不做改名 → 无覆盖风险，交付可直接验收；与指令中的分支名不一致。
  - B: 新建 `task/surgecart-baseline-audit-v1` 并把记录迁入新目录 → 与历史 `-v1` 目录命名冲突，需要先决定历史目录去留（涉及删除/移动他人记录，需 Owner 授权）。
  - C: 仅把分支改名（`git branch -m`）并推送新分支、保留旧分支 → 会留下两个分支指向同一提交，且远端旧分支需删除（需 Owner 授权）。
- 推荐: A。理由：不破坏历史、与已推送分支一致、交付可立即验收；命名差异不影响审计事实。
- 如果不决定: 交付按 A 执行，Owner 验收时可要求改名，但改名需要新的授权操作。
- Status: PENDING
