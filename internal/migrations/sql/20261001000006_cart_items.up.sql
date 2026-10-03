-- 购物车 V1：cart_items（用户 + SKU 粒度条目）。
-- 说明：经 golang-migrate 机制新增（version 紧随 addresses 20261001000005）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- quantity 为 INT UNSIGNED（正整数，业务上限 999）；price_snapshot 为加购时 skus.price 整数分快照（不锁价）；
-- selected TINYINT 1=勾选、0=取消（默认 1）。
-- user_id/sku_id 均采用软引用（不建 FK）：加购时应用层校验 SKU 存在，SKU 被物理删除后条目保留为不可购条目。
-- uk_user_sku(user_id, sku_id) 兜底「同一用户同一 SKU 至多一条」，兼作按用户查询索引（user_id 前导列）。

CREATE TABLE cart_items (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id        BIGINT UNSIGNED NOT NULL,
  sku_id         BIGINT UNSIGNED NOT NULL,
  quantity       INT UNSIGNED    NOT NULL,
  price_snapshot INT UNSIGNED    NOT NULL,
  selected       TINYINT         NOT NULL DEFAULT 1,
  created_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_sku (user_id, sku_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
