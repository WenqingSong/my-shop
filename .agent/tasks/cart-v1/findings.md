# Cleaner Findings

## Review Target

- 任务基线（base commit）：`5f160ab632e719d1fce2167bf6cada2541c74ae1`（分支 `feat/cart`，HEAD 为「合并来自 develop 的合并请求 #11」；`task.md` Review Baseline 声明该点 working tree clean）。
- 当前版本（复审）：HEAD = `1c36c3f`（`fix(cart): 修复相同值更新误判 404 及缺失 sku_id 返回 500`），其下为 `7e2bd66`（移除临时探针）、`70e19ed`（`feat(cart): 实现购物车 v1 功能`）。审查时 working tree clean。
- 审查范围：`git diff 5f160ab..HEAD` 中与 cart 相关的文件（同首审，另含修复提交 `1c36c3f` 对 `api/cart/v1/cart.go`、`internal/logic/cart/cart.go`、`internal/controller/cart/cart_test.go` 的改动）。
- 任务前已有修改区分：基线 tree clean，cart 相关 diff 均为本任务产物。
- 关键配置/迁移版本：MySQL 8.0（`mysql:8.0`）、Redis 7（`redis:7-alpine`）；迁移 latest = `20261001000005`。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（列表/空列表） | PASS | `List` 按 `ci.user_id` 过滤；`TestCartListEmptyAndAdd` 断言空购物车返回空列表、非错误。 |
| AC-002（添加 SKU） | PASS | `Add` 先显式校验 `sku_id<=0 → 1001/400`，再 `ISku.GetByID`→enabled→`IProduct.GetByID`→on_shelf；`TestCartAddValidation` 断言 SKU 不存在 5001/404、下架/禁用 7003/409、缺失/0 sku_id 400/1001，均无写入。 |
| AC-003（重复添加累加） | PASS | `INSERT ... AS new ON DUPLICATE KEY UPDATE cart_items.quantity = cart_items.quantity + new.quantity`（原子累加）；`TestCartAddAccumulate` 断言 3+5=8 且仅 1 条记录。 |
| AC-004（改数量边界） | PASS | `UpdateQuantity` 校验 1..999；`TestCartQuantityBoundary` 断言 0/-1/1.5/1000 → 400/7002 且原值不变。 |
| AC-005（删除） | PASS | `Delete` 按 `id AND user_id` + `RowsAffected`；`TestCartDelete` 断言删除成功、重复删除 404/7001。 |
| AC-006（勾选） | PASS | `UpdateSelected` 持久化 `selected`；`TestCartSelected` + `TestCartSameValueUpdateIdempotent` 断言勾选/取消/相同值均正确。 |
| AC-007（下架/禁用标识） | PASS | `toCartItem` 动态计算 `available`/`unavailable_reason`；`TestCartUnavailable` 断言 off_shelf/disabled 且条目保留、数量未变。 |
| AC-008（价格变化） | PASS | `price_snapshot` 快照 + 联查 `current_price` 对比；`TestCartPriceChanged` 断言快照不变、current=250、changed=true。 |
| AC-009（库存不足） | PASS | `insufficient = available && quantity > stock`（无记录=0）；`TestCartInsufficient` 断言不足标识且不自动调整数量。 |
| AC-010（用户隔离） | PASS | 全部写 `WHERE id AND user_id`、列表按 user_id 过滤；`TestCartUserIsolation` 断言 B 查/改/勾/删 A 条目均 404 且无写入。 |
| AC-011（必须登录） | PASS | 路由挂 `middleware.Auth` 分组；`TestCartRequiresAuth` 断言无 token 401/1002 且无数据。 |
| INV-007（SKU 删除软引用） | PASS | 无 FK、`LEFT JOIN` 容忍缺失；`TestCartSkuDeleted` 断言 SKU 删除不被阻塞、条目保留且 `sku_deleted`/null 字段。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| go build ./... | PASS | 退出码 0，无输出。 |
| go vet ./... | PASS | 退出码 0，无输出。 |
| gofmt | PASS | 对修复改动文件 `gofmt -l` 无输出。 |
| go test -p 1 ./... | PASS | 全量通过（cart、migrations、boot、cmd 等全部 ok）。 |
| go test -race ./internal/controller/cart/ | PASS | 14 个测试全部通过（含新增 `TestCartSameValueUpdateIdempotent`、扩展后的 `TestCartAddValidation`，以及 `TestCartConcurrentAdd`），无 data race。 |
| 环境 | 就绪 | MySQL、Redis 均 `Up (healthy)`；迁移已应用至 `20261001000005`。 |

## Findings

### CLEAN-001：相同值的改数量/勾选被误判为 404 —— CLOSED

- Severity：P2 → 已修复
- Status：CLOSED
- Location：`internal/logic/cart/cart.go:160`（`UpdateQuantity`）、`:191`（`UpdateSelected`）
- 修复：`RowsAffected()==0` 后回查 `itemExists`（`WHERE id AND user_id`）：命中（值未变）视为幂等成功，仅真正未命中返回 404。
- 复审验证：新增回归测试 `TestCartSameValueUpdateIdempotent`（相同 quantity/selected → 200，数量与勾选不变）通过；`TestCartDelete`（重复删除 404）、`TestCartUserIsolation`（B 操作 A 条目 404）仍通过，证明「未命中 404」未被放宽。`go test -race ./internal/controller/cart/` 全绿。

### CLEAN-002：缺失必填 sku_id 返回 500/非业务码 51 —— CLOSED

- Severity：P2 → 已修复
- Status：CLOSED
- Location：`api/cart/v1/cart.go:46`（移除 `v:"required"`）+ `internal/logic/cart/cart.go:83`（`req.SkuId <= 0 → 1001`）
- 修复：`Add` 显式校验 `sku_id<=0` 返回 `1001`（400），不再依赖 gvalid 的 `required`，避免非业务 gcode 51 透出为 500。
- 复审验证：`TestCartAddValidation` 新增断言「缺失 sku_id / sku_id=0 → 400/1001」，通过；`go test -race ./internal/controller/cart/` 全绿。

### CLEAN-003：`ON DUPLICATE KEY UPDATE ... VALUES()` 已弃用语法 —— CLOSED

- Severity：P3 → 已修复
- Status：CLOSED
- Location：`internal/logic/cart/cart.go:113`
- 修复：改用行别名 `INSERT ... VALUES (...) AS new ON DUPLICATE KEY UPDATE cart_items.quantity = cart_items.quantity + new.quantity`，消除 `VALUES()` 弃用隐患。
- 复审验证：`TestCartAddAccumulate`（3+5=8）、`TestCartConcurrentAdd`（20×5=100 无丢失更新）通过，证明原子累加语义不变。

（无开放 P0/P1/P2/P3；本轮结论 CLEAN。）
