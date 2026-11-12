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
| `sku_code` | VARCHAR(64) NULL | SKU 业务唯一编码（B1-04-02）。在线写入一律由服务端生成；历史行回填为 `SKU<10 位左补零 id>`；NULL 仅用于兼容离线/迁移写入。唯一约束 `uk_sku_code` |
| `product_id` | BIGINT UNSIGNED | 非空，FK → `products.id` `ON DELETE RESTRICT` |
| `name` | VARCHAR(128) | 非空，trim 后校验（保留为展示名，历史数据不改写） |
| `image_url` | VARCHAR(512) | 非空默认 `''`，SKU 规格图；空表示回退 `products.main_image` |
| `price` | INT UNSIGNED | 整数分，非空，业务上限 `0..99,999,999`；**下单定价唯一来源** |
| `original_price` | INT UNSIGNED | 非空默认 0，原价/划线价（整数分） |
| `status` | TINYINT | 非空默认 1（`1=enabled`、`0=disabled`） |
| `spec_signature` | CHAR(32) NULL | 规格组合指纹（等价 `spec_key`，32 位小写 hex）；NULL 表示无规格/历史 SKU |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`idx_product_id`；唯一约束：`uk_sku_code (sku_code)`（全局业务编码唯一）、`uk_product_name (product_id, name)`（同一商品下 name 唯一）、`uk_product_signature (product_id, spec_signature)`（同一商品下同一规格组合唯一，多个 NULL 允许）；FK：`fk_skus_product`（`ON DELETE RESTRICT`）。

**不含 `stock`**：库存不进入 SKU 元数据。

### 2.2 状态映射

| DB `status` | API 出参 |
| --- | --- |
| `1` | `"enabled"` |
| `0` | `"disabled"` |

SKU 状态为二态 `enabled`/`disabled`（不复用 SPU 三态），创建默认 `enabled`，create/update 直接设置与校验。

### 2.3 价格语义

`skus.price`（SKU 售价）与 `products.price`（SPU 基础展示价）**独立**，无派生、无 `min_price`/`max_price` 自动同步。

### 2.4 商品规格模型（`product_specs` / `product_spec_values` / `sku_spec_values`）

规格为**商品级**定义（每个商品自定义规格组，不建全局规格字典），由 migration `20261110000001_product_spec` 建立：

| 表 | 字段 | 约束/说明 |
| --- | --- | --- |
| `product_specs` | `id`、`product_id`、`name` VARCHAR(32)、`sort` INT、`created_at`/`updated_at` | `uk_product_spec_name (product_id, name)`；FK → `products.id` `ON DELETE CASCADE` |
| `product_spec_values` | `id`、`spec_id`、`value` VARCHAR(32)、`sort` INT、`created_at`/`updated_at` | `uk_spec_value (spec_id, value)`；`uk_id_spec (id, spec_id)` 供复合外键引用；FK → `product_specs.id` `ON DELETE CASCADE` |
| `sku_spec_values` | `sku_id`、`spec_id`、`spec_value_id` | PK `(sku_id, spec_id)`；`idx_spec_value (spec_value_id, spec_id)`；FK `sku_id` → `skus.id` `ON DELETE CASCADE`；复合 FK `(spec_value_id, spec_id)` → `product_spec_values(id, spec_id)` `ON DELETE RESTRICT` |

**规格组合指纹（`skus.spec_signature`，即任务口径的 `spec_key`）**：把该 SKU 的规格值按 `spec_id` 升序排列，每项写成 `"<spec_id>:<spec_value_id>"`，
以 `,` 连接后计算 MD5，取 32 位小写十六进制。示例：`1:11,2:21` → `MD5("1:11,2:21")`。
指纹只用于「同一商品内组合去重」，作用域由 `product_id` 与 `uk_product_signature` 限定，不用于安全场景；
实现时必须使用同一算法（`internal/logic/sku.specKey`），否则同一组合会生成不同指纹而绕过唯一约束。
不同商品出现相同指纹是合法的（唯一索引包含 `product_id`），不存在跨商品冲突。

**组合定位唯一 SKU**：请求携带 `product_id` + 规格值集合 → 计算指纹 → 按 `(product_id, spec_signature)` 命中
`skus` 唯一索引；`sku_spec_values` 用于反向展示（SKU → 规格值）与「按规格值筛选」。

**无规格/历史 SKU**：不写 `sku_spec_values`，`spec_signature` 保持 NULL，`image_url` 为空。唯一索引允许多个 NULL，
因此存量 SKU 可继续读写，不需要回填。

**下单规格快照**：`order_items.spec_text`（B1-03-02 新增）用于保存下单时的规格文本快照，订单读取不依赖
`sku_spec_values` 是否仍存在；该列默认空串，历史订单不回溯。B1-04-02 已接入下单链路：按 SKU 的规格值
（按规格组 `sort`、`id` 升序）以 `,` 连接写入（如 `红色,S`），无规格 SKU 写空串。

### 2.5 SKU 组合生成规则（B1-04-02）

管理端接口 `POST /admin/products/:id/skus/generate`（`sku:create`）：

1. 读取商品全部规格组与规格值（规格组按 `sort,id`，规格值按 `sort,id` 排序）；无规格组或任一组无值 → `5015`。
2. 按规格组顺序生成笛卡尔积（2 组 2×3 → 6 个组合；3 组 2×3×2 → 12 个）。
3. 每个组合计算 `spec_key`（算法见 §2.4），并按 `(product_id, spec_key)` 定位 SKU：
   - **已存在**：保留原 `id`、`sku_code`、`name`、价格、状态与图片，不覆盖；计入 `kept`。
   - **不存在**：新建 SKU，`name` = 规格值文本按顺序 `,` 连接，`price` 取请求值（缺省 `products.price`），
     `original_price` 缺省等于售价，`status` 默认 `enabled`，`image_url` 取请求值，
     `sku_code = SKU-P<product_id>-<spec_key 前 12 位大写>`（同一组合始终派生同一编码）；计入 `created`。
4. 同一事务写 `skus` 与 `sku_spec_values`；并发重复由 `uk_product_signature` / `uk_sku_code` 兜底，
   命中重复键视为「已存在」并计数为 `kept`（幂等），不产生重复 SKU。
5. **不删除任何 SKU**：已被购物车、订单、库存引用的 SKU 不会因重新生成而消失，编码也不会变更。
6. **不建立库存事实**：生成不写 `inventories`；新 SKU 无库存记录即库存 0，后续由 Inventory 模块
   `POST /admin/inventories/:sku_id/increase` 初始化（库存仍以 `inventories` 为唯一权威来源）。

### 2.6 商品详情 `van-sku` 契约（B1-04-02）

`GET /products/:id` 在保留原有 `id/name/.../skus` 字段的基础上，新增原 APP 端 `van-sku` 直接消费的结构：

```json
{
  "attr": [
    {"attrName": "颜色", "attrValueArr": ["红色", "蓝色"]},
    {"attrName": "尺码", "attrValueArr": ["S", "M", "L"]}
  ],
  "productValue": {
    "红色,S": {
      "unique": "SKU-P12-1A2B3C4D5E6F",
      "sku": "红色,S",
      "sku_id": 101,
      "sku_name": "红色,S",
      "price": 1999,
      "original_price": 2599,
      "stock": 7,
      "image": ""
    }
  }
}
```

- `attr[]`：按规格组 `sort,id` 升序；`attrValueArr` 为该组全部可选值文本（`sort,id` 升序）。
- `productValue{}`：键 = 按 `attr[]` 顺序把该 SKU 的规格值文本以 `,` 连接；值 = SKU 编码（`unique`）、
  展示文本（`sku`）、id/名称、售价、原价、可售库存（来自 `inventories`，无记录为 0）、规格图。
- 仅返回 `on_shelf` 商品与 `enabled` SKU（与既有可见性规则一致）；下架商品返回 `4001`（404）。
- 规格取值不完整的历史 SKU 不进入 `productValue`（避免伪造组合键），但仍保留在 `skus[]` 中，可按 `sku_id` 下单。
- 无规格商品：`attr` 为 `[]`、`productValue` 为 `{}`（不是 `null`），`skus[]` 正常返回。
- 不返回成本价等内部敏感字段（`skus` 表也不存在该列）。

后台 `GET /admin/products/:id` 额外返回 `specs[]`（规格组/规格值含 `id`，供维护），SKU 列表含全部状态。

### 2.7 下单服务端定价流程（B1-04-02）

1. `POST /orders`（`source=direct`）优先使用 `sku_code` 定位 SKU，兼容 `sku_id`；两者同时提交必须指向同一 SKU，
   否则 `9003`。
2. 服务端按 `sku_code` 读库（`ISku.GetByCode`），校验 SKU 存在（否则 `5001`）、`enabled`、所属商品 `on_shelf`
   （否则 `9003`）。
3. 成交价只取 `skus.price`；`price` 请求字段仅用于防伪校验：提交值与服务端售价不一致 → `9007`（409），
   相等也不参与金额计算。`source=cart` 提交 `price` 一律 `9007`。
4. 订单项快照写入 `sku_code`、`spec_text`、`price`、`original_price`、`sku_name`、`product_name`、主图，
   订单读取不依赖 SKU/规格是否仍存在。
5. 库存扣减复用现有 `IInventory.DeductInTx`，与写订单/订单项/清购物车同一事务；失败整体回滚。
6. `source=cart` 的行价格同样在 `resolveLine` 重读 `skus.price`，不信任 `cart_items.price_snapshot`。

## 3. 状态机与生命周期

- 二态：`enabled` ↔ `disabled`，无迁移限制（非状态机迁移，是字段直接赋值）。
- 创建：默认 `enabled`；非法 `status` 值在创建/更新被拒（5003）且无写入。
- 删除：物理删除（不预留软删字段）；存在库存记录/流水时由 FK `ON DELETE RESTRICT` 兜底返回 5005（409）。稳定键由 `BIGINT UNSIGNED AUTO_INCREMENT` 保证不重用。
- 规格生命周期：`product_specs`/`product_spec_values` 随商品级联删除；被 SKU 引用的规格值不可删除（`ON DELETE RESTRICT`，`5013`），
  删除 SKU 时其 `sku_spec_values` 级联清理。SKU 的 `spec_signature` 与关联行由应用层在同一事务内维护。
- 组合生成：`POST /admin/products/:id/skus/generate` 只新增/保留，**从不删除 SKU**；被购物车/订单引用的 SKU 不受影响。

## 4. 业务不变量

- INV-001（归属正确）：SKU 创建时 `product_id` 必须指向存在商品（否则 4001）；持久化后归属正确。
- INV-002（价格整数分）：`price` 全程整数分，负数/非整数/超上限在创建与更新均被拒且不改变原值。
- INV-003（一对多与归属隔离）：同一商品可多个 SKU；查询只返回该商品自己的；商品间不串号；删除 SKU 不影响其 SPU 及同商品其他 SKU。
- INV-004（状态合法）：`status` 仅 `enabled`/`disabled`；非法值被拒且无写入；创建默认 `enabled`。
- INV-005（RBAC）：未认证 401、无对应 `sku:*` 权限 403 且无写入、持有权限（含超管）成功。
- INV-006（稳定引用键）：`skus.id` 全局唯一自增主键，删除后不重用。
- INV-007（前台可见性）：前台详情仅对 `on_shelf` 商品返回 `skus`，且只含 `enabled` SKU；后台详情返回全部 SKU。
- INV-008（同商品 name 唯一）：由 `uk_product_name` 复合唯一约束兜底，撞名 409 `5004`。
- INV-009（规格组合唯一）：同一商品下同一规格组合至多一个 SKU，由 `uk_product_signature (product_id, spec_signature)` 兜底；
  `spec_signature` 为 NULL 的行不受限制（历史/无规格 SKU 可多行）。
- INV-010（历史 SKU 兼容）：无规格关联的 SKU 与迁移前完全等价——不写 `sku_spec_values`，`image_url` 为空串、
  `spec_signature` 为 NULL；购物车、订单、库存、秒杀对 `skus.id` 的引用语义不变。
- INV-011（规格值归属一致）：一个 SKU 在同一规格组下只能取一个值（PK `(sku_id, spec_id)`），且该值必须属于声明的规格组
  （复合外键 `(spec_value_id, spec_id)`）；跨组错配的关联行在写入时被拒绝。

## 5. 一致性模型与失败语义

- 事实来源：MySQL 单一系统。`skus`（主数据 + 状态）、`products`（商品存在性与 `on_shelf` 可见性判定）。
- 创建/更新（同步、单表）：创建先校验（`product_id` 存在、`name`/`price`/`status` 合法）后写入；更新先 `SELECT` 判定存在（404），再按提交字段 UPDATE。非法值写入前拒绝且无写入。
- 删除（同步）：物理 DELETE，先判定存在（404），核对 `RowsAffected`；有库存记录/流水时 FK 1451 → 409 `5005`；删除不级联、不影响 `products` 与其他 SKU。
- 唯一性兜底：`name` 撞名 DB 1062 → 409 `5004`；`uk_product_name` 兜底并发「先查再写」窗口。
  `spec_key` 由 `uk_product_signature` 兜底、`sku_code` 由 `uk_sku_code` 兜底；组合生成命中这两个键时视为「已存在」。
- 失败语义：认证 401、授权 403、校验 400/404/409 均无写入；DB 技术错误统一 `1000` 500。
- 并发目标：同商品并发创建同名 SKU 最终至多一行；同商品并发生成同一组合最终至多一个 SKU（唯一约束 + 幂等语义）；
  `skus.id` 自增不重用。

## 6. 安全与权限边界

- 写接口扁平路由（`product_id` 在请求体，`product_id` 不可变）：`AdminAuth` + `RequirePermission`：
  - `POST /admin/skus` → `sku:create`
  - `PUT /admin/skus/:id` → `sku:update`
  - `DELETE /admin/skus/:id` → `sku:delete`
  - `POST /admin/products/:id/skus/generate` → `sku:create`
- 商品规格维护（B1-04-02）：
  - `POST /admin/product-specs`、`POST /admin/product-specs/:id/values` → `spec:create`
  - `PUT /admin/product-specs/:id`、`PUT /admin/product-spec-values/:id` → `spec:update`
  - `DELETE /admin/product-specs/:id`、`DELETE /admin/product-spec-values/:id` → `spec:delete`
- SKU 无独立读接口、无独立列表接口；不新增 `sku:list` 读权限。
- 商品详情组合：前台 `GET /products/:id`（公开，`on_shelf` 商品返回 `enabled` SKU 与 `attr`/`productValue`）、
  后台 `GET /admin/products/:id`（`AdminAuth`，返回全部 SKU 与 `specs`），见 `product.md`。

## 7. 错误码域

| code | 语义 | HTTP |
| --- | --- | --- |
| 5001 | SKU_NOT_FOUND | 404 |
| 5002 | SKU_INVALID_PRICE | 400 |
| 5003 | SKU_INVALID_STATUS | 400 |
| 5004 | SKU_NAME_EXISTS | 409 |
| 5005 | SKU_HAS_INVENTORY（存在库存记录，不能删除） | 409 |
| 5010 | SPEC_NOT_FOUND（规格组/规格值不存在） | 404 |
| 5011 | SPEC_NAME_EXISTS（同商品规格组重名） | 409 |
| 5012 | SPEC_VALUE_EXISTS（同规格组值重复） | 409 |
| 5013 | SPEC_VALUE_IN_USE（规格值被 SKU 引用，不能删除） | 409 |
| 5015 | SKU_SPEC_MISSING（商品未配置规格，无法生成组合） | 400 |
| 5016 | SKU_SPEC_DUPLICATE（相同规格组合已存在） | 409 |
| 5017 | SKU_CODE_EXISTS（SKU 编码已存在） | 409 |

复用：`4001`（商品不存在）、`1001`（400）、`1002`（401）、`1003`（403）。`5005` 由 `inventory.md` 引入（库存 FK 兜底）。
下单定价相关：`5001`（编码不存在）、`9003`（SKU 停用/商品下架）、`9007`（客户端单价与服务端不一致），见 `order.md`。

## 8. 跨模块关系

- `skus.product_id` → `products.id`（FK `ON DELETE RESTRICT`）：商品删除前需先处理 SKU（当前无商品删除接口）。
- 库存绑定 `sku_id`（非 `product_id`）：`inventories.sku_id` → `skus.id`，见 `inventory.md`。
- `skus` 不含 `stock`，库存解耦；详情组合 SPU+SKU 由 `product.md` 的 `DetailRes`/`AdminDetailRes` 承载。
- `product_specs.product_id` → `products.id`（`ON DELETE CASCADE`）；`product_spec_values.spec_id` → `product_specs.id`（`ON DELETE CASCADE`）；
  `sku_spec_values` 同时引用 `skus.id`（CASCADE）与 `product_spec_values(id, spec_id)`（RESTRICT）。
- 秒杀只读 `skus`/`products` 固定列，新增 `image_url`/`spec_signature` 不改变其读写结果；`inventories`、`inventory_logs`、
  `flash_sale_*` 未随本批次变更，见 `flash-sale.md`、`inventory.md`。
- 建表经 golang-migrate 迁移（`20261001000003_skus.up.sql`、`20261110000001_product_spec.up.sql`、
  `20261111000001_sku_code_pricing.up.sql`），见 `migration.md`。

## 9. Deferred / 已知留白

- **订单引用 deferred rule**：物理删除 + 自增不重用，`skus.id` 为稳定引用键；订单模块引入 FK/应用层校验前，历史 SKU 被删后其 `id` 不被重用，但已存在订单若引用该 SKU 将失去关联——延后订单模块补齐。
- 库存展示：前台详情 `productValue[].stock` 与后台 SKU 列表已按 `inventories` 返回可售库存（无记录 = 0），
  但 SKU 列表接口本身仍不单独暴露库存字段；库存操作仍只走 `inventory.md` 的接口。
- 前台详情仅返回 `enabled` SKU：若未来需要前台展示「已停用但历史可售」SKU 需回改规则（当前不处理）。
- 组合生成只处理「完整组合」：商品存在规格组但未配置任何规格值时返回 `5015`；不支持只生成部分组合，
  也不支持在同一接口内为不同组合设置不同价格（可生成后用 `PUT /admin/skus/:id` 逐条调整）。
- 历史数据不自动回填：B1-03-04 之前的存量 SKU 保持 `spec_signature=NULL` 且无规格关联；B1-03-04 已由
  `legacymigrate` 从遗留 `store_product_attr_result` 显式导入商品规格、`sku_spec_values` 与 `spec_signature`，
  无法唯一匹配组合的 SKU 不写规格关联，不猜测组合。无规格商品的 SKU 仍保持 NULL。
