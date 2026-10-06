# Agent Workflow V3 — 核心架构设计

> **Workflow V3 Baseline**（`.agent/VERSION` = `3.0.0`）
>
> 当前 Workflow 协作 / 工程架构 major version 为 **V3**。Baseline 包含：six-role independent-session workflow、Evidence Snapshot、machine gates、Generic Shared Resources、strict YAML、independent Workflow Engine module、Adoption mechanism。
>
> 已知验证边界：Cross-project Adoption Dogfood **NOT PERFORMED**（DEFERRED BY OWNER）。portability architecture 已静态与内部验证，但尚未在第二个真实项目验证跨项目 Adoption。
>
> 命名约定：正文中「Workflow V2」若指当前架构名称，已被 V3 取代；若指 V1→V2 历史演进、legacy reference 或 state schema v2/v3，则保留历史含义，不随本次 Release Marking 改写。`module workflow-v2-engine` 为 historical technical identifier，本轮不改。

本文是 Workflow V3 的**唯一核心架构 Design 文档**：解释「为什么这样设计」「整个系统如何运行」「各概念之间的关系」「为什么需要这些 Gate / Git / Evidence 规则」。

本文定位是「**为什么**」。`.agent/specs/*`（协作规范、职责边界）与 `.agent/roles/*`（六个角色 Prompt）负责「**具体怎么做**」，引用本文，不重复定义。

事实源优先级：

1. 当前真实代码 / Validator / Tests（`.agent/engine/internal/workflow/*`、`.agent/engine/cmd/workflow-check/*`）；
2. 当前现行 Agent Prompt / 协作规范（`.agent/specs/*` 与 `.agent/roles/*`）；
3. 本文（`docs/design/agent-workflow.md`）；
4. 历史 Task Artifact（`.agent/tasks/*`，仅作历史证据，不作为现行语义来源）。

---

## 1. 设计目标

Workflow V2 不是抽象的设计稿，而是针对真实执行中暴露的缺陷演进出来的结果。它要解决的是一组具体问题：

| # | 真实问题 | V2 的应对 |
|---|---|---|
| 1 | 多 Agent 长上下文污染：一个 Session 越滚越长，后续角色被前面角色的推理历史干扰 | 每个角色独立 Agent Session，新 Session 只读取持久事实 |
| 2 | 自审偏差：同一个 Agent 自己写代码再自己 Review | Coder 与 Cleaner 分离为两个独立 Session |
| 3 | Owner 被迫记住「下一个角色是谁、流程走到哪」 | 状态机 + Gate 机械推导，Owner 不再承担流程记忆 |
| 4 | Owner 被迫承担机械 Git / Registry 操作 | Analyst Registry-only authority 接管 Registry 落盘，Owner 只做决策 |
| 5 | Conversation 状态和 Git 实际状态不一致 | 以 Git + Repository Artifacts 为跨 Session 唯一事实源 |
| 6 | Dirty Worktree 导致角色之间事实不稳定 | Handoff 前强制 clean exit（`handoffReady`） |
| 7 | Review Target 漂移 | Evidence Snapshot 绑定 immutable commit |
| 8 | Shared Resource collision（resource kind 撞车，如 migration version / 错误码域） | 共享 develop Registry + 只读 Validator 机械授权（Generic Reservation） |
| 9 | Agent 越权修改 shared develop | INV-1 权限边界 + 唯一 Registry-only 例外 |
| 10 | 「业务结果完成」与「Role Session 完成」混淆 | 通用 Completion Contract（业务结果 ≠ Session 完成） |

### 核心原则

- **Conversation 是临时上下文**。聊天历史不承担跨 Session 事实权威。
- **Git + Repository Artifacts 是跨 Session 的持久事实源**。`state.yaml` 是唯一机器权威源，Evidence Artifact（`contract.md` / `findings.md` / `core-logic.md` / `owner-decision.md` / `delivery.md`）是「为什么」。
- **Handoff 是导航信息，不是另一个事实数据库**。它只携带最小必要导航与事实，下一 Session 仍需重新读取 Git 与 Artifact。

---

## 2. 六角色模型

```text
TaskBuilder → Analyst → Coder → Cleaner → OwnerGate → Deliverer → Owner Integration
```

Owner 不是第七个 Agent，而是整个 Workflow 的 **Decision Authority + Shared Repository Authority**（人类控制面）。

### 为什么每个角色使用独立 Agent Session

- **降低上下文污染**：每个 Session 只看到本角色职责所需的事实，不带入前一角色的推理链。
- **降低自我确认偏差**：Coder 的实现与 Cleaner 的审查分属不同上下文，审查才能真正独立。
- **强化职责边界**：Session 边界即职责边界，角色完成自己阶段后停止，不顺手接管下一角色。
- **角色之间只依赖持久事实**：新 Session 必须重新读取 Git、Task artifacts、Contract、Design、Registry，禁止依赖「上一个 Agent 聊天里已经说过了」。

### 各角色职责

| 角色 | 回答的问题 | 输入 | 主要职责 | 负责的 Artifact | Authority | 明确禁止 | 完成条件 | 下一角色 |
|---|---|---|---|---|---|---|---|---|
| **TaskBuilder** | 交付什么、怎样判断完成 | Owner 需求 | 把需求整理成可执行、可验收的 `task.md` | `task.md`、`state.yaml` 初始结构 | 定义任务边界与路由 | 写生产代码；创建/删除/重命名 branch；替 Owner/Analyst 作关键决定 | `task.md` + 初始 `state.yaml` + 明确路由 | Analyst（COMPLEX）或 Coder（NORMAL） |
| **Analyst** | 有哪些约束、可行方案、必须保持什么性质 | `task.md` | 调查约束、出方案、写 Contract、派生资源、沉淀长期 Design | `contract.md`、`docs/design/*`、`state.contract`、`state.resources` | **唯一** Registry-only develop mutation authority | 改生产代码/测试；批准自己的推荐；改 shared develop 其他内容 | Contract APPROVED + Registry Reservation 落地 + 两段式提交 | Coder |
| **Coder** | 怎样在既定范围内完成实现 | `task.md` + APPROVED `contract.md` | 实现 AC、写测试、发起 Review Request | 业务代码、migration、tests、`state.review`（PENDING） | 在 feature 内实现 | 自行改 Contract/Task；写 `CLEAN`/`owner`/`delivery`；自行推断资源编号 | 实现 + 两段式提交（C1 + C2） | Cleaner |
| **Cleaner** | 完整变更是否满足任务、测试是否可信 | `task.md` + Contract + C1 | 独立审查、写 Finding、整理核心逻辑、给出 CLEAN | `findings.md`、`core-logic.md`、`state.review`（CLEAN） | 审查结论 | 默认不改生产代码/测试；写 `owner` 状态；替 Owner 最终接受 | CLEAN + artifacts commit/push + `owner-gate-start` PASS | OwnerGate |
| **OwnerGate** | 核心机制是什么、请求 Owner 决策并持久化 | CLEAN review | 提炼 1~3 个核心机制、解释给 Owner、持久化 Owner 决定 | `owner-decision.md`、`state.owner` | File Writer（**不是** Decision Authority） | 改代码/Contract/Review；自行生成 `ACCEPTED` | Owner 决定持久化 + `delivery-start` PASS | Deliverer |
| **Deliverer** | 能否在目标环境真实运行 | CLEAN + Owner ACCEPTED | 里程碑运行验证，记录三个不可变 SHA | `delivery.md`、`state.delivery` | 验证 | merge/push develop；修生产代码；替 Owner 接受 | delivery PASS + `merge-ready` PASS | Owner（集成决策） |

Owner 保留四类真正需要人判断的职责：Task / Branch Boundary、Contract Decision、Core Logic Decision、Shared Integration Decision。

---

## 3. Owner 与 Agent 的权限边界

这是 Workflow V2 的核心之一。权限边界用「**谁控制什么**」而非「谁更聪明」来划分。

### Owner 控制区

1. **Task / Feature Branch Lifecycle**：创建 feature branch、删除 branch、重命名 branch、branch boundary 决策。
2. **Shared Integration**：feature → develop、merge 时机、merge 顺序、merge / squash 策略、最终 integration 决策。

### Agent Autonomous Zone

默认在 Owner 已准备好的 feature branch 内工作。Agent 可以在自己的 Authority 内：

- 阅读代码；
- 修改代码；
- 测试；
- 显式 stage；
- commit；
- push 当前 feature。

但**不能**因为「工作需要」就自行改变 Shared Integration Boundary。共享主线与分支生命周期始终是 Owner Authority。

---

## 4. Analyst Registry-only Develop Authority

这是 `product-view-count-v1` dogfood 发现的第一个真实缺陷，必须作为正式设计写清楚。

### 原问题

Analyst 负责读 Registry、判断资源、计算 migration / error-code domain、形成 Resource Proposal，但最后却必须让 Owner 手工修改 Registry。这导致 Owner 再次沦为机械操作员，违背了「Owner 只做决策」的目标。

### 最终规则

Analyst 是唯一被允许进行 shared-develop mutation 的 Agent 例外。但这不是普通 develop 权限，而是**只允许**修改两个白名单文件：

```text
.agent/registry/migrations.md
.agent/registry/error-codes.md
```

> 这两个文件路径由机器配置 `.agent/workflow.yaml` 声明（见 §29 Machine Configuration）；本文中的两个路径是当前 my-shop 的默认值。

形成 **Registry-only commit**。流程：

```text
git fetch
→ 读取最新 origin/develop Registry
→ 重新计算 next resource
→ 修改 Registry
→ 显式 stage
→ 检查 staged / commit diff
→ commit
→ 正常 push develop
```

### 强约束

- push 前必须确认 diff **只含** Registry 白名单文件，出现任何其他路径 → **STOP**。
- push 因 non-fast-forward 被拒 → **禁止** force / force-with-lease / rewrite shared history；必须 fetch → 重新读取 Registry → 重新计算资源 → 重新形成 Reservation → 再正常 push。

### 语义定性

这是「**共享资源竞争协议**」，不是「Analyst 拥有 develop 开发权限」。TaskBuilder / Coder / Cleaner / OwnerGate / Deliverer 仍没有普通 develop push authority。

---

## 5. Artifact Ownership

一个角色只拥有自己负责的 Artifact。**一个角色不能顺手把另一个角色的未提交 Artifact 一起提交**。

| 所有者 | 拥有的 Artifact |
|---|---|
| TaskBuilder | `task.md`、task initialization / task metadata |
| Analyst | `contract.md`、业务设计文档（`docs/design/*`）、resource proposal / contract 状态 |
| Coder | 业务代码、migration、tests、实现相关文件 |
| Cleaner | `findings.md`、`core-logic.md`、review 状态 |
| OwnerGate | `owner-decision.md`、owner acceptance/rejection 状态 |
| Deliverer | `delivery.md`、delivery 状态 |
| Owner | branch lifecycle、final integration |
| shared Registry | Analyst Registry-only exception |

如果发现其他角色 owned artifact 是 dirty，**不要** `git add -A` / `git add .`，而是 STOP / 报告 / 让正确角色处理。

---

## 6. Workflow V2 State Model

### 为什么删除 V1 的 global phase

V1 使用 13 值 `phase` 状态机 + `STALE` 持久状态 + Transition Authority。这套设计的问题在于：

- `phase` 是「谁能行动」的持久事实，但它可以被一次文件编辑伪造，不构成可信的历史。
- `STALE` 是一个持久状态，但「审查是否还有效」本质上是**由 Git 事实动态推导**的结果，持久化它只会引入「状态与事实不同步」的新错误。
- 阶段推进的 Authority 表在 Prompt 层难以强制，V1 Validator 无法证明历史 transition 是否合法。

V2 改为：**不保存 `phase`、`STALE`、`DONE`**。生命周期由 Owner 与 Gate 控制；状态有效性由 immutable Evidence Snapshot + Neutral Tail 机械推导。

`state.yaml` 只保存 Git 无法推导的**工作流决策事实**。明确不保存：`phase`、`STALE`、`DONE`、`branch`、`HEAD`、`working_tree`、`changed_files`、`NEXT_ROLE`、`NEXT_ACTION`。

### 当前真实 `state.yaml` 数据模型

```yaml
schema_version: 3
task_id: <task-slug>

contract:
  status: PENDING | APPROVED | REJECTED | NOT_REQUIRED
  target: <evidence-commit-sha | "">

resources:
  reservations:
    <resource-kind>:
      - <opaque-value>

review:
  status: NOT_REQUESTED | PENDING | CLEAN | CHANGES_REQUIRED
  target: <evidence-commit-sha | "">

owner:
  status: PENDING | ACCEPTED | REJECTED | NOT_REQUIRED
  review_target: <review-target-sha | "">

delivery:
  status: NOT_RUN | PASS | FAIL | BLOCKED
  review_target: <sha | "">
  feature_head: <sha | "">
  develop_base: <sha | "">

blocked:
  active: false
  by: ""
  reason: ""
```

要点：

- `resources.reservations` 只声明「需要什么」，不自证满足；真正 Authority 永远读取 shared integration branch 上的 Registry（当前 my-shop 为 `origin/develop:.agent/registry/*`，由 `.agent/workflow.yaml` 声明，见 §29）。
- `resources.reservations.<resource-kind>` 是 **Generic Shared Resource Reservation**：Workflow Core 只认识 resource kind → opaque value 列表，不预置任何具体资源类型（migration / error code 等只是当前 my-shop Project Config 声明的 kind）。
- 每个 reservation value 对 Workflow Core 都是 opaque non-empty string，具体格式正确性属于 Project Policy。
- `schema_version: 3` 是新任务当前 schema；`schema_version: 2` 仅为历史任务做 Legacy Read Compatibility（读取后 runtime normalize，不写回磁盘）。其它 schema version 调用 Gate → `ERROR`（exit 2）。

---

## 7. Evidence Snapshot

这是 Workflow V2 最核心的设计之一。

### 为什么「当前 HEAD」不能直接等价于「被批准 / 被审查的对象」

工作区与分支 HEAD 是**移动的**：Coder 提交 C1 后又提交 metadata，Cleaner 开始时 HEAD 可能又前进了。如果「审查对象」=「Cleaner 开始时的 HEAD」，那么 Owner / Cleaner / Deliverer 讨论的就不是同一个不可变对象，任何后续 commit 都会让结论失去锚点。

### Evidence Snapshot 的概念

一个审批 / Review 必须绑定一个**不可变 Git commit**。例如：

```text
Analyst：
  A1 = Contract Evidence Commit
  A2 = Metadata / State Commit
  contract.target = A1

Coder：
  C1 = Implementation Evidence Commit
  C2 = Metadata / State Commit
  review.target = C1
```

Cleaner 审查的是 **C1**，而不是「Cleaner 开始工作时当前 HEAD」。这样 Owner / Cleaner / Deliverer 讨论的是同一个不可变实现。

三种 Evidence Binding：

| Binding | 绑定对象 | 语义 |
|---|---|---|
| Contract（INV-2） | `contract.target` = A1 | Owner 最终批准的 `contract.md` Evidence Commit |
| Review（INV-3 / INV-4） | `review.target` = C1 | Cleaner 真正审查的 immutable implementation Evidence Commit |
| Delivery（INV-6） | `delivery.review_target` / `feature_head` / `develop_base` | 被验证的业务实现 / 实际参与集成的 feature snapshot / 验证时的 develop 基线 |

---

## 8. Evidence Snapshot 为什么需要两阶段 Commit

一个实际的机制约束：**Commit 本身不能在创建之前知道自己的 SHA**。因此不能试图让 Evidence Commit 自己写入「我的 SHA 是 xxx」。

解决办法是**两阶段提交**：

```text
Evidence Commit（内容）
      ↓
Metadata Commit（只写 state.yaml，引用 Evidence Commit 的 SHA）
```

- Analyst：A1（contract 内容）→ A2（只写 `state.contract.status=APPROVED`、`state.contract.target=A1`）。
- Coder：C1（业务代码 + 测试）→ C2（只写 `state.review.status=PENDING`、`state.review.target=C1`）。

Metadata Commit 是「事后引用」Evidence Commit 的 SHA，因此 SHA 可以精确落盘。Cleaner 审 C1，不是 C2。

---

## 9. Neutral Tail

Evidence Snapshot 之后可以存在**有限**的 metadata commit，但必须严格限制。

核心规则：**task-scoped、exact path matching、default deny**。白名单必须精确限定在 `.agent/tasks/<current-task>/` 下，禁止 basename-only 宽松匹配。

### Review-neutral tail（`review.target` 之后允许）

```text
.agent/tasks/<task>/state.yaml
.agent/tasks/<task>/findings.md
.agent/tasks/<task>/core-logic.md
.agent/tasks/<task>/owner-decision.md
.agent/tasks/<task>/delivery.md
```

### Delivery-neutral tail（`feature_head` 之后允许，更窄）

```text
.agent/tasks/<task>/state.yaml
.agent/tasks/<task>/delivery.md
```

### 判定逻辑（以真实代码为准）

- `isReviewSubstantial(task, path)` = `!isReviewNeutral(task, path)`，即 default-deny：路径不在「本 task 目录 + 白名单文件名」内，一律判 substantive。
- 其他目录、其他 task 的同名文件（如 `.agent/tasks/other/state.yaml`、`docs/design/state.yaml`、`.agent/registry/state.yaml`）一律 substantive。
- `.go`、`.sql`、业务测试修改、业务设计修改都属于 substantive change。一旦出现，旧 `review.target` 的 CLEAN 就不再有效（INV-4，机械失效，不保存 STALE）。

---

## 10. 核心不变量（INV-1 ~ INV-7）

- **INV-1 Shared Branch Authority**：Only Owner mutates shared develop（branch 生命周期与最终 feature→develop integration）。Agent 不创建任务 branch、不最终 merge、不 push develop——**唯一例外**是 Analyst 的 Registry-only develop commit（仅 `.agent/workflow.yaml` 声明的 Registry 文件，当前 my-shop 为 `.agent/registry/migrations.md` 与 `.agent/registry/error-codes.md`）。
- **INV-2 Implement Authorization**：Coder 开始前 Contract valid + 每个 Shared Resource Reservation 由 `origin/<integration-branch>` 上对应的 Registry 授权（value 存在、owner 与当前 Task 一致、status 为 RESERVED/ACTIVE）。
- **INV-3 Immutable Review**：Cleaner 只能 CLEAN 明确 immutable commit，不能 CLEAN working tree。
- **INV-4 Review Freshness**：任何 substantive change 使旧 CLEAN 自动失效；不保存 STALE。
- **INV-5 Owner Binding**：Owner ACCEPTED 必须绑定当前 CLEAN review target（`owner.review_target == review.target`）。
- **INV-6 Delivery Binding**：Delivery PASS 必须绑定 current review target + exact develop base；develop 前进后旧 PASS 不再 Merge Ready。
- **INV-7 Evidence Snapshot Integrity**：Contract Approval、Cleaner Review、Delivery Verification 都必须绑定明确 immutable Evidence Snapshot；Evidence Snapshot 之后只允许该阶段定义的 neutral metadata tail，任何超出白名单的变化都会使对应验证结果失效。

---

## 11. workflow-check 的职责

`.agent/engine/cmd/workflow-check` 是**只读 Validator**，与业务二进制 `my-shop` 分离。Workflow Engine 是独立 Go Module（module `workflow-v2-engine`，位于 `.agent/engine/`），不依赖宿主业务 Go Module、GoFrame、MySQL / Redis 或任何业务包；只依赖 Git 仓库与 Go toolchain。

它只做：读取事实 → 判断 Gate → 输出 `PASS / FAIL / ERROR`。

它**不能**：

- 修改 YAML / 修改 Registry / reserve resource；
- 创建 branch / stage / commit / push / merge；
- 修改业务代码；
- 替 Owner 做业务决定。

### 当前真实 CLI

```text
workflow-check gate <coder-start|cleaner-start|owner-gate-start|delivery-start|merge-ready> <task> [--root DIR]
```

integration branch 与 Registry 路径来自 `.agent/workflow.yaml`（见 §29 Machine Configuration），不再由 CLI 写死；本地 develop 分支不是权威。

### 构建与运行（独立 Engine Module）

Workflow Engine 是 source-vendored nested Go Module，与业务 Module 彻底独立，不通过 `go.work` / `replace` 关联：

```text
# 构建本地 binary（进入 Git ignore，不进入 Git）
go -C .agent/engine build -o ../bin/workflow-check ./cmd/workflow-check

# 运行（正式 Gate 机器入口优先 direct binary，保留 0/1/2 exit code）
.agent/bin/workflow-check gate <gate> <task>

# 测试 / 静态检查（Engine 与 Business 分开运行）
go -C .agent/engine test ./...
go -C .agent/engine test -race ./...
go -C .agent/engine vet ./...
go -C .agent/engine build ./...
```

Repository Root 始终通过 `git rev-parse --show-toplevel` 解析，与 Engine Module Root（`.agent/engine/`）无关；从 repo 根、`.agent/`、`.agent/engine/` 任一 cwd 运行 binary 都应解析到同一个 Repository Root，并读取 `<repo-root>/.agent/workflow.yaml`。

### exit code

| code | 含义 |
|---|---|
| 0 | PASS |
| 1 | Invariant / Gate FAIL |
| 2 | Input / Runtime ERROR（含对非 V2 task 调用 gate） |

Validator 不代理任何 git 写操作；Agent 的 git 写约束属于协议 / Prompt 层，shared develop 的机器级保护交给仓库 branch protection / 外部 Git 控制能力。

---

## 12. 五个 Gate

Gate 是 Handoff 前的机械出站检查。每个 Gate 回答一个「现在能否交给下一角色」的问题。

| Gate | 为什么存在 | 关键前置 |
|---|---|---|
| `coder-start` | 保证 Coder 开始前 Contract / Resource 已合法 | `blocked.active=false` + `handoffReady` + Contract 有效 + Resources 授权 |
| `cleaner-start` | 保证 Cleaner 审的是稳定 Implementation Evidence | `review.status=PENDING` + target 真实且在 feature 历史 + `handoffReady` + review tail 仅 neutral |
| `owner-gate-start` | 保证 Cleaner 已完成 CLEAN 持久化和 Git 收尾 | `blocked.active=false` + `handoffReady` + `review.status=CLEAN` + cleanValidity PASS |
| `delivery-start` | 保证 Owner 已绑定当前 CLEAN review | Contract/Resources 有效 + cleanValidity + `owner.status=ACCEPTED` + `owner.review_target==review.target` + `handoffReady` |
| `merge-ready` | 只表示 feature 已满足交给 Owner 集成的机械条件，**不代表 Agent 可以自行 merge** | 上述全部 + `delivery.status=PASS` + delivery 三个 SHA 绑定 + develop/feature 新鲜度 + `handoffReady` |

---

## 13. handoffReady

正常角色 Handoff 需要满足的 Clean Exit 前提（每个出口 Gate 的共同前提）：

- 当前不是 detached HEAD；
- working tree clean；
- remote feature（`origin/<current-feature>`）存在；
- local HEAD == remote feature HEAD。

**核心原因**：如果 Artifact / Code 只存在于本地 dirty working tree，下一 Session 接手时事实是不稳定的。「Handoff Ready」本质上是「跨 Session 事实已经稳定落到 Git」。

---

## 14. CLEAN != Cleaner Session Complete

这是 `product-view-count-v1` dogfood 的第二个真实缺陷，必须单独说明。

### 原问题

Cleaner 审查完成 → `review.status = CLEAN` → 写 `findings.md` → 写 `core-logic.md` → 更新 `state.yaml` → 直接宣布 OwnerGate。但这些 Cleaner Artifact 尚未 commit / push，导致 `owner-gate-start` 正确失败（working tree dirty）。

### 正式设计原则

**CLEAN 只代表「审查结果为 CLEAN」，不等于 Cleaner Session 完成。**

Cleaner 得出 CLEAN 后仍必须完成：

1. 持久化 `findings.md`；
2. 持久化 `core-logic.md`；
3. 持久化 review state（`review.status=CLEAN`、`review.target=C1` 不变）；
4. `git status --short`；
5. 只 stage Cleaner-owned files；
6. `git diff --cached`；
7. commit；
8. push current feature；
9. 确认 worktree clean；
10. 确认 local == remote；
11. 运行 `owner-gate-start`；
12. PASS 后正式 HANDOFF → OwnerGate。

若 `owner-gate-start` FAIL：不得宣布 HANDOFF，Cleaner Session 不结束；失败原因在 Cleaner authority 内则继续修复，需要 Owner decision / action 则进入相应 Waiting 机制，真正无法继续才 BLOCKED。

同时说明：Cleaner artifact commit 是 **review-neutral tail**。`review.target` 必须继续指向 Coder 的 Implementation Evidence Commit C1，**不能前移成 Cleaner 自己的 commit**。

---

## 15. 通用 Completion Contract

把 Cleaner Finding 提升为通用原则：**Business Result != Role Session Complete**。

正常角色正式 Handoff 前必须：

1. 当前角色职责全部完成；
2. owned artifacts 已持久化；
3. 应 commit 的已 commit；
4. 应 push 的已 push；
5. working tree clean；
6. remote synced；
7. 下一角色入口 Gate PASS；
8. NEXT_ROLE 明确；
9. NEXT_ACTION 明确；
10. NEXT_TARGET 明确。

只有这样才允许正式 HANDOFF。

---

## 16. Handoff 的真正语义

Handoff 是「**当前角色结束，把控制权正式交给下一个角色**」。

它**不是**：

- 「我暂时需要 Owner 回答一个问题」（那是 Decision Checkpoint）；
- 「我暂时需要 Owner 做一次 branch 操作」（那是 Owner Action，非 Handoff）。

正常 Handoff：当前 Session 到此结束。下一角色：新开独立 Session。如果未来流程再次回到同一个角色，也是新的 Session。

---

## 17. Decision Checkpoint

Decision Checkpoint（`WAITING_FOR_OWNER_DECISION`）**不是 Handoff**。

例如 Analyst 发现设计决策需要 Owner 确认：进入 `WAITING_FOR_OWNER_DECISION`，但 Analyst Session 不结束，Owner 在当前 Session 回答，Analyst 继续原 Session。

同理，OwnerGate 在请求 Owner 核心逻辑决策时也使用 `WAITING_FOR_OWNER_DECISION`，Session 保持。

这不是 HANDOFF → Owner。

---

## 18. Owner Action（当前语义）

当前 Workflow V2 **不保留**一个名为 `WAITING_FOR_OWNER_ACTION` 的持久状态。

Owner-only 动作（例如 Owner 创建 / 切换 feature branch）发生在**角色 Session 之外**：Owner 在流程开始前或角色交接间隙准备 branch。如果 Agent 在 Session 中发现需要 Owner 执行某个只有 Owner 有 Authority 的动作、且动作完成后原角色仍需继续工作，这属于**同 Session 内的一次 Owner Action 请求**——Agent 暂停等待，Owner 完成后原 Session 继续，不机械理解成 Role Handoff，也不持久化一个独立状态。

只有当前角色**真的无法继续**、且不能通过简单 Decision / Owner Action 恢复时，才进入 `blocked.active=true`（BLOCKED，见 §19）。

---

## 19. BLOCKED

`blocked`（`state.yaml` 中的 `active / by / reason`）应用于：当前角色真的无法继续，且不能通过简单 Decision Checkpoint / Owner Action 恢复。

不要把所有「需要 Owner 做一下动作」都写成 BLOCKED Handoff。BLOCKED 是正交阻塞事实，不是生命周期阶段。

---

## 20. Handoff 数据内容

Handoff 只携带**最小必要导航和事实**，不复制完整推理过程、不携带大量主观评价、不替下一 Reviewer 做判断。

当前标准 Handoff 必填项（以现行 `.agent/specs/*` 与 `.agent/roles/*` 为准）：

- **Git SHA**：明确当前 feature HEAD；
- **REMOTE_SYNCED=YES**：local == `origin/<current-feature>`；
- **NEXT_ROLE**：下一角色（必填）；
- **NEXT_ACTION**：下一步动作（必填）；
- **RESULT=BLOCKED**：仅阻塞退出时使用。

其余（已完成的 Artifact、关键结论、非阻塞风险）只保留决策所需内容。Handoff 给「路线」，Git + Artifacts 给「事实」。

---

## 21. Git Authority / Safety

### 允许的普通操作

```text
fetch / status / diff / log / show / rev-parse
switch 到已存在且属于当前任务的分支
pull --ff-only 当前 feature
显式 add
commit
push 当前 feature
```

### 禁止的高风险操作

```text
force / force-with-lease
reset --hard
clean -fd
rebase 已发布历史
随意 amend 已发布 commit
删除 remote branch
git add -A / git add .
```

Analyst 的 Registry-only develop exception 单独按 §4 Registry Authority 规则处理。

---

## 22. Git 当前事实优先

如果 Agent Prompt 计划「修改 → stage → commit → push」，但 Agent 检查真实 Git 后发现目标修改**已经存在于一个已 push commit**，则应**接受真实 Git 事实**，不要为了「匹配计划步骤」重复提交。

更不能仅仅因为 commit subject 和 Prompt 建议的不完全一样，就 amend / rewrite history / force push。

**计划步骤不是事实，Git 是事实。**

---

## 23. Commit Message 规范

默认格式：

```text
<type>(<scope>): <中文摘要>
```

示例：

```text
feat(product): 增加商品浏览量计数
fix(workflow): 修复 Cleaner 过早交接问题
docs(workflow): 明确 HANDOFF 出站条件
test(product): 补充浏览量并发测试
```

- `type` / `scope` 保留英文 Conventional Commit 关键字（常用 `feat` / `fix` / `refactor` / `test` / `docs` / `chore`）。
- `summary` 默认中文。不要求 Agent summary 必须英文，不因中文 commit message 阻塞流程。

同时明确：Commit Message 是规范，不是重写历史的理由。已有已 push commit，只要内容正确，不因语言或 scope 不完全符合建议而 amend / rebase / force push。

---

## 24. Deliverer 与 Merge Ready

Deliverer **不负责最终 merge**。Deliverer 负责验证：当前 feature + 当前 review target + 当前 develop base 是否满足交付条件。

- delivery PASS 只意味着「可以交给 Owner 做 Shared Integration」。
- `merge-ready` 也只意味着「机械条件已满足」。
- 最终 feature → develop 仍然是 Owner Authority。

---

## 25. Delivery Freshness

`delivery` 绑定三个不可变 SHA：

```text
delivery.review_target  # 被验证的业务实现 C1
delivery.feature_head   # 实际参与集成验证的 feature snapshot
delivery.develop_base   # 验证时 shared develop 基线
```

为什么都需要：Deliverer PASS 后自己仍可能产生 `delivery.md`、`state.yaml`，因此不能简单要求 `current HEAD == feature_head`。真实实现（`deliveryFreshness`）改为：

1. `delivery.feature_head` 是当前 remote feature HEAD 的祖先；
2. `feature_head..remote HEAD` 只允许当前 task 的 delivery-neutral artifacts；
3. 当前 `origin/develop == delivery.develop_base`。

`origin/develop` 前进后，旧 PASS 自动失去 Merge Ready 效力。

---

## 26. Registry Authority 与 Generic Shared Resource 的关系

本轮只整理当前 Workflow 内部语义，**不设计跨项目迁移机制**。

Registry（`.agent/registry/*`，位于 `develop`）的作用是：在**同一个仓库的并行任务之间**协调**项目声明的 Shared Resource Kind** 的分配，避免资源碰撞。它属于 Workflow shared coordination state。

P2 起 Registry 的业务含义泛化：Workflow Core 不预置任何资源类型，只把每个 Registry row 抽象为 `Value / Owner / Status`（第 1 列 = Value、倒数第 3 列 = Owner、倒数第 2 列 = Status）。`migration_version` / `error_code_domain` 只是当前 my-shop 通过 `.agent/workflow.yaml` 声明的 resource kind，不是 Workflow Core builtin type。

- 状态三态：`RESERVED`（已落 develop、Feature 未合并）/ `ACTIVE`（已合并，终态）/ `RELEASED`（取消释放）。
- 复用规则由各 resource kind 的 Project Policy 决定（如 my-shop 的 migration version 一经分配永久 tombstone、错误码域仅纯 `RESERVED` 阶段可复用）；Workflow Core 不承载这些业务规则。
- 并行竞争防护：无外部锁，以 Git 提交顺序 + 冲突检测串行化——先提交到 develop 者胜。

跨项目迁移 / 复用的具体流程见 §28 Project Adoption 与 `.agent/specs/WorkflowAdoption.md`。

---

## 27. Workflow V2 Dogfood Findings

只记录真正改变 Design 的 Finding。

### Finding #1：Analyst 无 Registry mutation authority

- **问题**：Analyst 负责 Resource Reservation，但没有 Registry mutation authority，导致 Owner 变成机械 Registry 操作员。
- **修复**：授予 Analyst Registry-only develop authority（§4）。

### Finding #2：Cleaner CLEAN 后过早进入 OwnerGate

- **问题**：Cleaner 得出 CLEAN 后在 Artifacts 尚未 commit / push 时过早进入 OwnerGate，`owner-gate-start` 因 working tree dirty 正确失败。
- **修复**：CLEAN != Session Complete（§14）。Handoff 前必须完成 Artifact persistence + Git clean exit + next gate PASS。

这两个 Finding 说明为什么 V2 要进行真实 dogfood，而不能只靠静态设计。

---

## 28. Project Adoption / Cross-project Migration

Workflow V2 Core（Roles、Authority、Evidence Snapshot、Handoff、Gate、Completion Contract、Git safety model、Registry coordination concept）跨项目稳定，可以迁移。项目差异（框架、目录、build/test、migration、design mapping、shared resource applicability、项目工程约束）通过**一次性 Project Adoption** 解决。

关键结论：

- Workflow Core 可迁移；项目差异由一次性 Adoption 解决。
- `PROJECT_ADAPTATION.md` 是**临时工作单**，只存在于迁移 / Adoption 过程中。
- Adoption 完成后 `PROJECT_ADAPTATION.md` 必须删除，不成为长期事实源。
- Adoption 不新增第七个长期 Agent Role；完成后正常项目回到原六角色模型。
- 正常六角色运行不依赖 `PROJECT_ADAPTATION.md`。

Adoption 的五阶段（DISCOVER → RESOLVE → MATERIALIZE → VERIFY → CLEANUP）、`PROJECT_ADAPTATION.md` 生命周期、Migration / Registry 的 `ENABLED / DISABLED / NOT_APPLICABLE` 抽象、以及当前源项目 Validator 的项目耦合清单，详见 `.agent/specs/WorkflowAdoption.md`，不在本文重复。模板见 `.agent/templates/PROJECT_ADAPTATION.template.md`。

---

## 29. Machine Configuration（.agent/workflow.yaml）

Workflow 有三层事实：

1. **Workflow Core**：Roles、Authority、Evidence Snapshot、Handoff、Gate、Completion Contract、Git safety model、Registry coordination concept——跨项目稳定，固化在 `.agent/engine/internal/workflow/*` 与 `.agent/specs/*`、`.agent/roles/*`。
2. **Machine Configuration**：`.agent/workflow.yaml`——「跨项目会变化、且 Workflow Engine runtime 必须知道的最小参数」。
3. **Project Policy**：README / Makefile / `docs/design/*` / `AGENTS.md` 等——项目工程约束，供 Agent 读取，不供 `workflow-check` 机器解析。

`.agent/workflow.yaml` 是长期机器配置，**不是** `PROJECT_ADAPTATION.md`（一次性临时工作单）、不是 Task Artifact、不是业务配置。它在 Adoption 完成后长期存在，供 `workflow-check` 读取。

当前 v1 schema：

```yaml
schema_version: 1
git:
  integration_branch: develop
resources:
  migration_version:
    registry: .agent/registry/migrations.md
  error_code_domain:
    registry: .agent/registry/error-codes.md
```

约束：

- `schema_version` 必须为 `1`；缺失或不支持 → exit 2。
- `git.integration_branch` 必填；Engine 据此构造 `origin/<integration_branch>`。
- `resources` 是 arbitrary map：`resource kind → { registry }`。Workflow Core 不预置任何固定 kind，新增 `im_migration_version` 等 kind 无需修改 Go 代码。
- 每个 resource kind 必须：
  - 是合法 `lower_snake_case`（`^[a-z][a-z0-9_]*$`）；
  - 有非空 registry 路径，且路径规范化后严格位于 `.agent/registry/` 下（不能 absolute、不能 `..` traversal）；
  - 不同 kind 不得声明完全相同的 registry 路径（避免无 kind discriminator 的解析歧义）。
- 配置损坏（缺失 / 无法解析 / 缺 `integration_branch` / 非法 kind / 缺 registry / registry 路径非法 / registry 无法读取）一律 exit 2，不误报为「resource not reserved」。

硬编码不等于错误：`.agent/tasks/<task-id>` 仍属于 Workflow Core Convention，本轮不配置化。

### Generic Shared Resource Reservation 三层关系

P2 后，Workflow Core 只认识 Generic Shared Resource Reservation，不再认识 migration / error code 等具体业务语义。三者关系：

```text
Task State
resources.reservations.<resource-kind>
        ↓ 按 kind 动态绑定
Machine Configuration
resources.<resource-kind>.registry
        ↓ 读取 origin/<integration-branch>:<registry-path>
Shared Registry
reservation row（Value / Owner / Status）
```

`migration_version` / `error_code_domain` 只是当前 my-shop Project Config 声明的 resource kind，不是 Workflow Core builtin type。未来新增 `im_migration_version`、`nats_subject` 等 kind 只改 `.agent/workflow.yaml`，无需修改 Validator Core。

---

## 附录：ASCII Flow

```text
TaskBuilder
   ↓
Analyst（Contract Decision / Registry Reservation）
   ↓
Coder
   ↓
Cleaner
   ↓
OwnerGate（Owner Core Logic Decision）
   ↓
Deliverer
   ↓
Owner Integration
```

Evidence Snapshot 与 Neutral Tail 的时间轴：

```text
C1 Implementation Evidence Commit
   ↓
Cleaner neutral artifacts（findings.md / core-logic.md / state.yaml）
   ↓
OwnerGate（owner-decision.md / state.owner）
   ↓
Delivery neutral artifacts（delivery.md / state.yaml）
   ↓
Owner Integration
```

`review.target` 始终指向 C1，不前移。
