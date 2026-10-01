-- 普通库存 V1：inventories（1:1 sku_id 唯一）+ inventory_logs（库存变更流水）。
-- 说明：经 golang-migrate 机制新增（version 紧随 skus 20261001000003）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- quantity 为 INT UNSIGNED（整数个件，≥0），无 inventory 记录视为 quantity=0；
-- 防负库存以「条件更新 + RowsAffected」为主、INT UNSIGNED 类型为兜底。
-- change_type 1=increase、2=deduct；operator_admin_id 为软引用（无 FK），取自操作者管理员 id。
-- sku_id FK → skus.id ON DELETE RESTRICT：SKU 一旦有库存记录或流水即不可物理删除。

CREATE TABLE inventories (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  sku_id     BIGINT UNSIGNED NOT NULL,
  quantity   INT UNSIGNED    NOT NULL DEFAULT 0,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_sku_id (sku_id),
  CONSTRAINT fk_inventories_sku FOREIGN KEY (sku_id) REFERENCES skus(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE inventory_logs (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  sku_id            BIGINT UNSIGNED NOT NULL,
  change_type       TINYINT         NOT NULL,
  change_qty        INT UNSIGNED    NOT NULL,
  before_qty        INT UNSIGNED    NOT NULL,
  after_qty         INT UNSIGNED    NOT NULL,
  operator_admin_id BIGINT UNSIGNED NULL,
  reason            VARCHAR(255)    NOT NULL DEFAULT '',
  created_at        DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_sku_id (sku_id),
  CONSTRAINT fk_inventory_logs_sku FOREIGN KEY (sku_id) REFERENCES skus(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
