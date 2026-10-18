# Agent Workflow 架构

框架完整运行单元是 .agent/；宿主业务 docs/design/ 属于项目自己的架构。本文解释 WHY，执行规范与冲突处理见 [协作规范](AgentCollaborationSpecification.md)，机器行为以引擎与测试为事实基线。

本文保留 V4 工程不变量，包含当前 Program SHA/漂移修复、完整 Role 分流与自包含接入。日常使用见 [总入口](../README.md)。

## 定位与约束

Agent 协作需要跨 Session 可回读的真实证据，不能靠聊天自述证明完成。因此采用 Git commit 作为不可变快照，State 保存无法从 Git 推导的决定，Gate 校验事实是否一致。工具不调度模型、不自动编排、不提供 Owner 身份认证；身份与分支权限由运行环境和仓库保护承担。

当前开发版本见 .agent/VERSION，Task schema=4；历史 schema 2/3 只读兼容，Program 使用独立 schema 1。历史 module 名 workflow-v2-engine 不代表产品版本。无全局 phase、持久 STALE 或 DONE，失效由证据及 tail 机械推导。

## 角色分工

| 角色 | 架构职责 |
|---|---|
| Discovery | 固定 Task 前调查、Intake 与 Owner 范围决定 |
| TaskBuilder | 把明确范围转换为可验证任务并绑定分支/Worktree |
| Analyst | COMPLEX 的任务契约和资源承诺 |
| Architect | DESIGN 架构方案与第一个 Owner 检查点 |
| Coder | 实现和测试 |
| DocsSpecWriter | 已批准架构下的正式工程设计 |
| Cleaner | 独立审查与核心机制验证入口 |
| OwnerGate | 解释并机械记录人类最终决定 |
| Collector | 子任务交付登记，避免将登记混同合并 |
| Assembler | 依赖顺序、隔离集成与跨模块验证 |
| Merger | 接受与授权后受控更新父分支 |

Owner 是人类，Deliverer 为历史兼容资料。四流的执行顺序分别在 [workflows](workflows/) 定义，不复制角色 Prompt。

## Evidence 与 Neutral Tail

Contract.target、review.target、Owner 绑定、delivery 三 SHA 指向真实 commit。批准不是对任意后续版本的无限授权：批准的 Contract 必须与工作树内容一致；CLEAN 后实质变化使旧审查失效；交付验证绑定参与验证的 feature_head 与父分支基线。

Review-neutral 白名单仅限当前 Task 直属的 findings.md、core-logic.md、owner-decision.md、delivery.md、state.yaml、events.md。Delivery-neutral 更窄，仅 state.yaml/delivery.md/events.md。其他 Task 同名文件、嵌套路径、architecture.md/design.md/contract.md、代码、测试、项目设计均属 substantive。采用 default-deny，避免用文件名伪装任意变化。

## 四种工作流的差异

NORMAL 不要求独立 Contract；COMPLEX 要求先由人类批准 Contract。DESIGN 不要求生产业务代码，有架构与最终设计两个检查点，approved_target/review_target/final_target 分别绑定，不能用通用 Gate 代替专属链。

COORDINATED 拆成 Program→独立 Task，不允许嵌套 Program。Program 无机器 Intake 锚点，由 Owner 直接规划；其子 Task 仍各自有 Intake、独立分支和审查。Collector 的 Registry 是交付登记权威，Assembler 使用登记 SHA；子任务全部通过不等于整体通过。

Program 最终集成 C0 先形成，Cleaner 审查后才在后续 C1 首次新增受限审查报告，C0 不得预含报告。C1 只绑定 C0，后续 metadata 才登记 C1，避免自引用和审查时序倒置。

## Gate 与写工具分离

完整 20 个既有 Gate 见 [Gate Catalogue](protocols/GateCatalogue.md)，不在本文重复清单。workflow-check 与 workflow-program gate/plan 只读；workflow-program collect 写受控 Registry，assemble 复用 Merger 验证全部来源并固定新本地 C0 分支，不更新父分支；workflow-git 只负责初始化所需的受控分支/Worktree；workflow-event 追加日志，不代表批准；workflow-status 诊断和派生索引；workflow-deliver 的 plan 只读；verify task 为 NORMAL/COMPLEX 隔离验收并只写 delivery 证据，不 commit/push；execute 需人类明确对象、来源 SHA 与父分支授权。Task 绑定实际来源 tip，DESIGN/Program 保持最终 Evidence 授权语义。

Task schema 4 的版本化 Handoff 保存交接证据，prepared/entry 守交出和接手边界；ACK 由接手方自行写入，不由 Gate 自动生成。Intake 和 Program 不调用 Task handoff Gate。具体协议见 [HandoffProtocol](protocols/HandoffProtocol.md)。

## 路径即兼容协议

Task/Program/Intake/Registry/workflow.yaml 路径参与证据回读或配置解析，不能搬迁。specs 内部可分层，但保持 .agent/specs/ 与 docs/design/ 的 L3 前缀；任务 contract.md 后缀也参与冲突分级。

Merger 在隔离 Worktree 集成，真实测试通过且父分支未漂移、人类授权后才更新父分支，无 force。L2/L3 冲突停止并交 Owner，不自动做业务取舍。
OD-4 收口修正：任意模块中精确文件名 go.mod/go.sum 均属于依赖契约，冲突按 L3 停止；既有前缀规则保留。真实嵌套模块冲突回归验证不会运行后续测试或更新父分支。

## 分发、索引与迁移

框架与项目共用 .agent/ 命名空间，通过 [使用入口中的归属说明](../README.md) 明确归属，不能覆盖项目配置和运行证据。索引只读本地 State/Git，忽略生成物，不成为第二套权威；source_sha 不代表远端新鲜度。
历史 Task 保持原位，只读或用新 Task 承继，不原地改 schema，不补造过去的审批。细节见 [迁移资料](adoption/WorkflowAdoption.md)。

## 核心不变量的延续（INV-1～INV-7）

| 不变量 | V4.1 架构约束 |
|---|---|
| INV-1 Shared Branch Authority | 人类控制共享分支的生命周期和最终集成授权。TaskBuilder 用受控工具创建任务分支；Analyst 的 Registry-only 集成分支提交是限定例外；Merger 仅在人类绑定来源/目标授权后受控交付。不得沿用历史“Agent 绝不建分支”误述当前行为。 |
| INV-2 Implement Authorization | Coder 前 Contract 必须有效；资源值必须由 origin/配置集成分支的 Registry 授权，owner 为本 Task 且状态 RESERVED/ACTIVE。 |
| INV-3 Immutable Review | Cleaner 只对明确 Evidence Commit 给出 CLEAN，不 CLEAN 工作树。 |
| INV-4 Review Freshness | substantive change 使旧 CLEAN 机械失效，不持久化 STALE。 |
| INV-5 Owner Binding | owner.review_target 必须等于当前 CLEAN 的 review.target，接受仅来自明确人类决定。 |
| INV-6 Delivery Binding | delivery PASS 绑定 review.target、准确 feature_head 与父基线，父分支前进使旧验收不能直接用于交付。 |
| INV-7 Snapshot Integrity | Evidence 后只允许阶段定义的严格 neutral tail，不能把任何后续 HEAD 自动当作已审/已接受版本。 |

## State 与 Artifact 可发现性

完整字段以独立版本解析器 state_load.go/state_v4.go 和测试为准；新任务起始结构见 [State 模板](../templates/state.yaml.template)，内含 NORMAL/COMPLEX/DESIGN 差异，绝不能覆盖历史 State。
各角色对 state 子字段的机械写入权限见 [职责边界](AgentResponsibilityBoundary.md)，不以整文件可写推断对所有决策字段有权。

Contract 批准使用 contract.target/revision/history，不使用 DESIGN 的 approved_target 字段。DESIGN 的 approved/review/final 三组 SHA 与版本各自绑定不同检查点。Program 另用 program-state.yaml 和 registry.yaml，不能把 Program schema 1 混为 Task schema 4。

两阶段提交是为避免自引用：先提交交付 Evidence，再以 metadata commit 引用前一个完整 SHA；Handoff 与审查报告同理。metadata 的可修改性并不赋予批准权，也不自动使它 neutral，仍受当前作用域和白名单约束。

Analyst 资源分配只修改机器配置声明的 Registry 文件，单独提交、推送并核对远端新鲜度；不得夹带业务代码、Task 文件或改造 Registry 机制。资源值的业务格式由 Project Policy 决定，Core 只负责 opaque 值、归属与状态授权。


## 完整 Role 与 Program SHA 证明

角色权威规则在框架源码维护区单一维护，确定性生成两个公共入口 Discovery/TaskBuilder，以及 normal/complex/design/program 下 19 份后续 Prompt，共 21 份完整 Prompt；宿主只复制完整文件，可本地定制并按准确上游三方更新。11 个机器身份及 Handoff 枚举不变。

Program Registry.delivery_sha 仍是实际被验证 snapshot；交付 metadata 的远端 tip 另行核验：受限 delivery-neutral tail、State 只允许 Delivery 变化，Contract/Review/Owner/资源/Git 基线不变。集成固定该真实 tip，Merger 在测试前后精确核验来源和父分支；非 MERGE-L1 不能视为集成成功。原始 JSON 集成收据在 C1 后由 Assembler 登记，C0 不自引用自己。

内部 .agent/AGENTS.md 属框架通用导航，显式读取；宿主根 AGENTS.md 可选且项目拥有。新内部路径进入 Merger L3 保护，review/delivery neutral 白名单不扩大。验收前后拒绝被测树持久变化，输出使用工作树外临时目录；不声称可证明执行期间从未修改后恢复。

## 公共任务入口

新需求先经公共 Discovery 调查，流程选择发生在调查之后，由 Owner 确认；公共 TaskBuilder 只按获批 task_type 加载专属初始化规范。Program 仍由 Owner 规划，子 Task 单独获批。入口规则见 [EntryWorkflow](workflows/EntryWorkflow.md)。Engine 依赖机器 Role 与 State/Git Evidence，不读取 Prompt 的物理路径；本次不新增 Gate/Schema，也不改变 Handoff/ACK 权限。旧路径迁移见 [Role 索引](../roles/README.md)，历史批准和路径不改写。
