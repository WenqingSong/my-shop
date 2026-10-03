# Task: 历史 Design Debt 回填（Design Debt Backfill）

## Goal

在不重新设计、不改变任何现有业务行为的前提下，把已经由 Owner APPROVED 且已经实际落地的长期架构事实，从历史 `contract.md` + 最终实现 + Migration + Review 结果中提炼为项目级长期 Design（`docs/design/*`），补齐 `db-migration` / Product(SPU) / SKU / Inventory 四个已交付模块缺失的长期 Design，并处理 Category / Admin(RBAC) 的 Design 归属判断。交付后，未来接手者无需回读历史 `.agent/tasks/*` 即可理解系统核心架构事实，且每个 Design 与「最终 APPROVED Contract」和「最终 Implementation」三者一致。

## Scope

- 建立 4 个核心长期 Design Artifact，内容仅限「已 APPROVED 且已落地」的事实：
  - `docs/design/migration.md`：golang-migrate v4 职责、`migrate up/force/version` 与 `serve` 的职责分离、serve 只读 readiness check、baseline 模型、`schema_migrations`/dirty、seed 与 migration 的顺序、迁移文件命名规范、已执行迁移不可修改、多实例 `GET_LOCK` 锁语义、部署顺序（build → migrate up → serve）。
  - `docs/design/product.md`：SPU 职责边界、`products`/`product_images` 数据模型、`price` 整数分、`status` 三态状态机（`draft`/`on_shelf`/`off_shelf`，创建强制 `draft`）、条件 UPDATE + `RowsAffected` 并发迁移、前后台可见性隔离、分类「存在 + 叶子 + enabled」校验、分类删除保护（含 FK `ON DELETE RESTRICT`）、`keyword` LIKE 转义、排序白名单、图片事务一致性、RBAC 权限与错误码。
  - `docs/design/sku.md`：SPU↔SKU 1:N、`skus` 数据模型、SKU `price` 与 SPU `price` 的独立语义、`enabled`/`disabled` 二态、`(product_id, name)` 唯一约束、前后台 SKU 可见性、商品详情组合 SPU+SKU、RBAC、物理删除语义、与库存解耦、与订单引用相关的 deferred rule（`skus.id` 稳定引用键）。
  - `docs/design/inventory.md`：`inventories` 1:1 `sku_id`、`inventory_logs`、无记录 = `quantity 0`、`increase(delta)`、惰性创建、原子 upsert、`Deduct` 条件扣减（`quantity >= ?` + `RowsAffected`）、防负库存、库存变更与流水同事务、`before`/`delta`/`after`、SKU 删除 `ON DELETE RESTRICT`、库存不足 409、V1 不保证业务幂等、`request_id`/order idempotency 延后订单模块、`set absolute`/盘点 Out of Scope。
- 调查并判断 Category 与 Admin(RBAC) 的长期 Design 归属（最终由 Analyst 提案、Owner 决定，不机械扩大 Scope）：
  - Category：早期 NORMAL 任务、无独立 Contract，判断是否具备足够长期架构事实值得独立 `docs/design/category.md`（或并入 `product.md`）。
  - Admin(RBAC)：已有 APPROVED Contract 且 `docs/design/iam.md` 已部分覆盖（AdminAuth / RequirePermission / 身份域隔离），判断是否需要独立 Design（如 `rbac.md`/`admin.md`）还是维持 `iam.md` 现状不重写。
- 固化事实来源优先级与 Historical Drift（历史漂移）检测规则：`最终 APPROVED Contract > Contract Revision 的最终 Owner Decision > 最终 Implementation > Cleaner findings / core-logic / delivery > task.md`；Implementation 不得反向覆盖 Owner APPROVED Design。
- 明确 Coder 只能「提炼事实」、不能重新设计；发现 Contract 与实现不一致时记录为 Design Debt Conflict / Historical Drift，禁止静默选边。

## Out of Scope

- 修改生产代码、数据库 Schema、新增 Migration、修改 API、修改 RBAC、修改错误码、修改业务测试。
- 修改任何历史 Task / Contract / findings / core-logic / delivery（`.agent/tasks/*`、`docs/tasks/*`）。
- 重写已有的 `docs/design/iam.md`（当前与实现一致，不因职责调整回溯改写）。
- 重新打开 Product / SKU / Inventory / Migration 的设计讨论，或「顺便优化」历史架构。
- 处理未来 Order / Cart / Promotion / Cache 等假设性扩展。
- 修改刚完成的 Design Governance Workflow（`docs/agent/*` 与角色 Prompt）。
- 修改 `agents.md`（第 11 节「RBAC 未实现 / 无商品表」已过时，属独立技术债，仅记录、不修）。
- 顺手解决回填过程中发现的其他业务/技术债。

## Design Impact

Design Impact: NEW
Design Artifact: `docs/design/migration.md`、`docs/design/product.md`、`docs/design/sku.md`、`docs/design/inventory.md`、`docs/design/category.md`、`docs/design/rbac.md`

> 这里的 `NEW` 指：为「已存在但缺少长期 Design 的模块」新增 Design Artifact，而非重新设计业务模块。

## Acceptance Criteria

- [ ] AC-001：`docs/design/migration.md` 建立，覆盖最终迁移架构事实（golang-migrate v4 职责、CLI 职责分离、serve 只读 readiness、baseline/`force`/dirty、seed 顺序、命名规范、已执行迁移不可改、多实例锁语义、部署顺序），与 `db-migration` 最终 APPROVED Contract 及当前实现一致。
- [ ] AC-002：`docs/design/product.md` 建立，覆盖 SPU 最终核心架构（`products`/`product_images`、三态状态机、创建强制 `draft`、条件 UPDATE + `RowsAffected`、前后台可见性、分类「存在+叶子+enabled」、分类删除保护、价格整数分、`keyword` 转义、排序白名单、图片事务、RBAC、错误码），与 `product-spu-v1` 最终 APPROVED Contract 及当前实现一致。
- [ ] AC-003：`docs/design/sku.md` 建立，覆盖 SKU 最终核心架构（SPU↔SKU 1:N、`skus` 模型、SPU/SKU 价格独立语义、`enabled`/`disabled` 二态、`(product_id,name)` 唯一、前后台可见性、详情组合、RBAC、物理删除、库存解耦、订单引用 deferred rule），与 `sku-v1` 最终 APPROVED Contract 及当前实现一致。
- [ ] AC-004：`docs/design/inventory.md` 建立，覆盖库存最终核心架构（`inventories` 1:1、`inventory_logs`、无记录=0、`increase(delta)`、惰性创建、原子 upsert、`Deduct` 条件扣减、防负库存、同事务、`before`/`delta`/`after`、SKU 删除 RESTRICT、409、非幂等边界、`request_id` 延后），与 `inventory-v1` 最终 APPROVED Contract 及当前实现一致。
- [ ] AC-005：所有新增 Design 只包含「已由 Owner APPROVED 且已实际落地」的事实，不新增任何架构决策、表/字段、状态机迁移、API、错误码或权限。
- [ ] AC-006：每个新增 Design 与对应模块的最终 APPROVED Contract（含 Contract Revision 的最终 Owner Decision）一致，无冲突或静默选边。
- [ ] AC-007：每个新增 Design 与最终 Implementation（迁移文件、`internal/codes`、`internal/boot/seed.go`、路由与 logic）一致。
- [ ] AC-008：跨模块关系互相一致：`Product → SKU → Inventory` 三处对同一关系的描述无冲突（例如 SKU `price` 为独立实际售价、库存绑定 `sku_id` 而非 `product_id`、`skus` 不含 `stock`），且各 Design 与 `migration.md` 的建表机制一致。
- [ ] AC-009：Design 能独立解释对应模块，未来接手者不读取历史 `.agent/tasks/*` 也能理解核心架构事实（不含代码目录罗列、函数逐行说明、测试报告、Contract 全文复制、AC 清单复制）。
- [ ] AC-010：不修改任何生产代码、迁移文件、业务测试。
- [ ] AC-011：不修改任何历史 Task / Contract / findings / core-logic / delivery 文件。
- [ ] AC-012：不重写已正确且与实现一致的 `docs/design/iam.md`。
- [ ] AC-013：若发现 Historical Drift（历史 Contract 与当前实现不一致），显式记录为 Design Debt Conflict / Historical Drift，并交由治理流程决定（阻塞对应 Design 或另立 Contract Revision），禁止通过写 Design 偷偷选择一边。
- [ ] AC-014：Cleaner 完成跨模块一致性审查，验证「Task ↔ 最终 APPROVED Contract ↔ `docs/design/*` ↔ 最终 Implementation」四者一致后，方可给出 `CLEAN`。

## Relevant Context

### 已核实事实

- 治理机制已落地：`docs/agent/AgentCollaborationSpecification.md` 与 `docs/agent/Five-AgentResponsibilityBoundary.md` 已定义「任务级 Artifact vs 项目级长期 Design（`docs/design/*`）」、`Design Impact`（`NONE`/`UPDATE`/`NEW`）、`Design Artifact`、Analyst 主责 Design、Cleaner 四者一致校验；`.agent/tasks/design-doc-governance/contract.md` 为 `APPROVED`。前置检查通过，本任务不 `BLOCKED`。
- `docs/design/` 当前仅含 `iam.md`（内容正确，已覆盖前台用户 + 后台管理员 + 身份域隔离 + `AdminAuth`/`RequirePermission`）。
- 四个历史模块均有 APPROVED Contract 且实现完成：`db-migration`、`product-spu-v1`（含 CONTRACT_REVISION）、`sku-v1`、`inventory-v1`；其 `contract.md` 内含大量本应沉淀为长期 Design 的事实（数据模型、状态机、不变量、错误码、并发/一致性语义）。
- 迁移文件已落地 4 个：`internal/migrations/sql/{20261001000001_baseline, 20261001000002_products, 20261001000003_skus, 20261001000004_inventory}.up.sql`；`internal/migrations/migrations.go` 为 golang-migrate v4 封装，`internal/cmd/cmd.go` 拆为 `serve`/`migrate up|force|version`，`internal/boot/boot.go` 只做只读 readiness check + seed。
- `internal/codes/codes.go` 错误码与各 Contract 一致（通用 1000-1005、IAM 2001-2010、分类 3001-3005、商品 4001-4007、SKU 5001-5005、库存 6001-6002）。
- `internal/boot/seed.go` 现有 25 个权限（3 category + 4 product + 3 sku + 2 inventory + 4 admin + 5 role + 4 permission），与各模块 Contract 累计一致。
- `categories-v1`、`identity-domain-isolation` 为 NORMAL 任务，无 `contract.md`；其中 `identity-domain-isolation` 的边界已写入 `docs/design/iam.md`（无 Design Debt）。
- 已初步抽检：migration 机制、错误码、seed、boot、cmd 均与对应 APPROVED Contract 一致，未发现明显 Historical Drift；完整对比由 Analyst/Coder 逐项执行。

### Assumption

- 本任务 Backfill 的分工为「Analyst 固化事实来源/Design Scope/文档结构 → Owner APPROVE → Analyst 依据 APPROVED Contract 直接产出 Design Artifact → Cleaner 跨模块一致性审查」；不采用「Coder 起草 Design」方案（Owner 已确认，遵循已落地的 Design Governance：Analyst 维护长期设计事实，Coder 不作为 Design Owner，Cleaner 负责一致性校验）。该职责边界已在 `contract.md` 中厘清并获 Owner 批准。
- `docs/design/*` 的最终章节结构由 Analyst 固化（见 `contract.md`「Interfaces and Data」）。

### OPEN QUESTION

- Category 与 Admin(RBAC) 已由 Owner 决定纳入本次 Scope：分别建立独立 `docs/design/category.md` 与 `docs/design/rbac.md`。

## Verification

本任务为文档回填任务，不适用 `go test ./...` 作为主验证，采用「静态验证 + 逐项追溯 + 交叉对照 + 负向验证」：

- AC-001~AC-004 → 静态验证 `docs/design/{migration,product,sku,inventory}.md` 存在，且逐章节可追溯到对应模块的最终 APPROVED Contract 与 Owner Decision Record；不存在 Contract/实现都没有的「凭空事实」。
- AC-005 → 审查每个 Design 的每一处事实均有「APPROVED Contract」或「最终实现」证据来源；反向核对是否新增了表/字段/状态迁移/API/错误码/权限。
- AC-006 → Contract 对照：对每个 Design 的核心章节（数据模型、状态机、不变量、错误码、并发/一致性）逐项追溯到最终 APPROVED Contract（含 Revision 的 Owner Decision）。
- AC-007 → Implementation 对照：抽取关键事实核对真实代码/迁移，如 Product `status` 映射（`0/1/2`）、SKU 表字段（含 `uk_product_name`、FK `ON DELETE RESTRICT`）、Inventory `quantity`/`uk_sku_id`/FK、`migrate up|force|version` CLI、`serve` 只读 readiness、各域错误码与 `seedPermissionList`。
- AC-008 → Cross-Module 对照：确保 `product.md`/`sku.md`/`inventory.md`/`migration.md` 对同一关系描述一致（如「SKU `price` 为独立实际售价」「库存绑定 `sku_id`」「`skus` 不含 `stock`」「三表均经 golang-migrate 新增」）。
- AC-009 → 通读每个 Design，确认能脱离历史 Task 独立解释模块，且不包含代码目录罗列、测试报告、Commit 日志、Contract/AC/findings 全文复制。
- AC-010/AC-011/AC-012 → Git 基线核对：本次 Review Target 仅含 `docs/design/*`（新增文件）与 `.agent/tasks/design-debt-backfill/*`，不包含生产代码、历史任务文件、`docs/design/iam.md`。
- AC-013 → 负向验证 + 漂移检测：故意检查 Cleaner 能否识别「写了不存在的 `stock` 字段」「把 SKU `price` 说成由 SPU 自动派生」「宣称 `Deduct` 幂等」「宣称 serve 自动执行 migration」「宣称 SKU 复用 SPU 三态」等错误 Design；若历史 Contract 与实现不一致，须显式记录而非静默修正。
- AC-014 → 由 Cleaner 执行四者一致（Task ↔ 最终 APPROVED Contract ↔ `docs/design/*` ↔ 最终 Implementation）审查，结论记录于 `findings.md`。

## Complexity

COMPLEX

原因：涉及 6 个 Design Artifact、多份历史 Contract（含 Revision）与最终实现事实；需要固化事实来源优先级、检测 Historical Drift、定义统一 Design 结构、处理跨模块关系（Product↔SKU↔Inventory 及 Migration 作为建表基础）与跨模块一致性审查；「谁写 Design」「Category/RBAC 是否纳入」存在真实归属分歧，会产生不同治理结果。已由 Analyst 固化方案并经 Owner 确认。

## Analyst Questions

1. **事实来源优先级固化**：Backfill 的事实来源优先级（最终 APPROVED Contract > Revision 的 Owner Decision > 最终实现 > Cleaner findings/core-logic/delivery > task.md）如何具体落地？当 Contract 与实现不一致时，如何区分「Historical Drift 需阻塞/另立 Revision」与「可记录的描述性差异」，判定标准与处理路径是什么。
2. **Backfill 中「谁写 Design」的职责边界**（已解决）：Owner 确认由 Analyst 直接产出长期 Design，不采用「Coder 起草」方案；Coder 不作为 Design Owner，Cleaner 负责一致性校验。
3. **Design 文档结构与粒度**：统一每个 `docs/design/*` 的章节结构（WHY/WHAT/MODEL/INVARIANTS/LIFECYCLE/CONSISTENCY/SECURITY/CROSS-MODULE/DEFERRED），以及如何避免退化为 Contract 的机械全文复制；文件按模块一文件（`migration.md`/`product.md`/`sku.md`/`inventory.md`）的命名与边界。
4. **Category 的 Design 归属**：`categories-v1` 为 NORMAL 无 Contract 任务，但其树形结构、`maxLevel=3`、`(parent_id,name)` 唯一、`status` 软下线、删除保护（有子分类 + 有商品）等已是长期事实。判断是否建立独立 `category.md`，还是并入 `product.md`，或维持不建（附证据）。
5. **Admin(RBAC) 的 Design 归属**：`admin-identity-rbac` 有 APPROVED Contract，`iam.md` 已覆盖 AdminAuth/RequirePermission/身份域隔离。判断 RBAC 是否需要独立 Design（`rbac.md`/`admin.md`）、扩充 `iam.md`、还是维持 `iam.md` 现状不重写；给出边界判断，不机械扩 Scope。
6. **Historical Drift 检测清单**：对每个模块，确定必须逐项对比的关键事实点（状态机映射、表字段/约束/FK、错误码、权限、并发/一致性语义、CLI），以及漂移的登记格式与升级路径。
7. **产出顺序与跨模块一致性**（已解决）：由 Analyst 依次产出 6 个 Design（`migration.md` → `product.md` → `sku.md` → `inventory.md` → `category.md` → `rbac.md`）；`Product↔SKU↔Inventory` 交叉引用按 Contract 冻结的跨模块口径表统一，Cleaner 做四者一致跨模块审查。

## Review Baseline

- Base commit：`34dae87251dbc4a6ebf9785cba4664c20d31f91c`（分支 `develop`）
- 任务开始时已有修改：无（`git status --short` 为空，working tree clean）
- 重叠修改的区分方式：本任务 Review Target 原则上仅包含 `docs/design/*`（本任务新增的 Design 文件）与 `.agent/tasks/design-debt-backfill/*`；不触碰生产代码、历史任务目录、`docs/design/iam.md`。当前工作区干净，无既有未提交修改需区分。

## Initial Route

READY_FOR_ANALYST
