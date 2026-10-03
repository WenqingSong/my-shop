-- 收货地址 V1：addresses（前台用户收货地址）。
-- 说明：经 golang-migrate 机制新增（version 紧随 inventory 20261001000004）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- 地区采用自由文本（province/city/district 三个 VARCHAR），不引入区划码表。
-- 默认地址唯一性由「生成列 default_key + uk_user_default 唯一索引」在 DB 层保证：
--   default_key = IF(is_default=1, user_id, NULL)，仅在默认行等于 user_id（非空），
--   非默认行为 NULL；MySQL 唯一索引允许多个 NULL，因此「每用户最多一条 is_default=1」被强约束，
--   并发下亦成立。default_key 为只读生成列，插入/更新不得写入该列。
-- 注意：default_key 采用 VIRTUAL（而非 STORED）生成列。原因是 MySQL 8.0 不允许
--   STORED 生成列引用「同时作为外键列」的 user_id（会报 1215 Cannot add foreign key constraint）；
--   改为 VIRTUAL 后可在保留外键的同时用唯一索引强约束默认唯一性，语义与不变量完全一致。
-- user_id 为数据归属锚点，idx_user_id 支撑按用户列表查询；
-- FK ON DELETE CASCADE 为防御性（当前无用户删除接口）。

CREATE TABLE addresses (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id        BIGINT UNSIGNED NOT NULL,
  recipient_name VARCHAR(32)     NOT NULL,
  phone          VARCHAR(20)     NOT NULL,
  province       VARCHAR(32)     NOT NULL,
  city           VARCHAR(32)     NOT NULL,
  district       VARCHAR(32)     NOT NULL,
  detail         VARCHAR(255)    NOT NULL,
  is_default     TINYINT         NOT NULL DEFAULT 0,
  default_key    BIGINT UNSIGNED GENERATED ALWAYS AS (IF(`is_default` = 1, `user_id`, NULL)) VIRTUAL,
  created_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_user_id (user_id),
  UNIQUE KEY uk_user_default (default_key),
  CONSTRAINT fk_addresses_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
