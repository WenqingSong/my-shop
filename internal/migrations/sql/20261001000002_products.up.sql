-- 商品 SPU V1：products（商品主数据）+ product_images（商品图片）。
-- 说明：经 golang-migrate 机制新增（version 紧随 baseline 20261001000001）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- price 为整数分（INT UNSIGNED）；status 0=draft、1=on_shelf、2=off_shelf。
-- category_id 增加 FK → categories.id ON DELETE RESTRICT，用于并发删除分类的兜底保护。

CREATE TABLE products (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name        VARCHAR(128)    NOT NULL,
  brand       VARCHAR(64)     NOT NULL DEFAULT '',
  category_id BIGINT UNSIGNED NOT NULL,
  price       INT UNSIGNED    NOT NULL,
  main_image  VARCHAR(512)    NOT NULL DEFAULT '',
  detail      TEXT            NULL,
  status      TINYINT         NOT NULL DEFAULT 0,
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_category_id (category_id),
  KEY idx_status (status),
  CONSTRAINT fk_products_category FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE product_images (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  url        VARCHAR(512)    NOT NULL,
  sort       INT             NOT NULL DEFAULT 0,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_product_id (product_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
