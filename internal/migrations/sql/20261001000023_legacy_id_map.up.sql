-- 历史数据迁移 V1：legacy_id_map（旧 ID → 新 ID 映射表）。
-- 说明：经 golang-migrate 机制新增（version 20261001000023，已按 Registry 预留）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- 作用：持久化「历史旧主键 → 目标新主键」的映射，作为迁移幂等与审计事实源，
-- 与业务数据同库同事务提交（Contract D1=B）。
-- entity_type 枚举 category/product/sku；old_id 为历史旧表主键（store_category.id /
-- store_product.id / store_product_attr_value.id），new_id 为目标新主键。
-- uk_source_entity_old(source, entity_type, old_id) 兜底映射唯一，去重不依赖商品名称等自然键。
-- 新 ID 对 categories/products/skus 为软引用（无反向 FK），映射表不参与业务查询路径。

CREATE TABLE legacy_id_map (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  source      VARCHAR(64)     NOT NULL,
  entity_type VARCHAR(16)     NOT NULL,
  old_id      BIGINT UNSIGNED NOT NULL,
  new_id      BIGINT UNSIGNED NOT NULL,
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_source_entity_old (source, entity_type, old_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
