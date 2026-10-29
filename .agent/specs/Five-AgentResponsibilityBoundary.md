# 角色职责边界

## 1. 总则

各角色是能力分工，不是每个任务都必须走完的组织架构。命名沿用历史（`Five-Agent` 指 V1 的五个执行角色），当前职责表覆盖 `NORMAL` / `COMPLEX` 的六个角色与 `ARCHITECT` 额外启用的 Architect。

```text
TaskBuilder：定义要做什么
Analyst：解决关键设计选择
Architect：只在 ARCHITECT 路线交付结构成果
Coder：实现并自测
Cleaner：独立审查实现与测试
OwnerGate：解释核心机制并请求 Owner 决策（ARCHITECT 下先做独立结构验证）
Deliverer：验证重要里程碑能否真实运行
Owner：做决定并最终接受
```

`ARCHITECT` 路线的角色序列是 `TaskBuilder → Analyst → Architect → OwnerGate（V1 结构验证 → V2 Owner 决策）→ Deliverer → Owner`，**不经过 Cleaner**，也不新增 Gate。

Owner 不是第七个 Agent，而是整个 Workflow 的 **Decision Authority + Shared Repository Authority**。

共享流程、状态和文件规则见 `.agent/specs/AgentCollaborationSpecification.md`。Git 的 Stage / Commit / Push / Gate / HANDOFF 时机与七角色例外矩阵见 `.agent/specs/WorkflowGitLifecycle.md`；跨 Session HANDOFF 字段见 `.agent/specs/WorkflowHandoffContract.md`。各角色 Prompt 只说明本角色怎样工作，不重复整套工程制度。

本文件描述的角色边界、Gate 与 Handoff 约束只适用于按 `.agent/specs/WorkflowAgentDirective.md` §2 明确激活的 Workflow Role Session。普通 Agent Session 不因本文件存在而自动获得 Workflow Role 职责，也不自动受其 Git、Gate 与 Artifact 约束。

Workflow V2 状态模型（`state.yaml` schema、Evidence Snapshot / Neutral Tail、五个 Gate、INV-1 ~ INV-7、Global Registry 状态机、Handoff Contract）见 `docs/design/agent-workflow.md`。当前状态唯一机器权威源是 `.agent/tasks/<task-slug>/state.yaml`。

## 2. Owner

Owner 回答：我要什么、哪些重要方案可以接受、哪些核心逻辑必须掌握、是否最终接受？

Owner 负责：

- 决定 Goal、Scope、Out of Scope 和 Acceptance Criteria；
- 决定 Task / Branch Boundary：新任务还是延续、是否开新 feature、branch 名称与基线；需要时把「创建当前任务初始分支」这一窄操作按 `TB-BRANCH-1` 明确授权给 TaskBuilder；
- 确认复杂任务的关键方案（Contract Decision）；
- 阅读核心逻辑说明，做 Core Logic Decision（ACCEPT / REJECT / 继续询问）；
- 决定 Shared Integration：何时/如何 merge、merge 顺序、squash 方式、并行依赖；
- 决定最终 feature→develop integration 的 Commit、Merge、Push 和 Deploy。

Owner 不修改 `contract.md`、`owner-decision.md`、`state.yaml`（由对应角色在明确指令下机械持久化）。Owner 不必逐行代替 Cleaner Review，也不因 Agent 给出 `CLEAN` 或 `PASS` 自动接受任务。

## 3. TaskBuilder

TaskBuilder 回答：本次究竟交付什么，怎样判断完成？

负责：

- 把 Owner 需求整理成可执行、可验收的 `task.md`；
- 明确 Scope、Out of Scope、AC、验证要求和 Git 基线；
- 区分事实、合理假设和需要 Owner 决定的问题；
- 判断 Design Impact（`NONE` / `UPDATE` / `NEW`）；当为 `NEW/UPDATE` 时在 `task.md` 声明目标 Design Artifact，并纳入 Scope、Deliverables 与 AC；
- 判断任务直接交 Coder，还是先交 Analyst（Complexity）；
- 声明全局资源需求（类型 + 语义），不写具体域号 / version 号；
- 在 Owner 明确授权下执行 `TB-BRANCH-1`：创建当前任务的初始 Feature Branch、初始化任务 Artifact、Commit、普通 Push，并核验远端 SHA 与工作区状态。

不负责：写生产代码、替 Analyst 完成复杂设计、Review 实现，或替 Owner 创造关键业务规则。

TaskBuilder 默认不拥有分支生命周期，目标 branch 由 Owner 预先准备。唯一例外是 `TB-BRANCH-1`：Owner 逐项给出完整分支名、起始分支、起始 Commit SHA、Task Slug 并明确授权后，TaskBuilder 可为**当前任务**创建初始 Feature Branch 并完成首次 Commit / Push。TaskBuilder 仍禁止删除或重命名分支、Merge、Rebase、Force Push、Push `main` 或共享集成分支；该例外不改变 Engine、Gate 与 `state.yaml` schema。

TaskBuilder 的提交类型是 `BOOTSTRAP`：只允许任务初始 Artifact（`task.md`、`state.yaml` 及该目录内约定文件），且必须在本任务定义、状态初始化与（如执行）`TB-BRANCH-1` 预检全部完成后提交。不得把未写完的 `task.md` 草稿、探索性修改或「先存一下」的修改作为进度提交；出站完成前不得 HANDOFF。

## 4. Analyst

Analyst 回答：复杂问题有哪些约束，可行方案是什么，哪些性质必须保持？

仅在存在真正的设计选择、根因不明、安全、协议、并发或一致性风险时启用。

负责：

- 调查当前代码、配置、数据和测试；
- 比较少量真正可行的方案并给出推荐；
- 明确业务不变量、错误语义和验证方法；
- 起草 `contract.md`，请求 Owner Contract Decision；
- Owner 确认后，准确记录最终决定（两段式提交：A1 contract 内容 → A2 只写 `state.contract.status=APPROVED`、`state.contract.target=<A1 完整 SHA>`），随后 Commit + Push 当前 feature、核验远端同步并运行 `coder-start`，`PASS` 后才 HANDOFF Coder；Owner 未批准或拒绝时不得写 `APPROVED`，也不得改成 `NOT_REQUIRED` 绕过；
- 作为长期 Design（`docs/design/*`）内容的主责：`Design Impact = NEW/UPDATE` 时，在 Contract 经 Owner `APPROVED` 后、Coder 实现前新增/更新对应 Design Artifact；
- 读 `.agent/workflow.yaml` 确认已声明的 Shared Resource Kinds，再读对应 `.agent/registry/*` 派生候选资源，在 Contract APPROVED 后形成 Registry-only commit（仅含配置已声明的 Registry 文件）并 push `origin develop` 落实 `RESERVED`，然后验证 Reservation 真正进入 `origin/develop`；不得凭空自选编号，不得自行新增 resource kind 或修改 `.agent/workflow.yaml`。

不负责：修改生产代码、测试或任务目标；不能批准自己的推荐方案；除 Registry-only 例外（仅 `.agent/workflow.yaml` 已声明的 Registry 文件）外，不修改 shared develop 上的其他任何内容。

Analyst 的提交类型：A1 是 `EVIDENCE`（`contract.md` + 必要的 `docs/design/*`，不含 `state.yaml`），A2 是 `METADATA`（只含 `state.yaml`），共享资源是独立的 `REGISTRY_ONLY`。A1 只有在 Contract 内容与 Owner 明确批准一致、Design 已按批准内容更新、且不含业务代码时才允许提交；A2 只有在 A1 的完整 SHA 可解析、`contract.target` 写入正确时才允许提交。三者都不是 HANDOFF。

Analyst 是 Reservation 的语义负责人，并持有唯一的 Registry-only develop mutation authority（Registry-only Develop Authority）；Owner 仍独占 branch 生命周期与最终 feature→develop integration。

Analyst 的 Registry-only shared-develop mutation 仅限 `.agent/workflow.yaml` 声明的 Registry 文件（当前 my-shop 为 `.agent/registry/migrations.md` 与 `.agent/registry/error-codes.md`）。正常任务期间 Analyst 只读 `.agent/workflow.yaml`，不修改它。

## 5. Architect（仅 ARCHITECT 路线）

Architect 回答：在已批准 Contract 下，系统应该具有什么结构，结构是否成立？

负责：

- 开工前重新确认入口 Gate `coder-start`（复用既有 Gate，表示「已获准按 Contract 动手」，不使其成为 Coder）；
- 在 APPROVED Contract 限定的范围内交付结构成果：`.agent/tasks/<task>/architecture.md`、模块 / 包 / 接口骨架、结构性编译与结构测试、依赖方向验证、已批准 Contract 内的配置 Schema、模块边界与兼容性证明；
- 在 `architecture.md` 说明架构目标与非目标、模块与职责边界、接口与依赖方向、关键不变量、Contract / AC 对应关系、结构验证与复现命令、风险与开放问题、未实现的业务行为；
- 执行真实构建、格式化、静态检查与结构测试；未执行的业务检查标记 `NOT_EXECUTED`，不得写成 PASS；
- 两段式提交：D1（结构快照 + `architecture.md` + 结构测试）→ D2（只写 `state.review.status=PENDING`、`review.target=<D1 完整 SHA>`），D1、D2 都 commit + push 并核验远端同步，再运行 `cleaner-start`；
- `cleaner-start` `PASS` 后 HANDOFF → OwnerGate（ARCHITECT 结构验证阶段，新 Session）。

Architect 的提交类型：D1 是 `EVIDENCE`（结构骨架 + `architecture.md` + 结构测试，不含 `state.yaml`），D2 是 `METADATA`（只含 `state.yaml`）。D1 必须在构建、格式化、静态检查与结构测试真实执行后才允许提交；未实现的业务行为必须标 `NOT_EXECUTED`。`architecture.md` 属于实质结构事实，D1 之后修改它必须形成新的 D1；D1/D2 都不是 HANDOFF。

不负责：完整业务功能、数据库 Migration、未获批准的 DB Schema 变更、新外部依赖、未获批准的构建 / 部署模型变化；也不负责写 `CLEAN`、`ACCEPTED`、`PASS`，不自行完成或代替结构验证。

出现超出结构交付的需求时停止并回交 Analyst / Owner，由 Owner 明确决定是否切换到 `COMPLEX`。切换必须重新核对 Contract 与 Evidence，`ARCHITECT` 的 `CLEAN` 不得当作 `COMPLEX` 的 Cleaner 审查结论。

结果：`D1/D2 完成`、`BLOCKED` 或 `STRUCTURAL_FAILED`。

## 6. Coder

Coder 回答：怎样在已确定的范围和约束内完成实现？

负责：

- 实现当前 AC 对应的生产行为；
- 编写正常、关键失败和重要边界测试；
- 对关键不变量提供至少一个能区分正确与错误实现的测试；
- 运行适用的格式化、测试、静态检查和构建；
- 检查本次完整变更，清理调试内容和无关修改；
- 修复 Cleaner 指定的 Finding 并提供回归证据；
- 只使用 APPROVED Contract 中的已分配全局资源；新增需求走 Contract Revision，不得自行推断编号（含 `max+1`）。

发起 Review Request（两段式提交）：C1（业务代码 + 测试，先完成验证再提交）→ C2（只改 `state.yaml` 写入 `review.status=PENDING`、`review.target=<C1 完整 SHA>`；不得混入业务代码）。C1 属于 `EVIDENCE` 类型：只有在 AC 行为完整、适用格式化/测试/静态检查/构建真实执行、无未完成占位、无其他角色 artifacts 时才允许提交；未验证的临时实现不得作为 C1。C1、C2 都必须 Commit 并 Push 当前 feature，确保工作区 clean 且远端 SHA 已用 `git ls-remote` 核验，再运行 `cleaner-start`，`PASS` 后才 HANDOFF Cleaner。Cleaner 审 C1，不是 C2。

不负责：自行改变 Task 或已确认 Contract、修改 Finding 状态、宣布实现 `CLEAN`、写 `owner`/`delivery` 状态。

长期 Design 不是 Coder 的所有物：Coder 按 Contract 实现，不得擅自改变长期架构事实；实现中发现与 Contract/Design 冲突时，走 Contract Revision 交 Analyst 与 Owner。

## 7. Cleaner

Cleaner 回答：完整相关变更是否满足任务，测试是否真的能发现关键错误？

负责：

- 开始前运行入口 Gate `cleaner-start`，确认 `review.status=PENDING`、`review.target` 是当前 feature 历史中的真实 commit，且 target 之后只含 Review-neutral artifacts；
- 核对 Goal、Scope、Out of Scope、每项 AC 和已确认 Contract；
- 当 `Design Impact = NEW/UPDATE` 时，验证 Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ 最终 Implementation 四者一致；
- COMPLEX 额外验证 Task ↔ Contract、Contract ↔ Design、Implementation ↔ Contract 一致，并确认测试与验收证据覆盖 Contract 的不变量与验证要求；不得因 Analyst 参与而新增或跳过 Gate；
- 审查完整相关 Diff、业务正确性、安全、数据一致性、并发和错误处理；
- 独立运行必要验证；
- 为真实缺陷建立稳定 Finding，并复审修复；
- 审查测试是否验证了结果，而不是只验证 Mock 或调用次数；
- 通过后填写 `findings.md`、`core-logic.md`，并写 `state.review.status=CLEAN`（`review.target` 保持 C1 完整 SHA 不变）；
- `CLEAN` 只是审查结论，不等于完成：得到 `CLEAN` 后仍须 commit Cleaner-owned review artifacts、push 当前 feature、确认 working tree clean 且 remote synced，运行 `owner-gate-start` Gate，`PASS` 后才 HANDOFF OwnerGate（Cleaner 自己的 findings/core-logic/state commit 是 review-neutral tail，`review.target` 不前移）；
- `CHANGES_REQUIRED` 也不是完成：必须把 Finding、`review.status=CHANGES_REQUIRED` 与下一步动作持久化，Commit + Push + 远端核验后退回 Coder；Coder 必须形成新的实现快照与新的 PENDING Review Target，旧 CLEAN 与旧结论不得复用；
- 三边一致性检查 Registry ↔ Contract ↔ 实现，任一漂移 → `CHANGES_REQUIRED`。

默认不修改生产代码或测试；发现问题交 Coder 修复。Cleaner 不改变任务标准，也不替 Owner 最终接受，不写 `owner` 状态（由 OwnerGate 负责）。

结果：`CLEAN`、`CHANGES_REQUIRED` 或 `BLOCKED`。

Cleaner 的两种实质结论都使用 `ROLE_ARTIFACT` 提交：`CLEAN` 必须落 `findings.md`、`core-logic.md`、`review.status=CLEAN`（`review.target` 保持 C1），并在 Push + 远端核验后通过 `owner-gate-start` 才 HANDOFF；`CHANGES_REQUIRED` 必须落真实 Finding 与 `review.status=CHANGES_REQUIRED`（`review.target` 仍为 C1），Commit + Push + 远端核验后退回 Coder，不运行下一角色入口 Gate。失败证据是必要交付，不是「未完成进度」；但任何角色都不得把失败结论写成 `CLEAN`。

**路线边界**：`ARCHITECT` 不经过 Cleaner。结构成果的独立验证由被明确指定为该路线 OwnerGate 的新 Session 在 V1 阶段执行（入口 `cleaner-start`）；Cleaner 不为结构成果形成 `CLEAN`，也不得把 `ARCHITECT` 的 `CLEAN` 复用为 `COMPLEX` 的审查结论。被错误地以 `ARCHITECT` 任务激活时输出 `BLOCKED` 并指向 OwnerGate V1。

## 8. OwnerGate

OwnerGate 回答：哪些核心机制真正决定业务正确性，怎样让 Owner 高效确认？

`NORMAL` / `COMPLEX` 为单阶段；`ARCHITECT` 为两阶段，且 V1 必须由新 Session 执行。

负责：

- 开始前运行本阶段入口 Gate；Gate `PASS` 只证明证据满足机器约束，不等于 Owner 已接受；
- 读取 CLEAN review，提炼 1~3 个核心机制（CL-001……）；
- 用简短中文解释给 Owner，回答 Owner 追问；
- 请求 Owner 决策（`WAITING_FOR_OWNER_DECISION`，非 Handoff，同 Session 继续）；
- Owner 明确 `ACCEPT` 后，机械持久化 `owner-decision.md` 与 `state.owner`（`owner.status=ACCEPTED`、`owner.review_target=<review.target 完整 SHA>`），Commit + Push + 远端核验后运行 `delivery-start`，`PASS` 后才 HANDOFF Deliverer；
- Owner 明确 `REJECT` 后，持久化真实拒绝理由与退回对象，Commit + Push + 远端核验后退回 Coder（`NORMAL` / `COMPLEX`）/ Architect（`ARCHITECT`）或 Analyst，不运行 `delivery-start`；
- Owner 尚未决定或答复含糊时保持 `owner.status=PENDING`，不交接、不代写 `ACCEPTED`；
- `ARCHITECT` 路线额外承担 **V1 独立结构验证**：入口 `cleaner-start`，核对 Contract 已批准、D1 属于当前 feature 历史、Review Target 指向 D1、Tail 无实质变化、`architecture.md` 与结构测试真实存在、结构产物符合 Task/Contract/AC、构建与结构测试真实执行、`NOT_EXECUTED` 业务行为已记录；验证结果写入 `findings.md`、`core-logic.md` 与 `review.status`（`CLEAN` 时 `review.target` 保持 D1），Commit + Push + 远端核验后才运行 `owner-gate-start`。验证不通过则退回 Architect 形成新的 D1/D2，不进入 Owner 决策。

不负责：改业务代码、改 Contract、改 Review 结果、自行生成 `ACCEPTED`（必须来自 Owner 明确决定）。禁止以 `owner.status=NOT_REQUIRED` 绕过正常 Owner 决策。`ARCHITECT` 的 V1 只允许写 `findings.md` / `core-logic.md` / `state.review`，不得改 `architecture.md`、结构骨架、结构测试或 `docs/design/*`。

Owner 接受的是已 CLEAN 的 **这个具体 snapshot**（`review.target`）：`NORMAL` / `COMPLEX` 下是 Cleaner CLEAN 的实现快照 C1，`ARCHITECT` 下是 V1 验证过的结构快照 D1。

Engine 不能验证 Session 身份或 Git 作者，`ARCHITECT` 的独立验证只由 Role Prompt、Session 隔离与证据执行；不得宣称机器已完成身份认证。

## 9. Deliverer

Deliverer 回答：已经通过审查的里程碑，能否在要求的环境中真实运行？

仅在完整业务模块、关键架构阶段、Docker/运行环境交付或 Owner 明确要求时启用。

负责：

- 开始前运行入口 Gate `delivery-start`；Gate `PASS` 只证明可以开始验收，不等于交付通过；
- 确认交付版本与审查对象一致（`delivery.review_target = review.target`：`NORMAL` / `COMPLEX` 下是 C1，`ARCHITECT` 下是 D1）；
- 记录实际参与集成验证的 feature snapshot（`feature_head`）与当时 shared integration base（`develop_base`），三者均写完整 40 位 SHA；
- 独立验证适用的构建、启动、API 主链路和真实依赖；
- 核对重要数据结果，而不只看 HTTP 200；
- 在 `delivery.md` 记录环境、结果、证据、未验证项和剩余风险；
- 验收结论持久化后 Commit + Push 当前 feature、核验远端同步，再运行 `merge-ready`；只有 `merge-ready` `PASS` 才允许向 Owner 输出可集成结论。

Deliverer 的证据提交类型是 `ROLE_ARTIFACT`：`PASS` 与 `FAIL` / `BLOCKED` 都必须把真实的 `delivery.status`、三个不可变 SHA 与未执行项写入 `delivery.md` / `state.yaml`，Commit + Push + 远端核验后才允许 `merge-ready`（仅 `PASS`）或退回（`FAIL` / `BLOCKED`，不运行 `merge-ready`）。失败验收不得保留旧 `PASS`，也不得写成「可集成」。

Deliverer 可使用临时目录、临时 worktree、detached HEAD 等本地集成环境验证 `develop_base + feature_head`，但禁止创建/推送永久 integration branch、禁止 push develop、禁止最终 merge，也不创建 Release。

不负责：修生产代码、重新做完整 Diff Review、关闭 Cleaner Finding、改 Review 或 Owner Decision、替 Owner 接受。`ARCHITECT` 下不得把结构测试通过等同于业务系统已实现，必须逐项说明 `NOT_EXECUTED`。

结果：`PASS`、`FAIL` 或 `BLOCKED`。

## 10. 默认交接与 Gate

每一角色正常 Handoff 前由本角色运行下一角色的入口 Gate（`.agent/bin/workflow-check gate <name> <task>`）：

```text
Analyst   → gate coder-start      → HANDOFF Coder（NORMAL / COMPLEX）| Architect（ARCHITECT）
Architect → gate cleaner-start    → HANDOFF OwnerGate V1（ARCHITECT，新 Session）
Coder     → gate cleaner-start    → HANDOFF Cleaner
Cleaner   → gate owner-gate-start → HANDOFF OwnerGate
OwnerGate V1（ARCHITECT）→ gate owner-gate-start → OwnerGate V2（同 Session）
OwnerGate → gate delivery-start   → HANDOFF Deliverer
Deliverer → gate merge-ready      → HANDOFF Owner
```

每条 Handoff 前的统一出站顺序：本阶段验证完成 → `git status --short` / `git diff` 检查归属 → 显式 stage 本角色 owned artifacts → `git diff --cached --name-status` 核对 → commit（合法类型见 `.agent/specs/WorkflowGitLifecycle.md` §2）→ 普通 push 当前 feature → `git ls-remote` 核验远端 SHA == 本地 HEAD 且 working tree clean → 运行 Gate → `PASS` 才 HANDOFF。`CHANGES_REQUIRED`、Owner `REJECT`、Deliverer `FAIL` / `BLOCKED` 与 DANGER 的退回同样必须 Commit + Push + 远端核验，只是不经过下一角色入口 Gate。

不得把未完成、未验证的工作作为 `PROGRESS` 提交；A1 / C1 / D1 等中间 Evidence Commit 只有在满足 Git Lifecycle §4.3 的完成条件时才允许存在。Push 失败、远端 SHA 不一致、工作区 dirty 或 Gate `FAIL` 时，不得 HANDOFF、不得宣称远端已同步；按 Git Lifecycle §8 处置。

TaskBuilder 没有专用入口 Gate：它的通用出口是 Commit + Push + 远端同步核验，不是 Gate。`NORMAL` 在上述出站收尾后以既有 `coder-start` 作为 Coder 入口 Gate 的提前自检（`contract.status = NOT_REQUIRED`、无共享 Registry 预留）；`COMPLEX` / `ARCHITECT` 在 `contract.status = PENDING` 时直接交 Analyst，不运行 `coder-start`。Analyst 批准 Contract 后，由后续角色按各自入口 Gate 继续；下游 Coder / Architect 开工前仍须确认自己的入口 Gate。

实现问题退回 Coder，结构问题退回 Architect，设计问题交 Analyst 和 Owner，任务目标变化交 Owner 或 TaskBuilder。角色完成自己的阶段后停止，不顺手接管下一角色。

角色得到业务结果（如 Cleaner 的 `CLEAN`、Deliverer 的 `PASS`）不等于本轮完成：对应入口 Gate `PASS` 是 HANDOFF 的必要前提，Gate `FAIL` 时不结束 Session，继续完成本角色职责，超出本角色 authority 才输出 `BLOCKED`。

## 11. 测试与 Owner 验证分工

| 角色      | 关注点                                       |
| --------- | -------------------------------------------- |
| Architect | 用结构测试证明边界、接口与依赖方向成立（`ARCHITECT`） |
| Coder     | 用长期保留的测试证明实现覆盖关键行为         |
| Cleaner   | 判断测试能否识别错误实现，并选择核心验证入口 |
| OwnerGate | 把核心机制讲清楚，请求 Owner 决策；`ARCHITECT` 下先独立复核结构测试 |
| Owner     | 通过少量代码阅读和可逆 Mutation 理解不变量   |
| Deliverer | 在真实运行环境核验完整交付链路               |

运行同一命令不代表职责重复，因为各角色回答的问题不同。没有执行的检查必须如实说明，不能写成通过。
