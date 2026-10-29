# Workflow Git Lifecycle 规范（V3.2.0）

> 适用对象：所有已激活的 Workflow Role Session——TaskBuilder、Analyst、Architect、Coder、Cleaner、OwnerGate、Deliverer。
> 规范地位：本文是「角色何时可以 Stage / Commit / Push / HANDOFF」以及「失败、DANGER、Registry 等强制证据如何落 Git」的**唯一规范事实源**。
> 与其它规范的关系：角色职责与 Gate 语义见 `.agent/specs/AgentCollaborationSpecification.md`、`.agent/specs/Five-AgentResponsibilityBoundary.md` 与 `docs/design/agent-workflow.md` §12；跨 Session HANDOFF 的内容契约见 `.agent/specs/WorkflowHandoffContract.md`；循环熔断与 DANGER 触发条件见 `.agent/specs/WorkflowLoopCircuitBreaker.md`。本文不复制这些规范的判定细节，只固定 Git 时机与完成条件。

## 0. 核心要求与能力边界

### 0.1 唯一要求

**每个 Role 自行用 Git 保存自己的成果；只有达到合法交接条件，或完成一个被明确定义且可独立验证的交付阶段时，才允许提交。**

这条要求包含四个可分别判定的问题：

1. 现在完成的是**一个角色阶段**，还是只是「暂时保存一下」？
2. 这次提交属于哪一种**已定义的 Commit 类型**？
3. 这次 Push 是否指向**正确的远端与分支**，并且远端真的接受了？
4. 下一角色入口 Gate 是否 `PASS`，因而可以 HANDOFF？

四个问题任一无法给出可复核答案时，**不得** Commit、Push 或 HANDOFF。

### 0.2 这不是「禁止一切本地提交」的机器保证

`workflow-check` 是只读 Gate Validator：它不拦截 `git commit` / `git push`，也不认证 Session 身份。因此：

- 本文的 Stage 范围、Commit 类型、Push 目标、HANDOFF 条件**主要是协议约束（SPEC_ONLY）**，由 Role Prompt、Review 与真实 Git 证据执行；
- 机器强制的事实只有：`blocked.active=true` 时五个 Gate 一律 FAIL；Review Target / Contract Target / Delivery Freshness / Neutral Tail 的机械校验；`handoffReady` 的 clean + local == `origin/<branch>` 检查；
- 任何角色不得把本文描述为「Engine 已经禁止未授权提交」。真实的防线是：**用 Gate 与 Git 证据识别错误 HANDOFF**，而不是假装能阻止所有直接 Git 写操作。
- 本文不引入 Git Hook、不修改仓库全局配置、不拦截开发者日常 Git 操作；只提供只读自检与 Gate 事实。

### 0.3 与「两阶段 Evidence」的关系

A1/C1/D1、A2/C2/D2 不是「任意进度提交」的许可证，而是既有 Evidence 契约的**技术必然结果**：后一个 Commit 必须引用前一个不可变 Commit SHA。完整冲突分析见 §11，这里只给结论：

- A1 / C1 / D1 是**已定义、可独立验证的阶段交付快照**，允许在角色整体交接之前提交，但必须满足 §4.3 的全部完成条件；
- A2 / C2 / D2 是**只写 `state.yaml` 的引用提交**，不允许混入任何实质变更；
- 未完成、未验证的临时工作不属于上述任何类型，一律禁止提交。

## 1. 状态模型

```text
WORKING
  │  角色职责完成 + 本阶段验证真实通过
  ▼
DELIVERABLE_VERIFIED
  │  显式 stage 本角色 owned artifacts，staged scope 核对通过
  ▼
COMMITTED
  │  普通 push 当前 feature branch 成功
  ▼
PUSHED
  │  git ls-remote 核验远端 SHA == 本地 HEAD
  ▼
REMOTE_VERIFIED
  │  下一角色入口 Gate PASS
  ▼
GATED
  │  输出 HANDOFF（NEXT_ROLE / NEXT_TARGET / NEXT_ACTION）
  ▼
HANDOFFED
```

约束：

- 箭头不可跳过、不可颠倒。**先 Gate 后 Push** 不成立：Gate 的 `handoffReady` 本身就要求已 Push 且本地 == 远端。
- `COMMITTED` 与 `PUSHED` 之间不是「完成」；只有 `GATED` 之后才允许输出 HANDOFF。
- 失败不倒退为 `WORKING` 的「重新来过」，而是进入带原因的失败态：`PUSH_FAILED`、`REMOTE_MISMATCH`、`GATE_FAILED`、`DIRTY`、`DANGER_STOPPED`。失败态下的允许动作见 §8。

### 1.1 三种「完成」必须区分

| 概念 | 含义 | 是否允许 HANDOFF |
|---|---|---|
| `ROLE_TASK_COMPLETE` | 本角色职责全部完成（如 Coder 的 AC 实现、测试与自测） | 否，还缺 Git 与 Gate |
| `STAGE_DELIVERABLE_VERIFIED` | 一个可独立验证的阶段交付成立（A1/C1/D1、CLEAN、ACCEPT、PASS 等） | 否，阶段结论不是 Session 完成 |
| `SESSION_COMPLETE` | `WORKING → … → GATED` 全部通过，且下一角色入口 Gate PASS | 是 |

「测试跑过了」「报告写好了」「已经 commit 了」都不等于 `SESSION_COMPLETE`；`CLEAN`、`ACCEPTED`、`PASS` 也只是业务结论，不等于本轮结束。

## 2. Commit 类型

只允许以下六类 Commit。任何无法归类的提交都视为 `PROGRESS`，禁止。

| 类型 | 内容范围 | 允许时机 | 完成条件（可复核） | 是否可 HANDOFF |
|---|---|---|---|---|
| `BOOTSTRAP` | `.agent/tasks/<task>/task.md`、`state.yaml`（及该任务初始 Artifact） | TaskBuilder 初始化任务，或经 Owner 授权的 `TB-BRANCH-1` | 初始化字段符合路线；TB-BRANCH-1 预检 11 项全过；staged 只含任务目录 | 否（提交后可出站，出站完成才 HANDOFF） |
| `EVIDENCE` | A1：`contract.md` + 必要的 `docs/design/*`；C1：生产代码 + 测试；D1：结构骨架 + `architecture.md` + 结构测试 | 阶段交付已完整实现，且本角色验证已真实执行 | 适用测试/构建/静态检查已执行并记录；无未完成占位（结构占位必须标 `NOT_EXECUTED`）；不含 `state.yaml`；不含其他角色 artifacts | 否 |
| `METADATA` | **仅** `.agent/tasks/<task>/state.yaml` | Evidence Commit 已存在，且其完整 SHA 可解析 | `git diff --cached --name-only` 只有 `state.yaml`；引用字段为完整 40 位 SHA；字段转移与角色一致 | 否（与 EVIDENCE 组成 Review Request） |
| `ROLE_ARTIFACT` | 本角色 owned artifacts + `state.yaml` 对应字段：Cleaner `findings.md`/`core-logic.md`/`review.*`；OwnerGate `owner-decision.md`/`owner.*`（V1 另有 `findings.md`/`core-logic.md`/`review.*`）；Deliverer `delivery.md`/`delivery.*` | 审查 / 决策 / 验收结论真实产生，且已按结论完成持久化 | 结论有真实证据；不修改 `review.target`（Cleaner CLEAN、ARCHITECT V1 均保持 C1/D1）；不写不属于本角色的字段 | 是（CLEAN / ACCEPT / PASS 分支，仍需 Gate PASS） |
| `FORCED_EVIDENCE` | `loop-ledger.md`、`danger-report.md`、`state.yaml`（`blocked` / 恢复字段） | write-ahead 记账；DANGER 停机；Owner 授权恢复 | 事件字段完整、append-only；DANGER 只提交这三类文件；push 后核验远端包含新事件 | 否（DANGER 明确禁止 Handoff） |
| `REGISTRY_ONLY` | `.agent/workflow.yaml` 已声明的 Registry 文件 | Contract APPROVED 后落实 `RESERVED` | staged/commit diff 只含已声明 Registry 文件；push `origin <integration_branch>`；远端核验资源已落地 | 否 |

补充：

- `EVIDENCE` / `METADATA` / `ROLE_ARTIFACT` 一次提交只允许承担一个角色阶段，不允许把「Cleaner 的 findings + Coder 的修复代码」合并提交。
- Commit message 遵循 Conventional Commit + 中文摘要（`docs/design/agent-workflow.md` §23）；message **不得**把未验证内容写成已完成。
- 已 Push 的 Commit 不得 `amend` / `rebase` / `force push` 改写，即使 message 不完全符合建议。Git 事实优先。

## 3. Stage 规则

### 3.1 允许

- 只 stage **本角色 owned、且属于本次 Commit 类型**的明确路径：`git add <path> ...`；
- stage 前先运行 `git status --short` 与 `git diff`，确认没有其他角色的未提交修改或未跟踪文件；
- stage 后必须运行 `git diff --cached --name-status` 逐项核对。

### 3.2 禁止

- `git add -A`、`git add .`、`git add -u`；
- stage 生产代码到 `METADATA` / `FORCED_EVIDENCE` / `REGISTRY_ONLY`；
- stage `state.yaml` 到 `EVIDENCE`（`BOOTSTRAP` 与 `ROLE_ARTIFACT` 除外）；
- stage 其他角色的 artifacts、Registry、`task.md`、`contract.md`、`docs/design/*`（除该角色明确拥有且属于本 Commit 类型时）；
- 用 `git stash` / `git clean` / `git reset --hard` 处理现场来「让 staged 看起来干净」。

### 3.3 发现他人修改

发现工作区存在来源不明或其他角色的修改：

1. 不得顺便提交，不得丢弃，不得覆盖；
2. 若不影响本角色 Commit 范围 → 保留现场，提交时只 stage 自己的路径，并在 HANDOFF 中说明其存在；
3. 若影响本角色交付或无法安全区分 → `BLOCKED`（DANGER 场景按 `.agent/specs/WorkflowLoopCircuitBreaker.md` §8 记录其存在）。

## 4. 七角色完成条件与 Git 动作

### 4.1 总表

| 角色 | 职责完成条件（`ROLE_TASK_COMPLETE`） | 本角色 Commit 类型 | Push | 出口 Gate | HANDOFF |
|---|---|---|---|---|---|
| TaskBuilder | `task.md` 的 Goal/Scope/Out of Scope/AC/Review Baseline/Complexity/Initial Route 完整；`state.yaml` 按路线初始化；执行 `TB-BRANCH-1` 时预检全过且只提交任务 Artifacts | `BOOTSTRAP` | 当前 feature | NORMAL：`coder-start`；COMPLEX/ARCHITECT：无 Gate，只读同步自检 | Coder / Analyst |
| Analyst | Contract 内容已获 Owner 明确批准；`Design Impact=NEW/UPDATE` 时 Design 已更新；共享资源已在 `origin/<integration_branch>` 真实 `RESERVED/ACTIVE` | `EVIDENCE`（A1）→ `METADATA`（A2）；如有资源另有 `REGISTRY_ONLY` | 当前 feature；Registry-only 例外 push integration branch | `coder-start` | Coder / Architect |
| Architect | 结构骨架、`architecture.md`、结构测试完成；构建/格式化/静态检查/结构测试真实执行；未实现业务行为标 `NOT_EXECUTED` | `EVIDENCE`（D1）→ `METADATA`（D2） | 当前 feature | `cleaner-start` | OwnerGate V1（新 Session） |
| Coder | AC 行为完整；正常/失败/边界测试齐全；关键不变量有可区分测试；格式化/测试/静态检查/构建已执行；无无关修改 | `EVIDENCE`（C1）→ `METADATA`（C2） | 当前 feature | `cleaner-start` | Cleaner |
| Cleaner | 全量相关 Diff 已审查；AC/Contract 逐项核对；独立验证已执行；Finding 与核心验证卡已落盘；结论真实 | `ROLE_ARTIFACT` | 当前 feature | CLEAN：`owner-gate-start`；CHANGES_REQUIRED：无 Gate | OwnerGate / Coder |
| OwnerGate | 入口 Gate PASS；核心机制已向 Owner 解释并取得明确决定；或（ARCHITECT V1）独立结构验证已完成且结论真实 | `ROLE_ARTIFACT`（V1 与 V2 分两次） | 当前 feature | ACCEPT：`delivery-start`；REJECT：无 Gate；V1：`owner-gate-start` | Deliverer / 退回对象 |
| Deliverer | 交付版本与 `review.target` 一致；三 SHA 已绑定；真实环境验收已执行；未执行项已登记 | `ROLE_ARTIFACT` | 当前 feature | PASS：`merge-ready`；FAIL/BLOCKED：无 Gate | Owner / 退回对象 |

### 4.2 角色特有的顺序要求

- **TaskBuilder**：初始化 → 显式 stage 任务目录 → commit → push → 核验远端 → （NORMAL）`coder-start` → HANDOFF。`COMPLEX` / `ARCHITECT` 在 `contract.status=PENDING` 时**不得**运行 `coder-start`，也不得为了让它通过把 Contract 改成 `NOT_REQUIRED`。
- **Analyst**：Registry-only commit（如需）→ A1 → A2 → push → 远端核验 → `coder-start` → HANDOFF。A1 不得包含 Registry 文件；Registry 文件只出现在 `origin/<integration_branch>` 的 `REGISTRY_ONLY` commit 中。
- **Architect**：D1 → D2 → push → 远端核验 → `cleaner-start` → HANDOFF。`architecture.md` 属于实质结构事实，必须进入 D1；D1 之后修改它必须形成新的 D1。
- **Coder**：C1 → C2 → push → 远端核验 → `cleaner-start` → HANDOFF。C1 是 Cleaner 的审查对象；C2 不得混入业务代码。
- **Cleaner**：入口 `cleaner-start` PASS → 审查 → 写 `findings.md` / `core-logic.md` / `review.*` → commit → push → 远端核验 → CLEAN 时运行 `owner-gate-start`。`review.target` 保持 C1 不变。
- **OwnerGate**：
  - NORMAL / COMPLEX：`owner-gate-start` PASS → 解释机制（`WAITING_FOR_OWNER_DECISION`，不是 Handoff）→ Owner 明确决定 → 持久化 `owner-decision.md` / `owner.*` → commit → push → 远端核验 → ACCEPT 时 `delivery-start`；
  - ARCHITECT：V1（`cleaner-start` → 验证 → `findings.md` / `core-logic.md` / `review.*` → commit → push → `owner-gate-start`）→ V2（Owner 决策 → `owner-decision.md` / `owner.*` → commit → push → `delivery-start`）。**V1 与 V2 必须分两次出站**，不得合并提交，也不得在 V1 未落 Git 时请求 Owner 决策。
- **Deliverer**：`delivery-start` PASS → 验收 → `delivery.md` / `delivery.*`（三 SHA）→ commit → push → 远端核验 → PASS 时 `merge-ready`。

### 4.3 中间 Evidence Commit 的严格完成条件

在需要承载后续审查 / 批准对象的提交中，`EVIDENCE`（A1/C1/D1）是唯一允许早于角色整体交接的类型。必须同时满足：

1. **阶段边界明确**：该 Commit 对应 Task/Contract 中一个可命名的阶段（Contract 定稿 / 实现完成 / 结构成立），不是「先存一下」；
2. **验证已真实执行**：C1 要求适用的格式化、测试、静态检查、构建已运行；D1 要求结构测试、构建、依赖方向验证已运行；A1 要求 Contract 内容与 Owner 决定一致，且 Design 已按批准内容更新（可在 A1 或 A2 之前的独立提交中完成）；
3. **失败未隐藏**：未执行的检查标记 `NOT_VERIFIED` / `NOT_EXECUTED` 并说明原因；不得把失败写成通过；
4. **范围精确**：只含该阶段 owned artifacts；不含 `state.yaml`；不含其他角色 artifacts；不含无关重构；
5. **工作区可解释**：`git status --short` 中没有本角色未提交的实质修改；其他人的修改按 §3.3 处理；
6. **不宣称完成**：Commit message、交接说明与后续 HANDOFF 不得把 A1/C1/D1 描述为「已审查」「已接受」「可交付」；它只是**审查/批准前的不可变快照**；
7. **后续 Metadata 可解析**：一旦提交，SHA 即不可变；A2/C2/D2 必须引用该完整 SHA，后续任何实质修改都必须形成新的 Evidence Commit。

不满足任一条 → 停在 `WORKING`，不得提交；无法在本角色 authority 内满足 → `BLOCKED`。

## 5. Push 规则

### 5.1 允许

- 普通 push 当前 feature branch：`git push origin <current-feature>`（不带 `--force` / `--force-with-lease`）；
- Analyst 的 `REGISTRY_ONLY`：普通 push `origin <integration_branch>`，且只允许该类型；
- TaskBuilder `TB-BRANCH-1`：`git push -u origin <new-feature>`（普通 push）。

### 5.2 禁止

- `push --force` / `--force-with-lease` / 删除远端分支 / 改写已发布历史；
- push `main` / `master` / shared integration branch（`REGISTRY_ONLY` 以外）；
- push 到与 `task.md` / `TB-BRANCH-1` 记录不一致的远端或分支；
- push 后再 `amend` 或补一个「修正」提交来掩盖 scope 错误（scope 错误应在提交前发现；已 push 的错误按 §8 处理并如实报告）。

### 5.3 远端核验

Push 成功不等于远端已同步。必须：

```text
git rev-parse HEAD
git ls-remote --heads origin refs/heads/<current-feature>
git status --short
```

三者满足：远端 SHA == 本地 HEAD；工作区为空。`origin/<branch>` 只是本地 remote-tracking ref，**不能单独作为远端事实**（见 §11 `SRC-07`）。

## 6. Gate 与 Git 的实际顺序

### 6.1 标准顺序（所有角色）

```text
1. 入口 Gate（若该角色/阶段有入口 Gate）
2. 完成职责与验证
3. 写本角色 artifacts
4. git status --short / git diff
5. 显式 stage 本角色 artifacts
6. git diff --cached --name-status 核对 scope
7. commit（类型见 §2）
8. 普通 push 当前 feature
9. git ls-remote 核验远端 SHA + worktree clean
10. 运行下一角色入口 Gate
11. Gate PASS 后才输出 HANDOFF
```

Gate 必须在 Push 之后运行，因为五个 Gate 的 `handoffReady` 要求 clean 且 local == `origin/<branch>`。**不得**为了「先确认 Gate 能过」而伪造远端引用或跳过 Push。

### 6.2 角色顺序摘要

```text
TaskBuilder：Commit + Push + 远端核验 →（NORMAL）coder-start → HANDOFF
Analyst：A1 → A2 → Push + 核验 → coder-start → HANDOFF
Architect：D1 → D2 → Push + 核验 → cleaner-start → HANDOFF
Coder：C1 → C2 → Push + 核验 → cleaner-start → HANDOFF
Cleaner：commit → Push + 核验 →（CLEAN）owner-gate-start → HANDOFF
OwnerGate（NORMAL/COMPLEX）：owner-gate-start → Owner 决定 → commit → Push + 核验 →（ACCEPT）delivery-start → HANDOFF
OwnerGate（ARCHITECT）：cleaner-start → V1 commit → Push + 核验 → owner-gate-start → V2 → commit → Push + 核验 → delivery-start → HANDOFF
Deliverer：commit → Push + 核验 → merge-ready → HANDOFF
```

### 6.3 Gate 通过不等于业务接受

Gate `PASS` 只证明机器事实满足约束：它不是 `CLEAN`、不是 Owner `ACCEPTED`、不是 `PASS`，也不是 HANDOFF 本身。HANDOFF 仍需本角色职责完成、证据落盘、Push 与远端核验、Gate PASS 同时成立。

## 7. HANDOFF 条件

正常 HANDOFF 必须同时满足：

1. 当前角色职责全部完成（§4.1 对应行）；
2. owned artifacts 已持久化；
3. Commit 类型合法、staged scope 精确（§2、§3）；
4. 应 push 的已 push；
5. 远端 SHA 已用 `git ls-remote` 核验等于本地 HEAD；
6. `git status --short` 为空；
7. 下一角色入口 Gate `PASS`；
8. `NEXT_ROLE` / `NEXT_TARGET` / `NEXT_ACTION` 明确，且不存在 `blocked.active=true` 或 DANGER 停机。

以下任一情况**禁止**输出 HANDOFF：

- 有未提交的 owned artifacts，或工作区 dirty；
- Push 失败 / 远端未同步 / 远端 SHA 与本地不一致；
- Gate `FAIL`、`ERROR`，或 Gate 尚未运行；
- 只有业务结论（`CLEAN` / `ACCEPTED` / `PASS`）而没有完成 Git 收尾；
- 只有 Review Metadata 的意图，但没有 C2/D2/A2；
- `blocked.active=true`（DANGER 或 BLOCKED）；
- 试图通过切换角色 / 新 Session / 新分支绕过上述条件。

退回（`CHANGES_REQUIRED` / Owner `REJECT` / Deliverer `FAIL`）也必须 Commit + Push + 远端核验，但不运行下一角色入口 Gate，也不得把退回写成「完成」。

### 7.1 明确禁止

| 禁止行为 | 规范落点 |
|---|---|
| 没完成测试就把临时工作冒充完成的 C1（或 D1 / A1） | §4.3 条件 2/3；§2 `EVIDENCE` |
| Review 元数据未准备好就宣称 Coder HANDOFF | §4.2；§4.3；§7 |
| Push 失败后宣称远端已同步 | §5.3；§8.2；§9 |
| Gate `FAIL` 后宣称已经交接 | §7；§8.1 |
| 未满足角色职责完成条件就让下一 Role 接手 | §4.1；§7 |
| 任意扩大 Git staging 范围（`git add -A` / 顺带提交他人修改） | §3.1/§3.2 |
| 用修改旧 Evidence SHA 的方式掩盖问题 | §8.1；§9；§15 |

## 8. 失败处置

### 8.1 矩阵

| 情况 | 立即状态 | 允许动作 | 禁止动作 | 恢复条件 |
|---|---|---|---|---|
| Push 被拒（non-fast-forward） | `PUSH_FAILED` | `git fetch`；读取远端实际 SHA；确认是否有他人提交；报告实际状态 | force / force-with-lease / rebase 已发布历史 / 宣称 `REMOTE_SYNCED=YES` | 远端与本地关系明确，且在不改写共享历史的前提下完成同步；否则 `BLOCKED` |
| Push 网络 / 认证失败 | `PUSH_FAILED` | 保留本地 commit；报告命令与错误；可重试一次普通 push | 改用 force、删除远端分支、跳过 push 宣布完成 | 普通 push 成功且 `ls-remote` 等于本地 HEAD |
| Push 后远端 SHA != 本地 | `REMOTE_MISMATCH` | 停止；`git fetch`；调查远端被谁推进；报告双方 SHA | 直接 force 覆盖；修改旧 Evidence SHA；宣称已同步 | 远端事实与本地一致，或 Owner 明确处置 |
| Gate `FAIL` | `GATE_FAILED` | 保持 Session；修本角色职责范围内的问题；重新 commit/push/gate | 删除或改写 `review.target` / `contract.target` / `owner.review_target` 掩盖问题；把 FAIL 写成 PASS；结束 Session | 下一角色入口 Gate `PASS` |
| Gate `ERROR`（exit 2） | `GATE_FAILED` | 修配置 / 环境 / 输入错误后重跑 | 把 ERROR 当作 PASS；手工伪造 `origin/<branch>` | Gate 正常返回 `PASS` |
| 工作区 Dirty | `DIRTY` | 识别归属；自己的文件按当前 Commit 类型收尾；他人文件保留现象并报告 | `git add -A`；`git stash` 丢弃；`git clean`；提交他人修改 | 工作区 clean，且 staged scope 合法 |
| Review Target 不存在 / 不在 feature 历史 | `GATE_FAILED` | 重新形成新的 Evidence Commit 与 metadata | 修改旧 SHA 使其「看起来」合法；用分支名 / `HEAD` 顶替 | 新 Evidence Commit 可解析且是 feature HEAD 祖先 |
| DANGER（两轮耗尽） | `DANGER_STOPPED` | 追加 `DANGER`、写 `danger-report.md`、置 `blocked.active=true`、提交三类证据、push、核验、输出 `RESULT=DANGER` | 第三次请求；Handoff；继续实现；删除历史事件 | Owner 明确按熔断规范 §10 选择 `CLOSE` / `RESET` / 终止 / 新预算 |
| BLOCKED（非 DANGER） | `BLOCKED_STOPPED` | 写入真实阻塞原因（若需要持久化），提交并核验；请求 Owner / 上游 | 把「需要 Owner 一个小动作」升级成 BLOCKED Handoff；伪造完成 | 阻塞条件真实解除，按对应角色入口重新验证 |

### 8.2 Push 失败后的 HANDOFF

Push 失败时**没有**合法 HANDOFF。必须：

1. 不结束 Session；
2. 不把本地 commit 当作远端事实；
3. 不把 `git log` 的本地 HEAD 作为 `REMOTE_SYNCED=YES` 的证据；
4. 继续修复 push（不改写历史），或输出 `BLOCKED` / `RESULT=DANGER`（如已触发熔断）。

### 8.3 Gate FAIL 后的证据

Gate FAIL 不要求为了「记录失败」而额外 commit；但不得删除、回滚或改写已提交的 Evidence。若失败原因是本角色证据缺失，应补证据并形成新的合法 Commit；若失败原因超出 authority，输出 `BLOCKED` 或按熔断规范发起请求（write-ahead 后）。

## 9. 证据保存边界

| 场景 | 必须落 Git 的内容 | 不得做 |
|---|---|---|
| 正常成功阶段 | `EVIDENCE` / `METADATA` / `ROLE_ARTIFACT` + 远端核验结果 | 把 A1/C1/D1 说成已审查；跳过 Gate |
| Cleaner `CHANGES_REQUIRED` | `findings.md`（真实 Finding）、`review.status=CHANGES_REQUIRED`、`review.target` 保持 C1 | 写 `CLEAN`；删除 Finding；复用旧结论 |
| Owner `REJECT` | `owner-decision.md`、`owner.status=REJECTED`、真实理由与退回对象 | 写 `ACCEPTED`；运行 `delivery-start`；清除 `blocked` |
| Deliverer `FAIL` / `BLOCKED` | `delivery.md`、真实 `delivery.status`、证据与未执行项 | 保留旧 `PASS`；写「可集成」；伪造三 SHA |
| DANGER | `loop-ledger.md` 的 `DANGER` 事件、`danger-report.md`、`blocked.active=true` | 提交业务代码；Handoff；删除或重写历史事件 |
| write-ahead | `loop-ledger.md`（必要时 `state.yaml`） | 先发请求后补记；把本地写入当已记账 |
| Registry-only | 已声明 Registry 文件 + 正常 push `origin <integration_branch>` | 混入业务文件；force；把 Reservation 写在 feature 里当已授权 |
| Push 失败 | 不新增「成功」证据；如实报告本地/远端 SHA | 在 `state.yaml` / `delivery.md` / HANDOFF 写 `REMOTE_SYNCED=YES` |
| Gate FAIL | 保持现有证据；必要时补真实证据 | 修改旧 Evidence SHA；把 Gate 输出改写成 PASS |

## 10. 例外矩阵（可审计）

> 「例外」只表示**允许在角色整体交接前发生**，不表示放宽完成条件。每一行都必须同时满足 §2 的类型条件与 §4.3 的中间 Evidence 条件。

| 情况 | 是否允许 Commit | 是否允许 Push | 是否允许 HANDOFF | 必须满足的条件 | 真实技术理由 |
|---|---|---|---|---|---|
| TaskBuilder Bootstrap（NORMAL / COMPLEX / ARCHITECT） | 是（`BOOTSTRAP`） | 是（仅当前 feature） | 是，出站完成后 | 任务 Artifact 完整；`state.yaml` 按路线初始化；`TB-BRANCH-1` 预检全过（如执行）；staged 只含任务目录；NORMAL 需 `coder-start` PASS | 任务边界本身是一个可独立验证的交付阶段；无此提交，下游角色没有稳定事实源 |
| Analyst A1（Contract Evidence） | 是（`EVIDENCE`） | 允许（可与 A2 一并 push） | 否 | Contract 内容与 Owner 明确批准一致；Design 已更新；不含 Registry / 业务代码 / `state.yaml`；A1 SHA 已记录 | `contract.target` 必须是不可变 Commit；批准结论需要可引用的内容快照 |
| Analyst A2（Approval Metadata） | 是（`METADATA`） | 是（当前 feature） | 是，`coder-start` PASS 后 | 仅 `state.yaml`；`contract.status=APPROVED`；`contract.target=A1 完整 SHA`；资源已在 integration branch 验证；工作区 clean、远端同步 | 后一提交必须引用前一不可变 SHA；A2 是唯一的批准事实载体 |
| Coder C1（Implementation Evidence） | 是（`EVIDENCE`） | 允许 | 否 | AC 实现完成；适用测试/构建/静态检查已执行；无未完成占位；不含 `state.yaml`；不含其他角色 artifacts | Cleaner 必须审查一个不可变实现快照，而不是移动的 working tree |
| Coder C2（Review Metadata） | 是（`METADATA`） | 是（当前 feature） | 是，`cleaner-start` PASS 后 | 仅 `state.yaml`；`review.status=PENDING`；`review.target=C1 完整 SHA`；C2 无业务代码 | `review.target` 不能由 C1 自身写入；两阶段是 SHA 自引用的唯一可行解 |
| Architect D1（Structure Evidence） | 是（`EVIDENCE`） | 允许 | 否 | 结构骨架 + `architecture.md` + 结构测试；构建/格式/静态检查/结构测试真实执行；未实现业务标 `NOT_EXECUTED`；不含 `state.yaml` | 结构成果必须成为可独立复核的不可变快照 |
| Architect D2（Review Metadata） | 是（`METADATA`） | 是（当前 feature） | 是，`cleaner-start` PASS 后 | 仅 `state.yaml`；`review.status=PENDING`；`review.target=D1 完整 SHA` | 同上：D1 不能引用自己的 SHA |
| Cleaner `CLEAN` | 是（`ROLE_ARTIFACT`） | 是（当前 feature） | 是，`owner-gate-start` PASS 后 | 全量审查完成；`findings.md` / `core-logic.md` 落盘；`review.status=CLEAN`；`review.target` 仍为 C1；Tail 仅 neutral | CLEAN 是对 C1 的结论，Cleaner 自己的提交不能前移审查对象 |
| Cleaner `CHANGES_REQUIRED` | 是（`ROLE_ARTIFACT`） | 是（当前 feature） | 是（退回 Coder；不经过入口 Gate） | 真实 Finding + `review.status=CHANGES_REQUIRED`；`review.target` 保持 C1；push 后核验 | 失败证据必须能被下一个 Session 从 Git 恢复，否则退回不可审计 |
| OwnerGate V1（ARCHITECT 独立结构验证） | 是（`ROLE_ARTIFACT`） | 是（当前 feature） | 是（仅进入同 Session 的 V2 阶段，不是跨 Session 交接；仍需 `owner-gate-start` PASS） | `cleaner-start` PASS；D1 属于 feature 历史；`findings.md` / `core-logic.md` / `review.status` 落盘；CLEAN 时 `review.target` 保持 D1；push 后才能运行 `owner-gate-start` | V1 是独立验证阶段，必须先有稳定证据，Owner 决策才有对象 |
| OwnerGate `ACCEPT` | 是（`ROLE_ARTIFACT`） | 是（当前 feature） | 是，`delivery-start` PASS 后 | Owner 明确 `ACCEPT`；`owner-decision.md`；`owner.status=ACCEPTED`；`owner.review_target==review.target`；push 后核验 | Owner 决定必须在交付验收前成为 Git 事实 |
| OwnerGate `REJECT` | 是（`ROLE_ARTIFACT`） | 是（当前 feature） | 是（退回 Coder / Architect / Analyst；不运行 `delivery-start`） | 真实拒绝理由与退回对象；`owner.status=REJECTED`；`owner.review_target` 绑定当时 review target | 拒绝也是决策事实，必须可追溯，且不能被写成接受 |
| Deliverer `PASS` | 是（`ROLE_ARTIFACT`） | 是（当前 feature） | 是，`merge-ready` PASS 后 | 三 SHA 绑定（`review_target` / `feature_head` / `develop_base`）；真实环境验收；未执行项登记；push 后核验 | 交付验收依赖不可变版本与集成分支基线，必须固化 |
| Deliverer `FAIL` / `BLOCKED` | 是（`ROLE_ARTIFACT`） | 是（当前 feature） | 是（退回；不运行 `merge-ready`） | 真实 `delivery.status` 与证据；不保留旧 `PASS`；push 后核验 | 失败结论同样必须可恢复，不能只存在于聊天 |
| Registry-only（Analyst） | 是（`REGISTRY_ONLY`，在 integration branch） | 是（仅 `origin <integration_branch>`） | 否 | Contract APPROVED；staged/commit diff 只含 `.agent/workflow.yaml` 已声明 Registry 文件；push 后重新读取远端确认 `RESERVED/ACTIVE` | 共享资源竞争要求先在实际集成分支落地，属于唯一 develop 写例外 |
| loop-ledger write-ahead | 是（`FORCED_EVIDENCE`） | 是（当前 feature） | 否 | 事件字段完整；append-only；只 stage `loop-ledger.md`（必要时 `state.yaml`）；**先记账并 push 成功，再发请求** | 若不先落盘，请求无法被下一 Session 复核，轮次会被重算或丢失 |
| DANGER / BLOCKED 证据 | 是（`FORCED_EVIDENCE`） | 是（当前 feature） | 否（`RESULT=DANGER`） | DANGER：ledger + report + `blocked.active=true` 三类文件；BLOCKED：真实原因；push 后核验；失败则如实报告 `PERSISTENCE_FAILURE` | 停机事实必须比后续会话更持久；否则 DANGER 可被重启绕过 |
| Push 失败 / Gate FAIL / Dirty | 否（不产生「完成」提交） | 否（直到修复） | 否 | 见 §8；已提交内容不得改写；不伪造远端事实 | 失败不是交付阶段；任何「补一个提交掩盖失败」都会破坏 Evidence 语义 |

## 11. STRICT_REQUIREMENT_CONFLICT

用户要求「整个 Role 完成前不得 Commit」与既有 Evidence/Git 契约存在以下**无法同时严格满足**的点。这里不声称已完全满足，也不通过删除不变量来伪装满足。

本文不改变 Review Target、Contract Target、Delivery Freshness、DANGER 停机与 Neutral Tail 的任何机械判定，也不修改 Go Engine；下列处理只在协议层限定提交范围与时机。

| ID | 标记 | 冲突点 | 最小冲突说明 | 技术上可行的处理 | 本阶段状态 |
|---|---|---|---|---|---|
| `SRC-01` | `STRICT_REQUIREMENT_CONFLICT` | A1 / C1 / D1 在角色整体交接前提交 | 审查 / 批准必须绑定不可变 Commit；Commit 无法在创建前知道自己或后续 metadata 的 SHA；因此 Evidence Commit 必然早于角色完成 | 把「完成」区分为 `STAGE_DELIVERABLE_VERIFIED` 与 `SESSION_COMPLETE`（§1.1）；只允许满足 §4.3 全部条件的阶段快照提交 | 规范层已定义；是否接受「阶段完成」口径待第二阶段判断 |
| `SRC-02` | `STRICT_REQUIREMENT_CONFLICT` | loop-ledger write-ahead 在任务未完成时提交 | 熔断要求「先记账并 push，再发请求」；此时角色职责尚未完成 | 归类为 `FORCED_EVIDENCE`，只允许账本（必要时 `state.yaml`），不宣称完成、不 HANDOFF | 已限定范围；不能取消 write-ahead，否则丢失两轮预算证据 |
| `SRC-03` | `STRICT_REQUIREMENT_CONFLICT` | Registry-only 在交付前 push integration branch | 资源必须先在实际 integration branch 落地，才有资格进入实现 | 仅 Analyst、仅已声明 Registry 文件、仅普通 push；失败禁止 force；不作为 Handoff | 已限定；不改变「唯一 develop 写例外」 |
| `SRC-04` | `STRICT_REQUIREMENT_CONFLICT` | ARCHITECT V1 的 CLEAN 早于 OwnerGate 整体完成 | V1 必须先持久化 CLEAN，V2 才能取得 Owner 决策；两阶段之间不能合并 | V1 / V2 分两次 `ROLE_ARTIFACT` 出站；V1 明确不是 Owner 接受 | 已定义；不能合并提交，否则 `review.target` 与 Owner 决策对象失配 |
| `SRC-05` | `STRICT_REQUIREMENT_CONFLICT` | TaskBuilder Bootstrap 在无交付前提交 | 任务边界与初始状态必须先成为 Git 事实，下游才能开始 | 归类 `BOOTSTRAP`，只含任务目录；NORMAL 仍需 `coder-start`，COMPLEX/ARCHITECT 只读同步自检 | 已定义；无 Gate 可用是既有设计 |
| `SRC-06` | `STRICT_REQUIREMENT_CONFLICT` | 失败 / 拒绝 / CHANGES_REQUIRED 证据提交 | 工作未完成时仍需提交负面结论，否则下一 Session 无法恢复 | 归类 `ROLE_ARTIFACT` 或 `FORCED_EVIDENCE`；只含本角色证据与状态；禁止写成成功 | 已定义；不得为了「只在完成后提交」而丢弃失败证据 |
| `SRC-07` | `SPEC_ONLY` | Engine 的 `handoffReady` 只比较本地 remote-tracking ref | 若远端被他人推进，本地 `origin/<branch>` 可能陈旧，Gate 会在远端已变化时 PASS | 自检工具改用 `git ls-remote`（§12）；Engine 的 `handoffReady` 语义保持 V3.1.0 不变（V3.2.0 只新增路线不变量） | 已披露；`verify-remote` 与 `handoff-check` 用真实远端补足，Gate 自身仍可能误 PASS |
| `SRC-08` | `SPEC_ONLY` | 直接 `git commit` / `git push` 无法被 Engine 阻止 | 只读 Validator 不拦截写操作，协议无法阻止恶意或误操作 | 用 Gate + 真实 Git 证据识别错误 HANDOFF；不增加 Hook / 全局配置 | 不宣称机器强制 |
| `SRC-09` | `CLOSED` | TaskBuilder 角色 Prompt 的必读列表与 Directive §4 不一致 | Directive 要求包括 TaskBuilder 在内的所有角色读取 DANGER / Handoff / Git 规范；TaskBuilder 自身 Prompt 原本只列 4 项 | 统一入口固定在 Directive §4；TaskBuilder Prompt 已列出全部 7 项（含路线选择规范） | V3.2.0 第二阶段集成已关闭；`test_git_lifecycle.py` 的 `RoleWiringTest` 与 `test_handoff_contract.py` 覆盖 |
| `SRC-10` | `CLOSED` | `.agent/specs/WorkflowHandoffContract.md` 由 Agent B 提供 | 独立分支不包含该文件；缺失时若 Role 自行发明 Handoff 字段会形成第二套契约 | Directive 明确引用该路径；缺失时不得自行发明，按 `BLOCKED`（`HANDOFF_CONTRACT_ABSENT`）处理 | V3.2.0 第二阶段已核验文件存在、进入 Distribution 必需成员，并接入七个角色 |

## 12. Git 前置 / 后置自检（SPEC_ONLY）

`workflow-check` 只做 Gate，不做 Git 前置检查。为避免角色在提交前才发现 scope / 远端错误，提供一个**只读**自检工具：

```text
.agent/specs/tools/git-lifecycle-check <subcommand> [options]
```

### 12.1 子命令

| 子命令 | 用途 | 关键检查 |
|---|---|---|
| `preflight --role <Role> --task <task>` | 开工前 | 非 detached；工作区 clean；无遗留 staged；当前分支不是 `main` / `master` / integration（TaskBuilder bootstrap 例外）；`origin` 存在 |
| `commit-check --role <Role> --task <task> --mode <MODE> [--phase v1\|v2]` | Commit 前 | staged 路径是否属于该 `MODE` 的白名单；`METADATA` 是否只含 `state.yaml`；`EVIDENCE` 是否混入 `state.yaml`；`FORCED_EVIDENCE` / `REGISTRY_ONLY` 是否越界；引用字段是否为完整 SHA / 可解析；`OwnerGate` 必须用 `--phase` 区分 V1/V2；**本角色 scope 内不得残留未暂存/未跟踪文件**（其他角色的无关修改按 §3.3 保留，不阻断提交，也不得被提交） |
| `push-check --role <Role> --task <task> [--mode normal\|registry] [--expect-branch <name>]` | Push 前 | 目标分支不是 `main` / `master` / integration（Analyst `REGISTRY_ONLY` 例外）；可选核对当前分支 == 任务分支；不处于 detached |
| `verify-remote --task <task> [--branch <name>] [--expect-sha <sha>]` | Push 后 | `git ls-remote` 的实际远端 SHA == 本地 HEAD；本地 remote-tracking ref 是否陈旧 |
| `handoff-check --role <Role> --task <task> [--gate <gate>]` | HANDOFF 前 | 工作区 clean；远端 SHA == 本地 HEAD；`state.yaml` 无 `blocked.active=true`；可选：要求指定 Gate `PASS` |

退出码与 Engine 一致：`0` PASS、`1` FAIL（策略、scope 或 Git 事实不满足）、`2` ERROR（Git 运行级或环境错误）。工具**只读**：不 stage、不 commit、不 push、不修改任何文件。

### 12.2 使用顺序

```text
preflight → commit-check → git commit → push-check → git push → verify-remote → gate → handoff-check → HANDOFF
```

自检 `PASS` 不替代 Gate；自检 `FAIL` 时不得继续写 Git。自检工具无法证明 Commit 内容真实、无法认证 Session、无法阻止绕过工具的直接命令——这些边界必须在 HANDOFF 中如实承认。

## 13. 七角色检查矩阵（验证入口）

### 13.1 角色 × Git 检查

| 角色 | 允许的 Commit 类型 | 必须通过的出口 Gate | 覆盖用例 |
|---|---|---|---|
| TaskBuilder | `BOOTSTRAP` | NORMAL：`coder-start`；COMPLEX / ARCHITECT：无 Gate（只读同步自检） | GL-00 |
| Analyst | `EVIDENCE`（A1）、`METADATA`（A2）、`REGISTRY_ONLY` | `coder-start` | GL-01、GL-12 |
| Architect | `EVIDENCE`（D1）、`METADATA`（D2） | `cleaner-start` | GL-03 |
| Coder | `EVIDENCE`（C1）、`METADATA`（C2）、`FORCED_EVIDENCE`（write-ahead / DANGER） | `cleaner-start` | GL-02、GL-04～GL-09B |
| Cleaner | `ROLE_ARTIFACT`（CLEAN / CHANGES_REQUIRED） | CLEAN：`owner-gate-start`；CHANGES_REQUIRED：无 Gate | GL-11 |
| OwnerGate | `ROLE_ARTIFACT`（V1 与 V2 分两次） | V1：`cleaner-start`；V2：`owner-gate-start`；ACCEPT：`delivery-start` | GL-03、GL-10 |
| Deliverer | `ROLE_ARTIFACT`（PASS / FAIL / BLOCKED） | PASS：`merge-ready`；FAIL/BLOCKED：无 Gate | GL-13 |

### 13.2 场景矩阵

| 编号 | 场景 | 真实证据 | 期望 |
|---|---|---|---|
| GL-00 | TaskBuilder Bootstrap | 任务目录内 `task.md` + `state.yaml` | `--mode bootstrap` PASS；混入业务文件 → FAIL |
| GL-01 | 合法 A1/A2 | A1 `contract.md` + A2 `state.yaml`（APPROVED，target=A1） | `coder-start` PASS；A2 不含业务代码 |
| GL-02 | 合法 C1/C2 | C1 代码/测试 + C2 `state.yaml`（PENDING，target=C1） | `cleaner-start` PASS |
| GL-03 | 合法 D1/D2（含 V1） | D1 结构 + `architecture.md` + 结构测试 + D2 `state.yaml`（PENDING，target=D1）；V1 落 `findings.md` / `core-logic.md` / `review.status=CLEAN` | `cleaner-start` PASS；V1 后 `owner-gate-start` PASS |
| GL-04 | 未完成 Evidence 阶段 | 只有 C1 或 metadata 未 push / dirty | 不得 HANDOFF；Gate FAIL |
| GL-05 | Commit 后 Push 失败 | 远端已前进，普通 push 被拒 | 不宣称同步；Gate FAIL；`verify-remote` FAIL |
| GL-06 | Push 后 Gate FAIL | C2 混入业务代码 | Gate FAIL；不得 HANDOFF |
| GL-07 | Dirty Worktree | 未提交 owned artifact 或他人修改 | Gate FAIL；不得 stage 他人文件 |
| GL-08 | 错误远端 SHA | 远端被 force 更新，本地 tracking 陈旧 | 自检 FAIL；Engine 可能 PASS（已披露） |
| GL-09 | DANGER write-ahead | ledger `DANGER` + report + `blocked.active=true` | 五个 Gate 全 FAIL；`FORCED_EVIDENCE` scope 合法 |
| GL-09B | loop-ledger write-ahead（仅账本） | 只追加 `loop-ledger.md`，`state.yaml` 不变 | `commit-check --mode forced` PASS；夹带 Review 状态变化 → FAIL |
| GL-10 | Owner REJECT | `owner.status=REJECTED` + 决策文件 | `delivery-start` FAIL；证据可恢复 |
| GL-11 | Cleaner CHANGES_REQUIRED | Finding + `review.status=CHANGES_REQUIRED` | `cleaner-start` / `owner-gate-start` FAIL；退回证据已 push |
| GL-12 | Registry-only 边界 | develop 上仅 Registry 文件 | 合法；混入业务文件 → FAIL；非 Analyst → FAIL |
| GL-13 | 正常完成自行 Commit/Push | 全链路 C1/C2/CLEAN/ACCEPT/PASS | `merge-ready` PASS；远端 SHA == 本地；worktree clean |

测试实现见 `.agent/adoption/tests/test_git_lifecycle.py`，使用真实 Git 仓库、真实 bare remote 与真实 `workflow-check` binary，不使用 Markdown 文本搜索代替行为测试。

## 14. 与其它规范的关系

- **Directive**：唯一必读入口；本文与 Handoff 契约由 Directive 统一引用。
- **协作规范**：定义 Gate 语义、角色顺序与 Git 白名单；本文只细化「何时允许写 Git」。
- **职责边界**：定义每角色交付什么；本文定义交付物何时可以 Commit / Push / HANDOFF。
- **循环熔断**：定义 DANGER 触发、write-ahead 与 Owner 恢复；本文定义这些动作的 Commit 类型与证据边界。
- **Handoff 契约**（`.agent/specs/WorkflowHandoffContract.md`，Agent B）：定义 HANDOFF 的最小字段与格式；本文定义 HANDOFF 的 Git 前置条件。两者缺一不可。
- **Engine**：只读 Gate Validator；不因本文新增 Gate、字段或 Hook。

## 15. 不变量

1. 没有 `EVIDENCE` / `METADATA` / `ROLE_ARTIFACT` / `BOOTSTRAP` / `FORCED_EVIDENCE` / `REGISTRY_ONLY` 之外的 Commit。
2. 没有未经 `git diff --cached` 核对的 staged 内容。
3. 没有未 Push 的 HANDOFF。
4. 没有未核验远端 SHA 的 `REMOTE_SYNCED=YES`。
5. 没有 Gate `FAIL` / `ERROR` 后的 HANDOFF。
6. 没有 `blocked.active=true` 时的 HANDOFF。
7. 没有为了掩盖失败而修改旧 Evidence SHA、删除历史事件或改写已 Push 历史。
8. 没有把 A1/C1/D1 描述成已审查、已接受或可交付。
9. 没有把本文或 `workflow-check` 描述成「已禁止一切未授权 Git 写操作」。
