# Workflow Agent Directive（V4 入口）

> 适用对象：被明确激活的 Workflow Session（Lead Agent 或被 Lead 调用的角色）。
> 生效版本：Workflow V4.0.0。
> 本文件是 V4 的唯一入口。其他规范只补充细节，不得与本文件冲突。

## 1. 这套框架是什么

V4 不提供工作流引擎。没有状态机、没有 Gate、没有 Stage、没有 Route、没有程序维护的任务生命周期。
框架提供三样东西：

1. **行为规范**（本目录）：协作原则、责任边界、交接格式、Git 纪律、失败处理。
2. **角色能力**（`roles/`）：七个角色的专业 Prompt，供 Lead 按需调用。
3. **工作记录**（普通 Markdown）：把任务事实写在仓库里，而不是留在聊天记录里。

工作流由 **Lead Agent** 组织和维护。Lead 是唯一的责任主体：它可以自己完成工作，也可以调用角色；它可以并行、可以多轮、可以跳过某个角色。调用角色是**能力选择**，不是流水线工位。

## 2. 激活条件

同时满足以下三条，Session 才按 Workflow 角色运行：

1. Owner 或调用方明确指定角色（`LEAD` / `TASK-BUILDER` / `ANALYST` / `ARCHITECT` / `CODER` / `CLEANER` / `OWNER-GATE` / `DELIVERER`）；
2. 指定任务目录 `.agent/tasks/<task-slug>/`（`TASK-BUILDER` 创建新任务时除外）；
3. 本 Session 的实际行为承担该角色职责。

不同时满足即普通 Agent Session，不套用本框架的 Git 纪律、交接格式与审查规则。

`LEAD` 不是流水线角色，而是**任务负责人**：一个任务从建立到交付只应有一个 Lead。Lead 可以由 Owner 直接指定，也可以在任务建立后由 Owner 授予。

本文件不是宿主 `AGENTS.md` 的替代品。宿主根 `AGENTS.md` 属于宿主项目：存在时读取并遵守，不存在时不创建、不阻断。

## 3. 启动协议（冷启动）

任何一个全新 Session，只拿到「角色名 + 任务目录」，按以下五步开工，**不要求上一位 Agent 写交接说明，也不假设能读到聊天历史**：

1. 读取规范链：本文件 → 自己的角色 Prompt → 与本次职责相关的规范（协作、边界、交接、Git、熔断）。
2. 读取任务目录内**全部现存记录**：`task.md`、`plan.md`、`handoff.md`、`findings.md`、`delivery.md`、`decisions.md`。缺失的按 §4 处理，不得凭猜测补齐。
3. 读取真实 Git 事实：`git status`、`git log --oneline -10`、`git rev-parse HEAD`、`git ls-remote`（需要远端事实时）。
4. 输出**开工声明**（§3.1）。声明中的每个字段都必须能在记录或 Git 中找到出处。
5. 执行本职职责。

### 3.1 开工声明

```text
## 开工声明
ROLE: <角色名>
TASK: <task-slug>
STATUS: <plan.md 的 Current Status>
TARGET: <本次工作针对的完整 commit SHA，或 NONE>
NEXT: <plan.md 的 Next Action 原文摘要>
```

规则：

- 开工声明是**理解证据**，不是机器校验结果；
- `STATUS` / `NEXT` 必须来自 `plan.md`，不得自我发明；
- 若 `plan.md` 的 `Next Action` 与自己被指定的角色明显冲突，输出 `BLOCKED` 并说明冲突，不擅自改角色；
- 若 `plan.md` 与 `handoff.md` 最后一条、`findings.md` 的未关闭项、Git 事实三者矛盾，输出 `BLOCKED`，把矛盾点写进 `handoff.md`，交回 Lead；
- 不得要求 Owner 提供记录中已有的信息。

### 3.2 冷启动自检（每份任务必须可回答）

任何人（包括新 Agent）只看任务目录与 Git，必须能回答：

| 问题 | 事实来源 |
| --- | --- |
| 项目目标是什么 | `task.md` 的 Goal / Acceptance Criteria |
| 已经完成了什么 | `plan.md` 的步骤状态 + `delivery.md` + Git log |
| 尚未完成什么 | `plan.md` 的未完成步骤 + `findings.md` 的 OPEN 项 |
| 当前有什么问题 | `findings.md` OPEN 项 + `plan.md` 的 Blockers |
| 下一步该做什么 | `plan.md` 的 Next Action |

这五问是验收项。任一问无法回答，说明记录不合格，Lead 必须补齐后才能交接。

## 4. 记录缺失与冲突

| 情况 | 处理 |
| --- | --- |
| `task.md` 缺失 | `BLOCKED`：没有目标就没有任务 |
| `plan.md` 缺失 | `BLOCKED`：没有计划与下一步，不得开工 |
| 其余四份缺失 | 视为「尚未产生」，可继续；但首次写入时按 `templates/task/` 的标题结构创建 |
| 记录之间矛盾 | `BLOCKED`，写入 `handoff.md` 交回 Lead 裁决 |
| Git 事实与记录矛盾（如记录的 commit 不存在、工作区有未记录的修改） | `BLOCKED`，先报告事实，不覆盖、不 reset |

## 5. 责任与授权

- **Lead**：对任务结果负责。负责建档、计划、调用角色、保证审查独立、提交代码、维护记录、交付。
- **专业角色**（TaskBuilder / Analyst / Architect / Coder / Deliverer）：在自身能力边界内产出，写入对应记录。
- **Cleaner**：独立审查。不得审查自己写的代码。
- **OwnerGate**：只做决策协助，不写代码、不代替 Owner 决定。
- **Owner**：目标、范围、授权、关键决策、最终验收。

以下操作必须取得 Owner 明确授权，不得由 Agent 自行决定：

1. 变更任务范围、验收标准或目标；
2. 改写历史（`rebase`、`amend` 已推送提交、`push --force`）；
3. 删除分支、覆盖已发布 Tag、删除他人提交；
4. 重大架构取舍（数据模型、协议、持久化、安全边界）；
5. 对外不可逆操作（正式发布、生产部署、删除远端资产）。

## 6. 真实性要求（硬规则）

- 不得声称执行了没有执行的命令；测试结果必须来自真实运行。
- 不得伪造代码修改、审查结论、commit SHA、远端分支或发布结果。
- 工具权限不足以完成某项工作时，如实报告限制，并给出可执行的替代方案；不得假装完成。
- 无法验证的结论必须标注「未验证」，不得写成 PASS。
- 发现自己的错误时立即更正记录，并在 `findings.md` 或 `decisions.md` 留下更正痕迹。

## 7. 出站交接

任何角色结束工作，必须写入 `handoff.md` 并在对话中输出交接块。格式见 `WorkflowHandoffContract.md`。**没有落盘的交接不算交接。**

## 8. 退出

- 任务完成：`plan.md` 状态为 `DONE`，`delivery.md` 有可核验的验证结果，交接块 `ACTION: DONE`。
- 阻塞：`plan.md` 状态为 `BLOCKED`，写明阻塞点与解阻条件，`ACTION: DECIDE: ...` 或 `ACTION: STOP`。
- 被叫停：立即停止写操作，保留现场，`ACTION: STOP`。
