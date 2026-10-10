-- IAM V5（用户账号状态管理）：users 增 status/auth_epoch、refresh_tokens 增 auth_epoch、
-- 新增 user_status_audits（append-only 审计）。
-- 说明：经 golang-migrate 机制新增（version 20261001000019，紧随 flash_sale_request_audits 20261001000018）。
-- status 沿用 admins.status 的 TINYINT 语义（1=启用、0=禁用），旧用户升级后默认启用；
-- auth_epoch 为每用户持久化认证版本（仅在实际「启用→禁用」迁移时 +1），
-- 绑定会话 Hash 与 refresh token，用于「禁用即时失效」与「重新启用不复活旧凭证」。
-- refresh_tokens.auth_epoch 随 family 血缘继承（轮换沿用父行值，不重读 users.auth_epoch）。
-- user_status_audits 仅记录实际状态迁移（幂等 no-op 不写审计），软引用无 FK（防目标行删除破坏审计）。

ALTER TABLE users
  ADD COLUMN status     TINYINT         NOT NULL DEFAULT 1,
  ADD COLUMN auth_epoch BIGINT UNSIGNED NOT NULL DEFAULT 0;

ALTER TABLE refresh_tokens
  ADD COLUMN auth_epoch BIGINT UNSIGNED NOT NULL DEFAULT 0;

CREATE TABLE user_status_audits (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  target_user_id    BIGINT UNSIGNED NOT NULL,
  operator_admin_id BIGINT UNSIGNED NOT NULL,
  operator_username VARCHAR(64)     NOT NULL,
  action            VARCHAR(16)     NOT NULL,
  before_status     TINYINT         NOT NULL,
  after_status      TINYINT         NOT NULL,
  reason            VARCHAR(255)    NOT NULL,
  result            TINYINT         NOT NULL DEFAULT 1,
  created_at        DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_target_user (target_user_id),
  KEY idx_operator (operator_admin_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
