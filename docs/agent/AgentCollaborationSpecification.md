# Agent 协同规范

## 1. 目标

Agent 用来扩大实现、测试和审查能力；Owner 决定需求、关键方案和最终是否接受，并掌握重要业务逻辑。

本文件只规定跨角色协作。长期工程规则见仓库根目录 `AGENTS.md`，角色边界见 `docs/agent/Five-AgentResponsibilityBoundary.md`，当前需求以 `.agent/tasks/<task-slug>/task.md` 为准。

角色数量不是质量指标。只启用能为当前任务增加明确价值的角色。

## 2. 指令与事实来源

按以下顺序执行：

1. Owner 当前明确指令；
2. 当前任务的 Goal、Scope、Out of Scope 和 Acceptance Criteria；
3. Owner 已确认的 `contract.md` 与 `docs/design/*` 中的项目级长期设计；
4. `AGENTS.md`；
5. 本规范、职责边界和角色 Prompt。

代码、测试、日志和运行结果用于判断当前事实，不能自行改变任务目标。发现冲突时说明证据和需要 Owner 决定的事项，不暗自选边。

任何优先级都不能授权泄漏凭据、绕过认证授权、吞掉关键错误，或用降低测试标准制造成功。

## 3. 任务文件与长期 Design

任务级 Artifact 记录「本任务的临时事实与决策过程」，随任务结束而定稿。项目级长期 Design（`docs/design/*`）记录「跨越单个任务、长期稳定的项目级事实」。

每个独立任务使用以下任务级 Artifact：

```text
.agent/tasks/<task-slug>/
├── task.md
├── state.yaml        # 机器事实源（当前状态唯一权威）
├── findings.md
├── core-logic.md
├── delivery.md
└── contract.md       # 仅复杂任务需要
```

| 文件            | 用途                               | 默认维护者                  |
| --------------- | ---------------------------------- | --------------------------- |
| `task.md`       | 目标、范围、AC、验证要求和任务基线 | Task Builder；Owner 可修改  |
| `state.yaml`    | 当前状态的唯一机器权威源（`phase` + 子事实） | 各角色按 Transition Authority 机械持久化 |
| `contract.md`   | 已确认的关键设计、不变量和错误语义 | Analyst 根据 Owner 决定维护 |
| `findings.md`   | Review Finding 与复审结论          | Cleaner                     |
| `core-logic.md` | Owner 应理解的核心逻辑和验证卡     | Cleaner                     |
| `delivery.md`   | 里程碑运行验收                     | Deliverer                   |

Coder 负责生产代码和测试，不为展示过程而重复维护上述文档。

### 项目级长期 Design（`docs/design/*`）

任务级 Artifact（上表五类文件）记录单次任务的临时事实：`task.md` 定义目标与验收，`contract.md` 记录本任务的决策过程与 Owner 决定，`findings.md` / `core-logic.md` / `delivery.md` 记录审查与验收过程。它们不承担跨任务、跨版本的长期事实沉淀。

项目级长期事实沉淀在 `docs/design/*`（长期 Design），按业务模块或设计域一文件组织、kebab-case 命名，例如 `iam.md`、`product.md`、`migration.md`。Design Artifact 只记录长期稳定的结构事实：架构与组件、数据模型（表/实体/关系）、状态机与合法迁移、模块边界与跨模块不变量、一致性模型与失败语义、安全/权限边界、错误码域与公开协议、配置契约。Design 是「结论」（面向接手者），`contract.md` 是「过程」（本任务决策记录），二者不互相机械复制。

生命周期与维护者：

| 类别 | 落点 | 用途 | 默认维护者 |
| ---- | ---- | ---- | ---------- |
| 任务级 Artifact | `.agent/tasks/<task-slug>/` | 单任务目标、决策与验收过程 | Task Builder / Analyst / Cleaner / Deliverer |
| 长期 Design | `docs/design/*` | 项目级长期设计事实，供后续任务与接手者查阅 | Analyst（仅限 Owner `APPROVED` 后新增/更新）；Cleaner 校验一致性；Coder、Deliverer 不写 Design |

任务是否改变项目级长期事实由 `Design Impact`（`NONE` / `UPDATE` / `NEW`）判定。`NEW`/`UPDATE` 时必须在 `task.md` 声明目标 Design Artifact 并纳入 Scope/AC；`NONE` 时不要求 Design。判定清单见 `TaskBuilderPrompt.md`，职责见 `Five-AgentResponsibilityBoundary.md`。

## 4. 默认路由

普通任务：

```text
Owner → Task Builder → Coder → Cleaner → Owner
```

存在关键设计选择、安全边界、并发一致性或根因不明时：

```text
Task Builder → Analyst → Owner 确认 → Coder → Cleaner → Owner
```

仅在重要里程碑或 Owner 明确要求时增加 Deliverer：

```text
Cleaner CLEAN → Owner 核心验证 → Deliverer → Owner 最终决定
```

普通任务不为凑齐五个角色调用 Analyst 或 Deliverer。Task Builder 也可以由当前主 Agent 完成，不要求单独启动一个 Agent。

## 5. 阶段关口

| 角色         | 可以交接的条件                        | 结果                                             |
| ------------ | ------------------------------------- | ------------------------------------------------ |
| Task Builder | 任务可执行、可验收                    | `READY_FOR_CODER` / `READY_FOR_ANALYST`          |
| Analyst      | 推荐方案和待确认事项已写入 Contract   | `WAITING_FOR_OWNER_APPROVAL`                     |
| Analyst      | Owner 的决定已准确记录；`Design Impact = NEW/UPDATE` 时 Design Artifact 已按 APPROVED Contract 写入/更新 | `APPROVED`                                       |
| Coder        | 实现、必要测试和自验完成              | `READY_FOR_REVIEW`                               |
| Cleaner      | 需要修复                              | `CHANGES_REQUIRED`                               |
| Cleaner      | 所有 AC 有充分证据，且无开放 P0/P1/P2；`Design Impact = NEW/UPDATE` 时 Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ Implementation 四者一致 | `review.status=CLEAN`（并转 `WAITING_FOR_OWNER_ACCEPTANCE`） |
| Deliverer    | 里程碑运行验收完成或无法继续          | `PASS` / `CONDITIONAL_PASS` / `FAIL`；无法继续时 `blocked.is_blocked=true` |

上述结果对应的 13 值 `phase`、正交子事实（`review.status` / `owner_verification.status` / `delivery.status` / `blocked`）、每条转换的 Transition Authority（Decision Authority 与 File Writer 分离）见 `docs/design/agent-workflow.md`。`state.yaml` 是唯一机器事实源，本表不再重复状态定义。

复杂任务的 Contract 未获 Owner 确认时，Coder 不开始依赖该决定的实现。`Design Impact = NEW/UPDATE` 时，Design Artifact 就绪前 Coder 不开始依赖该设计的实现；Design 缺失或与 APPROVED Contract/实现不一致时，Cleaner 不得给出 `CLEAN`。`CLEAN` 和 `PASS` 都不能替代 Owner 的最终接受。

无法安全继续时输出 `BLOCKED`，同时给出：阻塞事实、已有证据、需要谁决定什么。

## 6. 证据、基线与 Scope

- Task Builder 记录任务开始时的 Git 基线和已有修改。
- Coder 保留已有工作，只修改当前 Scope 所需内容。
- Cleaner 审查基线后的全部相关变更，包括新增未跟踪文件；不能只看默认 `git diff`。
- `CLEAN` 只对 Cleaner 记录的审查对象有效。生产代码、测试或约束发生实质变化后必须复审。
- Deliverer 验证的版本必须与最近一次 `CLEAN` 对应。
- 未执行的检查标记为 `NOT_VERIFIED` 或 `NOT_EXECUTED`，并说明原因与影响。

需要突破 Scope 才能正确完成时，停止扩张并交 Owner 决定。不得覆盖来源不明的已有修改，也不得把别人的修改算作自己的成果。

## 7. Finding

Cleaner 使用稳定编号：`CLEAN-001`、`CLEAN-002`……

| 等级 | 含义                               | 是否阻塞 CLEAN |
| ---- | ---------------------------------- | -------------- |
| P0   | 安全漏洞、数据损坏、严重不可用     | 是             |
| P1   | 核心业务行为错误                   | 是             |
| P2   | 特定场景错误、可靠性或明显维护风险 | 是             |
| P3   | 低风险改进                         | 由 Owner 决定  |

Finding 必须说明触发条件、实际行为、预期行为、影响、证据和修复边界。只有 Cleaner 创建、关闭或重新打开 Finding；Coder 负责修复并提供回归证据。

## 8. Owner 核心验证

Owner 不需要逐行 Review 全部代码。交付应帮助 Owner 理解：

- 关键业务不变量是什么；
- 生产代码在哪里实现；
- 哪个测试证明它；
- 破坏该不变量时，测试能否失败。

Coder 为关键行为编写可长期保留的测试，并在交接中指出最重要的测试入口。Cleaner 审查测试可信度，在 `core-logic.md` 中整理一至两个高价值的 Owner 验证卡。

验证卡可以包含可逆 Mutation：先确认测试通过，临时破坏一条不变量，确认指定测试失败，再恢复代码并重新通过。Mutation 只用于理解和检验测试，执行后必须恢复正确实现并确认工作区状态。

Owner Verification 状态的唯一机器事实源是 `state.yaml.owner_verification.status`（取值 `NOT_REQUIRED` / `PENDING` / `ACCEPTED`）。`PENDING` / `NOT_REQUIRED` 由 Cleaner 生成 `core-logic.md` 时按「是否产生 CL 验证卡」初始机械写入 `state.yaml`；`ACCEPTED` 仅在 Owner 明确确认/接受指令驱动下由 Cleaner 机械持久化到 `state.yaml`。`core-logic.md` 只承载 Owner Core Logic 验证卡、因果说明与验证证据，不承担 Owner Verification 状态权威；如为便于人类阅读而保留展示性状态字段，必须明确标记为非权威 mirror，任何 Gate / Validator / Agent 决策均不得依赖它。任何 Agent 不得因 `CLEAN`、测试通过、Owner 阅读过文件或其它间接信号自行把 `PENDING` 置为 `ACCEPTED`。

Owner 是 `ACCEPTED / REJECTED`（Contract 与 Core Logic 两处）的唯一 Decision Authority；Agent 只能在收到 Owner 明确指令后机械持久化该决定，不得通过测试通过、Cleaner `CLEAN` 或读取聊天上下文自行推断 Owner 已接受。Deliverer 不得要求 Owner 手工编辑 Markdown。

## 9. 中文与交接表达

- Agent 面向 Owner 的分析、说明、文档和最终输出默认使用中文。
- 结论先行，只保留完成情况、关键证据、风险和下一步；省略重复背景、普通样板代码和可直接从 Diff 获得的完整文件清单。
- 只输出与当前结果有关的章节，不为满足模板制造空段落。
- 状态标识、代码标识符、API 字段、命令和协议名可以保留英文，但要用中文解释。
- 表格只用于映射和对比；简单交接优先使用短列表。

详细证据应写入对应任务文件、测试或 Git Diff。聊天交接用于帮助 Owner 快速判断，而不是复制所有过程记录。

## 10. 权限与最终决定

没有 Owner 明确授权时，Agent 不执行 Push、Force Push、改写历史、生产部署或其他高影响外部操作。

Owner 决定关键业务规则、Contract、是否接受 P3、是否启动 Deliverer，以及最终接受、Commit、Merge、Push 和 Deploy。

## 11. 跨任务全局资源预留

### 11.1 定义与分类

「全局资源」指多个并行 Task 可能同时申请、重复会导致冲突或语义错误的项目级命名资源：

| 类别 | 判定标准 | 治理 | 示例 |
| --- | --- | --- | --- |
| A 强全局唯一 | 重复导致构建/迁移/运行或语义冲突 | 纳入 Registry（强治理） | migration version、错误码域/编号 |
| B namespace | 通常靠命名空间避免，重复有潜在静默冲突 | 只分类、不纳入 | permission code、Redis key 前缀、config key、route prefix、MQ topic |
| C 局部命名 | 单任务内部、无跨任务冲突 | 禁止注册 | 局部变量、模块内函数、内部 struct、普通测试 fixture、临时命名 |

C 类**不得以任何形式要求注册**；新增资源类型进入治理须有明确判定标准且经 Owner 确认，不得默认「任何名字都要注册」。

### 11.2 共享事实源

`.agent/registry/`（`develop` 上）是全局资源**分配状态**的权威事实源，含 `error-codes.md`（错误码域）与 `migrations.md`（migration version）。

Reservation 生效 = 一个**只改 Registry 文件的 commit 落在 `develop`**。Feature Branch 在 Coder 开始前 `rebase/merge develop`（或取回最新 Registry）以读到其他并行 Task 的 `RESERVED`。仅在 Feature Branch 内自行声明 Reservation 不视为有效预留。

与 `docs/design/*` 的关系：Registry 记录「分配状态」，`docs/design/<module>.md` 的「错误码域」章节与 `docs/design/migration.md` 的迁移清单记录「合并后模块语义归属与内容」，二者互补、不复制，避免双事实源。

### 11.3 分配规则

- 错误码域：固定大小 1000、按 1000 对齐；域序 `domain_seq = code / 1000`，域区间 `[domain_seq × 1000, domain_seq × 1000 + 999]`；下一空闲域 `domain_seq_next = max(已记录域序) + 1`（域序为正整数、不受四位数宽度限制）；域内具体编号由 Analyst 在 Contract 逐个列出。
- migration version：保留 `YYYYMMDD + 序号` 格式；`next = max(所有已记录 version, 含 RELEASED) + 1`。

### 11.4 生命周期

状态三态：`RESERVED`（Reservation 已通过「只改 Registry 的 commit」落到共享 `develop` 生效、Feature 尚未合并进 `develop`）、`ACTIVE`（已合并生效，终态）、`RELEASED`（取消释放，记录保留）。

转换：`RESERVED → ACTIVE`（feature 合并进 `develop` 时由合并任务同步）；`RESERVED → RELEASED`（Task 取消时由 Analyst/Owner 标记）。

复用规则按资源类型区分：
- migration version：一旦分配即永久 tombstone、**不得复用**（`next` 仍计入 RELEASED 的 `max`）。
- 错误码域：仅纯 `RESERVED` 阶段可 `RELEASE` 后复用；已实现/已合并即不可复用。

### 11.5 并行竞争防护

无外部锁、无独立服务。以 Git 提交顺序 + 冲突检测串行化：先提交到 `develop` 者胜，后提交者在 rebase/合并时看到 Registry 已占用，必须重新申请不同值。结果至多一个 Task 合法持有同一资源。

### 11.6 一致性与检测

Coder 实现中出现的每个全局资源都必须已存在于 APPROVED Contract 的全局资源清单（禁止自行 `max+1` 占号）。实现阶段新增全局资源需求 → 走既有 `CONTRACT_REVISION` 流程。

Cleaner 做三边一致性检查：Registry ↔ Contract ↔ 实现（`internal/codes/codes.go`、`internal/migrations/sql/*`、`migrations_test.go`），任一漂移 → `CHANGES_REQUIRED`。

`scripts/check-registry.sh` 提供最小只读机械校验（域/version 重复 + Registry ↔ 实现明显漂移），不分配、不改 Registry、不替代语义判断。

`cmd/workflow-check` 在 Task 进入/越过 `IMPLEMENTING` 且声明了全局资源时，以 shared `develop` Registry 为权威机械验证 Reservation（version/owner task/status 一致）；`state.yaml.required_resources` 只记录「需要什么」、不自证满足，Feature Branch 私留 `RESERVED` 或写 `SATISFIED` 均不构成有效 Reservation。