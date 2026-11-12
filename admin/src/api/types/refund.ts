/**
 * 退款 DTO（与 `api/refund/v1/refund.go` 字段一一对应）。
 *
 * 状态机（DB TINYINT ↔ API 字符串）：
 * ```
 * applying(10) --approve--> pending_refund(20) --success--> refund_succeeded(60)
 *                                          \--failure--> refund_failed(30) --success--> 60
 *                                          \--close(仅 30)--> closed(70)
 * applying(10) --reject--> review_rejected(40, 终态)
 * applying(10) --用户撤销--> revoked(50, 终态)
 * ```
 *
 * 与订单状态是**两套独立枚举**：退款成功时后端才把订单 paid→refunded。
 */
export type RefundStatus =
  | "applying"
  | "pending_refund"
  | "refund_failed"
  | "review_rejected"
  | "revoked"
  | "refund_succeeded"
  | "closed";

/** 审核结果 */
export type RefundReviewResult = "approve" | "reject";
/** 执行结果（后端为人工 Mock 确认，不接入真实支付渠道） */
export type RefundExecuteResult = "success" | "failure";
/** 审计动作（append-only） */
export type RefundAuditAction =
  | "apply"
  | "review_approve"
  | "review_reject"
  | "execute_success"
  | "execute_failure"
  | "close"
  | "revoke";

/** 退款申请 */
export interface RefundDTO {
  id: number;
  order_id: number;
  user_id: number;
  status: RefundStatus;
  /** 退款金额（整数分，= 订单 total_amount 快照，服务端派生） */
  amount: number;
  /** 用户申请原因 */
  reason: string;
  /** 审核原因 */
  review_reason: string;
  reviewed_by: number | null;
  reviewed_at: string | null;
  /** 执行/关闭原因 */
  execute_reason: string;
  executed_by: number | null;
  executed_at: string | null;
  created_at: string | null;
  updated_at: string | null;
}

/** 退款审计记录（只追加，不可修改删除） */
export interface RefundAuditDTO {
  id: number;
  refund_id: number;
  action: RefundAuditAction;
  /** 1=用户，2=管理员 */
  operator_type: 1 | 2;
  operator_id: number;
  operator_username: string;
  /** 操作前状态（apply 为 0） */
  before_status: number;
  /** 操作后状态 */
  after_status: number;
  reason: string;
  created_at: string | null;
}

/** 后台退款列表查询参数 */
export interface AdminRefundListQuery {
  page?: number;
  size?: number;
  /** 订单 id，精确匹配（后端拒绝 <=0） */
  order_id?: number;
  /** 用户 id，精确匹配（后端拒绝 <=0） */
  user_id?: number;
  status?: RefundStatus;
}

/** 后台退款列表响应 */
export interface AdminRefundListResult {
  items: RefundDTO[];
  total: number;
  page: number;
  size: number;
}
