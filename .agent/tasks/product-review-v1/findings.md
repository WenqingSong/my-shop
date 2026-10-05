# Cleaner Findings

## Review Target

- 任务：`product-review-v1`（商品评价 V1）
- 分支：`feat/comment`
- 任务基线（base commit）：`e0a48e9f023809d279e61fe7a0052a7cff461095`
- 当前 HEAD（含 Registry Reservation commit）：`20046a0cc3be4bc1d5658b9b915a6649f567cf5d`
  - `20046a0` = `chore(registry): reserve review error-code domain 10000-10999 and migration 20261001000009`（只改 `.agent/registry/error-codes.md` 与 `migrations.md`）
- 工作区状态：上述 HEAD 之上存在**未提交**的实现变更（8 个已跟踪文件修改 + 7 个新增文件/目录），均为本任务产物，无无关修改。
- 区分方式：任务基线 `e0a48e9` 时工作区干净；本次变更全部来自 product-review-v1。Registry Reservation 已在 HEAD commit 落入共享 develop 事实（`RESERVED` 状态）。

关键配置/资源：

- 错误码域：`10000-10999`（域序 10，Registry `RESERVED`）
- migration version：`20261001000009`（Registry `RESERVED`）
- 权限 code：`review:take_down`（B 类 namespace，seed 登记）

被审查文件（`review.target_paths`）：

```
api/review/v1/review.go
internal/controller/review/review.go
internal/logic/review/review.go
internal/service/review.go
internal/cmd/review_test.go
internal/migrations/sql/20261001000009_reviews.up.sql
docs/design/review.md
internal/boot/boot_migration_test.go
internal/boot/seed.go
internal/cmd/routes_admin.go
internal/cmd/routes_frontend.go
internal/cmd/routes_test.go
internal/codes/codes.go
internal/logic/logic.go
internal/migrations/migrations_test.go
```

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（提交评价·购买资格） | PASS | `TestReviewCreatePublicListAndSummary`：登录买家对已完成订单项提交 5 星评价，断言 200/归属 user_id 正确、product/sku 服务端推导正确、status=published |
| AC-002（资格拒绝） | PASS | `TestReviewEligibilityRejected`：未购买（他人订单项）、订单未完成（待支付）、订单项不存在均 409/10002 且 `reviewCount()==0`；未登录由 `middleware.Auth` 401 兜底（共享中间件，已有独立测试覆盖） |
| AC-003（服务端归属绑定） | PASS | `TestReviewServerDerivesOwnership`：提交伪造 `user_id/product_id/sku_id`，断言服务端以 `Principal.UserID` 与订单项为准，伪造值被忽略 |
| AC-004（每个已购项最多一次） | PASS | `TestReviewDuplicateRejected`（顺序重复 409/10003）+ `TestReviewConcurrentDuplicateSingleRow`（8 并发仅 1 成功，`-race` 通过），`uk_order_item` 唯一约束兜底 |
| AC-005（公开列表与汇总） | PASS | `TestReviewCreatePublicListAndSummary`（avg=5.0/count=1）+ `TestReviewUpdateAndDelete`（修改后 avg=3.0、删除后 count=0/avg=0），实时聚合 |
| AC-006（归属隔离） | PASS | `TestReviewUserIsolation`：他人 PUT/DELETE 404/10001、不存在 404/10001，且 DB status 未被篡改 |
| AC-007（管理员审核/下架） | PASS | `TestReviewAdminTakeDownPermission`：无权限管理员 403/1003、普通用户 token 403、超管下架成功后评价从公开列表与汇总消失 |
| AC-008（非法输入拒绝） | PASS | `TestReviewInvalidInputRejected`：星级 0/6、内容空/全空白/501 字均 400/10004，`order_item_id` 缺失 400/1001，均无写入 |
| AC-009（数据模型与迁移） | PASS | `internal/migrations` 全部测试通过；`expectedSchema` 新增 `reviews` 表结构快照（列/类型/默认值/索引 `uk_order_item`/`idx_user_id`/`idx_product_status`），`latestMigrationVersion=20261001000009`、`businessTables` 含 `reviews` |
| AC-010（长期设计） | PASS | `docs/design/review.md` 与 APPROVED Contract 及最终实现逐项一致（数据模型/资格校验/汇总/权限边界/错误码域/权限 code） |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| 三边一致性（Registry ↔ Contract ↔ 实现） | PASS | `bash scripts/check-registry.sh`：无重复/无漂移；错误码 10001-10004 落 RESERVED 域 10000-10999（预期中间态）；migration 20261001000009 已登记 |
| `go build ./...` | PASS | 无输出（成功） |
| `go vet ./...` | PASS | 无输出（成功） |
| `go test -p 1 ./...` | PASS | 全部包 `ok`（含 `internal/cmd`、`internal/migrations`、`internal/boot` 等） |
| 并发重复评价 `-race` | PASS | `go test -p 1 ./internal/cmd/ -run TestReviewConcurrentDuplicateSingleRow -race` 通过 |
| 迁移结构等价 | PASS | `go test -p 1 ./internal/migrations/` 全绿（含 `TestSchemaStructureMatchesBaseline` 校验 `reviews` 结构） |
| 路由表 | PASS | `TestRouteTable` 通过，新增 6 条评价路由均已锁定 |

独立验证环境：MySQL 8.0（`my-shop-mysql`）与 Redis 7（`my-shop-redis`）容器均 healthy，集成测试走真实 `RegisterFrontendRoutes`+`Auth` 与 `RegisterAdminRoutes`+`AdminAuth/RequirePermission`。

## Findings

No actionable findings.

（备注：`docs/design/migration.md` 的「当前迁移清单」与 `docs/design/error-codes.md` 的域分配表记录的是「合并后」语义，review 的 00009 / 10000-10999 当前为 Registry `RESERVED`、尚未合并进 `develop`，由合并任务在 `RESERVED→ACTIVE` 时同步，不构成本任务缺陷。）
