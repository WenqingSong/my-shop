-- 拼团基础模型 V1：group_buy_activities（活动）+ group_buy_teams（团实例）+ group_buy_participants（参与记录）。
-- 说明：只建结构，不实现开团/参团/成团/失败/过期的业务逻辑，不新增订单字段、不改支付与库存。
--
-- 边界（重要）：
--   1) 不复制历史 store_combination / store_pink 的冗余列：不建活动库存、不建订单快照列、不建商户/运费/装修字段。
--   2) 拼团不拥有库存：不在本模型建 stock/sold，也不读写 inventories / inventory_logs / flash_sale_*。
--      B1-04 下单时仍走现有库存扣减；拼团只提供价格、成团人数与团有效期。
--   3) 活动价格与成团人数在「团」上快照：团队创建后活动改价/改人数不影响已开的团，同团成员支付同一 group_price。
--      required_size 同理为开团时快照，避免活动编辑导致已进行中的团人数要求漂移。
--   4) 关联约定：product_id/sku_id 为物理 FK（RESTRICT，商品/SKU 被活动引用时不可删）；
--      user_id 为软引用（与 users 既有约定一致）；order_id 为软引用 + 唯一索引（订单删除后参与记录仍可读）。
--
-- 状态：
--   活动 status：1=启用、0=停用（时间窗由 start_time/end_time 派生，不设冗余终态）。
--   团 status：10=拼团中、20=已成团、30=已失败（主动取消/团长退出等）、40=已过期（超过 expire_at 未成团）。
--     终态 = {20,30,40}；closed_at 记录进入终态的时间（三种终态共用）。
--   参与 status：10=有效、20=已退出。团队终态不逐行改写参与记录，团 status 为团队结果的权威事实。
--
-- 并发与容量：
--   joined_count 为团内有效参与人数计数器，默认 1（团长）。参团必须在同一事务内做条件更新
--   `UPDATE group_buy_teams SET joined_count = joined_count + 1
--     WHERE id=? AND status=10 AND joined_count < required_size AND expire_at > NOW()`，
--   再插入参与记录；uk_team_user 兜底同一用户重复参团，uk_order_id 兜底同一订单重复参团。
--   因此 required_size 与 joined_count 的组合是「成团人数」的数据库侧承载，不依赖事后 count(*)。
--   成团推进为 `status=20, closed_at=NOW() WHERE id=? AND status=10 AND joined_count >= required_size`。

CREATE TABLE group_buy_activities (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id       BIGINT UNSIGNED NOT NULL,
  sku_id           BIGINT UNSIGNED NOT NULL,
  name             VARCHAR(64)     NOT NULL,
  group_price      INT UNSIGNED    NOT NULL,
  group_size       INT UNSIGNED    NOT NULL,
  team_ttl_minutes INT UNSIGNED    NOT NULL,
  start_time       DATETIME        NOT NULL,
  end_time         DATETIME        NOT NULL,
  status           TINYINT         NOT NULL DEFAULT 1,
  created_at       DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_product_id (product_id),
  KEY idx_sku_id (sku_id),
  KEY idx_status_time (status, start_time, end_time),
  CONSTRAINT fk_group_buy_activities_product FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE RESTRICT,
  CONSTRAINT fk_group_buy_activities_sku FOREIGN KEY (sku_id) REFERENCES skus(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE group_buy_teams (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  activity_id    BIGINT UNSIGNED NOT NULL,
  leader_user_id BIGINT UNSIGNED NOT NULL,
  group_price    INT UNSIGNED    NOT NULL,
  required_size  INT UNSIGNED    NOT NULL,
  joined_count   INT UNSIGNED    NOT NULL DEFAULT 1,
  status         TINYINT         NOT NULL DEFAULT 10,
  expire_at      DATETIME        NOT NULL,
  closed_at      DATETIME        NULL,
  created_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_activity_status (activity_id, status),
  KEY idx_leader_user (leader_user_id),
  KEY idx_status_expire (status, expire_at),
  CONSTRAINT fk_group_buy_teams_activity FOREIGN KEY (activity_id) REFERENCES group_buy_activities(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE group_buy_participants (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  team_id    BIGINT UNSIGNED NOT NULL,
  user_id    BIGINT UNSIGNED NOT NULL,
  order_id   BIGINT UNSIGNED NULL,
  status     TINYINT         NOT NULL DEFAULT 10,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_team_user (team_id, user_id),
  UNIQUE KEY uk_order_id (order_id),
  KEY idx_user_time (user_id, created_at),
  CONSTRAINT fk_group_buy_participants_team FOREIGN KEY (team_id) REFERENCES group_buy_teams(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
