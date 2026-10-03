# Technical Contract

## Decision Status

WAITING_FOR_OWNER_APPROVAL

## Problem

把 4 个已交付模块（db-migration / Product(SPU) / SKU / Inventory）中「已由 Owner APPROVED 且已实际落地」的长期架构事实，从历史 `contract.md` + 最终实现 + Migration + Review 结果中提炼为项目级长期 Design（`docs/design/*`），并判断 Category / Admin(RBAC) 的 Design 归属。交付物是长期 Design 文件，不改任何生产代码、Schema、历史任务文件或既有 `docs/design/iam.md`。

本任务本质是「Design 内容生产」，不是「代码实现」；核心难点不是设计新模块，而是：① 固化事实来源优先级与 Historical Drift 检测规则；② 厘清 Backfill 场景下「谁写 Design」的职责边界（Task 的 Assumption 与治理规范存在冲突）；③ 统一 Design 文档结构；④ 判定 Category / Admin(RBAC) 归属；⑤ 保证跨模块口径一致。

## Verified Current Behavior

- VERIFIED：治理机制已落地且 `CLEAN`。`docs/agent/AgentCollaborationSpecification.md` 第 3 节与 `docs/agent/Five-AgentResponsibilityBoundary.md` 已定义「任务级 Artifact vs 项目级长期 Design（`docs/design/*`）」、`Design Impact`（`NONE`/`UPDATE`/`NEW`）、`Design Artifact`、Analyst 主责 Design、Cleaner 四者一致校验；`.agent/tasks/design-doc-governance/contract.md` 为 `APPROVED`，其 `findings.md` 为 `CLEAN`（仅遗留 P3 CLEAN-001，不阻塞）。
- VERIFIED：治理规范明确「Analyst 是长期 Design 内容主责」「Coder 不是长期 Design 所有者、不写 Design」「`Design Impact = NEW/UPDATE` 时在 Contract `APPROVED` 后、Coder 实现前由 Analyst 新增/更新 Design」。`Five-AgentResponsibilityBoundary.md` 第 62/83/104/113/126 行与 `AgentCollaborationSpecification.md` 第 61 行均如此。
- VERIFIED：本任务 `task.md` 的 Assumption 写「Coder 依据 APPROVED Scope 起草 Design 文件」，与上述治理规范「Analyst 主责 Design、Coder 不写 Design」冲突；`task.md` 已把该冲突列为 Analyst Question #2，交 Analyst 在 Contract 中厘清。
- VERIFIED：`docs/design/` 当前仅含 `iam.md`，内容覆盖前台用户 + 后台管理员 + 身份域隔离 + `AdminAuth`/`RequirePermission` 的鉴权与 Session 部分，但**不覆盖 RBAC 授权数据模型**（`roles`/`permissions`/`admin_roles`/`role_permissions` 四表、权限 code 清单、超级管理员保护、自操作防护、删除清理语义、2003-2010 错误码）。
- VERIFIED：4 个核心模块的 Contract 均 `APPROVED` 且实现已完成：`db-migration`（golang-migrate v4 + CLI 职责分离 + serve 只读 readiness + baseline/force/dirty + GET_LOCK）、`product-spu-v1`（含 CONTRACT_REVISION）、`sku-v1`、`inventory-v1`。其 `contract.md` 内含大量本应沉淀为长期 Design 的事实（数据模型、状态机、不变量、错误码、并发/一致性语义）。
- VERIFIED：最终实现与各 Contract 一致（逐项抽检）：
  - `internal/migrations/migrations.go`：golang-migrate v4 封装，`Up`/`Force`/`Status`（只读），`//go:embed` 内嵌、`iofs`、`multiStatements=true`、MySQL driver 默认 `GET_LOCK`；`Status` 只读不建表、不执行 DDL。
  - 4 个迁移文件：`20261001000001_baseline`（7 张既有表，去掉 `IF NOT EXISTS`）、`...0002_products`（`products`+`product_images`，`category_id` FK `ON DELETE RESTRICT`）、`...0003_skus`（`uk_product_name`、`product_id` FK RESTRICT、无 `stock`）、`...0004_inventory`（`inventories` 1:1 `uk_sku_id` + `inventory_logs`，`sku_id` FK RESTRICT）。
  - `internal/cmd/cmd.go`：`serve` + `migrate up|force|version` 子命令，无参数默认 serve；`internal/boot/boot.go` 只做 `migrations.Status` 只读 readiness + seed，无建表。
  - `internal/codes/codes.go`：通用 1000-1005、IAM 2001-2010、分类 3001-3005、商品 4001-4007、SKU 5001-5005、库存 6001-6002，与 Contract 一致。
  - `internal/boot/seed.go`：25 个权限（3 category + 4 product + 3 sku + 2 inventory + 4 admin + 5 role + 4 permission），与各模块累计一致。
  - 四模块 logic 与 Contract 一致：Product `status` 0/1/2 + 条件 UPDATE + `RowsAffected` + 分类「存在+叶子+enabled」+ keyword 转义 + 排序白名单 + 图片事务；SKU `price` 独立 + `enabled`/`disabled` 1/0 + 物理删除；Inventory `increase` 原子 upsert + `Deduct` 条件扣减 + 同事务流水。
- VERIFIED：`categories-v1`、`identity-domain-isolation` 为 NORMAL 任务，无 `contract.md`。`identity-domain-isolation` 边界已写入 `iam.md`（无 Design Debt）。Category 的长期事实（树形结构、`maxLevel=3`、`(parent_id,name)` 唯一、`status` 软下线、删除保护「有子分类 3003 + 有商品 3005/FK」、`Exists`/`HasChildren`/`IsEnabled` 跨模块服务边界）当前散落在 `categories-v1/task.md`（NORMAL 无 Contract）+ `product-spu-v1/contract.md`（跨模块部分）+ 最终实现。
- VERIFIED：`admin-identity-rbac/contract.md` 为 `APPROVED`，含 RBAC 授权数据模型（5 表、16 权限起步→现 25、超级管理员保护、自操作防护、401/403 判定、删除清理、2003-2010 错误码）；这部分未沉淀进 `iam.md`。
- VERIFIED：`categories-v1/findings.md` 曾 `CHANGES_REQUIRED`（CLEAN-001 P1 第 4 级子树层级、CLEAN-002 P3 缺失 404 测试）；当前 `internal/logic/categories/categories.go` 已含 `subtreeDepth` 修复（P1 已修），说明分类模块历史 Review 有未回关的残留，但当前实现正确。
- UNKNOWN：无阻塞性 UNKNOWN。Category / Admin(RBAC) 是否纳入本次 Scope 产生独立 Design，取决于 Owner 对 Analyst 提案的决定（不阻塞 4 个核心 Artifact 推进）。

## Recommendation

RECOMMENDATION：**4 个核心 Design 由 Analyst 直接产出；Category 建独立 `category.md`；Admin(RBAC) 建独立 `rbac.md`（`iam.md` 保持不动）；事实来源优先级 + Drift 检测 + 文档结构在 Contract 中冻结后交 Cleaner 做四者一致审查。** 逐项如下：

1. **「谁写 Design」职责边界（#2，治理冲突）**：本 Backfill 的交付物本身即长期 Design 内容，无生产代码/测试可写。治理规范（`design-doc-governance`，刚 `APPROVED` 且 `CLEAN`）明确「Analyst 主责 Design、Coder 不写 Design」，并把 iam-v2「Coder 起草 Design」列为要取代的反模式。因此**推荐：Analyst 直接产出全部 Design 文件，Coder 不承担 Design 成稿职责**（本任务 Coder 无代码工作，可不出场）。
   - 关键取舍：若 Owner 坚持按 Assumption「Coder 起草」以并行分担转录工作量，则必须在 Contract 冻结一份「零设计裁量的 Design 蓝图」（每文件：章节结构 + 逐事实清单 + 事实来源映射，不留任何设计自由度），Coder 仅做机械转录，Cleaner 再校验「无蓝图外事实」。此方案能并行，但重新引入「实现者反推描述」风险，且本任务不存在代码实现，Coder 角色价值有限。**默认不采用。**
   - 结论：无论走哪条，本任务的「设计权威与最终内容正确性」归 Analyst；Coder 任何情况下不得新增事实或重新设计。

2. **Category 归属（#4）**：**推荐建立独立 `docs/design/category.md`**。依据：Category 拥有完整长期事实（树形邻接表 + `maxLevel=3`、`(parent_id,name)` 唯一、`status` 1/0 软下线、删除保护「有子分类 3003 + 有商品 3005/FK 1451」、`Exists`/`HasChildren`/`IsEnabled` 跨模块服务边界），拥有独立公开 API（`GET /categories`）与独立错误码域（3001-3005），且被 Product 强依赖（叶子/enabled 校验、删除保护），其事实当前散落于 NORMAL task.md + product Contract + 实现。合并进 `product.md` 会造成「被依赖方放在依赖方文档下」的倒置。取舍：独立 `category.md` 多一个文件，但边界清晰、与「按业务模块一文件」的组织约定一致。

3. **Admin(RBAC) 归属（#5）**：**推荐建立独立 `docs/design/rbac.md`**（命名 `rbac.md` 而非 `admin.md`，聚焦「授权」区别于 `iam.md` 的「鉴权/Session/身份域」）。依据：`admin-identity-rbac` Contract 的 RBAC 授权数据模型（`roles`/`permissions`/`admin_roles`/`role_permissions` 四表、`资源:动作` 权限 code、超级管理员保护、自操作防护、401/403 判定、删除清理事务、2003-2010 错误码）未沉淀进 `iam.md`，是实质长期事实缺口。**不采用「扩充 iam.md」**：本任务 Out of Scope 已明确「不重写 `iam.md`」，扩充与重写的边界易引发争议；独立 `rbac.md` 与「`iam.md`=鉴权、`rbac.md`=授权」的分工一致，且不触碰 `iam.md`。取舍：`iam.md` 与 `rbac.md` 会各提及 `AdminAuth`/`RequirePermission`，但一个从「鉴权与身份域」、一个从「授权与权限数据模型」角度，不构成重复（见 CROSS-MODULE 口径）。

4. **事实来源优先级与 Drift 检测（#1、#6）**：固化优先级为 `最终 APPROVED Contract > Contract Revision 的最终 Owner Decision > 最终 Implementation > Cleaner findings / core-logic / delivery > task.md`；**实现不得反向覆盖 Owner APPROVED 的 Contract/Design**。判定标准：
   - **Historical Drift（必须阻塞该 Design 或另立 CONTRACT_REVISION）**：Contract 与实现差异改变「可观察业务行为」——状态机映射、表字段/约束/FK、错误码、权限 code、并发/一致性语义、CLI 行为。不得静默选边。
   - **描述性差异（可记录后继续）**：仅措辞/顺序/内部命名等不改变可观察行为的差异。
   - 判定锚点：该差异是否改变任一模块 Contract 的 Business Invariant 或 AC 可观察结果。
   - 登记格式：Drift 表（模块 / 事实点 / Contract 说法 / 实现实际 / 判定 / 处理路径）。写进 `findings.md` 并阻塞对应 Design，不写进 Design 文件（Design 只含无争议事实）。
   - 每模块 Drift 清单见「Interfaces and Data」。

5. **Design 文档结构与粒度（#3）**：统一结构（按需保留，禁止空章节），避免机械复制 Contract 全文：
   ```text
   # <模块> 设计
   ## 职责与边界          （模块定位、Out of Scope、事实来源）
   ## 数据模型            （表/字段/约束/FK/索引、枚举映射）
   ## 状态机与生命周期     （仅含状态机的模块：product 三态、sku 二态；migration/category/inventory 无则略）
   ## 业务不变量          （INV-xxx，可判定规则，来源=模块 Contract 的 Business Invariants 提炼）
   ## 一致性模型与失败语义 （事实来源、成功/失败、并发、事务、幂等边界）
   ## 安全与权限边界      （RBAC code、前后台可见性隔离）
   ## 错误码域            （code → 语义 → HTTP，来源=模块 Contract Error Semantics）
   ## 跨模块关系          （与其他 Design 的交叉引用口径）
   ## 配置契约            （仅含配置的模块，如无则略）
   ## Deferred / 已知留白 （延后设计决策，如库存幂等延后订单、SKU 物理删除引用风险）
   ```
   Design 是「结论」（面向接手者），不做 Contract 的「过程」（问题边界/证据/推荐/验证要求/Owner Decision Record）机械复制；不得罗列代码目录、函数逐行说明、测试报告、Commit 日志。

6. **跨模块口径统一（#7）**：`product.md`/`sku.md`/`inventory.md`/`migration.md` 对同一关系必须一致：SKU `price` 为独立实际售价（非 SPU 派生）、库存绑定 `sku_id`（非 `product_id`）、`skus` 不含 `stock`、三表均经 golang-migrate 新增、`products.category_id`/`skus.product_id`/`inventories.sku_id` 均 FK `ON DELETE RESTRICT`、错误码域分段（3000/4000/5000/6000）与权限 code 前缀（`category:*`/`product:*`/`sku:*`/`inventory:*`）。交叉引用口径在 Contract 中固化（见 Interfaces and Data）。

关键取舍：**「谁写 Design」采用「Analyst 直接产出」**。这是本任务最大的治理风险点——Task 的 Assumption（Coder 起草）与治理规范（Analyst 主责）直接冲突，且治理规范刚由 Owner 冻结、明确把「Coder 起草」列为反模式。选 Analyst 直写能让 Design 严格从 APPROVED Contract 派生、避免「实现者反推描述」；代价是 Analyst 承担全部转录工作、无法用 Coder 并行分担。若 Owner 因工作量坚持并行，退路是「零裁量蓝图 + Coder 机械转录」，但需 Owner 明确，且不改变「Analyst 是内容权威」这一底线。

## Selected Design

等待 Owner 确认。

## Interfaces and Data

### 交付物与命名

- `docs/design/migration.md`、`docs/design/product.md`、`docs/design/sku.md`、`docs/design/inventory.md`（4 个核心，必建）。
- 待 Owner 决定：`docs/design/category.md`（推荐建）、`docs/design/rbac.md`（推荐建）。
- 不改：`docs/design/iam.md`、生产代码、迁移文件、`internal/codes`、`internal/boot/seed.go`、任何历史任务文件（`.agent/tasks/*`、`docs/tasks/*`）。

### 每模块 Drift 检测关键事实点（Contract ↔ 实现逐项对照）

- migration：`migrate up/force/version` CLI 与 `serve` 分离；serve 只读 readiness（dirty/未初始化/版本落后均拒绝）；baseline 7 表结构严格等价（去掉 `IF NOT EXISTS`）；14 位时间戳 version；MySQL driver 默认 `GET_LOCK`；`multiStatements=true`；`schema_migrations`/dirty；seed 顺序（readiness 通过后）；部署顺序（build → migrate up → serve）。
- product：`status` 0/1/2 ↔ `draft`/`on_shelf`/`off_shelf`；创建强制 `draft`；条件 UPDATE + `RowsAffected`；`products`/`product_images` 字段与 `category_id` FK RESTRICT；`price` 整数分上限 99,999,999；分类「存在+叶子+enabled」（4003/4004/4007）；删除保护 3005 + FK 1451→409；keyword 转义 + ESCAPE；排序白名单；图片同事务；权限 `product:create/update/on_shelf/off_shelf`。
- sku：`skus` 字段（无 `stock`）；`uk_product_name`；`product_id` FK RESTRICT；`price` 独立（非 SPU 派生）；`enabled`/`disabled` 1/0；物理删除（含 5005 兜底）；详情组合 SPU+SKU；权限 `sku:create/update/delete`。
- inventory：`inventories` 1:1 `uk_sku_id`；`inventory_logs` 字段（`before/delta/after`、`operator_admin_id` 软引用）；无记录=0；`increase` 原子 upsert；`Deduct` 条件扣减 + `RowsAffected`；库存+流水同事务；FK RESTRICT（SKU 删除 5005）；409/6001、非幂等；权限 `inventory:increase/deduct`。

### 跨模块口径（统一引用，不得冲突）

| 口径 | 统一表述 |
| --- | --- |
| SKU 价格 | `skus.price` 为独立实际售价，`products.price` 为 SPU 基础展示价，二者独立、无派生、无同步 |
| 库存绑定 | 库存绑定 `sku_id`（`inventories.sku_id`），不绑定 `product_id` |
| 库存字段 | `skus` 不含 `stock`，库存数据由 `inventories`/`inventory_logs` 承载 |
| 建表机制 | 四模块全部表经 golang-migrate 迁移新增，serve 不建表 |
| 引用完整性 | `products.category_id`→categories、`skus.product_id`→products、`inventories.sku_id`/`inventory_logs.sku_id`→skus，均 FK `ON DELETE RESTRICT` |
| 错误码域 | 通用 1000-1005 / IAM+RBAC 2001-2010 / 分类 3001-3005 / 商品 4001-4007 / SKU 5001-5005 / 库存 6001-6002 |
| 权限前缀 | `category:*`、`product:*`、`sku:*`、`inventory:*`、`admin:*`、`role:*`、`permission:*` |
| RBAC 分层 | `AdminAuth`（认证/身份域，见 `iam.md`）+ `RequirePermission(code)`（授权，见 `rbac.md`） |

### 事实来源优先级与 Drift 处理

- 优先级：最终 APPROVED Contract > Contract Revision 的最终 Owner Decision > 最终 Implementation > Cleaner findings / core-logic / delivery > task.md。
- 实现不得反向覆盖 Owner APPROVED 的 Contract/Design。
- Drift（改变可观察行为）→ 阻塞对应 Design + 登记 `findings.md`，交治理流程（另立 CONTRACT_REVISION 或 Owner 决定）；描述性差异 → 记录后继续。
- 判定锚点：差异是否改变模块 Contract 的任一 Business Invariant 或 AC 可观察结果。

## Business Invariants

- INV-001（只沉淀已批准事实）：每个新增 Design 的每一处事实均有「APPROVED Contract（含 Revision 的 Owner Decision）」或「最终实现」来源；不新增任何表/字段/状态迁移/API/错误码/权限。
- INV-002（来源优先级不逆转）：实现不得反向覆盖 Owner APPROVED 的 Contract/Design；冲突时以 APPROVED Contract 为准，不静默选边。
- INV-003（Drift 显式登记）：Contract 与实现不一致且改变可观察行为时，显式登记为 Historical Drift / Design Debt Conflict 并阻塞对应 Design，禁止通过写 Design 偷偷选择一边。
- INV-004（三者一致）：每个新增 Design 与对应模块最终 APPROVED Contract 及最终实现一致（Cleaner 四者一致的一部分）。
- INV-005（跨模块口径一致）：`product.md`/`sku.md`/`inventory.md`/`migration.md` 对同一关系描述一致（SKU price 独立、库存绑定 sku_id、skus 无 stock、三表经 migrate 新增、FK RESTRICT）。
- INV-006（可独立理解）：每个 Design 能脱离历史 `.agent/tasks/*` 独立解释模块，不含代码目录罗列、测试报告、Contract/AC/findings 全文复制。

## Failure and Consistency Semantics

本任务为文档回填任务，无运行时一致性/并发/事务语义。其「一致性」指文档四者一致（Task ↔ 最终 APPROVED Contract ↔ `docs/design/*` ↔ 最终 Implementation）：
- Design 只写无争议事实；发现 Drift 时该 Design 进入「待 Owner/治理决定」态，不产出半成品。
- 跨模块口径冲突视为一致性问题，由 Cleaner 在四者一致审查中拦截（`CHANGES_REQUIRED` 交回 Analyst），不得 `CLEAN`。

## Allowed / Forbidden Changes

允许：
- 新增 `docs/design/migration.md`、`docs/design/product.md`、`docs/design/sku.md`、`docs/design/inventory.md`。
- 若 Owner 确认，新增 `docs/design/category.md`、`docs/design/rbac.md`。
- 新增/更新 `.agent/tasks/design-debt-backfill/*`（本任务 Artifact）。

禁止：
- 修改生产代码（`.go`）、迁移文件、`internal/codes`、`internal/boot/seed.go`、业务测试。
- 修改任何历史任务文件（`.agent/tasks/*`、`docs/tasks/*`）。
- 重写/修改 `docs/design/iam.md`。
- 新增任何 Contract 未批准的设计（新表/字段/状态迁移/API/错误码/权限）；借写 Design 顺手「优化」历史架构。
- 把 `docs/design/*` 写成 Contract 的机械全文复制（含问题边界、证据、推荐、验证要求、Owner Decision Record）。

## Verification Requirements

- INV-001 → 反向审查每个 Design 的每一处事实，逐一标注来源（Contract 或实现）；确认无「凭空事实」。
- INV-002/INV-003 → 对每模块 Drift 清单逐项对比 Contract 与实现；若发现差异，判定「Drift vs 描述性」，Drift 必须登记且不写入 Design。
- INV-004 → 对每个 Design 的核心章节（数据模型/状态机/不变量/错误码/并发一致性）逐项追溯到最终 APPROVED Contract（含 Revision 的 Owner Decision）与实现。
- INV-005 → 交叉对照 4 个 Design 的跨模块口径表，确保 SKU price / 库存绑定 sku_id / skus 无 stock / FK RESTRICT 等无冲突。
- INV-006 → 通读每个 Design，确认能脱离历史任务独立解释模块，且无代码目录罗列/测试报告/全文复制。
- 负向验证（AC-013）→ 检查 Design 未写出「不存在的 `stock` 字段」「SKU price 由 SPU 派生」「`Deduct` 幂等」「serve 自动 migrate」「SKU 复用 SPU 三态」等错误。
- 通用：本任务为文档回填，不适用 `go test ./...`；采用静态验证 + 逐项追溯 + 交叉对照 + 负向验证；Cleaner 执行四者一致审查后填 `findings.md`。

## Open Risks

- 「谁写 Design」若 Owner 选「Coder 起草」退路，存在「实现者反推描述」回归风险，必须以「零裁量蓝图 + Cleaner 校验无蓝图外事实」严格兜底。
- Category 事实源自 NORMAL task.md（无 Contract），其跨模块部分（删除保护、叶子/enabled 校验）的权威来源是 `product-spu-v1` Contract；写 `category.md` 时需以该 Contract + 实现为准，不得仅依赖 `categories-v1/task.md`。
- `categories-v1/findings.md` 残留 `CHANGES_REQUIRED` 状态（P1 已在实现中修复），是历史 Review 未回关，非本次 Drift；不阻塞 Category 归属判断。
- `iam.md` 与 `rbac.md` 会在 `AdminAuth`/`RequirePermission` 上有所交叉，需靠「鉴权 vs 授权」分工 + 交叉引用口径避免重复或矛盾。

## Owner Decision Record

等待 Owner 确认。
