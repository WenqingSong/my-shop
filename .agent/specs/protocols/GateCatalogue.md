# Gate Catalogue

唯一完整目录，共 20 个既有 Gate；0=PASS，1=FAIL，2=ERROR。Gate 均只读；审批及身份不能从 PASS 自动推断。

| 标识 | 二进制 | 作用对象 | 前置条件 | 适用范围 | 执行角色 | 执行阶段 | 失败下一步 | 强制级别 |
|---|---|---|---|---|---|---|---|---|
| coder-start | workflow-check | Task | 未阻塞、Contract/资源有效、handoffReady | NORMAL/COMPLEX | Coder（TaskBuilder/Analyst 出口预检） | 实现前 | 回 TaskBuilder/Analyst 修正 | REQUIRED |
| cleaner-start | workflow-check | Task | review=PENDING、target 真实、Contract/资源及同步有效 | NORMAL/COMPLEX | Cleaner（Coder 出口预检） | 审查前 | 回 Coder 固定审查快照 | REQUIRED |
| owner-gate-start | workflow-check | Task | Contract 有效、handoffReady、CLEAN 有效 | NORMAL/COMPLEX | OwnerGate（Cleaner 出口预检） | 请求 Owner 前 | 回 Cleaner 修正证据 | REQUIRED |
| delivery-start | workflow-check | Task | CLEAN 有效、Owner ACCEPTED 绑定 target、资源/同步有效、未阻塞 | NORMAL/COMPLEX；兼容历史 | OwnerGate/Merger | Owner 接受后、交付验证前 | 回 OwnerGate 或对应责任角色 | REQUIRED |
| merge-ready | workflow-check | Task | delivery PASS 三 SHA 绑定、CLEAN/Owner/Contract/资源/基线新鲜、同步有效 | NORMAL/COMPLEX；兼容历史 | Merger | 验收完成后集成前 | 回责任角色复验或固定新基线 | REQUIRED |
| handoff-prepared | workflow-check | Task | schema 4、prepared 齐备、证据真实祖先、handoffReady | 仅 schema 4 Task | TaskBuilder/Analyst/Architect/Coder/DocsSpecWriter/Cleaner/OwnerGate | Handoff metadata 提交推送后 | 交出方修复证据后重跑 | SCHEMA4_REQUIRED |
| handoff-entry | workflow-check | Task | schema 4、prepared 齐备、证据真实且为 HEAD 祖先 | 仅 schema 4 Task | TaskBuilder（已有 Task）/Analyst/Architect/Coder/DocsSpecWriter/Cleaner/OwnerGate/Merger | 接手职责前、ACK 前 | 返回交出方，不进入职责 | SCHEMA4_REQUIRED |
| discovery-start | workflow-check | Intake | Intake 记录、父分支/Base 存在且有祖先关系 | 新 Task Intake | Discovery | 调查前 | Discovery 修正基线 | REQUIRED |
| discovery-decision | workflow-check | Intake | 基线有效、Owner decision APPROVED | 新 Task Intake | Discovery | 第二阶段开始前 | 等 Owner 明确决定 | REQUIRED |
| discovery-handoff | workflow-check | Intake | Owner APPROVED、Handoff front-matter 与 Intake 一致 | 新 Task Intake | Discovery/TaskBuilder | Intake 交出/接收 | Discovery 补交接产物 | REQUIRED |
| taskbuilder-init | workflow-check | Task | schema 4、原始Owner批准与Handoff的Intake/id/type/git绑定一致、task.md非空、分支/Worktree存在 | 新 schema 4 Task | TaskBuilder | 初始化完成后 | TaskBuilder 修正初始化 | REQUIRED |
| design-architect-start | workflow-check | Task | DESIGN 类型、未阻塞、handoffReady、design=PENDING 且无既有批准 | DESIGN | Architect | 架构编写前 | 回 TaskBuilder 修正任务 | REQUIRED |
| design-architecture-review | workflow-check | Task | design APPROVED/version/target、架构证据有效 | DESIGN | Architect | Checkpoint 1 持久化后 | Architect 补证据或重请 Owner | REQUIRED |
| design-docsspecwriter-start | workflow-check | Task | 与 architecture-review 相同的架构批准边界 | DESIGN | DocsSpecWriter | 设计规范编写前 | 回 Architect 修正批准证据 | REQUIRED |
| design-cleaner-start | workflow-check | Task | 架构有效、review PENDING、target 真实、设计与快照一致 | DESIGN | Cleaner（DocsSpecWriter 出口预检） | 独立设计审查前 | 回 DocsSpecWriter 固定设计快照 | REQUIRED |
| design-owner-gate-start | workflow-check | Task | CLEAN 有效、design.review_target=review.target、架构有效 | DESIGN | OwnerGate（Cleaner 出口预检） | 最终 Owner 决定前 | 回 Cleaner 修正审查绑定 | REQUIRED |
| design-final-approval | workflow-check | Task | Owner ACCEPTED 绑定 target、final_version/target 有效、基线有效 | DESIGN | OwnerGate/Merger | 最终批准持久化后 | OwnerGate 补目标绑定或重新请 Owner | REQUIRED |
| assembler-start | workflow-program | Program | 全部子 Task 登记与交付证据、依赖图有效 | COORDINATED | Assembler | 隔离集成前 | Collector 补登记或 Owner 修正规划 | REQUIRED |
| program-cleaner | workflow-program | Program | review CLEAN、C0 最终集成 PASS、受限报告在 C1 首次新增、严格后代与 tail 合法 | COORDINATED | OwnerGate | Cleaner 产物提交后、Owner 接受前 | 回 Cleaner/Assembler 修正证据 | REQUIRED |
| final-delivery | workflow-program | Program | 全部登记、集成 PASS、program-cleaner PASS、Owner ACCEPTED、父分支新鲜 | COORDINATED | Merger | Owner ACCEPT 后、最终交付前 | 回责任角色复验或重定基线 | REQUIRED |

## 调用与边界

Task/Intake：`.agent/bin/workflow-check gate <标识> <task或intake路径>`；Program：`.agent/bin/workflow-program gate <标识> <program-id>`。完整实现仍为机器事实基线，表中前置是操作摘要，不穷举每个 schema 子字段。
OD-2 保留双边界；OD-6 schema 4 Task 强制，Intake/Program 不适用。通用 Gate 未按 task_type 完整隔离；Role 必须先选 Workflow。
OD-4 已在收口修正：任意模块中精确文件名 go.mod/go.sum 的冲突均为 L3，包含 `.agent/engine/go.mod` / `.agent/engine/go.sum`。Merger 停止并交 Owner，不自动处理依赖冲突；相似的嵌套备份文件名不因此升级。
