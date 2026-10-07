-- 秒杀 V3（异步下单）：flash_sale_order_requests（异步请求生命周期 / 出队表）。
-- 说明：经 golang-migrate 机制新增（version 20261001000015）。
-- V3 把「下单」拆成「入队快速响应 + 后台消费落单」两阶段，本表承载异步请求的
-- queued(0)/success(1)/failed(2)/dead(3) 生命周期，同时充当消费者的出队队列。
-- 它不改变成功订单事实来源：flash_sale_orders 行存在仍是「成功订单」唯一业务事实。
-- 请求级幂等由 uk_request_idempotency(user_id, idempotency_key) 兜底；
-- 消费出队由 idx_dequeue(status, next_attempt_at, id) 支撑
-- 「WHERE status=queued AND (next_attempt_at IS NULL OR next_attempt_at<=NOW()) ORDER BY id ... FOR UPDATE SKIP LOCKED」。
-- user_id/activity_id/sku_id 均为软引用（无 FK）；flash_order_id 成功后关联 flash_sale_orders.id（软引用）。

CREATE TABLE flash_sale_order_requests (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id         BIGINT UNSIGNED NOT NULL,
  activity_id     BIGINT UNSIGNED NOT NULL,
  sku_id          BIGINT UNSIGNED NOT NULL,
  idempotency_key VARCHAR(64)     NOT NULL,
  request_hash    VARCHAR(64)     NOT NULL,
  status          TINYINT         NOT NULL DEFAULT 0,
  retry_count     INT UNSIGNED    NOT NULL DEFAULT 0,
  next_attempt_at DATETIME        NULL,
  last_error_code INT             NULL,
  flash_order_id  BIGINT UNSIGNED NULL,
  created_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_request_idempotency (user_id, idempotency_key),
  KEY idx_dequeue (status, next_attempt_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
