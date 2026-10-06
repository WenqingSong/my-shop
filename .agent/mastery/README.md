# Project Mastery Workflow

Project Mastery Workflow 是一个独立的轻量学习体系，与生产 Workflow V2（TaskBuilder → Analyst → Coder → Cleaner → OwnerGate → Deliverer）目的不同、资产隔离。

它解决的问题不是「有没有看过代码」，而是「能否在不依赖源码的情况下解释这个功能」。

## Purpose

把真实项目源码，转化为可掌握的学习任务、高质量教程与面试表达能力。

衡量标准是 Mastery Capability，而不是 Generated Word Count。

## Two Roles

1. **Project Mastery Analyst** — 分析项目、接收 Owner 的「这次我要掌握什么」、锁定 Source Snapshot、决定学习范围、拆 Tutorial Tasks、生成 `MASTER_PLAN.md`。不写具体教程。
2. **Tutorial Writer** — 一个独立 Session 只处理一个 Tutorial Task，基于固定 Snapshot 读取真实源码，输出独立 Markdown 教程。不重新规划整个学习路线。

Question / Quiz / Interview / Feynman 都作为 Tutorial Content 的组成部分，不作为独立 Agent。

## Role Prompts

- `roles/MasteryAnalyst.md` — Project Mastery Analyst 正式执行 Prompt（P2 已实现）。
- `roles/TutorialWriter.md` — Tutorial Writer 正式执行 Prompt（P3 已实现）。

## Output Location

学习产物写入 `docs/learning/<learning-id>/`，例如：

```text
docs/learning/
└── product-view-count/
    ├── MASTER_PLAN.md
    ├── T01-request-flow.md
    └── ...
```

`.agent/mastery/` 只存放本 Workflow 的规则与模板，不存放具体教程。

## Source Snapshot Rule

每一次 Mastery Plan 必须绑定一个不可变 Git Commit SHA（完整 SHA），并写入 `SOURCE_SNAPSHOT`。后续所有 Tutorial Writer 必须基于同一个 Snapshot 分析源码，通过 `git show <snapshot>:<path>`、`git grep <pattern> <snapshot>` 等只读方式读取，不 checkout，避免破坏当前学习分支工作区。

## Normal Flow

```text
Owner: "我要掌握 XXX"
        ↓
Mastery Analyst
        ↓
MASTER_PLAN.md（范围 / 顺序 / 深度 / 时间 / 源码 / 完成标准）
        ↓
T01 → 新 Writer Session → T01-*.md
T02 → 新 Writer Session → T02-*.md
...
        ↓
Owner 学习 / Self Test / Feynman
        ↓
Interview Review
```

## Specification

共享设计与执行规范见 `specs/ProjectMasteryWorkflow.md`。

## Important

- 生命周期仅 `PLAN → WRITE TUTORIALS → LEARN / PRACTICE`（可选 `REPLAN`）。
- 无 State Machine / Gate / Registry / Validator。
- Learning Agent 对业务源码 Read-only，只写 `docs/learning/*`。
