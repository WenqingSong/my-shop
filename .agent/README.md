# Workflow V2 Runtime Assets

`.agent/` 是 Workflow V2 的 Repository Runtime Namespace：集中存放角色 Prompt、协作规范、职责边界、Adoption 协议、模板、机器配置、Registry 与运行时 Task Artifact。Agent 正常执行 Workflow 时，主要读取本目录。

## Entry

仓库 Agent 入口：`../AGENTS.md`

架构 Design：`../docs/design/agent-workflow.md`

## Structure

- `roles/` — 六个独立 Agent Role Prompt
- `specs/` — 协作协议、Authority / Responsibility Boundary、Project Adoption
- `templates/` — Workflow / Adoption 临时模板
- `workflow.yaml` — Workflow Engine machine configuration
- `registry/` — shared resource registries
- `tasks/` — task runtime artifacts

## Normal Flow

TaskBuilder → Analyst → Coder → Cleaner → OwnerGate → Deliverer → Owner Integration

## Important

- historical task artifacts are evidence, not current Workflow specification
- normal Agent rules come from roles/specs/design
- PROJECT_ADAPTATION.md is temporary and removed after successful Adoption

## Separate: Project Mastery Workflow

`.agent/mastery/` 是独立的 Project Mastery Workflow（学习 / 面试掌握），与 Workflow V2 生产开发流程目的不同、资产隔离，不参与 Workflow V2 状态机。入口：`mastery/README.md`。
