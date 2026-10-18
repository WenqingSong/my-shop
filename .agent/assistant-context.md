# Agent Workflow：辅助 LLM 上下文

这是给 ChatGPT 等对话顾问的单文件背景，不是正式 Role，不赋予写入/批准/交付权限。

产品是复制 `.agent/` 到 Go 项目后，由 CodeBuddy 等 Agent 执行的工程 Workflow。日常打开 `.agent/README.md`，选 normal/complex/design/program，完整复制一个 Role Prompt 到独立 Session。25 份完整 Prompt 表达 11 个正式机器 Role；共享来源由框架维护者确定性生成，宿主无须生成器，可定制 Prompt。

NORMAL：Discovery→TaskBuilder→Coder→Cleaner→OwnerGate→Merger。
COMPLEX 在 TaskBuilder 后增加 Analyst、Owner 批准 Contract；DESIGN 用 Architect（架构批准）→DocsSpecWriter→独立 Cleaner→OwnerGate（最终设计批准）→Merger。Program 由人类 Owner 规划，子 Task 各走独立流程，Collector→Assembler→独立 Cleaner→OwnerGate→Merger 完成整体。

Go Engine 在 `.agent/engine/`，独立 Go 1.23 module（历史名 workflow-v2-engine），提供六 CLI、20 Gate。Task State schema 4 只保存决策事实，历史 schema 2/3 只读兼容；架构返工可由 Owner 授权新 DESIGN 修订 Task 承继旧证据，旧批准不清空。Contract/Architecture/Review/Owner 均绑定具体 Git Evidence。Handoff/ACK 和 Event Log 不自动调度，不代表审批。

NORMAL/COMPLEX 交付使用 plan task→verify task→提交交付metadata→execute task；Owner 接受与授权更新父分支是不同决定。Program 子 Task 只 verify 不直接交付；DESIGN 子 Task使用 verify design。Registry.delivery_sha 是已验证 SHA，集成时还证明实际来源 tip 的 metadata tail 及审批/State不变。Merger 严格精确核验来源与父基线，测试前后漂移拒绝。

Program collect 是 Registry CAS 写入；assemble 复用 Merger 在隔离环境集成所有批次，真实测试后创建新本地 C0 来源分支，不更新父分支。Assembler 推送冻结来源 C0。独立 Cleaner 在证据分支首次新增 cleaner-review.yaml 为 C1，C0..C1 仅报告；后续 metadata 登记最终集成/Review/Owner，不推进来源分支。OwnerGate 在 program-cleaner Gate 通过后请求人类接受 C0；Merger 经 final-delivery 与最终授权交付。Gate/参数不机械认证人类身份，依赖运行环境、独立 Session和 Git 权限。

项目拥有 workflow.yaml、tasks/programs/intake/registry、所有 State/批准/Event/证据、AGENTS.md、upstream.yaml。新框架更新不能覆盖或移动它们。本地新版源码 + Agent 三方比较：旧准确上游 B、当前项目 L、新上游 N；保留本地定制，来源不明/核心语义冲突需要具体人类决定。VERSION不是准确上游Commit。

长期业务 docs/design/ 可由宿主拥有，仍受 Merger L3 保护；框架运行不要求复制源仓库外部 docs/examples/scripts。架构见 `.agent/specs/Architecture.md`，操作以 README、Spec和真实源码为准。辅助顾问可以解释、质疑或建议，不可宣布 Owner APPROVED/CLEAN/测试通过或声称已访问未提供的工作区。单主执行者、自动Session调度、云平台和自动升级系统均不是当前能力。

通用 Agent 指令位于 `.agent/AGENTS.md`，Role 显式读取；宿主根 AGENTS.md 是可选业务规则，存在时尊重并保留，不强制生成。V3 T0/T1/T2 规则见内部适配规范，不能用新 Role 直接续跑旧任务。
