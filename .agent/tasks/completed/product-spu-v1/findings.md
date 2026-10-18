# Cleaner Findings

## Review Target

- 任务：`product-spu-v1`（COMPLEX），Contract 状态 `APPROVED`（含 CONTRACT_REVISION：`4007`、FK 1451→409、keyword 转义）。
- 初审版本：HEAD `c51273c`（`feat(product): 新增商品 SPU 管理接口`）。
- 复审版本：HEAD `64f3e75`（`docs(product-spu-v1): 更新审查发现文档`）+ 工作区未提交改动 `internal/controller/product/product_test.go`（+198 行，为 Coder 对 CLEAN-001~004 的测试修复）。
- 实现 Diff 边界：生产代码自 `c51273c` 起未变（`git diff 440c671 c51273c` 17 文件 +1797/-30）；复审仅新增测试、不改生产代码。
- 环境：MySQL 8.0 / Redis 7 容器就绪；项目测试入口 `go test -p 1 ./...`（`scripts/test.sh` 已说明跨包共享同库需串行）。

## Result

CLEAN

结论：初审的 1 个 P2（CLEAN-001）与 3 个 P3（CLEAN-002/003/004）均已由 Coder 补齐测试并独立验证通过；23 项 AC 全部有代码与运行证据支撑；无开放 P0/P1/P2。生产实现正确、完整，未发现新问题。

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestProductCreateAndValidation`：合法创建断言 200/0、`status=draft`（DB=0）、price=1234、图片持久化。 |
| AC-002 | PASS | 创建/查询断言整数分；update 侧由 `TestProductUpdate` 断言 price=200 整数分往返。 |
| AC-003 | PASS | 创建侧负/非整数/超限 → 400/4002 且无写入；update 侧由 `TestProductUpdate` 断言同样拒绝且价格不变。 |
| AC-004 | PASS | 创建侧 4003/4004/4007；update 侧由 `TestProductUpdate` 断言非法分类拒绝且分类不变。 |
| AC-005 | PASS | `TestProductStateMachine`：update 提交合法 status 被忽略、非法 status 4006。 |
| AC-006 | PASS | `TestProductStateMachine`：draft→on_shelf 成功（DB=1）。 |
| AC-007 | PASS | `TestProductStateMachine`：on_shelf→off_shelf 成功（DB=2）。 |
| AC-008 | PASS | `TestProductStateMachine`：off_shelf→on_shelf 成功。 |
| AC-009 | PASS | `TestProductStateMachine`：on→on、off→off、draft→off 均 409/4005 且状态不变。 |
| AC-010 | PASS | `TestProductConcurrentTransition`：10 并发上架恰好 1 成功 9 冲突、最终 DB=1；`-race` 通过。 |
| AC-011 | PASS | `TestProductVisibility`：前台列表仅 on_shelf（total=1）。 |
| AC-012 | PASS | `TestProductVisibility`：前台详情 draft/off_shelf/不存在均 404/4001。 |
| AC-013 | PASS | `TestProductVisibility`：后台列表 total=3、后台详情三态可见。 |
| AC-014 | PASS | `TestProductListFilterSortPagination`：category_id 精确匹配。 |
| AC-015 | PASS | 同测试：分页结构 `{items,total,page,size}`、size 钳制 100。 |
| AC-016 | PASS | 同测试：keyword=Apple 命中 2 条；转义见 `TestProductKeywordEscaping`（`%`/`_`/`\`）。 |
| AC-017 | PASS | 同测试：sort 白名单生效、非法字段回落默认、无注入。 |
| AC-018 | PASS | 创建侧图片持久化+sort；update 侧由 `TestProductUpdate` 断言 images 全量替换/省略保留/写入失败回滚。 |
| AC-019 | PASS | `TestProductAuthorization`：无 token 写 → 401/1002。 |
| AC-020 | PASS | 同测试：用户 token 403、无 product 权限管理员 403 且查库无写入。 |
| AC-021 | PASS | `TestProductAuthorization` 超管成功 + `TestProductCreateWithGrantedPermission` 非超管持 `product:create` 端到端成功。 |
| AC-022 | PASS | `TestCategoryDeleteProtection`：分类下有商品删除 → 409/3005，分类与商品均不受影响。 |
| AC-023 | PASS | 同测试：无商品分类删除成功，详情 404/3001。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 无输出，exit 0。 |
| `go vet ./...` | PASS | 无输出，exit 0。 |
| `go test -p 1 ./...` | PASS | 全包 ok（含 `controller/product` 4.829s）。 |
| `go test -p 1 -run 'TestProductUpdate|TestProductOnShelfCategoryRevalidation|TestProductCreateWithGrantedPermission|TestProductKeywordEscaping' -v ./internal/controller/product/` | PASS | 4 个新增/修改测试全部 PASS。 |
| `go test -race -p 1 ./internal/controller/product/` | PASS | ok，无 data race。 |
| `go test ./...`（无 `-p 1`） | 环境特性（非缺陷） | 跨包共享同库清表竞争，项目约定入口为 `-p 1`。 |

## Findings

### CLEAN-001：Update 端点业务校验与图片原子性完全无测试 — CLOSED

- Severity：P2
- Status：CLOSED
- Location：`internal/logic/product/product.go:174-264`（`Update`）
- 复审结论：Coder 新增 `TestProductUpdate`（`product_test.go:779-867`），覆盖合法字段更新（name/brand/category_id/price/main_image/images）、`images` 全量替换与省略保留、非法价格（-1/12.5/100000000）拒绝且价格不变、非法分类（不存在 4003/非叶子 4004/禁用 4007）拒绝且分类不变、图片写入失败（URL 超 VARCHAR(512)）→ 500/1000 且商品名与图片均回滚无半成品。已独立运行通过。
- Evidence：`go test -p 1 -run TestProductUpdate -v` PASS。

### CLEAN-002：上架时分类有效性重校验无测试 — CLOSED

- Severity：P3
- Status：CLOSED
- Location：`internal/logic/product/product.go:305-309`
- 复审结论：Coder 新增 `TestProductOnShelfCategoryRevalidation`（`product_test.go:869-903`），覆盖分类禁用（4007）与变为非叶子（4004）后再次上架被拒且状态保持 off_shelf。已独立运行通过。
- Evidence：`go test -p 1 -run TestProductOnShelfCategoryRevalidation -v` PASS。

### CLEAN-003：keyword 反斜杠 `\` 转义无测试 — CLOSED

- Severity：P3
- Status：CLOSED
- Location：`internal/logic/product/product.go:537-541`
- 复审结论：`TestProductKeywordEscaping` 新增字面反斜杠用例（`a\b`，URL 编码后 `a%5Cb`），断言仅命中含字面 `\` 的商品。已独立运行通过。
- Evidence：`go test -p 1 -run TestProductKeywordEscaping -v` PASS。

### CLEAN-004：AC-021 非超管持权限成功未端到端验证 — CLOSED

- Severity：P3
- Status：CLOSED
- Location：`internal/controller/product/product_test.go`
- 复审结论：Coder 新增 `TestProductCreateWithGrantedPermission`（`product_test.go:905-922`），建角色→授 `product:create`→建普通管理员→分配角色→登录后创建商品成功，与 AC-020 无权限 403 形成对照，锁定 seed 权限 code 与路由 `require(...)` 一致。已独立运行通过。
- Evidence：`go test -p 1 -run TestProductCreateWithGrantedPermission -v` PASS。

## 备注

- 生产代码自初审后未变更，无 P0/P1 缺陷。状态机并发（条件 UPDATE + `RowsAffected`）、前后台可见性、keyword 转义 + 参数化绑定、分类删除保护（3005 + FK 1451→409）、RBAC 路由与权限 seed 均正确且与 Contract 一致。
- `go test ./...`（不加 `-p 1`）失败为项目既有环境特性，非本任务引入。
- Coder 修复尚未提交（`product_test.go` 位于工作区）；交付前需提交，但不影响本次审查结论。
