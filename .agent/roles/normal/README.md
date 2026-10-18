# NORMAL

Discovery → TaskBuilder → Coder → Cleaner → OwnerGate → Merger

| 顺序 | 打开并完整复制 | 交接输入 |
| --- | --- | --- |
| 1 | [公共 Discovery](../DiscoveryAgent.md) | 需求、intake_id；无须预选类型，调查后 Owner 决策 |
| 2 | [公共 TaskBuilder](../TaskBuilderAgent.md) | 已批准类型/范围/基线的 Intake；自动加载本流程初始化规范 |
| 3 | [CoderAgent.md](CoderAgent.md) | Task ID 或 task.md 路径、上一角色正式 Handoff |
| 4 | [CleanerAgent.md](CleanerAgent.md) | Task ID 或 task.md 路径、上一角色正式 Handoff |
| 5 | [OwnerGateAgent.md](OwnerGateAgent.md) | Task ID 或 task.md 路径、上一角色正式 Handoff |
| 6 | [MergerAgent.md](MergerAgent.md) | Task ID 或 task.md 路径、上一角色正式 Handoff |

每次只复制当前角色，按其输入块填写实际标识。Owner 决策在原 Session 回复；下一角色由人类打开新 Session。恢复、适配与交付细节见 [总入口](../../README.md)。
