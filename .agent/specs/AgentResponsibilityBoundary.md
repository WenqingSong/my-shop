# Agent 职责边界（Workflow V4）

> **V4 冻结边界**：V4 有 **11 个活动 Agent + 1 个 Human Owner**。11 个 Agent 为：
> Discovery / TaskBuilder / Analyst / Architect / Coder / DocsSpecWriter / Cleaner /
> OwnerGate / Collector / Assembler / Merger。Owner 是人类，不是第 12 个 Agent。
>
> **旧 Deliverer 是 V3 历史角色，不是 V4 活动角色**，仅作 V3 历史任务的兼容读取保留；
> V4 最终交付由 Merger 承担。`delivery-start` / `merge-ready` 机械 Gate 保留用于 V2/V3
> 历史任务的读取兼容，不因此新增第 12 个活动 Agent。
>
> 不存在自动 Agent 调度：Handoff 只表达交接信息，下一角色由 Owner 显式启动。

## 1. 总则

11 个 Agent 是能力分工，不是每个任务都必须走完的组织架构。四种工作流（NORMAL / COMPLEX /
DESIGN / COORDINATED）按任务类型启用不同角色子集，路由见 `.agent/specs/AgentCollaborationSpecification.md`。

```text
Discovery     ：在 Task 分支创建前，用受控 Intake 工作区调查真实仓库与需求，产出 Owner 决策证据
TaskBuilder   ：定义要做什么
Analyst       ：解决复杂任务的关键设计选择
Architect     ：DESIGN 任务的架构方案（DESIGN 专属）
Coder         ：实现并自测
DocsSpecWriter：把已批准架构落成正式工程设计规范（DESIGN 专属）
Cleaner       ：独立审查实现与测试
OwnerGate     ：解释核心机制并请求 Owner 决策
Collector     ：COORDINATED Program 子 Task 交付登记（只登记、不合并）
Assembler     ：COORDINATED Program 阶段性/最终集成
Merger        ：受控、隔离地集成已验收 feature 进目标父分支（最终交付角色）
Owner         ：做决定并最终接受（人类，Decision Authority）
```

Owner 不是第 12 个 Agent，而是整个 Workflow 的 **Decision Authority + Shared Repository Authority**。

共享流程、状态和文件规则见 `.agent/specs/AgentCollaborationSpecification.md`。各角色 Prompt 只说明本角色怎样工作，不重复整套工程制度。

Workflow 状态模型（`state.yaml` schema、Evidence Snapshot / Neutral Tail、Gate、不变量、Global Registry 状态机、Handoff Contract）见 `.agent/specs/Architecture.md`。当前状态唯一机器权威源是 `.agent/tasks/<task-slug>/state.yaml`。

## 2. Owner

Owner 回答：我要什么、哪些重要方案可以接受、哪些核心逻辑必须掌握、是否最终接受？

Owner 负责：

- 决定 Goal、Scope、Out of Scope 和 Acceptance Criteria；
- 决定 Task / Branch Boundary：新任务还是延续、是否开新 feature、branch 名称与基线；
- 确认复杂任务的关键方案（Contract Decision）；
- 确认 DESIGN 任务的架构方案（架构批准）与最终设计；
- 阅读核心逻辑说明，做 Core Logic Decision（ACCEPT / REJECT / 继续询问）；
- 决定 Shared Integration：何时/如何 merge、merge 顺序、squash 方式、并行依赖；
- 决定最终 feature→配置集成分支 integration 的 Commit、Merge、Push 和 Deploy。

Owner 不修改 `contract.md`、`owner-decision.md`、`state.yaml`（由对应角色在明确指令下机械持久化）。Owner 不必逐行代替 Cleaner Review，也不因 Agent 给出 `CLEAN` 或 `PASS` 自动接受任务。

## 3. Discovery

Discovery 回答：真实仓库与需求是什么、影响范围与任务类型是什么、Owner 需要就哪些范围问题做决定？

Discovery 只调查、只记录，不实现代码、不替 Owner 决策、不创建 Task 分支、不启动任何其他 Agent。所有新工作流（NORMAL / COMPLEX / DESIGN / COORDINATED）都从 Discovery 开始。

负责：

- 用受控 Intake 工作区（`.agent/intake/<intake-id>/`）调查真实仓库、父分支候选与当前 Git Base；
- 判断任务类型（NORMAL / COMPLEX / DESIGN，或 Program 下的 COORDINATED）与风险、依赖；
- 产出 `discovery-report.md` 与 `intake.yaml`，进入 `WAITING_FOR_OWNER_DECISION` 后停止；
- Owner 决策后核对范围与执行模式，产出可被 TaskBuilder 机器校验的 `taskbuilder-handoff.md`。

不负责：实现代码、创建 Task 分支、初始化 Task、把 Intake 升级成完整 Task、自动启动下一角色。

## 4. TaskBuilder

TaskBuilder 回答：本次究竟交付什么，怎样判断完成？

负责：

- 把 Owner 需求整理成可执行、可验收的 `task.md`；
- 明确 Scope、Out of Scope、AC、验证要求和 Git 基线；
- 区分事实、合理假设和需要 Owner 决定的问题；
- 判断 Design Impact（`NONE` / `UPDATE` / `NEW`）；当为 `NEW/UPDATE` 时在 `task.md` 声明目标 Design Artifact，并纳入 Scope、Deliverables 与 AC；
- 判断任务直接交 Coder，还是先交 Analyst（Complexity）；
- 声明全局资源需求（类型 + 语义），不写具体域号 / version 号。

不负责：写生产代码、替 Analyst 完成复杂设计、Review 实现，或替 Owner 创造关键业务规则。

TaskBuilder 只按 Owner 已批准的分支名和原 Base，使用受控工具 workflow-git 创建 Task 分支/Worktree；不得删除、重命名或覆盖已有分支，也不得自行选择父分支或基线。

## 5. Analyst

Analyst 回答：复杂问题有哪些约束，可行方案是什么，哪些性质必须保持？

仅在存在真正的设计选择、根因不明、安全、协议、并发或一致性风险时启用。

负责：

- 调查当前代码、配置、数据和测试；
- 比较少量真正可行的方案并给出推荐；
- 明确业务不变量、错误语义和验证方法；
- 起草 `contract.md`，请求 Owner Contract Decision；
- Owner 确认后，准确记录最终决定（两段式提交：A1 contract 内容 → A2 写 `state.contract.status=APPROVED`、`state.contract.target=A1`）；
- 作为长期 Design（`docs/design/*`）内容的主责：`Design Impact = NEW/UPDATE` 时，在 Contract 经 Owner `APPROVED` 后、Coder 实现前新增/更新对应 Design Artifact；
- 读 `.agent/workflow.yaml` 确认已声明的 Shared Resource Kinds，再读对应 `.agent/registry/*` 派生候选资源，在 Contract APPROVED 后形成 Registry-only commit（仅含配置已声明的 Registry 文件）并 push `origin HEAD:refs/heads/<integration_branch>` 落实 `RESERVED`，然后验证 Reservation 真正进入 `origin/<integration_branch>`；不得凭空自选编号，不得自行新增 resource kind 或修改 `.agent/workflow.yaml`。

不负责：修改生产代码、测试或任务目标；不能批准自己的推荐方案；除 Registry-only 例外（仅 `.agent/workflow.yaml` 已声明的 Registry 文件）外，不修改 共享集成分支 上的其他任何内容。

Analyst 是 Reservation 的语义负责人，并持有唯一的 Registry-only 集成分支 mutation authority（Registry-only Develop Authority）；Owner 仍独占 branch 生命周期与最终 feature→配置集成分支 integration。

## 6. Architect

Architect 回答：DESIGN 任务的技术架构方案是什么，接口与数据契约、业务不变量和失败边界是什么？

Architect 只服务 `task_type = DESIGN` 的任务；NORMAL / COMPLEX 任务仍走 Analyst → Coder 路径，不由 Architect 参与。

负责：

- 为已由 Owner 决策、由 TaskBuilder 定稿的任务形成技术架构方案（`architecture.md`）；
- 识别接口与数据契约、定义业务不变量、边界与失败场景；
- 请求 Owner 架构评审（Checkpoint 1），Owner `ACCEPT` 后机械持久化 `design.status=APPROVED`、`approved_version`、`approved_target`（两段式提交）。

不负责：批准自己的架构方案（File Writer ≠ Decision Authority）、实现业务代码、写 `docs/design/*`、修改 `contract.md` / Registry / `owner-decision.md`。

## 7. Coder

Coder 回答：怎样在已确定的范围和约束内完成实现？

负责：

- 实现当前 AC 对应的生产行为；
- 编写正常、关键失败和重要边界测试；
- 对关键不变量提供至少一个能区分正确与错误实现的测试；
- 运行适用的格式化、测试、静态检查和构建；
- 检查本次完整变更，清理调试内容和无关修改；
- 修复 Cleaner 指定的 Finding 并提供回归证据；
- NORMAL（`contract.status=NOT_REQUIRED`）：只在 Owner 已授权的任务边界内复用项目已有合法分配、允许当前用途的 ACTIVE 资源；读取配置声明的 Registry 与宿主政策，核对值、状态与使用范围，不要求虚构 APPROVED Contract，不占用其他 Task 的 RESERVED 值，也不把既有资源冒记为本 Task 的新预留。
- NORMAL 需要新增全局资源分配、独立 Contract 或超出任务边界时，停止相关新增使用，交 Owner/TaskBuilder 决定范围；按协作规范 §12.5 经 Owner 批准建立新的 COMPLEX 承继 Task，再由 Analyst 和 Owner 完成 Contract/资源流程，不原地改类型、不自行分配或修改 Registry。
- COMPLEX：严格按 Owner 批准的 APPROVED Contract 使用合法分配的资源，核对 Registry 与实现；新增需求走 Contract Revision 和 Owner 重新批准，再由 Analyst 按既有 Registry-only 权限落实分配。Coder 不得自行推断编号（含 `max+1`）、分配资源或修改 Registry。

发起 Review Request（两段式提交）：C1（业务代码 + 测试）→ C2（只改 `state.yaml` 写入 `review.status=PENDING`、`review.target=C1`）。Cleaner 审 C1，不是 C2。

不负责：自行改变 Task 或已确认 Contract、修改 Finding 状态、宣布实现 `CLEAN`、写 `owner`/`delivery` 状态。

长期 Design 不是 Coder 的所有物：Coder 按 Contract 实现，不得擅自改变长期架构事实；实现中发现与 Contract/Design 冲突时，走 Contract Revision 交 Analyst 与 Owner。

## 8. DocsSpecWriter

DocsSpecWriter 回答：已批准的架构如何落成可执行、可验证的正式工程设计与规范文档？

DocsSpecWriter 只服务 `task_type = DESIGN` 的任务，只能基于已批准架构工作。

负责：

- 接收 Owner 已批准的架构，转化为正式工程设计与规范（`design.md`）；
- 补充可执行的接口约束、测试要求、部署及恢复约定；
- 确保文档与批准架构一致，不引入批准架构之外的新功能或新设计决策；
- 完成后两段式提交（D1 `design.md` → Metadata Commit 写 `review.status=PENDING`、`review.target=D1`）。

不负责：批准架构、增加业务功能、改 Contract、替 Owner 做最终验收、改 `architecture.md` / `docs/design/*` / `task.md` Requirement。

## 9. Cleaner

Cleaner 回答：完整相关变更是否满足任务，测试是否真的能发现关键错误？

负责：

- 核对 Goal、Scope、Out of Scope、每项 AC 和已确认 Contract；
- 当 `Design Impact = NEW/UPDATE` 时，验证 Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ 最终 Implementation 四者一致；
- 审查完整相关 Diff、业务正确性、安全、数据一致性、并发和错误处理；
- 独立运行必要验证；
- 为真实缺陷建立稳定 Finding，并复审修复；
- 审查测试是否验证了结果，而不是只验证 Mock 或调用次数；
- 通过后填写 `findings.md`、`core-logic.md`，并写 `state.review.status=CLEAN`（`review.target` 保持 C1 不变）；
- `CLEAN` 只是审查结论，不等于完成：得到 `CLEAN` 后仍须 commit Cleaner-owned review artifacts、push 当前 feature、确认 working tree clean 且 remote synced，运行 `owner-gate-start` Gate，`PASS` 后才 HANDOFF OwnerGate；
- 涉及配置声明的全局资源时，核对权威 Registry、宿主政策、任务授权边界与实现。NORMAL 合法复用既有 ACTIVE 资源只做适用的一致性核验，不因 `contract.status=NOT_REQUIRED` 而要求 Contract 或新预留；新增分配/超出范围交 Owner/TaskBuilder，不能由 Cleaner 授权。COMPLEX 必须做 Registry ↔ 当前 APPROVED Contract ↔ 实现三边检查，不弱化 Contract/Owner 批准；其他适用的已批准 Contract 仍须核对。既定分配或实现漂移 → `CHANGES_REQUIRED`，缺必要批准或分配 → `BLOCKED`。DESIGN 与 Program 仍执行各自专属 Workflow 的架构批准、整体审查及子 Task 资源/审批约束，不套用 NORMAL 的复用规则替代这些要求。

默认不修改生产代码或测试；发现问题交 Coder 修复。Cleaner 不改变任务标准，也不替 Owner 最终接受，不写 `owner` 状态（由 OwnerGate 负责）。

结果：`CLEAN`、`CHANGES_REQUIRED` 或 `BLOCKED`。

## 10. OwnerGate

OwnerGate 回答：哪些核心机制真正决定业务正确性，怎样让 Owner 高效确认？

OwnerGate 不审查代码、不修改实现、不替 Owner 决定，也不代替人类 Owner 接受任务。

负责：

- 读取 CLEAN review，提炼 1~3 个核心机制（CL-001……）；
- 用简短中文解释给 Owner，回答 Owner 追问；
- 请求 Owner 决策（`WAITING_FOR_OWNER_DECISION`，非 Handoff，同 Session 继续）；
- Owner `ACCEPT / REJECT` 后，机械持久化到 `owner-decision.md` 与 `state.owner`（`owner.status` + `owner.review_target = review.target`）。

不负责：改业务代码、改 Contract、改 Review 结果、自行生成 `ACCEPTED`（必须来自 Owner 明确决定）。

Owner 接受的是 Cleaner 已 CLEAN 的 **这个具体 snapshot**（`review.target`）。

## 11. Collector

Collector 回答：COORDINATED Program 的子 Task 交付是否满足登记条件，如何把交付登记到 Program Registry？

Collector 是 **只负责交付登记** 的角色，绝不合并业务代码，绝不执行阶段性或最终集成，绝无 Git Merge 写权限。

负责：

- 校验子 Task 属于当前 Program 的 Owner 原规划、已完成独立审查（`review CLEAN`）、已获 Owner ACCEPT、交付 SHA/分支/Evidence 有效；
- 把交付登记到 Program Registry（`.agent/programs/<program-id>/registry.yaml`，revision 单调递增）；
- 拒绝未批准、已失效、重复或冲突的交付记录。

不负责：替 Owner 接受、验证业务正确性（那是 Cleaner 的职责）、执行集成（那是 Assembler / Merger 的职责）。

## 12. Assembler

Assembler 回答：COORDINATED Program 的已登记子 Task 如何按依赖图在隔离分支上完成阶段性/最终集成？

Assembler 是负责 **阶段性/最终集成** 的角色，绝不更新正式父分支；阶段性集成 PASS 绝不冒充整个 Program 的 PASS。

负责：

- 校验依赖图（无环、依赖指向已规划 Task）并确定集成批次；
- 使用确切已登记 SHA（`registry.deliveries[].delivery_sha`）在隔离分支执行阶段性合并；
- 执行跨模块接口/契约检查与真实构建、测试、运行验证；
- 保存各阶段不可变 Evidence，完成最终整体集成，为 Program Cleaner 提供完整结果。

不负责：更新正式父分支、冒充 Program 整体 PASS、登记交付（那是 Collector 的职责）。Program 最终交付仍由 Merger 执行。

## 13. Merger

Merger 回答：在 Owner 明确授权下，如何把已通过独立审查、Owner 接受与交付验证的 feature 受控、隔离地集成进目标父分支？

Merger 是 **最终交付角色**：不替 Owner 决定是否集成，不替 Cleaner 做二次审查，也不在冲突时自行做业务决策。

负责：

- 运行证据 Gate `merge-ready` 校验候选满足机械条件；
- 在隔离 scratch worktree 内完成真实集成，绝不直接污染父分支工作区；
- 对合并结果运行真实测试，测试失败则父分支保持不变；
- 冲突按 MERGE-L1 / L2 / L3 分级，L2/L3 停止并转 Owner，不自行修复；
- 仅在 Owner 明确授权、父分支未漂移、测试通过时，以 fast-forward-only 更新父分支，禁止 force push。

不负责：替 Owner 决定集成、二次审查、在 L3 语义冲突时做业务决策、自动启动 L3 转出的 COMPLEX Task。不新增 Merger 工作流类型，也不为 Program 新增专门角色。

## 14. Deliverer（V3 历史兼容，非 V4 活动角色）

Deliverer 是 **V3 历史角色**，回答「已经通过审查的里程碑，能否在要求的环境中真实运行」，仅在 V3 历史任务中启用。

- 在 V3 中负责：确认交付版本与 Cleaner 审查对象一致，记录 `feature_head` / `develop_base`，独立验证构建、启动、API 主链路和真实依赖，在 `delivery.md` 记录结果。
- 在 V4 中：最终交付由 **Merger** 承担；Deliverer 不再作为活动角色参与 V4 工作流。`delivery-start` / `merge-ready` 机械 Gate 保留用于 V2/V3 历史任务的读取兼容。

本文件不把 Deliverer 列为第 12 个 V4 活动 Agent，也不把「V3 原本就有 Merger」之类说法写入历史。

## 15. 默认交接与 Gate

四种工作流的固定顺序（角色级，不引入自动调度）：

```text
NORMAL：
Discovery → Owner → TaskBuilder → Coder → Cleaner → OwnerGate → Owner ACCEPT → Merger

COMPLEX：
Discovery → Owner → TaskBuilder → Analyst → Owner Contract Approval → Coder → Cleaner
→ OwnerGate → Owner ACCEPT → Merger

DESIGN：
Discovery → Owner → TaskBuilder → Architect → Owner 架构批准 → DocsSpecWriter → Cleaner
→ OwnerGate → Owner 最终批准 → Merger

COORDINATED：
Program Discovery → Owner 规划 → TaskBuilder 初始化 → 子 Task 独立实施、验收 → Collector
→ Assembler → Program Cleaner → OwnerGate → Owner 最终批准 → Merger
```

V2/V3 历史任务的默认交接 Gate（保留用于 Legacy 读取兼容）：

```text
Analyst   → gate coder-start      → HANDOFF Coder
Coder     → gate cleaner-start    → HANDOFF Cleaner
Cleaner   → gate owner-gate-start → HANDOFF OwnerGate
OwnerGate → gate delivery-start   → HANDOFF Deliverer（V3 历史角色）
Deliverer → gate merge-ready      → HANDOFF Owner（V3 历史）
```

V4 在 V2/V3 五个 Gate 之外新增：`discovery-start` / `discovery-decision` / `discovery-handoff`、
`taskbuilder-init`、`handoff-prepared` / `handoff-entry`、DESIGN 六 Gate（`design-architect-start` /
`design-architecture-review` / `design-docsspecwriter-start` / `design-cleaner-start` /
`design-owner-gate-start` / `design-final-approval`）与 Program Gate（`assembler-start` / `program-cleaner` / `final-delivery`）。

> 最终交付 Gate 接线（已核验）：Program 最终交付由 `workflow-deliver plan/execute program`
> 消费 `final-delivery`（内部再消费独立的 `program-cleaner`），DESIGN 最终交付由
> `workflow-deliver plan/execute design` 消费 `design-final-approval`；均在显式 Owner 授权后
> 受控调用 Merger，不因 Gate PASS 自动推送。`delivery-start` / `merge-ready` 保留用于
> V2/V3 历史任务的读取兼容。

实现问题退回 Coder，设计问题交 Analyst 和 Owner，任务目标变化交 Owner 或 TaskBuilder。角色完成自己的阶段后停止，不顺手接管下一角色。

角色得到业务结果（如 Cleaner 的 `CLEAN`）不等于本轮完成：对应入口 Gate `PASS` 是 HANDOFF 的必要前提，Gate `FAIL` 时不结束 Session，继续完成本角色职责，超出本角色 authority 才输出 `BLOCKED`。

## 16. 测试与 Owner 验证分工

| 角色           | 关注点                                       |
| -------------- | -------------------------------------------- |
| Coder          | 用长期保留的测试证明实现覆盖关键行为         |
| Cleaner        | 判断测试能否识别错误实现，并选择核心验证入口 |
| OwnerGate      | 把核心机制讲清楚，请求 Owner 决策            |
| Owner          | 通过少量代码阅读和可逆 Mutation 理解不变量   |
| Merger         | 在隔离环境对合并结果运行真实测试再受控交付   |
| Assembler      | 在隔离分支对阶段性集成运行真实构建/测试      |
| DocsSpecWriter | 把接口约束、测试与恢复约定写成可验证规范     |

运行同一命令不代表职责重复，因为各角色回答的问题不同。没有执行的检查必须如实说明，不能写成通过。
