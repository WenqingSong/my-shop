-- 回滚优惠券批次与适用范围 V1。
-- 破坏性说明（数据丢失）：
--   1) 必须 RESTORE 既有唯一键 uk_user_template(user_id, template_id)，而它无法容纳同一用户同一模板的
--      第 2..N 张券（claim_no > 1）。因此本 down 会先删除这些多领记录；只保留 claim_no = 1 的首张券。
--      这是「回到旧 schema」不可回避的数据损失，只允许在测试库/确认可丢弃多领数据的场景执行。
--   2) coupon_batches / coupon_template_scopes 为本次新增表，整体删除（批次与范围配置丢失）。
--   3) coupon_templates 三个新增列、user_coupons 两个新增列整体删除。
-- 既有订单、用户券（首张）、积分、钱包、秒杀表不受影响。

DELETE FROM user_coupons WHERE claim_no > 1;

ALTER TABLE user_coupons
  DROP INDEX uk_user_template_claim,
  DROP INDEX idx_batch_id,
  DROP COLUMN claim_no,
  DROP COLUMN batch_id,
  ADD UNIQUE KEY uk_user_template (user_id, template_id);

DROP TABLE coupon_template_scopes;
DROP TABLE coupon_batches;

ALTER TABLE coupon_templates
  DROP COLUMN per_user_limit,
  DROP COLUMN discount_rate_bp,
  DROP COLUMN discount_type;
