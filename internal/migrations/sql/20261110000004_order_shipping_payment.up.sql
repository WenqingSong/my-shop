-- 交易基础模型 V1：orders 增加运费/支付/履约内嵌字段，order_items 增加 SKU 规格快照。
-- 说明：纯 additive 变更，不改既有列类型与语义，不重算任何历史金额。
-- 金额单位沿用项目约定：整数分（INT UNSIGNED），不使用 decimal/浮点。
--
-- shipping_fee：订单运费快照（整数分）。历史订单与当前阶段默认 0；
--   total_amount 保持既有语义与已存值，不因新增该列而重新计算（历史订单 total_amount 不重算）。
--   B1-04 引入非零运费时，应付金额公式扩展为 total = original - discount + shipping，需同步设计文档与测试。
-- pay_type / transaction_no：Mock 支付的支付方式与交易编号（当前阶段为空串；不新增独立支付状态列/支付表）。
--   订单是否已支付仍以 orders.status（10..70）与 paid_at 为准，不引入第二套支付状态机。
-- delivery_type / delivery_name / delivery_sn：履约信息内嵌（物流方式/公司/运单号），默认空串；
--   不引入物流子表或外部物流服务，发货仍由既有 status=30 状态推进。
-- order_items.spec_text：下单时的 SKU 规格文本快照（默认空串），历史订单不回填；
--   与 skus.spec_signature / sku_spec_values 解耦，订单快照自足（SKU 或规格被删除后仍可读）。

ALTER TABLE orders
  ADD COLUMN shipping_fee  INT UNSIGNED NOT NULL DEFAULT 0  AFTER discount_amount,
  ADD COLUMN pay_type      VARCHAR(16)  NOT NULL DEFAULT '' AFTER user_coupon_id,
  ADD COLUMN transaction_no VARCHAR(64) NOT NULL DEFAULT '' AFTER pay_type,
  ADD COLUMN delivery_type VARCHAR(32)  NOT NULL DEFAULT '' AFTER refunded_at,
  ADD COLUMN delivery_name VARCHAR(64)  NOT NULL DEFAULT '' AFTER delivery_type,
  ADD COLUMN delivery_sn   VARCHAR(64)  NOT NULL DEFAULT '' AFTER delivery_name;

ALTER TABLE order_items
  ADD COLUMN spec_text VARCHAR(128) NOT NULL DEFAULT '' AFTER sku_name;
