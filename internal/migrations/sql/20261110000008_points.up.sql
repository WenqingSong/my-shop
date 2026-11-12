-- 积分 V1：user_points_accounts（用户积分账户）+ user_points_logs（积分流水，append-only）。
-- 说明：只建结构，不实现签到/消费返积分/积分抵扣/积分商城；积分使用整数（BIGINT），不使用小数。
--
-- user_points_accounts：
--   uk_user_id 保证一个用户一个积分账户；points 为当前积分，UNSIGNED 禁止负值（扣减越界时 SQL 报错回滚）。
--   user_id 软引用（无 FK）：积分记录不随用户删除级联删除。
-- user_points_logs：
--   每次积分变动一行，points_before/points_after 供逐笔核对；append-only，不提供 UPDATE/DELETE 业务路径。
--   change_amount 为带符号变动值（正=增加、负=扣减），与 points_before/after 满足
--   points_after = points_before + change_amount；扣减不得使 points_after 为负（应用条件更新 + UNSIGNED 兜底）。
--   uk_idempotency 保证「同一业务事件不重复加分/扣分」：写入方使用确定性唯一幂等键
--   （建议 `<change_type>:<biz_type>:<biz_id>`）；撞键 1062 视为重复事件。
--   change_type 为语义类型（sign_in/order_reward/order_deduct/refund_return/admin_adjust 等），
--   biz_type/biz_id 指向关联业务对象（biz_id 可空 = 无具体业务对象）。
--   idx_user_time 支撑按用户积分明细（时间线）查询；idx_biz 支撑按业务对象对账。

CREATE TABLE user_points_accounts (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    BIGINT UNSIGNED NOT NULL,
  points     BIGINT UNSIGNED NOT NULL DEFAULT 0,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE user_points_logs (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id         BIGINT UNSIGNED NOT NULL,
  change_amount   BIGINT          NOT NULL,
  points_before   BIGINT UNSIGNED NOT NULL,
  points_after    BIGINT UNSIGNED NOT NULL,
  change_type     VARCHAR(32)     NOT NULL,
  biz_type        VARCHAR(32)     NOT NULL,
  biz_id          BIGINT UNSIGNED NULL,
  idempotency_key VARCHAR(64)     NOT NULL,
  remark          VARCHAR(255)    NOT NULL DEFAULT '',
  created_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_idempotency (idempotency_key),
  KEY idx_user_time (user_id, created_at),
  KEY idx_biz (biz_type, biz_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
