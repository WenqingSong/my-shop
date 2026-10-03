# Task: 修复 Agent Workflow 中 Cleaner → Owner → Deliverer 的衔接

## Goal

让 Deliverer 在 Cleaner = `CLEAN` 后能够可靠识别「Owner 是否已完成核心逻辑确认」与「里程碑」这两项事实，从而在开始关口满足时直接进入里程碑验收，不再因为缺少可机读的 Owner 确认状态、未解析的 `milestone` 占位符或重复的交付授权而 `BLOCKED`。同时保留 Deliverer 开始关口与 Owner 真实确认，不削弱 Cleaner 质量门槛。

## Scope

- 为 `core-logic.md` 建立稳定、可机读的 Owner Verification 状态模型，状态集最小化为 `NOT_REQUIRED` / `PENDING` / `ACCEPTED`（是否引入 `REJECTED` 仅在确有需求时最小化补充，不得无需求预置）。
- 明确状态归属：Cleaner 生成 `core-logic.md` 时写入初始状态（需要 Owner 验证时为 `PENDING`，无需验证时为 `NOT_REQUIRED`）；`ACCEPTED` 只能由 Owner 的明确动作产生，任何 Agent 不得自行写入。
- 明确 `milestone` 的稳定唯一来源（在任务创建阶段于 `task.md` 中声明，Deliverer 从任务元数据稳定读取），消除对 `<当前里程碑>` 占位符的依赖。
- 修正 Deliverer 的输入契约与开始关口语义：开始关口满足（`CLEAN` + Owner Verification 状态满足 + Owner 主动进入 Deliverer）时直接验收，不再额外要求一份 Delivery Authorization。
- 保持原则：Deliverer 开始关口不被删除；Owner 核心逻辑验证环节不被删除；Cleaner 质量门槛不被削弱。

## Out of Scope

- 不修改任何生产代码、业务测试、历史业务 Contract、`docs/design/*` 业务 Design。
- 不重构整个 Agent Workflow，不改变 Task Builder / Analyst / Coder 的核心职责。
- 不删除 Deliverer，不删除 Owner Core Logic 验证环节，不降低 Deliverer 开始关口。
- 不让 Agent 自动伪造 Owner 已确认，不把 Owner Verification 默认为 `ACCEPTED`。
- 不新增独立 Release Approval Gate（调查确认当前规范中不存在该 Gate，见 Relevant Context）。

## Design Impact

Design Impact: NONE

说明：本任务修改的是 Agent 协同治理（`docs/agent/*` 的角色 Prompt 与协作规范）与任务级 Artifact 模板（`core-logic.md`、`task.md`），不触碰 `docs/design/*` 记录的业务/技术长期事实（数据模型、业务状态机、公开协议、安全边界等）。Owner 原始建议为 `UPDATE`，经核实仓库规范后校正为 `NONE`，证据如下：Design Impact 机制（见 `TaskBuilderPrompt.md`）的落点是 `docs/design/*` 业务设计 Artifact，而本任务的全部产物落在 `docs/agent/*` 治理文档域，二者分属不同 Artifact 域；Owner Verification 状态是治理过程状态，非业务状态机。故无需声明目标 Design Artifact。

## Acceptance Criteria

- [ ] AC-001：Cleaner 生成需要 Owner 验证的 `core-logic.md` 时，文件中包含明确、位置固定、可机读的 `Owner Verification Status: PENDING`。
- [ ] AC-002：不需要 Owner 核心逻辑验证的任务，不产生强制的人工确认步骤；Cleaner 以 `NOT_REQUIRED`（或等价明确表示）记录该事实。
- [ ] AC-003：Owner 明确确认后，能形成 Deliverer 可机读的 `ACCEPTED` 状态。
- [ ] AC-004：Agent（Cleaner / Deliverer / 其它）不得因测试全绿或 `CLEAN` 而自行把 `PENDING` 置为 `ACCEPTED`；`ACCEPTED` 只能由 Owner 的明确动作产生。
- [ ] AC-005：`milestone` 在任务创建阶段（`task.md`）或 Workflow 规范中存在唯一、稳定、可机读的来源，Deliverer 可自动取得。
- [ ] AC-006：Deliverer 的输入与流程不再依赖 `<当前里程碑>` 这类未解析占位符。
- [ ] AC-007：当 `Owner Verification = ACCEPTED` 且 `Cleaner = CLEAN` 时，Deliverer 不再因为「缺少额外交付授权」而 `BLOCKED`。
- [ ] AC-008：Owner 主动进入 Deliverer 阶段被正确解释为「进入里程碑验收」，且规范中不存在独立的 Release Approval Gate 需要再次授权。
- [ ] AC-009：Owner 最多只需做一次核心逻辑确认，不需要重复描述 CL 内容或补 `milestone` 文本。
- [ ] AC-010：Cleaner 的质量门槛（`CLEAN` 条件）不被削弱。
- [ ] AC-011：Deliverer 的开始关口不被删除，仅消除重复授权与不可识别状态。
- [ ] AC-012：无 `core-logic` 的正常任务仍能正常进入 Deliverer，不被额外要求 Owner Confirmation。
- [ ] AC-013：有 `core-logic` 但 Owner 未确认（`PENDING`）时，Deliverer 必须继续 `BLOCKED`。
- [ ] AC-014：本次不修改任何业务代码与业务设计文档（`docs/design/*`）。

## Relevant Context

已核实事实：

- `core-logic.md` 当前定位：由 Cleaner 维护（`AgentCollaborationSpecification.md` §3 表格），且「只有 `CLEAN` 后才填写」（`CleanerAgent.md` §163）；内容是 Owner 验证卡（CL-001/CL-002…，含「需要理解 / 生产代码 / 关键测试 / 基线验证 / 可选 Mutation / 预期失败 / 恢复确认」）。全仓库搜索 `Owner Verification Status` 命中 0 处，即**当前没有任何可机读的 Owner 确认状态字段**，也没有「谁记录 Owner 已确认」的机制。
- 真实样本：`shipping-address-v1/core-logic.md` 与 `iam-v3/core-logic.md` 均已由 Cleaner 填写 CL 卡，但都只有验证卡内容，无 Owner 确认状态；`categories-v1/core-logic.md` 仍为「等待 Cleaner 审查后填写」占位。共 12 个任务存在 `core-logic.md`，格式一致。
- `milestone` 当前来源：`task.md` **没有** `milestone` 字段（抽查 `shipping-address-v1`、`iam-v3` 的 `task.md` 均无）；`DelivererAgent.md` 的输入模板要求外部传入 `milestone: <当前里程碑>`；`<当前里程碑>` 占位符**只出现在 `DelivererAgent.md` 的输入示例中**（2 处），任务文件与 `delivery.md` 中都没有该占位符；`delivery.md` 的 `Milestone` 一节由 Deliverer 手写自由文本。即 milestone 目前是调用方手工补充的参数，任务元数据中没有稳定机读来源。
- Deliverer 授权语义：`DelivererAgent.md` §开始关口要求「Owner 已完成或明确确认核心逻辑验证，并要求进入里程碑验收」；`AgentCollaborationSpecification.md` §10 明确「Owner 决定……是否启动 Deliverer」；§4 默认路由为「Cleaner CLEAN → Owner 核心验证 → Deliverer → Owner 最终决定」。全仓库规范中**不存在独立的 Release Approval Gate**——「Owner 主动调用 Deliverer」本身即进入里程碑验收的语义已隐含在现有规范中，但由于没有可机读的「Owner 已确认」事实，Deliverer 无法自动核实，才会退化为再次向 Owner 索要授权。
- 本次只允许修改 Agent Workflow / Prompt / Artifact 规范，禁止改生产代码与业务设计（见 Out of Scope）。

Assumption：

- `milestone` 的稳定来源建议落在 `task.md`（Task Builder 在创建任务时声明，Deliverer 直接读取），这与 Owner 给出的方向一致；具体字段命名与读取方式由实现阶段固定，不构成本任务的范围扩张。
- `ACCEPTED` 的持久化机制（Owner 直接编辑 `core-logic.md`，或 Owner 指令驱动某角色记录）由实现阶段固定为一种明确、可机读、可追溯的方式；无论哪种，硬约束是「Agent 不得自行写入 `ACCEPTED`」。

OPEN QUESTION（不阻塞）：

- 是否需要正式的 `REJECTED` 状态。Owner 已明确「不要无需求增加状态」，本任务默认不引入；若后续发现「Owner 明确拒绝」必须被持久化才能避免误交付，再由 Analyst 最小化补充。不影响本任务创建。

## Verification

本任务不涉及生产代码，验证以「规范可读性 + 场景推演」为主，必要时用合成任务跑 Deliverer 输入契约：

- AC-001/AC-003/AC-004 → 阅读更新后的 `CleanerAgent.md` 与 `AgentCollaborationSpecification.md`：确认 `core-logic.md` 模板定义了 `Owner Verification Status` 字段、初始值规则（Cleaner 写 `PENDING`/`NOT_REQUIRED`）、以及「`ACCEPTED` 只能由 Owner 动作产生、Agent 禁止自行写入」的硬约束。预期：模板含该字段，且无任何「Cleaner/Coder/Deliverer 可自动置 ACCEPTED」的表述。
- AC-002/AC-012 → 阅读更新后的 Deliverer 开始关口：确认「无 core-logic / `NOT_REQUIRED`」时不要求 Owner Confirmation，可直接进入验收。预期：开始关口只要求 `CLEAN` + Owner Verification 状态满足 + Owner 主动进入 Deliverer。
- AC-005/AC-006 → 阅读 `TaskBuilderPrompt.md` 与 `DelivererAgent.md`：确认 `task.md` 模板新增 `milestone` 字段，Deliverer 输入不再含 `<当前里程碑>`，且 `DelivererAgent.md` 中不再存在该占位符（`grep '<当前里程碑>' docs/agent` 应为空）。
- AC-007/AC-008 → 用合成任务推演：`Cleaner=CLEAN` + `ACCEPTED` + Owner 调用 Deliverer，确认 Deliverer 输入契约不再要求额外授权，开始关口判据可自动满足。预期：Deliverer 直接进入验收而非 `BLOCKED`。
- AC-009 → 阅读更新后的 `core-logic.md` 模板与 Owner 操作路径：Owner 只需确认「接受当前实现」即可形成 `ACCEPTED`，无需重新描述 CL 内容或补 milestone。
- AC-010/AC-011 → 对照 `CleanerAgent.md` 的 `CLEAN` 条件与 `DelivererAgent.md` 的开始关口：确认二者未降低、未被删除，仅去除重复授权与不可识别状态。
- AC-013 → 合成任务推演：`Cleaner=CLEAN` + `PENDING` + Owner 调用 Deliverer，确认 Deliverer 仍 `BLOCKED`（这是正确行为）。
- AC-014 → 确认本次 Diff 仅触及 `docs/agent/*` 与 `.agent/tasks/agent-workflow-owner-confirmation/*`，不含 `docs/design/*`、`api/`、`internal/` 等业务代码。
- 场景推演（对应 Owner 要求）：
  - Scenario A（无 core-logic）→ `NOT_REQUIRED`，Deliverer 直接开始。
  - Scenario B（有 core-logic，PENDING）→ Deliverer `BLOCKED`（正确）。
  - Scenario C（有 core-logic，ACCEPTED）→ Deliverer 开始验收，不再要求补 milestone / 再授权 / 重述 CL。
  - Scenario D（milestone 解析）→ 以 `shipping-address-v1` 为例，Deliverer 从 `task.md` 稳定取到 milestone，无 `<当前里程碑>`。
  - Scenario E（伪造确认）→ Cleaner/Deliverer 不能因全绿/CLEAN 自行 `PENDING→ACCEPTED`。
  - Scenario F（Owner 拒绝）→ `Owner Verification != ACCEPTED` 时 Deliverer 不得继续交付。

## Complexity

NORMAL

原因：不涉及业务架构、数据模型、并发一致性、MQ、权限安全边界或根因不明；根因清晰（缺失可机读 Owner 确认状态 + milestone 无稳定来源 + Deliverer 重复授权）。实现对象是 `docs/agent/*` 治理文档的修改，不存在会产生不同业务/可靠性/运维结果的多种方案。故无需 Analyst，直接交 Coder。

## Review Baseline

- Base commit：`d0c4bb2da85c72f7eacac14a318167e103686deb`（分支 `develop`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务仅改动 `docs/agent/*`（角色 Prompt 与协作/职责规范）并新增 `.agent/tasks/agent-workflow-owner-confirmation/*` 任务级文件；不触碰生产代码与 `docs/design/*`。当前工作区干净，无既有未提交修改。

## Initial Route

READY_FOR_CODER
