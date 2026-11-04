# Workflow V3 Runtime Assets

`.agent/` 是 Workflow V3 的 Repository Runtime Namespace：集中存放角色 Prompt、协作规范、职责边界、Adoption 协议、模板、机器配置、Registry 与运行时 Task Artifact。Agent 正常执行 Workflow 时，主要读取本目录。

> **Workflow V3 Baseline**（`.agent/VERSION` = `3.4.0`）
>
> 当前 Workflow major version = **3**。Baseline 包含：seven-role independent-session workflow（`NORMAL` / `COMPLEX` 六角色 + `ARCHITECT` 的 Architect）、TaskBuilder 自主初始化（自动基线发现 / 自动命名 / 既有修改保护）、三选一路线决策与 `state.yaml.route`、七角色统一出站交接契约、Git Lifecycle 完成条件与只读自检、Evidence Snapshot、machine gates、Generic Shared Resources、strict YAML、two-round loop circuit breaker（DANGER）、independent Workflow Engine module、Adoption mechanism、release engineering（可校验资产与 `workflow-check version`）。
>
> Release Status：源码版本 `3.4.0` 由 `v3.4.0` Tag 推送触发发布流水线；发布完成与否以 CNB Release、附件校验与 Git Tag 核验结果为准。
>
> V3.4 的主要变化：角色可在**全新 Session** 中只凭角色名与 Task 目录开工（Session Bootstrap Protocol、Stage Derivation Table、`mode` / Finding 推导、开工声明）；出站交接收敛为四字段 `RESULT` / `NEXT_ROLE` / `TASK_PATH` / `OWNER_ACTION`，取消强制 `HANDOFF_PROMPT`；失败原因统一落 `loop-ledger.md` 与各角色 Artifact；TaskBuilder 增加 `UPDATE_COMPLETE` 终态与 CLEAN 失效规则。五个 Gate、角色数量与 `state.yaml` schema 不变。
>
> Cross-project Adoption Dogfood：**NOT PERFORMED**（DEFERRED BY OWNER）。
>
> 命名约定：`module workflow-v2-engine` 是 historical technical identifier（本轮不随 Release Marking 修改）；state schema v2/v3 版本号与 Workflow major version 相互独立。

## Entry

Workflow 专用指令入口：`specs/WorkflowAgentDirective.md`

- 只适用于被明确激活的 Workflow Role Session；普通 Agent 不自动加载，也不被套用 Workflow 的 Git 白名单、Gate 与 Handoff 约束。
- 角色必须显式读取；缺失或不可读时输出 `BLOCKED`，不得凭记忆继续。
- 宿主根 `AGENTS.md` 是宿主项目的业务规范，可选：存在时角色读取并遵守，不存在时不创建、不阻断。

架构 Design：`../docs/design/agent-workflow.md`

## Structure

- `roles/` — 七个独立 Agent Role Prompt（`NORMAL` / `COMPLEX` 的六个角色 + `ARCHITECT` 的 Architect）
- `specs/` — Workflow Agent Directive（Workflow 专用入口）、协作协议、Authority / Responsibility Boundary、路线选择（`WorkflowRouteSelection.md`）、统一交接契约（`WorkflowHandoffContract.md`）、Git Lifecycle（`WorkflowGitLifecycle.md`）、循环熔断、Project Adoption / 安装 / 升级
- `specs/tools/` — 只读 Git Lifecycle 自检（`git-lifecycle-check`：baseline / preflight / commit-check / push-check / verify-remote / handoff-check，SPEC_ONLY，不拦截直接 Git 写操作）
- `templates/` — Workflow / Adoption / upstream provenance 模板
- `adoption/` — 轻量 Source Distribution / install / upgrade / verify / restore 工具
- `workflow.yaml` — Workflow Engine machine configuration（宿主真实配置，不来自发布包）
- `upstream.yaml` — 宿主实际安装来源、B 快照与 binary 校验（只由成功事务写入）
- `registry/` — shared resource registries（按需启用，默认不存在）
- `tasks/` — task runtime artifacts（按需创建）
- `engine/` — 独立 Workflow Engine Go Module（`module workflow-v2-engine`），含 `go.mod`/`go.sum`、`cmd/workflow-check`、`internal/workflow`
- `bin/` — 本地生成的 `workflow-check` binary（Git ignore，不进入 Git）

## Workflow Engine（独立 Module）

Workflow Engine 位于 `.agent/engine/`，是 source-vendored 独立 Go Module，不依赖宿主业务 Go Module、`go.work`、root `replace`；只依赖 Git 仓库与 Go toolchain。

```text
# 构建本地 binary（进入 .agent/bin/，被 Git ignore）
go -C .agent/engine build -o ../bin/workflow-check ./cmd/workflow-check

# 运行（正式 Gate 机器入口优先 direct binary，保留 0/1/2 exit code）
.agent/bin/workflow-check gate <gate> <task>

# 路线事实自检（R-1 ~ R-4，不是 Gate；历史任务需显式 --allow-legacy）
.agent/bin/workflow-check route <task>
.agent/bin/workflow-check route <task> --allow-legacy

# 自描述版本信息（不读取 workflow.yaml，不需要仓库根解析）
.agent/bin/workflow-check version
.agent/bin/workflow-check version --json

# 测试 / 静态检查（Engine 与 Business 分开运行）
GOWORK=off go -C .agent/engine test ./...
GOWORK=off go -C .agent/engine test -race ./...
GOWORK=off go -C .agent/engine vet ./...
GOWORK=off go -C .agent/engine build ./...
```

Repository Root 通过 `git rev-parse --show-toplevel` 解析，与 Engine Module Root（`.agent/engine/`）无关；workflow config / task / registry 始终位于 `<repo-root>/.agent/workflow.yaml`、`<repo-root>/.agent/tasks/`、`<repo-root>/.agent/registry/`。

## Install / Upgrade

首次安装与升级使用 `.agent/adoption/workflow-adoption`：

```bash
# 构建可校验的 Source Distribution（源仓库必须干净）
.agent/adoption/workflow-adoption dist --source . --output dist/

# 首次安装（默认 Dry-run；确认计划后加 --execute）
.agent/adoption/workflow-adoption install \
  --target /path/to/host \
  --package dist/agent-workflow-src-<commit12>.tar.gz \
  --distribution-sha256 <sha256> \
  --source-commit <full-40-hex> \
  --integration-branch <owner-confirmed-branch> \
  --owner <owner> --owner-authorized --execute

# B/L/N 升级（默认 Dry-run）
.agent/adoption/workflow-adoption upgrade \
  --target /path/to/host \
  --package /path/to/new-source.tar.gz \
  --distribution-sha256 <sha256> \
  --source-commit <new-full-40-hex> \
  --owner <owner> --owner-authorized --execute

# 校验 / 恢复
.agent/adoption/workflow-adoption verify --target /path/to/host
.agent/adoption/workflow-adoption restore --target /path/to/host --backup <txn> --execute
```

关键边界：

- 发布包中的根 `README.md`、`.cnb.yml`、`docs/design/agent-workflow.md` 是 `package_only`，安装时不落宿主；
- 根 `AGENTS.md` 永不创建、覆盖或删除；`.agent/workflow.yaml`、`.agent/tasks/**`、`.agent/registry/**` 是宿主保护对象；
- 默认 `resources: {}`，不自动创建 Registry；只有显式 `--resource KIND=PATH` 才创建空表；
- 升级必须读取上次安装的精确 B 快照；快照缺失为 `BASE_UNKNOWN`，不得用远端 HEAD 代替；
- 冲突、B 缺失、删除未授权时 fail closed；失败自动回滚并保留报告与备份；
- 当前 `.agent/VERSION` 为 `3.4.0`；源码版本标记不等于 Release 已发布。`v3.4.0` Tag、Release、校验和与四平台 binary 由仓库根 `scripts/release/` 的发布流程产生，完成与否以 CNB Release 与附件核验为准。

详细规范见 `specs/WorkflowAdoption.md`，工具与平台边界见 `adoption/README.md`，发布契约见根 `PACKAGE_MANIFEST.md`。

## Normal Flow

TaskBuilder → Analyst → Coder → Cleaner → OwnerGate → Deliverer → Owner Integration

## Important

- historical task artifacts are evidence, not current Workflow specification
- normal Agent rules come from roles/specs/design
- PROJECT_ADAPTATION.md is temporary and removed after successful Adoption
