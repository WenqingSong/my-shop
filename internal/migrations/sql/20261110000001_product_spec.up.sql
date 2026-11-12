-- 商品规格 V1：规格组（product_specs）+ 规格值（product_spec_values）+ SKU↔规格值关联（sku_spec_values），
-- 并为 skus 增加规格图与规格组合指纹。
-- 说明：纯 additive 变更，不改既有列语义、不回填历史数据，不触碰 inventories/inventory_logs/flash_sale_*。
-- 历史 SKU 兼容：既有 SKU 不写 sku_spec_values，spec_signature 保持 NULL、image_url 取默认空串；
--   唯一索引 uk_product_signature 允许多个 NULL，因此历史 SKU 与无规格 SKU 不受影响。
-- 规格组合唯一性：uk_product_signature (product_id, spec_signature) 保证「同一商品下同一规格组合
--   最多一个 SKU」。spec_signature 为 32 位小写 hex，取值规则见 docs/design/sku.md：
--   MD5(按 spec_id 升序拼接的 "spec_id:spec_value_id"，以 "," 分隔)。
--   指纹作用域限定在单商品内，仅用于组合去重，不用于安全场景。
-- 关联约束（三重）：
--   1) PRIMARY KEY (sku_id, spec_id)：一个 SKU 在同一规格组下只能取一个值；
--   2) 复合外键 (spec_value_id, spec_id) → product_spec_values(id, spec_id)：规格值必须属于声明的规格组；
--   3) idx_spec_value 支撑「按规格值反查 SKU」与上述复合外键。
-- 删除策略：
--   - product_specs / product_spec_values 随商品级联删除（商品存在 SKU 时已被 fk_skus_product RESTRICT 拦截）；
--   - sku_spec_values 随 SKU 级联删除（删除 SKU 不影响 skus.id 语义与其它 SKU）；
--   - sku_spec_values → product_spec_values 为 RESTRICT：被 SKU 引用的规格值不可删除，
--     避免「静默丢失已售 SKU 的规格组合」。
-- text 列长度与既有约定一致（name/value 32、url 512）。

CREATE TABLE product_specs (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  name       VARCHAR(32)     NOT NULL,
  sort       INT             NOT NULL DEFAULT 0,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_product_spec_name (product_id, name),
  CONSTRAINT fk_product_specs_product FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- uk_id_spec 是复合外键的被引用键（id 已是主键，此唯一键仅用于让 (id, spec_id) 可被引用）。
CREATE TABLE product_spec_values (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  spec_id    BIGINT UNSIGNED NOT NULL,
  value      VARCHAR(32)     NOT NULL,
  sort       INT             NOT NULL DEFAULT 0,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_id_spec (id, spec_id),
  UNIQUE KEY uk_spec_value (spec_id, value),
  CONSTRAINT fk_product_spec_values_spec FOREIGN KEY (spec_id) REFERENCES product_specs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE sku_spec_values (
  sku_id        BIGINT UNSIGNED NOT NULL,
  spec_id       BIGINT UNSIGNED NOT NULL,
  spec_value_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (sku_id, spec_id),
  KEY idx_spec_value (spec_value_id, spec_id),
  CONSTRAINT fk_sku_spec_values_sku FOREIGN KEY (sku_id) REFERENCES skus(id) ON DELETE CASCADE,
  CONSTRAINT fk_sku_spec_values_value FOREIGN KEY (spec_value_id, spec_id) REFERENCES product_spec_values(id, spec_id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE skus
  ADD COLUMN image_url      VARCHAR(512) NOT NULL DEFAULT '' AFTER name,
  ADD COLUMN spec_signature CHAR(32)     NULL AFTER status,
  ADD UNIQUE KEY uk_product_signature (product_id, spec_signature);
