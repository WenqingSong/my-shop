-- 秒杀 B2：把 flash_sale_order_requests 扩展为 Request Outbox（不新建第二张表）。
-- 说明：经 golang-migrate 机制新增（version 20261001000024）。
--
-- 该表同时承担五件事：已受理请求的持久化、幂等证据、用户查询结果、
-- Redis inflight 计数的数据库事实来源，以及「Kafka Requested 事件的待发布记录」。
-- 本 migration 只增加发布状态与领取租约，不改动任何业务状态列（status/retry_count/
-- next_attempt_at/last_error_code/flash_order_id 语义不变），也不删除既有索引与唯一约束：
--   - idx_dequeue 仍服务于 MySQL Scanner 的 status=queued 出队；
--   - uk_request_idempotency 仍是请求级幂等的唯一兜底；
--   - next_attempt_at 仍是 Scanner 的业务重试时间，与 Kafka 发布退避 next_publish_at 无关。
--
-- 发布不变量：
--   - published_at 只能由「Broker 已确认」的发布结果写入（禁止先标记后发布）；
--   - publish_lease_until 是数据库级领取租约，多实例互斥依赖它而不是进程内 mutex；
--   - 租约过期后允许被其他 Publisher 重新领取，因此发布语义是 at-least-once，
--     消费方必须按 event_id（由 request_id 稳定推导）去重。
--
-- 旧数据兼容：既有行（无论 queued/success/failed/dead）在迁移后一律为
-- published_at=NULL / publish_attempts=0 / 租约为空，即「未发布」。
-- 只有 status=queued 的行才会被 Publisher 领取，终态行不会因为本次迁移被重新发布。

ALTER TABLE flash_sale_order_requests
  ADD COLUMN published_at        DATETIME(6)  NULL COMMENT 'Kafka Broker 确认发布时间；NULL 表示尚未确认发布' AFTER flash_order_id,
  ADD COLUMN publish_claim_token VARCHAR(64)  NULL COMMENT '本轮领取租约的唯一令牌；与 request_id 共同限定标记权限' AFTER published_at,
  ADD COLUMN publish_lease_until DATETIME(6)  NULL COMMENT '领取租约到期时间（数据库时间语义）；为空或过期可被重新领取' AFTER publish_claim_token,
  ADD COLUMN publish_attempts    INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '已发起的发布领取次数（仅用于退避与诊断，不代表成功）' AFTER publish_lease_until,
  ADD COLUMN next_publish_at     DATETIME(6)  NULL COMMENT '下次允许发布的时间（指数退避）；NULL 表示立即可领取' AFTER publish_attempts,
  ADD COLUMN last_publish_error  VARCHAR(255) NULL COMMENT '最近一次发布失败原因（截断，仅诊断，不改变业务状态）' AFTER next_publish_at,
  ADD COLUMN published_partition INT          NULL COMMENT 'Broker 确认的分区（诊断/对账用）' AFTER last_publish_error,
  ADD COLUMN published_offset    BIGINT       NULL COMMENT 'Broker 确认的位移（诊断/对账用）' AFTER published_partition,
  -- 领取索引：status + published_at 定位「待发布集合」，next_publish_at 让「未到期（退避中）」的
  -- 行落在索引范围之外，避免每次轮询把它们全部读出并加锁；id 收尾保证同一到期时间下的稳定顺序。
  -- 租约为空/过期在到期集合里只占少数（等于并发 Publisher 的批次大小），作为索引回表后的残余条件过滤。
  ADD KEY idx_outbox_claim (status, published_at, next_publish_at, id);
