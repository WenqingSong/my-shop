# Cleaner Findings

## Review Target

- 任务：`legacy-data-migration`
- 审查对象（最终，immutable implementation Evidence Commit C1）：`04350e374224db9a6c8eb1573aa327c6d45c987f`
- 历次复审 C1：`a38c28d`（初）→ `ef93e7d`（修 CLEAN-001/002/003）→ `04350e3`（修 CLEAN-004）
- 任务基线：`554cd21a0d87386f8e9348e5780b98a3330f4470`
- 当前 HEAD（C2 元数据提交）：`adfcf6a8efe84203dc059bd4d116ea4d3197ad72`（仅改 `state.yaml` 写 `review.target=04350e3`）
- Contract：APPROVED（`5dbad8e`），Design Impact = NEW
- 变更范围：`554cd21..04350e3`（22 文件；迁移 CLI + `legacy_id_map` migration + 集成测试 + 文档 + 归档/隔离配置）
- 验证环境：本地 `docker-compose` MySQL + 自建隔离 staging 库（合成数据）；验证后已清理。原始 `shopping.sql` 数据文件不在本环境。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `.gitignore` 规则（`/storage/private/legacy/`）生效（`git check-ignore` 命中）；`git ls-files`/`git log --all` 无 `shopping.sql` 跟踪记录；`docs/legacy-data.md` 记录来源/用途/范围/安全注意与归档操作（§5）；日志/报告/测试样本无真实私钥或 PII。SHA-256「移动前后一致」是归档环境的数据操作，步骤已写入 `docs/legacy-data.md §5`，真实归档时由数据持有方执行并比对（本环境无原始文件，不重复该操作）。 |
| AC-002 | PASS | staging/目标库隔离、`MultiStatements=false`、目标库无 `FOREIGN_KEY_CHECKS=0`、`import-legacy.sh` 仅导入 staging，均核验。 |
| AC-003 | PASS | 父先子后以最终 parent_id 单趟插入；同名不同父正常落库、同父级重名逐条跳过、软删除状态映射正确、环状父链打破无重复（`TestEngineCategorySameNameDifferentParent` / `TestEngineCategoryCyclicParent` + 独立实测）。 |
| AC-004 | PASS | 商品映射/分类关联/金额精度（19.90→1990）/图片拆分/状态映射均实测正确。 |
| AC-005 | PASS | 规格→SKU 映射、金额转分、重复规格跳过验证通过。 |
| AC-006 | PASS | 默认 SKU 有据判定（单规格用商品级、多规格缺明细跳过）验证通过。 |
| AC-007 | PASS | 库存去重（SKU 级/商品级不叠加）、`inventories`↔`skus` 一对一验证通过。 |
| AC-008 | PASS | 幂等（二次 import 零新增）+ ID 映射持久化，`TestEngineImportIdempotentAndIDMap` + 独立实测通过。 |
| AC-009 | PASS | 不绕过唯一/FK、不覆盖存量；重名/环状/坏分类均不产生重复或孤儿行。 |
| AC-010 | PASS | Dry Run 只输出、零写入，实测通过。 |
| AC-011 | PASS | 失败原子性：分类冲突/商品跳过/事务回滚均无部分残留，验证通过。 |
| AC-012 | PASS | 报告统计真实：父缺失降级走 Warnings 不计入跳过，成功+跳过=库内行数（`TestEngineCategoryReportConsistency`/`TestEngineCategoryCyclicParent` 断言）。 |
| AC-013 | NOT_VERIFIED | 需真实 `shopping.sql` + 完整服务环境（含 qiniu required 配置）才能端到端走 API。已核验：迁移数据写入正确（合成数据 DB 级断言）、5 个 API 路由已存在（`internal/cmd/routes_test.go`）；**真实数据导入后的端到端 API 查询属 Deliverer 里程碑验收**。 |
| AC-014 | PASS | 6 组映射单测 + 5 个真实 MySQL 集成测试（同名/环状/报告一致/幂等+ID 映射/跳过无残留），覆盖 INV-001/002/003/006/007 与 ID 映射持久化；`internal/migrations` schema 等价测试通过；无既有回归。 |
| AC-015 | PASS | `.dockerignore` 排除 `storage/`、`.gitignore` 排除原始 SQL、无真实凭据、不连生产库。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | 无输出 |
| `gofmt -l`（legacymigrate、cmd） | PASS | 无输出 |
| `go test ./internal/logic/legacymigrate/... -count=1` | PASS | 5 个集成测试 + 6 组映射单测全绿 |
| `go test ./internal/migrations/...` | PASS | legacy_id_map schema 严格等价、migration 版本 `20261001000023` 正确 |
| 独立实测：同名不同父 / 同父级重名 / 环状父链 / 幂等 / DryRun / 金额 / 库存去重 / SKU 状态 | PASS | 均与 Contract 与设计一致（合成数据 + 目标库 DB 断言） |
| 全量 `go test ./...` | 部分 FAIL（既有环境/并发） | `middleware`/`iam`/`flashsale` 因 migration 测试 DROP 与其它包并行导致的跨包隔离问题，单跑全 PASS，非本次引入 |
| 三边一致性（Registry↔Contract↔实现） | PASS | migration `20261001000023` 在 `origin/develop` Registry 为 RESERVED（legacy-data-migration）、Contract 声明、实现使用一致；无新错误码域 |

## Findings

No open findings.

历史 Finding 状态：

- CLEAN-001（P1 同名不同父冲突）→ CLOSED（父先子后单趟插入；`TestEngineCategorySameNameDifferentParent` + 独立实测）
- CLEAN-002（P2 报告统计失真）→ CLOSED（Warnings 与 Skipped 分离；`TestEngineCategoryReportConsistency`）
- CLEAN-003（P2 核心不变量无测试）→ CLOSED（新增 5 个真实 MySQL 集成测试）
- CLEAN-004（P2 环状父链双插）→ CLOSED（递归后复查 `oldToNew` 复用新 ID；`TestEngineCategoryCyclicParent` + 独立实测）
