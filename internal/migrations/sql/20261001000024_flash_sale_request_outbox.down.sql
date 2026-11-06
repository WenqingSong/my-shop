-- 秒杀 B2 回滚：移除 Outbox 发布状态与领取索引，恢复 B1 之前的表结构。
-- 说明：本 down 只回滚本次新增列与索引，不动既有列/索引/唯一约束。
-- 回滚会丢失发布证据（published_at 等），因此仅用于测试库与显式回滚场景；
-- 回滚后代码若仍运行 Kafka Publisher，行会因缺列而报错——必须先下线 Publisher 再回滚。

ALTER TABLE flash_sale_order_requests
  DROP KEY idx_outbox_claim,
  DROP COLUMN published_offset,
  DROP COLUMN published_partition,
  DROP COLUMN last_publish_error,
  DROP COLUMN next_publish_at,
  DROP COLUMN publish_attempts,
  DROP COLUMN publish_lease_until,
  DROP COLUMN publish_claim_token,
  DROP COLUMN published_at;
