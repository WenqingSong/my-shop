-- 钱包 V1：wallet_accounts（用户钱包账户）+ wallet_transactions（资金流水，append-only）。
-- 说明：只建结构，不实现充值/余额支付 API；金额统一整数分（INT UNSIGNED），与订单/优惠券一致。
--
-- wallet_accounts：
--   uk_user_id 保证「一个用户至多一个钱包账户」；balance 为可用余额（非负由 UNSIGNED + 条件更新共同保证）。
--   user_id 为软引用（无 FK）：用户删除不得级联删除资金账户与流水（资金记录必须保留）。
-- wallet_transactions：
--   每次余额变更一行，记录变更前/后余额，可逐笔追溯；append-only，不提供 UPDATE/DELETE 业务路径。
--   direction：1=入账（credit，balance_after = before + amount）、2=出账（debit，balance_after = before - amount）；
--   amount 恒为正数，方向由 direction 表达。
--   uk_idempotency 保证「同一业务请求不重复入账」：写入方必须使用确定性、全局唯一的幂等键
--   （建议 `<biz_type>:<biz_id>` 或充值/订单请求 id），撞键 1062 由调用方判定为重复请求。
--   fk_wallet_transactions_account → wallet_accounts(id) ON DELETE RESTRICT：
--     账户不存在时写入直接失败（1452），不允许「钱包不存在却静默吞掉资金操作」；删除账户需先处理流水。
--   idx_biz 支撑按业务对象对账（biz_id 可空 = 无具体业务对象的手工调整）。

CREATE TABLE wallet_accounts (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    BIGINT UNSIGNED NOT NULL,
  balance    INT UNSIGNED    NOT NULL DEFAULT 0,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE wallet_transactions (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  account_id      BIGINT UNSIGNED NOT NULL,
  user_id         BIGINT UNSIGNED NOT NULL,
  tx_type         VARCHAR(32)     NOT NULL,
  direction       TINYINT         NOT NULL,
  amount          INT UNSIGNED    NOT NULL,
  balance_before  INT UNSIGNED    NOT NULL,
  balance_after   INT UNSIGNED    NOT NULL,
  biz_type        VARCHAR(32)     NOT NULL,
  biz_id          BIGINT UNSIGNED NULL,
  idempotency_key VARCHAR(64)     NOT NULL,
  remark          VARCHAR(255)    NOT NULL DEFAULT '',
  created_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_idempotency (idempotency_key),
  KEY idx_account_id (account_id, id),
  KEY idx_user_id (user_id, id),
  KEY idx_biz (biz_type, biz_id),
  CONSTRAINT fk_wallet_transactions_account FOREIGN KEY (account_id) REFERENCES wallet_accounts(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
