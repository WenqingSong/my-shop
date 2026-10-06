# Workflow V2 项目 Adoption / 跨项目迁移规范

本文定义 Workflow V2 首次被复制 / 迁移到一个新项目时的一次性 **Project Adoption Procedure**。它是迁移协议，不是 Workflow V3，也不是新增的第七个长期 Agent Role。

正常六角色运行（`TaskBuilder → Analyst → Coder → Cleaner → OwnerGate → Deliverer → Owner Integration`）不依赖本文，也不依赖临时文件 `PROJECT_ADAPTATION.md`。本文只在迁移 / Adoption 时被读取。

## 1. 定位

- **Workflow Core**（Roles、Authority、Evidence Snapshot、Handoff、Gate、Completion Contract、Git safety model、Registry coordination concept）跨项目稳定，可迁移。
- **Project Adoption** 负责把 Workflow Core 绑定到某个具体项目的差异（框架、目录、build/test、migration、design mapping、shared resource applicability、项目工程约束）。
- **Task Contract** 负责当前具体功能（AC、selected design、resource requirements、task-specific risk）。

三者的关系：

```text
Workflow Core
+
Project Materialized Policy
+
Current Task Contract
=
Agent 当前执行环境
```

正常运行阶段的 Agent 不依赖临时 `PROJECT_ADAPTATION.md`，因为 Adoption 完成后它已被删除。

## 2. 核心原则：不创造第二个长期事实源

`PROJECT_ADAPTATION.md` 是**临时工作单**：

- 不是长期项目配置；
- 不是新的事实源；
- 不是正常 Task Artifact；
- 不是第七个 Agent 的状态文件。

它只存在于 Workflow V2 首次迁移 / Adoption 到一个项目的过程中。

Adoption 中确认出来的信息，最终必须写入它们真正所属的长期权威位置：

| 确认信息 | 长期权威位置 |
| --- | --- |
| 项目架构 | 项目已有 architecture / design 文档 |
| Build / Test 命令 | README / Makefile / 项目工程规范等真实权威位置 |
| Migration policy | 项目数据库设计 / 工程规范 + Registry |
| Shared Resource policy | 对应 Registry / 项目设计 |
| Branch / Workflow authority | Workflow / 项目工程规范 |
| Validator 必要配置 | Validator 的正式配置或代码位置 |

不能形成「README 说 A、`PROJECT_ADAPTATION.md` 说 B、实际代码是 C」这样的长期三套事实源。Adoption 完成后，`PROJECT_ADAPTATION.md` 必须可以安全删除。

## 3. PROJECT_ADAPTATION.md 生命周期

```text
PROJECT_ADAPTATION.template.md
        ↓
创建临时 PROJECT_ADAPTATION.md
        ↓
DISCOVER
        ↓
RESOLVE
        ↓
MATERIALIZE
        ↓
VERIFY
        ↓
CLEANUP
        ↓
删除 PROJECT_ADAPTATION.md
        ↓
ADOPTION READY
```

只有 Adoption 真正完成后才能删除。如果仍有：未确认 Owner 决策、未落地的项目规则、未初始化的 Registry、Validator 无法工作、项目规则冲突、BLOCKER，则 `PROJECT_ADAPTATION.md` 不得删除。

## 4. 五阶段

### 4.1 DISCOVER

Agent 自动扫描项目，不要求 Owner 先填表。Agent 能从事实可靠推断的，直接形成 Proposed Adaptation；无法可靠推断、或属于 Owner Authority 的，才进入 §4.2 的 Owner Decision。

至少检查：

**Repository**

- project name
- repository model
- monorepo / single service
- language(s)
- Go module
- primary branches
- current Git state
- existing branch conventions

**Architecture**

- framework
- service layout
- application layers
- database
- cache
- MQ / stream
- deployment model
- docs/design structure

**Build / Validation**

- build commands
- unit test commands
- integration test commands
- race test
- lint / vet
- migration tests
- Makefile / scripts
- CI pipeline

**Database Migration**

- 是否启用 migration
- migration tool
- migration path
- migration version format
- 是否存在全局版本空间
- 是否已经存在 migration collision control

**Shared Resources**

检查当前项目是否存在需要跨任务协调的：migration version、error code domain，以及其他项目已经明确存在的共享资源。

注意：不要凭空把 port、NATS subject、Kafka topic、metric namespace 全部变成 Registry。只有当前项目确实存在并需要全局分配冲突控制时，才提出。

**Workflow Existing State**

检查 `.agent`、registry、templates、workflow-check、design docs，哪些已经存在，哪些需要初始化。

### 4.2 RESOLVE

DISCOVER 后形成：

- Detected Facts
- Proposed Adaptation
- Unresolved Decisions

Agent 应尽量减少 Owner 提问。只有「无法可靠推断」或「属于 Owner Authority」才问 Owner。例如：

- main 和 develop 都存在，到底谁是 Shared Integration Branch；
- 多服务是共享 migration namespace 还是独立；
- error code 是否需要全局 Registry；
- 某历史习惯是不是正式项目政策；
- 哪个 Design 文件应成为某领域 authority。

每个 Owner Decision 必须说明：

- 检测到什么；
- 为什么不能自己决定；
- 有哪些合理方案；
- 推荐方案；
- 各方案影响。

状态语义：`WAITING_FOR_OWNER_DECISION`。Owner 回答后，继续当前 Adoption Session，**不是 Handoff**。

### 4.3 MATERIALIZE

Owner 确认后，把最终结果写入真正的项目权威位置。禁止「全部留在 `PROJECT_ADAPTATION.md`」。

每个 Adaptation Item 必须有：

- Source
- Decision
- Authoritative Destination
- Materialized Status

示例：

```text
Shared Integration Branch
→ develop
→ docs/design/agent-workflow.md / project workflow policy
→ MATERIALIZED

Migration Tool
→ golang-migrate
→ database/project engineering design
→ MATERIALIZED

Migration Registry
→ enabled
→ .agent/registry/migrations.md
→ INITIALIZED

Build Command
→ go build ./...
→ existing engineering docs / Makefile
→ VERIFIED
```

具体落点必须结合项目已有结构判断，优先复用现有权威文件，不为 Adoption 新建大量重复 Project Policy 文件。

Adoption 在 Owner Decision 确认后，必须生成 / 更新长期机器配置 `.agent/workflow.yaml`，至少 materialize：

- shared integration branch（`git.integration_branch`）；
- 项目实际声明的 Shared Resource Kinds 的 Registry 路径（`resources.<kind>.registry`，仅项目确实启用对应资源时声明；kind 名必须是合法 `lower_snake_case`，如 `migration_version`、`im_migration_version`）。

`.agent/workflow.yaml` 是长期机器配置，**不是** temporary；`PROJECT_ADAPTATION.md` 仍是 temporary，完成后删除。

### 4.4 VERIFY

必须验证迁移后的 Workflow 真正可以工作。

**Workflow structure**

- 六角色 Prompt 存在
- Workflow Design 存在
- task/template 结构完整
- Registry 状态合法

**Git model**

- Shared Integration Branch 明确
- Owner branch lifecycle authority 明确
- Analyst Registry-only exception 能够映射到当前项目

**Workflow Engine（`.agent/engine/`，独立 Module）**

P3 起 Workflow Engine 是 source-vendored 独立 Go Module（module `workflow-v2-engine`），不依赖宿主业务 Go Module。VERIFY 至少检查：

- `.agent/engine/go.mod` 存在，`module workflow-v2-engine`；
- Engine 能独立 build：`go -C .agent/engine build ./...`；
- 本地 binary 能生成：`go -C .agent/engine build -o ../bin/workflow-check ./cmd/workflow-check`；
- build 后 Git working tree 不因 binary 变 dirty（`.agent/bin/` 被 ignore）；
- `.agent/bin/workflow-check` 能读取 materialized `.agent/workflow.yaml` 并执行基础 Gate；
- Engine 不依赖宿主业务 Go Module（无业务 module import、无 `go.work`、无 root `replace`）。

- 如果当前 Validator 存在项目耦合：必须明确记录（见 §6）。
- 如果需要小型项目适配才能运行：在 Owner 已确认的 Adaptation 范围内 materialize。
- 如果发现需要重新设计 Validator Core：STOP，报告，不要在 Adoption 中偷偷重构 Workflow Engine。

**Machine Configuration**

验证 `.agent/workflow.yaml`：

- 可解析、schema_version 受支持；
- integration branch 可用（`origin/<integration_branch>` 存在）；
- 声明的 Registry 可在 integration branch 上读取；
- `workflow-check` 能基于该配置工作。

**Project validation**

使用项目实际定义的验证方式，而不是硬编码当前 my-shop 的 `go test ./...`。如果项目实际是 `make test`、`go test ./services/...`、`helm lint` 等，则使用真实项目规则。

### 4.5 CLEANUP

只有以下全部满足才能 Cleanup：

- 所有 Required Adaptation 已确定；
- Owner Decisions 已解决；
- 所有长期信息已 Materialize；
- Registry 已初始化 / 确认；
- Validator 可运行或已明确 NOT_APPLICABLE / 有正式处理；
- Verification PASS；
- 无 BLOCKER；
- `PROJECT_ADAPTATION.md` 中没有只存在于临时文件而未落地的重要事实。

然后删除临时 `PROJECT_ADAPTATION.md`，保留 `PROJECT_ADAPTATION.template.md` 与本文档。

```text
ADOPTION RESULT: READY
PROJECT_ADAPTATION.md: REMOVED
```

失败则：

```text
ADOPTION RESULT: NOT_READY
PROJECT_ADAPTATION.md: RETAINED
```

## 5. Agent 调查 vs Owner 决策分工

不要做成「Owner 先填写几十项表格」。正确模式：

1. Agent 先主动扫描项目，把能从事实可靠推断的直接形成 Proposed Adaptation（Language、Go module、framework、repository layout、database、migration tool / directory、Makefile、CI commands、existing tests、current branches、docs/design structure、Registry existing state 等）。
2. Agent 无法可靠推断、或属于 Owner Authority 的，才进入 `WAITING_FOR_OWNER_DECISION`。
3. Owner 在当前 Adoption Session 中回答，原 Session 继续，这不是 Handoff。

## 6. Migration / Registry 关键抽象

当前 my-shop 使用 `golang-migrate` + 全局 migration version Registry + error code domain Registry，但**这些不是所有项目都必须有**。不要把 Workflow V2 Core 写死成「所有项目都必须有 migrations.md 和 error-codes.md」。

Adoption 必须逐项判断：

### Migration

`ENABLED / DISABLED / NOT_APPLICABLE`。如果 ENABLED，确定：

- tool
- path
- identity / version strategy
- namespace
- Registry 是否需要

多服务 Monorepo 必须允许 Owner 决定：

- A. 整个 repo 共用 migration version namespace；
- B. 每个 service 独立 namespace。

不能由 Agent 擅自决定。

### Error Code Domain

`ENABLED / DISABLED / NOT_APPLICABLE`。有些项目根本没有全局 numeric error domain，此时 `NOT_APPLICABLE`，不要为了 Workflow 一致性强行创建。

## 7. 当前 Validator 已知项目耦合（源项目）

本节记录**当前 my-shop 源项目**里 `.agent/engine/`（`workflow-check` / `internal/workflow`）的已知耦合。前三项已由 P1 / P2 / P3 解决；其余保留，本轮不重构。

| 位置 | 当前耦合 | 状态 |
| --- | --- | --- |
| integration branch | 原硬编码 `origin/develop` | 已解决（P1）：由 `.agent/workflow.yaml` 的 `git.integration_branch` 声明 |
| Registry 路径 | 原硬编码 `.agent/registry/migrations.md` / `error-codes.md` | 已解决（P1）：由 `.agent/workflow.yaml` 的 `resources.*.registry` 声明 |
| `.agent/engine/internal/workflow/state.go` `Resources` | 硬编码 `Migrations` / `ErrorCodeDomains` 两个字段 | 已解决（P2）：泛化为 `Resources.Reservations map[string][]string`，schema v2 legacy 由兼容层 normalize |
| `.agent/engine/internal/workflow/paths.go` | 硬编码 `.agent/tasks/<task>/` 根路径 | 保留（Core convention，本轮不配置化） |
| `scripts/check-registry.sh` | 硬编码 `internal/codes/codes.go`、`internal/migrations/sql`、错误码 `CodeOK == 0`、14 位数字 migration version | 保留（Project Policy，非 Engine runtime） |
| Go module path | 原 `cnb.cool/go-cloud-devops/my-shop`（import path） | 已解决（P3）：Engine 独立 module `workflow-v2-engine`，不再依赖 my-shop module |

上述未解决项**不在本轮重构**，留待后续正式打包设计时单独决定。

## 8. 禁止事项

- 不制作 tar.gz、不打包 Kit、不创建 dist；
- 不执行当前 my-shop 的 live Adoption、不创建 live `PROJECT_ADAPTATION.md`；
- 不新增第七 Agent；
- 不修改正常 Workflow state schema（不新增 Adoption 状态到 state.yaml）；
- 不修改 INV-1 ~ INV-7、五个 Gate、Evidence Snapshot、Neutral Tail、Registry Authority；
- 不修改业务代码、历史 Task Artifact、当前 live Registry。

## 9. 模板

`PROJECT_ADAPTATION.template.md` 见 `.agent/templates/PROJECT_ADAPTATION.template.md`。模板同时承担：Agent 工作记录、Owner Decision Checkpoint、Materialization checklist、Verification checklist，不是「Owner 手填完的表」。
