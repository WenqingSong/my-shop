# Workflow Handoff Contract（V4）

## 1. 交接的两个部分

一次交接由两部分组成，缺一不可：

1. **落盘**：更新任务记录（`handoff.md` 等），使下一个 Session 能只靠仓库恢复上下文；
2. **交接块**：在对话中输出 4 个必填字段，用于定位。

只有交接块、没有落盘 = 交接无效。只有落盘、没有交接块 = 对方可能不知道去看，但事实仍然成立。

## 2. 交接块格式

```text
RESULT: <本次产出与终态>
NEXT: <下一个角色 | LEAD | OWNER | NONE>
TASK: .agent/tasks/<task-slug>/
ACTION: <CONTINUE | DECIDE: <最小问题> | DO: <必须先做的动作> | STOP | DONE>
EVIDENCE: <commit SHA / 报告文件路径 / 命令，可选，最多 2 行>
```

字段语义：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `RESULT` | 是 | 做了什么、结论是什么。不得只写「完成」 |
| `NEXT` | 是 | 建议的下一个执行者；`NONE` 表示不需要后续角色 |
| `TASK` | 是 | 任务目录，便于定位 |
| `ACTION` | 是 | 需要 Owner 或 Lead 做什么 |
| `EVIDENCE` | 否 | 可核验证据，最多两行 |

`ACTION` 取值：

- `CONTINUE`：按 `NEXT` 继续，无需额外决策；
- `DECIDE: <问题>`：需要 Owner 决策，问题必须是最小可回答形式；
- `DO: <动作>`：需要 Owner 先完成一个外部动作（提供凭据、确认环境）；
- `STOP`：停止，不再继续（阻塞、被叫停、风险过高）；
- `DONE`：任务已交付，等待 Owner 验收。

## 3. `handoff.md` 条目格式

append-only，每条至少包含：

```markdown
## <ISO 时间> <FROM> → <TO>

- Commit: <完整 SHA，或 NONE>
- Result: <一段话>
- Files: <关键文件列表>
- Next action: <具体动作，可被直接执行>
- Open findings: <F1, F2 或 NONE>
```

规则：

- 不得删除或改写历史条目；更正以新增条目说明；
- `Next action` 必须具体到可执行（读哪个文件、跑哪条命令、改哪个函数）；
- 若本次没有后续角色，`TO` 写 `OWNER` 或 `NONE`。

## 4. 角色 → 记录写入矩阵

| 角色 | 必须写 | 不得写 |
| --- | --- | --- |
| TaskBuilder | `task.md`、`plan.md` 骨架、`decisions.md`、`handoff.md` | 业务代码 |
| Analyst | `handoff.md`、`decisions.md`（建议段） | `task.md` 的验收标准、代码 |
| Architect | `handoff.md`、架构文档、`decisions.md`（取舍记录） | `task.md` 的范围 |
| Coder | `findings.md`（修复证据）、`delivery.md`（测试结果）、`handoff.md` | `findings.md` 的结论 |
| Cleaner | `findings.md`、`handoff.md` | 代码、`plan.md`、`task.md` |
| OwnerGate | `decisions.md`（建议）、`handoff.md` | 代码、事实性结论 |
| Deliverer | `delivery.md`、`handoff.md` | 代码、`task.md` |
| Lead | `plan.md`、`handoff.md`、`decisions.md`、其余记录的汇总 | 伪造他人结论 |

## 5. 失败交接

失败交接必须包含：

1. 失败现象（命令、输出、错误码）；
2. 已尝试的修复与结果（最多两次，见 `WorkflowLoopCircuitBreaker.md`）；
3. 判断的失败类别；
4. 需要谁做什么（Lead 决策 / Owner 授权 / 外部依赖）。

失败交接不允许使用 `ACTION: CONTINUE` 掩盖问题。无法继续时必须 `STOP` 或 `DECIDE`。

## 6. 接收方义务

1. 重新读取全部记录与 Git，不信任交接块中的文字描述；
2. 核对交接块中的 commit SHA 是否存在，`git cat-file -t <sha>` 应为 `commit`；
3. 核对 `plan.md` 的 `Next Action` 是否与自己的角色一致；
4. 发现不一致 → `BLOCKED`，写入 `handoff.md`，不猜测。

## 7. 反模式

- 交接块写「已完成，测试全过」但没有任何命令与输出；
- 把关键信息只写在交接块里，不落盘；
- `Next action: 继续推进` 这类无法执行的描述；
- 用 `ACTION: CONTINUE` 把需要 Owner 授权的事推给下一个 Agent；
- 接收方按聊天记忆继续，而不是按记录继续。
