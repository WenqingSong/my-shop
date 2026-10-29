# Task: 并行任务全局资源预留（Parallel Task Global Resource Reservation）

## Goal

建立一套最小、明确、可验证的 **Global Resource Reservation（跨任务全局唯一资源预留）治理机制**：任何需要占用「项目级全局唯一资源」（至少含 Error Code Domain 与 Migration Version）的 Task，必须在进入 Coder 之前，从**所有并行 Task 都能看到的共享事实源**取得自己独占的资源分配；后续 Coder 只能使用已预留的资源，Cleaner 能按确定步骤检出「Reservation ↔ 实现」不一致。

用户可观察结果：再次出现 `feat/address` 与 `feat/cart` 这类并行开发时，两个 Task 不会再各自合法地选中同一个错误码域或同一个 migration version，冲突在进入实现之前就被看见并被阻止，而不是等到 merge 时人工 renumber。

## Problem Statement

本项目多个 Feature Branch 从相近的 `develop` 基线并行开发。每个 Task 在「自己看得到的旧基线」上独立决定自己的全局资源编号，流程等价于：

```text
Task A: 读取当前最大资源 → 选择 next
Task B: 读取同一个旧基线 → 也选择 next
            ↓
        read / read / allocate / allocate
            ↓
          collision
```

当前 Workflow 中不存在 Reservation、Registry 或 Conflict Detection 机制，因此「尚未合并的并行任务正在占用什么」对任何一方都不可见。需要治理的是 **Cross-Task Global Resource Coordination**，而不是单独修 Address 或 Cart。

## Incident Reconstruction（Address + Cart 真实事件）

已核实事实（证据见文末引用）：

| 阶段 | Address（`feat/address`） | Cart（`feat/cart`） |
| --- | --- | --- |
| Task Builder 声明 | `shipping-address-v1/task.md`：「错误码：新增地址域（建议 7000-7999）」、「地址域 7000-7999 空闲」 | `cart-v1/task.md`：「错误码：新增购物车域（建议 7000-7999）」、「购物车域 7000-7999 空闲」 |
| 共同基线 | 最新错误码域 `6000-6999`（inventory）、最新 migration `20261001000004` | 同左 |
| Analyst Contract | `shipping-address-v1/contract.md`：错误码域 7000-7999（7001/7002），迁移 `20261001000005_addresses.up.sql` | `cart-v1/contract.md`：错误码域 7000-7999（7001/7002/7003），迁移 `20261001000005_cart_items.up.sql`，并附逃逸条款「落地时若被占用取下一个更大 14 位时间戳」 |
| Owner | `APPROVED`（含 CONTRACT_REVISION） | `APPROVED` |
| 冲突 | 两个分支各自独立选中 **7000-7999** 与 **00005** | 同左 |
| 最终仓库事实 | `internal/codes/codes.go`：`7001/7002`；`internal/migrations/sql/20261001000005_addresses.up.sql` | `internal/codes/codes.go`：`8001/8002/8003`；`internal/migrations/sql/20261001000006_cart_items.up.sql`；`migrations_test.go` 的 `latestMigrationVersion = 20261001000006` |
| 解决方式 | 人工 renumber + 同步测试与长期 Design（`777ec1d` Merge `feat/cart` → `develop`） | 同左 |

两条已核实的直接后果：

1. **两份 Contract 都写「7000-7999 空闲」**，因为双方读取的是同一个未包含对方的旧基线；Task Builder 与 Analyst 都不可能看到对方的存在。
2. **`cart-v1` 的 APPROVED Contract 至今仍记录「错误码域 7000-7999」「migration `20261001000005_cart_items`」「错误码 7001/7002/7003」，而最终实现是 `8001/8002/8003` 与 `20261001000006`。该 Contract 已是 `APPROVED` 且 `findings.md` 为 `CLEAN`，漂移未被任何机制检出**——因为当前 Workflow 里没有「Reservation ↔ 实现一致性」这一检查项。这不是要追责，而是「缺机制」的直接证据，也是本任务最好的真实检测样本。

## Root Cause

> 多个并行 Task 会同时申请「项目级全局唯一资源」，但当前 Agent Workflow 没有 Reservation / Registry / Conflict Detection 机制；所有全局资源的分配事实（错误码域、migration version）都只在**合并进 `develop` 之后**才可见，而分配决策发生在**合并之前**。

不是普通 Git Merge Conflict：也不是「谁不小心写错」，而是 **read → read → allocate → allocate 的并发竞争**，两个 Task 在各自视角下都完全合理。

## Scope

- 明确定义什么是 Global Resource，并给出 A/B/C 三类分类与判定标准（见 Relevant Context 的 Global Resource Inventory）。
- 建立 Error Code Domain 的唯一 Reservation 机制：谁申请、谁确认、何时 RESERVED、何时 ACTIVE、取消怎么办、如何防止两个 Task 同时 reserve 同一区间。
- 建立 Migration Version 的并行安全分配机制（Reservation / 真正时间戳唯一 ID / 组合机制，由 Analyst 提案、Owner 决定）。
- 建立共享事实源（Registry 或等价机制），**必须满足：Task A 的预留结果，在 Task B 开始实现之前可被 Task B 读到**；不允许「各 Feature Branch 自己维护一份互相不可见的 Reservation」。
- Task / Contract 中的全局资源声明格式：Task 进入 Coder 前必须能读到自己的已批准全局资源分配。
- Coder 使用约束：禁止自行占用未预留/未批准的全局资源；实现阶段新增全局资源需求的追加 Reservation / Revision Flow。
- Cleaner 冲突检测规则：Task/Contract Reservation ↔ 共享 Registry ↔ 最终实现 三者一致性检查项。
- 并行场景 Verification Scenarios（A–F）作为验收核心。
- 把上述规则写入本仓库既有的治理落点（`docs/agent/*` 角色 Prompt / 协同规范 / 任务级 Artifact 模板），以及 Registry 本体（具体落点由 Analyst 提案、Owner 决定）。
- 从当前仓库既有事实建立 Initial Registry / Baseline（1000-8999、migration 00001-00006），不需要重写历史 Task。

## Out of Scope

- 不修改 Address / Cart 的任何业务代码、错误码取值与 migration 取值；不再次调整已解决的 7000 / 8000 与 00005 / 00006 分配。
- 不修正 `cart-v1` Contract 与实现之间的历史编号漂移（仅作为 AC-011 的**检测样本输入**，本任务不改写历史 Contract、不重开其 `CLEAN`）。
- 不重写历史 Migration、不修改数据库业务 Schema、不修改 `internal/codes/codes.go` 的现有取值。
- 不重构整个 Agent Workflow；不改动既有 `Design Impact` / 长期 Design 治理机制（由 `design-doc-governance` 任务确立）。
- 不引入外部分布式锁服务、不搭建独立资源分配服务器、不引入新运行时依赖。
- 不为所有普通变量 / 函数 / 文件 / struct / 测试 fixture 建立 Registry。
- 不修改 Git 分支模型（已核实：无证据表明分支模型是本次根因；若 Analyst 发现必要证据，需单独提出并交 Owner 决定，不得在本任务内顺手改动）。
- 不要求 Owner 手工查号、编号或编排区间。

## Design Impact

Design Impact: NONE

说明（含校正条件）：本任务的产物是 **Agent 协同治理规则 + Reservation 共享事实源**，落点预期为 `docs/agent/*`（协同规范 / 角色 Prompt / 任务级 Artifact 模板）与 Registry 文件（候选 `.agent/registry/*` 或 `docs/agent/*`，由 Analyst 提案、Owner 决定），不属于 `docs/design/*` 记录的业务/技术长期事实（数据模型、业务状态机、公开 API 契约、安全边界等）。与 `agent-workflow-owner-confirmation` 任务的判定口径一致：治理过程机制 ≠ 业务长期设计事实。

**校正条件（交 Analyst，必要时回 Owner）**：若最终方案决定把「错误码域分配规则」或「migration version 分配模型」写入/修改 `docs/design/*`（例如 `docs/design/migration.md` §2.1 现声明「version 为 14 位时间戳 `YYYYMMDDHHMMSS`」，采用「真正时间戳唯一 ID」方案可能需要修订该段），则 Design Impact 应校正为 `UPDATE`，并把对应 Design Artifact 纳入 Scope / Deliverables / AC。校正改变 Scope 时须回 Owner / Task Builder。

## Acceptance Criteria

- [ ] AC-001（Global Resource 定义与分类）：机制明确定义「哪些资源属于 Global Resource」，分类至少覆盖 A 类强全局唯一（migration version、error code 域/编号）、B 类 namespace 资源（permission code、Redis key 前缀、config key、route prefix）、C 类不纳入治理的局部命名（局部变量、模块内函数名、内部 struct、普通测试 fixture）；并明确写出「C 类不得以任何形式要求注册」。
- [ ] AC-002（错误码域唯一 Reservation）：给定当前 ACTIVE 域最大为 `6000-6999`，两个均未合并的并行 Task 都需要新增错误码域时，依照机制二者取得**互不重叠**的域（例如 7000-7999 与 8000-8999）；不允许两个 Task 都合法地取得同一域。
- [ ] AC-003（Migration Version 并行安全）：两个均未合并的并行 Task 各新增一个 migration 时，依照机制二者取得**不同 version**；或后申请者在进入 Coder 前被明确阻止并要求重新申请。不允许「两个文件同 version，等到 merge/运行时才发现」。
- [ ] AC-004（共享事实源可见性）：Reservation 记录位于所有并行 Task 在开始实现前都能读取的共享位置；在 `feat/X` 分支上能读到 `feat/Y` 的 RESERVED 记录。仅存在于各 Feature Branch 私有文件的方案不满足本 AC。
- [ ] AC-005（进入 Coder 前已知分配）：Task 进入 Coder 前，其 Task/Contract 中已包含本任务获批的全局资源清单（错误码域 + migration version），Coder 不需要也不允许自行推断编号。
- [ ] AC-006（禁止 Coder 自选）：实现阶段禁止「我看到最新是 00004，所以写 00005」这类自行占用；出现未预留的全局资源需求时，必须走明确的追加/修订流程，不得自行分配。
- [ ] AC-007（追加 Reservation 流程）：Task 实现中需要新增第二个 migration（或新增错误码域）时，存在明确的追加 Reservation 流程：新资源由共享事实源给出，且不与任何 RESERVED / ACTIVE 资源冲突，并有确定的批准与记录方式。
- [ ] AC-008（生命周期明确）：机制明确定义 Reservation 的状态集与转换条件，语义上至少覆盖「已申请但尚未合并」「已合并生效」「已释放/不再使用」三种状态，以及每个状态的责任人；**Task 取消时预留资源有确定处理**（release / tombstone / 永不复用，由 Analyst 提案、Owner 决定），不得保持模糊。
- [ ] AC-009（Cleaner 检出错误码不一致）：给定 Reservation/Contract 声明错误码域 `8000-8999`、实现使用 `7001`，按机制定义的检查步骤能明确报出不一致，结论为 `CHANGES_REQUIRED`。
- [ ] AC-010（Cleaner 检出 migration version 不一致）：给定 reserved version `20261001000006`、迁移文件实际 version `20261001000005`，按机制定义的检查步骤能明确报出不一致，结论为 `CHANGES_REQUIRED`。
- [ ] AC-011（真实样本可检出）：以仓库既有 `cart-v1` 历史漂移（APPROVED Contract 记录错误码域 `7000-7999` / migration `20261001000005_cart_items` / 错误码 `7001-7003`，实现为 `8001-8003` / `20261001000006_cart_items`）作为输入，按机制定义的检查步骤能报出至少一处「Reservation ↔ 实现」不一致。本任务只把该漂移用作检测样本，不修正它。
- [ ] AC-012（历史基线可纳入，无需重写历史）：机制能从当前仓库既有事实（`internal/codes/codes.go`、`internal/migrations/sql/*`、`docs/design/*`）建立 Initial Registry / Baseline，覆盖 `1000-8999` 与 `20261001000001-20261001000006`；不需要重写任何历史 Task、历史 Contract 或历史 migration。
- [ ] AC-013（防止同时 reserve 同一区间）：机制明确给出「两个 Task 同时申请同一区间」时的处理（串行化 / 冲突检测 / 人工裁决之一，由 Analyst 提案、Owner 决定），结果必须是「至多一个 Task 合法持有该区间」，或双方都被明确要求重新申请。
- [ ] AC-014（Owner 不手工编号）：Owner 在本机制中的动作被限定为「接受或拒绝 Reservation 方案」，不要求 Owner 手工查询、比对或编排任何编号。
- [ ] AC-015（不扩大治理）：机制不适用于 C 类局部命名；新增资源类型进入治理需有明确判定标准且经 Owner 确认，不得默认「任何名字都要注册」。
- [ ] AC-016（不改动业务）：本次不修改 `api/`、`internal/` 业务代码、`docs/design/*` 业务设计（Design Impact 校正为 `UPDATE` 的情形除外，且须经 Owner 确认）；既有错误码取值与 migration 取值不变。
- [ ] AC-017（真实案例不重现）：在机制下重放本次 Address + Cart 并行案例（同一 `develop` 基线、各新增一个错误码域与一个 migration，互相不可见地并行），两个 Task 在进入 Coder 前即获得不同且不冲突的分配，本次冲突不会重现。

## Relevant Context

### Global Resource Inventory（分类结论，供 Analyst 校正）

调查依据：`internal/codes/codes.go`、`internal/migrations/**`、`docs/design/migration.md`、`internal/boot/seed.go`、`internal/auth/session.go`、`internal/cmd/routes_*.go`、`manifest/config/config.yaml`、`docs/design/*`。

**A 类：强全局唯一资源（重复 → 构建/迁移/运行或语义冲突）**

| 资源 | 事实来源 | 重复后果 | 证据 |
| --- | --- | --- | --- |
| Migration version | `internal/migrations/sql/{version}_{title}.up.sql` + `schema_migrations` | 同一 version 两个文件 → 迁移源载入/执行冲突，必须人工 renumber（本次已发生） | cart Contract 写 `...00005_cart_items`，最终仓库为 `...00006_cart_items` |
| Error code 域 / 编号 | `internal/codes/codes.go`（单一 const 块 + `codeTable` map） | 同一数字两种业务语义；若 `codeTable` 出现重复 key 则为 Go 编译错误 | 两份 Contract 同时声明 7000-7999；最终实现 7001/7002（Address）与 8001-8003（Cart） |

**B 类：Namespace 类资源（通常可靠命名空间避免，是否纳入 Registry 由 Analyst/Owner 决定）**

| 资源 | 现状 | 冲突风险 |
| --- | --- | --- |
| Permission code | `internal/boot/seed.go` 的 `seedPermissionList`，`permissions.code` UNIQUE，并发重复靠 1062 兜底「跳过」 | 两个并行 Task 用同一 code 但不同 name 时，先落库者胜、后者被静默跳过，产生授权缺口且无报错；这是「静默冲突」，值得 Analyst 评估 |
| Redis key 前缀 | `internal/auth/session.go`：`iam:session:`、`iam:admin:session:`、`iam:user:{id}:sessions`；注释明确「禁止散落 `iam:session:` 字面量」 | 已有模块前缀约定，冲突概率低 |
| Config key | `manifest/config/config.yaml`（`server.*`/`database.default.*`/`redis.default.*`/`auth.*`/`admin.super.*`/`startup.*`）+ 环境变量覆盖 | 扁平命名空间，跨模块重名概率低 |
| Route prefix / path | `internal/cmd/routes_frontend.go`、`routes_admin.go`；`g.Meta` path 为完整路径 | 同路径重复注册是运行时语义冲突，通常被模块名天然隔离 |
| MQ topic / Event name | **当前项目无 MQ、无异步、无订单模块**（`docs/design/migration.md`：无 MQ） | 仅作为「未来预留分类」记录，本次不强制注册 |

**C 类：不纳入全局治理的局部资源**

局部变量名、模块内函数名、内部 struct、普通测试 fixture、单次任务内的临时命名。禁止把机制扩大成「任何名字都要注册」。（附带一项非业务观察：`.agent/tasks/<task-slug>` 目录名重名会覆盖既有任务目录，属于任务命名卫生，不属本任务治理范围。）

### 当前资源分配机制（已核实）

**Error Code 分配机制**：
- 事实来源是单一文件 `internal/codes/codes.go`：域按千位划分（1000 通用 / 2000 IAM / 3000 category / 4000 product / 5000 SKU / 6000 inventory / 7000 address / 8000 cart），域内递增编号。
- 分配链路：**Task Builder 在 `task.md` 写「建议 7000-7999」→ Analyst 在 `contract.md` 固化为「错误码域 7000-7999，新增 7001/7002」→ Owner APPROVED → Coder 写入 `codes.go`**。
- 编号依据只有一句「当前最大域 + 下一个千位段」：两份 `task.md` 都写作「地址/购物车域 7000-7999 空闲」，因为双方读到的都是同一份不含对方的旧基线。
- `docs/design/<module>.md` 各自的「错误码域」章节（如 `docs/design/address.md` §6/§7「错误码域 7000-7999 归地址域所有」）在**合并后**才由 Analyst 写入，对并行 Task 不可见。

**Migration Version 分配机制**：
- 事实来源是 `internal/migrations/sql/` 目录 + `docs/design/migration.md` 的迁移清单 + `internal/migrations/migrations_test.go` 硬编码的 `latestMigrationVersion`。
- 名义规则是「14 位时间戳 `YYYYMMDDHHMMSS`」（`docs/design/migration.md` §2.1、`migrations.go` 包注释），但实际取值为 `20261001000001~00006`，末 6 位已退化为序号，本质仍是「当前最大 version + 1」。
- 分配链路：**Analyst 在 Contract 写死 `20261001000005_cart_items.up.sql`，并附带逃逸条款「落地时若被占用取下一个更大 14 位时间戳」→ Coder 在合并时自行顺延**。该逃逸条款正是本次要治理的「Coder 自己找 next」反模式，且已被证明不能避免冲突（它只在冲突已发生后被动生效，且导致 Contract ↔ 实现漂移）。

**当前并行 Task 协调机制**：
- **不存在。** `docs/agent/*` 全篇检索无「并行 / 跨任务 / 全局资源 / reservation」相关规则；`AGENTS.md` 也无此规则；全仓库检索 `registry` / `reservation` 无治理类命中。
- 唯一事实来源是各 Feature Branch 各自读到的 `develop` 快照。因此「并行任务之间彼此不可见」是结构性事实，不是执行失误。
- 现有的长期 Design 治理（`design-doc-governance`）只解决「任务级事实 ↔ `docs/design/*` 长期事实」的沉淀与一致性，不解决「分配前谁先占位」。

### 已核实事实（其它）

- 当前 `develop` HEAD 为 `be5d1ec`（`docs(agent): 完成 owner confirmation 任务评审文档`），`777ec1d` 为 `Merge remote-tracking branch 'remotes/origin/feat/cart' into develop`；本地分支 `feat/address` 仍在，远端存在 `origin/feat/cart`、`origin/feat/iam-v3`、`origin/feat/product`、`origin/feat/sku` 等并行分支证据。
- `cart-v1` 与 `shipping-address-v1` 的 `contract.md` 均为 `APPROVED`、`findings.md` 均为 `CLEAN`，两者都已进入交付（`delivery.md`）。本任务不推翻这些结论。
- `AGENTS.md` §11 与 `docs/agent/*` 均未授权任何角色跨任务分配全局编号。

### Assumption

- 「Error code 域按千位划分」的 Domain Range 模型本身可长期保留，本任务只解决「并行不可见」，不重设计编码体系。
- Registry 可以采用纯文档（Markdown）+ 流程约束实现，无需代码、脚本或外部服务；是否额外提供可执行的检测命令由 Analyst 提案、Owner 决定（见 Analyst Questions Q8）。
- 两个并行 Task 的 Reservation 竞争可以通过「提交顺序 + 冲突检测 + 人工裁决」这类轻量手段解决，无需分布式锁。

### OPEN QUESTION（不阻塞任务创建）

- 若采用「真正时间戳唯一 ID」作为 migration version，是否需要同步修订 `docs/design/migration.md` §2.1（Design Impact 可能需校正为 `UPDATE`）。见 Analyst Questions Q2。
- `cart-v1` 的 Contract ↔ 实现编号漂移，是否在机制建立后由某个后续任务统一清理（本任务明确不做）。

## Verification

本任务不修改生产代码，验证方式以「规范可读性 + 合成 Task 场景推演 + 既有真实样本检测」为主。若最终方案提供了可执行检测命令，则命令必须可重复运行并给出明确结论；未提供时，Cleaner 必须能按文档步骤人工复现同一结论。

- AC-001 → 阅读最终治理文档：确认存在 Global Resource 的明确定义与 A/B/C 分类表，且显式包含「C 类不注册」的禁止性表述。
- AC-002/AC-003/AC-004/AC-005 → **Scenario A / Scenario B 推演**：从 `develop` 基线（ACTIVE 最大域 `6000-6999`、最新 migration `20261001000006`）切出两个互不可见的合成 Task（`Task-A` = address-like、`Task-B` = cart-like），各自按机制走完「申请 → 确认 → 进入 Coder」。预期：二者在 Registry 中看到彼此的 RESERVED 记录，取得不同错误码域与不同 migration version；且 `Task-B` 在 `feat/B` 分支上能读到 `Task-A` 的预留。
- AC-006/AC-007 → **Scenario D 推演**：`Task-B` 在 Coder 阶段发现还需要第二个 migration。预期：机制明确阻止「自行取下一个数字」，进入追加 Reservation 流程，追加后的 version 不与任何 RESERVED/ACTIVE 冲突，且流程有明确记录与批准点。
- AC-008 → **Scenario E 推演**：`Task-B` 的资源已 RESERVED 后任务被取消。预期：按机制得到确定结果（release / tombstone / never reuse 之一），且后续新 Task 的行为可预测；不允许出现「文档未定义」。
- AC-009/AC-010 → 合成不一致样本：Contract/Reservation 声明 `8000-8999` 而实现 `7001`；reserved `20261001000006` 而文件 `20261001000005`。预期：检查步骤报出不一致并给出 `CHANGES_REQUIRED` 依据。
- AC-011 → **真实样本实测**：直接把仓库现有的 `cart-v1`（Contract：`7000-7999` / `20261001000005_cart_items` / `7001-7003`；实现：`8001-8003` / `20261001000006_cart_items`）与当前 `internal/codes/codes.go`、`internal/migrations/sql/` 作为输入，按机制定义的检查步骤执行。预期：至少检出 1 处不一致；且本任务结束后该漂移仍未被修改（`git status --short` 中不含 `.agent/tasks/cart-v1/`）。
- AC-012 → **Scenario F 推演 + 基线核对**：从 `internal/codes/codes.go`（1000/2000/3000/4000/5000/6000/7000/8000）与 `internal/migrations/sql/`（00001-00006）生成 Initial Registry，逐条比对。预期：覆盖全部 8 个域与 6 个 version，且不需要修改任何历史 Task / Contract / migration 文件。
- AC-013 → **Scenario A′ 推演**：两个合成 Task 在极短间隔内同时申请同一区间（如都申请 7000-7999）。预期：至多一个 Task 合法持有，或被明确要求重新申请；不允许双方都拿到。
- AC-014 → 阅读最终流程定义：Owner 的动作集合只包含「接受 / 拒绝 Reservation（或方案）」，不含任何查号、编号、编排步骤。
- AC-015 → 阅读分类规则：确认 C 类资源清单与「新增资源类型需 Owner 确认」的判定标准。
- AC-016 → `git status --short` 与 `git diff --stat <base>`：确认变更仅落在 `.agent/tasks/global-resource-reservation/*`、`docs/agent/*` 与 Registry 文件；不含 `api/`、`internal/`、`docs/design/*`（Design Impact 校正并经 Owner 确认的情形除外）。不触及 Go 代码时不要求 `go build`/`go test`；若最终方案新增了可执行检测脚本，按其自身说明运行。
- AC-017 → 在 Scenario A/B 结论上做一次「对照本次事件」的判定：重放案例在机制下的分配结果，与本次真实后果（人工 renumber）逐项对照，确认冲突不再重现。

## Complexity

COMPLEX

原因：跨 Task、跨 Branch 的并行竞争治理；涉及多个 Agent 角色的职责重定义（Task Builder / Analyst / Owner / Coder / Cleaner）；存在多个现实方案（Reservation 预分配 vs 真正时间戳唯一 ID vs 组合机制；Registry 落点与可见性方案；生命周期状态模型；是否纳入 B 类 namespace 资源；是否提供可执行检测），不同方案会产生不同的工作流负担、可靠性与维护结果；且需要与既有 `design-doc-governance` / `agent-workflow-owner-confirmation` 治理机制保持术语与职责一致。业务代码本身不复杂，治理设计复杂度高。

## Analyst Questions

1. **Registry 落点与共享可见性**：候选方向为 ① `develop` 上维护 `.agent/registry/*`；② `docs/agent/*` 下维护 Registry；③ 复用仓库其它既有机制。硬约束是「分支切出后、Coder 开始前可读到对方的 RESERVED」。请选择并说明如何满足；同时说明它与 `docs/design/*` 已有的「错误码域」章节是什么关系（避免双事实源）。
2. **Migration version 分配方案**：A Reservation 预分配序号 / B 真正时间戳唯一 ID（`YYYYMMDDHHMMSS`）/ C 组合。已核实约束：golang-migrate 要求全局唯一且升序；`internal/migrations/migrations_test.go` 硬编码 `latestMigrationVersion`；`docs/design/migration.md` §2.1 声明「version 为 14 位时间戳」；现存 `20261001000001-00006`。若选 B/C，是否需要修订 `docs/design/migration.md`（Design Impact 是否校正为 `UPDATE`）。
3. **错误码域分配权**：由谁给出域号（Task Builder 只声明需求 / Analyst 分配 / Registry 提供 next）；域大小固定 1000 还是按需；域内具体编号（7001/7002…）由谁定、是否需要在 Contract 中逐个列出。
4. **Reservation 生命周期**：状态集（是否需要 `RESERVED` / `ACTIVE` / `RELEASED`，或更简模型）；何时从 RESERVED 转 ACTIVE（merge 到 `develop`？Contract `APPROVED`？）；Task 取消时的确定处理（release / tombstone / never reuse）；已释放区间是否允许以后复用。
5. **并行竞争防护**：在无外部锁服务、无独立服务器的约束下，如何保证「两个 Task 同时 reserve 同一区间」不会双成功（提交顺序 + 冲突检测 + 人工裁决？Registry 变更必须走 `develop`？）。
6. **角色职责固化**：Task Builder（声明需求 / 是否可临时分配）、Analyst（确认并写入 Contract / 是否可自选编号）、Owner（接受或拒绝）、Coder（只用已批准资源、新增需求走 Revision）、Cleaner（一致性检查项与判定口径）各自的动作与禁止项；Coder 阶段新增全局资源需求时，是否复用既有 `CONTRACT_REVISION` 流程。
7. **B 类 namespace 资源是否纳入**：permission code（已核实：重复会被 1062 静默跳过，产生授权缺口）、Redis key 前缀、config key、route prefix 中哪些需要 Registry，纳入的最小形式是什么（仅声明清单 vs 强校验）。MQ topic / event name 当前项目无 MQ，是否只记录为「未来预留分类」。
8. **检测手段形态**：是否提供可执行的检测命令/脚本供 Cleaner 复核（若提供，落点与依赖边界是什么，且不得引入新服务或新依赖），还是纯规范约定 + Cleaner 按步骤人工核对。两种都要保证 AC-009/AC-010/AC-011 的结论可复现。
9. **Initial Baseline 建立方式**：从 `internal/codes/codes.go` + `internal/migrations/sql/*` + `docs/design/*` 提取，还是人工确认清单；如何保证基线建立过程本身不会与正在进行的并行 Task 冲突。
10. **治理文档落点清单**：需要修改哪些既有文件（`docs/agent/AgentCollaborationSpecification.md`、`docs/agent/Five-AgentResponsibilityBoundary.md`、`docs/agent/models/TaskBuilderPrompt.md`、`AnalystAgent.md`、`CoderAgent.md`、`CleanerAgent.md`、`AGENTS.md` 的哪些部分），以及如何与 `design-doc-governance` 已确立的术语保持一致。若 Analyst 发现「必须改 Git 分支模型才能成立」，请单独给出证据并交 Owner，不要在本任务内直接扩大 Scope。

## Review Baseline

- Base commit：`be5d1ec4264ce5b2dda750dfb8fa056a81f1dcef`（分支 `develop`）。
- 任务开始时已有修改：**无**（`git status --short` 为空，working tree clean）。
- 上一个治理任务 `agent-workflow-owner-confirmation` 的产物已随 `be5d1ec` 提交进基线，工作区无未提交残留，因此不存在与该任务重叠的未归属修改；本任务不修改其产物。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/global-resource-reservation/*`、`docs/agent/*` 的治理规则修改、以及由 Analyst/Owner 确定的 Registry 文件（路径待定）。不触碰 `api/`、`internal/`、`docs/design/*`（Design Impact 校正为 `UPDATE` 并经 Owner 确认的情形除外）与任何历史 `.agent/tasks/*` 任务文件——`cart-v1`、`shipping-address-v1` 只作为证据与 AC-011 的检测样本被读取，不得被修改。审查时以 `git diff --stat be5d1ec` 与 `git status --short`（含未跟踪文件）核对上述边界。

## Initial Route

READY_FOR_ANALYST
