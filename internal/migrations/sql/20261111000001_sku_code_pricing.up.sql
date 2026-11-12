-- SKU 规格组合定价 V1（B1-04-02）：为 skus 增加稳定业务编码 sku_code 与原价 original_price，
-- 并为 order_items 增加下单时的 SKU 编码与原价快照。
-- 说明：纯 additive 变更，不改既有列语义，不重算任何历史订单金额。
--
-- sku_code：SKU 的业务唯一编码（客户端下单按它定位 SKU）。列保持 NULL 以兼容两类写入：
--   - 历史行由本迁移回填为 SKU<id>（稳定、可读、与自增主键一一对应，删除后不重用）；
--   - 迁移工具/离线脚本仍可显式写入 NULL（唯一索引允许多个 NULL），在线业务写入一律生成编码。
--   唯一性由 uk_sku_code 兜底，禁止应用层「先查再写」作为唯一保证。
-- original_price：SKU 原价（整数分，划线价）。0 表示未设置，本迁移把历史行回填为当时售价，
--   避免历史 SKU 详情出现「原价 0」；在线创建时未提交则默认等于售价。
-- order_items.sku_code / original_price：下单快照，与 skus 解耦，SKU 后续改价/删除后订单仍自足。
--   spec_text（B1-03-02 已建）在同一链路写入规格文本快照，本迁移不重复加列。

ALTER TABLE skus
  ADD COLUMN sku_code       VARCHAR(64)  NULL         AFTER id,
  ADD COLUMN original_price INT UNSIGNED NOT NULL DEFAULT 0 AFTER price,
  ADD UNIQUE KEY uk_sku_code (sku_code);

-- 历史 SKU 回填：编码用 LPAD(id, 10, '0') 保证定长可读；原价未设置时回退售价（price=0 时保持 0）。
UPDATE skus SET sku_code = CONCAT('SKU', LPAD(id, 10, '0')) WHERE sku_code IS NULL;
UPDATE skus SET original_price = price WHERE original_price = 0 AND price > 0;

ALTER TABLE order_items
  ADD COLUMN sku_code       VARCHAR(64)  NOT NULL DEFAULT '' AFTER sku_id,
  ADD COLUMN original_price INT UNSIGNED NOT NULL DEFAULT 0  AFTER price;
