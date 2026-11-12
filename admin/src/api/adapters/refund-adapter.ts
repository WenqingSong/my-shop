/**
 * 退款 Adapter：状态机、操作可用性、DTO → 视图模型。
 *
 * 退款状态（`api/refund/v1/refund.go` + DB TINYINT）：
 * applying(10) / pending_refund(20) / refund_failed(30) / review_rejected(40)
 * / revoked(50) / refund_succeeded(60) / closed(70)。
 *
 * 与订单状态是**两套独立枚举**：审核通过 ≠ 已退款，
 * 只有后端"执行退款成功"才把订单 paid→refunded 并恢复库存。
 */
import { formatCentsAsCny } from "@/utils/money";
import { formatServerDateTime } from "@/utils/datetime";
import type {
  RefundAuditAction,
  RefundAuditDTO,
  RefundDTO,
  RefundStatus
} from "../types/refund";

type TagType = "success" | "info" | "warning" | "danger" | "primary";

interface RefundStatusMeta {
  label: string;
  tag: TagType;
  terminal: boolean;
}

const REFUND_STATUS_META: Record<RefundStatus, RefundStatusMeta> = {
  applying: { label: "申请中", tag: "warning", terminal: false },
  pending_refund: { label: "待退款", tag: "primary", terminal: false },
  refund_failed: { label: "退款失败", tag: "danger", terminal: false },
  review_rejected: { label: "审核拒绝", tag: "info", terminal: true },
  revoked: { label: "已撤销", tag: "info", terminal: true },
  refund_succeeded: { label: "退款成功", tag: "success", terminal: true },
  closed: { label: "已关闭", tag: "info", terminal: true }
};

/** 状态筛选项（按状态机顺序） */
export const REFUND_STATUS_OPTIONS: Array<{
  value: RefundStatus;
  label: string;
}> = (
  [
    "applying",
    "pending_refund",
    "refund_failed",
    "review_rejected",
    "revoked",
    "refund_succeeded",
    "closed"
  ] as RefundStatus[]
).map(value => ({ value, label: REFUND_STATUS_META[value].label }));

/** 退款状态文案 */
export function formatRefundStatus(status: string): string {
  return (
    REFUND_STATUS_META[status as RefundStatus]?.label ?? `未知状态（${status}）`
  );
}

/** 退款状态标签颜色 */
export function refundStatusTagType(status: string): TagType {
  return REFUND_STATUS_META[status as RefundStatus]?.tag ?? "danger";
}

/** 是否终态（仅展示用） */
export function isTerminalRefundStatus(status: string): boolean {
  return REFUND_STATUS_META[status as RefundStatus]?.terminal ?? false;
}

/** 审计记录里的状态是 DB TINYINT，需要单独映射 */
const REFUND_STATUS_BY_CODE: Record<number, string> = {
  0: "—",
  10: "申请中",
  20: "待退款",
  30: "退款失败",
  40: "审核拒绝",
  50: "已撤销",
  60: "退款成功",
  70: "已关闭"
};

/** 用 DB 状态码展示文案（审计 before/after） */
export function formatRefundStatusByCode(code: number): string {
  return REFUND_STATUS_BY_CODE[code] ?? `未知状态（${code}）`;
}

/** 审计动作文案（后端真实 action 值） */
const AUDIT_ACTION_LABELS: Record<RefundAuditAction, string> = {
  apply: "提交退款申请",
  review_approve: "审核通过",
  review_reject: "审核拒绝",
  execute_success: "确认退款成功",
  execute_failure: "确认退款失败",
  close: "关闭退款",
  revoke: "用户撤销申请"
};

/** 审计动作文案 */
export function formatAuditAction(action: string): string {
  return (
    AUDIT_ACTION_LABELS[action as RefundAuditAction] ?? `未知动作（${action}）`
  );
}

/** 审计动作标签颜色 */
export function auditActionTagType(action: string): TagType {
  switch (action) {
    case "review_approve":
    case "execute_success":
      return "success";
    case "review_reject":
    case "execute_failure":
    case "close":
      return "danger";
    case "revoke":
      return "info";
    default:
      return "primary";
  }
}

/** 操作者类型文案（1=用户，2=管理员） */
export function formatOperatorType(type: number): string {
  if (type === 1) return "用户";
  if (type === 2) return "管理员";
  return `未知类型（${type}）`;
}

/** 退款视图 */
export interface RefundView {
  id: number;
  orderId: number;
  userId: number;
  status: RefundStatus;
  statusLabel: string;
  statusTagType: TagType;
  amountCents: number;
  amountText: string;
  reason: string;
  reasonText: string;
  reviewReasonText: string;
  reviewedByText: string;
  reviewedAtText: string;
  executeReasonText: string;
  executedByText: string;
  executedAtText: string;
  createdText: string;
  updatedText: string;
}

/** 退款 DTO → 视图 */
export function toRefundView(dto: RefundDTO): RefundView {
  const reason = (dto.reason ?? "").trim();
  const reviewReason = (dto.review_reason ?? "").trim();
  const executeReason = (dto.execute_reason ?? "").trim();

  return {
    id: dto.id,
    orderId: dto.order_id,
    userId: dto.user_id,
    status: dto.status,
    statusLabel: formatRefundStatus(dto.status),
    statusTagType: refundStatusTagType(dto.status),
    amountCents: dto.amount,
    amountText: formatCentsAsCny(dto.amount),
    reason,
    reasonText: reason || "—",
    reviewReasonText: reviewReason || "—",
    reviewedByText:
      dto.reviewed_by === null ? "—" : `管理员 #${dto.reviewed_by}`,
    reviewedAtText: formatServerDateTime(dto.reviewed_at),
    executeReasonText: executeReason || "—",
    executedByText:
      dto.executed_by === null ? "—" : `管理员 #${dto.executed_by}`,
    executedAtText: formatServerDateTime(dto.executed_at),
    createdText: formatServerDateTime(dto.created_at),
    updatedText: formatServerDateTime(dto.updated_at)
  };
}

/** 审计视图 */
export interface RefundAuditView {
  id: number;
  action: string;
  actionLabel: string;
  actionTagType: TagType;
  operatorText: string;
  beforeStatusText: string;
  afterStatusText: string;
  reasonText: string;
  createdText: string;
}

/** 审计 DTO → 视图 */
export function toRefundAuditView(dto: RefundAuditDTO): RefundAuditView {
  const reason = (dto.reason ?? "").trim();
  return {
    id: dto.id,
    action: dto.action,
    actionLabel: formatAuditAction(dto.action),
    actionTagType: auditActionTagType(dto.action),
    operatorText: `${formatOperatorType(dto.operator_type)} ${dto.operator_username || `#${dto.operator_id}`}`,
    beforeStatusText: formatRefundStatusByCode(dto.before_status),
    afterStatusText: formatRefundStatusByCode(dto.after_status),
    reasonText: reason || "—",
    createdText: formatServerDateTime(dto.created_at)
  };
}

/** 后端原因字段最大字符数（VARCHAR(255)） */
export const REFUND_REASON_MAX_LENGTH = 255;

/**
 * 退款原因/审核意见校验。
 *
 * 后端对 review / execute / close 的 `reason` 都是必填且 ≤255 字符，
 * 前端只做提前提示，最终仍以后端 400 为准。
 */
export function validateRefundReason(reason: string): string | null {
  const trimmed = reason.trim();
  if (!trimmed) return "请填写原因";
  if ([...trimmed].length > REFUND_REASON_MAX_LENGTH) {
    return `原因不能超过 ${REFUND_REASON_MAX_LENGTH} 个字符`;
  }
  return null;
}

/** 退款可执行动作 */
export interface RefundActions {
  /** 审核（仅申请中） */
  review: boolean;
  /** 执行退款成功（待退款 / 退款失败重试） */
  executeSuccess: boolean;
  /** 执行退款失败（仅待退款） */
  executeFailure: boolean;
  /** 关闭退款（仅退款失败） */
  close: boolean;
}

/** 按状态计算可用动作（权限在页面另行叠加） */
export function getRefundActions(status: string): RefundActions {
  return {
    review: status === "applying",
    executeSuccess: status === "pending_refund" || status === "refund_failed",
    executeFailure: status === "pending_refund",
    close: status === "refund_failed"
  };
}

/** 退款操作弹窗模式 */
export type RefundActionMode =
  | "review_approve"
  | "review_reject"
  | "execute_success"
  | "execute_failure"
  | "close";

interface RefundActionMeta {
  title: string;
  submitLabel: string;
  /** 操作说明（弹窗内展示） */
  description: string;
  /** 二次确认补充提示 */
  warning: string;
  danger: boolean;
}

export const REFUND_ACTION_META: Record<RefundActionMode, RefundActionMeta> = {
  review_approve: {
    title: "审核通过退款申请",
    submitLabel: "通过审核",
    description:
      "审核通过后申请进入「待退款」，此时**资金并未到账**，还需执行退款并确认结果。",
    warning: "确认通过该退款申请？",
    danger: false
  },
  review_reject: {
    title: "拒绝退款申请",
    submitLabel: "确认拒绝",
    description:
      "拒绝后申请进入终态「审核拒绝」，订单保持已支付状态，不会恢复库存。",
    warning: "确认拒绝该退款申请？",
    danger: true
  },
  execute_success: {
    title: "确认退款成功",
    submitLabel: "确认退款成功",
    description:
      "后端将在同一事务内：订单 已支付→已退款、恢复订单商品库存、退款 待退款/退款失败→退款成功。该结果由管理员人工确认（Mock 语义），不代表接入了真实支付渠道。",
    warning: "确认该笔退款已经成功？操作不可撤销。",
    danger: false
  },
  execute_failure: {
    title: "确认退款失败",
    submitLabel: "标记退款失败",
    description:
      "仅「待退款」可标记失败；订单状态与库存不变，之后可重试退款成功或关闭退款。",
    warning: "确认该笔退款执行失败？",
    danger: true
  },
  close: {
    title: "关闭退款申请",
    submitLabel: "关闭退款",
    description:
      "仅「退款失败」可关闭。关闭后订单保持已支付、不恢复库存，可继续发货。",
    warning: "确认关闭该退款申请？",
    danger: true
  }
};
