# Cleaner Findings

## Review Target

- Task：`banner-v1`
- Design Impact：NEW（`docs/design/banner.md`）
- Base commit（任务基线）：`727cf978976d63010ed8bb67a991109e251bc57f`
- Contract target（A1，APPROVED 契约内容）：`558221a52bab4e9b27428690d44b57efe5937eec`
- **Review target（C1，Cleaner 审定的 implementation Evidence Commit）：`0c04cc0931f91b3f90857c3fcfc1a6234e0db8d9`**
- 复审时 feature HEAD（C2，Coder metadata commit，非审查对象）：`622dcf73a7f8f9833b9366159deb849d9a4885d9`
- 任务基线后新增文件与已跟踪文件：以 `git diff 727cf97..0c04cc0` 为完整变更面（含新增文件，非仅默认 `git diff`），涉及 `api/banner/v1`、`internal/controller/banner`、`internal/logic/banner`、`internal/service/banner.go`、`internal/storage`、`internal/codes/codes.go`、`internal/migrations/sql/20261001000014_banners.up.sql`、`internal/migrations/migrations_test.go`、`internal/boot/seed.go`、`internal/cmd/{cmd.go,routes_frontend.go,routes_admin.go,banner_test.go,routes_test.go}`、`internal/boot/boot_migration_test.go`、`manifest/config/config.yaml`、`docs/design/banner.md`。
- 任务前已有修改：无（基线 working tree clean）。
- 全局资源权威事实源：`origin/develop` 的 `.agent/registry/*`（develop HEAD `296af73`，banner-v1 Reservation 提交 `8753df2`）。本地 feature 分支的 `.agent/registry/*` 不含 banner 条目，属预期（RESERVED 落 develop、不进 Feature Branch）。
- 迁移版本：`20261001000014`（banners）；本分支内嵌迁移为 `00001..00012` + `00014`，`00013`（product_likes）属 product-like-v1 另一 feature，缺口为预期。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001（公开列表） | PASS | `internal/logic/banner/banner.go` `List` 仅 `Where("status",1)` 且 `Order("sort","id")`；`TestBannerPublicListEnabledAndSorted` 断言仅 3 个启用项、禁用项被过滤、sort 升序 + 同值 id 升序、字段完整，实测通过 |
| AC-002（图片可访问） | PASS | `internal/storage/storage.go` + `internal/cmd/cmd.go` `configureBannerStorage` 映射 `/storage/banners`；`TestBannerPlaceholderImagesAccessible` 断言 3 张占位图 HTTP 200 且 `Content-Type` 为 `image/*`，实测通过 |
| AC-003（后台创建与权限） | PASS | `routes_admin.go` `require("banner:create")` + `seed.go` 登记权限；`TestBannerAdminCreateAndPermission` 断言超管创建落库（默认 status=1/sort=0）、无权限 403/1003、未认证 401/1002 且均无写入，实测通过 |
| AC-004（后台更新与排序） | PASS | `Update` 仅更新提交字段 + `RowsAffected` 校验；`TestBannerAdminUpdateAndSort` 断言全量/部分更新反映新值、禁用后公开列表消失，实测通过 |
| AC-005（后台删除） | PASS | `Delete` 物理删除 + `RowsAffected` 校验；`TestBannerAdminDelete` 断言删除后公开/后台均不再出现、重复删除/详情/更新不存在统一 404/14001，实测通过 |
| AC-006（状态过滤） | PASS | 同 AC-001/AC-004 覆盖；`TestBannerPublicListEnabledAndSorted` 断言禁用项不出现在公开列表、后台可见全部状态，实测通过 |
| AC-007（数据模型与迁移） | PASS | `20261001000014_banners.up.sql` 建表，`migrations_test.go` 同步 `latestMigrationVersion=20261001000014`、`businessTables` 增 `banners`、`expectedSchema` 增 `banners` 快照；`TestUpCreatesSchemaAndIsIdempotent`/`TestSchemaStructureMatchesBaseline` 实测通过 |
| AC-008（长期设计） | PASS | `docs/design/banner.md` 存在，数据模型/Storage 边界/排序状态语义/权限边界/错误码域与 APPROVED Contract 及最终实现一致（四边核对） |

## Verification

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| 三边一致性（Registry↔Contract↔实现） | PASS | Registry（origin/develop）：`14000-14999` banner-v1 RESERVED、`20261001000014` banners RESERVED；Contract 同值；实现 `codes.go` 14001/14002、`20261001000014_banners.up.sql`、`latestMigrationVersion=20261001000014`，三者一致 |
| `scripts/check-registry.sh` | PASS | 以 origin/develop Registry 为权威运行，输出「校验通过」，14001/14002 落在 RESERVED 14000-14999 域、迁移 version 已登记 |
| `go build ./...` | PASS | 无错误 |
| `go vet ./...` | PASS | 无告警 |
| `go test -p 1 ./...` | PASS | 全包通过（含 `internal/cmd` 27.1s、`internal/migrations` 14.7s、`internal/boot` 4.8s） |
| 隔离 Mutation 验证（可逆，disposable worktree） | 记录 | 验证 `link_url` 置空字符串确将列写为 NULL（GoFrame `nil` 值语义）；验证「无变化更新返回 404」见 CLEAN-001 |

环境：Go 1.24.1 / linux/amd64；MySQL 8.0 + Redis 7 经 docker-compose 就绪（`my-shop-mysql`、`my-shop-redis` healthy）。集成测试走真实 `RegisterFrontendRoutes`/`RegisterAdminRoutes` + 真实 MySQL/Redis。

## Findings

### CLEAN-001：无变化更新误报 404（RowsAffected=0 不区分「不存在」与「值未变」）

- Severity：P3
- Status：OPEN
- Location：`internal/logic/banner/banner.go` `Update`（`Where("id",...).Data(data).Update()` 后 `RowsAffected()==0 → CodeBannerNotFound`）
- AC / Invariant：AC-004（后台更新）；Contract Failure Semantics「更新/删除 RowsAffected=0 → 14001 404」
- Trigger：管理员 PUT 提交的字段与当前记录完全一致（如重复保存相同 title）
- Actual：返回 404/14001「轮播图不存在」，但记录实际存在
- Expected：幂等更新应返回成功（或当前记录），而非「不存在」
- Impact：幂等保存被误报为不存在，前端可能误判；无数据损坏、无权限或一致性影响
- Evidence：隔离 worktree 实测 `TestCleanerVerifyNoopUpdate`：提交与当前值相同的 title → `status=404 code=14001`
- Required Fix Boundary：更新已存在记录且提交值无实际变化时应返回成功；不规定具体实现（例如以 `findOne` 存在性判断为准、或提交字段与当前值全同时直接返回当前记录）。注意：该行为与 Contract 字面语义「RowsAffected=0 → 404」一致，故仅列为 P3 供 Owner 决定是否收口，不阻塞 CLEAN。
