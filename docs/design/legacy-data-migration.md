# 历史数据迁移设计（Legacy Data Migration）

本文面向项目接手者，说明历史电商数据（`shopping.sql`）迁移工具与目标库 `legacy_id_map` 映射表的架构、映射规则、状态映射与失败语义。事实来源为 `legacy-data-migration` 任务的 APPROVED Contract 与最终实现。

## 1. 职责与边界

独立 Data Migration CLI（复用 `internal/cmd` 子命令）把历史 CRMEB 导出库中的分类、商品、SKU、库存及必要图片，适配导入 SurgeCart 目标测试库。**Schema Migration 与 Data Migration 严格区分**：本迁移只新增 `legacy_id_map` 一张映射表（migration `20261001000023`），不改动既有目标表结构与历史 migration 文件，不新增 HTTP 接口，不迁移订单/用户/优惠券等 Out of Scope 业务。

事实来源：staging 库（原始 `shopping.sql` 全量导入的隔离 schema）为**源**；目标库 `my_shop` 为**结果**；`legacy_id_map` 为目标库内的旧 ID→新 ID 映射事实。

## 2. 数据模型

### 2.1 `legacy_id_map`（目标库，migration `20261001000023`）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `source` | VARCHAR | 来源标识（如 `shopping.sql`） |
| `entity_type` | VARCHAR | `category`/`product`/`sku` |
| `old_id` | BIGINT UNSIGNED | 历史旧表主键（`store_category.id`/`store_product.id`/`store_product_attr_value.id`） |
| `new_id` | BIGINT UNSIGNED | 目标新主键（`categories.id`/`products.id`/`skus.id`） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP`（审计） |

唯一约束：`uk_source_entity_old (source, entity_type, old_id)`（至少 `(entity_type, old_id)`）保证映射幂等。**去重以本表为准，不依赖商品名称等自然键**。

`product_images` 由商品派生（`url` 即自然键），`inventories` 与 `skus` 一对一（以新 `sku_id` 为键），二者均不占用 `entity_type`。

## 3. 迁移映射规则

### 3.1 分类 `store_category` → `categories`

- `pid → parent_id`、`cate_name → name`、`sort → sort`。
- `is_del=1 → status=0`（禁用）、`is_del=0 → status=1`（启用）。
- `store_category.is_show`（是否推荐）目标无对应字段，不映射；`pic` 不映射。

### 3.2 商品 `store_product` → `products` + `product_images`

- `store_name → name`、`cate_id（字符串，转为 int64）→ category_id`、`description → detail`。
- 金额：`price`（decimal(8,2) 元）→ `price`（INT UNSIGNED 分），字符串解析 ×100 定点转换，禁浮点。
- 图片：`image → main_image`；`slider_image`（逗号分隔）拆分 → `product_images.url`（`sort` 按下标）。
- 状态：`is_del=1 → status=2`（off_shelf）；`is_del=0` 时 `is_show=1 → status=1`（on_shelf）、`is_show=0 → status=2`。
- 分类关联：`cate_id` 引用不存在或未迁移分类 → 该商品跳过并记录原因（FK `category_id` 必须有效）。

### 3.3 SKU `store_product_attr_value` → `skus`（权威来源）

- 权威来源为 `store_product_attr_value`（`product_id + sku + stock + price + image`）；`store_product_attr` 仅用于维度/组合名校验与单规格判定；`store_product_attr_result.result` 因数据污染**弃用**。
- `sku`（组合值字符串，如 `4+64G,金色`）→ `skus.name`；`price` → `skus.price`（转分）；`image` → SKU 图（空则回退商品主图）。
- 多规格商品：每行 `attr_value` 生成一个 SKU；单规格商品：生成 1 个 `name='默认'` SKU。
- **SKU 状态**：`skus.status = enabled(1)` 当且仅当商品 `status = on_shelf(1)`，否则 `disabled(0)`（见 §4 INV-007）。
- 多规格缺 `attr_value` 明细 → 跳过并记录原因（不补造 SKU）。

### 3.4 库存 → `inventories`

- 有 `attr_value` 行：每个 SKU 一条 `inventories`，`quantity = 该行 stock`（SKU 级）。
- 无 `attr_value` 行：单规格 SKU 一条 `inventories`，`quantity = 商品级 store_product.stock`。
- **绝不把商品级与 SKU 级相加**；`inventories` 与 `skus` 一对一（`uk(sku_id)` 兜底）。

## 4. 业务不变量

- INV-001（金额精确）：元→整数分全程定点，无浮点误差；`price ≤ 99,999,999`。
- INV-002（库存不重复计算）：每 `sku_id` 恰一条 `inventories`，同一商品库存单一来源（SKU 级或商品级，不叠加）。
- INV-003（映射幂等）：`legacy_id_map` 的 `(source, entity_type, old_id)` 唯一，重复导入不新增重复记录。
- INV-004（约束不绕过）：目标库导入不关闭外键/唯一键校验，FK 引用无效则跳过并记录。
- INV-005（存量不覆盖）：仅增量插入，不改写非迁移来源存量数据。
- INV-006（失败原子）：业务记录与 `legacy_id_map` 同事务提交，失败整体回滚。
- INV-007（SKU 可见性安全）：`skus.status` 严格跟随商品 `on_shelf`；软删除/下架商品 SKU 为 `disabled`，前台不可见、下单被拒（下单入口 `resolveLine` 同时要求 SKU enabled 且商品 on_shelf）。

## 5. 一致性模型与失败语义

- 事实来源：源 = staging 库；结果 + ID 映射 = 目标库（同库）。
- 事务边界：目标库按"分类 / 商品+图册+SKU+库存+`legacy_id_map`"分批单事务写入；业务记录与映射行同事务，任一步失败回滚该批。
- Dry Run：仅解析、映射、校验并输出将执行变更，不写目标库。
- 幂等：重复 `import` 先查 `legacy_id_map` 跳过已迁移 old_id。
- 无 Redis/MQ/异步：线性批处理，无乱序/消费者问题。

## 6. 安全边界

- 原始 `shopping.sql` 只导入隔离 staging 库；工具 DSN 仅指向 staging，目标库连接绝不执行 `SET FOREIGN_KEY_CHECKS=0`。
- 原始 SQL 归档至 `storage/private/legacy/shopping.sql`，`.gitignore`/`.dockerignore` 阻止进入 Git、Docker 镜像、CI 产物。
- 不连接历史生产库、不迁移历史账号与敏感配置。

## 7. 错误码与协议

迁移工具为独立 CLI、不新增 HTTP 接口，无新错误码域（`.agent/registry/error-codes.md` 不动）。工具输出以稳定退出码 + 逐条成功/跳过/失败报告表达结果。

## 8. 跨模块关系

- `legacy_id_map` 引用目标 `categories/products/skus` 的新 ID，无反向 FK（软引用，映射表是审计/幂等事实，不参与业务查询路径）。
- 迁移结果经既有 `GET /categories`、`GET /products`、`GET /products/:id`、`GET /admin/products/:id`、`GET /admin/inventories/:sku_id` 可查，复用现有 API，不新增接口。
- 目标表约束与状态机语义见 `category.md`、`product.md`、`sku.md`、`inventory.md`。
