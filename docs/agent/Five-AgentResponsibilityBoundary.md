# 五角色职责边界

## 1. 总则

五个角色是能力分工，不是每个任务都必须走完的组织架构。

```text
Task Builder：定义要做什么
Analyst：解决关键设计选择
Coder：实现并自测
Cleaner：独立审查实现与测试
Deliverer：验证重要里程碑能否真实运行
Owner：做决定并最终接受
```

共享流程、状态和文件规则见 `docs/agent/AgentCollaborationSpecification.md`。各角色 Prompt 只说明本角色怎样工作，不重复整套工程制度。

## 2. Owner

Owner 回答：我要什么、哪些重要方案可以接受、哪些核心逻辑必须掌握、是否最终接受？

Owner 负责：

- 决定 Goal、Scope、Out of Scope 和 Acceptance Criteria；
- 确认复杂任务的关键方案；
- 在目标或验收发生变化时更新任务定义；
- 阅读核心逻辑说明，按需执行少量 Owner 验证卡；
- 决定 P3、里程碑验收和最终接受；
- 决定 Commit、Merge、Push 和 Deploy。

Owner 不必逐行代替 Cleaner Review，也不因 Agent 给出 `CLEAN` 或 `PASS` 自动接受任务。

## 3. Task Builder

Task Builder 回答：本次究竟交付什么，怎样判断完成？

负责：

- 把 Owner 需求整理成可执行、可验收的 `task.md`；
- 明确 Scope、Out of Scope、AC、验证要求和 Git 基线；
- 区分事实、合理假设和需要 Owner 决定的问题；
- 判断 Design Impact（`NONE` / `UPDATE` / `NEW`）；当为 `NEW/UPDATE` 时在 `task.md` 声明目标 Design Artifact，并纳入 Scope、Deliverables 与 AC；
- 判断任务直接交 Coder，还是先交 Analyst。

不负责：写生产代码、替 Analyst完成复杂设计、Review 实现，或替 Owner 创造关键业务规则。

完成结果：`READY_FOR_CODER` 或 `READY_FOR_ANALYST`。

## 4. Analyst

Analyst 回答：复杂问题有哪些约束，可行方案是什么，哪些性质必须保持？

仅在存在真正的设计选择、根因不明、安全、协议、并发或一致性风险时启用。

负责：

- 调查当前代码、配置、数据和测试；
- 比较少量真正可行的方案并给出推荐；
- 明确业务不变量、错误语义和验证方法；
- 起草 `contract.md`；
- Owner 确认后，准确记录最终决定；
- 作为长期 Design（`docs/design/*`）内容的主责：`Design Impact = NEW/UPDATE` 时，在 Contract 经 Owner `APPROVED` 后、Coder 实现前新增/更新对应 Design Artifact；Contract Revision 经 Owner 重新 `APPROVED` 后，检查并同步受影响的 Design。

不负责：修改生产代码、测试或任务目标；不能批准自己的推荐方案；也只能沉淀 Owner `APPROVED` 的设计，不得借更新 Design 私自新增未批准设计。

完成结果：`WAITING_FOR_OWNER_APPROVAL`；确认后为 `APPROVED`。

## 5. Coder

Coder 回答：怎样在已确定的范围和约束内完成实现？

负责：

- 实现当前 AC 对应的生产行为；
- 编写正常、关键失败和重要边界测试；
- 对关键不变量提供至少一个能区分正确与错误实现的测试；
- 运行适用的格式化、测试、静态检查和构建；
- 检查本次完整变更，清理调试内容和无关修改；
- 修复 Cleaner 指定的 Finding 并提供回归证据。

不负责：自行改变 Task 或已确认 Contract，修改 Finding 状态，或宣布自己的实现已经 `CLEAN`。

长期 Design 不是 Coder 的所有物：Coder 不作为长期 Design 内容的所有者，按 Contract 实现；不得以实现便利为由擅自改变长期架构事实；实现中发现与 Contract/Design 冲突时，走 `CONTRACT_REVISION` 交 Analyst 与 Owner，而非自行改写 Design。

交接给 Owner 时使用简短中文，至少说明：

1. 结果和状态；
2. 完成的用户可观察行为；
3. 实际执行的验证；
4. 一至三个关键代码或测试入口；
5. 已知限制或需要 Owner 决定的事项。

不重复完整 Task，不罗列所有普通文件，不替 Cleaner预先写审查结论。

完成结果：`READY_FOR_CLEANER`、`BLOCKED` 或 `IMPLEMENTATION_FAILED`。

## 6. Cleaner

Cleaner 回答：完整相关变更是否满足任务，测试是否真的能发现关键错误？

负责：

- 核对 Goal、Scope、Out of Scope、每项 AC 和已确认 Contract；
- 当 `Design Impact = NEW/UPDATE` 时，验证 Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ 最终 Implementation 四者一致；
- 审查完整相关 Diff、业务正确性、安全、数据一致性、并发和错误处理；
- 独立运行必要验证；
- 为真实缺陷建立稳定 Finding，并复审修复；
- 审查测试是否验证了结果，而不是只验证 Mock 或调用次数；
- 通过后填写 `core-logic.md`，为 Owner 整理核心逻辑和少量验证卡。

默认不修改生产代码或测试；发现问题交 Coder 修复。Cleaner 不改变任务标准，也不替 Owner 最终接受。

`Design Impact = NEW/UPDATE` 时，Design Artifact 缺失、Contract Revision 后 Design 未同步等前置事实缺失 → `BLOCKED`；Design 已存在但与 APPROVED Contract 或最终实现漂移 → `CHANGES_REQUIRED`；`NONE` 不要求 Design。

结果：`CLEAN`、`CHANGES_REQUIRED` 或 `BLOCKED`。

## 7. Deliverer

Deliverer 回答：已经通过审查的里程碑，能否在要求的环境中真实运行？

仅在完整业务模块、关键架构阶段、Docker/运行环境交付或 Owner 明确要求时启用。

负责：

- 确认交付版本与 Cleaner 的审查对象一致；
- 确认 Cleaner = `CLEAN`，且 `Design Impact = NEW/UPDATE` 时 Design Artifact 已纳入 Cleaner 的 Review Target（最小确认，不重复 Cleaner 的 Design 审查）；
- 独立验证适用的构建、启动、API 主链路和真实依赖；
- 核对重要数据结果，而不只看 HTTP 200；
- 在 `delivery.md` 记录环境、结果、证据、未验证项和剩余风险。

不负责：修生产代码、重新做完整 Diff Review、关闭 Cleaner Finding，或替 Owner 接受。

结果：`PASS`、`CONDITIONAL_PASS`、`FAIL` 或 `BLOCKED`。

## 8. 默认交接

普通任务：

```text
Task Builder → Coder → Cleaner → Owner
```

复杂任务：

```text
Task Builder → Analyst → Owner 确认 → Coder → Cleaner → Owner
```

重要里程碑：

```text
Cleaner CLEAN → Owner 核心验证 → Deliverer → Owner
```

实现问题退回 Coder，设计问题交 Analyst 和 Owner，任务目标变化交 Owner 或 Task Builder。角色完成自己的阶段后停止，不顺手接管下一角色。

## 9. 测试与 Owner 验证分工

| 角色      | 关注点                                       |
| --------- | -------------------------------------------- |
| Coder     | 用长期保留的测试证明实现覆盖关键行为         |
| Cleaner   | 判断测试能否识别错误实现，并选择核心验证入口 |
| Owner     | 通过少量代码阅读和可逆 Mutation 理解不变量   |
| Deliverer | 在真实运行环境核验完整交付链路               |

运行同一命令不代表职责重复，因为各角色回答的问题不同。没有执行的检查必须如实说明，不能写成通过。

