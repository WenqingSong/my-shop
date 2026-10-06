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
- `engine/` — 独立 Workflow Engine Go Module（`module workflow-v2-engine`），含 `go.mod`/`go.sum`、`cmd/workflow-check`、`internal/workflow`
- `bin/` — 本地生成的 `workflow-check` binary（Git ignore，不进入 Git）

## Workflow Engine（独立 Module）

Workflow Engine 位于 `.agent/engine/`，是 source-vendored 独立 Go Module，不依赖宿主业务 Go Module、`go.work`、root `replace`；只依赖 Git 仓库与 Go toolchain。

```text
# 构建本地 binary（进入 .agent/bin/，被 Git ignore）
go -C .agent/engine build -o ../bin/workflow-check ./cmd/workflow-check

# 运行（正式 Gate 机器入口优先 direct binary，保留 0/1/2 exit code）
.agent/bin/workflow-check gate <gate> <task>

# 测试 / 静态检查（Engine 与 Business 分开运行）
go -C .agent/engine test ./...
go -C .agent/engine test -race ./...
go -C .agent/engine vet ./...
go -C .agent/engine build ./...
```

Repository Root 通过 `git rev-parse --show-toplevel` 解析，与 Engine Module Root（`.agent/engine/`）无关；workflow config / task / registry 始终位于 `<repo-root>/.agent/workflow.yaml`、`<repo-root>/.agent/tasks/`、`<repo-root>/.agent/registry/`。

## Normal Flow

TaskBuilder → Analyst → Coder → Cleaner → OwnerGate → Deliverer → Owner Integration

## Important

- historical task artifacts are evidence, not current Workflow specification
- normal Agent rules come from roles/specs/design
- PROJECT_ADAPTATION.md is temporary and removed after successful Adoption
