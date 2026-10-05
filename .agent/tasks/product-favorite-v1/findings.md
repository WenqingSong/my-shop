# Cleaner Findings

## Review Target

- 任务基线（base commit）：`fc585493bb60b43701815e45ab8fa4f15c2dc96c`（`feature/product-favorite`），任务开始时 working tree 干净、无已有修改。
- 当前 Commit（HEAD）：`9b1a50f66361aea1ba9e79b21deb7405bccf7187`（`docs(favorite): 新增商品收藏 v1 任务契约与设计文档`，含 Registry RESERVED、task/contract/design 工件）。
- 工作区状态：HEAD 之上存在**未提交**的实现变更（5 个已跟踪文件修改 + 6 个新增未跟踪文件），全部属于本任务收藏实现，可明确区分于基线。
- 已跟踪修改：`internal/boot/boot_migration_test.go`、`internal/cmd/routes_frontend.go`、`internal/codes/codes.go`、`internal/logic/logic.go`、`internal/migrations/migrations_test.go`。
- 新增文件：`api/favorite/v1/favorite.go`、`internal/controller/favorite/favorite.go`、`internal/logic/favorite/favorite.go`、`internal/service/favorite.go`、`internal/migrations/sql/20261001000010_favorites.up.sql`、`internal/cmd/favorite_test.go`。
- 关键资源：错误码域 `11000-11999`（域序 11，RESERVED）、migration `20261001000010`（favorites，RESERVED）。

> 说明：Coder 尚未 commit 实现（`state.yaml.phase` 仍为 `APPROVED`）。Cleaner 对「HEAD `9b1a50f` + 上述未提交工作区变更」这一对象给出 CLEAN，`target_base` 记录为 HEAD commit；Owner 决定 Commit 前，本 CLEAN 与该未提交变更的对应关系以本清单为准。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（添加收藏·登录） | PASS | `TestFavoriteAddAndList`：登录用户收藏 on_shelf 商品，落库且 `user_id` 归属本人、列表可见（真实 MySQL/Redis）。 |
| AC-002（重复收藏幂等+并发） | PASS | `TestFavoriteDuplicateIdempotent`（顺序重复仅 1 条）+ `TestFavoriteConcurrentDuplicateSingleRow`（8 并发 + `-race`，均幂等成功且仅 1 条）。 |
| AC-003（取消收藏） | PASS | `TestFavoriteRemoveAndCheck`：取消后 check=false、库中删除；取消未收藏商品幂等成功。 |
| AC-004（收藏列表分页） | PASS | `TestFavoriteAddAndList`：`total`/`items`/分页字段正确，含实时联查商品信息；`created_at` 字段由 `favoriteRow` 扫描填充。 |
| AC-005（是否已收藏） | PASS | `TestFavoriteRemoveAndCheck`：收藏后 check=true、取消后 check=false。 |
| AC-006（用户隔离） | PASS | `TestFavoriteUserIsolation`：B 列表为空、check=false、取消 A 的收藏幂等成功但不删除 A 记录（count=1、归属仍为 A）。 |
| AC-007（商品校验） | PASS | `TestFavoriteProductValidation`：不存在 404/4001、off_shelf/draft 409/11001、product_id≤0 400/1001，均无写入。 |
| AC-008（必须登录） | PASS | `TestFavoriteRequiresAuth`：四接口无 token 与非法 token 均 401/1002，无数据返回。 |
| AC-009（数据模型与迁移） | PASS | `TestUpCreatesSchemaAndIsIdempotent` + `TestSchemaStructureMatchesBaseline`（含 favorites 结构、`uk_user_product`）+ 迁移幂等，`latestMigrationVersion=20261001000010`。 |
| AC-010（长期设计） | PASS | `docs/design/favorite.md` 与 APPROVED Contract 及最终实现一致（见四者一致性核对）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 无错误。 |
| `go vet ./...` | PASS | 无告警。 |
| `gofmt -l`（涉及文件） | PASS | 无输出，格式合规。 |
| `bash scripts/check-registry.sh` | PASS | 未发现 Reservation 重复或 Registry↔实现漂移；`11001` 落在 RESERVED 域 `11000-11999`。 |
| `go test -p 1 ./internal/cmd/ -run TestFavorite` | PASS | 8 个收藏测试全 PASS。 |
| `go test -p 1 -race ./internal/cmd/ -run TestFavoriteConcurrentDuplicateSingleRow` | PASS | 无 data race，并发幂等 + 单行成立。 |
| `go test -p 1 ./internal/migrations/ ./internal/boot/` | PASS | 全部 PASS（含 `TestSchemaStructureMatchesBaseline`、`TestUpAppliesOnlyPendingMigration` 探针迁移 `...00011`）。 |
| `go test -p 1 ./...` | PASS* | 除 `internal/controller/health` 外全部 PASS；health 因 `:8000` 被运行中的 `my-shop-deliver` 进程占用而 bind 失败，与本次任务无关（收藏未触碰 health 控制器或 8000 端口）。 |

## 三边 / 四者一致性

- Registry ↔ Contract ↔ 实现：错误码域 `11000-11999`（Registry RESERVED = Contract RESERVED = `codes.go` 的 `11001`）；migration `20261001000010`（Registry RESERVED = Contract RESERVED = `20261001000010_favorites.up.sql` + `latestMigrationVersion` + `businessTables`）。三边一致，无漂移。
- Design Impact = NEW：Task 声明 `docs/design/favorite.md` → Contract APPROVED → Design Artifact 已存在 → 最终实现四者一致（数据模型、`uk_user_product`、幂等语义、on_shelf 校验、软引用、`GET /favorites/check`、错误码 11001 均逐项吻合）。

## Findings

无 P0/P1/P2 阻塞项。

P3 观察（非阻塞，交 Owner 决定）：

- `internal/migrations/migrations_test.go` 两处注释陈旧：`TestUpCreatesSchemaAndIsIdempotent` 注释「16 张业务表」（实际 19 张）；`expectedSchema` 注释「7 张 baseline 表 + orders + order_items」（实际还含 reviews、favorites）。仅为文档漂移。
- `TestFavoriteAddAndList` 未显式断言列表项 `created_at`（收藏时间）非空——`favoriteItemData` 未含 `CreatedAt` 字段。实现已正确填充，仅为测试覆盖略欠，不影响正确性。
