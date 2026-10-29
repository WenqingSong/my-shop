# Technical Contract

## Decision Status
APPROVED

## Problem

多个 Feature Branch 从相近的 `develop` 基线并行开发，各自在「旧基线」上独立决定全局唯一资源编号（错误码域、migration version），形成 `read → read → allocate → allocate` 的并发竞争：`feat/address` 与 `feat/cart` 各自合法选中同一错误码域 `7000-7999` 与同一 migration `20261001000005`，冲突直到 merge 时才被发现、再人工 renumber。根因是：当前 Workflow 不存在 Reservation / Registry / Conflict Detection，全局资源分配事实只在「合并进 `develop` 之后」可见，而分配决策发生在「合并之前」。

本任务交付一套最小、明确、可验证的 **Global Resource Reservation 治理机制**：任何需要占用「项目级全局唯一资源」（至少含 Error Code Domain 与 Migration Version）的 Task，在进入 Coder 前必须从**所有并行 Task 都能读到的共享事实源**取得独占分配；Coder 只能用已预留资源；Cleaner 能按确定步骤检出「Reservation ↔ 实现」不一致。

## Verified Current Behavior

- VERIFIED：错误码事实源为单一文件 `internal/codes/codes.go`，域按千位划分、域内递增；当前 ACTIVE 域为 `1000`（通用）/`2000`（IAM）/`3000`（category）/`4000`（product）/`5000`（SKU）/`6000`（inventory）/`7000`（address，7001/7002）/`8000`（cart，8001/8002/8003）。`codeTable` 若出现重复 key 是 Go 编译错误。
- VERIFIED：migration 事实源为 `internal/migrations/sql/{version}_{title}.up.sql` + `schema_migrations`；当前版本 `20261001000001~00006`；`internal/migrations/migrations_test.go` 硬编码 `latestMigrationVersion = 20261001000006` 与 `businessTables`。golang-migrate 要求 version 全局唯一且升序；同一 version 两个文件会导致载入/执行冲突。
- VERIFIED：`docs/design/migration.md` §2.1 声明「version 为 14 位时间戳 `YYYYMMDDHHMMSS`」，但实际取值 `20261001000001~00006` 已退化为「日期前缀 + 序号」，本质是「当前最大 version + 1」；`docs/design/address.md` §7 与 `docs/design/cart.md` §8 各自声明「错误码域 7000-7999 / 8000-8999 归本域所有」。
- VERIFIED：`cart-v1/contract.md`（`APPROVED`）仍记录「错误码域 7000-7999 / 7001-7003 / migration `20261001000005_cart_items`」，而最终实现为 `8001/8002/8003` 与 `20261001000006_cart_items`（`internal/codes/codes.go`、`internal/migrations/sql/`）；`findings.md` 为 `CLEAN`，漂移未被任何机制检出——这是「缺 Reservation ↔ 实现一致性检查」的直接证据，也是 AC-011 的检测样本。
- VERIFIED：`docs/agent/*`（协同规范、五角色职责边界、5 个角色 Prompt）与 `AGENTS.md` 全篇**无**「并行 / 跨任务 / 全局资源 / reservation」相关规则；无任何角色被授权跨任务分配全局编号。唯一事实来源是各 Feature Branch 各自读到的 `develop` 快照。
- VERIFIED：B 类 namespace 资源现状——permission code（`internal/boot/seed.go`，`permissions.code` UNIQUE，并发重复靠 1062「跳过」，存在「同 code 不同 name 先落库者胜、后者静默跳过」的授权缺口）；Redis key 前缀（`internal/auth/session.go`：`iam:session:`/`iam:admin:session:`/`iam:user:{id}:sessions`，已有模块前缀约定）；config key（`manifest/config/config.yaml`，扁平命名空间）；route prefix（`internal/cmd/routes_*.go`，`g.Meta` 完整路径）。当前无 MQ、无异步。
- VERIFIED：`design-doc-governance` 已确立「Design Impact（NONE/UPDATE/NEW）」「任务级 Artifact ↔ 长期 Design 分工」「CONTRACT_REVISION」等术语与流程；本机制须与之保持一致。
- VERIFIED：Git 分支模型不是本次根因（并行分支证据：`origin/feat/cart`、`origin/feat/iam-v3`、`origin/feat/product`、`origin/feat/sku` 等）；本机制在既有分支模型内可成立，无需改分支模型。
- UNKNOWN：无阻塞性 UNKNOWN。

## Recommendation

RECOMMENDATION：采用「**develop 上的共享 Registry（纯 Markdown）+ 提交顺序串行化 + 最小只读 Validator + Cleaner 三边一致性检查**」的单一方案，覆盖 A 类强全局唯一资源（Error Code Domain 与 Migration Version），不纳入 B/C 类。

### 1. Registry 落点与共享可见性（Q1）

Registry 落点 `.agent/registry/`（与 `.agent/tasks/` 平级），两个数据文件：

- `.agent/registry/error-codes.md`：错误码域分配表。
- `.agent/registry/migrations.md`：migration version 分配表。

**关键机制**：Reservation 事实必须落在 `develop` 上的 Registry（而不是 Feature Branch 私有文件）。Reservation 的生效动作 = 一个**只改 Registry 文件的 commit 落在 `develop`**；Feature Branch 在 Coder 开始前 `rebase/merge develop`（或至少取回最新 Registry），从而读到其他并行 Task 的 RESERVED。Feature Branch 内自行声明 Reservation 不视为有效预留。这直接满足 AC-004（`feat/X` 能读到 `feat/Y` 的 RESERVED）。

与 `docs/design/*` 的关系（避免双事实源）：Registry 是**分配状态**的权威事实源（谁 RESERVED/ACTIVE/RELEASED 了哪个域/version）；`docs/design/<module>.md` 的「错误码域」章节与 `docs/design/migration.md` 的迁移清单是**合并后模块语义归属与内容**的长期事实，二者互补、不复制。Cleaner 的检查以 Registry 为「分配」权威，`codes.go`/`migrations sql` 为「实现」事实。

### 2. Migration version 分配方案（Q2）

**方案 A（Reservation 预分配序号，Owner 已确认）**：保留现有 `YYYYMMDD + 序号` 的实际格式，由 Registry 给出 `max(所有已记录 version, 含 RELEASED) + 1` 作为下一个 version（整数自增），在进入 Coder 前写入 Contract。两个并行 Task 因 Registry 是 develop 上的单调事实源、且提交串行化，必然取得不同 version。**禁止 Coder 根据当前 migration 最大值自行执行 `max+1`。** 不改 `docs/design/migration.md` §2.1 → **Design Impact 保持 NONE**。

### 3. 错误码域分配权（Q3）

- **Task Builder**：只声明「本任务新增错误码域（语义：XX）」，**不写任何具体域号**（废止「建议 7000-7999」）。
- **Analyst**：读 Registry，按规则 `next = max(已记录域) + 1000` 派生下一个空闲域，写入 Contract 与 Registry 的 RESERVED 条目。
- 域大小固定 1000（保留既有「按千位划分」约定），不按需伸缩。
- 域内具体编号（7001/7002…）由 Analyst 在 Contract 中逐个列出（沿用现状），因域本身独占，域内编号无跨任务冲突。

### 4. Reservation 生命周期（Q4）

状态集三态：

- `RESERVED`（已申请、尚未合并进 `develop`；Coder 进行中）。
- `ACTIVE`（已合并进 `develop`，资源在 `develop` 实际生效；终态）。
- `RELEASED`（Task 取消释放；记录保留）。

转换：

- `RESERVED → ACTIVE`：feature 合并进 `develop` 时，合并任务同步把该条目状态更新为 `ACTIVE`。
- `RESERVED → RELEASED`：Task 取消时，由 Analyst/Owner 在 Registry 标记释放。

**取消处理按资源类型区分（Owner 已确认）**：

- **Migration Version**：一旦被 Reservation 分配，即使 Task 后续取消，也永久保留 tombstone，**不得复用**；`next` 恒为 `max(所有已记录值, 含 RELEASED) + 1`。
- **Error Code Domain**：仅当处于纯 `RESERVED` 阶段（尚未 APPROVED Contract 落地、尚未实现、尚未 merge）时可 `RELEASE` 后重新分配；一旦进入正式实现或成为已落地事实，则**不可复用**。

不采用「所有资源 RELEASED 永不复用」的统一规则。

### 5. 并行竞争防护（Q5、AC-013）

无外部锁、无独立服务、无新运行时依赖。以 **Git 提交顺序 + 冲突检测 + 人工裁决** 串行化：Registry 是 develop 上的单一共享文件，Reservation 只有在「只改 Registry 的 commit 落在 develop」后才生效；两个 Task 同时申请同一域/version 时，**先提交到 develop 者胜**，后提交者在 rebase/合并时看到 Registry 已被占用，必须重新申请不同值（或被明确要求重新申请）。结果满足 AC-013「至多一个 Task 合法持有」。Owner 全程只需「接受/拒绝 Reservation 方案」，不做任何查号/编号（AC-014）。

### 6. 角色职责固化（Q6）

| 角色 | 允许 | 禁止 |
| --- | --- | --- |
| Task Builder | 声明全局资源需求（类型 + 语义） | 写具体域号/version 号 |
| Analyst | 读 Registry 派生 next、写 RESERVED 条目与 Contract | 凭空自选编号（必须由 Registry 派生） |
| Owner | 接受/拒绝 Reservation 方案 | 手工查号/编号/编排区间 |
| Coder | 只使用 APPROVED Contract 中的已分配资源 | 自行推断「我看到最大是 X，所以写 X+1」 |
| Cleaner | 三边一致性检查 Registry ↔ Contract ↔ 实现 | 自行改编号/放行漂移 |

Coder 阶段新增全局资源需求 → 走**既有 `CONTRACT_REVISION` 流程**（追加 Reservation，Analyst 派生新值 → Owner 批准 → 更新 Contract 与 Registry）。

### 7. B/C 类资源（Q7、AC-001、AC-015）

- A 类（migration version、error code 域/编号）：纳入 Registry，强治理。
- B 类（permission code、Redis key 前缀、config key、route prefix、MQ topic）：**只分类、不纳入 Registry**。在分类表中记录其现状与「可靠命名空间约定」，并标注 permission code 的「1062 静默跳过 → 授权缺口」为**已知风险**（后续有真实需求再独立治理，不在本任务）。
- C 类（局部变量/模块内函数/内部 struct/普通测试 fixture/临时命名）：**明确禁止以任何形式要求注册**。新增资源类型进入治理须有明确判定标准且经 Owner 确认。

### 8. 检测手段形态（Q8 之二）

**采用「规范 + 最小只读 Validator」（Owner 已确认）**。Validator 仅机械检查：

1. Error Code Domain Reservation 是否重复；
2. Migration Version Reservation 是否重复；
3. Registry 与 `codes.go`/`migrations sql` 实际使用是否存在明显不一致。

**Validator 不得自动分配资源、不得修改 Registry、不得替代 Analyst/Cleaner 的语义判断**；Cleaner 仍负责最终的 Registry ↔ Contract ↔ Implementation 一致性审查。

### 9. Initial Baseline 建立（Q9）

从 `internal/codes/codes.go`（8 个域）与 `internal/migrations/sql/`（6 个 version）+ `docs/design/*` 提取，生成 Registry 的 ACTIVE 基线条目，随 Registry 初始提交落入 develop。基线只记录「已合并生效」事实，不写 RESERVED，因此不占用、也不与任何在途并行 Task 冲突。

### 10. 治理文档落点（Q10）

- `docs/agent/AgentCollaborationSpecification.md`：新增「跨任务全局资源预留」章节（机制、生命周期、冲突处理）。
- `docs/agent/Five-AgentResponsibilityBoundary.md`：五角色各自新增 reservation 职责/禁止项。
- `docs/agent/models/TaskBuilderPrompt.md`、`AnalystAgent.md`、`CoderAgent.md`、`CleanerAgent.md`：对应角色补 reservation 动作与禁止项。
- `AGENTS.md`：按需新增一条长期规则（引用 `docs/agent/*` 为唯一来源，不复制全文）。
- Registry 本体：`.agent/registry/error-codes.md`、`.agent/registry/migrations.md`。

术语与 `design-doc-governance` / `agent-workflow-owner-confirmation` 一致：`Design Impact`、`CONTRACT_REVISION`、五角色边界、`Owner 确认` 流程。

## Selected Design

经 Owner 确认（2026-10-04），采用本 Contract「Recommendation」中的单一方案，关键选择固化如下：

1. **Registry 落点**：`.agent/registry/`，以 `develop` 上的共享事实为准。Reservation 必须先通过独立的 Registry 变更进入共享 `develop`，Feature Task 才能使用对应资源；Feature Branch 内自行声明 Reservation 不视为有效预留。
2. **Migration version 方案 A**：保留当前编号模型，通过共享 Registry 预分配唯一 version；不在本任务切换真正时间戳方案；禁止 Coder 自行 `max+1`。Design Impact 保持 NONE。
3. **取消复用规则按资源类型区分**：Migration Version 一旦分配即永久 tombstone、不复用；Error Code Domain 仅纯 RESERVED 阶段（未 APPROVED 落地/未实现/未 merge）可 RELEASE 后复用，已落地即不复用。
4. **B 类资源**：本轮只分类 + 风险记录，不纳入 Registry；Permission Code 等冲突后续有真实需求再独立治理。
5. **检测形态**：规范 + 最小只读 Validator（机械重复/漂移检查），Validator 不分配、不改 Registry、不替代语义判断，Cleaner 做最终一致性审查。

## Interfaces and Data

### Registry 数据文件结构

`.agent/registry/error-codes.md`（每行一条，含基线 ACTIVE 条目）：

```text
| 域区间 | 拥有方（任务/模块） | 状态 | 备注 |
| 1000-1999 | 通用 | ACTIVE | 基线 |
| ...
| 9000-9999 | <task-slug> | RESERVED | 日期 + 说明 |
```

`.agent/registry/migrations.md`：

```text
| version | title | 拥有方（任务） | 状态 | 备注 |
| 20261001000001 | baseline | db-migration | ACTIVE | 基线 |
| ...
```

状态取值仅 `RESERVED` / `ACTIVE` / `RELEASED`；`next` 由规则派生（error code 域：`max(已记录域上限)+1000`；migration：`max(已记录 version)+1`，且 RELEASED 的 migration 仍计入 `max`），不维护易腐的显式 next 指针。

### Initial Baseline 事实

- 错误码域 ACTIVE：1000-1999（通用）、2000-2999（IAM）、3000-3999（category）、4000-4999（product）、5000-5999（SKU）、6000-6999（inventory）、7000-7999（address）、8000-8999（cart）。
- migration ACTIVE：`20261001000001~00006`。

### 最小只读 Validator

- 落点：`scripts/`（与既有 `.sh` 工具同级），单文件、无新外部依赖（shell 或 Go 标准库），不进入 `internal/`/`api/`，不参与运行时代码路径。
- 行为：只读、确定性、可重复运行，给出明确 pass/fail 结论；仅检查「错误码域 Reservation 重复」「migration version Reservation 重复」「Registry 与实际 codes/migration 使用明显不一致」三类。
- 边界：不得自动分配资源、不得修改 Registry、不得替代 Analyst/Cleaner 语义判断。Cleaner 仍负责 Registry ↔ Contract ↔ Implementation 的最终一致性审查（含语义层面）。

### 需要保持的既有接口/事实

- `internal/codes/codes.go` 的现有错误码取值、`internal/migrations/sql/*` 的现有迁移、`docs/design/*` 现有内容、历史 `.agent/tasks/*` 全部不动（`cart-v1`/`shipping-address-v1` 仅作为证据与 AC-011 样本读取）。

## Business Invariants

- INV-001（错误码域独占）：任一错误码千位域在 Registry 中至多被一个处于 `RESERVED`/`ACTIVE` 的任务持有。
- INV-002（migration version 唯一 + 单调 + tombstone）：任一 migration version 至多被一个任务持有；新预留 version 严格大于 Registry 中所有已记录 version；已分配（含 `RELEASED`）的 migration version 永不复用。
- INV-003（禁止 Coder 自选）：Coder 实现中出现的每个错误码域/编号与 migration version，都必须已存在于 APPROVED Contract 的全局资源清单；未预留即禁止自行占用（含 `max+1`）。
- INV-004（共享可见性）：每条 `RESERVED`/`ACTIVE`/`RELEASED` 记录必须位于 `develop` 的 `.agent/registry/*`；仅存在于 Feature Branch 私有文件的记录不构成有效 Reservation。
- INV-005（三边一致）：Cleaner 以 `.agent/registry/*` 为分配权威，Contract 声明 ↔ Registry ↔ 实现（`codes.go` / `migrations sql` / `migrations_test.go`）三者必须一致，任一漂移 → `CHANGES_REQUIRED`。
- INV-006（错误码域复用边界）：错误码域仅当处于纯 `RESERVED` 阶段（尚未 APPROVED Contract 落地、尚未实现、尚未 merge）才可 `RELEASE` 后复用；一旦进入实现或合并即不可复用。

## Failure and Consistency Semantics

- 事实源：`.agent/registry/*`（develop 上）是**全局资源分配**的权威事实源；`internal/codes/codes.go` 与 `internal/migrations/sql/*` 是**已合并生效实现**的事实源；`contract.md` 是任务级承诺。
- Reservation 成功 = 其 `RESERVED` 条目已提交到 develop 的 Registry，且不与既有 `RESERVED`/`ACTIVE` 条目冲突。
- 并发/竞争：Registry 变更必须落在 develop（单写者线性历史）。两个 Task 同时申请同一域/version 时，先提交者胜；后提交者在 rebase/合并时检测到冲突，必须重新申请不同值或被明确要求重新申请，**不得静默采用冲突值**（INV-001/002）。
- `ACTIVE` 转换：feature 合并进 develop 时，由合并任务同步把该条目 `RESERVED → ACTIVE`。
- `RELEASED`：Task 取消时 `RESERVED → RELEASED`。复用规则分资源类型：migration version 永不复用（tombstone，仍计入 `max`）；错误码域仅纯 RESERVED 阶段可复用，已实现/已落地不可复用（INV-006）。
- 部分完成/重试：Registry 提交是原子文件修改；提交失败或 rebase 冲突不产生半生效状态（要么落在 develop、要么重新申请）。
- 本机制无 Redis、无 MQ、无跨系统事务、无运行时组件；「一致性」体现为 Git 线性历史 + Validator 机械检查 + Cleaner 三边核对，不引入运行时锁。

## Design Impact

Design Impact：NONE。本任务产物为 Agent 协同治理规则 + Reservation 共享事实源 + 只读 Validator，落点 `docs/agent/*`、`.agent/registry/*`、`scripts/`，不改变 `docs/design/*` 记录的业务/技术长期事实。Migration 采用方案 A（保留现有编号模型），不改 `docs/design/migration.md` §2.1，故不触发校正条件，不产出 Design Artifact。

## Allowed / Forbidden Changes

允许：
- 新建 `.agent/registry/error-codes.md`、`.agent/registry/migrations.md`（含 Initial Baseline）。
- 修改 `docs/agent/*`（协同规范、职责边界、5 个角色 Prompt）与 `AGENTS.md` 的 reservation 相关部分。
- 新增 `scripts/` 下的最小只读 Validator（单文件、无新外部依赖、不参与运行时）。
- 维护本任务 `.agent/tasks/global-resource-reservation/*`。

禁止：
- 修改 `api/`、`internal/` 任何业务代码；修改 `internal/codes/codes.go` 现有取值；修改 `internal/migrations/sql/*` 现有迁移。
- 修改 `docs/design/*`（Design Impact = NONE）。
- 修改历史任务 `.agent/tasks/cart-v1/*`、`.agent/tasks/shipping-address-v1/*`（仅读取作证据/样本）。
- 修改 Git 分支模型；引入外部锁服务/独立服务/新运行时依赖。
- Validator 自动分配资源、修改 Registry、或替代 Analyst/Cleaner 语义判断。
- 把机制扩大为「任何名字都要注册」（C 类禁止注册）。

## Verification Requirements

- INV-001 → Scenario A′ 推演：两个合成 Task 同时申请同一域（如都申请 9000-9999），断言至多一个在 Registry 中合法持有，另一个被要求重新申请（AC-013）。
- INV-002 → Scenario A/B 推演 + `migrations_test.go`：两 Task 各新增一个 migration，断言版本不同且 `max+1`；RELEASED 的 migration version 不被复用（AC-003、AC-008）。
- INV-003 → Scenario D 推演：Coder 阶段新增第二个 migration，断言被阻止自行取号、走追加 Reservation，追加值不与任何 RESERVED/ACTIVE 冲突（AC-006/007）。
- INV-004 → Scenario B 推演：`feat/B` 分支上能读到 `feat/A` 在 Registry 的 RESERVED 记录（AC-004）。
- INV-005 → 合成不一致样本（Contract 声明 8000-8999 而实现 7001；reserved `20261001000006` 而文件 `20261001000005`）报 `CHANGES_REQUIRED`（AC-009/010）；真实样本 `cart-v1` 漂移（Contract 7000-7999/00005/7001-7003 vs 实现 8001-8003/00006）至少检出 1 处不一致，且 `git status --short` 不含 `.agent/tasks/cart-v1/`（AC-011）。
- INV-006 → Scenario E 推演：错误码域纯 RESERVED 阶段取消后可复用；已实现/已 merge 的域取消后不可复用；migration version 取消后永不复用（AC-008）。
- AC-001/AC-015 → 阅读分类表：A/B/C 分类齐全且显式含「C 类不得以任何形式要求注册」。
- AC-002/AC-017 → Scenario A/B 对照本次 Address+Cart 事件：二者取得不同域与不同 version，冲突不再重现。
- AC-012 → Scenario F：从 `codes.go` 与 `migrations sql` 生成 Initial Registry，逐条比对覆盖 8 域 + 6 version，无需改任何历史文件。
- AC-014 → 阅读流程定义：Owner 动作仅「接受/拒绝」，无查号/编号步骤。
- Validator → 按脚本自身说明运行，对「重复 Reservation」「Registry ↔ 实现明显漂移」给出确定 pass/fail；对 `cart-v1` 漂移样本至少报出 1 处不一致。
- AC-016 → `git status --short` 与 `git diff --stat be5d1ec`：变更仅落在 `.agent/tasks/global-resource-reservation/*`、`docs/agent/*`、`AGENTS.md`、`.agent/registry/*`、`scripts/`（Validator）；不含 `api/`、`internal/`、`docs/design/*`。

## Open Risks

- **错误码 Domain 空间接近耗尽（非阻塞 Follow-up）**：当前 1000-9999 空间已用 1000-8000（仅余 9000-9999 一个域），未来 Order/Payment 等模块需独立评估 Error Code Namespace 扩展方案，本任务不处理。
- permission code 的「1062 静默跳过 → 授权缺口」是真实潜在风险，但不属本任务（后续有真实需求再独立治理），已在分类表标注。
- Validator 只做机械检查，无法覆盖语义层一致性（如「域号正确但语义归属错」），这类仍需 Cleaner 人工审查——已在角色职责中明确，不构成机制缺口。

## Owner Decision Record

Owner 于 2026-10-04 确认以下决定（均与 Task 兼容，不改变 Goal/Scope/AC）：

1. **Registry 落点与可见性**：确认 `.agent/registry/` 为 Global Resource Registry，以 `develop` 上的共享事实为准；Reservation 必须先通过独立 Registry 变更进入共享 `develop` 后，Feature Task 才能使用对应资源；Feature Branch 内自行声明 Reservation 不视为有效预留。
2. **Migration version 方案 A**：继续保持当前编号模型，通过共享 Registry 预分配唯一 version；不在本任务切换真正时间戳方案；禁止 Coder 根据当前 migration 最大值自行 `max+1`。
3. **取消复用规则按资源类型区分**：Migration Version 一旦分配即永久 tombstone、不得复用；Error Code Domain 仅纯 Reservation 阶段（未 APPROVED Contract/实现/merge）可 RELEASE 后重新分配，已落地即不得复用；不采用「所有资源 RELEASED 永不复用」的统一规则。并记录非阻塞 Follow-up Risk：1000-9999 错误码 Domain 空间接近耗尽，未来 Order/Payment 等模块需独立评估 Error Code Namespace 扩展方案，本任务不处理。
4. **B 类资源**：本轮只完成分类与风险记录，不纳入 Registry；Permission Code 等潜在冲突后续有真实需求再独立治理，不扩大本任务 Scope。
5. **检测形态**：采用「规范 + 最小只读 Validator」；Validator 仅机械检查（Error Code Domain Reservation 重复、Migration Version Reservation 重复、Registry 与实际 codes/migration 使用明显不一致）；Validator 不得自动分配资源、修改 Registry、替代 Analyst/Cleaner 语义判断；Cleaner 仍负责最终 Registry ↔ Contract ↔ Implementation 一致性审查。

Design Impact = NONE，无 Design Artifact 产出，本任务 Coder 实现范围仅 Registry + `docs/agent/*` + `AGENTS.md` + `scripts/` Validator。Contract 转 `APPROVED`。
