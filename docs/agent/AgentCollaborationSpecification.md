# 全局 Agent 协同规范

## 1. 目标

本项目采用 AI Agent 辅助开发。

核心目标不是让 Agent 完全替代开发者，而是：

- Agent 承担大量代码实现、测试、Review 和重复性工程工作；
- Owner 掌握需求、核心设计和关键业务逻辑；
- 每个任务具有明确 Scope 和验收标准；
- 所有重要结论可以追踪；
- 不因为 Agent 协作引入无意义的流程复杂度。

---

# 2. 角色

项目包含五种 Agent。

## Task Builder

负责建立当前任务的协同文件。

不写生产代码。

## Coder

负责：

- 实现代码；
- 编写或补充测试；
- 修复 Cleaner Findings。

## Cleaner

独立检查 Coder 的实现。

负责：

- Review Diff；
- 运行必要测试；
- 检查业务正确性；
- 检查测试质量；
- 产生 Findings；
- 标记 Owner 应重点理解的核心逻辑；
- 给出适合人工 Mutation 验证的关键不变量。

Cleaner 默认不修改生产代码。

## Analyst

只在复杂任务中启用。

负责：

- 调查现有实现；
- 分析架构和依赖；
- 找出根因；
- 比较重要方案；
- 给出设计约束。

Analyst 不负责最终业务决策。

## Deliverer

只在重要里程碑或最终交付时启用。

负责独立验证：

- Build；
- Test；
- Integration；
- Race；
- API；
- Docker；
- 部署或其他交付要求。

---

# 3. Owner

Owner 是最终负责人。

Owner 不是 Agent。

Owner 负责：

- 提出需求；
- 决定 Scope；
- 决定重要架构方案；
- 理解关键业务逻辑；
- 对关键不变量进行必要的人工验证；
- 决定任务是否接受；
- 决定是否 Commit、Merge、Deploy。

Agent 不得代替 Owner 做最终接受决定。

---

# 4. 默认工作流

普通任务：

```
Owner
  ↓
Task Builder
  ↓
Coder
  ↓
Cleaner
  ↓
┌────────────────────┐
│ 有 Finding         │
↓                    │
Coder 修复            │
↓                    │
Cleaner 复审 ─────────┘
  ↓
CLEAN
  ↓
Owner 核心逻辑验证
  ↓
Task Done
```

复杂任务：

```
Owner
  ↓
Task Builder
  ↓
Analyst
  ↓
Owner 确认关键方案
  ↓
Coder
  ↓
Cleaner
  ↓
Owner
```

重要里程碑：

```
Coder
 ↓
Cleaner CLEAN
 ↓
Owner 核心验证
 ↓
Deliverer
 ↓
Owner 最终决定
```

---

# 5. 协同目录

每个独立任务建立目录：

```
.agent/tasks/<task-slug>/
```

默认包含：

```
.agent/tasks/<task-slug>/
├── task.md
├── findings.md
├── core-logic.md
└── delivery.md
```

复杂任务可以增加：

```
contract.md
```

---

# 6. 文件职责

## task.md

当前任务的唯一任务定义。

包括：

- Goal；
- Scope；
- Out of Scope；
- Acceptance Criteria；
- Relevant Context；
- Verification；
- 当前任务状态。

Task Builder 负责初始化。

后续 Agent 只能读取，不得自行改变任务目标和验收标准。

---

## findings.md

Cleaner 的 Review 结果。

包含：

- Finding ID；
- 严重程度；
- 文件位置；
- 实际行为；
- 预期行为；
- 影响；
- 证据；
- 修复状态。

Coder 可以引用 Finding，但不得修改 Cleaner 原始 Finding。

Cleaner 负责关闭或重新打开 Finding。

---

## core-logic.md

Cleaner 在 Review 完成后生成。

只记录 Owner 真正值得重点理解的代码。

例如：

- 权限判断；
- 事务边界；
- 库存扣减；
- 一人一单；
- 幂等；
- 状态流转；
- Redis Lua；
- Kafka Consumer；
- 补偿逻辑；
- 并发控制。

不记录普通 DTO、简单 CRUD、样板代码。

---

## contract.md

只有复杂任务才创建。

记录：

- 重要设计决定；
- 接口边界；
- 数据一致性要求；
- 行为不变量；
- 错误语义；
- 关键限制。

主要由 Analyst 产生，由 Owner 确认。

---

## delivery.md

重要里程碑使用。

由 Deliverer 写入：

- 验收环境；
- 测试命令；
- 验收结果；
- 未验证事项；
- 剩余风险；
- 最终交付状态。

普通小任务可以保留空文件，不要求填写。

---

# 7. 事实优先级

发生冲突时按照以下顺序判断：

```
1. Owner 当前明确指令
2. task.md
3. contract.md
4. 真实代码
5. 自动化测试结果
6. Agent 报告
7. 旧文档或注释
```

但如果：

```
task.md / contract.md
```

与真实代码存在明显冲突，Agent 不得偷偷选择一边。

必须明确报告冲突。

---

# 8. 公共规则

所有 Agent 都必须遵守：

1. 不擅自扩大当前任务 Scope。
2. 不修改与当前任务无关的代码。
3. 不覆盖 Owner 或其他 Agent 已有修改。
4. 不为了测试通过而降低测试标准。
5. 不删除失败测试来制造成功。
6. 不把推测写成已验证事实。
7. 重要结论应尽量提供代码、Diff、测试或日志证据。
8. 不因为存在 AI Agent 就跳过错误处理和测试。
9. 没有明确授权时不得 Push、Force Push 或改写 Git 历史。
10. Agent 不得自行决定部署生产环境。
11. 已经获得完成任务所需信息后，应开始执行，不进行无目的调查。
12. 如果实际实现要求突破明确 Scope，停止扩大修改并报告。

---

# 9. Finding 严重程度

统一使用：

```
P0
安全漏洞、数据损坏、严重不可用。

P1
核心业务行为错误，禁止接受当前实现。

P2
特定场景错误、可靠性问题、明显维护风险。

P3
低风险问题或非阻塞改进建议。
```

P0、P1、P2 默认必须解决后才能认为 Cleaner CLEAN。

P3 是否处理由 Owner 决定。

---

# 10. 核心逻辑原则

Owner 不需要逐行检查 Agent 生成的全部代码。

Cleaner 应负责从完整 Diff 中筛选真正重要的业务逻辑。

核心逻辑通常满足至少一个条件：

- 决定权限；
- 修改资金、库存、订单等重要数据；
- 涉及事务；
- 涉及并发；
- 涉及缓存一致性；
- 涉及幂等；
- 涉及消息可靠性；
- 涉及状态机；
- 涉及安全边界；
- 出错可能造成明显业务后果。

---

# 11. Mutation 验证原则

Cleaner 可以针对重要业务不变量提出 Mutation 建议。

例如原代码：

```
if user.IsAdmin == 1 {
    // allow
}
```

Cleaner 可以建议 Owner 临时修改：

```
if user.IsAdmin == 0 {
```

然后重新执行对应测试。

预期：

```
测试失败
```

说明现有测试能够发现该业务语义被破坏。

Mutation 只针对重要逻辑。

不要对普通 CRUD 和样板代码机械执行。

验证完成后必须恢复正确实现。