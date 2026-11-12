-- 回滚 SKU 规格组合定价 V1：只移除本迁移新增的列与索引，不影响既有 skus/order_items 行。

ALTER TABLE order_items
  DROP COLUMN original_price,
  DROP COLUMN sku_code;

ALTER TABLE skus
  DROP INDEX uk_sku_code,
  DROP COLUMN original_price,
  DROP COLUMN sku_code;
