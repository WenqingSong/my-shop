# Agent 协同规范

## 1. 目标

Agent 用来扩大实现、测试和审查能力；Owner 决定需求、关键方案和最终是否接受，并掌握重要业务逻辑。

本文件只规定跨角色协作。长期工程规则见仓库根目录 `AGENTS.md`，角色边界见 `.agent/specs/Five-AgentResponsibilityBoundary.md`，当前需求以 `.agent/tasks/<task-slug>/task.md` 为准，状态模型见 `docs/design/agent-workflow.md`。

角色数量不是质量指标。只启用能为当前任务增加明确价值的角色。

正常六角色运行只依赖 Git + Artifacts + `AGENTS.md` + `.agent/specs/*` + `.agent/roles/*` + `docs/design/*`，不依赖临时文件 `PROJECT_ADAPTATION.md`。`PROJECT_ADAPTATION.md` 只存在于 Workflow V2 首次迁移 / Adoption 到一个项目的过程中，Adoption 完成后即删除，见 `.agent/specs/WorkflowAdoption.md`。

## 2. 指令与事实来源

按以下顺序执行：

1. Owner 当前明确指令；
2. 当前任务的 Goal、Scope、Out of Scope 和 Acceptance Criteria；
3. Owner 已确认的 `contract.md` 与 `docs/design/*` 中的项目级长期设计；
4. `AGENTS.md`；
5. 本规范、职责边界和角色 Prompt。

代码、测试、日志和运行结果用于判断当前事实，不能自行改变任务目标。发现冲突时说明证据和需要 Owner 决定的事项，不暗自选边。

任何优先级都不能授权泄漏凭据、绕过认证授权、吞掉关键错误，或用降低测试标准制造成功。

## 3. Session Isolation 与事实来源

六个 Agent 默认运行在独立 Session / 独立上下文。新 Agent 必须重新读取 Git、Task artifacts、Contract、Design、Registry、Review artifacts 与对应 commit SHA，禁止依赖「上一个 Agent 聊天里已经说过了」。

原则：**Handoff 给路线，Git + Artifacts 给事实。**

## 4. 任务文件与长期 Design

任务级 Artifact 记录「本任务的临时事实与决策过程」，随任务结束而定稿。项目级长期 Design（`docs/design/*`）记录「跨越单个任务、长期稳定的项目级事实」。

```text
.agent/tasks/<task-slug>/
├── task.md
├── state.yaml          # 机器事实源（当前决策唯一权威）
├── contract.md         # 仅复杂任务需要
├── findings.md
├── core-logic.md
├── owner-decision.md
└── delivery.md
```

| 文件              | 用途                                     | 默认维护者          |
| ----------------- | ---------------------------------------- | ------------------- |
| `task.md`         | 目标、范围、AC、验证要求和任务基线       | TaskBuilder；Owner 可修改 |
| `state.yaml`      | 当前决策的唯一机器权威源（Evidence Binding） | 各角色按职责机械持久化 |
| `contract.md`     | 已确认的关键设计、不变量和错误语义       | Analyst             |
| `findings.md`     | Review Finding 与复审结论                | Cleaner             |
| `core-logic.md`   | 值得 Owner 理解的核心逻辑和验证卡         | Cleaner             |
| `owner-decision.md` | Owner 核心逻辑决策记录                   | OwnerGate           |
| `delivery.md`     | 里程碑运行验收                           | Deliverer           |

Coder 负责生产代码和测试，不为展示过程而重复维护上述文档。

`state.yaml` 只保存 Git 无法推导的决策事实，**不保存** `phase`/`STALE`/`DONE`/`branch`/`HEAD`/`working_tree`/`changed_files`/`NEXT_ROLE`/`NEXT_ACTION`。详见 `docs/design/agent-workflow.md`。

## 5. 默认路由

标准路由：

```text
Owner 准备 feature branch
→ TaskBuilder
→ Analyst（Contract Decision / 如需资源 Reservation）
→ Coder
→ Cleaner
→ OwnerGate（Owner Core Logic Decision）
→ Deliverer
→ Owner（决定何时/如何集成）
```

简单任务（无需独立 Contract/Design，`contract.status = NOT_REQUIRED`）可 `TaskBuilder → Coder`。TaskBuilder 不能为省流程擅自跳过 Analyst。

Owner Checkpoint（`WAITING_FOR_OWNER_DECISION`）不是 Handoff：不结束当前 Session、没有 NEXT_ROLE，Owner 回复后原 Agent 继续。

## 6. 阶段关口（Gate）

每个角色正常 Handoff 前运行对应出口 Gate（详见 `docs/design/agent-workflow.md` §5）：

| 角色      | 出口 Gate          | Handoff 目标 |
| --------- | ------------------ | ------------ |
| Analyst   | `coder-start`      | Coder        |
| Coder     | `cleaner-start`    | Cleaner      |
| Cleaner   | `owner-gate-start` | OwnerGate    |
| OwnerGate | `delivery-start`   | Deliverer    |
| Deliverer | `merge-ready`      | Owner        |

`handoffReady`（非 detached、clean、remote 存在、local==remote）是每个出口 Gate 的共同前提。

正常 HANDOFF 的统一前提是：

- 当前角色职责已经全部完成；
- 本角色 owned artifacts 已持久化；
- 应 commit 的已 commit；
- 应 push 的已 push；
- working tree clean；
- remote synced（local HEAD == `origin/<current-feature>`）；
- 下一角色入口 Gate 已 `PASS`；
- 然后才能输出最终 `HANDOFF`。

角色得到业务结果（如 Cleaner 的 `CLEAN`、Deliverer 的 `PASS`）只是阶段结论，不等于本轮完成：必须先完成上述出站收尾并通过下一角色入口 Gate。Gate `FAIL` 时不结束 Session，继续完成本角色职责，超出本角色 authority 才输出 `BLOCKED`。Decision Checkpoint（`WAITING_FOR_OWNER_DECISION`）不是 Handoff，期间不得结束 Session。

## 7. 证据、基线与 Scope

- TaskBuilder 记录任务开始时的 Git 基线和已有修改。
- Coder 保留已有工作，只修改当前 Scope 所需内容。
- Cleaner 审查 `review.target` 之后的全部相关变更；不能只看默认 `git diff`。
- `CLEAN` 只对 `review.target`（immutable commit）有效。生产代码、测试或约束发生实质变化后必须复审。
- Deliverer 验证的版本必须与最近一次 `CLEAN` 对应，并记录 `feature_head` 与 `develop_base`。
- 未执行的检查标记为 `NOT_VERIFIED` 或 `NOT_EXECUTED`，并说明原因与影响。

需要突破 Scope 才能正确完成时，停止扩张并交 Owner 决定。不得覆盖来源不明的已有修改，也不得把别人的修改算作自己的成果。

## 8. Finding

Cleaner 使用稳定编号：`CLEAN-001`、`CLEAN-002`……

| 等级 | 含义                               | 是否阻塞 CLEAN |
| ---- | ---------------------------------- | -------------- |
| P0   | 安全漏洞、数据损坏、严重不可用     | 是             |
| P1   | 核心业务行为错误                   | 是             |
| P2   | 特定场景错误、可靠性或明显维护风险 | 是             |
| P3   | 低风险改进                         | 由 Owner 决定  |

Finding 必须说明触发条件、实际行为、预期行为、影响、证据和修复边界。只有 Cleaner 创建、关闭或重新打开 Finding；Coder 负责修复并提供回归证据。

## 9. Owner 核心验证（OwnerGate）

Owner 不需要逐行 Review 全部代码。交付应帮助 Owner 理解：

- 关键业务不变量是什么；
- 生产代码在哪里实现；
- 哪个测试证明它；
- 破坏该不变量时，测试能否失败。

Cleaner 审查测试可信度，在 `core-logic.md` 整理核心验证卡；OwnerGate 据此提炼 1~3 个核心机制、解释并请求 Owner 决策，再把结果持久化到 `owner-decision.md` 与 `state.owner`。

`owner.status = ACCEPTED` 只在 Owner 明确接受指令下由 OwnerGate 机械持久化，并绑定当前 `review.target`（`owner.review_target`）。任何 Agent 不得因 `CLEAN`、测试通过或阅读聊天上下文自行把 `PENDING` 置为 `ACCEPTED`。

## 10. 中文与交接表达

- Agent 面向 Owner 的分析、说明、文档和最终输出默认使用中文。
- 结论先行，只保留完成情况、关键证据、风险和下一步；省略重复背景、普通样板代码和可直接从 Diff 获得的完整文件清单。
- 只输出与当前结果有关的章节，不为满足模板制造空段落。
- 状态标识、代码标识符、API 字段、命令和协议名可以保留英文，但要用中文解释。
- 表格只用于映射和对比；简单交接优先使用短列表。

详细证据应写入对应任务文件、测试或 Git Diff。聊天交接用于帮助 Owner 快速判断，而不是复制所有过程记录。

## 11. Git 权限与最终决定

- Agent 允许：`fetch / status / diff / log / show / rev-parse`、`switch` 到已存在且属于当前任务的分支、`pull --ff-only` 当前 feature。
- Agent 禁止：创建/删除/重命名 branch、merge feature→develop、`push --force`、`reset --hard`、`clean -fd`、`git add -A` / `git add .`、rebase 已发布历史、`commit --amend`、rewrite history。`push develop` 默认禁止——唯一例外是 Analyst 的 Registry-only develop commit（仅 `.agent/registry/migrations.md` 与 `.agent/registry/error-codes.md`，见 §12.2）。
- 这些 git 写约束是协议/Prompt 硬约束；shared develop 的机器级保护交给仓库 branch protection / 外部 Git 控制能力。`workflow-check` 只读，不拦截 git 写命令。
- 标准 commit 前流程：`git status --short` → `git diff` → `git add <明确属于本角色的文件>` → `git diff --cached` → `git commit`。发现其他角色遗留修改 → STOP，不得顺便提交。
- Agent-generated commit message 默认使用中文摘要，保持 Conventional Commit 结构：`<type>(<scope>): <中文摘要>`。`type` / `scope` 保留英文关键字（常用 `feat` / `fix` / `refactor` / `test` / `docs` / `chore`），中文只用于 summary。不要求 summary 必须英文，不因中文 commit message 阻塞流程。

Owner 决定关键业务规则、Contract、是否接受核心逻辑、是否启动 Deliverer，以及最终接受、Commit、Merge、Push 和 Deploy。

## 12. 跨任务全局资源预留

### 12.1 定义与分类

「全局资源」指多个并行 Task 可能同时申请、重复会导致冲突或语义错误的项目级命名资源：

| 类别 | 判定标准 | 治理 | 示例 |
| --- | --- | --- | --- |
| A 强全局唯一 | 重复导致构建/迁移/运行或语义冲突 | 纳入 Registry（强治理） | migration version、错误码域/编号 |
| B namespace | 通常靠命名空间避免，重复有潜在静默冲突 | 只分类、不纳入 | permission code、Redis key 前缀、config key、route prefix、MQ topic |
| C 局部命名 | 单任务内部、无跨任务冲突 | 禁止注册 | 局部变量、模块内函数、内部 struct、普通测试 fixture、临时命名 |

C 类**不得以任何形式要求注册**；新增资源类型进入治理须有明确判定标准且经 Owner 确认。

### 12.2 共享事实源与 Reservation 流程

`.agent/registry/`（`develop` 上）是全局资源分配状态的权威事实源，含 `error-codes.md` 与 `migrations.md`。这两个 Registry 文件由机器配置 `.agent/workflow.yaml`（`resources.*.registry`）声明；Analyst 的 Registry-only shared-develop mutation 仅限该配置声明的 Registry 文件。

需要资源时，Analyst fetch `origin/develop` → 读取最新 Registry → 计算 candidate → 检查空闲 → 将 candidate 写入 Contract 并请求 Owner Contract Decision。Contract APPROVED 后，Analyst 形成 Registry-only commit（仅 `.agent/registry/migrations.md` 与 `.agent/registry/error-codes.md`）并 push `origin develop` 落实 `RESERVED`。push 前必须确认 staged/commit diff 不含任何其他路径，出现即 STOP；push 因 non-fast-forward 被拒时禁止 force，改为 fetch 最新 develop → 重新读取 Registry → 重新计算资源 → 重新形成合法 reservation。落 `RESERVED` 后，Analyst 重新读取 `origin/develop` Registry 验证：资源存在、owner 正确、状态 `RESERVED/ACTIVE`，通过后才 HANDOFF Coder。

Feature Branch 内自行声明 `RESERVED` 不构成有效预留；`state.yaml.resources` 只记录「需要什么」，不自证满足。

### 12.3 生命周期

状态三态：`RESERVED`（已落 develop、Feature 未合并）、`ACTIVE`（已合并，终态）、`RELEASED`（取消释放，记录保留）。

复用规则：migration version 一经分配永久 tombstone、不得复用；错误码域仅纯 `RESERVED` 阶段可 `RELEASE` 后复用。

### 12.4 并行竞争防护

无外部锁、无独立服务。以 Git 提交顺序 + 冲突检测串行化：先提交到 `develop` 者胜，后提交者必须重新申请不同值。

### 12.5 一致性与检测

Coder 实现中出现的每个全局资源都必须已存在于 APPROVED Contract 的全局资源清单（禁止自行 `max+1` 占号）。实现阶段新增资源需求 → 走 Contract Revision。

Cleaner 做三边一致性检查：Registry ↔ Contract ↔ 实现，任一漂移 → `CHANGES_REQUIRED`。

`scripts/check-registry.sh` 提供最小只读机械校验；`workflow-check gate coder-start` 以 shared `develop` Registry 为权威机械验证 Reservation。
