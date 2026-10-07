-- 秒杀 V4（故障恢复）：flash_sale_request_audits（人工修复审计，append-only）。
-- 说明：经 golang-migrate 机制新增（version 20261001000018）。
-- 记录管理员对异常请求（dead→queued）的修复动作，只追加、不可修改删除（无 UPDATE/DELETE 接口）。
-- request_id/operator_admin_id 为软引用（无 FK）：防止目标行被删除时级联破坏审计完整性；
-- operator_username 为操作时用户名快照，抗 admin 删除。

CREATE TABLE flash_sale_request_audits (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_id        BIGINT UNSIGNED NOT NULL,
  operator_admin_id BIGINT UNSIGNED NOT NULL,
  operator_username VARCHAR(64)     NOT NULL,
  action            VARCHAR(32)     NOT NULL,
  before_status     TINYINT         NOT NULL,
  after_status      TINYINT         NOT NULL,
  reason            VARCHAR(255)    NOT NULL,
  created_at        DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_request_id (request_id),
  KEY idx_operator (operator_admin_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
