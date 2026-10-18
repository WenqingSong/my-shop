# 公共任务入口与初始化路由

规范性文件。新需求从 `.agent/roles/DiscoveryAgent.md` 开始，用户不需要预选 task_type。它先调查、推荐，再由人类 Owner 确认；`.agent/roles/TaskBuilderAgent.md` 只执行已批准的 Task 初始化。两个入口使用原机器身份 Discovery / TaskBuilder，不增加 Role、Gate、State 类型或自动调度。

## 先识别新需求还是恢复

Discovery 读取项目规则、配置、真实 Git 与相关源码，检查用户指定或相关的已有 Task/Program。相同目标已有活动任务时，先用 `workflow-status inspect <task-id>`、`workflow-status recovery-check <task-id>` 与实际 State/Handoff 定位，输出相应流程的下一 Role 或待 Owner 决定的问题；不因此创建重复 Task。Program 读其 program-state.yaml、registry.yaml、冻结来源与收据，不调用 Task recovery 冒充恢复。多个同名候选时请求确认，不猜对象。

真正的新范围、旧任务不能安全继续或需要独立批准的承继，才进入新调查；旧版本兼容见内部适配规范。调查阶段 Intake 只记录 intake_id/parent_branch/base_commit，无 task_type，不创建 Task State。没有明确 Intake ID 或可靠 Git 基线时请求必要输入。

## 调查后的推荐，不是预授权

| 推荐 | 判断依据 | 批准后初始化规范 | 首个执行角色 |
| --- | --- | --- | --- |
| NORMAL | 目标与验收清楚，无需独立 Contract；局部 API 修复、不改变契约的回归测试等 | [NormalWorkflow](NormalWorkflow.md) | normal/CoderAgent.md |
| COMPLEX | 接口/数据/一致性/并发/失败语义或新增全局分配需要先确认 Contract；不是按文件或行数判断 | [ComplexWorkflow](ComplexWorkflow.md) | complex/AnalystAgent.md |
| DESIGN | 主要交付物是架构与正式设计规范，先架构批准再规范落地，无生产实现要求 | [DesignWorkflow](DesignWorkflow.md) | design/ArchitectAgent.md |
| Program / COORDINATED | 多个可独立验收的子 Task，需要明确依赖和整体集成交付 | [CoordinatedWorkflow](CoordinatedWorkflow.md)，Owner 规划；不走 Task 初始化 | 每个子 Task 各自 Discovery/TaskBuilder；交付后 Collector |

修改简单 API 若同时改变公开错误协议/数据模型或引入新分配，不能只因代码少就 NORMAL。一个复杂但可独立验收的 Task 不必升级 Program。已有 ACTIVE 资源的合法复用不要求 NORMAL 制造 Contract；新分配须回 Owner 并走合法 COMPLEX 承继，见协作规范 §12。设计与实现同时需要时，由 Owner 确认一个 COMPLEX 范围或拆分 DESIGN 与实现 Task，不能让纯 DESIGN 偷做业务实现。

Discovery 给出推荐、依据、风险/依赖、现有对象、真实父分支候选和完整 Base，进入 WAITING_FOR_OWNER_DECISION。Owner 明确批准范围、Task ID、NORMAL/COMPLEX/DESIGN、父基线后，才机械记录原 owner-decision.md 与 taskbuilder-handoff.md，依次使用 discovery-decision/discovery-handoff。未经批准不得 finalize；三种 Task 都由同一公共入口接手。

Program 推荐只提供拆分与规划建议。Owner 决定 Program ID/版本、父基线、子 Task/类型/依赖、整体 AC，按专属协议记录 Program 规划；不生成 task_type=COORDINATED/PROGRAM 的 Task Handoff，不伪造 Program Intake，不把整体批准当子 Task 的 Intake/Contract/Owner 批准。Program 子 Task 仍分别走公共入口并绑定原规划。

## 公共 TaskBuilder

1. 在原受控 Intake 工作区运行 discovery-handoff，回读原 Owner 批准，核对实际 Intake/Task/类型/父基线和批准范围；不能只信 Handoff 的 APPROVED 镜像。
2. 按上表加载已批准类型的 Workflow Spec“公共 TaskBuilder 初始化”。不自行升级/降级类型；冲突报 Owner/Discovery，不篡改批准。Program 整体不适用。
3. 按原 Base 使用 workflow-git 核验基线、受控创建分支/Worktree。按准确来源 SHA 将原 Intake 四文件原字节带入新工作区；不以 Intake tip 替换原 Base，不覆盖同名不同证据。具体步骤见公共 TaskBuilder Prompt。
4. 依模板与专属规范建立 schema 4 Task、初始决策字段和适用的 program 引用。taskbuilder-init 机械检查三类型的 Contract/Design 初值、不继承旧批准/CLEAN/交付/预留、当前 Task Worktree 与 Base 祖先关系；存在初始 Handoff 时必须与类型/首位角色匹配。缺批准、类型/Base 不符或 Git 条件不满足必须拒绝。它只验证，不生成或升级 State。
5. 提交初始化 Evidence，再以 metadata commit 写正式 Task Handoff：NORMAL→Coder、COMPLEX→Analyst、DESIGN→Architect，workflow=已批准 task_type。运行 handoff-prepared；下一 Session 执行 entry、核对机器 Role 并写 ACK。Intake 不是 Task Handoff，不提前 ACK，不自引用不存在的提交。

Engine 不解析 Prompt 目录路径；Role 身份与 Handoff 枚举不变。路径重组不授权任何历史 State、Contract、批准或 Evidence 改写。旧路径映射见 [Role 索引](../../roles/README.md)。

初始化中断时，公共 TaskBuilder 先核验同一批准/ID/Base、已有分支/Worktree与文件归属；保留已有合法步骤，只补尚缺产物。受控工具重复创建被拒绝时不重建或覆盖。只读初始化复核可以重跑；已进入执行的 Task 不清回初值，改用恢复流程。冲突或归属不明则 BLOCKED，详见完整 TaskBuilder Prompt。
