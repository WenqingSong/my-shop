-- 回滚推荐关系图全局变更锁表（B1-04-FIX-01）。
--
-- 只删除本迁移新增的锁表，不动 user_referrals / distribution_commissions 与任何业务数据。
-- 回滚前提：应用侧已回退到不使用该锁的版本；否则绑定会因锁记录缺失而 fail-closed（拒绝写入），
-- 这是预期行为（宁可拒绝绑定，也不在无串行化保障下写关系图）。

DROP TABLE IF EXISTS referral_graph_guard;
