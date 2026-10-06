# Project Mastery Workflow — Shared Learning Protocol

Status: ACTIVE（P1 — 共享协议 / 目录 / 模板 / 约束）

## 1. Purpose

Project Mastery Workflow 是一个轻量学习体系，把真实项目源码转化为可掌握的学习任务、高质量教程与面试表达能力。

它解决的问题不是「有没有看过代码」，而是「能否在不依赖源码的情况下解释这个功能」。

它与生产 Workflow V2（TaskBuilder → Analyst → Coder → Cleaner → OwnerGate → Deliverer）是两个不同目的的体系：

- **Workflow V2**：负责把软件正确交付。
- **Project Mastery Workflow**：负责让开发者真正掌握已经存在的项目实现，用于面试、复习、技术表达、设计解释与源码理解。

衡量标准是 **Mastery Capability**，而不是 Generated Word Count。

## 2. Lifecycle

简单生命周期：

```text
PLAN → WRITE TUTORIALS → LEARN / PRACTICE
```

可选：`REPLAN`。

不引入生产 Workflow 的状态机、Gate、Invariant、Registry、OwnerGate、Deliverer、Evidence Snapshot Validator。这些概念不属于学习体系。

## 3. Two-Role Model

固定两个长期角色，不新增第三个：

### Project Mastery Analyst

- 分析整个项目；
- 接收 Owner 的「这次我要掌握什么」；
- 锁定源码 Snapshot；
- 决定学习范围；
- 拆 Tutorial Tasks；
- 生成 `MASTER_PLAN.md`。

不负责写具体教程。

### Tutorial Writer

- 一个独立 Session 只处理一个 Tutorial Task；
- 重新读取固定 Snapshot 下的真实源码；
- 每个 Task 输出一个独立 Markdown 教程。

不负责重新规划整个学习路线。

Question / Quiz / Interview / Feynman 都作为 Tutorial Content 的组成部分，不作为独立 Agent。禁止新增 LearningReviewer、QuizAgent、FeynmanAgent、InterviewAgent、SourceAgent、PlannerAgent 等长期角色。

> P1 仅定义角色边界。正式 Role Prompt（`MasteryAnalyst.md`、`TutorialWriter.md`）由 P2 / P3 实现。

## 4. Source Snapshot

每一次 Mastery Plan 必须绑定一个不可变 Git Commit SHA。

`MASTER_PLAN` 的 Metadata 必须记录：

```text
PROJECT
LEARNING_TARGET
SOURCE_BRANCH
SOURCE_SNAPSHOT
```

其中 `SOURCE_SNAPSHOT` 必须使用完整 Git Commit SHA，例如：

```text
SOURCE_SNAPSHOT:
df272012781fa29120f5a1d5aec09cbde041d02b
```

目的：防止「今天写 Tutorial → 项目继续开发 → 源码改变 → 教程指向另一版实现」。后续所有 Tutorial Writer 必须基于同一个 Snapshot 分析源码。

## 5. Snapshot 读取方式

不要求 Tutorial Writer checkout 到 `SOURCE_SNAPSHOT`（会破坏当前学习分支工作区）。推荐使用只读 Git 命令：

```text
git show <snapshot>:<path>
git grep <pattern> <snapshot>
git ls-tree -r <snapshot>
git log / show
```

或其他不改变工作树的只读方式。学习文档写在当前 learning branch，源码证据来自固定 Snapshot。

必须区分两个概念：

- **Learning Output HEAD**：当前学习分支，教程写入此处；
- **Source Snapshot**：被学习的业务代码 commit，只读、不前移。

## 6. Source Map

任何教程涉及真实项目实现时，必须能够回到源码。Source Map 至少包含：

- 文件路径；
- 函数 / 类型 / Symbol；
- 为什么相关。

示例（通用占位）：

```text
Source Map

1. `internal/controller/product/product.go`
   - `Detail`
   - HTTP 入口

2. `internal/logic/product/product.go`
   - `Detail`
   - 浏览量自增与商品查询

3. `internal/controller/product/product_test.go`
   - 并发计数测试
```

禁止只写「看 product.go」。

## 7. Scope Control

`MASTER_PLAN` 必须区分：

- **IN_SCOPE**：本轮学习直接覆盖的内容；
- **JUST_IN_TIME_PREREQUISITE**：完成学习所必需的最小前置知识；
- **OUT_OF_SCOPE**：明确不学习的内容。

禁止知识无限递归（功能 → MySQL → InnoDB → 操作系统 → 文件系统 → 硬件）。除非某项知识直接阻塞当前 Interview Target，否则不下钻。

## 8. Depth Model

固定三个 Depth：

| Depth | 名称 | 目标 | 建议篇幅 |
|-------|------|------|----------|
| L1 | KNOW | 知道是什么、在哪里、做什么、基础流程 | 约 800～1500 中文字 |
| L2 | EXPLAIN | 能完整解释流程、关键源码、为什么、基础失败语义、测试验证 | 约 1500～3000 中文字 |
| L3 | DEFEND | 面对深挖能继续解释并发、一致性、trade-off、alternative、failure mode、scalability、测试边界 | 约 3000～5000+ 中文字 |

这些是内容预算，不是 KPI。讲清即可，禁止为了凑字数灌水。

## 9. Interview Value

每个 Tutorial Task 必须有 Interview Value：`HIGH` / `MEDIUM` / `LOW`。

用于 Owner 时间有限时优先学习。不默认所有 Task 都 HIGH。

## 10. Estimated Time

每个 Task 必须提供 Estimated Time，单位为分钟（如 `15 min`、`25 min`、`40 min`）。

含义是 Owner 第一次有效掌握该 Task 的估算时间，不是 Writer 生成 Markdown 的时间。控制总学习成本是正式设计目标（Owner 需兼顾面试、投递、开发、学习）。

## 11. First-Pass Critical Path

`MASTER_PLAN` 必须定义 `FIRST_PASS_CRITICAL_PATH`：若 Owner 只有约 60～120 分钟，应优先学哪些 Task，并给出 Estimated Total Time。

不强迫完整计划全部学完才算有价值。完整计划可以更长，但必须有最短高价值学习路径。

## 12. Knowledge Debt

允许 Analyst 在计划中发现「当前功能依赖 Owner 尚未系统掌握的知识」，记录为 Knowledge Debt，分类固定为：

| Level | 含义 |
|-------|------|
| BLOCKING | 掌握当前目标所必需 |
| IMPORTANT_LATER | 重要但不阻塞当前目标 |
| OPTIONAL | 可学可不学 |

字段：`ID` / `Topic` / `Level` / `Reason` / `Current Action`。

关键规则：Knowledge Debt 不等于自动创建 Tutorial Task。只有 `BLOCKING` 或当前 Interview Value 明显 HIGH 的才进入当前学习计划；否则记录，不打断主线。

## 13. MASTER_PLAN Contract

`MASTER_PLAN.md` 是导航，不是教材。它定范围、定顺序、定深度、定时间、定源码、定完成标准；具体教学由 Tutorial Writer 完成。禁止 Analyst 在 Plan 中提前写大量解释。

模板见 `templates/MASTER_PLAN.template.md`，核心章节：

1. Metadata（Project / Learning Target / Interview Target / Source Branch / Source Snapshot / Plan Version）
2. Mastery Goal
3. Feature / System Overview
4. Source Map
5. Scope（IN_SCOPE / JUST_IN_TIME_PREREQUISITE / OUT_OF_SCOPE）
6. Knowledge Debt
7. Tutorial Plan（表格：ID / Topic / Depth / Interview Value / Estimated Time / Depends On / Output）
8. First-Pass Critical Path
9. Tutorial Task Cards
10. Interview Coverage
11. Skip List
12. Completion Criteria

## 14. Tutorial Contract

模板见 `templates/TUTORIAL.template.md`。默认高质量结构包含：本节必须回答的问题、系统定位、Source Map、关键源码、运行时行为、为什么这样设计、容易混淆的概念、Failure / Concurrency / Consistency、测试如何证明、面试追问、Self Test、Reference Answers、Feynman Check、本节结束标准。

模板不是要求每节机械填满。根据 Depth、Interview Value、Topic Complexity 裁剪。

## 15. One Task One Session

```text
T01 → Tutorial Writer Session A
T02 → 新 Tutorial Writer Session B
T03 → 新 Tutorial Writer Session C
```

禁止一个 Writer Session 连续写全部教程。原因：控制上下文、降低前文污染、提升模型输出稳定性、每个 Tutorial 可独立重做、某一节质量差不会污染后续所有节。

## 16. Context Boundary

Analyst → Writer 的上下文契约只需：

1. `MASTER_PLAN.md`
2. 一个明确 Task ID
3. `SOURCE_SNAPSHOT`
4. 当前仓库源码

Writer 自己从 MASTER_PLAN 读取该 Task Card（Task Goal / Source Scope / Depth / Interview Value / Must Answer / Completion Criteria）。不把完整 Analyst 推理过程复制给 Writer。

## 17. Source Excerpt Policy

不把整个源文件复制进 Markdown。原则：`Path + Symbol + 关键 Code Excerpt + 解释`。关键代码 excerpt 只复制完成当前教学目标真正需要的部分，通常 5～30 行，超过范围必须有明确教学价值。禁止为「内容完整」把 300 行文件整段贴进教程。源码较长时指出文件、Symbol、逻辑范围后解释。

## 18. Tutorial 内容自适应

No Padding。禁止为了篇幅重复解释、写与当前源码无关的教科书内容、每个 Task 都追溯到基础原理最深层。顺序是 Project Reality → General Principle，不是反过来。

## 19. 问题设计

教程题目主要分三层：

- **Recall**：检查记忆；
- **Reasoning**：检查理解；
- **Interview Pressure**：检查面试表达（目的不是否定当前设计，而是说清 V1 目标、当前 trade-off、规模升级后的演进方向）。

## 20. Self Test / Feynman / Interview Review

- **Self Test**：题型可选 Recall / Reasoning / Code Reading / Scenario / Design Trade-off；题目放前，答案放独立 Reference Answers 章节。
- **Feynman Check**：要求 Owner 自己输出解释，不直接附完美长答案。
- **Interview Review**：中等及以上复杂度 Learning Target 应安排在最后，包含 30 秒版本、2 分钟版本、深挖追问、Whiteboard、Resume Mapping。不是所有极小知识点都生成。

## 21. 知识缺口处理

Tutorial Writer 发现 Task Card 依赖 Analyst 未发现的关键前置知识时：

- 不无限展开；
- 在 Tutorial 中增加 New Knowledge Debt（`KD-NEW-xxx`，标注 BLOCKING / IMPORTANT_LATER / OPTIONAL）；
- 若真正 BLOCKING 到无法完成当前教学，STOP 并报告 `PLAN_GAP`；
- 不自行把一个 Task 扩成五个 Task（Plan 调整属于 Mastery Analyst 职责）。

## 22. Read-only Source Boundary

两个学习角色默认：Source Read-only + Learning Docs Writer。

允许：read / search / `git show` / `git grep` / `git log` / inspect tests / 写 `docs/learning/*`。

禁止：修改业务代码、修 bug、改 migration、改测试、改 Registry、改 Workflow V2 state、顺手优化源码。

学习中发现真实代码问题，只记录 `SOURCE_FINDING`，不修。

## 23. Learning Output Location

- `.agent/mastery/` = 学习 Workflow 本身的规则与模板。
- `docs/learning/` = 真正给 Owner 阅读和学习的材料。

不把具体教程写进 `.agent`。

## 24. Plan Version / Learning ID / 文件命名

`MASTER_PLAN` 记录 `Plan Version`（如 `1`）。Source Snapshot 改变或 Learning Scope 重大变化时不静默覆盖，产生新 Plan Version 或重新生成。P1 只定义概念，不实现版本管理系统。

Learning Target 使用 kebab-case learning id（如 `product-view-count`、`iam-refresh-token`、`flash-sale-stock-deduction`）。目录 `docs/learning/<learning-id>/`，文件 `MASTER_PLAN.md` + `Txx-<slug>.md`（Txx 保证排序稳定，slug 简短可理解）。

## 25. Git 语义

不引入复杂 Git Gate。当前 learning branch 由 Owner 准备。Agent 可写学习协议/模板、写 `docs/learning`、commit / push 当前 learning branch（Owner 授权后）。`SOURCE_SNAPSHOT` 永远指向被学习的业务代码 commit，即使 learning branch 后续新增教程也不前移，除非 Owner 明确开始新的 Mastery Plan Version。

## 26. No Heavy State Machine

明确禁止设计类似生产 Workflow 的 20 个状态、Gate、Invariant、Registry、OwnerGate、Delivery。只保留简单生命周期（§2）。

如果实现中出现 `state.yaml`、gate command、validator、`workflow-check-learning`，即属过度设计，应停止。
