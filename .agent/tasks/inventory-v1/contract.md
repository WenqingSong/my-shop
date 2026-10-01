# Technical Contract

## Decision Status

APPROVED

（2026-10-01 Owner 确认全部 4 项决定：increase(delta)+惰性库存记录模型、V1 不做业务幂等键、收紧 SKU 物理删除规则、库存不足 409 + `inventory:*` 权限；并明确流水字段与同事务要求。Contract 与 Task 兼容，转 APPROVED。）

## Problem

在已实现的 SKU 之上，基于 `sku_id` 建立独立的「普通库存」模型：库存查询、库存初始化/增加、条件扣减（充足才成功）、防负库存、库存变更流水，并以并发扣减测试证明核心不变量。为后续订单模块预留稳定的库存扣减语义（service 方法可复用），但当前无订单模块、无 MQ、无缓存库存，事实来源为单一 MySQL。

任务边界：不引入订单/购物车、不引入 MQ/幂等键/补偿、不引入库存锁定/预占、不做多仓/批次、不做缓存库存、不改前端。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2.10.3（Go 1.23.0），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta`）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 dao/model 层（`go.mod`、`internal/logic/logic.go`、`internal/service/*.go`）。
- VERIFIED：`skus` 表已存在（`internal/migrations/sql/20261001000003_skus.up.sql`），**无 `stock` 字段**；`sku-v1` Contract 已 APPROVED 并明确库存与 SKU 元数据解耦、由本任务基于 `sku_id` 独立建立。
- VERIFIED：`ISku` 接口（`internal/service/sku.go`）仅有 `Create`/`Update`/`Delete`/`ListByProduct`，**无 `Exists`**。`IProduct.Exists` / `ICategory.Exists` 均为 `Count > 0` 模式（`internal/logic/product/product.go:302`、`categories.go:225`），库存校验 `sku_id` 存在性需为 `ISku` 新增同名方法。
- VERIFIED：RBAC 完整。`internal/middleware/auth.go` 的 `AdminAuth` + `RequirePermission(code)`（超管 `IsSuper` 放行、DB 授权失败 fail-closed 500）。`internal/boot/seed.go` 的 `seedPermissionList` 当前 23 个权限（含 3 个 `sku:*`），无库存权限；`seed_test.go` 以 `len(seedPermissionList)` 断言数量，新增权限无需改数字。
- VERIFIED：错误码集中 `internal/codes/codes.go`：通用 1000-1005、IAM 2001-2010、分类 3001-3005、商品 4001-4007、SKU 5001-5004；**库存域 6000-6999 空闲**。统一响应 `{code,message,data}`，客户端靠 `code` 判型，HTTP 状态由 `codes.HTTPStatus` 映射。
- VERIFIED：迁移机制 golang-migrate v4（`internal/migrations/migrations.go`）：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，baseline `...001`、products `...002`、skus `...003`；`serve` 不自动执行（`my-shop migrate up`）。`migrations_test.go` 硬编码 `latestMigrationVersion = 20261001000003` 与 `businessTables` 列表（按 FK 依赖排序 drop），新增表必须同步更新。
- VERIFIED：路由分离 `internal/cmd/routes_admin.go`（`AdminAuth` + `require(permission)`，后台商品/SKU 查询仅 `AdminAuth` 无读权限）与 `routes_frontend.go`（公开）。事务模式 `g.DB().Transaction(ctx, func(ctx, tx gdb.TX) error {...})`（`internal/logic/product/product.go:150`、`admin.go`）。
- VERIFIED：事实来源单一 MySQL；Redis 仅用于会话；无 MQ、无订单模块、无缓存库存。docker-compose 使用 `mysql:8.0`（支持 CHECK 约束），但既有迁移未使用 CHECK。
- VERIFIED：`internal/controller/admin/admin.go` 已示范 controller 层经 `middleware.AdminPrincipalFromContext(ctx)` 读取 `AdminID`（`internal/middleware/principal.go`），作为操作者记录的依据。
- VERIFIED：SKU 删除为物理删除，`skus.product_id` FK `ON DELETE RESTRICT`（`20261001000003_skus.up.sql`）；SKU 删除后其库存记录/流水交互未定义。
- UNKNOWN：无阻塞性 UNKNOWN。MySQL 精确小版本（是否 8.0.16+）未验证，但因推荐方案不依赖 CHECK 约束，不影响设计。

## Recommendation

RECOMMENDATION：独立 `inventories`（1:1 `sku_id` 唯一）+ `inventory_logs`（流水）两张表；库存「无记录 = 0 库存」惰性建模，不侵入 SKU 创建；提供「增加（入库，兼作初始化）」与「条件扣减」两个受保护后台接口，核心扣减逻辑封装为可复用 `IInventory.Deduct` service 方法；防负库存用「条件更新 + `RowsAffected`」为主、`quantity INT UNSIGNED` 为类型兜底；流水与库存变更同事务；SKU 删除交互用 FK `ON DELETE RESTRICT` 兜底并给 SKU 删除加最小错误映射。

关键取舍：**「增加(delta) + 条件扣减」两接口、惰性建记录**，而非「设置绝对量」或「SKU 创建自动建 0 记录」——前者最贴合 AC-002「按提交量增加」与 AC-003/004「充足才成功」的语义，且保持库存域与 SKU 域解耦（SKU 创建不被侵入）。代价是「盘点/修正到任意绝对值」需组合增加/扣减完成（盘点/出入库单据已在 Out of Scope，V1 不提供单步 set）。

分项推荐：

1. **数据模型（Q1）**：`inventories` 表 `id BIGINT UNSIGNED AUTO_INCREMENT PK`、`sku_id BIGINT UNSIGNED NOT NULL UNIQUE`（1:1）、`quantity INT UNSIGNED NOT NULL DEFAULT 0`、`created_at`/`updated_at`；FK `sku_id → skus.id ON DELETE RESTRICT`。不自动建 0 记录，「无记录 = 0 库存」。
2. **初始化/增加（Q1）**：受保护 `POST /admin/inventories/:sku_id/increase`（`quantity` 正整数），事务内原子 upsert（`INSERT ... ON DUPLICATE KEY UPDATE quantity = quantity + N`），首次增加即初始化，产生「增加」流水。
3. **扣减形态（Q2）**：受保护 `POST /admin/inventories/:sku_id/deduct`（`quantity` 正整数）；核心逻辑封装 `IInventory.Deduct(ctx, skuID, qty, operatorID)` 供未来订单模块复用。**幂等键不做**（Out of Scope，订单模块补）。
4. **错误码（Q3）**：库存域新开 6000-6999：`6001 CodeInventoryInsufficient`（库存不足 → 409）、`6002 CodeInventoryInvalidQuantity`（数量非正整数 → 400）。`sku_id` 不存在复用 `5001 CodeSkuNotFound`（404）。另新增 `5005 CodeSkuHasInventory`（409）供 SKU 删除被库存 FK 阻塞时返回（最小调整）。
5. **可见性与权限（Q4）**：后台查询 `GET /admin/inventories/:sku_id` 与流水查询 `GET /admin/inventories/:sku_id/logs` 均仅 `AdminAuth`（无读权限，同后台商品查询先例）；前台不公开。写权限 `inventory:increase`（增加/初始化）、`inventory:deduct`（扣减），追加到 `seedPermissionList`。
6. **流水（Q5）**：`inventory_logs` 表 `id`、`sku_id`（FK RESTRICT）、`change_type TINYINT`（1=increase/2=deduct）、`change_qty INT UNSIGNED`（正变更量）、`before_qty`/`after_qty INT UNSIGNED`、`operator_admin_id BIGINT UNSIGNED NULL`（软引用，无 FK，取自 AdminPrincipal）、`reason VARCHAR(255) NOT NULL DEFAULT ''`、`created_at`；索引 `idx_sku_id`。每次成功变更记一条，失败（库存不足）不记。
7. **防负库存与并发（Q6）**：扣减用条件更新 `UPDATE ... SET quantity = quantity - ? WHERE sku_id = ? AND quantity >= ?` + 核对 `RowsAffected`（同 `product.transition` 既有模式），`quantity INT UNSIGNED` 兜底；不加 CHECK（unsigned 已足够，避免版本依赖）。增加用原子 upsert。库存变更 + 流水写入必须同事务。
8. **SKU 删除交互（Q7）**：`inventories.sku_id`、`inventory_logs.sku_id` 均 `ON DELETE RESTRICT`；SKU 删除在命中 FK 冲突（MySQL 1451）时返回稳定 `5005`（409），其余不变（无库存记录的 SKU 删除不受影响）。

## Selected Design

Owner 已确认（2026-10-01）全部设计问题：

1. **数据模型与初始化/增加（Q1）**：`inventories` 表 `id`/`sku_id`（1:1 唯一 `uk_sku_id`）/`quantity INT UNSIGNED NOT NULL DEFAULT 0`/`created_at`/`updated_at`；FK `sku_id → skus.id ON DELETE RESTRICT`。不自动建 0 记录，**无 inventory 记录视为 `quantity=0`**；首次 `increase` 建立记录。
2. **增加/扣减原子性（Q1/Q2/Q6）**：`increase` 用原子 upsert（`INSERT ... ON DUPLICATE KEY UPDATE quantity = quantity + N`），`uk_sku_id` 唯一约束兜底并发，保证首次创建/并发 increase 不产生重复记录、不丢失增量；`deduct` 用条件更新 `UPDATE ... WHERE quantity >= ?` + `RowsAffected`，保证原子条件扣减与防负库存。
3. **幂等（Q2）**：V1 不实现业务幂等键；`Deduct` 只保证原子条件扣减与防负库存，不宣称业务请求幂等。订单模块接入时基于真实 order/operation 标识补幂等扣减。
4. **SKU 删除规则（Q7）**：SKU 一旦存在 inventory 记录或库存流水，不允许物理删除。应用层提供友好校验（SKU `Delete` 识别 FK 冲突 1451 → 5005/409），数据库 FK `ON DELETE RESTRICT` 负责最终完整性保护。这是 Inventory 引入后的合理跨模块约束。
5. **错误码与权限（Q3/Q4）**：库存不足 `6001` → 409；数量非法 `6002` → 400；SKU 不存在复用 `5001` → 404；SKU 有库存 `5005` → 409。权限 `inventory:increase`/`inventory:deduct`。
6. **流水（Q5）**：`inventory_logs` 至少记录 `sku_id`、变更类型 `change_type`（1=increase/2=deduct）、变更量 `change_qty`（delta）、`before_qty`、`after_qty`、`created_at`，另含可空 `operator_admin_id` 与默认空 `reason`；每次成功变更记一条，**库存变更与对应流水写入同一事务**，失败（库存不足）不记流水。

其余 SKU 与库存解耦边界保持不变，不重新展开。

## Interfaces and Data

### 数据表（经 golang-migrate 新增单文件迁移）

迁移文件：`internal/migrations/sql/20261001000004_inventory.up.sql`（version 紧随 skus `...003`；落地时若被占用取下一个更大 14 位时间戳）。DDL 不用 `IF NOT EXISTS`。

```sql
CREATE TABLE inventories (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  sku_id     BIGINT UNSIGNED NOT NULL,
  quantity   INT UNSIGNED    NOT NULL DEFAULT 0,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_sku_id (sku_id),
  CONSTRAINT fk_inventories_sku FOREIGN KEY (sku_id) REFERENCES skus(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE inventory_logs (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  sku_id            BIGINT UNSIGNED NOT NULL,
  change_type       TINYINT         NOT NULL,
  change_qty        INT UNSIGNED    NOT NULL,
  before_qty        INT UNSIGNED    NOT NULL,
  after_qty         INT UNSIGNED    NOT NULL,
  operator_admin_id BIGINT UNSIGNED NULL,
  reason            VARCHAR(255)    NOT NULL DEFAULT '',
  created_at        DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_sku_id (sku_id),
  CONSTRAINT fk_inventory_logs_sku FOREIGN KEY (sku_id) REFERENCES skus(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

- `change_type`：DB `1=increase`、`2=deduct`；API 出参字符串 `"increase"`/`"deduct"`（同 SKU `status` 的 TINYINT↔string 映射风格）。
- `quantity` 为正整数（增加/扣减均 `≥1`），非法返回 6002；无额外业务上限，由 `INT UNSIGNED` 边界兜底。

### 路由（`internal/cmd/routes_admin.go`）

| 方法/路径 | 保护 | 权限 code | 说明 |
| --- | --- | --- | --- |
| `GET /admin/inventories/:sku_id` | `AdminAuth` | - | 查询当前库存（无记录 = 0） |
| `POST /admin/inventories/:sku_id/increase` | `AdminAuth` | `inventory:increase` | 增加/初始化库存，记「增加」流水 |
| `POST /admin/inventories/:sku_id/deduct` | `AdminAuth` | `inventory:deduct` | 条件扣减，充足才成功，记「扣减」流水 |
| `GET /admin/inventories/:sku_id/logs` | `AdminAuth` | - | 查询该 SKU 流水（`id` 倒序） |

前台不公开任何库存接口。

### 权限 seed 新增（2 个，追加到 `seedPermissionList`）

`inventory:increase`、`inventory:deduct`。

### 代码接口

- 新增 `api/inventory/v1`（`Inventory` 结构 + `GetReq/GetRes`、`IncreaseReq/IncreaseRes`、`DeductReq/DeductRes`、`ListLogsReq/ListLogsRes`、`Log` 结构）、`internal/controller/inventory`、`internal/service`（`IInventory` 接口 + `RegisterInventory`）、`internal/logic/inventory`（`init()` 注册）；`internal/logic/logic.go` 注册 inventory 包。
- `Inventory` 对外字段：`sku_id`、`quantity`、`created_at`、`updated_at`。`Log` 对外字段：`id`、`sku_id`、`change_type`（字符串）、`change_qty`、`before_qty`、`after_qty`、`operator_admin_id`（可空）、`reason`、`created_at`。
- `IInventory` 接口：`Get(ctx, skuID int64) (*v1.Inventory, error)`；`Increase(ctx, skuID, qty int64, operatorID *int64) (*v1.Inventory, error)`；`Deduct(ctx, skuID, qty int64, operatorID *int64) (*v1.Inventory, error)`；`ListLogs(ctx, skuID int64) ([]*v1.Log, error)`。`operatorID` 为 nil 表示无操作者（供未来订单模块/系统调用）。
- 为 `ISku` 新增 `Exists(ctx, id int64) (bool, error)`（同 `IProduct.Exists` 模式，`internal/logic/sku/sku.go` 补实现），供库存校验 `sku_id` 存在性。
- Controller 的 `Increase`/`Deduct` 经 `middleware.AdminPrincipalFromContext(ctx)` 取 `AdminID` 作为 `operatorID` 传入；查询接口不传操作者。

### 错误码新增（`internal/codes/codes.go`）

| code | 语义 | HTTP |
| --- | --- | --- |
| 6001 | INVENTORY_INSUFFICIENT（库存不足） | 409 |
| 6002 | INVENTORY_INVALID_QUANTITY（数量非正整数） | 400 |
| 5005 | SKU_HAS_INVENTORY（SKU 存在库存记录，不能删除） | 409 |

复用：`5001`（SKU 不存在 → 404）、`1001`（参数错误 → 400）、`1002`（401）、`1003`（403）。

## Business Invariants

- INV-001（防负库存）：任何成功的增加/扣减后 `inventories.quantity ≥ 0`，并发扣减亦不例外。
- INV-002（充足才成功）：扣减仅在 `quantity ≥ N` 时成功；不足时返回 6001 且库存与流水均不变。
- INV-003（流水与库存一致）：每次成功的增加/扣减在同事务内产生一条流水，`after_qty = before_qty ± change_qty` 且等于变更后当前库存；失败（库存不足）不产生成功流水。
- INV-004（1:1 归属）：`inventories.sku_id` 唯一，每 SKU 至多一条库存记录；变更只作用于指定 SKU，不串号。
- INV-005（RBAC）：未认证 401、无对应 `inventory:*` 权限 403 且无写入、持有权限（含超管）成功。
- INV-006（SKU 引用有效）：`sku_id` 必须指向存在的 SKU；不存在返回 5001（404）且无写入；FK RESTRICT 保证库存记录不悬空。

## Failure and Consistency Semantics

- 事实来源：单一 MySQL。`skus`（SKU 存在性与 `id` 稳定键）、`inventories`（当前库存数量，事实来源）、`inventory_logs`（审计流水，只追加）。
- 成功语义：查询成功代表返回时点 `sku_id` 的库存数量（无记录 = 0）；增加/扣减成功代表该次变更已持久化且对应流水已落库（同事务）。
- 原子性：每次成功的增加/扣减 = 「库存变更 + 一条流水」同事务提交；任一失败整体回滚，库存与流水均不变。
- 扣减失败（库存不足）：条件更新 `RowsAffected == 0` → 返回 6001，事务回滚，不写流水、库存不变。
- 增加：原子 upsert（`ON DUPLICATE KEY UPDATE quantity = quantity + N`），并发增加无丢失更新；`before_qty = after_qty - N` 在同一事务内取锁后的最新值。
- 校验失败（SKU 不存在 5001、数量非法 6002、未认证 401、未授权 403）：写入前拒绝且无写入。
- DB 技术错误：统一 `CodeInternalError` 500，不泄漏底层细节；FK 冲突（SKU 删除）由 SKU 域识别 1451 → 5005（409）。
- 并发目标落到可验证结果：并发扣减同一 SKU 最终库存 ≥ 0、成功扣减总量 = 初始库存 − 最终库存、成功次数 ≤ ⌊初始库存/单次扣减量⌋；并发增加总量正确（无丢失更新）。
- 无 Redis、无 MQ、无异步、无跨系统事务；不引入幂等/去重/补偿（订单模块任务补）。

## Allowed / Forbidden Changes

允许：
- 新增迁移 `internal/migrations/sql/20261001000004_inventory.up.sql`（`inventories` + `inventory_logs`，DDL 不用 `IF NOT EXISTS`）；同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion`（`...003` → `...004`）与 `businessTables`（`inventory_logs`、`inventories` 排在 `skus` 之前，保证 drop 顺序满足 FK）。
- 新增 `api/inventory/v1`、`internal/controller/inventory`、`internal/service`（`IInventory`）、`internal/logic/inventory`、错误码 6001/6002/5005、权限 seed 2 个、`internal/cmd/routes_admin.go` 路由挂载。
- 为 `ISku` 新增 `Exists` 方法并补实现（最小扩展，不改既有方法语义）。
- 为 `internal/logic/sku/sku.go` 的 `Delete` 增加 FK 冲突（1451）识别并返回 5005（最小兜底调整，Out of Scope 明确允许）。
- 新增对应测试（见 Verification Requirements）。

禁止：
- 不改动已合入的 baseline/products/skus 迁移及迁移机制本身；不回退到 `boot.go` 建表。
- 不改动 `skus` 表结构、SKU 既有接口语义（`Create`/`Update`/`Delete`/`ListByProduct` 行为不变，`Delete` 仅新增「有库存记录时 5005」一个拒绝分支）。
- 不改动 `products`/`categories`/IAM 等既有域行为；不引入订单、购物车、MQ、缓存库存、库存锁定/预占、幂等键、多仓/批次。
- 前台不新增任何库存接口；不新增 `inventory:list` 读权限。
- 不 fail-open 放行未认证/未授权请求；不在 SQL 中拼接前端输入；不把 `operator_admin_id` 建成 FK（软引用，避免与 `admins` 删除耦合）。

## Verification Requirements

- INV-001 → MySQL + `-race`：并发扣减同一 SKU（多 goroutine），断言最终 `quantity ≥ 0`。需 MySQL。
- INV-002 → MySQL：库存充足扣减成功、库存减少 N；不足扣减断言 409/6001、库存不变、流水无新增。需 MySQL。
- INV-003 → MySQL：增加/扣减后查 `inventory_logs`，断言 `change_type`/`change_qty`/`before_qty`/`after_qty` 与库存变化一致、`after_qty` 等于当前库存、每条成功变更恰好一条流水。需 MySQL。
- INV-004 → MySQL：同一 SKU 反复增加不产生重复 `inventories` 行（`uk_sku_id` 唯一）；多 SKU 变更不串号。需 MySQL。
- INV-005 → MySQL + Redis：无 token 401、无权限管理员 403（查库无写入）、有权限成功、超管放行。需 MySQL + Redis。
- INV-006 → MySQL：`sku_id` 不存在时查询/增加/扣减均 5001（404）且无写入；查 `inventories`/`inventory_logs` 确认 `sku_id` FK 存在。需 MySQL。
- 并发扣减（AC-007）→ MySQL + `-race`：初始库存 S，n 个 goroutine 各扣减 d，断言最终库存 = S − 成功次数×d ≥ 0，成功次数 ≤ ⌊S/d⌋。需 MySQL。
- 迁移结构 → MySQL：核对 `latestMigrationVersion`、`businessTables` 更新后 `go test ./internal/migrations/` 通过。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；MySQL/Redis 集成验证需容器就绪（`docker compose up -d`）。

## Open Risks

- SKU 删除行为变化：有库存记录（或流水）的 SKU 因 FK RESTRICT 无法物理删除，SKU 删除接口新增 409/5005 拒绝分支。这是对 SKU V1「物理删除」语义的最小收紧，需 Owner 确认接受。
- 「设置绝对量/盘点修正」不提供单步接口，需组合增加/扣减完成；盘点/出入库单据已在 Out of Scope。
- 幂等键缺失：扣减无去重，未来订单模块接入时必须补 `request_id`/`deduction_no` 防重复扣减（当前无订单模块、后台入口，风险可接受）。
- 流水无分页（V1 全量返回，按 `id` 倒序）；量大时由后续任务加分页，当前 SKU 变更频次低。

## Owner Decision Record

Owner 于 2026-10-01 确认：

1. **Q1 模型**：采用「increase(delta) + 惰性库存记录」模型；无 inventory 记录视为 `quantity=0`，首次 increase 建立记录；`sku_id` 保持 1:1 唯一，首次创建/并发 increase 必须原子一致，不允许重复记录或丢失增量。
2. **Q2 幂等**：Inventory V1 暂不实现业务幂等键；订单模块接入时再基于真实 order/operation 标识实现幂等扣减；当前 `Deduct` 保证原子条件扣减与防负库存，不宣称业务请求幂等。
3. **Q7 SKU 删除**：SKU 一旦存在 inventory 记录或库存流水，不允许物理删除，返回稳定 409；应用层友好校验 + 数据库引用约束（FK RESTRICT）最终兜底；该规则为 Inventory 引入后的合理跨模块约束。
4. **Q3/Q4 错误与权限**：库存不足 HTTP 409；权限 `inventory:increase`/`inventory:deduct`。
5. **Q5 流水**：`inventory_logs` 至少记录 `sku_id`、变更类型、delta、变更前 quantity、变更后 quantity、`created_at`；库存变更与对应流水写入同一事务。

适用范围：`inventory-v1` 本任务。其余已确认的 SKU 与库存解耦边界保持不变。

待办（已完成）：
- [x] Analyst 记录 Owner 决定并转 `APPROVED`（2026-10-01）。Contract 与 Task 一致，无需变更 Task Scope/AC。
