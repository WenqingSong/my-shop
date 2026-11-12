-- 订单状态流水 V1：order_status_logs（append-only，记录订单每一次状态迁移）。
-- 说明：只建表，不改既有订单状态变更逻辑；流水写入由后续 B1-04 在状态推进事务内实现。
-- 状态取值与 orders.status 完全一致（10=待支付、20=已支付、30=已发货、40=已收货、50=已完成、
--   60=已取消、70=已退款），不引入第二套订单状态机。
-- change_type 为语义化变更类型（create/pay/cancel/timeout_cancel/ship/receive/complete/refund），
--   仅作展示与审计分类，不参与状态机判定。
-- operator_type 沿用 refund_audits 约定并扩展系统操作：1=user、2=admin、3=system；
--   operator_id 为对应用户/管理员 id，system 驱动写 0（无 FK，避免删除用户/管理员阻断审计）。
-- 一个订单允许多条流水；idx_order_time(order_id, created_at) 支撑按订单时间线查询。
-- 流水只追加：不提供 UPDATE/DELETE 业务路径；备注长度与既有审计表一致。

CREATE TABLE order_status_logs (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id      BIGINT UNSIGNED NOT NULL,
  from_status   TINYINT         NOT NULL,
  to_status     TINYINT         NOT NULL,
  change_type   VARCHAR(32)     NOT NULL,
  operator_type TINYINT         NOT NULL,
  operator_id   BIGINT UNSIGNED NOT NULL DEFAULT 0,
  remark        VARCHAR(255)    NOT NULL DEFAULT '',
  created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_order_time (order_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
