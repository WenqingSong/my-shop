-- 退款闭环 V1：refund_requests（退款申请主数据）+ refund_audits（退款审计，append-only）。
-- 说明：经 golang-migrate 机制新增（version 紧随 user_status 20261001000019）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- 金额均为整数分（INT UNSIGNED）；状态为 TINYINT（见 docs/design/refund.md 状态机：
-- 10 申请中 / 20 待退款 / 30 退款失败 / 40 审核拒绝 / 50 已撤销 / 60 退款成功 / 70 已关闭）。
-- order_id/user_id 采用软引用（无 FK）：退款快照自足，订单删除后仍可读。
--
-- 「至多一条有效申请」约束：生成列 active_key = (status IN (10,20,30) ? order_id : NULL)，
-- UNIQUE(active_key)（MySQL 唯一索引允许多个 NULL）机械保证「同一订单至多一条有效（非拒绝/
-- 非撤销/非关闭/非成功）申请」，允许拒绝/撤销/关闭后再次申请。
-- refund_audits 只追加、不可修改删除（无 UPDATE/DELETE 接口）；operator_type：1=user、2=admin。

CREATE TABLE refund_requests (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id       BIGINT UNSIGNED NOT NULL,
  user_id        BIGINT UNSIGNED NOT NULL,
  status         TINYINT         NOT NULL DEFAULT 10,
  amount         INT UNSIGNED    NOT NULL,
  reason         VARCHAR(255)    NOT NULL DEFAULT '',
  review_reason  VARCHAR(255)    NOT NULL DEFAULT '',
  reviewed_by    BIGINT UNSIGNED NULL,
  reviewed_at    DATETIME        NULL,
  execute_reason VARCHAR(255)    NOT NULL DEFAULT '',
  executed_by    BIGINT UNSIGNED NULL,
  executed_at    DATETIME        NULL,
  active_key     BIGINT UNSIGNED GENERATED ALWAYS AS (CASE WHEN status IN (10,20,30) THEN order_id ELSE NULL END) STORED,
  created_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_order_id (order_id),
  KEY idx_user_id (user_id),
  UNIQUE KEY uk_active (active_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE refund_audits (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  refund_id         BIGINT UNSIGNED NOT NULL,
  action            VARCHAR(32)     NOT NULL,
  operator_type     TINYINT         NOT NULL,
  operator_id       BIGINT UNSIGNED NOT NULL,
  operator_username VARCHAR(64)     NOT NULL,
  before_status     TINYINT         NOT NULL,
  after_status      TINYINT         NOT NULL,
  reason            VARCHAR(255)    NOT NULL DEFAULT '',
  created_at        DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_refund_id (refund_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
