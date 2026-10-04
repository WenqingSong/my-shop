# Agent Workflow 状态机设计（Task State Machine）

本文面向项目接手者，说明多 Agent 工作流的任务状态模型、Transition Authority、Review Target / CLEAN / STALE 语义、Global Registry 状态机与资源授权规则。事实来源为 `agent-workflow-state-machine-v1` 的 APPROVED Contract 与最终实现。

本文是「结论」（面向接手者的长期稳定结构事实）；`contract.md` 是「过程」（单任务决策记录）；`docs/agent/*` 是「执行流程」（各角色 Prompt 与协作规范，引用本文不重复定义）。

## 1. 职责与边界

- 状态机属于 **Agent Control Plane**（治理/编排事实），与 **Work Plane**（业务 `my-shop` 服务）分离。
- 机器事实源是 `state.yaml`；人读证据 Artifact（`findings.md` / `core-logic.md` / `delivery.md` / `contract.md`）是「为什么」，不承担阶段权威。
- 本状态机不引入 DB/MQ/BPMN/Web UI/Orchestrator；不自动派发角色。

## 2. Artifact Authority

- `state.yaml`（`.agent/tasks/<task-slug>/state.yaml`）是 Task 当前状态的**唯一机器权威源**；流程判定只读 `phase` 与子字段。
- 不存在以 Markdown 第一行或 Git HEAD 作为唯一状态来源的判定路径。
- Git 是持久化与审计层：每次状态转换是一次对 `state.yaml` 的文件编辑。

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

## 3. 状态模型

### 3.1 主 `phase`（13 值，仅表达生命周期阶段）

| phase | 含义 | 下一个有权动作的角色 |
| --- | --- | --- |
| `NEW` | 任务已创建，未路由 | Task Builder |
| `READY_FOR_ANALYST` | 路由 Analyst（COMPLEX） | Analyst |
| `READY_FOR_CODER` | 路由 Coder（NORMAL） | Coder |
| `WAITING_FOR_OWNER_APPROVAL` | 等 Owner 批 Contract | Owner |
| `APPROVED` | Contract 已批准 + Design 已同步 | Coder |
| `IMPLEMENTING` | Coder 实现中 | Coder |
| `READY_FOR_REVIEW` | Coder 自验完成 / STALE 后待复审 | Cleaner |
| `IN_REVIEW` | Cleaner 审查中 | Cleaner |
| `CHANGES_REQUIRED` | 回退路由：回 Coder 修复 | Coder |
| `CONTRACT_REVISION_REQUIRED` | 回退路由：回 Analyst/Owner 修订 | Analyst |
| `WAITING_FOR_OWNER_ACCEPTANCE` | 审查通过后 Owner 决定（核心验证 + 最终接受） | Owner |
| `DELIVERING` | 里程碑验收中 | Deliverer |
| `DONE` | Owner 最终接受（终态） | — |

### 3.2 正交子事实（不是 phase）

| 子事实 | 取值 | 语义 |
| --- | --- | --- |
| `review.status` | `NONE` / `CLEAN` / `STALE` | 审查结论，绑定 `review.target` |
| `owner_verification.status` | `NOT_REQUIRED` / `PENDING` / `ACCEPTED` | 核心逻辑验证结论 |
| `delivery.status` | `NONE` / `PASS` / `CONDITIONAL_PASS` / `FAIL` | 里程碑验收结论 |
| `blocked` | `is_blocked` + `reason` | 正交阻塞事实，不改 phase |

同一事实只在一处表达：CLEAN/STALE 只在 `review.status`，Owner Verification 只在 `owner_verification.status`，Delivery Result 只在 `delivery.status`，阻塞只在 `blocked`。Gate 与 Validator 用「phase + 子字段组合」表达约束。

## 4. Transition Authority

每条 phase 转换有且仅有一个 **Decision Authority**；文件写入者 ≠ Decision Authority（Owner 的 ACCEPT/REJECT 由 Agent 在明确指令下机械持久化）。

Transition Authority 与合法 Transition Table 是 State Machine V1 的**规范性协议事实**（`Normative Transition Rule`），约束各角色 Prompt 与交接行为。V1 Validator **不**在运行时校验历史 transition sequence，也不凭当前快照证明实际执行者身份（见 §8）。`Normative Transition Rule != V1 Runtime Transition Enforcement`。

### 4.1 phase 转换

| # | 转换 | Decision Authority | File Writer | Gate / 说明 |
| --- | --- | --- | --- | --- |
| 1 | `NEW → READY_FOR_ANALYST` | Task Builder | Task Builder | COMPLEX |
| 2 | `NEW → READY_FOR_CODER` | Task Builder | Task Builder | NORMAL |
| 3 | `READY_FOR_ANALYST → WAITING_FOR_OWNER_APPROVAL` | Analyst | Analyst | 写 Contract + 推荐 |
| 4 | `WAITING_FOR_OWNER_APPROVAL → APPROVED` | Owner（ACCEPT） | Analyst（机械） | — |
| 5 | `WAITING_FOR_OWNER_APPROVAL → CONTRACT_REVISION_REQUIRED` | Owner（REJECT） | Analyst（机械） | — |
| 6 | `APPROVED → IMPLEMENTING` | Coder | Coder | Contract APPROVED + Design 同步 + 资源 Gate |
| 7 | `IMPLEMENTING → READY_FOR_REVIEW` | Coder | Coder | 自验完成 |
| 8 | `READY_FOR_REVIEW → IN_REVIEW` | Cleaner | Cleaner | — |
| 9 | `IN_REVIEW → WAITING_FOR_OWNER_ACCEPTANCE` | Cleaner | Cleaner | `review.status=CLEAN` |
| 10 | `IN_REVIEW → CHANGES_REQUIRED` | Cleaner | Cleaner | 有可修复缺陷 |
| 11 | `CHANGES_REQUIRED → IMPLEMENTING` | Coder | Coder | Finding 修复 |
| 12 | `APPROVED → CONTRACT_REVISION_REQUIRED` | Analyst | Analyst | 实现期设计冲突 |
| 13 | `WAITING_FOR_OWNER_ACCEPTANCE → DELIVERING` | Owner（主动进入） | Deliverer（机械记录） | `review.status=CLEAN` + `owner_verification ∈ {ACCEPTED,NOT_REQUIRED}` + 资源有效 |
| 14 | `WAITING_FOR_OWNER_ACCEPTANCE → DONE` | Owner（最终接受） | Owner | `owner_verification ∈ {ACCEPTED,NOT_REQUIRED}` |
| 15 | `DELIVERING → DONE` | Owner（最终接受） | Owner | `delivery.status=PASS` |
| 16 | `DELIVERING → CHANGES_REQUIRED` | Deliverer（IMPLEMENTATION_DEFECT） | Deliverer | 实现缺陷 |
| 17 | `DELIVERING → CONTRACT_REVISION_REQUIRED` | Deliverer（CONTRACT_PROBLEM） | Deliverer | 设计问题 |
| 18 | `DELIVERING → WAITING_FOR_OWNER_ACCEPTANCE` | Deliverer（OUT_OF_SCOPE_EXISTING_ISSUE） | Deliverer | 范围外既有问题 |

### 4.2 子事实转换

- `review.status`：`NONE → CLEAN`（Cleaner）；`CLEAN → STALE`（Mechanical Invalidation，见 §5）；`STALE → CLEAN`（**仅 Cleaner** 复审）。
- `owner_verification.status`：`PENDING`/`NOT_REQUIRED`（Cleaner 生成 `core-logic.md` 时写初始值）；`PENDING → ACCEPTED`（**Owner 明确指令** → Cleaner 机械记录）。
- `delivery.status`：`NONE → PASS/CONDITIONAL_PASS/FAIL`（Deliverer）。

## 5. Review Target / CLEAN / STALE 语义

- `review.status=CLEAN` 绑定唯一 `review.target`（`target_base` + `target_paths`），Cleaner 在 `findings.md` 的 Review Target 同步记录可复核版本。
- **实质变化判定（default-deny）**：`review.target_paths` 内出现任何非白名单变化即视为实质变化。
- **白名单（Review-neutral，唯一豁免）**：`findings.md`、`core-logic.md`、`delivery.md`、`state.yaml` 中由合法 Transition 产生的机械状态持久化、其他 Contract 明确列出的纯 Workflow Evidence。
- **必须触发的最小集合（保证下限，非穷举）**：Production Code（`internal/`、`api/`、`main.go`）、Business Tests（`*_test.go`）、`contract.md`、`task.md` 的 Scope/AC/Requirement、`docs/design/*`、migration SQL、runtime config、与本 Task 相关的 Registry 语义变化。

### Mechanical Invalidation（`CLEAN → STALE`）

确定性、单向的 Mechanical Downgrade——不是 Cleaner 的主观审查决定，也不是 Validator 有权执行的写转换：

1. 触发：`review.status` 仍为 `CLEAN` 时，`review.target` 出现非白名单实质变化 → 先前 CLEAN 客观失效。
2. 效果（一致的状态变化，降级包）：
   - `review.status: CLEAN → STALE`；
   - `phase → READY_FOR_REVIEW`；
   - `owner_verification.status: ACCEPTED → PENDING`（旧 ACCEPTED 不再作为新版本 Gate 依据）；
   - `delivery.status → NONE`（依赖旧 CLEAN 的交付结论失效）。
3. 持久化：任何角色产生或发现该客观失效事实，可机械持久化上述降级；该动作不是新的 Review Decision，**只允许降级，不允许升级**。
4. Validator **只读**：只能检测 `expected STALE, actual CLEAN` 并返回失败，**不得修改 `state.yaml`**。
5. 恢复：**仅 Cleaner** 有权 `STALE → CLEAN`（经 `READY_FOR_REVIEW → IN_REVIEW` 复审，按新 review.target 重新生成/确认 Core Logic Verification）。
6. 保守复审：V1 不要求 Validator 判断某次代码变化是否「语义上影响某个 CL」；凡 STALE 一律路由重新 Review，避免机器错误推断业务语义。

## 6. Global Registry 状态机与资源授权

### 6.1 `RESERVED` / `ACTIVE` / `RELEASED` 生命周期

- `RESERVED`：**Reservation 已通过「只改 Registry 的 commit」落到共享 `develop` 生效，但拥有该资源的 Feature 尚未合并进 `develop`**。
- `ACTIVE`：已合并进 `develop`，资源在 `develop` 实际生效（终态）。
- `RELEASED`：Task 取消释放（记录保留）。
- 转换：`RESERVED → ACTIVE`（feature 合并进 `develop` 时由合并任务同步）；`RESERVED → RELEASED`（Task 取消时由 Analyst/Owner 标记）。
- 复用规则：migration version 一经分配即永久 tombstone、**不得复用**（`next` 仍计入 RELEASED 的 `max`）；错误码域仅纯 `RESERVED` 阶段可 `RELEASE` 后复用，已实现/已合并不复用。

### 6.2 Resource Authority

`Task State records the fact; shared develop Registry authorizes the fact.`

- `state.yaml.required_resources` 只**声明**任务需要的全局资源，**不记录「已满足/SATISFIED」自证状态**。
- 进入 `IMPLEMENTING` 前，Validator 对每个所需全局资源机械验证 shared `develop` Registry：
  1. Reservation 的 **Registry-only commit 已进入共享 `develop`**（非仅 Feature Branch）；
  2. Registry 中 `version`/`owner task`/`status` 与当前 Task 一致；
  3. Feature Branch 自行写 `RESERVED` 或自行写 `resources.status: SATISFIED` 均不构成有效 Reservation。

## 7. Gate

- **Deliverer Gate**：`WAITING_FOR_OWNER_ACCEPTANCE → DELIVERING` 仅当 `review.status=CLEAN` + `owner_verification ∈ {ACCEPTED, NOT_REQUIRED}` + 资源有效 + Owner 主动进入。
- **Resource Gate**：`APPROVED → IMPLEMENTING` 仅当 Contract APPROVED + Design 同步 + 所需资源经 Registry 权威验证有效。
- 无效状态由 Validator 拒绝：`DELIVERING + owner_verification=PENDING`、`IMPLEMENTING + 必需 migration Reservation 缺失`、`CLEAN + review.target 已改`、`DONE + delivery 未 PASS`。

## 8. Validator

- 独立轻量 Go CLI **`cmd/workflow-check`**，与业务运行二进制 `my-shop` 分离，使用 `gopkg.in/yaml.v3` 结构化解析。
- 只读：不修改 `state.yaml`、Registry 或任何 Workflow Artifact；不承担 Orchestrator 职责；不做角色派发。
- **校验内容（V1，仅当前状态与 Gate）**：
  - `state.yaml` schema 合法、`phase` 属 13 值、子字段枚举合法；
  - 当前 phase 与正交子字段组合合法（INV-001～INV-010）；
  - 当前 Gate 满足（Resource Gate、Deliverer Gate）；
  - Review Validity：CLEAN 是否因 Review Target 后实质变化而客观 STALE（`expected STALE, actual CLEAN`）；
  - Resource Authority：Reservation 是否由 shared `develop` Registry 真实授权；
  - 非法当前状态：`DELIVERING + owner_verification=PENDING`、`DONE + delivery.status != PASS`（有 Deliverer 路径时）等；
  - Transition Authority 表作为 Workflow Protocol 的规范一致性（存在、每条唯一 Decision Authority、`Decision Authority != File Writer`）。
- **不负责（V1）**：不证明历史上每一次 phase transition 实际按合法顺序发生，也不根据当前状态快照证明实际执行者身份；仅凭当前 `state.yaml` 无法形成可信 Transition History / Actor Authenticity。
- exit code：`0` = 全部合法；`1` = 无效状态 / 不变量 / Gate 不满足 / review validity 失效；`2` = 运行错误，失败原因可定位。
- 对无 `state.yaml` 的 Legacy Task 跳过（不判违规）。

**规范与执行的边界**：Transition Authority 与合法 Transition Table 是 State Machine V1 的**规范事实**；V1 Validator 校验当前状态与 Gate 的合法性。对状态变化请求进行真正的 runtime transition enforcement，属于未来唯一状态写入口 / `agentctl` Orchestrator 的职责，不在本任务 Scope。

## 9. Cutover Rule

- State Machine V1 生效 commit（`agent-workflow-state-machine-v1` 合入 `develop` 的 commit）之后创建的新 Task：必须包含合法 `state.yaml`。
- 生效前已有 Task：Legacy 语义，不因缺 `state.yaml` 判违规；Validator 跳过。
- 历史回填另立 Task。

## 10. 业务不变量

- INV-001：`state.yaml.phase` 是 Task 阶段判定的唯一权威；无 Markdown 首行 / Git HEAD 唯一来源路径。
- INV-002：每条 phase 转换有且仅有一个 Decision Authority；文件写入者 ≠ Decision Authority。（规范性规则——由各角色 Prompt 遵守，非 V1 Validator 的 runtime transition enforcement）
- INV-003：`ACCEPTED`（Contract 与 Core Logic）只能由 Owner 明确 ACCEPT/REJECT 指令驱动 Agent 机械持久化。
- INV-004：`review.status=CLEAN` 绑定唯一 `review.target`；Cleaner 更新 `findings.md`/`core-logic.md` 不使 CLEAN 失效。
- INV-005：`review.target` 实质变化触发 Mechanical Invalidation（`CLEAN→STALE` + `phase→READY_FOR_REVIEW` + 旧 `ACCEPTED`/`delivery` 失效）；仅 Cleaner 可 `STALE→CLEAN`；无死循环。
- INV-006：需全局资源的 Task，进 `IMPLEMENTING` 前须经 Validator 机械验证 shared `develop` Registry；`state.yaml` 不自证；Feature Branch 私留 `RESERVED` 无效。
- INV-007：`DELIVERING` 时 `owner_verification ∈ {ACCEPTED, NOT_REQUIRED}`。
- INV-008：`RESERVED` = 「已在共享 `develop` 生效、Feature 未合并进 `develop`」。
- INV-009：`DONE` 时 `delivery.status=PASS`（有 Deliverer 路径）或 Owner 已最终接受（无 Deliverer 路径）。
- INV-010：V1 生效 commit 后新 Task 必须含合法 `state.yaml`；生效前 Legacy Task 不判违规。
- INV-011：`CLEAN→STALE` 是单向 Mechanical Downgrade——任何角色可机械持久化降级，仅 Cleaner 可升级；Validator 只读，不写 `state.yaml`/Registry/Artifact。

## 11. 一致性模型与失败语义

- 事实来源：`state.yaml` 是「当前状态」唯一事实源；Git 是持久化与审计层。
- 转换成功 = `state.yaml` 更新 + Validator 通过；「部分完成」不构成转换。
- 无效状态由 Validator 拒绝；`STALE` 由 `git diff` 机械推导（default-deny 白名单）+ 只读检测 `expected STALE, actual CLEAN`。
- 历史转换：V1 Validator 不校验历史 transition sequence / actor authenticity，仅校验当前状态与 Gate；乱序写入若各子字段组合合法且 Gate 满足，无法凭快照识别（由 Transition Authority 规范约束、未来 `agentctl` runtime 强制）。
- 阻塞：`blocked.is_blocked=true` 时禁止除「解除阻塞」外的 phase 转换。
- 资源权威：Task 状态只记录「需要什么」，Registry（`develop`）授权「是否满足」，不同源杜绝自证。
- 冲突：并行任务按 task-slug 隔离各自 `state.yaml`；全局资源竞争由 Registry（Git 提交顺序 + 冲突检测）串行化。

## 12. Deferred / 已知留白

- 不实现完整 `agentctl` Orchestrator、不自动派发角色；`state.yaml` + 各角色交接自检 + Validator 已足够形成确定性事实。
- **Future Extension（本任务不实现）**：未来可引入 `agentctl / controlled transition writer`，接收（current state、requested transition、actor / authority、transition table）并真正执行 allow / deny，构成可信 Transition History 与 runtime transition enforcement。本任务不实现 `agentctl`、Event Log、Orchestrator 或自动 Transition Writer。
- Legacy 历史任务的 `state.yaml` 回填另立独立 Task。
- `state.yaml` 若未来需要嵌套之外的复杂约束，再评估扩展，不在 V1 引入。
