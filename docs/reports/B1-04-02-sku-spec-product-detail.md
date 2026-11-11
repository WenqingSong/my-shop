# B1-04-02 交付报告：SKU 规格与商品详情

## 1. 元信息

| 项 | 值 |
| --- | --- |
| 任务 | B1-04-02 SKU 规格与商品详情 |
| 仓库 | `/root/projects/my-shop` |
| 基线分支 | `feat/p2-backend` |
| 基线提交 SHA | `637ae2d35d98892094b82184bc8045e6fda3b42c` |
| 开发分支 | `feat/b1-04-02-sku-spec-product-detail` |
| 最终提交 SHA | 本报告所在提交（见 `git log -1 --oneline`） |
| 结论 | PASS（限本报告 §8 声明的验证范围） |

## 2. 实施范围

1. 商品规格组 / 规格值的写入与读取（含排序、重名、归属、RBAC）。
2. SKU 规格组合生成（服务端笛卡尔积）与 `spec_key` 唯一性。
3. `sku_code` 业务编码（唯一）与 `original_price` 原价。
4. 商品详情返回原 APP `van-sku` 兼容的 `attr[]` + `productValue{}`。
5. 下单按 `sku_code` 取服务端权威价格，拒绝客户端伪造价格。
6. 无规格商品全链路兼容。

未改动：前端源码、秒杀 Redis/Lua/Kafka/Outbox、库存模块实现、B1-03 历史 Migration、B1-04-01/03/04/05/06/07/08 范围。

## 3. 数据模型与 Migration

### 3.1 复用既有结构（不重复建表）

B1-03-03 已建立：`product_specs`（规格组）、`product_spec_values`（规格值）、`sku_spec_values`（SKU↔规格值），
以及 `skus.spec_signature CHAR(32) NULL` + `uk_product_signature (product_id, spec_signature)`。

本任务把 `skus.spec_signature` 作为任务口径的 **`spec_key`**：算法为
`MD5(按 spec_id 升序拼接 "<spec_id>:<spec_value_id>"，逗号连接)`，32 位小写 hex。
其唯一性由 `uk_product_signature` 保证，作用域为单商品（不同商品相同组合不冲突）。

### 3.2 新增 Migration

`internal/migrations/sql/20261111000001_sku_code_pricing.{up,down}.sql`（向前兼容，纯 additive）：

| 表 | 变更 |
| --- | --- |
| `skus` | `sku_code VARCHAR(64) NULL` + `uk_sku_code`；`original_price INT UNSIGNED NOT NULL DEFAULT 0` |
| `skus`（回填） | 历史行 `sku_code = CONCAT('SKU', LPAD(id,10,'0'))`；`original_price = price`（price>0 时） |
| `order_items` | `sku_code VARCHAR(64) NOT NULL DEFAULT ''`、`original_price INT UNSIGNED NOT NULL DEFAULT 0` |

设计说明：
- `sku_code` 允许 NULL 是为兼容离线/迁移脚本写入（唯一索引允许多个 NULL）；在线业务写入一律生成编码。
- 未修改任何历史 Migration；`latestMigrationVersion` 更新为 `20261111000001`。
- 未在 `skus` 增加库存列，库存仍以 `inventories` 为唯一权威来源。

## 4. 新增与修改的 API

### 4.1 新增（7 条后台接口）

| 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| POST | `/admin/product-specs` | `spec:create` | 创建规格组 |
| PUT | `/admin/product-specs/:id` | `spec:update` | 更新规格组名称/排序 |
| DELETE | `/admin/product-specs/:id` | `spec:delete` | 删除规格组（级联值；被引用时 5013） |
| POST | `/admin/product-specs/:id/values` | `spec:create` | 创建规格值 |
| PUT | `/admin/product-spec-values/:id` | `spec:update` | 更新规格值 |
| DELETE | `/admin/product-spec-values/:id` | `spec:delete` | 删除规格值（被引用时 5013） |
| POST | `/admin/products/:id/skus/generate` | `sku:create` | 按规格组合生成 SKU（幂等） |

### 4.2 修改（保持向后兼容）

| 接口 | 变更 |
| --- | --- |
| `GET /products/:id` | 新增 `attr[]`、`productValue{}`；`skus[]` 增加 `sku_code`/`original_price`/`image_url`/`spec_key`/`spec_values`；保留原有全部字段 |
| `GET /admin/products/:id` | 新增 `specs[]`（含 id）；`skus[]` 同上 |
| `POST/PUT /admin/skus` | 新增可选 `original_price`/`image_url`；响应增加新字段（`sku_code` 服务端生成，不接受客户端指定） |
| `POST /orders` | 新增可选 `sku_code`（优先）与 `price`（仅防伪校验）；订单项新增 `sku_code`/`spec_text`/`original_price` |

## 5. 实现要点

### 5.1 规格组 / 规格值

- 位置：`api/spec/v1/spec.go`、`internal/controller/spec/spec.go`、`internal/service/spec.go`、`internal/logic/spec/spec.go`。
- 规格组必须指向存在商品（否则 4001）；组名/值 trim 后 1..32 字符；`sort` 升序、同值按 id。
- 重复组名 → 5011；重复值 → 5012；不存在的组/值 → 5010；被 SKU 引用的值/组 → 5013（复合外键 RESTRICT 兜底）。
- 规格值只能通过所属规格组的路径创建，结构上不可能跨商品写入。
- 权限：`spec:create` / `spec:update` / `spec:delete`（已 seed，契约检查覆盖 permission↔route↔seed）。

### 5.2 SKU 组合生成与唯一性

- 位置：`internal/logic/sku/sku.go`（`Generate`、`combinations`、`specKey`、`generatedSkuCode`）。
- 服务端按商品规格组生成笛卡尔积（2 组 2×3=6；3 组 2×3×2=12）。
- 每个组合计算 `spec_key`，按 `(product_id, spec_key)` 命中既有 SKU 则保留（编码/价格/状态/图片不动），否则新建。
- 新 SKU：`name` = 规格值文本按顺序 `,` 连接；`price` 缺省取 `products.price`；`original_price` 缺省等于售价；
  `sku_code = SKU-P<product_id>-<spec_key 前 12 位大写>`（稳定派生）。
- 并发/重复提交由 `uk_product_signature`、`uk_sku_code` 兜底：命中重复键计为 `kept`，不产生重复 SKU，接口幂等。
- **不删除任何 SKU**；不写 `inventories`（库存由 Inventory 模块初始化，无记录 = 0）。
- 无规格商品调用生成 → 400/5015；应改用 `POST /admin/skus` 创建单 SKU。

### 5.3 商品详情 van-sku 契约

`GET /products/:id`（仅 `on_shelf`，仅 `enabled` SKU）：

```json
{
  "attr": [{"attrName": "颜色", "attrValueArr": ["红色", "蓝色"]}],
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

- 键 = 按 `attr[]` 顺序逗号连接的规格值文本；`unique` = `sku_code`；`stock` 来自 `inventories`。
- 无规格商品：`attr` 为 `[]`、`productValue` 为 `{}`（显式初始化为空，不是 `null`）。
- 规格不完整的历史 SKU 不进入 `productValue`，仍保留在 `skus[]`（可按 `sku_id`/`sku_code` 下单）。
- 下架商品 404/4001；响应不含成本价字段（`skus` 表本身无成本价列）。

> 说明：任务参考资料目录 `tmp/eshop-app-vue|eshop-pc-vue|eshop-backend-java|shopping.sql` 在当前仓库/环境中不存在
> （`tmp/` 被 `.gitignore` 忽略，历史提交中亦无该目录）。契约依据为：任务给定的 `attrName`/`attrValueArr`/`unique`/`sku`
> 字段、遗留 `store_product_attr_result.result` 结构（`attr[].value/detail` + `value[].detail`，见 `legacymigrate`）、
> `docs/design/sku.md` 既有语义与现有 `skus` 字段。已在 `sku.md` §2.6 固化。

### 5.4 下单服务端定价

- 位置：`internal/logic/order/order.go`（`buildLines`、`resolveLine`、`verifyClientPrice`、`skuSpecText`、`insertOrder`）。
- `source=direct`：优先 `sku_code` 查库；兼容 `sku_id`；二者同时提交必须一致（否则 9003）。
- 校验链：SKU 存在（5001）→ enabled（9003）→ 商品 on_shelf（9003）。
- 成交价只取 `skus.price`；客户端 `price` 仅比对：不一致 → 9007（409）；`source=cart` 提交 `price` 一律 9007。
- 订单项快照：`sku_code`、`spec_text`（规格值文本）、`price`、`original_price`、名称与主图。
- 库存扣减复用 `IInventory.DeductInTx`，与订单/订单项/清购物车同事务；失败整体回滚。
- 购物车链路：`price_snapshot` 只取 `skus.price`，客户端提交的价格字段被忽略（测试覆盖）。

## 6. 无规格商品兼容

- 新建：`POST /admin/skus` 不要求规格，`spec_signature`/`spec_key` 为空、无 `sku_spec_values`。
- 详情：`attr=[]`、`productValue={}`，`skus[]` 正常返回 `sku_code`。
- 下单：`sku_code` 或 `sku_id` 均可正常下单，`spec_text` 为空串。
- 组合生成：明确返回 400/5015，不臆造默认规格组。

## 7. 修改文件清单

新增：
- `api/spec/v1/spec.go`
- `internal/controller/spec/spec.go`
- `internal/logic/spec/spec.go`
- `internal/service/spec.go`
- `internal/migrations/sql/20261111000001_sku_code_pricing.up.sql`
- `internal/migrations/sql/20261111000001_sku_code_pricing.down.sql`
- `internal/migrations/sku_pricing_migration_test.go`
- `internal/logic/sku/spec_key_test.go`
- `internal/controller/product/product_spec_detail_test.go`
- `test/e2e/order_sku_pricing_test.go`
- `openapi/paths/spec.yaml`（生成产物）
- `docs/reports/B1-04-02-sku-spec-product-detail.md`

修改（业务）：
- `api/product/v1/product.go`、`api/sku/v1/sku.go`、`api/order/v1/order.go`
- `internal/logic/sku/sku.go`、`internal/logic/product/product.go`、`internal/logic/order/order.go`、
  `internal/logic/inventory/inventory.go`、`internal/logic/logic.go`
- `internal/service/sku.go`、`internal/service/inventory.go`
- `internal/controller/sku/sku.go`
- `internal/cmd/routes_admin.go`、`internal/cmd/routes_test.go`
- `internal/codes/codes.go`、`internal/boot/seed.go`
- `internal/migrations/migrations_test.go`

修改（契约/工具/脚本/文档）：
- `tools/openapi-gen/{model,parse_dto,build_schema,emit,gen,opmeta,parse_codes,main_test}.go`、
  `tools/openapi-gen/unused-components-allowlist.yaml`
- `openapi/{surgecart.openapi.yaml,paths/sku.yaml,paths/order.yaml,components/schemas.yaml,components/error-codes.yaml}`（生成产物）
- `docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md`（生成产物）
- `scripts/test-unit.sh`、`scripts/contract-check.sh`
- `docs/design/{sku,product,order,migration}.md`、`docs/dev/product-sku.md`

## 8. 验证结果

环境：Go 1.26.0；真实 MySQL 8.0 与 Redis 7（本地容器 `b10402-mysql`/`b10402-redis`，非生产/非共享开发库）。

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 构建 | `go build ./...` | PASS |
| 静态检查 | `go vet ./...` | PASS |
| 单元测试门禁 | `make test-unit` | PASS |
| 迁移测试（全量） | `DATABASE_DEFAULT_PASS=root go test -count=1 -p 1 ./internal/migrations/` | PASS（331s） |
| 商品接口 + 规格/详情集成 | `go test -p 1 ./internal/controller/product/` | PASS（含 3 个新增用例） |
| SKU 接口回归 | `go test -p 1 ./internal/controller/sku/` | PASS |
| 购物车接口回归 | `go test -p 1 ./internal/controller/cart/` | PASS |
| 库存接口回归 | `go test -p 1 ./internal/controller/inventory/` | PASS |
| 订单/购物车/退款端到端 | `go test -p 1 -run 'TestOrder\|TestCart\|TestRefund' ./test/e2e/` | PASS（含 5 个新增用例） |
| OpenAPI 契约 | `bash scripts/contract-check.sh` | PASS（0 NOT RUN） |
| 空白/冲突检查 | `git diff --check` | PASS |

新增测试覆盖（对应验收表）：

| 场景 | 用例 | 结果 |
| --- | --- | --- |
| 正常 SKU 下单用数据库价格 | `TestOrderPricingBySkuCode` | PASS |
| 客户端伪造/篡改价格拒绝 | `TestOrderRejectsForgedPrice`（1/0/4999/5001/999999 及购物车带价） | PASS |
| 不存在的 sku_code | `TestOrderRejectsInvalidSkuCode` | PASS |
| sku_code 与 sku_id 不匹配 | 同上 | PASS |
| SKU 停用 / 商品下架 | 同上 | PASS |
| 无规格商品下单 | `TestOrderNoSpecSkuAndInsufficientStock` | PASS |
| 库存不足沿用 6001 且回滚 | 同上 | PASS |
| 购物车忽略客户端价格 | `TestCartIgnoresClientPrice` | PASS |
| 规格 CRUD / 重复 / 跨商品 / 越权 | `TestProductSpecCRUDAndRejections` | PASS |
| 笛卡尔积 / spec_key 稳定 / 重复与并发 / 跨商品隔离 / 不删除被引用 SKU | `TestSkuGenerateCombinations` | PASS |
| 详情结构 / 价格库存 / 停用收敛 / 无规格空数组 / 下架 / 无成本价 | `TestProductDetailVanSkuContract` | PASS |
| 指纹算法与编码派生（纯内存） | `TestSpecKeyStableAndSorted` 等 | PASS |

**NOT RUN（明确未验证）**：
- `make test`（完整门禁包含容器/K8s/Nginx/Registry/竞态等，超出本任务范围与本机依赖）。
- 全仓 `go test ./...`（其余域未受改动影响，未逐一重跑）。
- `make test-integration`（等价于全量集成，耗时超出本次验证窗口；已按受影响域分组运行）。
- 遗留数据导入（`import-legacy`）端到端：需要 staging 库，未运行。
- 前端联调：仓库不含前端源码与 `tmp/` 参考资料，未做真实 `van-sku` 渲染验证。

## 9. 已知限制

1. **前端契约依据**：`tmp/` 参考资料缺失，`attr`/`productValue` 结构由任务字段清单 + 遗留 `attr_result` 结构 + 现有设计推导，
   已写入 `docs/design/sku.md` §2.6；若原 APP 前端对键顺序/字段名有额外约定，需以真实前端联调结果微调（只影响响应字段，不影响业务规则）。
2. **`sku_code` 的 NULL 兼容**：离线/遗留导入脚本若在本次迁移后写入 SKU 且未提供编码，会留下 NULL 编码（详情 `unique` 为空）。
   `legacymigrate` 未在本任务中修改以控制范围；既有存量行已由迁移回填。
3. **历史 SKU 补全**：既有 `spec_signature` 但缺少 `sku_spec_values` 关联的历史行，重新生成时按「已存在」保留，不会自动补齐关联。
4. **组合生成粒度**：一次生成全部组合，不支持只生成子集或按组合单独定价；生成后可用 `PUT /admin/skus/:id` 调整。
5. **库存初始化**：生成不自动建库存（遵循 Inventory 模块职责），新 SKU 初始可售库存为 0，需后台增加库存。

## 10. 结论

**PASS**：本任务在声明范围内完成规格组/值、SKU 组合生成与 `spec_key` 唯一性、`sku_code`/原价、
商品详情 van-sku 契约、下单服务端定价与快照、无规格兼容；构建、vet、单测、受影响集成测试与契约检查全部通过。
§8/§9 列出的未运行项与已知限制已如实披露，不影响本任务验收条件。
