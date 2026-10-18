# Program / COORDINATED

Owner 先确认 Program 规划与子 Task。各子 Task 独立执行自己的 NORMAL/COMPLEX/DESIGN 流程，Merger 只验证，不更新父分支。

Collector → Assembler → Cleaner → OwnerGate → Merger

| 顺序 | 打开并完整复制 | 交接输入 |
| --- | --- | --- |
| 1 | [CollectorAgent.md](CollectorAgent.md) | Program ID、上一角色真实证据/冻结 SHA |
| 2 | [AssemblerAgent.md](AssemblerAgent.md) | Program ID、上一角色真实证据/冻结 SHA |
| 3 | [CleanerAgent.md](CleanerAgent.md) | Program ID、上一角色真实证据/冻结 SHA |
| 4 | [OwnerGateAgent.md](OwnerGateAgent.md) | Program ID、上一角色真实证据/冻结 SHA |
| 5 | [MergerAgent.md](MergerAgent.md) | Program ID、上一角色真实证据/冻结 SHA |

每次只复制当前角色，按其输入块填写实际标识。Owner 决策在原 Session 回复；下一角色由人类打开新 Session。恢复、适配与交付细节见 [总入口](../../README.md)。
