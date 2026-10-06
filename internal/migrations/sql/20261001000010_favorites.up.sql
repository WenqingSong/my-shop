-- 商品收藏 V1：favorites（收藏主数据）。
-- 说明：经 golang-migrate 机制新增（version 20261001000010，紧随 reviews 20261001000009）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- user_id/product_id 均为软引用（不建 FK）：添加时应用层校验商品存在且在售（on_shelf），
-- 商品被下架/删除后收藏保留为悬空引用，由查询时 LEFT JOIN products 标识不可用。
-- 收藏为二元关系（收藏/取消），无字段可变，故无 updated_at。
-- uk_user_product(user_id, product_id) 兜底「同一用户同一商品至多一条」，兼作按用户查询索引（user_id 前导列）。

CREATE TABLE favorites (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    BIGINT UNSIGNED NOT NULL,
  product_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_product (user_id, product_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
