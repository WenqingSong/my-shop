-- 商品评价 V1：reviews（评价主数据）。
-- 说明：经 golang-migrate 机制新增（version 20261001000009，紧随 refresh_tokens 20261001000008）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- user_id/order_item_id/product_id/sku_id 均为软引用（无 FK）：快照自足，
-- SKU/商品被删除后评价仍可读（与 order_items 软引用约定一致）。
-- product_id/sku_id 为提交时从 order_items 快照冗余；user_id 取自认证 Principal 并冗余。
-- uk_order_item(order_item_id) 唯一约束兜底「每个订单项最多一条评价」（order_item_id 本身全局唯一），
-- 删除为软删除（status=3），唯一槽位永久保留、不允许重新评价。
-- idx_user_id(user_id) 支撑本人评价列表；idx_product_status(product_id, status) 支撑公开列表与汇总聚合。
-- status：1=published、2=taken_down（管理员下架）、3=deleted（买家删除）。
-- image_urls 预留图片 URL 字段（本次不实现上传，恒为 NULL，不进 API）。

CREATE TABLE reviews (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id       BIGINT UNSIGNED NOT NULL,
  order_item_id BIGINT UNSIGNED NOT NULL,
  product_id    BIGINT UNSIGNED NOT NULL,
  sku_id        BIGINT UNSIGNED NOT NULL,
  rating        TINYINT UNSIGNED NOT NULL,
  content       VARCHAR(500)    NOT NULL,
  image_urls    JSON            NULL,
  status        TINYINT         NOT NULL DEFAULT 1,
  created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_order_item (order_item_id),
  KEY idx_user_id (user_id),
  KEY idx_product_status (product_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
