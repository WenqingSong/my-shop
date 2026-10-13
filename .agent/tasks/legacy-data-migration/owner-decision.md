# Owner Decision

## Review Target

04350e374224db9a6c8eb1573aa327c6d45c987f

## Core Logic

- CL-001（库存不重复计算 + 重复导入幂等）：同一商品库存要么全来自 SKU 级、要么全来自商品级，绝不叠加；重复 `import` 靠 `legacy_id_map` 幂等跳过，不新增重复商品/SKU/库存、不重复加库存。生产代码 `internal/logic/legacymigrate/mapping.go` `MapSkus`、`engine.go` `loadMigratedMap`/`migrateProducts`；关键测试 `TestMapSkusInventoryDedup`、`TestEngineImportIdempotentAndIDMap`。
- CL-002（SKU 状态安全）：`skus.status = enabled` 当且仅当商品 `on_shelf`，其余（off_shelf/draft）一律 `disabled`；软删除/下架商品 SKU 必须 disabled，不可下单、前台不可见。生产代码 `internal/logic/legacymigrate/mapping.go` `SkuStatusForProduct`、`MapProduct`；关键测试 `TestSkuStatusForProduct`、`TestMapProductStatus`。

## Owner Decision

ACCEPTED

## Decision Evidence

Owner 于 2026-10-12 明确回复 `ACCEPT`，接受 Cleaner 已 CLEAN 的 immutable snapshot `04350e374224db9a6c8eb1573aa327c6d45c987f` 所承载的核心机制（CL-001、CL-002）。

适用范围：本任务 legacy-data-migration 的数据迁移实现（历史商品/分类/SKU/库存接入隔离测试环境）；不改变既有业务模块行为。后续若 `review.target` 之后生产代码或测试出现实质变化，本决定对新的实现快照不再自动有效，需重新 Review 与 Owner 决策。
