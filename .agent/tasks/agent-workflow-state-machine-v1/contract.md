# Technical Contract

## Decision Status

APPROVED

> 第三轮 Contract Revision（响应 Cleaner CLEAN-001 / CLEAN-002）已获 Owner APPROVED：V1 Validator 能力边界收敛为「仅校验当前状态与 Gate」、`Normative Transition Rule != V1 Runtime Transition Enforcement`、不新增 `previous_phase`、Future Extension 仅记录不实现、统一 `READY_FOR_REVIEW` 术语。

## Problem

现有多 Agent Workflow 的「当前阶段」由各 Agent 自行推断：真实状态散落在 `task.md`、`contract.md`、`findings.md`、`core-logic.md`、`delivery.md`、`.agent/registry/*`、Git HEAD 与聊天决策中，没有唯一可机读的事实源，也没有「谁有权触发哪个转换」的权威。结果是阶段判定依赖 Prompt 文本，文件写入者与 Decision Authority 混同，CLEAN 无绑定 review_target，Registry 的 `RESERVED` 语义错误，进入实现前没有资源 Reservation 的硬 Gate。

本任务收敛为一套有限、明确、可审计、可机读的 Task 状态机与 Transition Authority，并用独立只读 Go Validator 使无效状态不可存在。不实现生产/业务代码，不引入 DB/MQ/BPMN/Web UI/Orchestrator，不推翻现有已验证有效规则。

## Verified Current Behavior

- VERIFIED：全仓库 `state.yaml` 命中 0 处；无任何机器可读 Task State；`STALE`/`RE_REVIEW` 无流程语义。
- VERIFIED：`docs/agent/*` 共 7 文件；协作规范 §5「阶段关口」表已列角色 → 可交接条件 → 结果，但状态由各角色 Prompt 各自描述，无统一状态机、无 Transition Authority。
- VERIFIED：阶段状态散落于 6 类 Artifact 的 6 套词汇，彼此无单一权威、无交叉校验。
- VERIFIED：`agent-workflow-owner-confirmation`（APPROVED）固化 Owner Verification 三态、`ACCEPTED` 仅 Owner 指令驱动、`milestone` 唯一来源 `task.md`、Deliverer 不额外授权，`Design Impact = NONE`。
- VERIFIED：`design-doc-governance`（APPROVED）固化 Analyst 主责长期 Design、`Design Impact` 三态、四者一致才 CLEAN；协作规范 §3 把「状态机与合法迁移」列为长期 Design 事实类别。
- VERIFIED：Registry `RESERVED` 定义（「尚未合并进 develop」）与「Reservation 生效 = 只改 Registry 的 commit 落在 develop」自相矛盾，为语义错误；`check-registry.sh` 注释同源错误。
- VERIFIED：Deliverer 开始关口已可机读，但「Contract APPROVED」「资源 Reservation 有效」未纳入机器 Gate。
- VERIFIED：`docs/design/*` 现有 11 个业务设计文件，无 workflow/state-machine Artifact。
- VERIFIED：`gopkg.in/yaml.v3 v3.0.1` 已在依赖图（`go.mod`，当前 `// indirect`），可直接结构化解析，无需引入新包。
- VERIFIED：业务运行二进制由根 `main.go` → `internal/cmd.Main` 构建（`serve` / `migrate` 子命令）；Workflow Validator 应独立于该二进制，另建 `cmd/workflow-check`。
- VERIFIED：Git 基线 HEAD = `bba7578ea9219e43bb24efb2b48fa994974aa523`（`develop`），working tree 仅新增未跟踪 `.agent/tasks/agent-workflow-state-machine-v1/`；`.agent/tasks/` 下共 19 个任务目录。
- VERIFIED：本任务不占用错误码域 / migration version，无需从 `.agent/registry/*` 派生编号。
- UNKNOWN（非阻塞）：无。

## Selected Design

### S1. 主 `phase` 集合（13 值，仅表达生命周期阶段）

`phase` 是 `state.yaml` 中唯一的 Task 生命周期阶段事实，只回答「下一个有权动作的角色是谁」：

```text
NEW                        任务已创建，未路由
READY_FOR_ANALYST          路由 Analyst（COMPLEX）
READY_FOR_CODER            路由 Coder（NORMAL）
WAITING_FOR_OWNER_APPROVAL Owner 批 Contract
APPROVED                   Contract 已批准 + Design 已同步
IMPLEMENTING               Coder 实现中
READY_FOR_REVIEW           Coder 自验完成 / 或 STALE 后待 Cleaner 复审
IN_REVIEW                  Cleaner 审查中
CHANGES_REQUIRED           回退路由：回 Coder 修复
CONTRACT_REVISION_REQUIRED 回退路由：回 Analyst/Owner 修订
WAITING_FOR_OWNER_ACCEPTANCE 审查通过后 Owner 决定（核心验证 + 最终接受）
DELIVERING                 里程碑验收中
DONE                       Owner 最终接受（终态）
```

**正交子事实（不是 phase，不重复表达）**：

- `review.status`：`NONE | CLEAN | STALE`（绑定 `review.target`）——CLEAN/STALE 只在此。
- `owner_verification.status`：`NOT_REQUIRED | PENDING | ACCEPTED`——只在此。
- `delivery.status`：`NONE | PASS | CONDITIONAL_PASS | FAIL`——只在此。
- `blocked`：正交阻塞事实（`is_blocked` + `reason`），任意 phase 皆可置阻塞，phase 不变；阻塞期间禁止除「解除阻塞」外的任何 phase 转换。

### S2. Transition Authority（每条 phase 转换唯一授权角色；文件写入者 ≠ Decision Authority）

| # | 转换 | Decision Authority | File Writer | Gate / 说明 |
| --- | --- | --- | --- | --- |
| 1 | `NEW → READY_FOR_ANALYST` | Task Builder | Task Builder | COMPLEX |
| 2 | `NEW → READY_FOR_CODER` | Task Builder | Task Builder | NORMAL |
| 3 | `READY_FOR_ANALYST → WAITING_FOR_OWNER_APPROVAL` | Analyst | Analyst | 写 Contract + 推荐 |
| 4 | `WAITING_FOR_OWNER_APPROVAL → APPROVED` | **Owner（ACCEPT）** | Analyst（机械） | — |
| 5 | `WAITING_FOR_OWNER_APPROVAL → CONTRACT_REVISION_REQUIRED` | **Owner（REJECT）** | Analyst（机械） | — |
| 6 | `APPROVED → IMPLEMENTING` | Coder | Coder | Contract APPROVED + Design 同步 + **资源 Gate（Registry 权威）** |
| 7 | `IMPLEMENTING → READY_FOR_REVIEW` | Coder | Coder | 自验完成 |
| 8 | `READY_FOR_REVIEW → IN_REVIEW` | Cleaner | Cleaner | — |
| 9 | `IN_REVIEW → WAITING_FOR_OWNER_ACCEPTANCE` | Cleaner | Cleaner | `review.status=CLEAN`（AC 全 PASS + 无 P0/P1/P2 + 四者一致） |
| 10 | `IN_REVIEW → CHANGES_REQUIRED` | Cleaner | Cleaner | 有可修复缺陷 |
| 11 | `CHANGES_REQUIRED → IMPLEMENTING` | Coder | Coder | Finding 修复 |
| 12 | `APPROVED → CONTRACT_REVISION_REQUIRED` | Analyst | Analyst | 实现期设计冲突 |
| 13 | `WAITING_FOR_OWNER_ACCEPTANCE → DELIVERING` | **Owner（主动进入）** | Deliverer（机械记录） | `review.status=CLEAN` + `owner_verification ∈ {ACCEPTED,NOT_REQUIRED}` + 资源有效 |
| 14 | `WAITING_FOR_OWNER_ACCEPTANCE → DONE` | **Owner（最终接受）** | Owner | `owner_verification ∈ {ACCEPTED,NOT_REQUIRED}` |
| 15 | `DELIVERING → DONE` | **Owner（最终接受）** | Owner | `delivery.status=PASS` |
| 16 | `DELIVERING → CHANGES_REQUIRED` | Deliverer（IMPLEMENTATION_DEFECT） | Deliverer | 实现缺陷 |
| 17 | `DELIVERING → CONTRACT_REVISION_REQUIRED` | Deliverer（CONTRACT_PROBLEM） | Deliverer | 设计问题 |
| 18 | `DELIVERING → WAITING_FOR_OWNER_ACCEPTANCE` | Deliverer（OUT_OF_SCOPE_EXISTING_ISSUE，记录后交 Owner） | Deliverer | 范围外既有问题 |

**子事实转换（非 phase 转换）**：

- `review.status`：`NONE → CLEAN`（Cleaner，随 #9）；`CLEAN → STALE`（Mechanical Invalidation，见 S4）；`STALE → CLEAN`（**仅 Cleaner** 复审）。
- `owner_verification.status`：`PENDING` / `NOT_REQUIRED`（Cleaner 生成 `core-logic.md` 时写初始值）；`PENDING → ACCEPTED`（**Owner 明确指令** → Cleaner 机械记录）。
- `delivery.status`：`NONE → PASS/CONDITIONAL_PASS/FAIL`（Deliverer）。

特殊点：(a) `review.status: CLEAN → STALE` 是 Mechanical Invalidation（确定性单向降级），非角色决策、非 Validator 写状态；(b) `ACCEPTED`（Contract 与 Core Logic 两处）Decision Authority 都是 Owner，File Writer 分别是 Analyst（Contract）与 Cleaner（Core Logic）。

**规范与执行分离**：S2 的 Transition Authority 与合法转换表是 State Machine V1 的**规范性协议事实**（`Normative Transition Rule`），约束各角色 Prompt 与交接行为；V1 Validator **不**在运行时校验历史 transition sequence，也不凭当前快照证明实际执行者身份（能力边界见 S7）。

### S3. `state.yaml` schema（嵌套 YAML，结构化解析）

```yaml
task: <task-slug>
phase: <13 值之一>
review:
  status: NONE | CLEAN | STALE
  target_base: <commit-hash | "">
  target_paths: [<被审查相对路径>]
owner_verification:
  status: NOT_REQUIRED | PENDING | ACCEPTED
delivery:
  status: NONE | PASS | CONDITIONAL_PASS | FAIL
required_resources:
  error_code_domains: [<域区间>]
  migrations: [<version>]
blocked:
  is_blocked: false
  reason: ""
```

- `phase`：唯一生命周期阶段权威；流程判定只读此字段。
- `review.target_base` + `review.target_paths`：Cleaner 记录 `CLEAN` 绑定的可复核版本，是 STALE 判定的输入。
- `required_resources`：仅**声明**任务需要的全局资源；**不记录「已满足/SATISFIED」自证状态**（见 S6）。
- Evidence Artifact（`findings.md` / `core-logic.md` / `delivery.md` / `contract.md`）是「为什么」，`state.yaml` 是「是什么」，Artifact 不承担阶段权威。

### S4. CLEAN / STALE：Mechanical Invalidation（default-deny + 白名单）

- `review.status=CLEAN` 仅在「`review.target` 未发生实质变化」时成立。
- **实质变化判定（default-deny）**：`review.target_paths` 内出现任何非白名单变化即视为实质变化。
- **白名单（Review-neutral，唯一豁免）**：`findings.md`、`core-logic.md`、`delivery.md`、`state.yaml` 中由合法 Transition 产生的机械状态持久化、其他 Contract 明确列出的纯 Workflow Evidence。
- **必须触发的最小集合（保证下限，非穷举）**：Production Code（`internal/`、`api/`、`main.go`）、Business Tests（`*_test.go`）、`contract.md`、`task.md` 的 Scope/AC/Requirement、`docs/design/*`、migration SQL、runtime config、与本 Task 相关的 Registry 语义变化。

**Mechanical Invalidation（`CLEAN → STALE`）** 是确定性、单向的 Mechanical Downgrade，不是 Cleaner 的主观审查决定，也不是 Validator 有权执行的写转换：

1. 触发：`review.status` 仍为 `CLEAN` 时，`review.target` 出现非白名单实质变化 → 先前 CLEAN 客观失效。
2. 效果（一致的状态变化，降级包）：
   - `review.status: CLEAN → STALE`；
   - `phase → READY_FOR_REVIEW`；
   - `owner_verification.status: ACCEPTED → PENDING`（旧 ACCEPTED 针对旧 review.target，不再作为新版本 Gate 依据）；
   - `delivery.status → NONE`（依赖旧 CLEAN 的交付结论失效）。
3. 持久化：任何角色产生或发现该客观失效事实，可机械持久化上述降级；该动作不是新的 Review Decision，**只允许降级，不允许升级**。
4. Validator **只读**：只能检测 `expected STALE, actual CLEAN` 并返回失败，**不得修改 `state.yaml`**。
5. 恢复：**仅 Cleaner** 有权 `STALE → CLEAN`（经 `READY_FOR_REVIEW → IN_REVIEW` 复审，按新 review.target 重新生成/确认 Core Logic Verification）。
6. 保守复审：V1 不要求 Validator 自动判断某次代码变化是否「语义上影响某个 CL」；凡 STALE 一律路由重新 Review，避免机器错误推断业务语义。

### S5. Global Registry 状态机与 `RESERVED` 语义修正

- 修正 `RESERVED` 定义为：**Reservation 已通过「只改 Registry 的 commit」落到共享 `develop` 生效，但拥有该资源的 Feature 尚未合并进 `develop`**。
- 生命周期三态 `RESERVED/ACTIVE/RELEASED` 与转换、复用规则（migration 永久 tombstone 不复用；错误码域仅纯 `RESERVED` 阶段可 `RELEASE` 后复用）保持不变。
- 修正落点：`.agent/registry/error-codes.md`、`migrations.md` 的「状态与生命周期」节、协作规范 §11.4、`scripts/check-registry.sh` 相关注释。

### S6. Resource Authority（`Task State records the fact; shared develop Registry authorizes the fact.`）

- `state.yaml` 只记录 `required_resources`（声明），资源是否满足由 Validator 机械验证 shared `develop` 上的 Registry 权威事实，不读取 `state.yaml` 自证字段。
- 进入 `IMPLEMENTING` 前，Validator 对每个所需全局资源验证：
  1. Reservation 对应的 **Registry-only commit 已进入共享 `develop`**（非仅 Feature Branch）；
  2. Registry 中 `version`/`owner task`/`status` 与当前 Task 一致；
  3. Feature Branch 自行写 `RESERVED` 或自行写 `resources.status: SATISFIED` 均不构成有效 Reservation。
- 这是机器 Gate，不是仅文档规则。

### S7. Validator（独立只读 Go CLI，Agent Control Plane 边界）

- 实现为独立轻量 Go CLI **`cmd/workflow-check`**（与业务运行二进制 `my-shop` 分离），使用 `gopkg.in/yaml.v3` 结构化解析。
- 职责边界：只读；不修改 `state.yaml`、Registry 或任何 Workflow Artifact；不承担 Orchestrator 职责；不做角色派发。
- **校验内容（V1，仅当前状态与 Gate）**：
  - `state.yaml` schema 合法、`phase` 属 13 值、子字段枚举合法；
  - 当前 phase 与正交子字段组合合法（INV-001～INV-010）；
  - 当前 Gate 满足（资源权威 Gate、Deliverer Gate）；
  - review validity：CLEAN 是否因 Review Target 后实质变化而客观 STALE（`expected STALE, actual CLEAN`）；
  - Resource Reservation 是否由 shared `develop` Registry 真实授权；
  - 非法当前状态：`DELIVERING + owner_verification=PENDING`、`DONE + delivery.status != PASS`（有 Deliverer 路径时）等；
  - Transition Authority 表作为 Workflow Protocol 的规范一致性（存在、每条唯一 Decision Authority、`Decision Authority != File Writer`）。
- **不负责（V1）**：不证明历史上每一次 phase transition 实际按合法顺序发生，也不根据当前状态快照证明实际执行者身份；仅凭当前 `state.yaml` 无法证明历史 Transition Sequence / Actor Authenticity。
- exit code：`0` = 全部合法；`1` = 无效状态 / 不变量 / Gate 不满足 / review validity 失效；`2` = 运行错误（缺文件 / 解析失败），且失败原因可定位。
- 至少拒绝（AC-011）：`DELIVERING` + `owner_verification=PENDING`；`IMPLEMENTING` + `required_resources.migrations` 非空但 Registry 无对应有效 `RESERVED/ACTIVE`；`review.status=CLEAN` 但 review.target 实质变更（expected STALE）；`DONE` 但 `delivery.status` 非 PASS（有 Deliverer 路径时）。
- 不得把后续 Agent Orchestration 能力堆入业务 `my-shop` 运行二进制。

**规范与执行的边界**：Transition Authority 与合法 Transition Table 是 State Machine V1 的**规范事实**；V1 Validator 校验当前状态与 Gate 的合法性。对状态变化请求进行真正的 runtime transition enforcement，属于未来唯一状态写入口 / `agentctl` Orchestrator 的职责，不在本任务 Scope。

**Future Extension（本任务不实现）**：未来可引入 `agentctl / controlled transition writer`，接收（current state、requested transition、actor / authority、transition table）并真正执行 allow / deny。本任务不得因此实现 `agentctl`、Event Log 或 Orchestrator。

### S8. Design Artifact 落点

- `docs/design/agent-workflow.md`（新建，`Design Impact = NEW`）：长期稳定的状态模型（phase + 子事实）、Transition Authority、Invariant、Gate、Artifact Authority、Review Target/CLEAN/STALE 语义、Global Registry 状态机。
- `docs/agent/*`：各角色执行流程与 Prompt 规则，引用状态机、不重复定义。

### S9. Cutover Rule（存量任务）

- State Machine V1 生效 commit（本任务合入 `develop` 的 commit）之后创建的新 Task：必须包含合法 `state.yaml`。
- 生效前已有 Task：Legacy 语义，不因缺 `state.yaml` 判违规；Validator 对无 `state.yaml` 的任务跳过（不报 fail）。
- 历史回填另立 Task，本任务不回填 19 个存量任务。

## Interfaces and Data

- `state.yaml`（新建，`.agent/tasks/<task-slug>/state.yaml`）：嵌套 schema 见 S3，唯一机器事实源；模板随 `TaskBuilderPrompt.md` 固化。
- `cmd/workflow-check`（新建，独立 Go CLI）+ `internal/workflow/`（纯校验逻辑 + 单元测试）；`gopkg.in/yaml.v3` 转直接依赖。
- `.agent/registry/error-codes.md` / `migrations.md`：仅修正 `RESERVED` 语义文本，不改分配表、不新增条目。
- `docs/design/agent-workflow.md`（新建）：长期状态机结构事实。
- `docs/agent/*`（`AgentCollaborationSpecification.md`、`Five-AgentResponsibilityBoundary.md`、`models/*` 5 文件）：同步到状态机模型，消除旧规则残留。
- 不新增错误码域、migration version；不修改 `internal/codes/codes.go` 与 `internal/migrations/sql/*`；不改业务二进制 `main.go` / `internal/cmd/` 的 serve/migrate 逻辑。

## Business Invariants

- INV-001：`state.yaml.phase` 是 Task 阶段判定的唯一权威；不存在以 Markdown 第一行或 Git HEAD 作为唯一状态来源的判定路径。（AC-001）
- INV-002：每条 phase 转换有且仅有一个 Decision Authority；任何角色只能触发被授权转换；文件写入者 ≠ Decision Authority。（AC-003，规范性规则——由各角色 Prompt 遵守，非 V1 Validator 的 runtime transition enforcement）
- INV-003：Contract Approval 与 Core Logic Acceptance 的 `ACCEPTED` 只能由 Owner 明确 ACCEPT/REJECT 指令驱动 Agent 机械持久化；任何 Agent 不得因 CLEAN、测试通过、阅读文件等间接信号自行置 `ACCEPTED`。（AC-004）
- INV-004：`review.status=CLEAN` 绑定唯一 `review.target`；Cleaner 更新 `findings.md`/`core-logic.md` 不使 CLEAN 失效。（AC-005）
- INV-005：`review.target` 实质变化（default-deny + 白名单豁免）触发 Mechanical Invalidation——`review.status: CLEAN → STALE` 且 `phase → READY_FOR_REVIEW`，旧 `ACCEPTED` 与 `delivery.status` 同被失效；仅 Cleaner 复审可 `STALE → CLEAN`；无死循环。（AC-006 + Decision 2/3）
- INV-006：需要全局资源的 Task，进入 `IMPLEMENTING` 前必须由 Validator 机械验证 shared `develop` Registry 权威事实；`state.yaml` 资源记录不自证；Feature Branch 私留 `RESERVED` 无效。（AC-008 + Decision 6）
- INV-007：`DELIVERING` 时 `owner_verification.status ∈ {ACCEPTED, NOT_REQUIRED}`；`DELIVERING + PENDING` 为无效状态。（AC-010/AC-011）
- INV-008：`RESERVED` 语义 = 「Reservation 已在共享 `develop` 生效、Feature 未合并进 `develop`」。（AC-009）
- INV-009：`DONE` 时 `delivery.status=PASS`（有 Deliverer 路径）或 Owner 已最终接受（无 Deliverer 路径）。（AC-011）
- INV-010：V1 生效 commit 后新建 Task 必须含合法 `state.yaml`；生效前 Legacy Task 不因缺 `state.yaml` 判违规。（Decision 5）
- INV-011：`CLEAN → STALE` 是确定性、单向 Mechanical Downgrade——任何角色可机械持久化降级，仅 Cleaner 可升级 `STALE → CLEAN`；Validator 只读，检测 `expected STALE, actual CLEAN` 并失败，不写 `state.yaml`/Registry/任何 Artifact。（Decision 2）

## Failure and Consistency Semantics

本任务无 DB/Redis/MQ。状态机自身的失败语义如下：

- 事实来源：`state.yaml`（`phase` + 子字段）是「当前状态」的唯一事实源；Git 是持久化与审计层，每次转换是一次对 `state.yaml` 的编辑。
- 转换成功 = `state.yaml` 更新 + Validator 通过；「部分完成」（如写了 evidence Artifact 但未更新 `state.yaml`）不构成转换，状态保持原值。
- 无效状态由 Validator 拒绝：非法 `phase`、非法子字段组合、Gate 不满足、review validity 失效均判 fail，Gate 不放开。
- `STALE` 是机械推导 + 降级持久化：由 `git diff` 检测 review.target 实质变更（default-deny 白名单）；Validator 只读检测 `expected STALE, actual CLEAN`；任何角色发现的客观失效可机械持久化降级，仅降级不升级。
- 重复/乱序：Validator 校验当前 `phase` 属 13 值且当前状态/Gate 合法；**不校验**历史 transition sequence 或 actor authenticity——乱序写入若各子字段组合合法且 Gate 满足，V1 Validator 无法凭当前快照识别；历史转换合法性由 Transition Authority（规范）约束、由未来唯一状态写入口 / `agentctl`（runtime）强制。
- 阻塞：`blocked.is_blocked=true` 时禁止除「解除阻塞」外的 phase 转换；阻塞是正交事实，不改 phase。
- 资源权威：Task 状态只记录「需要什么」，Registry（`develop` 上）授权「是否满足」；二者不同源，杜绝自证。
- 冲突：并行任务各自写各自 `state.yaml`（按 task-slug 隔离）；全局资源竞争由 Registry（Git 提交顺序 + 冲突检测）串行化。

## Allowed / Forbidden Changes

允许：
- 新建 `docs/design/agent-workflow.md`。
- 新建 `cmd/workflow-check/`（独立 Go CLI）与 `internal/workflow/`（校验逻辑 + 单元测试）；`gopkg.in/yaml.v3` 转直接依赖。
- 修改 `docs/agent/*`（协作规范、职责边界、5 个角色 Prompt），同步到状态机模型、固化 `state.yaml` 模板、消除旧规则残留。
- 修正 `.agent/registry/error-codes.md`、`migrations.md` 的 `RESERVED` 语义文本，及 `scripts/check-registry.sh` 相关注释；`docs/design/error-codes.md` / `migration.md` 涉及 RESERVED 语义的表述做最小一致性同步。
- 新增/更新 `.agent/tasks/agent-workflow-state-machine-v1/*`。

禁止：
- 修改任何生产/业务代码、业务测试、`internal/codes/codes.go`、`internal/migrations/sql/*`。
- 修改业务运行二进制 `main.go` 与 `internal/cmd/` 的 serve/migrate 逻辑；把 Orchestration 能力堆入 `my-shop`。
- 引入数据库、MQ、BPMN、Web UI 或完整 `agentctl` Orchestrator。
- 让 Validator 写入 `state.yaml`、Registry 或任何 Workflow Artifact。
- 在 Feature Branch 内私留 `RESERVED`；新增任何错误码域或 migration version。
- 在 `state.yaml` 记录资源「已满足/SATISFIED」类自证状态。
- 回填存量 19 个任务的 `state.yaml`。
- 推翻现有已验证有效规则：Owner Verification 三态、`milestone` 唯一来源、Deliverer 不额外授权、Cleaner `CLEAN` 门槛、`Design Impact` 机制、Registry 分配/复用/tombstone 规则。
- 让 `state.yaml` 之外的文件（Markdown 首行、Git HEAD）成为唯一状态来源。

## Verification Requirements

- INV-001/INV-002 → 阅读 `docs/design/agent-workflow.md` 与 `docs/agent/*`：存在 `state.yaml` 模板与 13 值 phase 集；`grep` 确认无「以 Markdown 第一行 / Git HEAD 作为唯一状态来源」残留；Transition Authority 表逐条核对授权角色与「文件写入者 ≠ Decision Authority」。
- INV-003 → 场景推演：任何 Agent 不得自行写 `ACCEPTED`；Owner 明确指令 → Analyst/Cleaner 机械持久化路径明确。
- INV-004/INV-005/INV-011 → 合成任务推演 `CLEAN → STALE → READY_FOR_REVIEW → IN_REVIEW → CLEAN`：Cleaner 写白名单文件不失效、实质修改（default-deny）触发 Mechanical Invalidation 降级包（phase 回退 + ACCEPTED/delivery 失效）、仅 Cleaner 可 `STALE→CLEAN`、Validator 只读、无死循环。
- INV-006 → 合成任务 + Validator 运行：缺有效 `develop` Registry Reservation 时 `APPROVED → IMPLEMENTING` 被拒；Feature Branch 私留 `RESERVED` 或写 `SATISFIED` 均无效。
- INV-007/INV-009 → 运行 `go run ./cmd/workflow-check`（或 `go test ./internal/workflow/...`）对构造的无效样本（`DELIVERING+PENDING`、`IMPLEMENTING+缺 Reservation`、`CLEAN+review_target 已改=expected STALE`、`DONE+delivery 未 PASS`）确认拒绝（exit 1），对合法样本确认通过（exit 0）。
- INV-008 → 阅读修正后的 Registry `RESERVED` 定义，确认改为「已在共享 `develop` 生效、Feature 未合并」。
- INV-010 → 场景推演：V1 生效 commit 后新任务缺 `state.yaml` → Validator 判违规；生效前 Legacy 任务缺 `state.yaml` → 跳过不判违规。
- AC-012/AC-013 → 全仓 `grep` 旧规则残留（「Owner 必须/亲自编辑」「Delivery Authorization」「<当前里程碑>」），交叉核对 Prompt ↔ Spec ↔ State Model ↔ Validator；检查 Diff 不引入 DB/MQ/BPMN/Web UI/Orchestrator，现有规则未被推翻。

## Open Risks

- `review.target_paths` 的精确性影响 STALE 判定：过宽误报、过窄漏报；Cleaner 必须如实、完整记录被审查文件集。
- `gopkg.in/yaml.v3` 由 `// indirect` 转直接依赖，属依赖图内变更，无新包引入；`go mod tidy` 自动完成。
- 存量 Legacy 任务无 `state.yaml`，Validator 需明确「跳过不判违规」而非「忽略为通过」，避免后续误用。

## Owner Decision Record

### 第一轮（2026-10-04，六项决策）

1. 主状态模型：拒绝单层 15 值；`phase` 只表达生命周期阶段，`CLEAN/STALE` 归 `review.status`、`PENDING/ACCEPTED/NOT_REQUIRED` 归 `owner_verification.status`、Delivery Result 归 `delivery.status`、`BLOCKED` 为正交阻塞事实；保留 `CHANGES_REQUIRED`、`CONTRACT_REVISION_REQUIRED` 回退阶段；避免同一事实双写。
2. Design Artifact：`docs/design/agent-workflow.md` 放长期状态模型/Transition Authority/Invariant/Gate/Artifact Authority；`docs/agent/*` 放角色执行流程与 Prompt。OPEN QUESTION 关闭。
3. CLEAN/STALE：default-deny + 白名单豁免（`findings.md`/`core-logic.md`/`delivery.md`/`state.yaml` 合法机械持久化/Contract 列明的纯 Workflow Evidence），不维护黑名单；最小触发集写入。
4. Validator：不接受 Shell 解析权威 YAML；保留 `state.yaml`，用轻量 Go + 正规 YAML parser；不扩大为 Orchestrator。
5. 存量 Task：不回填 19 个历史任务；定义 Cutover Rule。
6. Resource Authority：`state.yaml` 资源状态不自证；进入 `IMPLEMENTING` 前机械验证 shared `develop` Registry；`Task State records the fact; shared develop Registry authorizes the fact.`

### 第二轮（2026-10-04，最终 APPROVAL）

Owner 对修订版总体 APPROVED，并追加两项裁决、其余全部确认：

7. **Validator 独立于业务二进制**：不采用 `my-shop workflow-check`；实现为独立轻量 Go CLI `cmd/workflow-check`（Go + `yaml.v3`，只读，不写 `state.yaml`/Registry/Artifact，不承担 Orchestrator，做 schema/invariant/gate/review validity/resource authority 校验，明确 exit code 与可定位失败原因）；不得把 Orchestration 能力堆入 `my-shop` 运行二进制。
8. **`CLEAN → STALE` 为 Mechanical Invalidation**：确定性、单向 Mechanical Downgrade，非 Cleaner 主观决定、非 Validator 写状态；Validator 只检测 `expected STALE, actual CLEAN` 并失败，不写 `state.yaml`；任何角色发现客观失效可机械持久化降级（只降级不升级）；仅 Cleaner 有权 `STALE → CLEAN`。
9. **Review Invalidated 同步回退生命周期**：`review.status: CLEAN → STALE` 同时 `phase → READY_FOR_REVIEW`；依赖旧 CLEAN 的 Delivery Gate 立即失效；旧 `ACCEPTED` 不得继续作为新版本 Gate 依据，新版本先经 Cleaner 复审、按新 review.target 重新生成/确认 Core Logic Verification 后才恢复 Owner Verification/Delivery；V1 不要求 Validator 判断代码变化是否语义影响某 CL，采用保守重新 Review 路由。
10. 其余 Revision 决策全部确认（13 值 phase + 正交子事实、default-deny 白名单、`state.yaml` 权威源、Owner Decision 与机械落盘分离、Registry 授权源、`RESERVED` 语义修正、Cutover Rule、不实现 Orchestrator/`agentctl`）。

适用范围：`agent-workflow-state-machine-v1` 本任务，`Design Impact = NEW`。Contract 标记 `APPROVED`，Analyst 据此完成 `docs/design/agent-workflow.md` Design Sync 后交 Coder 实现。Design 落地若需改变上述状态语义、Transition Authority 或 Gate，不得自行调整，须进入 `CONTRACT_REVISION_REQUIRED`。

### 第三轮（2026-10-04，CONTRACT_REVISION_REQUIRED → APPROVED）

Cleaner CLEAN-001 / CLEAN-002 成立，Owner 指示做最小 Contract Revision（不扩 Scope）：

1. **不新增 `previous_phase`**：`previous_phase` 与 `phase` 可被同一次文件修改共同伪造，不构成可信 Transition History；真正证明历史转换合法需 Event Log / Git Transition History / 唯一状态写入口（未来 `agentctl`），超出 State Machine V1 范围。
2. **明确 V1 Validator 能力边界**：只校验当前 `state.yaml` schema、当前 phase/正交子状态组合、当前 Gate、INV-001～INV-010、CLEAN 客观 STALE、Resource 由 shared `develop` Registry 真实授权、`DELIVERING+PENDING` / `DONE+delivery!=PASS` 等非法当前状态、Transition Authority 表作为 Workflow Protocol 的规范一致性；**不**证明历史每次 phase transition 合法顺序、不凭当前快照证明实际执行者身份。
3. **Transition Authority 不删除**：保留唯一 Decision Authority、合法 phase transition 表、`Decision Authority != File Writer`、Agent Prompt 必须遵守；仅明确 `Normative Transition Rule != V1 Runtime Transition Enforcement`。
4. **Future Extension 留边界**：Design 可记录非本任务实现的 `agentctl / controlled transition writer`（接收 current state / requested transition / actor / transition table，执行 allow/deny）；本任务不实现 `agentctl`、Event Log、Orchestrator。
5. **CLEAN-002 收敛**：统一 phase 词为 `READY_FOR_REVIEW`，清除 `docs/agent/*` 中 `READY_FOR_CLEANER` 残留（交 Coder 最小调整），不得双词并存。

修订后状态回 `WAITING_FOR_OWNER_APPROVAL`。Owner APPROVE 后由 Analyst 同步 `docs/design/agent-workflow.md`，再交 Coder 做最小实现/文档调整，Cleaner 对新 Review Target 复审。

**Owner APPROVAL（2026-10-04）**：Owner 对本轮 Contract Revision APPROVED，确认：① V1 Validator 能力边界收敛为「校验当前 `state.yaml` schema / 当前 phase 与正交子状态组合 / 当前 Gate 与 Invariant / Review Validity / Resource Authority，不证明历史 transition sequence，不证明实际 actor authenticity」；② `Normative Transition Rule != V1 Runtime Transition Enforcement`，Transition Authority 与合法 Transition Table 作为规范事实保留，V1 Validator 不承担历史转换执行证明；③ 不新增 `previous_phase` 等伪历史字段，runtime transition enforcement 留给未来受控状态写入口（如 `agentctl / controlled transition writer`）；④ Future Extension 仅作长期方向记录，不实现 `agentctl` / Event Log / Orchestrator / 自动 Transition Writer；⑤ 统一权威 phase 术语为 `READY_FOR_REVIEW`，从 Workflow Specification / Agent Docs / Prompt / 示例清除 `READY_FOR_CLEANER`。

下一步：Analyst 同步 `docs/design/agent-workflow.md` → 交 Coder 做最小实现/文档收敛 → Cleaner 对新 Review Target re-review。
