# Workflow 循环熔断规范（V3.1.0，V3.2.0 七角色一致性修订）

> 适用对象：所有已激活的 Workflow Role Session——TaskBuilder、Analyst、Architect、Coder、Cleaner、OwnerGate、Deliverer。
> 规范地位：本文是「同一 Task 同一根因最多两轮阻塞往返」以及 `loop-ledger.md` / `danger-report.md` 两个证据文件的**唯一规范事实源**。角色 Prompt 只写触发点与停止规则，不复制计数细节。
> 修订说明：V3.2.0 只把适用范围与恢复入口补齐到当前七角色，**不改变**已验收的两轮预算、segment、append-only 账本、DANGER 停机与 Owner 恢复语义。

## 0. 边界与权威

- 本规范**不新增** `state.yaml` 字段、Gate、调度器或额外角色（当前为七角色）；它复用既有 `blocked` 状态。
- 本规范**不改变**任何角色的 Git Authority、Gate 前置条件、NORMAL / COMPLEX / ARCHITECT 的角色顺序，也不改变 ARCHITECT 的 OwnerGate V1/V2 阶段划分。Architect 与 ARCHITECT 路线下的 OwnerGate V1 已存在，本文按七角色适用。
- `state.yaml` 仍是唯一的机器状态权威。账本只记录「已经发生的循环事件」，不是第二套状态权威，也不得声明 `review` / `owner` / `delivery` 结论。
- 事实冲突时（账本与 `state.yaml` 在阻塞事实上不一致），以 `blocked.active=true` 为准，并停下报告实际状态；不得用账本内容解除 `blocked`。

### 0.1 这不是机器级限流

本协议是 **Agent 行为协议 + 可追溯证据**，不是 Engine 已具备的强制能力：

- `workflow-check` **不读取** `loop-ledger.md` / `danger-report.md`，不解析账本，不计算轮次；
- Engine 对这两个文件只有一条路径白名单判断（当前 Task 根目录 + 精确文件名），**不能证明**账本 append-only、不能证明轮次真实、不能证明 DANGER 报告属实；
- 跨 Session 的约束力来自「新 Session 必须重新读取账本」，而不是来自机器拒绝。

任何把本规范描述为「无法绕过的机器级限流」的表述都是错误的。真实的机械事实只有：`blocked.active=true` 时五个 Gate 一律拒绝；这两个证据文件在当前 Task 根目录内属于 Neutral Tail。

## 1. 术语

| 术语 | 含义 |
|---|---|
| 阻塞往返 | 一次必须由**其他角色**或 **Owner** 参与才能解除的往返（请求决定、跨角色退回、Contract 修订、环境补齐、Gate 事实争议） |
| `loop_key` | 由问题类别 + 稳定语义根因组成的稳定键；见 §2 |
| 计数键 | `(task_id, loop_key)`；`role`、Session、分支、Finding ID 都不是计数维度 |
| `segment` | 预算周期，形如 `S1`、`S2`；默认每个 segment 允许 2 轮 `REQUEST` |
| 轮次 `N` | 当前 segment 内该 `loop_key` 已记录的 `REQUEST` 事件数 |
| 记账 | 按 §5 write-ahead 把事件追加进 `loop-ledger.md` 并 push 到当前 feature 远端 |

「普通测试重跑、代码格式化、单条失败命令的内部排查、网络短暂重试」不是跨角色或 Owner 的往返，不消耗预算。它们只有在**升级为**「需要其他角色或 Owner 才能解除」时才成为一次计数往返。

## 2. 计数范围与分类

### 2.1 Loop Key 构造

```text
loop_key = <CATEGORY>:<root-cause-slug>
```

- `root-cause-slug`：稳定 ASCII kebab-case，描述**语义根因**，不描述措辞、不引用会变化的 Finding 编号、不包含 Session 或时间。
- 同一 Task 内同一 `root-cause-slug` 只能对应一个语义根因。
- 示例：`OWNER_DECISION:payment-retry-policy`、`ROLE_REPAIR:order-idempotency-missing`、`CONTRACT_REVISION:error-code-domain-unspecified`、`GATE_STUCK:review-target-orphaned`、`ENV_GAP:mysql-not-available`。

### 2.2 类别与分类依据

| 类别 | 判定依据（必须能指向可观察事实） |
|---|---|
| `OWNER_DECISION` | 存在多个真实可行方案，或需要 Owner 在业务规则、风险、成本、范围之间选择；现有 Task / Contract 无法推出唯一答案 |
| `ROLE_REPAIR` | 实现或证据不满足**已确认**的 Task / Contract / AC，需要退回上游角色修复；前提不变 |
| `CONTRACT_REVISION` | 已批准的 Contract 或 AC 本身遗漏、错误或不可实现，必须先修订并重新取得 Owner 批准 |
| `GATE_STUCK` | Gate 机械判定与可复核的 Git / Artifact 事实持续矛盾，且当前角色无权改变前提 |
| `ENV_GAP` | 缺少必要环境、依赖、权限或外部证据，角色无法自行补齐 |

### 2.3 一个真实事件只记一次

分类按以下**优先顺序**机械判定，命中即停止，避免同一事件被双重计数：

1. 解除阻塞需要**改变 Task / AC / Contract 内容** → `CONTRACT_REVISION`（其中包含的 Owner 批准不另记 `OWNER_DECISION`）；
2. 否则需要 Owner 在既有前提下做**选择** → `OWNER_DECISION`；
3. 否则需要**其他角色**修复实现或证据 → `ROLE_REPAIR`；
4. 否则缺环境 / 依赖 / 权限 / 证据 → `ENV_GAP`；
5. 否则 Gate 结论与可复核事实矛盾且本角色无权改变 → `GATE_STUCK`。

因此「同时涉及 Owner 和 Contract」的一次事件只会记成一条 `CONTRACT_REVISION`，只消耗一份预算。

### 2.4 去重依据（防止换壳重获预算）

同一语义根因由其**指纹**判定，三个要素全部相同即为同一 `loop_key`：

1. **阻塞锚点**：受影响的 AC / INV / Contract 条款、Gate 名，以及具体文件或路径（适用时）；
2. **失败语义**：观察到的实际结果与期望结果的差异；
3. **解除条件**：必须发生的变化（改代码 / 改 Contract / Owner 决定 / 补环境）。

以下变化**不构成**新根因，不产生新预算：

| 规避手法 | 处理 |
|---|---|
| 换措辞、换标题 | 指纹不变 → 同一 `loop_key` |
| 换 / 拆分 / 重排 Finding ID | 同一锚点与失败语义 → 合并计数 |
| 换 Session（含新开 Agent） | 新 Session 必须先读账本；`segment` 不因 Session 改变 |
| 换发起角色 | `role` 只是事件属性，不在计数键内 |
| 换分支、rebase、重建 worktree | 分支不在计数键内；不产生新预算 |
| 换类别标签 | 类别按 §2.3 机械判定；标签不改变指纹 |
| 先 DANGER 再「重新开始」 | 已 DANGER 时新 Session 不得重开两轮预算，必须 Owner 显式 `RESET` |
| 事后补记、倒签时间 | 违规；write-ahead 失败必须停止并如实报告 |

只有三个指纹要素之一**真实变化**时，才可以新增 `loop_key`，且必须在事件中写明 `premise_change` 与变化依据。判定不确定时，一律**合并到已有 loop**（保守计数），不得自行拆分。

## 3. 轮次语义

| 已记录轮次 N | 允许的后续行为 |
|---|---|
| `N=0` | 可以发起第 1 轮；必须先记账并 push，再发出请求 |
| `N=1` | 可以发起第 2 轮；必须先记账并 push，再发出请求 |
| `N=2` | 同一根因仍需第 3 轮时**禁止发起**；必须触发 DANGER 并停机 |

- `DANGER` 事件**不消耗轮次**，也不把 N 变成 3。
- 只有在**第三次请求 / 退回尚未发出**的前提下触发 DANGER，才算成功熔断。若第三轮已经发出才发现，属于**协议违规**，必须在 DANGER 报告中如实标注，不得称为「成功熔断」。
- `CLOSE` 只是结束当前 loop，**不重置计数**。同一 `loop_key` 在同一 segment 内再次阻塞时沿用历史 N。
- 只有 `RESET` 能改变预算，且必须来自 Owner 明确授权（§10）。

推论：在未获得 Owner 明确 `RESET` 授权前，两轮预算按 `(task_id, loop_key)` 在**整个 Task 生命周期**内累计，不按 Session、不按角色、不按分支重置。

## 4. Segment 与预算

- 每个 `(task_id, loop_key)` 从 `S1` 开始，默认预算为 **2** 次 `REQUEST`。
- 新 segment 只能由 Owner 明确授权产生（`RESET` 事件），并记录该 segment 的显式预算（例如 `2` 或 `UNBOUNDED`）。
- **不得原地修改**已有 segment 的预算；增加预算必须开新 segment。历史 segment 的事件永久保留。
- 当前 segment 由账本最后一条事件的 `segment` 推导，**不得**在账本文件头维护独立的「当前 segment / 当前轮次」计数器，避免形成第二状态权威。

## 5. Write-ahead 记账协议

发起第 1、2 轮之前，必须按顺序完成：

1. 读取当前 Task 的**完整** `loop-ledger.md`、`danger-report.md`（存在时）和 `state.yaml.blocked`。
2. 按 §2 确定稳定 `loop_key`（不确定时合并到已有 loop）。
3. 检查当前 segment 与预算；若 `N=2`，禁止记账为 `REQUEST`，直接进入 §8 DANGER。
4. 在 `loop-ledger.md` 追加一条 `REQUEST` 事件（格式见 §6）。
5. 只 stage 合法的当前 Task 证据文件：`.agent/tasks/<task-slug>/loop-ledger.md`，以及本次确实同时变更的 `.agent/tasks/<task-slug>/state.yaml`。**禁止**把生产代码、测试、Contract、Task、Design 混入这次 commit。
6. `commit` 并 `push` 当前 feature 分支（普通 push，禁止 force）。
7. 核对远端同步：`local HEAD == origin/<current-feature>`，且远端该文件确实包含新事件（`git show origin/<branch>:<path>`）。
8. 上述全部通过后，才允许发出对应的请求、跨角色退回或 Contract 修订。

失败处理：

- 账本无法写入、commit 失败、push 失败或远端未同步 → **禁止继续发问**，立即输出 `RESULT=BLOCKED`，说明实际持久化状态与失败命令。
- 不得把「本地已写、远端未同步」当作已记账。
- 若请求已经发出而记账失败，恢复持久化后必须补记，并在 `request_summary` 标注 `UNRECORDED_REQUEST`，按**保守原则**计入已消耗轮次（不得当作未发生）。
- 第 5～7 步的提交类型是 `FORCED_EVIDENCE`（`.agent/specs/WorkflowGitLifecycle.md` §2）：允许在角色交接前发生，但只含 `loop-ledger.md`（必要时 `state.yaml`），不得混入业务代码、测试、Contract、Task 或 Design，也不构成 HANDOFF。

## 6. `loop-ledger.md` 数据契约

### 6.1 位置与写入责任

```text
.agent/tasks/<task-slug>/loop-ledger.md
```

| 事件类型 | 写入责任 |
|---|---|
| `REQUEST` | **发起方**记账：提出请求、发起跨角色退回、发起 Contract 修订的一侧 |
| `DANGER` | 触发熔断的角色（发现 `N=2` 且问题仍阻塞的角色） |
| `CLOSE` | 验证问题已真实解决的角色，或执行 Owner 终止决定并持久化的角色 |
| `RESET` | 执行 Owner 明确授权、开启新 segment 的角色（通常是持有该 loop 的角色） |

被请求方、被退回方**不重复记账**。任何角色都只能写当前 Task 目录下的这两个证据文件，不得代写其他角色的 Artifact。

### 6.2 文件结构

```markdown
# Loop Ledger

- Task ID: <task-slug>
- Ledger Version: 1
- Protocol: .agent/specs/WorkflowLoopCircuitBreaker.md

## EV-0001 REQUEST OWNER_DECISION:payment-retry-policy S1 round=1

- task_id: <task-slug>
- event_id: EV-0001
- event_type: REQUEST
- loop_key: OWNER_DECISION:payment-retry-policy
- category: OWNER_DECISION
- segment: S1
- round: 1
- budget: 2
- role: Analyst
- evidence_commit: <7~40 位十六进制 SHA>
- ledger_base: <记账前 origin/<feature> 的完整 SHA>
- anchor: AC-003 / contract.md#failure-semantics
- root_cause: <≤200 字稳定根因摘要>
- request_summary: <向谁请求 / 退回了什么>
- premise_change: NONE
- owner_decision: NONE
- outcome: OPEN
- timestamp: 2026-10-27T12:00:00Z
- seq: 1
```

字段语义：

| 字段 | 必填 | 说明 |
|---|---|---|
| `task_id` | 是 | 必须与 `state.yaml.task_id` 一致 |
| `event_id` | 是 | `EV-` + 至少 4 位十进制序号；同一 Task 内单调递增、永不复用 |
| `event_type` | 是 | `REQUEST` / `DANGER` / `CLOSE` / `RESET` |
| `loop_key` | 是 | §2.1 构造 |
| `category` | 是 | 与 `loop_key` 前缀一致 |
| `segment` | 是 | `S1`、`S2`…… |
| `round` | 是 | `REQUEST` 为本次轮次（1 或 2）；`CLOSE` 为关闭时的 N；`RESET` 为 `0`；`DANGER` 为已消耗轮次（2） |
| `budget` | 是 | 该 segment 允许的 `REQUEST` 总数（或 `UNBOUNDED`） |
| `role` | 是 | 记账角色 |
| `evidence_commit` | 是 | 本次事件锚定的不可变 Evidence Commit（`review.target` / `contract.target` / 当时 feature HEAD 等），只接受 7~40 位十六进制 SHA |
| `ledger_base` | 是 | 追加前的远端 feature HEAD，提供可追溯顺序与「上一事件确实已 push」的证据 |
| `anchor` | 是 | 阻塞锚点（§2.4 要素 1） |
| `root_cause` | 是 | 稳定根因摘要，不含措辞性描述 |
| `request_summary` | 是 | 请求或退回的内容；`CLOSE` / `RESET` 记录解决或授权摘要 |
| `premise_change` | 是 | `NONE` 或具体前提变化 |
| `owner_decision` | 是 | `NONE` 或 Owner 明确决定的引用（决定原文要点 + 时间） |
| `outcome` | 是 | `OPEN` / `CLOSED` / `TERMINATED` / `DANGER` / `RESET` |
| `timestamp` | 是 | ISO-8601 UTC |
| `seq` | 是 | 单调递增整数，与 `event_id` 序号一致 |

### 6.3 Append-only

- 只允许在文件末尾追加新事件块；**不得修改或删除历史事件**（包括修正错别字）。
- 发现历史记录错误时，追加一条新事件说明更正，不得重写历史。
- 再次说明：Engine 只检查文件路径，**不能机械证明** append-only；这条规则靠协议与 Review 保证。

### 6.4 与 `state.yaml` 的关系

- 账本记录历史；`state.yaml` 记录当前机器事实。
- 账本**不得**声明 Gate 结论、不得写入 `review.status`、不得替代 `blocked`。
- 账本显示 `CLOSE` 而 `blocked.active` 仍为 `true` 时，以 `blocked` 为准：恢复尚未完成。

## 7. `danger-report.md`

### 7.1 位置

```text
.agent/tasks/<task-slug>/danger-report.md
```

由触发 DANGER 的角色创建；恢复阶段由执行 Owner 决定的角色**追加** Recovery 章节与 Owner 交互记录。不得删除或重写历史 DANGER 内容。

### 7.2 必需内容

```markdown
# DANGER Report

## 1. Task / Branch
- Task ID: <task-slug>
- Branch: <current-feature>

## 2. Trigger
- 触发角色: <Role>
- 触发时间: <ISO-8601 UTC>
- 触发方式: 协议熔断 / 协议违规（第三轮已发出）

## 3. Loop
- loop_key: <CATEGORY:slug>
- segment: <S1>
- 已消耗轮次: 2
- 预算: 2

## 4. 两轮证据
| Round | Event | Evidence Commit | 请求 / 退回摘要 | 结果 |
|---|---|---|---|---|
| 1 | EV-0001 | <sha> | ... | 未解除 |
| 2 | EV-0003 | <sha> | ... | 未解除 |

## 5. 当前阻塞原因
<准确、可复核；指向具体 Gate / Finding / 文件 / 命令输出>

## 6. 为什么第三轮被阻止
<按 §3 说明 N=2；若第三轮已发出，如实标注协议违规>

## 7. 当前状态（逐字引用 state.yaml）
- contract.status / contract.target:
- review.status / review.target:
- owner.status / owner.review_target:
- delivery.status / delivery.review_target / feature_head / develop_base:
- blocked.active / by / reason:

## 8. Git Evidence
- feature HEAD: <sha>
- origin/<branch>: <sha>
- remote synced: YES / NO

## 9. 需要 Owner 决定的最小问题
<一个问题；给出可选项与每个选项的后果>

## 10. 允许的恢复方式
- A. 解决该问题并 CLOSE
- B. 修改 Contract / AC 等真实前提后 RESET
- C. 终止 Task
- D. 明确批准额外预算（新 segment + 显式预算）

## 11. 未执行的测试或 Gate
| 检查 | 未执行原因 | 风险 |
|---|---|---|

## 12. 结论
<明确写出：不能自动继续；不得 Handoff；不得复用旧 Gate 结果>

## 13. Owner 交互记录
| 时间 | Owner 原话摘要 | 判定 | 处理 |
|---|---|---|---|
| ... | "再看看" | 含糊，不构成授权 | 保持 blocked |
```

### 7.3 诚实性要求

- 不得编造已经完成的验证、已通过的 Gate 或已存在的 Owner 决定。
- 未执行的检查必须列入 §11，不得省略。
- 已通过但已失效的旧 Gate 结果不得写成「仍然有效」。

## 8. DANGER 原子化停机

当 `N=2` 且同一根因仍然阻塞时：

1. **不得**发出第三次请求或再次退回。
2. 追加 `DANGER` 账本事件。
3. 创建或更新 `danger-report.md`（§7）。
4. 将 `state.blocked.active` 设为 `true`。
5. 写入 `blocked.by`（触发角色）与可定位的 `blocked.reason`：

   ```text
   loop_key=<CATEGORY:slug>; segment=<S>; rounds=2; danger=.agent/tasks/<task>/danger-report.md; question=<最小待决问题>
   ```

6. 只提交这三个 Task Artifact：`loop-ledger.md`、`danger-report.md`、`state.yaml`。
7. `push` 并核验远端同步（local HEAD == `origin/<branch>`，远端包含新事件与新报告）。
8. 输出 `RESULT=DANGER`。
9. 停止 Workflow 操作，**不再 Handoff**，不再运行下游 Gate，不再修改实现。

约束：

- 不得借 DANGER 覆盖真实的 Review / Owner / Delivery 结论；这些字段按实际值原样保留。
- 若写盘或 push 出错，报告**无法持久化**的实际状态，输出 `RESULT=BLOCKED`（附 `PERSISTENCE_FAILURE`），**不得**假装 DANGER 已成功记录。
- DANGER 后即使某个旧 Gate 曾经通过，也不得直接复用该结果继续推进。
- 工作区存在其他角色未提交的变更时，不得一并提交、不得 `stash` 丢弃；在 DANGER 报告中如实记录其存在。
- 第 6～7 步的提交类型是 `FORCED_EVIDENCE`：只允许 `loop-ledger.md`、`danger-report.md`、`state.yaml`；push 后必须核验远端包含新事件与新报告。该提交不构成 HANDOFF，也不得前移 `review.target` / `contract.target` / `owner.review_target`。

## 9. 跨 Session 与幂等

- 任何新 Session 开始前，必须先读取 `loop-ledger.md`、`danger-report.md` 与 `state.blocked`；**不得**依据聊天历史或记忆推断轮次。
- Session 重启**不**重置预算、**不**改变 segment、**不**产生新 `loop_key`。
- 幂等约束：
  - `(task_id, event_id)` 唯一；
  - `(loop_key, segment, event_type=REQUEST, round)` 唯一。
- 重复追加被禁止。发现目标事件已存在时，视为「已记账」，不得再写第二条，也不得再发一次同轮请求。
- 崩溃窗口处理：
  - **已记账并 push，但请求未发出**：下一 Session 可以发出该条已记录的请求（仅一次），或追加 `CLOSE` / `RESET`；不得追加新轮次。
  - **请求已发出，但记账未 push**：属于协议违规；恢复持久化后补记并标注 `UNRECORDED_REQUEST`，保守计入已消耗轮次。

## 10. Owner 人工恢复

### 10.1 有效授权

有效授权必须同时具备：

1. 指向具体 `task_id`；
2. 指向具体 `loop_key`（或明确说明覆盖范围）；
3. 明确选择 §10.2 的一种处置；
4. 可追溯（Owner 原话要点 + 时间 + 记录位置）。

以下表达**不构成**授权，不得解除 `blocked`：「继续」「你自己决定」「再试试」「看着办」「应该没问题」。此时只能把 `danger-report.md` §9 的最小问题重新呈现给 Owner，并把 Owner 原话记入 §13 Owner 交互记录。

重新呈现最小问题**不是**新一轮请求，不消耗预算；但不得借反复提问夹带新的决定范围。

### 10.2 四种处置

| Owner 选择 | 账本记录 | 结果 |
|---|---|---|
| 解决该问题并关闭 loop | `CLOSE` + `outcome=CLOSED` + 解决证据 | 清除 `blocked`，相关 Gate 重新验证 |
| 修改 Contract / AC 或其他真实前提 | `RESET` + `premise_change` + `owner_decision` + 新 segment | 清除 `blocked`，从对应角色入口重建结论 |
| 终止 Task | `CLOSE` + `outcome=TERMINATED` | **保持** `blocked.active=true`，不再运行任何 Gate，不再 Handoff |
| 批准额外预算 | `RESET` + 新 segment + 显式 `budget` | 新 segment 内重新从 `N=0` 计数，历史保留 |

`CLOSE` 只适用于「问题已经真实解决」或「Owner 明确撤回该问题」。若仍需要实现改动或进一步调查，必须选择 `RESET`（并给出新预算），**不得用 `CLOSE` 变相增加预算**。由于 `CLOSE` 不重置计数，提前关闭后同一根因再次阻塞会直接回到 `N=2` 并再次 DANGER——这使 `CLOSE` 无法被当作绕过手段。

### 10.3 恢复步骤

1. 读取最新账本、DANGER 报告和 `blocked` 状态。
2. 核对 Owner 授权对应的 Task 与 `loop_key`；不匹配则不得恢复。
3. 记录 Owner 决定与必要的新前提。
4. 按语义追加 `CLOSE` 或 `RESET`（附录 A）。
5. 具备恢复条件后清除 `blocked`（`active=false`；`by` / `reason` 可留空）。
6. `commit` + `push` 状态与证据更新，并核验远端同步。
7. 返回对应角色的正确入口重新验证（§10.4）。
8. 只有相关 Gate 与 Evidence 条件重新满足，才允许继续交接。

**解除 `blocked` 不等于已有 Review 或 Delivery 自动有效。** 旧结论只能由对应角色重新验证后重建。

### 10.4 各角色恢复入口

| 角色 | 恢复入口 |
|---|---|
| Coder | 重新执行 `coder-start` |
| Cleaner | 重新执行 `cleaner-start` |
| Analyst | 按 Contract 流程重新取得必要的 Owner 批准；不得把尚未批准的 Contract 交给 Coder。若 Owner 修改了 AC，先由 TaskBuilder 更新 Task |
| OwnerGate | NORMAL / COMPLEX 使用 `owner-gate-start`；ARCHITECT 先重新执行其承担的 `cleaner-start` V1 结构验证并持久化，通过后再运行 `owner-gate-start` 进入 V2 |
| Deliverer | 重新执行 `delivery-start`；必要时重新绑定 `review_target` / `feature_head` / `develop_base`，再执行 `merge-ready` |
| TaskBuilder | 按 NORMAL / COMPLEX / ARCHITECT 各自的出口规则恢复，不得强行统一使用 `coder-start` |

## 11. 角色触发点与停止规则

| 角色 | 记账触发点（发起前） | 停止规则 |
|---|---|---|
| TaskBuilder | 需要 Owner 澄清会改变 Task 边界的关键业务规则（`OWNER_DECISION`），或需要退回上游修订任务定义 | `N=2` 时不得再次请求同一决定，转 DANGER |
| Analyst | 请求 Owner Contract 决定（`OWNER_DECISION`）、提出 Contract 修订（`CONTRACT_REVISION`）、退回 Coder / Owner（`ROLE_REPAIR`） | `N=2` 时不得再次请求或再次修订，转 DANGER |
| Architect | 请求 Contract 修订、请求 Owner 决定、退回 Analyst（`ROLE_REPAIR` / `CONTRACT_REVISION`）；收到 OwnerGate V1 的结构验证退回不重复记账（由退回方记账） | `N=2` 时不得再次请求或修订，转 DANGER |
| Coder | 请求 Contract 修订、请求 Owner 决定、退回 Analyst（`ROLE_REPAIR` / `CONTRACT_REVISION`） | `N=2` 时不得再次请求；不得自行增加预算或换 `loop_key` 规避 |
| Cleaner | 发出 `CHANGES_REQUIRED` 退回 Coder（`ROLE_REPAIR`）、请求 Contract 修订、请求 Owner 决定审查标准 | `N=2` 时不得再次退回，转 DANGER |
| OwnerGate | 请求 Owner 核心逻辑决定（`OWNER_DECISION`） | `N=2` 时不得再次请求同一决定，转 DANGER |
| Deliverer | 退回 Coder / Analyst（`ROLE_REPAIR`）、请求 Owner 决定交付范围或环境例外（`OWNER_DECISION`） | `N=2` 时不得再次退回或请求，转 DANGER |

接收方（被请求 / 被退回的角色）不记账，也不因收到请求而消耗预算。

## 12. 与 CLEAN / Delivery Freshness / 历史兼容

- `loop-ledger.md` 与 `danger-report.md` 在 Review-neutral 与 Delivery-neutral 白名单中均为**精确文件名 + 当前 Task 根目录**：CLEAN 之后或 Delivery PASS 之后记录循环，不会破坏 Tail 有效性。
- 该豁免仅限这两个文件名与当前 Task 根目录。生产代码、测试、Contract、Task、Design、其他 Task 的同名文件、本 Task 子目录的同名文件仍然是 substantive change，仍会使旧 CLEAN / Delivery 失效。
- Git diff 禁用 rename detection：rename 会同时暴露源路径与目标路径，不能把实质变更伪装成 Neutral 目标路径。
- DANGER 恢复后仍必须验证 Delivery Freshness：`develop_base` 必须等于当前 `origin/<integration-branch>`；集成分支前进后旧 Delivery Evidence 依然失效。
- 历史兼容：本规范只约束生效之后的新事件。没有账本的历史 Task 不因缺失这两个文件而被判无效，也不要求补记账本。

## 附录 A：事件记录模板

### A.1 REQUEST

```markdown
## EV-0001 REQUEST <CATEGORY:slug> <segment> round=<1|2>

- task_id: <task-slug>
- event_type: REQUEST
- loop_key: <CATEGORY:slug>
- category: <CATEGORY>
- segment: <segment>
- round: <1|2>
- budget: <2|UNBOUNDED>
- role: <Role>
- evidence_commit: <sha>
- ledger_base: <sha>
- anchor: <AC/INV/Gate/file>
- root_cause: <stable summary>
- request_summary: <request or return>
- premise_change: NONE
- owner_decision: NONE
- outcome: OPEN
- timestamp: <ISO-8601>
- seq: <n>
```

### A.2 DANGER

同 A.1，差异：

```text
event_type: DANGER
round: <已消耗轮次，通常为 2>
budget: <当前 segment 预算>
outcome: DANGER
request_summary: <不发起第三轮的原因与最小待决问题>
```

### A.3 CLOSE

同 A.1，差异：

```text
event_type: CLOSE
round: <关闭时的 N>
budget: <当前 segment 预算>
outcome: CLOSED | TERMINATED
request_summary: <解决证据或终止依据>
owner_decision: <Owner 决定引用，或 NONE（角色自行验证解决时）>
```

### A.4 RESET

同 A.1，差异：

```text
event_type: RESET
segment: <新的 S(n+1)>
round: 0
budget: <新预算，如 2 或 UNBOUNDED>
premise_change: <具体前提变化，或 NONE（仅追加预算时）>
owner_decision: <Owner 明确授权原文要点 + 时间>
outcome: RESET
```
