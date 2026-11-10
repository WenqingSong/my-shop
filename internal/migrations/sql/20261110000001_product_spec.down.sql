-- 商品规格 V1 回滚。
-- 破坏性说明：DROP 规格三表会永久丢失全部规格组、规格值与 SKU↔规格值关联；
--   skus.image_url / skus.spec_signature 一并丢弃，历史 SKU 行本身（id/name/price/status）不受影响。
--   回滚后代码必须同步下线规格读写，否则查询 skus.image_url 等列会直接报错。
-- 顺序：先删子表再删被引用表，最后移除 skus 的新列与唯一索引。

DROP TABLE IF EXISTS sku_spec_values;
DROP TABLE IF EXISTS product_spec_values;
DROP TABLE IF EXISTS product_specs;

ALTER TABLE skus
  DROP INDEX uk_product_signature,
  DROP COLUMN spec_signature,
  DROP COLUMN image_url;
