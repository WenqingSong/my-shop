# Technical Contract

## Decision Status

APPROVED

## Problem

建立一套稳定、可执行、可验收的「长期 Design 文档治理机制」：在任务级临时事实（`task.md` / `contract.md` / `findings.md` / `core-logic.md` / `delivery.md`）与项目级长期事实（`docs/design/*`）之间划出明确的生命周期边界与流转规则。当任务改变项目级长期事实（新增业务模块、核心数据模型、数据库表、实体关系、状态机、模块边界、跨模块不变量、鉴权/RBAC 架构、Migration/Deployment 模型、长期公开协议、一致性/安全边界）时，通过 `Design Impact` 判定与各角色职责联动，确保这些事实在任务结束时同步进入 `docs/design/*`，并在 `CLEAN` 前验证「Task ↔ APPROVED Contract ↔ Design ↔ Implementation」四者一致。

交付物是被修改后的公共 Agent 规范与角色 Prompt，不是任何具体业务模块的 Design 文档；不回填历史 Design Debt。

## Verified Current Behavior

- VERIFIED：`docs/design/` 当前仅含 `iam.md`，内容正确且是完整的长期设计文档（架构/数据模型/鉴权·登出·撤销流程/安全边界）。其产生原因是 `iam-v2/task.md` 的 AC-010 与「设计文档（独立交付物）」小节单任务显式要求「由 Coder 起草、Cleaner 审查」，是人为要求，非公共规范赋予的系统职责。
- VERIFIED：`docs/agent/AgentCollaborationSpecification.md` 第 3 节「任务文件」只定义 `task.md` / `contract.md` / `findings.md` / `core-logic.md` / `delivery.md` 五类任务级文件，未定义 `docs/design/*` 这一类别，也未定义其与 Contract 的生命周期关系。
- VERIFIED：`docs/agent/Five-AgentResponsibilityBoundary.md` 未赋予任何角色「长期 Design 同步」职责；Analyst 职责仅到 `contract.md`，Cleaner 的 `CLEAN` 条件未涉及 Design。
- VERIFIED：全仓公共规范中不存在「Design Impact」概念；`docs/agent/` 下「design」一词仅出现在 `AnalystAgent.md` 的 `contract.md` 模板 `Selected Design` 章节（指 Contract 内章节，非 `docs/design/*`）。
- VERIFIED：`agents.md` 第 3 行规则优先级为「Owner 当前指令 → 当前 Task → 已确认 Contract/设计 → 本文件 → 角色流程」，其中「已确认 Contract/设计」已把「设计」与 Contract 并列，但未定义「设计」的落点与生命周期；第 2 行仅说「关键设计写入经 Owner 确认的 contract.md」。
- VERIFIED：历史 COMPLEX 任务 `product-spu-v1` / `sku-v1` / `inventory-v1` / `db-migration` / `admin-identity-rbac` / `iam-v2` 均已有 APPROVED `contract.md` 且实现完成，但 `docs/design/` 中除 `iam.md` 外无对应长期 Design 文件。这些 Contract 中含有大量本应沉淀为项目级长期事实的内容（例如 `db-migration` 的 golang-migrate 机制、CLI 结构、14 位时间戳 version 与 baseline/force/dirty 语义；`inventory-v1` 的 `inventories`/`inventory_logs` 数据模型、防负库存不变量、SKU 删除跨模块约束），目前仅存在于历史 Contract。
- VERIFIED：`iam-v2` 的 `task.md` 是唯一显式声明「Coder 起草、Cleaner 审查」Design 的先例，与本任务推荐方向（Analyst 主责长期 Design）在职责归属与产出时机上均不同，需统一。
- VERIFIED：当前分支 `develop`，HEAD `eef0911c1cfc2939d5115b689650229bc0d98197`，本任务仅新增 `.agent/tasks/design-doc-governance/`（未跟踪），无其他未提交修改。
- UNKNOWN：无阻塞性 UNKNOWN。历史任务不补 Design 的边界已由 Task 的 Out of Scope 明确（回填另建独立任务）。

## Recommendation

RECOMMENDATION：**Analyst 主责长期 Design + `Design Impact` 三态判定 + `CLEAN` 前四者一致校验**。逐项固化如下：

1. **Design Impact 三态与判定**：取 `NONE` / `UPDATE` / `NEW`。判定依据是「是否改变项目级长期事实」，与任务复杂度无关。
   - `NEW`：新增项目级长期事实 —— 新增业务模块（新表/新实体/新模块边界）、新增核心数据模型或实体关系、新增状态机、新增跨模块不变量或一致性模型、新增长期公开协议（对外 API 契约/错误码域/消息协议）、新增鉴权/RBAC 架构或安全边界、新增 Migration/Deployment 模型。
   - `UPDATE`：修改既有项目级长期事实 —— 修改既有数据模型/表结构/实体关系、修改状态机（新增/删除/重定义合法迁移）、修改模块边界或跨模块不变量、修改一致性模型/错误语义/安全边界、修改长期公开协议。
   - `NONE`：局部事实 —— Bugfix（不改变数据模型/状态机/协议）、纯测试补充、格式整理/注释/日志、不改变长期架构的局部优化。
   防「文档泛滥」靠「按事实类别判定、NONE 不产生 Design、不按 COMPLEX 机械要求 NEW」；防「遗漏」靠清单覆盖数据模型/状态机/模块边界/不变量/协议/安全/迁移七类，并以 AC-009 的「重建 Product/SKU/Inventory 任务应识别 NEW/UPDATE」作回归。

2. **`docs/design/*` 粒度与结构**：按业务模块/设计域一文件组织（一文件可承载一个模块，不按任务拆文件），命名 kebab-case（如 `iam.md`、`product.md`、`inventory.md`、`migration.md`）。Design 沉淀「长期稳定的结构事实」—— 架构与组件、数据模型（表/实体/关系）、状态机与合法迁移、模块边界与跨模块不变量、一致性模型与失败语义、安全/权限边界、错误码域与公开协议、配置契约。Contract 保留「本任务的决策过程」—— 问题边界、当前行为证据、推荐与取舍、本任务决定、验证要求、Allowed/Forbidden、Open Risks、Owner Decision Record。原则：Design 是「结论」（面向接手者），Contract 是「过程」（本任务决策记录），Design 不做 Contract 的机械全文复制。

3. **职责归属与产出时机**：Analyst 为长期 Design 内容主责（与现有「Analyst 负责 contract.md 与设计、Coder 不负责设计」的职责边界一致）。Analyst 在 Owner `APPROVED` Contract 后、Coder 实现前，基于 APPROVED Contract 写入/更新 Design Artifact；Coder 按 Contract 实现；Cleaner 验证四者一致。此规则取代 `iam-v2` 的「Coder 起草、Cleaner 审查」单任务先例（`iam.md` 已存在且正确，不因新规则回溯改写；AC-009 保证 IAM 既有流程不被破坏）。

4. **Contract Revision 传播**：任何 Contract Revision 经 Owner 重新 `APPROVED` 后，Analyst 必须检查是否影响 Design，影响则同步更新（保留 Owner Decision Record，旧版本由 Git 历史承载，Design 文件始终为最新权威）；Cleaner 发现「Revision 已 APPROVED 但 Design 未同步」时不得 `CLEAN`。

5. **Cleaner 阻塞阈值**：当 Design Impact = NEW/UPDATE 时——Design 缺失 → `BLOCKED`（缺少已批准设计，同现有「复杂任务 Contract 未批准」的 BLOCKED 语义）；Design 与 APPROVED Contract 冲突、或 Revision 后未同步 → `BLOCKED`（设计问题交 Analyst）；Design 与最终实现漂移 → `CHANGES_REQUIRED`（实现服从 Design，交 Coder 修复）。Design Impact = NONE 时不要求 Design。

6. **修改边界**：
   - 必改：`docs/agent/AgentCollaborationSpecification.md`（任务文件区分任务级/长期 Artifact + 阶段关口纳入 Design 一致性）、`docs/agent/Five-AgentResponsibilityBoundary.md`（Analyst 主责 + Cleaner 校验 + Task Builder 声明 + Coder 最小约束 + Deliverer 最小确认）、`docs/agent/models/TaskBuilderPrompt.md`（Design Impact 判定与 task.md 声明）、`docs/agent/models/AnalystAgent.md`（Design 主责、产出时机、Revision 传播）、`docs/agent/models/CleanerAgent.md`（CLEAN 条件）、`docs/agent/models/CoderAgent.md`（不擅改 Design 的最小约束）、`docs/agent/models/DelivererAgent.md`（Design Artifact 纳入 Review Target 的最小确认）。
   - 待 Owner 确认的最小改：根目录 `agents.md` 第 3 行优先级说明与第 2 行「关键设计写入 contract.md」，补充「长期设计的落点为 `docs/design/*`」。
   - 不改：生产代码、业务测试、`docs/design/iam.md`、任何历史任务目录、任何业务模块 Contract。

7. **术语统一**：全仓统一使用 `Design Impact`（三态 `NONE`/`UPDATE`/`NEW`）、`Design Artifact`（指 `docs/design/*` 文件）、`长期 Design`（指 `docs/design/*` 整体类别），各文件表达一致。

关键取舍：**职责归属上「Analyst 主责」取代「Coder 起草 + Cleaner 审查」**。前者让 Design 从 APPROVED Contract 派生、实现服从 Design（权威方向正确，且与现有职责边界一致）；后者的先例（iam-v2）本质是「实现落定后反推描述」，易使 Design 退化为实现的事后记录。代价是 Analyst 职责轻微扩展、产出时机提前到实现前，且若实现暴露 Contract 错误需走 Revision 后再同步 Design；这正是四者一致校验要覆盖的路径。

## Selected Design

Owner 已确认（2026-10-04）并冻结：

1. **职责归属**：长期 Design 由 Analyst 主责维护，取代 iam-v2「Coder 起草 Design」作为通用流程；Coder 负责实现，不作为长期设计事实所有者；Cleaner 负责最终一致性校验。现有 `docs/design/iam.md` 不因职责调整而回溯重写。

2. **产出时机与边界**：`Design Impact = NEW/UPDATE` 时，在 Contract 经 Owner `APPROVED` 后、Coder 实现前，由 Analyst 新增/更新对应 `docs/design/*`。Design 只能沉淀已 APPROVED 的项目级长期设计事实；不得提前写入需实现才能确定的代码级细节，不得新增 Contract 未批准的设计。实现阶段发现设计需改变时，必须先 `CONTRACT_REVISION → Owner APPROVAL → Design 同步`，未同步不得视为一致状态。

3. **`agents.md` 最小修改**：允许最小修改根目录 `agents.md`，明确区分「任务级设计约束与决策记录（`task.md`/`contract.md` 等）」与「项目级长期设计事实（`docs/design/*`）」；`Design Impact`、角色职责、同步时机与 `CLEAN` 门槛继续由 `docs/agent/*` 与各角色 Prompt 定义，不在 `agents.md` 重复实现细节。

4. **Cleaner 门槛**（Owner 明确）：
   - `NEW/UPDATE` 要求 Design 但 Artifact 缺失、Contract Revision 后 Design 未同步等前置事实缺失 → `BLOCKED`；
   - Design 已存在，但与最终 APPROVED Contract 或 Implementation 存在漂移 → `CHANGES_REQUIRED`；
   - `Design Impact = NONE` 不强制创建或修改 Design。

5. **历史 Design Debt**：本任务不回填 Product/SKU/Inventory/Migration 的历史 Design Debt，历史债务另立任务处理。

其余机制（三态判定清单、`docs/design/*` 粒度与结构、Contract Revision 传播、修改边界、术语统一、业务不变量）按 Recommendation 冻结。

## Interfaces and Data

### `task.md` 新增声明字段（Task Builder 产出）

当 `Design Impact = NEW/UPDATE` 时，`task.md` 必须声明：

```text
Design Impact: NEW / UPDATE / NONE
Design Artifact: <目标 docs/design/* 文件或设计域>
```

并把 Design Artifact 纳入 Scope / Deliverables / AC。`NONE` 时不声明 Design Artifact。

### `docs/design/*` 组织约定

- 一业务模块/设计域一文件，kebab-case 命名；文件内章节至少覆盖该域长期结构事实（架构/数据模型/状态机/不变量/错误语义/安全边界/公开协议/配置契约）。
- 维护者：Analyst（新增/更新仅限 Owner APPROVED 后）；Cleaner 验证一致性；Coder/Deliverer 不写 Design。

### 术语对照（全仓统一）

| 术语 | 含义 |
| --- | --- |
| `Design Impact` | 任务对项目级长期事实的影响：`NONE` / `UPDATE` / `NEW` |
| `Design Artifact` | `docs/design/*` 下的长期设计文件 |
| `长期 Design` | `docs/design/*` 整体类别 |

## Business Invariants

- INV-001（Design 声明完整）：`Design Impact = NEW/UPDATE` 的任务，其 `task.md` 必须声明目标 Design Artifact 并纳入 Scope/AC；未声明视为 Task 不完整，Task Builder 未完成职责。
- INV-002（Approval 门槛）：只有 Owner `APPROVED` 的 Contract 才能驱动 `docs/design/*` 的新增/更新；未 APPROVED 的设计不得进入 Design。
- INV-003（四者一致才 CLEAN）：`Design Impact = NEW/UPDATE` 时，Cleaner 必须验证「Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ 最终 Implementation」四者一致；任一不一致不得 `CLEAN`。
- INV-004（NONE 不强制 Design）：`Design Impact = NONE` 的普通 Bugfix / 纯测试补充 / 格式整理任务，不强制产生 Design Artifact。
- INV-005（Revision 传播）：Contract Revision 经 Owner `APPROVED` 后，受影响的 Design 必须同步更新；未同步不得 `CLEAN`。

## Allowed / Forbidden Changes

允许：
- 修改 `docs/agent/AgentCollaborationSpecification.md`、`docs/agent/Five-AgentResponsibilityBoundary.md`。
- 修改 `docs/agent/models/` 下五个角色 Prompt（TaskBuilder / Analyst / Coder / Cleaner / Deliverer）。
- 若 Owner 确认，对根目录 `agents.md` 做最小说明性修改（第 2/3 行补充长期设计落点与优先级关系），不改规则优先级本身。

禁止：
- 修改生产代码、业务测试、`docs/design/iam.md` 或任何业务模块 Contract。
- 新建/回填任何业务模块 Design 文档（`docs/design/product.md` 等历史回填另建独立任务）。
- 修改历史任务目录（`.agent/tasks/*`、`docs/tasks/*`）。
- 把 `contract.md` 改名或让 `docs/design/*` 成为 Contract 的机械全文复制。
- 改变除「长期 Design 相关职责」外的任何角色核心职责语义、阶段关口或 Finding 体系。
- 规定「所有 COMPLEX 任务都必须 NEW Design」，或要求 NONE 任务产生 Design。

## Verification Requirements

- INV-001 → 场景 B/C（新增 Product 模块 → `Design Impact = NEW`；修改 IAM 状态机 → `UPDATE`），走 Task Builder Prompt，预期 `task.md` 声明 Design Artifact 并纳入 Scope/AC；场景 A（局部 nil pointer bugfix）预期 `NONE` 且不要求 Design。
- INV-002 → 审查 `AnalystAgent.md`，确认「仅 Owner APPROVED 后写/更新 Design」边界写入，且无任何路径允许未批准设计进入 Design。
- INV-003 → 场景 D/E：模拟「Contract Revision 后 Design 未同步」「Design 缺失」，走 Cleaner Prompt，预期不得 `CLEAN`（`BLOCKED` 或 `CHANGES_REQUIRED`）。
- INV-004 → 场景 A 反证：普通 Bugfix 任务不产生 Design 要求。
- INV-005 → 场景 D：Revision APPROVED 后 Design 未同步，Cleaner 不得 CLEAN。
- AC-008（术语一致）→ 交叉审阅全部被改文件，`grep` `Design Impact` / `Design Artifact` / `长期 Design`，确认无循环依赖、职责无矛盾、术语一致。
- AC-009（回归）→ 审查 IAM 既有 `docs/design/iam.md` 流程不被新规则破坏；按新规则重建 Product/SKU/Inventory 任务能识别 `NEW`/`UPDATE`。
- 通用：本任务为 Agent Workflow 治理任务，不适用 `go test ./...`，采用「场景验证 + 静态一致性检查」。

## Open Risks

- `Design Impact` 判定边界存在主观性（如「局部优化」vs「修改数据模型」），清单只能覆盖常见类别；极端边界由 Task Builder 声明、Analyst 在 Contract 中校正，校正若改变 Scope 需回 Owner/Task Builder。
- 本任务只修 Workflow，不回填历史 Design Debt；`product.md`/`sku.md`/`inventory.md`/`migration.md` 长期缺失将持续存在，直到独立回填任务启动，期间 Cleaner 无法对既有模块执行四者一致校验（历史任务已 CLEAN，不回退审查）。

## Owner Decision Record

- 2026-10-04（APPROVED）：Owner 确认并冻结全部待决问题：
  1. **职责归属**：Analyst 主责长期 Design，取代 iam-v2「Coder 起草 Design」作为通用流程；`docs/design/iam.md` 不回溯重写。
  2. **产出时机与边界**：`Design Impact = NEW/UPDATE` 时，Contract `APPROVED` 后、Coder 实现前由 Analyst 写/更新 Design；只沉淀 APPROVED 事实，不得提前写代码级细节或未批准设计；实现期变更走 `CONTRACT_REVISION → Owner APPROVAL → Design 同步`。
  3. **`agents.md` 最小修改**：允许，仅区分任务级 vs 项目级事实，不在 `agents.md` 重复实现细节。
  4. **Cleaner 门槛**：前置事实缺失（Artifact 缺失、Revision 未同步）→ `BLOCKED`；Design 与 APPROVED Contract 或 Implementation 漂移 → `CHANGES_REQUIRED`；`NONE` 不强制 Design。
  5. **历史债务**：本任务不回填 Product/SKU/Inventory/Migration 的 Design Debt，另立任务。

  与 Recommendation 的差异：Recommendation 第 5 点原将「Design 与 APPROVED Contract 冲突」归为 `BLOCKED`（设计问题交 Analyst），Owner 调整为 `CHANGES_REQUIRED`，统一为「前置事实缺失 → `BLOCKED`；已有 Design 但漂移 → `CHANGES_REQUIRED`」。已按 Owner 决定写入 Selected Design。

  适用范围：`design-doc-governance` 本任务。Contract 与 Task 兼容，无需变更 Task Scope/AC。
