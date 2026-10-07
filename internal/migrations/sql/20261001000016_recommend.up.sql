-- 推荐位 V1：recommend_positions（推荐位主数据）+ recommend_items（推荐位 × 商品关系）。
-- 说明：经 golang-migrate 机制新增（version 20261001000016，紧随 flash_sale_order_requests 20261001000015）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- code 为稳定业务标识：唯一（uk_code）、格式 [a-z0-9][a-z0-9-]{0,63}、创建后不可变，前台按 code 定位。
-- status：1=启用、0=禁用（禁用为临时下线，关系保留，前台不返回，后台可见）。
-- recommend_items.product_id 为软引用（无 FK），存在性由应用层经 service.Product().Exists 校验。
-- uk_position_product(position_id, product_id) 兜底「同一推荐位同一商品唯一」，并发重复提交也被拒绝。
-- idx_position_sort(position_id, sort) 支撑前台查询（WHERE position_id=? ORDER BY sort, id）。
-- fk_recommend_item_position：物理删除推荐位时级联删除推荐商品关系（ON DELETE CASCADE），不留孤儿。

CREATE TABLE recommend_positions (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code       VARCHAR(64)     NOT NULL,
  name       VARCHAR(64)     NOT NULL,
  status     TINYINT         NOT NULL DEFAULT 1,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_code (code),
  KEY idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE recommend_items (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  position_id BIGINT UNSIGNED NOT NULL,
  product_id  BIGINT UNSIGNED NOT NULL,
  sort        INT             NOT NULL DEFAULT 0,
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_position_product (position_id, product_id),
  KEY idx_position_sort (position_id, sort),
  CONSTRAINT fk_recommend_item_position FOREIGN KEY (position_id) REFERENCES recommend_positions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
