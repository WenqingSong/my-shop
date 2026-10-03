# 商品 SPU 设计（Product）

本文面向项目接手者，说明商品 SPU 的架构、数据模型、状态机与安全边界。事实来源为 `product-spu-v1` 最终 APPROVED Contract（含 CONTRACT_REVISION）与最终实现。

## 1. 职责与边界

商品 SPU 回答「这是什么商品」，承载商品主数据（名称、品牌、分类、基础展示价、主图、详情、状态、图册）。SPU 只承载基础展示价，**不负责 SKU、库存、销量、订单**；SKU 由 `sku.md` 独立承载，库存由 `inventory.md` 独立承载。

事实来源为单一 MySQL（`products`/`product_images`/`categories`）。无 Redis、无 MQ、无异步。

## 2. 数据模型

### 2.1 `products`（商品主数据）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `name` | VARCHAR(128) | 非空，trim 后校验 |
| `brand` | VARCHAR(64) | 非空默认 `''`，可选 |
| `category_id` | BIGINT UNSIGNED | 非空，FK → `categories.id` `ON DELETE RESTRICT` |
| `price` | INT UNSIGNED | 整数分，非空，业务上限 `0..99,999,999`（= ¥999,999.99） |
| `main_image` | VARCHAR(512) | 非空默认 `''`，可选 |
| `detail` | TEXT | 可空 |
| `status` | TINYINT | 非空默认 0（`0=draft`、`1=on_shelf`、`2=off_shelf`） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`idx_category_id`、`idx_status`；FK：`fk_products_category`（`ON DELETE RESTRICT`，并发删除分类兜底）。

### 2.2 `product_images`（商品图册）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `product_id` | BIGINT UNSIGNED | 非空，无 FK（引用有效性由应用层 + 事务保证） |
| `url` | VARCHAR(512) | 非空 |
| `sort` | INT | 非空默认 0，排序下标 |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`idx_product_id`。

### 2.3 状态映射

| DB `status` | API 出参 |
| --- | --- |
| `0` | `"draft"` |
| `1` | `"on_shelf"` |
| `2` | `"off_shelf"` |

DB 存 TINYINT，API 出参用字符串枚举（具名三态，非布尔开关）。

## 3. 状态机与生命周期

合法状态迁移：

```text
draft ──> on_shelf ──> off_shelf
           ↑              │
           └──────────────┘
```

- 创建强制 `draft`，创建接口不能直接产生 `on_shelf`。
- 上架：`draft→on_shelf`、`off_shelf→on_shelf`；下架：`on_shelf→off_shelf`。
- 普通 update **不改变 status**：合法 `status` 值被忽略，非法值返回 4006。
- 迁移用「条件 UPDATE + `RowsAffected`」：上架 `UPDATE ... WHERE status IN (0,2)`，下架 `UPDATE ... WHERE status=1`；先 `SELECT` 判定存在（不存在→404），再条件 UPDATE，`RowsAffected=0` → 409（非法迁移与并发重复统一归为「当前状态不允许该迁移」）。
- 无删除接口，无删除竞态。

## 4. 业务不变量

- INV-001（状态机合法迁移）：仅允许 `draft→on_shelf`、`on_shelf→off_shelf`、`off_shelf→on_shelf`；其余被拒（409）且状态不变。
- INV-002（并发迁移最多一次成功）：同一商品并发重复上/下架，条件 UPDATE 保证最多一次 `RowsAffected=1`，最终状态正确。
- INV-003（前后台可见性隔离）：前台列表/详情仅返回 `status=on_shelf`；`draft`/`off_shelf` 前台不可见（404）。
- INV-004（创建强制 draft，普通 update 不改状态）。
- INV-005（SPU 仅绑定存在且启用的叶子分类）：创建、修改 `category_id`、上架时，`category_id` 必须同时满足「存在 + 叶子 + enabled」（4003/4004/4007），任一不满足被拒且无写入。
- INV-006（分类删除保护）：分类下存在商品时禁止删除，返回 409 `3005`；并发窗口由 FK `ON DELETE RESTRICT` 兜底。
- INV-007（商品与图片原子一致）：创建/更新时 `products` 与 `product_images` 在同一事务内写入，失败整体回滚。
- INV-008（价格整数分）：`price` 全程整数分，负数/超上限拒绝且无写入。
- INV-009（keyword 按普通文本搜索）：`keyword` 对 LIKE 的 `%`/`_`/`\` 转义后匹配，不暴露通配符语义。

## 5. 一致性模型与失败语义

- 事实来源：MySQL `products`（主数据 + 状态）、`product_images`（从属）、`categories`（叶子/存在性/启用判定）。
- 创建/更新（同步、单事务）：写入 `products`（+ `product_images` 全量替换——更新时 `images` 提供则删除旧行重插，未提供则保留）；任一步失败整体回滚。
- 状态迁移（同步、原子）：见状态机；上架前重新校验分类最新有效性（含「已禁用/已变非叶子」的再次上架拒绝）。
- 前台可见性（每请求）：列表/详情 SQL 强制 `status=on_shelf`；不存在或非 `on_shelf` 前台返回 404。
- 分类删除保护：`存在检查 → 有子分类(3003) → 有商品(3005) → DELETE`，核对 `RowsAffected`；并发窗口下 DELETE 命中 FK 1451 → 409 `3005`（应用层检查保留、FK 兜底最终一致性）。
- 失败语义：认证 401、授权 403、参数/业务校验 400/404/409，均无写入；DB 技术错误统一 `1000` 500，不泄漏底层细节。

## 6. 安全与权限边界

- 前台：`GET /products`、`GET /products/:id` 公开（仅 `on_shelf`）。
- 后台查询：`GET /admin/products`、`GET /admin/products/:id` 仅 `AdminAuth`（无读权限，查看全部状态）。
- 后台写操作：`AdminAuth` + `RequirePermission`：
  - `POST /admin/products` → `product:create`
  - `PUT /admin/products/:id` → `product:update`
  - `POST /admin/products/:id/on-shelf` → `product:on_shelf`
  - `POST /admin/products/:id/off-shelf` → `product:off_shelf`
- 授权判定见 `rbac.md`；不信任请求自带身份，`Principal` 是唯一身份来源。
- 列表参数：`page`（默认 1）、`size`（默认 20，max 100）、`category_id`（精确匹配）、`keyword`（转义后 LIKE）、`sort`（白名单 `id`/`price`/`created_at`/`updated_at`，默认 `created_at`）、`order`（`asc`/`desc`，默认 `desc`）；排序字段走白名单映射、关键词参数化绑定，禁止拼接前端字段进 SQL。

## 7. 错误码域

| code | 语义 | HTTP |
| --- | --- | --- |
| 4001 | PRODUCT_NOT_FOUND | 404 |
| 4002 | PRODUCT_INVALID_PRICE | 400 |
| 4003 | PRODUCT_INVALID_CATEGORY | 400 |
| 4004 | PRODUCT_CATEGORY_NOT_LEAF | 400 |
| 4005 | PRODUCT_INVALID_STATUS_TRANSITION | 409 |
| 4006 | PRODUCT_INVALID_STATUS | 400 |
| 4007 | PRODUCT_CATEGORY_DISABLED | 400 |

复用：`1001`（400）、`1002`（401）、`1003`（403）、`1004`（404）、`3005`（分类下有商品，409）。

## 8. 跨模块关系

- `products.category_id` → `categories.id`（FK `ON DELETE RESTRICT`）：分类删除保护，见 `category.md`。
- 商品详情组合 SPU + SKU：`DetailRes`/`AdminDetailRes` 内嵌 `skus`，见 `sku.md`。
- 商品建表经 golang-migrate 迁移（`20261001000002_products.up.sql`），见 `migration.md`。
- `products.price`（SPU 基础展示价）与 `skus.price`（SKU 独立售价）独立、无派生、无同步，见 `sku.md`。

## 9. Deferred / 已知留白

- 已绑定商品后分类被禁用：已有商品（含已上架）不受影响，前台仍按商品自身 `status` 过滤；再次上架会因分类禁用被拒。该「禁用后处理已有商品」语义不在 V1 扩展。
- 叶子分类被后续添加子分类后变为非叶子：已绑定商品不受影响，但再次上架会被拒（非叶子）。
- 无商品删除接口（当前不处理）。
