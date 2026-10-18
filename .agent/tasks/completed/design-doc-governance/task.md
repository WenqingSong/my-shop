# Task: 长期 Design 文档治理机制（Agent Workflow 修复）

## Goal

建立一套稳定、可执行、可验收的「长期 Design 文档治理机制」：在任务级临时事实（`task.md` / `contract.md` / `findings.md` / `core-logic.md` / `delivery.md`）与项目级长期事实（`docs/design/*`）之间建立明确的生命周期边界与流转规则。当任务改变长期架构、业务模块、数据模型、状态机、模块边界、跨模块不变量等项目级事实时，通过 Design Impact 判定与各角色职责联动，确保这些事实在任务结束时同步进入 `docs/design/*`，并在 `CLEAN` 前验证 Task ↔ APPROVED Contract ↔ Design ↔ Implementation 四者一致。

交付物是「被修改后的公共 Agent 规范与角色 Prompt」，而不是任何具体业务模块的 Design 文档。

## Scope

- 公共协同规范与职责边界（`docs/agent/AgentCollaborationSpecification.md`、`docs/agent/Five-AgentResponsibilityBoundary.md`）正式区分「任务级 Artifact」与「长期 Design Artifact」，并定义 `docs/design/*` 的用途、生命周期与维护者。
- 引入 Design Impact 判定机制（`NONE` / `UPDATE` / `NEW`，最终命名由 Analyst 固化），覆盖触发 `NEW`/`UPDATE` 的项目级事实（新增业务模块、核心数据模型、数据库表、实体关系、状态机、模块边界、鉴权/RBAC 架构、Migration/Deployment 模型、跨模块不变量、一致性模型、长期公开协议等）与触发 `NONE` 的局部事实（Bugfix、纯测试补充、格式整理、不改变长期架构的局部优化）。
- Task Builder 职责：有 Design Impact 时必须声明 Design Artifact（目标 `docs/design/*` 文件或设计域），并将其纳入 Scope / Deliverables / AC。
- Analyst 职责：明确为长期 Design 内容的主责角色；只能基于 Owner `APPROVED` 的 Contract 新建/更新 `docs/design/*`，写入职责边界、数据模型、状态机、不变量、模块关系、错误语义、安全/一致性边界等长期事实；不得借更新 Design 私自新增未批准设计；Contract Revision 发生后 Design 必须随最终 APPROVED Contract 更新。
- Coder 职责（最小补充）：不作为长期 Design 所有者；按 Contract 实现，不得擅自按实现修改架构事实；实现中发现冲突走 Contract Revision。
- Cleaner 职责：`CLEAN` 条件纳入 Design 一致性；Design Impact = NEW/UPDATE 时验证 Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ 最终 Implementation 四者一致；Design 缺失、与 Contract/实现冲突、Contract Revision 后未同步、关键长期事实只存在于历史 Contract 等情形不得 `CLEAN`。
- Deliverer 职责（最小确认）：交付前确认 `Cleaner = CLEAN` 且 Design Artifact 已纳入 Review Target，不重复 Cleaner 的 Design 审查。
- 术语一致性：`Design Impact`、`Design Artifact`、`长期 Design` 等术语在所有被改文件的表达统一。

## Out of Scope

- 不偿还历史 Design Debt：不新建 `docs/design/product.md` / `sku.md` / `inventory.md` / `migration.md`，不补写任何历史模块的长期 Design（历史回填另建独立任务）。
- 不修改任何具体业务模块的 `contract.md`，不重新设计 Product / SKU / Inventory / IAM / Category / Migration。
- 不修改生产代码与业务测试。
- 不规定「所有任务都必须写 Design」，不把 `contract.md` 改名成 Design，不让 `docs/design/*` 成为 Contract 的机械全文复制。
- 不把完整实现细节、测试细节、临时排障信息复制进 Design。
- 历史任务（`product-spu-v1` / `sku-v1` / `inventory-v1` / `db-migration` 等）只作为验证现有 Workflow 缺陷的证据，不在本任务中补它们的 Design。

## Acceptance Criteria

- [ ] AC-001：公共规范（`AgentCollaborationSpecification.md` 与 `Five-AgentResponsibilityBoundary.md`）明确区分「任务级 Artifact」与「项目级长期事实（`docs/design/*`）」，并说明各自用途、生命周期与默认维护者；不再出现「关键设计只能沉淀进 `contract.md`、项目长期设计落点无定义」的缺口。
- [ ] AC-002：Task Builder Prompt 定义了 Design Impact 判定机制（含 `NONE` / `UPDATE` / `NEW` 三态或等价机制），给出触发 `NEW`/`UPDATE` 与 `NONE` 的判定清单，且不规定「所有 COMPLEX 任务都必须 NEW Design」。
- [ ] AC-003：当 Design Impact = NEW/UPDATE 时，`task.md` 必须声明需要新增还是更新 Design、目标 Design 文件或设计域，并把 Design Artifact 纳入 Scope / Deliverables / Acceptance Criteria，使后续角色能看到该任务的设计责任。
- [ ] AC-004：Analyst 职责明确为长期 Design 内容主责，且只能沉淀 Owner `APPROVED` 的设计；Contract Revision 发生时，Design 必须随最终 APPROVED Contract 更新。
- [ ] AC-005：Coder 职责包含「不得以实现便利为由擅自改变长期 Design；实现中事实冲突走 Contract Revision」的最小约束，且不被定义为长期 Design 所有者。
- [ ] AC-006：Cleaner 的 `CLEAN` 条件在 Design Impact = NEW/UPDATE 时要求验证 Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ 最终 Implementation 四者一致；Design 缺失、与 Contract/实现冲突、Contract Revision 后未同步、关键长期事实仅存在于历史 Contract 时，不得给出 `CLEAN`。
- [ ] AC-007：Design Impact = NONE 的普通 Bugfix / 纯测试补充 / 格式整理任务，不被强制要求产生 Design Artifact。
- [ ] AC-008：各角色职责之间不存在互相矛盾或循环依赖；所有相关 Prompt/规范中 `Design Impact`、`Design Artifact` 等术语一致。
- [ ] AC-009：IAM 这类已有正确 Design 流程的任务不被新规则破坏；若按新规则重新创建 Product / SKU / Inventory 类任务，Task Builder 能识别出 Design Impact = NEW/UPDATE，而不是再次遗漏。

## Relevant Context

### 已核实事实（当前仓库）

- `docs/design/` 当前仅含 `iam.md`。其产生原因是 `iam-v2` 的 `task.md` 在 Scope 中显式要求「独立交付物：IAM 系统设计文档」（AC-010 与「设计文档（独立交付物）」小节），且当时约定「由 Coder 起草，Cleaner 审查其与 Contract/实现的一致性」——这是单任务人为要求，不是公共规范赋予的系统职责。
- `product-spu-v1`、`sku-v1`、`inventory-v1`、`db-migration` 均为 COMPLEX 任务，已有 `contract.md` 且实现完成（部分已 `CLEAN`/`PASS`），但 `docs/design/` 中均无对应长期 Design 文件。
- `docs/agent/AgentCollaborationSpecification.md` 第 3 节「任务文件」只定义 `task.md` / `contract.md` / `findings.md` / `core-logic.md` / `delivery.md`，未定义 `docs/design/*` 这一类别，也未定义其与 Contract 的生命周期关系。
- `docs/agent/Five-AgentResponsibilityBoundary.md` 未赋予任何角色「长期 Design 同步」职责；Analyst 职责仅到 `contract.md`，Cleaner 的 `CLEAN` 条件未涉及 Design。
- 全仓公共规范中不存在「Design Impact」概念；`docs/agent/` 下「design」一词仅出现在 `AnalystAgent.md` 的 `contract.md` 模板 `Selected Design` 章节（指 Contract 内章节，非 `docs/design/*`）。
- `agents.md` 第 3 行规则优先级为「Owner 当前指令 → 当前 Task → 已确认 Contract/设计 → 本文件 → 角色流程」，其中「已确认 Contract/设计」已把「设计」与 Contract 并列，但未定义「设计」的落点与生命周期。
- `iam-v2` 的 `task.md` 是唯一显式声明「Coder 起草、Cleaner 审查」Design 的先例，与本任务推荐方向（Analyst 主责长期 Design）存在职责归属差异，需 Analyst 在方案中厘清并给出统一规则。
- 当前分支 `develop`，HEAD `eef0911c1cfc2939d5115b689650229bc0d98197`，working tree clean。

### Assumption

- 「Design Impact」三态取值与「Design Artifact」字段/结构等最终命名、落点由 Analyst 固化，本任务只要求存在等价机制。
- 「Analyst 作为长期 Design 内容主责」是推荐方向；若仓库证据支持更优归属，由 Analyst 在方案中论证并由 Owner 确认。

### OPEN QUESTION

- 是否需要对 `agents.md` 做最小修改（如第 3 行「关键设计写入 `contract.md`」与 `docs/design/*` 的关系说明），以及修改边界，交 Analyst 确认。

## Verification

本任务为 Agent Workflow 治理任务，不适用 `go test ./...`。采用「场景验证 + 静态一致性检查」：

- AC-001 → 逐字审查 `AgentCollaborationSpecification.md` 与 `Five-AgentResponsibilityBoundary.md`，确认存在「任务级 Artifact vs 项目级长期事实」的显式区分与生命周期定义。
- AC-002/AC-003 → 场景 B/C：以「新增 Product 模块」「修改 IAM 状态机」为例，走 TaskBuilder Prompt，预期分别输出 `Design Impact = NEW`、`UPDATE` 且 `task.md` 声明 Design Artifact；场景 A（局部 nil pointer bugfix）预期输出 `NONE` 且不要求 Design。
- AC-004/AC-005 → 审查 `AnalystAgent.md` 与 `CoderAgent.md`，确认 Analyst 的 Design 主责与 Owner Approval 边界、Coder 的「不得擅自改 Design」约束已写入。
- AC-006 → 场景 D/E：模拟「Contract Revision 后 Design 未同步」「Design 缺失」，走 Cleaner Prompt，预期 Cleaner 不得给出 `CLEAN`（`CHANGES_REQUIRED` 或 `BLOCKED`）。
- AC-007 → 场景 A 反证：普通 Bugfix 任务不产生 Design 要求。
- AC-008 → 交叉审阅全部被改 Prompt/规范，确认职责无循环依赖、术语一致（grep `Design Impact` / `Design Artifact` / `长期 Design`）。
- AC-009 → 回归审查：IAM 现有 `docs/design/iam.md` 流程不被新规则破坏；按新规则重建 Product/SKU/Inventory 任务时能识别 Design Impact。

## Complexity

COMPLEX

原因：涉及多角色职责重新划分、Task 生命周期、Artifact 生命周期、Definition of Done、多个 Prompt/规范联动与术语统一；Design Impact 的判定边界、Analyst 写 Design 的时机、Contract Revision 的传播、Cleaner 阻塞阈值等都有多种现实方案，会产生不同的治理结果。需 Analyst 固化方案并由 Owner 确认后，再交 Coder 落地文档修改。

## Analyst Questions

1. Design Impact 的精确定义与三态命名（`NONE`/`UPDATE`/`NEW` 或等价），以及判定边界（哪些事实触发 NEW、哪些触发 UPDATE、哪些 NONE），如何防止「文档泛滥」与「遗漏」两个极端。
2. `docs/design/*` 的粒度与结构：按业务模块一文件还是按设计域组织；文件命名规范；如何避免成为 `contract.md` 的机械复制（哪些内容进 Design、哪些只留在 Contract）。
3. Analyst 写长期 Design 的时机与顺序：Contract `APPROVED` 后即写、Coder 实现落定后写、还是 Cleaner 审查后定稿；与 `iam-v2` 既有「Coder 起草、Cleaner 审查」先例如何统一（本任务推荐 Analyst 主责，需确认是否取代该先例）。
4. Contract Revision 如何传播到 Design：由谁触发（Analyst/Cleaner）、何时触发、旧版本如何标记，确保最终 Design 反映最新 APPROVED Contract。
5. Cleaner 阻塞规则的精确阈值：Design 缺失 / 与 Contract 冲突 / 与实现漂移 / Revision 未同步分别对应 `BLOCKED` 还是 Finding（`CHANGES_REQUIRED`），以及 Finding 严重级别（P0/P1/P2 如何对应）。
6. 各角色 Prompt 与公共规范的修改边界：`agents.md`、`AgentCollaborationSpecification.md`、`Five-AgentResponsibilityBoundary.md`、`TaskBuilderPrompt.md`、`AnalystAgent.md`、`CleanerAgent.md` 哪些必改、`CoderAgent.md` / `DelivererAgent.md` 是否最小改、哪些不改；最终 Allowed / Forbidden Changes。
7. 术语一致性方案：`Design Impact`、`Design Artifact`、`长期 Design` 等术语在全部被改文件中的统一表达。
8. 历史 Design Debt 的边界确认：本任务只修 Workflow；回填 Product/SKU/Inventory/Migration Design 的独立任务如何界定，避免本任务顺手扩大 Scope。

## Review Baseline

- Base commit：`eef0911c1cfc2939d5115b689650229bc0d98197`（分支 `develop`）
- 任务开始时已有修改：无（`git status --short` 为空，working tree clean）
- 重叠修改的区分方式：本任务仅修改 `docs/agent/**` 下的公共规范与角色 Prompt（可能含根目录 `agents.md` 的最小说明）；不触碰任何业务任务目录（`.agent/tasks/*` / `docs/tasks/*`）、业务代码或 `docs/design/*`。当前工作区干净，无既有未提交修改需区分。

## Initial Route

READY_FOR_ANALYST
