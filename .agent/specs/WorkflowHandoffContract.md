# Workflow Handoff Contract（V3.4.0）

本文件定义七角色统一的**出站交接**规范。

交接的定位是**定位**，不是叙事：它只告诉开发者「谁做完了、下一棒是谁、任务在哪里、Owner 是否需要动作」。所有任务事实必须已经保存在 Task Artifact、`state.yaml` 与 Git 中；交接块不承担「下一角色任务说明书」的职责。

本文件只定义**输出层契约**。它不新增 Gate、不改变 Gate 语义、不修改 `state.yaml` schema、不改 Engine、不重新设计"是否需要 Deliverer"等产品规则。

## 0. 边界与权威

| 事项 | 权威来源 | 本文件的作用 |
| --- | --- | --- |
| 启动协议、阶段推导、`mode` 推导 | `.agent/specs/WorkflowAgentDirective.md` §2 | 只引用推导结果，不复制推导表 |
| 五个 Gate 的语义、前置条件与 exit code | `docs/design/agent-workflow.md` §12 与 `.agent/engine/**` | 只引用 Gate 名称与前置条件，不新增、不改变 |
| 路线、角色边界、Handoff 出站顺序 | `.agent/specs/AgentCollaborationSpecification.md`、`.agent/specs/Five-AgentResponsibilityBoundary.md` | 只补输出结尾的字段与失败出口，不覆盖原规则 |
| 激活条件与退出 | `.agent/specs/WorkflowAgentDirective.md` §2 / §6 | 只引用 |
| DANGER 停机与两轮预算 | `.agent/specs/WorkflowLoopCircuitBreaker.md` §8 | 只引用；DANGER 时本契约强制 `NEXT_ROLE=NONE` |
| 各角色 Git 白名单、Commit 顺序 | 各角色 Prompt 的 Git 出站章节与 `AgentCollaborationSpecification` §6.2 / §11 | 只做接口映射，不另立 Git 规则 |

同一份 `state.yaml` 仍然不保存 `NEXT_ROLE` / `OWNER_ACTION`。这些字段只存在于角色最终回答中，是**交接声明**，不是机器事实，也不是 Gate 结论。

### 0.1 不得宣称机器认证

- Engine 只机械校验 `contract.*`、`review.*`、`owner.*`、`delivery.*`、`blocked.active`、Tail 白名单与 `handoffReady`；
- Engine **不能**校验 `RESULT`、`NEXT_ROLE`、`OWNER_ACTION` 的语义，也**不能**证明某个 Session 真的执行了角色职责；
- 任何角色都不得声称某个 Engine 不支持的交接"已获得机器认证"，也不得声称 Engine 已验证 Session 身份或 Git 作者。

## 1. 适用范围

适用于七个 Role：TaskBuilder、Analyst、Architect、Coder、Cleaner、OwnerGate、Deliverer，以及三条路线 `NORMAL` / `COMPLEX` / `ARCHITECT`。

## 2. 必填输出块

### 2.1 位置

标准块必须是角色最终回答的**最后一个章节**，标题固定为 `## 出站交接`。块之后不得再附加任何文字、表格或"补充说明"。

### 2.2 字段

```text
## 出站交接

RESULT: <本角色终态结果码>
NEXT_ROLE: <ANALYST | ARCHITECT | CODER | CLEANER | OWNER_GATE | DELIVERER | TASK_BUILDER | OWNER | NONE>
TASK_PATH: .agent/tasks/<task-slug>/
OWNER_ACTION: <NONE | CONTINUE | DECIDE: ... | DO: ... | STOP>
```

可选附加行（最多 2 行，且只在存在无法持久化的启动前提时使用）：

```text
NOTE: <一行启动前提>
```

字段顺序固定，不得改名、不得省略。`NEXT_ROLE` 必须恰好一个；禁止 `Coder / Analyst`、`Analyst 或 Owner`、"Owner 决定后再说"这类菜单。`TASK_PATH` 固定写**任务目录**（不是 `task.md` 路径）。

### 2.3 字段语义

| 字段 | 语义 | 常见错误 |
| --- | --- | --- |
| `RESULT` | 本角色终态结果码，按角色定义（§8） | 用 `DONE` / `OK` 掩盖 `BLOCKED` / `FAIL` |
| `NEXT_ROLE` | 唯一下一接收者 | 把 Owner 决策写成"交给 Coder 继续" |
| `TASK_PATH` | 当前任务目录 | 写分支名、写 `HEAD`、写聊天下文 |
| `OWNER_ACTION` | 开发者需要执行的动作或 `NONE` | 把「等待决策」写成 `NONE`；把「开新 Session」写成 `DO:` |
| `NOTE` | 无法持久化的启动前提（≤2 行） | 复述 AC、SHA、文件清单、实现步骤 |

### 2.4 `OWNER_ACTION` 五种取值

| 取值 | 对应动作语义 | `NEXT_ROLE` | 开发者动作 |
| --- | --- | --- | --- |
| `NONE` | `HANDOFF` / `FAILURE_HANDOFF`：事实已落盘，正常交接 | 执行型 Role 或 `OWNER` | 开新 Session：`以 <NEXT_ROLE> 角色执行任务：<TASK_PATH>` |
| `CONTINUE` | `SAME_SESSION`：同 Session 阶段推进或本角色继续 | 本角色或同 Session 下一阶段 | 无需操作，继续当前 Session |
| `DECIDE: <最小问题>` | `OWNER_DECISION`：等待 Owner 决策 | `OWNER` | 回答该问题 |
| `DO: <动作>` | 需要 Owner 先完成不可由 Agent 完成的操作 | 唯一有权解除者 | 执行后重跑入口 Gate |
| `STOP` | `STOP`：`BLOCKED` / `DANGER` / Owner 终止 | `NONE`，或唯一有权解除者 | 按 `NOTE` 的解除条件处理 |

`OWNER_ACTION` 与矩阵的 `action` 列一一对应：`HANDOFF|FAILURE_HANDOFF → NONE`，`SAME_SESSION → CONTINUE`，`OWNER_DECISION → DECIDE:`，`STOP → STOP`（需要 Owner 先执行动作时用 `DO:`）。

## 3. 正常 HANDOFF

正常交接必须同时满足：

- 本角色职责与 owned artifacts 已完整落盘；
- 应 commit 的已 commit、应 push 的已 push；
- working tree clean，`git rev-parse HEAD` == `git rev-parse origin/<current-feature>`；
- 下一角色入口 Gate 已 `PASS`（`none` 除外）；
- `NEXT_ROLE` 唯一，`TASK_PATH` 指向真实任务目录。

**交接块不包含**：AC / Scope / 不变量复述、完整 SHA、文件清单、实现步骤、Gate 前置条件、下一角色的必读清单。上述内容分别属于 `task.md`、`state.yaml` / `git rev-parse`、Git、Role Prompt、`WorkflowGitLifecycle.md` 与 `WorkflowAgentDirective.md` §4。

Gate `FAIL`、push 失败、远端未同步或工作区不干净时：**不得输出正常交接**，按 §6 输出 `STOP`，并如实写明未持久化的状态。

### 3.3 反模式（静态可检查）

交接块与 `NOTE` 中出现下列内容即违规：

1. 复述 AC、Scope、不变量或 Contract 条款；
2. 罗列完整 40 位 SHA（SHA 只在 `state.yaml` 与 Git 中）；
3. 写「下一步先读 X 文件」或必读清单（那是 Role Prompt 与 Directive §4 的职责）；
4. 写「注意不要改 contract.md」等 owned artifact 规则；
5. `NOTE` 超过 2 行，或出现 `AC-`、`.md` / `.go` 路径、40 位十六进制串。

## 4. 失败后跨 Role HANDOFF

失败退出不得只写"返回 Coder""请 Owner 处理""交 Analyst-Owner"。失败事实必须先持久化，再交接：

| 失败来源 | 持久化载体 | 必含内容 |
| --- | --- | --- |
| Cleaner `CHANGES_REQUIRED` | `findings.md` | Finding 编号、Severity、Trigger、Actual、Expected、Required Fix Boundary |
| OwnerGate `REJECTED` | `owner-decision.md` | 决定、退回对象（`Return To`）、理由、不可修改范围 |
| Deliverer `FAIL_*` | `delivery.md` | 检查项、实际结果、期望结果、证据 |
| Coder / Analyst / Architect / TaskBuilder 失败 | `loop-ledger.md` | `handoff_target`、`receiver_action`、根因、证据 Commit |
| 任意角色触发两轮熔断 | `loop-ledger.md` + `danger-report.md` | 见 `WorkflowLoopCircuitBreaker.md` §6.2 / §8 |

规则：

- 任何 `FAILURE_HANDOFF` 在发出前，必须在上述载体中**已落盘并 push**；否则只能输出 `STOP`（`PERSISTENCE_FAILURE`）；
- 交接块不复述失败原因，只给 `NEXT_ROLE` 与 `TASK_PATH`；接收者按自己的必读清单读取对应载体；
- 失败原因必须来自本次真实证据，不得把推断写成事实；
- 不得用 `RESULT` 掩盖真实终态，也不得把"未完成交接"写成已完成。

## 5. 等待 Owner 决策

`NEXT_ROLE=OWNER`、`OWNER_ACTION=DECIDE: <最小问题>`；必要时用一个 `NOTE` 给出可选项与影响。

- 这不是 HANDOFF：Session 保持，Owner 回复后原 Session 继续；
- 不得写 `NONE` 表示"等 Owner"；
- 不得替 Owner 预设答案，不得把 Agent 推荐写成 Owner 决定；
- 不得仅因"Owner 没回复"推进到下游 Role。

## 6. 合法停止、`BLOCKED`、DANGER 与终止

### 6.1 `BLOCKED`

`OWNER_ACTION=STOP`，用一个 `NOTE` 写明阻塞事实、证据与解除条件。

- `NEXT_ROLE` 只能是 `OWNER`（解除条件是 Owner 决策/授权）、`NONE`（当前无人可解除，需外部条件），或**缺失产物/交接的责任 Role**；
- `BLOCKED` 是合法停止：`NEXT_ROLE` 只表示"谁需要先行动才能解除"，不表示控制权已交出；
- 不得把 `BLOCKED` 路由给下游验收 Role（例如 `BLOCKED → Cleaner`）；
- `blocked.active=true` 的写入规则保持各角色与熔断规范原样；本契约不新增状态字段。

### 6.2 DANGER

`RESULT=DANGER`、`NEXT_ROLE=NONE`、`OWNER_ACTION=STOP`。

- 必须先按 `WorkflowLoopCircuitBreaker.md` §8 完成账本、`danger-report.md`、`state.blocked.active=true`、commit、push 与远端核验；
- DANGER 后不得运行下游 Gate，不得要求下游继续执行；
- 持久化失败时输出 `RESULT=BLOCKED`（附 `PERSISTENCE_FAILURE`），不得假装 DANGER 已记录。

### 6.3 Owner 终止任务

`RESULT=STOPPED`、`NEXT_ROLE=NONE`、`OWNER_ACTION=STOP`。

- 终止 ≠ 阻塞：**不得**写 `blocked.active=true`，否则五个 Gate 会永久拒绝该任务；
- 已产生的 Artifact 保留，不删除、不改写；终止事实写入 `loop-ledger.md` 或 `owner-decision.md`。

## 7. 同 Session 继续

以下情况**不是**跨 Role HANDOFF，使用 `OWNER_ACTION=CONTINUE`，`NEXT_ROLE` 为本角色或同 Session 的下一阶段：

- Analyst 在 Owner 追问下继续修订 Contract 草案；
- Architect / Coder 在自身 Scope 内继续修复；
- OwnerGate 在 `ARCHITECT` 路线完成 V1 结构验证后，同 Session 进入 V2 Owner 决策；
- OwnerGate 回答 Owner 追问后继续等待决策。

不得把同 Session 推进写成 `NONE`，也不得为它生成"新角色 Prompt"。

## 8. 覆盖矩阵（机器可读）

字段固定为：`role|terminal|result|next_role|action|gate|enforced`。

- `gate` 只能取 `coder-start` / `cleaner-start` / `owner-gate-start` / `delivery-start` / `merge-ready` / `none`；
- `action` 只能取 `HANDOFF` / `FAILURE_HANDOFF` / `OWNER_DECISION` / `SAME_SESSION` / `STOP`；
- `enforced=YES` 表示该 Role Prompt 的 `# handoff-exits` 表必须与矩阵逐项一致（七个角色全部适用）。

`RESULT` 是角色终态码，可与 `state.yaml` 的枚举不同（例如 Deliverer 的 `FAIL_*` 只表达失败原因与路由）；`state.delivery.status` 等 schema 字段仍保持 `PASS` / `FAIL` / `BLOCKED` 原语义。

```text
# handoff-matrix v1
role|terminal|result|next_role|action|gate|enforced
ANALYST|APPROVED_NORMAL|CONTRACT_APPROVED|CODER|HANDOFF|coder-start|YES
ANALYST|APPROVED_COMPLEX|CONTRACT_APPROVED|CODER|HANDOFF|coder-start|YES
ANALYST|APPROVED_ARCHITECT|CONTRACT_APPROVED|ARCHITECT|HANDOFF|coder-start|YES
ANALYST|AWAITING_OWNER|WAITING_FOR_OWNER_DECISION|OWNER|OWNER_DECISION|none|YES
ANALYST|REJECTED_IN_SCOPE|CONTRACT_REJECTED|OWNER|OWNER_DECISION|none|YES
ANALYST|REJECTED_SCOPE_CHANGE|CONTRACT_REJECTED|TASK_BUILDER|FAILURE_HANDOFF|none|YES
ANALYST|REVISION_IN_PROGRESS|CONTRACT_REVISION_IN_PROGRESS|ANALYST|SAME_SESSION|none|YES
ANALYST|BLOCKED_OWNER|BLOCKED|OWNER|STOP|none|YES
ANALYST|BLOCKED_NO_ACTOR|BLOCKED|NONE|STOP|none|YES
ANALYST|STOPPED|STOPPED|NONE|STOP|none|YES
ANALYST|DANGER|DANGER|NONE|STOP|none|YES
ARCHITECT|D1_D2_COMPLETE|D1_D2_COMPLETE|OWNER_GATE|HANDOFF|cleaner-start|YES
ARCHITECT|REPAIR_IN_SCOPE|STRUCTURAL_REPAIR_IN_SCOPE|ARCHITECT|SAME_SESSION|none|YES
ARCHITECT|FAILED_CONTRACT|STRUCTURAL_FAILED|ANALYST|FAILURE_HANDOFF|none|YES
ARCHITECT|FAILED_ROUTE_SWITCH|STRUCTURAL_FAILED|OWNER|FAILURE_HANDOFF|none|YES
ARCHITECT|BLOCKED_CONTRACT_DESIGN|BLOCKED|ANALYST|STOP|none|YES
ARCHITECT|BLOCKED_CONTRACT_PENDING|BLOCKED|OWNER|STOP|none|YES
ARCHITECT|BLOCKED_AUTHORIZATION_ENV|BLOCKED|OWNER|STOP|none|YES
ARCHITECT|STOPPED|STOPPED|NONE|STOP|none|YES
ARCHITECT|DANGER|DANGER|NONE|STOP|none|YES
CODER|REVIEW_REQUEST_READY|REVIEW_REQUEST_READY|CLEANER|HANDOFF|cleaner-start|YES
CODER|REPAIR_IN_SCOPE|IMPLEMENTATION_REPAIR_IN_SCOPE|CODER|SAME_SESSION|none|YES
CODER|FAILED_CONTRACT|IMPLEMENTATION_FAILED|ANALYST|FAILURE_HANDOFF|none|YES
CODER|FAILED_SCOPE|IMPLEMENTATION_FAILED|OWNER|FAILURE_HANDOFF|none|YES
CODER|BLOCKED_CONTRACT_DESIGN|BLOCKED|ANALYST|STOP|none|YES
CODER|BLOCKED_CONTRACT_PENDING|BLOCKED|OWNER|STOP|none|YES
CODER|BLOCKED_ENV_AUTH|BLOCKED|OWNER|STOP|none|YES
CODER|STOPPED|STOPPED|NONE|STOP|none|YES
CODER|DANGER|DANGER|NONE|STOP|none|YES
CLEANER|CLEAN|CLEAN|OWNER_GATE|HANDOFF|owner-gate-start|YES
CLEANER|REVIEW_CONTINUATION|REVIEW_IN_PROGRESS|CLEANER|SAME_SESSION|none|YES
CLEANER|CHANGES_REQUIRED|CHANGES_REQUIRED|CODER|FAILURE_HANDOFF|none|YES
CLEANER|ROUTE_MISMATCH|ROUTE_MISMATCH|OWNER_GATE|STOP|none|YES
CLEANER|BLOCKED_INPUT_INCOMPLETE|BLOCKED|CODER|STOP|none|YES
CLEANER|BLOCKED_CONTRACT_DESIGN|BLOCKED|ANALYST|STOP|none|YES
CLEANER|BLOCKED_CONTRACT_PENDING|BLOCKED|OWNER|STOP|none|YES
CLEANER|BLOCKED_DECISION_ENV|BLOCKED|OWNER|STOP|none|YES
CLEANER|STOPPED|STOPPED|NONE|STOP|none|YES
CLEANER|DANGER|DANGER|NONE|STOP|none|YES
OWNER_GATE|V1_CLEAN|V1_CLEAN|OWNER_GATE|SAME_SESSION|owner-gate-start|YES
OWNER_GATE|EVIDENCE_REPAIR_IN_SCOPE|ARTIFACT_REPAIR_IN_SCOPE|OWNER_GATE|SAME_SESSION|none|YES
OWNER_GATE|V1_CHANGES_REQUIRED|V1_CHANGES_REQUIRED|ARCHITECT|FAILURE_HANDOFF|none|YES
OWNER_GATE|V1_BLOCKED|V1_BLOCKED|ANALYST|FAILURE_HANDOFF|none|YES
OWNER_GATE|V1_BLOCKED_CONTRACT_PENDING|V1_BLOCKED|OWNER|STOP|none|YES
OWNER_GATE|AWAITING_OWNER|WAITING_FOR_OWNER_DECISION|OWNER|OWNER_DECISION|none|YES
OWNER_GATE|ACCEPTED|ACCEPTED|DELIVERER|HANDOFF|delivery-start|YES
OWNER_GATE|REJECTED_IMPLEMENTATION|REJECTED|CODER|FAILURE_HANDOFF|none|YES
OWNER_GATE|REJECTED_STRUCTURAL|REJECTED|ARCHITECT|FAILURE_HANDOFF|none|YES
OWNER_GATE|REJECTED_CONTRACT|REJECTED|ANALYST|FAILURE_HANDOFF|none|YES
OWNER_GATE|BLOCKED_AMBIGUOUS|BLOCKED|OWNER|STOP|none|YES
OWNER_GATE|STOPPED|STOPPED|NONE|STOP|none|YES
OWNER_GATE|DANGER|DANGER|NONE|STOP|none|YES
DELIVERER|VERIFICATION_IN_PROGRESS|VERIFICATION_IN_PROGRESS|DELIVERER|SAME_SESSION|none|YES
DELIVERER|PASS|PASS|OWNER|HANDOFF|merge-ready|YES
DELIVERER|FAIL_IMPLEMENTATION|FAIL_IMPLEMENTATION_DEFECT|CODER|FAILURE_HANDOFF|none|YES
DELIVERER|FAIL_STRUCTURAL|FAIL_STRUCTURAL_DEFECT|ARCHITECT|FAILURE_HANDOFF|none|YES
DELIVERER|FAIL_CONTRACT|FAIL_CONTRACT_PROBLEM|ANALYST|FAILURE_HANDOFF|none|YES
DELIVERER|FAIL_ENVIRONMENT|FAIL_ENVIRONMENT_GAP|OWNER|FAILURE_HANDOFF|none|YES
DELIVERER|FAIL_OUT_OF_SCOPE|FAIL_OUT_OF_SCOPE_EXISTING_ISSUE|OWNER|FAILURE_HANDOFF|none|YES
DELIVERER|FAIL_STALE_REVIEW|FAIL_STALE_REVIEW_EVIDENCE|CODER|FAILURE_HANDOFF|none|YES
DELIVERER|FAIL_UNVERIFIED_CHANGE|FAIL_UNVERIFIED_CHANGE|OWNER|FAILURE_HANDOFF|none|YES
DELIVERER|BLOCKED_ENTRY_GATE|BLOCKED|OWNER|STOP|none|YES
DELIVERER|STOPPED|STOPPED|NONE|STOP|none|YES
DELIVERER|DANGER|DANGER|NONE|STOP|none|YES
TASK_BUILDER|READY_NORMAL|BOOTSTRAP_COMPLETE|CODER|HANDOFF|coder-start|YES
TASK_BUILDER|READY_COMPLEX|BOOTSTRAP_COMPLETE|ANALYST|HANDOFF|none|YES
TASK_BUILDER|READY_ARCHITECT|BOOTSTRAP_COMPLETE|ANALYST|HANDOFF|none|YES
TASK_BUILDER|UPDATED_IMPLEMENTATION|UPDATE_COMPLETE|CODER|HANDOFF|coder-start|YES
TASK_BUILDER|UPDATED_ANALYSIS|UPDATE_COMPLETE|ANALYST|HANDOFF|none|YES
TASK_BUILDER|HELD_PRESERVED_CHANGES|BOOTSTRAP_HELD|OWNER|STOP|none|YES
TASK_BUILDER|BASE_UNRESOLVED|BASE_UNRESOLVED|OWNER|STOP|none|YES
TASK_BUILDER|FAILED_BOOTSTRAP|BOOTSTRAP_FAILED|OWNER|FAILURE_HANDOFF|none|YES
TASK_BUILDER|REPAIR_IN_SCOPE|BOOTSTRAP_REPAIR_IN_SCOPE|TASK_BUILDER|SAME_SESSION|none|YES
TASK_BUILDER|BLOCKED_OWNER_INPUT|OWNER_INPUT_REQUIRED|OWNER|OWNER_DECISION|none|YES
TASK_BUILDER|BLOCKED_OWNER_DECISION|BLOCKED|OWNER|STOP|none|YES
TASK_BUILDER|BLOCKED_NO_ACTOR|BLOCKED|NONE|STOP|none|YES
TASK_BUILDER|STOPPED|STOPPED|NONE|STOP|none|YES
TASK_BUILDER|DANGER|DANGER|NONE|STOP|none|YES
```

`TASK_BUILDER` 不存在 `UPDATE_COMPLETE → CLEANER` 出口：`task.md` 不在 Review-neutral 白名单（`.agent/engine/internal/workflow/paths.go`），任何 `task.md` 变更都会使既有 `CLEAN` 在 `owner-gate-start` 失效，`cleaner-start` 也要求 `review.status=PENDING`。因此更新后的合法路径只有「Coder 重建 Evidence」或「Analyst 重定 Contract」。

## 9. Git 出站接口

各角色必须在输出交接前自行保存本角色 Artifact，并按其 Prompt 的 Git 章节完成 commit / push。本契约不新增 Git 规则，只固定接口点：

| Role | 必须保存的 Artifact | 允许的提交顺序 | 远端同步检查 | Gate 前置条件 |
| --- | --- | --- | --- | --- |
| TaskBuilder | `task.md`、`state.yaml`、初始 Artifact | 单次 `BOOTSTRAP` commit（pathspec 只含任务目录）→ push | local HEAD == `origin/<feature>`，tree clean | `NORMAL` 自检 `coder-start`；`COMPLEX` / `ARCHITECT` 无入口 Gate |
| Analyst | `contract.md`、`docs/design/*`、`state.contract` | A1 内容 commit → A2 `state.yaml` commit → push | 同上 | `coder-start` |
| Architect | `architecture.md`、结构骨架、结构测试、`state.review` | D1 结构快照 → D2 metadata → push | local HEAD == `origin/<feature>` | `cleaner-start` |
| Coder | 生产代码、测试、`state.review` | C1 实现快照 → C2 metadata → push | local HEAD == `origin/<feature>` | `cleaner-start` |
| Cleaner | `findings.md`、`core-logic.md`、`state.review` | 单次 Cleaner-owned artifacts commit → push | local HEAD == `origin/<feature>` | `CLEAN`：`owner-gate-start`；`CHANGES_REQUIRED`：无入口 Gate |
| OwnerGate | `owner-decision.md`、`state.owner`；ARCHITECT 下另有 `findings.md`、`core-logic.md`、`state.review` | V1 evidence commit → push → `owner-gate-start`；V2 decision commit → push | local HEAD == `origin/<feature>` | V1：`cleaner-start`；V2 / 单阶段：`owner-gate-start`；ACCEPT：`delivery-start` |
| Deliverer | `delivery.md`、`state.delivery` | 单次 Delivery commit → push | local HEAD == `origin/<feature>`；`develop_base` == 当前 `origin/<integration-branch>` | `delivery-start`（开工）/ `merge-ready`（交接 Owner） |

**失败不能伪装为已交接**：Gate `FAIL`、push 失败、远端未同步、工作区不干净时，`RESULT` 不得声明交接成功；必须输出 `STOP` 并写明实际持久化状态。

## 10. 诚实性与未知事实

- 不得伪造 Commit SHA：未核验时写 `UNKNOWN`，并要求接收者先运行 `git rev-parse HEAD` 核验；
- 不得伪造任务路径：`task_path` 必须来自本次真实输入、TB-NAME-1 生成并记录的 Slug，或 Git 事实；
- 不得伪造 Owner 授权：Owner 未明确表达时只能写 `DECIDE:`，不得写 `ACCEPTED` / "Owner 已同意"；
- 不得伪造测试、Gate 或构建结果：未执行写 `NOT_EXECUTED` / `NOT_VERIFIED`；
- 不得用"Agent 自述已完成"代替证据；下一角色必须能只凭 Git + Artifacts 复核。

## 11. 向后兼容

| 旧形态 | V3.4 处理 |
| --- | --- |
| 交接块含 `HANDOFF_PROMPT` | 接收者只取 `NEXT_ROLE` / `TASK_PATH` / `OWNER_ACTION`；`HANDOFF_PROMPT` 不得作为事实来源 |
| 启动串为 `mode: ...` + `task_path: <task.md>` | 接受，但仅作为覆盖提示；与推导结果冲突 → `BLOCKED` |
| 交接块使用 `NEXT_ACTION` 五值 | 按 §2.4 映射为 `OWNER_ACTION`；不因命名差异阻塞 |
| 旧 Task 无 `route` | 不推断：要求 Owner 显式指定角色，否则 `BLOCKED` |

## 12. 静态一致性与 SPEC_ONLY 边界

本契约可以用静态脚本检查：矩阵完整性、字段存在性、唯一接收者、Gate 名称白名单、`STOPPED` 终态、禁止的路线（如 `ARCHITECT → CLEANER`）、反模式短语、交接块行数。

静态检查**不能**证明 LLM 在真实运行中一定遵守 Prompt，也不能证明 Session 身份、作者身份或验证真的被执行。这些仍属 `SPEC_ONLY` 约束。
