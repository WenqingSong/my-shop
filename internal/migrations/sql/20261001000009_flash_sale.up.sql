-- 秒杀核心闭环 V1：flash_sale_activities（活动）+ flash_sale_activity_skus（活动×SKU 绑定：秒杀价 + 秒杀库存）+ flash_sale_orders（秒杀订单）。
-- 说明：经 golang-migrate 机制新增（version 紧随 refresh_tokens 20261001000008）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- 秒杀库存独立建模：total_stock 为初始库存（语义为普通可售库存的预分配/预留活动配额），sold 为已售，
-- 剩余 = total_stock - sold（恒 ≥ 0）；V1 抢购事务内不联动普通 inventories。
-- 秒杀订单「下单即成交」：无支付/取消/退款/超时恢复状态机，行存在即成功订单。
-- 秒杀订单 sku_id/product_id/user_id/activity_id 均为软引用（无 FK），下单时快照自足。
-- 一人一单：uk_flash_one_per_user(activity_id, sku_id, user_id) 兜底；幂等：uk_flash_idempotency(user_id, idempotency_key) 兜底。
-- 防超卖：UPDATE ... SET sold = sold + 1 WHERE sold < total_stock 条件更新 + RowsAffected 兜底，INT UNSIGNED 防负。

CREATE TABLE flash_sale_activities (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name       VARCHAR(64)     NOT NULL,
  status     TINYINT         NOT NULL DEFAULT 1,
  start_time DATETIME        NOT NULL,
  end_time   DATETIME        NOT NULL,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_status (status),
  KEY idx_time (start_time, end_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE flash_sale_activity_skus (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  activity_id BIGINT UNSIGNED NOT NULL,
  sku_id      BIGINT UNSIGNED NOT NULL,
  flash_price INT UNSIGNED    NOT NULL,
  total_stock INT UNSIGNED    NOT NULL,
  sold        INT UNSIGNED    NOT NULL DEFAULT 0,
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_activity_sku (activity_id, sku_id),
  KEY idx_sku_id (sku_id),
  CONSTRAINT fk_flash_sku_activity FOREIGN KEY (activity_id) REFERENCES flash_sale_activities(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE flash_sale_orders (
  id                 BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_no           VARCHAR(32)     NOT NULL,
  user_id            BIGINT UNSIGNED NOT NULL,
  activity_id        BIGINT UNSIGNED NOT NULL,
  sku_id             BIGINT UNSIGNED NOT NULL,
  product_id         BIGINT UNSIGNED NOT NULL,
  sku_name           VARCHAR(128)    NOT NULL,
  product_name       VARCHAR(128)    NOT NULL,
  product_main_image VARCHAR(512)    NOT NULL DEFAULT '',
  flash_price        INT UNSIGNED    NOT NULL,
  quantity           INT UNSIGNED    NOT NULL DEFAULT 1,
  idempotency_key    VARCHAR(64)     NOT NULL,
  request_hash       VARCHAR(64)     NOT NULL,
  created_at         DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at         DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_flash_order_no (order_no),
  UNIQUE KEY uk_flash_idempotency (user_id, idempotency_key),
  UNIQUE KEY uk_flash_one_per_user (activity_id, sku_id, user_id),
  KEY idx_activity (activity_id),
  KEY idx_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
