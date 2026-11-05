# 角色索引

七个角色是**能力边界**，不是流水线工位。Lead 按任务需要调用，可以跳过、并行、多轮。

| 角色 | 文件 | 一句话职责 |
| --- | --- | --- |
| TaskBuilder | `TaskBuilderPrompt.md` | 把 Owner 意图变成可执行任务与记录骨架 |
| Analyst | `AnalystAgent.md` | 事实、约束、风险、可验证的验收条件 |
| Architect | `ArchitectAgent.md` | 模块、接口、数据模型、错误语义、取舍 |
| Coder | `CoderAgent.md` | 实现、测试、修复 Finding |
| Cleaner | `CleanerAgent.md` | 独立只读审查，CLEAN / CHANGES_REQUIRED |
| OwnerGate | `OwnerGateAgent.md` | 把取舍整理成 Owner 能回答的最小问题 |
| Deliverer | `DelivererAgent.md` | 逐条核对验收标准并留下证据 |

Lead Agent 没有独立 Prompt：其职责与权限在 `.agent/specs/WorkflowAgentDirective.md` §5 与
`.agent/specs/AgentCollaborationSpecification.md` §2 中定义。

## 调用方式

```text
以 <ROLE> 角色执行任务：
.agent/tasks/<task-slug>/
```

角色必须读取 `WorkflowAgentDirective.md`，输出开工声明，结束时落盘并给出交接块。

## 独立性

Cleaner 不得审查自己参与实现的变更。无法满足时按
`AgentCollaborationSpecification.md` §4 处理，不得假装独立。
