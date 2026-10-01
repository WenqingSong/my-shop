# Technical Contract

## Decision Status

APPROVED

（2026-10-01 Owner 确认 Q1/Q2/Q4 及 Q3/Q5/Q6 方向，并决定库存边界修订：`skus` 不承载 `stock`，库存数据与操作交由后续 `4.3 普通库存` 独立 inventory 模型/表基于 `sku_id` 建立。task.md 已同步移除 stock 相关 Scope/AC/Assumption/Analyst Question，Contract 与 Task 一致，转 APPROVED。）

## Problem

交付「SKU / 商品规格」基础能力：在已实现的商品 SPU 之上建立「商品 1 — N SKU」关系，SKU 承载名称、价格（整数分）、状态；提供 SKU 创建、更新、删除；商品详情（前台 + 后台）组合 SPU 与 SKU；SKU 写操作受 RBAC 权限保护、前后台可见性隔离；为后续订单模块预留稳定 `skus.id` 引用键。

库存不进入 SKU 元数据：库存查询、条件扣减、防负库存、库存变更记录与并发扣减由后续 `4.3 普通库存` 独立承担，SKU V1 仅提供 `skus.id` 作为库存域的引用键。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2 / Go 1.23+，模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta`）→ `controller` → `service`（接口 + Register）→ `logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 dao/model 层。SKU 模块沿用此结构。
- VERIFIED：商品 SPU 已实现（`product-spu-v1` Contract `APPROVED`）。`products` 表：`id BIGINT UNSIGNED AUTO_INCREMENT`、`price INT UNSIGNED`（整数分，业务上限 `99,999,999`）、`status TINYINT`（`0=draft`/`1=on_shelf`/`2=off_shelf`）、`category_id` FK `ON DELETE RESTRICT`。`api/product/v1/product.go` 中 `Product` 结构体被前台/后台列表与详情复用；`DetailRes`/`AdminDetailRes` 均为 `struct { Product }` 嵌入，无 SKU 字段。
- VERIFIED：`internal/logic/product/product.go` 中商品详情经 `load(ctx, id, onlyOnShelf)` 加载；前台 `onlyOnShelf=true` 强制 `WHERE status=on_shelf`（否则 404），后台 `onlyOnShelf=false` 查看全部状态。列表经 `queryList`，`ListRes`/`AdminListRes` 的 `Items` 为 `[]*Product`。
- VERIFIED：`internal/service/product.go` 的 `IProduct` 接口方法：`List`/`Detail`/`AdminList`/`AdminDetail`/`Create`/`Update`/`OnShelf`/`OffShelf`/`CountByCategory`。**无 `Exists` 方法**（SKU 校验 `product_id` 存在性需新增）。
- VERIFIED：RBAC 完整实现。`internal/middleware/auth.go` 提供 `AdminAuth` 与 `RequirePermission(code)`（`IsSuper` 直接放行，DB 授权查询失败 fail-closed 500）；`internal/boot/seed.go` 幂等 seed 20 个权限（含 4 个 `product:*`），`seedPermissionList` 无 SKU 权限。`seed_test.go` 以 `len(seedPermissionList)` 断言数量，新增权限后自动通过。
- VERIFIED：`internal/cmd/routes_admin.go` 中商品写接口用 `require("product:xxx")` 挂载；后台商品查询 `GET /admin/products`、`GET /admin/products/:id` 仅 `AdminAuth`（无 `product:list` 读权限）。`routes_frontend.go` 中前台商品详情 `GET /products/:id` 为公开接口。
- VERIFIED：错误码集中 `internal/codes/codes.go`：通用 1000-1005、IAM 2001-2010、分类 3001-3005、商品 4001-4007。SKU 域未占用（5000+ 空闲）。`CodeProductNotFound=4001`（404）。
- VERIFIED：迁移机制 golang-migrate v4：文件 `internal/migrations/sql/{14位时间戳}_{title}.up.sql`，baseline `20261001000001`、products `20261001000002`；`boot.Bootstrap` 只读校验 schema 就绪（不回退建表）。SKU 新增迁移 `20261001000003_skus.up.sql`。
- VERIFIED：无订单模块（`api/`、`internal/`、迁移均无 `orders`/`order_items`）。`skus` 表不存在，无任何 SKU 后端代码，无任何 inventory 表。
- VERIFIED：统一响应 `{code,message,data}`（`internal/middleware/response.go`），客户端靠 `code` 判型；HTTP 状态由 `codes.HTTPStatus` 映射。
- UNKNOWN：无阻塞性 UNKNOWN。

## Recommendation

以下为 Owner 已确认的决定，Contract 据此固化（不再重新展开）：

1. **SPU/SKU 价格独立（Q1，已确认）**：`products.price` 继续作为 SPU 基础展示价，`skus.price` 为独立 SKU 售价；V1 不做 `min_price`/`max_price` 派生与自动同步。
2. **SKU 状态二态（Q2，已确认）**：`enabled`/`disabled`（DB `TINYINT` `1=enabled`/`0=disabled`，API 字符串 `"enabled"`/`"disabled"`），默认 `enabled`，不复用 SPU 三态。
3. **SKU 标识与唯一性（Q4，已确认）**：不引入 `sku_code`；同一商品下 `name` 唯一，用 `(product_id, name)` 复合唯一约束兜底，并提供稳定重复错误码（409 `5004 SKU_NAME_EXISTS`）。
4. **形态/错误码/权限/删除（Q3/Q5/Q6，已确认）**：SKU 列表内嵌进详情响应、写接口扁平路由；错误码新开 5000-5999；权限 `sku:create`/`sku:update`/`sku:delete`；物理删除、不预留软删字段。
5. **库存解耦（Owner 修订）**：`skus` 不包含 `stock` 字段，SKU V1 只承载 `id`/`product_id`/`name`/`price`/`status`/`created_at`/`updated_at`。库存查询、条件扣减、防负库存、库存变更记录、并发扣减由后续 `4.3 普通库存` 独立 inventory 模型/表基于 `sku_id` 建立关系。

关键取舍：库存从 SKU 元数据中剥离，避免 SKU 域与库存域耦合；代价是 SKU V1 的详情响应不含库存，前台库存展示需等待 4.3。这一取舍已由 Owner 明确决定。

## Selected Design

Owner 已确认（2026-10-01）全部设计问题，并决定库存解耦：

1. **价格（Q1）**：`products.price`（SPU 基础展示价）与 `skus.price`（SKU 售价）独立，无派生、无同步。
2. **状态（Q2）**：SKU `status` 二态 `enabled`/`disabled`，DB `TINYINT`（`1=enabled`、`0=disabled`），API 字符串枚举，默认 `enabled`，create/update 直接设置与校验。
3. **标识与唯一性（Q4）**：仅自增 `id` 作稳定引用键，不引入 `sku_code`；同一商品下 `name` 唯一（`UNIQUE KEY uk_product_name (product_id, name)`），重复返回 409 `5004`。
4. **形态（Q3）**：SKU 列表内嵌进 `DetailRes`/`AdminDetailRes`；写接口扁平 `POST /admin/skus`、`PUT /admin/skus/:id`、`DELETE /admin/skus/:id`；不新增独立 SKU 读接口、不新增 `sku:list` 读权限、列表不返回 SKU 数量/价格区间。
5. **错误码与权限（Q5）**：错误码新开 5000-5999（见 Error Semantics）；权限 `sku:create`/`sku:update`/`sku:delete`。
6. **库存（Owner 修订）**：`skus` 不含 `stock`；库存留给 4.3 独立 inventory 模型/表基于 `sku_id` 建立。
7. **删除（Q6）**：物理删除，不预留软删字段，稳定键由 `BIGINT UNSIGNED AUTO_INCREMENT` 保证不重用。

## Interfaces and Data

### 路由

| 方法/路径 | 保护 | 权限 code | 说明 |
| --- | --- | --- | --- |
| `POST /admin/skus` | `AdminAuth` | `sku:create` | 创建 SKU（`product_id` 在请求体） |
| `PUT /admin/skus/:id` | `AdminAuth` | `sku:update` | 更新 name/price/status（`product_id` 不可变） |
| `DELETE /admin/skus/:id` | `AdminAuth` | `sku:delete` | 物理删除，成功返回空 data |
| `GET /products/:id` | 公开 | - | 前台详情：`on_shelf` 商品返回 `skus`（仅 enabled） |
| `GET /admin/products/:id` | `AdminAuth` | - | 后台详情：返回 `skus`（全部状态） |

- SKU 写接口与商品详情组合都在既有路由文件挂载；SKU 无独立读接口、无独立列表接口。

### 权限 seed 新增（3 个，追加到 `seedPermissionList`）

`sku:create`、`sku:update`、`sku:delete`。

### 数据表（经 golang-migrate 新增迁移）

迁移文件：`internal/migrations/sql/20261001000003_skus.up.sql`（version 紧随 products `20261001000002`，落地时若被占用则取下一个更大的 14 位时间戳）。DDL 与 baseline 一致，**不使用 `IF NOT EXISTS`**。

```sql
CREATE TABLE skus (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  name       VARCHAR(128)    NOT NULL,
  price      INT UNSIGNED    NOT NULL,
  status     TINYINT         NOT NULL DEFAULT 1,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_product_id (product_id),
  UNIQUE KEY uk_product_name (product_id, name),
  CONSTRAINT fk_skus_product FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

- `status` 存储映射：`1=enabled`、`0=disabled`（默认 `enabled`）。
- `name`（trim 后非空，≤128 字符）、`price`（整数分，`0..99,999,999`）。
- **不含 `stock`**：库存不进入 SKU 元数据，交由 4.3 inventory 基于 `sku_id` 建立。
- `skus.product_id` FK → `products.id` `ON DELETE RESTRICT`（与 `products.category_id` 一致的安全默认；当前无商品删除接口，为兜底）。

### 代码接口

- 新增 `api/sku/v1`（`Sku` 结构 + Create/Update/Delete 的 Req/Res）、`internal/controller/sku`、`internal/service`（`ISku` 接口 + `RegisterSku`）、`internal/logic/sku`（`init()` 注册）；`internal/logic/logic.go` 注册 sku 包。
- `Sku` 对外字段：`id`、`product_id`、`name`、`price`、`status`、`created_at`、`updated_at`（**无 `stock`**）。
- `api/product/v1` 的 `DetailRes`/`AdminDetailRes` 增加 `Skus []*skuv1.Sku`（`json:"skus"`）；列表响应 `ListRes`/`AdminListRes` 与 `Product` 结构体**不加** `skus`（列表保持轻量、详情才组合）。
- 扩展 `IProduct` 接口：新增 `Exists(ctx, id int64) (bool, error)`（供 SKU 校验商品存在性，与 `ICategory.Exists` 对称；唯一实现者 `sProduct` 补实现）。
- 新增 `ISku` 接口：含 `ListByProduct(ctx, productID int64, onlyEnabled bool) ([]*skuv1.Sku, error)`（供商品详情组合 SKU）。
- 商品详情组合：`product` logic 的 `Detail`/`AdminDetail` 在加载商品后调用 `service.Sku().ListByProduct`，前台 `onlyEnabled=true`、后台 `onlyEnabled=false`，SKU 按 `id ASC` 排序。列表路径（`queryList`）不加载 SKU。
- SKU 校验：创建时 `product_id` 经 `service.Product().Exists` 校验，不存在返回 `4001 CodeProductNotFound`（复用商品域，AC「商品不存在错误」）。`name` 撞名（同商品）返回 409 `5004`。
- `internal/codes` 新增 5001-5004；`internal/boot/seed.go` 追加 3 个 SKU 权限；`internal/cmd/routes_admin.go` 挂载 SKU 写路由。

## Business Invariants

- INV-001（归属正确）：SKU 创建时 `product_id` 必须指向存在的商品，持久化后指向正确商品；商品不存在被拒且无写入。
- INV-002（价格整数分）：`price` 全程整数分存储与出参；负数/非整数/超上限在创建与更新均被拒且不改变原值。
- INV-003（一对多与归属隔离）：同一商品可多个 SKU；查询某商品 SKU 只返回该商品自己的；商品间不串号；删除 SKU 不影响其 SPU 及同商品其他 SKU。
- INV-004（状态合法）：SKU `status` 仅 `enabled`/`disabled`；非法值在创建/更新被拒且无写入；创建默认 `enabled`。
- INV-005（RBAC）：未认证 401、已认证无对应 `sku:*` 权限 403 且无写入、持有权限（含超管）成功。
- INV-006（稳定引用键）：`skus.id` 为全局唯一自增主键，删除后不重用。
- INV-007（前台可见性）：前台详情仅对 `on_shelf` 商品返回 `skus`，且只含 `enabled` SKU；后台详情返回全部 SKU。
- INV-008（同商品 name 唯一）：同一商品下 `name` 唯一，由 `uk_product_name` 复合唯一约束兜底，撞名 409 `5004`。

## Failure and Consistency Semantics

- 事实来源：MySQL 单一系统。`skus`（SKU 主数据 + 状态）、`products`（商品存在性与 `on_shelf` 可见性判定）。`skus.product_id` 引用完整性由 FK `ON DELETE RESTRICT` 保证。无 Redis、无 MQ、无异步、无跨系统事务；库存不属本域，无库存一致性语义。
- 创建/更新（同步、单表）：创建先校验（`product_id` 存在、`name`/`price`/`status` 合法）后写入 `skus`；更新先 `SELECT` 判定存在（不存在→404 `5001`），再按提交字段 `UPDATE`。非法值在写入前拒绝且无写入，不改变原值。
- 删除（同步）：物理 `DELETE`，先判定存在（不存在/已删除→404 `5001`），核对 `RowsAffected`。成功返回空 data。删除不级联、不影响 `products` 与其他 SKU。
- 唯一性兜底：`name` 撞名时 DB 1062 → 映射 409 `5004`（不泄漏 500）；`uk_product_name` 兜底并发「先查再写」窗口。
- 失败语义：认证失败 401、授权失败 403（无写入）；参数/业务校验失败 400/404/409 均无写入；DB 技术错误统一 `CodeInternalError` 500，不泄漏底层细节。
- 并发目标落到可验证结果：同商品并发创建同名 SKU 最终至多一行（唯一约束兜底）；`skus.id` 自增不重用。

## Error Semantics

| code | 语义 | HTTP |
| --- | --- | --- |
| 5001 | SKU_NOT_FOUND（SKU 不存在） | 404 |
| 5002 | SKU_INVALID_PRICE（价格负/非整数/超上限） | 400 |
| 5003 | SKU_INVALID_STATUS（未知 status 值） | 400 |
| 5004 | SKU_NAME_EXISTS（同商品下 name 已存在） | 409 |

复用：`4001`（商品不存在，创建 SKU 时 `product_id` 无效）、`1001`（参数错误兜底）、`1002`（401）、`1003`（403）。

## Allowed / Forbidden Changes

允许：
- 新增迁移 `internal/migrations/sql/20261001000003_skus.up.sql`（`skus` 表，DDL 不用 `IF NOT EXISTS`，不回退到 `boot.go`）；`seedPermissionList` 追加 3 个 SKU 权限。
- 新增 `api/sku/v1`、`internal/controller/sku`、`internal/service`（ISku）、`internal/logic/sku`、错误码 5001-5004、路由挂载。
- `api/product/v1` 的 `DetailRes`/`AdminDetailRes` 增加 `skus` 字段（最小叠加，不改既有商品字段语义）；`IProduct` 增加 `Exists`。
- 新增对应测试（见 Verification Requirements）。

禁止：
- 不改动已合入的 baseline（`20261001000001`）与 products（`20261001000002`）迁移、迁移机制本身；仅经既有机制新增 `skus` 迁移。
- **`skus` 不得包含 `stock` 字段，不得实现任何库存查询/扣减/防负库存/库存流水/并发扣减逻辑**（留给 4.3）。
- 不改动 `products.price` 字段语义、前台价格展示；不引入 `min_price`/`max_price`/促销价派生。
- 不新增 SKU 独立读接口/列表接口；不新增 `sku:list` 读权限；不新增 `sku_code`；不预留软删字段。
- 不改动 `/health`、前台注册/登录/me/logout、分类公开/写接口、商品既有公开/写接口的既有行为（商品详情新增 `skus` 字段是唯一对外变化）。
- 不引入订单/规格笛卡尔组合/SPU 删除等 Out of Scope 能力。
- 不 fail-open 放行未认证/未授权请求；不在 SQL 中拼接前端输入。

## Verification Requirements

- INV-001 → 创建合法 SKU（`product_id` 指向存在商品）断言 200/0 且查库 `skus` 行归属正确；`product_id` 不存在断言拒绝（4001）且无写入。需 MySQL。
- INV-002 → 非法 `price`（负/非整数/超上限）在创建与更新断言 5002 且无写入、原值不变；合法值成功。需 MySQL。
- INV-003 → 构造多商品多 SKU 场景，断言查询/详情不串号、删除 SKU 不影响 SPU 及同商品其他 SKU。需 MySQL。
- INV-004 → 非法 `status` 值创建/更新断言 5003 且无写入；默认 `enabled` 断言。需 MySQL。
- INV-005 → 无 token 401、有 token 无权限 403（查库无写入）、有权限成功、超管放行。需 MySQL + Redis。
- INV-006 → 迁移 DDL 核对 `skus.id` 主键 `BIGINT UNSIGNED` 自增；查库确认删除后自增不回退。需 MySQL。
- INV-007 → 创建商品（draft/on_shelf/off_shelf）+ 多 SKU（enabled/disabled），访问前台/后台详情断言 SKU 列表归属与可见性规则。需 MySQL。
- INV-008 → 同商品创建同名 SKU 断言 409/5004 且无新行。需 MySQL。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；MySQL/Redis 集成验证需说明容器就绪（`docker compose up -d`）。

## Open Risks

- 物理删除 + 自增不重用：在订单模块引入 FK/应用层校验前，历史 SKU 被删后其 `id` 不被重用，但已存在订单若引用该 SKU 将失去关联。此为 deferred requirement 明确接受（延后到订单模块补齐），当前不处理。
- 库存展示缺口：SKU V1 详情响应不含库存，前台无法展示库存；需等待 4.3 库存域提供基于 `sku_id` 的查询。此为 Owner 已确认的库存解耦取舍。
- `skus.product_id` FK `ON DELETE RESTRICT`：当前无商品删除接口，约束不触发；未来引入商品删除时需先处理 SKU 引用（超出本任务）。
- 前台详情仅返回 `enabled` SKU：若未来需要前台展示「已停用但历史可售」的 SKU 需回改规则（当前不处理）。

## Owner Decision Record

Owner 于 2026-10-01 确认：

1. **Q1 价格**：SPU `price` 与 SKU `price` 独立；`products.price` 继续作为 SPU 基础展示价，`skus.price` 表示 SKU 售价；V1 不做 min/max price 派生与自动同步。
2. **Q2 状态**：SKU `status` 二态 `enabled`/`disabled`，默认 enabled，不复用 SPU 三态。
3. **Q4 唯一性**：同一商品下 SKU `name` 唯一，`(product_id, name)` 复合唯一约束，并提供稳定重复错误码。
4. **Q3/Q5/Q6 方向**：详情内嵌、扁平写路由、5000-5999 错误码段、`sku:create/update/delete` 权限、物理删除，原则上确认。
5. **库存边界修订**：`skus` 不承载 `stock`，SKU V1 只负责 `id`/`product_id`/`name`/`price`/`status`/`created_at`/`updated_at`；库存数据与操作留给 4.3 独立 inventory 模型/表基于 `sku_id` 建立。

适用范围：`sku-v1` 本任务。

待办（已完成）：
- [x] Task Builder 同步 `sku-v1/task.md`（2026-10-01）：移除 `skus` 的 `stock` 字段、SKU 承载「库存」的描述、AC-003（库存校验整条）、AC-001/005/009 中的 `stock` 字段、Assumption #2（stock 字段），并将「库存查询/扣减/防负库存/流水/并发控制留给 4.3 inventory 基于 `sku_id` 建立」写入 Out of Scope；Analyst Questions #6 删除；Initial Route 改为 `READY_FOR_CODER`。Contract 与 Task 一致，转 `APPROVED`。
