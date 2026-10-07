# Cleaner Findings

## Review Target

- 任务基线（Base）：`d8438b4938a9b6b2e2f82fb87d9c6c9a60b6662b`（分支 `feat/recommendation-slot`，任务开始时 working tree clean、无既有未提交修改）。
- Implementation Evidence Commit（C1，被审查对象）：`69842cbf485bacbd0defb0d7febe49a17bcf9fc2`（`feat(recommendation): 推荐位 V1 核心闭环`）。
- 当前工作区 HEAD：`7499849359b3cb720d15fa2cf733fb25c62c88db`（C2，仅改 `state.yaml` 写 `review.status=PENDING`/`review.target=C1`，属 Coder metadata commit，不参与审查）。
- Review Target 判定方式：审查 `d8438b4..69842cb` 的完整变更（含新增文件）；C1 之后到 HEAD 之间只有 C2 这一条 metadata commit，无任何生产代码/测试变化。
- 全局资源：错误码域 `15000-15999`（域序 15）、migration `20261001000016_recommend.up.sql`。共享 develop 上的 Reservation 位于 `27843b4`（`chore(registry): reserve 15000-15999 + 20261001000016 for recommendation-v1`），本 feature 分支尚未合并该 commit，故本地 `.agent/registry/*` 不含推荐位条目（`scripts/check-registry.sh` 读本地旧 Registry 产生的 `FAIL` 属预期假阳性，三边一致性以 `origin/develop` Registry 为准）。
- 已批准 Contract：`contract.status=APPROVED`，`contract.target=0f54b9306a0d9bba6900177495fb87453b90f1af`。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | 迁移 `20261001000016_recommend.up.sql` 建 `recommend_positions`/`recommend_items` 两表，含 `uk_position_product(position_id,product_id)` 唯一约束、`idx_position_sort(position_id,sort)` 查询索引、`idx_status`、FK `ON DELETE CASCADE`；`migrations_test.go` 已同步 `latestMigrationVersion=20261001000016`、`businessTables` 含两表、`expectedSchema` 精确快照两表。`TestUpCreatesSchemaAndIsIdempotent`（再次 Up 幂等）与 `TestSchemaStructureMatchesBaseline`（结构严格等价）实测通过。 |
| AC-002 | PASS | `Create/AdminList/AdminDetail/Update/Delete` 已实现；`routes_admin.go` 挂 `AdminAuth` + `require("recommend:create/update/delete")`。`TestRecommendationAdminCRUD` 覆盖创建/列表/详情/更新/删除、重复 code→409/15002、不存在→404/15001，实测通过。 |
| AC-003 | PASS | `AddItem/RemoveItem/UpdateSort` 已实现并挂 `require("recommend:item")`。`TestRecommendationItemManagement` 覆盖添加/移除/调整排序且后续后台与前台查询反映新值，实测通过。 |
| AC-004 | PASS | `recommend_items` 唯一约束 `uk_position_product` 兜底，逻辑层 `isDuplicateKeyError`（1062→15005）。`TestRecommendationDuplicateRejected`（重复添加→409/15005 且关系仅 1 条）+ `TestSchemaStructureMatchesBaseline`（唯一索引存在）实测通过。并发重复由 DB 唯一约束在 DB 层兜底（与顺序重复同一机制），无需额外 goroutine 断言。 |
| AC-005 | PASS | `AddItem` 仅校验商品存在（复用 `service.Product().Exists`→4001），不限制 on_shelf；`TestRecommendationItemManagement` 断言不存在商品→404/4001、draft/off_shelf 可加入但前台不展示，实测通过。 |
| AC-006 | PASS | 前台 `GET /recommendations/:code` 公开无 token，JOIN `products` 过滤 `status=on_shelf`、`ORDER BY sort,id`。`TestRecommendationFrontendOnlyOnShelfAndSorted` 断言仅含 on_shelf 商品、`sort,id` 稳定排序、禁用位/不存在位返回空，实测通过。 |
| AC-007 | PASS | 商品下架不物理删除 `recommend_items` 关系。`TestRecommendationItemManagement` 断言 p1 下架后关系保留（后台仍见 2 条）、前台不再返回，实测通过。 |
| AC-008 | PASS | `seed.go` 登记 `recommend:create/update/delete/item` 四码；`RequirePermission` fail-closed（`IsSuper` 放行、无权限 403、未认证 401）。`TestRecommendationPermissionIsolation` 断言权限 seed、无权限 403/1003、未认证 401/1002 且 `recommend_positions`/`recommend_items` 零多余写入，实测通过。 |
| AC-009 | PASS | `docs/design/recommendation.md` 与 APPROVED Contract、最终实现一致：数据模型（两表/唯一约束/索引/状态映射）、商品可售性过滤规则、6 条业务不变量、失败语义、权限边界、错误码域 15000-15999 与 5 个 code、路由契约逐一核对无漂移。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| 三边一致性（Registry↔Contract↔实现） | PASS | `origin/develop` Registry：`15000-15999 → recommendation-v1 RESERVED`、`20261001000016 → recommend RESERVED`；Contract 声明一致；实现 `codes.go` 用 15001-15005（域内）、迁移文件 `20261001000016_recommend.up.sql`。三者无漂移。本地 `scripts/check-registry.sh` 读 stale 本地 Registry 报假阳性（见 Review Target 说明）。 |
| go build | PASS | `go build ./...` exit 0。 |
| gofmt | PASS | 变更文件 `gofmt -l` 无输出。 |
| go vet | PASS | `go vet ./...` exit 0。 |
| 推荐位集成测试 | PASS | `go test ./internal/cmd -run TestRecommendation -count=1` 全部 PASS（7 个测试，真实路由 + MySQL/Redis）。 |
| 迁移测试 | PASS | `go test ./internal/migrations -run 'TestUpCreatesSchemaAndIsIdempotent|TestSchemaStructureMatchesBaseline|TestUpAppliesOnlyPendingMigration|TestConcurrentUp' -count=1` 全部 PASS。 |
| 全量测试 | PASS | `go test -p 1 ./...` 全部 ok（含 `internal/cmd` 44s、`internal/migrations` 21s）。 |

## Findings

No actionable findings.（无 P0/P1/P2。）

P3（低风险，供 Owner 决定，不阻塞 CLEAN）：

### CLEAN-001：UpdateSort 允许部分商品列表，未列出的商品保留旧 sort，可能与新值交错

- Severity：P3
- Status：OPEN
- Location：`internal/logic/recommendation/recommendation.go` `UpdateSort`（L319-382）
- AC / Invariant：AC-003（调整排序）
- Trigger：调用 `PUT /admin/recommend-positions/:id/items/sort` 时只提交部分 `product_ids`（子集），而非全部已加入商品。
- Actual：仅校验「列表非空、无重复、均为已加入该推荐位的商品」，不要求列表覆盖全部关系；未列出的商品保留原 `sort` 值，可能与列表内新赋的 `sort=0..n-1` 相等，导致顺序依赖 `id` 兜底、语义不够直观。
- Expected：行为本身无数据破坏（`sort` 无唯一约束、同值按 `id` 兜底，稳定且正确）；仅存在「部分重排」语义的模糊性。
- Impact：仅影响运营对「部分重排」的预期；正常用法（提交全量有序列表）无影响。
- Evidence：`TestRecommendationItemManagement` 覆盖全量重排 `[p3,p1,p2]` 且通过；无部分列表测试，也无明确契约禁止。
- Required Fix Boundary：若 Owner 认为「排序接口必须提交全部关系」，需在 Contract 明确「列表必须覆盖全部已加入商品」并加校验（当前实现不禁止部分列表，属可接受实现差异，不必修复）。由 Owner 决定是否处理。
