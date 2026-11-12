-- 回滚会员等级基础模型 V1。
-- 破坏性说明（数据丢失）：member_growth_logs / user_memberships / member_levels 三张本次新增表整体删除，
-- 等级配置、用户会员信息与成长值流水全部丢失；不触碰 user_points_accounts / user_points_logs（积分账本独立保留）。
-- 删除顺序必须先子后父：growth_logs（无 FK）→ memberships（FK→levels）→ levels。

DROP TABLE member_growth_logs;
DROP TABLE user_memberships;
DROP TABLE member_levels;
