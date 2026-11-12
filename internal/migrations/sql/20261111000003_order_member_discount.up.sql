-- 会员折扣金额快照列 V1（B1-04-08）：orders 增 member_discount_amount。
--
-- 为什么需要独立列（不能复用 discount_amount）：
--   orders.discount_amount 已被优惠券占用（见 docs/design/coupon.md、pricing.md §4），语义是「券优惠」；
--   会员等级折扣是另一项独立优惠（pricing.md §5 明确要求「折扣结果写入订单独立快照列，不得直接改写
--   discount_amount 的券语义」）。因此新增 member_discount_amount 承载会员折扣金额快照。
--
-- 金额语义（整数分，与全项目一致）：
--   original_amount       = Σ(order_items.price × quantity)     商品金额
--   discount_amount       = 优惠券优惠（既有列，语义不变）
--   member_discount_amount= 会员等级折扣（本列，默认 0）
--   total_amount          = original_amount - discount_amount - member_discount_amount + shipping_fee
--   约束：discount_amount + member_discount_amount ≤ original_amount，total_amount ≥ 0（应用层保证）。
--
-- 历史兼容（INV-P05 历史不伪造）：
--   默认 0 且不回填。迁移前的订单 member_discount_amount = 0，因此
--   total_amount = original_amount - discount_amount 仍成立，无需重算任何历史金额。
--
-- 为什么不在本迁移里新增 member_level_id / member_rate_bp：
--   本任务只要求「折扣参与一次算价并快照金额」。等级标识可由 user_memberships + 折扣金额反查，
--   不属于订单金额守恒的必需字段；为缩小与 B1-04-03（订单金额明细）迁移的冲突面，只加一列。

ALTER TABLE orders
  ADD COLUMN member_discount_amount INT UNSIGNED NOT NULL DEFAULT 0 AFTER discount_amount;
