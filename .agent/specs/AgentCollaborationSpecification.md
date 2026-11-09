# Agent Collaboration Specification（V4）

## 1. 事实来源

| 事实 | 唯一来源 |
| --- | --- |
| 目标、范围、验收标准 | `task.md` |
| 计划、步骤状态、下一步 | `plan.md`（Lead 维护） |
| 交接历史 | `handoff.md`（append-only） |
| 缺陷与未关闭项 | `findings.md` |
| 交付与验证结果 | `delivery.md` |
| 决策与 Owner 授权 | `decisions.md` |
| 代码是否存在、是否被审查 | Git（commit SHA 与 diff） |

聊天记录不是事实来源。上一位 Agent 的口头描述不是事实来源。发生冲突时，以仓库与 Git 为准；记录与 Git 冲突时先停下核实，不得任选其一继续。

## 2. Lead Agent 的地位

1. 一个任务只有一个 Lead，对结果负责。
2. Lead 决定：是否调用角色、调用哪些、串行还是并行、是否需要多轮审查。
3. Lead 维护 `plan.md`，包括步骤状态与 `Next Action`。其他角色不得改写 `plan.md` 的步骤表（可以在 `handoff.md` 提出建议）。
4. Lead 决定提交粒度、分支、推送时机；最终交付由 Lead 向 Owner 报告。
5. Lead 可以自己做分析或实现，但**不能自己充当独立审查者**。

## 3. 角色调用

- 调用方式：新 Session，或 Subagent，或同一 Session 内切换职责。三种都允许。
- 角色输入最小化为：角色名 + 任务目录。可选补充无法持久化的启动前提（如外部凭据位置、时限），最多两行。
- 角色是能力边界，不是流水线顺序。允许：
  - 并行调用多个 Analyst 分析不同侧面；
  - 跳过 Architect（简单任务）；
  - 多轮 Cleaner 审查；
  - 同一角色被多次调用（每次视为新的一次工作）。
- 角色被调用时必须输出开工声明（`WorkflowAgentDirective.md` §3.1）。

## 4. 独立性规则

1. **作者不得自审**：同一份变更的作者不能出具 `CLEAN` 结论。
2. 独立审查者必须满足：没有参与该变更的实现，且只能通过读取代码、Git diff 与运行测试来判断。
3. 若 Lead 无法获得独立审查者（例如平台只允许单 Agent），必须在 `delivery.md` 与 `decisions.md` 中明确标注「审查为作者自审，独立性未满足」，不得声称已通过独立审查。
4. OwnerGate 的决策建议不能替代 Owner 的授权；Cleaner 的 `CLEAN` 不能替代 Owner 的验收。
5. 只读 Subagent 可用于独立审查。使用时必须记录：调用了什么角色、工具权限是什么、审查了哪个 commit。

## 5. 并行协作

并行是允许的，但必须满足：

- 每个并行分支有明确的产出文件与写入边界，不并行修改同一文件；
- 并行结果由 Lead 合并进记录，冲突由 Lead 裁决；
- 并行分析不产生「谁先写完谁算数」的隐式优先级；
- 并行实现若涉及同一模块，改为串行。

## 6. 写入边界

| 记录 / 文件 | 谁可以写 |
| --- | --- |
| `task.md` | TaskBuilder 创建；范围变更只能由 Lead 在 Owner 授权后修改 |
| `plan.md` | 仅 Lead |
| `handoff.md` | 所有角色 append；不得删除他人条目 |
| `findings.md` | Cleaner 新增 Finding；Coder/Lead 更新状态与修复证据；不得删除 Finding |
| `delivery.md` | Deliverer 或 Lead |
| `decisions.md` | Lead 记录；Owner 决策由 Lead 誊录并标注来源 |
| 业务代码 | Coder、Lead（Lead 修复缺陷时） |
| 架构文档 | Architect、Lead |

Cleaner 默认**只读**：不改业务代码、不改 `plan.md`、不改 `task.md`。OwnerGate 同样只读，只写 `decisions.md` 的建议段或 `handoff.md`。

## 7. 完成与交接

1. 角色结束前必须把结果写入对应记录，再在对话中输出交接块。
2. 交接块中的 commit SHA 必须是真实存在的提交。
3. 接收方必须重新读取记录与 Git，不得只依赖交接块文字。
4. 交接块与记录不一致时，以记录为准，并要求发送方更正。

## 8. 反模式

- 把「已实现」写在聊天里，仓库里没有代码；
- 用「测试通过」代替测试命令与输出；
- 审查者顺手改了代码，然后给自己 `CLEAN`；
- Lead 把 `plan.md` 的 `Next Action` 留给下一个 Agent 猜；
- 为了让流程看起来顺畅，跳过未解决的 Finding；
- 引入「状态机 / Gate / Stage」来"保证"流程——V4 明确禁止。

## 9. 与其他规范的关系

- 启动与记录恢复：`WorkflowAgentDirective.md`
- 责任与工具权限：`Five-AgentResponsibilityBoundary.md`
- 交接格式：`WorkflowHandoffContract.md`
- 提交与分支：`WorkflowGitLifecycle.md`
- 失败与升级：`WorkflowLoopCircuitBreaker.md`
- 安装到宿主项目：`WorkflowAdoption.md`
