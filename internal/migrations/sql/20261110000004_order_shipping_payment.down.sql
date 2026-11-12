-- 交易基础模型 V1 回滚。
-- 破坏性说明：DROP 以下列会永久丢失已写入的运费、支付方式/交易编号、履约信息与订单项规格文本；
--   既有金额列（total_amount/original_amount/discount_amount）与订单/订单项行本身不受影响。
--   回滚前必须先下线读取这些列的查询与发货/支付逻辑，否则会直接报「未知列」。

ALTER TABLE order_items
  DROP COLUMN spec_text;

ALTER TABLE orders
  DROP COLUMN delivery_sn,
  DROP COLUMN delivery_name,
  DROP COLUMN delivery_type,
  DROP COLUMN transaction_no,
  DROP COLUMN pay_type,
  DROP COLUMN shipping_fee;
