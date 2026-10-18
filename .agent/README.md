# Agent Workflow：从这里开始

将完整 Role Prompt 复制到独立 Agent Session（例如 CodeBuddy），提供实际任务输入，即可按流程工作。Go Engine 负责机械校验和受控交付；人类 Owner 保留所有批准和授权。版本见 [VERSION](VERSION)。

## 第一次放入 Go 项目

只需要新版 `.agent/`，放在业务仓库根目录。项目已有 `.agent/` 时走下一节更新，不能直接覆盖。

在项目根目录打开 CodeBuddy，复制：

```text
请完成这个项目的 Agent Workflow 首次适配。先读 .agent/specs/adoption/WorkflowAdoption.md 的“首次适配”，按其中流程调查并执行。
新版框架目录：当前项目的 `.agent/`（若尚未复制，使用我提供的本地源码目录）。
显式读取 .agent/AGENTS.md；调查真实 Go module、Git/远端分支、已有修改、构建测试及共享资源约定。宿主根 AGENTS.md 可选：存在时读取并保留业务规则，不存在时不创建、不阻断；不要求另外复制根模板。
父分支、真实验收命令或资源权威无法确定时，只询问需要人类决定的内容，不猜测。
在独立适配分支形成可审阅提交，不直接合并共享父分支。构建六个 CLI，验证配置和引擎，记录准确上游来源；保留所有原有项目文件，不创建虚假 Task/批准来证明接入。
最后告诉我能否从本 README 的公共 Discovery 开始新需求，以及仍缺什么。
```

适配在独立分支完成，框架与项目配置需进入 Git，并通过项目已有审批成为后续 Task 的可用基线；不能从尚未包含 `.agent/` 的旧 Commit 创建新 Task。

适配会创建项目拥有的 `workflow.yaml`、必要 Registry、来源记录；框架指令由 `.agent/AGENTS.md` 提供，根 AGENTS.md 不强制生成；模板都在本目录中。只有必要事实明确、引擎检查和构建通过，才算接入准备完成。真实首个 Task 仍需 Discovery 与 Owner 批准。

## 更新已有项目的 Workflow

新版源码下载到项目以外的本地目录。保留业务项目原 `.agent/`，复制：

```text
请安全更新本项目的 Agent Workflow。
新版上游源码/ .agent/ 本地路径：<填写实际路径>。
先显式读新版 .agent/AGENTS.md，再读新版 .agent/specs/adoption/WorkflowAdoption.md 的“已有项目更新”和“V3 → V4.2 迁移”。
读取当前 .agent/upstream.yaml；比较旧版准确上游、项目当前副本、新版上游。保留本地 Role/Spec 定制及已有修改，保护 workflow.yaml、tasks、programs、intake、registry、Event Log 和全部历史 Evidence，不整目录覆盖，不改写旧批准。
宿主根 AGENTS.md 存在时读取，缺失时不创建；原字节和业务规则默认保留。发现旧 Workflow 路径或冲突时列出具体融合差异，由 Owner 授权后才最小修正，不直接覆盖。同步更新内部 AGENTS、Role 和 Spec。
在独立分支/worktree实施；旧上游未知或核心 Gate/审批/路径语义冲突无法安全判断时报告具体差异，等待决定。
验证引擎、内部 AGENTS/Role/Spec 引用和受保护数据（含未授权修改的根 AGENTS.md）未变，提供逐文件结果与仅撤回本次框架修改的方法；成功后再更新来源记录。不自动合并或发布。
```

详细保护范围、三方比较与失败撤回见 [适配与更新](specs/adoption/WorkflowAdoption.md)。更新框架不自动迁移旧 State，不重新解释运行中 Task/Program 的历史证据。

## 开始新需求：先调查，再决定流程

你只需说明想完成什么，不必先选择 NORMAL / COMPLEX / DESIGN / Program。打开 [公共 Discovery](roles/DiscoveryAgent.md)，完整复制到独立 Session，再提供：

```text
mode: discover
owner_request: <我想完成的需求或问题>
intake_id: <本次调查的实际标识>
# 如要继续已有工作，提供已知 Task/Program ID 或路径，先核对恢复入口。
```

Discovery 调查真实代码、规则、Git 与已有任务后推荐流程并解释依据，等待你确认。它发现同一活动任务时优先给恢复入口，不重复创建。新 Task 由你确认范围、ID、NORMAL/COMPLEX/DESIGN 和真实父分支/Base，原 Session 才 finalize；推荐不是批准。

Task 获批后，复制 [公共 TaskBuilder](roles/TaskBuilderAgent.md)，提供 `mode: create`、已批准 Intake 路径/ID及 Owner 已确认的任务信息。它回读原始批准，按已批准 task_type 自动加载专属初始化规范、建立 Worktree/State，通过 taskbuilder-init，再交下面的首个执行角色。你不需要选择另一份 TaskBuilder。

若推荐 Program，先由你按下文规划整体与子 Task；不创建 COORDINATED Task State，不把整体规划当子 Task 的批准。子 Task 各自经过相同的公共入口。判断与初始化细节见 [公共入口规范](specs/workflows/EntryWorkflow.md)。

| 调查后的类型 | 适用情况 | 公共 TaskBuilder 之后的角色顺序 | 人类检查点 |
| --- | --- | --- | --- |
| [NORMAL](roles/normal/README.md) | 范围明确、无需独立 Contract | Coder → Cleaner → OwnerGate → Merger | 调查范围/类型批准；接受 CLEAN 快照；具体交付授权 |
| [COMPLEX](roles/complex/README.md) | 接口、数据、不变量、并发或失败语义需先定 Contract | Analyst → Coder → Cleaner → OwnerGate → Merger | 调查批准；Contract/修订批准；接受及交付授权 |
| [DESIGN](roles/design/README.md) | 只产出架构和正式设计规范 | Architect → DocsSpecWriter → Cleaner → OwnerGate → Merger | 调查批准；架构批准；设计最终接受与交付授权 |
| [Program / COORDINATED](roles/program/README.md) | 多个独立 Task，统一登记、集成交付 | Owner 规划 → 子 Task 各走公共入口及自身流程 → Collector → Assembler → Cleaner → OwnerGate → Merger | 原规划/依赖；各 Task 批准；整体 C0 接受；最终授权 |

每次完整复制当前 Role 的一个文件，无需拼接共享内容或运行生成器；Owner 的决定在原 Session 回复，下一角色仍由人类启动。

### NORMAL

1. 使用上述 [公共 Discovery](roles/DiscoveryAgent.md)，在调查后确认 NORMAL、范围、Task ID 与真实父基线；原 Session finalize 并通过 Discovery Gate。
2. 复制 [公共 TaskBuilder](roles/TaskBuilderAgent.md)，提供批准 Intake 路径。它受控创建 Task/Worktree、绑定基线，写 NORMAL State 和下一步 Handoff。
3. 复制 [Coder](roles/normal/CoderAgent.md)，给 `mode: implementation`、`task_path: .agent/tasks/<id>/task.md`。接手 ACK 与 coder-start PASS 后才实现，提交真实实现快照并发起审查。
4. 复制 [Cleaner](roles/normal/CleanerAgent.md) 到独立 Session，给 Task 路径和审查目标；它验证 Handoff/cleaner-start、独立审查 AC 和测试。Finding 按下节返回，不由 Coder 自称 CLEAN。
5. 复制 [OwnerGate](roles/normal/OwnerGateAgent.md)，理解关键机制并明确接受具体 review.target。此时只完成接受；最终父分支更新还需授权。
6. 复制 [Merger](roles/normal/MergerAgent.md)，提供 Task 与真实验收命令，按下文 plan → verify → execute 交付。

每次切换交出 Task 路径、流程、分支/Worktree、完整 SHA、State/Handoff/验证和未决问题；接手 Agent 自行读取并记录 ACK。人类只需复制当前 Role，不必手工抄写 State。

### COMPLEX

调查后批准 COMPLEX，使用同一 [公共 Discovery](roles/DiscoveryAgent.md) 和 [公共 TaskBuilder](roles/TaskBuilderAgent.md)。然后复制 [Analyst](roles/complex/AnalystAgent.md)，给 Task 路径；它提出 Contract 并在同一 Session 等你批准具体版本，记录 revision/target/history 和资源授权。未批准不得进入 [Coder](roles/complex/CoderAgent.md)。随后 [Cleaner](roles/complex/CleanerAgent.md) 独立对照批准 Contract、接口、数据和不变量审查，最后 [OwnerGate](roles/complex/OwnerGateAgent.md) → [Merger](roles/complex/MergerAgent.md)。需要改变 Contract 时回 Analyst 和 Owner，不由 Coder 偷改。

### DESIGN

公共 Discovery 调查后批准 DESIGN，由公共 TaskBuilder 初始化，再复制 [Architect](roles/design/ArchitectAgent.md)。你批准 architecture.md 对应 A1 后，Architect 记录 approved_target/版本，通过 design-architecture-review 并交 [DocsSpecWriter](roles/design/DocsSpecWriterAgent.md)；后者基于批准架构形成 design.md 的 D1。

独立 [Cleaner](roles/design/CleanerAgent.md) 使用 **design-cleaner-start**，审查设计 AC、架构和 D1；纯设计不要求生产代码。[OwnerGate](roles/design/OwnerGateAgent.md) 使用 **design-owner-gate-start**，你最终 ACCEPT 后记录 final_target=D1、final_version=正整数，再通过 **design-final-approval**；[Merger](roles/design/MergerAgent.md) 使用 `plan/execute design`。不得用普通 Task Gate 替代 DESIGN Gate。

### Program

你先确认 Program ID、真实父分支/base、子 Task 清单/类型/依赖和整体 AC；Agent 按 [COORDINATED 规范](specs/workflows/CoordinatedWorkflow.md) 保存规划，Program 不套 Task Intake/State。每个新子 Task 仍经过公共 Discovery 和 TaskBuilder，绑定 program.id/version 并独立开发、审查、接受。

初始规划可以在项目 Agent 中给出以下指令；尖括号必须填写真实值：

```text
请读取 .agent/specs/workflows/CoordinatedWorkflow.md，机械记录我已确认的 Program 初始规划：
program_id: <ID>；parent_branch: <真实父分支>；base_commit: <完整SHA>。
子Task及类型/独立分支/依赖：<清单>。整体Goal/Scope/AC：<明确内容>。
核验Git和规划完整性，有缺失或冲突先停止询问，不代我决定，不创建新的机器Role，不伪造Review/Owner接受/集成PASS，不覆盖已有Program。
保存规划后告诉我各子 Task 的调查输入；新子 Task 均复制公共 Discovery，不预先选一份流程专属入口；后续按Program流程登记、集成和独立审查。
```

子 Task Merger 只隔离 verify 并提交交付 metadata，**不更新 Program 父分支**。DESIGN 子 Task 使用 `verify design`。然后依次复制 Program 目录的：

- [Collector](roles/program/CollectorAgent.md)：`workflow-program collect <program-id> --task <task-id> --expected-revision <N>`，受控登记，提交推送 Registry。
- [Assembler](roles/program/AssemblerAgent.md)：入口 assembler-start、plan；`workflow-program assemble <program-id> --source-branch <新分支> -- <真实测试命令及参数>`，真实集成全部子 Task，输出 JSON 收据并固定新本地 C0 分支。推送后冻结来源 tip=C0，绝不更新父分支。
- 独立 [Cleaner](roles/program/CleanerAgent.md)：审查真实 C0 和收据，在独立证据分支新增受限报告为 C1，C0..C1 只能新增报告。C1 后 Assembler 保存真实集成 metadata，Cleaner 登记 review 的 C0/C1 关系。C1/后续 metadata 不推进来源分支。
- [OwnerGate](roles/program/OwnerGateAgent.md)：`program-cleaner` 是其取得完整审查证据后的 Gate，通过才请求你接受 C0。
- [Merger](roles/program/MergerAgent.md)：final-delivery、`plan/execute program`，人类最终授权后隔离重测、受控交付 C0。

collect 写 Registry；assemble 创建新的本地来源分支、返回真实收据。gate/plan 仍只读，不存在自动调度/自动批准。重复登记拒绝；不能删除旧 Registry 记录让替换伪装成新登记，遇到已登记子 Task 需要实质修复，先交 Owner 确认新的 Program 规划。

## 继续已有 Task

在项目根运行（也可以把以下指令交给 Agent）：

```bash
.agent/bin/workflow-status inspect <task-id>
.agent/bin/workflow-status recovery-check <task-id>
```

给明确 Task ID 或 `.agent/tasks/<id>/task.md`，让 Agent 读取真实 State/Handoff、Git 与必要事件日志。inspect 是诊断，不等于可继续；recovery-check 不能唯一确定入口、blocked 未解除或 Gate 失败时须查清原因，不能清字段强行继续。next_role=TaskBuilder 时复制公共 TaskBuilder；其他执行角色按 `task_type` 进入对应流程目录。新需求仍从公共 Discovery 开始，不从恢复结果推断新的批准。若正在等待 Contract/Architecture/Owner 决定，回原角色 Session 明确回复，不另起实现角色。Program 恢复读 Program State/Registry、冻结 C0/C1 和最后真实收据，不能运行 Task recovery 冒充 Program 恢复。

## 修复 Cleaner Finding

NORMAL/COMPLEX 复制该流程 Coder，输入 `mode: finding_fix`、task_path、finding_ids；涉及 Contract 的变化回 Analyst + Owner。DESIGN 的已批准架构问题先交 Owner 授权新 DESIGN 修订 Task（见内部 DESIGN 返工规范），不清空旧批准，规范落地问题回 DocsSpecWriter。Program 集成问题回 Assembler，子 Task 问题回责任 Task 并重新审查/登记处理。

先确认旧 review.target/C0 和 Finding 证据，再修复。实质变更形成新目标，由独立 Cleaner 复审；不得复制旧 CLEAN/Owner Acceptance。新的正式 Handoff 仍按协议执行，修复者不关闭自己的 Finding。

## 正式交付

下列命令由 Merger 执行，尖括号替换为实际值；`--test` 是按空白拆分的程序及参数，需要复杂命令时先使用宿主已有脚本，例如 `--test "sh <宿主验证脚本>"`。

```bash
.agent/bin/workflow-deliver plan task <id> --test "<真实验收命令>"
.agent/bin/workflow-deliver verify task <id> --test "<真实验收命令>"
# verify PASS 后只提交并推送 delivery.md/state.delivery，保留被验证 feature_head
.agent/bin/workflow-check gate merge-ready .agent/tasks/<id>
# 人类授权 Task + 实际来源tip + 父分支后：
.agent/bin/workflow-deliver execute task <id> --source-sha <实际来源tip> --parent-branch <父分支> --test "<真实验收命令>"
```

DESIGN 使用 `plan design` / `execute design`，授权 SHA=批准的 D1；Program 使用 `plan program` / `execute program` 并提供 `--source-branch <冻结来源分支>`，授权 SHA=C0。两者都不能把 C1/C2 metadata HEAD 当最终授权对象。

Owner 接受结果与授权更新父分支是不同决定。无明确授权仅准备；来源实质变化要复审，来源/父基线变化导致旧 verify 无效时重新验证。Gate FAIL/ERROR、MERGE-L2/L3、缺少环境、测试失败、审批绑定失效都不能绕过，不强推、不自动发布。

验收脚本不得改变被测快照；测试输出写到工具提供的外部临时 WORKFLOW_TEST_OUTPUT_DIR，不在 scratch 留下未跟踪/忽略文件。HEAD、索引或工作树异常会拒绝交付；前后检查不证明执行过程中从未临时修改。

## 需要时查阅

[内部 Agent 指令](AGENTS.md) · [V3 迁移与安全更新](specs/adoption/WorkflowAdoption.md) · [规范权威链](specs/AgentCollaborationSpecification.md) · [职责边界](specs/AgentResponsibilityBoundary.md) · [Gate 目录](specs/protocols/GateCatalogue.md) · [六 CLI 参数](specs/protocols/CLI.md) · [Handoff](specs/protocols/HandoffProtocol.md) · [架构](specs/Architecture.md) · [辅助 LLM 上下文](assistant-context.md)

业务项目拥有 workflow.yaml、tasks/programs/intake/registry、AGENTS.md 和 upstream.yaml；更新时保护。engine/roles/specs/templates 是框架材料，bin/index 为可重建缓存。主仓库其他 docs/examples/scripts 是框架维护材料，无需复制到宿主。

## Separate: Project Mastery Workflow（项目本地定制，非上游框架）

`.agent/mastery/` 是独立的 Project Mastery Workflow（学习 / 面试掌握），与 Workflow V4 生产开发流程目的不同、资产隔离，不参与 Workflow V4 状态机。入口：`mastery/README.md`。
