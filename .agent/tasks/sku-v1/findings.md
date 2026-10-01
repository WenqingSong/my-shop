# Cleaner Findings

## Review Target

- 任务：`sku-v1`（SKU / 商品规格 V1）
- 基线 Base commit：`55cc88af78a00cf8e98dcbba23e2968505ef7d8f`（`main`，合并 MR #10）
- 审查对象（最终代码）：HEAD `c9388e2ff9bbf3af44de3b61a0f75d173541ffe6`（`feat(sku): 新增 SKU 接口与商品详情组合`）
- 工作区状态：clean（`git status --short` 为空），SKU 变更已全部提交于 HEAD；任务基线时工作区 clean，与 task.md Review Baseline 一致。
- 判定范围：`git diff 55cc88a..HEAD` 的全部变更（含新增文件），共 20 个文件、+1490/-20。
- 任务前已有修改区分方式：任务新增产物为 `.agent/tasks/sku-v1/`、`api/sku/v1`、`internal/controller/sku`、`internal/logic/sku`、`internal/service/sku.go`、`internal/codes`（SKU 段 5000-5999）、迁移 `20261001000003_skus.up.sql`、`internal/boot/seed.go`（3 个 `sku:*` 权限）、`internal/cmd/routes_admin.go`（SKU 路由）；对 `api/product/v1/product.go`、`internal/logic/product/product.go`、`internal/service/product.go` 为最小叠加（新增 `skus` 字段与 `Exists`），未改既有商品字段语义。基线（`20261001000001`）与 products（`20261001000002`）迁移未被改动。
- 关键配置：MySQL `root/root@127.0.0.1:3306/my_shop`、Redis `127.0.0.1:6379`（`docker compose` 容器就绪）。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `internal/controller/sku/sku_test.go::TestSkuCreateAndValidation` 合法创建断言 200/0 且 `dbSku` 核对 `product_id/name/price/status` 归属正确；`TestSkuCreateWithGrantedPermission` 非超管经角色授权后成功；`TestSkuAuthorization` 超管成功。 |
| AC-002 | PASS | 创建非法价（`-1`/`12.5`/`100000000`）断言 400/5002 且 `dbSkuCount` 无新增；更新非法价断言 5002 且 `dbSku` 原值不变（`TestSkuUpdate`）。 |
| AC-004 | PASS | `TestSkuCreateAndValidation`：`product_id=999999` 断言 404/4001 且 `dbSkuCount` 无新增。 |
| AC-005 | PASS | `TestSkuUpdate`：合法更新 name/price/status 后 `dbSku` 反映新值；非法值拒且原值不变；更新不存在断言 404/5001。 |
| AC-006 | PASS | `TestSkuDelete`：删除成功（200/0）；重复删除断言 404/5001。 |
| AC-007 | PASS | `TestSkuDelete`：删除后 `getAdminDetail` 商品仍存在，剩余 SKU 仅 id2。 |
| AC-008 | PASS | `TestSkuOneToManyIsolation`：多商品多 SKU，后台详情仅返回本商品 SKU（按 id 升序、product_id 归属正确）；`TestSkuNameUnique` 不同商品同名可共存。 |
| AC-009 | PASS | `TestSkuDetailVisibility`：上架商品前台详情仅 enabled SKU、后台详情含全部状态；draft 商品前台 404 不暴露 SKU。 |
| AC-010 | PASS | `TestSkuCreateAndValidation`：默认 enabled、显式 disabled、非法 status 断言 5003 无写入；`TestSkuUpdate` 非法 status 拒且原值不变。 |
| AC-011 | PASS | `TestSkuAuthorization`：无 token 401/1002、前台用户 token 403、无 `sku:*` 权限管理员 403 且无写入、超管成功；`TestSkuCreateWithGrantedPermission` 授权后成功。 |
| AC-012 | PASS | 迁移 DDL `id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT` 主键；`TestSkuDelete` 删除后新建 `id3 > id1`（自增不重用）；`TestUpCreatesSchemaAndIsIdempotent` 表存在且版本推进到 `20261001000003`。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0，无编译错误。 |
| `go vet ./...` | PASS | exit 0，无告警。 |
| `gofmt -l`（相关文件） | PASS | 无未格式化文件。 |
| `go test ./... -p 1 -count=1` | PASS | 全量包串行通过，含 `internal/controller/sku`（4.3s）、`internal/migrations`（8.1s）、`internal/boot`（2.7s）。 |
| MySQL/Redis 环境 | PASS | `my-shop-mysql`/`my-shop-redis` 容器 healthy；`mysqladmin ping` 正常。 |

说明：SKU 集成测试依赖共享 MySQL/Redis，多包并行会互相清表导致假失败（实测 `internal/migrations` 与 `internal/boot` 并行时出现 `Table already exists`/`doesn't exist`）；必须以 `-p 1` 串行运行，本记录以上述串行结果为准。这是测试基建的既有约束（migrations_test.go 已注明 `go test -p 1 串行`），非本任务缺陷。

## Findings

无 P0/P1/P2。以下为低风险观察，供 Owner 决定是否处理：

### CLEAN-001：迁移结构等价测试未覆盖 `skus` 表列级结构

- Severity：P3
- Status：OPEN
- Location：`internal/migrations/migrations_test.go`（`expectedSchema` 仅 7 张 baseline 表，不含 `skus`/`products`/`product_images`）
- AC / Invariant：AC-012 / INV-006（`skus.id` 为 `BIGINT UNSIGNED` 自增主键、稳定引用键）
- Trigger：`TestSchemaStructureMatchesBaseline` 仅对 `expectedSchema` 内 7 表做列级严格等价断言，不覆盖 `skus`。
- Actual：`skus` 的 DDL 正确性目前由「DDL 文件审阅 + `TestUpCreatesSchemaAndIsIdempotent` 表存在/版本推进 + `TestSkuDelete` 自增不回退」共同支撑，但无列级（类型/默认值/索引/字符集）自动化断言。
- Expected：可选将 `skus` 加入 `expectedSchema`（并同步 `TestSchemaStructureMatchesBaseline` 注释「7 张表」），使 DDL 漂移能被测试识别。
- Impact：当前无实际错误（DDL 已核对正确），但若后续误改 `skus` 列类型/约束，现有测试无法识别。属可维护性增强。
- Evidence：`migrations_test.go:360-473` 的 `expectedSchema` 仅含 users/categories/admins/roles/permissions/admin_roles/role_permissions。
- Required Fix Boundary：仅需补 `skus` 表的结构快照断言；不改动生产 DDL 语义。

### CLEAN-002：更新撞名（同商品 name 已存在）路径无显式测试

- Severity：P3
- Status：OPEN
- Location：`internal/logic/sku/sku.go::Update`（`isDuplicateKeyError` → 5004）
- AC / Invariant：INV-008（同商品 name 唯一，撞名 409/5004）
- Trigger：对已存在同商品同名 SKU 发起 `PUT /admin/skus/:id` 修改 name 为撞名值。
- Actual：代码经 `isDuplicateKeyError` 正确映射 1062 → 5004（与创建同一机制），但无测试显式覆盖「更新撞名」。
- Expected：补一条更新撞名断言 409/5004 的回归测试。
- Impact：代码行为当前正确，仅缺回归保护；风险低。
- Evidence：创建撞名已由 `TestSkuNameUnique` 覆盖，更新撞名未覆盖。
- Required Fix Boundary：仅补测试；不改变更新错误映射语义。

### CLEAN-003：详情响应的 `skus[].name/price` 未被显式断言

- Severity：P3
- Status：OPEN
- Location：`internal/controller/sku/sku_test.go`（`productDetailJSON`/`unmarshalDetail` 断言仅覆盖 id/product_id/status）
- AC / Invariant：AC-009（详情 SKU 列表包含 `name`/`price`/`status`）
- Trigger：详情测试仅断言 `status`、`id`、`product_id`，未断言 `name`/`price` 字段值。
- Actual：`name`/`price` 由 `toSku` 简单映射（与创建/更新出参共用同一 `Sku` 结构），创建/更新测试已核对 DB 值，但详情 JSON 的 `name`/`price` 未直接断言。
- Expected：可选在详情断言中补 `name`/`price` 字段值。
- Impact：映射函数简单、风险极低；缺一处断言完备性。
- Evidence：`sku_test.go:40-46` 的 `skuJSON` 含 Name/Price，但 `TestSkuDetailVisibility`/`TestSkuOneToManyIsolation` 未断言这两个字段。
- Required Fix Boundary：仅补断言；不改生产映射。

### CLEAN-004：两处过期注释

- Severity：P3
- Status：OPEN
- Location：`internal/boot/seed.go:27`（"20 个标准权限"，实际 23）；`internal/migrations/migrations_test.go:140`（"9 张业务表"，实际 10）
- AC / Invariant：无（纯注释）
- Trigger：阅读代码时误导。
- Actual：seed 权限现为 23（新增 3 个 `sku:*`）；`businessTables` 现为 10 张。
- Expected：更新注释数字。
- Impact：无功能影响，仅可读性。
- Evidence：`seedPermissionList` 共 23 项；`businessTables` 共 10 表。
- Required Fix Boundary：仅改注释文字。
