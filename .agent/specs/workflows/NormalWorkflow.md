# NORMAL 工作流

规范性文件。Task 由 Discovery → Owner → TaskBuilder → Coder → Cleaner → OwnerGate → Owner ACCEPT → Merger；不自动调度。

| 阶段 | Gate | 失败下一步 |
|---|---|---|
| Intake 调查、决策、交出 | discovery-start / discovery-decision / discovery-handoff | Discovery 补证据，批准由 Owner 决定 |
| 初始化 | taskbuilder-init | TaskBuilder 修正 Intake、分支、Worktree、schema 4 |
| Coder 入口 | coder-start | TaskBuilder 修正，contract 必须 NOT_REQUIRED |
| Cleaner 入口 | cleaner-start | Coder 补齐 review PENDING 与 Evidence |
| OwnerGate 入口 | owner-gate-start | Cleaner 修正审查证据 |
| Owner 接受后 | delivery-start | OwnerGate 补齐明确接受及目标绑定 |
| 交付验证后 | merge-ready | Merger 修正交付证据或回对应角色复验 |

每次 Task 交接另外执行 HandoffProtocol；完整 Gate 定义见 `.agent/specs/protocols/GateCatalogue.md`。
Merger 接手并持久化 ACK 后，运行 `workflow-deliver plan task <task-id> --test "<验收命令>"`，只读确认计划；运行 `workflow-deliver verify task <task-id> --test "<验收命令>"`，在隔离区合并与真实测试，父分支保持不变。成功后工具只写当前 Task 的 delivery.md 与 state.yaml 的 delivery 字段，绑定 review_target、feature_head、develop_base；不生成 CLEAN/接受、不 commit/push。失败不产生新的 PASS。

明确 stage 这两个文件，commit/push 后运行 merge-ready。Owner 对计划对象、实际来源 tip SHA 和父分支明确授权后，运行 `workflow-deliver execute task <task-id> --source-sha <实际来源tip完整SHA> --parent-branch <父分支> --test "<验收命令>"`。execute 重新 fetch，复核 merge-ready 与准确来源，隔离重测后受控更新父分支。授权 SHA 是包含交付 metadata 的当前 tip；原 review.target 和 delivery.feature_head 保持各自含义，不前移为 metadata commit。

verify 需要有效 schema 4 NORMAL/COMPLEX、配置集成分支与 Task 父分支一致、当前工作区为 task_branch、有效 CLEAN 与 Owner 接受、匹配本任务/Workflow 的 Merger Handoff 和 ACK，以及 clean/local==remote。历史 schema 仅只读，不走此写入口。Program 子 Task 可 verify 形成登记所需的独立交付证据，但不允许 execute task 直接更新 Program 父分支，应交 Collector 与 Assembler 整体集成。
