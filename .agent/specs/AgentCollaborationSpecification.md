# Agent 协同规范

## 1. 目标

Agent 用来扩大实现、测试和审查能力；Owner 决定需求、关键方案和最终是否接受，并掌握重要业务逻辑。

本文件只规定跨角色协作。Workflow 专用指令入口是 `.agent/specs/WorkflowAgentDirective.md`；宿主业务规范位于宿主根 `AGENTS.md`（存在时读取，不存在时不阻断）。角色边界见 `.agent/specs/Five-AgentResponsibilityBoundary.md`，当前需求以 `.agent/tasks/<task-slug>/task.md` 为准，状态模型见 `docs/design/agent-workflow.md`。

Git 的 Stage / Commit / Push / Gate / HANDOFF 时机与七角色例外矩阵以 `.agent/specs/WorkflowGitLifecycle.md` 为准；跨 Session HANDOFF 的最小字段与格式以 `.agent/specs/WorkflowHandoffContract.md` 为准。本文件不重复这两份规范的细节。

角色数量不是质量指标。只启用能为当前任务增加明确价值的角色。

正常运行只依赖 Git + Artifacts + `.agent/specs/WorkflowAgentDirective.md` + `.agent/specs/*` + `.agent/roles/*` + `docs/design/*`，不依赖临时文件 `PROJECT_ADAPTATION.md`。`PROJECT_ADAPTATION.md` 只存在于 Workflow V2 首次迁移 / Adoption 到一个项目的过程中，Adoption 完成后即删除，见 `.agent/specs/WorkflowAdoption.md`。

`NORMAL` / `COMPLEX` 使用六个角色（TaskBuilder、Analyst、Coder、Cleaner、OwnerGate、Deliverer）；`ARCHITECT` 在这六个角色中增加 **Architect**，并用 OwnerGate 的独立结构验证阶段取代 Cleaner 的结构审查。角色数量不是质量指标，路线决定实际启用的角色。

### 1.1 适用范围

本规范、§6 的 Gate 前置条件、§11 的 Git 操作白名单与禁止事项、各角色的 Task Artifact 状态写入职责，以及 Session Handoff 契约，**只约束被明确激活的 Workflow Role Session**。激活条件（角色指定、Task 指定、以该角色职责运行）只在 `.agent/specs/WorkflowAgentDirective.md` §2 维护，本文件不复制。

- 未按该 Directive 激活的 **普通 Agent Session** 不自动成为 Workflow Role，也不自动受上述 Git 白名单、Gate 前置条件、Artifact 写入职责和 Handoff 契约约束；
- 普通 Agent 在 Owner 明确授权、且宿主自身安全策略允许时，可以进行正常代码开发、Commit 和 Push；这不表示可以绕过平台权限、服务器分支保护或宿主工程规范；
- 本文件不是仓库级 Git 规则，不改变宿主项目的分支保护、CI 与平台权限。

反过来，本节的隔离不放松任何已激活 Workflow Role 的现有权限：各角色的 Git 操作白名单与禁止事项保持原样。

## 2. 指令与事实来源

按以下顺序执行：

1. Owner 当前明确指令；
2. 当前任务的 Goal、Scope、Out of Scope 和 Acceptance Criteria；
3. Owner 已确认的 `contract.md` 与 `docs/design/*` 中的项目级长期设计；
4. 宿主根 `AGENTS.md` 中的宿主业务规范（存在时；它不是 Workflow 指令入口）；
5. `.agent/specs/WorkflowAgentDirective.md`、本规范、职责边界和角色 Prompt（仅在 Workflow Role 已激活时生效）。

代码、测试、日志和运行结果用于判断当前事实，不能自行改变任务目标。发现冲突时说明证据和需要 Owner 决定的事项，不暗自选边。

任何优先级都不能授权泄漏凭据、绕过认证授权、吞掉关键错误，或用降低测试标准制造成功。

## 3. Session Isolation 与事实来源

各 Agent 默认运行在独立 Session / 独立上下文。新 Agent 必须重新读取 Git、Task artifacts、Contract、Design、Registry、Review artifacts 与对应 commit SHA，禁止依赖「上一个 Agent 聊天里已经说过了」。

`ARCHITECT` 路线额外要求：结构验证（OwnerGate V1）必须由被**明确指定为该路线 OwnerGate** 的**新 Session** 执行，不得由 Architect 在同一 Session 内自证。该要求是 Session 隔离协议，Engine 无法认证作者身份。

原则：**Handoff 给路线，Git + Artifacts 给事实。**

## 4. 任务文件与长期 Design

任务级 Artifact 记录「本任务的临时事实与决策过程」，随任务结束而定稿。项目级长期 Design（`docs/design/*`）记录「跨越单个任务、长期稳定的项目级事实」。

```text
.agent/tasks/<task-slug>/
├── task.md
├── state.yaml          # 机器事实源（当前决策唯一权威）
├── contract.md         # 仅复杂任务需要
├── architecture.md     # 仅 ARCHITECT 路线需要
├── findings.md
├── core-logic.md
├── owner-decision.md
└── delivery.md
```

| 文件              | 用途                                     | 默认维护者          |
| ----------------- | ---------------------------------------- | ------------------- |
| `task.md`         | 目标、范围、AC、依赖关系、验证要求和任务基线 | TaskBuilder；Owner 可修改 |
| `state.yaml`      | 当前决策的唯一机器权威源（Evidence Binding） | 各角色按职责机械持久化 |
| `contract.md`     | 已确认的关键设计、不变量和错误语义       | Analyst             |
| `architecture.md` | `ARCHITECT` 的结构事实与结构验证入口（模块边界、依赖方向、不变量、`NOT_EXECUTED`） | Architect（验证阶段只读） |
| `findings.md`     | Review Finding 与复审结论                | Cleaner             |
| `core-logic.md`   | 值得 Owner 理解的核心逻辑和验证卡         | Cleaner             |
| `owner-decision.md` | Owner 核心逻辑决策记录                   | OwnerGate           |
| `delivery.md`     | 里程碑运行验收                           | Deliverer           |

Coder 负责生产代码和测试，不为展示过程而重复维护上述文档。

`architecture.md` 属于**实质结构事实**，不在 Review-neutral / Delivery-neutral 白名单内：它必须与结构骨架、结构测试一起进入 D1，D1 之后的任何修改都会使旧 Review Evidence 失效。

`state.yaml` 只保存 Git 无法推导的决策事实，**不保存** `phase`/`STALE`/`DONE`/`branch`/`HEAD`/`working_tree`/`changed_files`/`NEXT_ROLE`/`OWNER_ACTION`。详见 `docs/design/agent-workflow.md`。

### 4.1 Artifact × Role 输入所有权

哪个 Artifact 可能是哪个角色的输入是固定的；角色不得等上一角色"告知"本表已有的内容：

| Artifact | TaskBuilder | Analyst | Architect | Coder | Cleaner | OwnerGate | Deliverer |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `task.md` | 写 | 读 | 读 | 读 | 读 | 读 | 读 |
| `state.yaml` | 写 | 读写 | 读写 | 读写 | 读写 | 读写 | 读写 |
| `contract.md` | — | 写 | 读 | 读 | 读 | 读 | 读 |
| `findings.md` | 建空 | 读 | 读 | 读 | 写 | 读 | 读 |
| `core-logic.md` | 建空 | 读 | 读 | — | 写 | 读 | 读 |
| `owner-decision.md` | 建空 | 读 | 读 | 读 | — | 写 | 读 |
| `delivery.md` | 建空 | 读 | 读 | 读 | — | 读 | 写 |
| `architecture.md` | — | 读 | 写 | 读 | — | 读（V1 只读） | 读 |
| `loop-ledger.md` | 读 | 读写 | 读写 | 读写 | 读写 | 读写 | 读写 |

**硬规则**：

1. 角色只能在 Task Artifact、`state.yaml`、Git 与 Gate 输出中取事实；**不得**把上一角色的聊天叙述、历史 `HANDOFF_PROMPT` 或「口头说明」当作事实来源。
2. 失败原因必须先落到上表对应的 Artifact（Cleaner `findings.md`、OwnerGate `owner-decision.md`、Deliverer `delivery.md`、其他角色 `loop-ledger.md`），再发出交接；没有落盘的失败原因视为未发生。
3. 角色不得要求 Owner 提供本表已能读取的信息（分支、基线、SHA、mode、Finding 编号）。

### 4.2 角色启动与阶段判定

角色的启动串、开工声明、阶段推导与 `mode` 推导统一由 `.agent/specs/WorkflowAgentDirective.md` §2.1–§2.4 定义（含 §2.2 阶段推导表）。本节与各角色 Prompt 只引用，不复制推导表。

## 5. 默认路由

标准路由：

```text
Owner 提出任务目标 / 清单（可显式指定 Slug、分支与基线）
→ TaskBuilder 自动发现基线、生成名称并创建当前任务初始分支
→ TaskBuilder
→ Analyst（Contract Decision / 如需资源 Reservation）
→ Coder
→ Cleaner
→ OwnerGate（Owner Core Logic Decision）
→ Deliverer
→ Owner（决定何时/如何集成）
```

Feature Branch 的成立方式有两种：Owner 预先创建并在其上开始；或 Owner 明确要求创建 / 初始化 / 执行任务后，由 TaskBuilder 按 `TB-BRANCH-1` 以**当前检出分支与当前 HEAD**为默认基线，自动生成 Task Slug 与 Feature Branch 名称，并创建**当前任务**的初始分支。Owner 显式给出的 Slug、分支或基线是上层约束；未给出时不要求补齐。授权范围与边界见 §11.1。

简单任务（无需独立 Contract/Design，`contract.status = NOT_REQUIRED`）可 `TaskBuilder → Coder`。TaskBuilder 不能为省流程擅自跳过 Analyst。

三条路线（`NORMAL` / `COMPLEX` / `ARCHITECT`）由 **TaskBuilder** 按 `.agent/specs/WorkflowRouteSelection.md`（TB-ROUTE-1）判定并写入 `state.yaml.route` 与 `task.md` 的 `## Complexity`。Owner 明确指定的路线是硬约束，TaskBuilder 必须遵守；Owner 未指定时，TaskBuilder 必须独立判定，包括独立识别 `ARCHITECT`。不存在「`ARCHITECT` 只能由 Owner 指定」的限制；改变已记录的路线仍只能由 Owner 要求。

`ARCHITECT` 路线交付架构设计与结构性成果，不交付完整业务功能：

```text
TaskBuilder 判定路线（TB-ROUTE-1）并记录 Owner 授权的基线
→ TaskBuilder（contract.status=PENDING）
→ Analyst（A1/A2，Contract 限定结构交付）
→ Architect（D1 结构快照 → D2 Review Metadata）
→ OwnerGate（新 Session：V1 独立结构验证 → V2 Owner 决策）
→ Deliverer（结构交付验收）
→ Owner（决定何时/如何集成）
```

该路线**不经过 Cleaner Role**：结构成果的独立验证由 OwnerGate 在 V1 阶段执行（入口 `cleaner-start`，验证后运行 `owner-gate-start`）。不新增 Gate，也不伪造 `CLEAN`：`review.status=CLEAN` 仍由验证方在真实验证后机械写入。

`ARCHITECT` 只能在 Task 与已批准 Contract 都限定为结构交付时使用。需要完整业务功能、数据库 Migration、未批准的 DB Schema 变更、新外部依赖或构建/部署模型变化时，Architect 停止并回交 Analyst/Owner，由 Owner 明确决定是否切换到 `COMPLEX`；切换后必须重新核对 Contract 与 Evidence，`ARCHITECT` 的 `CLEAN` 不得当作 `COMPLEX` 的 Cleaner 结论。

Owner Checkpoint（`WAITING_FOR_OWNER_DECISION`）不是跨角色 Handoff：不结束当前 Session，也不产生新的接收者。出站层按 `.agent/specs/WorkflowHandoffContract.md` §5 用 `NEXT_ROLE=OWNER` + `OWNER_ACTION=DECIDE:` 标记该检查点；这些字段不写入 `state.yaml`，Owner 回复后原 Agent 在同一 Session 继续。

## 6. 阶段关口（Gate）

Gate 是 Handoff 前的机械出站检查。角色 Handoff 前运行的 Gate 就是**下一角色的入口 Gate**；Gate 语义与实现以 `docs/design/agent-workflow.md` §12 为准，CLI 与 exit code 见 §11。

### 6.1 逐角色交接矩阵

| 路线 | 角色 | 入口 Gate | 本角色产出 Commit | Push | 出口 Gate | Handoff | 失败回退 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| NORMAL | TaskBuilder | 无（自动基线发现 + `TB-BRANCH-1` 预检） | 任务 Artifact 初始化（`contract.status=NOT_REQUIRED`） | 是 | `coder-start` | Coder | 修复本角色 Artifact 或 Git 收尾；超出 authority 则 `BLOCKED` |
| NORMAL | Coder | `coder-start` | C1 实现快照 + C2 Review Metadata（`PENDING` / `target=C1`） | 是 | `cleaner-start` | Cleaner | 修实现与证据；Contract/Scope 问题 `BLOCKED` |
| NORMAL | Cleaner | `cleaner-start` | CLEAN artifacts 或 CHANGES_REQUIRED artifacts | 是 | `owner-gate-start` / 无（退回） | OwnerGate / Coder | 补自身证据；实现缺陷 `CHANGES_REQUIRED` |
| NORMAL | OwnerGate | `owner-gate-start` | `owner-decision.md` + `owner.*` | 是 | `delivery-start` / 无（REJECT） | Deliverer / Coder 或 Analyst | Owner 未决保持 `PENDING`；REJECT 持久化后退回 |
| NORMAL | Deliverer | `delivery-start` | `delivery.md` + `delivery.*`（三 SHA 绑定） | 是 | `merge-ready` | Owner | `FAIL` / `BLOCKED` 持久化后退回 |
| COMPLEX | TaskBuilder | 无（自动基线发现 + `TB-BRANCH-1` 预检） | 任务 Artifact 初始化（`contract.status=PENDING`） | 是 | 无（只读同步自检，不运行 `coder-start`） | Analyst | 修复 Artifact 或 Git 收尾 |
| COMPLEX | Analyst | 无（TaskBuilder 直接交接） | A1 Contract Evidence + A2 Approval Metadata | 是 | `coder-start` | Coder | Owner 未批准保持 `PENDING`；REJECT / 实质修订重新形成 A1 |
| COMPLEX | Coder | `coder-start` | C1 实现快照 + C2 Review Metadata | 是 | `cleaner-start` | Cleaner | 同 NORMAL |
| COMPLEX | Cleaner | `cleaner-start` | CLEAN artifacts 或 CHANGES_REQUIRED artifacts | 是 | `owner-gate-start` / 无（退回） | OwnerGate / Coder | 四者一致性与 Contract 覆盖不足时 `CHANGES_REQUIRED` |
| COMPLEX | OwnerGate | `owner-gate-start` | `owner-decision.md` + `owner.*` | 是 | `delivery-start` / 无（REJECT） | Deliverer / Coder 或 Analyst | 同 NORMAL |
| COMPLEX | Deliverer | `delivery-start` | `delivery.md` + `delivery.*` | 是 | `merge-ready` | Owner | 同 NORMAL |
| ARCHITECT | TaskBuilder | 无（自动基线发现 + `TB-BRANCH-1` 预检） | 任务 Artifact 初始化（`contract.status=PENDING`） | 是 | 无（只读同步自检，不运行 `coder-start`） | Analyst | 修复 Artifact 或 Git 收尾 |
| ARCHITECT | Analyst | 无（TaskBuilder 直接交接） | A1 Contract Evidence（限定结构交付）+ A2 Approval Metadata | 是 | `coder-start` | Architect | 同 COMPLEX |
| ARCHITECT | Architect | `coder-start`（复用，开工前重新确认） | D1 结构快照 + D2 Review Metadata（`PENDING` / `target=D1`） | 是 | `cleaner-start` | OwnerGate（V1，新 Session） | 修结构骨架与证据；Contract/Scope 问题 `BLOCKED` |
| ARCHITECT | OwnerGate V1 | `cleaner-start` | `findings.md`、`core-logic.md`、`review.status=CLEAN`（`target` 保持 D1） | 是 | `owner-gate-start` | OwnerGate V2（同 Session） | 结构缺陷 `CHANGES_REQUIRED` → Architect；Contract 问题 `BLOCKED` |
| ARCHITECT | OwnerGate V2 | `owner-gate-start` | `owner-decision.md` + `owner.*`（`review_target=D1`） | 是 | `delivery-start` / 无（REJECT） | Deliverer / Architect 或 Analyst | Owner 未决保持 `PENDING`；REJECT 持久化后退回 |
| ARCHITECT | Deliverer | `delivery-start` | `delivery.md` + `delivery.*`（三 SHA 绑定） | 是 | `merge-ready` | Owner | 同 NORMAL；结构缺陷按 `STRUCTURAL_DEFECT` 退回 |

`handoffReady`（非 detached、clean、remote 存在、local==remote）是每个出口 Gate 的共同前提。

上表「本角色产出 Commit」列出的每个 Commit 都必须归入 `.agent/specs/WorkflowGitLifecycle.md` §2 的六种合法类型之一；其中 A1 / C1 / D1 只是**可独立验证的阶段快照**，A2 / C2 / D2 只允许写 `state.yaml`。它们都不等于 `SESSION_COMPLETE`，也不允许被描述为已审查、已接受或可交付。任何未验证的临时保存（`PROGRESS`）一律禁止。

TaskBuilder **没有专用入口 Gate**：它的通用出口是 **Commit + Push + 远端同步核验**，不是 Gate。`NORMAL` 路线在上述出站收尾之后再运行既有的 `coder-start`，作为 Coder 入口 Gate 的提前自检；`COMPLEX` / `ARCHITECT` 在 `contract.status = PENDING` 时不运行 `coder-start`，直接交 Analyst（该 Gate 要求 Contract 为 `APPROVED` 或 `NOT_REQUIRED`）。下游 Coder / Architect 仍须在开工前按各自协议确认入口 Gate。

`ARCHITECT` 不新增 Gate：`coder-start`、`cleaner-start`、`owner-gate-start`、`delivery-start`、`merge-ready` 五个 Gate 与语义保持不变。`cleaner-start` 在该路线下的含义是「Review Request 已稳定落 Git」，由 OwnerGate 的 V1 结构验证阶段消费，不是「交给 Cleaner Role」。

正常 HANDOFF 的统一前提是：

- 当前角色职责已经全部完成；
- 本角色 owned artifacts 已持久化；
- 应 commit 的已 commit；
- 应 push 的已 push；
- working tree clean；
- remote synced（local HEAD == `origin/<current-feature>`）；
- 下一角色入口 Gate 已 `PASS`；
- 然后才能输出最终 `HANDOFF`。

角色得到业务结果（如 Cleaner 的 `CLEAN`、Deliverer 的 `PASS`）只是阶段结论，不等于本轮完成：必须先完成上述出站收尾并通过下一角色入口 Gate。Gate `FAIL` 时不结束 Session，继续完成本角色职责，超出本角色 authority 才输出 `BLOCKED`。Decision Checkpoint（`WAITING_FOR_OWNER_DECISION`）不是 Handoff，期间不得结束 Session。

### 6.2 出站顺序

所有路线的 Git 出站统一按以下顺序执行，不得颠倒：

1. 本角色职责与产物完整落盘，本阶段验证真实执行；
2. `git status --short` / `git diff` 确认没有其他角色的未提交修改；需要时运行 `.agent/specs/tools/git-lifecycle-check preflight --role <Role> --task <task>`；
3. 显式 stage 本角色 owned artifacts，`git diff --cached --name-status` 核对无越权文件；
4. 只提交已定义的 Commit 类型：`BOOTSTRAP` / `EVIDENCE` / `METADATA` / `ROLE_ARTIFACT` / `FORCED_EVIDENCE` / `REGISTRY_ONLY`（内容 Evidence 与 Metadata 分两阶段，见各角色 Prompt）；
5. 普通 push 当前 feature branch；
6. `git rev-parse HEAD` == `git ls-remote --heads origin refs/heads/<current-feature>` 的实际远端 SHA，且 `git status --short` 为空；
7. 运行下一角色入口 Gate；
8. Gate `PASS` 后才输出 HANDOFF。

「已提交」「已 push」「已写报告」都不构成 Gate 结论，也不构成 Handoff 依据。**不得提交未完成工作的普通进度保存（`PROGRESS`）**；A1/C1/D1 等中间 Evidence Commit 只有在满足 `.agent/specs/WorkflowGitLifecycle.md` §4.3 的全部完成条件时才允许存在。

Push 失败、远端 SHA 不一致、工作区 dirty 或 Gate `FAIL` 时：不得 HANDOFF，不得宣称 `REMOTE_SYNCED=YES`；按 `.agent/specs/WorkflowGitLifecycle.md` §8 处置。`origin/<current-feature>` 是本地 tracking ref，远端事实必须用 `git ls-remote` 核验。

`ARCHITECT` 路线的 OwnerGate 必须分两次出站：V1 结构验证证据（`findings.md` / `core-logic.md` / `state.review`）先 commit + push + 远端核验，再运行 `owner-gate-start`；V2 的 Owner 决策（`owner-decision.md` / `state.owner`）再单独 commit + push。不得把两个阶段合并成一次提交，也不得在 V1 尚未落 Git 时请求 Owner 决策。

## 7. 证据、基线与 Scope

- TaskBuilder 记录任务开始时的 Git 基线和已有修改；经 `TB-BRANCH-1` 创建分支时，基线由当前检出的分支与当前 HEAD 自动发现（Owner 显式指定时以其为约束），不得擅自切换到 develop / main，也不得用远端最新 SHA 替换当前 HEAD。发现的分支、完整 SHA、upstream 与领先 / 落后 / 分叉状态必须写入 `task.md` 的 `Review Baseline`。
- Coder 保留已有工作，只修改当前 Scope 所需内容。
- Cleaner 审查 `review.target` 之后的全部相关变更；不能只看默认 `git diff`。
- `CLEAN` 只对 `review.target`（immutable commit）有效。生产代码、测试或约束发生实质变化后必须复审。
- Deliverer 验证的版本必须与最近一次 `CLEAN` 对应，并记录 `feature_head` 与 `develop_base`。
- 未执行的检查标记为 `NOT_VERIFIED` 或 `NOT_EXECUTED`，并说明原因与影响。

### 7.1 Evidence SHA 写入规范（F-03）

新证据一律写入**完整 40 位 Commit SHA**。

不得写入 `HEAD`、`HEAD~1`、分支名、Tag、`origin/<branch>` 或其他可移动 Git Ref；也不得写入无法唯一确定 Commit 的缩写。受此约束的字段至少包括 `contract.target`、`review.target`、`owner.review_target`、`delivery.review_target`、`delivery.feature_head`、`delivery.develop_base`，以及 Handoff 中声明的 A1 / A2 / D1 / D2 / C1 / C2 / feature HEAD。

Engine 为保护历史兼容仍接受合法形态的 7～40 位十六进制短 SHA；这不意味着新任务可以主动使用短 SHA。所有引用必须先解析并核验真实身份：是真实 commit、位于当前 feature 历史中、并满足该字段的绑定语义。完整 SHA 等值比较用于判定绑定是否一致，不得因历史短 SHA 与完整 SHA 字面值不同而误判为不同绑定。

历史任务中若存在可移动引用，属于升级迁移问题，由 Adoption / 升级说明处理；不得为兼容非法历史 Evidence 而放宽 Engine。

需要突破 Scope 才能正确完成时，停止扩张并交 Owner 决定。不得覆盖来源不明的已有修改，也不得把别人的修改算作自己的成果。

### 7.2 任务依赖模型（TB-DEP-1）

TaskBuilder 在任务初始化时区分三类依赖，并写入 `task.md` 的 `## Dependencies`：

| 类型 | 含义 | 上游未交付时 |
| --- | --- | --- |
| Blocking | 当前任务的 Goal / AC 必须使用上游已审查通过的结论或产物 | 不初始化、不 HANDOFF；`BLOCKED` + `UPSTREAM_NOT_DELIVERED` |
| Reference-only | 上游只提供参考，当前任务可独立调查与定义 | 继续初始化；记录未交付，不引用未审查结论 |
| Parallel | 无交付依赖，可并行执行 | 正常初始化 |

- 依赖必须来自 Owner 的任务清单与真实交付证据，不按任务编号顺序假设；
- 不得把 Blocking 依赖降级为 Reference-only 以求开工，也不得把 Reference-only 依赖伪装成已满足；
- 依赖只记录在 `task.md`，不新增 `state.yaml` 字段，不引入依赖图引擎；
- 每个任务仍独立走自己的路线与 Gate；Parallel 不等于免审批。

## 8. Finding

Cleaner 使用稳定编号：`CLEAN-001`、`CLEAN-002`……

| 等级 | 含义                               | 是否阻塞 CLEAN |
| ---- | ---------------------------------- | -------------- |
| P0   | 安全漏洞、数据损坏、严重不可用     | 是             |
| P1   | 核心业务行为错误                   | 是             |
| P2   | 特定场景错误、可靠性或明显维护风险 | 是             |
| P3   | 低风险改进                         | 由 Owner 决定  |

Finding 必须说明触发条件、实际行为、预期行为、影响、证据和修复边界。只有 Cleaner 创建、关闭或重新打开 Finding；Coder 负责修复并提供回归证据。

## 9. Owner 核心验证（OwnerGate）

Owner 不需要逐行 Review 全部代码。交付应帮助 Owner 理解：

- 关键业务不变量是什么；
- 生产代码在哪里实现；
- 哪个测试证明它；
- 破坏该不变量时，测试能否失败。

`NORMAL` / `COMPLEX` 下由 Cleaner 审查测试可信度，在 `core-logic.md` 整理核心验证卡；OwnerGate 据此提炼 1~3 个核心机制、解释并请求 Owner 决策，再把结果持久化到 `owner-decision.md` 与 `state.owner`。

`ARCHITECT` 下没有 Cleaner：OwnerGate 在其 V1 独立结构验证阶段复核结构测试可信度并整理结构验证卡，再在 V2 请求 Owner 决策。Owner 接受的是 D1 这个结构快照（`owner.review_target = review.target = D1`），不是「业务已实现」。

`owner.status = ACCEPTED` 只在 Owner 明确接受指令下由 OwnerGate 机械持久化，并绑定当前 `review.target`（`owner.review_target`）。任何 Agent 不得因 `CLEAN`、测试通过或阅读聊天上下文自行把 `PENDING` 置为 `ACCEPTED`。

## 10. 中文与交接表达

- Agent 面向 Owner 的分析、说明、文档和最终输出默认使用中文。
- 结论先行，只保留完成情况、关键证据、风险和下一步；省略重复背景、普通样板代码和可直接从 Diff 获得的完整文件清单。
- 只输出与当前结果有关的章节，不为满足模板制造空段落。
- 状态标识、代码标识符、API 字段、命令和协议名可以保留英文，但要用中文解释。
- 表格只用于映射和对比；简单交接优先使用短列表。

详细证据应写入对应任务文件、测试或 Git Diff。聊天交接用于帮助 Owner 快速判断，而不是复制所有过程记录。

## 11. Git 权限与最终决定

本节只适用于已激活的 Workflow Role Session（范围见 §1.1），不构成对宿主其他 Agent 或仓库级 Git 操作的通用规则；除 §11.1 的 TaskBuilder 窄例外外，不改变 Workflow Role 现有权限。

- Agent 允许：`fetch / status / diff / log / show / rev-parse`、`switch` 到已存在且属于当前任务的分支、`pull --ff-only` 当前 feature、显式 stage 本角色 owned artifacts、`commit`、普通 `push` 当前 feature branch（共享集成分支与 `main` 除外）。Commit 与 Push 是正常 Handoff 的必要动作，不是需要额外授权的例外。
- Agent 禁止：删除/重命名 branch、merge feature→develop、`push --force`、`reset --hard`、`clean -fd`、`git add -A` / `git add .`、rebase 已发布历史、`commit --amend`、rewrite history。`push develop` 默认禁止——唯一例外是 Analyst 的 Registry-only develop commit（仅 `.agent/workflow.yaml` 已声明的 Registry 文件，见 §12.2）。
- 创建 branch 默认禁止；唯一例外是 TaskBuilder 对当前任务的初始分支创建（`TB-BRANCH-1`，见 §11.1）。Owner 明确要求创建 / 初始化 / 执行任务即构成该窄授权，不需要 Owner 重复给出分支名或额外授权表述。
- 这些 git 写约束是协议/Prompt 硬约束；shared develop 的机器级保护交给仓库 branch protection / 外部 Git 控制能力。`.agent/bin/workflow-check` 只读，不拦截 git 写命令。
- 本规范不增加 Git Hook、不修改仓库全局配置、不拦截开发者日常 Git 操作。`git commit` / `git push` 的时机约束是 **SPEC_ONLY**，不得描述为 Engine 强制；可复核的防线是 Gate 与真实 Git 证据，以及 `.agent/specs/WorkflowGitLifecycle.md` §12 的只读前置自检。
- 标准 commit 前流程：`git status --short` → `git diff` → `git add <明确属于本角色的文件>` → `git diff --cached --name-status` → `git commit`。发现其他角色遗留修改 → STOP，不得顺便提交。Commit 类型必须属于 `.agent/specs/WorkflowGitLifecycle.md` §2 的合法类型。
- Agent-generated commit message 默认使用中文摘要，保持 Conventional Commit 结构：`<type>(<scope>): <中文摘要>`。`type` / `scope` 保留英文关键字（常用 `feat` / `fix` / `refactor` / `test` / `docs` / `chore`），中文只用于 summary。不要求 summary 必须英文，不因中文 commit message 阻塞流程。

Owner 决定关键业务规则、Contract、是否接受核心逻辑、是否启动 Deliverer，以及最终接受、Commit、Merge、Push 和 Deploy。

### 11.1 TaskBuilder 任务初始分支创建例外（TB-BRANCH-1）

权威的基线发现、命名、预检序列与结果码在 `.agent/roles/TaskBuilderPrompt.md` 的 `TB-BRANCH-1` / `TB-BASE-1` / `TB-NAME-1` 章节维护；本节只固定授权边界：

- Owner 继续拥有共享集成控制权；Owner 显式指定的分支名与基线是上层约束，TaskBuilder 必须遵守并验证；
- Owner 未指定时，TaskBuilder 以**当前检出分支与当前 HEAD**为默认基线，自动生成 Task Slug 与 `task/<task-slug>` 分支名；不得擅自切换到 develop / main，不得用远端 SHA 替换当前 HEAD，也不得因缺少名称而要求 Owner 补充；
- Owner 明确要求创建、初始化并执行任务，即构成本任务初始分支创建的窄授权；授权只覆盖：创建当前任务分支、写入任务初始 Artifact、Commit 当前任务目录、普通 Push 该分支、核验远端；
- 该例外不延伸到：删除或重命名分支、Merge、Rebase、Force Push、Push `main` / `master` / 共享集成分支、修改其他任务或角色的 Artifact、Push 共享 Registry；
- 除该例外，其他 Workflow Role 的 Git 白名单与禁止事项完全不变；Workflow Role 也不因该例外获得 Merge、Push main、Force、Reset 权力；
- `TB-BRANCH-1` 的结果码（`OWNER_INPUT_REQUIRED`、`BASE_UNRESOLVED`、`BASE_MISMATCH`、`BRANCH_ALREADY_EXISTS`、`TASK_ALREADY_EXISTS`、`TASK_DIR_CONFLICT`、`REMOTE_NOT_SYNCED`、`BOOTSTRAP_HELD`、`BOOTSTRAP_COMPLETE`）是 Prompt 层输出标识，不进入 `state.yaml` schema，也不改变 Engine Gate；
- 工作区已有修改按「保留与隔离」处理：不得 `stash` / `reset --hard` / `clean` / 覆盖；保留内容使工作区无法 clean exit 时，`BOOTSTRAP_HELD` 必须如实报告，不得声称 HANDOFF；
- 该例外不改变 §1.1：普通 Agent Session 的 Git 权限仍由 Owner 指令与宿主安全规则决定，不被 Workflow Role 白名单自动约束。

## 12. 跨任务全局资源预留

### 12.1 定义与分类

「全局资源」指多个并行 Task 可能同时申请、重复会导致冲突或语义错误的项目级命名资源：

| 类别 | 判定标准 | 治理 | 示例 |
| --- | --- | --- | --- |
| A 强全局唯一 | 重复导致构建/迁移/运行或语义冲突 | 纳入 Registry（强治理） | migration version、错误码域/编号 |
| B namespace | 通常靠命名空间避免，重复有潜在静默冲突 | 只分类、不纳入 | permission code、Redis key 前缀、config key、route prefix、MQ topic |
| C 局部命名 | 单任务内部、无跨任务冲突 | 禁止注册 | 局部变量、模块内函数、内部 struct、普通测试 fixture、临时命名 |

C 类**不得以任何形式要求注册**；新增资源类型进入治理须有明确判定标准且经 Owner 确认。

### 12.2 共享事实源与 Reservation 流程

`.agent/registry/`（`develop` 上）是全局资源分配状态的权威事实源。机器配置 `.agent/workflow.yaml` 的 `resources.<kind>.registry` 声明当前项目有哪些 Shared Resource Kind 及各对应哪个 Registry 文件（当前 my-shop 为 `migration_version → .agent/registry/migrations.md`、`error_code_domain → .agent/registry/error-codes.md`）。Analyst 的 Registry-only shared-develop mutation 仅限该配置已声明的 Registry 文件。

需要资源时，Analyst 读取 `.agent/workflow.yaml` 确认当前项目已声明哪些 Shared Resource Kinds → fetch `origin/develop` → 读取对应 Registry → 计算 candidate value → 检查空闲 → 将 candidate 写入 Contract 并请求 Owner Contract Decision。Contract APPROVED 后，Analyst 形成 Registry-only commit（仅含配置已声明的 Registry 文件）并 push `origin develop` 落实 `RESERVED`。push 前必须确认 staged/commit diff 只含这些合法 Registry 文件，出现任何其他路径 → 立即 STOP；push 因 non-fast-forward 被拒时禁止 force，改为 fetch 最新 develop → 重新读取 Registry → 重新计算资源 → 重新形成合法 reservation。落 `RESERVED` 后，Analyst 重新读取 `origin/develop` Registry 验证：资源存在、owner 正确、状态 `RESERVED/ACTIVE`，通过后才 HANDOFF Coder。

该提交类型是 `REGISTRY_ONLY`（定义见 `.agent/specs/WorkflowGitLifecycle.md` §2）：它不是交付完成，不构成 HANDOFF，也不授权任何其他 develop 写入；staged / commit diff 出现 Registry 之外的路径时必须 STOP。

Feature Branch 内自行声明 `RESERVED` 不构成有效预留；`state.yaml.resources.reservations` 只记录「需要什么」，不自证满足。

### 12.3 生命周期

状态三态：`RESERVED`（已落 develop、Feature 未合并）、`ACTIVE`（已合并，终态）、`RELEASED`（取消释放，记录保留）。

复用规则属于当前 my-shop 的 Project Policy 示例，Generic Core 本身不规定所有 resource kind 的复用策略：migration version 一经分配永久 tombstone、不得复用；错误码域仅纯 `RESERVED` 阶段可 `RELEASE` 后复用。

### 12.4 并行竞争防护

无外部锁、无独立服务。以 Git 提交顺序 + 冲突检测串行化：先提交到 `develop` 者胜，后提交者必须重新申请不同值。

### 12.5 一致性与检测

Coder 实现中出现的每个全局资源都必须已存在于 APPROVED Contract 的全局资源清单（禁止自行 `max+1` 占号）。实现阶段新增资源需求 → 走 Contract Revision。

Cleaner 做三边一致性检查：Registry ↔ Contract ↔ 实现，任一漂移 → `CHANGES_REQUIRED`。

`scripts/check-registry.sh` 提供最小只读机械校验；`.agent/bin/workflow-check gate coder-start` 以 shared `develop` Registry 为权威机械验证 Reservation。
