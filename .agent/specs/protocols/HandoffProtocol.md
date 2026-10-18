# Task Handoff 协议

schema 4 Task 的正式交接强制运行 prepared/entry（SCHEMA4_REQUIRED）。两 Gate 只读，不调度、不写 ACK、不构成审批。schema 2/3 禁止调用。Intake/Program 不适用。

## 顺序

交出方完成本角色职责及业务 Gate，写 state.handoff：prepared=true、scope/scope_id、next_role、workflow、evidence.version>0 与 items（kind/完整 SHA/可选路径）、next_action、source_commit。source_commit 引用已经存在的交付证据 commit，随后独立 metadata commit 保存 Handoff，避免自引用。每次更换来源/接手方时清除旧 ack，避免旧 target 与新 source_commit 冲突。明确 stage 本角色文件后 commit/push，clean 且 local==remote，再运行 `.agent/bin/workflow-check gate handoff-prepared <task>`；PASS 才允许交出。

接手方运行 `.agent/bin/workflow-check gate handoff-entry <task>`，PASS 后还须人工核对 scope/id、next_role、workflow 与本角色/任务一致，再持久化 ack.version、acked_by 与 target=source_commit，并记录 ACK 事件；明确 stage 自己的 ACK metadata、commit/push 并确认 clean/local==remote 后，才执行该角色业务入口 Gate（其中可能检查 handoffReady）。ACK 不替代批准。入口 Gate 不要求 ACK，也不保证 next_role 等于实际运行者；角色身份由运行环境与协作流程约束。

FAIL/ERROR：交出方修复本角色证据，权限外问题报 BLOCKED；接手方不进入业务职责，不自行修改交出方产物以通过 Gate。

## 附录 A：角色交接类别及分支

下表保留设计的八类交出边，但明确 Intake 例外；TaskBuilder 的三个路由不能当作一条相同 Gate。

| 交出方 | 接手方 | 适用 Workflow | 业务边界 | Task Handoff |
|---|---|---|---|---|
| Discovery | TaskBuilder | 所有新 Task | discovery-handoff，Task 尚未创建 | 不适用：Intake |
| TaskBuilder | Coder / Analyst / Architect | NORMAL / COMPLEX / DESIGN | taskbuilder-init 后按 task_type 选择 | 强制 |
| Analyst | Coder | COMPLEX；DESIGN 不适用 | Owner 批准 Contract 后 coder-start | 强制 |
| Architect | DocsSpecWriter | DESIGN；NORMAL/COMPLEX 不适用 | design-architecture-review | 强制 |
| Coder | Cleaner | NORMAL/COMPLEX；DESIGN 不适用 | cleaner-start | 强制 |
| DocsSpecWriter | Cleaner | DESIGN；NORMAL/COMPLEX 不适用 | design-cleaner-start | 强制 |
| Cleaner | OwnerGate | 三种 Task 类型 | 各自 OwnerGate 入口 Gate | 强制 |
| OwnerGate | Merger | 三种 Task 类型 | Owner ACCEPT 后各自出口 Gate | 强制 |

TaskBuilder 收 Intake 用 discovery-handoff；收到已存在 Task 的正式交接时才使用 handoff-entry。Collector/Assembler 的 Program 边见 `.agent/specs/workflows/CoordinatedWorkflow.md`，不调用 Task Gate。修复退回边同样遵守上述 Task 作用域与证据规则。

## 公共入口的路径与机器身份

Discovery/TaskBuilder 的完整 Prompt 统一在 roles 根目录，机器 next_role/acked_by 仍为 Discovery/TaskBuilder 等既有枚举，不使用文件路径或 ENTRY。Intake 的 discovery-start 不要求 task_type；批准后的 Task Handoff.workflow 必须是 NORMAL/COMPLEX/DESIGN。TaskBuilder 首次交出按已批准类型选择 Coder/Analyst/Architect。历史 Handoff 中已存在的 SHA/路径不因 Prompt 迁移而重写，映射见 `.agent/roles/README.md`；Program 保留自己的交接协议。
