# Workflow Handoff Contract（V3.2.0-B）

本文件定义七角色统一的**出站交接**规范：每个 Role 结束本轮执行时，必须在最终回答结尾明确下一步由谁处理、处理什么、依据哪份证据；跨 Role 失败必须给出可直接执行的下一角色 Prompt。

本文件只定义**输出层契约**。它不新增 Gate、不改变 Gate 语义、不修改 `state.yaml` schema、不改 Engine、不重新设计"是否需要 Deliverer"等产品规则。

## 0. 边界与权威

| 事项 | 权威来源 | 本文件的作用 |
| --- | --- | --- |
| 五个 Gate 的语义、前置条件与 exit code | `docs/design/agent-workflow.md` §12 与 `.agent/engine/**` | 只引用 Gate 名称与前置条件，不新增、不改变 |
| 路线、角色边界、Handoff 出站顺序 | `.agent/specs/AgentCollaborationSpecification.md`、`.agent/specs/Five-AgentResponsibilityBoundary.md` | 只补输出结尾的字段与失败出口，不覆盖原规则 |
| 激活条件与退出 | `.agent/specs/WorkflowAgentDirective.md` §2 / §6 | 只引用 |
| DANGER 停机与两轮预算 | `.agent/specs/WorkflowLoopCircuitBreaker.md` §8 | 只引用；DANGER 时本契约强制 `NEXT_ROLE=NONE` |
| 各角色 Git 白名单、Commit 顺序 | 各角色 Prompt 的 Git 出站章节与 `AgentCollaborationSpecification` §6.2 / §11 | 只做接口映射，不另立 Git 规则 |
| 统一 Git Lifecycle（建设中） | Agent C 的 Git Lifecycle 规范 | 本文件使用其共同接口；冲突时以 Agent C 规范与既有规范为准 |

同一份 `state.yaml` 仍然不保存 `NEXT_ROLE` / `NEXT_ACTION` / `NEXT_TARGET` / `HANDOFF_PROMPT`。这些字段只存在于角色最终回答中，是**交接声明**，不是机器事实，也不是 Gate 结论。

### 0.1 不得宣称机器认证

- Engine 只机械校验 `contract.*`、`review.*`、`owner.*`、`delivery.*`、`blocked.active`、Tail 白名单与 `handoffReady`；
- Engine **不能**校验 `NEXT_ROLE`、`NEXT_ACTION`、`NEXT_TARGET`、`HANDOFF_PROMPT`，也**不能**证明某个 Session 真的执行了 Prompt 要求的工作；
- 任何角色都不得在 Prompt 层声称某个 Engine 不支持的交接"已获得机器认证"，也不得声称 Engine 已验证 Session 身份或 Git 作者。

## 1. 适用范围与角色

适用于七个 Role：TaskBuilder、Analyst、Architect、Coder、Cleaner、OwnerGate、Deliverer，以及三条路线 `NORMAL` / `COMPLEX` / `ARCHITECT`。

- 本契约约束全部七个 Prompt；
- TaskBuilder 的出站块在 V3.2.0 第二阶段集成中已改为同一字段接口（`CURRENT_ROLE=TASK_BUILDER`、`NEXT_ROLE` 大写枚举、`FAILURE_HANDOFF` 十项 Prompt），其出口表与本文件 §8 矩阵逐项一致；`TASK_BUILDER` 行因此由「接口已定义」升级为「已强制」。

## 2. 必填输出块

### 2.1 位置

标准块必须是角色最终回答的**最后一个章节**，标题固定为 `## 出站交接`。块之后不得再附加任何文字、表格或"补充说明"。

### 2.2 字段

```text
## 出站交接

RESULT: <本角色终态结果码>
CURRENT_ROLE: <ANALYST | ARCHITECT | CODER | CLEANER | OWNER_GATE | DELIVERER | TASK_BUILDER>
NEXT_ROLE: <唯一接收者>
NEXT_ACTION: <HANDOFF | FAILURE_HANDOFF | OWNER_DECISION | SAME_SESSION | STOP>: <一句话动作>
NEXT_TARGET: <任务路径>；<状态字段 / Commit / 证据路径>
HANDOFF_PROMPT: <NONE | 下方 fenced block 中的完整可复制 Prompt>
```

需要跨 Role 执行时，`HANDOFF_PROMPT` 后紧跟一个 `text` fenced block，内容为完整 Prompt：

````text
## 出站交接

RESULT: ...
CURRENT_ROLE: ...
NEXT_ROLE: CODER
NEXT_ACTION: FAILURE_HANDOFF: 修复 CLEAN-001 后重新形成 C1/C2
NEXT_TARGET: .agent/tasks/<task-slug>/；review.target=<C1 完整 SHA>
HANDOFF_PROMPT:
```text
<按 §4 的十项模板填写的完整 Prompt>
```
````

字段顺序固定，不得改名、不得省略。`NEXT_ROLE` 必须恰好一个；禁止 `Coder / Analyst`、`Analyst 或 Owner`、"Owner 决定后再说"这类菜单。

### 2.3 字段语义

| 字段 | 语义 | 常见错误 |
| --- | --- | --- |
| `RESULT` | 本角色终态结果码，按角色定义（§8） | 用 `DONE` / `OK` 掩盖 `BLOCKED` / `FAIL` |
| `CURRENT_ROLE` | 产出本块的角色 | 用下一角色名代替自己 |
| `NEXT_ROLE` | 唯一下一接收者：`ANALYST` / `ARCHITECT` / `CODER` / `CLEANER` / `OWNER_GATE` / `DELIVERER` / `TASK_BUILDER` / `OWNER` / `NONE` | 把 Owner 决策写成"交给 Coder 继续" |
| `NEXT_ACTION` | 以五种标记之一开头的具体动作 | 只写"继续处理""请 Owner 处理" |
| `NEXT_TARGET` | 任务路径 + 状态/证据锚点 | 只写"当前分支"、只写分支名、只写 `HEAD` |
| `HANDOFF_PROMPT` | 跨 Role 执行时完整可复制 Prompt；否则 `NONE` | 写了 Prompt 但缺少 §4 的必填项；或本应交接却写 `NONE` |

### 2.4 五种 `NEXT_ACTION` 标记

| 标记 | 含义 | `NEXT_ROLE` | `HANDOFF_PROMPT` |
| --- | --- | --- | --- |
| `HANDOFF` | 正常完成，控制权交给下一 Role，新 Session | 执行型 Role 或 `OWNER`（最终集成决策） | 交给 Role 时必填；交给 `OWNER` 时 `NONE` |
| `FAILURE_HANDOFF` | 本角色失败，需要另一 Role 接手处理 | 唯一承接 Role，或 `OWNER`（决策请求） | 必填（交给 `OWNER` 时是决策请求 Prompt，不是执行指令） |
| `OWNER_DECISION` | 等待 Owner 决策，Session 保持 | `OWNER` | `NONE` |
| `SAME_SESSION` | 同 Session 内阶段推进或本角色继续修复 | 本角色或同 Session 的下一阶段 | `NONE` |
| `STOP` | 合法停止：`BLOCKED`、`DANGER`、Owner 终止任务 | `NONE`，或唯一有权解除阻塞者（`OWNER` / 上游 Role） | `NONE` |

## 3. 正常 HANDOFF

正常交接必须同时满足：

- 本角色职责与 owned artifacts 已完整落盘；
- 应 commit 的已 commit、应 push 的已 push；
- working tree clean，`git rev-parse HEAD` == `git rev-parse origin/<current-feature>`；
- 下一角色入口 Gate 已 `PASS`；
- `NEXT_ROLE` 唯一，`NEXT_TARGET` 指向真实任务路径与证据。

正常交接的 `HANDOFF_PROMPT` 必须让接收者不读聊天记录也能开工，至少写明：接收 Role、Repository / Branch / Task Path、当前 Commit 与证据状态、路线、本次结果（非失败时写"无失败原因"）、接收者应做什么、允许修改的范围、应验证的 Gate 与完成条件、不得修改的内容、完成后的下一步。

Prompt 必须写明下一角色的**入口 Gate 名称**与其执行前置条件（见 §9），例如 `cleaner-start` 要求 `review.status=PENDING`、target 为当前 feature 历史中的真实 commit 且 Tail 只含 Review-neutral artifacts。

Gate `FAIL`、push 失败、远端未同步或工作区不干净时：**不得输出 `HANDOFF`**，按 §3.4 输出 `STOP`，并如实写明未持久化的状态。

## 4. 失败后跨 Role HANDOFF

失败退出不得只写"返回 Coder""请 Owner 处理""交 Analyst-Owner"。必须给出可直接执行的 Prompt，至少包含以下十项：

1. 接收 Role（与 `NEXT_ROLE` 一致）。
2. Repository / Branch / Task Path。
3. 当前 Commit 与证据状态（feature HEAD、C1/D1/A1 等；未知写 `UNKNOWN` 并给出核验命令）。
4. 路线 `NORMAL` / `COMPLEX` / `ARCHITECT`。
5. 具体失败原因（结果码、Finding ID、Gate 输出、复现命令或错误摘要）。
6. 接收者应做什么（最小必要动作，不夹带新需求）。
7. 允许修改的范围（文件/目录/Artifact 白名单）。
8. 应验证的 Gate 与完成条件（Gate 名称必须来自五个既有 Gate，或明确"无入口 Gate"）。
9. 不得修改的内容（Contract、Design、他人 artifacts、Finding 状态、Owner 状态等）。
10. 完成后的下一步（回到哪个 Gate、交给哪个 Role、需要谁决策）。

要求：

- 十项缺一不可；不得用"同上""按规范处理"代替；
- 允许修改范围必须显式，默认拒绝范围外修改；
- 失败原因必须来自本次真实证据，不得把推断写成事实；
- 需要跨 Role 但未完成本角色出站收尾时，Prompt 中必须写明"本角色未完成交接，接收者先核验实际状态"，不得伪装成已完成交接。

## 5. 等待 Owner 决策

`NEXT_ROLE=OWNER`，`NEXT_ACTION` 以 `OWNER_DECISION:` 开头，明确 Owner 需要回答的**最小问题**、可选项与影响；`HANDOFF_PROMPT=NONE`。

- 这不是 HANDOFF：Session 保持，Owner 回复后原 Session 继续；
- 不得写 `HANDOFF: 交 Owner`，不得把"等待 Owner"写成"已交接 Owner"；
- 不得替 Owner 预设答案，不得把 Agent 推荐写成 Owner 决定；
- 不得仅因"Owner 没回复"推进到下游 Role。

## 6. 合法停止、`BLOCKED` 与 DANGER

### 6.1 `BLOCKED`

`NEXT_ACTION` 以 `STOP:` 开头，写明阻塞事实、证据与解除条件；`HANDOFF_PROMPT=NONE`。

- `NEXT_ROLE` 只能是 `OWNER`（解除条件是 Owner 决策/授权）、`NONE`（当前无人可解除，需外部条件），或**缺失产物/交接的责任 Role**（如 Coder 尚未完成 C1/C2，Cleaner 不能进入审查）；
- `BLOCKED` 是合法停止：`NEXT_ROLE` 只表示"谁需要先行动才能解除"，不表示控制权已交出，也不授权该 Role 越过自己的范围；
- 不得把 `BLOCKED` 路由给下游验收 Role（例如 `BLOCKED → Cleaner`），也不得在 `BLOCKED` 时生成要求下游继续实现的 Prompt；
- `blocked.active=true` 的写入规则保持各角色与熔断规范原样；本契约不新增状态字段。

### 6.2 DANGER

`RESULT=DANGER`、`NEXT_ROLE=NONE`、`NEXT_ACTION` 以 `STOP:` 开头、`HANDOFF_PROMPT=NONE`。

- 必须先按 `WorkflowLoopCircuitBreaker.md` §8 完成账本、`danger-report.md`、`state.blocked.active=true`、commit、push 与远端核验；
- DANGER 后不得运行下游 Gate，不得生成任何要求下游继续执行的 Prompt；
- 持久化失败时输出 `RESULT=BLOCKED`（附 `PERSISTENCE_FAILURE`），不得假装 DANGER 已记录；
- Owner 恢复后由对应角色的正确入口重新建立证据，旧结论不得复用。

### 6.3 Owner 终止任务

`RESULT=STOPPED`（或角色已有终止码）、`NEXT_ROLE=NONE`、`NEXT_ACTION=STOP:`、`HANDOFF_PROMPT=NONE`；`blocked.active` 按熔断规范保持 `true`。

## 7. 同 Session 继续

以下情况**不是**跨 Role HANDOFF，必须使用 `SAME_SESSION:`，`NEXT_ROLE` 为本角色或同 Session 的下一阶段，`HANDOFF_PROMPT=NONE`：

- Analyst 在 Owner 追问下继续修订 Contract 草案；
- Architect / Coder 在自身 Scope 内可继续修复（可自行修复的失败）；
- OwnerGate 在 `ARCHITECT` 路线完成 V1 结构验证后，同 Session 进入 V2 Owner 决策；
- OwnerGate 回答 Owner 追问后继续等待决策。

不得把同 Session 推进写成 `HANDOFF: 交 OwnerGate`，也不得为它生成"新角色 Prompt"。

## 8. 覆盖矩阵（机器可读）

字段固定为：`role|terminal|result|next_role|action|gate|prompt|enforced`。

- `gate` 只能取 `coder-start` / `cleaner-start` / `owner-gate-start` / `delivery-start` / `merge-ready` / `none`；
- `prompt` 取 `REQUIRED` / `NONE`；
- `enforced=YES` 表示该 Role Prompt 的 `# handoff-exits` 表必须与矩阵逐项一致（七个角色全部适用）；V3.2.0 第二阶段集成后不再存在 `NO` 行。

`RESULT` 是角色终态码，可与 `state.yaml` 的枚举不同（例如 Deliverer 的 `FAIL_*` 只表达失败原因与路由）；`state.delivery.status` 等 schema 字段仍保持 `PASS` / `FAIL` / `BLOCKED` 原语义，不因本契约新增枚举。

```text
# handoff-matrix v1
role|terminal|result|next_role|action|gate|prompt|enforced
ANALYST|APPROVED_NORMAL|CONTRACT_APPROVED|CODER|HANDOFF|coder-start|REQUIRED|YES
ANALYST|APPROVED_COMPLEX|CONTRACT_APPROVED|CODER|HANDOFF|coder-start|REQUIRED|YES
ANALYST|APPROVED_ARCHITECT|CONTRACT_APPROVED|ARCHITECT|HANDOFF|coder-start|REQUIRED|YES
ANALYST|AWAITING_OWNER|WAITING_FOR_OWNER_DECISION|OWNER|OWNER_DECISION|none|NONE|YES
ANALYST|REJECTED_IN_SCOPE|CONTRACT_REJECTED|OWNER|OWNER_DECISION|none|NONE|YES
ANALYST|REJECTED_SCOPE_CHANGE|CONTRACT_REJECTED|TASK_BUILDER|FAILURE_HANDOFF|none|REQUIRED|YES
ANALYST|REVISION_IN_PROGRESS|CONTRACT_REVISION_IN_PROGRESS|ANALYST|SAME_SESSION|none|NONE|YES
ANALYST|BLOCKED_OWNER|BLOCKED|OWNER|STOP|none|NONE|YES
ANALYST|BLOCKED_NO_ACTOR|BLOCKED|NONE|STOP|none|NONE|YES
ANALYST|DANGER|DANGER|NONE|STOP|none|NONE|YES
ARCHITECT|D1_D2_COMPLETE|D1_D2_COMPLETE|OWNER_GATE|HANDOFF|cleaner-start|REQUIRED|YES
ARCHITECT|REPAIR_IN_SCOPE|STRUCTURAL_REPAIR_IN_SCOPE|ARCHITECT|SAME_SESSION|none|NONE|YES
ARCHITECT|FAILED_CONTRACT|STRUCTURAL_FAILED|ANALYST|FAILURE_HANDOFF|none|REQUIRED|YES
ARCHITECT|FAILED_ROUTE_SWITCH|STRUCTURAL_FAILED|OWNER|FAILURE_HANDOFF|none|REQUIRED|YES
ARCHITECT|BLOCKED_CONTRACT_DESIGN|BLOCKED|ANALYST|STOP|none|NONE|YES
ARCHITECT|BLOCKED_CONTRACT_PENDING|BLOCKED|OWNER|STOP|none|NONE|YES
ARCHITECT|BLOCKED_AUTHORIZATION_ENV|BLOCKED|OWNER|STOP|none|NONE|YES
ARCHITECT|DANGER|DANGER|NONE|STOP|none|NONE|YES
CODER|REVIEW_REQUEST_READY|REVIEW_REQUEST_READY|CLEANER|HANDOFF|cleaner-start|REQUIRED|YES
CODER|REPAIR_IN_SCOPE|IMPLEMENTATION_REPAIR_IN_SCOPE|CODER|SAME_SESSION|none|NONE|YES
CODER|FAILED_CONTRACT|IMPLEMENTATION_FAILED|ANALYST|FAILURE_HANDOFF|none|REQUIRED|YES
CODER|FAILED_SCOPE|IMPLEMENTATION_FAILED|OWNER|FAILURE_HANDOFF|none|REQUIRED|YES
CODER|BLOCKED_CONTRACT_DESIGN|BLOCKED|ANALYST|STOP|none|NONE|YES
CODER|BLOCKED_CONTRACT_PENDING|BLOCKED|OWNER|STOP|none|NONE|YES
CODER|BLOCKED_ENV_AUTH|BLOCKED|OWNER|STOP|none|NONE|YES
CODER|DANGER|DANGER|NONE|STOP|none|NONE|YES
CLEANER|CLEAN|CLEAN|OWNER_GATE|HANDOFF|owner-gate-start|REQUIRED|YES
CLEANER|REVIEW_CONTINUATION|REVIEW_IN_PROGRESS|CLEANER|SAME_SESSION|none|NONE|YES
CLEANER|CHANGES_REQUIRED|CHANGES_REQUIRED|CODER|FAILURE_HANDOFF|none|REQUIRED|YES
CLEANER|ROUTE_MISMATCH|ROUTE_MISMATCH|OWNER_GATE|STOP|none|NONE|YES
CLEANER|BLOCKED_INPUT_INCOMPLETE|BLOCKED|CODER|STOP|none|NONE|YES
CLEANER|BLOCKED_CONTRACT_DESIGN|BLOCKED|ANALYST|STOP|none|NONE|YES
CLEANER|BLOCKED_CONTRACT_PENDING|BLOCKED|OWNER|STOP|none|NONE|YES
CLEANER|BLOCKED_DECISION_ENV|BLOCKED|OWNER|STOP|none|NONE|YES
CLEANER|DANGER|DANGER|NONE|STOP|none|NONE|YES
OWNER_GATE|V1_CLEAN|V1_CLEAN|OWNER_GATE|SAME_SESSION|owner-gate-start|NONE|YES
OWNER_GATE|EVIDENCE_REPAIR_IN_SCOPE|ARTIFACT_REPAIR_IN_SCOPE|OWNER_GATE|SAME_SESSION|none|NONE|YES
OWNER_GATE|V1_CHANGES_REQUIRED|V1_CHANGES_REQUIRED|ARCHITECT|FAILURE_HANDOFF|none|REQUIRED|YES
OWNER_GATE|V1_BLOCKED|V1_BLOCKED|ANALYST|FAILURE_HANDOFF|none|REQUIRED|YES
OWNER_GATE|V1_BLOCKED_CONTRACT_PENDING|V1_BLOCKED|OWNER|STOP|none|NONE|YES
OWNER_GATE|AWAITING_OWNER|WAITING_FOR_OWNER_DECISION|OWNER|OWNER_DECISION|none|NONE|YES
OWNER_GATE|ACCEPTED|ACCEPTED|DELIVERER|HANDOFF|delivery-start|REQUIRED|YES
OWNER_GATE|REJECTED_IMPLEMENTATION|REJECTED|CODER|FAILURE_HANDOFF|none|REQUIRED|YES
OWNER_GATE|REJECTED_STRUCTURAL|REJECTED|ARCHITECT|FAILURE_HANDOFF|none|REQUIRED|YES
OWNER_GATE|REJECTED_CONTRACT|REJECTED|ANALYST|FAILURE_HANDOFF|none|REQUIRED|YES
OWNER_GATE|BLOCKED_AMBIGUOUS|BLOCKED|OWNER|STOP|none|NONE|YES
OWNER_GATE|DANGER|DANGER|NONE|STOP|none|NONE|YES
DELIVERER|VERIFICATION_IN_PROGRESS|VERIFICATION_IN_PROGRESS|DELIVERER|SAME_SESSION|none|NONE|YES
DELIVERER|PASS|PASS|OWNER|HANDOFF|merge-ready|NONE|YES
DELIVERER|FAIL_IMPLEMENTATION|FAIL_IMPLEMENTATION_DEFECT|CODER|FAILURE_HANDOFF|none|REQUIRED|YES
DELIVERER|FAIL_STRUCTURAL|FAIL_STRUCTURAL_DEFECT|ARCHITECT|FAILURE_HANDOFF|none|REQUIRED|YES
DELIVERER|FAIL_CONTRACT|FAIL_CONTRACT_PROBLEM|ANALYST|FAILURE_HANDOFF|none|REQUIRED|YES
DELIVERER|FAIL_ENVIRONMENT|FAIL_ENVIRONMENT_GAP|OWNER|FAILURE_HANDOFF|none|REQUIRED|YES
DELIVERER|FAIL_OUT_OF_SCOPE|FAIL_OUT_OF_SCOPE_EXISTING_ISSUE|OWNER|FAILURE_HANDOFF|none|REQUIRED|YES
DELIVERER|FAIL_STALE_REVIEW|FAIL_STALE_REVIEW_EVIDENCE|CODER|FAILURE_HANDOFF|none|REQUIRED|YES
DELIVERER|FAIL_UNVERIFIED_CHANGE|FAIL_UNVERIFIED_CHANGE|OWNER|FAILURE_HANDOFF|none|REQUIRED|YES
DELIVERER|BLOCKED_ENTRY_GATE|BLOCKED|OWNER|STOP|none|NONE|YES
DELIVERER|DANGER|DANGER|NONE|STOP|none|NONE|YES
TASK_BUILDER|READY_NORMAL|BOOTSTRAP_COMPLETE|CODER|HANDOFF|coder-start|REQUIRED|YES
TASK_BUILDER|READY_COMPLEX|BOOTSTRAP_COMPLETE|ANALYST|HANDOFF|none|REQUIRED|YES
TASK_BUILDER|READY_ARCHITECT|BOOTSTRAP_COMPLETE|ANALYST|HANDOFF|none|REQUIRED|YES
TASK_BUILDER|FAILED_BOOTSTRAP|BOOTSTRAP_FAILED|OWNER|FAILURE_HANDOFF|none|REQUIRED|YES
TASK_BUILDER|REPAIR_IN_SCOPE|BOOTSTRAP_REPAIR_IN_SCOPE|TASK_BUILDER|SAME_SESSION|none|NONE|YES
TASK_BUILDER|BLOCKED_OWNER_INPUT|OWNER_INPUT_REQUIRED|OWNER|OWNER_DECISION|none|NONE|YES
TASK_BUILDER|BLOCKED_OWNER_DECISION|BLOCKED|OWNER|STOP|none|NONE|YES
TASK_BUILDER|BLOCKED_NO_ACTOR|BLOCKED|NONE|STOP|none|NONE|YES
TASK_BUILDER|DANGER|DANGER|NONE|STOP|none|NONE|YES
```

## 9. Git 出站接口

各角色必须在输出交接前自行保存本角色 Artifact，并按其 Prompt 的 Git 章节完成 commit / push。本契约不新增 Git 规则，只固定接口点：

| Role | 必须保存的 Artifact | 允许的提交顺序 | 远端同步检查 | Gate 前置条件 |
| --- | --- | --- | --- | --- |
| TaskBuilder | `task.md`、`state.yaml` 初始化 | 单次 Artifact commit → push | local HEAD == `origin/<feature>`，tree clean | `NORMAL` 自检 `coder-start`；`COMPLEX` / `ARCHITECT` 无入口 Gate |
| Analyst | `contract.md`、`docs/design/*`、`state.contract`；Registry 例外文件 | A1 内容 commit → A2 `state.yaml` commit → push；Registry-only commit 单独 push `develop` | 同上，并核验 `origin/develop` Registry | `coder-start` |
| Architect | `architecture.md`、结构骨架、结构测试、`state.review` | D1 结构快照 → D2 metadata → push | local HEAD == `origin/<feature>` | `cleaner-start` |
| Coder | 生产代码、测试、`state.review` | C1 实现快照 → C2 metadata → push | local HEAD == `origin/<feature>` | `cleaner-start` |
| Cleaner | `findings.md`、`core-logic.md`、`state.review` | 单次 Cleaner-owned artifacts commit → push | local HEAD == `origin/<feature>` | `CLEAN`：`owner-gate-start`；`CHANGES_REQUIRED`：无入口 Gate |
| OwnerGate | `owner-decision.md`、`state.owner`；ARCHITECT 下另有 `findings.md`、`core-logic.md`、`state.review` | V1 evidence commit → push → `owner-gate-start`；V2 decision commit → push | local HEAD == `origin/<feature>` | V1：`cleaner-start`；V2 / 单阶段：`owner-gate-start`；ACCEPT：`delivery-start` |
| Deliverer | `delivery.md`、`state.delivery` | 单次 Delivery commit → push | local HEAD == `origin/<feature>`；`develop_base` == 当前 `origin/<integration-branch>` | `delivery-start`（开工）/ `merge-ready`（交接 Owner） |

**失败不能伪装为已交接**：Gate `FAIL`、push 失败、远端未同步、工作区不干净时，`RESULT` 不得声明 `HANDOFF` 成功；必须输出 `STOP` 并写明实际持久化状态。

## 10. 诚实性与未知事实

- 不得伪造 Commit SHA：未核验时写 `UNKNOWN`，并要求接收者先运行 `git rev-parse HEAD` / `git log -1 --format=%H` 核验；
- 不得伪造任务路径：`task_path` 必须来自本次真实输入或 Git 事实；缺失时停止，不猜测 `task-slug`；
- 不得伪造 Owner 授权：Owner 未明确表达时只能写 `OWNER_DECISION`，不得写 `ACCEPTED` / "Owner 已同意"；
- 不得伪造测试、Gate 或构建结果：未执行写 `NOT_EXECUTED` / `NOT_VERIFIED`；
- 不得用"Agent 自述已完成"代替证据；下一角色必须能只凭 Git + Artifacts 复核；
- 缺少事实时必须明确要求接收者**先核验**，并给出核验命令或读取位置，不得用推测填空。

## 11. 现有规范冲突与取舍

本节只披露取舍，不修改既有规范文件；冲突处以**已验收的强制 Gate 契约**为准。

1. **三条路线的合法出口。** `NORMAL` / `COMPLEX`：Coder → Cleaner → OwnerGate → Deliverer → Owner。`ARCHITECT`：Architect → OwnerGate V1（结构验证，入口 `cleaner-start`）→ OwnerGate V2（`owner-gate-start`）→ Deliverer → Owner。`ARCHITECT` 不经过 Cleaner，也不存在 `Architect → Cleaner` 的 Handoff。
2. **Delivery 可选性 vs OwnerGate 强制交接。** `OwnerGate ACCEPT` 后仍必须 `delivery-start` `PASS` 才能交接；本契约不改变该强制 Gate，也不自行决定任务是否需要 Deliverer。是否需要独立运行验收由 Owner / Task 在任务定义阶段决定；契约只保证：一旦 Owner 明确接受，路由不能跳过 Gate，也不能把"可选"解释成"可绕过已通过的 Gate"。
3. **合法停止 vs `NEXT_ROLE` 必填。** 必填指"必须明确给出唯一值"，`NONE` 是合法值。`DANGER` 与 Owner 终止任务必须 `NONE`；`BLOCKED` 允许 `OWNER`、`NONE`，或**缺失产物/交接的责任 Role**（例如 Coder 尚未形成 C1/C2 时由 Coder 先补完），但不得指向验收型下游（如 `BLOCKED → Cleaner`），也不得把该字段当作让下游继续执行的授权。
4. **`AgentCollaborationSpecification` §5 "Owner Checkpoint 没有 NEXT_ROLE"。** 该表述针对"没有跨角色 Handoff"；本契约在输出层用 `NEXT_ROLE=OWNER` + `NEXT_ACTION=OWNER_DECISION` 标记同 Session 决策检查点，不构成 Handoff，也不写入 `state.yaml`。
5. **DANGER / BLOCKED 不得生成越权执行指令。** 两者的 `HANDOFF_PROMPT` 一律 `NONE`；只允许在 `NEXT_ACTION` 中写明停止原因与恢复条件。
6. **TaskBuilder 出站对齐。** `TaskBuilderPrompt.md` 在 V3.2.0 第二阶段集成中已改为与本契约相同的字段接口与出口表；六个业务角色的 Prompt、本契约与静态检查现在覆盖全部七个角色。TaskBuilder 初始化阶段的 Git 动作（`TB-BRANCH-1`）仍由该 Prompt 定义，本契约只固定其出站声明。

## 12. 静态一致性与 SPEC_ONLY 边界

本契约可以用静态脚本检查：矩阵完整性、字段存在性、唯一接收者、Gate 名称白名单、禁止的路线（如 `ARCHITECT → CLEANER`）与 `DANGER` 的 `NONE` 出口。

静态检查**不能**证明 LLM 在真实运行中一定遵守 Prompt，也不能证明 Session 身份、作者身份或验证真的被执行。这些仍属 `SPEC_ONLY` 约束，由 Role Prompt、Session 隔离与证据审查执行。
