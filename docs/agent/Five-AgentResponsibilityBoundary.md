# 角色规范边界&不准越界

## 1. Task Builder

回答：

> **这次到底要做什么？**
> 

输入：

```
Owner 的自然语言需求
```

输出：

```
task.md
findings.md 初始化
core-logic.md 初始化
delivery.md 初始化
```

它不设计复杂方案，不写代码，不 Review。

---

# 2. Analyst

回答：

> **复杂任务应该怎么设计，哪些性质必须成立？**
> 

只在需要时运行。

输出：

```
分析结论
contract.md
关键不变量
验证要求
```

它不写生产代码，不替 Owner 决策。

---

# 3. Coder

回答：

> **按照已经确定的 Task / Contract，把东西真正实现出来。**
> 

负责：

```
代码
测试
修复 Cleaner Finding
```

它是主要代码生产者。

---

# 4. Cleaner

回答：

> **Coder 实际写出来的东西到底对不对？**
> 

负责：

```
完整 Diff Review
独立测试
Finding
复审
core-logic.md
Mutation 建议
```

它不修改生产代码。

---

# 5. Deliverer

回答：

> **已经 Review 通过的东西，作为实际交付物能不能真正运行？**
> 

负责：

```
Build
Integration
Smoke Test
真实数据验证
Docker / Runtime
里程碑验收
delivery.md
```

它不修代码。

---

# 6. Owner

Owner 始终位于所有 Agent 之上。

回答：

> **我要什么？接受什么？哪些关键逻辑我本人必须掌握？**
> 

Owner 负责：

```
需求
关键决策
核心逻辑理解
必要 Mutation
最终接受
Commit / Merge / Deploy 决策
```

---

# 7. 不重复工作的原则

Task Builder 不做 Analyst 的工作。

Analyst 不做 Coder 的工作。

Coder 不宣布自己的代码 CLEAN。

Cleaner 不成为第二个 Coder。

Deliverer 不重新进行完整代码 Review。

Owner 不需要逐行替 Cleaner Review AI 生成的全部代码。

---

# 8. 标准普通任务

```
Owner
 ↓
Task Builder
 ↓
Coder
 ↓
Cleaner
 ↓
Coder Fix（如需要）
 ↓
Cleaner CLEAN
 ↓
Owner 核心逻辑检查 / Mutation
 ↓
Owner Accept
```

---

# 9. 标准复杂任务

```
Owner
 ↓
Task Builder
 ↓
Analyst
 ↓
Owner 确认方案
 ↓
Coder
 ↓
Cleaner
 ↓
Coder Fix
 ↓
Cleaner CLEAN
 ↓
Owner 核心逻辑检查 / Mutation
 ↓
Owner Accept
```

---

# 10. 重要里程碑

```
Owner
 ↓
Task Builder
 ↓
[Analyst if needed]
 ↓
Coder
 ↓
Cleaner
 ↓
Owner 核心验证
 ↓
Deliverer
 ↓
Owner Final Accept
```

---

# 11. 谁能修改生产代码

默认只有：

```
Coder
```

Analyst：

```
NO
```

Cleaner：

```
NO
```

Deliverer：

```
NO
```

Task Builder：

```
NO
```

这样可以明确责任。

---

# 12. 谁负责测试

不是只有一个角色负责测试。

## Coder

负责：

```
编写测试
实现阶段自测
```

## Cleaner

负责：

```
检查测试质量
独立运行测试
检查测试是否能抓住错误
```

## Owner

负责：

```
少量关键 Mutation / 核心语义验证
```

## Deliverer

负责：

```
交付级集成和运行验证
```

四层验证目的不同，不属于重复劳动。

---

# 13. Finding 流向

只允许：

```
Cleaner
 ↓
创建 Finding
 ↓
Coder
 ↓
修复
 ↓
Cleaner
 ↓
CLOSED / REOPENED
```

Deliverer 如果发现实现 Bug：

不创建第二套长期 Finding 系统。

记录 Delivery Failure 后退回：

```
Coder → Cleaner
```

---

# 14. Contract 流向

只在复杂任务中：

```
Analyst
 ↓
起草 contract.md
 ↓
Owner
 ↓
确认
 ↓
Coder / Cleaner / Deliverer
```

后续 Agent 不得擅自改写 Contract。

如果发现 Contract 本身错误：

```
→ Analyst
→ Owner
```

---

# 15. Core Logic 流向

```
Coder 完成
 ↓
Cleaner Review
 ↓
Cleaner 提取 core-logic.md
 ↓
Owner
 ↓
理解 / 质疑 / Mutation
```

Analyst 可以定义业务不变量。

但最终：

```
哪些实际代码承载这些不变量
```

由 Cleaner 根据最终实现标出来。

---

# 16. 文件写入责任

```
task.md
→ Task Builder

contract.md
→ Analyst 起草，Owner 确认

findings.md
→ Cleaner

core-logic.md
→ Cleaner

delivery.md
→ Deliverer
```

Coder 默认不负责维护这些协同文档。

Coder 主要负责：

```
代码 + 测试
```

减少文档竞争。

---

# 17. 项目日常默认模式

不要每次调用全部 Agent。

绝大多数普通任务：

```
Task Builder
→ Coder
→ Cleaner
→ Owner
```

复杂设计：

```
+ Analyst
```

重大里程碑：

```
+ Deliverer
```

---

# 18. 最终原则

Agent 数量不是质量目标。

真正目标是：

```
明确需求
+
正确实现
+
独立质量检查
+
Owner 掌握核心逻辑
+
真实交付验证
```

如果某一步没有提供新的可靠性价值：

不要为了流程而执行它。