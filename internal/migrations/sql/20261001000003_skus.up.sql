-- SKU V1：skus（商品 1 — N SKU）。
-- 说明：经 golang-migrate 机制新增（version 紧随 products 20261001000002）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- price 为整数分（INT UNSIGNED）；status 1=enabled、0=disabled（默认 enabled）。
-- product_id 增加 FK → products.id ON DELETE RESTRICT（当前无商品删除接口，为兜底）。
-- (product_id, name) 复合唯一约束兜底「同一商品下 name 唯一」的并发竞争窗口。
-- 不含 stock：库存数据与操作交由后续 4.3 普通库存基于 sku_id 独立建立。

CREATE TABLE skus (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  name       VARCHAR(128)    NOT NULL,
  price      INT UNSIGNED    NOT NULL,
  status     TINYINT         NOT NULL DEFAULT 1,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_product_id (product_id),
  UNIQUE KEY uk_product_name (product_id, name),
  CONSTRAINT fk_skus_product FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
