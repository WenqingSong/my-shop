-- Mock 充值订单 V1：recharge_orders。
-- 说明：只建结构，不实现充值/支付 API；不接入微信/支付宝等真实渠道。
-- 金额为整数分（INT UNSIGNED）；充值订单与钱包账户分离，入账通过 wallet_transactions 完成。
-- 状态语义：10=待支付、20=支付成功、30=已取消、40=支付失败；仅 10→20 会触发钱包入账，
--   入账幂等由 wallet_transactions.uk_idempotency（建议 `recharge:<recharge_order_id>`）保证，
--   「同一充值订单不重复入账」由唯一幂等键 + 同事务共同保证（本任务不写业务逻辑）。
-- order_no 全局唯一；pay_type 为支付方式文本（当前阶段仅 Mock，如 `mock`），空串表示尚未支付/未选择；
-- paid_at 仅在 20 状态写入。
-- user_id 软引用（无 FK）：资金类记录不随用户删除级联删除；idx_user_status 支撑「我的充值单/状态筛选」。

CREATE TABLE recharge_orders (
  id        BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_no  VARCHAR(32)     NOT NULL,
  user_id   BIGINT UNSIGNED NOT NULL,
  amount    INT UNSIGNED    NOT NULL,
  status    TINYINT         NOT NULL DEFAULT 10,
  pay_type  VARCHAR(16)     NOT NULL DEFAULT '',
  paid_at   DATETIME        NULL,
  created_at DATETIME       NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME       NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_order_no (order_no),
  KEY idx_user_status (user_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
