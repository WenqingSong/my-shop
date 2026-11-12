-- 钱包 V1 回滚。
-- 破坏性说明：先 DROP wallet_transactions 再 DROP wallet_accounts，会永久丢失全部钱包余额与资金流水
--   （不可恢复，且无法再从已支付订单反推）。回滚前必须确认无未完成的充值/支付/退款对账，并下线钱包读写。
--   订单、支付与退款表不引用钱包，回滚不会级联修改订单数据。

DROP TABLE IF EXISTS wallet_transactions;
DROP TABLE IF EXISTS wallet_accounts;
