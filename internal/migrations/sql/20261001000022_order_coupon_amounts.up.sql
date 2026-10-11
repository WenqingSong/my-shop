-- 优惠券 V1：orders 增三列（金额三列快照 + 用户券引用）。
-- 说明：经 golang-migrate 机制新增（version 20261001000022）。
-- original_amount：优惠前金额快照（= Σ order_items.price×quantity）；discount_amount：优惠金额快照（无券为 0）；
-- total_amount 语义不变，仍为应付/成交金额，恒等于 original_amount - discount_amount。
-- user_coupon_id：下单时使用的用户券 id（软引用，快照，下单时写入、不再随释放改变）。
-- 历史订单回填 original_amount = total_amount、discount_amount = 0，total_amount 不变（兼容 AC-010/AC-011）。

ALTER TABLE orders
  ADD COLUMN original_amount INT UNSIGNED NOT NULL DEFAULT 0 AFTER total_amount,
  ADD COLUMN discount_amount INT UNSIGNED NOT NULL DEFAULT 0 AFTER original_amount,
  ADD COLUMN user_coupon_id BIGINT UNSIGNED NULL AFTER discount_amount;

UPDATE orders SET original_amount = total_amount WHERE original_amount = 0;
