# 五角色职责边界

## **1. 本文档的作用**

本文档回答：Task Builder、Analyst、Coder、Cleaner、Deliverer 和 Owner 分别负责什么，任务在他们之间如何交接。

项目级工程规则见仓库根目录 `AGENTS.md`；共同流程、任务路径、状态和严重程度见 `docs/agent/AgentCollaborationSpecification.md`；本次具体目标与验收条件见 `.agent/tasks/<task-slug>/task.md`。各角色 Prompt 负责说明本角色如何执行，不得改变本文档的职责边界。

首次创建任务时，Task Builder 接收 Owner 需求并自行确定 `<task-slug>`；此时尚不存在 `task_path`。已有任务的更新及后续 Analyst、Coder、Cleaner、Deliverer 调用，必须明确传入 `.agent/tasks/<task-slug>/task.md`，不得自行猜测“当前任务”属于哪个目录。

## **2. Owner：需求、重要决策与最终接受**

Owner 回答：

> 我要什么？哪些重要方案可以接受？哪些关键逻辑我本人需要掌握？任务是否完成？
> 

Owner 负责：

- 提出需求，决定 Goal、Scope、Out of Scope 和 Acceptance Criteria；
- 确认复杂任务的关键方案及 `contract.md`；
- 在范围或验收标准需要改变时作明确决定，并使任务定义同步更新；
- 理解 Cleaner 标出的核心逻辑，质疑关键设计，并按需要执行少量高价值 Mutation；
- 决定是否接受 P3、是否进入里程碑验收，以及是否最终接受任务；
- 决定 Commit、Merge、Push、Deploy。

Owner 不必逐行替 Cleaner Review 全部代码。Owner 的最终接受不由任何 Agent 的 `CLEAN` 或 `PASS` 自动替代。

## **3. Task Builder：定义任务**

Task Builder 回答：

> 这次到底要交付什么，怎样判断完成？
> 

输入是 Owner 的当前需求、Agent 协同规范，以及定义任务所需的少量项目事实。

输出目录：

```
.agent/tasks/<task-slug>/
├── task.md
├── findings.md
├── core-logic.md
└── delivery.md
```

Task Builder 在 `task.md` 中写明 Goal、Scope、Out of Scope、可验证的 Acceptance Criteria、必要背景、Verification、Complexity、Initial Route 和任务审查基线。`Initial Route` 仅为 `READY_FOR_CODER` 或 `READY_FOR_ANALYST`，不在后续阶段反复更新。

Task Builder 初始化另外三个文件；复杂任务的 `contract.md` 由 Analyst 起草，不由 Task Builder 为了凑齐文件提前创建。

Task Builder 不写生产代码或测试，不 Review，不替 Analyst 完成复杂设计，也不代 Owner 创造关键业务规则。信息不足时，能用最小合理假设继续的，应在任务中显式标记；缺失信息会决定不同业务行为的，应记为 Open Question，不擅自决定。

完成标准：Coder 能据此进入实现，Cleaner 能据此 Review，Owner 能看出本次明确不做什么。

## **4. Analyst：复杂任务的技术分析**

Analyst 回答：

> 系统现在怎样工作？复杂问题的约束是什么？推荐哪个方案，哪些性质必须成立？
> 

只在任务确有重要设计选择、根因不明或明显一致性、并发、权限、协议等风险时启用。Analyst 定向调查真实代码、数据、配置和测试，区分已验证事实、合理推断和未知项。

Analyst 负责：

- 分析根因或设计约束，必要时比较少量真正可行的方案；
- 给出推荐方案、关键不变量、错误及一致性语义和验证要求；
- 起草 `contract.md`，初始状态为 `WAITING_FOR_OWNER_APPROVAL`；
- Owner 明确确认后，准确记录其选择和依据，并将 Contract 标记为 `APPROVED`。

Analyst 的推荐不等于 Owner 的决定。未经 Owner 确认，不得把自己的推荐方案标记为 `APPROVED`。Analyst 不修改生产代码、测试或 `task.md` 的任务目标；若分析表明原 Task 自相矛盾，提交证据与待决事项给 Owner。

## **5. Coder：完整实现与修复**

Coder 回答：

> 怎样在既定 Task 和已确认 Contract 的边界内，把功能完整实现出来？
> 

Coder 是默认的代码生产者，负责当前范围内的 API、Controller、Service、Logic、DAO、Model、Middleware、必要重构和测试；按实际项目结构实现，不机械增加层次。

Coder 负责：

- 实现全部当前 Acceptance Criteria 对应的代码行为；
- 编写正常路径、关键失败路径和边界情况所需的测试；
- 执行适用的格式化、测试、构建及自验；
- 检查任务 Diff、已存在的修改、新增文件和临时内容；
- 在 `finding_fix` 模式下确认根因、修复 Cleaner Finding 并做回归验证。

Coder 不得为了通过测试删除失败用例、放宽关键断言或跳过真实核心行为。Coder 不得自行宣称 `CLEAN`、修改 Finding 定义或状态，也不得修改已确认 Contract。

Coder 的完成状态为 `READY_FOR_CLEANER`、`BLOCKED` 或 `IMPLEMENTATION_FAILED`。输出 `READY_FOR_CLEANER` 后交 Cleaner 独立审查，不自行完成最后质量裁决。

## **6. Cleaner：独立审查与核心逻辑提取**

Cleaner 回答：

> Coder 实际提交的全部相关变更是否正确，测试能否发现关键错误？
> 

Cleaner 不依赖 Coder 的自评，而根据 Task、已确认 Contract、任务基线、完整相关 Diff、真实代码与测试证据审查。

Cleaner 负责：

- 对 Goal、Scope、Out of Scope 和每项 AC 给出可追溯的核查结果；
- 检查业务正确性、错误处理、数据一致性、并发、安全及测试质量；
- 独立运行适用的验证，说明未验证事项；
- 在 `findings.md` 创建有证据、稳定编号且有明确修复边界的 Finding；
- 复审 Coder 修复，关闭或重新打开 Finding；
- 达到 `CLEAN` 后，在 `core-logic.md` 标出最终代码中的关键业务逻辑和可选 Mutation 建议。

Cleaner 默认不修改生产代码或测试。发现缺陷或测试缺口时，通过 Finding 交 Coder 修复，不悄悄代写实现。Cleaner 不修改 Task 的验收条件，不因个人编码偏好制造阻塞 Finding，也不宣布 Owner 已接受任务。

Cleaner 的结果为 `CLEAN`、`CHANGES_REQUIRED` 或 `BLOCKED`。`CLEAN` 是当前审查关口通过，不能替代后续要求的运行验收与 Owner 最终决定。

## **7. Deliverer：里程碑的运行验收**

Deliverer 回答：

> 把已经通过代码审查的里程碑作为交付物，它能否在要求的环境中完整运行？
> 

仅在重要里程碑或 Owner 要求的最终交付时启用。开始前必须确认 Coder 已完成、Cleaner 为 `CLEAN`、Owner 已完成核心逻辑验证，且待交付版本与 Cleaner 审查通过的对象一致；复杂任务还须有 Owner 确认的 `contract.md`。若对象发生实质变化，先交 Cleaner 复核。

Deliverer 按 Task 的实际要求独立检查构建、测试、启动、API 主链路、数据库、Redis、MQ、Docker、数据结果或并发行为。不机械运行每项可能存在的检查，也不把未运行项目写成 `PASS`。

Deliverer 在 `delivery.md` 记录环境、命令、结果、证据、未验证事项和剩余风险。结果为 `PASS`、`FAIL`、`CONDITIONAL_PASS` 或 `BLOCKED`。

Deliverer 不修改生产代码或测试，不关闭 Cleaner Finding，不重新进行完整 Diff Review，不因为发现 Bug 而建立另一套长期 Finding。`PASS` 也不是 Owner 的最终接受。

## **8. 文件写入边界**

**文件或内容默认写入者其他角色的边界**`task.md` 及三个默认文件的初始内容Task BuilderOwner 可亲自修改任务定义，或明确指示 Task Builder 更新；其他 Agent 不自行改目标或 AC`contract.md`Analyst 起草，并据 Owner 的明确决定记录批准Coder、Cleaner、Deliverer 只读，不改写约束生产代码与测试CoderAnalyst、Cleaner、Deliverer 默认不修改`findings.md`CleanerCoder 引用并修复，不关闭或改写原始 Finding`core-logic.md`CleanerOwner 阅读、质疑与验证，不要求 Coder 自行指定审查重点`delivery.md`Deliverer实现失败退回既有 Coder → Cleaner 流程

Owner 对任务目标、关键方案和最终接受拥有决策权。需要变更已确认的 Task 时，由 Owner 或依其明确指示的 Task Builder 更新 `task.md`；Contract 的设计变更交 Analyst 分析，再由 Owner 确认。更新后重新核对受影响的实现与验证，不能让后续 Agent 猜测对话与文件哪一个有效。

## **9. 普通任务的交接**

```
Owner 需求
  ↓
Task Builder：task.md → READY_FOR_CODER
  ↓
Coder：实现与自测 → READY_FOR_CLEANER
  ↓
Cleaner：审查
  ├─ CHANGES_REQUIRED → Coder 修复 → Cleaner 复审
  └─ CLEAN → Owner 检查核心逻辑 → Owner 决定是否接受
```

普通 CRUD、简单 API、明确根因的局部 Bug 修复等，默认采用这一流程。不要为了形式加入 Analyst 或 Deliverer。

## **10. 复杂任务的交接**

```
Owner 需求
  ↓
Task Builder：task.md → READY_FOR_ANALYST
  ↓
Analyst：分析并起草 contract.md
  ↓
Owner：明确选择关键方案
  ↓
Analyst：记录 Owner 决定，contract.md → APPROVED
  ↓
Coder → Cleaner → Owner
```

`APPROVED` 之前 Coder 不开始该复杂任务的生产实现。若 Owner 不接受 Analyst 推荐，由 Analyst 按实际决定修订 Contract；无法兼顾原 Task 的，应先交 Owner 处理任务冲突。

## **11. 里程碑的交接**

```
Coder 完成 → Cleaner CLEAN → Owner 核心逻辑验证
                                  ↓
                       Owner 决定启动 Deliverer
                                  ↓
                         Deliverer 独立验收
                         ├─ PASS / CONDITIONAL_PASS → Owner 最终决定
                         └─ FAIL / BLOCKED → 按原因退回或补足条件
```

完整业务模块、秒杀阶段、Kafka 异步化、Docker 化和最终项目交付适合这一流程。仅增加普通字段的小改动一般不需要 Deliverer。

若某项 AC 必须在真实环境验证，Cleaner 应取得相应证据后才能将其记为 `PASS`。环境不可用时记为 `NOT_VERIFIED`，说明所缺条件，不能标记 `CLEAN`。重要里程碑在 Cleaner `CLEAN` 后，仍由 Deliverer 独立复核实际运行和最终数据。

## **12. 四层测试责任**

**角色测试目的典型行为**Coder证明实现覆盖关键行为编写测试，运行模块及适用的项目检查Cleaner判断测试是否可信，并独立复核实现检查断言、错误路径和 Mock 边界；运行必要测试Owner理解重要业务不变量选择性检查核心逻辑与少量高价值 MutationDeliverer验证完整交付链路在要求的运行环境执行构建、集成、Smoke 和数据检查

运行了同一条测试命令不意味着角色重复：Coder 负责实现反馈，Cleaner 负责独立审查，Deliverer 负责交付证据。没有执行的检查必须如实记录原因和风险。

## **13. Finding 的唯一流向**

```
Cleaner 创建 CLEAN-001（OPEN）
  ↓
Coder 在既定 Scope 内修复，并提供回归证据
  ↓
Cleaner 检查原问题、修复 Diff 与回归测试
  ├─ CLOSED
  └─ REOPENED → Coder 继续修复
```

只有 Cleaner 能改变 Finding 状态。P0/P1/P2 未关闭时不得标记 `CLEAN`；P3 是否修复由 Owner 决定。复审如发现修复引入新缺陷，Cleaner 可以新增 Finding，但不得借复审无限扩张任务范围。

Deliverer 发现运行失败，应在 `delivery.md` 记录行为与证据。实现 Bug 退回 Coder → Cleaner；Contract 或方案问题退回 Analyst → Owner。不创建与 `findings.md` 并行的缺陷台账。

## **14. Contract 的确认与变更**

Analyst 起草 Contract 时给出推荐方案、行为不变量、错误语义、并发与一致性约束和验证方式，状态为 `WAITING_FOR_OWNER_APPROVAL`。Owner 对真正影响行为或成本的选择作明确确认；Analyst 根据该确认记录最终选择与 `APPROVED` 状态。

Coder、Cleaner 和 Deliverer 以已确认 Contract 为实现或验证约束。如果发现 Contract 与 Task 或真实系统无法兼容，停止自行选择并报告证据；设计问题交 Analyst 分析，最终由 Owner 决定。未经 Owner 确认，不得暗中扩大或改写 Contract。

## **15. Core Logic 的产生与使用**

Analyst 可以在 Contract 中提出业务不变量；Coder 实现这些不变量；Cleaner 根据**最终代码**确定实际承载不变量的位置，写入 `core-logic.md`；Owner 据此理解、质疑与验证。

重点包括权限、金额、订单、库存、事务、幂等、并发、缓存一致性、消息可靠性和安全边界。普通 DTO、字段搬运、Getter、简单 CRUD 不必机械列入。

Mutation 建议应说明临时改动破坏了哪条不变量、哪个测试预期失败。通常挑少量高价值建议；Owner 执行后恢复正确代码并核对工作区。Mutation 是可选的理解与测试检验手段，不代替 Cleaner 的完整 Diff Review。

## **16. 谁推动下一步**

Owner 或实际调度 Agent 的人依据当前结果发起下一角色，不另设第六个 Agent。首次创建任务时向 Task Builder 传入 Owner 需求；已有任务的更新及其余角色调用，应给出明确 `task_path`、当前模式及必要的前一阶段结论。

- Task Builder 已给出 `READY_FOR_CODER`：可直接启动 Coder。
- Task Builder 已给出 `READY_FOR_ANALYST`：先启动 Analyst。
- Owner 更新已有任务后：按更新后的 Complexity 和 Task Builder 的交接结论重新路由；原 `Initial Route` 只是创建任务时的记录。
- Contract 待 Owner 确认：暂停依赖该决定的编码；确认并记录后再启动 Coder。
- Cleaner 给出 `CHANGES_REQUIRED`：将稳定 Finding 交 Coder，修复后交 Cleaner。
- Cleaner 给出 `CLEAN`：交 Owner 核心验证；需要里程碑验收时再启动 Deliverer。
- Deliverer 给出 `FAIL`：根据失败性质返回已有流程，不要求 Deliverer 修代码。

常规角色交接不需要 Owner 逐次批准；重大业务规则、架构取舍、验收标准变更和最终接受仍由 Owner 决定。

## **17. 阻塞与停止**

任一 Agent 发现 Task 自相矛盾、Contract 未确认、关键业务规则缺失、变更必须超出 Scope 或现有修改归属不清，且无法在既定边界内安全继续，应输出 `BLOCKED`、具体证据及所需决定。

`BLOCKED` 不是 Finding 的替代品。实现缺陷交 Cleaner 建 Finding；设计或需求冲突交 Analyst / Owner。Agent 不把猜测写成事实，也不通过自行扩大 Scope 消除阻塞。

角色完成本阶段职责后停止：Task Builder 不顺手写代码，Analyst 不继续实现，Coder 不替 Cleaner 宣布质量通过，Cleaner 不替 Owner 接受，Deliverer 不替 Coder 修复。

## **18. 最终原则**

协作目标是明确任务、正确实现、独立质量检查、Owner 掌握核心逻辑，以及在必要时验证真实交付。角色数量和文档数量本身不是质量指标；只有能增加明确可靠性价值的阶段才应执行。