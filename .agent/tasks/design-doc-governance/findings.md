# Cleaner Findings

## Review Target

- 任务：`.agent/tasks/design-doc-governance/task.md`（Agent Workflow 治理，`Design Impact` 对本任务而言为 `NONE`，交付物是 `docs/agent/*` 公共规范与角色 Prompt，不产生 `docs/design/*`）
- Base commit：`eef0911c1cfc2939d5115b689650229bc0d98197`（分支 `develop`，任务基线）
- 当前 HEAD：`34dae87251dbc4a6ebf9785cba4664c20d31f91c`（`docs(agent): 建立长期 Design 文档治理机制`）
- 工作区状态：`git status --short` 为空（clean），无新增未跟踪文件，无任务基线前的既有修改需区分
- 变更范围（`git diff eef0911..HEAD --name-status`）：
  - 新增 `.agent/tasks/design-doc-governance/` 五个任务级文件（`task.md` / `contract.md` / `findings.md` / `core-logic.md` / `delivery.md`）
  - 修改 `agents.md`（根目录，最小说明性修改）
  - 修改 `docs/agent/AgentCollaborationSpecification.md`、`docs/agent/Five-AgentResponsibilityBoundary.md`
  - 修改 `docs/agent/models/{TaskBuilderPrompt,Analyst,Coder,Cleaner,Deliverer}Agent.md`
- 复核方式：全部变更集中在单个 commit `34dae87`，相对 `eef0911` 无生产代码（`.go`）、无业务测试、无 `docs/design/iam.md`、无历史任务目录、无业务模块 Contract 的改动，与 Contract 的 Allowed/Forbidden Changes 一致。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `AgentCollaborationSpecification.md` 第 3 节改名「任务文件与长期 Design」，第 27 行显式区分「任务级 Artifact」与「项目级长期 Design」；第 50–63 行定义 `docs/design/*` 的用途、结构与「生命周期与维护者」表（Analyst 主责、Cleaner 校验、Coder/Deliverer 不写）；`Five-AgentResponsibilityBoundary.md` 第 62/83/104/113/126 行把长期 Design 职责落到各角色。原有「关键设计只能沉淀进 contract.md、长期设计落点无定义」缺口已消除。 |
| AC-002 | PASS | `TaskBuilderPrompt.md` 新增「判断 Design Impact」小节，给出 `NONE`/`UPDATE`/`NEW` 三态与触发清单；第 172 行明确「`NONE` 不要求 Design，也不因任务为 `COMPLEX` 就机械要求 `NEW`」。 |
| AC-003 | PASS | `TaskBuilderPrompt.md` 模板第 81–87 行新增「Design Impact」声明段（`Design Impact: NEW/UPDATE/NONE` + `Design Artifact: <目标 docs/design/* 文件或设计域>`），并写「`NEW/UPDATE` 时目标 Design Artifact 必须纳入 Scope / Deliverables / AC」；第 172 行重复强调。 |
| AC-004 | PASS | `AnalystAgent.md` 新增「长期 Design」小节（第 175–184 行）+「Owner 确认」第 5 步（第 170 行），明确「仅 Owner `APPROVED` 后、Coder 实现前新增/更新」；「只能写入 APPROVED 事实，不得私自新增未批准设计」；「Contract Revision 重新 APPROVED 后必须同步受影响 Design」。`Five-AgentResponsibilityBoundary.md` 第 62/64 行同义。 |
| AC-005 | PASS | `CoderAgent.md` 第 61 行与 `Five-AgentResponsibilityBoundary.md` 第 83 行：Coder「不是长期 Design 所有者、按 Contract 实现、不得以实现便利擅自改变长期架构事实、冲突走 `CONTRACT_REVISION`」。 |
| AC-006 | PASS | `CleanerAgent.md` 成功标准/审查方法/CLEAN 条件均纳入「四者一致」；`BLOCKED` 覆盖「Design Artifact 缺失、Contract Revision 后未同步」；`CHANGES_REQUIRED` 覆盖「Design 与 APPROVED Contract/实现漂移」；`Five-AgentResponsibilityBoundary.md` 第 113 行同义。见 Finding CLEAN-001 关于第 4 个反例的措辞。 |
| AC-007 | PASS | `TaskBuilderPrompt.md` 第 170/172 行 `NONE` 清单（Bugfix/纯测试补充/格式整理/局部优化）且「`NONE` 不要求 Design」；`AgentCollaborationSpecification.md` 第 63 行、`CleanerAgent.md` 第 161 行、`AnalystAgent.md` 第 184 行均复述「NONE 不要求 Design」。 |
| AC-008 | PASS | 术语 `Design Impact` / `Design Artifact` / `长期 Design` 全仓一致（grep 37 处无混用）；职责流转线性（Task Builder 判定→Analyst 写 Design→Coder 实现→Cleaner 校验→Deliverer 确认），无循环依赖或角色矛盾。 |
| AC-009 | PASS | `docs/design/iam.md` 零 diff（未被触碰）；`AnalystAgent.md` 第 184 行「既有已正确沉淀的 Design 不因职责调整回溯改写」保护 IAM 既有流程；`TaskBuilderPrompt.md` 判定清单中「新增业务模块（新表/新实体/新模块边界）→NEW」「修改状态机→UPDATE」使重建 Product/SKU/Inventory 任务可正确识别。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| 变更范围合规 | PASS | `git diff eef0911..HEAD --name-status`：仅 8 个规范/Prompt 文件 + 本任务 5 个任务级文件；无 `.go`、无业务测试、无 `docs/design/iam.md`、无历史任务/业务 Contract。 |
| `docs/design/` 未新建回填 | PASS | `docs/design/` 仍仅含 `iam.md`，符合 Out of Scope「不偿还历史 Design Debt」。 |
| 术语一致性 | PASS | grep `Design Impact`/`Design Artifact`/`长期 Design`/`docs/design` 全仓 37 处，术语统一，无 `contract.md 或设计` 旧表述残留。 |
| 四者一致（Task↔Contract↔docs↔实现） | PASS | Owner 冻结决定（前置事实缺失→`BLOCKED`；已有 Design 漂移→`CHANGES_REQUIRED`；`NONE` 不要求 Design）在 `CleanerAgent.md`/`Five-AgentResponsibilityBoundary.md` 中一致落定，无漂移。 |
| 运行验证 | NOT_EXECUTED | 本任务为 Agent Workflow 治理任务，`task.md` Verification 明确「不适用 `go test ./...`」，采用静态一致性 + 场景逻辑审查（见 AC 证据）。场景 A–E 的判定/阻塞逻辑均已逐条核对 Prompt 文本。 |

## Findings

### CLEAN-001：Cleaner 未逐字枚举「关键长期事实仅存在于历史 Contract」这一不得 CLEAN 的反例

- Severity：P3
- Status：OPEN
- Location：`docs/agent/models/CleanerAgent.md` 第 51/146/155/161 行；`docs/agent/Five-AgentResponsibilityBoundary.md` 第 113 行
- AC / Invariant：AC-006 第 4 个反例「关键长期事实仅存在于历史 Contract 时不得给出 CLEAN」
- Trigger：某模块既有长期事实（如 db-migration 的 golang-migrate 机制、inventory 的防负库存不变量）至今只存在于历史 `contract.md`；未来一个 `Design Impact = UPDATE` 任务只在新 `docs/design/*.md` 里写了增量、未沉淀既有长期事实，Cleaner 按「四者一致」仅对当前任务 Contract 校验，可能仍判一致。
- Actual：Cleaner 规则用「四者一致 + Design 缺失→`BLOCKED` + 漂移→`CHANGES_REQUIRED`」覆盖了缺失/冲突/未同步三种反例，但没有把「关键长期事实仅存在于历史 Contract」作为独立反例逐字列出。
- Expected：AC-006 明确要求该反例「不得 CLEAN」；可复用「四者一致」或「Design Artifact 缺失」语义显式点出，避免未来 Cleaner 只对增量做校验。
- Impact：低。四者一致与「Design 缺失→BLOCKED」在多数情况下已能间接拦截；该反例主要影响「Design 文件存在但仅含增量、遗漏既有领域事实」的边界。Contract 的 Open Risks 已声明历史债务暂不回填，本项不影响本次机制落地。
- Evidence：`task.md` AC-006 原文；`CleanerAgent.md` 全文 grep 无「历史 Contract」表述。
- Required Fix Boundary：在 Cleaner 的 NOT-CLEAN 条件中明确「关键长期事实仅存在于历史 Contract 时不得 `CLEAN`」（可并入四者一致或 Design 缺失语义），不改变 Owner 已冻结的 `BLOCKED`/`CHANGES_REQUIRED` 门槛，不引入新角色或新阶段。
