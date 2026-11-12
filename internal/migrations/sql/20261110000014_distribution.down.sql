-- 回滚一级分销基础模型 V1。
-- 破坏性说明（数据丢失）：distribution_commissions / user_referrals 两张本次新增表整体删除，
-- 推荐关系与佣金记录全部丢失；不触碰 orders、wallet_*、refund_* 与任何既有表。
-- 删除顺序：佣金记录（FK→orders）先删，再删推荐关系（无 FK）。

DROP TABLE distribution_commissions;
DROP TABLE user_referrals;
