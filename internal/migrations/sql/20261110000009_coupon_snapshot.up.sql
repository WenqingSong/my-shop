-- 优惠券权益快照 V1：user_coupons 增加领取时的权益快照列。
-- 说明：纯 additive；不改现有领取/预占/核销/释放流程，不新增批次/适用范围（属 B1-03-03）。
-- 目的：用户领券后，模板后续改名/改面额/改门槛/改有效期不影响已领取券的权益展示与使用判断。
--
-- 快照列全部允许 NULL：
--   NULL = 该券是在本迁移之前领取的历史券，未做快照（不伪造历史快照，不回填）；
--   读取方遇到 NULL 必须回退 coupon_templates 的当前值（历史券行为与迁移前完全一致）。
--   新领取的券（后续业务实现）应在领取事务内写入四项快照，写后不再随模板变化。
-- coupon_title：模板名称快照；coupon_price：减免金额快照（整数分）；use_min_price：使用门槛快照（整数分）；
--   end_time：领取时确定的券到期时间快照（对应模板 use_end_time，DATETIME）。
-- 与 orders.user_coupon_id / uk_user_template / idx_order_id 等既有约束无关，不改变任何唯一性语义。

ALTER TABLE user_coupons
  ADD COLUMN coupon_title  VARCHAR(64)  NULL AFTER template_id,
  ADD COLUMN coupon_price  INT UNSIGNED NULL AFTER coupon_title,
  ADD COLUMN use_min_price INT UNSIGNED NULL AFTER coupon_price,
  ADD COLUMN end_time      DATETIME     NULL AFTER use_min_price;
