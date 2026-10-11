-- 优惠券 V1：coupon_templates（优惠券模板）+ user_coupons（用户已领取券实例）。
-- 说明：经 golang-migrate 机制新增（version 20261001000021）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- 金额均为整数分（INT UNSIGNED）；时间为 DATETIME。
-- user_id/template_id/order_id 均软引用（无 FK），沿用 users/categories 惯例，快照自足。
-- 模板「未开始/已结束领取」由 claim_start_time/claim_end_time 与 MySQL NOW() 派生，不设冗余终态；
-- 「停止新领取」用 status 显式表示（0=已停止、1=正常，不影响已领取券）。
-- user_coupons.status：10=可用、20=已预占、30=已使用（三态状态机，无「已释放」终态，释放=回到可用）。
-- uk_user_template(user_id, template_id) 兜底「同一用户同一模板至多一张」（领取唯一）；
-- idx_user_id 支撑「我的券」查询；idx_order_id 支撑「按订单释放券」。

CREATE TABLE coupon_templates (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name              VARCHAR(64)     NOT NULL,
  threshold_amount  INT UNSIGNED    NOT NULL,
  discount_amount   INT UNSIGNED    NOT NULL,
  total_quantity    INT UNSIGNED    NOT NULL,
  issued_quantity   INT UNSIGNED    NOT NULL DEFAULT 0,
  claim_start_time  DATETIME        NOT NULL,
  claim_end_time    DATETIME        NOT NULL,
  use_start_time    DATETIME        NOT NULL,
  use_end_time      DATETIME        NOT NULL,
  status            TINYINT         NOT NULL DEFAULT 1,
  created_at        DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at        DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE user_coupons (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id     BIGINT UNSIGNED NOT NULL,
  template_id BIGINT UNSIGNED NOT NULL,
  status      TINYINT         NOT NULL,
  order_id    BIGINT UNSIGNED NULL,
  received_at DATETIME        NOT NULL,
  locked_at   DATETIME        NULL,
  used_at     DATETIME        NULL,
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_template (user_id, template_id),
  KEY idx_user_id (user_id),
  KEY idx_order_id (order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
