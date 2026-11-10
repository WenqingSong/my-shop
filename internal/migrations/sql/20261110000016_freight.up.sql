-- 运费模板 V1：freight_templates（模板）+ freight_template_rules（区域计费规则），
-- 并为 products 增加可空 freight_template_id 绑定列。
--
-- 金额单位沿用项目约定：整数分（INT UNSIGNED），不使用 decimal/浮点。
-- 本批次只建模「按件计费 + 区域差异化 + 满额免邮」，不建模重量计费/体积计费/第三方物流轨迹。
--
-- freight_templates：
--   is_free=1 表示模板级无条件包邮（跳过区域规则匹配）；free_threshold 为满额免邮阈值（整数分，0=不启用）。
--   status 1=启用、0=停用；停用模板不允许被下单计价使用（应用层返回明确错误，不静默按 0 运费）。
--   模板名唯一，避免后台同名模板歧义。
--
-- freight_template_rules：
--   region_id 指向 regions.id（省/市/区任一层级），一个模板对同一 region_id 只允许一条规则，
--   由 uk_template_region 强约束「区域规则不可重复配置」。
--   first_quantity/first_fee 为首件数量与首件费用；additional_quantity/additional_fee 为续件数量与续件费用。
--   free_threshold 为该区域覆盖的满额免邮阈值（0=继承模板值），便于对偏远地区设置更高门槛。
--   规则归属模板，模板删除时级联删除（规则无独立生命周期）。
--
-- products.freight_template_id：
--   可空。NULL = 历史商品/未指定模板，保持既有零运费兼容行为（迁移不回填）；
--   非 NULL = 下单计价必须按该模板计算，模板不存在/停用/无匹配区域规则时明确报错，不得静默免费。
--   不建外键：模板与本列由应用层校验，避免模板删除导致商品行被级联修改或阻塞。
--   绑定入口由商品管理侧写入该列（本批次不新增商品写接口）。

CREATE TABLE freight_templates (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name           VARCHAR(64)     NOT NULL,
  is_free        TINYINT         NOT NULL DEFAULT 0,
  free_threshold INT UNSIGNED    NOT NULL DEFAULT 0,
  status         TINYINT         NOT NULL DEFAULT 1,
  remark         VARCHAR(255)    NOT NULL DEFAULT '',
  created_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_name (name),
  KEY idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE freight_template_rules (
  id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  template_id         BIGINT UNSIGNED NOT NULL,
  region_id           BIGINT UNSIGNED NOT NULL,
  first_quantity      INT UNSIGNED    NOT NULL DEFAULT 1,
  first_fee           INT UNSIGNED    NOT NULL DEFAULT 0,
  additional_quantity INT UNSIGNED    NOT NULL DEFAULT 1,
  additional_fee      INT UNSIGNED    NOT NULL DEFAULT 0,
  free_threshold      INT UNSIGNED    NOT NULL DEFAULT 0,
  created_at          DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at          DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_template_region (template_id, region_id),
  KEY idx_region_id (region_id),
  CONSTRAINT fk_freight_rule_template FOREIGN KEY (template_id)
    REFERENCES freight_templates(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE products
  ADD COLUMN freight_template_id BIGINT UNSIGNED NULL DEFAULT NULL AFTER status,
  ADD KEY idx_freight_template_id (freight_template_id);
