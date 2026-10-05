# Cleaner Findings

## Review Target

- 任务：`product-review-v1`（商品评价 V1）
- 分支：`feat/comment`
- **本轮审查对象（最终实现）**：`c71372944128b565534815b769cc094caaf015de`（`feat(review): implement product review V1 core flow`），含全部生产代码、测试、Migration 与 `docs/design/review.md`（15 文件，+1344/-10）。
- **当前 HEAD / `review.target_base`**：`237663eb5d6658fea2c4201fc6062a431ce40d14`（`chore(product-review-v1): persist task artifacts and invalidate review`，仅新增 `.agent/tasks/product-review-v1/` 任务工件，不改生产代码）。
- 工作区状态：**clean**（`git status --short` 为空）。`c713729 → HEAD` 的 diff 仅 `.agent/tasks/product-review-v1/` 六个任务工件文件，无任何生产/测试/Migration/Design 变化。
- Registry Authority（`origin/develop` = `45e2df258575a49346efa72553e42afa2b277589`）：
  - 错误码域 `10000-10999`，owner `product-review-v1`，`RESERVED`
  - migration `20261001000009`（reviews），owner `product-review-v1`，`RESERVED`
- 权限 code：`review:take_down`（B 类 namespace，`internal/boot/seed.go` 登记）。

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
| AC-001（提交评价·购买资格） | PASS | `TestReviewCreatePublicListAndSummary`：登录买家对已完成订单项提交 5 星评价，断言 200、归属 user_id 正确、product/sku 服务端推导正确、status=published |
| AC-002（资格拒绝） | PASS | `TestReviewEligibilityRejected`：未购买（他人订单项）、订单未完成（待支付）、订单项不存在均 409/10002 且 `reviewCount()==0`；未登录由 `middleware.Auth` 401 兜底（共享中间件） |
| AC-003（服务端归属绑定） | PASS | `TestReviewServerDerivesOwnership`：提交伪造 `user_id/product_id/sku_id`，断言服务端以 `Principal.UserID` 与订单项为准，伪造值被忽略 |
| AC-004（每个已购项最多一次） | PASS | `TestReviewDuplicateRejected`（顺序重复 409/10003）+ `TestReviewConcurrentDuplicateSingleRow`（8 并发仅 1 成功，`-race` 通过），`uk_order_item` 唯一约束兜底 |
| AC-005（公开列表与汇总） | PASS | `TestReviewCreatePublicListAndSummary`（avg=5.0/count=1）+ `TestReviewUpdateAndDelete`（修改后 avg=3.0、删除后 count=0/avg=0），实时聚合 |
| AC-006（归属隔离） | PASS | `TestReviewUserIsolation`：他人 PUT/DELETE 404/10001、不存在 404/10001，且 DB status 未被篡改 |
| AC-007（管理员审核/下架） | PASS | `TestReviewAdminTakeDownPermission`：无权限管理员 403/1003、普通用户 token 403、超管下架成功后评价从公开列表与汇总消失 |
| AC-008（非法输入拒绝） | PASS | `TestReviewInvalidInputRejected`：星级 0/6、内容空/全空白/501 字均 400/10004，`order_item_id` 缺失 400/1001，均无写入 |
| AC-009（数据模型与迁移） | PASS | `internal/migrations` 全部测试通过；`expectedSchema` 含 `reviews` 表结构快照，`latestMigrationVersion=20261001000009`、`businessTables` 含 `reviews` |
| AC-010（长期设计） | PASS | `docs/design/review.md` 与 APPROVED Contract 及最终实现逐项一致 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| Registry Authority（origin/develop） | PASS | `git show origin/develop:...` 确认 `10000-10999` / `20261001000009` 均为 `product-review-v1`、`RESERVED`；`origin/develop = 45e2df2` |
| 三边一致性（Registry ↔ Contract ↔ 实现） | PASS | `bash scripts/check-registry.sh`：无重复/无漂移；10001-10004 落 RESERVED 域（预期中间态） |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `go test -p 1 ./...` | PASS | 全部包 `ok`（exit 0） |
| Review 专项测试（`-count=1` 非缓存） | PASS | 9 个 `TestReview*` 全绿 |
| 并发重复评价 `-race` | PASS | `TestReviewConcurrentDuplicateSingleRow -race` 通过 |
| 迁移结构等价 | PASS | `TestSchemaStructureMatchesBaseline`、`TestUpCreatesSchemaAndIsIdempotent` 通过 |
| workflow-check | PASS | `go run ./cmd/workflow-check .agent/tasks/product-review-v1` → `PASS task=product-review-v1`，exit 0 |

独立验证环境：MySQL 8.0（`my-shop-mysql`）与 Redis 7（`my-shop-redis`）容器 healthy，集成测试走真实 `RegisterFrontendRoutes`+`Auth` 与 `RegisterAdminRoutes`+`AdminAuth/RequirePermission`。

## Findings

### CLEAN-001：Contract「待写入 Registry」中错误码域 owner 旧措辞

- Severity：P3
- Status：OPEN
- Location：`.agent/tasks/product-review-v1/contract.md` §Global Resource Reservation（第 153 行）
- AC / Invariant：Registry ↔ Contract 资源元数据一致性
- Trigger：对比 contract.md 第 153 行与 `.agent/registry/error-codes.md` 实际内容
- Actual：contract.md 第 153 行将错误码域 owner 写作 `review`；实际 Registry（origin/develop `45e2df2`）已统一为 `product-review-v1`（task slug）。migrations 行（第 154 行）已正确写 `product-review-v1`
- Expected：owner 统一为 `product-review-v1`
- Impact：仅「待写入 Registry」描述中的旧措辞；不造成资源归属实质歧义（域 `10000-10999` / version `20261001000009` 唯一、Registry Authority 正确、业务设计与实现无漂移）
- Evidence：`git show origin/develop:.agent/registry/error-codes.md` 显示 `| 10000-10999 | product-review-v1 | RESERVED |`；`git show 45e2df2` 将 `review`→`product-review-v1`
- Required Fix Boundary：由 Analyst/Owner 将 contract.md 第 153 行 owner 措辞对齐为 `product-review-v1`（Cleaner 不修改 Contract）

（P3 非阻塞，由 Owner 决定是否处理；不阻塞本轮 CLEAN。）
