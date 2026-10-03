# Cleaner Findings

## Review Target

- 任务：`design-debt-backfill`（历史 Design Debt 回填）
- 结论对应版本：HEAD commit `d9fdfb7b403ccb59d9f1b17af012eee08693a1d7`（`docs(design): 回填 6 个模块长期设计文档`）
- 任务基线 base commit：`34dae87251dbc4a6ebf9785cba4664c20d31f91c`（分支 `develop`，与 task.md Review Baseline 声明一致）
- 工作区状态：`git status` clean（全部变更已提交，无未提交修改需区分）
- 本任务相关提交链（base 之后）：
  - `276ee72` 新增 `design-debt-backfill` 任务 + 前置治理任务（`design-doc-governance`）审查记录
  - `664b4dc` 本任务 Contract 转 `APPROVED` + 治理交付验收
  - `d9fdfb7`（HEAD）产出 6 个 `docs/design/*` + 同步 `task.md`/`contract.md`
- 本任务 Review Target（`git diff 34dae87..HEAD --name-only`）：
  - 新增 Design：`docs/design/{migration,product,sku,inventory,category,rbac}.md`（6 个，全部已跟踪）
  - 本任务 Artifact：`.agent/tasks/design-debt-backfill/{task,contract,findings,core-logic,delivery}.md`
  - 未触碰：任何 `.go` 生产代码、任何 `.sql` 迁移、`docs/design/iam.md`、任何历史任务目录（`.agent/tasks/product-spu-v1`、`sku-v1`、`inventory-v1`、`db-migration`、`admin-identity-rbac`、`categories-v1`）
  - 说明：`design-doc-governance` 的 3 个文件属前置治理任务自身收尾，非本任务 Scope，不计入本任务产出
- 关键配置/迁移版本：4 个迁移 `20261001000001_baseline` / `...0002_products` / `...0003_skus` / `...0004_inventory`；错误码 1000-1005 / 2001-2010 / 3001-3005 / 4001-4007 / 5001-5005 / 6001-6002；权限 seed 25 个

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `docs/design/migration.md` 建立；golang-migrate v4、CLI 职责分离、serve 只读 readiness、baseline/force/dirty、seed 顺序、命名规范、已执行迁移不可改、`GET_LOCK`、部署顺序逐项与 `db-migration` APPROVED Contract 及 `internal/migrations/migrations.go`、`internal/cmd/cmd.go`、`internal/boot/boot.go` 一致 |
| AC-002 | PASS | `docs/design/product.md` 建立；三态状态机（0/1/2）、创建强制 draft、条件 UPDATE+`RowsAffected`、前后台可见性、分类「存在+叶子+enabled」、删除保护、价格整数分、keyword 转义、排序白名单、图片事务、RBAC、错误码逐项与 `product-spu-v1` APPROVED Contract（含 CONTRACT_REVISION）及 `internal/logic/product/product.go`、迁移 `...0002_products` 一致 |
| AC-003 | PASS | `docs/design/sku.md` 建立；SPU↔SKU 1:N、price 独立语义、二态、`(product_id,name)` 唯一、可见性、详情组合、RBAC、物理删除、库存解耦、deferred rule 逐项与 `sku-v1` APPROVED Contract 及 `internal/logic/sku/sku.go`、迁移 `...0003_skus` 一致 |
| AC-004 | PASS | `docs/design/inventory.md` 建立；1:1 `sku_id`、流水、无记录=0、`increase` 惰性 upsert、`Deduct` 条件扣减+`RowsAffected`、防负库存、同事务流水、SKU 删除 RESTRICT、409、非幂等边界逐项与 `inventory-v1` APPROVED Contract 及 `internal/logic/inventory/inventory.go`、迁移 `...0004_inventory` 一致 |
| AC-005 | PASS | 反向核对 6 个 Design 无凭空事实：数据模型/状态机/错误码/权限/并发语义均能追溯到对应 APPROVED Contract 或真实实现；未新增表/字段/状态迁移/API/错误码/权限 |
| AC-006 | PASS | 逐项对照最终 APPROVED Contract（含 product 的 CONTRACT_REVISION、db-migration 的 CONTRACT_REVISION、Owner Decision Record），无冲突、无静默选边 |
| AC-007 | PASS | 逐项对照最终实现：`status` 0/1/2 映射、SKU 表字段（`uk_product_name`、FK RESTRICT）、inventory `quantity`/`uk_sku_id`/FK、`migrate up|force|version` CLI、serve 只读 readiness、错误码、`seedPermissionList` 25 项均一致 |
| AC-008 | PASS | 跨模块口径一致：SKU `price` 独立、库存绑定 `sku_id`、`skus` 无 `stock`、三表均经 migrate 新增、FK `ON DELETE RESTRICT`、错误码域分段、权限前缀，`product/sku/inventory/migration` 四处描述无冲突 |
| AC-009 | PASS | 6 个 Design 均面向接手者、可脱离历史任务独立解释；无代码目录罗列、无函数逐行说明、无测试报告、无 Contract/AC/findings 全文复制 |
| AC-010 | PASS | `git diff 34dae87..HEAD --name-only` 无任何 `.go`/`.sql` 文件，生产代码与迁移文件未改动 |
| AC-011 | PASS | 历史任务目录（product-spu-v1/sku-v1/inventory-v1/db-migration/admin-identity-rbac/categories-v1）的 Task/Contract/findings/core-logic/delivery 均未改动 |
| AC-012 | PASS | `docs/design/iam.md` 未出现在任何 diff 中（`git ls-files` 确认仍为原内容，未重写） |
| AC-013 | PASS | 逐项负向验证通过：无「`stock` 字段」「SKU price 由 SPU 派生」「`Deduct` 幂等」「serve 自动 migrate」「SKU 复用 SPU 三态」等错误陈述；未发现需登记的 Historical Drift（详见下） |
| AC-014 | PASS | 本审查即四者一致（Task ↔ 最终 APPROVED Contract ↔ `docs/design/*` ↔ 最终 Implementation）校验，结论 CLEAN |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| 静态验证 6 个 Design 存在 | PASS | `docs/design/{migration,product,sku,inventory,category,rbac}.md` 均存在且已跟踪 |
| 错误码逐项对照 `internal/codes/codes.go` | PASS | 通用 1000-1005、IAM 2001-2010、分类 3001-3005、商品 4001-4007、SKU 5001-5005、库存 6001-6002，与各 Contract 及 Design 完全一致 |
| 权限 seed 对照 `internal/boot/seed.go` | PASS | 25 个（3 category + 4 product + 3 sku + 2 inventory + 4 admin + 5 role + 4 permission），与 `rbac.md` 清单一致 |
| 迁移机制对照 `migrations.go`/`cmd.go`/`boot.go` | PASS | `//go:embed` + iofs、`multiStatements=true`、MySQL driver 默认 `GET_LOCK`、`Status` 只读、serve 只读 readiness（dirty/未初始化/版本落后均 fail-fast）、`Force` 不执行 SQL、无 down |
| 迁移 DDL 对照 4 个 `.up.sql` | PASS | baseline 7 表去掉 `IF NOT EXISTS`；products/skus/inventories 字段、索引、唯一键、FK `ON DELETE RESTRICT` 与 Design 描述一致 |
| 状态机/并发语义对照 logic | PASS | product `transition` 条件 UPDATE+`RowsAffected`；inventory `Deduct` `WHERE quantity >= ?`+`RowsAffected`、`Increase` 原子 upsert、同事务流水；sku 物理删除 1451→5005；category 子树层级校验（`subtreeDepth`） |
| RBAC 对照 `middleware/auth.go`/`logic/admin/admin.go` | PASS | `AdminAuth`/`RequirePermission` 分层、`IsSuper` 放行、401/403 判定顺序、超级管理员保护（2005）、自操作防护（2006）、删除清理事务 |
| 负向验证（AC-013 检查点） | PASS | 6 个 Design 均正确写「无 stock」「price 独立」「Deduct 非幂等」「serve 不自动 migrate」「SKU 二态」 |
| Historical Drift 检测 | PASS（无 Drift） | 仅存在两处「描述性/演进性」差异，均非 Drift：① db-migration Contract 记 16 权限，当前 25（由 product+4/sku+3/inventory+2 合法累计演进，`migration.md` 正确引用 `rbac.md` 而非硬编码 16）；② `categories-v1/findings.md` 残留 CHANGES_REQUIRED（P1 已在实现中经 `subtreeDepth` 修复，历史 Review 未回关，Contract 已明确记录） |

## Findings

No actionable findings.

审查未发现 P0/P1/P2 缺陷。无开放 Finding。无 Historical Drift 需登记。

备注（非 Finding，供 Owner 参考）：
- 本任务为文档回填任务，主验证为「静态验证 + 逐项追溯 + 交叉对照 + 负向验证」，未运行 `go test`（Contract 明确本任务不适用 `go test` 作为主验证，且本任务不改动任何生产代码/测试）。
- 生产代码正确性由历史任务（inventory-v1/product-spu-v1 等）的交付测试承担，Cleaner 已抽样确认核心机制测试（`TestInventoryConcurrentDeduct`、`TestProductConcurrentTransition`）验证的是最终业务结果（成功次数/最终库存/最终状态），能区分正确与错误实现。
