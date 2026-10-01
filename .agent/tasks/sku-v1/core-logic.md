# Core Logic

> 仅 `CLEAN` 后填写。以下机制决定数据一致性/可见性安全，Owner 应亲自理解并验证。

## CL-001：同商品 `name` 唯一 + 并发兜底（INV-008）

- Owner 需要理解：同一商品下 SKU 名称必须唯一，否则下单/展示会歧义。应用层「先查再写」存在并发窗口，真正兜底是 `(product_id, name)` 复合唯一约束；撞名必须返回稳定 409/`5004`（而非 500），并发同名创建最终至多一行。若映射错误或唯一约束缺失，会出现重复 SKU 或错误 500。
- 生产代码：`internal/migrations/sql/20261001000003_skus.up.sql:19`（`UNIQUE KEY uk_product_name (product_id, name)`）；`internal/logic/sku/sku.go:79-90`（创建 `InsertAndGetId` + `isDuplicateKeyError` → `CodeSkuNameExists`）、`internal/logic/sku/sku.go:273-277`（`isDuplicateKeyError` 识别 MySQL 1062）。
- 关键测试：
  - `internal/controller/sku/sku_test.go::TestSkuNameUnique`（同商品撞名 409/5004 且无新行；不同商品同名可共存）
  - `internal/controller/sku/sku_test.go::TestSkuConcurrentCreateSameName`（10 goroutine 并发同名，恰好 1 成功 + 9 冲突，最终 1 行）
- 基线验证：`go test ./internal/controller/sku/... -run 'TestSkuNameUnique|TestSkuConcurrentCreateSameName' -p 1 -count=1`，预期通过。
- 可选 Mutation：将 `internal/logic/sku/sku.go` 的 `isDuplicateKeyError` 改为恒 `return false`（模拟「未识别唯一冲突」）。
- 预期失败：`TestSkuNameUnique` 与 `TestSkuConcurrentCreateSameName` 必然失败——撞名返回 500 而非 5004、并发场景出现非 5004 的意外 code。
- 恢复确认：还原 `isDuplicateKeyError` 后重新运行上述命令，通过。

## CL-002：前台可见性隔离（INV-007）

- Owner 需要理解：前台商品详情只对 `on_shelf` 商品返回 SKU，且只返回 `enabled` SKU；后台详情返回全部状态。若前台把 `onlyEnabled` 传成 `false`，停用 SKU 会泄漏给未认证前台，造成越权可见性。此规则是权限/可见性边界，非样式问题。
- 生产代码：`internal/logic/product/product.go:97-108`（前台 `Detail` 调 `service.Sku().ListByProduct(ctx, id, true)`，`true`=仅 enabled）；`internal/logic/sku/sku.go:164-178`（`ListByProduct` 的 `onlyEnabled` 分支 `Where("status", statusEnabled)`）。
- 关键测试：`internal/controller/sku/sku_test.go::TestSkuDetailVisibility`（上架商品前台详情仅 1 个 enabled SKU；后台详情含 disabled 共 2 个；draft 商品前台 404 不暴露 SKU）。
- 基线验证：`go test ./internal/controller/sku/... -run TestSkuDetailVisibility -p 1 -count=1`，预期通过。
- 可选 Mutation：将 `internal/logic/product/product.go` 前台 `Detail` 中 `ListByProduct(ctx, id, true)` 改为 `false`。
- 预期失败：`TestSkuDetailVisibility` 必然失败——前台详情 `fd.Skus` 会包含 disabled SKU（数量 2 而非 1），断言 `len(fd.Skus) != 1` 触发。
- 恢复确认：还原 `true` 后重新运行上述命令，通过。
