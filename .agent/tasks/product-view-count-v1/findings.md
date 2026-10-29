# Cleaner Findings

## Review Target

- Task：`product-view-count-v1`（商品浏览量计数 V1，COMPLEX，Design Impact = UPDATE）
- Contract：APPROVED，target `edfa74c3aebd2231c52fc1f0ce98014f16842fc3`
- Task Base commit：`019401567078329af4a42f0612028989846679b4`（`feature/goods-view-count`）
- 任务开始时已有修改：无（working tree clean）
- **Review Target（C1，被审查的 immutable implementation commit）**：`df272012781fa29120f5a1d5aec09cbde041d02b`
  - 提交说明：`feat(product): 商品浏览量计数 V1（view_count 原子自增 + 详情/列表展示）`
- C2（Coder 的 metadata commit，不在审查对象内）：`6bf92ec`（chore(state) 发起复审）
- 审查时 HEAD：`6bf92ec`，working tree clean（`git status --short` 为空）
- 审查 Diff 范围：`df27201` 相对其父 `94db402`，共 5 个文件（4 改 + 1 新增），无无关改动：

  | 文件 | 变更 |
  | --- | --- |
  | `api/product/v1/product.go` | `Product` 加 `ViewCount int64` |
  | `internal/logic/product/product.go` | `product` 加 `ViewCount`、`toProduct` 映射、`Detail` 加计数 UPDATE |
  | `internal/controller/product/product_test.go` | 新增 3 个集成测试 |
  | `internal/migrations/migrations_test.go` | `latestMigrationVersion` 11→12，probe/broken 版本 12→13 |
  | `internal/migrations/sql/20261001000012_product_view_count.up.sql` | 新增迁移 |

- 全局资源：migration version `20261001000012`（RESERVED）。**Registry 分配权威在 `origin/develop`**，Reservation commit `d07ffd4`（`origin/develop` 的 tip）已登记 `| 20261001000012 | product_view_count | product-view-count-v1 | RESERVED |`。本地 feature 分支的 `.agent/registry/migrations.md` 副本早于该 commit（未含 12），属预期的分支滞后，非漂移（见 Verification）。
- 关键配置/迁移版本：`latestMigrationVersion = 20261001000012`；`schema_migrations` 当前 `version=20261001000012, dirty=0`。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（计数触发 +1 且详情语义不变） | PASS | `internal/logic/product/product.go:102-112` 前台 `Detail` 先执行条件自增 UPDATE、`RowsAffected=1` 继续 `load` 正常返回。`TestProductViewCount`（`product_test.go:950`）断言首次详情后 `view_count==1`、DB 亦为 1、响应 200。 |
| AC-002（并发计数不丢失） | PASS | 单条 `UPDATE ... SET view_count = view_count + 1`（InnoDB 行锁）。`TestProductViewCountConcurrent`（`product_test.go:1051`）50 并发请求 + `-race`，断言最终 `view_count==50`。实测通过。 |
| AC-003（详情展示最新累计值） | PASS | 详情经 `load` 读回自增后最新值；`TestProductViewCount` 断言第二次详情 `view_count==2`。 |
| AC-004（展示位置符合确认语义） | PASS | `view_count` 加入共享 `Product`（`api/product/v1/product.go:29`），前台详情/列表、后台详情/列表均返回。`TestProductViewCount` 断言前台列表、后台列表、后台详情 `view_count==3`。 |
| AC-005（不存在/非上架 404 且无写入） | PASS | 条件 UPDATE `WHERE status=1` + `RowsAffected==0 → CodeProductNotFound`（404）。`TestProductViewCountNoWriteOn404`（`product_test.go:1016`）断言 draft/off_shelf/不存在 → 404/4001 且 `view_count` 仍 0。 |
| AC-006（数据模型与迁移、幂等） | PASS | `internal/migrations/sql/20261001000012_product_view_count.up.sql` 加列；`latestMigrationVersion=12`。`go test ./internal/migrations/` 通过（含 `TestUpCreatesSchemaAndIsIdempotent` 幂等）。实测 DB：`view_count bigint unsigned NOT NULL DEFAULT 0`，`schema_migrations version=20261001000012 dirty=0`。 |
| AC-007（长期设计一致） | PASS | `docs/design/product.md` §2.1/§2.4/§5 已沉淀 `view_count` 模型、计数语义、并发边界与展示协议，与 APPROVED Contract 及最终实现一致。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `gofmt -l`（5 个变更文件） | PASS | 无输出（全部已格式化） |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `go test ./internal/migrations/ -count=1` | PASS | `ok ... 14.501s`（迁移结构/幂等/dirty 恢复/并发 Up） |
| `go test ./internal/controller/product/ -race -count=1` | PASS | `ok ... 32.238s`（含 3 个新视图计数测试，`-race` 下无数据竞争） |
| `go test -p 1 ./...` | PASS | 全量通过，无失败包 |
| MySQL DDL 结果 | PASS | `SHOW COLUMNS ... LIKE 'view_count'` → `bigint unsigned / NO / 0`；`schema_migrations version=20261001000012 dirty=0` |
| `scripts/check-registry.sh` | 1 处 FAIL（语义为假阳性） | 报「迁移文件 20261001000012 未在 Registry 登记」——脚本读取本地 feature 分支的 Registry 副本（滞后于 develop）；分配权威 `origin/develop` @ `d07ffd4` 已登记 `20261001000012 RESERVED`，三边一致，非漂移。 |

三边一致性（Registry ↔ Contract ↔ 实现）：migration version `20261001000012` 在 Registry（`origin/develop` 权威，owner `product-view-count-v1`，RESERVED）、Contract（RESERVED，commit `d07ffd4`）、实现（迁移文件名 + `latestMigrationVersion`）三处一致；错误码复用 `4001`/`1000`，未新增域，`codes.go` 无改动。

四者一致性（Design Impact = UPDATE）：Task（UPDATE，artifact `docs/design/product.md`）↔ APPROVED Contract ↔ `docs/design/product.md` ↔ 最终实现，在数据模型（`view_count BIGINT UNSIGNED NOT NULL DEFAULT 0`）、计数语义（总浏览量不去重、仅前台公开详情 +1）、并发（原子自增）、失败语义（fail-hard）、`updated_at` 保护、展示位置（共享 `Product`，前后台详情/列表返回）均一致。

## Findings

No actionable findings.

（说明：`scripts/check-registry.sh` 的 1 处 FAIL 为本地 Registry 副本滞后 `origin/develop` 所致，非 Registry 权威漂移；权威源 `origin/develop` 已正确 RESERVED `20261001000012`，故不建 Finding。）
