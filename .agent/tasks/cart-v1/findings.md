# Cleaner Findings

## Review Target

- 任务基线（base commit）：`5f160ab632e719d1fce2167bf6cada2541c74ae1`（分支 `feat/cart`，HEAD 为「合并来自 develop 的合并请求 #11」；`task.md` Review Baseline 声明该点 working tree clean）。
- 当前版本：HEAD = `7e2bd66`（`test(cart): 移除临时清理探针测试`），其下为 `70e19ed`（`feat(cart): 实现购物车 v1 功能`）。全部购物车变更已提交，审查时 working tree clean（`git status --short` 为空）。
- 审查范围：`git diff 5f160ab..HEAD` 中与 cart 相关的文件——新增 `api/cart/v1`、`internal/controller/cart`、`internal/logic/cart`、`internal/service/cart.go`、migration `20261001000005_cart_items.up.sql`、`docs/design/cart.md`、`internal/migrations/sql/...cart_items`，以及修改的 `internal/codes/codes.go`、`internal/cmd/routes_frontend.go`、`internal/logic/logic.go`、`internal/service/{sku,product}.go`、`internal/logic/{sku,product}/*.go`、`internal/migrations/migrations_test.go`、`internal/boot/boot_migration_test.go` 与对应测试。
- 任务前已有修改区分：基线 tree clean，故 cart 相关 diff 均为本任务产物，无重叠历史修改需剥离。
- 关键配置/迁移版本：MySQL 8.0（`mysql:8.0` 容器，实际 8.0.x）、Redis 7（`redis:7-alpine`）；迁移 latest = `20261001000005`。
- 本审查独立执行了 build/vet/gofmt/test（含 `-race`）与临时探针（探针文件已删除，未进入交付）。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（列表/空列表） | PASS | `List` 按 `ci.user_id` 过滤；`TestCartListEmptyAndAdd` 断言空购物车返回空列表、非错误。 |
| AC-002（添加 SKU） | PASS | `Add` 依次 `ISku.GetByID`→enabled→`IProduct.GetByID`→on_shelf；`TestCartAddValidation` 断言 SKU 不存在 5001/404、下架/禁用 7003/409 且无写入。 |
| AC-003（重复添加累加） | PASS | `INSERT ... ON DUPLICATE KEY UPDATE quantity = quantity + VALUES(quantity)`；`TestCartAddAccumulate` 断言 3+5=8 且仅 1 条记录。 |
| AC-004（改数量边界） | PASS | `UpdateQuantity` 校验 1..999；`TestCartQuantityBoundary` 断言 0/-1/1.5/1000 → 400/7002 且原值不变。 |
| AC-005（删除） | PASS | `Delete` 按 `id AND user_id` + `RowsAffected`；`TestCartDelete` 断言删除成功、重复删除 404/7001。 |
| AC-006（勾选） | PASS | `UpdateSelected` 持久化 `selected`；`TestCartSelected` 断言勾选/取消持久化且列表可见。 |
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
| gofmt | PASS | 对全部改动文件 `gofmt -l` 无输出。 |
| go test -p 1 ./... | PASS | 全量通过（`internal/controller/cart`、`internal/migrations`、`internal/boot`、`internal/cmd` 等全部 ok）。`-p 1` 为仓库既定约束（`scripts/test.sh`）。 |
| go test -race ./internal/controller/cart/ | PASS | 全部测试通过，含 `TestCartConcurrentAdd`（20×5=100 无丢失更新），无 data race 报告。 |
| 环境 | 就绪 | MySQL、Redis 均 `Up (healthy)`；迁移已应用至 `20261001000005`。 |
| 临时探针（已删除） | 记录 | 实测 ① 相同 quantity/selected 更新 → 404/7001；② `POST /cart/items` 缺失 `sku_id` → 500/非业务码 51。详见 Findings。 |

## Findings

### CLEAN-001：相同值的改数量/勾选被误判为 404

- Severity：P2
- Status：OPEN
- Location：`internal/logic/cart/cart.go:152`（`UpdateQuantity`）、`:176`（`UpdateSelected`）
- AC / Invariant：AC-005/AC-006、INV-001（改数量/勾选按 `id AND user_id` 定位，未命中才 404）
- Trigger：登录用户对「存在且归属本人」的条目设置与当前完全相同的 `quantity` 或 `selected`。
- Actual：MySQL 对值未变化的 UPDATE 返回 0 rows affected（受影响行 = 实际变化行数，非匹配行数），`RowsAffected()==0` 被当作「未命中」统一返回 `7001`（404）「购物车条目不存在」。
- Expected：条目命中且归属本人时，幂等设置相同值应返回成功；仅当条目确实不存在/已删除/属于他人时才返回 404。
- Impact：客户端幂等重放（重复勾选同一状态、重复提交相同数量、双击）被误报为「条目不存在」，展示错误提示，属可复现的业务行为错误。
- Evidence：临时探针（已删除）实测 `PUT /cart/items/:id {"quantity":5}`（当前值 5）→ 404/7001；`PUT /cart/items/:id/selected {"selected":true}`（当前 true）→ 404/7001。
- Required Fix Boundary：区分「行不存在/不属于本人」与「值未变化」。`RowsAffected==0` 后应回查 `WHERE id AND user_id` 是否存在：存在则视为幂等成功，不存在才 404；或采用等价方式，使「命中但值未变」的合法请求成功、仅真正未命中返回 404。

### CLEAN-002：缺失必填 sku_id 返回 500/非业务码 51 而非 400

- Severity：P2
- Status：OPEN
- Location：`api/cart/v1/cart.go:46`（`SkuId int64 v:"required"`）+ `internal/middleware/response.go:31`（gvalid 错误未映射为业务码）
- AC / Invariant：AC-002（稳定错误）、AGENTS.md §7（必填/类型校验）
- Trigger：`POST /cart/items` 缺失 `sku_id`。
- Actual：GoFrame gvalid `required` 拒绝后，`codes.FromError` 取到 gvalid 的 gcode `51`（非业务码），`HTTPStatus(51)` 落回 500、message=「内部错误」，客户端收到 `code=51`。
- Expected：缺失必填参数应返回稳定 400 参数错误（如 `1001` 或 `7002`），不得 500，且不得向客户端暴露非业务 gcode。
- Impact：客户端漏传 sku_id 时被误报为服务端错误（500），并泄漏内部 gcode `51`；错误语义不达标。
- Evidence：临时探针（已删除）实测 `POST /cart/items {"quantity":1}` → `status=500 code=51 msg="内部错误"`（对比 `sku_id=0` → 5001/404，说明 gvalid 仅拦截「字段缺失」而非 0 值）。
- Required Fix Boundary：缺失/非法 `sku_id` 必须返回稳定 400 参数错误码（建议在 controller/service 显式校验 `SkuId<=0`），不得 500 或非业务 gcode；不要求改动全局 gvalid 映射。

### CLEAN-003：`ON DUPLICATE KEY UPDATE ... VALUES()` 已弃用语法

- Severity：P3
- Status：OPEN
- Location：`internal/logic/cart/cart.go:106`
- AC / Invariant：INV-002（原子累加、无丢失更新）
- Trigger：MySQL 升级到移除 `VALUES()` 语法的版本后，重复添加累加路径可能报语法错误。
- Actual：当前 MySQL 8.0.x 下工作正常（集成测试全绿），但 `VALUES()` 在 `ON DUPLICATE KEY UPDATE` 中自 MySQL 8.0.20 起已弃用并计划移除。
- Expected：改用行别名语法（`INSERT ... VALUES (...) AS new ON DUPLICATE KEY UPDATE quantity = quantity + new.quantity`）或等价写法。
- Impact：仅前向兼容/维护风险，当前无功能影响（与 `inventory-v1` 的 CLEAN-001 同类）。
- Evidence：`go test -race ./internal/controller/cart/` 当前通过；docker `mysql:8.0`。
- Required Fix Boundary：保证累加 upsert 语义不变（首次建记录、并发累加不丢增量、不产生重复行）前提下替换弃用语法；不改动接口/错误码。

（无 P0/P1 问题；P2 见 CLEAN-001/CLEAN-002，阻塞 CLEAN。）
