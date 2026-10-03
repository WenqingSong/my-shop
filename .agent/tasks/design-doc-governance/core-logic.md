# Core Logic（Owner 核心验证）

本任务为 Agent Workflow 治理任务，交付物是公共规范与角色 Prompt（`docs/agent/*` 与根目录 `agents.md`），不含生产代码、测试或运行时不变量，因此不存在可执行 Mutation。Owner 需要亲自确认的核心机制是一条跨角色的治理规则闭环，如下。

## CL-001：Design Impact 三态 → 长期 Design 同步 → 四者一致才 CLEAN

- Owner 需要理解：一条任务是否改变「项目级长期事实」由 `Design Impact`（`NONE`/`UPDATE`/`NEW`）判定；`NEW/UPDATE` 时，长期设计必须先由 Analyst 在 Owner `APPROVED` Contract 后沉淀进 `docs/design/*`，Cleaner 在 `CLEAN` 前必须确认「Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ 实现」四者一致，否则长期设计会继续只散落在历史 `contract.md` 里、成为无人维护的债务。
- 规则落点：
  - 判定与声明：`docs/agent/models/TaskBuilderPrompt.md`「判断 Design Impact」及模板「Design Impact」段
  - 主责与时机：`docs/agent/models/AnalystAgent.md`「长期 Design（`docs/design/*`）」与「Owner 确认」第 5 步
  - 阻塞门槛：`docs/agent/models/CleanerAgent.md`（成功标准 / 开始条件 BLOCKED / 审查结论 CLEAN·CHANGES_REQUIRED·BLOCKED）
  - 职责总览：`docs/agent/Five-AgentResponsibilityBoundary.md`、`docs/agent/AgentCollaborationSpecification.md` 第 3 节
- 基线验证（静态 + 场景，本任务定义的方式，无 `go test`）：
  - 场景 B/C（新增 Product 模块 / 改 IAM 状态机）→ `TaskBuilderPrompt.md` 判定清单应分别输出 `NEW` / `UPDATE` 并在 `task.md` 声明 `Design Artifact`。
  - 场景 A（局部 nil pointer bugfix）→ 输出 `NONE` 且不要求 Design。
  - 场景 D/E（Contract Revision 后 Design 未同步 / Design 缺失）→ `CleanerAgent.md` 应给出 `BLOCKED` 而非 `CLEAN`。
- 可选 Mutation：无（无生产代码/测试可破坏；本任务的「不变量」是规则文本本身，改动即改规范，非可逆局部 Mutation）。
- 预期失败 / 恢复确认：不适用（见上）。

## 遗留 P3

`findings.md` CLEAN-001（P3）：Cleaner 规则未逐字枚举 AC-006 的「关键长期事实仅存在于历史 Contract」反例，四者一致与「Design 缺失→BLOCKED」可间接覆盖多数情况。是否补一句显式表述由 Owner 决定，不影响本次 CLEAN。
