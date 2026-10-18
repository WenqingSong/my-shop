# Agent Workflow 内部使用指令

本文件随 .agent 分发。正式 Role 必须显式读取，不能假定工具自动发现。人类日常操作见 [README](README.md)。

先读取 [协作规范](specs/AgentCollaborationSpecification.md) 的权威链、[职责边界](specs/AgentResponsibilityBoundary.md)，新需求先使用公共 Discovery 调查并推荐流程，Owner 确认后公共 TaskBuilder 按批准类型初始化；已有任务先核对恢复入口。角色入口见 [Role 索引](roles/README.md) 中的完整 Prompt 与对应 Workflow Spec。State/Gate/审批语义仅以现有 Spec 和 Engine 为准，本文件不另定义一套协议。

宿主根目录 AGENTS.md 是可选业务规则：存在时读取并尊重；不存在时不创建、不阻断。不把它移入 .agent，不覆盖、删除或自动替换其中业务约定。缺必要业务事实或与已批准任务/框架安全约束冲突时报告 Owner，不静默选边。

框架配置为 .agent/workflow.yaml；Task/Program/Intake/Registry、全部批准与 Event Log 是项目运行数据，不能被框架更新覆盖或搬迁。历史 Evidence 不是当前规则。人类 Owner 才能批准；测试、CLEAN、ACK 不替代接受或交付授权。

首次接入与更新均见 [适配规范](specs/adoption/WorkflowAdoption.md)，包括内部 V3 迁移说明。项目实际 module、父分支、资源、构建测试和服务事实必须调查确认，不预设业务路径或集成分支。

Git 和 Go 1.23+ 是工具要求；Engine 是 .agent/engine 下的独立 module。每个 Worktree 从其自身已提交源码构建六 CLI，不提交二进制，不用宿主 go.work/replace 拼接 Engine。

验收命令不得修改被测 Git 快照。Merger 检查验收前后 HEAD、索引和工作区，拒绝残留的受跟踪、未跟踪或忽略文件；需要输出文件时使用工具提供的外部临时 WORKFLOW_TEST_OUTPUT_DIR，内容会在命令结束后删除。持久证据应由角色在允许的证据工作区记录命令输出。这些前后检查不能证明命令执行过程中从未临时修改后恢复；验收脚本本身仍需审查。

[架构说明](specs/Architecture.md) · [辅助 LLM 上下文](assistant-context.md)
