-- 回滚 orders.member_discount_amount（只删除本迁移新增的列，不动既有金额列与历史数据）。
-- 回滚前提：应用侧已不再读写该列。历史订单的 total_amount 不会因此变化，因为该列只在
-- B1-04-08 之后的新订单上可能非 0；回滚后这些订单的会员折扣金额快照丢失，属于预期。

ALTER TABLE orders
  DROP COLUMN member_discount_amount;
