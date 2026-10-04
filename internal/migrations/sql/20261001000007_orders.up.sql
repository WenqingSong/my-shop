-- 订单核心闭环 V1：orders（订单主数据）+ order_items（订单项）。
-- 说明：经 golang-migrate 机制新增（version 紧随 cart_items 20261001000006）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- 金额均为整数分（INT UNSIGNED）；状态为 TINYINT（见状态机：10/20/30/40/50/60/70）。
-- user_id/address_id 采用软引用（无 FK）：快照自足，SKU/地址被删除后订单仍可读；
-- 订单项 sku_id/product_id 亦为软引用，商品/SKU 名与主图、成交价均在下单时快照。
-- uk_order_no(order_no) 兜底全局唯一订单号；uk_user_idempotency(user_id, idempotency_key)
-- 兜底「同一用户同一幂等键至多一单」（幂等去重的最终防线）。
-- idx_status_expire(status, expire_at) 支撑「待支付 + 已过期」的超时扫描与懒取消。
-- cancel_reason：1=用户取消、2=超时取消；expire_at = 下单时间 + order.pay_timeout。

CREATE TABLE orders (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_no        VARCHAR(32)     NOT NULL,
  user_id         BIGINT UNSIGNED NOT NULL,
  status          TINYINT         NOT NULL DEFAULT 10,
  total_amount    INT UNSIGNED    NOT NULL,
  idempotency_key VARCHAR(64)     NOT NULL,
  request_hash    VARCHAR(64)     NOT NULL,
  recipient_name  VARCHAR(32)     NOT NULL,
  phone           VARCHAR(20)     NOT NULL,
  province        VARCHAR(32)     NOT NULL,
  city            VARCHAR(32)     NOT NULL,
  district        VARCHAR(32)     NOT NULL,
  detail          VARCHAR(255)    NOT NULL,
  address_id      BIGINT UNSIGNED NULL,
  expire_at       DATETIME        NOT NULL,
  cancel_reason   TINYINT         NULL,
  paid_at         DATETIME        NULL,
  shipped_at      DATETIME        NULL,
  received_at     DATETIME        NULL,
  completed_at    DATETIME        NULL,
  cancelled_at    DATETIME        NULL,
  refunded_at     DATETIME        NULL,
  created_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_order_no (order_no),
  UNIQUE KEY uk_user_idempotency (user_id, idempotency_key),
  KEY idx_user_id (user_id),
  KEY idx_status_expire (status, expire_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE order_items (
  id                 BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id           BIGINT UNSIGNED NOT NULL,
  sku_id             BIGINT UNSIGNED NOT NULL,
  product_id         BIGINT UNSIGNED NOT NULL,
  sku_name           VARCHAR(128)    NOT NULL,
  product_name       VARCHAR(128)    NOT NULL,
  product_main_image VARCHAR(512)    NOT NULL DEFAULT '',
  price              INT UNSIGNED    NOT NULL,
  quantity           INT UNSIGNED    NOT NULL,
  created_at         DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_order_id (order_id),
  CONSTRAINT fk_order_items_order FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
