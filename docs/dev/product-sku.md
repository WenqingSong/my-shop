# 商品与 SKU 模块知识（接手必读）

本文只记录「代码怎么组织、约束在哪里、改哪里、怎么验证」，长期设计语义见 `docs/design/product.md` 与 `docs/design/sku.md`，不在此重复。

## 1. 当前实现状态（先看这一节）

- 商品 SPU、SKU 的读写链路完整：后台增删改查、前台详情/列表、库存与订单按 `sku_id`/`sku_code` 引用。
- 商品规格已实现（B1-04-02）：`internal/logic/spec` 维护 `product_specs`/`product_spec_values`；
  `internal/logic/sku` 负责组合生成、`spec_signature`（`spec_key`）计算与 `sku_spec_values` 写入；
  `internal/logic/product` 在详情中返回 `attr[]`/`productValue{}`。
- 迁移：`20261110000001_product_spec`（规格三表 + `image_url`/`spec_signature`）、
  `20261111000001_sku_code_pricing`（`skus.sku_code`/`original_price`、`order_items.sku_code`/`original_price`）。
- 无规格 SKU 仍是合法状态：不写 `sku_spec_values`、`spec_signature`/`spec_key` 为空，按 `sku_id` 或 `sku_code` 正常下单。

## 2. 模块边界

| 模块 | 负责 | 不负责 |
| --- | --- | --- |
| `internal/logic/product` | `products` + `product_images`：SPU 主数据、图册、三态、浏览量；详情组装 `attr`/`productValue` | SKU 写入、库存写入、规格定义写入 |
| `internal/logic/spec` | `product_specs` + `product_spec_values`：规格组/规格值 CRUD 与归属校验 | SKU 写入、组合生成、库存 |
| `internal/logic/sku` | `skus`：名称、售价/原价、状态、SKU 图、`sku_code`、规格组合生成与 `spec_key` | 库存（`internal/logic/inventory`）、规格定义（`internal/logic/spec`） |
| `internal/logic/inventory` | `inventories` + `inventory_logs`，库存唯一事实来源 | SKU 元数据 |
| `internal/logic/cart` / `order` / `flashsale` | 以 `skus.id` 为稳定引用，读取 `skus.price`/`products.status` | 规格结构解析 |
| `regions` | 独立只读地区字典（结构已建，无业务代码、无数据） | 地址自由文本（`addresses` 未接地区表） |

改动约束：不要给 `skus` 加 `stock`；不要把规格组合拆成多个 SKU 的 `name` 字符串来「模拟」规格；
不要修改 `inventories`/`inventory_logs`/`flash_sale_*` 来适配规格。

## 3. 主要代码入口

| 关注点 | 位置 |
| --- | --- |
| SPU 创建/更新/上下架/详情 | `internal/logic/product/product.go` |
| SKU 创建/更新/删除/按商品列表 | `internal/logic/sku/sku.go` |
| SKU 组合生成 / `spec_key` / `sku_code` 派生 | `internal/logic/sku/sku.go`（`Generate`、`specKey`、`generatedSkuCode`） |
| 规格组/规格值 CRUD | `internal/logic/spec/spec.go`、`api/spec/v1/spec.go` |
| SKU 对外结构与状态枚举 | `api/sku/v1/sku.go` |
| 详情 `attr`/`productValue` 组装 | `internal/logic/product/product.go`（`buildSkuSelector`） |
| 商品对外结构与详情组合 | `api/product/v1/product.go` |
| 服务接口注册（跨 logic 调用） | `internal/service/*.go`（`service.Product()`/`service.Sku()`） |
| 前台/后台路由 | `internal/cmd/routes_frontend.go`、`internal/cmd/routes_admin.go` |
| 表结构 | `internal/migrations/sql/`（`20261001000002_products`、`20261001000003_skus`、`20261110000001_product_spec`） |
| 规格约束测试 | `internal/migrations/product_model_migration_test.go` |
| 现有商品/SKU 接口测试 | `internal/controller/product/product_test.go`、`internal/controller/sku/sku_test.go` |

## 4. 规格数据结构（只记关键点）

```text
product_specs(id, product_id, name, sort)                 -- 「颜色」「尺码」
        │ 1:N
product_spec_values(id, spec_id, value, sort)             -- 「黑色」「39」
        │ 1:N（复合键 id + spec_id 被引用）
sku_spec_values(sku_id, spec_id, spec_value_id)           -- 一个 SKU 的规格选择
        │
skus(id, product_id, name, image_url, price, status, spec_signature)
```

- `sku_spec_values` 主键 `(sku_id, spec_id)`：一个 SKU 在一个规格组下只能有一个值。
- 复合外键 `(spec_value_id, spec_id)` → `product_spec_values(id, spec_id)`：值必须属于声明的规格组；
  `ON DELETE RESTRICT`：被 SKU 使用的规格值不能删。
- `sku_spec_values.sku_id` → `skus.id ON DELETE CASCADE`：删 SKU 自动清理关联。
- `product_specs`/`product_spec_values` 随 `products` 级联删除；商品有 SKU 时仍被 `fk_skus_product` 拦住。

## 5. SKU 组合定位方式

写路径（已实现，`ISku.Generate`）：

1. 读取商品规格组/值（`ISpec.ListByProduct`，按 `sort,id` 排序），不足两组或任一组无值直接拒绝。
2. 服务端生成笛卡尔积；每个组合按 `spec_id` 升序拼 `"<spec_id>:<spec_value_id>"`，逗号连接取 MD5 32 位小写 hex → `spec_key`。
3. 同一事务内按 `(product_id, spec_key)` 判断已存在；已存在则保留原 SKU（不改编码/价格/状态），否则写
   `skus`（含 `sku_code`、`spec_key`）与 `sku_spec_values`；`uk_product_signature`/`uk_sku_code` 兜底并发重复。
4. 生成不删除 SKU、不写 `inventories`；库存由 Inventory 模块按 `sku_id` 初始化。

读路径：

- 详情按商品批量读 SKU（`ListByProduct`）+ 规格取值（一次 join，无 N+1），组装 `attr`/`productValue`。
- SKU → 规格展示 → `sku_spec_values` join `product_specs`/`product_spec_values`（按 `sort`、`id`）。
- 下单按 `sku_code` 定位（`ISku.GetByCode`），成交价只取 `skus.price`。
- 历史 SKU：`spec_signature IS NULL` 且无关联行，按「无规格」展示，不进入 `productValue`。

## 6. 唯一约束与冲突语义

| 约束 | 保证 | 冲突时 |
| --- | --- | --- |
| `uk_product_name (product_id, name)` | 同商品 SKU 名称唯一 | 现有代码映射 1062 → `5004`（409） |
| `uk_sku_code (sku_code)` | SKU 业务编码全局唯一；多 NULL 允许（仅离线写入） | 组合生成命中视为已存在；手动创建重试换码 |
| `uk_product_signature (product_id, spec_signature)` | 同商品规格组合唯一；多 NULL 允许 | 组合生成命中视为已存在（幂等）；其余写入 5016 |
| `uk_product_spec_name (product_id, name)` | 同商品规格组名唯一 | 5011（409） |
| `uk_spec_value (spec_id, value)` | 同规格组值唯一 | 5012（409） |
| `sku_spec_values` 主键 | 同 SKU 同规格组单值 | 写入前校验，DB 兜底 |
| 复合外键 | 值属于声明的规格组 | 写入前校验，DB 兜底（1452） |

## 7. 兼容策略（不要破坏的部分）

- **历史 SKU**：不回填、不改 `skus.name`；`image_url` 默认空串表示回退商品主图；`spec_signature` 为 NULL。
- **稳定引用键**：`skus.id` 语义不变，购物车/订单项/库存/秒杀仍按它引用；新增表不回写这些表。
- **不可改的表**：`inventories`、`inventory_logs`、`flash_sale_*`。规格只影响「选哪个 SKU」，不影响库存与秒杀事实来源。
- **价格**：`skus.price` 仍是唯一下单定价来源；`spec_signature` 不可作为价格或身份凭证。
- **订单规格快照**：`order_items.spec_text`（B1-03-02 新增，默认空串）用于下单时保存规格文本，使订单读取不依赖
  `sku_spec_values` 是否仍存在；该列已建但下单链路尚未写入，历史订单为空串。

## 8. 关键测试与运行方式

迁移与规格约束需要真实 MySQL；商品/SKU 接口测试还需要 Redis：

```bash
DATABASE_DEFAULT_PASS=root go test -count=1 -p 1 ./internal/migrations/
DATABASE_DEFAULT_PASS=root go test -count=1 -p 1 ./internal/controller/product/ ./internal/controller/sku/
DATABASE_DEFAULT_PASS=root go test -count=1 -p 1 -run 'TestOrder' ./test/e2e/
```

| 测试 | 覆盖点 |
| --- | --- |
| `TestSkuCodeAndOriginalPriceBackfill` | 历史 SKU 回填 `sku_code`/`original_price`；`uk_sku_code` 生效且允许多 NULL；`order_items` 快照列存在 |
| `TestSkuCodePricingMigrationDownUp` | down → up 后列/索引重建，既有 SKU 行不丢失 |
| `TestSpecGroupAndValueCRUD`（`TestProductSpecCRUDAndRejections`） | 规格组/值 CRUD、重名冲突、跨商品隔离、非法规格组、RBAC 403 |
| `TestSkuGenerateCombinations` | 2×3 笛卡尔积、`spec_key` 稳定、重复/并发不重复、跨商品不冲突、无规格 5015、被购物车引用不删除 |
| `TestProductDetailVanSkuContract` | `attr`/`productValue` 结构、价格/库存、停用SKU 收敛、无规格空数组/空对象、下架 404、无成本价 |
| `TestOrderPricingBySkuCode` 等（`test/e2e`） | 按 `sku_code` 服务端定价、伪造价 9007、非法编码/停用/下架拒绝、无规格下单、库存不足回滚、购物车忽略客户端价格 |
| `TestSpecKey`/`TestSkuCodeDerivation`/`TestCombinations` | 指纹算法与编码派生、笛卡尔积（纯内存单测） |
| `TestProductModelMigrationUpDownUp` | up → down → up；回滚后既有 SKU 行不丢失；新增列默认值正确 |
| `TestLegacySkuWithoutSpecRemainsUsable` | 无规格历史 SKU 仍可进购物车、建库存、落订单项、绑秒杀活动 |
| `TestSkuSpecCombinationUniqueness` | 同商品同指纹拒绝重复；NULL 指纹多行；不同商品互不影响；不同组合关联不同 SKU |
| `TestSkuSpecValueIntegrity` | 同 SKU 同规格组单值、规格值归属、被引用值不可删、删 SKU 级联 |
| `TestB103MigrationFilesArePaired` | 本批次 up/down 成对、版本唯一且递增 |
| `internal/controller/product`、`internal/controller/sku` | 现有商品/SKU 读写与权限无回归 |

## 9. 后续历史数据迁移注意事项

- 规格来源：`store_product_attr`（组）+ `store_product_attr_result`（矩阵，用于校正值集合）+ `store_product_attr_value`（SKU 行）；
  不要只按逗号串拆 `attr_values`，该字段可能包含被改写的值。
- `store_product_attr_value.unique` 是原系统的 32 位组合码，**不能**直接当 `spec_signature` 使用：
  必须按本项目的算法（`spec_id` 升序 + `spec_id:spec_value_id` + MD5）重新生成，否则唯一约束失效。
- 无规格商品（`spec_type=0`）保持不写规格关联、`spec_signature=NULL`，不要臆造「默认」规格组。
- 写入顺序：`product_specs` → `product_spec_values` → `skus` → `sku_spec_values`，同一商品一个事务；
  撞 `uk_product_signature` 时应记录为数据问题并跳过，不得改指纹绕过。
- SKU 图片：遗留工具已解析 `attr_value.image` 但当前未落库；扩展时写 `skus.image_url`（空则留空，展示回退商品主图）。
- 库存只取 SKU 级 `attr_value.stock`，不要与商品级 `store_product.stock` 相加（库存仍只写 `inventories`）。
