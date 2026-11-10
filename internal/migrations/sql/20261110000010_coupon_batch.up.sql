-- 优惠券批次与适用范围 V1：在既有「模板 + 用户券」之上补齐发行批次、适用范围、折扣类型与每人限领。
-- 说明：纯 additive + 一次唯一键替换，不修改既有数据、不改变既有券状态机与核销流程。
--
-- 兼容策略（关键）：
--   1) coupon_templates 仍是券权益的唯一事实来源（门槛/减免/使用有效期/停止领取）；新增列全有默认值，
--      既有 AdminCreate 插入语句无需改动即可继续工作。
--   2) 模板级 total_quantity/issued_quantity 语义不变，仍是全局发放上限；批次只是可选的第 2 层闸门，
--      从批次领取时同时受「模板闸门 + 批次闸门」约束（模板计数照常 +1），历史模板无批次时行为完全不变。
--   3) user_coupons.batch_id 可空：NULL = 历史券或不经批次的直接发放；历史行不回填、不伪造批次。
--   4) claim_no = 该用户在该模板下的第几次领取（从 1 开始）。既有 uk_user_template 被替换为
--      uk_user_template_claim(user_id, template_id, claim_no)：历史行 claim_no 默认 1，
--      「同一用户同一模板默认只能领一次」的既有语义与 1062 冲突行为保持不变，同时为 per_user_limit > 1
--      提供数据库层承载。默认 1 也保证既有 Claim 插入路径无需改动即可继续正确去重。
--
-- discount_type：1=固定金额满减（当前唯一实现），2=折扣率（结构预留，discount_rate_bp 为万分比，
--   例如 9000 = 9 折）。type=1 使用 discount_amount；type=2 使用 discount_rate_bp，discount_amount 应为 0。
--   以应用层校验保证两者一致，不引入 CHECK（与项目既有迁移约定一致）。
-- per_user_limit：每人限领数量，0 = 不限；默认 1 与既有 uk_user_template 语义一致。
-- coupon_template_scopes：无行 = 平台通用券；有行 = 仅限所列商品/分类。product_id 与 category_id
--   恰有一个非空（应用层保证）；两个唯一键分别保证「同模板同一商品/分类至多一条范围」。
-- batch_id 为软引用（无 FK）：批次删除/合并不得连带删除用户已领取的历史券。

ALTER TABLE coupon_templates
  ADD COLUMN discount_type   TINYINT      NOT NULL DEFAULT 1 AFTER discount_amount,
  ADD COLUMN discount_rate_bp INT UNSIGNED NULL AFTER discount_type,
  ADD COLUMN per_user_limit  INT UNSIGNED NOT NULL DEFAULT 1 AFTER total_quantity;

CREATE TABLE coupon_batches (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  template_id       BIGINT UNSIGNED NOT NULL,
  total_quantity    INT UNSIGNED    NOT NULL,
  issued_quantity   INT UNSIGNED    NOT NULL DEFAULT 0,
  claim_start_time  DATETIME        NOT NULL,
  claim_end_time    DATETIME        NOT NULL,
  status            TINYINT         NOT NULL DEFAULT 1,
  created_at        DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at        DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_template_status (template_id, status),
  CONSTRAINT fk_coupon_batches_template FOREIGN KEY (template_id) REFERENCES coupon_templates(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE coupon_template_scopes (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  template_id BIGINT UNSIGNED NOT NULL,
  product_id  BIGINT UNSIGNED NULL,
  category_id BIGINT UNSIGNED NULL,
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_template_product (template_id, product_id),
  UNIQUE KEY uk_template_category (template_id, category_id),
  KEY idx_product_id (product_id),
  KEY idx_category_id (category_id),
  CONSTRAINT fk_coupon_scopes_template FOREIGN KEY (template_id) REFERENCES coupon_templates(id) ON DELETE CASCADE,
  CONSTRAINT fk_coupon_scopes_product FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE,
  CONSTRAINT fk_coupon_scopes_category FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE user_coupons
  ADD COLUMN batch_id BIGINT UNSIGNED NULL AFTER template_id,
  ADD COLUMN claim_no INT UNSIGNED NOT NULL DEFAULT 1 AFTER batch_id,
  DROP INDEX uk_user_template,
  ADD UNIQUE KEY uk_user_template_claim (user_id, template_id, claim_no),
  ADD KEY idx_batch_id (batch_id);
