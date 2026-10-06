# Txx — Tutorial Title

> 本模板为通用占位。使用时不要求机械填满全部章节，根据 Task 的 Depth / Interview Value / Topic Complexity 裁剪。核心原则：No Padding，Project Reality → General Principle。

## 1. 本节结束后你必须能回答什么

列出 3～8 个具体问题（不是「理解 XXX」）：

- <具体问题>
- <具体问题>

## 2. 先放到整个系统里看

用流程 / 调用链 / 数据流 / 事件流说明当前知识所在位置。若 Task 极小且不需要，可简化。

## 3. Source Map

必须基于 `SOURCE_SNAPSHOT`。

| Path | Symbol | Responsibility |
|------|--------|----------------|
| <path> | <symbol> | <职责> |

## 4. 关键源码

只放必要 excerpt，每段说明：

```text
Path:
Symbol:
Why It Matters:
```

然后解释代码。禁止整段复制大文件。

## 5. 运行时到底发生了什么

解释程序运行时真实过程，不解释语法。根据 Task 选用：Request Timeline / Concurrent Timeline / Transaction Timeline / Message Flow / State Transition。

## 6. 为什么这样设计

当前设计解决什么问题；如适用，说明错误 / 较弱方案是什么、为什么没采用。

## 7. 容易混淆的概念

只在真的存在混淆时加入。禁止为模板形式硬写。

## 8. Failure / Concurrency / Consistency

只在 Task 相关时展开，不每个教程都强行写并发。

## 9. 测试如何证明它

定位真实测试：Path / Test Name，解释 Arrange / Act / Assert，以及「证明了什么 / 没证明什么」。

## 10. 面试追问

### 基础

### 进阶

### 压力追问

数量根据 Interview Value 调整。

## 11. Self Test

题型可选：Recall / Reasoning / Code Reading / Scenario / Design Trade-off。题目放前面。

## 12. Reference Answers

独立章节，避免 Owner 一眼看到答案。

## 13. Feynman Check

提供一个要求 Owner 自己解释的问题（如「不看源码，用 2 分钟解释……」），不直接附完美长答案。

## 14. 本节结束标准

Checklist，与 Task Completion Criteria 对齐：

- [ ] <标准>
