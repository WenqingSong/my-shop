# 完整 Role：打开一个文件，整份复制

新需求不必先选流程。先复制 [公共 Discovery](DiscoveryAgent.md)，提供需求与 Intake ID；它调查、识别已有工作、推荐流程后等待 Owner 确认。批准一个 Task 后复制 [公共 TaskBuilder](TaskBuilderAgent.md)，提供原始批准 Intake；它按获批类型初始化，不自行改类型。

已有任务先从总 README 的恢复入口读取 State/Handoff，找到当前需要的角色，不重新创建任务。下一 Session 仍由人类启动，Prompt 不自动调度。

| Owner 确认的类型 | 公共入口之后，按顺序复制 |
| --- | --- |
| [NORMAL](normal/README.md) | normal/CoderAgent.md → normal/CleanerAgent.md → normal/OwnerGateAgent.md → normal/MergerAgent.md |
| [COMPLEX](complex/README.md) | complex/AnalystAgent.md → complex/CoderAgent.md → complex/CleanerAgent.md → complex/OwnerGateAgent.md → complex/MergerAgent.md |
| [DESIGN](design/README.md) | design/ArchitectAgent.md → design/DocsSpecWriterAgent.md → design/CleanerAgent.md → design/OwnerGateAgent.md → design/MergerAgent.md |
| [Program](program/README.md) | Owner 规划；子 Task 各自走公共入口/对应流程；交付后 program/CollectorAgent.md → AssemblerAgent.md → CleanerAgent.md → OwnerGateAgent.md → MergerAgent.md |

共有 21 份完整 Prompt（2 个公共入口 + 19 份流程执行 Prompt），表达原有 11 个机器 Role。每次只复制当前完整文件，不拼接共享内容，不需要宿主运行生成器。Owner 决策在原 Session 回复，独立 Cleaner 不与实现 Session 合并。

## 旧入口路径兼容映射

下表以本目录为基准，左列是 V4.2.x 的历史文件位置，**不是仍存在的执行入口**。新版本不再分发六份重复权威 Prompt；旧 Task 分支保留原版本，历史 Evidence/SHA/Handoff 不改写。

| 历史相对路径（只作映射） | 当前入口 |
| --- | --- |
| normal/DiscoveryAgent.md | [DiscoveryAgent.md](DiscoveryAgent.md) |
| complex/DiscoveryAgent.md | [DiscoveryAgent.md](DiscoveryAgent.md) |
| design/DiscoveryAgent.md | [DiscoveryAgent.md](DiscoveryAgent.md) |
| normal/TaskBuilderAgent.md | [TaskBuilderAgent.md](TaskBuilderAgent.md) |
| complex/TaskBuilderAgent.md | [TaskBuilderAgent.md](TaskBuilderAgent.md) |
| design/TaskBuilderAgent.md | [TaskBuilderAgent.md](TaskBuilderAgent.md) |

旧 TaskBuilderPrompt 映射公共 TaskBuilder；其他更早的平铺角色按实际 task_type 到相应流程找同名文件。Analyst 只在 complex，Architect/DocsSpecWriter 只在 design，Collector/Assembler 在 program。旧 Deliverer 仅在 [legacy](legacy/DelivererAgent.md)，不用于新任务。

更新只修正当前入口与明确授权的宿主文档链接，不替换历史引用，不给旧任务回填 Intake/批准。项目定制的六份旧 Prompt 先做准确上游三方比较，将共同定制适配到公共完整 Prompt、流程专有规则归入项目 Workflow Spec；冲突交 Owner，不直接删掉定制。详见 [适配与更新](../specs/adoption/WorkflowAdoption.md)。
