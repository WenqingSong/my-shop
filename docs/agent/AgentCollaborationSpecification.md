# Agent 协同规范

## 1. 目标

Agent 用来扩大实现、测试和审查能力；Owner 决定需求、关键方案和最终是否接受，并掌握重要业务逻辑。

本文件只规定跨角色协作。长期工程规则见仓库根目录 `AGENTS.md`，角色边界见 `docs/agent/Five-AgentResponsibilityBoundary.md`，当前需求以 `.agent/tasks/<task-slug>/task.md` 为准。

角色数量不是质量指标。只启用能为当前任务增加明确价值的角色。

## 2. 指令与事实来源

按以下顺序执行：

1. Owner 当前明确指令；
2. 当前任务的 Goal、Scope、Out of Scope 和 Acceptance Criteria；
3. Owner 已确认的 `contract.md` 或设计；
4. `AGENTS.md`；
5. 本规范、职责边界和角色 Prompt。

代码、测试、日志和运行结果用于判断当前事实，不能自行改变任务目标。发现冲突时说明证据和需要 Owner 决定的事项，不暗自选边。

任何优先级都不能授权泄漏凭据、绕过认证授权、吞掉关键错误，或用降低测试标准制造成功。

## 3. 任务文件

每个独立任务使用：

```text
.agent/tasks/<task-slug>/
├── task.md
├── findings.md
├── core-logic.md
├── delivery.md
└── contract.md       # 仅复杂任务需要
```

| 文件            | 用途                               | 默认维护者                  |
| --------------- | ---------------------------------- | --------------------------- |
| `task.md`       | 目标、范围、AC、验证要求和任务基线 | Task Builder；Owner 可修改  |
| `contract.md`   | 已确认的关键设计、不变量和错误语义 | Analyst 根据 Owner 决定维护 |
| `findings.md`   | Review Finding 与复审结论          | Cleaner                     |
| `core-logic.md` | Owner 应理解的核心逻辑和验证卡     | Cleaner                     |
| `delivery.md`   | 里程碑运行验收                     | Deliverer                   |

Coder 负责生产代码和测试，不为展示过程而重复维护上述文档。

## 4. 默认路由

普通任务：

```text
Owner → Task Builder → Coder → Cleaner → Owner
```

存在关键设计选择、安全边界、并发一致性或根因不明时：

```text
Task Builder → Analyst → Owner 确认 → Coder → Cleaner → Owner
```

仅在重要里程碑或 Owner 明确要求时增加 Deliverer：

```text
Cleaner CLEAN → Owner 核心验证 → Deliverer → Owner 最终决定
```

普通任务不为凑齐五个角色调用 Analyst 或 Deliverer。Task Builder 也可以由当前主 Agent 完成，不要求单独启动一个 Agent。

## 5. 阶段关口

| 角色         | 可以交接的条件                        | 结果                                             |
| ------------ | ------------------------------------- | ------------------------------------------------ |
| Task Builder | 任务可执行、可验收                    | `READY_FOR_CODER` / `READY_FOR_ANALYST`          |
| Analyst      | 推荐方案和待确认事项已写入 Contract   | `WAITING_FOR_OWNER_APPROVAL`                     |
| Analyst      | Owner 的决定已准确记录                | `APPROVED`                                       |
| Coder        | 实现、必要测试和自验完成              | `READY_FOR_CLEANER`                              |
| Cleaner      | 需要修复                              | `CHANGES_REQUIRED`                               |
| Cleaner      | 所有 AC 有充分证据，且无开放 P0/P1/P2 | `CLEAN`                                          |
| Deliverer    | 里程碑运行验收完成或无法继续          | `PASS` / `CONDITIONAL_PASS` / `FAIL` / `BLOCKED` |

复杂任务的 Contract 未获 Owner 确认时，Coder 不开始依赖该决定的实现。`CLEAN` 和 `PASS` 都不能替代 Owner 的最终接受。

无法安全继续时输出 `BLOCKED`，同时给出：阻塞事实、已有证据、需要谁决定什么。

## 6. 证据、基线与 Scope

- Task Builder 记录任务开始时的 Git 基线和已有修改。
- Coder 保留已有工作，只修改当前 Scope 所需内容。
- Cleaner 审查基线后的全部相关变更，包括新增未跟踪文件；不能只看默认 `git diff`。
- `CLEAN` 只对 Cleaner 记录的审查对象有效。生产代码、测试或约束发生实质变化后必须复审。
- Deliverer 验证的版本必须与最近一次 `CLEAN` 对应。
- 未执行的检查标记为 `NOT_VERIFIED` 或 `NOT_EXECUTED`，并说明原因与影响。

需要突破 Scope 才能正确完成时，停止扩张并交 Owner 决定。不得覆盖来源不明的已有修改，也不得把别人的修改算作自己的成果。

## 7. Finding

Cleaner 使用稳定编号：`CLEAN-001`、`CLEAN-002`……

| 等级 | 含义                               | 是否阻塞 CLEAN |
| ---- | ---------------------------------- | -------------- |
| P0   | 安全漏洞、数据损坏、严重不可用     | 是             |
| P1   | 核心业务行为错误                   | 是             |
| P2   | 特定场景错误、可靠性或明显维护风险 | 是             |
| P3   | 低风险改进                         | 由 Owner 决定  |

Finding 必须说明触发条件、实际行为、预期行为、影响、证据和修复边界。只有 Cleaner 创建、关闭或重新打开 Finding；Coder 负责修复并提供回归证据。

## 8. Owner 核心验证

Owner 不需要逐行 Review 全部代码。交付应帮助 Owner 理解：

- 关键业务不变量是什么；
- 生产代码在哪里实现；
- 哪个测试证明它；
- 破坏该不变量时，测试能否失败。

Coder 为关键行为编写可长期保留的测试，并在交接中指出最重要的测试入口。Cleaner 审查测试可信度，在 `core-logic.md` 中整理一至两个高价值的 Owner 验证卡。

验证卡可以包含可逆 Mutation：先确认测试通过，临时破坏一条不变量，确认指定测试失败，再恢复代码并重新通过。Mutation 只用于理解和检验测试，执行后必须恢复正确实现并确认工作区状态。

## 9. 中文与交接表达

- Agent 面向 Owner 的分析、说明、文档和最终输出默认使用中文。
- 结论先行，只保留完成情况、关键证据、风险和下一步；省略重复背景、普通样板代码和可直接从 Diff 获得的完整文件清单。
- 只输出与当前结果有关的章节，不为满足模板制造空段落。
- 状态标识、代码标识符、API 字段、命令和协议名可以保留英文，但要用中文解释。
- 表格只用于映射和对比；简单交接优先使用短列表。

详细证据应写入对应任务文件、测试或 Git Diff。聊天交接用于帮助 Owner 快速判断，而不是复制所有过程记录。

## 10. 权限与最终决定

没有 Owner 明确授权时，Agent 不执行 Push、Force Push、改写历史、生产部署或其他高影响外部操作。

Owner 决定关键业务规则、Contract、是否接受 P3、是否启动 Deliverer，以及最终接受、Commit、Merge、Push 和 Deploy。