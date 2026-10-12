# Cleaner Findings

## Review Target

- 任务：`legacy-data-migration`
- 审查对象（复审，immutable implementation Evidence Commit C1）：`ef93e7d5cb5df3fa1a71b037e49a477076aaacef`
- 上一轮审查对象（初审 C1）：`a38c28df967aa54016fa839f46f7f7f9fa0b9840`
- 任务基线：`554cd21a0d87386f8e9348e5780b98a3330f4470`
- 当前 HEAD（C2 元数据提交）：`f5565c433c114618222aa196f2ea143b24a53cb5`（仅改 `state.yaml` 写 `review.target=ef93e7d`）
- Contract：APPROVED（`5dbad8e`），Design Impact = NEW
- 修复提交 `ef93e7d` 变更：`engine.go`（父先子后递归插入 + 环检测 + 重复跳过）、`report.go`（新增 Warnings）、`engine_test.go`（新增 4 个集成测试）
- 验证环境：本地 `docker-compose` MySQL + 自建隔离 staging 库（合成数据）；验证后已清理。原始 `shopping.sql` 仍不在本环境。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | NOT_VERIFIED | `.gitignore`/`.dockerignore`/`docs/legacy-data.md` 与 Git 未跟踪事实均已核验；但原始 SQL 不在本环境，**移动前后 SHA-256 一致无法复核**。 |
| AC-002 | PASS | staging/目标库隔离、`MultiStatements=false`、目标库无 `FOREIGN_KEY_CHECKS=0`，均核验。 |
| AC-003 | PASS | 同名不同父分类按最终 parent_id 正常迁移（已独立实测 + `TestEngineCategorySameNameDifferentParent`）；同父级重名逐条跳过并记录；软删除状态映射正确。**残留**：环状父链会双插分类，见 CLEAN-004。 |
| AC-004 | PASS | 商品映射/分类关联/金额精度/图片拆分/状态映射均实测正确。 |
| AC-005 | PASS | 规格→SKU 映射、金额转分、重复规格跳过均验证。 |
| AC-006 | PASS | 默认 SKU 有据判定（单规格用商品级、多规格缺明细跳过）验证通过。 |
| AC-007 | PASS | 库存去重（SKU 级/商品级不叠加）、`inventories`↔`skus` 一对一验证通过。 |
| AC-008 | PASS | 幂等（二次 import 零新增）+ ID 映射持久化，`TestEngineImportIdempotentAndIDMap` + 独立实测均通过。 |
| AC-009 | PASS | 不绕过唯一/FK、不覆盖存量；同父级重名逐条跳过。**残留**：环状父链产生重复分类行，见 CLEAN-004。 |
| AC-010 | PASS | Dry Run 只输出、零写入，实测通过。 |
| AC-011 | PASS | 失败原子性：分类冲突/商品跳过均无部分残留（初轮实测 + `TestEngineImportSkipsInvalidNoResidue`）。 |
| AC-012 | PASS | 父缺失降级改用 Warnings，不再重复计入跳过；报告成功+跳过=源分类数，`TestEngineCategoryReportConsistency` 通过。**残留**：环状父链场景报告与实际行数不一致（见 CLEAN-004）。 |
| AC-013 | NOT_VERIFIED | 未启动真实服务走 API；真实 `shopping.sql` 缺失，全量 API 集成验收未执行。 |
| AC-014 | PASS | 新增 4 个集成测试（同名不同父/报告一致/幂等+ID 映射/跳过无残留），覆盖 INV-003/INV-006 与 ID 映射持久化。 |
| AC-015 | PASS | `.dockerignore`/`.gitignore` 排除、无真实凭据、不连生产库。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | 无输出 |
| `gofmt -l` | PASS | 无输出 |
| `go test ./internal/logic/legacymigrate/... -count=1` | PASS | 4 个新增集成测试 + 6 组映射单测全绿 |
| `go test ./internal/migrations/...`（schema 严格等价/幂等） | PASS | legacy_id_map schema 与 migration 版本 `20261001000023` 正确 |
| 独立实测：同名不同父（原 CLEAN-001 场景） | PASS | 数码/配件(顶级)/配件(数码下) 三分类正确落库，子分类 parent_id 保留 |
| 独立实测：同父级重名 | PASS | 逐条跳过（成功 2 / 跳过 1，库内 2 行一致） |
| 独立实测：环状父链 | FAIL | A↔B 环产生 3 行（B 被插入两次：`(0,B)` 与 `(parent=A,B)`），报告「成功 2/跳过 1」与库内 3 行不一致（CLEAN-004） |
| 全量 `go test ./...` | 部分 FAIL（环境/并发） | `middleware`/`iam` 等因 migration 测试 DROP 与其它包并行导致的既有跨包隔离问题，单跑 PASS，非本次引入 |

## Findings

### CLEAN-001：分类两阶段插入在唯一约束下把「不同父级同名分类」误判为冲突（已修复）

- Severity：P1
- Status：CLOSED
- Location：`internal/logic/legacymigrate/engine.go` `migrateCategories`
- 修复验证：改为「父先子后」递归以最终 `parent_id` 单趟插入，同名不同父分类不再临时同为 `(0,name)`。独立实测（数码/配件/配件）三分类正确落库、子分类保留父级；`TestEngineCategorySameNameDifferentParent` 断言最终 DB 状态通过。

### CLEAN-002：父分类未迁移时子分类被重复计入「成功」与「跳过」（已修复）

- Severity：P2
- Status：CLOSED
- Location：`internal/logic/legacymigrate/engine.go` `migrateCategories`、`report.go`
- 修复验证：父缺失/父被跳过/环降级改为 `addWarning`（不计入跳过），报告「成功+跳过=源分类数」。`TestEngineCategoryReportConsistency` 通过（成功 2 / 跳过 1，库内 2 行，孤儿分类不在跳过明细）。

### CLEAN-003：幂等/失败原子性/ID 映射核心不变量无自动化测试（已修复）

- Severity：P2
- Status：CLOSED
- Location：`internal/logic/legacymigrate/engine_test.go`（新增）
- 修复验证：新增 4 个真实 MySQL 集成测试，覆盖同名不同父、报告一致、幂等+ID 映射持久化、无效数据跳过无残留，断言最终 DB 行数与映射行数。

### CLEAN-004：环状父链被打破时分类被重复插入，产生重复数据且报告失真

- Severity：P2
- Status：OPEN
- Location：`internal/logic/legacymigrate/engine.go` `migrateCategories` 的 `resolve` 闭包（环检测分支 + 递归返回后再次 `insertCategory`）
- AC / Invariant：AC-003（非法分类按规则处理）、AC-009（结果不违反约束/不产生重复）、AC-012（报告真实）
- Trigger：历史分类存在环状父链，如 `A.pid=B` 且 `B.pid=A`（`store_category`: `(1,2,'A')`、`(2,1,'B')`）。
- Actual：环检测分支把 B 以 `parent_id=0` 插入并写入 `oldToNew[B]`；递归返回后外层 `resolve(B)` 未复查 `oldToNew`，再次以 `parent_id=A 的新ID` 插入 B。结果目标库出现 **3 行**：`(0,B)`、`(parent=B,A)`、`(parent=A,B)`，B 被重复插入两次；报告却输出「成功 2 / 跳过 1」（且跳过理由为「与同父级分类重名」，与实际不符），与库内 3 行不一致。
- Expected：环被打破的节点只插入一次（例如环中某个节点降级为顶级后，其余节点按最终父级落库，共 2 行）；报告成功/跳过/降级与库内实际一致。
- Impact：环状父链（脏数据，Contract「Open Risks」已预见同类脏数据）会导致目标库分类重复、报告失真，违反 AC-003/AC-009/AC-012。
- Evidence：独立实测——staging 写入 `(1,2,'A')`、`(2,1,'B')` 后 `import`，目标库 `SELECT id,parent_id,name FROM categories` 返回 `(1,0,'B')`、`(2,1,'A')`、`(3,2,'B')`，报告「分类：成功 2 / 跳过 1」。
- Required Fix Boundary：环检测分支插入（或递归返回）后，外层 `resolve` 必须复查该分类是否已写入 `oldToNew`（递归期间已被插入）并复用其新 ID，避免重复插入；保证任意环状/重复分类处理后「库内实际行数 == 成功+跳过的处理数」且无重复行。不规定无关实现细节。
