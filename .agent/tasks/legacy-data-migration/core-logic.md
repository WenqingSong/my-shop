# Owner Core Logic 验证

> 面向 Owner 的核心机制验证卡。数据迁移工具的正确性最终归结为「库存不重复计算」与「下架/软删除商品不可被下单」两条业务不变量，其余（金额定点、分类层级、幂等）以测试兜底。以下验证卡可直接在隔离环境复现。

## CL-001：库存去重与幂等（不重复计算、不重复导入）

- Owner 需要理解：同一商品的库存要么全部来自 SKU 级（`store_product_attr_value.stock`）、要么全部来自商品级（`store_product.stock`），二者绝不叠加；重复执行 `import` 必须通过 `legacy_id_map` 幂等跳过，不能新增重复商品/SKU/库存，也不能重复增加库存。若叠加或幂等失效，库存数字会虚高，直接影响下单可售数量。
- 生产代码：`internal/logic/legacymigrate/mapping.go` 的 `MapSkus`（有明细用行 stock、无明细单规格用商品级 stock）；`internal/logic/legacymigrate/engine.go` 的 `loadMigratedMap` + `migrateProducts`（已迁移 old_id 跳过）。
- 关键测试：`TestMapSkusInventoryDedup`（`mapping_test.go`）、`TestEngineImportIdempotentAndIDMap`（`engine_test.go`）。
- 基线验证：`DATABASE_DEFAULT_PASS=root go test ./internal/logic/legacymigrate/... -run 'TestMapSkusInventoryDedup|TestEngineImportIdempotentAndIDMap' -count=1` → 预期 PASS。
- 可选 Mutation：把 `MapSkus`「有明细」分支的库存改为 `Inventory: InventoryTarget{Quantity: av.Stock + productStock}`（叠加商品级库存）。
- 预期失败：`TestMapSkusInventoryDedup/有明细-多规格按行库存` 必须失败（多规格商品库存被叠加，且断言「不应使用商品级库存」触发）。
- 恢复确认：还原代码并重跑上述命令 → 恢复 PASS。

## CL-002：SKU 状态安全（软删除/下架商品不可下单、前台不可见）

- Owner 需要理解：`skus.status = enabled(1)` 当且仅当商品 `status = on_shelf(1)`，其余（off_shelf/draft）一律 `disabled(0)`。历史软删除（`is_del=1`）与下架商品映射为 `off_shelf`，其 SKU 必须 `disabled`；否则这些商品可能被下单（下单入口同时校验 SKU enabled 与商品 on_shelf）或前台展示，意外恢复销售。
- 生产代码：`internal/logic/legacymigrate/mapping.go` 的 `SkuStatusForProduct`；`MapProduct` 把 `is_del=1 → off_shelf`、`is_del=0 && is_show=0 → off_shelf`。
- 关键测试：`TestSkuStatusForProduct`、`TestMapProductStatus`（`mapping_test.go`）。
- 基线验证：`go test ./internal/logic/legacymigrate/... -run 'TestSkuStatusForProduct|TestMapProductStatus' -count=1` → 预期 PASS。
- 可选 Mutation：把 `SkuStatusForProduct` 改为恒返回 `skuStatusEnabled`（无条件 enabled）。
- 预期失败：`TestSkuStatusForProduct` 的 `off_shelf`、`draft` 用例必须失败（SKU 被错误启用）。
- 恢复确认：还原代码并重跑上述命令 → 恢复 PASS。
