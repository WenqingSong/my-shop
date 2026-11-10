-- 订单状态流水 V1 回滚。
-- 破坏性说明：DROP order_status_logs 会永久丢失全部订单状态审计流水（不可恢复）；
--   订单主数据、金额、状态列与订单项均不受影响。回滚前必须同步下线状态流水的写入/查询逻辑。

DROP TABLE IF EXISTS order_status_logs;
