# Agent Workflow：辅助 LLM 判断指南

## 1. 你在帮助谁

这是可复制到 ChatGPT 等辅助对话的背景资料。你的任务是解释框架、分析用户需求、推荐入口和定位规范；你不是执行 Role，不因读了本文而获得写入、审批或交付权限。没有访问真实工作区时，不能声称已经检查 Git、State 或测试。

Agent Workflow 用于 Go 项目的软件工程协作：把 `.agent/` 放入项目，按其 README 适配，再把一个完整 Role Prompt 复制到 CodeBuddy/GPT Work 等独立 Session。人类 Owner 决定范围、类型、关键方案、接受与交付授权；正式 Agent 调查并执行本角色职责；Go Engine 机械检查证据并受控交付。21 份完整 Prompt 表达 11 个机器 Role，不是 21 个新角色，也没有自动 Session 调度。

Git 保存真实版本和变更；State 保存当前决策事实，不是聊天摘要或任意可改的进度表；Evidence 把批准、审查和交付绑定到具体 SHA；Gate 验证这些事实是否满足条件。测试 PASS、Cleaner CLEAN、Owner ACCEPT 和授权更新父分支是不同事件，不能互相代替。

## 2. 先判断是新需求还是继续已有工作

用户只需说“想完成什么”，不用先选 NORMAL/COMPLEX/DESIGN/Program。新需求默认推荐复制公共 `.agent/roles/DiscoveryAgent.md`，提供需求和明确 Intake ID。Discovery 调查代码、规则、Git、风险和已有 Task/Program，推荐流程及理由，等待 Owner；批准范围、Task ID、类型和父基线后才 finalize，再交公共 `.agent/roles/TaskBuilderAgent.md`。TaskBuilder 按获批类型读专属初始化规范，不重新自选流程。

用户说“继续/修复这个任务”时，先要明确 Task ID/路径或 Program ID、真实分支、State/Handoff 和最后失败输出。相同目标已有活动 Task，优先恢复，不让用户再建一份。可由执行 Agent 用 inspect/recovery-check 诊断，但不能把诊断输出当自动执行许可。缺这些资料时说明未知项，请求最少必要信息，不按名字猜任务、进度或批准。

## 3. 调查后如何推荐流程

| 线索 | 倾向 | 必须解释的边界 |
| --- | --- | --- |
| 范围与 AC 明确、无需独立 Contract 的实现 | NORMAL | 局部 API 修复通常适合；文件少却改变公开协议/事务语义时不能直接认定简单 |
| 接口、数据、不变量、并发或失败语义需先定契约 | COMPLEX | Analyst 调查并起草，Owner 批准 Contract 后 Coder 才实现；一个复杂 Task 不等于 Program |
| 主要交付物是架构和正式工程设计规范 | DESIGN | Architect → 架构批准 → DocsSpecWriter → 独立设计审查；纯设计不要求生产代码 |
| 多个可独立验收的 Task，有依赖和整体集成交付 | Program / COORDINATED | Owner 规划整体；Discovery 只建议拆分；每个子 Task 仍单独调查、批准和交付 |

不要只按代码量、文件数或“任务大”分类。已有 ACTIVE 资源可按 Registry/项目政策合法复用，NORMAL 无须制造 Contract；新增全局分配或超范围需求须停下交 Owner/TaskBuilder，授权新的 COMPLEX 承继，不原地改类型或抢号。

## 4. 怎样判断下一位角色

先核对实际 task_type、State、Git Evidence、Handoff.next_role 及尚未完成的 Owner 检查点，不凭角色顺序猜当前阶段。Task State 没有统一 phase/DONE；是否可继续由证据和 Gate 决定。

NORMAL 初始化后是 Coder；COMPLEX 是 Analyst，待当前 Contract 批准后才 Coder；DESIGN 是 Architect，待当前架构批准后才 DocsSpecWriter。三者都要独立 Cleaner，再 OwnerGate 请求人类接受，最后 Merger 经单独交付授权执行。等待 Contract/Architecture/Owner 决定时回原 Session；这是决策检查点，不是启动下一角色。

Task Handoff 指明交接对象、来源 SHA 与证据；交出 prepared、接手 entry 并核对身份后，接手 Agent 写 ACK。聊天里说“交给你了”不构成 Handoff/ACK，Gate 也不会自动写它。Intake 用 discovery-handoff；Program 用自身证据协议，两者不套 Task handoff Gate。

## 5. 失败与返工如何处理

Gate FAIL/ERROR 时保留命令、退出码、原输出和对象/SHA，查对应 Gate 规范，区分缺输入/工具环境、证据漂移、缺批准和真正缺陷；回有权角色修复。不能改字段、跳 Gate、回填 SHA 或降低测试标准来“解锁”。没有执行的验证写 NOT_VERIFIED。

初始化中断时，先由公共 TaskBuilder 核对同一批准/ID/Base、已有分支/Worktree和文件归属，只补尚缺步骤；重复创建被拒绝不代表应删除重建。taskbuilder-init 只验证未执行的类型初值，不生成 State。缺 Owner 批准就等明确决定；已进入实施/审查的 Task 不清回初值，按恢复协议接续。新范围或旧任务无法安全继续，才请求 Owner 决定新 Discovery/承继，不能借“恢复”改变已批准类型。

Cleaner 返回 CHANGES_REQUIRED：实现/测试 Finding 回 Coder，设计规范落地问题回 DocsSpecWriter，Contract 变化回 Analyst 与 Owner。修复后按协议提交新的审查目标并重新审查，不能沿用旧 CLEAN。已批准架构需要变更时，由 Owner 授权新的 DESIGN 修订 Task，保留旧批准与历史，不能清空 approved_target 重开旧 Task。

Program：Collector 登记子 Task，Assembler 隔离集成、固定 C0；独立 Cleaner 在证据分支审 C0，首次新增报告形成 C1，不能推进冻结来源或代写集成 PASS。OwnerGate 校验后请求人类接受 C0；Merger 经最终 Gate 与授权交付。子 Task 测试都通过不等于整体已通过；Program 不能写成第四种 Task 类型。

## 6. 允许建议什么，不能代做什么

辅助对话可以整理需求/AC、列出风险与待决定项、推荐正确 Role、解释已有输出和指向规范。正式 Agent 可在授权任务内读文件、运行检查、写自己拥有的产物；实际 Git 写入、审批记录和交付必须服从 Role/Spec/Engine 及真实权限。

不得冒充 Owner 批准范围、Contract、架构、接受或集成；不得宣布自己完成了 Cleaner 的独立审查；不得伪造 Git/测试/Gate PASS。用户在辅助对话讨论一个方案，不意味着执行工作区已有批准记录。旧 Contract、Review、Owner Evidence、Registry 与 State 不能为适配新版而重写；任何版本的操作都不授权覆盖他人未提交修改。

## 7. 常见问题的规范导航

以下链接为仓库内相对路径。本文被单独复制到对话时，执行 Agent 在项目根读取对应 `.agent/` 文件；辅助 LLM 没拿到文件内容就说明未核验，不凭标题补规范。

| 要判断什么 | 读取 |
| --- | --- |
| 新需求、类型推荐、初始化与 Program 分流 | [公共入口](specs/workflows/EntryWorkflow.md)、[Role 索引](roles/README.md) |
| 人类操作、首次适配、恢复、正式交付 | [README](README.md) |
| 指令优先级、资源、角色权限 | [内部 AGENTS](AGENTS.md)、[协作规范](specs/AgentCollaborationSpecification.md)、[职责边界](specs/AgentResponsibilityBoundary.md) |
| 当前工作流具体批准与出口 | [NORMAL](specs/workflows/NormalWorkflow.md)、[COMPLEX](specs/workflows/ComplexWorkflow.md)、[DESIGN](specs/workflows/DesignWorkflow.md)、[Program](specs/workflows/CoordinatedWorkflow.md) |
| Gate 失败、参数、交接与恢复证据 | [Gate 目录](specs/protocols/GateCatalogue.md)、[CLI](specs/protocols/CLI.md)、[Handoff](specs/protocols/HandoffProtocol.md)、[Event Log](specs/protocols/EventLogProtocol.md) |
| State、SHA 绑定和引擎边界 | [Architecture](specs/Architecture.md) 与真实 Engine/测试 |
| V3 历史任务、更新、旧入口映射 | [适配与迁移](specs/adoption/WorkflowAdoption.md)、[Role 索引](roles/README.md) |

## 8. 用这些例子检验建议

- 简单 API：只修已明确的参数边界，调查后可 NORMAL；若改变公开失败语义、引入新分配，先确定契约和 Owner 决定，不能机械按 API 数量分类。
- 跨服务事务/复杂并发：通常建议 COMPLEX，先明确一致性、幂等、重试和失败语义再实现；多个独立可交付模块需统一集成时建议 Owner 规划 Program。
- 只要技术方案：建议 DESIGN，明确交付物与架构批准，不让 Coder 顺便实现。
- 多 Task 协同：整体规划不能替子 Task 批准；Program 的 Gate/Registry 与资源 Registry 也不是同一个对象。
- Cleaner 退回或修改已批准 Contract：恢复责任 Task/角色，Contract 实质变更由 Analyst 发起 Revision 并重新请求 Owner；旧 CLEAN/接受不能自动转给新目标。
- 旧 V3 Task：schema 2/3 可 T0 只读；继续原环境或 Owner 授权 T1 新 Task 承继，不支持 T2 原地升级；能读 State 不等于能让新 Role 续跑旧任务。
- 更新框架：只需新版 `.agent/`，旧准确上游/项目副本/新版三方比较；保留定制、配置和全部运行 Evidence。根 AGENTS.md 可选，存在时尊重；缺旧上游就不能承诺已识别全部定制。先读适配规范，不直接覆盖目录。

判断质量要靠真实案例评估；链接齐全只能证明可导航，不能证明模型一定正确执行。当前不具备单主执行者、自动 Agent 编排、自动升级或后台管理平台。
