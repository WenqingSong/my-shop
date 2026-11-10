-- 砍价基础模型 V1：bargain_activities（活动）+ bargain_instances（用户砍价实例）+ bargain_help_records（助力记录）。
-- 说明：只建结构，不实现发起砍价/助力/成功/失败/过期的业务逻辑，不新增订单字段、不改支付与库存。
--
-- 边界（重要）：
--   1) 不复制历史 store_bargain 的商户、运费、装修、分享量等冗余列；不建活动库存列，
--      也不读写 inventories / inventory_logs / flash_sale_*。砍价同样不拥有库存，成交仍走现有库存扣减。
--   2) start_price/floor_price 在活动上定义，并在实例创建时快照为 start_price/floor_price/current_price，
--      活动后续改价不影响已创建的实例。help_amount 为每次助力的名义砍减额（Mock 规则，整数分）。
--   3) 最后一次助力允许「补足剩余差额」：实际 cut_amount 可以小于 help_amount，但 price_after 不得低于
--      floor_price。B1-04 用条件更新保证：
--      `UPDATE bargain_instances SET current_price = GREATEST(floor_price, current_price - ?),
--         helped_count = helped_count + 1
--       WHERE id=? AND status=10 AND helped_count < max_help_count AND current_price > floor_price`。
--   4) 关联约定：product_id/sku_id 为物理 FK（RESTRICT）；user_id、order_id 为软引用（无 FK），
--      订单删除后砍价实例仍可读；order_id 唯一索引兜底「一个订单只对应一个砍价实例」。
--
-- 状态：
--   活动 status：1=启用、0=停用；时间窗由 start_time/end_time 派生。
--   实例 status：10=进行中、20=成功、30=失败（主动放弃/活动提前终止）、40=已过期（超过 expire_at）。
--     终态 = {20,30,40}；completed_at 仅记录成功时间，失败/过期由 status + expire_at + updated_at 表达。
--   expire_at 为实例创建时对活动 end_time 的快照，避免活动延期改变已参与实例的截止时间。
--
-- 并发与重复请求：
--   active_key 为生成列：status=10（进行中）时 = "activity_id:user_id"，否则 NULL；
--     UNIQUE(active_key) 保证「同一用户同一活动至多一个进行中实例」，并发重复发起 1062。
--     失败/过期后 active_key 变为 NULL，允许重新发起；历史实例保留，不覆盖审计记录。
--   uk_instance_helper(instance_id, helper_user_id)：同一用户对同一实例至多助力一次（并发重复助力 1062）。
--   helped_count < max_help_count 由条件更新保证；上述唯一键是并发重复请求的最终防线。
--   助力与实例价格推进必须同事务：先条件更新实例（RowsAffected=0 → 不可助力），再插入助力记录；
--   若插入撞唯一键则整体回滚，已推进的价格一并回滚。

CREATE TABLE bargain_activities (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id     BIGINT UNSIGNED NOT NULL,
  sku_id         BIGINT UNSIGNED NOT NULL,
  name           VARCHAR(64)     NOT NULL,
  start_price    INT UNSIGNED    NOT NULL,
  floor_price    INT UNSIGNED    NOT NULL,
  help_amount    INT UNSIGNED    NOT NULL,
  max_help_count INT UNSIGNED    NOT NULL,
  start_time     DATETIME        NOT NULL,
  end_time       DATETIME        NOT NULL,
  status         TINYINT         NOT NULL DEFAULT 1,
  created_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_product_id (product_id),
  KEY idx_sku_id (sku_id),
  KEY idx_status_time (status, start_time, end_time),
  CONSTRAINT fk_bargain_activities_product FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE RESTRICT,
  CONSTRAINT fk_bargain_activities_sku FOREIGN KEY (sku_id) REFERENCES skus(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE bargain_instances (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  activity_id    BIGINT UNSIGNED NOT NULL,
  user_id        BIGINT UNSIGNED NOT NULL,
  start_price    INT UNSIGNED    NOT NULL,
  floor_price    INT UNSIGNED    NOT NULL,
  current_price  INT UNSIGNED    NOT NULL,
  helped_count   INT UNSIGNED    NOT NULL DEFAULT 0,
  max_help_count INT UNSIGNED    NOT NULL,
  status         TINYINT         NOT NULL DEFAULT 10,
  expire_at      DATETIME        NOT NULL,
  completed_at   DATETIME        NULL,
  order_id       BIGINT UNSIGNED NULL,
  active_key     VARCHAR(64)     GENERATED ALWAYS AS (CASE WHEN status = 10 THEN CONCAT(activity_id, ':', user_id) ELSE NULL END) STORED,
  created_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_active_instance (active_key),
  UNIQUE KEY uk_order_id (order_id),
  KEY idx_activity_id (activity_id),
  KEY idx_user_status (user_id, status),
  KEY idx_status_expire (status, expire_at),
  CONSTRAINT fk_bargain_instances_activity FOREIGN KEY (activity_id) REFERENCES bargain_activities(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE bargain_help_records (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  instance_id    BIGINT UNSIGNED NOT NULL,
  helper_user_id BIGINT UNSIGNED NOT NULL,
  cut_amount     INT UNSIGNED    NOT NULL,
  price_before   INT UNSIGNED    NOT NULL,
  price_after    INT UNSIGNED    NOT NULL,
  created_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_instance_helper (instance_id, helper_user_id),
  KEY idx_helper_time (helper_user_id, created_at),
  CONSTRAINT fk_bargain_help_records_instance FOREIGN KEY (instance_id) REFERENCES bargain_instances(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
