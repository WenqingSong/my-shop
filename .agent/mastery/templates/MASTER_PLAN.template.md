# Mastery Plan

> 本模板为通用占位，不含任何真实业务数据。使用时由 Project Mastery Analyst 填写。

## 1. Metadata

| 字段 | 值 |
|------|-----|
| Project | <仓库 / 项目名> |
| Learning Target | <本次要掌握的功能，一句话> |
| Interview Target | <对应的面试场景 / 岗位 / 追问方向> |
| Source Branch | <被学习代码所在分支> |
| Source Snapshot | <完整 Git Commit SHA，例如 df272012781fa29120f5a1d5aec09cbde041d02b> |
| Plan Version | 1 |

## 2. Mastery Goal

完成这次学习后，Owner 应具备的能力。不要写「理解 XXX」，写可验证的行为：

- 不看源码可以解释……
- 可以回答……
- 可以画出……

## 3. Feature / System Overview

用较短内容建立全局心智模型，例如：

```text
Request → Controller → Service → Logic → Database
```

或：

```text
Client → WebSocket Gateway → NATS → Consumer → DB
```

## 4. Source Map

整个 Learning Target 的主要源码地图。每项：Path / Symbol / Responsibility。

| Path | Symbol | Responsibility |
|------|--------|----------------|
| <path> | <symbol> | <职责> |

## 5. Scope

### IN_SCOPE

- <学习直接覆盖的内容>

### JUST_IN_TIME_PREREQUISITE

- <完成学习所需的最小前置知识>

### OUT_OF_SCOPE

- <明确不学的内容>

## 6. Knowledge Debt

| ID | Topic | Level | Reason | Current Action |
|----|-------|-------|--------|----------------|
| KD-001 | <主题> | BLOCKING / IMPORTANT_LATER / OPTIONAL | <原因> | <本轮是否纳入> |

## 7. Tutorial Plan

| ID | Topic | Depth | Interview Value | Estimated Time | Depends On | Output |
|----|-------|-------|-----------------|----------------|------------|--------|
| T01 | <主题> | L1 / L2 / L3 | HIGH / MEDIUM / LOW | <N> min | - | T01-<slug>.md |

## 8. First-Pass Critical Path

若 Owner 只有约 60～120 分钟，优先学：

```text
Txx → Txx → Txx → Txx
```

Estimated Total Time：<N> min

## 9. Tutorial Task Cards

每个 Task 一个 Task Card。

### Txx — Title

- Goal:
- Depth: L1 / L2 / L3
- Interview Value: HIGH / MEDIUM / LOW
- Estimated Time: <N> min
- Depends On: -
- Output: Txx-<slug>.md

Source Scope:
- <path> — <symbol>
- <path> — <symbol>

Must Answer:
- <具体问题>
- <具体问题>

Key Concepts:
- <关键概念>

Required Evidence:
- <必须引用的真实源码 / 测试>

Do Not Expand Into:
- <禁止展开的相邻主题>

Completion Criteria:
- <完成标准>

## 10. Interview Coverage

映射常见面试追问 → 由哪个 Task 负责，检查是否存在明显面试空洞。

| 面试追问 | 负责 Task |
|----------|-----------|
| <追问> | Txx |

## 11. Skip List

| 内容 | 跳过原因 |
|------|----------|
| <本轮不学的内容> | <为什么> |

## 12. Completion Criteria

整个 Learning Target 结束时，Owner 应能：

- [ ] 画出整体流程
- [ ] 解释核心设计
- [ ] 指出关键源码
- [ ] 解释至少一个错误方案
- [ ] 回答关键并发/一致性问题（如适用）
- [ ] 解释测试如何证明关键性质
- [ ] 完成最终 Interview Review
