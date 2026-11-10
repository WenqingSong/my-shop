-- Mock 充值订单 V1 回滚。
-- 破坏性说明：DROP recharge_orders 会永久丢失全部充值订单（含已支付记录，不可恢复）；
--   钱包余额与 wallet_transactions 不受影响（不建 FK），但「充值单 → 入账流水」的业务追溯会缺失。
--   回滚前必须确认无待支付/处理中充值单，并下线充值入口。

DROP TABLE IF EXISTS recharge_orders;
