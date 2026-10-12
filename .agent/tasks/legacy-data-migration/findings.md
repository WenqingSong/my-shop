# Cleaner Findings

## Review Target

- 任务：`legacy-data-migration`
- 审查对象（immutable implementation Evidence Commit C1）：`a38c28df967aa54016fa839f46f7f7f9fa0b9840`
- 任务基线（Review Baseline）：`554cd21a0d87386f8e9348e5780b98a3330f4470`（分支 `feat/db-migration`）
- 当前 HEAD（C2 元数据提交）：`1c6cdefe1e3b834d965042805cf5d3fef3966d89`（仅改 `state.yaml` 写 `review.status=PENDING`、`review.target=C1`）
- Contract：APPROVED，target `5dbad8e01996be72ffeb2372efb43072ba72c594`，Design Impact = NEW
- 变更范围：`554cd21..a38c28d` 共 22 文件（`.agent/tasks/legacy-data-migration/*`、`.gitignore`、`docs/design/legacy-data-migration.md`、`docs/legacy-data.md`、`internal/boot/boot_migration_test.go`、`internal/cmd/cmd.go`、`internal/cmd/legacy_migrate.go`、`internal/logic/legacymigrate/{db,engine,mapping,report}.go`、`internal/logic/legacymigrate/mapping_test.go`、`internal/migrations/migrations_test.go`、`internal/migrations/sql/20261001000023_legacy_id_map.up.sql`、`manifest/config/config.yaml`、`scripts/import-legacy.sh`）
- 验证环境：本地 `docker-compose` MySQL（`my-shop-mysql`，schema `my_shop`，migration 已到 `20261001000023`）+ 自建隔离 staging 库 `my_shop_staging`（合成数据）；验证后已清理。
- 原始 `shopping.sql` 在本审查环境不存在（未归档到 `storage/private/legacy/`），故 SHA-256 前后一致性与真实数据导入无法在本机复核（见 AC-001/AC-013）。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | NOT_VERIFIED | `.gitignore` 规则（`/storage/private/legacy/`）生效（`git check-ignore` 命中）；`git ls-files`/`git log --all` 无 `shopping.sql` 跟踪记录；`docs/legacy-data.md` 记录来源/用途/范围/安全注意。但原始 SQL 文件在本环境不存在，**移动前后 SHA-256 一致无法复核**。 |
| AC-002 | PASS | `db.go` 源/目标分别连接 `legacy.staging.name`（默认 `my_shop_staging`）与 `database.default.name`（默认 `my_shop`），同名 fail-fast；`buildDSN` `MultiStatements=false`；目标库连接全程无 `SET FOREIGN_KEY_CHECKS=0`；`import-legacy.sh` 仅导入 staging。 |
| AC-003 | FAIL | 见 CLEAN-001：两阶段插入（parent_id 暂置 0）在 `uk(parent_id,name)` 下把「不同父级同名分类」误判冲突，导致**整个迁移失败**而非「非法分类按规则跳过并记录原因」。单名无冲突时层级映射正确（已合成验证）。 |
| AC-004 | PASS | 合成数据验证：`store_name→name`、`cate_id→category_id` 正确关联新分类、`19.90→1990`/`9.90→990`/`5.00→500` 精确转分、`slider_image` 拆分为 `product_images`、`is_del/is_show→status` 正确（软删除→off_shelf）。 |
| AC-005 | PASS | `MapSkus` 多规格每行一个 SKU、金额转分、status 映射、重复规格跳过；合成验证 2 个 SKU 正确生成。 |
| AC-006 | PASS | 单规格（无 `attr_value` 行）以商品级 stock/price 生成 `name='默认'` SKU；多规格缺明细返回 `ErrMultiSpecMissingDetail` 跳过。单元测试 + 合成验证一致。 |
| AC-007 | PASS | 合成验证：多规格用 SKU 级 stock（10/20）、单规格用商品级 stock（50），`inventories` 与 `skus` 一对一，无叠加。 |
| AC-008 | PASS | 合成验证：第二次 `import` 0 新增，商品/SKU/库存/`legacy_id_map` 行数不变（幂等）。 |
| AC-009 | PASS | 目标库连接不关闭外键/唯一键；仅增量 INSERT；合成导入无唯一/FK 冲突。但「约束冲突时优雅跳过」缺失（见 CLEAN-001，归入 AC-003）。 |
| AC-010 | PASS | 合成验证：`dry-run` 只输出将执行变更，目标库零写入。 |
| AC-011 | PASS | 合成验证：注入分类冲突失败后，目标库 0 残留（`categories`/`legacy_id_map` 均为 0），事务回滚生效。 |
| AC-012 | FAIL | 见 CLEAN-002：父分类未迁移时，子分类既计入「成功」又计入「跳过」，报告统计与库内实际行数不一致。 |
| AC-013 | NOT_VERIFIED | 未启动真实服务走 API；仅完成 DB 级合成数据验证。真实 `shopping.sql` 缺失，无法做全量 API 集成验收。 |
| AC-014 | FAIL | 见 CLEAN-003：仅有纯单元测试（`mapping_test.go`）；幂等（INV-003）、失败原子性（INV-006）、ID 映射持久化、写路径（`migrateCategories`/`writeProduct`/`loadMigratedMap`）无自动化测试或验收脚本。 |
| AC-015 | PASS | `.dockerignore` 已排除 `storage/`；`.gitignore` 排除原始 SQL 路径；代码/脚本无真实凭据；不连接生产库。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | 无输出 |
| `gofmt -l`（legacymigrate、cmd） | PASS | 无输出 |
| `go test ./internal/logic/legacymigrate/... -count=1` | PASS | 6 组映射单测全绿 |
| `go test ./internal/migrations/...`（`TestSchemaStructureMatchesBaseline`/`TestUpCreatesSchemaAndIsIdempotent`/`TestUpAppliesOnlyPendingMigration`） | PASS | legacy_id_map schema 与 `expectedSchema` 严格一致、migration 版本 `20261001000023` 正确 |
| 合成集成验证（自建 staging 库 + 目标库） | PASS（核心路径） | 分类树/商品/SKU/库存/金额/状态/幂等/原子性/DryRun 均正确（见各 AC 证据） |
| 分类同名冲突场景 | FAIL | `Error 1062 Duplicate entry '0-配件'` → 整个迁移 exit 1（CLEAN-001） |
| 父分类缺失报告场景 | FAIL | 报告「成功 2 / 跳过 2」，库内实际仅 2 行，`孤儿分类` 被重复计入（CLEAN-002） |
| `go test ./...`（全量） | 部分 FAIL（环境/并发） | `middleware`/`iam`/`flashsale` 等因「migration 测试 DROP ALL TABLES 与其它包并行」导致的表缺失/权限错误；**单独运行均 PASS**，属既有跨包并发隔离问题，非本次变更引入。需 `DATABASE_DEFAULT_PASS` 注入。 |

## Findings

### CLEAN-001：分类两阶段插入在唯一约束下把「不同父级同名分类」误判为冲突，导致整个迁移失败

- Severity：P1
- Status：OPEN
- Location：`internal/logic/legacymigrate/engine.go` `migrateCategories`（第一阶段 INSERT `parent_id=0`，第二阶段回填）
- AC / Invariant：AC-003（分类迁移、非法/异常分类按规则跳过并记录原因）；INV-004（约束不绕过）
- Trigger：staging 中存在两个「不同父级下的同名分类」，例如顶级 `配件`(pid=0) 与 `数码→配件`(pid=1)。两阶段先把所有分类以 `parent_id=0` 插入，二者临时同为 `(0,'配件')`，命中 `uk(parent_id,name)`。
- Actual：`Error 1062 (23000): Duplicate entry '0-配件' for key 'categories.uk_parent_name'`，`migrateCategories` 返回错误 → `Run` 直接返回错误 → **整个迁移退出（exit 1），分类、商品、SKU、库存全部未落库**。
- Expected：同名但在不同父级下的分类是合法数据，应各自按其最终 `parent_id` 落库；即便确有同父级重名，也应跳过该条并记录原因、继续其余迁移，而非整批回滚。
- Impact：真实历史分类（57 条）若存在跨父级同名（电商分类树常见，如「配件/其他/默认」），迁移将整体失败、无法完成核心目标；且失败无逐条原因记录，不符合「安全、可重复、可验证」。
- Evidence：合成数据（`store_category`: `(1,0,'数码')`、`(2,1,'配件')`、`(3,0,'配件')`）`import` 复现 1062 错误；`SHOW CREATE TABLE categories` 确认 `uk_parent_name(parent_id,name)` 且 `parent_id` **无自引用外键**，两阶段并非外键所迫。
- Required Fix Boundary：恢复「同名但不同父级」分类可正常迁移，且约束冲突（同父级重名等）按 AC-003 语义跳过并记录原因，不整批回滚/整程退出。实现可改为按父先子后顺序以**最终 parent_id** 单趟插入（`parent_id` 无自引用 FK，无需两阶段），或对每条冲突做逐条跳过处理；不规定无关实现细节。

### CLEAN-002：父分类未迁移时子分类被重复计入「成功」与「跳过」，报告统计失真

- Severity：P2
- Status：OPEN
- Location：`internal/logic/legacymigrate/engine.go` `migrateCategories` 第二阶段回填（`result.addSkip(entityCategory, c.ID, "父分类 ... 未迁移，parent_id 置 0")`）
- AC / Invariant：AC-012（结果统计真实，报告以真实执行结果为准）
- Trigger：某分类的父分类被跳过（如父分类名为空）。
- Actual：子分类实际**已插入**（第一阶段计入 `CategoriesMigrated++`），第二阶段又因父缺失被 `addSkip` 计入 `CategoriesSkipped++` 并写入「跳过明细」。报告「成功 2 / 跳过 2」，但库内实际只有 2 行（该子分类被同时算作成功和跳过），总数与真实行数不符。
- Expected：「父缺失、parent_id 置 0」是迁移完成的一种降级结果，应作为警告/降级说明，不应计入「跳过」；「成功 + 跳过」总数应等于源分类处理数，且逐条记录与库内实际一致。
- Impact：AC-012 要求的真实统计不成立，影响 Owner 通过报告判断迁移质量。
- Evidence：合成数据（`store_category`: `(99,0,'')`、`(100,99,'孤儿分类')`、`(1,0,'正常')`）`import` 输出「分类：成功 2 / 跳过 2」，`SELECT` 目标库 `categories` 仅 2 行（`正常`、`孤儿分类`）。
- Required Fix Boundary：报告统计与库内实际一致；「父缺失置 0」不再被计入「跳过」或被单独标识，不得把同一分类同时计入成功与跳过。

### CLEAN-003：幂等/失败原子性/ID 映射等核心不变量无自动化测试或验收脚本保护

- Severity：P2
- Status：OPEN
- Location：`internal/logic/legacymigrate/`（仅 `mapping_test.go` 有纯函数单测；`engine.go`/`db.go` 写路径零测试）；无 `*_integration_test.go` 或迁移验收脚本
- AC / Invariant：AC-014（可重复执行的迁移集成测试或验收脚本）；INV-003（映射幂等）、INV-006（失败原子）
- Trigger：对 `entityKey`/`loadMigratedMap`/`migrateCategories`/`writeProduct`/`migrateProducts` 的幂等判断、事务边界、约束冲突处理、旧 ID→新 ID 映射持久化做错误修改（如移除映射写入、去掉事务、放宽状态判定）。
- Actual：现有单测只覆盖纯映射函数（金额/状态/图片拆分/MapSkus）；「重复执行不重复插入」「失败整体回滚无残留」「ID 映射持久化」等关键不变量没有任何自动化测试或可重复验收脚本，写路径逻辑被改坏时测试仍会通过。
- Expected：至少有一个可重复执行的集成测试或验收脚本，覆盖：重复 `import` 幂等（行数不变）、注入失败后无部分残留、`legacy_id_map` 与业务记录同事务写入。
- Impact：核心不变量失去回归保护，未来修改可能静默破坏幂等/一致性；AC-014 明确要求的集成验证缺失。
- Evidence：`search` 确认 `internal/logic/legacymigrate/` 仅有 `mapping_test.go`；仓库无迁移验收脚本（`scripts/import-legacy.sh` 仅导入 staging，不验证迁移结果）。Cleaner 已用合成数据手工验证幂等与原子性（见 Verification），但无长期自动化保护。
- Required Fix Boundary：补充可重复执行的集成测试或验收脚本，至少保护 INV-003（幂等）与 INV-006（失败原子）以及 ID 映射持久化；不要求为纯样板代码补测。
