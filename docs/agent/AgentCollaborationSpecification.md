# 全局 Agent 协同规范

## **1. 目标与适用范围**

本项目使用 Agent 分担实现、测试、审查和交付验证。Owner 决定需求、重要方案和最终是否接受任务，并掌握关键业务逻辑。

本文件规定跨角色的协作规则。仓库根目录的 `AGENTS.md` 规定长期工程基线；`docs/agent/Five-AgentResponsibilityBoundary.md` 规定角色职责；各角色 Prompt 规定本角色的执行方法。当前业务需求以对应任务文件为准。普通任务不为凑齐角色而调用所有 Agent。

## **2. 统一路径与文件职责**

每个独立任务使用：

```
.agent/tasks/<task-slug>/
├── task.md
├── findings.md
├── core-logic.md
├── delivery.md
└── contract.md                 # 仅复杂任务创建
```

**文件内容写入责任**`task.md`Goal、Scope、Out of Scope、Acceptance Criteria、背景、验证要求、初始路由和审查基线Task Builder 初始化；Owner 可亲自修改，或明确指示 Task Builder 更新任务定义`contract.md`经确认的技术约束、接口、数据语义和关键不变量Analyst 起草；Owner 确认；Analyst 根据 Owner 的明确决定记录确认结果`findings.md`Review 结果、稳定编号的 Finding、复审结论Cleaner`core-logic.md`最终实现中值得 Owner 重点理解的逻辑和可选 Mutation 建议Cleaner`delivery.md`里程碑的运行与交付验证Deliverer

`design.md` 可以是项目已有的设计资料，不是每个任务的必建文件。Task Builder 初始化四个默认文件；简单任务不创建空的 `contract.md`。Coder 负责代码和测试，默认不维护上述协同文件。

`task.md` 中的 `Initial Route` 只记录 Task Builder 创建任务时的去向（`READY_FOR_CODER` 或 `READY_FOR_ANALYST`），不作为持续更新的任务状态。后续结果分别由对应角色写在其负责的文件中。Owner 后来改变任务时，以更新后的 Complexity、Task Builder 的本次交接结论和已有审查状态确定下一步；不得沿用过期的 Initial Route 跳过必要的 Analyst 或 Cleaner。

## **3. 指令、证据与冲突**

执行要求的优先顺序：

1. Owner 当前明确指令；
2. 当前 `task.md` 的 Goal、Scope、Out of Scope 和 Acceptance Criteria；
3. Owner 已确认的 `contract.md` 和适用的已确认设计；
4. `AGENTS.md` 的项目工程基线；
5. 本规范、职责边界与角色 Prompt。

真实代码、测试结果和运行日志用于判定**当前事实**，不能自行改变任务目标。Agent 报告和旧文档只作为辅助材料。Owner 当前指令若改变了既有目标或验收标准，先由 Owner 或依其明确指示的 Task Builder 更新 `task.md`；若影响已确认的 Contract，还须取得 Owner 对修订方案的确认。若任务与已确认的 Contract、真实系统约束或安全要求明显冲突，不得暗自选择一边；应给出冲突位置、证据和需要 Owner 决定的事项。不得泄漏凭据、绕过明确的认证授权要求，或吞掉关键错误制造成功。

## **4. 角色与默认路由**

**角色核心问题主要输出**Task Builder本次到底要交付什么？任务定义与协同文件初始化Analyst复杂任务有哪些可行方案和必须保持的不变量？分析结论与待确认的 `contract.md`Coder如何按已确定的任务和约束完成实现？生产代码、必要测试和验证结果Cleaner实际 Diff 是否满足任务、测试是否可信？`findings.md`；通过后填写 `core-logic.md`Deliverer重要里程碑作为交付物能否实际运行？`delivery.md`

普通任务：

```
Owner → Task Builder → Coder → Cleaner
                         ↑          │ CHANGES_REQUIRED
                         └──────────┘
Cleaner CLEAN → Owner 核心逻辑检查 → Owner 决定是否接受
```

复杂任务在 Coder 前增加：

```
Task Builder → Analyst → Owner 确认关键方案 → Coder
```

重要里程碑在 Cleaner CLEAN 且 Owner 完成核心逻辑验证后，由 Owner 决定是否启动 Deliverer；Deliverer 验证的必须是 Cleaner 已审查的同一版本。Deliverer 给出交付结果，Owner 再作最终接受决定。普通小任务不必调用 Deliverer。

Owner 或实际调度 Agent 的人负责按上述结果发起下一角色，不新增“调度 Agent”。已有足够信息时直接交接；只有重大业务或设计选择需要 Owner 决定。

## **5. 阶段关口与结果**

**当前阶段允许进入下一阶段的条件下一步**Task Builder任务可执行、可验收；初始路由已写入 `task.md`NORMAL 交 Coder；COMPLEX 交 AnalystAnalyst`contract.md` 已记录推荐方案、关键不变量和验证要求等待 Owner 明确确认；Analyst 记录确认后的方案与 `APPROVED` 状态，再交 CoderCoder实现、必要测试和自验完成；报告 `READY_FOR_CLEANER`交 CleanerCleaner`CHANGES_REQUIRED`Coder 修复 Finding，再由 Cleaner 复审Cleaner`CLEAN`Owner 检查核心逻辑；里程碑可交 DelivererDeliverer`FAIL`实现问题退回 Coder → Cleaner；设计问题交 Analyst → OwnerDeliverer`PASS` 或 `CONDITIONAL_PASS`Owner 依据证据与剩余风险决定是否接受

复杂任务的 `contract.md` 未标记 `APPROVED` 时，Coder 不开始实现；Owner 的明确确认是修改该状态的依据。Analyst 不得自行批准自己的推荐方案。

Cleaner 逐项将 Acceptance Criteria 标为 `PASS`、`FAIL` 或 `NOT_VERIFIED`。只有全部 AC 有充分证据支持 `PASS`、没有开放的 P0/P1/P2，且适用的关键测试通过，才能给出 `CLEAN`。若真实环境是验证某项 AC 的必要条件但当前不可用，应标为 `NOT_VERIFIED`，说明缺少的条件；不得为了继续流程而将其写成 `PASS` 或宣布 `CLEAN`。Deliverer 在 Cleaner `CLEAN` 后对重要里程碑独立执行交付级验证。

如需改变 AC 或接受与原 AC 不同的行为，由 Owner 明确调整任务定义后再继续审查；Agent 不得用口头“例外”自行宣布通过。P3 是否处理由 Owner 决定。

## **6. 任务基线、Scope 与 Diff**

Task Builder 创建任务时，在 `task.md` 记录任务开始时的 Git 基线（例如 Commit ID）和已有未提交修改的文件清单。若现有修改可能与任务重叠，应保留足以区分任务前后变更的基线证据，或先在独立工作区隔离；仅记录同一文件名不足以区分新旧修改。若尚无可用 Git 基线，应如实记录。已有修改不得擅自覆盖、丢弃或归入 Coder 的成果。

Coder 结束前检查工作区。Cleaner 审查当前任务的**全部相关变更**：基线之后的已跟踪文件、暂存区变更及新增未跟踪文件；同时排除或单独说明任务开始前已存在的修改。若已有修改与任务重叠且无法区分归属，先澄清再判断，不得根据一份不完整的 `git diff` 宣布审查完成。

Cleaner 在 `findings.md` 记录本次审查对象，足以区分 Commit、未提交 Diff 和新增文件。`CLEAN` 只对该对象有效；此后生产代码、测试或任务约束发生实质变化，应由 Cleaner 检查变化并重新给出结论。Deliverer 开始前核对交付对象与最近一次 `CLEAN` 的审查对象一致；不一致则先退回 Cleaner。Owner 执行 Mutation 后，也应恢复到已审查的正确实现。

所有角色只处理当前 Scope。实际实现需要突破明确 Scope 时，停止扩大修改并报告。不得修改无关代码、覆盖 Owner 或其他 Agent 的已有工作，或为了让验证通过而降低测试标准。

## **7. Finding 与复审**

Finding 使用稳定编号 `CLEAN-001`、`CLEAN-002` 等，并说明触发条件、实际与预期行为、影响、证据及必要的修复边界：

**级别含义是否阻塞 CLEAN**P0安全漏洞、数据损坏、严重不可用是P1核心业务行为错误是P2特定场景错误、可靠性问题或明显维护风险是P3低风险问题或非阻塞改进由 Owner 决定是否处理

只有 Cleaner 创建、关闭或重新打开 Finding。Coder 可以引用 Finding 并修复代码，不得改写原始 Finding 或自行关闭。Cleaner 复审原问题、修复 Diff、回归测试和新引入的风险。

Deliverer 发现交付失败时记录在 `delivery.md`；不建立第二套长期 Finding。实现缺陷退回 Coder → Cleaner，设计问题交 Analyst → Owner。

## **8. Owner 的核心逻辑验证**

Cleaner 在实现稳定后，从最终代码中标记真正值得 Owner 理解的权限、安全、事务、并发、库存、幂等、状态流转及一致性逻辑，不罗列 DTO 或普通样板代码。每项给出位置、业务不变量、相关测试；必要时建议少量高价值 Mutation。

Mutation 是可选的人工验证：临时破坏一条关键业务不变量，预期对应测试失败，随后恢复正确实现并确认工作区状态。Owner 不需要逐行重新 Review 全部 Diff。最终接受、Commit、Merge、Push 与 Deploy 均由 Owner 决定；没有明确授权时 Agent 不执行 Push、Force Push、改写历史或生产部署。最终接受结论及明确保留的风险应留在当前任务记录、Issue 或 PR 中，不另建一套状态文档。

## **9. 共同执行原则**

- 优先依据真实代码、测试和运行证据陈述结论；区分已验证事实、推断和未知项。
- Coder 实现并自测；Cleaner 独立审查代码与测试质量；Deliverer 在适用的里程碑独立验证实际运行。三者可以运行部分相同命令，但必须回答各自负责的问题。
- 未运行的检查标记为 `NOT_VERIFIED` 或 `NOT_EXECUTED`，写明原因，不得记为 `PASS`。
- 调查以当前任务为界；证据足以支持下一步时停止无目的调查。
- 任一角色发现无法安全继续的冲突，应输出 `BLOCKED`、证据和所需决定，不得用推测填补关键业务规则。