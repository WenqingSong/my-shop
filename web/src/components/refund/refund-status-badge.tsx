import type { RefundStatus } from '@/api/refunds'
import { cn } from '@/utils/cn'
import {
  describeRefundStatus,
  describeRefundStatusTone,
  type RefundStatusTone,
} from '@/utils/refunds'

const toneClass: Record<RefundStatusTone, string> = {
  warning: 'bg-warning-soft text-warning',
  primary: 'bg-primary-soft text-primary',
  success: 'bg-success-soft text-success',
  neutral: 'bg-gray-100 text-gray-600',
  danger: 'bg-danger-soft text-danger',
}

export interface RefundStatusBadgeProps {
  status: RefundStatus | string
  className?: string
}

/**
 * 退款状态徽标。
 *
 * 只做「后端状态枚举 → 中文文案 + 颜色」的映射；
 * 文案来源 `utils/refunds.ts` 的 `REFUND_STATUS_LABELS`（与后端 7 态一一对应）。
 *
 * 不使用「已到账 / 已退回」等资金语义：项目没有真实资金退回能力。
 */
export function RefundStatusBadge({ status, className }: RefundStatusBadgeProps) {
  return (
    <span
      data-refund-status={status}
      className={cn(
        'inline-flex items-center rounded-full px-2.5 py-1 text-xs font-medium',
        toneClass[describeRefundStatusTone(status)],
        className,
      )}
    >
      {describeRefundStatus(status)}
    </span>
  )
}
