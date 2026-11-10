-- 一级分销基础模型 V1：user_referrals（推荐关系）+ distribution_commissions（订单佣金记录）。
-- 说明：只建结构，不实现绑定/计佣/结算/提现的业务逻辑，不新增订单字段、不改退款与钱包流程。
-- 本项目暂定只做一级分销：不建多级关系表、不建佣金账户/提现表、不建结算批次/规则引擎。
--
-- 推荐关系（user_referrals）：
--   user_id = 被推荐人（下级），referrer_user_id = 一级推荐人；一人一行（uk_user_id），关系一次性绑定、
--   不可改绑（无 UPDATE 业务路径）。user_id/referrer_user_id 均为软引用（users 既有约定），
--   不建物理 FK，避免用户删除级联影响历史关系。
--   绑定规则（应用层，B1-04 实现）：不能自荐（user_id <> referrer_user_id）、推荐人必须存在且状态正常；
--   DB 只能保证「一个用户至多一个推荐人」。created_at 即绑定时间，不再冗余 bound_at。
--   idx_referrer_user 支撑「我的下级列表」与按推荐人归集。
--
-- 佣金记录（distribution_commissions）：
--   order_id 物理 FK → orders(id) ON DELETE RESTRICT：佣金是资金事实，订单不可在佣金存在时被删除。
--   buyer_user_id / beneficiary_user_id 为软引用：买家与受益人均为下单时的身份快照，用户后续变更不影响历史。
--   base_amount / rate_bp / commission_amount 为计佣快照：基数（整数分）、比例（万分比）、佣金（整数分）。
--     计算规则与舍入在 docs/design/distribution.md 固化，DB 只存快照，不在数据库中重算。
--   uk_order_id：一个订单至多一条佣金记录，是「防止重复计提」的最终幂等约束（重复写入 1062，
--     写入方据此判定已计提）。不额外引入 idempotency_key，避免与订单唯一键表达同一规则。
--   uk_refund_id：同一退款单至多冲回一条佣金（MySQL 唯一索引允许多个 NULL）；配合状态条件更新，
--     保证退款成功只冲回一次。refund_id 为软引用（refund_requests.id），不建 FK。
--   状态 status：10=待结算、20=已结算、30=已冲回。终态 = {30}；20 仍可因退款转为 30。
--   退款冲回依据：reversed_amount（已冲回金额，整单退款时等于 commission_amount）、reversed_at、
--     refund_id、reversal_reason（refund/cancel）。状态机与条件更新见设计文档。
--   idx_beneficiary_status 支撑「推荐人佣金明细/汇总」；idx_status_created 支撑「待结算佣金扫描」。

CREATE TABLE user_referrals (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id          BIGINT UNSIGNED NOT NULL,
  referrer_user_id BIGINT UNSIGNED NOT NULL,
  created_at       DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_id (user_id),
  KEY idx_referrer_user (referrer_user_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE distribution_commissions (
  id                 BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id           BIGINT UNSIGNED NOT NULL,
  buyer_user_id      BIGINT UNSIGNED NOT NULL,
  beneficiary_user_id BIGINT UNSIGNED NOT NULL,
  base_amount        INT UNSIGNED    NOT NULL,
  rate_bp            INT UNSIGNED    NOT NULL,
  commission_amount  INT UNSIGNED    NOT NULL,
  status             TINYINT         NOT NULL DEFAULT 10,
  settled_at         DATETIME        NULL,
  reversed_amount    INT UNSIGNED    NOT NULL DEFAULT 0,
  reversed_at        DATETIME        NULL,
  refund_id          BIGINT UNSIGNED NULL,
  reversal_reason    VARCHAR(32)     NOT NULL DEFAULT '',
  created_at         DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at         DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_order_id (order_id),
  UNIQUE KEY uk_refund_id (refund_id),
  KEY idx_beneficiary_status (beneficiary_user_id, status, created_at),
  KEY idx_status_created (status, created_at),
  CONSTRAINT fk_distribution_commissions_order FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
