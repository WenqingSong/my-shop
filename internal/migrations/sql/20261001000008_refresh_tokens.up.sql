-- IAM V4：refresh_tokens（前台 refresh token 哈希 + Token Family 血缘）。
-- 说明：经 golang-migrate 机制新增（version 20261001000008，紧随 orders 20261001000007）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- 仅存 SHA-256 哈希（token_hash），不存明文；family_id + parent_id + generation 表示血缘；
-- sid 为「当前 session → 对应 refresh family」的追踪键（family 内所有行同值）。
-- revoked_reason：'rotated'（被轮换）/ 'revoked'（被主动撤销），NULL=有效。
-- user_id/sid 均软引用（不建 FK）；uk_token_hash 唯一索引兜底并发轮换（条件 UPDATE 行锁）。

CREATE TABLE refresh_tokens (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  token_hash     CHAR(64)        NOT NULL,
  family_id      VARCHAR(32)     NOT NULL,
  user_id        BIGINT UNSIGNED NOT NULL,
  parent_id      BIGINT UNSIGNED NULL,
  generation     INT             NOT NULL DEFAULT 0,
  sid            VARCHAR(32)     NOT NULL,
  expires_at     DATETIME        NOT NULL,
  revoked_at     DATETIME        NULL,
  revoked_reason VARCHAR(16)     NULL,
  created_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_token_hash (token_hash),
  KEY idx_family (family_id),
  KEY idx_user (user_id),
  KEY idx_sid (sid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
