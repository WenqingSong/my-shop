-- 积分 V1 回滚。
-- 破坏性说明：先 DROP user_points_logs 再 DROP user_points_accounts，会永久丢失全部积分余额与积分流水
--   （不可恢复）。回滚前必须确认无进行中的积分抵扣/返还，并下线积分读写。

DROP TABLE IF EXISTS user_points_logs;
DROP TABLE IF EXISTS user_points_accounts;
