# Cleaner Findings

## Review Target

- 任务基线（Base）：`d8438b4938a9b6b2e2f82fb87d9c6c9a60b6662b`（分支 `feat/recommendation-slot`）。
- Implementation Evidence Commit（C1，本次被审查对象）：`777212983e7ce43974cba9b0b65ada9d2dc88cc1`（`feat(recommendation): UpdateSort 全量重排校验（新增 15006 CodeRecommendItemSortMismatch）`）。
- 当前工作区 HEAD：`7c463e423f993cf6d683cffafb3f62058f64f4bb`（C2，仅改 `state.yaml` 写 `review.status=PENDING`/`review.target=C1`，属 Coder metadata commit，不参与审查）。
- 复审性质：CLEAN-001（P3）触发 Owner 决策 → Contract Revision。上一轮 `CLEAN` 对应旧 C1=`69842cb`，因 Contract 修订 + 实现变化已客观失效；本次为新 Review Target。
- 变更链路（相对上一轮 CLEAN 之后）：`a54fbe1`/`6710f07`（contract.target 更新）→ `70672be`（Contract 精确化：15006 仅指缺漏）→ `0e67233`（设计文档同步）→ `7772129`（C1 实现）→ `7c463e4`（C2 metadata）。
- 已批准 Contract：`contract.status=APPROVED`，`contract.target=70672bee1558d5b2ae6b53dcab930a955270cd5a`（修订版，含 INV-007 全量重排与错误码 15006）。
- 全局资源：错误码域 `15000-15999`（新增 15006 属域内编号，无需新 Registry 预留）、migration `20261001000016_recommend.up.sql`（未变）。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | 迁移与 `migrations_test.go` 本轮无变化，上一轮已核实（`latestMigrationVersion=20261001000016`、两表唯一约束/索引、`expectedSchema` 快照）。`TestUpCreatesSchemaAndIsIdempotent`/`TestSchemaStructureMatchesBaseline` 仍通过。 |
| AC-002 | PASS | 推荐位 CRUD 本轮无变化，`TestRecommendationAdminCRUD` 仍通过。 |
| AC-003 | PASS | 调整排序精确化为「全量重排」：`UpdateSort` 新增缺漏校验（缺漏→15006、多余→15004、重复/空→15003，完整→按序写入）。`TestRecommendationItemManagement`（全量重排成功路径）与新增 `TestRecommendationUpdateSortIncomplete`（缺漏/多余/完整）实测通过。 |
| AC-004 | PASS | 重复添加唯一约束本轮无变化，`TestRecommendationDuplicateRejected` 仍通过。 |
| AC-005 | PASS | 商品存在性校验本轮无变化，`TestRecommendationItemManagement` 仍通过。 |
| AC-006 | PASS | 前台可见性/排序本轮无变化，`TestRecommendationFrontendOnlyOnShelfAndSorted` 仍通过。 |
| AC-007 | PASS | 下架语义本轮无变化，`TestRecommendationItemManagement` 仍通过。 |
| AC-008 | PASS | 权限隔离本轮无变化，`TestRecommendationPermissionIsolation` 仍通过。 |
| AC-009 | PASS | `docs/design/recommendation.md` 已随 Contract 修订同步（§2.4 排序语义、§4 INV-007、§5 失败语义、§7 错误码表 15006），与 APPROVED Contract 及最终实现一致。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| 三边一致性（Registry↔Contract↔实现） | PASS | 错误码域 15000-15999 不变；新增 15006 在域内，Contract 与实现均声明/使用 15006，无新 Registry 预留需求；migration 未变。三者无漂移。 |
| Contract 已批准 | PASS | `state.contract.status=APPROVED`，`target=70672be`（15006 仅指缺漏的精确化版本）。 |
| 设计文档同步 | PASS | `0e67233` 已同步 `docs/design/recommendation.md` 的 INV-007、失败语义、错误码表，与修订后 Contract 一致。 |
| 回归测试有效性 | PASS | 新增 `TestRecommendationUpdateSortIncomplete`：缺漏 `[p1]`→409/15006、多余 `[p1,999999]`→404/15004、完整 `[p2,p1]`→200。若缺漏校验被删除，缺漏用例会返回 200 而非 409/15006，测试必然失败，能识别错误实现。 |
| go build | PASS | `go build ./...` exit 0。 |
| gofmt | PASS | 变更文件 `gofmt -l` 无输出。 |
| go vet | PASS | `go vet ./...` exit 0。 |
| 推荐位集成测试 | PASS | `go test ./internal/cmd -run TestRecommendation -count=1` 全部 PASS（8 个测试，含新增回归测试）。 |
| 全量测试 | PASS | `go test -p 1 ./...` 全部 ok，无回归。 |

## Findings

CLEAN-001 已关闭（CLOSED）：Owner 决定「排序必须全量」→ Contract 修订（INV-007，缺漏→15006、多余→15004、重复/空→15003）→ 设计文档同步 → 实现与回归测试均已落实并经独立验证通过。

### CLEAN-001：UpdateSort 允许部分商品列表，未列出的商品保留旧 sort

- Severity：P3
- Status：CLOSED
- Location：`internal/logic/recommendation/recommendation.go` `UpdateSort`
- AC / Invariant：AC-003 / INV-007（全量重排覆盖）
- 复审结论：修复正确。`UpdateSort` 现校验顺序为 15001（不存在）→ 15003（空/重复/非法）→ 15004（含未加入商品）→ 15006（缺漏已加入商品），校验全部通过后才在事务内按提交顺序全量写入 `sort`。校验先于事务，缺漏/多余/重复均不产生任何 `sort` 写入。
- 回归证据：`TestRecommendationUpdateSortIncomplete` 覆盖缺漏/多余/完整三态，实测通过；`TestRecommendationItemManagement`（全量重排成功路径）无回归。

No actionable findings.（无开放 P0/P1/P2/P3。）
