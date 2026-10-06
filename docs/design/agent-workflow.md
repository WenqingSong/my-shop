# Agent Workflow 状态模型（Workflow V2）

本文面向项目接手者，说明 Workflow V2 的任务状态模型、Evidence Snapshot / Neutral Tail 语义、五个 Gate、Owner Authority Boundary、Global Registry 资源授权与 Handoff Contract。事实来源为 `agent-workflow-v2` 的 APPROVED Contract 与最终实现。

本文是「结论」（面向接手者的长期稳定结构事实）；`contract.md` 是「过程」（单任务决策记录）；`docs/agent/*` 是「执行流程」（各角色 Prompt 与协作规范，引用本文不重复定义）。

## 1. 职责与边界

- 状态模型属于 **Agent Control Plane**（治理事实），与 **Work Plane**（业务 `my-shop` 服务）分离。
- 机器事实源是 `state.yaml`；人读证据 Artifact（`findings.md` / `core-logic.md` / `owner-decision.md` / `delivery.md` / `contract.md`）是「为什么」，不承担阶段权威。
- **Agent Autonomous Zone = Feature Branch**；**Owner Controlled Zone = Branch Lifecycle + Shared `develop`**。Agent 在 feature 内自主工程化协作，但不能替 Owner 决定任务边界，也不能控制共享主线。
- 不引入 DB/MQ/BPMN/Web UI/Orchestrator；不自动派发角色；不代理任何 git 写操作。

## 2. 六角色模型

```text
TaskBuilder → Analyst → Coder → Cleaner → OwnerGate → Deliverer → Owner
```

Owner 不是第七个 Agent，而是 **Decision Authority + Shared Repository Authority**。六个 Agent 默认运行在独立 Session，通过 immutable Git snapshot、Artifact 与标准 Handoff 交接。

| 角色 | 回答的问题 | 拥有的 Artifact |
|---|---|---|
| TaskBuilder | 交付什么、怎样判断完成 | `task.md`、`state.yaml` 初始结构 |
| Analyst | 有哪些约束、可行方案、必须保持什么性质 | `contract.md`、`docs/design/*`、`state.contract`、`state.resources` |
| Coder | 怎样在既定范围内完成实现 | 业务代码、测试、migration、`state.review`（发起 PENDING） |
| Cleaner | 完整变更是否满足任务、测试是否可信 | `findings.md`、`core-logic.md`、`state.review`（CLEAN） |
| OwnerGate | 核心机制是什么、请求 Owner 决策并持久化 | `owner-decision.md`、`state.owner` |
| Deliverer | 能否在目标环境真实运行 | `delivery.md`、`state.delivery` |

## 3. `state.yaml`（唯一机器权威源）

只保存 Git 无法推导的工作流决策事实。**明确不保存** `phase`、`STALE`、`DONE`、`branch`、`HEAD`、`working_tree`、`changed_files`、`NEXT_ROLE`、`NEXT_ACTION`。

```yaml
schema_version: 2
task_id: <task-slug>

contract:
  status: PENDING | APPROVED | REJECTED | NOT_REQUIRED
  target: <evidence-commit-sha | "">

resources:
  migrations: [<version>]
  error_code_domains: [<区间>]

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

- `resources` 只声明「需要什么」，不自证满足；真正 Authority 永远读取 `origin/develop:.agent/registry/*`。
- 资源值必须是纯机器值（如 `20261001000011`、`12000-12999`），带说明的非法值在 Resource Schema 层直接 FAIL。

## 4. 三种 Evidence Binding（核心）

V2 用统一的「Evidence Snapshot + Allowed Neutral Tail = Still Valid」取代 V1 的 phase rollback / STALE / 重新推进状态。

### 4.1 Contract（INV-2）

- `contract.target` 绑定 Owner 最终批准版本 `contract.md` 的 **Evidence Commit**（A1）。
- Analyst 采用两段式提交：A1（contract 内容）→ A2（只改 `state.yaml` 写入 `contract.status=APPROVED`、`contract.target=A1`）。
- 有效性 = `contract.target` 为真实 commit + `contract.target` 中 `contract.md` 与当前 `contract.md` 完全一致。
- **Design 不绑定 Owner Approval SHA**；由 Analyst 在 Coder 前完成，由 Cleaner 做 Contract/Design/Implementation 三边一致性审查。

### 4.2 Review（INV-3 / INV-4）

- `review.target` 绑定 Cleaner 真正审查的 immutable implementation **Evidence Commit**（C1）。
- Coder 两段式提交：C1（业务代码 + 测试）→ C2（只改 `state.yaml` 写入 `review.status=PENDING`、`review.target=C1`）。Cleaner 审 C1，不是 C2。
- CLEAN 有效性 = `review.target` 为真实 commit + `review.target..featureHEAD` 只允许**当前 task** 的 review-neutral artifacts。
- 任何 substantive change（即使只改 `.go` 注释）都使旧 CLEAN 机械失效。**Staleness 是推导结果，不是持久状态**。

### 4.3 Delivery（INV-6）

- `delivery` 绑定三个不可变 SHA：`review_target`（被验证的业务实现 C1）、`feature_head`（实际参与集成验证的 feature snapshot F8）、`develop_base`（验证时 shared develop 的 D12）。
- `feature_head` 不要求等于当前 remote feature HEAD；它之后只允许**当前 task** 的 delivery-neutral artifacts（`delivery.md`、`state.yaml`）。
- `origin/develop` 前进后，旧 PASS 自动失去 Merge Ready 效力。

### 4.4 白名单（task-scoped，禁止 basename-only）

review-neutral（`review.target` 之后允许）：

```text
.agent/tasks/<task>/state.yaml
.agent/tasks/<task>/findings.md
.agent/tasks/<task>/core-logic.md
.agent/tasks/<task>/owner-decision.md
.agent/tasks/<task>/delivery.md
```

delivery-neutral（`feature_head` 之后允许，更窄）：

```text
.agent/tasks/<task>/state.yaml
.agent/tasks/<task>/delivery.md
```

其他目录、其他 task 的同名文件一律 substantive。

## 5. 五个 Gate

CLI：`workflow-check gate <coder-start|cleaner-start|owner-gate-start|delivery-start|merge-ready> <task>`。Validator 只读，结果统一 `PASS / FAIL / ERROR`（exit 0 / 1 / 2）。

| Gate | 回答的问题 | 关键前置 |
|---|---|---|
| `coder-start` | Coder 有资格开始实现吗？ | blocked=false + handoffReady + Contract 有效 + Resources 授权 |
| `cleaner-start` | immutable implementation snapshot 能交 Cleaner 吗？ | review=PENDING + target 真实/在 feature 历史 + handoffReady + review tail 仅 neutral |
| `owner-gate-start` | 当前 CLEAN snapshot 还能让 Owner 确认吗？ | blocked=false + handoffReady + review=CLEAN + cleanValidity PASS |
| `delivery-start` | CLEAN + Owner Accepted 的 feature 能开始验收吗？ | Contract/Resources 有效 + cleanValidity + owner=ACCEPTED + owner.review_target==review.target + handoffReady |
| `merge-ready` | Deliverer 验证结果现在仍可交 Owner 合并吗？ | 上述全部 + delivery=PASS + delivery 三个 SHA 绑定 + develop/feature 新鲜度 + handoffReady |

`handoffReady`（正常 Handoff 的 Clean Exit）：非 detached HEAD、working tree clean、`origin/<current-feature>` 存在、local feature HEAD == remote feature HEAD。

## 6. Owner Authority Boundary

Owner 保留四类真正需要人判断的职责：

1. **Task / Branch Boundary**：新任务还是延续、是否开新 feature、branch 名称与基线。
2. **Contract Decision**：Analyst 分析后请求 Owner `ACCEPT / REJECT / 继续解释`；Owner 不修改文件，Analyst 根据决定持久化。
3. **Core Logic Decision**：OwnerGate 提炼 1~3 个核心机制，请求 Owner 决策；Owner 不修改 `state.yaml`/`owner-decision.md`，由 OwnerGate 持久化。
4. **Shared Integration Decision**：何时/如何 merge、merge 顺序、squash 方式、并行依赖。**任何 Agent 都不得 merge/push `develop`**。

两个 Owner Checkpoint（`WAITING_FOR_OWNER_DECISION` / `WAITING_FOR_OWNER_ACTION`）**不是 Handoff**：不结束当前 Session、没有 NEXT_ROLE，Owner 回复后原 Agent 继续。

## 7. 核心不变量（INV-1 ~ INV-7）

- **INV-1 Shared Branch Authority**：Only Owner mutates shared develop。Agent 不创建任务 branch、不 push develop、不最终 merge。
- **INV-2 Implement Authorization**：Coder 开始前 Contract valid + Resources 由 origin/develop 授权。
- **INV-3 Immutable Review**：Cleaner 只能 CLEAN 明确 immutable commit，不能 CLEAN working tree。
- **INV-4 Review Freshness**：任何 substantive change 使旧 CLEAN 自动失效；不保存 STALE。
- **INV-5 Owner Binding**：Owner ACCEPTED 必须绑定当前 CLEAN review target。
- **INV-6 Delivery Binding**：Delivery PASS 必须绑定 current review target + exact develop base；develop 前进后旧 PASS 不再 Merge Ready。
- **INV-7 Evidence Snapshot Integrity**：Contract Approval、Cleaner Review、Delivery Verification 都必须绑定明确 immutable Evidence Snapshot；Evidence Snapshot 之后只允许该阶段定义的 neutral metadata tail，任何超出白名单的变化都会使对应验证结果失效。

## 8. Handoff Contract

Handoff = 真正交班（当前角色完成或终止本轮职责，把控制权交给另一个角色）。正常一个 Session 只产生一次最终 Handoff。Gate 是 Handoff 前的机械出站检查。

正常 Handoff 必须：该提交的已 commit、该 push 的已 push、working tree clean、明确 Git SHA、`REMOTE_SYNCED=YES`、`NEXT_ROLE` 与 `NEXT_ACTION` 必填。仅阻塞退出可例外（`RESULT=BLOCKED`）。

Handoff 禁止携带：上一个 Agent 的完整思维过程、聊天历史、大量源代码、对下一角色结论的诱导。

## 9. Global Registry 资源授权

- 状态：`RESERVED`（Reservation 已落到共享 `develop`、Feature 未合并）/ `ACTIVE`（已合并）/ `RELEASED`（取消释放）。
- `Task State records the fact; shared develop Registry authorizes the fact.` Feature 内私自声明 `RESERVED` 不产生全局授权。
- Analyst 是 Reservation 的语义负责人；Owner 是 Shared Registry Mutation Authority。
- Reservation 流程是 Owner Checkpoint（`WAITING_FOR_OWNER_ACTION`），Analyst 验证 `origin/develop` Registry 真正含 `RESERVED/ACTIVE` 且 owner 正确后才 HANDOFF Coder。
- migration version 一经分配永久 tombstone、不得复用；错误码域仅纯 `RESERVED` 阶段可 `RELEASE` 后复用。

## 10. Validator 边界

- `cmd/workflow-check` 独立只读 CLI，与业务二进制 `my-shop` 分离。
- 只读、比较、验证、`PASS/FAIL/ERROR`；禁止自动改 YAML、自动切状态、自动选 Agent、自动 reserve、自动 merge、自动修复、自动判断业务语义。
- **不代理任何 git 写操作**；Agent 的 git 写约束属于协议/Prompt 层，shared develop 的机器级保护交给仓库 branch protection / 外部 Git 控制能力。
- 对 `schema_version != 2` 的任务调用 gate → `ERROR`（exit 2）。

## 11. Legacy 策略

- `schema_version` 缺省 / `1` → Legacy；`2` → Workflow V2。
- 不自动回填历史 V1 Task；已有历史保留。需要迁移某具体任务时单独显式迁移。
