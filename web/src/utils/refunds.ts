import type { Refund, RefundStatus } from '@/api/refunds'
import type { Order } from '@/api/orders'

/**
 * 退款展示工具。
 *
 * 事实来源：
 * - 状态枚举：`api/refund/v1/refund.go:10-18`（7 态）；
 * - 状态机：`internal/logic/refund/refund.go` 与 `docs/design/refund.md`；
 * - 金额：`refund_requests.amount` = `orders.total_amount` 快照。
 *
 * 本模块只做「文案、色调、可操作性判断、格式化」，不计算任何金额，
 * 也不构造后端不存在的状态、时间轴或资金渠道。
 */

export const REFUND_STATUS_LABELS: Record<RefundStatus, string> = {
  applying: '申请中',
  pending_refund: '待退款',
  refund_failed: '退款失败',
  review_rejected: '审核拒绝',
  revoked: '已撤销',
  refund_succeeded: '退款成功',
  closed: '已关闭',
}

export function describeRefundStatus(status: RefundStatus | string): string {
  return REFUND_STATUS_LABELS[status as RefundStatus] ?? status
}

/** 状态徽标色调（只影响展示，不改变状态语义）。 */
export type RefundStatusTone = 'warning' | 'primary' | 'success' | 'neutral' | 'danger'

export function describeRefundStatusTone(status: RefundStatus | string): RefundStatusTone {
  switch (status) {
    case 'applying':
      return 'warning'
    case 'pending_refund':
      return 'primary'
    case 'refund_failed':
      return 'danger'
    case 'refund_succeeded':
      return 'success'
    case 'review_rejected':
    case 'revoked':
    case 'closed':
    default:
      return 'neutral'
  }
}

/**
 * 状态筛选选项：全部 + 后端返回的全部 7 个状态。
 *
 * 不做「处理中/已结束」之类的自行分组——那会引入后端不存在的业务分类。
 * 这是纯前端筛选（后端没有 status 查询参数）。
 */
export const REFUND_STATUS_FILTERS: readonly { id: 'all' | RefundStatus; label: string }[] = [
  { id: 'all', label: '全部' },
  { id: 'applying', label: REFUND_STATUS_LABELS.applying },
  { id: 'pending_refund', label: REFUND_STATUS_LABELS.pending_refund },
  { id: 'refund_failed', label: REFUND_STATUS_LABELS.refund_failed },
  { id: 'review_rejected', label: REFUND_STATUS_LABELS.review_rejected },
  { id: 'refund_succeeded', label: REFUND_STATUS_LABELS.refund_succeeded },
  { id: 'revoked', label: REFUND_STATUS_LABELS.revoked },
  { id: 'closed', label: REFUND_STATUS_LABELS.closed },
]

export type RefundStatusFilterId = 'all' | RefundStatus

/** 按状态筛选（本地过滤，不产生网络请求）。 */
export function filterRefundsByStatus(
  refunds: readonly Refund[],
  filter: RefundStatusFilterId,
): Refund[] {
  if (filter === 'all') {
    return [...refunds]
  }

  return refunds.filter((refund) => refund.status === filter)
}

/**
 * 有效退款状态 = {applying, pending_refund, refund_failed}。
 *
 * 与后端 `activeRefundStatuses`（`internal/logic/refund/refund.go:41`）一致：
 * 这三种状态会阻止同一订单再次申请、并阻塞发货。
 */
export const ACTIVE_REFUND_STATUSES: readonly RefundStatus[] = [
  'applying',
  'pending_refund',
  'refund_failed',
]

export function hasActiveRefund(refunds: readonly Refund[]): boolean {
  return refunds.some((refund) => ACTIVE_REFUND_STATUSES.includes(refund.status))
}

/** 订单当前是否存在有效退款申请（用于控制「再次申请」入口）。 */
export function findActiveRefund(refunds: readonly Refund[]): Refund | null {
  return refunds.find((refund) => ACTIVE_REFUND_STATUSES.includes(refund.status)) ?? null
}

/** 后端唯一允许撤销的状态：applying(10)。其余状态调用撤销必然 18002。 */
export function canRevokeRefund(refund: Pick<Refund, 'status'>): boolean {
  return refund.status === 'applying'
}

/**
 * 后端唯一允许申请退款的订单状态：paid(20)（已支付、未发货）。
 *
 * `internal/logic/refund/refund.go:159-161` 用 `snap.Status != orderStatusPaid`
 * 直接拒绝其它一切状态（18003）。前端只做入口可见性，不做业务判定。
 */
export function canApplyRefund(order: Pick<Order, 'status'>): boolean {
  return order.status === 'paid'
}

/**
 * 退款是否「已经走完内部业务处理」。
 *
 * 重要：`refund_succeeded` 只表示退款模块内部状态推进完成 + 订单变为 refunded，
 * **不代表真实资金已退回**（项目使用 Mock 支付，没有支付网关与资金流水）。
 * 页面必须同时展示这一声明，不能把状态描述为"已到账"。
 */
export function isRefundSettled(status: RefundStatus | string): boolean {
  return status === 'refund_succeeded'
}

/** 格式化后端返回的时间字符串：只截断、不转换时区、不补默认值。 */
export function formatRefundTime(value: string | null): string {
  return value ? value.slice(0, 19) : '—'
}

/**
 * 退款状态的用户侧说明。
 *
 * 文案严格对应后端状态机，不描述任何资金动作（如"正在原路退回"）。
 */
export function describeRefundStatusHint(status: RefundStatus | string): string {
  switch (status) {
    case 'applying':
      return '申请已提交，等待商家审核。审核通过前可以撤销申请。'
    case 'pending_refund':
      return '商家已审核通过，等待退款处理。此阶段不可撤销。'
    case 'refund_failed':
      return '退款处理未成功，订单仍保持已支付状态；商家可以重新处理或关闭该申请。'
    case 'review_rejected':
      return '申请已被商家拒绝，订单保持已支付状态。如有疑问请联系商家。'
    case 'revoked':
      return '申请已由你撤销，订单保持已支付状态。如需退款可以重新申请。'
    case 'refund_succeeded':
      return '退款申请已在系统内处理完成，关联订单状态已更新为「已退款」。'
    case 'closed':
      return '退款申请已关闭，订单保持已支付状态。'
    default:
      return '当前状态暂无更多说明。'
  }
}
