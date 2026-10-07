# Cleaner Findings

## Review Target

- Task 基线（Base）：`91a67d5b89abc5d250a49a2074881b9ba3f621e5`（`feature/cms`，task.md 声明的 Review Baseline；本地 registry 文件停留在此基线的陈旧态）。
- Implementation Evidence Commit C1（本次审查对象）：`f4411fcadc3d400a0613689277843210164f9673`。
- 当前 HEAD：`397f2aabed63d8eb1823ae4fe6d5ab306dca32bd`（C2，Coder 的 metadata commit，仅改 `state.yaml` 写入 `review.status=PENDING` / `review.target=C1`）。Cleaner 审 C1，不审 C2、不审 working tree。
- C1 的父提交 `61bbec6` 仅含 `.agent/tasks/article-cms-v1/` 任务脚手架文件（无生产代码），任务前无其他已有修改，基线可可靠区分。
- 相关变更文件（C1，11 个）：`api/article/v1/article.go`、`internal/service/article.go`、`internal/controller/article/article.go`、`internal/logic/article/article.go`、`internal/logic/logic.go`、`internal/cmd/routes_frontend.go`、`internal/codes/codes.go`、`internal/migrations/sql/20261001000017_articles.up.sql`、`internal/migrations/migrations_test.go`、`internal/boot/boot_migration_test.go`、`internal/cmd/article_test.go`。未触碰 `product_likes`/`favorites`/`review`/`product` 等既有模块。
- 关键资源：错误码域 `16000-16999`（域序 16，article-cms-v1）；migration `20261001000017_articles.up.sql`（articles + article_likes + article_favorites 三表）。
- Registry 权威态：`origin/develop` 已含 `16000-16999 | article-cms-v1 | RESERVED` 与 `20261001000017 | articles | article-cms-v1 | RESERVED`（本地 feature 分支 registry 文件为基线的陈旧态，非漂移）。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 发布文章 | PASS | `TestArticleCreateDetailOwnership`：`author_id==Principal.UserID`，伪造 `author_id`/`user_id` 被忽略，落库正确 |
| AC-002 修改文章 | PASS | `TestArticleUpdateOwnership`：作者修改成功；非作者 404/16001 且内容不变 |
| AC-003 删除文章 | PASS | `TestArticleDeleteOwnershipAndCleanup`：作者删除成功且关联清理；非作者 404/16001 且无删除 |
| AC-004 公开列表 | PASS | `TestArticlePublicList`：无需 token、按 `id DESC`、分页正确 |
| AC-005 公开详情 | PASS | `TestArticleCreateDetailOwnership`：详情内容/作者用户名正确；不存在 404/16001 |
| AC-006 我的文章 | PASS | `TestArticleMyListIsolation`：仅返回本人文章，不泄露他人 |
| AC-007 点赞 | PASS | `TestArticleLikeIdempotentAndCount` + `TestArticleLikeConcurrentDuplicateSingleRow`（`-race`，8 并发仅落一条）；公开计数正确 |
| AC-008 收藏 | PASS | `TestArticleFavoriteIdempotentCheckList`：收藏/取消/是否已收藏/我的收藏列表正确 |
| AC-009 详情点赞/收藏状态 | PASS | 独立鉴权接口 `GET /articles/:id/like/check`、`/favorite/check` 实现并通过测试，详情保持公开无 token（Contract D2） |
| AC-010 关联清理 | PASS | `TestArticleDeleteOwnershipAndCleanup`：删除后 `article_likes`/`article_favorites` 0 行、公开点赞数回落 0 |
| AC-011 鉴权边界 | PASS | `TestArticleRequiresAuth`：全部写/状态接口无 token 401/1002 且无写入；公开读 200；全量 `go test -p 1 ./...` 通过（商品点赞/收藏无回归） |
| AC-012 数据模型与迁移 | PASS | `migrations_test.go` 同步 `latestMigrationVersion=20261001000017`、三表入 `businessTables`/`expectedSchema`；`TestSchemaStructureMatchesBaseline`、`TestUpAppliesOnlyPendingMigration`（探针 18）、幂等/增量测试全通过 |
| AC-013 长期设计 | PASS | `docs/design/article.md` 与 APPROVED Contract、最终实现一致（四者一致） |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `gofmt -l`（变更文件） | PASS | 无输出 |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | 无输出 |
| `go test -p 1 ./...` | PASS | 全部包 `ok`，无回归 |
| `go test -race ./internal/cmd -run TestArticle` | PASS | 11 个文章测试全部 PASS（含并发） |
| `go test ./internal/migrations`（结构等价/增量/幂等/dirty） | PASS | 全 PASS |
| 三边一致性 Registry↔Contract↔实现 | PASS | `origin/develop` 已 RESERVED `16000-16999`/`20261001000017`；Contract 与实现（16001/16002、17.up.sql）一致 |
| Mutation（作者越权） | PASS | 移除 `Update` 的 `Where("author_id", userID)` 后 `TestArticleUpdateOwnership` 失败（`intruder update: status=200 code=0 want 404/16001`），隔离 worktree 执行、已清理 |

## Findings

### CLEAN-001：migrations_test.go 表数量注释过时

- Severity：P3
- Status：OPEN
- Location：`internal/migrations/migrations_test.go:19`（注释写「23 张业务表」，实际 27）、`:145`（「21 张业务表」）、`:365`（`expectedSchema` 说明未提及 articles）
- AC / Invariant：无（不影响行为）
- Trigger：阅读注释
- Actual：表数量注释与 `businessTables`/`expectedSchema` 实际条目数不一致
- Expected：注释与实际表数一致
- Impact：仅误导维护者，不影响测试断言或运行
- Evidence：`businessTables` 含 27 项，注释写 23/21
- Required Fix Boundary：同步注释中的表数（可由 Owner 决定是否处理，不阻塞 CLEAN）
