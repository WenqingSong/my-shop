-- 优惠券权益快照 V1 回滚。
-- 破坏性说明：DROP 四个快照列会永久丢失已领取券的权益快照；历史券（NULL）不受影响。
--   回滚后读取方必须恢复为「直接读模板当前值」的旧逻辑，否则查询会因未知列失败。
--   券状态、归属、订单绑定与核销记录均保留。

ALTER TABLE user_coupons
  DROP COLUMN end_time,
  DROP COLUMN use_min_price,
  DROP COLUMN coupon_price,
  DROP COLUMN coupon_title;
