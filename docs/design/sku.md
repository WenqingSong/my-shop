# SKU 设计（商品规格）

本文面向项目接手者，说明 SKU 的架构、数据模型、状态与边界。事实来源为 `sku-v1` 最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

SKU 在商品 SPU 之上建立「商品 1 — N SKU」关系，承载名称、价格（整数分）、状态。SKU 与库存解耦：**`skus` 不包含 `stock`**，库存数据与操作由 `inventory.md` 基于 `sku_id` 独立建立。SKU 仅提供 `skus.id` 作为库存域（及未来订单域）的稳定引用键。

事实来源为单一 MySQL（`skus`/`products`）。无 Redis、无 MQ、无异步。

## 2. 数据模型

### 2.1 `skus`

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增，全局唯一稳定引用键，删除后不重用 |
| `product_id` | BIGINT UNSIGNED | 非空，FK → `products.id` `ON DELETE RESTRICT` |
| `name` | VARCHAR(128) | 非空，trim 后校验 |
| `price` | INT UNSIGNED | 整数分，非空，业务上限 `0..99,999,999` |
| `status` | TINYINT | 非空默认 1（`1=enabled`、`0=disabled`） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`idx_product_id`；唯一约束：`uk_product_name (product_id, name)`（同一商品下 name 唯一）；FK：`fk_skus_product`（`ON DELETE RESTRICT`）。

**不含 `stock`**：库存不进入 SKU 元数据。

### 2.2 状态映射

| DB `status` | API 出参 |
| --- | --- |
| `1` | `"enabled"` |
| `0` | `"disabled"` |

SKU 状态为二态 `enabled`/`disabled`（不复用 SPU 三态），创建默认 `enabled`，create/update 直接设置与校验。

### 2.3 价格语义

`skus.price`（SKU 售价）与 `products.price`（SPU 基础展示价）**独立**，无派生、无 `min_price`/`max_price` 自动同步。

## 3. 状态机与生命周期

- 二态：`enabled` ↔ `disabled`，无迁移限制（非状态机迁移，是字段直接赋值）。
- 创建：默认 `enabled`；非法 `status` 值在创建/更新被拒（5003）且无写入。
- 删除：物理删除（不预留软删字段）；存在库存记录/流水时由 FK `ON DELETE RESTRICT` 兜底返回 5005（409）。稳定键由 `BIGINT UNSIGNED AUTO_INCREMENT` 保证不重用。

## 4. 业务不变量

- INV-001（归属正确）：SKU 创建时 `product_id` 必须指向存在商品（否则 4001）；持久化后归属正确。
- INV-002（价格整数分）：`price` 全程整数分，负数/非整数/超上限在创建与更新均被拒且不改变原值。
- INV-003（一对多与归属隔离）：同一商品可多个 SKU；查询只返回该商品自己的；商品间不串号；删除 SKU 不影响其 SPU 及同商品其他 SKU。
- INV-004（状态合法）：`status` 仅 `enabled`/`disabled`；非法值被拒且无写入；创建默认 `enabled`。
- INV-005（RBAC）：未认证 401、无对应 `sku:*` 权限 403 且无写入、持有权限（含超管）成功。
- INV-006（稳定引用键）：`skus.id` 全局唯一自增主键，删除后不重用。
- INV-007（前台可见性）：前台详情仅对 `on_shelf` 商品返回 `skus`，且只含 `enabled` SKU；后台详情返回全部 SKU。
- INV-008（同商品 name 唯一）：由 `uk_product_name` 复合唯一约束兜底，撞名 409 `5004`。

## 5. 一致性模型与失败语义

- 事实来源：MySQL 单一系统。`skus`（主数据 + 状态）、`products`（商品存在性与 `on_shelf` 可见性判定）。
- 创建/更新（同步、单表）：创建先校验（`product_id` 存在、`name`/`price`/`status` 合法）后写入；更新先 `SELECT` 判定存在（404），再按提交字段 UPDATE。非法值写入前拒绝且无写入。
- 删除（同步）：物理 DELETE，先判定存在（404），核对 `RowsAffected`；有库存记录/流水时 FK 1451 → 409 `5005`；删除不级联、不影响 `products` 与其他 SKU。
- 唯一性兜底：`name` 撞名 DB 1062 → 409 `5004`；`uk_product_name` 兜底并发「先查再写」窗口。
- 失败语义：认证 401、授权 403、校验 400/404/409 均无写入；DB 技术错误统一 `1000` 500。
- 并发目标：同商品并发创建同名 SKU 最终至多一行；`skus.id` 自增不重用。

## 6. 安全与权限边界

- 写接口扁平路由（`product_id` 在请求体，`product_id` 不可变）：`AdminAuth` + `RequirePermission`：
  - `POST /admin/skus` → `sku:create`
  - `PUT /admin/skus/:id` → `sku:update`
  - `DELETE /admin/skus/:id` → `sku:delete`
- SKU 无独立读接口、无独立列表接口；不新增 `sku:list` 读权限。
- 商品详情组合：前台 `GET /products/:id`（公开，`on_shelf` 商品返回 `enabled` SKU）、后台 `GET /admin/products/:id`（`AdminAuth`，返回全部 SKU），见 `product.md`。

## 7. 错误码域

| code | 语义 | HTTP |
| --- | --- | --- |
| 5001 | SKU_NOT_FOUND | 404 |
| 5002 | SKU_INVALID_PRICE | 400 |
| 5003 | SKU_INVALID_STATUS | 400 |
| 5004 | SKU_NAME_EXISTS | 409 |
| 5005 | SKU_HAS_INVENTORY（存在库存记录，不能删除） | 409 |

复用：`4001`（商品不存在）、`1001`（400）、`1002`（401）、`1003`（403）。`5005` 由 `inventory.md` 引入（库存 FK 兜底）。

## 8. 跨模块关系

- `skus.product_id` → `products.id`（FK `ON DELETE RESTRICT`）：商品删除前需先处理 SKU（当前无商品删除接口）。
- 库存绑定 `sku_id`（非 `product_id`）：`inventories.sku_id` → `skus.id`，见 `inventory.md`。
- `skus` 不含 `stock`，库存解耦；详情组合 SPU+SKU 由 `product.md` 的 `DetailRes`/`AdminDetailRes` 承载。
- 建表经 golang-migrate 迁移（`20261001000003_skus.up.sql`），见 `migration.md`。

## 9. Deferred / 已知留白

- **订单引用 deferred rule**：物理删除 + 自增不重用，`skus.id` 为稳定引用键；订单模块引入 FK/应用层校验前，历史 SKU 被删后其 `id` 不被重用，但已存在订单若引用该 SKU 将失去关联——延后订单模块补齐。
- 库存展示缺口：SKU V1 详情响应不含库存，前台库存展示需等待 `inventory.md` 提供基于 `sku_id` 的查询。
- 前台详情仅返回 `enabled` SKU：若未来需要前台展示「已停用但历史可售」SKU 需回改规则（当前不处理）。
