# Technical Contract

## Decision Status

APPROVED

## Design Impact

NEW（新增目标库表 `legacy_id_map`，需 migration version；新增 `docs/design/legacy-data-migration.md` 设计沉淀）

## Problem

把根目录历史生产库 `shopping.sql`（CRMEB 导出，83 表）中的商品、分类、SKU、库存及图片，经独立数据迁移工具适配进隔离测试环境，最终通过现有 GoFrame API 查询到迁移结果，且不破坏 SurgeCart 既有 Schema、业务逻辑与一致性约束。核心难点在旧 ID→新 ID 映射落点、规格→SKU 权威来源、商品级/SKU 级库存去重、软删除/上下架到目标状态机映射。

## Verified Current Behavior

- VERIFIED：目标 Schema（`internal/migrations/sql/`）为 `categories(id,parent_id,name,sort,status,uk(parent_id,name))`、`products(id,name,brand,category_id,price INT UNSIGNED 分,main_image,detail,status 0/1/2,fk→categories)`、`product_images`、`skus(id,product_id,name,price 分,status 0/1,uk(product_id,name),fk→products)`、`inventories(id,sku_id,quantity,uk(sku_id),fk→skus)`。目标只有 SKU 级库存。
- VERIFIED：状态语义（`docs/design/*` + 代码）：`categories.status` `1=启用/0=禁用`；`products.status` `0=draft/1=on_shelf/2=off_shelf`；`skus.status` `1=enabled/0=disabled`。
- VERIFIED：下单入口校验（`internal/logic/order/order.go` `resolveLine`）：`sku.Status != enabled → CodeOrderSkuUnavailable`，且 `product.Status != on_shelf → CodeOrderSkuUnavailable`——**下单同时要求 SKU enabled 且商品 on_shelf**。
- VERIFIED：前台可见性（`internal/logic/product/product.go`）：前台列表/详情仅 `on_shelf`；前台详情只组合 `enabled` SKU；后台详情返回全部 SKU。
- VERIFIED：历史表结构为 `store_category(id,pid,cate_name,sort,pic,is_show,is_del)`、`store_product(cate_id 字符串,price decimal(8,2),stock 商品级,is_show,is_del,spec_type,image,slider_image,store_name,store_info,description)`、`store_product_attr(product_id,attr_name,attr_values)`、`store_product_attr_value(product_id,sku,stock,price,image,…)`、`store_product_attr_result(result JSON)`。
- VERIFIED：`shopping.sql` 头部含 `SET FOREIGN_KEY_CHECKS = 0;`（第 18 行）与 `SET NAMES utf8mb4`，含全部 83 表 `DROP TABLE IF EXISTS` + `CREATE TABLE`。只能作用于 staging 库，目标库连接不得关闭外键校验。
- VERIFIED（数据污染）：`store_product_attr_result.result` 对 `product_id` 6~108 是重复复制的同一份护肤品 JSON，与商品真实身份矛盾，**不可作为 SKU 权威来源**。
- VERIFIED（字段不可靠）：`store_product.spec_type` 与真实规格不一致（商品 2 `spec_type=0` 却有 2 条 SKU），判定单/多规格不能用它。
- VERIFIED（SKU 平铺事实）：`store_product_attr_value` 是 SKU 级权威来源（`product_id + sku + stock + price + image`）；单规格商品有 `sku='默认'` 行，多规格商品多行。
- VERIFIED（软删除普遍）：`store_category` 57 行约 40 行 `is_del=1`；`store_product` 亦存在 `is_del=1`。
- VERIFIED（API/命令/环境）：路由、`internal/cmd` 子命令机制、`docker-compose.yml` 单一 MySQL、金额 `decimal(8,2)` 精确转分均已核实。
- UNKNOWN：逐商品"引用软删除/不存在分类的 cate_id"精确分布、缺 SKU 明细商品精确个数——运行时判定并报告（AC-012），不阻塞设计。

## Recommendation

（历史推荐记录，已被 Owner 决策覆盖）

- D1：推荐 A（staging 库映射表）→ **Owner 选 B（目标库映射表）**。
- D2：推荐软删除不跳过、映射为不可见 → **Owner 接受**。

## Selected Design

**D1（ID 映射落点，Owner 选 B）**：`legacy_id_map` 放在 SurgeCart **目标数据库**，与业务数据同库同事务提交。

- 新增 migration `20261001000023_legacy_id_map`（已按 Registry 预留，见「全局资源」）。
- 表结构（设计级约束，DDL 由 Coder 落定）：字段至少含 `id`、`source`（来源标识，如 `shopping.sql`）、`entity_type`（`category`/`product`/`sku`）、`old_id`（历史旧 ID）、`new_id`（目标新 ID）、`created_at`/`updated_at`（审计）；唯一约束 `uk(entity_type, old_id)`（必要时 `(source, entity_type, old_id)`）保证映射幂等；**不依赖商品名称等自然键去重**。
- 每次迁移在**同一 MySQL 事务**内写入业务记录与其 `legacy_id_map` 行，保证失败恢复/重复导入/staging 清理后映射可追溯。

**D2（软删除处理，Owner 接受推荐）**：软删除数据保留并映射为不可见状态。

- 分类：`is_del=1 → status=0`（禁用）；`is_del=0 → status=1`（启用）。`store_category.is_show`（是否推荐）目标无对应字段，不映射。
- 商品：`is_del=1 → status=2`（off_shelf）；`is_del=0` 时 `is_show=1 → status=1`（on_shelf）、`is_show=0 → status=2`（off_shelf）。
- SKU 状态（**不无条件 enabled**，已核实现有校验）：**`skus.status = enabled(1) 当且仅当 商品 status = on_shelf(1)`，其余（off_shelf/draft）一律 `disabled(0)`**。依据：下单入口 `resolveLine` 同时要求 SKU enabled 且商品 on_shelf；前台详情只返回 enabled SKU。此规则保证软删除/下架商品即使 SKU 存在也不可被下单或前台展示，不意外恢复销售。

**其余已核实规则（无需再决策）**：

- SKU 权威来源：`store_product_attr_value`（`sku`→`skus.name`、`stock`→库存、`price`→转分、`image`→SKU 图，空回退商品主图）；`store_product_attr` 仅用于维度/组合名校验与单规格判定；`store_product_attr_result` 弃用。
- 库存去重：有 `attr_value` 行 → 每行一个 SKU + 一个 inventory（quantity=该行 stock），商品级 stock 忽略；无 `attr_value` 行 → 单规格，生成 1 个 `name='默认'` SKU，quantity=商品级 stock。绝不把商品级与 SKU 级相加。
- 默认 SKU：单规格商品以商品级 stock/price 为据创建 `name='默认'` SKU；多规格缺 `attr_value` 明细 → 跳过并记录原因。
- 金额：字符串解析元→分 ×100（定点，禁浮点）；`NULL`→0、负数/非数值→跳过并记录。
- 图片：`/file/pic/...` 字符串直落，`main_image`=主图、`product_images`=轮播图拆分。
- 分类关联：`cate_id` 引用不存在或未迁移分类 → 该商品跳过并记录原因（`products.category_id` FK 必须有效）。
- 错误码域：迁移工具独立 CLI、不新增 HTTP 接口，**无需新错误码域**。
- 隔离环境：同一 MySQL 实例新增独立 schema `my_shop_staging`；工具 DSN 仅指向 staging，SurgeCart 仅指向 `my_shop`；`SET FOREIGN_KEY_CHECKS=0` 只在 staging 连接生效，目标库连接绝不关闭外键。

## Interfaces and Data

- 新增目标库表：`legacy_id_map`（migration `20261001000023`），见 Selected Design。
- 新增独立 Data Migration CLI（复用 `internal/cmd` 子命令 `legacy-migrate <dry-run|import>`），不新增 HTTP 接口、不改既有 migration 文件、不改既有业务模块行为。
- 新增 staging 库：`my_shop_staging`（隔离 schema），承载 `shopping.sql` 全量导入。
- 新增数据文档：`docs/legacy-data.md`（来源/用途/范围/安全注意）。
- 新增设计文档：`docs/design/legacy-data-migration.md`（迁移架构、`legacy_id_map`、映射规则、失败语义，Design Impact NEW）。
- 保持：`categories/products/product_images/skus/inventories` 既有约束（`uk(parent_id,name)`、`uk(product_id,name)`、`uk(sku_id)`、FK `category_id`/`product_id`/`sku_id`）不被绕过；不覆盖非迁移来源存量数据；原始 SQL 不入 Git/镜像/CI。

## Business Invariants

- INV-001（金额精确）：`price` 元→整数分全程定点（`19.90→1990`），无浮点误差；`products.price`/`skus.price` 均为整数分且 ≤ `99,999,999`。
- INV-002（库存不重复计算）：同一商品库存要么全来自 SKU 级、要么全来自商品级，不叠加；每个 `sku_id` 恰有一条 `inventories`（`uk(sku_id)` 兜底）。
- INV-003（ID 映射唯一）：`legacy_id_map` 的 `(entity_type, old_id)` 唯一，重复执行同一 old_id 只产生一个 new_id，不新增重复商品/SKU/库存。
- INV-004（约束不被绕过）：目标库导入不通过 `SET FOREIGN_KEY_CHECKS=0` 或删唯一键绕过校验；FK 引用无效则跳过并记录。
- INV-005（存量不被覆盖）：非迁移来源业务数据导入前后不变（仅增量插入）。
- INV-006（失败原子）：业务记录与对应 `legacy_id_map` 行同事务提交，任一步失败整体回滚，目标库不残留部分写入。
- INV-007（SKU 可见性安全）：`skus.status` 严格等于「商品 on_shelf 与否」，软删除/下架商品不可下单、前台不可见。

## Failure and Consistency Semantics

- 事实来源：原始数据 = staging 库；迁移结果 = 目标库 `my_shop`；ID 映射 = 目标库 `legacy_id_map`（与业务数据同库）。
- 成功代表：Dry Run 不写目标库、只输出将执行变更与校验结果；`import` 完成代表所有可迁移行已按规则落库，报告数与库内实际一致。
- 事务边界：目标库按"分类 / 商品+图册+SKU+库存+`legacy_id_map`"分批在单事务写入，任一步失败回滚该批；业务记录与其映射行**同事务**（D1=B 的原子性保证）。
- 幂等：重复 `import` 先查 `legacy_id_map`，已迁移 old_id 跳过；不重复增加库存、不重复插入。
- 重复/乱序：线性批处理，无 MQ/异步，无消费者乱序问题。

## Allowed / Forbidden Changes

- 允许：新增 `legacy_id_map` migration（`20261001000023`）；独立迁移 CLI 及单测/集成测试；staging 库隔离；`.gitignore`/`.dockerignore` 增补归档路径；`docs/legacy-data.md` 与 `docs/design/legacy-data-migration.md`；归档 `shopping.sql → storage/private/legacy/shopping.sql`。
- 禁止：改动既有目标表/历史 migration 文件；新增 HTTP 接口/错误码；关闭目标库外键；连接历史生产库；把原始 SQL 提交进 Git/镜像/CI；覆盖非迁移来源存量数据；迁移订单/用户/优惠券等 Out of Scope 业务。

## 全局资源（Generic Shared Resource）

| kind | 资源值 | 状态 | 说明 |
| --- | --- | --- | --- |
| migration_version | `20261001000023` | RESERVED | 目标库新增 `legacy_id_map` 表 |

（`next = max(已记录 version, 含 RELEASED) + 1 = 20261001000022 + 1`，经 Registry-only commit 落实 RESERVED 后 Coder 才可使用。）

## Verification Requirements

- INV-001 → 单测 `19.90→1990`/`0.00→0`/`NULL→0`/非法值→跳过；集成断言 `price` 为整数分。
- INV-002 → 多规格用 SKU 级、单规格用商品级，导入后 `inventories` 与 `skus` 一对一、`quantity` 精确无叠加。
- INV-003 → 重复执行 `import` 两遍，商品/SKU/库存/`legacy_id_map` 行数与第一遍一致。
- INV-004 → 导入后无唯一/FK 冲突；Dry Run 后目标库零写入；目标库连接无 `FOREIGN_KEY_CHECKS=0`。
- INV-005 → 比对迁移前存量未被覆盖。
- INV-006 → 注入中途失败，断言目标库无部分残留（含 `legacy_id_map` 无孤儿行）。
- INV-007 → 软删除/下架商品 SKU 为 disabled，前台不可见、下单被拒（`CodeOrderSkuUnavailable`/`CodeProduct...`）；on_shelf 商品 SKU 为 enabled 且前台可查。
- AC-013 → 真实服务 + `GET /categories`、`GET /products`、`GET /products/:id`、`GET /admin/products/:id`、`GET /admin/inventories/:sku_id` 断言迁移结果（前台仅 on_shelf，全量走后台）。

## Open Risks

- 历史数据除 `attr_result` 污染外仍可能有同类脏数据，靠运行时校验 + 跳过 + 原因记录兜底。
- `legacy_id_map` 与业务表同库，目标库新增一表（已由 Design Impact NEW 与 migration 预留覆盖）。
- 隔离环境为同实例双库；staging 重建不影响 `legacy_id_map`（在目标库），D1=B 已消除该风险。

## Owner Decision Record

- 2026-10-11，Owner 决策 D1=**B**（`legacy_id_map` 放目标库，同事务提交，新增专用 migration，遵循 Registry 预留）；D2=**接受推荐**（软删除映射为不可见；SKU 状态不得无条件 enabled，须核实现有校验规则）。
- 补充约束（1~7）已全部并入 Selected Design / Invariants / Allowed-Forbidden。
- 适用范围：本任务 legacy-data-migration 的数据迁移实现；不改变既有业务模块行为。
