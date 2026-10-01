# Cleaner Findings

## Review Target

- 任务：`product-spu-v1`（COMPLEX），Contract 状态 `APPROVED`（含 2026-10-01 CONTRACT_REVISION：`4007`、FK 1451→409、keyword 转义）。
- 审查版本：分支 `feat/spu`，HEAD = `c51273c`（`feat(product): 新增商品 SPU 管理接口`），工作区干净（`git status --short` 为空）。
- 实现 Diff 边界：`git diff 440c671 c51273c`（17 文件，+1797/-30）。父提交 `440c671` 为契约冻结，`05c2072` 已并入 db-migration 前置任务（不属 SPU 改动）。
- 新增/改动产物：`api/product/v1/product.go`、`internal/controller/product/`、`internal/logic/product/product.go`、`internal/service/product.go`、`internal/codes/codes.go`（3005、4001-4007）、`internal/migrations/sql/20261001000002_products.up.sql`、`internal/boot/seed.go`（+4 权限）、`internal/cmd/routes_{admin,frontend}.go`、`internal/logic/categories/categories.go`（Exists/HasChildren/IsEnabled + Delete 商品保护）、`internal/logic/logic.go`、以及对应测试。
- 基线说明：`task.md` 的 Review Baseline 仍写旧 base `2d9bb93`（db-migration 合并前），Contract「待办」中的“Task Builder 同步 task.md”未勾选；审查以已批准的 Contract 与当前 HEAD 为准（代码与 Contract 一致，任务文档基线滞后属 Task Builder 同步项，不计入 Coder 范围）。
- 环境：MySQL 8.0 / Redis 7 容器已就绪（`my-shop-mysql`、`my-shop-redis`）；项目测试入口为 `go test -p 1 ./...`（`scripts/test.sh` 已说明跨包共享同库需串行）。

## Result

CHANGES_REQUIRED

结论：生产实现正确、完整，AC 核心行为均有代码与测试支撑，无 P0/P1 生产缺陷；但 `PUT /admin/products/:id`（Update 端点）的业务校验与图片原子性没有任何测试，属于对关键不变量（INV-005/007/008）的保护缺口，Contract Verification Requirements 明确要求的 update 侧验证未落地。阻塞点为一个 P2（CLEAN-001），另有三个 P3。

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestProductCreateAndValidation`：合法创建断言 200/0、`status=draft`（DB=0）、price=1234、图片持久化。 |
| AC-002 | PASS | 创建/查询已断言整数分（DB `INT UNSIGNED` + `int64` + `json.Number`）；update 侧复用同一 `parsePrice`（独立测试缺失，见 CLEAN-001）。 |
| AC-003 | NOT_VERIFIED | 创建侧负/非整数/超限 → 400/4002 且无写入已验证；update 侧非法价格拒绝无任何运行证据。 |
| AC-004 | NOT_VERIFIED | 创建侧 4003/4004/4007 已验证；update 提交非法 `category_id` 拒绝无任何运行证据。 |
| AC-005 | PASS | `TestProductStateMachine`：update 提交合法 status 被忽略、非法 status 返回 4006。 |
| AC-006 | PASS | `TestProductStateMachine`：draft→on_shelf 成功（DB=1）。 |
| AC-007 | PASS | `TestProductStateMachine`：on_shelf→off_shelf 成功（DB=2）。 |
| AC-008 | PASS | `TestProductStateMachine`：off_shelf→on_shelf 成功。 |
| AC-009 | PASS | `TestProductStateMachine`：on→on、off→off、draft→off 均 409/4005 且状态不变。 |
| AC-010 | PASS | `TestProductConcurrentTransition`：10 并发上架，断言恰好 1 成功 9 冲突、最终 DB=1；`-race` 通过。 |
| AC-011 | PASS | `TestProductVisibility`：前台列表仅 on_shelf（total=1）。 |
| AC-012 | PASS | `TestProductVisibility`：前台详情 draft/off_shelf/不存在均 404/4001，on_shelf 可见。 |
| AC-013 | PASS | `TestProductVisibility`：后台列表 total=3、后台详情三种状态均可见。 |
| AC-014 | PASS | `TestProductListFilterSortPagination`：category_id 精确匹配（catA=2 条）。 |
| AC-015 | PASS | 同测试：分页结构 `{items,total,page,size}`、size=9999 钳制为 100。 |
| AC-016 | PASS | 同测试：keyword=Apple 命中 2 条（name/brand LIKE）；转义见 `TestProductKeywordEscaping`（`%`/`_`）。 |
| AC-017 | PASS | 同测试：sort=price&order=asc 生效；`sort=evil;DROP+TABLE` 回落默认不报错无注入。 |
| AC-018 | NOT_VERIFIED | 创建侧图片持久化+sort 已断言；update 侧 `images` 全量替换及失败回滚无运行证据。 |
| AC-019 | PASS | `TestProductAuthorization`：无 token 写 → 401/1002。 |
| AC-020 | PASS | 同测试：前台用户 token → 403/1003；无 product 权限管理员 → 403 且查库无写入。 |
| AC-021 | PASS | 同测试：超管创建成功；非超管持 `product:create` 的端到端成功未验证（见 CLEAN-004）。 |
| AC-022 | PASS | `TestCategoryDeleteProtection`：分类下有商品删除 → 409/3005，分类与商品均不受影响。 |
| AC-023 | PASS | 同测试：无商品分类删除成功，详情 404/3001。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 无输出，exit 0。 |
| `go vet ./...` | PASS | 无输出，exit 0。 |
| `go test -p 1 ./...` | PASS | 全包 ok（含 `controller/product` 3.631s、`migrations`、`middleware`、`boot` 等）。 |
| `go test -race -p 1 -run 'TestProductConcurrentTransition|TestProductStateMachine|TestProductVisibility' ./internal/controller/product/` | PASS | ok，无 data race。 |
| `go test ./...`（无 `-p 1`） | FAIL（环境特性，非缺陷） | 跨包共享同库清表导致 `admins` 等表被并发 DROP；项目约定入口为 `-p 1`（`scripts/test.sh` 已注释说明）。 |
| 迁移版本/结构 | PASS | `migrations_test.go` 断言 latest=`20261001000002`、9 张业务表；`boot_migration_test.go` 断言 readiness。 |
| 路由表 | PASS | `routes_test.go` 锁定商品前台/后台路径、无 `/api/v1`、`/admin/v1`、公开注册入口。 |

## Findings

### CLEAN-001：Update 端点业务校验与图片原子性完全无测试

- Severity：P2
- Status：OPEN
- Location：`internal/logic/product/product.go:174-264`（`Update`）；`internal/controller/product/product_test.go`（全文件仅 2 处 PUT，均为 status 字段，无 name/price/category_id/images 更新用例）
- AC / Invariant：AC-002/003/004/018、INV-005（修改 category_id 校验）、INV-007（图片替换原子性）、INV-008（update 价格拒绝）
- Trigger：对 `Update` 移除价格/分类校验、或把 `replaceProductImages` 移出事务，运行 `go test -p 1 ./...`。
- Actual：更新接口没有任何“业务字段”测试；AC-003/004/018 的“创建与更新”一侧，仅创建路径有测试，更新路径零覆盖。
- Expected：覆盖 update 的合法字段更新（name/price/category_id/main_image/images）、非法价格/分类拒绝且无写入、`images` 全量替换（删旧插新）及任一步失败整体回滚。
- Impact：更新路径的价格/分类校验、图片替换原子性一旦回归，现有测试全部仍绿，无法识别错误实现；属 Contract Verification Requirements 明确要求（INV-005“修改 category_id”、INV-007“更新后查 product_images + 回滚”、INV-008“负数/超上限”）。
- Evidence：`product_test.go` 仅 `TestProductStateMachine` 对 `PUT` 提交 `{"status":...}`；无任何用例提交 `name/price/category_id/images`。`go test -p 1 ./...` 全绿但未覆盖该路径。
- Required Fix Boundary：补充 update 端点的集成测试，断言（1）合法字段更新成功且结果正确；（2）非法 price/category_id（不存在/非叶子/禁用）被拒且无写入；（3）`images` 提供时全量替换、未提供时保留；（4）模拟图片写入失败断言无半成品（事务回滚）。不规定具体测试写法。

### CLEAN-002：上架时分类有效性重校验（禁用/非叶子拒绝）无测试

- Severity：P3
- Status：OPEN
- Location：`internal/logic/product/product.go:305-309`（`transition` 的 `if onShelf { validateCategory }`）
- AC / Invariant：INV-005 上架分支；Contract Open Risks（“再次上架会因分类禁用/变非叶子被拒”）
- Trigger：商品绑定叶子分类 → 禁用该分类或使其变为非叶子 → 再次上架。
- Actual：代码正确调用 `validateCategory`，但无测试覆盖该拒绝路径；`TestProductStateMachine` 的 off_shelf→on_shelf 仅在分类仍为合法叶子时进行。
- Expected：断言分类禁用（4007）/非叶子（4004）时再次上架被拒且状态不变。
- Impact：若移除 `transition` 的上架校验，测试仍全绿，无法识别回归。
- Evidence：代码 `product.go:305-309` 存在校验，但 `product_test.go` 无对应用例。
- Required Fix Boundary：补充“商品已存在，分类状态变化后再次上架被拒”的集成测试。

### CLEAN-003：keyword 反斜杠 `\` 转义无测试

- Severity：P3
- Status：OPEN
- Location：`internal/logic/product/product.go:537-541`（`escapeLikeKeyword`）；`product_test.go:620-656`（`TestProductKeywordEscaping`）
- AC / Invariant：INV-009（`\`/`%`/`_` 转义后按普通文本匹配）
- Trigger：keyword 含字面 `\` 进行搜索。
- Actual：测试注释声称覆盖 `%/_/\`，但实际仅覆盖 `%` 与 `_`，无 `\` 用例。
- Expected：增加字面反斜杠匹配用例，验证不产生转义通配符效果。
- Impact：反斜杠转义（CONTRACT_REVISION 明确新增项）回归无法被测试捕获。
- Evidence：`product_test.go:630-655` 仅 `pctID`/`otherID`/`underID` 三个用例。
- Required Fix Boundary：补充 keyword 含字面 `\` 的匹配用例。

### CLEAN-004：AC-021「持有 product:create 权限的非超管管理员成功」未端到端验证

- Severity：P3
- Status：OPEN
- Location：`internal/controller/product/product_test.go:658-694`（`TestProductAuthorization`）
- AC / Invariant：AC-021（持对应权限的管理员可正常执行创建/更新/上架/下架）
- Trigger：非超管管理员经角色授予 `product:create` 后调用创建接口。
- Actual：仅验证超管（IsSuper 放行）成功；`RequirePermission` 正向路径仅在 `internal/middleware/auth_test.go` 覆盖，未在商品端点端到端验证。
- Expected：端到端验证“非超管 + 有 product:create → 成功”，与 AC-020 的“无权限 → 403”形成对照。
- Impact：seed 权限 code 与路由 `require(...)` 参数一旦错位，超管放行会掩盖问题。
- Evidence：`product_test.go` 仅 `superToken` 成功用例，无“建角色→授 product:create→普通管理员创建成功”。
- Required Fix Boundary：补充非超管持 `product:create` 权限的成功用例（可复用 categories 测试的建角色/授权手法）。

## 备注

- 生产代码未发现 P0/P1 缺陷：状态机并发用条件 UPDATE + `RowsAffected`（`product.go:311-333`）正确；前后台可见性由 `queryList`/`load` 的 `status` 条件强制；`escapeLikeKeyword` + `ESCAPE '\\'` + 参数化绑定正确；分类删除保护（应用层 3005 + FK 1451→409）正确；RBAC 路由与权限 seed 一致。
- `go test ./...`（不加 `-p 1`）失败为项目既有环境特性（跨包共享 MySQL 清表竞争），`scripts/test.sh` 已约定 `-p 1`，非本任务引入。
