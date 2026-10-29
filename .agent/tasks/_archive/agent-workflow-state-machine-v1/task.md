# Task: Agent Workflow State Machine 升级（v1）

## Goal

把现有多 Agent Workflow 从「Prompt 驱动、真实状态散落在 `task.md` / `contract.md` / `findings.md` / `core-logic.md` / `delivery.md` / `.agent/registry/*` / Git HEAD / 聊天决策，由各 Agent 自行推断当前阶段」升级为「状态机驱动、Prompt 执行 Agent」。最终交付一套有限、明确、可审计、可机读的 Task 状态机与 Transition Authority，使「当前处于哪个阶段」「谁能触发哪个转换」「哪些无效状态不可存在」成为由机器事实源 + 轻量 Validator 保证的确定性事实，而非依赖 Prompt 文本与各 Agent 自行解释。

## Scope

- Canonical Task State Machine：定义有限、可审计的 Task 主状态集合，并明确区分主状态与子状态；不以 Markdown 第一行或 Git HEAD 作为唯一流程事实源。
- Transition Authority：为每条状态转换明确「哪个角色有权触发」，而非仅描述「每个 Agent 做什么」；固化「文件写入者 ≠ Decision Authority」。
- Machine-readable Task State：引入 `.agent/tasks/<task>/state.yaml` 作为当前状态的机器事实源，区分 Current State（是什么）与 Evidence / Human-readable Artifact（`findings.md` / `core-logic.md` / `delivery.md`，为什么）。
- Review Target / CLEAN / STALE：正式定义 CLEAN 绑定哪个 `review_target`；哪些实质变化使 `CLEAN → STALE`；哪些 workflow-only Artifact 修改不使 CLEAN 失效；re-review 后如何恢复 CLEAN；避免 Cleaner 更新 `findings.md` / `core-logic.md` 后导致无限 re-review。
- Owner Decision Persistence：固化「Decision Authority != File Writer」；Owner 明确表达 ACCEPT/REJECT 后由 Agent 机械持久化；任何 Agent 不得自行推断 Owner 已接受。
- Cleaner Side-effect Model：区分 Persistent Side Effect（Cleaner-owned Artifact、`findings.md`、`core-logic.md`、审查状态记录）与 Ephemeral Verification Side Effect（Mutation Testing、故障注入、临时错误实现）；后者必须在隔离环境 / 临时 worktree / disposable checkout 完成，不污染 Review Target working tree、不 commit、不成为最终交付内容。
- Global Registry State Machine：为 Global Resource 建立独立生命周期（`RESERVED` / `ACTIVE` / `RELEASED`），保留 migration tombstone / never-reuse 规则；修正 Registry 中 `RESERVED` 的语义错误。
- Task ↔ Resource Gate：需要 migration 的 Task，在有效 Reservation 尚未通过只改 Registry 的 commit 落到共享 `develop` 前，不得进入 IMPLEMENTING（硬 Gate，而非仅存在于文档的规则）。
- Deliverer Gate：将 Deliverer 开始条件正式化为机器可判断的 Gate，只消费既有状态（Contract APPROVED、required Design synchronized、Cleaner CLEAN、Owner Verification `ACCEPTED`/`NOT_REQUIRED`、required resource reservations valid），不替 Owner 最终接受、不要求 Owner 改 Markdown、不重复要求额外交付授权、不重新解释已明确的 Owner Decision。
- Invalid States Unrepresentable：用轻量机械 Validator 使无效状态无法存在（如 DELIVERING + Owner Verification PENDING、IMPLEMENTING + 必需 migration Reservation 缺失、CLEAN 但 review_target 已被实质修改、DONE 但 Delivery 未 PASS）。
- Prompt / Workflow Specification 同步：更新 `docs/agent/*`（协作规范、职责边界、各角色 Prompt）与 Artifact 模板，消除旧规则残留（如「Owner 必须亲自落笔」「额外交付授权」）。
- Compatibility 审计：核对现有 `docs/agent/*`、`.agent/tasks/*`、`.agent/registry/*`、`scripts/check-registry.sh`、Owner Verification Workflow、Contract Revision Workflow、Deliverer Gate，将已验证有效的规则统一收敛到正式状态模型，而非推翻。

## Out of Scope

- 不实现任何生产/业务代码，不新增业务 Feature。
- 不引入数据库、MQ、BPMN，不构建 Web UI，不构建大型 Workflow Engine。
- 不直接实现完整 `agentctl` Orchestrator（若 Analyst 认为未来需要，作为后续独立 Task 提案，不自动扩 Scope）。
- 默认不要求 Deliverer（除非后续分析证明存在需要独立运行验收的工具）。
- 不由 Task Builder 预设最终状态集合、Transition Authority 矩阵、state.yaml schema 或 Validator 实现细节（交 Analyst 设计、Owner 确认）。

## Design Impact

Design Impact: NEW

Design Artifact: `docs/design/agent-workflow.md`（新建：Agent Workflow 状态机、Transition Authority、Review Target/CLEAN/STALE 语义、Global Registry 状态机；具体文件名与内容边界由 Analyst 在 Contract 中最终确定）

## Acceptance Criteria

- [ ] AC-001：任务状态存在唯一、可机读的机器事实源（`state.yaml`），其「当前状态」是流程判定的权威来源；不存在任何以 Markdown 第一行或 Git HEAD 作为唯一状态来源的判定路径。
- [ ] AC-002：存在一套有限、明确、可审计的 Task 主状态与子状态定义，并明确区分主状态/子状态；状态集覆盖从 NEW 到 DONE 的完整生命周期（含 CHANGES_REQUIRED、CONTRACT_REVISION_REQUIRED、BLOCKED 等回退态），不存在未定义或无限状态。
- [ ] AC-003：每条状态转换都有明确、唯一的授权角色（Transition Authority）；任何角色只能触发被授权的转换；文件写入者不等于 Decision Authority。
- [ ] AC-004：Owner 是 Contract Approval 与 Core Logic Acceptance 的唯一决策来源；Agent 只能在 Owner 明确 ACCEPT/REJECT 后机械持久化该决定；任何 Agent 不得因 CLEAN、测试通过、阅读文件等间接信号自行推断 Owner 已接受。
- [ ] AC-005：CLEAN 明确绑定一个可复核的 `review_target`；Cleaner 自己更新 `findings.md` / `core-logic.md` 不导致 CLEAN 失效。
- [ ] AC-006：CLEAN 后发生实质业务代码、测试、Contract 或 Design 修改时，状态可判定地转为 STALE 并要求 re-review；re-review 通过后恢复 CLEAN；不存在 Cleaner 写 `core-logic.md` 导致无限 re-review 的死循环。
- [ ] AC-007：Mutation Testing、故障注入、临时错误实现等 Ephemeral Verification Side Effect 只在隔离环境 / 临时 worktree / disposable checkout 执行，不污染 Review Target working tree、不 commit、不成为最终交付内容。
- [ ] AC-008：需要 migration 的 Task，在有效 Reservation（只改 Registry 的 commit 落到共享 `develop`）存在前不得进入 IMPLEMENTING；Feature Branch 内自行写 `RESERVED` 不构成有效 Reservation。
- [ ] AC-009：Registry 中 `RESERVED` 的定义被修正为「Reservation 已在共享 `develop` 生效，但拥有该资源的 Feature 尚未合并进入 `develop`」，不再是「尚未合并进 `develop`」的错误表述。
- [ ] AC-010：Deliverer 开始条件为机器可判断的 Gate，只消费既有状态（Contract APPROVED、Design 同步、Cleaner CLEAN、Owner Verification ∈ {ACCEPTED, NOT_REQUIRED}、资源 Reservation 有效）；不替 Owner 最终接受、不要求 Owner 改 Markdown、不重复要求额外交付授权、不重新解释已明确的 Owner Decision。
- [ ] AC-011：轻量机械 Validator 能拒绝至少以下无效状态：DELIVERING + Owner Verification PENDING；IMPLEMENTING + 必需 migration Reservation 缺失；CLEAN 但 review_target 已被实质修改；DONE 但 Delivery 未 PASS。
- [ ] AC-012：Prompt ↔ Specification ↔ State Model ↔ Validator 四者一致；`docs/agent/*`、`.agent/registry/*`、`scripts/check-registry.sh`、Owner Verification、Contract Revision、Deliverer Gate 均收敛到同一状态模型，不存在旧规则残留（如「Owner 必须亲自落笔」「额外交付授权」）造成的相互矛盾。
- [ ] AC-013：本次不引入数据库、MQ、BPMN、Web UI 或完整 Orchestrator；现有已验证有效的 Workflow 规则不被推翻。

## Relevant Context

已核实事实：

- Git 基线：Base commit `bba7578ea9219e43bb24efb2b48fa994974aa523`（分支 `develop`），working tree clean（`git status --short` 为空）。
- 现状无任何机器可读 Task State：全仓库 `state.yaml` 命中 0 处；无 `STALE` / `RE_REVIEW` 语义（仅 `docs/design/iam.md` 中的 Redis "stale member" 与业务无关）；"Review Target" 目前只是 `findings.md` 的自由文本字段，非可机读绑定状态。
- `docs/agent/*` 现状：`AgentCollaborationSpecification.md`、`Five-AgentResponsibilityBoundary.md`、`models/{TaskBuilderPrompt,AnalystAgent,CoderAgent,CleanerAgent,DelivererAgent}.md`，共 7 个文件；协作规范 §5 已有「阶段关口」表（角色 → 可交接条件 → 结果），但状态仍由各角色 Prompt 描述，无统一状态机或 Transition Authority。
- 既有 `agent-workflow-owner-confirmation` 任务已固化：Owner Verification Status（`NOT_REQUIRED`/`PENDING`/`ACCEPTED`）、`milestone` 唯一来源（`task.md` 的 `Milestone` 字段）、Deliverer 不再要求额外交付授权；其结论为 `Design Impact = NONE`（只改 `docs/agent/*` 治理文档域，未写 `docs/design/*`）。
- Registry `RESERVED` 定义：`.agent/registry/error-codes.md` 与 `migrations.md` 的「状态与生命周期」节当前均为「`RESERVED`：已申请、尚未合并进 `develop`（Coder 进行中）」——正是 Owner 指出的语义错误。
- `scripts/check-registry.sh` 已存在：只做域/version 重复 + Registry ↔ 实现漂移的机械检查，不分配、不改 Registry、不替代语义判断；其注释把 `RESERVED` 解释为「已预留、未合并进 develop」。
- 关于 Owner 提到的「IAM V4 与 Order V1 同时使用 migration `20261001000007`」：当前 Registry 已为 order-v1 = `20261001000007`、iam-v4 = `20261001000008`（`.agent/registry/migrations.md`、`docs/design/migration.md`、`internal/migrations/sql/20261001000008_refresh_tokens.up.sql` 三者一致），未见该冲突。即该具体冲突在当前 `develop` 已不复现；但「资源预留只是文档规则、未成为进入 IMPLEMENTING 的硬 Gate」这一结构性问题仍成立，需 Analyst 以当前事实为准复核。
- `docs/design/*` 现有 11 个业务设计文件（address/cart/category/error-codes/iam/inventory/migration/order/product/rbac/sku），无 workflow/state-machine 相关的长期设计 Artifact。

Assumption：

- 本任务不占用错误码域或 migration version 等全局资源；仅修正 Registry 的 `RESERVED` 语义文本与资源生命周期表述，不需要在 Registry 新增 `RESERVED` 分配条目。
- 机器事实源文件命名沿用 Owner 建议 `.agent/tasks/<task>/state.yaml`；其字段 schema（`current_state` / `review_target` / `required_resources` / `owner_verification` 等）与 Validator 形态（shell 脚本或 Go test）由 Analyst 设计。

OPEN QUESTION（不阻塞）：

- Design Artifact 落点边界：本任务 Owner 明确要求为状态机建立长期 Design Artifact（`docs/design/*`），但既有 `agent-workflow-owner-confirmation` 任务把治理文档归为 `docs/agent/*` 域（`Design Impact = NONE`）。状态机作为项目级长期结构事实（协作规范 §3 已把「状态机与合法迁移」列为长期 Design 事实类别）落 `docs/design/*`，而各角色 Prompt 执行细节留 `docs/agent/*`，二者边界需 Analyst 提出并与 Owner 确认——本任务按 Owner 要求暂定 `Design Impact = NEW`。

## Verification

本任务不涉及生产代码，验证以「规范可读性 + 场景推演 + 轻量 Validator 运行」为主：

- AC-001 / AC-002 → 阅读更新后的规范与长期 Design：确认存在 `state.yaml` 模板与有限状态集定义；`grep` 确认无「以 Markdown 第一行 / Git HEAD 作为唯一状态来源」的残留表述。
- AC-003 → 阅读 Transition Authority 表，逐一核对每条转换的授权角色，确认「文件写入者 != Decision Authority」。
- AC-004 → 场景推演 + 阅读规范：任何 Agent 不得自行写 `ACCEPTED` 或自行推断 Owner 已接受；Owner 决策 → Agent 机械持久化的路径明确。
- AC-005 / AC-006 → 用合成任务推演 `CLEAN → STALE → RE_REVIEW → CLEAN`，确认 Cleaner 写 `findings.md`/`core-logic.md` 不失效、实质修改失效、re-review 恢复、无死循环。
- AC-007 → 阅读 Cleaner 规范：Ephemeral 副作用必须隔离环境 / 临时 worktree / disposable checkout，不污染、不 commit、不入交付。
- AC-008 → 合成任务推演 + 阅读规范：缺少有效 `develop` Reservation 时进入 IMPLEMENTING 被 Gate 拒绝；Feature Branch 私留 `RESERVED` 无效。
- AC-009 → 阅读修正后的 Registry `RESERVED` 定义，确认语义改为「已在共享 `develop` 生效、Feature 未合并」。
- AC-010 → 阅读 Deliverer 开始关口 + 合成任务推演：只消费既有状态，无额外授权、无要求 Owner 改 Markdown。
- AC-011 → 运行 Validator（如 `scripts/check-workflow-state.sh`）对构造的无效状态样本，确认被拒绝；对合法样本确认通过。
- AC-012 → 全仓 `grep` 旧规则残留（「Owner 必须/亲自编辑」「Delivery Authorization」「<当前里程碑>」等）+ 交叉核对 Prompt ↔ Spec ↔ State Model ↔ Validator。
- AC-013 → 检查 Diff 不引入 DB / MQ / BPMN / Web UI / Orchestrator；核对现有已验证规则未被推翻。

## Complexity

COMPLEX

原因：这是现有多 Agent Workflow 的结构性架构升级，涉及状态模型设计、Transition Authority、Review Target/CLEAN/STALE 语义、Global Resource Gate、机器事实源 schema 与 Validator 形态等多个存在真实方案取舍的设计选择；不同方案（状态集划分、Validator 形态与落点、Design Artifact 边界）会产生不同的流程行为与后续维护成本，且需要先做全仓证据审计。必须交 Analyst 调查并提交 Owner 确认后，Coder 才能实现。

## Analyst Questions

1. 最终 Task 主状态集合与主状态/子状态划分（哪些是主状态，哪些作为子状态或派生事实）。
2. Transition Authority 矩阵的最终形态：每条转换的唯一授权角色，以及文件写入与 Decision Authority 的分离边界。
3. `state.yaml` 的字段 schema（`current_state` / `review_target` / `required_resources` / `owner_verification` / 证据引用等）与「Current State vs Evidence Artifact」的权威关系。
4. Design Artifact 落点边界：状态机长期事实应落 `docs/design/*` 还是 `docs/agent/*`，如何与既有 `agent-workflow-owner-confirmation`（`Design Impact = NONE`）的边界统一。
5. 「实质变化」使 `CLEAN → STALE` 的可判定定义：哪些信号（业务代码/测试/Contract/Design 的 diff）机械触发 STALE，哪些 workflow-only 修改不触发。
6. Validator 形态与落点：shell 脚本（类似 `check-registry.sh`）还是 Go test；无效状态检查清单与执行时机。
7. 是否确实需要后续独立 `agentctl` Orchestrator（本任务不实现，仅作提案判断）。

## Review Baseline

- Base commit：`bba7578ea9219e43bb24efb2b48fa994974aa523`（分支 `develop`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增 `.agent/tasks/agent-workflow-state-machine-v1/*`，并可能修改 `docs/agent/*`、新建 `docs/design/agent-workflow.md`、修正 `.agent/registry/*` 的 `RESERVED` 语义文本、在 `scripts/` 新增 Validator。当前工作区干净，无既有未提交修改。

## Initial Route

READY_FOR_ANALYST
