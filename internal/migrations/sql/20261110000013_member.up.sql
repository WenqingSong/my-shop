-- 会员等级基础模型 V1：member_levels（等级配置）+ user_memberships（用户会员信息）+ member_growth_logs（成长值流水）。
-- 说明：只建结构，不实现等级计算/升级/会员折扣；不重复创建积分账本，不修改 user_points_accounts / user_points_logs。
--
-- 职责边界（重要）：
--   1) 积分（user_points_accounts/user_points_logs，B1-03-02）回答「可用积分余额与逐笔增减」；
--      成长值（本迁移）回答「会员等级晋升所依据的累计值」。二者是不同账本、不同语义，本迁移不做换算、
--      不共用余额列、不共用流水表；会员折扣也不得塞进 coupons.discount_amount 的券语义。
--   2) member_levels 只描述等级配置：等级序号、成长值门槛、折扣率、启用状态，不承载用户数据。
--   3) user_memberships 一人一行（uk_user_id），保存当前等级与累计成长值；等级变更必须与成长值流水同事务。
--   4) member_growth_logs 与 user_points_logs 同构（append-only、前后值、幂等键），但独立存储。
--
-- 数值约定：
--   growth_threshold / growth_value / change_amount：整数成长值，非负余额（UNSIGNED），不做小数。
--   discount_rate_bp：会员折扣率，万分比（10000=不打折/原价，9000=9 折，8800=8.8 折）；
--     INT UNSIGNED 保存，范围与「1..10000」由应用层校验，不引入 CHECK（与项目既有迁移约定一致）。
--   level_no：等级序号（1、2、3…），唯一；growth_threshold 唯一，避免两个等级共用同一门槛导致升级歧义。
--   upgraded_at：最近一次等级变更时间；NULL = 尚未发生过升级（初始等级/建档行）。
--
-- 升级关系（B1-04 实现时）：
--   growth_value >= 目标等级 growth_threshold 且目标等级 status=1 才可升级；单事务内
--   写 member_growth_logs（幂等键）+ 条件更新 user_memberships（growth_value/level_id/upgraded_at）。
--   降级、成长值过期、等级有效期均未建模（Deferred）。

CREATE TABLE member_levels (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  level_no         INT UNSIGNED    NOT NULL,
  name             VARCHAR(64)     NOT NULL,
  growth_threshold BIGINT UNSIGNED NOT NULL DEFAULT 0,
  discount_rate_bp INT UNSIGNED    NOT NULL DEFAULT 10000,
  status           TINYINT         NOT NULL DEFAULT 1,
  created_at       DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_level_no (level_no),
  UNIQUE KEY uk_growth_threshold (growth_threshold)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE user_memberships (
  id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id      BIGINT UNSIGNED NOT NULL,
  level_id     BIGINT UNSIGNED NOT NULL,
  growth_value BIGINT UNSIGNED NOT NULL DEFAULT 0,
  upgraded_at  DATETIME        NULL,
  created_at   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_id (user_id),
  KEY idx_level_id (level_id),
  CONSTRAINT fk_user_memberships_level FOREIGN KEY (level_id) REFERENCES member_levels(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE member_growth_logs (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id         BIGINT UNSIGNED NOT NULL,
  change_amount   BIGINT          NOT NULL,
  growth_before   BIGINT UNSIGNED NOT NULL,
  growth_after    BIGINT UNSIGNED NOT NULL,
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
