# Technical Contract

## Decision Status

APPROVED

## Problem

Deliverer 在 `Cleaner = CLEAN` 后无法机读「Owner 是否已完成核心逻辑确认」与「里程碑」两项事实，只能再次向 Owner 索要交付授权，导致 `BLOCKED`。根因有三：`core-logic.md` 缺少可机读的 Owner Verification 状态字段；`milestone` 无稳定来源（`<当前里程碑>` 仅存在于 Deliverer 输入示例，任务元数据无此字段）；Deliverer 开始关口把「Owner 已完成或明确确认核心逻辑验证」写成无法自动核实的判据，退化为重复授权。本任务只改 Agent 协同治理（`docs/agent/*`）与任务级模板（`core-logic.md`、`task.md`），不触碰生产代码与 `docs/design/*`。

## Verified Current Behavior

- VERIFIED：`core-logic.md` 由 Cleaner 维护（`AgentCollaborationSpecification.md` §3 表格），且「只有 `CLEAN` 后才填写」（`CleanerAgent.md` §163）。其模板只有 CL 验证卡（§169-179），**无任何 Owner Verification 状态字段**。全仓库 `Owner Verification Status` 命中 0 处。
- VERIFIED：真实样本 `shipping-address-v1/core-logic.md`、`iam-v3/core-logic.md` 均已由 Cleaner 填写 CL 卡，但无 Owner 确认状态；`categories-v1/core-logic.md` 仍为占位。`.agent/tasks/` 下除本任务外 12 个任务均存在 `core-logic.md`。
- VERIFIED：`<当前里程碑>` 占位符仅出现在 `docs/agent/models/DelivererAgent.md`（第 12、21 行，共 2 处）；`docs/agent` 其余文件无该占位符。
- VERIFIED：`milestone` 在 `.agent/tasks` 中只出现在各任务的 `delivery.md`（Deliverer 手写自由文本，如 `shipping-address-v1/delivery.md` 第 5 行）；所有 `task.md` 均无 `milestone` 字段。
- VERIFIED：全仓库不存在独立 Release Approval Gate（`Release Approval|Release Gate|交付授权|Delivery Authorization` 命中 0 处）；`AgentCollaborationSpecification.md` §4 路由为「Cleaner CLEAN → Owner 核心验证 → Deliverer → Owner 最终决定」，§10 明确 Owner 决定是否启动 Deliverer。
- VERIFIED：Deliverer 开始关口（`DelivererAgent.md` §开始关口）含「Owner 已完成或明确确认核心逻辑验证，并要求进入里程碑验收」，但无对应机读事实可核实。
- VERIFIED：`Design Impact = NONE` 判断正确——本任务产物落在 `docs/agent/*` 治理文档域，非 `docs/design/*` 业务设计域；Owner Verification 状态是治理过程状态，非业务状态机。

无影响设计的 UNKNOWN。

## Recommendation

RECOMMENDATION：

1. **Owner Verification 状态模型**：在 `core-logic.md` 模板固定位置（顶部、CL 卡之前）新增单行机读字段 `Owner Verification Status: <NOT_REQUIRED | PENDING | ACCEPTED>`。状态集最小化为三态，不引入 `REJECTED`（除非后续确需）。
   - 初始状态由 Cleaner 写入：本次产生 ≥1 张 CL 卡 → `PENDING`；无 core-logic（0 张 CL 卡）→ `NOT_REQUIRED`。
   - `ACCEPTED` 只能由 Owner 的明确动作产生；任何 Agent 不得因测试全绿或 `CLEAN` 自行写入。
2. **`ACCEPTED` 持久化机制（推荐 Owner 直接编辑）**：Owner 亲自把 `core-logic.md` 中的 `Owner Verification Status` 从 `PENDING` 改为 `ACCEPTED`。这是最防伪、最可追溯的方式——任何 Agent 在任何情况下都不写 `ACCEPTED`，与「Agent 不得自行写入」零歧义。
3. **`milestone` 稳定来源**：在 `task.md` 模板新增 `Milestone` 字段，由 Task Builder 在任务创建阶段声明；Deliverer 从任务元数据稳定读取。删除 `DelivererAgent.md` 输入模板中的 `<当前里程碑>` 占位符（第 12、21 行），`delivery.md` 的 Milestone 一节仍由 Deliverer 写入（内容取自 `task.md`）。
4. **Deliverer 开始关口语义**：开始关口 = `Cleaner = CLEAN` 且无开放 P0/P1/P2 + `Owner Verification Status ∈ {NOT_REQUIRED, ACCEPTED}` + Owner 主动调用 Deliverer（即进入里程碑验收）+ 复杂任务 Contract `APPROVED` + 版本与 Review Target 一致 + Mutation 已恢复。**移除「额外交付授权」**，不新增独立 Release Approval Gate。`PENDING` 时必然 `BLOCKED`（正确行为，AC-013）。

关键取舍：`ACCEPTED` 机制选「Owner 直接编辑」而非「Owner 指令驱动某角色代写」。前者防伪与可追溯最强、实现最简，代价是 Owner 需手动定位并编辑该字段（操作成本略高）；后者对 Owner 更省事（口述即可），但必须额外写入一条规则区分「Owner 明确指令」与「Agent 自决」，防伪保证略弱。这是唯一需要 Owner 拍板的取舍。

Owner 已就上述取舍作出决定：采用「Owner 明确指令驱动 Agent 机械代写」，详见 Selected Design 与 Owner Decision Record。

## Selected Design

Owner 已确认，采用「Owner 明确指令驱动 Agent 机械代写」方案（不采用 Owner 直接编辑 `core-logic.md`）：

1. **Owner Verification 状态模型**：`core-logic.md` 模板顶部（CL 卡之前）新增单行机读字段 `Owner Verification Status: <NOT_REQUIRED | PENDING | ACCEPTED>`，状态集最小化三态，不引入 `REJECTED`。
2. **状态归属**：`core-logic.md` 继续由 Cleaner 维护。
   - 有 CL 验证卡 → Cleaner 初始写 `PENDING`；
   - 无需 Owner 核心验证（0 张 CL 卡）→ Cleaner 写 `NOT_REQUIRED`；
   - `ACCEPTED`：仅当收到 Owner 明确的确认/接受指令后，Cleaner 才允许机械记录为 `ACCEPTED`；任何 Agent 不得因 `CLEAN`、测试通过、Owner 阅读过文件或其它间接信号自行写入。
3. **Owner 确认操作**：Owner 的正常操作压缩为一句明确确认，例如 `确认 core-logic.md 中 CL-001、CL-002，接受当前实现。`，不要求 Owner 手工编辑文件、不重复描述核心逻辑、不重写 Delivery Authorization。
4. **`milestone` 来源**：唯一来源为 `task.md` 的 `Milestone` 字段（Task Builder 在创建阶段声明）；删除 `DelivererAgent.md` 输入模板中的 `<当前里程碑>` 占位符。
5. **Deliverer 开始关口**：`Cleaner = CLEAN` 且无开放 P0/P1/P2 + `Owner Verification Status ∈ {NOT_REQUIRED, ACCEPTED}` + Owner 主动进入 Deliverer + 复杂任务 Contract `APPROVED` + 版本与 Review Target 一致 + Mutation 已恢复 → 直接开始交付验收，不再要求额外授权。`PENDING` 时必然 `BLOCKED`。

## Interfaces and Data

- `core-logic.md` 模板（`CleanerAgent.md`）：顶部新增固定单行 `Owner Verification Status: <PENDING | NOT_REQUIRED>`；Cleaner 按「是否产生 CL 卡」写初始值；`ACCEPTED` 仅由 Owner 明确指令驱动 Cleaner 机械记录，禁止任何 Agent 自行写入。
- `task.md` 模板（`TaskBuilderPrompt.md`）：新增 `Milestone` 字段，Task Builder 在创建阶段声明；涉及 Deliverer 的任务必填。
- `DelivererAgent.md`：输入模板删除 `milestone: <当前里程碑>` 两处；开始关口改写为可机读判据（见 Selected Design），不再要求额外交付授权。
- `AgentCollaborationSpecification.md`：补一句「Owner Verification 状态产生规则与防伪约束」作为跨角色规范：`PENDING`/`NOT_REQUIRED` 由 Cleaner 初始写入，`ACCEPTED` 仅由 Owner 明确指令驱动 Cleaner 记录，Agent 禁止自行写入。

## Business Invariants

- INV-001：`ACCEPTED` 只能由 Owner 的明确确认/接受指令驱动 Cleaner 机械记录；任何 Agent 不得因 `CLEAN`、测试通过、Owner 阅读过文件或其它间接信号自行把 `PENDING` 置为 `ACCEPTED`。
- INV-002：Deliverer 开始关口在 `Owner Verification Status = PENDING` 时必然 `BLOCKED`；`NOT_REQUIRED` 或 `ACCEPTED`（且 `CLEAN` + Owner 主动进入 Deliverer）时直接进入里程碑验收。
- INV-003：`milestone` 有且仅有一个稳定机读来源（`task.md` 的 `Milestone` 字段）；`docs/agent/` 中不再存在 `<当前里程碑>` 占位符。

## Allowed / Forbidden Changes

- 允许：修改 `docs/agent/models/{CleanerAgent,DelivererAgent,TaskBuilderPrompt}.md` 与 `docs/agent/AgentCollaborationSpecification.md`；新增/更新 `.agent/tasks/agent-workflow-owner-confirmation/*`；更新 `core-logic.md`、`task.md` 模板。
- 禁止：修改任何生产代码、业务测试、`docs/design/*`、历史业务 Contract；删除 Deliverer 开始关口或 Owner 核心逻辑验证环节；削弱 Cleaner `CLEAN` 门槛；无需求预置 `REJECTED` 状态；任何 Agent 自行写 `ACCEPTED`。

## Verification Requirements

- INV-001 → 阅读更新后的 `CleanerAgent.md` 与 `AgentCollaborationSpecification.md`：模板含 `Owner Verification Status` 字段、初始值规则（Cleaner 写 `PENDING`/`NOT_REQUIRED`）、`ACCEPTED` 仅由 Owner 明确指令驱动 Cleaner 记录、禁止因 `CLEAN`/测试/间接信号自行写入；全文无「Agent 可自动置 ACCEPTED」表述（对应 AC-001/003/004）。
- INV-002 → 场景推演：`CLEAN` + `PENDING` + Owner 调用 Deliverer → 必须 `BLOCKED`（AC-013）；`CLEAN` + `ACCEPTED` → 直接验收、不索要额外授权（AC-007）；无 core-logic / `NOT_REQUIRED` → 不要求 Owner Confirmation（AC-002/012）。
- INV-003 → `grep '<当前里程碑>' docs/agent` 结果为空；`task.md` 模板含 `Milestone` 字段，Deliverer 可自动读取（AC-005/006）。
- 其余 AC → 按 `task.md` Verification 节逐条核对，尤其 AC-009（Owner 最多一次确认）、AC-010/011（`CLEAN` 门槛与开始关口不削弱）、AC-014（Diff 不含 `docs/design/*` 与生产代码）。

## Open Risks

- `REJECTED` 状态未引入：若后续发现「Owner 明确拒绝」必须持久化才能避免误交付，再由 Analyst 最小化补充，不属本任务。
- 已确认「指令代写」机制：需在 `CleanerAgent.md` 明确「记录 `ACCEPTED` 仅限 Owner 明确确认/接受指令」的判定规则，并配套防伪验证（对应 INV-001）。

## Owner Decision Record

Owner 于 2026-10-04 确认以下决定：

1. **`ACCEPTED` 持久化机制**：采用「Owner 明确指令驱动 Agent 机械代写」，不采用 Analyst 推荐的「Owner 直接编辑 `core-logic.md`」。理由是 Owner 的职责是决策与确认，不应承担 Agent Artifact 手工维护；否则只是把「手写 Delivery Authorization Prompt」换成「手改 Markdown」，未消除流程人工接缝。
2. **状态归属**：`core-logic.md` 继续由 Cleaner 维护；有 CL 卡初始写 `PENDING`，无 Owner 核心验证写 `NOT_REQUIRED`；仅收到 Owner 明确确认/接受指令后，Cleaner 才机械记录 `ACCEPTED`。
3. **防伪约束**：Cleaner 不得因 `CLEAN`、测试通过、Owner 阅读过文件或其它间接信号自动写 `ACCEPTED`。
4. **Owner 操作**：Owner 用一句明确确认（如 `确认 core-logic.md 中 CL-001、CL-002，接受当前实现。`）即可，不手工编辑文件、不重复描述核心逻辑、不重写 Delivery Authorization。
5. **`milestone`**：唯一来源保持 `task.md`；Deliverer 不再接受或依赖 `<当前里程碑>` 占位符。
6. **Deliverer 开始关口**：`CLEAN` + `NOT_REQUIRED/ACCEPTED` + Owner 主动进入 Deliverer → 直接开始验收，不再要求额外授权。

适用范围：本任务（Agent Workflow Cleaner→Owner→Deliverer 衔接修复），`Design Impact = NONE`，不写 `docs/design/*`。关键问题均已解决，Contract 标记 `APPROVED`，可交 Coder 实现。
